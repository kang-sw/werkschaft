package wsdashboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

// The draft recovery merge clamps to the composer's IMG_MAX, which must be
// the inbox's per-message cap (inbox_routes.go refuses more).
func TestDraftImageCapMatchesInbox(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("assets", "dashboard-composer-images.js"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`var IMG_MAX = (\d+)`).FindSubmatch(src)
	if m == nil || string(m[1]) != strconv.Itoa(wsinbox.MaxImages) {
		t.Fatalf("IMG_MAX = %q, want wsinbox.MaxImages = %d", m, wsinbox.MaxImages)
	}
}

// TestDraftRecoveryMerge runs saveDraft's shared-row merge in node VMs:
// byte identity for ID-less images, no eviction past the image cap, and
// tombstones applied only once a write commits.
func TestDraftRecoveryMerge(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	out, err := exec.Command(node, "testdata/draft-merge.test.cjs").CombinedOutput()
	if err != nil {
		t.Fatalf("draft merge fixture: %v\n%s", err, out)
	}
	t.Log(string(out))
}

func TestDashboardDraftRecoveryBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE for Chromium acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", t.TempDir())
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	f := startPageFixture(t)
	f.withCommands(t, wslive.SlashCommand{Name: "compact", Source: "builtin"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "testdata/draft-recovery.browser.cjs", f.base)
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("dashboard draft recovery in Chromium: %v", err)
	}
	if os.Getenv("WS_DRAFT_CAPTURE_BEFORE") == "" {
		held := f.held(t)
		if len(held) != 4 {
			t.Fatalf("expected four independently accepted POSTs, got %d: %+v", len(held), held)
		}
		var queued int
		for _, msg := range held {
			if inboxBody(t, msg.Entry) == "Queued exactly once across tabs" {
				queued++
			}
		}
		if queued != 1 {
			t.Fatalf("queued send count across the two tabs = %d, want 1", queued)
		}
	}
}
