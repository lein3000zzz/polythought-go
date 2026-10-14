// Package notify tells users about what happened while they were away, wherever they get
// messages: for now, their linked Telegram chat. Each kind of notification can be
// switched off.
package notify

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// Kind is a kind of notification a user can switch on or off.
type Kind struct {
	Key         string
	Label       string
	Description string
	Default     bool
}

var WorkflowFailed = Kind{
	Key:   "workflow_failed",
	Label: "A workflow fails",
	Description: "A run of one of your workflows failed. At most once an hour for each workflow, " +
		"and never for runs you start by hand.",
	Default: true,
}

// Kinds lists every kind, in the order Settings shows them.
var Kinds = []Kind{WorkflowFailed}

const (
	failureCooldown = time.Hour
	queueSize       = 256
	maxErrorText    = 300
	maxCooldowns    = 4096
)

var _ ports.ExecutionNotifier = (*Service)(nil)

type Service struct {
	messenger ports.UserMessenger
	prefs     ports.NotificationPrefRepository
	workflows ports.WorkflowRepository
	publicURL string
	log       logging.Logger
	now       func() time.Time

	queue chan failure

	mu        sync.Mutex
	cooldowns map[domain.WorkflowID]cooldown
}

type failure struct {
	owner domain.UserID
	exec  domain.Execution
}

// cooldown is when a workflow's failure was last told, and how many were not since.
type cooldown struct {
	at     time.Time
	missed int
}

type Option func(*Service)

// WithClock replaces the clock, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// New makes the service. publicURL is where users open the app; messages link to it
// when it is https, which Telegram requires of link buttons.
func New(messenger ports.UserMessenger, prefs ports.NotificationPrefRepository, workflows ports.WorkflowRepository,
	publicURL string, log logging.Logger, opts ...Option,
) *Service {
	s := &Service{
		messenger: messenger,
		prefs:     prefs,
		workflows: workflows,
		publicURL: strings.TrimRight(publicURL, "/"),
		log:       log.With("component", "notify"),
		now:       time.Now,
		queue:     make(chan failure, queueSize),
		cooldowns: make(map[domain.WorkflowID]cooldown),
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Pref is a kind of notification and whether the user gets it.
type Pref struct {
	Kind

	Enabled bool
}

func (s *Service) Prefs(ctx context.Context, p auth.Principal) ([]Pref, error) {
	changed, err := s.prefs.Prefs(ctx, p.User.ID)
	if err != nil {
		return nil, err
	}

	out := make([]Pref, len(Kinds))

	for i, k := range Kinds {
		enabled, ok := changed[k.Key]
		if !ok {
			enabled = k.Default
		}

		out[i] = Pref{Kind: k, Enabled: enabled}
	}

	return out, nil
}

func (s *Service) SetPref(ctx context.Context, p auth.Principal, key string, enabled bool) (Pref, error) {
	for _, k := range Kinds {
		if k.Key != key {
			continue
		}

		if err := s.prefs.SetPref(ctx, p.User.ID, key, enabled); err != nil {
			return Pref{}, err
		}

		return Pref{Kind: k, Enabled: enabled}, nil
	}

	return Pref{}, fmt.Errorf("%w: no kind of notification %q", domain.ErrNotFound, key)
}

func (s *Service) enabled(ctx context.Context, user domain.UserID, k Kind) (bool, error) {
	changed, err := s.prefs.Prefs(ctx, user)
	if err != nil {
		return false, err
	}

	if enabled, ok := changed[k.Key]; ok {
		return enabled, nil
	}

	return k.Default, nil
}

// ExecutionFinished queues failed executions to tell their owners about. It never
// blocks the execution: when the queue is full, the failure goes untold.
func (s *Service) ExecutionFinished(owner domain.UserID, e domain.Execution) {
	if e.Status != domain.ExecutionFailed {
		return
	}

	select {
	case s.queue <- failure{owner: owner, exec: e}:
	default:
		s.log.Warn(context.Background(), "Failure notification dropped: too many at once", "workflow_id", e.WorkflowID)
	}
}

// Run sends the queued notifications until ctx is done.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case f := <-s.queue:
			s.workflowFailed(ctx, f)
		}
	}
}

func (s *Service) workflowFailed(ctx context.Context, f failure) {
	w, err := s.workflows.Get(ctx, f.owner, f.exec.WorkflowID)
	if errors.Is(err, domain.ErrNotFound) {
		return // deleted since
	}

	if err != nil {
		s.log.Warn(ctx, "Failure notification skipped", "workflow_id", f.exec.WorkflowID, logging.Err(err))
		return
	}

	// Whoever started it by hand is looking at the result.
	if trigger, ok := w.Node(f.exec.TriggeredBy); ok && trigger.Type == triggers.ManualType {
		return
	}

	on, err := s.enabled(ctx, f.owner, WorkflowFailed)
	if err != nil || !on {
		return
	}

	missed, tell := s.due(w.ID)
	if !tell {
		return
	}

	text := "⚠️ <b>" + html.EscapeString(w.Name) + "</b> failed."

	if missed > 0 {
		text += fmt.Sprintf(" It failed %d more %s since the last message.", missed, plural(missed, "time", "times"))
	}

	if f.exec.Error != "" {
		text += "\n<code>" + html.EscapeString(truncate(f.exec.Error, maxErrorText)) + "</code>"
	}

	var buttons [][]ports.TelegramButton
	if strings.HasPrefix(s.publicURL, "https://") {
		buttons = [][]ports.TelegramButton{{{Text: "Open workflow", URL: s.publicURL + "/workflows/" + string(w.ID)}}}
	}

	err = s.messenger.MessageUser(ctx, f.owner, ports.UserMessage{HTML: text, Buttons: buttons})

	switch {
	case err == nil:
	case errors.Is(err, domain.ErrTelegramNotLinked), errors.Is(err, domain.ErrTelegramUnavailable):
		s.log.Debug(ctx, "Failure not told: nowhere to send it", "user_id", f.owner)
	default:
		s.log.Warn(ctx, "Failure notification not sent", "user_id", f.owner, logging.Err(err))
	}
}

// due reports whether a failure of the workflow is told now, and how many were not since
// the last one that was.
func (s *Service) due(id domain.WorkflowID) (missed int, tell bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()

	c, seen := s.cooldowns[id]
	if seen && now.Sub(c.at) < failureCooldown {
		c.missed++
		s.cooldowns[id] = c

		return 0, false
	}

	if len(s.cooldowns) >= maxCooldowns {
		for wid, c := range s.cooldowns {
			if now.Sub(c.at) >= failureCooldown {
				delete(s.cooldowns, wid)
			}
		}
	}

	s.cooldowns[id] = cooldown{at: now}

	return c.missed, true
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}

	return many
}

func truncate(s string, most int) string {
	if utf8.RuneCountInString(s) <= most {
		return s
	}

	return string([]rune(s)[:most-1]) + "…"
}
