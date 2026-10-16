// Package code runs users' TypeScript: compiled, then run in the sandbox with the SDK,
// whose data requests it answers for the code's owner. Answers are read-only, reach only
// the owner's own wallets, and each request counts against the run's limit.
package code

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/account"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/markets"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// Bounds on data requests, so one cannot fetch a great deal.
const (
	maxSearch    = 50
	maxTokens    = 50
	maxDepth     = 50
	maxPositions = 500
)

// Data is what the SDK's requests read.
type Data struct {
	Markets    ports.MarketReader
	Events     ports.EventReader
	OrderBooks ports.OrderBookReader
	Wallets    ports.WalletDirectory
	Positions  ports.PositionReader
}

type Service struct {
	compiler ports.CodeCompiler
	runner   ports.CodeRunner
	data     Data
}

func New(compiler ports.CodeCompiler, runner ports.CodeRunner, data Data) *Service {
	return &Service{compiler: compiler, runner: runner, data: data}
}

var _ ports.CodeCaller = (*Service)(nil)

// Call compiles the code and calls the export. The code failing to compile or to run is a
// *domain.CodeError; the logs come back either way.
func (s *Service) Call(ctx context.Context, c ports.CodeCall) (ports.CodeResult, error) {
	code, err := s.compiler.Compile(ctx, c.Source)
	if err != nil {
		return ports.CodeResult{}, err
	}

	args := c.Args
	if args == nil {
		args = []any{}
	}

	params := c.Params
	if params == nil {
		params = map[string]any{}
	}

	input, err := json.Marshal(ports.CodeInvocation{
		Exports: code.Exports, Export: c.Export, Args: args, Each: c.Each,
		Context: ports.CodeContext{Params: params, Execution: c.Execution},
	})
	if err != nil {
		return ports.CodeResult{}, fmt.Errorf("code: the arguments: %w", err)
	}

	return s.runner.Run(ctx, ports.CodeJob{
		Source: code.Script, Entry: code.Invoke, Input: input, Host: s.host(c.Owner), Limits: c.Limits,
	})
}

// host answers the SDK's data requests for owner. Errors go back to the code, thrown, so
// they say what the code asked for.
func (s *Service) host(owner domain.UserID) ports.CodeHost {
	return func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
		var (
			answer any
			err    error
		)

		switch name {
		case "markets.search":
			answer, err = s.searchMarkets(ctx, args)
		case "markets.get":
			answer, err = s.market(ctx, args)
		case "orderBooks.get":
			answer, err = s.orderBooks(ctx, args)
		case "positions.list":
			answer, err = s.positions(ctx, owner, args)
		default:
			return nil, fmt.Errorf("there is no data request %q", name)
		}

		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}

		return json.Marshal(answer)
	}
}

func (s *Service) searchMarkets(ctx context.Context, raw json.RawMessage) (any, error) {
	var args struct {
		Query  string `json:"query"`
		Limit  *int   `json:"limit"`
		Closed bool   `json:"closed"`
	}

	if err := decode(raw, &args); err != nil {
		return nil, err
	}

	if strings.TrimSpace(args.Query) == "" {
		return nil, errors.New("give some words to search for")
	}

	limit, err := bounded("limit", args.Limit, 10, maxSearch)
	if err != nil {
		return nil, err
	}

	return markets.SearchMarkets(ctx, s.data.Events, args.Query, limit, args.Closed)
}

func (s *Service) market(ctx context.Context, raw json.RawMessage) (any, error) {
	var args struct {
		ID string `json:"id"`
	}

	if err := decode(raw, &args); err != nil {
		return nil, err
	}

	if args.ID == "" {
		return nil, errors.New("give a market's ID or slug")
	}

	var (
		m   domain.Market
		err error
	)

	if strings.HasPrefix(args.ID, "0x") {
		m, err = s.data.Markets.GetMarket(ctx, domain.MarketID(args.ID))
	} else {
		m, err = s.data.Markets.GetMarketBySlug(ctx, args.ID)
	}

	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return markets.MarketItem(m), nil
}

