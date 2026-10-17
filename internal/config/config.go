// Package config loads the bootstrap configuration, what the server needs before it can
// reach its database, from POLYTHOUGHT_* environment variables and a YAML config file
// (D69), the environment winning. Everything else is a setting (see Settings), which an
// admin can change on the Config page unless the environment or the file sets it.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
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

	// AdminEmail and AdminPassword create the first admin on start when no admin exists.
	AdminEmail    string
	AdminPassword string
}

// Load reads the configuration through getenv, normally os.Getenv, then file, a config
// file's values, and reports every problem at once.
func Load(getenv func(string) string, file File) (Config, error) {
	var (
		cfg  Config
		errs []error
	)

	keys := make(map[string]string, len(Bootstrap))
	for _, b := range Bootstrap {
		keys[b.Env] = b.Key
	}

	// raw is a value as written: the environment's, else the file's.
	raw := func(name string) string {
		if v := getenv(prefix + name); strings.TrimSpace(v) != "" {
			return v
		}

		return file.text(keys[prefix+name])
	}

	env := func(name string) string { return strings.TrimSpace(raw(name)) }

	// where names both places a value can come from, for errors.
	where := func(name string) string {
		return fmt.Sprintf("%s%s (or %s in the config file)", prefix, name, keys[prefix+name])
	}

	fail := func(name, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: "+format, append([]any{where(name)}, args...)...))
	}

	required := func(name string) string {
		v := env(name)
		if v == "" {
			errs = append(errs, fmt.Errorf("%s is required", where(name)))
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

	cfg.DatabaseURL = required("DATABASE_URL")
	cfg.SecretKeys = required("SECRET_KEYS")
	cfg.AutoMigrate = boolean("AUTO_MIGRATE", true)

	cfg.HTTPAddr = withDefault("HTTP_ADDR", ":8080")
	cfg.PublicURL = withDefault("PUBLIC_URL", "http://localhost:8080")

	cfg.AdminEmail = env("ADMIN_EMAIL")
	cfg.AdminPassword = raw("ADMIN_PASSWORD") // not trimmed: spaces may be part of it

	if (cfg.AdminEmail == "") != (cfg.AdminPassword == "") {
		errs = append(errs, fmt.Errorf("%s and %s must be set together", where("ADMIN_EMAIL"), where("ADMIN_PASSWORD")))
	}

	return cfg, errors.Join(errs...)
}
