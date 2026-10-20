package notify_test

import (
	"context"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/notify"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

// rain is the market the orders are in.
type rain struct{}

func (rain) ListMarkets(context.Context, ports.MarketFilter) (ports.Page[domain.Market], error) {
	return ports.Page[domain.Market]{}, nil
}

func (rain) GetMarket(_ context.Context, id domain.MarketID) (domain.Market, error) {
	if id != "0xm" {
		return domain.Market{}, domain.ErrNotFound
	}

	return domain.Market{ID: id, Question: "Will it rain?", Outcomes: []domain.Outcome{{TokenID: "t-yes", Label: "Yes"}}}, nil
}

func (rain) GetMarketBySlug(context.Context, string) (domain.Market, error) {
	return domain.Market{}, domain.ErrNotFound
}

func orderService(t *testing.T, publicURL string) (*notify.Service, deps) {
	t.Helper()

	d := deps{
		messenger: &messenger{}, mail: &mailbox{on: true}, prefs: &prefs{changed: map[string]bool{}},
		workflows: portsmocks.NewMockWorkflowRepository(t), clock: &clock{now: now},
	}
	d.workflows.EXPECT().Get(mock.Anything, domain.UserID("alice"), domain.WorkflowID("wf-1")).Return(spreads, nil).Maybe()

	s := notify.New(d.messenger, d.prefs, d.workflows, publicURL, logging.Noop(),
		notify.WithClock(d.clock.Now), notify.WithMail(d.mail), notify.WithMarkets(rain{}), notify.WithTick(5*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go s.Run(ctx)

	return s, d
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func liveOrder(status domain.OrderStatus, reason string) domain.PlacedOrder {
	return domain.PlacedOrder{
		OrderRequest: domain.OrderRequest{
			WorkflowID: "wf-1", OwnerID: "alice", Mode: domain.ModeLive, TokenID: "t-yes", MarketID: "0xm",
			Side: domain.SideBuy, Type: domain.OrderFAK, Price: dec("0.52"), Shares: dec("10"),
		},
		ID: "o-1", Status: status, Reason: reason,
	}
}

var fill = domain.OrderFill{Price: dec("0.52"), Shares: dec("10"), Dollars: dec("5.2"), Fee: dec("0.03")}

func TestOrderNotifications(t *testing.T) {
	ctx := context.Background()

	t.Run("a live fill: what, where, at what, with a link (D229, D230)", func(t *testing.T) {
		s, d := orderService(t, "https://poly.example")

		o := liveOrder(domain.OrderFilled, "")
		o.TraderLabel = "Alice"
		s.OrderHappened(domain.OrderEvent{Kind: domain.OrderEventFilled, Order: o, Fills: []domain.OrderFill{fill}})
		settle(t, d, 1)

		msg := d.messenger.sent[0]
		assert.Equal(t, "✅ Live: <b>Spreads &lt;tight&gt;</b> bought 10 shares of Yes in “Will it rain?” at 52¢ for $5.20, "+
			"$0.03 in fees. Copying Alice.", msg.HTML)
		assert.Equal(t, [][]ports.TelegramButton{{{Text: "Open the workflow", URL: "https://poly.example/workflows/wf-1"}}}, msg.Buttons)
	})

	t.Run("refused, with why; the market unknown, it is not named", func(t *testing.T) {
		s, d := orderService(t, "")

		o := liveOrder(domain.OrderRefused, "your kill switch is on: live trading is stopped until you resume it")
		o.MarketID = ""
		s.OrderHappened(domain.OrderEvent{Kind: domain.OrderEventRefused, Order: o})
		settle(t, d, 1)

		assert.Equal(t, "⛔ Live: <b>Spreads &lt;tight&gt;</b>'s buy of 10 shares at 52¢ was refused: "+
			"your kill switch is on: live trading is stopped until you resume it.", d.messenger.sent[0].HTML)
	})

	t.Run("off to start with: unfilled, resting, ended and every paper kind (D230, D232)", func(t *testing.T) {
		s, d := orderService(t, "")

		paper := liveOrder(domain.OrderFilled, "")
		paper.Mode = domain.ModePaper

		s.OrderHappened(domain.OrderEvent{Kind: domain.OrderEventUnfilled, Order: liveOrder(domain.OrderUnfilled, "the book could not fill it")})
		s.OrderHappened(domain.OrderEvent{Kind: domain.OrderEventRested, Order: liveOrder(domain.OrderOpen, "")})
		s.OrderHappened(domain.OrderEvent{Kind: domain.OrderEventExpired, Order: liveOrder(domain.OrderExpired, "")})
		s.OrderHappened(domain.OrderEvent{Kind: domain.OrderEventFilled, Order: paper, Fills: []domain.OrderFill{fill}})
		settle(t, d, 0)

		_, err := s.SetPref(ctx, alice, "paper_order_filled", true)
		require.NoError(t, err)
		_, err = s.SetPref(ctx, alice, "live_order_ended", true)
		require.NoError(t, err)

		s.OrderHappened(domain.OrderEvent{Kind: domain.OrderEventFilled, Order: paper, Fills: []domain.OrderFill{fill}})
		s.OrderHappened(domain.OrderEvent{Kind: domain.OrderEventCanceled, Order: liveOrder(domain.OrderCanceled, "cancelled: the kill switch is on, 0 of 10 shares filled")})
		settle(t, d, 2)

		assert.Contains(t, d.messenger.sent[0].HTML, "✅ Paper: <b>Spreads &lt;tight&gt;</b> bought 10 shares")
		assert.Equal(t, "🚫 Live: <b>Spreads &lt;tight&gt;</b>'s buy of 10 shares of Yes in “Will it rain?” at 52¢ was cancelled: "+
			"cancelled: the kill switch is on, 0 of 10 shares filled.", d.messenger.sent[1].HTML)
	})

	t.Run("20 of a kind an hour, then one summary as the hour ends (D231)", func(t *testing.T) {
		s, d := orderService(t, "")
		d.workflows.EXPECT().Get(mock.Anything, domain.UserID("alice"), domain.WorkflowID("gone")).Return(domain.Workflow{}, domain.ErrNotFound)

		filled := domain.OrderEvent{Kind: domain.OrderEventFilled, Order: liveOrder(domain.OrderFilled, ""), Fills: []domain.OrderFill{fill}}
		for range 23 {
			s.OrderHappened(filled)
		}

		gone := filled
		gone.Order.WorkflowID = "gone"
		s.OrderHappened(gone)

		refused := domain.OrderEvent{Kind: domain.OrderEventRefused, Order: liveOrder(domain.OrderRefused, "not enough shares")}
		s.OrderHappened(refused)
		settle(t, d, 21) // a kind's cap is its own

		d.clock.set(now.Add(61 * time.Minute))
		settle(t, d, 22)
		assert.Equal(t, "🔔 4 more live orders filled in the last hour, past the 20 told one by one: "+
			"Spreads &lt;tight&gt; (3), a deleted workflow (1).", d.messenger.sent[21].HTML)

		s.OrderHappened(filled)
		settle(t, d, 23)
		assert.Contains(t, d.messenger.sent[22].HTML, "✅ Live:", "a new hour")
	})

	t.Run("by mail when chosen; a deleted workflow's links to the order log", func(t *testing.T) {
		s, d := orderService(t, "https://poly.example")
		d.workflows.EXPECT().Get(mock.Anything, domain.UserID("alice"), domain.WorkflowID("gone")).Return(domain.Workflow{}, domain.ErrNotFound)

		_, err := s.SetEmailPref(ctx, alice, "live_order_filled", true)
		require.NoError(t, err)

		o := liveOrder(domain.OrderFilled, "")
		o.WorkflowID = "gone"
		s.OrderHappened(domain.OrderEvent{Kind: domain.OrderEventFilled, Order: o, Fills: []domain.OrderFill{fill}})
		settle(t, d, 1)

		require.Len(t, d.mail.mails(), 1)
		m := d.mail.mails()[0]
		assert.Equal(t, "Live order of a deleted workflow filled", m.Subject)
		assert.Equal(t, "Live: a deleted workflow bought 10 shares of Yes in “Will it rain?” at 52¢ for $5.20, $0.03 in fees.", m.Paragraphs[0])
		assert.Equal(t, "https://poly.example/orders", m.LinkURL)
		assert.Equal(t, "Open live orders", d.messenger.sent[0].Buttons[0][0].Text)
	})

	t.Run("a failed trade has a kind of its own", func(t *testing.T) {
		s, d := orderService(t, "")

		s.OrderHappened(domain.OrderEvent{Kind: domain.OrderEventFailed, Order: liveOrder(domain.OrderFailed, "")})
		settle(t, d, 0)
	})
}
