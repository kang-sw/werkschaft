// exec_seams.go holds what the daemon's hosted exec jobs need from the
// runner: the owning conversation's identity, a daemon-owned approval
// decided by its role and live permission mode, once-only admission of a
// job's completion notice, and the count of ws MCP calls in flight that a
// turn-replacing steer must wait out.
package wsagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wsinbox"
)

// ExecOwner is the conversation a trusted MCP binding names, as exec
// admission reads it: Gen is its open generation (Record.OpenGen), which a
// completion notice must still match.
type ExecOwner struct {
	ID      string
	Gen     uint64
	Lead    bool
	Backend string
}

// ExecOwner returns the open conversation id's exec identity; a closed,
// handed-off or unknown conversation is refused.
func (r *Runner) ExecOwner(id string) (ExecOwner, error) {
	c, err := r.open(id)
	if err != nil {
		return ExecOwner{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ExecOwner{}, endedError(c.rec)
	}
	return ExecOwner{ID: id, Gen: c.rec.OpenGen, Lead: c.rec.Lead, Backend: c.rec.Backend}, nil
}

// ExecApproval is one exec call shown for approval: the tool called, the
// command as the user reads it, the directory it runs in, and the rest of
// what decides what runs: the program that starts (argv[0] resolved on the
// job's PATH), the call's own environment overlay as NAME=value entries,
// and the head of the stdin it gives with that stdin's whole length.
type ExecApproval struct {
	Tool       string
	Command    string
	Cwd        string
	Program    string
	Env        []string
	Stdin      string
	StdinBytes int
}

// execApprovalInput is call as its prompt's input: what the page shows.
func execApprovalInput(call ExecApproval) ([]byte, string, error) {
	in := map[string]any{"command": call.Command, "cwd": call.Cwd}
	var more []string
	if call.Program != "" {
		in["program"] = call.Program
		more = append(more, "program "+call.Program)
	}
	if len(call.Env) > 0 {
		env := map[string]string{}
		for _, kv := range call.Env {
			name, value, _ := strings.Cut(kv, "=")
			env[name] = value
		}
		in["env"] = env
		more = append(more, "env "+strings.Join(call.Env, " "))
	}
	if call.StdinBytes > 0 {
		in["stdin"] = call.Stdin
		in["stdin_bytes"] = call.StdinBytes
		more = append(more, fmt.Sprintf("stdin %d bytes", call.StdinBytes))
	}
	desc := "Run this command: " + call.Command
	if len(more) > 0 {
		desc += " (" + strings.Join(more, "; ") + ")"
	}
	raw, err := json.Marshal(in)
	return raw, desc, err
}

// ExecGrant is how an exec call was allowed.
type ExecGrant int

const (
	// ExecGrantPosture: the conversation's role or live permission mode
	// runs the call without a prompt. The mode can change before the
	// process starts, so the caller rechecks it then (ExecImmediate).
	ExecGrantPosture ExecGrant = iota + 1
	// ExecGrantAnswer: the user allowed this call; it stands.
	ExecGrantAnswer
)

// ErrExecDenied is the user's refusal of an exec call (ApproveExec); the
// error carries the user's message when one was given.
var ErrExecDenied = errors.New("the user denied this exec call")

type execDenial struct{ message string }

func (e *execDenial) Error() string {
	if e.message == "" {
		return ErrExecDenied.Error()
	}
	return ErrExecDenied.Error() + ": " + e.message
}

func (e *execDenial) Unwrap() error { return ErrExecDenied }

// execAnswer settles one waiting exec approval: the user's answer, or
// posture when the live mode came to run the call without a prompt.
type execAnswer struct {
	allow   bool
	posture bool
	message string
}

// ExecImmediate reports whether conversation id runs an exec call now
// without a prompt (execImmediate).
func (r *Runner) ExecImmediate(id string) (bool, error) {
	c, err := r.open(id)
	if err != nil {
		return false, err
	}
	return c.execImmediate(), nil
}

// LockExecAuthority shares the permission transition's admission guard.
// Take it before consulting authority or the hosted job's mutex.
func (r *Runner) LockExecAuthority(id string) (func(), error) {
	c, err := r.open(id)
	if err != nil {
		return nil, err
	}
	admission := &r.permissionSlot(id, c.dir).admission
	admission.RLock()
	return admission.RUnlock, nil
}

// execImmediate reports whether c runs an exec call without a prompt: every
// conversation that is not a lead does (a spawned worker's own permission
// requests are answered for it, and no page shows a non-lead's prompt), and
// a lead does under full permission or agy's ws bypass. A Codex lead with no
// common selection counts its harness's reported full-access mode, as the
// page's permission level does (permissionState). It reads memory only: the
// record and the selection cache a permission write refreshes.
func (c *conv) execImmediate() bool {
	c.mu.Lock()
	rec, active, process := c.rec, c.activeMode, c.proc != nil
	c.mu.Unlock()
	if !rec.Lead || rec.agyBypass() {
		return true
	}
	selected, explicit, readErr := c.r.cachedPermissionSelection(rec.ReplyID, c.dir)
	level, source, err := c.r.resolveLeadPermission(rec, selected, explicit, readErr)
	if err != nil {
		return false
	}
	level = c.r.projectLiveMode(level, rec, source, active, process)
	return level == PermissionFull
}

// ApproveExec returns once conversation id may run call: at once when its
// role or live mode runs it without a prompt, otherwise after the user
// allows it on the session page, where it waits as a permission prompt
// bound to this call and this conversation. A denial is ErrExecDenied; the
// end of ctx withdraws the prompt and returns ctx's error. A mode change
// that runs the call without a prompt settles a waiting one
// (recheckExecApprovals).
func (r *Runner) ApproveExec(ctx context.Context, id string, call ExecApproval) (ExecGrant, error) {
	c, err := r.open(id)
	if err != nil {
		return 0, err
	}
	if c.execImmediate() {
		return ExecGrantPosture, nil
	}
	reqID, err := newExecApprovalID()
	if err != nil {
		return 0, err
	}
	input, description, err := execApprovalInput(call)
	if err != nil {
		return 0, err
	}
	answer := make(chan execAnswer, 1)
	var refusal error
	if !c.do(func() {
		if c.closed {
			refusal = endedError(c.record())
			return
		}
		if c.execImmediate() {
			answer <- execAnswer{posture: true}
			return
		}
		if c.rec.CompanionRole != "" {
			// A hidden companion has no page to answer the prompt: refuse at
			// once rather than park it forever. (A call its posture runs without
			// a prompt, execImmediate above, is the ws server's own gate:
			// companionBodyBaseAllows keeps exec.* from the body.)
			refusal = &execDenial{message: "companion sessions run no shell commands; delegate command work to an engineering agent with agent.spawn"}
			return
		}
		c.addExecApproval(ApprovalRequest{
			ID:          reqID,
			Tool:        call.Tool,
			Input:       input,
			Description: description,
			StartedAt:   r.opts.Now().UnixMilli(),
		}, answer)
	}) {
		return 0, endedError(c.record())
	}
	if refusal != nil {
		return 0, refusal
	}
	settle := func(a execAnswer) (ExecGrant, error) {
		switch {
		case a.posture:
			return ExecGrantPosture, nil
		case a.allow:
			return ExecGrantAnswer, nil
		}
		return 0, &execDenial{message: strings.TrimSpace(a.message)}
	}
	select {
	case a := <-answer:
		return settle(a)
	case <-ctx.Done():
		c.do(func() { c.withdrawExecApproval(reqID) })
		select {
		case a := <-answer:
			// The answer won the race with the withdrawal.
			return settle(a)
		default:
		}
		return 0, ctx.Err()
	case <-c.done:
		return 0, endedError(c.record())
	}
}

func newExecApprovalID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "exec-" + hex.EncodeToString(raw), nil
}

