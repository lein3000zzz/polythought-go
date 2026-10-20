package polymarket

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

var _ ports.LiveExchange = (*Exchange)(nil)

// Exchange places a wallet's orders on Polymarket's CLOB and follows them (3p2): built
// and signed here with the wallet's key, as Polymarket's client builds and signs them
// (3n), and sent through the CLOB client (3p1).
type Exchange struct {
	clob    pm.ClobTradingClient
	account pm.ClobAccountClient

	now  func() time.Time
	salt func() (uint64, error)
}

func NewExchange(clob pm.ClobTradingClient, account pm.ClobAccountClient) *Exchange {
	return &Exchange{clob: clob, account: account, now: time.Now, salt: pm.NewSalt}
}

// Place signs o for the wallet and sends it. A refusal, and an order the CLOB could not
// fill, are answers; the error is for an order not sent at all.
func (e *Exchange) Place(ctx context.Context, k ports.OrderKey, o ports.LiveOrder, signed func(id string) error) (ports.LivePlacement, error) {
	post, err := e.signed(k, o)
	if err != nil {
		return ports.LivePlacement{}, err
	}

	id, err := post.ID()
	if err != nil {
		return ports.LivePlacement{}, err
	}

	if signed != nil {
		if err := signed(id); err != nil {
			return ports.LivePlacement{}, err
		}
	}

	posted, err := e.clob.PostOrder(ctx, toL2(k.Credentials), post)

	return placement(id, posted, err)
}

// PlaceBatch signs the orders and sends them MaxBatchOrders to a request (D244).
func (e *Exchange) PlaceBatch(ctx context.Context, k ports.OrderKey, os []ports.LiveOrder, signed func(i int, id string) error) ([]ports.LivePlacement, error) {
	posts := make([]pm.OrderPost, len(os))
	ids := make([]string, len(os))

	for i, o := range os {
		post, err := e.signed(k, o)
		if err != nil {
			return nil, err
		}

		if ids[i], err = post.ID(); err != nil {
			return nil, err
		}

		posts[i] = post
	}

	if signed != nil {
		for i, id := range ids {
			if err := signed(i, id); err != nil {
				return nil, err
			}
		}
	}

	out := make([]ports.LivePlacement, len(os))

	for start := 0; start < len(posts); start += pm.MaxBatchOrders {
		end := min(start+pm.MaxBatchOrders, len(posts))

		results, err := e.clob.PostOrders(ctx, toL2(k.Credentials), posts[start:end])
		if err != nil {
			results = make([]pm.PostResult, end-start)
			for i := range results {
				results[i].Err = err // the whole request: each order the same
			}
		}

		for i, r := range results {
			p, err := placement(ids[start+i], r.Order, r.Err)
			if err != nil {
				p = ports.LivePlacement{ID: ids[start+i], State: ports.LiveRefused, Reason: "it could not be sent to Polymarket: " + err.Error()}
			}

			out[start+i] = p
		}
	}

	return out, nil
}

// placement is how an order's send went, as the CLOB answered: placed, refused, a FAK or
// FOK it could not fill, or unknown. The error is for an order not sent at all.
func placement(id string, posted pm.PostedOrder, err error) (ports.LivePlacement, error) {
	var (
		refused   *pm.OrderRefusedError
		uncertain *pm.OrderUncertainError
	)

	switch {
	case errors.As(err, &refused) && (refused.Code == pm.RefusedFOKNotFilled || refused.Code == pm.RefusedFAKNotFilled):
		return ports.LivePlacement{ID: id, State: ports.LiveDone, Reason: notFilled(refused.Code)}, nil
	case errors.As(err, &refused):
		return ports.LivePlacement{ID: id, State: ports.LiveRefused, Reason: refusal(refused)}, nil
	case errors.As(err, &uncertain):
		return ports.LivePlacement{ID: id, State: ports.LiveUnknown}, nil
	case errors.Is(err, pm.ErrUnauthorized):
		return ports.LivePlacement{ID: id, State: ports.LiveRefused,
			Reason: "Polymarket refused the wallet's API credentials: set them up again on the Wallets page"}, nil
	case err != nil:
		return ports.LivePlacement{}, fmt.Errorf("%w: %w", domain.ErrUnavailable, err)
	}

	return ports.LivePlacement{ID: cmp.Or(posted.ID, id), State: postState(posted.Status), TradeIDs: posted.TradeIDs}, nil
}

