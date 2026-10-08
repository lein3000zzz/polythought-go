package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/config"
)

func TestLoad(t *testing.T) {
	base := map[string]string{
		"POLYTHOUGHT_DATABASE_URL": "postgres://localhost/db",
		"POLYTHOUGHT_SECRET_KEYS":  "k1:abc",
	}

	with := func(overrides map[string]string) map[string]string {
		env := make(map[string]string, len(base)+len(overrides))
		for k, v := range base {
			env[k] = v
		}

		for k, v := range overrides {
			env[k] = v
		}

		return env
	}

	tests := []struct {
		name       string
		env        map[string]string
		want       config.Config
		wantErrors []string
	}{
		{
			name: "defaults",
			env:  base,
			want: config.Config{DatabaseURL: "postgres://localhost/db", SecretKeys: "k1:abc", AutoMigrate: true},
		},
		{
			name: "auto migrate off",
			env:  with(map[string]string{"POLYTHOUGHT_AUTO_MIGRATE": "false"}),
			want: config.Config{DatabaseURL: "postgres://localhost/db", SecretKeys: "k1:abc", AutoMigrate: false},
		},
		{
			name:       "missing required values are all reported",
			env:        map[string]string{},
			wantErrors: []string{"POLYTHOUGHT_DATABASE_URL is required", "POLYTHOUGHT_SECRET_KEYS is required"},
		},
		{
			name:       "bad boolean",
			env:        with(map[string]string{"POLYTHOUGHT_AUTO_MIGRATE": "sometimes"}),
			wantErrors: []string{`POLYTHOUGHT_AUTO_MIGRATE: "sometimes" is not a boolean`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.Load(func(k string) string { return tt.env[k] })

			if tt.wantErrors != nil {
				require.Error(t, err)

				for _, e := range tt.wantErrors {
					assert.Contains(t, err.Error(), e)
				}

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg)
		})
	}
}
