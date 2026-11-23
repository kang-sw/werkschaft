package wsreview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsgit"
)

func checkpointTestWriteFile(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", full, err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// checkpointTestCommit adds one trivial commit to root's current branch and
// returns its full SHA.
func checkpointTestCommit(t *testing.T, root, label string) string {
	t.Helper()
	return checkpointTestCommitFiles(t, root, label, map[string][]byte{
		label + ".txt": []byte(label + "\n"),
	})
}

func checkpointTestCommitFiles(t *testing.T, root, message string, files map[string][]byte) string {
	t.Helper()
	for rel, data := range files {
		checkpointTestWriteFile(t, root, rel, data)
		reviewTestRunGit(t, root, "add", rel)
	}
	reviewTestRunGit(t, root, "commit", "-m", message)
	return strings.TrimSpace(string(reviewTestRunGitOutput(t, root, "rev-parse", "HEAD")))
}

// checkpointTestMergeBranch creates a branch off track, applies files in one
// commit, switches back to track, and lands a --no-ff merge commit.
func checkpointTestMergeBranch(t *testing.T, root, track, branch string, files map[string][]byte) string {
	t.Helper()
	reviewTestRunGit(t, root, "checkout", "-b", branch, track)
	checkpointTestCommitFiles(t, root, branch, files)
	reviewTestRunGit(t, root, "checkout", track)
	reviewTestRunGit(t, root, "merge", "--no-ff", branch, "-m", "merge "+branch)
	return strings.TrimSpace(string(reviewTestRunGitOutput(t, root, "rev-parse", "HEAD")))
}

func checkpointTestLines(n int) []byte {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "line-%05d\n", i)
	}
	return []byte(b.String())
}

func checkpointTestInitSubmoduleCommits(t *testing.T) (string, string) {
	t.Helper()
	sub := t.TempDir()
	reviewTestInitRepoOnBranch(t, sub, "main")
	sha1 := strings.TrimSpace(string(reviewTestRunGitOutput(t, sub, "rev-parse", "HEAD")))
	sha2 := checkpointTestCommit(t, sub, "sub-v2")
	return sha1, sha2
}

type countingGitRunner struct {
	inner wsgit.Runner
	calls [][]string
}

func (r *countingGitRunner) RunGit(ctx context.Context, root string, args ...string) ([]byte, error) {
	cp := make([]string, len(args))
	copy(cp, args)
	r.calls = append(r.calls, cp)
	return r.inner.RunGit(ctx, root, args...)
}

// TestCheckpointNudgeNoLedgerReturnsBaselineAdvisoryWithoutWriting is the
// ledger-honesty guard's no-ledger branch: CheckpointNudge must surface a
// baseline-missing advisory but must never create the ledger file itself.
func TestCheckpointNudgeNoLedgerReturnsBaselineAdvisoryWithoutWriting(t *testing.T) {
	root := t.TempDir()
	reviewTestInitRepoOnBranch(t, root, "main")

	got := CheckpointNudge(context.Background(), root)
	if !strings.Contains(got, "no review ledger yet") {
		t.Fatalf("CheckpointNudge = %q, want a baseline-missing advisory", got)
	}
	if _, err := os.Stat(LedgerPath(root)); !os.IsNotExist(err) {
		t.Fatalf("CheckpointNudge must never create the ledger file on the no-ledger path; stat err = %v", err)
	}
}

