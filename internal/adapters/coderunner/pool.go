package coderunner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"

	"github.com/lein3000zzz/polythought-go/internal/adapters/jsruntime"
	"github.com/lein3000zzz/polythought-go/internal/adapters/sandbox"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	tracerName = "github.com/lein3000zzz/polythought-go/internal/adapters/coderunner"
	opRun      = "coderunner.Run"

	// startFor is how long a runner may take to start and make its engine.
	startFor = 30 * time.Second

	// grace is how long past its time a job's runner may take to answer, its own limit
	// having ended it, before the pool kills it.
	grace = 2 * time.Second

	// engineOverhead is jsruntime's overhead: what an instance takes outside its job's
	// memory, the stack, the engine's data, and the source and input written in.
	engineOverhead = 64 << 20

	// idleData is room for an idle runner's data segment: about 250 MiB, most of it the
	// engine compiled, measured under the sandbox.
	idleData = 512 << 20

	// fileSize is the most a file a runner writes may be: they write none.
	fileSize = 1 << 20
)

// dataCap is a runner's hard limit for jobs of up to that much memory: its data segment's
// (RLIMIT_DATA, P10), which no job within it ever reaches. A Go process keeps what it maps
// for its heap, and the engine's memory is copied as it grows, each copy a quarter bigger,
// so the copies it leaves may take four times it again: under the sandbox, from 8 MiB to
// 1 GiB, a job took at most 5.4 times its memory past an idle runner. Six times the job's
// memory and the engine's overhead, then, three copies of the biggest job frame as it is
// read, and the idle runner's own: 1.4 GiB for 64 MiB, 7.1 GiB for jsruntime.MaxMemory.
func dataCap(memory int64) int64 {
	return 6*(memory+engineOverhead) + 3*maxFrame + idleData
}

var _ ports.CodeRunner = (*Pool)(nil)

// Config is a pool's.
type Config struct {
	// Size is how many runners there are, so how many runs may go at once; more wait their
	// turn. Zero means one per CPU.
	Size int

	// Prelude runs before every job's code, as jsruntime's does.
	Prelude string

	// Limits are the most a run may use, as jsruntime's: asked before every run.
	Limits func() ports.CodeLimits

	// Sandbox starts the runners under the sandbox; nil where there is none, when they
	// start without it (D354).
	Sandbox *sandbox.Launcher

	// Watch is how often a runner's memory is looked at while it runs a job, and Headroom
	// what its process may take beside twice the job's memory before it is stopped; zero
	// means every half second, and the package's Headroom.
	Watch    time.Duration
	Headroom int64

	entry string // what the runners run: Entry, but in tests
}

// Pool runs user code in runner processes (D353): a warm pool, each runner running one
// job at a time; one that stops fails only its own run, and is started again for the next.
// A runner's process runs the jobs of one owner only (P1).
type Pool struct {
	cfg    Config
	log    logging.Logger
	kit    *redkit.Kit
	scope  tracing.Scope
	shares jsruntime.Shares
	jobs   atomic.Uint64 // the last job's ID
	said   *quota        // how much of the runners' stderr the server's log still takes

	mu       sync.Mutex
	all      []*runner
	idle     []*runner     // those running no job
	freed    chan struct{} // closed as one is put back, then a new one
	restarts int           // runners started again, after they stopped or were stopped
	lastErr  string        // why one last stopped, or would not start
	closed   bool
}

// New starts the runners, each making its engine, which takes a moment, and refuses the
// pool when one does not start.
func New(ctx context.Context, util *observability.Utility, cfg Config) (*Pool, error) {
	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    util.Logger.With("component", "coderunner"),
		Subsystem: "coderunner",
		Buckets:   redkit.MediumLongBuckets,
		Component: "code run",
	})
	if err != nil {
		return nil, fmt.Errorf("coderunner: register metrics: %w", err)
	}

	if cfg.Size <= 0 {
		cfg.Size = runtime.NumCPU()
	}

	if cfg.Limits == nil {
		cfg.Limits = func() ports.CodeLimits { return jsruntime.DefaultLimits }
	}

	if cfg.Watch <= 0 {
		cfg.Watch = 500 * time.Millisecond
	}

	if cfg.Headroom <= 0 {
		cfg.Headroom = Headroom
	}

	if cfg.entry == "" {
		cfg.entry = Entry.Name
	}

	p := &Pool{
		cfg: cfg, log: util.Logger.With("component", "coderunner"), kit: kit,
		scope: tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		said:  &quota{limit: rate.NewLimiter(linesPerSecond, linesBurst)},
	}

	memory := p.memory()

	var (
		wg   sync.WaitGroup
		errs = make([]error, cfg.Size)
	)

	for i := range cfg.Size {
		r := &runner{pool: p}
		p.all = append(p.all, r)

		wg.Go(func() { errs[i] = r.start(ctx, memory) })
	}

	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		_ = p.Close(ctx)
		return nil, fmt.Errorf("coderunner: %w", err)
	}

	p.idle = slices.Clone(p.all)

	return p, nil
}

