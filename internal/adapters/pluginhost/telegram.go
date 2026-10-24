package pluginhost

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/pkg/plugin"
	pluginv1 "github.com/lein3000zzz/polythought-go/pkg/plugin/v1"
)

// Telegram is how plugins reach the workflow's owner (D291, D307): one left nil makes
// its calls fail; without Reminder, questions get no reminders.
type Telegram struct {
	Messenger ports.UserMessenger
	Asker     ports.UserAsker
	Reminder  ports.QuestionReminder
}

const (
	maxMessage  = 4096 // characters Telegram takes in a message
	closingRoom = 64   // what a question keeps free for the answer it closes with
	maxOptions  = 10
	maxOption   = 40 // characters of an option's button
	maxState    = 64 << 10

	defaultTimeout = time.Hour
	closeFor       = 10 * time.Second // to close a question, the run done or not
	// remindFrom is the shortest question that is reminded of, at three quarters of its
	// time, as Telegram Ask's are (D31).
	remindFrom = 10 * time.Minute
)

// SendMessage messages the owner, after the plugin's name (D308).
func (a *api) SendMessage(ctx context.Context, req *pluginv1.SendMessageRequest) (*pluginv1.SendMessageResponse, error) {
	return call(ctx, a, req.GetSession(), opMessage, func(ctx context.Context, s *session, _ trace.Span) (*pluginv1.SendMessageResponse, error) {
		switch {
		case !a.perms.telegram:
			return nil, status.Errorf(codes.PermissionDenied, "telegram: its permissions do not name it (%s)", plugin.PermTelegram)
		case a.deps.Telegram.Messenger == nil:
			return nil, errNoSource
		}

		text, err := a.marked(req.GetText(), req.GetHtml(), maxMessage)
		if err != nil {
			return nil, err
		}

		err = a.deps.Telegram.Messenger.MessageUser(ctx, s.rc.OwnerID, ports.UserMessage{HTML: text, Silent: req.GetSilent()})
		if err != nil {
			return nil, a.telegramError(ctx, s, err)
		}

		return &pluginv1.SendMessageResponse{}, nil
	})
}

// AskQuestion asks the owner, once a node's run: the run then waits on it (proxyNode).
func (a *api) AskQuestion(ctx context.Context, req *pluginv1.AskQuestionRequest) (*pluginv1.AskQuestionResponse, error) {
	return call(ctx, a, req.GetSession(), opAsk, func(ctx context.Context, s *session, _ trace.Span) (*pluginv1.AskQuestionResponse, error) {
		switch {
		case !a.perms.telegram:
			return nil, status.Errorf(codes.PermissionDenied, "telegram: its permissions do not name it (%s)", plugin.PermTelegram)
		case s.trigger:
			return nil, status.Error(codes.FailedPrecondition, "a trigger cannot ask: only a node's run waits")
		case a.deps.Telegram.Asker == nil:
			return nil, errNoSource
		}

		q, err := a.question(req, time.Now())
		if err != nil {
			return nil, err
		}

		if err := s.ask(); err != nil {
			return nil, err
		}

		q.Message, err = a.deps.Telegram.Asker.AskUser(ctx, s.rc.OwnerID, ports.Question{
			HTML: q.HTML, Options: q.Options, ExecutionID: s.rc.ExecutionID, Key: q.Key,
		})

		if waited := s.sent(q, err); err == nil && !waited {
			// Sent once the plugin had answered the run, which no longer waits.
			a.closeQuestion(ctx, s.rc.OwnerID, q, notWaited)
		}

		if err != nil {
			return nil, a.telegramError(ctx, s, err)
		}

		return &pluginv1.AskQuestionResponse{Deadline: timeText(q.Deadline)}, nil
	})
}

// question is a question the host asked for a plugin: what it keeps while the run waits.
type question struct {
	Message  int       `json:"message"`
	HTML     string    `json:"html"` // as it was sent
	Options  []string  `json:"options"`
	Key      string    `json:"key"`
	Asked    time.Time `json:"asked"`
	Deadline time.Time `json:"deadline"`
	Remind   bool      `json:"remind"` // the reminder is still to come
}

