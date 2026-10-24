package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging/slogging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/prommetrics"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing/oteltracing"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/adapters/clientdocs"
	"github.com/lein3000zzz/polythought-go/internal/adapters/crypto"
	"github.com/lein3000zzz/polythought-go/internal/adapters/github"
	"github.com/lein3000zzz/polythought-go/internal/adapters/llm"
	"github.com/lein3000zzz/polythought-go/internal/adapters/mailer"
	"github.com/lein3000zzz/polythought-go/internal/adapters/mcpserver"
	"github.com/lein3000zzz/polythought-go/internal/adapters/pluginhost"
	pmadapter "github.com/lein3000zzz/polythought-go/internal/adapters/polymarket"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/adapters/ratelimit"
	"github.com/lein3000zzz/polythought-go/internal/adapters/sandbox"
	tgadapter "github.com/lein3000zzz/polythought-go/internal/adapters/telegram"
	"github.com/lein3000zzz/polythought-go/internal/adapters/tscompiler"
	"github.com/lein3000zzz/polythought-go/internal/adapters/webhook"
	"github.com/lein3000zzz/polythought-go/internal/config"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/ai"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/code"
	"github.com/lein3000zzz/polythought-go/internal/core/services/confirmations"
	"github.com/lein3000zzz/polythought-go/internal/core/services/credentials"
	"github.com/lein3000zzz/polythought-go/internal/core/services/customnodes"
	"github.com/lein3000zzz/polythought-go/internal/core/services/events"
	"github.com/lein3000zzz/polythought-go/internal/core/services/feed"
	"github.com/lein3000zzz/polythought-go/internal/core/services/geoblock"
	"github.com/lein3000zzz/polythought-go/internal/core/services/library"
	"github.com/lein3000zzz/polythought-go/internal/core/services/mail"
	"github.com/lein3000zzz/polythought-go/internal/core/services/memory"
	"github.com/lein3000zzz/polythought-go/internal/core/services/meta"
	"github.com/lein3000zzz/polythought-go/internal/core/services/models"
	"github.com/lein3000zzz/polythought-go/internal/core/services/notify"
	"github.com/lein3000zzz/polythought-go/internal/core/services/oauth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/plugins"
	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
	"github.com/lein3000zzz/polythought-go/internal/core/services/spend"
	"github.com/lein3000zzz/polythought-go/internal/core/services/tasks"
	"github.com/lein3000zzz/polythought-go/internal/core/services/telegram"
	"github.com/lein3000zzz/polythought-go/internal/core/services/traders"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
	"github.com/lein3000zzz/polythought-go/internal/core/services/waits"
	"github.com/lein3000zzz/polythought-go/internal/core/services/wallets"
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

var logLevels = map[string]logging.Level{
	"debug": logging.LevelDebug,
	"info":  logging.LevelInfo,
	"warn":  logging.LevelWarn,
	"error": logging.LevelError,
}

// newUtility sets up logging, metrics and tracing. Call its Shutdown before exiting. The
// returned logger is the root one, whose level can change while running.
func newUtility(level, format string) (*observability.Utility, *slogging.SlogLogger, error) {
	logFormat := logging.FormatText
	if format == "json" {
		logFormat = logging.FormatJSON
	}

	log, err := slogging.NewSlogLoggerWithConfig(slogging.Config{
		Level:    logLevels[level],
		Format:   logFormat,
		ToStdout: true,
	}, oteltracing.CorrelateCore)
	if err != nil {
		return nil, nil, err
	}

	oteltracing.InstallPropagator()

	return observability.NewUtility(
		log,
		prommetrics.DefaultManager(),
		oteltracing.DefaultOtelManager(),
		func(context.Context) error { return log.Close() },
	), log, nil
}

func shutdown(util *observability.Utility) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := util.Shutdown(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "polythought: shutdown:", err)
	}
}

// restartDelay lets the request that asked for a restart be answered first.
const restartDelay = 300 * time.Millisecond

// app is everything the server and the operator tools share.
type app struct {
	cfg             config.Config
	file            config.File // the config file's values, for the settings' file layer
	util            *observability.Utility
	db              *postgres.DB
	box             ports.SecretBox
	settings        *settings.Service
	auth            *auth.Service
	wallets         *wallets.Service
	workflows       *workflows.Service
	registry        *engine.Registry
	supervisor      *engine.Supervisor
	feed            *feed.Hub
	traders         *traders.Hub
	trading         *trading.Service
	confirmations   *confirmations.Service
	workflowTrading *postgres.WorkflowTrading
	nodeMemory      *postgres.NodeMemory
	memory          *memory.Service
	events          *events.Hub
	library         *library.Service
	customNodes     *customnodes.Service
	credentials     *credentials.Service
	models          *models.Service
	spend           *spend.Service
	telegramBot     *tgadapter.Bot
	telegram        *telegram.Service
	notify          *notify.Service
	mail            *mail.Service
	geoblock        *geoblock.Service
	meta            *meta.Service
	oauth           *oauth.Service
	markets         *pmadapter.Reader
	waits           *waits.Service
	taskAgents      *tasks.Presence // which owners have an agent taking tasks (D386)
	tasks           *tasks.Service
	code            *code.Service
	codeRunners     func() ports.CodeRunners
	launcher        *sandbox.Launcher // nil where there is no sandbox, noSandbox saying why
	noSandbox       error
	codeGate        auth.CodeGate
	plugins         *plugins.Service
	pluginRuntime   *pluginhost.Runtime

	restartOnce sync.Once
	restartc    chan struct{} // closed when an admin asked for a restart
}

