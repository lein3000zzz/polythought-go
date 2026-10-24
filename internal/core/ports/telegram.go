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
	Silent  bool // delivered without a sound; sending only
	ReplyTo int  // the ID of a message in the chat this one replies to; sending only

	// Keyboard replaces the keyboard under the chat's input field with these buttons,
	// which send their text when pressed; RemoveKeyboard takes it away. Sending only, and
	// not with Buttons: a message has one or the other.
	Keyboard       [][]string
	RemoveKeyboard bool
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
	// Edit replaces a message's text and buttons; with no HTML, only its buttons.
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

// UserMessage is a message for a user, wherever they get them: for now their linked
// Telegram chat.
type UserMessage struct {
	HTML    string // Telegram's subset of HTML; escape everything that is not markup
	Buttons [][]TelegramButton
	Silent  bool // delivered without a sound
	ReplyTo int  // the ID of an earlier message to the user this one replies to
}

// UserMessenger writes to users. One with nowhere to write to is
// domain.ErrTelegramNotLinked.
type UserMessenger interface {
	MessageUser(ctx context.Context, user domain.UserID, m UserMessage) error
}

// ConfirmAsker asks a user on Telegram to confirm or refuse a live action of one of their
// tokens or agents, with a button each (D406), and says there how it was decided.
type ConfirmAsker interface {
	// AskConfirmation sends html with the two buttons; the message's ID comes back.
	AskConfirmation(ctx context.Context, user domain.UserID, id domain.ConfirmationID, html string) (int, error)
	// CloseQuestion replaces a message's text, its buttons gone.
	CloseQuestion(ctx context.Context, user domain.UserID, messageID int, html string) error
}

// Question asks a user to pick one of its options, to answer a waiting execution.
type Question struct {
	HTML    string // Telegram's subset of HTML; escape everything that is not markup
	Options []string

	// What a pick answers: the execution, through its wait's key.
	ExecutionID domain.ExecutionID
	Key         string
}

// UserAsker asks users questions they answer with a button. One with nowhere to ask is
// domain.ErrTelegramNotLinked.
type UserAsker interface {
	// AskUser sends q and returns the ID of its message.
	AskUser(ctx context.Context, user domain.UserID, q Question) (messageID int, err error)
	// CloseQuestion replaces a question's text with html and takes its buttons away.
	CloseQuestion(ctx context.Context, user domain.UserID, messageID int, html string) error
}

// TelegramInboxMessage is a message a linked user sent the bot.
type TelegramInboxMessage struct {
	Text      string
	Username  string
	FirstName string
	At        time.Time
}

// TelegramInbox hands the messages linked users send the bot to the workflows waiting for
// them. Messages the bot answers itself, such as /start, never arrive here.
type TelegramInbox interface {
	// Subscribe delivers the owner's messages that match, until cancel is called. A
	// subscriber too slow to keep up misses messages.
	Subscribe(owner domain.UserID, match func(text string) bool) (messages <-chan TelegramInboxMessage, cancel func())
}
