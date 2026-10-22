package main

import (
	"context"
	"fmt"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/adapters/coderunner"
	"github.com/lein3000zzz/polythought-go/internal/adapters/jsruntime"
	"github.com/lein3000zzz/polythought-go/internal/config"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
)

// codeRunner is what runs user code, with the SDK as its prelude: the server's own engine,
// or runner processes where the setting says (D352), under the sandbox where there is one
// (D354). Runners that will not start leave the server running, failing every code run and
// saying why (D358). Either closes as the server stops.
func (a *app) codeRunner(ctx context.Context, prelude string, s *settings.Snapshot) (ports.CodeRunner, error) {
	limits := func() ports.CodeLimits { return codeLimits(a.settings.Current()) }

	if !s.Bool(config.KeyCodeRunners) {
		runner, err := jsruntime.New(ctx, a.util, jsruntime.Config{
			Concurrency: s.Int(config.KeyCodeConcurrency), Prelude: prelude, Limits: limits,
		})
		if err != nil {
			return nil, err
		}

		a.util.OnShutdown(runner.Close)
		a.codeRunners = func() ports.CodeRunners { return ports.CodeRunners{} }

		return runner, nil
	}

	why := ""
	if a.noSandbox != nil {
		why = a.noSandbox.Error()
		a.util.Logger.Warn(ctx, "Code runners cannot be sandboxed here: they run without it", logging.Err(a.noSandbox))
	}

	pool, err := coderunner.New(ctx, a.util, coderunner.Config{
		Size: s.Int(config.KeyCodeConcurrency), Prelude: prelude, Limits: limits, Sandbox: a.launcher,
	})
	if err != nil {
		a.util.Logger.Error(ctx, "Code runners would not start: code runs fail until they do", logging.Err(err))
		a.codeRunners = func() ports.CodeRunners {
			return ports.CodeRunners{InRunners: true, Sandboxed: a.launcher != nil, Why: why, Failed: err.Error()}
		}

		return failing{err: err}, nil
	}

	a.util.OnShutdown(pool.Close)
	a.codeRunners = func() ports.CodeRunners {
		st := pool.Status()
		st.Why = why

		return st
	}

	return pool, nil
}

// failing fails every code run, as the runner processes would not start (D358): code
// never runs in the server's process instead.
type failing struct{ err error }

func (f failing) Run(context.Context, ports.CodeJob) (ports.CodeResult, error) {
	return ports.CodeResult{}, fmt.Errorf("code does not run: the runner processes would not start: %w", f.err)
}
