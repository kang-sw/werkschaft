// composer_usercmd.go is the dashboard side of the user commands (the "command
// slots"): bash lines an open hosted lead registered, listed to the user in
// that lead's chat, each with a Run click that runs the stored line through the
// composer's `!` path (composer_exec.go) as a one-use approval of the revision
// the user was shown, and a remove control. Every route here is localhost-only
// behind the composer's gate; the lists are served from memory.
package wsdashboard

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/kang-sw/devenv/internal/wsusercmd"
)

// UserCommandService is the dashboard's narrow dependency on the user commands
// of an open hosted lead, bound by the command layer. The worktree scope comes
// from the runner's state, never from the browser. Reads are memory only.
type UserCommandService interface {
	// List is the lead's commands in its bound worktree and their opaque
	// revision. fresh reads the worktree afresh; otherwise a recent read of it
	// may serve (the page load and the list refetch touch no file).
	List(replyID string, fresh bool) (commands []wsusercmd.Command, rev string, err error)
	// Get returns one command of that scope.
	Get(replyID, id string) (wsusercmd.Command, bool, error)
	// Remove withdraws a command, the service the agent's unregister tool
	// calls; false when there was none.
	Remove(replyID, id string) (bool, error)
	// Revision is the opaque revision of the lead's commands (any worktree):
	// it differs after every change.
	Revision(replyID string) string
}

// SetUserCommands binds the user commands' service; before it the routes
// answer unavailable and no stream sends the revision event.
func (d *Daemon) SetUserCommands(svc UserCommandService) {
	if svc == nil {
		d.userCmds.Store(nil)
		return
	}
	d.userCmds.Store(&svc)
}

// UserCommandsChanged wakes the open page streams after a change to a lead's
// commands: each stream compares the lead's revision with the one it last sent
// and sends the opaque "usercmd" event when it differs, which makes the page
// refetch its list. Safe from any goroutine.
func (d *Daemon) UserCommandsChanged(replyID string) {
	if d.broker == nil {
		return
	}
	d.broker.signalAll()
}

// userCommandRev is the opaque revision a localhost session stream sends, or ""
// when there is none to send.
func (d *Daemon) userCommandRev(replyID string) string {
	svc := d.userCmds.Load()
	if svc == nil || replyID == "" {
		return ""
	}
	return (*svc).Revision(replyID)
}

// userCommandRoute is the request body of a click and a removal: the revision
// the page displayed, plus a click's submission fields.
type userCommandRequest struct {
	SubmissionID string `json:"submission_id"`
	Revision     int64  `json:"revision"`
	Retry        bool   `json:"retry"`
	Token        string `json:"token"`
}

type userCommandList struct {
	Commands []wsusercmd.Command `json:"commands"`
	Rev      string              `json:"rev"`
	// Token is the composer's admission token (composerExec.token), so a click
	// needs no further read.
	Token string `json:"token,omitempty"`
	// Error names why a write was refused with this list attached: stale (the
	// definition changed since the page displayed it) or withdrawn.
	Error string `json:"error,omitempty"`
}

