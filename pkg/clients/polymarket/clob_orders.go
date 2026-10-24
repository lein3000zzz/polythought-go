package polymarket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Placing, reading back and cancelling an account's orders (3p1, D190), as
// @polymarket/client does: checked against the bodies it sends and the answers the docs
// give (D196). An order's ID is its hash, known before it is sent, so a send that gets no
// answer is settled by looking the order up, and sent once more only when the CLOB does
// not have it (D194).

var _ ClobTradingClient = (*DefaultClobClient)(nil)

const (
	opPostOrder          = "post_order"
	opPostOrders         = "post_orders"
	opOrder              = "order"
	opClobTrades         = "trades"
	opCancelOrder        = "cancel_order"
	opCancelOrders       = "cancel_orders"
	opCancelAll          = "cancel_all"
	opCancelMarketOrders = "cancel_market_orders"
	opHeartbeat          = "heartbeat"
)

const (
	attrOrderID    = "polymarket.order_id"
	attrOrderCount = "polymarket.order_count"
)

// The most orders one request may post, and cancel.
const (
	MaxBatchOrders  = 15
	MaxBatchCancels = 1000
)

// lookupWait is how long a send that got no answer is given before the order is looked
// up: the CLOB may still be taking it.
const lookupWait = 2 * time.Second

// OrderType is how long an order may wait for the book.
type OrderType string

const (
	OrderGTC OrderType = "GTC" // rests until filled or cancelled
	OrderGTD OrderType = "GTD" // rests until filled, cancelled or its expiration
	OrderFOK OrderType = "FOK" // fills wholly at once, or not at all
	OrderFAK OrderType = "FAK" // fills what the book has at once; the rest is cancelled
)

// OrderPost is a signed order as POST /order takes it.
type OrderPost struct {
	Order    SignedOrder
	Exchange Exchange // the order was signed for: its ID is a hash on its domain
	Type     OrderType
	PostOnly bool // GTC and GTD only: refused rather than matched at once
}

// ID is the order's ID on the CLOB: its hash (D194).
func (p OrderPost) ID() (string, error) {
	return p.Order.Hash(Polygon, p.Exchange)
}

func (p OrderPost) check() error {
	switch p.Type {
	case OrderGTC, OrderGTD, OrderFOK, OrderFAK:
	default:
		return fmt.Errorf("polymarket: no order type %q", p.Type)
	}

	switch {
	case p.PostOnly && p.Type != OrderGTC && p.Type != OrderGTD:
		return errors.New("polymarket: only GTC and GTD orders can be post-only")
	case (p.Type == OrderGTD) != (p.Order.Expiration > 0):
		return errors.New("polymarket: a GTD order has an expiration, and no other order does")
	}

	return nil
}

// sendOrder is POST /order's body, as @polymarket/client's createSendOrderPayload makes
// it: postOnly only when it is.
type sendOrder struct {
	DeferExec bool        `json:"deferExec"`
	Order     SignedOrder `json:"order"`
	OrderType OrderType   `json:"orderType"`
	Owner     string      `json:"owner"`
	PostOnly  bool        `json:"postOnly,omitempty"`
}

// PostStatus is how an order stands once the CLOB took it.
type PostStatus string

const (
	PostLive    PostStatus = "live"    // resting on the book
	PostMatched PostStatus = "matched" // matched at once, wholly or in part; a FAK's rest cancelled
	// PostDelayed is held in the market's matching delay, and cannot be cancelled; then
	// it matches, rests unmatched, or is refused.
	PostDelayed PostStatus = "delayed"
	// PostUnmatched rests on the book, unmatched after its delay. The official client
	// counts it refused; the docs, placed.
	PostUnmatched PostStatus = "unmatched"
	// PostDone is an order found after a send got no answer that is no longer on the
	// book: what it matched is in its trades.
	PostDone PostStatus = "done"
)

// PostedOrder is an order the CLOB took.
type PostedOrder struct {
	ID     string
	Status PostStatus

	// MakingAmount and TakingAmount are what matched at once, as the CLOB gives them:
	// the official client reads decimals, the API reference shows millionths. The trades
	// are what counts.
	MakingAmount, TakingAmount OptDecimal

	TradeIDs          []string
	TransactionHashes []string // no longer given for FAK and FOK orders

	// Found is the order as the CLOB lists it, when it was looked up (D194) rather than
	// answered.
	Found *OpenOrder
}

