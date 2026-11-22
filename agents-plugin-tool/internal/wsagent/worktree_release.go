// worktree_release.go holds the release boundary of spawned conversations
// bound to a checkout: a completed worktree.release ends every binding made
// before it, so such a conversation never launches, wakes or resumes there.
package wsagent

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsgit"
)

// releaseBound reports whether rec can lose its binding to a release: only a
// spawned, non-lead conversation does. A lead's binding keeps counting.
func releaseBound(rec Record) bool {
	return rec.Parent != nil && !rec.Lead
}

// resolveCheckouts lists the checkouts rec is bound to (bindingCheckouts);
// nil when rec is not release-bound. It touches the file system, so a
// conversation resolves it once, when it is loaded or spawned (startConv),
// and keeps it in memory.
//
// root and cwd are rec's canonical Root and effective working directory
// (conv.canonRoot, conv.canonCwd; canonicalBinding).
func resolveCheckouts(rec Record, root, cwd string) []string {
	if !releaseBound(rec) {
		return nil
	}
	return bindingCheckouts(root, cwd)
}

// canonicalBinding is rec's canonical root and canonical effective working
// directory (Record.workDir: its cwd, or its root when it has none), the two
// paths live occupancy (boundConvs, ActiveChildrenInWorktree) and the create
// reservation (reserveBoundWorktrees) bind a conversation by.
func canonicalBinding(rec Record) (root, cwd string) {
	return canonicalWorktreePath(rec.Root), canonicalWorktreePath(rec.workDir())
}

// bindingCheckouts lists the checkouts a conversation with canonical root and
// effective working directory cwd is bound to, by exactly the rule live
// occupancy uses (boundConvs): every checkout that contains root or cwd, so
// a root or working directory inside a submodule or nested repository of a
// pooled worktree reaches that worktree. It costs one Lstat per ancestor.
func bindingCheckouts(root, cwd string) []string {
	var out []string
	for _, path := range []string{root, cwd} {
		for _, checkout := range wsgit.ContainingCheckouts(path) {
			if !slices.ContainsFunc(out, func(have string) bool { return sameWorktreePath(have, checkout) }) {
				out = append(out, checkout)
			}
		}
	}
	return out
}

// sameWorktreePath reports whether two canonical paths name one checkout;
// Windows paths compare without case.
func sameWorktreePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// checkoutRelease is one checkout's most recent completed release as the
// runner knows it: at is its marker's instant when ok.
type checkoutRelease struct {
	at time.Time
	ok bool
}

// releaseKey is a canonical checkout path's key in Runner.releases; Windows
// paths compare without case, as sameWorktreePath does.
func releaseKey(checkout string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(checkout)
	}
	return checkout
}

// primeCheckoutReleases reads, once per checkout per daemon, the release
// marker of each checkout the runner has not seen yet, so the interactive
// paths (Deliver, mail claims, process start, occupancy) answer from memory.
// A release this daemon hears of later replaces the entry (CheckoutReleased).
// An unreadable or malformed marker reads as no recorded release, as before:
// every binding then counts.
func (r *Runner) primeCheckoutReleases(checkouts []string) {
	for _, checkout := range checkouts {
		key := releaseKey(checkout)
		r.relMu.Lock()
		_, known := r.releases[key]
		r.relMu.Unlock()
		if known {
			continue
		}
		at, ok, err := wsgit.WorktreeReleasedAt(checkout)
		rel := checkoutRelease{at: at, ok: ok && err == nil}
		r.relMu.Lock()
		if _, known := r.releases[key]; !known {
			if r.releases == nil {
				r.releases = map[string]checkoutRelease{}
			}
			r.releases[key] = rel
		}
		r.relMu.Unlock()
	}
}

// knownCheckoutRelease is checkout's release from memory alone; a checkout
// never primed reads as unreleased.
func (r *Runner) knownCheckoutRelease(checkout string) checkoutRelease {
	r.relMu.Lock()
	defer r.relMu.Unlock()
	return r.releases[releaseKey(checkout)]
}

