package wsmeta

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return m
}

func TestImportOldDataKeepsOriginalsAndUnknownMembers(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	before := treeState(t, dir)
	s := openTest(t, dir, testOpts())

	if _, err := s.Load(ctx); !errors.Is(err, ErrNotImported) {
		t.Fatalf("Load before import = %v, want ErrNotImported", err)
	}
	stage, err := s.Stage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantSessions := 5 // hostedlead, clionly, worker, futurerec, wrongid
	if runtime.GOOS != "windows" {
		wantSessions++ // worker2 (dangling selection link)
	}
	if stage.Sessions != wantSessions || stage.Restaged == 0 || stage.Reused != 0 || len(stage.Unfenced) != 0 {
		t.Fatalf("stage = %+v", stage)
	}
	res, err := s.Commit(ctx, CommitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.AlreadyComplete || res.Stage.Attempt != 2 {
		t.Fatalf("commit = %+v", res)
	}

	// Discovery and import read legacy files only: every file, bundled or
	// adjacent, keeps its bytes, mode and mtime.
	if after := treeState(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatalf("legacy tree changed:\nbefore %v\nafter  %v", before, after)
	}

	caps := captureMap(t, s)
	for rel, c := range caps {
		if c.State != CapturePresent {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != string(c.Bytes) || c.Digest != contentDigest(raw) {
			t.Fatalf("%s: captured bytes differ from the original", rel)
		}
	}
	wantStatus := map[string]string{
		"sessions/hostedlead/agent.json":      ImportImported,
		"sessions/hostedlead/permission.json": ImportImported,
		"sessions/clionly/agent.json":         ImportAbsent,
		"sessions/clionly/permission.json":    ImportAbsent,
		"sessions/clionly/label.json":         ImportImported,
		"sessions/clionly/hiring.json":        ImportImported,
		"sessions/worker/permission.json":     ImportInvalid,
		"sessions/worker/label.json":          ImportIgnored,
		"sessions/futurerec/agent.json":       ImportFuture,
		"sessions/wrongid/agent.json":         ImportInvalid,
		"sessions/wrongid/permission.json":    ImportInvalid,
		"plan-usage-codex-history.json":       ImportInvalid,
		"plan-usage-agy-history.json":         ImportFuture,
		"pi-probes-v1/fp1-models.json":        ImportImported,
		"pi-probes-v1/fp1-version.json":       ImportFuture,
		"pi-probes-v1/odd.json":               ImportIgnored,
		"tracker-hooks.json":                  ImportRetained,
		"memos/" + memoHash + ".json":         ImportImported,
		"tracker-hooks/store1.json":           ImportImported,
	}
	if runtime.GOOS != "windows" {
		wantStatus["sessions/worker2/permission.json"] = ImportInvalid
	}
	for rel, want := range wantStatus {
		if got := caps[rel].ImportStatus; got != want {
			t.Errorf("%s: import status %q (%s), want %q", rel, got, caps[rel].ImportDetail, want)
		}
	}
	for rel := range caps {
		if filepath.Base(rel) == "registration.json" || filepath.Base(rel) == "holder-h1.json" || filepath.Ext(rel) == ".tmp" || rel == "endpoint.json" {
			t.Errorf("adjacent file %s was captured", rel)
		}
	}
	if _, ok := caps["sessions/Not-A-Reply/agent.json"]; ok {
		t.Error("a non-reply-id directory was discovered")
	}

	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Records: unknown members survive, identity stays with its directory,
	// and each holder cursor belongs to its own record.
	if len(snap.Records) != 2 {
		t.Fatalf("records = %v", snap.Records)
	}
	lead := snap.Records["hostedlead"]
	if m := decode(t, lead.JSON); m["future_member"] == nil || m["holder_seq"].(float64) != 7 || m["ws_session_key"] != "key-1" {
		t.Fatalf("lead record = %s", lead.JSON)
	}
	if !reflect.DeepEqual(lead.HolderCursors, map[string]uint64{"h1": 7}) || !reflect.DeepEqual(snap.Records["worker"].HolderCursors, map[string]uint64{"h2": 3}) {
		t.Fatalf("holder cursors lead %v worker %v", lead.HolderCursors, snap.Records["worker"].HolderCursors)
	}
	if lead.SessionGen == "" || lead.SessionGen != caps["sessions/hostedlead/agent.json"].SessionGen {
		t.Fatalf("lead session generation %q", lead.SessionGen)
	}
	// Permissions: valid keeps provenance presence and unknown members;
	// invalid and unreadable stay apart from absence.
	if p := snap.Permissions["hostedlead"]; p.State != "valid" || p.Level != "workdir-write" || !p.DelegatedSet || p.Delegated || decode(t, p.Body)["future_perm"] != "keep" {
		t.Fatalf("lead permission = %+v", p)
	}
	if p := snap.Permissions["worker"]; p.State != "invalid" || p.Error == "" {
		t.Fatalf("worker permission = %+v", p)
	}
	if p := snap.Permissions["wrongid"]; p.State != "invalid" {
		t.Fatalf("unparseable permission = %+v", p)
	}
	if runtime.GOOS != "windows" {
		if p := snap.Permissions["worker2"]; p.State != "invalid" || p.Error != "dangling symbolic link" {
			t.Fatalf("dangling permission = %+v", p)
		}
	}
	if _, ok := snap.Permissions["clionly"]; ok {
		t.Fatal("absent permission imported as a row")
	}
	// CLI-only labels and hiring import alongside hosted ones; a cleared
	// label has no row.
	if !reflect.DeepEqual(snap.Labels, map[string]string{"hostedlead": "Lead A", "clionly": "CLI lead"}) {
		t.Fatalf("labels = %v", snap.Labels)
	}
	if h := snap.Hiring["hostedlead"]; !h.Enabled || !h.Hired || h.RequestID != "req-1" {
		t.Fatalf("lead hiring = %+v", h)
	}
	if h, ok := snap.Hiring["clionly"]; !ok || h.Enabled {
		t.Fatalf("cli hiring = %+v %v", h, ok)
	}
	if m := decode(t, snap.AckChecks["hostedlead"]); m["later"] != true || m["through"].(float64) != 12 {
		t.Fatalf("ack = %s", snap.AckChecks["hostedlead"])
	}
	// Dashboard stores.
	if !reflect.DeepEqual(snap.Support.Stores, map[string]string{"store-a": "conv1", "store-b": "conv2"}) || decode(t, snap.Support.Last)["harness"] != "pi" || decode(t, snap.Support.Extra)["later"] == nil {
		t.Fatalf("support = %+v", snap.Support)
	}
	l := snap.RailLayout
	if l == nil || !reflect.DeepEqual(l.WorkspaceOrder, []string{"/w1", "/w2"}) || len(l.Workspaces) != 1 || decode(t, l.Workspaces["/w1"])["ws_extra"] != true || decode(t, l.Extra)["doc_extra"] != "x" {
		t.Fatalf("rail layout = %+v", l)
	}
	if u := snap.LatestUsage["plan-usage"]; len(u.Entries) != 3 || u.Entries[0].Key != "claude" || u.Entries[2].Key != "later" {
		t.Fatalf("latest usage = %+v", u)
	}
	if u := snap.LatestUsage["plan-usage-agy"]; len(u.Entries) != 2 || u.Entries[1].Key != "g2" {
		t.Fatalf("agy usage = %+v", u)
	}
	h := snap.UsageHistory["plan-usage-history"]
	if len(h.Samples) != 1 || h.Samples[0].AsOf != 10 || decode(t, h.Samples[0].Body)["ext"] != "keep" || decode(t, h.Extra)["doc_ext"] == nil {
		t.Fatalf("history = %+v", h)
	}
	if _, ok := snap.UsageHistory["plan-usage-codex-history"]; ok {
		t.Fatal("an incomplete history imported samples")
	}
	if p, ok := snap.PiProbes[PiProbeKey{"fp1", "models"}]; !ok || p.ObservedAt == "" || len(snap.PiProbes) != 1 {
		t.Fatalf("pi probes = %v", snap.PiProbes)
	}
	memo := snap.Memos[memoHash]
	if len(memo.Items) != 2 || memo.Items[0].ID != "m1" || decode(t, memo.Items[0].Body)["pin"] != true || decode(t, memo.Extra)["doc"] != "x" {
		t.Fatalf("memo = %+v", memo)
	}
	hook := snap.TrackerHooks[TrackerHookKey{"store1", "n1"}]
	if len(hook.Processed) != 1 || len(hook.Due) != 1 || decode(t, hook.Cursor)["seq"].(float64) != 4 {
		t.Fatalf("hook state = %+v", hook)
	}
}

func TestStageInterruptedMidPassLeavesNothingAndRetries(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	opts := testOpts()
	fail := true
	opts.Fault = func(stage string) error {
		if fail && stage == "stage-session" {
			return errors.New("killed")
		}
		return nil
	}
	s := openTest(t, dir, opts)
	if _, err := s.Stage(ctx); err == nil {
		t.Fatal("interrupted stage succeeded")
	}
	if caps := captureMap(t, s); len(caps) != 0 {
		t.Fatalf("interrupted stage left %d captures", len(caps))
	}
	fail = false
	if _, err := s.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, CommitOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestCommitInterruptedRetriesFromLiveUnfencedFiles(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	opts := testOpts()
	failAt := ""
	opts.Fault = func(stage string) error {
		if stage == failAt {
			return errors.New("killed at " + stage)
		}
		return nil
	}
	s := openTest(t, dir, opts)
	if _, err := s.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	// Between attempts the legacy files are still the live authority: an
	// old writer changes a label, a session is pruned and another appears.
	put(t, dir, "sessions/hostedlead/label.json", `{"label":"Renamed"}`)
	if err := os.RemoveAll(filepath.Join(dir, "sessions", "clionly")); err != nil {
		t.Fatal(err)
	}
	put(t, dir, "sessions/latecomer/label.json", `{"label":"Late"}`)

	for _, stage := range []string{"commit-staged", "commit-imported", "before-commit"} {
		failAt = stage
		if _, err := s.Commit(ctx, CommitOptions{}); err == nil {
			t.Fatalf("commit interrupted at %s succeeded", stage)
		}
		if done, err := s.ImportComplete(ctx); err != nil || done {
			t.Fatalf("after interruption at %s: complete=%v err=%v", stage, done, err)
		}
		if _, err := s.Load(ctx); !errors.Is(err, ErrNotImported) {
			t.Fatalf("Load after interruption at %s = %v", stage, err)
		}
	}
	// The interrupted commits rolled back their passes: the journal still
	// holds the first pass, including the since-pruned session.
	if caps := captureMap(t, s); caps["sessions/clionly/label.json"].State != CapturePresent || caps["sessions/hostedlead/label.json"].Attempt != 1 {
		t.Fatalf("rolled-back commit changed the journal: %+v", caps["sessions/hostedlead/label.json"])
	}

	failAt = "commit-done" // after the commit point: the import stands
	if _, err := s.Commit(ctx, CommitOptions{}); err == nil {
		t.Fatal("fault after commit was not reported")
	}
	done, err := s.ImportComplete(ctx)
	if err != nil || !done {
		t.Fatalf("complete=%v err=%v after commit point", done, err)
	}
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"hostedlead": "Renamed", "latecomer": "Late"}
	if !reflect.DeepEqual(snap.Labels, want) {
		t.Fatalf("labels = %v, want %v", snap.Labels, want)
	}
	if _, ok := snap.Hiring["clionly"]; ok {
		t.Fatal("a pruned session was imported")
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", "clionly")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pruned session directory was recreated: %v", err)
	}
	failAt = ""
	res, err := s.Commit(ctx, CommitOptions{})
	if err != nil || !res.AlreadyComplete {
		t.Fatalf("repeat commit = %+v, %v", res, err)
	}
}

func TestRecreatedSessionDiscardsItsJournal(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	s := openTest(t, dir, testOpts())
	if _, err := s.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "sessions", "clionly")
	// Recreate under the same reply-id while the old directory still exists,
	// so the new incarnation cannot reuse its inode.
	if err := os.Rename(old, old+"-gone"); err != nil {
		t.Fatal(err)
	}
	put(t, dir, "sessions/clionly/label.json", `{"label":"Reborn"}`)
	if err := os.RemoveAll(old + "-gone"); err != nil {
		t.Fatal(err)
	}
	res, err := s.Stage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Discarded != 1 {
		t.Fatalf("discarded = %d, want 1", res.Discarded)
	}
	caps := captureMap(t, s)
	if string(caps["sessions/clionly/label.json"].Bytes) != `{"label":"Reborn"}` || caps["sessions/clionly/hiring.json"].State != CaptureAbsent {
		t.Fatalf("recreated session captures: %+v", caps["sessions/clionly/label.json"])
	}
}

