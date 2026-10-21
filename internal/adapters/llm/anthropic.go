package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// defaultMaxTokens is what an answer may take when the request does not say.
const defaultMaxTokens = 16000

func (c *Client) anthropic(key ports.LLMKey) anthropic.Client {
	return anthropic.NewClient(
		option.WithoutEnvironmentDefaults(), // no ANTHROPIC_* variable, profile or token of the server's
		option.WithAPIKey(key.APIKey),
		option.WithBaseURL(c.anthropicURL),
		option.WithHTTPClient(c.public),
		option.WithMaxRetries(maxRetries),
	)
}

// anthropicComplete streams the answer and gathers it whole: a long one would outlast a
// plain request.
func (c *Client) anthropicComplete(ctx context.Context, key ports.LLMKey, cl call) (ports.LLMResponse, error) {
	req := cl.req
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: cmpMax(req.MaxTokens),
		Messages:  make([]anthropic.MessageParam, len(req.Messages)),
	}

	for i, m := range req.Messages {
		params.Messages[i] = anthropicMessage(m)
	}

	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}

	if req.Temperature != nil {
		params.Temperature = anthropic.Float(*req.Temperature)
	}

	// Both in output_config: the answer's shape, and how hard Claude thinks, its depth's
	// only control on the newest models.
	if cl.schema != nil {
		params.OutputConfig.Format = anthropic.JSONOutputFormatParam{Schema: cl.schema}
	}

	if req.Effort != "" {
		params.OutputConfig.Effort = anthropic.OutputConfigEffort(req.Effort)
	}

	for _, t := range cl.tools {
		params.Tools = append(params.Tools, anthropicTool(t))
	}

	client := c.anthropic(key)
	stream := client.Messages.NewStreaming(ctx, params)

	var message anthropic.Message
	for stream.Next() {
		if err := message.Accumulate(stream.Current()); err != nil {
			return ports.LLMResponse{}, err
		}
	}

	if err := stream.Err(); err != nil {
		return ports.LLMResponse{}, err
	}

	// The turn goes back as it came, thinking blocks and their signatures with it, which
	// Claude checks when tool results follow.
	var text strings.Builder

	turn := ports.LLMMessage{Role: ports.LLMAssistant, Native: message.ToParam()}

	for _, block := range message.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			text.WriteString(b.Text)
		case anthropic.ToolUseBlock:
			turn.ToolCalls = append(turn.ToolCalls, ports.LLMToolCall{ID: b.ID, Name: b.Name, Input: b.Input})
		}
	}

	turn.Text = text.String()

	return ports.LLMResponse{
		Text: turn.Text, Model: string(message.Model), StopReason: string(message.StopReason),
		Usage: ports.LLMUsage{InputTokens: message.Usage.InputTokens, OutputTokens: message.Usage.OutputTokens},
		Turn:  turn,
	}, nil
}

// anthropicMessage is a turn as Claude takes it: an assistant's own as it was when there
// is one, tool results first in a user's.
func anthropicMessage(m ports.LLMMessage) anthropic.MessageParam {
	var blocks []anthropic.ContentBlockParamUnion

	if m.Role == ports.LLMAssistant {
		if native, ok := m.Native.(anthropic.MessageParam); ok {
			return native
		}

		if m.Text != "" {
			blocks = append(blocks, anthropic.NewTextBlock(m.Text))
		}

		for _, tc := range m.ToolCalls {
			blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, tc.Input, tc.Name))
		}

		return anthropic.NewAssistantMessage(blocks...)
	}

	for _, r := range m.ToolResults {
		blocks = append(blocks, anthropic.NewToolResultBlock(r.CallID, r.Content, r.IsError))
	}

	if m.Text != "" || len(blocks) == 0 {
		blocks = append(blocks, anthropic.NewTextBlock(m.Text))
	}

	return anthropic.NewUserMessage(blocks...)
}

// anthropicTool is a tool as Claude takes it: strict when its schema fits, and its input
// streamed as it is written. Claude then does not check that input, so the tool must.
func anthropicTool(t tool) anthropic.ToolUnionParam {
	input := anthropic.ToolInputSchemaParam{Properties: t.schema["properties"], ExtraFields: map[string]any{}}

	for k, v := range t.schema {
		switch k {
		case "type", "properties":
		case "required":
			for _, r := range asList(v) {
				if name, ok := r.(string); ok {
					input.Required = append(input.Required, name)
				}
			}
		default:
			input.ExtraFields[k] = v
		}
	}

	param := anthropic.ToolParam{Name: t.Name, InputSchema: input, EagerInputStreaming: anthropic.Bool(true)}

	if t.Description != "" {
		param.Description = anthropic.String(t.Description)
	}

	if strict(t.schema) {
		param.Strict = anthropic.Bool(true)
	}

	return anthropic.ToolUnionParam{OfTool: &param}
}

func asList(v any) []any {
	list, _ := v.([]any)
	return list
}

func (c *Client) anthropicModels(ctx context.Context, key ports.LLMKey) ([]ports.LLMModel, error) {
	client := c.anthropic(key)
	pages := client.Models.ListAutoPaging(ctx, anthropic.ModelListParams{})

	var out []ports.LLMModel
	for pages.Next() {
		m := pages.Current()
		out = append(out, ports.LLMModel{ID: m.ID, Name: firstOf(m.DisplayName, m.ID)})
	}

	return out, pages.Err()
}

// anthropicError is Anthropic's refusal in its words: the message of its error body.
func anthropicError(err error) error {
	var e *anthropic.Error
	if !errors.As(err, &e) {
		return err
	}

	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(e.RawJSON()), &body)

	return answered("Anthropic", e.StatusCode, body.Error.Message)
}

func cmpMax(n int64) int64 {
	if n <= 0 {
		return defaultMaxTokens
	}

	return n
}

func firstOf(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}

	return ""
}
