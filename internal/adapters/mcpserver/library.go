package mcpserver

import (
	"context"
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/library"
)

// The tools that use and share library entries (D405): any entry made into a workflow of
// the user's, switched off, another user's saying it brings their code; the user's
// workflows shared and taken down where the user lets agents share.

// Sharing is what the library tools change: the library service.
type Sharing interface {
	Use(ctx context.Context, p auth.Principal, id domain.TemplateID, in library.UseInput) (domain.Workflow, error)
	Share(ctx context.Context, p auth.Principal, workflowID domain.WorkflowID, in library.ShareInput) (library.Entry, error)
	Delete(ctx context.Context, p auth.Principal, id domain.TemplateID) error
}

type useIn struct {
	ID     string                    `json:"id" jsonschema:"the library entry's ID"`
	Name   string                    `json:"name,omitempty" jsonschema:"the new workflow's name; the entry's unless said"`
	Values map[string]map[string]any `json:"values,omitempty" jsonschema:"what using it asks for, as get_template's inputs say: by node, then setting"`
}

type usedOut struct {
	workflowSummary

	// Brings is another user's code the workflow has from the entry.
	Brings *broughtOut `json:"brings,omitempty" jsonschema:"another user's code it brings, which runs as the user's"`
	Note   string      `json:"note,omitempty"`
}

type broughtOut struct {
	CodeNodes   []string `json:"codeNodes,omitempty" jsonschema:"its nodes that run code, by ID"`
	CustomNodes []string `json:"customNodes,omitempty" jsonschema:"the custom nodes it copied to the user's, by type"`
}

type shareIn struct {
	Workflow    string `json:"workflow" jsonschema:"the workflow's ID"`
	Summary     string `json:"summary,omitempty" jsonschema:"one line for the library's list, up to 200 characters"`
	Description string `json:"description,omitempty" jsonschema:"what it does and what to change, in plain paragraphs"`
}

func (s *Server) addLibraryTools(srv *mcp.Server) {
	add(s, srv, Tool[useIn, usedOut]{
		Name: "use_template", Title: "Use a library entry", Scope: domain.ScopeBuild,
		Description: "Make a workflow of the user's from a library entry, switched off, with the values its inputs ask for: " +
			"get_template lists them. Another user's entry brings their code, which then runs as the user's: the answer " +
			"says which, for the user to read (get_template has it) before it is switched on or run.",
		Handle: s.useTemplate,
	})

	add(s, srv, Tool[shareIn, templateSummary]{
		Name: "share_workflow", Title: "Share a workflow", Scope: domain.ScopeBuild,
		Description: "Put a copy of one of the user's workflows in the library, for everyone here to use, under the user's " +
			"name; shared again, the copy is updated. Its personal settings, wallets and credentials are left out, and the " +
			"custom nodes it uses go along, code and all. Only where the user lets agents share.",
		Handle: s.shareWorkflow,
	})

	add(s, srv, Tool[idIn, deletedOut]{
		Name: "unshare_template", Title: "Take a library entry down", Scope: domain.ScopeBuild, Destructive: true,
		Description: "Take one of the user's own library entries out of the library; the workflow it was shared from stays. " +
			"Only where the user lets agents share.",
		Handle: s.unshareTemplate,
	})
}

func (s *Server) useTemplate(ctx context.Context, p auth.Principal, in useIn) (usedOut, error) {
	e, err := s.library.Get(ctx, p, domain.TemplateID(in.ID))
	if err != nil {
		return usedOut{}, err
	}

	values := make(map[domain.NodeID]map[string]any, len(in.Values))
	for node, v := range in.Values {
		values[domain.NodeID(node)] = v
	}

	w, err := s.sharing.Use(ctx, p, e.ID, library.UseInput{Name: in.Name, Values: values})
	if err != nil {
		return usedOut{}, err
	}

	sum, err := s.summaryOf(ctx, w)
	if err != nil {
		return usedOut{}, err
	}

	out := usedOut{workflowSummary: sum}

	if others(e) {
		if out.Brings, err = s.brought(ctx, p, e); err != nil {
			return usedOut{}, err
		}

		if out.Brings != nil {
			out.Note = "This workflow runs another user's code as the user's. Show the user that code, get_template has it, " +
				"before switching it on or running it."
		}
	}

	return out, nil
}

// brought is the code an entry brings: its code nodes, and its custom nodes.
func (s *Server) brought(ctx context.Context, p auth.Principal, e library.Entry) (*broughtOut, error) {
	types, err := s.types(ctx, p)
	if err != nil {
		return nil, err
	}

	custom := map[string]bool{}
	for _, t := range e.CustomTypes {
		custom[t.Name] = true
	}

	var b broughtOut

	for _, n := range e.Nodes {
		if t, ok := types[n.Type]; ok && t.Code && !custom[n.Type] {
			b.CodeNodes = append(b.CodeNodes, string(n.ID))
		}
	}

	for name := range custom {
		b.CustomNodes = append(b.CustomNodes, name)
	}

	slices.Sort(b.CustomNodes)

	if len(b.CodeNodes) == 0 && len(b.CustomNodes) == 0 {
		return nil, nil
	}

	return &b, nil
}

func (s *Server) shareWorkflow(ctx context.Context, p auth.Principal, in shareIn) (templateSummary, error) {
	e, err := s.sharing.Share(ctx, p, domain.WorkflowID(in.Workflow), library.ShareInput{Summary: in.Summary, Description: in.Description})
	if err != nil {
		return templateSummary{}, err
	}

	return templateSummaryOf(e), nil
}

func (s *Server) unshareTemplate(ctx context.Context, p auth.Principal, in idIn) (deletedOut, error) {
	e, err := s.library.Get(ctx, p, domain.TemplateID(in.ID))
	if err != nil {
		return deletedOut{}, err
	}

	// An admin's say over others' entries is for the admin tools (7e).
	if !e.Mine {
		return deletedOut{}, fmt.Errorf("%w: only the user's own entries", domain.ErrForbidden)
	}

	if err := s.sharing.Delete(ctx, p, e.ID); err != nil {
		return deletedOut{}, err
	}

	return deletedOut{Deleted: in.ID}, nil
}
