package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/plugins"
)

// PluginService is what the handlers need from the plugins service (5c).
type PluginService interface {
	List(ctx context.Context, p auth.Principal) ([]plugins.Info, []ports.PluginFound, error)
	Install(ctx context.Context, p auth.Principal, archive io.Reader) (plugins.Installed, error)
	InstallFrom(ctx context.Context, p auth.Principal, url string) (plugins.Installed, error)
	Adopt(ctx context.Context, p auth.Principal, id, version string) (plugins.Installed, error)
	Approve(ctx context.Context, p auth.Principal, id, version string, permissions []string) error
	RemoveVersion(ctx context.Context, p auth.Principal, id, version string) error
	SetEnabled(ctx context.Context, p auth.Principal, id string, enabled bool) error
	SetUsers(ctx context.Context, p auth.Principal, id string, everyone bool, users []domain.UserID) error
	Start(ctx context.Context, p auth.Principal, id string) error
	Uninstall(ctx context.Context, p auth.Principal, id string, removeData bool) error
	History(ctx context.Context, p auth.Principal, id string) ([]domain.PluginAudit, error)
	Settings(ctx context.Context, p auth.Principal, id string) ([]plugins.Setting, error)
	SetSettings(ctx context.Context, p auth.Principal, id string, changes map[string]json.RawMessage) error
	SetCaps(ctx context.Context, p auth.Principal, id string, caps domain.PluginCaps) error
	Sandbox(p auth.Principal) (ports.PluginSandbox, error)
	LeftBehind(ctx context.Context, p auth.Principal) ([]plugins.LeftData, error)
	RemoveLeftBehind(ctx context.Context, p auth.Principal, id string) error
}

// archiveRoute is where admins upload archives: its body streams, past the size other
// requests' bodies are held to (limitBody), to the plugins directory's own cap.
const archiveRoute = "/api/v1/admin/plugins"

type pluginsDTO struct {
	Plugins []pluginDTO `json:"plugins"`
	// Found are versions on disk no admin installed, to adopt (D321).
	Found   []foundPluginDTO `json:"found"`
	Sandbox pluginSandboxDTO `json:"sandbox"`
} // @name Plugins

// pluginSandboxDTO is whether this server runs plugins under the sandbox (D338).
type pluginSandboxDTO struct {
	Available bool   `json:"available"`
	Why       string `json:"why"` // why not, when it is not
	// Unsandboxed says admins let plugins start without it where it is not: the Config
	// page's setting.
	Unsandboxed bool `json:"unsandboxed"`
} // @name PluginSandbox

// pluginCapsDTO are a plugin's own caps (D346), in MiB: 0 for the Config page's.
type pluginCapsDTO struct {
	DataCap   int `json:"dataCap" minimum:"0" maximum:"1048576"`
	MemoryCap int `json:"memoryCap" minimum:"0" maximum:"65536"`
} // @name PluginCaps

// pluginDataDTO is what a plugin's data takes, in bytes (D342).
type pluginDataDTO struct {
	Files  int64 `json:"files"`  // its directory's
	Stored int64 `json:"stored"` // what its nodes stored in workflows
} // @name PluginData

// leftPluginDataDTO is what a plugin no longer installed left behind (D342), in bytes.
type leftPluginDataDTO struct {
	ID     string `json:"id"`
	Files  int64  `json:"files"`  // its directory's
	Stored int64  `json:"stored"` // what its nodes stored in workflows
} // @name LeftPluginData

type pluginDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"` // its newest version's
	// Version is the approved one, which runs when enabled; empty until the first approval.
	Version  string        `json:"version"`
	Enabled  bool          `json:"enabled"`
	Everyone bool          `json:"everyone"` // false: only users may use it (D282)
	Users    []string      `json:"users"`
	Caps     pluginCapsDTO `json:"caps"`
	// Data is what its data takes; one no version of which was approved yet has what its
	// ID left behind as it was uninstalled (D366).
	Data     pluginDataDTO      `json:"data"`
	Versions []pluginVersionDTO `json:"versions"` // newest first
	// Status is how it runs, as it was loaded at start; absent when it was not.
	Status *pluginStatusDTO `json:"status,omitempty" validate:"optional"`
	// Restart says what runs is not what admins decided: a restart applies it (D317).
	Restart bool `json:"restart"`
} // @name Plugin

