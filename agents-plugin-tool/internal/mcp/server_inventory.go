package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// isLeadOnlyTool reports whether a tool is restricted to lead-scoped session
// keys. It is the authority for the keyed-gate escalation block. The bootstrap
// tool must be listed explicitly because it no longer lives under the
// `lead.*` prefix (260617 obscurity rename) — a prefix-only check would
// silently stop blocking it for non-lead keys. workflow_manual is likewise
// listed explicitly: delegate/leaf callers must not reach the handler (Phase 3a
// security hardening), mirroring the ferrule guard. workflow_state (260702) is
// gated the same way as its sibling workflow_manual: it is a cheaper view of
// the same lead-bootstrap/recovery surface, not a general todo/agenda
// accessor, so it stays lead-only even though the underlying todo.*/agenda.*
// data is itself scope-open. tickets.sage_stamp (260723 Phase 2, renamed from
// tickets.sage_record) is listed explicitly because it stays inside the
// `tickets.*` prefix (no dedicated prefix to gate on): it is the sole
// terminal writer of sage-review posture + the ## Blocked companion, and
// reviewers must never write frontmatter directly (single-writer property,
// {#260720-sage-gate-record-tools}) — this is new enforcement, not a
// preserved no-op, since the pre-rename tickets.sage_record was reachable by
// a delegate-scoped key.
func isLeadOnlyTool(name string) bool {
	return sessionHireTool(name) || userCommandTool(name) || isPiTool(name) || isAgentTool(name) || isCompanionTool(name) || isDelegationReportTool(name) || isDaemonControlTool(name) || isConvTool(name) || name == bootstrapToolName || name == "workflow_manual" || name == "workflow_state" || name == "tickets.sage_stamp" || name == "tickets.index_init" || name == "git.merge" || name == "worktree.acquire" || name == "worktree.release" || name == "worktree.remove" || name == "worktree.list" || strings.HasPrefix(name, "lead.") || workflowPreferenceWriterTool(name)
}

// Companion session capability markers ((1737)=a, (1744)=a, (1740), (1750)).
// Kept as separate, additive predicates beside isLeadOnlyTool/roleAllowsTool
// per the shared gate surface constraint.
const (
	CompanionRoleBody = "companion-body"
	CompanionRoleWork = "companion-work"
)

// companionBodyBaseAllows is the fixed server-side base allowlist for Tier-1
// (companion-body) sessions at every posture ((1740)): ws/companion.*,
// mailbox.*, and read-only discovery with zero ws/note.* access (D3, D8).
// No lead-level mutating tool is reachable.
func companionBodyBaseAllows(name string) bool {
	// The guide subset of ws/daemon.* (tabs, dom, highlight, monitor) is
	// reachable here; daemonControlCallRefusal still requires an open
	// top-level body record, and phone posture still locks it.
	if isCompanionTool(name) || strings.HasPrefix(name, "mailbox.") || isDaemonGuideTool(name) {
		return true
	}
	switch name {
	case "call", "workflow_manual", "workflow_state", "runtime.read",
		"git.status", "git.diff", "git.log", "git.merge_base", "git.followups",
		"tickets.query", "tickets.verify",
		"agenda.list", "todo.list", "todo.read",
		"api.list", "project.tree", "rationale.query", "review.marker",
		"config.get", "config.list", "config.resolve_agent",
		"infra.read", "convention.read", "playbook.read",
		dashboardIntakeTool, dashboardInboxDrainTool:
		return true
	default:
		return false
	}
}

// companionBodyPhonePostureAllows is the strict allowlist enforced for
// companion-body while holding the smartphone ((1740), D8): phone_* tools,
// interruptible pacing (companion.act), active memory tools
// (remember_soul/remember_episode/recall_memory), mailbox.*, and minimal
// read-only discovery (call, workflow_manual, workflow_state). Every other
// tool, including companion.delegate_work, is a work tool and is blocked with
// posture_locked.
func companionBodyPhonePostureAllows(name string) bool {
	if isCompanionPhoneTool(name) || isCompanionPacingOrMemoryTool(name) || strings.HasPrefix(name, "mailbox.") {
		return true
	}
	switch name {
	case "call", "workflow_manual", "workflow_state", dashboardIntakeTool, dashboardInboxDrainTool:
		return true
	default:
		return false
	}
}

