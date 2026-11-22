package wsholder

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// auditChild writes kept and ring-skipped lines, then waits for its input
// to close, so the connection outlives the output. The reconfigure keeps
// Python's text-mode stdout from turning each "\n" into "\r\n" on Windows:
// the holder relays the child's bytes as written, and the test asserts LF.
const auditChild = `import sys
sys.stdout.reconfigure(newline="\n")
sys.stdout.write("keep 1\ndrop a\ndrop b\nkeep 2\n")
sys.stdout.flush()
sys.stdin.read()
`

// runAuditHolder launches a ring-skipping holder under a manager with the
// audit channel offered or not, runs auditChild to exit, and returns the
// holder's events.
func runAuditHolder(t *testing.T, offer bool) []Event {
	t.Helper()
	dir := testHoldersDir(t)
	sink := newCollector()
	m := testManager(t, dir, sink, func(o *Options) { o.AuditChannel = offer })
	id, err := m.LaunchWith(LaunchSpec{RingSkip: "^drop"})
	if err != nil {
		t.Fatalf("LaunchWith: %v", err)
	}
	if _, err := m.Client(id).Spawn(SpawnSpec{Argv: []string{testPython(t), "-c", auditChild}, Mode: "pipe"}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	waitFor(t, "the kept lines", func() bool { return strings.Contains(sink.output(id), "keep 2") })
	if err := m.Client(id).Input(nil, true); err != nil {
		t.Fatalf("close input: %v", err)
	}
	waitFor(t, "the exit record", func() bool {
		for _, ev := range sink.snapshot(id) {
			if ev.Event == "exit" {
				return true
			}
		}
		return false
	})
	if err := m.Stop(id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if offer && len(auditEvents(sink.snapshot(id))) > 0 {
		waitFor(t, "the detach marker", func() bool {
			evs := sink.snapshot(id)
			return len(evs) > 0 && evs[len(evs)-1].Event == EventAuditDetached
		})
	}
	if strings.Contains(sink.output(id), "drop") {
		t.Fatalf("a skipped line reached the record stream: %q", sink.output(id))
	}
	return sink.snapshot(id)
}

func auditEvents(evs []Event) []Event {
	var out []Event
	for _, ev := range evs {
		if strings.HasPrefix(ev.Event, "audit") {
			out = append(out, ev)
		}
	}
	return out
}

// TestAuditChannelTeesSkippedLinesToANegotiatingDaemon pins the new-daemon,
// new-holder direction: the attach negotiates the channel, the skipped
// lines arrive as audit events anchored on the kept line before them, each
// kept run is fenced, and the client marks the connection's start and end.
func TestAuditChannelTeesSkippedLinesToANegotiatingDaemon(t *testing.T) {
	evs := runAuditHolder(t, true)
	audit := auditEvents(evs)
	if len(audit) == 0 || audit[0].Event != EventAuditAttached || audit[len(audit)-1].Event != EventAuditDetached {
		t.Fatalf("audit events = %+v, want them between the attach and detach markers", audit)
	}
	var keep1 uint64
	for _, ev := range evs {
		if ev.Event == "output" && bytes.HasPrefix(ev.Data, []byte("keep 1")) {
			keep1 = ev.Seq
		}
	}
	var lines []string
	for _, ev := range audit {
		if ev.Event == EventAudit {
			lines = append(lines, string(ev.Data))
			if ev.Anchor != keep1 || ev.At <= 0 || ev.Seq != 0 {
				t.Fatalf("tee %+v: want anchor %d, a read time, and no sequence", ev, keep1)
			}
		}
	}
	if strings.Join(lines, "") != "drop a\ndrop b\n" {
		t.Fatalf("teed lines = %q", lines)
	}
	var fenced bool
	for _, ev := range audit {
		if ev.Event == EventAuditFence && ev.Anchor == keep1 {
			fenced = ev.Sent == 2 && ev.Dropped == 0 && ev.Next > keep1
		}
	}
	if !fenced {
		t.Fatalf("no fence closed the interval after %d with two sent: %+v", keep1, audit)
	}
}

// TestAuditChannelOldHolderAttachesWithoutIt pins the new-daemon,
// old-holder direction against the shipped holder that predates the channel
// (testdata/holder-pre-audit.py, main's holder.py when the channel was
// added): it ignores the offer, the attach succeeds as before, and no audit
// event (not even the client's markers) reaches the sinks.
func TestAuditChannelOldHolderAttachesWithoutIt(t *testing.T) {
	old, err := os.ReadFile("testdata/holder-pre-audit.py")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(old, []byte(`"audit"`)) {
		t.Fatal("the stand-in for an old holder knows the audit channel")
	}
	current := script
	script = old
	t.Cleanup(func() { script = current })
	evs := runAuditHolder(t, true)
	if audit := auditEvents(evs); len(audit) != 0 {
		t.Fatalf("an old holder produced audit events: %+v", audit)
	}
}

// TestAuditChannelNewHolderSendsNothingToAnOldDaemon pins the old-daemon,
// new-holder direction: an attach that offers no channel (an older daemon's)
// receives no audit message.
func TestAuditChannelNewHolderSendsNothingToAnOldDaemon(t *testing.T) {
	evs := runAuditHolder(t, false)
	if audit := auditEvents(evs); len(audit) != 0 {
		t.Fatalf("a daemon that offered no channel received audit events: %+v", audit)
	}
}
