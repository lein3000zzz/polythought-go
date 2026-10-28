package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
)

// Keys of the settings the server itself reads.
const (
	KeyRegistration        = "auth.registration"
	KeyVerifyEmail         = "auth.verify_email"
	KeyPasswordReset       = "auth.password_reset"
	KeySessionIdleTTL      = "auth.session_idle_ttl"
	KeySessionAbsoluteTTL  = "auth.session_absolute_ttl"
	KeyInviteTTL           = "auth.invite_ttl"
	KeyInviteEmail         = "auth.invite_email"
	KeyRequests            = "auth.requests"
	KeyRequestDeniedMail   = "auth.request_denied_mail"
	KeyMinPasswordLength   = "auth.min_password_length"
	KeyMaxWorkflows        = "workflows.max_per_user"
	KeyMaxActiveWorkflows  = "workflows.max_active_per_user"
	KeyMaxNodes            = "workflows.max_nodes"
	KeyScheduleMinInterval = "workflows.schedule_min_interval"
	KeyFeedMaxTokens       = "workflows.feed_max_tokens"
	KeyPnLSampleEvery      = "workflows.pnl_sample_every"
	KeyExecutionRetention  = "executions.retention"
	KeyMaxWaiting          = "executions.max_waiting_per_user"
	KeyMaxWait             = "executions.max_wait"
	KeyCodeAccess          = "code.access"
	KeyCustomNodes         = "code.custom_nodes_per_user"
	KeyCodeMemory          = "code.memory"
	KeyCodeTime            = "code.time"
	KeyCodeOutput          = "code.output"
	KeyCodeHostCalls       = "code.host_calls"
	KeyCodeConcurrency     = "code.concurrency"
	KeyCodeRunners         = "code.runners"
	KeyPluginsDir          = "plugins.dir"
	KeyPluginTrustedKeys   = "plugins.trusted_keys"
	KeyPluginMaxArchive    = "plugins.max_archive"
	KeyPluginFetchTime     = "plugins.fetch_time"
	KeyPluginFetchBody     = "plugins.fetch_body"
	KeyPluginStoreSize     = "plugins.store_size"
	KeyPluginUnsandboxed   = "plugins.unsandboxed"
	KeyPluginDataCap       = "plugins.data_cap"
	KeyPluginMemoryCap     = "plugins.memory_cap"
	KeyPaperBalance        = "trading.paper_balance"
	KeyKeyImport           = "trading.key_import"
	KeyBuilderAPIKey       = "trading.builder_api_key"
	KeyBuilderSecret       = "trading.builder_secret"
	KeyBuilderPassphrase   = "trading.builder_passphrase"
	KeyPolygonRPC          = "trading.polygon_rpc"
	KeyLiveTrading         = "trading.live"
	KeyBuilderCode         = "trading.builder_code"
	KeyMaxLiveOrder        = "trading.max_live_order"
	KeyMaxLiveDay          = "trading.max_live_day"

	// Agent spend caps a month, for every user unless an admin gives them their own
	// (D271): dollars to the cent, as providers report them, and tokens; 0 is no cap.
	KeyAgentDollars = "agents.max_dollars_month"
	KeyAgentInput   = "agents.max_input_month"
	KeyAgentOutput  = "agents.max_output_month"
	KeyAgentTotal   = "agents.max_total_month"

	// Agents over MCP (D398): agents such as Claude Code, signed in with OAuth.
	KeyMCPEnabled          = "mcp.enabled"
	KeyMCPRegistration     = "mcp.registration"
	KeyMCPMaxUnused        = "mcp.max_unused_clients"
	KeyMCPPrivateDocuments = "mcp.private_documents"
	KeyMCPSkipConsent      = "mcp.skip_consent"
	KeyMCPRate             = "mcp.rate"
	KeyMCPUserRate         = "mcp.user_rate"
	KeyMCPAccessTTL        = "mcp.access_ttl"
	KeyMCPRefreshTTL       = "mcp.refresh_ttl"
	KeyMCPConfirmFor       = "mcp.confirm_for"
	KeyMCPWindowWait       = "mcp.window_wait"
	KeyMCPAgentPasswords   = "mcp.agent_passwords"
	KeyMCPTaskWait         = "mcp.task_wait"
	KeyMCPTaskLease        = "mcp.task_lease"
	KeyMCPTaskSeen         = "mcp.task_seen"
	KeyMCPClients          = "mcp.clients"
	KeyMCPAddress          = "mcp.address"
	KeyMCPSessionIdle      = "mcp.session_idle"
	KeyMCPSessionsPerAgent = "mcp.sessions_per_agent"
	KeyMCPSessionsPerUser  = "mcp.sessions_per_user"
	KeyMCPNewSessions      = "mcp.new_sessions"

	KeyTrustedProxies      = "http.trusted_proxies"
	KeyWebhookAllowPrivate = "webhook.allow_private"
	KeyOutboundProxy       = "outbound.proxy"
	KeyGitHubStars         = "github.stars"
	KeyUpdateCheck         = "updates.check"
	KeyUpdatePrereleases   = "updates.prereleases"
	KeyUpdaterURL          = "updates.updater_url"
	KeyUpdaterToken        = "updates.updater_token"
	KeyLogLevel            = "log.level"
	KeyLogFormat           = "log.format"
	KeyTelegramEnabled     = "telegram.enabled"
	KeyTelegramToken       = "telegram.token"
	KeyTelegramMode        = "telegram.mode"
	KeyMailHost            = "mail.host"
	KeyMailPort            = "mail.port"
	KeyMailSecurity        = "mail.security"
	KeyMailUsername        = "mail.username"
	KeyMailPassword        = "mail.password"
	KeyMailFrom            = "mail.from_address"
	KeyMailFromName        = "mail.from_name"
	KeyMailNotifications   = "mail.notifications"
)

