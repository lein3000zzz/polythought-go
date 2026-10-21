package engine_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

func TestRegistryRegister(t *testing.T) {
	tests := []struct {
		name     string
		register func(r *engine.Registry) error
		wantErr  bool
	}{
		{
			name: "new node type",
			register: func(r *engine.Registry) error {
				return r.RegisterNode(fakeNode{typ: engine.NodeType{Name: "test.new"}})
			},
		},
		{
			name: "duplicate node type",
			register: func(r *engine.Registry) error {
				return r.RegisterNode(fakeNode{typ: engine.NodeType{Name: "test.pass"}})
			},
			wantErr: true,
		},
		{
			name: "trigger reusing a node type name",
			register: func(r *engine.Registry) error {
				return r.RegisterTrigger(fakeTrigger{typ: engine.NodeType{Name: "test.pass"}})
			},
			wantErr: true,
		},
		{
			name: "node reusing a trigger type name",
			register: func(r *engine.Registry) error {
				return r.RegisterNode(fakeNode{typ: engine.NodeType{Name: "test.trigger"}})
			},
			wantErr: true,
		},
		{
			name: "empty name",
			register: func(r *engine.Registry) error {
				return r.RegisterNode(fakeNode{})
			},
			wantErr: true,
		},
		{
			name:     "a table",
			register: withTable(engine.ParamSpec{Name: "label", Kind: engine.ParamString, Options: []string{"a", "b"}}),
		},
		{name: "a table without columns", register: withTable(), wantErr: true},
		{name: "a column of a kind cells do not hold", register: withTable(engine.ParamSpec{Name: "w", Kind: engine.ParamWallet}), wantErr: true},
		{name: "a column named twice", register: withTable(engine.ParamSpec{Name: "a", Kind: engine.ParamBool}, engine.ParamSpec{Name: "a", Kind: engine.ParamBool}), wantErr: true},
		{name: "an unnamed column", register: withTable(engine.ParamSpec{Kind: engine.ParamBool}), wantErr: true},
		{name: "choices of a number column", register: withTable(engine.ParamSpec{Name: "n", Kind: engine.ParamNumber, Options: []string{"1"}}), wantErr: true},
		{
			name: "columns on a param that is not a table",
			register: func(r *engine.Registry) error {
				return r.RegisterNode(fakeNode{typ: engine.NodeType{Name: "test.cols", Params: []engine.ParamSpec{
					{Name: "s", Kind: engine.ParamString, Columns: []engine.ParamSpec{{Name: "a", Kind: engine.ParamBool}}},
				}}})
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.register(testRegistry(t))

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// withTable registers a node type with a table of these columns.
func withTable(columns ...engine.ParamSpec) func(r *engine.Registry) error {
	return func(r *engine.Registry) error {
		return r.RegisterNode(fakeNode{typ: engine.NodeType{Name: "test.table", Params: []engine.ParamSpec{
			{Name: "rows", Kind: engine.ParamTable, Columns: columns},
		}}})
	}
}

func TestRegistryLookup(t *testing.T) {
	r := testRegistry(t)

	tests := []struct {
		name        string
		typ         string
		wantOK      bool
		wantTrigger bool
	}{
		{name: "node", typ: "test.pass", wantOK: true},
		{name: "trigger", typ: "test.trigger", wantOK: true, wantTrigger: true},
		{name: "unknown", typ: "test.ghost"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typ, trigger, ok := r.Type(tt.typ)

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantTrigger, trigger)

			if ok {
				assert.Equal(t, tt.typ, typ.Name)
			}
		})
	}
}

func TestRegistryValidate(t *testing.T) {
	tests := []struct {
		name         string
		workflow     domain.Workflow
		wantProblems []string // substrings, nil means valid
		wantErrIs    error
	}{
		{
			name: "valid",
			workflow: domain.Workflow{
				Nodes:       []domain.NodeSpec{trigger(), spec("if", "test.if", map[string]any{"threshold": "0.5"})},
				Connections: []domain.Connection{mainLink("trigger", "if")},
			},
		},
		{
			name: "structural problems come first",
			workflow: domain.Workflow{
				Nodes:       []domain.NodeSpec{trigger()},
				Connections: []domain.Connection{mainLink("trigger", "ghost")},
			},
			wantProblems: []string{`connection to unknown node "ghost"`},
		},
		{
			name:         "unknown node type",
			workflow:     domain.Workflow{Nodes: []domain.NodeSpec{trigger(), spec("x", "test.ghost", nil)}},
			wantProblems: []string{`node "x": unknown node type "test.ghost"`},
			wantErrIs:    engine.ErrUnknownNodeType,
		},
		{
			name:         "no trigger",
			workflow:     domain.Workflow{Nodes: []domain.NodeSpec{spec("a", "test.pass", nil)}},
			wantProblems: []string{"no trigger node"},
		},
		{
			name:         "missing required param",
			workflow:     domain.Workflow{Nodes: []domain.NodeSpec{trigger(), spec("if", "test.if", nil)}},
			wantProblems: []string{`node "if": missing required param "threshold"`},
		},
		{
			name: "unknown output port",
			workflow: domain.Workflow{
				Nodes:       []domain.NodeSpec{trigger(), spec("a", "test.pass", nil)},
				Connections: []domain.Connection{link("trigger", "other", "a", engine.DefaultPort)},
			},
			wantProblems: []string{`node "trigger" (test.trigger) has no output port "other"`},
		},
		{
			name: "unknown input port",
			workflow: domain.Workflow{
				Nodes:       []domain.NodeSpec{trigger(), spec("m", "test.merge", nil)},
				Connections: []domain.Connection{mainLink("trigger", "m")},
			},
			wantProblems: []string{`node "m" (test.merge) has no input port "main"`},
		},
		{
			name: "connections into a trigger are rejected by its ports",
			workflow: domain.Workflow{
				Nodes:       []domain.NodeSpec{trigger(), spec("a", "test.pass", nil)},
				Connections: []domain.Connection{mainLink("a", "trigger")},
			},
			wantProblems: []string{`node "trigger" (test.trigger) has no input port "main"`},
		},
		{
			name: "error port needs continueOnFail",
			workflow: domain.Workflow{
				Nodes:       []domain.NodeSpec{trigger(), spec("f", "test.fail", nil), spec("a", "test.pass", nil)},
				Connections: []domain.Connection{mainLink("trigger", "f"), link("f", engine.ErrorPort, "a", engine.DefaultPort)},
			},
			wantProblems: []string{`node "f": error port is connected but continueOnFail is off`},
		},
		{
			name:         "unknown param",
			workflow:     domain.Workflow{Nodes: []domain.NodeSpec{trigger(), spec("a", "test.pass", map[string]any{"tga": "x"})}},
			wantProblems: []string{`node "a": unknown param "tga"`},
		},
		{
			name:         "param of the wrong type",
			workflow:     domain.Workflow{Nodes: []domain.NodeSpec{trigger(), spec("if", "test.if", map[string]any{"threshold": "lots"})}},
			wantProblems: []string{`node "if": param "threshold" must be a number`},
			wantErrIs:    engine.ErrInvalidParam,
		},
		{
			name:     "an expression where a number goes",
			workflow: domain.Workflow{Nodes: []domain.NodeSpec{trigger(), spec("if", "test.if", map[string]any{"threshold": "{{ params.least }}"})}},
		},
		{
			name: "triggers work no expressions out",
			workflow: domain.Workflow{Nodes: []domain.NodeSpec{
				{ID: "trigger", Type: "test.trigger.timed", Params: map[string]any{"every": "{{ params.x }}"}},
			}},
			wantProblems: []string{`param "every" "{{ params.x }}" is not a positive duration`},
		},
		{
			name: "error port with continueOnFail",
			workflow: domain.Workflow{
				Nodes: []domain.NodeSpec{
					trigger(), {ID: "f", Type: "test.fail", ContinueOnFail: true}, spec("a", "test.pass", nil),
				},
				Connections: []domain.Connection{mainLink("trigger", "f"), link("f", engine.ErrorPort, "a", engine.DefaultPort)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := testRegistry(t).Validate(tt.workflow)

			if tt.wantProblems == nil {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, domain.ErrInvalidWorkflow)

			if tt.wantErrIs != nil {
				require.ErrorIs(t, err, tt.wantErrIs)
			}

			for _, p := range tt.wantProblems {
				assert.Contains(t, err.Error(), p)
			}
		})
	}
}

func TestCheckParams(t *testing.T) {
	typ := engine.NodeType{
		Name:    "test.check",
		Inputs:  mainPorts(),
		Outputs: mainPorts(),
		Params: []engine.ParamSpec{
			{Name: "op", Kind: engine.ParamString, Options: []string{"gt", "lt"}},
			{Name: "on", Kind: engine.ParamList, Options: []string{"trade", "book"}},
			{Name: "every", Kind: engine.ParamDuration},
			{Name: "flag", Kind: engine.ParamBool},
			{Name: "n", Kind: engine.ParamNumber},
			{Name: "d", Kind: engine.ParamDecimal},
			{Name: "wallet", Kind: engine.ParamWallet},
			{Name: "tokens", Kind: engine.ParamTokens},
			{Name: "key", Kind: engine.ParamCredential, CredentialTypes: []string{"test.key"}},
			{Name: "who", Kind: engine.ParamAddress},
			{Name: "traders", Kind: engine.ParamTable, Columns: []engine.ParamSpec{
				{Name: "label", Label: "Label", Kind: engine.ParamString, Required: true},
				{Name: "address", Label: "Address", Kind: engine.ParamAddress, Required: true},
				{Name: "on", Label: "On", Kind: engine.ParamBool, Default: true},
				{Name: "size", Label: "Size", Kind: engine.ParamDecimal},
				{Name: "side", Label: "Side", Kind: engine.ParamString, Options: []string{"buy", "sell"}},
			}},
		},
	}

	alice := "0x5f3c00000000000000000000000000000000a1b2"
	row := func(cells map[string]any) map[string]any {
		out := map[string]any{"label": "Alice", "address": alice}
		for k, v := range cells {
			out[k] = v
		}

		return out
	}

	tests := []struct {
		name    string
		params  map[string]any
		problem string // "" means valid
	}{
		{name: "an address", params: map[string]any{"who": alice}},
		{name: "not an address", params: map[string]any{"who": "0x5f3c"}, problem: `param "who" "0x5f3c" is not an address: 0x and 40 hex digits`},
		{
			name: "an address with a typo", params: map[string]any{"who": "0x5f3C00000000000000000000000000000000a1b2"},
			problem: `"0x5f3C00000000000000000000000000000000a1b2" has a typo: its mixed case does not match its checksum`,
		},
		{name: "a table", params: map[string]any{"traders": []any{
			row(nil), row(map[string]any{"label": "Bob", "on": false, "size": "0.5", "side": "buy"}),
		}}},
		{name: "a table with no rows", params: map[string]any{"traders": []any{}}},
		{name: "a cell left out for its default", params: map[string]any{"traders": []any{row(map[string]any{"on": nil})}}},
		{name: "not a table", params: map[string]any{"traders": "Alice"}, problem: `param "traders" must be a table: a list of rows`},
		{name: "a row that is not one", params: map[string]any{"traders": []any{"Alice"}}, problem: `param "traders" in row 1: must be a row of cells`},
		{name: "a required cell missing", params: map[string]any{"traders": []any{row(nil), map[string]any{"label": "Bob"}}}, problem: `param "traders" in row 2: Address is missing`},
		{name: "a cell of the wrong kind", params: map[string]any{"traders": []any{row(map[string]any{"size": "lots"})}}, problem: `param "traders" in row 1: Size must be a number`},
		{name: "a cell not an address", params: map[string]any{"traders": []any{row(map[string]any{"address": "alice.eth"})}}, problem: `param "traders" in row 1: Address "alice.eth" is not an address`},
		{name: "a choice outside the list", params: map[string]any{"traders": []any{row(map[string]any{"side": "hold"})}}, problem: `param "traders" in row 1: Side "hold" is not one of [buy sell]`},
		{name: "a cell with no column", params: map[string]any{"traders": []any{row(map[string]any{"colour": "red"})}}, problem: `param "traders" in row 1: there is no column "colour"`},
		{name: "a cell's {{ }} is text", params: map[string]any{"traders": []any{row(map[string]any{"label": "{{ name }}"})}}},
		{name: "too many rows", params: map[string]any{"traders": make([]any, engine.MaxRows+1)}, problem: `param "traders" has 501 rows, more than the 500 allowed`},
		{name: "valid values", params: map[string]any{
			"op": "gt", "on": []any{"trade"}, "every": "5s", "flag": true, "n": 3, "wallet": "w-1", "tokens": []any{"t1"},
			"key": "cred-1",
		}},
		{name: "json numbers count", params: map[string]any{"n": json.Number("3"), "d": json.Number("2.5")}},
		{name: "decimals may be numeric strings", params: map[string]any{"d": "0.02"}},
		{name: "fraction for a whole number", params: map[string]any{"n": json.Number("2.5")}, problem: `param "n" must be a whole number`},
		{name: "decimal as text", params: map[string]any{"d": "lots"}, problem: `param "d" must be a number`},
		{name: "option outside the list", params: map[string]any{"op": "eq"}, problem: `param "op" "eq" is not one of [gt lt]`},
		{name: "list element outside the options", params: map[string]any{"on": []any{"trade", "gossip"}}, problem: `param "on" "gossip" is not one of [trade book]`},
		{name: "not a duration", params: map[string]any{"every": "soon"}, problem: `param "every" "soon" is not a positive duration`},
		{name: "negative duration", params: map[string]any{"every": "-5s"}, problem: `param "every" "-5s" is not a positive duration`},
		{name: "bool as string", params: map[string]any{"flag": "yes"}, problem: `param "flag" must be true or false`},
		{name: "number as text", params: map[string]any{"n": "many"}, problem: `param "n" must be a whole number`},
		{name: "wallet as number", params: map[string]any{"wallet": 7}, problem: `param "wallet" must be a string`},
		{name: "tokens not a list", params: map[string]any{"tokens": "t1"}, problem: `param "tokens" must be a list of strings`},
		{name: "credential as number", params: map[string]any{"key": 7}, problem: `param "key" must be a string`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := testRegistry(t)
			require.NoError(t, r.RegisterCredentialType(keyType))
			require.NoError(t, r.RegisterNode(fakeNode{typ: typ}))

			err := r.Validate(domain.Workflow{
				Nodes:       []domain.NodeSpec{trigger(), spec("c", "test.check", tt.params)},
				Connections: []domain.Connection{mainLink("trigger", "c")},
			})

			if tt.problem == "" {
				require.NoError(t, err)
				return
			}

			var v *domain.ValidationError
			require.ErrorAs(t, err, &v)
			require.Len(t, v.Problems, 1)
			assert.Equal(t, domain.NodeID("c"), v.Problems[0].NodeID)
			assert.Contains(t, v.Problems[0].Message, tt.problem)
			assert.NotEmpty(t, v.Problems[0].Param, "the editor can point at the field")
		})
	}
}

var keyType = engine.CredentialType{
	Name: "test.key", Label: "Test key",
	Fields: []engine.CredentialField{{Name: "key", Label: "Key", Secret: true, Required: true}},
}

func TestRegistryCredentialTypes(t *testing.T) {
	user := func(credTypes ...string) fakeNode {
		return fakeNode{typ: engine.NodeType{
			Name:   "test.user",
			Params: []engine.ParamSpec{{Name: "key", Kind: engine.ParamCredential, CredentialTypes: credTypes}},
		}}
	}

	other := engine.CredentialType{Name: "test.other", Label: "Other key", Fields: keyType.Fields}

	tests := []struct {
		name     string
		register func(r *engine.Registry) error
		wantErr  string
	}{
		{name: "a type", register: func(r *engine.Registry) error { return r.RegisterCredentialType(keyType) }},
		{
			name: "a type twice",
			register: func(r *engine.Registry) error {
				_ = r.RegisterCredentialType(keyType)
				return r.RegisterCredentialType(keyType)
			},
			wantErr: "already registered",
		},
		{
			name: "a type without fields",
			register: func(r *engine.Registry) error {
				return r.RegisterCredentialType(engine.CredentialType{Name: "x", Label: "X"})
			},
			wantErr: "no fields",
		},
		{
			name: "a field twice",
			register: func(r *engine.Registry) error {
				f := engine.CredentialField{Name: "k", Label: "K"}
				return r.RegisterCredentialType(engine.CredentialType{Name: "x", Label: "X", Fields: []engine.CredentialField{f, f}})
			},
			wantErr: "unique name",
		},
		{
			name: "a node using a registered type",
			register: func(r *engine.Registry) error {
				_ = r.RegisterCredentialType(keyType)
				return r.RegisterNode(user("test.key"))
			},
		},
		{
			name: "a node taking either of two types",
			register: func(r *engine.Registry) error {
				_ = r.RegisterCredentialType(keyType)
				_ = r.RegisterCredentialType(other)
				return r.RegisterNode(user("test.key", "test.other"))
			},
		},
		{
			name:     "a node using an unknown type",
			register: func(r *engine.Registry) error { return r.RegisterNode(user("test.key")) },
			wantErr:  `param "key": a credential param must name registered credential types, not "test.key"`,
		},
		{
			name: "a node with one of its types unknown",
			register: func(r *engine.Registry) error {
				_ = r.RegisterCredentialType(keyType)
				return r.RegisterNode(user("test.key", "test.other"))
			},
			wantErr: `not "test.other"`,
		},
		{
			name: "models of a key",
			register: func(r *engine.Registry) error {
				_ = r.RegisterCredentialType(keyType)
				n := user("test.key")
				n.typ.Params = append(n.typ.Params, engine.ParamSpec{Name: "model", Kind: engine.ParamString, ModelsOf: "key"})
				return r.RegisterNode(n)
			},
		},
		{
			name: "models of what is no key",
			register: func(r *engine.Registry) error {
				_ = r.RegisterCredentialType(keyType)
				n := user("test.key")
				n.typ.Params = append(n.typ.Params, engine.ParamSpec{Name: "model", Kind: engine.ParamString, ModelsOf: "model"})
				return r.RegisterNode(n)
			},
			wantErr: `models of "model", which is not a credential param`,
		},
		{
			name:     "a node naming no type",
			register: func(r *engine.Registry) error { return r.RegisterNode(user()) },
			wantErr:  "none named",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.register(engine.NewRegistry())
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
		})
	}

	t.Run("lookup and listing", func(t *testing.T) {
		r := engine.NewRegistry()
		require.NoError(t, r.RegisterCredentialType(engine.CredentialType{Name: "z.last", Label: "Z", Fields: keyType.Fields}))
		require.NoError(t, r.RegisterCredentialType(keyType))

		got, ok := r.CredentialType("test.key")
		require.True(t, ok)
		assert.Equal(t, "Test key", got.Label)

		_, ok = r.CredentialType("nope")
		assert.False(t, ok)

		names := []string{}
		for _, ct := range r.CredentialTypes() {
			names = append(names, ct.Name)
		}

		assert.Equal(t, []string{"test.key", "z.last"}, names)
	})

	t.Run("credential params are personal", func(t *testing.T) {
		assert.True(t, engine.ParamSpec{Kind: engine.ParamCredential}.Personal())
		assert.True(t, engine.ParamSpec{Kind: engine.ParamWallet}.Personal())
		assert.False(t, engine.ParamSpec{Kind: engine.ParamString}.Personal())
	})
}
