package trading_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

// token is alice with an API token of only scopes.
func token(scopes ...domain.Scope) auth.Principal {
	return auth.Principal{User: domain.User{ID: "alice"}, TokenID: "t-1", Scopes: scopes}
}

// Paper and live told apart inside a route (D399): what a token or agent does on the live
// side needs live, on the paper side paper.
func TestTradingScopes(t *testing.T) {
	ctx := context.Background()
	paper, live := token(domain.ScopePaper), token(domain.ScopeLive)

	t.Run("going live", func(t *testing.T) {
		l := liveService(t)
		l.repo.m = map[domain.WorkflowID]domain.WorkflowTrading{}
		golive := trading.SetupChange{Mode: domain.ModeLive, WalletID: "w-1", Confirmed: true}

		_, err := l.svc.SetSetup(ctx, paper, "wf", golive)
		require.ErrorIs(t, err, domain.ErrInsufficientScope)
		assert.Contains(t, err.Error(), "it needs the live scope")
		assert.Empty(t, l.repo.m, "nothing changed")

		s, err := l.svc.SetSetup(ctx, live, "wf", golive)
		require.NoError(t, err)
		assert.Equal(t, domain.ModeLive, s.Mode)

		_, err = l.svc.SetSetup(ctx, live, "wf", trading.SetupChange{Mode: domain.ModePaper, WalletID: "w-1"})
		require.ErrorIs(t, err, domain.ErrInsufficientScope, "back to paper is paper's")

		_, err = l.svc.SetSetup(ctx, paper, "wf", trading.SetupChange{Mode: domain.ModePaper, WalletID: "w-1"})
		require.NoError(t, err)
	})

	t.Run("cancelling an order, by its mode", func(t *testing.T) {
		l, _ := safeService(t, domain.LiveCaps{})
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveResting}

		o, err := l.svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"))
		require.NoError(t, err)
		require.Equal(t, domain.ModeLive, o.Mode)

		_, err = l.svc.Cancel(ctx, paper, o.WorkflowID, o.ID)
		require.ErrorIs(t, err, domain.ErrInsufficientScope)
		assert.Empty(t, l.exchange.cancelled, "nothing sent to Polymarket")

		l.exchange.cancel = ports.LiveCancel{Canceled: true}
		l.exchange.states = []ports.LiveOrderState{{State: ports.LiveDone}}

		got, err := l.svc.Cancel(ctx, live, o.WorkflowID, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderCanceled, got.Status)

	})

	t.Run("cancelling a paper order", func(t *testing.T) {
		l := liveService(t)
		l.repo.m = map[domain.WorkflowID]domain.WorkflowTrading{} // wf trades on paper

		o, err := l.svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"))
		require.NoError(t, err)
		require.Equal(t, domain.ModePaper, o.Mode)

		_, err = l.svc.Cancel(ctx, live, o.WorkflowID, o.ID)
		require.ErrorIs(t, err, domain.ErrInsufficientScope, "a paper order is paper's")

		got, err := l.svc.Cancel(ctx, paper, o.WorkflowID, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderCanceled, got.Status)
	})

	t.Run("the kill switch: on with any scope, off with live", func(t *testing.T) {
		l, us := safeService(t, domain.LiveCaps{})
		read := token(domain.ScopeRead)

		ks, err := l.svc.SetKillSwitch(ctx, read, true)
		require.NoError(t, err)
		assert.True(t, ks.On)

		_, err = l.svc.SetKillSwitch(ctx, read, false)
		require.ErrorIs(t, err, domain.ErrInsufficientScope)

		u, _ := us.ByID(ctx, "alice")
		assert.True(t, u.LiveStopped(), "still on")

		_, err = l.svc.SetKillSwitch(ctx, live, false)
		require.NoError(t, err)

		u, _ = us.ByID(ctx, "alice")
		assert.False(t, u.LiveStopped())
	})
}
