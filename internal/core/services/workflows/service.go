// Package workflows stores users' workflows, keeps active ones running and runs them on
// demand.
package workflows

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

const maxName = 128

// Limits keep one user from exhausting a shared instance.
type Limits struct {
	Workflows       int
	ActiveWorkflows int
	NodesPerFlow    int
}

func DefaultLimits() Limits {
	return Limits{Workflows: 100, ActiveWorkflows: 20, NodesPerFlow: 200}
}

// Validator checks a workflow against the registered node types.
type Validator interface {
	Validate(ctx context.Context, w domain.Workflow) error
}

// Executor runs one execution.
type Executor interface {
	Execute(
		ctx context.Context,
		w domain.Workflow,
		trigger domain.NodeID,
		output engine.Ports,
		opts ...engine.ExecuteOption,
	) (engine.Result, error)
}

// Supervisor keeps active workflows running.
type Supervisor interface {
	Start(w domain.Workflow)
	// Resume starts a workflow that was running before, so its triggers may catch up.
	Resume(w domain.Workflow)
	Stop(id domain.WorkflowID)
}

// Waits cancels executions that wait.
type Waits interface {
	Cancel(ctx context.Context, owner domain.UserID, id domain.ExecutionID, reason string) (domain.Execution, error)
	CancelWorkflow(ctx context.Context, owner domain.UserID, workflowID domain.WorkflowID, reason string) error
}

type Service struct {
	repo       ports.WorkflowRepository
	executions ports.ExecutionRepository
	validator  Validator
	executor   Executor
	supervisor Supervisor
	waits      Waits             // optional: without it, nothing waits
	fields     FieldChecker      // optional: without it, field settings go unchecked
	exprs      ExpressionChecker // optional: without it, expressions go unchecked
	code       CodeAccessChecker // optional: without it, anyone may save code nodes
	memory     NodeMemory        // optional: without it, removed nodes' memory waits to expire
	orders     RestingOrders     // optional: without it, live resting orders outlive their workflow
	limits     func() Limits
	log        logging.Logger
	now        func() time.Time
}

type Option func(*Service)

func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func WithLimits(l Limits) Option {
	return func(s *Service) { s.limits = func() Limits { return l } }
}

// FieldChecker reports field settings that name a field the items there do not have, or
// one of the wrong kind.
type FieldChecker interface {
	CheckFields(ctx context.Context, w domain.Workflow) error
}

// WithFieldChecks checks field settings when a workflow is saved. Only saving: a workflow
// saved before keeps running, and the next edit points at the field.
func WithFieldChecks(c FieldChecker) Option {
	return func(s *Service) { s.fields = c }
}

// ExpressionChecker reports expressions that do not compile, and {{ }} in settings that
// take none.
type ExpressionChecker interface {
	CheckExpressions(ctx context.Context, w domain.Workflow) error
}

// WithExpressionChecks checks expressions when a workflow is saved, like field settings.
func WithExpressionChecks(c ExpressionChecker) Option {
	return func(s *Service) { s.exprs = c }
}

// CodeAccessChecker reports the code nodes of a workflow whose owner may not write code,
// as a *domain.ValidationError. Other errors are its own failing.
type CodeAccessChecker interface {
	CheckCodeAccess(ctx context.Context, w domain.Workflow) error
}

// WithCodeAccess refuses to save code nodes for a user who may not write code (D44).
func WithCodeAccess(c CodeAccessChecker) Option {
	return func(s *Service) { s.code = c }
}

// NodeMemory is what the service needs from node memory: a node removed from its workflow
// takes what it remembered along, so a new node of the same ID starts fresh.
type NodeMemory interface {
	ForgetOthers(ctx context.Context, wf domain.WorkflowID, keep []domain.NodeID) error
}

func WithNodeMemory(m NodeMemory) Option {
	return func(s *Service) { s.memory = m }
}

// WithWaits cancels waiting executions when a workflow is switched off, and lets users
// cancel them.
// RestingOrders cancels a workflow's live orders resting on the book (D213, D217): the
// trading service's.
type RestingOrders interface {
	// CancelResting says how many still rest: those Polymarket would not or could not cancel.
	CancelResting(ctx context.Context, w domain.WorkflowID, why string) (int, error)
}

// WithRestingOrders cancels a workflow's live resting orders when it is deleted, and when
// it is switched off and its owner asks.
func WithRestingOrders(o RestingOrders) Option {
	return func(s *Service) { s.orders = o }
}

func WithWaits(w Waits) Option {
	return func(s *Service) { s.waits = w }
}

// WithLiveLimits reads the limits through limits on every check, so changes apply at once.
func WithLiveLimits(limits func() Limits) Option {
	return func(s *Service) { s.limits = limits }
}

