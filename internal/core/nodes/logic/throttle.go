package logic

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/fields"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	ThrottleType = "logic.throttle"

	// PortLimited takes the items over the limit (D143).
	PortLimited = "limited"
)

var _ engine.Node = (*Throttle)(nil)

// Throttle lets at most so many items through in the last period (D123, D143), counted
// per value of a field or all together, in node memory: across runs and restarts, two
// runs at once counting each other's.
type Throttle struct {
	memory ports.NodeMemory
	now    func() time.Time
}

func NewThrottle(memory ports.NodeMemory) *Throttle {
	return &Throttle{memory: memory, now: time.Now}
}

// WithClock replaces time.Now, for tests.
func (t *Throttle) WithClock(now func() time.Time) *Throttle {
	t.now = now
	return t
}

func (*Throttle) Type() engine.NodeType {
	return engine.NodeType{
		Name:  ThrottleType,
		Label: "Throttle",
		Description: "Lets at most so many items through in a period, like 3 copies of a trader a day, counted per " +
			"value of a field or all together; the rest leave from limited. It remembers across runs and restarts.",
		Inputs:    []string{engine.DefaultPort},
		Outputs:   []string{engine.DefaultPort, PortLimited},
		Items:     shape.Passed(),
		Remembers: true,
		Params: []engine.ParamSpec{
			{
				Name: "limit", Label: "At most", Kind: engine.ParamNumber, Required: true,
				Description: "How many items may go on in a period.",
			},
			{
				Name: "per", Label: "In", Kind: engine.ParamDuration, Default: "24h",
				Description: "The period they count over, back from each item: 24h is any 24 hours.",
			},
			{
				Name: "field", Label: "Count per", Kind: engine.ParamString, Field: true,
				Description: "Counts each value of this field on its own, e.g. trader.address for each trader. " +
					"Empty: all items together.",
			},
		},
	}
}

func (t *Throttle) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	// The settings cannot depend on an item: the items are counted together.
	p, err := rc.Params.Resolve(engine.ItemRef{Port: engine.DefaultPort})
	if err != nil {
		return nil, err
	}

	limit, err := p.Int("limit")
	if err != nil {
		return nil, err
	}

	if limit < 1 {
		return nil, fmt.Errorf("%w %q: let at least one item through", engine.ErrInvalidParam, "limit")
	}

	per, err := p.Duration("per")
	if err != nil {
		return nil, err
	}

	field, _ := p.String("field") // empty: all together

	type entry struct {
		item   engine.Item
		src    engine.ItemRef
		member string
	}

	var (
		out   = engine.Ports{}
		all   []entry
		keys  []string // in the order first seen
		byKey = map[string][]entry{}
	)

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}
		key := ""

		if field != "" {
			v, ok := fields.Lookup(item.JSON, field)
			if !ok || v == nil {
				out.Fail(item, src, fmt.Errorf("field %q is missing, so the item cannot be counted", field))
				continue
			}

			key = fields.Format(v)
		}

		if _, seen := byKey[key]; !seen {
			keys = append(keys, key)
		}

		e := entry{item: item, src: src, member: uuid.NewString()}
		all = append(all, e)
		byKey[key] = append(byKey[key], e)
	}

	now := t.now()
	through := map[string]bool{}

	for _, key := range keys {
		entries := byKey[key]

		members := make([]string, len(entries))
		for i, e := range entries {
			members[i] = e.member
		}

		admitted, err := t.memory.Admit(ctx, rc.WorkflowID, rc.NodeID, key, members, limit, now.Add(-per), now, now.Add(per))
		if err != nil {
			return nil, fmt.Errorf("count the items: %w", err)
		}

		for _, m := range admitted {
			through[m] = true
		}
	}

	for _, e := range all { // in the order they came
		port := PortLimited
		if through[e.member] {
			port = engine.DefaultPort
		}

		out.Add(port, engine.Item{JSON: e.item.JSON, Source: []engine.ItemRef{e.src}})
	}

	return out, nil
}
