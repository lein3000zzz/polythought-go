package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/services/meta"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

// The meta is for every page, signed in or not, and the docs.
func TestMetaRoute(t *testing.T) {
	stars := 1234

	for name, tc := range map[string]struct {
		meta *meta.Meta
		want string
	}{
		"with stars":      {&meta.Meta{Stars: &stars}, `{"repo":"https://github.com/lein3000zzz/polythought-go","stars":1234}`},
		"GitHub silent":   {&meta.Meta{}, `{"repo":"https://github.com/lein3000zzz/polythought-go"}`},
		"no meta service": {nil, `{"repo":"https://github.com/lein3000zzz/polythought-go"}`},
	} {
		t.Run(name, func(t *testing.T) {
			services := httpapi.Services{Auth: httpapimocks.NewMockAuthService(t)} // never asked: no one signs in

			if tc.meta != nil {
				m := httpapimocks.NewMockMetaService(t)
				m.EXPECT().Meta(mock.Anything).Return(*tc.meta)
				services.Meta = m
			}

			srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), services)
			require.NoError(t, err)

			w := request{method: "GET", path: "/api/v1/meta"}.do(t, srv)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.JSONEq(t, tc.want, w.Body.String())
		})
	}
}
