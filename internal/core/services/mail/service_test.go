package mail

import (
	"context"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

var admin = auth.Principal{User: domain.User{ID: "u-1", Email: "admin@example.com", Role: domain.RoleAdmin}, SessionID: "s-1"}

func TestSendTest(t *testing.T) {
	t.Run("to the address given", func(t *testing.T) {
		mailer := portsmocks.NewMockMailer(t)

		var sent ports.Mail
		mailer.EXPECT().Send(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, m ports.Mail) error {
			sent = m
			return nil
		})

		s := New(mailer, "https://poly.example/", logging.Noop())
		require.NoError(t, s.SendTest(context.Background(), admin, " ops@example.com "))

		assert.Equal(t, "ops@example.com", sent.To)
		assert.Equal(t, "Polythought test email", sent.Subject)
		assert.Contains(t, sent.Text, "sent from the Config page by admin@example.com")
		assert.Contains(t, sent.Text, "Polythought, https://poly.example")
		assert.Contains(t, sent.HTML, "<p")
	})

	t.Run("what the server answered", func(t *testing.T) {
		mailer := portsmocks.NewMockMailer(t)
		mailer.EXPECT().Send(mock.Anything, mock.Anything).Return(domain.ErrMailFailed)

		err := New(mailer, "", logging.Noop()).SendTest(context.Background(), admin, "ops@example.com")
		require.ErrorIs(t, err, domain.ErrMailFailed)
	})

	t.Run("admins only, to an address", func(t *testing.T) {
		s := New(portsmocks.NewMockMailer(t), "", logging.Noop())

		user := auth.Principal{User: domain.User{ID: "u-2", Role: domain.RoleUser}, SessionID: "s-2"}
		require.ErrorIs(t, s.SendTest(context.Background(), user, "ops@example.com"), domain.ErrForbidden)
		require.ErrorIs(t, s.SendTest(context.Background(), admin, "not an address"), domain.ErrInvalidEmail)
	})
}

func TestCompose(t *testing.T) {
	s := New(nil, "https://poly.example", logging.Noop())

	m := s.compose("ada@example.com", Message{
		Subject:    "Hi",
		Paragraphs: []string{"One <b>bold</b> & two.", "Second."},
		Link:       &Link{Text: "Verify", URL: "https://poly.example/verify?token=a&b"},
	})

	assert.Equal(t, "One <b>bold</b> & two.\n\nSecond.\n\nVerify: https://poly.example/verify?token=a&b\n\n-- \nPolythought, https://poly.example\n", m.Text)
	assert.Contains(t, m.HTML, "One &lt;b&gt;bold&lt;/b&gt; &amp; two.", "escaped")
	assert.Contains(t, m.HTML, `href="https://poly.example/verify?token=a&amp;b"`)
	assert.NotContains(t, m.HTML, "<b>")
}

func TestSendVerification(t *testing.T) {
	mailer := portsmocks.NewMockMailer(t)

	var sent ports.Mail
	mailer.EXPECT().Send(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, m ports.Mail) error {
		sent = m
		return nil
	})

	s := New(mailer, "https://poly.example", logging.Noop())
	require.NoError(t, s.SendVerification(context.Background(), "ada@example.com", "tok+en/1", 24*time.Hour))

	assert.Equal(t, "ada@example.com", sent.To)
	assert.Equal(t, "Verify your email for Polythought", sent.Subject)
	assert.Contains(t, sent.Text, "Verify my address: https://poly.example/verify-email?token=tok%2Ben%2F1")
	assert.Contains(t, sent.Text, "It works for 24 hours.")
	assert.Contains(t, sent.HTML, `href="https://poly.example/verify-email?token=tok%2Ben%2F1"`)
}

func TestForPeople(t *testing.T) {
	assert.Equal(t, "24 hours", forPeople(24*time.Hour))
	assert.Equal(t, "1 hour", forPeople(time.Hour))
	assert.Equal(t, "30 minutes", forPeople(30*time.Minute))
}
