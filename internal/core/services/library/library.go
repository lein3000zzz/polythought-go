// Package library lists workflows to start from: built-in scenarios that show what
// Polythought can do, and workflows members shared. Starting from one copies it into a
// new workflow of your own.
package library

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/customnodes"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
)

const (
	// MaxShared bounds the copies one member keeps in the library.
	MaxShared = 20

	maxSummary     = 200
	maxDescription = 4000
)

// Workflows is what the library needs from the workflow service.
type Workflows interface {
	Get(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error)
	Create(ctx context.Context, p auth.Principal, def workflows.Definition) (domain.Workflow, error)
}

// NodeTypes tells which settings of a node are personal or still to be filled in.
type NodeTypes interface {
	Type(name string) (typ engine.NodeType, trigger bool, ok bool)
}

// Input is a setting a template leaves for whoever uses it to fill in, like a wallet.
type Input struct {
	NodeID domain.NodeID
	Param  engine.ParamSpec
}

// Entry is a template as one member sees it.
type Entry struct {
	domain.Template

	Mine   bool    // shared by this member
	Inputs []Input // what using it asks for

	// CustomTypes are the node types of the custom nodes it brings, for drawing it.
	CustomTypes []engine.NodeType
}

type ShareInput struct {
	Summary     string
	Description string
}

type UseInput struct {
	Name   string // empty keeps the template's
	Values map[domain.NodeID]map[string]any
}

type Service struct {
	repo      ports.TemplateRepository
	workflows Workflows
	types     NodeTypes
	own       engine.Catalog // each sharer's types, their custom nodes included; types if not set
	custom    CustomNodes    // bundles and copies custom nodes; none if not set
	log       logging.Logger
	now       func() time.Time

	builtins []domain.Template
	byID     map[domain.TemplateID]domain.Template
}

type Option func(*Service)

// WithClock replaces time.Now, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithOwnTypes takes a sharer's node types from c, so the personal settings of their
// custom nodes, like a wallet, are left out of what they share too.
func WithOwnTypes(c engine.Catalog) Option {
	return func(s *Service) { s.own = c }
}

// WithScenarios adds scenarios to the built-in ones: the plugins' loaded (D284).
func WithScenarios(ts []domain.Template) Option {
	return func(s *Service) {
		for _, t := range ts {
			if _, taken := s.byID[t.ID]; !taken {
				s.builtins = append(s.builtins, t)
				s.byID[t.ID] = t
			}
		}
	}
}

func New(repo ports.TemplateRepository, wf Workflows, types NodeTypes, log logging.Logger, opts ...Option) (*Service, error) {
	builtins, err := Builtins()
	if err != nil {
		return nil, err
	}

	s := &Service{
		repo:      repo,
		workflows: wf,
		types:     types,
		log:       log.With("component", "library"),
		now:       time.Now,
		builtins:  builtins,
		byID:      make(map[domain.TemplateID]domain.Template, len(builtins)),
	}

	for _, t := range builtins {
		s.byID[t.ID] = t
	}

	for _, opt := range opts {
		opt(s)
	}

	return s, nil
}

// List returns the built-in scenarios, on the first page only, then members' shared
// workflows, newest first.
func (s *Service) List(ctx context.Context, p auth.Principal, f ports.PageFilter) (ports.Page[Entry], error) {
	shared, err := s.repo.List(ctx, f)
	if err != nil {
		return ports.Page[Entry]{}, err
	}

	var page ports.Page[Entry]

	if f.Cursor == "" {
		for _, t := range s.builtins {
			page.Items = append(page.Items, s.entry(p, t))
		}
	}

	for _, t := range shared.Items {
		page.Items = append(page.Items, s.entry(p, t))
	}

	page.NextCursor = shared.NextCursor

	return page, nil
}

func (s *Service) Get(ctx context.Context, p auth.Principal, id domain.TemplateID) (Entry, error) {
	t, err := s.template(ctx, id)
	if err != nil {
		return Entry{}, err
	}

	return s.entry(p, t), nil
}

