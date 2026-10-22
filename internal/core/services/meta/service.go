// Package meta is what the app and the docs show about Polythought itself: its
// repository, and how many stars GitHub counts for it (D372).
package meta

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// Repository is Polythought's on GitHub, as owner/name.
const Repository = "lein3000zzz/polythought-go"

// RepositoryURL is where every page links to (D368).
const RepositoryURL = "https://github.com/" + Repository

const (
	every = time.Hour        // between looks: the count changes slowly
	retry = 10 * time.Minute // after GitHub did not answer
)

type Meta struct {
	// Stars is GitHub's count, nil while it has not said or the lookup is off.
	Stars *int
}

// Service keeps the star count GitHub last gave, and asks again now and then while the
// lookup is on. A request never waits on GitHub.
type Service struct {
	stars ports.RepoStars
	on    func() bool // the setting, read live
	log   logging.Logger
	now   func() time.Time

	mu     sync.Mutex
	count  *int
	next   time.Time // when to look again
	failed bool      // the last look failed, said once in the log
}

func New(stars ports.RepoStars, on func() bool, log logging.Logger) *Service {
	return &Service{stars: stars, on: on, log: log.With("component", "meta"), now: time.Now}
}

func (s *Service) Meta(context.Context) Meta {
	if !s.on() {
		return Meta{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.count == nil {
		return Meta{}
	}

	n := *s.count

	return Meta{Stars: &n}
}

// LookEvery looks at once, then at each tick, asking GitHub when a look is due: an hour
// after the last, ten minutes after one that failed. The setting is read at each tick,
// so turning it on asks at the next.
func (s *Service) LookEvery(ctx context.Context, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()

	for {
		s.look(ctx)

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) look(ctx context.Context) {
	if !s.on() {
		s.mu.Lock()
		s.count, s.next, s.failed = nil, time.Time{}, false
		s.mu.Unlock()

		return
	}

	now := s.now()

	s.mu.Lock()
	due := !now.Before(s.next)
	s.mu.Unlock()

	if !due {
		return
	}

	n, err := s.stars.Stars(ctx)
	if err != nil && ctx.Err() != nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case err == nil:
		if s.failed {
			s.log.Info(ctx, "GitHub's star count is back", "stars", n)
		}

		s.count, s.next, s.failed = &n, now.Add(every), false
	case errors.Is(err, domain.ErrNotFound):
		// Private, or gone: no count to show, and nothing to ask sooner.
		if !s.failed {
			s.log.Info(ctx, "GitHub shows no repository "+Repository+"; the link shows no count")
		}

		s.count, s.next, s.failed = nil, now.Add(every), true
	default:
		// The last count stays: it was right an hour ago.
		if !s.failed {
			s.log.Warn(ctx, "GitHub's star count not had", logging.Err(err))
		}

		s.next, s.failed = now.Add(retry), true
	}
}
