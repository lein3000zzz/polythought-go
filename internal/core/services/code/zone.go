package code

import (
	"encoding/json"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/nodes/zones"
)

// maxPeriods bounds the periods a zone's answer holds: a year either side has two or
// three changes in most zones.
const maxPeriods = 16

// zonePeriod is a stretch of time a zone keeps one offset, its ends in Unix milliseconds,
// null where it has none.
type zonePeriod struct {
	From         *int64 `json:"from"`
	Until        *int64 `json:"until"`
	Offset       int    `json:"offset"` // seconds east of UTC
	Abbreviation string `json:"abbreviation"`
	DST          bool   `json:"dst"`
}

// zoneOf answers the SDK's zone(name): the zone's periods from a year before now to a
// year after, which it reads any time between by, daylight saving and all (D440). The
// engine the code runs in has no zone database of its own.
func zoneOf(raw json.RawMessage, now time.Time) (any, error) {
	var args struct {
		Name string `json:"name"`
	}

	if err := decode(raw, &args); err != nil {
		return nil, err
	}

	loc, err := zones.Load(args.Name)
	if err != nil {
		return nil, err
	}

	end := now.AddDate(1, 0, 0)
	periods := []zonePeriod{}

	for t := now.AddDate(-1, 0, 0); len(periods) < maxPeriods; {
		local := t.In(loc)
		abbreviation, offset := local.Zone()
		from, until := local.ZoneBounds()

		p := zonePeriod{Offset: offset, Abbreviation: abbreviation, DST: local.IsDST()}
		if !from.IsZero() {
			ms := from.UnixMilli()
			p.From = &ms
		}

		if !until.IsZero() {
			ms := until.UnixMilli()
			p.Until = &ms
		}

		periods = append(periods, p)

		if until.IsZero() || !until.Before(end) {
			break
		}

		t = until
	}

	return map[string]any{"name": loc.String(), "periods": periods}, nil
}
