package httpapi

import (
	"context"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/services/meta"
)

// MetaService is what the app and the docs show about Polythought itself.
type MetaService interface {
	Meta(ctx context.Context) meta.Meta
}

type metaDTO struct {
	// Repo is Polythought's repository.
	Repo string `json:"repo"`
	// Stars is how many stars GitHub counts for it, while GitHub says and the lookup is
	// on (D372).
	Stars *int `json:"stars,omitempty" validate:"optional"`
} // @name Meta

// getMeta says where Polythought's repository is and how many stars it has, for every
// page, signed in or not, and the docs.
//
//	@Summary	About Polythought
//	@ID			getMeta
//	@Tags		meta
//	@Success	200	{object}	metaDTO
//	@Router		/meta [get]
func (s *Server) getMeta(ctx context.Context, c *app.RequestContext) {
	out := metaDTO{Repo: meta.RepositoryURL}
	if s.meta != nil {
		out.Stars = s.meta.Meta(ctx).Stars
	}

	c.JSON(http.StatusOK, out)
}
