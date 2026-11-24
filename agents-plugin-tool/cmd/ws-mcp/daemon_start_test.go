package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

func TestDesktopPredecessor(t *testing.T) {
	identity := `{"port":1234,"pid":4321,"started_at":"time","version":"0.50.0","token_sha256":"` +
		strings.Repeat("a", 64) + `"}`
	if id, err := desktopPredecessor(strings.NewReader(identity)); err != nil || id.PID != 4321 {
		t.Fatalf("valid predecessor was refused: %v", err)
	}
	if id, err := desktopPredecessor(strings.NewReader("null")); err != nil || id != nil {
		t.Fatalf("missing-only start was refused: %v", err)
	}
	for _, input := range []string{"", "{}", identity + "null", strings.Repeat(" ", 4096) + "null"} {
		if _, err := desktopPredecessor(strings.NewReader(input)); err == nil {
			t.Fatal("accepted malformed or oversized handoff")
		}
	}
}

func TestDaemonStartReplaceFlagWiring(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WS_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(root, "config"))
	for _, replace := range []bool{false, true} {
		args := []string{"--build-time", "123", "--start-wait", "1s"}
		if replace {
			args = append(args, "--replace")
		}
		called := false
		daemonStartWithEnsure(args, func(_ context.Context, opts wsdashboard.EnsureOptions) (wsdashboard.Outcome, error) {
			called = true
			if opts.Replace != replace || opts.BuildTime != 123 || opts.StartWait != time.Second {
				t.Fatalf("starter options = %+v", opts)
			}
			return wsdashboard.OutcomeRunning, nil
		})
		if !called {
			t.Fatal("Ensure not called")
		}
	}
}

func TestDaemonStartStartsUnderLegacyLauncherLock(t *testing.T) {
	bin := buildDashboardTestBin(t, "")
	env, dir := dashboardTestEnv(t)
	env = append(env, "WS_MCP_CONFIG_MANIFESTS="+writeProxyTestManifest(t, true))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := flock.New(filepath.Join(dir, "launcher-start.lock"))
	if ok, err := lock.TryLock(); err != nil || !ok {
		t.Fatalf("fixture launcher lock: %v, %v", ok, err)
	}
	defer lock.Close()

	// The v0.51.1 launcher waits for daemon-start with the lock still held
	// and passes no ownership marker.
	ctx, cancel := context.WithTimeout(context.Background(), wsdashboard.DefaultStartWait)
	defer cancel()
	start := exec.CommandContext(ctx, bin, "daemon-start", "--build-time", "123")
	start.Env = env
	out, err := start.CombinedOutput()
	if err != nil || !strings.Contains(string(out), `"outcome":"started"`) {
		t.Fatalf("daemon-start with legacy launcher lock: %v\n%s", err, out)
	}
	if ep, err := wsdashboard.ReadEndpoint(dir); err != nil {
		t.Fatalf("read daemon endpoint: %v", err)
	} else if _, err := wsdashboard.Health(ctx, dashboardTestClient, ep); err != nil {
		t.Fatalf("started daemon is unhealthy: %v", err)
	}
}

func TestDaemonStartStarterLockOwnership(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WS_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(root, "config"))
	dir, err := wsdashboard.Dir("")
	if err != nil {
		t.Fatal(err)
	}
	daemon, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{Dir: dir, Home: root, Version: version})
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()
	lock := flock.New(filepath.Join(dir, "launcher-start.lock"))
	if ok, err := lock.TryLock(); err != nil || !ok {
		t.Fatalf("fixture launcher lock: %v, %v", ok, err)
	}
	defer lock.Close()

	const marker = "WS_MCP_LAUNCHER_START_LOCK_HELD"
	t.Setenv(marker, "1")
	daemonStartWithEnsure(nil, func(ctx context.Context, opts wsdashboard.EnsureOptions) (wsdashboard.Outcome, error) {
		if _, present := os.LookupEnv(marker); present {
			t.Fatal("starter marker would leak to the child")
		}
		ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		outcome, err := wsdashboard.Ensure(ctx, opts)
		if outcome != wsdashboard.OutcomeRunning || err != nil {
			t.Fatalf("inherited Go entry tried to take the lock again: %s, %v", outcome, err)
		}
		return outcome, nil
	})
}

func TestDaemonStartStrictLifetimeFlagWiring(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only CLI opt-in; compile-only on remote Linux")
	}
	root := t.TempDir()
	t.Setenv("WS_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(root, "config"))
	daemonStartWithEnsure([]string{"--require-independent-lifetime", "--replace"}, func(_ context.Context, opts wsdashboard.EnsureOptions) (wsdashboard.Outcome, error) {
		if !opts.SpawnBeforeShutdown || !opts.JoinStarterLock || !opts.PreserveRunningLAN || !opts.Replace {
			t.Fatalf("strict options = %+v", opts)
		}
		return wsdashboard.OutcomeRunning, nil
	})
}

