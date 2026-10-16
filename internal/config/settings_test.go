package config_test

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/jsruntime"
	"github.com/lein3000zzz/polythought-go/internal/config"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
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
		config.KeyCodeMemory:          int(jsruntime.DefaultLimits.Memory >> 20),
		config.KeyCodeTime:            jsruntime.DefaultLimits.Time,
		config.KeyCodeOutput:          jsruntime.DefaultLimits.Output >> 10,
		config.KeyCodeHostCalls:       jsruntime.DefaultLimits.HostCalls,
		config.KeyCodeConcurrency:     0,
		config.KeyTrustedProxies:      []string{},
		config.KeyWebhookAllowPrivate: false,
		config.KeyLogLevel:            "info",
		config.KeyLogFormat:           "text",
		config.KeyTelegramEnabled:     false,
		config.KeyTelegramToken:       "",
		config.KeyTelegramMode:        "polling",
	} {
		assert.Equal(t, want, spec(t, key).Default, key)
	}

	assert.Len(t, registry(t).Specs(), 26, "every setting has a default checked above")
	assert.True(t, spec(t, config.KeyTelegramToken).Secret)
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
