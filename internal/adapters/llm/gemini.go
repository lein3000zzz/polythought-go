package llm

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"google.golang.org/genai"

	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// gemini is a client of the Gemini API with the key. Backend, key and base URL are all
// given, so no GOOGLE_* or GEMINI_* variable of the server's picks them instead. It
// retries as the other SDKs do: unless told, it would not at all.
func (c *Client) gemini(ctx context.Context, key ports.LLMKey) (*genai.Client, error) {
	attempts := int32(1 + maxRetries)

	return genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:     key.APIKey,
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: c.public,
		HTTPOptions: genai.HTTPOptions{
			BaseURL: c.geminiURL, RetryOptions: &genai.HTTPRetryOptions{Attempts: &attempts},
		},
	})
}

func (c *Client) geminiComplete(ctx context.Context, key ports.LLMKey, cl call) (ports.LLMResponse, error) {
	req := cl.req

	client, err := c.gemini(ctx, key)
	if err != nil {
		return ports.LLMResponse{}, err
	}

	contents := make([]*genai.Content, len(req.Messages))
	for i, m := range req.Messages {
		contents[i] = geminiContent(m)
	}

	config := &genai.GenerateContentConfig{MaxOutputTokens: int32(min(cmpMax(req.MaxTokens), 1<<31-1))} //nolint:gosec // bounded just before

	if req.System != "" {
		config.SystemInstruction = genai.NewContentFromText(req.System, genai.RoleUser)
	}

	if req.Temperature != nil {
		t := float32(*req.Temperature)
		config.Temperature = &t
	}

	if cl.schema != nil {
		config.ResponseMIMEType, config.ResponseJsonSchema = "application/json", cl.schema
	}

	if level, ok := geminiLevels[req.Effort]; ok {
		config.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: level}
	}

	if len(cl.tools) > 0 {
		decls := make([]*genai.FunctionDeclaration, len(cl.tools))
		for i, t := range cl.tools {
			decls[i] = &genai.FunctionDeclaration{Name: t.Name, Description: t.Description, ParametersJsonSchema: t.schema}
		}

		config.Tools = []*genai.Tool{{FunctionDeclarations: decls}}
	}

	resp, err := client.Models.GenerateContent(ctx, req.Model, contents, config)
	if err != nil {
		return ports.LLMResponse{}, err
	}

	out := ports.LLMResponse{Text: geminiText(resp), Model: resp.ModelVersion, Turn: ports.LLMMessage{Role: ports.LLMAssistant}}
	out.Turn.Text = out.Text

	// The model's content goes back as it came: its function calls carry thought
	// signatures that Gemini checks.
	if len(resp.Candidates) > 0 {
		out.StopReason = string(resp.Candidates[0].FinishReason)
		out.Turn.Native = resp.Candidates[0].Content
	}

	for _, fc := range resp.FunctionCalls() {
		input, err := json.Marshal(fc.Args)
		if err != nil || fc.Args == nil {
			input = json.RawMessage(`{}`)
		}

		out.Turn.ToolCalls = append(out.Turn.ToolCalls, ports.LLMToolCall{ID: fc.ID, Name: fc.Name, Input: input})
	}

	// Thoughts are billed as output too.
	if u := resp.UsageMetadata; u != nil {
		out.Usage = ports.LLMUsage{InputTokens: int64(u.PromptTokenCount), OutputTokens: int64(u.CandidatesTokenCount) + int64(u.ThoughtsTokenCount)}
	}

	return out, nil
}

// geminiText is the answer's text, its thoughts left out. genai's own Text says so on
// the standard logger whenever there are function calls too.
func geminiText(resp *genai.GenerateContentResponse) string {
	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return ""
	}

	var b strings.Builder

	for _, p := range resp.Candidates[0].Content.Parts {
		if p != nil && !p.Thought {
			b.WriteString(p.Text)
		}
	}

	return b.String()
}

// geminiContent is a turn as Gemini takes it: the model's own as it was when there is
// one, and a function response for each result.
func geminiContent(m ports.LLMMessage) *genai.Content {
	if m.Role == ports.LLMAssistant {
		if native, ok := m.Native.(*genai.Content); ok && native != nil {
			return native
		}

		var parts []*genai.Part
		if m.Text != "" {
			parts = append(parts, genai.NewPartFromText(m.Text))
		}

		for _, tc := range m.ToolCalls {
			var args map[string]any
			_ = json.Unmarshal(tc.Input, &args)

			parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: tc.ID, Name: tc.Name, Args: args}})
		}

		return genai.NewContentFromParts(parts, genai.RoleModel)
	}

	var parts []*genai.Part

	for _, r := range m.ToolResults {
		// A function's response is an object: what the tool gave, or why it failed.
		response := map[string]any{"error": r.Content}

		if !r.IsError {
			var v any
			if err := json.Unmarshal([]byte(r.Content), &v); err != nil {
				v = r.Content
			}

			response = map[string]any{"output": v}
		}

		parts = append(parts, &genai.Part{FunctionResponse: &genai.FunctionResponse{ID: r.CallID, Name: r.Name, Response: response}})
	}

	if m.Text != "" || len(parts) == 0 {
		parts = append(parts, genai.NewPartFromText(m.Text))
	}

	return genai.NewContentFromParts(parts, genai.RoleUser)
}

// geminiLevels are the thinking levels of the efforts.
var geminiLevels = map[ports.LLMEffort]genai.ThinkingLevel{
	ports.LLMEffortLow:    genai.ThinkingLevelLow,
	ports.LLMEffortMedium: genai.ThinkingLevelMedium,
	ports.LLMEffortHigh:   genai.ThinkingLevelHigh,
}

// geminiModels are the models that answer: those that generate content.
func (c *Client) geminiModels(ctx context.Context, key ports.LLMKey) ([]ports.LLMModel, error) {
	client, err := c.gemini(ctx, key)
	if err != nil {
		return nil, err
	}

	var out []ports.LLMModel

	for m, err := range client.Models.All(ctx) {
		if err != nil {
			return nil, err
		}

		if !slices.Contains(m.SupportedActions, "generateContent") {
			continue
		}

		id := strings.TrimPrefix(m.Name, "models/")
		out = append(out, ports.LLMModel{ID: id, Name: firstOf(m.DisplayName, id)})
	}

	return out, nil
}

// geminiError is Gemini's refusal in its words, without the details it lists.
func geminiError(err error) error {
	var e genai.APIError
	if !errors.As(err, &e) {
		return err
	}

	return answered("Gemini", e.Code, e.Message)
}
