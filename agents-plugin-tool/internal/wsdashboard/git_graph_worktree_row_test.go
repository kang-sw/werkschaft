package wsdashboard

import (
	"os/exec"
	"testing"
)

// TestGitGraphWorktreeRow runs the Git page graph's working-tree row cases
// (testdata/git-graph-worktree-row.test.cjs): the slot shift above HEAD,
// the hollow node's geometry (HEAD's column, a stub lane, alone at slot 0
// for an unborn or unloaded HEAD), the row on an empty log, the status
// signature's no-op and in-place patch, "300+" for a cut group, and the
// row's open path never reading the loaded commits.
func TestGitGraphWorktreeRow(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; run node --test testdata/git-graph-worktree-row.test.cjs locally")
	}
	out, err := exec.Command(node, "--test", "testdata/git-graph-worktree-row.test.cjs").CombinedOutput()
	if err != nil {
		t.Fatalf("working-tree row cases failed: %v\n%s", err, out)
	}
	t.Log(string(out))
}
