package confirmations_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/confirmations"
)

var at = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// memRepo keeps confirmations in memory, scoped by owner like the real one.
type memRepo struct {
	mu   sync.Mutex
	rows map[domain.ConfirmationID]domain.Confirmation
}

func (m *memRepo) Create(_ context.Context, c domain.Confirmation) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.rows[c.ID] = c

	return nil
}

func (m *memRepo) Get(_ context.Context, owner domain.UserID, id domain.ConfirmationID) (domain.Confirmation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.rows[id]
	if !ok || c.OwnerID != owner {
		return domain.Confirmation{}, domain.ErrNotFound
	}

	return c, nil
}

func (m *memRepo) Recent(_ context.Context, owner domain.UserID, _ int) ([]domain.Confirmation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []domain.Confirmation

	for _, c := range m.rows {
		if c.OwnerID == owner {
			out = append(out, c)
		}
	}

	return out, nil
}

func (m *memRepo) CountPending(_ context.Context, owner domain.UserID, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	n := 0

	for _, c := range m.rows {
		if c.OwnerID == owner && c.StatusAt(now) == domain.ConfirmPending {
			n++
		}
	}

	return n, nil
}

func (m *memRepo) Decide(
	_ context.Context, owner domain.UserID, id domain.ConfirmationID, status domain.ConfirmStatus, now time.Time, via domain.ConfirmVia,
) (domain.Confirmation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.rows[id]
	if !ok || c.OwnerID != owner {
		return domain.Confirmation{}, domain.ErrNotFound
	}

	if c.StatusAt(now) != domain.ConfirmPending {
		return domain.Confirmation{}, domain.ErrConflict
	}

	c.Status, c.DecidedAt, c.DecidedVia = status, now, via
	m.rows[id] = c

	return c, nil
}

func (m *memRepo) Finish(_ context.Context, id domain.ConfirmationID, status domain.ConfirmStatus, result string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	c := m.rows[id]
	c.Status, c.Result = status, result
	m.rows[id] = c

	return nil
}

func (m *memRepo) SetMessage(_ context.Context, id domain.ConfirmationID, message int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	c := m.rows[id]
	c.Message = message
	m.rows[id] = c

	return nil
}

// told is the notify service: Telegram's message is 42, and how each ended is kept.
type told struct {
	mu     sync.Mutex
	asked  []domain.ConfirmationID
	closed []domain.ConfirmStatus
}

func (t *told) ConfirmationAsked(_ context.Context, c domain.Confirmation) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.asked = append(t.asked, c.ID)

	return 42, nil
}

func (t *told) ConfirmationClosed(_ context.Context, c domain.Confirmation) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if c.Message != 42 {
		panic("closed without its message")
	}

	t.closed = append(t.closed, c.Status)
}

type users struct {
	set domain.Unconfirmed
	ask bool
}

func (u *users) SetUnconfirmed(_ context.Context, _ domain.UserID, un domain.Unconfirmed) error {
	u.set = un
	return nil
}

func (u *users) SetAskInAgent(_ context.Context, _ domain.UserID, on bool) error {
	u.ask = on
	return nil
}

func newService(t *testing.T, clock *time.Time) (*confirmations.Service, *memRepo) {
	t.Helper()

	repo := &memRepo{rows: map[domain.ConfirmationID]domain.Confirmation{}}
	svc := confirmations.New(repo, &users{}, logging.Noop(),
		confirmations.WithClock(func() time.Time { return *clock }),
		confirmations.WithExpiry(func() time.Duration { return time.Hour }))

	return svc, repo
}

var (
	alice  = domain.User{ID: "u-1"}
	agent  = auth.Principal{User: alice, GrantID: "g-1", Agent: "Claude Code", Scopes: []domain.Scope{domain.ScopeBuild, domain.ScopeLive}}
	atDesk = auth.Principal{User: alice, SessionID: "s-1"}
)

func held(t *testing.T, err error) domain.Confirmation {
	t.Helper()

	var waiting *domain.AwaitingError
	require.ErrorAs(t, err, &waiting)
	require.ErrorIs(t, err, domain.ErrAwaitsConfirmation)

	return waiting.Confirmation
}

