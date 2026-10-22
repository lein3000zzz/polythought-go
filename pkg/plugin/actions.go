package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	pluginv1 "github.com/lein3000zzz/polythought-go/pkg/plugin/v1"
)

// Message is a message to the workflow's owner on Telegram: Text as written, or
// Telegram's HTML (<b>, <i>, <code>, <a href="…">) with HTML. It reaches them after the
// plugin's name, so they know who is talking.
type Message struct {
	Text   string
	HTML   bool
	Silent bool // delivered without a sound
}

// Question asks the owner on Telegram, a button for each of Options (up to 10, 40
// characters each), and waits up to Timeout, an hour when 0. State is what Resume gets
// back, as JSON.
type Question struct {
	Text    string
	HTML    bool
	Options []string
	Timeout time.Duration
	State   any
}

// Wait is what a node returns as its error, from Ask, for its run to wait for the
// answer: the node's Resume then finishes it. Only a Resumer may wait.
type Wait struct {
	state    []byte
	Deadline time.Time // when the run stops waiting
}

func (w *Wait) Error() string {
	return "waiting for an answer until " + w.Deadline.UTC().Format(time.RFC3339)
}

// Resumer is a node that may ask and wait. Resume finishes the run that waited, with the
// items it had, once the answer comes or the time is up. It may ask again.
type Resumer interface {
	Node
	Resume(ctx context.Context, run Run, in Ports, a Answer) (Ports, error)
}

// Answer is how a question ended: an option pressed, or no answer in time.
type Answer struct {
	TimedOut bool
	Option   int // from 0
	Label    string

	state []byte
}

// State decodes into v what the node kept when it asked.
func (a Answer) State(v any) error {
	if len(a.state) == 0 {
		return errors.New("the node kept nothing")
	}

	return json.Unmarshal(a.state, v)
}

// Order is an order, as Place order's settings make one: Shares to buy or sell at Price
// or better, or for a buy Dollars to spend instead.
type Order struct {
	TokenID  string
	MarketID string // empty: the token's
	Side     string // buy or sell
	Type     string // FOK, FAK, GTC or GTD
	Price    Decimal
	Shares   Decimal
	Dollars  Decimal
	PostOnly bool      // a resting order that may not take
	Expires  time.Time // GTD: when what is left of it is cancelled

	// Trader is the address whose trade it copies, if it copies one, with their label.
	Trader      string
	TraderLabel string
}

// PlacedOrder is an order as the workflow's ledger keeps it, and how far it filled.
type PlacedOrder struct {
	ID      string
	Mode    string // paper or live
	Status  string // filled, partial, unfilled, refused, pending, failed, open, canceled or expired
	Reason  string // why it was refused, or did not fill
	TokenID string
	Side    string
	Type    string
	Shares  Decimal // filled so far
	Dollars Decimal
	Fees    Decimal
	Price   Decimal // the average filled; empty when nothing filled
}

// OrderResult is how one order went: placed, refused among them, or Err, why it could
// not be tried at all.
type OrderResult struct {
	Order PlacedOrder
	Err   error
}

// Cancel names the workflow's resting orders to cancel: one by its ID, a market's, an
// outcome's, or All.
type Cancel struct {
	OrderID  string
	MarketID string
	TokenID  string
	All      bool
}

// Cancelled is how cancelling one order went: Reason empty, it was.
type Cancelled struct {
	Order  PlacedOrder
	Reason string
}

func (h *hostClient) Message(ctx context.Context, m Message) error {
	if h.rpc == nil {
		return errNoHost
	}

	_, err := h.rpc.SendMessage(ctx, &pluginv1.SendMessageRequest{Session: h.session, Text: m.Text, Html: m.HTML, Silent: m.Silent})
	if err != nil {
		return fromHost(err)
	}

	return nil
}

func (h *hostClient) Ask(ctx context.Context, q Question) (*Wait, error) {
	if h.rpc == nil {
		return nil, errNoHost
	}

	state, err := json.Marshal(q.State)
	if err != nil {
		return nil, fmt.Errorf("the question's state: %w", err)
	}

	in := &pluginv1.AskQuestionRequest{Session: h.session, Text: q.Text, Html: q.HTML, Options: q.Options}
	if q.Timeout > 0 {
		in.Timeout = q.Timeout.String()
	}

	out, err := h.rpc.AskQuestion(ctx, in)
	if err != nil {
		return nil, fromHost(err)
	}

	return &Wait{state: state, Deadline: timeOf(out.GetDeadline())}, nil
}

func (h *hostClient) PlaceOrders(ctx context.Context, orders []Order) ([]OrderResult, error) {
	if h.rpc == nil {
		return nil, errNoHost
	}

	in := &pluginv1.PlaceOrdersRequest{Session: h.session}
	for _, o := range orders {
		in.Orders = append(in.Orders, &pluginv1.OrderRequest{
			TokenId: o.TokenID, MarketId: o.MarketID, Side: o.Side, Type: o.Type, Price: string(o.Price),
			Shares: string(o.Shares), Dollars: string(o.Dollars), PostOnly: o.PostOnly, Expires: textOf(o.Expires),
			Trader: o.Trader, TraderLabel: o.TraderLabel,
		})
	}

	out, err := h.rpc.PlaceOrders(ctx, in)
	if err != nil {
		return nil, fromHost(err)
	}

	results := make([]OrderResult, len(out.GetResults()))
	for i, r := range out.GetResults() {
		results[i] = OrderResult{Order: placedOf(r.GetOrder())}
		if msg := r.GetError(); msg != "" {
			results[i].Err = errors.New(msg)
		}
	}

	return results, nil
}

func (h *hostClient) CancelOrders(ctx context.Context, c Cancel) ([]Cancelled, error) {
	if h.rpc == nil {
		return nil, errNoHost
	}

	out, err := h.rpc.CancelOrders(ctx, &pluginv1.CancelOrdersRequest{
		Session: h.session, OrderId: c.OrderID, MarketId: c.MarketID, TokenId: c.TokenID, All: c.All,
	})
	if err != nil {
		return nil, fromHost(err)
	}

	results := make([]Cancelled, len(out.GetResults()))
	for i, r := range out.GetResults() {
		results[i] = Cancelled{Order: placedOf(r.GetOrder()), Reason: r.GetReason()}
	}

	return results, nil
}

func placedOf(o *pluginv1.PlacedOrder) PlacedOrder {
	return PlacedOrder{
		ID: o.GetId(), Mode: o.GetMode(), Status: o.GetStatus(), Reason: o.GetReason(), TokenID: o.GetTokenId(),
		Side: o.GetSide(), Type: o.GetType(), Shares: Decimal(o.GetShares()), Dollars: Decimal(o.GetDollars()),
		Fees: Decimal(o.GetFees()), Price: Decimal(o.GetPrice()),
	}
}
