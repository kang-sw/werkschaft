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
// root and cwd are rec's canonical Root and Cwd (conv.canonRoot,
// conv.canonCwd).
func resolveCheckouts(rec Record, root, cwd string) []string {
	if !releaseBound(rec) {
		return nil
	}
	return bindingCheckouts(root, cwd)
}

// bindingCheckouts lists the checkouts a conversation with canonical root and
// working directory cwd is bound to, by exactly the rule live occupancy uses
// (boundConvs): root when it is a checkout, and every checkout that contains
// cwd, so a working directory inside a submodule of a pooled worktree
// reaches that worktree. It costs one Lstat per ancestor of cwd.
func bindingCheckouts(root, cwd string) []string {
	var out []string
	add := func(p string) {
		for _, have := range out {
			if sameWorktreePath(have, p) {
				return
			}
		}
		out = append(out, p)
	}
	if root != "" {
		if _, err := os.Lstat(filepath.Join(root, ".git")); err == nil {
			add(root)
		}
	}
	for _, checkout := range wsgit.ContainingCheckouts(cwd) {
		add(checkout)
	}
	return out
}

// reserveSpawnCheckouts takes the shared per-worktree release reservation
// (wsgit.ReserveWorktreeSpawn) of every pooled worktree a spawn binds its
// child to: the explicit worktree when one was named, and each linked
// worktree among the checkouts holding the child's root or working directory
// (bindingCheckouts). It is held until the child is registered, so a release
// or an acquisition's claim, which take the reservation exclusively, never
// checks occupancy between this spawn's admission and its registration.
func reserveSpawnCheckouts(tool, explicit, root, cwd string) (func(), error) {
	targets := []string{}
	if explicit != "" {
		targets = append(targets, explicit)
	}
	for _, checkout := range bindingCheckouts(canonicalWorktreePath(root), canonicalWorktreePath(cwd)) {
		if wsgit.IsLinkedWorktree(checkout) {
			targets = append(targets, checkout)
		}
	}
	var unlocks []func()
	unlockAll := func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
	seen := []string{}
	for _, target := range targets {
		canon := canonicalWorktreePath(target)
		if slices.ContainsFunc(seen, func(p string) bool { return sameWorktreePath(p, canon) }) {
			continue
		}
		seen = append(seen, canon)
		unlock, err := wsgit.ReserveWorktreeSpawn(target)
		if err != nil {
			unlockAll()
			if errors.Is(err, wsgit.ErrWorktreeReleasing) {
				return nil, conflict("%s: worktree %s is being released or re-acquired right now; wait for that to finish (re-acquire it if it was released) before spawning there", tool, target)
			}
			return nil, badRequest("%s: reserve worktree %s: %v", tool, target, err)
		}
		unlocks = append(unlocks, unlock)
	}
	return unlockAll, nil
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
	checkouts := resolveCheckouts(rec, canonicalWorktreePath(rec.Root), canonicalWorktreePath(rec.Cwd))
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

// boundConvs lists the open conversations whose canonical root is target or
// whose working directory is target or below it. It reads memory only.
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
		if sameWorktreePath(c.canonRoot, target) || worktreePathContains(target, c.canonCwd) {
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
