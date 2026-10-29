package workflows_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
	workflowsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/workflows"
)

// switches is the history: what it was told.
type switches struct {
	mu   sync.Mutex
	kept []domain.Switch
}

func (s *switches) Switched(_ context.Context, sw domain.Switch) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.kept = append(s.kept, sw)
}

func withHistory(t *testing.T) (*workflows.Service, deps, *switches) {
	t.Helper()

	d := deps{
		repo:       portsmocks.NewMockWorkflowRepository(t),
		executions: portsmocks.NewMockExecutionRepository(t),
		validator:  workflowsmocks.NewMockValidator(t),
		executor:   workflowsmocks.NewMockExecutor(t),
		supervisor: workflowsmocks.NewMockSupervisor(t),
	}
	d.validator.EXPECT().Validate(mock.Anything, mock.Anything).Return(nil).Maybe()

	h := &switches{}
	svc := workflows.New(d.repo, d.executions, d.validator, d.executor, d.supervisor, logging.Noop(),
		workflows.WithClock(func() time.Time { return now }), workflows.WithLimits(workflows.DefaultLimits()),
		workflows.WithHistory(h))

	return svc, d, h
}

// Who switched a workflow on or off and why is kept (D433): only switches that change it.
func TestSwitchHistory(t *testing.T) {
	ctx := context.Background()

	t.Run("switched on by an agent, saying why", func(t *testing.T) {
		svc, d, h := withHistory(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(stored, nil)
		d.repo.EXPECT().Count(mock.Anything, domain.UserID("u-1")).Return(1, 0, nil)
		d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), true, now, mock.Anything).Return(nil)
		d.supervisor.EXPECT().Start(mock.Anything)

		why := claude
		why.Reason = "the subagent for weather asked"

		_, err := svc.Activate(ctx, why, "wf-1")
		require.NoError(t, err)

		require.Len(t, h.kept, 1)
		assert.Equal(t, domain.SwitchOn, h.kept[0].Kind)
		assert.Equal(t, domain.WorkflowID("wf-1"), h.kept[0].WorkflowID)
		assert.Equal(t, "Claude Code", h.kept[0].By.Name)
		assert.Equal(t, "the subagent for weather asked", h.kept[0].Reason)
	})

	t.Run("on already, or off already: nothing kept", func(t *testing.T) {
		svc, d, h := withHistory(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(active(stored), nil).Once()
		d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), true, now, mock.Anything).Return(nil)
		d.supervisor.EXPECT().Start(mock.Anything)

		_, err := svc.Activate(ctx, alice, "wf-1")
		require.NoError(t, err)

		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(stored, nil).Once()
		d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), false, now, domain.Actor{}).Return(nil)
		d.supervisor.EXPECT().Stop(domain.WorkflowID("wf-1"))

		_, err = svc.Deactivate(ctx, alice, "wf-1")
		require.NoError(t, err)

		assert.Empty(t, h.kept)
	})

	t.Run("switched off by the user", func(t *testing.T) {
		svc, d, h := withHistory(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(active(stored), nil)
		d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), false, now, domain.Actor{}).Return(nil)
		d.supervisor.EXPECT().Stop(domain.WorkflowID("wf-1"))

		_, err := svc.Deactivate(ctx, alice, "wf-1")
		require.NoError(t, err)

		require.Len(t, h.kept, 1)
		assert.Equal(t, domain.SwitchOff, h.kept[0].Kind)
		assert.True(t, h.kept[0].By.IsUser())
		assert.False(t, h.kept[0].Polythought)
	})

	t.Run("by Polythought: paused, or the account disabled", func(t *testing.T) {
		svc, d, h := withHistory(t)
		gone := active(domain.Workflow{ID: "wf-1", OwnerID: alice.User.ID})
		d.repo.EXPECT().ListActive(mock.Anything).Return([]domain.Workflow{gone}, nil)
		d.repo.EXPECT().SetActive(mock.Anything, alice.User.ID, domain.WorkflowID("wf-1"), false, now, domain.Actor{}).Return(nil)
		d.repo.EXPECT().DeactivateAll(mock.Anything, alice.User.ID, now).Return([]domain.WorkflowID{"wf-2"}, nil)
		d.supervisor.EXPECT().Stop(mock.Anything)

		_, err := svc.Pause(ctx, func(domain.Workflow) string { return "the plugin weather is disabled" })
		require.NoError(t, err)
		require.NoError(t, svc.StopAll(ctx, alice.User.ID))

		require.Len(t, h.kept, 2)
		assert.Equal(t, domain.Switch{WorkflowID: "wf-1", Kind: domain.SwitchOff, Polythought: true, Reason: "the plugin weather is disabled"}, h.kept[0])
		assert.Equal(t, domain.Switch{WorkflowID: "wf-2", Kind: domain.SwitchOff, Polythought: true, Reason: "an admin disabled the account"}, h.kept[1])
	})
}
