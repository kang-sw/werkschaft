package wsagent

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
