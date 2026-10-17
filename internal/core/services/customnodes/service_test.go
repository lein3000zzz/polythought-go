package customnodes_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/customnodes"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

var (
	now   = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	alice = auth.Principal{User: domain.User{ID: "u-1"}, SessionID: "s-1"}
	bob   = auth.Principal{User: domain.User{ID: "u-2"}, SessionID: "s-2"}
)

// memNodes keeps custom nodes in memory, like the repository, and counts the lists.
type memNodes struct {
	mu    sync.Mutex
	nodes map[domain.CustomNodeID]domain.CustomNode
	lists int
}

func (m *memNodes) Create(_ context.Context, n domain.CustomNode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, o := range m.nodes {
		if o.OwnerID == n.OwnerID && o.Name == n.Name {
			return domain.ErrCustomNodeTaken
		}
	}

	m.nodes[n.ID] = n

	return nil
}

func (m *memNodes) Update(_ context.Context, n domain.CustomNode, expected int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	old, ok := m.nodes[n.ID]
	if !ok || old.OwnerID != n.OwnerID {
		return domain.ErrNotFound
	}

	if old.Version != expected {
		return domain.ErrConflict
	}

	m.nodes[n.ID] = n

	return nil
}

func (m *memNodes) Get(_ context.Context, owner domain.UserID, id domain.CustomNodeID) (domain.CustomNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	n, ok := m.nodes[id]
	if !ok || n.OwnerID != owner {
		return domain.CustomNode{}, domain.ErrNotFound
	}

	return n, nil
}

func (m *memNodes) List(_ context.Context, owner domain.UserID) ([]domain.CustomNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.lists++

	var out []domain.CustomNode

	for _, n := range m.nodes {
		if n.OwnerID == owner {
			out = append(out, n)
		}
	}

	slices.SortFunc(out, func(a, b domain.CustomNode) int { return strings.Compare(a.Name, b.Name) })

	return out, nil
}

func (m *memNodes) CountForOwner(ctx context.Context, owner domain.UserID) (int, error) {
	list, err := m.List(ctx, owner)
	return len(list), err
}

func (m *memNodes) Delete(_ context.Context, owner domain.UserID, id domain.CustomNodeID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if n, ok := m.nodes[id]; !ok || n.OwnerID != owner {
		return domain.ErrNotFound
	}

	delete(m.nodes, id)

	return nil
}

// compiler refuses code with "oops" in it, at its line 1, column 5.
type compiler struct{}

func (compiler) Compile(_ context.Context, source string) (ports.CompiledCode, error) {
	if strings.Contains(source, "oops") {
		return ports.CompiledCode{}, &domain.CodeError{
			Kind: domain.CodeDidNotCompile, Message: "it does not compile",
			Problems: []domain.CodeProblem{{Line: 1, Column: 5, Message: `Expected ";" but found "oops"`}},
		}
	}

	return ports.CompiledCode{Script: source}, nil
}

func (compiler) CompileExpression(context.Context, string) (string, error) { return "", nil }

func (compiler) CompileWorkflow(context.Context, string) (ports.CompiledCode, error) {
	return ports.CompiledCode{}, nil
}

type gate struct{ off bool }

func (g gate) CodeAllowed(context.Context, domain.UserID) (bool, error) { return !g.off, nil }

type trigger struct{}

func (trigger) Type() engine.NodeType {
	return engine.NodeType{Name: "trigger.manual", Outputs: []string{"main"}}
}

func (trigger) Start(context.Context, engine.TriggerContext, engine.Emit) error { return nil }

type pass struct{}

func (pass) Type() engine.NodeType {
	return engine.NodeType{
		Name: "logic.pass", Inputs: []string{"main"}, Outputs: []string{"main"}, Items: &engine.Items{Passed: true},
		Params: []engine.ParamSpec{{Name: "field", Kind: engine.ParamString, Field: true}},
	}
}

func (pass) Run(_ context.Context, _ engine.RunContext, in engine.Ports) (engine.Ports, error) {
	return in, nil
}

type fixture struct {
	svc       *customnodes.Service
	nodes     *memNodes
	workflows *portsmocks.MockWorkflowRepository
}

