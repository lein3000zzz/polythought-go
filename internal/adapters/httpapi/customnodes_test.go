package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/customnodes"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

const splitBody = `{
	"name": "spread-split", "label": "Spread split",
	"settings": [{"name": "threshold", "kind": "decimal", "default": 0.02}],
	"outputs": ["wide", "tight"],
	"items": {"passed": false, "fields": [{"name": "best", "kind": "object", "optional": false, "nullable": true,
		"fields": [{"name": "price", "kind": "decimal", "optional": false, "nullable": false}]}]},
	"source": "export function run(items) { return { wide: items } }"
}`

func storedSplit() domain.CustomNode {
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	return domain.CustomNode{
		ID: "n-1", OwnerID: alice.ID, Name: "spread-split", Label: "Spread split",
		Settings: []domain.CustomSetting{{Name: "threshold", Label: "threshold", Kind: "decimal", Default: json.Number("0.02")}},
		Outputs:  []string{"wide", "tight"},
		Items: domain.CustomItems{Fields: []domain.CustomField{
			{Name: "best", Kind: "object", Nullable: true, Fields: []domain.CustomField{{Name: "price", Kind: "decimal"}}},
		}},
		Source: "export function run(items) { return { wide: items } }", Version: 2, CreatedAt: at, UpdatedAt: at,
	}
}

