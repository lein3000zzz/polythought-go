package pluginhost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lein3000zzz/polythought-go/internal/adapters/sandbox"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/pkg/plugin"
	pluginv1 "github.com/lein3000zzz/polythought-go/pkg/plugin/v1"
)

const (
	tracerName = "github.com/lein3000zzz/polythought-go/internal/adapters/pluginhost"

	opFetch         = "pluginhost.Fetch"
	opCredential    = "pluginhost.GetCredential"
	opGetValue      = "pluginhost.GetValue"
	opSetValue      = "pluginhost.SetValue"
	opDeleteValue   = "pluginhost.DeleteValue"
	opListKeys      = "pluginhost.ListKeys"
	opSearchMarkets = "pluginhost.SearchMarkets"
	opOrderBook     = "pluginhost.GetOrderBook"
	opPriceHistory  = "pluginhost.GetPriceHistory"
	opTrades        = "pluginhost.ListTrades"
	opPositions     = "pluginhost.ListPositions"
	opWallets       = "pluginhost.ListWallets"
	opMessage       = "pluginhost.SendMessage"
	opAsk           = "pluginhost.AskQuestion"
	opPlaceOrders   = "pluginhost.PlaceOrders"
	opCancelOrders  = "pluginhost.CancelOrders"
)

// Deps are what the host serves plugins from (5b).
type Deps struct {
	// Util logs, counts and traces what plugins do, and what they report; nil: nothing.
	Util *observability.Utility

	// Credentials reads the owner's credentials for their nodes; nil: there are none.
	Credentials ports.CredentialReader

	// Store keeps what plugin nodes store; nil: nothing can be.
	Store ports.PluginStore

	// Data is what plugins may read.
	Data Data

	// Telegram reaches the workflow's owner; Trading trades for the workflow.
	Telegram Telegram
	Trading  Trading

	// AllowPrivate lets fetches reach addresses inside the server's network, as the
	// webhooks' setting does; asked on every connection, nil means never.
	AllowPrivate func() bool
	// Proxy is the outbound proxy fetches to public addresses go through, asked on every
	// connection; empty, or nil, for none (D428).
	Proxy func() string

	// Limits bound what one run may ask, read as each run starts; nil, or a limit at
	// zero: DefaultLimits'.
	Limits func() Limits

	// Settings are a plugin's own settings, by its ID, as each run gets them (D329); nil:
	// none.
	Settings func(plugin string) map[string]any

	// Sandbox starts plugins' processes under the sandbox (D337): nil where there is none,
	// NoSandbox saying why. Unsandboxed, asked as each process starts, lets them start
	// without one there (D338); nil means never.
	Sandbox     *sandbox.Launcher
	NoSandbox   error
	Unsandboxed func() bool

	// Caps are what each plugin may take, by its ID (D341, D343), read as its process
	// starts and each time it is measured; nil: DefaultCaps.
	Caps func(plugin string) Caps
}

// Limits bound what one run may ask of the host (D293).
type Limits struct {
	// HostCalls are the calls a node's run may make, or a trigger's in a minute: code's
	// data requests setting.
	HostCalls int

	FetchTime time.Duration // a fetch's, from dialling to its body's last byte
	FetchBody int           // bytes of a fetch's request body, and of its response's

	// StoreBytes are what one node may keep in its store, its keys and values (D303).
	StoreBytes int
}

var DefaultLimits = Limits{HostCalls: 100, FetchTime: 10 * time.Second, FetchBody: 1 << 20, StoreBytes: 1 << 20}

// orDefaults is l with DefaultLimits' for what it leaves at zero.
func (l Limits) orDefaults() Limits {
	if l.HostCalls <= 0 {
		l.HostCalls = DefaultLimits.HostCalls
	}

	if l.FetchTime <= 0 {
		l.FetchTime = DefaultLimits.FetchTime
	}

	if l.FetchBody <= 0 {
		l.FetchBody = DefaultLimits.FetchBody
	}

	if l.StoreBytes <= 0 {
		l.StoreBytes = DefaultLimits.StoreBytes
	}

	return l
}

// What a run's logs, metrics and spans are held to: they are not calls, so not counted
// with them, but no plugin may flood the server's.
const (
	maxTelemetry = 1000     // log lines, metric records and spans a run, or a trigger's minute
	maxText      = 8 << 10  // bytes of a log line, a field or an attribute, cut past it
	maxFields    = 32       // fields of a log line, attributes of a span: the rest dropped
	maxLine      = 16 << 10 // bytes of a log line, its message and fields, or of a span's attributes (P4, P11)
	maxSpans     = 100      // spans a run has going at once (P11)
	maxSeries    = 1000     // label sets one metric is recorded with
	maxLabel     = 128      // bytes of a label's value
)

