package logic

import (
	"context"
	"fmt"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/fields"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const DedupeType = "logic.dedupe"

var _ engine.Node = (*Dedupe)(nil)

// Dedupe passes an item only the first time it is seen, so a schedule that finds the
// same thing every few minutes reports it once. It remembers in node memory (D121): per
// workflow node, across runs and restarts, up to ports.MaxRemembered items, the oldest
// forgotten first. Runs started by hand count too.
type Dedupe struct {
	memory ports.NodeMemory
	now    func() time.Time
}

func NewDedupe(memory ports.NodeMemory) *Dedupe {
	return &Dedupe{memory: memory, now: time.Now}
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
			"on every run. It remembers across runs and restarts, up to 10,000 items.",
		Inputs:    []string{engine.DefaultPort},
		Outputs:   []string{engine.DefaultPort},
		Items:     shape.Passed(),
		Remembers: true,
		Params: []engine.ParamSpec{
			{
				Name: "field", Label: "Identify items by", Kind: engine.ParamString, Default: "id", Field: true,
				Description: "The field that tells items apart, e.g. id, tokenId or txHash.",
			},
			{
				Name: "forget", Label: "New again after", Kind: engine.ParamDuration, Default: "24h",
				Description: "How long an item counts as seen. A change applies to the items seen after it.",
			},
		},
	}
}

func (d *Dedupe) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	now := d.now()

	type seen struct {
		item engine.Item
		src  engine.ItemRef
		key  string
	}

	var (
		items []seen
		byTTL = map[time.Duration][]string{} // keys by how long they are remembered
		out   = engine.Ports{}
	)

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

		key := fields.Format(v)
		items = append(items, seen{item: item, src: src, key: key})
		byTTL[forget] = append(byTTL[forget], key)
	}

	fresh := map[string]bool{}

	for ttl, keys := range byTTL {
		got, err := d.memory.Remember(ctx, rc.WorkflowID, rc.NodeID, keys, now, now.Add(ttl))
		if err != nil {
			return nil, fmt.Errorf("remember the items: %w", err)
		}

		for _, k := range got {
			fresh[k] = true
		}
	}

	// In the order they came, a repeat within the run dropped too.
	for _, s := range items {
		if fresh[s.key] {
			delete(fresh, s.key)
			out.Add(engine.DefaultPort, engine.Item{JSON: s.item.JSON, Source: []engine.ItemRef{s.src}})
		}
	}

	return out, nil
}
