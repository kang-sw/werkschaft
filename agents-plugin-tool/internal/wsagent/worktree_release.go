// worktree_release.go holds the release boundary of spawned conversations
// bound to a checkout: a completed worktree.release ends every binding made
// before it, so such a conversation never launches, wakes or resumes there.
package wsagent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// Runner.releases holds only completed releases, never "not released": a
// release this daemon did not hear of (a notice that was lost, or a release
// by another process) must still reach the next cold check, which reads the
// marker (readCheckoutRelease). It is monotonic: an entry only moves to a
// later instant, so a stale or reordered notice never moves it back.

// knownCheckoutRelease is checkout's release from memory alone: the latest
// release this daemon heard of or read. A checkout with none reads as
// unreleased. The interactive paths (Deliver to a running process, mail
// claims, the occupancy scan's per-conversation check) use it.
func (r *Runner) knownCheckoutRelease(checkout string) checkoutRelease {
	r.relMu.Lock()
	defer r.relMu.Unlock()
	at, ok := r.releases[releaseKey(checkout)]
	return checkoutRelease{at: at, ok: ok}
}

// readCheckoutRelease is checkout's release as its durable marker records it,
// or as memory knows it when that is later: the cold paths' check (a process
// start, a load, a Pi revival, the occupancy scan's target). A marker found
// is remembered; its absence never is. An unreadable or malformed marker
// reads as no recorded release, as before: every binding then counts. It
// costs the marker lookup: a stat of .git, a read of a gitfile, and a read of
// the marker.
func (r *Runner) readCheckoutRelease(checkout string) checkoutRelease {
	if at, ok, err := wsgit.WorktreeReleasedAt(checkout); err == nil && ok {
		r.noteCheckoutRelease(checkout, at)
	}
	return r.knownCheckoutRelease(checkout)
}

// noteCheckoutRelease records, in memory, that checkout's release completed
// at at, unless memory already knows a later one.
func (r *Runner) noteCheckoutRelease(checkout string, at time.Time) {
	r.relMu.Lock()
	defer r.relMu.Unlock()
	if r.releases == nil {
		r.releases = map[string]time.Time{}
	}
	key := releaseKey(checkout)
	if have, ok := r.releases[key]; !ok || at.After(have) {
		r.releases[key] = at
	}
}

// releasedFrom is the first of checkouts whose known release came after rec
// was bound; "" when there is none or rec is not release-bound. It reads
// memory only.
func (r *Runner) releasedFrom(rec Record, checkouts []string) string {
	return r.releasedBy(rec, checkouts, r.knownCheckoutRelease)
}

// releasedOnDisk is releasedFrom with each checkout's marker read
// (readCheckoutRelease): the cold paths' check, which a lost notice cannot
// mislead.
func (r *Runner) releasedOnDisk(rec Record, checkouts []string) string {
	return r.releasedBy(rec, checkouts, r.readCheckoutRelease)
}

func (r *Runner) releasedBy(rec Record, checkouts []string, of func(string) checkoutRelease) string {
	if !releaseBound(rec) {
		return ""
	}
	for _, checkout := range checkouts {
		if rel := of(checkout); rel.ok && wsgit.BoundBeforeRelease(rec.CreatedAt, rec.CreatedUnixNano, rel.at) {
			return checkout
		}
	}
	return ""
}

type resolvedAgentBinding struct {
	root, cwd, released string
	// gone is the binding path that no longer exists (goneBinding), when
	// no release ended the binding first.
	gone      string
	checkouts []string
}

// resolveAgentBinding loads the paths and release evidence once per lifetime.
// Startup reuses it for both reconciliation and serving wakeable records.
func (r *Runner) resolveAgentBinding(rec Record) resolvedAgentBinding {
	root, cwd := canonicalBinding(rec)
	checkouts := resolveCheckouts(rec, root, cwd)
	binding := resolvedAgentBinding{root: root, cwd: cwd, checkouts: checkouts, released: r.releasedOnDisk(rec, checkouts)}
	if binding.released == "" {
		binding.gone = goneBinding(rec, root, cwd, checkouts)
	}
	return binding
}

// ended is the checkout whose release or removal ended the binding: the
// released one first, else the gone one; "" when neither.
func (b resolvedAgentBinding) ended() string {
	if b.released != "" {
		return b.released
	}
	return b.gone
}

