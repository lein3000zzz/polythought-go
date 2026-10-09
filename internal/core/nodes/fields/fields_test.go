package fields_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"

	"github.com/lein3000zzz/polythought-go/internal/core/nodes/fields"
)

var item = map[string]any{
	"question": "Will it rain?",
	"spread":   decimal.RequireFromString("0.015"),
	"volume":   json.Number("1200.5"),
	"at":       time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC),
	"outcomes": []any{
		map[string]any{"label": "Yes", "price": decimal.RequireFromString("0.62")},
		map[string]any{"label": "No", "price": "0.38"},
	},
	"empty": nil,
}

func TestLookup(t *testing.T) {
	tests := []struct {
		path   string
		want   any
		wantOK bool
	}{
		{path: "question", want: "Will it rain?", wantOK: true},
		{path: "outcomes.1.label", want: "No", wantOK: true},
		{path: "outcomes.0.price", want: decimal.RequireFromString("0.62"), wantOK: true},
		{path: "empty", want: nil, wantOK: true},
		{path: "missing"},
		{path: "outcomes.2.label"},
		{path: "outcomes.x"},
		{path: "question.length"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, ok := fields.Lookup(item, tt.path)

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNumber(t *testing.T) {
	tests := []struct {
		name   string
		v      any
		want   string
		wantOK bool
	}{
		{name: "decimal", v: decimal.RequireFromString("0.015"), want: "0.015", wantOK: true},
		{name: "json number", v: json.Number("1200.5"), want: "1200.5", wantOK: true},
		{name: "numeric string", v: " 0.38 ", want: "0.38", wantOK: true},
		{name: "float", v: 2.5, want: "2.5", wantOK: true},
		{name: "int", v: 7, want: "7", wantOK: true},
		{name: "text", v: "lots"},
		{name: "nil", v: nil},
		{name: "bool", v: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := fields.Number(tt.v)

			assert.Equal(t, tt.wantOK, ok)
			if ok {
				assert.Equal(t, tt.want, got.String())
			}
		})
	}
}

func TestRender(t *testing.T) {
	tests := []struct {
		template string
		want     string
	}{
		{template: "Spread on {{question}}: {{ spread }}", want: "Spread on Will it rain?: 0.015"},
		{template: "{{outcomes.0.label}} at {{outcomes.0.price}}", want: "Yes at 0.62"},
		{template: "At {{at}}", want: "At 2026-10-09T12:00:00Z"},
		{template: "Unknown: [{{nope}}]", want: "Unknown: []"},
		{template: "No placeholders", want: "No placeholders"},
		{template: "{{outcomes.1}}", want: `{"label":"No","price":"0.38"}`},
	}

	for _, tt := range tests {
		t.Run(tt.template, func(t *testing.T) {
			assert.Equal(t, tt.want, fields.Render(tt.template, item))
		})
	}
}
