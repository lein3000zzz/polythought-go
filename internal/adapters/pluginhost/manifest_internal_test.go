package pluginhost

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/pkg/plugin"
)

func good() plugin.Manifest {
	return plugin.Manifest{
		ID: "weather", Version: "1.0.0", Name: "Weather", SDK: plugin.SDKVersion,
		Nodes:           []plugin.NodeType{{Name: "forecast", Trades: true}},
		Triggers:        []plugin.NodeType{{Name: "storm"}},
		CredentialTypes: []plugin.CredentialType{{Name: "key", Label: "Key"}},
		Scenarios:       []plugin.Scenario{{ID: "rain"}},
		Permissions: []string{
			"fetch:api.weather.example", "fetch:203.0.113.7", "fetch:2001:db8::1", "fetch:*", "wallets", "telegram", "orders",
		},
		Settings: []plugin.Setting{
			{Key: "api_key", Label: "API key", Kind: "string", Secret: true},
			{Key: "units", Label: "Units", Kind: "enum", Options: []string{"celsius", "kelvin"}, Default: "celsius"},
			{Key: "days", Label: "Days ahead", Kind: "int", Default: json.Number("3")},
			{Key: "every", Label: "Every", Kind: "duration", Default: "1h"},
			{Key: "cities", Label: "Cities", Kind: "list", Default: []any{"Paris"}},
			{Key: "metric", Label: "Metric", Kind: "bool", Default: true},
		},
		Metrics: []plugin.Metric{
			{Name: "forecasts_total", Kind: "counter", Help: "Forecasts made.", Labels: []string{"city"}},
			{Name: "rain_mm", Kind: "histogram", Help: "Rain forecast.", Buckets: []float64{1, 5, 20}},
		},
	}
}

func TestPermissions(t *testing.T) {
	p, err := permissionsOf([]string{"fetch:api.weather.example", "fetch:203.0.113.7"})
	require.NoError(t, err)

	for host, want := range map[string]bool{
		"api.weather.example":    true,
		"API.Weather.Example.":   true, // as a URL may write it
		"203.0.113.7":            true,
		"weather.example":        false,
		"x.api.weather.example":  false, // not its subdomains
		"api.weather.example.co": false,
		"":                       false,
	} {
		assert.Equal(t, want, p.fetches(host), host)
	}

	anyHost, err := permissionsOf([]string{"fetch:*"})
	require.NoError(t, err)
	assert.True(t, anyHost.fetches("anything.example"))

	none, err := permissionsOf(nil)
	require.NoError(t, err)
	assert.False(t, none.fetches("api.weather.example"), "no permission, no fetch")
}

