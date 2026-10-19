// Package mail writes the mail Polythought sends, as plain text and HTML, and sends it
// through the mail server an admin connected (D164).
package mail

import (
	"context"
	"html"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

type Service struct {
	mailer    ports.Mailer
	publicURL string
	log       logging.Logger
}

// New makes the service. publicURL is where people open the app; mail links to it.
func New(mailer ports.Mailer, publicURL string, log logging.Logger) *Service {
	return &Service{mailer: mailer, publicURL: strings.TrimRight(publicURL, "/"), log: log.With("component", "mail")}
}

// SetUp says whether a mail server is set up.
func (s *Service) SetUp() bool {
	return s.mailer.SetUp()
}

// SendTest sends a test message to to, for an admin to see whether the mail server takes
// mail, and what it answers when it does not (D168).
func (s *Service) SendTest(ctx context.Context, actor auth.Principal, to string) error {
	if !actor.User.IsAdmin() {
		return domain.ErrForbidden
	}

	addr, err := mail.ParseAddress(strings.TrimSpace(to))
	if err != nil {
		return domain.ErrInvalidEmail
	}

	if err := s.send(ctx, addr.Address, Message{
		Subject: "Polythought test email",
		Paragraphs: []string{
			"This is a test email from Polythought, sent from the Config page by " + actor.User.Email + ".",
			"It came through, so Polythought can send mail through this server.",
		},
	}); err != nil {
		return err
	}

	s.log.Info(ctx, "Test email sent", "user_id", actor.User.ID)

	return nil
}

var _ ports.AccountMail = (*Service)(nil)

// SendVerification mails the link verifying an account's address: it opens the app's
// verification page, which then asks for the password (D172).
func (s *Service) SendVerification(ctx context.Context, to, token string, lasts time.Duration) error {
	return s.send(ctx, to, Message{
		Subject: "Verify your email for Polythought",
		Paragraphs: []string{
			"Someone, hopefully you, made a Polythought account with this address. Open the link to verify it, then sign in.",
			"It works for " + forPeople(lasts) + ". Did not sign up? Ignore this mail: the account is removed within 7 days.",
		},
		Link: &Link{Text: "Verify my address", URL: s.publicURL + "/verify-email?token=" + url.QueryEscape(token)},
	})
}

// SendPasswordReset mails the link setting a new password; the page then asks to sign in
// with it (D177).
func (s *Service) SendPasswordReset(ctx context.Context, to, token string, lasts time.Duration) error {
	return s.send(ctx, to, Message{
		Subject: "Set a new Polythought password",
		Paragraphs: []string{
			"Someone, hopefully you, asked to set a new password for the Polythought account of this address.",
			"The link works once, for " + forPeople(lasts) + ". Setting the password signs the account out everywhere. " +
				"Did not ask? Ignore this mail: your password stays as it is.",
		},
		Link: &Link{Text: "Set a new password", URL: s.publicURL + "/reset-password?token=" + url.QueryEscape(token)},
	})
}

// SendPasswordChanged tells an account its password was changed, so its owner learns of a
// change they did not make.
func (s *Service) SendPasswordChanged(ctx context.Context, to string, at time.Time) error {
	return s.send(ctx, to, Message{
		Subject: "Your Polythought password was changed",
		Paragraphs: []string{
			"The password of your Polythought account was changed on " + at.UTC().Format("2 January 2006 at 15:04") + " UTC.",
			"If that was you, there is nothing to do. If not, set a new one now and tell an admin: someone may know your old password.",
		},
		Link: &Link{Text: "Set a new password", URL: s.publicURL + "/forgot-password"},
	})
}

// SendInvite mails an invite, naming the admin who sent it (D182); its link opens the
// sign-up form with the address filled in (D181).
func (s *Service) SendInvite(ctx context.Context, to, inviter, code string, until time.Time) error {
	where := "Polythought"
	if s.publicURL != "" {
		where += ", at " + s.publicURL
	}

	return s.send(ctx, to, Message{
		Subject: inviter + " invited you to Polythought",
		Paragraphs: []string{
			inviter + " invited you to " + where + ".",
			"Open the link to make your account with this address. It works once, until " +
				until.UTC().Format("2 January 2006 at 15:04") + " UTC. Not expecting it? Ignore this mail.",
		},
		Link: &Link{Text: "Make my account", URL: s.publicURL + "/register?invite=" + url.QueryEscape(code)},
	})
}

// forPeople says how long d is, roughly: 24 hours, 30 minutes.
func forPeople(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		return plural(int(d/time.Hour), "hour", "hours")
	}

	return plural(int(d.Round(time.Minute)/time.Minute), "minute", "minutes")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}

	return strconv.Itoa(n) + " " + many
}

// Message is what a mail says: its subject, paragraphs, and a link to act on.
type Message struct {
	Subject    string
	Paragraphs []string
	Link       *Link
}

// Link is a button in the HTML, and a line in the text.
type Link struct {
	Text, URL string
}

func (s *Service) send(ctx context.Context, to string, m Message) error {
	return s.mailer.Send(ctx, s.compose(to, m))
}

// compose writes m as plain text and as HTML, everything in it escaped.
func (s *Service) compose(to string, m Message) ports.Mail {
	var text, body strings.Builder

	for _, p := range m.Paragraphs {
		text.WriteString(p + "\n\n")
		body.WriteString(`<p style="margin:0 0 16px">` + html.EscapeString(p) + "</p>\n")
	}

	if m.Link != nil {
		text.WriteString(m.Link.Text + ": " + m.Link.URL + "\n\n")
		body.WriteString(`<p style="margin:24px 0"><a href="` + html.EscapeString(m.Link.URL) +
			`" style="background:#18181b;color:#fff;padding:10px 16px;border-radius:6px;text-decoration:none">` +
			html.EscapeString(m.Link.Text) + "</a></p>\n" +
			`<p style="margin:0 0 16px;color:#71717a;font-size:13px">Or open ` + html.EscapeString(m.Link.URL) + "</p>\n")
	}

	footer := "Polythought"
	if s.publicURL != "" {
		footer += ", " + s.publicURL
	}

	text.WriteString("-- \n" + footer + "\n")

	return ports.Mail{
		To:      to,
		Subject: m.Subject,
		Text:    text.String(),
		HTML: `<!doctype html><html><body style="font-family:system-ui,sans-serif;font-size:15px;color:#18181b;max-width:560px">` +
			"\n" + body.String() + `<p style="margin:32px 0 0;color:#71717a;font-size:13px">` + html.EscapeString(footer) +
			"</p>\n</body></html>\n",
	}
}