// A token's or an agent's live action waits for its user, unless the user lets it go or
// it is the user's own (D406).
func TestHold(t *testing.T) {
	ctx := context.Background()
	clock := at
	svc, _ := newService(t, &clock)

	c := held(t, svc.Hold(ctx, agent, domain.ConfirmRun, "wf-1", `Run "Spread watch" by hand, which trades live`, map[string]string{"trigger": "manual"}))
	assert.Equal(t, domain.ConfirmPending, c.Status)
	assert.Equal(t, at.Add(time.Hour), c.ExpiresAt)
	assert.Equal(t, "Claude Code", c.Asker())
	assert.JSONEq(t, `{"trigger": "manual"}`, string(c.Payload))
	assert.Equal(t, []domain.Scope{domain.ScopeBuild, domain.ScopeLive}, c.Scopes, "as the agent had them")

	require.NoError(t, svc.Hold(ctx, atDesk, domain.ConfirmRun, "wf-1", "x", nil), "the user's own")

	confirmed := agent
	confirmed.Confirmed = true
	require.NoError(t, svc.Hold(ctx, confirmed, domain.ConfirmRun, "wf-1", "x", nil), "one the user confirmed")

	deploy := auth.Principal{User: alice, TokenID: "t-1", TokenName: "deploy", Scopes: []domain.Scope{domain.ScopeLive}}
	byToken := held(t, svc.Hold(ctx, deploy, domain.ConfirmKillOff, "", "Turn your kill switch off", nil))
	assert.Equal(t, "the API token “deploy”", byToken.Asker(), "a token by its name")

	lets := agent
	lets.User.Unconfirmed.Run = true
	require.NoError(t, svc.Hold(ctx, lets, domain.ConfirmSwitchOn, "wf-1", "x", nil), "switching on goes with running")
	held(t, svc.Hold(ctx, lets, domain.ConfirmGoLive, "wf-1", "x", nil))
}

