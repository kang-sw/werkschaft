// composer_usercmd.go is the dashboard side of the user commands (the "command
// slots"): session-scoped commands registered by an open hosted lead ("agent")
// and project-root-persistent commands saved by the user ("user"), served from
// memory behind the localhost-only composer gate.
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
	// List is the lead's merged commands (session agent commands plus worktree
	// user commands) and the opaque revision they carry, from one read. It
	// reads memory only.
	List(replyID string) (commands []wsusercmd.Command, rev string, err error)
	// Get returns one scope-qualified command ("agent" or "user") and the
	// worktree it was read for.
	Get(replyID, source, id string) (cmd wsusercmd.Command, root string, found bool, err error)
	// Create stores a new project-root-persistent user command in the lead's
	// bound worktree after validating cwd synchronously.
	Create(replyID string, in wsusercmd.Input) (cmd wsusercmd.Command, err error)
	// PatchIf updates a command ("agent" or "user") only while it has the
	// displayed revision, validating any cwd change synchronously.
	PatchIf(replyID, source, id string, rev int64, patch wsusercmd.Patch) (cmd wsusercmd.Command, err error)
	// RemoveIf withdraws a command ("agent" or "user") only while it has the
	// revision the user was shown: wsusercmd.ErrStale when it changed,
	// wsusercmd.ErrUnknown when it is gone.
	RemoveIf(replyID, source, id string, rev int64) error
	// Revision is the combined opaque revision of the lead's commands and
	// worktree: it differs after every change to either scope.
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
// or worktree's commands: each stream compares its lead's revision with the
// one it last sent and sends the opaque "usercmd" event when it differs.
// Safe from any goroutine.
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

// userCommandRequest is the request body of /run and /remove: the revision the
// page displayed, optional scope (defaults to "agent"), optional one-off line
// and cwd overrides for /run, plus /run's submission fields.
type userCommandRequest struct {
	SubmissionID string  `json:"submission_id"`
	Revision     int64   `json:"revision"`
	Retry        bool    `json:"retry"`
	Token        string  `json:"token"`
	Source       string  `json:"source"`
	Line         *string `json:"line"`
	Cwd          *string `json:"cwd"`
}

// userCommandCreateRequest is the request body of POST /user-commands.
type userCommandCreateRequest struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Line        string `json:"line"`
	Prompt      string `json:"prompt"`
	Cwd         string `json:"cwd"`
}

// userCommandUpdateRequest is the request body of PUT /user-commands/<id>.
type userCommandUpdateRequest struct {
	Source      string  `json:"source"`
	Revision    int64   `json:"revision"`
	Label       *string `json:"label"`
	Description *string `json:"description"`
	Line        *string `json:"line"`
	Prompt      *string `json:"prompt"`
	Cwd         *string `json:"cwd"`
}

type userCommandList struct {
	Commands []wsusercmd.Command `json:"commands"`
	Rev      string              `json:"rev"`
	// Token is the composer's admission token (composerExec.token), so a click
	// needs no further read.
	Token string `json:"token,omitempty"`
	// Command is the created or updated command on POST/PUT success.
	Command *wsusercmd.Command `json:"command,omitempty"`
	// Error names why a write was refused with this list attached: stale (the
	// definition changed since the page displayed it) or withdrawn.
	Error string `json:"error,omitempty"`
}

