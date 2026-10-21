package engine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

var (
	textField    = engine.Field{Name: "question", Kind: engine.FieldText}
	decimalField = engine.Field{Name: "price", Kind: engine.FieldDecimal}
	outcomes     = engine.Field{Name: "outcomes", Kind: engine.FieldList, Of: &engine.Field{Kind: engine.FieldObject, Fields: []engine.Field{
		{Name: "label", Kind: engine.FieldText}, {Name: "price", Kind: engine.FieldDecimal},
	}}}
	numbers = []engine.FieldKind{engine.FieldNumber, engine.FieldDecimal}
)

// shapesRegistry: a trigger of markets, nodes that pass, pick, say nothing or have their
// own, and nodes whose params name fields.
func shapesRegistry(t *testing.T) *engine.Registry {
	t.Helper()

	r := engine.NewRegistry()
	in := mainPorts()

	require.NoError(t, r.RegisterTrigger(fakeTrigger{typ: engine.NodeType{
		Name: "t.markets", Outputs: in, Items: &engine.Items{Fields: []engine.Field{textField, decimalField, outcomes}},
	}}))

	for _, typ := range []engine.NodeType{
		{Name: "t.pass", Inputs: in, Outputs: in, Items: &engine.Items{Passed: true}},
		{Name: "t.add", Inputs: in, Outputs: in, Items: &engine.Items{Passed: true, Fields: []engine.Field{
			{Name: "price", Kind: engine.FieldText}, {Name: "copy", Kind: engine.FieldObject, Fields: []engine.Field{{Name: "dollars", Kind: engine.FieldDecimal}}},
		}}},
		{Name: "t.unknown", Inputs: in, Outputs: in},
		{Name: "t.own", Inputs: in, Outputs: in, Items: &engine.Items{Fields: []engine.Field{{Name: "price", Kind: engine.FieldNumber}, {Name: "n", Kind: engine.FieldNumber}}}},
		{
			Name: "t.pick", Inputs: in, Outputs: in, Items: &engine.Items{Picked: "fields"},
			Params: []engine.ParamSpec{{Name: "fields", Kind: engine.ParamList, Field: true}},
		},
		{
			Name: "t.split", Inputs: in, Outputs: in, Items: &engine.Items{Split: "list"},
			Params: []engine.ParamSpec{{Name: "list", Kind: engine.ParamString, Field: true}},
		},
		{
			Name: "t.compare", Inputs: in, Outputs: mainPorts("true", "false"), Items: &engine.Items{Passed: true},
			Params: []engine.ParamSpec{{Name: "field", Kind: engine.ParamString, Field: true, FieldKinds: numbers}},
		},
		{
			Name: "t.dedupe", Inputs: in, Outputs: in, Items: &engine.Items{Passed: true},
			Params: []engine.ParamSpec{{Name: "field", Kind: engine.ParamString, Field: true, Default: "id"}},
		},
	} {
		require.NoError(t, r.RegisterNode(fakeNode{typ: typ}))
	}

	return r
}

func chain(types ...string) domain.Workflow {
	w := domain.Workflow{Nodes: []domain.NodeSpec{{ID: "n0", Type: "t.markets"}}}

	for i, typ := range types {
		id := domain.NodeID("n" + string(rune('1'+i)))
		w.Nodes = append(w.Nodes, domain.NodeSpec{ID: id, Type: typ})
		w.Connections = append(w.Connections, domain.Connection{From: w.Nodes[i].ID, FromPort: engine.DefaultPort, To: id, ToPort: engine.DefaultPort})
	}

	return w
}

func names(s engine.Shape) []string {
	out := make([]string, len(s))
	for i, f := range s {
		out[i] = f.Name
	}

	return out
}

