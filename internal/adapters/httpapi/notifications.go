package httpapi

import (
	"context"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/notify"
)

// NotificationService is what the handlers need to let users choose their notifications.
type NotificationService interface {
	Prefs(ctx context.Context, p auth.Principal) ([]notify.Pref, error)
	SetPref(ctx context.Context, p auth.Principal, key string, enabled bool) (notify.Pref, error)
}

type notificationDTO struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
} // @name Notification

type notificationList struct {
	Notifications []notificationDTO `json:"notifications"`
} // @name NotificationList

type setNotificationRequest struct {
	Enabled bool `json:"enabled"`
} // @name SetNotificationRequest

func toNotificationDTO(p notify.Pref) notificationDTO {
	return notificationDTO{Key: p.Key, Label: p.Label, Description: p.Description, Enabled: p.Enabled}
}

// listNotifications lists the kinds of notification and whether you get each. They go to
// your linked Telegram chat.
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

	out := notificationList{Notifications: make([]notificationDTO, len(prefs))}
	for i, pref := range prefs {
		out.Notifications[i] = toNotificationDTO(pref)
	}

	c.JSON(http.StatusOK, out)
}

// setNotification switches one kind of notification on or off for you.
//
//	@Summary	Switch a notification on or off
//	@ID			setNotification
//	@Tags		notifications
//	@Param		key		path		string					true	"Kind of notification, e.g. workflow_failed"
//	@Param		body	body		setNotificationRequest	true	"On or off"
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

	p, _ := principal(c)

	pref, err := s.notifications.SetPref(ctx, p, c.Param("key"), req.Enabled)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toNotificationDTO(pref))
}