// companionMarkerAllowsTool enforces the capability marker rules for
// companion-body, companion-work, and unmarked sessions ((1737)=a, (1740),
// (1750)). Unmarked sessions keep lead-scoped companion.{speak,face,motion}
// while never seeing or reaching companion.phone_*, companion.delegate_work,
// or delegation.report.
func (s *Server) companionMarkerAllowsTool(entry sessionEntry, name string) string {
	switch entry.companionRole {
	case "":
		if isCompanionBodyOnlyTool(name) || isDelegationReportTool(name) {
			return fmt.Sprintf("tool not available in current %s MCP profile: %s", s.runtimeNamespace(), name)
		}
		return ""
	case CompanionRoleBody:
		if !companionBodyBaseAllows(name) {
			return fmt.Sprintf("tool not available in companion-body %s MCP profile: %s", s.runtimeNamespace(), name)
		}
		return ""
	case CompanionRoleWork:
		if isCompanionTool(name) || sessionHireTool(name) || userCommandTool(name) {
			return fmt.Sprintf("tool not available in companion-work %s MCP profile: %s", s.runtimeNamespace(), name)
		}
		return ""
	default:
		return fmt.Sprintf("tool not available in current %s MCP profile: %s", s.runtimeNamespace(), name)
	}
}

// companionWorkAllowsWithoutApproval is the fail-closed low-impact allowlist
// for companion-work ((1750)). Any tool not explicitly allowed here (such as
// global config writes via config.tune, cross-session control, whiteboard/
// tracker mutations, tickets.index_init, or future unlisted mutating tools)
// requires 1-click user approval.
func companionWorkAllowsWithoutApproval(name string, args map[string]any) bool {
	if isDelegationReportTool(name) || isAgentTool(name) || strings.HasPrefix(name, "mailbox.") {
		return true
	}
	if strings.HasPrefix(name, "exec.") || isFSReadTool(name) || isFSWriteTool(name) || isWebFetchTool(name) {
		return true
	}
	if strings.HasPrefix(name, "worktree.") || strings.HasPrefix(name, "todo.") || strings.HasPrefix(name, "agenda.") {
		return true
	}
	if strings.HasPrefix(name, "tickets.") {
		return name != "tickets.index_init"
	}
	if strings.HasPrefix(name, "git.") {
		return true
	}
	if isNoteMutationTool(name) {
		// Only session-local note state runs without approval; repo/clone/global
		// note mutations are shared state and fail closed.
		layer, _ := args["layer"].(string)
		return strings.EqualFold(strings.TrimSpace(layer), "session")
	}
	switch name {
	case "call", "workflow_manual", "workflow_state", "runtime.read", "runtime.debug_events",
		"config.get", "config.list", "config.resolve_agent",
		"route.resolve_implement", "playbook.read", "playbook.render",
		"infra.read", "convention.read", "api.list", "project.tree",
		"rationale.query", "review.marker", "review.stamp",
		"path.generate", "path.write", "note.query", "compact", "session.children",
		dashboardIntakeTool, dashboardInboxDrainTool:
		return true
	default:
		return false
	}
}

func formatCompanionDeniedError(tool, reason, message string) string {
	payload := struct {
		Error   string `json:"error"`
		Reason  string `json:"reason,omitempty"`
		Tool    string `json:"tool"`
		Message string `json:"message"`
	}{
		Error:   "denied",
		Reason:  reason,
		Tool:    tool,
		Message: message,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "denied: " + message
	}
	return string(raw)
}

func clampRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}

