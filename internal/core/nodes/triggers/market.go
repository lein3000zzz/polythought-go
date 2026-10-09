package triggers

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const MarketType = "trigger.market"

// Kinds of market update the trigger can listen for.
const (
	KindTrade      = "trade"
	KindBook       = "book" // snapshots and level changes
	KindQuote      = "quote"
	KindTick       = "tick"
	KindResolution = "resolution"
)

var kinds = []string{KindTrade, KindBook, KindQuote, KindTick, KindResolution}

// maxBatchItems bounds one execution's input; the newest updates are kept.
const maxBatchItems = 500

var _ engine.Trigger = (*Market)(nil)

// Market fires on live market activity. Updates are gathered over each batch window and
// start one execution with all of them as items, so a busy market cannot start one
// execution per tick.
type Market struct {
	feed     ports.MarketFeed
	minBatch time.Duration
}

func NewMarket(feed ports.MarketFeed, minBatch time.Duration) *Market {
	return &Market{feed: feed, minBatch: minBatch}
}

func (*Market) Type() engine.NodeType {
	return engine.NodeType{
		Name:        MarketType,
		Label:       "Market activity",
		Description: "Starts the workflow on live trades, book changes or resolutions of the chosen tokens, batched per window.",
		Outputs:     []string{engine.DefaultPort},
		Params: []engine.ParamSpec{
			{Name: "tokens", Label: "Tokens", Description: "Outcome token IDs to watch.", Kind: engine.ParamTokens, Required: true},
			{Name: "on", Label: "Events", Kind: engine.ParamList, Default: []any{KindTrade}, Options: kinds},
			{Name: "batch", Label: "Batch window", Description: "Updates in one window start one execution.", Kind: engine.ParamDuration, Default: "5s"},
		},
	}
}

func (m *Market) Start(ctx context.Context, tc engine.TriggerContext, emit engine.Emit) error {
	tokens, on, batch, err := m.config(tc.Params)
	if err != nil {
		return err
	}

	sub, err := m.feed.Subscribe(ctx, tokens)
	if err != nil {
		return err
	}
	defer sub.Close()

	ticker := time.NewTicker(batch)
	defer ticker.Stop()

	var pending []engine.Item

	for {
		select {
		case <-ctx.Done():
			return nil

		case u, ok := <-sub.Updates():
			if !ok {
				return nil // the feed is shutting down
			}

			kind, items := toItems(u)
			if !on[kind] {
				continue
			}

			pending = append(pending, items...)
			if over := len(pending) - maxBatchItems; over > 0 {
				pending = pending[over:]
			}

		case <-ticker.C:
			if len(pending) == 0 {
				continue
			}

			_ = emit(ctx, engine.Ports{engine.DefaultPort: pending})
			pending = nil
		}
	}
}

func (m *Market) config(p engine.Params) ([]domain.TokenID, map[string]bool, time.Duration, error) {
	raw, err := p.Strings("tokens")
	if err != nil {
		return nil, nil, 0, err
	}

	if len(raw) == 0 {
		return nil, nil, 0, fmt.Errorf("%w %q: list at least one token", engine.ErrInvalidParam, "tokens")
	}

	tokens := make([]domain.TokenID, len(raw))
	for i, t := range raw {
		tokens[i] = domain.TokenID(t)
	}

	onList, err := p.Strings("on")
	if err != nil {
		return nil, nil, 0, err
	}

	on := make(map[string]bool, len(onList))

	for _, k := range onList {
		if !slices.Contains(kinds, k) {
			return nil, nil, 0, fmt.Errorf("%w %q: %q is not one of %v", engine.ErrInvalidParam, "on", k, kinds)
		}

		on[k] = true
	}

	rawBatch, err := p.String("batch")
	if err != nil {
		return nil, nil, 0, err
	}

	batch, err := time.ParseDuration(rawBatch)
	if err != nil || batch < m.minBatch {
		return nil, nil, 0, fmt.Errorf("%w %q: want a duration of at least %s", engine.ErrInvalidParam, "batch", m.minBatch)
	}

	return tokens, on, batch, nil
}

// toItems turns an update into items, one per changed level for book changes.
func toItems(u domain.MarketUpdate) (string, []engine.Item) {
	item := func(j map[string]any) engine.Item { return engine.Item{JSON: j} }

	switch e := u.(type) {
	case domain.TradeUpdate:
		return KindTrade, []engine.Item{item(map[string]any{
			"kind": KindTrade, "marketId": string(e.MarketID), "tokenId": string(e.TokenID),
			"side": string(e.Side), "price": e.Price, "size": e.Size, "at": e.At, "txHash": e.TxHash,
		})}

	case domain.BookSnapshot:
		j := map[string]any{
			"kind": KindBook, "marketId": string(e.MarketID), "tokenId": string(e.TokenID), "at": e.At,
			"bestBid": nil, "bestAsk": nil, "mid": nil, "spread": nil,
		}

		if bid, ok := e.BestBid(); ok {
			j["bestBid"] = bid.Price
		}

		if ask, ok := e.BestAsk(); ok {
			j["bestAsk"] = ask.Price
		}

		if mid, ok := e.Mid(); ok {
			j["mid"] = mid
		}

		if spread, ok := e.Spread(); ok {
			j["spread"] = spread
		}

		return KindBook, []engine.Item{item(j)}

	case domain.BookChanges:
		items := make([]engine.Item, len(e.Changes))
		for i, c := range e.Changes {
			items[i] = item(map[string]any{
				"kind": KindBook, "marketId": string(e.MarketID), "tokenId": string(c.TokenID), "at": e.At,
				"side": string(c.Side), "price": c.Price, "size": c.Size, "bestBid": c.BestBid, "bestAsk": c.BestAsk,
			})
		}

		return KindBook, items

	case domain.QuoteUpdate:
		return KindQuote, []engine.Item{item(map[string]any{
			"kind": KindQuote, "marketId": string(e.MarketID), "tokenId": string(e.TokenID),
			"bestBid": e.BestBid, "bestAsk": e.BestAsk, "at": e.At,
		})}

	case domain.TickSizeChange:
		return KindTick, []engine.Item{item(map[string]any{
			"kind": KindTick, "marketId": string(e.MarketID), "tokenId": string(e.TokenID), "tickSize": e.TickSize, "at": e.At,
		})}

	case domain.MarketResolution:
		return KindResolution, []engine.Item{item(map[string]any{
			"kind": KindResolution, "marketId": string(e.MarketID), "winningTokenId": string(e.WinningTokenID),
			"winningOutcome": e.WinningOutcome, "at": e.At,
		})}

	default:
		return "", nil
	}
}
