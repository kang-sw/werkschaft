package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsreview"
)

// reviewNudgeTestRepo inits a repo on an explicit "main" branch (avoiding any
// dependency on the test runner's init.defaultBranch git config, since
// wsreview.ResolveTrack's pre-④ fallback needs a resolvable local main or
// master ref when no origin remote exists) with one seed commit.
func reviewNudgeTestRepo(t *testing.T, root string) {
	t.Helper()
	initGit(t, root)
	runGit(t, root, "checkout", "-b", "main")
	mustWrite(t, root, "README.md", "root\n")
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "root commit")
}

// reviewNudgeTestCommit adds one trivial commit to root's current branch and
// returns its full SHA.
func reviewNudgeTestCommit(t *testing.T, root, label string) string {
	t.Helper()
	mustWrite(t, root, label+".txt", label+"\n")
	runGit(t, root, "add", label+".txt")
	runGit(t, root, "commit", "-m", label)
	return strings.TrimSpace(string(runGitOutput(t, root, "rev-parse", "HEAD")))
}

// reviewNudgeSeedStaleMarker stamps a pass entry at current HEAD, then lands
// a qualifying reviewable change past the shipped metric threshold so the
// ledger marker is stale over whatever branch is current at call time.
func reviewNudgeSeedStaleMarker(t *testing.T, root string, aheadCommits int) {
	t.Helper()
	head := strings.TrimSpace(string(runGitOutput(t, root, "rev-parse", "HEAD")))
	if err := wsreview.Append(root, wsreview.Entry{Base: head, Head: head, Verdict: wsreview.VerdictPass}); err != nil {
		t.Fatalf("seed review ledger: %v", err)
	}
	var b strings.Builder
	lines := 5000
	if aheadCommits > 0 && aheadCommits > lines {
		lines = aheadCommits
	}
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "line-%05d\n", i)
	}
	mustWrite(t, root, "stale-change.txt", b.String())
	runGit(t, root, "add", "stale-change.txt")
	runGit(t, root, "commit", "-m", "qualifying stale change")
}

func reviewNudgeMergeCommit(t *testing.T, root, track, branch, relPath, content string) {
	t.Helper()
	runGit(t, root, "checkout", "-b", branch, track)
	mustWrite(t, root, relPath, content)
	runGit(t, root, "add", relPath)
	runGit(t, root, "commit", "-m", branch)
	runGit(t, root, "checkout", track)
	runGit(t, root, "merge", "--no-ff", branch, "-m", "merge "+branch)
}

// reviewNudgeRestamp appends a fresh pass entry at current HEAD, simulating a
// just-completed sweep that advances the marker to the current tip.
func reviewNudgeRestamp(t *testing.T, root string) {
	t.Helper()
	head := strings.TrimSpace(string(runGitOutput(t, root, "rev-parse", "HEAD")))
	if err := wsreview.Append(root, wsreview.Entry{Base: head, Head: head, Verdict: wsreview.VerdictPass}); err != nil {
		t.Fatalf("restamp review ledger: %v", err)
	}
}

// TestServeStdioTicketsCloseReviewWatermarkNudgeSurfacesAndQuiets covers the
// first of the three cheap-checkpoint call sites: tickets.close must carry a
// "review-watermark:" advisory when the track (here, local "main" fallback)
// is stale past the ledger marker by more than the size threshold, and must
// stay silent right after the ledger is restamped to the current tip.
func TestServeStdioTicketsCloseReviewWatermarkNudgeSurfacesAndQuiets(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	reviewNudgeTestRepo(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	reviewNudgeSeedStaleMarker(t, root, wsreview.SizeThresholdCommits+2)

	stemStale := "260101-feat-review-watermark-close-nudge-stale"
	mustWrite(t, root, filepath.Join("ai-docs", "tickets", "todo", stemStale+".md"), "---\ntitle: Stale\n---\n\nBody.\n")
	stemFresh := "260101-feat-review-watermark-close-nudge-fresh"
	mustWrite(t, root, filepath.Join("ai-docs", "tickets", "todo", stemFresh+".md"), "---\ntitle: Fresh\n---\n\nBody.\n")

	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 1, root, nil))

	staleResp := callToolWithKey(t, server, 2, key, "tickets.close", map[string]any{"stem": stemStale, "status": "done"})
	if !strings.Contains(staleResp, "review-watermark:") {
		t.Fatalf("tickets.close should carry the review-watermark nudge for a stale range: %s", staleResp)
	}

	reviewNudgeRestamp(t, root)

	freshResp := callToolWithKey(t, server, 3, key, "tickets.close", map[string]any{"stem": stemFresh, "status": "done"})
	if strings.Contains(freshResp, "review-watermark:") {
		t.Fatalf("tickets.close should stay silent right after the ledger was restamped: %s", freshResp)
	}
}

