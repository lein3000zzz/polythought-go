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
	"strings"
	"sync"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

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
)

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
}

// Pool runs user code in runner processes (D353): a warm pool, each runner running one
// job at a time; one that stops fails only its own run, and is started again for the next.
type Pool struct {
	cfg    Config
	log    logging.Logger
	kit    *redkit.Kit
	scope  tracing.Scope
	idle   chan *runner
	shares jsruntime.Shares

	mu       sync.Mutex
	all      []*runner
	restarts int    // runners started again, after they stopped or were stopped
	lastErr  string // why one last stopped, or would not start
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

	p := &Pool{
		cfg: cfg, log: util.Logger.With("component", "coderunner"), kit: kit,
		scope: tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		idle:  make(chan *runner, cfg.Size),
	}

	var (
		wg   sync.WaitGroup
		errs = make([]error, cfg.Size)
	)

	for i := range cfg.Size {
		r := &runner{pool: p}
		p.all = append(p.all, r)

		wg.Go(func() { errs[i] = r.start(ctx) })
	}

	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		_ = p.Close(ctx)
		return nil, fmt.Errorf("coderunner: %w", err)
	}

	for _, r := range p.all {
		p.idle <- r
	}

	return p, nil
}

// Sandboxed says the runners run under the sandbox.
func (p *Pool) Sandboxed() bool { return p.cfg.Sandbox != nil }

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

	// No owner holds more than their share of the runners (R12), then a runner.
	share, err := p.shares.Take(ctx, ports.CodeOwner(ctx), cap(p.idle))
	if err != nil {
		return ports.CodeResult{}, err
	}
	defer share()

	var r *runner

	select {
	case r = <-p.idle:
	case <-ctx.Done():
		return ports.CodeResult{}, ctx.Err()
	}
	defer func() { p.idle <- r }()

	if !r.alive() {
		if err := p.restart(ctx, r); err != nil {
			return ports.CodeResult{}, err
		}
	}

	return r.run(ctx, j, limits)
}

// restart starts a runner that stopped again.
func (p *Pool) restart(ctx context.Context, r *runner) error {
	p.mu.Lock()
	closed := p.closed
	p.restarts++
	p.mu.Unlock()

	if closed {
		return errors.New("coderunner: the pool is closed")
	}

	if err := r.start(ctx); err != nil {
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

	mu   sync.Mutex
	proc *proc
	last string // the last line its process wrote to stderr
}

// proc is a runner's process.
type proc struct {
	cmd    *exec.Cmd
	in     *bufio.Writer
	stdin  io.Closer
	frames chan message  // what it says, closed once it says no more
	exited chan struct{} // closed once it has exited
	quit   chan struct{} // closed as it is stopped: what it says then goes unread
	once   sync.Once
	temp   string
}

// start starts its process, under the sandbox where there is one, and waits for its engine.
func (r *runner) start(ctx context.Context) error {
	temp, err := os.MkdirTemp("", "polythought-runner-")
	if err != nil {
		return fmt.Errorf("its temporary directory: %w", err)
	}

	var cmd *exec.Cmd

	if sb := r.pool.cfg.Sandbox; sb != nil {
		cmd, err = sb.Command(sandbox.Policy{Entry: Entry.Name, Temp: temp})
	} else {
		cmd, err = sandbox.EntryCommand(Entry.Name)
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
		cmd: cmd, in: bufio.NewWriter(stdin), stdin: stdin, temp: temp,
		frames: make(chan message), exited: make(chan struct{}), quit: make(chan struct{}),
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

// read passes on what the process says until it ends, then waits for it.
func (pr *proc) read(stdout io.Reader) {
	defer close(pr.exited)

	out := bufio.NewReader(stdout)

	for {
		m, err := readFrame(out)
		if err != nil {
			break
		}

		select {
		case pr.frames <- m:
		case <-pr.quit:
		}
	}

	close(pr.frames)
	_ = pr.cmd.Wait() // once everything it wrote is read
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
	if err := writeFrame(pr.in, m); err != nil {
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
// or it is stopped: past its time and grace, past its memory, or as ctx is done.
func (r *runner) run(ctx context.Context, j ports.CodeJob, limits ports.CodeLimits) (ports.CodeResult, error) {
	pr := r.current()
	if pr == nil {
		return ports.CodeResult{}, r.crashed(nil)
	}

	if err := pr.send(message{Kind: kindJob, Job: &job{
		Source: j.Source, Entry: j.Entry, Input: j.Input, Hosted: j.Host != nil, Limits: limits,
	}}); err != nil {
		return ports.CodeResult{}, r.crashed(pr)
	}

	deadline := time.NewTimer(limits.Time + grace)
	defer deadline.Stop()

	watch := time.NewTicker(r.pool.cfg.Watch)
	defer watch.Stop()

	most := memoryCap(limits.Memory, r.pool.cfg.Headroom)

	for {
		select {
		case m, ok := <-pr.frames:
			switch {
			case !ok:
				return ports.CodeResult{}, r.crashed(pr)
			case m.Kind == kindCall:
				if err := pr.answer(ctx, j, m); err != nil {
					return ports.CodeResult{}, r.crashed(pr)
				}
			case m.Kind == kindResult && m.Result != nil:
				return pr.outcome(m.Result, most)
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

// outcome is a result as the runner's caller gets it. A process left past its memory is
// stopped, and started again before the next job.
func (pr *proc) outcome(res *result, most int64) (ports.CodeResult, error) {
	if used, ok := sandbox.Memory(pr.pid()); ok && used > most {
		pr.stop()
	}

	switch {
	case res.Code != nil:
		return res.codeResult(), res.Code
	case res.Error != "":
		return res.codeResult(), errors.New(res.Error)
	}

	return res.codeResult(), nil
}

// crashed stops the process, which stopped or broke off, and is the run's error: the
// code may have done it, as when it stops the engine in the server.
func (r *runner) crashed(pr *proc) error {
	if pr != nil {
		pr.stop()
	}

	why := r.lastLine()
	r.pool.stopped(why)

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

// lines writes what a runner writes to stderr to the server's log, keeping its last line.
type lines struct {
	r *runner
}

func (l *lines) Write(b []byte) (int, error) {
	for line := range strings.SplitSeq(strings.TrimRight(string(b), "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			l.r.pool.log.Info(context.Background(), "Code runner says", "line", line)

			l.r.mu.Lock()
			l.r.last = line
			l.r.mu.Unlock()
		}
	}

	return len(b), nil
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