// signed is o as the wallet's order, signed with its key: made by the wallet, signed by
// the key for an EOA, a proxy or a Safe, by the wallet itself for a Deposit Wallet.
func (e *Exchange) signed(k ports.OrderKey, o ports.LiveOrder) (pm.OrderPost, error) {
	signer, err := pm.NewOrderSigner(k.Key, k.Credentials.SessionKey)
	if err != nil {
		return pm.OrderPost{}, err
	}

	kind := pm.SignatureType(k.Credentials.SignatureType)
	wallet := common.HexToAddress(string(k.Wallet.Address)).Hex()

	by := signer.Address()
	if kind == pm.SignatureDepositWallet {
		by = wallet
	}

	side := pm.SideBuy
	if o.Side == domain.SideSell {
		side = pm.SideSell
	}

	var amounts pm.Amounts

	switch {
	case o.Shares.IsNegative() || o.Dollars.IsNegative() || (!o.Shares.IsPositive() && !o.Dollars.IsPositive()):
		return pm.OrderPost{}, fmt.Errorf("%w: an order of %s shares and $%s", domain.ErrInvalidInput, o.Shares, o.Dollars)
	case o.Side == domain.SideBuy && o.Dollars.IsPositive():
		amounts, err = pm.MarketAmounts(side, o.Dollars, o.Price, o.TickSize, true)
	case o.Side == domain.SideSell:
		amounts, err = pm.MarketAmounts(side, o.Shares, o.Price, o.TickSize, true)
	default:
		amounts, err = pm.LimitAmounts(side, o.Price, o.Shares, o.TickSize)
	}

	if err != nil {
		return pm.OrderPost{}, err
	}

	ex, err := pm.ExchangeFor(string(o.TokenID), o.NegRisk)
	if err != nil {
		return pm.OrderPost{}, err
	}

	salt, err := e.salt()
	if err != nil {
		return pm.OrderPost{}, err
	}

	order := pm.Order{
		Salt: salt, Maker: wallet, Signer: by, TokenID: string(o.TokenID),
		MakerAmount: amounts.Maker, TakerAmount: amounts.Taker, Side: side, SignatureType: kind,
		Timestamp: uint64(e.now().UnixMilli()),
	}

	if o.Builder != "" {
		b, err := builderCode(o.Builder)
		if err != nil {
			return pm.OrderPost{}, err
		}

		order.Builder = b
	}

	signature, err := signer.Sign(pm.Polygon, ex, order)
	if err != nil {
		return pm.OrderPost{}, err
	}

	signed := pm.SignedOrder{Order: order, Signature: signature}
	if o.Type == domain.OrderGTD {
		// The CLOB ends a GTD a minute before its expiration: it is set a minute later.
		signed.Expiration = o.Expires.Add(gtdLead).Unix()
	}

	return pm.OrderPost{Order: signed, Exchange: ex, Type: pm.OrderType(o.Type), PostOnly: o.PostOnly}, nil
}

// gtdLead is how much earlier than its expiration the CLOB ends a GTD order.
const gtdLead = time.Minute

// builderCode is a builder code as the order signs it: 32 bytes.
func builderCode(code string) ([32]byte, error) {
	var out [32]byte

	b := common.FromHex(code)
	if len(b) != 32 || !strings.HasPrefix(code, "0x") {
		return out, fmt.Errorf("the builder code %q is not 32 bytes of hex", code)
	}

	copy(out[:], b)

	return out, nil
}

