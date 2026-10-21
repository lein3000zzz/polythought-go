package pluginhost

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/pkg/plugin"
)

var (
	pluginID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	// ownName is a node's, trigger's, credential type's or scenario's within its plugin.
	ownName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]{0,63}$`)
	semver  = regexp.MustCompile(`^(\d+)\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
)

// Check refuses a manifest the host cannot take: an ID, version or name not in their
// form, names given twice, or an SDK of another major version.
func Check(m plugin.Manifest) error {
	var errs []error

	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if !pluginID.MatchString(m.ID) {
		fail("id %q: a-z, 0-9 and dashes, starting with a letter, up to 40", m.ID)
	}

	if !semver.MatchString(m.Version) {
		fail("version %q is not semver, like 1.2.0", m.Version)
	}

	if strings.TrimSpace(m.Name) == "" {
		fail("a name is needed")
	}

	if got, want := major(m.SDK), major(plugin.SDKVersion); got != want {
		fail("built with SDK %q; this server takes %s.x", m.SDK, want)
	}

	// Nodes and triggers are both types of the engine's: one name for one.
	types := map[string]bool{}

	for kind, list := range map[string][]plugin.NodeType{"node": m.Nodes, "trigger": m.Triggers} {
		for _, t := range list {
			switch {
			case !ownName.MatchString(t.Name):
				fail("%s %q: a letter, then letters, digits and dashes", kind, t.Name)
			case types[t.Name]:
				fail("%s %q: a node or trigger has that name already", kind, t.Name)
			}

			types[t.Name] = true
		}
	}

	unique := func(kind string, names []string) {
		seen := map[string]bool{}

		for _, n := range names {
			switch {
			case !ownName.MatchString(n):
				fail("%s %q: a letter, then letters, digits and dashes", kind, n)
			case seen[n]:
				fail("%s %q twice", kind, n)
			}

			seen[n] = true
		}
	}

	var creds, scenarios []string
	for _, c := range m.CredentialTypes {
		creds = append(creds, c.Name)
	}

	for _, s := range m.Scenarios {
		scenarios = append(scenarios, s.ID)
	}

	unique("credential type", creds)
	unique("scenario", scenarios)

	if len(errs) > 0 {
		return fmt.Errorf("its manifest: %w", errors.Join(errs...))
	}

	return nil
}

func major(version string) string {
	m := semver.FindStringSubmatch(version)
	if m == nil {
		return ""
	}

	return m[1]
}

// qualified is a plugin's name as the rest of Polythought knows it (D285).
func qualified(id, name string) string {
	return "plugin." + id + "." + name
}

// paramKinds are those a plugin's settings may be: not code, which runs in the sandbox.
var paramKinds = map[string]engine.ParamKind{
	"string": engine.ParamString, "number": engine.ParamNumber, "decimal": engine.ParamDecimal,
	"bool": engine.ParamBool, "list": engine.ParamList, "duration": engine.ParamDuration,
	"wallet": engine.ParamWallet, "tokens": engine.ParamTokens, "address": engine.ParamAddress,
	"table": engine.ParamTable, "credential": engine.ParamCredential,
}

var fieldKinds = map[string]engine.FieldKind{
	"text": engine.FieldText, "number": engine.FieldNumber, "decimal": engine.FieldDecimal, "bool": engine.FieldBool,
	"time": engine.FieldTime, "list": engine.FieldList, "object": engine.FieldObject, "any": engine.FieldAny,
}

// nodeType is a plugin's node or trigger as the engine's: named plugin.<id>.<name>, main
// its ports unless it says, and nothing a plugin cannot be, code or a node with memory.
// The registry checks the rest as it does a built-in's.
func nodeType(id string, t plugin.NodeType, trigger bool) (engine.NodeType, error) {
	out := engine.NodeType{
		Name: qualified(id, t.Name), Label: t.Label, Description: t.Description,
		Inputs: t.Inputs, Outputs: t.Outputs, OutputsParam: t.OutputsParam, OutputsColumn: t.OutputsColumn,
	}

	switch {
	case trigger && len(t.Inputs) > 0:
		return out, errors.New("a trigger takes no inputs")
	case !trigger && len(out.Inputs) == 0:
		out.Inputs = []string{engine.DefaultPort}
	}

	if len(out.Outputs) == 0 {
		out.Outputs = []string{engine.DefaultPort}
	}

	for _, p := range t.Params {
		spec, err := param(id, p, false)
		if err != nil {
			return out, err
		}

		out.Params = append(out.Params, spec)
	}

	if it := t.Items; it != nil {
		fields, err := fieldsOf(it.Fields)
		if err != nil {
			return out, err
		}

		out.Items = &engine.Items{Fields: fields, Passed: it.Passed, Picked: it.Picked, Split: it.Split}
	}

	return out, nil
}

func param(id string, p plugin.Param, cell bool) (engine.ParamSpec, error) {
	kind, ok := paramKinds[p.Kind]
	if !ok {
		return engine.ParamSpec{}, fmt.Errorf("setting %q: %q is not a kind a plugin's setting can be", p.Name, p.Kind)
	}

	spec := engine.ParamSpec{
		Name: p.Name, Label: p.Label, Description: p.Description, Kind: kind, Required: p.Required, Default: p.Default,
		Options: p.Options, Sensitive: p.Sensitive, Field: p.Field, Input: p.Input, Multiline: p.Multiline,
	}

	// A credential type of the plugin's own by its name; a built-in one by its full name.
	for _, c := range p.CredentialTypes {
		if !strings.Contains(c, ".") {
			c = qualified(id, c)
		}

		spec.CredentialTypes = append(spec.CredentialTypes, c)
	}

	for _, k := range p.FieldKinds {
		fk, ok := fieldKinds[k]
		if !ok {
			return spec, fmt.Errorf("setting %q: %q is not a field's kind", p.Name, k)
		}

		spec.FieldKinds = append(spec.FieldKinds, fk)
	}

	if cell && len(p.Columns) > 0 {
		return spec, fmt.Errorf("setting %q: a table's cell is no table", p.Name)
	}

	for _, c := range p.Columns {
		col, err := param(id, c, true)
		if err != nil {
			return spec, err
		}

		spec.Columns = append(spec.Columns, col)
	}

	return spec, nil
}

func fieldsOf(fields []plugin.Field) ([]engine.Field, error) {
	out := make([]engine.Field, 0, len(fields))

	for _, f := range fields {
		ef, err := fieldOf(f)
		if err != nil {
			return nil, err
		}

		out = append(out, ef)
	}

	return out, nil
}

func fieldOf(f plugin.Field) (engine.Field, error) {
	kind, ok := fieldKinds[f.Kind]
	if !ok {
		return engine.Field{}, fmt.Errorf("field %q: %q is not a field's kind", f.Name, f.Kind)
	}

	out := engine.Field{
		Name: f.Name, Kind: kind, Description: f.Description, Optional: f.Optional, Nullable: f.Nullable, Options: f.Options,
	}

	if f.Of != nil {
		of, err := fieldOf(*f.Of)
		if err != nil {
			return out, err
		}

		out.Of = &of
	}

	fields, err := fieldsOf(f.Fields)
	if err != nil {
		return out, err
	}

	if len(fields) > 0 {
		out.Fields = fields
	}

	return out, nil
}