func TestCustomNodeRoutes(t *testing.T) {
	breaks := &domain.BreaksError{Workflows: []domain.BrokenWorkflow{{
		ID: "wf-1", Name: "Spread watch",
		Problems: []domain.Problem{{NodeID: "split", Message: `node "split" (custom.spread-split) has no output port "wide"`}},
	}}}

	tests := []struct {
		name       string
		req        request
		setup      func(s *httpapimocks.MockCustomNodeService)
		wantStatus int
		wantBody   []string
	}{
		{
			name: "list, with each one's type",
			req:  request{method: "GET", path: "/api/v1/custom-nodes", headers: cookie},
			setup: func(s *httpapimocks.MockCustomNodeService) {
				s.EXPECT().List(mock.Anything, mock.Anything).Return([]domain.CustomNode{storedSplit()}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`"id":"n-1","type":"custom.spread-split","version":2`, `"name":"spread-split"`,
				`"settings":[{"name":"threshold","label":"threshold","kind":"decimal","default":0.02}]`,
				`"items":{"passed":false,"fields":[{"name":"best","kind":"object","optional":false,"nullable":true,"fields":[{"name":"price"`,
			},
		},
		{
			name: "create, numbers kept exact",
			req:  request{method: "POST", path: "/api/v1/custom-nodes", body: splitBody, headers: cookie},
			setup: func(s *httpapimocks.MockCustomNodeService) {
				s.EXPECT().Create(mock.Anything, mock.Anything, mock.MatchedBy(func(d customnodes.Definition) bool {
					return d.Name == "spread-split" && d.Settings[0].Default == json.Number("0.02") &&
						len(d.Outputs) == 2 && d.Items.Fields[0].Fields[0].Name == "price" && d.Items.Fields[0].Nullable
				})).Return(storedSplit(), nil)
			},
			wantStatus: http.StatusCreated,
			wantBody:   []string{`"type":"custom.spread-split"`},
		},
		{
			name: "problems say which part of the form",
			req:  request{method: "POST", path: "/api/v1/custom-nodes", body: splitBody, headers: cookie},
			setup: func(s *httpapimocks.MockCustomNodeService) {
				s.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(domain.CustomNode{}, &domain.ValidationError{
					Base: domain.ErrInvalidInput, Problems: []domain.Problem{{Param: "settings.0.default", Message: "the default must be a number"}},
				})
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"invalid_input"`, `{"param":"settings.0.default","message":"the default must be a number"}`},
		},
		{
			name: "not for those who may not write code",
			req:  request{method: "POST", path: "/api/v1/custom-nodes", body: splitBody, headers: cookie},
			setup: func(s *httpapimocks.MockCustomNodeService) {
				s.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(domain.CustomNode{}, domain.ErrCodeOff)
			},
			wantStatus: http.StatusForbidden,
			wantBody:   []string{`"code":"code_off"`},
		},
		{
			name: "a name taken",
			req:  request{method: "POST", path: "/api/v1/custom-nodes", body: splitBody, headers: cookie},
			setup: func(s *httpapimocks.MockCustomNodeService) {
				s.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(domain.CustomNode{}, domain.ErrCustomNodeTaken)
			},
			wantStatus: http.StatusConflict,
			wantBody:   []string{`"code":"custom_node_taken"`},
		},
		{
			name:       "fields it does not know are refused",
			req:        request{method: "POST", path: "/api/v1/custom-nodes", body: `{"name":"x","inputs":["a"]}`, headers: cookie},
			setup:      func(*httpapimocks.MockCustomNodeService) {},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "update, confirmed",
			req: request{method: "PUT", path: "/api/v1/custom-nodes/n-1", headers: cookie,
				body: `{"name":"spread-split","label":"Split","settings":[],"outputs":["tight"],"items":{"passed":true,"fields":[]},"source":"x","version":2,"confirm":true}`},
			setup: func(s *httpapimocks.MockCustomNodeService) {
				s.EXPECT().Update(mock.Anything, mock.Anything, domain.CustomNodeID("n-1"), 2,
					mock.MatchedBy(func(d customnodes.Definition) bool { return d.Label == "Split" && d.Items.Passed }), true).
					Return(storedSplit(), nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "an update that breaks workflows says which and how",
			req: request{method: "PUT", path: "/api/v1/custom-nodes/n-1", headers: cookie,
				body: `{"name":"spread-split","label":"Split","settings":[],"outputs":["tight"],"items":{"passed":true,"fields":[]},"source":"x","version":2}`},
			setup: func(s *httpapimocks.MockCustomNodeService) {
				s.EXPECT().Update(mock.Anything, mock.Anything, domain.CustomNodeID("n-1"), 2, mock.Anything, false).
					Return(domain.CustomNode{}, breaks)
			},
			wantStatus: http.StatusConflict,
			wantBody: []string{
				`"code":"breaks_workflows"`, `"message":"the change breaks workflows that use the node: \"Spread watch\""`,
				`"workflows":[{"id":"wf-1","name":"Spread watch","problems":[{"nodeId":"split","message":"node \"split\" (custom.spread-split) has no output port \"wide\""}]}]`,
			},
		},
		{
			name: "delete, refused while workflows use it",
			req:  request{method: "DELETE", path: "/api/v1/custom-nodes/n-1", headers: cookie},
			setup: func(s *httpapimocks.MockCustomNodeService) {
				s.EXPECT().Delete(mock.Anything, mock.Anything, domain.CustomNodeID("n-1"), false).Return(breaks)
			},
			wantStatus: http.StatusConflict,
			wantBody:   []string{`"code":"breaks_workflows"`, `"id":"wf-1"`},
		},
		{
			name: "delete, confirmed",
			req:  request{method: "DELETE", path: "/api/v1/custom-nodes/n-1?confirm=true", headers: cookie},
			setup: func(s *httpapimocks.MockCustomNodeService) {
				s.EXPECT().Delete(mock.Anything, mock.Anything, domain.CustomNodeID("n-1"), true).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "someone else's is not found",
			req:  request{method: "GET", path: "/api/v1/custom-nodes/n-9", headers: cookie},
			setup: func(s *httpapimocks.MockCustomNodeService) {
				s.EXPECT().Get(mock.Anything, mock.Anything, domain.CustomNodeID("n-9")).Return(domain.CustomNode{}, domain.ErrNotFound)
			},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authSvc := httpapimocks.NewMockAuthService(t)
			withSession(authSvc, alice)

			svc := httpapimocks.NewMockCustomNodeService(t)
			tt.setup(svc)

			srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
				httpapi.Services{Auth: authSvc, CustomNodes: svc})
			require.NoError(t, err)

			w := tt.req.do(t, srv)

			assert.Equal(t, tt.wantStatus, w.Code, w.Body.String())

			for _, s := range tt.wantBody {
				assert.Contains(t, w.Body.String(), s)
			}
		})
	}
}
