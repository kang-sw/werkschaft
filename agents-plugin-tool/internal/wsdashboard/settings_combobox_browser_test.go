package wsdashboard_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/mcp"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

func TestSettingsComboboxBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run the settings combobox Chromium acceptance")
	}
	shots := os.Getenv("WS_COMBOBOX_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_COMBOBOX_SHOTS must be absolute")
	}
	cache, home := t.TempDir(), t.TempDir()
	t.Setenv("WS_CACHE_HOME", cache)
	t.Setenv("WS_CONFIG_HOME", home)
	t.Setenv("WS_RSRC_DEV_CHECKOUT", "")
	t.Setenv("WS_MCP_DASHBOARD_DEV_SOURCE", "")
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "devenv")
	if out, err := exec.Command("git", "init", "-q", "-b", "develop", root).CombinedOutput(); err != nil {
		t.Fatalf("initialize session worktree: %v\n%s", err, out)
	}
	seed := `{"overrides":{"workflow.lang":"English"},"structured_overrides":{"agent.tier_routing":{"bundles":{"deep-design":{"harness":"claude","model":"claude-opus-4-6","effort":"high"}}}}}`
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	source := mcp.NewDashboardSessionSource()
	backend, err := mcp.DashboardConfigBackend("ws", wsconfig.Options{CacheHome: cache, ConfigHome: home}, source, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cache, "dashboard")
	key, err := mcp.NewAgentKeyStore().MintLead(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := wsdashboard.WriteRegistration(dir, wsdashboard.Registration{
		ReplyID: wsmailbox.ReplyID(key), Project: root, Harness: "claude", GitCommonDir: filepath.Join(root, ".git"),
		RegisteredAt: now, HeartbeatAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	d, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{
		Dir: dir, Home: t.TempDir(), Sessions: source, Worktrees: tierUIWorktrees{}, Config: backend, Agents: tierUIAgents{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	deadline := time.Now().Add(5 * time.Second)
	for !d.Index().Loaded() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	login, err := d.LoginLink("/settings")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "testdata/settings-combobox.browser.cjs", login, shots)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("settings combobox Chromium acceptance: %v", err)
	}
}
