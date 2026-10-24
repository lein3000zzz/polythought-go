package mcpserver_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/customnodes"
)

var split = domain.CustomNode{
	ID: "cn-1", OwnerID: "u-1", Name: "spread-split", Label: "Spread split", Version: 2, UpdatedAt: at,
	Settings: []domain.CustomSetting{{Name: "threshold", Kind: "decimal", Default: "0.02"}},
	Outputs:  []string{"wide", "tight"},
	Items: domain.CustomItems{Passed: true, Fields: []domain.CustomField{
		{Name: "levels", Kind: "list", Of: &domain.CustomField{Kind: "object", Fields: []domain.CustomField{{Name: "price", Kind: "decimal"}}}},
	}},
	Source: "export function run(items) { return { wide: items } }",
}

// nodesFake is the custom nodes service: it keeps what it was given, and holds a change
// that breaks a workflow until confirmed.
type nodesFake struct {
	mu      sync.Mutex
	made    customnodes.Definition
	deleted domain.CustomNodeID
}

func (*nodesFake) List(context.Context, auth.Principal) ([]domain.CustomNode, error) {
	return []domain.CustomNode{split}, nil
}

func (*nodesFake) Get(_ context.Context, _ auth.Principal, id domain.CustomNodeID) (domain.CustomNode, error) {
	if id != split.ID {
		return domain.CustomNode{}, domain.ErrNotFound
	}

	return split, nil
}

func (f *nodesFake) Create(_ context.Context, _ auth.Principal, def customnodes.Definition) (domain.CustomNode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if def.Label == "" {
		v := domain.ValidationError{Base: domain.ErrInvalidInput}
		v.Add(domain.Problem{Param: "label", Message: "a label is 1 to 80 characters"})
		v.Add(domain.Problem{Param: "settings.0.default", Message: "not a decimal"})

		return domain.CustomNode{}, v.Err()
	}

	f.made = def

	return domain.CustomNode{ID: "cn-2", Name: def.Name, Label: def.Label, Outputs: def.Outputs, Items: def.Items, Source: def.Source, Version: 1, UpdatedAt: at}, nil
}

func (*nodesFake) Update(_ context.Context, _ auth.Principal, id domain.CustomNodeID, version int, def customnodes.Definition, confirm bool) (domain.CustomNode, error) {
	if version != split.Version {
		return domain.CustomNode{}, domain.ErrConflict
	}

	if !confirm {
		return domain.CustomNode{}, &domain.BreaksError{Workflows: []domain.BrokenWorkflow{{
			ID: "wf-1", Name: "Spread watch", Problems: []domain.Problem{{NodeID: "split", Message: `node "split" (custom.spread-split) has no output port "tight"`}},
		}}}
	}

	n := split
	n.ID, n.Version, n.Outputs = id, version+1, def.Outputs

	return n, nil
}

func (f *nodesFake) Delete(_ context.Context, p auth.Principal, id domain.CustomNodeID, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := p.NeedDelete(); err != nil {
		return err
	}

	f.deleted = id

	return nil
}

func withNodes(t *testing.T, u domain.User, f *nodesFake, scopes ...domain.Scope) *mcp.ClientSession {
	t.Helper()

	d := deps(u, &trades{}, scopes)
	d.CustomNodes = f

	return connect(t, d)
}

