package mcpserver

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/library"
	"github.com/lein3000zzz/polythought-go/internal/core/workflowcode"
)

// nodeDeclarations are the node types' settings as TypeScript, too long to give whole:
// get_node_type gives a type's.
const nodeDeclarations = "nodes.d.ts"

type typesIn struct {
	Query string `json:"query,omitempty" jsonschema:"words its name, label or description has, to narrow the list"`
	page
}

type typeSummary struct {
	Name    string `json:"name"`
	Trigger bool   `json:"trigger,omitempty" jsonschema:"it starts executions"`
	Label   string `json:"label,omitempty"`
	Summary string `json:"summary,omitempty"`
	// Plugin is the plugin a type comes from, whose text is its author's.
	Plugin    string    `json:"plugin,omitempty"`
	Untrusted Untrusted `json:"untrusted,omitempty" jsonschema:"a plugin's label and summary, its author's words"`
}

type typeList struct {
	Types  []typeSummary `json:"types"`
	Next   string        `json:"next,omitempty"`
	Notice string        `json:"notice,omitempty"`
}

type typeIn struct {
	Name string `json:"name" jsonschema:"the node type's name, such as logic.if"`
}

type paramOut struct {
	Name        string    `json:"name"`
	Label       string    `json:"label,omitempty"`
	Kind        string    `json:"kind"`
	Description string    `json:"description,omitempty"`
	Required    bool      `json:"required,omitempty"`
	Default     any       `json:"default,omitempty"`
	Options     []string  `json:"options,omitempty" jsonschema:"the only values it takes"`
	Field       bool      `json:"field,omitempty" jsonschema:"it names a field of the items received, such as outcomes.0.price"`
	Personal    bool      `json:"personal,omitempty" jsonschema:"a personal setting, such as a webhook URL"`
	Credentials []string  `json:"credentials,omitempty" jsonschema:"the kinds of credential it takes"`
	Untrusted   Untrusted `json:"untrusted,omitempty"`
}

type fieldOut struct {
	Name        string   `json:"name,omitempty"`
	Kind        string   `json:"kind"`
	Description string   `json:"description,omitempty"`
	Optional    bool     `json:"optional,omitempty"`
	Nullable    bool     `json:"nullable,omitempty"`
	Options     []string `json:"options,omitempty"`
	// Of and Fields are fields again, typed any as the schema cannot hold a tree.
	Of     any   `json:"of,omitempty" jsonschema:"a list's elements, a field"`
	Fields []any `json:"fields,omitempty" jsonschema:"an object's fields"`
}

type sendsOut struct {
	Fields []any  `json:"fields,omitempty" jsonschema:"the fields of the items it sends"`
	Passed bool   `json:"passed,omitempty" jsonschema:"it sends on the items it receives, with these fields added"`
	Picked string `json:"picked,omitempty" jsonschema:"the setting naming the only fields it keeps"`
	Split  string `json:"split,omitempty" jsonschema:"the setting naming the list it sends an item for each entry of"`
}

type typeOut struct {
	Name        string     `json:"name"`
	Trigger     bool       `json:"trigger,omitempty"`
	Label       string     `json:"label,omitempty"`
	Description string     `json:"description,omitempty"`
	Inputs      []string   `json:"inputs,omitempty"`
	Outputs     []string   `json:"outputs,omitempty"`
	Settings    []paramOut `json:"settings"`
	Sends       *sendsOut  `json:"sends,omitempty"`
	Code        bool       `json:"code,omitempty" jsonschema:"it runs TypeScript the user writes"`
	Remembers   bool       `json:"remembers,omitempty" jsonschema:"it keeps what it saw across runs"`
	Trades      bool       `json:"trades,omitempty" jsonschema:"it places orders"`
	Plugin      string     `json:"plugin,omitempty"`
	Untrusted   Untrusted  `json:"untrusted,omitempty" jsonschema:"a plugin's label and description, its author's words"`
	Notice      string     `json:"notice,omitempty"`
}

type fileOut struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type sdkOut struct {
	Files []fileOut `json:"files"`
	Note  string    `json:"note"`
}

type templateSummary struct {
	ID      string `json:"id"`
	BuiltIn bool   `json:"builtIn,omitempty" jsonschema:"one of Polythought's own"`
	Mine    bool   `json:"mine,omitempty" jsonschema:"the user shared it"`
	Nodes   int    `json:"nodes"`
	Name    string `json:"name,omitempty"`
	Summary string `json:"summary,omitempty"`
	// Untrusted is another user's name and summary for it.
	Untrusted Untrusted `json:"untrusted,omitempty" jsonschema:"another user's name and summary for it"`
}

type templateList struct {
	Templates []templateSummary `json:"templates"`
	Next      string            `json:"next,omitempty"`
	Notice    string            `json:"notice,omitempty"`
}

