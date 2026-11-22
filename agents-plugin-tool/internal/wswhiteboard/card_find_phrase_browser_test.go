package wswhiteboard

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestCardFindPhraseBrowser checks that host card-find.js and the doc frame's
// runFind match a phrase across inline formatting (e.g. bold) in Chromium.
func TestCardFindPhraseBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Chromium acceptance")
	}
	host, err := assetFS.ReadFile("assets/card-find.js")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := assetFS.ReadFile("assets/doc-frame.js")
	if err != nil {
		t.Fatal(err)
	}
	_, frame, found := strings.Cut(string(raw), "  // findMarks holds each match's first mark;")
	frame, _, found2 := strings.Cut(frame, "  window.addEventListener(\"message\"")
	if !found || !found2 {
		t.Fatal("doc-frame.js find block markers moved")
	}
	dir := t.TempDir()
	hostPath, framePath := dir+"/card-find.js", dir+"/frame-find.js"
	if err := os.WriteFile(hostPath, host, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(framePath, []byte("//"+frame), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/card-find-phrase.browser.cjs", hostPath, framePath).CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("card-find phrase Chromium: %v", err)
	}
}
