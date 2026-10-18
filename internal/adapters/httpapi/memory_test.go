package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestNodeMemory(t *testing.T) {
	authSvc := httpapimocks.NewMockAuthService(t)
	withSession(authSvc, alice)

	memory := httpapimocks.NewMockMemoryService(t)
	srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
		httpapi.Services{Auth: authSvc, Memory: memory})
	require.NoError(t, err)

	byAlice := mock.MatchedBy(func(p auth.Principal) bool { return p.User.ID == alice.ID })

	t.Run("how much a node remembers", func(t *testing.T) {
		memory.EXPECT().Count(mock.Anything, byAlice, domain.WorkflowID("wf-1"), domain.NodeID("new")).Return(312, nil).Once()

		w := request{method: "GET", path: "/api/v1/workflows/wf-1/nodes/new/memory", headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"count":312}`, w.Body.String())
	})

	t.Run("forgotten", func(t *testing.T) {
		memory.EXPECT().Forget(mock.Anything, byAlice, domain.WorkflowID("wf-1"), domain.NodeID("new")).Return(nil).Once()

		w := request{method: "DELETE", path: "/api/v1/workflows/wf-1/nodes/new/memory", headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	})

	t.Run("someone else's workflow is not there", func(t *testing.T) {
		memory.EXPECT().Count(mock.Anything, byAlice, domain.WorkflowID("wf-bob"), domain.NodeID("new")).Return(0, domain.ErrNotFound).Once()

		w := request{method: "GET", path: "/api/v1/workflows/wf-bob/nodes/new/memory", headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("signed in only", func(t *testing.T) {
		w := request{method: "DELETE", path: "/api/v1/workflows/wf-1/nodes/new/memory"}.do(t, srv)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}