// TestServeStdioReviewWatermarkNudgeSkipsWorkerKeys: the sweep belongs to a
// top-level lead, so a worker key (lead-scoped with a parent) gets no nudge
// from tickets.close or route.resolve_implement while the lead still does.
func TestServeStdioReviewWatermarkNudgeSkipsWorkerKeys(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	reviewNudgeTestRepo(t, root)
	reviewNudgeSeedStaleMarker(t, root, wsreview.SizeThresholdCommits+2)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	server := NewServer(root, "test")
	leadKey, _ := parseLoginResponse(t, callLogin(t, server, 1, root, nil))
	workerKey, err := server.sessions.mint(root, roleLead, leadKey)
	if err != nil {
		t.Fatalf("mint worker key: %v", err)
	}

	stems := []string{"260101-feat-review-nudge-worker", "260101-feat-review-nudge-lead"}
	for _, stem := range stems {
		mustWrite(t, root, filepath.Join("ai-docs", "tickets", "todo", stem+".md"), "---\ntitle: T\n---\n\nBody.\n")
	}
	closed := callToolWithKey(t, server, 2, workerKey, "tickets.close", map[string]any{"stem": stems[0], "status": "done"})
	if !strings.Contains(closed, ".done") || strings.Contains(closed, "review-watermark:") {
		t.Fatalf("worker key must close the ticket without the review-watermark nudge: %s", closed)
	}
	if got := callToolWithKey(t, server, 3, leadKey, "tickets.close", map[string]any{"stem": stems[1], "status": "done"}); !strings.Contains(got, "review-watermark:") {
		t.Fatalf("top-level lead lost the review-watermark nudge on tickets.close: %s", got)
	}

	runGit(t, root, "switch", "-c", "feature/base")
	routed := callToolWithKey(t, server, 4, workerKey, "route.resolve_implement", implementReadyArgs("text"))
	if !strings.Contains(routed, "Implementation Verdict") || strings.Contains(routed, "review-watermark:") {
		t.Fatalf("worker key must route without the review-watermark nudge: %s", routed)
	}
	if got := callToolWithKey(t, server, 5, leadKey, "route.resolve_implement", implementReadyArgs("text")); !strings.Contains(got, "review-watermark:") {
		t.Fatalf("top-level lead lost the review-watermark nudge on route.resolve_implement: %s", got)
	}
}

