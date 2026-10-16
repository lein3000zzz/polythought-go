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

var (
	now   = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	alice = auth.Principal{User: domain.User{ID: "u-1"}, SessionID: "s-1"}
	def   = workflows.Definition{
		Name:  " Spread watch ",
		Nodes: []domain.NodeSpec{{ID: "manual", Type: "trigger.manual"}, {ID: "log", Type: "output.log"}},
		Connections: []domain.Connection{
			{From: "manual", FromPort: "main", To: "log", ToPort: "main"},
		},
	}
	stored = domain.Workflow{
		ID: "wf-1", OwnerID: "u-1", Name: "Spread watch", Nodes: def.Nodes, Connections: def.Connections,
		Version: 3, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
)

type deps struct {
	repo       *portsmocks.MockWorkflowRepository
	executions *portsmocks.MockExecutionRepository
	validator  *workflowsmocks.MockValidator
	executor   *workflowsmocks.MockExecutor
	supervisor *workflowsmocks.MockSupervisor
}

func newService(t *testing.T, limits workflows.Limits) (*workflows.Service, deps) {
	t.Helper()

	d := deps{
		repo:       portsmocks.NewMockWorkflowRepository(t),
		executions: portsmocks.NewMockExecutionRepository(t),
		validator:  workflowsmocks.NewMockValidator(t),
		executor:   workflowsmocks.NewMockExecutor(t),
		supervisor: workflowsmocks.NewMockSupervisor(t),
	}

	svc := workflows.New(d.repo, d.executions, d.validator, d.executor, d.supervisor, logging.Noop(),
		workflows.WithClock(func() time.Time { return now }), workflows.WithLimits(limits))

	return svc, d
}

func active(w domain.Workflow) domain.Workflow {
	w.Active = true
	return w
}

func TestCreate(t *testing.T) {
	tests := []struct {
		name    string
		def     workflows.Definition
		limits  workflows.Limits
		setup   func(d deps)
		wantErr error
	}{
		{
			name: "valid and inactive", def: def, limits: workflows.DefaultLimits(),
			setup: func(d deps) {
				d.repo.EXPECT().Count(mock.Anything, domain.UserID("u-1")).Return(0, 0, nil)
				d.validator.EXPECT().Validate(mock.MatchedBy(func(w domain.Workflow) bool { return w.Name == "Spread watch" })).Return(nil)
				d.repo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(w domain.Workflow) bool {
					return !w.Active && w.Version == 1 && w.OwnerID == "u-1" && w.CreatedAt.Equal(now) && w.ID != ""
				})).Return(nil)
			},
		},
		{
			name: "invalid graph", def: def, limits: workflows.DefaultLimits(),
			setup: func(d deps) {
				d.repo.EXPECT().Count(mock.Anything, mock.Anything).Return(0, 0, nil)
				d.validator.EXPECT().Validate(mock.Anything).Return(domain.ErrInvalidWorkflow)
			},
			wantErr: domain.ErrInvalidWorkflow,
		},
		{
			name: "blank name", def: workflows.Definition{Name: "  ", Nodes: def.Nodes}, limits: workflows.DefaultLimits(),
			setup: func(d deps) {
				d.repo.EXPECT().Count(mock.Anything, mock.Anything).Return(0, 0, nil)
			},
			wantErr: domain.ErrInvalidInput,
		},
		{
			name: "too many nodes", def: def, limits: workflows.Limits{Workflows: 10, ActiveWorkflows: 10, NodesPerFlow: 1},
			setup: func(d deps) {
				d.repo.EXPECT().Count(mock.Anything, mock.Anything).Return(0, 0, nil)
			},
			wantErr: domain.ErrInvalidInput,
		},
		{
			name: "per-user workflow limit", def: def, limits: workflows.Limits{Workflows: 2, ActiveWorkflows: 1, NodesPerFlow: 10},
			setup: func(d deps) {
				d.repo.EXPECT().Count(mock.Anything, mock.Anything).Return(2, 0, nil)
			},
			wantErr: domain.ErrInvalidInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, d := newService(t, tt.limits)
			tt.setup(d)

			w, err := svc.Create(context.Background(), alice, tt.def)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, "Spread watch", w.Name)
		})
	}
}

