package wsdashboard

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The repository side of the rail: the worktree list read of one repository,
// pooled-worktree and path tests, and the repository root of a session.

// railRepo is what the worktree lister holds of one repository.
type railRepo struct {
	// Read reports a successful list; Entries are its worktrees, the root
	// worktree first.
	Read    bool
	Entries []railWorktreeEntry
	// Pools are the repository's worktree pool roots.
	Pools []string
}

// railWorktreeEntry is one worktree git lists.
type railWorktreeEntry struct {
	Path string
	// Branch is the short branch name; empty with Detached, or a bare entry.
	Branch                                       string
	Detached                                     bool
	Prunable                                     bool
	AdmissionRepository, AdmissionPath, Revision string
}

// railPathUnder reports whether child is at or below parent, at directory
// boundaries.
func railPathUnder(parent, child string) bool {
	if parent == child {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(child, strings.TrimSuffix(parent, sep)+sep)
}

// railPooled reports a folder the rail hides: one at or below a ws
// worktree pool root of the repository at root (its configured pools and
// the default and legacy ones, worktreePoolRoots, so a session shows no
// pooled row before the list is read either), never root itself.
func railPooled(repo railRepo, root, path string) bool {
	path = filepath.Clean(path)
	return path != root && worktreePooled(worktreePoolRoots(repo.Pools, root), path)
}

// railRepoRoot is the repository root of a session: from its registered git
// common directory when it has one (a non-bare repository's common
// directory is <root>/.git; a bare repository's or a submodule's is not,
// and such a session shows as a plain folder), else from its root's .git
// read without running git (railFallbackRoot). Empty for a plain folder.
func railRepoRoot(reg Registration) string {
	if c := reg.GitCommonDir; c != "" {
		c = filepath.Clean(c)
		if filepath.Base(c) != ".git" {
			return ""
		}
		return filepath.Dir(c)
	}
	return railFallbackRoot(reg.Project)
}

// railFallbacks memoizes railFallbackRoot per path, as worktreeMains does.
var railFallbacks sync.Map // path -> string

// railFallbackRoot is the repository root of path for a registration
// without a common directory: its main repository when path is a linked
// worktree (worktreeMain), path itself when it holds a .git directory, else
// empty. Memoized; git is never run.
func railFallbackRoot(path string) string {
	if path == "" {
		return ""
	}
	if v, ok := railFallbacks.Load(path); ok {
		return v.(string)
	}
	root := worktreeMain(path)
	if root == "" {
		if info, err := os.Stat(filepath.Join(path, ".git")); err == nil && info.IsDir() {
			root = filepath.Clean(path)
		}
	}
	railFallbacks.Store(path, root)
	return root
}
