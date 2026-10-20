package polymarket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// The CLOB's user channel (3p3, D195): an account's order and trade events as they
// happen, authenticated with its API credentials. No replay: after each reconnect what
// was missed is to be read again over REST.

// DefaultUserWSURL is the CLOB's user channel.
const DefaultUserWSURL = "wss://ws-subscriptions-clob.polymarket.com/ws/user"

// UserStreamClient follows accounts on the user channel.
type UserStreamClient interface {
	// Watch streams the account's events until ctx is done, reconnecting as needed: a
	// Gap first on every connection, as what came before it may have been missed. The
	// channel is closed once ctx is done.
	Watch(ctx context.Context, creds L2Credentials) <-chan UserEvent
}

var _ UserStreamClient = (*DefaultUserStreamClient)(nil)

// UserEvent is one thing the user channel said; a Gap carries nothing else.
type UserEvent struct {
	Gap   bool
	Order *UserOrderEvent
	Trade *UserTradeEvent
}

// UserOrderEvent is an order of the account placed, matched in part or whole, or
// cancelled. It says which; what it came to is read over REST, which has it all.
type UserOrderEvent struct {
	ID              string   `json:"id"`
	Type            string   `json:"type"`   // PLACEMENT, UPDATE or CANCELLATION
	Status          string   `json:"status"` // LIVE, MATCHED, DELAYED, UNMATCHED or CANCELED
	Market          string   `json:"market"`
	AssetID         string   `json:"asset_id"`
	AssociateTrades []string `json:"associate_trades"`
}

// UserTradeEvent is a trade one of the account's orders is in, as taker or maker, and
// how far it has come on chain.
type UserTradeEvent struct {
	ID           string `json:"id"`
	TakerOrderID string `json:"taker_order_id"`
	// Status is the bare word on this channel, CONFIRMED; read as REST's.
	Status      TradeStatus `json:"status"`
	AssetID     string      `json:"asset_id"`
	MakerOrders []struct {
		OrderID string `json:"order_id"`
	} `json:"maker_orders"`
}

// Orders are the orders the trade is of: its taker's and its makers'.
func (t UserTradeEvent) Orders() []string {
	out := []string{t.TakerOrderID}
	for _, m := range t.MakerOrders {
		out = append(out, m.OrderID)
	}

	return out
}

// UserStreamConfig configures the user channel.
type UserStreamConfig struct {
	URL          string
	PingInterval time.Duration // 0 => 10s; no message for 3 intervals forces a reconnect
	ReconnectMin time.Duration // 0 => 1s
	ReconnectMax time.Duration // 0 => 30s
}

func (c UserStreamConfig) withDefaults() UserStreamConfig {
	if c.URL == "" {
		c.URL = DefaultUserWSURL
	}

	if c.PingInterval <= 0 {
		c.PingInterval = defaultPingInterval
	}

	if c.ReconnectMin <= 0 {
		c.ReconnectMin = defaultReconnectMin
	}

	if c.ReconnectMax < c.ReconnectMin {
		c.ReconnectMax = max(defaultReconnectMax, c.ReconnectMin)
	}

	return c
}

type DefaultUserStreamClient struct {
	cfg    UserStreamConfig
	log    logging.Logger
	dialer *http.Client
}

func NewUserStreamClient(util *observability.Utility, cfg UserStreamConfig) *DefaultUserStreamClient {
	return &DefaultUserStreamClient{
		cfg:    cfg.withDefaults(),
		log:    util.Logger.With("component", "polymarket.user_stream"),
		dialer: &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)},
	}
}

// userSubscribe is the first message of a connection, as @polymarket/client sends it:
// the API key's three parts, every market.
type userSubscribe struct {
	Auth struct {
		APIKey     string `json:"apiKey"`
		Passphrase string `json:"passphrase"`
		Secret     string `json:"secret"`
	} `json:"auth"`
	Type string `json:"type"`
}