// PostResult is how one order of a batch went: placed, or Err an *OrderRefusedError or
// an *OrderUncertainError.
type PostResult struct {
	Order PostedOrder
	Err   error
}

// RefusalCode says why the CLOB refused an order, as far as its words tell.
type RefusalCode string

const (
	RefusedBalance         RefusalCode = "balance"          // not enough pUSD or shares, or allowance
	RefusedTick            RefusalCode = "tick"             // the price is off the market's tick
	RefusedMinSize         RefusalCode = "min_size"         // under the market's least order
	RefusedDuplicate       RefusalCode = "duplicate"        // the same order was placed before
	RefusedPostOnlyCross   RefusalCode = "post_only_cross"  // a post-only order would match at once
	RefusedExpiration      RefusalCode = "expiration"       // a GTD's expiration too near or past
	RefusedMarketNotReady  RefusalCode = "market_not_ready" // the market takes no orders yet
	RefusedFOKNotFilled    RefusalCode = "fok_not_filled"   // the book could not fill all of it
	RefusedFAKNotFilled    RefusalCode = "fak_not_filled"   // the book had nothing to match
	RefusedCloseOnly       RefusalCode = "close_only"       // the address or its region may only close
	RefusedBanned          RefusalCode = "banned"
	RefusedTradingDisabled RefusalCode = "trading_disabled"
	RefusedCancelOnly      RefusalCode = "cancel_only"    // only cancels are taken for now
	RefusedPostOnlyMode    RefusalCode = "post_only_mode" // only post-only orders for now: RetryAfter
	RefusedRestarting      RefusalCode = "restarting"     // the engine is restarting (425)
	RefusedRateLimited     RefusalCode = "rate_limited"
	RefusedCredentials     RefusalCode = "credentials" // the order's owner or signer is not the API key's
	RefusedInvalid         RefusalCode = "invalid"     // the CLOB could not take the order as sent
	RefusedOther           RefusalCode = "other"
)

// OrderRefusedError is the CLOB refusing an order: it is not on the book.
type OrderRefusedError struct {
	ID         string // the order's, when known
	Code       RefusalCode
	Message    string        // the CLOB's own words
	StatusCode int           // the HTTP status; 200 for one refused in its answer
	RetryAfter time.Duration // when the CLOB says when to try again
}

func (e *OrderRefusedError) Error() string {
	return fmt.Sprintf("polymarket: order refused (%s): %s", e.Code, e.Message)
}

// OrderUncertainError is an order sent without an answer and not found after: it may yet
// reach the book. It is to be looked up by its ID before anything else.
type OrderUncertainError struct {
	ID  string
	Err error
}

func (e *OrderUncertainError) Error() string {
	return fmt.Sprintf("polymarket: order %s may or may not be placed: %v", e.ID, e.Err)
}

func (e *OrderUncertainError) Unwrap() error { return e.Err }

// placedOrRefused is the RED outcome of placing: a refusal is an answer about the order,
// not a failure of the service.
func placedOrRefused(err error) bool {
	var refused *OrderRefusedError

	return succeeded(err) || errors.As(err, &refused)
}

// PostOrder places an order. Sent again, it is found rather than placed twice: the CLOB
// refuses the duplicate, and the one it has is looked up.
func (c *DefaultClobClient) PostOrder(ctx context.Context, creds L2Credentials, p OrderPost) (_ PostedOrder, err error) {
	done := c.kit.Start(ctx, opPostOrder)
	defer func() { done(placedOrRefused(err)) }()

	return tracing.InScope(ctx, c.scope, opPostOrder, func(ctx context.Context, span trace.Span) (PostedOrder, error) {
		span.SetAttributes(attribute.String(attrAddress, creds.Address))

		results, err := c.place(ctx, creds, []OrderPost{p}, false)
		if err != nil {
			return PostedOrder{}, c.placeFailed(ctx, opPostOrder, err)
		}

		r := results[0]
		span.SetAttributes(attribute.String(attrOrderID, r.Order.ID))

		if r.Err != nil {
			return PostedOrder{}, c.placeFailed(ctx, opPostOrder, r.Err)
		}

		c.log.Info(ctx, "Placed a Polymarket order", "order_id", r.Order.ID, "status", r.Order.Status)

		return r.Order, nil
	})
}

