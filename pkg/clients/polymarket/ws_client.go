package polymarket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var _ MarketStreamClient = (*DefaultMarketStreamClient)(nil)

var ErrStreamStarted = errors.New("polymarket: stream already started")

const (
	streamReadLimit  = 32 << 20
	streamWriteLimit = 5 * time.Second
	streamSubsystem  = "polymarket_stream"
)

const (
	opStreamConnect     = "stream_connect"
	opStreamSubscribe   = "stream_subscribe"
	opStreamUnsubscribe = "stream_unsubscribe"
)

// DefaultMarketStreamClient keeps one market channel connection alive and fans its
// events into a buffered channel. When the buffer is full the newest event is dropped
// and counted, so a slow consumer never stalls the socket.
type DefaultMarketStreamClient struct {
	cfg        StreamConfig
	events     chan MarketEvent
	dropped    metrics.Counter
	reconnects metrics.Counter
	kit        *redkit.Kit
	scope      tracing.Scope
	log        logging.Logger
	dialer     *http.Client
	started    atomic.Bool

	// mu guards assets and conn, and serialises subscription writes so the
	// initial subscribe and later operations reach the server in order.
	mu     sync.Mutex
	assets map[string]struct{}
	conn   *websocket.Conn
}

type subscribeMessage struct {
	Type           string   `json:"type,omitempty"`
	Operation      string   `json:"operation,omitempty"`
	AssetIDs       []string `json:"assets_ids"`
	InitialDump    bool     `json:"initial_dump,omitempty"`
	CustomFeatures bool     `json:"custom_feature_enabled"`
}

