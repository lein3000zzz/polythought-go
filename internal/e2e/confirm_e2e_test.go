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

// A live action a token asks for waits for its user (D406): the kill switch, turned off by
// a token with the live scope, stays on until the user confirms it, at a browser; then it
// goes off as the token asked.
func TestLiveActionWaitsForTheUser(t *testing.T) {
	base := env("E2E_BASE_URL", "http://localhost:3100")
	b := signIn(t, base)

	b.call(http.MethodPut, "/api/v1/me/kill-switch", map[string]any{"on": true}, http.StatusOK, nil)
	defer b.call(http.MethodPut, "/api/v1/me/kill-switch", map[string]any{"on": false}, http.StatusOK, nil)

	var token struct{ Token string }
	b.call(http.MethodPost, "/api/v1/tokens", map[string]any{"name": "e2e live", "scopes": []string{"live"}}, http.StatusCreated, &token)

	asToken := func(method, path, body string) (int, string) {
		req, err := http.NewRequest(method, base+path, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+token.Token)
		req.Header.Set("Content-Type", "application/json")

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		defer func() { _ = res.Body.Close() }()

		var out struct {
			Error struct{ Code, Message string }
		}
		_ = json.NewDecoder(res.Body).Decode(&out)

		return res.StatusCode, out.Error.Code
	}

	status, code := asToken(http.MethodPut, "/api/v1/me/kill-switch", `{"on": false}`)
	require.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "awaits_confirmation", code)

	type confirmation struct{ ID, Kind, Asker, Status, Result string }

	var list struct{ Confirmations []confirmation }
	b.call(http.MethodGet, "/api/v1/me/confirmations", nil, http.StatusOK, &list)
	require.NotEmpty(t, list.Confirmations)

	waiting := list.Confirmations[0]
	assert.Equal(t, confirmation{ID: waiting.ID, Kind: "kill_off", Asker: "the API token “e2e live”", Status: "pending"}, waiting, "by its name (D408)")

	var me struct{ LiveStopped bool }
	b.call(http.MethodGet, "/api/v1/me", nil, http.StatusOK, &me)
	assert.True(t, me.LiveStopped, "still on, meanwhile")

	status, _ = asToken(http.MethodPost, "/api/v1/me/confirmations/"+waiting.ID+"/confirm", "")
	assert.Equal(t, http.StatusForbidden, status, "the token never confirms itself")

	b.call(http.MethodPost, "/api/v1/me/confirmations/"+waiting.ID+"/confirm", nil, http.StatusOK, nil)

	require.Eventually(t, func() bool {
		b.call(http.MethodGet, "/api/v1/me/confirmations", nil, http.StatusOK, &list)
		return list.Confirmations[0].Status == "done"
	}, 10*time.Second, 100*time.Millisecond)
	assert.Equal(t, "kill switch off", list.Confirmations[0].Result)

	b.call(http.MethodGet, "/api/v1/me", nil, http.StatusOK, &me)
	assert.False(t, me.LiveStopped, "off, as the token asked")

	b.call(http.MethodPost, "/api/v1/me/confirmations/"+waiting.ID+"/refuse", nil, http.StatusConflict, nil)
}