// addExecApproval shows a daemon-owned exec approval as a waiting prompt.
// It runs in the loop.
func (c *conv) addExecApproval(a ApprovalRequest, answer chan execAnswer) {
	if c.execAnswers == nil {
		c.execAnswers = map[string]chan execAnswer{}
	}
	c.execAnswers[a.ID] = answer
	c.setStatus(func() { c.approvals = append(c.approvals, a) })
	c.checkAction(false)
	c.showApprovals(false)
}

// answerExecApproval hands the user's answer to the exec call waiting on
// prompt id, and reports whether id was such a prompt. It runs in the loop.
func (c *conv) answerExecApproval(id string, allow bool, message string) bool {
	answer, ok := c.execAnswers[id]
	if !ok {
		return false
	}
	delete(c.execAnswers, id)
	c.dropApproval(id, false)
	answer <- execAnswer{allow: allow, message: message}
	return true
}

// withdrawExecApproval removes the prompt of an exec call that ended
// waiting. It runs in the loop.
func (c *conv) withdrawExecApproval(id string) {
	if _, ok := c.execAnswers[id]; !ok {
		return
	}
	delete(c.execAnswers, id)
	c.dropApproval(id, false)
}

// recheckExecApprovals settles every waiting exec approval once the live
// mode runs exec calls without a prompt. It runs in the loop, after each
// permission change.
func (c *conv) recheckExecApprovals() {
	if len(c.execAnswers) == 0 || !c.execImmediate() {
		return
	}
	for id, answer := range c.execAnswers {
		delete(c.execAnswers, id)
		c.dropApproval(id, false)
		answer <- execAnswer{posture: true}
	}
}