func TestReceived(t *testing.T) {
	t.Parallel()

	r := shapesRegistry(t)

	t.Run("through nodes that pass items on", func(t *testing.T) {
		got := r.Received(chain("t.pass", "t.compare", "t.pass"))

		assert.Nil(t, got["n0"], "a trigger receives nothing")
		assert.Equal(t, []string{"question", "price", "outcomes"}, names(got["n1"]))
		assert.Equal(t, got["n1"], got["n3"])
	})

	t.Run("passed on with fields added", func(t *testing.T) {
		got := r.Received(chain("t.add", "t.pass"))["n2"]

		assert.Equal(t, []string{"question", "outcomes", "price", "copy"}, names(got), "the added replace their namesakes")
		assert.Equal(t, engine.FieldText, got[2].Kind)

		assert.Nil(t, r.Received(chain("t.unknown", "t.add", "t.pass"))["n3"], "added to what nobody knows: still unknown")
	})

	t.Run("their own, or nobody knows", func(t *testing.T) {
		got := r.Received(chain("t.own", "t.unknown", "t.pass"))

		assert.Equal(t, []string{"price", "n"}, names(got["n2"]))
		assert.Nil(t, got["n3"], "after a node that does not say")
	})

	t.Run("picked", func(t *testing.T) {
		w := chain("t.pick", "t.pass")
		w.Nodes[1].Params = map[string]any{"fields": []any{"question", "outcomes.0.price", "ghost"}}

		got := r.Received(w)["n2"]
		assert.Equal(t, []engine.Field{
			{Name: "question", Kind: engine.FieldText},
			{Name: "outcomes.0.price", Kind: engine.FieldDecimal, Optional: true},
			{Name: "ghost", Kind: engine.FieldAny, Optional: true},
		}, []engine.Field(got))
	})

	t.Run("split: an entry's fields at the top, winning a clash", func(t *testing.T) {
		w := chain("t.split", "t.pass")
		w.Nodes[1].Params = map[string]any{"list": "outcomes"}

		got := r.Received(w)["n2"]
		assert.Equal(t, []engine.Field{
			textField, {Name: "label", Kind: engine.FieldText}, {Name: "price", Kind: engine.FieldDecimal},
		}, []engine.Field(got), "the market's fields but the list, and the outcome's")

		w.Nodes[1].Params = map[string]any{"list": "question"}
		assert.Nil(t, r.Received(w)["n2"], "not a list: nobody knows")
	})

	t.Run("split: an entry that is no object takes the list's place", func(t *testing.T) {
		require.NoError(t, r.RegisterTrigger(fakeTrigger{typ: engine.NodeType{
			Name: "t.tokens", Outputs: mainPorts(), Items: &engine.Items{Fields: []engine.Field{
				textField, {Name: "tokens", Kind: engine.FieldList, Of: &engine.Field{Kind: engine.FieldText}},
			}},
		}}))

		w := chain("t.split", "t.pass")
		w.Nodes[0].Type = "t.tokens"
		w.Nodes[1].Params = map[string]any{"list": "tokens"}

		assert.Equal(t, []engine.Field{textField, {Name: "tokens", Kind: engine.FieldText}}, []engine.Field(r.Received(w)["n2"]))
	})

	t.Run("failed items are the ones received", func(t *testing.T) {
		w := chain("t.own", "t.pass")
		w.Connections[1].FromPort = engine.ErrorPort

		assert.Equal(t, []string{"question", "price", "outcomes"}, names(r.Received(w)["n2"]))
	})

	t.Run("where connections meet", func(t *testing.T) {
		w := chain("t.own", "t.pass")
		w.Connections = append(w.Connections, domain.Connection{From: "n0", FromPort: engine.DefaultPort, To: "n2", ToPort: engine.DefaultPort})

		got := r.Received(w)["n2"]
		assert.Equal(t, []engine.Field{
			{Name: "price", Kind: engine.FieldAny}, // a number one way, a decimal the other
			{Name: "n", Kind: engine.FieldNumber, Optional: true},
			{Name: "question", Kind: engine.FieldText, Optional: true},
			outcomesOptional(),
		}, []engine.Field(got))

		w.Connections = append(w.Connections, domain.Connection{From: "n0", FromPort: engine.DefaultPort, To: "n9", ToPort: engine.DefaultPort})
		w.Nodes = append(w.Nodes, domain.NodeSpec{ID: "n9", Type: "t.unknown"})
		w.Connections = append(w.Connections, domain.Connection{From: "n9", FromPort: engine.DefaultPort, To: "n2", ToPort: engine.DefaultPort})
		assert.Nil(t, r.Received(w)["n2"], "one unknown makes the whole unknown")
	})
}

func outcomesOptional() engine.Field {
	f := outcomes
	f.Optional = true

	return f
}

func TestShapeLookupAndPaths(t *testing.T) {
	t.Parallel()

	s := engine.Shape{textField, outcomes, {Name: "extra", Kind: engine.FieldAny}}

	for path, want := range map[string]engine.FieldKind{
		"question":         engine.FieldText,
		"outcomes":         engine.FieldList,
		"outcomes.0":       engine.FieldObject,
		"outcomes.1.price": engine.FieldDecimal,
		"extra.whatever.2": engine.FieldAny,
	} {
		f, ok := s.Lookup(path)
		assert.True(t, ok, path)
		assert.Equal(t, want, f.Kind, path)
	}

	for _, path := range []string{"ghost", "outcomes.first", "outcomes.0.ghost", "question.length"} {
		_, ok := s.Lookup(path)
		assert.False(t, ok, path)
	}

	assert.Equal(t, []string{"question", "outcomes", "outcomes.0.label", "outcomes.0.price", "extra"}, s.Paths())
}

