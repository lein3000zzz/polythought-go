// Package account holds nodes that read a user's own Polymarket accounts: positions
// through the public Data API, orders and balances with the wallet's L2 credentials.
// Wallets are always resolved through the workflow's owner.
package account

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	PositionsType = "polymarket.positions"
	OrdersType    = "polymarket.orders"
	BalanceType   = "polymarket.balance"
)

// maxOrderPages bounds one node run on accounts with very many orders.
const maxOrderPages = 10

var ErrNoCredentials = errors.New("wallet has no API credentials")

var (
	_ engine.Node = (*Positions)(nil)
	_ engine.Node = (*Orders)(nil)
	_ engine.Node = (*Balance)(nil)
)

var walletParam = engine.ParamSpec{Name: "wallet", Label: "Wallet", Kind: engine.ParamWallet, Required: true}

// perItem runs fn for every input item with its resolved params and the wallet named by
// the "wallet" param. Item-level problems fail the item; a cancelled run fails the node.
func perItem(
	ctx context.Context,
	rc engine.RunContext,
	in engine.Ports,
	fn func(item engine.Item, src engine.ItemRef, p engine.Params, wallet domain.WalletID, out engine.Ports) error,
) (engine.Ports, error) {
	out := engine.Ports{}

	for i, item := range in[engine.DefaultPort] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		wallet, err := p.String("wallet")
		if err != nil {
			return nil, err
		}

		if err := fn(item, src, p, domain.WalletID(wallet), out); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}

			out.Fail(item, src, err)
		}
	}

	return out, nil
}

// Positions lists the positions of one of the owner's wallets.
type Positions struct {
	wallets   ports.WalletDirectory
	positions ports.PositionReader
}

func NewPositions(wallets ports.WalletDirectory, positions ports.PositionReader) *Positions {
	return &Positions{wallets: wallets, positions: positions}
}

func (*Positions) Type() engine.NodeType {
	return engine.NodeType{
		Name:        PositionsType,
		Label:       "Positions",
		Description: "Lists the open positions of one of your wallets, one item per position.",
		Inputs:      []string{engine.DefaultPort},
		Outputs:     []string{engine.DefaultPort},
		Params: []engine.ParamSpec{
			walletParam,
			{Name: "limit", Label: "Limit", Description: "Most positions to return.", Kind: engine.ParamNumber, Default: 50},
		},
	}
}

func (n *Positions) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	return perItem(ctx, rc, in, func(_ engine.Item, src engine.ItemRef, p engine.Params, id domain.WalletID, out engine.Ports) error {
		limit, err := p.Int("limit")
		if err != nil {
			return err
		}

		w, err := n.wallets.Wallet(ctx, rc.OwnerID, id)
		if err != nil {
			return fmt.Errorf("wallet %s: %w", id, err)
		}

		page, err := n.positions.Positions(ctx, ports.PositionFilter{Wallet: w.Address, Limit: limit})
		if err != nil {
			return err
		}

		for _, pos := range page.Items {
			out.Add(engine.DefaultPort, engine.Item{Source: []engine.ItemRef{src}, JSON: map[string]any{
				"walletId":      string(w.ID),
				"address":       string(w.Address),
				"marketId":      string(pos.MarketID),
				"tokenId":       string(pos.TokenID),
				"outcome":       pos.Outcome,
				"size":          pos.Size,
				"avgPrice":      pos.AvgPrice,
				"currentPrice":  pos.CurrentPrice,
				"value":         pos.Value,
				"realizedPnl":   pos.RealizedPnL,
				"unrealizedPnl": pos.UnrealizedPnL,
				"redeemable":    pos.Redeemable,
			}})
		}

		return nil
	})
}

// credentials resolves a wallet's credentials, telling "none stored" apart.
func credentials(ctx context.Context, wallets ports.WalletDirectory, owner domain.UserID, id domain.WalletID) (domain.PolymarketCredentials, error) {
	creds, err := wallets.Credentials(ctx, owner, id)
	if errors.Is(err, domain.ErrNotFound) {
		if _, werr := wallets.Wallet(ctx, owner, id); werr == nil {
			return domain.PolymarketCredentials{}, fmt.Errorf("wallet %s: %w", id, ErrNoCredentials)
		}
	}

	if err != nil {
		return domain.PolymarketCredentials{}, fmt.Errorf("wallet %s: %w", id, err)
	}

	return creds, nil
}

