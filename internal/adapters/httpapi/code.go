package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// CodeTypes are the declarations a user's code is written against, their own node types
// included.
type CodeTypes interface {
	Declarations(ctx context.Context, p auth.Principal) ([]ports.CodeFile, error)
}

// WorkflowCode runs workflow code into the workflow it builds (D74), for those who may
// write code. Code that fails is a *domain.CodeError.
type WorkflowCode interface {
	Build(ctx context.Context, p auth.Principal, source string) (domain.Workflow, error)
}

type codeFileDTO struct {
	// Path is the file's in the @polythought/sdk package, like index.d.ts.
	Path    string `json:"path"`
	Content string `json:"content"`
} // @name CodeFile

type codeTypesDTO struct {
	// Files of @polythought/sdk, for an editor to load as that package: index.d.ts
	// exports the others.
	Files []codeFileDTO `json:"files"`
} // @name CodeTypes

// getCodeTypes returns the TypeScript declarations of @polythought/sdk: the SDK's, and
// the settings and items of every node type the user can place.
//
//	@Summary	Types for code
//	@ID			getCodeTypes
//	@Tags		code
//	@Success	200	{object}	codeTypesDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/code/types [get]
func (s *Server) getCodeTypes(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	files, err := s.codeTypes.Declarations(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := codeTypesDTO{Files: make([]codeFileDTO, len(files))}
	for i, f := range files {
		out.Files[i] = codeFileDTO{Path: f.Path, Content: f.Content}
	}

	c.JSON(http.StatusOK, out)
}

type workflowCodeRequest struct {
	// Source is TypeScript whose default export is a workflow made with
	// @polythought/workflow.
	Source string `json:"source"`
} // @name WorkflowCodeRequest

type workflowCodeDTO struct {
	// Workflow is what the code builds. Code says nothing of positions: every node's is
	// 0, 0, for the editor to place.
	Workflow definitionRequest `json:"workflow"`
} // @name WorkflowCode

// postWorkflowCode runs workflow code, as the editor's Code tab applies it, and returns
// the workflow it builds. Nothing is saved. The code runs in the sandbox, under the code
// limits, and cannot ask for data. Code that does not compile, throws or exports no
// workflow is refused with invalid_code; problems say where, by line and column, when it
// did not compile.
//
//	@Summary	Run workflow code
//	@ID			runWorkflowCode
//	@Tags		code
//	@Param		body	body		workflowCodeRequest	true	"The code"
//	@Success	200		{object}	workflowCodeDTO
//	@Failure	400		{object}	errorBody	"invalid_code"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"code_off: an admin has turned code off for you"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/code/workflow [post]
func (s *Server) postWorkflowCode(ctx context.Context, c *app.RequestContext) {
	var req workflowCodeRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	w, err := s.workflowCode.Build(ctx, p, req.Source)

	var codeErr *domain.CodeError
	if errors.As(err, &codeErr) {
		problems := make([]problemDTO, len(codeErr.Problems))
		for i, cp := range codeErr.Problems {
			problems[i] = problemDTO{Line: cp.Line, Column: cp.Column, Message: cp.Message}
		}

		c.JSON(http.StatusBadRequest, errorBody{Error: errorDetail{Code: "invalid_code", Message: codeErr.Message, Problems: problems}})

		return
	}

	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := workflowCodeDTO{Workflow: definitionRequest{
		Name:        w.Name,
		Nodes:       make([]nodeDTO, len(w.Nodes)),
		Connections: make([]connectionDTO, len(w.Connections)),
	}}

	for i, n := range w.Nodes {
		out.Workflow.Nodes[i] = toNodeDTO(n)
	}

	for i, cn := range w.Connections {
		out.Workflow.Connections[i] = connectionDTO{From: string(cn.From), FromPort: cn.FromPort, To: string(cn.To), ToPort: cn.ToPort}
	}

	c.JSON(http.StatusOK, out)
}