type pluginVersionDTO struct {
	Version     string   `json:"version"`
	Permissions []string `json:"permissions"`
	// Manifest is the version's manifest.json: its nodes, triggers, credential types,
	// scenarios and settings.
	Manifest map[string]any `json:"manifest"`
	// Signer is the trusted key's name that signed it; empty when none an admin trusts
	// did, keyId saying which key did, if any.
	Signer      string     `json:"signer"`
	KeyID       string     `json:"keyId"`
	InstalledBy string     `json:"installedBy"` // empty once the account is gone
	InstalledAt time.Time  `json:"installedAt" format:"date-time"`
	Approved    bool       `json:"approved"`
	ApprovedAt  *time.Time `json:"approvedAt,omitempty" format:"date-time" validate:"optional"`
} // @name PluginVersion

type pluginStatusDTO struct {
	Version   string    `json:"version"`
	State     string    `json:"state" enums:"running,restarting,crashed,stopped,failed"`
	Since     time.Time `json:"since" format:"date-time"`
	Crashes   int       `json:"crashes"` // in the last 10 minutes
	LastError string    `json:"lastError"`
	Log       []string  `json:"log"` // its last lines
	// Sandboxed says its process runs under the sandbox. Memory and data are what it took
	// when last measured, in bytes, memory 0 where it is not watched; the caps are those
	// it is held to, in bytes.
	Sandboxed bool  `json:"sandboxed"`
	Memory    int64 `json:"memory"`
	MemoryCap int64 `json:"memoryCap"`
	Data      int64 `json:"data"`
	DataCap   int64 `json:"dataCap"`
} // @name PluginStatus

type foundPluginDTO struct {
	ID      string `json:"id"`
	Version string `json:"version"`
} // @name FoundPlugin