// How the server connects to the mail server (D169): STARTTLS, TLS from the start, or
// plain, for one on the same host or network. Certificates are always checked.
const (
	MailSTARTTLS = "starttls"
	MailTLS      = "tls"
	MailPlain    = "none"
)

// The ways the instance takes private keys (D21): auto is on unless registration is open.
const (
	KeyImportAuto = "auto"
	KeyImportOn   = "on"
	KeyImportOff  = "off"
)

// TakesKeys reports whether users may import private keys, by the Private keys and
// Registration settings.
func TakesKeys(mode string, registration domain.RegistrationMode) bool {
	switch mode {
	case KeyImportOn:
		return true
	case KeyImportOff:
		return false
	default:
		return registration != domain.RegistrationOpen
	}
}

const (
	groupAccounts   = "Accounts"
	groupWorkflows  = "Workflows"
	groupExecutions = "Executions"
	groupCode       = "Code"
	groupPlugins    = "Plugins"
	groupTrading    = "Trading"
	groupAgents     = "AI agents"
	groupMCP        = "Agents over MCP"
	groupNetwork    = "Network"
	groupUpdates    = "Updates"
	groupLogging    = "Logging"
	groupTelegram   = "Telegram"
	groupMail       = "Mail"
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
			Key: KeyVerifyEmail, Group: groupAccounts, Label: "Verify sign-ups",
			Description: "People who sign up themselves get a link by mail, and can sign in once they open it; an account unverified after 7 days is removed. Needs mail set up, under Mail. Accounts from before, and those admins create, count as verified.",
			Kind:        settings.KindBool, Default: false,
		},
		settings.Spec{
			Key: KeyPasswordReset, Group: groupAccounts, Label: "Password reset by email",
			Description: "Forgot password? on the sign-in page mails a link to set a new one, working once for an hour; setting it signs the account out everywhere. Works while mail is set up, under Mail.",
			Kind:        settings.KindBool, Default: true,
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
			Key: KeyInviteEmail, Group: groupAccounts, Label: "Invites by email",
			Description: "Admins can type an address on the Invites page, and the invite is mailed, naming them; only that address signs up with it, once, verified. Invites can still be shared as links. Works while mail is set up, under Mail.",
			Kind:        settings.KindBool, Default: true,
		},
		settings.Spec{
			Key: KeyRequests, Group: groupAccounts, Label: "Requests to join",
			Description: "While registration is by invite, the sign-up page lets people without one ask for it, with a note. " +
				"They confirm their address by a mailed link first; admins then approve on the Invites page, which mails an invite " +
				"there, or deny. Needs mail set up, under Mail.",
			Kind: settings.KindBool, Default: false,
		},
		settings.Spec{
			Key: KeyRequestDeniedMail, Group: groupAccounts, Label: "Tell denied requests",
			Description: "Mail someone whose request to join an admin denied, saying they may ask again in 30 days. Off, a denied request just closes.",
			Kind:        settings.KindBool, Default: true,
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
			Key: KeyPnLSampleEvery, Group: groupWorkflows, Label: "Highest and lowest P&L, checked every",
			Description: "How often each trading workflow's profit and loss is checked, to keep its highest and lowest for the dashboard. " +
				"A peak between checks is missed, and a workflow holding a position with no price is skipped until it has one. Applies from the next check.",
			Kind: settings.KindDuration, Default: 10 * time.Minute, Check: within(time.Minute, 24*time.Hour),
		},
		settings.Spec{
			Key: KeyExecutionRetention, Group: groupExecutions, Label: "Keep executions for",
			Description: "Finished executions older than this are deleted, at start and then daily.",
			Kind:        settings.KindDuration, Default: 30 * 24 * time.Hour, Check: atLeast(time.Hour),
			Env: prefix + "EXECUTION_RETENTION",
		},
		settings.Spec{
			Key: KeyMaxWaiting, Group: groupExecutions, Label: "Waiting executions per user",
			Description: "The most executions of one user that may wait at once, such as at a Wait node. Past it, a node that would wait fails.",
			Kind:        settings.KindInt, Default: 50, Check: between(1, 10_000),
		},
		settings.Spec{
			Key: KeyMaxWait, Group: groupExecutions, Label: "Longest wait",
			Description: "How far ahead a wait may end. A node asking to wait longer fails.",
			Kind:        settings.KindDuration, Default: 7 * 24 * time.Hour, Check: atLeast(time.Minute),
		},
		settings.Spec{
			Key: KeyCodeAccess, Group: groupCode, Label: "Who may write code",
			Description: "Code nodes run TypeScript users write: everyone may (everyone), admins and the users switched on for code on the Users page (chosen), or no one, admins included (off). Expressions are not code. A workflow with code nodes whose owner may not write code cannot be saved, and fails at once when it is triggered.",
			Kind:        settings.KindEnum, Default: string(domain.CodeEveryone),
			Options: []string{string(domain.CodeEveryone), string(domain.CodeChosen), string(domain.CodeOff)},
		},
		settings.Spec{
			Key: KeyCustomNodes, Group: groupCode, Label: "Custom nodes per user",
			Description: "The most custom nodes one user may define. Users above it keep theirs but cannot make more.",
			Kind:        settings.KindInt, Default: 50, Check: between(1, 1_000),
		},
		settings.Spec{
			Key: KeyCodeMemory, Group: groupCode, Label: "Memory per run (MiB)",
			Description: "The most memory one run of user code may use. Running out stops the run.",
			Kind:        settings.KindInt, Default: 64, Check: between(8, 1024),
		},
		settings.Spec{
			Key: KeyCodeTime, Group: groupCode, Label: "Time per run",
			Description: "How long one run of user code may take, waiting for the data it asks for included.",
			Kind:        settings.KindDuration, Default: 5 * time.Second, Check: within(100*time.Millisecond, 5*time.Minute),
		},
		settings.Spec{
			Key: KeyCodeOutput, Group: groupCode, Label: "Result per run (KiB)",
			Description: "The largest result one run of user code may return, measured as JSON.",
			Kind:        settings.KindInt, Default: 1024, Check: between(1, 16384),
		},
		settings.Spec{
			Key: KeyCodeHostCalls, Group: groupCode, Label: "Data requests per run",
			Description: "How many times one run of user code, or of a plugin's node, may ask Polythought for data or anything else, such as a market search or a fetch. A plugin's trigger may ask as often each minute.",
			Kind:        settings.KindInt, Default: 100, Check: between(1, 10_000),
		},
		settings.Spec{
			Key: KeyCodeConcurrency, Group: groupCode, Label: "Runs at once",
			Description: "How many runs of user code may go at the same time; the others wait their turn. 0 means one per CPU of the server.",
			Kind:        settings.KindInt, Default: 0, Check: between(0, 256),
			Apply: settings.ApplyRestart,
		},
		settings.Spec{
			Key: KeyCodeRunners, Group: groupCode, Label: "Runner processes",
			Description: "Whether user code runs in processes of its own, one for each run at once, rather than in the server's: for public instances. A runner reaches nothing but the server, and runs in the sandbox where the server has one, on Linux 6.2 or later. Each takes about 80 MiB, more while it runs. Off, code runs in the server's process, inside its engine and its limits all the same.",
			Kind:        settings.KindBool, Default: false, Apply: settings.ApplyRestart,
		},
		settings.Spec{
			Key: KeyPluginsDir, Group: groupPlugins, Label: "Plugins directory",
			Description: "Where installed plugins are kept: a directory the server may write, a volume in Docker. What admins decide about them is kept in the database.",
			Kind:        settings.KindString, Default: "plugins", Check: nonEmpty,
			Apply: settings.ApplyRestart, Env: prefix + "PLUGINS_DIR",
		},
		settings.Spec{
			Key: KeyPluginTrustedKeys, Group: groupPlugins, Label: "Trusted signing keys",
			Description: "minisign public keys whose signatures you trust, one a line: the key's line from its .pub file, after a name and a space if you like, such as \"Polythought RWQf6LRC…\". A plugin they signed shows who did; any other installs only after a warning.",
			Kind:        settings.KindList, Default: []string{}, Check: minisignKeys,
		},
		settings.Spec{
			Key: KeyPluginMaxArchive, Group: groupPlugins, Label: "Largest archive (MB)",
			Description: "The largest plugin archive an admin may install, uploaded or fetched.",
			Kind:        settings.KindInt, Default: 100, Check: between(1, 2048),
		},
		settings.Spec{
			Key: KeyPluginFetchTime, Group: groupPlugins, Label: "Plugin fetch time",
			Description: "How long a plugin's request to another site may take, from connecting to its answer's last byte.",
			Kind:        settings.KindDuration, Default: 10 * time.Second, Check: within(time.Second, 2*time.Minute),
		},
		settings.Spec{
			Key: KeyPluginFetchBody, Group: groupPlugins, Label: "Plugin fetch size (KiB)",
			Description: "The largest body a plugin's request to another site may send, and its answer bring back.",
			Kind:        settings.KindInt, Default: 1024, Check: between(1, 65536),
		},
		settings.Spec{
			Key: KeyPluginStoreSize, Group: groupPlugins, Label: "Plugin store per node (KiB)",
			Description: "How much one plugin node may keep in its store, its keys and values, in each workflow. Writes past it are refused; what is kept already stays.",
			Kind:        settings.KindInt, Default: 1024, Check: between(1, 65536),
		},
		settings.Spec{
			Key: KeyPluginUnsandboxed, Group: groupPlugins, Label: "Unsandboxed plugins",
			Description: "Whether plugins start without the sandbox where the server has none: not on Linux, or on a kernel older than 6.2. Unsandboxed, a plugin reaches whatever the server can, its files, its network and its other processes, so allow it only for plugins you would run yourself. Off, they do not start there. Where the server has a sandbox, plugins always run in it. From each plugin's next start.",
			Kind:        settings.KindBool, Default: false,
		},
		settings.Spec{
			Key: KeyPluginDataCap, Group: groupPlugins, Label: "Plugin data cap (MiB)",
			Description: "How much disk one plugin may take, its own directory and its temporary one together. Past it, the plugin is stopped as crashed and the admins told. Its card on the Plugins page may set its own.",
			Kind:        settings.KindInt, Default: 1024, Check: between(1, domain.MaxPluginDataMiB),
		},
		settings.Spec{
			Key: KeyPluginMemoryCap, Group: groupPlugins, Label: "Plugin memory cap (MiB)",
			Description: "How much memory one plugin's process may take. Past it, the process is killed, which counts as a crash, and started again; a plugin written in Go also gets a soft limit a little under it, from its next start. Watched on Linux. Its card on the Plugins page may set its own.",
			Kind:        settings.KindInt, Default: 256, Check: between(domain.MinPluginMemoryMiB, domain.MaxPluginMemoryMiB),
		},
		settings.Spec{
			Key: KeyPaperBalance, Group: groupTrading, Label: "Paper balance ($)",
			Description: "What a workflow's paper account starts with, in dollars: its paper buys spend it and its sells add to it. Accounts already open keep theirs.",
			Kind:        settings.KindInt, Default: 1000, Check: between(1, 10_000_000),
		},
		settings.Spec{
			Key: KeyLiveTrading, Group: groupTrading, Label: "Live trading",
			Description: "Whether workflows may trade live: real orders on Polymarket from their owner's wallet, spending its pUSD. Off, every workflow trades on paper and live orders are refused. On, an owner switches a workflow to live in its Trading view, confirming it. Turning it off cancels every live order resting on the book, every user's; workflows switched to live stay so, but place no orders until it is on again.",
			Kind:        settings.KindBool, Default: false,
		},
		settings.Spec{
			Key: KeyMaxLiveOrder, Group: groupTrading, Label: "Max $ a live order",
			Description: "The most one live buy may spend, in whole dollars, for every user: one over it is refused before it is sent. 0 for no cap. An admin can give a user a cap of their own on the Users page.",
			Kind:        settings.KindInt, Default: 100, Check: between(0, 10_000_000),
		},
		settings.Spec{
			Key: KeyMaxLiveDay, Group: groupTrading, Label: "Max $ a day of live buys",
			Description: "The most a user's live buys may spend and hold in 24 hours, in whole dollars, all their workflows together: what they spent, and what their resting buys may still spend. A buy that would go over is refused before it is sent. 0 for no cap. An admin can give a user a cap of their own on the Users page.",
			Kind:        settings.KindInt, Default: 1000, Check: between(0, 10_000_000),
		},
		settings.Spec{
			Key: KeyBuilderCode, Group: groupTrading, Label: "Builder code",
			Description: "Your Polymarket builder code, 0x and 64 hex digits, from polymarket.com/settings?tab=builder: live orders then carry it, crediting your builder account with their volume, and pay the builder fee you set there on top of Polymarket's. Empty, orders carry none and pay no builder fee.",
			Kind:        settings.KindString, Default: "", Check: builderCode,
		},
		settings.Spec{
			Key: KeyKeyImport, Group: groupTrading, Label: "Private keys",
			Description: "Whether users may import a wallet's private key, so it can trade: on, off, or auto, on unless anyone may sign up (Registration open). The server keeps imported keys encrypted, and could trade with them. Keys imported before stay when it is switched off; new ones are refused.",
			Kind:        settings.KindEnum, Default: KeyImportAuto,
			Options: []string{KeyImportAuto, KeyImportOn, KeyImportOff},
		},
		settings.Spec{
			Key: KeyBuilderAPIKey, Group: groupTrading, Label: "Builder API key",
			Description: "Your Polymarket Builder API key, with its secret and passphrase below, from polymarket.com/settings?tab=builder. Users of Deposit Wallets can authorise session keys once all three are in: trade-only keys that need no private key here. Polymarket must enable the key for session keys first (builder@polymarket.com).",
			Kind:        settings.KindString, Secret: true, Default: "",
		},
		settings.Spec{
			Key: KeyBuilderSecret, Group: groupTrading, Label: "Builder secret",
			Description: "The secret of the Builder API key, base64.",
			Kind:        settings.KindString, Secret: true, Default: "", Check: base64Secret,
		},
		settings.Spec{
			Key: KeyBuilderPassphrase, Group: groupTrading, Label: "Builder passphrase",
			Description: "The passphrase of the Builder API key.",
			Kind:        settings.KindString, Secret: true, Default: "",
		},
		settings.Spec{
			Key: KeyPolygonRPC, Group: groupTrading, Label: "Polygon RPC",
			Description: "The Polygon JSON-RPC endpoint the server reads approvals from before it grants any, and estimates proxy wallets' gas with: https://polygon.drpc.org, the one Polymarket's client uses, unless you set another, such as your own node or a provider's URL. Kept secret, as such URLs often hold a key.",
			Kind:        settings.KindString, Secret: true, Default: DefaultPolygonRPC, Check: rpcURL,
		},
		settings.Spec{
			Key: KeyAgentDollars, Group: groupAgents, Label: "Max $ a month of agent spend",
			Description: "The most a user's AI agent nodes may spend in a calendar month, UTC, in dollars to the cent, all their workflows together: counted as providers report what they charged, which OpenRouter does and the others do not, so their calls count against the token caps only. Polythought keeps no prices. Once reached, the next call is refused. 0 for no cap. An admin can give a user caps of their own on the Users page.",
			Kind:        settings.KindString, Default: "20.00", Check: dollars,
		},
		settings.Spec{
			Key: KeyAgentInput, Group: groupAgents, Label: "Max input tokens a month",
			Description: "The most input tokens a user's AI agent nodes may take in a calendar month, UTC, all their workflows together: what they read, prompts and tool results. A call that could go over is refused before it is made. 0 for no cap.",
			Kind:        settings.KindInt, Default: 0, Check: between(0, maxTokensMonth),
		},
		settings.Spec{
			Key: KeyAgentOutput, Group: groupAgents, Label: "Max output tokens a month",
			Description: "The most output tokens a user's AI agent nodes may take in a calendar month, UTC: what the models write, thinking included. A call that could go over, its Max tokens with it, is refused before it is made. 0 for no cap.",
			Kind:        settings.KindInt, Default: 0, Check: between(0, maxTokensMonth),
		},
		settings.Spec{
			Key: KeyAgentTotal, Group: groupAgents, Label: "Max tokens a month",
			Description: "The most tokens a user's AI agent nodes may take in a calendar month, UTC, input and output together. A call that could go over is refused before it is made. 0 for no cap.",
			Kind:        settings.KindInt, Default: 20_000_000, Check: between(0, maxTokensMonth),
		},
		settings.Spec{
			Key: KeyMCPEnabled, Group: groupMCP, Label: "Agents over MCP",
			Description: "Let users connect agents such as Claude Code and Codex: an agent signs in through its user's browser, " +
				"where they allow it scopes, then works at /mcp. Off, /mcp and agents' sign-in are not there, and agents " +
				"already connected stop until it is on again; their grants stay, to revoke on Settings.",
			Kind: settings.KindBool, Default: true,
		},
		settings.Spec{
			Key: KeyMCPRegistration, Group: groupMCP, Label: "Agents register themselves",
			Description: "Let an agent with no metadata document of its own register itself, naming itself, as older agents " +
				"do; the consent page marks it unverified. Off, only agents whose metadata document is at an https address " +
				"can sign in. Agents registered before keep working.",
			Kind: settings.KindBool, Default: true,
		},
		settings.Spec{
			Key: KeyMCPMaxUnused, Group: groupMCP, Label: "Agents registered, not yet allowed",
			Description: "How many agents that registered themselves may wait at once with no user allowing them. Past " +
				"it, registering is refused until the daily prune removes those a day old. Applies at once.",
			Kind: settings.KindInt, Default: 1000, Check: between(10, 1_000_000),
		},
		settings.Spec{
			Key: KeyMCPPrivateDocuments, Group: groupMCP, Label: "Documents at private addresses",
			Description: "Fetch agents' metadata documents from addresses inside the server's network too, for an agent " +
				"published there. Leave off on a shared instance: any signed-in user could have the server fetch one. " +
				"Agents on a private network can register themselves instead.",
			Kind: settings.KindBool, Default: false,
		},
		settings.Spec{
			Key: KeyMCPSkipConsent, Group: groupMCP, Label: "Let allowed agents through",
			Description: "An agent asking for no more than its user allowed it before gets in without the consent page " +
				"asking again, as when it starts on another of their machines, where it comes back to its own site. One " +
				"coming back to the user's machine, as command-line agents do, takes a click, its scopes ticked as " +
				"allowed: anything there could be listening. Off, the page asks every time.",
			Kind: settings.KindBool, Default: true,
		},
		settings.Spec{
			Key: KeyMCPRate, Group: groupMCP, Label: "Tool calls a minute",
			Description: "How many tool calls one agent, or one API token, may make in a minute, all at once if they " +
				"come so; past it, a call is refused until the minute allows another. A change applies at once.",
			Kind: settings.KindInt, Default: 120, Check: between(1, 100_000),
		},
		settings.Spec{
			Key: KeyMCPUserRate, Group: groupMCP, Label: "Tool calls a minute per user",
			Description: "How many tool calls all of a user's agents and API tokens may make in a minute together, " +
				"however many of them there are; past it, a call is refused until the minute allows another. " +
				"A change applies at once.",
			Kind: settings.KindInt, Default: 600, Check: between(1, 1_000_000),
		},
		settings.Spec{
			Key: KeyMCPAccessTTL, Group: groupMCP, Label: "Access token lifetime",
			Description: "How long an agent's access token works before it refreshes it. Revoking an agent stops it at once " +
				"whatever this is. Applies to tokens given from now.",
			Kind: settings.KindDuration, Default: time.Hour, Check: within(5*time.Minute, 24*time.Hour),
		},
		settings.Spec{
			Key: KeyMCPRefreshTTL, Group: groupMCP, Label: "Refresh token lifetime",
			Description: "How long an agent may go unused before its user signs it in again: each refresh gives it a new " +
				"refresh token, lasting this long. Applies to tokens given from now.",
			Kind: settings.KindDuration, Default: 30 * 24 * time.Hour, Check: within(time.Hour, 365*24*time.Hour),
		},
		settings.Spec{
			Key: KeyMCPConfirmFor, Group: groupMCP, Label: "Live actions wait",
			Description: "How long an agent's or an API token's live action waits for its user to confirm it, where the " +
				"user has it wait, before it expires unconfirmed. Applies to those asked from now.",
			Kind: settings.KindDuration, Default: time.Hour, Check: within(5*time.Minute, 7*24*time.Hour),
		},
		settings.Spec{
			Key: KeyMCPWindowWait, Group: groupMCP, Label: "Agent window waits",
			Description: "How long an agent's tool call waits for the user's answer in the agent's own window, where the " +
				"user has it ask there, before it says the action waits in Polythought. Some clients give up on a call sooner. " +
				"Applies at once.",
			Kind: settings.KindDuration, Default: 2 * time.Minute, Check: within(10*time.Second, 30*time.Minute),
		},
		settings.Spec{
			Key: KeyMCPAgentPasswords, Group: groupMCP, Label: "Agents create accounts with passwords",
			Description: "Let an admin's agents and API tokens create accounts with a password they give, as the Users page " +
				"does. Off, they invite people instead, by mail where mail is set up, else with a link; a password then " +
				"never passes through an agent. Applies at once.",
			Kind: settings.KindBool, Default: false,
		},
		settings.Spec{
			Key: KeyMCPTaskWait, Group: groupMCP, Label: "Agents wait for tasks",
			Description: "How long an agent's call waiting for a task from a local agent node is held open before it says " +
				"none came, and the agent calls again. Keep it under what agents' clients wait for a call: Codex gives up " +
				"after a minute unless told otherwise. Applies at once.",
			Kind: settings.KindDuration, Default: 50 * time.Second, Check: within(5*time.Second, 30*time.Minute),
		},
		settings.Spec{
			Key: KeyMCPTaskLease, Group: groupMCP, Label: "A task is an agent's for",
			Description: "How long a task stays with the agent it was handed to, while it neither answers it nor waits for " +
				"another, before it goes back to the user's other agents. Applies at once.",
			Kind: settings.KindDuration, Default: 10 * time.Minute, Check: within(time.Minute, 24*time.Hour),
		},
		settings.Spec{
			Key: KeyMCPTaskSeen, Group: groupMCP, Label: "Agents count as around for",
			Description: "How long after an agent last waited for a task it still counts as there, so a local agent node " +
				"hands it one rather than failing at once, as when it is between two calls. Applies at once.",
			Kind: settings.KindDuration, Default: time.Minute, Check: within(5*time.Second, time.Hour),
		},
		settings.Spec{
			Key: KeyMCPClients, Group: groupMCP, Label: "Clients with steps",
			Description: "The agents' clients Connected agents gives steps for, a tab each, in this order: claude-code, codex, " +
				"gemini-cli, cursor, opencode, vscode, claude-ai (Claude Desktop and claude.ai, which connect from " +
				"Anthropic's cloud) and other (any client, or an API token). Applies at once.",
			Kind: settings.KindList, Default: slices.Clone(MCPClients), Check: mcpClients,
		},
		settings.Spec{
			Key: KeyMCPAddress, Group: groupMCP, Label: "Address agents connect at",
			Description: "The address Connected agents gives agents, such as https://polythought.example.com/mcp. Empty " +
				"for the server's public URL followed by /mcp; set it where agents reach the server by another name. " +
				"Applies at once.",
			Kind: settings.KindString, Default: "", Check: mcpAddress,
		},
		settings.Spec{
			Key: KeyMCPSessionIdle, Group: groupMCP, Label: "Agents' sessions last",
			Description: "How long an agent's MCP session lasts with no call before the server closes it, and it no " +
				"longer shows as connected; its client opens another when it calls again.",
			Kind: settings.KindDuration, Default: time.Hour, Check: within(5*time.Minute, 24*time.Hour), Apply: settings.ApplyRestart,
		},
		settings.Spec{
			Key: KeyMCPSessionsPerAgent, Group: groupMCP, Label: "Sessions per agent",
			Description: "How many MCP sessions one agent, or one API token, keeps open at once. Past it, its oldest " +
				"closes, and that client opens another when it calls again. Applies at once.",
			Kind: settings.KindInt, Default: 10, Check: between(1, 1000),
		},
		settings.Spec{
			Key: KeyMCPSessionsPerUser, Group: groupMCP, Label: "Sessions per user",
			Description: "How many MCP sessions all of a user's agents and API tokens keep open at once. Past it, " +
				"their oldest closes. Each lasts until it goes idle and costs the server memory. Applies at once.",
			Kind: settings.KindInt, Default: 30, Check: between(1, 10_000),
		},
		settings.Spec{
			Key: KeyMCPNewSessions, Group: groupMCP, Label: "New sessions a minute",
			Description: "How many MCP sessions a user's agents and API tokens may open in a minute, all of them " +
				"together; past it, opening one is refused until the minute allows another. Applies at once.",
			Kind: settings.KindInt, Default: 30, Check: between(1, 10_000),
		},
		settings.Spec{
			Key: KeyTrustedProxies, Group: groupNetwork, Label: "Trusted proxies",
			Description: "Address ranges (CIDR, e.g. 10.0.0.0/8) of reverse proxies allowed to report the client's address in X-Forwarded-For. Leave empty without a proxy: anyone could then pick their own address and dodge rate limits.",
			Kind:        settings.KindList, Default: []string{}, Check: cidrs,
			Env: prefix + "TRUSTED_PROXIES",
		},
		settings.Spec{
			Key: KeyWebhookAllowPrivate, Group: groupNetwork, Label: "Private addresses",
			Description: "Let workflows reach addresses inside the server's network, such as a service on the same machine: " +
				"webhooks, and language models served there through an OpenAI-compatible key, in plain http too (never to a " +
				"public address). Leave off on a shared instance.",
			Kind: settings.KindBool, Default: false,
			Env: prefix + "WEBHOOK_ALLOW_PRIVATE",
		},
		settings.Spec{
			Key: KeyOutboundProxy, Group: groupNetwork, Label: "Outbound proxy",
			Description: "A proxy for requests to the internet, where the server cannot reach some sites directly. It " +
				"carries webhooks, AI agent nodes' model calls, plugins' fetches, and the server's fetches of plugin archives " +
				"and agents' documents. http://host:port or socks5://host:port, with user:password@ before the host if it asks. " +
				"Each address is checked first, and the proxy asked for a tunnel to that address alone, so it reaches " +
				"nothing inside the network for them; an address inside, where Private addresses allows it, goes direct. " +
				"Polymarket, Telegram and GitHub follow HTTPS_PROXY in the server's environment instead. Empty for none.",
			Kind: settings.KindString, Secret: true, Default: "", Check: outboundProxy,
			Env: prefix + "OUTBOUND_PROXY",
		},
		settings.Spec{
			Key: KeyGitHubStars, Group: groupNetwork, Label: "GitHub stars",
			Description: "Ask GitHub about once an hour how many stars Polythought's repository has, to show beside its link " +
				"in the app and the docs. Off, the server never calls GitHub, and the link shows no count.",
			Kind: settings.KindBool, Default: true,
			Env: prefix + "GITHUB_STARS",
		},
		settings.Spec{
			Key: KeyUpdateCheck, Group: groupUpdates, Label: "Check for updates",
			Description: "Ask GitHub once a day for Polythought's newest release, to show on the Config page beside " +
				"the server's version, and to tell admins once when a newer one is out. Off, the server never asks " +
				"GitHub about releases.",
			Kind: settings.KindBool, Default: true,
			Env: prefix + "UPDATE_CHECK",
		},
		settings.Spec{
			Key: KeyUpdatePrereleases, Group: groupUpdates, Label: "Pre-releases",
			Description: "Count pre-releases, such as 0.2.0-beta.1, as newer versions too, to update to before " +
				"they are stable. Off, only stable releases.",
			Kind: settings.KindBool, Default: false,
			Env: prefix + "UPDATE_PRERELEASES",
		},
		settings.Spec{
			Key: KeyUpdaterURL, Group: groupUpdates, Label: "Updater address",
			Description: "Where the server reaches Polythought's updater, which updates it when an admin asks on " +
				"the Config page. The Compose file's updater is at http://updater:8080.",
			Kind: settings.KindString, Default: "http://updater:8080", Check: updaterURL,
			AtBrowser: true, // where the updater token goes
			Env:       prefix + "UPDATER_URL",
		},
		settings.Spec{
			Key: KeyUpdaterToken, Group: groupUpdates, Label: "Updater token",
			Description: "The token the updater takes, the same as its own POLYTHOUGHT_UPDATER_TOKEN: at least 32 " +
				"characters. Empty, no updater is set up, and the Config page shows the commands to update by hand.",
			Kind: settings.KindString, Secret: true, Default: "", Check: updaterToken,
			Env: prefix + "UPDATER_TOKEN",
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
		settings.Spec{
			Key: KeyTelegramEnabled, Group: groupTelegram, Label: "Telegram bot",
			Description: "Run the instance's Telegram bot, so members can link Telegram from Settings. Changes restart the bot, not the server.",
			Kind:        settings.KindBool, Default: false,
		},
		settings.Spec{
			Key: KeyTelegramToken, Group: groupTelegram, Label: "Bot token",
			Description: "The token @BotFather gave you when you made the bot, like 123456:ABC-DEF…",
			Kind:        settings.KindString, Secret: true, Default: "", Check: botToken,
		},
		settings.Spec{
			Key: KeyTelegramMode, Group: groupTelegram, Label: "How the bot gets messages",
			Description: "polling asks Telegram for new messages, and works anywhere, even behind a firewall. webhook has Telegram send them to this server, and needs the public URL to be https.",
			Kind:        settings.KindEnum, Default: "polling", Options: []string{"polling", "webhook"},
		},
		settings.Spec{
			Key: KeyMailHost, Group: groupMail, Label: "Mail server",
			Description: "The SMTP server Polythought sends mail through, like mail.example.com, with no port: that is below. Mail is set up once this and the From address are in.",
			Kind:        settings.KindString, Default: "", Check: mailHost,
			AtBrowser: true, // where the mail password goes, and every reset link (A7)
		},
		settings.Spec{
			Key: KeyMailPort, Group: groupMail, Label: "Port",
			Description: "587 for STARTTLS, 465 for TLS from the start, 25 for a plain connection.",
			Kind:        settings.KindInt, Default: 587, Check: between(1, 65535),
			AtBrowser: true,
		},
		settings.Spec{
			Key: KeyMailSecurity, Group: groupMail, Label: "Security",
			Description: "starttls upgrades the connection to TLS, tls uses TLS from the start, none stays plain, for a server on the same host or network. The server's certificate is always checked; a password is never sent over a plain connection, except to this host.",
			Kind:        settings.KindEnum, Default: MailSTARTTLS, Options: []string{MailSTARTTLS, MailTLS, MailPlain},
			AtBrowser: true,
		},
		settings.Spec{
			Key: KeyMailUsername, Group: groupMail, Label: "Username",
			Description: "The account Polythought signs in to the mail server with. Leave empty for a server that takes mail without signing in.",
			Kind:        settings.KindString, Default: "",
			AtBrowser: true,
		},
		settings.Spec{
			Key: KeyMailPassword, Group: groupMail, Label: "Password",
			Description: "The password of that account.",
			Kind:        settings.KindString, Secret: true, Default: "",
		},
		settings.Spec{
			Key: KeyMailFrom, Group: groupMail, Label: "From address",
			Description: "The address Polythought's mail comes from, like polythought@example.com. Your mail server must let the account send as it.",
			Kind:        settings.KindString, Default: "", Check: emailAddress,
		},
		settings.Spec{
			Key: KeyMailFromName, Group: groupMail, Label: "From name",
			Description: "The name mail comes from, shown beside the address.",
			Kind:        settings.KindString, Default: "Polythought",
		},
		settings.Spec{
			Key: KeyMailNotifications, Group: groupMail, Label: "Notifications by email",
			Description: "Users may get their notifications by email too: each chooses per kind in Settings, by email off until they turn it on. Works while mail is set up.",
			Kind:        settings.KindBool, Default: false,
		},
	)
	if err != nil {
		return nil, err
	}

	// Verifying sign-ups needs mail, so a missing server cannot block them unseen (D173).
	// So do requests to join: their confirmation and their invite go by mail (D394).
	r.AddRule(KeyRequests, func(s *settings.Snapshot) error {
		if s.Bool(KeyRequests) && !MailSetUp(s) {
			return errors.New("needs mail set up first: a mail server and a From address, under Mail")
		}

		return nil
	})

	r.AddRule(KeyVerifyEmail, func(s *settings.Snapshot) error {
		if s.Bool(KeyVerifyEmail) && !MailSetUp(s) {
			return errors.New("needs mail set up first: a mail server and a From address, under Mail")
		}

		return nil
	})

	return r, nil
}

