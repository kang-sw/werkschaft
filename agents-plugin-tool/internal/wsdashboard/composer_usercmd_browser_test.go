package wsdashboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsusercmd"
)

// TestUserCommandBrowser drives the user commands' strip in Chromium against the
// real session page and routes (only the launch service is a fake): the slot
// buttons and a slot-run card in ko and en, the strip's refresh by the opaque
// revision event with no polling, a stale click that runs nothing, the remove
// control, a command named "constructor", a double click that runs once, an
// older removal answer that lands after a newer list (or one from another
// daemon epoch) and must not replace it, a failed read that leaves the strip and
// still answers a pending wake, and the strip's absence from the shared
// transcript. The script asks
// this test to change the registry as the agent would (a signal file per step).
func TestUserCommandBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Chromium acceptance")
	}
	shots := os.Getenv("WS_USERCMD_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_USERCMD_SHOTS must be absolute")
	}
	f := startUserCommandFixture(t)
	signals := t.TempDir()
	set := func(id, label, desc, line string) {
		if _, _, err := f.users.reg.Set("livesession", f.users.root, wsusercmd.Input{ID: id, Label: label, Description: desc, Line: line}); err != nil {
			t.Error(err)
		}
	}
	steps := map[string]func(){
		"register": func() {
			set("build", "Build", "compile everything", "make all")
			set("test", "Test", "run the suite", "make test\n./run --verbose")
		},
		"replace-build": func() { set("build", "Build", "compile everything", "make clean all") },
		"hold-slot": func() {
			if _, _, err := f.users.reg.Set("livesession", f.users.root, wsusercmd.Input{
				ID: "hold", Label: "Hold", Line: "echo old", Prompt: "  Keep the first line.\r\nAnd the second.  ",
			}); err != nil {
				t.Error(err)
			}
		},
		"hold-run":    func() { set("hold", "Hold", "", "echo changed run") },
		"hold-ask":    func() { set("hold", "Hold", "", "echo changed ask") },
		"hold-delete": func() { set("hold", "Hold", "", "echo changed delete") },
		"unregister-test": func() {
			if err := f.users.reg.Remove("livesession", f.users.root, "test"); err != nil {
				t.Error(err)
			}
		},
		"register-extra": func() {
			set("constructor", "Constructor", "named like an Object property", "echo ctor")
			set("dbl", "Double", "clicked twice", "echo dbl")
		},
		"put-x":         func() { set("x", "X", "", "echo x") },
		"put-y":         func() { set("y", "Y", "", "echo y") },
		"put-z":         func() { set("z", "Z", "", "echo z") },
		"put-w":         func() { set("w", "W", "", "echo w") },
		"register-late": func() { set("late", "Late", "registered after a removal", "echo late") },
		"reset": func() {
			for _, c := range f.users.reg.List("livesession", f.users.root) {
				_ = f.users.reg.Remove("livesession", f.users.root, c.ID)
			}
		},
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		seen := map[string]bool{}
		for {
			select {
			case <-stop:
				return
			case <-time.After(30 * time.Millisecond):
			}
			entries, _ := os.ReadDir(signals)
			for _, e := range entries {
				name := e.Name()
				if !strings.HasPrefix(name, "do-") || seen[name] {
					continue
				}
				seen[name] = true
				step := strings.TrimPrefix(name, "do-")
				if i := strings.Index(step, "."); i >= 0 {
					step = step[:i]
				}
				if fn := steps[step]; fn != nil {
					fn()
				}
				_ = os.WriteFile(filepath.Join(signals, "ack-"+strings.TrimPrefix(name, "do-")), nil, 0o600)
			}
		}
	}()
	defer func() { close(stop); <-done }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	script := "testdata/composer-usercmd.browser.cjs"
	out, err := exec.CommandContext(ctx, "node", script, f.base, signals, shots).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium user command acceptance: %v", err)
	}
	f.svc.mu.Lock()
	defer f.svc.mu.Unlock()
	runs := map[string]int{}
	for _, l := range f.svc.launches {
		runs[l.Line]++
	}
	// Each confirmed command launches once per language.
	if runs["make all"] != 2 || runs["echo ctor"] != 2 || runs["echo dbl"] != 2 || len(f.svc.launches) != 6 {
		t.Fatalf("launches %v, want one of each confirmed command per language", runs)
	}
}
