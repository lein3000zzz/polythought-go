package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// Questions: a workflow asks its owner something, with a button per option, and waits.
// A button's data names the execution, the key of its wait and the option. A press
// answers only if the presser's linked account owns the execution and the wait still has
// that key, so buttons of an earlier question, or someone else's, lead nowhere.

// Answers is what questions need from the waits service.
type Answers interface {
	Answer(ctx context.Context, owner domain.UserID, id domain.ExecutionID, key string, payload map[string]any) error
}

// UseAnswers lets button presses answer questions. Call it before the bot runs.
func (s *Service) UseAnswers(a Answers) {
	s.answers = a
}

const (
	dataAsk = "ask:" // ask:<execution id>:<key>:<option>: picks an option

	maxButtonData = 64 // bytes, Telegram's limit
	shortOption   = 14 // characters; options this short share a row
	optionsPerRow = 3

	textQuestionClosed = "This question is closed: it was answered, ran out of time, or its run was cancelled."
)

var _ ports.UserAsker = (*Service)(nil)

func (s *Service) AskUser(ctx context.Context, user domain.UserID, q ports.Question) (int, error) {
	if strings.Contains(q.Key, ":") || q.Key == "" {
		return 0, fmt.Errorf("telegram: question key %q: empty or has a colon", q.Key)
	}

	buttons := make([]ports.TelegramButton, len(q.Options))

	for i, option := range q.Options {
		data := dataAsk + string(q.ExecutionID) + ":" + q.Key + ":" + strconv.Itoa(i)
		if len(data) > maxButtonData {
			return 0, fmt.Errorf("telegram: question button data is %d bytes, more than %d", len(data), maxButtonData)
		}

		buttons[i] = ports.TelegramButton{Text: option, Data: data}
	}

	return s.send(ctx, user, ports.TelegramMessage{HTML: q.HTML, Buttons: optionRows(buttons)})
}

// optionRows puts short options side by side, a few to a row, and long ones on their own.
func optionRows(buttons []ports.TelegramButton) [][]ports.TelegramButton {
	var rows [][]ports.TelegramButton

	for _, b := range buttons {
		last := len(rows) - 1
		short := utf8.RuneCountInString(b.Text) <= shortOption

		if short && last >= 0 && len(rows[last]) < optionsPerRow && utf8.RuneCountInString(rows[last][0].Text) <= shortOption {
			rows[last] = append(rows[last], b)
			continue
		}

		rows = append(rows, []ports.TelegramButton{b})
	}

	return rows
}

func (s *Service) CloseQuestion(ctx context.Context, user domain.UserID, messageID int, html string) error {
	link, err := s.chatOf(ctx, user)
	if err != nil {
		return err
	}

	return s.bot.Edit(ctx, messageID, ports.TelegramMessage{ChatID: link.ChatID, HTML: html})
}

// answerQuestion answers the waiting execution a press picks an option for. The question's
// node then says what was picked, in place of the buttons.
func (s *Service) answerQuestion(ctx context.Context, cb ports.TelegramCallback, user domain.User) {
	id, key, option, ok := parseAsk(cb.Data)

	err := domain.ErrNotFound
	if ok && s.answers != nil {
		err = s.answers.Answer(ctx, user.ID, id, key, map[string]any{"option": option})
	}

	switch {
	case err == nil:
		s.answer(ctx, cb.ID, "")
	case errors.Is(err, domain.ErrNotFound):
		s.answer(ctx, cb.ID, textQuestionClosed)
		// Its buttons would only say the same again.
		s.ignored(ctx, "Closed question's buttons not removed", s.bot.Edit(ctx, cb.MessageID, ports.TelegramMessage{ChatID: cb.ChatID}))
	default:
		s.log.Warn(ctx, "Question not answered", "execution_id", id, logging.Err(err))
		s.answer(ctx, cb.ID, textFailed)
	}
}

func parseAsk(data string) (id domain.ExecutionID, key string, option int, ok bool) {
	parts := strings.Split(strings.TrimPrefix(data, dataAsk), ":")
	if len(parts) != 3 {
		return "", "", 0, false
	}

	option, err := strconv.Atoi(parts[2])
	if err != nil || option < 0 {
		return "", "", 0, false
	}

	return domain.ExecutionID(parts[0]), parts[1], option, true
}
