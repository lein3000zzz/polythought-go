package nodes_test

import (
	"context"
	"testing"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/nodes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/trading"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

func TestRegister(t *testing.T) {
	deps := nodes.Deps{
		Markets:    portsmocks.NewMockMarketReader(t),
		OrderBooks: portsmocks.NewMockOrderBookReader(t),
		Memory:     memoryStub{},
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
		{name: "wait", typ: "logic.wait"},
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

	t.Run("every type says what it emits", func(t *testing.T) {
		all := engine.NewRegistry()
		require.NoError(t, nodes.Register(all, nodes.Deps{
			Logger: logging.Noop(), Feed: portsmocks.NewMockMarketFeed(t), Traders: tradersStub{},
			Orders: ordersStub{}, Books: booksStub{}, Accounts: accountsStub{},
			Messenger: telegramStub{}, TelegramInbox: telegramStub{}, Asker: telegramStub{}, Code: codeStub{},
			Memory: memoryStub{},
		}))

		types := all.Types()
		assert.Len(t, types, 36)

		for _, typ := range types {
			if typ.Name == "code.transform" || typ.Name == "code.aggregate" {
				assert.Nil(t, typ.Items, "%s: what code makes, nobody knows (D43)", typ.Name)
				continue
			}

			assert.NotNil(t, typ.Items, typ.Name)
		}
	})

	t.Run("registering twice fails", func(t *testing.T) {
		assert.Error(t, nodes.Register(registry, deps))
	})
}

// memoryStub stands in for node memory: only the node types are read.
type memoryStub struct{ ports.NodeMemory }

// telegramStub stands in for the bot: only the node types are read.
type telegramStub struct{}

func (telegramStub) MessageUser(context.Context, domain.UserID, ports.UserMessage) error { return nil }

func (telegramStub) AskUser(context.Context, domain.UserID, ports.Question) (int, error) {
	return 0, nil
}

func (telegramStub) CloseQuestion(context.Context, domain.UserID, int, string) error { return nil }

func (telegramStub) Subscribe(domain.UserID, func(string) bool) (<-chan ports.TelegramInboxMessage, func()) {
	return nil, func() {}
}

type codeStub struct{}

func (codeStub) Call(context.Context, ports.CodeCall) (ports.CodeResult, error) {
	return ports.CodeResult{}, nil
}

type tradersStub struct{}

func (tradersStub) Follow(context.Context, []domain.WalletAddress, time.Time) (ports.TraderSubscription, error) {
	return nil, nil //nolint:nilnil // never called: only the types are read
}

type (
	ordersStub   struct{}
	booksStub    struct{ ports.Ledger }     // never called
	accountsStub struct{ trading.Accounts } // never called
)

func (ordersStub) Place(context.Context, domain.OrderRequest) (domain.PlacedOrder, error) {
	return domain.PlacedOrder{}, nil
}
