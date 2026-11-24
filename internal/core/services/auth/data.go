package auth

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// DataGate says whether a user may use datasets and models now, and run the workflows
// that do.
type DataGate struct {
	Users ports.UserRepository
	Mode  func() domain.DataAccess // read on every use, so a change applies at once
}

// DataAllowed reads the user only when the answer depends on who they are.
func (g DataGate) DataAllowed(ctx context.Context, owner domain.UserID) (bool, error) {
	mode := g.Mode()
	if mode != domain.DataChosen {
		return mode.Allows(domain.User{}), nil
	}

	user, err := g.Users.ByID(ctx, owner)
	if err != nil {
		return false, err
	}

	return mode.Allows(user), nil
}
