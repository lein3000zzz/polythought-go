package llm

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// openAIOptions are the options of a call to base with the key. The services are made
// from them alone, not by openai.NewClient, whose defaults read OPENAI_BASE_URL,
// OPENAI_ORG_ID, OPENAI_PROJECT_ID and OPENAI_CUSTOM_HEADERS: the server's, which a
// user's request to their own base URL must not carry.
func (c *Client) openAIOptions(key ports.LLMKey, base string) []option.RequestOption {
	client := c.public

	switch {
	case base == c.openAIURL:
	case strings.HasPrefix(base, "http://"):
		client = c.local // a server on the network, the key in clear
	default:
		client = c.guarded // an address the user chose
	}

	return []option.RequestOption{
		option.WithAPIKey(key.APIKey),
		option.WithBaseURL(base),
		option.WithHTTPClient(client),
		option.WithMaxRetries(maxRetries),
	}
}

func (c *Client) openAIComplete(ctx context.Context, key ports.LLMKey, base string, req ports.LLMRequest, schema map[string]any) (ports.LLMResponse, error) {
	params := openai.ChatCompletionNewParams{
		Model:    req.Model,
		Messages: make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages)+1),
	}

	if req.System != "" {
		params.Messages = append(params.Messages, openai.SystemMessage(req.System))
	}

	for _, m := range req.Messages {
		if m.Role == ports.LLMAssistant {
			params.Messages = append(params.Messages, openai.AssistantMessage(m.Text))
		} else {
			params.Messages = append(params.Messages, openai.UserMessage(m.Text))
		}
	}

	// OpenAI's own wants max_completion_tokens for its newer models; compatible APIs
	// mostly know max_tokens.
	if base == c.openAIURL {
		params.MaxCompletionTokens = openai.Int(cmpMax(req.MaxTokens))
	} else {
		params.MaxTokens = openai.Int(cmpMax(req.MaxTokens))
	}

	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	}

	if schema != nil {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
			JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{Name: "answer", Schema: schema, Strict: openai.Bool(strict(schema))},
		}}
	}

	if req.Effort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(req.Effort)
	}

	chat := openai.NewChatService(c.openAIOptions(key, base)...)

	resp, err := chat.Completions.New(ctx, params)
	if err != nil {
		return ports.LLMResponse{}, err
	}

	out := ports.LLMResponse{
		Model: resp.Model,
		Usage: ports.LLMUsage{InputTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.CompletionTokens},
	}

	if len(resp.Choices) > 0 {
		out.Text, out.StopReason = resp.Choices[0].Message.Content, resp.Choices[0].FinishReason
	}

	// OpenRouter says what it charged, in its credits: dollars (D250). A field the SDK
	// does not know is never Valid, having nothing to decode into: its raw JSON is read.
	if cost, ok := resp.Usage.JSON.ExtraFields["cost"]; ok {
		if d, err := decimal.NewFromString(strings.TrimSpace(cost.Raw())); err == nil {
			out.Usage.Cost = &d
		}
	}

	return out, nil
}

func (c *Client) openAIModels(ctx context.Context, key ports.LLMKey, base string) ([]ports.LLMModel, error) {
	models := openai.NewModelService(c.openAIOptions(key, base)...)
	pages := models.ListAutoPaging(ctx)

	var out []ports.LLMModel
	for pages.Next() {
		m := pages.Current()
		out = append(out, ports.LLMModel{ID: m.ID, Name: m.ID})
	}

	return out, pages.Err()
}

// openAIError is the API's refusal in its words, named by its host unless it is OpenAI's
// own: the SDK's own error keeps only the status.
func (c *Client) openAIError(err error, base string) error {
	var e *openai.Error
	if !errors.As(err, &e) {
		return err
	}

	who := "OpenAI"
	if u, perr := url.Parse(base); perr == nil && base != c.openAIURL {
		who = u.Host
	}

	return answered(who, e.StatusCode, e.Message)
}

// strict is whether a schema fits OpenAI's strict mode, which then holds the answer to it:
// every object closed, with additionalProperties false, and requiring all its
// properties. One that does not is sent as a guide only, rather than refused.
func strict(schema any) bool {
	switch s := schema.(type) {
	case map[string]any:
		if s["type"] == "object" || s["properties"] != nil {
			if s["additionalProperties"] != false {
				return false
			}

			props, _ := s["properties"].(map[string]any)
			required, _ := s["required"].([]any)

			for name := range props {
				if !slices.Contains(required, any(name)) {
					return false
				}
			}
		}

		for _, v := range s {
			if !strict(v) {
				return false
			}
		}
	case []any:
		for _, v := range s {
			if !strict(v) {
				return false
			}
		}
	}

	return true
}