func (d *Daemon) handleUserCommands(w http.ResponseWriter, r *http.Request, replyID, rest string) {
	_, gen, write, ok := d.composerGate(w, r, replyID)
	if !ok {
		return
	}
	usvc := d.userCmds.Load()
	if usvc == nil {
		http.Error(w, "user commands are unavailable", http.StatusServiceUnavailable)
		return
	}
	users := *usvc
	rest = strings.TrimPrefix(rest, "/")
	switch {
	case rest == "" && !write:
		d.userCommandList(w, users, replyID, "", http.StatusOK)
	case write && rest != "":
		id, action, _ := strings.Cut(rest, "/")
		switch action {
		case "run":
			d.userCommandRun(w, r, users, replyID, id, gen)
		case "remove":
			d.userCommandRemove(w, r, users, replyID, id)
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

// userCommandList answers the lead's list; errText and status attach a refused
// write's reason to it.
func (d *Daemon) userCommandList(w http.ResponseWriter, users UserCommandService, replyID, errText string, status int) {
	// A list that answers a refused write is read afresh; a plain read is not.
	cmds, rev, err := users.List(replyID, errText != "" || status != http.StatusOK)
	if err != nil {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	if cmds == nil {
		cmds = []wsusercmd.Command{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, userCommandList{Commands: cmds, Rev: rev, Token: d.composer.token(), Error: errText})
}

// userCommandBody reads one JSON object with no field this route does not
// take; it answers the refusal itself.
func userCommandBody(w http.ResponseWriter, r *http.Request, into *userCommandRequest) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, composerBodyLimit+1))
	if err != nil || len(raw) > composerBodyLimit {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		http.Error(w, "malformed request", http.StatusBadRequest)
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "malformed request", http.StatusBadRequest)
		return false
	}
	return true
}

// userCommandRun runs the stored line of command id through the composer's
// admission. The browser names the command and the revision it displayed and
// never sends text: the line that runs is the stored one, and only when its
// revision is still the displayed one.
func (d *Daemon) userCommandRun(w http.ResponseWriter, r *http.Request, users UserCommandService, replyID, id string, gen uint64) {
	svcp := d.composer.svc.Load()
	if svcp == nil || !wsusercmd.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	var req userCommandRequest
	if !userCommandBody(w, r, &req) {
		return
	}
	if !composerSubmissionID.MatchString(req.SubmissionID) {
		http.Error(w, "a submission id is required", http.StatusBadRequest)
		return
	}
	svc := *svcp
	// An admission already made, or one an unknown outcome stands for, answers
	// before the command is looked up: a retry of a click whose response was
	// lost must map to its one job (or report the outcome unknown) even when the
	// command has changed or been withdrawn since, and never launch.
	slot := &composerSlot{id: id, revision: req.Revision}
	if sub, fenced := d.composerPeek(replyID, req.SubmissionID); sub != nil || fenced || req.Retry {
		sub, admission := d.composerAdmitSlot(replyID, req.SubmissionID, "", gen, true, req.Token, slot)
		d.composerAnswer(w, svc, sub, admission, req.SubmissionID, "", slot)
		return
	}
	cmd, found, err := users.Get(replyID, id)
	if err != nil {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	switch {
	case !found:
		d.userCommandList(w, users, replyID, "withdrawn", http.StatusConflict)
		return
	case cmd.Revision != req.Revision:
		d.userCommandList(w, users, replyID, "stale", http.StatusConflict)
		return
	}
	slot.label = cmd.Label
	sub, admission := d.composerAdmitSlot(replyID, req.SubmissionID, cmd.Line, gen, false, req.Token, slot)
	d.composerAnswer(w, svc, sub, admission, req.SubmissionID, cmd.Line, slot)
}

// userCommandRemove is the user's own withdrawal of command id: the service the
// agent's unregister tool calls. A revision the page displayed that is no longer
// current is stale (the user is shown the new definition first).
func (d *Daemon) userCommandRemove(w http.ResponseWriter, r *http.Request, users UserCommandService, replyID, id string) {
	if !wsusercmd.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	var req userCommandRequest
	if !userCommandBody(w, r, &req) {
		return
	}
	if req.Revision != 0 {
		cmd, found, err := users.Get(replyID, id)
		if err != nil {
			http.Error(w, "unknown session", http.StatusNotFound)
			return
		}
		if found && cmd.Revision != req.Revision {
			d.userCommandList(w, users, replyID, "stale", http.StatusConflict)
			return
		}
	}
	if _, err := users.Remove(replyID, id); err != nil {
		http.Error(w, "could not remove the command: "+err.Error(), http.StatusInternalServerError)
		return
	}
	d.userCommandList(w, users, replyID, "", http.StatusOK)
}