func TestUpdate(t *testing.T) {
	tests := []struct {
		name    string
		current domain.Workflow
		version int
		setup   func(d deps)
		wantErr error
		check   func(t *testing.T, w domain.Workflow)
	}{
		{
			name: "saves the next version", current: stored, version: 3,
			setup: func(d deps) {
				d.validator.EXPECT().Validate(mock.Anything).Return(nil)
				d.repo.EXPECT().Update(mock.Anything, mock.MatchedBy(func(w domain.Workflow) bool {
					return w.Version == 4 && w.UpdatedAt.Equal(now)
				}), 3).Return(nil)
			},
			check: func(t *testing.T, w domain.Workflow) { assert.Equal(t, 4, w.Version) },
		},
		{
			name: "an active workflow restarts with the new definition", current: active(stored), version: 3,
			setup: func(d deps) {
				d.validator.EXPECT().Validate(mock.Anything).Return(nil)
				d.repo.EXPECT().Update(mock.Anything, mock.Anything, 3).Return(nil)
				d.supervisor.EXPECT().Start(mock.MatchedBy(func(w domain.Workflow) bool { return w.Version == 4 })).Once()
			},
		},
		{
			name: "stale version is a conflict", current: stored, version: 2,
			setup:   func(deps) {},
			wantErr: domain.ErrConflict,
		},
		{
			name: "losing a race at the database is a conflict", current: stored, version: 3,
			setup: func(d deps) {
				d.validator.EXPECT().Validate(mock.Anything).Return(nil)
				d.repo.EXPECT().Update(mock.Anything, mock.Anything, 3).Return(domain.ErrConflict)
			},
			wantErr: domain.ErrConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, d := newService(t, workflows.DefaultLimits())
			d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(tt.current, nil)
			tt.setup(d)

			w, err := svc.Update(context.Background(), alice, "wf-1", tt.version, def)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			if tt.check != nil {
				tt.check(t, w)
			}
		})
	}
}

