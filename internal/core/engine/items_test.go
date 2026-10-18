package engine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

func TestRegisterItems(t *testing.T) {
	t.Parallel()

	text := func(name string) engine.Field { return engine.Field{Name: name, Kind: engine.FieldText} }
	node := func(items engine.Items) engine.NodeType {
		return engine.NodeType{
			Name: "test.items", Inputs: mainPorts(), Outputs: mainPorts(), Items: &items,
			Params: []engine.ParamSpec{
				{Name: "fields", Kind: engine.ParamList}, {Name: "one", Kind: engine.ParamString},
				{Name: "list", Kind: engine.ParamString, Field: true},
			},
		}
	}

	tests := []struct {
		name    string
		typ     engine.NodeType
		wantErr string
	}{
		{name: "unknown", typ: engine.NodeType{Name: "test.items", Inputs: mainPorts(), Outputs: mainPorts()}},
		{name: "passed", typ: node(engine.Items{Passed: true})},
		{name: "picked", typ: node(engine.Items{Picked: "fields"})},
		{
			name: "fields, nested",
			typ: node(engine.Items{Fields: []engine.Field{
				text("question"),
				{Name: "side", Kind: engine.FieldText, Options: []string{"buy", "sell"}},
				{Name: "outcomes", Kind: engine.FieldList, Of: &engine.Field{Kind: engine.FieldObject, Fields: []engine.Field{text("label")}}},
			}}),
		},
		{name: "split", typ: node(engine.Items{Split: "list"})},
		{name: "both kinds", typ: node(engine.Items{Passed: true, Picked: "fields"}), wantErr: "passed whole, picked or split: one of them"},
		{name: "split and picked", typ: node(engine.Items{Split: "list", Picked: "fields"}), wantErr: "one of them"},
		{name: "split and own", typ: node(engine.Items{Split: "list", Fields: []engine.Field{text("x")}}), wantErr: "not both"},
		{name: "split by no field", typ: node(engine.Items{Split: "one"}), wantErr: `split by "one", which is not a field param`},
		{name: "received, with fields added", typ: node(engine.Items{Passed: true, Fields: []engine.Field{text("x")}})},
		{name: "picked and own", typ: node(engine.Items{Picked: "fields", Fields: []engine.Field{text("x")}}), wantErr: "not both"},
		{
			name: "a trigger cannot pass on",
			typ:  engine.NodeType{Name: "test.items", Outputs: mainPorts(), Items: &engine.Items{Passed: true}}, wantErr: "receives no items",
		},
		{name: "picked by no list", typ: node(engine.Items{Picked: "one"}), wantErr: `picked by "one", which is not a list param`},
		{name: "unnamed", typ: node(engine.Items{Fields: []engine.Field{text("")}}), wantErr: "has no name"},
		{name: "twice", typ: node(engine.Items{Fields: []engine.Field{text("a"), text("a")}}), wantErr: `field "a" twice`},
		{
			name:    "a list without elements",
			typ:     node(engine.Items{Fields: []engine.Field{{Name: "xs", Kind: engine.FieldList}}}),
			wantErr: `list "xs" needs its elements`,
		},
		{
			name:    "an object without fields",
			typ:     node(engine.Items{Fields: []engine.Field{{Name: "o", Kind: engine.FieldObject}}}),
			wantErr: `object "o" needs fields`,
		},
		{
			name: "deep inside",
			typ: node(engine.Items{Fields: []engine.Field{{
				Name: "xs", Kind: engine.FieldList, Of: &engine.Field{Kind: engine.FieldObject, Fields: []engine.Field{{Name: "n", Kind: "float"}}},
			}}}),
			wantErr: `field "xs[].n" has no kind "float"`,
		},
		{
			name:    "options on a number",
			typ:     node(engine.Items{Fields: []engine.Field{{Name: "n", Kind: engine.FieldNumber, Options: []string{"1"}}}}),
			wantErr: "has options but is not text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := engine.NewRegistry().RegisterNode(fakeNode{typ: tt.typ})
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
