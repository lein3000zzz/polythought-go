package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// AgentSetup is what Connected agents gives for connecting an agent (D414): whether agents
// are on here, the address they connect at, and the clients it has steps for.
type AgentSetup struct {
	Enabled bool
	Address string
	Clients []string
}

// AgentSessions is the user's agents' sessions open on /mcp (D414): the MCP server.
type AgentSessions interface {
	Connected(ctx context.Context, owner domain.UserID) ([]ports.ConnectedSession, error)
}

type agentSetupDTO struct {
	// Enabled says agents may connect here; off, an admin turned Agents over MCP off.
	Enabled bool `json:"enabled"`
	// Address is where agents connect, such as https://polythought.example.com/mcp.
	Address string `json:"address"`
	// Metadata is where an agent finds how to sign in (RFC 9728).
	Metadata string `json:"metadata"`
	// Clients are those the steps are for, a tab each, in order.
	Clients []string `json:"clients" enums:"claude-code,codex,gemini-cli,cursor,opencode,vscode,claude-ai,other"`
} // @name AgentSetup

type agentSessionDTO struct {
	// Kind is agent, signed in with OAuth, or token, an API token.
	Kind string `json:"kind" enums:"agent,token"`
	// Name is the agent's or the token's, as it was when the session opened.
	Name string `json:"name"`
	// Client is its client as it said, with its version, like claude-code 2.1.300.
	Client   string    `json:"client"`
	OpenedAt time.Time `json:"openedAt" format:"date-time"`
	SeenAt   time.Time `json:"seenAt" format:"date-time"`
	// BeforeRestart says it was open as the server restarted, and its agent has not come
	// back since; it opens a new one when it calls again.
	BeforeRestart bool `json:"beforeRestart"`
} // @name AgentSession

type agentSessionList struct {
	Sessions []agentSessionDTO `json:"sessions"`
} // @name AgentSessionList

// getAgentSetup is how to connect an agent here (D414).
//
//	@Summary	How to connect an agent
//	@ID			getAgentSetup
//	@Tags		oauth
//	@Success	200	{object}	agentSetupDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/me/agent-setup [get]
func (s *Server) getAgentSetup(_ context.Context, c *app.RequestContext) {
	var setup AgentSetup
	if s.agentSetup != nil {
		setup = s.agentSetup()
	}

	address := setup.Address
	if address == "" {
		address = strings.TrimRight(s.publicURL, "/") + "/mcp"
	}

	clients := setup.Clients
	if clients == nil {
		clients = []string{}
	}

	c.JSON(http.StatusOK, agentSetupDTO{
		Enabled: setup.Enabled, Address: address, Clients: clients,
		Metadata: strings.TrimRight(s.publicURL, "/") + "/.well-known/oauth-protected-resource/mcp",
	})
}

// listAgentSessions is the user's agents and tokens connected now: each session open on
// /mcp, and those from before a restart whose agents have not come back yet (D414).
//
//	@Summary	Your agents connected now
//	@ID			listAgentSessions
//	@Tags		oauth
//	@Success	200	{object}	agentSessionList
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/me/agent-sessions [get]
func (s *Server) listAgentSessions(ctx context.Context, c *app.RequestContext) {
	out := agentSessionList{Sessions: []agentSessionDTO{}}

	if s.agentSessions != nil {
		p, _ := principal(c)

		list, err := s.agentSessions.Connected(ctx, p.User.ID)
		if err != nil {
			s.fail(ctx, c, err)
			return
		}

		for _, m := range list {
			kind := "agent"
			if strings.HasPrefix(m.Holder, "token:") {
				kind = "token"
			}

			out.Sessions = append(out.Sessions, agentSessionDTO{
				Kind: kind, Name: m.Name, Client: m.Client, OpenedAt: m.OpenedAt, SeenAt: m.SeenAt, BeforeRestart: m.BeforeRestart,
			})
		}
	}

	c.JSON(http.StatusOK, out)
}