func looksSensitiveFieldName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, needle := range []string{"token", "secret", "password", "passwd", "api_key", "apikey", "credential", "auth", "cookie", "bearer", "private_key"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func companionApprovalSummary(name string, args map[string]any) string {
	var parts []string
	sensitiveKey := false
	for _, field := range []string{"key", "scope", "layer", "action", "topic", "id", "slug", "target", "name"} {
		if raw, ok := args[field]; ok {
			if s, ok := raw.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					if (field == "key" || field == "name") && looksSensitiveFieldName(s) {
						sensitiveKey = true
					}
					parts = append(parts, field+"="+clampRunes(wsdashboard.RedactCredentials(s), 48))
				}
			}
		}
	}
	if rawVal, ok := args["value"]; ok && rawVal != nil {
		var valStr string
		switch v := rawVal.(type) {
		case string:
			valStr = strings.TrimSpace(v)
		case bool, float64, int, int64:
			valStr = fmt.Sprint(v)
		}
		if valStr != "" {
			if sensitiveKey {
				parts = append(parts, "value=[redacted]")
			} else {
				parts = append(parts, "value="+clampRunes(wsdashboard.RedactCredentials(valStr), 48))
			}
		}
	}
	if len(parts) == 0 {
		return clampRunes(name, 180)
	}
	return clampRunes(name+" ("+strings.Join(parts, ", ")+")", 180)
}

// companionWorkApprovalGate enforces the (1750) high-impact confirmation gate
// at MCP dispatch for companion-work sessions via the shared top-level shell
// 1-click approval banner ((1728)=a, (1742), (1750)). Read-only and allowlisted
// tools execute immediately; ws/daemon.* tools run their own server-classified
// 1-click banner with CompanionPause=true inside DaemonCurl/DaemonUI (avoiding
// double-prompting). Any other high-impact lead tool blocks until approved or
// fails closed with structured error="denied" (reject, dismiss, timeout, or no
// active tab) and zero side effects.
func (s *Server) companionWorkApprovalGate(ctx context.Context, entry sessionEntry, found bool, name string, args map[string]any) string {
	if !found || entry.companionRole != CompanionRoleWork {
		return ""
	}
	if isDaemonControlTool(name) {
		return ""
	}
	if companionWorkAllowsWithoutApproval(name, args) {
		return ""
	}
	if s.daemonControl == nil {
		return formatCompanionDeniedError(name, "no_active_tab", fmt.Sprintf("%s denied: no dashboard confirmation host is available.", name))
	}
	res := s.daemonControl.RequestDaemonConfirmation(ctx, wsdashboard.DaemonConfirmRequest{
		Tool:           name,
		Summary:        companionApprovalSummary(name, args),
		CompanionPause: true,
	})
	if res.Approved {
		return ""
	}
	reason := strings.TrimSpace(res.Reason)
	if reason == "" {
		reason = "denied"
	}
	return formatCompanionDeniedError(name, reason, fmt.Sprintf("%s denied (%s) by 1-click user confirmation gate.", name, reason))
}

func workflowPreferenceWriterTool(name string) bool {
	entry, ok := configKeyEntryForTool(name)
	return ok && entry.RequiresLeadAuthority
}

// wsNamespaceRef matches the ws namespace prefix token (ws/ or ws:) anchored at
// a word boundary so that words containing "ws" as an interior substring (e.g.
// "news/", "rows:", "workflows/") are never mangled.
var wsNamespaceRef = regexp.MustCompile(`\bws([/:])`)

func LeadToolNames() []string { return leadToolNames(os.Getenv) }

// LeadToolNamesFromEnv reports the runtime inventory under a connecting
// caller's product mode, without applying a session's tool-profile filter.
func LeadToolNamesFromEnv(env []string) []string { return leadToolNames(newProcEnv(env).get) }

