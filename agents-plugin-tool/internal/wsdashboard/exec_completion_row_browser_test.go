package wsdashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsstate"
)

// TestExecCompletionRowBrowser verifies the compact ws/exec completion row in
// real Chromium: single-line collapsed ok/fail rendering without user-bubble
// affordances, inline expand with zero closed-state /exec/bg/* requests,
// View output jump when #bgbadge is hidden (anchor fallback on-screen),
// bgPendingSel overriding first-running auto-selection, expired job button
// hide + inline note, legacy RingUser parity, and LAN read-only hiding.
func TestExecCompletionRowBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Chromium acceptance")
	}
	shots := os.Getenv("WS_EXEC_ROW_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_EXEC_ROW_SHOTS must be absolute")
	}
	f := startComposerFixture(t, lanMode)
	now := time.Now().UnixMilli()
	zero := 0

	doneJob := &bgExecBrowserJob{
		Owner: "livesession", ID: "exec_done_ok", Tool: "exec.shell",
		Command: "go test ./internal/wsinbox -v", Cwd: "agents-plugin-tool",
		State: execjob.HostedExited, ExitCode: &zero, Origin: execjob.OriginAgent,
		StartedAt: now - 40000, EndedAt: now - 12000,
		segs: []execjob.Segment{
			{Stream: execjob.StreamStdout, Data: []byte("=== RUN   TestExecEnvelope\n")},
			{Stream: execjob.StreamStdout, Data: []byte("--- PASS: TestExecEnvelope (0.02s)\nPASS\n")},
		},
	}
	doneJob.written = int64(len(doneJob.segs[0].Data) + len(doneJob.segs[1].Data))

	svc := &bgExecBrowserSvc{
		d:      f.d,
		counts: map[string]int{},
		jobs:   []*bgExecBrowserJob{doneJob},
		trees: map[string][]ExecTreeNode{
			"livesession": {
				{ID: "livesession", Label: "root-lead", Depth: 0},
			},
		},
	}
	f.d.SetExecJobs(svc)

	okNotice := "[ws exec] exec_done_ok exited with code 0 after 28s.\n" +
		"command: go test ./internal/wsinbox -v\n" +
		"output:\n" +
		"--- PASS: TestExecEnvelope (0.02s)\nPASS\n" +
		"Read more with exec.tail (exec_id exec_done_ok)."

	expiredNotice := "[ws exec] exec_expired_fail exited with code 1 after 3.2s.\n" +
		"command: npm run typecheck\n" +
		"output (last 48 of 1200 bytes):\n" +
		"src/app.ts(12,5): error TS2322: type mismatch\n" +
		"Read more with exec.tail (exec_id exec_expired_fail)."

	expiredNotice2 := "[ws exec] exec_expired_two exited with code 2 after 1.1s.\n" +
		"command: make lint\n" +
		"no output.\n" +
		"Read more with exec.tail (exec_id exec_expired_two)."

	ring := []wslive.RingEntry{
		{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: "Run the checks in the background."},
		{Kind: wslive.RingWake, Cause: "background_exit", Source: "exec_done_ok", Text: okNotice},
		{Kind: wslive.RingWake, Cause: "background_exit", Source: "exec_expired_fail", Text: expiredNotice},
		{Kind: wslive.RingWake, Cause: "background_exit", Source: "exec_expired_two", Text: expiredNotice2},
		{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: okNotice},
	}
	if err := wslive.AppendRing(wslive.RingDir(SessionDir(f.dir, "livesession")), ring); err != nil {
		t.Fatal(err)
	}

	writeSnap := func() {
		svc.mu.Lock()
		selfRun := 0
		for _, j := range svc.jobs {
			if j.Origin != execjob.OriginUser && j.State == execjob.HostedRunning && j.Owner == "livesession" {
				selfRun++
			}
		}
		svc.mu.Unlock()
		snap := &wslive.Snapshot{
			SchemaVersion: 1,
			ReplyID:       "livesession",
			Adapter:       "pi",
			UpdatedAt:     time.Now().UnixMilli(),
			HeartbeatMs:   30000,
			Lead: wslive.Lead{
				Activity: wslive.Activity{Phase: wslive.PhaseIdle},
			},
			Host: &wslive.Host{
				BgExecCount: selfRun,
				BgExecSelf:  selfRun,
			},
		}
		if raw, err := json.Marshal(snap); err == nil {
			path := wslive.SnapshotPath(SessionDir(f.dir, "livesession"))
			if os.WriteFile(path+".tmp", raw, 0o644) == nil {
				_ = wsstate.AtomicReplaceFile(path+".tmp", path)
			}
		}
	}
	writeSnap()

	ctl := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(v)
	}
	ctl.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		svc.mu.Lock()
		cp := map[string]int{}
		for k, v := range svc.counts {
			cp[k] = v
		}
		svc.mu.Unlock()
		reply(w, map[string]any{"counts": cp})
	})
	ctl.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		svc.mu.Lock()
		svc.counts = map[string]int{}
		svc.mu.Unlock()
		reply(w, "ok")
	})
	ctl.HandleFunc("/add-running", func(w http.ResponseWriter, r *http.Request) {
		svc.mu.Lock()
		runJob := &bgExecBrowserJob{
			Owner: "livesession", ID: "exec_run_other", Tool: "exec.shell",
			Command: "sleep 300", Cwd: "agents-plugin-tool",
			State: execjob.HostedRunning, Origin: execjob.OriginAgent,
			StartedAt: time.Now().UnixMilli() - 2000,
			segs:      []execjob.Segment{{Stream: execjob.StreamStdout, Data: []byte("still running...\n")}},
		}
		runJob.written = int64(len(runJob.segs[0].Data))
		svc.jobs = append(svc.jobs, runJob)
		svc.mu.Unlock()
		writeSnap()
		f.d.ExecJobsChanged()
		reply(w, "ok")
	})
	ctlSrv := httptest.NewServer(ctl)
	defer ctlSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	beat := time.NewTicker(3 * time.Second)
	defer beat.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-beat.C:
				if reg, err := ReadRegistration(f.dir, "livesession"); err == nil {
					reg.HeartbeatAt = stampFor(time.Now())
					_ = WriteRegistration(f.dir, reg)
				}
				writeSnap()
			}
		}
	}()

	script := "testdata/exec-completion-row.browser.cjs"
	out, err := exec.CommandContext(ctx, "node", script, f.base, ctlSrv.URL, shots, readLANToken(t, f.dir)).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium exec-completion-row acceptance: %v", err)
	}
}
