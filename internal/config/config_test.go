package config_test

import (
	"maps"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/config"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
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
	cfg, err := config.Load(func(k string) string { return base[k] })
	require.NoError(t, err)

	assert.Equal(t, config.Config{
		DatabaseURL:        "postgres://localhost/db",
		SecretKeys:         "k1:abc",
		AutoMigrate:        true,
		HTTPAddr:           ":8080",
		PublicURL:          "http://localhost:8080",
		Registration:       domain.RegistrationInvite,
		SessionIdleTTL:     7 * 24 * time.Hour,
		SessionAbsoluteTTL: 30 * 24 * time.Hour,
		ExecutionRetention: 30 * 24 * time.Hour,
		LogLevel:           "info",
		LogFormat:          "text",
	}, cfg)
}

func TestLoad(t *testing.T) {
	_, private, _ := net.ParseCIDR("10.0.0.0/8")
	_, v6, _ := net.ParseCIDR("fd00::/8")

	tests := []struct {
		name       string
		env        map[string]string
		check      func(t *testing.T, cfg config.Config)
		wantErrors []string
	}{
		{
			name: "overrides",
			env: with(map[string]string{
				"POLYTHOUGHT_AUTO_MIGRATE":         "false",
				"POLYTHOUGHT_HTTP_ADDR":            "0.0.0.0:9000",
				"POLYTHOUGHT_PUBLIC_URL":           "https://app.example",
				"POLYTHOUGHT_TRUSTED_PROXIES":      "10.0.0.0/8, fd00::/8",
				"POLYTHOUGHT_REGISTRATION":         "open",
				"POLYTHOUGHT_SESSION_IDLE_TTL":     "24h",
				"POLYTHOUGHT_SESSION_ABSOLUTE_TTL": "72h",
				"POLYTHOUGHT_ADMIN_EMAIL":          "root@example.com",
				"POLYTHOUGHT_ADMIN_PASSWORD":       " spaced secret ",
				"POLYTHOUGHT_LOG_LEVEL":            "debug",
				"POLYTHOUGHT_LOG_FORMAT":           "json",
			}),
			check: func(t *testing.T, cfg config.Config) {
				assert.False(t, cfg.AutoMigrate)
				assert.Equal(t, "0.0.0.0:9000", cfg.HTTPAddr)
				assert.Equal(t, "https://app.example", cfg.PublicURL)
				assert.Equal(t, []*net.IPNet{private, v6}, cfg.TrustedProxies)
				assert.Equal(t, domain.RegistrationOpen, cfg.Registration)
				assert.Equal(t, 24*time.Hour, cfg.SessionIdleTTL)
				assert.Equal(t, 72*time.Hour, cfg.SessionAbsoluteTTL)
				assert.Equal(t, " spaced secret ", cfg.AdminPassword, "passwords keep their spaces")
				assert.Equal(t, "json", cfg.LogFormat)
			},
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
		{
			name:       "bad proxy cidr",
			env:        with(map[string]string{"POLYTHOUGHT_TRUSTED_PROXIES": "10.0.0.1"}),
			wantErrors: []string{`POLYTHOUGHT_TRUSTED_PROXIES: "10.0.0.1" is not a CIDR`},
		},
		{
			name:       "unknown registration mode",
			env:        with(map[string]string{"POLYTHOUGHT_REGISTRATION": "maybe"}),
			wantErrors: []string{`POLYTHOUGHT_REGISTRATION: "maybe" is not open, invite or closed`},
		},
		{
			name:       "bad durations",
			env:        with(map[string]string{"POLYTHOUGHT_SESSION_IDLE_TTL": "7d", "POLYTHOUGHT_SESSION_ABSOLUTE_TTL": "-1h"}),
			wantErrors: []string{`POLYTHOUGHT_SESSION_IDLE_TTL: "7d"`, `POLYTHOUGHT_SESSION_ABSOLUTE_TTL: "-1h"`},
		},
		{
			name:       "admin email without password",
			env:        with(map[string]string{"POLYTHOUGHT_ADMIN_EMAIL": "root@example.com"}),
			wantErrors: []string{"must be set together"},
		},
		{
			name:       "bad log settings",
			env:        with(map[string]string{"POLYTHOUGHT_LOG_LEVEL": "loud", "POLYTHOUGHT_LOG_FORMAT": "xml"}),
			wantErrors: []string{`POLYTHOUGHT_LOG_LEVEL: "loud"`, `POLYTHOUGHT_LOG_FORMAT: "xml"`},
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
			tt.check(t, cfg)
		})
	}
}