func TestCheck(t *testing.T) {
	require.NoError(t, Check(good()))

	for name, change := range map[string]func(*plugin.Manifest){
		"an id in capitals":       func(m *plugin.Manifest) { m.ID = "Weather" },
		"an id with a dot":        func(m *plugin.Manifest) { m.ID = "we.ather" },
		"no version":              func(m *plugin.Manifest) { m.Version = "" },
		"a version not semver":    func(m *plugin.Manifest) { m.Version = "v1" },
		"no name":                 func(m *plugin.Manifest) { m.Name = " " },
		"another SDK major":       func(m *plugin.Manifest) { m.SDK = "2.0.0" },
		"no SDK":                  func(m *plugin.Manifest) { m.SDK = "" },
		"a node with a dot":       func(m *plugin.Manifest) { m.Nodes[0].Name = "fore.cast" },
		"a trigger named a node":  func(m *plugin.Manifest) { m.Triggers[0].Name = "forecast" },
		"a credential type twice": func(m *plugin.Manifest) { m.CredentialTypes = append(m.CredentialTypes, m.CredentialTypes[0]) },
		"a scenario unnamed":      func(m *plugin.Manifest) { m.Scenarios[0].ID = "" },
		"a permission unknown":    func(m *plugin.Manifest) { m.Permissions = []string{"network"} },
		"wallets misspelt":        func(m *plugin.Manifest) { m.Permissions = []string{"wallet"} },
		"a trigger that trades":   func(m *plugin.Manifest) { m.Triggers[0].Trades = true },
		"trades, no orders":       func(m *plugin.Manifest) { m.Nodes[0].Trades = true; m.Permissions = nil },
		"a setting's key":         func(m *plugin.Manifest) { m.Settings[0].Key = "API key" },
		"a setting twice":         func(m *plugin.Manifest) { m.Settings = append(m.Settings, m.Settings[0]) },
		"a setting's kind":        func(m *plugin.Manifest) { m.Settings[0].Kind = "password" },
		"an enum of nothing":      func(m *plugin.Manifest) { m.Settings[1].Options = nil },
		"a secret number":         func(m *plugin.Manifest) { m.Settings[2].Secret = true },
		"a default not its kind":  func(m *plugin.Manifest) { m.Settings[2].Default = "ten" },
		"an enum's default":       func(m *plugin.Manifest) { m.Settings[1].Default = "fahrenheit" },
		"a setting unlabelled":    func(m *plugin.Manifest) { m.Settings[0].Label = "" },
		"a fetch with no host":    func(m *plugin.Manifest) { m.Permissions = []string{"fetch:"} },
		"a fetch of a URL":        func(m *plugin.Manifest) { m.Permissions = []string{"fetch:https://api.example.com"} },
		"a fetch of subdomains":   func(m *plugin.Manifest) { m.Permissions = []string{"fetch:*.example.com"} },
		"a fetch in capitals":     func(m *plugin.Manifest) { m.Permissions = []string{"fetch:API.example.com"} },
		"a metric's name":         func(m *plugin.Manifest) { m.Metrics[0].Name = "rain-total" },
		"a metric twice":          func(m *plugin.Manifest) { m.Metrics = append(m.Metrics, m.Metrics[0]) },
		"a metric's kind":         func(m *plugin.Manifest) { m.Metrics[0].Kind = "gauge" },
		"a metric without help":   func(m *plugin.Manifest) { m.Metrics[0].Help = "" },
		"the host's label":        func(m *plugin.Manifest) { m.Metrics[0].Labels = []string{"plugin"} },
		"a label twice":           func(m *plugin.Manifest) { m.Metrics[0].Labels = []string{"city", "city"} },
		"a counter's buckets":     func(m *plugin.Manifest) { m.Metrics[0].Buckets = []float64{1} },
		"buckets going down":      func(m *plugin.Manifest) { m.Metrics[1].Buckets = []float64{5, 1} },
	} {
		m := good()
		change(&m)
		assert.Error(t, Check(m), name)
	}

	m := good()
	m.SDK = "1.9.3"
	assert.NoError(t, Check(m), "a newer minor of the same major")
}

