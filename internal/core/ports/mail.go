package ports

import "context"

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
