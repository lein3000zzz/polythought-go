package shape_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
)

func TestConforms(t *testing.T) {
	t.Parallel()

	fields := []engine.Field{
		shape.Text("question", ""),
		shape.OneOf("side", "", "buy", "sell"),
		shape.Number("count", ""),
		shape.Decimal("price", ""),
		shape.Bool("open", ""),
		shape.Time("at", ""),
		shape.Nullable(shape.Decimal("bid", "")),
		shape.Optional(shape.Text("note", "")),
		shape.ListOf("outcomes", "", shape.Text("label", ""), shape.Decimal("price", "")),
		{Name: "extra", Kind: engine.FieldAny},
	}

	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	typed := func() map[string]any {
		return map[string]any{
			"question": "BTC?", "side": "buy", "count": 3, "price": decimal.RequireFromString("0.42"), "open": true,
			"at": at, "bid": nil, "outcomes": []any{map[string]any{"label": "Yes", "price": decimal.RequireFromString("0.4")}},
			"extra": []string{"anything"},
		}
	}

	require.NoError(t, shape.Conforms(fields, typed()), "as nodes build them")
	require.NoError(t, shape.Conforms(fields, map[string]any{
		"question": "BTC?", "side": "sell", "count": 3.0, "price": "0.42", "open": false, "at": "2026-10-15T12:00:00Z",
		"bid": "0.41", "note": "hi", "outcomes": []any{}, "extra": nil,
	}), "as JSON reads them back")

	tests := []struct {
		name   string
		change func(map[string]any)
		want   string
	}{
		{"a field missing", func(m map[string]any) { delete(m, "price") }, "field price is missing"},
		{"one not declared", func(m map[string]any) { m["spread"] = "0.01" }, "field spread is not declared"},
		{"null where it may not be", func(m map[string]any) { m["price"] = nil }, "field price is null"},
		{"text not among the options", func(m map[string]any) { m["side"] = "hold" }, "field side is string hold, not text"},
		{"a number as text", func(m map[string]any) { m["count"] = "3" }, "field count is string 3, not number"},
		{"a decimal that is not one", func(m map[string]any) { m["price"] = "lots" }, "field price is string lots, not decimal"},
		{"a time that is not one", func(m map[string]any) { m["at"] = "yesterday" }, "not time"},
		{"not a list", func(m map[string]any) { m["outcomes"] = "Yes" }, "field outcomes is string, not a list"},
		{"inside a list", func(m map[string]any) {
			m["outcomes"] = []map[string]any{{"label": "Yes", "price": "0.4"}, {"label": 1, "price": "0.6"}}
		}, "field outcomes[1].label is int 1, not text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			item := typed()
			tt.change(item)

			err := shape.Conforms(fields, item)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()

	own := engine.NodeType{Name: "test.own", Items: shape.Own(shape.Text("q", ""))}
	out := engine.Ports{
		engine.DefaultPort: {{JSON: map[string]any{"q": "BTC?"}}},
		engine.ErrorPort:   {{JSON: map[string]any{"whatever": 1}}}, // the items received
	}

	require.NoError(t, shape.Check(own, out))

	out.Add(engine.DefaultPort, engine.Item{JSON: map[string]any{"q": 1}})
	require.EqualError(t, shape.Check(own, out), "test.own: main item 1: field q is int 1, not text")

	passed := engine.NodeType{Name: "test.passed", Inputs: []string{engine.DefaultPort}, Items: shape.Passed()}
	require.NoError(t, shape.Check(passed, out), "not its own to check")
}