func TestFenceLandedBeforeJournalMarkKeepsOriginal(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	s := openTest(t, dir, testOpts())
	if _, err := s.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	const perm = "sessions/hostedlead/permission.json"
	const absentPerm = "sessions/clionly/permission.json"
	fenced := []byte(`{"permission_level":"!cutover-fenced"}`)
	for _, rel := range []string{perm, absentPerm} {
		if err := s.PrepareFence(ctx, rel, fenced, false); err != nil {
			t.Fatal(err)
		}
		// The replacement lands; the process dies before MarkFenceLanded.
		put(t, dir, rel, string(fenced))
	}
	// The removal fence of the legacy hooks file lands and is marked.
	if err := s.PrepareFence(ctx, "tracker-hooks.json", nil, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "tracker-hooks.json")); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkFenceLanded(ctx, "tracker-hooks.json"); err != nil {
		t.Fatal(err)
	}
	res, err := s.Stage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reused != 3 || len(res.Unfenced) != 0 {
		t.Fatalf("retry stage = %+v", res)
	}
	caps := captureMap(t, s)
	if c := caps[perm]; c.State != CapturePresent || string(c.Bytes) != `{"permission_level":"workdir-write","delegated_authority":false,"future_perm":"keep"}` || c.FenceState != FenceIntent {
		t.Fatalf("fenced capture = %+v", c)
	}
	if c := caps[absentPerm]; c.State != CaptureAbsent {
		t.Fatalf("absent-before-fence capture = %+v", c)
	}
	if _, err := s.Commit(ctx, CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p := snap.Permissions["hostedlead"]; p.State != "valid" || p.Level != "workdir-write" {
		t.Fatalf("imported permission = %+v (want the staged original)", p)
	}
	if _, ok := snap.Permissions["clionly"]; ok {
		t.Fatal("a fence on an absent selection imported a row")
	}
	if c := captureMap(t, s)["tracker-hooks.json"]; c.State != CapturePresent || c.ImportStatus != ImportRetained {
		t.Fatalf("retained hooks config = %+v", c)
	}
	if err := s.PrepareFence(ctx, perm, fenced, false); !errors.Is(err, ErrImportComplete) {
		t.Fatalf("fence after import = %v", err)
	}
}

