// Package mcpserver is Polythought's MCP endpoint (D381, D398): the official Go SDK's
// Streamable HTTP server at /mcp, for agents signed in with OAuth and for API tokens,
// each request's bearer token checked as it comes. 7b gives it its tools; until then it
// has whoami.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/ai"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/oauth"
)

const (
	// A session no request came for in this long is closed; its client starts another.
	sessionIdle = time.Hour
	maxBody     = 1 << 20 // as the API's
)

// Agents checks agents' OAuth access tokens: the oauth service.
type Agents interface {
	Enabled() bool
	Verify(ctx context.Context, token string) (oauth.Access, error)
}

// Tokens checks API tokens: the auth service.
type Tokens interface {
	AuthenticateToken(ctx context.Context, token string) (auth.Principal, error)
}

const tracerName = "github.com/lein3000zzz/polythought-go/internal/adapters/mcpserver"

type Config struct {
	// PublicURL is the server's, which the 401 challenge points to the metadata at.
	PublicURL string
}

// Deps are what the server needs.
type Deps struct {
	Agents Agents
	Tokens Tokens
	// Limiter counts each agent's or token's tool calls (D400).
	Limiter ports.RateLimiter
	Util    *observability.Utility

	// What the tools read (D401); a tool whose source is missing is not offered.
	Workflows Workflows
	NodeTypes NodeTypes
	CodeTypes CodeTypes
	Library   Library
	Trading   Trading

	// What the build tools change (D403); without them they are not offered.
	Edit Editing
	Code WorkflowCode

	// What the market tools read (D402); without a reader they are not offered.
	Markets Markets
	// What the money tools read (D402).
	Wallets Wallets
	Trades  Trades
}

type Server struct {
	agents  Agents
	tokens  Tokens
	limiter ports.RateLimiter
	log     logging.Logger

	workflows Workflows
	nodeTypes NodeTypes
	codeTypes CodeTypes
	library   Library
	trading   Trading
	edit      Editing
	code      WorkflowCode

	markets      ai.Sources
	marketReader ports.MarketReader
	wallets      Wallets
	trades       Trades

	kit     *redkit.Kit
	scope   tracing.Scope
	handler http.Handler
}

func New(cfg Config, d Deps) (*Server, error) {
	log := d.Util.Logger.With("component", "mcp")

	kit, err := redkit.Register(redkit.Params{
		Manager: d.Util.MetricsManager, Logger: log, Subsystem: "mcp", Buckets: redkit.MediumBuckets, Component: "mcp tool call",
	})
	if err != nil {
		return nil, fmt.Errorf("mcpserver: register metrics: %w", err)
	}

	s := &Server{
		agents: d.Agents, tokens: d.Tokens, limiter: d.Limiter, log: log, kit: kit,
		workflows: d.Workflows, nodeTypes: d.NodeTypes, codeTypes: d.CodeTypes, library: d.Library, trading: d.Trading,
		edit: d.Edit, code: d.Code,
		markets: d.Markets.Sources, marketReader: d.Markets.Reader, wallets: d.Wallets, trades: d.Trades,
		scope: tracing.Scope{Manager: d.Util.TracerManager, TracerName: tracerName},
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name: "polythought", Title: "Polythought", Version: version(), WebsiteURL: strings.TrimRight(cfg.PublicURL, "/"),
	}, &mcp.ServerOptions{
		Instructions: "Polythought builds and runs workflows that trade on Polymarket, on paper or live. " +
			"You act for the user who allowed you, with the scopes they gave you; whoami says which. " + Notice,
	})

	add(s, srv, Tool[struct{}, whoamiResult]{
		Name:        "whoami",
		Title:       "Who am I",
		Description: "Who you act for in Polythought, how you signed in, and what you may do: your scopes.",
		ReadOnly:    true,
		Handle:      whoami,
	})

	if s.workflows != nil && s.nodeTypes != nil {
		s.addWorkflowTools(srv)
	}

	if s.workflows != nil && s.edit != nil && s.code != nil {
		s.addEditTools(srv)
	}

	if s.nodeTypes != nil && s.codeTypes != nil && s.library != nil {
		s.addCatalogTools(srv)
	}

	if s.marketReader != nil {
		s.addMarketTools(srv)
	}

	if s.wallets != nil && s.trades != nil {
		s.addMoneyTools(srv)
	}

	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		SessionTimeout:      sessionIdle,
		MaxRequestBodyBytes: maxBody,
		// The protection is for local servers that take no credentials: every request here
		// carries a bearer token, which a page rebound to this host's name would not have.
		// Behind a proxy on the same machine, it would refuse every request.
		DisableLocalhostProtection: true,
	})

	s.handler = sdkauth.RequireBearerToken(s.verify, &sdkauth.RequireBearerTokenOptions{
		ResourceMetadataURL: ResourceMetadataURL(cfg.PublicURL),
		// API tokens may never expire; agents' always do, and are checked as they come.
		AllowMissingExpiration: true,
	})(stream)

	return s, nil
}

// ResourceMetadataURL is where the endpoint's protected resource metadata is (RFC 9728).
func ResourceMetadataURL(publicURL string) string {
	return strings.TrimRight(publicURL, "/") + "/.well-known/oauth-protected-resource/mcp"
}

// ServeHTTP serves /mcp; switched off, it is not there (D398).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.agents.Enabled() {
		http.Error(w, domain.ErrAgentsOff.Error(), http.StatusNotFound)
		return
	}

	s.handler.ServeHTTP(w, r)
}

// verify checks a request's bearer token: an agent's access token, or an API token.
func (s *Server) verify(ctx context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
	var (
		p       auth.Principal
		expires time.Time
		err     error
	)

	switch {
	case strings.HasPrefix(token, "poa_"):
		var a oauth.Access
		a, err = s.agents.Verify(ctx, token)
		p, expires = a.Principal, a.ExpiresAt
	case strings.HasPrefix(token, "pt_"):
		p, err = s.tokens.AuthenticateToken(ctx, token)
	default:
		return nil, fmt.Errorf("%w: not a token of this server", sdkauth.ErrInvalidToken)
	}

	switch {
	case errors.Is(err, domain.ErrUnauthenticated), errors.Is(err, domain.ErrUserDisabled), errors.Is(err, domain.ErrEmailNotVerified):
		return nil, fmt.Errorf("%w: %v", sdkauth.ErrInvalidToken, err)
	case err != nil:
		return nil, err
	}

	return &sdkauth.TokenInfo{
		Scopes:     scopesOf(p),
		Expiration: expires,
		UserID:     string(p.User.ID), // a session stays its user's
		Extra:      map[string]any{principalName: p},
	}, nil
}

const principalName = "principal"

// Principal is who a tool's request acts for, its scopes with it.
func Principal(req *mcp.CallToolRequest) (auth.Principal, bool) {
	if req.Extra == nil || req.Extra.TokenInfo == nil {
		return auth.Principal{}, false
	}

	p, ok := req.Extra.TokenInfo.Extra[principalName].(auth.Principal)

	return p, ok
}

// scopesOf is what the principal may do, its role allowing: an API token made before
// scopes has every scope, admin only for an admin.
func scopesOf(p auth.Principal) []string {
	out := []string{}

	for _, sc := range domain.Scopes {
		if p.Can(sc) && (sc != domain.ScopeAdmin || p.User.IsAdmin()) {
			out = append(out, string(sc))
		}
	}

	return out
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}

	return "dev"
}
