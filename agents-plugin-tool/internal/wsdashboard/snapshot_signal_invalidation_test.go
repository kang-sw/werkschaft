package wsdashboard

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// TestSignalInvalidatesSnapshotWithinValidationWindow pins that the daemon's
// own change signals (POST /nudge, NotifySessionChange, and the index hook
// they drive, sessionChanged) expire Daemon.snapshots' 5 s FileCache
// validation window. A snapshot.json rewritten inside the window is seen at
// once by sessionChanged's reader, by a woken session stream and by the rail
// (both non-fresh readers), with no manual Invalidate after the write.
func TestSignalInvalidatesSnapshotWithinValidationWindow(t *testing.T) {
	f := startPageFixture(t, withWorkspaces(t))
	if _, err := f.d.workspaces.add(f.project, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the index to load", f.d.Index().Loaded)
	const id = "livesession"
	snapPath := wslive.SnapshotPath(SessionDir(f.dir, id))
	rawBody, err := json.Marshal(NudgeBody{ReplyID: id, Project: f.project})
	if err != nil {
		t.Fatal(err)
	}
	body := string(rawBody)
	nudge := func() {
		t.Helper()
		if code, _, raw := postJSON(t, f.base+"/nudge", "", body); code != http.StatusNoContent {
			t.Fatalf("POST /nudge = %d %q, want 204", code, raw)
		}
	}
	replying := func(text string) *wslive.Snapshot {
		return &wslive.Snapshot{SchemaVersion: 1, Adapter: "pi", UpdatedAt: time.Now().UnixMilli(), HeartbeatMs: 30000,
			Lead: wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseReplying}, Reply: &wslive.Reply{Text: text}}}
	}
	replyOf := func() string {
		snap := f.d.snapshots.snapshotWithoutObserved(f.dir, id)
		if snap == nil || snap.Lead.Reply == nil {
			return ""
		}
		return snap.Lead.Reply.Text
	}
	// stale proves the fixture sits inside the validation window: the rewrite
	// is on disk, yet the cache still answers the previous snapshot.
	stale := func(want string) {
		t.Helper()
		if got := replyOf(); got != want {
			t.Fatalf("before the signal the cache read %q, want the cached %q (validation window not exercised)", got, want)
		}
	}
	rowOf := func() string {
		t.Helper()
		region, err := renderRailRegion(f.d.railView(f.d.pagesIn("en")), "en")
		if err != nil {
			t.Fatal(err)
		}
		_, rest, _ := strings.Cut(string(region.HTML), `data-rid="`+id+`"`)
		head, _, _ := strings.Cut(rest, ">")
		return head
	}

	// Priming: an idle snapshot whose mtime is old enough not to be racy, so
	// the cache holds it for the whole validation window. This write is the
	// fixture's own and is not followed by any signal, so it expires the
	// window by hand once, before the signals under test.
	idle := &wslive.Snapshot{SchemaVersion: 1, Adapter: "pi", UpdatedAt: time.Now().UnixMilli(), HeartbeatMs: 30000,
		Lead: wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}}}
	writeSnapshot(t, f.dir, id, idle)
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(snapPath, old, old); err != nil {
		t.Fatal(err)
	}
	f.d.snapshots.Invalidate(f.dir, id)
	if replyOf() != "" || strings.Contains(rowOf(), "data-busy") {
		t.Fatalf("idle priming not read: reply %q row %q", replyOf(), rowOf())
	}
	events := openStream(t, f.base+"/s/"+id+"/events")
	quiet(t, events)

	// 1. A session write that changes the index's record: /nudge re-reads it,
	// the change hook (sessionChanged) wakes the stream and the rail.
	writeSnapshot(t, f.dir, id, replying("first rewritten reply"))
	stale("")
	page := f.src.page(id)
	page.Seq++
	f.src.setPage(id, page)
	nudge()
	waitRegionHolding(t, events, regionConvLive, "first rewritten reply")
	waitFor(t, "the woken rail to show the rewritten snapshot", func() bool {
		return strings.Contains(rowOf(), `data-busy="1"`)
	})
	if got := replyOf(); got != "first rewritten reply" {
		t.Fatalf("after the woken stream the cache read %q", got)
	}

	// 2. /nudge alone: nothing the index fingerprints changed, so no change
	// hook runs, and the handler's own invalidation must carry the rewrite.
	writeSnapshot(t, f.dir, id, replying("second rewritten reply"))
	stale("first rewritten reply")
	nudge()
	if got := replyOf(); got != "second rewritten reply" {
		t.Fatalf("after POST /nudge the cache read %q, want the rewrite", got)
	}

	// 3. The in-process notification a hosted ws-mcp sends instead of HTTP.
	writeSnapshot(t, f.dir, id, replying("third rewritten reply"))
	stale("second rewritten reply")
	if _, handled := f.d.NotifySessionChange(f.dir, NudgeBody{ReplyID: id, Project: f.project}); !handled {
		t.Fatal("NotifySessionChange did not handle the state directory")
	}
	if got := replyOf(); got != "third rewritten reply" {
		t.Fatalf("after NotifySessionChange the cache read %q, want the rewrite", got)
	}

	// 4. The index change hook itself, as the agent runner's write hook and
	// the poll lane reach it.
	writeSnapshot(t, f.dir, id, replying("fourth rewritten reply"))
	stale("third rewritten reply")
	f.d.sessionChanged(id)
	if got := replyOf(); got != "fourth rewritten reply" {
		t.Fatalf("after sessionChanged the cache read %q, want the rewrite", got)
	}
}