// checkoutReleaseOf is checkout's release, priming it from its marker at its
// first use in this daemon.
func (r *Runner) checkoutReleaseOf(checkout string) checkoutRelease {
	r.primeCheckoutReleases([]string{checkout})
	return r.knownCheckoutRelease(checkout)
}

// noteCheckoutRelease records, in memory, that checkout's release completed
// at at.
func (r *Runner) noteCheckoutRelease(checkout string, at time.Time) {
	r.relMu.Lock()
	defer r.relMu.Unlock()
	if r.releases == nil {
		r.releases = map[string]checkoutRelease{}
	}
	r.releases[releaseKey(checkout)] = checkoutRelease{at: at, ok: true}
}

// releasedFrom is the first of checkouts whose known release came after rec
// was bound; "" when there is none or rec is not release-bound. It reads
// memory only.
func (r *Runner) releasedFrom(rec Record, checkouts []string) string {
	if !releaseBound(rec) {
		return ""
	}
	for _, checkout := range checkouts {
		if rel := r.knownCheckoutRelease(checkout); rel.ok && wsgit.BoundBeforeRelease(rec.CreatedAt, rec.CreatedUnixNano, rel.at) {
			return checkout
		}
	}
	return ""
}

// loadReleasedCheckout resolves rec's checkouts, primes their releases, and
// reports the one whose release ended its binding: the load-time check of a
// record with no conversation yet (a Pi child's resume).
func (r *Runner) loadReleasedCheckout(rec Record) string {
	root, cwd := canonicalBinding(rec)
	checkouts := resolveCheckouts(rec, root, cwd)
	r.primeCheckoutReleases(checkouts)
	return r.releasedFrom(rec, checkouts)
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

// released is the checkout whose release, as the runner knows it in memory,
// ended this conversation's binding; "" when none. It touches no file.
func (c *conv) released() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.releasedLocked()
}

// releasedLocked is released with c.mu held. The answer is kept once found:
// a release is never undone.
func (c *conv) releasedLocked() string {
	if c.releasedCheckout == "" {
		c.releasedCheckout = c.r.releasedFrom(c.rec, c.checkouts)
	}
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
// release marker at releasedAt (zero: read the marker, for a notice that does
// not carry it). The runner keeps that release in memory, and every
// non-closed spawned conversation bound to worktree before it is marked
// released, so it takes no further input there even while its process
// lives. With cancelTurns (a forced release by a root lead) each such
// conversation's running turn is interrupted through the turn-cancellation
// path; an interrupt error is reported, never answered by stopping the
// process.
func (r *Runner) CheckoutReleased(worktree string, releasedAt time.Time, cancelTurns bool) []ReleasedConversation {
	target := canonicalWorktreePath(worktree)
	if target == "" {
		return nil
	}
	if releasedAt.IsZero() {
		at, ok, err := wsgit.WorktreeReleasedAt(target)
		if err != nil || !ok {
			return nil
		}
		releasedAt = at
	}
	r.noteCheckoutRelease(target, releasedAt)
	var out []ReleasedConversation
	for _, c := range r.boundConvs(target) {
		c.mu.Lock()
		rec, closed := c.rec, c.closed
		c.mu.Unlock()
		if closed || rec.handedOff() || !releaseBound(rec) || !wsgit.BoundBeforeRelease(rec.CreatedAt, rec.CreatedUnixNano, releasedAt) {
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

// boundConvs lists the conversations whose canonical root or canonical
// effective working directory is target or below it (the rule of
// ActiveChildrenInWorktree and reserveBoundWorktrees). It reads memory only.
func (r *Runner) boundConvs(target string) []*conv {
	r.mu.Lock()
	convs := make([]*conv, 0, len(r.convs))
	for _, c := range r.convs {
		convs = append(convs, c)
	}
	r.mu.Unlock()
	var out []*conv
	for _, c := range convs {
		// canonRoot and canonCwd were resolved once in startConv: this
		// per-candidate scan touches no file.
		if worktreePathContains(target, c.canonRoot) || worktreePathContains(target, c.canonCwd) {
			out = append(out, c)
		}
	}
	return out
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
