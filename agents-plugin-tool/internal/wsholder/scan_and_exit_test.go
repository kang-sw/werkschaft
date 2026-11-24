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
	onStop     func()        // optional callback invoked on protocol "stop"
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
		case "stop":
			_ = enc.Encode(map[string]any{"reply": req.ID, "ok": true})
			if s.onStop != nil {
				s.onStop()
			}
			return
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

type blockingSink struct {
	targetID string
	ready    <-chan struct{}
	entered  chan struct{}
	done     chan struct{}
	once     sync.Once

	mu     sync.Mutex
	events []Event
}

func (s *blockingSink) HolderEvent(id string, ev Event) {
	if id != s.targetID {
		return
	}
	s.once.Do(func() {
		if s.entered != nil {
			close(s.entered)
		}
	})
	<-s.ready
	s.mu.Lock()
	s.events = append(s.events, ev)
	s.mu.Unlock()
	if s.done != nil {
		select {
		case <-s.done:
		default:
			close(s.done)
		}
	}
}

func (s *blockingSink) countExits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, ev := range s.events {
		if ev.Event == "exit" {
			n++
		}
	}
	return n
}

// TestManagerScanReusedPIDCleanupDoesNotDeadlockBlockedSink verifies that a
// dead holder whose PID was reused before Scan() does not emit a synchronous
// synthetic exit or deadlock a sink gated on post-scan attachReady admission.
func TestManagerScanReusedPIDCleanupDoesNotDeadlockBlockedSink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DirName)
	id := "reused-pid-1"

	// Reserve a closed port so attach fails immediately with CauseNoAnswer.
	srv := startFakeHolderServer(t, dir, id, 44200, nil, false)
	srv.closeAll()

	attachReady := make(chan struct{})
	var closeReady sync.Once
	unlockReady := func() { closeReady.Do(func() { close(attachReady) }) }
	defer unlockReady()

	sink := &blockingSink{
		targetID: id,
		ready:    attachReady,
	}
	m := New(Options{
		Dir:            dir,
		Sink:           sink,
		RescanInterval: time.Hour,
		alive:          func(int) bool { return true },
		procStart: func(int) (time.Time, error) {
			// Mismatched start time -> ErrStartTimeMismatch in sameProcess.
			return time.Now().Add(-time.Hour), nil
		},
	})
	defer m.Close()

	scanDone := make(chan struct{})
	go func() {
		m.Scan()
		close(scanDone)
	}()

	select {
	case <-scanDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Scan deadlocked on reused-PID cleanup with blocked sink")
	}
	if _, err := os.Stat(srv.dir); !os.IsNotExist(err) {
		t.Fatalf("reused-PID dead holder directory still exists: %v", err)
	}
	unlockReady()
	time.Sleep(50 * time.Millisecond)
	if got := sink.countExits(); got != 0 {
		t.Fatalf("never-attached reused-PID cleanup emitted %d synthetic exit(s), want 0", got)
	}
}

// TestManagerPostAttachDisconnectCleanupDoesNotDeadlockStopOrBlockedSink
// simulates the ABBA lock graph where a holder attaches during Scan() and dies
// abruptly before post-scan admission closes attachReady, while another caller
// invokes Stop() (needing opMu). Cleanup must dispatch the synthetic exit
// outside opMu so neither Scan() nor Stop() deadlocks, and once attachReady
// opens the synthetic exit still settles the consumer.
func TestManagerPostAttachDisconnectCleanupDoesNotDeadlockStopOrBlockedSink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DirName)
	const (
		pidLive = 44201
		pidDead = 44202
	)
	var alive1, alive2 atomic.Bool
	alive1.Store(true)
	alive2.Store(true)

	srv1 := startFakeHolderServer(t, dir, "post-attach-h1", pidLive, nil, false)
	srv1.onStop = func() {
		alive1.Store(false)
		srv1.closeAll()
	}
	srv2 := startFakeHolderServer(t, dir, "post-attach-h2", pidDead, nil, false)

	attachReady := make(chan struct{})
	var closeReady sync.Once
	unlockReady := func() { closeReady.Do(func() { close(attachReady) }) }
	defer unlockReady()

	sink := &blockingSink{
		targetID: srv2.desc.ID,
		ready:    attachReady,
		entered:  make(chan struct{}),
		done:     make(chan struct{}),
	}
	m := New(Options{
		Dir:            dir,
		Sink:           sink,
		RescanInterval: time.Hour,
		alive: func(pid int) bool {
			switch pid {
			case pidLive:
				return alive1.Load()
			case pidDead:
				return alive2.Load()
			default:
				return false
			}
		},
		kill: func(pid int) error {
			if pid == pidLive {
				alive1.Store(false)
				srv1.closeAll()
			}
			return nil
		},
		procStart: func(int) (time.Time, error) {
			return time.Now(), nil
		},
	})
	defer m.Close()

	m.Scan()
	if m.Client(srv1.desc.ID) == nil || m.Client(srv2.desc.ID) == nil {
		t.Fatal("holders did not attach during initial Scan")
	}

	// Kill h2 abruptly while attachReady is still closed (simulating death
	// before holdersScanned reaches h2's conversation).
	alive2.Store(false)
	srv2.closeAll()

	// Wait until handleDisconnect removes h2's dir and the async synthetic exit
	// goroutine is actively blocked inside sink.HolderEvent on <-attachReady.
	waitFor(t, "abruptly dead h2 directory removed", func() bool {
		_, err := os.Stat(srv2.dir)
		return os.IsNotExist(err)
	})
	select {
	case <-sink.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("synthetic exit goroutine did not enter sink")
	}

	// While sink.HolderEvent is still blocked on <-attachReady, Stop(h1) and
	// Scan() must both acquire opMu and complete promptly without deadlocking.
	stopDone := make(chan error, 1)
	go func() {
		stopDone <- m.Stop(srv1.desc.ID)
	}()
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop(h1) failed while h2 sink was blocked: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop(h1) deadlocked on opMu while h2 sink was blocked on attachReady")
	}

	scanDone := make(chan struct{})
	go func() {
		m.Scan()
		close(scanDone)
	}()
	select {
	case <-scanDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Scan() deadlocked on opMu while h2 sink was blocked on attachReady")
	}

	// Opening attachReady unblocks the async synthetic exit cleanly.
	unlockReady()
	select {
	case <-sink.done:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked sink did not finish after attachReady closed")
	}
	if got := sink.countExits(); got != 1 {
		t.Fatalf("synthetic exit count for h2 = %d, want 1", got)
	}
}
