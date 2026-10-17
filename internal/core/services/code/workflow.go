package code

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// built is a workflow as @polythought/workflow makes it.
type built struct {
	Name  *string `json:"name"`
	Nodes *[]struct {
		ID             string         `json:"id"`
		Type           string         `json:"type"`
		Name           string         `json:"name"`
		Notes          string         `json:"notes"`
		Params         map[string]any `json:"params"`
		ContinueOnFail bool           `json:"continueOnFail"`
	} `json:"nodes"`
	Connections *[]struct {
		From     string `json:"from"`
		FromPort string `json:"fromPort"`
		To       string `json:"to"`
		ToPort   string `json:"toPort"`
	} `json:"connections"`
}

// Workflow runs workflow code (D74): compiled with the builder, then run in the sandbox
// with no data requests, under the configured limits. It gives the workflow the code
// exports by default, without positions. Code that fails to compile or to run, or that
// exports something else, is a *domain.CodeError.
func (s *Service) Workflow(ctx context.Context, source string) (domain.Workflow, error) {
	code, err := s.compiler.CompileWorkflow(ctx, source)
	if err != nil {
		return domain.Workflow{}, err
	}

	res, err := s.runner.Run(ctx, ports.CodeJob{Source: code.Script, Entry: code.Invoke, Input: json.RawMessage("null")})
	if err != nil {
		return domain.Workflow{}, err
	}

	if len(res.Output) == 0 || string(res.Output) == "null" {
		return domain.Workflow{}, &domain.CodeError{
			Kind:    domain.CodeNoEntry,
			Message: `the code exports no workflow: end it with export default workflow("Name", (w) => { ... })`,
		}
	}

	var b built

	dec := json.NewDecoder(bytes.NewReader(res.Output))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&b); err != nil || b.Name == nil || b.Nodes == nil || b.Connections == nil {
		return domain.Workflow{}, &domain.CodeError{
			Kind:    domain.CodeNoEntry,
			Message: "what the code exports by default is not a workflow: make it with workflow() from @polythought/workflow",
		}
	}

	w := domain.Workflow{
		Name:        *b.Name,
		Nodes:       make([]domain.NodeSpec, len(*b.Nodes)),
		Connections: make([]domain.Connection, len(*b.Connections)),
	}

	for i, n := range *b.Nodes {
		w.Nodes[i] = domain.NodeSpec{
			ID: domain.NodeID(n.ID), Type: n.Type, Name: n.Name, Notes: n.Notes, Params: n.Params, ContinueOnFail: n.ContinueOnFail,
		}
	}

	for i, c := range *b.Connections {
		w.Connections[i] = domain.Connection{From: domain.NodeID(c.From), FromPort: c.FromPort, To: domain.NodeID(c.To), ToPort: c.ToPort}
	}

	return w, nil
}