// Orders lists the open orders of one of the owner's wallets, using its API credentials.
type Orders struct {
	wallets ports.WalletDirectory
	account ports.AccountReader
}

func NewOrders(wallets ports.WalletDirectory, account ports.AccountReader) *Orders {
	return &Orders{wallets: wallets, account: account}
}

func (*Orders) Type() engine.NodeType {
	return engine.NodeType{
		Name:        OrdersType,
		Label:       "Open orders",
		Description: "Lists the open orders of one of your wallets. Needs API credentials on the wallet.",
		Inputs:      []string{engine.DefaultPort},
		Outputs:     []string{engine.DefaultPort},
		Params: []engine.ParamSpec{
			walletParam,
			{Name: "market", Label: "Market", Description: "Only orders in this market (condition ID).", Kind: engine.ParamString},
		},
	}
}

func (n *Orders) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	return perItem(ctx, rc, in, func(_ engine.Item, src engine.ItemRef, p engine.Params, id domain.WalletID, out engine.Ports) error {
		market, err := p.String("market")
		if err != nil && !errors.Is(err, engine.ErrMissingParam) {
			return err
		}

		creds, err := credentials(ctx, n.wallets, rc.OwnerID, id)
		if err != nil {
			return err
		}

		f := ports.OrderFilter{MarketID: domain.MarketID(market)}

		for range maxOrderPages {
			page, err := n.account.OpenOrders(ctx, creds, f)
			if err != nil {
				return err
			}

			for _, o := range page.Items {
				out.Add(engine.DefaultPort, engine.Item{Source: []engine.ItemRef{src}, JSON: orderItem(id, o)})
			}

			if page.NextCursor == "" {
				break
			}

			f.Cursor = page.NextCursor
		}

		return nil
	})
}

func orderItem(wallet domain.WalletID, o domain.Order) map[string]any {
	var expires any
	if !o.ExpiresAt.IsZero() {
		expires = o.ExpiresAt
	}

	return map[string]any{
		"walletId":  string(wallet),
		"id":        o.ID,
		"marketId":  string(o.MarketID),
		"tokenId":   string(o.TokenID),
		"outcome":   o.Outcome,
		"side":      string(o.Side),
		"price":     o.Price,
		"size":      o.Size,
		"filled":    o.Filled,
		"remaining": o.Remaining(),
		"status":    o.Status,
		"type":      o.Type,
		"createdAt": o.CreatedAt,
		"expiresAt": expires,
	}
}

// Balance reports the spendable collateral of one of the owner's wallets.
type Balance struct {
	wallets ports.WalletDirectory
	account ports.AccountReader
	now     func() time.Time
}

func NewBalance(wallets ports.WalletDirectory, account ports.AccountReader) *Balance {
	return &Balance{wallets: wallets, account: account, now: time.Now}
}

func (*Balance) Type() engine.NodeType {
	return engine.NodeType{
		Name:        BalanceType,
		Label:       "Balance",
		Description: "Reads the USDC collateral balance of one of your wallets. Needs API credentials on the wallet.",
		Inputs:      []string{engine.DefaultPort},
		Outputs:     []string{engine.DefaultPort},
		Params:      []engine.ParamSpec{walletParam},
	}
}

func (n *Balance) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	return perItem(ctx, rc, in, func(_ engine.Item, src engine.ItemRef, _ engine.Params, id domain.WalletID, out engine.Ports) error {
		creds, err := credentials(ctx, n.wallets, rc.OwnerID, id)
		if err != nil {
			return err
		}

		balance, err := n.account.CollateralBalance(ctx, creds)
		if err != nil {
			return err
		}

		out.Add(engine.DefaultPort, engine.Item{Source: []engine.ItemRef{src}, JSON: map[string]any{
			"walletId": string(id),
			"signer":   string(creds.Signer),
			"balance":  balance,
			"at":       n.now().UTC(),
		}})

		return nil
	})
}
