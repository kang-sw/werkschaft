package wsmeta_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmeta"
)

func allOwnerValidators() map[string]wsmeta.LegacyValidator {
	return map[string]wsmeta.LegacyValidator{
		wsmeta.StoreAgentRecord:       wsagent.ValidateLegacyRecord,
		wsmeta.StoreUsageHistory:      wsdashboard.ValidateLegacyUsageHistory,
		wsmeta.StoreUsageHistoryCodex: wsdashboard.ValidateLegacyUsageHistory,
		wsmeta.StoreUsageHistoryAgy:   wsdashboard.ValidateLegacyUsageHistory,
	}
}

func writeLegacyFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCutoverAppliesAllFourFencesAndImportsOriginals(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "dashboard")
	writeLegacyFiles(t, dir, map[string]string{
		// Lead with explicit permission + hiring enabled.
		"sessions/lead1/agent.json":        `{"schema_version":1,"reply_id":"lead1","profile":"lead","backend":"codex","root":"/r","ws_session_key":"k-lead","lead":true,"created_at":"2026-11-20T00:00:00Z"}`,
		"sessions/lead1/permission.json":   `{"permission_level":"full","delegated_authority":false,"keep_me":"yes"}`,
		"sessions/lead1/hiring.json":       `{"enabled":true,"hired":true,"request_id":"req-9","extra":"ok"}`,
		"sessions/lead1/registration.json": `{"reply_id":"lead1"}`,
		// Open support (parentless non-lead) with NO permission.json.
		"sessions/supp1/agent.json":        `{"schema_version":1,"reply_id":"supp1","profile":"support-codex","backend":"codex","root":"/r","ws_session_key":"k-supp","created_at":"2026-11-21T00:00:00Z","unknown_f":9}`,
		"sessions/supp1/registration.json": `{"reply_id":"supp1"}`,
		// Open child (non-lead with parent).
		"sessions/child1/agent.json":        `{"schema_version":1,"reply_id":"child1","profile":"worker-codex","backend":"codex","root":"/r","ws_session_key":"k-child","parent":{"kind":"lead","reply_id":"lead1"},"created_at":"2026-11-22T00:00:00Z"}`,
		"sessions/child1/permission.json":   `{"permission_level":"read-only","delegated_authority":true}`,
		"sessions/child1/registration.json": `{"reply_id":"child1"}`,
		// Already-closed support record: should NOT be modified by agent fence.
		"sessions/closed1/agent.json":        `{"schema_version":1,"reply_id":"closed1","profile":"support-codex","backend":"codex","root":"/r","ws_session_key":"k-closed","closed_at":"2026-11-19T00:00:00Z"}`,
		"sessions/closed1/registration.json": `{"reply_id":"closed1"}`,
		// Legacy tracker hooks config.
		"tracker-hooks.json": `{"v":1,"hooks":[{"id":"h1"}]}`,
		// Invalid usage history to verify ImportIssues reporting.
		"plan-usage-history.json": `{"schema_version":2,"samples":[]}`,
	})

	s, err := wsmeta.Open(dir, wsmeta.Options{CheckpointIdle: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	res, err := s.Cutover(ctx, wsmeta.CutoverOptions{Validate: allOwnerValidators()})
	if err != nil {
		t.Fatalf("Cutover failed: %v", err)
	}
	if res.AlreadyComplete {
		t.Fatalf("expected fresh cutover, got AlreadyComplete")
	}

	// Second call is an immediate AlreadyComplete no-op.
	res2, err := s.Cutover(ctx, wsmeta.CutoverOptions{Validate: allOwnerValidators()})
	if err != nil || !res2.AlreadyComplete {
		t.Fatalf("second Cutover = (%+v, %v), want AlreadyComplete", res2, err)
	}

	// 1. Legacy tracker-hooks.json removed and published at tracker-hooks-v2.json.
	if _, err := os.Stat(filepath.Join(dir, wsmeta.LegacyTrackerHooksConfigName)); !os.IsNotExist(err) {
		t.Fatalf("expected legacy tracker-hooks.json absent, got err=%v", err)
	}
	v2Raw, err := os.ReadFile(filepath.Join(dir, wsmeta.TrackerHooksWitnessName))
	if err != nil || string(v2Raw) != `{"v":1,"hooks":[{"id":"h1"}]}` {
		t.Fatalf("tracker-hooks-v2.json = (%q, %v)", v2Raw, err)
	}

	// 2. Every session (including supp1 where permission.json was absent) has a present invalid permission.json.
	for _, rid := range []string{"lead1", "supp1", "child1", "closed1"} {
		raw, err := os.ReadFile(filepath.Join(dir, "sessions", rid, "permission.json"))
		if err != nil {
			t.Fatalf("%s permission.json read failed: %v", rid, err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil || m["permission_level"] != wsmeta.PermissionCutoverFencedLevel {
			t.Fatalf("%s permission.json not fenced: %s", rid, raw)
		}
	}

	// 3. Hiring fence cleared enabled while preserving hired, request_id, and unknown fields.
	hiringRaw, err := os.ReadFile(filepath.Join(dir, "sessions", "lead1", "hiring.json"))
	if err != nil {
		t.Fatal(err)
	}
	var hm map[string]any
	if err := json.Unmarshal(hiringRaw, &hm); err != nil {
		t.Fatal(err)
	}
	if hm["enabled"] != false || hm["hired"] != true || hm["request_id"] != "req-9" || hm["extra"] != "ok" {
		t.Fatalf("unexpected fenced hiring.json: %s", hiringRaw)
	}

	// 4. Agent record fences:
	// - lead1 untouched (no closed_at, original profile)
	leadDisk, err := wsagent.ReadRecord(dir, "lead1")
	if err != nil || leadDisk.ClosedAt != "" || leadDisk.Profile != "lead" {
		t.Fatalf("lead1 disk record unexpectedly fenced: %+v, %v", leadDisk, err)
	}
	// - supp1 has nonempty closed_at, preserving key, root, and unknown field
	suppDiskRaw, err := os.ReadFile(filepath.Join(dir, "sessions", "supp1", "agent.json"))
	if err != nil {
		t.Fatal(err)
	}
	var suppMap map[string]any
	if err := json.Unmarshal(suppDiskRaw, &suppMap); err != nil {
		t.Fatal(err)
	}
	if suppMap["closed_at"] != "2026-11-21T00:00:00Z" || suppMap["ws_session_key"] != "k-supp" || suppMap["unknown_f"] != float64(9) {
		t.Fatalf("unexpected fenced supp1 disk record: %s", suppDiskRaw)
	}
	// - child1 keeps closed_at empty, profile="!cutover-fenced", cutover_profile="worker-codex"
	childDiskRaw, err := os.ReadFile(filepath.Join(dir, "sessions", "child1", "agent.json"))
	if err != nil {
		t.Fatal(err)
	}
	var childMap map[string]any
	if err := json.Unmarshal(childDiskRaw, &childMap); err != nil {
		t.Fatal(err)
	}
	if childMap["profile"] != wsmeta.ChildCutoverFencedProfile || childMap["cutover_profile"] != "worker-codex" || childMap["closed_at"] != nil {
		t.Fatalf("unexpected fenced child1 disk record: %s", childDiskRaw)
	}

	// 5. SQLite snapshot retains all ORIGINAL unfenced values + ImportIssues.
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p := snap.Permissions["lead1"]; p.State != "valid" || p.Level != "full" || !p.DelegatedSet || p.Delegated {
		t.Fatalf("SQLite lead1 permission lost original: %+v", p)
	}
	if _, exists := snap.Permissions["supp1"]; exists {
		t.Fatalf("SQLite supp1 should have no permission row (was absent)")
	}
	if h := snap.Hiring["lead1"]; !h.Enabled || !h.Hired || h.RequestID != "req-9" {
		t.Fatalf("SQLite lead1 hiring lost original: %+v", h)
	}
	var suppSQLite wsagent.Record
	if err := json.Unmarshal(snap.Records["supp1"].JSON, &suppSQLite); err != nil || suppSQLite.ClosedAt != "" {
		t.Fatalf("SQLite supp1 should be open: %+v, %v", suppSQLite, err)
	}
	var childSQLite wsagent.Record
	if err := json.Unmarshal(snap.Records["child1"].JSON, &childSQLite); err != nil || childSQLite.Profile != "worker-codex" || childSQLite.ClosedAt != "" {
		t.Fatalf("SQLite child1 should retain original profile: %+v, %v", childSQLite, err)
	}
	if issue, ok := snap.ImportIssues["plan-usage-history.json"]; !ok || issue.Status != wsmeta.ImportFuture {
		t.Fatalf("expected future ImportIssue for plan-usage-history.json, got %+v", snap.ImportIssues)
	}
}

func TestCutoverUnreadableHooksIsCleanFallbackBeforeReplacementFences(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "dashboard")
	writeLegacyFiles(t, dir, map[string]string{
		"sessions/lead1/agent.json":      `{"schema_version":1,"reply_id":"lead1","profile":"lead","backend":"codex","root":"/r","ws_session_key":"k-lead","lead":true}`,
		"sessions/lead1/permission.json": `{"permission_level":"full"}`,
	})
	// Dangling symlink for tracker-hooks.json is immediately CaptureUnreadable on every OS.
	if err := os.Symlink(filepath.Join(dir, "missing-hooks-target.json"), filepath.Join(dir, wsmeta.LegacyTrackerHooksConfigName)); err != nil {
		t.Fatal(err)
	}
	s, err := wsmeta.Open(dir, wsmeta.Options{CheckpointIdle: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_, err = s.Cutover(ctx, wsmeta.CutoverOptions{Validate: allOwnerValidators()})
	if !errors.Is(err, wsmeta.ErrCutoverUnsupported) || !errors.Is(err, wsmeta.ErrRemovalUnreadable) {
		t.Fatalf("expected ErrCutoverUnsupported wrapping ErrRemovalUnreadable, got %v", err)
	}
	// Permission file was NOT fenced because removal fence ran first.
	raw, rerr := os.ReadFile(filepath.Join(dir, "sessions", "lead1", "permission.json"))
	if rerr != nil || string(raw) != `{"permission_level":"full"}` {
		t.Fatalf("permission.json should remain untouched on clean fallback, got (%q, %v)", raw, rerr)
	}
}

func TestCutoverRetriesWhenLegacyWriterMutatesMidCutover(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "dashboard")
	writeLegacyFiles(t, dir, map[string]string{
		"sessions/lead1/agent.json":      `{"schema_version":1,"reply_id":"lead1","profile":"lead","backend":"codex","root":"/r","ws_session_key":"k-lead","lead":true}`,
		"sessions/lead1/permission.json": `{"permission_level":"full"}`,
	})
	s, err := wsmeta.Open(dir, wsmeta.Options{CheckpointIdle: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mutated := false
	res, err := s.Cutover(ctx, wsmeta.CutoverOptions{
		Validate: allOwnerValidators(),
		BeforeFence: func(attempt int, _ []wsmeta.Capture) error {
			if !mutated {
				mutated = true
				// Simulate an intervening legacy writer lowering permission after Stage.
				return os.WriteFile(filepath.Join(dir, "sessions", "lead1", "permission.json"), []byte(`{"permission_level":"read-only"}`), 0o600)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Cutover did not recover from mid-cutover legacy write: %v", err)
	}
	if res.AlreadyComplete {
		t.Fatalf("expected fresh completion")
	}
	snap, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p := snap.Permissions["lead1"]; p.Level != "read-only" {
		t.Fatalf("expected lowered permission read-only after retry, got %+v", p)
	}
}
