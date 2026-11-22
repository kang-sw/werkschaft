package wsholder

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// exitEvent is id's exit record once it has arrived.
func exitEvent(c *collector, id string) (Event, bool) {
	for _, ev := range c.snapshot(id) {
		if ev.Event == "exit" {
			return ev, true
		}
	}
	return Event{}, false
}

// TestLaunchExitCopySurvivesCleanup pins LaunchSpec.ExitCopy: the holder
// writes its exit record to the copy as well as to exit.json, and the
// manager's cleanup after Stop removes the holder directory but leaves the
// copy, so a terminal status outlives a daemon that discards exit.json.
func TestLaunchExitCopySurvivesCleanup(t *testing.T) {
	dir := testHoldersDir(t)
	sink := newCollector()
	var mu sync.Mutex
	var persisted []Exit
	m := testManager(t, dir, sink, func(o *Options) {
		o.OnExit = func(_ Descriptor, exit Exit) error {
			mu.Lock()
			persisted = append(persisted, exit)
			mu.Unlock()
			return nil
		}
	})
	copyPath := filepath.Join(t.TempDir(), "exec-exits", "job.json")
	id, err := m.LaunchWith(LaunchSpec{Session: json.RawMessage(`{"kind":"test"}`), ExitCopy: copyPath})
	if err != nil {
		t.Fatalf("LaunchWith: %v", err)
	}
	if _, err := m.Client(id).Spawn(SpawnSpec{Argv: []string{testPython(t), "-c", "import sys; sys.exit(4)"}, Mode: "pipe"}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	waitFor(t, "the exit record", func() bool { _, ok := exitEvent(sink, id); return ok })
	if _, err := os.Stat(copyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the copy exists before the holder exits: %v", err)
	}
	if err := m.Stop(id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the holder directory remains after cleanup: %v", err)
	}
	raw, err := os.ReadFile(copyPath)
	if err != nil {
		t.Fatalf("the exit copy did not survive cleanup: %v", err)
	}
	var copied Exit
	if err := json.Unmarshal(raw, &copied); err != nil {
		t.Fatalf("exit copy %q: %v", raw, err)
	}
	if copied.ExitCode == nil || *copied.ExitCode != 4 || copied.Signal != nil || copied.Reason != "child_exited" || copied.StoppedAt == "" {
		t.Fatalf("exit copy = %s, want exit 4, no signal, reason child_exited, a stop time", raw)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(persisted) != 1 {
		t.Fatalf("cleanup persisted %d exits, want 1", len(persisted))
	}
	got := persisted[0]
	if got.ExitCode == nil || *got.ExitCode != *copied.ExitCode || got.Reason != copied.Reason || got.StoppedAt != copied.StoppedAt {
		t.Fatalf("exit.json = %+v, copy = %s: want the same record", got, raw)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(copyPath); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("exit copy mode = %v (%v), want 0600", info.Mode().Perm(), err)
		}
		if info, err := os.Stat(filepath.Dir(copyPath)); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("exit copy directory mode = %v (%v), want 0700", info.Mode().Perm(), err)
		}
	}
}

// readToEOF is a pipe child that reads its whole stdin, prints the byte
// count and the first line, and exits 0 only once stdin has closed.
const readToEOF = `
import sys
data = sys.stdin.buffer.read()
# Binary write: text-mode stdout turns "\n" into "\r\n" on Windows.
sys.stdout.buffer.write(("len=%d first=%s\n" % (len(data), data.split(b"\n", 1)[0].decode())).encode())
sys.stdout.flush()
`

