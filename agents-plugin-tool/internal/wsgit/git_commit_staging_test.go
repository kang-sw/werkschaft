// Tests for Git commit staging, including sparse-checkout behavior.
package wsgit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCommitExpandsTicketMovePathsByStem(t *testing.T) {
	preStatus := ParseStatus([]byte("1 .D N... 100644 100644 100644 aaa bbb ai-docs/tickets/todo/260503-feat-demo.md\n? ai-docs/tickets/.done/260503-feat-demo.md\n"))
	paths := expandCommitPathsForTicketMoves(preStatus, []string{"ai-docs/tickets/.done/260503-feat-demo.md"})
	want := []string{"ai-docs/tickets/.done/260503-feat-demo.md", "ai-docs/tickets/todo/260503-feat-demo.md"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
}

func TestCommitExpandsTodoToReadyTicketMovePathsByStem(t *testing.T) {
	preStatus := ParseStatus([]byte("1 .D N... 100644 100644 100644 aaa bbb ai-docs/tickets/todo/260503-feat-demo.md\n? ai-docs/tickets/ready/260503-feat-demo.md\n"))
	paths := expandCommitPathsForTicketMoves(preStatus, []string{"ai-docs/tickets/ready/260503-feat-demo.md"})
	want := []string{"ai-docs/tickets/ready/260503-feat-demo.md", "ai-docs/tickets/todo/260503-feat-demo.md"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
}

func TestFilterIndexDeleteSidePathsDropsRenameOldPath(t *testing.T) {
	status := ParseStatus([]byte("2 R. N... 100644 100644 100644 aaa bbb R100 ai-docs/tickets/ready/260503-feat-demo.md\tai-docs/tickets/todo/260503-feat-demo.md\n"))
	got := filterIndexDeleteSidePaths(status, []string{"ai-docs/tickets/ready/260503-feat-demo.md", "ai-docs/tickets/todo/260503-feat-demo.md"})
	want := []string{"ai-docs/tickets/ready/260503-feat-demo.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterIndexDeleteSidePaths = %#v, want %#v", got, want)
	}
}

func TestFilterIndexDeleteSidePathsDropsCloseRenameOldPath(t *testing.T) {
	status := ParseStatus([]byte("2 R. N... 100644 100644 100644 aaa bbb R100 ai-docs/tickets/.dropped/260503-feat-demo.md\tai-docs/tickets/todo/260503-feat-demo.md\n"))
	got := filterIndexDeleteSidePaths(status, []string{"ai-docs/tickets/.dropped/260503-feat-demo.md", "ai-docs/tickets/todo/260503-feat-demo.md"})
	want := []string{"ai-docs/tickets/.dropped/260503-feat-demo.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterIndexDeleteSidePaths = %#v, want %#v", got, want)
	}
}

func TestFilterIndexDeleteSidePathsDropsOutrightDeletion(t *testing.T) {
	status := ParseStatus([]byte("1 D. N... 100644 000000 000000 aaa 0000000000000000000000000000000000000000 ai-docs/tickets/idea/260503-feat-demo.md\n"))
	got := filterIndexDeleteSidePaths(status, []string{"ai-docs/tickets/idea/260503-feat-demo.md"})
	if len(got) != 0 {
		t.Fatalf("filterIndexDeleteSidePaths = %#v, want empty", got)
	}
}

func TestFilterIndexDeleteSidePathsKeepsUnrelatedContentEdit(t *testing.T) {
	status := ParseStatus([]byte("1 M. N... 100644 100644 100644 aaa bbb ai-docs/tickets/todo/260503-feat-other.md\n"))
	got := filterIndexDeleteSidePaths(status, []string{"ai-docs/tickets/todo/260503-feat-other.md"})
	want := []string{"ai-docs/tickets/todo/260503-feat-other.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterIndexDeleteSidePaths = %#v, want %#v", got, want)
	}
}

