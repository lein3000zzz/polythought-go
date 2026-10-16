package auth_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

func TestCodeGate(t *testing.T) {
	plain := user("plain", domain.RoleUser)
	chosen := user("chosen", domain.RoleUser)
	chosen.CodeChosen = true
	admin := user("admin", domain.RoleAdmin)
	failing := errors.New("db down")

	tests := []struct {
		name    string
		mode    domain.CodeAccess
		user    domain.User
		lookup  error // nil: the user is found
		reads   bool  // whether the gate reads the user
		want    bool
		wantErr error
	}{
		{name: "everyone may, without reading the user", mode: domain.CodeEveryone, user: plain, want: true},
		{name: "off is off for admins too", mode: domain.CodeOff, user: admin, want: false},
		{name: "chosen lets a picked user", mode: domain.CodeChosen, user: chosen, reads: true, want: true},
		{name: "chosen lets admins", mode: domain.CodeChosen, user: admin, reads: true, want: true},
		{name: "chosen refuses the others", mode: domain.CodeChosen, user: plain, reads: true, want: false},
		{name: "a failed read is an error, not a no", mode: domain.CodeChosen, user: plain, lookup: failing, reads: true, wantErr: failing},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			users := portsmocks.NewMockUserRepository(t)
			if tt.reads {
				users.EXPECT().ByID(mock.Anything, tt.user.ID).Return(tt.user, tt.lookup).Once()
			}

			gate := auth.CodeGate{Users: users, Mode: func() domain.CodeAccess { return tt.mode }}

			got, err := gate.CodeAllowed(t.Context(), tt.user.ID)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
