package config

import (
	"fmt"
	"net"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
)

// Keys of the settings the server itself reads.
const (
	KeyRegistration        = "auth.registration"
	KeySessionIdleTTL      = "auth.session_idle_ttl"
	KeySessionAbsoluteTTL  = "auth.session_absolute_ttl"
	KeyInviteTTL           = "auth.invite_ttl"
	KeyMinPasswordLength   = "auth.min_password_length"
	KeyMaxWorkflows        = "workflows.max_per_user"
	KeyMaxActiveWorkflows  = "workflows.max_active_per_user"
	KeyMaxNodes            = "workflows.max_nodes"
	KeyScheduleMinInterval = "workflows.schedule_min_interval"
	KeyFeedMaxTokens       = "workflows.feed_max_tokens"
	KeyExecutionRetention  = "executions.retention"
	KeyTrustedProxies      = "http.trusted_proxies"
	KeyWebhookAllowPrivate = "webhook.allow_private"
	KeyLogLevel            = "log.level"
	KeyLogFormat           = "log.format"
)

const (
	groupAccounts   = "Accounts"
	groupWorkflows  = "Workflows"
	groupExecutions = "Executions"
	groupNetwork    = "Network"
	groupLogging    = "Logging"
)

// Settings is the registry of the server's own settings.
func Settings() (*settings.Registry, error) {
	r := settings.NewRegistry()

	err := r.Register(
		settings.Spec{
			Key: KeyRegistration, Group: groupAccounts, Label: "Registration",
			Description: "Who may create an account: anyone (open), people an admin invited (invite), or only admins, on the Users page (closed).",
			Kind:        settings.KindEnum, Default: string(domain.RegistrationInvite),
			Options: []string{string(domain.RegistrationOpen), string(domain.RegistrationInvite), string(domain.RegistrationClosed)},
			Env:     prefix + "REGISTRATION",
		},
		settings.Spec{
			Key: KeySessionIdleTTL, Group: groupAccounts, Label: "Idle sign-out",
			Description: "A browser session unused this long signs out. Applies to new activity at once.",
			Kind:        settings.KindDuration, Default: 7 * 24 * time.Hour, Check: atLeast(5 * time.Minute),
			Env: prefix + "SESSION_IDLE_TTL",
		},
		settings.Spec{
			Key: KeySessionAbsoluteTTL, Group: groupAccounts, Label: "Longest session",
			Description: "A browser session signs out after this long however active it is. Applies to sessions started from now.",
			Kind:        settings.KindDuration, Default: 30 * 24 * time.Hour, Check: atLeast(5 * time.Minute),
			Env: prefix + "SESSION_ABSOLUTE_TTL",
		},
		settings.Spec{
			Key: KeyInviteTTL, Group: groupAccounts, Label: "Invite lifetime",
			Description: "How long an invite works. Applies to invites created from now.",
			Kind:        settings.KindDuration, Default: 7 * 24 * time.Hour, Check: atLeast(time.Hour),
		},
		settings.Spec{
			Key: KeyMinPasswordLength, Group: groupAccounts, Label: "Shortest password",
			Description: "The fewest characters a new password may have. Existing passwords keep working.",
			Kind:        settings.KindInt, Default: 12, Check: between(8, 128),
		},
		settings.Spec{
			Key: KeyMaxWorkflows, Group: groupWorkflows, Label: "Workflows per user",
			Description: "The most workflows one user may have. Users above it keep theirs but cannot create more.",
			Kind:        settings.KindInt, Default: 100, Check: between(1, 100_000),
		},
		settings.Spec{
			Key: KeyMaxActiveWorkflows, Group: groupWorkflows, Label: "Active workflows per user",
			Description: "The most workflows one user may have running. Lowering it stops none; it applies when one is turned on.",
			Kind:        settings.KindInt, Default: 20, Check: between(1, 10_000),
		},
		settings.Spec{
			Key: KeyMaxNodes, Group: groupWorkflows, Label: "Nodes per workflow",
			Description: "The most nodes a workflow may have. Applies when a workflow is saved.",
			Kind:        settings.KindInt, Default: 200, Check: between(2, 10_000),
		},
		settings.Spec{
			Key: KeyScheduleMinInterval, Group: groupWorkflows, Label: "Fastest schedule",
			Description: "The shortest interval a schedule or market trigger may use, to spare the APIs workflows call.",
			Kind:        settings.KindDuration, Default: time.Second, Check: atLeast(100 * time.Millisecond),
			Apply: settings.ApplyRestart,
		},
		settings.Spec{
			Key: KeyFeedMaxTokens, Group: groupWorkflows, Label: "Tokens per market trigger",
			Description: "The most outcome tokens one market trigger may watch.",
			Kind:        settings.KindInt, Default: 100, Check: between(1, 1_000),
			Apply: settings.ApplyRestart,
		},
		settings.Spec{
			Key: KeyExecutionRetention, Group: groupExecutions, Label: "Keep executions for",
			Description: "Finished executions older than this are deleted, at start and then daily.",
			Kind:        settings.KindDuration, Default: 30 * 24 * time.Hour, Check: atLeast(time.Hour),
			Env: prefix + "EXECUTION_RETENTION",
		},
		settings.Spec{
			Key: KeyTrustedProxies, Group: groupNetwork, Label: "Trusted proxies",
			Description: "Address ranges (CIDR, e.g. 10.0.0.0/8) of reverse proxies allowed to report the client's address in X-Forwarded-For. Leave empty without a proxy: anyone could then pick their own address and dodge rate limits.",
			Kind:        settings.KindList, Default: []string{}, Check: cidrs,
			Env: prefix + "TRUSTED_PROXIES",
		},
		settings.Spec{
			Key: KeyWebhookAllowPrivate, Group: groupNetwork, Label: "Webhooks to private addresses",
			Description: "Let workflows post to addresses inside the server's network, such as a service on the same machine. Leave off on a shared instance.",
			Kind:        settings.KindBool, Default: false,
			Env: prefix + "WEBHOOK_ALLOW_PRIVATE",
		},
		settings.Spec{
			Key: KeyLogLevel, Group: groupLogging, Label: "Log level",
			Description: "The least severe messages the server logs.",
			Kind:        settings.KindEnum, Default: "info", Options: []string{"debug", "info", "warn", "error"},
			Env: prefix + "LOG_LEVEL",
		},
		settings.Spec{
			Key: KeyLogFormat, Group: groupLogging, Label: "Log format",
			Description: "Plain text for people, JSON for log collectors.",
			Kind:        settings.KindEnum, Default: "text", Options: []string{"text", "json"},
			Apply: settings.ApplyRestart,
			Env:   prefix + "LOG_FORMAT",
		},
	)
	if err != nil {
		return nil, err
	}

	return r, nil
}

func atLeast(least time.Duration) func(any) error {
	return func(v any) error {
		if v.(time.Duration) < least {
			return fmt.Errorf("must be at least %s", settings.FormatDuration(least))
		}

		return nil
	}
}

func between(least, most int) func(any) error {
	return func(v any) error {
		if n := v.(int); n < least || n > most {
			return fmt.Errorf("must be between %d and %d", least, most)
		}

		return nil
	}
}

func cidrs(v any) error {
	_, err := ParseCIDRs(v.([]string))
	return err
}

// ParseCIDRs parses address ranges like 10.0.0.0/8.
func ParseCIDRs(list []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(list))

	for _, raw := range list {
		_, cidr, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("%q is not a range like 10.0.0.0/8", raw)
		}

		out = append(out, cidr)
	}

	return out, nil
}
