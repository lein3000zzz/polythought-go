package mcpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/mcpserver"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/oauth"
)

const publicURL = "https://pt.example"

var (
	alice = domain.User{ID: "u-1", Email: "alice@example.com", Role: domain.RoleUser}
	root  = domain.User{ID: "u-0", Email: "root@example.com", Role: domain.RoleAdmin}
)

type agents struct {
	off    bool
	access map[string]oauth.Access
}

func (a agents) Enabled() bool { return !a.off }

func (a agents) Verify(_ context.Context, token string) (oauth.Access, error) {
	if x, ok := a.access[token]; ok {
		return x, nil
	}

	return oauth.Access{}, domain.ErrUnauthenticated
}

type tokens map[string]any // a principal, or the error

func (t tokens) AuthenticateToken(_ context.Context, token string) (auth.Principal, error) {
	switch x := t[token].(type) {
	case auth.Principal:
		return x, nil
	case error:
		return auth.Principal{}, x
	}

	return auth.Principal{}, domain.ErrUnauthenticated
}

// limiter allows calls until it is told to refuse them.
type limiter struct{ refuse atomic.Bool }

func (l *limiter) Allow(string) bool { return !l.refuse.Load() }

func serve(t *testing.T, a agents, tk tokens) string {
	t.Helper()

	url, _ := serveLimited(t, a, tk)

	return url
}

func serveLimited(t *testing.T, a agents, tk tokens) (string, *limiter) {
	t.Helper()

	l := &limiter{}

	mcp, err := mcpserver.New(mcpserver.Config{PublicURL: publicURL + "/"}, mcpserver.Deps{
		Agents: a, Tokens: tk, Limiter: l, Util: observability.NoopUtility(),
	})
	require.NoError(t, err)

	srv := httptest.NewServer(mcp)
	t.Cleanup(srv.Close)

	return srv.URL, l
}

// bearer sends every request with the token, as an agent does.
type bearer string

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))

	return http.DefaultTransport.RoundTrip(r)
}

// whoami connects with the token, as the SDK's client does, and asks who it is.
func whoami(t *testing.T, url, token string) map[string]any {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)

	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: url, HTTPClient: &http.Client{Transport: bearer(token)},
	}, nil)
	require.NoError(t, err)

	defer func() { _ = session.Close() }()

	assert.Equal(t, "polythought", session.InitializeResult().ServerInfo.Name)

	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 1)
	assert.Equal(t, "whoami", tools.Tools[0].Name)
	assert.True(t, tools.Tools[0].Annotations.ReadOnlyHint)

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
	require.NoError(t, err)
	require.False(t, res.IsError)

	b, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(b, &out))

	return out
}

func TestWhoami(t *testing.T) {
	url := serve(t, agents{access: map[string]oauth.Access{
		"poa_agent": {
			Principal: auth.Principal{User: alice, GrantID: "g-1", Agent: "Claude Code", Scopes: []domain.Scope{domain.ScopeRead, domain.ScopeBuild}},
			Client:    domain.OAuthClient{Name: "Claude Code"},
			ExpiresAt: time.Now().Add(time.Hour),
		},
	}}, tokens{
		"pt_scoped": auth.Principal{User: alice, TokenID: "t-1", Scopes: []domain.Scope{domain.ScopePaper}},
		"pt_before": auth.Principal{User: alice, TokenID: "t-2"},
		"pt_admin":  auth.Principal{User: root, TokenID: "t-3"},
	})

	for token, want := range map[string]map[string]any{
		"poa_agent": {"email": "alice@example.com", "role": "user", "via": "oauth", "agent": "Claude Code", "scopes": []any{"read", "build"}},
		// Every scope brings read (D399).
		"pt_scoped": {"email": "alice@example.com", "role": "user", "via": "token", "scopes": []any{"read", "paper"}},
		// A token made before scopes has every scope its user's role allows.
		"pt_before": {"email": "alice@example.com", "role": "user", "via": "token", "scopes": []any{"read", "build", "tasks", "settings", "paper", "live"}},
		"pt_admin":  {"email": "root@example.com", "role": "admin", "via": "token", "scopes": []any{"read", "build", "tasks", "settings", "paper", "live", "admin"}},
	} {
		assert.Equal(t, want, whoami(t, url, token), token)
	}
}

// Without a token the agent is told where to sign in (RFC 9728); with a bad one it is
// refused; switched off, there is no /mcp.
func TestChallenge(t *testing.T) {
	url := serve(t, agents{}, tokens{
		"pt_disabled": domain.ErrUserDisabled,
		"pt_broken":   errors.New("database down"),
	})

	post := func(token string) *http.Response {
		req, err := http.NewRequest(http.MethodPost, url, nil)
		require.NoError(t, err)

		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = res.Body.Close() })

		return res
	}

	res := post("")
	assert.Equal(t, http.StatusUnauthorized, res.StatusCode)
	assert.Equal(t, `Bearer resource_metadata="https://pt.example/.well-known/oauth-protected-resource/mcp"`, res.Header.Get("WWW-Authenticate"))

	for _, token := range []string{"poa_unknown", "pt_unknown", "pt_disabled", "sess-token"} {
		res := post(token)
		assert.Equal(t, http.StatusUnauthorized, res.StatusCode, token)
		assert.Contains(t, res.Header.Get("WWW-Authenticate"), "resource_metadata=", token)
	}

	assert.Equal(t, http.StatusInternalServerError, post("pt_broken").StatusCode)

	off := serve(t, agents{off: true}, tokens{"pt_before": auth.Principal{User: alice, TokenID: "t-2"}})
	req, err := http.NewRequest(http.MethodPost, off, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer pt_before")

	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

// A call over the limit is refused as a tool error, which the model reads (D400).
func TestToolCallLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url, l := serveLimited(t, agents{}, tokens{"pt_scoped": auth.Principal{User: alice, TokenID: "t-1", Scopes: []domain.Scope{domain.ScopeRead}}})

	client := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: url, HTTPClient: &http.Client{Transport: bearer("pt_scoped")}}, nil)
	require.NoError(t, err)

	defer func() { _ = session.Close() }()

	l.refuse.Store(true)

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
	require.NoError(t, err, "a tool error, not the protocol's")
	require.True(t, res.IsError)
	require.Len(t, res.Content, 1)
	assert.Contains(t, res.Content[0].(*mcp.TextContent).Text, "too many tool calls")
}

// All of a user's agents and tokens share a limit too, however many they make (A11).
func TestUserCallLimit(t *testing.T) {
	limit := &allowing{n: 1}
	d := deps(alice, &trades{}, []domain.Scope{domain.ScopeRead})
	d.UserLimiter = limit
	agent := connect(t, d)

	_, err := call(t, agent, "whoami", nil)
	require.Empty(t, err)

	_, err = call(t, agent, "whoami", nil)
	assert.Contains(t, err, "too many tool calls from your agents and tokens together")
	assert.Equal(t, []string{"mcp-user:" + string(alice.ID), "mcp-user:" + string(alice.ID)}, limit.keys)
}
