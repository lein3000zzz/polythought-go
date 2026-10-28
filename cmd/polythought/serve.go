package main

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/buildinfo"
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
	build := buildinfo.Get()
	log.Info(ctx, "Polythought starting", "version", build.Version, "commit", build.Short(), "go", build.Go)

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

	mcp, err := a.newMCP()
	if err != nil {
		return err
	}

	a.mcp = mcp

	// Its sessions stay kept as the server stops, for their agents to come back to (D414).
	a.util.OnShutdown(mcp.Shutdown)

	srv, err := httpapi.New(httpapi.Config{
		Addr:           a.cfg.HTTPAddr,
		PublicURL:      a.cfg.PublicURL,
		TrustedProxies: proxies,
		Build:          build,
	}, a.util, httpapi.Services{
		Auth: a.auth, Wallets: a.wallets, Workflows: a.workflows, NodeTypes: nodeTypes{types: a.customNodes, plugins: a.plugins}, Events: a.events,
		CodeTypes: codeTypes{types: a.customNodes}, WorkflowCode: workflowCode{code: a.code, gate: a.codeGate},
		CustomNodes: a.customNodes, Library: a.library, Settings: a.settings, Credentials: a.credentials, Models: a.models, Spend: a.spend,
		Trading:  a.trading,
		Memory:   a.memory,
		Telegram: a.telegram, TelegramWebhook: a.telegramBot, Notifications: a.notify, Mail: a.mail,
		Geoblock: a.geoblock, Tasks: a.tasks, TaskAgents: a.taskAgents,
		Meta:    a.meta,
		Updates: a.updates,
		Stats:   a.stats,
		OAuth:   a.oauth, Confirmations: a.confirmations,
		MCP:     mcp,
		Plugins: a.plugins,
		AgentSetup: func() httpapi.AgentSetup {
			cur := a.settings.Current()
			return httpapi.AgentSetup{
				Enabled: cur.Bool(config.KeyMCPEnabled), Address: cur.String(config.KeyMCPAddress), Clients: cur.Strings(config.KeyMCPClients),
			}
		},
		AgentSessions: mcp,

		CodeRunners: a.codeRunners,
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

	go func() {
		if err := a.traders.Run(ctx); err != nil {
			log.Error(ctx, "Trader feed stopped", logging.Err(err))
		}
	}()

	// Paper holdings of resolved markets settle on their own (D106).
	go a.trading.SettleEvery(ctx, 5*time.Minute)

	// Live orders still settling: pending ones, and trades not final (D193, D204).
	go a.trading.FollowEvery(ctx, 5*time.Second)

	// Orders resting on the book (3p3a): paper ones filled as the market trades through
	// them (D209), live ones read as their wallet's user channel says they changed (D195).
	go a.trading.RestEvery(ctx, 5*time.Second)
	go a.trading.WatchLive(ctx, 30*time.Second)

	// Each trading workflow's highest and lowest P&L, sampled (D430).
	go a.stats.SampleEvery(ctx, func() time.Duration { return a.settings.Current().Duration(config.KeyPnLSampleEvery) })

	// What nodes remember goes when it expires (D121).
	go a.memory.SweepEvery(ctx, 10*time.Minute)

	// Session keys and approval setups submitted are followed until confirmed; session
	// keys' owners are reminded before they expire (D156, D161).
	go a.wallets.FollowEvery(ctx, 5*time.Second, time.Hour)

	// Whether Polymarket takes orders from here, at start and every 6 hours (D186).
	go a.geoblock.CheckEvery(ctx, 6*time.Hour)

	// GitHub's star count, at start and about once an hour while it is on (D372).
	go a.meta.LookEvery(ctx, time.Minute)

	// Polythought's newest release, at start and once a day while the check is on (D424).
	go a.updates.CheckEvery(ctx, time.Minute)

	// The bot follows its settings: a change restarts it, not the server.
	telegramDone := make(chan struct{})

	go func() {
		defer close(telegramDone)
		a.telegramBot.Run(ctx)
	}()

	a.telegramBot.Configure(a.telegramConfig(a.settings.Current()))
	go a.notify.Run(ctx)

	// Waits come due here, also the ones that did while the server was down.
	waitsDone := make(chan struct{})

	go func() {
		defer close(waitsDone)
		a.waits.Run(ctx)
	}()
	a.settings.Subscribe(func(s *settings.Snapshot) { a.telegramBot.Configure(a.telegramConfig(s)) })

	// Live trading turned off cancels every live order resting on the book (D226).
	var liveOn atomic.Bool

	liveOn.Store(a.settings.Current().Bool(config.KeyLiveTrading))
	a.settings.Subscribe(func(s *settings.Snapshot) {
		if on := s.Bool(config.KeyLiveTrading); liveOn.Swap(on) && !on {
			go func() {
				if _, err := a.trading.CancelAllLive(ctx); err != nil {
					log.Error(ctx, "Live trading off, but not every resting live order cancelled", logging.Err(err))
				}
			}()
		}
	})

	// The workflows plugins gone leave unable to run are paused before the rest start (D319).
	if err := a.plugins.PauseGone(ctx); err != nil {
		return err
	}

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

	// Executions it carried on are cancelled with ctx; they record that before it returns.
	select {
	case <-waitsDone:
	case <-time.After(5 * time.Second):
		log.Warn(context.Background(), "Waiting executions slow to stop")
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

			// Accounts unverified after 7 days go (D174), with links expired.
			if n, err := a.auth.PruneUnverified(ctx); err != nil {
				log.Warn(ctx, "Unverified accounts not pruned", logging.Err(err))
			} else if n > 0 {
				log.Info(ctx, "Unverified accounts removed", "count", n)
			}

			// Agents' ended codes and tokens, and clients that never got a grant (D397).
			if _, err := a.oauth.Prune(ctx); err != nil {
				log.Warn(ctx, "OAuth codes and tokens not pruned", logging.Err(err))
			}
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