func TestCustomNodeTools(t *testing.T) {
	f := &nodesFake{}
	agent := withNodes(t, alice, f, domain.ScopeBuild)

	t.Run("read", func(t *testing.T) {
		list, _ := call(t, agent, "list_custom_nodes", nil)
		assert.Equal(t, []any{map[string]any{
			"id": "cn-1", "type": "custom.spread-split", "label": "Spread split", "version": 2.0, "updatedAt": "2026-10-23T12:00:00Z",
		}}, list["customNodes"])

		got, _ := call(t, agent, "get_custom_node", map[string]any{"id": "cn-1"})
		assert.Equal(t, split.Source, got["source"])
		assert.Equal(t, []any{"wide", "tight"}, got["outputs"])
		assert.Equal(t, map[string]any{"passed": true, "fields": []any{map[string]any{
			"name": "levels", "kind": "list", "of": map[string]any{"kind": "object", "fields": []any{map[string]any{"name": "price", "kind": "decimal"}}},
		}}}, got["items"], "its fields a tree")
	})

	t.Run("create, fields and all", func(t *testing.T) {
		got, _ := call(t, agent, "create_custom_node", map[string]any{
			"name": "spread-flag", "label": "Spread flag", "outputs": []any{"main"}, "source": "export function run(items) { return items }",
			"settings": []any{map[string]any{"name": "threshold", "kind": "decimal", "default": "0.02"}},
			"items": map[string]any{"passed": true, "fields": []any{
				map[string]any{"name": "flags", "kind": "list", "of": map[string]any{"kind": "text", "options": []any{"wide", "tight"}}},
			}},
		})
		assert.Equal(t, "custom.spread-flag", got["type"])

		f.mu.Lock()
		defer f.mu.Unlock()

		assert.Equal(t, []domain.CustomSetting{{Name: "threshold", Kind: "decimal", Default: "0.02"}}, f.made.Settings)
		assert.Equal(t, domain.CustomItems{Passed: true, Fields: []domain.CustomField{
			{Name: "flags", Kind: "list", Of: &domain.CustomField{Kind: "text", Options: []string{"wide", "tight"}}},
		}}, f.made.Items)
	})

	t.Run("a form refused says where each problem is", func(t *testing.T) {
		_, err := call(t, agent, "create_custom_node", map[string]any{"name": "x", "label": "", "outputs": []any{"main"}, "items": map[string]any{}, "source": "x"})
		assert.Equal(t, "invalid input: label: a label is 1 to 80 characters\ninvalid input: settings.0.default: not a decimal", err)

		_, err = call(t, agent, "create_custom_node", map[string]any{
			"name": "x", "label": "X", "outputs": []any{"main"}, "source": "x", "items": map[string]any{"fields": []any{"price"}},
		})
		assert.Contains(t, err, "items.fields: each {name, kind")
	})

	t.Run("a change that breaks a workflow, then confirmed", func(t *testing.T) {
		change := map[string]any{"id": "cn-1", "version": 2, "label": "Spread split", "outputs": []any{"wide"}, "items": map[string]any{"passed": true}, "source": split.Source}

		_, err := call(t, agent, "update_custom_node", change)
		assert.Equal(t, "the change breaks workflows that use the node; change them first, or call again with confirm to make the change anyway:\n"+
			`"Spread watch" (wf-1): node "split" (custom.spread-split) has no output port "tight"`, err)

		change["confirm"] = true
		got, _ := call(t, agent, "update_custom_node", change)
		assert.Equal(t, 3.0, got["version"])
	})

	t.Run("delete, where the user lets agents", func(t *testing.T) {
		_, err := call(t, agent, "delete_custom_node", map[string]any{"id": "cn-1"})
		assert.Contains(t, err, "the user has not let agents and tokens delete")

		may := alice
		may.Agents.Delete = true

		got, _ := call(t, withNodes(t, may, f, domain.ScopeBuild), "delete_custom_node", map[string]any{"id": "cn-1", "confirm": true})
		assert.Equal(t, map[string]any{"deleted": "cn-1"}, got)
	})

	t.Run("reading needs read, the rest build", func(t *testing.T) {
		reader := withNodes(t, alice, f, domain.ScopeRead)

		_, err := call(t, reader, "list_custom_nodes", nil)
		assert.Empty(t, err)

		_, err = call(t, reader, "delete_custom_node", map[string]any{"id": "cn-1"})
		assert.Contains(t, err, "it needs the build scope")
	})
}

// The schema an agent writes a custom node's fields to is what the tool reads them as.
func TestCustomNodeFieldsRoundTrip(t *testing.T) {
	got, _ := call(t, withNodes(t, alice, &nodesFake{}, domain.ScopeRead), "get_custom_node", map[string]any{"id": "cn-1"})

	data, err := json.Marshal(got["items"])
	require.NoError(t, err)

	f := &nodesFake{}
	agent := withNodes(t, alice, f, domain.ScopeBuild)

	var items map[string]any
	require.NoError(t, json.Unmarshal(data, &items))

	_, msg := call(t, agent, "create_custom_node", map[string]any{"name": "again", "label": "Again", "outputs": []any{"main"}, "items": items, "source": "x"})
	require.Empty(t, msg)

	f.mu.Lock()
	defer f.mu.Unlock()

	assert.Equal(t, split.Items, f.made.Items)
}
