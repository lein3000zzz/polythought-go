package history_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/history"
)

var now = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

// kept is the repository: what it was given, and the filter it was asked with.
type kept struct {
	switches []domain.Switch
	filter   ports.SwitchFilter
	before   time.Time
	fails    bool
}

func (k *kept) Record(_ context.Context, s domain.Switch) error {
	if k.fails {
		return errors.New("database down")
	}

	k.switches = append(k.switches, s)

	return nil
}

func (k *kept) List(_ context.Context, owner domain.UserID, _ domain.WorkflowID, f ports.SwitchFilter) (ports.Page[domain.Switch], error) {
	if owner != "alice" {
		return ports.Page[domain.Switch]{}, domain.ErrNotFound
	}

	k.filter = f

	return ports.Page[domain.Switch]{Items: k.switches}, nil
}

func (k *kept) DeleteBefore(_ context.Context, before time.Time) (int64, error) {
	k.before = before
	return 3, nil
}

func TestSwitched(t *testing.T) {
	repo := &kept{}
	svc := history.New(repo, nil, history.WithClock(func() time.Time { return now }))

	agent := auth.Principal{
		User: domain.User{ID: "alice"}, GrantID: "g-1", Agent: "Claude Code", Confirmed: true,
		Reason: "  " + strings.Repeat("é", domain.SwitchReasonMost+10) + "  ",
	}

	svc.Switched(context.Background(), history.By(agent, "wf-1", domain.SwitchOn))
	svc.Switched(context.Background(), history.By(auth.Principal{User: domain.User{ID: "alice"}, SessionID: "s-1"}, "wf-1", domain.SwitchOff))
	svc.Switched(context.Background(), history.Itself("wf-1", domain.SwitchPaper, "its wallet was unlinked"))

	require.Len(t, repo.switches, 3)

	on := repo.switches[0]
	assert.NotEmpty(t, on.ID)
	assert.Equal(t, now, on.At)
	assert.Equal(t, domain.Actor{GrantID: "g-1", Name: "Claude Code", Confirmed: true}, on.By, "no how: the kind says")
	assert.Len(t, []rune(on.Reason), domain.SwitchReasonMost, "trimmed, and cut")
	assert.NotEqual(t, on.ID, repo.switches[1].ID)

	assert.True(t, repo.switches[1].By.IsUser(), "the user")
	assert.False(t, repo.switches[1].Polythought)
	assert.True(t, repo.switches[2].Polythought)
	assert.Equal(t, "its wallet was unlinked", repo.switches[2].Reason)

	t.Run("not kept, the switch stands", func(t *testing.T) {
		repo.fails = true
		svc.Switched(context.Background(), history.Itself("wf-1", domain.SwitchOff, "a plugin it uses is gone"))
		assert.Len(t, repo.switches, 3)
	})
}

func TestList(t *testing.T) {
	repo := &kept{}
	svc := history.New(repo, nil, history.WithClock(func() time.Time { return now }))
	alice := auth.Principal{User: domain.User{ID: "alice"}}

	_, err := svc.List(context.Background(), alice, "wf-1", []domain.SwitchGroup{domain.GroupOnOff, domain.GroupKillSwitch}, ports.PageFilter{Limit: 5})
	require.NoError(t, err)
	assert.Equal(t, []domain.SwitchKind{domain.SwitchOn, domain.SwitchOff, domain.SwitchKillOn, domain.SwitchKillOff}, repo.filter.Kinds)
	assert.Equal(t, 5, repo.filter.Limit)

	_, err = svc.List(context.Background(), alice, "wf-1", nil, ports.PageFilter{})
	require.NoError(t, err)
	assert.Empty(t, repo.filter.Kinds, "every kind")

	_, err = svc.List(context.Background(), alice, "wf-1", []domain.SwitchGroup{"runs"}, ports.PageFilter{})
	require.ErrorIs(t, err, domain.ErrInvalidInput)

	_, err = svc.List(context.Background(), auth.Principal{User: domain.User{ID: "bob"}}, "wf-1", nil, ports.PageFilter{})
	require.ErrorIs(t, err, domain.ErrNotFound, "alice's workflow")

	n, err := svc.Forget(context.Background(), 90*24*time.Hour)
	require.NoError(t, err)
	assert.EqualValues(t, 3, n)
	assert.Equal(t, now.Add(-90*24*time.Hour), repo.before)
}