func newApp(ctx context.Context) (*app, error) {
	registry, err := config.Settings()
	if err != nil {
		return nil, err
	}

	path, required := config.FilePath(configPath, os.Getenv)

	file, err := config.ReadFile(path, required, registry)
	if err != nil {
		return nil, err
	}

	cfg, err := config.Load(os.Getenv, file)
	if err != nil {
		return nil, fmt.Errorf("config:\n%w", err)
	}

	// The database comes first: the log settings are stored in it.
	db, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}

	a := &app{cfg: cfg, file: file, db: db, restartc: make(chan struct{})}

	if err := a.init(ctx); err != nil {
		if a.util != nil {
			shutdown(a.util)
		} else {
			_ = db.Close(ctx)
		}

		return nil, err
	}

	return a, nil
}

func (a *app) init(ctx context.Context) error {
	var applied []int64

	if a.cfg.AutoMigrate {
		var err error
		if applied, err = a.db.Migrate(ctx); err != nil {
			return err
		}
	}

	keys, err := crypto.ParseStaticKeys(a.cfg.SecretKeys)
	if err != nil {
		return err
	}

	a.box = crypto.NewEnvelope(keys)

	if err := a.initSettings(ctx, a.box); err != nil {
		return err
	}

	a.util.OnShutdown(a.db.Close)

	if len(applied) > 0 {
		a.util.Logger.Info(ctx, "Migrations applied", "versions", applied)
	}

	pmc, err := newPolymarket(a)
	if err != nil {
		return err
	}

	a.markets = pmc.reader // agents' market tools read it too (D402)

	// Before the engine: the Telegram nodes use the bot, and failures are notified.
	// The bot and the notifications run only in serve, which starts them.
	if a.telegramBot, err = tgadapter.New(a.util); err != nil {
		return err
	}

	a.telegram = telegram.New(a.telegramBot, postgres.NewTelegramLinks(a.db), postgres.NewUsers(a.db), a.util.Logger)
	a.telegramBot.SetHandler(a.telegram.HandleUpdate)

	// Mail goes through the server set up on the Config page, read for each message (D168).
	a.mail = mail.New(mailer.New(func() mailer.Config {
		s := a.settings.Current()

		return mailer.Config{
			Host: s.String(config.KeyMailHost), Port: s.Int(config.KeyMailPort), Security: s.String(config.KeyMailSecurity),
			Username: s.String(config.KeyMailUsername), Password: s.String(config.KeyMailPassword),
			From: s.String(config.KeyMailFrom), FromName: s.String(config.KeyMailFromName),
		}
	}, a.util.Logger), a.cfg.PublicURL, a.util.Logger,
		mail.WithNotifications(postgres.NewUsers(a.db), func() bool { return a.settings.Current().Bool(config.KeyMailNotifications) }))

	a.notify = notify.New(a.telegram, postgres.NewNotificationPrefs(a.db), postgres.NewWorkflows(a.db), a.cfg.PublicURL, a.util.Logger,
		notify.WithMail(a.mail), notify.WithMarkets(pmc.reader), notify.WithAdmins(postgres.NewUsers(a.db)),
		notify.WithConfirmAsker(a.telegram))

	// Whether Polymarket takes orders from here (D186); serve checks it every 6 hours.
	a.geoblock = geoblock.New(pmc.geoblock, a.util.Logger)

	// The repository's stars beside its link, while the setting is on (D372); serve
	// looks.
	stars, err := github.New(a.util, github.Config{Repo: meta.Repository})
	if err != nil {
		return err
	}

	a.meta = meta.New(stars, func() bool { return a.settings.Current().Bool(config.KeyGitHubStars) }, a.util.Logger)

	// The OAuth server agents sign in with, for the MCP endpoint (D382, D397), as the
	// Config page sets it (D398). A client's document is fetched through the guard
	// webhooks use, from public addresses unless an admin allows private ones.
	a.oauth = oauth.New(oauth.Config{Issuer: a.cfg.PublicURL, Resource: strings.TrimRight(a.cfg.PublicURL, "/") + "/mcp"},
		postgres.NewOAuth(a.db),
		clientdocs.New(func() bool { return a.settings.Current().Bool(config.KeyMCPPrivateDocuments) }, nil),
		postgres.NewUsers(a.db),
		ratelimit.NewMemory(30*time.Second, 10), // per address: 10, then one every 30 s
		a.util.Logger,
		oauth.WithPolicy(func() oauth.Policy { return oauthPolicy(a.settings.Current()) }))

	// After notify, which reminds of expiring session keys. A wallet a workflow trades
	// live from is not unlinked.
	walletRepo := postgres.NewWallets(a.db)
	a.workflowTrading = postgres.NewWorkflowTrading(a.db)
	a.wallets = wallets.New(walletRepo, a.box, pmc.account, a.util.Logger,
		wallets.WithWalletRelease(laterTrading{a}),
		wallets.WithKeys(pmc.keys, func() bool {
			s := a.settings.Current()
			return config.TakesKeys(s.String(config.KeyKeyImport), domain.RegistrationMode(s.String(config.KeyRegistration)))
		}),
		wallets.WithSessionKeys(walletRepo, pmc.sessionKeys, a.notify),
		wallets.WithApprovals(walletRepo, pmc.approvals))

	if err := a.initEngine(ctx, pmc); err != nil {
		return err
	}

	a.telegram.UseMenu(telegram.Menu{
		Workflows: a.workflows, Wallets: a.wallets, Positions: pmc.reader, Notifications: a.notify, Trading: a.trading,
		Live: a.trading,
	})
	a.telegram.UseAnswers(a.waits)
	a.telegram.UseConfirmations(a.confirmations)

	a.auth, err = auth.New(authConfig(a.settings.Current()), auth.Repositories{
		Users:        postgres.NewUsers(a.db),
		Sessions:     postgres.NewSessions(a.db),
		Tokens:       postgres.NewAPITokens(a.db),
		Invites:      postgres.NewInvites(a.db),
		EmailTokens:  postgres.NewEmailTokens(a.db),
		JoinRequests: postgres.NewJoinRequests(a.db),
		Tx:           a.db,
	},
		crypto.NewArgon2id(crypto.DefaultArgon2Params),
		ratelimit.NewMemory(30*time.Second, 10), // 10 attempts, then one every 30 s per key
		a.util.Logger,
		auth.WithWorkflowStopper(a.workflows),
		auth.WithLiveStopper(laterTrading{a}),
		auth.WithAccountLimiter(ratelimit.NewMemory(6*time.Second, 50)), // per address, from every IP
		auth.WithLiveConfig(func() auth.Config { return authConfig(a.settings.Current()) }),
		auth.WithAccountMail(a.mail),
		auth.WithInviteBox(a.box), // codes kept to copy again (D393)
		auth.WithJoinNotifier(a.notify),
		// An admin's agents' work on users waits for the admin where they say so (D410).
		auth.WithConfirmations(a.confirmations),
		auth.WithInstanceCaps(func() (domain.LiveCaps, domain.AgentCaps) { return a.liveCaps(), a.agentCaps() }),
	)
	if err != nil {
		return err
	}

	// Each kind of an admin's work on users, carried out as asked once confirmed.
	for _, kind := range []domain.ConfirmKind{domain.ConfirmRole, domain.ConfirmDisable, domain.ConfirmCaps, domain.ConfirmCode} {
		a.confirmations.Handle(kind, func(ctx context.Context, p auth.Principal, c domain.Confirmation) (string, error) {
			var in auth.UserAsked
			if err := json.Unmarshal(c.Payload, &in); err != nil {
				return "", err
			}

			_, err := a.auth.UpdateUser(ctx, p, in.User, in.Update)

			return "changed", err
		})
	}

	return nil
}