// execApprovalsOnly returns the waiting prompts that are daemon-owned exec
// approvals, oldest first.
func (c *conv) execApprovalsOnly() []ApprovalRequest {
	var out []ApprovalRequest
	for _, a := range c.approvals {
		if _, ok := c.execAnswers[a.ID]; ok {
			out = append(out, a)
		}
	}
	return out
}

// CompletionOutcome is what AdmitCompletion did with a notice.
type CompletionOutcome int

const (
	// CompletionAccepted: the notice is in the conversation's inbox.
	CompletionAccepted CompletionOutcome = iota + 1
	// CompletionDuplicate: the event was admitted before; nothing changed.
	CompletionDuplicate
	// CompletionSuppressed: the conversation was closed (explicitly, at
	// any time since the job's admission) or is unknown; the notice is
	// dropped for good.
	CompletionSuppressed
	// CompletionDeferred: the session is handed to a terminal harness; a
	// takeover may bring it back, so the notice waits for a later try.
	CompletionDeferred
)

func (o CompletionOutcome) String() string {
	switch o {
	case CompletionAccepted:
		return "accepted"
	case CompletionDuplicate:
		return "duplicate"
	case CompletionSuppressed:
		return "suppressed"
	case CompletionDeferred:
		return "deferred"
	}
	return fmt.Sprintf("outcome(%d)", int(o))
}

// acceptedEventsCap bounds Record.AcceptedEvents. A notice's source marks
// it delivered right after its admission, so only a crash between the two
// replays it; the cap covers far more than that window.
const acceptedEventsCap = 64

