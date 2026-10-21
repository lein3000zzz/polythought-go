package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// seen is what a fake provider was sent.
type seen struct {
	mu      sync.Mutex
	headers []http.Header
	bodies  []map[string]any
	paths   []string
}

func (s *seen) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var body map[string]any
	if b, _ := io.ReadAll(r.Body); len(b) > 0 {
		_ = json.Unmarshal(b, &body)
	}

	s.headers, s.bodies, s.paths = append(s.headers, r.Header.Clone()), append(s.bodies, body), append(s.paths, r.URL.Path)
}

// fake answers each path with its handler, in JSON unless it says, recording what came.
func fake(t *testing.T, routes map[string]http.HandlerFunc) (*httptest.Server, *seen) {
	t.Helper()

	mux := http.NewServeMux()
	for path, h := range routes {
		mux.HandleFunc(path, h)
	}

	s := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.Header().Set("Content-Type", "application/json")
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	return srv, s
}

func newClient(t *testing.T, allowPrivate bool) *Client {
	t.Helper()

	c, err := New(observability.NoopUtility(), Config{AllowPrivate: func() bool { return allowPrivate }})
	require.NoError(t, err)

	return c
}

var question = ports.LLMRequest{
	Model: "m-1", System: "You are terse.", MaxTokens: 100,
	Messages: []ports.LLMMessage{{Role: ports.LLMUser, Text: "Will it rain?"}, {Role: ports.LLMAssistant, Text: "Maybe."}, {Role: ports.LLMUser, Text: "Yes or no?"}},
}

const anthropicStream = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":12,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"No"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"."}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}

event: message_stop
data: {"type":"message_stop"}

`

func TestAnthropic(t *testing.T) {
	ctx := context.Background()

	// The server's own: none of it may reach a user's request.
	t.Setenv("ANTHROPIC_API_KEY", "server-key")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "server-token")
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:1")

	srv, got := fake(t, map[string]http.HandlerFunc{
		"/v1/messages": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, anthropicStream)
		},
		"/v1/models": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"data":[{"id":"claude-opus-5-5","display_name":"Claude Opus 5.5","type":"model","created_at":"2026-09-01T00:00:00Z"},
				{"id":"claude-haiku-5-5","display_name":"","type":"model","created_at":"2026-09-01T00:00:00Z"}],"has_more":false}`)
		},
	})

	c := newClient(t, false)
	c.anthropicURL = srv.URL + "/"
	key := ports.LLMKey{Provider: ports.LLMAnthropic, APIKey: "sk-ant-user"}

	res, err := c.Complete(ctx, key, question)
	require.NoError(t, err)
	assert.Equal(t, ports.LLMResponse{
		Text: "No.", Model: "claude-opus-5-5", StopReason: "end_turn",
		Usage: ports.LLMUsage{InputTokens: 12, OutputTokens: 3},
	}, res)

	require.Len(t, got.headers, 1)
	assert.Equal(t, "sk-ant-user", got.headers[0].Get("X-Api-Key"), "the user's own key (D243)")
	assert.Empty(t, got.headers[0].Get("Authorization"), "no token of the server's")

	body := got.bodies[0]
	assert.Equal(t, "m-1", body["model"])
	assert.EqualValues(t, 100, body["max_tokens"])
	assert.Equal(t, true, body["stream"], "streamed, so a long answer outlasts no timeout")
	assert.NotContains(t, body, "temperature", "the newest Claude models refuse one: sent only when set")
	assert.Equal(t, "You are terse.", body["system"].([]any)[0].(map[string]any)["text"])
	assert.Len(t, body["messages"], 3)

	models, err := c.Models(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, []ports.LLMModel{{ID: "claude-opus-5-5", Name: "Claude Opus 5.5"}, {ID: "claude-haiku-5-5", Name: "claude-haiku-5-5"}}, models)
}