func TestLifecycle(t *testing.T) {
	limits := workflows.Limits{Workflows: 10, ActiveWorkflows: 2, NodesPerFlow: 10}

	tests := []struct {
		name    string
		setup   func(d deps)
		call    func(s *workflows.Service) error
		wantErr error
	}{
		{
			name: "activate validates, persists and starts",
			setup: func(d deps) {
				d.repo.EXPECT().Get(mock.Anything, mock.Anything, domain.WorkflowID("wf-1")).Return(stored, nil)
				d.repo.EXPECT().Count(mock.Anything, mock.Anything).Return(3, 1, nil)
				d.validator.EXPECT().Validate(stored).Return(nil)
				d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), true, now).Return(nil)
				d.supervisor.EXPECT().Start(active(stored)).Once()
			},
			call: func(s *workflows.Service) error {
				_, err := s.Activate(context.Background(), alice, "wf-1")
				return err
			},
		},
		{
			name: "activation limit",
			setup: func(d deps) {
				d.repo.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(stored, nil)
				d.repo.EXPECT().Count(mock.Anything, mock.Anything).Return(3, 2, nil)
			},
			call: func(s *workflows.Service) error {
				_, err := s.Activate(context.Background(), alice, "wf-1")
				return err
			},
			wantErr: domain.ErrInvalidInput,
		},
		{
			name: "re-activating an active workflow skips the limit",
			setup: func(d deps) {
				d.repo.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(active(stored), nil)
				d.validator.EXPECT().Validate(mock.Anything).Return(nil)
				d.repo.EXPECT().SetActive(mock.Anything, mock.Anything, mock.Anything, true, now).Return(nil)
				d.supervisor.EXPECT().Start(mock.Anything).Once()
			},
			call: func(s *workflows.Service) error {
				_, err := s.Activate(context.Background(), alice, "wf-1")
				return err
			},
		},
		{
			name: "deactivate persists and stops",
			setup: func(d deps) {
				d.repo.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(active(stored), nil)
				d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), false, now).Return(nil)
				d.supervisor.EXPECT().Stop(domain.WorkflowID("wf-1")).Once()
			},
			call: func(s *workflows.Service) error {
				_, err := s.Deactivate(context.Background(), alice, "wf-1")
				return err
			},
		},
		{
			name: "delete stops first",
			setup: func(d deps) {
				d.repo.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(active(stored), nil)
				d.supervisor.EXPECT().Stop(domain.WorkflowID("wf-1")).Once()
				d.repo.EXPECT().Delete(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(nil)
			},
			call: func(s *workflows.Service) error { return s.Delete(context.Background(), alice, "wf-1") },
		},
		{
			name: "someone else's workflow is not found",
			setup: func(d deps) {
				d.repo.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(domain.Workflow{}, domain.ErrNotFound)
			},
			call:    func(s *workflows.Service) error { return s.Delete(context.Background(), alice, "wf-1") },
			wantErr: domain.ErrNotFound,
		},
		{
			name: "executions check ownership first",
			setup: func(d deps) {
				d.repo.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(domain.Workflow{}, domain.ErrNotFound)
			},
			call: func(s *workflows.Service) error {
				_, err := s.Executions(context.Background(), alice, "wf-1", ports.PageFilter{})
				return err
			},
			wantErr: domain.ErrNotFound,
		},
		{
			name: "executions of an owned workflow",
			setup: func(d deps) {
				d.repo.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(stored, nil)
				d.executions.EXPECT().List(mock.Anything, domain.WorkflowID("wf-1"), ports.PageFilter{Limit: 5}).Return(ports.Page[domain.Execution]{}, nil)
			},
			call: func(s *workflows.Service) error {
				_, err := s.Executions(context.Background(), alice, "wf-1", ports.PageFilter{Limit: 5})
				return err
			},
		},
		{
			name: "prune uses the retention window",
			setup: func(d deps) {
				d.executions.EXPECT().DeleteFinishedBefore(mock.Anything, now.Add(-30*24*time.Hour)).Return(7, nil)
			},
			call: func(s *workflows.Service) error {
				_, err := s.PruneExecutions(context.Background(), 30*24*time.Hour)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, d := newService(t, limits)
			tt.setup(d)

			require.ErrorIs(t, tt.call(svc), tt.wantErr)
		})
	}
}

