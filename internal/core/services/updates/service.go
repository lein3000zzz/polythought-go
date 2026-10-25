// Package updates says whether a newer Polythought is out (D424): it asks GitHub once a
// day while the check is on, and when an admin asks, and tells the admins of each newer
// release once. Where an updater is set up, an admin has the server updated to it (D425).
package updates

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/buildinfo"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/semver"
)

const (
	every = 24 * time.Hour // between checks: releases are days apart at least
	retry = time.Hour      // after GitHub did not answer

	// updaterWait bounds asking the updater how it is, for a page that shows it.
	updaterWait = 3 * time.Second
)

// noRelease is what the check says while GitHub shows none.
const noRelease = "GitHub shows no release of Polythought yet"

// Notifier tells the admins of a release newer than the server's. It never blocks.
type Notifier interface {
	UpdateAvailable(version, current string)
}

type Config struct {
	// Current is the server's version, such as 0.1.2, or buildinfo.Dev.
	Current string
	// On and Prereleases are the settings, read live: whether to check at all, and
	// whether pre-releases count.
	On          func() bool
	Prereleases func() bool
}

// Status is what is known of updates, for admins.
type Status struct {
	Current     string
	On          bool
	Prereleases bool
	// Latest is the newest release GitHub last gave, nil before it did; Newer says it is
	// newer than the server's, never so for a development build.
	Latest *domain.Release
	Newer  bool
	// Error says why the last check failed, when it did; At is when it was.
	Error string
	At    time.Time

	// Updater says one is set up. Update is its latest update, nil where it did not
	// answer, and UpdaterError why.
	Updater      bool
	Update       *domain.UpdaterStatus
	UpdaterError string
}

type Service struct {
	releases ports.Releases
	notices  ports.ReleaseNotices
	notifier Notifier      // optional
	updater  ports.Updater // optional
	cfg      Config
	log      logging.Logger
	now      func() time.Time

	mu     sync.Mutex
	latest *domain.Release
	asked  bool // whether the last check asked for pre-releases
	err    string
	at     time.Time
	next   time.Time // when the next check is due
	failed bool      // the last check failed, said once in the log
}

type Option func(*Service)

// WithNotifier tells the admins of each newer release (D424).
func WithNotifier(n Notifier) Option {
	return func(s *Service) { s.notifier = n }
}

// WithUpdater lets admins update the server (D425).
func WithUpdater(u ports.Updater) Option {
	return func(s *Service) { s.updater = u }
}