func postState(s pm.PostStatus) ports.LiveState {
	switch s {
	case pm.PostMatched:
		return ports.LiveMatched
	case pm.PostLive, pm.PostUnmatched:
		return ports.LiveResting
	case pm.PostDelayed:
		return ports.LiveDelayed
	default:
		return ports.LiveDone
	}
}

func notFilled(code pm.RefusalCode) string {
	if code == pm.RefusedFOKNotFilled {
		return "the book could not fill all of it within its price"
	}

	return "the book had nothing to match within its price"
}

// refusal is why Polymarket refused an order, for the owner, with its own words.
func refusal(r *pm.OrderRefusedError) string {
	why := map[pm.RefusalCode]string{
		pm.RefusedBalance:         "not enough pUSD or shares in the wallet, or its trading approvals are missing",
		pm.RefusedTick:            "its price is off the market's tick",
		pm.RefusedMinSize:         "it is under the market's least order",
		pm.RefusedMarketNotReady:  "the market takes no orders yet",
		pm.RefusedCloseOnly:       "Polymarket lets this wallet, or this server's region, only close positions",
		pm.RefusedBanned:          "Polymarket has banned this wallet",
		pm.RefusedTradingDisabled: "trading on Polymarket is disabled for now",
		pm.RefusedCancelOnly:      "Polymarket takes only cancels for now",
		pm.RefusedPostOnlyMode:    "Polymarket takes only post-only orders for now",
		pm.RefusedRestarting:      "Polymarket's matching engine is restarting",
		pm.RefusedRateLimited:     "too many orders: Polymarket's rate limit",
		pm.RefusedCredentials:     "the wallet's API credentials are not its key's: set them up again",
	}[r.Code]

	if why == "" {
		return "Polymarket refused it: " + r.Message
	}

	return why + " (Polymarket: " + r.Message + ")"
}

func (e *Exchange) Order(ctx context.Context, k ports.OrderKey, id string) (ports.LiveOrderState, error) {
	o, err := e.clob.Order(ctx, toL2(k.Credentials), id)
	if errors.Is(err, pm.ErrNotFound) {
		return ports.LiveOrderState{State: ports.LiveNotFound}, nil
	}

	if err != nil {
		return ports.LiveOrderState{}, accountError(err)
	}

	state := ports.LiveDone

	switch strings.ToUpper(o.Status) {
	case "LIVE", "UNMATCHED":
		state = ports.LiveResting
	case "MATCHED":
		state = ports.LiveMatched
	case "DELAYED":
		state = ports.LiveDelayed
	}

	return ports.LiveOrderState{State: state, Matched: o.SizeMatched, TradeIDs: o.AssociateTrades}, nil
}

// Trades reads each trade by its ID. The order's part in it are its fills: as the taker,
// one at each maker order's price, in the order's own outcome; as a maker, its own part.
func (e *Exchange) Trades(ctx context.Context, k ports.OrderKey, order string, token domain.TokenID, ids []string) ([]ports.LiveTrade, error) {
	out := make([]ports.LiveTrade, 0, len(ids))

	for _, id := range ids {
		page, err := e.clob.Trades(ctx, toL2(k.Credentials), pm.ClobTradesQuery{ID: id})
		if err != nil {
			return nil, accountError(err)
		}

		for _, t := range page.Items {
			if t.ID == id {
				maker := !strings.EqualFold(t.TakerOrderID, order)
				out = append(out, ports.LiveTrade{ID: t.ID, Status: fillStatus(t.Status), Fills: fills(t, order, token, maker), Maker: maker})
			}
		}
	}

	return out, nil
}

var one = decimal.NewFromInt(1)