// TestServeStdioWorkflowManualReviewWatermarkNudgeSurfacesAndQuiets covers the
// second call site (session start): both the FRESH-with-root and CONTINUE
// branches of workflow_manual must carry the nudge on a stale range and go
// quiet once restamped.
func TestServeStdioWorkflowManualReviewWatermarkNudgeSurfacesAndQuiets(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	reviewNudgeTestRepo(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("WS_RSRC_ROOT", filepath.Join("..", "..", "..", "agents-plugin", "rsrc"))

	reviewNudgeSeedStaleMarker(t, root, wsreview.SizeThresholdCommits+2)

	s := NewServer(root, "test")

	// workflow_manual injects the raw CheckpointNudge text directly (no
	// "review-watermark:" label prefix, unlike the other two call sites),
	// mirroring the computeManuals injection shape.
	const nudgeMarker = "review watermark is"

	freshResp := callToolWithKey(t, s, 1, freshBootstrapKey, "workflow_manual", map[string]any{"root": root})
	if !strings.HasPrefix(freshResp, workflowManualReadFullNotice+"\n\n") {
		t.Fatalf("workflow_manual FRESH-with-root must start with the full-result notice: %s", freshResp)
	}
	if !strings.Contains(freshResp, nudgeMarker) {
		t.Fatalf("workflow_manual FRESH-with-root should carry the review-watermark nudge for a stale range: %s", freshResp)
	}

	key, _ := parseLoginResponse(t, callLogin(t, s, 2, root, nil))
	continueRespStale := callToolWithKey(t, s, 3, key, "workflow_manual", nil)
	if !strings.HasPrefix(continueRespStale, workflowManualReadFullNotice+"\n\n") {
		t.Fatalf("workflow_manual CONTINUE must start with the full-result notice: %s", continueRespStale)
	}
	if !strings.Contains(continueRespStale, nudgeMarker) {
		t.Fatalf("workflow_manual CONTINUE should carry the review-watermark nudge for a stale range: %s", continueRespStale)
	}

	reviewNudgeRestamp(t, root)

	continueRespFresh := callToolWithKey(t, s, 4, key, "workflow_manual", nil)
	if strings.Contains(continueRespFresh, nudgeMarker) {
		t.Fatalf("workflow_manual CONTINUE should stay silent right after the ledger was restamped: %s", continueRespFresh)
	}
}

// TestServeStdioEnterImplementReviewWatermarkNudgeSurfacesAndQuiets
// covers the third call site (handleEnterImplement).
// The extra commits land on the track branch ("main") before the session's
// working branch is cut from its tip, mirroring
// TestEnterImplementNewSchemaReturnsVerdictAndStoresAgenda's branch setup.
func TestServeStdioEnterImplementReviewWatermarkNudgeSurfacesAndQuiets(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	reviewNudgeTestRepo(t, root)
	reviewNudgeSeedStaleMarker(t, root, wsreview.SizeThresholdCommits+2)
	runGit(t, root, "switch", "-c", "feature/base")
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 1, root, nil))

	staleText := callToolWithKey(t, server, 2, key, "route.resolve_implement", implementReadyArgs("text"))
	if !strings.Contains(staleText, "review-watermark:") {
		t.Fatalf("route.resolve_implement should carry the review-watermark nudge for a stale range: %s", staleText)
	}

	runGit(t, root, "checkout", "main")
	reviewNudgeRestamp(t, root)
	runGit(t, root, "checkout", "feature/base")

	freshText := callToolWithKey(t, server, 3, key, "route.resolve_implement", implementReadyArgs("text"))
	if strings.Contains(freshText, "review-watermark:") {
		t.Fatalf("route.resolve_implement should stay silent right after the ledger was restamped: %s", freshText)
	}
}

// TestServeStdioWorkflowManualReviewWatermarkNudgeFailsOpenOnUnresolvableTrack
// proves the fail-open contract: when no origin/HEAD and neither main nor
// master exists (here compounded with a detached HEAD), CheckpointNudge must
// never error or block the underlying tool call — it silently omits the
// nudge line.
func TestServeStdioWorkflowManualReviewWatermarkNudgeFailsOpenOnUnresolvableTrack(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	initGit(t, root)
	runGit(t, root, "checkout", "-b", "topic")
	mustWrite(t, root, "README.md", "root\n")
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "root commit")
	head := strings.TrimSpace(string(runGitOutput(t, root, "rev-parse", "HEAD")))
	runGit(t, root, "checkout", "--detach", head)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("WS_RSRC_ROOT", filepath.Join("..", "..", "..", "agents-plugin", "rsrc"))

	s := NewServer(root, "test")
	freshResp := callToolWithKey(t, s, 1, freshBootstrapKey, "workflow_manual", map[string]any{"root": root})
	if strings.Contains(freshResp, "review watermark is") {
		t.Fatalf("workflow_manual should stay silent when the review track is unresolvable: %s", freshResp)
	}
	if !strings.Contains(freshResp, "Session Key") {
		t.Fatalf("workflow_manual FRESH-with-root should still succeed (no error/block) when track resolution fails: %s", freshResp)
	}
}

