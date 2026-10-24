package workflows_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

var claude = auth.Principal{User: domain.User{ID: "u-1"}, GrantID: "g-1", Agent: "Claude Code", Scopes: domain.Scopes}

// The agent or token behind a workflow's runs is named: one that switched it on, or changed
// it while on, until it is switched on or off again (D407).
func TestActedBy(t *testing.T) {
	ctx := context.Background()
	switchedOn := domain.Actor{GrantID: "g-1", Name: "Claude Code", How: domain.ActorSwitchedOn}

	t.Run("switched on by an agent", func(t *testing.T) {
		svc, d := withPersonal(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(stored, nil)
		d.repo.EXPECT().Count(mock.Anything, domain.UserID("u-1")).Return(1, 0, nil)
		d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), true, now, switchedOn).Return(nil)
		d.supervisor.EXPECT().Start(mock.MatchedBy(func(w domain.Workflow) bool { return w.ActedBy == switchedOn }))

		w, err := svc.Activate(ctx, claude, "wf-1")
		require.NoError(t, err)
		assert.Equal(t, switchedOn, w.ActedBy)
	})

	t.Run("on already, it stays as it was", func(t *testing.T) {
		svc, d := withPersonal(t)

		on := stored
		on.Active, on.ActedBy = true, switchedOn
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(on, nil)
		d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), true, now, switchedOn).Return(nil)
		d.supervisor.EXPECT().Start(mock.Anything)

		_, err := svc.Activate(ctx, alice, "wf-1")
		require.NoError(t, err, "the user's no-op switching names no one new")
	})

	t.Run("changed while on, by an agent; by the user, as it was", func(t *testing.T) {
		on := stored
		on.Active, on.ActedBy = true, domain.Actor{}

		for _, tt := range []struct {
			who  auth.Principal
			want domain.Actor
		}{
			{claude, domain.Actor{GrantID: "g-1", Name: "Claude Code", How: domain.ActorChanged}},
			{alice, domain.Actor{}},
		} {
			svc, d := withPersonal(t)
			d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(on, nil)
			d.repo.EXPECT().Update(mock.Anything, mock.MatchedBy(func(w domain.Workflow) bool { return w.ActedBy == tt.want }), 3).Return(nil)
			d.supervisor.EXPECT().Resume(mock.Anything)

			_, err := svc.Update(ctx, tt.who, "wf-1", 3, def)
			require.NoError(t, err)
		}
	})

	t.Run("switched off, no one", func(t *testing.T) {
		svc, d := withPersonal(t)

		on := stored
		on.Active, on.ActedBy = true, switchedOn
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(on, nil)
		d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), false, now, domain.Actor{}).Return(nil)
		d.supervisor.EXPECT().Stop(domain.WorkflowID("wf-1"))

		w, err := svc.Deactivate(ctx, claude, "wf-1")
		require.NoError(t, err)
		assert.True(t, w.ActedBy.IsUser())
	})
}