// liveCaps are the instance's caps on each user's live buys (D224).
func (a *app) liveCaps() domain.LiveCaps {
	s := a.settings.Current()
	perOrder, perDay := s.Int(config.KeyMaxLiveOrder), s.Int(config.KeyMaxLiveDay)

	return domain.LiveCaps{PerOrder: &perOrder, PerDay: &perDay}
}

// agentCaps are the instance's caps on each user's agents' spend a month (D271).
func (a *app) agentCaps() domain.AgentCaps {
	s := a.settings.Current()
	dollars, _ := decimal.NewFromString(s.String(config.KeyAgentDollars))
	in, out, total := int64(s.Int(config.KeyAgentInput)), int64(s.Int(config.KeyAgentOutput)), int64(s.Int(config.KeyAgentTotal))

	return domain.AgentCaps{Dollars: &dollars, InputTokens: &in, OutputTokens: &out, TotalTokens: &total}
}

// newMCP is agents' endpoint (D398), for their OAuth tokens and for API tokens, each
// agent's tool calls held to the setting's a minute (D400), its tools reading what the
// API does (D401). Made once every service is, plugins' included.
func (a *app) newMCP() (*mcpserver.Server, error) {
	return mcpserver.New(mcpserver.Config{PublicURL: a.cfg.PublicURL}, mcpserver.Deps{
		Agents: a.oauth, Tokens: a.auth, Util: a.util,
		Limiter:       ratelimit.NewPerMinute(func() int { return a.settings.Current().Int(config.KeyMCPRate) }),
		Workflows:     a.workflows,
		Edit:          a.workflows,
		Code:          workflowCode{code: a.code, gate: a.codeGate},
		CustomNodes:   a.customNodes,
		Credentials:   a.credentials,
		Sharing:       a.library,
		Confirmations: a.confirmations,
		Trader:        a.trading,
		Window:        a.confirmations,
		WindowWait:    func() time.Duration { return a.settings.Current().Duration(config.KeyMCPWindowWait) },
		NodeTypes:     nodeTypes{types: a.customNodes, plugins: a.plugins},
		CodeTypes:     codeTypes{types: a.customNodes},
		Library:       a.library,
		Markets: mcpserver.Markets{Reader: a.markets, Sources: ai.Sources{
			Events: a.markets, Books: a.markets, History: a.markets, Trades: a.markets, Positions: a.markets,
		}},
		Wallets:     a.wallets,
		Trades:      a.trading,
		Admin:       a.auth,
		Settings:    a.settings,
		Mail:        a.mail,
		Geoblock:    a.geoblock,
		Plugins:     a.plugins,
		Tasks:       a.tasks,
		Sessions:    postgres.NewMCPSessions(a.db),
		SessionIdle: a.settings.Current().Duration(config.KeyMCPSessionIdle),
		Trading: func(ctx context.Context, w domain.Workflow) (string, error) {
			if !nodes.PlacesOrders(a.registry, w) {
				return "none", nil
			}

			mode, err := a.trading.Mode(ctx, w.ID)

			return string(mode), err
		},
	})
}

