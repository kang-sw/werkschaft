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
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/kang-sw/devenv/internal/wsusercmd"
)

// UserCommandService is the dashboard's narrow dependency on the user commands
// of an open hosted lead, bound by the command layer. The worktree scope comes
// from the runner's state, never from the browser. Reads are memory only.
type UserCommandService interface {
	// List is the lead's commands in its bound worktree and the opaque revision
	// they carry, from one read (the revision always belongs to the list). It
	// reads memory only.
	List(replyID string) (commands []wsusercmd.Command, rev string, err error)
	// Get returns one command of that scope and the worktree it was read for.
	Get(replyID, id string) (cmd wsusercmd.Command, root string, found bool, err error)
	// RemoveIf withdraws a command only while it has the revision the user was
	// shown, decided together with the removal: wsusercmd.ErrStale when it
	// changed, wsusercmd.ErrUnknown when it is gone. (The agent's unregister
	// tool withdraws by ID and does not come through here.)
	RemoveIf(replyID, id string, rev int64) error
	// Revision is the opaque revision of the lead's commands and worktree: it
	// differs after every change.
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
	cmds, rev, err := users.List(replyID)
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
	// One ordering point: the known-card, fence and token answers, the check
	// that the command still has the displayed revision, and the claim happen
	// under the composer's admission lock (composerAdmitSlot). A retry, a fenced
	// id or a known card answers without ever looking the command up, so a click
	// whose response was lost maps to its one job (or reports the outcome
	// unknown) even when the command changed or was withdrawn since, and never
	// launches.
	slot := &composerSlot{id: id, revision: req.Revision}
	var refusal string
	var lookupErr error
	resolve := func() (string, string, bool) {
		cmd, root, found, err := users.Get(replyID, id)
		switch {
		case err != nil:
			lookupErr = err
			return "", "", false
		case !found:
			refusal = "withdrawn"
			return "", "", false
		case cmd.Revision != req.Revision:
			refusal = "stale"
			return "", "", false
		}
		slot.label = cmd.Label
		return cmd.Line, root, true
	}
	sub, admission := d.composerAdmitSlot(replyID, req.SubmissionID, "", gen, req.Retry, req.Token, slot, resolve)
	if admission == composerRefused {
		if lookupErr != nil {
			http.Error(w, "unknown session", http.StatusNotFound)
			return
		}
		d.userCommandList(w, users, replyID, refusal, http.StatusConflict)
		return
	}
	line := ""
	if sub != nil {
		sub.mu.Lock()
		line = sub.line
		sub.mu.Unlock()
	}
	d.composerAnswer(w, svc, sub, admission, req.SubmissionID, line, slot)
}

// userCommandRemove is the user's own withdrawal of command id. The displayed
// revision is required: the removal is made only while the command still has it
// (decided under the registry's writer lock together with the removal), and
// otherwise the user is shown the current list first. The agent's unregister
// tool withdraws by ID and does not come through here.
func (d *Daemon) userCommandRemove(w http.ResponseWriter, r *http.Request, users UserCommandService, replyID, id string) {
	if !wsusercmd.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	var req userCommandRequest
	if !userCommandBody(w, r, &req) {
		return
	}
	if req.Revision <= 0 {
		http.Error(w, "the displayed revision is required", http.StatusBadRequest)
		return
	}
	switch err := users.RemoveIf(replyID, id, req.Revision); {
	case err == nil:
		d.userCommandList(w, users, replyID, "", http.StatusOK)
	case errors.Is(err, wsusercmd.ErrStale):
		d.userCommandList(w, users, replyID, "stale", http.StatusConflict)
	case errors.Is(err, wsusercmd.ErrUnknown):
		d.userCommandList(w, users, replyID, "withdrawn", http.StatusConflict)
	default:
		http.Error(w, "could not remove the command: "+err.Error(), http.StatusInternalServerError)
	}
}
