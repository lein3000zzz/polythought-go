package httpapi_test

import (
	"context"
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
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/tasks"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

// desk is the tasks service: a run waiting on a task Claude Code has; what it is asked
// to do kept.
type desk struct {
	handedBack bool
	answers    []any
	refuse     error
}

func (*desk) Status(_ context.Context, _ auth.Principal, wf domain.WorkflowID, id domain.ExecutionID) (tasks.Status, error) {
	if wf != "wf-1" || id != "ex-1" {
		return tasks.Status{}, domain.ErrNotFound
	}

	until := time.Date(2026, 10, 24, 12, 10, 0, 0, time.UTC)

	return tasks.Status{
		Task: tasks.Task{
			ID: id, WorkflowID: wf, Until: until.Add(time.Hour), LeaseUntil: until,
			Task: ports.WaitTask{
				Call: "each", AnswerAs: "fields", Schema: json.RawMessage(`{"type":"object"}`),
				Items: []ports.TaskItem{{Prompt: "Look into Rain?", Item: map[string]any{"question": "Rain?"}}},
			},
		},
		HeldBy: "Claude Code",
	}, nil
}

func (d *desk) HandBack(context.Context, auth.Principal, domain.WorkflowID, domain.ExecutionID) error {
	d.handedBack = true
	return nil
}

func (d *desk) AnswerYourself(_ context.Context, _ auth.Principal, _ domain.WorkflowID, _ domain.ExecutionID, _ any, answers []any) error {
	if d.refuse != nil {
		return d.refuse
	}

	d.answers = answers

	return nil
}

// taking says the token t-9 waits for tasks.
type taking struct{}

func (taking) Taking(domain.UserID) map[string]bool { return map[string]bool{"token:t-9": true} }

func taskServer(t *testing.T, d *desk) (*httpapi.Server, *httpapimocks.MockAuthService) {
	t.Helper()

	authSvc := httpapimocks.NewMockAuthService(t)

	srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
		httpapi.Services{Auth: authSvc, Tasks: d, TaskAgents: taking{}})
	require.NoError(t, err)

	return srv, authSvc
}

// A run's task, as the app shows it, handed back and answered by the user at a browser
// (D413); and the tokens taking tasks, marked.
func TestTaskRoutes(t *testing.T) {
	t.Run("the task and who has it, for a token too", func(t *testing.T) {
		srv, authSvc := taskServer(t, &desk{})
		withToken(authSvc, alice)

		w := request{method: http.MethodGet, path: "/api/v1/workflows/wf-1/executions/ex-1/task", headers: bearer}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{
			"call": "each", "answerAs": "fields", "schema": {"type": "object"},
			"items": [{"prompt": "Look into Rain?", "item": {"question": "Rain?"}}],
			"until": "2026-10-24T13:10:00Z", "heldBy": "Claude Code", "heldUntil": "2026-10-24T12:10:00Z"
		}`, w.Body.String())

		w = request{method: http.MethodGet, path: "/api/v1/workflows/wf-1/executions/ex-2/task", headers: bearer}.do(t, srv)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("handed back and answered at a browser alone", func(t *testing.T) {
		d := &desk{}
		srv, authSvc := taskServer(t, d)
		withToken(authSvc, alice)

		for _, path := range []string{"task/hand-back", "task/answer"} {
			w := request{method: http.MethodPost, path: "/api/v1/workflows/wf-1/executions/ex-1/" + path, body: `{}`, headers: bearer}.do(t, srv)
			assert.Equal(t, http.StatusForbidden, w.Code, path)
		}

		srv, authSvc = taskServer(t, d)
		withSession(authSvc, alice)

		w := request{method: http.MethodPost, path: "/api/v1/workflows/wf-1/executions/ex-1/task/hand-back", headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
		assert.True(t, d.handedBack)

		w = request{method: http.MethodPost, path: "/api/v1/workflows/wf-1/executions/ex-1/task/answer", body: `{"answers": [{"watch": true}]}`, headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
		assert.Equal(t, []any{map[string]any{"watch": true}}, d.answers)

		d.refuse = &domain.ValidationError{Base: domain.ErrInvalidInput, Problems: []domain.Problem{{Param: "answers[0]", Message: "watch is missing"}}}
		w = request{method: http.MethodPost, path: "/api/v1/workflows/wf-1/executions/ex-1/task/answer", body: `{"answers": [{}]}`, headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "watch is missing")
	})

	t.Run("the tokens taking tasks, marked", func(t *testing.T) {
		srv, authSvc := taskServer(t, &desk{})
		withSession(authSvc, alice)
		authSvc.EXPECT().ListAPITokens(mock.Anything, mock.Anything).Return([]domain.APIToken{{ID: "t-9", Name: "laptop"}, {ID: "t-8", Name: "ci"}}, nil)

		w := request{method: http.MethodGet, path: "/api/v1/tokens", headers: cookie}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		var body struct {
			Tokens []struct {
				ID          string
				TakingTasks bool
			}
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.True(t, body.Tokens[0].TakingTasks)
		assert.False(t, body.Tokens[1].TakingTasks)
	})
}
