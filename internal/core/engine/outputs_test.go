package engine_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

// routeType has a fixed "timeout" port and one per value of its "routes" param.
func routeType() engine.NodeType {
	return engine.NodeType{
		Name: "test.route", Inputs: mainPorts(), Outputs: mainPorts("timeout"),
		Params:       []engine.ParamSpec{{Name: "routes", Kind: engine.ParamList, Default: []string{"Yes", "No"}}},
		OutputsParam: "routes",
	}
}

func TestOutputsOf(t *testing.T) {
	t.Parallel()

	typ := routeType()

	assert.Equal(t, []string{"timeout", "Yes", "No"}, typ.OutputsOf(nil), "the default")
	assert.Equal(t, []string{"timeout", "Buy"}, typ.OutputsOf(map[string]any{"routes": []any{"Buy"}}))
	assert.Equal(t, []string{"timeout"}, typ.OutputsOf(map[string]any{"routes": "Buy"}), "not a list")
	assert.Equal(t, []string{"timeout"}, typ.OutputsOf(map[string]any{"routes": []any{}}))

	fixed := engine.NodeType{Name: "test.fixed", Outputs: mainPorts()}
	assert.Equal(t, []string{engine.DefaultPort}, fixed.OutputsOf(map[string]any{"routes": []any{"Buy"}}))
}

func TestRegisterOutputsParam(t *testing.T) {
	t.Parallel()

	r := engine.NewRegistry()
	require.NoError(t, r.RegisterNode(fakeNode{typ: routeType()}))

	missing := routeType()
	missing.Name, missing.OutputsParam = "test.missing", "ghost"
	require.ErrorContains(t, r.RegisterNode(fakeNode{typ: missing}), `outputs param must name a list param, not "ghost"`)

	notList := routeType()
	notList.Name = "test.string"
	notList.Params = []engine.ParamSpec{{Name: "routes", Kind: engine.ParamString}}
	require.ErrorContains(t, r.RegisterNode(fakeNode{typ: notList}), "must name a list param")
}

func TestValidateParamPorts(t *testing.T) {
	t.Parallel()

	route := func(routes any, from string) domain.Workflow {
		params := map[string]any{}
		if routes != nil {
			params["routes"] = routes
		}

		return domain.Workflow{
			Nodes: []domain.NodeSpec{trigger(), spec("r", "test.route", params), spec("a", "test.pass", nil)},
			Connections: []domain.Connection{
				mainLink("trigger", "r"), link("r", from, "a", engine.DefaultPort),
			},
		}
	}

	tests := []struct {
		name     string
		workflow domain.Workflow
		want     string // a problem, empty if valid
	}{
		{name: "a port from the default", workflow: route(nil, "No")},
		{name: "a port from the param", workflow: route([]any{"Buy", "Skip"}, "Skip")},
		{name: "the fixed port", workflow: route([]any{"Buy"}, "timeout")},
		{
			name: "a value no longer there", workflow: route([]any{"Buy"}, "Yes"),
			want: `node "r" (test.route) has no output port "Yes"`,
		},
		{name: "no values", workflow: route([]any{}, "timeout"), want: `param "routes" needs at least one value`},
		{
			name: "too many", workflow: route(strings.Split("a b c d e f g h i j k", " "), "a"),
			want: `param "routes" has 11 values, more than the 10 allowed`,
		},
		{name: "empty", workflow: route([]any{"Buy", " "}, "Buy"), want: `param "routes" has an empty value`},
		{name: "twice", workflow: route([]any{"Buy", "Buy"}, "Buy"), want: `param "routes" has "Buy" twice`},
		{
			name: "too long", workflow: route([]any{strings.Repeat("é", 41)}, "timeout"),
			want: "is longer than 40 characters",
		},
		{
			name: "a fixed port's name", workflow: route([]any{"timeout"}, "timeout"),
			want: `param "routes" cannot use "timeout", the name of another port`,
		},
		{name: "the error port", workflow: route([]any{"error"}, "timeout"), want: `cannot use "error"`},
		{name: "not a list", workflow: route("Buy", "timeout"), want: `param "routes" must be a list of strings`},
	}

	r := testRegistry(t)
	require.NoError(t, r.RegisterNode(fakeNode{typ: routeType()}))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := r.Validate(tt.workflow)
			if tt.want == "" {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, domain.ErrInvalidWorkflow)
			assert.Contains(t, err.Error(), tt.want)

			var v *domain.ValidationError
			require.ErrorAs(t, err, &v)

			if !strings.Contains(tt.want, "has no output port") {
				assert.Equal(t, "routes", v.Problems[0].Param, "shown on the field")
			}
		})
	}
}
