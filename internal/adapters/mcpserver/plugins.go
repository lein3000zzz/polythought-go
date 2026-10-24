package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/plugins"
)

// The admin's tools for plugins (D411): installing from a URL or an archive in the call,
// approving, and the rest of what the Plugins page does. Approving a version, which runs
// another's code on the server, and every other change may wait for the admin; installing
// waits for nothing, since nothing installed runs before it is approved. A plugin's name,
// description and log are its author's words, under untrusted.

// Plugins is the plugins service.
type Plugins interface {
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
	LeftBehind(ctx context.Context, p auth.Principal) ([]plugins.LeftData, error)
	RemoveLeftBehind(ctx context.Context, p auth.Principal, id string) error
}

type pluginIn struct {
	ID string `json:"id" jsonschema:"the plugin's ID, as list_plugins gives it"`
}

type pluginVersionIn struct {
	ID      string `json:"id" jsonschema:"the plugin's ID"`
	Version string `json:"version" jsonschema:"the version, as list_plugins gives it"`
}

type pluginsOut struct {
	Plugins []pluginOut `json:"plugins"`
	// Found are versions on disk no admin installed, to adopt.
	Found      []pluginFoundOut `json:"found" jsonschema:"versions on disk no admin installed, for adopt_plugin"`
	LeftBehind []leftOut        `json:"leftBehind" jsonschema:"the data of plugins uninstalled without it, in bytes"`
	Notice     string           `json:"notice,omitempty"`
}

type pluginOut struct {
	ID       string           `json:"id"`
	Version  string           `json:"version,omitempty" jsonschema:"the approved version, which runs when enabled; absent until one is approved"`
	Enabled  bool             `json:"enabled"`
	Everyone bool             `json:"everyone" jsonschema:"every user may use it; else only users"`
	Users    []string         `json:"users,omitempty" jsonschema:"the IDs of those who may, when not everyone"`
	Caps     pluginCaps       `json:"caps"`
	Data     leftOut          `json:"data" jsonschema:"what its data takes, in bytes"`
	Versions []pluginVerOut   `json:"versions" jsonschema:"newest first"`
	Status   *pluginStatusOut `json:"status,omitempty" jsonschema:"how it runs, as it was loaded at start"`
	Restart  bool             `json:"restart,omitempty" jsonschema:"what runs is not what admins decided: restart_server applies it"`
	// Untrusted is its newest version's name and description: its author's words.
	Untrusted Untrusted `json:"untrusted,omitempty" jsonschema:"its name and description, its author's words"`
}

type pluginCaps struct {
	DataMiB   int `json:"dataMiB" jsonschema:"MiB of its data it may take; 0 for the Config page's"`
	MemoryMiB int `json:"memoryMiB" jsonschema:"MiB of memory its process may take; 0 for the Config page's"`
}

type pluginVerOut struct {
	Version     string     `json:"version"`
	Permissions []string   `json:"permissions" jsonschema:"what it may ask the server for, which approving it allows"`
	Nodes       int        `json:"nodes"`
	Triggers    int        `json:"triggers"`
	Signer      string     `json:"signer,omitempty" jsonschema:"the trusted key that signed it; absent when none an admin trusts did"`
	KeyID       string     `json:"keyId,omitempty"`
	InstalledBy string     `json:"installedBy,omitempty" jsonschema:"the user's ID"`
	InstalledAt time.Time  `json:"installedAt"`
	Approved    bool       `json:"approved"`
	ApprovedAt  *time.Time `json:"approvedAt,omitempty"`
}

type pluginStatusOut struct {
	Version   string    `json:"version"`
	State     string    `json:"state" jsonschema:"running, restarting, crashed, stopped, or failed: it would not start"`
	Since     time.Time `json:"since"`
	Crashes   int       `json:"crashes" jsonschema:"in the last 10 minutes"`
	Sandboxed bool      `json:"sandboxed"`
	Memory    int64     `json:"memory,omitempty" jsonschema:"bytes, when last measured"`
	MemoryCap int64     `json:"memoryCap,omitempty"`
	Data      int64     `json:"data,omitempty"`
	DataCap   int64     `json:"dataCap,omitempty"`
	// Untrusted is its last error and log lines, which it wrote.
	Untrusted map[string]any `json:"untrusted,omitempty" jsonschema:"its last error and its last log lines, as it wrote them"`
}