// memory is the most memory a job may have now, as the limits are.
func (p *Pool) memory() int64 {
	return jsruntime.Bound(ports.CodeLimits{}, p.cfg.Limits()).Memory
}

// Sandboxed says the runners run under the sandbox.
func (p *Pool) Sandboxed() bool { return p.cfg.Sandbox != nil }

// Status is how the runners are.
func (p *Pool) Status() ports.CodeRunners {
	p.mu.Lock()
	defer p.mu.Unlock()

	st := ports.CodeRunners{
		InRunners: true, Size: len(p.all), Sandboxed: p.Sandboxed(), Restarts: p.restarts, LastError: p.lastErr,
	}

	for _, r := range p.all {
		if r.alive() {
			st.Running++
		}
	}

	return st
}

// Close stops every runner. Runs still going fail.
func (p *Pool) Close(context.Context) error {
	p.mu.Lock()
	p.closed = true
	all := p.all
	p.mu.Unlock()

	for _, r := range all {
		r.stop()
	}

	return nil
}

func (p *Pool) Run(ctx context.Context, j ports.CodeJob) (res ports.CodeResult, err error) {
	done := p.kit.Start(ctx, opRun)

	defer func() {
		var codeErr *domain.CodeError
		done(err == nil || errors.As(err, &codeErr)) // the code failing is not the runner failing
	}()

	return tracing.InScope(ctx, p.scope, opRun, func(ctx context.Context, span trace.Span) (ports.CodeResult, error) {
		res, err := p.run(ctx, j)

		span.SetAttributes(attribute.Int("code.size", len(j.Source)), attribute.Int("code.host_calls", res.HostCalls))

		var codeErr *domain.CodeError
		if errors.As(err, &codeErr) {
			span.SetAttributes(attribute.String("code.failure", string(codeErr.Kind)))
		}

		return res, err
	})
}

func (p *Pool) run(ctx context.Context, j ports.CodeJob) (ports.CodeResult, error) {
	limits := jsruntime.Bound(j.Limits, p.cfg.Limits())
	owner := ports.CodeOwner(ctx)

	// No owner holds more than their share of the runners (R12), then a runner.
	share, err := p.shares.Take(ctx, owner, p.cfg.Size)
	if err != nil {
		return ports.CodeResult{}, err
	}
	defer share()

	r, fresh, err := p.take(ctx, owner, limits.Memory)
	if err != nil {
		return ports.CodeResult{}, err
	}
	defer p.put(r)

	if fresh {
		if err := p.fresh(ctx, r, max(limits.Memory, p.memory())); err != nil {
			return ports.CodeResult{}, err
		}
	}

	return r.run(ctx, j, limits)
}

