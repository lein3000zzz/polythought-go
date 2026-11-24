// Package weather holds the weather nodes (D441): forecasts from several models side by
// side, and an airport's latest observation with the day's high and low so far, for the
// temperature markets. A city from the preset gives the place, its station, its time zone
// and its unit; coordinates or a station by hand do too.
package weather

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/cities"
)

// Units a node's temperatures may be in: the city's, or one said.
const (
	UnitCity = "city"
	UnitF    = "F"
	UnitC    = "C"
)

func cityParam() engine.ParamSpec {
	return engine.ParamSpec{
		Name: "city", Label: "City", Kind: engine.ParamString, Options: cities.Names(),
		Description: "One of the cities Polymarket lists temperature markets for: the station its markets resolve by, " +
			"its time zone and its unit. Or the place by hand, below.",
	}
}

func unitParam() engine.ParamSpec {
	return engine.ParamSpec{
		Name: "unit", Label: "Unit", Kind: engine.ParamString, Default: UnitCity, Options: []string{UnitCity, UnitF, UnitC},
		Description: "city: the unit its markets are in, °F in the US, °C elsewhere and by hand. F or C: that one.",
	}
}

// cityOf is the item's city, if its settings name one.
func cityOf(p engine.Params) (cities.City, bool, error) {
	if p["city"] == nil {
		return cities.City{}, false, nil
	}

	name, err := p.String("city")
	if err != nil || strings.TrimSpace(name) == "" {
		return cities.City{}, false, err
	}

	c, ok := cities.Find(name)
	if !ok {
		return cities.City{}, false, fmt.Errorf("%w %q: %q is not one of the cities", engine.ErrInvalidParam, "city", name)
	}

	return c, true, nil
}

// fahrenheit says whether the node's temperatures are in °F.
func fahrenheit(p engine.Params, city cities.City, hasCity bool) (bool, error) {
	unit := UnitCity
	if p["unit"] != nil {
		var err error
		if unit, err = p.String("unit"); err != nil {
			return false, err
		}
	}

	switch unit {
	case UnitF:
		return true, nil
	case UnitC:
		return false, nil
	default:
		return hasCity && city.Fahrenheit, nil
	}
}

func unitName(f bool) string {
	if f {
		return UnitF
	}

	return UnitC
}

// tenth is x to one decimal place.
func tenth(x float64) float64 { return math.Round(x*10) / 10 }

// toF is °C as °F, to the tenth.
func toF(c float64) float64 { return tenth(c*9/5 + 32) }

var errNoPlace = errors.New("no place: pick a city, or give a latitude and a longitude")
