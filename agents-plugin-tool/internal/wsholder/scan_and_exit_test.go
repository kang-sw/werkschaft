package wsholder

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeHolderServer speaks the minimal holder attach + replay protocol over TCP
// so tests can control disconnects, protocol exit frames, and attach stalls
// deterministically without spawning Python subprocesses.
type fakeHolderServer struct {
	t     *testing.T
	ln    net.Listener
	desc  Descriptor
	dir   string
	token string

	mu         sync.Mutex
	conns      []net.Conn
	attachGate chan struct{} // when non-nil, Attach blocks until closed
	sendExit   bool          // when true, Replay emits a protocol "exit" frame
}

func startFakeHolderServer(t *testing.T, holdersDir, id string, pid int, attachGate chan struct{}, sendExit bool) *fakeHolderServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	token := "tok-" + id
	desc := Descriptor{
		Schema:      1,
		ID:          id,
		PID:         pid,
		Port:        ln.Addr().(*net.TCPAddr).Port,
		Token:       token,
		Protocol:    protocolVersion,
		StartedAt:   float64(time.Now().Unix()),
		RestartSafe: true,
	}
	dir := filepath.Join(holdersDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		ln.Close()
		t.Fatal(err)
	}
	raw, err := json.Marshal(desc)
	if err != nil {
		ln.Close()
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, descriptorName), raw, 0o600); err != nil {
		ln.Close()
		t.Fatal(err)
	}
	s := &fakeHolderServer{
		t:          t,
		ln:         ln,
		desc:       desc,
		dir:        dir,
		token:      token,
		attachGate: attachGate,
		sendExit:   sendExit,
	}
	go s.serve()
	t.Cleanup(func() { s.closeAll() })
	return s
}

func (s *fakeHolderServer) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, c)
		s.mu.Unlock()
		go s.handleConn(c)
	}
}

func (s *fakeHolderServer) handleConn(c net.Conn) {
	defer c.Close()
	if s.attachGate != nil {
		<-s.attachGate
	}
	enc := json.NewEncoder(c)
	if err := enc.Encode(map[string]any{
		"hello":    "ws-holder",
		"protocol": protocolVersion,
	}); err != nil {
		return
	}
	sc := bufio.NewScanner(c)
	for sc.Scan() {
		var req struct {
			Op string `json:"op"`
			ID int64  `json:"id"`
		}
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			return
		}
		switch req.Op {
		case "attach":
			_ = enc.Encode(map[string]any{
				"reply":     req.ID,
				"ok":        true,
				"protocol":  protocolVersion,
				"child":     map[string]any{"state": "running", "pid": s.desc.PID},
				"first_seq": 0,
				"next_seq":  0,
			})
		case "replay":
			if s.sendExit {
				code := 0
				_ = enc.Encode(map[string]any{
					"event":     "exit",
					"seq":       1,
					"exit_code": code,
				})
				_ = enc.Encode(map[string]any{
					"reply":     req.ID,
					"ok":        true,
					"first_seq": 1,
					"next_seq":  2,
				})
			} else {
				_ = enc.Encode(map[string]any{
					"reply":     req.ID,
					"ok":        true,
					"first_seq": 0,
					"next_seq":  0,
				})
			}
		default:
			_ = enc.Encode(map[string]any{"reply": req.ID, "ok": true})
		}
	}
}

