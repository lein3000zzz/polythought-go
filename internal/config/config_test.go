package config_test

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/config"
)

var base = map[string]string{
	"POLYTHOUGHT_DATABASE_URL": "postgres://localhost/db",
	"POLYTHOUGHT_SECRET_KEYS":  "k1:abc",
}

func with(overrides map[string]string) map[string]string {
	env := maps.Clone(base)
	maps.Copy(env, overrides)

	return env
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load(func(k string) string { return base[k] }, nil)
	require.NoError(t, err)

	assert.Equal(t, config.Config{
		DatabaseURL: "postgres://localhost/db",
		SecretKeys:  "k1:abc",
		AutoMigrate: true,
		HTTPAddr:    ":8080",
		PublicURL:   "http://localhost:8080",
	}, cfg)
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		file       config.File
		check      func(t *testing.T, cfg config.Config)
		wantErrors []string
	}{
		{
			name: "overrides",
			env: with(map[string]string{
				"POLYTHOUGHT_AUTO_MIGRATE":   "false",
				"POLYTHOUGHT_HTTP_ADDR":      "0.0.0.0:9000",
				"POLYTHOUGHT_PUBLIC_URL":     "https://app.example",
				"POLYTHOUGHT_ADMIN_EMAIL":    "root@example.com",
				"POLYTHOUGHT_ADMIN_PASSWORD": " spaced secret ",
			}),
			check: func(t *testing.T, cfg config.Config) {
				assert.False(t, cfg.AutoMigrate)
				assert.Equal(t, "0.0.0.0:9000", cfg.HTTPAddr)
				assert.Equal(t, "https://app.example", cfg.PublicURL)
				assert.Equal(t, "root@example.com", cfg.AdminEmail)
				assert.Equal(t, " spaced secret ", cfg.AdminPassword, "passwords keep their spaces")
			},
		},
		{
			name: "missing required values are all reported",
			env:  map[string]string{},
			wantErrors: []string{
				"POLYTHOUGHT_DATABASE_URL (or database.url in the config file) is required",
				"POLYTHOUGHT_SECRET_KEYS (or secrets.keys in the config file) is required",
			},
		},
		{
			name: "from the config file, the environment winning",
			env:  map[string]string{"POLYTHOUGHT_HTTP_ADDR": ":9000"},
			file: config.File{
				"database.url": "postgres://file", "secrets.keys": "k1:abc", "database.auto_migrate": false,
				"http.addr": ":7000", "http.public_url": "https://file.example",
				"admin.email": "root@example.com", "admin.password": " from file ",
			},
			check: func(t *testing.T, cfg config.Config) {
				assert.Equal(t, "postgres://file", cfg.DatabaseURL)
				assert.Equal(t, "k1:abc", cfg.SecretKeys)
				assert.False(t, cfg.AutoMigrate, "YAML's false, as text")
				assert.Equal(t, ":9000", cfg.HTTPAddr, "the environment wins")
				assert.Equal(t, "https://file.example", cfg.PublicURL)
				assert.Equal(t, " from file ", cfg.AdminPassword)
			},
		},
		{
			name:       "a bad value in the file says where",
			env:        with(map[string]string{}),
			file:       config.File{"database.auto_migrate": "sometimes"},
			wantErrors: []string{`POLYTHOUGHT_AUTO_MIGRATE (or database.auto_migrate in the config file): "sometimes" is not a boolean`},
		},
		{
			name:       "bad boolean",
			env:        with(map[string]string{"POLYTHOUGHT_AUTO_MIGRATE": "sometimes"}),
			wantErrors: []string{`POLYTHOUGHT_AUTO_MIGRATE (or database.auto_migrate in the config file): "sometimes" is not a boolean`},
		},
		{
			name:       "admin email without password",
			env:        with(map[string]string{"POLYTHOUGHT_ADMIN_EMAIL": "root@example.com"}),
			wantErrors: []string{"must be set together"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.Load(func(k string) string { return tt.env[k] }, tt.file)

			if tt.wantErrors != nil {
				require.Error(t, err)

				for _, e := range tt.wantErrors {
					assert.Contains(t, err.Error(), e)
				}

				return
			}

			require.NoError(t, err)
			tt.check(t, cfg)
		})
	}
}
