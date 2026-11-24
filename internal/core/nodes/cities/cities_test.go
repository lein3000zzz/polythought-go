package cities_test

import (
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/nodes/cities"
)

func TestCities(t *testing.T) {
	icao := regexp.MustCompile(`^[A-Z0-9]{4}$`)
	names := cities.Names()

	assert.True(t, slices.IsSorted(names), "by name")
	assert.Len(t, slices.Compact(slices.Clone(names)), len(names), "each once")

	for _, c := range cities.All {
		t.Run(c.Name, func(t *testing.T) {
			assert.Regexp(t, icao, c.Station)
			assert.True(t, c.Latitude >= -90 && c.Latitude <= 90 && c.Longitude >= -180 && c.Longitude <= 180)

			_, err := time.LoadLocation(c.Zone)
			require.NoError(t, err, c.Zone)
		})
	}

	nyc, ok := cities.Find(" nyc ")
	require.True(t, ok)
	assert.Equal(t, "KLGA", nyc.Station)
	assert.True(t, nyc.Fahrenheit)

	london, _ := cities.Find("London")
	assert.False(t, london.Fahrenheit, "°C outside the US")

	_, ok = cities.Find("Atlantis")
	assert.False(t, ok)
}