// TestDaemonStartLANFollowsTheApplyRule pins daemonStartLAN: a starter the
// dashboard applies to decides from its settings; any other starter leaves
// a running daemon's mode alone and otherwise takes the global file's
// value, never its own settings' false.
func TestDaemonStartLANFollowsTheApplyRule(t *testing.T) {
	running := func(lan, ok bool) func() (bool, bool) { return func() (bool, bool) { return lan, ok } }
	global := func(v bool) func() bool { return func() bool { return v } }
	for _, c := range []struct {
		name              string
		applies, settings bool
		running           func() (bool, bool)
		global            func() bool
		want              bool
	}{
		{"applies: settings true", true, true, running(false, true), global(false), true},
		{"applies: settings false", true, false, running(true, true), global(true), false},
		{"not applying: running LAN daemon stays", false, false, running(true, true), global(false), true},
		{"not applying: running loopback daemon stays", false, false, running(false, true), global(true), false},
		{"not applying, none running: global true", false, false, running(false, false), global(true), true},
		{"not applying, none running: global false", false, false, running(false, false), global(false), false},
	} {
		if got := daemonStartLAN(c.applies, c.settings, c.running, c.global); got != c.want {
			t.Errorf("%s: daemonStartLAN = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDaemonStartBelowFloorFlagWiring(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WS_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("WS_CONFIG_HOME", filepath.Join(root, "config"))
	for _, below := range []bool{false, true} {
		args := []string{"--build-time", "123"}
		if below {
			args = append(args, "--below-floor")
		}
		called := false
		daemonStartWithEnsure(args, func(_ context.Context, opts wsdashboard.EnsureOptions) (wsdashboard.Outcome, error) {
			called = true
			if opts.BelowFloor != below {
				t.Fatalf("BelowFloor = %v, want %v", opts.BelowFloor, below)
			}
			return wsdashboard.OutcomeRunning, nil
		})
		if !called {
			t.Fatal("Ensure not called")
		}
	}
}

func TestDaemonStartBinaryMinorFloorRefusalAndRollback(t *testing.T) {
	bin := buildDashboardTestBin(t, "")
	env, dir := dashboardTestEnv(t)
	env = append(env, "WS_MCP_CONFIG_MANIFESTS="+writeProxyTestManifest(t, true))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	floorPath := filepath.Join(dir, wsdashboard.MinorFloorFile)
	if err := os.WriteFile(floorPath, []byte("99.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), wsdashboard.DefaultStartWait)
	defer cancel()

	// Without --below-floor: refused, names update path, floor stays 99.0.
	refuse := exec.CommandContext(ctx, bin, "daemon-start")
	refuse.Env = env
	refuseOut, refuseErr := refuse.CombinedOutput()
	if refuseErr == nil {
		t.Fatalf("daemon-start admitted below floor:\n%s", refuseOut)
	}
	if !strings.Contains(string(refuseOut), "update this plugin") {
		t.Fatalf("refusal missing update path:\n%s", refuseOut)
	}
	if raw, err := os.ReadFile(floorPath); err != nil || string(raw) != "99.0\n" {
		t.Fatalf("floor changed after refusal: %q (%v)", string(raw), err)
	}

	// With --below-floor: admitted, daemon starts, floor lowers to binary minor.
	rollback := exec.CommandContext(ctx, bin, "daemon-start", "--below-floor")
	rollback.Env = env
	rollbackOut, rollbackErr := rollback.CombinedOutput()
	if rollbackErr != nil || !strings.Contains(string(rollbackOut), `"outcome":"started"`) {
		t.Fatalf("daemon-start --below-floor: %v\n%s", rollbackErr, rollbackOut)
	}
	ep, err := wsdashboard.ReadEndpoint(dir)
	if err != nil {
		t.Fatalf("read endpoint: %v", err)
	}
	if _, err := wsdashboard.Health(ctx, dashboardTestClient, ep); err != nil {
		t.Fatalf("rolled-back daemon unhealthy: %v", err)
	}
	floor, exists, err := wsdashboard.ReadMinorFloor(dir)
	if err != nil || !exists {
		t.Fatalf("ReadMinorFloor = (%v, %v, %v)", floor, exists, err)
	}
	if !wsdashboard.SameMinor(floor.String()+".0", version) {
		t.Fatalf("floor after rollback = %s, want minor of %s", floor, version)
	}
}
