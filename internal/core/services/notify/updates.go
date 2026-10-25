package notify

import (
	"context"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
)

var UpdateAvailable = Kind{
	Key: "update_available", Group: groupAdmin,
	Label: "A new version is out",
	Description: "A release of Polythought newer than the server's is out, as the update check found. Once per " +
		"release. See what changed, and update, on the Config page.",
	Default: true, EmailDefault: true, Admins: true,
}

type newRelease struct {
	version string
	current string
}

// UpdateAvailable queues telling the admins a newer release is out (D424). It never
// blocks: when the queue is full, it goes untold, the Config page showing it all the same.
func (s *Service) UpdateAvailable(version, current string) {
	select {
	case s.released <- newRelease{version: version, current: current}:
	default:
		s.log.Warn(context.Background(), "New release notification dropped: too many at once", "version", version)
	}
}

func (s *Service) updateAvailable(ctx context.Context, r newRelease) {
	if s.admins == nil {
		return
	}

	admins, err := s.admins.Admins(ctx)
	if err != nil {
		s.log.Warn(ctx, "New release notification skipped", "version", r.version, logging.Err(err))
		return
	}

	said := "Polythought " + r.version + " is out; this server runs " + r.current + ". See what changed, and update, on the Config page."

	for _, a := range admins {
		s.tell(ctx, a.ID, UpdateAvailable, "Polythought "+r.version+" is out", said, "Open Config", s.publicURL+"/admin/config", "🆕 ")
	}
}
