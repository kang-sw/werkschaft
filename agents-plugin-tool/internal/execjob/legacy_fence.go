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
// between its admission and its active entry.
var legacyAdmission struct {
	mu sync.Mutex
	// reserved counts the launches past admission and not yet active, by
	// cleaned root.
	reserved map[string]int
	// fences counts the fences on each form of a fenced target.
	fences map[string]int
}

// beforeLegacyActive, when set (tests only), runs after a legacy job's
// process started and before it enters the active set.
var beforeLegacyActive func()

// reserveLegacy admits a legacy launch into root with working directory wd,
// refused when either lies in a fenced worktree. The returned release is
// idempotent.
func reserveLegacy(root, wd string) (func(), error) {
	root = filepath.Clean(root)
	legacyAdmission.mu.Lock()
	defer legacyAdmission.mu.Unlock()
	for fenced := range legacyAdmission.fences {
		if within(fenced, root) || within(fenced, wd) {
			return nil, fmt.Errorf("exec: %s is being released or removed; launch elsewhere", fenced)
		}
	}
	if legacyAdmission.reserved == nil {
		legacyAdmission.reserved = map[string]int{}
	}
	legacyAdmission.reserved[root]++
	var once sync.Once
	return func() {
		once.Do(func() {
			legacyAdmission.mu.Lock()
			defer legacyAdmission.mu.Unlock()
			if legacyAdmission.reserved[root]--; legacyAdmission.reserved[root] <= 0 {
				delete(legacyAdmission.reserved, root)
			}
		})
	}, nil
}

// FenceLegacyRoot reserves worktree target for a release or removal
// against this process's legacy jobs: refused while one runs, or is being
// launched, in it (its root at or beneath target, in either symlink form);
// otherwise new legacy launches there are refused until the returned
// unfence (idempotent) runs.
func FenceLegacyRoot(target string) (func(), error) {
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
		if inTarget(filepath.Clean(v.(*activeJob).root)) {
			n++
		}
		return true
	})
	for root, c := range legacyAdmission.reserved {
		if inTarget(root) {
			n += c
		}
	}
	if n > 0 {
		return nil, fmt.Errorf("%d exec job(s) still run in it; abort them with exec.abort, or let them finish, first", n)
	}
	if legacyAdmission.fences == nil {
		legacyAdmission.fences = map[string]int{}
	}
	for _, f := range forms {
		legacyAdmission.fences[f]++
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			legacyAdmission.mu.Lock()
			defer legacyAdmission.mu.Unlock()
			for _, f := range forms {
				if legacyAdmission.fences[f]--; legacyAdmission.fences[f] <= 0 {
					delete(legacyAdmission.fences, f)
				}
			}
		})
	}, nil
}