// MailSetUp says whether the settings name a mail server and a From address.
func MailSetUp(s *settings.Snapshot) bool {
	return s.String(KeyMailHost) != "" && s.String(KeyMailFrom) != ""
}

func atLeast(least time.Duration) func(any) error {
	return func(v any) error {
		if v.(time.Duration) < least {
			return fmt.Errorf("must be at least %s", settings.FormatDuration(least))
		}

		return nil
	}
}

// MCPClients are the agents' clients Connected agents can give steps for (D414), in their
// order there.
var MCPClients = []string{"claude-code", "codex", "gemini-cli", "cursor", "opencode", "vscode", "claude-ai", "other"}

func mcpClients(v any) error {
	for _, c := range v.([]string) {
		if !slices.Contains(MCPClients, c) {
			return fmt.Errorf("%q is no client with steps: %s", c, strings.Join(MCPClients, ", "))
		}
	}

	return nil
}

// plainHost and plainPath are what an MCP address may have: the app puts it in commands
// users copy and run, so nothing a shell or a config file reads as more (A8).
var (
	plainHost = regexp.MustCompile(`^[A-Za-z0-9.:-]+$`)
	plainPath = regexp.MustCompile(`^[A-Za-z0-9/._~-]*$`)
)

func mcpAddress(v any) error {
	addr := v.(string)
	if addr == "" {
		return nil
	}

	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.String() != addr ||
		!plainHost.MatchString(u.Hostname()) || !plainPath.MatchString(u.Path) {
		return errors.New("an http or https address with a plain path, like https://polythought.example.com/mcp: " +
			"no query, fragment, sign-in or spaces")
	}

	return nil
}

