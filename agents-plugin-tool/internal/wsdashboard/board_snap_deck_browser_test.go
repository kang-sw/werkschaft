package wsdashboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func runSnapDeckBrowserSuite(t *testing.T, script string, timeout time.Duration) {
	t.Helper()
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skipf("set WS_PLAYWRIGHT_MODULE to run Chromium snap-deck suite %s", script)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not installed; run node --test %s locally", script)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, node, "--test", filepath.FromSlash(script)).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("%s failed: %v", script, err)
	}
}

func TestBoardScrollP1FixesBrowser(t *testing.T) {
	runSnapDeckBrowserSuite(t, "testdata/board-scroll-p1-fixes.browser.cjs", 90*time.Second)
}

func TestBoardSnapDeckP2Browser(t *testing.T) {
	runSnapDeckBrowserSuite(t, "testdata/board-snap-deck-p2.browser.cjs", 90*time.Second)
}

func TestBoardSnapDeckFeelBrowser(t *testing.T) {
	runSnapDeckBrowserSuite(t, "testdata/board-snap-deck-feel.browser.cjs", 90*time.Second)
}

func TestBoardSnapDeckP3Browser(t *testing.T) {
	runSnapDeckBrowserSuite(t, "testdata/board-snap-deck-p3.browser.cjs", 120*time.Second)
}

func TestBoardSnapDeckP4Browser(t *testing.T) {
	runSnapDeckBrowserSuite(t, "testdata/board-snap-deck-p4.browser.cjs", 120*time.Second)
}
