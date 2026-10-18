package polymarket

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

const resolved = "resolved"

func toMarket(m pm.Market) domain.Market {
	return domain.Market{
		ID:              domain.MarketID(m.ConditionID),
		Slug:            m.Slug,
		Question:        m.Question,
		Description:     m.Description,
		Outcomes:        toOutcomes(m),
		Status:          toMarketStatus(m),
		AcceptingOrders: m.AcceptingOrders,
		NegRisk:         m.NegRisk,
		TickSize:        m.OrderPriceMinTickSize,
		MinOrderSize:    m.OrderMinSize,
		Volume24h:       m.Volume24hr,
		Liquidity:       m.LiquidityNum,
		EndsAt:          parseTime(m.EndDate),
		Tags:            tagLabels(m.Tags),
	}
}

func tagLabels(tags []pm.Tag) []string {
	if len(tags) == 0 {
		return nil
	}

	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = t.Label
	}

	return out
}

// toOutcomes zips labels, token IDs and prices. Markets without an order book have no
// token IDs; their outcomes keep an empty TokenID.
func toOutcomes(m pm.Market) []domain.Outcome {
	tokens := m.TokenIDs()
	outcomes := make([]domain.Outcome, len(m.Outcomes))

	for i, label := range m.Outcomes {
		outcomes[i].Label = label

		if i < len(tokens) {
			outcomes[i].TokenID = domain.TokenID(tokens[i])
		}

		if i < len(m.OutcomePrices) {
			outcomes[i].Price = m.OutcomePrices[i]
		}
	}

	return outcomes
}

func toMarketStatus(m pm.Market) domain.MarketStatus {
	switch {
	case m.Archived:
		return domain.MarketArchived
	case m.Closed && (m.UMAResolutionStatus == resolved || m.ResolutionStatus == resolved):
		return domain.MarketResolved
	case m.Closed:
		return domain.MarketClosed
	default:
		return domain.MarketOpen
	}
}

func toEvent(e pm.Event) domain.Event {
	markets := make([]domain.Market, len(e.Markets))
	for i, m := range e.Markets {
		markets[i] = toMarket(m)
	}

	tags := make([]string, len(e.Tags))
	for i, t := range e.Tags {
		tags[i] = t.Slug
	}

	return domain.Event{
		ID:          domain.EventID(e.ID),
		Slug:        e.Slug,
		Title:       e.Title,
		Description: e.Description,
		Markets:     markets,
		Tags:        tags,
		NegRisk:     e.NegRisk || e.EnableNegRisk,
		StartsAt:    parseTime(e.StartDate),
		EndsAt:      parseTime(e.EndDate),
		Volume24h:   e.Volume24hr,
		Liquidity:   e.Liquidity,
	}
}

func toOrderBook(b pm.OrderBook) domain.OrderBook {
	return domain.OrderBook{
		MarketID: domain.MarketID(b.Market),
		TokenID:  domain.TokenID(b.AssetID),
		Bids:     toLevels(b.Bids, true),
		Asks:     toLevels(b.Asks, false),
		TickSize: b.TickSize,
		At:       parseTime(b.Timestamp),
	}
}

// toLevels sorts best first: bids high to low, asks low to high. The API sends the
// opposite order, but sorting does not depend on that.
func toLevels(levels []pm.PriceLevel, bids bool) []domain.Level {
	out := make([]domain.Level, len(levels))
	for i, l := range levels {
		out[i] = domain.Level{Price: l.Price, Size: l.Size}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if bids {
			return out[i].Price.GreaterThan(out[j].Price)
		}

		return out[i].Price.LessThan(out[j].Price)
	})

	return out
}

func toTrade(t pm.Trade) domain.Trade {
	return domain.Trade{
		MarketID: domain.MarketID(t.ConditionID),
		TokenID:  domain.TokenID(t.TokenID),
		Outcome:  t.Outcome,
		Side:     toSide(t.Side),
		Price:    t.Price,
		Size:     t.Size,
		Wallet:   domain.WalletAddress(t.ProxyWallet),
		At:       time.Unix(t.Timestamp, 0).UTC(),
		TxHash:   t.TransactionHash,

		Title:     t.Title,
		Slug:      t.Slug,
		EventSlug: t.EventSlug,
		Name:      traderName(t.Name, t.Pseudonym),
	}
}

// placeholderName is the name a profile without a username gets: its address and a time.
var placeholderName = regexp.MustCompile(`^0x[0-9a-fA-F]{40}-\d+$`)

// traderName is a wallet's name on Polymarket: its username, else its pseudonym.
func traderName(name, pseudonym string) string {
	if name == "" || placeholderName.MatchString(name) {
		return pseudonym
	}

	return name
}

func toPosition(p pm.Position) domain.Position {
	return domain.Position{
		Wallet:        domain.WalletAddress(p.ProxyWallet),
		MarketID:      domain.MarketID(p.ConditionID),
		TokenID:       domain.TokenID(p.TokenID),
		Title:         p.Title,
		Outcome:       p.Outcome,
		Size:          p.CurrentSize,
		AvgPrice:      p.AvgPrice,
		CurrentPrice:  p.CurrentPrice,
		Value:         p.CurrentValue,
		RealizedPnL:   p.RealizedPnl,
		UnrealizedPnL: p.UnrealizedPnl,
		Redeemable:    p.Redeemable,
	}
}

func toPricePoint(p pm.PricePoint) domain.PricePoint {
	return domain.PricePoint{At: time.Unix(p.Timestamp, 0).UTC(), Price: p.Price}
}

func toSide(s pm.Side) domain.Side {
	switch s {
	case pm.SideBuy:
		return domain.SideBuy
	case pm.SideSell:
		return domain.SideSell
	default:
		return ""
	}
}

func fromSide(s domain.Side) pm.Side {
	switch s {
	case domain.SideBuy:
		return pm.SideBuy
	case domain.SideSell:
		return pm.SideSell
	default:
		return ""
	}
}

// timeLayouts are the formats seen across the APIs: Gamma ISO dates (with and without
// fractions), Gamma closedTime ("2026-10-08 14:59:26+00") and Data end_date ("2027-01-01").
var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05-07",
	"2006-01-02",
}

// parseTime handles every time encoding the APIs use: ISO strings, and unix seconds or
// milliseconds as digit strings. Unparseable or empty input gives the zero time.
func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}

	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if len(s) >= 13 {
			return time.UnixMilli(n).UTC()
		}

		return time.Unix(n, 0).UTC()
	}

	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}

	return time.Time{}
}

// toStrings converts typed domain IDs to the plain strings the clients take. Empty input
// stays nil, so unset filters stay unset.
func toStrings[T ~string](ids []T) []string {
	if len(ids) == 0 {
		return nil
	}

	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}

	return out
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return t.Unix()
}
