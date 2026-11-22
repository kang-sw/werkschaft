// worktree_release.go holds the release boundary of spawned conversations
// bound to a checkout: a completed worktree.release ends every binding made
// before it, so such a conversation never launches, wakes or resumes there.
package wsagent

import (
	"fmt"

	"github.com/kang-sw/devenv/internal/wsgit"
)

// releaseBound reports whether rec can lose its binding to a release: only a
// spawned, non-lead conversation does. A lead's binding keeps counting.
func releaseBound(rec Record) bool {
	return rec.Parent != nil && !rec.Lead
}

// releasedCheckoutOf is the checkout, holding rec's root or working
// directory, whose most recent completed release came after rec was bound;
// "" when there is none or rec is not release-bound. It reads the checkouts'
// release markers, so the runner asks it only where a process would start
// (startProcess, Deliver to a sleeping conversation) and once at a rejoin.
func releasedCheckoutOf(rec Record) string {
	if !releaseBound(rec) {
		return ""
	}
	seen := map[string]bool{}
	for _, p := range []string{rec.Root, rec.workDir()} {
		checkout := wsgit.CheckoutRoot(canonicalWorktreePath(p))
		if checkout == "" || seen[checkout] {
			continue
		}
		seen[checkout] = true
		if at, ok, err := wsgit.WorktreeReleasedAt(checkout); err == nil && ok && wsgit.BoundBeforeRelease(rec.CreatedAt, at) {
			return checkout
		}
	}
	return ""
}

// checkoutReleasedRefusal is the start refusal of a conversation whose
// checkout was released after it was bound. It is an admission refusal: the
// input stays and the conversation counts as asleep.
func checkoutReleasedRefusal(checkout string) *admissionRefusal {
	return &admissionRefusal{
		reason:   fmt.Sprintf("its worktree %s was released after it was bound there; a release ends every earlier binding, so it no longer launches, wakes or resumes in that worktree. Spawn a new agent (into a re-acquired worktree if needed) to continue", checkout),
		checkout: checkout,
	}
}

// released is the checkout this conversation is known, in memory, to have
// been released from; "" when none is known.
func (c *conv) released() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.releasedCheckout
}

// noteReleased records, in memory, that checkout's release ended this
// conversation's binding.
func (c *conv) noteReleased(checkout string) {
	if checkout == "" {
		return
	}
	c.setStatus(func() { c.releasedCheckout = checkout })
}

// ReleasedConversation is one spawned conversation whose binding a release
// ended (Runner.CheckoutReleased): its address and state, and, for a forced
// release, whether its running turn was asked to stop and the backend's
// interrupt error when that failed. A failed interrupt leaves the process
// running; the release never stops it.
type ReleasedConversation struct {
	Address   string `json:"address"`
	State     string `json:"state"`
	Cancelled bool   `json:"cancelled,omitempty"`
	Error     string `json:"error,omitempty"`
}

// CheckoutReleased is worktree.release's notice, after it recorded worktree's
// release marker: every non-closed spawned conversation bound to worktree
// before that release is marked released in memory, so it takes no further
// input there even while its process lives. With cancelTurns (a forced
// release) each such conversation's running turn is interrupted through the
// turn-cancellation path; an interrupt error is reported, never answered by
// stopping the process.
func (r *Runner) CheckoutReleased(worktree string, cancelTurns bool) []ReleasedConversation {
	target := canonicalWorktreePath(worktree)
	if target == "" {
		return nil
	}
	at, ok, err := wsgit.WorktreeReleasedAt(target)
	if err != nil || !ok {
		return nil
	}
	var out []ReleasedConversation
	for _, c := range r.boundConvs(target) {
		c.mu.Lock()
		rec, closed := c.rec, c.closed
		c.mu.Unlock()
		if closed || rec.handedOff() || !releaseBound(rec) || !wsgit.BoundBeforeRelease(rec.CreatedAt, at) {
			continue
		}
		c.noteReleased(target)
		entry := ReleasedConversation{Address: AgentAddress(rec.ReplyID)}
		if cancelTurns {
			var cancelErr error
			ran := c.do(func() {
				entry.Cancelled, cancelErr = c.cancelTurn()
			})
			if !ran {
				cancelErr = errClosed
			}
			if cancelErr != nil {
				entry.Cancelled = false
				entry.Error = cancelErr.Error()
			}
		}
		entry.State = AgentStatus(c.memoryState())
		out = append(out, entry)
	}
	return out
}

// boundConvs lists the open conversations whose canonical root is target or
// whose working directory is target or below it.
func (r *Runner) boundConvs(target string) []*conv {
	r.mu.Lock()
	convs := make([]*conv, 0, len(r.convs))
	for _, c := range r.convs {
		convs = append(convs, c)
	}
	r.mu.Unlock()
	var out []*conv
	for _, c := range convs {
		c.mu.Lock()
		root, cwd := c.rec.Root, c.rec.Cwd
		c.mu.Unlock()
		if sameWorktreePath(canonicalWorktreePath(root), target) || worktreePathContains(target, canonicalWorktreePath(cwd)) {
			out = append(out, c)
		}
	}
	return out
}

// LiveExecJobsInWorktree lists the daemon's running exec jobs in worktree
// (Options.LiveExecJobs); none without that option.
func (r *Runner) LiveExecJobsInWorktree(worktree string) []string {
	if r.opts.LiveExecJobs == nil {
		return nil
	}
	return r.opts.LiveExecJobs(worktree)
}

// tellRefused tells from that conversation id's start was refused: by
// checkout's release when checkout is set (Options.CheckoutReleased, falling
// back to WakeRefused), else by admission (WakeRefused).
func (r *Runner) tellRefused(from, id, checkout, reason string) {
	if from == "" {
		return
	}
	if checkout != "" && r.opts.CheckoutReleased != nil {
		r.opts.CheckoutReleased(from, id, checkout, reason)
		return
	}
	if r.opts.WakeRefused != nil {
		r.opts.WakeRefused(from, id, reason)
	}
}
