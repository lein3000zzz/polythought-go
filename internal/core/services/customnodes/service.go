// Package customnodes keeps the node types users define (D49). It checks and stores their
// definitions, warns before a change breaks the workflows that use one (D50), and gives
// each owner's workflows the types they may use: the built-in ones and their own.
package customnodes

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/customnodes"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// Limits bound what one user keeps (D56).
type Limits struct {
	PerUser int
}

func DefaultLimits() Limits {
	return Limits{PerUser: 50}
}

// Definition is what a custom node's form sets.
type Definition struct {
	Name        string
	Label       string
	Description string
	Settings    []domain.CustomSetting
	Outputs     []string
	Items       domain.CustomItems
	Source      string
}

type Service struct {
	repo      ports.CustomNodeRepository
	workflows ports.WorkflowRepository
	base      *engine.Registry // the built-in types
	code      ports.CodeCaller
	compiler  ports.CodeCompiler
	gate      engine.CodeGate
	limits    func() Limits
	log       logging.Logger
	now       func() time.Time

	mu    sync.Mutex
	cache map[domain.UserID]*engine.Registry // each owner's types, until one of theirs changes
	gen   map[domain.UserID]uint64           // counts the changes, so a load that raced one is not kept
}

var _ engine.Catalog = (*Service)(nil)

type Option func(*Service)

func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithLiveLimits reads the limits through limits on every check, so changes apply at once.
func WithLiveLimits(limits func() Limits) Option {
	return func(s *Service) { s.limits = limits }
}

// New keeps custom nodes in repo. Their code compiles with compiler and runs through
// code; gate says who may write it (D44); base has the built-in types they join.
func New(
	repo ports.CustomNodeRepository,
	workflows ports.WorkflowRepository,
	base *engine.Registry,
	code ports.CodeCaller,
	compiler ports.CodeCompiler,
	gate engine.CodeGate,
	log logging.Logger,
	opts ...Option,
) *Service {
	s := &Service{
		repo: repo, workflows: workflows, base: base, code: code, compiler: compiler, gate: gate,
		limits: DefaultLimits,
		log:    log.With("component", "custom_nodes"),
		now:    func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) },
		cache:  make(map[domain.UserID]*engine.Registry),
		gen:    make(map[domain.UserID]uint64),
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

func (s *Service) List(ctx context.Context, p auth.Principal) ([]domain.CustomNode, error) {
	return s.repo.List(ctx, p.User.ID)
}

func (s *Service) Get(ctx context.Context, p auth.Principal, id domain.CustomNodeID) (domain.CustomNode, error) {
	return s.repo.Get(ctx, p.User.ID, id)
}

// Create makes a custom node of def for the principal, who must be allowed to write code.
func (s *Service) Create(ctx context.Context, p auth.Principal, def Definition) (domain.CustomNode, error) {
	if err := s.mayWrite(ctx, p); err != nil {
		return domain.CustomNode{}, err
	}

	total, err := s.repo.CountForOwner(ctx, p.User.ID)
	if err != nil {
		return domain.CustomNode{}, err
	}

	if most := s.limits().PerUser; total >= most {
		return domain.CustomNode{}, fmt.Errorf("%w: at most %d custom nodes per user", domain.ErrInvalidInput, most)
	}

	now := s.now()
	n := domain.CustomNode{
		ID:      domain.CustomNodeID(uuid.Must(uuid.NewV7()).String()),
		OwnerID: p.User.ID, Version: 1, CreatedAt: now, UpdatedAt: now,
	}

	if err := s.apply(ctx, &n, def); err != nil {
		return domain.CustomNode{}, err
	}

	if err := s.repo.Create(ctx, n); err != nil {
		return domain.CustomNode{}, err
	}

	s.forget(n.OwnerID)
	s.log.Info(ctx, "Custom node created", "user_id", n.OwnerID, "custom_node_id", n.ID, "name", n.Name)

	return n, nil
}

// Update replaces the definition if version is still current. A change that breaks
// workflows using the node is refused with a *domain.BreaksError saying how, unless
// confirm says to go ahead (D50). The name stays: it is the type workflows name.
func (s *Service) Update(
	ctx context.Context,
	p auth.Principal,
	id domain.CustomNodeID,
	version int,
	def Definition,
	confirm bool,
) (domain.CustomNode, error) {
	if err := s.mayWrite(ctx, p); err != nil {
		return domain.CustomNode{}, err
	}

	n, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return domain.CustomNode{}, err
	}

	if n.Version != version {
		return domain.CustomNode{}, domain.ErrConflict
	}

	if def.Name != "" && def.Name != n.Name {
		return domain.CustomNode{}, invalid("name", "a custom node keeps its name, which workflows use; make a new one for another")
	}

	def.Name = n.Name

	edited := n
	if err := s.apply(ctx, &edited, def); err != nil {
		return domain.CustomNode{}, err
	}

	edited.Version, edited.UpdatedAt = version+1, s.now()

	if !confirm {
		if err := s.breaks(ctx, n, &edited); err != nil {
			return domain.CustomNode{}, err
		}
	}

	if err := s.repo.Update(ctx, edited, version); err != nil {
		return domain.CustomNode{}, err
	}

	s.forget(n.OwnerID)
	s.log.Info(ctx, "Custom node updated", "user_id", n.OwnerID, "custom_node_id", n.ID, "version", edited.Version)

	return edited, nil
}

