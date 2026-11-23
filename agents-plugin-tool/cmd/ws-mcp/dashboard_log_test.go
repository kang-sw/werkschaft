package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

func readDashboardLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDashboardLogRestartAndLockLoser(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dashboard.log")
	start := func() *wsdashboard.Daemon {
		t.Helper()
		l := openDashboardLog(dir)
		d, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{
			Dir: dir, Log: l, LogUnderLock: func() func() {
				end := l.begin()
				return func() {
					probe := flock.New(filepath.Join(dir, "dashboard.lock"))
					locked, err := probe.TryLock()
					if err != nil {
						t.Error(err)
					}
					if locked {
						_ = probe.Unlock()
						t.Error("log ended after singleton unlock")
					}
					_ = probe.Close()
					end()
				}
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(d.Close)
		return d
	}
	first := start()
	before := readDashboardLog(t, path)
	loser := openDashboardLog(dir)
	var fallback bytes.Buffer
	loser.fallback = &fallback
	fmt.Fprintln(loser, "before lock")
	_, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{
		Dir: dir, Log: loser, LogUnderLock: loser.begin, LockWait: time.Millisecond,
	})
	if !errors.Is(err, wsdashboard.ErrLocked) {
		t.Fatalf("lock loser: %v", err)
	}
	if got := readDashboardLog(t, path); got != before {
		t.Fatal("lock loser changed log")
	}
	if fallback.String() != "before lock\n" {
		t.Fatal("pre-lock diagnostic missing")
	}
	first.Close()
	stopped := readDashboardLog(t, path)
	if !strings.Contains(stopped, "dashboard: stopped") {
		t.Fatal("missing shutdown line")
	}
	second := start()
	second.Close()
	if got := readDashboardLog(t, path); !strings.HasPrefix(got, stopped) {
		t.Fatal("restart lost predecessor lines")
	}
	// Late writes cannot reopen the file after the singleton was released.
	final := readDashboardLog(t, path)
	fmt.Fprintln(loser, "late")
	if got := readDashboardLog(t, path); got != final {
		t.Fatal("late write changed history")
	}
}

func TestDashboardLogRotation(t *testing.T) {
	dir := t.TempDir()
	l := openDashboardLog(dir)
	end := l.begin()
	defer end()
	data := bytes.Repeat([]byte("a"), dashboardLogCap)
	if _, err := l.Write(data); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(l.path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("early rotation: %v", err)
	}
	if _, err := l.Write([]byte("next")); err != nil {
		t.Fatal(err)
	}
	if got := readDashboardLog(t, l.path+".1"); got != string(data) {
		t.Fatal("rotation lost contents")
	}
	if got := readDashboardLog(t, l.path); got != "next" {
		t.Fatalf("active log = %q", got)
	}
	// The next rotation replaces the same retained generation.
	if _, err := l.Write(bytes.Repeat([]byte("b"), dashboardLogCap-4)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write([]byte("third")); err != nil {
		t.Fatal(err)
	}
	if got := readDashboardLog(t, l.path+".1"); len(got) != dashboardLogCap || !strings.HasPrefix(got, "next") {
		t.Fatal("retained generation not replaced")
	}
	if _, err := os.Stat(l.path + ".2"); !os.IsNotExist(err) {
		t.Fatal("extra retained generation")
	}
}

func TestDashboardLogStartupFailureEndsUnderLock(t *testing.T) {
	dir := t.TempDir()
	l := openDashboardLog(dir)
	var fallback bytes.Buffer
	l.fallback = &fallback
	// Exercise the real post-lock failure through executable validation.
	_, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{
		Dir: dir, Executable: filepath.Join(dir, "missing"), Log: l, LogUnderLock: l.begin,
	})
	if err == nil {
		t.Fatal("expected startup failure")
	}
	if l.file != nil {
		t.Fatal("startup failure left log open")
	}
	before := readDashboardLog(t, l.path)
	fmt.Fprintln(l, "late failure")
	if got := readDashboardLog(t, l.path); got != before {
		t.Fatal("late failure wrote after unlock")
	}
	if !strings.Contains(fallback.String(), "late failure") {
		t.Fatal("late diagnostic missing")
	}
}
