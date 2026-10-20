package polymarket

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	polymarketmocks "github.com/lein3000zzz/polythought-go/internal/mocks/polymarket"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

func TestGeoblock(t *testing.T) {
	client := polymarketmocks.NewMockGeoblockClient(t)
	g := NewGeoblock(client)

	client.EXPECT().Geoblock(mock.Anything).Return(pm.Geoblock{Blocked: true, IP: "203.0.113.42", Country: "DE", Region: "HE"}, nil).Once()

	got, err := g.Geoblock(t.Context())
	require.NoError(t, err)
	assert.Equal(t, domain.Geoblock{Blocked: true, IP: "203.0.113.42", Country: "DE", Region: "HE"}, got)

	client.EXPECT().Geoblock(mock.Anything).Return(pm.Geoblock{}, errors.New("dial tcp: refused")).Once()

	_, err = g.Geoblock(t.Context())
	require.ErrorIs(t, err, domain.ErrUnavailable)
}