func New(
	repo ports.WorkflowRepository,
	executions ports.ExecutionRepository,
	validator Validator,
	executor Executor,
	supervisor Supervisor,
	log logging.Logger,
	opts ...Option,
) *Service {
	s := &Service{
		repo:       repo,
		executions: executions,
		validator:  validator,
		executor:   executor,
		supervisor: supervisor,
		limits:     DefaultLimits,
		log:        log.With("component", "workflows"),
		now:        func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) },
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Definition is the editable part of a workflow.
type Definition struct {
	Name        string
	Nodes       []domain.NodeSpec
	Connections []domain.Connection
}

// Create stores a new, inactive workflow.
func (s *Service) Create(ctx context.Context, p auth.Principal, def Definition) (domain.Workflow, error) {
	total, _, err := s.repo.Count(ctx, p.User.ID)
	if err != nil {
		return domain.Workflow{}, err
	}

	if most := s.limits().Workflows; total >= most {
		return domain.Workflow{}, fmt.Errorf("%w: at most %d workflows per user", domain.ErrInvalidInput, most)
	}

	now := s.now()
	w := domain.Workflow{
		ID:        domain.WorkflowID(uuid.Must(uuid.NewV7()).String()),
		OwnerID:   p.User.ID,
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.apply(ctx, &w, def); err != nil {
		return domain.Workflow{}, err
	}

	if err := s.repo.Create(ctx, w); err != nil {
		return domain.Workflow{}, err
	}

	s.log.Info(ctx, "Workflow created", "user_id", p.User.ID, "workflow_id", w.ID)

	return w, nil
}

func (s *Service) Get(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error) {
	return s.repo.Get(ctx, p.User.ID, id)
}

func (s *Service) List(ctx context.Context, p auth.Principal, f ports.PageFilter) (ports.Page[domain.Workflow], error) {
	return s.repo.List(ctx, p.User.ID, f)
}

// Update replaces the definition if version is still current. An active workflow is
// restarted with the new definition.
func (s *Service) Update(ctx context.Context, p auth.Principal, id domain.WorkflowID, version int, def Definition) (domain.Workflow, error) {
	w, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return domain.Workflow{}, err
	}

	if w.Version != version {
		return domain.Workflow{}, domain.ErrConflict
	}

	if err := s.apply(ctx, &w, def); err != nil {
		return domain.Workflow{}, err
	}

	w.Version = version + 1
	w.UpdatedAt = s.now()

	if err := s.repo.Update(ctx, w, version); err != nil {
		return domain.Workflow{}, err
	}

	if w.Active {
		s.supervisor.Resume(w) // it was running: the new version picks up where it stopped
	}

	if s.memory != nil {
		keep := make([]domain.NodeID, len(w.Nodes))
		for i, n := range w.Nodes {
			keep[i] = n.ID
		}

		// Saved already: what removed nodes remember otherwise expires on its own.
		if err := s.memory.ForgetOthers(ctx, w.ID, keep); err != nil {
			s.log.Warn(ctx, "Removed nodes' memory not forgotten", "workflow_id", w.ID, logging.Err(err))
		}
	}

	s.log.Info(ctx, "Workflow updated", "user_id", p.User.ID, "workflow_id", w.ID, "version", w.Version)

	return w, nil
}

func (s *Service) Delete(ctx context.Context, p auth.Principal, id domain.WorkflowID) error {
	if _, err := s.repo.Get(ctx, p.User.ID, id); err != nil {
		return err
	}

	s.supervisor.Stop(id)

	// Deleted, it leaves no live orders behind (D213); its live books stay (D199).
	if s.orders != nil {
		if left, err := s.orders.CancelResting(ctx, id, "cancelled: the workflow was deleted"); err != nil || left > 0 {
			s.log.Warn(ctx, "Live resting orders of a deleted workflow not all cancelled", "workflow_id", id, "left", left)
		}
	}

	if err := s.repo.Delete(ctx, p.User.ID, id); err != nil {
		return err
	}

	s.log.Info(ctx, "Workflow deleted", "user_id", p.User.ID, "workflow_id", id)

	return nil
}

// Activate starts the workflow's triggers and keeps them running across restarts.
func (s *Service) Activate(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error) {
	w, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return domain.Workflow{}, err
	}

	if !w.Active {
		_, active, err := s.repo.Count(ctx, p.User.ID)
		if err != nil {
			return domain.Workflow{}, err
		}

		if most := s.limits().ActiveWorkflows; active >= most {
			return domain.Workflow{}, fmt.Errorf("%w: at most %d active workflows per user", domain.ErrInvalidInput, most)
		}
	}

	// Node types may have changed since the workflow was saved.
	if err := s.validator.Validate(ctx, w); err != nil {
		return domain.Workflow{}, err
	}

	if err := s.repo.SetActive(ctx, p.User.ID, id, true, s.now()); err != nil {
		return domain.Workflow{}, err
	}

	w.Active = true
	s.supervisor.Start(w)

	return w, nil
}

