package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

func nodes(ids ...domain.NodeID) []domain.NodeSpec {
	out := make([]domain.NodeSpec, len(ids))
	for i, id := range ids {
		out[i] = domain.NodeSpec{ID: id, Type: "test." + string(id)}
	}

	return out
}

func conn(from, to domain.NodeID) domain.Connection {
	return domain.Connection{From: from, FromPort: "main", To: to, ToPort: "main"}
}

// diamond: trigger fans out to two branches that merge again.
func diamond() domain.Workflow {
	return domain.Workflow{
		ID:    "wf",
		Nodes: nodes("trigger", "left", "right", "merge"),
		Connections: []domain.Connection{
			conn("trigger", "left"),
			conn("trigger", "right"),
			conn("left", "merge"),
			conn("right", "merge"),
		},
	}
}

func TestWorkflowValidate(t *testing.T) {
	tests := []struct {
		name     string
		workflow domain.Workflow
		// wantProblems are substrings, one per expected problem; nil means valid.
		wantProblems []string
	}{
		{
			name:     "single node",
			workflow: domain.Workflow{Nodes: nodes("a")},
		},
		{
			name:     "diamond with fan-out and fan-in",
			workflow: diamond(),
		},
		{
			name: "branch ports on one node",
			workflow: domain.Workflow{
				Nodes: nodes("if", "yes", "no"),
				Connections: []domain.Connection{
					{From: "if", FromPort: "true", To: "yes", ToPort: "main"},
					{From: "if", FromPort: "false", To: "no", ToPort: "main"},
				},
			},
		},
		{
			name:         "no nodes",
			workflow:     domain.Workflow{},
			wantProblems: []string{"no nodes"},
		},
		{
			name:         "node without id or type",
			workflow:     domain.Workflow{Nodes: []domain.NodeSpec{{}}},
			wantProblems: []string{"node 0 has no id", `node "" has no type`},
		},
		{
			name:         "duplicate node id",
			workflow:     domain.Workflow{Nodes: nodes("a", "a")},
			wantProblems: []string{`duplicate node id "a"`},
		},
		{
			name:         "connection to an unknown node",
			workflow:     domain.Workflow{Nodes: nodes("a"), Connections: []domain.Connection{conn("a", "ghost")}},
			wantProblems: []string{`connection to unknown node "ghost"`},
		},
		{
			name:         "connection from an unknown node",
			workflow:     domain.Workflow{Nodes: nodes("a"), Connections: []domain.Connection{conn("ghost", "a")}},
			wantProblems: []string{`connection from unknown node "ghost"`},
		},
		{
			name: "unnamed port",
			workflow: domain.Workflow{
				Nodes:       nodes("a", "b"),
				Connections: []domain.Connection{{From: "a", To: "b", ToPort: "main"}},
			},
			wantProblems: []string{"a -> b has an unnamed port"},
		},
		{
			name:         "self loop",
			workflow:     domain.Workflow{Nodes: nodes("a"), Connections: []domain.Connection{conn("a", "a")}},
			wantProblems: []string{`node "a" is connected to itself`},
		},
		{
			name: "duplicate connection",
			workflow: domain.Workflow{
				Nodes:       nodes("a", "b"),
				Connections: []domain.Connection{conn("a", "b"), conn("a", "b")},
			},
			wantProblems: []string{"duplicate connection a.main -> b.main"},
		},
		{
			name: "cycle",
			workflow: domain.Workflow{
				Nodes:       nodes("start", "a", "b"),
				Connections: []domain.Connection{conn("start", "a"), conn("a", "b"), conn("b", "a")},
			},
			wantProblems: []string{"cycle, cannot order [a b]"},
		},
		{
			name: "every problem is reported at once",
			workflow: domain.Workflow{
				Nodes:       []domain.NodeSpec{{ID: "a"}, {ID: "a", Type: "t"}},
				Connections: []domain.Connection{conn("a", "ghost"), conn("a", "a")},
			},
			wantProblems: []string{
				`node "a" has no type`,
				`duplicate node id "a"`,
				`connection to unknown node "ghost"`,
				`node "a" is connected to itself`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.workflow.Validate()

			if tt.wantProblems == nil {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, domain.ErrInvalidWorkflow)

			for _, p := range tt.wantProblems {
				assert.Contains(t, err.Error(), p)
			}
		})
	}
}

func TestWorkflowTopologicalOrder(t *testing.T) {
	tests := []struct {
		name     string
		workflow domain.Workflow
		want     []domain.NodeID
		wantErr  bool
	}{
		{
			name: "linear chain declared backwards",
			workflow: domain.Workflow{
				Nodes:       nodes("c", "b", "a"),
				Connections: []domain.Connection{conn("a", "b"), conn("b", "c")},
			},
			want: []domain.NodeID{"a", "b", "c"},
		},
		{
			name:     "diamond keeps declaration order between branches",
			workflow: diamond(),
			want:     []domain.NodeID{"trigger", "left", "right", "merge"},
		},
		{
			name:     "disconnected nodes keep declaration order",
			workflow: domain.Workflow{Nodes: nodes("x", "y", "z")},
			want:     []domain.NodeID{"x", "y", "z"},
		},
		{
			name: "connections to unknown nodes are ignored",
			workflow: domain.Workflow{
				Nodes:       nodes("a", "b"),
				Connections: []domain.Connection{conn("ghost", "b"), conn("a", "ghost"), conn("a", "b")},
			},
			want: []domain.NodeID{"a", "b"},
		},
		{
			name: "cycle",
			workflow: domain.Workflow{
				Nodes:       nodes("a", "b"),
				Connections: []domain.Connection{conn("a", "b"), conn("b", "a")},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.workflow.TopologicalOrder()

			if tt.wantErr {
				require.ErrorIs(t, err, domain.ErrInvalidWorkflow)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestWorkflowLookups(t *testing.T) {
	wf := diamond()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{name: "outgoing", got: wf.Outgoing("trigger"), want: []domain.Connection{conn("trigger", "left"), conn("trigger", "right")}},
		{name: "incoming", got: wf.Incoming("merge"), want: []domain.Connection{conn("left", "merge"), conn("right", "merge")}},
		{name: "no outgoing", got: wf.Outgoing("merge"), want: []domain.Connection(nil)},
		{name: "no incoming", got: wf.Incoming("trigger"), want: []domain.Connection(nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.got)
		})
	}

	t.Run("node lookup", func(t *testing.T) {
		n, ok := wf.Node("left")
		require.True(t, ok)
		assert.Equal(t, "test.left", n.Type)

		_, ok = wf.Node("ghost")
		assert.False(t, ok)
	})
}
