package ports

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// Releases asks GitHub for Polythought's releases (D424): the newest by version,
// pre-releases among them only when asked. domain.ErrNotFound while there is none, or
// GitHub shows no such repository, as while it is private; domain.ErrUnavailable when
// GitHub cannot be asked.
type Releases interface {
	Newest(ctx context.Context, prereleases bool) (domain.Release, error)
}

// ReleaseNotices keeps which releases admins were told of (D424), so each is told once,
// restarts and all.
type ReleaseNotices interface {
	// Note records version as told at at: false where it was already.
	Note(ctx context.Context, version string, at time.Time) (bool, error)
}

// Updater has the server's own containers updated to a release (D425): Polythought's
// updater, run beside them. domain.ErrUnavailable where it cannot be reached or refuses
// the server, domain.ErrUpdating while it updates already.
type Updater interface {
	// Set says whether one is set up; it is asked nothing otherwise.
	Set() bool
	Update(ctx context.Context, version string) error
	Status(ctx context.Context) (domain.UpdaterStatus, error)
}
