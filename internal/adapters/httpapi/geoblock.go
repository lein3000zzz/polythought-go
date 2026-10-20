package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/geoblock"
)

// GeoblockService is what the handlers need to show whether Polymarket takes the server's
// orders.
type GeoblockService interface {
	Status(ctx context.Context, actor auth.Principal) (geoblock.Status, error)
	CheckNow(ctx context.Context, actor auth.Principal) (geoblock.Status, error)
}

type geoblockDTO struct {
	// Checked says Polymarket answered at least once; the fields below are its last
	// answer.
	Checked bool `json:"checked"`
	// Blocked says Polymarket refuses orders from the server's IP, or restricts only its
	// website there, which the answer does not tell apart.
	Blocked   bool       `json:"blocked"`
	IP        string     `json:"ip,omitempty" validate:"optional"`
	Country   string     `json:"country,omitempty" validate:"optional"`
	Region    string     `json:"region,omitempty" validate:"optional"`
	CheckedAt *time.Time `json:"checkedAt,omitempty" validate:"optional" format:"date-time"`
	// Error says why the last check failed, when it did; the answer before stays.
	Error string `json:"error,omitempty" validate:"optional"`
} // @name Geoblock

func toGeoblockDTO(st geoblock.Status) geoblockDTO {
	out := geoblockDTO{Error: st.Error}
	if st.Last != nil {
		at := st.Last.CheckedAt
		out.Checked, out.Blocked, out.IP, out.Country, out.Region, out.CheckedAt = true, st.Last.Blocked, st.Last.IP,
			st.Last.Country, st.Last.Region, &at
	}

	return out
}

// geoblockStatus says whether Polymarket takes orders from the server's IP, as it last
// answered (D186).
//
//	@Summary	Polymarket's geoblock for the server
//	@ID			getGeoblock
//	@Tags		admin
//	@Success	200	{object}	geoblockDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/geoblock [get]
func (s *Server) geoblockStatus(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	st, err := s.geoblock.Status(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toGeoblockDTO(st))
}

// checkGeoblock asks Polymarket again whether it takes orders from the server's IP.
//
//	@Summary	Check Polymarket's geoblock now
//	@ID			checkGeoblock
//	@Tags		admin
//	@Success	200	{object}	geoblockDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/geoblock/check [post]
func (s *Server) checkGeoblock(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	st, err := s.geoblock.CheckNow(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toGeoblockDTO(st))
}
