package httpapi

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/library"
)

// LibraryService is what the handlers need from the library.
type LibraryService interface {
	List(ctx context.Context, p auth.Principal, f ports.PageFilter) (ports.Page[library.Entry], error)
	Get(ctx context.Context, p auth.Principal, id domain.TemplateID) (library.Entry, error)
	Use(ctx context.Context, p auth.Principal, id domain.TemplateID, in library.UseInput) (domain.Workflow, error)
	Share(ctx context.Context, p auth.Principal, workflowID domain.WorkflowID, in library.ShareInput) (library.Entry, error)
	SharedCopy(ctx context.Context, p auth.Principal, workflowID domain.WorkflowID) (library.Entry, error)
	Delete(ctx context.Context, p auth.Principal, id domain.TemplateID) error
}

// inputDTO is a setting a template asks whoever uses it to fill in.
type inputDTO struct {
	NodeID string   `json:"nodeId"`
	Param  paramDTO `json:"param"`
} // @name TemplateInput

type templateDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	// BuiltIn scenarios ship with Polythought; the others are members' shared workflows.
	BuiltIn bool `json:"builtIn"`
	// Mine is set on the workflows you shared.
	Mine      bool       `json:"mine"`
	NodeTypes []string   `json:"nodeTypes"` // the kinds of node it uses, without repeats
	Inputs    []inputDTO `json:"inputs"`
	// Nodes and connections come with one template, not with the list.
	Nodes       []nodeDTO       `json:"nodes,omitempty" validate:"optional"`
	Connections []connectionDTO `json:"connections,omitempty" validate:"optional"`
	// CustomNodes are the custom nodes it brings, code included, as their author had them
	// when sharing: using it copies them. With one template, not with the list.
	CustomNodes []templateCustomNodeDTO `json:"customNodes,omitempty" validate:"optional"`
	UpdatedAt   *time.Time              `json:"updatedAt,omitempty" validate:"optional" format:"date-time"`
} // @name Template

// templateCustomNodeDTO is a custom node a shared workflow brings.
type templateCustomNodeDTO struct {
	customDefinitionDTO

	// Type is what the template's nodes name it by: custom.<name>.
	Type string `json:"type"`
	// NodeType describes it as the node list describes types, for drawing the template.
	NodeType nodeTypeDTO `json:"nodeType"`
} // @name TemplateCustomNode

type templatePage struct {
	Templates []templateDTO `json:"templates"`
	// NextCursor is absent on the last page.
	NextCursor string `json:"nextCursor,omitempty" validate:"optional"`
} // @name TemplatePage

type useTemplateRequest struct {
	// Name of the new workflow; left out, it takes the template's.
	Name string `json:"name" validate:"optional"`
	// Values for the template's inputs, by node ID and then param name.
	Values map[string]map[string]any `json:"values" validate:"optional"`
} // @name UseTemplateRequest

type shareRequest struct {
	Summary     string `json:"summary" validate:"optional" maxLength:"200"`
	Description string `json:"description" validate:"optional" maxLength:"4000"`
} // @name ShareRequest

func toTemplateDTO(e library.Entry, withGraph bool) templateDTO {
	out := templateDTO{
		ID: string(e.ID), Name: e.Name, Summary: e.Summary, Description: e.Description,
		BuiltIn: e.BuiltIn(), Mine: e.Mine, NodeTypes: []string{}, Inputs: make([]inputDTO, len(e.Inputs)),
		UpdatedAt: optionalTime(e.UpdatedAt),
	}

	for _, n := range e.Nodes {
		if !slices.Contains(out.NodeTypes, n.Type) {
			out.NodeTypes = append(out.NodeTypes, n.Type)
		}
	}

	for i, in := range e.Inputs {
		out.Inputs[i] = inputDTO{NodeID: string(in.NodeID), Param: toParamDTO(in.Param)}
	}

	if withGraph {
		out.Nodes = make([]nodeDTO, len(e.Nodes))
		for i, n := range e.Nodes {
			out.Nodes[i] = toNodeDTO(n)
		}

		out.Connections = make([]connectionDTO, len(e.Connections))
		for i, c := range e.Connections {
			out.Connections[i] = connectionDTO{From: string(c.From), FromPort: c.FromPort, To: string(c.To), ToPort: c.ToPort}
		}

		for i, n := range e.CustomNodes {
			out.CustomNodes = append(out.CustomNodes, templateCustomNodeDTO{
				customDefinitionDTO: toCustomNodeDTO(n).customDefinitionDTO,
				Type:                e.CustomTypes[i].Name,
				NodeType:            toNodeTypeDTO(engine.TypeInfo{NodeType: e.CustomTypes[i]}),
			})
		}
	}

	return out
}