// PostOrders places 1 to MaxBatchOrders orders at once. The CLOB admits all of them or
// none; then each is placed or refused on its own.
func (c *DefaultClobClient) PostOrders(ctx context.Context, creds L2Credentials, ps []OrderPost) (_ []PostResult, err error) {
	done := c.kit.Start(ctx, opPostOrders)
	defer func() { done(placedOrRefused(err)) }()

	return tracing.InScope(ctx, c.scope, opPostOrders, func(ctx context.Context, span trace.Span) ([]PostResult, error) {
		span.SetAttributes(attribute.String(attrAddress, creds.Address), attribute.Int(attrOrderCount, len(ps)))

		if len(ps) == 0 || len(ps) > MaxBatchOrders {
			return nil, fmt.Errorf("polymarket: a batch is 1 to %d orders, not %d", MaxBatchOrders, len(ps))
		}

		results, err := c.place(ctx, creds, ps, true)
		if err != nil {
			return nil, c.placeFailed(ctx, opPostOrders, err)
		}

		for _, r := range results {
			if r.Err != nil {
				_ = c.placeFailed(ctx, opPostOrders, r.Err)
			}
		}

		c.log.Info(ctx, "Placed a batch of Polymarket orders", "orders", len(ps))

		return results, nil
	})
}

// placeFailed logs an order not placed: a refusal is routine, an order in doubt is not.
func (c *DefaultClobClient) placeFailed(ctx context.Context, op string, err error) error {
	var refused *OrderRefusedError
	if errors.As(err, &refused) {
		c.log.Info(ctx, "Polymarket refused an order", "operation", op, "order_id", refused.ID,
			"code", refused.Code, "reason", refused.Message)

		return err
	}

	return failed(ctx, c.log, op, err)
}

// place sends the orders, one to POST /order or a batch to /orders, and settles those
// whose send got no answer (D194): found by their IDs, or sent once more. Its error is
// for orders not sent at all.
func (c *DefaultClobClient) place(ctx context.Context, creds L2Credentials, ps []OrderPost, batch bool) ([]PostResult, error) {
	ids := make([]string, len(ps))

	for i, p := range ps {
		if err := p.check(); err != nil {
			return nil, err
		}

		id, err := p.ID()
		if err != nil {
			return nil, err
		}

		ids[i] = id
	}

	results := make([]PostResult, len(ps))
	pending := make([]int, len(ps))

	for i := range ps {
		pending[i] = i
	}

	for round := 0; len(pending) > 0; round++ {
		answers, err := c.send(ctx, creds, ps, ids, pending, batch)

		var unanswered *SendError
		if err != nil && !errors.As(err, &unanswered) {
			if round == 0 {
				return nil, err
			}

			// Sent before without an answer, and not found then: still in doubt.
			return inDoubt(results, ids, pending, err), nil
		}

		// Those without an answer, or that the CLOB has already, are looked up: it says
		// what it has.
		var find []int

		for n, i := range pending {
			results[i] = PostResult{Order: PostedOrder{ID: ids[i]}}

			if unanswered == nil && !duplicated(answers[n]) {
				results[i] = answers[n]
				continue
			}

			find = append(find, i)
		}

		if len(find) > 0 {
			// The CLOB may still be taking what it did not answer.
			if err := sleep(ctx, c.lookupWait); err != nil {
				return inDoubt(results, ids, find, err), nil
			}
		}

		var again []int

		for _, i := range find {
			found, err := c.order(ctx, creds, ids[i])
			r := results[i]

			switch {
			case err == nil:
				results[i] = PostResult{Order: foundOrder(found)}
			case errors.Is(err, ErrNotFound) && unanswered != nil && round == 0:
				again = append(again, i)
			case errors.Is(err, ErrNotFound) && unanswered == nil:
				results[i] = PostResult{Order: r.Order, Err: &OrderUncertainError{ID: ids[i], Err: errors.New("refused as a duplicate, yet not found")}}
			case errors.Is(err, ErrNotFound):
				results[i] = PostResult{Order: r.Order, Err: &OrderUncertainError{ID: ids[i], Err: errors.New("sent twice without an answer, and not found")}}
			default:
				results[i] = PostResult{Order: r.Order, Err: &OrderUncertainError{ID: ids[i], Err: errors.Join(unanswered, err)}}
			}
		}

		pending = again
	}

	return results, nil
}