func TestConfirm(t *testing.T) {
	ctx := context.Background()
	clock := at
	svc, repo := newService(t, &clock)

	var (
		mu  sync.Mutex
		got auth.Principal
	)

	svc.Handle(domain.ConfirmRun, func(_ context.Context, p auth.Principal, c domain.Confirmation) (string, error) {
		mu.Lock()
		defer mu.Unlock()

		got = p

		var in struct{ Trigger string }
		if err := json.Unmarshal(c.Payload, &in); err != nil {
			return "", err
		}

		return "execution ex-1, succeeded, from " + in.Trigger, nil
	})
	svc.Handle(domain.ConfirmKillOff, func(context.Context, auth.Principal, domain.Confirmation) (string, error) {
		return "", errors.New("no kill switch here")
	})

	t.Run("carried out as the agent asked", func(t *testing.T) {
		c := held(t, svc.Hold(ctx, agent, domain.ConfirmRun, "wf-1", "Run it", map[string]string{"trigger": "manual"}))

		_, err := svc.Confirm(ctx, agent, c.ID)
		require.ErrorIs(t, err, domain.ErrForbidden, "never by the agent itself")

		_, err = svc.Confirm(ctx, atDesk, c.ID)
		require.NoError(t, err)
		require.NoError(t, svc.Wait(ctx))

		done, err := svc.Get(ctx, atDesk, c.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ConfirmDone, done.Status)
		assert.Equal(t, "execution ex-1, succeeded, from manual", done.Result)

		mu.Lock()
		defer mu.Unlock()

		assert.Equal(t, auth.Principal{User: alice, GrantID: "g-1", Agent: "Claude Code", Scopes: agent.Scopes, Confirmed: true}, got)

		_, err = svc.Confirm(ctx, atDesk, c.ID)
		require.ErrorIs(t, err, domain.ErrConflict, "once")
		assert.Contains(t, err.Error(), "it is done already")
	})

	t.Run("failing, saying why", func(t *testing.T) {
		c := held(t, svc.Hold(ctx, agent, domain.ConfirmKillOff, "", "Turn your kill switch off", nil))

		_, err := svc.Confirm(ctx, atDesk, c.ID)
		require.NoError(t, err)
		require.NoError(t, svc.Wait(ctx))

		failed, err := svc.Get(ctx, atDesk, c.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ConfirmFailed, failed.Status)
		assert.Equal(t, "no kill switch here", failed.Result)
	})

	t.Run("refused", func(t *testing.T) {
		c := held(t, svc.Hold(ctx, agent, domain.ConfirmRun, "wf-1", "Run it", nil))

		refused, err := svc.Refuse(ctx, atDesk, c.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ConfirmRefused, refused.Status)

		_, err = svc.Confirm(ctx, atDesk, c.ID)
		assert.Contains(t, err.Error(), "it is refused already")
	})

	t.Run("expired", func(t *testing.T) {
		c := held(t, svc.Hold(ctx, agent, domain.ConfirmRun, "wf-1", "Run it", nil))

		clock = at.Add(2 * time.Hour)
		defer func() { clock = at }()

		_, err := svc.Confirm(ctx, atDesk, c.ID)
		assert.Contains(t, err.Error(), "it is expired already")

		list, err := svc.Recent(ctx, atDesk)
		require.NoError(t, err)

		for _, l := range list {
			if l.ID == c.ID {
				assert.Equal(t, domain.ConfirmExpired, l.Status)
			}
		}
	})

	t.Run("someone else's", func(t *testing.T) {
		c := held(t, svc.Hold(ctx, agent, domain.ConfirmRun, "wf-1", "Run it", nil))

		_, err := svc.Confirm(ctx, auth.Principal{User: domain.User{ID: "u-2"}, SessionID: "s-2"}, c.ID)
		require.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("not too many at once", func(t *testing.T) {
		repo.mu.Lock()
		repo.rows = map[domain.ConfirmationID]domain.Confirmation{}
		repo.mu.Unlock()

		for range 20 {
			held(t, svc.Hold(ctx, agent, domain.ConfirmRun, "wf-1", "Run it", nil))
		}

		require.ErrorIs(t, svc.Hold(ctx, agent, domain.ConfirmRun, "wf-1", "Run it", nil), domain.ErrRateLimited)
	})
}

func TestSetUnconfirmed(t *testing.T) {
	ctx := context.Background()
	clock := at
	repo := &memRepo{rows: map[domain.ConfirmationID]domain.Confirmation{}}
	u := &users{}
	svc := confirmations.New(repo, u, logging.Noop(), confirmations.WithClock(func() time.Time { return clock }))

	require.ErrorIs(t, svc.SetUnconfirmed(ctx, agent, domain.Unconfirmed{Run: true}), domain.ErrForbidden, "an agent never loosens its own")
	require.NoError(t, svc.SetUnconfirmed(ctx, atDesk, domain.Unconfirmed{Run: true}))
	assert.Equal(t, domain.Unconfirmed{Run: true}, u.set)
}

// The user is told of what waits, and the message says how it was decided; in their own
// Telegram chat they decide as at a browser (D406).
func TestTold(t *testing.T) {
	ctx := context.Background()
	clock := at
	repo := &memRepo{rows: map[domain.ConfirmationID]domain.Confirmation{}}
	n := &told{}
	svc := confirmations.New(repo, &users{}, logging.Noop(), confirmations.WithNotifier(n),
		confirmations.WithClock(func() time.Time { return clock }))
	svc.Handle(domain.ConfirmRun, func(context.Context, auth.Principal, domain.Confirmation) (string, error) {
		return "execution ex-1, succeeded", nil
	})

	inTelegram := auth.Principal{User: alice}

	run := held(t, svc.Hold(ctx, agent, domain.ConfirmRun, "wf-1", "Run it", nil))
	assert.Equal(t, 42, run.Message)

	kept, err := svc.Get(ctx, atDesk, run.ID)
	require.NoError(t, err)
	assert.Equal(t, 42, kept.Message, "the message kept, to close later")

	_, err = svc.Confirm(ctx, inTelegram, run.ID)
	require.NoError(t, err)
	require.NoError(t, svc.Wait(ctx))

	refused := held(t, svc.Hold(ctx, agent, domain.ConfirmRun, "wf-1", "Run it", nil))
	_, err = svc.Refuse(ctx, inTelegram, refused.ID)
	require.NoError(t, err)

	late := held(t, svc.Hold(ctx, agent, domain.ConfirmRun, "wf-1", "Run it", nil))
	clock = at.Add(2 * time.Hour)
	_, err = svc.Confirm(ctx, inTelegram, late.ID)
	require.ErrorIs(t, err, domain.ErrConflict)

	n.mu.Lock()
	defer n.mu.Unlock()

	assert.Len(t, n.asked, 3)
	assert.Equal(t, []domain.ConfirmStatus{
		domain.ConfirmConfirmed, domain.ConfirmDone, domain.ConfirmRefused, domain.ConfirmExpired,
	}, n.closed)
}

// The agent's own window (D409): where the user lets it, the agent's call asks there and
// waits; the user's answer, there or in Polythought, is the call's to carry out.
func TestInWindow(t *testing.T) {
	ctx := context.Background()
	clock := at

	setup := func(t *testing.T) (*confirmations.Service, *int) {
		t.Helper()

		repo := &memRepo{rows: map[domain.ConfirmationID]domain.Confirmation{}}
		svc := confirmations.New(repo, &users{}, logging.Noop(), confirmations.WithClock(func() time.Time { return clock }))

		ran := 0
		svc.Handle(domain.ConfirmRun, func(context.Context, auth.Principal, domain.Confirmation) (string, error) {
			ran++
			return "done by the service", nil
		})

		return svc, &ran
	}

	asks := agent
	asks.User.AskInAgent = true

	t.Run("only where the user lets it, and only its own", func(t *testing.T) {
		svc, _ := setup(t)
		c := held(t, svc.Hold(ctx, agent, domain.ConfirmRun, "wf-1", "Run it", nil))

		_, err := svc.ConfirmInWindow(ctx, agent, c.ID)
		require.ErrorIs(t, err, domain.ErrForbidden)

		other := asks
		other.GrantID = "g-2"
		_, err = svc.ConfirmInWindow(ctx, other, c.ID)
		require.ErrorIs(t, err, domain.ErrNotFound, "another agent's")
	})

	t.Run("confirmed there, the call carries it out", func(t *testing.T) {
		svc, ran := setup(t)
		c := held(t, svc.Hold(ctx, asks, domain.ConfirmRun, "wf-1", "Run it", nil))

		decided, err := svc.ConfirmInWindow(ctx, asks, c.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ConfirmViaAgent, decided.DecidedVia)

		svc.Finish(ctx, decided, "execution ex-1, succeeded", nil)

		got, err := svc.Get(ctx, atDesk, c.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ConfirmDone, got.Status)
		assert.Equal(t, "execution ex-1, succeeded", got.Result)
		assert.Zero(t, *ran, "not the service's to carry out")
	})

	t.Run("refused there", func(t *testing.T) {
		svc, _ := setup(t)
		c := held(t, svc.Hold(ctx, asks, domain.ConfirmRun, "wf-1", "Run it", nil))

		refused, err := svc.RefuseInWindow(ctx, asks, c.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ConfirmRefused, refused.Status)
	})

	t.Run("confirmed in Polythought while the call awaits it", func(t *testing.T) {
		svc, ran := setup(t)
		c := held(t, svc.Hold(ctx, asks, domain.ConfirmRun, "wf-1", "Run it", nil))

		type answer struct {
			c     domain.Confirmation
			carry bool
			err   error
		}

		got := make(chan answer, 1)

		go func() {
			c, carry, err := svc.Await(ctx, asks, c.ID)
			got <- answer{c, carry, err}
		}()

		require.Eventually(t, func() bool { return svc.Awaited(c.ID) }, time.Second, time.Millisecond)

		_, err := svc.Confirm(ctx, atDesk, c.ID)
		require.NoError(t, err)

		a := <-got
		require.NoError(t, a.err)
		assert.True(t, a.carry, "the call's to carry out")
		assert.Equal(t, domain.ConfirmViaApp, a.c.DecidedVia)

		require.NoError(t, svc.Wait(ctx))
		assert.Zero(t, *ran, "not the service's too")
	})

	t.Run("refused in Polythought while the call awaits it", func(t *testing.T) {
		svc, _ := setup(t)
		c := held(t, svc.Hold(ctx, asks, domain.ConfirmRun, "wf-1", "Run it", nil))

		errs := make(chan error, 1)

		go func() {
			_, _, err := svc.Await(ctx, asks, c.ID)
			errs <- err
		}()

		require.Eventually(t, func() bool { return svc.Awaited(c.ID) }, time.Second, time.Millisecond)

		_, err := svc.Refuse(ctx, atDesk, c.ID)
		require.NoError(t, err)

		require.ErrorIs(t, <-errs, domain.ErrConfirmRefused)
	})

	t.Run("the call gone, the service carries it out", func(t *testing.T) {
		svc, ran := setup(t)
		c := held(t, svc.Hold(ctx, asks, domain.ConfirmRun, "wf-1", "Run it", nil))

		short, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
		defer cancel()

		_, carry, err := svc.Await(short, asks, c.ID)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.False(t, carry)

		_, err = svc.Confirm(ctx, atDesk, c.ID)
		require.NoError(t, err)
		require.NoError(t, svc.Wait(ctx))
		assert.Equal(t, 1, *ran)
	})
}
