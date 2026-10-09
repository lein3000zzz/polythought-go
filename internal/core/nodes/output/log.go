// Package output holds nodes that send items out of a workflow.
package output

import (
	"context"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

const LogType = "output.log"

var _ engine.Node = (*Log)(nil)

// Log writes each item to the application log and passes it on unchanged.
type Log struct {
	log logging.Logger
}

func NewLog(log logging.Logger) *Log {
	return &Log{log: log.With("component", "node.log")}
}

func (*Log) Type() engine.NodeType {
	return engine.NodeType{
		Name:        LogType,
		Label:       "Log",
		Description: "Writes each item to the server log and passes it on.",
		Inputs:      []string{engine.DefaultPort},
		Outputs:     []string{engine.DefaultPort},
		Params: []engine.ParamSpec{
			{Name: "message", Label: "Message", Kind: engine.ParamString, Default: "Workflow item"},
		},
	}
}

func (n *Log) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		message, err := p.String("message")
		if err != nil {
			return nil, err
		}

		n.log.Info(ctx, message,
			"workflow_id", rc.WorkflowID,
			"execution_id", rc.ExecutionID,
			"node_id", rc.NodeID,
			"item", item.JSON,
		)

		out.Add(engine.DefaultPort, engine.Item{JSON: item.JSON, Source: []engine.ItemRef{src}})
	}

	return out, nil
}
