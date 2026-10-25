package updates

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/buildinfo"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

var (
	start = time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	admin = auth.Principal{User: domain.User{ID: "root", Role: domain.RoleAdmin}, SessionID: "s-1"}
	user  = auth.Principal{User: domain.User{ID: "u-1", Role: domain.RoleUser}, SessionID: "s-2"}
	token = auth.Principal{User: domain.User{ID: "root", Role: domain.RoleAdmin}, TokenID: "t-1"}

	v002 = domain.Release{Version: "0.0.2", URL: "https://github.com/o/r/releases/tag/v0.0.2", PublishedAt: start}
	beta = domain.Release{Version: "0.1.0-beta.1", Prerelease: true, URL: "https://github.com/o/r/releases/tag/v0.1.0-beta.1"}
)

type clock struct{ at time.Time }

func (c *clock) pass(d time.Duration) { c.at = c.at.Add(d) }

// notices are kept as Postgres keeps them: each version once.
type notices struct {
	mu   sync.Mutex
	told map[string]bool
}

func (n *notices) Note(_ context.Context, version string, _ time.Time) (bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.told[version] {
		return false, nil
	}

	n.told[version] = true

	return true, nil
}

type notifier struct {
	mu   sync.Mutex
	told []string
}

func (n *notifier) UpdateAvailable(version, current string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.told = append(n.told, version+" over "+current)
}

type fixture struct {
	s        *Service
	releases *portsmocks.MockReleases
	updater  *portsmocks.MockUpdater
	on, pre  *atomic.Bool
	clock    *clock
	notices  *notices
	notifier *notifier
}

func newService(t *testing.T, current string) fixture {
	t.Helper()

	f := fixture{
		releases: portsmocks.NewMockReleases(t), updater: portsmocks.NewMockUpdater(t),
		on: &atomic.Bool{}, pre: &atomic.Bool{}, clock: &clock{at: start},
		notices: &notices{told: map[string]bool{}}, notifier: &notifier{},
	}
	f.on.Store(true)

	f.s = New(f.releases, f.notices, Config{Current: current, On: f.on.Load, Prereleases: f.pre.Load}, logging.Noop(),
		WithNotifier(f.notifier), WithUpdater(f.updater), WithClock(func() time.Time { return f.clock.at }))

	return f
}

func (f fixture) status(t *testing.T) Status {
	t.Helper()

	f.updater.EXPECT().Set().Return(false).Once()

	st, err := f.s.Status(context.Background(), admin)
	require.NoError(t, err)

	return st
}

func TestCheck(t *testing.T) {
	ctx := context.Background()
	f := newService(t, "0.0.1")

	st := f.status(t)
	assert.Nil(t, st.Latest, "not checked yet")
	assert.True(t, st.On)
	assert.Equal(t, "0.0.1", st.Current)

	f.releases.EXPECT().Newest(mock.Anything, false).Return(v002, nil).Once()
	f.s.look(ctx)

	st = f.status(t)
	require.NotNil(t, st.Latest)
	assert.Equal(t, v002, *st.Latest)
	assert.True(t, st.Newer)
	assert.Equal(t, start, st.At)
	assert.Equal(t, []string{"0.0.2 over 0.0.1"}, f.notifier.told, "the admins hear of it")

	f.clock.pass(23 * time.Hour)
	f.s.look(ctx) // not due: the mock would fail on a second call

	f.clock.pass(time.Hour)
	f.releases.EXPECT().Newest(mock.Anything, false).Return(v002, nil).Once()
	f.s.look(ctx)
	assert.Len(t, f.notifier.told, 1, "once per release")

	t.Run("pre-releases switched on ask again at once", func(t *testing.T) {
		f.pre.Store(true)
		assert.Nil(t, f.status(t).Latest, "the stable one is not the newest as the settings are now")

		f.releases.EXPECT().Newest(mock.Anything, true).Return(beta, nil).Once()
		f.s.look(ctx)

		st := f.status(t)
		assert.Equal(t, "0.1.0-beta.1", st.Latest.Version)
		assert.Equal(t, []string{"0.0.2 over 0.0.1", "0.1.0-beta.1 over 0.0.1"}, f.notifier.told)
	})
}

