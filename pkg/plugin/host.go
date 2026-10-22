package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/lein3000zzz/polythought-go/pkg/plugin/v1"
)

// Host is what a node or trigger may ask of Polythought while it runs, in Run.Host: its
// only way out of its process. Each call is checked against the permissions the manifest
// asks for. Fetch and Credential count against the run's limit (a trigger's, a minute):
// past it they fail, and so does the node, whether it minds or not.
type Host interface {
	// Fetch makes an HTTP request to a host a fetch permission names, fetch:api.example.com,
	// or to any public address with fetch:*. Any status is a response, not an error.
	Fetch(ctx context.Context, req Request) (Response, error)

	// Credential is the credential that the setting param names, as the workflow's owner
	// picked it for item: an input item, or the zero Item in a trigger, whose settings are
	// its own.
	Credential(ctx context.Context, param string, item Item) (Credential, error)

	// Metric adds value to the counter, or observes it in the histogram, that the
	// manifest declares as name, with the labels it declares.
	Metric(ctx context.Context, name string, value float64, labels map[string]string) error

	// Span starts a span, under the one ctx carries or else the node's, with attributes;
	// end ends it, failed unless err is nil. The context it returns carries it.
	Span(ctx context.Context, name string, attributes map[string]string) (_ context.Context, end func(err error))

	// Get, Set, Delete and Keys are the node's own store in its workflow, for a node its
	// manifest marks Remembers: values as JSON, keys up to 256 bytes, all within the
	// node's size, 1 MiB unless an admin says. It shows on the node's settings as its
	// memory, and goes with Forget, the node and the workflow.
	//
	// Get decodes the value under key into v, and says whether there was one.
	Get(ctx context.Context, key string, v any) (bool, error)
	Set(ctx context.Context, key string, v any) error
	Delete(ctx context.Context, key string) error
	// Keys are those starting with prefix, in byte order, after after: up to limit,
	// 100 when 0, 1000 at most.
	Keys(ctx context.Context, prefix, after string, limit int) ([]string, error)

	// SearchMarkets, OrderBook, PriceHistory, Trades and Positions read Polymarket's
	// public data, any address's positions among them: no permission.
	//
	// SearchMarkets finds markets by words of their event's title, open ones unless
	// closed: up to limit, 20 when 0, 100 at most.
	SearchMarkets(ctx context.Context, query string, closed bool, limit int) ([]Market, error)
	OrderBook(ctx context.Context, tokenID string) (OrderBook, error)
	PriceHistory(ctx context.Context, q PriceQuery) ([]PricePoint, error)
	Trades(ctx context.Context, q TradeQuery) ([]Trade, error)
	// Positions are an address's open positions: up to limit, 20 when 0, 100 at most.
	Positions(ctx context.Context, address string, limit int) ([]Position, error)

	// Wallets are the workflow's owner's own wallets, with PermWallets.
	Wallets(ctx context.Context) ([]Wallet, error)

	// Message messages the workflow's owner on Telegram, with PermTelegram.
	Message(ctx context.Context, m Message) error

	// Ask asks the owner a question on Telegram, with PermTelegram, once a run, never in
	// a trigger. The node returns the Wait as its error, and its run waits for the
	// answer; its Resume, which it must have, then finishes the run.
	Ask(ctx context.Context, q Question) (*Wait, error)

	// PlaceOrders places 1 to 15 orders for the workflow, as Place order does: paper or
	// live as the workflow trades, within its caps and the kill switch. CancelOrders
	// cancels the workflow's own resting orders. Both take PermOrders, from a node the
	// manifest marks Trades.
	PlaceOrders(ctx context.Context, orders []Order) ([]OrderResult, error)
	CancelOrders(ctx context.Context, c Cancel) ([]Cancelled, error)
}

// Request is an HTTP request for Fetch.
type Request struct {
	Method string // GET when empty
	URL    string
	Header http.Header
	Body   []byte
}

// Response is what Fetch got: the whole body, within the host's cap.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Credential is a credential's values. Type names it as the setting does: the plugin's
// own types by their name, a built-in one by its full name.
type Credential struct {
	Type   string
	Values map[string]string
}

var (
	// ErrNotAllowed is the host refusing a call its permissions do not allow, or one
	// outside the run.
	ErrNotAllowed = errors.New("not allowed")

	// ErrLimit is the host refusing a call past a limit: the run's calls, or the room in
	// the node's store.
	ErrLimit = errors.New("past a limit")
)

// hostError is the host's reason a call failed, matching ErrNotAllowed or ErrLimit by
// its code.
type hostError struct {
	kind error
	msg  string
}

func (e *hostError) Error() string        { return e.msg }
func (e *hostError) Is(target error) bool { return target != nil && target == e.kind }

func fromHost(err error) error {
	s, ok := status.FromError(err)
	if !ok {
		return err
	}

	switch s.Code() {
	case codes.PermissionDenied:
		return &hostError{kind: ErrNotAllowed, msg: s.Message()}
	case codes.ResourceExhausted:
		return &hostError{kind: ErrLimit, msg: s.Message()}
	default:
		return errors.New(s.Message())
	}
}

// errNoHost is what a host that serves nothing answers.
var errNoHost = errors.New("the host serves no calls here")

