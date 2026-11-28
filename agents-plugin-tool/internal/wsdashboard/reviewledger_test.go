package wsdashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsreview"
)

func getLedger(t *testing.T, f *pageFixture) wsreview.CheckpointStatus {
	t.Helper()
	resp, err := testClient().Get(f.base + "/s/livesession/review-ledger")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET review-ledger = %d: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var st wsreview.CheckpointStatus
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("answer %q: %v", body, err)
	}
	return st
}

func ledgerRepo(t *testing.T, root string) string {
	t.Helper()
	gitRun(t, root, "init", "-q", "-b", "main")
	gitCommit(t, root, "seed.txt")
	return ledgerHead(t, root)
}

func TestReviewLedgerRouteStates(t *testing.T) {
	t.Run("norepo for a non-git session root", func(t *testing.T) {
		f := startPageFixture(t)
		if st := getLedger(t, f); st != (wsreview.CheckpointStatus{State: wsreview.CheckpointNoRepo}) {
			t.Fatalf("status = %+v, want bare norepo", st)
		}
	})
	t.Run("norepo for a ledger without a resolvable review track", func(t *testing.T) {
		f := startPageFixture(t)
		gitRun(t, f.project, "init", "-q", "-b", "feature-only")
		gitCommit(t, f.project, "seed.txt")
		head := ledgerHead(t, f.project)
		if err := wsreview.Append(f.project, wsreview.Entry{Base: head, Head: head, Verdict: wsreview.VerdictPass}); err != nil {
			t.Fatal(err)
		}
		if st := getLedger(t, f); st.State != wsreview.CheckpointNoRepo || st.HasLedger {
			t.Fatalf("status = %+v, want norepo", st)
		}
	})
	t.Run("missing without writing the ledger", func(t *testing.T) {
		f := startPageFixture(t)
		ledgerRepo(t, f.project)
		if st := getLedger(t, f); st != (wsreview.CheckpointStatus{State: wsreview.CheckpointMissing}) {
			t.Fatalf("status = %+v, want bare missing", st)
		}
		if _, err := os.Stat(wsreview.LedgerPath(f.project)); !os.IsNotExist(err) {
			t.Fatalf("the route created the ledger; stat err = %v", err)
		}
	})
	t.Run("ok then warn on the metric thresholds", func(t *testing.T) {
		f := startPageFixture(t)
		head := ledgerRepo(t, f.project)
		if err := wsreview.Append(f.project, wsreview.Entry{Base: head, Head: head, Verdict: wsreview.VerdictPass}); err != nil {
			t.Fatal(err)
		}
		st := getLedger(t, f)
		if st.State != wsreview.CheckpointOK || !st.HasLedger || st.Frontier.Head != head || st.Frontier.Verdict != wsreview.VerdictPass || st.Track != "main" || st.Nudge {
			t.Fatalf("status = %+v, want ok on main at %s", st, head)
		}
		if st.MergesThreshold != 10 || st.LinesThreshold != 5000 {
			t.Errorf("thresholds = %d / %d, want the shipped 10 / 5000", st.MergesThreshold, st.LinesThreshold)
		}
		writeReviewLocal(t, f.project, "## Checkpoint Nudge\nlines: 1\n")
		gitCommit(t, f.project, "more.txt")
		st = getLedger(t, f)
		if st.State != wsreview.CheckpointWarn || !st.Nudge || st.LinesThreshold != 1 || st.LinesBehind != 1 || !strings.Contains(st.NudgeText, head+"..main") {
			t.Fatalf("status = %+v, want warn at the 1-line threshold", st)
		}
	})
	t.Run("legacy staleness threshold", func(t *testing.T) {
		f := startPageFixture(t)
		head := ledgerRepo(t, f.project)
		if err := wsreview.Append(f.project, wsreview.Entry{Base: head, Head: head, Verdict: wsreview.VerdictConcern}); err != nil {
			t.Fatal(err)
		}
		writeReviewLocal(t, f.project, "## Checkpoint Nudge\nstaleness: 2 commits\n")
		gitCommit(t, f.project, "a.txt")
		st := getLedger(t, f)
		if st.State != wsreview.CheckpointOK || !st.LegacyMode || st.LegacyCommitsBehind != 1 || st.LegacyThreshold != 2 || st.Frontier.Verdict != wsreview.VerdictConcern {
			t.Fatalf("status = %+v, want legacy ok at 1 of 2", st)
		}
		gitCommit(t, f.project, "b.txt")
		if st = getLedger(t, f); st.State != wsreview.CheckpointWarn || !st.Nudge || st.LegacyCommitsBehind != 2 {
			t.Fatalf("status = %+v, want legacy warn at 2 of 2", st)
		}
	})
}

func ledgerHead(t *testing.T, root string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func writeReviewLocal(t *testing.T, root, body string) {
	t.Helper()
	if err := os.MkdirAll(root+"/ai-docs", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root+"/ai-docs/_review.local.md", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReviewLedgerRouteRefusalsAndFailOpen(t *testing.T) {
	f := startPageFixture(t)
	if code, _ := get(t, f.base+"/s/nosuchsession/review-ledger"); code != http.StatusNotFound {
		t.Errorf("unknown session = %d, want 404", code)
	}
	if code, _, _ := postJSON(t, f.base+"/s/livesession/review-ledger", f.base, ""); code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", code)
	}

	// A runner whose git always fails never turns into a 500.
	prev := reviewLedgerRunner
	reviewLedgerRunner = failingLedgerRunner{}
	t.Cleanup(func() { reviewLedgerRunner = prev })
	head := strings.Repeat("cd", 20)
	if err := wsreview.Append(f.project, wsreview.Entry{Base: head, Head: head, Verdict: wsreview.VerdictPass}); err != nil {
		t.Fatal(err)
	}
	if st := getLedger(t, f); st.State != wsreview.CheckpointNoRepo {
		t.Errorf("status with failing git = %+v, want norepo", st)
	}
}

type failingLedgerRunner struct{}

func (failingLedgerRunner) RunGit(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("git unavailable")
}
