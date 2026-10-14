// Package telegram holds the nodes that use the instance's Telegram bot: sending
// messages to the workflow's owner, and starting workflows on the owner's messages.
// They reach only the owner's own linked chat.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"unicode/utf8"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/fields"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	SendType    = "telegram.send"
	TriggerType = "trigger.telegram"

	// maxMessages bounds the messages one run sends, so a long list cannot flood a chat.
	maxMessages = 20

	// maxLength is the most characters Telegram shows in one message.
	maxLength = 4096
)

var (
	_ engine.Node    = (*Send)(nil)
	_ engine.Trigger = (*Trigger)(nil)
)

// Send writes items to the workflow owner's Telegram chat and passes on the items it
// delivered.
type Send struct {
	messenger ports.UserMessenger
}

func NewSend(messenger ports.UserMessenger) *Send {
	return &Send{messenger: messenger}
}

func (*Send) Type() engine.NodeType {
	return engine.NodeType{
		Name:        SendType,
		Label:       "Telegram message",
		Description: "Sends items to you on Telegram, in your chat with the instance's bot. Link Telegram in Settings first.",
		Inputs:      []string{engine.DefaultPort},
		Outputs:     []string{engine.DefaultPort},
		Params: []engine.ParamSpec{
			{
				Name: "message", Label: "Message", Kind: engine.ParamString, Default: "Update from your Polythought workflow",
				Description: "{{field}} puts in an item's field, e.g. {{question}} or {{spread}}.",
			},
			{
				Name: "format", Label: "Format", Kind: engine.ParamString, Default: "text", Options: []string{"text", "html"},
				Description: "text: sent as written. html: Telegram's tags work, such as <b>, <i>, <code> and <a href=\"…\">; item fields are escaped.",
			},
			{
				Name: "send", Label: "Send", Kind: engine.ParamString, Default: "all", Options: []string{"all", "each"},
				Description: "all: one message listing every item. each: one message per item, up to 20 a run.",
			},
			{
				Name: "silent", Label: "Silent", Kind: engine.ParamBool, Default: false,
				Description: "Deliver without a sound.",
			},
		},
	}
}

type sendConfig struct {
	message, format, send string
	silent                bool
}

func (n *Send) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	items := in[engine.DefaultPort]
	if len(items) == 0 {
		return out, nil
	}

	cfg, err := sendSettings(rc)
	if err != nil {
		return nil, err
	}

	refs := make([]engine.ItemRef, len(items))
	for i := range items {
		refs[i] = engine.ItemRef{Port: engine.DefaultPort, Index: i}
	}

	if cfg.send != "each" {
		return out, n.deliver(ctx, rc.OwnerID, cfg, items, refs, out)
	}

	for i, item := range items {
		if i >= maxMessages {
			out.Fail(item, refs[i], fmt.Errorf("not sent: at most %d messages a run; send them all in one instead", maxMessages))
			continue
		}

		if err := n.deliver(ctx, rc.OwnerID, cfg, []engine.Item{item}, refs[i:i+1], out); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// deliver sends items as one message and passes them on, or fails them. Having nowhere
// to send, no linked chat or no bot, fails the node instead: no item would get through.
func (n *Send) deliver(ctx context.Context, owner domain.UserID, cfg sendConfig, items []engine.Item, refs []engine.ItemRef, out engine.Ports) error {
	text, err := render(cfg, items)
	if err == nil {
		err = n.messenger.MessageUser(ctx, owner, ports.UserMessage{HTML: text, Silent: cfg.silent})
	}

	if errors.Is(err, domain.ErrTelegramNotLinked) || errors.Is(err, domain.ErrTelegramUnavailable) || ctx.Err() != nil {
		return errors.Join(err, ctx.Err())
	}

	for i, item := range items {
		if err != nil {
			out.Fail(item, refs[i], err)
			continue
		}

		out.Add(engine.DefaultPort, engine.Item{JSON: item.JSON, Source: []engine.ItemRef{refs[i]}})
	}

	return nil
}

// render writes the message for items: one line per item, as HTML for Telegram.
func render(cfg sendConfig, items []engine.Item) (string, error) {
	lines := make([]string, len(items))

	if cfg.format == "html" {
		for i, it := range items {
			lines[i] = fields.RenderEscaped(cfg.message, it.JSON, html.EscapeString)
		}

		text := strings.Join(lines, "\n")
		if utf8.RuneCountInString(text) > maxLength {
			return "", fmt.Errorf("the message is longer than the %d characters Telegram allows", maxLength)
		}

		return text, nil
	}

	for i, it := range items {
		lines[i] = fields.Render(cfg.message, it.JSON)
	}

	return html.EscapeString(truncate(strings.Join(lines, "\n"), maxLength)), nil
}

func truncate(s string, most int) string {
	if utf8.RuneCountInString(s) <= most {
		return s
	}

	return string([]rune(s)[:most-1]) + "…"
}

func sendSettings(rc engine.RunContext) (sendConfig, error) {
	p, err := rc.Params.Resolve(engine.ItemRef{Port: engine.DefaultPort})
	if err != nil {
		return sendConfig{}, err
	}

	var cfg sendConfig

	for name, dst := range map[string]*string{"message": &cfg.message, "format": &cfg.format, "send": &cfg.send} {
		if *dst, err = p.String(name); err != nil {
			return sendConfig{}, err
		}
	}

	if cfg.silent, err = p.Bool("silent"); err != nil {
		return sendConfig{}, err
	}

	return cfg, nil
}

// Trigger starts the workflow on messages its owner sends the bot.
type Trigger struct {
	inbox ports.TelegramInbox
}

func NewTrigger(inbox ports.TelegramInbox) *Trigger {
	return &Trigger{inbox: inbox}
}

func (*Trigger) Type() engine.NodeType {
	return engine.NodeType{
		Name:  TriggerType,
		Label: "Telegram message",
		Description: "Starts the workflow when you send the instance's bot a message, one item per message. " +
			"Link Telegram in Settings first.",
		Outputs: []string{engine.DefaultPort},
		Params: []engine.ParamSpec{
			{
				Name: "startsWith", Label: "Starts with", Kind: engine.ParamString,
				Description: "Only messages that start with this, in any letter case, e.g. /report. Empty: every message. " +
					"The bot keeps /start, /menu and the names of its menu's sections for itself.",
			},
		},
	}
}

// Start emits an item for each matching message until ctx is done. Messages that come
// while the last one's execution runs wait their turn; too many are dropped.
func (t *Trigger) Start(ctx context.Context, tc engine.TriggerContext, emit engine.Emit) error {
	prefix := ""
	if tc.Params["startsWith"] != nil {
		var err error
		if prefix, err = tc.Params.String("startsWith"); err != nil {
			return err
		}
	}

	prefix = strings.TrimSpace(prefix)

	messages, cancel := t.inbox.Subscribe(tc.OwnerID, func(text string) bool { return hasPrefixFold(text, prefix) })
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return nil
		case m, ok := <-messages:
			if !ok {
				return nil
			}

			_ = emit(ctx, Output(m, prefix))
		}
	}
}

// Output is what the trigger emits for a message: its text, the text after the prefix,
// who sent it and when.
func Output(m ports.TelegramInboxMessage, prefix string) engine.Ports {
	return engine.Ports{engine.DefaultPort: {{JSON: map[string]any{
		"text":       m.Text,
		"rest":       strings.TrimSpace(m.Text[len(prefix):]),
		"username":   m.Username,
		"firstName":  m.FirstName,
		"receivedAt": m.At.UTC(),
	}}}}
}

// hasPrefixFold is strings.HasPrefix ignoring letter case, on whole runes.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}
