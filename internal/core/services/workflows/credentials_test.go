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

func withCredentials(t *testing.T) (*workflows.Service, deps) {
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
		workflows.WithCredentials(func(nodeType string) []string {
			if nodeType == "output.webhook" {
				return []string{"credential"}
			}

			return nil
		}))

	return svc, d
}

// withHeader is hooked, its webhook sending one of the user's credentials.
func withHeader() domain.Workflow {
	w := hooked()
	w.Nodes[1].Params["credential"] = "cred-1"

	return w
}

func definitionOf(w domain.Workflow) workflows.Definition {
	return workflows.Definition{Name: w.Name, Nodes: w.Nodes, Connections: w.Connections}
}

var (
	agentOff = auth.Principal{User: domain.User{ID: "u-1"}, GrantID: "g-1", Scopes: domain.Scopes}
	agentOn  = auth.Principal{User: domain.User{ID: "u-1", Agents: domain.AgentAccess{UseCredentials: true}}, GrantID: "g-1", Scopes: domain.Scopes}
)

// A workflow an agent or a token builds can send a credential wherever its nodes call, so
// they save, switch on and run one that uses a credential only where their user lets them
// (D404).
func TestCredentialsNeedTheSwitch(t *testing.T) {
	ctx := context.Background()

	t.Run("saving", func(t *testing.T) {
		svc, d := withCredentials(t)
		d.repo.EXPECT().Count(mock.Anything, domain.UserID("u-1")).Return(0, 0, nil)

		_, err := svc.Create(ctx, agentOff, definitionOf(withHeader()))
		require.ErrorIs(t, err, domain.ErrForbidden)
		assert.Contains(t, err.Error(), `node "hook" uses a credential`)

		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(withHeader(), nil)

		changed := withHeader()
		changed.Nodes[1].Params["url"] = "https://attacker.example/catch"
		_, err = svc.Update(ctx, agentOff, "wf-1", 3, definitionOf(changed))
		require.ErrorIs(t, err, domain.ErrForbidden, "a workflow that has one already too: its webhook could go elsewhere")
	})

	t.Run("switching on and running", func(t *testing.T) {
		svc, d := withCredentials(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(withHeader(), nil)
		d.repo.EXPECT().Count(mock.Anything, domain.UserID("u-1")).Return(1, 0, nil)

		_, err := svc.Activate(ctx, agentOff, "wf-1")
		require.ErrorIs(t, err, domain.ErrForbidden)

		_, err = svc.Run(ctx, agentOff, "wf-1", "")
		require.ErrorIs(t, err, domain.ErrForbidden)
	})

	t.Run("one that uses none", func(t *testing.T) {
		svc, d := withCredentials(t)
		d.repo.EXPECT().Count(mock.Anything, domain.UserID("u-1")).Return(0, 0, nil)
		d.repo.EXPECT().Create(mock.Anything, mock.Anything).Return(nil)

		_, err := svc.Create(ctx, agentOff, definitionOf(hooked()))
		require.NoError(t, err)
	})

	t.Run("where the user lets them, and the user", func(t *testing.T) {
		for _, p := range []auth.Principal{agentOn, alice} {
			svc, d := withCredentials(t)
			d.repo.EXPECT().Count(mock.Anything, domain.UserID("u-1")).Return(0, 0, nil)
			d.repo.EXPECT().Create(mock.Anything, mock.Anything).Return(nil)

			_, err := svc.Create(ctx, p, definitionOf(withHeader()))
			require.NoError(t, err)
		}
	})
}
