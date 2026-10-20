package geoblock

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

var (
	at    = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	admin = auth.Principal{User: domain.User{ID: "root", Role: domain.RoleAdmin}, SessionID: "s-1"}
	user  = auth.Principal{User: domain.User{ID: "u-1", Role: domain.RoleUser}, SessionID: "s-2"}
)

func newService(t *testing.T) (*Service, *portsmocks.MockGeoblockChecker) {
	t.Helper()

	checker := portsmocks.NewMockGeoblockChecker(t)
	s := New(checker, logging.Noop())
	s.now = func() time.Time { return at }

	return s, checker
}

func TestCheckNow(t *testing.T) {
	s, checker := newService(t)

	st, err := s.Status(context.Background(), admin)
	require.NoError(t, err)
	assert.Nil(t, st.Last, "not checked yet")

	checker.EXPECT().Geoblock(mock.Anything).Return(domain.Geoblock{Blocked: true, IP: "203.0.113.42", Country: "DE", Region: "HE"}, nil).Once()

	st, err = s.CheckNow(context.Background(), admin)
	require.NoError(t, err)
	require.NotNil(t, st.Last)
	assert.Equal(t, domain.Geoblock{Blocked: true, IP: "203.0.113.42", Country: "DE", Region: "HE", CheckedAt: at}, *st.Last)

	// A failed check keeps the last answer, saying it failed.
	checker.EXPECT().Geoblock(mock.Anything).Return(domain.Geoblock{}, errors.New("upstream service unavailable")).Once()

	st, err = s.CheckNow(context.Background(), admin)
	require.NoError(t, err)
	require.NotNil(t, st.Last)
	assert.True(t, st.Last.Blocked)
	assert.Equal(t, "upstream service unavailable", st.Error)

	got, err := s.Status(context.Background(), admin)
	require.NoError(t, err)
	assert.Equal(t, st, got)
}

func TestAdminsOnly(t *testing.T) {
	s, _ := newService(t)

	_, err := s.Status(context.Background(), user)
	require.ErrorIs(t, err, domain.ErrForbidden)

	_, err = s.CheckNow(context.Background(), user)
	require.ErrorIs(t, err, domain.ErrForbidden)
}

func TestCheckEvery(t *testing.T) {
	s, checker := newService(t)

	ctx, cancel := context.WithCancel(context.Background())
	checker.EXPECT().Geoblock(mock.Anything).RunAndReturn(func(context.Context) (domain.Geoblock, error) {
		cancel()
		return domain.Geoblock{Country: "IE"}, nil
	}).Once()

	s.CheckEvery(ctx, time.Hour) // checks at once, then stops with ctx

	st, err := s.Status(context.Background(), admin)
	require.NoError(t, err)
	require.NotNil(t, st.Last)
	assert.Equal(t, "IE", st.Last.Country)
}
