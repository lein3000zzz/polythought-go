package pluginhost_test

import (
	"context"
	"net/http"
	"slices"
	"sync"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
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
