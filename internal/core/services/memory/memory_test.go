package memory_test

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
	"github.com/lein3000zzz/polythought-go/internal/core/services/memory"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

var (
	alice = auth.Principal{User: domain.User{ID: "alice"}}
	bob   = auth.Principal{User: domain.User{ID: "bob"}}
	now   = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
)

// alicesWorkflows has wf-1, alice's.
type alicesWorkflows struct{}

func (alicesWorkflows) Get(_ context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error) {
	if p.User.ID != alice.User.ID || id != "wf-1" {
		return domain.Workflow{}, domain.ErrNotFound
	}

	return domain.Workflow{ID: id, OwnerID: alice.User.ID}, nil
}

func TestService(t *testing.T) {
	ctx := context.Background()

	mem := portsmocks.NewMockNodeMemory(t)
	svc := memory.New(mem, alicesWorkflows{}, logging.Noop()).WithClock(func() time.Time { return now })

	mem.EXPECT().Count(ctx, domain.WorkflowID("wf-1"), domain.NodeID("new"), now).Return(312, nil).Once()

	n, err := svc.Count(ctx, alice, "wf-1", "new")
	require.NoError(t, err)
	assert.Equal(t, 312, n)

	mem.EXPECT().Forget(ctx, domain.WorkflowID("wf-1"), domain.NodeID("new")).Return(nil).Once()
	require.NoError(t, svc.Forget(ctx, alice, "wf-1", "new"))

	_, err = svc.Count(ctx, bob, "wf-1", "new")
	require.ErrorIs(t, err, domain.ErrNotFound, "someone else's workflow is not there")
	require.ErrorIs(t, svc.Forget(ctx, bob, "wf-1", "new"), domain.ErrNotFound)

	t.Run("sweeps until stopped", func(t *testing.T) {
		swept := make(chan struct{}, 2)
		mem.EXPECT().Sweep(mock.Anything, now).RunAndReturn(func(context.Context, time.Time) (int, error) {
			select {
			case swept <- struct{}{}:
			default:
			}

			return 3, nil
		})

		stop, cancel := context.WithCancel(ctx)
		done := make(chan struct{})

		go func() {
			defer close(done)
			svc.SweepEvery(stop, time.Millisecond)
		}()

		<-swept
		<-swept
		cancel()
		<-done
	})
}
