package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
)

// WithSettings keeps plugins' own settings (D329), secret ones sealed by box.
func WithSettings(store ports.PluginSettings, box ports.SecretBox) Option {
	return func(s *Service) { s.settings, s.box = store, box }
}

// Setting is one of a plugin's own settings, as admins see it: Value is nil for a secret,
// which IsSet says whether one is saved.
type Setting struct {
	settings.Spec

	Value any
	IsSet bool
}

// manifestSettings are the settings a manifest declares, as setting specs.
type manifestSettings struct {
	Settings []struct {
		Key         string   `json:"key"`
		Label       string   `json:"label"`
		Description string   `json:"description"`
		Kind        string   `json:"kind"`
		Default     any      `json:"default"`
		Options     []string `json:"options"`
		Secret      bool     `json:"secret"`
	} `json:"settings"`
}

// specsOf are the settings of the version a plugin runs, its approved one, parsed: none
// before an approval.
func specsOf(ip domain.InstalledPlugin) ([]settings.Spec, error) {
	v, ok := ip.Find(ip.Version)
	if !ok {
		return nil, nil
	}

	var m manifestSettings

	dec := json.NewDecoder(bytes.NewReader(v.Manifest))
	dec.UseNumber()

	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("the manifest of %s %s: %w", ip.ID, ip.Version, err)
	}

	specs := make([]settings.Spec, 0, len(m.Settings))

	for _, st := range m.Settings {
		spec := settings.Spec{
			Key: st.Key, Label: st.Label, Description: st.Description, Kind: settings.Kind(st.Kind),
			Options: st.Options, Secret: st.Secret, Apply: settings.ApplyLive,
		}

		if st.Default != nil {
			d, err := spec.Parse(st.Default)
			if err != nil {
				return nil, fmt.Errorf("setting %s's default: %w", st.Key, err)
			}

			spec.Default = d
		} else {
			spec.Default = zeroOf(spec)
		}

		specs = append(specs, spec)
	}

	return specs, nil
}

// zeroOf is a setting's default when its manifest gives none.
func zeroOf(spec settings.Spec) any {
	switch spec.Kind {
	case settings.KindInt:
		return 0
	case settings.KindBool:
		return false
	case settings.KindList:
		return []string{}
	case settings.KindDuration:
		return time.Duration(0)
	case settings.KindEnum:
		if len(spec.Options) > 0 {
			return spec.Options[0]
		}
	}

	return ""
}

// values are a plugin's settings, each its saved value or else its default, secrets
// opened: what its runs get.
func (s *Service) values(ctx context.Context, ip domain.InstalledPlugin) (map[string]any, map[string]bool, error) {
	specs, err := specsOf(ip)
	if err != nil || len(specs) == 0 {
		return map[string]any{}, map[string]bool{}, err
	}

	var stored []ports.StoredPluginSetting
	if s.settings != nil {
		if stored, err = s.settings.Settings(ctx, ip.ID); err != nil {
			return nil, nil, err
		}
	}

	byKey := map[string]ports.StoredPluginSetting{}
	for _, st := range stored {
		byKey[st.Key] = st
	}

	values, set := make(map[string]any, len(specs)), make(map[string]bool, len(specs))

	for _, spec := range specs {
		values[spec.Key] = spec.Default

		st, ok := byKey[spec.Key]
		if !ok {
			continue
		}

		var raw any

		switch {
		case st.Sealed != nil && s.box != nil:
			plain, err := s.box.Open(ctx, st.Sealed, settingAAD(ip.ID, spec.Key))
			if err != nil {
				s.log.Warn(ctx, "Plugin setting not opened", "plugin", ip.ID, "key", spec.Key, logging.Err(err))
				continue
			}

			raw = string(plain)
		case st.Value != nil:
			dec := json.NewDecoder(bytes.NewReader(st.Value))
			dec.UseNumber()

			if err := dec.Decode(&raw); err != nil {
				continue
			}
		default:
			continue
		}

		// A value saved for a version before may not be of this one's kind: its default then.
		if v, err := spec.Parse(raw); err == nil {
			values[spec.Key], set[spec.Key] = v, true
		}
	}

	return values, set, nil
}

// settingAAD binds a sealed setting to its plugin and key.
func settingAAD(plugin, key string) []byte {
	return []byte("plugin-setting:" + plugin + ":" + key)
}