// take waits for an idle runner and takes the best for owner's job, of that much memory:
// one whose process ran owner's jobs, else one whose ran none; else any, fresh saying its
// process is to be started again before the job runs (P1).
func (p *Pool) take(ctx context.Context, owner domain.UserID, memory int64) (r *runner, fresh bool, err error) {
	for {
		p.mu.Lock()
		if len(p.idle) > 0 {
			r, fresh = p.pick(owner, memory)
			p.mu.Unlock()

			return r, fresh, nil
		}

		if p.freed == nil {
			p.freed = make(chan struct{})
		}

		wait := p.freed
		p.mu.Unlock()

		select {
		case <-wait:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
}

// pick takes the idle runner best for owner's job, as take says, and gives its process
// to owner; p.mu is held. Of those to start again, one that stopped or is too small goes
// first, before another owner's.
func (p *Pool) pick(owner domain.UserID, memory int64) (*runner, bool) {
	best, rank := 0, 4

	for i, r := range p.idle {
		pr := r.current()

		n := 3 // another owner's

		switch {
		case pr == nil || pr.memory < memory:
			n = 2
		case !r.served:
			n = 1
		case owner != "" && r.owner == owner:
			n = 0
		}

		if n < rank {
			best, rank = i, n
		}
	}

	r := p.idle[best]
	p.idle = slices.Delete(p.idle, best, best+1)
	r.served, r.owner = true, owner

	return r, rank >= 2
}

// put gives a runner back, for the next job.
func (p *Pool) put(r *runner) {
	p.mu.Lock()
	p.idle = append(p.idle, r)
	freed := p.freed
	p.freed = nil
	p.mu.Unlock()

	if freed != nil {
		close(freed)
	}
}

// fresh starts a runner's process again, for jobs of up to that much memory: one that
// stopped, which counts as a restart, or one stopped now, as it ran another owner's jobs
// or has too little memory for this one (P1).
func (p *Pool) fresh(ctx context.Context, r *runner, memory int64) error {
	p.mu.Lock()
	closed := p.closed

	if !r.alive() {
		p.restarts++
	}
	p.mu.Unlock()

	if closed {
		return errors.New("coderunner: the pool is closed")
	}

	r.stop()

	if err := r.start(ctx, memory); err != nil {
		p.stopped(err.Error())
		return fmt.Errorf("coderunner: %w", err)
	}

	return nil
}

// stopped records why a runner stopped, or would not start.
func (p *Pool) stopped(why string) {
	p.mu.Lock()
	p.lastErr = why
	p.mu.Unlock()

	p.log.Warn(context.Background(), "Code runner stopped", "why", why)
}

// runner is one of the pool's runners: its process, while it has one.
type runner struct {
	pool *Pool

	// Whose its process is, under the pool's mu: the owner of the jobs it ran (P1). Code
	// nobody owns is an owner of its own, so a process that ran some runs nothing else.
	served bool
	owner  domain.UserID

	mu   sync.Mutex
	proc *proc
	last string // the last line its process wrote to stderr
}

// proc is a runner's process.
type proc struct {
	cmd     *exec.Cmd
	in      *bufio.Writer
	stdin   io.Closer
	frames  chan message  // what it says, closed once it says no more
	exited  chan struct{} // closed once it has exited
	quit    chan struct{} // closed as it is stopped: what it says then goes unread
	once    sync.Once
	temp    string
	memory  int64            // the most memory a job of its may have, for its hard limit
	stopped func(why string) // the pool's, as the process is stopped for what it said

	mu   sync.Mutex
	job  uint64 // the job it runs, none at zero: what it says then stops it (P1)
	room int    // the most a frame of its job may be (P10)
	why  string // why it was stopped for what it said
}

// start starts its process, under the sandbox where there is one, for jobs of up to that
// much memory, and waits for its engine.
func (r *runner) start(ctx context.Context, memory int64) error {
	temp, err := os.MkdirTemp("", "polythought-runner-")
	if err != nil {
		return fmt.Errorf("its temporary directory: %w", err)
	}

	var cmd *exec.Cmd

	if sb := r.pool.cfg.Sandbox; sb != nil {
		cmd, err = sb.Command(sandbox.Policy{
			Entry: r.pool.cfg.entry, Temp: temp, Memory: dataCap(memory), FileSize: fileSize,
		})
	} else {
		cmd, err = sandbox.EntryCommand(r.pool.cfg.entry)
	}

	if err != nil {
		_ = os.RemoveAll(temp)
		return err
	}

	// None of the server's environment, so none of its settings or secrets.
	cmd.Dir = temp
	cmd.Env = []string{"TMPDIR=" + temp, "HOME=" + temp}
	cmd.Stderr = &lines{r: r}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = os.RemoveAll(temp)
		return err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = os.RemoveAll(temp)
		return err
	}

	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(temp)
		return fmt.Errorf("start: %w", err)
	}

	pr := &proc{
		cmd: cmd, in: bufio.NewWriter(stdin), stdin: stdin, temp: temp, memory: memory,
		frames: make(chan message), exited: make(chan struct{}), quit: make(chan struct{}),
		stopped: r.pool.stopped,
	}

	go pr.read(stdout)

	r.mu.Lock()
	r.proc, r.last = pr, ""
	r.mu.Unlock()

	ready, err := r.ready(ctx, pr)
	if err != nil {
		pr.stop()
		return err
	}

	if ready.Error != "" {
		pr.stop()
		return fmt.Errorf("its engine: %s", ready.Error)
	}

	return nil
}

