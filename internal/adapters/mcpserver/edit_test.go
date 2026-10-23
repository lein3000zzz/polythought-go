package mcpserver_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	workflowsvc "github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
)

// editing is the workflows service and the code runner for the build tools: it keeps what
// it was asked to do.
type editing struct {
	mu sync.Mutex

	created  workflowsvc.Definition
	updated  workflowsvc.Definition
	deleted  domain.WorkflowID
	switched string
	ran      domain.NodeID
}

func (e *editing) Create(_ context.Context, _ auth.Principal, def workflowsvc.Definition) (domain.Workflow, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var v domain.ValidationError
	for _, n := range def.Nodes {
		if n.Type == "output.nope" {
			v.Add(domain.Problem{NodeID: n.ID, Message: fmt.Sprintf("node %q: unknown type %q", n.ID, n.Type)})
		}
	}

	if len(v.Problems) > 0 {
		v.Add(domain.Problem{Message: "no trigger node"})
		return domain.Workflow{}, v.Err()
	}

	e.created = def

	return domain.Workflow{ID: "wf-2", Name: def.Name, Version: 1, Nodes: def.Nodes, Connections: def.Connections, UpdatedAt: at}, nil
}

func (e *editing) Update(_ context.Context, _ auth.Principal, id domain.WorkflowID, version int, def workflowsvc.Definition) (domain.Workflow, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if version != flow.Version {
		return domain.Workflow{}, domain.ErrConflict
	}

	e.updated = def

	return domain.Workflow{ID: id, Name: def.Name, Version: version + 1, Active: true, Nodes: def.Nodes, Connections: def.Connections, UpdatedAt: at}, nil
}

func (e *editing) Delete(_ context.Context, p auth.Principal, id domain.WorkflowID) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !p.MayDelete() {
		return fmt.Errorf("%w: the user has not let agents and tokens delete workflows", domain.ErrForbidden)
	}

	e.deleted = id

	return nil
}

func (e *editing) Activate(_ context.Context, _ auth.Principal, id domain.WorkflowID) (domain.Workflow, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.switched = "on"
	w := flow
	w.ID = id

	return w, nil
}

func (e *editing) SwitchOff(_ context.Context, _ auth.Principal, id domain.WorkflowID, cancelOrders bool) (domain.Workflow, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.switched = fmt.Sprintf("off, cancelling orders: %v", cancelOrders)
	w := flow
	w.ID, w.Active = id, false

	return w, nil
}

func (e *editing) Run(_ context.Context, _ auth.Principal, _ domain.WorkflowID, trigger domain.NodeID) (domain.Execution, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.ran = trigger

	return byHand, nil
}

func (e *editing) CancelExecution(_ context.Context, _ auth.Principal, _ domain.WorkflowID, execID domain.ExecutionID) (domain.Execution, error) {
	if execID != "ex-3" {
		return domain.Execution{}, fmt.Errorf("%w: it is succeeded", domain.ErrNotWaiting)
	}

	return domain.Execution{ID: execID, Status: domain.ExecutionCancelled, StartedAt: at, Error: "cancelled by you"}, nil
}

// Build runs the code as the code service would: "bad" does not compile, "throws" throws.
func (e *editing) Build(_ context.Context, _ auth.Principal, source string) (domain.Workflow, error) {
	switch source {
	case "bad":
		return domain.Workflow{}, &domain.CodeError{Kind: domain.CodeDidNotCompile, Message: "line 1, column 4: ';' expected", Problems: []domain.CodeProblem{
			{Line: 1, Column: 4, Message: "';' expected"}, {Line: 2, Column: 1, Message: "Cannot find name 'x'"},
		}}
	case "throws":
		return domain.Workflow{}, &domain.CodeError{Kind: domain.CodeThrew, Message: `Error: node "a" is declared twice`, Stack: "at main (workflow.ts:3:9)"}
	}

	return domain.Workflow{Name: "From code", Nodes: []domain.NodeSpec{{ID: "manual", Type: "trigger.manual"}}}, nil
}

