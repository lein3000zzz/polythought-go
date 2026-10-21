package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// search is a tool as 4c2's are: a closed schema, every field required.
var search = ports.LLMTool{
	Name: "search_markets", Description: "Finds markets by words.",
	Schema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["query"],"properties":{"query":{"type":"string"}}}`),
}

// converse asks with the tool, answers the call it gets with a result, and asks again:
// what a node's tool loop does.
func converse(t *testing.T, c *Client, key ports.LLMKey) (first, second ports.LLMResponse) {
	t.Helper()

	req := question
	req.Messages = []ports.LLMMessage{{Role: ports.LLMUser, Text: "Any bitcoin markets?"}}
	req.Tools = []ports.LLMTool{search}

	first, err := c.Complete(context.Background(), key, req)
	require.NoError(t, err)
	require.Len(t, first.Turn.ToolCalls, 1, "a tool asked for")

	call := first.Turn.ToolCalls[0]
	assert.Equal(t, "search_markets", call.Name)
	assert.JSONEq(t, `{"query":"bitcoin"}`, string(call.Input))

	req.Messages = append(req.Messages, first.Turn, ports.LLMMessage{Role: ports.LLMUser, ToolResults: []ports.LLMToolResult{
		{CallID: call.ID, Name: call.Name, Content: `{"markets":[{"question":"BTC above 100k?"}]}`},
	}})

	second, err = c.Complete(context.Background(), key, req)
	require.NoError(t, err)
	assert.Empty(t, second.Turn.ToolCalls, "answered")

	return first, second
}

const anthropicToolStream = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":30,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Look it up."}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-1"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"search_markets","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"query\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"bitcoin\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}

event: message_stop
data: {"type":"message_stop"}

`

func TestAnthropicTools(t *testing.T) {
	var n atomic.Int32

	srv, got := fake(t, map[string]http.HandlerFunc{
		"/v1/messages": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")

			if n.Add(1) == 1 {
				_, _ = io.WriteString(w, anthropicToolStream)
				return
			}

			_, _ = io.WriteString(w, anthropicStream)
		},
	})

	c := newClient(t, false)
	c.anthropicURL = srv.URL + "/"

	first, second := converse(t, c, ports.LLMKey{Provider: ports.LLMAnthropic, APIKey: "sk-ant-user"})
	assert.Equal(t, "tool_use", first.StopReason)
	assert.Equal(t, "toolu_1", first.Turn.ToolCalls[0].ID)
	assert.Equal(t, "No.", second.Text)

	tools := got.bodies[0]["tools"].([]any)
	assert.Equal(t, map[string]any{
		"name": "search_markets", "description": "Finds markets by words.", "strict": true, "eager_input_streaming": true,
		"input_schema": map[string]any{
			"type": "object", "additionalProperties": false, "required": []any{"query"},
			"properties": map[string]any{"query": map[string]any{"type": "string"}},
		},
	}, tools[0])

	msgs := got.bodies[1]["messages"].([]any)
	require.Len(t, msgs, 3)

	turn := msgs[1].(map[string]any)["content"].([]any)
	assert.Equal(t, map[string]any{"type": "thinking", "thinking": "Look it up.", "signature": "sig-1"}, turn[0],
		"the thinking back as it was, its signature with it")
	assert.Equal(t, map[string]any{"type": "tool_use", "id": "toolu_1", "name": "search_markets", "input": map[string]any{"query": "bitcoin"}}, turn[1])

	result := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	assert.Equal(t, "tool_result", result["type"])
	assert.Equal(t, "toolu_1", result["tool_use_id"])
	assert.Contains(t, result["content"].([]any)[0].(map[string]any)["text"], "BTC above 100k?")
}