func TestOpenAICompatible(t *testing.T) {
	ctx := context.Background()

	t.Setenv("OPENAI_ORG_ID", "server-org")
	t.Setenv("OPENAI_PROJECT_ID", "server-project")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "X-Secret: server")
	t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:1")

	completion := `{"id":"c-1","object":"chat.completion","created":1,"model":"openai/gpt-x","choices":[{"index":0,"finish_reason":"stop",
		"message":{"role":"assistant","content":"No."}}],"usage":{"prompt_tokens":20,"completion_tokens":2,"total_tokens":22,"cost":0.00042}}`

	srv, got := fake(t, map[string]http.HandlerFunc{
		"/v1/chat/completions": func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, completion) },
		"/v1/models": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"openai/gpt-x","object":"model","created":1,"owned_by":"o"}]}`)
		},
	})

	t.Run("its own: max_completion_tokens", func(t *testing.T) {
		c := newClient(t, false)
		c.openAIURL = srv.URL + "/v1/" // as if OpenAI's
		got.bodies, got.headers = nil, nil

		res, err := c.Complete(ctx, ports.LLMKey{Provider: ports.LLMOpenAI, APIKey: "sk-user"}, question)
		require.NoError(t, err)
		assert.Equal(t, "No.", res.Text)
		assert.Equal(t, "stop", res.StopReason)

		h := got.headers[0]
		assert.Equal(t, "Bearer sk-user", h.Get("Authorization"))
		assert.Empty(t, h.Get("OpenAI-Organization"), "nothing of the server's environment")
		assert.Empty(t, h.Get("OpenAI-Project"))
		assert.Empty(t, h.Get("X-Secret"))

		body := got.bodies[0]
		assert.EqualValues(t, 100, body["max_completion_tokens"])
		assert.NotContains(t, body, "max_tokens")
		assert.Equal(t, "system", body["messages"].([]any)[0].(map[string]any)["role"])
		assert.Len(t, body["messages"], 4)
	})

	t.Run("OpenRouter, or a server on the network: max_tokens, and the cost it reports (D250)", func(t *testing.T) {
		c := newClient(t, true) // the fake is on 127.0.0.1, in plain http
		got.bodies = nil

		key := ports.LLMKey{Provider: ports.LLMOpenAI, APIKey: "sk-or-user", BaseURL: srv.URL + "/v1"}
		res, err := c.Complete(ctx, key, question)
		require.NoError(t, err)
		require.NotNil(t, res.Usage.Cost)
		assert.Equal(t, "0.00042", res.Usage.Cost.String())
		assert.Equal(t, ports.LLMUsage{InputTokens: 20, OutputTokens: 2, Cost: res.Usage.Cost}, res.Usage)
		assert.EqualValues(t, 100, got.bodies[0]["max_tokens"])

		models, err := c.Models(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, []ports.LLMModel{{ID: "openai/gpt-x", Name: "openai/gpt-x"}}, models)
	})

	t.Run("a base URL a user chose keeps to public addresses, in https", func(t *testing.T) {
		c := newClient(t, false)

		_, err := c.Complete(ctx, ports.LLMKey{Provider: ports.LLMOpenAI, APIKey: "sk-user", BaseURL: srv.URL + "/v1"}, question)
		require.ErrorIs(t, err, ErrInsecureURL, "a key in clear")

		_, err = c.Complete(ctx, ports.LLMKey{Provider: ports.LLMOpenAI, APIKey: "sk-user", BaseURL: "https://127.0.0.1:9/v1"}, question)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "address inside the server's network", "refused as it dials, after DNS")
	})

	t.Run("a redirect is not followed, the key not sent on", func(t *testing.T) {
		moved, hit := fake(t, map[string]http.HandlerFunc{
			"/v1/models": func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, srv.URL+"/v1/models", http.StatusTemporaryRedirect)
			},
		})
		got.headers = nil

		_, err := newClient(t, true).Models(ctx, ports.LLMKey{Provider: ports.LLMOpenAI, APIKey: "sk-user", BaseURL: moved.URL + "/v1"})
		require.ErrorIs(t, err, errRedirected)
		assert.NotEmpty(t, hit.paths)
		assert.Empty(t, got.headers, "nothing reached where it pointed")
	})

	t.Run("plain http, allowed for the network, reaches no public address", func(t *testing.T) {
		c := newClient(t, true)

		_, err := c.Models(ctx, ports.LLMKey{Provider: ports.LLMOpenAI, APIKey: "sk-user", BaseURL: "http://192.0.2.1:9/v1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "a public address in plain http", "refused before it connects")
	})
}

func TestGemini(t *testing.T) {
	ctx := context.Background()

	t.Setenv("GEMINI_API_KEY", "server-key")
	t.Setenv("GOOGLE_API_KEY", "server-key")
	t.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "true")
	t.Setenv("GOOGLE_GEMINI_BASE_URL", "http://127.0.0.1:1")

	srv, got := fake(t, map[string]http.HandlerFunc{
		"/v1beta/models/m-1:generateContent": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"No."}]},"finishReason":"STOP"}],
				"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":2,"thoughtsTokenCount":5},"modelVersion":"gemini-x"}`)
		},
		"/v1beta/models": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"models":[{"name":"models/gemini-x","displayName":"Gemini X","supportedGenerationMethods":["generateContent","countTokens"]},
				{"name":"models/embed-x","displayName":"Embed X","supportedGenerationMethods":["embedContent"]}]}`)
		},
	})

	c := newClient(t, false)
	c.geminiURL = srv.URL + "/"
	key := ports.LLMKey{Provider: ports.LLMGemini, APIKey: "gem-user"}

	res, err := c.Complete(ctx, key, question)
	require.NoError(t, err)
	assert.Equal(t, ports.LLMResponse{
		Text: "No.", Model: "gemini-x", StopReason: "STOP",
		Usage: ports.LLMUsage{InputTokens: 9, OutputTokens: 7},
	}, res, "thoughts billed as output")

	assert.Equal(t, "gem-user", got.headers[0].Get("X-Goog-Api-Key"), "the user's key, not the server's")
	assert.Equal(t, "You are terse.", got.bodies[0]["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"])

	models, err := c.Models(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, []ports.LLMModel{{ID: "gemini-x", Name: "Gemini X"}}, models, "only those that answer")
}

func TestNotYetAndScrubbed(t *testing.T) {
	c := newClient(t, false)

	req := question
	req.Tools = []ports.LLMTool{{Name: "search", Schema: json.RawMessage(`{"type":"object"}`)}}
	_, err := c.Complete(context.Background(), ports.LLMKey{Provider: ports.LLMAnthropic, APIKey: "sk-ant-user"}, req)
	require.ErrorIs(t, err, ports.ErrLLMNotYet)

	assert.Equal(t, io.ErrUnexpectedEOF, scrub(io.ErrUnexpectedEOF, ports.LLMKey{APIKey: "sk-secret-1"}), "nothing to scrub: the same error")

	err = scrub(errors.New(`401: invalid key "sk-secret-1"`), ports.LLMKey{APIKey: "sk-secret-1"})
	assert.Equal(t, `401: invalid key "<key>"`, err.Error())
}

// A provider's refusal reads in its own words: no URL, request ID or key.
func TestRefusals(t *testing.T) {
	ctx := context.Background()

	refuse := func(status int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Request-Id", "req_1")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}
	}

	srv, _ := fake(t, map[string]http.HandlerFunc{
		"/v1/models": refuse(http.StatusUnauthorized,
			`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"},"request_id":"req_1"}`),
		"/api/v1/models": refuse(http.StatusUnauthorized, `{"error":{"message":"No auth credentials found","code":401}}`),
		"/v1beta/models": refuse(http.StatusBadRequest, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT"}}`),
		"/v2/models":     refuse(http.StatusForbidden, "<html>\n"+strings.Repeat("blocked ", 100)+"</html>"),
		"/v3/models":     refuse(http.StatusForbidden, ""),
	})

	c := newClient(t, true)
	c.anthropicURL, c.openAIURL, c.geminiURL = srv.URL+"/", srv.URL+"/v1/", srv.URL+"/"
	host := strings.TrimPrefix(srv.URL, "http://")

	for _, tc := range []struct {
		key  ports.LLMKey
		want string
	}{
		{ports.LLMKey{Provider: ports.LLMAnthropic, APIKey: "sk-ant-bad"}, "Anthropic answered 401: invalid x-api-key"},
		{ports.LLMKey{Provider: ports.LLMOpenAI, APIKey: "sk-or-bad", BaseURL: srv.URL + "/api/v1"}, host + " answered 401: No auth credentials found"},
		{ports.LLMKey{Provider: ports.LLMGemini, APIKey: "gem-bad"}, "Gemini answered 400: API key not valid. Please pass a valid API key."},
		{ports.LLMKey{Provider: ports.LLMOpenAI, APIKey: "sk-bad", BaseURL: srv.URL + "/v3"}, host + " answered 403: Forbidden"},
	} {
		_, err := c.Models(ctx, tc.key)
		require.Error(t, err)
		assert.Equal(t, tc.want, err.Error())
	}

	_, err := c.Models(ctx, ports.LLMKey{Provider: ports.LLMOpenAI, APIKey: "sk-bad", BaseURL: srv.URL + "/v2"})
	require.Error(t, err)
	assert.LessOrEqual(t, len([]rune(err.Error())), maxMessage+len(host)+len(" answered 403: …"), "a page in the way, cut short")
	assert.NotContains(t, err.Error(), "\n")
}