type pluginFoundOut struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type leftOut struct {
	ID     string `json:"id,omitempty"`
	Files  int64  `json:"files" jsonschema:"its directory's"`
	Stored int64  `json:"stored" jsonschema:"what its nodes stored in workflows"`
}

type installIn struct {
	URL     string `json:"url,omitempty" jsonschema:"an https URL of the archive, a gzipped tar"`
	Archive string `json:"archive,omitempty" jsonschema:"or the archive itself, base64: at most about 700 KB, a call being at most 1 MiB"`
}

type installedOut struct {
	ID             string    `json:"id"`
	Version        string    `json:"version"`
	Permissions    []string  `json:"permissions"`
	NewPermissions []string  `json:"newPermissions" jsonschema:"those the version that runs was not approved for: all of them for a plugin new here"`
	Upgrade        bool      `json:"upgrade,omitempty"`
	Signed         bool      `json:"signed"`
	Signer         string    `json:"signer,omitempty"`
	KeyID          string    `json:"keyId,omitempty"`
	Warning        string    `json:"warning,omitempty" jsonschema:"for the admin, when no key they trust signed it"`
	Untrusted      Untrusted `json:"untrusted,omitempty" jsonschema:"its name, its author's words"`
	Notice         string    `json:"notice,omitempty"`
}

type approveIn struct {
	ID          string   `json:"id"`
	Version     string   `json:"version"`
	Permissions []string `json:"permissions" jsonschema:"the permissions it asks for, as list_plugins has them, every one: what the admin agrees to"`
}

type enableIn struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled" jsonschema:"on or off, from the next start: workflows using one switched off pause then"`
}

type pluginUsersIn struct {
	ID       string   `json:"id"`
	Everyone bool     `json:"everyone,omitempty" jsonschema:"every user may use it"`
	Users    []string `json:"users,omitempty" jsonschema:"else the IDs of those who may, as list_users gives them"`
}

type pluginCapsIn struct {
	ID string `json:"id"`
	pluginCaps
}

type uninstallIn struct {
	ID         string `json:"id"`
	RemoveData bool   `json:"removeData,omitempty" jsonschema:"remove its data too; else it is left behind, for a version installed after"`
}

type pluginSettingsOut struct {
	Settings []settingOut `json:"settings"`
}

type pluginSettingsIn struct {
	ID      string         `json:"id"`
	Changes map[string]any `json:"changes" jsonschema:"by key, each its new value, null for its default"`
}

type pluginHistoryOut struct {
	Changes []pluginAuditOut `json:"changes"`
}

type pluginAuditOut struct {
	Version string    `json:"version,omitempty"`
	Action  string    `json:"action"`
	Detail  string    `json:"detail,omitempty"`
	By      string    `json:"by,omitempty" jsonschema:"the user's ID; absent when the server did it itself"`
	At      time.Time `json:"at"`
}

