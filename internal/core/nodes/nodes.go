// Package nodes registers the built-in node types.
package nodes

import (
	"errors"

	"github.com/lein3000zzz/polythought-go/internal/core/nodes/account"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/logic"
	markets2 "github.com/lein3000zzz/polythought-go/internal/core/nodes/markets"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/output"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// Deps are the ports and services the built-in nodes need.
type Deps struct {
	Markets    ports.MarketReader
	OrderBooks ports.OrderBookReader
	Positions  ports.PositionReader
	Account    ports.AccountReader
	Wallets    ports.WalletDirectory
	Feed       ports.MarketFeed // optional: without it, trigger.market is not available
	Logger     logging.Logger
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
		r.RegisterNode(markets2.NewOrderBooks(d.OrderBooks)),
		r.RegisterNode(logic.If{}),
		r.RegisterNode(output.NewLog(d.Logger)),
		r.RegisterNode(account.NewPositions(d.Wallets, d.Positions)),
		r.RegisterNode(account.NewOrders(d.Wallets, d.Account)),
		r.RegisterNode(account.NewBalance(d.Wallets, d.Account)),
	)
}
