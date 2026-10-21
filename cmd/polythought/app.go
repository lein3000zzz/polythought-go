package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging/slogging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/prommetrics"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing/oteltracing"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/adapters/crypto"
	"github.com/lein3000zzz/polythought-go/internal/adapters/jsruntime"
	"github.com/lein3000zzz/polythought-go/internal/adapters/llm"
	"github.com/lein3000zzz/polythought-go/internal/adapters/mailer"
	pmadapter "github.com/lein3000zzz/polythought-go/internal/adapters/polymarket"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/adapters/ratelimit"
	tgadapter "github.com/lein3000zzz/polythought-go/internal/adapters/telegram"
	"github.com/lein3000zzz/polythought-go/internal/adapters/tscompiler"
	"github.com/lein3000zzz/polythought-go/internal/adapters/webhook"
	"github.com/lein3000zzz/polythought-go/internal/config"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/code"
	"github.com/lein3000zzz/polythought-go/internal/core/services/credentials"
	"github.com/lein3000zzz/polythought-go/internal/core/services/customnodes"
	"github.com/lein3000zzz/polythought-go/internal/core/services/events"
	"github.com/lein3000zzz/polythought-go/internal/core/services/feed"
	"github.com/lein3000zzz/polythought-go/internal/core/services/geoblock"
	"github.com/lein3000zzz/polythought-go/internal/core/services/library"
	"github.com/lein3000zzz/polythought-go/internal/core/services/mail"
	"github.com/lein3000zzz/polythought-go/internal/core/services/memory"
	"github.com/lein3000zzz/polythought-go/internal/core/services/notify"
	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
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
	workflowTrading *postgres.WorkflowTrading
	nodeMemory      *postgres.NodeMemory
	memory          *memory.Service
	events          *events.Hub
	library         *library.Service
	customNodes     *customnodes.Service
	credentials     *credentials.Service
	telegramBot     *tgadapter.Bot
	telegram        *telegram.Service
	notify          *notify.Service
	mail            *mail.Service
	geoblock        *geoblock.Service
	waits           *waits.Service
	code            *code.Service
	codeGate        auth.CodeGate

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
		notify.WithMail(a.mail), notify.WithMarkets(pmc.reader))

	// Whether Polymarket takes orders from here (D186); serve checks it every 6 hours.
	a.geoblock = geoblock.New(pmc.geoblock, a.util.Logger)

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

	a.auth, err = auth.New(authConfig(a.settings.Current()), auth.Repositories{
		Users:       postgres.NewUsers(a.db),
		Sessions:    postgres.NewSessions(a.db),
		Tokens:      postgres.NewAPITokens(a.db),
		Invites:     postgres.NewInvites(a.db),
		EmailTokens: postgres.NewEmailTokens(a.db),
		Tx:          a.db,
	},
		crypto.NewArgon2id(crypto.DefaultArgon2Params),
		ratelimit.NewMemory(30*time.Second, 10), // 10 attempts, then one every 30 s per key
		a.util.Logger,
		auth.WithWorkflowStopper(a.workflows),
		auth.WithLiveStopper(laterTrading{a}),
		auth.WithAccountLimiter(ratelimit.NewMemory(6*time.Second, 50)), // per address, from every IP
		auth.WithLiveConfig(func() auth.Config { return authConfig(a.settings.Current()) }),
		auth.WithAccountMail(a.mail),
	)

	return err
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

	return cfg
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

// nodeTypes lists the node types a user may place: the built-in ones and their own.
type nodeTypes struct {
	types engine.Catalog
}

func (n nodeTypes) Types(ctx context.Context, p auth.Principal) ([]engine.TypeInfo, error) {
	reg, err := n.types.For(ctx, p.User.ID)
	if err != nil {
		return nil, err
	}

	return reg.Types(), nil
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

	runner, err := jsruntime.New(ctx, a.util, jsruntime.Config{
		Concurrency: current.Int(config.KeyCodeConcurrency),
		Prelude:     sdk,
		Limits:      func() ports.CodeLimits { return codeLimits(a.settings.Current()) },
	})
	if err != nil {
		return err
	}

	a.util.OnShutdown(runner.Close)

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
	models, err := llm.New(a.util, llm.Config{AllowPrivate: allowPrivate})
	if err != nil {
		return err
	}

	// Orders: on paper, simulated against the live book with a paper account per workflow
	// (D89 to D92); live, from the workflow's wallet through Polymarket's CLOB, while an
	// admin lets workflows trade live (D192). Place order places them; the books are shown
	// to their owner, whom the workflows service, made later, knows.
	ledger := postgres.NewLedger(a.db)
	paperBalance := func() decimal.Decimal {
		return decimal.NewFromInt(int64(a.settings.Current().Int(config.KeyPaperBalance)))
	}
	a.nodeMemory = postgres.NewNodeMemory(a.db)

	a.trading = trading.New(trading.Deps{
		Ledger: ledger, Books: pmc.reader, Rules: pmc.reader, Markets: pmc.reader,
		Workflows: laterWorkflows{a}, Logger: a.util.Logger, PaperBalance: paperBalance,
		Trading: a.workflowTrading, Exchange: pmc.exchange, Keys: a.wallets, Wallets: a.wallets, Notifier: a.notify,
		Feed: a.feed, Stream: pmc.orders,
		LiveOn:      func() bool { return a.settings.Current().Bool(config.KeyLiveTrading) },
		BuilderCode: func() string { return a.settings.Current().String(config.KeyBuilderCode) },
		Users:       postgres.NewUsers(a.db),
		Notices:     a.notify,
		Caps: func() domain.LiveCaps {
			s := a.settings.Current()
			perOrder, perDay := s.Int(config.KeyMaxLiveOrder), s.Int(config.KeyMaxLiveDay)

			return domain.LiveCaps{PerOrder: &perOrder, PerDay: &perDay}
		},
	})

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
		LLM:           models,
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

	executions := postgres.NewExecutions(a.db)

	if a.events, err = events.NewHub(a.util); err != nil {
		return err
	}

	waitRecords := postgres.NewWaits(a.db)
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
		engine.WithEvaluator(a.code), engine.WithCodeGate(codeGate), engine.WithCatalog(a.customNodes),
		engine.WithWaits(waitRecords, func() engine.WaitLimits { return waitLimits(a.settings.Current()) }))
	if err != nil {
		return err
	}

	// Executions that wait carry on from here, which serve runs.
	a.waits = waits.New(waitRecords, executor, a.util.Logger)

	a.supervisor = engine.NewSupervisor(ctx, engine.NewRunner(a.util, a.customNodes, executor), a.util.Logger)
	a.util.OnShutdown(a.supervisor.Shutdown)

	checks := workflowChecks{types: a.customNodes, compiler: compiler, gate: codeGate}

	a.workflows = workflows.New(postgres.NewWorkflows(a.db), executions, checks, executor, a.supervisor, a.util.Logger,
		workflows.WithLiveLimits(func() workflows.Limits { return workflowLimits(a.settings.Current()) }),
		workflows.WithWaits(a.waits), workflows.WithFieldChecks(checks),
		workflows.WithExpressionChecks(checks), workflows.WithCodeAccess(checks), workflows.WithNodeMemory(a.nodeMemory),
		workflows.WithRestingOrders(a.trading))
	a.memory = memory.New(a.nodeMemory, a.workflows, a.util.Logger)

	if a.library, err = library.New(postgres.NewTemplates(a.db), a.workflows, a.registry, a.util.Logger,
		library.WithOwnTypes(a.customNodes), library.WithCustomNodes(a.customNodes)); err != nil {
		return err
	}

	return nil
}
