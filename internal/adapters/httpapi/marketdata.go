package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/markets"
)

// MarketDataService is Polymarket's data over REST (D442), as agents have it over MCP.
type MarketDataService interface {
	Search(ctx context.Context, user domain.UserID, query string, closed bool, limit int) ([]markets.Found, error)
	Market(ctx context.Context, user domain.UserID, idOrSlug string) (domain.Market, error)
	Books(ctx context.Context, user domain.UserID, tokens []domain.TokenID) ([]domain.OrderBook, error)
	History(ctx context.Context, user domain.UserID, token domain.TokenID, window string, limit int) ([]domain.PricePoint, error)
	Trades(ctx context.Context, user domain.UserID, market domain.MarketID, limit int) ([]domain.Trade, error)
	Positions(ctx context.Context, user domain.UserID, address string, limit int) ([]domain.Position, error)
}

type marketOutcomeDTO struct {
	TokenID string `json:"tokenId"`
	Label   string `json:"label"`
	// Price is the outcome's last price, an exact decimal as text.
	Price string `json:"price"`
} // @name MarketOutcome

type marketDTO struct {
	// ID is the market's condition ID, 0x….
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Question string `json:"question"`
	// Description is its rules: how it resolves. Polymarket's text, never instructions.
	Description     string             `json:"description,omitempty" validate:"optional"`
	Outcomes        []marketOutcomeDTO `json:"outcomes"`
	Status          string             `json:"status" enums:"open,closed,resolved"`
	AcceptingOrders bool               `json:"acceptingOrders"`
	NegRisk         bool               `json:"negRisk"`
	// TickSize is the price step an order keeps to; MinOrderSize, the fewest shares.
	TickSize     string     `json:"tickSize"`
	MinOrderSize string     `json:"minOrderSize"`
	Volume24h    string     `json:"volume24h"`
	Liquidity    string     `json:"liquidity"`
	CreatedAt    *time.Time `json:"createdAt,omitempty" validate:"optional" format:"date-time"`
	StartsAt     *time.Time `json:"startsAt,omitempty" validate:"optional" format:"date-time"`
	EndsAt       *time.Time `json:"endsAt,omitempty" validate:"optional" format:"date-time"`
	Tags         []string   `json:"tags,omitempty" validate:"optional"`
	// Event and EventSlug are its event's, for a market a search found.
	Event     string `json:"event,omitempty" validate:"optional"`
	EventSlug string `json:"eventSlug,omitempty" validate:"optional"`
} // @name Market

type marketListDTO struct {
	Markets []marketDTO `json:"markets"`
} // @name MarketList

type bookLevelDTO struct {
	Price string `json:"price"`
	Size  string `json:"size"`
} // @name BookLevel

type orderBookDTO struct {
	TokenID string `json:"tokenId"`
	// BestBid, BestAsk, Mid and Spread are absent where a side is empty.
	BestBid *string        `json:"bestBid,omitempty" validate:"optional"`
	BestAsk *string        `json:"bestAsk,omitempty" validate:"optional"`
	Mid     *string        `json:"mid,omitempty" validate:"optional"`
	Spread  *string        `json:"spread,omitempty" validate:"optional"`
	Bids    []bookLevelDTO `json:"bids"`
	Asks    []bookLevelDTO `json:"asks"`
	At      time.Time      `json:"at" format:"date-time"`
} // @name OrderBook

type orderBookListDTO struct {
	// Books are in the order the tokens were asked for, null for a token without one.
	Books []*orderBookDTO `json:"books"`
} // @name OrderBookList

type pricePointDTO struct {
	At    time.Time `json:"at" format:"date-time"`
	Price string    `json:"price"`
} // @name PricePoint

