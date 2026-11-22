package wsagent

import (
	"strings"

	"github.com/kang-sw/devenv/internal/claudeagents"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// claudePermissionPolicy maps the common levels to Claude's native modes. A
// session with no selection derives its level from the recorded mode; the
// omitted mode runs as the user's default, which asks before edits.
func claudePermissionPolicy() PermissionPolicy {
	return PermissionPolicy{
		NativeModes: map[PermissionLevel]string{
			PermissionReadOnly:     "plan",
			PermissionWorkdirWrite: "acceptEdits",
			PermissionFull:         "bypassPermissions",
		},
		LegacyModes: map[string]PermissionLevel{
			"plan":              PermissionReadOnly,
			"bypassPermissions": PermissionFull,
			"":                  PermissionWorkdirWrite,
			"auto":              PermissionWorkdirWrite,
			"default":           PermissionWorkdirWrite,
			"acceptEdits":       PermissionWorkdirWrite,
			"dontAsk":           PermissionWorkdirWrite,
		},
	}
}

// claudeConnectionPolicy: a Claude client names itself, and a conversation the
// daemon launched without the native subagent tool delegates through
// agent.spawn.
func claudeConnectionPolicy() ConnectionPolicy {
	return ConnectionPolicy{
		NamedClient: true,
		DelegatesThroughDaemon: func(rec Record, _ func() *Profile) bool {
			return RemovesSubagents(rec)
		},
	}
}

// claudeDashboardTraits: Claude's live snapshots report the account's usage,
// its snapshot rows without a daemon identity are its own subagents, an
// ended terminal session can be taken over, and the daemon's replayed
// initialize presents the CLI's client name.
func claudeDashboardTraits() wsdashboard.HarnessTraits {
	return wsdashboard.HarnessTraits{
		PushesClaudeUsage:  true,
		UntaggedRowsNative: true,
		TerminalTakeover:   true,
		ReplayClientName:   "claude-code",
	}
}

// claudeSpawnIdiom is the Claude SpawnIdiom value. Claude's Agent tool takes a
// per-call model but no reasoning-effort parameter; effort comes only from the
// subagent definition, so the plugin ships one effort-carrier agent per Claude
// level (<namespace>:effort-<level>). The value carries the whole mapping rule
// because workers spawn from playbook.render bindings and config.resolve_agent
// output without ever reading the lead-only native-spawn-binding overlay. It is
// substituted mid-sentence at every {{.SpawnIdiom}} site, so it stays one
// clause. The namespace is assembled here rather than written as a
// {{.SkillNamespace}} literal: the renderer never re-scans replacement
// values. The level list is the generator's own, so it names exactly the
// agents the packages ship.
func claudeSpawnIdiom(namespace string) string {
	levels := claudeagents.Levels
	levelList := strings.Join(levels[:len(levels)-1], ", ") + ", or " + levels[len(levels)-1]
	return `Agent({subagent_type: "` + namespace + `:effort-<resolved effort>", model: <resolved model>, ...})` +
		` (subagent_type "general-purpose" when the resolved effort is not exactly ` + levelList + `)`
}

var _ auditSource = (*claudeMapper)(nil)

func (*claudeMapper) newAudit(sessionID string) (string, auditAdapter) {
	return BackendClaude, newClaudeAudit(sessionID)
}

var _ terminalSession = ClaudeBackend{}

func (ClaudeBackend) terminalResume(rec Record, _ string) string {
	if rec.SessionID == "" {
		return ""
	}
	return resumeLine(rec.workDir(), "claude", "--resume", rec.SessionID)
}

func (ClaudeBackend) checkTerminalSession(string) error { return nil }

// adoptTerminalSession keeps the profile's native permission mode: a Claude
// terminal session resumes under it.
func (ClaudeBackend) adoptTerminalSession(rec *Record, session string) {
	rec.SessionID = session
}
