package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging/slogging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/prommetrics"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing/oteltracing"

	"github.com/lein3000zzz/polythought-go/internal/adapters/crypto"
	pmadapter "github.com/lein3000zzz/polythought-go/internal/adapters/polymarket"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/adapters/ratelimit"
	"github.com/lein3000zzz/polythought-go/internal/config"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
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

// newUtility sets up logging, metrics and tracing. Call its Shutdown before exiting.
func newUtility(level, format string) (*observability.Utility, error) {
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
		return nil, err
	}

	oteltracing.InstallPropagator()

	return observability.NewUtility(
		log,
		prommetrics.DefaultManager(),
		oteltracing.DefaultOtelManager(),
		func(context.Context) error { return log.Close() },
	), nil
}

func shutdown(util *observability.Utility) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := util.Shutdown(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "polythought: shutdown:", err)
	}
}

// app is everything the server and the operator tools share.
type app struct {
	cfg        config.Config
	util       *observability.Utility
	db         *postgres.DB
	auth       *auth.Service
	wallets    *wallets.Service
	workflows  *workflows.Service
	registry   *engine.Registry
	supervisor *engine.Supervisor
}

func newApp(ctx context.Context) (*app, error) {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return nil, fmt.Errorf("config:\n%w", err)
	}

	util, err := newUtility(cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		return nil, err
	}

	a := &app{cfg: cfg, util: util}

	if err := a.init(ctx); err != nil {
		shutdown(util)
		return nil, err
	}

	return a, nil
}

func (a *app) init(ctx context.Context) error {
	db, err := postgres.Open(ctx, a.cfg.DatabaseURL)
	if err != nil {
		return err
	}

	a.db = db
	a.util.OnShutdown(db.Close)

	if a.cfg.AutoMigrate {
		applied, err := db.Migrate(ctx)
		if err != nil {
			return err
		}

		if len(applied) > 0 {
			a.util.Logger.Info(ctx, "Migrations applied", "versions", applied)
		}
	}

	keys, err := crypto.ParseStaticKeys(a.cfg.SecretKeys)
	if err != nil {
		return err
	}

	pmc, err := newPolymarket(a)
	if err != nil {
		return err
	}

	a.wallets = wallets.New(postgres.NewWallets(db), crypto.NewEnvelope(keys), pmc.account, a.util.Logger)

	if err := a.initEngine(ctx, pmc); err != nil {
		return err
	}

	authCfg := auth.DefaultConfig()
	authCfg.Registration = a.cfg.Registration
	authCfg.SessionIdleTTL = a.cfg.SessionIdleTTL
	authCfg.SessionAbsoluteTTL = a.cfg.SessionAbsoluteTTL

	a.auth, err = auth.New(authCfg, auth.Repositories{
		Users:    postgres.NewUsers(db),
		Sessions: postgres.NewSessions(db),
		Tokens:   postgres.NewAPITokens(db),
		Invites:  postgres.NewInvites(db),
		Tx:       db,
	},
		crypto.NewArgon2id(crypto.DefaultArgon2Params),
		ratelimit.NewMemory(30*time.Second, 10), // 10 attempts, then one every 30 s per key
		a.util.Logger,
	)

	return err
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
	a.registry = engine.NewRegistry()

	err := nodes.Register(a.registry, nodes.Deps{
		Markets:    pmc.reader,
		OrderBooks: pmc.reader,
		Positions:  pmc.reader,
		Account:    pmc.account,
		Wallets:    a.wallets,
		Logger:     a.util.Logger,
	})
	if err != nil {
		return err
	}

	executions := postgres.NewExecutions(a.db)

	executor, err := engine.NewExecutor(a.util, a.registry, engine.WithRecorder(executions))
	if err != nil {
		return err
	}

	a.supervisor = engine.NewSupervisor(ctx, engine.NewRunner(a.util, a.registry, executor), a.util.Logger)
	a.util.OnShutdown(a.supervisor.Shutdown)

	a.workflows = workflows.New(postgres.NewWorkflows(a.db), executions, a.registry, executor, a.supervisor, a.util.Logger)

	return nil
}
