// Package nodes registers the built-in node types.
package nodes

import (
	"errors"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/nodes/account"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/ai"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/codenodes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/credtypes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/logic"
	markets2 "github.com/lein3000zzz/polythought-go/internal/core/nodes/markets"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/output"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/telegram"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/trading"
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
	Traders      ports.TraderFeed       // optional: without it, trigger.walletActivity is not available
	Memory       ports.NodeMemory       // optional: without it, Only new, Throttle and Consensus are not available
	Logger       logging.Logger

	// Messenger, TelegramInbox and Asker are optional: without them the Telegram nodes
	// are not available. Without Reminder, questions get no reminders.
	Messenger     ports.UserMessenger
	TelegramInbox ports.TelegramInbox
	Asker         ports.UserAsker
	Reminder      ports.QuestionReminder

	// Code runs code nodes; optional: without it they are not available.
	Code ports.CodeCaller

	// LLM calls the language model providers with their users' keys; optional: without
	// it, their credential types and the AI agent are not available. Spend counts what
	// the agent spends; optional: without it, nothing is counted.
	LLM   ports.LLM
	Spend ports.AgentMeter

	// Orders places orders, on paper or live; optional: without it, the ledger (Books)
	// and how workflows trade (Accounts), the trading nodes are not available.
	// TradingRules and Portfolios are for Copy sizing.
	Orders   ports.OrderPlacer
	Books    trading.Books
	Accounts trading.Accounts
	// Canceller cancels a workflow's resting orders, for Cancel orders; OrderEvents is
	// what happens to them later, for Order activity. Optional, each.
	Canceller   ports.OrderCanceller
	OrderEvents ports.OrderEvents
	// Batches places a list of orders together, for Place orders (D244); optional.
	Batches      ports.BatchPlacer
	TradingRules ports.TradingRulesReader
	Portfolios   ports.PortfolioReader

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

	var agent error
	if d.LLM != nil {
		for _, t := range credtypes.LLMTypes(d.LLM) {
			if err := r.RegisterCredentialType(t); err != nil {
				return err
			}
		}

		node := ai.NewAgent(d.Credentials, d.LLM, ai.Sources{
			Events: d.Events, Books: d.OrderBooks, History: d.PriceHistory, Trades: d.Trades, Positions: d.Positions,
		})

		if d.Spend != nil {
			node.WithMeter(d.Spend)
		}

		agent = r.RegisterNode(node)
	}

	var market, wallet, dedupe, throttle, consensus error
	if d.Memory != nil {
		dedupe = r.RegisterNode(logic.NewDedupe(d.Memory))
		throttle = r.RegisterNode(logic.NewThrottle(d.Memory))
		consensus = r.RegisterNode(logic.NewConsensus(d.Memory))
	}

	if d.Feed != nil {
		market = r.RegisterTrigger(triggers.NewMarket(d.Feed, minInterval))
	}

	if d.Traders != nil {
		wallet = r.RegisterTrigger(triggers.NewWalletActivity(d.Traders, d.Logger, time.Now, d.Memory))
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

	var trade []error
	if d.Orders != nil && d.Books != nil && d.Accounts != nil {
		trade = append(trade,
			r.RegisterNode(trading.NewCopySize(trading.Sources{
				Books: d.Books, Accounts: d.Accounts, Rules: d.TradingRules, Markets: d.Markets, Positions: d.Positions,
				Portfolios: d.Portfolios,
			})),
			r.RegisterNode(trading.NewRiskGuard(d.Books, d.Accounts, d.TradingRules, d.Markets, time.Now)),
			r.RegisterNode(trading.NewPlaceOrder(d.Orders)),
		)

		if d.Canceller != nil {
			trade = append(trade, r.RegisterNode(trading.NewCancelOrders(d.Canceller)))
		}

		if d.Batches != nil {
			trade = append(trade, r.RegisterNode(trading.NewPlaceOrders(d.Batches)))
		}

		if d.OrderEvents != nil {
			trade = append(trade, r.RegisterTrigger(triggers.NewOrderActivity(d.OrderEvents)))
		}
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
		errors.Join(trade...),
		market,
		wallet,
		dedupe,
		throttle,
		consensus,
		telegramSend,
		telegramTrigger,
		telegramAsk,
		agent,
		r.RegisterTrigger(triggers.Manual{}),
		r.RegisterTrigger(triggers.NewSchedule(minInterval)),
		r.RegisterNode(markets2.NewMarkets(d.Markets)),
		r.RegisterNode(markets2.NewSearch(d.Events)),
		r.RegisterNode(markets2.NewOrderBooks(d.OrderBooks)),
		r.RegisterNode(markets2.NewPriceChange(d.PriceHistory)),
		r.RegisterNode(markets2.NewTrades(d.Trades)),
		r.RegisterNode(logic.If{}),
		r.RegisterNode(logic.Filter{}),
		r.RegisterNode(logic.Switch{}),
		r.RegisterNode(logic.Merge{}),
		r.RegisterNode(logic.Sample{}),
		r.RegisterNode(logic.NewWait(time.Now)),
		r.RegisterNode(transform.Top{}),
		r.RegisterNode(transform.Pick{}),
		r.RegisterNode(transform.SplitOut{}),
		r.RegisterNode(output.NewLog(d.Logger)),
		r.RegisterNode(output.NewWebhook(d.Webhooks, d.Credentials)),
		r.RegisterNode(account.NewPositions(d.Wallets, d.Positions)),
		r.RegisterNode(account.NewOrders(d.Wallets, d.Account)),
		r.RegisterNode(account.NewBalance(d.Wallets, d.Account)),
	)
}
