package polymarket

import (
	"context"
	"fmt"
	"net/url"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var _ GammaClient = (*DefaultGammaClient)(nil)

const (
	opListMarkets     = "list_markets"
	opListEvents      = "list_events"
	opGetMarket       = "get_market"
	opGetMarketBySlug = "get_market_by_slug"
	opGetEvent        = "get_event"
	opGetEventBySlug  = "get_event_by_slug"
	opListTags        = "list_tags"
	opSearch          = "search"
)

type DefaultGammaClient struct {
	core  *httpCore
	kit   *redkit.Kit
	scope tracing.Scope
	log   logging.Logger
}

func NewGammaClient(util *observability.Utility, cfg HTTPConfig) (*DefaultGammaClient, error) {
	log := util.Logger.With("component", "polymarket.gamma")

	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    log,
		Subsystem: "polymarket_gamma",
		Buckets:   redkit.MediumLongBuckets,
		Component: "polymarket gamma",
	})
	if err != nil {
		return nil, fmt.Errorf("polymarket: register gamma metrics: %w", err)
	}

	return &DefaultGammaClient{
		core:  newHTTPCore(log, cfg.withDefaults(DefaultGammaURL, defaultGammaRPS)),
		kit:   kit,
		scope: tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		log:   log,
	}, nil
}

func (c *DefaultGammaClient) ListMarkets(ctx context.Context, q MarketsQuery) (_ Page[Market], err error) {
	done := c.kit.Start(ctx, opListMarkets)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opListMarkets, func(ctx context.Context, span trace.Span) (Page[Market], error) {
		span.SetAttributes(pageAttrs(q.Limit, q.AfterCursor)...)

		params := url.Values{}
		setInt(params, "limit", q.Limit)
		setString(params, "after_cursor", q.AfterCursor)
		setBool(params, "closed", q.Closed)
		addAll(params, "tag_id", q.TagIDs)
		addAll(params, "condition_ids", q.ConditionIDs)
		addAll(params, "slug", q.Slugs)
		addAll(params, "clob_token_ids", q.TokenIDs)

		var resp struct {
			Markets    []Market `json:"markets"`
			NextCursor string   `json:"next_cursor"`
		}

		if err := c.core.get(ctx, "/markets/keyset", params, &resp); err != nil {
			return Page[Market]{}, failed(ctx, c.log, opListMarkets, err)
		}

		span.SetAttributes(resultAttrs(len(resp.Markets), resp.NextCursor)...)
		c.log.Debug(ctx, "Listed markets", "count", len(resp.Markets), "has_more", resp.NextCursor != "")

		return Page[Market]{Items: resp.Markets, NextCursor: resp.NextCursor}, nil
	})
}

func (c *DefaultGammaClient) ListEvents(ctx context.Context, q EventsQuery) (_ Page[Event], err error) {
	done := c.kit.Start(ctx, opListEvents)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opListEvents, func(ctx context.Context, span trace.Span) (Page[Event], error) {
		span.SetAttributes(pageAttrs(q.Limit, q.AfterCursor)...)

		params := url.Values{}
		setInt(params, "limit", q.Limit)
		setString(params, "after_cursor", q.AfterCursor)
		setBool(params, "closed", q.Closed)
		setBool(params, "live", q.Live)
		setBool(params, "featured", q.Featured)
		setString(params, "title_search", q.TitleSearch)
		addAll(params, "series_id", q.SeriesIDs)
		addAll(params, "exclude_tag_id", q.ExcludeTagIDs)

		var resp struct {
			Events     []Event `json:"events"`
			NextCursor string  `json:"next_cursor"`
		}

		if err := c.core.get(ctx, "/events/keyset", params, &resp); err != nil {
			return Page[Event]{}, failed(ctx, c.log, opListEvents, err)
		}

		span.SetAttributes(resultAttrs(len(resp.Events), resp.NextCursor)...)
		c.log.Debug(ctx, "Listed events", "count", len(resp.Events), "has_more", resp.NextCursor != "")

		return Page[Event]{Items: resp.Events, NextCursor: resp.NextCursor}, nil
	})
}

func (c *DefaultGammaClient) GetMarket(ctx context.Context, id string) (_ Market, err error) {
	done := c.kit.Start(ctx, opGetMarket)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opGetMarket, func(ctx context.Context, span trace.Span) (Market, error) {
		span.SetAttributes(attribute.String(attrID, id))

		var m Market
		if err := c.core.get(ctx, "/markets/"+url.PathEscape(id), nil, &m); err != nil {
			return Market{}, failed(ctx, c.log, opGetMarket, err)
		}

		span.SetAttributes(attribute.String(attrConditionID, m.ConditionID))
		c.log.Debug(ctx, "Fetched market", "id", id, "condition_id", m.ConditionID)

		return m, nil
	})
}