func within(least, most time.Duration) func(any) error {
	return func(v any) error {
		if d := v.(time.Duration); d < least || d > most {
			return fmt.Errorf("must be between %s and %s", settings.FormatDuration(least), settings.FormatDuration(most))
		}

		return nil
	}
}

// maxTokensMonth bounds a token cap a month: a trillion.
const maxTokensMonth = 1_000_000_000_000

// maxDollarsMonth bounds the dollar cap a month.
var maxDollarsMonth = decimal.NewFromInt(10_000_000)

// dollars checks a dollar cap: 0 or more, to the cent.
func dollars(v any) error {
	d, err := decimal.NewFromString(strings.TrimSpace(v.(string)))

	switch {
	case err != nil:
		return errors.New("must be dollars, like 20 or 12.50")
	case d.IsNegative() || d.GreaterThan(maxDollarsMonth):
		return fmt.Errorf("must be between 0 and %s", maxDollarsMonth)
	case !d.Equal(d.Truncate(2)):
		return errors.New("must be to the cent at most, like 12.50")
	}

	return nil
}

func between(least, most int) func(any) error {
	return func(v any) error {
		if n := v.(int); n < least || n > most {
			return fmt.Errorf("must be between %d and %d", least, most)
		}

		return nil
	}
}

// mailHost checks a mail server's host: a name or an address, without a scheme or port.
func mailHost(v any) error {
	h := v.(string)
	if h == "" {
		return nil
	}

	if strings.ContainsAny(h, ":/ @") {
		return errors.New("is a host alone, like mail.example.com, without a scheme or port")
	}

	return nil
}

