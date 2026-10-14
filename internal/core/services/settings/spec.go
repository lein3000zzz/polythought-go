// Package settings holds the instance's runtime settings: what each one is, where its
// value comes from, and who may change it.
//
// A value comes from the first of these that has one: an environment variable, which
// locks the setting; a value an admin saved; the setting's default. Bootstrap values the
// server needs before it can reach the database stay in internal/config.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Kind string

const (
	KindString   Kind = "string"
	KindInt      Kind = "int"
	KindBool     Kind = "bool"
	KindDuration Kind = "duration"
	KindEnum     Kind = "enum"
	KindList     Kind = "list" // of strings
)

// Apply says when a change takes effect.
type Apply string

const (
	ApplyLive    Apply = "live"    // at once
	ApplyRestart Apply = "restart" // after the server restarts
)

// EnvPrefix starts every environment variable the server reads.
const EnvPrefix = "POLYTHOUGHT_"

// Spec describes one setting.
type Spec struct {
	Key         string // dotted, e.g. "auth.registration"
	Group       string // a heading on the admin Config page
	Label       string
	Description string
	Kind        Kind

	// Default is the value with no override, typed as the setting reads: string, int,
	// bool, time.Duration or []string.
	Default any

	Options []string // the allowed values of an enum
	Secret  bool     // stored encrypted and never shown again
	Apply   Apply

	// Check validates a parsed value beyond its kind, e.g. a range.
	Check func(v any) error

	// Env names the environment variable that overrides the setting. Empty derives it
	// from the key: "auth.registration" reads POLYTHOUGHT_AUTH_REGISTRATION.
	Env string
}

// EnvName is the environment variable that overrides the setting.
func (s Spec) EnvName() string {
	if s.Env != "" {
		return s.Env
	}

	return EnvPrefix + strings.ToUpper(strings.ReplaceAll(s.Key, ".", "_"))
}

// Parse turns a raw value into the setting's type. Raw values come as environment
// strings or as decoded JSON (strings, json.Number or float64, bool, []any). Errors say
// what is wrong in words fit for the person who typed the value.
func (s Spec) Parse(raw any) (any, error) {
	v, err := s.parseKind(raw)
	if err != nil {
		return nil, err
	}

	if s.Check != nil {
		if err := s.Check(v); err != nil {
			return nil, err
		}
	}

	return v, nil
}

func (s Spec) parseKind(raw any) (any, error) {
	switch s.Kind {
	case KindString:
		str, ok := raw.(string)
		if !ok {
			return nil, errors.New("want text")
		}

		if !s.Secret {
			str = strings.TrimSpace(str)
		}

		return str, nil

	case KindInt:
		switch n := raw.(type) {
		case int:
			return n, nil
		case float64:
			if n != float64(int(n)) {
				return nil, errors.New("want a whole number")
			}

			return int(n), nil
		case json.Number:
			i, err := strconv.Atoi(n.String())
			if err != nil {
				return nil, errors.New("want a whole number")
			}

			return i, nil
		case string:
			i, err := strconv.Atoi(strings.TrimSpace(n))
			if err != nil {
				return nil, fmt.Errorf("%q is not a whole number", n)
			}

			return i, nil
		}

		return nil, errors.New("want a whole number")

	case KindBool:
		switch b := raw.(type) {
		case bool:
			return b, nil
		case string:
			v, err := strconv.ParseBool(strings.TrimSpace(b))
			if err != nil {
				return nil, fmt.Errorf("%q is not true or false", b)
			}

			return v, nil
		}

		return nil, errors.New("want true or false")

	case KindDuration:
		if d, ok := raw.(time.Duration); ok {
			return d, nil
		}

		str, ok := raw.(string)
		if !ok {
			return nil, errors.New("want a duration like 30s or 24h")
		}

		d, err := time.ParseDuration(strings.TrimSpace(str))
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("%q is not a positive duration like 30s or 24h", str)
		}

		return d, nil

	case KindEnum:
		str, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("want one of %s", strings.Join(s.Options, ", "))
		}

		str = strings.TrimSpace(str)
		if !slices.Contains(s.Options, str) {
			return nil, fmt.Errorf("%q is not one of %s", str, strings.Join(s.Options, ", "))
		}

		return str, nil

	case KindList:
		var items []string

		switch l := raw.(type) {
		case []string:
			items = l
		case string: // an environment variable: comma-separated
			items = strings.Split(l, ",")
		case []any:
			for _, e := range l {
				str, ok := e.(string)
				if !ok {
					return nil, errors.New("want a list of text")
				}

				items = append(items, str)
			}
		default:
			return nil, errors.New("want a list of text")
		}

		out := []string{}
		for _, item := range items {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}

		return out, nil
	}

	return nil, fmt.Errorf("unknown kind %q", s.Kind)
}

// Encode is the JSON form of a parsed value, as stored and as the API shows it.
func (s Spec) Encode(v any) json.RawMessage {
	if d, ok := v.(time.Duration); ok {
		v = FormatDuration(d)
	}

	b, _ := json.Marshal(v) // parsed values are plain strings, numbers, bools and lists
	return b
}

// FormatDuration writes d the way people type it: 168h, 1h30m, 90s.
func FormatDuration(d time.Duration) string {
	s := d.String()

	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}

	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}

	return s
}

// Registry holds every known setting.
type Registry struct {
	specs map[string]Spec
	order []string
}

func NewRegistry() *Registry {
	return &Registry{specs: make(map[string]Spec)}
}

// Register adds settings. A key registered twice, or a default that does not pass its
// own validation, is a programming error.
func (r *Registry) Register(specs ...Spec) error {
	for _, s := range specs {
		if s.Key == "" || s.Label == "" {
			return fmt.Errorf("settings: a setting needs a key and a label (%q)", s.Key)
		}

		if _, dup := r.specs[s.Key]; dup {
			return fmt.Errorf("settings: %q registered twice", s.Key)
		}

		// Keys are paths in one tree, so a setting cannot sit inside another.
		for _, other := range r.order {
			if strings.HasPrefix(other, s.Key+".") || strings.HasPrefix(s.Key, other+".") {
				return fmt.Errorf("settings: %q and %q overlap", s.Key, other)
			}
		}

		if s.Apply == "" {
			s.Apply = ApplyLive
		}

		if s.Secret && s.Kind != KindString {
			return fmt.Errorf("settings: secret %q must be a string", s.Key)
		}

		if _, err := s.Parse(s.Default); err != nil {
			return fmt.Errorf("settings: default of %q: %w", s.Key, err)
		}

		r.specs[s.Key] = s
		r.order = append(r.order, s.Key)
	}

	return nil
}

func (r *Registry) Spec(key string) (Spec, bool) {
	s, ok := r.specs[key]
	return s, ok
}

// Specs lists the settings in the order they were registered.
func (r *Registry) Specs() []Spec {
	out := make([]Spec, len(r.order))
	for i, k := range r.order {
		out[i] = r.specs[k]
	}

	return out
}
