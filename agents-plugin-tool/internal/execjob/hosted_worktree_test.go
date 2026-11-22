package execjob

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestHostedLiveInWorktreeMatchesRootOrCwd: worktree.release and acquisition
// see a running job whose root is the worktree or whose working directory is
// in it; finished jobs and jobs elsewhere do not count.
func TestHostedLiveInWorktreeMatchesRootOrCwd(t *testing.T) {
	base := t.TempDir()
	wt := filepath.Join(base, "pool", "wt")
	other := filepath.Join(base, "pool", "wt-other")
	main := filepath.Join(base, "main")
	for _, dir := range []string{filepath.Join(wt, "sub"), other, main} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exit := 0
	h := &Hosted{jobs: map[string]*hostedJob{
		"exec_root":     {row: HostedJobRow{ID: "exec_root", Root: wt, Cwd: wt, State: HostedRunning}},
		"exec_cwd":      {row: HostedJobRow{ID: "exec_cwd", Root: main, Cwd: filepath.Join(wt, "sub"), State: HostedRunning}},
		"exec_done":     {row: HostedJobRow{ID: "exec_done", Root: wt, Cwd: wt, State: "exited", ExitCode: &exit, EndedAt: 1}},
		"exec_sibling":  {row: HostedJobRow{ID: "exec_sibling", Root: other, Cwd: other, State: HostedRunning}},
		"exec_mainonly": {row: HostedJobRow{ID: "exec_mainonly", Root: main, Cwd: main, State: HostedRunning}},
	}}
	if got, want := h.LiveInWorktree(wt), []string{"exec_cwd", "exec_root"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("LiveInWorktree = %v, want %v", got, want)
	}
	if got := h.LiveInWorktree(filepath.Join(base, "pool", "absent")); len(got) != 0 {
		t.Fatalf("no job runs in an unrelated worktree: %v", got)
	}
	var none *Hosted
	if got := none.LiveInWorktree(wt); got != nil {
		t.Fatalf("a daemon without hosted exec has none: %v", got)
	}
}
