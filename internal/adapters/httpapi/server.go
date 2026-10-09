package httpapi

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/wallets"
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
)

const tracerName = "github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"

// AuthService is what the handlers need from the auth service.
type AuthService interface {
	RegistrationMode() domain.RegistrationMode
	Register(ctx context.Context, in auth.RegisterInput) (domain.User, error)
	Login(ctx context.Context, in auth.LoginInput) (auth.LoginResult, error)
	Logout(ctx context.Context, id domain.SessionID) error
	AuthenticateSession(ctx context.Context, token string) (auth.Principal, error)
	AuthenticateToken(ctx context.Context, token string) (auth.Principal, error)
	ChangePassword(ctx context.Context, p auth.Principal, current, next string) error

	CreateAPIToken(ctx context.Context, p auth.Principal, name string, expiresAt time.Time) (string, domain.APIToken, error)
	ListAPITokens(ctx context.Context, p auth.Principal) ([]domain.APIToken, error)
	RevokeAPIToken(ctx context.Context, p auth.Principal, id domain.APITokenID) error

	CreateUser(ctx context.Context, actor auth.Principal, email, password string, role domain.Role) (domain.User, error)
	ListUsers(ctx context.Context, actor auth.Principal, f ports.PageFilter) (ports.Page[domain.User], error)
	UpdateUser(ctx context.Context, actor auth.Principal, id domain.UserID, upd auth.UserUpdate) (domain.User, error)
	CreateInvite(ctx context.Context, actor auth.Principal) (string, domain.Invite, error)
	ListInvites(ctx context.Context, actor auth.Principal) ([]domain.Invite, error)
}

// WalletService is what the handlers need from the wallet service.
type WalletService interface {
	Link(ctx context.Context, p auth.Principal, address, label string) (domain.Wallet, error)
	List(ctx context.Context, p auth.Principal) ([]domain.Wallet, error)
	Unlink(ctx context.Context, p auth.Principal, id domain.WalletID) error
	SetCredentials(ctx context.Context, p auth.Principal, id domain.WalletID, in wallets.CredentialsInput) (domain.CredentialsInfo, error)
	CredentialsInfo(ctx context.Context, p auth.Principal, id domain.WalletID) (domain.CredentialsInfo, error)
	RemoveCredentials(ctx context.Context, p auth.Principal, id domain.WalletID) error
}

// WorkflowService is what the handlers need from the workflow service.
type WorkflowService interface {
	Create(ctx context.Context, p auth.Principal, def workflows.Definition) (domain.Workflow, error)
	Get(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error)
	List(ctx context.Context, p auth.Principal, f ports.PageFilter) (ports.Page[domain.Workflow], error)
	Update(ctx context.Context, p auth.Principal, id domain.WorkflowID, version int, def workflows.Definition) (domain.Workflow, error)
	Delete(ctx context.Context, p auth.Principal, id domain.WorkflowID) error
	Activate(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error)
	Deactivate(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error)
	Run(ctx context.Context, p auth.Principal, id domain.WorkflowID, trigger domain.NodeID) (domain.Execution, error)
	Executions(ctx context.Context, p auth.Principal, id domain.WorkflowID, f ports.PageFilter) (ports.Page[domain.Execution], error)
	Execution(ctx context.Context, p auth.Principal, id domain.WorkflowID, execID domain.ExecutionID) (domain.Execution, error)
}

// NodeTypes lists what the editor can place in a workflow.
type NodeTypes interface {
	Types() []engine.TypeInfo
}

// Services are the application services behind the API.
type Services struct {
	Auth      AuthService
	Wallets   WalletService
	Workflows WorkflowService
	NodeTypes NodeTypes
}

type Config struct {
	Addr string

	// PublicURL is where browsers reach the API, e.g. https://polythought.example.com.
	// It is the only origin allowed to make cookie-authenticated state changes, and an
	// https URL makes the session cookie Secure.
	PublicURL string

	// TrustedProxies may set X-Forwarded-For / X-Real-IP. Empty means the socket address
	// is the client, which is right unless a reverse proxy sits in front.
	TrustedProxies []*net.IPNet

	MaxBodyBytes int
}

type Server struct {
	hertz     *server.Hertz
	auth      AuthService
	wallets   WalletService
	workflows WorkflowService
	nodeTypes NodeTypes
	log       logging.Logger
	kit       *redkit.Kit
	scope     tracing.Scope
	origin    string
	secure    bool
}

