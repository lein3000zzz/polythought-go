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

var root = auth.Principal{User: domain.User{ID: "u-0", Role: domain.RoleAdmin}, SessionID: "s-0"}

// overseen is the workflows service as an admin uses it (D435), with its history, its
// owners' notices, its confirmations and personal settings to hide.
func overseen(t *testing.T, opts ...workflows.Option) (*workflows.Service, deps, *switches, *pauses, *holder) {
	t.Helper()

	d := deps{
		repo:       portsmocks.NewMockWorkflowRepository(t),
		executions: portsmocks.NewMockExecutionRepository(t),
		validator:  workflowsmocks.NewMockValidator(t),
		executor:   workflowsmocks.NewMockExecutor(t),
		supervisor: workflowsmocks.NewMockSupervisor(t),
	}

	h, told, held := &switches{}, &pauses{told: map[domain.WorkflowID]string{}}, &holder{}
	svc := workflows.New(d.repo, d.executions, d.validator, d.executor, d.supervisor, logging.Noop(),
		append([]workflows.Option{
			workflows.WithClock(func() time.Time { return now }), workflows.WithLimits(workflows.DefaultLimits()),
			workflows.WithHistory(h), workflows.WithPauser(told), workflows.WithConfirmations(held),
			workflows.WithPersonal(func(nodeType string) []string {
				if nodeType == "output.webhook" {
					return []string{"url"}
				}

				return nil
			}),
		}, opts...)...)

	return svc, d, h, told, held
}