// TestSpawnDeliversFiniteInput pins SpawnSpec.Input and InputEOF: a child
// that reads to EOF finishes with no later Input call, for input inside the
// spawn request, for input longer than one chunk, and for EOF alone; and a
// child that exits before reading long input still reports its start.
func TestSpawnDeliversFiniteInput(t *testing.T) {
	dir := testHoldersDir(t)
	sink := newCollector()
	m := testManager(t, dir, sink)
	python := testPython(t)
	long := []byte("long\n" + strings.Repeat("x", inputChunk*2+123))
	// Multibyte text whose 256 KiB marks fall inside runes.
	wide := []byte("wide\n" + strings.Repeat("가é😀", inputChunk/4))
	for _, tc := range []struct {
		name  string
		code  string
		input []byte
		want  string
		exit  int
	}{
		{"in the spawn", readToEOF, []byte("alpha\nbeta\n"), "len=11 first=alpha\n", 0},
		{"longer than a chunk", readToEOF, long, "len=" + strconv.Itoa(len(long)) + " first=long\n", 0},
		{"multibyte across chunks", readToEOF, wide, "len=" + strconv.Itoa(len(wide)) + " first=wide\n", 0},
		{"eof alone", readToEOF, nil, "len=0 first=\n", 0},
		{"exits before reading", "import sys; sys.exit(3)", long, "", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, err := m.Launch(json.RawMessage(`{"kind":"test"}`))
			if err != nil {
				t.Fatalf("Launch: %v", err)
			}
			child, err := m.Client(id).Spawn(SpawnSpec{Argv: []string{python, "-c", tc.code}, Mode: "pipe", Input: tc.input, InputEOF: true})
			if err != nil {
				t.Fatalf("spawn: %v", err)
			}
			if child.State != "running" || child.PID <= 0 {
				t.Fatalf("spawned child = %+v, want it running", child)
			}
			waitFor(t, "the exit record", func() bool { _, ok := exitEvent(sink, id); return ok })
			ev, _ := exitEvent(sink, id)
			if ev.ExitCode == nil || *ev.ExitCode != tc.exit {
				t.Fatalf("exit record = %+v, want exit %d", ev, tc.exit)
			}
			if got := sink.output(id); got != tc.want {
				t.Fatalf("output = %q, want %q", got, tc.want)
			}
			if err := m.Stop(id); err != nil {
				t.Fatalf("Stop: %v", err)
			}
		})
	}
}

// TestInputReportsWhatTheHolderTook pins InputError: input to a child that
// does not read fills the holder's unread-input limit partway through a
// multi-chunk Input, and the error says how many bytes the holder queued
// and that it refused the rest, so a retry resends only those. For
// multibyte text the count ends between runes, so the rest is still text.
func TestInputReportsWhatTheHolderTook(t *testing.T) {
	t.Run("binary", func(t *testing.T) {
		data := make([]byte, 9<<20)
		accepted := inputUntilRefused(t, data)
		if accepted%inputChunk != 0 {
			t.Fatalf("accepted = %d, want whole chunks", accepted)
		}
	})
	t.Run("multibyte text", func(t *testing.T) {
		data := []byte(strings.Repeat("가é😀a", 1<<20))
		accepted := inputUntilRefused(t, data)
		if !utf8.Valid(data[:accepted]) || !utf8.Valid(data[accepted:]) {
			t.Fatalf("accepted = %d splits a rune", accepted)
		}
	})
}

func inputUntilRefused(t *testing.T, data []byte) int {
	t.Helper()
	dir := testHoldersDir(t)
	m := testManager(t, dir, newCollector())
	id, err := m.Launch(json.RawMessage(`{"kind":"test"}`))
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(id) })
	if _, err := m.Client(id).Spawn(SpawnSpec{Argv: []string{testPython(t), "-c", "import time; time.sleep(60)"}, Mode: "pipe"}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	err = m.Client(id).Input(data, true)
	var inErr *InputError
	if !errors.As(err, &inErr) {
		t.Fatalf("Input over the limit = %v, want an *InputError", err)
	}
	var opErr *OpError
	if !inErr.Refused || !errors.As(err, &opErr) || opErr.Code != "input_overflow" {
		t.Fatalf("Input error = %+v, want a refusal for input_overflow", inErr)
	}
	if inErr.Accepted <= 0 || inErr.Accepted >= len(data) {
		t.Fatalf("accepted = %d of %d, want part of it", inErr.Accepted, len(data))
	}
	return inErr.Accepted
}

// TestChunkEndKeepsRunesWhole: a chunk of valid UTF-8 ends at a rune start
// whichever byte of a 2-, 3- or 4-byte rune the 256 KiB mark falls on;
// other data is cut at the mark.
func TestChunkEndKeepsRunesWhole(t *testing.T) {
	for _, r := range []string{"é", "가", "😀"} {
		for shift := 0; shift < len(r); shift++ {
			data := []byte(strings.Repeat("a", shift) + strings.Repeat(r, inputChunk/len(r)+2))
			n := chunkEnd(data, true)
			if n > inputChunk || n <= inputChunk-utf8.UTFMax || !utf8.Valid(data[:n]) || !utf8.Valid(data[n:]) {
				t.Fatalf("%q shifted %d: chunk ends at %d", r, shift, n)
			}
		}
	}
	if n := chunkEnd([]byte(strings.Repeat("가", inputChunk)), false); n != inputChunk {
		t.Fatalf("non-text chunk ends at %d", n)
	}
	if n := chunkEnd([]byte("short"), true); n != 5 {
		t.Fatalf("short data chunk ends at %d", n)
	}
}
