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
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

func TestAccountSettingsBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Account in cached Chromium")
	}
	shots := os.Getenv("WS_ACCOUNT_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_ACCOUNT_SHOTS must be absolute")
	}
	cache, config := t.TempDir(), t.TempDir()
	t.Setenv("WS_CACHE_HOME", cache)
	t.Setenv("WS_CONFIG_HOME", config)
	t.Setenv("WS_RSRC_DEV_CHECKOUT", "")
	t.Setenv("WS_MCP_DASHBOARD_DEV_SOURCE", "")
	options := wsconfig.Options{CacheHome: cache, ConfigHome: config}
	backend, err := mcp.DashboardConfigBackend("ws", options, mcp.NewDashboardSessionSource(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	d, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{
		Dir: filepath.Join(cache, "dashboard"), Home: t.TempDir(), Config: backend,
		Credentials: wscred.NewStore(filepath.Join(config, wscred.FileName)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	login, err := d.LoginLink("/")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/account-settings.browser.cjs", login, shots).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium Account settings: %v", err)
	}
}