func leadToolNames(getenv func(string) string) []string {
	names := make([]string, 0, len(tools()))
	for _, tool := range tools() {
		name, _ := tool["name"].(string)
		if permanentlyHiddenTool(name) || name == "call" {
			// call is connection-descriptor-only, not a default runtime tool.
			continue
		}
		if envFlagOn(getenv(envNoAgent)) && noAgentHiddenTool(name) {
			continue
		}
		if hiddenUnlessAllowlisted(name) && !parseAllowedTools(getenv(envAllowedTools))[name] {
			continue
		}
		if name != "" {
			names = append(names, name)
		}
	}
	// The launcher checks callable aliases against the shipped runtime
	// contract. They have no MCP definitions and stay out of tools/list.
	names = append(names, legacyPiToolNames...)
	sort.Strings(names)
	return names
}

func (s *Server) filteredTools() []map[string]any {
	var hostedEntry sessionEntry
	if hostedKey := s.hostedSessionKey(); hostedKey != "" {
		if entry, ok := s.lookupSession(hostedKey); ok {
			hostedEntry = entry
		}
	}
	return s.filteredToolsFor(hostedEntry, true)
}

func (s *Server) filteredToolsFor(markerEntry sessionEntry, applyMarker bool) []map[string]any {
	namespace := s.runtimeNamespace()
	descriptor := s.connectionDescriptor()
	base := tools()
	filtered := make([]map[string]any, 0, len(base))
	for _, tool := range base {
		name, _ := tool["name"].(string)
		if permanentlyHiddenTool(name) && !s.cutoverListed(name) {
			continue
		}
		if userCommandTool(name) && !s.userCommandsListed() {
			// Advertised only to an authenticated open hosted lead: its
			// trusted binding, not a supplied key, names the session.
			continue
		}
		if isDaemonControlTool(name) && !s.daemonControlListedFor(name) {
			// Advertised only to an opted-in top-level hosted lead whose
			// persisted && effective dashboard_control_access are both true,
			// to companion-work, and (guide tools only) to companion-body.
			continue
		}
		if isConvTool(name) && !s.convToolsListed() {
			// Advertised only to a hosted lead whose start-time snapshot
			// enabled conv_history_access.
			continue
		}
		if isAgentTool(name) {
			// Listed where a runner answers them, the daemon's or the token
			// route's; the agentless product mode keeps them, since
			// delegation runs through them there too.
			if !s.agentListed() {
				continue
			}
			if s.workerForwards() {
				tool = agentWorkerTool(tool)
			}
		}
		if name == "compact" && !s.compactAdvertisesLeadSummary() {
			tool = compactToolWithoutLeadSummary(tool)
		}
		if applyMarker && s.companionMarkerAllowsTool(markerEntry, name) != "" {
			continue
		}
		if s.toolAllowed(name) {
			definition := publicToolDefinition(tool, descriptor.aliases.advertised(name), namespace)
			if descriptor.toolsAlwaysLoad {
				meta := make(map[string]any)
				if existing, ok := definition["_meta"].(map[string]any); ok {
					for key, value := range existing {
						meta[key] = value
					}
				}
				// Retained name check: the _meta key is Antigravity's own, read by its client
				// (TestHostedAgyToolsAlwaysLoad).
				meta["antigravity/alwaysLoad"] = true
				definition["_meta"] = meta
			}
			filtered = append(filtered, definition)
		}
	}
	return filtered
}

// callDiscoveryTools narrows tools/list's advertised surface to the caller's
// authority. Reuse the identity already read by callTool, not one key lookup
// per candidate. With no recognised key, use the most restrictive role so
// omitting a key cannot disclose a role-gated tool's name or schema.
func (s *Server) callDiscoveryTools(key string, entry sessionEntry, found bool) []map[string]any {
	if !found {
		entry = sessionEntry{scope: roleLeaf}
	}
	aliases := s.connectionDescriptor().aliases
	var visible []map[string]any
	for _, tool := range s.filteredToolsFor(entry, false) {
		name := aliases.canonical(tool["name"].(string))
		if s.permissionRefusal(name, nil) != "" {
			continue
		}
		if _, refusal := s.sessionToolAccess(name, key, entry); refusal != "" {
			continue
		}
		if s.companionPostureRefusal(name, key, entry, found) != "" {
			continue
		}
		if s.convAccessRefusal(name) != "" {
			continue
		}
		visible = append(visible, tool)
	}
	return visible
}