func newService(t *testing.T, g gate, limits customnodes.Limits) fixture {
	t.Helper()

	base := engine.NewRegistry()
	require.NoError(t, base.RegisterTrigger(trigger{}))
	require.NoError(t, base.RegisterNode(pass{}))

	f := fixture{
		nodes:     &memNodes{nodes: map[domain.CustomNodeID]domain.CustomNode{}},
		workflows: portsmocks.NewMockWorkflowRepository(t),
	}
	f.svc = customnodes.New(f.nodes, f.workflows, base, nil, compiler{}, g, logging.Noop(),
		customnodes.WithClock(func() time.Time { return now }),
		customnodes.WithLiveLimits(func() customnodes.Limits { return limits }))

	return f
}

// split is a valid definition: a threshold, two outputs, items passed on.
func split() customnodes.Definition {
	return customnodes.Definition{
		Name: "spread-split", Label: " Spread split ", Description: "Wide books one way, tight ones the other.",
		Settings: []domain.CustomSetting{
			{Name: "threshold", Kind: "decimal", Default: "0.02"},
			{Name: "on", Label: "Field", Kind: "string", Field: true, FieldKinds: []string{"decimal"}},
		},
		Outputs: []string{"wide", "tight"},
		Items:   domain.CustomItems{Passed: true},
		Source:  "export function run(items) { return { wide: items } }",
	}
}

// using is a workflow of owner that sends split's wide items on to a pass node.
func using(owner domain.UserID) domain.Workflow {
	return domain.Workflow{
		ID: "wf-1", OwnerID: owner, Name: "Spread watch",
		Nodes: []domain.NodeSpec{
			{ID: "manual", Type: "trigger.manual"},
			{ID: "split", Type: "custom.spread-split", Params: map[string]any{"threshold": "0.03"}},
			{ID: "log", Type: "logic.pass"},
		},
		Connections: []domain.Connection{
			{From: "manual", FromPort: "main", To: "split", ToPort: "main"},
			{From: "split", FromPort: "wide", To: "log", ToPort: "main"},
		},
	}
}

func (f fixture) listing(workflows ...domain.Workflow) {
	f.workflows.EXPECT().List(mock.Anything, mock.Anything, mock.Anything).
		Return(ports.Page[domain.Workflow]{Items: workflows}, nil)
}

func TestCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("makes the type, for its owner only", func(t *testing.T) {
		f := newService(t, gate{}, customnodes.DefaultLimits())

		n, err := f.svc.Create(ctx, alice, split())
		require.NoError(t, err)
		assert.Equal(t, domain.UserID("u-1"), n.OwnerID)
		assert.Equal(t, "Spread split", n.Label, "trimmed")
		assert.Equal(t, "threshold", n.Settings[0].Label, "a setting without a label is called by its name")
		assert.Equal(t, 1, n.Version)
		assert.Equal(t, now, n.CreatedAt)

		reg, err := f.svc.For(ctx, "u-1")
		require.NoError(t, err)

		typ, trigger, ok := reg.Type("custom.spread-split")
		require.True(t, ok)
		assert.False(t, trigger)
		assert.True(t, typ.Code)
		assert.Equal(t, []string{"wide", "tight"}, typ.Outputs)

		_, _, ok = reg.Type("logic.pass")
		assert.True(t, ok, "with the built-ins")

		theirs, err := f.svc.For(ctx, "u-2")
		require.NoError(t, err)
		_, _, ok = theirs.Type("custom.spread-split")
		assert.False(t, ok, "someone else's is not theirs")

		_, err = f.svc.Create(ctx, alice, split())
		require.ErrorIs(t, err, domain.ErrCustomNodeTaken)

		_, err = f.svc.Create(ctx, bob, split())
		require.NoError(t, err, "a namespace each")
	})

	t.Run("only for those who may write code", func(t *testing.T) {
		f := newService(t, gate{off: true}, customnodes.DefaultLimits())

		_, err := f.svc.Create(ctx, alice, split())
		require.ErrorIs(t, err, domain.ErrCodeOff)
	})

	t.Run("up to the limit", func(t *testing.T) {
		f := newService(t, gate{}, customnodes.Limits{PerUser: 1})

		_, err := f.svc.Create(ctx, alice, split())
		require.NoError(t, err)

		other := split()
		other.Name = "other"

		_, err = f.svc.Create(ctx, alice, other)
		require.ErrorIs(t, err, domain.ErrInvalidInput)
		assert.ErrorContains(t, err, "at most 1 custom nodes per user")
	})
}