func (c *DefaultUserStreamClient) Watch(ctx context.Context, creds L2Credentials) <-chan UserEvent {
	events := make(chan UserEvent, 64)

	go func() {
		defer close(events)

		backoff := c.cfg.ReconnectMin

		for {
			received := c.session(ctx, creds, events)
			if ctx.Err() != nil {
				return
			}

			if received {
				backoff = c.cfg.ReconnectMin
			}

			wait := backoff/2 + rand.N(backoff/2+1)
			c.log.Info(ctx, "Reconnecting user stream", "address", creds.Address, "wait", wait)

			if sleep(ctx, wait) != nil {
				return
			}

			backoff = min(backoff*2, c.cfg.ReconnectMax)
		}
	}()

	return events
}

// session runs one connection until it fails: received reports whether any message came,
// which resets the backoff.
func (c *DefaultUserStreamClient) session(ctx context.Context, creds L2Credentials, events chan<- UserEvent) (received bool) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, c.cfg.URL, &websocket.DialOptions{HTTPClient: c.dialer})
	if err != nil {
		if ctx.Err() == nil {
			c.log.Warn(ctx, "User stream not connected", "address", creds.Address, logging.Err(err))
		}

		return false
	}
	defer func() { _ = conn.CloseNow() }()

	conn.SetReadLimit(streamReadLimit)

	var sub userSubscribe
	sub.Type = "user"
	sub.Auth.APIKey, sub.Auth.Passphrase, sub.Auth.Secret = creds.APIKey, creds.Passphrase, creds.Secret

	if err := writeUserJSON(ctx, conn, sub); err != nil {
		c.log.Warn(ctx, "User stream not subscribed", "address", creds.Address, logging.Err(err))
		return false
	}

	c.log.Info(ctx, "User stream connected", "address", creds.Address)

	if !emit(ctx, events, UserEvent{Gap: true}) {
		return false
	}

	go keepUserAlive(ctx, conn, c.cfg.PingInterval)

	for {
		readCtx, readCancel := context.WithTimeout(ctx, 3*c.cfg.PingInterval)
		typ, data, err := conn.Read(readCtx)
		readCancel()

		if err != nil {
			if ctx.Err() != nil {
				_ = conn.Close(websocket.StatusNormalClosure, "")
				return received
			}

			c.log.Warn(ctx, "User stream disconnected", "address", creds.Address, logging.Err(err))

			return received
		}

		received = true

		if typ != websocket.MessageText || len(data) == 0 || (data[0] != '{' && data[0] != '[') {
			continue // PONG
		}

		for _, e := range decodeUserEvents(data) {
			if !emit(ctx, events, e) {
				return received
			}
		}
	}
}

func emit(ctx context.Context, events chan<- UserEvent, e UserEvent) bool {
	select {
	case events <- e:
		return true
	case <-ctx.Done():
		return false
	}
}

func keepUserAlive(ctx context.Context, conn *websocket.Conn, every time.Duration) {
	ticker := time.NewTicker(every)
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
				return
			}
		}
	}
}

func writeUserJSON(ctx context.Context, conn *websocket.Conn, msg any) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, streamWriteLimit)
	defer cancel()

	return conn.Write(ctx, websocket.MessageText, payload)
}

// decodeUserEvents reads one message: an event, or an array of them. What it cannot read,
// and events of other kinds, it skips.
func decodeUserEvents(data []byte) []UserEvent {
	raws := []json.RawMessage{data}
	if data[0] == '[' {
		if err := json.Unmarshal(data, &raws); err != nil {
			return nil
		}
	}

	var out []UserEvent

	for _, raw := range raws {
		e, err := decodeUserEvent(raw)
		if err == nil {
			out = append(out, e)
		}
	}

	return out
}

var errNotUserEvent = errors.New("polymarket: not an order or trade event")

func decodeUserEvent(raw []byte) (UserEvent, error) {
	var head struct {
		Type string `json:"event_type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return UserEvent{}, err
	}

	switch head.Type {
	case "order":
		var o UserOrderEvent
		if err := json.Unmarshal(raw, &o); err != nil {
			return UserEvent{}, fmt.Errorf("polymarket: an order event: %w", err)
		}

		return UserEvent{Order: &o}, nil
	case "trade":
		var t UserTradeEvent
		if err := json.Unmarshal(raw, &t); err != nil {
			return UserEvent{}, fmt.Errorf("polymarket: a trade event: %w", err)
		}

		return UserEvent{Trade: &t}, nil
	default:
		return UserEvent{}, errNotUserEvent
	}
}
