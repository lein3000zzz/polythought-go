package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestCodeTypes(t *testing.T) {
	authSvc := httpapimocks.NewMockAuthService(t)
	withSession(authSvc, alice)

	types := httpapimocks.NewMockCodeTypes(t)
	types.EXPECT().Declarations(mock.Anything, mock.MatchedBy(func(p auth.Principal) bool { return p.User.ID == alice.ID })).
		Return([]ports.CodeFile{{Path: "index.d.ts", Content: `export * from "./sdk";`}}, nil)

	srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
		httpapi.Services{Auth: authSvc, CodeTypes: types})
	require.NoError(t, err)

	w := request{method: "GET", path: "/api/v1/code/types", headers: cookie}.do(t, srv)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"files":[{"path":"index.d.ts","content":"export * from \"./sdk\";"}]}`, w.Body.String())

	w = request{method: "GET", path: "/api/v1/code/types"}.do(t, srv)
	assert.Equal(t, http.StatusUnauthorized, w.Code, "signed in only")
}

func TestWorkflowCode(t *testing.T) {
	authSvc := httpapimocks.NewMockAuthService(t)
	withSession(authSvc, alice)

	code := httpapimocks.NewMockWorkflowCode(t)
	srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
		httpapi.Services{Auth: authSvc, WorkflowCode: code})
	require.NoError(t, err)

	byAlice := mock.MatchedBy(func(p auth.Principal) bool { return p.User.ID == alice.ID })
	post := func(source string) request {
		return request{method: "POST", path: "/api/v1/code/workflow", body: `{"source":` + quoteJSON(source) + `}`, headers: cookie}
	}

	t.Run("the workflow it builds", func(t *testing.T) {
		code.EXPECT().Build(mock.Anything, byAlice, "export default it").Return(domain.Workflow{
			Name: "Spread watch",
			Nodes: []domain.NodeSpec{
				{ID: "manual", Type: "trigger.manual"},
				{ID: "log", Type: "output.log", Name: "Log", Params: map[string]any{"message": "hi"}, ContinueOnFail: true},
			},
			Connections: []domain.Connection{{From: "manual", FromPort: "main", To: "log", ToPort: "main"}},
		}, nil).Once()

		w := post("export default it").do(t, srv)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"workflow": {
			"name": "Spread watch",
			"nodes": [
				{"id": "manual", "type": "trigger.manual", "position": {"x": 0, "y": 0}},
				{"id": "log", "type": "output.log", "name": "Log", "params": {"message": "hi"}, "continueOnFail": true, "position": {"x": 0, "y": 0}}
			],
			"connections": [{"from": "manual", "fromPort": "main", "to": "log", "toPort": "main"}]
		}}`, w.Body.String())
	})

	t.Run("code that does not compile, with where", func(t *testing.T) {
		code.EXPECT().Build(mock.Anything, byAlice, "export default (").Return(domain.Workflow{}, &domain.CodeError{
			Kind: domain.CodeDidNotCompile, Message: "line 1, column 17: Unexpected end of file",
			Problems: []domain.CodeProblem{{Line: 1, Column: 17, Message: "Unexpected end of file"}},
		}).Once()

		w := post("export default (").do(t, srv)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.JSONEq(t, `{"error": {
			"code": "invalid_code", "message": "line 1, column 17: Unexpected end of file",
			"problems": [{"line": 1, "column": 17, "message": "Unexpected end of file"}]
		}}`, w.Body.String())
	})

	t.Run("code that throws", func(t *testing.T) {
		code.EXPECT().Build(mock.Anything, byAlice, "throw").Return(domain.Workflow{}, &domain.CodeError{
			Kind: domain.CodeThrew, Message: `Error: node "a" is declared twice`,
		}).Once()

		w := post("throw").do(t, srv)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.JSONEq(t, `{"error": {"code": "invalid_code", "message": "Error: node \"a\" is declared twice"}}`, w.Body.String())
	})

	t.Run("code off", func(t *testing.T) {
		code.EXPECT().Build(mock.Anything, byAlice, "off").Return(domain.Workflow{}, domain.ErrCodeOff).Once()

		w := post("off").do(t, srv)
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), `"code":"code_off"`)
	})

	t.Run("signed in only", func(t *testing.T) {
		w := request{method: "POST", path: "/api/v1/code/workflow", body: `{"source":""}`}.do(t, srv)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
