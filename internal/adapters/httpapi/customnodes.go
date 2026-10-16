package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/customnodes"
)

// CustomNodeService is what the handlers need from the custom node service.
type CustomNodeService interface {
	List(ctx context.Context, p auth.Principal) ([]domain.CustomNode, error)
	Get(ctx context.Context, p auth.Principal, id domain.CustomNodeID) (domain.CustomNode, error)
	Create(ctx context.Context, p auth.Principal, def customnodes.Definition) (domain.CustomNode, error)
	Update(ctx context.Context, p auth.Principal, id domain.CustomNodeID, version int, def customnodes.Definition, confirm bool) (domain.CustomNode, error)
	Delete(ctx context.Context, p auth.Principal, id domain.CustomNodeID, confirm bool) error
}

// customSettingDTO is a setting of a custom node, as a node type's setting is described,
// in the kinds custom nodes take: no credentials, so code never sees a secret.
type customSettingDTO struct {
	// Name is what code reads it as: ctx.params.<name>.
	Name        string `json:"name"`
	Label       string `json:"label,omitempty" validate:"optional"`
	Description string `json:"description,omitempty" validate:"optional"`
	Kind        string `json:"kind" enums:"string,number,decimal,bool,duration,list,wallet"`
	Required    bool   `json:"required,omitempty" validate:"optional"`
	Default     any    `json:"default,omitempty" validate:"optional"`
	// Options are the choices of a string, or of each element of a list.
	Options []string `json:"options,omitempty" validate:"optional"`
	// Field marks a string, or each element of a list, as a path to a field of the items
	// the node receives; FieldKinds, when present, are the kinds that field may be.
	Field      bool     `json:"field,omitempty" validate:"optional"`
	FieldKinds []string `json:"fieldKinds,omitempty" validate:"optional" enums:"text,number,decimal,bool,time,list,object,any"`
} // @name CustomSetting

// customItemsDTO is what a custom node sends on: the items it got (passed), or items of
// its own with these fields, checked when it runs.
type customItemsDTO struct {
	Passed bool       `json:"passed"`
	Fields []fieldDTO `json:"fields"`
} // @name CustomItems

// customDefinitionDTO is what a custom node's form sets.
type customDefinitionDTO struct {
	// Name makes its type, custom.<name>: lowercase letters, digits and hyphens, starting
	// with a letter, up to 40. It cannot change.
	Name        string             `json:"name"`
	Label       string             `json:"label"`
	Description string             `json:"description,omitempty" validate:"optional"`
	Settings    []customSettingDTO `json:"settings"`
	// Outputs are its output ports, in order; it has one input, main.
	Outputs []string       `json:"outputs"`
	Items   customItemsDTO `json:"items"`
	// Source is TypeScript exporting run(items, ctx), which gets every item at once and
	// returns the items, or with several outputs, { port: items }.
	Source string `json:"source"`
} // @name CustomNodeDefinition

type customNodeDTO struct {
	customDefinitionDTO

	ID string `json:"id"`
	// Type is what workflows name it by: custom.<name>.
	Type      string    `json:"type"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"createdAt" format:"date-time"`
	UpdatedAt time.Time `json:"updatedAt" format:"date-time"`
} // @name CustomNode

type customNodeList struct {
	CustomNodes []customNodeDTO `json:"customNodes"`
} // @name CustomNodeList

type updateCustomNodeRequest struct {
	customDefinitionDTO

	// Version is the version the edit started from; a newer one on the server is a conflict.
	Version int `json:"version"`
	// Confirm makes a change that breaks workflows using the node, which is otherwise
	// refused with breaks_workflows saying which.
	Confirm bool `json:"confirm,omitempty" validate:"optional"`
} // @name UpdateCustomNodeRequest

// brokenWorkflowDTO is a workflow a change to a custom node would break, with what saving
// it would then refuse.
type brokenWorkflowDTO struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Problems []problemDTO `json:"problems"`
} // @name BrokenWorkflow

func toBrokenWorkflowDTOs(err error) []brokenWorkflowDTO {
	var breaks *domain.BreaksError
	if !errors.As(err, &breaks) {
		return nil
	}

	out := make([]brokenWorkflowDTO, len(breaks.Workflows))

	for i, w := range breaks.Workflows {
		out[i] = brokenWorkflowDTO{ID: string(w.ID), Name: w.Name, Problems: toProblemDTOs(&domain.ValidationError{Problems: w.Problems})}
	}

	return out
}

func (d customDefinitionDTO) definition() customnodes.Definition {
	def := customnodes.Definition{
		Name: d.Name, Label: d.Label, Description: d.Description,
		Settings: make([]domain.CustomSetting, len(d.Settings)), Outputs: d.Outputs,
		Items:  domain.CustomItems{Passed: d.Items.Passed, Fields: customFields(d.Items.Fields)},
		Source: d.Source,
	}

	for i, s := range d.Settings {
		def.Settings[i] = domain.CustomSetting{
			Name: s.Name, Label: s.Label, Description: s.Description, Kind: s.Kind, Required: s.Required,
			Default: s.Default, Options: s.Options, Field: s.Field, FieldKinds: s.FieldKinds,
		}
	}

	return def
}

