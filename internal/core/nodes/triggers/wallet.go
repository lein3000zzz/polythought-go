package triggers

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const WalletActivityType = "trigger.walletActivity"

var _ engine.Trigger = (*WalletActivity)(nil)

// The Traders table (D83).
var traderColumns = []engine.ParamSpec{
	{Name: "label", Label: "Label", Description: "Who they are to you.", Kind: engine.ParamString, Required: true},
	{
		Name: "address", Label: "Address", Kind: engine.ParamAddress, Required: true,
		Description: "Their Polymarket wallet: the address in their profile's link.",
	},
	{Name: "on", Label: "On", Description: "Off, their trades are not followed.", Kind: engine.ParamBool, Default: true},
	{
		Name: "scale", Label: "Size ×", Kind: engine.ParamDecimal,
		Description: "Scales what Copy sizing works out for their trades; empty is 1.",
	},
	{
		Name: "maxDollars", Label: "Max $", Kind: engine.ParamDecimal,
		Description: "The most dollars a copy of one of their trades may spend; empty leaves it to the Risk guard.",
	},
	{
		Name: "take", Label: "Take %", Kind: engine.ParamDecimal,
		Description: "The share of their buys Copy sizing copies, each the same way every time: 30 is about 3 in 10. " +
			"Empty copies them all.",
	},
}

var sides = []string{string(domain.SideBuy), string(domain.SideSell)}

// WalletActivity starts a workflow on the trades of the traders it follows, as they
// happen (D83 to D88): an item a trade, its fills summed.
type WalletActivity struct {
	feed ports.TraderFeed
	log  logging.Logger
	now  func() time.Time
}

func NewWalletActivity(feed ports.TraderFeed, log logging.Logger, now func() time.Time) *WalletActivity {
	return &WalletActivity{feed: feed, log: log, now: now}
}

func (*WalletActivity) Type() engine.NodeType {
	return engine.NodeType{
		Name:  WalletActivityType,
		Label: "Wallet activity",
		Description: "Starts the workflow on the trades of the traders you follow as they happen, about two " +
			"seconds after: a trade each item, its fills summed.",
		Outputs: []string{engine.DefaultPort},
		Items:   shape.Own(traderTradeFields()...),
		Params: []engine.ParamSpec{
			{
				Name: "traders", Label: "Traders", Kind: engine.ParamTable, Required: true, Columns: traderColumns,
				Description: "A row a trader: their trades start the workflow while their row is on.",
			},
			{Name: "sides", Label: "Sides", Kind: engine.ParamList, Default: []any{"buy", "sell"}, Options: sides},
			{
				Name: "least", Label: "At least $", Kind: engine.ParamDecimal, Default: "0",
				Description: "Trades worth fewer dollars are skipped.",
			},
			{
				Name: "catchUp", Label: "Catch up within", Kind: engine.ParamDuration, Default: "2m",
				Description: "After the server restarts or the trade stream drops, trades missed meanwhile still " +
					"start the workflow if they are younger than this, up to an hour; older ones are skipped.",
			},
		},
	}
}

func traderTradeFields() []engine.Field {
	opt := func(f engine.Field) engine.Field { return shape.Nullable(f) }

	return []engine.Field{
		{
			Name: "trader", Kind: engine.FieldObject, Description: "Whose trade it is: their row of Traders.",
			Fields: []engine.Field{
				shape.Text("label", "Their label."),
				shape.Text("address", "Their wallet."),
				opt(shape.Text("name", "Their name on Polymarket.")),
				opt(shape.Decimal("scale", "Size ×: what Copy sizing works out is scaled by it.")),
				opt(shape.Decimal("maxDollars", "Max $: the most a copy may spend.")),
				opt(shape.Decimal("take", "Take %: the share of their buys copied.")),
			},
		},
		shape.OneOf("side", "Whether they bought or sold.", sides...),
		opt(shape.OneOf("role", "taker: their order met the book; maker: a resting order of theirs filled. "+
			"Null when the source did not say.", string(domain.RoleTaker), string(domain.RoleMaker))),
		shape.Text("tokenId", "The outcome's token ID."),
		opt(shape.Text("marketId", "The market's condition ID.")),
		opt(shape.Text("question", "The market's question.")),
		opt(shape.Text("outcome", "The outcome, like Yes.")),
		opt(shape.Text("slug", "The market's slug.")),
		opt(shape.Text("eventSlug", "Its event's slug.")),
		shape.Decimal("shares", "Shares traded, all its fills together."),
		shape.Decimal("price", "The average price per share."),
		shape.Decimal("dollars", "Dollars traded: shares times their prices."),
		shape.Number("fills", "How many fills it took."),
		shape.Text("txHash", "The transaction."),
		shape.Time("at", "When it happened."),
	}
}

type followed struct {
	label         string
	address       domain.WalletAddress
	scale, maxUSD *decimal.Decimal
	take          *decimal.Decimal
}

type walletConfig struct {
	traders map[string]followed // by address, lower case
	sides   map[domain.Side]bool
	least   decimal.Decimal
	catchUp time.Duration
}

// maxCatchUp is the furthest back a catch-up looks, as far as the trader feed does.
const maxCatchUp = time.Hour

