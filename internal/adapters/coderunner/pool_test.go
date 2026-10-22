package coderunner_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/coderunner"
	"github.com/lein3000zzz/polythought-go/internal/adapters/jsruntime"
	"github.com/lein3000zzz/polythought-go/internal/adapters/sandbox"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// launcher is the sandbox, where the kernel has one: the runners run under it there, as
// the server's do, and without one elsewhere (D354).
var launcher *sandbox.Launcher

const prelude = `globalThis.answer = 42`

func TestMain(m *testing.M) {
	// This test binary is a runner too, as the server's binary is.
	sandbox.Launch(coderunner.Entry)

	launcher, _ = sandbox.New()

	os.Exit(m.Run())
}

func pool(t *testing.T, cfg coderunner.Config) *coderunner.Pool {
	t.Helper()

	cfg.Sandbox, cfg.Prelude = launcher, prelude

	p, err := coderunner.New(context.Background(), observability.NoopUtility(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close(context.Background()) })

	return p
}

func echo(_ context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	if name == "fail" {
		return nil, errors.New("no markets today")
	}

	return json.Marshal(map[string]any{"name": name, "args": args})
}

// A job runs in a runner as it would in the server: what it gives, what it logs, its host
// calls, and how it fails, each the same (D353).
func TestSameAsInTheServer(t *testing.T) {
	ctx := context.Background()

	inServer, err := jsruntime.New(ctx, observability.NoopUtility(), jsruntime.Config{Concurrency: 2, Prelude: prelude})
	require.NoError(t, err)
	t.Cleanup(func() { _ = inServer.Close(ctx) })

	p := pool(t, coderunner.Config{Size: 2})
	assert.Equal(t, launcher != nil, p.Sandboxed())

	for name, j := range map[string]ports.CodeJob{
		"what it gives, and logs": {
			Source: `function run(x) { console.log("seen", x); console.warn("careful"); return x + answer }`,
			Entry:  "run", Input: json.RawMessage(`1`),
		},
		"a promise it returns": {Source: `async function run() { return await Promise.resolve("later") }`, Entry: "run"},
		"its host calls, answered or thrown": {
			Source: `function run() {
				const a = __host("markets.search", {q: "election", limit: 2})
				let b
				try { __host("fail") } catch (e) { b = e.message }
				return {a, b}
			}`,
			Entry: "run", Host: echo,
		},
		"none to make":     {Source: `function run() { __host("markets.search") }`, Entry: "run"},
		"too many":         {Source: `function run() { for (let i = 0; i < 10; i++) try { __host("x", i) } catch {} }`, Entry: "run", Host: echo, Limits: ports.CodeLimits{HostCalls: 3}},
		"it throws":        {Source: `function run() { throw new TypeError("not that") }`, Entry: "run"},
		"no such function": {Source: `function other() {}`, Entry: "run"},
		"past its time":    {Source: `function run() { for (;;) {} }`, Entry: "run", Limits: ports.CodeLimits{Time: 200 * time.Millisecond}},
		"past its memory":  {Source: `function run() { const a = []; for (;;) a.push("x".repeat(1000)) }`, Entry: "run", Limits: ports.CodeLimits{Memory: 8 << 20}},
		"past its output":  {Source: `function run() { return "x".repeat(1000) }`, Entry: "run", Limits: ports.CodeLimits{Output: 100}},
		"never settles":    {Source: `function run() { return new Promise(() => {}) }`, Entry: "run"},
	} {
		t.Run(name, func(t *testing.T) {
			want, wantErr := inServer.Run(ctx, j)
			got, gotErr := p.Run(ctx, j)

			assert.Equal(t, string(want.Output), string(got.Output))
			assert.Equal(t, want.Logs, got.Logs)
			assert.Equal(t, want.HostCalls, got.HostCalls)

			if wantErr == nil {
				require.NoError(t, gotErr)
				return
			}

			var w, g *domain.CodeError
			require.ErrorAs(t, wantErr, &w)
			require.ErrorAs(t, gotErr, &g)
			assert.Equal(t, w, g)
		})
	}
}