func (c *DefaultGammaClient) GetMarketBySlug(ctx context.Context, slug string) (_ Market, err error) {
	done := c.kit.Start(ctx, opGetMarketBySlug)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opGetMarketBySlug, func(ctx context.Context, span trace.Span) (Market, error) {
		span.SetAttributes(attribute.String(attrSlug, slug))

		var m Market
		if err := c.core.get(ctx, "/markets/slug/"+url.PathEscape(slug), nil, &m); err != nil {
			return Market{}, failed(ctx, c.log, opGetMarketBySlug, err)
		}

		span.SetAttributes(attribute.String(attrConditionID, m.ConditionID))
		c.log.Debug(ctx, "Fetched market", "slug", slug, "condition_id", m.ConditionID)

		return m, nil
	})
}

func (c *DefaultGammaClient) GetEvent(ctx context.Context, id string) (_ Event, err error) {
	done := c.kit.Start(ctx, opGetEvent)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opGetEvent, func(ctx context.Context, span trace.Span) (Event, error) {
		span.SetAttributes(attribute.String(attrID, id))

		var e Event
		if err := c.core.get(ctx, "/events/"+url.PathEscape(id), nil, &e); err != nil {
			return Event{}, failed(ctx, c.log, opGetEvent, err)
		}

		span.SetAttributes(attribute.Int(attrResults, len(e.Markets)))
		c.log.Debug(ctx, "Fetched event", "id", id, "markets", len(e.Markets))

		return e, nil
	})
}

func (c *DefaultGammaClient) GetEventBySlug(ctx context.Context, slug string) (_ Event, err error) {
	done := c.kit.Start(ctx, opGetEventBySlug)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opGetEventBySlug, func(ctx context.Context, span trace.Span) (Event, error) {
		span.SetAttributes(attribute.String(attrSlug, slug))

		var e Event
		if err := c.core.get(ctx, "/events/slug/"+url.PathEscape(slug), nil, &e); err != nil {
			return Event{}, failed(ctx, c.log, opGetEventBySlug, err)
		}

		span.SetAttributes(attribute.String(attrID, e.ID), attribute.Int(attrResults, len(e.Markets)))
		c.log.Debug(ctx, "Fetched event", "slug", slug, "markets", len(e.Markets))

		return e, nil
	})
}

func (c *DefaultGammaClient) ListTags(ctx context.Context, q TagsQuery) (_ []Tag, err error) {
	done := c.kit.Start(ctx, opListTags)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opListTags, func(ctx context.Context, span trace.Span) ([]Tag, error) {
		span.SetAttributes(attribute.Int(attrLimit, q.Limit))

		params := url.Values{}
		setInt(params, "limit", q.Limit)
		setInt(params, "offset", q.Offset)

		var tags []Tag
		if err := c.core.get(ctx, "/tags", params, &tags); err != nil {
			return nil, failed(ctx, c.log, opListTags, err)
		}

		span.SetAttributes(attribute.Int(attrResults, len(tags)))
		c.log.Debug(ctx, "Listed tags", "count", len(tags))

		return tags, nil
	})
}

func (c *DefaultGammaClient) Search(ctx context.Context, q SearchQuery) (_ SearchResult, err error) {
	done := c.kit.Start(ctx, opSearch)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opSearch, func(ctx context.Context, span trace.Span) (SearchResult, error) {
		span.SetAttributes(attribute.String(attrQuery, q.Query), attribute.Int(attrLimit, q.LimitPerType))

		params := url.Values{}
		params.Set("q", q.Query)
		setInt(params, "limit_per_type", q.LimitPerType)
		setInt(params, "page", q.Page)
		setString(params, "events_status", q.EventsStatus)

		var r SearchResult
		if err := c.core.get(ctx, "/public-search", params, &r); err != nil {
			return SearchResult{}, failed(ctx, c.log, opSearch, err)
		}

		span.SetAttributes(attribute.Int(attrResults, len(r.Events)), attribute.Bool(attrHasMore, r.Pagination.HasMore))
		c.log.Debug(ctx, "Searched", "query", q.Query, "events", len(r.Events), "tags", len(r.Tags), "profiles", len(r.Profiles))

		return r, nil
	})
}
