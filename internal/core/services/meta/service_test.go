package meta

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

var start = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

type clock struct{ at time.Time }

func (c *clock) pass(d time.Duration) { c.at = c.at.Add(d) }

func newService(t *testing.T) (*Service, *portsmocks.MockRepoStars, *atomic.Bool, *clock) {
	t.Helper()

	stars := portsmocks.NewMockRepoStars(t)
	on := &atomic.Bool{}
	on.Store(true)
	c := &clock{at: start}

	s := New(stars, on.Load, logging.Noop())
	s.now = func() time.Time { return c.at }

	return s, stars, on, c
}

func stars(s *Service) any {
	if m := s.Meta(context.Background()); m.Stars != nil {
		return *m.Stars
	}

	return nil
}

func TestLook(t *testing.T) {
	ctx := context.Background()
	s, gh, _, c := newService(t)

	assert.Nil(t, stars(s), "nothing before the first look")

	gh.EXPECT().Stars(mock.Anything).Return(1234, nil).Once()
	s.look(ctx)
	assert.Equal(t, 1234, stars(s))

	c.pass(59 * time.Minute)
	s.look(ctx) // not due: the mock would fail on a second call
	assert.Equal(t, 1234, stars(s))

	c.pass(time.Minute)
	gh.EXPECT().Stars(mock.Anything).Return(1240, nil).Once()
	s.look(ctx)
	assert.Equal(t, 1240, stars(s), "an hour on")
}

// A failing GitHub leaves the last count, and is asked again sooner; a repository it
// shows none of has no count.
func TestLookFailing(t *testing.T) {
	ctx := context.Background()
	s, gh, _, c := newService(t)

	gh.EXPECT().Stars(mock.Anything).Return(1234, nil).Once()
	s.look(ctx)

	c.pass(time.Hour)
	gh.EXPECT().Stars(mock.Anything).Return(0, errors.New("upstream service unavailable")).Once()
	s.look(ctx)
	assert.Equal(t, 1234, stars(s), "the last count stays")

	c.pass(9 * time.Minute)
	s.look(ctx) // not due yet

	c.pass(time.Minute)
	gh.EXPECT().Stars(mock.Anything).Return(0, domain.ErrNotFound).Once()
	s.look(ctx)
	assert.Nil(t, stars(s), "private again")

	c.pass(10 * time.Minute)
	s.look(ctx) // no sooner than an hour after a 404
}

func TestLookOff(t *testing.T) {
	ctx := context.Background()
	s, gh, on, c := newService(t)

	gh.EXPECT().Stars(mock.Anything).Return(1234, nil).Once()
	s.look(ctx)

	on.Store(false)
	assert.Nil(t, stars(s), "no count while off")

	c.pass(2 * time.Hour)
	s.look(ctx) // GitHub is not asked

	on.Store(true)
	assert.Nil(t, stars(s), "the old count went with the setting")

	gh.EXPECT().Stars(mock.Anything).Return(1300, nil).Once()
	s.look(ctx)
	assert.Equal(t, 1300, stars(s), "asked at once, back on")
}

func TestLookStopping(t *testing.T) {
	s, gh, _, _ := newService(t)
	ctx, cancel := context.WithCancel(context.Background())

	gh.EXPECT().Stars(mock.Anything).RunAndReturn(func(context.Context) (int, error) {
		cancel()
		return 0, context.Canceled
	}).Once()
	s.look(ctx)

	assert.False(t, s.failed, "stopping is not GitHub failing")
	assert.True(t, s.next.IsZero(), "still due")
}