type templateIn struct {
	ID     string `json:"id" jsonschema:"the library entry's ID"`
	Format string `json:"format,omitempty" jsonschema:"json, its graph, unless said; or typescript, its workflow code"`
}

type inputOut struct {
	Node    string `json:"node"`
	Setting string `json:"setting"`
	Label   string `json:"label,omitempty"`
}

// templateText is an entry's text and graph: its author's.
type templateText struct {
	Name        string `json:"name,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	Workflow    *graph `json:"workflow,omitempty"`
	Code        string `json:"code,omitempty"`
	// CustomNodes are the custom nodes it brings, code and all (D62, D405).
	CustomNodes []templateNodeOut `json:"customNodes,omitempty" jsonschema:"the custom nodes using it copies to the user's, their code included"`
}

type templateNodeOut struct {
	Type   string `json:"type"`
	Label  string `json:"label"`
	Source string `json:"source" jsonschema:"its TypeScript, which runs as the user's"`
}

type templateOut struct {
	ID        string     `json:"id"`
	BuiltIn   bool       `json:"builtIn,omitempty"`
	Mine      bool       `json:"mine,omitempty"`
	UpdatedAt time.Time  `json:"updatedAt"`
	Inputs    []inputOut `json:"inputs,omitempty" jsonschema:"what using it asks the user for"`

	*templateText

	// Untrusted is the same, for another user's entry: all of it is theirs.
	Untrusted *templateText `json:"untrusted,omitempty" jsonschema:"another user's entry, its text and its graph, code and all: theirs"`
	Notice    string        `json:"notice,omitempty"`
}

func (s *Server) addCatalogTools(srv *mcp.Server) {
	add(s, srv, Tool[typesIn, typeList]{
		Name: "list_node_types", Title: "List node types", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "The node types the user may put in a workflow: Polythought's, their own custom nodes, and plugins' they " +
			"may use. get_node_type gives one's settings and what it sends.",
		Handle: s.listNodeTypes,
	})

	add(s, srv, Tool[typeIn, typeOut]{
		Name: "get_node_type", Title: "Get a node type", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "One node type, as its docs page has it: what it does, its ports, each setting, and the fields of the items it sends.",
		Handle:      s.getNodeType,
	})

	add(s, srv, Tool[struct{}, sdkOut]{
		Name: "get_code_sdk", Title: "Get the code SDK", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "The TypeScript declarations code nodes and custom nodes are written against: what ctx offers, read-only data and all.",
		Handle:      s.getCodeSDK,
	})

	add(s, srv, Tool[page, templateList]{
		Name: "list_templates", Title: "List the library", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "The library's workflows to start from: Polythought's, the user's, and other users' shared ones, whose " +
			"words are theirs, under untrusted.",
		Handle: s.listTemplates,
	})

	add(s, srv, Tool[templateIn, templateOut]{
		Name: "get_template", Title: "Get a library entry", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "One library entry: what it does, what using it asks for, and its graph as JSON or TypeScript. Another " +
			"user's entry is theirs, code and all: under untrusted.",
		Handle: s.getTemplate,
	})
}

func (s *Server) listNodeTypes(ctx context.Context, p auth.Principal, in typesIn) (typeList, error) {
	all, err := s.nodeTypes.Types(ctx, p)
	if err != nil {
		return typeList{}, err
	}

	words := strings.Fields(strings.ToLower(in.Query))

	var found []engine.TypeInfo

	for _, t := range all {
		text := strings.ToLower(t.Name + " " + t.Label + " " + t.Description)
		if !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(text, w) }) {
			found = append(found, t)
		}
	}

	start, limit := in.offset(), in.filter().Limit
	out := typeList{Types: []typeSummary{}}

	for i := start; i < len(found) && i < start+limit; i++ {
		t := found[i]
		sum := typeSummary{Name: t.Name, Trigger: t.Trigger, Plugin: t.Plugin}

		if t.Plugin != "" {
			sum.Untrusted, out.Notice = Untrusted{"label": t.Label, "summary": firstSentence(t.Description)}, Notice
		} else {
			sum.Label, sum.Summary = t.Label, firstSentence(t.Description)
		}

		out.Types = append(out.Types, sum)
	}

	if start+limit < len(found) {
		out.Next = strconv.Itoa(start + limit)
	}

	return out, nil
}

func (s *Server) getNodeType(ctx context.Context, p auth.Principal, in typeIn) (typeOut, error) {
	types, err := s.types(ctx, p)
	if err != nil {
		return typeOut{}, err
	}

	t, ok := types[in.Name]
	if !ok {
		return typeOut{}, fmt.Errorf("%w: no node type %q the user may place; list_node_types lists them", domain.ErrNotFound, in.Name)
	}

	plugin := t.Plugin != ""
	out := typeOut{
		Name: t.Name, Trigger: t.Trigger, Inputs: t.Inputs, Outputs: t.Outputs, Settings: make([]paramOut, len(t.Params)),
		Code: t.Code, Remembers: t.Remembers, Trades: t.Trades, Plugin: t.Plugin,
	}

	if plugin {
		out.Untrusted, out.Notice = Untrusted{"label": t.Label, "description": t.Description}, Notice
	} else {
		out.Label, out.Description = t.Label, t.Description
	}

	for i, spec := range t.Params {
		ps := paramOut{
			Name: spec.Name, Kind: string(spec.Kind), Required: spec.Required, Default: spec.Default, Options: spec.Options,
			Field: spec.Field, Personal: spec.Sensitive, Credentials: spec.CredentialTypes,
		}

		if plugin {
			ps.Untrusted = Untrusted{"label": spec.Label, "description": spec.Description}
		} else {
			ps.Label, ps.Description = spec.Label, spec.Description
		}

		out.Settings[i] = ps
	}

	if t.Items != nil {
		out.Sends = &sendsOut{Fields: fieldsOf(t.Items.Fields), Passed: t.Items.Passed, Picked: t.Items.Picked, Split: t.Items.Split}
	}

	return out, nil
}

func fieldsOf(fs []engine.Field) []any {
	out := make([]any, len(fs))

	for i, f := range fs {
		field := fieldOut{
			Name: f.Name, Kind: string(f.Kind), Description: f.Description, Optional: f.Optional, Nullable: f.Nullable,
			Options: f.Options,
		}

		if len(f.Fields) > 0 {
			field.Fields = fieldsOf(f.Fields)
		}

		if f.Of != nil {
			field.Of = fieldsOf([]engine.Field{*f.Of})[0]
		}

		out[i] = field
	}

	return out
}

func (s *Server) getCodeSDK(ctx context.Context, p auth.Principal, _ struct{}) (sdkOut, error) {
	files, err := s.codeTypes.Declarations(ctx, p)
	if err != nil {
		return sdkOut{}, err
	}

	out := sdkOut{
		Files: []fileOut{},
		Note:  "The node types' settings, as TypeScript, are left out, being long: get_node_type gives a type's settings.",
	}

	for _, f := range files {
		if !strings.HasSuffix(f.Path, nodeDeclarations) {
			out.Files = append(out.Files, fileOut{Path: f.Path, Content: f.Content})
		}
	}

	return out, nil
}

func (s *Server) listTemplates(ctx context.Context, p auth.Principal, in page) (templateList, error) {
	got, err := s.library.List(ctx, p, in.filter())
	if err != nil {
		return templateList{}, err
	}

	out := templateList{Templates: make([]templateSummary, len(got.Items)), Next: got.NextCursor}

	for i, e := range got.Items {
		if out.Templates[i] = templateSummaryOf(e); others(e) {
			out.Notice = Notice
		}
	}

	return out, nil
}

func templateSummaryOf(e library.Entry) templateSummary {
	sum := templateSummary{ID: string(e.ID), BuiltIn: e.BuiltIn(), Mine: e.Mine, Nodes: len(e.Nodes)}

	if others(e) {
		sum.Untrusted = Untrusted{"name": e.Name, "summary": e.Summary}
	} else {
		sum.Name, sum.Summary = e.Name, e.Summary
	}

	return sum
}

func (s *Server) getTemplate(ctx context.Context, p auth.Principal, in templateIn) (templateOut, error) {
	format, err := formatOf(in.Format)
	if err != nil {
		return templateOut{}, err
	}

	e, err := s.library.Get(ctx, p, domain.TemplateID(in.ID))
	if err != nil {
		return templateOut{}, err
	}

	text := &templateText{Name: e.Name, Summary: e.Summary, Description: e.Description}

	for _, n := range e.CustomNodes {
		text.CustomNodes = append(text.CustomNodes, templateNodeOut{Type: engine.CustomTypePrefix + n.Name, Label: n.Label, Source: n.Source})
	}

	if format == formatTypeScript {
		types, err := s.types(ctx, p)
		if err != nil {
			return templateOut{}, err
		}

		text.Code = workflowcode.Print(e.Name, e.Nodes, e.Connections, types.printer(e.CustomTypes...))
	} else {
		text.Workflow = graphOf(e.Nodes, e.Connections)
	}

	out := templateOut{ID: string(e.ID), BuiltIn: e.BuiltIn(), Mine: e.Mine, UpdatedAt: e.UpdatedAt}

	for _, input := range e.Inputs {
		out.Inputs = append(out.Inputs, inputOut{Node: string(input.NodeID), Setting: input.Param.Name, Label: input.Param.Label})
	}

	if others(e) {
		out.Untrusted, out.Notice = text, Notice
	} else {
		out.templateText = text
	}

	return out, nil
}

// others says an entry is another user's: its words are theirs.
func others(e library.Entry) bool { return !e.BuiltIn() && !e.Mine }

// firstSentence is a description's first sentence, for a list.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}

	return s
}
