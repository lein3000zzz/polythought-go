package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/plugins"
	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

var adminP = auth.Principal{User: root, SessionID: "s-1"}

func TestPluginRoutes(t *testing.T) {
	installedAt := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

	weather := plugins.Info{
		InstalledPlugin: domain.InstalledPlugin{
			ID: "weather", Version: "1.0.0", Enabled: true, Everyone: false, Users: []domain.UserID{"u-1"},
			Caps: domain.PluginCaps{DataMiB: 2048},
			Versions: []domain.PluginVersion{
				{Version: "1.1.0", Manifest: json.RawMessage(`{"name":"Weather","permissions":["fetch:*","telegram"]}`), InstalledAt: installedAt},
				{
					Version: "1.0.0", Manifest: json.RawMessage(`{"name":"Weather","permissions":["fetch:*"]}`), Signer: "Alice",
					InstalledBy: "u-0", InstalledAt: installedAt, ApprovedAt: installedAt,
				},
			},
		},
		Status: &ports.PluginStatus{
			Version: "1.0.0", State: "crashed", Since: installedAt, Crashes: 5, LastError: "exit status 3", Log: []string{"boom"},
			Sandboxed: true, Memory: 40 << 20, MemoryCap: 256 << 20, Data: 1 << 20, DataCap: 2 << 30,
		},
		Restart: false,
		Data:    plugins.DataTaken{Files: 3 << 20, Stored: 512},
	}

	installed := plugins.Installed{
		File:           ports.PluginFile{ID: "weather", Version: "1.1.0", Name: "Weather", Permissions: []string{"fetch:*", "telegram"}, Warning: "It is not signed."},
		NewPermissions: []string{"telegram"}, Upgrade: true,
	}

	tests := []struct {
		name       string
		req        request
		setup      func(s *httpapimocks.MockPluginService)
		wantStatus int
		wantBody   []string
	}{
		{
			name: "listed with their versions, status, and what was found on disk",
			req:  request{method: "GET", path: "/api/v1/admin/plugins", headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().List(mock.Anything, adminP).Return([]plugins.Info{weather}, []ports.PluginFound{{ID: "copied", Version: "2.0.0"}}, nil)
				s.EXPECT().Sandbox(adminP).Return(ports.PluginSandbox{Why: "only Linux has the sandbox", Unsandboxed: true}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`"id":"weather","name":"Weather","version":"1.0.0","enabled":true,"everyone":false,"users":["u-1"],"caps":{"dataCap":2048,"memoryCap":0},"data":{"files":3145728,"stored":512}`,
				`"sandboxed":true,"memory":41943040,"memoryCap":268435456,"data":1048576,"dataCap":2147483648`,
				`"sandbox":{"available":false,"why":"only Linux has the sandbox","unsandboxed":true}`,
				`"version":"1.1.0","permissions":["fetch:*","telegram"]`, `"approved":false`,
				`"signer":"Alice"`, `"approved":true,"approvedAt":"2026-10-22T12:00:00Z"`,
				`"status":{"version":"1.0.0","state":"crashed"`, `"crashes":5,"lastError":"exit status 3","log":["boom"]`,
				`"found":[{"id":"copied","version":"2.0.0"}]`,
			},
		},
		{
			name: "installed from a URL, saying what is new to approve",
			req:  request{method: "POST", path: "/api/v1/admin/plugins/fetch", body: `{"url":"https://plugins.example/weather.tar.gz"}`, headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().InstallFrom(mock.Anything, adminP, "https://plugins.example/weather.tar.gz").Return(installed, nil)
			},
			wantStatus: http.StatusCreated,
			wantBody:   []string{`"newPermissions":["telegram"],"upgrade":true`, `"warning":"It is not signed."`},
		},
		{
			name: "a version twice",
			req:  request{method: "POST", path: "/api/v1/admin/plugins/fetch", body: `{"url":"https://plugins.example/weather.tar.gz"}`, headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().InstallFrom(mock.Anything, adminP, mock.Anything).Return(plugins.Installed{}, domain.ErrPluginInstalled)
			},
			wantStatus: http.StatusConflict,
			wantBody:   []string{`"code":"plugin_installed"`},
		},
		{
			name: "found on disk, adopted",
			req:  request{method: "POST", path: "/api/v1/admin/plugins/found/copied/2.0.0", headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().Adopt(mock.Anything, adminP, "copied", "2.0.0").Return(installed, nil)
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "approved, naming what it asks for",
			req:  request{method: "POST", path: "/api/v1/admin/plugins/weather/versions/1.1.0/approve", body: `{"permissions":["fetch:*","telegram"]}`, headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().Approve(mock.Anything, adminP, "weather", "1.1.0", []string{"fetch:*", "telegram"}).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "a version removed",
			req:  request{method: "DELETE", path: "/api/v1/admin/plugins/weather/versions/1.1.0", headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().RemoveVersion(mock.Anything, adminP, "weather", "1.1.0").Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "disabled, enabled",
			req:  request{method: "POST", path: "/api/v1/admin/plugins/weather/disable", headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().SetEnabled(mock.Anything, adminP, "weather", false).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "for chosen users",
			req:  request{method: "PUT", path: "/api/v1/admin/plugins/weather/users", body: `{"everyone":false,"users":["u-1"]}`, headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().SetUsers(mock.Anything, adminP, "weather", false, []domain.UserID{"u-1"}).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "started again, uninstalled",
			req:  request{method: "POST", path: "/api/v1/admin/plugins/weather/start", headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().Start(mock.Anything, adminP, "weather").Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "uninstalled with its data (D348)",
			req:  request{method: "DELETE", path: "/api/v1/admin/plugins/weather?data=true", headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().Uninstall(mock.Anything, adminP, "weather", true).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "uninstalled, its data left behind (D342)",
			req:  request{method: "DELETE", path: "/api/v1/admin/plugins/weather", headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().Uninstall(mock.Anything, adminP, "weather", false).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "its own caps (D346)",
			req:  request{method: "PUT", path: "/api/v1/admin/plugins/weather/caps", body: `{"dataCap":2048,"memoryCap":0}`, headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().SetCaps(mock.Anything, adminP, "weather", domain.PluginCaps{DataMiB: 2048}).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "caps out of bounds, each named",
			req:  request{method: "PUT", path: "/api/v1/admin/plugins/weather/caps", body: `{"dataCap":0,"memoryCap":8}`, headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().SetCaps(mock.Anything, adminP, "weather", domain.PluginCaps{MemoryMiB: 8}).Return(&domain.ValidationError{
					Base: domain.ErrInvalidInput, Problems: []domain.Problem{{Param: "memoryCap", Message: "A memory cap is 16 to 65536 MiB."}},
				})
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"param":"memoryCap"`},
		},
		{
			name: "data left behind",
			req:  request{method: "GET", path: "/api/v1/admin/plugins/left-behind", headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().LeftBehind(mock.Anything, adminP).Return([]plugins.LeftData{{ID: "news", Files: 4096, Stored: 12}}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`[{"id":"news","files":4096,"stored":12}]`},
		},
		{
			name: "data left behind, removed",
			req:  request{method: "DELETE", path: "/api/v1/admin/plugins/left-behind/news", headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().RemoveLeftBehind(mock.Anything, adminP, "news").Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "its own settings, a secret's value never shown (D329)",
			req:  request{method: "GET", path: "/api/v1/admin/plugins/weather/settings", headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().Settings(mock.Anything, adminP, "weather").Return([]plugins.Setting{
					{Spec: settings.Spec{Key: "units", Label: "Units", Kind: settings.KindEnum, Options: []string{"celsius", "kelvin"}, Default: "celsius"}, Value: "kelvin", IsSet: true},
					{Spec: settings.Spec{Key: "api_key", Label: "API key", Kind: settings.KindString, Secret: true, Default: ""}, IsSet: true},
				}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`{"key":"units","label":"Units","description":"","kind":"enum","options":["celsius","kelvin"],"secret":false,"isSet":true,"value":"kelvin","default":"celsius"}`,
				`{"key":"api_key","label":"API key","description":"","kind":"string","secret":true,"isSet":true,"value":null,"default":null}`,
			},
		},
		{
			name: "its settings changed, numbers as sent",
			req:  request{method: "PATCH", path: "/api/v1/admin/plugins/weather/settings", body: `{"values":{"days":7,"units":null}}`, headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().SetSettings(mock.Anything, adminP, "weather", map[string]json.RawMessage{
					"days": json.RawMessage(`7`), "units": json.RawMessage(`null`),
				}).Return(nil)
				s.EXPECT().Settings(mock.Anything, adminP, "weather").Return(nil, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`{"settings":[]}`},
		},
		{
			name: "its history",
			req:  request{method: "GET", path: "/api/v1/admin/plugins/weather/history", headers: cookie},
			setup: func(s *httpapimocks.MockPluginService) {
				s.EXPECT().History(mock.Anything, adminP, "weather").Return([]domain.PluginAudit{{Version: "1.0.0", Action: "approved", Detail: "fetch:*", By: "u-0", At: installedAt}}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`[{"version":"1.0.0","action":"approved","detail":"fetch:*","by":"u-0","at":"2026-10-22T12:00:00Z"}]`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authSvc := httpapimocks.NewMockAuthService(t)
			withSession(authSvc, root)

			svc := httpapimocks.NewMockPluginService(t)
			tt.setup(svc)

			srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc, Plugins: svc})
			require.NoError(t, err)

			w := tt.req.do(t, srv)
			assert.Equal(t, tt.wantStatus, w.Code, w.Body.String())

			for _, s := range tt.wantBody {
				assert.Contains(t, w.Body.String(), s)
			}
		})
	}

	t.Run("not there without plugins", func(t *testing.T) {
		authSvc := httpapimocks.NewMockAuthService(t)
		withSession(authSvc, root)

		srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc})
		require.NoError(t, err)

		w := request{method: "GET", path: "/api/v1/admin/plugins", headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

// TestBodyLimits runs the server on a port, as bodies only stream through Hertz's network
// layer: an archive's upload streams past the cap other requests are held to.
func TestBodyLimits(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := l.Addr().String()
	require.NoError(t, l.Close())

	authSvc := httpapimocks.NewMockAuthService(t)
	authSvc.EXPECT().AuthenticateSession(mock.Anything, "sess-token").Return(adminP, nil).Maybe()

	archive := bytes.Repeat([]byte("x"), 3<<20)

	// Not a mock: one keeps the body it was given, which the server recycles once the
	// request is answered.
	svc := &uploads{want: archive}

	srv, err := httpapi.New(httpapi.Config{Addr: addr, PublicURL: publicURL, MaxBodyBytes: 1 << 20}, observability.NoopUtility(),
		httpapi.Services{Auth: authSvc, Plugins: svc})
	require.NoError(t, err)

	go func() { _ = srv.Run() }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	base := "http://" + addr

	require.Eventually(t, func() bool {
		resp, err := http.Get(base + "/healthz") //nolint:noctx // a test's
		if err == nil {
			_ = resp.Body.Close()
		}

		return err == nil
	}, 5*time.Second, 20*time.Millisecond)

	post := func(path, contentType string, body []byte) (int, string) {
		req, err := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(body)) //nolint:noctx // a test's
		require.NoError(t, err)

		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Cookie", cookie["Cookie"])
		req.Header.Set("Origin", publicURL)

		req.Close = true // so shutting down waits for no idle connection

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		out, _ := io.ReadAll(resp.Body)

		return resp.StatusCode, string(out)
	}

	code, body := post("/api/v1/admin/plugins", "application/gzip", archive)
	assert.Equal(t, http.StatusCreated, code, "an archive three times the cap streams on: "+body)

	code, body = post("/api/v1/admin/plugins/fetch", "application/json", []byte(`{"url":"`+strings.Repeat("x", 2<<20)+`"}`))
	assert.Equal(t, http.StatusRequestEntityTooLarge, code, "any other body past the cap is refused")
	assert.Contains(t, body, `"code":"too_large"`)

	code, _ = post("/api/v1/auth/login", "application/json", bytes.Repeat([]byte(" "), 2<<20))
	assert.Equal(t, http.StatusRequestEntityTooLarge, code, "signed in or not")
}

// uploads takes an archive's upload, checking it came whole.
type uploads struct {
	httpapi.PluginService

	want []byte
}

func (u *uploads) Install(_ context.Context, _ auth.Principal, r io.Reader) (plugins.Installed, error) {
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, u.want) {
		return plugins.Installed{}, fmt.Errorf("%w: the archive came %d bytes, %v", domain.ErrInvalidInput, len(got), err)
	}

	return plugins.Installed{File: ports.PluginFile{ID: "weather", Version: "1.0.0"}}, nil
}
