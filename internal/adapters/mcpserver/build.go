package mcpserver

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/library"
	"github.com/lein3000zzz/polythought-go/internal/core/workflowcode"
)

// The tools that read what users build (D401): their workflows, as JSON or TypeScript,
// their problems, their executions and the items kept, the node catalog with its docs,
// the SDK's types, the library.

// Workflows reads the user's workflows and their executions: the workflows service.
type Workflows interface {
	List(ctx context.Context, p auth.Principal, f ports.PageFilter) (ports.Page[domain.Workflow], error)
	Get(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error)
	Check(ctx context.Context, p auth.Principal, id domain.WorkflowID) ([]domain.Problem, error)
	Executions(ctx context.Context, p auth.Principal, id domain.WorkflowID, f ports.PageFilter) (ports.Page[domain.Execution], error)
	Execution(ctx context.Context, p auth.Principal, id domain.WorkflowID, execID domain.ExecutionID) (domain.Execution, error)
	Question(ctx context.Context, p auth.Principal, id domain.WorkflowID, execID domain.ExecutionID) (*ports.WaitQuestion, error)
}

// NodeTypes are the node types the user may place: the built-in ones, their own, and
// the plugins' they may use.
type NodeTypes interface {
	Types(ctx context.Context, p auth.Principal) ([]engine.TypeInfo, error)
}

// CodeTypes are the declarations user code is written against.
type CodeTypes interface {
	Declarations(ctx context.Context, p auth.Principal) ([]ports.CodeFile, error)
}

// Library is the library of workflows to start from: the library service.
type Library interface {
	List(ctx context.Context, p auth.Principal, f ports.PageFilter) (ports.Page[library.Entry], error)
	Get(ctx context.Context, p auth.Principal, id domain.TemplateID) (library.Entry, error)
}

// Trading says how a workflow trades: none, for one that places no orders, paper or live.
type Trading func(ctx context.Context, w domain.Workflow) (string, error)

const (
	pageSize = 20
	pageMost = 50
)

type page struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"how many to give: 20 unless said, at most 50"`
	Cursor string `json:"cursor,omitempty" jsonschema:"the next of the page before, for the page after it"`
}

func (pg page) filter() ports.PageFilter {
	limit := pg.Limit
	if limit <= 0 {
		limit = pageSize
	}

	return ports.PageFilter{Limit: min(limit, pageMost), Cursor: pg.Cursor}
}

// offset is where a page of a list in memory starts, from its cursor.
func (pg page) offset() int {
	n, err := strconv.Atoi(pg.Cursor)
	if err != nil || n < 0 {
		return 0
	}

	return n
}

// The forms a workflow is given in (D400).
const (
	formatJSON       = "json"
	formatTypeScript = "typescript"
)

// graph is a workflow's nodes and connections, as JSON gives them: no positions.
type graph struct {
	Nodes       []nodeOut `json:"nodes"`
	Connections []connOut `json:"connections"`
}

type nodeOut struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	Name           string         `json:"name,omitempty"`
	Notes          string         `json:"notes,omitempty"`
	ContinueOnFail bool           `json:"continueOnFail,omitempty"`
	Params         map[string]any `json:"params,omitempty"`
}

type connOut struct {
	From     string `json:"from"`
	FromPort string `json:"fromPort"`
	To       string `json:"to"`
	ToPort   string `json:"toPort"`
}

// typeIndex is the user's node types by name.
type typeIndex map[string]engine.TypeInfo

func (s *Server) types(ctx context.Context, p auth.Principal) (typeIndex, error) {
	list, err := s.nodeTypes.Types(ctx, p)
	if err != nil {
		return nil, err
	}

	out := make(typeIndex, len(list))
	for _, t := range list {
		out[t.Name] = t
	}

	return out, nil
}

// printer is what the TypeScript printer needs of the types.
func (ti typeIndex) printer(extra ...engine.NodeType) workflowcode.Types {
	more := map[string]engine.NodeType{}
	for _, t := range extra {
		more[t.Name] = t
	}

	return func(name string) (workflowcode.Type, bool) {
		var (
			t       engine.NodeType
			trigger bool
		)

		if info, ok := ti[name]; ok {
			t, trigger = info.NodeType, info.Trigger
		} else if m, ok := more[name]; ok {
			t = m
		} else {
			return workflowcode.Type{}, false
		}

		params := make([]string, len(t.Params))
		for i, p := range t.Params {
			params[i] = p.Name
		}

		return workflowcode.Type{Trigger: trigger, Params: params}, true
	}
}

// hiddenIn are the personal settings the workflows service hid from the caller (D402), as
// node.param.
func hiddenIn(nodes []domain.NodeSpec) []string {
	var out []string

	for _, n := range nodes {
		for k, v := range n.Params {
			if domain.IsHiddenPersonal(v) {
				out = append(out, string(n.ID)+"."+k)
			}
		}
	}

	return sortedStrings(out)
}

func graphOf(nodes []domain.NodeSpec, conns []domain.Connection) *graph {
	g := &graph{Nodes: make([]nodeOut, len(nodes)), Connections: make([]connOut, len(conns))}

	for i, n := range nodes {
		g.Nodes[i] = nodeOut{
			ID: string(n.ID), Type: n.Type, Name: n.Name, Notes: n.Notes, ContinueOnFail: n.ContinueOnFail, Params: n.Params,
		}
	}

	for i, c := range conns {
		g.Connections[i] = connOut{From: string(c.From), FromPort: c.FromPort, To: string(c.To), ToPort: c.ToPort}
	}

	return g
}

// formatOf is the form asked for, JSON unless TypeScript.
func formatOf(f string) (string, error) {
	switch f {
	case "", formatJSON:
		return formatJSON, nil
	case formatTypeScript:
		return formatTypeScript, nil
	}

	return "", invalid("format is json or typescript")
}

func invalid(why string) error { return fmt.Errorf("%w: %s", domain.ErrInvalidInput, why) }

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}

	return &t
}

func sortedStrings(s []string) []string {
	slices.Sort(s)
	return s
}
