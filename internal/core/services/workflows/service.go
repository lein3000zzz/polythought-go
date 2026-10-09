// Package workflows stores users' workflows, keeps active ones running and runs them on
// demand.
package workflows

import (
	"context"
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
	Validate(w domain.Workflow) error
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
	Stop(id domain.WorkflowID)
}

type Service struct {
	repo       ports.WorkflowRepository
	executions ports.ExecutionRepository
	validator  Validator
	executor   Executor
	supervisor Supervisor
	limits     Limits
	log        logging.Logger
	now        func() time.Time
}

type Option func(*Service)

func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func WithLimits(l Limits) Option {
	return func(s *Service) { s.limits = l }
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
		limits:     DefaultLimits(),
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

	if total >= s.limits.Workflows {
		return domain.Workflow{}, fmt.Errorf("%w: at most %d workflows per user", domain.ErrInvalidInput, s.limits.Workflows)
	}

	now := s.now()
	w := domain.Workflow{
		ID:        domain.WorkflowID(uuid.Must(uuid.NewV7()).String()),
		OwnerID:   p.User.ID,
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.apply(&w, def); err != nil {
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

	if err := s.apply(&w, def); err != nil {
		return domain.Workflow{}, err
	}

	w.Version = version + 1
	w.UpdatedAt = s.now()

	if err := s.repo.Update(ctx, w, version); err != nil {
		return domain.Workflow{}, err
	}

	if w.Active {
		s.supervisor.Start(w)
	}

	s.log.Info(ctx, "Workflow updated", "user_id", p.User.ID, "workflow_id", w.ID, "version", w.Version)

	return w, nil
}

func (s *Service) Delete(ctx context.Context, p auth.Principal, id domain.WorkflowID) error {
	if _, err := s.repo.Get(ctx, p.User.ID, id); err != nil {
		return err
	}

	s.supervisor.Stop(id)

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

		if active >= s.limits.ActiveWorkflows {
			return domain.Workflow{}, fmt.Errorf("%w: at most %d active workflows per user", domain.ErrInvalidInput, s.limits.ActiveWorkflows)
		}
	}

	// Node types may have changed since the workflow was saved.
	if err := s.validator.Validate(w); err != nil {
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
	}

	if len(ids) > 0 {
		s.log.Info(ctx, "Workflows of a disabled user stopped", "owner_id", owner, "count", len(ids))
	}

	return nil
}

// Run executes the workflow once from a manual trigger: the one named, or the
// workflow's only trigger.manual node. It waits for the execution to finish, which keeps
// samples of each node's output for inspection.
func (s *Service) Run(ctx context.Context, p auth.Principal, id domain.WorkflowID, trigger domain.NodeID) (domain.Execution, error) {
	w, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return domain.Execution{}, err
	}

	if trigger == "" {
		trigger, err = manualTrigger(w)
		if err != nil {
			return domain.Execution{}, err
		}
	}

	res, err := s.executor.Execute(ctx, w, trigger, triggers.ManualOutput(), engine.KeepSamples())
	if err != nil {
		return domain.Execution{}, err
	}

	return res.Execution, nil
}

func manualTrigger(w domain.Workflow) (domain.NodeID, error) {
	var found []domain.NodeID

	for _, n := range w.Nodes {
		if n.Type == triggers.ManualType {
			found = append(found, n.ID)
		}
	}

	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", fmt.Errorf("%w: the workflow has no %s node", domain.ErrInvalidInput, triggers.ManualType)
	default:
		return "", fmt.Errorf("%w: the workflow has several %s nodes, name one", domain.ErrInvalidInput, triggers.ManualType)
	}
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

// StartActive starts every active workflow; call it once on boot. Workflows that no
// longer validate are logged and left stopped.
func (s *Service) StartActive(ctx context.Context) error {
	active, err := s.repo.ListActive(ctx)
	if err != nil {
		return err
	}

	for _, w := range active {
		if err := s.validator.Validate(w); err != nil {
			s.log.Error(ctx, "Active workflow is invalid, not started", "workflow_id", w.ID, "owner_id", w.OwnerID, logging.Err(err))
			continue
		}

		s.supervisor.Start(w)
	}

	s.log.Info(ctx, "Active workflows started", "count", len(active))

	return nil
}

// PruneExecutions deletes executions that finished before the retention window.
func (s *Service) PruneExecutions(ctx context.Context, retention time.Duration) (int64, error) {
	return s.executions.DeleteFinishedBefore(ctx, s.now().Add(-retention))
}

// apply validates def and copies it onto w.
func (s *Service) apply(w *domain.Workflow, def Definition) error {
	name := strings.TrimSpace(def.Name)
	if name == "" || len(name) > maxName {
		return fmt.Errorf("%w: name must be 1 to %d characters", domain.ErrInvalidInput, maxName)
	}

	if len(def.Nodes) > s.limits.NodesPerFlow {
		return fmt.Errorf("%w: at most %d nodes per workflow", domain.ErrInvalidInput, s.limits.NodesPerFlow)
	}

	candidate := *w
	candidate.Name = name
	candidate.Nodes = def.Nodes
	candidate.Connections = def.Connections

	if err := s.validator.Validate(candidate); err != nil {
		return err
	}

	*w = candidate

	return nil
}
