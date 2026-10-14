package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/telegram"
)

// TelegramService is what the handlers need to link accounts to Telegram.
type TelegramService interface {
	Status(ctx context.Context, p auth.Principal) (telegram.Status, error)
	CreateLink(ctx context.Context, p auth.Principal) (telegram.LinkURL, error)
	Unlink(ctx context.Context, p auth.Principal) error
}

// TelegramWebhook takes the updates Telegram posts when the bot runs in webhook mode.
type TelegramWebhook interface {
	HandleWebhook(ctx context.Context, secret string, body []byte) error
}

// telegramSecretHeader carries the secret the bot gave Telegram with the webhook.
const telegramSecretHeader = "X-Telegram-Bot-Api-Secret-Token"

type telegramDTO struct {
	// Available is set while the instance's bot runs, so accounts can link.
	Available   bool   `json:"available"`
	BotUsername string `json:"botUsername,omitempty" validate:"optional"`
	// Problem says why the bot does not run, or what last went wrong. Admins only.
	Problem   string           `json:"problem,omitempty" validate:"optional"`
	ProblemAt *time.Time       `json:"problemAt,omitempty" validate:"optional" format:"date-time"`
	Link      *telegramLinkDTO `json:"link,omitempty" validate:"optional"`
} // @name TelegramStatus

type telegramLinkDTO struct {
	// Username is the Telegram user's, without the @; not everyone has one.
	Username  string    `json:"username,omitempty" validate:"optional"`
	FirstName string    `json:"firstName,omitempty" validate:"optional"`
	LinkedAt  time.Time `json:"linkedAt" format:"date-time"`
} // @name TelegramLink

type telegramLinkURLDTO struct {
	// URL opens the bot in Telegram; it links whoever opens it, once.
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt" format:"date-time"`
} // @name TelegramLinkURL

// telegramStatus says whether you can link Telegram, and to whom you did.
//
//	@Summary	Your Telegram link
//	@ID			getTelegram
//	@Tags		telegram
//	@Success	200	{object}	telegramDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/telegram [get]
func (s *Server) telegramStatus(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	st, err := s.telegram.Status(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := telegramDTO{
		Available: st.Available, BotUsername: st.BotUsername,
		Problem: st.Problem, ProblemAt: optionalTime(st.ProblemAt),
	}

	if st.Link != nil {
		out.Link = &telegramLinkDTO{Username: st.Link.Username, FirstName: st.Link.FirstName, LinkedAt: st.Link.LinkedAt}
	}

	c.JSON(http.StatusOK, out)
}

// createTelegramLink makes a single-use link to the bot. Opening it in Telegram links
// that Telegram account to yours. A new link replaces the last.
//
//	@Summary	Make a link to the Telegram bot
//	@ID			createTelegramLink
//	@Tags		telegram
//	@Success	201	{object}	telegramLinkURLDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	503	{object}	errorBody	"telegram_unavailable"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/telegram/link [post]
func (s *Server) createTelegramLink(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	link, err := s.telegram.CreateLink(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusCreated, telegramLinkURLDTO{URL: link.URL, ExpiresAt: link.ExpiresAt})
}

// unlinkTelegram forgets your Telegram account; its chat is told.
//
//	@Summary	Unlink Telegram
//	@ID			unlinkTelegram
//	@Tags		telegram
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found: nothing is linked"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/telegram/link [delete]
func (s *Server) unlinkTelegram(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.telegram.Unlink(ctx, p); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// telegramWebhook is where Telegram posts updates when the bot runs in webhook mode.
// Telegram proves itself with the secret the bot gave it; anything else is not found.
//
//	@Summary	Updates from Telegram (webhook mode)
//	@ID			telegramWebhook
//	@Tags		telegram
//	@Param		X-Telegram-Bot-Api-Secret-Token	header	string	true	"The secret the bot set with the webhook"
//	@Success	200
//	@Failure	404	{object}	errorBody	"not_found"
//	@Router		/telegram/webhook [post]
func (s *Server) telegramWebhook(ctx context.Context, c *app.RequestContext) {
	if s.telegramHook == nil {
		s.fail(ctx, c, domain.ErrNotFound)
		return
	}

	err := s.telegramHook.HandleWebhook(ctx, string(c.GetHeader(telegramSecretHeader)), c.Request.Body())
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusOK)
}
