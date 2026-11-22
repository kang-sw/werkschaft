package wsdashboard_test

import (
	"context"
	"net/http"
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

// tierUIAgents makes the daemon host lead sessions, so the rail shows the
// new-session button; the browser script answers the agent and support
// routes itself, so no harness runs.
type tierUIAgents struct{}

func (tierUIAgents) Handler() http.Handler      { return http.NotFoundHandler() }
func (tierUIAgents) Start(func(string, string)) {}
func (tierUIAgents) InboxChanged(string)        {}
func (tierUIAgents) Close()                     {}

// tierUIWorktrees shows the session folder as a worktree row; no git runs.
type tierUIWorktrees struct{}

func (tierUIWorktrees) ReadWorktrees(wsdashboard.Registration) (wsdashboard.WorktreePool, error) {
	return wsdashboard.WorktreePool{}, nil
}
func (tierUIWorktrees) ReadRepoWorktrees(repo string, _ []wsdashboard.Registration) (wsdashboard.RepoWorktrees, error) {
	return wsdashboard.RepoWorktrees{Worktrees: []wsdashboard.RepoWorktree{{Path: repo, Branch: "develop"}}}, nil
}

// The tier routing editor and the new-session named-tier picker in headless
// Chromium against the production settings backend (the daemon's own graph
// view, preview, and validation): pointer edits, passthrough and pinning,
// exceptions, reset, user nodes and bundles, refused deletion with inbound
// references, inline cycle and hop-limit errors, reload against the
// daemon's resolution, and the picker filling the launch fields.
// WS_TIER_ROUTING_SHOTS=<abs dir> saves the captures. Needs
// WS_PLAYWRIGHT_MODULE.
func TestTierRoutingUIBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run the tier routing Chromium acceptance")
	}
	shots := os.Getenv("WS_TIER_ROUTING_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_TIER_ROUTING_SHOTS must be absolute")
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
	// The seed: review and explore move to codex, explore:small pins a
	// claude slot, one playbook goes through a user node to a bundle, one to
	// another bundle, and implement and an empty user node keep the caller
	// fallthrough.
	seed := `{"overrides":{"workflow.lang":"English"},"structured_overrides":{"agent.tier_routing":{
		"nodes":{"review":{"*":"codex.*"},"explore":{"*":"codex.*","small":"claude.small"},
			"ticket-reviewer-design":{"*":"deep-review:*"},"deep-review":{"*":"deep-design"},
			"ticket-selector":{"*":"fast-triage"},"lead-explore":{}},
		"bundles":{"deep-design":{"harness":"claude","model":"claude-opus-4-6","effort":"high"},"fast-triage":{"harness":"agy","model":"gemini-flash","effort":"low"}}}}}`
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	source := mcp.NewDashboardSessionSource()
	backend, err := mcp.DashboardConfigBackend("ws", wsconfig.Options{CacheHome: cache, ConfigHome: home}, source, nil)
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "testdata/tier-routing.browser.cjs", login, shots)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("tier routing UI in Chromium: %v", err)
	}
}
