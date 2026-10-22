package httpapi

import (
	"context"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// codeRunnersDTO is where user code runs (D352), and how its runner processes are.
type codeRunnersDTO struct {
	// InRunners says code runs in runner processes; false, in the server's.
	InRunners bool `json:"inRunners"`
	Size      int  `json:"size"`    // runner processes
	Running   int  `json:"running"` // of them, now: one that stopped starts with the next run
	Sandboxed bool `json:"sandboxed"`
	// Why says why they are not sandboxed, when they are not.
	Why      string `json:"why"`
	Restarts int    `json:"restarts"` // runners started again since the server started
	// LastError says why one last stopped; Failed why they would not start, every code
	// run failing (D358).
	LastError string `json:"lastError"`
	Failed    string `json:"failed"`
} // @name CodeRunners

// codeRunners is admin only.
//
//	@Summary	Where user code runs, and how its runners are
//	@ID			getCodeRunners
//	@Tags		admin
//	@Success	200	{object}	codeRunnersDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden: not an admin"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/code-runners [get]
func (s *Server) codeRunners(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)
	if !p.User.IsAdmin() {
		s.fail(ctx, c, domain.ErrForbidden)
		return
	}

	var st ports.CodeRunners
	if s.codeRunnersStatus != nil {
		st = s.codeRunnersStatus()
	}

	c.JSON(http.StatusOK, codeRunnersDTO{
		InRunners: st.InRunners, Size: st.Size, Running: st.Running, Sandboxed: st.Sandboxed, Why: st.Why,
		Restarts: st.Restarts, LastError: st.LastError, Failed: st.Failed,
	})
}
