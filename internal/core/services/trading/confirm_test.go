package trading_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

// holder holds every action it is asked to, as the confirmations service does where the
// user has them wait.
type holder struct {
	kinds    []domain.ConfirmKind
	whats    []string
	payloads []any
}

func (h *holder) Hold(_ context.Context, _ auth.Principal, a domain.Ask) error {
	h.kinds, h.whats, h.payloads = append(h.kinds, a.Kind), append(h.whats, a.What), append(h.payloads, a.Payload)
	return &domain.AwaitingError{Confirmation: domain.Confirmation{ID: "c-1"}}
}

// A token's or an agent's going live, and turning the kill switch off, wait for its user
// where they say so (D406).
func TestLiveActionsWait(t *testing.T) {
	ctx := context.Background()
	live := token(domain.ScopeLive)

	t.Run("going live", func(t *testing.T) {
		h := &holder{}
		l := liveService(t, func(d *trading.Deps) { d.Confirm = h })
		l.repo.m = map[domain.WorkflowID]domain.WorkflowTrading{}

		_, err := l.svc.SetSetup(ctx, live, "wf", trading.SetupChange{Mode: domain.ModeLive, WalletID: "w-1"})
		require.ErrorIs(t, err, domain.ErrAwaitsConfirmation)
		assert.Empty(t, l.repo.m, "nothing changed meanwhile")
		assert.Equal(t, []domain.ConfirmKind{domain.ConfirmGoLive}, h.kinds)
		assert.Contains(t, h.whats[0], `live, from the wallet "Main", `)
		assert.Equal(t, trading.SetupChange{Mode: domain.ModeLive, WalletID: "w-1", Confirmed: true}, h.payloads[0],
			"the user's confirming it is their yes to real money")

		confirmed := live
		confirmed.Confirmed = true

		s, err := l.svc.SetSetup(ctx, confirmed, "wf", trading.SetupChange{Mode: domain.ModeLive, WalletID: "w-1"})
		require.NoError(t, err)
		assert.Equal(t, domain.ModeLive, s.Mode)
	})

	t.Run("live already, from the same wallet, and back to paper", func(t *testing.T) {
		h := &holder{}
		l := liveService(t, func(d *trading.Deps) { d.Confirm = h })

		_, err := l.svc.SetSetup(ctx, live, "wf", trading.SetupChange{Mode: domain.ModeLive, WalletID: "w-1"})
		require.NoError(t, err)

		_, err = l.svc.SetSetup(ctx, token(domain.ScopePaper), "wf", trading.SetupChange{Mode: domain.ModePaper, WalletID: "w-1"})
		require.NoError(t, err)
		assert.Empty(t, h.kinds)
	})

	t.Run("the kill switch off, never on", func(t *testing.T) {
		h := &holder{}
		us := &users{m: map[domain.UserID]domain.User{}}
		l := liveService(t, func(d *trading.Deps) { d.Users, d.Confirm = us, h })

		_, err := l.svc.SetKillSwitch(ctx, live, true)
		require.NoError(t, err)

		_, err = l.svc.SetKillSwitch(ctx, live, false)
		require.ErrorIs(t, err, domain.ErrAwaitsConfirmation)

		u, _ := us.ByID(ctx, "alice")
		assert.True(t, u.LiveStopped(), "still on meanwhile")
		assert.Equal(t, []domain.ConfirmKind{domain.ConfirmKillOff}, h.kinds)
	})
}