func (s *Service) Deactivate(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error) {
	w, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return domain.Workflow{}, err
	}

	if err := s.repo.SetActive(ctx, p.User.ID, id, false, s.now()); err != nil {
		return domain.Workflow{}, err
	}

	s.supervisor.Stop(id)
	w.Active = false

	// Switched off is off: executions that wait would carry on otherwise.
	if s.waits != nil {
		if err := s.waits.CancelWorkflow(ctx, p.User.ID, id, "cancelled: the workflow was switched off"); err != nil {
			s.log.Warn(ctx, "Waiting executions not cancelled", "workflow_id", id, "error", err.Error())
		}
	}

	return w, nil
}

// SwitchOff deactivates the workflow and, its owner asking, cancels its live resting
// orders (D217): without asking, they keep resting. How many still rest, Polymarket not
// cancelling them, is logged; the workflow is off whatever.
func (s *Service) SwitchOff(ctx context.Context, p auth.Principal, id domain.WorkflowID, cancelOrders bool) (domain.Workflow, error) {
	w, err := s.Deactivate(ctx, p, id)
	if err != nil || !cancelOrders || s.orders == nil {
		return w, err
	}

	if left, err := s.orders.CancelResting(ctx, id, "cancelled: the workflow was switched off"); err != nil || left > 0 {
		s.log.Warn(ctx, "Live resting orders of a workflow switched off not all cancelled", "workflow_id", id, "left", left)
	}

	return w, nil
}

// StopAll deactivates every workflow of owner and stops the running ones. It is for
// disabling a user: their workflows stay off when they are enabled again.
func (s *Service) StopAll(ctx context.Context, owner domain.UserID) error {
	ids, err := s.repo.DeactivateAll(ctx, owner, s.now())
	if err != nil {
		return err
	}

	for _, id := range ids {
		s.supervisor.Stop(id)

		if s.waits != nil {
			if err := s.waits.CancelWorkflow(ctx, owner, id, "cancelled: the account was disabled"); err != nil {
				s.log.Warn(ctx, "Waiting executions not cancelled", "workflow_id", id, "error", err.Error())
			}
		}
	}

	if len(ids) > 0 {
		s.log.Info(ctx, "Workflows of a disabled user stopped", "owner_id", owner, "count", len(ids))
	}

	return nil
}

// Run executes the workflow once, by hand, from the trigger named or else its only
// manual trigger, or its only schedule, fired as if its time had come. Market activity
// cannot be imitated, so those workflows run only when active. Run waits for the
// execution to finish, which keeps samples of each node's output for inspection.
func (s *Service) Run(ctx context.Context, p auth.Principal, id domain.WorkflowID, trigger domain.NodeID) (domain.Execution, error) {
	w, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return domain.Execution{}, err
	}

	if trigger == "" {
		trigger, err = runnableTrigger(w)
		if err != nil {
			return domain.Execution{}, err
		}
	}

	spec, _ := w.Node(trigger)

	var output engine.Ports

	switch spec.Type {
	case triggers.ManualType:
		output = triggers.ManualOutput()
	case triggers.ScheduleType:
		output = triggers.ScheduleOutput(s.now())
	default:
		return domain.Execution{}, fmt.Errorf("%w: %q starts on market activity, which a run by hand cannot imitate; switch the workflow on instead", domain.ErrInvalidInput, trigger)
	}

	// A run that no longer holds is recorded as failed; by hand, say what to fix instead.
	if err := s.validator.Validate(ctx, w); err != nil {
		return domain.Execution{}, err
	}

	res, err := s.executor.Execute(ctx, w, trigger, output, engine.KeepSamples())
	if err != nil {
		return domain.Execution{}, err
	}

	return res.Execution, nil
}

// runnableTrigger picks the trigger a run by hand starts from: the only manual trigger,
// or else the only schedule.
func runnableTrigger(w domain.Workflow) (domain.NodeID, error) {
	for _, typ := range []string{triggers.ManualType, triggers.ScheduleType} {
		var found []domain.NodeID

		for _, n := range w.Nodes {
			if n.Type == typ {
				found = append(found, n.ID)
			}
		}

		switch len(found) {
		case 0:
			continue
		case 1:
			return found[0], nil
		default:
			return "", fmt.Errorf("%w: the workflow has several %s nodes, name one", domain.ErrInvalidInput, typ)
		}
	}

	return "", fmt.Errorf("%w: the workflow has no manual or schedule trigger to run it by hand; switch it on instead", domain.ErrInvalidInput)
}

