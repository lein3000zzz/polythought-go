//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An admin's agent over MCP (D410, D411): users without their emails, an invite as a link
// where the instance mails none, and a Config change that waits for the admin, carried out
// once they confirm it; a secret setting is never the agent's.
func TestAdminAgentOverMCP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	base := env("E2E_BASE_URL", "http://localhost:3100")
	b := signIn(t, base)

	var token struct{ Token string }
	b.call(http.MethodPost, "/api/v1/tokens", map[string]any{"name": "e2e admin agent", "scopes": []string{"admin"}}, http.StatusCreated, &token)

	agent, err := mcp.NewClient(&mcp.Implementation{Name: "go-sdk-e2e-admin", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: base + "/mcp", HTTPClient: &http.Client{Transport: bearer(token.Token)},
	}, nil)
	require.NoError(t, err)

	defer func() { _ = agent.Close() }()

	users := tool(ctx, t, agent, "list_users", nil)
	require.NotEmpty(t, users["users"])
	data, _ := json.Marshal(users)
	assert.NotContains(t, string(data), "@", "no user's email")

	found := tool(ctx, t, agent, "find_user", map[string]any{"email": env("E2E_ADMIN_EMAIL", "admin@e2e.test")})
	assert.Equal(t, "admin", found["role"])

	invite := tool(ctx, t, agent, "invite", nil)
	assert.Contains(t, invite["link"], "/register?invite=", "a link to pass on")
	tool(ctx, t, agent, "revoke_invite", map[string]any{"id": invite["id"]})

	failed := func(name string, args map[string]any) string {
		t.Helper()

		res, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		require.NoError(t, err)
		require.True(t, res.IsError, name)

		return res.Content[0].(*mcp.TextContent).Text
	}

	assert.Contains(t, failed("update_settings", map[string]any{"changes": map[string]any{"telegram.token": "123:abc"}}),
		"is secret: an admin sets it at a browser")

	assert.Contains(t, failed("update_settings", map[string]any{"changes": map[string]any{"mcp.window_wait": "3m"}}),
		"it waits for the user to confirm it")
	defer b.call(http.MethodPatch, "/api/v1/admin/settings", map[string]any{"values": map[string]any{"mcp.window_wait": nil}}, http.StatusOK, nil)

	var list struct {
		Confirmations []struct{ ID, Kind, What, Status string }
	}
	b.call(http.MethodGet, "/api/v1/me/confirmations", nil, http.StatusOK, &list)
	require.NotEmpty(t, list.Confirmations)

	c := list.Confirmations[0]
	assert.Equal(t, "admin_config", c.Kind)
	assert.Equal(t, `Change Config: Agent window waits to "3m"`, c.What)

	b.call(http.MethodPost, "/api/v1/me/confirmations/"+c.ID+"/confirm", nil, http.StatusOK, nil)

	require.Eventually(t, func() bool {
		b.call(http.MethodGet, "/api/v1/me/confirmations", nil, http.StatusOK, &list)
		return list.Confirmations[0].Status == "done"
	}, 10*time.Second, 100*time.Millisecond)

	settings := tool(ctx, t, agent, "list_settings", map[string]any{"group": "Agents over MCP"})

	var wait any
	for _, s := range settings["settings"].([]any) {
		if s.(map[string]any)["key"] == "mcp.window_wait" {
			wait = s.(map[string]any)["value"]
		}
	}

	assert.Equal(t, "3m", wait, "carried out as the agent asked")

	plugins := tool(ctx, t, agent, "list_plugins", nil)
	assert.Contains(t, plugins, "plugins")
	assert.False(t, strings.Contains(failed("install_plugin", map[string]any{}), "internal error"))
}