func TestAdminSwitchOff(t *testing.T) {
	ctx := context.Background()

	t.Run("an admin switches it off and holds it off; its owner told why", func(t *testing.T) {
		svc, d, h, told, _ := overseen(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(active(stored), nil)
		d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), false, now, domain.Actor{}).Return(nil)
		d.repo.EXPECT().SetHold(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"),
			&domain.Hold{By: "u-0", Reason: "3,000 runs an hour", At: now}).Return(nil)
		d.supervisor.EXPECT().Stop(domain.WorkflowID("wf-1"))

		w, err := svc.AdminSwitchOff(ctx, root, "u-1", "wf-1", "  3,000 runs an hour ", true)
		require.NoError(t, err)
		assert.False(t, w.Active)
		require.NotNil(t, w.Hold)

		assert.Equal(t, []domain.Switch{{WorkflowID: "wf-1", Kind: domain.SwitchHold, Admin: true, Reason: "3,000 runs an hour"}}, h.kept)
		assert.Equal(t, "3,000 runs an hour", told.held["wf-1"])
	})

	t.Run("switched off without a hold, told so", func(t *testing.T) {
		svc, d, h, told, _ := overseen(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(active(stored), nil)
		d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), false, now, domain.Actor{}).Return(nil)
		d.supervisor.EXPECT().Stop(domain.WorkflowID("wf-1"))

		_, err := svc.AdminSwitchOff(ctx, root, "u-1", "wf-1", "it loops", false)
		require.NoError(t, err)

		assert.Equal(t, domain.SwitchOff, h.kept[0].Kind)
		assert.Equal(t, "an admin switched it off, saying “it loops”", told.told["wf-1"])
	})

	t.Run("one off already is only held, or left as it is", func(t *testing.T) {
		svc, d, h, _, _ := overseen(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(stored, nil)

		w, err := svc.AdminSwitchOff(ctx, root, "u-1", "wf-1", "just in case", false)
		require.NoError(t, err)
		assert.Nil(t, w.Hold)
		assert.Empty(t, h.kept, "nothing done, nothing kept")
	})

	t.Run("its personal settings hidden from the admin too", func(t *testing.T) {
		svc, d, _, _, _ := overseen(t)
		hooked := stored
		hooked.Nodes = []domain.NodeSpec{{ID: "hook", Type: "output.webhook", Params: map[string]any{"url": "https://hooks.example/secret"}}}
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(hooked, nil)

		w, err := svc.AdminGet(ctx, root, "u-1", "wf-1")
		require.NoError(t, err)
		assert.Equal(t, domain.HiddenPersonal(), w.Nodes[0].Params["url"])
	})

	t.Run("admins alone; a reason needed", func(t *testing.T) {
		svc, _, _, _, _ := overseen(t)

		_, err := svc.AdminSwitchOff(ctx, alice, "u-1", "wf-1", "mine now", true)
		require.ErrorIs(t, err, domain.ErrForbidden)

		_, err = svc.AdminGet(ctx, alice, "u-1", "wf-1")
		require.ErrorIs(t, err, domain.ErrForbidden)

		_, err = svc.AdminSwitchOff(ctx, root, "u-1", "wf-1", "  ", true)
		require.ErrorIs(t, err, domain.ErrInvalidInput)
	})

	t.Run("an admin's agent's waits for the admin (D410)", func(t *testing.T) {
		svc, d, h, _, held := overseen(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(active(stored), nil)

		agent := root
		agent.SessionID, agent.GrantID, agent.Agent = "", "g-1", "Claude Code"

		_, err := svc.AdminSwitchOff(ctx, agent, "u-1", "wf-1", "it loops", true)
		require.ErrorIs(t, err, domain.ErrAwaitsConfirmation)
		assert.Equal(t, []domain.ConfirmKind{domain.ConfirmAdminWorkflow}, held.kinds)
		assert.Equal(t, []string{"Why: it loops"}, held.details)
		assert.Empty(t, h.kept, "nothing done yet")
	})
}

func TestAdminRelease(t *testing.T) {
	ctx := context.Background()
	held := stored
	held.Hold = &domain.Hold{By: "u-0", Reason: "3,000 runs an hour", At: now.Add(-time.Hour)}

	t.Run("released, its owner told; switched on again by them", func(t *testing.T) {
		svc, d, h, told, _ := overseen(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(held, nil)
		d.repo.EXPECT().SetHold(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), (*domain.Hold)(nil)).Return(nil)

		w, err := svc.AdminRelease(ctx, root, "u-1", "wf-1")
		require.NoError(t, err)
		assert.Nil(t, w.Hold)
		assert.Equal(t, []domain.Switch{{WorkflowID: "wf-1", Kind: domain.SwitchRelease, Admin: true}}, h.kept)
		assert.Equal(t, []domain.WorkflowID{"wf-1"}, told.released)
	})

	t.Run("held, its owner cannot switch it on", func(t *testing.T) {
		svc, d, _, _, _ := overseen(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(held, nil)

		_, err := svc.Activate(ctx, alice, "wf-1")
		require.ErrorIs(t, err, domain.ErrHeld)
		assert.Contains(t, err.Error(), "3,000 runs an hour")
	})
}

// A workflow whose triggers start it more often than the instance allows is paused, the
// run past the cap never starting; switched on again, it counts from nothing (D435).
func TestRunCap(t *testing.T) {
	most := 3
	svc, d, h, told, _ := overseen(t, workflows.WithRunCap(func() int { return most }))
	gate := svc.RunCap()
	w := active(stored)

	stopped := make(chan struct{})
	d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), false, now, domain.Actor{}).Return(nil).Once()
	d.supervisor.EXPECT().Stop(domain.WorkflowID("wf-1")).Run(func(domain.WorkflowID) { close(stopped) }).Once()

	for range 3 {
		require.True(t, gate.Allow(w))
	}

	assert.False(t, gate.Allow(w), "the fourth in the hour")
	assert.False(t, gate.Allow(w), "paused once, however many more come")

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("not paused")
	}

	require.Eventually(t, func() bool {
		told.mu.Lock()
		defer told.mu.Unlock()

		return told.told["wf-1"] != ""
	}, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "its triggers started it 3 times in an hour, the most this instance allows", told.told["wf-1"])
	assert.True(t, h.kept[0].Polythought)

	gate.Forget("wf-1")
	assert.True(t, gate.Allow(w), "switched on again: from nothing")

	most = 0
	for range 10 {
		assert.True(t, gate.Allow(w), "no cap")
	}
}
