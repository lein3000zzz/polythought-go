package coderunner_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	// This test binary is a runner too, as the server's binary is, and a runner gone bad.
	sandbox.Launch(coderunner.Entry, coderunner.Rogue)

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
	ctx := ports.WithCodeOwner(context.Background(), "alice") // her runners, not started again for each job

	inServer, err := jsruntime.New(ctx, observability.NoopUtility(), jsruntime.Config{Concurrency: 2, Prelude: prelude})
	require.NoError(t, err)
	t.Cleanup(func() { _ = inServer.Close(ctx) })

	p := pool(t, coderunner.Config{Size: 2})
	assert.Equal(t, ports.CodeRunners{InRunners: true, Size: 2, Running: 2, Sandboxed: launcher != nil}, p.Status())

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
		"past its memory":  {Source: `function run() { const a = []; for (;;) a.push("x".repeat(1000)) }`, Entry: "run", Limits: ports.CodeLimits{Memory: 8 << 20, Time: time.Minute}}, // memory first, however slow the machine
		"past its output":  {Source: `function run() { return "x".repeat(1000) }`, Entry: "run", Limits: ports.CodeLimits{Output: 100}},
		"never settles":    {Source: `function run() { return new Promise(() => {}) }`, Entry: "run"},
		"as it gives it":   {Source: `function run() { return "<b>&amp;</b>".repeat(10) }`, Entry: "run", Limits: ports.CodeLimits{Output: 122}},
		"more logs than kept": {
			Source: `function run() { console.log("xé".repeat(1000)); for (let i = 0; i < 120; i++) console.warn("line", i); return 1 }`,
			Entry:  "run",
		},
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

	st := p.Status()
	assert.Equal(t, 1, st.Restarts)
	assert.Equal(t, 1, st.Running)
	assert.NotEmpty(t, st.LastError, "why it stopped")
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

// A runner's process runs one owner's jobs: another's, or code nobody owns, gets a fresh
// process, an owner's next job the process that ran theirs (P1). Taking turns is no
// restart.
func TestOwners(t *testing.T) {
	t.Run("one runner", func(t *testing.T) {
		p := pool(t, coderunner.Config{Size: 1})

		run := func(owner domain.UserID) int {
			res, err := p.Run(ports.WithCodeOwner(context.Background(), owner), ports.CodeJob{Source: `function run() { return answer }`, Entry: "run"})
			require.NoError(t, err)
			assert.Equal(t, "42", string(res.Output))

			pids := p.Pids()
			require.Len(t, pids, 1)

			return pids[0]
		}

		alice := run("alice")
		assert.Equal(t, alice, run("alice"), "hers again")

		bob := run("bob")
		assert.NotEqual(t, alice, bob, "not hers")
		assert.Equal(t, bob, run("bob"))

		nobody := run("")
		assert.NotEqual(t, bob, nobody, "not his")
		assert.NotEqual(t, nobody, run(""), "code nobody owns is an owner of its own")
		assert.NotContains(t, []int{alice, bob, nobody}, run("alice"), "a fresh one, as hers was stopped")

		assert.Zero(t, p.Status().Restarts)
	})

	t.Run("one each", func(t *testing.T) {
		p := pool(t, coderunner.Config{Size: 2})
		ctx := context.Background()

		pids := func() []int { return slices.Sorted(slices.Values(p.Pids())) }
		started := pids()

		for _, owner := range []domain.UserID{"alice", "bob", "alice", "bob"} {
			_, err := p.Run(ports.WithCodeOwner(ctx, owner), ports.CodeJob{Source: `function run() {}`, Entry: "run"})
			require.NoError(t, err)
			assert.Equal(t, started, pids(), "one never used, then their own")
		}

		_, err := p.Run(ports.WithCodeOwner(ctx, "carol"), ports.CodeJob{Source: `function run() {}`, Entry: "run"})
		require.NoError(t, err)

		now := pids()
		require.Len(t, now, 2)
		assert.Len(t, slices.DeleteFunc(now, func(pid int) bool { return slices.Contains(started, pid) }), 1, "one of theirs, started again for her")
	})
}

// A runner's hard memory limit is for the most memory a job may have as it starts: one
// that may have more, the limit raised since, gets a process started for it (P10). A job
// within it fails as it would in the server, not by its process reaching it.
func TestMemoryLimit(t *testing.T) {
	var memory atomic.Int64
	memory.Store(64 << 20)

	p := pool(t, coderunner.Config{Size: 1, Limits: func() ports.CodeLimits { return ports.CodeLimits{Memory: memory.Load()} }})
	ctx := ports.WithCodeOwner(context.Background(), "alice")

	run := func() int {
		_, err := p.Run(ctx, ports.CodeJob{Source: `function run() {}`, Entry: "run"})
		require.NoError(t, err)

		pids := p.Pids()
		require.Len(t, pids, 1)

		return pids[0]
	}

	small := run()
	assert.Equal(t, small, run())
	assertDataCap(t, small, 64<<20)

	memory.Store(256 << 20)

	big := run()
	assert.NotEqual(t, small, big, "started again for more memory")
	assertDataCap(t, big, 256<<20)

	// Out of memory, as the engine or the watch says, not crashed by its hard limit.
	_, err := p.Run(ctx, ports.CodeJob{
		Source: `function run() { const a = []; for (;;) a.push(new Uint8Array(1 << 20)) }`, Entry: "run",
		Limits: ports.CodeLimits{Time: time.Minute},
	})

	var ce *domain.CodeError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, domain.CodeOutOfMemory, ce.Kind, ce.Message)
}