func (s *Service) orderBooks(ctx context.Context, raw json.RawMessage) (any, error) {
	var args struct {
		TokenIDs []string `json:"tokenIds"`
		Depth    *int     `json:"depth"`
	}

	if err := decode(raw, &args); err != nil {
		return nil, err
	}

	if len(args.TokenIDs) == 0 || len(args.TokenIDs) > maxTokens {
		return nil, fmt.Errorf("give from 1 to %d token IDs, not %d", maxTokens, len(args.TokenIDs))
	}

	depth, err := bounded("depth", args.Depth, 10, maxDepth)
	if err != nil {
		return nil, err
	}

	ids := make([]domain.TokenID, len(args.TokenIDs))
	for i, id := range args.TokenIDs {
		ids[i] = domain.TokenID(id)
	}

	books, err := s.data.OrderBooks.OrderBooks(ctx, ids...)
	if err != nil {
		return nil, err
	}

	byToken := make(map[domain.TokenID]domain.OrderBook, len(books))
	for _, b := range books {
		byToken[b.TokenID] = b
	}

	// In the order asked; null for a token without a book.
	out := make([]any, len(ids))

	for i, id := range ids {
		if b, ok := byToken[id]; ok {
			out[i] = bookAnswer(b, depth)
		}
	}

	return out, nil
}

// bookAnswer is an order book as the SDK's OrderBook describes it.
func bookAnswer(b domain.OrderBook, depth int) map[string]any {
	j := map[string]any{
		"tokenId": string(b.TokenID),
		"bestBid": nil, "bestAsk": nil, "mid": nil, "spread": nil,
		"bids": levels(b.Bids, depth), "asks": levels(b.Asks, depth),
		"at": b.At,
	}

	j["bidDepth"], j["askDepth"] = markets.SumSize(b.Bids, depth), markets.SumSize(b.Asks, depth)

	if bid, ok := b.BestBid(); ok {
		j["bestBid"] = bid.Price
	}

	if ask, ok := b.BestAsk(); ok {
		j["bestAsk"] = ask.Price
	}

	if mid, ok := b.Mid(); ok {
		j["mid"] = mid
	}

	if spread, ok := b.Spread(); ok {
		j["spread"] = spread
	}

	return j
}

func levels(ls []domain.Level, depth int) []map[string]any {
	out := make([]map[string]any, 0, min(depth, len(ls)))
	for _, l := range ls[:min(depth, len(ls))] {
		out = append(out, map[string]any{"price": l.Price, "size": l.Size})
	}

	return out
}

func (s *Service) positions(ctx context.Context, owner domain.UserID, raw json.RawMessage) (any, error) {
	var args struct {
		WalletID string `json:"walletId"`
		Limit    *int   `json:"limit"`
	}

	if err := decode(raw, &args); err != nil {
		return nil, err
	}

	limit, err := bounded("limit", args.Limit, 50, maxPositions)
	if err != nil {
		return nil, err
	}

	// Only the owner's: someone else's wallet is as good as none.
	w, err := s.data.Wallets.Wallet(ctx, owner, domain.WalletID(args.WalletID))
	if errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("you have no wallet %q", args.WalletID)
	}

	if err != nil {
		return nil, err
	}

	page, err := s.data.Positions.Positions(ctx, ports.PositionFilter{Wallet: w.Address, Limit: limit})
	if err != nil {
		return nil, err
	}

	out := make([]map[string]any, len(page.Items))
	for i, pos := range page.Items {
		out[i] = account.PositionItem(w, pos)
	}

	return out, nil
}

func decode(raw json.RawMessage, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return errors.New("these are not the arguments it takes")
	}

	return nil
}

// bounded reads an optional whole number from 1 to most.
func bounded(name string, n *int, otherwise, most int) (int, error) {
	if n == nil {
		return otherwise, nil
	}

	if *n < 1 || *n > most {
		return 0, fmt.Errorf("%s must be from 1 to %d, not %d", name, most, *n)
	}

	return *n, nil
}
