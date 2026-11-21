package wsdashboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDialogLayoutBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE for Chromium/WebKit dialog layout acceptance")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	if os.Getenv("PLAYWRIGHT_BROWSERS_PATH") == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			t.Setenv("PLAYWRIGHT_BROWSERS_PATH", filepath.Join(home, "Library", "Caches", "ms-playwright"))
		}
	}
	f := startPageFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "testdata/dialog-layout.browser.cjs", f.base)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("dialog layout browser test: %v", err)
	}
}
