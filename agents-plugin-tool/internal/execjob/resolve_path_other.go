//go:build !windows

package execjob

import "path/filepath"

// resolvePath is p with every symlink resolved: the one form the worktree
// fence compares a job's places and a worktree in. p must exist.
func resolvePath(p string) (string, error) { return filepath.EvalSymlinks(p) }

// checkOrdinarySpelling has nothing to check here: a resolved path is the
// spelling the process starts in.
func checkOrdinarySpelling(string) error { return nil }