func TestRun(t *testing.T) {
	twoManual := stored
	twoManual.Nodes = []domain.NodeSpec{{ID: "m1", Type: "trigger.manual"}, {ID: "m2", Type: "trigger.manual"}}

	scheduled := stored
	scheduled.Nodes = []domain.NodeSpec{{ID: "every", Type: "trigger.schedule"}}

	both := stored
	both.Nodes = []domain.NodeSpec{{ID: "every", Type: "trigger.schedule"}, {ID: "manual", Type: "trigger.manual"}}

	live := stored
	live.Nodes = []domain.NodeSpec{{ID: "trades", Type: "trigger.market"}}

	manualItem := engine.Ports{engine.DefaultPort: {{JSON: map[string]any{}}}}
	tick := engine.Ports{engine.DefaultPort: {{JSON: map[string]any{"firedAt": now}}}}

	tests := []struct {
		name        string
		workflow    domain.Workflow
		trigger     domain.NodeID
		wantTrigger domain.NodeID
		wantOutput  engine.Ports
		wantErr     error
	}{
		{name: "the only manual trigger", workflow: stored, wantTrigger: "manual", wantOutput: manualItem},
		{name: "a named trigger", workflow: twoManual, trigger: "m2", wantTrigger: "m2", wantOutput: manualItem},
		{name: "several manual triggers need a name", workflow: twoManual, wantErr: domain.ErrInvalidInput},
		{name: "a schedule fires as if its time had come", workflow: scheduled, wantTrigger: "every", wantOutput: tick},
		{name: "a manual trigger comes before a schedule", workflow: both, wantTrigger: "manual", wantOutput: manualItem},
		{name: "a named schedule", workflow: both, trigger: "every", wantTrigger: "every", wantOutput: tick},
		{name: "market activity cannot be imitated", workflow: live, wantErr: domain.ErrInvalidInput},
		{name: "nor named", workflow: live, trigger: "trades", wantErr: domain.ErrInvalidInput},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, d := newService(t, workflows.DefaultLimits())
			d.repo.EXPECT().Get(mock.Anything, mock.Anything, mock.Anything).Return(tt.workflow, nil)

			if tt.wantErr == nil {
				// One option: manual runs keep samples (engine.KeepSamples, tested in the engine).
				d.executor.EXPECT().Execute(mock.Anything, tt.workflow, tt.wantTrigger, tt.wantOutput, mock.Anything).
					Return(engine.Result{Execution: domain.Execution{ID: "ex-1", Status: domain.ExecutionSucceeded}}, nil)
			}

			exec, err := svc.Run(context.Background(), alice, "wf-1", tt.trigger)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, domain.ExecutionID("ex-1"), exec.ID)
		})
	}
}

func TestStartActive(t *testing.T) {
	valid, broken := active(stored), active(stored)
	broken.ID = "wf-broken"

	svc, d := newService(t, workflows.DefaultLimits())
	d.repo.EXPECT().ListActive(mock.Anything).Return([]domain.Workflow{valid, broken}, nil)
	d.validator.EXPECT().Validate(valid).Return(nil)
	d.validator.EXPECT().Validate(broken).Return(domain.ErrInvalidWorkflow)
	d.supervisor.EXPECT().Start(valid).Once()

	require.NoError(t, svc.StartActive(context.Background()))
}

func TestStopAll(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(d deps)
		wantErr error
	}{
		{
			name: "deactivates and stops every running workflow",
			setup: func(d deps) {
				d.repo.EXPECT().DeactivateAll(mock.Anything, alice.User.ID, now).Return([]domain.WorkflowID{"wf-1", "wf-2"}, nil)
				d.supervisor.EXPECT().Stop(domain.WorkflowID("wf-1")).Once()
				d.supervisor.EXPECT().Stop(domain.WorkflowID("wf-2")).Once()
			},
		},
		{
			name: "nothing to stop",
			setup: func(d deps) {
				d.repo.EXPECT().DeactivateAll(mock.Anything, alice.User.ID, now).Return(nil, nil)
			},
		},
		{
			name: "nothing is stopped when the database refuses",
			setup: func(d deps) {
				d.repo.EXPECT().DeactivateAll(mock.Anything, alice.User.ID, now).Return(nil, domain.ErrUnavailable)
			},
			wantErr: domain.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, d := newService(t, workflows.DefaultLimits())
			tt.setup(d)

			require.ErrorIs(t, svc.StopAll(context.Background(), alice.User.ID), tt.wantErr)
		})
	}
}

