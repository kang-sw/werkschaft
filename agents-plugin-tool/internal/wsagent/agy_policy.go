package wsagent

import (
	"net/http"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// agyDashboardTraits: the daemon's replayed initialize presents agy's client
// name. Agy's client is detected through its registration, not by name alone,
// and its usage has its own provider (plan_usage_agy.go).
func agyDashboardTraits() wsdashboard.HarnessTraits {
	return wsdashboard.HarnessTraits{ReplayClientName: "agy"}
}

var (
	_ terminalSession    = AgyBackend{}
	_ nativeModeAdmitter = AgyBackend{}
)

// Deprecated: Explicit terminal handoff is deprecated and scheduled for
// removal in 261124-refactor-remove-terminal-handoff.
func (AgyBackend) terminalResume(rec Record, _ string) string {
	if rec.SessionID == "" {
		return ""
	}
	return resumeLine(rec.workDir(), "agy", "--conversation", rec.SessionID)
}

func (AgyBackend) checkTerminalSession(string) error { return nil }

// adoptTerminalSession drops the profile's native mode: agy's modes are not
// the terminal session's.
func (AgyBackend) adoptTerminalSession(rec *Record, session string) {
	rec.PermissionMode = ""
	rec.SessionID = session
}

// admitNativeMode reserves the ws-side bypass to the user: an agent may keep it
// but not enable it.
func (AgyBackend) admitNativeMode(rec Record, mode string, user bool) error {
	if mode == "bypass" && !rec.agyBypass() && !user {
		return &requestError{status: http.StatusForbidden, msg: "only the user may enable agy bypass through permission_mode"}
	}
	return nil
}
