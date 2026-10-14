package main

import (
	"context"
	"errors"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/config"
	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
)

const sessionPruneInterval = time.Hour

// errRestart ends serve when an admin asked for a restart, once everything has shut down.
var errRestart = errors.New("restart requested")

func serve(ctx context.Context) error {
	// Cancelling stops workflows, the market feed and background jobs, for a signal and
	// for a restart alike.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	a, err := newApp(ctx)
	if err != nil {
		return err
	}
	defer shutdown(a.util)

	log := a.util.Logger

	if a.cfg.AdminEmail != "" {
		created, err := a.auth.EnsureAdmin(ctx, a.cfg.AdminEmail, a.cfg.AdminPassword)
		if err != nil {
			return err
		}

		if created {
			log.Info(ctx, "Admin created from environment", "email", a.cfg.AdminEmail)
		}
	}

	proxies, err := config.ParseCIDRs(a.settings.Current().Strings(config.KeyTrustedProxies))
	if err != nil {
		return err
	}

	srv, err := httpapi.New(httpapi.Config{
		Addr:           a.cfg.HTTPAddr,
		PublicURL:      a.cfg.PublicURL,
		TrustedProxies: proxies,
	}, a.util, httpapi.Services{
		Auth: a.auth, Wallets: a.wallets, Workflows: a.workflows, NodeTypes: a.registry, Events: a.events,
		Library: a.library, Settings: a.settings, Credentials: a.credentials,
		Telegram: a.telegram, TelegramWebhook: a.telegramBot,
	})
	if err != nil {
		return err
	}

	a.settings.Subscribe(func(s *settings.Snapshot) {
		// Saved values passed the same parsing, so this cannot fail.
		if proxies, err := config.ParseCIDRs(s.Strings(config.KeyTrustedProxies)); err == nil {
			srv.SetTrustedProxies(proxies)
		}
	})

	go func() {
		if err := a.feed.Run(ctx); err != nil {
			log.Error(ctx, "Market feed stopped", logging.Err(err))
		}
	}()

	// The bot follows its settings: a change restarts it, not the server.
	telegramDone := make(chan struct{})

	go func() {
		defer close(telegramDone)
		a.telegramBot.Run(ctx)
	}()

	a.telegramBot.Configure(a.telegramConfig(a.settings.Current()))
	a.settings.Subscribe(func(s *settings.Snapshot) { a.telegramBot.Configure(a.telegramConfig(s)) })

	if err := a.workflows.StartActive(ctx); err != nil {
		return err
	}

	go pruneSessions(ctx, a, log)
	go pruneExecutions(ctx, a, log)

	errc := make(chan error, 1)
	go func() { errc <- srv.Run() }()

	restart := false

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	case <-a.restartc:
		restart = true
	}

	log.Info(context.Background(), "Shutting down", "restart", restart)
	cancel()

	select {
	case <-telegramDone:
	case <-time.After(5 * time.Second):
		log.Warn(context.Background(), "Telegram bot slow to stop")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()

	if err := srv.Shutdown(stopCtx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	if restart {
		return errRestart
	}

	return nil
}

func pruneSessions(ctx context.Context, a *app, log logging.Logger) {
	ticker := time.NewTicker(sessionPruneInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := a.auth.PruneSessions(ctx)
			if err != nil {
				log.Warn(ctx, "Session pruning failed", logging.Err(err))
				continue
			}

			log.Debug(ctx, "Expired sessions pruned", "count", n)
		}
	}
}

const executionPruneInterval = 24 * time.Hour

func pruneExecutions(ctx context.Context, a *app, log logging.Logger) {
	prune := func() {
		retention := a.settings.Current().Duration(config.KeyExecutionRetention)

		n, err := a.workflows.PruneExecutions(ctx, retention)
		if err != nil {
			log.Warn(ctx, "Execution pruning failed", logging.Err(err))
			return
		}

		log.Info(ctx, "Old executions pruned", "count", n, "retention", retention)
	}

	prune()

	ticker := time.NewTicker(executionPruneInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}
