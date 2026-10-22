package pluginhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/pkg/plugin"
)

// The kinds of metric a plugin may declare.
const (
	metricCounter   = "counter"
	metricHistogram = "histogram"
)

const (
	maxMetrics      = 32
	maxMetricLabels = 8
	maxBuckets      = 32
)

// What a manifest may bring (P2, P5): text an admin and users read, in characters, and how
// many of each thing, generous for any plugin, but no more than a page shows.
const (
	maxName             = 64   // the plugin's name, which marks its messages
	maxTitle            = 100  // a label, or a scenario's name
	maxDescription      = 2000 // a description, a scenario's summary, a metric's help
	maxTypes            = 100  // nodes, and triggers
	maxParams           = 64   // a node's settings, and a table's columns
	maxCredentialTypes  = 32
	maxCredentialFields = 32 // fields of a credential type
	maxScenarios        = 32
)

var (
	pluginID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	// metricName is a metric's or a label's name: Prometheus's, lowercase, no colons.
	metricName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	// ownName is a node's, trigger's, credential type's or scenario's within its plugin.
	ownName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]{0,63}$`)
	semver  = regexp.MustCompile(`^(\d+)\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
)

// Check refuses a manifest the host cannot take: an ID, version or name not in their
// form, names given twice, an SDK of another major version, or more, or longer, than
// checkText takes.
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

			switch {
			case t.Trades && kind == "trigger":
				fail("trigger %q: a trigger trades nothing, a node does", t.Name)
			case t.Trades && !slices.Contains(m.Permissions, plugin.PermOrders):
				fail("node %q trades, but the manifest asks for no %s permission", t.Name, plugin.PermOrders)
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

	if _, err := permissionsOf(m.Permissions); err != nil {
		errs = append(errs, err)
	}

	errs = append(errs, checkMetrics(m.Metrics)...)
	errs = append(errs, checkSettings(m.Settings)...)
	errs = append(errs, checkText(m)...)

	if len(errs) > 0 {
		return fmt.Errorf("its manifest: %w", errors.Join(errs...))
	}

	return nil
}

// checkMetrics refuses metrics the host would not register as asked: names not in form or
// given twice, kinds it does not have, labels it keeps for itself, buckets out of order.
func checkMetrics(list []plugin.Metric) []error {
	var errs []error

	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if len(list) > maxMetrics {
		fail("more than %d metrics", maxMetrics)
	}

	seen := map[string]bool{}

	for _, mt := range list {
		switch {
		case !metricName.MatchString(mt.Name):
			fail("metric %q: a-z, 0-9 and underscores, starting with a letter, up to 64", mt.Name)
		case seen[mt.Name]:
			fail("metric %q twice", mt.Name)
		}

		seen[mt.Name] = true

		if mt.Kind != metricCounter && mt.Kind != metricHistogram {
			fail("metric %q: kind %q is neither counter nor histogram", mt.Name, mt.Kind)
		}

		if strings.TrimSpace(mt.Help) == "" {
			fail("metric %q: help says what it counts", mt.Name)
		}

		if len(mt.Labels) > maxMetricLabels {
			fail("metric %q: more than %d labels", mt.Name, maxMetricLabels)
		}

		labels := map[string]bool{}

		for _, l := range mt.Labels {
			switch {
			case !metricName.MatchString(l):
				fail("metric %q: label %q: a-z, 0-9 and underscores, starting with a letter", mt.Name, l)
			case l == "plugin":
				fail("metric %q: the label plugin is the host's, the plugin's ID", mt.Name)
			case labels[l]:
				fail("metric %q: label %q twice", mt.Name, l)
			}

			labels[l] = true
		}

		switch {
		case len(mt.Buckets) > 0 && mt.Kind != metricHistogram:
			fail("metric %q: only a histogram has buckets", mt.Name)
		case len(mt.Buckets) > maxBuckets:
			fail("metric %q: more than %d buckets", mt.Name, maxBuckets)
		}

		for i, b := range mt.Buckets {
			if math.IsNaN(b) || math.IsInf(b, 0) || (i > 0 && b <= mt.Buckets[i-1]) {
				fail("metric %q: buckets go up, finite", mt.Name)
				break
			}
		}
	}

	return errs
}

