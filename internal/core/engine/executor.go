package engine

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

const tracerName = "github.com/lein3000zzz/polythought-go/internal/core/engine"

const (
	opExecute = "execute_workflow"
	opRunNode = "run_node"
)

var (
	ErrNotTrigger = errors.New("node is not a trigger")

	// ErrItemsFailed fails an execution when a node without continueOnFail emits on ErrorPort.
	ErrItemsFailed = errors.New("items failed")

	ErrNodePanicked = errors.New("node panicked")
)

// Result is a finished execution and every node's output, for inspection and tests.
type Result struct {
	Execution domain.Execution
	Outputs   map[domain.NodeID]Ports
}

// Executor runs one workflow execution at a time per call; calls may run concurrently.
type Executor struct {
	registry  *Registry
	execKit   *redkit.Kit
	nodeKit   *redkit.Kit
	scope     tracing.Scope
	log       logging.Logger
	now       func() time.Time
	newExecID func() domain.ExecutionID
}

type ExecutorOption func(*Executor)

// WithClock replaces time.Now, for tests.
func WithClock(now func() time.Time) ExecutorOption {
	return func(e *Executor) { e.now = now }
}

// WithExecutionIDs replaces the random execution ID generator, for tests.
func WithExecutionIDs(next func() domain.ExecutionID) ExecutorOption {
	return func(e *Executor) { e.newExecID = next }
}

func NewExecutor(util *observability.Utility, registry *Registry, opts ...ExecutorOption) (*Executor, error) {
	log := util.Logger.With("component", "engine")

	execKit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    log,
		Subsystem: "engine_executions",
		Buckets:   redkit.LongBuckets,
		Component: "workflow execution",
	})
	if err != nil {
		return nil, fmt.Errorf("engine: register execution metrics: %w", err)
	}

	nodeKit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    log,
		Subsystem: "engine_nodes",
		Buckets:   redkit.MediumLongBuckets,
		Component: "node run",
	})
	if err != nil {
		return nil, fmt.Errorf("engine: register node metrics: %w", err)
	}

	e := &Executor{
		registry:  registry,
		execKit:   execKit,
		nodeKit:   nodeKit,
		scope:     tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		log:       log,
		now:       time.Now,
		newExecID: func() domain.ExecutionID { return domain.ExecutionID(rand.Text()) },
	}

	for _, opt := range opts {
		opt(e)
	}

	return e, nil
}

// Execute runs w from the trigger node that fired with output. Nodes run in topological
// order; a node runs only if items reached it, others are recorded as skipped.
//
// The returned error is for executions that could not start (invalid workflow, unknown
// trigger). Failures while running are reported in Result.Execution, not as an error.
func (e *Executor) Execute(ctx context.Context, w domain.Workflow, trigger domain.NodeID, output Ports) (Result, error) {
	if err := e.registry.Validate(w); err != nil {
		return Result{}, err
	}

	spec, _ := w.Node(trigger)
	if _, isTrigger := e.registry.Trigger(spec.Type); !isTrigger {
		return Result{}, fmt.Errorf("%w: %q (%s)", ErrNotTrigger, trigger, spec.Type)
	}

	order, err := w.TopologicalOrder()
	if err != nil {
		return Result{}, err
	}

	exec := domain.NewExecution(e.newExecID(), w.ID, trigger, e.now())

	done := e.execKit.Start(ctx, opExecute)

	result, _ := tracing.InScope(ctx, e.scope, opExecute, func(ctx context.Context, span trace.Span) (Result, error) {
		span.SetAttributes(
			attribute.String("workflow.id", string(w.ID)),
			attribute.String("workflow.execution_id", string(exec.ID)),
			attribute.String("workflow.trigger", string(trigger)),
		)

		run := &execution{executor: e, workflow: w, exec: &exec, outputs: make(map[domain.NodeID]Ports, len(w.Nodes))}
		cause := run.walk(ctx, order, trigger, output)

		status := statusOf(ctx, cause)
		_ = exec.Finish(status, e.now(), cause) // exec is ours and still running, so this cannot fail

		span.SetAttributes(attribute.String("workflow.status", string(status)))
		e.logFinished(ctx, exec, cause)

		return Result{Execution: exec, Outputs: run.outputs}, cause
	})

	done(exec.Status == domain.ExecutionSucceeded)

	return result, nil
}

func statusOf(ctx context.Context, cause error) domain.ExecutionStatus {
	switch {
	case cause == nil:
		return domain.ExecutionSucceeded
	case ctx.Err() != nil:
		return domain.ExecutionCancelled
	default:
		return domain.ExecutionFailed
	}
}

