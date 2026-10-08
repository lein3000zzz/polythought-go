package engine_test

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

func TestParamsGetters(t *testing.T) {
	params := engine.Params{
		"str":       "hello",
		"bool":      true,
		"dec":       "0.25",
		"decValue":  decimal.RequireFromString("1.5"),
		"float":     0.5,
		"int":       7,
		"int64":     int64(8),
		"wholeF":    3.0,
		"fraction":  2.5,
		"jsonNum":   json.Number("0.125"),
		"jsonInt":   json.Number("42"),
		"strings":   []string{"a", "b"},
		"anyList":   []any{"x", "y"},
		"mixedList": []any{"x", 1},
		"null":      nil,
	}

	tests := []struct {
		name    string
		get     func() (any, error)
		want    any
		wantErr error
	}{
		{name: "string", get: func() (any, error) { return params.String("str") }, want: "hello"},
		{name: "string from bool", get: func() (any, error) { return params.String("bool") }, wantErr: engine.ErrInvalidParam},
		{name: "bool", get: func() (any, error) { return params.Bool("bool") }, want: true},
		{name: "bool from string", get: func() (any, error) { return params.Bool("str") }, wantErr: engine.ErrInvalidParam},
		{name: "decimal from string", get: func() (any, error) { return params.Decimal("dec") }, want: "0.25"},
		{name: "decimal from decimal", get: func() (any, error) { return params.Decimal("decValue") }, want: "1.5"},
		{name: "decimal from float", get: func() (any, error) { return params.Decimal("float") }, want: "0.5"},
		{name: "decimal from int", get: func() (any, error) { return params.Decimal("int") }, want: "7"},
		{name: "decimal from int64", get: func() (any, error) { return params.Decimal("int64") }, want: "8"},
		{name: "decimal from json number", get: func() (any, error) { return params.Decimal("jsonNum") }, want: "0.125"},
		{name: "decimal from non-numeric string", get: func() (any, error) { return params.Decimal("str") }, wantErr: engine.ErrInvalidParam},
		{name: "decimal from bool", get: func() (any, error) { return params.Decimal("bool") }, wantErr: engine.ErrInvalidParam},
		{name: "int", get: func() (any, error) { return params.Int("int") }, want: 7},
		{name: "int from int64", get: func() (any, error) { return params.Int("int64") }, want: 8},
		{name: "int from whole float", get: func() (any, error) { return params.Int("wholeF") }, want: 3},
		{name: "int from json number", get: func() (any, error) { return params.Int("jsonInt") }, want: 42},
		{name: "int from fraction", get: func() (any, error) { return params.Int("fraction") }, wantErr: engine.ErrInvalidParam},
		{name: "int from fractional json number", get: func() (any, error) { return params.Int("jsonNum") }, wantErr: engine.ErrInvalidParam},
		{name: "strings", get: func() (any, error) { return params.Strings("strings") }, want: []string{"a", "b"}},
		{name: "strings from json array", get: func() (any, error) { return params.Strings("anyList") }, want: []string{"x", "y"}},
		{name: "strings from mixed array", get: func() (any, error) { return params.Strings("mixedList") }, wantErr: engine.ErrInvalidParam},
		{name: "missing", get: func() (any, error) { return params.String("nope") }, wantErr: engine.ErrMissingParam},
		{name: "null is missing", get: func() (any, error) { return params.Decimal("null") }, wantErr: engine.ErrMissingParam},
		{name: "missing bool", get: func() (any, error) { return params.Bool("nope") }, wantErr: engine.ErrMissingParam},
		{name: "missing int", get: func() (any, error) { return params.Int("nope") }, wantErr: engine.ErrMissingParam},
		{name: "missing strings", get: func() (any, error) { return params.Strings("nope") }, wantErr: engine.ErrMissingParam},
		{name: "int from string", get: func() (any, error) { return params.Int("str") }, wantErr: engine.ErrInvalidParam},
		{name: "strings from string", get: func() (any, error) { return params.Strings("str") }, wantErr: engine.ErrInvalidParam},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.get()

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			if d, ok := got.(decimal.Decimal); ok {
				got = d.String()
			}

			assert.Equal(t, tt.want, got)
		})
	}
}