func (s *Server) addPluginTools(srv *mcp.Server) {
	add(s, srv, Tool[struct{}, pluginsOut]{
		Name: "list_plugins", Title: "List the plugins", Scope: domain.ScopeAdmin, ReadOnly: true,
		Description: "Every plugin installed, its versions and the permissions each asks for, how it runs, who may use it, " +
			"its caps; versions on disk no admin installed; data uninstalled plugins left behind.",
		Handle: s.listPlugins,
	})

	add(s, srv, Tool[installIn, installedOut]{
		Name: "install_plugin", Title: "Install a plugin", Scope: domain.ScopeAdmin,
		Description: "Install a plugin's archive, from an https URL or sent in the call, checked whole. Nothing installed runs " +
			"until it is approved: approve_plugin, once the admin has seen what it asks for.",
		Handle: s.installPlugin,
	})

	add(s, srv, Tool[pluginVersionIn, installedOut]{
		Name: "adopt_plugin", Title: "Adopt a plugin found on disk", Scope: domain.ScopeAdmin,
		Description: "Install a version found on disk that no admin installed, checked as an archive would be.",
		Handle:      s.adoptPlugin,
	})

	add(s, srv, Tool[approveIn, doneOut]{
		Name: "approve_plugin", Title: "Approve a plugin version", Scope: domain.ScopeAdmin,
		Description: "Approve a version and the permissions it asks for, every one, so it runs on the server from the next " +
			"start: another's code. It may wait for the admin to confirm.",
		Handle: s.approvePlugin,
	})

	add(s, srv, Tool[pluginVersionIn, doneOut]{
		Name: "remove_plugin_version", Title: "Remove a plugin version", Scope: domain.ScopeAdmin, Destructive: true,
		Description: "Remove a version not approved, its files and all: an upgrade the admin will not have. It may wait for the admin.",
		Handle:      s.removePluginVersion,
	})

	add(s, srv, Tool[enableIn, doneOut]{
		Name: "enable_plugin", Title: "Switch a plugin on or off", Scope: domain.ScopeAdmin,
		Description: "Switch a plugin on or off, from the next start. It may wait for the admin.",
		Handle:      s.enablePlugin,
	})

	add(s, srv, Tool[pluginUsersIn, doneOut]{
		Name: "set_plugin_users", Title: "Set who may use a plugin", Scope: domain.ScopeAdmin,
		Description: "Let every user use a plugin, or only those named. It may wait for the admin.",
		Handle:      s.setPluginUsers,
	})

	add(s, srv, Tool[pluginCapsIn, doneOut]{
		Name: "set_plugin_caps", Title: "Set a plugin's caps", Scope: domain.ScopeAdmin,
		Description: "Set what a plugin may take of disk and memory, 0 for the Config page's. It may wait for the admin.",
		Handle:      s.setPluginCaps,
	})

	add(s, srv, Tool[pluginIn, doneOut]{
		Name: "start_plugin", Title: "Start a plugin again", Scope: domain.ScopeAdmin,
		Description: "Start a plugin that crashed or stopped, as it was approved. It may wait for the admin.",
		Handle:      s.startPlugin,
	})

	add(s, srv, Tool[uninstallIn, doneOut]{
		Name: "uninstall_plugin", Title: "Uninstall a plugin", Scope: domain.ScopeAdmin, Destructive: true,
		Description: "Uninstall a plugin, every version, from the next start: workflows using it pause then. Its data stays " +
			"unless removeData. It may wait for the admin.",
		Handle: s.uninstallPlugin,
	})

	add(s, srv, Tool[pluginIn, doneOut]{
		Name: "remove_left_behind", Title: "Remove a plugin's data left behind", Scope: domain.ScopeAdmin, Destructive: true,
		Description: "Remove the data a plugin uninstalled without it left behind. It may wait for the admin.",
		Handle:      s.removeLeftBehind,
	})

	add(s, srv, Tool[pluginIn, pluginSettingsOut]{
		Name: "plugin_settings", Title: "A plugin's own settings", Scope: domain.ScopeAdmin, ReadOnly: true,
		Description: "A plugin's own settings, as the version it runs declares them; secrets say only whether they are set.",
		Handle:      s.pluginSettings,
	})

	add(s, srv, Tool[pluginSettingsIn, pluginSettingsOut]{
		Name: "update_plugin_settings", Title: "Change a plugin's settings", Scope: domain.ScopeAdmin,
		Description: "Change a plugin's own settings, all or none, its runs getting them from the next. Secret ones are the " +
			"admin's to set at a browser. It may wait for the admin.",
		Handle: s.updatePluginSettings,
	})

	add(s, srv, Tool[pluginIn, pluginHistoryOut]{
		Name: "plugin_history", Title: "A plugin's history", Scope: domain.ScopeAdmin, ReadOnly: true,
		Description: "What admins and the server did with a plugin, newest first, with who did it.",
		Handle:      s.pluginHistory,
	})
}