// telegramConfig is how the bot should run, from the settings.
func (a *app) telegramConfig(s *settings.Snapshot) tgadapter.Config {
	return tgadapter.Config{
		Enabled:   s.Bool(config.KeyTelegramEnabled),
		Token:     s.String(config.KeyTelegramToken),
		Mode:      s.String(config.KeyTelegramMode),
		PublicURL: a.cfg.PublicURL,
	}
}

// initSettings loads the settings, then builds logging from them.
func (a *app) initSettings(ctx context.Context, box ports.SecretBox) error {
	registry, err := config.Settings()
	if err != nil {
		return err
	}

	a.settings = settings.New(registry, postgres.NewSettings(a.db), box, settings.WithRestarter(a.requestRestart))

	warnings, err := a.settings.Load(ctx, os.Getenv, a.file.Settings())
	if err != nil {
		return fmt.Errorf("settings:\n%w", err)
	}

	current := a.settings.Current()

	util, root, err := newUtility(current.String(config.KeyLogLevel), current.String(config.KeyLogFormat))
	if err != nil {
		return err
	}

	a.util = util

	for _, w := range warnings {
		util.Logger.Warn(ctx, "Saved setting ignored; fix it on the Config page", logging.Err(w))
	}

	a.settings.Subscribe(func(s *settings.Snapshot) {
		root.SetLevel(logLevels[s.String(config.KeyLogLevel)])
	})

	return nil
}

// requestRestart is what an admin's restart request calls.
func (a *app) requestRestart(by domain.User) {
	a.restartOnce.Do(func() {
		a.util.Logger.Info(context.Background(), "Restart requested", "by", by.Email)
		time.AfterFunc(restartDelay, func() { close(a.restartc) })
	})
}

func authConfig(s *settings.Snapshot) auth.Config {
	cfg := auth.DefaultConfig()
	cfg.Registration = domain.RegistrationMode(s.String(config.KeyRegistration))
	cfg.SessionIdleTTL = s.Duration(config.KeySessionIdleTTL)
	cfg.SessionAbsoluteTTL = s.Duration(config.KeySessionAbsoluteTTL)
	cfg.InviteTTL = s.Duration(config.KeyInviteTTL)
	cfg.MinPasswordLength = s.Int(config.KeyMinPasswordLength)
	cfg.CodeAccess = codeAccess(s)
	cfg.VerifyEmail = s.Bool(config.KeyVerifyEmail)
	cfg.PasswordReset = s.Bool(config.KeyPasswordReset)
	cfg.InviteEmail = s.Bool(config.KeyInviteEmail)
	cfg.Requests = s.Bool(config.KeyRequests)
	cfg.RequestDeniedMail = s.Bool(config.KeyRequestDeniedMail)
	cfg.AgentPasswords = s.Bool(config.KeyMCPAgentPasswords)

	return cfg
}

// oauthPolicy is what admins set for agents over MCP (D398).
func oauthPolicy(s *settings.Snapshot) oauth.Policy {
	return oauth.Policy{
		Enabled:      s.Bool(config.KeyMCPEnabled),
		Registration: s.Bool(config.KeyMCPRegistration),
		SkipConsent:  s.Bool(config.KeyMCPSkipConsent),
		AccessTTL:    s.Duration(config.KeyMCPAccessTTL),
		RefreshTTL:   s.Duration(config.KeyMCPRefreshTTL),
	}
}

func codeAccess(s *settings.Snapshot) domain.CodeAccess {
	return domain.CodeAccess(s.String(config.KeyCodeAccess))
}

func customNodeLimits(s *settings.Snapshot) customnodes.Limits {
	return customnodes.Limits{PerUser: s.Int(config.KeyCustomNodes)}
}

func waitLimits(s *settings.Snapshot) engine.WaitLimits {
	return engine.WaitLimits{
		MaxWaiting: s.Int(config.KeyMaxWaiting),
		MaxWait:    s.Duration(config.KeyMaxWait),
	}
}

// workflowChecks checks a workflow as saving does, with the node types of its owner: the
// built-in ones and their custom nodes.
type workflowChecks struct {
	types    engine.Catalog
	compiler ports.CodeCompiler
	gate     engine.CodeGate
	plugins  engine.PluginGate
}

func (c workflowChecks) Validate(ctx context.Context, w domain.Workflow) error {
	reg, err := c.types.For(ctx, w.OwnerID)
	if err != nil {
		return err
	}

	return reg.Validate(w)
}

func (c workflowChecks) CheckFields(ctx context.Context, w domain.Workflow) error {
	reg, err := c.types.For(ctx, w.OwnerID)
	if err != nil {
		return err
	}

	return reg.CheckFields(w)
}

// CheckExpressions checks the workflow's expressions and code: each compiled.
func (c workflowChecks) CheckExpressions(ctx context.Context, w domain.Workflow) error {
	reg, err := c.types.For(ctx, w.OwnerID)
	if err != nil {
		return err
	}

	var problems domain.ValidationError

	problems.Merge(reg.CheckExpressions(w, func(expr string) error {
		_, err := c.compiler.CompileExpression(ctx, expr)
		return err
	}))

	problems.Merge(reg.CheckCode(w, func(source string) error {
		_, err := c.compiler.Compile(ctx, source)
		return err
	}))

	return problems.Err()
}

