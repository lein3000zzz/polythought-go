package auth_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// A user keeps the keys they set for the editor's shortcuts, each of a shortcut's shape
// (D420).
func TestSetShortcuts(t *testing.T) {
	ctx := context.Background()
	u := user("u1", domain.RoleUser)

	t.Run("kept, in place of all before", func(t *testing.T) {
		d := newDeps(t)
		keys := map[string]string{"undo": "mod+z", "help": "?", "fitView": "shift+1", "zoomIn": "plus", "run": "mod+alt+enter"}
		d.users.EXPECT().SetShortcuts(mock.Anything, u.ID, keys).Return(nil)

		require.NoError(t, newService(t, d, config(domain.RegistrationOpen)).SetShortcuts(ctx, sessionPrincipal(u), keys))
	})

	t.Run("none: every one as it started", func(t *testing.T) {
		d := newDeps(t)
		d.users.EXPECT().SetShortcuts(mock.Anything, u.ID, map[string]string(nil)).Return(nil)

		require.NoError(t, newService(t, d, config(domain.RegistrationOpen)).SetShortcuts(ctx, sessionPrincipal(u), nil))
	})

	many := map[string]string{}
	for i := range 65 {
		many[fmt.Sprintf("a%c", 'a'+i%26)+fmt.Sprint(i)] = "x"
	}

	for name, keys := range map[string]map[string]string{
		"an action of no shape": {"Undo!": "mod+z"},
		"keys of no shape":      {"undo": "mod+"},
		"a word":                {"undo": "mod+zzzzzzzzzzzzz"},
		"spaces":                {"undo": "mod+ "},
		"too many":              many,
	} {
		t.Run(name+", refused", func(t *testing.T) {
			err := newService(t, newDeps(t), config(domain.RegistrationOpen)).SetShortcuts(ctx, sessionPrincipal(u), keys)
			require.ErrorIs(t, err, domain.ErrInvalidInput)
		})
	}
}
