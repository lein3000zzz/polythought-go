//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

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
