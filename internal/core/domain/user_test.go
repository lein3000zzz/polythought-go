package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

func TestCodeAccessAllows(t *testing.T) {
	plain := domain.User{Role: domain.RoleUser}
	chosen := domain.User{Role: domain.RoleUser, CodeChosen: true}
	admin := domain.User{Role: domain.RoleAdmin}

	tests := []struct {
		mode domain.CodeAccess
		user domain.User
		want bool
	}{
		{domain.CodeEveryone, plain, true},
		{domain.CodeEveryone, admin, true},
		{domain.CodeChosen, plain, false},
		{domain.CodeChosen, chosen, true},
		{domain.CodeChosen, admin, true}, // D46: admins always, under chosen
		{domain.CodeOff, chosen, false},
		{domain.CodeOff, admin, false}, // and off is off for them too
		{"", admin, false},             // an unknown mode lets no one
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.mode.Allows(tt.user), "%q, %+v", tt.mode, tt.user)
	}
}
