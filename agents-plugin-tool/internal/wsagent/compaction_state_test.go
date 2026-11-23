package wsagent

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// compactRingTriggers are the triggers of the compaction entries that landed.
func compactRingTriggers(h *leadHarness, id string) []string {
	var out []string
	for _, e := range h.ring(id) {
		if e.Kind == wslive.RingCompact && !e.CompactFailed {
			out = append(out, e.Trigger)
		}
	}
	return out
}

// A compaction ws requested is recorded with that request's origin even though
// the harness reports its own trigger for the command that ran it, and the
// runner's state shows the compaction queued/running, then its count and
// latest origin.
func TestCompactionStateAndOriginAttribution(t *testing.T) {
	h := compactionHarness(t)
	id, holder := h.runLead("one")
	key := h.record(id).WSKey
	h.holders.out(holder, turnLines("sess-1", "u1", "first")...)
	h.idle(id)
	if st := h.state(id); st.Compacting || st.Compactions != 0 || st.CompactOrigin != "" {
		t.Fatalf("fresh state: %+v", st)
	}

	// ws auto: the harness reports "manual" for the /compact it was sent.
	h.runner.conv(id).do(func() {
		c := h.runner.conv(id)
		c.compactPending = &compactRequest{auto: true}
		c.publishCompaction(false)
		c.kick()
	})
	if !h.state(id).Compacting {
		t.Fatal("a queued auto compaction is not shown as compacting")
	}
	eventually(t, "auto compact dispatched", func() bool {
		return strings.Join(h.userInputs(holder), "|") != "" && countCompactInputs(h.userInputs(holder)) == 1
	})
	if !h.state(id).Compacting {
		t.Fatal("a running compaction is not shown as compacting")
	}
	compactBoundary(h, id, holder)
	h.idle(id)
	st := h.state(id)
	if st.Compacting || st.Compactions != 1 || st.CompactOrigin != wslive.CompactOriginAuto {
		t.Fatalf("after ws auto: %+v", st)
	}
	if got := compactRingTriggers(h, id); len(got) != 1 || got[0] != wslive.TriggerAuto {
		t.Fatalf("ws auto recorded as %v", got)
	}

	// A request through ws's compact tool is manual.
	resume := false
	if out := h.runner.Compact(key, "", &resume); out.Status != "queued" {
		t.Fatal(out)
	}
	if !h.state(id).Compacting {
		t.Fatal("a queued manual compaction is not shown as compacting")
	}
	eventually(t, "manual compact dispatched", func() bool { return countCompactInputs(h.userInputs(holder)) == 2 })
	compactBoundary(h, id, holder)
	h.idle(id)
	st = h.state(id)
	if st.Compacting || st.Compactions != 2 || st.CompactOrigin != wslive.CompactOriginManual {
		t.Fatalf("after manual: %+v", st)
	}
	if got := compactRingTriggers(h, id); len(got) != 2 || got[1] != wslive.TriggerManual {
		t.Fatalf("manual recorded as %v", got)
	}

	// The harness compacting on its own: shown while it runs, and its own
	// trigger stays on the record.
	h.holders.out(holder, `{"type":"system","subtype":"status","status":"compacting"}`)
	eventually(t, "native compaction shown", func() bool { return h.state(id).Compacting })
	h.holders.out(holder, `{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"auto"}}`, resultLine(), stateLine(HarnessIdle))
	h.sync(id, holder)
	h.idle(id)
	st = h.state(id)
	if st.Compacting || st.Compactions != 3 || st.CompactOrigin != wslive.CompactOriginNative {
		t.Fatalf("after native: %+v", st)
	}
	if got := compactRingTriggers(h, id); len(got) != 3 || got[2] != wslive.TriggerAuto {
		t.Fatalf("native recorded as %v", got)
	}
	// A duplicate end counts nothing.
	h.runner.conv(id).do(func() {
		h.runner.conv(id).compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactEnd})
	})
	if got := h.state(id).Compactions; got != 3 {
		t.Fatalf("duplicate end counted: %d", got)
	}
}

func countCompactInputs(inputs []string) int {
	n := 0
	for _, in := range inputs {
		if in == "/compact" {
			n++
		}
	}
	return n
}

