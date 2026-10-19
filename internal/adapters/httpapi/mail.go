package httpapi

import (
	"context"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// MailService is what the handlers need from the mail service.
type MailService interface {
	SendTest(ctx context.Context, actor auth.Principal, to string) error
}

type testMailRequest struct {
	// To is the address the test email goes to.
	To string `json:"to"`
} // @name TestMailRequest

// sendTestMail sends a test email through the mail server set up on the Config page
// (D168): an error says what the server answered.
//
//	@Summary	Send a test email
//	@ID			sendTestMail
//	@Tags		admin
//	@Param		body	body	testMailRequest	true	"Where to"
//	@Success	204
//	@Failure	400	{object}	errorBody	"invalid_email"
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Failure	409	{object}	errorBody	"mail_not_set_up"
//	@Failure	502	{object}	errorBody	"mail_failed: the message says what the server answered"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/mail/test [post]
func (s *Server) sendTestMail(ctx context.Context, c *app.RequestContext) {
	var req testMailRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	if err := s.mail.SendTest(ctx, p, req.To); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