func (s *Server) listPlugins(ctx context.Context, p auth.Principal, _ struct{}) (pluginsOut, error) {
	list, found, err := s.plugins.List(ctx, p)
	if err != nil {
		return pluginsOut{}, err
	}

	left, err := s.plugins.LeftBehind(ctx, p)
	if err != nil {
		return pluginsOut{}, err
	}

	out := pluginsOut{Plugins: make([]pluginOut, len(list)), Found: make([]pluginFoundOut, len(found)), LeftBehind: make([]leftOut, len(left))}

	for i, info := range list {
		out.Plugins[i] = pluginOf(info)

		if len(out.Plugins[i].Untrusted) > 0 || out.Plugins[i].Status != nil && out.Plugins[i].Status.Untrusted != nil {
			out.Notice = Notice
		}
	}

	for i, f := range found {
		out.Found[i] = pluginFoundOut{ID: f.ID, Version: f.Version}
	}

	for i, l := range left {
		out.LeftBehind[i] = leftOut{ID: l.ID, Files: l.Files, Stored: l.Stored}
	}

	return out, nil
}

// manifestOf is what a version's manifest says that a tool gives.
type manifestOf struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Permissions []string          `json:"permissions"`
	Nodes       []json.RawMessage `json:"nodes"`
	Triggers    []json.RawMessage `json:"triggers"`
}

func pluginOf(info plugins.Info) pluginOut {
	out := pluginOut{
		ID: info.ID, Version: info.Version, Enabled: info.Enabled, Everyone: info.Everyone, Restart: info.Restart,
		Caps:     pluginCaps{DataMiB: info.Caps.DataMiB, MemoryMiB: info.Caps.MemoryMiB},
		Data:     leftOut{Files: info.Data.Files, Stored: info.Data.Stored},
		Versions: make([]pluginVerOut, len(info.Versions)),
	}

	for _, u := range info.Users {
		out.Users = append(out.Users, string(u))
	}

	for i, v := range info.Versions {
		var m manifestOf
		_ = json.Unmarshal(v.Manifest, &m)

		if i == 0 {
			out.Untrusted = worded(Untrusted{"name": m.Name, "description": m.Description})
		}

		out.Versions[i] = pluginVerOut{
			Version: v.Version, Permissions: orNone(m.Permissions), Nodes: len(m.Nodes), Triggers: len(m.Triggers),
			Signer: v.Signer, KeyID: v.KeyID, InstalledBy: string(v.InstalledBy), InstalledAt: v.InstalledAt,
			Approved: v.Approved(), ApprovedAt: optionalTime(v.ApprovedAt),
		}
	}

	if st := info.Status; st != nil {
		out.Status = &pluginStatusOut{
			Version: st.Version, State: st.State, Since: st.Since, Crashes: st.Crashes, Sandboxed: st.Sandboxed,
			Memory: st.Memory, MemoryCap: st.MemoryCap, Data: st.Data, DataCap: st.DataCap,
		}

		if st.LastError != "" || len(st.Log) > 0 {
			out.Status.Untrusted = map[string]any{"lastError": st.LastError, "log": orNone(st.Log)}
		}
	}

	return out
}

// worded is what of u has words in it, nil for none.
func worded(u Untrusted) Untrusted {
	for k, v := range u {
		if v == "" {
			delete(u, k)
		}
	}

	if len(u) == 0 {
		return nil
	}

	return u
}

func orNone(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}

func (s *Server) installPlugin(ctx context.Context, p auth.Principal, in installIn) (installedOut, error) {
	var (
		got plugins.Installed
		err error
	)

	switch {
	case (in.URL == "") == (in.Archive == ""):
		return installedOut{}, invalid("give the archive's url, or the archive itself, one of them")
	case in.URL != "":
		got, err = s.plugins.InstallFrom(ctx, p, in.URL)
	default:
		archive, decodeErr := base64.StdEncoding.DecodeString(in.Archive)
		if decodeErr != nil {
			return installedOut{}, invalid("archive is not base64")
		}

		got, err = s.plugins.Install(ctx, p, bytes.NewReader(archive))
	}

	if err != nil {
		return installedOut{}, err
	}

	return installedOf(got), nil
}

func installedOf(in plugins.Installed) installedOut {
	f := in.File

	out := installedOut{
		ID: f.ID, Version: f.Version, Permissions: orNone(f.Permissions), NewPermissions: orNone(in.NewPermissions),
		Upgrade: in.Upgrade, Signed: f.Signed, Signer: f.Signer, KeyID: f.KeyID, Warning: f.Warning,
		Untrusted: worded(Untrusted{"name": f.Name}),
	}

	if out.Untrusted != nil {
		out.Notice = Notice
	}

	return out
}