// A failed compaction ends the compacting state without counting.
func TestCompactionStateFailedIsNotCounted(t *testing.T) {
	h := compactionHarness(t)
	id, holder := h.runLead("one")
	key := h.record(id).WSKey
	h.holders.out(holder, turnLines("sess-1", "u1", "first")...)
	h.idle(id)
	resume := false
	if out := h.runner.Compact(key, "", &resume); out.Status != "queued" {
		t.Fatal(out)
	}
	eventually(t, "compact dispatched", func() bool { return countCompactInputs(h.userInputs(holder)) == 1 })
	h.holders.out(holder, `{"type":"system","subtype":"status","status":"compacting"}`)
	h.sync(id, holder)
	h.runner.conv(id).do(func() {
		c := h.runner.conv(id)
		c.compactCommand = false
		c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
		c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
	})
	st := h.state(id)
	if st.Compacting || st.Compactions != 0 || st.CompactOrigin != "" {
		t.Fatalf("failed compaction: %+v", st)
	}
}

// A hosted worker's compaction state reaches its parent's agent row: the
// compacting flag toggles, and the count and latest origin follow completion,
// a ws auto request recorded as auto though Claude records its command as manual.
func TestCompactionStateReachesTheParentsAgentRow(t *testing.T) {
	h, _ := newHeldWorkerHarness(t)
	reg := h.runner.opts.Backends[BackendClaude]
	reg.NativeCompaction = fixtureCompaction()
	h.runner.opts.Backends[BackendClaude] = reg
	lead := h.lead(BackendClaude, LeadOptions{})
	child, err := h.runner.Spawn(SpawnSpec{Profile: AgentWorkerProfile(BackendClaude), PermissionLevel: PermissionWorkdirWrite, delegatedAuthority: true, Root: h.root, Prompt: "work", Parent: &ParentRef{Kind: ParentLead, ReplyID: lead.ID}})
	if err != nil {
		t.Fatal(err)
	}
	holder := h.holder(child.ID)
	h.holders.out(holder, resultLine(), stateLine(HarnessIdle))
	h.idle(child.ID)
	row := func() wslive.Agent {
		for _, a := range h.snapshot(lead.ID).Agents {
			if a.ID == "pi:"+child.ID {
				return a
			}
		}
		return wslive.Agent{}
	}
	eventually(t, "the child's row", func() bool { return row().ID != "" })
	if r := row(); r.Compacting || r.Compactions != 0 || r.CompactOrigin != "" {
		t.Fatalf("fresh row: %+v", r)
	}
	h.runner.conv(child.ID).do(func() {
		c := h.runner.conv(child.ID)
		c.compactPending = &compactRequest{auto: true}
		c.publishCompaction(false)
		c.kick()
	})
	eventually(t, "row shows compacting", func() bool { return row().Compacting })
	eventually(t, "worker compact command", func() bool { return countCompactInputs(h.userInputs(holder)) == 1 })
	compactBoundary(h, child.ID, holder)
	eventually(t, "row shows the completed compaction", func() bool {
		r := row()
		return !r.Compacting && r.Compactions == 1 && r.CompactOrigin == wslive.CompactOriginAuto
	})
}

// State, the agent event and the row carry the fields only while set.
func TestCompactionStateProjectionOmitsZero(t *testing.T) {
	ev := SpawnAgentEvent(State{ID: "c1", Status: StatusIdle}, "key")
	raw, _ := json.Marshal(ev)
	for _, k := range []string{"compacting", "compactions", "compactOrigin"} {
		if strings.Contains(string(raw), k) {
			t.Fatalf("zero event carries %s: %s", k, raw)
		}
	}
	raw, _ = json.Marshal(State{ID: "c1"})
	for _, k := range []string{"compacting", "compactions", "compactOrigin"} {
		if strings.Contains(string(raw), k) {
			t.Fatalf("zero state carries %s: %s", k, raw)
		}
	}
	ev = SpawnAgentEvent(State{ID: "c1", Status: StatusRunning, Compacting: true, Compactions: 2, CompactOrigin: wslive.CompactOriginNative}, "key")
	if !ev.Compacting || ev.Compactions != 2 || ev.CompactOrigin != wslive.CompactOriginNative {
		t.Fatalf("event: %+v", ev)
	}
	ev = SpawnAgentEvent(State{ID: "c1", Status: StatusClosed, Compacting: true, Compactions: 2, CompactOrigin: wslive.CompactOriginAuto}, "key")
	if ev.Compacting || ev.Compactions != 2 {
		t.Fatalf("closed row keeps compacting or loses the count: %+v", ev)
	}
}