func fills(t pm.ClobTrade, order string, token domain.TokenID, maker bool) []domain.OrderFill {
	fill := func(shares, price decimal.Decimal) domain.OrderFill {
		return domain.OrderFill{Price: price, Shares: shares, Dollars: shares.Mul(price), Fee: decimal.Zero, TradeID: t.ID, At: t.MatchTime.UTC()}
	}

	// A maker order in the other outcome matched ours by minting or merging: its price
	// there is what ours costs here, from 1.
	priced := func(m pm.MakerOrder) decimal.Decimal {
		if m.AssetID != string(token) {
			return one.Sub(m.Price)
		}

		return m.Price
	}

	var out []domain.OrderFill

	if maker {
		for _, m := range t.MakerOrders {
			if strings.EqualFold(m.OrderID, order) {
				out = append(out, fill(m.MatchedAmount, m.Price))
			}
		}

		return out
	}

	for _, m := range t.MakerOrders {
		out = append(out, fill(m.MatchedAmount, priced(m)))
	}

	if len(out) == 0 {
		out = append(out, fill(t.Size, t.Price))
	}

	return out
}

func fillStatus(s pm.TradeStatus) domain.FillStatus {
	switch s {
	case pm.TradeMined:
		return domain.FillMined
	case pm.TradeRetrying:
		return domain.FillRetrying
	case pm.TradeConfirmed:
		return domain.FillConfirmed
	case pm.TradeFailed:
		return domain.FillFailed
	default:
		return domain.FillMatched
	}
}

// Cancel cancels what is left of the order with id; one Polymarket does not cancel, as
// it matched or went already, says why.
func (e *Exchange) Cancel(ctx context.Context, k ports.OrderKey, id string) (ports.LiveCancel, error) {
	c, err := e.clob.CancelOrder(ctx, toL2(k.Credentials), id)
	if err != nil {
		return ports.LiveCancel{}, accountError(err)
	}

	if slices.ContainsFunc(c.Canceled, func(got string) bool { return strings.EqualFold(got, id) }) {
		return ports.LiveCancel{Canceled: true}, nil
	}

	for got, why := range c.NotCanceled {
		if strings.EqualFold(got, id) {
			return ports.LiveCancel{Reason: why}, nil
		}
	}

	return ports.LiveCancel{Reason: "Polymarket did not say"}, nil
}

// Heartbeat sends one heartbeat for the wallet's API key (D214); after a mismatch, once
// more with the ID Polymarket expected, as its client does.
func (e *Exchange) Heartbeat(ctx context.Context, k ports.OrderKey, last string) (string, error) {
	next, err := e.clob.Heartbeat(ctx, toL2(k.Credentials), last)

	var mismatch *pm.HeartbeatMismatchError
	if errors.As(err, &mismatch) {
		next, err = e.clob.Heartbeat(ctx, toL2(k.Credentials), mismatch.Expected)
	}

	if err != nil {
		return "", accountError(err)
	}

	return next, nil
}

func (e *Exchange) Cash(ctx context.Context, k ports.OrderKey) (decimal.Decimal, error) {
	b, err := e.account.BalanceAllowance(ctx, toL2(k.Credentials), pm.BalanceQuery{
		AssetType: pm.AssetCollateral, SignatureType: int(k.Credentials.SignatureType),
	})
	if err != nil {
		return decimal.Zero, accountError(err)
	}

	return b.Balance.Shift(-collateralDecimals), nil
}

var _ ports.LiveOrderStream = (*OrderStream)(nil)

// OrderStream follows wallets' orders on the CLOB's user channel (3p3, D195): which
// changed, for the trading service to read again over REST.
type OrderStream struct {
	users pm.UserStreamClient
}

func NewOrderStream(users pm.UserStreamClient) *OrderStream {
	return &OrderStream{users: users}
}

func (s *OrderStream) Watch(ctx context.Context, k ports.OrderKey) <-chan ports.LiveOrderEvent {
	out := make(chan ports.LiveOrderEvent, 64)
	events := s.users.Watch(ctx, toL2(k.Credentials))

	go func() {
		defer close(out)

		for e := range events {
			var ev ports.LiveOrderEvent

			switch {
			case e.Gap:
				ev.Gap = true
			case e.Order != nil:
				ev.Orders = []string{e.Order.ID}
			case e.Trade != nil:
				ev.Orders = e.Trade.Orders()
			default:
				continue
			}

			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}