// goneBinding is the first path of a release-bound conversation's binding
// that was removed: one of its checkouts whose directory, .git entry, or
// registered admin directory is gone (wsgit.CheckoutRegistrationGone), or
// its canonical root or working directory, when no live checkout of the
// binding contains it (its checkouts were resolved after the worktree had
// already vanished), removed from a parent that still exists (removedDir).
// A checkout removed
// after a release can take its release marker with it (the marker lives in
// the admin directory), so this is the cold paths' evidence that a binding
// must not launch there again. "" when there is no such evidence or rec is
// not release-bound. It costs an Lstat per path and a gitfile read per
// checkout.
//
// A missing working directory under a checkout that is still registered (a
// subdirectory a branch switch or a clean removed) is not evidence: the
// checkout lives, and the start fails and can be retried as before. Neither
// is a path whose parent is missing too (an unavailable drive or share reads
// as not found), nor, once loaded after the removal, a worktree that lay
// inside another live checkout (an in-repository pool): that checkout
// contains its path. A checkout emptied in place, with its .git entry deleted but
// its directory kept (a removal a process's working directory blocked on
// Windows), leaves no checkout above cwd to compare: it is not detected
// here; the persisted release closure and the finished-child sleep keep
// such a conversation from launching there.
func goneBinding(rec Record, root, cwd string, checkouts []string) string {
	if !releaseBound(rec) {
		return ""
	}
	for _, checkout := range checkouts {
		if wsgit.CheckoutRegistrationGone(checkout) {
			return checkout
		}
	}
	for _, path := range []string{root, cwd} {
		if !slices.ContainsFunc(checkouts, func(checkout string) bool { return pathWithin(checkout, path) }) && removedDir(path) {
			return path
		}
	}
	return ""
}

