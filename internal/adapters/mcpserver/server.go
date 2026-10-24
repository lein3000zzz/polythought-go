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
	"sync"
	"sync/atomic"
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
	// A session no request came for in this long is closed, unless Deps say otherwise;
	// its client starts another.
	sessionIdle = time.Hour
	maxBody     = 1 << 20 // as the API's
	// maxOpening is the most a request opening a session may send: an initialize, whose
	// client's name and capabilities the session keeps while it lasts (A10).
	maxOpening = 64 << 10
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
	// Limiter counts each agent's or token's tool calls (D400), and UserLimiter all of a
	// user's together (A11); without it, they are not.
	Limiter     ports.RateLimiter
	UserLimiter ports.RateLimiter
	Util        *observability.Utility

	// What the tools read (D401); a tool whose source is missing is not offered.
	Workflows Workflows
	NodeTypes NodeTypes
	CodeTypes CodeTypes
	Library   Library
	Trading   Trading

	// What the build tools change (D403); without them they are not offered.
	Edit Editing
	Code WorkflowCode
	// The user's custom nodes and credentials (D404); without them, their tools are not
	// offered.
	CustomNodes CustomNodes
	Credentials Credentials
	// What the library tools change (D405); without it, using and sharing are not offered.
	Sharing Sharing
	// The live actions waiting for their users (D406).
	Confirmations Confirmations
	// What the trading tools change (D406); without it, they are not offered.
	Trader Trader
	// What decides a waiting action from the agent's window (D409), and how long a call
	// waits there; without it, actions wait in Polythought alone.
	Window     Window
	WindowWait func() time.Duration

	// An admin's: users, invites and requests to join, and the instance's Config (D410,
	// D411); without them, their tools are not offered.
	Admin    Admin
	Settings Settings
	Mail     Mail
	Geoblock Geoblock
	Plugins  Plugins

	// What hands the local agent nodes' tasks to agents (D386); without it, the task tools
	// are not offered.
	Tasks Tasks

	// Sessions keeps the sessions open, for Settings to show who is connected (D414); and
	// SessionIdle is how long one lasts with no call, an hour when zero.
	Sessions    ports.MCPSessionRepository
	SessionIdle time.Duration
	// SessionLimits bound the sessions a user's agents and tokens keep open, read on each
	// new one; NewSessions counts each user's new ones (A10). Without them, nothing does.
	SessionLimits func() SessionLimits
	NewSessions   ports.RateLimiter

	// What the market tools read (D402); without a reader they are not offered.
	Markets Markets
	// What the money tools read (D402).
	Wallets Wallets
	Trades  Trades
}

type Server struct {
	agents      Agents
	tokens      Tokens
	limiter     ports.RateLimiter
	userLimiter ports.RateLimiter
	log         logging.Logger

	workflows Workflows
	nodeTypes NodeTypes
	codeTypes CodeTypes
	library   Library
	trading   Trading
	edit      Editing
	code      WorkflowCode

	customNodes CustomNodes
	credentials Credentials
	sharing     Sharing
	confirms    Confirmations
	trader      Trader
	window      Window
	windowWait  func() time.Duration
	publicURL   string

	admin    Admin
	config   Settings
	mail     Mail
	geoblock Geoblock
	plugins  Plugins
	tasks    Tasks

	markets      ai.Sources
	marketReader ports.MarketReader
	wallets      Wallets
	trades       Trades

	sessions ports.MCPSessionRepository
	boot     string        // this run of the server, its sessions kept with it
	idle     time.Duration // how long a session lasts with no call
	closing  atomic.Bool   // the server is stopping: its sessions stay kept
	mu       sync.Mutex
	tracked  map[string]*opened // the sessions seen, by ID
	limits   func() SessionLimits
	opening  ports.RateLimiter

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
		agents: d.Agents, tokens: d.Tokens, limiter: d.Limiter, userLimiter: d.UserLimiter, log: log, kit: kit,
		workflows: d.Workflows, nodeTypes: d.NodeTypes, codeTypes: d.CodeTypes, library: d.Library, trading: d.Trading,
		edit: d.Edit, code: d.Code, customNodes: d.CustomNodes, credentials: d.Credentials,
		sharing: d.Sharing, confirms: d.Confirmations, trader: d.Trader,
		window: d.Window, windowWait: d.WindowWait, publicURL: strings.TrimRight(cfg.PublicURL, "/"),
		admin: d.Admin, config: d.Settings, mail: d.Mail, geoblock: d.Geoblock, plugins: d.Plugins, tasks: d.Tasks,
		markets: d.Markets.Sources, marketReader: d.Markets.Reader, wallets: d.Wallets, trades: d.Trades,
		scope:    tracing.Scope{Manager: d.Util.TracerManager, TracerName: tracerName},
		sessions: d.Sessions, boot: newBoot(), idle: d.SessionIdle, tracked: map[string]*opened{},
		limits: d.SessionLimits, opening: d.NewSessions,
	}

	if s.idle <= 0 {
		s.idle = sessionIdle
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name: "polythought", Title: "Polythought", Version: version(), WebsiteURL: strings.TrimRight(cfg.PublicURL, "/"),
	}, &mcp.ServerOptions{
		Instructions: "Polythought builds and runs workflows that trade on Polymarket, on paper or live. " +
			"You act for the user who allowed you, with the scopes they gave you; whoami says which. With the tasks scope, " +
			"wait_for_task hands you what their workflows ask of you. " + Notice,
	})

	srv.AddReceivingMiddleware(s.track)

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

	if s.customNodes != nil {
		s.addCustomNodeTools(srv)
	}

	if s.credentials != nil {
		s.addCredentialTools(srv)
	}

	if s.nodeTypes != nil && s.codeTypes != nil && s.library != nil {
		s.addCatalogTools(srv)
	}

	if s.library != nil && s.sharing != nil && s.nodeTypes != nil {
		s.addLibraryTools(srv)
	}

	if s.confirms != nil {
		s.addConfirmationTools(srv)
	}

	if s.trader != nil && s.trades != nil {
		s.addTradeTools(srv)
	}

	if s.admin != nil {
		s.addAdminTools(srv)
	}

	if s.config != nil {
		s.addConfigTools(srv)
	}

	if s.plugins != nil {
		s.addPluginTools(srv)
	}

	if s.tasks != nil {
		s.addTaskTools(srv)
	}

	if s.marketReader != nil {
		s.addMarketTools(srv)
	}

	if s.wallets != nil && s.trades != nil {
		s.addMoneyTools(srv)
	}

	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		SessionTimeout:      s.idle,
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
	})(s.limitOpening(stream))

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
