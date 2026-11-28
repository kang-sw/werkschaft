package wsmeta_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/kang-sw/devenv/internal/wsmeta"
)

func openImportedMeta(t *testing.T) *wsmeta.Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dashboard")
	s, err := wsmeta.Open(dir, wsmeta.Options{CheckpointIdle: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Cutover(context.Background(), wsmeta.CutoverOptions{}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLiveCRUDRefusesBeforeImport(t *testing.T) {
	ctx := context.Background()
	s, err := wsmeta.Open(filepath.Join(t.TempDir(), "dashboard"), wsmeta.Options{CheckpointIdle: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.SetLabel(ctx, "abc123", "hello"); !errors.Is(err, wsmeta.ErrNotImported) {
		t.Fatalf("SetLabel before import = %v, want ErrNotImported", err)
	}
	if err := s.SetSupportMapping(ctx, "s1", "abc123"); !errors.Is(err, wsmeta.ErrNotImported) {
		t.Fatalf("SetSupportMapping before import = %v, want ErrNotImported", err)
	}
	if err := s.DeleteUsageLatestEntry(ctx, wsmeta.UsageSourcePlan, "claude:work"); !errors.Is(err, wsmeta.ErrNotImported) {
		t.Fatalf("DeleteUsageLatestEntry before import = %v, want ErrNotImported", err)
	}
}

func TestLivePermissionsSupportLabelsAckHiringAndDeleteSession(t *testing.T) {
	ctx := context.Background()
	s := openImportedMeta(t)

	// Record + Permission + Label + AckCheck + Hiring.
	rec := []byte(`{"schema_version":1,"reply_id":"sess1","ws_session_key":"k1","root":"/r","extra":true}`)
	if err := s.PutRecord(ctx, "sess1", "gen1", rec); err != nil {
		t.Fatal(err)
	}
	if err := s.PutPermission(ctx, "sess1", wsmeta.Permission{
		State:        "valid",
		Level:        "workdir-write",
		DelegatedSet: true,
		Delegated:    true,
		Body:         json.RawMessage(`{"permission_level":"workdir-write","delegated_authority":true,"custom":7}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutPermission(ctx, "sess2", wsmeta.Permission{
		State: "invalid",
		Body:  json.RawMessage(`{"permission_level":"bogus"}`),
		Error: "invalid permission_level",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLabel(ctx, "sess1", "  My Lead  "); err != nil {
		t.Fatal(err)
	}
	if err := s.PutAckCheck(ctx, "sess1", []byte(`{"through":100,"interruptedAt":50}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutHiring(ctx, "sess1", wsmeta.Hiring{
		Enabled:   true,
		Hired:     false,
		RequestID: "req-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkHired(ctx, "sess3", "req-3", "Hired Lead"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSupportLast(ctx, []byte(`{"harness":"codex","model":"gpt-5","effort":"high"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSupportMapping(ctx, "store-a", "sess1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSupportMapping(ctx, "store-b", "sess2"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSupportMapping(ctx, "store-b"); err != nil {
		t.Fatal(err)
	}

	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p := snap.Permissions["sess1"]; p.State != "valid" || p.Level != "workdir-write" || !p.DelegatedSet || !p.Delegated || string(p.Body) != `{"permission_level":"workdir-write","delegated_authority":true,"custom":7}` {
		t.Fatalf("unexpected sess1 permission: %+v", p)
	}
	if p := snap.Permissions["sess2"]; p.State != "invalid" || p.Error != "invalid permission_level" {
		t.Fatalf("unexpected sess2 permission: %+v", p)
	}
	if snap.Labels["sess1"] != "My Lead" || snap.Labels["sess3"] != "Hired Lead" {
		t.Fatalf("unexpected labels: %+v", snap.Labels)
	}
	if string(snap.AckChecks["sess1"]) != `{"through":100,"interruptedAt":50}` {
		t.Fatalf("unexpected ack check: %s", snap.AckChecks["sess1"])
	}
	if h := snap.Hiring["sess1"]; !h.Enabled || h.Hired || h.RequestID != "req-1" {
		t.Fatalf("unexpected sess1 hiring: %+v", h)
	}
	if h := snap.Hiring["sess3"]; h.Enabled || !h.Hired || h.RequestID != "req-3" {
		t.Fatalf("unexpected sess3 hiring: %+v", h)
	}
	if snap.Support.Stores["store-a"] != "sess1" || len(snap.Support.Stores) != 1 {
		t.Fatalf("unexpected support stores: %+v", snap.Support.Stores)
	}
	if string(snap.Support.Last) != `{"harness":"codex","model":"gpt-5","effort":"high"}` {
		t.Fatalf("unexpected support last: %s", snap.Support.Last)
	}

	// Clearing label deletes row.
	if err := s.SetLabel(ctx, "sess1", "   "); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Labels["sess1"]; ok {
		t.Fatalf("expected cleared label to have no row, got %+v", snap.Labels)
	}

	// DeleteSession clears all session-scoped rows for sess1 only.
	if err := s.DeleteSession(ctx, "sess1"); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Records["sess1"]; ok {
		t.Fatalf("sess1 record still present")
	}
	if _, ok := snap.Permissions["sess1"]; ok {
		t.Fatalf("sess1 permission still present")
	}
	if _, ok := snap.AckChecks["sess1"]; ok {
		t.Fatalf("sess1 ack check still present")
	}
	if _, ok := snap.Hiring["sess1"]; ok {
		t.Fatalf("sess1 hiring still present")
	}
	if _, ok := snap.Permissions["sess2"]; !ok {
		t.Fatalf("sess2 permission was unexpectedly deleted")
	}
}

func TestLiveRailLayoutUsageMemosAndTrackerHooks(t *testing.T) {
	ctx := context.Background()
	s := openImportedMeta(t)

	// Rail layout: workspace-scoped update preserves untouched workspaces & extra.
	if err := s.UpdateRailLayout(ctx, wsmeta.RailLayoutPatch{
		OrderChanged:   true,
		WorkspaceOrder: []string{"/ws/a", "/ws/b"},
		ExtraChanged:   true,
		Extra:          json.RawMessage(`{"future_flag":1}`),
		PutWorkspaces: map[string]json.RawMessage{
			"/ws/a": json.RawMessage(`{"order":["s1"],"children":{},"stars":[]}`),
			"/ws/b": json.RawMessage(`{"order":["s2"],"children":{},"stars":["s2"]}`),
		},
	}); err != nil {
		t.Fatal(err)
	}
	// Update only /ws/a; /ws/b and order/extra remain untouched.
	if err := s.UpdateRailLayout(ctx, wsmeta.RailLayoutPatch{
		PutWorkspaces: map[string]json.RawMessage{
			"/ws/a": json.RawMessage(`{"order":["s1","s3"],"children":{},"stars":["s1"]}`),
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Latest usage: keyed entry updates do not overwrite sibling entries.
	if err := s.PutUsageLatestEntry(ctx, wsmeta.UsageSourcePlan, "claude", []byte(`{"harness":"claude","asOf":100,"windows":[]}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutUsageLatestEntry(ctx, wsmeta.UsageSourcePlan, "gpt", []byte(`{"harness":"codex","asOf":200,"windows":[]}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutUsageLatestEntry(ctx, wsmeta.UsageSourcePlan, "claude", []byte(`{"harness":"claude","asOf":300,"windows":[]}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutUsageLatestEntry(ctx, wsmeta.UsageSourcePlan, "claude:work", []byte(`{"harness":"claude","asOf":250,"windows":[]}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUsageLatestEntry(ctx, wsmeta.UsageSourcePlan, "claude:work"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUsageLatestEntries(ctx, wsmeta.UsageSourceAgy, []wsmeta.UsageEntry{
		{Key: "gemini", Body: json.RawMessage(`{"harness":"agy","group":"gemini","windows":[]}`)},
	}); err != nil {
		t.Fatal(err)
	}

	// Usage history: incremental add, update, delete.
	seqs, err := s.MutateUsageHistory(ctx, wsmeta.UsageHistoryDefault, []wsmeta.UsageSample{
		{Harness: "claude", Kind: "five_hour", Source: "Claude push", ResetsAt: 1000, AsOf: 10, Percent: 12, Body: json.RawMessage(`{"harness":"claude","kind":"five_hour","source":"Claude push","resetsAt":1000,"asOf":10,"percent":12,"ext":1}`)},
		{Harness: "claude", Kind: "five_hour", Source: "Claude push", ResetsAt: 1000, AsOf: 20, Percent: 25, Body: json.RawMessage(`{"harness":"claude","kind":"five_hour","source":"Claude push","resetsAt":1000,"asOf":20,"percent":25}`)},
	}, nil, nil)
	if err != nil || len(seqs) != 2 {
		t.Fatalf("MutateUsageHistory add = %v, %v", seqs, err)
	}
	if _, err := s.MutateUsageHistory(ctx, wsmeta.UsageHistoryDefault, nil, []wsmeta.UsageSample{
		{Seq: seqs[0], Harness: "claude", Kind: "five_hour", Source: "Claude push", ResetsAt: 1000, AsOf: 10, Percent: 15, Body: json.RawMessage(`{"harness":"claude","kind":"five_hour","source":"Claude push","resetsAt":1000,"asOf":10,"percent":15,"ext":1}`)},
	}, []int64{seqs[1]}); err != nil {
		t.Fatal(err)
	}

	// Memos: prepend order + in-place edit + delete without touching siblings.
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("/ws/a")))
	p1, err := s.AddMemo(ctx, hash, "/ws/a", wsmeta.Memo{ID: "m1", Body: json.RawMessage(`{"id":"m1","text":"first","extra":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.AddMemo(ctx, hash, "/ws/a", wsmeta.Memo{ID: "m2", Body: json.RawMessage(`{"id":"m2","text":"second"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if p2 >= p1 {
		t.Fatalf("expected prepend position %d < %d", p2, p1)
	}
	if err := s.UpdateMemo(ctx, hash, p1, wsmeta.Memo{ID: "m1", Body: json.RawMessage(`{"id":"m1","text":"first-edited","extra":1}`)}); err != nil {
		t.Fatal(err)
	}

	// Tracker hooks: nonce-scoped delta + nonce reset cascades old nonce.
	k1 := wsmeta.TrackerHookKey{StoreID: "st1", Nonce: "n1"}
	if err := s.UpdateTrackerHook(ctx, k1, wsmeta.TrackerHookDelta{
		CursorChanged:   true,
		Cursor:          json.RawMessage(`{"seq":5}`),
		UpsertProcessed: map[string]json.RawMessage{"e1": json.RawMessage(`{"at":"2026-11-26T00:00:00Z"}`)},
		UpsertDue:       map[string]json.RawMessage{"i1:2026-11-27": json.RawMessage(`"2026-11-26T00:00:00Z"`)},
	}); err != nil {
		t.Fatal(err)
	}
	k2 := wsmeta.TrackerHookKey{StoreID: "st1", Nonce: "n2"}
	if err := s.UpdateTrackerHook(ctx, k2, wsmeta.TrackerHookDelta{
		ResetNonce:    true,
		CursorChanged: true,
		Cursor:        json.RawMessage(`{"seq":1}`),
	}); err != nil {
		t.Fatal(err)
	}

	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snap.RailLayout == nil || len(snap.RailLayout.WorkspaceOrder) != 2 || string(snap.RailLayout.Extra) != `{"future_flag":1}` {
		t.Fatalf("unexpected rail layout: %+v", snap.RailLayout)
	}
	if string(snap.RailLayout.Workspaces["/ws/b"]) != `{"order":["s2"],"children":{},"stars":["s2"]}` {
		t.Fatalf("/ws/b unexpectedly modified: %s", snap.RailLayout.Workspaces["/ws/b"])
	}
	if got := snap.LatestUsage[wsmeta.UsageSourcePlan].Entries; len(got) != 2 || got[0].Key != "claude" || got[1].Key != "gpt" {
		t.Fatalf("unexpected plan-usage entries: %+v", got)
	}
	if got := snap.UsageHistory[wsmeta.UsageHistoryDefault].Samples; len(got) != 1 || got[0].Percent != 15 || got[0].Seq != seqs[0] {
		t.Fatalf("unexpected usage history: %+v", got)
	}
	if got := snap.Memos[hash].Items; len(got) != 2 || got[0].ID != "m2" || got[1].ID != "m1" {
		t.Fatalf("unexpected memo order: %+v", got)
	}
	if _, oldExists := snap.TrackerHooks[k1]; oldExists {
		t.Fatalf("old nonce k1 was not dropped on ResetNonce")
	}
	if got, ok := snap.TrackerHooks[k2]; !ok || string(got.Cursor) != `{"seq":1}` {
		t.Fatalf("unexpected tracker hook k2: %+v", got)
	}

	// Delete memo m2 and verify m1 remains.
	if err := s.DeleteMemo(ctx, hash, p2); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.Memos[hash].Items; len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("unexpected memos after delete: %+v", got)
	}
}

func TestDiffJSONObjects(t *testing.T) {
	before := []byte(`{"a":1,"b":"keep","c":"drop","d":{"x":1}}`)
	after := []byte(`{"a":2,"b":"keep","d":{"x":1},"e":true}`)
	patch, err := wsmeta.DiffJSONObjects(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(patch) != 3 || string(patch["a"]) != "2" || string(patch["c"]) != "null" || string(patch["e"]) != "true" {
		t.Fatalf("unexpected diff patch: %+v", patch)
	}
}