// TestCheckpointNudgeQuietOnSmallFreshRangeNeverAppends is the ledger-honesty
// guard's quiet branch: a range below both thresholds must return "" and
// must never mutate the (already-seeded) ledger file.
func TestCheckpointNudgeQuietOnSmallFreshRangeNeverAppends(t *testing.T) {
	root := t.TempDir()
	reviewTestInitRepoOnBranch(t, root, "main")
	head := strings.TrimSpace(string(reviewTestRunGitOutput(t, root, "rev-parse", "HEAD")))
	if err := Append(root, Entry{Base: head, Head: head, Verdict: VerdictPass}); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	before, err := os.ReadFile(LedgerPath(root))
	if err != nil {
		t.Fatalf("read seeded ledger: %v", err)
	}

	for i := 0; i < 3; i++ {
		checkpointTestMergeBranch(t, root, "main", fmt.Sprintf("b%d", i), map[string][]byte{
			fmt.Sprintf("f%d.go", i): []byte("package p\n"),
		})
	}

	got := CheckpointNudge(context.Background(), root)
	if got != "" {
		t.Fatalf("CheckpointNudge on a small fresh range = %q, want \"\"", got)
	}
	after, err := os.ReadFile(LedgerPath(root))
	if err != nil {
		t.Fatalf("read ledger after CheckpointNudge: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("quiet-range CheckpointNudge mutated the ledger file: before=%q after=%q", before, after)
	}
}

func TestCheckpointNudgeShippedDefaultsTriggerAtEquality(t *testing.T) {
	t.Run("merges threshold equality", func(t *testing.T) {
		root := t.TempDir()
		reviewTestInitRepoOnBranch(t, root, "main")
		head := strings.TrimSpace(string(reviewTestRunGitOutput(t, root, "rev-parse", "HEAD")))
		if err := Append(root, Entry{Base: head, Head: head, Verdict: VerdictPass}); err != nil {
			t.Fatalf("seed ledger: %v", err)
		}

		for i := 0; i < defaultMergesThreshold-1; i++ {
			checkpointTestMergeBranch(t, root, "main", fmt.Sprintf("m%d", i), map[string][]byte{
				fmt.Sprintf("m%d.go", i): []byte("line\n"),
			})
		}
		if got := CheckpointNudge(context.Background(), root); got != "" {
			t.Fatalf("at %d merges (< %d), got %q, want quiet", defaultMergesThreshold-1, defaultMergesThreshold, got)
		}

		checkpointTestMergeBranch(t, root, "main", "m-last", map[string][]byte{"mlast.go": []byte("line\n")})
		got := CheckpointNudge(context.Background(), root)
		wantFragment := fmt.Sprintf("review watermark is %d merges, %d net lines behind main (thresholds: %d merges / %d lines)", defaultMergesThreshold, defaultMergesThreshold, defaultMergesThreshold, defaultLinesThreshold)
		if !strings.Contains(got, wantFragment) || !strings.Contains(got, head+"..main") {
			t.Fatalf("at %d merges, got %q, want fragment %q and range %s..main", defaultMergesThreshold, got, wantFragment, head)
		}
	})

	t.Run("net lines threshold equality via direct commits", func(t *testing.T) {
		root := t.TempDir()
		reviewTestInitRepoOnBranch(t, root, "main")
		head := strings.TrimSpace(string(reviewTestRunGitOutput(t, root, "rev-parse", "HEAD")))
		if err := Append(root, Entry{Base: head, Head: head, Verdict: VerdictPass}); err != nil {
			t.Fatalf("seed ledger: %v", err)
		}

		// 4999 eligible lines + 200 direct ai-docs lines must stay quiet.
		checkpointTestCommitFiles(t, root, "direct-below", map[string][]byte{
			"big.txt":             checkpointTestLines(defaultLinesThreshold - 1),
			"ai-docs/direct-doc.md": checkpointTestLines(200),
		})
		if got := CheckpointNudge(context.Background(), root); got != "" {
			t.Fatalf("at %d eligible lines (+200 ai-docs), got %q, want quiet", defaultLinesThreshold-1, got)
		}

		// 1 more eligible line crosses the 5000-line threshold even with 0 merges.
		checkpointTestCommitFiles(t, root, "direct-cross", map[string][]byte{
			"one.txt": []byte("one\n"),
		})
		got := CheckpointNudge(context.Background(), root)
		if !strings.Contains(got, "0 merges, 5000 net lines behind main") {
			t.Fatalf("at 5000 net lines, got %q, want 0 merges and 5000 net lines", got)
		}
	})
}

func TestCheckpointNudgeIndependentAndCombinedLocalOverrides(t *testing.T) {
	root := t.TempDir()
	reviewTestInitRepoOnBranch(t, root, "main")
	head := strings.TrimSpace(string(reviewTestRunGitOutput(t, root, "rev-parse", "HEAD")))
	if err := Append(root, Entry{Base: head, Head: head, Verdict: VerdictPass}); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	mustWriteReviewLocalConfig(t, root, "## Checkpoint Nudge\nmerges: 2\nlines: 10\n")

	// 1 merge, 5 lines: quiet below both.
	checkpointTestMergeBranch(t, root, "main", "m1", map[string][]byte{"a.go": checkpointTestLines(5)})
	if got := CheckpointNudge(context.Background(), root); got != "" {
		t.Fatalf("below both overrides: got %q, want quiet", got)
	}

	// Direct commit brings lines to 10 (equality on lines, merges still 1 < 2).
	checkpointTestCommitFiles(t, root, "d1", map[string][]byte{"b.go": checkpointTestLines(5)})
	gotLinesOnly := CheckpointNudge(context.Background(), root)
	if !strings.Contains(gotLinesOnly, "1 merge, 10 net lines behind main (thresholds: 2 merges / 10 lines)") {
		t.Fatalf("lines-only trigger = %q", gotLinesOnly)
	}

	// Second merge makes BOTH thresholds met; still one single advisory message.
	checkpointTestMergeBranch(t, root, "main", "m2", map[string][]byte{"c.go": checkpointTestLines(3)})
	gotBoth := CheckpointNudge(context.Background(), root)
	if !strings.Contains(gotBoth, "2 merges, 13 net lines behind main (thresholds: 2 merges / 10 lines)") {
		t.Fatalf("both-met trigger = %q", gotBoth)
	}
	if strings.Count(gotBoth, "review watermark is") != 1 {
		t.Fatalf("expected one advisory message when both thresholds met, got %q", gotBoth)
	}
}

func TestCheckpointNudgeFirstParentHistoryExclusionsAndReverts(t *testing.T) {
	root := t.TempDir()
	reviewTestInitRepoOnBranch(t, root, "trunk")
	checkpointTestWriteFile(t, root, "AGENTS.md", []byte("## Workflow\n\n### Review Policy\nreview-track: trunk\n"))
	reviewTestRunGit(t, root, "add", "AGENTS.md")
	reviewTestRunGit(t, root, "commit", "-m", "declare non-develop review track")
	// Also create a main branch to prove AGENTS.md's trunk wins.
	reviewTestRunGit(t, root, "branch", "main")

	head := strings.TrimSpace(string(reviewTestRunGitOutput(t, root, "rev-parse", "HEAD")))
	if err := Append(root, Entry{Base: head, Head: head, Verdict: VerdictPass}); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	mustWriteReviewLocalConfig(t, root, "## Checkpoint Nudge\nmerges: 1\nlines: 99999\nexclude: dist/**\nexclude: *.gen.go\n")

	// 1. Docs-only merge -> excluded (0 merges, 0 lines).
	checkpointTestMergeBranch(t, root, "trunk", "docs-only", map[string][]byte{
		"ai-docs/notes.md": checkpointTestLines(50),
	})
	// 2. Generated-only merges matching repeated exclude pathspecs -> excluded.
	checkpointTestMergeBranch(t, root, "trunk", "gen-dir", map[string][]byte{
		"dist/bundle.js": checkpointTestLines(100),
	})
	checkpointTestMergeBranch(t, root, "trunk", "gen-glob", map[string][]byte{
		"pkg/types.gen.go": checkpointTestLines(80),
	})
	// 3. Empty direct commit -> 0 merges, 0 lines.
	reviewTestRunGit(t, root, "commit", "--allow-empty", "-m", "empty direct")
	// 4. Empty merge -> 0 merges, 0 lines.
	reviewTestRunGit(t, root, "checkout", "-b", "empty-side", "trunk")
	reviewTestRunGit(t, root, "commit", "--allow-empty", "-m", "empty side")
	reviewTestRunGit(t, root, "checkout", "trunk")
	reviewTestRunGit(t, root, "merge", "--no-ff", "empty-side", "-m", "merge empty-side")
	// 5. Self-reverted branch merge (M tree == M^1 tree) -> 0 merges, 0 lines.
	reviewTestRunGit(t, root, "checkout", "-b", "self-revert", "trunk")
	checkpointTestCommitFiles(t, root, "add tmp", map[string][]byte{"tmp.go": checkpointTestLines(10)})
	reviewTestRunGit(t, root, "rm", "tmp.go")
	reviewTestRunGit(t, root, "commit", "-m", "rm tmp")
	reviewTestRunGit(t, root, "checkout", "trunk")
	reviewTestRunGit(t, root, "merge", "--no-ff", "self-revert", "-m", "merge self-revert")
	// 6. Direct ai-docs commit -> 0 merges, 0 lines.
	checkpointTestCommitFiles(t, root, "direct-docs", map[string][]byte{
		"ai-docs/direct.md": checkpointTestLines(40),
	})

	if got := CheckpointNudge(context.Background(), root); got != "" {
		t.Fatalf("only excluded/empty changes should stay quiet, got %q", got)
	}

	// 7. Side-branch with its OWN internal merge + mixed ai-docs/generated/real
	// changes + a file literally named COMMIT -> must count as exactly 1
	// first-parent merge and only the reviewable text lines (7 lines).
	reviewTestRunGit(t, root, "checkout", "-b", "feat-outer", "trunk")
	checkpointTestCommitFiles(t, root, "outer-docs", map[string][]byte{
		"ai-docs/mixed.md": checkpointTestLines(30),
		"dist/extra.js":    checkpointTestLines(40),
	})
	reviewTestRunGit(t, root, "checkout", "-b", "feat-inner", "feat-outer")
	checkpointTestCommitFiles(t, root, "inner-code", map[string][]byte{
		"src/app.go": checkpointTestLines(5),
		"COMMIT":     checkpointTestLines(2),
	})
	reviewTestRunGit(t, root, "checkout", "feat-outer")
	reviewTestRunGit(t, root, "merge", "--no-ff", "feat-inner", "-m", "side-branch internal merge")
	reviewTestRunGit(t, root, "checkout", "trunk")
	reviewTestRunGit(t, root, "merge", "--no-ff", "feat-outer", "-m", "land feat-outer")

	// Direct ai-docs edit after qualifying merge still adds 0 lines.
	checkpointTestCommitFiles(t, root, "direct-docs-after", map[string][]byte{
		"ai-docs/more.md": checkpointTestLines(25),
	})

	got := CheckpointNudge(context.Background(), root)
	if !strings.Contains(got, "review watermark is 1 merge, 7 net lines behind trunk") {
		t.Fatalf("mixed merge + side internal merge = %q, want 1 merge and 7 net lines behind trunk", got)
	}

	// 8. Reverted change within the range: add 50 lines in a direct commit and
	// remove them in the next direct commit -> net lines stays 7!
	checkpointTestCommitFiles(t, root, "temp-add", map[string][]byte{"reverted.go": checkpointTestLines(50)})
	reviewTestRunGit(t, root, "rm", "reverted.go")
	reviewTestRunGit(t, root, "commit", "-m", "temp-revert")

	gotAfterRevert := CheckpointNudge(context.Background(), root)
	if !strings.Contains(gotAfterRevert, "review watermark is 1 merge, 7 net lines behind trunk") {
		t.Fatalf("after reverted change = %q, want net lines unchanged at 7", gotAfterRevert)
	}
}

func TestCheckpointNudgeBinaryModeAndGitlinkDeltas(t *testing.T) {
	sub1, sub2 := checkpointTestInitSubmoduleCommits(t)

	root := t.TempDir()
	reviewTestInitRepoOnBranch(t, root, "main")
	checkpointTestCommitFiles(t, root, "seed-script", map[string][]byte{
		"script.sh":   []byte("#!/bin/sh\necho hi\n"),
		"replace.txt": checkpointTestLines(3),
	})
	if err := os.MkdirAll(filepath.Join(root, "mysub"), 0o755); err != nil {
		t.Fatalf("mkdir mysub: %v", err)
	}
	reviewTestRunGit(t, root, "update-index", "--add", "--cacheinfo", "160000,"+sub1+",mysub")
	reviewTestRunGit(t, root, "commit", "-m", "add gitlink")

	head := strings.TrimSpace(string(reviewTestRunGitOutput(t, root, "rev-parse", "HEAD")))
	if err := Append(root, Entry{Base: head, Head: head, Verdict: VerdictPass}); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	mustWriteReviewLocalConfig(t, root, "## Checkpoint Nudge\nmerges: 3\nlines: 1\n")

	// Merge 1: binary-only delta.
	checkpointTestMergeBranch(t, root, "main", "bin-only", map[string][]byte{
		"asset.bin": {0x00, 0x01, 0x02, 0x00, 0xff, 0xfe},
	})
	// Merge 2: mode-only delta.
	reviewTestRunGit(t, root, "checkout", "-b", "mode-only", "main")
	if err := os.Chmod(filepath.Join(root, "script.sh"), 0o755); err != nil {
		t.Fatalf("chmod script.sh: %v", err)
	}
	reviewTestRunGit(t, root, "update-index", "--chmod=+x", "script.sh")
	reviewTestRunGit(t, root, "commit", "-m", "chmod +x")
	reviewTestRunGit(t, root, "checkout", "main")
	reviewTestRunGit(t, root, "merge", "--no-ff", "mode-only", "-m", "merge mode-only")

	// At 2 merges and 0 lines (even though lines threshold is 1!), must stay quiet.
	if got := CheckpointNudge(context.Background(), root); got != "" {
		t.Fatalf("binary + mode-only (2 merges, 0 lines) = %q, want quiet", got)
	}

	// Merge 3: gitlink-only delta (must count as merge 3 and add 0 lines —
	// never count synthetic "Subproject commit" lines!).
	reviewTestRunGit(t, root, "checkout", "-b", "gitlink-only", "main")
	reviewTestRunGit(t, root, "update-index", "--add", "--cacheinfo", "160000,"+sub2+",mysub")
	reviewTestRunGit(t, root, "commit", "-m", "bump gitlink")
	reviewTestRunGit(t, root, "checkout", "main")
	reviewTestRunGit(t, root, "merge", "--no-ff", "gitlink-only", "-m", "merge gitlink-only")

	got := CheckpointNudge(context.Background(), root)
	if !strings.Contains(got, "review watermark is 3 merges, 0 net lines behind main") {
		t.Fatalf("binary + mode + gitlink merges = %q, want 3 merges and 0 net lines", got)
	}

	// Bonus: typechange regular 3-line file -> gitlink must count the 3 real
	// deleted text lines and NOT the 1 synthetic added gitlink line.
	reviewTestRunGit(t, root, "rm", "replace.txt")
	if err := os.MkdirAll(filepath.Join(root, "replace.txt"), 0o755); err != nil {
		t.Fatalf("mkdir replace.txt: %v", err)
	}
	reviewTestRunGit(t, root, "update-index", "--add", "--cacheinfo", "160000,"+sub1+",replace.txt")
	reviewTestRunGit(t, root, "commit", "-m", "replace file with gitlink")

	gotTypechange := CheckpointNudge(context.Background(), root)
	if !strings.Contains(gotTypechange, "review watermark is 3 merges, 3 net lines behind main") {
		t.Fatalf("after file->gitlink typechange = %q, want 3 merges and 3 net lines", gotTypechange)
	}
}

func TestCheckpointNudgeComparisonBaseOffFirstParentChain(t *testing.T) {
	cases := []struct {
		name              string
		interveningOnMain bool
	}{
		{name: "merge first parent is ancestor of side-branch base", interveningOnMain: false},
		{name: "merge first parent is outside side-branch base ancestry", interveningOnMain: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			reviewTestInitRepoOnBranch(t, root, "main")

			// Side branch S1 off initial commit; frontier sits at child S1b so
			// merge-base(frontier, main) == S1 (not on main's first-parent chain).
			reviewTestRunGit(t, root, "checkout", "-b", "side-s1", "main")
			checkpointTestCommitFiles(t, root, "S1", map[string][]byte{"side.go": []byte("s1\n")})
			reviewTestRunGit(t, root, "checkout", "-b", "frontier-branch", "side-s1")
			frontierHead := checkpointTestCommitFiles(t, root, "S1b", map[string][]byte{"frontier.go": []byte("f\n")})

			reviewTestRunGit(t, root, "checkout", "main")
			if tc.interveningOnMain {
				checkpointTestCommitFiles(t, root, "main-d", map[string][]byte{"d.go": []byte("d\n")})
			}
			// M1 merges S1 into main (outside S1's reachable ancestry, delta vs M1^1 changes side.go).
			reviewTestRunGit(t, root, "merge", "--no-ff", "side-s1", "-m", "M1-merges-S1")
			// M2 is a later eligible merge on main.
			checkpointTestMergeBranch(t, root, "main", "after-s1", map[string][]byte{"after.go": []byte("a1\na2\n")})

			if err := Append(root, Entry{Base: frontierHead, Head: frontierHead, Verdict: VerdictPass}); err != nil {
				t.Fatalf("seed ledger: %v", err)
			}
			mustWriteReviewLocalConfig(t, root, "## Checkpoint Nudge\nmerges: 2\nlines: 99999\n")

			got := CheckpointNudge(context.Background(), root)
			if !strings.Contains(got, "review watermark is 2 merges,") || !strings.Contains(got, frontierHead+"..main") {
				t.Fatalf("side-branch merge-base (%s) = %q, want 2 merges and range %s..main", tc.name, got, frontierHead)
			}
		})
	}
}