// assertDataCap checks a runner's hard memory limit, where the sandbox sets one.
func assertDataCap(t *testing.T, pid int, memory int64) {
	t.Helper()

	if launcher == nil || runtime.GOOS != "linux" {
		return
	}

	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/limits")
	require.NoError(t, err)

	for line := range strings.Lines(string(raw)) {
		if limit, ok := strings.CutPrefix(line, "Max data size"); ok {
			assert.Equal(t, strconv.FormatInt(coderunner.DataCap(memory), 10), strings.Fields(limit)[0])
			return
		}
	}

	t.Fatal("no data size limit")
}

// A runner whose code escaped the engine is held to its job as the engine would hold it,
// and stopped as it does what no sound runner does (P1, P10).
func TestEscaped(t *testing.T) {
	ctx := ports.WithCodeOwner(context.Background(), "mallory")

	inServer, err := jsruntime.New(ctx, observability.NoopUtility(), jsruntime.Config{Concurrency: 1, Prelude: prelude})
	require.NoError(t, err)
	t.Cleanup(func() { _ = inServer.Close(ctx) })

	// What it sends past its job's limits, and the code that would go past them in the
	// server: the same outcome either way.
	for name, c := range map[string]struct {
		rogue   string
		job     ports.CodeJob
		stopped bool
	}{
		"too many host calls": {
			rogue: "calls", stopped: true,
			job: ports.CodeJob{
				Source: `function run() { for (let i = 0; i < 4; i++) try { __host("x", i) } catch {} }`,
				Entry:  "run", Host: echo, Limits: ports.CodeLimits{HostCalls: 3},
			},
		},
		"too big an output": {
			rogue: "output", stopped: true,
			job: ports.CodeJob{Source: `function run() { return "x".repeat(1000) }`, Entry: "run", Limits: ports.CodeLimits{Output: 100}},
		},
		"too many logs": {
			rogue: "logs",
			job: ports.CodeJob{
				Source: `function run() { console.log("x".repeat(5000)); for (let i = 1; i < 150; i++) console.log("line " + i); return null }`,
				Entry:  "run",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := pool(t, coderunner.Gone(coderunner.Config{Size: 1}))

			var asked atomic.Int64

			j := c.job
			if j.Host != nil {
				j.Host = func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
					asked.Add(1)
					return echo(ctx, name, args)
				}
			}

			want, wantErr := inServer.Run(ctx, j)
			inServerAsked := asked.Swap(0)

			j.Source = c.rogue
			got, gotErr := p.Run(ctx, j)

			assert.Equal(t, string(want.Output), string(got.Output))
			assert.Equal(t, want.Logs, got.Logs)
			assert.Equal(t, want.HostCalls, got.HostCalls)
			assert.Equal(t, inServerAsked, asked.Load(), "the host asked as often")
			assert.Equal(t, c.stopped, p.Status().Running == 0, "stopped")

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

	// What no sound runner sends: the runner is stopped, as soon as it sends it.
	t.Run("a frame after its result", func(t *testing.T) {
		p := pool(t, coderunner.Gone(coderunner.Config{Size: 1}))
		first := p.Pids()

		res, err := p.Run(ctx, ports.CodeJob{Source: "stale", Entry: "run"})
		require.NoError(t, err)
		assert.Equal(t, `"done"`, string(res.Output))

		require.Eventually(t, func() bool { return p.Status().Running == 0 }, 5*time.Second, 10*time.Millisecond)
		assert.Equal(t, "it said something with no job to run", p.Status().LastError)

		_, err = p.Run(ctx, ports.CodeJob{Source: "", Entry: "run"})
		require.NoError(t, err)
		assert.NotEqual(t, first, p.Pids(), "the owner's next job, in a fresh one")
	})

	for name, c := range map[string]struct{ rogue, why string }{
		"another job's result": {"another job's", "a result of job "},
		"too big a frame":      {"frame", "a frame of "},
	} {
		t.Run(name, func(t *testing.T) {
			p := pool(t, coderunner.Gone(coderunner.Config{Size: 1}))

			_, err := p.Run(ctx, ports.CodeJob{Source: c.rogue, Entry: "run"})

			var ce *domain.CodeError
			require.ErrorAs(t, err, &ce)
			assert.Equal(t, domain.CodeCrashed, ce.Kind)
			assert.Contains(t, ce.Message, "the code's runner stopped: "+c.why)
			assert.Contains(t, p.Status().LastError, c.why)
			assert.Zero(t, p.Status().Running)
		})
	}
}

// A host call more than a frame of its job's may be is not sent: the code gets the error,
// as it would the host's (P10).
func TestBigCall(t *testing.T) {
	p := pool(t, coderunner.Config{Size: 1})

	res, err := p.Run(context.Background(), ports.CodeJob{
		Source: `function run() { try { __host("x", "y".repeat(4 << 20)) } catch (e) { return e.message } }`,
		Entry:  "run", Host: echo,
	})
	require.NoError(t, err)
	assert.Contains(t, string(res.Output), "a host call of 4.0 MiB, more than the ")
	assert.Equal(t, 1, res.HostCalls)
}
