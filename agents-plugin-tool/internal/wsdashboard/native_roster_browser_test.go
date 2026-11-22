package wsdashboard

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// TestNativeSubagentRosterBrowser renders native subagent rows on a fixture
// session page of an agy lead in disposable Chromium: the compact HUD with a
// running agy native row and its lifecycle tooltip, the observation-ended
// count, and the expanded tree's ended group with an end-unknown agy row and
// a completed Codex row. WS_NATIVE_SHOTS=<dir> saves the captures. Needs
// WS_PLAYWRIGHT_MODULE.
func TestNativeSubagentRosterBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE for disposable Chromium acceptance")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	f := startPageFixture(t)
	reg := register(t, f.dir, "livesession", f.project, time.Now())
	reg.Harness = "agy"
	if err := WriteRegistration(f.dir, reg); err != nil {
		t.Fatal(err)
	}
	f.d.Index().Refresh()
	waitFor(t, "the agy lead registration", func() bool {
		s, ok := f.d.Index().Session("livesession")
		return ok && s.Harness == "agy"
	})
	now := time.Now().UnixMilli()
	running := wslive.Agent{ID: wslive.NativeAgyPrefix + "call_17051178851859014051", Label: "Kiwi Responder", Status: wslive.StatusRunning,
		StartedAt: now - 42_000, TurnStartedAt: now - 42_000, Activity: wslive.Activity{Phase: wslive.PhaseThinking, PhaseStartedAt: now - 42_000}}
	unknown := wslive.Agent{ID: wslive.NativeAgyPrefix + "call_10998817474857222024", Label: "research", Status: wslive.StatusDone,
		StartedAt: now - 15*60_000, EndedAt: now - 10*60_000, EndUnknown: true, Activity: wslive.Activity{Phase: wslive.PhaseIdle, PhaseStartedAt: now - 10*60_000}}
	codex := wslive.Agent{ID: wslive.NativeCodexPrefix + "call_991318ad7bb04b65ace1b4b9e9f15336", Label: "ok_reply", Status: wslive.StatusDone,
		StartedAt: now - 20*60_000, EndedAt: now - 18*60_000, ActiveMs: 120_000, Activity: wslive.Activity{Phase: wslive.PhaseIdle, PhaseStartedAt: now - 18*60_000}}
	snap := wslive.Snapshot{SchemaVersion: 1, Adapter: "agy", UpdatedAt: now, HeartbeatMs: 30000,
		Lead:   wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseThinking, PhaseStartedAt: now - 50_000}, TurnStartedAt: now - 60_000},
		Agents: []wslive.Agent{codex, unknown, running}}
	f.d.snapshots.SetLive(f.dir, "livesession", snap)
	f.d.broker.wake(liveDep("livesession"))
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "testdata/native-roster.browser.cjs")
	cmd.Env = append(os.Environ(), "NATIVE_BASE="+f.base, "NATIVE_SHOTS="+os.Getenv("WS_NATIVE_SHOTS"))
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("native roster browser: %v", err)
	}
}
