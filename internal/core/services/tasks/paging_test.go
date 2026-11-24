package tasks_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/ai"
	"github.com/lein3000zzz/polythought-go/internal/core/services/tasks"
)

// bulky are n items of about 2 KiB each: together far more than an agent takes at once.
func bulky(n int) []map[string]any {
	items := make([]map[string]any, n)
	for i := range items {
		items[i] = map[string]any{"n": i, "text": strings.Repeat("x", 2000)}
	}

	return items
}

// A task too big to take at once goes in parts (D436), each to an agent on its own, several
// at once; the run carries on once every part is answered, each item its own answer, in
// order, and every agent that answered named.
func TestTaskParts(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	first, second := f.waiting(claude), f.waiting(codex)
	f.around(t)

	res := f.run(t, map[string]any{"prompt": "Look into it"}, bulky(30)...)
	require.Equal(t, domain.ExecutionWaiting, res.Execution.Status)

	a, b := <-first, <-second
	require.Greater(t, a.Parts, 1, "too big for one")
	assert.Equal(t, a.Parts, b.Parts)
	assert.NotEqual(t, a.Part, b.Part, "two agents, two parts")

	byPart := map[int]tasks.Task{a.Part: a, b.Part: b}
	whoHas := map[int]string{a.Part: "claude", b.Part: "codex"}

	answer := func(who string, t tasks.Task) (int, error) {
		answers := make([]any, len(t.Task.Items))
		for i := range answers {
			answers[i] = fmt.Sprint("item ", t.First+i)
		}

		p := claude
		if who == "codex" {
			p = codex
		}

		return f.tasks.Answer(ctx, p, t.ID, t.Part, nil, answers)
	}

	for _, part := range []int{a.Part, b.Part} {
		task := byPart[part]
		assert.LessOrEqual(t, len(task.Task.Items)*2000, ai.MaxTask, "each part fits")

		left, err := answer(whoHas[part], task)
		require.NoError(t, err)
		assert.Positive(t, left, "a part still waits")
	}

	_, err := answer("codex", a)
	require.ErrorIs(t, err, tasks.ErrNoTask, "not codex's part")

	// The rest, a part at a time, by whichever agent waits.
	for {
		got := f.waiting(claude)
		task, ok := <-got
		if !ok {
			break
		}

		left, err := answer("claude", task)
		require.NoError(t, err)

		if left == 0 {
			break
		}
	}

	require.Eventually(t, func() bool { return len(f.captured.items()) == 30 }, 5*time.Second, time.Millisecond)

	for i, it := range f.captured.items() {
		got := it.JSON["answer"].(map[string]any)
		assert.Equal(t, fmt.Sprint("item ", i), got["text"], "in order")
		assert.Contains(t, got["by"], "Claude Code")
		assert.Contains(t, got["by"], "Codex", "every agent that answered")
	}
}

// Taken together, a task too big to take at once has its items in pages its agent reads
// before it answers once (D436).
func TestTaskPages(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	got := f.waiting(claude)
	f.around(t)

	res := f.run(t, map[string]any{"prompt": "Which of them matter?", "call": "together"}, bulky(30)...)
	require.Equal(t, domain.ExecutionWaiting, res.Execution.Status)

	task := <-got
	assert.Equal(t, 1, task.Parts, "one answer: one part")
	require.Greater(t, task.Pages, 1)

	seen := len(task.Task.Items)
	for page := 2; page <= task.Pages; page++ {
		items, pages, err := f.tasks.Page(ctx, claude, task.ID, page)
		require.NoError(t, err)
		assert.Equal(t, task.Pages, pages)
		assert.Equal(t, float64(seen), toFloat(items[0].Item["n"]), "after the page before")
		seen += len(items)
	}

	assert.Equal(t, 30, seen, "every item, once")

	_, _, err := f.tasks.Page(ctx, codex, task.ID, 2)
	require.ErrorIs(t, err, tasks.ErrNoTask, "the agent's that has it")

	_, _, err = f.tasks.Page(ctx, claude, task.ID, task.Pages+1)
	require.ErrorIs(t, err, domain.ErrInvalidInput)

	left, err := f.tasks.Answer(ctx, claude, task.ID, 0, "the first three", nil)
	require.NoError(t, err)
	assert.Zero(t, left)

	require.Eventually(t, func() bool { return len(f.captured.items()) == 1 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, "the first three", f.captured.items()[0].JSON["answer"].(map[string]any)["text"])
}

// An item too big to take at once, even alone, fails the run, naming it.
func TestTaskItemTooBig(t *testing.T) {
	f := newFixture(t)
	_ = f.waiting(claude)
	f.around(t)

	items := bulky(3)
	items[1]["text"] = strings.Repeat("x", 30_000)

	res := f.run(t, map[string]any{"prompt": "Look into it"}, items...)
	assert.Equal(t, domain.ExecutionFailed, res.Execution.Status)
	assert.Contains(t, res.Execution.Error, "item 2 is")
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case float64:
		return n
	}

	return -1
}

// A prompt holding every item is too big to page: its user is told how to hand them over.
func TestTaskPromptTooBig(t *testing.T) {
	f := newFixture(t)
	_ = f.waiting(claude)
	f.around(t)

	res := f.run(t, map[string]any{"prompt": "Which of {{ items }} matter?", "call": "together"}, bulky(30)...)
	assert.Equal(t, domain.ExecutionFailed, res.Execution.Status)
	assert.Contains(t, res.Execution.Error, "leave {{ items }} out of it and turn Include the items on")
}