// A process that ends, or that ws stops, takes its compaction with it: the
// state no longer shows a queued or running one, whether it was only queued,
// or dispatched and started (the failure the end emits must not leave the
// dispatched command's flag behind it), and the origin it held is dropped.
func TestCompactionStateClearsWhenTheProcessEnds(t *testing.T) {
	ends := map[string]func(c *conv){
		"exit": func(c *conv) { c.processExited() },
		"stop": func(c *conv) { c.stopProcess("test_stop") },
	}
	for _, how := range []string{"exit", "stop"} {
		for _, shape := range []string{"queued", "dispatched"} {
			t.Run(shape+"-then-"+how, func(t *testing.T) {
				h := compactionHarness(t)
				id, holder := h.runLead("one")
				key := h.record(id).WSKey
				h.holders.out(holder, turnLines("sess-1", "u1", "first")...)
				h.idle(id)
				c := h.runner.conv(id)
				if shape == "queued" {
					c.do(func() {
						c.compactPending = &compactRequest{auto: true}
						c.publishCompaction(false)
					})
				} else {
					resume := false
					if out := h.runner.Compact(key, "", &resume); out.Status != "queued" {
						t.Fatal(out)
					}
					eventually(t, "compact dispatched", func() bool { return countCompactInputs(h.userInputs(holder)) == 1 })
					h.holders.out(holder, `{"type":"system","subtype":"status","status":"compacting"}`)
					eventually(t, "compaction running", func() bool {
						var running bool
						c.do(func() { running = c.compacting && c.compactCommand })
						return running
					})
				}
				if !h.state(id).Compacting {
					t.Fatal("the compaction is not shown before the process ends")
				}
				var origin string
				c.do(func() {
					ends[how](c)
					origin = c.compactOrigin
				})
				st := h.state(id)
				if st.Compacting {
					t.Fatalf("a compaction shown after the process %s: %+v", how, st)
				}
				if st.Compactions != 0 || st.CompactOrigin != "" || origin != "" {
					t.Fatalf("a compaction that did not finish left %+v, origin %q", st, origin)
				}
			})
		}
	}
}

// A child's compaction (queued, running, finished) reaches its parent's agent
// row through the ordinary snapshot path: a hosted parent's disk write stays
// inside the heartbeat interval, so the three changes cost no file operation
// of their own, and nothing writes per event outside the writer's gate.
func TestCompactionStateAddsNoSnapshotWriteInsideTheInterval(t *testing.T) {
	now := time.Unix(1000, 0)
	w := newLiveWriter(t.TempDir(), "counted", "claude", func() time.Time { return now }, io.Discard)
	w.slot = &idleSnapshotSlot{}
	writes := 0
	w.writeSnapshotFile = func(path string, raw []byte, mode os.FileMode) error {
		writes++
		return writeFileAtomic(path, raw, mode)
	}
	child := State{ID: "c1", Backend: BackendClaude, Status: StatusIdle, Process: true}
	w.apply(SpawnAgentEvent(child, "key"))
	base := writes
	for _, st := range []State{
		{Compacting: true},
		{Compacting: true},
		{Compactions: 1, CompactOrigin: wslive.CompactOriginAuto},
	} {
		now = now.Add(2 * time.Second)
		next := child
		next.Compacting, next.Compactions, next.CompactOrigin = st.Compacting, st.Compactions, st.CompactOrigin
		w.apply(SpawnAgentEvent(next, "key"))
	}
	if writes != base {
		t.Fatalf("a compaction cost %d snapshot writes inside the interval", writes-base)
	}
	var row wslive.Agent
	for _, a := range w.slot.(*idleSnapshotSlot).snapshot.Agents {
		if a.ID == "pi:c1" {
			row = a
		}
	}
	if row.Compactions != 1 || row.CompactOrigin != wslive.CompactOriginAuto || row.Compacting {
		t.Fatalf("published row: %+v", row)
	}
}

