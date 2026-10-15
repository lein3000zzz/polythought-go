package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
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
	recorder  ports.ExecutionRecorder
	notifiers []ports.ExecutionNotifier

	waits      ports.WaitRepository
	waitLimits func() WaitLimits

	evaluator Evaluator // works expressions out; nil: only plain fields
}

type ExecutorOption func(*Executor)

// WithClock replaces time.Now, for tests.
func WithClock(now func() time.Time) ExecutorOption {
	return func(e *Executor) { e.now = now }
}

// WithRecorder stores every finished execution.
func WithRecorder(r ports.ExecutionRecorder) ExecutorOption {
	return func(e *Executor) { e.recorder = r }
}

// WithNotifier tells n about every finished execution, after it is recorded. Each use
// adds a notifier; they are told in order.
func WithNotifier(n ports.ExecutionNotifier) ExecutorOption {
	return func(e *Executor) { e.notifiers = append(e.notifiers, n) }
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
		newExecID: func() domain.ExecutionID { return domain.ExecutionID(uuid.Must(uuid.NewV7()).String()) },
	}

	for _, opt := range opts {
		opt(e)
	}

	return e, nil
}

// Execute runs w from the trigger node that fired with output. Nodes run in topological
// order; a node runs only if items reached it, others are recorded as skipped. A node
// that waits pauses the execution: Execute returns it waiting, and Continue carries on.
//
// The returned error is for executions that could not start (invalid workflow, unknown
// trigger). Failures while running are reported in Result.Execution, not as an error.
func (e *Executor) Execute(
	ctx context.Context,
	w domain.Workflow,
	trigger domain.NodeID,
	output Ports,
	opts ...ExecuteOption,
) (Result, error) {
	var o executeOptions
	for _, opt := range opts {
		opt(&o)
	}

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
	exec.WorkflowVersion = w.Version

	x := e.newExecution(w, &exec, o.samples)
	rest := slices.DeleteFunc(slices.Clone(order), func(id domain.NodeID) bool { return id == trigger })

	return e.drive(ctx, x, func(ctx context.Context) error {
		x.start(trigger, output)
		return x.runFrom(ctx, rest)
	}), nil
}

// Continue carries on a waiting execution from its wait record, now that r ended the
// wait. It runs the workflow as it was when the execution started. The error is for a
// record that cannot be read at all; such an execution is lost.
func (e *Executor) Continue(ctx context.Context, rec ports.WaitRecord, r Resumption) (Result, error) {
	snap, err := decodeSnapshot(rec.State)
	if err != nil {
		return Result{}, err
	}

	if err := snap.Execution.Resume(); err != nil {
		return Result{}, err
	}

	x := e.newExecution(snap.Workflow, &snap.Execution, snap.Samples)
	for id, in := range snap.Pending {
		x.inputs[id] = in.ports()
	}

	r.State = snap.State

	return e.drive(ctx, x, func(ctx context.Context) error {
		// The node types may have changed since it paused.
		if err := e.registry.Validate(snap.Workflow); err != nil {
			return err
		}

		if err := x.resumeNode(ctx, snap.Node, snap.Input.ports(), r); err != nil {
			var again *pause
			if errors.As(err, &again) {
				again.remaining = snap.Remaining
			}

			return err
		}

		return x.runFrom(ctx, snap.Remaining)
	}), nil
}

// Cancel ends a waiting execution, from its wait record, as cancelled for reason.
func (e *Executor) Cancel(ctx context.Context, rec ports.WaitRecord, reason string) (domain.Execution, error) {
	snap, err := decodeSnapshot(rec.State)
	if err != nil {
		return domain.Execution{}, err
	}

	exec := snap.Execution
	now := e.now()

	for i := len(exec.Runs) - 1; i >= 0; i-- {
		if exec.Runs[i].NodeID == snap.Node && exec.Runs[i].Status == domain.NodeRunWaiting {
			exec.Runs[i].Status, exec.Runs[i].FinishedAt = domain.NodeRunCancelled, now
			break
		}
	}

	cause := errors.New(reason)
	if err := exec.Finish(domain.ExecutionCancelled, now, cause); err != nil {
		return domain.Execution{}, err
	}

	e.logFinished(ctx, exec, cause)
	e.record(ctx, exec)
	e.notify(snap.Workflow.OwnerID, exec)

	return exec, nil
}

func (e *Executor) newExecution(w domain.Workflow, exec *domain.Execution, samples bool) *execution {
	return &execution{
		executor: e, workflow: w, exec: exec, samples: samples,
		inputs:  make(map[domain.NodeID]Ports, len(w.Nodes)),
		outputs: make(map[domain.NodeID]Ports, len(w.Nodes)),
	}
}

