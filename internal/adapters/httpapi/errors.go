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

// statusClientClosedRequest is nginx's status for a request its client abandoned.
const statusClientClosedRequest = 499

type errorBody struct {
	Error errorDetail `json:"error"`
} // @name Error

type errorDetail struct {
	// Code is stable and meant for programs, e.g. invalid_workflow or not_found.
	Code    string `json:"code"`
	Message string `json:"message"`

	// Problems lists everything wrong with a submitted workflow (code invalid_workflow),
	// with changed settings (code invalid_input, param is the setting's key) or with a
	// credential (code invalid_input, param is "name" or "values.<field>"), with a
	// custom node (code invalid_input, param is the part of its form: "name",
	// "settings.0.default", "outputs.1", "items", "source"), or with workflow code that
	// does not compile (code invalid_code, with line and column).
	Problems []problemDTO `json:"problems,omitempty" validate:"optional"`

	// Workflows lists the workflows a change to a custom node would break (code
	// breaks_workflows), with what is wrong in each.
	Workflows []brokenWorkflowDTO `json:"workflows,omitempty" validate:"optional"`
} // @name ErrorDetail

// problemDTO points at the node and parameter a problem is about, when it is about one,
// so an editor can highlight them.
type problemDTO struct {
	NodeID string `json:"nodeId,omitempty" validate:"optional"`
	Param  string `json:"param,omitempty" validate:"optional"`
	// Line and Column are where in the code a problem is, from 1, columns in characters.
	Line    int    `json:"line,omitempty" validate:"optional"`
	Column  int    `json:"column,omitempty" validate:"optional"`
	Message string `json:"message"`
} // @name Problem

func toProblemDTOs(err error) []problemDTO {
	var v *domain.ValidationError
	if !errors.As(err, &v) {
		return nil
	}

	out := make([]problemDTO, len(v.Problems))
	for i, p := range v.Problems {
		out[i] = problemDTO{NodeID: string(p.NodeID), Param: p.Param, Message: p.Message}
	}

	return out
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
	{domain.ErrCodeOff, http.StatusForbidden, "code_off"},
	{domain.ErrKeyImportOff, http.StatusForbidden, "key_import_off"},
	{domain.ErrKeyNotForWallet, http.StatusBadRequest, "key_not_for_wallet"},
	{domain.ErrSessionKeysOff, http.StatusForbidden, "session_keys_off"},
	{domain.ErrNotDepositWallet, http.StatusBadRequest, "not_deposit_wallet"},
	{domain.ErrWrongSigner, http.StatusBadRequest, "wrong_signer"},
	{domain.ErrSignatureExpired, http.StatusBadRequest, "signature_expired"},
	{domain.ErrRelayerRefused, http.StatusBadRequest, "relayer_refused"},
	{domain.ErrSessionKeyInUse, http.StatusConflict, "session_key_in_use"},
	{domain.ErrSessionKeyPending, http.StatusConflict, "session_key_pending"},
	{domain.ErrNoBuilderKey, http.StatusForbidden, "builder_key_missing"},
	{domain.ErrEOAApprovals, http.StatusBadRequest, "eoa_approvals"},
	{domain.ErrNeedsImportedKey, http.StatusBadRequest, "needs_imported_key"},
	{domain.ErrNotOwner, http.StatusBadRequest, "not_owner"},
	{domain.ErrNotDeployed, http.StatusBadRequest, "not_deployed"},
	{domain.ErrSetupPending, http.StatusConflict, "setup_pending"},
	{domain.ErrCredentialsRejected, http.StatusBadRequest, "credentials_rejected"},
	{domain.ErrInvalidCredentials, http.StatusUnauthorized, "invalid_credentials"},
	{domain.ErrUnauthenticated, http.StatusUnauthorized, "unauthenticated"},
	{domain.ErrUserDisabled, http.StatusForbidden, "user_disabled"},
	{domain.ErrRegistrationClosed, http.StatusForbidden, "registration_closed"},
	{domain.ErrForbidden, http.StatusForbidden, "forbidden"},
	{domain.ErrNotFound, http.StatusNotFound, "not_found"},
	{domain.ErrEmailTaken, http.StatusConflict, "email_taken"},
	{domain.ErrWalletExists, http.StatusConflict, "wallet_exists"},
	{domain.ErrCredentialExists, http.StatusConflict, "credential_exists"},
	{domain.ErrCustomNodeTaken, http.StatusConflict, "custom_node_taken"},
	{domain.ErrBreaksWorkflows, http.StatusConflict, "breaks_workflows"},
	{domain.ErrLastAdmin, http.StatusConflict, "last_admin"},
	{domain.ErrConflict, http.StatusConflict, "conflict"},
	{domain.ErrNotWaiting, http.StatusConflict, "not_waiting"},
	{domain.ErrRateLimited, http.StatusTooManyRequests, "rate_limited"},
	{domain.ErrUnavailable, http.StatusServiceUnavailable, "upstream_unavailable"},
	{domain.ErrTelegramUnavailable, http.StatusServiceUnavailable, "telegram_unavailable"},
}

// fail writes err as a JSON error response. Unexpected errors are logged with the
// request's trace and answered with a generic message.
func (s *Server) fail(ctx context.Context, c *app.RequestContext, err error) {
	// The client went away mid-request, so its work was cancelled: nobody is waiting for
	// an answer, and it is not the server's fault.
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		c.SetStatusCode(statusClientClosedRequest)
		return
	}

	for _, m := range errorMapping {
		if errors.Is(err, m.err) {
			c.JSON(m.status, errorBody{Error: errorDetail{
				Code: m.code, Message: err.Error(), Problems: toProblemDTOs(err), Workflows: toBrokenWorkflowDTOs(err),
			}})
			return
		}
	}

	s.log.Error(ctx, "Request failed", "path", string(c.Path()), logging.Err(err))

	c.JSON(http.StatusInternalServerError, errorBody{Error: errorDetail{Code: "internal", Message: "internal error"}})
}