func (w *WalletActivity) Start(ctx context.Context, tc engine.TriggerContext, emit engine.Emit) error {
	cfg, err := readWalletConfig(tc.Params)
	if err != nil {
		return err
	}

	if len(cfg.traders) == 0 {
		<-ctx.Done() // every trader off: nothing to follow until it is edited
		return nil
	}

	wallets := make([]domain.WalletAddress, 0, len(cfg.traders))
	for _, t := range cfg.traders {
		wallets = append(wallets, t.address)
	}

	slices.Sort(wallets)

	// A first start follows from now on; resumed, it catches up on what it missed (D86).
	started := w.now()
	notBefore, since := started.Add(-2*time.Second), time.Time{} // trade times are to the second
	if tc.Resumed {
		since = started.Add(-cfg.catchUp)
		notBefore = since
	}

	sub, err := w.feed.Follow(ctx, wallets, since)
	if err != nil {
		return err
	}
	defer sub.Close()

	for {
		select {
		case <-ctx.Done():
			return nil

		case batch, ok := <-sub.Trades():
			if !ok {
				return nil // the feed is shutting down
			}

			if items := w.items(ctx, tc, cfg, batch, notBefore); len(items) > 0 {
				_ = emit(ctx, engine.Ports{engine.DefaultPort: items})
			}
		}
	}
}

func (w *WalletActivity) items(ctx context.Context, tc engine.TriggerContext, cfg walletConfig,
	batch []domain.TraderTrade, notBefore time.Time,
) []engine.Item {
	now := w.now()

	var items []engine.Item

	for _, t := range batch {
		tr, ok := cfg.traders[strings.ToLower(string(t.Wallet))]

		switch {
		case !ok, !cfg.sides[t.Side], t.Dollars.LessThan(cfg.least):
			continue
		case t.At.Before(notBefore):
			continue // from before it started
		case now.Sub(t.At) > cfg.catchUp:
			// Never copied late (D86).
			w.log.Info(ctx, "Trade skipped: older than Catch up within", "workflow_id", tc.WorkflowID,
				"node_id", tc.NodeID, "trader", tr.label, "tx", t.TxHash, "age", now.Sub(t.At).Round(time.Second))

			continue
		}

		items = append(items, engine.Item{JSON: tradeItem(tr, t)})
	}

	return items
}

func tradeItem(tr followed, t domain.TraderTrade) map[string]any {
	orNil := func(s string) any {
		if s == "" {
			return nil
		}

		return s
	}

	decOrNil := func(d *decimal.Decimal) any {
		if d == nil {
			return nil
		}

		return *d
	}

	return map[string]any{
		"trader": map[string]any{
			"label": tr.label, "address": string(tr.address), "name": orNil(t.Name),
			"scale": decOrNil(tr.scale), "maxDollars": decOrNil(tr.maxUSD), "take": decOrNil(tr.take),
		},
		"side": string(t.Side), "role": orNil(string(t.Role)),
		"tokenId": string(t.TokenID), "marketId": orNil(string(t.MarketID)), "question": orNil(t.Title),
		"outcome": orNil(t.Outcome), "slug": orNil(t.Slug), "eventSlug": orNil(t.EventSlug),
		"shares": t.Shares, "price": t.Price, "dollars": t.Dollars, "fills": t.Fills,
		"txHash": t.TxHash, "at": t.At,
	}
}

func readWalletConfig(p engine.Params) (walletConfig, error) {
	cfg := walletConfig{traders: map[string]followed{}, sides: map[domain.Side]bool{}}

	rows, err := p.Table("traders", traderColumns)
	if err != nil {
		return cfg, err
	}

	for i, row := range rows {
		if on, err := row.Bool("on"); err != nil || !on {
			continue
		}

		label, err := row.String("label")
		if err != nil {
			return cfg, fmt.Errorf("%w %q: row %d: give a label", engine.ErrInvalidParam, "traders", i+1)
		}

		raw, err := row.String("address")
		if err != nil {
			return cfg, fmt.Errorf("%w %q: row %d: give an address", engine.ErrInvalidParam, "traders", i+1)
		}

		address, err := domain.ParseAddress(raw)
		if err != nil {
			return cfg, fmt.Errorf("%w %q: row %d: %q is not an address", engine.ErrInvalidParam, "traders", i+1, raw)
		}

		key := strings.ToLower(string(address))
		if _, twice := cfg.traders[key]; twice {
			continue // the first row of a wallet is the one that counts
		}

		t := followed{label: label, address: address}
		if t.scale, err = optionalDecimal(row, "scale"); err != nil {
			return cfg, err
		}

		if t.maxUSD, err = optionalDecimal(row, "maxDollars"); err != nil {
			return cfg, err
		}

		if t.take, err = optionalDecimal(row, "take"); err != nil {
			return cfg, err
		}

		cfg.traders[key] = t
	}

	list, err := p.Strings("sides")
	if err != nil {
		return cfg, err
	}

	for _, s := range list {
		cfg.sides[domain.Side(s)] = true
	}

	if cfg.least, err = p.Decimal("least"); err != nil {
		return cfg, err
	}

	if cfg.catchUp, err = p.Duration("catchUp"); err != nil {
		return cfg, err
	}

	cfg.catchUp = min(cfg.catchUp, maxCatchUp)

	return cfg, nil
}

func optionalDecimal(row engine.Params, name string) (*decimal.Decimal, error) {
	if row[name] == nil {
		return nil, nil
	}

	d, err := row.Decimal(name)
	if err != nil {
		return nil, err
	}

	return &d, nil
}
