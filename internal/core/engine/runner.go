package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// Runner keeps a workflow live: it starts every trigger node and turns each emit into
// an execution.
type Runner struct {
	types    Catalog
	executor *Executor
	log      logging.Logger
}

// NewRunner starts triggers as types has them for each workflow's owner; a *Registry
// gives every owner its own.
func NewRunner(util *observability.Utility, types Catalog, executor *Executor) *Runner {
	return &Runner{types: types, executor: executor, log: util.Logger.With("component", "runner")}
}

// Run starts the triggers of w and blocks until ctx is done or every trigger has stopped.
// It does not look at w.Active; deciding what to run is the caller's job. Executions from
// one trigger never overlap: emit returns once its execution finishes.
//
// Saving checked the rest of the workflow. Should it no longer hold, say a custom node it
// uses lost a port, each execution fails saying so, where its owner sees it.
func (r *Runner) Run(ctx context.Context, w domain.Workflow, resumed bool) error {
	reg, err := r.types.For(ctx, w.OwnerID)
	if err != nil {
		return fmt.Errorf("the node types of the workflow's owner: %w", err)
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	started := 0

	for _, spec := range w.Nodes {
		trigger, ok := reg.Trigger(spec.Type)
		if !ok {
			continue
		}

		started++

		tc := TriggerContext{
			WorkflowID: w.ID,
			OwnerID:    w.OwnerID,
			NodeID:     spec.ID,
			Params:     newLiteralResolver(trigger.Type(), spec.Params).params,
			Resumed:    resumed,
		}

		wg.Go(func() {
			r.log.Info(ctx, "Trigger started", "workflow_id", w.ID, "node_id", spec.ID, "type", spec.Type)

			if err := trigger.Start(ctx, tc, r.emit(w, spec.ID)); err != nil {
				r.log.Error(ctx, "Trigger stopped", "workflow_id", w.ID, "node_id", spec.ID, logging.Err(err))

				mu.Lock()
				errs = append(errs, fmt.Errorf("trigger %q: %w", spec.ID, err))
				mu.Unlock()
			}
		})
	}

	if started == 0 {
		return fmt.Errorf("%w: no trigger node", domain.ErrInvalidWorkflow)
	}

	wg.Wait()

	return errors.Join(errs...)
}

// emit runs one execution. Its error only covers executions that could not start;
// failures while running are logged by the executor and kept in the execution record.
func (r *Runner) emit(w domain.Workflow, trigger domain.NodeID) Emit {
	return func(ctx context.Context, out Ports) error {
		_, err := r.executor.Execute(ctx, w, trigger, out)
		if err != nil {
			r.log.Error(ctx, "Execution did not start", "workflow_id", w.ID, "node_id", trigger, logging.Err(err))
		}

		return err
	}
}
