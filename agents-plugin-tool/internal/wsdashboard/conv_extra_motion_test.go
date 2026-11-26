package wsdashboard

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

func TestSettingsPopoverHasExtraMotionAndNoLayoutSwitcher(t *testing.T) {
	f := startPageFixture(t)
	for _, path := range []string{"/", "/s/livesession"} {
		code, body := get(t, f.base+path)
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, code)
		}
		gmenuIdx := strings.Index(body, `id="gmenu"`)
		if gmenuIdx < 0 {
			t.Fatalf("%s: missing #gmenu settings popover", path)
		}
		gmenuSlice := body[gmenuIdx:]
		if end := strings.Index(gmenuSlice, `</select>`); end > 0 {
			gmenuSlice = gmenuSlice[:end]
		}
		for _, want := range []string{
			`data-extra-motion="on"`,
			`data-extra-motion="off"`,
			`Extra Motion`,
		} {
			if !strings.Contains(gmenuSlice, want) {
				t.Errorf("%s: settings popover missing %q", path, want)
			}
		}
		if strings.Contains(gmenuSlice, `data-layout=`) || strings.Contains(body, `<button data-layout=`) {
			t.Errorf("%s: settings popover must not contain obsolete data-layout controls:\n%s", path, gmenuSlice)
		}
	}
}

func TestConvLiveAdditiveMotionAttributesAndExactTokens(t *testing.T) {
	now := testNow
	lead := wslive.Lead{
		Activity: wslive.Activity{
			Phase:          wslive.PhaseTool,
			PhaseStartedAt: now.Add(-1 * time.Second).UnixMilli(),
			Done:           3,
			Failed:         1,
			Batches: []wslive.BatchView{
				{
					ToolNames: []string{"ws/exec.shell"},
					CallCount: 3,
					Failed:    1,
					StartedAt: now.Add(-4 * time.Second).UnixMilli(),
					EndedAt:   now.Add(-2 * time.Second).UnixMilli(),
				},
				{
					StartedAt: now.Add(-1 * time.Second).UnixMilli(),
					Running: &wslive.RunningView{
						ToolName:    "ws/git.diff",
						ArgsPreview: "HEAD~1",
						StartedAt:   now.Add(-1 * time.Second).UnixMilli(),
					},
				},
			},
		},
		TurnStartedAt:   now.Add(-5 * time.Second).UnixMilli(),
		TurnAddedTokens: 1440,
		ToolGap: &wslive.ToolSummary{
			Calls:      3,
			Failed:     1,
			DurationMs: 2500,
			InBytes:    400,
			OutBytes:   1200,
			Batches:    []wslive.ToolBatch{{Names: []string{"ws/exec.shell"}, Calls: 3, Failed: 1, DurationMs: 2500}},
		},
	}

	view := buildSessionView(pageInputs{
		Session:  liveSession("motion-test", "/tmp/proj", now),
		Snapshot: liveSnap(now, lead),
		Now:      now,
	})
	set, err := renderSessionRegions(view, newEntryCache())
	if err != nil {
		t.Fatalf("render r-convlive: %v", err)
	}
	html := string(set[regionConvLive].HTML)
	for _, want := range []string{
		`data-phase="tool"`,
		`data-tool="ws/git.diff"`,
		`data-done="3"`,
		`data-failed="1"`,
		`data-turn-tokens="1440"`,
		`data-tokens="1440"`,
		`data-conv-turn-tokens`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("r-convlive missing %q in:\n%s", want, html)
		}
	}

	// Estimated (~) fallback must NOT emit exact data-tokens on [data-conv-turn-tokens].
	leadEst := lead
	leadEst.TurnAddedTokens = 0
	viewEst := buildSessionView(pageInputs{
		Session:  liveSession("motion-test", "/tmp/proj", now),
		Snapshot: liveSnap(now, leadEst),
		Now:      now,
	})
	setEst, err := renderSessionRegions(viewEst, newEntryCache())
	if err != nil {
		t.Fatalf("render estimated r-convlive: %v", err)
	}
	htmlEst := string(setEst[regionConvLive].HTML)
	if !strings.Contains(htmlEst, `data-estimated="1"`) {
		t.Errorf("estimated turn summary missing data-estimated=\"1\":\n%s", htmlEst)
	}
	if strings.Contains(htmlEst, `data-tokens=`) {
		t.Errorf("estimated turn summary must not emit exact data-tokens:\n%s", htmlEst)
	}
}

