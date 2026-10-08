package polymarket

import (
	"net/url"
	"strconv"
	"strings"
)

// Helpers that skip zero values, so unset query fields fall back to the API defaults.

func setString(v url.Values, key, value string) {
	if value != "" {
		v.Set(key, value)
	}
}

func setInt(v url.Values, key string, value int) {
	if value > 0 {
		v.Set(key, strconv.Itoa(value))
	}
}

func setInt64(v url.Values, key string, value int64) {
	if value > 0 {
		v.Set(key, strconv.FormatInt(value, 10))
	}
}

func setBool(v url.Values, key string, value *bool) {
	if value != nil {
		v.Set(key, strconv.FormatBool(*value))
	}
}

// addAll repeats the key per value (tag_id=1&tag_id=2). Gamma ignores the documented "key[]" form.
func addAll(v url.Values, key string, values []string) {
	for _, value := range values {
		v.Add(key, value)
	}
}

// setCSV joins values with commas (condition=a,b). The Data API rejects repeated keys.
func setCSV(v url.Values, key string, values []string) {
	if len(values) > 0 {
		v.Set(key, strings.Join(values, ","))
	}
}
