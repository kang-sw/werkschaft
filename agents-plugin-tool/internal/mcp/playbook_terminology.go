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
// agent.spawn with a rendered tier passed as returned (an agy route is
// allowed for an agy lead), restrained by the Antigravity terms risk toward
// an external fixed tier when another harness fits (nativeFirstTierRule).
// ContinueIdiom is unchanged.
func nativeFirstTerminology(namespace string) map[string]string {
	terms := piMCPOnlyTerminology(namespace)
	terms["ExploreAgent"] = "your native subagent (no ws tools, your harness's default model; when the question needs a specific tier, a read-only `" + namespace + "/agent.spawn` subagent at an external-harness tier)"
	terms["SpawnIdiom"] = "`" + namespace + "/agent.spawn` (tier: the resolved capability tier, not a model name; " + nativeFirstTierRule + "; authority: at most your own)"
	return terms
}

// nativeFirstTierRule is the agy lead's tier rule for agent.spawn, shared by
// its SpawnIdiom and its manual's tier line. Restraint of agy-to-agy spawns
// is this prompt alone: agy adds no harness-specific count or tier limit,
// while the generic depth, nested-tier, and width limits every agent.spawn
// passes still apply.
const nativeFirstTierRule = "pass a rendered tier as returned: a role tier keeps its configured route, model and effort, and may end on agy, which is allowed; an agy route carries the Antigravity terms risk, so when another harness fits the work, choose an external fixed tier such as `claude.small` for `playbook-explore:small` instead; never put a harness prefix before a role tier (`claude.playbook-explore:small` is rejected)"

// nativeFirstManualRewrites are the workflow manual sentences a
// nativeFirstTerminology caller reads differently, as (today's rendered
// text, its replacement) pairs, applied by nativeFirstManual. Its native
// subagent cannot run at "the tier the question needs", so the exploration
// bullet loses that suffix; the rendered-delegate bullet spawns through
// agent.spawn with the prompt path and the render's tier, and the tier line
// states nativeFirstTierRule. agent.spawn takes no model or effort, so the
// native wording names no render bindings.
func nativeFirstManualRewrites(namespace string) []string {
	explore := nativeFirstTerminology(namespace)["ExploreAgent"]
	spawn := "`" + namespace + "/agent.spawn`"
	return []string{
		explore + " at the tier the question needs (`### Choosing a tier`).",
		explore + "; pick that tier by `### Choosing a tier`.",
		"give the subagent the returned path and spawn it with the\n  tier and bindings the render returns.",
		"spawn the subagent through\n  " + spawn + " with the returned path and the render's tier, chosen as `### Choosing a tier` states.",
		"- A rendered playbook → the tier its render recommends, passed as returned;\n  a role tier such as `ticket-worker:large` is valid there.\n",
		"- A rendered playbook → the tier its render recommends; as a " + spawn + " target, " + nativeFirstTierRule + ".\n",
	}
}

// nativeFirstManual rewrites a rendered workflow manual body for a
// nativeFirstTerminology caller (nativeFirstManualRewrites). The rewrite is
// renderer-internal text replacement, not a declared variable, so the
// manual's frontmatter and caller-context validation stay those of every
// other caller.
func nativeFirstManual(body, namespace string) string {
	return strings.NewReplacer(nativeFirstManualRewrites(namespace)...).Replace(body)
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
