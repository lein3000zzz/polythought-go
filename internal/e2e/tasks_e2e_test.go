//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A Local agent node hands its task to the user's agent waiting for one, which answers it
// (D386, D412, D413); the user sees who has it, hands it back, and answers one themself.
func TestAgentTakesATask(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	base := env("E2E_BASE_URL", "http://localhost:3100")
	b := signIn(t, base)

	var token struct{ ID, Token string }
	b.call(http.MethodPost, "/api/v1/tokens", map[string]any{"name": "e2e tasks", "scopes": []string{"read", "tasks"}}, http.StatusCreated, &token)

	agent, err := mcp.NewClient(&mcp.Implementation{Name: "go-sdk-e2e-tasks", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: base + "/mcp", HTTPClient: &http.Client{Transport: bearer(token.Token)},
	}, nil)
	require.NoError(t, err)

	defer func() { _ = agent.Close() }()

	var wf struct{ ID string }
	b.call(http.MethodPost, "/api/v1/workflows", map[string]any{
		"name": "Asks my agent",
		"nodes": []map[string]any{
			{"id": "manual", "type": "trigger.manual", "position": map[string]any{"x": 0, "y": 0}},
			{"id": "ask", "type": "ai.localAgent", "position": map[string]any{"x": 300, "y": 0}, "params": map[string]any{
				"prompt": "Name a colour", "answerAs": "fields", "fields": []any{map[string]any{"name": "colour", "type": "text"}},
			}},
		},
		"connections": []map[string]any{{"from": "manual", "fromPort": "main", "to": "ask", "toPort": "main"}},
	}, http.StatusCreated, &wf)

	type execution struct {
		ID, Status, Error string
		Runs              []struct {
			NodeID  string
			Samples map[string][]struct{ JSON map[string]any }
		}
	}

	run := func() execution {
		t.Helper()

		var e execution
		b.call(http.MethodPost, "/api/v1/workflows/"+wf.ID+"/run", map[string]any{}, http.StatusOK, &e)

		return e
	}

	// answerOf is what the node added to its item, once the run carried on.
	answerOf := func(id string) map[string]any {
		t.Helper()

		var e execution

		require.Eventually(t, func() bool {
			b.call(http.MethodGet, "/api/v1/workflows/"+wf.ID+"/executions/"+id, nil, http.StatusOK, &e)
			return e.Status != "waiting"
		}, 10*time.Second, 100*time.Millisecond)
		require.Equal(t, "succeeded", e.Status, e.Error)

		for _, r := range e.Runs {
			if r.NodeID == "ask" {
				return r.Samples["main"][0].JSON["answer"].(map[string]any)
			}
		}

		t.Fatal("no answer")

		return nil
	}

	// waiting has the agent wait for a task in the background, as it would.
	waiting := func() <-chan map[string]any {
		got := make(chan map[string]any, 1)

		go func() {
			defer close(got)

			res, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: "wait_for_task"})
			if err == nil && !res.IsError {
				if task, ok := res.StructuredContent.(map[string]any)["task"].(map[string]any); ok {
					got <- task
				}
			}
		}()

		return got
	}

	failed := run()
	assert.Equal(t, "failed", failed.Status, "none of the user's agents waits yet")
	assert.Contains(t, failed.Error, "none of your agents is waiting for tasks")

	got := waiting()

	require.Eventually(t, func() bool {
		var tokens struct {
			Tokens []struct {
				ID          string
				TakingTasks bool
			}
		}
		b.call(http.MethodGet, "/api/v1/tokens", nil, http.StatusOK, &tokens)

		for _, tk := range tokens.Tokens {
			if tk.ID == token.ID {
				return tk.TakingTasks
			}
		}

		return false
	}, 10*time.Second, 100*time.Millisecond, "marked as taking tasks")

	// Connected now, by its client (D414); and the steps' address, the server's.
	var sessions struct {
		Sessions []struct{ Kind, Name, Client string }
	}
	b.call(http.MethodGet, "/api/v1/me/agent-sessions", nil, http.StatusOK, &sessions)
	assert.Contains(t, sessions.Sessions, struct{ Kind, Name, Client string }{"token", "e2e tasks", "go-sdk-e2e-tasks 1"})

	var setup struct {
		Enabled bool
		Address string
	}
	b.call(http.MethodGet, "/api/v1/me/agent-setup", nil, http.StatusOK, &setup)
	assert.Equal(t, struct {
		Enabled bool
		Address string
	}{true, base + "/mcp"}, setup)

	time.Sleep(2 * time.Second) // the agent's call held open a while, through the proxy

	waits := run()
	require.Equal(t, "waiting", waits.Status, waits.Error)

	var task map[string]any
	select {
	case task = <-got:
	case <-ctx.Done():
		t.Fatal("no task handed to the agent")
	}

	require.NotNil(t, task, "handed to the agent waiting")
	assert.Equal(t, waits.ID, task["id"])
	assert.Equal(t, "Name a colour", task["untrusted"].(map[string]any)["items"].([]any)[0].(map[string]any)["prompt"])

	var status struct{ HeldBy string }
	b.call(http.MethodGet, "/api/v1/workflows/"+wf.ID+"/executions/"+waits.ID+"/task", nil, http.StatusOK, &status)
	assert.Equal(t, "the API token “e2e tasks”", status.HeldBy)

	b.call(http.MethodPost, "/api/v1/workflows/"+wf.ID+"/executions/"+waits.ID+"/task/hand-back", nil, http.StatusNoContent, nil)

	var free struct{ HeldBy string }
	b.call(http.MethodGet, "/api/v1/workflows/"+wf.ID+"/executions/"+waits.ID+"/task", nil, http.StatusOK, &free)
	assert.Empty(t, free.HeldBy, "handed back")

	again := tool(ctx, t, agent, "wait_for_task", nil)
	assert.Equal(t, waits.ID, again["task"].(map[string]any)["id"], "taken again at once")

	res, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: "answer_task", Arguments: map[string]any{
		"id": waits.ID, "answers": []any{map[string]any{"color": "teal"}},
	}})
	require.NoError(t, err)
	require.True(t, res.IsError, "not the task's shape")

	tool(ctx, t, agent, "answer_task", map[string]any{"id": waits.ID, "answers": []any{map[string]any{"colour": "teal"}}})

	answer := answerOf(waits.ID)
	assert.Equal(t, map[string]any{"colour": "teal"}, answer["json"])
	assert.Equal(t, "the API token “e2e tasks”", answer["by"])

	// The agent waited a moment ago, so a run hands it a task; the user answers this one.
	yours := run()
	require.Equal(t, "waiting", yours.Status, yours.Error)

	b.call(http.MethodPost, "/api/v1/workflows/"+wf.ID+"/executions/"+yours.ID+"/task/answer",
		map[string]any{"answers": []any{map[string]any{"colour": "red"}}}, http.StatusNoContent, nil)

	answer = answerOf(yours.ID)
	assert.Equal(t, map[string]any{"colour": "red"}, answer["json"])
	assert.Equal(t, "you", answer["by"])
}
