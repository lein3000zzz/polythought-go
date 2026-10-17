package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"github.com/shopspring/decimal"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var _ ActivityStreamClient = (*DefaultActivityStreamClient)(nil)

const (
	activitySubsystem = "polymarket_activity"
	opActivityConnect = "activity_connect"
	activityTopic     = "activity"

	// Activity row types: every participant's side of a transaction, and the taker's
	// order once per transaction.
	ActivityTrades        = "trades"
	ActivityOrdersMatched = "orders_matched"
)

// ActivityStreamClient streams every trade on Polymarket: there is no filter by wallet,
// so whoever reads it picks the wallets out.
type ActivityStreamClient interface {
	// Events is never closed: Run may be called again after it returns.
	Events() <-chan ActivityEvent
	Run(ctx context.Context) error
}

// ActivityEvent is an ActivityTrade or an ActivityConnected.
type ActivityEvent interface {
	activityEvent()
}

// ActivityTrade is one row of the activity topic, a participant's side of one
// transaction. Type ActivityTrades has a row for every participant: the taker's for its
// whole order, at the average price, and one per maker order filled, each from its
// owner's side. ActivityOrdersMatched repeats the taker's row, once per transaction.
type ActivityTrade struct {
	Type            string          `json:"-"`
	ProxyWallet     string          `json:"proxyWallet"` // mixed case, as EIP-55 writes it
	Side            Side            `json:"side"`
	Asset           string          `json:"asset"`
	ConditionID     string          `json:"conditionId"` // empty in some rows: the market is the asset's
	Outcome         string          `json:"outcome"`
	Title           string          `json:"title"`
	Slug            string          `json:"slug"`
	EventSlug       string          `json:"eventSlug"`
	Price           decimal.Decimal `json:"price"`
	Size            decimal.Decimal `json:"size"`
	Timestamp       int64           `json:"timestamp"` // seconds, when matched
	TransactionHash string          `json:"transactionHash"`
	Name            string          `json:"name"`
	Pseudonym       string          `json:"pseudonym"`
}

// ActivityConnected says the stream has just subscribed, again or for the first time:
// trades from before it, while it was down, are not on the stream.
type ActivityConnected struct {
	At time.Time
}

func (ActivityTrade) activityEvent()     {}
func (ActivityConnected) activityEvent() {}

// DefaultActivityStreamClient keeps one connection to the activity topic while it runs
// and fans its rows into a buffered channel; a full buffer drops the newest row, counted.
// A connection that goes quiet is replaced: the stream is busy all the time, and is known
// to freeze while staying open.
type DefaultActivityStreamClient struct {
	cfg        ActivityStreamConfig
	events     chan ActivityEvent
	dropped    metrics.Counter
	reconnects metrics.Counter
	kit        *redkit.Kit
	scope      tracing.Scope
	log        logging.Logger
	dialer     *http.Client
	running    atomic.Bool
}

type activitySubscription struct {
	Topic string `json:"topic"`
	Type  string `json:"type"`
}

type activitySubscribe struct {
	Action        string                 `json:"action"`
	Subscriptions []activitySubscription `json:"subscriptions"`
}

