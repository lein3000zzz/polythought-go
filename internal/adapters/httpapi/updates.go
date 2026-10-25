package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/updates"
)

// UpdateService is what the handlers need to say whether a newer Polythought is out
// (D424), and to have the server updated to it (D425).
type UpdateService interface {
	Status(ctx context.Context, actor auth.Principal) (updates.Status, error)
	CheckNow(ctx context.Context, actor auth.Principal) (updates.Status, error)
	Apply(ctx context.Context, actor auth.Principal, version string) error
}

type releaseDTO struct {
	// Version is as it was tagged, without the v, such as 0.1.2.
	Version    string `json:"version"`
	Prerelease bool   `json:"prerelease"`
	// URL is its page on GitHub, with what changed.
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"publishedAt" format:"date-time"`
} // @name Release

type updaterStatusDTO struct {
	State   string `json:"state" enums:"idle,updating,done,failed"`
	Version string `json:"version,omitempty" validate:"optional"` // the release it updates or updated to
	// Error says why the update failed; Log is the end of what it did.
	Error      string     `json:"error,omitempty" validate:"optional"`
	Log        string     `json:"log,omitempty" validate:"optional"`
	StartedAt  *time.Time `json:"startedAt,omitempty" validate:"optional" format:"date-time"`
	FinishedAt *time.Time `json:"finishedAt,omitempty" validate:"optional" format:"date-time"`
} // @name UpdaterStatus

type updatesDTO struct {
	// Current is the server's version, such as 0.1.2, or dev.
	Current string `json:"current"`
	// On says the update check is on; off, the server asks GitHub nothing.
	On          bool `json:"on"`
	Prereleases bool `json:"prereleases"`
	// Latest is the newest release GitHub last gave; Newer says it is newer than the
	// server's, never so for a development build.
	Latest    *releaseDTO `json:"latest,omitempty" validate:"optional"`
	Newer     bool        `json:"newer"`
	CheckedAt *time.Time  `json:"checkedAt,omitempty" validate:"optional" format:"date-time"`
	// Error says why the last check failed, when it did.
	Error string `json:"error,omitempty" validate:"optional"`

	// Updater says an updater is set up, which Update now asks. Update is its latest
	// update, absent where it did not answer, and UpdaterError why.
	Updater      bool              `json:"updater"`
	Update       *updaterStatusDTO `json:"update,omitempty" validate:"optional"`
	UpdaterError string            `json:"updaterError,omitempty" validate:"optional"`
} // @name Updates

type applyUpdateRequest struct {
	// Version is the newest release, which the check found.
	Version string `json:"version"`
} // @name ApplyUpdateRequest

func toUpdatesDTO(st updates.Status) updatesDTO {
	out := updatesDTO{
		Current: st.Current, On: st.On, Prereleases: st.Prereleases, Newer: st.Newer, Error: st.Error,
		Updater: st.Updater, UpdaterError: st.UpdaterError,
	}

	if st.Latest != nil {
		out.Latest = &releaseDTO{
			Version: st.Latest.Version, Prerelease: st.Latest.Prerelease, URL: st.Latest.URL, PublishedAt: st.Latest.PublishedAt,
		}
	}

	if !st.At.IsZero() {
		at := st.At
		out.CheckedAt = &at
	}

	if u := st.Update; u != nil {
		out.Update = &updaterStatusDTO{State: string(u.State), Version: u.Version, Error: u.Error, Log: u.Log}
		if !u.StartedAt.IsZero() {
			at := u.StartedAt
			out.Update.StartedAt = &at
		}

		if !u.FinishedAt.IsZero() {
			at := u.FinishedAt
			out.Update.FinishedAt = &at
		}
	}

	return out
}

func (s *Server) hasUpdates(ctx context.Context, c *app.RequestContext) {
	if s.updates == nil {
		s.fail(ctx, c, domain.ErrNotFound)
		c.Abort()

		return
	}

	c.Next(ctx)
}

// updatesStatus says whether a newer Polythought is out, as GitHub last answered (D424),
// and how the updater is, where one is set up (D425).
//
//	@Summary	Updates
//	@ID			getUpdates
//	@Tags		admin
//	@Success	200	{object}	updatesDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/updates [get]
func (s *Server) updatesStatus(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	st, err := s.updates.Status(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toUpdatesDTO(st))
}

// checkUpdates asks GitHub again for Polythought's newest release, while the check is on.
//
//	@Summary	Check for updates now
//	@ID			checkUpdates
//	@Tags		admin
//	@Success	200	{object}	updatesDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/updates/check [post]
func (s *Server) checkUpdates(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	st, err := s.updates.CheckNow(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toUpdatesDTO(st))
}

// applyUpdate has the updater update the server to the newest release (D425): it pulls
// the release's images and restarts the server with them, which then updates the
// database's schema. Only from a browser session: no token or agent updates the server.
//
//	@Summary	Update the server
//	@ID			applyUpdate
//	@Tags		admin
//	@Param		body	body	applyUpdateRequest	true	"The newest release"
//	@Success	202
//	@Failure	400	{object}	errorBody	"invalid_input: not the newest release, newer than the server's"
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden: not an admin, or not at a browser"
//	@Failure	409	{object}	errorBody	"no_updater, or updating: an update is under way already"
//	@Failure	503	{object}	errorBody	"upstream_unavailable: the updater did not answer, or refused the token"
//	@Security	SessionCookie
//	@Router		/admin/updates/apply [post]
func (s *Server) applyUpdate(ctx context.Context, c *app.RequestContext) {
	var req applyUpdateRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	if err := s.updates.Apply(ctx, p, req.Version); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusAccepted)
}