func TestCheckText(t *testing.T) {
	nodes := func(n int) []plugin.NodeType {
		out := make([]plugin.NodeType, n)
		for i := range out {
			out[i] = plugin.NodeType{Name: fmt.Sprintf("n%d", i)}
		}

		return out
	}

	long := func(n int) string { return strings.Repeat("x", n) }

	m := good()
	m.Name = strings.Repeat("é", maxName-2) + "☔ "
	m.Description = long(maxDescription)
	m.Nodes[0].Label = "Prévisions ☔ " + long(maxTitle-13)
	m.Nodes[0].Description = "Line one.\nLine two, \u200fمطر\u200f too." // a description may break lines, and mark direction
	m.Nodes = append(m.Nodes, nodes(maxTypes-1)...)
	m.Scenarios[0].Name = "Rain watch"
	require.NoError(t, Check(m), "generous: as long, and as many, as any plugin needs")

	for _, tt := range []struct {
		name, want string
		change     func(*plugin.Manifest)
	}{
		{"a long name", "the name: up to 64 characters", func(m *plugin.Manifest) { m.Name = strings.Repeat("W", maxName+1) }},
		{"a name padded unseen (P2)", "the name: up to 64", func(m *plugin.Manifest) { m.Name = "Movers" + strings.Repeat("\u200b", 4090) }},
		{"a name turned around", "the name: no control or bidi characters", func(m *plugin.Manifest) { m.Name = "Weather \u202esredro" }},
		{"a name isolating", "the name: no control or bidi", func(m *plugin.Manifest) { m.Name = "Weather \u2067x\u2069" }},
		{"a name over two lines", "the name: no control or bidi", func(m *plugin.Manifest) { m.Name = "Weather\nPolythought" }},
		{"a name, a separator", "the name: no control or bidi", func(m *plugin.Manifest) { m.Name = "Weather\u2028Polythought" }},
		{"a long description", "the plugin: a description of up to 2000 characters", func(m *plugin.Manifest) { m.Description = long(maxDescription + 1) }},
		{"a node's long label", `node "forecast": a label of up to 100 characters`, func(m *plugin.Manifest) { m.Nodes[0].Label = long(maxTitle + 1) }},
		{"a node's label, a NUL", `node "forecast": a label without control or bidi characters`, func(m *plugin.Manifest) { m.Nodes[0].Label = "Fore\x00cast" }},
		{"a trigger's long description", `trigger "storm": a description of up to 2000`, func(m *plugin.Manifest) {
			m.Triggers[0].Description = long(maxDescription + 1)
		}},
		{"a setting's label turned", `setting "api_key": a label without control or bidi`, func(m *plugin.Manifest) { m.Settings[0].Label = "\u202eyek IPA" }},
		{"a metric's long help", `metric "forecasts_total": a description of up to 2000`, func(m *plugin.Manifest) { m.Metrics[0].Help = long(maxDescription + 1) }},
		{"a credential type's label", `credential type "key": a label without control or bidi`, func(m *plugin.Manifest) { m.CredentialTypes[0].Label = "Key\r" }},
		{"a credential field's label", `credential type "key": field "k": a label of up to 100`, func(m *plugin.Manifest) {
			m.CredentialTypes[0].Fields = []plugin.CredentialField{{Name: "k", Label: long(maxTitle + 1)}}
		}},
		{"a scenario's name", `scenario "rain": a label without control or bidi`, func(m *plugin.Manifest) { m.Scenarios[0].Name = "Rain\twatch" }},
		{"a scenario's summary", `scenario "rain": a description of up to 2000`, func(m *plugin.Manifest) { m.Scenarios[0].Summary = long(maxDescription + 1) }},
		{"a node setting's label", `node "forecast": setting "city": a label of up to 100`, func(m *plugin.Manifest) {
			m.Nodes[0].Params = []plugin.Param{{Name: "city", Kind: "string", Label: long(maxTitle + 1)}}
		}},
		{"a column's label turned", `node "forecast": setting "rows": setting "at": a label without control or bidi`, func(m *plugin.Manifest) {
			m.Nodes[0].Params = []plugin.Param{{Name: "rows", Kind: "table", Columns: []plugin.Param{{Name: "at", Kind: "string", Label: "\u200eAt"}}}}
		}},
		{"a field's long description", `node "forecast": field "days": field "": field "rain": a description of up to 2000`, func(m *plugin.Manifest) {
			m.Nodes[0].Items = &plugin.Items{Fields: []plugin.Field{{Name: "days", Kind: "list", Of: &plugin.Field{
				Kind: "object", Fields: []plugin.Field{{Name: "rain", Kind: "decimal", Description: long(maxDescription + 1)}},
			}}}}
		}},
		{"too many nodes", "more than 100 nodes", func(m *plugin.Manifest) { m.Nodes = nodes(maxTypes + 1) }},
		{"too many triggers", "more than 100 triggers", func(m *plugin.Manifest) { m.Triggers = nodes(maxTypes + 1); m.Nodes = nil }},
		{"too many settings of a node", `node "forecast": more than 64 settings`, func(m *plugin.Manifest) {
			for i := range maxParams + 1 {
				m.Nodes[0].Params = append(m.Nodes[0].Params, plugin.Param{Name: fmt.Sprintf("p%d", i), Kind: "string"})
			}
		}},
		{"too many credential types", "more than 32 credential types", func(m *plugin.Manifest) {
			m.CredentialTypes = nil
			for i := range maxCredentialTypes + 1 {
				m.CredentialTypes = append(m.CredentialTypes, plugin.CredentialType{Name: fmt.Sprintf("c%d", i)})
			}
		}},
		{"too many scenarios", "more than 32 scenarios", func(m *plugin.Manifest) {
			m.Scenarios = nil
			for i := range maxScenarios + 1 {
				m.Scenarios = append(m.Scenarios, plugin.Scenario{ID: fmt.Sprintf("s%d", i)})
			}
		}},
		{"a credential's default (P25)", `node "forecast": setting "key": a credential has no default`, func(m *plugin.Manifest) {
			m.Nodes[0].Params = []plugin.Param{{Name: "key", Kind: "credential", CredentialTypes: []string{"key"}, Default: "9b1c-alice"}}
		}},
	} {
		m := good()
		tt.change(&m)
		assert.ErrorContains(t, Check(m), tt.want, tt.name)
	}
}