func TestCreateChecksTheDefinition(t *testing.T) {
	tests := []struct {
		name   string
		change func(d *customnodes.Definition)
		param  string
		want   string
	}{
		{"a name with capitals", func(d *customnodes.Definition) { d.Name = "Split" }, "name", "lowercase letters"},
		{"no label", func(d *customnodes.Definition) { d.Label = "  " }, "label", "1 to 80 characters"},
		{"too many settings", func(d *customnodes.Definition) {
			d.Settings = make([]domain.CustomSetting, 21)
			for i := range d.Settings {
				d.Settings[i] = domain.CustomSetting{Name: "s" + strings.Repeat("x", i), Kind: "string"}
			}
		}, "settings", "at most 20 settings"},
		{"a setting code cannot read", func(d *customnodes.Definition) { d.Settings[0].Name = "the-threshold" }, "settings.0.name", "letters, digits and underscores"},
		{"two settings of one name", func(d *customnodes.Definition) { d.Settings[1].Name = "threshold" }, "settings.1.name", "another setting is called threshold"},
		{"a credential", func(d *customnodes.Definition) { d.Settings[0] = domain.CustomSetting{Name: "key", Kind: "credential"} }, "settings.0.kind", `not "credential"`},
		{"code", func(d *customnodes.Definition) { d.Settings[0] = domain.CustomSetting{Name: "more", Kind: "code"} }, "settings.0.kind", `not "code"`},
		{"choices of a number", func(d *customnodes.Definition) {
			d.Settings[0] = domain.CustomSetting{Name: "n", Kind: "number", Options: []string{"1"}}
		}, "settings.0.options", "only text and lists have choices"},
		{"a field that is a number", func(d *customnodes.Definition) {
			d.Settings[0] = domain.CustomSetting{Name: "n", Kind: "number", Field: true}
		}, "settings.0.field", "only text and lists name fields"},
		{"field kinds without a field", func(d *customnodes.Definition) { d.Settings[1].Field = false }, "settings.1.fieldKinds", "only a setting that names a field"},
		{"a field kind there is not", func(d *customnodes.Definition) { d.Settings[1].FieldKinds = []string{"money"} }, "settings.1.fieldKinds", `no field kind "money"`},
		{"a default of the wrong kind", func(d *customnodes.Definition) { d.Settings[0].Default = true }, "settings.0.default", "the default must be a number"},
		{"a default not among the choices", func(d *customnodes.Definition) {
			d.Settings[0] = domain.CustomSetting{Name: "side", Kind: "string", Options: []string{"buy", "sell"}, Default: "hold"}
		}, "settings.0.default", `"hold" is not one of [buy sell]`},
		{"a wallet with a default", func(d *customnodes.Definition) {
			d.Settings[0] = domain.CustomSetting{Name: "wallet", Kind: "wallet", Default: "w-1"}
		}, "settings.0.default", "no default"},
		{"no outputs", func(d *customnodes.Definition) { d.Outputs = nil }, "outputs", "1 to 10 outputs"},
		{"an output called error", func(d *customnodes.Definition) { d.Outputs[1] = "error" }, "outputs.1", "the port failed items go out of"},
		{"two outputs of one name", func(d *customnodes.Definition) { d.Outputs[1] = "wide" }, "outputs.1", "another output is called wide"},
		{"an output with a space", func(d *customnodes.Definition) { d.Outputs[0] = "very wide" }, "outputs.0", "lowercase letters"},
		{"passed on and fields too", func(d *customnodes.Definition) {
			d.Items.Fields = []domain.CustomField{{Name: "n", Kind: "number"}}
		}, "items", "not both"},
		{"a field code cannot read", func(d *customnodes.Definition) {
			d.Items = domain.CustomItems{Fields: []domain.CustomField{{Name: "best", Kind: "object", Fields: []domain.CustomField{{Name: "a.b", Kind: "text"}}}}}
		}, "items", `field "best.a.b"`},
		{"a list without what it holds", func(d *customnodes.Definition) {
			d.Items = domain.CustomItems{Fields: []domain.CustomField{{Name: "tags", Kind: "list"}}}
		}, "items", `list "tags" needs its elements`},
		{"no code", func(d *customnodes.Definition) { d.Source = " " }, "source", "the code is empty"},
		{"too much code", func(d *customnodes.Definition) { d.Source = strings.Repeat("x", 64<<10+1) }, "source", "more than the 64 KiB allowed"},
		{"code that does not compile", func(d *customnodes.Definition) { d.Source = "let oops" }, "source", `line 1, column 5: Expected ";" but found "oops"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newService(t, gate{}, customnodes.DefaultLimits())

			def := split()
			tt.change(&def)

			_, err := f.svc.Create(context.Background(), alice, def)
			require.ErrorIs(t, err, domain.ErrInvalidInput)

			var v *domain.ValidationError
			require.ErrorAs(t, err, &v)

			i := slices.IndexFunc(v.Problems, func(p domain.Problem) bool { return p.Param == tt.param })
			require.GreaterOrEqual(t, i, 0, "a problem with %s in %v", tt.param, v.Problems)
			assert.Contains(t, v.Problems[i].Message, tt.want)

			list, _ := f.nodes.List(context.Background(), "u-1")
			assert.Empty(t, list, "nothing stored")
		})
	}
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()

	made := func(t *testing.T) (fixture, domain.CustomNode) {
		t.Helper()

		f := newService(t, gate{}, customnodes.DefaultLimits())
		n, err := f.svc.Create(ctx, alice, split())
		require.NoError(t, err)

		return f, n
	}

	t.Run("a change that breaks nothing is saved", func(t *testing.T) {
		f, n := made(t)
		f.listing(using("u-1"))

		reg, err := f.svc.For(ctx, "u-1")
		require.NoError(t, err)
		_, _, ok := reg.Type("custom.spread-split")
		require.True(t, ok)

		def := split()
		def.Label = "Split by spread"
		def.Outputs = []string{"wide", "tight", "none"}

		got, err := f.svc.Update(ctx, alice, n.ID, 1, def, false)
		require.NoError(t, err)
		assert.Equal(t, 2, got.Version)
		assert.Equal(t, "Split by spread", got.Label)

		reg, err = f.svc.For(ctx, "u-1")
		require.NoError(t, err)

		typ, _, _ := reg.Type("custom.spread-split")
		assert.Equal(t, []string{"wide", "tight", "none"}, typ.Outputs, "the owner's types are the new ones")
	})

	t.Run("one that breaks a workflow says how, and is made when confirmed", func(t *testing.T) {
		f, n := made(t)

		other := using("u-1")
		other.ID, other.Name, other.Nodes[1].Params = "wf-2", "Unaffected", nil
		other.Connections = other.Connections[:1]
		f.listing(using("u-1"), other)

		def := split()
		def.Outputs = []string{"tight"}                        // wide, which wf-1 sends on, is gone
		def.Settings = []domain.CustomSetting{def.Settings[1]} // and threshold, which it sets

		_, err := f.svc.Update(ctx, alice, n.ID, 1, def, false)
		require.ErrorIs(t, err, domain.ErrBreaksWorkflows)

		var breaks *domain.BreaksError
		require.ErrorAs(t, err, &breaks)
		require.Len(t, breaks.Workflows, 1, "only those it breaks")

		w := breaks.Workflows[0]
		assert.Equal(t, domain.WorkflowID("wf-1"), w.ID)
		assert.Equal(t, "Spread watch", w.Name)

		messages := make([]string, len(w.Problems))
		for i, p := range w.Problems {
			messages[i] = p.Message
		}

		assert.ElementsMatch(t, []string{
			`node "split": unknown param "threshold"`,
			`node "split" (custom.spread-split) has no output port "wide"`,
		}, messages)

		kept, err := f.nodes.Get(ctx, "u-1", n.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, kept.Version, "not saved")

		got, err := f.svc.Update(ctx, alice, n.ID, 1, def, true)
		require.NoError(t, err)
		assert.Equal(t, []string{"tight"}, got.Outputs)
	})

	t.Run("what was wrong before does not count", func(t *testing.T) {
		f, n := made(t)

		broken := using("u-1")
		broken.Nodes[2].Params = map[string]any{"field": "nope"} // a field the items lack, already
		f.listing(broken)

		def := split()
		def.Label = "Split"

		_, err := f.svc.Update(ctx, alice, n.ID, 1, def, false)
		require.NoError(t, err)
	})

	t.Run("checks the version and keeps the name", func(t *testing.T) {
		f, n := made(t)

		_, err := f.svc.Update(ctx, alice, n.ID, 7, split(), false)
		require.ErrorIs(t, err, domain.ErrConflict)

		renamed := split()
		renamed.Name = "renamed"

		_, err = f.svc.Update(ctx, alice, n.ID, 1, renamed, false)
		require.ErrorIs(t, err, domain.ErrInvalidInput)
		assert.ErrorContains(t, err, "keeps its name")

		_, err = f.svc.Update(ctx, bob, n.ID, 1, split(), false)
		require.ErrorIs(t, err, domain.ErrNotFound, "someone else's")
	})

	t.Run("only for those who may write code", func(t *testing.T) {
		f, n := made(t)
		off := customnodes.New(f.nodes, f.workflows, engine.NewRegistry(), nil, compiler{}, gate{off: true}, logging.Noop())

		_, err := off.Update(ctx, alice, n.ID, 1, split(), true)
		require.ErrorIs(t, err, domain.ErrCodeOff)

		require.NoError(t, off.Delete(ctx, alice, n.ID, true), "deleting needs no code")
	})
}

func TestDelete(t *testing.T) {
	ctx := context.Background()

	f := newService(t, gate{}, customnodes.DefaultLimits())
	n, err := f.svc.Create(ctx, alice, split())
	require.NoError(t, err)

	f.listing(using("u-1"))

	err = f.svc.Delete(ctx, alice, n.ID, false)

	var breaks *domain.BreaksError
	require.ErrorAs(t, err, &breaks)
	require.Len(t, breaks.Workflows, 1)
	assert.Equal(t, `node "split": unknown node type "custom.spread-split"`, breaks.Workflows[0].Problems[0].Message)
	assert.EqualError(t, err, `the change breaks workflows that use the node: "Spread watch"`)

	require.NoError(t, f.svc.Delete(ctx, alice, n.ID, true))

	reg, err := f.svc.For(ctx, "u-1")
	require.NoError(t, err)
	_, _, ok := reg.Type("custom.spread-split")
	assert.False(t, ok, "gone from the owner's types")
}

func TestForCaches(t *testing.T) {
	ctx := context.Background()

	f := newService(t, gate{}, customnodes.DefaultLimits())
	_, err := f.svc.Create(ctx, alice, split())
	require.NoError(t, err)

	lists := func() int {
		f.nodes.mu.Lock()
		defer f.nodes.mu.Unlock()

		return f.nodes.lists
	}

	start := lists()

	for range 3 {
		_, err := f.svc.For(ctx, "u-1")
		require.NoError(t, err)
	}

	assert.Equal(t, start+1, lists(), "read once")

	other := split()
	other.Name = "other"
	_, err = f.svc.Create(ctx, alice, other)
	require.NoError(t, err)

	reg, err := f.svc.For(ctx, "u-1")
	require.NoError(t, err)
	_, _, ok := reg.Type("custom.other")
	assert.True(t, ok, "read again after a change")
}

// A stored node that no longer registers is left out; the owner's others still work.
func TestForLeavesOutWhatNoLongerRegisters(t *testing.T) {
	ctx := context.Background()

	f := newService(t, gate{}, customnodes.DefaultLimits())
	_, err := f.svc.Create(ctx, alice, split())
	require.NoError(t, err)

	bad := domain.CustomNode{ID: "n-bad", OwnerID: "u-1", Name: "bad", Outputs: []string{"main"},
		Items: domain.CustomItems{Fields: []domain.CustomField{{Name: "x", Kind: "money"}}}}
	require.NoError(t, f.nodes.Create(ctx, bad))

	f2 := customnodes.New(f.nodes, f.workflows, engine.NewRegistry(), nil, compiler{}, gate{}, logging.Noop())

	reg, err := f2.For(ctx, "u-1")
	require.NoError(t, err)

	_, _, ok := reg.Type("custom.bad")
	assert.False(t, ok)
	_, _, ok = reg.Type("custom.spread-split")
	assert.True(t, ok)
}