// sessionToolAccess is the shared keyed capability gate for execution and
// call discovery. A successful sub-lead board access also supplies the board
// rewrite execution needs; discovery never runs a handler or writes a board.
func (s *Server) sessionToolAccess(name, key string, entry sessionEntry) (*subleadBoard, string) {
	if refusal := s.companionMarkerAllowsTool(entry, name); refusal != "" {
		return nil, refusal
	}
	// A forwarding worker's delegation tools use its token, not its key.
	workerByToken := (isPiTool(name) || isAgentTool(name)) && s.workerForwards()
	if entry.scope != roleLead && !workerByToken && (isLeadOnlyTool(name) || !roleAllowsTool(entry.scope, name)) {
		msg := fmt.Sprintf("tool not available in current %s MCP profile: %s", s.runtimeNamespace(), name)
		if entry.scope == roleDelegate && isLeadOnlyTool(name) {
			// A spawn key is delegate-scoped by design; the lead-scoped key a
			// worker-role render spliced in is the only one a lead-only tool
			// admits.
			msg += "; this key is delegate-scoped; a lead-only tool needs the lead-scoped key a worker-role playbook.render spliced into your prompt."
		}
		return nil, msg
	}
	if isNoteMutationTool(name) && !entry.isTopLevelLead() {
		return nil, noteReadOnlyDenial(name)
	}
	if isDaemonControlTool(name) {
		if !entry.isTopLevelLead() {
			return nil, daemonControlLeadOnlyDenial(name)
		}
		if refusal := s.daemonControlCallRefusal(name); refusal != "" {
			return nil, refusal
		}
	}
	if name == whiteboardLoadTool && !entry.isTopLevelLead() {
		return nil, whiteboardLeadOnlyDenial(name)
	}
	if isWhiteboardTool(name) && !entry.isTopLevelLead() {
		board, marked := s.subleadBoardFor(key, entry)
		if !marked {
			return nil, whiteboardLeadOnlyDenial(name)
		}
		return &board, ""
	}
	// Support conversations may reach only their profile's tracker/wiki tools.
	if (isTrackerTool(name) || isWikiTool(name) || name == trackerShowTool || name == trackerUITool) && !entry.isTopLevelLead() {
		admitted, err := s.trackerSupportAdmits(key, entry, name)
		if err != nil {
			return nil, err.Error()
		}
		if !admitted {
			return nil, trackerLeadOnlyDenial(name)
		}
	}
	return nil, ""
}

func publicToolDefinition(tool map[string]any, advertisedName, namespace string) map[string]any {
	clone := make(map[string]any, len(tool))
	for key, value := range tool {
		clone[key] = value
	}
	if advertisedName != "" {
		clone["name"] = advertisedName
	}
	if description, _ := clone["description"].(string); description != "" {
		clone["description"] = namespaceText(description, namespace)
	}
	if schema, ok := clone["inputSchema"].(map[string]any); ok {
		clone["inputSchema"] = namespaceValue(schema, namespace)
	}
	return clone
}

func (s *Server) toolAllowed(name string) bool {
	// Only a descriptor opting into fallback exposes the envelope. The
	// inner tool is still checked separately, including its allowlist.
	if name == "call" {
		return s.connectionDescriptor().callFallback
	}
	if s.noAgentMode() && noAgentHiddenTool(name) {
		return false
	}
	if allowed := s.explicitAllowedTools(); len(allowed) > 0 {
		return allowed[name]
	}
	return !hiddenUnlessAllowlisted(name)
}

// isNoteMutationTool reports whether name changes note state every session
// sees. Mute and unmute count: they change what every Notes block shows.
func isNoteMutationTool(name string) bool {
	switch name {
	case "note.write", "note.erase", "note.mute", "note.unmute":
		return true
	default:
		return false
	}
}

