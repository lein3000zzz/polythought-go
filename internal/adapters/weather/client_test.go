package weather_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/weather"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// Open-Meteo's answer for three models, as it gave one for LaGuardia (trimmed).
const threeModels = `{
	"latitude": 40.76809, "longitude": -73.862785, "timezone": "America/New_York",
	"daily": {
		"time": ["2026-11-24", "2026-11-25"],
		"temperature_2m_max_gfs_seamless": [50.5, 46.3], "temperature_2m_min_gfs_seamless": [33.6, 29.5],
		"temperature_2m_max_ecmwf_ifs025": [52.1, null], "temperature_2m_min_ecmwf_ifs025": [36.7, null],
		"temperature_2m_max_icon_seamless": [50.2, 46.2], "temperature_2m_min_icon_seamless": [36.8, 33.8]
	},
	"hourly": {
		"time": ["2026-11-24T00:00", "2026-11-24T01:00"],
		"temperature_2m_gfs_seamless": [50.5, 48.2], "temperature_2m_ecmwf_ifs025": [50.0, 48.7], "temperature_2m_icon_seamless": [50.2, 50.1]
	}
}`

// aviationweather.gov's reports, a METAR and a SPECI (trimmed).
const reports = `[
	{"icaoId": "KLGA", "name": "New York/La Guardia Arpt, NY, US", "obsTime": 1795524660, "metarType": "SPECI",
	 "temp": 6.7, "dewp": -1.7, "wdir": "VRB", "wspd": 3, "rawOb": "SPECI KLGA 241251Z VRB03KT"},
	{"icaoId": "KLGA", "name": "New York/La Guardia Arpt, NY, US", "obsTime": 1795521060, "metarType": "METAR",
	 "temp": 7.2, "dewp": -1.1, "wdir": 330, "wspd": 16, "wgst": 23, "rawOb": "METAR KLGA 241151Z 33016G23KT T00721011"}
]`

type server struct {
	srv   *httptest.Server
	asked atomic.Int32
	last  atomic.Pointer[http.Request]
}

func serve(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *server {
	t.Helper()

	s := &server{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.asked.Add(1)
		s.last.Store(r.Clone(context.Background()))
		handler(w, r)
	}))
	t.Cleanup(s.srv.Close)

	return s
}

func client(t *testing.T, cfg weather.Config, now func() time.Time) *weather.Client {
	t.Helper()

	cfg.AllowPrivate = func() bool { return true }

	c, err := weather.New(observability.NoopUtility(), cfg)
	require.NoError(t, err)

	if now != nil {
		c.WithClock(now)
	}

	return c
}

func f(v float64) *float64 { return &v }