func (s *Server) adoptPlugin(ctx context.Context, p auth.Principal, in pluginVersionIn) (installedOut, error) {
	got, err := s.plugins.Adopt(ctx, p, in.ID, in.Version)
	if err != nil {
		return installedOut{}, err
	}

	return installedOf(got), nil
}

func (s *Server) approvePlugin(ctx context.Context, p auth.Principal, in approveIn) (doneOut, error) {
	return done(s.plugins.Approve(ctx, p, in.ID, in.Version, orNone(in.Permissions)))
}

func (s *Server) removePluginVersion(ctx context.Context, p auth.Principal, in pluginVersionIn) (doneOut, error) {
	return done(s.plugins.RemoveVersion(ctx, p, in.ID, in.Version))
}

func (s *Server) enablePlugin(ctx context.Context, p auth.Principal, in enableIn) (doneOut, error) {
	return done(s.plugins.SetEnabled(ctx, p, in.ID, in.Enabled))
}

func (s *Server) setPluginUsers(ctx context.Context, p auth.Principal, in pluginUsersIn) (doneOut, error) {
	users := make([]domain.UserID, len(in.Users))
	for i, u := range in.Users {
		users[i] = domain.UserID(u)
	}

	return done(s.plugins.SetUsers(ctx, p, in.ID, in.Everyone, users))
}

func (s *Server) setPluginCaps(ctx context.Context, p auth.Principal, in pluginCapsIn) (doneOut, error) {
	return done(s.plugins.SetCaps(ctx, p, in.ID, domain.PluginCaps{DataMiB: in.DataMiB, MemoryMiB: in.MemoryMiB}))
}

func (s *Server) startPlugin(ctx context.Context, p auth.Principal, in pluginIn) (doneOut, error) {
	return done(s.plugins.Start(ctx, p, in.ID))
}

func (s *Server) uninstallPlugin(ctx context.Context, p auth.Principal, in uninstallIn) (doneOut, error) {
	return done(s.plugins.Uninstall(ctx, p, in.ID, in.RemoveData))
}

func (s *Server) removeLeftBehind(ctx context.Context, p auth.Principal, in pluginIn) (doneOut, error) {
	return done(s.plugins.RemoveLeftBehind(ctx, p, in.ID))
}

// done is the answer to a change that answers nothing more.
func done(err error) (doneOut, error) {
	if err != nil {
		return doneOut{}, err
	}

	return doneOut{Done: true}, nil
}

func (s *Server) pluginSettings(ctx context.Context, p auth.Principal, in pluginIn) (pluginSettingsOut, error) {
	list, err := s.plugins.Settings(ctx, p, in.ID)
	if err != nil {
		return pluginSettingsOut{}, err
	}

	out := pluginSettingsOut{Settings: make([]settingOut, len(list))}

	for i, st := range list {
		o := settingOut{
			Key: st.Key, Label: st.Label, Description: st.Description, Kind: string(st.Kind), Options: st.Options,
			Secret: st.Secret, IsSet: st.IsSet,
		}

		if !st.Secret {
			o.Value, o.Default = jsonValue(st.Encode(st.Value)), jsonValue(st.Encode(st.Default))
		}

		out.Settings[i] = o
	}

	return out, nil
}

func (s *Server) updatePluginSettings(ctx context.Context, p auth.Principal, in pluginSettingsIn) (pluginSettingsOut, error) {
	changes, err := rawChanges(in.Changes)
	if err != nil {
		return pluginSettingsOut{}, err
	}

	if err := s.plugins.SetSettings(ctx, p, in.ID, changes); err != nil {
		return pluginSettingsOut{}, err
	}

	return s.pluginSettings(ctx, p, pluginIn{ID: in.ID})
}

func (s *Server) pluginHistory(ctx context.Context, p auth.Principal, in pluginIn) (pluginHistoryOut, error) {
	list, err := s.plugins.History(ctx, p, in.ID)
	if err != nil {
		return pluginHistoryOut{}, err
	}

	out := pluginHistoryOut{Changes: make([]pluginAuditOut, len(list))}

	for i, a := range list {
		out.Changes[i] = pluginAuditOut{Version: a.Version, Action: a.Action, Detail: a.Detail, By: string(a.By), At: a.At}
	}

	return out, nil
}
