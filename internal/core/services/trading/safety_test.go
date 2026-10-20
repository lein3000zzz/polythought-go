package trading_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

// users keeps kill switches and caps; a user never seen has neither.
type users struct {
	mu sync.Mutex
	m  map[domain.UserID]domain.User
}

func (u *users) ByID(_ context.Context, id domain.UserID) (domain.User, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	usr, ok := u.m[id]
	if !ok {
		usr.ID = id
	}

	return usr, nil
}

func (u *users) SetLiveStopped(_ context.Context, id domain.UserID, at time.Time) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	usr := u.m[id]
	usr.ID, usr.LiveStoppedAt = id, at
	u.m[id] = usr

	return nil
}

func (u *users) set(usr domain.User) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.m[usr.ID] = usr
}

func dollars(n int) *int { return &n }

// safeService is liveService with alice's kill switch and caps, and the instance's caps.
func safeService(t *testing.T, instance domain.LiveCaps) (live, *users) {
	t.Helper()

	us := &users{m: map[domain.UserID]domain.User{}}
	l := liveService(t, func(d *trading.Deps) {
		d.Users = us
		d.Caps = func() domain.LiveCaps { return instance }
	})

	return l, us
}

func TestKillSwitch(t *testing.T) {
	ctx := context.Background()

	t.Run("on: what rests is cancelled and live orders are refused; off: they go again (D220, D221)", func(t *testing.T) {
		l, us := safeService(t, domain.LiveCaps{})
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveResting}

		o, err := l.svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"))
		require.NoError(t, err)
		require.Equal(t, domain.OrderOpen, o.Status)

		l.exchange.cancel = ports.LiveCancel{Canceled: true}
		l.exchange.states = []ports.LiveOrderState{{State: ports.LiveDone}}

		ks, err := l.svc.SetKillSwitch(ctx, owner, true)
		require.NoError(t, err)
		assert.Equal(t, trading.KillSwitch{On: true, Since: at, Cancelled: 1}, ks)
		assert.Equal(t, []string{"0xabc"}, l.exchange.cancelled)

		got, err := l.ledger.Order(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderCanceled, got.Status)
		assert.Contains(t, got.Reason, "cancelled: the kill switch is on")

		refused, err := l.svc.Place(ctx, liveBuy("10"))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderRefused, refused.Status)
		assert.Equal(t, "your kill switch is on: live trading is stopped until you resume it", refused.Reason)
		assert.Equal(t, 1, l.exchange.sentCount(), "nothing more sent")

		paper := rest(domain.OrderGTC, domain.SideBuy, "0.40", "10")
		paper.WorkflowID = "wf-paper"
		onPaper, err := l.svc.Place(ctx, paper)
		require.NoError(t, err)
		assert.NotEqual(t, domain.OrderRefused, onPaper.Status, "paper trades on")

		_, err = l.svc.SetKillSwitch(ctx, owner, false)
		require.NoError(t, err)

		u, _ := us.ByID(ctx, "alice")
		assert.False(t, u.LiveStopped())

		l.exchange.placed = ports.LivePlacement{ID: "0xdef", State: ports.LiveDone}
		again, err := l.svc.Place(ctx, liveBuy("10"))
		require.NoError(t, err)
		assert.NotEqual(t, domain.OrderRefused, again.Status)
		assert.Equal(t, 2, l.exchange.sentCount())
	})

	t.Run("on again, it keeps when it went on, and only its user's orders go", func(t *testing.T) {
		l, us := safeService(t, domain.LiveCaps{})
		us.set(domain.User{ID: "alice", LiveStoppedAt: at.Add(-time.Hour)})

		theirs := domain.PlacedOrder{ID: "o-bob", ClobID: "0xbob", WalletID: "w-9", Status: domain.OrderOpen, OrderRequest: domain.OrderRequest{
			WorkflowID: "wf-bob", OwnerID: "bob", Mode: domain.ModeLive, Side: domain.SideBuy,
			TokenID: "t-yes", Type: domain.OrderGTC, Price: d("0.4"), Shares: d("10"),
		}}
		require.NoError(t, l.ledger.Record(ctx, theirs, nil))

		ks, err := l.svc.StopLive(ctx, "alice", "cancelled: an admin disabled the account")
		require.NoError(t, err)
		assert.Equal(t, at.Add(-time.Hour), ks.Since)
		assert.Zero(t, ks.Cancelled)
		assert.Empty(t, l.exchange.cancelled)
	})

	t.Run("what comes back resting after it went on is cancelled by the sweep", func(t *testing.T) {
		l, us := safeService(t, domain.LiveCaps{})
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveResting}

		o, err := l.svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"))
		require.NoError(t, err)

		// As if it was on its way while the switch went on.
		us.set(domain.User{ID: "alice", LiveStoppedAt: at})
		l.exchange.cancel = ports.LiveCancel{Canceled: true}
		l.exchange.states = []ports.LiveOrderState{{State: ports.LiveResting}}

		watched(t, l, func() bool {
			got, _ := l.ledger.Order(ctx, o.ID)
			return got.Status == domain.OrderCanceled
		})
	})

	t.Run("live trading off: the sweep cancels every resting live order (D226)", func(t *testing.T) {
		l, _ := safeService(t, domain.LiveCaps{})
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveResting}

		o, err := l.svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"))
		require.NoError(t, err)

		*l.on = false
		l.exchange.cancel = ports.LiveCancel{Canceled: true}
		l.exchange.states = []ports.LiveOrderState{{State: ports.LiveResting}}

		watched(t, l, func() bool {
			got, _ := l.ledger.Order(ctx, o.ID)
			return got.Status == domain.OrderCanceled && slices.Contains(l.exchange.cancelledIDs(), "0xabc")
		})

		got, err := l.ledger.Order(ctx, o.ID)
		require.NoError(t, err)
		assert.Contains(t, got.Reason, "cancelled: an admin turned live trading off")
	})

	t.Run("live trading off: all at once, as it goes off", func(t *testing.T) {
		l, _ := safeService(t, domain.LiveCaps{})
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveResting}

		_, err := l.svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"))
		require.NoError(t, err)

		l.exchange.cancel = ports.LiveCancel{Canceled: true}
		l.exchange.states = []ports.LiveOrderState{{State: ports.LiveDone}}

		left, err := l.svc.CancelAllLive(ctx)
		require.NoError(t, err)
		assert.Zero(t, left)
		assert.Equal(t, []string{"0xabc"}, l.exchange.cancelled)
	})
}

