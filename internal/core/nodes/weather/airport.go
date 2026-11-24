package weather

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/cities"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/zones"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const AirportType = "weather.airport"

// reportHours are the hours of reports read: enough to hold the whole local day so far,
// whatever the zone.
const reportHours = 30

var icao = regexp.MustCompile(`^[A-Z0-9]{4}$`)

var _ engine.Node = (*Airport)(nil)

// Airport puts an airport's latest observation on each item, its METAR, and the day's high
// and low so far as the temperature markets count them: routine reports only, no SPECI, in
// tenths of a degree Celsius, through the station's local day (D441).
type Airport struct {
	weather ports.WeatherReader
	now     func() time.Time
}

func NewAirport(weather ports.WeatherReader, now func() time.Time) *Airport {
	return &Airport{weather: weather, now: now}
}

var airportField = engine.Field{Name: "airport", Kind: engine.FieldObject, Description: "The airport's latest observation.", Fields: []engine.Field{
	shape.Text("station", "Its ICAO code, like KLGA."),
	shape.Text("name", "Its name."),
	shape.Nullable(shape.Text("city", "The city, when one was picked.")),
	shape.Time("observedAt", "When the latest report was made."),
	shape.Bool("routine", "The latest is a routine METAR, not a special report (SPECI)."),
	shape.Nullable(shape.Number("temp", "The temperature, in the unit.")),
	shape.Nullable(shape.Number("tempC", "The temperature in °C, to the tenth where the report gives tenths.")),
	shape.Nullable(shape.Number("dewpoint", "The dew point, in the unit.")),
	shape.OneOf("unit", "Its temperatures' unit.", UnitF, UnitC),
	engine.Field{Name: "wind", Kind: engine.FieldObject, Description: "The wind.", Fields: []engine.Field{
		shape.Nullable(shape.Number("dir", "From where, in degrees; null for variable or calm.")),
		shape.Nullable(shape.Number("speedKt", "Its speed, in knots.")),
		shape.Nullable(shape.Number("gustKt", "Its gusts, in knots.")),
	}},
	shape.Text("raw", "The report as sent, like METAR KLGA 241251Z …"),
	engine.Field{Name: "today", Kind: engine.FieldObject, Description: "The local day so far, by routine reports, as the markets count it.", Fields: []engine.Field{
		shape.Text("date", "The local day, like 2026-11-24."),
		shape.Text("zone", "Its time zone."),
		shape.Nullable(shape.Number("high", "The highest temperature so far, in the unit.")),
		shape.Nullable(shape.Number("low", "The lowest so far, in the unit.")),
		shape.Nullable(shape.Number("highC", "The highest so far, in °C.")),
		shape.Nullable(shape.Number("lowC", "The lowest so far, in °C.")),
		shape.Number("reports", "The routine reports counted."),
	}},
	shape.Optional(shape.Text("note", "What the city's markets resolve by, when not its station's METARs.")),
}}

func (*Airport) Type() engine.NodeType {
	return engine.NodeType{
		Name:  AirportType,
		Label: "Airport reading",
		Description: "Puts an airport's latest observation on each item, its METAR from aviationweather.gov, with the " +
			"day's high and low so far as the temperature markets count them: routine reports only, in the local day.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort},
		Reads:   true,
		Items:   &engine.Items{Passed: true, Named: "field", Fields: []engine.Field{airportField}},
		Params: []engine.ParamSpec{
			cityParam(),
			{
				Name: "station", Label: "Station", Kind: engine.ParamString,
				Description: "Without a city, or in place of its own: an airport's ICAO code, like KLGA or EGLC.",
			},
			{
				Name: "zone", Label: "Time zone", Kind: engine.ParamString, Check: checkZone,
				Description: "Whose day is today, for a station by hand: like America/Chicago; empty for the city's, or UTC.",
			},
			unitParam(),
			{Name: "field", Label: "Reading field", Kind: engine.ParamString, Default: "airport", Description: "Where the reading goes on the item."},
		},
	}
}

func checkZone(v any) error {
	if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
		return nil
	}

	return zones.Check(v)
}

// airportAsk is what one item asks for.
type airportAsk struct {
	city    cities.City
	hasCity bool
	station string
	loc     *time.Location
	f       bool
	field   string
}

