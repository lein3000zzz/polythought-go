// Package jsruntime runs user JavaScript in a sandbox: QuickJS-NG compiled to WebAssembly
// (engine/engine.c, built by engine/build.sh) and run by wazero. Every run gets a fresh
// instance of the engine, with no files, network, environment or arguments: only the
// time, random numbers, a console and the host calls its job answers. Limits bound its
// memory, time, output and host calls, and the instance is thrown away after each run.
package jsruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

//go:embed engine/engine.wasm
var engineWasm []byte

const (
	tracerName = "github.com/lein3000zzz/polythought-go/internal/adapters/jsruntime"
	opRun      = "jsruntime.Run"

	// MaxMemory is the most a run's memory limit may be; more is lowered to it.
	MaxMemory = 1 << 30

	// The engine's C stack is 4 MiB (engine/build.sh). JavaScript gets all but 1 MiB of
	// it, which the engine's own C code may still need when JavaScript is at its limit.
	jsStack = 3 << 20

	// overhead is what an instance takes outside its memory limit: the stack, the
	// engine's data, and the source and input the host writes in.
	overhead = 64 << 20

	maxLogs     = 100
	maxLogBytes = 2000
	maxReport   = 64 << 10 // bytes of an error report read back
)

// DefaultLimits are the limits without Config.Limits.
var DefaultLimits = ports.CodeLimits{Memory: 64 << 20, Time: 5 * time.Second, Output: 1 << 20, HostCalls: 100}

// What the engine's run returns, as engine.c numbers it.
const (
	runOK = iota
	runThrew
	runOutOfMemory
	runOutOfTime
	runNoEntry
	runPending
)

var _ ports.CodeRunner = (*Runner)(nil)

type Runner struct {
	rt     wazero.Runtime
	engine wazero.CompiledModule
	slots  chan struct{}
	limits func() ports.CodeLimits
	kit    *redkit.Kit
	scope  tracing.Scope
}

type Config struct {
	// Concurrency is how many runs may go at once; more wait their turn. Zero means one
	// per CPU.
	Concurrency int

	// Limits are the most a run may use, and what it gets when its job sets no limit;
	// a job may ask for less. Asked before every run, so changes apply at once. Nil, or
	// a limit at zero, means DefaultLimits.
	Limits func() ports.CodeLimits
}

// New compiles the engine, which takes a moment, once for every run to come.
func New(ctx context.Context, util *observability.Utility, cfg Config) (*Runner, error) {
	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    util.Logger.With("component", "jsruntime"),
		Subsystem: "jsruntime",
		Buckets:   redkit.MediumLongBuckets,
		Component: "code run",
	})
	if err != nil {
		return nil, fmt.Errorf("jsruntime: register metrics: %w", err)
	}

	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages((MaxMemory+overhead)/65536))

	r, err := newRunner(ctx, rt, cfg)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}

	r.kit = kit
	r.scope = tracing.Scope{Manager: util.TracerManager, TracerName: tracerName}

	return r, nil
}

func newRunner(ctx context.Context, rt wazero.Runtime, cfg Config) (*Runner, error) {
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		return nil, fmt.Errorf("jsruntime: wasi: %w", err)
	}

	_, err := rt.NewHostModuleBuilder("polythought").
		NewFunctionBuilder().WithFunc(hostCall).Export("host_call").
		NewFunctionBuilder().WithFunc(hostRead).Export("host_read").
		NewFunctionBuilder().WithFunc(interrupted).Export("interrupted").
		NewFunctionBuilder().WithFunc(hostLog).Export("log").
		Instantiate(ctx)
	if err != nil {
		return nil, fmt.Errorf("jsruntime: host functions: %w", err)
	}

	engine, err := rt.CompileModule(ctx, engineWasm)
	if err != nil {
		return nil, fmt.Errorf("jsruntime: compile the engine: %w", err)
	}

	n := cfg.Concurrency
	if n <= 0 {
		n = runtime.NumCPU()
	}

	limits := cfg.Limits
	if limits == nil {
		limits = func() ports.CodeLimits { return DefaultLimits }
	}

	return &Runner{rt: rt, engine: engine, slots: make(chan struct{}, n), limits: limits}, nil
}

// Close releases the engine. Runs still going end.
func (r *Runner) Close(ctx context.Context) error {
	return r.rt.Close(ctx)
}

func (r *Runner) Run(ctx context.Context, job ports.CodeJob) (res ports.CodeResult, err error) {
	done := r.kit.Start(ctx, opRun)

	defer func() {
		var codeErr *domain.CodeError
		done(err == nil || errors.As(err, &codeErr)) // the code failing is not the runner failing
	}()

	return tracing.InScope(ctx, r.scope, opRun, func(ctx context.Context, span trace.Span) (ports.CodeResult, error) {
		res, err := r.run(ctx, job)

		span.SetAttributes(attribute.Int("code.size", len(job.Source)), attribute.Int("code.host_calls", res.HostCalls))

		var codeErr *domain.CodeError
		if errors.As(err, &codeErr) {
			span.SetAttributes(attribute.String("code.failure", string(codeErr.Kind)))
		}

		return res, err
	})
}