func TestConvExtraMotionEmbeddedAndUnitHarness(t *testing.T) {
	assets, err := loadDashboardAssets()
	if err != nil {
		t.Fatalf("loadDashboardAssets: %v", err)
	}
	if !strings.Contains(string(assets.data.Script), "WsConvMotion") {
		t.Fatalf("embedded dashboard script missing WsConvMotion")
	}
	if !strings.Contains(string(assets.data.CSS), "d-conv-motion-canvas") {
		t.Fatalf("embedded dashboard CSS missing d-conv-motion-canvas")
	}
	runNode(t, "conv-extra-motion unit harness", `
		const cp = require("node:child_process");
		const res = cp.spawnSync(process.execPath, ["--test", "./testdata/conv-extra-motion.test.cjs"], { encoding: "utf8" });
		if (res.status !== 0) {
			throw new Error((res.stdout || "") + "\n" + (res.stderr || ""));
		}
	`)
}

func TestConvExtraMotionBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	t.Setenv("WS_MCP_DASHBOARD_DEV_SOURCE", "")
	dir := t.TempDir()
	project := t.TempDir()
	now := time.Now()
	register(t, dir, "livesession", project, now)
	src := newPageFake()
	src.setPage("livesession", PageContent{HasBoard: true, Seq: 3, Board: boardRegions(t, "live board text")})
	if err := wslive.AppendRing(wslive.RingDir(SessionDir(dir, "livesession")), []wslive.RingEntry{
		{Kind: wslive.RingUser, Origin: wslive.OriginTerminal, Text: "Run verification suite and check motion"},
	}); err != nil {
		t.Fatal(err)
	}
	writeSnapshot(t, dir, "livesession", &wslive.Snapshot{
		SchemaVersion: 1,
		Adapter:       "codex",
		UpdatedAt:     now.UnixMilli(),
		HeartbeatMs:   3600000,
		Lead: wslive.Lead{
			Activity: wslive.Activity{
				Phase:          wslive.PhaseTool,
				PhaseStartedAt: now.Add(-2 * time.Second).UnixMilli(),
				Done:           3,
				Failed:         0,
				Batches: []wslive.BatchView{
					{
						ToolNames: []string{"ws/exec.shell"},
						CallCount: 3,
						StartedAt: now.Add(-6 * time.Second).UnixMilli(),
						EndedAt:   now.Add(-3 * time.Second).UnixMilli(),
					},
					{
						StartedAt: now.Add(-2 * time.Second).UnixMilli(),
						Running: &wslive.RunningView{
							ToolName:    "ws/exec.shell",
							ArgsPreview: "go test ./internal/wsdashboard/...",
							StartedAt:   now.Add(-2 * time.Second).UnixMilli(),
						},
					},
				},
			},
			TurnStartedAt:   now.Add(-7 * time.Second).UnixMilli(),
			TurnAddedTokens: 1440,
			ToolGap: &wslive.ToolSummary{
				Calls:      3,
				DurationMs: 4100,
				InBytes:    400,
				OutBytes:   1200,
				Batches:    []wslive.ToolBatch{{Names: []string{"ws/exec.shell"}, Calls: 3, DurationMs: 4100}},
			},
		},
	})
	d := startIndexedDaemon(t, dir, src, time.Hour, func(o *DaemonOptions) { o.noGitBadge = true })
	waitFor(t, "livesession indexed", func() bool {
		_, ok := d.Index().Session("livesession")
		return d.Index().Loaded() && ok
	})
	base := endpointURL(d.Endpoint(), "")
	mockupPath, _ := filepath.Abs(filepath.Join("..", "..", "..", "ai-docs", "assets", "mockups", "261125", "whiteboard-chat-view", "mockup.html"))
	cmd := exec.Command("node", "testdata/conv-extra-motion.browser.cjs", base, mockupPath)
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Conv Extra Motion browser: %v", err)
	}
}
