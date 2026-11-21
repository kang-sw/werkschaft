package wsdashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SessionVisibilityFile is dashboard-owned state, outside both typed rewrite
// domains (agent.json and registration.json). It shares the session's existing
// directory/prune lifetime; old runtimes may display the row normally.
const SessionVisibilityFile = "visibility.json"

// SessionLifecycle is the dashboard's non-visual lifecycle projection. It is
// distinct from registration heartbeat liveness and introduces no new timer.
type SessionLifecycle string

const (
	SessionActiveOn        SessionLifecycle = "active_on"
	SessionAsleep          SessionLifecycle = "asleep"
	SessionArchived        SessionLifecycle = "archived"
	SessionStopped         SessionLifecycle = "stopped"
	SessionPendingDeletion SessionLifecycle = "pending_deletion"
	// SessionChildWaiting is a spawned child agent asleep with no turn: mail
	// wakes it, and it is deleted after agent.child_waiting_days without
	// activity (internal/wsagent agent_tools.go). The runner's State
	// projects it; registrations never derive it.
	SessionChildWaiting SessionLifecycle = "child_waiting"
)

// DeriveSessionLifecycle uses runner state for hosted leads: their heartbeat
// can age while the machine sleeps and their holders keep running. EndedAt on
// a hosted lead records its conversation close. Terminal handoff clears Hosted,
// and an external MCP shutdown or worker retirement can also set EndedAt.
func DeriveSessionLifecycle(reg Registration, hidden bool, now time.Time) SessionLifecycle {
	if !reg.Hosted {
		if !reg.Live(now) {
			return SessionStopped
		}
		return SessionActiveOn
	}
	if reg.EndedAt != "" {
		// A rolled-back hire also closes its uncommitted lead. Fold that
		// cancelled creation with manually deleted leads.
		return SessionPendingDeletion
	}
	if hidden {
		return SessionArchived
	}
	if !reg.HostStopped {
		return SessionActiveOn
	}
	switch reg.HostExitReason {
	case "stop", "killed":
		return SessionAsleep
	case "":
		// A hosted lead can have no process before its first launch.
		if reg.HostStoppedAt == "" && reg.HostExitCode == nil && reg.HostExitSignal == nil {
			return SessionAsleep
		}
	}
	return SessionStopped
}

// ReadSessionHidden restores archived visibility. Absence preserves legacy
// visibility; callers must not turn a read error into permission to launch.
func ReadSessionHidden(dir, replyID string) (bool, error) {
	if !ValidReplyID(replyID) {
		return false, fmt.Errorf("invalid reply-id %q", replyID)
	}
	raw, err := os.ReadFile(filepath.Join(SessionDir(dir, replyID), SessionVisibilityFile))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var state struct {
		Hidden bool `json:"hidden"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return false, err
	}
	return state.Hidden, nil
}

// WriteSessionHidden atomically replaces only the dashboard's sidecar. A
// failed temporary write leaves the last good visibility untouched.
func WriteSessionHidden(dir, replyID string, hidden bool) error {
	if !ValidReplyID(replyID) {
		return fmt.Errorf("invalid reply-id %q", replyID)
	}
	sessionDir := SessionDir(dir, replyID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(struct {
		Hidden bool `json:"hidden"`
	}{hidden})
	if err != nil {
		return err
	}
	return writePrivateFile(sessionDir, SessionVisibilityFile, append(raw, '\n'))
}
