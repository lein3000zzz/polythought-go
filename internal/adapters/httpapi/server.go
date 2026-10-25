package httpapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
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

	"github.com/lein3000zzz/polythought-go/internal/buildinfo"
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
	Register(ctx context.Context, in auth.RegisterInput) (auth.Registration, error)
	VerifyEmail(ctx context.Context, token, ip string) (string, error)
	ResendVerification(ctx context.Context, email, ip string) error
	SendVerificationLink(ctx context.Context, actor auth.Principal, id domain.UserID) error
	PasswordReset() bool
	RequestPasswordReset(ctx context.Context, email, ip string) error
	ResetPassword(ctx context.Context, token, password, ip string) (string, error)
	InviteEmail() bool
	InviteByEmail(ctx context.Context, actor auth.Principal, email string) (domain.Invite, bool, error)
	InviteFor(ctx context.Context, code, ip string) (domain.Invite, error)
	RequestsOpen() bool
	RequestToJoin(ctx context.Context, email, note, ip string) error
	ConfirmJoin(ctx context.Context, token, ip string) error
	Login(ctx context.Context, in auth.LoginInput) (auth.LoginResult, error)
	Logout(ctx context.Context, id domain.SessionID) error
	AuthenticateSession(ctx context.Context, token string) (auth.Principal, error)
	AuthenticateToken(ctx context.Context, token string) (auth.Principal, error)
	ChangePassword(ctx context.Context, p auth.Principal, current, next string) error
	SetShortcuts(ctx context.Context, p auth.Principal, keys map[string]string) error

	CreateAPIToken(ctx context.Context, p auth.Principal, name string, expiresAt time.Time, scopes []domain.Scope) (string, domain.APIToken, error)
	ListAPITokens(ctx context.Context, p auth.Principal) ([]domain.APIToken, error)
	RevokeAPIToken(ctx context.Context, p auth.Principal, id domain.APITokenID) error

	CreateUser(ctx context.Context, actor auth.Principal, email, password string, role domain.Role) (domain.User, error)
	ListUsers(ctx context.Context, actor auth.Principal, f ports.PageFilter) (ports.Page[domain.User], error)
	UpdateUser(ctx context.Context, actor auth.Principal, id domain.UserID, upd auth.UserUpdate) (domain.User, error)
	CreateInvite(ctx context.Context, actor auth.Principal, spec auth.InviteSpec) (domain.Invite, error)
	CheckInviteCode(ctx context.Context, actor auth.Principal, code string) (string, error)
	RevokeInvite(ctx context.Context, actor auth.Principal, id domain.InviteID) error
	ListInvites(ctx context.Context, actor auth.Principal) ([]domain.Invite, error)
	ListJoinRequests(ctx context.Context, actor auth.Principal) ([]domain.JoinRequest, error)
	ApproveJoin(ctx context.Context, actor auth.Principal, id domain.JoinRequestID) (domain.Invite, bool, error)
	DenyJoin(ctx context.Context, actor auth.Principal, id domain.JoinRequestID) error
}

// WalletService is what the handlers need from the wallet service.
type WalletService interface {
	Link(ctx context.Context, p auth.Principal, address, label string) (domain.Wallet, error)
	List(ctx context.Context, p auth.Principal) ([]domain.Wallet, error)
	// Unlink, confirmed, frees the wallet from the workflows trading live from it (D218).
	Unlink(ctx context.Context, p auth.Principal, id domain.WalletID, confirmed bool) error
	SetHeartbeat(ctx context.Context, p auth.Principal, id domain.WalletID, on bool) (domain.Wallet, error)
	SetCredentials(ctx context.Context, p auth.Principal, id domain.WalletID, in wallets.CredentialsInput) (domain.CredentialsInfo, error)
	CredentialsInfo(ctx context.Context, p auth.Principal, id domain.WalletID) (domain.CredentialsInfo, error)
	RemoveCredentials(ctx context.Context, p auth.Principal, id domain.WalletID) error
	ImportKey(ctx context.Context, p auth.Principal, id domain.WalletID, key string) (domain.CredentialsInfo, error)
	TakesKeys() bool

	TakesSessionKeys() bool
	SessionKeys(ctx context.Context, p auth.Principal, id domain.WalletID) (wallets.SessionKeys, error)
	StartSessionKey(ctx context.Context, p auth.Principal, id domain.WalletID, owner string) (wallets.SessionKeySigning, error)
	SignSessionKey(ctx context.Context, p auth.Principal, id domain.WalletID, signature string) (domain.SessionKey, error)
	StartRevocation(ctx context.Context, p auth.Principal, id domain.WalletID) (wallets.SessionKeySigning, error)
	RevokeSessionKey(ctx context.Context, p auth.Principal, id domain.WalletID, signature string) (wallets.Revocation, error)
	RemoveSessionKey(ctx context.Context, p auth.Principal, id domain.WalletID) error

	Approvals(ctx context.Context, p auth.Principal, id domain.WalletID) (wallets.ApprovalStatus, error)
	SetUpApprovals(ctx context.Context, p auth.Principal, id domain.WalletID) (domain.ApprovalSetup, error)
	StartApprovalSetup(ctx context.Context, p auth.Principal, id domain.WalletID, owner string) (wallets.ApprovalSigning, error)
	SignApprovalSetup(ctx context.Context, p auth.Principal, id domain.WalletID, signature string) (domain.ApprovalSetup, error)
}

