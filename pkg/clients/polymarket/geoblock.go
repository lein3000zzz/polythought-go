package polymarket

import (
	"context"
	"fmt"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/trace"
)

// DefaultGeoblockURL is where Polymarket says whether it takes orders from the IP asking.
const DefaultGeoblockURL = "https://polymarket.com"

const (
	defaultGeoblockRPS = 1
	opGeoblock         = "geoblock"
)

// GeoblockClient asks Polymarket whether it takes orders from this IP.
type GeoblockClient interface {
	Geoblock(ctx context.Context) (Geoblock, error)
}

var _ GeoblockClient = (*DefaultGeoblockClient)(nil)

// Geoblock is Polymarket's answer for the IP asking: blocked where it does not take its
// orders, or only its website does not; the country is ISO 3166-1, the region its
// subdivision.
type Geoblock struct {
	Blocked bool   `json:"blocked"`
	IP      string `json:"ip"`
	Country string `json:"country"`
	Region  string `json:"region"`
}

type DefaultGeoblockClient struct {
	core  *httpCore
	kit   *redkit.Kit
	scope tracing.Scope
	log   logging.Logger
}

func NewGeoblockClient(util *observability.Utility, cfg HTTPConfig) (*DefaultGeoblockClient, error) {
	log := util.Logger.With("component", "polymarket.geoblock")

	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    log,
		Subsystem: "polymarket_geoblock",
		Buckets:   redkit.MediumLongBuckets,
		Component: "polymarket geoblock",
	})
	if err != nil {
		return nil, fmt.Errorf("polymarket: register geoblock metrics: %w", err)
	}

	return &DefaultGeoblockClient{
		core:  newHTTPCore(log, cfg.withDefaults(DefaultGeoblockURL, defaultGeoblockRPS)),
		kit:   kit,
		scope: tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		log:   log,
	}, nil
}

// Geoblock is GET /api/geoblock, for the IP this server reaches Polymarket from.
func (c *DefaultGeoblockClient) Geoblock(ctx context.Context) (_ Geoblock, err error) {
	done := c.kit.Start(ctx, opGeoblock)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opGeoblock, func(ctx context.Context, _ trace.Span) (Geoblock, error) {
		var out Geoblock
		if err := c.core.get(ctx, "/api/geoblock", nil, &out); err != nil {
			return Geoblock{}, failed(ctx, c.log, opGeoblock, err)
		}

		return out, nil
	})
}