// drive runs body as the execution's work, then finishes the execution, or parks it when a
// node waits, and tells the listeners.
func (e *Executor) drive(ctx context.Context, x *execution, body func(context.Context) error) Result {
	done := e.execKit.Start(ctx, opExecute)

	result, _ := tracing.InScope(ctx, e.scope, opExecute, func(ctx context.Context, span trace.Span) (Result, error) {
		span.SetAttributes(
			attribute.String("workflow.id", string(x.workflow.ID)),
			attribute.String("workflow.execution_id", string(x.exec.ID)),
			attribute.String("workflow.trigger", string(x.exec.TriggeredBy)),
		)

		cause := e.settle(ctx, x, body(ctx))
		span.SetAttributes(attribute.String("workflow.status", string(x.exec.Status)))

		return Result{Execution: *x.exec, Outputs: x.outputs}, cause
	})

	done(x.exec.Status == domain.ExecutionSucceeded || x.exec.Status == domain.ExecutionWaiting)
	e.notify(x.workflow.OwnerID, *x.exec)

	return result
}

// settle parks the execution when a node waits and may, and finishes it otherwise. It
// returns what the execution ended with: nil when it succeeded or waits.
func (e *Executor) settle(ctx context.Context, x *execution, cause error) error {
	var p *pause
	if errors.As(cause, &p) {
		if cause = e.park(ctx, x, p); cause == nil {
			e.log.Info(ctx, "Execution waiting",
				"workflow_id", x.workflow.ID, "execution_id", x.exec.ID, "node_id", p.node, "until", p.wait.Until)

			return nil
		}

		x.failWait(p.node, cause)
	}

	status := statusOf(ctx, cause)
	_ = x.exec.Finish(status, e.now(), cause) // running or waiting, so this cannot fail

	e.logFinished(ctx, *x.exec, cause)
	e.record(ctx, *x.exec)

	return cause
}

// park keeps a paused execution in waits, within the limits. It saves the execution
// first: the wait refers to it.
func (e *Executor) park(ctx context.Context, x *execution, p *pause) error {
	if e.waits == nil {
		return fmt.Errorf("node %q: %w: waiting is not available", p.node, ErrCannotWait)
	}

	// A wait outlives the run that made it, including one cut short by a shutdown.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	var limits WaitLimits
	if e.waitLimits != nil {
		limits = e.waitLimits()
	}

	if limits.MaxWait > 0 && p.wait.Until.Sub(e.now()) > limits.MaxWait {
		return fmt.Errorf("node %q: %w: waits end within %s", p.node, ErrWaitRefused, limits.MaxWait)
	}

	if limits.MaxWaiting > 0 {
		n, err := e.waits.CountForOwner(ctx, x.workflow.OwnerID)
		if err != nil {
			return err
		}

		if n >= limits.MaxWaiting {
			return fmt.Errorf("node %q: %w: %d of your executions are waiting already, the most there may be",
				p.node, ErrWaitRefused, n)
		}
	}

	if err := x.exec.Wait(p.node, p.wait.Until); err != nil {
		return err
	}

	state, err := encodeSnapshot(x, p)
	if err != nil {
		return fmt.Errorf("node %q: %w", p.node, err)
	}

	if e.recorder != nil {
		if err := e.recorder.Save(ctx, *x.exec); err != nil {
			return err
		}
	}

	return e.waits.Add(ctx, ports.WaitRecord{
		ExecutionID: x.exec.ID, WorkflowID: x.workflow.ID, OwnerID: x.workflow.OwnerID,
		NodeID: p.node, Until: p.wait.Until, Key: p.wait.Key, State: state, CreatedAt: e.now(),
	})
}

func (e *Executor) notify(owner domain.UserID, exec domain.Execution) {
	for _, n := range e.notifiers {
		n.ExecutionFinished(owner, exec)
	}
}

// record saves the execution even when ctx was cancelled: a cancelled run is worth
// keeping too. A failed save is logged; the execution itself already happened.
func (e *Executor) record(ctx context.Context, exec domain.Execution) {
	if e.recorder == nil {
		return
	}

	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	if err := e.recorder.Save(saveCtx, exec); err != nil {
		e.log.Error(ctx, "Execution not recorded", "workflow_id", exec.WorkflowID, "execution_id", exec.ID, logging.Err(err))
	}
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
	samples  bool // keep the first items of each node's output
	inputs   map[domain.NodeID]Ports
	outputs  map[domain.NodeID]Ports
}

// start records the trigger's run and hands its output on.
func (x *execution) start(trigger domain.NodeID, output Ports) {
	now := x.executor.now()
	x.record(domain.NodeRun{
		NodeID: trigger, Status: domain.NodeRunSucceeded,
		StartedAt: now, FinishedAt: now, Output: output.Counts(), Samples: x.sample(output),
	})
	x.route(trigger, output)
}

// runFrom runs nodes in order and returns the error that stopped the execution, if any:
// a pause when a node waits.
func (x *execution) runFrom(ctx context.Context, order []domain.NodeID) error {
	for i, id := range order {
		if err := ctx.Err(); err != nil {
			return err
		}

		in := x.inputs[id]
		delete(x.inputs, id) // what is left is what a pause keeps

		if in.Len() == 0 {
			now := x.executor.now()
			x.record(domain.NodeRun{NodeID: id, Status: domain.NodeRunSkipped, StartedAt: now, FinishedAt: now})

			continue
		}

		if err := x.runNode(ctx, id, in); err != nil {
			var p *pause
			if errors.As(err, &p) {
				p.remaining = order[i+1:]
			}

			return err
		}
	}

	return nil
}