// A runner that stops fails only its own run, and is started again for the next (D353).
func TestCrash(t *testing.T) {
	ctx := context.Background()
	p := pool(t, coderunner.Config{Size: 1})

	kill := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		for _, pid := range p.Pids() {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}

		time.Sleep(50 * time.Millisecond) // for it to be gone

		return json.RawMessage(`null`), nil
	}

	_, err := p.Run(ctx, ports.CodeJob{Source: `function run() { __host("die") }`, Entry: "run", Host: kill})

	var ce *domain.CodeError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, domain.CodeCrashed, ce.Kind)
	assert.Contains(t, ce.Message, "the code's runner stopped")

	res, err := p.Run(ctx, ports.CodeJob{Source: `function run() { return answer }`, Entry: "run"})
	require.NoError(t, err, "started again")
	assert.Equal(t, "42", string(res.Output))
	assert.Equal(t, 1, p.Restarts())
}

// A run given up on stops its runner, which is started again for the next.
func TestGivenUp(t *testing.T) {
	p := pool(t, coderunner.Config{Size: 1})

	ctx, cancel := context.WithCancel(context.Background())

	wait := func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		cancel()
		<-ctx.Done()

		return nil, ctx.Err()
	}

	_, err := p.Run(ctx, ports.CodeJob{Source: `function run() { __host("wait") }`, Entry: "run", Host: wait})
	require.ErrorIs(t, err, context.Canceled)

	res, err := p.Run(context.Background(), ports.CodeJob{Source: `function run() { return answer }`, Entry: "run"})
	require.NoError(t, err)
	assert.Equal(t, "42", string(res.Output))
}

// No owner holds more than half the runners (R12): their next run waits, others' do not.
func TestShares(t *testing.T) {
	p := pool(t, coderunner.Config{Size: 2})

	var (
		hold    = make(chan struct{})
		started = make(chan domain.UserID, 3)
		wg      sync.WaitGroup
	)

	host := func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		started <- ports.CodeOwner(ctx)
		<-hold

		return json.RawMessage(`null`), nil
	}

	for _, owner := range []domain.UserID{"alice", "alice", "bob"} {
		wg.Go(func() {
			_, err := p.Run(ports.WithCodeOwner(context.Background(), owner), ports.CodeJob{
				Source: `function run() { __host("hold") }`, Entry: "run", Host: host,
			})
			assert.NoError(t, err)
		})
	}

	first := []domain.UserID{<-started, <-started}
	assert.ElementsMatch(t, []domain.UserID{"alice", "bob"}, first, "one of alice's, and bob's")

	select {
	case owner := <-started:
		t.Fatalf("%s ran past their share", owner)
	case <-time.After(200 * time.Millisecond):
	}

	close(hold)
	assert.Equal(t, domain.UserID("alice"), <-started, "once one of hers is done")
	wg.Wait()
}

// A runner past its memory, which the engine's own limit would not catch, is stopped as
// it runs, and started again for the next run (D353).
func TestMemoryWatched(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("memory is watched on Linux")
	}

	p := pool(t, coderunner.Config{Size: 1, Watch: 10 * time.Millisecond, Headroom: 1})

	_, err := p.Run(context.Background(), ports.CodeJob{
		Source: `function run() { for (;;) {} }`, Entry: "run", Limits: ports.CodeLimits{Memory: 1 << 20, Time: 5 * time.Second},
	})

	var ce *domain.CodeError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, domain.CodeOutOfMemory, ce.Kind)
	assert.Equal(t, "the code needed more than the 1 MiB of memory it may use", ce.Message)
}

// Runners that do not start refuse the pool, saying why: a prelude that does not compile.
func TestNotStarted(t *testing.T) {
	_, err := coderunner.New(context.Background(), observability.NoopUtility(), coderunner.Config{
		Size: 1, Sandbox: launcher, Prelude: `const = 1`,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "its engine: jsruntime: the prelude does not compile")
}
