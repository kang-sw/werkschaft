package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wspackage"
)

// Real stdio launchers, real isolated daemons, no installed workflow package.
// The same launcher survives replacement, then restarts the successor's build
// after a kill. Neither original executable remains available at that point.
func TestHostedLauncherSurvivesReplacementAndRestartsRecordedBuild(t *testing.T) {
	t.Setenv("WS_RSRC_DEV_CHECKOUT", "")
	for _, name := range []string{"ws", "wsflow"} {
		t.Run(name, func(t *testing.T) {
			bin := buildDashboardTestBin(t, "")
			env, dir := dashboardTestEnv(t)
			cwd := t.TempDir()
			env = append(env, "WS_RSRC_DEV_CHECKOUT=", "WS_MCP_ALLOWED_TOOLS=", "WS_MCP_NAMESPACE=",
				"WS_MCP_CONFIG_MANIFESTS="+writeProxyTestManifest(t, true),
				"WS_RSRC_ROOT="+filepath.Join(cwd, "absent-plugin", "rsrc"))
			start := func(binary string) {
				t.Helper()
				cmd := exec.Command(binary, "daemon-start")
				cmd.Env, cmd.Dir = env, cwd
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("isolated daemon-start: %v\n%s", err, out)
				}
			}
			start(bin)
			initial := waitForEndpoint(t, dir, version, 10*time.Second)
			root, err := wspackage.StageHosted(name, dir)
			if err != nil {
				t.Fatal(err)
			}
			policy, err := wsagent.HostedPluginEnvironment(name, root)
			if err != nil {
				t.Fatal(err)
			}
			launcherEnv := append([]string(nil), env...)
			for key, value := range policy {
				launcherEnv = append(launcherEnv, key+"="+value)
			}
			launcherEnv = append(launcherEnv, "WS_MCP_HOSTED_PACKAGE=1", "WS_MCP_PROJECT_ROOT="+cwd)
			client := startLauncher(t, filepath.Join(root, "bin", "ws-mcp-launcher.py"), cwd, launcherEnv, "serve", "--stdio")
			init := client.call("initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "hosted-fixture", "version": "0"}})
			if init["error"] != nil {
				t.Fatalf("initialize: %v", init)
			}
			client.notify("notifications/initialized")
			assertRead := func(want string) {
				t.Helper()
				// A request sent during the boundary may fail with unknown outcome;
				// this read-only fixture explicitly submits a fresh request, rather
				// than requiring the launcher to replay a sent request.
				for attempt := 0; attempt < 2; attempt++ {
					response := client.call("tools/call", map[string]any{"name": "runtime.read", "arguments": map[string]any{"format": "json"}})
					if response["error"] != nil {
						continue
					}
					if text := toolText(t, response); !strings.Contains(text, want) {
						t.Fatalf("runtime.read = %s, want %s", text, want)
					}
					return
				}
				t.Fatalf("no runtime.read answer\n%s", client.stderr.String())
			}
			assertRead(version)

			// Newer than this build in patch only: a release bump moves the
			// build's minor, and a fixed literal then lands older or in another
			// minor than the launcher accepts.
			var major, minor int
			if _, err := fmt.Sscanf(version, "%d.%d.", &major, &minor); err != nil {
				t.Fatalf("build version %q: %v", version, err)
			}
			successorVersion := fmt.Sprintf("%d.%d.99-dev", major, minor)
			successor := buildDashboardTestBin(t, successorVersion)
			start(successor) // Ordinary newer-core replacement, no --replace.
			replaced := waitForEndpoint(t, dir, successorVersion, 15*time.Second)
			if replaced.PID == initial.PID {
				t.Fatal("successor did not replace the original daemon")
			}
			assertRead(successorVersion)
			// A direct daemon-start spawns the daemon from its own path (the
			// hosted launcher stages a copy before it calls daemon-start), so
			// the successor daemon still runs from successor here. Windows
			// refuses to delete a running image but permits renaming it,
			// which equally takes the original path away.
			for _, original := range []string{bin, successor} {
				if runtime.GOOS == "windows" {
					if err := os.Rename(original, filepath.Join(t.TempDir(), "moved.exe")); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.Remove(original); err != nil {
					t.Fatal(err)
				}
			}
			var record struct {
				Executable string `json:"executable"`
				Version    string `json:"version"`
			}
			raw, err := os.ReadFile(filepath.Join(dir, wsdashboard.RunningExecutableFile))
			if err != nil || json.Unmarshal(raw, &record) != nil || record.Version != successorVersion {
				t.Fatalf("successor restart record %s: %v", raw, err)
			}
			process, err := os.FindProcess(replaced.PID)
			if err != nil {
				t.Fatal(err)
			}
			if err := process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = process.Release()
			// Wait for a different healthy PID, not the killed endpoint's file.
			var restarted wsdashboard.Endpoint
			for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
				if ep, err := wsdashboard.ReadEndpoint(dir); err == nil && ep.PID != replaced.PID {
					if health, err := wsdashboard.Health(context.Background(), dashboardTestClient, ep); err == nil && health.Version == successorVersion {
						restarted = ep
						break
					}
				}
				time.Sleep(50 * time.Millisecond)
			}
			if restarted.PID == 0 {
				t.Fatalf("hosted launcher did not restart the recorded build\n%s", client.stderr.String())
			}
			assertRead(successorVersion)
			if _, err := os.Stat(filepath.Join(root, ".runtime")); !os.IsNotExist(err) {
				t.Fatalf("hosted package acquired a plugin runtime: %v", err)
			}
			if _, err := os.Stat(record.Executable); err != nil {
				t.Fatal(err)
			}
		})
	}
}
