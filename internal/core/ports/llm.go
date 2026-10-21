package ports

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/shopspring/decimal"
)

// Language models (phase 4): a call to a provider, with its owner's own key (D243), the
// same shape for every provider (D249).

// LLMProvider is who answers: the credential type a key comes from (D250).
type LLMProvider string

const (
	LLMAnthropic LLMProvider = "anthropic"
	LLMOpenAI    LLMProvider = "openai" // OpenAI and any compatible API: OpenRouter, a local server
	LLMGemini    LLMProvider = "gemini"
)

// LLMKey is a user's key for a provider, from their vault.
type LLMKey struct {
	Provider LLMProvider
	APIKey   string
	// BaseURL is where an OpenAI-compatible API is: empty for OpenAI's own.
	BaseURL string
}

// ParseLLMBaseURL reads an OpenAI-compatible base URL: http or https, a host, and
// nothing a key could leak through, no user, query or fragment.
func ParseLLMBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))

	switch {
	case err != nil, u.Scheme != "https" && u.Scheme != "http", u.Hostname() == "":
		return nil, errors.New("the base URL must be an http or https address, like https://openrouter.ai/api/v1")
	case u.User != nil, u.RawQuery != "", u.Fragment != "":
		return nil, errors.New("the base URL takes no user, query or fragment")
	}

	return u, nil
}

type LLMRole string

const (
	LLMUser      LLMRole = "user"
	LLMAssistant LLMRole = "assistant"
)

// LLMMessage is one turn of the conversation.
type LLMMessage struct {
	Role LLMRole
	Text string
}

// LLMTool is a tool the model may ask for (4c).
type LLMTool struct {
	Name        string
	Description string
	Schema      json.RawMessage // its input's JSON schema
}

// LLMRequest is one call.
type LLMRequest struct {
	Model     string
	System    string
	Messages  []LLMMessage
	MaxTokens int64
	// Temperature is the provider's own when nil; Claude's newest models refuse any.
	Temperature *float64

	Schema json.RawMessage // a JSON schema the answer keeps to (4b)
	Tools  []LLMTool       // (4c)
}

// LLMUsage is what a call took: tokens, and what the provider says it charged, when it
// says (OpenRouter), in dollars.
type LLMUsage struct {
	InputTokens  int64
	OutputTokens int64
	Cost         *decimal.Decimal
}

// LLMResponse is a call's answer.
type LLMResponse struct {
	Text       string
	Model      string // as the provider names the one that answered
	StopReason string
	Usage      LLMUsage
}

// LLMModel is a model a key can use.
type LLMModel struct {
	ID   string
	Name string // for people; the ID when the provider gives none
}

// ErrLLMNotYet is for what the port has room for but no slice has built yet: a JSON
// schema (4b), tools (4c).
var ErrLLMNotYet = errors.New("not supported yet")

// LLM calls a provider with a user's key. Errors from the provider keep its words, never
// the key.
type LLM interface {
	Complete(ctx context.Context, key LLMKey, req LLMRequest) (LLMResponse, error)
	// Models are those the key can use, as the provider lists them.
	Models(ctx context.Context, key LLMKey) ([]LLMModel, error)
}
