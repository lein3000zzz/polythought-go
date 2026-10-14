package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/services/credentials"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestCredentialRoutes(t *testing.T) {
	relay := domain.Credential{
		ID: "c-1", OwnerID: alice.ID, Type: "http.header", Name: "Relay",
		Fields: map[string]string{"header": "Authorization"}, Secrets: []string{"value"},
		CreatedAt: created, UpdatedAt: created,
	}

	tests := []struct {
		name       string
		req        request
		setup      func(s *httpapimocks.MockCredentialService)
		wantStatus int
		wantBody   []string
	}{
		{
			name: "types say which fields are secret and whether they can be tested",
			req:  request{method: "GET", path: "/api/v1/credential-types", headers: cookie},
			setup: func(s *httpapimocks.MockCredentialService) {
				s.EXPECT().Types().Return([]engine.CredentialType{{
					Name: "http.header", Label: "HTTP header",
					Fields: []engine.CredentialField{
						{Name: "header", Label: "Header", Required: true},
						{Name: "value", Label: "Value", Required: true, Secret: true},
					},
				}, {
					Name: "test.api", Label: "API", Fields: []engine.CredentialField{{Name: "key", Label: "Key"}},
					Test: func(context.Context, map[string]string) error { return nil },
				}})
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`{"name":"http.header","label":"HTTP header","fields":[{"name":"header","label":"Header","required":true,"secret":false},{"name":"value","label":"Value","required":true,"secret":true}],"testable":false}`,
				`"name":"test.api"`, `"testable":true`,
			},
		},
		{
			name: "a list never carries secret values",
			req:  request{method: "GET", path: "/api/v1/credentials", headers: cookie},
			setup: func(s *httpapimocks.MockCredentialService) {
				s.EXPECT().List(mock.Anything, mock.Anything).Return([]domain.Credential{relay}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`{"credentials":[{"id":"c-1","type":"http.header","name":"Relay","values":{"header":"Authorization"},"secrets":["value"],"createdAt":"2026-10-08T12:00:00Z","updatedAt":"2026-10-08T12:00:00Z"}]}`,
			},
		},
		{
			name: "create",
			req: request{
				method: "POST", path: "/api/v1/credentials", headers: cookie,
				body: `{"type":"http.header","name":"Relay","values":{"header":"Authorization","value":"Bearer t-1"}}`,
			},
			setup: func(s *httpapimocks.MockCredentialService) {
				s.EXPECT().Create(mock.Anything, mock.Anything, credentials.Input{
					Type: "http.header", Name: "Relay", Values: map[string]string{"header": "Authorization", "value": "Bearer t-1"},
				}).Return(relay, nil)
			},
			wantStatus: http.StatusCreated,
			wantBody:   []string{`"id":"c-1"`, `"secrets":["value"]`},
		},
		{
			name: "problems point at the field",
			req:  request{method: "POST", path: "/api/v1/credentials", headers: cookie, body: `{"type":"http.header","name":"","values":{}}`},
			setup: func(s *httpapimocks.MockCredentialService) {
				v := domain.ValidationError{Base: domain.ErrInvalidInput}
				v.Add(domain.Problem{Param: "values.value", Message: "Value is required"})
				s.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(domain.Credential{}, v.Err())
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"invalid_input"`, `"problems":[{"param":"values.value","message":"Value is required"}]`},
		},
		{
			name: "a name already used",
			req:  request{method: "POST", path: "/api/v1/credentials", headers: cookie, body: `{"type":"http.header","name":"Relay","values":{}}`},
			setup: func(s *httpapimocks.MockCredentialService) {
				s.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(domain.Credential{}, domain.ErrCredentialExists)
			},
			wantStatus: http.StatusConflict,
			wantBody:   []string{`"code":"credential_exists"`},
		},
		{
			name: "update",
			req:  request{method: "PATCH", path: "/api/v1/credentials/c-1", headers: cookie, body: `{"name":"Renamed","values":{"value":""}}`},
			setup: func(s *httpapimocks.MockCredentialService) {
				s.EXPECT().Update(mock.Anything, mock.Anything, domain.CredentialID("c-1"), credentials.Change{
					Name: "Renamed", Values: map[string]string{"value": ""},
				}).Return(relay, nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "someone else's is not found",
			req:  request{method: "DELETE", path: "/api/v1/credentials/c-9", headers: cookie},
			setup: func(s *httpapimocks.MockCredentialService) {
				s.EXPECT().Delete(mock.Anything, mock.Anything, domain.CredentialID("c-9")).Return(domain.ErrNotFound)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "delete",
			req:  request{method: "DELETE", path: "/api/v1/credentials/c-1", headers: cookie},
			setup: func(s *httpapimocks.MockCredentialService) {
				s.EXPECT().Delete(mock.Anything, mock.Anything, domain.CredentialID("c-1")).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "a failed test is a result",
			req:  request{method: "POST", path: "/api/v1/credentials/c-1/test", headers: cookie},
			setup: func(s *httpapimocks.MockCredentialService) {
				tested := relay
				tested.TestedAt, tested.TestError = created, "the service refused the key"
				s.EXPECT().Test(mock.Anything, mock.Anything, domain.CredentialID("c-1")).Return(tested, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"testedAt":"2026-10-08T12:00:00Z","testError":"the service refused the key"`},
		},
		{
			name: "failing to test is an error",
			req:  request{method: "POST", path: "/api/v1/credentials/c-1/test", headers: cookie},
			setup: func(s *httpapimocks.MockCredentialService) {
				s.EXPECT().Test(mock.Anything, mock.Anything, domain.CredentialID("c-1")).Return(domain.Credential{}, errors.New("box broken"))
			},
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authSvc := httpapimocks.NewMockAuthService(t)
			withSession(authSvc, alice)

			svc := httpapimocks.NewMockCredentialService(t)
			tt.setup(svc)

			srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
				httpapi.Services{Auth: authSvc, Credentials: svc})
			require.NoError(t, err)

			w := tt.req.do(t, srv)

			assert.Equal(t, tt.wantStatus, w.Code, w.Body.String())

			for _, s := range tt.wantBody {
				assert.Contains(t, w.Body.String(), s)
			}
		})
	}
}
