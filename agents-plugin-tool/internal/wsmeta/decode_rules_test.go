package wsmeta

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func commitDir(t *testing.T, dir string) (*Store, *Snapshot) {
	t.Helper()
	ctx := context.Background()
	s := openTest(t, dir, testOpts())
	if _, err := s.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return s, snap
}

// A record or history the legacy reader rejects stays out of the live
// store, keeping its bytes in the journal with invalid status.
func TestLegacyValidatorsGateRecordsAndHistories(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dashboard")
	put(t, dir, "sessions/typed/agent.json", `{"schema_version":1,"reply_id":"typed","root":"/r","ws_session_key":"k","holder_id":"h","holder_seq":4,"closed_at":123}`)
	put(t, dir, "sessions/good/agent.json", `{"schema_version":1,"reply_id":"good","root":"/r","ws_session_key":"k"}`)
	put(t, dir, "plan-usage-codex-history.json", `{"schema_version":1,"samples":[{"harness":"codex","kind":"five_hour","source":"s","resetsAt":0,"asOf":10,"percent":5},{"harness":"codex","kind":"five_hour","source":"s","resetsAt":0,"asOf":-1,"percent":5}]}`)

	// A commit without the owners' validators refuses and changes nothing.
	ctx := context.Background()
	s := openTest(t, dir, testOpts())
	if _, err := s.Commit(ctx, CommitOptions{}); !errors.Is(err, ErrValidatorMissing) {
		t.Fatalf("commit without validators = %v", err)
	}
	if done, _ := s.ImportComplete(ctx); done {
		t.Fatal("import completed without validators")
	}
	if _, err := s.Commit(ctx, testCommit()); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Records["typed"]; ok {
		t.Fatal("a record with a mistyped known member became live")
	}
	if _, ok := snap.Records["good"]; !ok {
		t.Fatal("valid record not imported")
	}
	if _, ok := snap.UsageHistory["plan-usage-codex-history"]; ok {
		t.Fatal("a history with one invalid sample was published")
	}
	caps := captureMap(t, s)
	for _, p := range []string{"sessions/typed/agent.json", "plan-usage-codex-history.json"} {
		if c := caps[p]; c.ImportStatus != ImportInvalid || len(c.Bytes) == 0 {
			t.Fatalf("%s: %+v, want invalid with retained bytes", p, c)
		}
	}
	// The rejected record left no holder cursor behind.
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_holder_cursors WHERE reply_id = 'typed'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("cursor rows for a rejected record: %d %v", n, err)
	}
}

func TestNullDelegationKeepsProvenance(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dashboard")
	put(t, dir, "sessions/a/permission.json", `{"permission_level":"full","delegated_authority":null}`)
	_, snap := commitDir(t, dir)
	if p := snap.Permissions["a"]; p.State != "valid" || !p.DelegatedSet || p.Delegated || p.Level != "full" {
		t.Fatalf("permission = %+v, want valid with provenance present and false", p)
	}
}

func TestLabelsFollowTheLegacyLabelRules(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dashboard")
	put(t, dir, "sessions/trimmed/label.json", `{"label":"  Lead A  "}`)
	put(t, dir, "sessions/blank/label.json", `{"label":"   "}`)
	put(t, dir, "sessions/control/label.json", `{"label":"a\u0007b"}`)
	put(t, dir, "sessions/long/label.json", `{"label":"`+strings.Repeat("x", maxLabelRunes+1)+`"}`)
	s, snap := commitDir(t, dir)
	if want := map[string]string{"trimmed": "Lead A"}; !reflect.DeepEqual(snap.Labels, want) {
		t.Fatalf("labels = %v, want %v", snap.Labels, want)
	}
	if c := captureMap(t, s)["sessions/control/label.json"]; !strings.Contains(string(c.Bytes), `\u0007`) {
		t.Fatalf("original bytes not kept: %q", c.Bytes)
	}
}

func TestMemoNullItemIsKeptAndSkipped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dashboard")
	put(t, dir, "memos/"+memoHash+".json", `{"schema_version":1,"memos":[null,{"id":"m1","text":"one"}]}`)
	_, snap := commitDir(t, dir)
	items := snap.Memos[memoHash].Items
	if len(items) != 2 || items[0].ID != "" || string(items[0].Body) != "null" || items[1].ID != "m1" {
		t.Fatalf("memo items = %+v", items)
	}
}

func TestHolderChangeResetsTheSequence(t *testing.T) {
	ctx := context.Background()
	s, _ := importedStore(t)
	// The runner's attach sets only the new holder identity.
	if err := s.PatchRecord(ctx, "hostedlead", map[string]json.RawMessage{"holder_id": json.RawMessage(`"h9"`)}); err != nil {
		t.Fatal(err)
	}
	m, rec := recordMembers(t, s, "hostedlead")
	if m["holder_id"] != "h9" || m["holder_seq"] != nil || len(rec.HolderCursors) != 0 {
		t.Fatalf("after a holder change: seq %v cursors %v", m["holder_seq"], rec.HolderCursors)
	}
	// The same holder patched again keeps its sequence.
	if err := s.SetHolderCursor(ctx, "hostedlead", "h9", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.PatchRecord(ctx, "hostedlead", map[string]json.RawMessage{"holder_id": json.RawMessage(`"h9"`), "turn_open": json.RawMessage(`true`)}); err != nil {
		t.Fatal(err)
	}
	if m, _ := recordMembers(t, s, "hostedlead"); m["holder_seq"].(float64) != 3 {
		t.Fatalf("same-holder patch reset holder_seq: %v", m["holder_seq"])
	}
	// Switching back to an earlier holder never revives its old cursor.
	if err := s.PatchRecord(ctx, "hostedlead", map[string]json.RawMessage{"holder_id": json.RawMessage(`"h1"`)}); err != nil {
		t.Fatal(err)
	}
	if m, _ := recordMembers(t, s, "hostedlead"); m["holder_seq"] != nil {
		t.Fatalf("returning holder got seq %v", m["holder_seq"])
	}
}

func TestHolderCursorRejectsUnorderedValues(t *testing.T) {
	ctx := context.Background()
	s, _ := importedStore(t)
	if err := s.SetHolderCursor(ctx, "hostedlead", "h1", math.MaxInt64); err != nil {
		t.Fatal(err)
	}
	if err := s.SetHolderCursor(ctx, "hostedlead", "h1", math.MaxInt64+1); err == nil {
		t.Fatal("a sequence above MaxInt64 was accepted")
	}
	if _, rec := recordMembers(t, s, "hostedlead"); rec.HolderCursors["h1"] != math.MaxInt64 {
		t.Fatalf("cursor = %d", rec.HolderCursors["h1"])
	}
}
