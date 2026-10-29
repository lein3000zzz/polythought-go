package trading_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

// switches is the history: what it was told.
type switches struct {
	mu   sync.Mutex
	kept []domain.Switch
}

func (s *switches) Switched(_ context.Context, sw domain.Switch) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.kept = append(s.kept, sw)
}

func (s *switches) take() []domain.Switch {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := s.kept
	s.kept = nil

	return out
}

// Who switched a workflow to paper or live, another wallet, reset it or turned the kill
// switch as it traded live, and why, is kept (D433); a change to nothing, not.
func TestTradingHistory(t *testing.T) {
	ctx := context.Background()
	h := &switches{}
	us := &users{m: map[domain.UserID]domain.User{}}
	l := liveService(t, func(d *trading.Deps) { d.History, d.Users = h, us })
	l.repo.m["wf-paper"] = domain.WorkflowTrading{WorkflowID: "wf-paper", Mode: domain.ModePaper}

	agent := auth.Principal{User: domain.User{ID: "alice"}, GrantID: "g-1", Agent: "Claude Code", Scopes: domain.Scopes, Reason: "the drawdown is too deep"}

	t.Run("paper, live, another wallet", func(t *testing.T) {
		_, err := l.svc.SetSetup(ctx, agent, "wf", trading.SetupChange{Mode: domain.ModePaper, WalletID: "w-1"})
		require.NoError(t, err)

		_, err = l.svc.SetSetup(ctx, owner, "wf", trading.SetupChange{Mode: domain.ModeLive, WalletID: "w-2", Confirmed: true})
		require.NoError(t, err)

		_, err = l.svc.SetSetup(ctx, owner, "wf", trading.SetupChange{Mode: domain.ModeLive, WalletID: "w-1", Confirmed: true})
		require.NoError(t, err)

		_, err = l.svc.SetSetup(ctx, owner, "wf", trading.SetupChange{Mode: domain.ModeLive, WalletID: "w-1", Confirmed: true})
		require.NoError(t, err)

		got := h.take()
		require.Len(t, got, 3, "the last changed nothing")

		assert.Equal(t, domain.SwitchPaper, got[0].Kind)
		assert.Equal(t, "Claude Code", got[0].By.Name)
		assert.Equal(t, "the drawdown is too deep", got[0].Reason)
		assert.Equal(t, domain.Switch{WorkflowID: "wf", Kind: domain.SwitchLive, WalletID: "w-2"}, got[1], "the user, from w-2")
		assert.Equal(t, domain.Switch{WorkflowID: "wf", Kind: domain.SwitchWallet, WalletID: "w-1"}, got[2])
	})

	t.Run("a reset, not the account's opening", func(t *testing.T) {
		_, err := l.svc.Reset(ctx, owner, "wf")
		require.NoError(t, err)
		assert.Empty(t, h.take(), "opened, not reset")

		_, err = l.svc.Reset(ctx, agent, "wf")
		require.NoError(t, err)

		got := h.take()
		require.Len(t, got, 1)
		assert.Equal(t, domain.SwitchReset, got[0].Kind)
		assert.Equal(t, "the drawdown is too deep", got[0].Reason)
	})

	t.Run("the kill switch, on those trading live alone, as it changes", func(t *testing.T) {
		_, err := l.svc.SetKillSwitch(ctx, agent, true)
		require.NoError(t, err)
		_, err = l.svc.SetKillSwitch(ctx, owner, true)
		require.NoError(t, err)

		got := h.take()
		require.Len(t, got, 1, "on already the second time; wf-paper trades on paper")
		assert.Equal(t, domain.WorkflowID("wf"), got[0].WorkflowID)
		assert.Equal(t, domain.SwitchKillOn, got[0].Kind)
		assert.Equal(t, "Claude Code", got[0].By.Name)

		_, err = l.svc.SetKillSwitch(ctx, owner, false)
		require.NoError(t, err)
		_, err = l.svc.SetKillSwitch(ctx, owner, false)
		require.NoError(t, err)

		got = h.take()
		require.Len(t, got, 1, "off already the second time")
		assert.Equal(t, domain.Switch{WorkflowID: "wf", Kind: domain.SwitchKillOff}, got[0])

		_, err = l.svc.StopLive(ctx, "alice", "cancelled: an admin disabled the account")
		require.NoError(t, err)

		got = h.take()
		require.Len(t, got, 1)
		assert.Equal(t, domain.Switch{WorkflowID: "wf", Kind: domain.SwitchKillOn, Polythought: true, Reason: "an admin disabled the account"}, got[0])
	})

	t.Run("back to paper, its wallet unlinked", func(t *testing.T) {
		require.NoError(t, l.svc.ReleaseWallet(ctx, "alice", "w-1"))

		got := h.take()
		require.Len(t, got, 1)
		assert.Equal(t, domain.Switch{WorkflowID: "wf", Kind: domain.SwitchPaper, Polythought: true, Reason: "its wallet was unlinked"}, got[0])
	})
}