// question reads what a plugin asks, checked: its text, its options, its time.
func (a *api) question(req *pluginv1.AskQuestionRequest, now time.Time) (question, error) {
	options := req.GetOptions()

	switch {
	case len(options) == 0 || len(options) > maxOptions:
		return question{}, status.Errorf(codes.InvalidArgument, "options: 1 to %d", maxOptions)
	case slices.ContainsFunc(options, func(o string) bool { return strings.TrimSpace(o) == "" || utf8.RuneCountInString(o) > maxOption }):
		return question{}, status.Errorf(codes.InvalidArgument, "an option: 1 to %d characters", maxOption)
	case len(slices.Compact(slices.Sorted(slices.Values(options)))) != len(options):
		return question{}, status.Error(codes.InvalidArgument, "an option twice")
	}

	timeout := defaultTimeout

	if t := req.GetTimeout(); t != "" {
		var err error
		if timeout, err = time.ParseDuration(t); err != nil || timeout < time.Minute {
			return question{}, status.Error(codes.InvalidArgument, "timeout: a duration of a minute or more, like 30m")
		}
	}

	text, err := a.marked(req.GetText(), req.GetHtml(), maxMessage-closingRoom)
	if err != nil {
		return question{}, err
	}

	key, err := newKey()
	if err != nil {
		return question{}, status.Error(codes.Internal, "a question's key")
	}

	return question{
		HTML: text, Options: slices.Clone(options), Key: key, Asked: now, Deadline: now.Add(timeout),
		Remind: a.deps.Telegram.Reminder != nil && timeout >= remindFrom,
	}, nil
}

// until is when the wait on q ends: its deadline, or its reminder, at three quarters of
// its time.
func (q question) until() time.Time {
	if !q.Remind {
		return q.Deadline
	}

	return q.Asked.Add(q.Deadline.Sub(q.Asked) * 3 / 4)
}

// ask reserves the run's one question.
func (s *session) ask() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case s.question != nil || s.asking:
		return status.Error(codes.FailedPrecondition, "a run asks one question")
	case s.settled:
		return status.Error(codes.FailedPrecondition, "the run is over: the plugin answered it")
	}

	s.asking = true

	return nil
}

// sent records the run's question, or frees its place when it could not be sent; it
// says whether the run waits on it, which it does not once settled.
func (s *session) sent(q question, err error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.asking = false
	if err == nil {
		s.question = &q
	}

	return !s.settled
}

// marked is a message as the owner gets it: the plugin's name first, then the text
// escaped, cut to fit, or the plugin's HTML, refused when it does not fit.
func (a *api) marked(text string, isHTML bool, most int) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", status.Error(codes.InvalidArgument, "the message is empty")
	}

	mark := "🧩 <b>" + html.EscapeString(a.name) + "</b>\n"

	room := most - utf8.RuneCountInString(mark)
	if room < 1 {
		// Check bounds the name, but no name may cut the text out of range (P2).
		return "", status.Error(codes.FailedPrecondition, "the plugin's name leaves no room for a message")
	}

	if !isHTML {
		if utf8.RuneCountInString(text) > room {
			text = string([]rune(text)[:room-1]) + "…"
		}

		return mark + html.EscapeString(text), nil
	}

	if utf8.RuneCountInString(text) > room {
		return "", status.Errorf(codes.InvalidArgument, "the message is longer than the %d characters there are room for, after the plugin's name", room)
	}

	return mark + text, nil
}

// waiting is what a waiting run keeps, in the engine's wait: the question, and the
// plugin's state, as it gave it.
type waiting struct {
	Question question `json:"question"`
	State    string   `json:"state"`
}

func (w waiting) encode() map[string]any {
	raw, _ := json.Marshal(w)

	var m map[string]any
	_ = json.Unmarshal(raw, &m)

	return m
}

func decodeWaiting(m map[string]any) (waiting, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return waiting{}, err
	}

	var w waiting
	if err := json.Unmarshal(raw, &w); err != nil {
		return waiting{}, fmt.Errorf("what the run kept as it waited: %w", err)
	}

	return w, nil
}

// waitOn is the engine's wait for the question the run asked, the plugin's state kept.
func waitOn(q question, state []byte) (*engine.Wait, error) {
	if len(state) > maxState {
		return nil, fmt.Errorf("its state is larger than the %d bytes a wait may keep", maxState)
	}

	return &engine.Wait{Until: q.until(), Key: q.Key, Question: q.asked(), State: waiting{Question: q, State: string(state)}.encode()}, nil
}

// asked is the question as an answer elsewhere sees it (D405).
func (q question) asked() *ports.WaitQuestion {
	return &ports.WaitQuestion{HTML: q.HTML, Options: slices.Clone(q.Options)}
}

// closeQuestion takes a question's buttons away, saying how it ended: a question left
// open does not change that.
func (a *api) closeQuestion(ctx context.Context, owner domain.UserID, q question, closing string) {
	if a.deps.Telegram.Asker == nil {
		return
	}

	// Closed even when the run was cancelled: its buttons would answer nothing.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeFor)
	defer cancel()

	_ = a.deps.Telegram.Asker.CloseQuestion(ctx, owner, q.Message, q.HTML+"\n\n"+closing)
}

// telegramError is why Telegram failed, as the plugin hears it (P13).
func (a *api) telegramError(ctx context.Context, s *session, err error) error {
	if errors.Is(err, domain.ErrTelegramNotLinked) {
		return status.Error(codes.FailedPrecondition, domain.ErrTelegramNotLinked.Error())
	}

	return a.sourceError(ctx, s, "telegram", err)
}

// newKey is a random key for a question's wait, safe in button data.
func newKey() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}
