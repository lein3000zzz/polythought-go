package mcpserver

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/geoblock"
	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
)

// The admin's tools for the instance's Config (D411): its settings and their history, a
// restart, the mail test and the geoblock check. Secret settings are the browser's alone;
// changing Config and restarting may wait for the admin.

// Settings is the instance's: the settings service.
type Settings interface {
	List(ctx context.Context, p auth.Principal) (settings.Overview, error)
	Update(ctx context.Context, p auth.Principal, changes map[string]json.RawMessage) error
	History(ctx context.Context, p auth.Principal, f ports.PageFilter) (ports.Page[ports.SettingAudit], error)
	Restart(ctx context.Context, p auth.Principal) error
}

// Mail sends the admin's test mail: the mail service.
type Mail interface {
	SendTest(ctx context.Context, actor auth.Principal, to string) error
}

// Geoblock is whether Polymarket takes orders from here: the geoblock service.
type Geoblock interface {
	Status(ctx context.Context, actor auth.Principal) (geoblock.Status, error)
	CheckNow(ctx context.Context, actor auth.Principal) (geoblock.Status, error)
}

type settingsIn struct {
	Group string `json:"group,omitempty" jsonschema:"only those of this group, such as Trading or Agents over MCP"`
}

type settingOut struct {
	Key         string   `json:"key"`
	Group       string   `json:"group"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Kind        string   `json:"kind" jsonschema:"string, int, bool, duration, enum or list"`
	Options     []string `json:"options,omitempty"`
	Value       any      `json:"value,omitempty" jsonschema:"what it is now; absent for a secret"`
	Default     any      `json:"default,omitempty"`
	Source      string   `json:"source,omitempty" jsonschema:"default, database, file or environment: one set by the file or the environment changes there"`
	Applies     string   `json:"applies,omitempty" jsonschema:"live, at once; restart, once the server restarts"`
	Pending     bool     `json:"pending,omitempty" jsonschema:"changed, waiting for a restart"`
	Secret      bool     `json:"secret,omitempty" jsonschema:"secret: set at a browser only, never by an agent"`
	AtBrowser   bool     `json:"atBrowser,omitempty" jsonschema:"no secret, but it says where one goes: set at a browser only, never by an agent"`
	IsSet       bool     `json:"isSet,omitempty" jsonschema:"a secret has a value"`
}

type settingsOut struct {
	Settings       []settingOut `json:"settings"`
	RestartPending bool         `json:"restartPending,omitempty" jsonschema:"some changes wait for a restart"`
}

type updateSettingsIn struct {
	Changes map[string]any `json:"changes" jsonschema:"by key, each its new value as the setting's kind takes it, null for its default"`
}

type historyOut struct {
	Changes []settingChangeOut `json:"changes"`
	Next    string             `json:"next,omitempty"`
}

type settingChangeOut struct {
	Key      string    `json:"key"`
	OldValue any       `json:"oldValue,omitempty"`
	NewValue any       `json:"newValue,omitempty"`
	Secret   bool      `json:"secret,omitempty"`
	By       string    `json:"by" jsonschema:"the user's ID"`
	At       time.Time `json:"at"`
}

type testMailIn struct {
	To string `json:"to" jsonschema:"the address to mail, as the admin gave it"`
}

type geoblockIn struct {
	Check bool `json:"check,omitempty" jsonschema:"ask Polymarket now, rather than give the last answer"`
}

type geoblockOut struct {
	Checked   bool       `json:"checked" jsonschema:"Polymarket has answered at least once"`
	Blocked   bool       `json:"blocked" jsonschema:"it refuses orders from where the server runs"`
	Country   string     `json:"country,omitempty"`
	Region    string     `json:"region,omitempty"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	Error     string     `json:"error,omitempty"`
}

func (s *Server) addConfigTools(srv *mcp.Server) {
	add(s, srv, Tool[settingsIn, settingsOut]{
		Name: "list_settings", Title: "List the Config settings", Scope: domain.ScopeAdmin, ReadOnly: true,
		Description: "The instance's settings, as the Config page has them: each one's value, default, where it is set, and " +
			"whether a change applies at once or on a restart. Secrets show only whether they are set.",
		Handle: s.listSettings,
	})

	add(s, srv, Tool[updateSettingsIn, settingsOut]{
		Name: "update_settings", Title: "Change Config", Scope: domain.ScopeAdmin,
		Description: "Change settings, by key, all or none: each checked as the Config page does. Secret settings, and those " +
			"marked atBrowser, are the admin's to set at a browser. It may wait for the admin to confirm, as get_confirmation then says. Some apply only " +
			"once the server restarts.",
		Handle: s.updateSettings,
	})

	add(s, srv, Tool[page, historyOut]{
		Name: "setting_history", Title: "List Config changes", Scope: domain.ScopeAdmin, ReadOnly: true,
		Description: "The changes to the settings, newest first, with who made them; a secret's values never.",
		Handle:      s.settingHistory,
	})

	add(s, srv, Tool[struct{}, doneOut]{
		Name: "restart_server", Title: "Restart the server", Scope: domain.ScopeAdmin,
		Description: "Restart the server, so the changes waiting for a restart apply: every user's work stops for a moment, and " +
			"this connection drops. It may wait for the admin to confirm.",
		Handle: s.restartServer,
	})

	if s.mail != nil {
		add(s, srv, Tool[testMailIn, doneOut]{
			Name: "send_test_mail", Title: "Send a test mail", Scope: domain.ScopeAdmin,
			Description: "Mail an address the admin gave you, to see that the mail settings work.",
			Handle:      s.sendTestMail,
		})
	}

	if s.geoblock != nil {
		add(s, srv, Tool[geoblockIn, geoblockOut]{
			Name: "geoblock", Title: "Whether Polymarket takes orders from here", Scope: domain.ScopeAdmin, ReadOnly: true,
			Description: "Whether Polymarket refuses orders from where the server runs, as it last said, or as it says now.",
			Handle:      s.geoblockStatus,
		})
	}
}

