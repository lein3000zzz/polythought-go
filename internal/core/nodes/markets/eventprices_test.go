package markets

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

// takingOrders is market with its orders open.
func takingOrders(m domain.Market) domain.Market {
	m.AcceptingOrders = true
	return m
}

// An event's Yes prices added up (D437): the buckets still taking orders alone, each book
// asked once, however many items name the event.
func TestEventPricesNode(t *testing.T) {
	ctx := context.Background()

	resolved := market("m-old", "t-old-yes", "t-old-no")
	resolved.Status = domain.MarketResolved

	nyc := domain.Event{
		ID: "1234", Slug: "highest-temperature-in-nyc", Title: "Highest temperature in NYC?",
		Markets: []domain.Market{
			takingOrders(market("m-70", "t-70-yes", "t-70-no")),
			takingOrders(market("m-72", "t-72-yes", "t-72-no")),
			takingOrders(market("m-74", "t-74-yes", "t-74-no")),
			resolved,
		},
	}

	events := portsmocks.NewMockEventReader(t)
	events.EXPECT().GetEventBySlug(mock.Anything, "highest-temperature-in-nyc").Return(nyc, nil).Once()
	events.EXPECT().GetEvent(mock.Anything, domain.EventID("1234")).Return(nyc, nil).Once()
	events.EXPECT().GetEventBySlug(mock.Anything, "no-such-event").Return(domain.Event{}, domain.ErrNotFound).Once()

	books := portsmocks.NewMockOrderBookReader(t)
	books.EXPECT().OrderBooks(mock.Anything, mock.Anything).Return([]domain.OrderBook{
		book("t-70-yes", [][2]string{{"0.30", "10"}}, [][2]string{{"0.33", "10"}}),
		book("t-72-yes", [][2]string{{"0.45", "10"}}, [][2]string{{"0.48", "10"}}),
		book("t-74-yes", [][2]string{{"0.20", "10"}}, nil),
	}, nil).Twice()

	node := NewEventPrices(events, books)

	// run runs the node on items naming one event, as its setting says.
	run := func(event string, items ...map[string]any) engine.Ports {
		t.Helper()

		in := engine.Ports{}
		for _, it := range items {
			in.Add(engine.DefaultPort, engine.Item{JSON: it})
		}

		out, err := node.Run(ctx, runContext(node.Type(), map[string]any{"event": event}), in)
		require.NoError(t, err)

		return out
	}

	out := run("https://polymarket.com/event/highest-temperature-in-nyc/the-72-bucket", map[string]any{"kept": true}, map[string]any{})

	got := out[engine.DefaultPort]
	require.Len(t, got, 2, "each item, the event read once")
	require.NoError(t, shape.Check(node.Type(), engine.Ports{engine.DefaultPort: got}))

	p := got[0].JSON["prices"].(map[string]any)
	assert.Equal(t, "Highest temperature in NYC?", p["event"])
	assert.Equal(t, "0.81", p["asks"].(interface{ String() string }).String(), "0.33 + 0.48; the 74 bucket has no ask")
	assert.Equal(t, "0.95", p["bids"].(interface{ String() string }).String())
	assert.Equal(t, "0.78", p["mids"].(interface{ String() string }).String(), "0.315 + 0.465, the two with both sides")
	assert.Equal(t, 3, p["outcomes"])
	assert.Equal(t, 1, p["leftOut"], "the resolved bucket")
	assert.Equal(t, 1, p["noAsk"])
	assert.Equal(t, 0, p["noBid"])
	require.Len(t, p["each"], 3)
	assert.Nil(t, p["each"].([]any)[2].(map[string]any)["ask"])
	assert.Equal(t, true, got[0].JSON["kept"], "the item passes on")

	out = run("1234", map[string]any{})
	require.Len(t, out[engine.DefaultPort], 1, "by its ID")

	out = run("no-such-event", map[string]any{})
	require.Len(t, out[engine.ErrorPort], 1)
	assert.ErrorContains(t, out[engine.ErrorPort][0].Err, `no event "no-such-event"`)
}

func TestEventPricesNodeRefusesOtherLinks(t *testing.T) {
	node := NewEventPrices(portsmocks.NewMockEventReader(t), portsmocks.NewMockOrderBookReader(t))

	in := engine.Ports{engine.DefaultPort: {{JSON: map[string]any{}}}}
	out, err := node.Run(context.Background(), runContext(node.Type(), map[string]any{"event": "https://polymarket.com/markets/crypto"}), in)
	require.NoError(t, err)
	require.Len(t, out[engine.ErrorPort], 1)
	assert.ErrorContains(t, out[engine.ErrorPort][0].Err, "is no event's link")
}