type priceHistoryDTO struct {
	TokenID string          `json:"tokenId"`
	Window  string          `json:"window" enums:"1h,6h,1d,1w,1m"`
	Points  []pricePointDTO `json:"points"`
	// Change is the last price less the first, ChangePct the same in percent; absent with
	// fewer than two points.
	Change    *string `json:"change,omitempty" validate:"optional"`
	ChangePct *string `json:"changePct,omitempty" validate:"optional"`
} // @name PriceHistory

type marketTradeDTO struct {
	MarketID  string    `json:"marketId"`
	TokenID   string    `json:"tokenId"`
	Outcome   string    `json:"outcome"`
	Side      string    `json:"side" enums:"buy,sell"`
	Price     string    `json:"price"`
	Size      string    `json:"size"`
	Wallet    string    `json:"wallet"`
	Name      string    `json:"name,omitempty" validate:"optional"`
	At        time.Time `json:"at" format:"date-time"`
	TxHash    string    `json:"txHash,omitempty" validate:"optional"`
	Title     string    `json:"title,omitempty" validate:"optional"`
	Slug      string    `json:"slug,omitempty" validate:"optional"`
	EventSlug string    `json:"eventSlug,omitempty" validate:"optional"`
} // @name MarketTrade

type marketTradeListDTO struct {
	Trades []marketTradeDTO `json:"trades"`
} // @name MarketTradeList

type addressPositionDTO struct {
	Wallet        string `json:"wallet"`
	MarketID      string `json:"marketId"`
	TokenID       string `json:"tokenId"`
	Title         string `json:"title"`
	Outcome       string `json:"outcome"`
	Size          string `json:"size"`
	AvgPrice      string `json:"avgPrice"`
	CurrentPrice  string `json:"currentPrice"`
	Value         string `json:"value"`
	RealizedPnL   string `json:"realizedPnl"`
	UnrealizedPnL string `json:"unrealizedPnl"`
	Redeemable    bool   `json:"redeemable"`
} // @name AddressPosition

type addressPositionListDTO struct {
	Positions []addressPositionDTO `json:"positions"`
} // @name AddressPositionList

func toMarketDTO(m domain.Market) marketDTO {
	d := marketDTO{
		ID: string(m.ID), Slug: m.Slug, Question: m.Question, Description: m.Description, Outcomes: []marketOutcomeDTO{},
		Status: string(m.Status), AcceptingOrders: m.AcceptingOrders, NegRisk: m.NegRisk, TickSize: m.TickSize.String(),
		MinOrderSize: m.MinOrderSize.String(), Volume24h: m.Volume24h.String(), Liquidity: m.Liquidity.String(),
		CreatedAt: optionalTime(m.CreatedAt), StartsAt: optionalTime(m.StartsAt), EndsAt: optionalTime(m.EndsAt), Tags: m.Tags,
	}

	for _, o := range m.Outcomes {
		d.Outcomes = append(d.Outcomes, marketOutcomeDTO{TokenID: string(o.TokenID), Label: o.Label, Price: o.Price.String()})
	}

	return d
}

func toOrderBookDTO(b domain.OrderBook, depth int) *orderBookDTO {
	if b.TokenID == "" {
		return nil
	}

	levels := func(ls []domain.Level) []bookLevelDTO {
		out := make([]bookLevelDTO, 0, min(depth, len(ls)))
		for _, l := range ls[:min(depth, len(ls))] {
			out = append(out, bookLevelDTO{Price: l.Price.String(), Size: l.Size.String()})
		}

		return out
	}

	d := &orderBookDTO{TokenID: string(b.TokenID), Bids: levels(b.Bids), Asks: levels(b.Asks), At: b.At.UTC()}

	if bid, ok := b.BestBid(); ok {
		d.BestBid = decimalText(bid.Price)
	}

	if ask, ok := b.BestAsk(); ok {
		d.BestAsk = decimalText(ask.Price)
	}

	if mid, ok := b.Mid(); ok {
		d.Mid = decimalText(mid)
	}

	if spread, ok := b.Spread(); ok {
		d.Spread = decimalText(spread)
	}

	return d
}