// read passes on what the process says until it ends, then waits for it: its ready, then
// only its job's calls and result.
func (pr *proc) read(stdout io.Reader) {
	defer close(pr.exited)

	out := bufio.NewReader(stdout)

	m, err := readFrame(out, readyRoom)

	for err == nil {
		select {
		case pr.frames <- m:
		case <-pr.quit:
		}

		m, err = pr.next(out)
	}

	close(pr.frames)
	_ = pr.cmd.Process.Kill() // one that stops talking runs nothing more
	_ = pr.cmd.Wait()         // once everything it wrote is read
}

// next reads the next frame of its job. One while it has no job, more than its job needs,
// not as it should be or of another job stops the process, unread past its length: a
// runner only says that when its code has escaped the engine (P1, P10). Its result ends
// its job: anything after it stops it too.
func (pr *proc) next(out io.Reader) (message, error) {
	n, err := readLength(out)
	if err != nil {
		return message{}, err // it ended
	}

	pr.mu.Lock()
	id, room := pr.job, pr.room
	pr.mu.Unlock()

	switch {
	case id == 0:
		return message{}, pr.refuse("it said something with no job to run")
	case n > room:
		return message{}, pr.refuse(fmt.Sprintf("a frame of %d bytes, more than the %d its job needs", n, room))
	}

	m, err := readBody(out, n)

	switch {
	case errors.Is(err, io.ErrUnexpectedEOF):
		return message{}, err // it ended
	case err != nil:
		return message{}, pr.refuse(err.Error())
	case m.ID != id:
		return message{}, pr.refuse(fmt.Sprintf("a %s of job %d, not of its job %d", m.Kind, m.ID, id))
	case m.Kind == kindResult:
		pr.expect(0, 0)
	}

	return m, nil
}

// expect says which job the process runs, none at zero, and how big its frames may be.
func (pr *proc) expect(id uint64, room int) {
	pr.mu.Lock()
	pr.job, pr.room = id, room
	pr.mu.Unlock()
}

// refuse kills the process for what it said, saying why.
func (pr *proc) refuse(why string) error {
	pr.mu.Lock()
	pr.why = why
	pr.mu.Unlock()

	_ = pr.cmd.Process.Kill()
	pr.stopped(why)

	return errors.New(why)
}

// refused is why the process was killed for what it said; empty when it was not.
func (pr *proc) refused() string {
	pr.mu.Lock()
	defer pr.mu.Unlock()

	return pr.why
}

// ready sends the prelude and waits for the runner's engine.
func (r *runner) ready(ctx context.Context, pr *proc) (message, error) {
	if err := pr.send(message{Kind: kindInit, Prelude: r.pool.cfg.Prelude}); err != nil {
		return message{}, fmt.Errorf("its init: %w: %s", err, r.lastLine())
	}

	t := time.NewTimer(startFor)
	defer t.Stop()

	select {
	case m, ok := <-pr.frames:
		switch {
		case !ok:
			return message{}, fmt.Errorf("it stopped as it started: %s", r.lastLine())
		case m.Kind != kindReady:
			return message{}, fmt.Errorf("a %s, not ready", m.Kind)
		}

		return m, nil
	case <-t.C:
		return message{}, fmt.Errorf("it did not start within %s", startFor)
	case <-ctx.Done():
		return message{}, ctx.Err()
	}
}

func (pr *proc) send(m message) error {
	if err := writeFrame(pr.in, m, maxFrame); err != nil {
		return err
	}

	return pr.in.Flush()
}

// stop ends the process and empties its temporary directory; again, it does nothing.
func (pr *proc) stop() {
	pr.once.Do(func() {
		close(pr.quit)
		_ = pr.stdin.Close()
		_ = pr.cmd.Process.Kill()
		<-pr.exited
		_ = os.RemoveAll(pr.temp)
	})
}

func (pr *proc) pid() int { return pr.cmd.Process.Pid }

// current is its process, nil when it has none that runs.
func (r *runner) current() *proc {
	r.mu.Lock()
	pr := r.proc
	r.mu.Unlock()

	if pr == nil {
		return nil
	}

	select {
	case <-pr.exited:
		return nil
	default:
		return pr
	}
}

func (r *runner) alive() bool { return r.current() != nil }

// stop ends its process, if it has one.
func (r *runner) stop() {
	r.mu.Lock()
	pr := r.proc
	r.mu.Unlock()

	if pr != nil {
		pr.stop()
	}
}

