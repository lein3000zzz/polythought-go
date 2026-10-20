package trading_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

func TestOrderLog(t *testing.T) {
	ctx := context.Background()

	l := liveService(t)
	l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveResting}

	live, err := l.svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"))
	require.NoError(t, err)

	paper := rest(domain.OrderGTC, domain.SideBuy, "0.40", "10")
	paper.WorkflowID = "wf-paper"
	_, err = l.svc.Place(ctx, paper)
	require.NoError(t, err)

	t.Run("live orders only, their markets named (D228)", func(t *testing.T) {
		page, err := l.svc.OrderLog(ctx, owner, ports.OrderLogFilter{})
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		assert.Equal(t, live.ID, page.Items[0].ID)
		assert.Equal(t, "Will it?", page.Items[0].Question)
		assert.Equal(t, "Yes", page.Items[0].Outcome)
	})

	t.Run("narrowed by wallet and status, and downloaded whole", func(t *testing.T) {
		none, err := l.svc.OrderLog(ctx, owner, ports.OrderLogFilter{WalletID: "w-other"})
		require.NoError(t, err)
		assert.Empty(t, none.Items)

		open, err := l.svc.OrderLog(ctx, owner, ports.OrderLogFilter{WalletID: "w-1", Status: domain.OrderOpen})
		require.NoError(t, err)
		assert.Len(t, open.Items, 1)

		all, err := l.svc.ExportOrders(ctx, owner, ports.OrderLogFilter{})
		require.NoError(t, err)
		assert.Len(t, all, 1)
	})
}
