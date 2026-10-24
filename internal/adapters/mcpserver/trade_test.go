package mcpserver_test

import (
	"context"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

// trader is the trading service: going live waits for the user, as the confirmations
// service has it where the user has it wait, and the rest is kept.
type trader struct {
	mu        sync.Mutex
	set       trading.SetupChange
	cancelled domain.OrderID
}

func (t *trader) SetSetup(_ context.Context, p auth.Principal, _ domain.WorkflowID, c trading.SetupChange) (trading.Setup, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := p.Need(c.Mode.Scope()); err != nil {
		return trading.Setup{}, err
	}

	if c.Mode == domain.ModeLive {
		return trading.Setup{}, &domain.AwaitingError{Confirmation: held}
	}

	t.set = c

	return trading.Setup{Mode: c.Mode, LiveAvailable: true}, nil
}

func (*trader) SetKillSwitch(_ context.Context, _ auth.Principal, on bool) (trading.KillSwitch, error) {
	if !on {
		return trading.KillSwitch{}, &domain.AwaitingError{Confirmation: held}
	}

	return trading.KillSwitch{On: true, Since: at, Cancelled: 2}, nil
}

func (*trader) Reset(_ context.Context, _ auth.Principal, id domain.WorkflowID) (domain.PaperAccount, error) {
	return domain.PaperAccount{WorkflowID: id, Cash: decimal.NewFromInt(1000), StartedWith: decimal.NewFromInt(1000), ResetAt: at}, nil
}

func (t *trader) Cancel(_ context.Context, _ auth.Principal, w domain.WorkflowID, id domain.OrderID) (domain.PlacedOrder, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.cancelled = id

	return domain.PlacedOrder{
		ID: id, Status: domain.OrderCanceled, CreatedAt: at,
		OrderRequest: domain.OrderRequest{WorkflowID: w, Mode: domain.ModePaper, Side: domain.SideBuy, Type: domain.OrderGTC, Price: decimal.RequireFromString("0.4"), Shares: decimal.NewFromInt(10)},
	}, nil
}

func withTrader(t *testing.T, tr *trader, scopes ...domain.Scope) *mcp.ClientSession {
	t.Helper()

	d := deps(alice, &trades{}, scopes)
	d.Trader = tr

	return connect(t, d)
}

func TestTradeTools(t *testing.T) {
	tr := &trader{}
	agent := withTrader(t, tr, domain.ScopePaper, domain.ScopeLive)

	tools, err := agent.ListTools(context.Background(), nil)
	require.NoError(t, err)

	destructive := map[string]bool{}
	for _, tool := range tools.Tools {
		if !tool.Annotations.ReadOnlyHint {
			destructive[tool.Name] = *tool.Annotations.DestructiveHint
		}
	}

	assert.Equal(t, map[string]bool{"set_trading": false, "set_kill_switch": false, "reset_paper_account": true, "cancel_order": false}, destructive)

	t.Run("paper at once, live waiting for the user", func(t *testing.T) {
		got, _ := call(t, agent, "set_trading", map[string]any{"workflow": "wf-1", "mode": "paper"})
		assert.Equal(t, "paper", got["mode"])

		_, msg := call(t, agent, "set_trading", map[string]any{"workflow": "wf-1", "mode": "live", "wallet": "w-1", "confirmed": true})
		assert.Contains(t, msg, "it waits for the user to confirm it")
		assert.Contains(t, msg, "confirmation c-1")
	})

	t.Run("the kill switch", func(t *testing.T) {
		on, _ := call(t, agent, "set_kill_switch", map[string]any{"on": true})
		assert.Equal(t, map[string]any{"on": true, "since": "2026-10-23T12:00:00Z", "cancelled": 2.0}, on)

		_, msg := call(t, agent, "set_kill_switch", map[string]any{"on": false})
		assert.Contains(t, msg, "confirmation c-1")
	})

	t.Run("a paper reset and a cancel", func(t *testing.T) {
		reset, _ := call(t, agent, "reset_paper_account", map[string]any{"workflow": "wf-1"})
		assert.Equal(t, map[string]any{"cash": "1000", "startedWith": "1000", "resetAt": "2026-10-23T12:00:00Z"}, reset)

		cancelled, _ := call(t, agent, "cancel_order", map[string]any{"workflow": "wf-1", "order": "o-1"})
		assert.Equal(t, "canceled", cancelled["status"])

		tr.mu.Lock()
		defer tr.mu.Unlock()

		assert.Equal(t, domain.OrderID("o-1"), tr.cancelled)
	})

	t.Run("the scopes", func(t *testing.T) {
		reader := withTrader(t, tr, domain.ScopeRead)

		_, msg := call(t, reader, "reset_paper_account", map[string]any{"workflow": "wf-1"})
		assert.Contains(t, msg, "it needs the paper scope")

		_, msg = call(t, reader, "set_trading", map[string]any{"workflow": "wf-1", "mode": "paper"})
		assert.Contains(t, msg, "it needs the paper scope", "the service's to say, by the mode")
	})
}
