package main

import (
	"context"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/adapters/plugindir"
	"github.com/lein3000zzz/polythought-go/internal/adapters/pluginhost"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/adapters/sandbox"
	"github.com/lein3000zzz/polythought-go/internal/config"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/plugins"
	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
)

// initPlugins loads the plugins admins installed and approved (5c), as the server starts
// (D317): kept in the plugins directory (D315), served the host API from the app's
// services (5b), supervised (D320), each under the sandbox (5d), or not started where there
// is none (D338). Their nodes join the registry before anything reads it. Who may use each
// applies at once; the rest at the next start.
func (a *app) initPlugins(ctx context.Context, pmc polymarket, allowPrivate func() bool) error {
	dir, err := plugindir.Open(a.settings.Current().String(config.KeyPluginsDir))
	if err != nil {
		return err
	}

	launcher, noSandbox := sandbox.New()
	if noSandbox != nil {
		a.util.Logger.Warn(ctx, "Plugins cannot be sandboxed here, so none starts", logging.Err(noSandbox))
	}

	files := plugindir.NewFiles(dir, func() []string { return a.settings.Current().Strings(config.KeyPluginTrustedKeys) })
	repo := postgres.NewPlugins(a.db)

	a.pluginRuntime = pluginhost.NewRuntime(ctx, pluginhost.Deps{
		Util:         a.util,
		Credentials:  a.credentials,
		Store:        postgres.NewPluginStore(a.db),
		AllowPrivate: allowPrivate,
		Limits:       func() pluginhost.Limits { return pluginLimits(a.settings.Current()) },
		Settings:     func(id string) map[string]any { return a.plugins.RunSettings(id) },
		Sandbox:      launcher,
		NoSandbox:    noSandbox,
		Data: pluginhost.Data{
			Events: pmc.reader, Books: pmc.reader, History: pmc.reader, Trades: pmc.reader, Positions: pmc.reader,
			Wallets: postgres.NewWallets(a.db),
		},
		Telegram: pluginhost.Telegram{Messenger: a.telegram, Asker: a.telegram, Reminder: a.notify},
		Trading:  pluginhost.Trading{Orders: a.trading, Batches: a.trading, Canceller: a.trading},
	}, a.registry, pluginhost.WithChecks(func(id, version string) error {
		_, err := files.Open(id, version)
		return err
	}, a.notify.PluginCrashed))

	a.util.OnShutdown(func(context.Context) error {
		a.pluginRuntime.Close()
		return nil
	})

	a.plugins = plugins.New(repo, files, a.pluginRuntime, laterPauser{a}, a.util.Logger,
		plugins.WithSettings(repo, a.box),
		plugins.WithFetcher(plugindir.NewFetcher(allowPrivate, nil)),
		plugins.WithNotifier(a.notify),
		plugins.WithLimits(func() plugins.Limits {
			return plugins.Limits{MaxArchive: int64(a.settings.Current().Int(config.KeyPluginMaxArchive)) << 20}
		}))

	return a.plugins.Load(ctx)
}

// pluginLimits are what one run of a plugin may ask of the host (D293).
func pluginLimits(s *settings.Snapshot) pluginhost.Limits {
	return pluginhost.Limits{
		HostCalls:  s.Int(config.KeyCodeHostCalls),
		FetchTime:  s.Duration(config.KeyPluginFetchTime),
		FetchBody:  s.Int(config.KeyPluginFetchBody) << 10,
		StoreBytes: s.Int(config.KeyPluginStoreSize) << 10,
	}
}

// laterPauser pauses through the workflows service, made after the plugins load.
type laterPauser struct{ a *app }

func (l laterPauser) Pause(ctx context.Context, why func(domain.Workflow) string) (int, error) {
	return l.a.workflows.Pause(ctx, why)
}

// CheckPluginAccess refuses nodes of plugins a workflow's owner may not use (D282).
func (c workflowChecks) CheckPluginAccess(ctx context.Context, w domain.Workflow) error {
	reg, err := c.types.For(ctx, w.OwnerID)
	if err != nil {
		return err
	}

	return reg.CheckPluginAccess(ctx, w, c.plugins)
}