func (e *Executor) logFinished(ctx context.Context, exec domain.Execution, cause error) {
	attrs := []any{
		"workflow_id", exec.WorkflowID,
		"execution_id", exec.ID,
		"status", exec.Status,
		"duration", exec.Duration(),
		"nodes", len(exec.Runs),
	}

	switch exec.Status {
	case domain.ExecutionSucceeded:
		e.log.Info(ctx, "Execution finished", attrs...)
	case domain.ExecutionCancelled:
		e.log.Info(ctx, "Execution cancelled", attrs...)
	default:
		e.log.Error(ctx, "Execution failed", append(attrs, logging.Err(cause))...)
	}
}

// execution is the state of one Execute call.
type execution struct {
	executor *Executor
	workflow domain.Workflow
	exec     *domain.Execution
	inputs   map[domain.NodeID]Ports
	outputs  map[domain.NodeID]Ports
}

// walk runs nodes in order and returns the error that stopped the execution, if any.
func (x *execution) walk(ctx context.Context, order []domain.NodeID, trigger domain.NodeID, output Ports) error {
	x.inputs = make(map[domain.NodeID]Ports, len(order))

	now := x.executor.now()
	x.record(domain.NodeRun{
		NodeID: trigger, Status: domain.NodeRunSucceeded,
		StartedAt: now, FinishedAt: now, Output: output.Counts(),
	})
	x.route(trigger, output)

	for _, id := range order {
		if id == trigger {
			continue
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		in := x.inputs[id]
		if in.Len() == 0 {
			now := x.executor.now()
			x.record(domain.NodeRun{NodeID: id, Status: domain.NodeRunSkipped, StartedAt: now, FinishedAt: now})

			continue
		}

		if err := x.runNode(ctx, id, in); err != nil {
			return err
		}
	}

	return nil
}

func (x *execution) runNode(ctx context.Context, id domain.NodeID, in Ports) (err error) {
	spec, _ := x.workflow.Node(id)
	node, _ := x.executor.registry.Node(spec.Type) // Validate guarantees non-trigger types are nodes
	typ := node.Type()

	run := domain.NodeRun{NodeID: id, StartedAt: x.executor.now(), Input: in.Counts()}

	done := x.executor.nodeKit.Start(ctx, typ.Name)
	defer func() { done(err == nil) }()

	out, err := tracing.InScope(ctx, x.executor.scope, opRunNode, func(ctx context.Context, span trace.Span) (Ports, error) {
		span.SetAttributes(
			attribute.String("node.id", string(id)),
			attribute.String("node.type", typ.Name),
			attribute.Int("node.items_in", in.Len()),
		)

		out, err := x.safeRun(ctx, node, RunContext{
			ExecutionID: x.exec.ID,
			WorkflowID:  x.workflow.ID,
			NodeID:      id,
			Params:      newLiteralResolver(typ, spec.Params),
		}, in)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", id, err)
		}

		failed := out[ErrorPort]
		span.SetAttributes(attribute.Int("node.items_out", out.Len()-len(failed)), attribute.Int("node.items_failed", len(failed)))

		if len(failed) > 0 && !spec.ContinueOnFail {
			return out, fmt.Errorf("node %q: %d %w: %w", id, len(failed), ErrItemsFailed, failed[0].Err)
		}

		return out, nil
	})

	run.FinishedAt = x.executor.now()
	run.Output = out.Counts()
	run.Status = domain.NodeRunSucceeded

	if err != nil {
		run.Status = domain.NodeRunFailed
		run.Error = err.Error()
	}

	x.record(run)
	x.outputs[id] = out

	x.executor.log.Debug(ctx, "Node finished",
		"node_id", id,
		"node_type", typ.Name,
		"status", run.Status,
		"items_in", in.Len(),
		"items_out", out.Len(),
	)

	if err != nil {
		return err
	}

	x.route(id, out)

	return nil
}

// safeRun turns a panicking node into a failed node, so one bad node cannot take down
// every workflow running in the process.
func (x *execution) safeRun(ctx context.Context, node Node, rc RunContext, in Ports) (out Ports, err error) {
	defer func() {
		if r := recover(); r != nil {
			x.executor.log.Error(ctx, "Node panicked", "node_id", rc.NodeID, "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("%w: %v", ErrNodePanicked, r)
		}
	}()

	return node.Run(ctx, rc, in)
}

func (x *execution) record(run domain.NodeRun) {
	_ = x.exec.Record(run) // the execution only finishes after walk returns
}

// route appends a node's output to the inputs of every node connected to it.
func (x *execution) route(from domain.NodeID, out Ports) {
	x.outputs[from] = out

	for _, c := range x.workflow.Outgoing(from) {
		items := out[c.FromPort]
		if len(items) == 0 {
			continue
		}

		if x.inputs[c.To] == nil {
			x.inputs[c.To] = make(Ports)
		}

		x.inputs[c.To].Add(c.ToPort, items...)
	}
}