// logFor bounds a log line's delivery: a logger has no context of its own.
const logFor = 5 * time.Second

// hostClient is a run's Host, over the host's HostService.
type hostClient struct {
	rpc     pluginv1.HostServiceClient // nil: the host serves none
	session string
}

var _ Host = (*hostClient)(nil)

func (h *hostClient) Fetch(ctx context.Context, req Request) (Response, error) {
	if h.rpc == nil {
		return Response{}, errNoHost
	}

	in := &pluginv1.FetchRequest{Session: h.session, Method: req.Method, Url: req.URL, Body: req.Body}
	for name, values := range req.Header {
		for _, v := range values {
			in.Headers = append(in.Headers, &pluginv1.Header{Name: name, Value: v})
		}
	}

	out, err := h.rpc.Fetch(ctx, in)
	if err != nil {
		return Response{}, fromHost(err)
	}

	res := Response{Status: int(out.GetStatus()), Header: http.Header{}, Body: out.GetBody()}
	for _, hd := range out.GetHeaders() {
		res.Header.Add(hd.GetName(), hd.GetValue())
	}

	return res, nil
}

func (h *hostClient) Credential(ctx context.Context, param string, item Item) (Credential, error) {
	if h.rpc == nil {
		return Credential{}, errNoHost
	}

	in := &pluginv1.GetCredentialRequest{Session: h.session, Param: param}
	if item.ref != nil {
		in.Item = &pluginv1.ItemRef{Port: item.ref.Port, Index: int32(item.ref.Index)} //nolint:gosec // a run's items
	}

	out, err := h.rpc.GetCredential(ctx, in)
	if err != nil {
		return Credential{}, fromHost(err)
	}

	c := out.GetCredential()

	return Credential{Type: c.GetType(), Values: c.GetValues()}, nil
}

func (h *hostClient) Metric(ctx context.Context, name string, value float64, labels map[string]string) error {
	if h.rpc == nil {
		return errNoHost
	}

	_, err := h.rpc.RecordMetric(ctx, &pluginv1.RecordMetricRequest{Session: h.session, Name: name, Value: value, Labels: labels})
	if err != nil {
		return fromHost(err)
	}

	return nil
}

// spanKey carries the span a context is in, as the host numbers it.
type spanKey struct{}

func (h *hostClient) Span(ctx context.Context, name string, attributes map[string]string) (context.Context, func(error)) {
	if h.rpc == nil {
		return ctx, func(error) {}
	}

	parent, _ := ctx.Value(spanKey{}).(uint64)

	out, err := h.rpc.StartSpan(ctx, &pluginv1.StartSpanRequest{Session: h.session, Name: name, Parent: parent, Attributes: attributes})
	if err != nil {
		// Work goes on untraced: a span is no reason to fail it.
		return ctx, func(error) {}
	}

	id := out.GetSpan()

	return context.WithValue(ctx, spanKey{}, id), func(err error) {
		end := &pluginv1.EndSpanRequest{Session: h.session, Span: id}
		if err != nil {
			end.Error = err.Error()
		}

		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), logFor)
		defer cancel()

		_, _ = h.rpc.EndSpan(ctx, end)
	}
}

func (h *hostClient) log(ctx context.Context, level slog.Level, msg string, fields map[string]string) {
	if h.rpc == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), logFor)
	defer cancel()

	// A line the host does not take is lost, as a log line may be.
	_, _ = h.rpc.Log(ctx, &pluginv1.LogRequest{Session: h.session, Level: levelName(level), Message: msg, Fields: fields})
}

func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warn"
	case l >= slog.LevelInfo:
		return "info"
	default:
		return "debug"
	}
}

// logHandler sends a run's log lines to the host, which logs them with the plugin's
// name and the run's: Run.Log.
type logHandler struct {
	host   *hostClient
	attrs  []slog.Attr // with their groups in their keys
	prefix string      // the group new attributes go in, like "a.b."
}

func (h *logHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *logHandler) Handle(ctx context.Context, r slog.Record) error {
	fields := make(map[string]string, len(h.attrs)+r.NumAttrs())
	for _, a := range h.attrs {
		addField(fields, "", a)
	}

	r.Attrs(func(a slog.Attr) bool {
		addField(fields, h.prefix, a)
		return true
	})

	h.host.log(ctx, r.Level, r.Message, fields)

	return nil
}

func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := &logHandler{host: h.host, prefix: h.prefix, attrs: append([]slog.Attr(nil), h.attrs...)}
	for _, a := range attrs {
		a.Key = h.prefix + a.Key
		out.attrs = append(out.attrs, a)
	}

	return out
}

func (h *logHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	return &logHandler{host: h.host, attrs: h.attrs, prefix: h.prefix + name + "."}
}

// addField flattens an attribute into fields, a group's members as group.key.
func addField(fields map[string]string, prefix string, a slog.Attr) {
	v := a.Value.Resolve()

	if v.Kind() == slog.KindGroup {
		inner := prefix
		if a.Key != "" {
			inner += a.Key + "."
		}

		for _, g := range v.Group() {
			addField(fields, inner, g)
		}

		return
	}

	if a.Key == "" {
		return
	}

	fields[prefix+a.Key] = fmt.Sprint(v.Any())
}
