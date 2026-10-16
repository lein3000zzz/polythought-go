package ports

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// CustomNodeRepository stores the node types users define. Every call is for one owner's:
// someone else's is not found.
type CustomNodeRepository interface {
	// Create returns domain.ErrCustomNodeTaken when the owner has one by that name.
	Create(ctx context.Context, n domain.CustomNode) error
	// Update replaces all but the name, if expectedVersion is still current: it returns
	// domain.ErrConflict when it is not, and domain.ErrNotFound when the node is gone.
	Update(ctx context.Context, n domain.CustomNode, expectedVersion int) error
	Get(ctx context.Context, owner domain.UserID, id domain.CustomNodeID) (domain.CustomNode, error)
	// List returns the owner's custom nodes by name.
	List(ctx context.Context, owner domain.UserID) ([]domain.CustomNode, error)
	CountForOwner(ctx context.Context, owner domain.UserID) (int, error)
	Delete(ctx context.Context, owner domain.UserID, id domain.CustomNodeID) error
}
