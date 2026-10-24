package mcpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/credentials"
)

const secret = "Bearer s3cret-from-the-user"

var header = domain.Credential{
	ID: "cred-1", OwnerID: "u-1", Type: "http.header", Name: "Discord bot",
	Fields: map[string]string{"header": "Authorization"}, Secrets: []string{"value"},
}

// vault is the credentials service: it keeps what it was given, secrets and all, and
// gives back what the real one does, never a secret.
type vault struct {
	mu      sync.Mutex
	created credentials.Input
	changed credentials.Change
}

func (*vault) Types() []engine.CredentialType {
	return []engine.CredentialType{{
		Name: "http.header", Label: "HTTP header", Description: "A header sent with each request.",
		Fields: []engine.CredentialField{
			{Name: "header", Label: "Header", Required: true},
			{Name: "value", Label: "Value", Required: true, Secret: true},
		},
		Test: func(context.Context, map[string]string) error { return nil },
	}}
}

func (*vault) List(context.Context, auth.Principal) ([]domain.Credential, error) {
	return []domain.Credential{header}, nil
}

func (v *vault) Create(_ context.Context, _ auth.Principal, in credentials.Input) (domain.Credential, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.created = in
	c := header
	c.ID, c.Name, c.Fields = "cred-2", in.Name, map[string]string{"header": in.Values["header"]}

	return c, nil
}

func (v *vault) Update(_ context.Context, p auth.Principal, id domain.CredentialID, ch credentials.Change) (domain.Credential, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if _, moved := ch.Values["header"]; moved && !p.MayUseCredentials() {
		return domain.Credential{}, fmt.Errorf("%w: changing Header needs the user to let agents and tokens use credentials", domain.ErrForbidden)
	}

	v.changed = ch
	c := header
	c.ID, c.Name = id, ch.Name

	return c, nil
}

func (*vault) Delete(_ context.Context, p auth.Principal, _ domain.CredentialID) error {
	return p.NeedDelete()
}

func (*vault) Test(_ context.Context, p auth.Principal, id domain.CredentialID) (domain.Credential, error) {
	if !p.MayUseCredentials() {
		return domain.Credential{}, fmt.Errorf("%w: testing a credential needs the user to let agents and tokens use credentials", domain.ErrForbidden)
	}

	c := header
	c.ID, c.TestedAt, c.TestError = id, at, "Discord answered 401"

	return c, nil
}

func withVault(t *testing.T, u domain.User, v *vault, scopes ...domain.Scope) *mcp.ClientSession {
	t.Helper()

	d := deps(u, &trades{}, scopes)
	d.Credentials = v

	return connect(t, d)
}

func TestCredentialTools(t *testing.T) {
	v := &vault{}
	agent := withVault(t, alice, v, domain.ScopeBuild)

	t.Run("its kinds", func(t *testing.T) {
		got, _ := call(t, agent, "list_credential_types", nil)
		assert.Equal(t, []any{map[string]any{
			"name": "http.header", "label": "HTTP header", "description": "A header sent with each request.", "testable": true,
			"fields": []any{
				map[string]any{"name": "header", "label": "Header", "required": true},
				map[string]any{"name": "value", "label": "Value", "required": true, "secret": true},
			},
		}}, got["types"])
	})

	t.Run("listed by name, its secrets never", func(t *testing.T) {
		got, _ := call(t, agent, "list_credentials", nil)
		assert.Equal(t, []any{map[string]any{
			"id": "cred-1", "name": "Discord bot", "type": "http.header", "fields": map[string]any{"header": "Authorization"}, "secrets": []any{"value"},
		}}, got["credentials"])
	})

	t.Run("made with the secret the user gave, which never comes back", func(t *testing.T) {
		res, err := agent.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_credential", Arguments: map[string]any{
			"type": "http.header", "name": "Hook auth", "values": map[string]any{"header": "Authorization", "value": secret},
		}})
		require.NoError(t, err)
		require.False(t, res.IsError, "%v", res.Content)

		data, err := json.Marshal(res)
		require.NoError(t, err)
		assert.NotContains(t, string(data), "s3cret", "not in the answer, structured or as text")

		v.mu.Lock()
		defer v.mu.Unlock()

		assert.Equal(t, credentials.Input{Type: "http.header", Name: "Hook auth", Values: map[string]string{"header": "Authorization", "value": secret}}, v.created)
	})

	t.Run("a new secret, its name kept", func(t *testing.T) {
		got, _ := call(t, agent, "update_credential", map[string]any{"id": "cred-1", "values": map[string]any{"value": "Bearer new"}})
		assert.Equal(t, "Discord bot", got["name"])

		v.mu.Lock()
		defer v.mu.Unlock()

		assert.Equal(t, credentials.Change{Name: "Discord bot", Values: map[string]string{"value": "Bearer new"}}, v.changed)
	})

	t.Run("where it goes, and testing it, need the user's leave", func(t *testing.T) {
		_, err := call(t, agent, "update_credential", map[string]any{"id": "cred-1", "values": map[string]any{"header": "X-Elsewhere"}})
		assert.Contains(t, err, "needs the user to let agents and tokens use credentials")

		_, err = call(t, agent, "test_credential", map[string]any{"id": "cred-1"})
		assert.Contains(t, err, "needs the user to let agents and tokens use credentials")

		may := alice
		may.Agents.UseCredentials = true

		got, _ := call(t, withVault(t, may, v, domain.ScopeBuild), "test_credential", map[string]any{"id": "cred-1"})
		assert.Equal(t, "Discord answered 401", got["testError"])
		assert.Equal(t, "2026-10-23T12:00:00Z", got["testedAt"])
	})

	t.Run("delete, where the user lets agents", func(t *testing.T) {
		_, err := call(t, agent, "delete_credential", map[string]any{"id": "cred-1"})
		assert.Contains(t, err, "the user has not let agents and tokens delete")

		may := alice
		may.Agents.Delete = true

		got, _ := call(t, withVault(t, may, v, domain.ScopeBuild), "delete_credential", map[string]any{"id": "cred-1"})
		assert.Equal(t, map[string]any{"deleted": "cred-1"}, got)
	})

	t.Run("an unknown one", func(t *testing.T) {
		_, err := call(t, agent, "update_credential", map[string]any{"id": "cred-9", "values": map[string]any{"value": "x"}})
		assert.Contains(t, err, "list_credentials lists them")
	})
}
