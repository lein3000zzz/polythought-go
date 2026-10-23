package httpapi_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

const (
	publicURL = "https://app.example"
	origin    = "Origin"
)

var (
	created = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	alice   = domain.User{ID: "u-1", Email: "alice@example.com", Role: domain.RoleUser, CreatedAt: created}
	root    = domain.User{ID: "u-0", Email: "root@example.com", Role: domain.RoleAdmin, CreatedAt: created}
)

type request struct {
	method  string
	path    string
	body    string
	headers map[string]string
}

func (r request) do(t *testing.T, srv *httpapi.Server) *ut.ResponseRecorder {
	t.Helper()

	var body *ut.Body
	if r.body != "" {
		body = &ut.Body{Body: bytes.NewBufferString(r.body), Len: len(r.body)}
	}

	headers := []ut.Header{{Key: "Content-Type", Value: "application/json"}}
	for k, v := range r.headers {
		headers = append(headers, ut.Header{Key: k, Value: v})
	}

	return ut.PerformRequest(srv.Engine().Engine, r.method, r.path, body, headers...)
}

func newServer(t *testing.T, svc *httpapimocks.MockAuthService, proxies ...*net.IPNet) *httpapi.Server {
	t.Helper()

	srv, err := httpapi.New(httpapi.Config{Addr: "127.0.0.1:0", PublicURL: publicURL, TrustedProxies: proxies}, observability.NoopUtility(), httpapi.Services{Auth: svc})
	require.NoError(t, err)

	return srv
}

func withSession(svc *httpapimocks.MockAuthService, u domain.User) {
	svc.EXPECT().AuthenticateSession(mock.Anything, "sess-token").Return(auth.Principal{User: u, SessionID: "s-1"}, nil)
}

func withToken(svc *httpapimocks.MockAuthService, u domain.User) {
	svc.EXPECT().AuthenticateToken(mock.Anything, "pt_secret").Return(auth.Principal{User: u, TokenID: "t-1"}, nil)
}

var (
	cookie = map[string]string{"Cookie": "__Host-polythought_session=sess-token", origin: publicURL}
	bearer = map[string]string{"Authorization": "Bearer pt_secret"}
)