func New(cfg Config, util *observability.Utility, services Services) (*Server, error) {
	public, err := url.Parse(cfg.PublicURL)
	if err != nil || public.Scheme == "" || public.Host == "" {
		return nil, fmt.Errorf("httpapi: public url %q must be absolute", cfg.PublicURL)
	}

	log := util.Logger.With("component", "http")

	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    log,
		Subsystem: "http",
		Buckets:   redkit.MediumBuckets,
		Component: "http request",
	})
	if err != nil {
		return nil, fmt.Errorf("httpapi: register metrics: %w", err)
	}

	maxBody := cfg.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = 1 << 20
	}

	hlog.SetLevel(hlog.LevelWarn)

	h := server.New(
		server.WithHostPorts(cfg.Addr),
		server.WithMaxRequestBodySize(maxBody),
		server.WithExitWaitTime(5*time.Second),
		server.WithDisablePrintRoute(true),
	)

	// Hertz trusts X-Forwarded-For from anyone by default, which would let clients pick
	// their own IP and dodge rate limits.
	h.SetClientIPFunc(app.ClientIPWithOption(app.ClientIPOptions{
		RemoteIPHeaders: []string{"X-Forwarded-For", "X-Real-IP"},
		TrustedCIDRs:    cfg.TrustedProxies,
	}))

	s := &Server{
		hertz:     h,
		auth:      services.Auth,
		wallets:   services.Wallets,
		workflows: services.Workflows,
		nodeTypes: services.NodeTypes,
		log:       log,
		kit:       kit,
		scope:     tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		origin:    public.Scheme + "://" + public.Host,
		secure:    public.Scheme == "https",
	}

	s.routes()

	return s, nil
}

// Run serves until Shutdown is called.
func (s *Server) Run() error {
	s.log.Info(context.Background(), "HTTP server listening", "public_url", s.origin)
	return s.hertz.Run()
}

// Shutdown matches observability.ShutdownFunc.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.hertz.Shutdown(ctx)
}

func (s *Server) routes() {
	h := s.hertz

	h.Use(s.observe, s.authenticate, s.checkOrigin)
	h.NoRoute(func(ctx context.Context, c *app.RequestContext) { s.fail(ctx, c, domain.ErrNotFound) })

	h.GET("/healthz", s.health)

	v1 := h.Group("/api/v1")

	v1.GET("/auth/config", s.authConfig)
	v1.POST("/auth/register", s.register)
	v1.POST("/auth/login", s.login)
	v1.POST("/auth/logout", s.requireAuth, s.logout)

	v1.GET("/me", s.requireAuth, s.me)
	v1.PUT("/me/password", s.requireAuth, s.changePassword)

	v1.GET("/tokens", s.requireAuth, s.listTokens)
	v1.POST("/tokens", s.requireAuth, s.createToken)
	v1.DELETE("/tokens/:id", s.requireAuth, s.revokeToken)

	w := v1.Group("/wallets", s.requireAuth)
	w.GET("", s.listWallets)
	w.POST("", s.linkWallet)
	w.DELETE("/:id", s.unlinkWallet)
	w.GET("/:id/credentials", s.credentialsInfo)
	w.PUT("/:id/credentials", s.setCredentials)
	w.DELETE("/:id/credentials", s.removeCredentials)

	v1.GET("/node-types", s.requireAuth, s.listNodeTypes)

	wf := v1.Group("/workflows", s.requireAuth)
	wf.GET("", s.listWorkflows)
	wf.POST("", s.createWorkflow)
	wf.GET("/:id", s.getWorkflow)
	wf.PUT("/:id", s.updateWorkflow)
	wf.DELETE("/:id", s.deleteWorkflow)
	wf.POST("/:id/activate", s.activateWorkflow)
	wf.POST("/:id/deactivate", s.deactivateWorkflow)
	wf.POST("/:id/run", s.runWorkflow)
	wf.GET("/:id/executions", s.listExecutions)
	wf.GET("/:id/executions/:eid", s.getExecution)

	admin := v1.Group("/admin", s.requireAuth)
	admin.GET("/users", s.listUsers)
	admin.POST("/users", s.createUser)
	admin.PATCH("/users/:id", s.updateUser)
	admin.GET("/invites", s.listInvites)
	admin.POST("/invites", s.createInvite)
}

// Engine exposes the router for in-process tests.
func (s *Server) Engine() *server.Hertz {
	return s.hertz
}
