package ai_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/ai"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// around says whether the owner has an agent taking tasks.
type around bool

func (a around) Around(domain.UserID) bool { return bool(a) }

var asked = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func localRun(t *testing.T, agents ports.TaskAgents, params map[string]any, items ...map[string]any) (engine.Ports, error) {
	t.Helper()

	node := ai.NewLocalAgent(agents, func() time.Time { return asked })

	in := engine.Ports{}
	for _, it := range items {
		in.Add(engine.DefaultPort, engine.Item{JSON: it})
	}

	return node.Run(context.Background(), engine.RunContext{OwnerID: owner, Params: engine.LiteralParams(node.Type(), params)}, in)
}

// The local agent node's own part (D386): what it hands out, and what it makes of the
// answer.
func TestLocalAgent(t *testing.T) {
	t.Run("none around: every item fails at once", func(t *testing.T) {
		for _, agents := range []ports.TaskAgents{around(false), nil} {
			out, err := localRun(t, agents, map[string]any{"prompt": "Look"}, map[string]any{"q": 1}, map[string]any{"q": 2})
			require.NoError(t, err)
			require.Len(t, out[engine.ErrorPort], 2)
			assert.Contains(t, out[engine.ErrorPort][0].Err.Error(), "none of your agents is waiting for tasks")
		}
	})

	t.Run("together without the items", func(t *testing.T) {
		_, err := localRun(t, around(true), map[string]any{"prompt": "Sum up", "call": "together", "include": false}, map[string]any{"q": 1})

		var wait *engine.Wait
		require.ErrorAs(t, err, &wait)
		assert.Equal(t, &ports.WaitTask{Call: ai.CallTogether, AnswerAs: ai.AnswerText, Prompt: "Sum up"}, wait.Task)
		assert.Equal(t, asked.Add(10*time.Minute), wait.Until)
		assert.NotEmpty(t, wait.Key)
	})

	t.Run("a task too big for an agent's call", func(t *testing.T) {
		_, err := localRun(t, around(true), map[string]any{"prompt": "Look"}, map[string]any{"text": strings.Repeat("x", 30<<10)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "more than an agent takes at once (24 KiB)")
	})

	t.Run("settings it cannot work with", func(t *testing.T) {
		_, err := localRun(t, around(true), map[string]any{"prompt": " "}, map[string]any{"q": 1})
		require.ErrorContains(t, err, "task: what to ask is empty")

		_, err = localRun(t, around(true), map[string]any{"prompt": "Look", "answerAs": "schema", "schema": "{"}, map[string]any{"q": 1})
		require.ErrorContains(t, err, "JSON schema")
	})

	t.Run("each answer held to the shape, one bad failing alone", func(t *testing.T) {
		node := ai.NewLocalAgent(around(true), func() time.Time { return asked })
		params := map[string]any{"prompt": "Look", "answerAs": "fields", "fields": []any{map[string]any{"name": "watch", "type": "true or false"}}}

		in := engine.Ports{}
		in.Add(engine.DefaultPort, engine.Item{JSON: map[string]any{"q": 1}}, engine.Item{JSON: map[string]any{"q": 2}})
		rc := engine.RunContext{OwnerID: owner, Params: engine.LiteralParams(node.Type(), params)}

		_, err := node.Run(context.Background(), rc, in)

		var wait *engine.Wait
		require.ErrorAs(t, err, &wait)

		out, err := node.Resume(context.Background(), rc, in, engine.Resumption{
			State:   wait.State,
			Payload: map[string]any{ai.TaskAnswers: []any{map[string]any{"watch": true}, map[string]any{"watch": "yes"}}, engine.AnsweredBy: "Codex"},
		})
		require.NoError(t, err)
		require.Len(t, out[engine.DefaultPort], 1)
		assert.Equal(t, map[string]any{"watch": true}, out[engine.DefaultPort][0].JSON["answer"].(map[string]any)["json"])
		assert.Equal(t, "Codex", out[engine.DefaultPort][0].JSON["answer"].(map[string]any)["by"])
		require.Len(t, out[engine.ErrorPort], 1)
		assert.Equal(t, 2, out[engine.ErrorPort][0].JSON["q"])

		out, err = node.Resume(context.Background(), rc, in, engine.Resumption{State: wait.State, Payload: map[string]any{ai.TaskAnswers: []any{}}})
		require.NoError(t, err)
		require.Len(t, out[engine.ErrorPort], 2)
		assert.Contains(t, out[engine.ErrorPort][0].Err.Error(), "0 answers for 2 items")

		out, err = node.Resume(context.Background(), rc, in, engine.Resumption{
			State: wait.State, Payload: map[string]any{ai.TaskFailed: "no forecast", engine.AnsweredBy: "Codex"},
		})
		require.NoError(t, err)
		assert.EqualError(t, out[engine.ErrorPort][1].Err, "Codex could not do it: no forecast")
	})

	t.Run("an answer as text, or JSON as text", func(t *testing.T) {
		_, err := ai.CheckAnswer(ports.WaitTask{AnswerAs: ai.AnswerText}, map[string]any{"a": 1})
		require.ErrorContains(t, err, "the answer is text")

		got, err := ai.CheckAnswer(ports.WaitTask{AnswerAs: ai.AnswerSchema, Schema: []byte(`{"type":"object","required":["n"]}`)}, "```json\n{\"n\": 2}\n```")
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"n": 2.0}, got)

		_, err = ai.CheckAnswer(ports.WaitTask{AnswerAs: ai.AnswerSchema, Schema: []byte(`{"type":"object","required":["n"]}`)}, map[string]any{})
		require.Error(t, err, "n is required")
	})
}
