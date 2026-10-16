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

	"github.com/lein3000zzz/polythought-go/internal/adapters/crypto"
	"github.com/lein3000zzz/polythought-go/internal/adapters/jsruntime"
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
	"github.com/lein3000zzz/polythought-go/internal/core/services/events"
	"github.com/lein3000zzz/polythought-go/internal/core/services/feed"
	"github.com/lein3000zzz/polythought-go/internal/core/services/library"
	"github.com/lein3000zzz/polythought-go/internal/core/services/notify"
	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
	"github.com/lein3000zzz/polythought-go/internal/core/services/telegram"
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
	cfg         config.Config
	util        *observability.Utility
	db          *postgres.DB
	box         ports.SecretBox
	settings    *settings.Service
	auth        *auth.Service
	wallets     *wallets.Service
	workflows   *workflows.Service
	registry    *engine.Registry
	supervisor  *engine.Supervisor
	feed        *feed.Hub
	events      *events.Hub
	library     *library.Service
	credentials *credentials.Service
	telegramBot *tgadapter.Bot
	telegram    *telegram.Service
	notify      *notify.Service
	waits       *waits.Service
	code        *code.Service

	restartOnce sync.Once
	restartc    chan struct{} // closed when an admin asked for a restart
}

func newApp(ctx context.Context) (*app, error) {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return nil, fmt.Errorf("config:\n%w", err)
	}

	// The database comes first: the log settings are stored in it.
	db, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}

	a := &app{cfg: cfg, db: db, restartc: make(chan struct{})}

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

	a.wallets = wallets.New(postgres.NewWallets(a.db), a.box, pmc.account, a.util.Logger)

	// Before the engine: the Telegram nodes use the bot, and failures are notified.
	// The bot and the notifications run only in serve, which starts them.
	if a.telegramBot, err = tgadapter.New(a.util); err != nil {
		return err
	}

	a.telegram = telegram.New(a.telegramBot, postgres.NewTelegramLinks(a.db), postgres.NewUsers(a.db), a.util.Logger)
	a.telegramBot.SetHandler(a.telegram.HandleUpdate)
	a.notify = notify.New(a.telegram, postgres.NewNotificationPrefs(a.db), postgres.NewWorkflows(a.db), a.cfg.PublicURL, a.util.Logger)

	if err := a.initEngine(ctx, pmc); err != nil {
		return err
	}

	a.telegram.UseMenu(telegram.Menu{
		Workflows: a.workflows, Wallets: a.wallets, Positions: pmc.reader, Notifications: a.notify,
	})
	a.telegram.UseAnswers(a.waits)

	a.auth, err = auth.New(authConfig(a.settings.Current()), auth.Repositories{
		Users:    postgres.NewUsers(a.db),
		Sessions: postgres.NewSessions(a.db),
		Tokens:   postgres.NewAPITokens(a.db),
		Invites:  postgres.NewInvites(a.db),
		Tx:       a.db,
	},
		crypto.NewArgon2id(crypto.DefaultArgon2Params),
		ratelimit.NewMemory(30*time.Second, 10), // 10 attempts, then one every 30 s per key
		a.util.Logger,
		auth.WithWorkflowStopper(a.workflows),
		auth.WithLiveConfig(func() auth.Config { return authConfig(a.settings.Current()) }),
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

	warnings, err := a.settings.Load(ctx, os.Getenv)
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

	return cfg
}

func waitLimits(s *settings.Snapshot) engine.WaitLimits {
	return engine.WaitLimits{
		MaxWaiting: s.Int(config.KeyMaxWaiting),
		MaxWait:    s.Duration(config.KeyMaxWait),
	}
}

// expressionChecker checks a workflow's expressions and code as saving does: each
// compiled.
type expressionChecker struct {
	registry *engine.Registry
	compiler ports.CodeCompiler
}

func (c expressionChecker) CheckExpressions(w domain.Workflow) error {
	var problems domain.ValidationError

	problems.Merge(c.registry.CheckExpressions(w, func(expr string) error {
		_, err := c.compiler.CompileExpression(context.Background(), expr)
		return err
	}))

	problems.Merge(c.registry.CheckCode(w, func(source string) error {
		_, err := c.compiler.Compile(context.Background(), source)
		return err
	}))

	return problems.Err()
}

// codeTypes declares what code is written against: the SDK and the node types, the same
// for every user until custom nodes come.
type codeTypes struct {
	registry *engine.Registry
}

func (c codeTypes) Declarations(context.Context, auth.Principal) []ports.CodeFile {
	return tscompiler.Declarations(c.registry.Types())
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
	reader  *pmadapter.Reader
	account *pmadapter.Account
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

	return polymarket{reader: pmadapter.NewReader(gamma, clob, data), account: pmadapter.NewAccount(clob)}, nil
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

	a.registry = engine.NewRegistry()

	// The vault reads credential types from the registry, which nodes.Register fills.
	a.credentials = credentials.New(a.registry, postgres.NewCredentials(a.db), a.box, a.util.Logger)

	webhooks, err := webhook.New(a.util, webhook.Config{
		AllowPrivate: func() bool { return a.settings.Current().Bool(config.KeyWebhookAllowPrivate) },
	})
	if err != nil {
		return err
	}

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
		Feed:          a.feed,
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

	executor, err := engine.NewExecutor(a.util, a.registry,
		engine.WithRecorder(executions), engine.WithNotifier(a.events), engine.WithNotifier(a.notify),
		engine.WithEvaluator(a.code),
		engine.WithWaits(waitRecords, func() engine.WaitLimits { return waitLimits(a.settings.Current()) }))
	if err != nil {
		return err
	}

	// Executions that wait carry on from here, which serve runs.
	a.waits = waits.New(waitRecords, executor, a.util.Logger)

	a.supervisor = engine.NewSupervisor(ctx, engine.NewRunner(a.util, a.registry, executor), a.util.Logger)
	a.util.OnShutdown(a.supervisor.Shutdown)

	a.workflows = workflows.New(postgres.NewWorkflows(a.db), executions, a.registry, executor, a.supervisor, a.util.Logger,
		workflows.WithLiveLimits(func() workflows.Limits { return workflowLimits(a.settings.Current()) }),
		workflows.WithWaits(a.waits), workflows.WithFieldChecks(a.registry),
		workflows.WithExpressionChecks(expressionChecker{registry: a.registry, compiler: compiler}))

	if a.library, err = library.New(postgres.NewTemplates(a.db), a.workflows, a.registry, a.util.Logger); err != nil {
		return err
	}

	return nil
}
