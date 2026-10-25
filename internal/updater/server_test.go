package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

const testToken = "0123456789abcdef0123456789abcdef"

// gate is a job that waits to be let through, then fails as told.
type gate struct {
	through chan struct{}
	fail    error
	asked   chan string
}

func newGate() *gate {
	return &gate{through: make(chan struct{}), asked: make(chan string, 4)}
}

func (g *gate) job(_ context.Context, version string, out io.Writer) error {
	g.asked <- version
	<-g.through
	_, _ = fmt.Fprintln(out, "pulled", version)

	return g.fail
}

func newServer(t *testing.T, job Job) (*Server, *httptest.Server) {
	t.Helper()

	s, err := NewServer(context.Background(), testToken, job, logging.Noop())
	require.NoError(t, err)

	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	return s, srv
}

func call(t *testing.T, srv *httptest.Server, method, path, token, body string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, strings.NewReader(body))
	require.NoError(t, err)

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	res, err := srv.Client().Do(req)
	require.NoError(t, err)

	defer func() { _ = res.Body.Close() }()

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	return res.StatusCode, string(b)
}

func TestNewServer(t *testing.T) {
	_, err := NewServer(context.Background(), "short", nil, logging.Noop())
	assert.ErrorContains(t, err, "at least 32 characters")
}

func TestServerAuth(t *testing.T) {
	_, srv := newServer(t, newGate().job)

	code, _ := call(t, srv, http.MethodGet, PathHealth, "", "")
	assert.Equal(t, http.StatusOK, code, "health is open")

	for name, token := range map[string]string{"none": "", "another": strings.Repeat("x", 32), "a prefix": testToken[:31]} {
		t.Run(name, func(t *testing.T) {
			code, body := call(t, srv, http.MethodGet, PathStatus, token, "")
			assert.Equal(t, http.StatusUnauthorized, code)
			assert.JSONEq(t, `{"error":"the token is not the updater's"}`, body)

			code, _ = call(t, srv, http.MethodPost, PathUpdate, token, `{"version":"0.0.2"}`)
			assert.Equal(t, http.StatusUnauthorized, code, "nothing updated")
		})
	}
}

func TestServerUpdate(t *testing.T) {
	g := newGate()
	s, srv := newServer(t, g.job)

	code, body := call(t, srv, http.MethodGet, PathStatus, testToken, "")
	assert.Equal(t, http.StatusOK, code)
	assert.JSONEq(t, `{"state":"idle"}`, body)

	for _, bad := range []string{`{"version":"v0.0.2"}`, `{"version":"0.0.2; reboot"}`, `{"version":"0.0.2","then":"x"}`, `0.0.2`} {
		code, _ = call(t, srv, http.MethodPost, PathUpdate, testToken, bad)
		assert.Equal(t, http.StatusBadRequest, code, bad)
	}

	code, body = call(t, srv, http.MethodPost, PathUpdate, testToken, `{"version":"0.0.2"}`)
	assert.Equal(t, http.StatusAccepted, code)
	assert.Contains(t, body, `"state":"updating","version":"0.0.2"`)
	assert.Equal(t, "0.0.2", <-g.asked)

	code, body = call(t, srv, http.MethodPost, PathUpdate, testToken, `{"version":"0.0.3"}`)
	assert.Equal(t, http.StatusConflict, code, "one at a time")
	assert.JSONEq(t, `{"error":"an update is under way already, to 0.0.2"}`, body)

	close(g.through)
	s.Wait()

	_, body = call(t, srv, http.MethodGet, PathStatus, testToken, "")
	assert.Contains(t, body, `"state":"done","version":"0.0.2","log":"pulled 0.0.2\n"`)
	assert.Contains(t, body, `"finishedAt":`)

	t.Run("a failed update says why", func(t *testing.T) {
		g.through, g.fail = make(chan struct{}), errors.New("pulling the images: docker compose: exit status 1")
		close(g.through)

		code, _ := call(t, srv, http.MethodPost, PathUpdate, testToken, `{"version":"0.0.3"}`)
		require.Equal(t, http.StatusAccepted, code, "the last one done, another may start")
		s.Wait()

		_, body := call(t, srv, http.MethodGet, PathStatus, testToken, "")
		assert.Contains(t, body, `"state":"failed","version":"0.0.3","error":"pulling the images: docker compose: exit status 1"`)
	})
}

// The server's client, against the updater itself.
func TestClient(t *testing.T) {
	g := newGate()
	s, srv := newServer(t, g.job)

	cfg := ClientConfig{URL: srv.URL + "/", Token: testToken}
	c := NewClient(func() ClientConfig { return cfg }, nil)
	ctx := context.Background()

	assert.True(t, c.Set())

	st, err := c.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, domain.UpdaterIdle, st.State)

	require.NoError(t, c.Update(ctx, "0.0.2"))
	<-g.asked
	assert.ErrorIs(t, c.Update(ctx, "0.0.3"), domain.ErrUpdating)

	close(g.through)
	s.Wait()

	st, err = c.Status(ctx)
	require.NoError(t, err)
	assert.Equal(t, domain.UpdaterStatus{
		State: domain.UpdaterDone, Version: "0.0.2", Log: "pulled 0.0.2\n", StartedAt: st.StartedAt, FinishedAt: st.FinishedAt,
	}, st)
	assert.False(t, st.FinishedAt.Before(st.StartedAt))

	t.Run("refused", func(t *testing.T) {
		cfg.Token = strings.Repeat("x", 32)

		err := c.Update(ctx, "0.0.3")
		require.ErrorIs(t, err, domain.ErrUnavailable)
		assert.ErrorContains(t, err, "the updater refused the server's token")
	})

	t.Run("not there", func(t *testing.T) {
		gone := httptest.NewServer(http.NotFoundHandler())
		gone.Close()
		cfg = ClientConfig{URL: gone.URL, Token: testToken}

		_, err := c.Status(ctx)
		require.ErrorIs(t, err, domain.ErrUnavailable)
		assert.ErrorContains(t, err, "the updater did not answer at "+gone.URL+": is it running?")
	})

	t.Run("not set up", func(t *testing.T) {
		cfg = ClientConfig{URL: srv.URL}

		assert.False(t, c.Set())
		assert.ErrorIs(t, c.Update(ctx, "0.0.3"), domain.ErrNoUpdater)
	})
}

func TestTail(t *testing.T) {
	tl := &tail{max: 8}

	_, _ = io.WriteString(tl, "0123")
	_, _ = io.WriteString(tl, "456789")
	assert.Equal(t, "23456789", tl.String(), "the end kept")

	_, _ = io.WriteString(tl, "abc€")
	assert.Equal(t, "89abc€", tl.String())

	_, _ = io.WriteString(tl, "1234567")
	assert.Equal(t, "1234567", tl.String(), "never half a character: € was cut")
}