func TestCheckpointNudgeUnresolvedMergeBaseReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	reviewTestInitRepoOnBranch(t, root, "main")

	// Create an orphan branch with unrelated history for the ledger frontier.
	reviewTestRunGit(t, root, "checkout", "--orphan", "orphan")
	reviewTestRunGit(t, root, "rm", "-rf", ".")
	orphanHead := checkpointTestCommit(t, root, "orphan-commit")
	reviewTestRunGit(t, root, "checkout", "main")
	checkpointTestMergeBranch(t, root, "main", "m1", map[string][]byte{"a.go": checkpointTestLines(100)})

	if err := Append(root, Entry{Base: orphanHead, Head: orphanHead, Verdict: VerdictPass}); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	mustWriteReviewLocalConfig(t, root, "## Checkpoint Nudge\nmerges: 1\nlines: 1\n")

	if got := CheckpointNudge(context.Background(), root); got != "" {
		t.Fatalf("unresolved merge-base = %q, want empty", got)
	}
}

func TestCheckpointNudgeLegacyTriggerAndPrecedence(t *testing.T) {
	t.Run("legacy-only staleness below 20", func(t *testing.T) {
		root := t.TempDir()
		reviewTestInitRepoOnBranch(t, root, "main")
		head := strings.TrimSpace(string(reviewTestRunGitOutput(t, root, "rev-parse", "HEAD")))
		if err := Append(root, Entry{Base: head, Head: head, Verdict: VerdictPass}); err != nil {
			t.Fatalf("seed ledger: %v", err)
		}
		mustWriteReviewLocalConfig(t, root, "## Checkpoint Nudge\nstaleness: 4 commits\n")

		for i := 0; i < 3; i++ {
			checkpointTestCommit(t, root, fmt.Sprintf("c%d", i))
		}
		if got := CheckpointNudge(context.Background(), root); got != "" {
			t.Fatalf("3 commits (< 4): got %q, want quiet", got)
		}
		checkpointTestCommit(t, root, "c3")
		got := CheckpointNudge(context.Background(), root)
		want := fmt.Sprintf("review watermark is 4 commits behind main (threshold: 4 commits); consider running a sweep (lead-review range: %s..main).", head)
		if got != want {
			t.Fatalf("legacy trigger = %q, want %q", got, want)
		}
	})

	t.Run("legacy-only staleness above 20 preserves cutoff at 20", func(t *testing.T) {
		root := t.TempDir()
		reviewTestInitRepoOnBranch(t, root, "main")
		head := strings.TrimSpace(string(reviewTestRunGitOutput(t, root, "rev-parse", "HEAD")))
		if err := Append(root, Entry{Base: head, Head: head, Verdict: VerdictPass}); err != nil {
			t.Fatalf("seed ledger: %v", err)
		}
		mustWriteReviewLocalConfig(t, root, "## Checkpoint Nudge\nstaleness: 30 commits\n")

		for i := 0; i < SizeThresholdCommits-1; i++ {
			checkpointTestCommit(t, root, fmt.Sprintf("c%d", i))
		}
		if got := CheckpointNudge(context.Background(), root); got != "" {
			t.Fatalf("19 commits (< 20 cap): got %q, want quiet", got)
		}
		checkpointTestCommit(t, root, "c-cap")
		got := CheckpointNudge(context.Background(), root)
		if !strings.Contains(got, "review watermark is 20 commits behind main (threshold: 20 commits)") {
			t.Fatalf("legacy cap trigger = %q, want 20-commit cutoff", got)
		}
	})

	t.Run("valid new threshold takes precedence over legacy staleness", func(t *testing.T) {
		root := t.TempDir()
		reviewTestInitRepoOnBranch(t, root, "main")
		head := strings.TrimSpace(string(reviewTestRunGitOutput(t, root, "rev-parse", "HEAD")))
		if err := Append(root, Entry{Base: head, Head: head, Verdict: VerdictPass}); err != nil {
			t.Fatalf("seed ledger: %v", err)
		}
		mustWriteReviewLocalConfig(t, root, "## Checkpoint Nudge\nstaleness: 2 commits\nmerges: 5\n")

		for i := 0; i < 4; i++ {
			checkpointTestCommit(t, root, fmt.Sprintf("c%d", i))
		}
		if got := CheckpointNudge(context.Background(), root); got != "" {
			t.Fatalf("new key should take precedence over staleness:2; got %q, want quiet", got)
		}
	})
}