// WorkflowService is what the handlers need from the workflow service.
type WorkflowService interface {
	Create(ctx context.Context, p auth.Principal, def workflows.Definition) (domain.Workflow, error)
	Get(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error)
	Check(ctx context.Context, p auth.Principal, id domain.WorkflowID) ([]domain.Problem, error)
	List(ctx context.Context, p auth.Principal, f ports.PageFilter) (ports.Page[domain.Workflow], error)
	Find(ctx context.Context, p auth.Principal, f ports.WorkflowFilter) (ports.Page[domain.Workflow], error)
	Pin(ctx context.Context, p auth.Principal, id domain.WorkflowID, pinned bool) error
	SetPinOrder(ctx context.Context, p auth.Principal, ids []domain.WorkflowID) error
	Update(ctx context.Context, p auth.Principal, id domain.WorkflowID, version int, def workflows.Definition) (domain.Workflow, error)
	Delete(ctx context.Context, p auth.Principal, id domain.WorkflowID) error
	Activate(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error)
	Deactivate(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error)
	// SwitchOff deactivates it and, asked, cancels its live resting orders (D217).
	SwitchOff(ctx context.Context, p auth.Principal, id domain.WorkflowID, cancelOrders bool) (domain.Workflow, error)
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
	Auth         AuthService
	Wallets      WalletService
	Workflows    WorkflowService
	NodeTypes    NodeTypes
	CodeTypes    CodeTypes
	WorkflowCode WorkflowCode
	Trading      TradingService
	Memory       MemoryService
	Events       EventService
	Library      LibraryService
	Settings     SettingsService
	Credentials  CredentialService
	// Models lists the models a language model key can use; nil when there are none.
	Models   ModelService
	Telegram TelegramService
	// TelegramWebhook takes updates in webhook mode; nil when there is no bot.
	TelegramWebhook TelegramWebhook
	Notifications   NotificationService
	CustomNodes     CustomNodeService
	Mail            MailService
	Geoblock        GeoblockService
	// Meta is what pages show about Polythought itself; nil: its repository alone.
	Meta MetaService
	// OAuth is the OAuth server agents sign in with (D397).
	OAuth OAuthService
	// Confirmations hold tokens' and agents' live actions for their users (D406).
	Confirmations ConfirmationService
	// Tasks are the tasks of Local agent nodes, and TaskAgents the agents taking them
	// (D413); nil where there are none.
	Tasks      TaskService
	TaskAgents TaskAgents
	// AgentSetup is how to connect an agent, and AgentSessions the agents connected now
	// (D414); nil where there is no MCP.
	AgentSetup    func() AgentSetup
	AgentSessions AgentSessions
	// MCP serves /mcp, agents' endpoint (D398): the SDK's server, checking its own tokens.
	MCP http.Handler
	// Spend is what users' agents spend, and the caps on it; nil when there is no agent.
	Spend SpendService
	// Plugins are what admins install (5c); nil where there are none.
	Plugins PluginService
	// CodeRunners says where user code runs, and how its runners are (D357); nil: in
	// the server's process.
	CodeRunners func() ports.CodeRunners
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

	// Build is the server's own build, shown to admins (D425).
	Build buildinfo.Info
}