// run runs a job, its limits bounded, answering its host calls, until its result comes
// or it is stopped: past its time and grace, past its memory, past its host calls, or as
// ctx is done.
func (r *runner) run(ctx context.Context, j ports.CodeJob, limits ports.CodeLimits) (ports.CodeResult, error) {
	pr := r.current()
	if pr == nil {
		return ports.CodeResult{}, r.crashed(nil)
	}

	id := r.pool.jobs.Add(1)
	pr.expect(id, room(limits))

	if err := pr.send(message{Kind: kindJob, ID: id, Job: &job{
		Source: j.Source, Entry: j.Entry, Input: j.Input, Hosted: j.Host != nil, Limits: limits,
	}}); err != nil {
		return ports.CodeResult{}, r.crashed(pr)
	}

	deadline := time.NewTimer(limits.Time + grace)
	defer deadline.Stop()

	watch := time.NewTicker(r.pool.cfg.Watch)
	defer watch.Stop()

	most := memoryCap(limits.Memory, r.pool.cfg.Headroom)
	calls := 0 // answered, or asked past the job's most

	for {
		select {
		case m, ok := <-pr.frames:
			switch {
			case !ok:
				return ports.CodeResult{}, r.crashed(pr)
			case m.Kind == kindCall:
				// The engine asks no more than the job may: a runner that does has escaped
				// it, and is stopped, failing the job as the engine would (P10).
				if calls++; calls > limits.HostCalls {
					pr.stop()
					r.pool.stopped(fmt.Sprintf("it asked for data more than the %d times its job may", limits.HostCalls))

					return ports.CodeResult{HostCalls: calls}, &domain.CodeError{
						Kind:    domain.CodeTooManyCalls,
						Message: fmt.Sprintf("the code asked for data more than the %d times it may", limits.HostCalls),
					}
				}

				if err := pr.answer(ctx, j, m); err != nil {
					return ports.CodeResult{}, r.crashed(pr)
				}
			case m.Kind == kindResult && m.Result != nil:
				return r.outcome(pr, m.Result, limits, calls, most)
			default:
				pr.stop()
				return ports.CodeResult{}, fmt.Errorf("coderunner: the runner sent a %s, not a result", m.Kind)
			}
		case <-watch.C:
			if used, ok := sandbox.Memory(pr.pid()); ok && used > most {
				pr.stop()
				r.pool.stopped(fmt.Sprintf("it took %s of memory, past the %s it may", size(used), size(most)))

				return ports.CodeResult{}, &domain.CodeError{
					Kind:    domain.CodeOutOfMemory,
					Message: fmt.Sprintf("the code needed more than the %s of memory it may use", size(limits.Memory)),
				}
			}
		case <-deadline.C:
			pr.stop()
			r.pool.stopped("it ran past its job's time")

			return ports.CodeResult{}, &domain.CodeError{
				Kind: domain.CodeOutOfTime, Message: fmt.Sprintf("the code ran longer than the %s it may", limits.Time),
			}
		case <-ctx.Done():
			pr.stop() // its engine with it: started again for the next run
			return ports.CodeResult{}, ctx.Err()
		}
	}
}

// answer answers a host call the job's code made.
func (pr *proc) answer(ctx context.Context, j ports.CodeJob, m message) error {
	a := message{Kind: kindAnswer, Call: m.Call}

	if j.Host == nil {
		a.Error = "this code has no host calls" // never asked: the runner knows
	} else if value, err := j.Host(ctx, m.Name, m.Args); err != nil {
		a.Error = err.Error()
	} else {
		a.Value = value
	}

	return pr.send(a)
}

// outcome is a result as the runner's caller gets it, held to the job's limits as the
// engine holds it, which a runner whose code escaped it need not (P10): its logs to
// jsruntime's caps, its host calls to those answered and the most the engine counts, and
// its output to the job's, a runner past it being stopped. A process left past its memory
// is stopped too, and started again before the next job.
func (r *runner) outcome(pr *proc, res *result, limits ports.CodeLimits, calls int, most int64) (ports.CodeResult, error) {
	if used, ok := sandbox.Memory(pr.pid()); ok && used > most {
		pr.stop()
	}

	out := ports.CodeResult{
		Output: res.Output, Logs: held(res.Logs), HostCalls: max(calls, res.HostCalls),
	}

	switch {
	case res.Code != nil:
		out.Output = nil
		return out, res.Code.codeError()
	case res.Error != "":
		out.Output = nil
		return out, errors.New(res.Error)
	case len(res.Output) > limits.Output:
		pr.stop()
		r.pool.stopped(fmt.Sprintf("it gave %d bytes, more than the %d its job may", len(res.Output), limits.Output))

		out.Output = nil

		return out, &domain.CodeError{
			Kind:    domain.CodeOutputTooBig,
			Message: fmt.Sprintf("the code returned %s, more than the %s it may", size(int64(len(res.Output))), size(int64(limits.Output))),
		}
	}

	return out, nil
}