// TestCheckpointNudgeOriginatesFromFrontierNotRawTail pins the frontier rule:
// with a trailing block entry after an older pass entry, the advisory measures
// from the pass frontier rather than the block tail.
func TestCheckpointNudgeOriginatesFromFrontierNotRawTail(t *testing.T) {
	root := t.TempDir()
	reviewTestInitRepoOnBranch(t, root, "main")
	passHead := strings.TrimSpace(string(reviewTestRunGitOutput(t, root, "rev-parse", "HEAD")))
	if err := Append(root, Entry{Base: passHead, Head: passHead, Verdict: VerdictPass}); err != nil {
		t.Fatalf("seed pass entry: %v", err)
	}
	mustWriteReviewLocalConfig(t, root, "## Checkpoint Nudge\nmerges: 2\nlines: 99999\n")

	blockHead := checkpointTestMergeBranch(t, root, "main", "m-before-block", map[string][]byte{"a.go": []byte("a\n")})
	if err := Append(root, Entry{Base: passHead, Head: blockHead, Verdict: VerdictBlock, Ref: "260901-bug-example"}); err != nil {
		t.Fatalf("seed trailing block entry: %v", err)
	}

	// One more merge: 1 merge from blockHead (would stay quiet), 2 merges from passHead (fires).
	checkpointTestMergeBranch(t, root, "main", "m-after-block", map[string][]byte{"b.go": []byte("b\n")})

	got := CheckpointNudge(context.Background(), root)
	if !strings.Contains(got, "review watermark is 2 merges,") {
		t.Fatalf("CheckpointNudge with trailing block = %q, want 2 merges measured from pass frontier", got)
	}
	if !strings.Contains(got, passHead+"..main") || strings.Contains(got, blockHead) {
		t.Fatalf("CheckpointNudge = %q, want frontier head %q and not block head %q", got, passHead, blockHead)
	}
}

