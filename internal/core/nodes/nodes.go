// Package nodes registers the built-in node types.
package nodes

import (
	"errors"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/nodes/account"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/codenodes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/credtypes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/logic"
	markets2 "github.com/lein3000zzz/polythought-go/internal/core/nodes/markets"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/output"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/telegram"
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
	Credentials  ports.CredentialReader // optional: without it, nodes cannot use credentials
	Feed         ports.MarketFeed       // optional: without it, trigger.market is not available
	Logger       logging.Logger

	// Messenger, TelegramInbox and Asker are optional: without them the Telegram nodes
	// are not available. Without Reminder, questions get no reminders.
	Messenger     ports.UserMessenger
	TelegramInbox ports.TelegramInbox
	Asker         ports.UserAsker
	Reminder      ports.QuestionReminder

	// Code runs code nodes; optional: without it they are not available.
	Code ports.CodeCaller

	// MinInterval is the shortest interval schedules and market triggers may use. Zero
	// means triggers.DefaultMinInterval.
	MinInterval time.Duration
}

func Register(r *engine.Registry, d Deps) error {
	minInterval := d.MinInterval
	if minInterval <= 0 {
		minInterval = triggers.DefaultMinInterval
	}

	// Credential types first: nodes asking for one are checked against them.
	if err := r.RegisterCredentialType(credtypes.HTTPHeaderType()); err != nil {
		return err
	}

	var market error
	if d.Feed != nil {
		market = r.RegisterTrigger(triggers.NewMarket(d.Feed, minInterval))
	}

	var telegramSend, telegramTrigger, telegramAsk error
	if d.Messenger != nil {
		telegramSend = r.RegisterNode(telegram.NewSend(d.Messenger))
	}

	if d.TelegramInbox != nil {
		telegramTrigger = r.RegisterTrigger(telegram.NewTrigger(d.TelegramInbox))
	}

	if d.Asker != nil {
		telegramAsk = r.RegisterNode(telegram.NewAsk(d.Asker, d.Reminder, time.Now))
	}

	var code []error
	if d.Code != nil {
		for _, n := range []*codenodes.Node{
			codenodes.NewTransform(d.Code, d.Logger), codenodes.NewFilter(d.Code, d.Logger),
			codenodes.NewRoute(d.Code, d.Logger), codenodes.NewAggregate(d.Code, d.Logger),
		} {
			code = append(code, r.RegisterNode(n))
		}
	}

	return errors.Join(
		errors.Join(code...),
		market,
		telegramSend,
		telegramTrigger,
		telegramAsk,
		r.RegisterTrigger(triggers.Manual{}),
		r.RegisterTrigger(triggers.NewSchedule(minInterval)),
		r.RegisterNode(markets2.NewMarkets(d.Markets)),
		r.RegisterNode(markets2.NewSearch(d.Events)),
		r.RegisterNode(markets2.NewOrderBooks(d.OrderBooks)),
		r.RegisterNode(markets2.NewPriceChange(d.PriceHistory)),
		r.RegisterNode(markets2.NewTrades(d.Trades)),
		r.RegisterNode(logic.If{}),
		r.RegisterNode(logic.Filter{}),
		r.RegisterNode(logic.NewDedupe()),
		r.RegisterNode(logic.NewWait(time.Now)),
		r.RegisterNode(transform.Top{}),
		r.RegisterNode(transform.Pick{}),
		r.RegisterNode(output.NewLog(d.Logger)),
		r.RegisterNode(output.NewWebhook(d.Webhooks, d.Credentials)),
		r.RegisterNode(account.NewPositions(d.Wallets, d.Positions)),
		r.RegisterNode(account.NewOrders(d.Wallets, d.Account)),
		r.RegisterNode(account.NewBalance(d.Wallets, d.Account)),
	)
}