func TestFilterIndexDeleteSidePathsKeepsCopySourcePath(t *testing.T) {
	status := ParseStatus([]byte("2 C. N... 100644 100644 100644 aaa bbb C100 ai-docs/tickets/ready/260503-feat-demo.md\tai-docs/tickets/todo/260503-feat-demo.md\n"))
	got := filterIndexDeleteSidePaths(status, []string{"ai-docs/tickets/ready/260503-feat-demo.md", "ai-docs/tickets/todo/260503-feat-demo.md"})
	want := []string{"ai-docs/tickets/ready/260503-feat-demo.md", "ai-docs/tickets/todo/260503-feat-demo.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterIndexDeleteSidePaths = %#v, want %#v (copy source must not be filtered)", got, want)
	}
}

func TestCommitStagesDeletedTicketMoveByParentDirectory(t *testing.T) {
	preStatus := ParseStatus([]byte("1 .D N... 100644 100644 100644 aaa bbb ai-docs/tickets/todo/260503-feat-demo.md\n? ai-docs/tickets/.done/260503-feat-demo.md\n"))
	got := stagingCommandsForCommit([]string{"ai-docs/tickets/.done/260503-feat-demo.md", "ai-docs/tickets/todo/260503-feat-demo.md"}, preStatus, false)
	want := [][]string{
		{"add", "-A", "--", "ai-docs/tickets/.done/260503-feat-demo.md"},
		{"rm", "--cached", "--ignore-unmatch", "--", "ai-docs/tickets/todo/260503-feat-demo.md"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("staging commands = %#v, want %#v", got, want)
	}
}

func TestCommitStagesDeletedTodoToReadyTicketMove(t *testing.T) {
	preStatus := ParseStatus([]byte("1 .D N... 100644 100644 100644 aaa bbb ai-docs/tickets/todo/260503-feat-demo.md\n? ai-docs/tickets/ready/260503-feat-demo.md\n"))
	got := stagingCommandsForCommit([]string{"ai-docs/tickets/ready/260503-feat-demo.md", "ai-docs/tickets/todo/260503-feat-demo.md"}, preStatus, false)
	want := [][]string{
		{"add", "-A", "--", "ai-docs/tickets/ready/260503-feat-demo.md"},
		{"rm", "--cached", "--ignore-unmatch", "--", "ai-docs/tickets/todo/260503-feat-demo.md"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("staging commands = %#v, want %#v", got, want)
	}
}

func TestCommitStagesDeletedTicketMoveWhenOldStatusDirectoryIsGone(t *testing.T) {
	preStatus := ParseStatus([]byte("1 .D N... 100644 100644 100644 aaa bbb ai-docs/tickets/wip/260503-feat-demo.md\n? ai-docs/tickets/todo/260503-feat-demo.md\n"))
	got := stagingCommandsForCommit([]string{"ai-docs/tickets/todo/260503-feat-demo.md", "ai-docs/tickets/wip/260503-feat-demo.md"}, preStatus, false)
	want := [][]string{
		{"add", "-A", "--", "ai-docs/tickets/todo/260503-feat-demo.md"},
		{"rm", "--cached", "--ignore-unmatch", "--", "ai-docs/tickets/wip/260503-feat-demo.md"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("staging commands = %#v, want %#v", got, want)
	}
}

func TestCommitStagesRenamedDirectoryWithoutAddingMissingOldRoot(t *testing.T) {
	preStatus := ParseStatus([]byte("2 RM N... 100644 100644 100644 aaa bbb R100 agents-plugin/skills/lead-check-blockers/SKILL.md\tagents-plugin/skills/lead-can-we-proceed/SKILL.md\n"))
	got := stagingCommandsForCommit([]string{"agents-plugin/skills/lead-can-we-proceed", "agents-plugin/skills/lead-check-blockers"}, preStatus, false)
	want := [][]string{
		{"add", "-A", "--", "agents-plugin/skills/lead-check-blockers"},
		{"rm", "--cached", "--ignore-unmatch", "--", "agents-plugin/skills/lead-can-we-proceed/SKILL.md"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("staging commands = %#v, want %#v", got, want)
	}
}

func TestCommitStagesDeletedDirectoryRootByConcreteChildren(t *testing.T) {
	preStatus := ParseStatus([]byte("1 D. N... 100644 000000 000000 aaa 0000000000000000000000000000000000000000 old/file.txt\n"))
	got := stagingCommandsForCommit([]string{"old"}, preStatus, false)
	want := [][]string{
		{"rm", "--cached", "--ignore-unmatch", "--", "old/file.txt"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("staging commands = %#v, want %#v", got, want)
	}
}

func TestCommitStillAddsRootWithLiveChangesAndDeletedChildren(t *testing.T) {
	preStatus := ParseStatus([]byte("1 .D N... 100644 100644 100644 aaa bbb src/old.go\n1 .M N... 100644 100644 100644 aaa bbb src/live.go\n"))
	got := stagingCommandsForCommit([]string{"src"}, preStatus, false)
	want := [][]string{
		{"add", "-A", "--", "src"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("staging commands = %#v, want %#v", got, want)
	}
}

// TestStagingCommandsForCommitAddsSparseFlagWhenScopeActive pins the new
// #260810 behavior: a plain new path stages with `git add -A --sparse --`
// when the caller signals an active sparse-checkout scope, so an off-topic
// capture (e.g. a new idea/ ticket) under a hidden directory can be staged.
func TestStagingCommandsForCommitAddsSparseFlagWhenScopeActive(t *testing.T) {
	preStatus := ParseStatus([]byte("? ai-docs/tickets/idea/260810-idea-demo.md\n"))
	got := stagingCommandsForCommit([]string{"ai-docs/tickets/idea/260810-idea-demo.md"}, preStatus, true)
	want := [][]string{
		{"add", "-A", "--sparse", "--", "ai-docs/tickets/idea/260810-idea-demo.md"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("staging commands = %#v, want %#v", got, want)
	}
}

// TestStagingCommandsForCommitOmitsSparseFlagWhenScopeInactive is the
// companion regression pin: sparseActive:false reproduces today's exact
// `git add -A --` shape, byte-for-byte, for the same input that would gain
// `--sparse` when the scope is active.
func TestStagingCommandsForCommitOmitsSparseFlagWhenScopeInactive(t *testing.T) {
	preStatus := ParseStatus([]byte("? ai-docs/tickets/idea/260810-idea-demo.md\n"))
	got := stagingCommandsForCommit([]string{"ai-docs/tickets/idea/260810-idea-demo.md"}, preStatus, false)
	want := [][]string{
		{"add", "-A", "--", "ai-docs/tickets/idea/260810-idea-demo.md"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("staging commands = %#v, want %#v", got, want)
	}
}

// TestStagingCommandsForCommitLeavesRmCachedUnsparsedWhenScopeActive is the
// guardrail pin: even with sparseActive:true, the `rm --cached` branch must
// never gain `--sparse` — the ticket's binding guardrail scopes `--sparse` to
// the `add` command only (see stagingCommandsForCommit's doc comment).
func TestStagingCommandsForCommitLeavesRmCachedUnsparsedWhenScopeActive(t *testing.T) {
	preStatus := ParseStatus([]byte("1 .D N... 100644 100644 100644 aaa bbb ai-docs/tickets/todo/260503-feat-demo.md\n? ai-docs/tickets/ready/260503-feat-demo.md\n"))
	got := stagingCommandsForCommit([]string{"ai-docs/tickets/ready/260503-feat-demo.md", "ai-docs/tickets/todo/260503-feat-demo.md"}, preStatus, true)
	want := [][]string{
		{"add", "-A", "--sparse", "--", "ai-docs/tickets/ready/260503-feat-demo.md"},
		{"rm", "--cached", "--ignore-unmatch", "--", "ai-docs/tickets/todo/260503-feat-demo.md"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("staging commands = %#v, want %#v", got, want)
	}
}

// TestStagingCommandsForCommitRoutesUnmatchedSparseHiddenPathToAddNotRm pins
// the ticket's binding, non-negotiable deletion-safety guardrail: `--sparse`
// staging must never stage a deletion of an absent skip-worktree path. A
// tracked, skip-worktree, on-disk-absent, unmodified path is exactly the
// shape that produces NO matching entry in the pre-staging `git status`
// snapshot (skip-worktree suppresses the worktree-vs-index comparison
// entirely — verified empirically, git 2.43; see
// deletedPathsUnderCommitRoot's doc comment). This test asserts that when a
// caller-named path has no matching ChangedFiles entry at all,
// stagingCommandsForCommit still routes it through the `add` command — never
// `rm --cached` — regardless of sparseActive, because deletedPathsUnderCommitRoot
// can only classify a path as deleted when status actually reports a `D` or
// rename record for it.
func TestStagingCommandsForCommitRoutesUnmatchedSparseHiddenPathToAddNotRm(t *testing.T) {
	// No ChangedFiles at all: an unrelated path's status line is present, but
	// nothing matches ai-docs/tickets/idea/hidden-existing.md — the shape a
	// merely-hidden, unmodified tracked path produces.
	status := ParseStatus([]byte("1 .M N... 100644 100644 100644 aaa bbb src/unrelated.go\n"))
	path := "ai-docs/tickets/idea/hidden-existing.md"

	got := stagingCommandsForCommit([]string{path}, status, true)
	want := [][]string{
		{"add", "-A", "--sparse", "--", path},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("staging commands = %#v, want %#v — a path with no matching pre-staging status entry must route to `add`, never `rm --cached`", got, want)
	}

	// Same guardrail must hold when sparseActive is false too — the routing
	// logic (add vs. rm) is independent of the --sparse flag.
	got = stagingCommandsForCommit([]string{path}, status, false)
	want = [][]string{
		{"add", "-A", "--", path},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("staging commands (unscoped) = %#v, want %#v", got, want)
	}
}

// TestCommitPassesSparseScopeActiveThroughToStagingCommand exercises
// Client.Commit end-to-end (fake runner) to prove opts.SparseScopeActive
// reaches the actual `git add` invocation, not just the pure
// stagingCommandsForCommit helper.
func TestCommitPassesSparseScopeActiveThroughToStagingCommand(t *testing.T) {
	root := "/repo"
	ticketPath := "ai-docs/tickets/idea/260810-idea-demo.md"
	runner := &sequenceRunner{outs: [][]byte{
		[]byte("main\n"), // symbolic-ref --short HEAD (branch guard)
		{},               // pre-status (nothing staged yet)
		{},               // add -A --sparse --
		[]byte("1 A. N... 100644 100644 100644 aaa bbb " + ticketPath + "\n"), // post-status
		{},                 // detectTicketChanges name-status
		{},                 // detectTicketChanges unified diff
		{},                 // commit -m
		[]byte("abc123\n"), // rev-parse HEAD
	}}
	result, err := (Client{Runner: runner}).Commit(context.Background(), root, CommitOptions{
		Paths:             []string{ticketPath},
		Title:             "docs(ticket): capture idea",
		AIContext:         []string{"User intent: prove SparseScopeActive reaches git add."},
		ExpectedBranch:    "main",
		SparseScopeActive: true,
	})
	if err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}
	if result.Hash != "abc123" {
		t.Fatalf("result.Hash = %q, want abc123", result.Hash)
	}
	if len(runner.calls) < 3 {
		t.Fatalf("runner.calls = %#v, want at least branch guard, pre-status and add", runner.calls)
	}
	addCall := runner.calls[2]
	want := []string{"add", "-A", "--sparse", "--", ticketPath}
	if !reflect.DeepEqual(addCall.args, want) {
		t.Fatalf("add call args = %#v, want %#v", addCall.args, want)
	}
}

func TestCommitDivergentCaseExcludesRenameOldPathEvenWhenCallerPassedIt(t *testing.T) {
	root := "/repo"
	preStatus := strings.Join([]string{
		"1 .D N... 100644 100644 100644 aaa bbb ai-docs/tickets/todo/260503-feat-alpha.md",
		"? ai-docs/tickets/ready/260503-feat-alpha.md",
		"1 .M N... 100644 100644 100644 aaa bbb ai-docs/tickets/todo/260504-feat-beta.md",
		"",
	}, "\n")
	postStatus := strings.Join([]string{
		"2 R. N... 100644 100644 100644 aaa bbb R100 ai-docs/tickets/ready/260503-feat-alpha.md\tai-docs/tickets/todo/260503-feat-alpha.md",
		"1 M. N... 100644 100644 100644 aaa bbb ai-docs/tickets/todo/260504-feat-beta.md",
		"",
	}, "\n")
	runner := &sequenceRunner{outs: [][]byte{
		[]byte("main\n"), // symbolic-ref --short HEAD (branch guard)
		[]byte(preStatus),
		{},
		{},
		[]byte(postStatus),
		{},
		{},
		{},
		[]byte("abc123\n"),
	}}
	var gotPaths []string
	client := Client{Runner: runner, Verifier: func(_ string, paths []string) ([]string, error) {
		gotPaths = paths
		return nil, nil
	}}

	result, err := client.Commit(context.Background(), root, CommitOptions{
		Paths: []string{
			"ai-docs/tickets/todo/260503-feat-alpha.md",
			"ai-docs/tickets/ready/260503-feat-alpha.md",
			"ai-docs/tickets/todo/260504-feat-beta.md",
		},
		Title:          "docs(ticket): promote alpha and edit beta",
		AIContext:      []string{"User intent: promote one ticket while editing another in the same commit."},
		ExpectedBranch: "main",
	})
	if err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}
	if result.Hash != "abc123" {
		t.Fatalf("result.Hash = %q, want abc123", result.Hash)
	}
	want := []string{"ai-docs/tickets/ready/260503-feat-alpha.md", "ai-docs/tickets/todo/260504-feat-beta.md"}
	if !reflect.DeepEqual(gotPaths, want) {
		t.Fatalf("verifier paths = %#v, want %#v (rename old path must be excluded even though the caller passed it explicitly)", gotPaths, want)
	}
}

// TestCommitStillRefusesUnrelatedStagedRenameOldPath confirms
// validateCommitStatus's existing unrelated-path guard, which runs before
// the new delete-side filter, is unaffected: a staged rename for a stem the
// caller never named must still block the commit before the Verifier is
// ever invoked.
func TestCommitStillRefusesUnrelatedStagedRenameOldPath(t *testing.T) {
	postStatus := "2 R. N... 100644 100644 100644 aaa bbb R100 ai-docs/tickets/ready/260503-feat-alpha.md\tai-docs/tickets/todo/260503-feat-alpha.md\n"
	runner := &sequenceRunner{outs: [][]byte{
		[]byte("main\n"), // symbolic-ref --short HEAD (branch guard)
		{},               // pre-status
		{},               // add
		[]byte(postStatus),
	}}
	verifierCalled := false
	client := Client{Runner: runner, Verifier: func(string, []string) ([]string, error) {
		verifierCalled = true
		return nil, nil
	}}

	_, err := client.Commit(context.Background(), "/repo", CommitOptions{
		Paths:          []string{"ai-docs/tickets/todo/260505-feat-gamma.md"},
		Title:          "docs(ticket): unrelated commit",
		AIContext:      []string{"User intent: commit an unrelated ticket path."},
		ExpectedBranch: "main",
	})
	if err == nil || !strings.Contains(err.Error(), "unrelated staged path") {
		t.Fatalf("Commit error = %v, want unrelated staged path", err)
	}
	if verifierCalled {
		t.Fatalf("Verifier was invoked despite an unrelated staged path blocking the commit")
	}
	if len(runner.calls) != 5 || !reflect.DeepEqual(runner.calls[4].args, []string{"rev-parse", "--git-path", "MERGE_HEAD"}) {
		t.Fatalf("runner.calls = %#v, want branch guard, pre-status, add, post-status, and a read-only merge-state probe", runner.calls)
	}
}

// TestCommitIndexOnlyPathStillRefusesUnrelatedStagedRename closes the
// dogfood-matrix coverage gap: committing only "_index.md" while an
// unrelated ticket rename is separately staged must still be refused, since
// "_index.md" is not ticket-shaped and triggers no stem expansion.
func TestCommitIndexOnlyPathStillRefusesUnrelatedStagedRename(t *testing.T) {
	postStatus := strings.Join([]string{
		"2 R. N... 100644 100644 100644 aaa bbb R100 ai-docs/tickets/ready/260503-feat-alpha.md\tai-docs/tickets/todo/260503-feat-alpha.md",
		"1 M. N... 100644 100644 100644 aaa bbb _index.md",
		"",
	}, "\n")
	runner := &sequenceRunner{outs: [][]byte{
		[]byte("main\n"), // symbolic-ref --short HEAD (branch guard)
		{},
		{},
		[]byte(postStatus),
	}}
	verifierCalled := false
	client := Client{Runner: runner, Verifier: func(string, []string) ([]string, error) {
		verifierCalled = true
		return nil, nil
	}}

	_, err := client.Commit(context.Background(), "/repo", CommitOptions{
		Paths:          []string{"_index.md"},
		Title:          "docs: update index",
		AIContext:      []string{"User intent: commit only the index while a ticket rename is separately staged."},
		ExpectedBranch: "main",
	})
	if err == nil || !strings.Contains(err.Error(), "unrelated staged path") {
		t.Fatalf("Commit error = %v, want unrelated staged path", err)
	}
	if verifierCalled {
		t.Fatalf("Verifier was invoked despite an unrelated staged ticket rename blocking the commit")
	}
}

// TestCommitContentOnlyEditLeavesVerifierPathsUnmodified confirms the filter
// is a no-op when nothing in the commit is a rename or deletion.
func TestCommitStagesCapturedIdeaTicketUnderSparseScopeThenSelfHides(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	sparseTestInitGit(t, root)

	// Seed HEAD with a tracked keep-file so the idea/ directory survives the
	// upcoming exclude pattern.
	mustWriteGitTestFixture(t, root, "ai-docs/tickets/idea/.gitkeep", "")
	sparseTestRunGit(t, root, "add", "ai-docs/tickets/idea/.gitkeep")
	sparseTestRunGit(t, root, "commit", "-m", "chore: seed idea/ keep-file")

	sparseTestRunGit(t, root, "sparse-checkout", "set", "--no-cone",
		"/*",
		"!/ai-docs/tickets/idea/*",
		"/ai-docs/tickets/idea/.gitkeep",
	)

	ticketPath := "ai-docs/tickets/idea/260810-idea-demo.md"
	mustWriteGitTestFixture(t, root, ticketPath, "# Demo Idea\n")

	client := Client{Runner: ExecRunner{}}
	branchName := strings.TrimSpace(string(sparseTestRunGitOutput(t, root, "rev-parse", "--abbrev-ref", "HEAD")))
	result, err := client.Commit(context.Background(), root, CommitOptions{
		Paths:             []string{ticketPath},
		Title:             "docs(ticket): capture demo idea",
		AIContext:         []string{"User intent: prove a hidden-idea/ capture stages and self-hides."},
		ExpectedBranch:    branchName,
		SparseScopeActive: true,
	})
	if err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}
	if result.Hash == "" {
		t.Fatalf("result.Hash is empty")
	}

	// Immediately after commit, the file is still on disk (self-hiding is
	// deferred to the next sparse re-apply, per the ticket's verified
	// experiment).
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(ticketPath))); err != nil {
		t.Fatalf("captured idea file missing from disk immediately after commit: %v", err)
	}

	sparseTestRunGit(t, root, "sparse-checkout", "reapply")

	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(ticketPath))); !os.IsNotExist(err) {
		t.Fatalf("captured idea file did not self-hide after `sparse-checkout reapply`: stat err = %v", err)
	}

	// The index (and HEAD) must still carry the file despite it being hidden
	// from the worktree.
	out := sparseTestRunGitOutput(t, root, "show", "HEAD:"+ticketPath)
	if !strings.Contains(string(out), "Demo Idea") {
		t.Fatalf("git show HEAD:%s did not return the captured content: %q", ticketPath, out)
	}
}

func sparseTestInitGit(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, string(out))
	}
	sparseTestRunGit(t, root, "config", "core.autocrlf", "false")
	sparseTestRunGit(t, root, "config", "user.email", "test@example.com")
	sparseTestRunGit(t, root, "config", "user.name", "Test User")
}