// Use copies the template into a new, inactive workflow of p's, with values for its
// inputs. Values left out make the new workflow invalid, reported as for any save.
func (s *Service) Use(ctx context.Context, p auth.Principal, id domain.TemplateID, in UseInput) (domain.Workflow, error) {
	t, err := s.template(ctx, id)
	if err != nil {
		return domain.Workflow{}, err
	}

	asked := make(map[domain.NodeID]map[string]bool)
	for _, input := range s.inputs(t) {
		if asked[input.NodeID] == nil {
			asked[input.NodeID] = make(map[string]bool)
		}

		asked[input.NodeID][input.Param.Name] = true
	}

	nodes := make([]domain.NodeSpec, len(t.Nodes))

	for i, n := range t.Nodes {
		n.Params = maps.Clone(n.Params)

		for name, v := range in.Values[n.ID] {
			if !asked[n.ID][name] {
				return domain.Workflow{}, fmt.Errorf("%w: %s of node %q is not something this template asks for", domain.ErrInvalidInput, name, n.ID)
			}

			if n.Params == nil {
				n.Params = make(map[string]any)
			}

			n.Params[name] = v
		}

		nodes[i] = n
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = t.Name
	}

	// The custom nodes it brings first: the workflow's nodes name them.
	renamed, made, err := s.copyCustomNodes(ctx, p, t.CustomNodes)
	if err != nil {
		return domain.Workflow{}, err
	}

	for i, n := range nodes {
		if to, ok := renamed[n.Type]; ok {
			nodes[i].Type = to
		}
	}

	w, err := s.workflows.Create(ctx, p, workflows.Definition{Name: name, Nodes: nodes, Connections: t.Connections})
	if err != nil {
		s.removeCopies(ctx, p, made)
		return domain.Workflow{}, err
	}

	s.log.Info(ctx, "Template used", "template_id", t.ID, "workflow_id", w.ID, "user_id", p.User.ID)

	return w, nil
}

// Share puts a copy of one of p's workflows in the library, or updates the copy shared
// before. Personal settings, wallets and values like webhook URLs, are left out: whoever
// uses the copy fills in their own.
func (s *Service) Share(ctx context.Context, p auth.Principal, workflowID domain.WorkflowID, in ShareInput) (Entry, error) {
	// A token or an agent publishes only where its user lets it (D403).
	if !p.MayShare() {
		return Entry{}, fmt.Errorf("%w: the user has not let agents and tokens share to the library: Settings, Connected agents", domain.ErrForbidden)
	}

	summary, description := strings.TrimSpace(in.Summary), strings.TrimSpace(in.Description)

	if utf8.RuneCountInString(summary) > maxSummary || utf8.RuneCountInString(description) > maxDescription {
		return Entry{}, fmt.Errorf("%w: a summary of at most %d characters and a description of at most %d", domain.ErrInvalidInput, maxSummary, maxDescription)
	}

	w, err := s.workflows.Get(ctx, p, workflowID)
	if err != nil {
		return Entry{}, err
	}

	types := s.types
	if s.own != nil {
		if types, err = s.own.For(ctx, p.User.ID); err != nil {
			return Entry{}, err
		}
	}

	bundled, err := s.bundle(ctx, p, w.Nodes)
	if err != nil {
		return Entry{}, err
	}

	now := s.now()
	t := domain.Template{
		ID: domain.TemplateID(uuid.Must(uuid.NewV7()).String()), AuthorID: p.User.ID, SourceID: w.ID,
		Name: w.Name, Summary: summary, Description: description,
		Nodes: withoutPersonal(types, w.Nodes), Connections: w.Connections, CustomNodes: bundled,
		CreatedAt: now, UpdatedAt: now,
	}

	if _, err := s.repo.BySource(ctx, p.User.ID, w.ID); err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return Entry{}, err
		}

		// A new copy: within the member's allowance.
		count, err := s.repo.CountByAuthor(ctx, p.User.ID)
		if err != nil {
			return Entry{}, err
		}

		if count >= MaxShared {
			return Entry{}, fmt.Errorf("%w: at most %d shared workflows per member; delete one first", domain.ErrInvalidInput, MaxShared)
		}
	}

	saved, err := s.repo.Save(ctx, t)
	if err != nil {
		return Entry{}, err
	}

	s.log.Info(ctx, "Workflow shared", "template_id", saved.ID, "workflow_id", w.ID, "user_id", p.User.ID)

	return s.entry(p, saved), nil
}