// Settings are a plugin's own settings, as the version it runs declares them (D329).
func (s *Service) Settings(ctx context.Context, p auth.Principal, id string) ([]Setting, error) {
	if !p.User.IsAdmin() {
		return nil, domain.ErrForbidden
	}

	ip, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	specs, err := specsOf(ip)
	if err != nil {
		return nil, err
	}

	values, set, err := s.values(ctx, ip)
	if err != nil {
		return nil, err
	}

	out := make([]Setting, len(specs))
	for i, spec := range specs {
		out[i] = Setting{Spec: spec, IsSet: set[spec.Key]}
		if !spec.Secret {
			out[i].Value = values[spec.Key]
		}
	}

	return out, nil
}

// SetSettings saves changes to a plugin's own settings, keyed by setting; a JSON null
// resets one to its default, as does an empty string for a secret. Every problem is
// reported at once, naming the setting, and nothing is saved unless every change is
// valid. Its runs get them from the next one.
func (s *Service) SetSettings(ctx context.Context, p auth.Principal, id string, changes map[string]json.RawMessage) error {
	if !p.User.IsAdmin() {
		return domain.ErrForbidden
	}

	if s.settings == nil {
		return fmt.Errorf("%w: plugin settings are not kept here", domain.ErrInvalidInput)
	}

	ip, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}

	specs, err := specsOf(ip)
	if err != nil {
		return err
	}

	before, _, err := s.values(ctx, ip)
	if err != nil {
		return err
	}

	byKey := map[string]settings.Spec{}
	for _, spec := range specs {
		byKey[spec.Key] = spec
	}

	problems := domain.ValidationError{Base: domain.ErrInvalidInput}

	var (
		out    []ports.PluginSettingChange
		detail []string
	)

	for _, key := range slices.Sorted(maps.Keys(changes)) {
		spec, ok := byKey[key]
		if !ok {
			problems.Add(domain.Problem{Param: key, Message: fmt.Sprintf("%s has no setting %q", ip.ID, key)})
			continue
		}

		change, said, err := s.change(ctx, ip.ID, spec, changes[key], before[key])
		if err != nil {
			problems.Add(domain.Problem{Param: key, Message: fmt.Sprintf("%s: %v", spec.Label, err)})
			continue
		}

		out, detail = append(out, change), append(detail, said)
	}

	if err := problems.Err(); err != nil {
		return err
	}

	if len(out) == 0 {
		return nil
	}

	err = s.settings.SetSettings(ctx, ip.ID, out, domain.PluginAudit{
		PluginID: ip.ID, Version: ip.Version, Action: domain.PluginSettings, Detail: strings.Join(detail, "; "),
		By: p.User.ID, At: s.now(),
	})
	if err != nil {
		return err
	}

	s.log.Info(ctx, "Plugin settings saved", "plugin", ip.ID, "by", p.User.ID, "keys", len(out))
	s.reload(ctx)

	return nil
}

// change is one setting's change, checked, and what its audit says of it: a secret only
// that it changed.
func (s *Service) change(ctx context.Context, plugin string, spec settings.Spec, raw json.RawMessage, was any) (ports.PluginSettingChange, string, error) {
	c := ports.PluginSettingChange{Key: spec.Key}

	var v any

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	if err := dec.Decode(&v); err != nil {
		return c, "", fmt.Errorf("not a value: %w", err)
	}

	if v == nil || (spec.Secret && v == "") {
		if spec.Secret {
			return c, spec.Key + " reset", nil
		}

		return c, fmt.Sprintf("%s: %s → %s (its default)", spec.Key, spec.Encode(was), spec.Encode(spec.Default)), nil
	}

	parsed, err := spec.Parse(v)
	if err != nil {
		return c, "", err
	}

	if !spec.Secret {
		c.Value = spec.Encode(parsed)
		return c, fmt.Sprintf("%s: %s → %s", spec.Key, spec.Encode(was), c.Value), nil
	}

	if s.box == nil {
		return c, "", fmt.Errorf("secrets cannot be kept here")
	}

	if c.Sealed, err = s.box.Seal(ctx, []byte(parsed.(string)), settingAAD(plugin, spec.Key)); err != nil {
		return c, "", err
	}

	return c, spec.Key + " changed", nil
}

// RunSettings are what a plugin's runs get of its settings (D329), as JSON would have
// them: its saved values, or its defaults, secrets opened.
func (s *Service) RunSettings(id string) map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.runSettings[id]
}

// encodedValues are values as a run gets them: durations as text, like the API's.
func encodedValues(specs []settings.Spec, values map[string]any) map[string]any {
	out := make(map[string]any, len(values))

	for _, spec := range specs {
		var v any
		_ = json.Unmarshal(spec.Encode(values[spec.Key]), &v)
		out[spec.Key] = v
	}

	return out
}
