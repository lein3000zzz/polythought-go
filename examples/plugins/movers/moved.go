package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/lein3000zzz/polythought-go/pkg/plugin"
)

// moved is the Market moved trigger: it looks at the markets its search finds every so
// often, and starts a run with those whose first outcome moved past the threshold since it
// last said so. The price it last said so at is in its store, by market, so a restart, or
// the server's, does not say it again.
type moved struct{}

func (moved) Start(ctx context.Context, run plugin.Run, params map[string]any, _ bool, emit plugin.Emit) error {
	w := watch{
		host:    run.Host,
		query:   text(params["query"]),
		move:    float64(whole(params["move"], whole(run.Settings["move"], 5))) / 100,
		markets: min(int(whole(params["markets"], 10)), 50),
	}

	every, err := time.ParseDuration(text(run.Settings["every"]))
	if err != nil || every < time.Minute {
		every = 5 * time.Minute
	}

	tick := time.NewTicker(every)
	defer tick.Stop()

	for {
		movers, err := w.look(ctx)

		switch {
		case err != nil:
			// The next look may do better: Polymarket's search, or the store, may be back.
			run.Log.Warn("Markets not looked at", "error", err)
		case len(movers) > 0:
			_ = run.Host.Metric(ctx, "moves_total", float64(len(movers)), nil)

			if err := emit(ctx, plugin.Ports{plugin.MainPort: movers}); err != nil && ctx.Err() == nil {
				run.Log.Warn("Moves' run failed", "error", err)
			}
		}

		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

// watch is what a Market moved looks at.
type watch struct {
	host    plugin.Host
	query   string
	move    float64 // a price's, 0.05 for 5 points
	markets int
}

// look finds the markets that moved past the threshold since they were last said to,
// keeping their prices now as those they were said to at. A market seen for the first time
// is kept, not said.
func (w watch) look(ctx context.Context) ([]plugin.Item, error) {
	markets, err := w.host.SearchMarkets(ctx, w.query, false, w.markets)
	if err != nil {
		return nil, err
	}

	var movers []plugin.Item

	for _, m := range markets {
		if len(m.Outcomes) == 0 {
			continue
		}

		outcome := m.Outcomes[0]

		now, err := strconv.ParseFloat(string(outcome.Price), 64)
		if err != nil {
			continue // no price yet
		}

		key := "said:" + m.ID

		var said plugin.Decimal

		known, err := w.host.Get(ctx, key, &said)
		if err != nil {
			return movers, err
		}

		before, err := strconv.ParseFloat(string(said), 64)

		switch {
		case !known || err != nil:
			// First seen: what it moves from.
		case math.Abs(now-before) < w.move:
			continue
		default:
			movers = append(movers, plugin.Item{JSON: map[string]any{
				"market": m.ID, "slug": m.Slug, "question": m.Question, "outcome": outcome.Label,
				"from": string(said), "to": string(outcome.Price),
			}})
		}

		if err := w.host.Set(ctx, key, outcome.Price); err != nil {
			return movers, err
		}
	}

	return movers, nil
}

// text is a setting as text: empty when it is not.
func text(v any) string {
	s, _ := v.(string)
	return s
}

// whole is a whole number setting, as JSON brings it, or otherwise when it is not set.
func whole(v any, otherwise int64) int64 {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil && i > 0 {
			return i
		}
	case float64:
		if n > 0 {
			return int64(n)
		}
	case int64:
		if n > 0 {
			return n
		}
	}

	return otherwise
}

// points says a move in percentage points, signed: +6, -12.
func points(from, to string) string {
	a, errA := strconv.ParseFloat(from, 64)
	b, errB := strconv.ParseFloat(to, 64)

	if errA != nil || errB != nil {
		return "?"
	}

	return fmt.Sprintf("%+.0f", (b-a)*100)
}
