package auth

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// CodeGate says whether a user may write code now, and run the workflows that have it.
// It stands apart from Service, which the engine it serves comes before.
type CodeGate struct {
	Users ports.UserRepository
	Mode  func() domain.CodeAccess // read on every use, so a change applies at once
}

// CodeAllowed reads the user only when the answer depends on who they are.
func (g CodeGate) CodeAllowed(ctx context.Context, owner domain.UserID) (bool, error) {
	mode := g.Mode()
	if mode != domain.CodeChosen {
		return mode.Allows(domain.User{}), nil
	}

	user, err := g.Users.ByID(ctx, owner)
	if err != nil {
		return false, err
	}

	return mode.Allows(user), nil
}