// noteReadOnlyDenial is the refusal a spawned session gets for a note
// mutation. It names the hand-off, because the Notes footer tells every
// reader to erase stale notes and a worker must learn that erasing is its
// lead's job.
func noteReadOnlyDenial(name string) string {
	return fmt.Sprintf("%s is not available to a spawned session: notes are read-only here (note.query still works). Put the note's content (key, value, and the layer it needs) in your final report so your lead can write it; erasing, muting, and unmuting notes is also your lead's job.", name)
}

func roleAllowsTool(role toolRole, name string) bool {
	switch role {
	case roleLead:
		return true
	case roleDelegate:
		if strings.HasPrefix(name, "session.") {
			return false
		}
		// config.get is a read-only single-key read; a child must reach it to
		// inherit its lead's session-scope values. config.resolve_agent is a
		// read-only tier resolution a worker playbook runs before each spawn.
		return name == "config.get" || name == "config.resolve_agent" || !strings.HasPrefix(name, "config.")
	case roleLeaf:
		if name == "config.get" {
			return true
		}
		return !strings.HasPrefix(name, "config.") && !strings.HasPrefix(name, "session.") && name != "git.commit"
	default:
		return false
	}
}

func namespaceText(text, namespace string) string {
	if namespace == "ws" {
		return text
	}
	return namespaceTerms(text, namespace)
}

func namespaceTerms(text, namespace string) string {
	text = wsNamespaceRef.ReplaceAllString(text, namespace+"$1")
	for _, term := range []string{"MCP", "plugin", "project", "runtime", "tool", "tools", "workflow", "user", "agent", "agents"} {
		pattern := regexp.MustCompile(`\bws ` + regexp.QuoteMeta(term) + `\b`)
		text = pattern.ReplaceAllString(text, namespace+" "+term)
	}
	for _, term := range []string{"managed", "owned"} {
		pattern := regexp.MustCompile(`\bws-` + regexp.QuoteMeta(term) + `\b`)
		text = pattern.ReplaceAllString(text, namespace+"-"+term)
	}
	return text
}

func namespaceValue(value any, namespace string) any {
	switch typed := value.(type) {
	case string:
		return namespaceText(typed, namespace)
	case map[string]any:
		clone := make(map[string]any, len(typed))
		for key, child := range typed {
			clone[key] = namespaceValue(child, namespace)
		}
		return clone
	case map[string]string:
		clone := make(map[string]string, len(typed))
		for key, child := range typed {
			clone[key] = namespaceText(child, namespace)
		}
		return clone
	case []any:
		clone := make([]any, len(typed))
		for i, child := range typed {
			clone[i] = namespaceValue(child, namespace)
		}
		return clone
	case []string:
		clone := make([]string, len(typed))
		for i, child := range typed {
			clone[i] = namespaceText(child, namespace)
		}
		return clone
	default:
		return value
	}
}

// permanentlyHiddenTool reports a tool no ordinary inventory lists: the exec
// tools and the fs.* read tools, which only a cutover session's connection
// lists (cutoverListed).
func permanentlyHiddenTool(name string) bool {
	return strings.HasPrefix(name, "exec.") || isFSReadTool(name) || isFSWriteTool(name) || isWebFetchTool(name)
}

func noAgentHiddenTool(name string) bool {
	if permanentlyHiddenTool(name) {
		return true
	}
	// A config.* knob declaring NoAgentVisible: false is hidden on the
	// agentless surface. Every other config.* knob, including the bootstrap
	// tool, stays visible (wsflow still needs session-key bootstrap).
	if strings.HasPrefix(name, "config.") {
		if entry, ok := configKeyEntryForTool(name); ok {
			return !entry.NoAgentVisible
		}
	}
	return false
}

func parseAllowedTools(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	allowed := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name != "" {
			allowed[name] = true
		}
	}
	return allowed
}
