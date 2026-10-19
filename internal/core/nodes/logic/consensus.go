package logic

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/fields"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	ConsensusType = "logic.consensus"

	// PortAlone takes the items without enough agreement yet (D146).
	PortAlone = "alone"
)

var _ engine.Node = (*Consensus)(nil)

// Consensus lets an item on once enough different traders, or whatever its Who field
// names, were seen for what it agrees on within a window (D124): copy an outcome when 2
// of your traders bought it within 6 hours. From then on in the window, each such item
// goes on (D144); the ones before leave from alone. It remembers in node memory.
type Consensus struct {
	memory ports.NodeMemory
	now    func() time.Time
}

func NewConsensus(memory ports.NodeMemory) *Consensus {
	return &Consensus{memory: memory, now: time.Now}
}

// WithClock replaces time.Now, for tests.
func (c *Consensus) WithClock(now func() time.Time) *Consensus {
	c.now = now
	return c
}

func (*Consensus) Type() engine.NodeType {
	return engine.NodeType{
		Name:  ConsensusType,
		Label: "Consensus",
		Description: "Lets an item on once enough different traders agreed within a while: at 2 in 6h, a trade " +
			"goes on when another trader made the same one in the 6 hours before. The others leave from alone. " +
			"It remembers across runs and restarts.",
		Inputs:    []string{engine.DefaultPort},
		Outputs:   []string{engine.DefaultPort, PortAlone},
		Items:     shape.Passed(),
		Remembers: true,
		Params: []engine.ParamSpec{
			{
				Name: "needed", Label: "At least", Kind: engine.ParamNumber, Default: 2,
				Description: "How many different traders must agree, this item's among them.",
			},
			{
				Name: "within", Label: "Within", Kind: engine.ParamDuration, Default: "6h",
				Description: "How far back a trader's agreeing counts, from each item.",
			},
			{
				Name: "on", Label: "Agree on", Kind: engine.ParamList, Field: true, Default: []string{"tokenId", "side"},
				Description: "The fields that must be the same, one per line: the outcome and the side by default, " +
					"so a buy and a sell do not count together.",
			},
			{
				Name: "who", Label: "Who", Kind: engine.ParamString, Field: true, Default: "trader.address",
				Description: "The field that tells traders apart: one counts once, however often they trade.",
			},
		},
	}
}

func (c *Consensus) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	// The settings cannot depend on an item: the items are counted together.
	p, err := rc.Params.Resolve(engine.ItemRef{Port: engine.DefaultPort})
	if err != nil {
		return nil, err
	}

	needed, err := p.Int("needed")
	if err != nil {
		return nil, err
	}

	if needed < 1 {
		return nil, fmt.Errorf("%w %q: at least one trader", engine.ErrInvalidParam, "needed")
	}

	within, err := p.Duration("within")
	if err != nil {
		return nil, err
	}

	on, err := p.Strings("on")
	if err != nil || len(on) == 0 {
		return nil, fmt.Errorf("%w %q: name the fields to agree on", engine.ErrInvalidParam, "on")
	}

	who, err := p.String("who")
	if err != nil {
		return nil, err
	}

	out := engine.Ports{}
	now := c.now()

	// In the order they came: a trade agrees with the ones before it, this run's too.
	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		key, member, missing := agreement(item, on, who)
		if missing != "" {
			out.Fail(item, src, fmt.Errorf("field %q is missing, so the item cannot be counted", missing))
			continue
		}

		n, err := c.memory.Gather(ctx, rc.WorkflowID, rc.NodeID, key, member, now.Add(-within), now, now.Add(within))
		if err != nil {
			return nil, fmt.Errorf("count who agrees: %w", err)
		}

		port := PortAlone
		if n >= needed {
			port = engine.DefaultPort
		}

		out.Add(port, engine.Item{JSON: item.JSON, Source: []engine.ItemRef{src}})
	}

	return out, nil
}

// agreement is what an item agrees on, as one key, and who it is; or the field it lacks.
func agreement(item engine.Item, on []string, who string) (key, member, missing string) {
	values := make([]string, len(on))

	for i, f := range on {
		v, ok := fields.Lookup(item.JSON, f)
		if !ok || v == nil {
			return "", "", f
		}

		values[i] = fields.Format(v)
	}

	w, ok := fields.Lookup(item.JSON, who)
	if !ok || w == nil {
		return "", "", who
	}

	return strings.Join(values, "\x1f"), fields.Format(w), ""
}
