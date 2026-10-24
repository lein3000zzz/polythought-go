package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

// sessions are alice's agent's session open, and a token's from before a restart.
type sessions struct{}

func (sessions) Connected(_ context.Context, owner domain.UserID) ([]ports.ConnectedSession, error) {
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	return []ports.ConnectedSession{
		{MCPSession: ports.MCPSession{ID: "s-1", UserID: owner, Holder: "grant:g-1", Name: "Claude Code", Client: "claude-code 2.1.300", OpenedAt: at, SeenAt: at.Add(time.Minute)}},
		{MCPSession: ports.MCPSession{ID: "s-0", UserID: owner, Holder: "token:t-1", Name: "laptop", OpenedAt: at, SeenAt: at}, BeforeRestart: true},
	}, nil
}

func setupServer(t *testing.T, setup httpapi.AgentSetup) (*httpapi.Server, *httpapimocks.MockAuthService) {
	t.Helper()

	authSvc := httpapimocks.NewMockAuthService(t)

	srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{
		Auth: authSvc, AgentSetup: func() httpapi.AgentSetup { return setup }, AgentSessions: sessions{},
	})
	require.NoError(t, err)

	return srv, authSvc
}

// How to connect an agent, and the agents connected now (D414).
func TestAgentSetupRoutes(t *testing.T) {
	t.Run("the server's address unless an admin set another", func(t *testing.T) {
		srv, authSvc := setupServer(t, httpapi.AgentSetup{Enabled: true, Clients: []string{"claude-code", "other"}})
		withSession(authSvc, alice)

		w := request{method: http.MethodGet, path: "/api/v1/me/agent-setup", headers: cookie}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{
			"enabled": true, "address": "`+publicURL+`/mcp", "clients": ["claude-code", "other"],
			"metadata": "`+publicURL+`/.well-known/oauth-protected-resource/mcp"
		}`, w.Body.String())

		srv, authSvc = setupServer(t, httpapi.AgentSetup{Address: "https://poly.tailnet.example/mcp"})
		withSession(authSvc, alice)

		w = request{method: http.MethodGet, path: "/api/v1/me/agent-setup", headers: cookie}.do(t, srv)
		assert.Contains(t, w.Body.String(), `"address":"https://poly.tailnet.example/mcp"`)
		assert.Contains(t, w.Body.String(), `"clients":[]`)
	})

	t.Run("the sessions open, and from before a restart", func(t *testing.T) {
		srv, authSvc := setupServer(t, httpapi.AgentSetup{Enabled: true})
		withSession(authSvc, alice)

		w := request{method: http.MethodGet, path: "/api/v1/me/agent-sessions", headers: cookie}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"sessions": [
			{"kind": "agent", "name": "Claude Code", "client": "claude-code 2.1.300", "openedAt": "2026-10-24T12:00:00Z", "seenAt": "2026-10-24T12:01:00Z", "beforeRestart": false},
			{"kind": "token", "name": "laptop", "client": "", "openedAt": "2026-10-24T12:00:00Z", "seenAt": "2026-10-24T12:00:00Z", "beforeRestart": true}
		]}`, w.Body.String())
	})
}