// emailAddress checks an address alone, like polythought@example.com.
func emailAddress(v any) error {
	a := v.(string)
	if a == "" {
		return nil
	}

	if addr, err := mail.ParseAddress(a); err != nil || addr.Address != a {
		return errors.New("is not an address like polythought@example.com")
	}

	return nil
}

// DefaultPolygonRPC is the public endpoint @polymarket/client uses.
const DefaultPolygonRPC = "https://polygon.drpc.org"

// rpcURL checks an RPC endpoint: an http or https URL with a host.
func rpcURL(v any) error {
	u, err := url.Parse(v.(string))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("is not an http or https URL")
	}

	return nil
}

// updaterURL checks the updater's address: http or https, with a host, and no sign-in,
// query or fragment, as the paths it serves come after it.
func updaterURL(v any) error {
	u, err := url.Parse(v.(string))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("is not an http or https address, like http://updater:8080")
	}

	return nil
}

// outboundProxy checks the outbound proxy's address (D428): empty, or http:// or
// socks5:// with a host and a port, a user and a password before them, nothing after.
func outboundProxy(v any) error {
	s := v.(string)
	if s == "" {
		return nil
	}

	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "socks5") || u.Hostname() == "" || u.Port() == "" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("is not a proxy's address: http://host:port or socks5://host:port, a user:password@ before the host if it asks")
	}

	return nil
}

