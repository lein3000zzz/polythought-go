package ai_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/ai"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/credtypes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

const owner = domain.UserID("alice")

// clock is the node's now when set; the tests run one at a time.
var clock func() time.Time

var key = ports.LLMKey{Provider: ports.LLMAnthropic, APIKey: "sk-ant-1"}

// fakeLLM answers each call in turn, recording what it was asked.
type fakeLLM struct {
	mu      sync.Mutex
	answers []any // a ports.LLMResponse or an error, in turn
	asked   []ports.LLMRequest
}

func (f *fakeLLM) Complete(_ context.Context, k ports.LLMKey, req ports.LLMRequest) (ports.LLMResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if k != key {
		return ports.LLMResponse{}, errors.New("another key")
	}

	f.asked = append(f.asked, req)

	if len(f.answers) == 0 {
		return ports.LLMResponse{}, errors.New("asked once too often")
	}

	next := f.answers[0]
	f.answers = f.answers[1:]

	if err, ok := next.(error); ok {
		return ports.LLMResponse{}, err
	}

	return next.(ports.LLMResponse), nil
}

func (*fakeLLM) Models(context.Context, ports.LLMKey) ([]ports.LLMModel, error) { return nil, nil }

func said(text string) ports.LLMResponse {
	return ports.LLMResponse{Text: text, Model: "claude-opus-5-5", StopReason: "end_turn", Usage: ports.LLMUsage{InputTokens: 10, OutputTokens: 5}}
}

func run(t *testing.T, llm *fakeLLM, params map[string]any, items ...map[string]any) engine.Ports {
	t.Helper()

	return runWith(t, llm, ai.Sources{}, params, items...)
}

func runWith(t *testing.T, llm *fakeLLM, sources ai.Sources, params map[string]any, items ...map[string]any) engine.Ports {
	t.Helper()

	creds := portsmocks.NewMockCredentialReader(t)
	creds.EXPECT().Values(mock.Anything, owner, domain.CredentialID("c-1"), credtypes.LLMKeyTypes).
		Return(credtypes.AnthropicKey, map[string]string{"apiKey": "sk-ant-1"}, nil).Once()

	node := ai.NewAgent(creds, llm, sources)
	if clock != nil {
		node.WithClock(clock)
	}
	base := map[string]any{"credential": "c-1", "model": "claude-opus-5-5", "prompt": "Worth watching?"}

	for k, v := range params {
		base[k] = v
	}

	in := engine.Ports{}
	for _, it := range items {
		in.Add(engine.DefaultPort, engine.Item{JSON: it})
	}

	out, err := node.Run(context.Background(), engine.RunContext{OwnerID: owner, Params: engine.LiteralParams(node.Type(), base)}, in)
	require.NoError(t, err)

	// As its type says, the answer under the field its setting names.
	typ := node.Type()
	if name, ok := base["field"].(string); ok && strings.TrimSpace(name) != "" {
		items := *typ.Items
		items.Fields = []engine.Field{items.Fields[0]}
		items.Fields[0].Name = name
		typ.Items = &items
	}

	require.NoError(t, shape.Check(typ, out), "as its type says")

	return out
}

func TestEachItem(t *testing.T) {
	llm := &fakeLLM{answers: []any{said("Yes."), said("No.")}}

	out := run(t, llm, map[string]any{"system": "Be brief.", "effort": "low", "temperature": "0.2"},
		map[string]any{"question": "BTC?"}, map[string]any{"question": "ETH?"})

	require.Len(t, out[engine.DefaultPort], 2)
	first := out[engine.DefaultPort][0].JSON
	assert.Equal(t, "BTC?", first["question"], "the item's fields stay, in order")
	assert.Equal(t, "ETH?", out[engine.DefaultPort][1].JSON["question"])

	answer := first["answer"].(map[string]any)
	assert.Contains(t, []any{"Yes.", "No."}, answer["text"], "the calls run at once, answered in either order")
	delete(answer, "text")
	assert.Equal(t, map[string]any{
		"model": "claude-opus-5-5", "stopReason": "end_turn", "tries": 1, "toolCalls": 0, "tools": []any{},
		"usage": map[string]any{"input": int64(10), "output": int64(5)}, "cost": nil,
	}, answer)

	req := llm.asked[0]
	assert.Equal(t, "claude-opus-5-5", req.Model)
	assert.Equal(t, "Be brief.", req.System)
	assert.Equal(t, []ports.LLMMessage{{Role: ports.LLMUser, Text: "Worth watching?"}}, req.Messages)
	assert.Equal(t, int64(16000), req.MaxTokens)
	assert.Equal(t, ports.LLMEffortLow, req.Effort)
	require.NotNil(t, req.Temperature)
	assert.InDelta(t, 0.2, *req.Temperature, 1e-9)
	assert.Empty(t, req.Schema, "text: no schema")
}

