package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/customnodes"
)

// The tools for the user's custom nodes (D404): node types of their own, a form and
// TypeScript, read, made, changed on the version read, and deleted where the user lets
// agents. A change that breaks workflows using one is made once the agent was told what.

// CustomNodes are the user's own node types: the custom nodes service.
type CustomNodes interface {
	List(ctx context.Context, p auth.Principal) ([]domain.CustomNode, error)
	Get(ctx context.Context, p auth.Principal, id domain.CustomNodeID) (domain.CustomNode, error)
	Create(ctx context.Context, p auth.Principal, def customnodes.Definition) (domain.CustomNode, error)
	Update(ctx context.Context, p auth.Principal, id domain.CustomNodeID, version int, def customnodes.Definition, confirm bool) (domain.CustomNode, error)
	Delete(ctx context.Context, p auth.Principal, id domain.CustomNodeID, confirm bool) error
}

const fieldShape = "each {name, kind: text, number, decimal, bool, time, list, object or any, description, optional, nullable, " +
	"options: the values text may take, of: a list's elements as a field, fields: an object's}"

type customSetting struct {
	Name        string   `json:"name" jsonschema:"what the code reads it as: ctx.params.<name>"`
	Label       string   `json:"label,omitempty"`
	Description string   `json:"description,omitempty"`
	Kind        string   `json:"kind" jsonschema:"string, number, decimal, bool, duration, list or wallet"`
	Required    bool     `json:"required,omitempty"`
	Default     any      `json:"default,omitempty"`
	Options     []string `json:"options,omitempty" jsonschema:"the only values a string, or each of a list, takes"`
	Field       bool     `json:"field,omitempty" jsonschema:"a string, or each of a list, names a field of the items it gets"`
	FieldKinds  []string `json:"fieldKinds,omitempty" jsonschema:"the kinds that field may be; any unless said"`
}

type customItems struct {
	Passed bool  `json:"passed,omitempty" jsonschema:"it sends on the items it gets, these fields added; else items of these fields alone"`
	Fields []any `json:"fields,omitempty" jsonschema:"the fields of the items it sends: each {name, kind, description, optional, nullable, options, of, fields}"`
}

type customDefinition struct {
	Name        string          `json:"name,omitempty" jsonschema:"makes its type custom.<name>: lowercase letters, digits and hyphens, from a letter, up to 40; it cannot change"`
	Label       string          `json:"label" jsonschema:"what the node list and the canvas call it"`
	Description string          `json:"description,omitempty"`
	Settings    []customSetting `json:"settings,omitempty"`
	Outputs     []string        `json:"outputs" jsonschema:"its output ports, in order, 1 to 10; it has one input, main"`
	Items       customItems     `json:"items"`
	Source      string          `json:"source" jsonschema:"TypeScript exporting run(items, ctx): every item at once; it returns the items, or with several outputs { port: items }"`
}