// Asked in the agent's window, where the user lets it (D409): the client's yes there
// confirms the action, and the call carries it out, answering as if it had gone ahead.
func TestAgentWindowConfirms(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	base := env("E2E_BASE_URL", "http://localhost:3100")
	b := signIn(t, base)

	b.call(http.MethodPut, "/api/v1/me/kill-switch", map[string]any{"on": true}, http.StatusOK, nil)
	defer b.call(http.MethodPut, "/api/v1/me/kill-switch", map[string]any{"on": false}, http.StatusOK, nil)

	b.call(http.MethodPut, "/api/v1/me/live-confirm", map[string]any{"inAgent": true}, http.StatusNoContent, nil)
	defer b.call(http.MethodPut, "/api/v1/me/live-confirm", map[string]any{"inAgent": false}, http.StatusNoContent, nil)

	var token struct{ Token string }
	b.call(http.MethodPost, "/api/v1/tokens", map[string]any{"name": "e2e window", "scopes": []string{"live"}}, http.StatusCreated, &token)

	var asked []string

	client := mcp.NewClient(&mcp.Implementation{Name: "go-sdk-e2e-window", Version: "1"}, &mcp.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			asked = append(asked, req.Params.Message)
			return &mcp.ElicitResult{Action: "accept"}, nil
		},
	})

	agent, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: base + "/mcp", HTTPClient: &http.Client{Transport: bearer(token.Token)},
	}, nil)
	require.NoError(t, err)

	defer func() { _ = agent.Close() }()

	off := tool(ctx, t, agent, "set_kill_switch", map[string]any{"on": false})
	assert.Equal(t, false, off["on"], "carried out once confirmed in the window")
	require.Len(t, asked, 1)
	assert.Contains(t, asked[0], "Turn your kill switch off")

	var list struct {
		Confirmations []struct{ Kind, Status, DecidedVia string }
	}
	b.call(http.MethodGet, "/api/v1/me/confirmations", nil, http.StatusOK, &list)
	require.NotEmpty(t, list.Confirmations)
	assert.Equal(t, struct{ Kind, Status, DecidedVia string }{"kill_off", "done", "agent"}, list.Confirmations[0])

	var me struct{ LiveStopped bool }
	b.call(http.MethodGet, "/api/v1/me", nil, http.StatusOK, &me)
	assert.False(t, me.LiveStopped)
}

// An admin's token making a user an admin waits for the admin (D410), whom it is about
// theirs to see; confirmed, it is carried out.
func TestAdminWorkWaitsForTheAdmin(t *testing.T) {
	base := env("E2E_BASE_URL", "http://localhost:3100")
	b := signIn(t, base)

	email := "promoted-" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "") + "@e2e.test"

	var target struct{ ID string }
	b.call(http.MethodPost, "/api/v1/admin/users", map[string]any{"email": email, "password": "e2e-promoted-password"}, http.StatusCreated, &target)
	defer b.call(http.MethodPatch, "/api/v1/admin/users/"+target.ID, map[string]any{"role": "user"}, http.StatusOK, nil)

	var token struct{ Token string }
	b.call(http.MethodPost, "/api/v1/tokens", map[string]any{"name": "e2e admin", "scopes": []string{"admin"}}, http.StatusCreated, &token)

	req, err := http.NewRequest(http.MethodPatch, base+"/api/v1/admin/users/"+target.ID, strings.NewReader(`{"role": "admin"}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusConflict, res.StatusCode, "it waits")

	var list struct {
		Confirmations []struct{ ID, Kind, What, About, Status string }
	}
	b.call(http.MethodGet, "/api/v1/me/confirmations", nil, http.StatusOK, &list)
	require.NotEmpty(t, list.Confirmations)

	c := list.Confirmations[0]
	assert.Equal(t, "admin_role", c.Kind)
	assert.Equal(t, email, c.About, "the admin sees whom")
	assert.NotContains(t, c.What, email, "the token does not")

	b.call(http.MethodPost, "/api/v1/me/confirmations/"+c.ID+"/confirm", nil, http.StatusOK, nil)

	require.Eventually(t, func() bool {
		b.call(http.MethodGet, "/api/v1/me/confirmations", nil, http.StatusOK, &list)
		return list.Confirmations[0].Status == "done"
	}, 10*time.Second, 100*time.Millisecond)

	var users struct {
		Users []struct{ ID, Role string }
	}
	b.call(http.MethodGet, "/api/v1/admin/users?limit=200", nil, http.StatusOK, &users)

	for _, u := range users.Users {
		if u.ID == target.ID {
			assert.Equal(t, "admin", u.Role)
		}
	}
}
