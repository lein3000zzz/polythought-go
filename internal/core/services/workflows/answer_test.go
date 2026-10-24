package workflows_test

import (
	"context"
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
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
	workflowsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/workflows"
)

var asked = ports.WaitRecord{
	ExecutionID: "ex-1", WorkflowID: "wf-1", OwnerID: "u-1", NodeID: "ask", Key: "k-1",
	Question: &ports.WaitQuestion{HTML: "Buy <b>YES</b>?", Options: []string{"Buy", "Skip"}},
}

// waiting is a service whose workflow wf-1 has execution ex-1 waiting, with rec.
func waiting(t *testing.T, rec ports.WaitRecord) (*workflows.Service, *workflowsmocks.MockWaits) {
	t.Helper()

	repo := portsmocks.NewMockWorkflowRepository(t)
	executions := portsmocks.NewMockExecutionRepository(t)
	w := workflowsmocks.NewMockWaits(t)

	repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(stored, nil).Maybe()
	executions.EXPECT().Get(mock.Anything, domain.WorkflowID("wf-1"), domain.ExecutionID("ex-1")).
		Return(domain.Execution{ID: "ex-1", WorkflowID: "wf-1", Status: domain.ExecutionWaiting}, nil).Maybe()
	w.EXPECT().Waiting(mock.Anything, domain.UserID("u-1"), domain.ExecutionID("ex-1")).Return(rec, nil).Maybe()

	svc := workflows.New(repo, executions, workflowsmocks.NewMockValidator(t), workflowsmocks.NewMockExecutor(t),
		workflowsmocks.NewMockSupervisor(t), logging.Noop(), workflows.WithClock(func() time.Time { return now }),
		workflows.WithWaits(w))

	return svc, w
}

// What a waiting execution asks is there to see; answering it, for a token or an agent,
// takes the user's leave, and says who answered (D405).
func TestAnswer(t *testing.T) {
	ctx := context.Background()
	agent := auth.Principal{User: domain.User{ID: "u-1", Agents: domain.AgentAccess{Answer: true}}, GrantID: "g-1", Agent: "Claude Code", Scopes: domain.Scopes}

	t.Run("seen", func(t *testing.T) {
		svc, _ := waiting(t, asked)

		q, err := svc.Question(ctx, agentOff, "wf-1", "ex-1")
		require.NoError(t, err)
		assert.Equal(t, asked.Question, q, "seeing it takes no leave")
	})

	t.Run("a wait for its time asks nothing", func(t *testing.T) {
		timed := asked
		timed.Key, timed.Question = "", nil
		svc, _ := waiting(t, timed)

		q, err := svc.Question(ctx, alice, "wf-1", "ex-1")
		require.NoError(t, err)
		assert.Nil(t, q)

		require.ErrorIs(t, svc.Answer(ctx, alice, "wf-1", "ex-1", 0), domain.ErrNotWaiting)
	})

	t.Run("answered by an agent, where the user lets it", func(t *testing.T) {
		svc, w := waiting(t, asked)

		require.ErrorIs(t, svc.Answer(ctx, agentOff, "wf-1", "ex-1", 0), domain.ErrForbidden)

		w.EXPECT().Answer(mock.Anything, domain.UserID("u-1"), domain.ExecutionID("ex-1"), "k-1",
			map[string]any{"option": 1, engine.AnsweredBy: "Claude Code"}).Return(nil)
		require.NoError(t, svc.Answer(ctx, agent, "wf-1", "ex-1", 1))
	})

	t.Run("by the user, as a button press", func(t *testing.T) {
		svc, w := waiting(t, asked)

		w.EXPECT().Answer(mock.Anything, domain.UserID("u-1"), domain.ExecutionID("ex-1"), "k-1", map[string]any{"option": 0}).Return(nil)
		require.NoError(t, svc.Answer(ctx, alice, "wf-1", "ex-1", 0))
	})

	t.Run("an option it does not have", func(t *testing.T) {
		svc, _ := waiting(t, asked)

		require.ErrorIs(t, svc.Answer(ctx, agent, "wf-1", "ex-1", 2), domain.ErrInvalidInput)
	})

	t.Run("carried on meanwhile", func(t *testing.T) {
		svc, w := waiting(t, asked)

		w.EXPECT().Answer(mock.Anything, domain.UserID("u-1"), domain.ExecutionID("ex-1"), "k-1", mock.Anything).Return(domain.ErrNotFound)
		require.ErrorIs(t, svc.Answer(ctx, agent, "wf-1", "ex-1", 0), domain.ErrNotWaiting)
	})
}