// duplicated is whether the CLOB refused the order as one it has.
func duplicated(r PostResult) bool {
	var refused *OrderRefusedError

	return errors.As(r.Err, &refused) && refused.Code == RefusedDuplicate
}

// inDoubt marks the orders at which as maybe placed, for why.
func inDoubt(results []PostResult, ids []string, which []int, why error) []PostResult {
	for _, i := range which {
		results[i] = PostResult{Order: PostedOrder{ID: ids[i]}, Err: &OrderUncertainError{ID: ids[i], Err: why}}
	}

	return results
}

// foundOrder is an order looked up, as placing it would have answered.
func foundOrder(o OpenOrder) PostedOrder {
	status := PostDone

	switch strings.ToUpper(o.Status) {
	case "LIVE":
		status = PostLive
	case "MATCHED":
		status = PostMatched
	case "DELAYED":
		status = PostDelayed
	case "UNMATCHED":
		status = PostUnmatched
	}

	return PostedOrder{ID: o.ID, Status: status, TradeIDs: o.AssociateTrades, Found: &o}
}

// send posts the pending orders once admitted, waiting out 425 and 429 as reads do: how
// each went, in pending's order. Its error is a *SendError when the CLOB may have them
// without having answered.
func (c *DefaultClobClient) send(ctx context.Context, creds L2Credentials, ps []OrderPost, ids []string, pending []int, batch bool) ([]PostResult, error) {
	bodies := make([]sendOrder, len(pending))
	for n, i := range pending {
		bodies[n] = sendOrder{Order: ps[i].Order, OrderType: ps[i].Type, Owner: creds.APIKey, PostOnly: ps[i].PostOnly}
	}

	path, payload, err := "/order", []byte(nil), error(nil)
	if batch {
		path = "/orders"
		payload, err = json.Marshal(bodies)
	} else {
		payload, err = json.Marshal(bodies[0])
	}

	if err != nil {
		return nil, fmt.Errorf("polymarket: encode %s: %w", path, err)
	}

	for attempt := 0; ; attempt++ {
		resp, err := c.trade.once(ctx, http.MethodPost, path, nil, payload, creds)
		if err != nil {
			return nil, err
		}

		if (resp.status == http.StatusTooEarly || resp.status == http.StatusTooManyRequests) && attempt < c.trade.maxRetries {
			if err := c.trade.backOff(ctx, path, resp, attempt); err != nil {
				return nil, err
			}

			continue
		}

		trace.SpanFromContext(ctx).SetAttributes(
			attribute.Int(attrAttempts, attempt+1),
			attribute.Int(attrStatusCode, resp.status),
		)

		pendingIDs := make([]string, len(pending))
		for n, i := range pending {
			pendingIDs[n] = ids[i]
		}

		return answered(path, resp, pendingIDs, batch)
	}
}

// orderAnswer is the CLOB's answer for one order.
type orderAnswer struct {
	Success            bool       `json:"success"`
	ErrorMsg           string     `json:"errorMsg"`
	OrderID            string     `json:"orderID"`
	Status             string     `json:"status"`
	MakingAmount       OptDecimal `json:"makingAmount"`
	TakingAmount       OptDecimal `json:"takingAmount"`
	TradeIDs           []string   `json:"tradeIDs"`
	TransactionsHashes []string   `json:"transactionsHashes"`
}

// clobError is how the CLOB and its gateway word a refused request.
type clobError struct {
	Error             string `json:"error"`
	ErrorMsg          string `json:"error_msg"`
	Message           string `json:"message"`
	Code              string `json:"code"`
	RetryAfterSeconds int    `json:"retry_after_seconds"`
}