func (s *Server) listSettings(ctx context.Context, p auth.Principal, in settingsIn) (settingsOut, error) {
	o, err := s.config.List(ctx, p)
	if err != nil {
		return settingsOut{}, err
	}

	out := settingsOut{Settings: []settingOut{}, RestartPending: o.RestartPending}

	for _, e := range o.Entries {
		if in.Group != "" && e.Group != in.Group {
			continue
		}

		st := settingOut{
			Key: e.Key, Group: e.Group, Label: e.Label, Description: e.Description, Kind: string(e.Kind), Options: e.Options,
			Source: string(e.Source), Applies: string(e.Apply), Pending: e.Pending, Secret: e.Secret, IsSet: e.IsSet,
			AtBrowser: e.AtBrowser,
		}

		if !e.Secret {
			st.Value, st.Default = jsonValue(e.Encode(e.Value)), jsonValue(e.Encode(e.Default))
		}

		out.Settings = append(out.Settings, st)
	}

	return out, nil
}

// rawChanges are settings' changes as the services take them.
func rawChanges(in map[string]any) (map[string]json.RawMessage, error) {
	if len(in) == 0 {
		return nil, invalid("changes names the settings to change, by key")
	}

	changes := make(map[string]json.RawMessage, len(in))

	for key, v := range in {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, invalid(key + ": its value is not JSON")
		}

		changes[key] = raw
	}

	return changes, nil
}

// jsonValue is raw JSON as a value, for an answer.
func jsonValue(raw json.RawMessage) any {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}

	return v
}

func (s *Server) updateSettings(ctx context.Context, p auth.Principal, in updateSettingsIn) (settingsOut, error) {
	changes, err := rawChanges(in.Changes)
	if err != nil {
		return settingsOut{}, err
	}

	if err := s.config.Update(ctx, p, changes); err != nil {
		return settingsOut{}, err
	}

	keys := map[string]bool{}
	for key := range changes {
		keys[key] = true
	}

	all, err := s.listSettings(ctx, p, settingsIn{})
	if err != nil {
		return settingsOut{}, err
	}

	out := settingsOut{Settings: []settingOut{}, RestartPending: all.RestartPending}

	for _, st := range all.Settings {
		if keys[st.Key] {
			out.Settings = append(out.Settings, st)
		}
	}

	return out, nil
}

func (s *Server) settingHistory(ctx context.Context, p auth.Principal, in page) (historyOut, error) {
	got, err := s.config.History(ctx, p, in.filter())
	if err != nil {
		return historyOut{}, err
	}

	out := historyOut{Changes: make([]settingChangeOut, len(got.Items)), Next: got.NextCursor}

	for i, a := range got.Items {
		out.Changes[i] = settingChangeOut{
			Key: a.Key, OldValue: jsonValue(a.OldValue), NewValue: jsonValue(a.NewValue), Secret: a.Secret, By: string(a.ActorID), At: a.At,
		}
	}

	return out, nil
}

func (s *Server) restartServer(ctx context.Context, p auth.Principal, _ struct{}) (doneOut, error) {
	if err := s.config.Restart(ctx, p); err != nil {
		return doneOut{}, err
	}

	return doneOut{Done: true}, nil
}

func (s *Server) sendTestMail(ctx context.Context, p auth.Principal, in testMailIn) (doneOut, error) {
	if err := s.mail.SendTest(ctx, p, in.To); err != nil {
		return doneOut{}, err
	}

	return doneOut{Done: true}, nil
}

func (s *Server) geoblockStatus(ctx context.Context, p auth.Principal, in geoblockIn) (geoblockOut, error) {
	get := s.geoblock.Status
	if in.Check {
		get = s.geoblock.CheckNow
	}

	st, err := get(ctx, p)
	if err != nil {
		return geoblockOut{}, err
	}

	out := geoblockOut{Error: st.Error}
	if st.Last != nil {
		at := st.Last.CheckedAt
		out.Checked, out.Blocked, out.Country, out.Region, out.CheckedAt = true, st.Last.Blocked, st.Last.Country, st.Last.Region, &at
	}

	return out, nil
}
