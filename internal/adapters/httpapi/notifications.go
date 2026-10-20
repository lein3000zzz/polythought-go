package httpapi

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/notify"
)

// NotificationService is what the handlers need to let users choose their notifications.
type NotificationService interface {
	Prefs(ctx context.Context, p auth.Principal) ([]notify.Pref, error)
	SetPref(ctx context.Context, p auth.Principal, key string, enabled bool) (notify.Pref, error)
	SetEmailPref(ctx context.Context, p auth.Principal, key string, enabled bool) (notify.Pref, error)
	MailOn() bool
}

type notificationDTO struct {
	Key string `json:"key"`
	// Group is what the kind is about, to show the kinds under: Workflows, Wallets, Live
	// orders, Paper orders.
	Group       string `json:"group"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// Enabled is on Telegram, to your linked chat.
	Enabled bool `json:"enabled"`
	// Email is by mail, to your account's address, while MailOn (D170).
	Email bool `json:"email"`
} // @name Notification

type notificationList struct {
	Notifications []notificationDTO `json:"notifications"`
	// MailOn says notifications can go by mail now: an admin allows it, and mail is set up
	// (D184).
	MailOn bool `json:"mailOn"`
} // @name NotificationList

type setNotificationRequest struct {
	// Enabled switches it on Telegram, Email by mail; at least one is given.
	Enabled *bool `json:"enabled" validate:"optional"`
	Email   *bool `json:"email" validate:"optional"`
} // @name SetNotificationRequest

func toNotificationDTO(p notify.Pref) notificationDTO {
	return notificationDTO{Key: p.Key, Group: p.Group, Label: p.Label, Description: p.Description, Enabled: p.Enabled, Email: p.Email}
}

// listNotifications lists the kinds of notification and how you get each: on Telegram, to
// your linked chat, and by mail.
//
//	@Summary	Your notifications
//	@ID			listNotifications
//	@Tags		notifications
//	@Success	200	{object}	notificationList
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/notifications [get]
func (s *Server) listNotifications(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	prefs, err := s.notifications.Prefs(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := notificationList{Notifications: make([]notificationDTO, len(prefs)), MailOn: s.notifications.MailOn()}
	for i, pref := range prefs {
		out.Notifications[i] = toNotificationDTO(pref)
	}

	c.JSON(http.StatusOK, out)
}

// setNotification switches one kind of notification on or off for you, on Telegram, by
// mail, or both.
//
//	@Summary	Switch a notification on or off
//	@ID			setNotification
//	@Tags		notifications
//	@Param		key		path		string					true	"Kind of notification, e.g. workflow_failed"
//	@Param		body	body		setNotificationRequest	true	"On or off, on Telegram or by mail"
//	@Success	200		{object}	notificationDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/notifications/{key} [put]
func (s *Server) setNotification(ctx context.Context, c *app.RequestContext) {
	var req setNotificationRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	if req.Enabled == nil && req.Email == nil {
		s.fail(ctx, c, fmt.Errorf("%w: give enabled, email, or both", domain.ErrInvalidInput))
		return
	}

	p, _ := principal(c)

	var (
		pref notify.Pref
		err  error
	)

	if req.Enabled != nil {
		if pref, err = s.notifications.SetPref(ctx, p, c.Param("key"), *req.Enabled); err != nil {
			s.fail(ctx, c, err)
			return
		}
	}

	if req.Email != nil {
		if pref, err = s.notifications.SetEmailPref(ctx, p, c.Param("key"), *req.Email); err != nil {
			s.fail(ctx, c, err)
			return
		}
	}

	c.JSON(http.StatusOK, toNotificationDTO(pref))
}