func TestForecast(t *testing.T) {
	ctx := context.Background()
	models := []string{"gfs_seamless", "ecmwf_ifs025", "icon_seamless"}

	t.Run("each model's days and hours, kept a while", func(t *testing.T) {
		om := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(threeModels)) })

		now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
		c := client(t, weather.Config{ForecastURL: om.srv.URL}, func() time.Time { return now })

		q := ports.ForecastQuery{Latitude: 40.77945, Longitude: -73.88027, Models: models, Days: 2, Hourly: true, Zone: "America/New_York", Fahrenheit: true}

		got, err := c.Forecast(ctx, q)
		require.NoError(t, err)

		asked := om.last.Load().URL.Query()
		assert.Equal(t, "40.7794", asked.Get("latitude"), "to four places")
		assert.Equal(t, "gfs_seamless,ecmwf_ifs025,icon_seamless", asked.Get("models"))
		assert.Equal(t, "fahrenheit", asked.Get("temperature_unit"))
		assert.Equal(t, "America/New_York", asked.Get("timezone"))
		assert.Equal(t, "temperature_2m", asked.Get("hourly"))
		assert.Equal(t, "Polythought-Weather/1", om.last.Load().Header.Get("User-Agent"))

		assert.Equal(t, "America/New_York", got.Zone)
		require.Len(t, got.Daily, 2)
		assert.Equal(t, "2026-11-24", got.Daily[0].Date)
		assert.Equal(t, f(52.1), got.Daily[0].High["ecmwf_ifs025"])
		assert.Equal(t, f(33.6), got.Daily[0].Low["gfs_seamless"])
		assert.Nil(t, got.Daily[1].High["ecmwf_ifs025"], "a model with none that day")
		require.Len(t, got.Hourly, 2)
		assert.Equal(t, f(50.1), got.Hourly[1].Temp["icon_seamless"])

		_, err = c.Forecast(ctx, q)
		require.NoError(t, err)
		assert.EqualValues(t, 1, om.asked.Load(), "the same, kept")

		now = now.Add(11 * time.Minute)
		_, err = c.Forecast(ctx, q)
		require.NoError(t, err)
		assert.EqualValues(t, 2, om.asked.Load(), "asked again after 10 minutes")
	})

	t.Run("one model's series are not suffixed", func(t *testing.T) {
		om := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"timezone": "GMT", "daily": {"time": ["2026-11-24"], "temperature_2m_max": [9.5], "temperature_2m_min": [3]}}`))
		})

		got, err := client(t, weather.Config{ForecastURL: om.srv.URL}, nil).Forecast(ctx, ports.ForecastQuery{Models: []string{"best_match"}, Days: 1})
		require.NoError(t, err)
		assert.Equal(t, f(9.5), got.Daily[0].High["best_match"])
		assert.Equal(t, "auto", om.last.Load().URL.Query().Get("timezone"), "the place's own zone")
		assert.Empty(t, om.last.Load().URL.Query().Get("temperature_unit"), "°C")
	})

	t.Run("with a key, the customer API, the key left out of errors", func(t *testing.T) {
		free := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
		paid := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })

		c := client(t, weather.Config{ForecastURL: free.srv.URL, CustomerForecastURL: paid.srv.URL, Key: func() string { return "k-secret" }}, nil)

		_, err := c.Forecast(ctx, ports.ForecastQuery{Models: models, Days: 1})
		require.ErrorIs(t, err, domain.ErrUnavailable)
		assert.NotContains(t, err.Error(), "k-secret")
		assert.Zero(t, free.asked.Load())
		assert.Equal(t, "k-secret", paid.last.Load().URL.Query().Get("apikey"))
	})

	t.Run("refused, saying why", func(t *testing.T) {
		om := serve(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error": true, "reason": "Latitude must be in range of -90 to 90°."}`))
		})

		_, err := client(t, weather.Config{ForecastURL: om.srv.URL}, nil).Forecast(ctx, ports.ForecastQuery{Latitude: 99, Models: models, Days: 1})
		require.ErrorIs(t, err, domain.ErrInvalidInput)
		assert.ErrorContains(t, err, "Latitude must be in range")
	})
}

func TestObservations(t *testing.T) {
	ctx := context.Background()

	t.Run("a station's reports, METARs and SPECIs told apart", func(t *testing.T) {
		awc := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(reports)) })

		got, err := client(t, weather.Config{MetarURL: awc.srv.URL}, nil).Observations(ctx, " klga ", 30)
		require.NoError(t, err)

		asked := awc.last.Load().URL.Query()
		assert.Equal(t, "KLGA", asked.Get("ids"))
		assert.Equal(t, "30", asked.Get("hours"))

		require.Len(t, got, 2)
		assert.False(t, got[0].Routine, "a SPECI")
		assert.Nil(t, got[0].WindDir, "variable")
		assert.True(t, got[1].Routine)
		assert.Equal(t, f(7.2), got[1].TempC)
		assert.Equal(t, 330, *got[1].WindDir)
		assert.Equal(t, 23, *got[1].GustKt)
		assert.Equal(t, time.Unix(1795521060, 0).UTC(), got[1].ObservedAt)
	})

	t.Run("none for a station with no report", func(t *testing.T) {
		awc := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

		got, err := client(t, weather.Config{MetarURL: awc.srv.URL}, nil).Observations(ctx, "ZZZZ", 1)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("not a station", func(t *testing.T) {
		_, err := client(t, weather.Config{}, nil).Observations(ctx, "LaGuardia", 1)
		require.ErrorIs(t, err, weather.ErrStation)
		require.ErrorIs(t, err, domain.ErrInvalidInput)
	})
}
