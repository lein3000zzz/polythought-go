package polymarket

import "go.opentelemetry.io/otel/attribute"

const tracerName = "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"

// Span attribute keys shared by the clients.
const (
	attrID           = "polymarket.id"
	attrSlug         = "polymarket.slug"
	attrConditionID  = "polymarket.condition_id"
	attrConditionIDs = "polymarket.condition_ids"
	attrTokenID      = "polymarket.token_id"
	attrTokenIDs     = "polymarket.token_ids"
	attrTokenCount   = "polymarket.token_count"
	attrUser         = "polymarket.user"
	attrQuery        = "polymarket.query"
	attrLimit        = "polymarket.limit"
	attrCursor       = "polymarket.cursor"
	attrResults      = "polymarket.results"
	attrHasMore      = "polymarket.has_more"
	attrAttempts     = "polymarket.attempts"
	attrAssets       = "polymarket.assets"
	attrURL          = "url.full"
	attrStatusCode   = "http.response.status_code"
)

// Token IDs are ~78 characters, so large batches record only their count.
const maxTokenIDsOnSpan = 10

func tokenAttrs(ids []string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{attribute.Int(attrTokenCount, len(ids))}
	if len(ids) <= maxTokenIDsOnSpan {
		attrs = append(attrs, attribute.StringSlice(attrTokenIDs, ids))
	}

	return attrs
}

func pageAttrs(limit int, cursor string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.Int(attrLimit, limit),
		attribute.String(attrCursor, cursor),
	}
}

func resultAttrs(n int, nextCursor string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.Int(attrResults, n),
		attribute.Bool(attrHasMore, nextCursor != ""),
	}
}