// CheckCodeAccess refuses code nodes in a workflow whose owner may not write code.
func (c workflowChecks) CheckCodeAccess(ctx context.Context, w domain.Workflow) error {
	reg, err := c.types.For(ctx, w.OwnerID)
	if err != nil {
		return err
	}

	return reg.CheckCodeAccess(ctx, w, c.gate)
}

// nodeTypes lists the node types a user may place: the built-in ones, their own, and
// those of the plugins they may use (D330).
type nodeTypes struct {
	types   engine.Catalog
	plugins engine.PluginGate
}

func (n nodeTypes) Types(ctx context.Context, p auth.Principal) ([]engine.TypeInfo, error) {
	reg, err := n.types.For(ctx, p.User.ID)
	if err != nil {
		return nil, err
	}

	all := reg.Types()
	out := make([]engine.TypeInfo, 0, len(all))
	allowed := map[string]bool{}

	for _, t := range all {
		if t.Plugin != "" {
			ok, seen := allowed[t.Plugin]
			if !seen {
				if ok, err = n.plugins.PluginAllowed(ctx, p.User.ID, t.Plugin); err != nil {
					return nil, err
				}

				allowed[t.Plugin] = ok
			}

			if !ok {
				continue
			}
		}

		out = append(out, t)
	}

	return out, nil
}

// codeTypes declares what a user's code is written against: the SDK and the node types
// they may use, their own included.
type codeTypes struct {
	types engine.Catalog
}

func (c codeTypes) Declarations(ctx context.Context, p auth.Principal) ([]ports.CodeFile, error) {
	reg, err := c.types.For(ctx, p.User.ID)
	if err != nil {
		return nil, err
	}

	return tscompiler.Declarations(reg.Types()), nil
}

// laterTrading is the trading service, made after the wallets service that asks it which
// workflows trade live from a wallet being unlinked (D218).
type laterTrading struct{ a *app }

func (l laterTrading) TradingLive(ctx context.Context, owner domain.UserID, wallet domain.WalletID) ([]string, error) {
	return l.a.trading.TradingLive(ctx, owner, wallet)
}

func (l laterTrading) ReleaseWallet(ctx context.Context, owner domain.UserID, wallet domain.WalletID) error {
	return l.a.trading.ReleaseWallet(ctx, owner, wallet)
}

// StopLive turns on the kill switch of a user an admin disabled (D226), for the auth
// service, made before the trading service.
func (l laterTrading) StopLive(ctx context.Context, owner domain.UserID) error {
	_, err := l.a.trading.StopLive(ctx, owner, "cancelled: an admin disabled the account")

	return err
}

// laterWorkflows is the workflows service, made after the trading service that asks it
// whose a workflow is.
type laterWorkflows struct{ a *app }

func (l laterWorkflows) Get(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error) {
	return l.a.workflows.Get(ctx, p, id)
}

// handleConfirmed carries out what users confirm of their tokens' and agents' live
// actions, as each asked (D406).
func (a *app) handleConfirmed() {
	a.confirmations.Handle(domain.ConfirmRun, func(ctx context.Context, p auth.Principal, c domain.Confirmation) (string, error) {
		var in workflows.RunAsked
		if err := json.Unmarshal(c.Payload, &in); err != nil {
			return "", err
		}

		e, err := a.workflows.Run(ctx, p, c.WorkflowID, in.Trigger)
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("execution %s, %s", e.ID, e.Status), nil
	})

	a.confirmations.Handle(domain.ConfirmSwitchOn, func(ctx context.Context, p auth.Principal, c domain.Confirmation) (string, error) {
		_, err := a.workflows.Activate(ctx, p, c.WorkflowID)
		return "switched on", err
	})

	a.confirmations.Handle(domain.ConfirmChange, func(ctx context.Context, p auth.Principal, c domain.Confirmation) (string, error) {
		var in workflows.ChangeAsked
		if err := json.Unmarshal(c.Payload, &in); err != nil {
			return "", err
		}

		w, err := a.workflows.Update(ctx, p, c.WorkflowID, in.Version, in.Definition)
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("version %d saved", w.Version), nil
	})

	a.confirmations.Handle(domain.ConfirmGoLive, func(ctx context.Context, p auth.Principal, c domain.Confirmation) (string, error) {
		var in trading.SetupChange
		if err := json.Unmarshal(c.Payload, &in); err != nil {
			return "", err
		}

		_, err := a.trading.SetSetup(ctx, p, c.WorkflowID, in)

		return "trading live", err
	})

	a.confirmations.Handle(domain.ConfirmKillOff, func(ctx context.Context, p auth.Principal, _ domain.Confirmation) (string, error) {
		_, err := a.trading.SetKillSwitch(ctx, p, false)
		return "kill switch off", err
	})

	// An admin's work on the instance (D411).
	a.settings.UseConfirmations(a.confirmations)
	a.plugins.UseConfirmations(a.confirmations)

	a.confirmations.Handle(domain.ConfirmConfig, func(ctx context.Context, p auth.Principal, c domain.Confirmation) (string, error) {
		var changes map[string]json.RawMessage
		if err := json.Unmarshal(c.Payload, &changes); err != nil {
			return "", err
		}

		return "changed", a.settings.Update(ctx, p, changes)
	})

	a.confirmations.Handle(domain.ConfirmRestart, func(ctx context.Context, p auth.Principal, _ domain.Confirmation) (string, error) {
		return "restarting", a.settings.Restart(ctx, p)
	})

	for _, kind := range []domain.ConfirmKind{domain.ConfirmPluginApprove, domain.ConfirmPluginChange} {
		a.confirmations.Handle(kind, func(ctx context.Context, p auth.Principal, c domain.Confirmation) (string, error) {
			var in plugins.Asked
			if err := json.Unmarshal(c.Payload, &in); err != nil {
				return "", err
			}

			return "done", a.pluginAsked(ctx, p, in)
		})
	}
}

