package pluginhost_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// logLine is a line the server logged, its attributes by key, a group's as []logging.Attr.
type logLine struct {
	level logging.Level
	msg   string
	attrs map[string]any
}

// recordedLogs is a logging core keeping what it is given.
type recordedLogs struct {
	mu    sync.Mutex
	lines []logLine
}

func (r *recordedLogs) find(msg string) (logLine, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	i := slices.IndexFunc(r.lines, func(l logLine) bool { return l.msg == msg })
	if i < 0 {
		return logLine{}, false
	}

	return r.lines[i], true
}

type logCore struct {
	logs *recordedLogs
	with []logging.Attr
}

func (c logCore) Enabled(context.Context, logging.Level) bool { return true }

func (c logCore) Log(_ context.Context, level logging.Level, msg string, attrs ...logging.Attr) {
	line := logLine{level: level, msg: msg, attrs: map[string]any{}}
	for _, a := range append(slices.Clone(c.with), attrs...) {
		line.attrs[a.Key] = a.Value
	}

	c.logs.mu.Lock()
	c.logs.lines = append(c.logs.lines, line)
	c.logs.mu.Unlock()
}

func (c logCore) With(attrs ...logging.Attr) logging.Core {
	return logCore{logs: c.logs, with: append(slices.Clone(c.with), attrs...)}
}

func (c logCore) WithGroup(string) logging.Core { return c }

// point is a metric's record: what a counter added or a histogram observed.
type point struct {
	value  float64
	labels metrics.Labels
}

// recordedMetrics is a metrics manager keeping the definitions it registers and the
// points recorded, by name.
type recordedMetrics struct {
	mu     sync.Mutex
	defs   map[string]metrics.Definition
	points map[string][]point
}

func newRecordedMetrics() *recordedMetrics {
	return &recordedMetrics{defs: map[string]metrics.Definition{}, points: map[string][]point{}}
}

func (m *recordedMetrics) register(def metrics.Definition) *recorder {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := def.Namespace + "_" + def.Subsystem + "_" + def.Name
	m.defs[name] = def

	return &recorder{m: m, name: name}
}

func (m *recordedMetrics) of(name string) (metrics.Definition, []point) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.defs[name], slices.Clone(m.points[name])
}

func (m *recordedMetrics) RegisterCounter(d metrics.CounterDefinition) (metrics.Counter, error) {
	return m.register(d.Definition), nil
}

func (m *recordedMetrics) RegisterGauge(d metrics.GaugeDefinition) (metrics.Gauge, error) {
	return m.register(d.Definition), nil
}

func (m *recordedMetrics) RegisterHistogram(d metrics.HistogramDefinition) (metrics.Histogram, error) {
	return m.register(d.Definition), nil
}

func (m *recordedMetrics) Handler() http.Handler { return http.NotFoundHandler() }

// recorder is a counter, gauge and histogram at once, recording what it is given.
type recorder struct {
	m    *recordedMetrics
	name string
}

func (r *recorder) add(v float64, labels metrics.Labels) error {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()

	r.m.points[r.name] = append(r.m.points[r.name], point{value: v, labels: labels.Clone()})

	return nil
}

func (r *recorder) Inc(l metrics.Labels) error                { return r.add(1, l) }
func (r *recorder) Dec(l metrics.Labels) error                { return r.add(-1, l) }
func (r *recorder) Add(v float64, l metrics.Labels) error     { return r.add(v, l) }
func (r *recorder) Sub(v float64, l metrics.Labels) error     { return r.add(-v, l) }
func (r *recorder) Set(v float64, l metrics.Labels) error     { return r.add(v, l) }
func (r *recorder) Observe(v float64, l metrics.Labels) error { return r.add(v, l) }

// credentials are the owner's credentials, for the host to read from.
type credentials struct {
	mu    sync.Mutex
	asked [][]string // the types each read asked for
}

// stored are alice's: a token of the plugin's type, a header, and one gone.
var stored = map[domain.CredentialID]struct {
	typ    string
	values map[string]string
}{
	"cred-1": {"plugin.echo.token", map[string]string{"token": "tok_1"}},
	"cred-2": {"plugin.echo.token", map[string]string{"token": "tok_2"}},
	"cred-h": {"http.header", map[string]string{"name": "Authorization", "value": "Bearer x", "token": "header"}},
}

func (c *credentials) Values(_ context.Context, owner domain.UserID, id domain.CredentialID, types []string) (string, map[string]string, error) {
	c.mu.Lock()
	c.asked = append(c.asked, types)
	c.mu.Unlock()

	s, ok := stored[id]
	if !ok || owner != "alice" {
		return "", nil, domain.ErrNotFound
	}

	if !slices.Contains(types, s.typ) {
		return "", nil, domain.ErrInvalidInput
	}

	return s.typ, s.values, nil
}

// store is a plugin store in memory, held to its size as Postgres's is.
type store struct {
	mu     sync.Mutex
	values map[ports.PluginNode]map[string]json.RawMessage
}

