package ports

import (
	"context"
	"time"
)

// WeatherReader reads forecasts and airports' observations (D441), for the weather nodes.
type WeatherReader interface {
	// Forecast is the daily highs and lows, and the hourly temperatures when asked, of
	// each model for a place.
	Forecast(ctx context.Context, q ForecastQuery) (Forecast, error)
	// Observations are a station's reports of the last hours, the latest first; none for
	// a station that reported nothing.
	Observations(ctx context.Context, station string, hours int) ([]Observation, error)
}

type ForecastQuery struct {
	Latitude, Longitude float64
	// Models are Open-Meteo's names of them, like gfs_seamless.
	Models []string
	Days   int
	Hourly bool
	// Zone is the time zone the days are in; empty for the place's own.
	Zone       string
	Fahrenheit bool
}

// Forecast is each model's temperatures, by model name, nil where a model has none.
type Forecast struct {
	// Latitude and Longitude are those of the forecast's grid point.
	Latitude, Longitude float64
	Zone                string
	Daily               []DailyForecast
	Hourly              []HourlyForecast
}

type DailyForecast struct {
	Date      string // 2026-11-24
	High, Low map[string]*float64
}

type HourlyForecast struct {
	Time string // 2026-11-24T06:00, local
	Temp map[string]*float64
}

// Observation is one report of a station, a METAR or a SPECI.
type Observation struct {
	Station, Name string
	ObservedAt    time.Time
	// Routine says it is a METAR, not a special report (SPECI).
	Routine bool
	// TempC and DewpointC are in °C, to the tenth where the report gives tenths.
	TempC, DewpointC *float64
	WindDir          *int // degrees; nil for variable or calm
	WindKt, GustKt   *int
	Raw              string
}
