package wsagent

import (
	"net/http"
)

var (
	_ terminalSession    = AgyBackend{}
	_ nativeModeAdmitter = AgyBackend{}
)

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
