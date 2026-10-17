package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
)

// SettingsService is what the handlers need from the settings.
type SettingsService interface {
	List(ctx context.Context, p auth.Principal) (settings.Overview, error)
	Update(ctx context.Context, p auth.Principal, changes map[string]json.RawMessage) error
	History(ctx context.Context, p auth.Principal, f ports.PageFilter) (ports.Page[ports.SettingAudit], error)
	Restart(ctx context.Context, p auth.Principal) error
}

type settingDTO struct {
	Key         string   `json:"key"`
	Group       string   `json:"group"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Kind        string   `json:"kind" enums:"string,int,bool,duration,enum,list"`
	Options     []string `json:"options,omitempty" validate:"optional"` // the choices of an enum
	// Secret values are write-only: value is null and isSet says whether one is saved.
	Secret bool `json:"secret"`
	IsSet  bool `json:"isSet"`
	// Apply says when a change takes effect: at once, or after a restart.
	Apply string `json:"apply" enums:"live,restart"`
	// Value and Default are typed by kind: text, a whole number, true or false, a
	// duration as text like "168h", or a list of text. Both are null for a secret.
	Value   any `json:"value"`
	Default any `json:"default"`
	// Source is where the value comes from. One set by the environment variable Env, or in
	// the config file under Key, cannot be changed here.
	Source string `json:"source" enums:"default,database,file,environment"`
	Env    string `json:"env"`
	// Pending is set on a changed setting that waits for a restart.
	Pending bool `json:"pending"`
} // @name Setting

type settingsDTO struct {
	Settings []settingDTO `json:"settings"`
	// RestartPending is set when a saved change waits for a restart.
	RestartPending bool `json:"restartPending"`
} // @name Settings

type updateSettingsRequest struct {
	// New values by setting key, typed as Setting.value. null resets a setting to its
	// default, as does an empty string for a secret.
	Values map[string]any `json:"values"`
} // @name UpdateSettingsRequest

type settingChangeDTO struct {
	ID  string `json:"id"`
	Key string `json:"key"`
	// The values that applied before and after; null for a secret.
	OldValue any  `json:"oldValue"`
	NewValue any  `json:"newValue"`
	Secret   bool `json:"secret"`
	// ActorEmail is absent once the account that made the change is gone.
	ActorEmail string    `json:"actorEmail,omitempty" validate:"optional"`
	At         time.Time `json:"at" format:"date-time"`
} // @name SettingChange

type settingChangePage struct {
	Changes []settingChangeDTO `json:"changes"`
	// NextCursor is absent on the last page.
	NextCursor string `json:"nextCursor,omitempty" validate:"optional"`
} // @name SettingChangePage

func toSettingsDTO(o settings.Overview) settingsDTO {
	out := settingsDTO{Settings: make([]settingDTO, len(o.Entries)), RestartPending: o.RestartPending}

	for i, e := range o.Entries {
		d := settingDTO{
			Key: e.Key, Group: e.Group, Label: e.Label, Description: e.Description,
			Kind: string(e.Kind), Options: e.Options, Secret: e.Secret, IsSet: e.IsSet,
			Apply: string(e.Apply), Value: json.RawMessage(nil), Default: json.RawMessage(nil),
			Source: string(e.Source), Env: e.EnvName(), Pending: e.Pending,
		}

		if !e.Secret {
			d.Value = e.Encode(e.Value)
			d.Default = e.Encode(e.Default)
		}

		out.Settings[i] = d
	}

	return out
}

// listSettings is admin only.
//
//	@Summary	List the instance's settings
//	@ID			listSettings
//	@Tags		admin
//	@Success	200	{object}	settingsDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/settings [get]
func (s *Server) listSettings(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	o, err := s.settings.List(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toSettingsDTO(o))
}

// updateSettings is admin only. Either every change is saved or, when any is invalid,
// none is, and the error's problems name each bad setting in param.
//
//	@Summary	Change settings
//	@ID			updateSettings
//	@Tags		admin
//	@Param		body	body		updateSettingsRequest	true	"Changes"
//	@Success	200		{object}	settingsDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/settings [patch]
func (s *Server) updateSettings(ctx context.Context, c *app.RequestContext) {
	var req updateSettingsRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	changes := make(map[string]json.RawMessage, len(req.Values))

	for key, v := range req.Values {
		raw, err := json.Marshal(v) // numbers stay json.Number, so they come back as sent
		if err != nil {
			s.fail(ctx, c, err)
			return
		}

		changes[key] = raw
	}

	p, _ := principal(c)

	if err := s.settings.Update(ctx, p, changes); err != nil {
		s.fail(ctx, c, err)
		return
	}

	o, err := s.settings.List(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toSettingsDTO(o))
}

// listSettingChanges is admin only.
//
//	@Summary	List changes to settings, newest first
//	@ID			listSettingChanges
//	@Tags		admin
//	@Param		limit	query		int		false	"Page size"	minimum(1)
//	@Param		cursor	query		string	false	"nextCursor of the previous page"
//	@Success	200		{object}	settingChangePage
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/settings/history [get]
func (s *Server) listSettingChanges(ctx context.Context, c *app.RequestContext) {
	f, err := pageFilter(c)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	page, err := s.settings.History(ctx, p, f)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := settingChangePage{Changes: make([]settingChangeDTO, len(page.Items)), NextCursor: page.NextCursor}
	for i, a := range page.Items {
		out.Changes[i] = settingChangeDTO{
			ID: a.ID, Key: a.Key, OldValue: a.OldValue, NewValue: a.NewValue,
			Secret: a.Secret, ActorEmail: a.ActorEmail, At: a.At,
		}
	}

	c.JSON(http.StatusOK, out)
}

// restartServer is admin only. The server answers, then shuts down gracefully and
// starts again, which applies the changes waiting for a restart. Poll /healthz to see it
// back.
//
//	@Summary	Restart the server
//	@ID			restartServer
//	@Tags		admin
//	@Success	202
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/restart [post]
func (s *Server) restartServer(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.settings.Restart(ctx, p); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusAccepted)
}