// SharedCopy returns the copy p shared of a workflow, or domain.ErrNotFound.
func (s *Service) SharedCopy(ctx context.Context, p auth.Principal, workflowID domain.WorkflowID) (Entry, error) {
	if _, err := s.workflows.Get(ctx, p, workflowID); err != nil {
		return Entry{}, err
	}

	t, err := s.repo.BySource(ctx, p.User.ID, workflowID)
	if err != nil {
		return Entry{}, err
	}

	return s.entry(p, t), nil
}

// Delete removes a shared copy: its author may, and so may admins, to keep the library
// clean. Built-in scenarios cannot be deleted. A token or an agent takes one down where
// its user lets it share (D405), another's only with the admin scope too.
func (s *Service) Delete(ctx context.Context, p auth.Principal, id domain.TemplateID) error {
	if !p.MayShare() {
		return fmt.Errorf("%w: the user has not let agents and tokens share to the library, or take entries down: Settings, Connected agents",
			domain.ErrForbidden)
	}

	if _, ok := s.byID[id]; ok {
		return fmt.Errorf("%w: built-in scenarios cannot be deleted", domain.ErrForbidden)
	}

	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}

	if t.AuthorID != p.User.ID && (!p.User.IsAdmin() || !p.Can(domain.ScopeAdmin)) {
		return domain.ErrForbidden
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}

	s.log.Info(ctx, "Shared workflow deleted", "template_id", id, "author_id", t.AuthorID, "user_id", p.User.ID)

	return nil
}

func (s *Service) template(ctx context.Context, id domain.TemplateID) (domain.Template, error) {
	if t, ok := s.byID[id]; ok {
		return t, nil
	}

	return s.repo.Get(ctx, id)
}

func (s *Service) entry(p auth.Principal, t domain.Template) Entry {
	e := Entry{Template: t, Mine: !t.BuiltIn() && t.AuthorID == p.User.ID, Inputs: s.inputs(t)}

	for _, n := range t.CustomNodes {
		e.CustomTypes = append(e.CustomTypes, customnodes.TypeOf(n))
	}

	return e
}

// inputs lists the settings a template needs and does not have: required, without a
// default, and empty. Shared copies have their personal settings removed, so those
// become inputs; built-in scenarios leave out the ones only their user can know. Empty
// credentials are asked for too, required or not: only their user has them.
func (s *Service) inputs(t domain.Template) []Input {
	var out []Input

	types := s.typesOf(t)

	for _, n := range t.Nodes {
		typ, _, ok := types.Type(n.Type)
		if !ok {
			continue
		}

		for _, param := range typ.Params {
			if n.Params[param.Name] != nil {
				continue
			}

			if param.Required && param.Default == nil || param.Kind == engine.ParamCredential {
				out = append(out, Input{NodeID: n.ID, Param: param})
			}
		}
	}

	return out
}

func withoutPersonal(types NodeTypes, nodes []domain.NodeSpec) []domain.NodeSpec {
	out := make([]domain.NodeSpec, len(nodes))

	for i, n := range nodes {
		typ, _, ok := types.Type(n.Type)
		n.Params = maps.Clone(n.Params)

		if ok {
			for _, param := range typ.Params {
				if param.Personal() {
					delete(n.Params, param.Name)
				}
			}
		}

		out[i] = n
	}

	return out
}
