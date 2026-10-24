//go:build e2e

// Package e2e drives the stack `make e2e` starts, as an agent does: the official Go SDK's
// client signs in with OAuth and calls /mcp (D398). A fake browser stands in for the user
// on the consent page, through the API the page calls.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const callback = "http://127.0.0.1:53999/callback" // never listened on: the fake browser reads the code

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}

// browser is the user's, signed in as the admin.
type browser struct {
	t      *testing.T
	base   string
	client *http.Client
}

func signIn(t *testing.T, base string) *browser {
	t.Helper()

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)

	b := &browser{t: t, base: base, client: &http.Client{Jar: jar, Timeout: 10 * time.Second}}

	b.call(http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": env("E2E_ADMIN_EMAIL", "admin@e2e.test"), "password": env("E2E_ADMIN_PASSWORD", "e2e-admin-password"),
	}, http.StatusOK, nil)

	return b
}

// call sends JSON as the app does, from its own origin, and reads the answer into out.
func (b *browser) call(method, path string, body any, status int, out any) {
	b.t.Helper()

	var r *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(b.t, err)
		r = bytes.NewReader(data)
	} else {
		r = bytes.NewReader(nil)
	}

	req, err := http.NewRequest(method, b.base+path, r)
	require.NoError(b.t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", b.base)

	res, err := b.client.Do(req)
	require.NoError(b.t, err)

	defer func() { _ = res.Body.Close() }()

	require.Equal(b.t, status, res.StatusCode, "%s %s", method, path)

	if out != nil {
		require.NoError(b.t, json.NewDecoder(res.Body).Decode(out))
	}
}

// consent is the consent page: it reads the request from the URL the agent opened, shows
// what is offered, and allows what is ticked, as a user who changes nothing does.
func (b *browser) consent(_ context.Context, args *sdkauth.AuthorizationArgs) (*sdkauth.AuthorizationResult, error) {
	b.t.Helper()

	u, err := url.Parse(args.URL)
	require.NoError(b.t, err)
	require.Equal(b.t, "/oauth/authorize", u.Path, "the agent opens the consent page")

	q := u.Query()
	request := map[string]string{}

	for _, k := range []string{"response_type", "client_id", "redirect_uri", "code_challenge", "code_challenge_method", "scope", "state", "resource"} {
		if q.Has(k) {
			request[k] = q.Get(k)
		}
	}

	var consent struct {
		Client  struct{ Name string }
		Offered []struct {
			Scope  string
			Ticked bool
		}
		Allowed bool
	}
	b.call(http.MethodPost, "/api/v1/oauth/authorization", request, http.StatusOK, &consent)

	assert.Equal(b.t, "Go SDK e2e", consent.Client.Name)

	var ticked []string

	for _, o := range consent.Offered {
		if o.Ticked {
			ticked = append(ticked, o.Scope)
		}
	}

	assert.Equal(b.t, []string{"read", "build", "paper"}, ticked, "live and admin unticked, though the agent asks for all")

	var decision struct{ Redirect string }
	b.call(http.MethodPost, "/api/v1/oauth/authorization/approve", map[string]any{"request": request, "scopes": ticked}, http.StatusOK, &decision)

	back, err := url.Parse(decision.Redirect)
	require.NoError(b.t, err)
	require.True(b.t, strings.HasPrefix(decision.Redirect, callback), decision.Redirect)

	return &sdkauth.AuthorizationResult{
		Code: back.Query().Get("code"), State: back.Query().Get("state"), Iss: back.Query().Get("iss"),
	}, nil
}

// bearer sends every request with an API token.
type bearer string

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))

	return http.DefaultTransport.RoundTrip(r)
}

func whoami(ctx context.Context, t *testing.T, session *mcp.ClientSession) map[string]any {
	t.Helper()

	return tool(ctx, t, session, "whoami", nil)
}

// tool calls a tool and gives its result as JSON reads it.
func tool(ctx context.Context, t *testing.T, session *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err, name)
	require.False(t, res.IsError, "%s: %v", name, res.Content)

	data, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(data, &out))

	return out
}