func TestCheckFields(t *testing.T) {
	t.Parallel()

	r := shapesRegistry(t)
	with := func(w domain.Workflow, node int, params map[string]any) domain.Workflow {
		w.Nodes[node].Params = params
		return w
	}

	tests := []struct {
		name string
		w    domain.Workflow
		want []string // problems; none means it passes
	}{
		{name: "a field there", w: with(chain("t.compare"), 1, map[string]any{"field": "price"})},
		{name: "through a list", w: with(chain("t.compare"), 1, map[string]any{"field": "outcomes.0.price"})},
		{
			name: "a typo, and what was meant",
			w:    with(chain("t.compare"), 1, map[string]any{"field": "Price"}),
			want: []string{`node "n1": param "field": the items it receives have no field "Price"; "price"?`},
		},
		{
			name: "a near miss in a path",
			w:    with(chain("t.compare"), 1, map[string]any{"field": "outcomes.0.prise"}),
			want: []string{`no field "outcomes.0.prise"; "outcomes.0.price"?`},
		},
		{
			name: "nothing like it",
			w:    with(chain("t.compare"), 1, map[string]any{"field": "volume"}),
			want: []string{`node "n1": param "field": the items it receives have no field "volume"`},
		},
		{
			name: "the wrong kind",
			w:    with(chain("t.compare"), 1, map[string]any{"field": "question"}),
			want: []string{`node "n1": param "field": field "question" is text, and this takes number or decimal`},
		},
		{
			name: "each of a list",
			w:    with(chain("t.pick"), 1, map[string]any{"fields": []any{"question", "spread", "outcomes.0.label"}}),
			want: []string{`no field "spread"`},
		},
		{
			name: "a default that does not fit",
			w:    chain("t.dedupe"),
			want: []string{`node "n1": param "field": the items it receives have no field "id"`},
		},
		{name: "after a node that does not say, nothing is checked", w: with(chain("t.unknown", "t.compare"), 2, map[string]any{"field": "anything"})},
		{name: "a field of any kind passes", w: meeting(with(chain("t.own", "t.compare"), 2, map[string]any{"field": "price"}))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := r.CheckFields(tt.w)
			if tt.want == nil {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, domain.ErrInvalidWorkflow)
			require.ErrorIs(t, err, engine.ErrInvalidParam)

			for _, w := range tt.want {
				assert.Contains(t, err.Error(), w)
			}

			var v *domain.ValidationError
			require.ErrorAs(t, err, &v)
			assert.Len(t, v.Problems, len(tt.want))
			assert.NotEmpty(t, v.Problems[0].Param, "shown on the field")
		})
	}
}

// meeting connects the trigger straight to the last node too, where its price, a decimal,
// meets t.own's, a number: a field of any kind.
func meeting(w domain.Workflow) domain.Workflow {
	last := w.Nodes[len(w.Nodes)-1].ID
	w.Connections = append(w.Connections, domain.Connection{From: "n0", FromPort: engine.DefaultPort, To: last, ToPort: engine.DefaultPort})

	return w
}

func TestRegisterFieldParams(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		spec engine.ParamSpec
		want string
	}{
		{engine.ParamSpec{Name: "f", Kind: engine.ParamNumber, Field: true}, "names a field, so it is text or a list"},
		{engine.ParamSpec{Name: "f", Kind: engine.ParamString, FieldKinds: numbers}, "has field kinds but names no field"},
		{engine.ParamSpec{Name: "f", Kind: engine.ParamString, Field: true, FieldKinds: []engine.FieldKind{"float"}}, `no field kind "float"`},
	} {
		err := engine.NewRegistry().RegisterNode(fakeNode{typ: engine.NodeType{Name: "t.x", Inputs: mainPorts(), Params: []engine.ParamSpec{tc.spec}}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), tc.want)
	}
}

