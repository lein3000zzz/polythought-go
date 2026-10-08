package engine_test

import (
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
