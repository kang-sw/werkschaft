package wsagent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// TestCommonCodeHasNoHarnessNameBranch is the durable residual gate of the
// harness-name umbrella (261118-refactor-ws-harness-name-branches, Phase 4).
// Common code reads registration data (BackendRegistration, its policy blocks)
// and narrow Backend interfaces; it never branches on which harness it serves.
//
// The scan covers the non-test Go files of the packages the umbrella moved:
// internal/wsagent, internal/mcp, internal/wsdashboard, and cmd/ws-mcp. It
// parses each file and fails on
//
//   - any use of a constant whose value is a harness name: BackendClaude,
//     BackendCodex, BackendAgy, BackendPi, BackendWerk, PiTierHarness, wslive.AgentKindPi,
//     and any constant declared that way later (harnessAliases finds them from
//     their declarations);
//   - any short string literal (no whitespace, or at most
//     shortNameLiteral bytes) that names a harness (claude, codex, agy, pi, werk,
//     antigravity) as a word or inside a token such as a config key, tool
//     name, environment variable, path, or provider label;
//   - any alternation of harness names inside a literal (a pattern); and
//   - any literal naming a harness, however long, that is a decision operand: a
//     switch case, an ==/!= operand, a strings comparison argument, or a map key.
//
// outside the adapter-owned files and outside harnessNameAllowlist, which this
// test carries. Long prose (descriptions, diagnostics, error text) is not a
// name branch and is not gated. The allowlist is exact: each entry names a
// file, the literal or identifier, how many times it occurs, its class, the
// persisted or external boundary that registration data cannot interpret, and
// the fixture that pins it. A new site, a changed count, a stale entry, a
// missing fixture, or a persisted or external site whose own top-level
// declaration carries no site comment fails the test. Work that adds a
// harness difference must route it through the registration seam
// (backend_policy.go) or extend the allowlist in its own diff under the
// (1881) rule: only old persisted bytes or external identifiers qualify.

// shortNameLiteral is the longest literal with whitespace that is still gated:
// it covers provider labels ("via Codex CLI") but not prose.
const shortNameLiteral = 24

// Allowlist classes.
const (
	// classDeclarative is a registration row: a built-in profile or tool name,
	// or the name constants the registrations are keyed by.
	classDeclarative = "declarative"
	// classPersisted is a name that old persisted bytes carry: a record, holder,
	// config, history, or session-layout member written by an earlier version.
	classPersisted = "persisted"
	// classExternal is an identifier owned outside ws: a harness's environment
	// variable, executable, credential path, wire marker, MCP key, or CLI
	// contract.
	classExternal = "external"
)

// siteComment is the marker the top-level declaration of every persisted or
// external allowlist site carries in a comment (its doc comment or one inside).
const siteComment = "Retained name check"

// siteCommentRe also accepts the older wording of the marker (Phases 2-3),
// which may wrap across comment lines.
var siteCommentRe = regexp.MustCompile(`(?i)retained name check|\(?1881\)?\s+(//\s*)?survivor|survivor \(1881`)

type nameSite struct {
	file     string // relative to the agents-plugin-tool root
	token    string // the unquoted literal, or the identifier
	count    int
	class    string
	boundary string
	fixtures []string // test functions that pin the retained behavior
}

func site(file, token string, count int, class, boundary string, fixtures ...string) nameSite {
	return nameSite{file, token, count, class, boundary, fixtures}
}

