package llm

import (
	"context"
	"errors"
	"slices"
	"strings"

	"google.golang.org/genai"

	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// gemini is a client of the Gemini API with the key. Backend, key and base URL are all
// given, so no GOOGLE_* or GEMINI_* variable of the server's picks them instead.
func (c *Client) gemini(ctx context.Context, key ports.LLMKey) (*genai.Client, error) {
	return genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:      key.APIKey,
		Backend:     genai.BackendGeminiAPI,
		HTTPClient:  c.public,
		HTTPOptions: genai.HTTPOptions{BaseURL: c.geminiURL},
	})
}

func (c *Client) geminiComplete(ctx context.Context, key ports.LLMKey, req ports.LLMRequest, schema map[string]any) (ports.LLMResponse, error) {
	client, err := c.gemini(ctx, key)
	if err != nil {
		return ports.LLMResponse{}, err
	}

	contents := make([]*genai.Content, len(req.Messages))
	for i, m := range req.Messages {
		role := genai.Role(genai.RoleUser)
		if m.Role == ports.LLMAssistant {
			role = genai.RoleModel
		}

		contents[i] = genai.NewContentFromText(m.Text, role)
	}

	config := &genai.GenerateContentConfig{MaxOutputTokens: int32(min(cmpMax(req.MaxTokens), 1<<31-1))} //nolint:gosec // bounded just before

	if req.System != "" {
		config.SystemInstruction = genai.NewContentFromText(req.System, genai.RoleUser)
	}

	if req.Temperature != nil {
		t := float32(*req.Temperature)
		config.Temperature = &t
	}

	if schema != nil {
		config.ResponseMIMEType, config.ResponseJsonSchema = "application/json", schema
	}

	if level, ok := geminiLevels[req.Effort]; ok {
		config.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: level}
	}

	resp, err := client.Models.GenerateContent(ctx, req.Model, contents, config)
	if err != nil {
		return ports.LLMResponse{}, err
	}

	out := ports.LLMResponse{Text: resp.Text(), Model: resp.ModelVersion}

	if len(resp.Candidates) > 0 {
		out.StopReason = string(resp.Candidates[0].FinishReason)
	}

	// Thoughts are billed as output too.
	if u := resp.UsageMetadata; u != nil {
		out.Usage = ports.LLMUsage{InputTokens: int64(u.PromptTokenCount), OutputTokens: int64(u.CandidatesTokenCount) + int64(u.ThoughtsTokenCount)}
	}

	return out, nil
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