// maxInFlight are the calls a run may have going at once: more wait their turn, so one
// run cannot make the server hold many fetches' bodies at once (P12).
const maxInFlight = 4

// api serves one plugin's process its HostService: a call must name a session of one of
// its runs going on, and ask what its permissions allow, within the run's limits.
type api struct {
	pluginv1.UnimplementedHostServiceServer

	deps  Deps
	log   logging.Logger
	kit   *redkit.Kit
	scope tracing.Scope
	http  *http.Client

	// Set once the plugin has described itself, before it has any run.
	id      string
	name    string // as messages are marked with it (D308)
	perms   permissions
	metrics map[string]*metric

	mu       sync.Mutex
	sessions map[string]*session
}

func newAPI(deps Deps) (*api, error) {
	if deps.Util == nil {
		deps.Util = observability.NoopUtility()
	}

	if deps.Limits == nil {
		deps.Limits = func() Limits { return DefaultLimits }
	}

	log := deps.Util.Logger.With("component", "plugin")

	kit, err := redkit.Register(redkit.Params{
		Manager: deps.Util.MetricsManager, Logger: log, Subsystem: "pluginhost", Buckets: redkit.MediumLongBuckets,
		Component: "pluginhost",
	})
	if err != nil {
		return nil, fmt.Errorf("pluginhost: register metrics: %w", err)
	}

	a := &api{
		deps: deps, log: log, kit: kit,
		scope:    tracing.Scope{Manager: deps.Util.TracerManager, TracerName: tracerName},
		sessions: map[string]*session{},
	}
	a.http = a.fetchClient()

	return a, nil
}

// bind makes the api the plugin's, once its manifest is read and checked.
func (a *api) bind(m plugin.Manifest) error {
	perms, err := permissionsOf(m.Permissions)
	if err != nil {
		return err
	}

	a.id, a.name, a.perms, a.log = m.ID, m.Name, perms, a.log.With("plugin", m.ID)
	a.metrics = a.registerMetrics(m)

	return nil
}

// session is one run's standing with the host: a node's run, or a trigger's while it
// runs. Its token is what the plugin's calls name.
type session struct {
	// ctx is the run's, its own once open, ended as it closes: the plugin's spans go under
	// its span, and its calls end with it (P15).
	ctx     context.Context
	cancel  context.CancelFunc
	rc      engine.RunContext
	trigger bool
	typ     engine.NodeType
	trades  bool // its manifest marks it trades (D309)
	// params are each input item's settings, worked out; a trigger's are under the zero
	// ref.
	params map[engine.ItemRef]engine.Params
	limits Limits
	now    func() time.Time
	slots  chan struct{} // its calls in flight (P12)

	mu        sync.Mutex
	calls     int
	telemetry int
	window    time.Time // a trigger's minute, from its first call in it
	over      bool      // a node's run asked past its calls
	spans     map[uint64]*openSpan
	lastSpan  uint64

	question *question // the one the run asked, which it waits on
	asking   bool      // its question is being sent
	settled  bool      // the plugin answered the run: a question sent now is not waited on
}

// openSpan is a span of the plugin's going on, and the bytes of attributes it may still
// take (P11).
type openSpan struct {
	span trace.Span
	room int
}

// settle is the question the run asked, if it did, once the plugin has answered the run:
// one sent after is not waited on, so its call closes it (D311, P15).
func (s *session) settle() *question {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.settled = true

	return s.question
}

// open registers a run's session, until closed: its token is what the plugin's calls
// name.
func (a *api) open(s *session) (token string, closed func(), err error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("a session: %w", err)
	}

	token = hex.EncodeToString(raw)
	s.ctx, s.cancel = context.WithCancel(s.ctx)
	s.limits = a.deps.Limits().orDefaults()
	s.now = time.Now
	s.slots = make(chan struct{}, maxInFlight)
	s.spans = map[uint64]*openSpan{}

	a.mu.Lock()
	a.sessions[token] = s
	a.mu.Unlock()

	return token, func() {
		a.mu.Lock()
		delete(a.sessions, token)
		a.mu.Unlock()

		s.cancel()
		s.endSpans()
	}, nil
}