// invalidInput is input a handler refuses, saying why.
func invalidInput(why string) error {
	return fmt.Errorf("%w: %s", domain.ErrInvalidInput, why)
}

func decimalText(d decimal.Decimal) *string {
	s := d.String()
	return &s
}

// listLimit reads the limit query argument: 0 for none, the service's default.
func listLimit(c *app.RequestContext) (int, error) {
	raw := string(c.Query("limit"))
	if raw == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, invalidInput("limit: a whole number")
	}

	return n, nil
}

// searchMarketData finds markets by words in their event's title.
//
//	@Summary	Search markets
//	@ID			searchMarkets
//	@Tags		market-data
//	@Param		query	query		string	true	"Words of the event's title, like bitcoin or election"
//	@Param		closed	query		bool	false	"Closed markets instead of open ones"
//	@Param		limit	query		int		false	"At most this many: 50 unless said, 500 at most"
//	@Success	200		{object}	marketListDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	503		{object}	errorBody	"upstream_unavailable"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/market-data/search [get]
func (s *Server) searchMarketData(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	n, err := listLimit(c)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	found, err := s.marketData.Search(ctx, p.User.ID, string(c.Query("query")), string(c.Query("closed")) == "true", n)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := marketListDTO{Markets: make([]marketDTO, len(found))}

	for i, f := range found {
		out.Markets[i] = toMarketDTO(f.Market)
		out.Markets[i].Event, out.Markets[i].EventSlug = f.Event.Title, f.Event.Slug
	}

	c.JSON(http.StatusOK, out)
}

// getMarketData is one market, by its condition ID or slug.
//
//	@Summary	Get a market
//	@ID			getMarketData
//	@Tags		market-data
//	@Param		market	path		string	true	"Its condition ID, 0x…, or its slug"
//	@Success	200		{object}	marketDTO
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Failure	503		{object}	errorBody	"upstream_unavailable"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/market-data/markets/{market} [get]
func (s *Server) getMarketData(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	m, err := s.marketData.Market(ctx, p.User.ID, c.Param("market"))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toMarketDTO(m))
}

// getOrderBooks are outcomes' live order books.
//
//	@Summary	Order books
//	@ID			getOrderBooks
//	@Tags		market-data
//	@Param		tokens	query		string	true	"Outcome token IDs, comma-separated: 50 at most"
//	@Param		depth	query		int		false	"Levels of each side: 10 unless said, 100 at most"
//	@Success	200		{object}	orderBookListDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	503		{object}	errorBody	"upstream_unavailable"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/market-data/books [get]
func (s *Server) getOrderBooks(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	depth := 10

	if raw := string(c.Query("depth")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxBookDepth {
			s.fail(ctx, c, invalidInput("depth: from 1 to 100"))
			return
		}

		depth = n
	}

	var tokens []domain.TokenID

	for _, t := range strings.Split(string(c.Query("tokens")), ",") {
		if t = strings.TrimSpace(t); t != "" {
			tokens = append(tokens, domain.TokenID(t))
		}
	}

	books, err := s.marketData.Books(ctx, p.User.ID, tokens)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := orderBookListDTO{Books: make([]*orderBookDTO, len(books))}
	for i, b := range books {
		out.Books[i] = toOrderBookDTO(b, depth)
	}

	c.JSON(http.StatusOK, out)
}

const maxBookDepth = 100