// Delete removes a custom node. If workflows use it, it is refused with a
// *domain.BreaksError naming them, unless confirm says to go ahead (D50). Anyone may
// delete their own, code or no code.
func (s *Service) Delete(ctx context.Context, p auth.Principal, id domain.CustomNodeID, confirm bool) error {
	// A token or an agent deletes only where its user lets it (D404).
	if err := p.NeedDelete(); err != nil {
		return err
	}

	n, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return err
	}

	if !confirm {
		if err := s.breaks(ctx, n, nil); err != nil {
			return err
		}
	}

	if err := s.repo.Delete(ctx, p.User.ID, id); err != nil {
		return err
	}

	s.forget(n.OwnerID)
	s.log.Info(ctx, "Custom node deleted", "user_id", n.OwnerID, "custom_node_id", n.ID)

	return nil
}

func (s *Service) mayWrite(ctx context.Context, p auth.Principal) error {
	allowed, err := s.gate.CodeAllowed(ctx, p.User.ID)
	if err != nil {
		return err
	}

	if !allowed {
		return domain.ErrCodeOff
	}

	return nil
}

// For gives the types owner's workflows use: the built-in ones and their custom nodes.
func (s *Service) For(ctx context.Context, owner domain.UserID) (*engine.Registry, error) {
	s.mu.Lock()
	reg, ok := s.cache[owner]
	gen := s.gen[owner]
	s.mu.Unlock()

	if ok {
		return reg, nil
	}

	nodes, err := s.repo.List(ctx, owner)
	if err != nil {
		return nil, err
	}

	reg = s.registry(ctx, nodes)

	s.mu.Lock()
	if s.gen[owner] == gen {
		s.cache[owner] = reg
	}
	s.mu.Unlock()

	return reg, nil
}

// registry joins nodes to the built-in types. One that no longer registers, as after an
// upgrade made a rule stricter, is left out, so the others still work; the workflows
// using it fail saying its type is unknown.
func (s *Service) registry(ctx context.Context, nodes []domain.CustomNode) *engine.Registry {
	reg, _ := s.base.With() // a copy cannot fail

	for _, n := range nodes {
		if err := reg.RegisterNode(customnodes.New(n, s.code, s.log)); err != nil {
			s.log.Warn(ctx, "Custom node left out", "user_id", n.OwnerID, "custom_node_id", n.ID, logging.Err(err))
		}
	}

	return reg
}

func (s *Service) forget(owner domain.UserID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.cache, owner)
	s.gen[owner]++
}

// breaks reports the workflows of n's owner that changing n to edited, or deleting it
// when edited is nil, breaks: a problem saving would refuse that is not there now.
func (s *Service) breaks(ctx context.Context, n domain.CustomNode, edited *domain.CustomNode) error {
	nodes, err := s.repo.List(ctx, n.OwnerID)
	if err != nil {
		return err
	}

	now := s.registry(ctx, nodes)

	nodes = slices.DeleteFunc(nodes, func(c domain.CustomNode) bool { return c.ID == n.ID })
	if edited != nil {
		nodes = append(nodes, *edited)
	}

	after := s.registry(ctx, nodes)
	typ := engine.CustomTypePrefix + n.Name

	var broken []domain.BrokenWorkflow

	for cursor := ""; ; {
		page, err := s.workflows.List(ctx, n.OwnerID, ports.WorkflowFilter{PageFilter: ports.PageFilter{Limit: 200, Cursor: cursor}})
		if err != nil {
			return err
		}

		for _, w := range page.Items {
			if !slices.ContainsFunc(w.Nodes, func(spec domain.NodeSpec) bool { return spec.Type == typ }) {
				continue
			}

			before := problems(now, w)

			var added []domain.Problem

			for _, p := range problems(after, w) {
				if !slices.ContainsFunc(before, func(b domain.Problem) bool { return same(b, p) }) {
					added = append(added, p)
				}
			}

			if len(added) > 0 {
				broken = append(broken, domain.BrokenWorkflow{ID: w.ID, Name: w.Name, Problems: added})
			}
		}

		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}

	if len(broken) > 0 {
		return &domain.BreaksError{Workflows: broken}
	}

	return nil
}

// problems are what saving w with the types of reg would refuse, but for its code and
// expressions, which a node type does not change.
func problems(reg *engine.Registry, w domain.Workflow) []domain.Problem {
	var v domain.ValidationError

	v.Merge(reg.Validate(w))
	v.Merge(reg.CheckFields(w))

	return v.Problems
}

func same(a, b domain.Problem) bool {
	return a.NodeID == b.NodeID && a.Param == b.Param && a.Message == b.Message
}
