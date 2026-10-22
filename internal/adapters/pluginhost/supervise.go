package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// State is how a plugin's process is.
type State string

const (
	Running    State = "running"
	Restarting State = "restarting" // it crashed, and is started again after a while
	Crashed    State = "crashed"    // it crashed too often, and waits for an admin (D320)
	Stopped    State = "stopped"
)

// Restarts are how a supervised plugin is started again after it crashes (D320): after
// First, doubling with each crash to Max, until Crashes within Within stop it.
type Restarts struct {
	First   time.Duration
	Max     time.Duration
	Crashes int
	Within  time.Duration

	// Every is how often its process is looked at.
	Every time.Duration
}

// DefaultRestarts are D320's.
var DefaultRestarts = Restarts{First: time.Second, Max: 5 * time.Minute, Crashes: 5, Within: 10 * time.Minute, Every: 500 * time.Millisecond}

// Watch is what a supervision tells and asks: Check, before each start, that the
// plugin's files are still as approved; Crashed, once it stops it as crashed.
type Watch struct {
	Check   func() error
	Crashed func(err error)
}

// logLines are the lines of a plugin's own log it keeps.
const logLines = 200

// Status is how a plugin is: its state since when, its crashes in the supervision's
// window, its last error, and its last lines.
type Status struct {
	State     State
	Since     time.Time
	Crashes   int
	LastError string
	Log       []string
}

func (p *Plugin) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()

	return Status{State: p.state, Since: p.since, Crashes: len(p.crashes), LastError: p.lastErr, Log: p.out.lines()}
}

// Supervise starts the plugin again whenever its process crashes, as r says, until ctx is
// done or it is closed: past r's crashes, it stops it as crashed and tells w.Crashed, and
// waits for Start.
func (p *Plugin) Supervise(ctx context.Context, r Restarts, w Watch) {
	go p.supervise(ctx, r, w)
}

// Start starts a plugin stopped as crashed again, its crashes forgotten.
func (p *Plugin) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.state != Crashed {
		return fmt.Errorf("plugin %s is %s, not crashed", p.manifest.ID, p.state)
	}

	p.crashes = nil

	select {
	case p.wake <- struct{}{}:
	default:
	}

	return nil
}

func (p *Plugin) supervise(ctx context.Context, r Restarts, w Watch) {
	tick := time.NewTicker(r.Every)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}

		p.mu.Lock()
		closed := p.closed
		crashed := p.conn != nil && p.conn.client.Exited()
		p.mu.Unlock()

		switch {
		case closed:
			return
		case !crashed:
			continue
		}

		err := errors.New(p.out.last("its process exited"))

		for err != nil {
			if !p.crash(err, r, w) {
				if !p.waitForStart(ctx) {
					return
				}
			} else if !p.sleep(ctx, p.backoff(r)) {
				return
			}

			err = p.restart(ctx, w)
		}
	}
}

// crash records a crash: false when it is one too many, the plugin stopped as crashed.
func (p *Plugin) crash(err error, r Restarts, w Watch) bool {
	now := time.Now()

	p.mu.Lock()

	if old := p.conn; old != nil {
		defer old.client.Kill() // what is left of it
	}

	p.conn = nil
	p.lastErr = err.Error()
	p.crashes = append(slices.DeleteFunc(p.crashes, func(at time.Time) bool { return now.Sub(at) > r.Within }), now)

	if len(p.crashes) < r.Crashes {
		p.state, p.since = Restarting, now
		p.mu.Unlock()

		p.log.Warn(context.Background(), "Plugin crashed, starting it again", "error", err.Error(), "crashes", len(p.crashes))

		return true
	}

	p.state, p.since = Crashed, now
	p.mu.Unlock()

	p.log.Error(context.Background(), "Plugin crashed too often, stopped", "error", err.Error(), "crashes", len(p.crashes))

	if w.Crashed != nil {
		w.Crashed(err)
	}

	return false
}

// backoff is how long to wait before the next start: First, doubling with each crash in
// the window, Max at most.
func (p *Plugin) backoff(r Restarts) time.Duration {
	p.mu.Lock()
	n := len(p.crashes)
	p.mu.Unlock()

	d := r.First
	for i := 1; i < n && d < r.Max; i++ {
		d *= 2
	}

	return min(d, r.Max)
}

// restart starts the plugin's process again: refused when its files are no longer as
// approved, or it describes itself otherwise than it did.
func (p *Plugin) restart(ctx context.Context, w Watch) error {
	if w.Check != nil {
		if err := w.Check(); err != nil {
			return fmt.Errorf("not started again: %w", err)
		}
	}

	c, m, err := p.connect(ctx)
	if err != nil {
		return fmt.Errorf("not started again: %w", err)
	}

	if !Same(m, p.manifest) {
		c.client.Kill()
		return errors.New("not started again: it describes itself otherwise than when it first started")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		c.client.Kill()
		return nil
	}

	p.conn, p.state, p.since = c, Running, time.Now()

	p.log.Info(ctx, "Plugin started again")

	return nil
}

func (p *Plugin) waitForStart(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-p.wake:
		return true
	}
}

func (p *Plugin) sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// ring keeps the last lines written to it.
type ring struct {
	mu    sync.Mutex
	buf   []string
	next  int
	full  bool
	limit int
}

func newRing(limit int) *ring { return &ring{buf: make([]string, limit), limit: limit} }

func (r *ring) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.buf[r.next] = clip(line)
	r.next = (r.next + 1) % r.limit
	r.full = r.full || r.next == 0
}

// lines are the lines kept, oldest first.
func (r *ring) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.full {
		return slices.Clone(r.buf[:r.next])
	}

	return append(slices.Clone(r.buf[r.next:]), r.buf[:r.next]...)
}

// last is the last line kept, or none when there is none.
func (r *ring) last(none string) string {
	lines := r.lines()
	if len(lines) == 0 {
		return none
	}

	return lines[len(lines)-1]
}