func NewMarketStreamClient(util *observability.Utility, cfg StreamConfig) (*DefaultMarketStreamClient, error) {
	cfg = cfg.withDefaults()
	log := util.Logger.With("component", "polymarket.stream")

	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    log,
		Subsystem: streamSubsystem,
		Buckets:   redkit.MediumBuckets,
		Component: "polymarket stream",
	})
	if err != nil {
		return nil, fmt.Errorf("polymarket: register stream metrics: %w", err)
	}

	dropped, err := util.MetricsManager.RegisterCounter(metrics.CounterDefinition{
		Definition: metrics.Definition{
			Namespace:      metrics.DefaultNamespace,
			Subsystem:      streamSubsystem,
			Name:           "events_dropped_total",
			Help:           "Market events dropped because the consumer buffer was full.",
			VariableLabels: []string{"event_type"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("polymarket: register stream metrics: %w", err)
	}

	reconnects, err := util.MetricsManager.RegisterCounter(metrics.CounterDefinition{
		Definition: metrics.Definition{
			Namespace: metrics.DefaultNamespace,
			Subsystem: streamSubsystem,
			Name:      "reconnects_total",
			Help:      "Market channel reconnect attempts.",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("polymarket: register stream metrics: %w", err)
	}

	return &DefaultMarketStreamClient{
		cfg:        cfg,
		events:     make(chan MarketEvent, cfg.BufferSize),
		dropped:    dropped,
		reconnects: reconnects,
		kit:        kit,
		scope:      tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		log:        log,
		dialer:     &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)},
		assets:     make(map[string]struct{}),
	}, nil
}

// Events is closed when Run returns.
func (c *DefaultMarketStreamClient) Events() <-chan MarketEvent {
	return c.events
}

// Run connects, keeps the connection alive and reconnects with backoff until ctx is done.
// It may be called once and returns nil on cancellation.
func (c *DefaultMarketStreamClient) Run(ctx context.Context) error {
	if !c.started.CompareAndSwap(false, true) {
		return ErrStreamStarted
	}
	defer close(c.events)

	backoff := c.cfg.ReconnectMin

	for {
		received := c.session(ctx)
		if ctx.Err() != nil {
			return nil
		}

		if received {
			backoff = c.cfg.ReconnectMin
		}

		wait := backoff/2 + rand.N(backoff/2+1)
		c.log.Info(ctx, "Reconnecting market stream", "wait", wait)

		if err := sleep(ctx, wait); err != nil {
			return nil
		}

		backoff = min(backoff*2, c.cfg.ReconnectMax)
		c.incCounter(ctx, c.reconnects, nil)
	}
}

// Subscribe adds assets. They are sent now if connected and on every reconnect.
func (c *DefaultMarketStreamClient) Subscribe(ctx context.Context, assetIDs ...string) error {
	return c.update(ctx, opStreamSubscribe, "subscribe", assetIDs, func(id string) { c.assets[id] = struct{}{} })
}

func (c *DefaultMarketStreamClient) Unsubscribe(ctx context.Context, assetIDs ...string) error {
	return c.update(ctx, opStreamUnsubscribe, "unsubscribe", assetIDs, func(id string) { delete(c.assets, id) })
}

func (c *DefaultMarketStreamClient) update(
	ctx context.Context,
	op, operation string,
	assetIDs []string,
	apply func(string),
) (err error) {
	if len(assetIDs) == 0 {
		return nil
	}

	done := c.kit.Start(ctx, op)
	defer func() { done(err == nil) }()

	return c.scope.WithSpan(ctx, op, func(ctx context.Context, span trace.Span) error {
		span.SetAttributes(tokenAttrs(assetIDs)...)

		c.mu.Lock()
		defer c.mu.Unlock()

		for _, id := range assetIDs {
			apply(id)
		}

		connected := c.conn != nil
		span.SetAttributes(attribute.Int(attrAssets, len(c.assets)), attribute.Bool("polymarket.connected", connected))

		if !connected {
			c.log.Debug(ctx, "Stream assets updated while disconnected", "operation", operation, "assets", len(assetIDs))
			return nil
		}

		if err := c.writeJSON(ctx, c.conn, subscribeMessage{
			Operation:      operation,
			AssetIDs:       assetIDs,
			InitialDump:    operation == "subscribe",
			CustomFeatures: c.cfg.CustomFeatures,
		}); err != nil {
			return failed(ctx, c.log, op, fmt.Errorf("polymarket: %s: %w", operation, err))
		}

		c.log.Debug(ctx, "Stream assets updated", "operation", operation, "assets", len(assetIDs))

		return nil
	})
}

// session runs one connection until it fails, logging why. received reports whether any
// message arrived, which resets the reconnect backoff.
func (c *DefaultMarketStreamClient) session(ctx context.Context) (received bool) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	conn, err := c.connect(ctx)
	if err != nil {
		return false
	}
	defer conn.CloseNow()
	defer c.detach()

	go c.keepAlive(ctx, conn)

	for {
		readCtx, readCancel := context.WithTimeout(ctx, 3*c.cfg.PingInterval)
		typ, data, err := conn.Read(readCtx)
		readCancel()

		if err != nil {
			if ctx.Err() != nil {
				_ = conn.Close(websocket.StatusNormalClosure, "")
				return received
			}

			c.log.Warn(ctx, "Market stream disconnected", logging.Err(err))

			return received
		}

		received = true

		if typ != websocket.MessageText || len(data) == 0 || (data[0] != '{' && data[0] != '[') {
			continue // PONG and other plain-text replies
		}

		c.dispatch(ctx, data)
	}
}

// connect dials and subscribes. The session itself is not a span: it can live for hours.
func (c *DefaultMarketStreamClient) connect(ctx context.Context) (_ *websocket.Conn, err error) {
	done := c.kit.Start(ctx, opStreamConnect)
	defer func() { done(err == nil) }()

	return tracing.InScope(ctx, c.scope, opStreamConnect, func(ctx context.Context, span trace.Span) (*websocket.Conn, error) {
		span.SetAttributes(attribute.String(attrURL, c.cfg.URL))

		conn, _, err := websocket.Dial(ctx, c.cfg.URL, &websocket.DialOptions{HTTPClient: c.dialer})
		if err != nil {
			return nil, failed(ctx, c.log, opStreamConnect, fmt.Errorf("polymarket: dial: %w", err))
		}

		conn.SetReadLimit(streamReadLimit)

		assets, err := c.attach(ctx, conn)
		if err != nil {
			_ = conn.CloseNow()
			return nil, failed(ctx, c.log, opStreamConnect, err)
		}

		span.SetAttributes(attribute.Int(attrAssets, assets))

		return conn, nil
	})
}

// attach publishes the connection and sends the current asset set as one atomic step.
func (c *DefaultMarketStreamClient) attach(ctx context.Context, conn *websocket.Conn) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	assetIDs := slices.AppendSeq(make([]string, 0, len(c.assets)), maps.Keys(c.assets))

	err := c.writeJSON(ctx, conn, subscribeMessage{
		Type:           "market",
		AssetIDs:       assetIDs,
		InitialDump:    true,
		CustomFeatures: c.cfg.CustomFeatures,
	})
	if err != nil {
		return 0, fmt.Errorf("polymarket: subscribe: %w", err)
	}

	c.conn = conn
	c.log.Info(ctx, "Market stream connected", "assets", len(assetIDs))

	return len(assetIDs), nil
}

