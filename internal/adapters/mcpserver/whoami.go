package mcpserver

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

type whoamiResult struct {
	Email string `json:"email" jsonschema:"the user you act for"`
	Role  string `json:"role" jsonschema:"their role: user or admin"`
	// Via says how the agent came in.
	Via   string `json:"via" jsonschema:"how you signed in: oauth, as an agent the user allowed, or token, with an API token"`
	Agent string `json:"agent,omitempty" jsonschema:"the agent's name, as the user allowed it"`
	// Scopes are what the agent may do, the user's role allowing.
	Scopes []string `json:"scopes" jsonschema:"what you may do: read, build, settings, paper, live, admin"`
}

func whoami(_ context.Context, p auth.Principal, _ struct{}) (whoamiResult, error) {
	out := whoamiResult{Email: p.User.Email, Role: string(p.User.Role), Via: "token", Scopes: scopesOf(p), Agent: p.Agent}

	if p.GrantID != "" {
		out.Via = "oauth"
	}

	return out, nil
}
