// Package config loads settings from POLYTHOUGHT_* environment variables.
package config

import (
	"errors"
	"fmt"
	"strconv"
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
}

// Load reads the configuration through getenv, normally os.Getenv.
func Load(getenv func(string) string) (Config, error) {
	var (
		cfg  Config
		errs []error
	)

	env := func(name string) string { return getenv(prefix + name) }

	required := func(name string) string {
		v := env(name)
		if v == "" {
			errs = append(errs, fmt.Errorf("%s%s is required", prefix, name))
		}

		return v
	}

	boolean := func(name string, def bool) bool {
		v := env(name)
		if v == "" {
			return def
		}

		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s%s: %q is not a boolean", prefix, name, v))
		}

		return b
	}

	cfg.DatabaseURL = required("DATABASE_URL")
	cfg.SecretKeys = required("SECRET_KEYS")
	cfg.AutoMigrate = boolean("AUTO_MIGRATE", true)

	return cfg, errors.Join(errs...)
}
