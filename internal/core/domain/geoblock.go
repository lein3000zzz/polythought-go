package domain

import "time"

// Geoblock is Polymarket's answer on whether it takes orders from the server's IP (D186):
// Blocked where it refuses them, or where only its website is restricted, which the
// answer does not tell apart. Country is ISO 3166-1, Region its subdivision.
type Geoblock struct {
	Blocked   bool
	IP        string
	Country   string
	Region    string
	CheckedAt time.Time
}
