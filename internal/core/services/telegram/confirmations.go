package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// Confirmations: a token's or an agent's live action waits for its user (D406), who
// confirms or refuses it with a button. A press decides only the presser's own, while it
// waits; the message then says how it was decided.

// Confirms is what the buttons need from the confirmations service.
type Confirms interface {
	Confirm(ctx context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error)
	Refuse(ctx context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error)
}

// UseConfirmations lets the buttons confirm and refuse. Call it before the bot runs.
func (s *Service) UseConfirmations(c Confirms) {
	s.confirms = c
}

const (
	dataConfirm = "conf:" // conf:<confirmation id>:<y or n>

	textConfirmClosed = "This is closed: it was confirmed, refused, or not confirmed in time."
)

var _ ports.ConfirmAsker = (*Service)(nil)

func (s *Service) AskConfirmation(ctx context.Context, user domain.UserID, id domain.ConfirmationID, html string) (int, error) {
	yes, no := dataConfirm+string(id)+":y", dataConfirm+string(id)+":n"
	if len(yes) > maxButtonData {
		return 0, fmt.Errorf("telegram: confirmation button data is %d bytes, more than %d", len(yes), maxButtonData)
	}

	return s.send(ctx, user, ports.TelegramMessage{HTML: html, Buttons: [][]ports.TelegramButton{{
		{Text: "✅ Confirm", Data: yes}, {Text: "✖️ Refuse", Data: no},
	}}})
}

// confirmPress confirms or refuses the user's own waiting action; the service then says
// so in the message.
func (s *Service) confirmPress(ctx context.Context, cb ports.TelegramCallback, user domain.User) {
	id, yes, ok := parseConfirm(cb.Data)

	if !ok || s.confirms == nil {
		s.answer(ctx, cb.ID, textConfirmClosed)
		return
	}

	// The user, in their own chat: as at a browser (D399).
	p := auth.Principal{User: user}

	var err error
	if yes {
		_, err = s.confirms.Confirm(ctx, p, id)
	} else {
		_, err = s.confirms.Refuse(ctx, p, id)
	}

	switch {
	case err == nil && yes:
		s.answer(ctx, cb.ID, "Confirmed: it is being carried out")
	case err == nil:
		s.answer(ctx, cb.ID, "Refused")
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrNotFound):
		s.answer(ctx, cb.ID, textConfirmClosed)
	default:
		s.log.Warn(ctx, "Confirmation not decided", "confirmation_id", id, logging.Err(err))
		s.answer(ctx, cb.ID, textFailed)
	}
}

func parseConfirm(data string) (domain.ConfirmationID, bool, bool) {
	id, answer, ok := strings.Cut(strings.TrimPrefix(data, dataConfirm), ":")
	if !ok || id == "" || (answer != "y" && answer != "n") {
		return "", false, false
	}

	return domain.ConfirmationID(id), answer == "y", true
}