// session is the run a call names, refused when it is none of this plugin's going on.
func (a *api) session(token string) (*session, error) {
	a.mu.Lock()
	s := a.sessions[token]
	a.mu.Unlock()

	if s == nil {
		return nil, status.Error(codes.PermissionDenied, "no run of the plugin's has that session: it has ended, or never was")
	}

	return s, nil
}

// count counts a call against the run's limit: a node's run that goes past it fails, the
// call refused, whatever the plugin makes of that. A trigger's calls are counted by the
// minute.
func (s *session) count() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.rollWindow()

	if s.calls++; s.calls <= s.limits.HostCalls {
		return nil
	}

	if s.trigger {
		return status.Errorf(codes.ResourceExhausted, "past the %d calls a trigger may make a minute", s.limits.HostCalls)
	}

	s.over = true

	return status.Errorf(codes.ResourceExhausted, "past the %d calls a run may make", s.limits.HostCalls)
}

// report counts a log line, metric record or span against the run's cap on them.
func (s *session) report() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.rollWindow()

	if s.telemetry++; s.telemetry > maxTelemetry {
		return status.Errorf(codes.ResourceExhausted, "past the %d log lines, metric records and spans a run may report", maxTelemetry)
	}

	return nil
}

// rollWindow starts a trigger's next minute of calls. Under s.mu.
func (s *session) rollWindow() {
	if !s.trigger {
		return
	}

	if now := s.now(); s.window.IsZero() || now.Sub(s.window) >= time.Minute {
		s.window, s.calls, s.telemetry = now, 0, 0
	}
}

// overLimit says the run asked past its calls.
func (s *session) overLimit() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.over
}

// work is the context a call does its work in: the session's, so it ends as the run's
// session closes, whatever the plugin left going (P15), and its spans join the node's;
// ended too if the plugin gives up on the call.
func (s *session) work(call context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(s.ctx)
	stop := context.AfterFunc(call, cancel)

	return ctx, func() {
		stop()
		cancel()
	}
}

// endSpans ends the spans the plugin left open, once its run is done.
func (s *session) endSpans() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, open := range s.spans {
		open.span.SetStatus(otelcodes.Error, "the run ended before the plugin ended the span")
		open.span.End()
		delete(s.spans, id)
	}
}

// acquire takes a place for a call among the session's maxInFlight, waiting for one while
// the call lasts (P12).
func (s *session) acquire(ctx context.Context) error {
	select {
	case s.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ended(ctx.Err())
	}
}

func (s *session) release() { <-s.slots }

// call runs one of a session's calls, counted against its limit, in the session's
// context, no more than maxInFlight at once, timed and traced as op.
func call[T any](ctx context.Context, a *api, token, op string, fn func(context.Context, *session, trace.Span) (T, error)) (_ T, err error) {
	var none T

	s, err := a.session(token)
	if err != nil {
		return none, err
	}

	if err := s.count(); err != nil {
		return none, err
	}

	ctx, done := s.work(ctx)
	defer done()

	if err := s.acquire(ctx); err != nil {
		return none, err
	}
	defer s.release()

	finish := a.kit.Start(ctx, op)
	defer func() { finish(err == nil) }()

	return tracing.InScope(ctx, a.scope, op, func(ctx context.Context, span trace.Span) (T, error) {
		span.SetAttributes(attribute.String("plugin.id", a.id))
		return fn(ctx, s, span)
	})
}

// told are the errors a plugin hears in their own words: the domain's, about what it
// asked, which say nothing of the server's insides (P13).
var told = []error{
	domain.ErrUnavailable, domain.ErrCredentialsRejected, domain.ErrLiveTradingOff, domain.ErrWalletCannotTrade,
	domain.ErrNotEnoughCash, domain.ErrNotEnoughShares, domain.ErrNotOpen, domain.ErrTelegramUnavailable,
	domain.ErrTelegramBlocked, domain.ErrTelegramNotLinked,
}

// failure is what a plugin hears of an error doing what (P13): the context's end, not
// found, or one of told, in their own words; of anything else only that it failed, the
// error logged with the run's, as storeError does.
func (a *api) failure(ctx context.Context, s *session, what string, err error) (codes.Code, string) {
	if e := ended(err); e != nil {
		return status.Code(e), what + ": " + status.Convert(e).Message()
	}

	if errors.Is(err, domain.ErrNotFound) {
		return codes.NotFound, what + ": not found"
	}

	if i := slices.IndexFunc(told, func(t error) bool { return errors.Is(err, t) }); i >= 0 {
		return codes.Unavailable, what + ": " + told[i].Error()
	}

	a.log.Warn(ctx, "Plugin's call failed", append(s.logArgs(), "call", what, logging.Err(err))...)

	return codes.Unavailable, what + " failed"
}

