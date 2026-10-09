package nodes_test

import (
	"testing"

	"github.com/lein3000zzz/polythought-go/internal/core/nodes"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

func TestRegister(t *testing.T) {
	deps := nodes.Deps{
		Markets:    portsmocks.NewMockMarketReader(t),
		OrderBooks: portsmocks.NewMockOrderBookReader(t),
		Logger:     logging.Noop(),
	}

	tests := []struct {
		name        string
		typ         string
		wantTrigger bool
	}{
		{name: "manual trigger", typ: "trigger.manual", wantTrigger: true},
		{name: "schedule trigger", typ: "trigger.schedule", wantTrigger: true},
		{name: "markets", typ: "polymarket.markets"},
		{name: "order books", typ: "polymarket.orderbook"},
		{name: "if", typ: "logic.if"},
		{name: "log", typ: "output.log"},
		{name: "positions", typ: "polymarket.positions"},
		{name: "orders", typ: "polymarket.orders"},
		{name: "balance", typ: "polymarket.balance"},
		{name: "search", typ: "polymarket.search"},
		{name: "price change", typ: "polymarket.priceChange"},
		{name: "trades", typ: "polymarket.trades"},
		{name: "filter", typ: "logic.filter"},
		{name: "only new", typ: "logic.dedupe"},
		{name: "top items", typ: "transform.top"},
		{name: "keep fields", typ: "transform.pick"},
		{name: "webhook", typ: "output.webhook"},
	}

	registry := engine.NewRegistry()
	require.NoError(t, nodes.Register(registry, deps))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, trigger, ok := registry.Type(tt.typ)

			require.True(t, ok)
			assert.Equal(t, tt.wantTrigger, trigger)
		})
	}

	t.Run("registering twice fails", func(t *testing.T) {
		assert.Error(t, nodes.Register(registry, deps))
	})
}
