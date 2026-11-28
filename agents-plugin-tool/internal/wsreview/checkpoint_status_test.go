package wsreview

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsgit"
)

func checkpointStatusSeed(t *testing.T, branch string) (root, head string) {
	t.Helper()
	root = t.TempDir()
	reviewTestInitRepoOnBranch(t, root, branch)
	head = strings.TrimSpace(string(reviewTestRunGitOutput(t, root, "rev-parse", "HEAD")))
	if err := Append(root, Entry{Base: head, Head: head, Verdict: VerdictPass, Ref: ""}); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	return root, head
}

func statusOf(t *testing.T, root string) CheckpointStatus {
	t.Helper()
	return CheckpointStatusWith(context.Background(), wsgit.ExecRunner{}, root)
}

func TestCheckpointStatusOKReportsFrontierAndCounts(t *testing.T) {
	root, head := checkpointStatusSeed(t, "main")
	for i := 0; i < 2; i++ {
		checkpointTestMergeBranch(t, root, "main", fmt.Sprintf("b%d", i), map[string][]byte{
			fmt.Sprintf("f%d.go", i): []byte("a\nb\nc\n"),
		})
	}
	before, _ := os.ReadFile(LedgerPath(root))

	st := statusOf(t, root)
	if st.State != CheckpointOK || !st.HasLedger || st.Nudge || st.NudgeText != "" {
		t.Fatalf("status = %+v, want ok without a nudge", st)
	}
	if st.Frontier.Head != head || st.Frontier.Base != head || st.Frontier.Verdict != VerdictPass || st.Track != "main" {
		t.Errorf("frontier/track = %+v / %q, want %s pass on main", st.Frontier, st.Track, head)
	}
	if st.MergesBehind != 2 || st.LinesBehind != 6 {
		t.Errorf("behind = %d merges, %d lines, want 2 and 6", st.MergesBehind, st.LinesBehind)
	}
	if st.MergesThreshold != defaultMergesThreshold || st.LinesThreshold != defaultLinesThreshold || st.LegacyMode || st.LegacyThreshold != 0 {
		t.Errorf("thresholds = %+v, want the shipped metric defaults", st)
	}
	after, _ := os.ReadFile(LedgerPath(root))
	if string(before) != string(after) {
		t.Errorf("status mutated the ledger: %q -> %q", before, after)
	}
}

func TestCheckpointStatusWarnCarriesTheNudgeText(t *testing.T) {
	root, head := checkpointStatusSeed(t, "main")
	mustWriteReviewLocalConfig(t, root, "## Checkpoint Nudge\nmerges: 2\n")
	for i := 0; i < 2; i++ {
		checkpointTestMergeBranch(t, root, "main", fmt.Sprintf("b%d", i), map[string][]byte{
			fmt.Sprintf("f%d.go", i): []byte("x\n"),
		})
	}
	st := statusOf(t, root)
	if st.State != CheckpointWarn || !st.Nudge || st.MergesBehind != 2 || st.MergesThreshold != 2 {
		t.Fatalf("status = %+v, want warn at 2 merges / threshold 2", st)
	}
	if want := CheckpointNudge(context.Background(), root); st.NudgeText != want || !strings.Contains(want, head+"..main") {
		t.Errorf("nudge_text = %q, want CheckpointNudge's %q", st.NudgeText, want)
	}
}

func TestCheckpointStatusLegacyMode(t *testing.T) {
	root, _ := checkpointStatusSeed(t, "main")
	mustWriteReviewLocalConfig(t, root, "## Checkpoint Nudge\nstaleness: 30 commits\n")
	for i := 0; i < 3; i++ {
		checkpointTestCommit(t, root, fmt.Sprintf("c%d", i))
	}
	st := statusOf(t, root)
	if st.State != CheckpointOK || !st.LegacyMode || st.LegacyCommitsBehind != 3 || st.LegacyThreshold != SizeThresholdCommits {
		t.Fatalf("status = %+v, want legacy mode, 3 behind, threshold capped at %d", st, SizeThresholdCommits)
	}
	if st.MergesThreshold != 0 || st.LinesThreshold != 0 || st.MergesBehind != 0 || st.LinesBehind != 0 {
		t.Errorf("metric fields in legacy mode = %+v, want zero", st)
	}

	mustWriteReviewLocalConfig(t, root, "## Checkpoint Nudge\nstaleness: 3 commits\n")
	st = statusOf(t, root)
	if st.State != CheckpointWarn || !st.Nudge || st.LegacyThreshold != 3 || !strings.Contains(st.NudgeText, "3 commits behind main") {
		t.Errorf("status at the legacy threshold = %+v, want warn", st)
	}
}

func TestCheckpointStatusMissingAndNoRepo(t *testing.T) {
	t.Run("repository without a ledger is missing and stays unwritten", func(t *testing.T) {
		root := t.TempDir()
		reviewTestInitRepoOnBranch(t, root, "main")
		st := statusOf(t, root)
		if st != (CheckpointStatus{State: CheckpointMissing}) {
			t.Fatalf("status = %+v, want bare missing", st)
		}
		if _, err := os.Stat(LedgerPath(root)); !os.IsNotExist(err) {
			t.Fatalf("status created the ledger; stat err = %v", err)
		}
	})
	t.Run("not a git repository", func(t *testing.T) {
		if st := statusOf(t, t.TempDir()); st != (CheckpointStatus{State: CheckpointNoRepo}) {
			t.Fatalf("status = %+v, want bare norepo", st)
		}
	})
	t.Run("ledger but no resolvable track is norepo", func(t *testing.T) {
		root, _ := checkpointStatusSeed(t, "feature-only")
		if st := statusOf(t, root); st != (CheckpointStatus{State: CheckpointNoRepo}) {
			t.Fatalf("status = %+v, want bare norepo", st)
		}
	})
}

// TestCheckpointStatusUncomputableCountsStayOK: a frontier the repository does
// not hold leaves the behind counts 0 and the state ok rather than failing.
func TestCheckpointStatusUncomputableCountsStayOK(t *testing.T) {
	root := t.TempDir()
	reviewTestInitRepoOnBranch(t, root, "main")
	ghost := strings.Repeat("ab", 20)
	if err := Append(root, Entry{Base: ghost, Head: ghost, Verdict: VerdictConcern}); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	st := statusOf(t, root)
	if st.State != CheckpointOK || !st.HasLedger || st.Frontier.Head != ghost || st.Frontier.Verdict != VerdictConcern || st.Track != "main" {
		t.Fatalf("status = %+v, want ok with the ghost frontier", st)
	}
	if st.MergesBehind != 0 || st.LinesBehind != 0 || st.Nudge {
		t.Errorf("status = %+v, want zero counts and no nudge", st)
	}
}

// TestCheckpointStatusJSONShape pins the wire keys the dashboard page reads.
func TestCheckpointStatusJSONShape(t *testing.T) {
	raw, err := json.Marshal(CheckpointStatus{State: CheckpointMissing})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"state", "has_ledger", "frontier", "track", "legacy_mode", "merges_behind", "lines_behind",
		"merges_threshold", "lines_threshold", "legacy_commits_behind", "legacy_threshold", "nudge", "nudge_text"} {
		if _, ok := m[key]; !ok {
			t.Errorf("JSON lacks %q: %s", key, raw)
		}
	}
	fr, _ := m["frontier"].(map[string]any)
	for _, key := range []string{"base", "head", "verdict", "ref"} {
		if _, ok := fr[key]; !ok {
			t.Errorf("frontier JSON lacks %q: %s", key, raw)
		}
	}
}
