// whiteboard_only.go keeps a hosted lead's sticky whiteboard-only state and the
// one-shot corrective advisory for a turn that ignored it.
//
// The composer's whiteboard toggle rides each user message as
// Entry.WhiteboardFirst. A turn woken by anything else (a child's report, peer
// mail, an exec notice, a restore) carries no such message, so the state is
// kept on the Record: a user message sets or clears it, nothing else does.
package wsagent

import (
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

// Plain-text output past either bound is no longer a brief acknowledgement.
const (
	whiteboardAckMaxRunes = 140
	whiteboardAckMaxLines = 2
)

// whiteboardOnlyAdvisory is staged, once, after a whiteboard-only turn that
// answered in plain text without touching the board.
const whiteboardOnlyAdvisory = "Your previous turn answered in plain text, but the user reads your output through the whiteboard only, and the board did not change. Put responses and progress notes on the relevant topic with wb:log or wb:panel, and keep plain-text output to a one-line acknowledgement."

// userOriginEntry reports whether e is a message the user typed into the
// composer. The inbox also carries what the runner and daemon write for
// themselves: scheduled prompts, exec notices, interrupts, slash commands, and
// the system prompt of a manually triggered quota continuation (which is sent
// as an operator entry so it runs interactively). None of those says anything
// about the user's whiteboard toggle.
func userOriginEntry(e wsinbox.Entry) bool {
	if (!e.User && !e.Operator) || e.Command != nil || e.Cron != nil || e.EventID != "" || e.Interrupt {
		return false
	}
	if len(e.Parts) == 1 && (e.Parts[0] == quotaContinuationPrompt(true) || e.Parts[0] == quotaContinuationPrompt(false)) {
		return false
	}
	return true
}

// foldWhiteboardOnly applies one input to the sticky state: a message sent
// with the toggle sets it, a user message sent without clears it, anything
// else leaves it. Only the composer produces WhiteboardFirst, so it counts as
// user input whatever route it took (a LAN browser's message is not an
// operator entry).
func foldWhiteboardOnly(on, whiteboardFirst, user bool) bool {
	switch {
	case whiteboardFirst:
		return true
	case user:
		return false
	}
	return on
}

// whiteboardOnlyAfter folds a turn's inputs, in order, into the sticky state.
func whiteboardOnlyAfter(on bool, inputs []QueuedInput) bool {
	for _, in := range inputs {
		on = foldWhiteboardOnly(on, in.WhiteboardFirst, in.User)
	}
	return on
}

// whiteboardOnlyAfterRecs is whiteboardOnlyAfter for inbox records (a steer).
func whiteboardOnlyAfterRecs(on bool, recs []wsinbox.Record) bool {
	for _, rec := range recs {
		on = foldWhiteboardOnly(on, rec.Entry.WhiteboardFirst, userOriginEntry(rec.Entry))
	}
	return on
}

// setWhiteboardOnly makes next the lead's sticky state, persisting a change.
// Leaving the mode drops a pending advisory, which no longer applies. It runs
// on the conversation's loop.
func (c *conv) setWhiteboardOnly(next bool) bool {
	if !c.rec.Lead {
		return false
	}
	if next != c.rec.WhiteboardOnly {
		c.update(func(rec *Record) { rec.WhiteboardOnly = next })
	}
	if !next {
		c.passive.clearAdvisory()
	}
	return next
}

// noteSteerWhiteboard persists the sticky state the steered records leave,
// before their directive is composed (steerWithDirective).
func (c *conv) noteSteerWhiteboard(recs []wsinbox.Record) {
	c.setWhiteboardOnly(whiteboardOnlyAfterRecs(c.rec.WhiteboardOnly, recs))
}

// boardRevision reads the lead's board revision through the daemon's in-memory
// hook without touching disk; ok is false without a hook, for a non-lead, or
// when the lead lacks a root or session key.
func (c *conv) boardRevision() (rev uint64, ok bool) {
	if c == nil || c.r == nil {
		return 0, false
	}
	fn := c.r.opts.WhiteboardRevision
	if fn == nil || !c.rec.Lead || c.rec.Root == "" || c.rec.WSKey == "" {
		return 0, false
	}
	return fn(filepath.Clean(c.rec.Root), c.rec.WSKey), true
}

// snapshotBoardRevision records the board revision at a turn's start, for
// stageWhiteboardNudge. A lead outside whiteboard-only mode skips the read.
func (c *conv) snapshotBoardRevision() {
	c.wbRev, c.wbRevOK = 0, false
	if c.rec.Lead && c.rec.WhiteboardOnly {
		c.wbRev, c.wbRevOK = c.boardRevision()
	}
}

// briefAck reports whether reply is short enough to be a chat acknowledgement.
func briefAck(reply string) bool {
	reply = strings.TrimSpace(reply)
	return utf8.RuneCountInString(reply) <= whiteboardAckMaxRunes && strings.Count(reply, "\n") < whiteboardAckMaxLines
}

// stageWhiteboardNudge runs when a lead's ordinary turn ended. In whiteboard-
// only mode, a turn that left the board unchanged yet answered at length in
// plain text (reply, the turn's final message) stages one advisory for the
// next turn's input; it starts no turn and costs none. A turn that did change the board, or a lead that left the
// mode, drops a pending advisory. A failed or interrupted turn, or one whose
// start was not observed (no hook, a rejoined process), stages and drops
// nothing.
func (c *conv) stageWhiteboardNudge(turnErr, reply string) {
	snapshot, known := c.wbRev, c.wbRevOK
	c.wbRev, c.wbRevOK = 0, false
	if !c.rec.Lead {
		return
	}
	if !c.rec.WhiteboardOnly {
		c.passive.clearAdvisory()
		return
	}
	if turnErr != "" || c.summaryReason == wslive.TurnAborted || !known {
		return
	}
	rev, ok := c.boardRevision()
	if !ok {
		return
	}
	if rev != snapshot {
		c.passive.clearAdvisory()
		return
	}
	if briefAck(reply) {
		return
	}
	c.passive.putAdvisory(c.openGen(), whiteboardOnlyAdvisory, rev, ok)
}