func TestWaitingExecutions(t *testing.T) {
	ctx := context.Background()

	withWaits := func(t *testing.T) (*workflows.Service, deps, *workflowsmocks.MockWaits) {
		t.Helper()

		d := deps{
			repo:       portsmocks.NewMockWorkflowRepository(t),
			executions: portsmocks.NewMockExecutionRepository(t),
			validator:  workflowsmocks.NewMockValidator(t),
			executor:   workflowsmocks.NewMockExecutor(t),
			supervisor: workflowsmocks.NewMockSupervisor(t),
		}
		w := workflowsmocks.NewMockWaits(t)

		svc := workflows.New(d.repo, d.executions, d.validator, d.executor, d.supervisor, logging.Noop(),
			workflows.WithClock(func() time.Time { return now }), workflows.WithWaits(w))

		return svc, d, w
	}

	t.Run("switching off cancels what waits", func(t *testing.T) {
		s, d, w := withWaits(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(active(stored), nil)
		d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), false, now).Return(nil)
		d.supervisor.EXPECT().Stop(domain.WorkflowID("wf-1")).Once()
		w.EXPECT().CancelWorkflow(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), "cancelled: the workflow was switched off").Return(nil).Once()

		_, err := s.Deactivate(ctx, alice, "wf-1")
		require.NoError(t, err)
	})

	t.Run("so does disabling the account", func(t *testing.T) {
		s, d, w := withWaits(t)
		d.repo.EXPECT().DeactivateAll(mock.Anything, alice.User.ID, now).Return([]domain.WorkflowID{"wf-1"}, nil)
		d.supervisor.EXPECT().Stop(domain.WorkflowID("wf-1")).Once()
		w.EXPECT().CancelWorkflow(mock.Anything, alice.User.ID, domain.WorkflowID("wf-1"), "cancelled: the account was disabled").Return(nil).Once()

		require.NoError(t, s.StopAll(ctx, alice.User.ID))
	})

	t.Run("cancel one that waits", func(t *testing.T) {
		s, d, w := withWaits(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(stored, nil)
		d.executions.EXPECT().Get(mock.Anything, domain.WorkflowID("wf-1"), domain.ExecutionID("ex-1")).
			Return(domain.Execution{ID: "ex-1", Status: domain.ExecutionWaiting}, nil)
		w.EXPECT().Cancel(mock.Anything, domain.UserID("u-1"), domain.ExecutionID("ex-1"), "cancelled by you").
			Return(domain.Execution{ID: "ex-1", Status: domain.ExecutionCancelled}, nil)

		e, err := s.CancelExecution(ctx, alice, "wf-1", "ex-1")
		require.NoError(t, err)
		assert.Equal(t, domain.ExecutionCancelled, e.Status)
	})

	t.Run("only one that waits", func(t *testing.T) {
		s, d, _ := withWaits(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(stored, nil)
		d.executions.EXPECT().Get(mock.Anything, domain.WorkflowID("wf-1"), domain.ExecutionID("ex-1")).
			Return(domain.Execution{ID: "ex-1", Status: domain.ExecutionSucceeded}, nil)

		_, err := s.CancelExecution(ctx, alice, "wf-1", "ex-1")
		require.ErrorIs(t, err, domain.ErrNotWaiting)
		assert.ErrorContains(t, err, "it is succeeded")
	})

	t.Run("one that carried on meanwhile", func(t *testing.T) {
		s, d, w := withWaits(t)
		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(stored, nil)
		d.executions.EXPECT().Get(mock.Anything, domain.WorkflowID("wf-1"), domain.ExecutionID("ex-1")).
			Return(domain.Execution{ID: "ex-1", Status: domain.ExecutionWaiting}, nil)
		w.EXPECT().Cancel(mock.Anything, domain.UserID("u-1"), domain.ExecutionID("ex-1"), mock.Anything).
			Return(domain.Execution{}, domain.ErrNotFound)

		_, err := s.CancelExecution(ctx, alice, "wf-1", "ex-1")
		require.ErrorIs(t, err, domain.ErrNotWaiting)
	})
}

func TestFieldChecks(t *testing.T) {
	ctx := context.Background()
	alice := auth.Principal{User: domain.User{ID: "u-1", Role: domain.RoleUser}}

	withChecks := func(t *testing.T) (*workflows.Service, deps, *workflowsmocks.MockFieldChecker) {
		t.Helper()

		d := deps{
			repo: portsmocks.NewMockWorkflowRepository(t), executions: portsmocks.NewMockExecutionRepository(t),
			validator: workflowsmocks.NewMockValidator(t), executor: workflowsmocks.NewMockExecutor(t),
			supervisor: workflowsmocks.NewMockSupervisor(t),
		}
		fc := workflowsmocks.NewMockFieldChecker(t)

		svc := workflows.New(d.repo, d.executions, d.validator, d.executor, d.supervisor, logging.Noop(),
			workflows.WithClock(func() time.Time { return now }), workflows.WithFieldChecks(fc))

		return svc, d, fc
	}

	t.Run("saving checks field settings", func(t *testing.T) {
		s, d, fc := withChecks(t)
		problem := &domain.ValidationError{Problems: []domain.Problem{{NodeID: "tight", Param: "field", Message: `no field "bestbid"`}}}

		d.repo.EXPECT().Count(mock.Anything, mock.Anything).Return(0, 0, nil)
		d.validator.EXPECT().Validate(mock.Anything).Return(nil)
		fc.EXPECT().CheckFields(mock.MatchedBy(func(w domain.Workflow) bool { return w.Name == "Spread watch" })).Return(problem)

		_, err := s.Create(ctx, alice, def)

		var v *domain.ValidationError
		require.ErrorAs(t, err, &v)
		assert.Equal(t, problem.Problems, v.Problems)
	})

	t.Run("and expressions, with every problem at once", func(t *testing.T) {
		d := deps{
			repo: portsmocks.NewMockWorkflowRepository(t), executions: portsmocks.NewMockExecutionRepository(t),
			validator: workflowsmocks.NewMockValidator(t), executor: workflowsmocks.NewMockExecutor(t),
			supervisor: workflowsmocks.NewMockSupervisor(t),
		}
		fc := workflowsmocks.NewMockFieldChecker(t)
		ec := workflowsmocks.NewMockExpressionChecker(t)

		s := workflows.New(d.repo, d.executions, d.validator, d.executor, d.supervisor, logging.Noop(),
			workflows.WithFieldChecks(fc), workflows.WithExpressionChecks(ec))

		field := domain.Problem{NodeID: "tight", Param: "field", Message: `no field "bestbid"`}
		expr := domain.Problem{NodeID: "log", Param: "message", Message: `{{ price * }}: line 1, column 8: Unexpected end of the expression`}

		d.repo.EXPECT().Count(mock.Anything, mock.Anything).Return(0, 0, nil)
		d.validator.EXPECT().Validate(mock.Anything).Return(nil)
		fc.EXPECT().CheckFields(mock.Anything).Return(&domain.ValidationError{Problems: []domain.Problem{field}})
		ec.EXPECT().CheckExpressions(mock.Anything).Return(&domain.ValidationError{Problems: []domain.Problem{expr}})

		_, err := s.Create(ctx, alice, def)
		require.ErrorIs(t, err, domain.ErrInvalidWorkflow)

		var v *domain.ValidationError
		require.ErrorAs(t, err, &v)
		assert.Equal(t, []domain.Problem{field, expr}, v.Problems)
	})

	t.Run("switching on does not: a workflow saved before keeps running", func(t *testing.T) {
		s, d, _ := withChecks(t)

		d.repo.EXPECT().Get(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1")).Return(stored, nil)
		d.repo.EXPECT().Count(mock.Anything, domain.UserID("u-1")).Return(1, 0, nil)
		d.validator.EXPECT().Validate(mock.Anything).Return(nil)
		d.repo.EXPECT().SetActive(mock.Anything, domain.UserID("u-1"), domain.WorkflowID("wf-1"), true, now).Return(nil)
		d.supervisor.EXPECT().Start(mock.Anything).Once()

		_, err := s.Activate(ctx, alice, "wf-1")
		require.NoError(t, err)
	})
}