// verdict is a schema as the fields table makes it: closed, every field required.
const verdict = `{"type":"object","additionalProperties":false,"required":["watch","reason"],
	"properties":{"watch":{"type":"boolean"},"reason":{"type":"string"}}}`

// The schema and the effort reach each provider in its own words.
func TestSchemaAndEffort(t *testing.T) {
	ctx := context.Background()

	req := question
	req.Schema, req.Effort = json.RawMessage(verdict), ports.LLMEffortLow

	srv, got := fake(t, map[string]http.HandlerFunc{
		"/v1/messages": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, anthropicStream)
		},
		"/v1/chat/completions": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"id":"c-1","object":"chat.completion","created":1,"model":"gpt-x","choices":[{"index":0,
				"finish_reason":"stop","message":{"role":"assistant","content":"{}"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
		},
		"/v1beta/models/m-1:generateContent": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"{}"}]},"finishReason":"STOP"}]}`)
		},
	})

	c := newClient(t, false)
	c.anthropicURL, c.openAIURL, c.geminiURL = srv.URL+"/", srv.URL+"/v1/", srv.URL+"/"

	var schema map[string]any
	require.NoError(t, json.Unmarshal([]byte(verdict), &schema))

	t.Run("Claude: output_config", func(t *testing.T) {
		got.bodies = nil
		_, err := c.Complete(ctx, ports.LLMKey{Provider: ports.LLMAnthropic, APIKey: "sk-ant-user"}, req)
		require.NoError(t, err)

		assert.Equal(t, map[string]any{"effort": "low", "format": map[string]any{"type": "json_schema", "schema": schema}},
			got.bodies[0]["output_config"])
	})

	t.Run("OpenAI: response_format, strict when it fits, and reasoning_effort", func(t *testing.T) {
		got.bodies = nil
		_, err := c.Complete(ctx, ports.LLMKey{Provider: ports.LLMOpenAI, APIKey: "sk-user"}, req)
		require.NoError(t, err)

		body := got.bodies[0]
		assert.Equal(t, "low", body["reasoning_effort"])
		assert.Equal(t, map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "answer", "strict": true, "schema": schema}},
			body["response_format"])

		loose := req
		loose.Schema = json.RawMessage(`{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string"}}}}`)
		_, err = c.Complete(ctx, ports.LLMKey{Provider: ports.LLMOpenAI, APIKey: "sk-user"}, loose)
		require.NoError(t, err)
		assert.Equal(t, false, got.bodies[1]["response_format"].(map[string]any)["json_schema"].(map[string]any)["strict"],
			"a guide only, rather than refused")
	})

	t.Run("Gemini: a JSON answer to the schema, and a thinking level", func(t *testing.T) {
		got.bodies = nil
		_, err := c.Complete(ctx, ports.LLMKey{Provider: ports.LLMGemini, APIKey: "gem-user"}, req)
		require.NoError(t, err)

		config := got.bodies[0]["generationConfig"].(map[string]any)
		assert.Equal(t, "application/json", config["responseMimeType"])
		assert.Equal(t, schema, config["responseJsonSchema"])
		assert.Equal(t, map[string]any{"thinkingLevel": "LOW"}, config["thinkingConfig"])
	})

	t.Run("neither when not asked", func(t *testing.T) {
		got.bodies = nil
		_, err := c.Complete(ctx, ports.LLMKey{Provider: ports.LLMAnthropic, APIKey: "sk-ant-user"}, question)
		require.NoError(t, err)
		assert.NotContains(t, got.bodies[0], "output_config")

		_, err = c.Complete(ctx, ports.LLMKey{Provider: ports.LLMOpenAI, APIKey: "sk-user"}, question)
		require.NoError(t, err)
		assert.NotContains(t, got.bodies[1], "response_format")
		assert.NotContains(t, got.bodies[1], "reasoning_effort")
	})

	t.Run("a schema that is no object", func(t *testing.T) {
		bad := question
		bad.Schema = json.RawMessage(`["type"]`)
		_, err := c.Complete(ctx, ports.LLMKey{Provider: ports.LLMAnthropic, APIKey: "sk-ant-user"}, bad)
		require.ErrorContains(t, err, "the schema must be a JSON object")
	})
}