// AdmitCompletion delivers the completion notice text of event eventID to
// conversation id, admitted under open generation gen (ExecOwner.Gen), as
// a steered entry: a running turn takes it now where its harness steers, an
// idle session starts a turn with it, and a harness that takes no steer
// gets it at its next turn. An event is admitted once: its id is checked
// and recorded under the same locks as the inbox write, and recovered from
// the inbox itself, so a replay after a crash on either side of the write
// finds it. A closed conversation, or one closed since gen, suppresses the
// notice; a closed spawned Pi child is never reopened for it (send's
// revival is for ordinary input).
func (r *Runner) AdmitCompletion(id string, gen uint64, eventID, text string) (CompletionOutcome, error) {
	eventID, text = strings.TrimSpace(eventID), strings.TrimSpace(text)
	if eventID == "" || text == "" {
		return 0, badRequest("a completion notice needs an event id and text")
	}
	for {
		c, err := r.open(id)
		if err != nil {
			r.mu.Lock()
			rec, known := r.closed[id]
			r.mu.Unlock()
			if known && rec.ClosedAt == "" && rec.handedOff() && rec.OpenGen == gen {
				return CompletionDeferred, nil
			}
			return CompletionSuppressed, nil
		}
		if r.opts.admitCompletionAdmitting != nil {
			r.opts.admitCompletionAdmitting(id)
		}
		c.wmu.Lock()
		c.mu.Lock()
		if c.parkedOut {
			// Parked after open returned (maybePark holds wmu while it parks):
			// retry open so the child rehydrates and receives the notice.
			c.mu.Unlock()
			c.wmu.Unlock()
			continue
		}
		defer c.wmu.Unlock()
		if c.closed || c.rec.OpenGen != gen {
			c.mu.Unlock()
			return CompletionSuppressed, nil
		}
		if c.acceptedEvents == nil {
			c.acceptedEvents = map[string]struct{}{}
			for _, have := range c.rec.AcceptedEvents {
				c.acceptedEvents[have] = struct{}{}
			}
			// An entry written just before a crash that lost its record write.
			held, readErr := wsinbox.EventIDs(c.inbox)
			for _, have := range held {
				c.acceptedEvents[have] = struct{}{}
			}
			if readErr != nil {
				fmt.Fprintf(r.opts.Log, "agent %s: read notice ids: %v\n", id, readErr)
			}
		}
		if _, seen := c.acceptedEvents[eventID]; seen {
			c.mu.Unlock()
			return CompletionDuplicate, nil
		}
		if _, _, err := wsinbox.Send(c.inbox, wsinbox.Entry{Parts: []string{text}, Steer: true, EventID: eventID}, r.opts.Now()); err != nil {
			c.mu.Unlock()
			return 0, err
		}
		c.acceptedEvents[eventID] = struct{}{}
		accepted := append(c.rec.AcceptedEvents, eventID)
		if len(accepted) > acceptedEventsCap {
			accepted = accepted[len(accepted)-acceptedEventsCap:]
		}
		c.rec.AcceptedEvents = append([]string(nil), accepted...)
		c.sends++
		c.held++
		c.rec.Sends = c.sends
		c.syncStateLocked()
		c.cancelModelWaitBackoffForInput()
		if c.rec.Parent != nil {
			c.rec.LastActiveAt = r.opts.Now().UTC().Format(time.RFC3339)
		}
		c.mu.Unlock()
		c.inboxMutated(1)
		// The acceptance is durable in the inbox already; the record keeps it
		// past the entry's drain and trim.
		_ = c.persistRecord()
		c.signalInbox()
		return CompletionAccepted, nil
	}
}

// MCPCallBegin counts one ws MCP tool call in flight on session token's
// conversation and returns the call's end, which is idempotent: the reply
// and a cancellation may both end it, and only the first counts. A token
// the runner does not know counts nothing.
func (r *Runner) MCPCallBegin(token string) (end func()) {
	r.mu.Lock()
	t := r.mcpTokens[token]
	r.mu.Unlock()
	if t == nil {
		return func() {}
	}
	c := r.resident(t.id)
	if c == nil {
		return func() {}
	}
	c.mcpCalls.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() {
			if c.mcpCalls.Add(-1) == 0 {
				// A notice held for this call (holdsNotices) goes now.
				c.signalInbox()
			}
		})
	}
}

// holdsNotices reports whether system notices wait instead of steering: the
// harness's steer replaces its running turn (processControls.replacesTurn)
// and a ws MCP call is in flight, whose reply that new turn would lose. The
// call's end signals the inbox, which steers them then; a turn that ends
// first takes them at its end.
func (c *conv) holdsNotices(ctl processControls) bool {
	return ctl.replacesTurn && c.mcpCalls.Load() > 0
}