func (s *Service) Executions(ctx context.Context, p auth.Principal, id domain.WorkflowID, f ports.PageFilter) (ports.Page[domain.Execution], error) {
	if _, err := s.repo.Get(ctx, p.User.ID, id); err != nil {
		return ports.Page[domain.Execution]{}, err
	}

	return s.executions.List(ctx, id, f)
}

func (s *Service) Execution(ctx context.Context, p auth.Principal, id domain.WorkflowID, execID domain.ExecutionID) (domain.Execution, error) {
	if _, err := s.repo.Get(ctx, p.User.ID, id); err != nil {
		return domain.Execution{}, err
	}

	return s.executions.Get(ctx, id, execID)
}

// CancelExecution cancels one of the workflow's executions that waits.
func (s *Service) CancelExecution(ctx context.Context, p auth.Principal, id domain.WorkflowID, execID domain.ExecutionID) (domain.Execution, error) {
	exec, err := s.Execution(ctx, p, id, execID)
	if err != nil {
		return domain.Execution{}, err
	}

	if exec.Status != domain.ExecutionWaiting || s.waits == nil {
		return domain.Execution{}, fmt.Errorf("%w: it is %s", domain.ErrNotWaiting, exec.Status)
	}

	cancelled, err := s.waits.Cancel(ctx, p.User.ID, execID, "cancelled by you")
	if errors.Is(err, domain.ErrNotFound) {
		// It carried on, or timed out, in the meantime.
		return domain.Execution{}, fmt.Errorf("%w: it carried on meanwhile", domain.ErrNotWaiting)
	}

	return cancelled, err
}

// StartActive starts every active workflow; call it once on boot. One that no longer
// validates starts too, so that its runs fail saying why, where its owner sees it (D50).
func (s *Service) StartActive(ctx context.Context) error {
	active, err := s.repo.ListActive(ctx)
	if err != nil {
		return err
	}

	for _, w := range active {
		s.supervisor.Resume(w)
	}

	s.log.Info(ctx, "Active workflows started", "count", len(active))

	return nil
}

// PruneExecutions deletes executions that finished before the retention window.
func (s *Service) PruneExecutions(ctx context.Context, retention time.Duration) (int64, error) {
	return s.executions.DeleteFinishedBefore(ctx, s.now().Add(-retention))
}

// Check says what saving the workflow as it is would refuse, without saving: what no
// longer holds since its node types changed, say (D60). None is an empty list.
func (s *Service) Check(ctx context.Context, p auth.Principal, id domain.WorkflowID) ([]domain.Problem, error) {
	w, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return nil, err
	}

	err = s.check(ctx, w)

	var v *domain.ValidationError
	if errors.As(err, &v) {
		return v.Problems, nil
	}

	return []domain.Problem{}, err
}

// check runs the checks saving does. Their problems come back together as a
// *domain.ValidationError, so one save says everything; a check failing to run is an
// error of its own.
func (s *Service) check(ctx context.Context, w domain.Workflow) error {
	if err := s.validator.Validate(ctx, w); err != nil {
		return err
	}

	var problems domain.ValidationError

	checks := []func() error{}

	if s.fields != nil {
		checks = append(checks, func() error { return s.fields.CheckFields(ctx, w) })
	}

	if s.exprs != nil {
		checks = append(checks, func() error { return s.exprs.CheckExpressions(ctx, w) })
	}

	if s.code != nil {
		checks = append(checks, func() error { return s.code.CheckCodeAccess(ctx, w) })
	}

	for _, check := range checks {
		err := check()

		var found *domain.ValidationError
		if err != nil && !errors.As(err, &found) {
			return err
		}

		problems.Merge(err)
	}

	return problems.Err()
}

// apply validates def and copies it onto w.
func (s *Service) apply(ctx context.Context, w *domain.Workflow, def Definition) error {
	name := strings.TrimSpace(def.Name)
	if name == "" || len(name) > maxName {
		return fmt.Errorf("%w: name must be 1 to %d characters", domain.ErrInvalidInput, maxName)
	}

	if most := s.limits().NodesPerFlow; len(def.Nodes) > most {
		return fmt.Errorf("%w: at most %d nodes per workflow", domain.ErrInvalidInput, most)
	}

	candidate := *w
	candidate.Name = name
	candidate.Nodes = def.Nodes
	candidate.Connections = def.Connections

	if err := s.check(ctx, candidate); err != nil {
		return err
	}

	*w = candidate

	return nil
}
