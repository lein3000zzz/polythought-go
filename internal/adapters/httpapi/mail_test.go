package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestSendTestMail(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{"sent", nil, http.StatusNoContent, ""},
		{"no mail server", domain.ErrMailNotSetUp, http.StatusConflict, `"code":"mail_not_set_up"`},
		{"refused, saying how", fmt.Errorf("%w: 535 5.7.8 Authentication credentials invalid", domain.ErrMailFailed), http.StatusBadGateway,
			`"message":"the mail server did not take the message: 535 5.7.8 Authentication credentials invalid"`},
		{"not an address", domain.ErrInvalidEmail, http.StatusBadRequest, `"code":"invalid_email"`},
		{"not an admin", domain.ErrForbidden, http.StatusForbidden, `"code":"forbidden"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authSvc := httpapimocks.NewMockAuthService(t)
			withSession(authSvc, alice)

			mailSvc := httpapimocks.NewMockMailService(t)
			mailSvc.EXPECT().SendTest(mock.Anything, mock.Anything, "ops@example.com").Return(tt.err)

			srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc, Mail: mailSvc})
			require.NoError(t, err)

			w := request{method: "POST", path: "/api/v1/admin/mail/test", headers: cookie, body: `{"to":"ops@example.com"}`}.do(t, srv)

			assert.Equal(t, tt.wantStatus, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), tt.wantBody)
		})
	}
}
