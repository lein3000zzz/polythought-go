// Package weather reads forecasts from Open-Meteo and airports' observations from
// aviationweather.gov (D441), each answer kept a while: many workflows ask for the same
// city every few seconds, and the forecasts change hourly at most.
package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/singleflight"

	"github.com/lein3000zzz/polythought-go/internal/adapters/webhook"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	tracerName = "github.com/lein3000zzz/polythought-go/internal/adapters/weather"

	opForecast     = "weather.Forecast"
	opObservations = "weather.Observations"

	forecastURL         = "https://api.open-meteo.com/v1/forecast"
	customerForecastURL = "https://customer-api.open-meteo.com/v1/forecast"
	metarURL            = "https://aviationweather.gov/api/data/metar"

	defaultForecastFor = 10 * time.Minute
	observationsFor    = time.Minute
	maxBody            = 4 << 20
	maxKept            = 2000
	userAgent          = "Polythought-Weather/1"
)

var _ ports.WeatherReader = (*Client)(nil)

// ErrStation is a station's code that is none.
var ErrStation = errors.New("not a station's code: four letters or digits, like KLGA")

var icao = regexp.MustCompile(`^[A-Z0-9]{4}$`)

type Config struct {
	// Key is an Open-Meteo customer key, for commercial use; empty, or nil, for the free
	// API (D441). Asked on every request.
	Key func() string
	// ForecastFor is how long a forecast is kept; nil or zero for 10 minutes.
	ForecastFor func() time.Duration
	// Proxy is the outbound proxy, as webhooks take it (D428); nil for none.
	Proxy func() string

	// The services' addresses, and whether those may be inside the network, for tests;
	// empty, and nil, for theirs.
	ForecastURL, CustomerForecastURL, MetarURL string
	AllowPrivate                               func() bool
}

type Client struct {
	cfg    Config
	client *http.Client
	kit    *redkit.Kit
	scope  tracing.Scope
	log    logging.Logger
	now    func() time.Time

	group singleflight.Group
	mu    sync.Mutex
	kept  map[string]kept
}

type kept struct {
	body  []byte
	until time.Time
}

func New(util *observability.Utility, cfg Config) (*Client, error) {
	log := util.Logger.With("component", "weather")

	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    log,
		Subsystem: "weather",
		Buckets:   redkit.MediumLongBuckets,
		Component: "weather",
	})
	if err != nil {
		return nil, fmt.Errorf("weather: register metrics: %w", err)
	}

	if cfg.ForecastURL == "" {
		cfg.ForecastURL = forecastURL
	}

	if cfg.CustomerForecastURL == "" {
		cfg.CustomerForecastURL = customerForecastURL
	}

	if cfg.MetarURL == "" {
		cfg.MetarURL = metarURL
	}

	// Public addresses only, through the outbound proxy where one is set; the services'
	// own addresses are fixed, but a redirect is not.
	dialer := webhook.GuardedDialer(cfg.AllowPrivate, cfg.Proxy)

	return &Client{
		cfg: cfg,
		client: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				Proxy:               nil,
				DialContext:         dialer.DialContext,
				ForceAttemptHTTP2:   true,
				TLSHandshakeTimeout: 5 * time.Second,
				MaxIdleConns:        10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		kit:   kit,
		scope: tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		log:   log,
		now:   time.Now,
		kept:  map[string]kept{},
	}, nil
}

// WithClock replaces the clock, for tests.
func (c *Client) WithClock(now func() time.Time) *Client {
	c.now = now
	return c
}

// openMeteo is Open-Meteo's answer, its series keyed by variable and model.
type openMeteo struct {
	Latitude  float64                    `json:"latitude"`
	Longitude float64                    `json:"longitude"`
	Timezone  string                     `json:"timezone"`
	Daily     map[string]json.RawMessage `json:"daily"`
	Hourly    map[string]json.RawMessage `json:"hourly"`
}

