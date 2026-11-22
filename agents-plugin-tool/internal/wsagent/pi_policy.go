package wsagent

import (
	"os"
	"slices"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// piConnectionPolicy: a Pi client names itself, its bridge extension
// identifies it by its own client name, the bridge may opt the client in to
// delegating through the daemon, and a conversation the daemon runs without
// the native extension does.
func piConnectionPolicy() ConnectionPolicy {
	return ConnectionPolicy{
		NamedClient:            true,
		BridgeClientName:       "ws-pi-bridge",
		ClientDaemonAgents:     true,
		DelegatesThroughDaemon: piDelegatesThroughDaemon,
	}
}

// piDashboardTraits: an ended Pi terminal session can be taken over, and the
// daemon's replayed initialize presents the bridge's client name, which the
// MCP server detects Pi from.
func piDashboardTraits() wsdashboard.HarnessTraits {
	return wsdashboard.HarnessTraits{TerminalTakeover: true, ReplayClientName: "ws-pi-bridge"}
}

var _ leadCompactionBridge = PiBackend{}

// bridgesLeadCompaction: a Pi lead with the native extension compacts through
// its bridge; one running MCP-only (PiMCPOnly) has none.
func (PiBackend) bridgesLeadCompaction(rec Record) bool { return !PiMCPOnly(rec, nil) }

// piDelegatesThroughDaemon reports whether rec runs without the native ws Pi
// extension (PiMCPOnly), so it connects through Pi's builtin MCP client,
// whose clientInfo is not the extension's bridge name and is not detected as
// Pi. A non-lead's launched profile must also grant agent.spawn and
// mailbox.send. Records written before the launched-profile snapshot fall
// back to the profile their name resolves to now (launched).
func piDelegatesThroughDaemon(rec Record, launched func() *Profile) bool {
	def := rec.ResolvedDefinition
	if def == nil && !rec.Lead && launched != nil {
		def = launched()
	}
	if !PiMCPOnly(rec, def) {
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

// The Pi extension's compaction wire markers: the capability its hello reports,
// the preparation marker the daemon appends to compaction guidance, and the
// argument that marks a daemon-started auto-compaction. They are bytes the
// bundled extension matches, so they live with the Pi adapter.
const (
	piCompactCapability  = "pi_compaction_preparation_v1"
	piCompactPreparation = "<!-- ws-pi-compaction-preparation -->"
	piDaemonAutoCompact  = "<!-- ws-pi-daemon-auto-compact -->"
)