func TestOpenAITools(t *testing.T) {
	var n atomic.Int32

	srv, got := fake(t, map[string]http.HandlerFunc{
		"/v1/chat/completions": func(w http.ResponseWriter, _ *http.Request) {
			if n.Add(1) == 1 {
				_, _ = io.WriteString(w, `{"id":"c-1","object":"chat.completion","created":1,"model":"gpt-x","choices":[{"index":0,
					"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function",
					"function":{"name":"search_markets","arguments":"{\"query\":\"bitcoin\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":5}}`)

				return
			}

			_, _ = io.WriteString(w, `{"id":"c-2","object":"chat.completion","created":1,"model":"gpt-x","choices":[{"index":0,
				"finish_reason":"stop","message":{"role":"assistant","content":"One."}}],"usage":{"prompt_tokens":9,"completion_tokens":2}}`)
		},
	})

	c := newClient(t, false)
	c.openAIURL = srv.URL + "/v1/"

	first, second := converse(t, c, ports.LLMKey{Provider: ports.LLMOpenAI, APIKey: "sk-user"})
	assert.Equal(t, "tool_calls", first.StopReason)
	assert.Equal(t, "One.", second.Text)

	fn := got.bodies[0]["tools"].([]any)[0].(map[string]any)
	assert.Equal(t, "function", fn["type"])
	assert.Equal(t, true, fn["function"].(map[string]any)["strict"])

	msgs := got.bodies[1]["messages"].([]any)
	require.Len(t, msgs, 4, "system, user, the assistant's calls, the result")

	asst := msgs[2].(map[string]any)
	assert.Equal(t, "assistant", asst["role"])
	assert.Equal(t, "call_1", asst["tool_calls"].([]any)[0].(map[string]any)["id"])

	result := msgs[3].(map[string]any)
	assert.Equal(t, "tool", result["role"])
	assert.Equal(t, "call_1", result["tool_call_id"])
	assert.Contains(t, result["content"], "BTC above 100k?")
}

func TestGeminiTools(t *testing.T) {
	var n atomic.Int32

	srv, got := fake(t, map[string]http.HandlerFunc{
		"/v1beta/models/m-1:generateContent": func(w http.ResponseWriter, _ *http.Request) {
			if n.Add(1) == 1 {
				_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"search_markets",
					"args":{"query":"bitcoin"}},"thoughtSignature":"c2lnLTE="}]},"finishReason":"STOP"}]}`)

				return
			}

			_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"One."}]},"finishReason":"STOP"}]}`)
		},
	})

	c := newClient(t, false)
	c.geminiURL = srv.URL + "/"

	_, second := converse(t, c, ports.LLMKey{Provider: ports.LLMGemini, APIKey: "gem-user"})
	assert.Equal(t, "One.", second.Text)

	decl := got.bodies[0]["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)[0].(map[string]any)
	assert.Equal(t, "search_markets", decl["name"])
	assert.NotNil(t, decl["parametersJsonSchema"])

	contents := got.bodies[1]["contents"].([]any)
	require.Len(t, contents, 3)

	part := contents[1].(map[string]any)["parts"].([]any)[0].(map[string]any)
	assert.Equal(t, "c2lnLTE=", part["thoughtSignature"], "the signature back as it was")
	assert.Equal(t, "search_markets", part["functionCall"].(map[string]any)["name"])

	response := contents[2].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
	assert.Equal(t, "search_markets", response["name"])
	assert.Equal(t, map[string]any{"output": map[string]any{"markets": []any{map[string]any{"question": "BTC above 100k?"}}}}, response["response"])
}

// A turn from elsewhere, without its provider's own form, is made of its text and calls.
func TestTurnWithoutNative(t *testing.T) {
	srv, got := fake(t, map[string]http.HandlerFunc{
		"/v1/messages": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, anthropicStream)
		},
	})

	c := newClient(t, false)
	c.anthropicURL = srv.URL + "/"

	req := question
	req.Tools = []ports.LLMTool{search}
	req.Messages = []ports.LLMMessage{
		{Role: ports.LLMUser, Text: "Any?"},
		{Role: ports.LLMAssistant, Text: "Looking.", ToolCalls: []ports.LLMToolCall{{ID: "toolu_9", Name: "search_markets", Input: json.RawMessage(`{"query":"x"}`)}}},
		{Role: ports.LLMUser, ToolResults: []ports.LLMToolResult{{CallID: "toolu_9", Name: "search_markets", Content: "none", IsError: true}}},
	}

	_, err := c.Complete(context.Background(), ports.LLMKey{Provider: ports.LLMAnthropic, APIKey: "sk-ant-user"}, req)
	require.NoError(t, err)

	msgs := got.bodies[0]["messages"].([]any)
	turn := msgs[1].(map[string]any)["content"].([]any)
	assert.Equal(t, "Looking.", turn[0].(map[string]any)["text"])
	assert.Equal(t, "toolu_9", turn[1].(map[string]any)["id"])

	result := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	assert.Equal(t, true, result["is_error"])
}
