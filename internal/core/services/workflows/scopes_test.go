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
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
	workflowsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/workflows"
)

// A workflow that places orders, live: saving, switching on or running it needs live
// (D399); one that places none needs only build.
func TestTradeScope(t *testing.T) {
	ctx := context.Background()
	order := domain.NodeSpec{ID: "buy", Type: "trading.placeOrder"}

	trades := stored
	trades.Nodes = append([]domain.NodeSpec{}, def.Nodes...)
	trades.Nodes = append(trades.Nodes, order)

	tradesDef := def
	tradesDef.Nodes = trades.Nodes

	token := func(scopes ...domain.Scope) auth.Principal {
		return auth.Principal{User: domain.User{ID: "u-1"}, TokenID: "t-1", Scopes: scopes}
	}
	build, live := token(domain.ScopeBuild), token(domain.ScopeBuild, domain.ScopeLive)

	newScoped := func(t *testing.T) (*workflows.Service, deps) {
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
			workflows.WithTradeScope(func(_ context.Context, w domain.Workflow) (domain.Scope, error) {
				if _, ok := w.Node("buy"); ok {
					return domain.ScopeLive, nil
				}

				return "", nil
			}))

		return svc, d
	}

	t.Run("refused without live", func(t *testing.T) {
		svc, d := newScoped(t)
		d.repo.EXPECT().Count(mock.Anything, mock.Anything).Return(0, 0, nil)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(trades, nil)

		_, err := svc.Create(ctx, build, tradesDef)
		require.ErrorIs(t, err, domain.ErrInsufficientScope)

		_, err = svc.Update(ctx, build, "wf-1", trades.Version, tradesDef)
		require.ErrorIs(t, err, domain.ErrInsufficientScope)

		_, err = svc.Activate(ctx, build, "wf-1")
		require.ErrorIs(t, err, domain.ErrInsufficientScope)

		_, err = svc.Run(ctx, build, "wf-1", "")
		require.ErrorIs(t, err, domain.ErrInsufficientScope)
		assert.Contains(t, err.Error(), "it needs the live scope")
		// The mocks were asked to create, save, switch on or run nothing.
	})

	t.Run("allowed with live, and to the user at a browser", func(t *testing.T) {
		for _, p := range []auth.Principal{live, alice} {
			svc, d := newScoped(t)
			d.repo.EXPECT().Count(mock.Anything, mock.Anything).Return(0, 0, nil)
			d.repo.EXPECT().Create(mock.Anything, mock.Anything).Return(nil)
			d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(trades, nil)
			d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), true, now).Return(nil)
			d.supervisor.EXPECT().Start(mock.Anything)
			d.executor.EXPECT().Execute(mock.Anything, mock.Anything, domain.NodeID("manual"), mock.Anything, mock.Anything).
				Return(engine.Result{}, nil)

			_, err := svc.Create(ctx, p, tradesDef)
			require.NoError(t, err)

			_, err = svc.Activate(ctx, p, "wf-1")
			require.NoError(t, err)

			_, err = svc.Run(ctx, p, "wf-1", "")
			require.NoError(t, err)
		}
	})

	t.Run("one that places no orders needs build alone", func(t *testing.T) {
		svc, d := newScoped(t)
		d.repo.EXPECT().Count(mock.Anything, mock.Anything).Return(0, 0, nil)
		d.repo.EXPECT().Create(mock.Anything, mock.Anything).Return(nil)

		_, err := svc.Create(ctx, build, def)
		require.NoError(t, err)
	})
}
