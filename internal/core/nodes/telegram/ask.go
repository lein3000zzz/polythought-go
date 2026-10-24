package telegram

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	AskType     = "telegram.ask"
	TimeoutPort = "timeout"

	// remindFrom is the shortest timeout that gets a reminder, sent when a quarter of the
	// time is left.
	remindFrom = 10 * time.Minute

	// closingRoom is kept free in the question for what closing it adds.
	closingRoom = 64
)

var _ engine.Resumer = (*Ask)(nil)

// Ask asks the workflow's owner a question on Telegram, a button per option, and waits.
// The items go on through the port of the option picked, or through timeout.
type Ask struct {
	asker    ports.UserAsker
	reminder ports.QuestionReminder // nil: no reminders
	now      func() time.Time
}

func NewAsk(asker ports.UserAsker, reminder ports.QuestionReminder, now func() time.Time) *Ask {
	return &Ask{asker: asker, reminder: reminder, now: now}
}

func (*Ask) Type() engine.NodeType {
	return engine.NodeType{
		Name:  AskType,
		Label: "Telegram question",
		Description: "Asks you on Telegram, with a button per answer, and waits. The items go on through the port " +
			"of the button you press, or through timeout. Link Telegram in Settings first.",
		Inputs:       []string{engine.DefaultPort},
		Outputs:      []string{TimeoutPort},
		Items:        shape.Passed(),
		OutputsParam: "buttons",
		Params: []engine.ParamSpec{
			{
				Name: "message", Template: true, Label: "Question", Kind: engine.ParamString, Default: "Go ahead?",
				Description: "{{field}} puts in an item's field, e.g. {{question}}. Several items make a line each; one answer goes for all of them.",
			},
			{
				Name: "format", Label: "Format", Kind: engine.ParamString, Default: "text", Options: []string{"text", "html"},
				Description: "text: sent as written. html: Telegram's tags work, such as <b>, <i>, <code> and <a href=\"…\">; item fields are escaped.",
			},
			{
				Name: "buttons", Label: "Buttons", Kind: engine.ParamList, Default: []string{"Yes", "No"},
				Description: "One per line, up to 10, at most 40 characters each. Each button is an output port of the same name.",
			},
			{
				Name: "timeout", Label: "Timeout", Kind: engine.ParamDuration, Default: "1h",
				Description: "How long to wait for a press before the items go through timeout, e.g. 30m or 24h. " +
					"At most the instance's longest wait, 7 days unless an admin changed it.",
			},
		},
	}
}

func (n *Ask) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	items := in[engine.DefaultPort]
	if len(items) == 0 {
		return engine.Ports{}, nil
	}

	p, err := rc.Params.Resolve(engine.ItemRef{Port: engine.DefaultPort})
	if err != nil {
		return nil, err
	}

	var cfg sendConfig

	for name, dst := range map[string]*string{"format": &cfg.format} {
		if *dst, err = p.String(name); err != nil {
			return nil, err
		}
	}

	options, err := p.Strings("buttons")
	if err != nil {
		return nil, err
	}

	timeout, err := p.Duration("timeout")
	if err != nil {
		return nil, err
	}

	refs := make([]engine.ItemRef, len(items))
	for i := range items {
		refs[i] = engine.ItemRef{Port: engine.DefaultPort, Index: i}
	}

	question, err := render(rc, cfg, items, refs, maxLength-closingRoom)
	if err != nil {
		return nil, err
	}

	key, err := newKey()
	if err != nil {
		return nil, err
	}

	asked := n.now()

	id, err := n.asker.AskUser(ctx, rc.OwnerID, ports.Question{HTML: question, Options: options, ExecutionID: rc.ExecutionID, Key: key})
	if err != nil {
		return nil, err
	}

	st := askState{Message: id, Question: question, Options: options, Key: key, Deadline: asked.Add(timeout)}

	until := st.Deadline
	if n.reminder != nil && timeout >= remindFrom {
		st.Remind = true
		until = asked.Add(timeout * 3 / 4)
	}

	return nil, &engine.Wait{Until: until, Key: key, Question: st.asked(), State: st.encode()}
}

// Resume reminds once a quarter of the time is left, then sends the items on through the
// option picked or through timeout, and closes the question saying which.
func (n *Ask) Resume(ctx context.Context, rc engine.RunContext, in engine.Ports, r engine.Resumption) (engine.Ports, error) {
	st, err := decodeAskState(r.State)
	if err != nil {
		return nil, err
	}

	if r.TimedOut && st.Remind {
		// A reminder that does not arrive is no reason to give up on the answer.
		_ = n.reminder.RemindQuestion(ctx, rc.OwnerID, rc.WorkflowID, st.Message, st.Deadline.Sub(n.now()))

		st.Remind = false

		return nil, &engine.Wait{Until: st.Deadline, Key: st.Key, Question: st.asked(), State: st.encode()}
	}

	port, closing := TimeoutPort, "⌛ <i>No answer in time.</i>"

	if !r.TimedOut {
		i, err := engine.Params(r.Payload).Int("option")
		if err != nil || i < 0 || i >= len(st.Options) {
			return nil, fmt.Errorf("an answer for button %v, of %d", r.Payload["option"], len(st.Options))
		}

		port, closing = st.Options[i], "✅ <b>"+html.EscapeString(st.Options[i])+"</b>"+answeredBy(r)
	}

	// Like the reminder, a question left open does not change the answer.
	_ = n.asker.CloseQuestion(ctx, rc.OwnerID, st.Message, st.Question+"\n\n"+closing)

	out := engine.Ports{}
	for i, it := range in[engine.DefaultPort] {
		out.Add(port, engine.Item{JSON: it.JSON, Source: []engine.ItemRef{{Port: engine.DefaultPort, Index: i}}})
	}

	return out, nil
}

// answeredBy says who answered for the owner, when someone did.
func answeredBy(r engine.Resumption) string {
	if by := r.By(); by != "" {
		return ", <i>answered by " + html.EscapeString(by) + "</i>"
	}

	return ""
}

// askState is what a question keeps while it waits.
type askState struct {
	Message  int
	Question string
	Options  []string
	Key      string
	Deadline time.Time
	Remind   bool // the reminder is still to come
}

// asked is the question as an answer elsewhere sees it.
func (s askState) asked() *ports.WaitQuestion {
	return &ports.WaitQuestion{HTML: s.Question, Options: s.Options}
}

func (s askState) encode() map[string]any {
	return map[string]any{
		"message": s.Message, "question": s.Question, "options": s.Options, "key": s.Key,
		"deadline": s.Deadline.UTC().Format(time.RFC3339Nano), "remind": s.Remind,
	}
}

func decodeAskState(m map[string]any) (askState, error) {
	p := engine.Params(m)

	var (
		s   askState
		err error
	)

	if s.Message, err = p.Int("message"); err != nil {
		return askState{}, err
	}

	if s.Question, err = p.String("question"); err != nil {
		return askState{}, err
	}

	if s.Options, err = p.Strings("options"); err != nil {
		return askState{}, err
	}

	if s.Key, err = p.String("key"); err != nil {
		return askState{}, err
	}

	deadline, err := p.String("deadline")
	if err != nil {
		return askState{}, err
	}

	if s.Deadline, err = time.Parse(time.RFC3339Nano, deadline); err != nil {
		return askState{}, fmt.Errorf("the question's deadline: %w", err)
	}

	if s.Remind, err = p.Bool("remind"); err != nil {
		return askState{}, err
	}

	return s, nil
}

// newKey is a random key for a question's wait, safe in button data.
func newKey() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}
