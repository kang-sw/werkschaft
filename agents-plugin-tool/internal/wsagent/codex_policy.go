package wsagent

import "github.com/kang-sw/devenv/internal/wsdashboard"

// codexConnectionPolicy: a Codex client names itself.
func codexConnectionPolicy() ConnectionPolicy { return ConnectionPolicy{NamedClient: true} }

// codexDashboardTraits: Codex's five-hour usage samples keep their own
// history, and the daemon's replayed initialize presents its client name.
func codexDashboardTraits() wsdashboard.HarnessTraits {
	return wsdashboard.HarnessTraits{SeparateFiveHourHistory: true, ReplayClientName: "codex"}
}

// codexPermissionPolicy maps the common levels to Codex's sandbox names. A
// launch with no selection keeps the user's own Codex settings, whose
// reported mode derives the display level only while the process lives.
func codexPermissionPolicy() PermissionPolicy {
	return PermissionPolicy{
		NativeModes: map[PermissionLevel]string{
			PermissionReadOnly:     "read-only",
			PermissionWorkdirWrite: "workspace-write",
			PermissionFull:         "danger-full-access",
		},
		LegacyModes: map[string]PermissionLevel{
			"read-only":          PermissionReadOnly,
			"workspace-write":    PermissionWorkdirWrite,
			"danger-full-access": PermissionFull,
		},
		LiveModeProjection: true,
	}
}

var _ auditSource = (*codexMapper)(nil)

func (*codexMapper) newAudit(sessionID string) (string, auditAdapter) {
	return BackendCodex, newCodexAudit(sessionID)
}
