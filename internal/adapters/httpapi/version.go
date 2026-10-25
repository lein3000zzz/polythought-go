package httpapi

import (
	"context"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// versionDTO is the server's own build (D425).
type versionDTO struct {
	// Version is as its release was tagged, such as 0.1.2; dev for a build no release
	// stamped.
	Version string `json:"version"`
	// Commit is the full hash it was built from; empty where unknown.
	Commit string `json:"commit"`
	// Modified says the checkout had changes not committed.
	Modified bool   `json:"modified"`
	Go       string `json:"go"` // the Go toolchain it was built with
} // @name ServerVersion

// serverVersion is admin only: which build of Polythought the server runs.
//
//	@Summary	The server's version
//	@ID			getServerVersion
//	@Tags		admin
//	@Success	200	{object}	versionDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden: not an admin"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/version [get]
func (s *Server) serverVersion(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)
	if !p.User.IsAdmin() {
		s.fail(ctx, c, domain.ErrForbidden)
		return
	}

	c.JSON(http.StatusOK, versionDTO{Version: s.build.Version, Commit: s.build.Commit, Modified: s.build.Modified, Go: s.build.Go})
}