func (x *execution) runNode(ctx context.Context, id domain.NodeID, in Ports) error {
	return x.step(ctx, id, in, func(ctx context.Context, node Node, rc RunContext) (Ports, error) {
		return node.Run(ctx, rc, in)
	})
}

// resumeNode finishes the run of a node that waited.
func (x *execution) resumeNode(ctx context.Context, id domain.NodeID, in Ports, r Resumption) error {
	return x.step(ctx, id, in, func(ctx context.Context, node Node, rc RunContext) (Ports, error) {
		resumer, ok := node.(Resumer)
		if !ok {
			return nil, ErrCannotWait
		}

		return resumer.Resume(ctx, rc, in, r)
	})
}

// step runs one node through call and records how it went. A node that waits gives a
// pause instead of an error.
func (x *execution) step(
	ctx context.Context,
	id domain.NodeID,
	in Ports,
	call func(context.Context, Node, RunContext) (Ports, error),
) (err error) {
	spec, _ := x.workflow.Node(id)
	node, _ := x.executor.registry.Node(spec.Type) // Validate guarantees non-trigger types are nodes
	typ := node.Type()

	run := domain.NodeRun{NodeID: id, StartedAt: x.executor.now(), Input: in.Counts()}
	if prev, ok := x.waitingRun(id); ok {
		run.StartedAt = prev.StartedAt // a resumed run counts from when it began
	}

	var wait *Wait

	done := x.executor.nodeKit.Start(ctx, typ.Name)
	defer func() { done(err == nil || wait != nil) }()

	out, err := tracing.InScope(ctx, x.executor.scope, opRunNode, func(ctx context.Context, span trace.Span) (Ports, error) {
		span.SetAttributes(
			attribute.String("node.id", string(id)),
			attribute.String("node.type", typ.Name),
			attribute.Int("node.items_in", in.Len()),
		)

		rc := RunContext{
			ExecutionID: x.exec.ID,
			WorkflowID:  x.workflow.ID,
			OwnerID:     x.workflow.OwnerID,
			NodeID:      id,
			Params: x.executor.resolverFor(ctx, typ, spec.Params, in, map[string]any{
				"id": string(x.exec.ID), "workflowId": string(x.workflow.ID), "startedAt": x.exec.StartedAt,
			}),
		}

		out, err := x.safeCall(ctx, id, func(ctx context.Context) (Ports, error) { return call(ctx, node, rc) })

		if errors.As(err, &wait) {
			if _, ok := node.(Resumer); !ok {
				wait = nil
				return nil, fmt.Errorf("node %q: %w: it cannot wait", id, ErrCannotWait)
			}

			span.SetAttributes(attribute.String("node.waits_until", wait.Until.UTC().Format(time.RFC3339)))

			return nil, nil
		}

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

	if wait != nil {
		run.Status = domain.NodeRunWaiting
		x.keep(run)

		return &pause{node: id, in: in, wait: wait}
	}

	run.FinishedAt = x.executor.now()
	run.Output = out.Counts()
	run.Samples = x.sample(out)
	run.Status = domain.NodeRunSucceeded

	if err != nil {
		run.Status = domain.NodeRunFailed
		run.Error = err.Error()
	}

	x.keep(run)
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

func (x *execution) sample(out Ports) map[string][]domain.ItemSample {
	if !x.samples {
		return nil
	}

	return sample(out)
}

// safeCall turns a panicking node into a failed node, so one bad node cannot take down
// every workflow running in the process.
func (x *execution) safeCall(ctx context.Context, id domain.NodeID, call func(context.Context) (Ports, error)) (out Ports, err error) {
	defer func() {
		if r := recover(); r != nil {
			x.executor.log.Error(ctx, "Node panicked", "node_id", id, "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("%w: %v", ErrNodePanicked, r)
		}
	}()

	return call(ctx)
}

func (x *execution) record(run domain.NodeRun) {
	_ = x.exec.Record(run) // the execution only finishes after its nodes ran
}

// keep records a node's run, replacing the one it waited in, if any.
func (x *execution) keep(run domain.NodeRun) {
	if _, ok := x.waitingRun(run.NodeID); ok {
		_ = x.exec.UpdateRun(run)
		return
	}

	x.record(run)
}

// waitingRun is the node's run that waits, if it is the last one.
func (x *execution) waitingRun(id domain.NodeID) (domain.NodeRun, bool) {
	for i := len(x.exec.Runs) - 1; i >= 0; i-- {
		if x.exec.Runs[i].NodeID == id {
			return x.exec.Runs[i], x.exec.Runs[i].Status == domain.NodeRunWaiting
		}
	}

	return domain.NodeRun{}, false
}

// failWait marks the run that could not wait as failed.
func (x *execution) failWait(id domain.NodeID, cause error) {
	if run, ok := x.waitingRun(id); ok {
		run.Status, run.Error, run.FinishedAt = domain.NodeRunFailed, cause.Error(), x.executor.now()
		_ = x.exec.UpdateRun(run)
	}
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