func (c *Client) Forecast(ctx context.Context, q ports.ForecastQuery) (f ports.Forecast, err error) {
	done := c.kit.Start(ctx, opForecast)
	defer func() { done(err == nil) }()

	return tracing.InScope(ctx, c.scope, opForecast, func(ctx context.Context, span trace.Span) (ports.Forecast, error) {
		if len(q.Models) == 0 {
			return ports.Forecast{}, fmt.Errorf("%w: no model asked for", domain.ErrInvalidInput)
		}

		v := url.Values{}
		v.Set("latitude", strconv.FormatFloat(q.Latitude, 'f', 4, 64))
		v.Set("longitude", strconv.FormatFloat(q.Longitude, 'f', 4, 64))
		v.Set("daily", "temperature_2m_max,temperature_2m_min")
		v.Set("models", strings.Join(q.Models, ","))
		v.Set("forecast_days", strconv.Itoa(max(1, min(q.Days, 16))))

		if q.Hourly {
			v.Set("hourly", "temperature_2m")
		}

		zone := q.Zone
		if zone == "" {
			zone = "auto"
		}

		v.Set("timezone", zone)

		if q.Fahrenheit {
			v.Set("temperature_unit", "fahrenheit")
		}

		span.SetAttributes(attribute.String("weather.models", v.Get("models")), attribute.Int("weather.days", q.Days))

		base, key := c.cfg.ForecastURL, ""
		if c.cfg.Key != nil {
			key = strings.TrimSpace(c.cfg.Key())
		}

		if key != "" {
			base = c.cfg.CustomerForecastURL
		}

		body, err := c.get(ctx, base, v, key, c.forecastFor())
		if err != nil {
			return ports.Forecast{}, err
		}

		var om openMeteo
		if err := json.Unmarshal(body, &om); err != nil {
			return ports.Forecast{}, fmt.Errorf("open-meteo: the answer is not one: %w", err)
		}

		return om.forecast(q.Models)
	})
}

func (c *Client) forecastFor() time.Duration {
	if c.cfg.ForecastFor != nil {
		if d := c.cfg.ForecastFor(); d > 0 {
			return d
		}
	}

	return defaultForecastFor
}

func (om openMeteo) forecast(models []string) (ports.Forecast, error) {
	f := ports.Forecast{Latitude: om.Latitude, Longitude: om.Longitude, Zone: om.Timezone}

	var dates []string
	if err := json.Unmarshal(om.Daily["time"], &dates); err != nil {
		return ports.Forecast{}, errors.New("open-meteo: the answer has no days")
	}

	highs, lows := series(om.Daily, "temperature_2m_max", models), series(om.Daily, "temperature_2m_min", models)

	for i, date := range dates {
		day := ports.DailyForecast{Date: date, High: map[string]*float64{}, Low: map[string]*float64{}}

		for _, m := range models {
			day.High[m], day.Low[m] = at(highs[m], i), at(lows[m], i)
		}

		f.Daily = append(f.Daily, day)
	}

	if raw, ok := om.Hourly["time"]; ok {
		var times []string
		if err := json.Unmarshal(raw, &times); err != nil {
			return ports.Forecast{}, errors.New("open-meteo: the answer's hours are not")
		}

		temps := series(om.Hourly, "temperature_2m", models)

		for i, t := range times {
			hour := ports.HourlyForecast{Time: t, Temp: map[string]*float64{}}
			for _, m := range models {
				hour.Temp[m] = at(temps[m], i)
			}

			f.Hourly = append(f.Hourly, hour)
		}
	}

	return f, nil
}

// series are a variable's values for each model: suffixed by its name when several were
// asked for, as Open-Meteo answers then, or not when one was.
func series(data map[string]json.RawMessage, variable string, models []string) map[string][]*float64 {
	out := make(map[string][]*float64, len(models))

	for _, m := range models {
		raw, ok := data[variable+"_"+m]
		if !ok && len(models) == 1 {
			raw = data[variable]
		}

		var values []*float64
		_ = json.Unmarshal(raw, &values)
		out[m] = values
	}

	return out
}

func at(values []*float64, i int) *float64 {
	if i < 0 || i >= len(values) {
		return nil
	}

	return values[i]
}

// awcReport is a report as aviationweather.gov's JSON has it.
type awcReport struct {
	ICAO      string   `json:"icaoId"`
	Name      string   `json:"name"`
	ObsTime   int64    `json:"obsTime"`
	Type      string   `json:"metarType"`
	Temp      *float64 `json:"temp"`
	Dewpoint  *float64 `json:"dewp"`
	WindDir   any      `json:"wdir"` // degrees, or "VRB"
	WindSpeed *int     `json:"wspd"`
	Gust      *int     `json:"wgst"`
	Raw       string   `json:"rawOb"`
}

