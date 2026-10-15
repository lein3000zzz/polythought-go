package jsruntime_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/jsruntime"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// runner is compiled once for every test.
var runner *jsruntime.Runner

func TestMain(m *testing.M) {
	var err error

	runner, err = jsruntime.New(context.Background(), observability.NoopUtility(), jsruntime.Config{Concurrency: 4})
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func run(t *testing.T, source string, input string) (ports.CodeResult, error) {
	t.Helper()

	return runner.Run(context.Background(), ports.CodeJob{Source: source, Entry: "run", Input: json.RawMessage(input)})
}

// codeErr checks err is the code failing in this way, and returns how.
func codeErr(t *testing.T, err error, kind domain.CodeErrorKind) domain.CodeError {
	t.Helper()

	var ce *domain.CodeError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, kind, ce.Kind, ce.Message)

	return *ce
}

func TestResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		entry  string // run when empty
		input  string
		want   string
	}{
		{name: "a value", source: `function run(x) { return {sum: x.a + x.b} }`, input: `{"a":1,"b":2}`, want: `{"sum":3}`},
		{name: "awaited", source: `async function run(x) { await null; return [x, await Promise.resolve(2)] }`, input: `1`, want: `[1,2]`},
		{name: "no input", source: `function run(x) { return x }`, want: `null`},
		{name: "nothing returned", source: `function run() {}`, want: `null`},
		{name: "decimals stay text", source: `function run(x) { return x.price }`, input: `{"price":"0.0200"}`, want: `"0.0200"`},
		{name: "an entry in an object", source: `var exports = {run: (x) => x * 2}`, entry: "exports.run", input: `21`, want: `42`},
		{name: "modern JavaScript", source: `const run = (xs) => xs.map(({n}) => n ?? 0).toSorted((a, b) => b - a).at(0)`, input: `[{"n":2},{},{"n":5}]`, want: `5`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			entry := tt.entry
			if entry == "" {
				entry = "run"
			}

			res, err := runner.Run(context.Background(), ports.CodeJob{Source: tt.source, Entry: entry, Input: json.RawMessage(tt.input)})
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(res.Output))
		})
	}
}

func TestFailures(t *testing.T) {
	t.Parallel()

	t.Run("a throw, with where", func(t *testing.T) {
		t.Parallel()

		_, err := run(t, "function run(x) {\n  return x.a.b\n}", `{}`)
		ce := codeErr(t, err, domain.CodeThrew)
		assert.Equal(t, "TypeError: cannot read property 'b' of undefined", ce.Message)
		assert.Contains(t, ce.Stack, "code.js:2")
	})

	for name, tc := range map[string]struct {
		source string
		kind   domain.CodeErrorKind
		want   string
	}{
		"a rejection":         {`async function run() { throw new RangeError("nope") }`, domain.CodeThrew, "RangeError: nope"},
		"not an error thrown": {`function run() { throw "boom" }`, domain.CodeThrew, "boom"},
		"a syntax error":      {`function run( {`, domain.CodeThrew, "SyntaxError: "},
		"no entry function":   {`const go = 1`, domain.CodeNoEntry, "the code defines no function named run"},
		"never settles":       {`function run() { return new Promise(() => {}) }`, domain.CodeNeverSettled, "never settles"},
		"bad input":           {`function run() {}`, domain.CodeThrew, "SyntaxError"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			input := "1"
			if name == "bad input" {
				input = "{"
			}

			res, err := run(t, tc.source, input)
			ce := codeErr(t, err, tc.kind)
			assert.Contains(t, ce.Message, tc.want)
			assert.Nil(t, res.Output)
		})
	}

	t.Run("an entry that is not a name is the caller's mistake", func(t *testing.T) {
		t.Parallel()

		_, err := runner.Run(context.Background(), ports.CodeJob{Source: `function run() {}`, Entry: "run()"})
		require.ErrorContains(t, err, `entry "run()" is not a name or a path of names`)

		var ce *domain.CodeError
		assert.False(t, errors.As(err, &ce))
	})

	t.Run("deep recursion is an error the code can catch", func(t *testing.T) {
		t.Parallel()

		res, err := run(t, `function f(n) { return f(n + 1) + 1 }
			function run() { try { f(0) } catch (e) { return String(e) } }`, "")
		require.NoError(t, err)
		assert.Equal(t, `"RangeError: Maximum call stack size exceeded"`, string(res.Output))
	})
}