func TestNodeType(t *testing.T) {
	typ, err := nodeType("weather", plugin.NodeType{
		Name: "forecast",
		Params: []plugin.Param{
			{Name: "city", Kind: "string", Required: true},
			{Name: "key", Kind: "credential", CredentialTypes: []string{"key", "http.header"}},
			{Name: "field", Kind: "string", Field: true, FieldKinds: []string{"number", "decimal"}},
			{Name: "rows", Kind: "table", Columns: []plugin.Param{{Name: "at", Kind: "string"}}},
		},
		Items: &plugin.Items{Passed: true, Fields: []plugin.Field{
			{Name: "days", Kind: "list", Of: &plugin.Field{Kind: "object", Fields: []plugin.Field{{Name: "rain", Kind: "decimal"}}}},
		}},
	}, false)
	require.NoError(t, err)

	assert.Equal(t, "plugin.weather.forecast", typ.Name)
	assert.Equal(t, []string{engine.DefaultPort}, typ.Inputs)
	assert.Equal(t, []string{engine.DefaultPort}, typ.Outputs)
	assert.Equal(t, []string{"plugin.weather.key", "http.header"}, typ.Params[1].CredentialTypes)
	assert.Equal(t, []engine.FieldKind{engine.FieldNumber, engine.FieldDecimal}, typ.Params[2].FieldKinds)
	assert.Equal(t, engine.ParamTable, typ.Params[3].Kind)
	assert.Equal(t, engine.FieldDecimal, typ.Items.Fields[0].Of.Fields[0].Kind)
	assert.False(t, typ.Code || typ.SeesItems, "nothing a plugin cannot be")
	assert.False(t, typ.Remembers, "unless it says")

	kept, err := nodeType("weather", plugin.NodeType{Name: "seen", Remembers: true}, false)
	require.NoError(t, err)
	assert.True(t, kept.Remembers, "its store shown as its memory")

	for name, n := range map[string]plugin.NodeType{
		"code":            {Name: "x", Params: []plugin.Param{{Name: "src", Kind: "code"}}},
		"an unknown kind": {Name: "x", Params: []plugin.Param{{Name: "y", Kind: "image"}}},
		"a field's kind":  {Name: "x", Params: []plugin.Param{{Name: "y", Kind: "string", Field: true, FieldKinds: []string{"blob"}}}},
		"a table in a table": {Name: "x", Params: []plugin.Param{{Name: "t", Kind: "table", Columns: []plugin.Param{
			{Name: "inner", Kind: "table", Columns: []plugin.Param{{Name: "a", Kind: "string"}}},
		}}}},
		"an item's kind": {Name: "x", Items: &plugin.Items{Fields: []plugin.Field{{Name: "a", Kind: "blob"}}}},
		"a credential's default": {Name: "x", Params: []plugin.Param{
			{Name: "key", Kind: "credential", CredentialTypes: []string{"key"}, Default: "9b1c-alice"},
		}},
	} {
		_, err := nodeType("weather", n, false)
		assert.Error(t, err, name)
	}

	_, err = nodeType("weather", plugin.NodeType{Name: "storm", Inputs: []string{"main"}}, true)
	require.EqualError(t, err, "a trigger takes no inputs")

	trig, err := nodeType("weather", plugin.NodeType{Name: "storm"}, true)
	require.NoError(t, err)
	assert.Empty(t, trig.Inputs)
	assert.Equal(t, []string{engine.DefaultPort}, trig.Outputs)
}