func sparseTestRunGit(t *testing.T, root string, args ...string) {
	t.Helper()
	sparseTestRunGitOutput(t, root, args...)
}

func sparseTestRunGitOutput(t *testing.T, root string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return out
}

func mustWriteGitTestFixture(t *testing.T, root, relPath, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDropVanishedTicketMoveSourcesDropsSourceOfFirstCommitMove(t *testing.T) {
	status := ParseStatus([]byte("1 A. N... 000000 100644 100644 0000000000000000000000000000000000000000 bbb ai-docs/tickets/todo/260810-feat-demo.md\n"))
	paths := []string{"ai-docs/tickets/idea/260810-feat-demo.md", "ai-docs/tickets/todo/260810-feat-demo.md", "docs/readme.md"}
	got := dropVanishedTicketMoveSources(status, paths, nil)
	want := []string{"ai-docs/tickets/todo/260810-feat-demo.md", "docs/readme.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %#v, want %#v", got, want)
	}
	// A source the index still holds (tracked and unchanged) is no vanished
	// source: it stays in the grant.
	if got := dropVanishedTicketMoveSources(status, paths, map[string]bool{"ai-docs/tickets/idea/260810-feat-demo.md": true}); !reflect.DeepEqual(got, paths) {
		t.Fatalf("indexed source dropped: %#v", got)
	}
}

