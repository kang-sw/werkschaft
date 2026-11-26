package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsgit"
)

// worktreeRemoveResult is the return shape of worktree.remove.
type worktreeRemoveResult struct {
	Path    string `json:"path"`
	Removed bool   `json:"removed"`
	Profile string `json:"profile"`
	// Branch is the branch the worktree had checked out; remove keeps it.
	Branch string            `json:"branch,omitempty"`
	Hooks  []worktreeHookRun `json:"hooks,omitempty"`
	Notes  []string          `json:"notes,omitempty"`
}

func (r worktreeRemoveResult) text() string {
	text := fmt.Sprintf("removed: %s\nprofile: %s\n", r.Path, r.Profile)
	if r.Branch != "" {
		text += "branch kept: " + r.Branch + "\n"
	}
	for _, h := range r.Hooks {
		text += h.text()
	}
	for _, n := range r.Notes {
		text += "note: " + n + "\n"
	}
	return text
}

// removeWorktree deletes a pooled worktree: every refusal is checked first,
// then the marker's profile's pre_remove hooks run, then the checkout is moved
// into its pool root's trash and the worktree unregistered together with its
// admin directory (lease and marker included); both are deleted in the
// background (retireRegisteredWorktree). The checked-out branch is kept, so
// the refusals guard unmerged work: a dirty tree, a branch holding commits no
// other branch reaches, a detached HEAD holding commits no ref reaches, and a
// lease whose activity is newer than staleAfter (a live worker's tree). It
// also refuses what release refuses: the primary worktree and any path
// outside the owned pool.
func removeWorktree(ctx context.Context, runner wsgit.Runner, env []string, wtPath, poolConfigValue string, staleAfter time.Duration, now time.Time, lookup worktreeProfileLookup) (worktreeRemoveResult, error) {
	return removeWorktreeFenced(ctx, runner, env, wtPath, poolConfigValue, staleAfter, now, lookup, nil, nil, "")
}

// removeWorktreeFenced is removeWorktree that first reserves the target
// against exec jobs (worktreeExecFence): a live job refuses the removal,
// and none starts there until it is done. It then asks occupancy (nil: no
// daemon to ask, so not occupied) which agents occupy the target and refuses
// while any does, or when the daemon cannot answer, before any other check,
// hook, or change.
//
// callerRoot is the caller's repository root (empty: none known). When git
// cannot resolve wtPath as a registered pooled worktree, a directory inside
// that repository's owned pool that is no longer registered (left behind by
// an interrupted removal) is cleaned up instead, under the same fence and
// occupancy checks (removeOrphanPoolDir).
func removeWorktreeFenced(ctx context.Context, runner wsgit.Runner, env []string, wtPath, poolConfigValue string, staleAfter time.Duration, now time.Time, lookup worktreeProfileLookup, fenceExec func(string) (func(), error), occupancy func(context.Context, string) (worktreeOccupancy, error), callerRoot string) (worktreeRemoveResult, error) {
	var res worktreeRemoveResult
	target, entry, mainRoot, err := ownedPoolWorktree(ctx, runner, wtPath, poolConfigValue, "worktree.remove")
	if err != nil {
		orphan, ok, orphanErr := resolveOrphanPoolDir(ctx, runner, callerRoot, wtPath, poolConfigValue)
		if orphanErr != nil {
			return res, orphanErr
		}
		if !ok {
			return res, err
		}
		unfence, err := removeOccupancyCheck(ctx, orphan.target, fenceExec, occupancy)
		if err != nil {
			return res, err
		}
		defer unfence()
		return removeOrphanPoolDir(ctx, runner, callerRoot, orphan)
	}
	unfence, err := removeOccupancyCheck(ctx, target, fenceExec, occupancy)
	if err != nil {
		return res, err
	}
	defer unfence()
	res.Path = target
	if !entry.Detached {
		res.Branch = strings.TrimPrefix(entry.Branch, "refs/heads/")
	}

	dirty, err := wsgit.PorcelainPaths(ctx, runner, target)
	if err != nil {
		return res, fmt.Errorf("worktree.remove: read the status of %s: %w", target, err)
	}
	if len(dirty) > 0 {
		if len(dirty) > 5 {
			dirty = append(dirty[:5], "...")
		}
		return res, fmt.Errorf("worktree.remove refuses %s: it has uncommitted changes or untracked files (%s); commit or discard them first", target, strings.Join(dirty, ", "))
	}

	if res.Branch != "" {
		n, err := wsgit.CountUnreachedCommits(ctx, runner, target, "refs/heads/"+res.Branch, "--exclude="+res.Branch, "--branches", "--remotes")
		if err != nil {
			return res, fmt.Errorf("worktree.remove: %w", err)
		}
		if n > 0 {
			return res, fmt.Errorf("worktree.remove refuses %s: its branch %q holds %d commit(s) no other branch reaches; merge them first (remove keeps the branch, but the unmerged work loses its checkout)", target, res.Branch, n)
		}
	} else {
		n, err := wsgit.CountUnreachedCommits(ctx, runner, target, "HEAD", "--branches", "--tags", "--remotes")
		if err != nil {
			return res, fmt.Errorf("worktree.remove: %w", err)
		}
		if n > 0 {
			return res, fmt.Errorf("worktree.remove refuses %s: its detached HEAD holds %d commit(s) no ref reaches; put them on a branch or discard them first", target, n)
		}
	}

	lease, err := readWorktreeLease(ctx, runner, target)
	if err != nil {
		return res, fmt.Errorf("worktree.remove: %w", err)
	}
	if lease != nil {
		activity, err := worktreeActivity(ctx, runner, target, lease)
		if err != nil {
			return res, fmt.Errorf("worktree.remove refuses %s: cannot tell how recently its leaseholder was active: %w", target, err)
		}
		if now.Sub(activity) < staleAfter {
			return res, fmt.Errorf("worktree.remove refuses %s: it is leased to worker %s, last active %s, within the worktree.nudge_stale_days threshold; release it with worktree.release once its holder is done", target, lease.WorkerKey, formatFactTime(activity))
		}
	}

	// Resolve where the checkout and its admin directory retire to, and
	// refuse a locked worktree (as git worktree remove --force does), before
	// any hook runs.
	poolRoot := containingPoolRoot(ownedPoolRoots(poolConfigValue, mainRoot), target)
	if poolRoot == "" || poolRoot == target {
		return res, fmt.Errorf("worktree.remove refuses %s: it is a worktree pool root itself, so it has no pool trash to move into. Nothing was changed", target)
	}
	adminDir, commonDir, err := worktreeAdminLayout(ctx, runner, target, mainRoot)
	if err != nil {
		return res, fmt.Errorf("worktree.remove refuses %s: %v. Nothing was changed", target, err)
	}
	if reason, err := os.ReadFile(filepath.Join(adminDir, "locked")); err == nil {
		why := ""
		if r := strings.TrimSpace(string(reason)); r != "" {
			why = " (" + r + ")"
		}
		return res, fmt.Errorf("worktree.remove refuses %s: it is locked%s; unlock it with git worktree unlock, then remove again. Nothing was changed", target, why)
	}

	marker, err := readWorktreeMarker(ctx, runner, target)
	if err != nil {
		return res, fmt.Errorf("worktree.remove: %w", err)
	}
	if marker == nil {
		res.Profile = worktreeLegacyLabel
	} else {
		res.Profile = marker.Profile
		profile, found, err := lookup(marker.Profile)
		if err != nil {
			return res, fmt.Errorf("worktree.remove: resolve profile %q of %s: %w", marker.Profile, target, err)
		}
		switch {
		case !found:
			res.Notes = append(res.Notes, fmt.Sprintf("profile %q no longer resolves; its pre_remove hooks were skipped", marker.Profile))
		case profile.Definition.Hooks != nil:
			runs, failure := runWorktreeHooks(ctx, worktreeHookPreRemove, profile.Definition.Hooks.PreRemove, profile.Scope, worktreeHookEnv{WorktreeDir: target, TargetBranch: res.Branch, Environ: env})
			res.Hooks = runs
			if failure != nil {
				return res, fmt.Errorf("worktree.remove: profile %q: %w. %s was not removed; fix the hook or the profile (config.tune), then remove again", marker.Profile, failure, target)
			}
		}
		if marker.Quarantine != nil {
			res.Notes = append(res.Notes, fmt.Sprintf("cleared its quarantine (%s hook failed at %s)", marker.Quarantine.Point, marker.Quarantine.At))
		}
	}

	// The refusals above guard uncommitted and unmerged work, standing in for
	// git's own checks (which also refuse initialized submodules); their Git
	// directories live in the admin directory and leave with it.
	aside, err := retireRegisteredWorktree(target, poolRoot, adminDir, commonDir)
	if err != nil {
		return res, err
	}
	res.Removed = true
	res.Notes = append(res.Notes, "its files are deleted in the background from "+aside)
	return res, nil
}