// harnessNameAllowlist is the exact (1881) allowlist.
var harnessNameAllowlist = func() []nameSite {
	var out []nameSite
	add := func(s ...nameSite) { out = append(out, s...) }

	const (
		profileRows = "TestBuiltinBackendRegistrationDefaults"
		leadRows    = "TestBuiltinLeadProfilesResolve"
		piTools     = "TestRetiredPiProfileRemainsReadable"
		piChild     = "TestCapabilityTokenGoesToSpawnedWorkersOnly"
		compactKeys = "TestCompactionConfigGetTuneAndLegacy"
		detect      = "TestDetectHarnessFromRawKeepsExternalMarkers"
		history     = "TestPlanUsageHistoryValidatorsKeepPersistedNames"
		credentials = "TestFSLocalDeniedProviderCredentials"
	)

	// Pi retirement compatibility paths.
	add(
		site("internal/mcp/server_harness.go", "ws-pi-bridge", 1, classExternal, "legacy exact MCP client name", "TestDetectHarnessFromInitializeParamsPrecedence"),
		site("internal/mcp/server_harness.go", "BackendPi", 1, classExternal, "legacy exact MCP client identity", "TestDetectHarnessFromInitializeParamsPrecedence"),
		site("internal/wsagent/agent_tools.go", "BackendPi", 2, classPersisted, "stored tier routing can still name Pi", "TestRetiredPiTierRedirectsToWerk"),
		site("internal/wsagent/agent_tools.go", "BackendWerk", 1, classPersisted, "the effective backend for a stored Pi tier", "TestRetiredPiTierRedirectsToWerk"),
		site("internal/mcp/config_manifest.go", "dashboard.pi_spawn_enabled", 1, classPersisted, "retired manifest keys from old packages", "TestRetiredPiManifestKeysAreIgnored"),
		site("internal/mcp/config_manifest.go", "pi.", 1, classPersisted, "retired manifest keys from old packages", "TestRetiredPiManifestKeysAreIgnored"),
		site("internal/wsagent/children.go", "BackendPi", 3, classPersisted, "legacy Pi history and wire tool names", "TestRetiredPiListUsesCallerOwnership"),
		site("internal/wsagent/children.go", "pi.list", 1, classPersisted, "legacy Pi history and wire tool names", "TestRetiredPiListUsesCallerOwnership"),
		site("internal/wsagent/children.go", "pi.transcript", 2, classPersisted, "legacy Pi history and wire tool names", "TestRetiredPiListUsesCallerOwnership"),
		site("internal/wsagent/handoff.go", "BackendPi", 3, classPersisted, "retained Pi records and handoff requests", "TestRetiredPiHoldersKeepRunning"),
		site("internal/wsagent/permission.go", "pi.list", 1, classPersisted, "legacy tool names in stored profiles", "TestRetiredPiProfileRemainsReadable"),
		site("internal/wsagent/permission.go", "pi.send", 1, classPersisted, "legacy tool names in stored profiles", "TestRetiredPiProfileRemainsReadable"),
		site("internal/wsagent/permission.go", "pi.spawn", 1, classPersisted, "legacy tool names in stored profiles", "TestRetiredPiProfileRemainsReadable"),
		site("internal/wsagent/permission.go", "pi.stop", 1, classPersisted, "legacy tool names in stored profiles", "TestRetiredPiProfileRemainsReadable"),
		site("internal/wsagent/permission.go", "pi.transcript", 1, classPersisted, "legacy tool names in stored profiles", "TestRetiredPiProfileRemainsReadable"),
		site("internal/wsagent/permission.go", "pi.wait", 1, classPersisted, "legacy tool names in stored profiles", "TestRetiredPiProfileRemainsReadable"),
		site("internal/wsagent/retired_pi_tiers.go", "BackendPi", 3, classPersisted, "stored Pi tier alias to werk and direct pi.<tier> refusal", "TestRetiredPiTierRedirectsToWerk"),
		site("internal/wsagent/retired_pi_tiers.go", "BackendWerk", 4, classPersisted, "stored Pi tier alias to werk", "TestRetiredPiTierRedirectsToWerk"),
		site("internal/wsagent/runner_create.go", "BackendPi", 2, classPersisted, "legacy profile and record backend", "TestRetiredPiProfileRemainsReadable"),
		site("internal/wsagent/runner_startup.go", "BackendPi", 2, classPersisted, "retained holder descriptors", "TestRetiredPiRealHolderSurvivesStartup"),
		site("internal/wsagent/runner_state.go", "BackendPi", 1, classPersisted, "ended state of retained Pi records", "TestRetiredPiHoldersKeepRunning"),
		site("internal/wsagent/support.go", "BackendPi", 2, classPersisted, "old support selection and response notice", "TestRetiredPiSupportSelectionFallsBackWithoutRewriting"),
		site("internal/wsagent/support.go", "json:\"retired_pi,omitempty\"", 1, classPersisted, "old support selection and response notice", "TestRetiredPiSupportSelectionFallsBackWithoutRewriting"),
		site("internal/wsdashboard/pageview.go", "pi", 1, classPersisted, "persisted snapshot worker kind and Pi harness", "TestRetiredPiAgentRowEndsAndLinks"),
	)

	// internal/wsagent
	add(
		site("internal/wsagent/backend.go", "claude", 1, classExternal,
			"the Windows shim remedy keys on the program the shim wraps (claude.exe), an executable name, not a backend",
			"TestHarnessCommandRefusalNamesARemedy"),
		site("internal/wsagent/profile.go", "BackendClaude", 1, classDeclarative, "the registration name constants", profileRows),
		site("internal/wsagent/profile.go", "BackendCodex", 1, classDeclarative, "the registration name constants", profileRows),
		site("internal/wsagent/profile.go", "BackendAgy", 1, classDeclarative, "the registration name constants", profileRows),
		site("internal/wsagent/profile.go", "BackendPi", 3, classDeclarative, "the registration name constants", profileRows),
		site("internal/wsagent/profile.go", "claude", 1, classDeclarative, "the registration name constants", profileRows),
		site("internal/wsagent/profile.go", "codex", 1, classDeclarative, "the registration name constants", profileRows),
		site("internal/wsagent/profile.go", "agy", 1, classDeclarative, "the registration name constants", profileRows),
		site("internal/wsagent/profile.go", "pi", 1, classDeclarative, "the registration name constants", profileRows),
		site("internal/wsagent/builtin.go", "tracker-support-claude", 1, classDeclarative, "built-in profile row name", "TestBuiltinSupportProfilesResolve"),
		site("internal/wsagent/builtin.go", "tracker-support-pi", 1, classDeclarative, "built-in profile row name", "TestBuiltinSupportProfilesResolve"),
		site("internal/wsagent/builtin.go", "search-answer-claude", 1, classDeclarative, "built-in profile row name", "TestBuiltinSupportProfilesResolve"),
		site("internal/wsagent/builtin.go", "search-answer-pi", 1, classDeclarative, "built-in profile row name", "TestBuiltinSupportProfilesResolve"),
		site("internal/wsagent/builtin.go", "lead-claude", 1, classDeclarative, "built-in profile row name", leadRows),
		site("internal/wsagent/builtin.go", "lead-pi", 1, classDeclarative, "built-in profile row name", leadRows),
		site("internal/wsagent/builtin.go", "lead-agy", 1, classDeclarative, "built-in profile row name", leadRows),
		site("internal/wsagent/builtin.go", "lead-codex", 1, classDeclarative, "built-in profile row name", leadRows),
		site("internal/wsagent/builtin.go", "agent-worker-agy", 1, classDeclarative, "built-in profile row name", "TestBuiltinAgentWorkerProfiles"),
		site("internal/wsagent/builtin.go", "agent-worker-codex", 1, classDeclarative, "built-in profile row name", "TestBuiltinAgentWorkerProfiles"),
		site("internal/wsagent/catalog.go", "claude.compaction_advisory_percent", 1, classPersisted, "legacy config key a user's config may still hold", compactKeys),
		site("internal/wsagent/catalog.go", "claude.compaction_advisory_enabled", 1, classPersisted, "config key a user's config may hold", compactKeys),
		site("internal/wsagent/children.go", "WS_MCP_PI_TOKEN", 1, classExternal, "environment variable the bundled Pi extension reads", piChild),
		site("internal/wsagent/children.go", "WS_MCP_PI_ENDPOINT", 1, classExternal, "environment variable the bundled Pi extension reads", piChild),
		site("internal/wsagent/record.go", "AgentKindPi", 1, classPersisted, "a Pi child row's agent kind, the persisted marker of a row the daemon's runner owns", "TestLiveWriterEndAgentsLeavesPiChildRows"),
		site("internal/wsagent/spawn_live.go", "AgentKindPi", 1, classPersisted, "the agent kind stamped on a Pi child row in snapshots", "TestAgentSpawnHostedWorkerRows"),
		site("internal/wsagent/holder.go", "pi-worker", 1, classPersisted, "holder kind an older daemon wrote into a still-running holder's descriptor", "TestWorkerHolderOwnership"),
		site("internal/wsagent/hosted_plugins.go", ".claude-plugin/plugin.json", 1, classExternal, "the shipped package manifest path, a build-owned file layout", "TestHostedPluginEnvironmentUsesNativeTemplatePaths"),
		site("internal/wsagent/hosted_plugins.go", "${CLAUDE_PLUGIN_ROOT}", 2, classExternal, "the root placeholder shipped manifests carry", "TestHostedPluginEnvironmentUsesNativeTemplatePaths"),
		site("internal/wsagent/record.go", `json:"agy_bypass,omitempty"`, 1, classPersisted, "Record.AgyBypass, a persisted record member older daemons wrote", "TestAgyBypassScopeAndLegacyRecordRewrite"),
		site("internal/wsagent/registration.go", "BackendPi", 1, classPersisted, "legacy Pi permission values accepted on reads and ignored at launch", "TestPermissionModesLegacyFallback"),
		site("internal/wsagent/spawn_live.go", "pi:", 4, classPersisted, "the live agent id prefix of a Pi child row in snapshots written by older daemons", "TestAgentSpawnHostedWorkerRows"),
	)

	// internal/mcp
	add(
		site("internal/mcp/compaction.go", "claude.compaction_advisory_enabled", 1, classPersisted, "config keys a user's config may hold", compactKeys),
		site("internal/mcp/compaction.go", "claude.compaction_advisory_percent", 1, classPersisted, "config keys a user's config may hold", compactKeys),
		site("internal/mcp/compaction.go", "pi.compaction_advisory_percent", 1, classPersisted, "config keys a user's config may hold", compactKeys),
		site("internal/mcp/compaction.go", "pi.compaction_hard_percent", 1, classPersisted, "config keys a user's config may hold", compactKeys),
		site("internal/mcp/dashboard_settings_config.go", "claude.status_band_enabled", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardAdapterReadLimitsKeepHarnessKeyScopes"),
		site("internal/mcp/dashboard_settings_config.go", "agy.model_wait_timeout_seconds", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardAdapterReadLimitsKeepHarnessKeyScopes"),
		site("internal/mcp/dashboard_settings_config.go", "agy.model_wait_retries", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardAdapterReadLimitsKeepHarnessKeyScopes"),
		site("internal/mcp/dashboard_settings_config.go", "agy.stall_log_check", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardAdapterReadLimitsKeepHarnessKeyScopes"),
		site("internal/mcp/dashboard_settings_config.go", "agy.http2_frame_trace", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardAdapterReadLimitsKeepHarnessKeyScopes"),
		site("internal/mcp/dashboard_settings_config.go", "agy.prefer_native_subagent", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardAdapterReadLimitsKeepHarnessKeyScopes"),
		site("internal/mcp/dashboard_settings_config.go", "agy.avoid_agy_subagent", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardAdapterReadLimitsKeepHarnessKeyScopes"),
		site("internal/mcp/dashboard_settings_config.go", "claude.", 1, classPersisted, "the harness-bucketed config key prefix the settings page groups by", "TestDashboardAdapterReadLimitsKeepHarnessKeyScopes"),
		site("internal/mcp/server_dispatch.go", "pi.list", 1, classExternal, "hidden legacy Pi history read tool name", "TestRetiredPiListUsesCallerOwnership"),
		site("internal/mcp/server_dispatch.go", "pi.transcript", 1, classExternal, "hidden legacy Pi history read tool name", "TestRetiredPiTranscriptUsesCallerOwnership"),
		site("internal/mcp/server_harness.go", "x-codex-turn-metadata", 2, classExternal, "Codex's own request metadata key and its raw marker", detect),
		site("internal/mcp/server_harness.go", "codex", 3, classExternal, "historical raw initialize substring and its precedence over claude", detect),
		site("internal/mcp/server_harness.go", "claude", 2, classExternal, "historical raw initialize substring", detect),
		site("internal/mcp/server_inventory.go", "antigravity/alwaysLoad", 1, classExternal, "the _meta key Antigravity's client reads to always load a tool", "TestHostedAgyToolsAlwaysLoad"),
	)

	// internal/wsdashboard
	add(
		site("internal/wsdashboard/fs_denied.go", "CODEX_HOME", 1, classExternal, "the provider's credential directory variable", credentials),
		site("internal/wsdashboard/fs_denied.go", "CLAUDE_CONFIG_DIR", 1, classExternal, "the provider's credential directory variable", credentials),
		site("internal/wsdashboard/fs_denied.go", "PI_CODING_AGENT_DIR", 1, classExternal, "the provider's credential directory variable", credentials),
		site("internal/wsdashboard/fs_outside.go", ".codex/auth.json", 1, classExternal, "the provider's credential file", credentials),
		site("internal/wsdashboard/fs_outside.go", ".claude/.credentials.json", 1, classExternal, "the provider's credential file", credentials),
		site("internal/wsdashboard/fs_outside.go", ".pi/agent/auth.json", 1, classExternal, "the provider's credential file", credentials),
		site("internal/wsdashboard/conversation_presentation.go", `^\[(?:ws|wsflow) note: from a peer lead session \((?:(?:claude|codex|pi), )?not a worker\), with the same authority as yours\]$`, 1, classPersisted,
			"stored peer-lead mail text written by earlier versions, which labeled only claude, codex, and pi", "TestConversationPresentationPeerNoteIsDisplayOnly"),
		site("internal/wsdashboard/pageview.go", "AgentKindPi", 2, classPersisted, "a finished Pi child row is told apart by its persisted agent kind", "TestRetiredPiRowOpensAsEndedHistory"),
		site("internal/wsdashboard/registry.go", "pi", 1, classPersisted, "the Pi record backend and session file location protected from retirement deletion", "TestPruneRegistrationsAgesRetiredLeadsByTheirRow"),
		site("internal/wsdashboard/plan_usage.go", `json:"claude"`, 2, classPersisted, "the plan-usage file's claude member", history),
		site("internal/wsdashboard/plan_usage.go", `json:"werk,omitempty"`, 1, classPersisted, "the plan-usage response member for Werk usage projections", "TestPlanUsageCodexOpenAIExactWorkspaceMatchUnifiesAndDeduplicatesProbe"),
		site("internal/wsdashboard/plan_usage.go", "claude", 5, classPersisted, "persisted plan-usage entry key and history harness label", history),
		site("internal/wsdashboard/plan_usage.go", "codex", 1, classPersisted, "persisted history harness label", history),
		site("internal/wsdashboard/plan_usage.go", "Claude push", 2, classPersisted, "persisted history source label", history),
		site("internal/wsdashboard/plan_usage.go", "CLAUDE_CONFIG_DIR", 1, classExternal, "the provider's credential directory variable", "TestPlanUsageCodexHomeSelection"),
		site("internal/wsdashboard/plan_usage.go", ".claude", 1, classExternal, "the provider's credential directory", "TestPlanUsageCodexHomeSelection"),
		site("internal/wsdashboard/plan_usage.go", "CODEX_HOME", 1, classExternal, "the provider's credential directory variable", "TestPlanUsageCodexHomeSelection"),
		site("internal/wsdashboard/plan_usage.go", ".codex", 1, classExternal, "the provider's credential directory", "TestPlanUsageCodexHomeSelection"),
		site("internal/wsdashboard/plan_usage.go", `json:"claudeAiOauth"`, 1, classExternal, "the provider's credential file member", "TestPlanUsageCodexCredentials"),
		site("internal/wsdashboard/plan_usage.go", "codexFiveHourHistory", 1, classPersisted, "the plan-usage response member the page reads", "TestPlanUsageCodexFiveHourT0CompatibilityAndSeparateFile"),
		site("internal/wsdashboard/fs_model_write_protection.go", "WerkJournalDirName", 1, classPersisted, "the durable session journal directory", "TestModelFileProtectionRefusesWerkJournalAlias"),
		site("internal/wsdashboard/plan_usage_history.go", "plan-usage-codex-history.json", 1, classPersisted, "the persisted five-hour history file name", "TestPlanUsageCodexFiveHourT0CompatibilityAndSeparateFile"),
		site("internal/wsdashboard/plan_usage_history.go", "codex", 3, classPersisted, "persisted history validators and unified codex_openai history projection accept the labels earlier writers wrote", history, "TestPlanUsageCodexOpenAIExactWorkspaceMatchUnifiesAndDeduplicatesProbe"),
		site("internal/wsdashboard/plan_usage_history.go", "claude", 2, classPersisted, "persisted history validators accept the labels earlier writers wrote", history),
		site("internal/wsdashboard/plan_usage_history.go", "via Codex CLI", 2, classPersisted, "persisted history source label", history),
		site("internal/wsdashboard/plan_usage_history.go", "via Pi", 2, classPersisted, "persisted history source label", history),
		site("internal/wsdashboard/plan_usage_history.go", "Claude push", 1, classPersisted, "persisted history source label", history),
		site("internal/wsdashboard/plan_usage_openai.go", "via Werk OpenAI", 1, classPersisted, "wire source label for Werk OpenAI usage entries", "TestPlanUsageSameEmailDifferentWorkspacesStaySeparate"),
		site("internal/wsdashboard/plan_usage_openai.go", "codex_openai", 1, classPersisted, "wire harness identifier for unified Codex/OpenAI usage entries", "TestPlanUsageCodexOpenAIExactWorkspaceMatchUnifiesAndDeduplicatesProbe"),
		site("internal/wsdashboard/plan_usage_openai.go", "werk_openai", 1, classPersisted, "wire harness identifier for separate Werk OpenAI usage entries", "TestPlanUsageSameEmailDifferentWorkspacesStaySeparate"),
		site("internal/wsdashboard/plan_usage_openai.go", "Codex / OpenAI", 1, classPersisted, "wire display label for unified Codex/OpenAI usage entries", "TestPlanUsageCodexOpenAIExactWorkspaceMatchUnifiesAndDeduplicatesProbe"),
		site("internal/wsdashboard/plan_usage_probe.go", "via Codex CLI", 1, classPersisted, "persisted history source label", history),
		site("internal/wsdashboard/plan_usage_probe.go", "codex", 1, classPersisted, "persisted history harness label", history),
		site("internal/wsdashboard/plan_usage_probe.go", "CODEX_HOME", 1, classExternal, "the provider's credential directory variable", "TestPlanUsageCodexHomeSelection"),
		site("internal/wsdashboard/plan_usage_probe.go", ".codex", 1, classExternal, "the provider's credential directory", "TestPlanUsageCodexHomeSelection"),
		site("internal/wsdashboard/plan_usage_probe.go", "codex_cli_rs", 1, classExternal, "the provider's usage endpoint originator header", "TestPlanUsageIgnoresPiLogin"),
	)

	// cmd/ws-mcp
	add(
		site("cmd/ws-mcp/hosted_cli.go", "codex-stop-hook", 1, classExternal, "the CLI subcommand Codex's hook configuration invokes", "TestMailboxCodexStopHookBlocksWhenNamedInboxHasUnreadMail"),
		site("cmd/ws-mcp/mailbox.go", "codex-stop-hook", 1, classExternal, "the CLI subcommand Codex's hook configuration invokes", "TestMailboxCodexStopHookBlocksWhenNamedInboxHasUnreadMail"),
		site("cmd/ws-mcp/mailbox.go", "mailbox codex-stop-hook", 1, classExternal, "the CLI subcommand Codex's hook configuration invokes", "TestMailboxCodexStopHookBlocksWhenNamedInboxHasUnreadMail"),
		site("cmd/ws-mcp/mailbox.go", "usage: ws-mcp mailbox <wait|codex-stop-hook>", 1, classExternal, "the usage line of that subcommand", "TestMailboxCodexStopHookBlocksWhenNamedInboxHasUnreadMail"),
	)
	return out
}()

// scanRoots are the scanned package directories, relative to this package, and
// their names relative to the agents-plugin-tool root.
var scanRoots = []struct{ dir, rel string }{
	{".", "internal/wsagent"},
	{"../mcp", "internal/mcp"},
	{"../wsdashboard", "internal/wsdashboard"},
	{"../../cmd/ws-mcp", "cmd/ws-mcp"},
}

// aliasRoots are the directories searched for named string constants whose value
// is a harness name (wslive.AgentKindPi, PiTierHarness): a constant is the same
// name branch as the literal it holds, so a use of one is gated like the literal.
var aliasRoots = []string{"..", "../../cmd"}

var (
	adapterOwned = []*regexp.Regexp{
		// Backend implementation files own genuine harness behavior.
		regexp.MustCompile(`^internal/wsagent/(claude|codex|agy|pi|werk)[a-z0-9_]*\.go$`),
		regexp.MustCompile(`^internal/wsagent/audit_(claude|codex|agy|werk)\.go$`),
		// The builtin harness's hidden subcommand, which the daemon starts.
		regexp.MustCompile(`^cmd/ws-mcp/werk[a-z0-9_]*\.go$`),
		// The Pi extension's MCP tool surface and forwarder.
		regexp.MustCompile(`^internal/mcp/pi_[a-z0-9_]*\.go$`),
		// The Agy usage provider.
		regexp.MustCompile(`^internal/wsdashboard/plan_usage_agy\.go$`),
	}
	harnessWord  = regexp.MustCompile(`(?i)claude|codex|agy|antigravity|(^|[^a-z0-9])(pi|werk)([^a-z0-9]|$)`)
	harnessExact = regexp.MustCompile(`(?i)^(claude|codex|agy|antigravity|pi|pi-rpc|werk)$`)
	harnessAlt   = regexp.MustCompile(`(?i)\|(claude|codex|agy|pi-rpc|pi|werk)\b|\b(claude|codex|agy|pi-rpc|pi|werk)\|`)
	backendIdent = regexp.MustCompile(`^Backend(Claude|Codex|Agy|Pi|PiRPC|Werk)$`)

	// decisionCalls are the strings functions whose literal argument is a
	// comparison operand: a long product name there is a branch, not prose.
	decisionCalls = map[string]bool{
		"EqualFold": true, "Contains": true, "HasPrefix": true, "HasSuffix": true,
		"Index": true, "Cut": true, "TrimPrefix": true, "TrimSuffix": true,
	}
)

// gatedLiteral reports whether a string literal names a harness as a name: a
// short or token-shaped literal, or an alternation of names.
func gatedLiteral(v string) bool {
	if harnessAlt.MatchString(v) {
		return true
	}
	if !harnessWord.MatchString(v) {
		return false
	}
	return !strings.ContainsAny(v, " \t\n") || len(v) <= shortNameLiteral
}

// gatedOperand is gatedLiteral for a literal that is a decision operand (a
// switch case, an ==/!= operand, a strings comparison argument, or a map key):
// any literal naming a harness there is gated, however long, because a product
// name compared against a client string is a name branch. Diagnostic and format
// strings are not operands and stay allowed.
func gatedOperand(v string, operand bool) bool {
	return gatedLiteral(v) || (operand && harnessWord.MatchString(v))
}

// harnessAliases is the set of constant names whose value is a harness name,
// discovered once across the repository's non-test Go code: every constant
// declared as a harness name literal, and every constant declared as one of
// those. Backend{Claude,Codex,Agy,Pi}, PiTierHarness, and wslive.AgentKindPi
// are found this way, as is any constant added later.
var (
	aliasOnce  sync.Once
	aliasNames map[string]bool
)

func harnessAliases(t testing.TB) map[string]bool {
	t.Helper()
	aliasOnce.Do(func() {
		aliasNames = map[string]bool{}
		type pending struct {
			name, from string
		}
		var chained []pending
		fset := token.NewFileSet()
		for _, root := range aliasRoots {
			err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					switch d.Name() {
					case "node_modules", "testdata", ".git", "vendor":
						return filepath.SkipDir
					}
					return nil
				}
				if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return nil
				}
				f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
				if err != nil {
					return err
				}
				for _, decl := range f.Decls {
					gen, ok := decl.(*ast.GenDecl)
					if !ok || gen.Tok != token.CONST {
						continue
					}
					for _, spec := range gen.Specs {
						vs := spec.(*ast.ValueSpec)
						for i, name := range vs.Names {
							if i >= len(vs.Values) {
								break
							}
							switch v := vs.Values[i].(type) {
							case *ast.BasicLit:
								if val, err := strconv.Unquote(v.Value); err == nil && v.Kind == token.STRING && harnessExact.MatchString(val) {
									aliasNames[name.Name] = true
								}
							case *ast.Ident:
								chained = append(chained, pending{name.Name, v.Name})
							case *ast.SelectorExpr:
								chained = append(chained, pending{name.Name, v.Sel.Name})
							}
						}
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		for changed := true; changed; {
			changed = false
			for _, c := range chained {
				if aliasNames[c.from] && !aliasNames[c.name] {
					aliasNames[c.name] = true
					changed = true
				}
			}
		}
	})
	return aliasNames
}

type foundSite struct{ file, token string }

// scanResult is what one source file holds: each gated name occurrence by token,
// and how many of them sit in a top-level declaration that carries no site
// comment.
type scanResult struct {
	found    map[foundSite]int
	unmarked map[foundSite]int
}

// scanSource parses one file and returns its gated name occurrences. An
// occurrence is marked when the top-level declaration holding it (its doc
// comment included) carries a site comment, so a marker names the concrete site
// it justifies rather than the file.
func scanSource(rel string, src []byte, aliases map[string]bool) (scanResult, error) {
	res := scanResult{found: map[foundSite]int{}, unmarked: map[foundSite]int{}}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.ParseComments)
	if err != nil {
		return res, err
	}

	// Decision operands.
	operand := map[*ast.BasicLit]bool{}
	mark := func(e ast.Expr) {
		if lit, ok := e.(*ast.BasicLit); ok {
			operand[lit] = true
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if x.Op == token.EQL || x.Op == token.NEQ {
				mark(x.X)
				mark(x.Y)
			}
		case *ast.CaseClause:
			for _, e := range x.List {
				mark(e)
			}
		case *ast.KeyValueExpr:
			mark(x.Key)
		case *ast.IndexExpr:
			mark(x.Index)
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && decisionCalls[sel.Sel.Name] {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "strings" {
					for _, a := range x.Args {
						mark(a)
					}
				}
			}
		}
		return true
	})

	var markers []*ast.CommentGroup
	for _, g := range f.Comments {
		var raw strings.Builder
		for _, c := range g.List {
			raw.WriteString(c.Text)
			raw.WriteByte('\n')
		}
		if siteCommentRe.MatchString(raw.String()) {
			markers = append(markers, g)
		}
	}
	marked := func(pos token.Pos) bool {
		for _, d := range f.Decls {
			start, end := d.Pos(), d.End()
			switch x := d.(type) {
			case *ast.FuncDecl:
				if x.Doc != nil {
					start = x.Doc.Pos()
				}
			case *ast.GenDecl:
				if x.Doc != nil {
					start = x.Doc.Pos()
				}
			}
			if pos < start || pos >= end {
				continue
			}
			for _, g := range markers {
				if g.Pos() >= start && g.End() <= end {
					return true
				}
			}
			return false
		}
		return false
	}

	record := func(tok string, pos token.Pos) {
		key := foundSite{rel, tok}
		res.found[key]++
		if !marked(pos) {
			res.unmarked[key]++
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if backendIdent.MatchString(x.Name) || aliases[x.Name] {
				record(x.Name, x.Pos())
			}
		case *ast.BasicLit:
			if x.Kind != token.STRING {
				break
			}
			v, err := strconv.Unquote(x.Value)
			if err != nil {
				break
			}
			if gatedOperand(v, operand[x]) {
				record(v, x.Pos())
			}
		}
		return true
	})
	return res, nil
}

func isAdapterOwned(rel string) bool {
	for _, re := range adapterOwned {
		if re.MatchString(rel) {
			return true
		}
	}
	return false
}

// scanHarnessNames returns every gated name occurrence of the scanned packages
// by file and token, with the count of those outside a site-commented
// declaration.
func scanHarnessNames(t *testing.T) scanResult {
	t.Helper()
	aliases := harnessAliases(t)
	total := scanResult{found: map[foundSite]int{}, unmarked: map[foundSite]int{}}
	for _, root := range scanRoots {
		entries, err := os.ReadDir(root.dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			rel := root.rel + "/" + name
			if isAdapterOwned(rel) {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(root.dir, name))
			if err != nil {
				t.Fatal(err)
			}
			res, err := scanSource(rel, raw, aliases)
			if err != nil {
				t.Fatalf("%s: %v", rel, err)
			}
			for k, n := range res.found {
				total.found[k] += n
			}
			for k, n := range res.unmarked {
				total.unmarked[k] += n
			}
		}
	}
	return total
}

func TestCommonCodeHasNoHarnessNameBranch(t *testing.T) {
	res := scanHarnessNames(t)
	found := res.found
	fixtures := testFunctionIndex(t)

	allowed := map[foundSite]nameSite{}
	for _, s := range harnessNameAllowlist {
		key := foundSite{s.file, s.token}
		if _, dup := allowed[key]; dup {
			t.Errorf("allowlist lists %s %q twice", s.file, s.token)
		}
		allowed[key] = s
	}

	var problems []string
	for key, n := range found {
		s, ok := allowed[key]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: %q occurs %d time(s) in common code; route the difference through the registration seam (backend_policy.go) or, for old persisted bytes or an external identifier, add an allowlist entry", key.file, key.token, n))
		case s.count != n:
			problems = append(problems, fmt.Sprintf("%s: %q occurs %d time(s), the allowlist says %d", key.file, key.token, n, s.count))
		}
	}
	for key, s := range allowed {
		if found[key] == 0 {
			problems = append(problems, fmt.Sprintf("%s: stale allowlist entry %q", key.file, key.token))
		}
		if s.class != classDeclarative && s.class != classPersisted && s.class != classExternal {
			problems = append(problems, fmt.Sprintf("%s %q: unknown class %q", key.file, key.token, s.class))
		}
		if s.boundary == "" || len(s.fixtures) == 0 {
			problems = append(problems, fmt.Sprintf("%s %q: an entry needs its boundary and a fixture", key.file, key.token))
		}
		if n := res.unmarked[key]; s.class != classDeclarative && n > 0 {
			problems = append(problems, fmt.Sprintf("%s %q: %d site(s) sit in a declaration with no %q comment naming the boundary", key.file, key.token, n, siteComment))
		}
		for _, fixture := range s.fixtures {
			if !fixtures[fixture] {
				problems = append(problems, fmt.Sprintf("%s %q: fixture %s does not exist", key.file, key.token, fixture))
			}
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

var testFuncRe = regexp.MustCompile(`(?m)^func (Test\w+)\(`)

// testFunctionIndex is the set of test functions declared in the scanned
// packages' test files, built from one read of each file.
func testFunctionIndex(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, root := range scanRoots {
		entries, err := os.ReadDir(root.dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(root.dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range testFuncRe.FindAllSubmatch(raw, -1) {
				out[string(m[1])] = true
			}
		}
	}
	return out
}

// TestHarnessNameScanDetectsAnOffender proves the gate trips: a branch on a
// backend constant or on an alias of one, a name literal, a key prefix, or a
// long product name in a comparison is gated, and prose and unrelated words are
// not.
func TestHarnessNameScanDetectsAnOffender(t *testing.T) {
	for _, v := range []string{"claude", "Pi", "agy", "codex", "pi.", "lead-pi", "via Codex CLI", "claude.exec_cutover_enabled", "CODEX_HOME", "(?:claude|codex|pi)", "<wait|codex-stop-hook>"} {
		if !gatedLiteral(v) {
			t.Errorf("%q is not gated", v)
		}
	}
	for _, v := range []string{"", "pipe", "spill", "api", "happy", "Pass a rendered tier as returned by the daemon, claude or codex", "lead", "typical", "pin"} {
		if gatedLiteral(v) {
			t.Errorf("%q is gated", v)
		}
	}
	if !backendIdent.MatchString("BackendPi") || backendIdent.MatchString("BackendPiExtra") || backendIdent.MatchString("Backends") {
		t.Error("the Backend identifier pattern is wrong")
	}
}

// scanOffender scans a synthetic common-code file.
func scanOffender(t *testing.T, src string) scanResult {
	t.Helper()
	res, err := scanSource("internal/mcp/offender.go", []byte(src), harnessAliases(t))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestHarnessNameScanFindsConstantAliases: a constant whose value is a harness
// name is the same branch as the literal, so a use of it is gated. The aliases
// come from the constants' own declarations, not from a list kept here.
func TestHarnessNameScanFindsConstantAliases(t *testing.T) {
	aliases := harnessAliases(t)
	for _, name := range []string{"BackendClaude", "BackendCodex", "BackendAgy", "BackendPi", "AgentKindPi"} {
		if !aliases[name] {
			t.Errorf("%s is not discovered as a harness-name constant", name)
		}
	}
	res := scanOffender(t, `package mcp

func branch(h string) bool {
	switch h {
	case wsagent.BackendPi:
		return true
	}
	return h == wslive.AgentKindPi
}
`)
	for _, name := range []string{"BackendPi", "AgentKindPi"} {
		if res.found[foundSite{"internal/mcp/offender.go", name}] != 1 {
			t.Errorf("a use of %s was not gated: %v", name, res.found)
		}
	}
}

// TestHarnessNameScanGatesLongProductNameOperands: a long product name compared
// against a client string is a branch, while the same words in a diagnostic are
// prose.
func TestHarnessNameScanGatesLongProductNameOperands(t *testing.T) {
	res := scanOffender(t, `package mcp

import "strings"

func branch(clientName string, m map[string]int) bool {
	if strings.EqualFold(clientName, "Claude Code Enterprise CLI") {
		return true
	}
	if clientName == "Codex Desktop Enterprise Edition build" {
		return true
	}
	_ = m["Antigravity Workspace Client build"]
	switch clientName {
	case "Pi Coding Agent Extension Bridge":
	}
	return false
}

func message() string {
	return "this read tool runs only in a Claude Code session whose shell tool is replaced"
}
`)
	for _, tok := range []string{"Claude Code Enterprise CLI", "Codex Desktop Enterprise Edition build", "Antigravity Workspace Client build", "Pi Coding Agent Extension Bridge"} {
		if res.found[foundSite{"internal/mcp/offender.go", tok}] != 1 {
			t.Errorf("the operand %q was not gated: %v", tok, res.found)
		}
	}
	if len(res.found) != 4 {
		t.Errorf("prose was gated: %v", res.found)
	}
}

// TestHarnessNameScanMarksSitesIndividually: a site comment justifies the
// declaration it sits in, not the file.
func TestHarnessNameScanMarksSitesIndividually(t *testing.T) {
	res := scanOffender(t, `package mcp

// Retained name check: the stored key.
func marked() string { return "claude.key" }

func unmarked() string { return "pi.key" }
`)
	if res.unmarked[foundSite{"internal/mcp/offender.go", "claude.key"}] != 0 {
		t.Error("a commented declaration was reported unmarked")
	}
	if res.unmarked[foundSite{"internal/mcp/offender.go", "pi.key"}] != 1 {
		t.Error("an uncommented declaration was reported marked")
	}
}