// MinUpdaterToken is the shortest token the updater takes (D425).
const MinUpdaterToken = 32

// updaterToken checks the updater's token: empty, or at least MinUpdaterToken printable
// characters with no spaces, as it goes in a header.
func updaterToken(v any) error {
	s := v.(string)
	if s == "" {
		return nil
	}

	if len(s) < MinUpdaterToken || strings.ContainsFunc(s, func(r rune) bool { return r <= ' ' || r > '~' }) {
		return fmt.Errorf("must be at least %d characters, with no spaces, such as from openssl rand -hex 32", MinUpdaterToken)
	}

	return nil
}

// builderCode checks a builder code: empty, or 32 bytes of hex after 0x.
// botToken checks a Telegram bot token: digits, a colon, then letters, digits, _ and -.
// Anything else would not work, and could end up in an error that quotes it (R11).
func botToken(v any) error {
	s := v.(string)
	if s == "" {
		return nil
	}

	id, secret, ok := strings.Cut(s, ":")
	if !ok || id == "" || secret == "" || strings.Trim(id, "0123456789") != "" ||
		strings.TrimFunc(secret, func(r rune) bool {
			return r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-')
		}) != "" {
		return errors.New("is not a bot token: digits, a colon, then letters, digits, _ and -")
	}

	return nil
}