func toParamDTO(p engine.ParamSpec) paramDTO {
	dto := paramDTO{
		Name: p.Name, Label: p.Label, Description: p.Description, Kind: string(p.Kind),
		Required: p.Required, Default: p.Default, Options: p.Options, Sensitive: p.Sensitive,
		CredentialTypes: p.CredentialTypes, Field: p.Field, Input: p.Input,
	}

	for _, k := range p.FieldKinds {
		dto.FieldKinds = append(dto.FieldKinds, string(k))
	}

	for _, c := range p.Columns {
		dto.Columns = append(dto.Columns, toParamDTO(c))
	}

	return dto
}

// listTemplates lists the built-in scenarios, on the first page, then members' shared
// workflows, newest first.
//
//	@Summary	List the library
//	@ID			listTemplates
//	@Tags		library
//	@Param		limit	query		int		false	"Page size, of shared workflows"	minimum(1)
//	@Param		cursor	query		string	false	"nextCursor of the previous page"
//	@Success	200		{object}	templatePage
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/library [get]
func (s *Server) listTemplates(ctx context.Context, c *app.RequestContext) {
	f, err := pageFilter(c)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	page, err := s.library.List(ctx, p, f)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := templatePage{Templates: make([]templateDTO, len(page.Items)), NextCursor: page.NextCursor}
	for i, e := range page.Items {
		out.Templates[i] = toTemplateDTO(e, false)
	}

	c.JSON(http.StatusOK, out)
}

// getTemplate returns a template with its nodes and what using it asks for.
//
//	@Summary	Get a template
//	@ID			getTemplate
//	@Tags		library
//	@Param		id	path		string	true	"Template ID"
//	@Success	200	{object}	templateDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/library/{id} [get]
func (s *Server) getTemplate(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	e, err := s.library.Get(ctx, p, domain.TemplateID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toTemplateDTO(e, true))
}

// useTemplate copies a template into a new, inactive workflow of yours.
//
//	@Summary	Start a workflow from a template
//	@ID			useTemplate
//	@Tags		library
//	@Param		id		path		string				true	"Template ID"
//	@Param		body	body		useTemplateRequest	true	"Name and values for the template's inputs"
//	@Success	201		{object}	workflowDTO
//	@Failure	400		{object}	errorBody	"invalid_input, or invalid_workflow with problems, e.g. an input left empty"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/library/{id}/use [post]
func (s *Server) useTemplate(ctx context.Context, c *app.RequestContext) {
	var req useTemplateRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	values := make(map[domain.NodeID]map[string]any, len(req.Values))
	for node, params := range req.Values {
		values[domain.NodeID(node)] = params
	}

	p, _ := principal(c)

	w, err := s.library.Use(ctx, p, domain.TemplateID(c.Param("id")), library.UseInput{Name: req.Name, Values: values})
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusCreated, toWorkflowDTO(w))
}

// deleteTemplate removes a shared workflow from the library: yours, or anyone's if you
// are an admin. Built-in scenarios stay.
//
//	@Summary	Delete a shared workflow
//	@ID			deleteTemplate
//	@Tags		library
//	@Param		id	path	string	true	"Template ID"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/library/{id} [delete]
func (s *Server) deleteTemplate(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.library.Delete(ctx, p, domain.TemplateID(c.Param("id"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// getSharedCopy returns the copy of this workflow you shared to the library.
//
//	@Summary	Get a workflow's shared copy
//	@ID			getSharedCopy
//	@Tags		library
//	@Param		id	path		string	true	"Workflow ID"
//	@Success	200	{object}	templateDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found: no such workflow, or not shared"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id}/share [get]
func (s *Server) getSharedCopy(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	e, err := s.library.SharedCopy(ctx, p, domain.WorkflowID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toTemplateDTO(e, true))
}

// shareWorkflow puts a copy of the saved workflow in the library, or updates the copy
// shared before. Wallets and other personal settings are left out.
//
//	@Summary	Share a workflow
//	@ID			shareWorkflow
//	@Tags		library
//	@Param		id		path		string			true	"Workflow ID"
//	@Param		body	body		shareRequest	true	"How to describe it"
//	@Success	200		{object}	templateDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id}/share [put]
func (s *Server) shareWorkflow(ctx context.Context, c *app.RequestContext) {
	var req shareRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	e, err := s.library.Share(ctx, p, domain.WorkflowID(c.Param("id")), library.ShareInput{Summary: req.Summary, Description: req.Description})
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toTemplateDTO(e, true))
}