// TestServeStdioTicketsCloseReviewWatermarkNudgeToleratesMalformedLocalConfig
// proves the fail-open contract on the config-parsing side: a malformed
// ai-docs/_review.local.md staleness value must never error the call —
// StalenessKnob falls back to its default, and the nudge still renders.
func TestServeStdioTicketsCloseReviewWatermarkNudgeToleratesMalformedLocalConfig(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	reviewNudgeTestRepo(t, root)
	mustWrite(t, root, filepath.Join("ai-docs", "_review.local.md"), "## Checkpoint Nudge\nstaleness: not-a-number commits\n")
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	reviewNudgeSeedStaleMarker(t, root, wsreview.SizeThresholdCommits+2)

	stem := "260101-feat-review-watermark-close-nudge-malformed-config"
	mustWrite(t, root, filepath.Join("ai-docs", "tickets", "todo", stem+".md"), "---\ntitle: Malformed config\n---\n\nBody.\n")

	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 1, root, nil))

	resp := callToolWithKey(t, server, 2, key, "tickets.close", map[string]any{"stem": stem, "status": "done"})
	if !strings.Contains(resp, "review-watermark:") {
		t.Fatalf("tickets.close should still surface the nudge (falling back to the default staleness) despite a malformed config: %s", resp)
	}
}

// TestServeStdioReviewMarkerReadsAbsentAndPresentEntry covers the new
// review.marker tool's plain (non-bootstrap) read path.
func TestServeStdioReviewMarkerReadsAbsentAndPresentEntry(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	reviewNudgeTestRepo(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 1, root, nil))

	absent := callToolWithKey(t, server, 2, key, "review.marker", nil)
	if !strings.Contains(absent, "no entry yet") {
		t.Fatalf("review.marker on an empty ledger should report no entry yet: %s", absent)
	}

	head := strings.TrimSpace(string(runGitOutput(t, root, "rev-parse", "HEAD")))
	if err := wsreview.Append(root, wsreview.Entry{Base: head, Head: head, Verdict: wsreview.VerdictPass}); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	present := callToolWithKey(t, server, 3, key, "review.marker", nil)
	if !strings.Contains(present, head) || !strings.Contains(present, "pass") {
		t.Fatalf("review.marker should surface the latest entry: %s", present)
	}
}

// TestServeStdioReviewMarkerBootstrapCreatesExactlyOneEntryAndIsIdempotent
// covers review.marker(bootstrap: true): the sole caller-opted-in bootstrap
// trigger, which must be a no-op on a second call.
func TestServeStdioReviewMarkerBootstrapCreatesExactlyOneEntryAndIsIdempotent(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	reviewNudgeTestRepo(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 1, root, nil))

	first := callToolWithKey(t, server, 2, key, "review.marker", map[string]any{"bootstrap": true})
	if !strings.Contains(first, "bootstrapped") {
		t.Fatalf("first review.marker(bootstrap: true) should report a bootstrap: %s", first)
	}

	second := callToolWithKey(t, server, 3, key, "review.marker", map[string]any{"bootstrap": true})
	if strings.Contains(second, "bootstrapped") {
		t.Fatalf("second review.marker(bootstrap: true) must be idempotent (no-op), got: %s", second)
	}

	raw, err := os.ReadFile(wsreview.LedgerPath(root))
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	// Count only entry lines: the Phase-3 banner prepends `#`-prefixed
	// comment lines at first creation, which the parser (and this idempotency
	// assertion) must skip. The invariant is exactly one *entry*, not one raw
	// file line.
	var entries []string
	for _, ln := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		entries = append(entries, ln)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one ledger entry after repeated bootstrap, got %d: %v", len(entries), entries)
	}
}

