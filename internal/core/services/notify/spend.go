package notify

import (
	"context"
	"errors"
	"html"
	"strings"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.SpendNotifier = (*Service)(nil)

type spendAlert struct {
	owner domain.UserID
	alert domain.SpendAlert
}

// AgentSpendNoticed queues a notice that a user's agents reached 80% of a monthly cap,
// or a cap (D274). It never blocks a call: when the queue is full, the notice goes untold.
func (s *Service) AgentSpendNoticed(owner domain.UserID, a domain.SpendAlert) {
	select {
	case s.spends <- spendAlert{owner: owner, alert: a}:
	default:
		s.log.Warn(context.Background(), "Agent spend notification dropped: too many at once", "user_id", owner, "notice", a.Notice)
	}
}

func (s *Service) agentSpend(ctx context.Context, e spendAlert) {
	k, a := AgentSpendWarned, e.alert
	if a.Notice == domain.NoticeCapped {
		k = AgentSpendCapped
	}

	telegram, mail, err := s.channels(ctx, e.owner, k)
	if err != nil {
		s.log.Warn(ctx, "Agent spend notification skipped", "user_id", e.owner, logging.Err(err))
		return
	}

	resets := a.Resets.UTC().Format("2 January, 15:04") + " UTC"
	used := a.Used + " of " + a.Cap + " " + a.What
	if a.What == "dollars" {
		used = a.Used + " of " + a.Cap
	}

	title, said := "Your AI agents used 80% of a monthly cap",
		"Your AI agent nodes have used "+used+" this month, 80% of their cap. It resets on "+resets+"."
	if a.Notice == domain.NoticeCapped {
		title, said = "Your AI agents reached a monthly cap",
			"Your AI agent nodes have used "+used+" this month: their cap is reached, so their calls are refused "+
				"and their items fail until it resets on "+resets+", unless an admin raises it."
	}

	mailed := false
	if mail {
		mailed = s.mailUser(ctx, e.owner, k, ports.UserMail{
			Subject: title, Paragraphs: []string{said}, LinkText: "See your spend", LinkURL: s.publicURL + "/settings",
		}) == nil
	}

	if !telegram {
		return
	}

	var buttons [][]ports.TelegramButton
	if strings.HasPrefix(s.publicURL, "https://") {
		buttons = [][]ports.TelegramButton{{{Text: "See your spend", URL: s.publicURL + "/settings"}}}
	}

	icon := "📈 "
	if a.Notice == domain.NoticeCapped {
		icon = "⛔ "
	}

	err = s.messenger.MessageUser(ctx, e.owner, ports.UserMessage{HTML: icon + html.EscapeString(said), Buttons: buttons})
	if err != nil && !mailed && !errors.Is(err, domain.ErrTelegramNotLinked) && !errors.Is(err, domain.ErrTelegramUnavailable) {
		s.log.Warn(ctx, "Agent spend notification not sent", "user_id", e.owner, logging.Err(err))
	}
}