// pathWithin reports whether path is dir or lies under it; Windows paths
// compare without case, as sameWorktreePath does.
func pathWithin(dir, path string) bool {
	if dir == "" || path == "" {
		return false
	}
	dir, path = filepath.Clean(dir), filepath.Clean(path)
	if runtime.GOOS == "windows" {
		dir, path = strings.ToLower(dir), strings.ToLower(path)
	}
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

// removedDir reports whether path no longer exists while its parent
// directory does: the evidence of a removal rather than of an unreachable
// volume, whose parents read as missing as well.
func removedDir(path string) bool {
	if path == "" {
		return false
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return false
	}
	path = filepath.Clean(path)
	parent := filepath.Dir(path)
	if parent == path {
		return false
	}
	_, err := os.Lstat(parent)
	return err == nil
}

// deadCheckoutRefusal is the refusal of a binding checkout ended: removed
// (checkoutGoneRefusal) when it no longer exists as a checkout, else
// released (checkoutReleasedRefusal).
func deadCheckoutRefusal(checkout string) *admissionRefusal {
	if _, err := os.Lstat(checkout); errors.Is(err, os.ErrNotExist) || wsgit.CheckoutRegistrationGone(checkout) {
		return checkoutGoneRefusal(checkout)
	}
	return checkoutReleasedRefusal(checkout)
}

// checkoutGoneRefusal is the start refusal of a conversation whose working
// directory or checkout no longer exists: the checkout was removed (or
// unregistered) after the conversation was bound there. Like a release, it
// ends the binding for good.
func checkoutGoneRefusal(path string) *admissionRefusal {
	return &admissionRefusal{
		reason:   fmt.Sprintf("its worktree %s no longer exists as a checkout (removed or unregistered after it was bound there), so it no longer launches, wakes or resumes there. Spawn a new agent (into a re-acquired worktree if needed) to continue", path),
		checkout: path,
	}
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

// releasedOnDiskLocked is releasedLocked for a cold path (a process start):
// it reads the checkouts' markers rather than trusting memory alone, so a
// release whose notice never reached this daemon still refuses the start.
// Only a release found is kept. c.mu is held.
func (c *conv) releasedOnDiskLocked() string {
	if c.releasedCheckout == "" {
		c.releasedCheckout = c.r.releasedOnDisk(c.rec, c.checkouts)
	}
	return c.releasedCheckout
}

// noteReleased records, in memory, that checkout's release ended this
// conversation's binding. It waits for an input the loop admitted before it
// to reach the backend (admitInput), so once it returns no input admitted
// before the release is still on its way.
func (c *conv) noteReleased(checkout string) {
	if checkout == "" {
		return
	}
	c.inputMu.Lock()
	c.setStatus(func() { c.releasedCheckout = checkout })
	c.inputMu.Unlock()
	c.watched()
}

// admitInput runs write, the step that hands a turn or a steer to the
// backend, unless a release ended this conversation's binding; it then
// returns that checkout and write does not run. The check is the last one
// before the backend sees the input, and it and write share inputMu with
// noteReleased: a release notice either marks the conversation first, and
// the input is refused, or waits until write returned. Every earlier check
// is only a snapshot. It runs in the loop.
func (c *conv) admitInput(write func()) (refusedBy string) {
	c.inputMu.Lock()
	defer c.inputMu.Unlock()
	if checkout := c.released(); checkout != "" {
		return checkout
	}
	write()
	return ""
}

// inputReleasedError is the refusal of an automatic input (a compaction
// command, nudge, continuation or restore turn) whose last release check
// (admitInput) found the binding ended: nothing reached the backend. It is
// no send failure, so its callers drop the input quietly (dropReleasedInput)
// rather than record an error or stop the process.
type inputReleasedError struct{ checkout string }

func (e *inputReleasedError) Error() string {
	return fmt.Sprintf("its worktree %s was released after it was bound there", e.checkout)
}

// releasedInput reports the checkout of err's release refusal, when err is
// one (inputReleasedError).
func releasedInput(err error) (string, bool) {
	var refused *inputReleasedError
	if errors.As(err, &refused) {
		return refused.checkout, true
	}
	return "", false
}

// dropReleasedInput logs an automatic input a release refused; the process
// is not stopped here: the release's own close (closeDeadBinding) ends a
// held one.
func (c *conv) dropReleasedInput(what, checkout string) {
	fmt.Fprintf(c.r.opts.Log, "agent %s: no %s: its worktree %s was released\n", c.rec.ReplyID, what, checkout)
}

// ReleasedConversation is one spawned conversation whose binding a release
// ended (Runner.CheckoutReleased): its address and state, and, for a forced
// release, whether its running turn was asked to stop and the backend's
// interrupt error when that failed. The close that follows ends a process
// held under a session holder whether or not the interrupt succeeded
// (closeDeadBinding).
type ReleasedConversation struct {
	Address   string `json:"address"`
	State     string `json:"state"`
	Cancelled bool   `json:"cancelled,omitempty"`
	Error     string `json:"error,omitempty"`
}

// CheckoutReleased is worktree.release's notice, after it recorded worktree's
// release marker at releasedAt (zero: read the marker, for a notice that does
// not carry it). The runner keeps that release in memory, and every
// non-closed spawned conversation bound to worktree before it is closed,
// so it takes no further input there; a process it holds under a session
// holder is ended with the close, so none keeps its working directory in
// the released checkout. With cancelTurns (a forced release by a root lead)
// each such conversation's running turn is interrupted through the
// turn-cancellation path first; an interrupt error is reported.
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
		var cancelErr, closeErr error
		ran := c.do(func() {
			if c.closed || c.crossHarnessRejoin && c.rec.HolderID != "" {
				return
			}
			if cancelTurns {
				entry.Cancelled, cancelErr = c.cancelTurn()
				if cancelErr != nil {
					entry.Cancelled = false
				}
			}
			closeErr = c.closeDeadBinding(checkoutReleasedRefusal(target))
		})
		if cancelErr != nil {
			entry.Error = cancelErr.Error()
		}
		if closeErr != nil {
			fmt.Fprintf(r.opts.Log, "agent %s: %v\n", rec.ReplyID, closeErr)
		}
		state := c.memoryState()
		if ran && closeErr == nil && state.Status == StatusClosed {
			r.releaseParent(rec.ReplyID)
		}
		entry.State = AgentStatus(state)
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
	// A parked child is open: it is rehydrated so the release (or the
	// reservation) meets it as it meets a resident one.
	return append(out, r.boundParkedConvs(target)...)
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
