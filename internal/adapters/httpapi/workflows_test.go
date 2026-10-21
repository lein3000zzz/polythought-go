package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

var flow = domain.Workflow{
	ID: "wf-1", OwnerID: alice.ID, Name: "Spread watch", Version: 2,
	Nodes: []domain.NodeSpec{
		{ID: "manual", Type: "trigger.manual", Position: domain.CanvasPosition{X: 40, Y: -12.5}},
		{ID: "tight", Type: "logic.if", Params: map[string]any{"field": "spread", "value": json.Number("0.02")}},
	},
	Connections: []domain.Connection{{From: "manual", FromPort: "main", To: "tight", ToPort: "main"}},
	CreatedAt:   created, UpdatedAt: created,
}

const flowBody = `{"name":"Spread watch","nodes":[{"id":"manual","type":"trigger.manual","position":{"x":40,"y":-12.5}},` +
	`{"id":"tight","type":"logic.if","params":{"field":"spread","value":0.02}}],` +
	`"connections":[{"from":"manual","fromPort":"main","to":"tight","toPort":"main"}]}`

// exactDefinition checks the request body reached the service intact, numbers exact.
func exactDefinition(def workflows.Definition) bool {
	return def.Name == "Spread watch" && len(def.Nodes) == 2 && len(def.Connections) == 1 &&
		def.Nodes[0].Position == domain.CanvasPosition{X: 40, Y: -12.5} &&
		def.Nodes[1].Params["value"] == json.Number("0.02")
}

