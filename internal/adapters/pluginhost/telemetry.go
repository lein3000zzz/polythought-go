package pluginhost

import (
	"context"
	"errors"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lein3000zzz/polythought-go/pkg/plugin"
	pluginv1 "github.com/lein3000zzz/polythought-go/pkg/plugin/v1"
)

var levels = map[string]logging.Level{
	"debug": logging.LevelDebug, "info": logging.LevelInfo, "warn": logging.LevelWarn, "error": logging.LevelError,
}

// Log logs a line of the plugin's, with its name and the run's, its own fields apart
// under "fields" so they cannot pass for the host's (D294): maxLine bytes of them at most,
// with its message (P4).
func (a *api) Log(ctx context.Context, req *pluginv1.LogRequest) (*pluginv1.LogResponse, error) {
	s, err := a.session(req.GetSession())
	if err != nil {
		return nil, err
	}

	level, ok := levels[req.GetLevel()]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "level %q: debug, info, warn or error", req.GetLevel())
	}

	if err := s.report(); err != nil {
		return nil, err
	}

	ctx, done := s.work(ctx)
	defer done()

	msg := clip(req.GetMessage())

	fields := make([]logging.Attr, 0, min(len(req.GetFields()), maxFields))
	fit(req.GetFields(), maxLine-len(msg), func(k, v string) { fields = append(fields, logging.Attr{Key: k, Value: v}) })

	args := s.logArgs()
	if len(fields) > 0 {
		args = append(args, logging.Group("fields", fields...))
	}

	a.log.Log(ctx, level, msg, args...)

	return &pluginv1.LogResponse{}, nil
}

// metric is one the plugin's manifest declares, registered as plugin_<id>_<name> with
// the plugin's ID as a label, so two plugins' never mix.
type metric struct {
	kind      string
	labels    []string
	counter   metrics.Counter
	histogram metrics.Histogram

	mu     sync.Mutex
	series map[string]bool // label sets recorded, their values joined
}

// registerMetrics registers the manifest's metrics. One that cannot be, its name taken
// with other labels, say, is logged and left out: recording it fails.
func (a *api) registerMetrics(m plugin.Manifest) map[string]*metric {
	out := make(map[string]*metric, len(m.Metrics))

	for _, mt := range m.Metrics {
		def := metrics.Definition{
			Namespace: metrics.DefaultNamespace, Subsystem: "plugin_" + strings.ReplaceAll(m.ID, "-", "_"), Name: mt.Name,
			Help: mt.Help, ConstLabels: metrics.Labels{"plugin": m.ID}, VariableLabels: mt.Labels,
		}

		reg := &metric{kind: mt.Kind, labels: mt.Labels, series: map[string]bool{}}

		var err error

		switch mt.Kind {
		case metricCounter:
			reg.counter, err = a.deps.Util.MetricsManager.RegisterCounter(metrics.CounterDefinition{Definition: def})
		case metricHistogram:
			reg.histogram, err = a.deps.Util.MetricsManager.RegisterHistogram(metrics.HistogramDefinition{Definition: def, Buckets: mt.Buckets})
		}

		if err != nil {
			a.log.Warn(context.Background(), "Plugin's metric not registered", "metric", mt.Name, logging.Err(err))
			continue
		}

		out[mt.Name] = reg
	}

	return out
}

// RecordMetric adds to a counter, or observes in a histogram, of the manifest's, with
// exactly the labels it declares, and no more label sets than maxSeries.
func (a *api) RecordMetric(_ context.Context, req *pluginv1.RecordMetricRequest) (*pluginv1.RecordMetricResponse, error) {
	s, err := a.session(req.GetSession())
	if err != nil {
		return nil, err
	}

	m := a.metrics[req.GetName()]
	if m == nil {
		return nil, status.Errorf(codes.NotFound, "the manifest declares no metric %q", req.GetName())
	}

	labels := req.GetLabels()
	if len(labels) != len(m.labels) {
		return nil, status.Errorf(codes.InvalidArgument, "%s takes the labels %v", req.GetName(), m.labels)
	}

	values := make([]string, 0, len(m.labels))

	for _, l := range m.labels {
		v, ok := labels[l]
		switch {
		case !ok:
			return nil, status.Errorf(codes.InvalidArgument, "%s takes the labels %v", req.GetName(), m.labels)
		case len(v) > maxLabel:
			return nil, status.Errorf(codes.InvalidArgument, "label %s: longer than %d bytes", l, maxLabel)
		}

		values = append(values, v)
	}

	v := req.GetValue()

	switch {
	case math.IsNaN(v) || math.IsInf(v, 0):
		return nil, status.Errorf(codes.InvalidArgument, "%s takes a finite value", req.GetName())
	case m.kind == metricCounter && v < 0:
		return nil, status.Errorf(codes.InvalidArgument, "%s is a counter: it only goes up", req.GetName())
	}

	if err := s.report(); err != nil {
		return nil, err
	}

	if err := m.admit(strings.Join(values, "\x00")); err != nil {
		return nil, err
	}

	if m.counter != nil {
		err = m.counter.Add(v, labels)
	} else {
		err = m.histogram.Observe(v, labels)
	}

	if err != nil {
		return nil, status.Errorf(codes.Internal, "%s not recorded", req.GetName())
	}

	return &pluginv1.RecordMetricResponse{}, nil
}

