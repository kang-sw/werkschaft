package execjob

import (
	"fmt"
	"path/filepath"
	"sync"
)

// legacy_fence.go serializes the file-backed (legacy) launches of this
// process with a worktree release or removal: a launch reserves its root
// before its process starts and holds the reservation until the job is in
// the set ActiveRoots counts, and FenceLegacyRoot counts both under the
// same lock as it fences, so a release never sees zero while a launch is
// between its admission and its active entry. A launch or job counts in a
// worktree that holds its root or its working directory (a call rooted at
// the primary checkout may run in a nested pooled worktree), once.
var legacyAdmission struct {
	mu sync.Mutex
	// reserved holds the launches past admission and not yet active, each
	// as its cleaned root and working directory.
	reserved map[*legacyLaunch]struct{}
	// fences holds the live fences on each form of a fenced target, oldest
	// first; a refusal names the latest one's reason.
	fences map[string]fenceHolds
}

// The reasons a worktree fence gives when it refuses a new job.
const (
	FenceReleasing = "being released or removed"
	FenceAcquiring = "being acquired (checked out for a new holder)"
)

// legacyLaunch is a reserved launch's places: its root and working
// directory, and their resolved spellings (realPlaces).
type legacyLaunch struct {
	root, wd string
	real     []string
}

// beforeLegacyActive, when set (tests only), runs after a legacy job's
// process started and before it enters the active set.
var beforeLegacyActive func()

// reserveLegacy admits a legacy launch into root with working directory wd
// (real: their resolved spellings), refused when any of them lies in a
// fenced worktree. The returned release is idempotent.
func reserveLegacy(root, wd string, real []string) (func(), error) {
	root = filepath.Clean(root)
	places := append([]string{root, wd}, real...)
	legacyAdmission.mu.Lock()
	defer legacyAdmission.mu.Unlock()
	for fenced, holds := range legacyAdmission.fences {
		if placesIn([]string{fenced}, places) {
			return nil, fmt.Errorf("exec: %s is %s; launch elsewhere", fenced, holds.reason())
		}
	}
	if legacyAdmission.reserved == nil {
		legacyAdmission.reserved = map[*legacyLaunch]struct{}{}
	}
	launch := &legacyLaunch{root: root, wd: filepath.Clean(wd), real: real}
	legacyAdmission.reserved[launch] = struct{}{}
	var once sync.Once
	return func() {
		once.Do(func() {
			legacyAdmission.mu.Lock()
			defer legacyAdmission.mu.Unlock()
			delete(legacyAdmission.reserved, launch)
		})
	}, nil
}

// FenceLegacyRoot reserves worktree target for a release or removal
// against this process's legacy jobs: refused while one runs, or is being
// launched, in it (its root or working directory at or beneath target, in
// either symlink form);
// otherwise new legacy launches there are refused until the returned
// unfence (idempotent) runs.
func FenceLegacyRoot(target string) (func(), error) {
	return FenceLegacyRootAs(target, FenceReleasing)
}

// FenceLegacyRootAs is FenceLegacyRoot whose refusal of a new launch names
// reason; while fences with different reasons overlap, a refusal names the
// latest one still held.
func FenceLegacyRootAs(target, reason string) (func(), error) {
	forms := rootForms(target)
	legacyAdmission.mu.Lock()
	defer legacyAdmission.mu.Unlock()
	n := 0
	inTarget := func(root string) bool {
		for _, f := range forms {
			if within(f, root) {
				return true
			}
		}
		return false
	}
	active.Range(func(_, v any) bool {
		if job := v.(*activeJob); inTarget(filepath.Clean(job.root)) || (job.wd != "" && inTarget(filepath.Clean(job.wd))) || placesIn(forms, job.real) {
			n++
		}
		return true
	})
	for launch := range legacyAdmission.reserved {
		if inTarget(launch.root) || inTarget(launch.wd) || placesIn(forms, launch.real) {
			n++
		}
	}
	if n > 0 {
		return nil, fmt.Errorf("%d exec job(s) still run in it; abort them with exec.abort, or let them finish, first", n)
	}
	if legacyAdmission.fences == nil {
		legacyAdmission.fences = map[string]fenceHolds{}
	}
	hold := &fenceHold{reason: reason}
	for _, f := range forms {
		legacyAdmission.fences[f] = append(legacyAdmission.fences[f], hold)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			legacyAdmission.mu.Lock()
			defer legacyAdmission.mu.Unlock()
			for _, f := range forms {
				if holds := legacyAdmission.fences[f].drop(hold); len(holds) > 0 {
					legacyAdmission.fences[f] = holds
				} else {
					delete(legacyAdmission.fences, f)
				}
			}
		})
	}, nil
}
