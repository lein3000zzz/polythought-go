package weather_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/weather"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// fake answers as Open-Meteo and aviationweather.gov would, keeping what it was asked.
type fake struct {
	forecasts []ports.ForecastQuery
	stations  []string
	forecast  ports.Forecast
	obs       []ports.Observation
	err       error
}

func (f *fake) Forecast(_ context.Context, q ports.ForecastQuery) (ports.Forecast, error) {
	f.forecasts = append(f.forecasts, q)
	return f.forecast, f.err
}

func (f *fake) Observations(_ context.Context, station string, _ int) ([]ports.Observation, error) {
	f.stations = append(f.stations, station)
	return f.obs, f.err
}

func n(v float64) *float64 { return &v }
func i(v int) *int         { return &v }

func run(t *testing.T, node engine.Node, params map[string]any, items int) engine.Ports {
	t.Helper()

	in := engine.Ports{}
	for k := range items {
		in.Add(engine.DefaultPort, engine.Item{JSON: map[string]any{"k": k}})
	}

	out, err := node.Run(context.Background(), engine.RunContext{Params: engine.LiteralParams(node.Type(), params)}, in)
	require.NoError(t, err)

	return out
}

func TestForecast(t *testing.T) {
	forecast := ports.Forecast{
		Latitude: 40.77, Longitude: -73.86, Zone: "America/New_York",
		Daily: []ports.DailyForecast{{
			Date: "2026-11-24",
			High: map[string]*float64{"gfs_seamless": n(50.5), "ecmwf_ifs025": n(52.1), "icon_seamless": nil},
			Low:  map[string]*float64{"gfs_seamless": n(33.6), "ecmwf_ifs025": n(36.7), "icon_seamless": n(36.8)},
		}},
	}

	t.Run("a city's, in its unit, each model by its name, once for every item", func(t *testing.T) {
		f := &fake{forecast: forecast}
		node := weather.NewForecast(f)
		out := run(t, node, map[string]any{"city": "NYC"}, 2)

		require.Len(t, f.forecasts, 1, "the same place asked once")
		q := f.forecasts[0]
		assert.Equal(t, ports.ForecastQuery{
			Latitude: 40.77945, Longitude: -73.88027, Models: []string{"gfs_seamless", "ecmwf_ifs025", "icon_seamless"},
			Days: 3, Zone: "America/New_York", Fahrenheit: true,
		}, q)

		items := out[engine.DefaultPort]
		require.Len(t, items, 2)
		require.NoError(t, shape.Check(node.Type(), out))

		got := items[1].JSON["forecast"].(map[string]any)
		assert.Equal(t, "NYC", got["city"])
		assert.Equal(t, "KLGA", got["station"])
		assert.Equal(t, "F", got["unit"])
		assert.Equal(t, []any{"gfs", "ecmwf", "icon"}, got["models"])

		day := got["daily"].([]any)[0].(map[string]any)
		assert.Equal(t, map[string]any{"gfs": 50.5, "ecmwf": 52.1, "icon": nil}, day["high"])
		assert.InDelta(t, 51.3, day["highMean"], 0.001, "the models that have one")
		assert.InDelta(t, 35.7, day["lowMean"], 0.001)
		assert.Equal(t, 1, items[1].JSON["k"], "the item's own fields stay")
	})

	t.Run("a place by hand, the models and unit as set", func(t *testing.T) {
		f := &fake{forecast: forecast}
		run(t, weather.NewForecast(f), map[string]any{
			"latitude": "51.5", "longitude": "-0.12", "models": []any{"best", "ukmo"}, "days": 7, "hourly": true, "unit": "C",
		}, 1)

		require.Len(t, f.forecasts, 1)
		assert.Equal(t, ports.ForecastQuery{
			Latitude: 51.5, Longitude: -0.12, Models: []string{"best_match", "ukmo_seamless"}, Days: 7, Hourly: true,
		}, f.forecasts[0])
	})

	t.Run("what it refuses", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			params map[string]any
			want   string
		}{
			{name: "no place", params: map[string]any{}, want: "no place"},
			{name: "a place on no Earth", params: map[string]any{"latitude": "91", "longitude": "0"}, want: "no place on Earth"},
			{name: "a model that is none", params: map[string]any{"city": "London", "models": []any{"crystal"}}, want: `"crystal" is not a model`},
			{name: "too many days", params: map[string]any{"city": "London", "days": 17}, want: "from 1 to 16"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := &fake{}
				out := run(t, weather.NewForecast(f), tc.params, 1)

				assert.Empty(t, f.forecasts)
				require.Len(t, out[engine.ErrorPort], 1)
				assert.ErrorContains(t, out[engine.ErrorPort][0].Err, tc.want)
				assert.True(t, engine.IsFinal(out[engine.ErrorPort][0].Err), "no use trying again")
			})
		}
	})

	t.Run("a service that fails fails the items, to be tried again", func(t *testing.T) {
		f := &fake{err: errors.New("api.open-meteo.com answered 503: upstream service unavailable")}
		out := run(t, weather.NewForecast(f), map[string]any{"city": "Paris"}, 2)

		assert.Len(t, f.forecasts, 1)
		require.Len(t, out[engine.ErrorPort], 2)
		assert.False(t, engine.IsFinal(out[engine.ErrorPort][0].Err))
	})

	t.Run("a city whose markets resolve otherwise says so", func(t *testing.T) {
		out := run(t, weather.NewForecast(&fake{forecast: forecast}), map[string]any{"city": "Hong Kong"}, 1)
		assert.Contains(t, out[engine.DefaultPort][0].JSON["forecast"].(map[string]any)["note"], "Hong Kong Observatory")
	})
}