func TestStrict(t *testing.T) {
	for raw, want := range map[string]bool{
		verdict: true,
		`{"type":"object","additionalProperties":false,"required":["a"],"properties":{"a":{"type":"string"},"b":{"type":"string"}}}`: false,
		`{"type":"object","required":["a"],"properties":{"a":{"type":"string"}}}`:                                                    false,
		`{"type":"object","additionalProperties":false,"required":["list"],"properties":{"list":{"type":"array","items":
			{"type":"object","additionalProperties":false,"required":["x"],"properties":{"x":{"type":"number"}}}}}}`: true,
		`{"type":"object","additionalProperties":false,"required":["list"],"properties":{"list":{"type":"array","items":
			{"type":"object","properties":{"x":{"type":"number"}}}}}}`: false,
		`{"type":"object","additionalProperties":false,"required":["a"],"properties":{"a":{"anyOf":[{"type":"string"},{"type":"null"}]}}}`: true,
	} {
		var schema map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &schema))
		assert.Equal(t, want, strict(schema), raw)
	}
}

// Gemini tries again when busy, as the other SDKs do.
func TestGeminiRetries(t *testing.T) {
	busy := true

	srv, got := fake(t, map[string]http.HandlerFunc{
		"/v1beta/models/m-1:generateContent": func(w http.ResponseWriter, _ *http.Request) {
			if busy {
				busy = false
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, `{"error":{"code":503,"message":"overloaded","status":"UNAVAILABLE"}}`)

				return
			}

			_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"No."}]},"finishReason":"STOP"}]}`)
		},
	})

	c := newClient(t, false)
	c.geminiURL = srv.URL + "/"

	res, err := c.Complete(context.Background(), ports.LLMKey{Provider: ports.LLMGemini, APIKey: "gem-user"}, question)
	require.NoError(t, err)
	assert.Equal(t, "No.", res.Text)
	assert.Len(t, got.paths, 2)
}