// held are a result's logs as jsruntime keeps them, which a runner keeps to already
// (P10): maxLogs lines, each of maxLogBytes and of a level of the engine's, then a line
// saying how many more there were, the runner's own when it is that line alone.
func held(l logs) []ports.CodeLog {
	lines, more := l.lines, l.more

	if len(lines) > maxLogs {
		extra := lines[maxLogs:]
		more += len(extra)

		if n, ok := dropped(extra[0]); ok && len(extra) == 1 && l.more == 0 {
			more = n
		}

		lines = lines[:maxLogs]
	}

	out := make([]ports.CodeLog, 0, len(lines)+1)

	for _, line := range lines {
		if !slices.Contains(logLevels, line.Level) {
			line.Level = "log"
		}

		if len(line.Text) > maxLogBytes && (len(line.Text) > maxLogBytes+len(cutLog) || !strings.HasSuffix(line.Text, cutLog)) {
			// A character cut in two at the end goes.
			line.Text = strings.ToValidUTF8(line.Text[:maxLogBytes], "") + cutLog
		}

		out = append(out, line)
	}

	if more > 0 {
		out = append(out, ports.CodeLog{Level: "info", Text: fmt.Sprintf(droppedLogs, more)})
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// dropped is how many lines jsruntime's last line says it did not keep; false when it is
// not that line.
func dropped(line ports.CodeLog) (int, bool) {
	var n int
	if _, err := fmt.Sscanf(line.Text, droppedLogs, &n); err != nil || n <= 0 {
		return 0, false
	}

	return n, line == ports.CodeLog{Level: "info", Text: fmt.Sprintf(droppedLogs, n)}
}

// logLevels are those the engine's lines may have, as jsruntime names them.
var logLevels = []string{"log", "info", "warn", "error"}

// crashed stops the process, which stopped or broke off, and is the run's error: the
// code may have done it, as when it stops the engine in the server.
func (r *runner) crashed(pr *proc) error {
	why := ""

	if pr != nil {
		pr.stop()
		why = pr.refused() // said, as it was stopped
	}

	if why == "" {
		why = r.lastLine()
		r.pool.stopped(why)
	}

	return &domain.CodeError{Kind: domain.CodeCrashed, Message: "the code's runner stopped: " + why}
}

func (r *runner) lastLine() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.last == "" {
		return "its process ended"
	}

	return r.last
}

// lines writes what a runner writes to stderr to the server's log, as much as the pool's
// quota takes, each line cut to maxLine, keeping its last line: a runner the code escaped
// into says what it likes there, as a plugin may (P4).
type lines struct {
	r *runner
}

// The runners' stderr the server's log takes, all of them together: each line cut to
// maxLine, linesBurst lines at once, then linesPerSecond.
const (
	maxLine        = 8 << 10
	linesBurst     = 100
	linesPerSecond = 10
)

func (l *lines) Write(b []byte) (int, error) {
	for line := range strings.SplitSeq(strings.TrimRight(string(b), "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if len(line) > maxLine {
				line = strings.ToValidUTF8(line[:maxLine], "") + " …"
			}

			if l.r.pool.said.take(l.r.pool.log) {
				l.r.pool.log.Info(context.Background(), "Code runner says", "line", line)
			}

			l.r.mu.Lock()
			l.r.last = line
			l.r.mu.Unlock()
		}
	}

	return len(b), nil
}

// quota is how many of the runners' lines the server's log still takes: the lines past
// it are dropped, and counted, their count logged a second after the first of them.
type quota struct {
	limit *rate.Limiter

	mu      sync.Mutex
	dropped int
}

// take is whether log may take one more line; when not, it is counted as dropped.
func (q *quota) take(log logging.Logger) bool {
	if q.limit.Allow() {
		return true
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if q.dropped++; q.dropped == 1 {
		time.AfterFunc(time.Second, func() { q.report(log) })
	}

	return false
}

// report logs how many lines were dropped since it last did.
func (q *quota) report(log logging.Logger) {
	q.mu.Lock()
	n := q.dropped
	q.dropped = 0
	q.mu.Unlock()

	log.Warn(context.Background(), "Code runners said too much: lines not logged", "dropped", n)
}

// size writes bytes for people, as jsruntime's errors do.
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