func TestCheckpointNudgeSkipsBehindTrackArmWhenTrackUnresolvable(t *testing.T) {
	root := t.TempDir()
	reviewTestInitRepoOnBranch(t, root, "feature-only")
	head := strings.TrimSpace(string(reviewTestRunGitOutput(t, root, "rev-parse", "HEAD")))
	if err := Append(root, Entry{Base: head, Head: head, Verdict: VerdictPass}); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}

	got := CheckpointNudge(context.Background(), root)
	if got != "" {
		t.Fatalf("CheckpointNudge with a ledger but no resolvable track = %q, want \"\"", got)
	}
}

func TestCheckpointNudgeSurfacesBaselineWithoutTrack(t *testing.T) {
	root := t.TempDir()
	reviewTestInitRepoOnBranch(t, root, "feature-only")

	got := CheckpointNudge(context.Background(), root)
	if !strings.Contains(got, "no review ledger yet") {
		t.Fatalf("CheckpointNudge with no track and no ledger = %q, want baseline-missing advisory", got)
	}
	if _, err := os.Stat(LedgerPath(root)); !os.IsNotExist(err) {
		t.Fatalf("CheckpointNudge must never create the ledger file on the no-ledger path; stat err = %v", err)
	}
}

func TestCheckpointNudgePartialCloneDoesNotLazyFetchOrWriteLedger(t *testing.T) {
	origin := t.TempDir()
	reviewTestInitRepoOnBranch(t, origin, "main")
	reviewTestRunGit(t, origin, "config", "uploadpack.allowFilter", "true")
	reviewTestRunGit(t, origin, "config", "uploadpack.allowAnySHA1InWant", "true")

	baseHead := strings.TrimSpace(string(reviewTestRunGitOutput(t, origin, "rev-parse", "HEAD")))
	checkpointTestCommitFiles(t, origin, "large-change", map[string][]byte{
		"payload.txt": checkpointTestLines(defaultLinesThreshold + 50),
	})

	cloneDir := filepath.Join(t.TempDir(), "clone")
	originURL := "file://" + filepath.ToSlash(origin)
	if !strings.HasPrefix(originURL, "file:///") {
		originURL = "file:///" + strings.TrimPrefix(originURL, "file://")
	}
	reviewTestRunGit(t, filepath.Dir(cloneDir), "clone", "--no-checkout", "--filter=blob:none", originURL, cloneDir)

	if err := Append(cloneDir, Entry{Base: baseHead, Head: baseHead, Verdict: VerdictPass}); err != nil {
		t.Fatalf("seed ledger in partial clone: %v", err)
	}
	ledgerBefore, err := os.ReadFile(LedgerPath(cloneDir))
	if err != nil {
		t.Fatalf("read ledger before: %v", err)
	}

	missingBefore := strings.TrimSpace(string(reviewTestRunGitOutput(t, cloneDir, "rev-list", "--objects", "--missing=print", "HEAD")))
	if !strings.Contains(missingBefore, "?") {
		t.Fatalf("expected missing promisor blobs in partial clone, got: %q", missingBefore)
	}

	got := CheckpointNudge(context.Background(), cloneDir)
	if got != "" {
		t.Fatalf("partial clone with missing local objects = %q, want empty (fail open without lazy fetch)", got)
	}

	missingAfter := strings.TrimSpace(string(reviewTestRunGitOutput(t, cloneDir, "rev-list", "--objects", "--missing=print", "HEAD")))
	if missingAfter != missingBefore {
		t.Fatalf("CheckpointNudge triggered a lazy fetch in partial clone:\nbefore:\n%s\nafter:\n%s", missingBefore, missingAfter)
	}
	ledgerAfter, err := os.ReadFile(LedgerPath(cloneDir))
	if err != nil {
		t.Fatalf("read ledger after: %v", err)
	}
	if string(ledgerAfter) != string(ledgerBefore) {
		t.Fatalf("CheckpointNudge mutated ledger on partial-clone fail-open")
	}
}