func TestAgentSignsInOverMCP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	base := env("E2E_BASE_URL", "http://localhost:3100")
	admin := env("E2E_ADMIN_EMAIL", "admin@e2e.test")

	// Without a token, /mcp says where to sign in, through Caddy.
	res, err := http.Post(base+"/mcp", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	_ = res.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, res.StatusCode)
	assert.Equal(t, `Bearer resource_metadata="`+base+`/.well-known/oauth-protected-resource/mcp"`, res.Header.Get("WWW-Authenticate"))

	b := signIn(t, base)

	// The SDK's client finds the server, registers itself, sends the user to consent,
	// exchanges the code with its PKCE verifier, and connects.
	handler, err := sdkauth.NewAuthorizationCodeHandler(&sdkauth.AuthorizationCodeHandlerConfig{
		DynamicClientRegistrationConfig: &sdkauth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
			ClientName: "Go SDK e2e", RedirectURIs: []string{callback}, TokenEndpointAuthMethod: "none",
			GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"},
		}},
		RedirectURL:              callback,
		AuthorizationCodeFetcher: b.consent,
	})
	require.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "go-sdk-e2e", Version: "1"}, nil)

	agent, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp", OAuthHandler: handler}, nil)
	require.NoError(t, err)

	defer func() { _ = agent.Close() }()

	assert.Equal(t, map[string]any{
		"email": admin, "role": "admin", "via": "oauth", "agent": "Go SDK e2e", "scopes": []any{"read", "build", "paper"},
	}, whoami(ctx, t, agent))

	// It reads what the user builds (D401): a workflow, as the editor's Code view has it, its
	// webhook's URL hidden.
	var made struct{ ID string }
	b.call(http.MethodPost, "/api/v1/workflows", map[string]any{
		"name": "Agent reads me",
		"nodes": []map[string]any{
			{"id": "manual", "type": "trigger.manual", "position": map[string]any{"x": 0, "y": 0}},
			{"id": "hook", "type": "output.webhook", "params": map[string]any{"url": "https://hooks.example/secret-path"}, "position": map[string]any{"x": 300, "y": 0}},
		},
		"connections": []map[string]any{{"from": "manual", "fromPort": "main", "to": "hook", "toPort": "main"}},
	}, http.StatusCreated, &made)

	code := tool(ctx, t, agent, "get_workflow", map[string]any{"id": made.ID, "format": "typescript"})
	assert.Contains(t, code["code"], `const manual = w.trigger("manual", "trigger.manual")`)
	assert.Contains(t, code["code"], `$personal: "hidden"`)
	assert.NotContains(t, code["code"], "secret-path")
	assert.Equal(t, []any{"hook.url"}, code["hidden"])

	types := tool(ctx, t, agent, "get_node_type", map[string]any{"name": "output.webhook"})
	assert.Equal(t, "output.webhook", types["name"])

	sdk := tool(ctx, t, agent, "get_code_sdk", nil)
	assert.NotEmpty(t, sdk["files"])

	// And how it trades (D402): on paper, the kill switch off.
	tr := tool(ctx, t, agent, "get_trading", map[string]any{"workflow": made.ID})
	assert.Equal(t, "paper", tr["setup"].(map[string]any)["mode"])
	assert.Equal(t, false, tr["killSwitch"])

	wallets := tool(ctx, t, agent, "list_wallets", nil)
	assert.NotNil(t, wallets["wallets"])

	// And trades (D406): on paper at once, the paper account reset; the kill switch on with
	// any scope, but off only with live.
	assert.Equal(t, "paper", tool(ctx, t, agent, "set_trading", map[string]any{"workflow": made.ID, "mode": "paper"})["mode"])
	assert.Equal(t, "1000", tool(ctx, t, agent, "reset_paper_account", map[string]any{"workflow": made.ID})["cash"])

	assert.Equal(t, true, tool(ctx, t, agent, "set_kill_switch", map[string]any{"on": true})["on"])

	off, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: "set_kill_switch", Arguments: map[string]any{"on": false}})
	require.NoError(t, err)
	require.True(t, off.IsError)
	assert.Contains(t, off.Content[0].(*mcp.TextContent).Text, "it needs the live scope")

	b.call(http.MethodPut, "/api/v1/me/kill-switch", map[string]any{"on": false}, http.StatusOK, nil)

	// It builds (D403): a workflow from code, then changed on the version it read with a
	// node more, which goes beside the one feeding it; run by hand; deleted once the user
	// lets agents delete.
	built := tool(ctx, t, agent, "create_workflow", map[string]any{"code": `import { workflow } from "@polythought/workflow"

export default workflow("Agent builds me", (w) => {
  const manual = w.trigger("manual", "trigger.manual")
  const log = w.node("log", "output.log", { message: "hello" })

  manual.to(log)
})
`})
	assert.Equal(t, "Agent builds me", built["name"])
	assert.Equal(t, false, built["active"])

	id := built["id"].(string)

	var placed struct {
		Nodes []struct {
			ID       string
			Position struct{ X, Y float64 }
		}
	}
	b.call(http.MethodGet, "/api/v1/workflows/"+id, nil, http.StatusOK, &placed)
	require.Len(t, placed.Nodes, 2)
	assert.Equal(t, 300.0, placed.Nodes[1].Position.X-placed.Nodes[0].Position.X, "laid out as the canvas would")

	changed := tool(ctx, t, agent, "update_workflow", map[string]any{"id": id, "version": built["version"], "workflow": map[string]any{
		"nodes": []any{
			map[string]any{"id": "manual", "type": "trigger.manual"},
			map[string]any{"id": "log", "type": "output.log", "params": map[string]any{"message": "hello"}},
			map[string]any{"id": "again", "type": "output.log", "params": map[string]any{"message": "again"}},
		},
		"connections": []any{
			map[string]any{"from": "manual", "fromPort": "main", "to": "log", "toPort": "main"},
			map[string]any{"from": "log", "fromPort": "main", "to": "again", "toPort": "main"},
		},
	}})
	assert.Equal(t, 2.0, changed["version"])

	b.call(http.MethodGet, "/api/v1/workflows/"+id, nil, http.StatusOK, &placed)
	require.Len(t, placed.Nodes, 3)
	assert.Equal(t, placed.Nodes[1].Position.X+300, placed.Nodes[2].Position.X, "after the node feeding it")

	stale, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: "update_workflow", Arguments: map[string]any{
		"id": id, "version": 1, "workflow": map[string]any{"nodes": []any{map[string]any{"id": "manual", "type": "trigger.manual"}}},
	}})
	require.NoError(t, err)
	require.True(t, stale.IsError)
	assert.Contains(t, stale.Content[0].(*mcp.TextContent).Text, "changed since version 1", "the version it read is old")

	ran := tool(ctx, t, agent, "run_workflow", map[string]any{"id": id})
	assert.Equal(t, "succeeded", ran["status"])
	assert.Len(t, ran["nodes"], 3)

	refused, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: "delete_workflow", Arguments: map[string]any{"id": id}})
	require.NoError(t, err)
	require.True(t, refused.IsError)
	assert.Contains(t, refused.Content[0].(*mcp.TextContent).Text, "has not let agents and tokens delete")

	b.call(http.MethodPut, "/api/v1/me/agent-access", map[string]any{"delete": true}, http.StatusNoContent, nil)
	defer b.call(http.MethodPut, "/api/v1/me/agent-access", map[string]any{"delete": false}, http.StatusNoContent, nil)

	assert.Equal(t, map[string]any{"deleted": id}, tool(ctx, t, agent, "delete_workflow", map[string]any{"id": id}))
	b.call(http.MethodGet, "/api/v1/workflows/"+id, nil, http.StatusNotFound, nil)

	// Its custom nodes and credentials (D404): a node made from its form and code; a
	// credential kept with the secret the user gave, which never comes back, used in a
	// workflow once the user lets agents use credentials.
	node := tool(ctx, t, agent, "create_custom_node", map[string]any{
		"name": "e2e-pass", "label": "E2E pass", "outputs": []any{"main"}, "items": map[string]any{"passed": true},
		"source": "export function run(items: unknown[]) { return items }",
	})
	assert.Equal(t, "custom.e2e-pass", node["type"])
	assert.Equal(t, map[string]any{"deleted": node["id"]}, tool(ctx, t, agent, "delete_custom_node", map[string]any{"id": node["id"]}))

	kept := tool(ctx, t, agent, "create_credential", map[string]any{
		"type": "http.header", "name": "E2E hook", "values": map[string]any{"header": "Authorization", "value": "Bearer e2e-secret"},
	})
	assert.Equal(t, []any{"value"}, kept["secrets"])

	listed, err := json.Marshal(tool(ctx, t, agent, "list_credentials", nil))
	require.NoError(t, err)
	assert.Contains(t, string(listed), "E2E hook")
	assert.NotContains(t, string(listed), "e2e-secret")

	uses := map[string]any{"name": "Agent uses a credential", "workflow": map[string]any{
		"nodes": []any{
			map[string]any{"id": "manual", "type": "trigger.manual"},
			map[string]any{"id": "hook", "type": "output.webhook", "params": map[string]any{"url": "https://hooks.example/e2e", "credential": kept["id"]}},
		},
		"connections": []any{map[string]any{"from": "manual", "fromPort": "main", "to": "hook", "toPort": "main"}},
	}}

	refused, err = agent.CallTool(ctx, &mcp.CallToolParams{Name: "create_workflow", Arguments: uses})
	require.NoError(t, err)
	require.True(t, refused.IsError)
	assert.Contains(t, refused.Content[0].(*mcp.TextContent).Text, `node "hook" uses a credential`)

	b.call(http.MethodPut, "/api/v1/me/agent-access", map[string]any{"useCredentials": true}, http.StatusNoContent, nil)
	defer b.call(http.MethodPut, "/api/v1/me/agent-access", map[string]any{"useCredentials": false}, http.StatusNoContent, nil)

	hooked := tool(ctx, t, agent, "create_workflow", uses)
	tool(ctx, t, agent, "delete_workflow", map[string]any{"id": hooked["id"]})
	tool(ctx, t, agent, "delete_credential", map[string]any{"id": kept["id"]})

	// The library (D405): a built-in entry used, nothing of another's in it; the workflow
	// shared, and taken down, once the user lets agents share.
	used := tool(ctx, t, agent, "use_template", map[string]any{"id": "first-workflow", "name": "Agent's first"})
	assert.Equal(t, "Agent's first", used["name"])
	assert.NotContains(t, used, "brings")

	refused, err = agent.CallTool(ctx, &mcp.CallToolParams{Name: "share_workflow", Arguments: map[string]any{"workflow": used["id"]}})
	require.NoError(t, err)
	require.True(t, refused.IsError)
	assert.Contains(t, refused.Content[0].(*mcp.TextContent).Text, "has not let agents and tokens share")

	b.call(http.MethodPut, "/api/v1/me/agent-access", map[string]any{"share": true}, http.StatusNoContent, nil)
	defer b.call(http.MethodPut, "/api/v1/me/agent-access", map[string]any{"share": false}, http.StatusNoContent, nil)

	entry := tool(ctx, t, agent, "share_workflow", map[string]any{"workflow": used["id"], "summary": "Shared by an agent."})
	assert.Equal(t, true, entry["mine"])
	assert.Equal(t, map[string]any{"deleted": entry["id"]}, tool(ctx, t, agent, "unshare_template", map[string]any{"id": entry["id"]}))
	tool(ctx, t, agent, "delete_workflow", map[string]any{"id": used["id"]})

	// An API token with the read scope alone works there too, with that scope.
	var token struct{ Token string }
	b.call(http.MethodPost, "/api/v1/tokens", map[string]any{"name": "mcp e2e", "scopes": []string{"read"}}, http.StatusCreated, &token)

	scripted, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: base + "/mcp", HTTPClient: &http.Client{Transport: bearer(token.Token)},
	}, nil)
	require.NoError(t, err)

	defer func() { _ = scripted.Close() }()

	assert.Equal(t, map[string]any{"email": admin, "role": "admin", "via": "token", "scopes": []any{"read"}}, whoami(ctx, t, scripted))

	// Revoked on Settings, the agent's token stops at once.
	source, err := handler.TokenSource(ctx)
	require.NoError(t, err)
	access, err := source.Token()
	require.NoError(t, err)

	var grants struct {
		Grants []struct {
			ID     string
			Client struct{ Name string }
		}
	}
	b.call(http.MethodGet, "/api/v1/me/grants", nil, http.StatusOK, &grants)

	for _, g := range grants.Grants {
		if g.Client.Name == "Go SDK e2e" {
			b.call(http.MethodDelete, "/api/v1/me/grants/"+g.ID, nil, http.StatusNoContent, nil)
		}
	}

	req, err := http.NewRequest(http.MethodPost, base+"/mcp", strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+access.AccessToken)

	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, res.StatusCode, "revoked")
}