// editor is an agent of u's with scopes, against a server that has the build tools.
func editor(t *testing.T, u domain.User, e *editing, scopes ...domain.Scope) *mcp.ClientSession {
	t.Helper()

	d := deps(u, &trades{}, scopes)
	d.Edit, d.Code = e, e

	return connect(t, d)
}

var made = map[string]any{
	"nodes": []any{
		map[string]any{"id": "manual", "type": "trigger.manual"},
		map[string]any{"id": "log", "type": "output.log", "params": map[string]any{"message": "hi"}},
	},
	"connections": []any{map[string]any{"from": "manual", "fromPort": "main", "to": "log", "toPort": "main"}},
}

func TestEditTools(t *testing.T) {
	e := &editing{}
	agent := editor(t, alice, e, domain.ScopeBuild)

	tools, err := agent.ListTools(context.Background(), nil)
	require.NoError(t, err)

	destructive := map[string]bool{}
	for _, tool := range tools.Tools {
		if !tool.Annotations.ReadOnlyHint {
			require.NotNil(t, tool.Annotations.DestructiveHint, tool.Name)
			destructive[tool.Name] = *tool.Annotations.DestructiveHint
		}
	}

	assert.Equal(t, map[string]bool{
		"create_workflow": false, "update_workflow": false, "delete_workflow": true,
		"switch_workflow": false, "run_workflow": false, "cancel_execution": false,
	}, destructive)

	t.Run("create, from JSON, laid out", func(t *testing.T) {
		got, _ := call(t, agent, "create_workflow", map[string]any{"name": "Hello", "workflow": made})
		assert.Equal(t, "wf-2", got["id"])
		assert.Equal(t, "Hello", got["name"])

		e.mu.Lock()
		defer e.mu.Unlock()

		require.Len(t, e.created.Nodes, 2)
		assert.Equal(t, domain.CanvasPosition{X: 0, Y: 0}, e.created.Nodes[0].Position)
		assert.Equal(t, domain.CanvasPosition{X: 300, Y: 0}, e.created.Nodes[1].Position)
		assert.Equal(t, "hi", e.created.Nodes[1].Params["message"])
		assert.Equal(t, []domain.Connection{{From: "manual", FromPort: "main", To: "log", ToPort: "main"}}, e.created.Connections)
	})

	t.Run("create, from code, named by it", func(t *testing.T) {
		got, _ := call(t, agent, "create_workflow", map[string]any{"code": "ok"})
		assert.Equal(t, "From code", got["name"])
	})

	t.Run("code that fails says where", func(t *testing.T) {
		_, err := call(t, agent, "create_workflow", map[string]any{"code": "bad"})
		assert.Equal(t, "the code does not compile: line 1, column 4: ';' expected\n"+
			"the code does not compile: line 2, column 1: Cannot find name 'x'", err)

		_, err = call(t, agent, "create_workflow", map[string]any{"code": "throws"})
		assert.Equal(t, "the code failed: Error: node \"a\" is declared twice\nat main (workflow.ts:3:9)", err)
	})

	t.Run("a workflow refused says each problem", func(t *testing.T) {
		broken := map[string]any{"nodes": []any{map[string]any{"id": "x", "type": "output.nope"}}}

		_, err := call(t, agent, "create_workflow", map[string]any{"name": "Broken", "workflow": broken})
		assert.Equal(t, "invalid workflow: node \"x\": unknown type \"output.nope\"\ninvalid workflow: no trigger node", err)
	})

	t.Run("code or JSON, one of the two", func(t *testing.T) {
		_, err := call(t, agent, "create_workflow", map[string]any{"name": "Both", "code": "ok", "workflow": made})
		assert.Contains(t, err, "give code or workflow, one of the two")

		_, err = call(t, agent, "create_workflow", map[string]any{"name": "Neither"})
		assert.Contains(t, err, "give code or workflow, one of the two")
	})

	t.Run("update keeps the nodes' places and its name", func(t *testing.T) {
		changed := map[string]any{
			"nodes": []any{
				map[string]any{"id": "manual", "type": "trigger.manual"},
				map[string]any{"id": "hook", "type": "output.webhook", "params": map[string]any{"url": map[string]any{"$personal": "hidden"}}},
				map[string]any{"id": "log", "type": "output.log"},
				map[string]any{"id": "after", "type": "output.log"},
			},
			"connections": []any{
				map[string]any{"from": "manual", "fromPort": "main", "to": "hook", "toPort": "main"},
				map[string]any{"from": "hook", "fromPort": "main", "to": "log", "toPort": "main"},
				map[string]any{"from": "log", "fromPort": "main", "to": "after", "toPort": "main"},
			},
		}

		got, _ := call(t, agent, "update_workflow", map[string]any{"id": "wf-1", "version": 3, "workflow": changed})
		assert.Equal(t, 4.0, got["version"])
		assert.Equal(t, "Spread watch", got["name"])
		assert.Equal(t, []any{"hook.url"}, got["hidden"], "kept as it was")

		e.mu.Lock()
		defer e.mu.Unlock()

		require.Len(t, e.updated.Nodes, 4)

		for _, n := range e.updated.Nodes[:3] {
			assert.Equal(t, domain.CanvasPosition{}, n.Position, "%s kept its place", n.ID)
		}

		assert.Equal(t, domain.CanvasPosition{X: 300, Y: 0}, e.updated.Nodes[3].Position, "after the node feeding it")
	})

	t.Run("update on an old version", func(t *testing.T) {
		_, err := call(t, agent, "update_workflow", map[string]any{"id": "wf-1", "version": 2, "code": "ok"})
		assert.Contains(t, err, "the workflow changed since version 2; get_workflow again")
	})

	t.Run("delete, where the user lets agents", func(t *testing.T) {
		_, err := call(t, agent, "delete_workflow", map[string]any{"id": "wf-1"})
		assert.Contains(t, err, "the user has not let agents and tokens delete workflows")

		may := alice
		may.Agents.Delete = true

		got, _ := call(t, editor(t, may, e, domain.ScopeBuild), "delete_workflow", map[string]any{"id": "wf-1"})
		assert.Equal(t, map[string]any{"deleted": "wf-1"}, got)

		e.mu.Lock()
		defer e.mu.Unlock()

		assert.Equal(t, domain.WorkflowID("wf-1"), e.deleted)
	})

	t.Run("switch on and off", func(t *testing.T) {
		on, _ := call(t, agent, "switch_workflow", map[string]any{"id": "wf-1", "on": true})
		assert.Equal(t, true, on["active"])

		off, _ := call(t, agent, "switch_workflow", map[string]any{"id": "wf-1", "on": false, "cancelOrders": true})
		assert.Equal(t, false, off["active"])

		e.mu.Lock()
		defer e.mu.Unlock()

		assert.Equal(t, "off, cancelling orders: true", e.switched)
	})

	t.Run("run", func(t *testing.T) {
		got, _ := call(t, agent, "run_workflow", map[string]any{"id": "wf-1", "trigger": "manual"})
		assert.Equal(t, "ex-1", got["id"])
		assert.Len(t, got["nodes"], 3)

		e.mu.Lock()
		defer e.mu.Unlock()

		assert.Equal(t, domain.NodeID("manual"), e.ran)
	})

	t.Run("cancel an execution", func(t *testing.T) {
		got, _ := call(t, agent, "cancel_execution", map[string]any{"workflow": "wf-1", "execution": "ex-3"})
		assert.Equal(t, "cancelled", got["status"])

		_, err := call(t, agent, "cancel_execution", map[string]any{"workflow": "wf-1", "execution": "ex-1"})
		assert.Equal(t, "the execution is not waiting: it is succeeded", err)
	})
}

// Reading is not building (D399).
func TestEditToolsNeedBuild(t *testing.T) {
	agent := editor(t, alice, &editing{}, domain.ScopeRead, domain.ScopePaper)

	for name, args := range map[string]map[string]any{
		"create_workflow": {"code": "ok"}, "update_workflow": {"id": "wf-1", "version": 3, "code": "ok"},
		"delete_workflow": {"id": "wf-1"}, "switch_workflow": {"id": "wf-1", "on": true},
		"run_workflow": {"id": "wf-1"}, "cancel_execution": {"workflow": "wf-1", "execution": "ex-3"},
	} {
		_, err := call(t, agent, name, args)
		assert.Contains(t, err, "it needs the build scope", name)
	}
}
