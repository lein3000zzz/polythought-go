package config_test

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/jsruntime"
	"github.com/lein3000zzz/polythought-go/internal/config"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/customnodes"
	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

func registry(t *testing.T) *settings.Registry {
	t.Helper()

	r, err := config.Settings()
	require.NoError(t, err)

	return r
}

func spec(t *testing.T, key string) settings.Spec {
	t.Helper()

	s, ok := registry(t).Spec(key)
	require.True(t, ok, key)

	return s
}

func TestSettingsDefaults(t *testing.T) {
	t.Parallel()

	authCfg := auth.DefaultConfig()
	limits := workflows.DefaultLimits()

	// The services' own defaults, used by tests and tools, match what the server runs with.
	for key, want := range map[string]any{
		config.KeyRegistration:        string(authCfg.Registration),
		config.KeyVerifyEmail:         authCfg.VerifyEmail,
		config.KeyPasswordReset:       authCfg.PasswordReset,
		config.KeyInviteEmail:         authCfg.InviteEmail,
		config.KeySessionIdleTTL:      authCfg.SessionIdleTTL,
		config.KeySessionAbsoluteTTL:  authCfg.SessionAbsoluteTTL,
		config.KeyInviteTTL:           authCfg.InviteTTL,
		config.KeyMinPasswordLength:   authCfg.MinPasswordLength,
		config.KeyMaxWorkflows:        limits.Workflows,
		config.KeyMaxActiveWorkflows:  limits.ActiveWorkflows,
		config.KeyMaxNodes:            limits.NodesPerFlow,
		config.KeyScheduleMinInterval: triggers.DefaultMinInterval,
		config.KeyFeedMaxTokens:       100,
		config.KeyExecutionRetention:  30 * 24 * time.Hour,
		config.KeyMaxWaiting:          50,
		config.KeyMaxWait:             7 * 24 * time.Hour,
		config.KeyCodeAccess:          string(authCfg.CodeAccess),
		config.KeyCustomNodes:         customnodes.DefaultLimits().PerUser,
		config.KeyCodeMemory:          int(jsruntime.DefaultLimits.Memory >> 20),
		config.KeyCodeTime:            jsruntime.DefaultLimits.Time,
		config.KeyCodeOutput:          jsruntime.DefaultLimits.Output >> 10,
		config.KeyCodeHostCalls:       jsruntime.DefaultLimits.HostCalls,
		config.KeyCodeConcurrency:     0,
		config.KeyPaperBalance:        1000,
		config.KeyKeyImport:           config.KeyImportAuto,
		config.KeyLiveTrading:         false,
		config.KeyBuilderCode:         "",
		config.KeyMaxLiveOrder:        100,
		config.KeyMaxLiveDay:          1000,
		config.KeyBuilderAPIKey:       "",
		config.KeyBuilderSecret:       "",
		config.KeyBuilderPassphrase:   "",
		config.KeyPolygonRPC:          pm.DefaultRPCURL,
		config.KeyTrustedProxies:      []string{},
		config.KeyWebhookAllowPrivate: false,
		config.KeyLogLevel:            "info",
		config.KeyLogFormat:           "text",
		config.KeyTelegramEnabled:     false,
		config.KeyTelegramToken:       "",
		config.KeyTelegramMode:        "polling",
		config.KeyMailHost:            "",
		config.KeyMailPort:            587,
		config.KeyMailSecurity:        config.MailSTARTTLS,
		config.KeyMailUsername:        "",
		config.KeyMailPassword:        "",
		config.KeyMailFrom:            "",
		config.KeyMailFromName:        "Polythought",
		config.KeyMailNotifications:   false,
	} {
		assert.Equal(t, want, spec(t, key).Default, key)
	}

	assert.Len(t, registry(t).Specs(), 48, "every setting has a default checked above")

	for _, key := range []string{
		config.KeyTelegramToken, config.KeyBuilderAPIKey, config.KeyBuilderSecret, config.KeyBuilderPassphrase, config.KeyPolygonRPC,
		config.KeyMailPassword,
	} {
		assert.True(t, spec(t, key).Secret, key)
	}
}

func TestSettingsEnvironmentNames(t *testing.T) {
	t.Parallel()

	// Variables from before settings moved to the Config page keep working.
	for key, want := range map[string]string{
		config.KeyRegistration:        "POLYTHOUGHT_REGISTRATION",
		config.KeySessionIdleTTL:      "POLYTHOUGHT_SESSION_IDLE_TTL",
		config.KeySessionAbsoluteTTL:  "POLYTHOUGHT_SESSION_ABSOLUTE_TTL",
		config.KeyExecutionRetention:  "POLYTHOUGHT_EXECUTION_RETENTION",
		config.KeyTrustedProxies:      "POLYTHOUGHT_TRUSTED_PROXIES",
		config.KeyWebhookAllowPrivate: "POLYTHOUGHT_WEBHOOK_ALLOW_PRIVATE",
		config.KeyLogLevel:            "POLYTHOUGHT_LOG_LEVEL",
		config.KeyLogFormat:           "POLYTHOUGHT_LOG_FORMAT",
		config.KeyInviteTTL:           "POLYTHOUGHT_AUTH_INVITE_TTL",
		config.KeyTelegramToken:       "POLYTHOUGHT_TELEGRAM_TOKEN",
	} {
		assert.Equal(t, want, spec(t, key).EnvName(), key)
	}

	for _, s := range registry(t).Specs() {
		assert.NotEmpty(t, s.Group, s.Key)
		assert.NotEmpty(t, s.Description, s.Key)
	}
}

func TestSettingsValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key     string
		raw     string
		wantErr string
	}{
		{config.KeyTrustedProxies, "10.0.0.0/8, 10.0.0.1", `"10.0.0.1" is not a range like 10.0.0.0/8`},
		{config.KeyRegistration, "maybe", `"maybe" is not one of open, invite, closed`},
		{config.KeySessionIdleTTL, "7d", `"7d" is not a positive duration`},
		{config.KeySessionAbsoluteTTL, "1m", "must be at least 5m"},
		{config.KeyMinPasswordLength, "4", "must be between 8 and 128"},
		{config.KeyLogLevel, "loud", `"loud" is not one of debug, info, warn, error`},
		{config.KeyLogFormat, "xml", `"xml" is not one of text, json`},
		{config.KeyWebhookAllowPrivate, "sometimes", `"sometimes" is not true or false`},
		{config.KeyCodeTime, "10m", "must be between 100ms and 5m"},
		{config.KeyCodeMemory, "2048", "must be between 8 and 1024"},
		{config.KeyBuilderSecret, "not base64!", "is not base64"},
		{config.KeyPolygonRPC, "wss://polygon.example", "is not an http or https URL"},
		{config.KeyPolygonRPC, "polygon.drpc.org", "is not an http or https URL"},
		{config.KeyBuilderCode, "0xb1", "is not 0x and 64 hex digits"},
		{config.KeyBuilderCode, "0x" + strings.Repeat("g", 64), "is not 0x and 64 hex digits"},
		{config.KeyLiveTrading, "maybe", `"maybe" is not true or false`},
		{config.KeyMailHost, "smtp://mail.example.com", "without a scheme or port"},
		{config.KeyMailHost, "mail.example.com:587", "without a scheme or port"},
		{config.KeyMailPort, "70000", "must be between 1 and 65535"},
		{config.KeyMailSecurity, "ssl", `"ssl" is not one of starttls, tls, none`},
		{config.KeyMailFrom, "Polythought <p@example.com>", "is not an address like"},
	}

	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()

			_, err := spec(t, tc.key).Parse(tc.raw)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}

	v, err := spec(t, config.KeyTrustedProxies).Parse("10.0.0.0/8, fd00::/8")
	require.NoError(t, err)

	_, private, _ := net.ParseCIDR("10.0.0.0/8")
	_, v6, _ := net.ParseCIDR("fd00::/8")

	got, err := config.ParseCIDRs(v.([]string))
	require.NoError(t, err)
	assert.Equal(t, []*net.IPNet{private, v6}, got)

	assert.True(t, domain.RegistrationMode(spec(t, config.KeyRegistration).Default.(string)).Valid())

	_, err = spec(t, config.KeyBuilderCode).Parse("0x" + strings.Repeat("0b", 32))
	require.NoError(t, err, "a builder code")
}

func TestSettingsGroupsAreTogether(t *testing.T) {
	t.Parallel()

	// The Config page and the reference list a group where it first appears.
	done := map[string]bool{}
	current := ""

	for _, s := range registry(t).Specs() {
		if s.Group == current {
			continue
		}

		assert.False(t, done[s.Group], "%s: group %q is split; register its settings together", s.Key, s.Group)
		done[current], current = true, s.Group
	}
}

func TestTakesKeys(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		mode         string
		registration domain.RegistrationMode
		want         bool
	}{
		{config.KeyImportAuto, domain.RegistrationInvite, true},
		{config.KeyImportAuto, domain.RegistrationClosed, true},
		{config.KeyImportAuto, domain.RegistrationOpen, false},
		{config.KeyImportOn, domain.RegistrationOpen, true},
		{config.KeyImportOff, domain.RegistrationInvite, false},
	} {
		assert.Equal(t, tc.want, config.TakesKeys(tc.mode, tc.registration), "%s with %s registration (D21)", tc.mode, tc.registration)
	}
}

func TestVerifyEmailNeedsMail(t *testing.T) {
	t.Parallel()

	r := registry(t)
	require.Len(t, r.Rules(), 1)
	rule := r.Rules()[0]
	assert.Equal(t, config.KeyVerifyEmail, rule.Key)

	mail := map[string]any{config.KeyMailHost: "mail.example.com", config.KeyMailFrom: "p@example.com"}

	require.NoError(t, rule.Check(r.Snapshot(nil)), "off")
	require.NoError(t, rule.Check(r.Snapshot(mail)), "off, mail set up")

	on := map[string]any{config.KeyVerifyEmail: true}
	require.ErrorContains(t, rule.Check(r.Snapshot(on)), "needs mail set up first")

	on[config.KeyMailHost] = "mail.example.com"
	require.Error(t, rule.Check(r.Snapshot(on)), "no From address")

	on[config.KeyMailFrom] = "p@example.com"
	require.NoError(t, rule.Check(r.Snapshot(on)))
}