// TestServeStdioReviewStampRoundTripsThroughMarker covers review.stamp
// appending an entry that review.marker then reads back.
func TestServeStdioReviewStampRoundTripsThroughMarker(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	reviewNudgeTestRepo(t, root)
	base := strings.TrimSpace(string(runGitOutput(t, root, "rev-parse", "HEAD")))
	head := reviewNudgeTestCommit(t, root, "extra")
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 1, root, nil))

	for _, name := range []string{"review.marker", "review.stamp"} {
		resp := callToolOnce(t, server, 2, name, map[string]any{
			"base": base, "head": head, "verdict": "pass",
		})
		if !strings.Contains(resp, `"isError":true`) || !strings.Contains(toolText(t, resp), "mandatory_session_key") {
			t.Fatalf("%s without session_key should fail root resolution: %s", name, resp)
		}
	}

	stampResp := callToolOnce(t, server, 3, "review.stamp", map[string]any{
		"session_key": key, "base": base, "head": head, "verdict": "pass",
	})
	if !strings.Contains(stampResp, "stamped") {
		t.Fatalf("review.stamp should confirm the append: %s", stampResp)
	}

	marker := callToolOnce(t, server, 4, "review.marker", map[string]any{
		"session_key": key, "format": "json",
	})
	var got reviewMarkerJSON
	if err := json.Unmarshal([]byte(toolText(t, marker)), &got); err != nil {
		t.Fatalf("review.marker should return JSON: %v\n%s", err, marker)
	}
	if !got.Found || got.Base != base || got.Head != head || got.Verdict != "pass" {
		t.Fatalf("review.marker should round-trip the stamped entry: %+v", got)
	}
}

// TestServeStdioReviewMarkerReportsFrontierNotRawTailAfterBlock pins the
// frontier switch at the MCP boundary: after a pass-then-block sequence,
// review.marker (bare read) must still report the pass entry — the
// frontier — not the trailing block entry.
func TestServeStdioReviewMarkerReportsFrontierNotRawTailAfterBlock(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	reviewNudgeTestRepo(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 1, root, nil))

	passBase := strings.TrimSpace(string(runGitOutput(t, root, "rev-parse", "HEAD")))
	passHead := reviewNudgeTestCommit(t, root, "pass-head")
	if err := wsreview.Append(root, wsreview.Entry{Base: passBase, Head: passHead, Verdict: wsreview.VerdictPass}); err != nil {
		t.Fatalf("seed pass entry: %v", err)
	}
	blockHead := reviewNudgeTestCommit(t, root, "block-head")
	if err := wsreview.Append(root, wsreview.Entry{Base: passHead, Head: blockHead, Verdict: wsreview.VerdictBlock, Ref: "260901-bug-example"}); err != nil {
		t.Fatalf("seed trailing block entry: %v", err)
	}

	resp := callToolWithKey(t, server, 2, key, "review.marker", nil)
	if !strings.Contains(resp, passHead) || !strings.Contains(resp, "pass") {
		t.Fatalf("review.marker should report the frontier (pass) entry, not the trailing block: %s", resp)
	}
	if strings.Contains(resp, blockHead) {
		t.Fatalf("review.marker should not report the trailing block entry's head: %s", resp)
	}
}

