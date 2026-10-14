package domain

import (
	"errors"
	"time"
)

var (
	ErrTelegramUnavailable = errors.New("telegram is not set up on this instance")

	// ErrTelegramBlocked means the bot can no longer write to the chat: the user blocked
	// it or deleted the chat.
	ErrTelegramBlocked = errors.New("the bot cannot write to this chat any more")

	ErrTelegramNotLinked = errors.New("telegram is not linked: link it in Settings → Telegram")
)

// TelegramLink ties an account to the Telegram user who linked it, and to their private
// chat with the instance's bot.
type TelegramLink struct {
	UserID         UserID
	TelegramUserID int64
	ChatID         int64
	Username       string // without the @; Telegram users may have none
	FirstName      string
	LinkedAt       time.Time
}
