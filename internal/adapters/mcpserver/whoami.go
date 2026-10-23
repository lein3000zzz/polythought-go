package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type whoamiResult struct {
	Email string `json:"email" jsonschema:"the user you act for"`
	Role  string `json:"role" jsonschema:"their role: user or admin"`
	// Via says how the agent came in.
	Via   string `json:"via" jsonschema:"how you signed in: oauth, as an agent the user allowed, or token, with an API token"`
	Agent string `json:"agent,omitempty" jsonschema:"the agent's name, as the user allowed it"`
	// Scopes are what the agent may do, the user's role allowing.
	Scopes []string `json:"scopes" jsonschema:"what you may do: read, build, paper, live, admin"`
}

func whoami(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, whoamiResult, error) {
	p, ok := Principal(req)
	if !ok {
		return nil, whoamiResult{}, errors.New("not signed in")
	}

	out := whoamiResult{Email: p.User.Email, Role: string(p.User.Role), Via: "token", Scopes: scopesOf(p)}

	if p.GrantID != "" {
		out.Via = "oauth"
		out.Agent, _ = req.Extra.TokenInfo.Extra["agent"].(string)
	}

	return nil, out, nil
}
