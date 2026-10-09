package httpapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/events"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

// client does not keep connections alive, so Shutdown need not wait for idle ones.
var client = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

// startServer runs srv on a real listener: streaming bypasses Hertz's in-process test
// helper. It returns the base URL.
func startServer(t *testing.T, services httpapi.Services) (string, *httpapi.Server) {
	t.Helper()

	addr := freeAddr(t)

	srv, err := httpapi.New(httpapi.Config{Addr: addr, PublicURL: publicURL}, observability.NoopUtility(), services)
	require.NoError(t, err)

	go func() { _ = srv.Run() }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	require.Eventually(t, func() bool {
		resp, err := client.Get("http://" + addr + "/healthz")
		if err != nil {
			return false
		}

		_ = resp.Body.Close()

		return resp.StatusCode == http.StatusOK
	}, 3*time.Second, 10*time.Millisecond)

	return "http://" + addr, srv
}

// nextEvent reads the stream up to the next blank line and returns its non-comment lines.
func nextEvent(t *testing.T, r *bufio.Reader) []string {
	t.Helper()

	var lines []string

	for {
		line, err := r.ReadString('\n')
		require.NoError(t, err)

		line = strings.TrimRight(line, "\n")
		switch {
		case line == "" && len(lines) > 0:
			return lines
		case line == "", strings.HasPrefix(line, ":"):
			continue
		default:
			lines = append(lines, line)
		}
	}
}

func TestEventStream(t *testing.T) {
	hub, err := events.NewHub(observability.NoopUtility())
	require.NoError(t, err)

	authSvc := httpapimocks.NewMockAuthService(t)
	withSession(authSvc, alice)

	base, srv := startServer(t, httpapi.Services{Auth: authSvc, Events: hub})

	req, err := http.NewRequest(http.MethodGet, base+"/api/v1/events", nil)
	require.NoError(t, err)
	req.Header.Set("Cookie", cookie["Cookie"])

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	stream := bufio.NewReader(resp.Body)
	assert.Equal(t, []string{"retry: 5000"}, nextEvent(t, stream))

	finished := domain.Execution{
		ID: "ex-1", WorkflowID: "wf-1", Status: domain.ExecutionFailed, Error: "boom",
		Runs: []domain.NodeRun{{
			NodeID: "manual", Status: domain.NodeRunSucceeded, Output: map[string]int{"main": 1},
			Samples: map[string][]domain.ItemSample{"main": {{JSON: json.RawMessage(`{}`)}}},
		}},
	}

	hub.ExecutionFinished("someone-else", domain.Execution{ID: "ex-0", WorkflowID: "wf-9"})
	hub.ExecutionFinished(alice.ID, finished)

	lines := nextEvent(t, stream)
	require.Len(t, lines, 2)
	assert.Equal(t, "event: execution", lines[0])

	var ev struct {
		WorkflowID string `json:"workflowId"`
		Execution  struct {
			ID     string           `json:"id"`
			Status string           `json:"status"`
			Error  string           `json:"error"`
			Runs   []map[string]any `json:"runs"`
		} `json:"execution"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(lines[1], "data: ")), &ev))

	assert.Equal(t, "wf-1", ev.WorkflowID, "only the owner's executions arrive")
	assert.Equal(t, "ex-1", ev.Execution.ID)
	assert.Equal(t, "boom", ev.Execution.Error)
	require.Len(t, ev.Execution.Runs, 1)
	assert.NotContains(t, ev.Execution.Runs[0], "samples", "events stay small")

	// Shutting down ends the stream instead of waiting for the client.
	done := make(chan error, 1)
	go func() { done <- srv.Shutdown(context.Background()) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown waited for the open stream")
	}
}

func TestEventStreamNeedsAuthentication(t *testing.T) {
	hub, err := events.NewHub(observability.NoopUtility())
	require.NoError(t, err)

	base, _ := startServer(t, httpapi.Services{Auth: httpapimocks.NewMockAuthService(t), Events: hub})

	resp, err := client.Get(base + "/api/v1/events")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