// TestDropVanishedTicketMoveSourcesNeedsStagedAddition: only a staged
// addition of the same stem stands for the move. An untracked or a modified
// sibling keeps the named path, so its pathspec error stands.
func TestDropVanishedTicketMoveSourcesNeedsStagedAddition(t *testing.T) {
	paths := []string{"ai-docs/tickets/idea/260810-feat-demo.md"}
	for name, line := range map[string]string{
		"untracked": "? ai-docs/tickets/todo/260810-feat-demo.md\n",
		"modified":  "1 .M N... 100644 100644 100644 aaa aaa ai-docs/tickets/todo/260810-feat-demo.md\n",
	} {
		status := ParseStatus([]byte(line))
		if got := dropVanishedTicketMoveSources(status, paths, nil); !reflect.DeepEqual(got, paths) {
			t.Fatalf("%s sibling: paths = %#v, want unchanged", name, got)
		}
	}
}

func TestDropVanishedTicketMoveSourcesKeepsTrackedMoveAndMistypedPaths(t *testing.T) {
	status := ParseStatus([]byte("2 R. N... 100644 100644 100644 aaa bbb R100 ai-docs/tickets/ready/260810-feat-demo.md\tai-docs/tickets/todo/260810-feat-demo.md\n"))
	paths := []string{"ai-docs/tickets/todo/260810-feat-demo.md", "ai-docs/tickets/ready/260810-feat-demo.md", "ai-docs/tickets/todo/260810-feat-typo.md"}
	got := dropVanishedTicketMoveSources(status, paths, nil)
	if !reflect.DeepEqual(got, paths) {
		t.Fatalf("paths = %#v, want unchanged %#v (rename old path stays in the grant; a sibling-less path keeps its error)", got, paths)
	}
}

