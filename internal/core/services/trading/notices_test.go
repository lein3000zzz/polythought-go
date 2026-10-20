package trading_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

// notices hears what the trading service tells owners of.
type notices struct {
	mu     sync.Mutex
	events []domain.OrderEvent
}

func (n *notices) OrderHappened(e domain.OrderEvent) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.events = append(n.events, e)
}

// kinds are the kinds heard since the last call.
func (n *notices) kinds() []domain.OrderEventKind {
	n.mu.Lock()
	defer n.mu.Unlock()

	out := make([]domain.OrderEventKind, len(n.events))
	for i, e := range n.events {
		out[i] = e.Kind
	}

	n.events = nil

	return out
}

func TestOrderNotices(t *testing.T) {
	ctx := context.Background()

	t.Run("on paper, as placed and later (D229, D232)", func(t *testing.T) {
		now, heard := at, &notices{}
		svc := paperService(newLedger(), &feed{}, &now, func(d *trading.Deps) { d.Notices = heard })

		fak := rest(domain.OrderFAK, domain.SideBuy, "0.55", "10")
		o, err := svc.Place(ctx, fak)
		require.NoError(t, err)
		require.Equal(t, domain.OrderPartial, o.Status)
		assert.Equal(t, []domain.OrderEventKind{domain.OrderEventFilled}, heard.kinds(), "4 of 10 filled, the rest killed")

		_, err = svc.Place(ctx, rest(domain.OrderFAK, domain.SideBuy, "0.40", "10"))
		require.NoError(t, err)
		assert.Equal(t, []domain.OrderEventKind{domain.OrderEventUnfilled}, heard.kinds())

		_, err = svc.Place(ctx, rest(domain.OrderFAK, domain.SideSell, "0.40", "50"))
		require.NoError(t, err)
		assert.Equal(t, []domain.OrderEventKind{domain.OrderEventRefused}, heard.kinds(), "not enough shares")

		resting, err := svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"))
		require.NoError(t, err)
		assert.Equal(t, []domain.OrderEventKind{domain.OrderEventRested}, heard.kinds())

		_, err = svc.Cancel(ctx, owner, "wf", resting.ID)
		require.NoError(t, err)
		assert.Equal(t, []domain.OrderEventKind{domain.OrderEventCanceled}, heard.kinds())
	})

	t.Run("live: a pending order, once known", func(t *testing.T) {
		heard := &notices{}
		l := liveService(t, func(d *trading.Deps) { d.Notices = heard })
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveUnknown}

		o, err := l.svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"))
		require.NoError(t, err)
		require.Equal(t, domain.OrderPending, o.Status)
		assert.Empty(t, heard.kinds(), "nothing known yet")

		l.exchange.states = []ports.LiveOrderState{{State: ports.LiveResting}}
		require.NoError(t, l.svc.Follow(ctx))
		assert.Equal(t, []domain.OrderEventKind{domain.OrderEventRested}, heard.kinds())
	})
}