func TestCheckpointNudgeSubprocessCountIsConstantAcrossManyMerges(t *testing.T) {
	setup := func(t *testing.T, mergeCount int) (string, int) {
		t.Helper()
		root := t.TempDir()
		reviewTestInitRepoOnBranch(t, root, "develop")
		checkpointTestWriteFile(t, root, "AGENTS.md", []byte("## Workflow\n\n### Review Policy\nreview-track: develop\n"))
		reviewTestRunGit(t, root, "add", "AGENTS.md")
		reviewTestRunGit(t, root, "commit", "-m", "review policy")

		head := strings.TrimSpace(string(reviewTestRunGitOutput(t, root, "rev-parse", "HEAD")))
		if err := Append(root, Entry{Base: head, Head: head, Verdict: VerdictPass}); err != nil {
			t.Fatalf("seed ledger: %v", err)
		}
		for i := 0; i < mergeCount; i++ {
			checkpointTestMergeBranch(t, root, "develop", fmt.Sprintf("m%02d", i), map[string][]byte{
				fmt.Sprintf("f%02d.go", i): []byte("package p\n"),
			})
		}
		counter := &countingGitRunner{inner: wsgit.ExecRunner{Env: localOnlyGitEnv}}
		nudge := CheckpointNudgeWith(context.Background(), counter, root)
		return nudge, len(counter.calls)
	}

	ordinaryNudge, ordinaryCalls := setup(t, 3)
	if ordinaryNudge != "" {
		t.Fatalf("3-merge ordinary range = %q, want quiet", ordinaryNudge)
	}
	manyNudge, manyCalls := setup(t, 20)
	if !strings.Contains(manyNudge, "review watermark is 20 merges,") {
		t.Fatalf("20-merge range = %q, want 20 merges advisory", manyNudge)
	}
	if ordinaryCalls != 3 || manyCalls != 3 {
		t.Fatalf("Git subprocess counts: ordinary(3 merges)=%d, many(20 merges)=%d; want constant 3 (merge-base, log, diff)", ordinaryCalls, manyCalls)
	}
}
