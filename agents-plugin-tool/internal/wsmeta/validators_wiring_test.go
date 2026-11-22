package wsmeta_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmeta"
)

// The legacy owners' exported validators plug into Commit as they are, and
// each receives the identity it dispatches on: a record its session, a
// history its file name.
func TestOwnersValidatorsWireIntoCommit(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "dashboard")
	files := map[string]string{
		"sessions/good/agent.json":        `{"schema_version":1,"reply_id":"good","root":"/r","ws_session_key":"k"}`,
		"sessions/typo/agent.json":        `{"reply_id":"typo","root":"/r","ws_session_key":"k","closed_at":123}`,
		"plan-usage-codex-history.json":   `{"schema_version":1,"samples":[{"harness":"codex","kind":"five_hour","source":"via Codex CLI","resetsAt":0,"asOf":10,"percent":5}]}`,
		"plan-usage-history.json":         `{"schema_version":1,"samples":[{"harness":"codex","kind":"five_hour","source":"via Codex CLI","resetsAt":0,"asOf":10,"percent":5}]}`,
		"plan-usage-agy-history.json":     `{"schema_version":1,"samples":[]}`,
		"sessions/good/permission.json":   `{"permission_level":"read-only"}`,
		"sessions/typo/permission.json":   `{"permission_level":"read-only"}`,
		"sessions/good/registration.json": `{}`,
	}
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := wsmeta.Open(dir, wsmeta.Options{CheckpointIdle: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	_, err = s.Commit(ctx, wsmeta.CommitOptions{Validate: map[string]wsmeta.LegacyValidator{
		wsmeta.StoreAgentRecord:       wsagent.ValidateLegacyRecord,
		wsmeta.StoreUsageHistory:      wsdashboard.ValidateLegacyUsageHistory,
		wsmeta.StoreUsageHistoryCodex: wsdashboard.ValidateLegacyUsageHistory,
		wsmeta.StoreUsageHistoryAgy:   wsdashboard.ValidateLegacyUsageHistory,
	}})
	if err != nil {
		t.Fatal(err)
	}
	caps, err := s.Captures(ctx)
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	detail := map[string]string{}
	for _, c := range caps {
		status[c.Path], detail[c.Path] = c.ImportStatus, c.ImportDetail
	}
	want := map[string]string{
		"sessions/good/agent.json":      wsmeta.ImportImported,
		"sessions/typo/agent.json":      wsmeta.ImportInvalid,
		"plan-usage-codex-history.json": wsmeta.ImportImported,
		"plan-usage-agy-history.json":   wsmeta.ImportImported,
		// The shared history admits claude samples only.
		"plan-usage-history.json": wsmeta.ImportInvalid,
	}
	for rel, w := range want {
		if status[rel] != w {
			t.Errorf("%s: %s (%s), want %s", rel, status[rel], detail[rel], w)
		}
	}
}
