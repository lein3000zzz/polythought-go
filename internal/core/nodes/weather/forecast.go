package weather

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const ForecastType = "weather.forecast"

// Models are Open-Meteo's, by the names the node's settings give them.
var Models = map[string]string{
	"gfs":         "gfs_seamless",
	"ecmwf":       "ecmwf_ifs025",
	"ecmwf_hres":  "ecmwf_ifs",
	"aifs":        "ecmwf_aifs025_single",
	"icon":        "icon_seamless",
	"gem":         "gem_seamless",
	"jma":         "jma_seamless",
	"ukmo":        "ukmo_seamless",
	"meteofrance": "meteofrance_seamless",
	"metno":       "metno_seamless",
	"knmi":        "knmi_seamless",
	"dmi":         "dmi_seamless",
	"cma":         "cma_grapes_global",
	"nbm":         "ncep_nbm_conus",
	"hrrr":        "ncep_hrrr_conus",
	"best":        "best_match",
}

// modelNames are the models in the order the settings offer them.
var modelNames = []string{"gfs", "ecmwf", "icon", "ecmwf_hres", "aifs", "gem", "jma", "ukmo", "meteofrance", "metno", "knmi", "dmi", "cma", "nbm", "hrrr", "best"}

const maxDays = 16

var _ engine.Node = (*Forecast)(nil)

// Forecast puts a place's forecast on each item: each model's daily highs and lows, side
// by side, and their means; the hourly temperatures when asked (D441).
type Forecast struct {
	weather ports.WeatherReader
}

func NewForecast(weather ports.WeatherReader) *Forecast {
	return &Forecast{weather: weather}
}

var forecastField = engine.Field{Name: "forecast", Kind: engine.FieldObject, Description: "The forecast.", Fields: []engine.Field{
	shape.Nullable(shape.Text("city", "The city, when one was picked.")),
	shape.Nullable(shape.Text("station", "Its markets' station, when a city was picked.")),
	shape.Number("latitude", "The forecast's grid point."),
	shape.Number("longitude", "The forecast's grid point."),
	shape.Text("zone", "The time zone its days and hours are in."),
	shape.OneOf("unit", "Its temperatures' unit.", UnitF, UnitC),
	{Name: "models", Kind: engine.FieldList, Description: "The models, as named in the settings.", Of: &engine.Field{Kind: engine.FieldText}},
	shape.ListOf("daily", "A day each, the first today.",
		shape.Text("date", "The day, like 2026-11-24."),
		engine.Field{Name: "high", Kind: engine.FieldAny, Description: "Each model's high, by its name: high.gfs; null where it has none."},
		engine.Field{Name: "low", Kind: engine.FieldAny, Description: "Each model's low, by its name."},
		shape.Nullable(shape.Number("highMean", "The models' highs, averaged.")),
		shape.Nullable(shape.Number("lowMean", "The models' lows, averaged.")),
	),
	shape.ListOf("hourly", "An hour each, when asked for.",
		shape.Text("time", "The hour, local, like 2026-11-24T06:00."),
		engine.Field{Name: "temp", Kind: engine.FieldAny, Description: "Each model's temperature, by its name."},
		shape.Nullable(shape.Number("mean", "The models' temperatures, averaged.")),
	),
	shape.Optional(shape.Text("note", "What the city's markets resolve by, when not its station's METARs.")),
}}

func (*Forecast) Type() engine.NodeType {
	return engine.NodeType{
		Name:  ForecastType,
		Label: "Weather forecast",
		Description: "Puts a forecast on each item: the daily highs and lows of several weather models side by side, " +
			"from Open-Meteo, for a city Polymarket lists temperature markets for or any place.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort},
		Reads:   true,
		Items:   &engine.Items{Passed: true, Named: "field", Fields: []engine.Field{forecastField}},
		Params: []engine.ParamSpec{
			cityParam(),
			{Name: "latitude", Label: "Latitude", Kind: engine.ParamDecimal, Description: "Without a city: the place's, like 40.7794."},
			{Name: "longitude", Label: "Longitude", Kind: engine.ParamDecimal, Description: "Without a city: the place's, like -73.8803."},
			{
				Name: "models", Label: "Models", Kind: engine.ParamList, Options: modelNames, Default: []any{"gfs", "ecmwf", "icon"},
				Description: "The weather models, side by side: GFS, ECMWF, ICON and more; best is Open-Meteo's own blend. " +
					"nbm and hrrr cover the US only.",
			},
			{
				Name: "days", Label: "Days", Kind: engine.ParamNumber, Default: 3,
				Description: "How many days, today first: 1 to 16.",
				Check: func(v any) error {
					n, err := engine.Params{"days": v}.Int("days")
					if err != nil || n < 1 || n > maxDays {
						return fmt.Errorf("days: from 1 to %d", maxDays)
					}

					return nil
				},
			},
			{Name: "hourly", Label: "Hourly", Kind: engine.ParamBool, Default: false, Description: "Also each hour's temperatures."},
			unitParam(),
			{Name: "field", Label: "Forecast field", Kind: engine.ParamString, Default: "forecast", Description: "Where the forecast goes on the item."},
		},
	}
}

// forecastAsk is what one item asks for.
type forecastAsk struct {
	q      ports.ForecastQuery
	names  []string
	city   string
	note   string
	sta    string
	field  string
	hourly bool
}

