// Package workflows stores users' workflows, keeps active ones running and runs them on
// demand.
package workflows

import (
	"context"
	"errors"
	"fmt"
	"maps"
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

// Waits cancels and answers executions that wait.
type Waits interface {
	Cancel(ctx context.Context, owner domain.UserID, id domain.ExecutionID, reason string) (domain.Execution, error)
	CancelWorkflow(ctx context.Context, owner domain.UserID, workflowID domain.WorkflowID, reason string) error
	Waiting(ctx context.Context, owner domain.UserID, id domain.ExecutionID) (ports.WaitRecord, error)
	Answer(ctx context.Context, owner domain.UserID, id domain.ExecutionID, key string, payload map[string]any) error
}

type Service struct {
	repo       ports.WorkflowRepository
	executions ports.ExecutionRepository
	validator  Validator
	executor   Executor
	supervisor Supervisor
	waits      Waits               // optional: without it, nothing waits
	fields     FieldChecker        // optional: without it, field settings go unchecked
	exprs      ExpressionChecker   // optional: without it, expressions go unchecked
	code       CodeAccessChecker   // optional: without it, anyone may save code nodes
	plugins    PluginAccessChecker // optional: without it, anyone may save plugins' nodes
	pauser     Pauser              // optional: without it, owners are not told of pauses
	memory     NodeMemory          // optional: without it, removed nodes' memory waits to expire
	orders     RestingOrders       // optional: without it, live resting orders outlive their workflow
	tradeScope TradeScope          // optional: without it, a workflow's orders need no scope of their own
	personal   Personal            // optional: without it, nothing is hidden from tokens and agents
	creds      Credentials         // optional: without it, tokens and agents use credentials freely
	confirm    Confirmer           // optional: without it, tokens' and agents' live actions go ahead at once
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

// PluginAccessChecker reports the nodes of a workflow of plugins its owner may not use
// (D282), as a *domain.ValidationError. Other errors are its own failing.
type PluginAccessChecker interface {
	CheckPluginAccess(ctx context.Context, w domain.Workflow) error
}

// WithPluginAccess refuses to save nodes of plugins their owner may not use.
func WithPluginAccess(c PluginAccessChecker) Option {
	return func(s *Service) { s.plugins = c }
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

// Personal names a node type's personal settings, such as a webhook's URL (D401).
type Personal func(nodeType string) []string

// WithPersonal hides personal settings from tokens and agents whose user has not let them
// see those (D402), and keeps the values saved hidden as they were.
func WithPersonal(f Personal) Option {
	return func(s *Service) { s.personal = f }
}

// forCaller is the workflow as the caller may see it: a copy with its personal settings
// hidden, for a token or an agent whose user has not let them see those.
func (s *Service) forCaller(p auth.Principal, w domain.Workflow) domain.Workflow {
	if s.personal == nil || p.SeesPersonal() {
		return w
	}

	nodes := make([]domain.NodeSpec, len(w.Nodes))

	for i, n := range w.Nodes {
		copied := false

		for _, name := range s.personal(n.Type) {
			if _, set := n.Params[name]; !set {
				continue
			}

			if !copied {
				n.Params, copied = maps.Clone(n.Params), true
			}

			n.Params[name] = domain.HiddenPersonal()
		}

		nodes[i] = n
	}

	w.Nodes = nodes

	return w
}

// keepHidden puts back the values of personal settings saved hidden (D402): each as the
// node of the same ID had it before; one that had none has none.
func keepHidden(def Definition, before []domain.NodeSpec) Definition {
	had := make(map[domain.NodeID]map[string]any, len(before))
	for _, n := range before {
		had[n.ID] = n.Params
	}

	nodes := make([]domain.NodeSpec, len(def.Nodes))

	for i, n := range def.Nodes {
		copied := false

		for k, v := range n.Params {
			if !domain.IsHiddenPersonal(v) {
				continue
			}

			if !copied {
				n.Params, copied = maps.Clone(n.Params), true
			}

			if old, ok := had[n.ID][k]; ok {
				n.Params[k] = old
			} else {
				delete(n.Params, k)
			}
		}

		nodes[i] = n
	}

	def.Nodes = nodes

	return def
}

// TradeScope is the scope a workflow's orders need, by the mode it trades in (D399): none
// for a workflow that places no orders.
type TradeScope func(ctx context.Context, w domain.Workflow) (domain.Scope, error)

// WithTradeScope holds tokens and agents to their scopes for a workflow's orders: saving,
// switching on or running one that places orders needs paper, or live where it trades
// live.
func WithTradeScope(f TradeScope) Option {
	return func(s *Service) { s.tradeScope = f }
}

// Credentials names a node type's settings that pick one of the owner's credentials.
type Credentials func(nodeType string) []string

// WithCredentials holds tokens and agents to their user's leave to use credentials
// (D404): saving, switching on or running a workflow that uses one needs it, for a
// workflow they build can send a credential wherever its nodes call.
func WithCredentials(f Credentials) Option {
	return func(s *Service) { s.creds = f }
}

// allowed is nil when the principal may save, switch on or run the workflow: its
// credentials need the user's leave (D404), its orders their scope (D399).
func (s *Service) allowed(ctx context.Context, p auth.Principal, w domain.Workflow) error {
	if s.creds != nil && !p.MayUseCredentials() {
		for _, n := range w.Nodes {
			for _, name := range s.creds(n.Type) {
				if v, set := n.Params[name]; set && v != nil && v != "" {
					return fmt.Errorf("%w: node %q uses a credential, and the user has not let agents and tokens use credentials: "+
						"Settings, Connected agents", domain.ErrForbidden, n.ID)
				}
			}
		}
	}

	return s.mayTrade(ctx, p, w)
}

// Confirmer holds a token's or an agent's live action for its user to confirm (D406): nil to
// go ahead.
type Confirmer interface {
	Hold(ctx context.Context, p auth.Principal, a domain.Ask) error
}

// WithConfirmations has tokens' and agents' live actions wait for their users where the
// users say so (D406): switching on, or running, a workflow that trades live, and saving a
// new graph for one that is on.
func WithConfirmations(c Confirmer) Option {
	return func(s *Service) { s.confirm = c }
}

// RunAsked is what a held run carries out.
type RunAsked struct {
	Trigger domain.NodeID `json:"trigger,omitempty"`
}

// ChangeAsked is what a held change carries out: the definition as it was given, on the
// version it was given for.
type ChangeAsked struct {
	Version    int        `json:"version"`
	Definition Definition `json:"definition"`
}

// hold keeps a token's or an agent's action on a workflow that trades live for its user
// (D406).
func (s *Service) hold(ctx context.Context, p auth.Principal, kind domain.ConfirmKind, w domain.Workflow, what string, payload any) error {
	if s.confirm == nil || s.tradeScope == nil || p.Itself() || p.Confirmed {
		return nil
	}

	sc, err := s.tradeScope(ctx, w)
	if err != nil || sc != domain.ScopeLive {
		return err
	}

	return s.confirm.Hold(ctx, p, domain.Ask{Kind: kind, WorkflowID: w.ID, What: fmt.Sprintf(what, w.Name), Payload: payload})
}

// mayTrade is nil when the principal may have the workflow place its orders.
func (s *Service) mayTrade(ctx context.Context, p auth.Principal, w domain.Workflow) error {
	if s.tradeScope == nil {
		return nil
	}

	sc, err := s.tradeScope(ctx, w)
	if err != nil || sc == "" {
		return err
	}

	return p.Need(sc)
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

	if err := s.apply(ctx, &w, keepHidden(def, nil)); err != nil {
		return domain.Workflow{}, err
	}

	if err := s.allowed(ctx, p, w); err != nil {
		return domain.Workflow{}, err
	}

	if err := s.repo.Create(ctx, w); err != nil {
		return domain.Workflow{}, err
	}

	s.log.Info(ctx, "Workflow created", "user_id", p.User.ID, "workflow_id", w.ID)

	return s.forCaller(p, w), nil
}

func (s *Service) Get(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error) {
	w, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return domain.Workflow{}, err
	}

	return s.forCaller(p, w), nil
}

func (s *Service) List(ctx context.Context, p auth.Principal, f ports.PageFilter) (ports.Page[domain.Workflow], error) {
	page, err := s.repo.List(ctx, p.User.ID, f)

	for i, w := range page.Items {
		page.Items[i] = s.forCaller(p, w)
	}

	return page, err
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

	if err := s.apply(ctx, &w, keepHidden(def, w.Nodes)); err != nil {
		return domain.Workflow{}, err
	}

	if err := s.allowed(ctx, p, w); err != nil {
		return domain.Workflow{}, err
	}

	if w.Active {
		if err := s.hold(ctx, p, domain.ConfirmChange, w, "Change %q, which trades live and is on", ChangeAsked{Version: version, Definition: def}); err != nil {
			return domain.Workflow{}, err
		}
	}

	w.Version = version + 1
	w.UpdatedAt = s.now()

	// A token's or an agent's change to one that is on names it on its runs, until it is
	// switched again (D407); the user's leaves it as it was.
	if w.Active && !p.Itself() {
		w.ActedBy = p.Actor(domain.ActorChanged)
	}

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

	return s.forCaller(p, w), nil
}

func (s *Service) Delete(ctx context.Context, p auth.Principal, id domain.WorkflowID) error {
	// A token or an agent deletes only where its user lets it (D403).
	if err := p.NeedDelete(); err != nil {
		return err
	}

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

	if err := s.allowed(ctx, p, w); err != nil {
		return domain.Workflow{}, err
	}

	if !w.Active {
		if err := s.hold(ctx, p, domain.ConfirmSwitchOn, w, "Switch on %q, which trades live", struct{}{}); err != nil {
			return domain.Workflow{}, err
		}
	}

	// Switching it on names who did, on its runs, until it is switched again (D407); one
	// on already stays as it was.
	if !w.Active {
		w.ActedBy = p.Actor(domain.ActorSwitchedOn)
	}

	if err := s.repo.SetActive(ctx, p.User.ID, id, true, s.now(), w.ActedBy); err != nil {
		return domain.Workflow{}, err
	}

	w.Active = true
	s.supervisor.Start(w)

	return s.forCaller(p, w), nil
}

func (s *Service) Deactivate(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error) {
	w, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return domain.Workflow{}, err
	}

	if err := s.repo.SetActive(ctx, p.User.ID, id, false, s.now(), domain.Actor{}); err != nil {
		return domain.Workflow{}, err
	}

	s.supervisor.Stop(id)
	w.Active, w.ActedBy = false, domain.Actor{}

	// Switched off is off: executions that wait would carry on otherwise.
	if s.waits != nil {
		if err := s.waits.CancelWorkflow(ctx, p.User.ID, id, "cancelled: the workflow was switched off"); err != nil {
			s.log.Warn(ctx, "Waiting executions not cancelled", "workflow_id", id, "error", err.Error())
		}
	}

	return s.forCaller(p, w), nil
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

// Pauser tells a workflow's owner why it was paused (D319).
type Pauser interface {
	WorkflowPaused(owner domain.UserID, w domain.Workflow, why string)
}

// WithPauser tells owners why their workflows were paused.
func WithPauser(p Pauser) Option {
	return func(s *Service) { s.pauser = p }
}

// Pause switches off each active workflow why gives a reason for, as its owner would,
// and tells them the reason (D319): a plugin it uses is gone, say. How many it paused.
func (s *Service) Pause(ctx context.Context, why func(w domain.Workflow) string) (int, error) {
	active, err := s.repo.ListActive(ctx)
	if err != nil {
		return 0, err
	}

	paused := 0

	for _, w := range active {
		reason := why(w)
		if reason == "" {
			continue
		}

		if err := s.repo.SetActive(ctx, w.OwnerID, w.ID, false, s.now(), domain.Actor{}); err != nil {
			return paused, err
		}

		s.supervisor.Stop(w.ID)

		if s.waits != nil {
			if err := s.waits.CancelWorkflow(ctx, w.OwnerID, w.ID, "cancelled: the workflow was paused: "+reason); err != nil {
				s.log.Warn(ctx, "Waiting executions not cancelled", "workflow_id", w.ID, "error", err.Error())
			}
		}

		if s.pauser != nil {
			s.pauser.WorkflowPaused(w.OwnerID, w, reason)
		}

		s.log.Info(ctx, "Workflow paused", "workflow_id", w.ID, "owner_id", w.OwnerID, "why", reason)

		paused++
	}

	return paused, nil
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

	if err := s.allowed(ctx, p, w); err != nil {
		return domain.Execution{}, err
	}

	if err := s.hold(ctx, p, domain.ConfirmRun, w, "Run %q by hand, which trades live", RunAsked{Trigger: trigger}); err != nil {
		return domain.Execution{}, err
	}

	res, err := s.executor.Execute(ctx, w, trigger, output, engine.KeepSamples(), engine.By(p.Actor(domain.ActorRan)))
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

// Question is what one of the workflow's executions asks while it waits for an answer
// (D405): nil when it waits for none, or was asked before questions were kept.
func (s *Service) Question(ctx context.Context, p auth.Principal, id domain.WorkflowID, execID domain.ExecutionID) (*ports.WaitQuestion, error) {
	rec, err := s.waiting(ctx, p, id, execID)
	if errors.Is(err, domain.ErrNotWaiting) {
		return nil, nil
	}

	if err != nil || rec.Key == "" {
		return nil, err
	}

	return rec.Question, nil
}

// Answer answers one of the workflow's executions that waits for an answer, picking the
// option at index option, as pressing its button does. A token or an agent answers only
// where its user lets it, and the question then says who answered (D405).
func (s *Service) Answer(ctx context.Context, p auth.Principal, id domain.WorkflowID, execID domain.ExecutionID, option int) error {
	if !p.MayAnswer() {
		return fmt.Errorf("%w: the user has not let agents and tokens answer their workflows' questions: Settings, Connected agents",
			domain.ErrForbidden)
	}

	rec, err := s.waiting(ctx, p, id, execID)
	if err != nil {
		return err
	}

	switch {
	case rec.Key == "":
		return fmt.Errorf("%w: it waits for its time, not for an answer", domain.ErrNotWaiting)
	case rec.Question == nil:
		return fmt.Errorf("%w: it was asked before questions could be answered here; answer it where it was asked", domain.ErrInvalidInput)
	case option < 0 || option >= len(rec.Question.Options):
		return fmt.Errorf("%w: it has %d options, not one at %d", domain.ErrInvalidInput, len(rec.Question.Options), option)
	}

	payload := map[string]any{"option": option}
	if !p.Itself() {
		payload[engine.AnsweredBy] = answerer(p)
	}

	err = s.waits.Answer(ctx, p.User.ID, execID, rec.Key, payload)
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("%w: it carried on meanwhile", domain.ErrNotWaiting)
	}

	if err == nil {
		s.log.Info(ctx, "Question answered", "user_id", p.User.ID, "workflow_id", id, "execution_id", execID, "by", answerer(p))
	}

	return err
}

// waiting is the wait of one of the workflow's executions that waits.
func (s *Service) waiting(ctx context.Context, p auth.Principal, id domain.WorkflowID, execID domain.ExecutionID) (ports.WaitRecord, error) {
	exec, err := s.Execution(ctx, p, id, execID)
	if err != nil {
		return ports.WaitRecord{}, err
	}

	if exec.Status != domain.ExecutionWaiting || s.waits == nil {
		return ports.WaitRecord{}, fmt.Errorf("%w: it is %s", domain.ErrNotWaiting, exec.Status)
	}

	rec, err := s.waits.Waiting(ctx, p.User.ID, execID)
	if errors.Is(err, domain.ErrNotFound) {
		return ports.WaitRecord{}, fmt.Errorf("%w: it carried on meanwhile", domain.ErrNotWaiting)
	}

	return rec, err
}

// answerer is who answers for the user: an agent by its name, or a token.
func answerer(p auth.Principal) string {
	switch {
	case p.Itself():
		return "the user"
	case p.Agent != "":
		return p.Agent
	}

	return "an API token"
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

	// Who may write code first: code they may not is never compiled (R5).
	codeRefused := false

	if s.code != nil {
		checks = append(checks, func() error {
			err := s.code.CheckCodeAccess(ctx, w)
			codeRefused = err != nil

			return err
		})
	}

	if s.exprs != nil {
		checks = append(checks, func() error {
			if codeRefused {
				return nil
			}

			return s.exprs.CheckExpressions(ctx, w)
		})
	}

	if s.plugins != nil {
		checks = append(checks, func() error { return s.plugins.CheckPluginAccess(ctx, w) })
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