func TestMergedShapes(t *testing.T) {
	r := shapesRegistry(t)
	ab := []string{"a", "b"}

	require.NoError(t, r.RegisterNode(fakeNode{typ: engine.NodeType{
		Name: "t.merge", Inputs: ab, Outputs: mainPorts(), Items: &engine.Items{Merged: "mode", JoinedAs: "as"},
		Params: []engine.ParamSpec{
			{Name: "mode", Kind: engine.ParamString, Default: engine.MergeBoth},
			{Name: "field", Kind: engine.ParamString, Field: true, Input: "a"},
			{Name: "fieldB", Kind: engine.ParamString, Field: true, Input: "b"},
			{Name: "as", Kind: engine.ParamString, Default: "b"},
		},
	}}))

	bad := engine.NodeType{
		Name: "t.badinput", Inputs: mainPorts(), Outputs: mainPorts(),
		Params: []engine.ParamSpec{{Name: "f", Kind: engine.ParamString, Field: true, Input: "b"}},
	}
	require.ErrorContains(t, r.RegisterNode(fakeNode{typ: bad}), `names a field of input "b", which it does not have`)

	// Markets into a, their own items into b.
	w := func(params map[string]any) domain.Workflow {
		return domain.Workflow{
			Nodes: []domain.NodeSpec{
				{ID: "m", Type: "t.markets"}, {ID: "own", Type: "t.own"},
				{ID: "x", Type: "t.merge", Params: params}, {ID: "after", Type: "t.pass"},
			},
			Connections: []domain.Connection{
				{From: "m", FromPort: engine.DefaultPort, To: "own", ToPort: engine.DefaultPort},
				{From: "m", FromPort: engine.DefaultPort, To: "x", ToPort: "a"},
				{From: "own", FromPort: engine.DefaultPort, To: "x", ToPort: "b"},
				{From: "x", FromPort: engine.DefaultPort, To: "after", ToPort: engine.DefaultPort},
			},
		}
	}

	ports := r.ReceivedPorts(w(nil))
	assert.Equal(t, []string{"question", "price", "outcomes"}, names(ports["x"]["a"]))
	assert.Equal(t, []string{"price", "n"}, names(ports["x"]["b"]))
	assert.Equal(t, []string{"question", "price", "outcomes"}, names(ports["after"][engine.DefaultPort]), "both: A's")

	either := r.ReceivedPorts(w(map[string]any{"mode": engine.MergeEither}))["after"][engine.DefaultPort]
	assert.Equal(t, []string{"question", "price", "outcomes", "n"}, names(either), "A's or B's")
	assert.True(t, either[3].Optional)

	joined := r.Received(w(map[string]any{"mode": engine.MergeJoin, "as": "theirs"}))["after"]
	require.Len(t, joined, 4)
	assert.Equal(t, engine.Field{Name: "theirs", Kind: engine.FieldObject, Fields: []engine.Field{
		{Name: "price", Kind: engine.FieldNumber}, {Name: "n", Kind: engine.FieldNumber},
	}}, joined[3], "B's item under its own field")

	t.Run("each field setting is checked against its own input", func(t *testing.T) {
		require.NoError(t, r.CheckFields(w(map[string]any{"field": "question", "fieldB": "n"})))

		err := r.CheckFields(w(map[string]any{"field": "n", "fieldB": "question"}))
		require.ErrorContains(t, err, `param "field": the items it receives have no field "n"`)
		require.ErrorContains(t, err, `param "fieldB": the items it receives have no field "question"`)
	})
}

// An answer goes under the field its setting names; taken together, the item is the
// answer alone (D253, D256).
func TestNamedAndAloneShapes(t *testing.T) {
	r := shapesRegistry(t)

	require.NoError(t, r.RegisterNode(fakeNode{typ: engine.NodeType{
		Name: "t.ask", Inputs: mainPorts(), Outputs: mainPorts(),
		Items: &engine.Items{
			Passed: true, Named: "field", Alone: engine.Choice{Param: "call", Value: "together"},
			Fields: []engine.Field{{Name: "answer", Kind: engine.FieldObject, Fields: []engine.Field{{Name: "text", Kind: engine.FieldText}}}},
		},
		Params: []engine.ParamSpec{
			{Name: "field", Kind: engine.ParamString, Default: "answer"},
			{Name: "call", Kind: engine.ParamString, Default: "each", Options: []string{"each", "together"}},
		},
	}}))

	after := func(params map[string]any) engine.Shape {
		return r.Received(domain.Workflow{
			Nodes: []domain.NodeSpec{{ID: "m", Type: "t.markets"}, {ID: "x", Type: "t.ask", Params: params}, {ID: "after", Type: "t.pass"}},
			Connections: []domain.Connection{
				{From: "m", FromPort: engine.DefaultPort, To: "x", ToPort: engine.DefaultPort},
				{From: "x", FromPort: engine.DefaultPort, To: "after", ToPort: engine.DefaultPort},
			},
		})["after"]
	}

	assert.Equal(t, []string{"question", "price", "outcomes", "answer"}, names(after(nil)), "each item, with its answer")
	assert.Equal(t, []string{"question", "price", "outcomes", "verdict"}, names(after(map[string]any{"field": "verdict"})))
	assert.Equal(t, []string{"answer"}, names(after(map[string]any{"call": "together"})), "one item, the answer alone")
	assert.Nil(t, after(map[string]any{"field": "{{ params.where }}"}), "a name not known yet")
	assert.Nil(t, after(map[string]any{"call": "{{ params.how }}"}), "either way")

	f, ok := after(map[string]any{"field": "verdict"}).Lookup("verdict.text")
	require.True(t, ok)
	assert.Equal(t, engine.FieldText, f.Kind)
}
