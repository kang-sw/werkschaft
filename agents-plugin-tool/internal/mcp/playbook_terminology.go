// playbook_terminology.go holds harness terminology and caller detection for playbooks.
package mcp

import (
	"slices"
	"strings"

	"github.com/kang-sw/devenv/internal/claudeagents"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wsrsrc"
)

// reservedToolVarNames documents the complete set of variable names that the
// tool layer may inject (from the terminology bindings, namespace table, or model
// alias resolution).
// This variable is a documentation artifact: the "tool-injected wins on
// collision" invariant is achieved by layer ordering in buildPlaybookVars
// (terminology, namespace, and model alias layers overwrite the caller context
// layer), not by a guard that references this set. Tests use it to assert that the
// documented reserved set is complete.
//
// Fixed-tier model and reasoning-effort variables are
// config-resolved like RoleModel. They
// arrive here via the wsrsrc.ImplicitVariableNames loop below (auto-inject
// pattern), not an explicit set[...] = true line.
var reservedToolVarNames = func() map[string]bool {
	set := map[string]bool{
		"ExploreAgent":  true,
		"SpawnIdiom":    true,
		"ContinueIdiom": true,
		"RoleModel":     true,
		"WorkflowLang":  true,
		// The workflow manual's spawning clauses (withManualClauses).
		"ExploreTierClause":   true,
		"RenderedSpawnClause": true,
		"RenderedTierClause":  true,
	}
	for _, name := range wsrsrc.ImplicitVariableNames {
		set[name] = true
	}
	return set
}()

// terminologyForHarness binds the registered static idioms to playbook
// variables. A harness without static idioms uses the host-neutral wording.
func terminologyForHarness(harness, namespace string) map[string]string {
	terms := wsagent.BuiltinPlaybookTerminology(harness)
	if terms == (wsagent.PlaybookTerminology{}) {
		terms = wsagent.PlaybookTerminology{
			ExploreAgent:  "an exploration agent",
			SpawnIdiom:    "spawning a subagent",
			ContinueIdiom: "resuming the agent using its returned id",
		}
	}
	if harness == wsagent.BackendClaude {
		terms.SpawnIdiom = claudeSpawnIdiom(namespace)
	}
	return map[string]string{
		"ExploreAgent":  terms.ExploreAgent,
		"SpawnIdiom":    terms.SpawnIdiom,
		"ContinueIdiom": terms.ContinueIdiom,
	}
}

// playbookTerminology selects the daemon agent idioms (agent.spawn and
// mailbox.send) only for a caller that delegates through them (a Pi caller
// without the native extension, or a Claude caller launched without its
// native subagent tool) and whose effective tool surface permits them.
func playbookTerminology(harness string, daemonAgents bool, namespace string) map[string]string {
	if daemonAgents {
		return piMCPOnlyTerminology(namespace)
	}
	return terminologyForHarness(harness, namespace)
}

// callerRecord is the daemon's own conversation record for key, found
// through the key's reply-id and naming this exact key, read as tracker
// support reads it. A key with no such record (a terminal harness, a
// keyless render, a native-subagent child) has none.
func (s *Server) callerRecord(key string) (wsagent.Record, bool) {
	if key == "" {
		return wsagent.Record{}, false
	}
	replyID := wsmailbox.ReplyID(key)
	if !wsdashboard.ValidReplyID(replyID) {
		return wsagent.Record{}, false
	}
	dir, err := s.dashboard.stateDir()
	if err != nil {
		return wsagent.Record{}, false
	}
	rec, err := wsagent.ReadRecord(dir, replyID)
	if err != nil || rec.WSKey != key {
		return wsagent.Record{}, false
	}
	return rec, true
}

// callerDaemonAgents reports whether key belongs to a conversation that
// delegates through the daemon's agent tools, by its callerRecord read
// once: an MCP-only Pi conversation (piMCPOnlyRecord) or a Claude
// conversation launched without its native subagent tool
// (subagentRemovedRecord).
func (s *Server) callerDaemonAgents(key string) bool {
	rec, ok := s.callerRecord(key)
	return ok && (s.piMCPOnlyRecord(rec) || subagentRemovedRecord(rec))
}

// subagentRemovedRecord reports whether rec is a Claude conversation the
// daemon launches without the native subagent tool
// (wsagent.RemovesSubagents: a hosted lead or an agent.spawn child). Its
// connection is detected as claude, but the claude row's spawn idiom names
// the tool its launch removed.
func subagentRemovedRecord(rec wsagent.Record) bool {
	return rec.Backend == wsagent.BackendClaude && wsagent.RemovesSubagents(rec)
}

