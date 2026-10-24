package notify

import (
	"context"
	"html"
	"time"
	"unicode/utf8"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// A token's or an agent's live action waits for its user (D406): they are told each way
// they chose, Telegram's message with a button to confirm and one to refuse, and the
// message then says how it was decided.

var ConfirmationAsked = Kind{
	Key: "confirmation_asked", Group: groupConnected,
	Label: "An agent asks you to confirm a live action",
	Description: "An agent or an API token asks to go live, switch on or run a workflow that trades live, change one " +
		"that is on, or turn your kill switch off. Confirm or refuse it with the message's buttons on Telegram, or in " +
		"the app under Settings → Connected agents.",
	Default: true,
}

// WithConfirmAsker asks on Telegram with buttons, and says there how it was decided.
func WithConfirmAsker(a ports.ConfirmAsker) Option {
	return func(s *Service) { s.confirmAsker = a }
}

// ConfirmationAsked tells the user of a live action waiting for them, each way they chose:
// the Telegram message's ID comes back, 0 when none went.
func (s *Service) ConfirmationAsked(ctx context.Context, c domain.Confirmation) (int, error) {
	telegram, mail, err := s.channels(ctx, c.OwnerID, ConfirmationAsked)
	if err != nil {
		return 0, err
	}

	review := s.publicURL + "/settings/connected#confirmations"

	if mail {
		said := []string{c.Asker() + " asks you to confirm: " + c.What + aboutWhom(c) + "."}
		if c.Detail != "" {
			said = append(said, "What it changes:\n"+c.Detail)
		}

		_ = s.mailUser(ctx, c.OwnerID, ConfirmationAsked, ports.UserMail{
			Subject: c.Asker() + " asks you to confirm a live action",
			Paragraphs: append(said,
				"It waits for you until "+c.ExpiresAt.UTC().Format("15:04, 2 January 2006")+" (UTC), then expires."),
			LinkText: "Review it", LinkURL: review,
		})
	}

	if !telegram || s.confirmAsker == nil {
		return 0, nil
	}

	return s.confirmAsker.AskConfirmation(ctx, c.OwnerID, c.ID, confirmText(c))
}

// ConfirmationClosed says in its Telegram message how a confirmation was decided, or
// ended: its buttons go.
func (s *Service) ConfirmationClosed(ctx context.Context, c domain.Confirmation) {
	if c.Message == 0 || s.confirmAsker == nil {
		return
	}

	var closing string

	switch c.Status {
	case domain.ConfirmPending:
		return
	case domain.ConfirmConfirmed:
		closing = "✅ <b>Confirmed</b>: being carried out."
	case domain.ConfirmDone:
		closing = "✅ <b>Confirmed</b>, and done: " + html.EscapeString(c.Result) + "."
	case domain.ConfirmFailed:
		closing = "⚠️ <b>Confirmed</b>, but it failed: " + html.EscapeString(c.Result)
	case domain.ConfirmRefused:
		closing = "✖️ <b>Refused</b>."
	case domain.ConfirmExpired:
		closing = "⌛ <i>Not confirmed in time.</i>"
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	if err := s.confirmAsker.CloseQuestion(ctx, c.OwnerID, c.Message, confirmText(c)+"\n\n"+closing); err != nil {
		s.log.Warn(ctx, "Confirmation's message not closed", "confirmation_id", c.ID, logging.Err(err))
	}
}

// aboutWhom names whom a confirmation is about, for the user: never the agent's (D410).
func aboutWhom(c domain.Confirmation) string {
	if c.About == "" {
		return ""
	}

	return " (" + c.About + ")"
}

func confirmText(c domain.Confirmation) string {
	text := "🔐 <b>" + html.EscapeString(c.Asker()) + "</b> asks you to confirm:\n" + html.EscapeString(c.What+aboutWhom(c)) + ".\n\n"

	if c.Detail != "" {
		text += "What it changes:\n<pre>" + html.EscapeString(truncate(c.Detail, maxDetailText)) + "</pre>\n"
		if utf8.RuneCountInString(c.Detail) > maxDetailText {
			text += "See all of it in the app.\n"
		}

		text += "\n"
	}

	return text + "It waits until " + c.ExpiresAt.UTC().Format("15:04 on 2 January") + " (UTC)."
}