// TestCommitFirstCommitTicketMoveNamesOldOrNewPath replays what
// tickets.create_empty followed by tickets.move leaves in a fresh repository:
// the ticket was never committed, so the index holds only the destination.
// Naming the vanished source used to abort the commit with "pathspec did not
// match any files".
func TestCommitFirstCommitTicketMoveNamesOldOrNewPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	const oldPath = "ai-docs/tickets/idea/260810-feat-demo.md"
	const newPath = "ai-docs/tickets/todo/260810-feat-demo.md"
	for name, paths := range map[string][]string{
		"old":  {oldPath},
		"new":  {newPath},
		"both": {oldPath, newPath},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			sparseTestInitGit(t, root)
			mustWriteGitTestFixture(t, root, "README.md", "x\n")
			sparseTestRunGit(t, root, "add", "README.md")
			sparseTestRunGit(t, root, "commit", "-m", "seed")
			mustWriteGitTestFixture(t, root, oldPath, "# Demo\n")
			mustWriteGitTestFixture(t, root, "ai-docs/tickets/todo/.keep", "")
			sparseTestRunGit(t, root, "add", oldPath)
			sparseTestRunGit(t, root, "mv", "--force", oldPath, newPath)

			branch := strings.TrimSpace(string(sparseTestRunGitOutput(t, root, "rev-parse", "--abbrev-ref", "HEAD")))
			var verified []string
			client := Client{Runner: ExecRunner{}, Verifier: func(_ string, paths []string) ([]string, error) {
				verified = paths
				return nil, nil
			}}
			result, err := client.Commit(context.Background(), root, CommitOptions{
				Paths:          paths,
				Title:          "docs(ticket): add demo",
				AIContext:      []string{"User intent: commit a ticket moved before its first commit."},
				ExpectedBranch: branch,
			})
			if err != nil {
				t.Fatalf("Commit returned error: %v", err)
			}
			if !reflect.DeepEqual(result.Paths, []string{newPath}) {
				t.Fatalf("result.Paths = %#v, want only %q", result.Paths, newPath)
			}
			if !reflect.DeepEqual(verified, []string{newPath}) {
				t.Fatalf("verifier paths = %#v, want only %q", verified, newPath)
			}
			committed := strings.TrimSpace(string(sparseTestRunGitOutput(t, root, "show", "--name-only", "--format=", "HEAD")))
			if committed != newPath {
				t.Fatalf("commit contains %q, want only %q", committed, newPath)
			}
			if out := strings.TrimSpace(string(sparseTestRunGitOutput(t, root, "diff", "--cached", "--name-only"))); out != "" {
				t.Fatalf("index still has staged paths after commit: %q", out)
			}
		})
	}
}