func TestWorkflowRoutes(t *testing.T) {
	exec := domain.Execution{
		ID: "ex-1", WorkflowID: "wf-1", WorkflowVersion: 2, TriggeredBy: "manual", Status: domain.ExecutionSucceeded,
		StartedAt: created, FinishedAt: created.Add(1500 * time.Millisecond),
		Runs: []domain.NodeRun{{
			NodeID: "manual", Status: domain.NodeRunSucceeded, Output: map[string]int{"main": 1},
			Samples: map[string][]domain.ItemSample{
				"main":  {{JSON: json.RawMessage(`{"price":0.020}`)}},
				"error": {{JSON: json.RawMessage(`{"v":1}`), Error: "boom"}},
			},
		}},
	}

	tests := []struct {
		name       string
		req        request
		setup      func(w *httpapimocks.MockWorkflowService, nt *httpapimocks.MockNodeTypes)
		wantStatus int
		wantBody   []string
		check      func(t *testing.T, w *ut.ResponseRecorder)
	}{
		{
			name: "node types for the editor",
			req:  request{method: "GET", path: "/api/v1/node-types", headers: cookie},
			setup: func(_ *httpapimocks.MockWorkflowService, nt *httpapimocks.MockNodeTypes) {
				nt.EXPECT().Types(mock.Anything, mock.MatchedBy(func(p auth.Principal) bool { return p.User.ID == alice.ID })).Return([]engine.TypeInfo{
					{NodeType: engine.NodeType{Name: "trigger.manual", Outputs: []string{"main"}, Items: &engine.Items{Fields: []engine.Field{
						{Name: "side", Kind: engine.FieldText, Options: []string{"buy", "sell"}},
						{Name: "outcomes", Kind: engine.FieldList, Of: &engine.Field{Kind: engine.FieldObject, Fields: []engine.Field{
							{Name: "price", Kind: engine.FieldDecimal, Nullable: true},
						}}},
					}}}, Trigger: true},
					{NodeType: engine.NodeType{Name: "logic.if", Label: "If", Inputs: []string{"main"}, Outputs: []string{"true", "false"},
						Params: []engine.ParamSpec{
							{Name: "operator", Kind: engine.ParamString, Default: "gt", Options: []string{"gt", "lt"}},
							{Name: "field", Kind: engine.ParamString, Field: true, FieldKinds: []engine.FieldKind{engine.FieldNumber, engine.FieldDecimal}},
						},
						Items: &engine.Items{Passed: true}}},
					{NodeType: engine.NodeType{Name: "output.webhook", Inputs: []string{"main"}, Outputs: []string{"main"},
						Params: []engine.ParamSpec{
							{Name: "credential", Kind: engine.ParamCredential, CredentialTypes: []string{"http.header"}},
							{Name: "traders", Kind: engine.ParamTable, Columns: []engine.ParamSpec{
								{Name: "address", Label: "Address", Kind: engine.ParamAddress, Required: true},
								{Name: "on", Kind: engine.ParamBool, Default: true},
							}},
						}}},
					{NodeType: engine.NodeType{Name: "code.filter", Inputs: []string{"main"}, Outputs: []string{"main"}, Code: true}},
				}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`"name":"trigger.manual","trigger":true,"inputs":[]`, `"label":"If"`, `"default":"gt","options":["gt","lt"]`,
				`{"name":"credential","kind":"credential","required":false,"credentialTypes":["http.header"]}`,
				`{"name":"traders","kind":"table","required":false,"columns":[` +
					`{"name":"address","label":"Address","kind":"address","required":true},` +
					`{"name":"on","kind":"bool","required":false,"default":true}]}`,
				`"items":{"fields":[{"name":"side","kind":"text","optional":false,"nullable":false,"options":["buy","sell"]},` +
					`{"name":"outcomes","kind":"list","optional":false,"nullable":false,"of":{"name":"","kind":"object","optional":false,"nullable":false,` +
					`"fields":[{"name":"price","kind":"decimal","optional":false,"nullable":true}]}}],"passed":false}`,
				`"items":{"fields":[],"passed":true}`,
				`{"name":"field","kind":"string","required":false,"field":true,"fieldKinds":["number","decimal"]}`,
				`"name":"code.filter","trigger":false,"inputs":["main"],"outputs":["main"],"params":[],"code":true`,
				`"name":"logic.if","label":"If","trigger":false`,
			},
		},
		{
			name: "check what no longer holds, without saving",
			req:  request{method: "GET", path: "/api/v1/workflows/wf-1/problems", headers: cookie},
			setup: func(ws *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				ws.EXPECT().Check(mock.Anything, mock.Anything, domain.WorkflowID("wf-1")).Return([]domain.Problem{
					{NodeID: "split", Message: `node "split" (custom.spread-split) has no output port "wide"`},
				}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`{"problems":[{"nodeId":"split","message":"node \"split\" (custom.spread-split) has no output port \"wide\""}]}`},
		},
		{
			name: "check one that holds",
			req:  request{method: "GET", path: "/api/v1/workflows/wf-1/problems", headers: cookie},
			setup: func(ws *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				ws.EXPECT().Check(mock.Anything, mock.Anything, domain.WorkflowID("wf-1")).Return([]domain.Problem{}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`{"problems":[]}`},
		},
		{
			name: "create keeps numbers exact",
			req:  request{method: "POST", path: "/api/v1/workflows", body: flowBody, headers: cookie},
			setup: func(w *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				w.EXPECT().Create(mock.Anything, mock.Anything, mock.MatchedBy(exactDefinition)).Return(flow, nil)
			},
			wantStatus: http.StatusCreated,
			wantBody:   []string{`"id":"wf-1"`, `"value":0.02`, `"version":2`, `"position":{"x":40,"y":-12.5}`, `"position":{"x":0,"y":0}`},
		},
		{
			name: "invalid graph",
			req:  request{method: "POST", path: "/api/v1/workflows", body: flowBody, headers: cookie},
			setup: func(w *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				var v domain.ValidationError
				v.Add(domain.Problem{NodeID: "tight", Param: "value", Message: `node "tight": param "value" must be a number`})
				v.Add(domain.Problem{Message: "no trigger node"})
				w.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(domain.Workflow{}, fmt.Errorf("create: %w", v.Err()))
			},
			wantStatus: http.StatusBadRequest,
			wantBody: []string{
				`"code":"invalid_workflow"`,
				`"problems":[{"nodeId":"tight","param":"value","message":"node \"tight\": param \"value\" must be a number"},{"message":"no trigger node"}]`,
			},
		},
		{
			name: "update names the version it edited",
			req:  request{method: "PUT", path: "/api/v1/workflows/wf-1", body: flowBody[:len(flowBody)-1] + `,"version":2}`, headers: cookie},
			setup: func(w *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				w.EXPECT().Update(mock.Anything, mock.Anything, domain.WorkflowID("wf-1"), 2, mock.MatchedBy(exactDefinition)).Return(flow, nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "stale update is a conflict",
			req:  request{method: "PUT", path: "/api/v1/workflows/wf-1", body: flowBody[:len(flowBody)-1] + `,"version":1}`, headers: cookie},
			setup: func(w *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				w.EXPECT().Update(mock.Anything, mock.Anything, mock.Anything, 1, mock.Anything).Return(domain.Workflow{}, domain.ErrConflict)
			},
			wantStatus: http.StatusConflict,
			wantBody:   []string{`"code":"conflict"`},
		},
		{
			name: "list",
			req:  request{method: "GET", path: "/api/v1/workflows?limit=10", headers: cookie},
			setup: func(w *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				w.EXPECT().List(mock.Anything, mock.Anything, ports.PageFilter{Limit: 10}).Return(ports.Page[domain.Workflow]{Items: []domain.Workflow{flow}}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"workflows":[{"id":"wf-1"`},
		},
		{
			name: "get someone else's workflow",
			req:  request{method: "GET", path: "/api/v1/workflows/wf-9", headers: cookie},
			setup: func(w *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				w.EXPECT().Get(mock.Anything, mock.Anything, domain.WorkflowID("wf-9")).Return(domain.Workflow{}, domain.ErrNotFound)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "delete",
			req:  request{method: "DELETE", path: "/api/v1/workflows/wf-1", headers: cookie},
			setup: func(w *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				w.EXPECT().Delete(mock.Anything, mock.Anything, domain.WorkflowID("wf-1")).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "activate",
			req:  request{method: "POST", path: "/api/v1/workflows/wf-1/activate", headers: cookie},
			setup: func(w *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				on := flow
				on.Active = true
				w.EXPECT().Activate(mock.Anything, mock.Anything, domain.WorkflowID("wf-1")).Return(on, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"active":true`},
		},
		{
			name: "deactivate",
			req:  request{method: "POST", path: "/api/v1/workflows/wf-1/deactivate", headers: cookie},
			setup: func(w *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				w.EXPECT().SwitchOff(mock.Anything, mock.Anything, domain.WorkflowID("wf-1"), false).Return(flow, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"active":false`},
		},
		{
			name: "deactivate, its resting orders cancelled",
			req:  request{method: "POST", path: "/api/v1/workflows/wf-1/deactivate?cancelOrders=true", headers: cookie},
			setup: func(w *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				w.EXPECT().SwitchOff(mock.Anything, mock.Anything, domain.WorkflowID("wf-1"), true).Return(flow, nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "run without a body uses the manual trigger",
			req:  request{method: "POST", path: "/api/v1/workflows/wf-1/run", headers: cookie},
			setup: func(w *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				w.EXPECT().Run(mock.Anything, mock.Anything, domain.WorkflowID("wf-1"), domain.NodeID("")).Return(exec, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`"status":"succeeded"`, `"durationMs":1500`, `"output":{"main":1}`,
				`"main":[{"json":{"price":0.020}}]`, `"error":[{"json":{"v":1},"error":"boom"}]`,
			},
		},
		{
			name: "run a named trigger",
			req:  request{method: "POST", path: "/api/v1/workflows/wf-1/run", body: `{"trigger":"m2"}`, headers: cookie},
			setup: func(w *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				w.EXPECT().Run(mock.Anything, mock.Anything, domain.WorkflowID("wf-1"), domain.NodeID("m2")).Return(exec, nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "executions",
			req:  request{method: "GET", path: "/api/v1/workflows/wf-1/executions?cursor=ex-9", headers: cookie},
			setup: func(w *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				w.EXPECT().Executions(mock.Anything, mock.Anything, domain.WorkflowID("wf-1"), ports.PageFilter{Cursor: "ex-9"}).
					Return(ports.Page[domain.Execution]{Items: []domain.Execution{exec}, NextCursor: "ex-1"}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"executions":[{"id":"ex-1"`, `"nextCursor":"ex-1"`},
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				assert.NotContains(t, w.Body.String(), "samples", "lists stay small")
			},
		},
		{
			name: "one execution",
			req:  request{method: "GET", path: "/api/v1/workflows/wf-1/executions/ex-1", headers: cookie},
			setup: func(w *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				w.EXPECT().Execution(mock.Anything, mock.Anything, domain.WorkflowID("wf-1"), domain.ExecutionID("ex-1")).Return(exec, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"workflowVersion":2`, `"triggeredBy":"manual"`, `"samples":{`},
		},
		{
			name: "a waiting execution says where and until when",
			req:  request{method: "GET", path: "/api/v1/workflows/wf-1/executions/ex-2", headers: cookie},
			setup: func(w *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				w.EXPECT().Execution(mock.Anything, mock.Anything, domain.WorkflowID("wf-1"), domain.ExecutionID("ex-2")).Return(domain.Execution{
					ID: "ex-2", Status: domain.ExecutionWaiting, StartedAt: created, WaitingOn: "pause", WaitingUntil: created.Add(time.Hour),
					Runs: []domain.NodeRun{{NodeID: "pause", Status: domain.NodeRunWaiting, StartedAt: created}},
				}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`"status":"waiting"`, `"waitingOn":"pause","waitingUntil":"2026-10-08T13:00:00Z"`,
				`{"nodeId":"pause","status":"waiting","startedAt":"2026-10-08T12:00:00Z"}`,
			},
		},
		{
			name: "cancel a waiting execution",
			req:  request{method: "POST", path: "/api/v1/workflows/wf-1/executions/ex-2/cancel", headers: cookie},
			setup: func(w *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				w.EXPECT().CancelExecution(mock.Anything, mock.Anything, domain.WorkflowID("wf-1"), domain.ExecutionID("ex-2")).
					Return(domain.Execution{ID: "ex-2", Status: domain.ExecutionCancelled, Error: "cancelled by you"}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"status":"cancelled"`, `"error":"cancelled by you"`},
		},
		{
			name: "only a waiting one",
			req:  request{method: "POST", path: "/api/v1/workflows/wf-1/executions/ex-1/cancel", headers: cookie},
			setup: func(w *httpapimocks.MockWorkflowService, _ *httpapimocks.MockNodeTypes) {
				w.EXPECT().CancelExecution(mock.Anything, mock.Anything, domain.WorkflowID("wf-1"), domain.ExecutionID("ex-1")).
					Return(domain.Execution{}, fmt.Errorf("%w: it is succeeded", domain.ErrNotWaiting))
			},
			wantStatus: http.StatusConflict,
			wantBody:   []string{`"code":"not_waiting"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authSvc := httpapimocks.NewMockAuthService(t)
			withSession(authSvc, alice)

			wfSvc := httpapimocks.NewMockWorkflowService(t)
			nodeTypes := httpapimocks.NewMockNodeTypes(t)
			tt.setup(wfSvc, nodeTypes)

			srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
				httpapi.Services{Auth: authSvc, Workflows: wfSvc, NodeTypes: nodeTypes})
			require.NoError(t, err)

			w := tt.req.do(t, srv)

			assert.Equal(t, tt.wantStatus, w.Code, w.Body.String())

			for _, s := range tt.wantBody {
				assert.Contains(t, w.Body.String(), s)
			}

			if tt.check != nil {
				tt.check(t, w)
			}
		})
	}
}
