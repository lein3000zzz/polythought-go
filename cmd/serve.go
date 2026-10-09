package main

import (
	"context"
	"errors"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
)

const sessionPruneInterval = time.Hour

func serve(ctx context.Context) error {
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

	srv, err := httpapi.New(httpapi.Config{
		Addr:           a.cfg.HTTPAddr,
		PublicURL:      a.cfg.PublicURL,
		TrustedProxies: a.cfg.TrustedProxies,
	}, a.util, httpapi.Services{Auth: a.auth, Wallets: a.wallets, Workflows: a.workflows, NodeTypes: a.registry})
	if err != nil {
		return err
	}

	if err := a.workflows.StartActive(ctx); err != nil {
		return err
	}

	go pruneSessions(ctx, a, log)
	go pruneExecutions(ctx, a, log)

	errc := make(chan error, 1)
	go func() { errc <- srv.Run() }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	log.Info(context.Background(), "Shutting down")

	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(stopCtx); err != nil && !errors.Is(err, context.Canceled) {
		return err
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
		n, err := a.workflows.PruneExecutions(ctx, a.cfg.ExecutionRetention)
		if err != nil {
			log.Warn(ctx, "Execution pruning failed", logging.Err(err))
			return
		}

		log.Info(ctx, "Old executions pruned", "count", n, "retention", a.cfg.ExecutionRetention)
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
