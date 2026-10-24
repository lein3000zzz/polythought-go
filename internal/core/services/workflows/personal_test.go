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
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
	workflowsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/workflows"
)

const secretURL = "https://discord.example/api/webhooks/1/secret"

func hooked() domain.Workflow {
	w := stored
	w.Nodes = []domain.NodeSpec{
		{ID: "manual", Type: "trigger.manual"},
		{ID: "hook", Type: "output.webhook", Params: map[string]any{"url": secretURL, "method": "POST"}},
	}

	return w
}

func withPersonal(t *testing.T) (*workflows.Service, deps) {
	t.Helper()

	d := deps{
		repo:       portsmocks.NewMockWorkflowRepository(t),
		executions: portsmocks.NewMockExecutionRepository(t),
		validator:  workflowsmocks.NewMockValidator(t),
		executor:   workflowsmocks.NewMockExecutor(t),
		supervisor: workflowsmocks.NewMockSupervisor(t),
	}
	d.validator.EXPECT().Validate(mock.Anything, mock.Anything).Return(nil).Maybe()

	svc := workflows.New(d.repo, d.executions, d.validator, d.executor, d.supervisor, logging.Noop(),
		workflows.WithClock(func() time.Time { return now }), workflows.WithLimits(workflows.DefaultLimits()),
		workflows.WithPersonal(func(nodeType string) []string {
			if nodeType == "output.webhook" {
				return []string{"url"}
			}

			return nil
		}))

	return svc, d
}

func url(w domain.Workflow) any { return w.Nodes[1].Params["url"] }

// A token or an agent sees a personal setting only where its user lets it (D401, D402);
// the user themself always does.
func TestPersonalHidden(t *testing.T) {
	ctx := context.Background()
	user := domain.User{ID: "u-1"}
	letting := domain.User{ID: "u-1", Agents: domain.AgentAccess{SeePersonal: true}}

	for name, tt := range map[string]struct {
		p    auth.Principal
		sees bool
	}{
		"at a browser":              {auth.Principal{User: user, SessionID: "s"}, true},
		"in Telegram":               {auth.Principal{User: user}, true},
		"a token":                   {auth.Principal{User: user, TokenID: "t"}, false},
		"an agent":                  {auth.Principal{User: user, GrantID: "g", Scopes: domain.Scopes}, false},
		"a token its user lets see": {auth.Principal{User: letting, TokenID: "t"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			svc, d := withPersonal(t)
			kept := hooked()
			d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(kept, nil)
			d.repo.EXPECT().List(mock.Anything, domain.UserID("u-1"), mock.Anything).Return(ports.Page[domain.Workflow]{Items: []domain.Workflow{hooked()}}, nil)

			got, err := svc.Get(ctx, tt.p, "wf-1")
			require.NoError(t, err)

			list, err := svc.List(ctx, tt.p, ports.PageFilter{})
			require.NoError(t, err)

			if tt.sees {
				assert.Equal(t, secretURL, url(got))
				assert.Equal(t, secretURL, url(list.Items[0]))
			} else {
				assert.True(t, domain.IsHiddenPersonal(url(got)))
				assert.True(t, domain.IsHiddenPersonal(url(list.Items[0])))
				assert.Equal(t, "POST", got.Nodes[1].Params["method"], "the rest as it is")
			}

			assert.Equal(t, secretURL, url(kept), "what was read is left as it was")
		})
	}
}

// Saved hidden, a personal setting keeps its value (D402); one that had none has none.
func TestPersonalKept(t *testing.T) {
	ctx := context.Background()
	token := auth.Principal{User: domain.User{ID: "u-1"}, TokenID: "t"}

	t.Run("updated", func(t *testing.T) {
		svc, d := withPersonal(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(hooked(), nil)

		var saved domain.Workflow
		d.repo.EXPECT().Update(mock.Anything, mock.Anything, 3).RunAndReturn(func(_ context.Context, w domain.Workflow, _ int) error {
			saved = w
			return nil
		})

		def := workflows.Definition{Name: "Spread watch", Nodes: []domain.NodeSpec{
			{ID: "manual", Type: "trigger.manual"},
			{ID: "hook", Type: "output.webhook", Params: map[string]any{"url": domain.HiddenPersonal(), "method": "GET"}},
			{ID: "new", Type: "output.webhook", Params: map[string]any{"url": domain.HiddenPersonal()}},
		}}

		got, err := svc.Update(ctx, token, "wf-1", 3, def)
		require.NoError(t, err)

		assert.Equal(t, map[string]any{"url": secretURL, "method": "GET"}, saved.Nodes[1].Params, "kept, the rest changed")
		assert.Equal(t, map[string]any{}, saved.Nodes[2].Params, "no value before, none now")
		assert.True(t, domain.IsHiddenPersonal(url(got)), "and hidden again in the answer")
		assert.True(t, domain.IsHiddenPersonal(def.Nodes[1].Params["url"]), "what was sent is left as it was")
	})

	t.Run("created", func(t *testing.T) {
		svc, d := withPersonal(t)
		d.repo.EXPECT().Count(mock.Anything, mock.Anything).Return(0, 0, nil)

		var saved domain.Workflow
		d.repo.EXPECT().Create(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, w domain.Workflow) error {
			saved = w
			return nil
		})

		_, err := svc.Create(ctx, token, workflows.Definition{Name: "New", Nodes: []domain.NodeSpec{
			{ID: "hook", Type: "output.webhook", Params: map[string]any{"url": domain.HiddenPersonal()}},
		}})
		require.NoError(t, err)
		assert.Empty(t, saved.Nodes[0].Params)
	})

	t.Run("switched on, the workflow runs as it is", func(t *testing.T) {
		svc, d := withPersonal(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(hooked(), nil)
		d.repo.EXPECT().Count(mock.Anything, mock.Anything).Return(0, 0, nil)
		d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), true, now, mock.Anything).Return(nil)

		var started domain.Workflow
		d.supervisor.EXPECT().Start(mock.Anything).Run(func(w domain.Workflow) { started = w })

		got, err := svc.Activate(ctx, token, "wf-1")
		require.NoError(t, err)
		assert.Equal(t, secretURL, url(started), "its triggers have the URL")
		assert.True(t, domain.IsHiddenPersonal(url(got)), "the caller does not")
	})
}

// A token or an agent deletes a workflow only where its user lets it (D403).
func TestDeleteNeedsTheSwitch(t *testing.T) {
	ctx := context.Background()

	svc, _ := withPersonal(t)
	err := svc.Delete(ctx, auth.Principal{User: domain.User{ID: "u-1"}, GrantID: "g", Scopes: domain.Scopes}, "wf-1")
	require.ErrorIs(t, err, domain.ErrForbidden, "asked nothing of the repository")

	for _, p := range []auth.Principal{
		{User: domain.User{ID: "u-1", Agents: domain.AgentAccess{Delete: true}}, TokenID: "t"},
		{User: domain.User{ID: "u-1"}, SessionID: "s"},
	} {
		svc, d := withPersonal(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(hooked(), nil)
		d.supervisor.EXPECT().Stop(domain.WorkflowID("wf-1"))
		d.repo.EXPECT().Delete(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(nil)

		require.NoError(t, svc.Delete(ctx, p, "wf-1"))
	}
}