// WithClock replaces the clock, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func New(releases ports.Releases, notices ports.ReleaseNotices, cfg Config, log logging.Logger, opts ...Option) *Service {
	s := &Service{
		releases: releases, notices: notices, cfg: cfg, log: log.With("component", "updates"),
		now: func() time.Time { return time.Now().UTC() },
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Status is what is known of updates, for admins: asking the updater how it is, where one
// is set up.
func (s *Service) Status(ctx context.Context, actor auth.Principal) (Status, error) {
	if !actor.User.IsAdmin() {
		return Status{}, domain.ErrForbidden
	}

	st := s.status()

	if s.updater != nil && s.updater.Set() {
		st.Updater = true

		ctx, cancel := context.WithTimeout(ctx, updaterWait)
		defer cancel()

		u, err := s.updater.Status(ctx)
		if err != nil {
			st.UpdaterError = err.Error()
		} else {
			st.Update = &u
		}
	}

	return st, nil
}

// CheckNow asks GitHub again, for an admin, while the check is on: off, the server asks
// GitHub nothing.
func (s *Service) CheckNow(ctx context.Context, actor auth.Principal) (Status, error) {
	if !actor.User.IsAdmin() {
		return Status{}, domain.ErrForbidden
	}

	if s.cfg.On() {
		s.check(ctx)
	}

	return s.Status(ctx, actor)
}

// Apply has the updater update the server to version, the newest release, newer than the
// server's. Only an admin at a browser does: Polythought restarts, the database's schema
// with it.
func (s *Service) Apply(ctx context.Context, actor auth.Principal, version string) error {
	if !actor.User.IsAdmin() {
		return domain.ErrForbidden
	}

	if !actor.Itself() {
		return fmt.Errorf("%w: an admin updates the server at a browser, on the Config page", domain.ErrForbidden)
	}

	if s.updater == nil || !s.updater.Set() {
		return domain.ErrNoUpdater
	}

	st := s.status()

	switch {
	case st.Current == buildinfo.Dev:
		return fmt.Errorf("%w: a development build is updated from its code, not to a release", domain.ErrInvalidInput)
	case st.Latest == nil:
		return fmt.Errorf("%w: no newer release is known: check for one first", domain.ErrInvalidInput)
	case st.Latest.Version != version:
		return fmt.Errorf("%w: the newest release is %s, to update to", domain.ErrInvalidInput, st.Latest.Version)
	case !st.Newer:
		return fmt.Errorf("%w: the server runs %s already", domain.ErrInvalidInput, st.Current)
	}

	if err := s.updater.Update(ctx, version); err != nil {
		return err
	}

	s.log.Info(ctx, "Update asked of the updater", "version", version, "current", st.Current, "user_id", actor.User.ID)

	return nil
}

// CheckEvery looks at once, then at each tick, asking GitHub when a check is due: a day
// after the last, an hour after one that failed, or at once where pre-releases were
// switched since. The settings are read at each tick, so turning the check on asks at the
// next.
func (s *Service) CheckEvery(ctx context.Context, tick time.Duration) {
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
	if !s.cfg.On() {
		s.mu.Lock()
		s.latest, s.err, s.at, s.next, s.failed = nil, "", time.Time{}, time.Time{}, false
		s.mu.Unlock()

		return
	}

	s.mu.Lock()
	due := !s.now().Before(s.next) || s.asked != s.cfg.Prereleases()
	s.mu.Unlock()

	if due {
		s.check(ctx)
	}
}

func (s *Service) status() Status {
	st := Status{Current: s.cfg.Current, On: s.cfg.On(), Prereleases: s.cfg.Prereleases()}
	if !st.On {
		return st
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st.Error, st.At = s.err, s.at

	// Asked otherwise, it is not the newest as the settings are now: the next tick asks.
	if s.latest != nil && s.asked == st.Prereleases {
		r := *s.latest
		st.Latest, st.Newer = &r, semver.Newer(r.Version, st.Current)
	}

	return st
}

func (s *Service) check(ctx context.Context) {
	pre := s.cfg.Prereleases()

	r, err := s.releases.Newest(ctx, pre)
	if err != nil && ctx.Err() != nil {
		return
	}

	now := s.now()

	s.mu.Lock()

	switch {
	case err == nil:
		if s.failed {
			s.log.Info(ctx, "GitHub answers about releases again", "latest", r.Version)
		}

		s.latest, s.asked, s.err, s.at, s.next, s.failed = &r, pre, "", now, now.Add(every), false
	case errors.Is(err, domain.ErrNotFound):
		// None out, or the repository private: nothing to ask sooner.
		if !s.failed {
			s.log.Info(ctx, noRelease)
		}

		s.latest, s.asked, s.err, s.at, s.next, s.failed = nil, pre, noRelease, now, now.Add(every), true
	default:
		// The last answer stays where it was asked as now: it was right a day ago.
		if !s.failed {
			s.log.Warn(ctx, "Polythought's releases not checked", logging.Err(err))
		}

		if s.asked != pre {
			s.latest = nil
		}

		s.asked, s.err, s.at, s.next, s.failed = pre, err.Error(), now, now.Add(retry), true
	}

	s.mu.Unlock()

	if err == nil {
		s.tell(ctx, r)
	}
}

// tell has the admins told of r once, where it is newer than the server's.
func (s *Service) tell(ctx context.Context, r domain.Release) {
	if s.notifier == nil || !semver.Newer(r.Version, s.cfg.Current) {
		return
	}

	first, err := s.notices.Note(ctx, r.Version, s.now())
	if err != nil {
		s.log.Warn(ctx, "New release not told: not noted", "version", r.Version, logging.Err(err))
		return
	}

	if first {
		s.log.Info(ctx, "A newer Polythought is out", "version", r.Version, "current", s.cfg.Current)
		s.notifier.UpdateAvailable(r.Version, s.cfg.Current)
	}
}