// sourceError is why a call failed, as the plugin hears it.
func (a *api) sourceError(ctx context.Context, s *session, what string, err error) error {
	code, msg := a.failure(ctx, s, what, err)
	return status.Error(code, msg)
}

// ended is the call's end with its context, when err is that, in words of its own: the
// error carrying it may say more of the server's insides (P13). Nil otherwise.
func ended(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, context.Canceled.Error())
	}

	return nil
}

// logArgs are a run's, for a log line about it.
func (s *session) logArgs() []any {
	args := []any{"workflow_id", s.rc.WorkflowID, "node_id", s.rc.NodeID}
	if s.rc.ExecutionID != "" {
		args = append(args, "execution_id", s.rc.ExecutionID)
	}

	return args
}

// GetCredential gives the values of the credential that one of the node's credential
// settings names, for the item the plugin names (D289): nothing the owner did not pick
// for that node.
func (a *api) GetCredential(ctx context.Context, req *pluginv1.GetCredentialRequest) (*pluginv1.GetCredentialResponse, error) {
	return call(ctx, a, req.GetSession(), opCredential, func(ctx context.Context, s *session, span trace.Span) (*pluginv1.GetCredentialResponse, error) {
		span.SetAttributes(attribute.String("plugin.param", req.GetParam()))

		spec, ok := credentialParam(s.typ, req.GetParam())
		if !ok {
			return nil, status.Errorf(codes.InvalidArgument, "%q is none of the node's credential settings", req.GetParam())
		}

		params, err := s.paramsFor(req.GetItem())
		if err != nil {
			return nil, err
		}

		id, _ := params[spec.Name].(string)
		if id == "" {
			return nil, status.Errorf(codes.NotFound, "no credential is picked in %q", spec.Name)
		}

		if a.deps.Credentials == nil {
			return nil, status.Error(codes.Unavailable, "credentials are not available here")
		}

		typ, values, err := a.deps.Credentials.Values(ctx, s.rc.OwnerID, domain.CredentialID(id), spec.CredentialTypes)
		if err != nil {
			return nil, credentialError(spec.Name, err)
		}

		a.log.Debug(ctx, "Plugin read a credential", append(s.logArgs(), "param", spec.Name, "credential_id", id)...)

		// The plugin's own types by the name it gave them, as its settings name them.
		short := strings.TrimPrefix(typ, qualified(a.id, ""))

		return &pluginv1.GetCredentialResponse{Credential: &pluginv1.Credential{Type: short, Values: values}}, nil
	})
}

// credentialParam is the node type's credential setting named name.
func credentialParam(typ engine.NodeType, name string) (engine.ParamSpec, bool) {
	for _, p := range typ.Params {
		if p.Name == name && p.Kind == engine.ParamCredential {
			return p, true
		}
	}

	return engine.ParamSpec{}, false
}

// paramsFor are the settings of the item ref names: an input item of a node's run, or
// none for a trigger, whose settings are its own.
func (s *session) paramsFor(ref *pluginv1.ItemRef) (engine.Params, error) {
	switch {
	case s.trigger && ref != nil:
		return nil, status.Error(codes.InvalidArgument, "a trigger has no input items: its settings are its own")
	case s.trigger:
		return s.params[engine.ItemRef{}], nil
	case ref == nil:
		return nil, status.Error(codes.InvalidArgument, "which input item's settings? name it")
	}

	params, ok := s.params[engine.ItemRef{Port: ref.GetPort(), Index: int(ref.GetIndex())}]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "the run has no input item %s %d", ref.GetPort(), ref.GetIndex())
	}

	return params, nil
}

func credentialError(param string, err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return status.Errorf(codes.NotFound, "the credential in %q is gone", param)
	case errors.Is(err, domain.ErrInvalidInput):
		return status.Errorf(codes.InvalidArgument, "%s: %v", param, err)
	default:
		return status.Errorf(codes.Internal, "the credential in %q could not be read", param)
	}
}

// clip cuts text past maxText bytes, at a rune's start.
func clip(text string) string { return cut(text, maxText) }

// ellipsis marks text cut.
const ellipsis = "…"

// cut cuts text past most bytes, at a rune's start, marked: most bytes and the ellipsis at
// most.
func cut(text string, most int) string {
	if len(text) <= most {
		return text
	}

	at := max(most, 0)
	for at > 0 && !isRuneStart(text[at]) {
		at--
	}

	return text[:at] + ellipsis
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
