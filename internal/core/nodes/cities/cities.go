// Package cities is the preset of the cities Polymarket lists temperature markets for
// (D441): each with the station its markets resolve by, that station's coordinates, the
// city's time zone and the unit its markets' buckets are in. Built from the markets' rules
// and aviationweather.gov's station list, as of November 2026.
package cities

import (
	"slices"
	"strings"
)

// City is one city Polymarket lists temperature markets for.
type City struct {
	// Name is as the markets' titles have it: NYC, Seoul (Incheon).
	Name string
	// Station is the ICAO code of the airport its markets resolve by.
	Station string
	// Latitude and Longitude are the station's.
	Latitude, Longitude float64
	// Zone is the city's time zone, whose dates the markets are for.
	Zone string
	// Fahrenheit says its markets' buckets are in °F; else °C.
	Fahrenheit bool
	// Note says what the markets resolve by when it is not the station's METARs.
	Note string
}

// All are the cities, by name.
var All = []City{
	{Name: "Amsterdam", Station: "EHAM", Latitude: 52.315, Longitude: 4.79, Zone: "Europe/Amsterdam"},
	{Name: "Ankara", Station: "LTAC", Latitude: 40.128, Longitude: 32.995, Zone: "Europe/Istanbul"},
	{Name: "Atlanta", Station: "KATL", Latitude: 33.62972, Longitude: -84.44223, Zone: "America/New_York", Fahrenheit: true},
	{Name: "Austin", Station: "KAUS", Latitude: 30.1831, Longitude: -97.68063, Zone: "America/Chicago", Fahrenheit: true},
	{Name: "Beijing", Station: "ZBAA", Latitude: 40.082, Longitude: 116.603, Zone: "Asia/Shanghai"},
	{Name: "Buenos Aires", Station: "SAEZ", Latitude: -34.822, Longitude: -58.536, Zone: "America/Argentina/Buenos_Aires"},
	{Name: "Busan", Station: "RKPK", Latitude: 35.179, Longitude: 128.938, Zone: "Asia/Seoul"},
	{Name: "Cape Town", Station: "FACT", Latitude: -33.965, Longitude: 18.602, Zone: "Africa/Johannesburg"},
	{Name: "Chengdu", Station: "ZUUU", Latitude: 30.576, Longitude: 103.95, Zone: "Asia/Shanghai"},
	{Name: "Chicago", Station: "KORD", Latitude: 41.96017, Longitude: -87.93161, Zone: "America/Chicago", Fahrenheit: true},
	{Name: "Chongqing", Station: "ZUCK", Latitude: 29.718, Longitude: 106.639, Zone: "Asia/Shanghai"},
	{Name: "Dallas", Station: "KDAL", Latitude: 32.83836, Longitude: -96.83584, Zone: "America/Chicago", Fahrenheit: true},
	{Name: "Denver", Station: "KDEN", Latitude: 39.84657, Longitude: -104.65623, Zone: "America/Denver", Fahrenheit: true},
	{Name: "Guangzhou", Station: "ZGGG", Latitude: 23.392, Longitude: 113.307, Zone: "Asia/Shanghai"},
	{Name: "Helsinki", Station: "EFHK", Latitude: 60.327, Longitude: 24.957, Zone: "Europe/Helsinki"},
	{
		Name: "Hong Kong", Station: "VHHH", Latitude: 22.302, Longitude: 114.174, Zone: "Asia/Hong_Kong",
		Note: "Its markets resolve by the Hong Kong Observatory's daily extract, not by the airport's METARs; its coordinates are the Observatory's.",
	},
	{Name: "Houston", Station: "KHOU", Latitude: 29.64582, Longitude: -95.28214, Zone: "America/Chicago", Fahrenheit: true},
	{Name: "Istanbul", Station: "LTFM", Latitude: 41.262, Longitude: 28.74, Zone: "Europe/Istanbul"},
	{Name: "Jeddah", Station: "OEJN", Latitude: 21.685, Longitude: 39.166, Zone: "Asia/Riyadh"},
	{
		Name: "Jinan", Station: "ZSJN", Latitude: 36.856, Longitude: 117.206, Zone: "Asia/Shanghai",
		Note: "Its markets resolve by Weather Underground's daily history for the station, in whole degrees.",
	},
	{Name: "Karachi", Station: "OPKC", Latitude: 24.902, Longitude: 67.139, Zone: "Asia/Karachi"},
	{Name: "Kuala Lumpur", Station: "WMKK", Latitude: 2.747, Longitude: 101.714, Zone: "Asia/Kuala_Lumpur"},
	{Name: "London", Station: "EGLC", Latitude: 51.505, Longitude: 0.055, Zone: "Europe/London"},
	{Name: "Los Angeles", Station: "KLAX", Latitude: 33.93817, Longitude: -118.3866, Zone: "America/Los_Angeles", Fahrenheit: true},
	{Name: "Lucknow", Station: "VILK", Latitude: 26.761, Longitude: 80.889, Zone: "Asia/Kolkata"},
	{Name: "Madrid", Station: "LEMD", Latitude: 40.466, Longitude: -3.555, Zone: "Europe/Madrid"},
	{Name: "Manila", Station: "RPLL", Latitude: 14.507, Longitude: 121.004, Zone: "Asia/Manila"},
	{Name: "Mexico City", Station: "MMMX", Latitude: 19.436, Longitude: -99.072, Zone: "America/Mexico_City"},
	{Name: "Miami", Station: "KMIA", Latitude: 25.78806, Longitude: -80.31692, Zone: "America/New_York", Fahrenheit: true},
	{Name: "Milan", Station: "LIMC", Latitude: 45.631, Longitude: 8.728, Zone: "Europe/Rome"},
	{Name: "Moscow", Station: "UUWW", Latitude: 55.592, Longitude: 37.261, Zone: "Europe/Moscow"},
	{Name: "Munich", Station: "EDDM", Latitude: 48.348, Longitude: 11.813, Zone: "Europe/Berlin"},
	{Name: "NYC", Station: "KLGA", Latitude: 40.77945, Longitude: -73.88027, Zone: "America/New_York", Fahrenheit: true},
	{Name: "Panama City", Station: "MPMG", Latitude: 8.967, Longitude: -79.555, Zone: "America/Panama"},
	{Name: "Paris", Station: "LFPB", Latitude: 48.967, Longitude: 2.428, Zone: "Europe/Paris"},
	{Name: "Qingdao", Station: "ZSQD", Latitude: 36.362, Longitude: 120.087, Zone: "Asia/Shanghai"},
	{Name: "San Francisco", Station: "KSFO", Latitude: 37.61961, Longitude: -122.36561, Zone: "America/Los_Angeles", Fahrenheit: true},
	{Name: "Sao Paulo", Station: "SBGR", Latitude: -23.432, Longitude: -46.469, Zone: "America/Sao_Paulo"},
	{Name: "Seattle", Station: "KSEA", Latitude: 47.44467, Longitude: -122.31442, Zone: "America/Los_Angeles", Fahrenheit: true},
	{Name: "Seoul (Incheon)", Station: "RKSI", Latitude: 37.469, Longitude: 126.451, Zone: "Asia/Seoul"},
	{Name: "Shanghai", Station: "ZSPD", Latitude: 31.146, Longitude: 121.8, Zone: "Asia/Shanghai"},
	{Name: "Shenzhen", Station: "ZGSZ", Latitude: 22.639, Longitude: 113.803, Zone: "Asia/Shanghai"},
	{Name: "Singapore", Station: "WSSS", Latitude: 1.368, Longitude: 103.982, Zone: "Asia/Singapore"},
	{Name: "Taipei", Station: "RCSS", Latitude: 25.069, Longitude: 121.552, Zone: "Asia/Taipei"},
	{Name: "Tel Aviv", Station: "LLBG", Latitude: 32.011, Longitude: 34.887, Zone: "Asia/Jerusalem"},
	{Name: "Tokyo", Station: "RJTT", Latitude: 35.553, Longitude: 139.781, Zone: "Asia/Tokyo"},
	{Name: "Toronto", Station: "CYYZ", Latitude: 43.679, Longitude: -79.629, Zone: "America/Toronto"},
	{Name: "Warsaw", Station: "EPWA", Latitude: 52.163, Longitude: 20.961, Zone: "Europe/Warsaw"},
	{Name: "Wellington", Station: "NZWN", Latitude: -41.331, Longitude: 174.806, Zone: "Pacific/Auckland"},
	{Name: "Wuhan", Station: "ZHHH", Latitude: 30.783, Longitude: 114.205, Zone: "Asia/Shanghai"},
	{
		Name: "Zhengzhou", Station: "ZHCC", Latitude: 34.52, Longitude: 113.834, Zone: "Asia/Shanghai",
		Note: "Its markets resolve by Weather Underground's daily history for the station, in whole degrees.",
	},
}

// Names are the cities' names, as settings offer them.
func Names() []string {
	out := make([]string, len(All))
	for i, c := range All {
		out[i] = c.Name
	}

	return out
}

// Find is the city named, its letter case aside.
func Find(name string) (City, bool) {
	name = strings.TrimSpace(name)

	i := slices.IndexFunc(All, func(c City) bool { return strings.EqualFold(c.Name, name) })
	if i < 0 {
		return City{}, false
	}

	return All[i], true
}