func TestFields(t *testing.T) {
	llm := &fakeLLM{answers: []any{said("```json\n{\"watch\": true, \"reason\": \"volume\", \"mood\": null}\n```")}}

	out := run(t, llm, map[string]any{
		"answerAs": ai.AnswerFields, "field": "verdict",
		"fields": []any{
			map[string]any{"name": "watch", "type": "true or false", "description": "Worth watching."},
			map[string]any{"name": "reason", "type": "text"},
			map[string]any{"name": "mood", "type": "text", "oneOf": "calm, wild", "optional": true},
		},
	}, map[string]any{"question": "BTC?"})

	require.Len(t, out[engine.DefaultPort], 1)
	verdict := out[engine.DefaultPort][0].JSON["verdict"].(map[string]any)
	assert.Equal(t, map[string]any{"watch": true, "reason": "volume", "mood": nil}, verdict["json"], "out of its fence")
	assert.NotContains(t, verdict, "text")

	var schema map[string]any
	require.NoError(t, json.Unmarshal(llm.asked[0].Schema, &schema))
	assert.Equal(t, map[string]any{
		"type": "object", "additionalProperties": false, "required": []any{"watch", "reason", "mood"},
		"properties": map[string]any{
			"watch":  map[string]any{"type": "boolean", "description": "Worth watching."},
			"reason": map[string]any{"type": "string"},
			"mood":   map[string]any{"anyOf": []any{map[string]any{"type": "string", "enum": []any{"calm", "wild"}}, map[string]any{"type": "null"}}},
		},
	}, schema, "closed, and requiring every field, so every provider holds to it")
}

func TestRetries(t *testing.T) {
	schema := `{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}`

	t.Run("asked again, told why, every try counted", func(t *testing.T) {
		costly := func(text string, cost string) ports.LLMResponse {
			r := said(text)
			c := decimal.RequireFromString(cost)
			r.Usage.Cost = &c

			return r
		}

		llm := &fakeLLM{answers: []any{costly("forty-two", "0.001"), costly(`{"n": 42}`, "0.002")}}
		out := run(t, llm, map[string]any{"answerAs": ai.AnswerSchema, "schema": schema}, map[string]any{})

		answer := out[engine.DefaultPort][0].JSON["answer"].(map[string]any)
		assert.Equal(t, map[string]any{"n": float64(42)}, answer["json"])
		assert.Equal(t, 2, answer["tries"])
		assert.Equal(t, map[string]any{"input": int64(20), "output": int64(10)}, answer["usage"])
		assert.Equal(t, "0.003", answer["cost"])

		require.Len(t, llm.asked, 2)
		again := llm.asked[1].Messages
		require.Len(t, again, 3)
		assert.Equal(t, ports.LLMMessage{Role: ports.LLMAssistant, Text: "forty-two"}, again[1])
		assert.Contains(t, again[2].Text, "That answer does not fit the schema: it is not JSON")
	})

	t.Run("then the item fails", func(t *testing.T) {
		cut := said(`{"n": `)
		cut.StopReason = "max_tokens"

		llm := &fakeLLM{answers: []any{said(`{"n": "x"}`), cut}}
		out := run(t, llm, map[string]any{"answerAs": ai.AnswerSchema, "schema": schema}, map[string]any{"question": "BTC?"})

		require.Empty(t, out[engine.DefaultPort])
		require.Len(t, out[engine.ErrorPort], 1)
		failed := out[engine.ErrorPort][0]
		assert.Equal(t, "BTC?", failed.JSON["question"], "the item, for an error branch")
		assert.ErrorContains(t, failed.Err, "the answer does not fit the schema, after 2 tries: cut off at Max tokens")
	})

	t.Run("none, when none are wanted", func(t *testing.T) {
		llm := &fakeLLM{answers: []any{said("nope")}}
		out := run(t, llm, map[string]any{"answerAs": ai.AnswerSchema, "schema": schema, "retries": 0}, map[string]any{})
		require.Len(t, out[engine.ErrorPort], 1)
		assert.Len(t, llm.asked, 1)
	})
}