func (s *fakeHolderServer) dropConnections() {
	s.mu.Lock()
	conns := append([]net.Conn(nil), s.conns...)
	s.conns = nil
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

func (s *fakeHolderServer) closeAll() {
	_ = s.ln.Close()
	s.dropConnections()
}

func countExitEvents(c *collector, id string) int {
	n := 0
	for _, ev := range c.snapshot(id) {
		if ev.Event == "exit" {
			n++
		}
	}
	return n
}

// TestManagerScanParallelStalledHolderDoesNotBlockHealthy verifies that
// Manager.Scan processes holder directories concurrently so one stalled attach
// cannot block attaching all healthy holders or cleaning up dead ones, and
// runs race-free under -race with a non-thread-safe Log writer.
func TestManagerScanParallelStalledHolderDoesNotBlockHealthy(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DirName)
	gate := make(chan struct{})
	var closeGate sync.Once
	unlockGate := func() { closeGate.Do(func() { close(gate) }) }
	defer unlockGate()

	// One holder stalls during attach handshake.
	stalled := startFakeHolderServer(t, dir, "00-stalled", 9001, gate, false)
	// Four healthy holders answer immediately.
	healthyIDs := []string{"01-healthy", "02-healthy", "03-healthy", "04-healthy"}
	for i, id := range healthyIDs {
		startFakeHolderServer(t, dir, id, 9100+i, nil, false)
	}
	// Two dead holders should be cleaned up concurrently.
	deadIDs := []string{"05-dead", "06-dead"}
	for i, id := range deadIDs {
		s := startFakeHolderServer(t, dir, id, 9200+i, nil, false)
		s.closeAll()
	}

	var logBuf bytes.Buffer
	m := New(Options{
		Dir: dir,
		Log: &logBuf,
		alive: func(pid int) bool {
			return pid < 9200
		},
	})
	defer m.Close()

	scanDone := make(chan struct{})
	go func() {
		m.Scan()
		close(scanDone)
	}()

	// All 4 healthy holders must attach while the stalled holder is still gated.
	waitFor(t, "all healthy holders attached while stalled holder is blocked", func() bool {
		for _, id := range healthyIDs {
			if m.Client(id) == nil {
				return false
			}
		}
		return true
	})
	if m.Client(stalled.desc.ID) != nil {
		t.Fatal("stalled holder unexpectedly attached before gate opened")
	}

	// Unblock stalled holder so Scan finishes cleanly.
	unlockGate()
	select {
	case <-scanDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Scan did not finish after stalled holder unblocked")
	}
	if m.Client(stalled.desc.ID) == nil {
		t.Fatal("stalled holder did not attach after gate opened")
	}
	for _, id := range deadIDs {
		if _, err := os.Stat(filepath.Join(dir, id)); !os.IsNotExist(err) {
			t.Fatalf("dead holder %s directory still exists: %v", id, err)
		}
	}
}

// TestManagerAbruptHolderDeathEmitsSyntheticExitPromptly verifies that when an
// attached holder dies abruptly without sending a protocol "exit" frame,
// Manager detects the dead PID on socket drop and emits a synthetic "exit"
// promptly without waiting for the 3-minute rescan.
func TestManagerAbruptHolderDeathEmitsSyntheticExitPromptly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DirName)
	var alive atomic.Bool
	alive.Store(true)

	srv := startFakeHolderServer(t, dir, "abrupt-dead-1", 44101, nil, false)
	col := newCollector()
	m := New(Options{
		Dir:            dir,
		Sink:           col,
		RescanInterval: time.Hour,
		alive:          func(int) bool { return alive.Load() },
	})
	defer m.Close()

	m.Scan()
	if m.Client(srv.desc.ID) == nil {
		t.Fatal("holder did not attach")
	}

	// Simulate abrupt holder process death without sending any "exit" frame.
	alive.Store(false)
	srv.closeAll()

	waitFor(t, "synthetic exit event on abrupt holder death", func() bool {
		return countExitEvents(col, srv.desc.ID) == 1
	})
	if _, err := os.Stat(srv.dir); !os.IsNotExist(err) {
		t.Fatalf("dead holder directory still exists after cleanup: %v", err)
	}
	// A subsequent Scan must not emit a duplicate exit.
	m.Scan()
	if got := countExitEvents(col, srv.desc.ID); got != 1 {
		t.Fatalf("exit events after follow-up scan = %d, want 1", got)
	}
}