type Server struct {
	hertz         *server.Hertz
	auth          AuthService
	wallets       WalletService
	workflows     WorkflowService
	nodeTypes     NodeTypes
	codeTypes     CodeTypes
	workflowCode  WorkflowCode
	trading       TradingService
	memory        MemoryService
	customNodes   CustomNodeService
	events        EventService
	library       LibraryService
	settings      SettingsService
	credentials   CredentialService
	models        ModelService
	telegram      TelegramService
	telegramHook  TelegramWebhook
	notifications NotificationService
	mail          MailService
	geoblock      GeoblockService
	meta          MetaService
	oauth         OAuthService
	confirmations ConfirmationService
	tasks         TaskService
	taskAgents    TaskAgents
	agentSetup    func() AgentSetup
	agentSessions AgentSessions
	publicURL     string
	mcp           http.Handler
	spend         SpendService
	plugins       PluginService

	codeRunnersStatus func() ports.CodeRunners
	log               logging.Logger
	kit               *redkit.Kit
	scope             tracing.Scope
	origin            string
	build             buildinfo.Info
	secure            bool
	clientIP          atomic.Pointer[app.ClientIP]

	// closing ends event streams on shutdown, which would otherwise hold it up.
	closing   chan struct{}
	closeOnce sync.Once

	maxBody int // bytes of a request's body, but an archive's (limitBody)
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
		// Bodies past it stream, for archives' uploads; limitBody holds the others to it.
		server.WithStreamBody(true),
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
		workflowCode:  services.WorkflowCode,
		trading:       services.Trading,
		memory:        services.Memory,
		customNodes:   services.CustomNodes,
		events:        services.Events,
		library:       services.Library,
		settings:      services.Settings,
		credentials:   services.Credentials,
		models:        services.Models,
		telegram:      services.Telegram,
		telegramHook:  services.TelegramWebhook,
		notifications: services.Notifications,
		mail:          services.Mail,
		geoblock:      services.Geoblock,
		meta:          services.Meta,
		oauth:         services.OAuth,
		confirmations: services.Confirmations,
		tasks:         services.Tasks,
		taskAgents:    services.TaskAgents,
		agentSetup:    services.AgentSetup,
		agentSessions: services.AgentSessions,
		publicURL:     cfg.PublicURL,
		mcp:           services.MCP,
		spend:         services.Spend,
		plugins:       services.Plugins,

		codeRunnersStatus: services.CodeRunners,
		log:               log,
		kit:               kit,
		scope:             tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		origin:            public.Scheme + "://" + public.Host,
		build:             cfg.Build,
		secure:            public.Scheme == "https",
		closing:           make(chan struct{}),
		maxBody:           maxBody,
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

	h.Use(s.limitBody(s.maxBody), s.observe, s.authenticate, s.checkOrigin, s.checkScope)
	h.NoRoute(func(ctx context.Context, c *app.RequestContext) { s.fail(ctx, c, domain.ErrNotFound) })

	h.GET("/healthz", s.health)

	v1 := h.Group("/api/v1")

	v1.GET("/meta", s.getMeta)

	// The OAuth server agents sign in with (D397); its metadata at the root (RFC 8414).
	h.GET("/.well-known/oauth-authorization-server", s.oauthMetadata)
	v1.POST("/oauth/register", s.registerOAuthClient)
	v1.POST("/oauth/token", s.oauthToken)
	v1.POST("/oauth/revoke", s.oauthRevoke)
	v1.POST("/oauth/authorization", s.requireAuth, s.oauthConsent)
	v1.POST("/oauth/authorization/approve", s.requireAuth, s.oauthApprove)
	v1.POST("/oauth/authorization/deny", s.requireAuth, s.oauthDeny)
	v1.GET("/me/grants", s.requireAuth, s.listGrants)
	v1.GET("/me/agent-setup", s.requireAuth, s.getAgentSetup)
	v1.GET("/me/agent-sessions", s.requireAuth, s.listAgentSessions)
	v1.DELETE("/me/grants/:id", s.requireAuth, s.revokeGrant)
	v1.PUT("/me/agent-access", s.requireAuth, s.setAgentAccess)
	v1.GET("/me/confirmations", s.requireAuth, s.listConfirmations)
	v1.POST("/me/confirmations/:id/confirm", s.requireAuth, s.confirmConfirmation)
	v1.POST("/me/confirmations/:id/refuse", s.requireAuth, s.refuseConfirmation)
	v1.PUT("/me/live-confirm", s.requireAuth, s.setLiveConfirm)

	// Agents' endpoint (D398) and its metadata (RFC 9728), at the path clients derive from
	// /mcp and at the root.
	h.GET("/.well-known/oauth-protected-resource", s.resourceMetadata)
	h.GET("/.well-known/oauth-protected-resource/mcp", s.resourceMetadata)
	h.GET("/mcp", s.serveMCP)
	h.POST("/mcp", s.serveMCP)
	h.DELETE("/mcp", s.serveMCP)

	v1.GET("/auth/config", s.authConfig)
	v1.POST("/auth/register", s.register)
	v1.POST("/auth/login", s.login)
	v1.POST("/auth/verify-email", s.verifyEmail)
	v1.POST("/auth/verification", s.resendVerification)
	v1.POST("/auth/password-reset", s.requestPasswordReset)
	v1.POST("/auth/password-reset/confirm", s.resetPassword)
	v1.POST("/auth/invite", s.inviteFor)
	v1.POST("/auth/requests", s.requestToJoin)
	v1.POST("/auth/requests/confirm", s.confirmJoin)
	v1.POST("/auth/logout", s.requireAuth, s.logout)

	v1.GET("/me", s.requireAuth, s.me)
	v1.PUT("/me/shortcuts", s.requireAuth, s.setShortcuts)
	v1.PUT("/me/password", s.requireAuth, s.changePassword)
	v1.PUT("/me/kill-switch", s.requireAuth, s.setKillSwitch)
	v1.GET("/me/agent-spend", s.requireAuth, s.myAgentSpend)
	v1.PUT("/me/agents", s.requireAuth, s.setAgentChoices)

	v1.GET("/tokens", s.requireAuth, s.listTokens)
	v1.POST("/tokens", s.requireAuth, s.createToken)
	v1.DELETE("/tokens/:id", s.requireAuth, s.revokeToken)

	v1.GET("/orders", s.requireAuth, s.listLiveOrders)
	v1.GET("/orders/export", s.requireAuth, s.exportLiveOrders)
	v1.GET("/orders/actors", s.requireAuth, s.listOrderActors)

	w := v1.Group("/wallets", s.requireAuth)
	w.GET("", s.listWallets)
	w.POST("", s.linkWallet)
	w.DELETE("/:id", s.unlinkWallet)
	w.PUT("/:id/heartbeat", s.setHeartbeat)
	w.GET("/:id/credentials", s.credentialsInfo)
	w.PUT("/:id/credentials", s.setCredentials)
	w.DELETE("/:id/credentials", s.removeCredentials)
	w.PUT("/:id/key", s.importKey)
	w.GET("/:id/session-key", s.sessionKeys)
	w.POST("/:id/session-key", s.startSessionKey)
	w.DELETE("/:id/session-key", s.removeSessionKey)
	w.POST("/:id/session-key/signature", s.signSessionKey)
	w.POST("/:id/session-key/revocation", s.startRevocation)
	w.POST("/:id/session-key/revocation/signature", s.revokeSessionKey)
	w.GET("/:id/cash", s.getWalletCash)
	w.GET("/:id/approvals", s.approvals)
	w.POST("/:id/approvals/setup", s.setUpApprovals)
	w.POST("/:id/approvals/signing", s.startApprovalSetup)
	w.POST("/:id/approvals/signature", s.signApprovalSetup)

	v1.GET("/credential-types", s.requireAuth, s.listCredentialTypes)

	cr := v1.Group("/credentials", s.requireAuth)
	cr.GET("", s.listCredentials)
	cr.POST("", s.createCredential)
	cr.PATCH("/:id", s.updateCredential)
	cr.DELETE("/:id", s.deleteCredential)
	cr.POST("/:id/test", s.testCredential)
	cr.GET("/:id/models", s.listModels)

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
	v1.POST("/code/workflow", s.requireAuth, s.postWorkflowCode)
	v1.GET("/events", s.requireAuth, s.streamEvents)

	wf := v1.Group("/workflows", s.requireAuth)
	wf.GET("", s.listWorkflows)
	wf.PUT("/pins", s.orderPins)
	wf.PUT("/:id/pin", s.pinWorkflow)
	wf.POST("", s.createWorkflow)
	wf.GET("/:id", s.getWorkflow)
	wf.GET("/:id/problems", s.checkWorkflow)
	wf.PUT("/:id", s.updateWorkflow)
	wf.DELETE("/:id", s.deleteWorkflow)
	wf.POST("/:id/activate", s.activateWorkflow)
	wf.POST("/:id/deactivate", s.deactivateWorkflow)
	wf.POST("/:id/run", s.runWorkflow)
	wf.GET("/:id/executions", s.listExecutions)
	wf.GET("/:id/executions/:eid", s.getExecution)
	wf.POST("/:id/executions/:eid/cancel", s.cancelExecution)
	wf.GET("/:id/executions/:eid/task", s.getExecutionTask)
	wf.POST("/:id/executions/:eid/task/hand-back", s.handBackTask)
	wf.POST("/:id/executions/:eid/task/answer", s.answerTask)
	wf.GET("/:id/paper-account", s.getPaperAccount)
	wf.POST("/:id/paper-account/reset", s.resetPaperAccount)
	wf.GET("/:id/trading", s.getTradingSummary)
	wf.GET("/:id/trading/setup", s.getTradingSetup)
	wf.PUT("/:id/trading/setup", s.setTradingSetup)
	wf.GET("/:id/orders", s.listOrders)
	wf.POST("/:id/orders/:order/cancel", s.cancelOrder)
	wf.GET("/:id/holdings", s.listHoldings)
	wf.GET("/:id/nodes/:node/memory", s.getNodeMemory)
	wf.DELETE("/:id/nodes/:node/memory", s.forgetNodeMemory)
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
	admin.POST("/users/:id/verification", s.sendVerificationLink)
	admin.GET("/users/:id/agent-spend", s.userAgentSpend)
	admin.GET("/invites", s.listInvites)
	admin.POST("/invites", s.createInvite)
	admin.POST("/invites/check", s.checkInviteCode)
	admin.POST("/invites/:id/revoke", s.revokeInvite)
	admin.GET("/requests", s.listJoinRequests)
	admin.POST("/requests/:id/approve", s.approveJoin)
	admin.POST("/requests/:id/deny", s.denyJoin)
	admin.GET("/settings", s.listSettings)
	admin.PATCH("/settings", s.updateSettings)
	admin.GET("/settings/history", s.listSettingChanges)
	admin.POST("/restart", s.restartServer)
	admin.GET("/version", s.serverVersion)
	admin.POST("/mail/test", s.sendTestMail)
	admin.GET("/plugins", s.hasPlugins, s.listPlugins)
	admin.POST("/plugins", s.hasPlugins, s.installPlugin)
	admin.POST("/plugins/fetch", s.hasPlugins, s.fetchPlugin)
	admin.POST("/plugins/found/:id/:version", s.hasPlugins, s.adoptPlugin)
	admin.POST("/plugins/:id/versions/:version/approve", s.hasPlugins, s.approvePlugin)
	admin.DELETE("/plugins/:id/versions/:version", s.hasPlugins, s.removePluginVersion)
	admin.POST("/plugins/:id/enable", s.hasPlugins, s.enablePlugin)
	admin.POST("/plugins/:id/disable", s.hasPlugins, s.disablePlugin)
	admin.PUT("/plugins/:id/users", s.hasPlugins, s.setPluginUsers)
	admin.POST("/plugins/:id/start", s.hasPlugins, s.startPlugin)
	admin.DELETE("/plugins/:id", s.hasPlugins, s.uninstallPlugin)
	admin.PUT("/plugins/:id/caps", s.hasPlugins, s.setPluginCaps)
	admin.GET("/plugins/left-behind", s.hasPlugins, s.pluginDataLeft)
	admin.DELETE("/plugins/left-behind/:id", s.hasPlugins, s.removePluginDataLeft)
	admin.GET("/plugins/:id/history", s.hasPlugins, s.pluginHistory)
	admin.GET("/plugins/:id/settings", s.hasPlugins, s.pluginSettings)
	admin.PATCH("/plugins/:id/settings", s.hasPlugins, s.updatePluginSettings)
	admin.GET("/geoblock", s.geoblockStatus)
	admin.GET("/code-runners", s.codeRunners)
	admin.POST("/geoblock/check", s.checkGeoblock)
}

// Engine exposes the router for in-process tests.
func (s *Server) Engine() *server.Hertz {
	return s.hertz
}
