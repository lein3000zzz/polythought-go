package credtypes

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// The language model providers' keys (D250): a type each, the type telling the provider.
const (
	AnthropicKey = "anthropic.apiKey"
	OpenAIKey    = "openai.apiKey"
	GeminiKey    = "gemini.apiKey"
)

// LLMKeyTypes are the providers' key types, for a setting taking any of them (D254).
var LLMKeyTypes = []string{AnthropicKey, OpenAIKey, GeminiKey}

var llmProviders = map[string]ports.LLMProvider{
	AnthropicKey: ports.LLMAnthropic,
	OpenAIKey:    ports.LLMOpenAI,
	GeminiKey:    ports.LLMGemini,
}

// LLMTypes are the providers' credential types. Each Test lists the models the key can
// use, which shows it works and costs nothing (D252).
func LLMTypes(llm ports.LLM) []engine.CredentialType {
	apiKey := func(where string) engine.CredentialField {
		return engine.CredentialField{Name: "apiKey", Label: "API key", Required: true, Secret: true, Description: where}
	}

	test := func(credType string) func(context.Context, map[string]string) error {
		return func(ctx context.Context, values map[string]string) error {
			key, _ := LLMKey(credType, values)
			_, err := llm.Models(ctx, key)

			return err
		}
	}

	return []engine.CredentialType{
		{
			Name:  AnthropicKey,
			Label: "Anthropic API key",
			Description: "A key for Anthropic's Claude models. Calls made with it are billed to its account, not " +
				"Polythought's.",
			Fields: []engine.CredentialField{apiKey("From the Claude Console, under API keys.")},
			Check:  checkLLM,
			Test:   test(AnthropicKey),
		},
		{
			Name:  OpenAIKey,
			Label: "OpenAI-compatible API key",
			Description: "A key for OpenAI, or for any API compatible with it: OpenRouter, or a model served on " +
				"your own network, at its base URL. Billed to the key's account; OpenRouter also reports what each " +
				"call cost.",
			Fields: []engine.CredentialField{
				apiKey("From the provider's dashboard, e.g. OpenAI's API keys page."),
				{
					Name: "baseUrl", Label: "Base URL",
					Description: "Where the API is, e.g. https://openrouter.ai/api/v1; empty for OpenAI's own. Plain " +
						"http only when the administrator allows private addresses, for a server on your network.",
				},
			},
			Check: checkLLM,
			Test:  test(OpenAIKey),
		},
		{
			Name:        GeminiKey,
			Label:       "Google Gemini API key",
			Description: "A key for Google's Gemini models, through the Gemini API. Billed to the key's Google project.",
			Fields:      []engine.CredentialField{apiKey("From Google AI Studio, under API keys.")},
			Check:       checkLLM,
			Test:        test(GeminiKey),
		},
	}
}

// LLMKey is the key a credential of credType holds; false when credType is no
// provider's.
func LLMKey(credType string, values map[string]string) (ports.LLMKey, bool) {
	provider, ok := llmProviders[credType]
	if !ok {
		return ports.LLMKey{}, false
	}

	return ports.LLMKey{
		Provider: provider,
		APIKey:   strings.TrimSpace(values["apiKey"]),
		BaseURL:  strings.TrimSpace(values["baseUrl"]),
	}, true
}

func checkLLM(values map[string]string) error {
	if strings.ContainsFunc(strings.TrimSpace(values["apiKey"]), func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return errors.New("the API key cannot contain spaces or line breaks")
	}

	if base := strings.TrimSpace(values["baseUrl"]); base != "" {
		if _, err := ports.ParseLLMBaseURL(base); err != nil {
			return err
		}
	}

	return nil
}