func (d *Daemon) handleUserCommands(w http.ResponseWriter, r *http.Request, replyID, rest string) {
	_, gen, write, ok := d.composerGateMethod(w, r, replyID, true)
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
		d.userCommandList(w, users, replyID, "", nil, http.StatusOK)
	case rest == "" && r.Method == http.MethodPost:
		d.userCommandCreate(w, r, users, replyID)
	case r.Method == http.MethodPut && rest != "" && !strings.Contains(rest, "/"):
		d.userCommandUpdate(w, r, users, replyID, rest)
	case r.Method == http.MethodPost && rest != "":
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

// userCommandList answers the lead's merged list; errText and status attach a
// refused write's reason to it, and cmd attaches a created/updated command.
func (d *Daemon) userCommandList(w http.ResponseWriter, users UserCommandService, replyID, errText string, cmd *wsusercmd.Command, status int) {
	cmds, rev, err := users.List(replyID)
	if err != nil {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	if cmds == nil {
		cmds = []wsusercmd.Command{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, userCommandList{Commands: cmds, Rev: rev, Token: d.composer.token(), Command: cmd, Error: errText})
}

// decodeUserCommandJSON reads one strict JSON object with no unknown fields;
// allowEmpty controls whether a blank body succeeds with zero values.
func decodeUserCommandJSON(w http.ResponseWriter, r *http.Request, into any, allowEmpty bool) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, composerBodyLimit+1))
	if err != nil || len(raw) > composerBodyLimit {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		if allowEmpty {
			return true
		}
		http.Error(w, "malformed request", http.StatusBadRequest)
		return false
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

// userCommandCreate handles POST /user-commands to create a project-root
// persistent user command ("user" scope).
func (d *Daemon) userCommandCreate(w http.ResponseWriter, r *http.Request, users UserCommandService, replyID string) {
	var req userCommandCreateRequest
	if !decodeUserCommandJSON(w, r, &req, false) {
		return
	}
	created, err := users.Create(replyID, wsusercmd.Input{
		ID:          req.ID,
		Label:       req.Label,
		Description: req.Description,
		Line:        req.Line,
		Prompt:      req.Prompt,
		Cwd:         req.Cwd,
		Source:      wsusercmd.SourceUser,
	})
	switch {
	case err == nil:
		d.userCommandList(w, users, replyID, "", &created, http.StatusCreated)
	case errors.Is(err, wsusercmd.ErrInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, wsusercmd.ErrFull), errors.Is(err, wsusercmd.ErrExists):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, "could not create the command: "+err.Error(), http.StatusInternalServerError)
	}
}

// userCommandUpdate handles PUT /user-commands/<id> to update a command's
// stored template in either scope ("agent" or "user") when its revision matches.
func (d *Daemon) userCommandUpdate(w http.ResponseWriter, r *http.Request, users UserCommandService, replyID, id string) {
	if !wsusercmd.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	var req userCommandUpdateRequest
	if !decodeUserCommandJSON(w, r, &req, false) {
		return
	}
	if req.Revision <= 0 {
		http.Error(w, "the displayed revision is required", http.StatusBadRequest)
		return
	}
	source, err := wsusercmd.NormalizeSource(req.Source)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	updated, err := users.PatchIf(replyID, source, id, req.Revision, wsusercmd.Patch{
		Label:       req.Label,
		Description: req.Description,
		Line:        req.Line,
		Prompt:      req.Prompt,
		Cwd:         req.Cwd,
	})
	switch {
	case err == nil:
		d.userCommandList(w, users, replyID, "", &updated, http.StatusOK)
	case errors.Is(err, wsusercmd.ErrInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, wsusercmd.ErrStale):
		d.userCommandList(w, users, replyID, "stale", nil, http.StatusConflict)
	case errors.Is(err, wsusercmd.ErrUnknown):
		d.userCommandList(w, users, replyID, "withdrawn", nil, http.StatusConflict)
	default:
		http.Error(w, "could not update the command: "+err.Error(), http.StatusInternalServerError)
	}
}

// userCommandRun runs command (source, id) through the composer's admission.
// Optional one-off line and cwd overrides are validated against the same
// bounds and worktree-root cwd fence synchronously before any slot is claimed,
// while still requiring the stored template's revision to match the displayed
// revision and binding idempotency to the full (source, id, revision, overrides)
// tuple.
func (d *Daemon) userCommandRun(w http.ResponseWriter, r *http.Request, users UserCommandService, replyID, id string, gen uint64) {
	svcp := d.composer.svc.Load()
	if svcp == nil || !wsusercmd.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	var req userCommandRequest
	if !decodeUserCommandJSON(w, r, &req, true) {
		return
	}
	if !composerSubmissionID.MatchString(req.SubmissionID) {
		http.Error(w, "a submission id is required", http.StatusBadRequest)
		return
	}
	source, err := wsusercmd.NormalizeSource(req.Source)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	slot := &composerSlot{source: source, id: id, revision: req.Revision}
	if req.Line != nil {
		if err := wsusercmd.ValidateLine(*req.Line); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		slot.hasLine = true
		slot.lineOverride = *req.Line
	}
	if req.Cwd != nil {
		if err := wsusercmd.ValidateCwdSyntax(*req.Cwd); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		slot.hasCwd = true
		slot.cwdOverride = strings.TrimSpace(*req.Cwd)
	}
	svc := *svcp
	// One ordering point: the known-card, fence and token answers, the check
	// that the command still has the displayed revision, the synchronous cwd
	// worktree fence, and the claim happen under composerAdmitSlot. A retry, a
	// fenced id or a known card answers without ever looking the command up, so
	// a run whose response was lost maps to its one job (or reports unknown)
	// even when the command changed or was withdrawn since, and never launches.
	var refusal string
	var lookupErr error
	var badReqErr error
	resolve := func() (string, string, string, bool) {
		cmd, root, found, err := users.Get(replyID, source, id)
		switch {
		case err != nil:
			lookupErr = err
			return "", "", "", false
		case !found:
			refusal = "withdrawn"
			return "", "", "", false
		case cmd.Revision != req.Revision:
			refusal = "stale"
			return "", "", "", false
		}
		effLine := cmd.Line
		if slot.hasLine {
			effLine = slot.lineOverride
		}
		effCwd := cmd.Cwd
		if slot.hasCwd {
			effCwd = slot.cwdOverride
		}
		checkedCwd, err := wsusercmd.ValidateWorktreeCwd(root, effCwd)
		if err != nil {
			badReqErr = err
			return "", "", "", false
		}
		slot.label = cmd.Label
		return effLine, root, checkedCwd, true
	}
	sub, admission := d.composerAdmitSlot(replyID, req.SubmissionID, "", gen, req.Retry, req.Token, slot, resolve)
	if admission == composerRefused {
		if lookupErr != nil {
			http.Error(w, "unknown session", http.StatusNotFound)
			return
		}
		if badReqErr != nil {
			http.Error(w, badReqErr.Error(), http.StatusBadRequest)
			return
		}
		d.userCommandList(w, users, replyID, refusal, nil, http.StatusConflict)
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

// userCommandRemove is the user's own withdrawal of command (source, id). The
// displayed revision is required: the removal is made only while the command
// still has it (decided under the registry's writer lock together with the
// removal), and otherwise the user is shown the current list first.
func (d *Daemon) userCommandRemove(w http.ResponseWriter, r *http.Request, users UserCommandService, replyID, id string) {
	if !wsusercmd.ValidID(id) {
		http.NotFound(w, r)
		return
	}
	var req userCommandRequest
	if !decodeUserCommandJSON(w, r, &req, true) {
		return
	}
	if req.Revision <= 0 {
		http.Error(w, "the displayed revision is required", http.StatusBadRequest)
		return
	}
	source, err := wsusercmd.NormalizeSource(req.Source)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch err := users.RemoveIf(replyID, source, id, req.Revision); {
	case err == nil:
		d.userCommandList(w, users, replyID, "", nil, http.StatusOK)
	case errors.Is(err, wsusercmd.ErrStale):
		d.userCommandList(w, users, replyID, "stale", nil, http.StatusConflict)
	case errors.Is(err, wsusercmd.ErrUnknown):
		d.userCommandList(w, users, replyID, "withdrawn", nil, http.StatusConflict)
	default:
		http.Error(w, "could not remove the command: "+err.Error(), http.StatusInternalServerError)
	}
}