func TestLimits(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	job := func(source string, limits ports.CodeLimits) ports.CodeJob {
		return ports.CodeJob{Source: source, Entry: "run", Limits: limits}
	}

	t.Run("time", func(t *testing.T) {
		t.Parallel()

		begun := time.Now()
		_, err := runner.Run(ctx, job(`function run() { while (true) {} }`, ports.CodeLimits{Time: 200 * time.Millisecond}))
		ce := codeErr(t, err, domain.CodeOutOfTime)
		assert.Equal(t, "the code ran longer than the 200ms it may", ce.Message)
		assert.Less(t, time.Since(begun), 2*time.Second)

		// Catching is no way out.
		_, err = runner.Run(ctx, job(`function run() { for (;;) { try { while (true) {} } catch {} } }`, ports.CodeLimits{Time: 200 * time.Millisecond}))
		codeErr(t, err, domain.CodeOutOfTime)
	})

	t.Run("memory, even when caught", func(t *testing.T) {
		t.Parallel()

		for _, source := range []string{
			`function run() { const a = []; while (true) a.push("x".repeat(1e5) + a.length) }`,
			`function run() { try { const a = []; while (true) a.push(new Array(1e5).fill(1)) } catch { return "fine" } }`,
		} {
			_, err := runner.Run(ctx, job(source, ports.CodeLimits{Memory: 16 << 20}))
			ce := codeErr(t, err, domain.CodeOutOfMemory)
			assert.Equal(t, "the code needed more than the 16 MiB of memory it may use", ce.Message)
		}

		res, err := runner.Run(ctx, job(`function run() { return new Array(1e5).fill(1).length }`, ports.CodeLimits{Memory: 16 << 20}))
		require.NoError(t, err, "within it")
		assert.Equal(t, "100000", string(res.Output))
	})

	t.Run("output", func(t *testing.T) {
		t.Parallel()

		res, err := runner.Run(ctx, job(`function run() { return "x".repeat(2e6) }`, ports.CodeLimits{Output: 1 << 20}))
		ce := codeErr(t, err, domain.CodeOutputTooBig)
		assert.Equal(t, "the code returned 1.9 MiB, more than the 1 MiB it may", ce.Message)
		assert.Nil(t, res.Output)
	})

	t.Run("the caller giving up is not the code's failure", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()

		_, err := runner.Run(ctx, job(`function run() { while (true) {} }`, ports.CodeLimits{Time: time.Minute}))
		require.ErrorIs(t, err, context.DeadlineExceeded)

		var ce *domain.CodeError
		assert.False(t, errors.As(err, &ce))
	})
}

func TestHostCalls(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	echo := func(_ context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
		if name == "fail" {
			return nil, errors.New("no markets today")
		}

		return json.RawMessage(fmt.Sprintf(`{"name":%q,"args":%s}`, name, args)), nil
	}

	t.Run("answered, or thrown", func(t *testing.T) {
		t.Parallel()

		res, err := runner.Run(ctx, ports.CodeJob{
			Source: `function run() {
				const a = __host("markets.search", {q: "election", limit: 2})
				let b
				try { __host("fail") } catch (e) { b = e.message }
				return {a, b}
			}`,
			Entry: "run", Host: echo,
		})
		require.NoError(t, err)
		assert.JSONEq(t, `{"a":{"name":"markets.search","args":{"q":"election","limit":2}},"b":"no markets today"}`, string(res.Output))
		assert.Equal(t, 2, res.HostCalls)
	})

	t.Run("none to make", func(t *testing.T) {
		t.Parallel()

		_, err := runner.Run(ctx, ports.CodeJob{Source: `function run() { __host("markets.search") }`, Entry: "run"})
		ce := codeErr(t, err, domain.CodeThrew)
		assert.Equal(t, "Error: markets.search: this code cannot ask for data", ce.Message)
	})

	t.Run("too many fails the run, caught or not", func(t *testing.T) {
		t.Parallel()

		res, err := runner.Run(ctx, ports.CodeJob{
			Source: `function run() { for (let i = 0; i < 10; i++) try { __host("x", i) } catch {} return "done" }`,
			Entry:  "run", Host: echo, Limits: ports.CodeLimits{HostCalls: 3},
		})
		ce := codeErr(t, err, domain.CodeTooManyCalls)
		assert.Equal(t, "the code asked for data more than the 3 times it may", ce.Message)
		assert.Equal(t, 10, res.HostCalls)
	})

	t.Run("waiting on one counts against the time", func(t *testing.T) {
		t.Parallel()

		slow := func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}

		_, err := runner.Run(ctx, ports.CodeJob{
			Source: `function run() { __host("slow") }`, Entry: "run", Host: slow,
			Limits: ports.CodeLimits{Time: 200 * time.Millisecond},
		})
		codeErr(t, err, domain.CodeOutOfTime)
	})
}