func TestForeignWriteOverFenceReturnsToLiveAuthority(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	s := openTest(t, dir, testOpts())
	if _, err := s.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	const perm = "sessions/hostedlead/permission.json"
	fenced := []byte(`{"permission_level":"!cutover-fenced"}`)
	if err := s.PrepareFence(ctx, perm, fenced, false); err != nil {
		t.Fatal(err)
	}
	put(t, dir, perm, string(fenced))
	if err := s.MarkFenceLanded(ctx, perm); err != nil {
		t.Fatal(err)
	}
	// The old daemon lowers the selection over the fence.
	lowered := `{"permission_level":"read-only"}`
	put(t, dir, perm, lowered)

	// A commit refuses while a fenced file is unfenced, and changes nothing.
	if _, err := s.Commit(ctx, CommitOptions{}); !errors.Is(err, ErrFenceInvalidated) {
		t.Fatalf("commit over an invalidated fence = %v", err)
	}
	if c := captureMap(t, s)[perm]; c.FenceState != FenceLanded {
		t.Fatalf("refused commit changed the journal: %+v", c)
	}
	// A retry re-stages the live file's bytes.
	res, err := s.Stage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Unfenced, []string{perm}) {
		t.Fatalf("unfenced = %v", res.Unfenced)
	}
	if c := captureMap(t, s)[perm]; string(c.Bytes) != lowered || c.FenceState != FenceNone {
		t.Fatalf("re-staged capture = %+v", c)
	}
	var seen []Capture
	if _, err := s.Commit(ctx, CommitOptions{BeforeMarker: func(c []Capture) error { seen = c; return nil }}); err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 {
		t.Fatal("BeforeMarker not called")
	}
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p := snap.Permissions["hostedlead"]; p.Level != "read-only" {
		t.Fatalf("permission = %+v, want the lowered selection", p)
	}
}