func customFields(fields []fieldDTO) []domain.CustomField {
	out := make([]domain.CustomField, len(fields))

	for i, f := range fields {
		out[i] = domain.CustomField{
			Name: f.Name, Description: f.Description, Kind: f.Kind, Optional: f.Optional, Nullable: f.Nullable,
			Options: f.Options, Fields: customFields(f.Fields),
		}

		if f.Of != nil {
			of := customFields([]fieldDTO{*f.Of})[0]
			out[i].Of = &of
		}
	}

	return out
}

func toCustomNodeDTO(n domain.CustomNode) customNodeDTO {
	d := customNodeDTO{
		customDefinitionDTO: customDefinitionDTO{
			Name: n.Name, Label: n.Label, Description: n.Description,
			Settings: make([]customSettingDTO, len(n.Settings)), Outputs: nonNil(n.Outputs),
			Items:  customItemsDTO{Passed: n.Items.Passed, Fields: toCustomFieldDTOs(n.Items.Fields)},
			Source: n.Source,
		},
		ID: string(n.ID), Type: engine.CustomTypePrefix + n.Name, Version: n.Version,
		CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
	}

	for i, s := range n.Settings {
		d.Settings[i] = customSettingDTO{
			Name: s.Name, Label: s.Label, Description: s.Description, Kind: s.Kind, Required: s.Required,
			Default: s.Default, Options: s.Options, Field: s.Field, FieldKinds: s.FieldKinds,
		}
	}

	return d
}

func toCustomFieldDTOs(fields []domain.CustomField) []fieldDTO {
	out := make([]fieldDTO, len(fields))

	for i, f := range fields {
		out[i] = fieldDTO{
			Name: f.Name, Kind: f.Kind, Description: f.Description,
			Optional: f.Optional, Nullable: f.Nullable, Options: f.Options,
		}

		if f.Of != nil {
			of := toCustomFieldDTOs([]domain.CustomField{*f.Of})[0]
			out[i].Of = &of
		}

		if len(f.Fields) > 0 {
			out[i].Fields = toCustomFieldDTOs(f.Fields)
		}
	}

	return out
}

// listCustomNodes lists your custom nodes by name.
//
//	@Summary	List your custom nodes
//	@ID			listCustomNodes
//	@Tags		custom-nodes
//	@Success	200	{object}	customNodeList
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/custom-nodes [get]
func (s *Server) listCustomNodes(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	list, err := s.customNodes.List(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := customNodeList{CustomNodes: make([]customNodeDTO, len(list))}
	for i, n := range list {
		out.CustomNodes[i] = toCustomNodeDTO(n)
	}

	c.JSON(http.StatusOK, out)
}

// getCustomNode returns one of your custom nodes.
//
//	@Summary	Get a custom node
//	@ID			getCustomNode
//	@Tags		custom-nodes
//	@Param		id	path		string	true	"Custom node ID"
//	@Success	200	{object}	customNodeDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/custom-nodes/{id} [get]
func (s *Server) getCustomNode(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	n, err := s.customNodes.Get(ctx, p, domain.CustomNodeID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toCustomNodeDTO(n))
}

// createCustomNode defines a node type of your own, custom.<name>, which only your
// workflows see. Problems name the part of the form they are about in param: name,
// settings.0.default, outputs.1, items, source.
//
//	@Summary	Create a custom node
//	@ID			createCustomNode
//	@Tags		custom-nodes
//	@Param		body	body		customDefinitionDTO	true	"Definition"
//	@Success	201		{object}	customNodeDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"code_off: an admin has turned code off for you"
//	@Failure	409		{object}	errorBody	"custom_node_taken"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/custom-nodes [post]
func (s *Server) createCustomNode(ctx context.Context, c *app.RequestContext) {
	var req customDefinitionDTO

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	n, err := s.customNodes.Create(ctx, p, req.definition())
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusCreated, toCustomNodeDTO(n))
}

// updateCustomNode replaces a custom node's definition; its name stays. Workflows use the
// latest version. A change that breaks some, by taking away a port or setting they use,
// is refused with breaks_workflows saying which and how; with confirm it is made, and they
// fail when they run until they are edited.
//
//	@Summary	Change a custom node
//	@ID			updateCustomNode
//	@Tags		custom-nodes
//	@Param		id		path		string					true	"Custom node ID"
//	@Param		body	body		updateCustomNodeRequest	true	"Definition"
//	@Success	200		{object}	customNodeDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"code_off"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Failure	409		{object}	errorBody	"conflict, or breaks_workflows with the workflows"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/custom-nodes/{id} [put]
func (s *Server) updateCustomNode(ctx context.Context, c *app.RequestContext) {
	var req updateCustomNodeRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	n, err := s.customNodes.Update(ctx, p, domain.CustomNodeID(c.Param("id")), req.Version, req.definition(), req.Confirm)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toCustomNodeDTO(n))
}

// deleteCustomNode removes a custom node. If workflows use it, it is refused with
// breaks_workflows naming them; with confirm=true it goes, and they fail when they run
// until they are edited.
//
//	@Summary	Delete a custom node
//	@ID			deleteCustomNode
//	@Tags		custom-nodes
//	@Param		id		path	string	true	"Custom node ID"
//	@Param		confirm	query	bool	false	"Delete it even if workflows use it"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Failure	409	{object}	errorBody	"breaks_workflows with the workflows"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/custom-nodes/{id} [delete]
func (s *Server) deleteCustomNode(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.customNodes.Delete(ctx, p, domain.CustomNodeID(c.Param("id")), c.Query("confirm") == "true"); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