// answered reads what the CLOB said to the orders with ids: each order's answer, a
// refusal of them all, or a *SendError when it said nothing that settles them.
func answered(path string, resp response, ids []string, batch bool) ([]PostResult, error) {
	noAnswer := func(err error) error { return &SendError{Method: http.MethodPost, Path: path, Err: err} }

	switch {
	case resp.status == http.StatusUnauthorized:
		return nil, tracing.Expected(fmt.Errorf("%w: %s", ErrUnauthorized, truncate(resp.body, maxErrorBodyLen)), path)
	case resp.status >= 200 && resp.status <= 299:
		answers := make([]orderAnswer, 1)

		var err error
		if batch {
			err = json.Unmarshal(resp.body, &answers)
		} else {
			err = json.Unmarshal(resp.body, &answers[0])
		}

		if err != nil || len(answers) != len(ids) {
			return nil, noAnswer(fmt.Errorf("an answer it could not read (%d for %d orders, %v): %s",
				len(answers), len(ids), err, truncate(resp.body, maxErrorBodyLen)))
		}

		return matchAnswers(answers, ids), nil
	}

	var body clobError
	readable := json.Unmarshal(resp.body, &body) == nil
	text := firstOf(body.Error, body.ErrorMsg, body.Message)

	// A server error, a gateway's, or the gateway saying it does not know: the orders
	// may be on the book or not.
	if resp.status == http.StatusInternalServerError || resp.status == http.StatusBadGateway ||
		resp.status == http.StatusGatewayTimeout || body.Code == "submission_outcome_unknown" ||
		(resp.status == http.StatusServiceUnavailable && (!readable || text == "")) {
		return nil, noAnswer(fmt.Errorf("http %d: %s", resp.status, truncate(resp.body, maxErrorBodyLen)))
	}

	if text == "" {
		text = truncate(resp.body, maxErrorBodyLen)
	}

	refusal := &OrderRefusedError{
		Code: refusalCode(resp.status, text, body.Code), Message: text, StatusCode: resp.status,
		RetryAfter: retryAfter(resp.header, body.RetryAfterSeconds),
	}

	results := make([]PostResult, len(ids))
	for i, id := range ids {
		r := *refusal
		r.ID = id
		results[i] = PostResult{Order: PostedOrder{ID: id}, Err: &r}
	}

	return results, nil
}

// matchAnswers pairs answers with the orders they answer: by ID, then those without one
// (refused) in order. The CLOB promises an answer an order, not their order.
func matchAnswers(answers []orderAnswer, ids []string) []PostResult {
	results := make([]PostResult, len(ids))
	used := make([]bool, len(answers))
	matched := make([]bool, len(ids))

	for i, id := range ids {
		for n, a := range answers {
			if !used[n] && a.OrderID != "" && strings.EqualFold(a.OrderID, id) {
				results[i], used[n], matched[i] = a.result(id), true, true
				break
			}
		}
	}

	n := 0
	for i, id := range ids {
		if matched[i] {
			continue
		}

		for used[n] {
			n++
		}

		results[i], used[n] = answers[n].result(id), true
	}

	return results
}

// result is how the order with id went, as its answer says. Taken means success, no
// error, an ID and a status the CLOB places with.
func (a orderAnswer) result(id string) PostResult {
	status := PostStatus(a.Status)

	taken := status == PostLive || status == PostMatched || status == PostDelayed || status == PostUnmatched
	if a.Success && a.ErrorMsg == "" && a.OrderID != "" && taken {
		return PostResult{Order: PostedOrder{
			ID: a.OrderID, Status: status, MakingAmount: a.MakingAmount, TakingAmount: a.TakingAmount,
			TradeIDs: a.TradeIDs, TransactionHashes: a.TransactionsHashes,
		}}
	}

	text := a.ErrorMsg
	if text == "" {
		text = "refused without a reason"
		if a.Status != "" {
			text += " (" + a.Status + ")"
		}
	}

	return PostResult{
		Order: PostedOrder{ID: id},
		Err:   &OrderRefusedError{ID: id, Code: refusalCode(http.StatusOK, text, ""), Message: text, StatusCode: http.StatusOK},
	}
}

