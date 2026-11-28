package wsdashboard

import (
	"path/filepath"

	"github.com/kang-sw/devenv/internal/wsinbox"
)

// ChangeHost optionally accepts hosted store invalidations without HTTP.
// handled is false when the host cannot serve this state directory.
type ChangeHost interface {
	NotifySessionChange(stateDir string, body NudgeBody) (port int, handled bool)
}

// NotifySessionChange schedules the same targeted lane used by HTTP nudges.
// Index.Nudge validates the registration and project as the HTTP route does;
// notification is not authorization to mutate a store. Unknown sessions
// still wake mail waits.
func (d *Daemon) NotifySessionChange(stateDir string, body NudgeBody) (int, bool) {
	if stateDir == "" || d.dir == "" || filepath.Clean(stateDir) != filepath.Clean(d.dir) {
		return 0, false
	}
	select {
	case <-d.stop:
		return 0, false
	default:
	}
	d.invalidateInboxNames(body.ReplyID)
	if d.mailWake != nil {
		d.mailWake.Nudge()
	}
	d.foreign.mailChanged()
	if d.index == nil || !d.index.Nudge(body.ReplyID, body.Project) {
		return 0, true
	}
	return d.endpoint.Port, true
}

// invalidateInboxNames expires wsinbox's cached-polling listings before a nudge
// wakes readers so external ws-mcp inbox writes and drains are observed on the
// woken read rather than waiting for the 5 s fallback window.
func (d *Daemon) invalidateInboxNames(replyID string) {
	if d.dir != "" && ValidReplyID(replyID) {
		inbox := wsinbox.Dir(SessionDir(d.dir, replyID))
		wsinbox.InvalidateNames(inbox, wsinbox.DrainedDir(inbox))
		return
	}
	wsinbox.InvalidateNames()
}