func TestConsole(t *testing.T) {
	t.Parallel()

	res, err := run(t, `function run() {
		console.log("hi", {a: 1}, [2], 3n, undefined)
		console.info("i"); console.warn("w"); console.error("e"); console.debug("d")
		console.log("é".repeat(3000))
		for (let i = 0; i < 200; i++) console.log(i)
	}`, "")
	require.NoError(t, err)

	require.Len(t, res.Logs, 101)
	assert.Equal(t, ports.CodeLog{Level: "log", Text: `hi {"a":1} [2] 3 undefined`}, res.Logs[0])
	assert.Equal(t, []string{"info", "warn", "error", "log"}, []string{res.Logs[1].Level, res.Logs[2].Level, res.Logs[3].Level, res.Logs[4].Level})

	long := res.Logs[5].Text
	assert.True(t, strings.HasSuffix(long, "é…"), "cut at a whole character")
	assert.Equal(t, 1000, strings.Count(long, "é"))

	assert.Equal(t, ports.CodeLog{Level: "info", Text: "… and 106 more lines, not kept"}, res.Logs[100])
}

func TestSandbox(t *testing.T) {
	t.Parallel()

	res, err := run(t, `function run() {
		return ["std", "os", "require", "process", "fetch", "setTimeout", "WebAssembly", "__log", "XMLHttpRequest"]
			.filter((name) => typeof globalThis[name] !== "undefined")
	}`, "")
	require.NoError(t, err)
	assert.JSONEq(t, `[]`, string(res.Output), "nothing reaches out but __host")

	t.Run("each run starts afresh", func(t *testing.T) {
		_, err := run(t, `function run() { globalThis.leak = 1; Object.prototype.polluted = true }`, "")
		require.NoError(t, err)

		res, err := run(t, `function run() { return [typeof leak, ({}).polluted ?? null] }`, "")
		require.NoError(t, err)
		assert.JSONEq(t, `["undefined", null]`, string(res.Output))
	})

	t.Run("the time of day and random numbers are real", func(t *testing.T) {
		res, err := run(t, `function run() { return [Date.now(), Math.random()] }`, "")
		require.NoError(t, err)

		again, err := run(t, `function run() { return [Date.now(), Math.random()] }`, "")
		require.NoError(t, err)

		var a, b [2]float64
		require.NoError(t, json.Unmarshal(res.Output, &a))
		require.NoError(t, json.Unmarshal(again.Output, &b))

		assert.InDelta(t, float64(time.Now().UnixMilli()), a[0], 5000)
		assert.NotEqual(t, a[1], b[1])
	})
}

func TestConcurrentRuns(t *testing.T) {
	t.Parallel()

	var wg sync.WaitGroup

	for i := range 16 {
		wg.Go(func() {
			res, err := run(t, `function run(n) { let s = 0; for (let i = 0; i <= n; i++) s += i; return s }`, fmt.Sprint(i*1000))
			assert.NoError(t, err)
			assert.Equal(t, fmt.Sprint(i*1000*(i*1000+1)/2), string(res.Output))
		})
	}

	wg.Wait()
}

func TestConfiguredLimits(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	var (
		mu   sync.Mutex
		most = ports.CodeLimits{Memory: 16 << 20, Time: 300 * time.Millisecond}
	)

	r, err := jsruntime.New(ctx, observability.NoopUtility(), jsruntime.Config{
		Concurrency: 1,
		Limits: func() ports.CodeLimits {
			mu.Lock()
			defer mu.Unlock()

			return most
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close(ctx) })

	loop := func(limits ports.CodeLimits) error {
		_, err := r.Run(ctx, ports.CodeJob{Source: `function run() { while (true) {} }`, Entry: "run", Limits: limits})
		return err
	}

	ce := codeErr(t, loop(ports.CodeLimits{}), domain.CodeOutOfTime)
	assert.Contains(t, ce.Message, "300ms", "what is configured, where the job sets nothing")

	ce = codeErr(t, loop(ports.CodeLimits{Time: time.Hour}), domain.CodeOutOfTime)
	assert.Contains(t, ce.Message, "300ms", "a job cannot ask for more")

	ce = codeErr(t, loop(ports.CodeLimits{Time: 100 * time.Millisecond}), domain.CodeOutOfTime)
	assert.Contains(t, ce.Message, "100ms", "but may for less")

	mu.Lock()
	most.Time = 150 * time.Millisecond
	mu.Unlock()

	ce = codeErr(t, loop(ports.CodeLimits{}), domain.CodeOutOfTime)
	assert.Contains(t, ce.Message, "150ms", "changes apply to the next run")

	mu.Lock()
	most.Time = 0 // the default
	mu.Unlock()

	_, err = r.Run(ctx, ports.CodeJob{
		Source: `function run() { const a = []; while (true) a.push("x".repeat(1e5) + a.length) }`, Entry: "run",
		Limits: ports.CodeLimits{Memory: 1 << 30},
	})
	ce = codeErr(t, err, domain.CodeOutOfMemory)
	assert.Contains(t, ce.Message, "16 MiB")

	res, err := r.Run(ctx, ports.CodeJob{Source: `function run() { return 1 }`, Entry: "run"})
	require.NoError(t, err, "unset limits are the defaults")
	assert.Equal(t, "1", string(res.Output))
}
