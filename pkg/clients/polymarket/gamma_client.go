package polymarket

import (
	"context"
	"net/url"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
)

var _ GammaClient = (*DefaultGammaClient)(nil)

type DefaultGammaClient struct {
	core *httpCore
}

func NewGammaClient(util *observability.Utility, cfg HTTPConfig) (*DefaultGammaClient, error) {
	core, err := newHTTPCore(util, "gamma", cfg.withDefaults(DefaultGammaURL, defaultGammaRPS))
	if err != nil {
		return nil, err
	}

	return &DefaultGammaClient{core: core}, nil
}

func (c *DefaultGammaClient) ListMarkets(ctx context.Context, q MarketsQuery) (Page[Market], error) {
	params := url.Values{}
	setInt(params, "limit", q.Limit)
	setString(params, "after_cursor", q.AfterCursor)
	setBool(params, "closed", q.Closed)
	addAll(params, "tag_id", q.TagIDs)
	addAll(params, "condition_ids", q.ConditionIDs)
	addAll(params, "slug", q.Slugs)

	var resp struct {
		Markets    []Market `json:"markets"`
		NextCursor string   `json:"next_cursor"`
	}

	if err := c.core.get(ctx, "list_markets", "/markets/keyset", params, &resp); err != nil {
		return Page[Market]{}, err
	}

	return Page[Market]{Items: resp.Markets, NextCursor: resp.NextCursor}, nil
}

func (c *DefaultGammaClient) ListEvents(ctx context.Context, q EventsQuery) (Page[Event], error) {
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

	if err := c.core.get(ctx, "list_events", "/events/keyset", params, &resp); err != nil {
		return Page[Event]{}, err
	}

	return Page[Event]{Items: resp.Events, NextCursor: resp.NextCursor}, nil
}

func (c *DefaultGammaClient) GetMarket(ctx context.Context, id string) (Market, error) {
	var m Market
	err := c.core.get(ctx, "get_market", "/markets/"+url.PathEscape(id), nil, &m)

	return m, err
}

func (c *DefaultGammaClient) GetMarketBySlug(ctx context.Context, slug string) (Market, error) {
	var m Market
	err := c.core.get(ctx, "get_market_by_slug", "/markets/slug/"+url.PathEscape(slug), nil, &m)

	return m, err
}

func (c *DefaultGammaClient) GetEvent(ctx context.Context, id string) (Event, error) {
	var e Event
	err := c.core.get(ctx, "get_event", "/events/"+url.PathEscape(id), nil, &e)

	return e, err
}

func (c *DefaultGammaClient) GetEventBySlug(ctx context.Context, slug string) (Event, error) {
	var e Event
	err := c.core.get(ctx, "get_event_by_slug", "/events/slug/"+url.PathEscape(slug), nil, &e)

	return e, err
}

func (c *DefaultGammaClient) ListTags(ctx context.Context, q TagsQuery) ([]Tag, error) {
	params := url.Values{}
	setInt(params, "limit", q.Limit)
	setInt(params, "offset", q.Offset)

	var tags []Tag
	err := c.core.get(ctx, "list_tags", "/tags", params, &tags)

	return tags, err
}

func (c *DefaultGammaClient) Search(ctx context.Context, q SearchQuery) (SearchResult, error) {
	params := url.Values{}
	params.Set("q", q.Query)
	setInt(params, "limit_per_type", q.LimitPerType)
	setInt(params, "page", q.Page)
	setString(params, "events_status", q.EventsStatus)

	var r SearchResult
	err := c.core.get(ctx, "search", "/public-search", params, &r)

	return r, err
}
