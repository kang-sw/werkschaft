package wsdashboard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

// TestBoardHoldScrollBrowser clicks the hold toggle on a scrolled,
// multi-topic free-canvas board in live Chromium and asserts #bcol's
// scrollTop and the clicked toggle's viewport Y stay put across the click
// and the board SSE echo, for hold on and hold off.
func TestBoardHoldScrollBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Chromium acceptance")
	}
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", t.TempDir())
	t.Setenv("WS_MCP_DASHBOARD_DEV_SOURCE", "")
	SetDevAssets(nil)

	var cmds strings.Builder
	for i := 1; i <= 8; i++ {
		var body strings.Builder
		fmt.Fprintf(&body, "# Topic %d body\n\n", i)
		// t6 is short, so its asks show while t5 is still the anchor.
		lines := 24
		if i == 6 {
			lines = 2
		}
		for l := 1; l <= lines; l++ {
			fmt.Fprintf(&body, "- Topic %d line %02d keeps the body frame tall enough to scroll.\n", i, l)
		}
		fmt.Fprintf(&cmds, `<wb:topic name="t%d" label="Topic %d"><wb:body>%s</wb:body>`, i, i, body.String())
		for a := 1; a <= 3; a++ {
			fmt.Fprintf(&cmds, `<wb:add class="policy"><wb:title>Topic %d ask %d</wb:title><wb:option>Alpha</wb:option><wb:option>Beta</wb:option></wb:add>`, i, a)
		}
		cmds.WriteString(`</wb:topic>`)
	}
	b := newTestBoardDB(t, cmds.String())
	// A live adapter snapshot renders the live grid, whose bounded #bcol
	// runs the free-canvas virtual scroll.
	dir := t.TempDir()
	project := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	register(t, dir, "sessone", project, time.Now())
	writeSnapshot(t, dir, "sessone", &wslive.Snapshot{SchemaVersion: 1, Adapter: "pi", UpdatedAt: time.Now().UnixMilli(), HeartbeatMs: 3600000, Lead: wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}}})
	src := &holdBoardSource{boards: map[string]*wswhiteboard.BoardDB{"sessone": b}}
	d := startIndexedDaemon(t, dir, src, time.Hour, func(o *DaemonOptions) { o.noGitBadge = true })
	base := endpointURL(d.Endpoint(), "")
	waitFor(t, "index to load sessone", func() bool {
		s, ok := d.Index().Session("sessone")
		return d.Index().Loaded() && ok && s.HasBoard
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/board-hold-scroll.browser.cjs", base).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("board hold scroll browser: %v", err)
	}
}