// entryName is what an entry may be: a name, which the engine evaluates to find it.
var entryName = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func (r *Runner) run(ctx context.Context, job ports.CodeJob) (ports.CodeResult, error) {
	if !entryName.MatchString(job.Entry) {
		return ports.CodeResult{}, fmt.Errorf("jsruntime: entry %q is not a name", job.Entry)
	}

	limits := bound(job.Limits, r.limits())

	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		return ports.CodeResult{}, ctx.Err()
	}
	defer func() { <-r.slots }()

	// The time limit starts once the run has its slot.
	runCtx, cancel := context.WithTimeout(ctx, limits.Time)
	defer cancel()

	st := &state{host: job.Host, limits: limits}
	err := r.call(context.WithValue(runCtx, stateKey{}, st), job, st)

	res := ports.CodeResult{Logs: st.logs, HostCalls: st.calls}
	if st.droppedLogs > 0 {
		res.Logs = append(res.Logs, ports.CodeLog{Level: "info", Text: fmt.Sprintf("… and %d more lines, not kept", st.droppedLogs)})
	}

	switch {
	case ctx.Err() != nil:
		return res, ctx.Err() // the caller gave up: not the code's doing
	case st.status == runOutOfTime || runCtx.Err() != nil:
		return res, codeError(domain.CodeOutOfTime, "the code ran longer than the %s it may", limits.Time)
	case st.status == runOutOfMemory || st.noRoom:
		return res, codeError(domain.CodeOutOfMemory, "the code needed more than the %s of memory it may use", size(limits.Memory))
	case err != nil:
		return res, codeError(domain.CodeCrashed, "the code stopped the engine: %v", err)
	case st.tooManyCalls:
		return res, codeError(domain.CodeTooManyCalls, "the code asked for data more than the %d times it may", limits.HostCalls)
	}

	switch st.status {
	case runOK:
		if st.resultLen > limits.Output {
			return res, codeError(domain.CodeOutputTooBig, "the code returned %s, more than the %s it may", size(int64(st.resultLen)), size(int64(limits.Output)))
		}

		res.Output = st.result

		return res, nil
	case runThrew:
		return res, thrown(st.result)
	case runNoEntry:
		return res, codeError(domain.CodeNoEntry, "the code defines no function named %s", job.Entry)
	case runPending:
		return res, codeError(domain.CodeNeverSettled, "the code returned a promise that never settles: there are no timers or events to wait for")
	default:
		return res, codeError(domain.CodeCrashed, "the engine answered %d", st.status)
	}
}

// call runs the job in a fresh instance of the engine, leaving the outcome in st.
func (r *Runner) call(ctx context.Context, job ports.CodeJob, st *state) error {
	mod, err := r.rt.InstantiateModule(ctx, r.engine, wazero.NewModuleConfig().
		WithName("").
		WithStartFunctions("_initialize").
		WithSysWalltime().
		WithSysNanotime().
		WithRandSource(rand.Reader))
	if err != nil {
		return err
	}

	defer func() { _ = mod.Close(context.Background()) }()

	input := []byte(job.Input)
	if len(input) == 0 {
		input = []byte("null")
	}

	var ptrs [3]uint32

	for i, b := range [][]byte{[]byte(job.Source), []byte(job.Entry), input} {
		if ptrs[i], err = place(ctx, mod, b); err != nil || ptrs[i] == 0 {
			st.noRoom = ptrs[i] == 0 && err == nil
			return err
		}
	}

	out, err := mod.ExportedFunction("run").Call(ctx,
		uint64(ptrs[0]), uint64(len(job.Source)), uint64(ptrs[1]), uint64(ptrs[2]), uint64(len(input)),
		uint64(min(st.limits.Memory, MaxMemory)), jsStack)
	if err != nil {
		return err
	}

	st.status = uint32(out[0])

	return st.read(ctx, mod)
}

// place copies b into the instance, with a NUL byte after it, and says where; 0 when
// there is no room.
func place(ctx context.Context, mod api.Module, b []byte) (uint32, error) {
	out, err := mod.ExportedFunction("alloc").Call(ctx, uint64(len(b)+1))
	if err != nil {
		return 0, err
	}

	ptr := uint32(out[0])
	if ptr == 0 {
		return 0, nil
	}

	if !mod.Memory().Write(ptr, b) || !mod.Memory().WriteByte(ptr+uint32(len(b)), 0) {
		return 0, errors.New("jsruntime: write outside the engine's memory")
	}

	return ptr, nil
}

type stateKey struct{}

// state is one run's, reached by the host functions through their context. They are
// called on the goroutine that runs the engine, one at a time.
type state struct {
	host   ports.CodeHost
	limits ports.CodeLimits

	calls        int
	tooManyCalls bool
	answer       []byte // the last host call's, until the engine reads it

	logs        []ports.CodeLog
	droppedLogs int

	status    uint32
	result    []byte
	resultLen int
	noRoom    bool // the source or input did not fit
}

