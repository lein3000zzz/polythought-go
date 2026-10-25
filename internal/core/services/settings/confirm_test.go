package settings_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
)

// holder holds what it is asked to where the admin has it wait, as the confirmations
// service does.
type holder struct {
	asks []domain.Ask
}

func (h *holder) Hold(_ context.Context, p auth.Principal, a domain.Ask) error {
	if !p.User.Unconfirmed.Waits(a.Kind) || p.Confirmed {
		return nil
	}

	h.asks = append(h.asks, a)

	return &domain.AwaitingError{Confirmation: domain.Confirmation{ID: "c-1"}}
}

// An admin's agent's change to Config and its restarting the server wait for the admin
// where they say so; a secret setting is never the agent's to set (D411).
func TestAgentsOnConfig(t *testing.T) {
	t.Parallel()

	agent := auth.Principal{User: root.User, GrantID: "g-1", Agent: "Claude Code", Scopes: []domain.Scope{domain.ScopeAdmin}}
	open := map[string]json.RawMessage{keyRegistration: json.RawMessage(`"open"`), keyMaxWorkflows: json.RawMessage(`null`)}

	held := func(t *testing.T) (*settings.Service, *memRepo, *holder, *[]domain.UserID) {
		t.Helper()

		var restarts []domain.UserID

		repo := newRepo(saved(keyMaxWorkflows, `5`))
		s := load(t, repo, nil, settings.WithRestarter(func(u domain.User) { restarts = append(restarts, u.ID) }))
		h := &holder{}
		s.UseConfirmations(h)

		return s, repo, h, &restarts
	}

	t.Run("a change waits, saying what it does", func(t *testing.T) {
		t.Parallel()

		s, repo, h, _ := held(t)

		require.ErrorIs(t, s.Update(context.Background(), agent, open), domain.ErrAwaitsConfirmation)
		assert.Empty(t, repo.applied, "nothing saved yet")
		require.Len(t, h.asks, 1)
		assert.Equal(t, domain.ConfirmConfig, h.asks[0].Kind)
		assert.Contains(t, h.asks[0].What, `Registration to "open"`)
		assert.Contains(t, h.asks[0].What, "Workflows per user back to its default")
		assert.Equal(t, open, h.asks[0].Payload, "what a confirmation carries out")
	})

	t.Run("one that breaks the rules is refused at once, never asked", func(t *testing.T) {
		t.Parallel()

		s, _, h, _ := held(t)

		require.ErrorIs(t, s.Update(context.Background(), agent, map[string]json.RawMessage{keyMaxWorkflows: json.RawMessage(`0`)}), domain.ErrInvalidInput)
		assert.Empty(t, h.asks)
	})

	t.Run("confirmed, it is saved", func(t *testing.T) {
		t.Parallel()

		s, repo, h, _ := held(t)
		confirmed := agent
		confirmed.Confirmed = true

		require.NoError(t, s.Update(context.Background(), confirmed, open))
		assert.Empty(t, h.asks)
		assert.Len(t, repo.applied, 1)
	})

	t.Run("where the admin lets it go, and the admin's own", func(t *testing.T) {
		t.Parallel()

		s, repo, h, _ := held(t)
		lets := agent
		lets.User.Unconfirmed.Config = true

		require.NoError(t, s.Update(context.Background(), lets, open))
		require.NoError(t, s.Update(context.Background(), root, map[string]json.RawMessage{keyRegistration: json.RawMessage(`"closed"`)}))
		assert.Empty(t, h.asks)
		assert.Len(t, repo.applied, 2)
	})

	t.Run("a secret, never", func(t *testing.T) {
		t.Parallel()

		s, repo, h, _ := held(t)
		lets := agent
		lets.User.Unconfirmed.Config = true

		for _, who := range []auth.Principal{agent, lets} {
			err := s.Update(context.Background(), who, map[string]json.RawMessage{keyToken: json.RawMessage(`"123:abc"`)})
			require.ErrorIs(t, err, domain.ErrForbidden)
			assert.Contains(t, err.Error(), "Bot token is secret: an admin sets it at a browser")
		}

		assert.Empty(t, h.asks)
		assert.Empty(t, repo.applied)

		require.NoError(t, s.Update(context.Background(), root, map[string]json.RawMessage{keyToken: json.RawMessage(`"123:abc"`)}))
	})

	t.Run("nor where a secret goes, such as the mail server (A7)", func(t *testing.T) {
		t.Parallel()

		s, repo, h, _ := held(t)
		lets := agent
		lets.User.Unconfirmed.Config = true

		for _, who := range []auth.Principal{agent, lets} {
			err := s.Update(context.Background(), who, map[string]json.RawMessage{keyMailHost: json.RawMessage(`"smtp.evil.example"`)})
			require.ErrorIs(t, err, domain.ErrForbidden)
			assert.Contains(t, err.Error(), "Mail server says where a secret goes: an admin sets it at a browser")
		}

		assert.Empty(t, h.asks)
		assert.Empty(t, repo.applied)

		require.NoError(t, s.Update(context.Background(), root, map[string]json.RawMessage{keyMailHost: json.RawMessage(`"smtp.example"`)}))
	})

	t.Run("a restart waits", func(t *testing.T) {
		t.Parallel()

		s, _, h, restarts := held(t)

		require.ErrorIs(t, s.Restart(context.Background(), agent), domain.ErrAwaitsConfirmation)
		assert.Empty(t, *restarts)
		require.Len(t, h.asks, 1)
		assert.Equal(t, domain.Ask{Kind: domain.ConfirmRestart, What: "Restart the server", Payload: settings.RestartAsked{}}, h.asks[0])

		confirmed := agent
		confirmed.Confirmed = true

		require.NoError(t, s.Restart(context.Background(), confirmed))
		assert.Equal(t, []domain.UserID{"root"}, *restarts)
	})
}