// A restarted runner counts from zero, but the parent's restored row still
// holds the count: the child takes it back before its first row is published,
// so the row neither drops to zero nor restarts its count, and a later
// compaction continues from it.
func TestCompactionStateCountSurvivesARunnerRestart(t *testing.T) {
	h, _ := newHeldWorkerHarness(t)
	reg := h.runner.opts.Backends[BackendClaude]
	reg.NativeCompaction = fixtureCompaction()
	h.runner.opts.Backends[BackendClaude] = reg
	lead := h.lead(BackendClaude, LeadOptions{})
	child, err := h.runner.Spawn(SpawnSpec{Profile: AgentWorkerProfile(BackendClaude), PermissionLevel: PermissionWorkdirWrite, delegatedAuthority: true, Root: h.root, Prompt: "work", Parent: &ParentRef{Kind: ParentLead, ReplyID: lead.ID}})
	if err != nil {
		t.Fatal(err)
	}
	holder := h.holder(child.ID)
	h.holders.out(holder, resultLine(), stateLine(HarnessIdle))
	h.idle(child.ID)
	row := func() wslive.Agent {
		for _, a := range h.snapshot(lead.ID).Agents {
			if a.ID == "pi:"+child.ID {
				return a
			}
		}
		return wslive.Agent{}
	}
	eventually(t, "the child's row", func() bool { return row().ID != "" })

	// The row an earlier daemon left: two compactions, the latest ws auto.
	parent := h.runner.conv(lead.ID)
	parent.do(func() {
		ev := SpawnAgentEvent(h.state(child.ID), h.record(child.ID).WSKey)
		ev.Compactions, ev.CompactOrigin = 2, wslive.CompactOriginAuto
		parent.live.applyQuiet(ev)
		parent.spawnDirty, parent.spawnNext = true, time.Time{}
		parent.seedSpawnRows()
		parent.live.writeSnapshot()
	})
	if st := h.state(child.ID); st.Compactions != 2 || st.CompactOrigin != wslive.CompactOriginAuto {
		t.Fatalf("the child did not take its count back: %+v", st)
	}
	if r := row(); r.Compactions != 2 || r.CompactOrigin != wslive.CompactOriginAuto {
		t.Fatalf("the row dropped its count: %+v", r)
	}

	// The next compaction continues from it, and a lower row seeds nothing.
	h.runner.conv(child.ID).do(func() {
		c := h.runner.conv(child.ID)
		c.seedCompactions(1, wslive.CompactOriginManual)
		c.compacting = true
		c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactEnd})
	})
	if st := h.state(child.ID); st.Compactions != 3 || st.CompactOrigin != wslive.CompactOriginNative {
		t.Fatalf("after the next compaction: %+v", st)
	}
}

// A child closed before a daemon restart has no conversation in the next run;
// its closed row must republish the count the restored snapshot kept, not a
// zero that would wipe it.
func TestCompactionStateClosedChildKeepsItsCountAcrossARestart(t *testing.T) {
	h, _ := newHeldWorkerHarness(t)
	reg := h.runner.opts.Backends[BackendClaude]
	reg.NativeCompaction = fixtureCompaction()
	h.runner.opts.Backends[BackendClaude] = reg
	lead := h.lead(BackendClaude, LeadOptions{})
	child, err := h.runner.Spawn(SpawnSpec{Profile: AgentWorkerProfile(BackendClaude), PermissionLevel: PermissionWorkdirWrite, delegatedAuthority: true, Root: h.root, Prompt: "work", Parent: &ParentRef{Kind: ParentLead, ReplyID: lead.ID}})
	if err != nil {
		t.Fatal(err)
	}
	holder := h.holder(child.ID)
	h.holders.out(holder, resultLine(), stateLine(HarnessIdle))
	h.idle(child.ID)
	h.runner.conv(child.ID).do(func() {
		c := h.runner.conv(child.ID)
		c.compactPending = &compactRequest{auto: true}
		c.publishCompaction(false)
		c.kick()
	})
	eventually(t, "worker compact command", func() bool { return countCompactInputs(h.userInputs(holder)) == 1 })
	compactBoundary(h, child.ID, holder)
	eventually(t, "the completed compaction", func() bool { return h.state(child.ID).Compactions == 1 })
	eventually(t, "the row shows the count", func() bool {
		for _, a := range h.snapshot(lead.ID).Agents {
			if a.ID == "pi:"+child.ID && a.Compactions == 1 {
				return true
			}
		}
		return false
	})
	if _, err := h.runner.CloseConversation(child.ID); err != nil {
		t.Fatal(err)
	}
	h.runner.conv(lead.ID).do(func() {
		c := h.runner.conv(lead.ID)
		c.spawnDirty, c.spawnNext = true, time.Time{}
		c.seedSpawnRows()
		c.live.forceSnapshot()
	})

	h.restart()
	if c := h.runner.conv(child.ID); c != nil {
		t.Fatal("the closed child has a conversation in the next run")
	}
	parent := h.runner.conv(lead.ID)
	if parent == nil {
		t.Fatal("the lead has no conversation in the next run")
	}
	var row wslive.Agent
	parent.do(func() {
		parent.spawnDirty, parent.spawnNext = true, time.Time{}
		parent.seedSpawnRows()
		for _, a := range parent.live.st.Snapshot(h.runner.opts.Now().UnixMilli()).Agents {
			if a.ID == "pi:"+child.ID {
				row = a
			}
		}
	})
	if row.ID == "" || row.Compactions != 1 || row.CompactOrigin != wslive.CompactOriginAuto {
		t.Fatalf("the closed child's row after the restart: %+v", row)
	}
}
