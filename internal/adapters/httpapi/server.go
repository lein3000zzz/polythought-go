package httpapi

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
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
	MinPasswordLength() int
	CodeAccess() domain.CodeAccess
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
	CancelExecution(ctx context.Context, p auth.Principal, id domain.WorkflowID, execID domain.ExecutionID) (domain.Execution, error)
}

// NodeTypes lists what the editor can place in a user's workflow: the built-in types and
// their custom nodes.
type NodeTypes interface {
	Types(ctx context.Context, p auth.Principal) ([]engine.TypeInfo, error)
}

// Services are the application services behind the API.
type Services struct {
	Auth        AuthService
	Wallets     WalletService
	Workflows   WorkflowService
	NodeTypes   NodeTypes
	CodeTypes   CodeTypes
	Events      EventService
	Library     LibraryService
	Settings    SettingsService
	Credentials CredentialService
	Telegram    TelegramService
	// TelegramWebhook takes updates in webhook mode; nil when there is no bot.
	TelegramWebhook TelegramWebhook
	Notifications   NotificationService
	CustomNodes     CustomNodeService
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
	hertz         *server.Hertz
	auth          AuthService
	wallets       WalletService
	workflows     WorkflowService
	nodeTypes     NodeTypes
	codeTypes     CodeTypes
	customNodes   CustomNodeService
	events        EventService
	library       LibraryService
	settings      SettingsService
	credentials   CredentialService
	telegram      TelegramService
	telegramHook  TelegramWebhook
	notifications NotificationService
	log           logging.Logger
	kit           *redkit.Kit
	scope         tracing.Scope
	origin        string
	secure        bool
	clientIP      atomic.Pointer[app.ClientIP]

	// closing ends event streams on shutdown, which would otherwise hold it up.
	closing   chan struct{}
	closeOnce sync.Once
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
		// Cancel a request's context when its client goes away, so an event stream for a
		// closed tab ends at once. Only Hertz's netpoll transport, the one on Linux, can.
		server.WithSenseClientDisconnection(true),
	)

	s := &Server{
		hertz:         h,
		auth:          services.Auth,
		wallets:       services.Wallets,
		workflows:     services.Workflows,
		nodeTypes:     services.NodeTypes,
		codeTypes:     services.CodeTypes,
		customNodes:   services.CustomNodes,
		events:        services.Events,
		library:       services.Library,
		settings:      services.Settings,
		credentials:   services.Credentials,
		telegram:      services.Telegram,
		telegramHook:  services.TelegramWebhook,
		notifications: services.Notifications,
		log:           log,
		kit:           kit,
		scope:         tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		origin:        public.Scheme + "://" + public.Host,
		secure:        public.Scheme == "https",
		closing:       make(chan struct{}),
	}

	s.SetTrustedProxies(cfg.TrustedProxies)
	h.SetClientIPFunc(func(c *app.RequestContext) string { return (*s.clientIP.Load())(c) })

	s.routes()

	return s, nil
}

// SetTrustedProxies replaces the proxies trusted to report a client's address. Requests
// that start afterwards use the new ones.
func (s *Server) SetTrustedProxies(cidrs []*net.IPNet) {
	// Hertz trusts X-Forwarded-For from anyone by default, which would let clients pick
	// their own IP and dodge rate limits.
	clientIP := app.ClientIPWithOption(app.ClientIPOptions{
		RemoteIPHeaders: []string{"X-Forwarded-For", "X-Real-IP"},
		TrustedCIDRs:    cidrs,
	})

	s.clientIP.Store(&clientIP)
}

// Run serves until Shutdown is called.
func (s *Server) Run() error {
	s.log.Info(context.Background(), "HTTP server listening", "public_url", s.origin)
	return s.hertz.Run()
}

// Shutdown matches observability.ShutdownFunc.
func (s *Server) Shutdown(ctx context.Context) error {
	s.closeOnce.Do(func() { close(s.closing) })
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

	v1.GET("/credential-types", s.requireAuth, s.listCredentialTypes)

	cr := v1.Group("/credentials", s.requireAuth)
	cr.GET("", s.listCredentials)
	cr.POST("", s.createCredential)
	cr.PATCH("/:id", s.updateCredential)
	cr.DELETE("/:id", s.deleteCredential)
	cr.POST("/:id/test", s.testCredential)

	tg := v1.Group("/telegram")
	tg.GET("", s.requireAuth, s.telegramStatus)
	tg.POST("/link", s.requireAuth, s.createTelegramLink)
	tg.DELETE("/link", s.requireAuth, s.unlinkTelegram)
	tg.POST("/webhook", s.telegramWebhook) // Telegram proves itself with a secret, not a session

	v1.GET("/notifications", s.requireAuth, s.listNotifications)
	v1.PUT("/notifications/:key", s.requireAuth, s.setNotification)

	v1.GET("/node-types", s.requireAuth, s.listNodeTypes)

	cn := v1.Group("/custom-nodes", s.requireAuth)
	cn.GET("", s.listCustomNodes)
	cn.POST("", s.createCustomNode)
	cn.GET("/:id", s.getCustomNode)
	cn.PUT("/:id", s.updateCustomNode)
	cn.DELETE("/:id", s.deleteCustomNode)

	v1.GET("/code/types", s.requireAuth, s.getCodeTypes)
	v1.GET("/events", s.requireAuth, s.streamEvents)

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
	wf.POST("/:id/executions/:eid/cancel", s.cancelExecution)
	wf.GET("/:id/share", s.getSharedCopy)
	wf.PUT("/:id/share", s.shareWorkflow)

	lib := v1.Group("/library", s.requireAuth)
	lib.GET("", s.listTemplates)
	lib.GET("/:id", s.getTemplate)
	lib.POST("/:id/use", s.useTemplate)
	lib.DELETE("/:id", s.deleteTemplate)

	admin := v1.Group("/admin", s.requireAuth)
	admin.GET("/users", s.listUsers)
	admin.POST("/users", s.createUser)
	admin.PATCH("/users/:id", s.updateUser)
	admin.GET("/invites", s.listInvites)
	admin.POST("/invites", s.createInvite)
	admin.GET("/settings", s.listSettings)
	admin.PATCH("/settings", s.updateSettings)
	admin.GET("/settings/history", s.listSettingChanges)
	admin.POST("/restart", s.restartServer)
}

// Engine exposes the router for in-process tests.
func (s *Server) Engine() *server.Hertz {
	return s.hertz
}
