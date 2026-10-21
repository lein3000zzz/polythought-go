package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
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

func (c *Client) openAIComplete(ctx context.Context, key ports.LLMKey, base string, cl call) (ports.LLMResponse, error) {
	req := cl.req
	params := openai.ChatCompletionNewParams{
		Model:    req.Model,
		Messages: make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages)+1),
	}

	if req.System != "" {
		params.Messages = append(params.Messages, openai.SystemMessage(req.System))
	}

	for _, m := range req.Messages {
		params.Messages = append(params.Messages, openAIMessages(m)...)
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

	if cl.schema != nil {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
			JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{Name: "answer", Schema: cl.schema, Strict: openai.Bool(strict(cl.schema))},
		}}
	}

	if req.Effort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(req.Effort)
	}

	for _, t := range cl.tools {
		fn := shared.FunctionDefinitionParam{Name: t.Name, Parameters: shared.FunctionParameters(t.schema), Strict: openai.Bool(strict(t.schema))}
		if t.Description != "" {
			fn.Description = openai.String(t.Description)
		}

		params.Tools = append(params.Tools, openai.ChatCompletionToolUnionParam{OfFunction: &openai.ChatCompletionFunctionToolParam{Function: fn}})
	}

	chat := openai.NewChatService(c.openAIOptions(key, base)...)

	resp, err := chat.Completions.New(ctx, params)
	if err != nil {
		return ports.LLMResponse{}, err
	}

	out := ports.LLMResponse{
		Model: resp.Model,
		Usage: ports.LLMUsage{InputTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.CompletionTokens},
		Turn:  ports.LLMMessage{Role: ports.LLMAssistant},
	}

	if len(resp.Choices) > 0 {
		msg := resp.Choices[0].Message
		out.Text, out.StopReason = msg.Content, resp.Choices[0].FinishReason
		out.Turn.Text, out.Turn.Native = msg.Content, msg.ToParam()

		for _, tc := range msg.ToolCalls {
			if tc.Type != "function" {
				continue
			}

			out.Turn.ToolCalls = append(out.Turn.ToolCalls, ports.LLMToolCall{
				ID: tc.ID, Name: tc.Function.Name, Input: json.RawMessage(tc.Function.Arguments),
			})
		}
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

// openAIMessages are a turn as Chat Completions takes it: an assistant's own as it was
// when there is one, and a tool message for each result.
func openAIMessages(m ports.LLMMessage) []openai.ChatCompletionMessageParamUnion {
	if m.Role == ports.LLMAssistant {
		if native, ok := m.Native.(openai.ChatCompletionMessageParamUnion); ok {
			return []openai.ChatCompletionMessageParamUnion{native}
		}

		asst := openai.ChatCompletionAssistantMessageParam{}
		if m.Text != "" {
			asst.Content.OfString = openai.String(m.Text)
		}

		for _, tc := range m.ToolCalls {
			asst.ToolCalls = append(asst.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
					ID:       tc.ID,
					Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{Name: tc.Name, Arguments: string(tc.Input)},
				},
			})
		}

		return []openai.ChatCompletionMessageParamUnion{{OfAssistant: &asst}}
	}

	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(m.ToolResults)+1)
	for _, r := range m.ToolResults {
		content := r.Content
		if r.IsError {
			content = "Error: " + content
		}

		out = append(out, openai.ToolMessage(content, r.CallID))
	}

	if m.Text != "" || len(out) == 0 {
		out = append(out, openai.UserMessage(m.Text))
	}

	return out
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
