package pluginhost

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/pkg/plugin"
)

func good() plugin.Manifest {
	return plugin.Manifest{
		ID: "weather", Version: "1.0.0", Name: "Weather", SDK: plugin.SDKVersion,
		Nodes:           []plugin.NodeType{{Name: "forecast"}},
		Triggers:        []plugin.NodeType{{Name: "storm"}},
		CredentialTypes: []plugin.CredentialType{{Name: "key", Label: "Key"}},
		Scenarios:       []plugin.Scenario{{ID: "rain"}},
		Permissions:     []string{"fetch:api.weather.example", "fetch:203.0.113.7", "fetch:2001:db8::1", "fetch:*"},
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
	assert.False(t, typ.Code || typ.Remembers || typ.SeesItems, "nothing a plugin cannot be")

	for name, n := range map[string]plugin.NodeType{
		"code":            {Name: "x", Params: []plugin.Param{{Name: "src", Kind: "code"}}},
		"an unknown kind": {Name: "x", Params: []plugin.Param{{Name: "y", Kind: "image"}}},
		"a field's kind":  {Name: "x", Params: []plugin.Param{{Name: "y", Kind: "string", Field: true, FieldKinds: []string{"blob"}}}},
		"a table in a table": {Name: "x", Params: []plugin.Param{{Name: "t", Kind: "table", Columns: []plugin.Param{
			{Name: "inner", Kind: "table", Columns: []plugin.Param{{Name: "a", Kind: "string"}}},
		}}}},
		"an item's kind": {Name: "x", Items: &plugin.Items{Fields: []plugin.Field{{Name: "a", Kind: "blob"}}}},
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
