package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/koanf/v2"

	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
)

// DefaultFile is where the server looks for its config file when not told (D70).
const DefaultFile = "configs/polythought.yml"

// FileEnv names the environment variable that says where the config file is.
const FileEnv = prefix + "CONFIG"

// File is a config file's values by dotted key, as its YAML nests them: auth: then
// registration: is auth.registration. The bootstrap values sit beside the settings,
// under the keys in Bootstrap.
type File map[string]any

// BootstrapKey is a bootstrap value as the config file names it.
type BootstrapKey struct {
	Key         string // in the file: database.url
	Env         string // the environment variable that wins over it
	Description string
	Default     string // empty when there is none
}

// Bootstrap lists the bootstrap values a config file may hold, in the order the example
// file shows them.
var Bootstrap = []BootstrapKey{
	{"database.url", prefix + "DATABASE_URL", "A Postgres DSN: postgres://user:pass@host:5432/db?sslmode=disable. Required.", ""},
	{"database.auto_migrate", prefix + "AUTO_MIGRATE", "Apply pending database migrations on start.", "true"},
	{"secrets.keys", prefix + "SECRET_KEYS", "The master keys for secrets at rest, \"id:base64key[,id:base64key]\": the first encrypts, the rest only decrypt. Required; better kept in the environment.\nMake one with: echo \"k1:$(openssl rand -base64 32)\"", ""},
	{"http.addr", prefix + "HTTP_ADDR", "The address the server listens on.", ":8080"},
	{"http.public_url", prefix + "PUBLIC_URL", "Where users reach the server: its origin is the only one allowed to make cookie-authenticated changes, and https makes cookies Secure.", "http://localhost:8080"},
	{"admin.email", prefix + "ADMIN_EMAIL", "With admin.password: the first admin, created on start when there is no admin yet.", ""},
	{"admin.password", prefix + "ADMIN_PASSWORD", "The first admin's password, at least 12 characters. Better kept in the environment.", ""},
}

// FilePath is the config file to read and whether it must be there: path when given, as
// by --config; else the one the environment names; else DefaultFile, if it is there.
func FilePath(path string, getenv func(string) string) (string, bool) {
	if path != "" {
		return path, true
	}

	if env := strings.TrimSpace(getenv(FileEnv)); env != "" {
		return env, true
	}

	return DefaultFile, false
}

// ReadFile reads a YAML config file. A file that is not there is an empty one unless it
// is required. Every key must be a bootstrap value or a setting of reg, so a typo fails
// the start, as a bad environment variable does.
func ReadFile(path string, required bool, reg *settings.Registry) (File, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the operator names it
	if errors.Is(err, fs.ErrNotExist) && !required {
		return File{}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("config file %s: %w", path, err)
	}

	k := koanf.New(".")
	if err := k.Load(rawbytes.Provider(raw), yaml.Parser()); err != nil {
		return nil, fmt.Errorf("config file %s: %w", path, err)
	}

	file := File{}

	for key, v := range k.All() {
		if v != nil { // a key with nothing after it, or only comments under it, is not set
			file[key] = v
		}
	}

	var unknown []string

	for key := range file {
		if _, ok := reg.Spec(key); ok || slices.ContainsFunc(Bootstrap, func(b BootstrapKey) bool { return b.Key == key }) {
			continue
		}

		unknown = append(unknown, key)
	}

	if len(unknown) > 0 {
		slices.Sort(unknown)
		return nil, fmt.Errorf("config file %s: no such value: %s", path, strings.Join(unknown, ", "))
	}

	return file, nil
}

// Settings are the file's values of settings, for the settings service's file layer.
func (f File) Settings() map[string]any {
	out := make(map[string]any, len(f))

	for key, v := range f {
		if !slices.ContainsFunc(Bootstrap, func(b BootstrapKey) bool { return b.Key == key }) {
			out[key] = v
		}
	}

	return out
}

// text is the file's value of a bootstrap key as the environment would give it.
func (f File) text(key string) string {
	v, ok := f[key]
	if !ok || v == nil {
		return ""
	}

	return fmt.Sprint(v)
}
