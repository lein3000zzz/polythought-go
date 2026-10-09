// Package nodes registers the built-in node types.
package nodes

import (
	"errors"

	"github.com/lein3000zzz/polythought-go/internal/core/nodes/account"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/logic"
	markets2 "github.com/lein3000zzz/polythought-go/internal/core/nodes/markets"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/output"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/transform"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// Deps are the ports and services the built-in nodes need.
type Deps struct {
	Markets      ports.MarketReader
	Events       ports.EventReader
	OrderBooks   ports.OrderBookReader
	PriceHistory ports.PriceHistoryReader
	Trades       ports.TradeReader
	Positions    ports.PositionReader
	Account      ports.AccountReader
	Wallets      ports.WalletDirectory
	Webhooks     ports.WebhookSender
	Feed         ports.MarketFeed // optional: without it, trigger.market is not available
	Logger       logging.Logger
}

func Register(r *engine.Registry, d Deps) error {
	var market error
	if d.Feed != nil {
		market = r.RegisterTrigger(triggers.NewMarket(d.Feed, triggers.DefaultMinInterval))
	}

	return errors.Join(
		market,
		r.RegisterTrigger(triggers.Manual{}),
		r.RegisterTrigger(triggers.NewSchedule(triggers.DefaultMinInterval)),
		r.RegisterNode(markets2.NewMarkets(d.Markets)),
		r.RegisterNode(markets2.NewSearch(d.Events)),
		r.RegisterNode(markets2.NewOrderBooks(d.OrderBooks)),
		r.RegisterNode(markets2.NewPriceChange(d.PriceHistory)),
		r.RegisterNode(markets2.NewTrades(d.Trades)),
		r.RegisterNode(logic.If{}),
		r.RegisterNode(logic.Filter{}),
		r.RegisterNode(logic.NewDedupe()),
		r.RegisterNode(transform.Top{}),
		r.RegisterNode(transform.Pick{}),
		r.RegisterNode(output.NewLog(d.Logger)),
		r.RegisterNode(output.NewWebhook(d.Webhooks)),
		r.RegisterNode(account.NewPositions(d.Wallets, d.Positions)),
		r.RegisterNode(account.NewOrders(d.Wallets, d.Account)),
		r.RegisterNode(account.NewBalance(d.Wallets, d.Account)),
	)
}