// watched runs WatchLive until done says so.
func watched(t *testing.T, l live, done func() bool) {
	t.Helper()

	ctx, stop := context.WithCancel(context.Background())
	finished := make(chan struct{})

	go func() {
		defer close(finished)
		l.svc.WatchLive(ctx, time.Millisecond)
	}()

	assert.Eventually(t, done, time.Second, time.Millisecond)

	stop()
	<-finished
}

func TestAdminCaps(t *testing.T) {
	ctx := context.Background()

	t.Run("an order over the cap is refused before it is sent (D223)", func(t *testing.T) {
		l, _ := safeService(t, domain.LiveCaps{PerOrder: dollars(100), PerDay: dollars(1000)})

		o, err := l.svc.Place(ctx, liveBuy("300"))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderRefused, o.Status)
		assert.Equal(t, "$150.00 is over the $100 a live order may spend: an admin's cap", o.Reason)
		assert.Zero(t, l.exchange.sentCount())
	})

	t.Run("a user's own caps go over the instance's; 0 is none (D222)", func(t *testing.T) {
		l, us := safeService(t, domain.LiveCaps{PerOrder: dollars(100), PerDay: dollars(1000)})
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveDone}

		us.set(domain.User{ID: "alice", LiveCaps: domain.LiveCaps{PerOrder: dollars(200)}})
		o, err := l.svc.Place(ctx, liveBuy("300"))
		require.NoError(t, err)
		assert.NotEqual(t, domain.OrderRefused, o.Status)

		us.set(domain.User{ID: "alice", LiveCaps: domain.LiveCaps{PerOrder: dollars(0), PerDay: dollars(0)}})
		o, err = l.svc.Place(ctx, liveBuy("4000"))
		require.NoError(t, err)
		assert.NotEqual(t, domain.OrderRefused, o.Status, "$2,000 with no cap")
		assert.Equal(t, 2, l.exchange.sentCount())
	})

	t.Run("the day counts what was spent in 24 hours and what rests (D223)", func(t *testing.T) {
		l, _ := safeService(t, domain.LiveCaps{PerDay: dollars(1000)})
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveDone}

		bought := func(id domain.OrderID, ago time.Duration, status domain.OrderStatus, shares string, filled bool) {
			o := domain.PlacedOrder{ID: id, WalletID: "w-1", Status: status, CreatedAt: at.Add(-ago), OrderRequest: domain.OrderRequest{
				WorkflowID: "wf-old", OwnerID: "alice", Mode: domain.ModeLive, Side: domain.SideBuy,
				TokenID: "t-yes", Type: domain.OrderGTC, Price: d("0.5"), Shares: d(shares),
			}}
			if filled {
				o.Fills = []domain.OrderFill{{Price: d("0.5"), Shares: d(shares), Dollars: d(shares).Mul(d("0.5"))}}
			}

			require.NoError(t, l.ledger.Record(ctx, o, nil))
		}

		bought("o-1", time.Hour, domain.OrderFilled, "1200", true)     // $600 spent
		bought("o-2", 2*time.Hour, domain.OrderOpen, "600", false)     // $300 held
		bought("o-3", 25*time.Hour, domain.OrderFilled, "2000", true)  // too long ago
		bought("o-4", 30*time.Hour, domain.OrderPending, "100", false) // $50 that may still go

		refused, err := l.svc.Place(ctx, liveBuy("300"))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderRefused, refused.Status)
		assert.Equal(t, "$150.00 would go over the $1000 a day your live buys may spend and hold, an admin's cap: "+
			"$950.00 spent or held in the last 24 hours, $50.00 left", refused.Reason)

		fits, err := l.svc.Place(ctx, liveBuy("100"))
		require.NoError(t, err)
		assert.NotEqual(t, domain.OrderRefused, fits.Status, "$50, to the cap")

		sell := liveBuy("10")
		sell.Side = domain.SideSell
		l.ledger.holdings[liveKey] = domain.Holding{WorkflowID: "wf", Mode: domain.ModeLive, Trader: alice, TokenID: "t-yes", Shares: d("10")}
		sold, err := l.svc.Place(ctx, sell)
		require.NoError(t, err)
		assert.NotEqual(t, domain.OrderRefused, sold.Status, "a sell lowers what is at risk")
	})

	t.Run("two at once cannot both pass the day cap", func(t *testing.T) {
		l, _ := safeService(t, domain.LiveCaps{PerDay: dollars(1000)})
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveDone}
		l.repo.m["wf-2"] = domain.WorkflowTrading{WorkflowID: "wf-2", Mode: domain.ModeLive, WalletID: "w-1"}

		gate := make(chan struct{})
		l.exchange.gate = gate

		first := make(chan domain.PlacedOrder)

		go func() {
			o, err := l.svc.Place(ctx, liveBuy("1200"))
			assert.NoError(t, err)
			first <- o
		}()

		require.Eventually(t, func() bool { return l.exchange.sentCount() == 1 }, time.Second, time.Millisecond)

		other := liveBuy("1200")
		other.WorkflowID = "wf-2"
		second, err := l.svc.Place(ctx, other)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderRefused, second.Status)
		assert.Contains(t, second.Reason, "$600.00 spent or held in the last 24 hours, $400.00 left")

		close(gate)
		assert.Equal(t, domain.OrderUnfilled, (<-first).Status)

		third, err := l.svc.Place(ctx, other)
		require.NoError(t, err)
		assert.NotEqual(t, domain.OrderRefused, third.Status, "the first spent nothing in the end")
	})
}
