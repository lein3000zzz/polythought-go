package models_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/credtypes"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/models"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

var (
	alice = auth.Principal{User: domain.User{ID: "alice"}}
	opus  = []ports.LLMModel{{ID: "claude-opus-5-5", Name: "Claude Opus 5.5"}}
	key   = ports.LLMKey{Provider: ports.LLMAnthropic, APIKey: "sk-ant-1"}
)

func TestList(t *testing.T) {
	ctx := context.Background()

	setup := func(t *testing.T) (*models.Service, *portsmocks.MockCredentialReader, *portsmocks.MockLLM, *time.Time) {
		creds, llm := portsmocks.NewMockCredentialReader(t), portsmocks.NewMockLLM(t)
		now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

		return models.New(models.Deps{Credentials: creds, LLM: llm, Now: func() time.Time { return now }}), creds, llm, &now
	}

	holds := func(creds *portsmocks.MockCredentialReader, apiKey string) {
		creds.EXPECT().Values(mock.Anything, domain.UserID("alice"), domain.CredentialID("c-1"), credtypes.LLMKeyTypes).
			Return(credtypes.AnthropicKey, map[string]string{"apiKey": apiKey}, nil).Once()
	}

	t.Run("asked once an hour", func(t *testing.T) {
		s, creds, llm, now := setup(t)

		holds(creds, "sk-ant-1")
		llm.EXPECT().Models(mock.Anything, key).Return(opus, nil).Once()
		got, err := s.List(ctx, alice, "c-1")
		require.NoError(t, err)
		assert.Equal(t, opus, got)

		*now = now.Add(59 * time.Minute)
		holds(creds, "sk-ant-1")
		got, err = s.List(ctx, alice, "c-1")
		require.NoError(t, err)
		assert.Equal(t, opus, got, "kept, the provider not asked")

		*now = now.Add(2 * time.Minute)
		holds(creds, "sk-ant-1")
		llm.EXPECT().Models(mock.Anything, key).Return(opus[:0], nil).Once()
		got, err = s.List(ctx, alice, "c-1")
		require.NoError(t, err)
		assert.Empty(t, got, "an hour on, asked again")
	})

	t.Run("a changed key asks again", func(t *testing.T) {
		s, creds, llm, _ := setup(t)

		holds(creds, "sk-ant-1")
		llm.EXPECT().Models(mock.Anything, key).Return(opus, nil).Once()
		_, err := s.List(ctx, alice, "c-1")
		require.NoError(t, err)

		holds(creds, "sk-ant-2")
		llm.EXPECT().Models(mock.Anything, ports.LLMKey{Provider: ports.LLMAnthropic, APIKey: "sk-ant-2"}).Return(nil, nil).Once()
		got, err := s.List(ctx, alice, "c-1")
		require.NoError(t, err)
		assert.Equal(t, []ports.LLMModel{}, got, "none, as a list")
	})

	t.Run("a failure is the provider's, and not kept", func(t *testing.T) {
		s, creds, llm, _ := setup(t)

		holds(creds, "sk-ant-1")
		llm.EXPECT().Models(mock.Anything, key).Return(nil, errors.New("Anthropic answered 401: invalid x-api-key")).Once()
		_, err := s.List(ctx, alice, "c-1")
		require.ErrorIs(t, err, domain.ErrProviderFailed)
		assert.ErrorContains(t, err, "Anthropic answered 401: invalid x-api-key")

		holds(creds, "sk-ant-1")
		llm.EXPECT().Models(mock.Anything, key).Return(opus, nil).Once()
		got, err := s.List(ctx, alice, "c-1")
		require.NoError(t, err)
		assert.Equal(t, opus, got)
	})

	t.Run("only the user's own keys, of a provider", func(t *testing.T) {
		s, creds, _, _ := setup(t)

		creds.EXPECT().Values(mock.Anything, domain.UserID("alice"), domain.CredentialID("c-9"), credtypes.LLMKeyTypes).
			Return("", nil, domain.ErrNotFound).Once()
		_, err := s.List(ctx, alice, "c-9")
		require.ErrorIs(t, err, domain.ErrNotFound)

		creds.EXPECT().Values(mock.Anything, domain.UserID("alice"), domain.CredentialID("c-2"), credtypes.LLMKeyTypes).
			Return("", nil, domain.ErrInvalidInput).Once()
		_, err = s.List(ctx, alice, "c-2")
		require.ErrorIs(t, err, domain.ErrInvalidInput, "a header is no provider's key")
	})
}
