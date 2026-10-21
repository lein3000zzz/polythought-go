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
func (c *Client) anthropicComplete(ctx context.Context, key ports.LLMKey, req ports.LLMRequest, schema map[string]any) (ports.LLMResponse, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: cmpMax(req.MaxTokens),
		Messages:  make([]anthropic.MessageParam, len(req.Messages)),
	}

	for i, m := range req.Messages {
		if m.Role == ports.LLMAssistant {
			params.Messages[i] = anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Text))
		} else {
			params.Messages[i] = anthropic.NewUserMessage(anthropic.NewTextBlock(m.Text))
		}
	}

	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}

	if req.Temperature != nil {
		params.Temperature = anthropic.Float(*req.Temperature)
	}

	// Both in output_config: the answer's shape, and how hard Claude thinks, its depth's
	// only control on the newest models.
	if schema != nil {
		params.OutputConfig.Format = anthropic.JSONOutputFormatParam{Schema: schema}
	}

	if req.Effort != "" {
		params.OutputConfig.Effort = anthropic.OutputConfigEffort(req.Effort)
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

	var text strings.Builder

	for _, block := range message.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}

	return ports.LLMResponse{
		Text: text.String(), Model: string(message.Model), StopReason: string(message.StopReason),
		Usage: ports.LLMUsage{InputTokens: message.Usage.InputTokens, OutputTokens: message.Usage.OutputTokens},
	}, nil
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