// admit lets a label set in, as long as the metric has fewer than maxSeries.
func (m *metric) admit(series string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.series[series] && len(m.series) >= maxSeries {
		return status.Errorf(codes.ResourceExhausted, "past the %d label sets a metric may have", maxSeries)
	}

	m.series[series] = true

	return nil
}

// StartSpan starts a span of the plugin's, under one of the run's it has not ended or the
// node's own, so the run's trace shows what the plugin did (D294): maxSpans going at once,
// each with maxLine bytes of attributes, at start and end together (P11).
func (a *api) StartSpan(_ context.Context, req *pluginv1.StartSpanRequest) (*pluginv1.StartSpanResponse, error) {
	s, err := a.session(req.GetSession())
	if err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.GetName())
	if name == "" || len(name) > maxLabel {
		return nil, status.Errorf(codes.InvalidArgument, "a span's name: 1 to %d bytes", maxLabel)
	}

	if err := s.report(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.spans) >= maxSpans {
		return nil, status.Errorf(codes.ResourceExhausted, "past the %d spans a run may have going at once: end some", maxSpans)
	}

	ctx := s.ctx

	if id := req.GetParent(); id != 0 {
		parent, ok := s.spans[id]
		if !ok {
			return nil, status.Errorf(codes.InvalidArgument, "no span %d of the run's is going on", id)
		}

		ctx = trace.ContextWithSpan(ctx, parent.span)
	}

	// The node's span's provider: the plugin's spans join its trace.
	tracer := trace.SpanFromContext(s.ctx).TracerProvider().Tracer(tracerName)

	attrs, room := attributes(req.GetAttributes(), maxLine)
	_, span := tracer.Start(ctx, name, trace.WithAttributes(append(attrs, attribute.String("plugin.id", a.id))...))

	s.lastSpan++
	s.spans[s.lastSpan] = &openSpan{span: span, room: room}

	return &pluginv1.StartSpanResponse{Span: s.lastSpan}, nil
}

// EndSpan ends one of the run's spans, failed with the error the plugin gives.
func (a *api) EndSpan(_ context.Context, req *pluginv1.EndSpanRequest) (*pluginv1.EndSpanResponse, error) {
	s, err := a.session(req.GetSession())
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	open, ok := s.spans[req.GetSpan()]
	delete(s.spans, req.GetSpan())
	s.mu.Unlock()

	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "no span %d of the run's is going on", req.GetSpan())
	}

	span := open.span

	attrs, _ := attributes(req.GetAttributes(), open.room)
	span.SetAttributes(attrs...)

	if msg := req.GetError(); msg != "" {
		msg = clip(msg)
		span.RecordError(errors.New(msg))
		span.SetStatus(otelcodes.Error, msg)
	}

	span.End()

	return &pluginv1.EndSpanResponse{}, nil
}

// attributes are a span's, as fit takes them: room bytes of them, and what room is left.
func attributes(m map[string]string, room int) ([]attribute.KeyValue, int) {
	out := make([]attribute.KeyValue, 0, min(len(m), maxFields))
	left := fit(m, room, func(k, v string) { out = append(out, attribute.String(k, v)) })

	return out, left
}

// fit adds a log line's fields or a span's attributes, by key, maxFields of them at most,
// each key and value cut to maxText, all of them within room bytes, keys and values
// together: a value past it cut to what is left, one with no room left out. It is what
// room is left.
func fit(m map[string]string, room int, add func(k, v string)) int {
	keys := slices.Sorted(maps.Keys(m))

	for _, k := range keys[:min(len(keys), maxFields)] {
		key, value := clip(k), clip(m[k])

		if len(key)+len(value) > room {
			if room-len(key) <= len(ellipsis) {
				continue
			}

			value = cut(value, room-len(key)-len(ellipsis))
		}

		room -= len(key) + len(value)
		add(key, value)
	}

	return room
}
