package polymarket

import (
	"context"
	"fmt"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

var _ ports.GeoblockChecker = (*Geoblock)(nil)

// Geoblock serves ports.GeoblockChecker with Polymarket's geoblock endpoint.
type Geoblock struct {
	client pm.GeoblockClient
}

func NewGeoblock(client pm.GeoblockClient) *Geoblock {
	return &Geoblock{client: client}
}

func (g *Geoblock) Geoblock(ctx context.Context) (domain.Geoblock, error) {
	got, err := g.client.Geoblock(ctx)
	if err != nil {
		return domain.Geoblock{}, fmt.Errorf("%w: %w", domain.ErrUnavailable, err)
	}

	return domain.Geoblock{Blocked: got.Blocked, IP: got.IP, Country: got.Country, Region: got.Region}, nil
}