func TestFailures(t *testing.T) {
	t.Run("a refusal is not asked again", func(t *testing.T) {
		refused := said("")
		refused.StopReason = "refusal"

		llm := &fakeLLM{answers: []any{refused}}
		out := run(t, llm, map[string]any{"answerAs": ai.AnswerSchema, "schema": `{"type":"object"}`, "retries": 3}, map[string]any{})
		require.Len(t, out[engine.ErrorPort], 1)
		assert.ErrorContains(t, out[engine.ErrorPort][0].Err, "the model refused to answer")
		assert.Len(t, llm.asked, 1)
	})

	t.Run("the provider's words, for that item only", func(t *testing.T) {
		llm := &fakeLLM{answers: []any{errors.New("Anthropic answered 429: rate limited"), said("Fine.")}}
		out := run(t, llm, map[string]any{"prompt": "Q"}, map[string]any{"n": 1})
		require.Len(t, out[engine.ErrorPort], 1)
		assert.ErrorContains(t, out[engine.ErrorPort][0].Err, "Anthropic answered 429")

		llm = &fakeLLM{answers: []any{said("Fine.")}}
		out = run(t, llm, nil, map[string]any{"n": 1})
		assert.Len(t, out[engine.DefaultPort], 1)
	})

	for name, params := range map[string]map[string]any{
		"no model":       {"model": " "},
		"no prompt":      {"prompt": ""},
		"retries":        {"retries": 4},
		"max tokens":     {"maxTokens": 0},
		"temperature":    {"temperature": "3"},
		"a bad schema":   {"answerAs": ai.AnswerSchema, "schema": `{"type": 7}`},
		"no schema":      {"answerAs": ai.AnswerSchema},
		"no fields":      {"answerAs": ai.AnswerFields},
		"a bad name":     {"answerAs": ai.AnswerFields, "fields": []any{map[string]any{"name": "has space"}}},
		"a name twice":   {"answerAs": ai.AnswerFields, "fields": []any{map[string]any{"name": "a"}, map[string]any{"name": "a"}}},
		"no answer name": {"field": " "},
	} {
		t.Run(name+" fails the item, unasked", func(t *testing.T) {
			llm := &fakeLLM{}
			out := run(t, llm, params, map[string]any{})
			require.Len(t, out[engine.ErrorPort], 1)
			assert.Empty(t, llm.asked)
		})
	}
}

func TestTogether(t *testing.T) {
	llm := &fakeLLM{answers: []any{said("BTC, then ETH.")}}

	out := run(t, llm, map[string]any{"call": ai.CallTogether, "prompt": "Rank these."},
		map[string]any{"question": "BTC?"}, map[string]any{"question": "ETH?"})

	require.Len(t, out[engine.DefaultPort], 1)
	one := out[engine.DefaultPort][0]
	assert.Equal(t, []string{"answer"}, keys(one.JSON), "the answer alone")
	assert.Len(t, one.Source, 2, "from all of them")

	require.Len(t, llm.asked, 1, "one call")
	prompt := llm.asked[0].Messages[0].Text
	assert.True(t, strings.HasPrefix(prompt, "Rank these.\n\nThe items, as JSON:\n"), prompt)
	assert.Contains(t, prompt, `"question": "ETH?"`)

	t.Run("without them", func(t *testing.T) {
		llm := &fakeLLM{answers: []any{said("ok")}}
		run(t, llm, map[string]any{"call": ai.CallTogether, "prompt": "Rank these.", "include": false}, map[string]any{"question": "BTC?"})
		assert.Equal(t, "Rank these.", llm.asked[0].Messages[0].Text)
	})

	t.Run("a failure fails the one item", func(t *testing.T) {
		llm := &fakeLLM{answers: []any{errors.New("Gemini answered 400: API key not valid")}}
		out := run(t, llm, map[string]any{"call": ai.CallTogether}, map[string]any{}, map[string]any{})
		require.Len(t, out[engine.ErrorPort], 1)
		assert.Len(t, out[engine.ErrorPort][0].Source, 2)
	})
}

func TestKey(t *testing.T) {
	creds := portsmocks.NewMockCredentialReader(t)
	creds.EXPECT().Values(mock.Anything, owner, domain.CredentialID("c-2"), credtypes.LLMKeyTypes).
		Return("", nil, domain.ErrInvalidInput).Once()

	node := ai.NewAgent(creds, &fakeLLM{}, ai.Sources{})
	in := engine.Ports{}
	in.Add(engine.DefaultPort, engine.Item{JSON: map[string]any{}})

	_, err := node.Run(context.Background(), engine.RunContext{OwnerID: owner, Params: engine.LiteralParams(node.Type(), map[string]any{
		"credential": "c-2", "model": "m", "prompt": "p",
	})}, in)
	require.ErrorIs(t, err, domain.ErrInvalidInput, "a key that is no provider's fails the node")

	out, err := node.Run(context.Background(), engine.RunContext{OwnerID: owner, Params: engine.LiteralParams(node.Type(), nil)}, engine.Ports{})
	require.NoError(t, err)
	assert.Empty(t, out, "no items, no call")
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}
