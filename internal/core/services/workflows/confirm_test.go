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
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
	workflowsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/workflows"
)

// holder holds every action it is asked to, as the confirmations service does where the
// user has them wait.
type holder struct {
	kinds []domain.ConfirmKind
	whats []string
}

func (h *holder) Hold(_ context.Context, _ auth.Principal, a domain.Ask) error {
	h.kinds, h.whats = append(h.kinds, a.Kind), append(h.whats, a.What)
	return &domain.AwaitingError{Confirmation: domain.Confirmation{ID: "c-1"}}
}

func withConfirmations(t *testing.T, live bool) (*workflows.Service, deps, *holder) {
	t.Helper()

	d := deps{
		repo:       portsmocks.NewMockWorkflowRepository(t),
		executions: portsmocks.NewMockExecutionRepository(t),
		validator:  workflowsmocks.NewMockValidator(t),
		executor:   workflowsmocks.NewMockExecutor(t),
		supervisor: workflowsmocks.NewMockSupervisor(t),
	}
	d.validator.EXPECT().Validate(mock.Anything, mock.Anything).Return(nil).Maybe()

	scope := domain.ScopePaper
	if live {
		scope = domain.ScopeLive
	}

	h := &holder{}
	svc := workflows.New(d.repo, d.executions, d.validator, d.executor, d.supervisor, logging.Noop(),
		workflows.WithClock(func() time.Time { return now }), workflows.WithLimits(workflows.DefaultLimits()),
		workflows.WithTradeScope(func(context.Context, domain.Workflow) (domain.Scope, error) { return scope, nil }),
		workflows.WithConfirmations(h))

	return svc, d, h
}

func manualFlow() domain.Workflow {
	w := stored
	w.Nodes = []domain.NodeSpec{{ID: "manual", Type: "trigger.manual"}}

	return w
}

// A token's or an agent's switching on, running or changing a workflow that trades live
// waits for its user where they say so (D406); on paper it goes ahead.
func TestLiveActionsWait(t *testing.T) {
	ctx := context.Background()

	t.Run("switching on and running", func(t *testing.T) {
		svc, d, h := withConfirmations(t, true)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(manualFlow(), nil)
		d.repo.EXPECT().Count(mock.Anything, domain.UserID("u-1")).Return(1, 0, nil)

		_, err := svc.Activate(ctx, agentOn, "wf-1")
		require.ErrorIs(t, err, domain.ErrAwaitsConfirmation)

		_, err = svc.Run(ctx, agentOn, "wf-1", "")
		require.ErrorIs(t, err, domain.ErrAwaitsConfirmation, "nothing executed meanwhile")

		assert.Equal(t, []domain.ConfirmKind{domain.ConfirmSwitchOn, domain.ConfirmRun}, h.kinds)
		assert.Equal(t, `Run "Spread watch" by hand, which trades live`, h.whats[1])
	})

	t.Run("changing one that is on", func(t *testing.T) {
		svc, d, h := withConfirmations(t, true)

		on := manualFlow()
		on.Active = true
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(on, nil)

		_, err := svc.Update(ctx, agentOn, "wf-1", 3, definitionOf(manualFlow()))
		require.ErrorIs(t, err, domain.ErrAwaitsConfirmation, "nothing saved meanwhile")
		assert.Equal(t, []domain.ConfirmKind{domain.ConfirmChange}, h.kinds)
	})

	t.Run("on paper, and confirmed", func(t *testing.T) {
		svc, d, h := withConfirmations(t, false)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(manualFlow(), nil)
		d.repo.EXPECT().Count(mock.Anything, domain.UserID("u-1")).Return(1, 0, nil)
		d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), true, now, mock.Anything).Return(nil)
		d.supervisor.EXPECT().Start(mock.Anything)

		_, err := svc.Activate(ctx, agentOn, "wf-1")
		require.NoError(t, err)
		assert.Empty(t, h.kinds, "nothing to hold on paper")

		live, d, h := withConfirmations(t, true)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(manualFlow(), nil)
		d.repo.EXPECT().Count(mock.Anything, domain.UserID("u-1")).Return(1, 0, nil)
		d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), true, now, mock.Anything).Return(nil)
		d.supervisor.EXPECT().Start(mock.Anything)

		confirmed := agentOn
		confirmed.Confirmed = true

		_, err = live.Activate(ctx, confirmed, "wf-1")
		require.NoError(t, err)
		assert.Empty(t, h.kinds, "the user confirmed it")
	})
}