// pluginAsked carries out a held plugin action, as it was asked.
func (a *app) pluginAsked(ctx context.Context, p auth.Principal, in plugins.Asked) error {
	switch in.Op {
	case plugins.OpApprove:
		return a.plugins.Approve(ctx, p, in.ID, in.Version, in.Permissions)
	case plugins.OpRemoveVersion:
		return a.plugins.RemoveVersion(ctx, p, in.ID, in.Version)
	case plugins.OpEnable:
		return a.plugins.SetEnabled(ctx, p, in.ID, in.Enabled)
	case plugins.OpUsers:
		return a.plugins.SetUsers(ctx, p, in.ID, in.Everyone, in.Users)
	case plugins.OpStart:
		return a.plugins.Start(ctx, p, in.ID)
	case plugins.OpUninstall:
		return a.plugins.Uninstall(ctx, p, in.ID, in.RemoveData)
	case plugins.OpCaps:
		return a.plugins.SetCaps(ctx, p, in.ID, in.Caps)
	case plugins.OpSettings:
		return a.plugins.SetSettings(ctx, p, in.ID, in.Settings)
	case plugins.OpRemoveLeft:
		return a.plugins.RemoveLeftBehind(ctx, p, in.ID)
	}

	return fmt.Errorf("no plugin action %q", in.Op)
}

// paramsWhere names a node type's settings that match.
func paramsWhere(r *engine.Registry, match func(engine.ParamSpec) bool) func(nodeType string) []string {
	return func(nodeType string) []string {
		t, _, ok := r.Type(nodeType)
		if !ok {
			return nil
		}

		var out []string

		for _, p := range t.Params {
			if match(p) {
				out = append(out, p.Name)
			}
		}

		return out
	}
}

// workflowCode runs workflow code for the Code tab's Apply (D74), for those who may write
// code (D75).
type workflowCode struct {
	code *code.Service
	gate engine.CodeGate
}

func (w workflowCode) Build(ctx context.Context, p auth.Principal, source string) (domain.Workflow, error) {
	allowed, err := w.gate.CodeAllowed(ctx, p.User.ID)
	if err != nil {
		return domain.Workflow{}, err
	}

	if !allowed {
		return domain.Workflow{}, domain.ErrCodeOff
	}

	return w.code.Workflow(ports.WithCodeOwner(ctx, p.User.ID), source)
}

// codeLimits are the most one run of user code may use.
func codeLimits(s *settings.Snapshot) ports.CodeLimits {
	return ports.CodeLimits{
		Memory:    int64(s.Int(config.KeyCodeMemory)) << 20,
		Time:      s.Duration(config.KeyCodeTime),
		Output:    s.Int(config.KeyCodeOutput) << 10,
		HostCalls: s.Int(config.KeyCodeHostCalls),
	}
}

func workflowLimits(s *settings.Snapshot) workflows.Limits {
	return workflows.Limits{
		Workflows:       s.Int(config.KeyMaxWorkflows),
		ActiveWorkflows: s.Int(config.KeyMaxActiveWorkflows),
		NodesPerFlow:    s.Int(config.KeyMaxNodes),
	}
}

// polymarket holds the adapters over the Polymarket clients.
type polymarket struct {
	reader      *pmadapter.Reader
	account     *pmadapter.Account
	keys        *pmadapter.Keys
	sessionKeys *pmadapter.SessionKeys
	approvals   *pmadapter.Approvals
	geoblock    *pmadapter.Geoblock
	exchange    *pmadapter.Exchange
	orders      *pmadapter.OrderStream
}

func newPolymarket(a *app) (polymarket, error) {
	gamma, err := pm.NewGammaClient(a.util, pm.HTTPConfig{})
	if err != nil {
		return polymarket{}, err
	}

	clob, err := pm.NewClobClient(a.util, pm.HTTPConfig{})
	if err != nil {
		return polymarket{}, err
	}

	data, err := pm.NewDataClient(a.util, pm.HTTPConfig{})
	if err != nil {
		return polymarket{}, err
	}

	relayer, err := pm.NewRelayerClient(a.util, pm.HTTPConfig{})
	if err != nil {
		return polymarket{}, err
	}

	geo, err := pm.NewGeoblockClient(a.util, pm.HTTPConfig{})
	if err != nil {
		return polymarket{}, err
	}

	rpc, err := pm.NewRPCClient(a.util, pm.HTTPConfig{}, func() string {
		return a.settings.Current().String(config.KeyPolygonRPC)
	})
	if err != nil {
		return polymarket{}, err
	}

	// The Builder key, while an admin has entered all of it (D156).
	builder := func() (pm.BuilderCredentials, bool) {
		s := a.settings.Current()
		b := pm.BuilderCredentials{
			Key: s.String(config.KeyBuilderAPIKey), Secret: s.String(config.KeyBuilderSecret),
			Passphrase: s.String(config.KeyBuilderPassphrase),
		}

		return b, b.Key != "" && b.Secret != "" && b.Passphrase != ""
	}

	return polymarket{
		reader: pmadapter.NewReader(gamma, clob, data), account: pmadapter.NewAccount(clob), keys: pmadapter.NewKeys(clob),
		sessionKeys: pmadapter.NewSessionKeys(relayer, builder),
		approvals:   pmadapter.NewApprovals(data, rpc, relayer, builder),
		geoblock:    pmadapter.NewGeoblock(geo),
		exchange:    pmadapter.NewExchange(clob, clob),
		orders:      pmadapter.NewOrderStream(pm.NewUserStreamClient(a.util, pm.UserStreamConfig{})),
	}, nil
}

