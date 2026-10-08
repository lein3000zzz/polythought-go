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
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/adapters/ratelimit"
	"github.com/lein3000zzz/polythought-go/internal/config"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
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
	cfg  config.Config
	util *observability.Utility
	db   *postgres.DB
	auth *auth.Service
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

	// Parsed now so a bad key fails at start; secrets storage arrives with wallets.
	if _, err := crypto.ParseStaticKeys(a.cfg.SecretKeys); err != nil {
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
