// Package mailer sends mail through an SMTP server, with go-mail (D171).
package mailer

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	gomail "github.com/wneessen/go-mail"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// How the connection is secured (D169).
const (
	STARTTLS = "starttls"
	TLS      = "tls"
	Plain    = "none"
)

const (
	sendTimeout   = 30 * time.Second
	maxAnswerText = 300
)

// Config is how to reach the mail server, and who mail comes from.
type Config struct {
	Host     string
	Port     int
	Security string // STARTTLS, TLS or Plain
	Username string
	Password string
	From     string
	FromName string
}

var _ ports.Mailer = (*SMTP)(nil)

// SMTP serves ports.Mailer. It reads its Config for each message, so a change on the
// Config page applies to the next one. The server's certificate is always checked.
type SMTP struct {
	config  func() Config
	rootCAs *x509.CertPool // nil: the system's
	timeout time.Duration
	log     logging.Logger
}

func New(config func() Config, log logging.Logger) *SMTP {
	return &SMTP{config: config, timeout: sendTimeout, log: log.With("component", "mailer")}
}

func (s *SMTP) SetUp() bool {
	c := s.config()
	return c.Host != "" && c.From != ""
}

func (s *SMTP) Send(ctx context.Context, m ports.Mail) error {
	c := s.config()
	if c.Host == "" || c.From == "" {
		return domain.ErrMailNotSetUp
	}

	msg := gomail.NewMsg()
	if err := msg.FromFormat(c.FromName, c.From); err != nil {
		return fmt.Errorf("%w: the From address is not one", domain.ErrMailFailed)
	}

	if err := msg.To(m.To); err != nil {
		return domain.ErrInvalidEmail
	}

	msg.Subject(m.Subject)
	msg.SetDate()
	msg.SetMessageIDWithValue(messageID(c.From))
	msg.SetGenHeader("Auto-Submitted", "auto-generated") // RFC 3834: no replies from robots
	msg.SetBodyString(gomail.TypeTextPlain, m.Text)

	if m.HTML != "" {
		msg.AddAlternativeString(gomail.TypeTextHTML, m.HTML)
	}

	client, err := gomail.NewClient(c.Host, s.options(c)...)
	if err != nil {
		return fmt.Errorf("%w: %s", domain.ErrMailFailed, answer(err))
	}

	start := time.Now()

	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		s.log.Warn(ctx, "Mail not sent", "host", c.Host, "subject", m.Subject, logging.Err(err))

		return fmt.Errorf("%w: %s", domain.ErrMailFailed, answer(err))
	}

	s.log.Info(ctx, "Mail sent", "subject", m.Subject, "took", time.Since(start))

	return nil
}

func (s *SMTP) options(c Config) []gomail.Option {
	opts := []gomail.Option{
		gomail.WithPort(c.Port),
		gomail.WithTimeout(s.timeout),
		gomail.WithTLSConfig(&tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12, RootCAs: s.rootCAs}),
	}

	switch c.Security {
	case TLS:
		opts = append(opts, gomail.WithSSL())
	case Plain:
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	default:
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}

	// Over TLS, the strongest mechanism the server offers. Over a plain connection, PLAIN,
	// which go-mail sends to this host only.
	if c.Username != "" {
		auth := gomail.SMTPAuthAutoDiscover
		if c.Security == Plain {
			auth = gomail.SMTPAuthPlain
		}

		opts = append(opts, gomail.WithSMTPAuth(auth), gomail.WithUsername(c.Username), gomail.WithPassword(c.Password))
	}

	return opts
}

// messageID is a new Message-ID on the From address's domain, which names no machine.
func messageID(from string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)

	domain := from[strings.LastIndex(from, "@")+1:]

	return hex.EncodeToString(b) + "@" + domain
}

// answer is what went wrong, for the admin: the server's reply, on one line.
func answer(err error) string {
	text := strings.Join(strings.Fields(err.Error()), " ")
	if len(text) > maxAnswerText {
		text = text[:maxAnswerText] + "…"
	}

	return text
}
