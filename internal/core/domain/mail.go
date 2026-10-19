package domain

import "errors"

var (
	// ErrMailNotSetUp means no mail server is set up: an admin connects one on the Config
	// page (D168).
	ErrMailNotSetUp = errors.New("no mail server is set up")
	// ErrMailFailed means the mail server did not take a message: the error says what it
	// answered.
	ErrMailFailed = errors.New("the mail server did not take the message")
)