type activityEnvelope struct {
	Topic   string          `json:"topic"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
	Body    *struct {
		Message string `json:"message"`
	} `json:"body"`
}

func NewActivityStreamClient(util *observability.Utility, cfg ActivityStreamConfig) (*DefaultActivityStreamClient, error) {
	cfg = cfg.withDefaults()
	log := util.Logger.With("component", "polymarket.activity")

	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    log,
		Subsystem: activitySubsystem,
		Buckets:   redkit.MediumBuckets,
		Component: "polymarket activity",
	})
	if err != nil {
		return nil, fmt.Errorf("polymarket: register activity metrics: %w", err)
	}

	dropped, err := util.MetricsManager.RegisterCounter(metrics.CounterDefinition{
		Definition: metrics.Definition{
			Namespace: metrics.DefaultNamespace,
			Subsystem: activitySubsystem,
			Name:      "events_dropped_total",
			Help:      "Activity rows dropped because the consumer buffer was full.",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("polymarket: register activity metrics: %w", err)
	}

	reconnects, err := util.MetricsManager.RegisterCounter(metrics.CounterDefinition{
		Definition: metrics.Definition{
			Namespace: metrics.DefaultNamespace,
			Subsystem: activitySubsystem,
			Name:      "reconnects_total",
			Help:      "Activity stream reconnect attempts.",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("polymarket: register activity metrics: %w", err)
	}

	return &DefaultActivityStreamClient{
		cfg:        cfg,
		events:     make(chan ActivityEvent, cfg.BufferSize),
		dropped:    dropped,
		reconnects: reconnects,
		kit:        kit,
		scope:      tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		log:        log,
		dialer:     &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)},
	}, nil
}

func (c *DefaultActivityStreamClient) Events() <-chan ActivityEvent {
	return c.events
}

// Run connects, keeps the connection alive and reconnects with backoff until ctx is done,
// returning nil then. One Run at a time; another may follow once it has returned.
func (c *DefaultActivityStreamClient) Run(ctx context.Context) error {
	if !c.running.CompareAndSwap(false, true) {
		return ErrStreamStarted
	}
	defer c.running.Store(false)

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
		c.log.Info(ctx, "Reconnecting activity stream", "wait", wait)

		if err := sleep(ctx, wait); err != nil {
			return nil
		}

		backoff = min(backoff*2, c.cfg.ReconnectMax)
		c.incCounter(ctx, c.reconnects)
	}
}

// session runs one connection until it fails or goes quiet. received reports whether a
// row arrived, which resets the reconnect backoff.
func (c *DefaultActivityStreamClient) session(ctx context.Context) (received bool) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	conn, err := c.connect(ctx)
	if err != nil {
		return false
	}
	defer func() { _ = conn.CloseNow() }()

	// Said before any row, so whoever reads knows what may be missing up to now.
	select {
	case c.events <- ActivityConnected{At: time.Now()}:
	case <-ctx.Done():
		return false
	}

	go c.keepAlive(ctx, conn)

	for {
		readCtx, readCancel := context.WithTimeout(ctx, c.cfg.Silence)
		typ, data, err := conn.Read(readCtx)
		quiet := readCtx.Err() != nil && ctx.Err() == nil
		readCancel()

		if err != nil {
			switch {
			case ctx.Err() != nil:
				_ = conn.Close(websocket.StatusNormalClosure, "")
			case quiet:
				c.log.Warn(ctx, "Activity stream went quiet", "for", c.cfg.Silence)
			default:
				c.log.Warn(ctx, "Activity stream disconnected", logging.Err(err))
			}

			return received
		}

		if typ != websocket.MessageText || len(data) == 0 || data[0] != '{' {
			continue // empty frames and other plain-text replies
		}

		if c.dispatch(ctx, data) {
			received = true
		}
	}
}

func (c *DefaultActivityStreamClient) connect(ctx context.Context) (_ *websocket.Conn, err error) {
	done := c.kit.Start(ctx, opActivityConnect)
	defer func() { done(err == nil) }()

	return tracing.InScope(ctx, c.scope, opActivityConnect, func(ctx context.Context, span trace.Span) (*websocket.Conn, error) {
		span.SetAttributes(attribute.String(attrURL, c.cfg.URL))

		conn, _, err := websocket.Dial(ctx, c.cfg.URL, &websocket.DialOptions{HTTPClient: c.dialer})
		if err != nil {
			return nil, failed(ctx, c.log, opActivityConnect, fmt.Errorf("polymarket: dial activity: %w", err))
		}

		conn.SetReadLimit(streamReadLimit)

		payload, err := json.Marshal(activitySubscribe{
			Action:        "subscribe",
			Subscriptions: []activitySubscription{{Topic: activityTopic, Type: "*"}},
		})
		if err != nil {
			_ = conn.CloseNow()
			return nil, err
		}

		writeCtx, cancel := context.WithTimeout(ctx, streamWriteLimit)
		defer cancel()

		if err := conn.Write(writeCtx, websocket.MessageText, payload); err != nil {
			_ = conn.CloseNow()
			return nil, failed(ctx, c.log, opActivityConnect, fmt.Errorf("polymarket: subscribe to activity: %w", err))
		}

		c.log.Info(ctx, "Activity stream connected")

		return conn, nil
	})
}

func (c *DefaultActivityStreamClient) keepAlive(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(c.cfg.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			writeCtx, cancel := context.WithTimeout(ctx, streamWriteLimit)
			err := conn.Write(writeCtx, websocket.MessageText, []byte("ping"))
			cancel()

			if err != nil {
				return // the read loop sees the broken connection and reconnects
			}
		}
	}
}

// dispatch hands on a trade row and reports whether it was one.
func (c *DefaultActivityStreamClient) dispatch(ctx context.Context, data []byte) bool {
	var env activityEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		c.log.Warn(ctx, "Undecodable activity message", logging.Err(err))
		return false
	}

	if env.Body != nil && env.Body.Message != "" {
		c.log.Warn(ctx, "Activity stream said", "message", env.Body.Message)
		return false
	}

	if env.Topic != activityTopic || (env.Type != ActivityTrades && env.Type != ActivityOrdersMatched) {
		return false
	}

	var trade ActivityTrade
	if err := json.Unmarshal(env.Payload, &trade); err != nil {
		c.log.Warn(ctx, "Undecodable activity row", logging.Err(err))
		return false
	}

	trade.Type = env.Type

	select {
	case c.events <- trade:
	default:
		c.incCounter(ctx, c.dropped)
	}

	return true
}

func (c *DefaultActivityStreamClient) incCounter(ctx context.Context, counter metrics.Counter) {
	if err := counter.Inc(nil); err != nil {
		c.log.Warn(ctx, "Failed to increment metric", logging.Err(err))
	}
}
