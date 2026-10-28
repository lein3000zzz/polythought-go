package auth

import (
	"context"
	"fmt"
	"regexp"
	"slices"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// The bounds of the keys a user sets for the workflow editor's shortcuts (D420): the app
// knows the actions and checks the keys; this keeps what it is given to their shape.
const maxShortcuts = 64

var (
	shortcutAction = regexp.MustCompile(`^[a-z][a-zA-Z]{0,31}$`)
	// Modifiers, then a key: a named one such as enter or arrowup, a letter or digit, or a
	// symbol such as ? or /.
	shortcutKeys = regexp.MustCompile(`^((mod|ctrl|alt|shift)\+){0,4}([a-z0-9]{1,10}|[^\sa-zA-Z0-9+])$`)
)

// SetShortcuts keeps the keys the user set for the editor's shortcuts, by action, in place
// of all they set before: none is every shortcut with its keys to start with.
func (s *Service) SetShortcuts(ctx context.Context, p Principal, keys map[string]string) error {
	if len(keys) > maxShortcuts {
		return fmt.Errorf("%w: at most %d shortcuts", domain.ErrInvalidInput, maxShortcuts)
	}

	for action, binding := range keys {
		if !shortcutAction.MatchString(action) {
			return fmt.Errorf("%w: %q is not a shortcut", domain.ErrInvalidInput, action)
		}

		if !shortcutKeys.MatchString(binding) {
			return fmt.Errorf("%w: %q is not keys a shortcut takes, such as mod+z", domain.ErrInvalidInput, binding)
		}
	}

	if err := s.repos.Users.SetShortcuts(ctx, p.User.ID, keys); err != nil {
		return err
	}

	s.log.Info(ctx, "Shortcuts set", "user_id", p.User.ID, "changed", len(keys))

	return nil
}

// The bounds of the dashboard's parts a user hides (D429): the app knows the parts; this
// keeps what it is given to their shape.
const maxDashboardParts = 16

var dashboardPart = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// SetDashboard keeps how the user set the workflows' dashboard (D429): the parts they
// hid, each once, in place of those they hid before.
func (s *Service) SetDashboard(ctx context.Context, p Principal, d domain.Dashboard) error {
	if len(d.Hidden) > maxDashboardParts {
		return fmt.Errorf("%w: at most %d parts", domain.ErrInvalidInput, maxDashboardParts)
	}

	hidden := make([]string, 0, len(d.Hidden))

	for _, part := range d.Hidden {
		if !dashboardPart.MatchString(part) {
			return fmt.Errorf("%w: %q is not a part of the dashboard", domain.ErrInvalidInput, part)
		}

		if !slices.Contains(hidden, part) {
			hidden = append(hidden, part)
		}
	}

	return s.repos.Users.SetDashboard(ctx, p.User.ID, domain.Dashboard{Hidden: hidden})
}
