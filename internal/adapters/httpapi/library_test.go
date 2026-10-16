package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/customnodes"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/library"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestLibraryRoutes(t *testing.T) {
	scenario := library.Entry{
		Template: domain.Template{
			ID: "chat-alerts", Name: "Alerts", Summary: "Get pinged", Description: "How it works.",
			Nodes: []domain.NodeSpec{
				{ID: "every", Type: "trigger.schedule", Notes: "Every 5 minutes"},
				{ID: "chat", Type: "output.webhook"},
				{ID: "again", Type: "output.webhook"},
			},
			Connections: []domain.Connection{{From: "every", FromPort: "main", To: "chat", ToPort: "main"}},
		},
		Inputs: []library.Input{{NodeID: "chat", Param: engine.ParamSpec{Name: "url", Label: "Webhook URL", Kind: engine.ParamString, Required: true, Sensitive: true}}},
	}
	shared := library.Entry{Template: domain.Template{ID: "tpl-1", AuthorID: alice.ID, Name: "Mine", UpdatedAt: created}, Mine: true}

	tests := []struct {
		name       string
		req        request
		setup      func(l *httpapimocks.MockLibraryService)
		wantStatus int
		wantBody   []string
		check      func(t *testing.T, w *ut.ResponseRecorder)
	}{
		{
			name: "the list leaves out the graphs",
			req:  request{method: "GET", path: "/api/v1/library", headers: cookie},
			setup: func(l *httpapimocks.MockLibraryService) {
				l.EXPECT().List(mock.Anything, mock.Anything, ports.PageFilter{}).Return(ports.Page[library.Entry]{Items: []library.Entry{scenario, shared}, NextCursor: "tpl-1"}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`"id":"chat-alerts"`, `"builtIn":true`, `"nodeTypes":["trigger.schedule","output.webhook"]`,
				`"id":"tpl-1"`, `"mine":true`, `"nextCursor":"tpl-1"`,
			},
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				assert.NotContains(t, w.Body.String(), `"nodes"`)
			},
		},
		{
			name: "one template comes with its graph and inputs",
			req:  request{method: "GET", path: "/api/v1/library/chat-alerts", headers: cookie},
			setup: func(l *httpapimocks.MockLibraryService) {
				l.EXPECT().Get(mock.Anything, mock.Anything, domain.TemplateID("chat-alerts")).Return(scenario, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`"notes":"Every 5 minutes"`, `"connections":[{"from":"every"`,
				`"inputs":[{"nodeId":"chat","param":{"name":"url","label":"Webhook URL","kind":"string","required":true,"sensitive":true}}]`,
			},
		},
		{
			name: "one template comes with the custom nodes it brings, code included",
			req:  request{method: "GET", path: "/api/v1/library/tpl-1", headers: cookie},
			setup: func(l *httpapimocks.MockLibraryService) {
				def := domain.CustomNode{
					Name: "split", Label: "Split", Outputs: []string{"wide", "tight"},
					Settings: []domain.CustomSetting{{Name: "threshold", Label: "threshold", Kind: "decimal", Default: "0.02"}},
					Items:    domain.CustomItems{Passed: true}, Source: "export function run(items) { return { wide: items } }",
				}
				shared := library.Entry{
					Template:    domain.Template{ID: "tpl-1", AuthorID: "u-2", Name: "Spread watch", CustomNodes: []domain.CustomNode{def}},
					CustomTypes: []engine.NodeType{customnodes.TypeOf(def)},
				}
				l.EXPECT().Get(mock.Anything, mock.Anything, domain.TemplateID("tpl-1")).Return(shared, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`"customNodes":[{"name":"split","label":"Split"`,
				`"source":"export function run(items) { return { wide: items } }","type":"custom.split"`,
				`"nodeType":{"name":"custom.split","label":"Split","trigger":false,"inputs":["main"],"outputs":["wide","tight"]`,
			},
		},
		{
			name: "using a template passes the values on, numbers exact",
			req: request{
				method: "POST", path: "/api/v1/library/chat-alerts/use", headers: cookie,
				body: `{"name":"Mine","values":{"chat":{"url":"https://hook","limit":0.020}}}`,
			},
			setup: func(l *httpapimocks.MockLibraryService) {
				l.EXPECT().Use(mock.Anything, mock.Anything, domain.TemplateID("chat-alerts"), library.UseInput{
					Name:   "Mine",
					Values: map[domain.NodeID]map[string]any{"chat": {"url": "https://hook", "limit": json.Number("0.020")}},
				}).Return(flow, nil)
			},
			wantStatus: http.StatusCreated,
			wantBody:   []string{`"id":"wf-1"`},
		},
		{
			name: "an input left empty is a problem on its node",
			req:  request{method: "POST", path: "/api/v1/library/chat-alerts/use", headers: cookie, body: `{}`},
			setup: func(l *httpapimocks.MockLibraryService) {
				var v domain.ValidationError
				v.Add(domain.Problem{NodeID: "chat", Param: "url", Message: `node "chat": missing required param "url"`})
				l.EXPECT().Use(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(domain.Workflow{}, v.Err())
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"problems":[{"nodeId":"chat","param":"url"`},
		},
		{
			name: "deleting someone else's is forbidden",
			req:  request{method: "DELETE", path: "/api/v1/library/tpl-9", headers: cookie},
			setup: func(l *httpapimocks.MockLibraryService) {
				l.EXPECT().Delete(mock.Anything, mock.Anything, domain.TemplateID("tpl-9")).Return(domain.ErrForbidden)
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "share a workflow",
			req:  request{method: "PUT", path: "/api/v1/workflows/wf-1/share", headers: cookie, body: `{"summary":"Short","description":"Long"}`},
			setup: func(l *httpapimocks.MockLibraryService) {
				l.EXPECT().Share(mock.Anything, mock.Anything, domain.WorkflowID("wf-1"), library.ShareInput{Summary: "Short", Description: "Long"}).Return(shared, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"id":"tpl-1"`, `"builtIn":false`, `"updatedAt":"2026-10-08T12:00:00Z"`},
		},
		{
			name: "a workflow not shared has no copy",
			req:  request{method: "GET", path: "/api/v1/workflows/wf-1/share", headers: cookie},
			setup: func(l *httpapimocks.MockLibraryService) {
				l.EXPECT().SharedCopy(mock.Anything, mock.Anything, domain.WorkflowID("wf-1")).Return(library.Entry{}, domain.ErrNotFound)
			},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authSvc := httpapimocks.NewMockAuthService(t)
			withSession(authSvc, alice)

			lib := httpapimocks.NewMockLibraryService(t)
			tt.setup(lib)

			srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
				httpapi.Services{Auth: authSvc, Library: lib})
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
