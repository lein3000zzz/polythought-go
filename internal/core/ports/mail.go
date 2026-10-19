package ports

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// Mail is a message to one address: its subject, and its body as plain text and as HTML.
type Mail struct {
	To, Subject, Text, HTML string
}

// Mailer sends mail through the server an admin connected (D164, D168).
type Mailer interface {
	// SetUp says whether a mail server is set up.
	SetUp() bool
	// Send sends m: domain.ErrMailNotSetUp without a server, domain.ErrMailFailed saying
	// what the server answered when it did not take it.
	Send(ctx context.Context, m Mail) error
}

// AccountMail mails accounts the links they open (D165).
type AccountMail interface {
	// SetUp says whether mail can be sent.
	SetUp() bool
	// SendVerification mails to the link verifying its address, carrying token, which
	// works for lasts.
	SendVerification(ctx context.Context, to, token string, lasts time.Duration) error
	// SendPasswordReset mails to the link setting a new password, carrying token, which
	// works for lasts.
	SendPasswordReset(ctx context.Context, to, token string, lasts time.Duration) error
	// SendPasswordChanged tells to that its password was changed at at.
	SendPasswordChanged(ctx context.Context, to string, at time.Time) error
	// SendInvite mails to an invite from inviter, carrying code, working until until.
	SendInvite(ctx context.Context, to, inviter, code string, until time.Time) error
}

// UserMail is a notification as mail: a subject, paragraphs, and a link to act on.
type UserMail struct {
	Subject    string
	Paragraphs []string
	LinkText   string
	LinkURL    string
}

// NotificationMail mails users their notifications, to their account's address (D170,
// D184).
type NotificationMail interface {
	// On says whether notifications can go by mail: switched on, and mail set up.
	On() bool
	// MailUser mails m to the user; nothing to an account that is disabled or gone.
	MailUser(ctx context.Context, user domain.UserID, m UserMail) error
}