type installedPluginDTO struct {
	ID          string   `json:"id"`
	Version     string   `json:"version"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
	// NewPermissions are those it asks for that what runs was not approved for: all of
	// them for a plugin new here (D318).
	NewPermissions []string `json:"newPermissions"`
	Upgrade        bool     `json:"upgrade"`
	Signed         bool     `json:"signed"`
	Signer         string   `json:"signer"`
	KeyID          string   `json:"keyId"`
	// Warning is for the admin who approves it, when no key they trust signed it (D280).
	Warning string `json:"warning"`
} // @name InstalledPlugin

type fetchPluginRequest struct {
	URL string `json:"url"` // https
} // @name FetchPluginRequest

type approvePluginRequest struct {
	// Permissions are those the version asks for, which the admin approves: exactly
	// those, or the approval is refused.
	Permissions []string `json:"permissions"`
} // @name ApprovePluginRequest

type pluginUsersRequest struct {
	Everyone bool     `json:"everyone"`
	Users    []string `json:"users"` // who may use it when not everyone
} // @name PluginUsersRequest

type pluginAuditDTO struct {
	Version string    `json:"version"`
	Action  string    `json:"action"`
	Detail  string    `json:"detail"`
	By      string    `json:"by"` // the admin's ID; empty for the server's own doing
	At      time.Time `json:"at" format:"date-time"`
} // @name PluginAudit

// hasPlugins answers not found where there are no plugins.
func (s *Server) hasPlugins(ctx context.Context, c *app.RequestContext) {
	if s.plugins == nil {
		s.fail(ctx, c, domain.ErrNotFound)
		c.Abort()

		return
	}

	c.Next(ctx)
}

func toPluginDTO(i plugins.Info) pluginDTO {
	out := pluginDTO{
		ID: i.ID, Version: i.Version, Enabled: i.Enabled, Everyone: i.Everyone, Users: []string{},
		Caps:     pluginCapsDTO{DataCap: i.Caps.DataMiB, MemoryCap: i.Caps.MemoryMiB},
		Data:     pluginDataDTO{Files: i.Data.Files, Stored: i.Data.Stored},
		Versions: make([]pluginVersionDTO, len(i.Versions)), Restart: i.Restart,
	}

	for _, u := range i.Users {
		out.Users = append(out.Users, string(u))
	}

	for k, v := range i.Versions {
		var m struct {
			Name        string   `json:"name"`
			Permissions []string `json:"permissions"`
		}

		_ = json.Unmarshal(v.Manifest, &m)

		if k == 0 {
			out.Name = m.Name
		}

		dto := pluginVersionDTO{
			Version: v.Version, Permissions: orNone(m.Permissions), Manifest: itemJSON(v.Manifest), Signer: v.Signer, KeyID: v.KeyID,
			InstalledBy: string(v.InstalledBy), InstalledAt: v.InstalledAt, Approved: v.Approved(),
		}

		if v.Approved() {
			dto.ApprovedAt = &v.ApprovedAt
		}

		out.Versions[k] = dto
	}

	if st := i.Status; st != nil {
		out.Status = &pluginStatusDTO{
			Version: st.Version, State: st.State, Since: st.Since, Crashes: st.Crashes, LastError: st.LastError,
			Log: orNone(st.Log), Sandboxed: st.Sandboxed, Memory: st.Memory, MemoryCap: st.MemoryCap, Data: st.Data, DataCap: st.DataCap,
		}
	}

	return out
}

func toInstalledDTO(in plugins.Installed) installedPluginDTO {
	f := in.File

	return installedPluginDTO{
		ID: f.ID, Version: f.Version, Name: f.Name, Permissions: orNone(f.Permissions), NewPermissions: orNone(in.NewPermissions),
		Upgrade: in.Upgrade, Signed: f.Signed, Signer: f.Signer, KeyID: f.KeyID, Warning: f.Warning,
	}
}

func orNone(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}

// listPlugins is admin only.
//
//	@Summary	List the plugins installed, and versions found on disk
//	@ID			listPlugins
//	@Tags		admin
//	@Success	200	{object}	pluginsDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/plugins [get]
func (s *Server) listPlugins(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	list, found, err := s.plugins.List(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	sb, err := s.plugins.Sandbox(p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := pluginsDTO{
		Plugins: make([]pluginDTO, len(list)), Found: make([]foundPluginDTO, len(found)),
		Sandbox: pluginSandboxDTO{Available: sb.Available, Why: sb.Why, Unsandboxed: sb.Unsandboxed},
	}
	for i, info := range list {
		out.Plugins[i] = toPluginDTO(info)
	}

	for i, f := range found {
		out.Found[i] = foundPluginDTO{ID: f.ID, Version: f.Version}
	}

	c.JSON(http.StatusOK, out)
}

// installPlugin is admin only. The body is the archive itself, a gzipped tar, up to the
// size the Config page allows; it is refused whole when anything is wrong with it. It
// runs once approved, from the next start.
//
//	@Summary	Install a plugin from an archive
//	@ID			installPlugin
//	@Tags		admin
//	@Accept		application/gzip
//	@Param		body	body		string	true	"The archive"	format(binary)
//	@Success	201		{object}	installedPluginDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"forbidden"
//	@Failure	409		{object}	errorBody	"plugin_installed"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/plugins [post]
func (s *Server) installPlugin(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	body := c.RequestBodyStream()
	if body == nil || !c.Request.IsBodyStream() {
		body = bytes.NewReader(c.Request.Body())
	}

	in, err := s.plugins.Install(ctx, p, body)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusCreated, toInstalledDTO(in))
}

// fetchPlugin is admin only: the archive at an https URL, through the guard that keeps
// out the server's own network.
//
//	@Summary	Install a plugin from a URL
//	@ID			fetchPlugin
//	@Tags		admin
//	@Param		body	body		fetchPluginRequest	true	"Where"
//	@Success	201		{object}	installedPluginDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"forbidden"
//	@Failure	409		{object}	errorBody	"plugin_installed"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/plugins/fetch [post]
func (s *Server) fetchPlugin(ctx context.Context, c *app.RequestContext) {
	var req fetchPluginRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	in, err := s.plugins.InstallFrom(ctx, p, req.URL)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusCreated, toInstalledDTO(in))
}

// adoptPlugin is admin only: a version found on disk, checked as an archive would be.
//
//	@Summary	Install a version found on disk
//	@ID			adoptPlugin
//	@Tags		admin
//	@Param		id		path		string	true	"Plugin ID"
//	@Param		version	path		string	true	"Version"
//	@Success	201		{object}	installedPluginDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"forbidden"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Failure	409		{object}	errorBody	"plugin_installed"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/plugins/found/{id}/{version} [post]
func (s *Server) adoptPlugin(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	in, err := s.plugins.Adopt(ctx, p, c.Param("id"), c.Param("version"))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusCreated, toInstalledDTO(in))
}

// approvePlugin is admin only. It names the permissions approved, which must be those
// the version asks for. The version runs from the next start.
//
//	@Summary	Approve a version of a plugin
//	@ID			approvePlugin
//	@Tags		admin
//	@Param		id		path	string					true	"Plugin ID"
//	@Param		version	path	string					true	"Version"
//	@Param		body	body	approvePluginRequest	true	"What is approved"
//	@Success	204
//	@Failure	400	{object}	errorBody	"invalid_input"
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/plugins/{id}/versions/{version}/approve [post]
func (s *Server) approvePlugin(ctx context.Context, c *app.RequestContext) {
	var req approvePluginRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	if err := s.plugins.Approve(ctx, p, c.Param("id"), c.Param("version"), req.Permissions); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.SetStatusCode(http.StatusNoContent)
}

// removePluginVersion is admin only: a version not approved, an upgrade not wanted.
//
//	@Summary	Remove a version of a plugin
//	@ID			removePluginVersion
//	@Tags		admin
//	@Param		id		path	string	true	"Plugin ID"
//	@Param		version	path	string	true	"Version"
//	@Success	204
//	@Failure	400	{object}	errorBody	"invalid_input"
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/plugins/{id}/versions/{version} [delete]
func (s *Server) removePluginVersion(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.plugins.RemoveVersion(ctx, p, c.Param("id"), c.Param("version")); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.SetStatusCode(http.StatusNoContent)
}

// enablePlugin is admin only: from the next start.
//
//	@Summary	Enable a plugin
//	@ID			enablePlugin
//	@Tags		admin
//	@Param		id	path	string	true	"Plugin ID"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/plugins/{id}/enable [post]
func (s *Server) enablePlugin(ctx context.Context, c *app.RequestContext) {
	s.setPluginEnabled(ctx, c, true)
}

// disablePlugin is admin only: from the next start, when the active workflows using it
// are paused and their owners told (D319).
//
//	@Summary	Disable a plugin
//	@ID			disablePlugin
//	@Tags		admin
//	@Param		id	path	string	true	"Plugin ID"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/plugins/{id}/disable [post]
func (s *Server) disablePlugin(ctx context.Context, c *app.RequestContext) {
	s.setPluginEnabled(ctx, c, false)
}

func (s *Server) setPluginEnabled(ctx context.Context, c *app.RequestContext, enabled bool) {
	p, _ := principal(c)

	if err := s.plugins.SetEnabled(ctx, p, c.Param("id"), enabled); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.SetStatusCode(http.StatusNoContent)
}

// setPluginUsers is admin only, and applies at once: the active workflows of owners who
// may no longer use it are paused, and they are told (D319).
//
//	@Summary	Say who may use a plugin
//	@ID			setPluginUsers
//	@Tags		admin
//	@Param		id		path	string				true	"Plugin ID"
//	@Param		body	body	pluginUsersRequest	true	"Who"
//	@Success	204
//	@Failure	400	{object}	errorBody	"invalid_input"
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/plugins/{id}/users [put]
func (s *Server) setPluginUsers(ctx context.Context, c *app.RequestContext) {
	var req pluginUsersRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	users := make([]domain.UserID, len(req.Users))
	for i, u := range req.Users {
		users[i] = domain.UserID(u)
	}

	p, _ := principal(c)

	if err := s.plugins.SetUsers(ctx, p, c.Param("id"), req.Everyone, users); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.SetStatusCode(http.StatusNoContent)
}

// startPlugin is admin only: one stopped as crashed (D320).
//
//	@Summary	Start a crashed plugin again
//	@ID			startPlugin
//	@Tags		admin
//	@Param		id	path	string	true	"Plugin ID"
//	@Success	204
//	@Failure	400	{object}	errorBody	"invalid_input"
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/plugins/{id}/start [post]
func (s *Server) startPlugin(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.plugins.Start(ctx, p, c.Param("id")); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.SetStatusCode(http.StatusNoContent)
}

// uninstallPlugin is admin only. It runs until the next start, when the active workflows
// using it are paused (D319); its history is kept. Its data, its directory and what its
// nodes stored, goes too when data is true (D348), and is otherwise left behind, for a
// reinstall to find (D342).
//
//	@Summary	Uninstall a plugin
//	@ID			uninstallPlugin
//	@Tags		admin
//	@Param		id		path	string	true	"Plugin ID"
//	@Param		data	query	bool	false	"Remove its data too"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/plugins/{id} [delete]
func (s *Server) uninstallPlugin(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.plugins.Uninstall(ctx, p, c.Param("id"), c.Query("data") == "true"); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.SetStatusCode(http.StatusNoContent)
}

// setPluginCaps is admin only: the plugin's own caps (D346), 0 for the Config page's. They
// apply as it is next measured; its memory's soft limit from its next start.
//
//	@Summary	Set a plugin's own caps
//	@ID			setPluginCaps
//	@Tags		admin
//	@Param		id		path	string			true	"Plugin ID"
//	@Param		body	body	pluginCapsDTO	true	"Its caps, in MiB"
//	@Success	204
//	@Failure	400	{object}	errorBody	"invalid_input"
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/plugins/{id}/caps [put]
func (s *Server) setPluginCaps(ctx context.Context, c *app.RequestContext) {
	var req pluginCapsDTO

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	if err := s.plugins.SetCaps(ctx, p, c.Param("id"), domain.PluginCaps{DataMiB: req.DataCap, MemoryMiB: req.MemoryCap}); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.SetStatusCode(http.StatusNoContent)
}

// pluginDataLeft is admin only: what plugins uninstalled without their data left behind
// (D342).
//
//	@Summary	The data plugins left behind
//	@ID			pluginDataLeft
//	@Tags		admin
//	@Success	200	{array}		leftPluginDataDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/plugins/left-behind [get]
func (s *Server) pluginDataLeft(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	left, err := s.plugins.LeftBehind(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := make([]leftPluginDataDTO, len(left))
	for i, l := range left {
		out[i] = leftPluginDataDTO{ID: l.ID, Files: l.Files, Stored: l.Stored}
	}

	c.JSON(http.StatusOK, out)
}

// removePluginDataLeft is admin only: what a plugin no longer installed left behind. An
// installed plugin's is refused: it goes as the plugin is uninstalled.
//
//	@Summary	Remove the data a plugin left behind
//	@ID			removePluginDataLeft
//	@Tags		admin
//	@Param		id	path	string	true	"Plugin ID"
//	@Success	204
//	@Failure	400	{object}	errorBody	"invalid_input"
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/plugins/left-behind/{id} [delete]
func (s *Server) removePluginDataLeft(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.plugins.RemoveLeftBehind(ctx, p, c.Param("id")); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.SetStatusCode(http.StatusNoContent)
}

// pluginHistory is admin only: what admins did with it, newest first, kept after it is
// gone.
//
//	@Summary	What admins did with a plugin
//	@ID			pluginHistory
//	@Tags		admin
//	@Param		id	path		string	true	"Plugin ID"
//	@Success	200	{array}		pluginAuditDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/plugins/{id}/history [get]
func (s *Server) pluginHistory(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	history, err := s.plugins.History(ctx, p, c.Param("id"))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := make([]pluginAuditDTO, len(history))
	for i, a := range history {
		out[i] = pluginAuditDTO{Version: a.Version, Action: a.Action, Detail: a.Detail, By: string(a.By), At: a.At}
	}

	c.JSON(http.StatusOK, out)
}

type pluginSettingDTO struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Kind        string   `json:"kind" enums:"string,int,bool,duration,enum,list"`
	Options     []string `json:"options,omitempty" validate:"optional"` // the choices of an enum
	// Secret values are write-only: value is null and isSet says whether one is saved.
	Secret bool `json:"secret"`
	IsSet  bool `json:"isSet"`
	// Value and Default are typed by kind, as a Setting's are; both null for a secret.
	Value   any `json:"value"`
	Default any `json:"default"`
} // @name PluginSetting

type pluginSettingsDTO struct {
	Settings []pluginSettingDTO `json:"settings"`
} // @name PluginSettings

type updatePluginSettingsRequest struct {
	// New values by setting key, typed as PluginSetting.value. null resets a setting to
	// its default, as does an empty string for a secret.
	Values map[string]any `json:"values"`
} // @name UpdatePluginSettingsRequest

func toPluginSettingsDTO(list []plugins.Setting) pluginSettingsDTO {
	out := pluginSettingsDTO{Settings: make([]pluginSettingDTO, len(list))}

	for i, st := range list {
		d := pluginSettingDTO{
			Key: st.Key, Label: st.Label, Description: st.Description, Kind: string(st.Kind), Options: st.Options,
			Secret: st.Secret, IsSet: st.IsSet, Value: json.RawMessage(nil), Default: json.RawMessage(nil),
		}

		if !st.Secret {
			d.Value = st.Encode(st.Value)
			d.Default = st.Encode(st.Default)
		}

		out.Settings[i] = d
	}

	return out
}

// pluginSettings is admin only: a plugin's own settings, as the version it runs declares
// them (D329).
//
//	@Summary	A plugin's own settings
//	@ID			pluginSettings
//	@Tags		admin
//	@Param		id	path		string	true	"Plugin ID"
//	@Success	200	{object}	pluginSettingsDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/plugins/{id}/settings [get]
func (s *Server) pluginSettings(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	list, err := s.plugins.Settings(ctx, p, c.Param("id"))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toPluginSettingsDTO(list))
}

// updatePluginSettings is admin only. Either every change is saved or, when any is
// invalid, none is, and the error's problems name each bad setting in param. The plugin's
// runs get them from the next one.
//
//	@Summary	Change a plugin's own settings
//	@ID			updatePluginSettings
//	@Tags		admin
//	@Param		id		path		string						true	"Plugin ID"
//	@Param		body	body		updatePluginSettingsRequest	true	"Changes"
//	@Success	200		{object}	pluginSettingsDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"forbidden"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/plugins/{id}/settings [patch]
func (s *Server) updatePluginSettings(ctx context.Context, c *app.RequestContext) {
	var req updatePluginSettingsRequest

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

	if err := s.plugins.SetSettings(ctx, p, c.Param("id"), changes); err != nil {
		s.fail(ctx, c, err)
		return
	}

	list, err := s.plugins.Settings(ctx, p, c.Param("id"))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toPluginSettingsDTO(list))
}
