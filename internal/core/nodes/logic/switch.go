package logic

import (
	"context"
	"fmt"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
)

const (
	SwitchType = "logic.switch"

	// PortOther takes the items no rule takes.
	PortOther = "other"
)

var _ engine.Node = Switch{}

// ruleColumns are a row of Rules: the port, and a test like If's.
var ruleColumns = append([]engine.ParamSpec{{
	Name: "port", Label: "Port", Kind: engine.ParamString, Required: true,
	Description: "The output port the items this rule takes leave from.",
}}, moreTestColumns...)

// Switch sends each item out of the port of the first rule it passes (D129, D130), or
// out of other. Rules are tests like If's, tried in order; a field a rule cannot test
// fails the item, as there is no telling where it goes.
type Switch struct{}

func (Switch) Type() engine.NodeType {
	return engine.NodeType{
		Name:  SwitchType,
		Label: "Switch",
		Description: "Sends each item out of the port of the first rule it passes, each rule a test like If's; " +
			"the items no rule takes leave from other.",
		Inputs:        []string{engine.DefaultPort},
		Outputs:       []string{PortOther},
		OutputsParam:  "rules",
		OutputsColumn: "port",
		Items:         shape.Passed(),
		Params: []engine.ParamSpec{{
			Name: "rules", Label: "Rules", Kind: engine.ParamTable, Columns: ruleColumns,
			Description: "A row each, tried in order: the port it sends to, and its test. Each port shows on the node.",
		}},
	}
}

func (Switch) Run(_ context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	// Rules take no expressions: their ports must be known when the workflow is saved.
	p, err := rc.Params.Resolve(engine.ItemRef{Port: engine.DefaultPort})
	if err != nil {
		return nil, err
	}

	rows, err := p.Table("rules", ruleColumns)
	if err != nil {
		return nil, err
	}

	type rule struct {
		port string
		test test
	}

	rules := make([]rule, len(rows))

	for i, row := range rows {
		port, err := row.String("port")
		if err != nil {
			return nil, err
		}

		t, err := rowTest(row, fmt.Sprintf(`"rules": in row %d`, i+1))
		if err != nil {
			return nil, err
		}

		// Every rule now, not only those an item gets to.
		if err := t.valid(); err != nil {
			return nil, err
		}

		rules[i] = rule{port: port, test: t}
	}

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}
		port := PortOther

		var itemErr error

		for _, r := range rules {
			ok, bad, err := r.test.check(item)
			if err != nil {
				return nil, err
			}

			if bad != nil || ok {
				port, itemErr = r.port, bad
				break
			}
		}

		if itemErr != nil {
			out.Fail(item, src, itemErr)
			continue
		}

		out.Add(port, engine.Item{JSON: item.JSON, Source: []engine.ItemRef{src}})
	}

	return out, nil
}
