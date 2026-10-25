package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// fakeGitHub answers for one repository as GitHub's API does, and keeps the request.
func fakeGitHub(t *testing.T, status int, body string) (*Client, *http.Request) {
	t.Helper()

	got := &http.Request{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = *r.Clone(context.Background())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	s, err := New(observability.NoopUtility(), Config{Repo: "lein3000zzz/polythought-go", BaseURL: srv.URL + "/"})
	require.NoError(t, err)

	return s, got
}

func TestStars(t *testing.T) {
	s, req := fakeGitHub(t, http.StatusOK, `{"full_name":"lein3000zzz/polythought-go","stargazers_count":1234,"forks_count":56}`)

	n, err := s.Stars(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1234, n)

	assert.Equal(t, "/repos/lein3000zzz/polythought-go", req.URL.Path)
	assert.Equal(t, "polythought", req.Header.Get("User-Agent"), "GitHub refuses requests without one")
	assert.Equal(t, "application/vnd.github+json", req.Header.Get("Accept"))
	assert.Empty(t, req.Header.Get("Authorization"), "no token")
}

func TestStarsFailing(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   error
	}{
		"private or gone": {http.StatusNotFound, `{"message":"Not Found"}`, domain.ErrNotFound},
		"rate limited":    {http.StatusForbidden, `{"message":"API rate limit exceeded"}`, domain.ErrUnavailable},
		"down":            {http.StatusBadGateway, ``, domain.ErrUnavailable},
		"no count":        {http.StatusOK, `{"full_name":"lein3000zzz/polythought-go"}`, domain.ErrUnavailable},
		"not JSON":        {http.StatusOK, `<html>`, domain.ErrUnavailable},
		"negative":        {http.StatusOK, `{"stargazers_count":-1}`, domain.ErrUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := fakeGitHub(t, tc.status, tc.body)

			_, err := s.Stars(context.Background())
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

func TestStarsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()

	s, err := New(observability.NoopUtility(), Config{Repo: "lein3000zzz/polythought-go", BaseURL: srv.URL})
	require.NoError(t, err)

	_, err = s.Stars(context.Background())
	assert.ErrorIs(t, err, domain.ErrUnavailable)
}
