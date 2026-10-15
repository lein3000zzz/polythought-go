package httpapi

import (
	"context"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// CodeTypes are the declarations code is written against.
type CodeTypes interface {
	Declarations(ctx context.Context, p auth.Principal) []ports.CodeFile
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

	files := s.codeTypes.Declarations(ctx, p)

	out := codeTypesDTO{Files: make([]codeFileDTO, len(files))}
	for i, f := range files {
		out.Files[i] = codeFileDTO{Path: f.Path, Content: f.Content}
	}

	c.JSON(http.StatusOK, out)
}
