package polymarket

import (
	"net/http"
	"time"
)

const (
	DefaultGammaURL    = "https://gamma-api.polymarket.com"
	DefaultClobURL     = "https://clob.polymarket.com"
	DefaultDataURL     = "https://data-api.polymarket.com"
	DefaultMarketWSURL = "wss://ws-subscriptions-clob.polymarket.com/ws/market"

	// DefaultActivityWSURL is the real-time data service, whose activity topic carries every
	// trade on Polymarket. The topic is no longer documented, but works (2026-10).
	DefaultActivityWSURL = "wss://ws-live-data.polymarket.com"
)

// Default request rates sit at the strictest endpoint each client uses
// (Gamma /markets 300/10s, CLOB /books 500/10s, Data /v2/positions 200/10s).
const (
	defaultGammaRPS = 30
	defaultClobRPS  = 50
	defaultDataRPS  = 20
)

const (
	defaultTimeout    = 10 * time.Second
	defaultMaxRetries = 3
)

// HTTPConfig configures one REST client. Zero fields take that client's defaults.
type HTTPConfig struct {
	BaseURL    string
	Timeout    time.Duration     // per attempt, 0 => 10s
	RPS        float64           // 0 => client default (gamma 30, clob 50, data 20)
	Burst      int               // 0 => RPS
	MaxRetries int               // 0 => 3, negative => no retries
	Transport  http.RoundTripper // nil => http.DefaultTransport, always wrapped by otelhttp
}

func (c HTTPConfig) withDefaults(baseURL string, rps float64) HTTPConfig {
	if c.BaseURL == "" {
		c.BaseURL = baseURL
	}

	if c.Timeout <= 0 {
		c.Timeout = defaultTimeout
	}

	if c.RPS <= 0 {
		c.RPS = rps
	}

	if c.Burst <= 0 {
		c.Burst = max(1, int(c.RPS))
	}

	switch {
	case c.MaxRetries == 0:
		c.MaxRetries = defaultMaxRetries
	case c.MaxRetries < 0:
		c.MaxRetries = 0
	}

	if c.Transport == nil {
		c.Transport = http.DefaultTransport
	}

	return c
}

const (
	defaultStreamBuffer = 1024
	defaultPingInterval = 10 * time.Second
	defaultReconnectMin = time.Second
	defaultReconnectMax = 30 * time.Second
)

// StreamConfig configures the market channel. Zero fields take the defaults.
type StreamConfig struct {
	URL            string
	BufferSize     int           // events channel capacity, 0 => 1024; newer events are dropped when full
	PingInterval   time.Duration // 0 => 10s; no message for 3 intervals forces a reconnect
	ReconnectMin   time.Duration // 0 => 1s
	ReconnectMax   time.Duration // 0 => 30s
	CustomFeatures bool          // enables best_bid_ask, new_market and market_resolved events
}

func (c StreamConfig) withDefaults() StreamConfig {
	if c.URL == "" {
		c.URL = DefaultMarketWSURL
	}

	if c.BufferSize <= 0 {
		c.BufferSize = defaultStreamBuffer
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

// ActivityStreamConfig configures the activity stream, every trade on Polymarket as it
// happens.
type ActivityStreamConfig struct {
	URL          string
	BufferSize   int           // events channel capacity, 0 => 4096; newer events are dropped when full
	PingInterval time.Duration // 0 => 5s
	Silence      time.Duration // 0 => 15s; nothing for this long forces a reconnect, as the stream can freeze
	ReconnectMin time.Duration // 0 => 1s
	ReconnectMax time.Duration // 0 => 30s
}

func (c ActivityStreamConfig) withDefaults() ActivityStreamConfig {
	if c.URL == "" {
		c.URL = DefaultActivityWSURL
	}

	if c.BufferSize <= 0 {
		c.BufferSize = 4096
	}

	if c.PingInterval <= 0 {
		c.PingInterval = 5 * time.Second
	}

	if c.Silence <= 0 {
		c.Silence = 15 * time.Second
	}

	if c.ReconnectMin <= 0 {
		c.ReconnectMin = defaultReconnectMin
	}

	if c.ReconnectMax < c.ReconnectMin {
		c.ReconnectMax = max(defaultReconnectMax, c.ReconnectMin)
	}

	return c
}
