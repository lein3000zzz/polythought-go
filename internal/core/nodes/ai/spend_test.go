package ai_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/ai"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// fakeMeter counts what the node spends, or refuses as it is told.
type fakeMeter struct {
	mu       sync.Mutex
	runs     []ports.SpendRun
	recorded []ports.LLMUsage
	released int
	refuse   string
	err      error
}

func (m *fakeMeter) Run(r ports.SpendRun) ports.SpendBudget {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.runs = append(m.runs, r)

	return m
}

func (m *fakeMeter) Admit(context.Context, ports.LLMRequest) (ports.SpendHold, string, error) {
	if m.err != nil || m.refuse != "" {
		return nil, m.refuse, m.err
	}

	return &fakeHold{m: m}, "", nil
}

type fakeHold struct{ m *fakeMeter }

func (h *fakeHold) Record(_ context.Context, u ports.LLMUsage) {
	h.m.mu.Lock()
	defer h.m.mu.Unlock()

	h.m.recorded = append(h.m.recorded, u)
}

func (h *fakeHold) Release() {
	h.m.mu.Lock()
	defer h.m.mu.Unlock()

	h.m.released++
}

func metered(t *testing.T) *fakeMeter {
	t.Helper()

	m := &fakeMeter{}
	meter = m

	t.Cleanup(func() { meter = nil })

	return m
}

func TestSpendCounted(t *testing.T) {
	schema := `{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}`

	t.Run("every call, a run's budget for its owner", func(t *testing.T) {
		m := metered(t)
		llm := &fakeLLM{answers: []any{said("A."), said("B.")}}

		run(t, llm, nil, map[string]any{}, map[string]any{})

		require.Len(t, m.runs, 1, "one budget for the run")
		assert.Equal(t, owner, m.runs[0].OwnerID)
		assert.Len(t, m.recorded, 2)
	})

	t.Run("an item that fails counts what it took", func(t *testing.T) {
		m := metered(t)
		llm := &fakeLLM{answers: []any{said("no"), said("still no")}}

		out := run(t, llm, map[string]any{"answerAs": ai.AnswerSchema, "schema": schema}, map[string]any{})
		require.Len(t, out[engine.ErrorPort], 1)
		assert.Len(t, m.recorded, 2, "both tries, though the item failed")
	})

	t.Run("a refusal and tool rounds count too", func(t *testing.T) {
		m := metered(t)
		refused := said("")
		refused.StopReason = "refusal"

		run(t, &fakeLLM{answers: []any{refused}}, nil, map[string]any{})
		assert.Len(t, m.recorded, 1)

		m = metered(t)
		llm := &fakeLLM{answers: []any{asks(call("c1", "search_markets", `{"query":"bitcoin","closed":false}`)), said("ok")}}
		runWith(t, llm, bitcoin(t), map[string]any{"tools": []any{ai.ToolSearch}}, map[string]any{})
		assert.Len(t, m.recorded, 2, "the round that asked for a tool, and the answer")
	})

	t.Run("a call the provider failed is released, its cost unknown", func(t *testing.T) {
		m := metered(t)
		run(t, &fakeLLM{answers: []any{errors.New("Anthropic answered 529: overloaded")}}, nil, map[string]any{})
		assert.Empty(t, m.recorded)
		assert.Equal(t, 1, m.released)
	})
}

func TestSpendRefused(t *testing.T) {
	m := metered(t)
	m.refuse = "over the $20.00 a month an admin allows"

	llm := &fakeLLM{}
	out := run(t, llm, nil, map[string]any{"question": "BTC?"})

	require.Len(t, out[engine.ErrorPort], 1)
	assert.ErrorContains(t, out[engine.ErrorPort][0].Err, "over the $20.00 a month an admin allows")
	assert.Equal(t, "BTC?", out[engine.ErrorPort][0].JSON["question"], "the item, for an error branch")
	assert.Empty(t, llm.asked, "no call made")

	m.refuse, m.err = "", errors.New("meter down")
	out = run(t, llm, nil, map[string]any{})
	assert.ErrorContains(t, out[engine.ErrorPort][0].Err, "spend: meter down")
	assert.Empty(t, llm.asked)
}
