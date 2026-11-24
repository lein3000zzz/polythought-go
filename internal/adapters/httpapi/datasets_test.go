package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/datasets"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestDatasets(t *testing.T) {
	at := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	forecasts := domain.Dataset{Name: "forecasts", KeyField: "city", KeepRows: 1000, Rows: 2, Bytes: 48, CreatedAt: at, UpdatedAt: at}

	server := func(t *testing.T, authSvc *httpapimocks.MockAuthService, ds *httpapimocks.MockDatasetService) *httpapi.Server {
		t.Helper()

		srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc, Datasets: ds})
		require.NoError(t, err)

		return srv
	}

	withScopes := func(t *testing.T, scopes ...domain.Scope) *httpapimocks.MockAuthService {
		t.Helper()

		authSvc := httpapimocks.NewMockAuthService(t)
		scoped(authSvc, alice, scopes...)

		return authSvc
	}

	t.Run("a notebook makes one, appends, reads a page and a row by its key", func(t *testing.T) {
		ds := httpapimocks.NewMockDatasetService(t)
		ds.EXPECT().Create(mock.Anything, alice.ID, datasets.Spec{Name: "forecasts", KeyField: "city", KeepRows: 1000}).Return(forecasts, nil).Once()
		ds.EXPECT().Append(mock.Anything, alice.ID, "forecasts", mock.MatchedBy(func(rows []json.RawMessage) bool {
			return len(rows) == 2 && string(rows[0]) == `{"city": "NYC", "high": 51}`
		})).Return(forecasts, nil).Once()
		ds.EXPECT().Rows(mock.Anything, alice.ID, "forecasts", int64(0), 2).Return([]domain.DatasetRow{
			{Seq: 7, Key: "NYC", At: at, Row: []byte(`{"city":"NYC","high":51}`)},
			{Seq: 8, Key: "LDN", At: at, Row: []byte(`{"city":"LDN","high":9}`)},
		}, nil).Once()
		ds.EXPECT().Lookup(mock.Anything, alice.ID, "forecasts", "NYC").Return(domain.DatasetRow{Seq: 7, Key: "NYC", At: at, Row: []byte(`{"city":"NYC"}`)}, nil).Once()

		srv := server(t, withScopes(t, domain.ScopeData), ds)

		w := request{method: "POST", path: "/api/v1/datasets", body: `{"name": "forecasts", "keyField": "city", "keepRows": 1000}`, headers: bearer}.do(t, srv)
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		assert.JSONEq(t, `{"name": "forecasts", "keyField": "city", "keepDays": 0, "keepRows": 1000, "rows": 2, "bytes": 48,
			"createdAt": "2026-11-24T12:00:00Z", "updatedAt": "2026-11-24T12:00:00Z"}`, w.Body.String())

		w = request{method: "POST", path: "/api/v1/datasets/forecasts/rows", body: `{"rows": [{"city": "NYC", "high": 51}, {"city": "LDN", "high": 9}]}`, headers: bearer}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		w = request{method: "GET", path: "/api/v1/datasets/forecasts/rows?limit=2", headers: bearer}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"rows": [
			{"seq": 7, "key": "NYC", "at": "2026-11-24T12:00:00Z", "row": {"city": "NYC", "high": 51}},
			{"seq": 8, "key": "LDN", "at": "2026-11-24T12:00:00Z", "row": {"city": "LDN", "high": 9}}
		], "next": 8}`, w.Body.String(), "a full page says where the next starts")

		w = request{method: "GET", path: "/api/v1/datasets/forecasts/rows/NYC", headers: bearer}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	})

	t.Run("a replace, staged then finished", func(t *testing.T) {
		ds := httpapimocks.NewMockDatasetService(t)
		ds.EXPECT().StartReplace(mock.Anything, alice.ID, "forecasts").Return("s-1", nil).Once()
		ds.EXPECT().StageRows(mock.Anything, alice.ID, "forecasts", ports.DatasetStageID("s-1"), mock.Anything).Return(2, nil).Once()
		ds.EXPECT().FinishReplace(mock.Anything, alice.ID, "forecasts", ports.DatasetStageID("s-1")).Return(forecasts, nil).Once()

		srv := server(t, withScopes(t, domain.ScopeData), ds)

		w := request{method: "POST", path: "/api/v1/datasets/forecasts/replace", headers: bearer}.do(t, srv)
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		assert.JSONEq(t, `{"stage": "s-1", "staged": 0}`, w.Body.String())

		w = request{method: "POST", path: "/api/v1/datasets/forecasts/replace/s-1/rows", body: `{"rows": [{"city": "A"}, {"city": "B"}]}`, headers: bearer}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"stage": "s-1", "staged": 2}`, w.Body.String())

		w = request{method: "POST", path: "/api/v1/datasets/forecasts/replace/s-1/finish", headers: bearer}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	})

	t.Run("as CSV, a column a field", func(t *testing.T) {
		ds := httpapimocks.NewMockDatasetService(t)
		ds.EXPECT().Rows(mock.Anything, alice.ID, "forecasts", int64(0), datasets.MaxPage).Return([]domain.DatasetRow{
			{Seq: 1, Row: []byte(`{"city":"NYC","high":51,"models":{"gfs":50}}`)},
			{Seq: 2, Row: []byte(`{"city":"LDN","note":"rain, then sun"}`)},
		}, nil).Once()

		w := request{method: "GET", path: "/api/v1/datasets/forecasts/csv", headers: bearer}.do(t, server(t, withScopes(t, domain.ScopeRead), ds))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, "city,high,models,note\nNYC,51,\"{\"\"gfs\"\":50}\",\nLDN,,,\"rain, then sun\"\n", w.Body.String())
		assert.Contains(t, string(w.Header().Get("Content-Disposition")), `filename="forecasts.csv"`)
	})

	t.Run("a read token reads them but writes none", func(t *testing.T) {
		ds := httpapimocks.NewMockDatasetService(t)
		ds.EXPECT().List(mock.Anything, alice.ID).Return([]domain.Dataset{forecasts}, nil).Once()

		srv := server(t, withScopes(t, domain.ScopeRead, domain.ScopeBuild), ds)

		w := request{method: "GET", path: "/api/v1/datasets", headers: bearer}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		for _, r := range []request{
			{method: "POST", path: "/api/v1/datasets", body: `{"name": "x"}`, headers: bearer},
			{method: "POST", path: "/api/v1/datasets/forecasts/rows", body: `{"rows": []}`, headers: bearer},
			{method: "DELETE", path: "/api/v1/datasets/forecasts", headers: bearer},
			{method: "POST", path: "/api/v1/datasets/forecasts/replace", headers: bearer},
		} {
			w := r.do(t, srv)
			assert.Equal(t, http.StatusForbidden, w.Code, r.method+" "+r.path)
		}
	})

	t.Run("why it refuses", func(t *testing.T) {
		ds := httpapimocks.NewMockDatasetService(t)
		ds.EXPECT().List(mock.Anything, alice.ID).Return(nil, domain.ErrDataOff).Once()
		ds.EXPECT().Create(mock.Anything, alice.ID, mock.Anything).Return(domain.Dataset{}, domain.ErrDatasetExists).Once()
		ds.EXPECT().Append(mock.Anything, alice.ID, "forecasts", mock.Anything).Return(domain.Dataset{}, domain.ErrDatasetFull).Once()

		srv := server(t, withScopes(t, domain.ScopeData), ds)

		for _, tc := range []struct {
			req  request
			want int
			code string
		}{
			{req: request{method: "GET", path: "/api/v1/datasets", headers: bearer}, want: http.StatusForbidden, code: "data_off"},
			{req: request{method: "POST", path: "/api/v1/datasets", body: `{"name": "forecasts"}`, headers: bearer}, want: http.StatusConflict, code: "dataset_exists"},
			{req: request{method: "POST", path: "/api/v1/datasets/forecasts/rows", body: `{"rows": [{}]}`, headers: bearer}, want: http.StatusConflict, code: "dataset_full"},
		} {
			w := tc.req.do(t, srv)
			assert.Equal(t, tc.want, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), `"code":"`+tc.code+`"`)
		}
	})

	t.Run("an admin sees each user's storage", func(t *testing.T) {
		authSvc := httpapimocks.NewMockAuthService(t)
		withSession(authSvc, root)

		ds := httpapimocks.NewMockDatasetService(t)
		ds.EXPECT().Usage(mock.Anything).Return(map[domain.UserID]int64{"u-1": 2048}, nil).Once()
		ds.EXPECT().Limit().Return(100 << 20).Once()

		w := request{method: "GET", path: "/api/v1/admin/datasets/usage", headers: cookie}.do(t, server(t, authSvc, ds))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"bytes": {"u-1": 2048}, "limit": 104857600}`, w.Body.String())
	})
}