// TestServeStdioReviewMarkerFormatJSONReturnsStructuredFrontier covers the
// new format=json output: structured base/head/verdict fields, sourced from
// the frontier so a caller (the lead-ship gate) can consume the head without
// string-scraping.
func TestServeStdioReviewMarkerFormatJSONReturnsStructuredFrontier(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	reviewNudgeTestRepo(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 1, root, nil))

	base := strings.TrimSpace(string(runGitOutput(t, root, "rev-parse", "HEAD")))
	head := reviewNudgeTestCommit(t, root, "extra")
	if err := wsreview.Append(root, wsreview.Entry{Base: base, Head: head, Verdict: wsreview.VerdictPass}); err != nil {
		t.Fatalf("seed pass entry: %v", err)
	}

	resp := callToolWithKey(t, server, 2, key, "review.marker", map[string]any{"format": "json"})
	var got struct {
		Base    string `json:"base"`
		Head    string `json:"head"`
		Verdict string `json:"verdict"`
		Ref     string `json:"ref"`
		Found   bool   `json:"found"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp)), &got); err != nil {
		t.Fatalf("review.marker(format: json) did not return valid JSON: %v\nresponse: %s", err, resp)
	}
	if got.Base != base || got.Head != head || got.Verdict != wsreview.VerdictPass || !got.Found {
		t.Fatalf("review.marker(format: json) = %+v, want base=%q head=%q verdict=pass found=true", got, base, head)
	}
}

// TestServeStdioReviewMarkerBootstrapFormatJSONReturnsJSONOnAllBlockNoFloor
// covers the bootstrap branch's near-unreachable all-block-no-floor path: a
// ledger whose only entry is a block (so wsreview.Read/Bootstrap's own
// idempotency check finds an entry and no-ops) but which therefore has no
// resolvable frontier (block never clears). A bootstrap:true, format:json
// call in that state must still return a JSON object (found: false), not
// fall through to the plain-text "no entry yet" message — pinning the same
// wantsJSON-before-!found ordering the bare-read branch already had.
func TestServeStdioReviewMarkerBootstrapFormatJSONReturnsJSONOnAllBlockNoFloor(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	reviewNudgeTestRepo(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	head := strings.TrimSpace(string(runGitOutput(t, root, "rev-parse", "HEAD")))
	if err := wsreview.Append(root, wsreview.Entry{Base: head, Head: head, Verdict: wsreview.VerdictBlock, Ref: "260901-bug-example"}); err != nil {
		t.Fatalf("seed all-block ledger: %v", err)
	}

	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 1, root, nil))

	resp := callToolWithKey(t, server, 2, key, "review.marker", map[string]any{"bootstrap": true, "format": "json"})
	var got struct {
		Found bool `json:"found"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp)), &got); err != nil {
		t.Fatalf("review.marker(bootstrap: true, format: json) on an all-block ledger did not return valid JSON: %v\nresponse: %s", err, resp)
	}
	if got.Found {
		t.Fatalf("review.marker(bootstrap: true, format: json) on an all-block ledger = %+v, want found=false (no resolvable frontier)", got)
	}
}

// TestServeStdioReviewStampRejectsBlockVerdictWithoutRef mirrors Phase 1's
// Append test coverage shape (ledger_test.go's TestAppendBlockWithEmptyRefFails)
// at the MCP tool boundary: review.stamp must surface Append's own validation
// error as the tool error, not swallow it.
func TestServeStdioReviewStampRejectsBlockVerdictWithoutRef(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	reviewNudgeTestRepo(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))

	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 1, root, nil))

	head := strings.TrimSpace(string(runGitOutput(t, root, "rev-parse", "HEAD")))
	resp := callToolOnce(t, server, 2, "review.stamp", map[string]any{
		"session_key": key, "base": head, "head": head, "verdict": "block",
	})
	if !toolIsError(t, resp) {
		t.Fatalf("review.stamp with verdict=block and no ref should error: %s", resp)
	}
	if !strings.Contains(toolText(t, resp), "requires a non-empty Ref") {
		t.Fatalf("review.stamp block-without-ref error message unexpected: %s", toolText(t, resp))
	}
}