func newStore() *store {
	return &store{values: map[ports.PluginNode]map[string]json.RawMessage{}}
}

func (s *store) Get(_ context.Context, n ports.PluginNode, key string) (json.RawMessage, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.values[n][key]

	return v, ok, nil
}

func (s *store) Set(_ context.Context, n ports.PluginNode, key string, value json.RawMessage, max int, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	used := len(key) + len(value)
	for k, v := range s.values[n] {
		if k != key {
			used += len(k) + len(v)
		}
	}

	if used > max {
		return domain.ErrStoreFull
	}

	if s.values[n] == nil {
		s.values[n] = map[string]json.RawMessage{}
	}

	s.values[n][key] = value

	return nil
}

func (s *store) Delete(_ context.Context, n ports.PluginNode, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.values[n], key)

	return nil
}

func (s *store) Keys(_ context.Context, n ports.PluginNode, prefix, after string, limit int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var keys []string

	for k := range s.values[n] {
		if strings.HasPrefix(k, prefix) && k > after {
			keys = append(keys, k)
		}
	}

	slices.Sort(keys)

	return keys[:min(len(keys), limit)], nil
}

// data is Polymarket's data for the tests: an event of rain, an open market and a closed
// one, and alice's wallets. It keeps the filters it is asked with.
type data struct {
	mu      sync.Mutex
	history ports.PriceHistoryFilter
	trades  ports.TradeFilter
}

var (
	at     = time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	trader = domain.WalletAddress("0x00000000000000000000000000000000000000aa")
	aliceW = domain.WalletAddress("0x00000000000000000000000000000000000000bb")
	dec    = decimal.RequireFromString
	monday = domain.Market{
		ID: "0xm1", Question: "Will it rain Monday?", Status: domain.MarketOpen, AcceptingOrders: true,
		TickSize: dec("0.01"), Volume24h: dec("1234.5678"), EndsAt: at.Add(72 * time.Hour),
		Outcomes: []domain.Outcome{{TokenID: "tok-yes", Label: "Yes", Price: dec("0.42")}, {TokenID: "tok-no", Label: "No", Price: dec("0.58")}},
	}
	sunday = domain.Market{ID: "0xm0", Question: "Did it rain Sunday?", Status: domain.MarketClosed}
)

func (d *data) ListEvents(context.Context, ports.EventFilter) (ports.Page[domain.Event], error) {
	return ports.Page[domain.Event]{Items: []domain.Event{{Title: "Rain in Paris?", Slug: "rain-paris", Markets: []domain.Market{sunday, monday}}}}, nil
}

func (d *data) GetEvent(context.Context, domain.EventID) (domain.Event, error) {
	return domain.Event{}, domain.ErrNotFound
}

func (d *data) GetEventBySlug(context.Context, string) (domain.Event, error) {
	return domain.Event{}, domain.ErrNotFound
}

func (d *data) OrderBooks(_ context.Context, ids ...domain.TokenID) ([]domain.OrderBook, error) {
	if ids[0] != "tok-yes" {
		return nil, nil
	}

	return []domain.OrderBook{{
		MarketID: "0xm1", TokenID: "tok-yes", TickSize: dec("0.01"), At: at,
		Bids: []domain.Level{{Price: dec("0.41"), Size: dec("100")}}, Asks: []domain.Level{{Price: dec("0.43"), Size: dec("50")}},
	}}, nil
}

func (d *data) PriceHistory(_ context.Context, f ports.PriceHistoryFilter) (ports.Page[domain.PricePoint], error) {
	d.mu.Lock()
	d.history = f
	d.mu.Unlock()

	return ports.Page[domain.PricePoint]{Items: []domain.PricePoint{
		{At: at.Add(2 * time.Minute), Price: dec("0.44")}, {At: at, Price: dec("0.40")}, {At: at.Add(time.Minute), Price: dec("0.42")},
	}}, nil
}

func (d *data) Trades(_ context.Context, f ports.TradeFilter) (ports.Page[domain.Trade], error) {
	d.mu.Lock()
	d.trades = f
	d.mu.Unlock()

	return ports.Page[domain.Trade]{Items: []domain.Trade{
		{MarketID: "0xm1", TokenID: "tok-yes", Side: domain.SideBuy, Price: dec("0.42"), Size: dec("10"), Wallet: trader, At: at},
	}}, nil
}

func (d *data) Positions(_ context.Context, f ports.PositionFilter) (ports.Page[domain.Position], error) {
	return ports.Page[domain.Position]{Items: []domain.Position{
		{Wallet: f.Wallet, MarketID: "0xm1", TokenID: "tok-yes", Outcome: "Yes", Size: dec("25"), Value: dec("10.50")},
	}}, nil
}

func (d *data) ListForUser(_ context.Context, user domain.UserID) ([]domain.Wallet, error) {
	if user != "alice" {
		return nil, nil
	}

	return []domain.Wallet{{ID: "w-1", UserID: "alice", Label: "Main", Address: aliceW}}, nil
}