func (a forecastAsk) key() string {
	return fmt.Sprintf("%.4f,%.4f,%s,%d,%t,%s,%t", a.q.Latitude, a.q.Longitude, strings.Join(a.q.Models, ","), a.q.Days, a.q.Hourly, a.q.Zone, a.q.Fahrenheit)
}

func (n *Forecast) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}
	got := map[string]ports.Forecast{}
	failed := map[string]error{}

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		ask, err := forecastAskOf(p)
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		key := ask.key()
		if _, done := got[key]; !done && failed[key] == nil {
			f, err := n.weather.Forecast(ctx, ask.q)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}

				failed[key] = err
			} else {
				got[key] = f
			}
		}

		if err := failed[key]; err != nil {
			out.Fail(item, src, err)
			continue
		}

		j := maps.Clone(item.JSON)
		if j == nil {
			j = map[string]any{}
		}

		j[ask.field] = forecastJSON(ask, got[key])
		out.Add(engine.DefaultPort, engine.Item{JSON: j, Source: []engine.ItemRef{src}})
	}

	return out, nil
}

func forecastAskOf(p engine.Params) (forecastAsk, error) {
	var a forecastAsk

	city, hasCity, err := cityOf(p)
	if err != nil {
		return a, err
	}

	switch {
	case hasCity:
		a.q.Latitude, a.q.Longitude, a.q.Zone = city.Latitude, city.Longitude, city.Zone
		a.city, a.sta, a.note = city.Name, city.Station, city.Note
	case p["latitude"] != nil && p["longitude"] != nil:
		lat, err := p.Decimal("latitude")
		if err != nil {
			return a, err
		}

		lon, err := p.Decimal("longitude")
		if err != nil {
			return a, err
		}

		a.q.Latitude, a.q.Longitude = lat.InexactFloat64(), lon.InexactFloat64()
		if a.q.Latitude < -90 || a.q.Latitude > 90 || a.q.Longitude < -180 || a.q.Longitude > 180 {
			return a, fmt.Errorf("%w: %v, %v is no place on Earth", engine.ErrInvalidParam, a.q.Latitude, a.q.Longitude)
		}
	default:
		return a, fmt.Errorf("%w: %w", engine.ErrInvalidParam, errNoPlace)
	}

	if a.q.Fahrenheit, err = fahrenheit(p, city, hasCity); err != nil {
		return a, err
	}

	names := []string{"gfs", "ecmwf", "icon"}
	if p["models"] != nil {
		if names, err = p.Strings("models"); err != nil {
			return a, err
		}
	}

	for _, name := range names {
		id, ok := Models[name]
		if !ok {
			return a, fmt.Errorf("%w: %q is not a model: one of %s", engine.ErrInvalidParam, name, strings.Join(modelNames, ", "))
		}

		if !slices.Contains(a.q.Models, id) {
			a.q.Models = append(a.q.Models, id)
			a.names = append(a.names, name)
		}
	}

	if len(a.names) == 0 {
		return a, fmt.Errorf("%w: no model: pick one at least", engine.ErrInvalidParam)
	}

	a.q.Days = 3
	if p["days"] != nil {
		if a.q.Days, err = p.Int("days"); err != nil {
			return a, err
		}
	}

	if a.q.Days < 1 || a.q.Days > maxDays {
		return a, fmt.Errorf("%w %q: from 1 to %d", engine.ErrInvalidParam, "days", maxDays)
	}

	if p["hourly"] != nil {
		if a.q.Hourly, err = p.Bool("hourly"); err != nil {
			return a, err
		}
	}

	a.hourly = a.q.Hourly

	a.field = "forecast"
	if p["field"] != nil {
		if a.field, err = p.String("field"); err != nil {
			return a, err
		}
	}

	return a, nil
}

// forecastJSON is the forecast as the item has it: each model by the name its settings
// gave, and their means.
func forecastJSON(a forecastAsk, f ports.Forecast) map[string]any {
	names := make([]any, len(a.names))
	for i, n := range a.names {
		names[i] = n
	}

	daily := make([]any, 0, len(f.Daily))

	for _, d := range f.Daily {
		high, highMean := byName(a, d.High)
		low, lowMean := byName(a, d.Low)

		daily = append(daily, map[string]any{"date": d.Date, "high": high, "low": low, "highMean": highMean, "lowMean": lowMean})
	}

	hourly := []any{}

	for _, h := range f.Hourly {
		temp, mean := byName(a, h.Temp)
		hourly = append(hourly, map[string]any{"time": h.Time, "temp": temp, "mean": mean})
	}

	out := map[string]any{
		"city": nil, "station": nil, "latitude": f.Latitude, "longitude": f.Longitude, "zone": f.Zone,
		"unit": unitName(a.q.Fahrenheit), "models": names, "daily": daily, "hourly": hourly,
	}

	if a.city != "" {
		out["city"], out["station"] = a.city, a.sta
	}

	if a.note != "" {
		out["note"] = a.note
	}

	return out
}

// byName are the models' values by the names the settings gave them, and their mean,
// nil for none.
func byName(a forecastAsk, values map[string]*float64) (map[string]any, any) {
	out := make(map[string]any, len(a.names))

	var (
		sum   float64
		count int
	)

	for i, name := range a.names {
		v := values[a.q.Models[i]]
		if v == nil {
			out[name] = nil
			continue
		}

		out[name] = *v
		sum += *v
		count++
	}

	if count == 0 {
		return out, nil
	}

	return out, tenth(sum / float64(count))
}