func TestRoutes(t *testing.T) {
	tests := []struct {
		name       string
		req        request
		setup      func(svc *httpapimocks.MockAuthService)
		wantStatus int
		wantBody   []string // substrings
		check      func(t *testing.T, w *ut.ResponseRecorder)
	}{
		{
			name:       "health",
			req:        request{method: "GET", path: "/healthz"},
			setup:      func(*httpapimocks.MockAuthService) {},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"status":"ok"`},
		},
		{
			name: "registration mode and password rule",
			req:  request{method: "GET", path: "/api/v1/auth/config"},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().RegistrationMode().Return(domain.RegistrationInvite)
				svc.EXPECT().MinPasswordLength().Return(14)
				svc.EXPECT().PasswordReset().Return(true)
				svc.EXPECT().RequestsOpen().Return(true)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`{"registration":"invite","minPasswordLength":14,"passwordReset":true,"requests":true}`},
		},
		{
			name: "ask to join, answered the same whatever comes of it",
			req:  request{method: "POST", path: "/api/v1/auth/requests", body: `{"email":"new@example.com","note":"Hello"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().RequestToJoin(mock.Anything, "new@example.com", "Hello", mock.Anything).Return(nil)
			},
			wantStatus: http.StatusAccepted,
		},
		{
			name: "asking while requests are off",
			req:  request{method: "POST", path: "/api/v1/auth/requests", body: `{"email":"new@example.com"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().RequestToJoin(mock.Anything, "new@example.com", "", mock.Anything).Return(domain.ErrRequestsOff)
			},
			wantStatus: http.StatusForbidden,
			wantBody:   []string{`"code":"requests_off"`},
		},
		{
			name: "confirm a request by its link",
			req:  request{method: "POST", path: "/api/v1/auth/requests/confirm", body: `{"token":"tok"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().ConfirmJoin(mock.Anything, "tok", mock.Anything).Return(domain.ErrInvalidLink)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"invalid_link"`},
		},
		{
			name: "the requests, with how many wait",
			req:  request{method: "GET", path: "/api/v1/admin/requests", headers: cookie},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, root)
				at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
				svc.EXPECT().ListJoinRequests(mock.Anything, mock.Anything).Return([]domain.JoinRequest{
					{ID: "r-1", Email: "new@example.com", Note: "Hello", CreatedAt: at, ConfirmedAt: at},
					{ID: "r-2", Email: "old@example.com", CreatedAt: at, ConfirmedAt: at, DecidedAt: at, DecidedBy: root.ID},
				}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"pending":1`, `"status":"pending"`, `"status":"denied"`, `"note":"Hello"`},
		},
		{
			name: "approve a request, mailing an invite",
			req:  request{method: "POST", path: "/api/v1/admin/requests/r-1/approve", headers: cookie},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, root)
				svc.EXPECT().ApproveJoin(mock.Anything, mock.Anything, domain.JoinRequestID("r-1")).
					Return(domain.Invite{ID: "i-1", Email: "new@example.com", MaxUses: 1}, true, nil)
			},
			wantStatus: http.StatusCreated,
			wantBody:   []string{`"email":"new@example.com"`, `"sent":true`},
		},
		{
			name: "deny a request",
			req:  request{method: "POST", path: "/api/v1/admin/requests/r-1/deny", headers: cookie},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, root)
				svc.EXPECT().DenyJoin(mock.Anything, mock.Anything, domain.JoinRequestID("r-1")).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "ask for a reset link",
			req:  request{method: "POST", path: "/api/v1/auth/password-reset", body: `{"email":"a@b.c"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().RequestPasswordReset(mock.Anything, "a@b.c", mock.Anything).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "reset is off",
			req:  request{method: "POST", path: "/api/v1/auth/password-reset", body: `{"email":"a@b.c"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().RequestPasswordReset(mock.Anything, mock.Anything, mock.Anything).Return(domain.ErrPasswordResetOff)
			},
			wantStatus: http.StatusForbidden,
			wantBody:   []string{`"code":"password_reset_off"`},
		},
		{
			name: "set a new password",
			req:  request{method: "POST", path: "/api/v1/auth/password-reset/confirm", body: `{"token":"tok","password":"new one"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().ResetPassword(mock.Anything, "tok", "new one", mock.Anything).Return("alice@example.com", nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`{"email":"alice@example.com"}`},
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				assert.Empty(t, w.Result().Header.Peek("Set-Cookie"), "not signed in (D177)")
			},
		},
		{
			name: "a weak new password",
			req:  request{method: "POST", path: "/api/v1/auth/password-reset/confirm", body: `{"token":"tok","password":"x"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().ResetPassword(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", domain.ErrWeakPassword)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"weak_password"`},
		},
		{
			name: "register",
			req:  request{method: "POST", path: "/api/v1/auth/register", body: `{"email":"alice@example.com","password":"pw","inviteCode":"inv_1"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().Register(mock.Anything, mock.MatchedBy(func(in auth.RegisterInput) bool {
					return in.Email == "alice@example.com" && in.Password == "pw" && in.InviteCode == "inv_1"
				})).Return(auth.Registration{User: alice, MustVerify: true, Sent: true}, nil)
			},
			wantStatus: http.StatusCreated,
			wantBody:   []string{`"user":{"id":"u-1"`, `"role":"user"`, `"mustVerify":true`, `"sent":true`},
		},
		{
			name:       "unknown fields are rejected",
			req:        request{method: "POST", path: "/api/v1/auth/register", body: `{"email":"a@b.c","pasword":"typo"}`},
			setup:      func(*httpapimocks.MockAuthService) {},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"invalid_input"`, "pasword"},
		},
		{
			name: "domain errors map to codes",
			req:  request{method: "POST", path: "/api/v1/auth/register", body: `{"email":"a@b.c","password":"pw"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().Register(mock.Anything, mock.Anything).Return(auth.Registration{}, domain.ErrEmailTaken)
			},
			wantStatus: http.StatusConflict,
			wantBody:   []string{`"code":"email_taken"`},
		},
		{
			name: "login sets a hardened cookie",
			req:  request{method: "POST", path: "/api/v1/auth/login", body: `{"email":"alice@example.com","password":"pw"}`, headers: map[string]string{"User-Agent": "ut"}},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().Login(mock.Anything, mock.MatchedBy(func(in auth.LoginInput) bool { return in.UserAgent == "ut" })).
					Return(auth.LoginResult{User: alice, Token: "new-token", Session: domain.Session{AbsoluteExpiresAt: time.Now().Add(time.Hour)}}, nil)
			},
			wantStatus: http.StatusOK,
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				// Cookie attribute names are case-insensitive; Hertz writes "secure".
				c := strings.ToLower(string(w.Result().Header.Peek("Set-Cookie")))
				for _, attr := range []string{"__host-polythought_session=new-token", "httponly", "secure", "samesite=lax", "path=/"} {
					assert.Contains(t, c, attr)
				}
			},
		},
		{
			name: "login rate limited",
			req:  request{method: "POST", path: "/api/v1/auth/login", body: `{"email":"a@b.c","password":"pw"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().Login(mock.Anything, mock.Anything).Return(auth.LoginResult{}, domain.ErrRateLimited)
			},
			wantStatus: http.StatusTooManyRequests,
			wantBody:   []string{`"code":"rate_limited"`},
		},
		{
			name: "login of an account that must verify",
			req:  request{method: "POST", path: "/api/v1/auth/login", body: `{"email":"a@b.c","password":"pw"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().Login(mock.Anything, mock.Anything).Return(auth.LoginResult{}, domain.ErrEmailNotVerified)
			},
			wantStatus: http.StatusForbidden,
			wantBody:   []string{`"code":"email_not_verified"`},
		},
		{
			name: "verify an address",
			req:  request{method: "POST", path: "/api/v1/auth/verify-email", body: `{"token":"tok"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().VerifyEmail(mock.Anything, "tok", mock.Anything).Return("alice@example.com", nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`{"email":"alice@example.com"}`},
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				assert.Empty(t, w.Result().Header.Peek("Set-Cookie"), "not signed in (D172)")
			},
		},
		{
			name: "a used link",
			req:  request{method: "POST", path: "/api/v1/auth/verify-email", body: `{"token":"old"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().VerifyEmail(mock.Anything, "old", mock.Anything).Return("", domain.ErrInvalidLink)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"invalid_link"`},
		},
		{
			name: "send the link again",
			req:  request{method: "POST", path: "/api/v1/auth/verification", body: `{"email":"a@b.c"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().ResendVerification(mock.Anything, "a@b.c", mock.Anything).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "an admin sends it again",
			req:  request{method: "POST", path: "/api/v1/admin/users/u-2/verification", headers: cookie},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, alice)
				svc.EXPECT().SendVerificationLink(mock.Anything, mock.Anything, domain.UserID("u-2")).Return(domain.ErrMailNotSetUp)
			},
			wantStatus: http.StatusConflict,
			wantBody:   []string{`"code":"mail_not_set_up"`},
		},
		{
			name: "an admin marks an address verified",
			req:  request{method: "PATCH", path: "/api/v1/admin/users/u-2", headers: cookie, body: `{"emailVerified":true}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, alice)
				svc.EXPECT().UpdateUser(mock.Anything, mock.Anything, domain.UserID("u-2"), mock.MatchedBy(func(u auth.UserUpdate) bool {
					return u.EmailVerified != nil && *u.EmailVerified
				})).Return(domain.User{ID: "u-2", EmailVerifiedAt: time.Now()}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"emailVerified":true`},
		},
		{
			name:       "me needs authentication",
			req:        request{method: "GET", path: "/api/v1/me"},
			setup:      func(*httpapimocks.MockAuthService) {},
			wantStatus: http.StatusUnauthorized,
			wantBody:   []string{`"code":"unauthenticated"`},
		},
		{
			name:       "me with a session cookie",
			req:        request{method: "GET", path: "/api/v1/me", headers: cookie},
			setup:      func(svc *httpapimocks.MockAuthService) { withSession(svc, alice) },
			wantStatus: http.StatusOK,
			wantBody:   []string{`"email":"alice@example.com"`, `"codeChosen":false`, `"mayWriteCode":false`},
		},
		{
			name:       "me says an admin may write code while it is for chosen users",
			req:        request{method: "GET", path: "/api/v1/me", headers: cookie},
			setup:      func(svc *httpapimocks.MockAuthService) { withSession(svc, root) },
			wantStatus: http.StatusOK,
			wantBody:   []string{`"mayWriteCode":true`},
		},
		{
			name: "stale cookie is cleared",
			req:  request{method: "GET", path: "/api/v1/me", headers: cookie},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().AuthenticateSession(mock.Anything, "sess-token").Return(auth.Principal{}, domain.ErrUnauthenticated)
			},
			wantStatus: http.StatusUnauthorized,
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				c := string(w.Result().Header.Peek("Set-Cookie"))
				assert.Contains(t, c, "__Host-polythought_session=;")
				assert.Contains(t, c, "max-age=0")
			},
		},
		{
			name:       "me with an api token",
			req:        request{method: "GET", path: "/api/v1/me", headers: bearer},
			setup:      func(svc *httpapimocks.MockAuthService) { withToken(svc, alice) },
			wantStatus: http.StatusOK,
		},
		{
			name: "a bad token does not fall back to the cookie",
			req:  request{method: "GET", path: "/api/v1/me", headers: map[string]string{"Authorization": "Bearer pt_bad", "Cookie": "__Host-polythought_session=sess-token"}},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().AuthenticateToken(mock.Anything, "pt_bad").Return(auth.Principal{}, domain.ErrUnauthenticated)
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "non-bearer authorization",
			req:        request{method: "GET", path: "/api/v1/me", headers: map[string]string{"Authorization": "Basic YTpi"}},
			setup:      func(*httpapimocks.MockAuthService) {},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "cross-origin change with a cookie is blocked",
			req:        request{method: "POST", path: "/api/v1/admin/invites", headers: map[string]string{"Cookie": "__Host-polythought_session=sess-token", origin: "https://evil.example"}},
			setup:      func(svc *httpapimocks.MockAuthService) { withSession(svc, root) },
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "cross-site referer is blocked when origin is missing",
			req:        request{method: "POST", path: "/api/v1/admin/invites", headers: map[string]string{"Cookie": "__Host-polythought_session=sess-token", "Referer": "https://evil.example/page"}},
			setup:      func(svc *httpapimocks.MockAuthService) { withSession(svc, root) },
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "cross-origin login is blocked",
			req:        request{method: "POST", path: "/api/v1/auth/login", body: `{}`, headers: map[string]string{origin: "https://evil.example"}},
			setup:      func(*httpapimocks.MockAuthService) {},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "same-origin change is allowed",
			req:  request{method: "POST", path: "/api/v1/admin/invites", headers: cookie},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, root)
				svc.EXPECT().CreateInvite(mock.Anything, mock.Anything, auth.InviteSpec{}).
					Return(domain.Invite{ID: "i-1", Code: "inv_code", CreatedBy: root.ID, MaxUses: 1, ExpiresAt: time.Now().Add(time.Hour)}, nil)
			},
			wantStatus: http.StatusCreated,
			wantBody:   []string{`"code":"inv_code"`, `"maxUses":1`, `"used":0`, `"signups":[]`, `"status":"open"`},
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				assert.NotContains(t, w.Body.String(), "revokedAt", "unset times are left out, not null")
			},
		},
		{
			name: "bearer requests are not origin-checked",
			req:  request{method: "POST", path: "/api/v1/admin/invites", headers: map[string]string{"Authorization": "Bearer pt_secret", origin: "https://evil.example"}},
			setup: func(svc *httpapimocks.MockAuthService) {
				withToken(svc, root)
				svc.EXPECT().CreateInvite(mock.Anything, mock.Anything, mock.Anything).Return(domain.Invite{}, nil)
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "logout ends the session and clears the cookie",
			req:  request{method: "POST", path: "/api/v1/auth/logout", headers: cookie},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, alice)
				svc.EXPECT().Logout(mock.Anything, domain.SessionID("s-1")).Return(nil)
			},
			wantStatus: http.StatusNoContent,
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				assert.Contains(t, string(w.Result().Header.Peek("Set-Cookie")), "max-age=0")
			},
		},
		{
			name: "change password",
			req:  request{method: "PUT", path: "/api/v1/me/password", body: `{"currentPassword":"old","newPassword":"new"}`, headers: cookie},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, alice)
				svc.EXPECT().ChangePassword(mock.Anything, mock.Anything, "old", "new").Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "create a token shows the secret once",
			req:  request{method: "POST", path: "/api/v1/tokens", body: `{"name":"cli","expiresAt":"2027-01-01T00:00:00Z","scopes":["read","paper"]}`, headers: cookie},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, alice)
				svc.EXPECT().CreateAPIToken(mock.Anything, mock.Anything, "cli", time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), []domain.Scope{domain.ScopeRead, domain.ScopePaper}).
					Return("pt_full_secret", domain.APIToken{ID: "t-9", Name: "cli", Prefix: "pt_full_se", Scopes: []domain.Scope{domain.ScopeRead, domain.ScopePaper}}, nil)
			},
			wantStatus: http.StatusCreated,
			wantBody:   []string{`"token":"pt_full_secret"`, `"prefix":"pt_full_se"`, `"scopes":["read","paper"]`},
		},
		{
			name: "list tokens never shows secrets",
			req:  request{method: "GET", path: "/api/v1/tokens", headers: cookie},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, alice)
				svc.EXPECT().ListAPITokens(mock.Anything, mock.Anything).Return([]domain.APIToken{{ID: "t-9", Name: "cli", Prefix: "pt_abc"}}, nil)
			},
			wantStatus: http.StatusOK,
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				assert.NotContains(t, w.Body.String(), `"token"`)
			},
		},
		{
			name: "revoke a token",
			req:  request{method: "DELETE", path: "/api/v1/tokens/t-9", headers: cookie},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, alice)
				svc.EXPECT().RevokeAPIToken(mock.Anything, mock.Anything, domain.APITokenID("t-9")).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "list users with paging",
			req:  request{method: "GET", path: "/api/v1/admin/users?limit=5&cursor=abc", headers: bearer},
			setup: func(svc *httpapimocks.MockAuthService) {
				withToken(svc, root)
				svc.EXPECT().ListUsers(mock.Anything, mock.Anything, ports.PageFilter{Limit: 5, Cursor: "abc"}).
					Return(ports.Page[domain.User]{Items: []domain.User{alice}, NextCursor: "u-1"}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"nextCursor":"u-1"`, `"alice@example.com"`},
		},
		{
			name: "bad limit",
			req:  request{method: "GET", path: "/api/v1/admin/users?limit=-1", headers: bearer},
			setup: func(svc *httpapimocks.MockAuthService) {
				withToken(svc, root)
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "update a user",
			req:  request{method: "PATCH", path: "/api/v1/admin/users/u-1", body: `{"role":"admin","disabled":false}`, headers: bearer},
			setup: func(svc *httpapimocks.MockAuthService) {
				withToken(svc, root)
				svc.EXPECT().UpdateUser(mock.Anything, mock.Anything, domain.UserID("u-1"), mock.MatchedBy(func(u auth.UserUpdate) bool {
					return u.Role != nil && *u.Role == domain.RoleAdmin && u.Disabled != nil && !*u.Disabled && u.CodeChosen == nil
				})).Return(alice, nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "an admin's caps for a user's live buys (D222)",
			req:  request{method: "PATCH", path: "/api/v1/admin/users/u-1", body: `{"liveCaps":{"perOrder":250}}`, headers: bearer},
			setup: func(svc *httpapimocks.MockAuthService) {
				withToken(svc, root)

				perOrder, capped := 250, alice
				capped.LiveCaps.PerOrder = &perOrder

				svc.EXPECT().UpdateUser(mock.Anything, mock.Anything, domain.UserID("u-1"), mock.MatchedBy(func(u auth.UserUpdate) bool {
					return u.LiveCaps != nil && *u.LiveCaps.PerOrder == 250 && u.LiveCaps.PerDay == nil && u.Role == nil
				})).Return(capped, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"liveCaps":{"perOrder":250}`, `"liveStopped":false`},
		},
		{
			name: "an admin's caps on a user's agents (D271)",
			req: request{
				method: "PATCH", path: "/api/v1/admin/users/u-1", headers: bearer,
				body: `{"agentCaps":{"dollars":"12.50","totalTokens":0}}`,
			},
			setup: func(svc *httpapimocks.MockAuthService) {
				withToken(svc, root)

				dollars, none, capped := decimal.RequireFromString("12.50"), int64(0), alice
				capped.AgentCaps = domain.AgentCaps{Dollars: &dollars, TotalTokens: &none}

				svc.EXPECT().UpdateUser(mock.Anything, mock.Anything, domain.UserID("u-1"), mock.MatchedBy(func(u auth.UserUpdate) bool {
					return u.AgentCaps != nil && u.AgentCaps.Dollars.String() == "12.5" && *u.AgentCaps.TotalTokens == 0 &&
						u.AgentCaps.InputTokens == nil && u.LiveCaps == nil
				})).Return(capped, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"agentCaps":{"dollars":"12.50","totalTokens":0}`, `"killStopsAgents":false`},
		},
		{
			name: "agent caps that are no amount",
			req:  request{method: "PATCH", path: "/api/v1/admin/users/u-1", body: `{"agentCaps":{"dollars":"lots"}}`, headers: bearer},
			setup: func(svc *httpapimocks.MockAuthService) {
				withToken(svc, root)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`is not an amount, like 20.00`},
		},
		{
			name: "pick a user to write code",
			req:  request{method: "PATCH", path: "/api/v1/admin/users/u-1", body: `{"codeChosen":true}`, headers: bearer},
			setup: func(svc *httpapimocks.MockAuthService) {
				withToken(svc, root)

				chosen := alice
				chosen.CodeChosen = true

				svc.EXPECT().UpdateUser(mock.Anything, mock.Anything, domain.UserID("u-1"), mock.MatchedBy(func(u auth.UserUpdate) bool {
					return u.CodeChosen != nil && *u.CodeChosen && u.Role == nil && u.Disabled == nil
				})).Return(chosen, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"codeChosen":true`, `"mayWriteCode":true`},
		},
		{
			name: "admin creates a user with the default role",
			req:  request{method: "POST", path: "/api/v1/admin/users", body: `{"email":"new@example.com","password":"pw"}`, headers: bearer},
			setup: func(svc *httpapimocks.MockAuthService) {
				withToken(svc, root)
				svc.EXPECT().CreateUser(mock.Anything, mock.Anything, "new@example.com", "pw", domain.RoleUser).Return(alice, nil)
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "invite by email",
			req:  request{method: "POST", path: "/api/v1/admin/invites", headers: cookie, body: `{"email":"new@example.com"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, root)
				svc.EXPECT().InviteByEmail(mock.Anything, mock.Anything, "new@example.com").
					Return(domain.Invite{ID: "i-1", Code: "inv_code", CreatedBy: root.ID, Email: "new@example.com"}, false, nil)
			},
			wantStatus: http.StatusCreated,
			wantBody:   []string{`"code":"inv_code"`, `"email":"new@example.com"`, `"sent":false`},
		},
		{
			name: "invite an address with an account",
			req:  request{method: "POST", path: "/api/v1/admin/invites", headers: cookie, body: `{"email":"bob@example.com"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, root)
				svc.EXPECT().InviteByEmail(mock.Anything, mock.Anything, mock.Anything).Return(domain.Invite{}, false, domain.ErrEmailTaken)
			},
			wantStatus: http.StatusConflict,
			wantBody:   []string{`"code":"email_taken"`},
		},
		{
			name: "a typed code, its uses and its end (D371)",
			req: request{method: "POST", path: "/api/v1/admin/invites", headers: cookie,
				body: `{"code":"Spring-2026","maxUses":25,"expiresAt":"2026-11-30T00:00:00Z"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, root)
				svc.EXPECT().CreateInvite(mock.Anything, mock.Anything, auth.InviteSpec{
					Code: "Spring-2026", MaxUses: 25, ExpiresAt: time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC),
				}).Return(domain.Invite{ID: "i-1", Code: "Spring-2026", MaxUses: 25}, nil)
			},
			wantStatus: http.StatusCreated,
			wantBody:   []string{`"code":"Spring-2026"`, `"maxUses":25`},
		},
		{
			name: "a code another invite has",
			req:  request{method: "POST", path: "/api/v1/admin/invites", headers: cookie, body: `{"code":"Spring-2026"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, root)
				svc.EXPECT().CreateInvite(mock.Anything, mock.Anything, mock.Anything).Return(domain.Invite{}, domain.ErrInviteCodeTaken)
			},
			wantStatus: http.StatusConflict,
			wantBody:   []string{`"code":"invite_code_taken"`},
		},
		{
			name: "a mailed invite takes no code, uses or end",
			req:  request{method: "POST", path: "/api/v1/admin/invites", headers: cookie, body: `{"email":"new@example.com","maxUses":5}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, root)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"invalid_input"`, `a mailed invite is one use`},
		},
		{
			name: "a typed code checked as the admin types",
			req:  request{method: "POST", path: "/api/v1/admin/invites/check", headers: cookie, body: `{"code":"short"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, root)
				svc.EXPECT().CheckInviteCode(mock.Anything, mock.Anything, "short").Return("A code has 8 to 64 characters.", nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`{"problem":"A code has 8 to 64 characters."}`},
		},
		{
			name: "a code free to take",
			req:  request{method: "POST", path: "/api/v1/admin/invites/check", headers: cookie, body: `{"code":"free-code"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, root)
				svc.EXPECT().CheckInviteCode(mock.Anything, mock.Anything, "free-code").Return("", nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`{}`},
		},
		{
			name: "revoke an invite",
			req:  request{method: "POST", path: "/api/v1/admin/invites/i-1/revoke", headers: cookie},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, root)
				svc.EXPECT().RevokeInvite(mock.Anything, mock.Anything, domain.InviteID("i-1")).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "the invite list says whether invites can be mailed",
			req:  request{method: "GET", path: "/api/v1/admin/invites", headers: cookie},
			setup: func(svc *httpapimocks.MockAuthService) {
				withSession(svc, root)
				svc.EXPECT().ListInvites(mock.Anything, mock.Anything).Return([]domain.Invite{
					{ID: "i-1", Email: "new@example.com", MaxUses: 1, Used: 1, Signups: []domain.InviteUse{
						{UserID: "u-9", Email: "new@example.com", At: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)},
					}},
					{ID: "i-2", MaxUses: 5, ExpiresAt: time.Now().Add(time.Hour), RevokedAt: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)},
				}, nil)
				svc.EXPECT().InviteEmail().Return(true)
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`"email":"new@example.com"`, `"byEmail":true`, `"status":"used"`, `"status":"revoked"`,
				`"signups":[{"userId":"u-9","email":"new@example.com","at":"2026-10-20T09:00:00Z"}]`,
			},
		},
		{
			name: "look an invite up",
			req:  request{method: "POST", path: "/api/v1/auth/invite", body: `{"code":"inv_code"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().InviteFor(mock.Anything, "inv_code", mock.Anything).Return(domain.Invite{Email: "new@example.com"}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`{"email":"new@example.com"}`},
		},
		{
			name: "a used invite",
			req:  request{method: "POST", path: "/api/v1/auth/invite", body: `{"code":"used"}`},
			setup: func(svc *httpapimocks.MockAuthService) {
				svc.EXPECT().InviteFor(mock.Anything, "used", mock.Anything).Return(domain.Invite{}, domain.ErrInvalidInvite)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"invalid_invite"`},
		},
		{
			name: "forbidden for regular users",
			req:  request{method: "GET", path: "/api/v1/admin/invites", headers: bearer},
			setup: func(svc *httpapimocks.MockAuthService) {
				withToken(svc, alice)
				svc.EXPECT().ListInvites(mock.Anything, mock.Anything).Return(nil, domain.ErrForbidden)
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "internal errors do not leak",
			req:  request{method: "GET", path: "/api/v1/tokens", headers: bearer},
			setup: func(svc *httpapimocks.MockAuthService) {
				withToken(svc, alice)
				svc.EXPECT().ListAPITokens(mock.Anything, mock.Anything).Return(nil, errors.New("pq: password authentication failed for db"))
			},
			wantStatus: http.StatusInternalServerError,
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				assert.JSONEq(t, `{"error":{"code":"internal","message":"internal error"}}`, w.Body.String())
			},
		},
		{
			name:       "unknown route",
			req:        request{method: "GET", path: "/api/v1/nope"},
			setup:      func(*httpapimocks.MockAuthService) {},
			wantStatus: http.StatusNotFound,
			wantBody:   []string{`"code":"not_found"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := httpapimocks.NewMockAuthService(t)
			svc.EXPECT().CodeAccess().Return(domain.CodeChosen).Maybe()
			tt.setup(svc)

			w := tt.req.do(t, newServer(t, svc))

			assert.Equal(t, tt.wantStatus, w.Code, w.Body.String())

			for _, s := range tt.wantBody {
				assert.Contains(t, w.Body.String(), s)
			}

			if tt.check != nil {
				tt.check(t, w)
			}
		})
	}
}

// TestClientIP runs a real listener: Hertz's ut helper skips the engine's client-IP
// function, so only real connections show which address the server believes.
func TestClientIP(t *testing.T) {
	_, everyone, _ := net.ParseCIDR("0.0.0.0/0")

	tests := []struct {
		name    string
		proxies []*net.IPNet
		later   []*net.IPNet // set while the server runs
		want    string
	}{
		{name: "forwarded headers ignored by default", want: "127.0.0.1"},
		{name: "trusted proxy may forward", proxies: []*net.IPNet{everyone}, want: "9.9.9.9"},
		{name: "proxies trusted while running", later: []*net.IPNet{everyone}, want: "9.9.9.9"},
		{name: "proxies distrusted while running", proxies: []*net.IPNet{everyone}, later: []*net.IPNet{}, want: "127.0.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := httpapimocks.NewMockAuthService(t)

			gotIP := make(chan string, 1)
			svc.EXPECT().Login(mock.Anything, mock.Anything).
				RunAndReturn(func(_ context.Context, in auth.LoginInput) (auth.LoginResult, error) {
					gotIP <- in.IP
					return auth.LoginResult{}, domain.ErrInvalidCredentials
				})

			addr := freeAddr(t)

			srv, err := httpapi.New(httpapi.Config{Addr: addr, PublicURL: publicURL, TrustedProxies: tt.proxies}, observability.NoopUtility(), httpapi.Services{Auth: svc})
			require.NoError(t, err)

			go func() { _ = srv.Run() }()
			t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

			require.Eventually(t, func() bool {
				resp, err := client.Get("http://" + addr + "/healthz")
				if err != nil {
					return false
				}

				_ = resp.Body.Close()

				return resp.StatusCode == http.StatusOK
			}, 3*time.Second, 10*time.Millisecond)

			if tt.later != nil {
				srv.SetTrustedProxies(tt.later)
			}

			req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/api/v1/auth/login", strings.NewReader(`{}`))
			require.NoError(t, err)
			req.Header.Set("X-Forwarded-For", "9.9.9.9")

			resp, err := client.Do(req)
			require.NoError(t, err)
			_ = resp.Body.Close()

			assert.Equal(t, tt.want, <-gotIP)
		})
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := l.Addr().String()
	require.NoError(t, l.Close())

	return addr
}

func TestNewRejectsRelativePublicURL(t *testing.T) {
	_, err := httpapi.New(httpapi.Config{PublicURL: "app.example"}, observability.NoopUtility(), httpapi.Services{})
	require.Error(t, err)
}