func TestAirport(t *testing.T) {
	// 13:30 UTC: 08:30 in New York, 13:30 in London.
	now := time.Date(2026, 11, 24, 13, 30, 0, 0, time.UTC)
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		require.NoError(t, err)

		return v
	}

	obs := []ports.Observation{
		{Station: "KLGA", Name: "New York/La Guardia", ObservedAt: at("2026-11-24T11:51:00Z"), Routine: true, TempC: n(7.2), DewpointC: n(-1.1), WindDir: i(330), WindKt: i(16), GustKt: i(23), Raw: "METAR KLGA 241151Z"},
		{Station: "KLGA", Name: "New York/La Guardia", ObservedAt: at("2026-11-24T12:51:00Z"), Routine: false, TempC: n(9.9), Raw: "SPECI KLGA 241251Z"},
		{Station: "KLGA", Name: "New York/La Guardia", ObservedAt: at("2026-11-24T05:51:00Z"), Routine: true, TempC: n(5.0), Raw: "METAR KLGA 240551Z"},
		// Yesterday in New York, though today in UTC.
		{Station: "KLGA", Name: "New York/La Guardia", ObservedAt: at("2026-11-24T03:51:00Z"), Routine: true, TempC: n(12.0), Raw: "METAR KLGA 240351Z"},
	}

	t.Run("the latest, and the local day's high and low by routine reports, in the city's unit", func(t *testing.T) {
		f := &fake{obs: obs}
		node := weather.NewAirport(f, func() time.Time { return now })
		out := run(t, node, map[string]any{"city": "NYC"}, 2)

		assert.Equal(t, []string{"KLGA"}, f.stations, "asked once")
		require.Len(t, out[engine.DefaultPort], 2)
		require.NoError(t, shape.Check(node.Type(), out))

		got := out[engine.DefaultPort][0].JSON["airport"].(map[string]any)
		assert.Equal(t, "SPECI KLGA 241251Z", got["raw"], "the latest")
		assert.Equal(t, false, got["routine"])
		assert.Equal(t, 49.8, got["temp"], "9.9 °C in °F")
		assert.Equal(t, 9.9, got["tempC"])
		assert.Equal(t, "NYC", got["city"])

		today := got["today"].(map[string]any)
		assert.Equal(t, "2026-11-24", today["date"])
		assert.Equal(t, "America/New_York", today["zone"])
		assert.Equal(t, 7.2, today["highC"], "the SPECI's 9.9 and yesterday's 12.0 left out")
		assert.Equal(t, 5.0, today["lowC"])
		assert.Equal(t, 45.0, today["high"])
		assert.Equal(t, 41.0, today["low"])
		assert.Equal(t, 2, today["reports"])
	})

	t.Run("a station by hand, its day in the zone given, °C", func(t *testing.T) {
		f := &fake{obs: obs}
		out := run(t, weather.NewAirport(f, func() time.Time { return now }), map[string]any{"station": "klga"}, 1)

		assert.Equal(t, []string{"KLGA"}, f.stations)
		got := out[engine.DefaultPort][0].JSON["airport"].(map[string]any)
		assert.Equal(t, "C", got["unit"])
		assert.Nil(t, got["city"])
		assert.Equal(t, 12.0, got["today"].(map[string]any)["highC"], "UTC's day has the 03:51 report")
	})

	t.Run("what it refuses", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			params map[string]any
			want   string
		}{
			{name: "no station", params: map[string]any{}, want: "no station"},
			{name: "a station that is none", params: map[string]any{"station": "LaGuardia"}, want: "is not a station's code"},
			{name: "a zone that is none", params: map[string]any{"station": "KLGA", "zone": "Mars/Base"}, want: "is not a time zone"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := &fake{obs: obs}
				out := run(t, weather.NewAirport(f, func() time.Time { return now }), tc.params, 1)

				assert.Empty(t, f.stations)
				require.Len(t, out[engine.ErrorPort], 1)
				assert.ErrorContains(t, out[engine.ErrorPort][0].Err, tc.want)
			})
		}
	})

	t.Run("a station with no report fails its items", func(t *testing.T) {
		out := run(t, weather.NewAirport(&fake{}, func() time.Time { return now }), map[string]any{"station": "ZZZZ"}, 1)

		require.Len(t, out[engine.ErrorPort], 1)
		assert.ErrorContains(t, out[engine.ErrorPort][0].Err, "ZZZZ: no report in 30 hours")
	})

	t.Run("refused by the service, as it says", func(t *testing.T) {
		f := &fake{err: domain.ErrInvalidInput}
		out := run(t, weather.NewAirport(f, func() time.Time { return now }), map[string]any{"city": "London"}, 1)

		require.Len(t, out[engine.ErrorPort], 1)
		assert.True(t, engine.IsFinal(out[engine.ErrorPort][0].Err))
	})
}