func (c *DefaultMarketStreamClient) detach() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.conn = nil
}

func (c *DefaultMarketStreamClient) keepAlive(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(c.cfg.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			writeCtx, cancel := context.WithTimeout(ctx, streamWriteLimit)
			err := conn.Write(writeCtx, websocket.MessageText, []byte("PING"))
			cancel()

			if err != nil {
				return // the read loop sees the broken connection and reconnects
			}
		}
	}
}

func (c *DefaultMarketStreamClient) writeJSON(ctx context.Context, conn *websocket.Conn, msg any) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, streamWriteLimit)
	defer cancel()

	return conn.Write(ctx, websocket.MessageText, payload)
}

// dispatch handles both shapes the channel sends: a JSON array (initial snapshot) or one object.
func (c *DefaultMarketStreamClient) dispatch(ctx context.Context, data []byte) {
	if data[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(data, &batch); err != nil {
			c.log.Warn(ctx, "Undecodable market batch", logging.Err(err))
			return
		}

		for _, raw := range batch {
			c.dispatchOne(ctx, raw)
		}

		return
	}

	c.dispatchOne(ctx, data)
}

func (c *DefaultMarketStreamClient) dispatchOne(ctx context.Context, raw []byte) {
	event, err := decodeMarketEvent(raw)
	if err != nil {
		c.log.Warn(ctx, "Undecodable market event", logging.Err(err))
		return
	}

	select {
	case c.events <- event:
	default:
		c.incCounter(ctx, c.dropped, metrics.Labels{"event_type": string(event.EventType())})
	}
}

func (c *DefaultMarketStreamClient) incCounter(ctx context.Context, counter metrics.Counter, labels metrics.Labels) {
	if err := counter.Inc(labels); err != nil {
		c.log.Warn(ctx, "Failed to increment metric", logging.Err(err))
	}
}

func decodeMarketEvent(raw []byte) (MarketEvent, error) {
	var head struct {
		Type MarketEventType `json:"event_type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}

	switch head.Type {
	case EventBook:
		return decodeAs[BookEvent](raw)
	case EventPriceChange:
		return decodeAs[PriceChangeEvent](raw)
	case EventLastTradePrice:
		return decodeAs[LastTradePriceEvent](raw)
	case EventTickSizeChange:
		return decodeAs[TickSizeChangeEvent](raw)
	case EventBestBidAsk:
		return decodeAs[BestBidAskEvent](raw)
	case EventMarketResolved:
		return decodeAs[MarketResolvedEvent](raw)
	default:
		return UnknownEvent{Type: head.Type, Raw: slices.Clone(raw)}, nil
	}
}

func decodeAs[T MarketEvent](raw []byte) (MarketEvent, error) {
	var event T
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, err
	}

	return event, nil
}
