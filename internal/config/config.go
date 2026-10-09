// Package config loads settings from POLYTHOUGHT_* environment variables.
package config

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

const prefix = "POLYTHOUGHT_"

type Config struct {
	// DatabaseURL is a Postgres DSN: postgres://user:pass@host:5432/db?sslmode=disable
	DatabaseURL string

	// SecretKeys are the master keys for secrets at rest, "id:base64key[,id:base64key]".
	// The first key encrypts; the rest only decrypt, for rotation.
	SecretKeys string

	// AutoMigrate applies pending migrations on start.
	AutoMigrate bool

	HTTPAddr string

	// PublicURL is where users reach the server. Its origin is the only one allowed to
	// make cookie-authenticated changes; https makes cookies Secure.
	PublicURL string

	// TrustedProxies are the CIDRs allowed to set X-Forwarded-For, comma-separated.
	TrustedProxies []*net.IPNet

	Registration       domain.RegistrationMode
	SessionIdleTTL     time.Duration
	SessionAbsoluteTTL time.Duration

	// ExecutionRetention is how long finished executions are kept.
	ExecutionRetention time.Duration

	// AdminEmail and AdminPassword create the first admin on start when no admin exists.
	AdminEmail    string
	AdminPassword string

	LogLevel  string // debug, info, warn, error
	LogFormat string // text or json
}

// Load reads the configuration through getenv, normally os.Getenv, and reports every
// problem at once.
func Load(getenv func(string) string) (Config, error) {
	var (
		cfg  Config
		errs []error
	)

	env := func(name string) string { return strings.TrimSpace(getenv(prefix + name)) }

	fail := func(name, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s%s: "+format, append([]any{prefix, name}, args...)...))
	}

	required := func(name string) string {
		v := env(name)
		if v == "" {
			errs = append(errs, fmt.Errorf("%s%s is required", prefix, name))
		}

		return v
	}

	withDefault := func(name, def string) string {
		if v := env(name); v != "" {
			return v
		}

		return def
	}

	boolean := func(name string, def bool) bool {
		v := env(name)
		if v == "" {
			return def
		}

		b, err := strconv.ParseBool(v)
		if err != nil {
			fail(name, "%q is not a boolean", v)
		}

		return b
	}

	duration := func(name string, def time.Duration) time.Duration {
		v := env(name)
		if v == "" {
			return def
		}

		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			fail(name, "%q is not a positive duration like 168h", v)
		}

		return d
	}

	cfg.DatabaseURL = required("DATABASE_URL")
	cfg.SecretKeys = required("SECRET_KEYS")
	cfg.AutoMigrate = boolean("AUTO_MIGRATE", true)

	cfg.HTTPAddr = withDefault("HTTP_ADDR", ":8080")
	cfg.PublicURL = withDefault("PUBLIC_URL", "http://localhost:8080")

	for _, raw := range strings.Split(env("TRUSTED_PROXIES"), ",") {
		if raw = strings.TrimSpace(raw); raw == "" {
			continue
		}

		_, cidr, err := net.ParseCIDR(raw)
		if err != nil {
			fail("TRUSTED_PROXIES", "%q is not a CIDR like 10.0.0.0/8", raw)
			continue
		}

		cfg.TrustedProxies = append(cfg.TrustedProxies, cidr)
	}

	cfg.Registration = domain.RegistrationMode(withDefault("REGISTRATION", string(domain.RegistrationInvite)))
	if !cfg.Registration.Valid() {
		fail("REGISTRATION", "%q is not open, invite or closed", cfg.Registration)
	}

	cfg.SessionIdleTTL = duration("SESSION_IDLE_TTL", 7*24*time.Hour)
	cfg.SessionAbsoluteTTL = duration("SESSION_ABSOLUTE_TTL", 30*24*time.Hour)
	cfg.ExecutionRetention = duration("EXECUTION_RETENTION", 30*24*time.Hour)

	cfg.AdminEmail = env("ADMIN_EMAIL")
	cfg.AdminPassword = getenv(prefix + "ADMIN_PASSWORD") // not trimmed: spaces may be part of it

	if (cfg.AdminEmail == "") != (cfg.AdminPassword == "") {
		errs = append(errs, fmt.Errorf("%sADMIN_EMAIL and %sADMIN_PASSWORD must be set together", prefix, prefix))
	}

	cfg.LogLevel = withDefault("LOG_LEVEL", "info")
	cfg.LogFormat = withDefault("LOG_FORMAT", "text")

	if cfg.LogFormat != "text" && cfg.LogFormat != "json" {
		fail("LOG_FORMAT", "%q is not text or json", cfg.LogFormat)
	}

	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		fail("LOG_LEVEL", "%q is not debug, info, warn or error", cfg.LogLevel)
	}

	return cfg, errors.Join(errs...)
}
