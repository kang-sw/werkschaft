package wsdashboard

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsauditstore"
	"github.com/kang-sw/devenv/internal/wslive"
)

// TestAuditRowsBoundBrowser opens a hosted-session conversation whose audit
// store holds far more hidden events than the history it shows (one init per
// turn, subagent task lifecycle, thinking, hooks) and checks that the rows
// stay bounded: one session banner, folded runs, and a capped open-below
// fetch. WS_AUDIT_BOUND_SHOTS names a directory for a screenshot.
func TestAuditRowsBoundBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE for disposable Chromium acceptance")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	for _, key := range []string{"HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "WS_CACHE_HOME", "WS_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		dir := filepath.Join(home, key)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, dir)
	}
	t.Setenv("WS_MCP_DASHBOARD_DEV_SOURCE", "")

	fx := &fixtureAudit{}
	f := startPageFixture(t, func(o *DaemonOptions) { o.Home, o.auditRead = os.Getenv("HOME"), fx })
	ringDir := wslive.RingDir(SessionDir(f.dir, "livesession"))

	// Audit events older than every conversation entry: 2400 hidden events, a
	// turn's worth of init / task lifecycle / thinking / hook / system noise.
	start := time.Now().UTC().Add(-time.Hour)
	var events []wsaudit.Envelope
	add := func(at time.Time, kind wsaudit.Kind, sub string) {
		n := len(events) + 1
		events = append(events, wsaudit.Envelope{
			V: 1, ID: fmt.Sprintf("b-%05d", n), Seq: uint64(n), Session: "livesession", At: at,
			Harness: "claude", Kind: kind, Subkind: sub,
			Out: `{"type":"system","subtype":"` + sub + `","session_id":"s","tools":["Read","Grep","Bash"]}`,
		})
	}
	noise := []struct {
		kind wsaudit.Kind
		sub  string
	}{
		{wsaudit.KindSystem, "init"}, {wsaudit.KindSubagent, "task_started"}, {wsaudit.KindSubagent, "task_updated"},
		{wsaudit.KindSubagent, "task_notification"}, {wsaudit.KindThinking, "reasoning"}, {wsaudit.KindHook, "PreToolUse"},
	}
	for i := 0; i < 2400; i++ {
		n := noise[i%len(noise)]
		add(start.Add(time.Duration(i)*time.Millisecond), n.kind, n.sub)
	}

	time.Sleep(20 * time.Millisecond)
	var entries []wslive.RingEntry
	for i := 0; i < 12; i++ {
		kind := wslive.RingReply
		if i%2 == 0 {
			kind = wslive.RingUser
		}
		entries = append(entries, wslive.RingEntry{Kind: kind, Text: fmt.Sprintf("conversation entry %d", i)})
	}
	if err := wslive.AppendRing(ringDir, entries); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	// Inside the shown span: a per-turn init plus task lifecycle between entries.
	spanAt := time.Now().UTC()
	for i := 0; i < 24; i++ {
		n := noise[i%len(noise)]
		add(spanAt.Add(time.Duration(i)*time.Microsecond), n.kind, n.sub)
	}
	f.change(t, "livesession", f.project, "live board text", 5)
	fx.set("livesession", wsauditstore.View{Events: events})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "testdata/audit-rows-bound.browser.cjs")
	cmd.Env = append(os.Environ(), "AUDIT_BASE="+f.base, "AUDIT_BOUND_SHOTS="+os.Getenv("WS_AUDIT_BOUND_SHOTS"), "AUDIT_BOUND_EXPECT="+os.Getenv("WS_AUDIT_BOUND_EXPECT"))
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Log(out.String())
		t.Fatalf("audit rows bound Chromium: %v", err)
	}
	t.Log(out.String())
}