func TestBeforeMarkerErrorRollsBack(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, legacyFixture(t), testOpts())
	boom := errors.New("fence check failed")
	if _, err := s.Commit(ctx, CommitOptions{BeforeMarker: func([]Capture) error { return boom }}); !errors.Is(err, boom) {
		t.Fatalf("commit = %v", err)
	}
	if done, _ := s.ImportComplete(ctx); done {
		t.Fatal("marker committed despite the check")
	}
	if caps := captureMap(t, s); len(caps) != 0 {
		t.Fatalf("rolled-back commit left %d captures", len(caps))
	}
}

func TestRestartRestoresFromSQLiteAndIgnoresLegacyEdits(t *testing.T) {
	ctx := context.Background()
	dir := legacyFixture(t)
	s, err := Open(dir, testOpts())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, CommitOptions{}); err != nil {
		t.Fatal(err)
	}
	first, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	journal := captureMap(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// An old binary edits, deletes and adds bundled files after cutover.
	put(t, dir, "sessions/hostedlead/label.json", `{"label":"Old edit"}`)
	if err := os.Remove(filepath.Join(dir, "support.json")); err != nil {
		t.Fatal(err)
	}
	put(t, dir, "sessions/newold/agent.json", `{"schema_version":1,"reply_id":"newold","root":"/r","ws_session_key":"k"}`)

	s2 := openTest(t, dir, testOpts())
	second, err := s2.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("restart did not restore the committed state")
	}
	if _, err := s2.Stage(ctx); !errors.Is(err, ErrImportComplete) {
		t.Fatalf("stage after import = %v", err)
	}
	if res, err := s2.Commit(ctx, CommitOptions{}); err != nil || !res.AlreadyComplete {
		t.Fatalf("commit after import = %+v, %v", res, err)
	}
	if !reflect.DeepEqual(journal, captureMap(t, s2)) {
		t.Fatal("the capture journal changed after the import")
	}
}

func TestEmptyDashboardImportsNothing(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "dashboard"), testOpts())
	res, err := s.Commit(ctx, CommitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status[ImportImported] != 0 || res.Stage.Sessions != 0 {
		t.Fatalf("result = %+v", res)
	}
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Records) != 0 || snap.RailLayout != nil {
		t.Fatalf("snapshot = %+v", snap)
	}
}
