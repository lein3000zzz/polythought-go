package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/config"
)

func write(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "polythought.yml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

func TestReadFile(t *testing.T) {
	t.Parallel()

	t.Run("nested keys, bootstrap values beside settings", func(t *testing.T) {
		t.Parallel()

		file, err := config.ReadFile(write(t, `
database:
  url: postgres://x
http:
  public_url: https://app.example
  trusted_proxies: [10.0.0.0/8]
auth:
  registration: open
  min_password_length: 16
code:
  time: 10s
`), true, registry(t))
		require.NoError(t, err)

		assert.Equal(t, config.File{
			"database.url": "postgres://x", "http.public_url": "https://app.example",
			"http.trusted_proxies": []any{"10.0.0.0/8"}, "auth.registration": "open",
			"auth.min_password_length": 16, "code.time": "10s",
		}, file)
		assert.Equal(t, map[string]any{
			"http.trusted_proxies": []any{"10.0.0.0/8"}, "auth.registration": "open",
			"auth.min_password_length": 16, "code.time": "10s",
		}, file.Settings(), "the settings, without the bootstrap values")
	})

	t.Run("a key that is neither is refused", func(t *testing.T) {
		t.Parallel()

		_, err := config.ReadFile(write(t, "auth:\n  registraton: open\ndatabase:\n  uri: x\n"), true, registry(t))
		assert.ErrorContains(t, err, "no such value: auth.registraton, database.uri")
	})

	t.Run("YAML that does not parse", func(t *testing.T) {
		t.Parallel()

		_, err := config.ReadFile(write(t, "auth: [\n"), true, registry(t))
		assert.ErrorContains(t, err, "config file")
	})

	t.Run("the default file may be missing; a named one may not", func(t *testing.T) {
		t.Parallel()

		missing := filepath.Join(t.TempDir(), "none.yml")

		file, err := config.ReadFile(missing, false, registry(t))
		require.NoError(t, err)
		assert.Empty(t, file)

		_, err = config.ReadFile(missing, true, registry(t))
		assert.ErrorContains(t, err, "none.yml")
	})
}

func TestFilePath(t *testing.T) {
	t.Parallel()

	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "POLYTHOUGHT_CONFIG" {
				return v
			}

			return ""
		}
	}

	path, required := config.FilePath("given.yml", env("env.yml"))
	assert.Equal(t, "given.yml", path, "the flag wins")
	assert.True(t, required)

	path, required = config.FilePath("", env("env.yml"))
	assert.Equal(t, "env.yml", path)
	assert.True(t, required)

	path, required = config.FilePath("", env(""))
	assert.Equal(t, "configs/polythought.yml", path)
	assert.False(t, required, "the default may be missing")
}
