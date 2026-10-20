// Package geoblock asks Polymarket whether it takes orders from the server's IP, at start,
// every few hours and when an admin asks (D186), and warns while it does not (D187). Only
// the server's IP is asked about (D188).
package geoblock

import (
	"context"
	"sync"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// Status is the last check: its answer, or the error asking; neither before the first.
type Status struct {
	Last  *domain.Geoblock
	Error string
	At    time.Time
}

type Service struct {
	checker ports.GeoblockChecker
	log     logging.Logger
	now     func() time.Time

	mu     sync.Mutex
	status Status
}

func New(checker ports.GeoblockChecker, log logging.Logger) *Service {
	return &Service{checker: checker, log: log.With("component", "geoblock"), now: func() time.Time { return time.Now().UTC() }}
}

// Status is the last check, for admins.
func (s *Service) Status(_ context.Context, actor auth.Principal) (Status, error) {
	if !actor.User.IsAdmin() {
		return Status{}, domain.ErrForbidden
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.status, nil
}

// CheckNow asks Polymarket again, for an admin.
func (s *Service) CheckNow(ctx context.Context, actor auth.Principal) (Status, error) {
	if !actor.User.IsAdmin() {
		return Status{}, domain.ErrForbidden
	}

	return s.check(ctx), nil
}

// CheckEvery checks at once, then every every, until ctx is done.
func (s *Service) CheckEvery(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()

	for {
		s.check(ctx)

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) check(ctx context.Context) Status {
	got, err := s.checker.Geoblock(ctx)
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		if ctx.Err() != nil {
			return s.status
		}

		// The last answer stays: it is what is known.
		s.status.Error, s.status.At = err.Error(), now
		s.log.Warn(ctx, "Polymarket's geoblock not checked", logging.Err(err))

		return s.status
	}

	got.CheckedAt = now
	s.status = Status{Last: &got, At: now}

	if got.Blocked {
		s.log.Warn(ctx, "Polymarket geoblocks this server: it refuses orders from here, or restricts its website",
			"country", got.Country, "region", got.Region)
	} else {
		s.log.Info(ctx, "Polymarket takes orders from this server", "country", got.Country, "region", got.Region)
	}

	return s.status
}