// TestCommitNeverStagesUntrackedTicketSibling: naming a ticket path git never
// knew, while an untracked file of the same stem sits in another status
// directory, fails on the named path and leaves the sibling untracked; only a
// staged addition stands in for a first-commit move.
func TestCommitNeverStagesUntrackedTicketSibling(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	const named = "ai-docs/tickets/idea/260810-feat-demo.md"
	const sibling = "ai-docs/tickets/todo/260810-feat-demo.md"
	root := t.TempDir()
	sparseTestInitGit(t, root)
	mustWriteGitTestFixture(t, root, "README.md", "x\n")
	// A tracked file in the sibling's directory makes status list the
	// untracked sibling itself rather than its directory.
	mustWriteGitTestFixture(t, root, "ai-docs/tickets/todo/.keep", "")
	sparseTestRunGit(t, root, "add", "README.md", "ai-docs/tickets/todo/.keep")
	sparseTestRunGit(t, root, "commit", "-m", "seed")
	mustWriteGitTestFixture(t, root, sibling, "# Demo\n")

	branch := strings.TrimSpace(string(sparseTestRunGitOutput(t, root, "rev-parse", "--abbrev-ref", "HEAD")))
	client := Client{Runner: ExecRunner{}, Verifier: func(string, []string) ([]string, error) { return nil, nil }}
	_, err := client.Commit(context.Background(), root, CommitOptions{
		Paths:          []string{named},
		Title:          "docs(ticket): add demo",
		AIContext:      []string{"User intent: commit a ticket path git never knew."},
		ExpectedBranch: branch,
	})
	if err == nil || !strings.Contains(err.Error(), named) {
		t.Fatalf("Commit error = %v, want the named path's pathspec failure", err)
	}
	if out := strings.TrimSpace(string(sparseTestRunGitOutput(t, root, "diff", "--cached", "--name-only"))); out != "" {
		t.Fatalf("an untracked sibling was staged: %q", out)
	}
	if out := strings.TrimSpace(string(sparseTestRunGitOutput(t, root, "status", "--porcelain"))); out != "?? "+sibling {
		t.Fatalf("status = %q, want the sibling still untracked", out)
	}
	if n := strings.TrimSpace(string(sparseTestRunGitOutput(t, root, "rev-list", "--count", "HEAD"))); n != "1" {
		t.Fatalf("commits = %s, want only the seed", n)
	}
}