// piMCPOnlyRecord reports whether rec is a Pi conversation the daemon runs
// without the native ws Pi extension (wsagent.PiMCPOnly). Such a session
// connects through Pi's builtin MCP client, whose clientInfo is not the
// extension's "ws-pi-bridge", so harness detection does not see it as pi.
func (s *Server) piMCPOnlyRecord(rec wsagent.Record) bool {
	if rec.Backend != wsagent.BackendPi {
		return false
	}
	def := rec.ResolvedDefinition
	if def == nil && !rec.Lead {
		// Records written before the launched-profile snapshot fall back to
		// the profile their name resolves to now.
		if rp, err := wsagent.ResolveProfile(s.configOptions(), rec.Root, rec.Profile, s.agentBackends()...); err == nil {
			def = &rp.Definition
		}
	}
	if !wsagent.PiMCPOnly(rec, def) {
		return false
	}
	if rec.Lead {
		return true
	}
	if def == nil {
		return false
	}
	return slices.Contains(def.WSTools, "agent.spawn") && slices.Contains(def.WSTools, "mailbox.send")
}

// piMCPOnlyTerminology names the daemon's agent tools, which a Pi session
// without the native extension and a Claude session without its native
// subagent tool delegate through, using the runtime namespace. ContinueIdiom has no backticks because delegationTip wraps it
// in a code span.
func piMCPOnlyTerminology(namespace string) map[string]string {
	return map[string]string{
		// No tier here: each site states the tier it wants (the manual's
		// "at the tier the question needs").
		"ExploreAgent":  "a read-only `" + namespace + "/agent.spawn` subagent",
		"SpawnIdiom":    "`" + namespace + "/agent.spawn` (tier: the resolved capability tier, not a model name; authority: at most your own)",
		"ContinueIdiom": namespace + "/mailbox.send to the address agent.spawn returned",
	}
}

// nativeFirstTerminology is the terminology of text a PreferNativeSubagent
// hosted lead executes itself (wsagent.BackendRegistration.PreferNativeSubagent):
// exploration goes to its own native subagent, which has no ws tools and
// runs at the harness's default model, and a spawn is piMCPOnlyTerminology's
// agent.spawn on an external harness, a bare size or own-harness role tier
// qualified with a harness first. ContinueIdiom is unchanged.
func nativeFirstTerminology(namespace string) map[string]string {
	terms := piMCPOnlyTerminology(namespace)
	terms["ExploreAgent"] = "your native subagent (no ws tools, your harness's default model; when the question needs a specific tier, a read-only `" + namespace + "/agent.spawn` subagent at an external-harness tier)"
	terms["SpawnIdiom"] = "`" + namespace + "/agent.spawn` on an external harness (tier: the resolved capability tier, not a model name, as a `claude.<size>`, `codex.<size>`, or configured route target; put `claude.` or `codex.` before a bare size or a role tier that resolves to your own harness; authority: at most your own)"
	return terms
}

// withManualClauses is terminology plus the workflow manual's spawning
// clauses: ExploreTierClause follows {{.ExploreAgent}} in the exploration
// bullet, RenderedSpawnClause says how a rendered delegate is spawned, and
// RenderedTierClause names a rendered playbook's tier. By default they are
// the manual's own wording. For nativeFirst (nativeFirstTerminology's
// caller), whose bare size would resolve to its own harness, the rendered
// tier gets the external-harness prefix and the exploration bullet drops
// the "tier the question needs" suffix, which its native subagent cannot
// honor. The caller's terminology map is not modified.
func withManualClauses(terminology map[string]string, nativeFirst bool, namespace string) map[string]string {
	out := make(map[string]string, len(terminology)+3)
	for k, v := range terminology {
		out[k] = v
	}
	if nativeFirst {
		out["ExploreTierClause"] = "; pick that tier by `### Choosing a tier`"
		out["RenderedSpawnClause"] = "bindings the render returns through `" + namespace + "/agent.spawn` on an external harness, at the render's tier qualified as `### Choosing a tier` states"
		out["RenderedTierClause"] = "the tier its render recommends; as a `" + namespace + "/agent.spawn` target, put `claude.` or `codex.` before a bare size or a role tier that resolves to your own harness"
		return out
	}
	out["ExploreTierClause"] = " at the tier the question needs (`### Choosing a tier`)"
	out["RenderedSpawnClause"] = "tier and bindings the render returns"
	out["RenderedTierClause"] = "the tier its render recommends"
	return out
}

// claudeSpawnIdiom is the Claude SpawnIdiom value. Claude's Agent tool takes a
// per-call model but no reasoning-effort parameter; effort comes only from the
// subagent definition, so the plugin ships one effort-carrier agent per Claude
// level (<namespace>:effort-<level>). The value carries the whole mapping rule
// because workers spawn from playbook.render bindings and config.resolve_agent
// output without ever reading the lead-only native-spawn-binding overlay. It is
// substituted mid-sentence at every {{.SpawnIdiom}} site, so it stays one
// clause. The namespace is assembled here rather than written as a
// {{.SkillNamespace}} literal: substituteVars never re-scans replacement
// values. The level list is the generator's own, so it names exactly the
// agents the packages ship.
func claudeSpawnIdiom(namespace string) string {
	levels := claudeagents.Levels
	levelList := strings.Join(levels[:len(levels)-1], ", ") + ", or " + levels[len(levels)-1]
	return `Agent({subagent_type: "` + namespace + `:effort-<resolved effort>", model: <resolved model>, ...})` +
		` (subagent_type "general-purpose" when the resolved effort is not exactly ` + levelList + `)`
}
