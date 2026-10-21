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
	}
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
