package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// CredentialField is one value of a credential, such as an API key.
type CredentialField struct {
	Name        string
	Label       string
	Description string
	Required    bool
	Secret      bool // stored encrypted and never shown again
}

// CredentialType describes a kind of credential users keep in their vault for nodes to
// use on their behalf. A node asks for one with a credential param naming the type.
type CredentialType struct {
	Name        string // "http.header"
	Label       string
	Description string
	Fields      []CredentialField

	// Check validates values beyond required fields, such as the form of a header name.
	// Optional.
	Check func(values map[string]string) error

	// Test tries the credential against the service it is for. Optional.
	Test func(ctx context.Context, values map[string]string) error
}

// RegisterCredentialType adds a credential type. Register types before the nodes that
// use them.
func (r *Registry) RegisterCredentialType(t CredentialType) error {
	if t.Name == "" || t.Label == "" {
		return fmt.Errorf("register credential type %q: a name and a label are required", t.Name)
	}

	if len(t.Fields) == 0 {
		return fmt.Errorf("register credential type %q: no fields", t.Name)
	}

	seen := make(map[string]bool, len(t.Fields))
	for _, f := range t.Fields {
		if f.Name == "" || f.Label == "" || seen[f.Name] {
			return fmt.Errorf("register credential type %q: field %q needs a unique name and a label", t.Name, f.Name)
		}

		seen[f.Name] = true
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.credentials[t.Name]; ok {
		return fmt.Errorf("register credential type %q: already registered", t.Name)
	}

	r.credentials[t.Name] = t

	return nil
}

func (r *Registry) CredentialType(name string) (CredentialType, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	t, ok := r.credentials[name]

	return t, ok
}

// CredentialTypes lists the registered credential types, sorted by name.
func (r *Registry) CredentialTypes() []CredentialType {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]CredentialType, 0, len(r.credentials))
	for _, t := range r.credentials {
		out = append(out, t)
	}

	slices.SortFunc(out, func(a, b CredentialType) int { return strings.Compare(a.Name, b.Name) })

	return out
}

var errCredentialParam = errors.New("a credential param must name a registered credential type")

// checkCredentialParams makes sure every credential param of typ names a known type.
// The caller holds r.mu.
func (r *Registry) checkCredentialParams(typ NodeType) error {
	for _, p := range typ.Params {
		if p.Kind != ParamCredential {
			continue
		}

		if _, ok := r.credentials[p.CredentialType]; !ok {
			return fmt.Errorf("register node type %q: param %q: %w, not %q", typ.Name, p.Name, errCredentialParam, p.CredentialType)
		}
	}

	return nil
}