type customNodeOut struct {
	customDefinition

	ID        string    `json:"id"`
	Type      string    `json:"type" jsonschema:"what workflows name it by"`
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type customNodeSummary struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Label     string    `json:"label"`
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type customNodeList struct {
	CustomNodes []customNodeSummary `json:"customNodes"`
}

type customNodeIn struct {
	ID string `json:"id" jsonschema:"the custom node's ID"`
}

type createCustomNodeIn struct {
	customDefinition
}

type updateCustomNodeIn struct {
	ID      string `json:"id" jsonschema:"the custom node's ID"`
	Version int    `json:"version" jsonschema:"the version you read it at"`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"make it though it breaks workflows that use the node, once told which and how"`
	customDefinition
}

type deleteCustomNodeIn struct {
	ID      string `json:"id" jsonschema:"the custom node's ID"`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"delete it though workflows use it, once told which"`
}

func (s *Server) addCustomNodeTools(srv *mcp.Server) {
	add(s, srv, Tool[struct{}, customNodeList]{
		Name: "list_custom_nodes", Title: "List custom nodes", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "The user's custom nodes: node types of their own, a form and TypeScript. get_node_type says how one is used, get_custom_node how it is made.",
		Handle:      s.listCustomNodes,
	})

	add(s, srv, Tool[customNodeIn, customNodeOut]{
		Name: "get_custom_node", Title: "Get a custom node", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "A custom node as its form and code make it: its settings, outputs, the items it sends, and its TypeScript.",
		Handle:      s.getCustomNode,
	})

	add(s, srv, Tool[createCustomNodeIn, customNodeOut]{
		Name: "create_custom_node", Title: "Create a custom node", Scope: domain.ScopeBuild,
		Description: "Make a node type of the user's own, custom.<name>, for their workflows only: its form and its TypeScript, " +
			"written against get_code_sdk's declarations as a code node's is. Only for a user who may write code.",
		Handle: s.createCustomNode,
	})

	add(s, srv, Tool[updateCustomNodeIn, customNodeOut]{
		Name: "update_custom_node", Title: "Change a custom node", Scope: domain.ScopeBuild,
		Description: "Replace a custom node's form and code, as get_custom_node gave them and changed, on the version you " +
			"read. Its name stays. A change that breaks workflows using it is refused, saying which and how; with confirm it is made.",
		Handle: s.updateCustomNode,
	})

	add(s, srv, Tool[deleteCustomNodeIn, deletedOut]{
		Name: "delete_custom_node", Title: "Delete a custom node", Scope: domain.ScopeBuild, Destructive: true,
		Description: "Delete a custom node. Workflows that use it are named and it stays, unless confirm; it cannot be undone, " +
			"and works only where the user lets agents delete.",
		Handle: s.deleteCustomNode,
	})
}

func (s *Server) listCustomNodes(ctx context.Context, p auth.Principal, _ struct{}) (customNodeList, error) {
	nodes, err := s.customNodes.List(ctx, p)
	if err != nil {
		return customNodeList{}, err
	}

	out := customNodeList{CustomNodes: make([]customNodeSummary, len(nodes))}
	for i, n := range nodes {
		out.CustomNodes[i] = customNodeSummary{ID: string(n.ID), Type: engine.CustomTypePrefix + n.Name, Label: n.Label, Version: n.Version, UpdatedAt: n.UpdatedAt}
	}

	return out, nil
}

func (s *Server) getCustomNode(ctx context.Context, p auth.Principal, in customNodeIn) (customNodeOut, error) {
	n, err := s.customNodes.Get(ctx, p, domain.CustomNodeID(in.ID))
	if err != nil {
		return customNodeOut{}, err
	}

	return customNodeOf(n), nil
}

func (s *Server) createCustomNode(ctx context.Context, p auth.Principal, in createCustomNodeIn) (customNodeOut, error) {
	def, err := in.definition()
	if err != nil {
		return customNodeOut{}, err
	}

	n, err := s.customNodes.Create(ctx, p, def)
	if err != nil {
		return customNodeOut{}, err
	}

	return customNodeOf(n), nil
}

func (s *Server) updateCustomNode(ctx context.Context, p auth.Principal, in updateCustomNodeIn) (customNodeOut, error) {
	def, err := in.definition()
	if err != nil {
		return customNodeOut{}, err
	}

	n, err := s.customNodes.Update(ctx, p, domain.CustomNodeID(in.ID), in.Version, def, in.Confirm)
	if err != nil {
		return customNodeOut{}, err
	}

	return customNodeOf(n), nil
}

func (s *Server) deleteCustomNode(ctx context.Context, p auth.Principal, in deleteCustomNodeIn) (deletedOut, error) {
	if err := s.customNodes.Delete(ctx, p, domain.CustomNodeID(in.ID), in.Confirm); err != nil {
		return deletedOut{}, err
	}

	return deletedOut{Deleted: in.ID}, nil
}

// definition is the form as the service takes it, its fields read as a tree.
func (d customDefinition) definition() (customnodes.Definition, error) {
	def := customnodes.Definition{
		Name: d.Name, Label: d.Label, Description: d.Description, Outputs: d.Outputs, Source: d.Source,
		Items: domain.CustomItems{Passed: d.Items.Passed},
	}

	for _, st := range d.Settings {
		def.Settings = append(def.Settings, domain.CustomSetting{
			Name: st.Name, Label: st.Label, Description: st.Description, Kind: st.Kind, Required: st.Required, Default: st.Default,
			Options: st.Options, Field: st.Field, FieldKinds: st.FieldKinds,
		})
	}

	data, err := json.Marshal(d.Items.Fields)
	if err != nil {
		return customnodes.Definition{}, err
	}

	var fields []fieldIn
	if err := json.Unmarshal(data, &fields); err != nil {
		return customnodes.Definition{}, invalid(fmt.Sprintf("items.fields: %s", fieldShape))
	}

	def.Items.Fields = customFieldsOf(fields)

	return def, nil
}

// fieldIn is a field as an agent gives it, a tree.
type fieldIn struct {
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	Description string    `json:"description"`
	Optional    bool      `json:"optional"`
	Nullable    bool      `json:"nullable"`
	Options     []string  `json:"options"`
	Of          *fieldIn  `json:"of"`
	Fields      []fieldIn `json:"fields"`
}

func customFieldsOf(in []fieldIn) []domain.CustomField {
	var out []domain.CustomField

	for _, f := range in {
		cf := domain.CustomField{
			Name: f.Name, Kind: f.Kind, Description: f.Description, Optional: f.Optional, Nullable: f.Nullable, Options: f.Options,
			Fields: customFieldsOf(f.Fields),
		}

		if f.Of != nil {
			cf.Of = &customFieldsOf([]fieldIn{*f.Of})[0]
		}

		out = append(out, cf)
	}

	return out
}

func customNodeOf(n domain.CustomNode) customNodeOut {
	out := customNodeOut{
		ID: string(n.ID), Type: engine.CustomTypePrefix + n.Name, Version: n.Version, UpdatedAt: n.UpdatedAt,
		customDefinition: customDefinition{
			Name: n.Name, Label: n.Label, Description: n.Description, Outputs: n.Outputs, Source: n.Source,
			Items: customItems{Passed: n.Items.Passed, Fields: customFieldsOut(n.Items.Fields)},
		},
	}

	for _, st := range n.Settings {
		out.Settings = append(out.Settings, customSetting{
			Name: st.Name, Label: st.Label, Description: st.Description, Kind: st.Kind, Required: st.Required, Default: st.Default,
			Options: st.Options, Field: st.Field, FieldKinds: st.FieldKinds,
		})
	}

	return out
}

func customFieldsOut(fields []domain.CustomField) []any {
	var out []any

	for _, f := range fields {
		field := fieldOut{Name: f.Name, Kind: f.Kind, Description: f.Description, Optional: f.Optional, Nullable: f.Nullable, Options: f.Options}

		if len(f.Fields) > 0 {
			field.Fields = customFieldsOut(f.Fields)
		}

		if f.Of != nil {
			field.Of = customFieldsOut([]domain.CustomField{*f.Of})[0]
		}

		out = append(out, field)
	}

	return out
}