func builderCode(v any) error {
	s := v.(string)
	if s == "" {
		return nil
	}

	if len(s) != 66 || !strings.HasPrefix(s, "0x") {
		return errors.New("is not 0x and 64 hex digits")
	}

	if _, err := hex.DecodeString(s[2:]); err != nil {
		return errors.New("is not 0x and 64 hex digits")
	}

	return nil
}

// base64Secret checks an API secret as Polymarket issues them: base64url, padded or not.
func base64Secret(v any) error {
	s := v.(string)
	if s == "" {
		return nil
	}

	if _, err := base64.URLEncoding.DecodeString(s); err == nil {
		return nil
	}

	if _, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return nil
	}

	return errors.New("is not base64")
}

func nonEmpty(v any) error {
	if strings.TrimSpace(v.(string)) == "" {
		return errors.New("it cannot be empty")
	}

	return nil
}

// minisignKeys checks each entry is a minisign public key's line, a name before it if
// given: base64 of "Ed", an 8-byte ID and a 32-byte key.
func minisignKeys(v any) error {
	for _, e := range v.([]string) {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}

		line := e
		if i := strings.LastIndexByte(e, ' '); i > 0 {
			line = e[i+1:]
		}

		raw, err := base64.StdEncoding.DecodeString(line)
		if err != nil || len(raw) != 42 || string(raw[:2]) != "Ed" {
			return fmt.Errorf("%q is not a minisign public key: give its line from the .pub file, like RWQf6LRC…", e)
		}
	}

	return nil
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
