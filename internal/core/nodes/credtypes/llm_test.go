package credtypes_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/credtypes"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

func TestLLMTypes(t *testing.T) {
	llm := portsmocks.NewMockLLM(t)
	types := credtypes.LLMTypes(llm)

	r := engine.NewRegistry()
	byName := map[string]engine.CredentialType{}

	for _, typ := range types {
		require.NoError(t, r.RegisterCredentialType(typ))
		byName[typ.Name] = typ
	}

	require.Len(t, byName, 3)

	t.Run("checked as saved", func(t *testing.T) {
		openAI := byName[credtypes.OpenAIKey]

		for _, tc := range []struct {
			name    string
			values  map[string]string
			wantErr string
		}{
			{name: "OpenAI's own", values: map[string]string{"apiKey": "sk-proj-1"}},
			{name: "OpenRouter", values: map[string]string{"apiKey": " sk-or-1 ", "baseUrl": "https://openrouter.ai/api/v1"}},
			{name: "on the network", values: map[string]string{"apiKey": "k", "baseUrl": "http://10.0.0.5:11434/v1"}},
			{name: "a space inside", values: map[string]string{"apiKey": "sk 1"}, wantErr: "cannot contain spaces"},
			{name: "a line break", values: map[string]string{"apiKey": "sk-1\nX-Other: 1"}, wantErr: "cannot contain spaces or line breaks"},
			{name: "not a URL", values: map[string]string{"apiKey": "k", "baseUrl": "openrouter.ai"}, wantErr: "an http or https address"},
			{name: "another scheme", values: map[string]string{"apiKey": "k", "baseUrl": "ftp://x.example/v1"}, wantErr: "an http or https address"},
			{name: "a user in it", values: map[string]string{"apiKey": "k", "baseUrl": "https://me:pw@x.example/v1"}, wantErr: "no user, query or fragment"},
			{name: "a query", values: map[string]string{"apiKey": "k", "baseUrl": "https://x.example/v1?key=1"}, wantErr: "no user, query or fragment"},
		} {
			err := openAI.Check(tc.values)
			if tc.wantErr == "" {
				assert.NoError(t, err, tc.name)
				continue
			}

			assert.ErrorContains(t, err, tc.wantErr, tc.name)
		}

		assert.ErrorContains(t, byName[credtypes.AnthropicKey].Check(map[string]string{"apiKey": "sk ant"}), "cannot contain spaces")
	})

	t.Run("tested by listing its models", func(t *testing.T) {
		ctx := context.Background()

		llm.EXPECT().Models(mock.Anything, ports.LLMKey{Provider: ports.LLMOpenAI, APIKey: "sk-or-1", BaseURL: "https://openrouter.ai/api/v1"}).
			Return([]ports.LLMModel{{ID: "a/b", Name: "a/b"}}, nil).Once()
		require.NoError(t, byName[credtypes.OpenAIKey].Test(ctx, map[string]string{"apiKey": " sk-or-1", "baseUrl": "https://openrouter.ai/api/v1 "}))

		refused := errors.New("Anthropic answered 401: invalid x-api-key")
		llm.EXPECT().Models(mock.Anything, ports.LLMKey{Provider: ports.LLMAnthropic, APIKey: "sk-ant-1"}).Return(nil, refused).Once()
		require.ErrorIs(t, byName[credtypes.AnthropicKey].Test(ctx, map[string]string{"apiKey": "sk-ant-1"}), refused)

		llm.EXPECT().Models(mock.Anything, ports.LLMKey{Provider: ports.LLMGemini, APIKey: "gem-1"}).Return(nil, nil).Once()
		require.NoError(t, byName[credtypes.GeminiKey].Test(ctx, map[string]string{"apiKey": "gem-1"}))
	})

	t.Run("a key from its type", func(t *testing.T) {
		key, ok := credtypes.LLMKey(credtypes.GeminiKey, map[string]string{"apiKey": "gem-1", "baseUrl": ""})
		assert.True(t, ok)
		assert.Equal(t, ports.LLMKey{Provider: ports.LLMGemini, APIKey: "gem-1"}, key)

		_, ok = credtypes.LLMKey(credtypes.HTTPHeader, map[string]string{"value": "Bearer x"})
		assert.False(t, ok, "a header is no provider's key")
	})
}
