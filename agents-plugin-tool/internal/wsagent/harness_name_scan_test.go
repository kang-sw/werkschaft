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
//   - any BackendClaude, BackendCodex, BackendAgy, or BackendPi identifier;
//   - any short string literal (no whitespace, or at most
//     shortNameLiteral bytes) that names a harness (claude, codex, agy, pi,
//     antigravity) as a word or inside a token such as a config key, tool
//     name, environment variable, path, or provider label; and
//   - any alternation of harness names inside a literal (a pattern).
//
// outside the adapter-owned files and outside harnessNameAllowlist, which this
// test carries. Long prose (descriptions, diagnostics, error text) is not a
// name branch and is not gated. The allowlist is exact: each entry names a
// file, the literal or identifier, how many times it occurs, its class, the
// persisted or external boundary that registration data cannot interpret, and
// the fixture that pins it. A new site, a changed count, a stale entry, a
// missing fixture, or a missing site comment fails the test. Work that adds a
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

// siteComment is the marker every file holding a persisted or external
// allowlist entry carries in a comment at its sites.
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
		piTools     = "TestBuiltinPiMiniLeadProfileResolves"
		piChild     = "TestPiTokenSpawnMintsTheChildKeyUnderItsHoldersPolicyKey"
		compactKeys = "TestCompactionConfigGetTuneAndLegacy"
		detect      = "TestDetectHarnessFromRawKeepsExternalMarkers"
		history     = "TestPlanUsageHistoryValidatorsKeepPersistedNames"
		credentials = "TestFSLocalDeniedProviderCredentials"
	)

	// internal/wsagent
	add(
		site("internal/wsagent/backend.go", "claude", 1, classExternal,
			"the Windows shim remedy keys on the program the shim wraps (claude.exe), an executable name, not a backend",
			"TestHarnessCommandRefusalNamesARemedy"),
		site("internal/wsagent/profile.go", "BackendClaude", 1, classDeclarative, "the registration name constants", profileRows),
		site("internal/wsagent/profile.go", "BackendCodex", 1, classDeclarative, "the registration name constants", profileRows),
		site("internal/wsagent/profile.go", "BackendAgy", 1, classDeclarative, "the registration name constants", profileRows),
		site("internal/wsagent/profile.go", "BackendPi", 1, classDeclarative, "the registration name constants", profileRows),
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
		site("internal/wsagent/builtin.go", "pi-worker", 1, classDeclarative, "built-in profile row name", "TestBuiltinAgentWorkerProfiles"),
		site("internal/wsagent/builtin.go", "pi-mini-lead", 1, classDeclarative, "built-in profile row name", piTools),
		site("internal/wsagent/builtin.go", "agent-worker-agy", 1, classDeclarative, "built-in profile row name", "TestBuiltinAgentWorkerProfiles"),
		site("internal/wsagent/builtin.go", "agent-worker-codex", 1, classDeclarative, "built-in profile row name", "TestBuiltinAgentWorkerProfiles"),
		site("internal/wsagent/builtin.go", "pi.spawn", 1, classExternal, "the Pi extension's own MCP tool names in the Pi worker allowlist", piTools),
		site("internal/wsagent/builtin.go", "pi.send", 1, classExternal, "the Pi extension's own MCP tool names in the Pi worker allowlist", piTools),
		site("internal/wsagent/builtin.go", "pi.list", 1, classExternal, "the Pi extension's own MCP tool names in the Pi worker allowlist", piTools),
		site("internal/wsagent/builtin.go", "pi.stop", 1, classExternal, "the Pi extension's own MCP tool names in the Pi worker allowlist", piTools),
		site("internal/wsagent/builtin.go", "pi.transcript", 1, classExternal, "the Pi extension's own MCP tool names in the Pi worker allowlist", piTools),
		site("internal/wsagent/builtin.go", "pi.wait", 1, classExternal, "the Pi extension's own MCP tool names in the Pi worker allowlist", piTools),
		site("internal/wsagent/builtin.go", "pi.", 1, classExternal, "the Pi tool-name prefix that keeps the Pi child tools out of the agent worker allowlist", "TestAgentWSToolsListSageGateAndStamp"),
		site("internal/wsagent/catalog.go", "claude.compaction_advisory_percent", 1, classPersisted, "legacy config key a user's config may still hold", compactKeys),
		site("internal/wsagent/catalog.go", "claude.compaction_advisory_enabled", 1, classPersisted, "config key a user's config may hold", compactKeys),
		site("internal/wsagent/catalog.go", "pi.compaction_advisory_percent", 1, classPersisted, "legacy config key a user's config may still hold", compactKeys),
		site("internal/wsagent/catalog.go", "pi.compaction_hard_percent", 1, classPersisted, "legacy config key a user's config may still hold", compactKeys),
		site("internal/wsagent/children.go", "WS_MCP_PI_TOKEN", 1, classExternal, "environment variable the bundled Pi extension reads", piChild),
		site("internal/wsagent/children.go", "WS_MCP_PI_ENDPOINT", 1, classExternal, "environment variable the bundled Pi extension reads", piChild),
		site("internal/wsagent/children.go", "pi", 1, classPersisted, "PiTierHarness: the harness bucket stored in tier routing and the legacy pi.spawn tier rule", piChild),
		site("internal/wsagent/children.go", "pi.", 3, classExternal, "the pi.* tool-name prefix of the Pi extension's calls", piChild),
		site("internal/wsagent/children.go", "pi.spawn", 1, classExternal, "the Pi extension's spawn tool name", piChild),
		site("internal/wsagent/holder.go", "pi-worker", 1, classPersisted, "holder kind an older daemon wrote into a still-running holder's descriptor", "TestWorkerHolderOwnership"),
		site("internal/wsagent/worker_rejoin.go", "BackendPi", 2, classPersisted, "a legacy pi-worker holder keeps its absent backend across rejoin", "TestWorkerHolderOwnership"),
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
		site("internal/mcp/dashboard_settings_config.go", "claude.status_band_enabled", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardSettingsReadTunedValues"),
		site("internal/mcp/dashboard_settings_config.go", "pi.runaway_threshold", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardSettingsReadTunedValues"),
		site("internal/mcp/dashboard_settings_config.go", "pi.settle_delay_ms", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardSettingsReadTunedValues"),
		site("internal/mcp/dashboard_settings_config.go", "pi.compaction_advisory_percent", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardSettingsReadTunedValues"),
		site("internal/mcp/dashboard_settings_config.go", "pi.compaction_hard_percent", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardSettingsReadTunedValues"),
		site("internal/mcp/dashboard_settings_config.go", "pi.context_window_override", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardSettingsReadTunedValues"),
		site("internal/mcp/dashboard_settings_config.go", "pi.compaction_dialog_budget_bytes", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardSettingsReadTunedValues"),
		site("internal/mcp/dashboard_settings_config.go", "pi.compaction_reread_budget_bytes", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardSettingsReadTunedValues"),
		site("internal/mcp/dashboard_settings_config.go", "agy.model_wait_timeout_seconds", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardSettingsReadTunedValues"),
		site("internal/mcp/dashboard_settings_config.go", "agy.model_wait_retries", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardSettingsReadTunedValues"),
		site("internal/mcp/dashboard_settings_config.go", "agy.stall_log_check", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardSettingsReadTunedValues"),
		site("internal/mcp/dashboard_settings_config.go", "agy.http2_frame_trace", 1, classPersisted, "persisted config keys the settings page routes by group", "TestDashboardSettingsReadTunedValues"),
		site("internal/mcp/dashboard_settings_config.go", "dashboard.pi_spawn_enabled", 1, classPersisted, "persisted config key the settings page routes by group", "TestDashboardSettingsReadTunedValues"),
		site("internal/mcp/dashboard_settings_config.go", "claude.", 1, classPersisted, "the harness-bucketed config key prefix the settings page groups by", "TestDashboardSettingsReadTunedValues"),
		site("internal/mcp/dashboard_settings_config.go", "pi.", 1, classPersisted, "the harness-bucketed config key prefix the settings page groups by", "TestDashboardSettingsReadTunedValues"),
		site("internal/mcp/server_dispatch.go", "pi.spawn", 1, classExternal, "the Pi extension's MCP tool names, forwarded to the daemon", "TestPiForwardShowsSleepAndDeleted"),
		site("internal/mcp/server_dispatch.go", "pi.send", 1, classExternal, "the Pi extension's MCP tool names, forwarded to the daemon", "TestPiForwardShowsSleepAndDeleted"),
		site("internal/mcp/server_dispatch.go", "pi.list", 1, classExternal, "the Pi extension's MCP tool names, forwarded to the daemon", "TestPiForwardShowsSleepAndDeleted"),
		site("internal/mcp/server_dispatch.go", "pi.stop", 1, classExternal, "the Pi extension's MCP tool names, forwarded to the daemon", "TestPiForwardShowsSleepAndDeleted"),
		site("internal/mcp/server_dispatch.go", "pi.transcript", 1, classExternal, "the Pi extension's MCP tool names, forwarded to the daemon", "TestPiForwardShowsSleepAndDeleted"),
		site("internal/mcp/server_dispatch.go", "pi.wait", 1, classExternal, "the Pi extension's MCP tool names, forwarded to the daemon", "TestPiForwardShowsSleepAndDeleted"),
		site("internal/mcp/server_harness.go", "x-codex-turn-metadata", 2, classExternal, "Codex's own request metadata key and its raw marker", detect),
		site("internal/mcp/server_harness.go", "codex", 3, classExternal, "historical raw initialize substring and its precedence over claude", detect),
		site("internal/mcp/server_harness.go", "claude", 2, classExternal, "historical raw initialize substring", detect),
		site("internal/mcp/server_inventory.go", "antigravity/alwaysLoad", 1, classExternal, "the _meta key Antigravity's client reads to always load a tool", "TestHostedAgyToolsAlwaysLoad"),
	)

	// internal/wsdashboard
	add(
		site("internal/wsdashboard/fs_denied.go", "CODEX_HOME", 1, classExternal, "the provider's credential directory variable", credentials),
		site("internal/wsdashboard/fs_denied.go", "CLAUDE_CONFIG_DIR", 1, classExternal, "the provider's credential directory variable", credentials),
		site("internal/wsdashboard/fs_outside.go", ".codex/auth.json", 1, classExternal, "the provider's credential file", credentials),
		site("internal/wsdashboard/fs_outside.go", ".claude/.credentials.json", 1, classExternal, "the provider's credential file", credentials),
		site("internal/wsdashboard/conversation_presentation.go", `^\[(?:ws|wsflow) note: from a peer lead session \((?:(?:claude|codex|pi), )?not a worker\), with the same authority as yours\]$`, 1, classPersisted,
			"stored peer-lead mail text written by earlier versions, which labeled only claude, codex, and pi", "TestConversationPresentationPeerNoteIsDisplayOnly"),
		site("internal/wsdashboard/page_reader.go", "pi", 1, classPersisted, "the Pi session file location inside a session directory, watched for child revival", "TestPruneRegistrationsAgesRetiredLeadsByTheirRow"),
		site("internal/wsdashboard/pageview_hud.go", "pi", 1, classPersisted, "the historical Pi agent-kind marker of a live agent with no harness name", "TestHUDHarnessLegacyPiAgentKind"),
		site("internal/wsdashboard/registry.go", "pi", 2, classPersisted, "the Pi record backend and session file location protected from retirement deletion", "TestPruneRegistrationsAgesRetiredLeadsByTheirRow"),
		site("internal/wsdashboard/plan_usage.go", `json:"claude"`, 2, classPersisted, "the plan-usage file's claude member", history),
		site("internal/wsdashboard/plan_usage.go", "claude", 3, classPersisted, "persisted history harness label", history),
		site("internal/wsdashboard/plan_usage.go", "codex", 1, classPersisted, "persisted history harness label", history),
		site("internal/wsdashboard/plan_usage.go", "Claude push", 2, classPersisted, "persisted history source label", history),
		site("internal/wsdashboard/plan_usage.go", "CLAUDE_CONFIG_DIR", 1, classExternal, "the provider's credential directory variable", "TestPlanUsageCodexHomeSelection"),
		site("internal/wsdashboard/plan_usage.go", ".claude", 1, classExternal, "the provider's credential directory", "TestPlanUsageCodexHomeSelection"),
		site("internal/wsdashboard/plan_usage.go", "CODEX_HOME", 1, classExternal, "the provider's credential directory variable", "TestPlanUsageCodexHomeSelection"),
		site("internal/wsdashboard/plan_usage.go", ".codex", 1, classExternal, "the provider's credential directory", "TestPlanUsageCodexHomeSelection"),
		site("internal/wsdashboard/plan_usage.go", ".pi", 1, classExternal, "the Pi credential directory", "TestPlanUsageCodexCredentials"),
		site("internal/wsdashboard/plan_usage.go", `json:"claudeAiOauth"`, 1, classExternal, "the provider's credential file member", "TestPlanUsageCodexCredentials"),
		site("internal/wsdashboard/plan_usage.go", `json:"openai-codex"`, 1, classExternal, "the provider's credential file member", "TestPlanUsageCodexCredentials"),
		site("internal/wsdashboard/plan_usage.go", "codexFiveHourHistory", 1, classPersisted, "the plan-usage response member the page reads", "TestPlanUsageCodexFiveHourT0CompatibilityAndSeparateFile"),
		site("internal/wsdashboard/plan_usage_history.go", "plan-usage-codex-history.json", 1, classPersisted, "the persisted five-hour history file name", "TestPlanUsageCodexFiveHourT0CompatibilityAndSeparateFile"),
		site("internal/wsdashboard/plan_usage_history.go", "codex", 2, classPersisted, "persisted history validators accept the labels earlier writers wrote", history),
		site("internal/wsdashboard/plan_usage_history.go", "claude", 2, classPersisted, "persisted history validators accept the labels earlier writers wrote", history),
		site("internal/wsdashboard/plan_usage_history.go", "via Codex CLI", 2, classPersisted, "persisted history source label", history),
		site("internal/wsdashboard/plan_usage_history.go", "via Pi", 2, classPersisted, "persisted history source label", history),
		site("internal/wsdashboard/plan_usage_history.go", "Claude push", 1, classPersisted, "persisted history source label", history),
		site("internal/wsdashboard/plan_usage_probe.go", "via Codex CLI", 1, classPersisted, "persisted history source label", history),
		site("internal/wsdashboard/plan_usage_probe.go", "via Pi", 2, classPersisted, "persisted history source label", history),
		site("internal/wsdashboard/plan_usage_probe.go", "codex", 1, classPersisted, "persisted history harness label", history),
		site("internal/wsdashboard/plan_usage_probe.go", "CODEX_HOME", 1, classExternal, "the provider's credential directory variable", "TestPlanUsageCodexHomeSelection"),
		site("internal/wsdashboard/plan_usage_probe.go", ".codex", 1, classExternal, "the provider's credential directory", "TestPlanUsageCodexHomeSelection"),
		site("internal/wsdashboard/plan_usage_probe.go", ".pi", 1, classExternal, "the Pi credential directory", "TestPlanUsageCodexCredentials"),
		site("internal/wsdashboard/plan_usage_probe.go", `json:"openai-codex"`, 1, classExternal, "the provider's credential file member", "TestPlanUsageCodexCredentials"),
		site("internal/wsdashboard/plan_usage_probe.go", "codex_cli_rs", 1, classExternal, "the provider's usage endpoint originator header", "TestPlanUsageCodexRESTWindows"),
		site("internal/wsdashboard/plan_usage_probe.go", "codex-cli", 1, classExternal, "the provider's usage endpoint client header", "TestPlanUsageCodexRESTWindows"),
		site("internal/wsdashboard/plan_usage_probe.go", "codex-1", 1, classExternal, "the provider's usage endpoint beta header", "TestPlanUsageCodexRESTWindows"),
		site("internal/wsdashboard/plan_usage_probe.go", "Codex Desktop", 1, classExternal, "the provider's usage endpoint originator header", "TestPlanUsageCodexRESTWindows"),
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

var (
	adapterOwned = []*regexp.Regexp{
		// Backend implementation files own genuine harness behavior.
		regexp.MustCompile(`^internal/wsagent/(claude|codex|agy|pi)[a-z0-9_]*\.go$`),
		regexp.MustCompile(`^internal/wsagent/audit_(claude|codex)\.go$`),
		// The Pi extension's MCP tool surface and forwarder.
		regexp.MustCompile(`^internal/mcp/pi_[a-z0-9_]*\.go$`),
		// The Agy usage provider.
		regexp.MustCompile(`^internal/wsdashboard/plan_usage_agy\.go$`),
	}
	harnessWord  = regexp.MustCompile(`(?i)claude|codex|agy|antigravity|(^|[^a-z0-9])pi([^a-z0-9]|$)`)
	harnessAlt   = regexp.MustCompile(`(?i)\|(claude|codex|agy|pi)\b|\b(claude|codex|agy|pi)\|`)
	backendIdent = regexp.MustCompile(`^Backend(Claude|Codex|Agy|Pi)$`)
)

func gatedLiteral(v string) bool {
	if harnessAlt.MatchString(v) {
		return true
	}
	if !harnessWord.MatchString(v) {
		return false
	}
	return !strings.ContainsAny(v, " \t\n") || len(v) <= shortNameLiteral
}

type foundSite struct{ file, token string }

// scanHarnessNames returns the count of every gated name occurrence by file
// and token, and the source of each scanned file.
func scanHarnessNames(t *testing.T) (map[foundSite]int, map[string]string) {
	t.Helper()
	found := map[foundSite]int{}
	sources := map[string]string{}
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
			raw, err := os.ReadFile(filepath.Join(root.dir, name))
			if err != nil {
				t.Fatal(err)
			}
			sources[rel] = string(raw)
			skip := false
			for _, re := range adapterOwned {
				skip = skip || re.MatchString(rel)
			}
			if skip {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), rel, raw, 0)
			if err != nil {
				t.Fatalf("%s: %v", rel, err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.Ident:
					if backendIdent.MatchString(x.Name) {
						found[foundSite{rel, x.Name}]++
					}
				case *ast.BasicLit:
					if x.Kind != token.STRING {
						break
					}
					v, err := strconv.Unquote(x.Value)
					if err != nil {
						t.Fatalf("%s: %v", rel, err)
					}
					if gatedLiteral(v) {
						found[foundSite{rel, v}]++
					}
				}
				return true
			})
		}
	}
	return found, sources
}

func TestCommonCodeHasNoHarnessNameBranch(t *testing.T) {
	found, sources := scanHarnessNames(t)

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
		if s.class != classDeclarative && !siteCommentRe.MatchString(sources[key.file]) {
			problems = append(problems, fmt.Sprintf("%s %q: the file carries no %q site comment", key.file, key.token, siteComment))
		}
		for _, fixture := range s.fixtures {
			if !fixtureExists(t, fixture) {
				problems = append(problems, fmt.Sprintf("%s %q: fixture %s does not exist", key.file, key.token, fixture))
			}
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

// fixtureExists reports whether a test function of that name is declared in a
// scanned package's test files.
func fixtureExists(t *testing.T, name string) bool {
	t.Helper()
	needle := "func " + name + "("
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
			if strings.Contains(string(raw), needle) {
				return true
			}
		}
	}
	return false
}

// TestHarnessNameScanDetectsAnOffender proves the gate trips: a branch on a
// backend constant, a name literal, or a key prefix is gated, and prose and
// unrelated words are not.
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
