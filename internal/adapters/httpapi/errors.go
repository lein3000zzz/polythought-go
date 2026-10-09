package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

const protocolLax = protocol.CookieSameSiteLaxMode

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// errorMapping lists the errors clients may see, with their status and stable code.
// Anything else is an internal error and its message stays in the logs.
var errorMapping = []struct {
	err    error
	status int
	code   string
}{
	{domain.ErrInvalidInput, http.StatusBadRequest, "invalid_input"},
	{domain.ErrInvalidEmail, http.StatusBadRequest, "invalid_email"},
	{domain.ErrInvalidAddress, http.StatusBadRequest, "invalid_address"},
	{domain.ErrWeakPassword, http.StatusBadRequest, "weak_password"},
	{domain.ErrInvalidInvite, http.StatusBadRequest, "invalid_invite"},
	{domain.ErrInvalidWorkflow, http.StatusBadRequest, "invalid_workflow"},
	{domain.ErrCredentialsRejected, http.StatusBadRequest, "credentials_rejected"},
	{domain.ErrInvalidCredentials, http.StatusUnauthorized, "invalid_credentials"},
	{domain.ErrUnauthenticated, http.StatusUnauthorized, "unauthenticated"},
	{domain.ErrUserDisabled, http.StatusForbidden, "user_disabled"},
	{domain.ErrRegistrationClosed, http.StatusForbidden, "registration_closed"},
	{domain.ErrForbidden, http.StatusForbidden, "forbidden"},
	{domain.ErrNotFound, http.StatusNotFound, "not_found"},
	{domain.ErrEmailTaken, http.StatusConflict, "email_taken"},
	{domain.ErrWalletExists, http.StatusConflict, "wallet_exists"},
	{domain.ErrLastAdmin, http.StatusConflict, "last_admin"},
	{domain.ErrConflict, http.StatusConflict, "conflict"},
	{domain.ErrRateLimited, http.StatusTooManyRequests, "rate_limited"},
	{domain.ErrUnavailable, http.StatusServiceUnavailable, "upstream_unavailable"},
}

// fail writes err as a JSON error response. Unexpected errors are logged with the
// request's trace and answered with a generic message.
func (s *Server) fail(ctx context.Context, c *app.RequestContext, err error) {
	for _, m := range errorMapping {
		if errors.Is(err, m.err) {
			c.JSON(m.status, errorBody{Error: errorDetail{Code: m.code, Message: err.Error()}})
			return
		}
	}

	s.log.Error(ctx, "Request failed", "path", string(c.Path()), logging.Err(err))

	c.JSON(http.StatusInternalServerError, errorBody{Error: errorDetail{Code: "internal", Message: "internal error"}})
}
