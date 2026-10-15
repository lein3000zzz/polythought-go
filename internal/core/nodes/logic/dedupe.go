package logic

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/fields"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
)

const DedupeType = "logic.dedupe"

const (
	// maxRemembered bounds what one node remembers; past it the oldest go first.
	maxRemembered = 10_000

	// sweepEvery is how often a run also forgets expired items of every other node, so
	// deleted workflows do not hold memory forever.
	sweepEvery = 10 * time.Minute
)

var _ engine.Node = (*Dedupe)(nil)

// Dedupe passes an item only the first time it is seen, so a schedule that finds the
// same thing every few minutes reports it once. It remembers in memory, per workflow
// node: a restart forgets, and runs started by hand count too.
type Dedupe struct {
	now func() time.Time

	mu        sync.Mutex
	seen      map[dedupeKey]map[string]time.Time // item key -> when it was first seen
	lastSweep time.Time
}

type dedupeKey struct {
	workflow domain.WorkflowID
	node     domain.NodeID
}

func NewDedupe() *Dedupe {
	return &Dedupe{now: time.Now, seen: make(map[dedupeKey]map[string]time.Time)}
}

// WithClock replaces time.Now, for tests.
func (d *Dedupe) WithClock(now func() time.Time) *Dedupe {
	d.now = now
	return d
}

func (*Dedupe) Type() engine.NodeType {
	return engine.NodeType{
		Name:  DedupeType,
		Label: "Only new",
		Description: "Passes an item only the first time it shows up, so an alert fires once instead of " +
			"on every run. It remembers in memory: a server restart forgets.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort},
		Items:   shape.Passed(),
		Params: []engine.ParamSpec{
			{
				Name: "field", Label: "Identify items by", Kind: engine.ParamString, Default: "id",
				Description: "The field that tells items apart, e.g. id, tokenId or txHash.",
			},
			{
				Name: "forget", Label: "New again after", Kind: engine.ParamDuration, Default: "24h",
				Description: "How long an item counts as seen.",
			},
		},
	}
}

func (d *Dedupe) Run(_ context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}
	key := dedupeKey{workflow: rc.WorkflowID, node: rc.NodeID}
	now := d.now()

	d.mu.Lock()
	defer d.mu.Unlock()

	d.sweep(now)

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		field, err := p.String("field")
		if err != nil {
			return nil, err
		}

		forget, err := p.Duration("forget")
		if err != nil {
			return nil, err
		}

		v, ok := fields.Lookup(item.JSON, field)
		if !ok || v == nil {
			out.Fail(item, src, fmt.Errorf("field %q is missing, so the item cannot be told apart", field))
			continue
		}

		if d.remember(key, fields.Format(v), now, forget) {
			out.Add(engine.DefaultPort, engine.Item{JSON: item.JSON, Source: []engine.ItemRef{src}})
		}
	}

	return out, nil
}

// remember records id and reports whether it is new: never seen, or seen longer than
// forget ago.
func (d *Dedupe) remember(key dedupeKey, id string, now time.Time, forget time.Duration) bool {
	seen := d.seen[key]
	if seen == nil {
		seen = make(map[string]time.Time)
		d.seen[key] = seen
	}

	if at, ok := seen[id]; ok && now.Sub(at) < forget {
		return false
	}

	if len(seen) >= maxRemembered {
		forgetOldest(seen)
	}

	seen[id] = now

	return true
}

// sweep drops items no node can still count as seen. The longest "forget" any node uses
// is unknown here, so it keeps a generous week.
func (d *Dedupe) sweep(now time.Time) {
	if now.Sub(d.lastSweep) < sweepEvery {
		return
	}

	d.lastSweep = now

	for key, seen := range d.seen {
		for id, at := range seen {
			if now.Sub(at) > 7*24*time.Hour {
				delete(seen, id)
			}
		}

		if len(seen) == 0 {
			delete(d.seen, key)
		}
	}
}

func forgetOldest(seen map[string]time.Time) {
	var (
		oldest   string
		oldestAt time.Time
	)

	for id, at := range seen {
		if oldestAt.IsZero() || at.Before(oldestAt) {
			oldest, oldestAt = id, at
		}
	}

	delete(seen, oldest)
}
