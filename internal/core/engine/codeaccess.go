package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// CodeGate says whether a user may write code now, and run the workflows that have it:
// the admin's code switch (D44).
type CodeGate interface {
	CodeAllowed(ctx context.Context, owner domain.UserID) (bool, error)
}

// WithCodeGate fails executions of workflows with code nodes whose owner may not run
// code. Without it, everyone may.
func WithCodeGate(g CodeGate) ExecutorOption {
	return func(e *Executor) { e.codeGate = g }
}

// CodeNodes are the nodes of w whose types run code their owner wrote.
func (r *Registry) CodeNodes(w domain.Workflow) []domain.NodeID {
	var ids []domain.NodeID

	for _, n := range w.Nodes {
		if typ, _, ok := r.Type(n.Type); ok && typ.Code {
			ids = append(ids, n.ID)
		}
	}

	return ids
}

// CheckCodeAccess reports each code node of w when its owner may not write code, as gate
// says. Saving asks this. An error from gate comes back as it is, not as a problem.
func (r *Registry) CheckCodeAccess(ctx context.Context, w domain.Workflow, gate CodeGate) error {
	ids := r.CodeNodes(w)
	if len(ids) == 0 {
		return nil
	}

	allowed, err := gate.CodeAllowed(ctx, w.OwnerID)
	if err != nil || allowed {
		return err
	}

	var v domain.ValidationError

	for _, id := range ids {
		v.Add(domain.Problem{NodeID: id, Err: domain.ErrCodeOff, Message: fmt.Sprintf("node %q: %v", id, domain.ErrCodeOff)})
	}

	return v.Err()
}

// checkCode fails an execution of a workflow with code nodes whose owner may not run
// code, before any node runs (D47).
func (e *Executor) checkCode(ctx context.Context, w domain.Workflow) error {
	if e.codeGate == nil {
		return nil
	}

	ids := e.registry.CodeNodes(w)
	if len(ids) == 0 {
		return nil
	}

	allowed, err := e.codeGate.CodeAllowed(ctx, w.OwnerID)
	if err != nil {
		return fmt.Errorf("could not tell whether the owner may run code: %w", err)
	}

	if !allowed {
		names := make([]string, len(ids))
		for i, id := range ids {
			names[i] = string(id)
		}

		return fmt.Errorf("%w, so the workflow does not run: it has code nodes (%s)", domain.ErrCodeOff, strings.Join(names, ", "))
	}

	return nil
}