// refusalCode reads why from the CLOB's words, its code and status, as the docs and the
// official client word them.
func refusalCode(status int, text, code string) RefusalCode {
	t := strings.ToLower(text)

	switch {
	case code == "post_only_mode" || strings.Contains(t, "post-only mode"):
		return RefusedPostOnlyMode
	case strings.Contains(t, "not enough balance") || strings.Contains(t, "allowance is not enough"):
		return RefusedBalance
	case strings.Contains(t, "minimum tick size") || code == "invalid_order_price":
		return RefusedTick
	case strings.Contains(t, "lower than the minimum") || code == "order_too_small":
		return RefusedMinSize
	case strings.Contains(t, "duplicated"):
		return RefusedDuplicate
	case strings.Contains(t, "invalid post-only order") || strings.Contains(t, "crosses book"):
		return RefusedPostOnlyCross
	case strings.Contains(t, "invalid expiration") || code == "invalid_order_expiration":
		return RefusedExpiration
	case strings.Contains(t, "not yet ready"):
		return RefusedMarketNotReady
	case strings.Contains(t, "fok orders"):
		return RefusedFOKNotFilled
	case strings.Contains(t, "fak order"):
		return RefusedFAKNotFilled
	case strings.Contains(t, "closed only mode") || code == "restricted_region_close_only":
		return RefusedCloseOnly
	case strings.Contains(t, "address banned"):
		return RefusedBanned
	case strings.Contains(t, "trading is currently disabled"):
		return RefusedTradingDisabled
	case strings.Contains(t, "cancel-only"):
		return RefusedCancelOnly
	case status == http.StatusTooEarly:
		return RefusedRestarting
	case status == http.StatusTooManyRequests || code == "rate_limited" || strings.Contains(t, "rate limit"):
		return RefusedRateLimited
	case strings.Contains(t, "of the api key") || code == "api_key_mismatch" || code == "invalid_signer":
		return RefusedCredentials
	case strings.Contains(t, "invalid") || strings.HasPrefix(code, "invalid_"):
		return RefusedInvalid
	default:
		return RefusedOther
	}
}

// retryAfter is when the CLOB says to try again: its Retry-After header, else its body's.
func retryAfter(header http.Header, seconds int) time.Duration {
	if s, err := strconv.Atoi(header.Get("Retry-After")); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}

	return time.Duration(max(seconds, 0)) * time.Second
}

func firstOf(texts ...string) string {
	for _, t := range texts {
		if t != "" {
			return t
		}
	}

	return ""
}

// Order is one of the account's orders by its ID, cancelled and matched ones too.
func (c *DefaultClobClient) Order(ctx context.Context, creds L2Credentials, id string) (_ OpenOrder, err error) {
	done := c.kit.Start(ctx, opOrder)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opOrder, func(ctx context.Context, span trace.Span) (OpenOrder, error) {
		span.SetAttributes(attribute.String(attrAddress, creds.Address), attribute.String(attrOrderID, id))

		o, err := c.order(ctx, creds, id)
		if err != nil {
			return OpenOrder{}, failed(ctx, c.log, opOrder, err)
		}

		return o, nil
	})
}

func (c *DefaultClobClient) order(ctx context.Context, creds L2Credentials, id string) (OpenOrder, error) {
	if id == "" || strings.ContainsAny(id, "/?#") {
		return OpenOrder{}, fmt.Errorf("polymarket: %q is no order ID", id)
	}

	var o *OpenOrder
	if err := c.core.getL2(ctx, "/data/order/"+id, nil, creds, &o); err != nil {
		return OpenOrder{}, err
	}

	// An order it does not have may come back as nothing at all rather than a 404.
	if o == nil || o.ID == "" {
		return OpenOrder{}, tracing.Expected(ErrNotFound, "/data/order")
	}

	return *o, nil
}

func (c *DefaultClobClient) Trades(ctx context.Context, creds L2Credentials, q ClobTradesQuery) (_ Page[ClobTrade], err error) {
	done := c.kit.Start(ctx, opClobTrades)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opClobTrades, func(ctx context.Context, span trace.Span) (Page[ClobTrade], error) {
		span.SetAttributes(attribute.String(attrAddress, creds.Address), attribute.String(attrCursor, q.Cursor))

		cursor := q.Cursor
		if cursor == "" {
			cursor = clobFirstCursor
		}

		params := url.Values{"next_cursor": {cursor}}
		setString(params, "id", q.ID)
		setString(params, "maker_address", q.MakerAddress)
		setString(params, "market", q.Market)
		setString(params, "asset_id", q.AssetID)

		if q.After > 0 {
			params.Set("after", strconv.FormatInt(q.After, 10))
		}

		if q.Before > 0 {
			params.Set("before", strconv.FormatInt(q.Before, 10))
		}

		var resp struct {
			Data       []ClobTrade `json:"data"`
			NextCursor string      `json:"next_cursor"`
		}

		if err := c.core.getL2(ctx, "/data/trades", params, creds, &resp); err != nil {
			return Page[ClobTrade]{}, failed(ctx, c.log, opClobTrades, err)
		}

		next := resp.NextCursor
		if next == clobEndCursor {
			next = ""
		}

		span.SetAttributes(resultAttrs(len(resp.Data), next)...)
		c.log.Debug(ctx, "Fetched trades", "count", len(resp.Data))

		return Page[ClobTrade]{Items: resp.Data, NextCursor: next}, nil
	})
}