// checkText refuses more of anything than the bounds above, text longer than they say,
// and in the name and labels, control and bidi characters, which could make them read as
// something else on the page that approves the plugin, or in a message (P2, P19). Nor may
// a credential setting have a default, which would pick a credential the owner did not
// (D289, P25).
func checkText(m plugin.Manifest) []error {
	var errs []error

	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	label := func(what, text string) {
		switch {
		case utf8.RuneCountInString(text) > maxTitle:
			fail("%s: a label of up to %d characters", what, maxTitle)
		case !plain(text):
			fail("%s: a label without control or bidi characters", what)
		}
	}

	description := func(what, text string) {
		if utf8.RuneCountInString(text) > maxDescription {
			fail("%s: a description of up to %d characters", what, maxDescription)
		}
	}

	switch {
	case utf8.RuneCountInString(m.Name) > maxName:
		fail("the name: up to %d characters", maxName)
	case !plain(m.Name):
		fail("the name: no control or bidi characters")
	}

	description("the plugin", m.Description)

	for kind, list := range map[string][]plugin.NodeType{"node": m.Nodes, "trigger": m.Triggers} {
		if len(list) > maxTypes {
			fail("more than %d %ss", maxTypes, kind)
		}

		for _, t := range list {
			what := fmt.Sprintf("%s %.64q", kind, t.Name)
			label(what, t.Label)
			description(what, t.Description)
			errs = append(errs, checkParams(what, t.Params, label, description)...)

			if t.Items != nil {
				checkFields(what, t.Items.Fields, description)
			}
		}
	}

	for _, st := range m.Settings {
		what := fmt.Sprintf("setting %.64q", st.Key)
		label(what, st.Label)
		description(what, st.Description)
	}

	for _, mt := range m.Metrics {
		description(fmt.Sprintf("metric %.64q", mt.Name), mt.Help)
	}

	if len(m.CredentialTypes) > maxCredentialTypes {
		fail("more than %d credential types", maxCredentialTypes)
	}

	for _, c := range m.CredentialTypes {
		what := fmt.Sprintf("credential type %.64q", c.Name)
		label(what, c.Label)
		description(what, c.Description)

		if len(c.Fields) > maxCredentialFields {
			fail("%s: more than %d fields", what, maxCredentialFields)
		}

		for _, f := range c.Fields {
			label(fmt.Sprintf("%s: field %.64q", what, f.Name), f.Label)
			description(fmt.Sprintf("%s: field %.64q", what, f.Name), f.Description)
		}
	}

	if len(m.Scenarios) > maxScenarios {
		fail("more than %d scenarios", maxScenarios)
	}

	for _, sc := range m.Scenarios {
		what := fmt.Sprintf("scenario %.64q", sc.ID)
		label(what, sc.Name)
		description(what, sc.Summary)
		description(what, sc.Description)
	}

	return errs
}

// checkParams refuses a node's settings past maxParams, a table's columns too, their text
// as checkText's, and a credential setting's default.
func checkParams(node string, params []plugin.Param, label, description func(what, text string)) []error {
	var errs []error

	if len(params) > maxParams {
		errs = append(errs, fmt.Errorf("%s: more than %d settings", node, maxParams))
	}

	for _, p := range params {
		what := fmt.Sprintf("%s: setting %.64q", node, p.Name)
		label(what, p.Label)
		description(what, p.Description)

		if p.Kind == "credential" && p.Default != nil {
			errs = append(errs, fmt.Errorf("%s: a credential has no default: the owner picks theirs", what))
		}

		errs = append(errs, checkParams(what, p.Columns, label, description)...)
	}

	return errs
}

// checkFields holds what items' fields say of themselves to description.
func checkFields(node string, fields []plugin.Field, description func(what, text string)) {
	for _, f := range fields {
		what := fmt.Sprintf("%s: field %.64q", node, f.Name)
		description(what, f.Description)

		if f.Of != nil {
			checkFields(what, []plugin.Field{*f.Of}, description)
		}

		checkFields(what, f.Fields, description)
	}
}