func (n *Airport) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}
	got := map[string][]ports.Observation{}
	failed := map[string]error{}

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		ask, err := airportAskOf(p)
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		if _, done := got[ask.station]; !done && failed[ask.station] == nil {
			obs, err := n.weather.Observations(ctx, ask.station, reportHours)

			switch {
			case err != nil && ctx.Err() != nil:
				return nil, ctx.Err()
			case err != nil:
				failed[ask.station] = err
			case len(obs) == 0:
				failed[ask.station] = fmt.Errorf("%s: no report in %d hours", ask.station, reportHours)
			default:
				got[ask.station] = obs
			}
		}

		if err := failed[ask.station]; err != nil {
			out.Fail(item, src, err)
			continue
		}

		j := maps.Clone(item.JSON)
		if j == nil {
			j = map[string]any{}
		}

		j[ask.field] = readingJSON(ask, got[ask.station], n.now())
		out.Add(engine.DefaultPort, engine.Item{JSON: j, Source: []engine.ItemRef{src}})
	}

	return out, nil
}

func airportAskOf(p engine.Params) (airportAsk, error) {
	var (
		a   airportAsk
		err error
	)

	if a.city, a.hasCity, err = cityOf(p); err != nil {
		return a, err
	}

	if a.hasCity {
		a.station = a.city.Station
	}

	if p["station"] != nil {
		s, err := p.String("station")
		if err != nil {
			return a, err
		}

		if s = strings.ToUpper(strings.TrimSpace(s)); s != "" {
			if !icao.MatchString(s) {
				return a, fmt.Errorf("%w %q: %q is not a station's code: four letters or digits, like KLGA", engine.ErrInvalidParam, "station", s)
			}

			a.station = s
		}
	}

	if a.station == "" {
		return a, fmt.Errorf("%w: no station: pick a city, or give a station like KLGA", engine.ErrInvalidParam)
	}

	zone := ""
	if a.hasCity {
		zone = a.city.Zone
	}

	if p["zone"] != nil {
		z, err := p.String("zone")
		if err != nil {
			return a, err
		}

		if strings.TrimSpace(z) != "" {
			zone = z
		}
	}

	if a.loc, err = zones.Load(zone); err != nil {
		return a, fmt.Errorf("%w %q: %w", engine.ErrInvalidParam, "zone", err)
	}

	if a.f, err = fahrenheit(p, a.city, a.hasCity); err != nil {
		return a, err
	}

	a.field = "airport"
	if p["field"] != nil {
		if a.field, err = p.String("field"); err != nil {
			return a, err
		}
	}

	return a, nil
}

// readingJSON is the latest report and the local day's routine ones, as the item has them.
func readingJSON(a airportAsk, obs []ports.Observation, now time.Time) map[string]any {
	sorted := slices.SortedFunc(slices.Values(obs), func(x, y ports.Observation) int { return y.ObservedAt.Compare(x.ObservedAt) })
	latest := sorted[0]

	in := func(c *float64) any {
		switch {
		case c == nil:
			return nil
		case a.f:
			return toF(*c)
		default:
			return *c
		}
	}

	intOrNil := func(v *int) any {
		if v == nil {
			return nil
		}

		return *v
	}

	var (
		high, low *float64
		counted   int
	)

	date := now.In(a.loc).Format(time.DateOnly)

	for _, o := range sorted {
		if !o.Routine || o.TempC == nil || o.ObservedAt.In(a.loc).Format(time.DateOnly) != date {
			continue
		}

		counted++
		t := *o.TempC

		if high == nil || t > *high {
			high = &t
		}

		if low == nil || t < *low {
			low = &t
		}
	}

	ptr := func(f *float64) any {
		if f == nil {
			return nil
		}

		return *f
	}

	out := map[string]any{
		"station": latest.Station, "name": latest.Name, "city": nil, "observedAt": latest.ObservedAt.UTC(),
		"routine": latest.Routine, "temp": in(latest.TempC), "tempC": ptr(latest.TempC), "dewpoint": in(latest.DewpointC),
		"unit": unitName(a.f),
		"wind": map[string]any{"dir": intOrNil(latest.WindDir), "speedKt": intOrNil(latest.WindKt), "gustKt": intOrNil(latest.GustKt)},
		"raw":  latest.Raw,
		"today": map[string]any{
			"date": date, "zone": a.loc.String(), "high": in(high), "low": in(low), "highC": ptr(high), "lowC": ptr(low), "reports": counted,
		},
	}

	if a.hasCity {
		out["city"] = a.city.Name

		if a.city.Note != "" {
			out["note"] = a.city.Note
		}
	}

	return out
}