// read copies the result out, unless it is too big to keep.
func (st *state) read(ctx context.Context, mod api.Module) error {
	ptr, err := mod.ExportedFunction("result_ptr").Call(ctx)
	if err != nil {
		return err
	}

	n, err := mod.ExportedFunction("result_len").Call(ctx)
	if err != nil {
		return err
	}

	st.resultLen = int(uint32(n[0]))

	most := maxReport
	if st.status == runOK {
		most = st.limits.Output
	}

	if st.status == runOK && st.resultLen > most {
		return nil
	}

	b, ok := mod.Memory().Read(uint32(ptr[0]), uint32(min(st.resultLen, most)))
	if !ok {
		return errors.New("jsruntime: the result is outside the engine's memory")
	}

	st.result = bytes.Clone(b)

	return nil
}

func stateOf(ctx context.Context) *state {
	return ctx.Value(stateKey{}).(*state)
}

// hostCall answers __host(name, args): the answer's length, or -1 minus the length of
// an error message, for hostRead to copy in.
func hostCall(ctx context.Context, m api.Module, namePtr, nameLen, argsPtr, argsLen uint32) int32 {
	st := stateOf(ctx)
	st.answer = nil

	name, ok := m.Memory().Read(namePtr, nameLen)
	if !ok {
		return st.fail("a host call outside the engine's memory")
	}

	args, ok := m.Memory().Read(argsPtr, argsLen)
	if !ok {
		return st.fail("a host call outside the engine's memory")
	}

	st.calls++

	switch {
	case st.host == nil:
		return st.fail(fmt.Sprintf("%s: this code cannot ask for data", name))
	case st.calls > st.limits.HostCalls:
		st.tooManyCalls = true
		return st.fail(fmt.Sprintf("%s: asked for data more than the %d times a run may", name, st.limits.HostCalls))
	}

	answer, err := st.host(ctx, string(name), json.RawMessage(bytes.Clone(args)))
	if err != nil {
		return st.fail(err.Error())
	}

	if len(answer) == 0 {
		answer = []byte("null")
	}

	st.answer = answer

	return int32(len(answer))
}

func (st *state) fail(msg string) int32 {
	st.answer = []byte(msg)
	return -1 - int32(len(msg))
}

func hostRead(ctx context.Context, m api.Module, dst uint32) {
	st := stateOf(ctx)
	m.Memory().Write(dst, st.answer)
	st.answer = nil
}

// interrupted tells the engine to stop once the run is out of time.
func interrupted(ctx context.Context) int32 {
	if ctx.Err() != nil {
		return 1
	}

	return 0
}

var logLevels = []string{"log", "info", "warn", "error"}

func hostLog(ctx context.Context, m api.Module, level int32, ptr, n uint32) {
	st := stateOf(ctx)

	if len(st.logs) >= maxLogs {
		st.droppedLogs++
		return
	}

	b, ok := m.Memory().Read(ptr, min(n, maxLogBytes))
	if !ok {
		return
	}

	// A character cut in two at the end goes.
	text := strings.ToValidUTF8(string(b), "")
	if n > maxLogBytes {
		text += "…"
	}

	name := "log"
	if level >= 0 && int(level) < len(logLevels) {
		name = logLevels[level]
	}

	st.logs = append(st.logs, ports.CodeLog{Level: name, Text: text})
}

// thrown describes what the code threw, from the engine's report.
func thrown(report []byte) error {
	var r struct {
		Name    string `json:"name"`
		Message string `json:"message"`
		Stack   string `json:"stack"`
	}

	if err := json.Unmarshal(report, &r); err != nil {
		return codeError(domain.CodeThrew, "the code threw something that could not be read")
	}

	msg := r.Message
	if r.Name != "" {
		msg = r.Name + ": " + r.Message
	}

	return &domain.CodeError{Kind: domain.CodeThrew, Message: msg, Stack: strings.TrimRight(r.Stack, "\n")}
}

func codeError(kind domain.CodeErrorKind, format string, args ...any) *domain.CodeError {
	return &domain.CodeError{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// bound is what a job gets: what it asks for, within most.
func bound(asked, most ports.CodeLimits) ports.CodeLimits {
	most.Memory = min(limit(most.Memory, DefaultLimits.Memory), MaxMemory)
	most.Time = limit(most.Time, DefaultLimits.Time)
	most.Output = limit(most.Output, DefaultLimits.Output)
	most.HostCalls = limit(most.HostCalls, DefaultLimits.HostCalls)

	return ports.CodeLimits{
		Memory:    limit(min(asked.Memory, most.Memory), most.Memory),
		Time:      limit(min(asked.Time, most.Time), most.Time),
		Output:    limit(min(asked.Output, most.Output), most.Output),
		HostCalls: limit(min(asked.HostCalls, most.HostCalls), most.HostCalls),
	}
}

// limit is n, or otherwise when n is not set.
func limit[T int | int64 | time.Duration](n, otherwise T) T {
	if n <= 0 {
		return otherwise
	}

	return n
}

// size writes bytes for people: 64 MiB, 512 KiB, 300 bytes.
func size(n int64) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", n>>20)
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KiB", n>>10)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