func (c *DefaultClobClient) CancelOrder(ctx context.Context, creds L2Credentials, id string) (Cancellation, error) {
	if id == "" {
		return Cancellation{}, errors.New("polymarket: no order to cancel")
	}

	return c.cancel(ctx, creds, opCancelOrder, "/order", map[string]string{"orderID": id}, 1)
}

func (c *DefaultClobClient) CancelOrders(ctx context.Context, creds L2Credentials, ids []string) (Cancellation, error) {
	if len(ids) == 0 || len(ids) > MaxBatchCancels {
		return Cancellation{}, fmt.Errorf("polymarket: a cancel is 1 to %d orders, not %d", MaxBatchCancels, len(ids))
	}

	return c.cancel(ctx, creds, opCancelOrders, "/orders", ids, len(ids))
}

func (c *DefaultClobClient) CancelAll(ctx context.Context, creds L2Credentials) (Cancellation, error) {
	return c.cancel(ctx, creds, opCancelAll, "/cancel-all", nil, 0)
}

func (c *DefaultClobClient) CancelMarketOrders(ctx context.Context, creds L2Credentials, q CancelMarketQuery) (Cancellation, error) {
	if q.Market == "" && q.AssetID == "" {
		return Cancellation{}, errors.New("polymarket: name a market or an outcome to cancel its orders")
	}

	return c.cancel(ctx, creds, opCancelMarketOrders, "/cancel-market-orders", q, 0)
}

// cancel sends a cancel, retried as reads are: cancelling twice cancels nothing more.
func (c *DefaultClobClient) cancel(ctx context.Context, creds L2Credentials, op, path string, body any, n int) (_ Cancellation, err error) {
	done := c.kit.Start(ctx, op)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, op, func(ctx context.Context, span trace.Span) (Cancellation, error) {
		span.SetAttributes(attribute.String(attrAddress, creds.Address), attribute.Int(attrOrderCount, n))

		var out Cancellation
		if err := c.trade.do(ctx, http.MethodDelete, path, nil, body, creds, &out); err != nil {
			return Cancellation{}, failed(ctx, c.log, op, err)
		}

		c.log.Info(ctx, "Cancelled Polymarket orders", "operation", op, "canceled", len(out.Canceled),
			"not_canceled", len(out.NotCanceled))

		return out, nil
	})
}

// HeartbeatMismatchError is a heartbeat with an ID the CLOB did not expect: Expected is
// the one it did. A heartbeat it refused does not keep the switch from firing.
type HeartbeatMismatchError struct {
	Expected string
}

func (e *HeartbeatMismatchError) Error() string {
	return "polymarket: heartbeat ID not the one expected"
}

// Heartbeat sends one heartbeat, as @polymarket/client does: once, not retried, as the
// next must carry the ID this one gives. Missed for 10 s, the CLOB cancels the API key's
// open orders.
func (c *DefaultClobClient) Heartbeat(ctx context.Context, creds L2Credentials, last string) (_ string, err error) {
	done := c.kit.Start(ctx, opHeartbeat)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opHeartbeat, func(ctx context.Context, span trace.Span) (string, error) {
		span.SetAttributes(attribute.String(attrAddress, creds.Address))

		payload, err := json.Marshal(map[string]string{"heartbeat_id": last})
		if err != nil {
			return "", err
		}

		resp, err := c.trade.once(ctx, http.MethodPost, "/v1/heartbeats", nil, payload, creds)
		if err != nil {
			return "", failed(ctx, c.log, opHeartbeat, err)
		}

		var body struct {
			HeartbeatID string `json:"heartbeat_id"`
			ErrorMsg    string `json:"error_msg"`
		}

		if resp.status == http.StatusBadRequest && json.Unmarshal(resp.body, &body) == nil && body.HeartbeatID != "" {
			return "", &HeartbeatMismatchError{Expected: body.HeartbeatID}
		}

		if err := decodeResponse("/v1/heartbeats", resp.status, resp.body, &body); err != nil {
			return "", failed(ctx, c.log, opHeartbeat, err)
		}

		return body.HeartbeatID, nil
	})
}
