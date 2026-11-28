package mcp

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wsstate"
)

func TestPokedIdentityCollisionSuppressesBothTargets(t *testing.T) {
	known := map[string]nudgeKeyIdentity{
		"old": {Scope: string(roleLead)},
		"new": {Scope: string(roleLead)},
	}
	// Synthetic hash collision avoids searching the four-word ReplyID space.
	replyID := func(string) string { return "collision" }
	for _, key := range []string{"old", "new"} {
		if nudgeIdentityUnique(key, known, replyID) {
			t.Fatalf("colliding %s target remained eligible", key)
		}
	}
	calls := 0
	counts := nudgeAssociationCounts(known, func(string) string { calls++; return "collision" })
	if counts["collision"] != 2 || calls != len(known) {
		t.Fatalf("association pass: counts=%v hashes=%d, want one hash per identity", counts, calls)
	}
	delete(known, "new")
	if !nudgeIdentityUnique("old", known, replyID) {
		t.Fatal("recovered unique target remained suppressed")
	}
}

func TestWhiteboardPokedDiscoveryIsConstantIO(t *testing.T) {
	f := newNudgeFixture(t)
	for i := 0; i < 200; i++ {
		nudgeWriteJSON(t, f.n.dirs().partPath(fmt.Sprintf("unrelated-%d", i), partKey), f.record)
	}
	path := f.n.dirs().partPath(f.target.key, partKey)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	f.n.discover()
	before := wsstate.FileIOCounters()
	if !f.n.discoverTarget(f.target) {
		t.Fatal("poked board missing")
	}
	after := wsstate.FileIOCounters()
	if got := after["file_io_stat"] - before["file_io_stat"]; got != 1 {
		t.Fatalf("poked discovery used %d stats, want identity only", got)
	}
	if got := after["file_io_read_dir"] - before["file_io_read_dir"]; got != 0 {
		t.Fatalf("poke enumerated keys: %d directory reads", got)
	}
}

type countingPiAgents struct {
	*fakePiAgents
	gets atomic.Int64
}

func (c *countingPiAgents) Get(id string) (wsagent.State, error) {
	c.gets.Add(1)
	return c.fakePiAgents.Get(id)
}

func siteDelta(before, after map[string]int64, substr string) int64 {
	var total int64
	for k, v := range after {
		if strings.Contains(k, substr) {
			total += v - before[k]
		}
	}
	return total
}

func TestWhiteboardProcessBoardLazyDeliveryGateZeroIO(t *testing.T) {
	f := newNudgeFixture(t)
	_, fake := f.hosted(t)
	agents := &countingPiAgents{fakePiAgents: fake}
	f.n.agents = agents

	// 1. Absent board: zero identity(), ReadRegistration, or agents.Get calls.
	absent := nudgeTarget{key: "absent-board-lazy-gate", root: f.target.root}
	nudgeWriteJSON(t, f.n.dirs().partPath(absent.key, partKey), f.record)
	absentRow := f.row
	absentRow.ReplyID = wsmailbox.ReplyID(absent.key)
	if err := wsdashboard.WriteRegistration(f.n.dir, absentRow); err != nil {
		t.Fatal(err)
	}
	before := wsstate.FileIOCounters()
	next, presence := f.n.processBoard(absent, nudgeNow.Add(8*time.Hour))
	after := wsstate.FileIOCounters()
	if presence != nudgeAbsent || !next.IsZero() {
		t.Fatalf("absent processBoard = (%v, %v), want (zero, nudgeAbsent)", next, presence)
	}
	if got := agents.gets.Load(); got != 0 {
		t.Fatalf("absent board called agents.Get %d times, want 0", got)
	}
	if got := siteDelta(before, after, "wsdashboard/registry.go:"); got != 0 {
		t.Fatalf("absent board triggered %d registry.go file ops, want 0", got)
	}
	if got := siteDelta(before, after, "mcp/whiteboard_nudges.go:"); got != 0 {
		t.Fatalf("absent board triggered %d whiteboard_nudges.go file ops, want 0", got)
	}

	// 2. Present but not-due board: PeekNotify returns exact next deadline with
	// zero identity(), ReadRegistration, or agents.Get I/O.
	before = wsstate.FileIOCounters()
	next, presence = f.n.processBoard(f.target, nudgeNow.Add(30*time.Second))
	after = wsstate.FileIOCounters()
	wantNext := nudgeNow.Add(time.Minute)
	if presence != nudgePresent || !next.Equal(wantNext) {
		t.Fatalf("not-due processBoard = (%v, %v), want (%v, nudgePresent)", next, presence, wantNext)
	}
	if got := agents.gets.Load(); got != 0 {
		t.Fatalf("not-due board called agents.Get %d times, want 0", got)
	}
	if got := after["file_io_stat"] - before["file_io_stat"]; got != 0 {
		t.Fatalf("not-due board used %d stats, want 0", got)
	}
	if got := after["file_io_read_file"] - before["file_io_read_file"]; got != 0 {
		t.Fatalf("not-due board used %d file reads, want 0", got)
	}

	// 3. Due board: constructs deliveryGate outside board lock (safe even while
	// sessionStore.mu is held) and enqueues the nudge via Notify.
	store := newSessionStore()
	store.mu.Lock()
	defer store.mu.Unlock()
	_, presence = f.n.processBoard(f.target, nudgeNow.Add(2*time.Minute))
	if presence != nudgePresent {
		t.Fatalf("due board presence = %v, want nudgePresent", presence)
	}
	if got := agents.gets.Load(); got != 1 {
		t.Fatalf("due board called agents.Get %d times, want 1", got)
	}
	if got := len(f.queue(t)); got != 1 {
		t.Fatalf("due board enqueued %d messages, want 1", got)
	}
}