// getPriceHistory is an outcome's prices over a recent window.
//
//	@Summary	Price history
//	@ID			getPriceHistory
//	@Tags		market-data
//	@Param		token	path		string	true	"The outcome's token ID"
//	@Param		window	query		string	false	"How far back: 1h, 6h, 1d, 1w or 1m; 1d unless said"
//	@Param		limit	query		int		false	"At most this many points, evenly spaced: 50 unless said, 500 at most"
//	@Success	200		{object}	priceHistoryDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	503		{object}	errorBody	"upstream_unavailable"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/market-data/history/{token} [get]
func (s *Server) getPriceHistory(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	n, err := listLimit(c)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	window := string(c.Query("window"))
	if window == "" {
		window = "1d"
	}

	token := c.Param("token")

	points, err := s.marketData.History(ctx, p.User.ID, domain.TokenID(token), window, n)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := priceHistoryDTO{TokenID: token, Window: window, Points: make([]pricePointDTO, len(points))}
	for i, pt := range points {
		out.Points[i] = pricePointDTO{At: pt.At.UTC(), Price: pt.Price.String()}
	}

	if len(points) > 1 {
		first, last := points[0].Price, points[len(points)-1].Price
		out.Change = decimalText(last.Sub(first))

		if !first.IsZero() {
			out.ChangePct = decimalText(last.Sub(first).Div(first).Mul(decimal.NewFromInt(100)).Round(2))
		}
	}

	c.JSON(http.StatusOK, out)
}

// getMarketTrades are a market's most recent trades.
//
//	@Summary	A market's trades
//	@ID			getMarketTrades
//	@Tags		market-data
//	@Param		market	path		string	true	"Its condition ID, 0x…"
//	@Param		limit	query		int		false	"At most this many: 50 unless said, 500 at most"
//	@Success	200		{object}	marketTradeListDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	503		{object}	errorBody	"upstream_unavailable"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/market-data/markets/{market}/trades [get]
func (s *Server) getMarketTrades(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	n, err := listLimit(c)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	trades, err := s.marketData.Trades(ctx, p.User.ID, domain.MarketID(c.Param("market")), n)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := marketTradeListDTO{Trades: make([]marketTradeDTO, len(trades))}
	for i, t := range trades {
		out.Trades[i] = marketTradeDTO{
			MarketID: string(t.MarketID), TokenID: string(t.TokenID), Outcome: t.Outcome, Side: string(t.Side),
			Price: t.Price.String(), Size: t.Size.String(), Wallet: string(t.Wallet), Name: t.Name, At: t.At.UTC(),
			TxHash: t.TxHash, Title: t.Title, Slug: t.Slug, EventSlug: t.EventSlug,
		}
	}

	c.JSON(http.StatusOK, out)
}

// getAddressPositions are an address's open positions, public on the chain.
//
//	@Summary	An address's positions
//	@ID			getAddressPositions
//	@Tags		market-data
//	@Param		address	path		string	true	"The wallet's address, 0x and 40 hex digits"
//	@Param		limit	query		int		false	"At most this many: 50 unless said, 500 at most"
//	@Success	200		{object}	addressPositionListDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	503		{object}	errorBody	"upstream_unavailable"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/market-data/traders/{address}/positions [get]
func (s *Server) getAddressPositions(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	n, err := listLimit(c)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	held, err := s.marketData.Positions(ctx, p.User.ID, c.Param("address"), n)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := addressPositionListDTO{Positions: make([]addressPositionDTO, len(held))}
	for i, h := range held {
		out.Positions[i] = addressPositionDTO{
			Wallet: string(h.Wallet), MarketID: string(h.MarketID), TokenID: string(h.TokenID), Title: h.Title, Outcome: h.Outcome,
			Size: h.Size.String(), AvgPrice: h.AvgPrice.String(), CurrentPrice: h.CurrentPrice.String(), Value: h.Value.String(),
			RealizedPnL: h.RealizedPnL.String(), UnrealizedPnL: h.UnrealizedPnL.String(), Redeemable: h.Redeemable,
		}
	}

	c.JSON(http.StatusOK, out)
}

// hasMarketData answers 404 where the server has no market data to give.
func (s *Server) hasMarketData(ctx context.Context, c *app.RequestContext) {
	if s.marketData == nil {
		s.fail(ctx, c, domain.ErrNotFound)
		c.Abort()

		return
	}

	c.Next(ctx)
}