func TestCheckFailing(t *testing.T) {
	ctx := context.Background()
	f := newService(t, "0.0.1")

	f.releases.EXPECT().Newest(mock.Anything, false).Return(v002, nil).Once()
	f.s.look(ctx)

	f.clock.pass(24 * time.Hour)
	f.releases.EXPECT().Newest(mock.Anything, false).Return(domain.Release{}, errors.New("upstream service unavailable: GitHub answered 403")).Once()
	f.s.look(ctx)

	st := f.status(t)
	assert.Equal(t, "0.0.2", st.Latest.Version, "the last answer stays")
	assert.Equal(t, "upstream service unavailable: GitHub answered 403", st.Error)

	f.clock.pass(59 * time.Minute)
	f.s.look(ctx) // not due yet

	f.clock.pass(time.Minute)
	f.releases.EXPECT().Newest(mock.Anything, false).Return(domain.Release{}, domain.ErrNotFound).Once()
	f.s.look(ctx)

	st = f.status(t)
	assert.Nil(t, st.Latest, "none, as while the repository is private")
	assert.Equal(t, noRelease, st.Error)

	t.Run("off, nothing is asked and nothing kept", func(t *testing.T) {
		f.on.Store(false)
		f.clock.pass(48 * time.Hour)
		f.s.look(ctx)
		f.updater.EXPECT().Set().Return(false).Once()

		st, err := f.s.CheckNow(ctx, admin) // asks GitHub nothing: the mock would fail
		require.NoError(t, err)
		assert.False(t, st.On)
		assert.Empty(t, st.Error)

		f.on.Store(true)
		f.releases.EXPECT().Newest(mock.Anything, false).Return(v002, nil).Once()
		f.s.look(ctx)
		assert.Equal(t, "0.0.2", f.status(t).Latest.Version, "on again, asked at the next tick")
	})
}

// A development build shows the newest release, newer or not, and tells nobody.
func TestCheckDev(t *testing.T) {
	f := newService(t, buildinfo.Dev)

	f.releases.EXPECT().Newest(mock.Anything, false).Return(v002, nil).Once()
	f.updater.EXPECT().Set().Return(false).Once()

	st, err := f.s.CheckNow(context.Background(), admin)
	require.NoError(t, err)
	assert.Equal(t, "0.0.2", st.Latest.Version)
	assert.False(t, st.Newer)
	assert.Empty(t, f.notifier.told)
}

func TestAdminsOnly(t *testing.T) {
	f := newService(t, "0.0.1")

	_, err := f.s.Status(context.Background(), user)
	require.ErrorIs(t, err, domain.ErrForbidden)

	_, err = f.s.CheckNow(context.Background(), user)
	require.ErrorIs(t, err, domain.ErrForbidden)

	require.ErrorIs(t, f.s.Apply(context.Background(), user, "0.0.2"), domain.ErrForbidden)
}

func TestApply(t *testing.T) {
	ctx := context.Background()

	checked := func(t *testing.T, current string) fixture {
		t.Helper()

		f := newService(t, current)
		f.releases.EXPECT().Newest(mock.Anything, false).Return(v002, nil).Once()
		f.s.look(ctx)

		return f
	}

	t.Run("to the newest release, by its updater", func(t *testing.T) {
		f := checked(t, "0.0.1")
		f.updater.EXPECT().Set().Return(true)
		f.updater.EXPECT().Update(mock.Anything, "0.0.2").Return(nil).Once()

		require.NoError(t, f.s.Apply(ctx, admin, "0.0.2"))

		f.updater.EXPECT().Status(mock.Anything).Return(domain.UpdaterStatus{State: domain.UpdaterUpdating, Version: "0.0.2"}, nil).Once()

		st, err := f.s.Status(ctx, admin)
		require.NoError(t, err)
		assert.True(t, st.Updater)
		assert.Equal(t, domain.UpdaterUpdating, st.Update.State)
	})

	t.Run("an updater that does not answer says so", func(t *testing.T) {
		f := checked(t, "0.0.1")
		f.updater.EXPECT().Set().Return(true)
		f.updater.EXPECT().Status(mock.Anything).Return(domain.UpdaterStatus{}, errors.New("upstream service unavailable: the updater did not answer")).Once()

		st, err := f.s.Status(ctx, admin)
		require.NoError(t, err)
		assert.True(t, st.Updater)
		assert.Nil(t, st.Update)
		assert.Equal(t, "upstream service unavailable: the updater did not answer", st.UpdaterError)
	})

	for name, tc := range map[string]struct {
		current string
		actor   auth.Principal
		set     bool
		version string
		want    error
	}{
		"not from a token":        {"0.0.1", token, true, "0.0.2", domain.ErrForbidden},
		"no updater":              {"0.0.1", admin, false, "0.0.2", domain.ErrNoUpdater},
		"not to another version":  {"0.0.1", admin, true, "0.0.3", domain.ErrInvalidInput},
		"not to what it runs":     {"0.0.2", admin, true, "0.0.2", domain.ErrInvalidInput},
		"not back":                {"0.0.3", admin, true, "0.0.2", domain.ErrInvalidInput},
		"not a development build": {buildinfo.Dev, admin, true, "0.0.2", domain.ErrInvalidInput},
	} {
		t.Run(name, func(t *testing.T) {
			f := checked(t, tc.current)
			f.updater.EXPECT().Set().Return(tc.set).Maybe()

			require.ErrorIs(t, f.s.Apply(ctx, tc.actor, tc.version), tc.want) // Update is never called
		})
	}

	t.Run("nothing checked, nothing to update to", func(t *testing.T) {
		f := newService(t, "0.0.1")
		f.updater.EXPECT().Set().Return(true)

		require.ErrorIs(t, f.s.Apply(ctx, admin, "0.0.2"), domain.ErrInvalidInput)
	})
}
