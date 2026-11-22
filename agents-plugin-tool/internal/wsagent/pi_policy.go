package wsagent

import (
	"os"
)

var _ terminalSession = PiBackend{}

// terminalResume names the session by its file: pi resolves a bare id only
// inside its own session directory, and a session the daemon started keeps its
// file in the daemon's.
func (PiBackend) terminalResume(rec Record, sessionDir string) string {
	file := rec.SessionFile
	if file == "" {
		file = piSessionFile(sessionDir)
		if _, err := os.Stat(file); err != nil {
			return ""
		}
	}
	return resumeLine(rec.workDir(), "pi", "--session", file)
}

// checkTerminalSession requires the session file to still be a regular file.
func (PiBackend) checkTerminalSession(session string) error {
	if info, err := os.Stat(session); err != nil || !info.Mode().IsRegular() {
		return conflict("the session's file is gone: %s", session)
	}
	return nil
}

// adoptTerminalSession runs the conversation on the terminal's own file and
// drops the profile's native mode, which Pi leads do not use.
func (PiBackend) adoptTerminalSession(rec *Record, session string) {
	rec.PermissionMode = ""
	rec.SessionFile = session
}