// initEngine builds the workflow engine. Workflows run under ctx: cancelling it stops
// them, and the supervisor is also stopped on shutdown, before the database closes.
func (a *app) initEngine(ctx context.Context, pmc polymarket) error {
	streamClient, err := pm.NewMarketStreamClient(a.util, pm.StreamConfig{})
	if err != nil {
		return err
	}

	current := a.settings.Current()

	// User code compiles here and runs with the SDK, which every run loads first. Made
	// before the supervisor, the runner closes after: shutdown goes in reverse, so
	// workflows have stopped by then.
	compiler, err := tscompiler.New(a.util)
	if err != nil {
		return err
	}

	sdk, err := tscompiler.SDK()
	if err != nil {
		return err
	}

	// The sandbox, for plugins and code runners, or why there is none (D338, D354).
	a.launcher, a.noSandbox = sandbox.New()

	runner, err := a.codeRunner(ctx, sdk, current)
	if err != nil {
		return err
	}

	a.code = code.New(compiler, runner, code.Data{
		Markets: pmc.reader, Events: pmc.reader, OrderBooks: pmc.reader, Wallets: a.wallets, Positions: pmc.reader,
	})

	// One market stream for the whole process; serve runs it.
	a.feed, err = feed.NewHub(a.util, pmadapter.NewStream(streamClient), feed.WithMaxTokens(current.Int(config.KeyFeedMaxTokens)))
	if err != nil {
		return err
	}

	// Every trade on Polymarket, while anyone follows a trader, with the Data API
	// filling gaps (D87); serve runs it too.
	activityClient, err := pm.NewActivityStreamClient(a.util, pm.ActivityStreamConfig{})
	if err != nil {
		return err
	}

	a.traders, err = traders.NewHub(a.util, pmadapter.NewActivity(activityClient), pmc.reader, pmc.reader, traders.Config{})
	if err != nil {
		return err
	}

	a.registry = engine.NewRegistry()

	// The vault reads credential types from the registry, which nodes.Register fills.
	a.credentials = credentials.New(a.registry, postgres.NewCredentials(a.db), a.box, a.util.Logger)

	allowPrivate := func() bool { return a.settings.Current().Bool(config.KeyWebhookAllowPrivate) }

	webhooks, err := webhook.New(a.util, webhook.Config{AllowPrivate: allowPrivate})
	if err != nil {
		return err
	}

	// Language models, each call with its user's own key (D243); an OpenAI-compatible base
	// URL keeps to public addresses as webhooks do.
	llmClient, err := llm.New(a.util, llm.Config{AllowPrivate: allowPrivate})
	if err != nil {
		return err
	}

	// The models a key can use, for the agent node's picker (D251); what the agent spends
	// with them, counted per user and month (D268).
	a.models = models.New(models.Deps{Credentials: a.credentials, LLM: llmClient})
	a.spend = spend.New(spend.Deps{
		Usage: postgres.NewAgentUsage(a.db), Users: postgres.NewUsers(a.db), Notices: a.notify, Logger: a.util.Logger,
		Caps: a.agentCaps,
	})

	// Orders: on paper, simulated against the live book with a paper account per workflow
	// (D89 to D92); live, from the workflow's wallet through Polymarket's CLOB, while an
	// admin lets workflows trade live (D192). Place order places them; the books are shown
	// to their owner, whom the workflows service, made later, knows.
	ledger := postgres.NewLedger(a.db)
	paperBalance := func() decimal.Decimal {
		return decimal.NewFromInt(int64(a.settings.Current().Int(config.KeyPaperBalance)))
	}
	a.nodeMemory = postgres.NewNodeMemory(a.db)

	// Tokens' and agents' live actions wait for their users where they say so (D406).
	a.confirmations = confirmations.New(postgres.NewConfirmations(a.db), postgres.NewUsers(a.db), a.util.Logger,
		confirmations.WithExpiry(func() time.Duration { return a.settings.Current().Duration(config.KeyMCPConfirmFor) }),
		confirmations.WithNotifier(a.notify))
	a.util.OnShutdown(a.confirmations.Wait)

	a.trading = trading.New(trading.Deps{
		Ledger: ledger, Books: pmc.reader, Rules: pmc.reader, Markets: pmc.reader,
		Workflows: laterWorkflows{a}, Logger: a.util.Logger, PaperBalance: paperBalance,
		Trading: a.workflowTrading, Exchange: pmc.exchange, Keys: a.wallets, Wallets: a.wallets, Notifier: a.notify,
		Feed: a.feed, Stream: pmc.orders, Confirm: a.confirmations,
		LiveOn:      func() bool { return a.settings.Current().Bool(config.KeyLiveTrading) },
		BuilderCode: func() string { return a.settings.Current().String(config.KeyBuilderCode) },
		Users:       postgres.NewUsers(a.db),
		Notices:     a.notify,
		Caps:        a.liveCaps,
	})

	a.taskAgents = tasks.NewPresence(func() time.Duration { return a.settings.Current().Duration(config.KeyMCPTaskSeen) }, time.Now)

	err = nodes.Register(a.registry, nodes.Deps{
		Markets:       pmc.reader,
		Events:        pmc.reader,
		OrderBooks:    pmc.reader,
		PriceHistory:  pmc.reader,
		Trades:        pmc.reader,
		Positions:     pmc.reader,
		Account:       pmc.account,
		Wallets:       a.wallets,
		Webhooks:      webhooks,
		Credentials:   a.credentials,
		Messenger:     a.telegram,
		TelegramInbox: a.telegram,
		Asker:         a.telegram,
		Reminder:      a.notify,
		Code:          a.code,
		LLM:           llmClient,
		Spend:         a.spend,
		TaskAgents:    a.taskAgents,
		Feed:          a.feed,
		Traders:       a.traders,
		Orders:        a.trading,
		Books:         ledger,
		Accounts:      a.trading,
		Canceller:     a.trading,
		Batches:       a.trading,
		OrderEvents:   a.trading,
		TradingRules:  pmc.reader,
		Portfolios:    pmc.reader,
		Memory:        a.nodeMemory,
		Logger:        a.util.Logger,
		MinInterval:   current.Duration(config.KeyScheduleMinInterval),
	})
	if err != nil {
		return err
	}

	if err := a.initPlugins(ctx, pmc, allowPrivate); err != nil {
		return err
	}

	executions := postgres.NewExecutions(a.db)

	if a.events, err = events.NewHub(a.util); err != nil {
		return err
	}

	// A task kept wakes its owner's waiting agents (D386).
	waitRecords := tasks.Announcing(postgres.NewWaits(a.db), a.taskAgents)
	codeGate := auth.CodeGate{
		Users: postgres.NewUsers(a.db),
		Mode:  func() domain.CodeAccess { return codeAccess(a.settings.Current()) },
	}
	a.codeGate = codeGate

	// Each owner's workflows use the built-in types and their own custom nodes.
	a.customNodes = customnodes.New(postgres.NewCustomNodes(a.db), postgres.NewWorkflows(a.db), a.registry,
		a.code, compiler, codeGate, a.util.Logger,
		customnodes.WithLiveLimits(func() customnodes.Limits { return customNodeLimits(a.settings.Current()) }))

	executor, err := engine.NewExecutor(a.util, a.registry,
		engine.WithRecorder(executions), engine.WithNotifier(a.events), engine.WithNotifier(a.notify),
		engine.WithEvaluator(a.code), engine.WithCodeGate(codeGate), engine.WithPluginGate(a.plugins), engine.WithCatalog(a.customNodes),
		engine.WithWaits(waitRecords, func() engine.WaitLimits { return waitLimits(a.settings.Current()) }))
	if err != nil {
		return err
	}

	// Executions that wait carry on from here, which serve runs.
	a.waits = waits.New(waitRecords, executor, a.util.Logger)
	a.tasks = tasks.New(waitRecords, a.waits, a.taskAgents, func() tasks.Config {
		cur := a.settings.Current()
		return tasks.Config{Wait: cur.Duration(config.KeyMCPTaskWait), Lease: cur.Duration(config.KeyMCPTaskLease)}
	})

	a.supervisor = engine.NewSupervisor(ctx, engine.NewRunner(a.util, a.customNodes, executor), a.util.Logger)
	a.util.OnShutdown(a.supervisor.Shutdown)

	checks := workflowChecks{types: a.customNodes, compiler: compiler, gate: codeGate, plugins: a.plugins}

	a.workflows = workflows.New(postgres.NewWorkflows(a.db), executions, checks, executor, a.supervisor, a.util.Logger,
		workflows.WithLiveLimits(func() workflows.Limits { return workflowLimits(a.settings.Current()) }),
		workflows.WithWaits(a.waits), workflows.WithFieldChecks(checks),
		workflows.WithExpressionChecks(checks), workflows.WithCodeAccess(checks), workflows.WithNodeMemory(a.nodeMemory),
		workflows.WithPluginAccess(checks), workflows.WithPauser(a.notify),
		workflows.WithRestingOrders(a.trading),
		// Personal settings hidden from tokens and agents but where their user lets them see
		// (D402): the node types' sensitive ones.
		workflows.WithPersonal(paramsWhere(a.registry, func(p engine.ParamSpec) bool { return p.Sensitive })),
		// And they use credentials only where their user lets them (D404).
		workflows.WithCredentials(paramsWhere(a.registry, func(p engine.ParamSpec) bool { return p.Kind == engine.ParamCredential })),
		// A workflow that places orders needs its mode's scope too (D399).
		workflows.WithTradeScope(func(ctx context.Context, w domain.Workflow) (domain.Scope, error) {
			if !nodes.PlacesOrders(a.registry, w) {
				return "", nil
			}

			mode, err := a.trading.Mode(ctx, w.ID)

			return mode.Scope(), err
		}),
		workflows.WithConfirmations(a.confirmations))
	a.memory = memory.New(a.nodeMemory, a.workflows, a.util.Logger)
	a.handleConfirmed()

	if a.library, err = library.New(postgres.NewTemplates(a.db), a.workflows, a.registry, a.util.Logger,
		library.WithOwnTypes(a.customNodes), library.WithCustomNodes(a.customNodes),
		library.WithScenarios(a.pluginRuntime.Scenarios())); err != nil {
		return err
	}

	return nil
}
