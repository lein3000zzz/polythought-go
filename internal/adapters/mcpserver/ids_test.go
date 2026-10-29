package mcpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/mcpserver"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/tasks"
)

// slowDesk hands out no task: each wait lasts until it is let go, or cancelled.
type slowDesk struct {
	waiting   chan struct{} // one each wait that starts
	release   chan struct{}
	cancelled chan struct{} // one each wait cancelled
}

func (d *slowDesk) Wait(ctx context.Context, _ auth.Principal) (tasks.Task, bool, error) {
	d.waiting <- struct{}{}

	select {
	case <-d.release:
		return tasks.Task{}, false, nil
	case <-ctx.Done():
		d.cancelled <- struct{}{}
		return tasks.Task{}, false, ctx.Err()
	}
}

func (*slowDesk) Answer(context.Context, auth.Principal, domain.ExecutionID, any, []any) error {
	return nil
}

func (*slowDesk) Fail(context.Context, auth.Principal, domain.ExecutionID, string) error { return nil }

// rawAgent posts to /mcp as the bare protocol has it, the session's ID on each.
type rawAgent struct {
	t       *testing.T
	url     string
	session string
}

func (a *rawAgent) post(ctx context.Context, body string) (int, string) {
	a.t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, bytes.NewBufferString(body))
	require.NoError(a.t, err)
	req.Header.Set("Authorization", "Bearer poa_agent")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	if a.session != "" {
		req.Header.Set("Mcp-Session-Id", a.session)
		req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = res.Body.Close() }()

	if a.session == "" {
		a.session = res.Header.Get("Mcp-Session-Id")
	}

	data, _ := io.ReadAll(res.Body)

	return res.StatusCode, string(data)
}

func (a *rawAgent) open() {
	a.t.Helper()

	status, body := a.post(context.Background(), `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"subagents","version":"1"}}}`)
	require.Equal(a.t, http.StatusOK, status, body)
	require.NotEmpty(a.t, a.session)

	status, body = a.post(context.Background(), `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	require.Equal(a.t, http.StatusAccepted, status, body)
}

func toolCall(id any, name string) string {
	data, _ := json.Marshal(id)
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{"name":%q,"arguments":{}}}`, data, name)
}

// answerOf is the one message of an answer, as a stream or as JSON.
func answerOf(t *testing.T, body string) map[string]any {
	t.Helper()

	for line := range strings.SplitSeq(body, "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			body = data
		}
	}

	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &m), body)

	return m
}

// An agent's subagents share its session, each numbering from 0: one waiting for a task
// keeps no other from asking with the same ID (D432).
func TestSharedSessionIDs(t *testing.T) {
	desk := &slowDesk{waiting: make(chan struct{}, 4), release: make(chan struct{}), cancelled: make(chan struct{}, 4)}
	d := deps(alice, &trades{}, []domain.Scope{domain.ScopeRead, domain.ScopeTasks})
	d.Tasks = desk

	srv, err := mcpserver.New(mcpserver.Config{PublicURL: publicURL}, d)
	require.NoError(t, err)

	h := httptest.NewServer(srv)
	t.Cleanup(h.Close)

	// Let go however the test ends, for the server to close.
	var letGo sync.Once
	t.Cleanup(func() { letGo.Do(func() { close(desk.release) }) })

	agent := &rawAgent{t: t, url: h.URL}
	agent.open()

	waited := make(chan string, 1)

	go func() {
		_, body := agent.post(context.Background(), toolCall(0, "wait_for_task"))
		waited <- body
	}()

	<-desk.waiting

	status, body := agent.post(context.Background(), toolCall(0, "whoami"))
	require.Equal(t, http.StatusOK, status, body)

	who := answerOf(t, body)
	assert.InDelta(t, 0, who["id"], 0, "its own ID back")
	assert.NotContains(t, who, "error")
	assert.Contains(t, fmt.Sprint(who["result"]), "alice@example.com")

	letGo.Do(func() { close(desk.release) })

	wait := answerOf(t, <-waited)
	assert.InDelta(t, 0, wait["id"], 0)
	assert.Contains(t, fmt.Sprint(wait["result"]), "No task came")

	t.Run("a cancellation reaches the caller's latest request of its ID", func(t *testing.T) {
		desk.release = make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		go agent.post(ctx, toolCall("w-1", "wait_for_task"))
		<-desk.waiting

		status, body := agent.post(context.Background(),
			`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"w-1","reason":"the subagent is done"}}`)
		require.Equal(t, http.StatusAccepted, status, body)

		select {
		case <-desk.cancelled:
		case <-time.After(5 * time.Second):
			t.Fatal("the wait was not cancelled")
		}
	})
}