// TestManagerLiveHolderDisconnectDoesNotEmitSyntheticExit verifies that a
// transient TCP disconnect on a live holder leaves it detached and
// reattachable without emitting any synthetic exit.
func TestManagerLiveHolderDisconnectDoesNotEmitSyntheticExit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DirName)
	srv := startFakeHolderServer(t, dir, "live-drop-1", 44102, nil, false)
	col := newCollector()
	m := New(Options{
		Dir:            dir,
		Sink:           col,
		RescanInterval: time.Hour,
		alive:          func(int) bool { return true },
	})
	defer m.Close()

	m.Scan()
	if m.Client(srv.desc.ID) == nil {
		t.Fatal("holder did not attach")
	}

	// Drop only the TCP connection while the holder PID remains alive.
	srv.dropConnections()
	waitFor(t, "client detached after drop", func() bool {
		return m.Client(srv.desc.ID) == nil
	})
	time.Sleep(200 * time.Millisecond)

	if got := countExitEvents(col, srv.desc.ID); got != 0 {
		t.Fatalf("live holder disconnect emitted %d synthetic exit(s), want 0", got)
	}
	if _, err := os.Stat(srv.dir); err != nil {
		t.Fatalf("live holder directory was removed: %v", err)
	}
	// Reattaching on the next scan succeeds cleanly.
	m.Scan()
	if m.Client(srv.desc.ID) == nil {
		t.Fatal("live holder failed to reattach on follow-up scan")
	}
}

// TestManagerCloseDoesNotEmitSyntheticExit verifies that Manager.Close during
// daemon shutdown/replacement never emits synthetic exits.
func TestManagerCloseDoesNotEmitSyntheticExit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DirName)
	var alive atomic.Bool
	alive.Store(true)

	srv := startFakeHolderServer(t, dir, "close-safe-1", 44103, nil, false)
	col := newCollector()
	m := New(Options{
		Dir:            dir,
		Sink:           col,
		RescanInterval: time.Hour,
		alive:          func(int) bool { return alive.Load() },
	})

	m.Scan()
	if m.Client(srv.desc.ID) == nil {
		t.Fatal("holder did not attach")
	}

	// Close the manager (daemon replace); even if the PID check turns false,
	// Close must never synthesize an exit or delete the holder's directory.
	alive.Store(false)
	m.Close()
	time.Sleep(150 * time.Millisecond)

	if got := countExitEvents(col, srv.desc.ID); got != 0 {
		t.Fatalf("Manager.Close emitted %d synthetic exit(s), want 0", got)
	}
	if _, err := os.Stat(srv.dir); err != nil {
		t.Fatalf("Manager.Close removed holder directory: %v", err)
	}
}

// TestManagerProtocolExitFollowedByCleanupDoesNotDoubleEmit verifies at-most-
// once exit delivery when a normal protocol "exit" frame arrives before the
// holder process exits and cleanup runs.
func TestManagerProtocolExitFollowedByCleanupDoesNotDoubleEmit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DirName)
	var alive atomic.Bool
	alive.Store(true)

	srv := startFakeHolderServer(t, dir, "proto-exit-1", 44104, nil, true)
	col := newCollector()
	m := New(Options{
		Dir:            dir,
		Sink:           col,
		RescanInterval: time.Hour,
		alive:          func(int) bool { return alive.Load() },
	})
	defer m.Close()

	m.Scan()
	waitFor(t, "protocol exit frame delivered", func() bool {
		return countExitEvents(col, srv.desc.ID) == 1
	})

	// Holder exits after delivering its protocol "exit" frame.
	alive.Store(false)
	srv.closeAll()

	waitFor(t, "dead holder directory removed", func() bool {
		_, err := os.Stat(srv.dir)
		return os.IsNotExist(err)
	})
	m.Scan()
	time.Sleep(50 * time.Millisecond)

	if got := countExitEvents(col, srv.desc.ID); got != 1 {
		t.Fatalf("total exit events = %d, want 1 (must not double-emit): %s", got, fmt.Sprint(col.snapshot(srv.desc.ID)))
	}
}
