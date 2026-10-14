package settings_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
)

func TestSpecParse(t *testing.T) {
	t.Parallel()

	positive := func(v any) error {
		if v.(int) < 1 {
			return errors.New("must be at least 1")
		}

		return nil
	}

	tests := []struct {
		name    string
		spec    settings.Spec
		raw     any
		want    any
		wantErr string
	}{
		{name: "string is trimmed", spec: settings.Spec{Kind: settings.KindString}, raw: "  hi ", want: "hi"},
		{name: "secret is kept as typed", spec: settings.Spec{Kind: settings.KindString, Secret: true}, raw: " p w ", want: " p w "},
		{name: "string rejects a number", spec: settings.Spec{Kind: settings.KindString}, raw: 1.0, wantErr: "want text"},
		{name: "int from env", spec: settings.Spec{Kind: settings.KindInt}, raw: " 42 ", want: 42},
		{name: "int from JSON number", spec: settings.Spec{Kind: settings.KindInt}, raw: json.Number("7"), want: 7},
		{name: "int from float", spec: settings.Spec{Kind: settings.KindInt}, raw: 3.0, want: 3},
		{name: "int rejects a fraction", spec: settings.Spec{Kind: settings.KindInt}, raw: 3.5, wantErr: "whole number"},
		{name: "int rejects text", spec: settings.Spec{Kind: settings.KindInt}, raw: "lots", wantErr: `"lots" is not a whole number`},
		{name: "int check", spec: settings.Spec{Kind: settings.KindInt, Check: positive}, raw: "0", wantErr: "must be at least 1"},
		{name: "bool from env", spec: settings.Spec{Kind: settings.KindBool}, raw: "true", want: true},
		{name: "bool from JSON", spec: settings.Spec{Kind: settings.KindBool}, raw: false, want: false},
		{name: "bool rejects text", spec: settings.Spec{Kind: settings.KindBool}, raw: "maybe", wantErr: "not true or false"},
		{name: "duration from text", spec: settings.Spec{Kind: settings.KindDuration}, raw: "1h30m", want: 90 * time.Minute},
		{name: "duration as typed in code", spec: settings.Spec{Kind: settings.KindDuration}, raw: time.Hour, want: time.Hour},
		{name: "duration must be positive", spec: settings.Spec{Kind: settings.KindDuration}, raw: "-1s", wantErr: "positive duration"},
		{name: "duration rejects a number", spec: settings.Spec{Kind: settings.KindDuration}, raw: 5.0, wantErr: "want a duration"},
		{name: "enum", spec: settings.Spec{Kind: settings.KindEnum, Options: []string{"a", "b"}}, raw: " b", want: "b"},
		{name: "enum rejects others", spec: settings.Spec{Kind: settings.KindEnum, Options: []string{"a", "b"}}, raw: "c", wantErr: `"c" is not one of a, b`},
		{name: "list from env", spec: settings.Spec{Kind: settings.KindList}, raw: "a, b,,c ", want: []string{"a", "b", "c"}},
		{name: "list from JSON", spec: settings.Spec{Kind: settings.KindList}, raw: []any{"a", " "}, want: []string{"a"}},
		{name: "empty list is not nil", spec: settings.Spec{Kind: settings.KindList}, raw: []string(nil), want: []string{}},
		{name: "list rejects numbers", spec: settings.Spec{Kind: settings.KindList}, raw: []any{1.0}, wantErr: "list of text"},
		{name: "unknown kind", spec: settings.Spec{Kind: "colour"}, raw: "red", wantErr: "unknown kind"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := tc.spec.Parse(tc.raw)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSpecEnvName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "POLYTHOUGHT_AUTH_SESSION_IDLE_TTL", settings.Spec{Key: "auth.session_idle_ttl"}.EnvName())
	assert.Equal(t, "POLYTHOUGHT_REGISTRATION", settings.Spec{Key: "auth.registration", Env: "POLYTHOUGHT_REGISTRATION"}.EnvName())
}

func TestSpecEncode(t *testing.T) {
	t.Parallel()

	assert.JSONEq(t, `"168h"`, string(settings.Spec{}.Encode(168*time.Hour)))
	assert.JSONEq(t, `["a"]`, string(settings.Spec{}.Encode([]string{"a"})))
	assert.JSONEq(t, `12`, string(settings.Spec{}.Encode(12)))
}

func TestFormatDuration(t *testing.T) {
	t.Parallel()

	for d, want := range map[time.Duration]string{
		168 * time.Hour:  "168h",
		90 * time.Minute: "1h30m",
		90 * time.Second: "1m30s",
		time.Second:      "1s",
		5 * time.Minute:  "5m",
		time.Millisecond: "1ms",
	} {
		assert.Equal(t, want, settings.FormatDuration(d))
	}
}

func TestRegistryRegister(t *testing.T) {
	t.Parallel()

	ok := settings.Spec{Key: "a.b", Label: "B", Kind: settings.KindInt, Default: 1}

	tests := []struct {
		name    string
		specs   []settings.Spec
		wantErr string
	}{
		{name: "no key", specs: []settings.Spec{{Label: "X", Kind: settings.KindInt, Default: 1}}, wantErr: "needs a key and a label"},
		{name: "no label", specs: []settings.Spec{{Key: "x", Kind: settings.KindInt, Default: 1}}, wantErr: "needs a key and a label"},
		{name: "twice", specs: []settings.Spec{ok, ok}, wantErr: "registered twice"},
		{name: "inside another", specs: []settings.Spec{ok, {Key: "a.b.c", Label: "C", Kind: settings.KindInt, Default: 1}}, wantErr: "overlap"},
		{name: "holds another", specs: []settings.Spec{ok, {Key: "a", Label: "A", Kind: settings.KindInt, Default: 1}}, wantErr: "overlap"},
		{name: "secret not text", specs: []settings.Spec{{Key: "x", Label: "X", Kind: settings.KindInt, Secret: true, Default: 1}}, wantErr: "must be a string"},
		{name: "bad default", specs: []settings.Spec{{Key: "x", Label: "X", Kind: settings.KindEnum, Options: []string{"a"}, Default: "b"}}, wantErr: "default of"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := settings.NewRegistry().Register(tc.specs...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}

	t.Run("keeps order and defaults apply to live", func(t *testing.T) {
		t.Parallel()

		r := settings.NewRegistry()
		require.NoError(t, r.Register(ok, settings.Spec{Key: "a.a", Label: "A", Kind: settings.KindBool, Default: false, Apply: settings.ApplyRestart}))

		specs := r.Specs()
		require.Len(t, specs, 2)
		assert.Equal(t, "a.b", specs[0].Key)
		assert.Equal(t, settings.ApplyLive, specs[0].Apply)
		assert.Equal(t, settings.ApplyRestart, specs[1].Apply)

		_, found := r.Spec("a.c")
		assert.False(t, found)
	})
}