// TestReviewWatermarkAdvisoryParityAcrossActiveEmitters verifies that all
// active review-watermark checkpoint emitters — workflow_manual FRESH-with-root,
// workflow_manual CONTINUE, hosted RenderLeadManual, route.resolve_implement,
// and tickets.close — surface the exact same single-tier advisory computed from
// the new metric + exclusion contract and go quiet together when thresholds are
// not met.
func TestReviewWatermarkAdvisoryParityAcrossActiveEmitters(t *testing.T) {
	useLeadProfile(t)
	root := t.TempDir()
	reviewNudgeTestRepo(t, root)
	t.Setenv("WS_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("WS_RSRC_ROOT", filepath.Join("..", "..", "..", "agents-plugin", "rsrc"))

	frontierHead := strings.TrimSpace(string(runGitOutput(t, root, "rev-parse", "HEAD")))
	if err := wsreview.Append(root, wsreview.Entry{Base: frontierHead, Head: frontierHead, Verdict: wsreview.VerdictPass}); err != nil {
		t.Fatalf("seed review ledger: %v", err)
	}

	// Land 2 eligible merges plus 1 ai-docs-only merge and 1 excluded generated
	// merge so metric filtering is exercised across every emitter.
	reviewNudgeMergeCommit(t, root, "main", "feat-1", "pkg/one.txt", "one\n")
	reviewNudgeMergeCommit(t, root, "main", "docs-only", filepath.Join("ai-docs", "note.md"), "docs\n")
	reviewNudgeMergeCommit(t, root, "main", "gen-only", filepath.Join("generated", "bundle.js"), strings.Repeat("x\n", 6000))
	reviewNudgeMergeCommit(t, root, "main", "feat-2", "pkg/two.txt", "two\n")

	mustWrite(t, root, filepath.Join("ai-docs", "_review.local.md"), "## Checkpoint Nudge\nmerges: 2\nlines: 5000\nexclude: generated/**\n")

	wantAdvisory := fmt.Sprintf(
		"review watermark is 2 merges, 2 net lines behind main (thresholds: 2 merges / 5000 lines); consider running a sweep (lead-review range: %s..main).",
		frontierHead,
	)

	stemStale := "260101-feat-review-watermark-parity-stale"
	mustWrite(t, root, filepath.Join("ai-docs", "tickets", "todo", stemStale+".md"), "---\ntitle: Parity Stale\n---\n\nBody.\n")
	stemQuiet := "260101-feat-review-watermark-parity-quiet"
	mustWrite(t, root, filepath.Join("ai-docs", "tickets", "todo", stemQuiet+".md"), "---\ntitle: Parity Quiet\n---\n\nBody.\n")

	server := NewServer(root, "test")
	key, _ := parseLoginResponse(t, callLogin(t, server, 1, root, nil))

	freshResp := callToolWithKey(t, server, 2, freshBootstrapKey, "workflow_manual", map[string]any{"root": root})
	continueResp := callToolWithKey(t, server, 3, key, "workflow_manual", nil)
	hostedResp, err := RenderLeadManual(os.Environ(), root, HostedRender{Harness: "codex"}, key)
	if err != nil {
		t.Fatalf("RenderLeadManual: %v", err)
	}
	closeResp := callToolWithKey(t, server, 4, key, "tickets.close", map[string]any{"stem": stemStale, "status": "done"})

	runGit(t, root, "switch", "-c", "feature/parity")
	routeResp := callToolWithKey(t, server, 5, key, "route.resolve_implement", implementReadyArgs("text"))

	for name, body := range map[string]string{
		"workflow_manual FRESH":    freshResp,
		"workflow_manual CONTINUE": continueResp,
		"hosted RenderLeadManual":  hostedResp,
		"route.resolve_implement":  routeResp,
		"tickets.close":            closeResp,
	} {
		if !strings.Contains(body, wantAdvisory) {
			t.Fatalf("%s missing expected advisory %q\nbody:\n%s", name, wantAdvisory, body)
		}
	}

	// Raising the local merge threshold above 2 (while keeping generated/**
	// excluded so the 6000-line generated merge does not trip the line threshold)
	// must quiet every active emitter simultaneously.
	mustWrite(t, root, filepath.Join("ai-docs", "_review.local.md"), "## Checkpoint Nudge\nmerges: 3\nlines: 5000\nexclude: generated/**\n")

	freshQuiet := callToolWithKey(t, server, 6, freshBootstrapKey, "workflow_manual", map[string]any{"root": root})
	continueQuiet := callToolWithKey(t, server, 7, key, "workflow_manual", nil)
	hostedQuiet, err := RenderLeadManual(os.Environ(), root, HostedRender{Harness: "codex"}, key)
	if err != nil {
		t.Fatalf("RenderLeadManual quiet: %v", err)
	}
	routeQuiet := callToolWithKey(t, server, 8, key, "route.resolve_implement", implementReadyArgs("text"))
	closeQuiet := callToolWithKey(t, server, 9, key, "tickets.close", map[string]any{"stem": stemQuiet, "status": "done"})

	for name, body := range map[string]string{
		"workflow_manual FRESH":    freshQuiet,
		"workflow_manual CONTINUE": continueQuiet,
		"hosted RenderLeadManual":  hostedQuiet,
		"route.resolve_implement":  routeQuiet,
		"tickets.close":            closeQuiet,
	} {
		if strings.Contains(body, "review watermark is") {
			t.Fatalf("%s should stay quiet once threshold is raised above 2 merges:\n%s", name, body)
		}
	}
}
