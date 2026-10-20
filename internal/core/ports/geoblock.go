package ports

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// GeoblockChecker asks Polymarket whether it takes orders from the server's IP;
// domain.ErrUnavailable when it cannot be asked.
type GeoblockChecker interface {
	Geoblock(ctx context.Context) (domain.Geoblock, error)
}