// plain says text has no control characters, line or paragraph separators, nor bidi
// controls, which reorder what follows them.
func plain(text string) bool {
	return !strings.ContainsFunc(text, func(r rune) bool {
		return unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp, unicode.Bidi_Control)
	})
}

// Same says whether two manifests describe the same plugin, word for word, whichever SDK
// they say they were built with: what its manifest.json says and its program describes,
// or what it described before and after a restart.
func Same(a, b plugin.Manifest) bool {
	a.SDK, b.SDK = "", ""

	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)

	return errA == nil && errB == nil && bytes.Equal(ra, rb)
}

// settingKinds are the kinds a plugin's own setting may be, as the server's are.
var settingKinds = []string{"string", "int", "bool", "duration", "enum", "list"}

const maxSettings = 32

// checkSettings refuses settings the host could not keep: keys not in form or given
// twice, kinds it has not, an enum with no options, a default not of its kind, a secret
// not text.
func checkSettings(list []plugin.Setting) []error {
	var errs []error

	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if len(list) > maxSettings {
		fail("more than %d settings", maxSettings)
	}

	seen := map[string]bool{}

	for _, st := range list {
		switch {
		case !metricName.MatchString(st.Key):
			fail("setting %q: a-z, 0-9 and underscores, starting with a letter, up to 64", st.Key)
		case seen[st.Key]:
			fail("setting %q twice", st.Key)
		}

		seen[st.Key] = true

		switch {
		case !slices.Contains(settingKinds, st.Kind):
			fail("setting %q: kind %q is none of %s", st.Key, st.Kind, strings.Join(settingKinds, ", "))
		case st.Kind == "enum" && len(st.Options) == 0:
			fail("setting %q: an enum lists its options", st.Key)
		case st.Secret && st.Kind != "string":
			fail("setting %q: only text is secret", st.Key)
		case strings.TrimSpace(st.Label) == "":
			fail("setting %q: a label says what it is", st.Key)
		}

		if st.Default != nil && !defaultOfKind(st) {
			fail("setting %q: its default is no %s", st.Key, st.Kind)
		}
	}

	return errs
}

// defaultOfKind says whether a setting's default, as JSON decoded it, is of its kind.
func defaultOfKind(st plugin.Setting) bool {
	switch st.Kind {
	case "string":
		_, ok := st.Default.(string)
		return ok
	case "int":
		n, ok := st.Default.(json.Number)
		if !ok {
			return false
		}

		_, err := n.Int64()

		return err == nil
	case "bool":
		_, ok := st.Default.(bool)
		return ok
	case "duration":
		text, ok := st.Default.(string)
		if !ok {
			return false
		}

		d, err := time.ParseDuration(text)

		return err == nil && d > 0
	case "enum":
		text, ok := st.Default.(string)
		return ok && slices.Contains(st.Options, text)
	case "list":
		items, ok := st.Default.([]any)
		if !ok {
			return false
		}

		for _, item := range items {
			if _, ok := item.(string); !ok {
				return false
			}
		}

		return true
	}

	return false
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
// its ports unless it says, and nothing a plugin cannot be, such as code. One that
// remembers keeps its data in the plugin store, shown as its memory (D302). The registry
// checks the rest as it does a built-in's.
func nodeType(id string, t plugin.NodeType, trigger bool) (engine.NodeType, error) {
	out := engine.NodeType{
		Name: qualified(id, t.Name), Label: t.Label, Description: t.Description,
		Inputs: t.Inputs, Outputs: t.Outputs, OutputsParam: t.OutputsParam, OutputsColumn: t.OutputsColumn,
		Remembers: t.Remembers, Plugin: id, Trades: t.Trades,
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

	if kind == engine.ParamCredential && p.Default != nil {
		return engine.ParamSpec{}, fmt.Errorf("setting %q: a credential has no default: the owner picks theirs (D289)", p.Name)
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