func (c *Client) Observations(ctx context.Context, station string, hours int) (obs []ports.Observation, err error) {
	done := c.kit.Start(ctx, opObservations)
	defer func() { done(err == nil) }()

	return tracing.InScope(ctx, c.scope, opObservations, func(ctx context.Context, span trace.Span) ([]ports.Observation, error) {
		station = strings.ToUpper(strings.TrimSpace(station))
		if !icao.MatchString(station) {
			return nil, fmt.Errorf("%w: %q: %w", domain.ErrInvalidInput, station, ErrStation)
		}

		span.SetAttributes(attribute.String("weather.station", station))

		v := url.Values{}
		v.Set("ids", station)
		v.Set("format", "json")
		v.Set("hours", strconv.Itoa(max(1, min(hours, 72))))

		body, err := c.get(ctx, c.cfg.MetarURL, v, "", observationsFor)
		if err != nil {
			return nil, err
		}

		if len(strings.TrimSpace(string(body))) == 0 {
			return nil, nil // no report: aviationweather.gov answers 204
		}

		var reports []awcReport
		if err := json.Unmarshal(body, &reports); err != nil {
			return nil, fmt.Errorf("aviationweather.gov: the answer is not one: %w", err)
		}

		out := make([]ports.Observation, 0, len(reports))

		for _, r := range reports {
			o := ports.Observation{
				Station: r.ICAO, Name: r.Name, ObservedAt: time.Unix(r.ObsTime, 0).UTC(),
				Routine: r.Type != "SPECI", TempC: r.Temp, DewpointC: r.Dewpoint,
				WindKt: r.WindSpeed, GustKt: r.Gust, Raw: r.Raw,
			}

			if d, ok := r.WindDir.(float64); ok {
				deg := int(d)
				o.WindDir = &deg
			}

			out = append(out, o)
		}

		return out, nil
	})
}

// get is base's answer to v, kept for as long as for says; calls at once for the same
// share one request. key goes along as apikey, kept out of the cache's keys and errors.
func (c *Client) get(ctx context.Context, base string, v url.Values, key string, keepFor time.Duration) ([]byte, error) {
	id := base + "?" + v.Encode()

	if body, ok := c.cached(id); ok {
		return body, nil
	}

	body, err, _ := c.group.Do(id, func() (any, error) {
		q := v
		if key != "" {
			q = url.Values{}
			for k, vs := range v {
				q[k] = vs
			}

			q.Set("apikey", key)
		}

		body, err := c.fetch(ctx, base+"?"+q.Encode())
		if err != nil {
			return nil, err
		}

		c.keep(id, body, keepFor)

		return body, nil
	})
	if err != nil {
		return nil, err
	}

	return body.([]byte), nil
}

func (c *Client) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	host := req.URL.Hostname()

	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err // its message repeats the URL, the key in it
		}

		return nil, fmt.Errorf("%s: %w: %w", host, domain.ErrUnavailable, err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %w", host, domain.ErrUnavailable, err)
	}

	switch {
	case resp.StatusCode == http.StatusNoContent:
		return nil, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		c.log.Warn(ctx, "Weather service unavailable", "host", host, "status", resp.StatusCode)
		return nil, fmt.Errorf("%s answered %d: %w", host, resp.StatusCode, domain.ErrUnavailable)
	case resp.StatusCode >= 400:
		var refused struct {
			Reason string `json:"reason"`
		}

		_ = json.Unmarshal(body, &refused)

		return nil, fmt.Errorf("%w: %s answered %d: %s", domain.ErrInvalidInput, host, resp.StatusCode, refused.Reason)
	}

	return body, nil
}

func (c *Client) cached(id string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	k, ok := c.kept[id]
	if !ok || !c.now().Before(k.until) {
		return nil, false
	}

	return k.body, true
}

func (c *Client) keep(id string, body []byte, keepFor time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()

	if len(c.kept) >= maxKept {
		for k, v := range c.kept {
			if !now.Before(v.until) {
				delete(c.kept, k)
			}
		}

		if len(c.kept) >= maxKept {
			clear(c.kept) // all fresh: start over rather than grow
		}
	}

	c.kept[id] = kept{body: body, until: now.Add(keepFor)}
}