// removeOccupancyCheck takes worktree.remove's exec fence on target and
// refuses while agents occupy it or the daemon cannot answer; the returned
// unfence is never nil.
func removeOccupancyCheck(ctx context.Context, target string, fenceExec func(string) (func(), error), occupancy func(context.Context, string) (worktreeOccupancy, error)) (func(), error) {
	unfence := func() {}
	if fenceExec != nil {
		u, err := fenceExec(target)
		if err != nil {
			return unfence, fmt.Errorf("worktree.remove refuses %s: %w", target, err)
		}
		unfence = u
	}
	if occupancy == nil {
		return unfence, nil
	}
	occ, err := occupancy(ctx, target)
	if err != nil {
		unfence()
		return func() {}, fmt.Errorf("worktree.remove refuses %s: could not check bound agents with the daemon (%v). Nothing was changed. Retry once the daemon answers", target, err)
	}
	if occupants := occupyingChildren(occ.Children); len(occupants) > 0 {
		parts := make([]string, 0, len(occupants))
		for _, c := range occupants {
			part := c.Address + " " + c.State
			if c.Holder != "" {
				part += " holder=" + c.Holder
			}
			if c.PID != 0 {
				part += " pid=" + strconv.Itoa(c.PID)
			}
			if c.Cwd != "" {
				part += " cwd=" + c.Cwd
			}
			parts = append(parts, strings.TrimSpace(part))
		}
		unfence()
		return func() {}, fmt.Errorf("worktree.remove refuses %s: agent(s) still occupy this worktree (%s). Nothing was changed. Close or release the agents (worktree.release with force: true stops a released worktree's agents), then remove again", target, strings.Join(parts, "; "))
	}
	return unfence, nil
}

// worktreeActivity is a worktree's last activity: the later of its lease's
// acquired_at (when leased) and its HEAD commit time.
func worktreeActivity(ctx context.Context, runner wsgit.Runner, wtPath string, lease *worktreeLease) (time.Time, error) {
	secs, err := wtRun(ctx, runner, wtPath, "log", "-1", "--format=%ct", "HEAD")
	if err != nil {
		return time.Time{}, err
	}
	n, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse HEAD commit time %q: %v", secs, err)
	}
	activity := time.Unix(n, 0)
	if lease != nil {
		acquired, err := time.Parse(time.RFC3339, lease.AcquiredAt)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse lease acquired_at %q: %v", lease.AcquiredAt, err)
		}
		if acquired.After(activity) {
			activity = acquired
		}
	}
	return activity, nil
}
