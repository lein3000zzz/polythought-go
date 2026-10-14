package ports

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// TelegramButton is a button under a message: pressing it sends Data back to the bot,
// or opens URL.
type TelegramButton struct {
	Text string
	Data string // at most 64 bytes, so it names state kept on the server
	URL  string
}

// TelegramMessage is a message for the bot to send, or the new content of one it sent.
type TelegramMessage struct {
	ChatID  int64
	HTML    string // Telegram's subset of HTML; escape everything that is not markup
	Buttons [][]TelegramButton
}

type TelegramUser struct {
	ID        int64
	Username  string
	FirstName string
}

// TelegramUpdate is something a Telegram user did. Exactly one field is set.
type TelegramUpdate struct {
	Message  *TelegramIncoming
	Callback *TelegramCallback
}

// TelegramIncoming is a text message to the bot.
type TelegramIncoming struct {
	ChatID  int64
	Private bool // a one-to-one chat with the bot
	From    TelegramUser
	Text    string
}

// TelegramCallback is a press of a button under one of the bot's messages.
type TelegramCallback struct {
	ID        string
	ChatID    int64
	MessageID int
	From      TelegramUser
	Data      string
}

// TelegramStatus says whether the instance's bot runs.
type TelegramStatus struct {
	Running  bool
	Username string // the bot's, without the @
	Mode     string // polling or webhook
	Error    string // why it does not run, or the last problem talking to Telegram
	ErrorAt  time.Time
}

// TelegramBot is the instance's bot. Using it while it does not run is
// domain.ErrTelegramUnavailable; writing to a chat that blocked it is
// domain.ErrTelegramBlocked.
type TelegramBot interface {
	Status() TelegramStatus
	Send(ctx context.Context, m TelegramMessage) (messageID int, err error)
	Edit(ctx context.Context, messageID int, m TelegramMessage) error
	AnswerCallback(ctx context.Context, callbackID, text string) error
}

// TelegramLinkRepository keeps which Telegram user each account linked, and the codes
// that link them. Unknown links and codes are domain.ErrNotFound.
type TelegramLinkRepository interface {
	ForUser(ctx context.Context, user domain.UserID) (domain.TelegramLink, error)
	ForTelegramUser(ctx context.Context, telegramUserID int64) (domain.TelegramLink, error)
	// Save links an account. It replaces the account's earlier link and any other
	// account's link to the same Telegram user.
	Save(ctx context.Context, link domain.TelegramLink) error
	Delete(ctx context.Context, user domain.UserID) error

	// SaveCode keeps the hash of a link code for user, replacing any earlier code.
	SaveCode(ctx context.Context, user domain.UserID, codeHash []byte, expiresAt time.Time) error
	// ConsumeCode deletes a code and returns whose it was. Expired codes are not found.
	ConsumeCode(ctx context.Context, codeHash []byte, now time.Time) (domain.UserID, error)
}
