package wsagent

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

type restoreProbeCall struct {
	key      string
	cause    RestoreCause
	children []AgentChild
}

type restoreProbe struct {
	mu    sync.Mutex
	state string
	err   error
	calls []restoreProbeCall
}

func (p *restoreProbe) render(_ string, _ string, key string, cause RestoreCause, children []AgentChild) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, restoreProbeCall{key: key, cause: cause, children: append([]AgentChild(nil), children...)})
	if p.err != nil {
		return "", p.err
	}
	return fmt.Sprintf("restore[%s] key=%s cause=%s", p.state, key, cause), nil
}

func (p *restoreProbe) setState(state string) {
	p.mu.Lock()
	p.state = state
	p.mu.Unlock()
}

func (p *restoreProbe) setError(err error) {
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
}

func (p *restoreProbe) snapshot() []restoreProbeCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]restoreProbeCall(nil), p.calls...)
}

// An idle lead keeps its holder across a restart and takes one standalone
// restore turn after the replay. An idle finished worker is put to sleep at
// shutdown instead (holder and child ended), takes no standalone restore
// turn, and gets the restart restore prepended to the input of its next wake.
func TestRestartRestoreIdleLeadAndChildAfterReplay(t *testing.T) {
	probe := &restoreProbe{state: "stale"}
	h := newLeadHarness(t)
	// The fixture launches workers with stdio MCP, whose route a restart
	// never makes stale: the worker's sleep comes from its finished-idle
	// state alone.
	reg := h.opts.Backends[BackendClaude]
	reg.WorkerHTTPMCP = false
	h.opts.Backends[BackendClaude] = reg
	h.opts.RestoreBundle = probe.render
	h.opts.WSMCP = func(string) (MCPCommand, error) { return MCPCommand{Command: "/unused/ws-mcp"}, nil }
	for tier, model := range map[string]string{"small": "prov/small", "medium": "prov/medium"} {
		if _, err := wsconfig.SetGlobalAgentsTierForHarness(h.opts.Config, tier, "", model, BackendClaude, "low"); err != nil {
			t.Fatal(err)
		}
	}
	h.start()

	leadID, leadHolder := h.runLead("lead work")
	h.holders.out(leadHolder, turnLines("sess-1", "lead-1", "lead done")...)
	h.idle(leadID)
	worker, err := h.runner.Spawn(SpawnSpec{
		Profile: AgentWorkerProfile(BackendClaude), Root: h.root, Prompt: "worker work", Tier: "medium",
		PermissionLevel: PermissionWorkdirWrite, delegatedAuthority: true,
		Parent: &ParentRef{Kind: ParentLead, ReplyID: leadID},
	})
	if err != nil {
		t.Fatal(err)
	}
	workerHolder := h.holder(worker.ID)
	eventually(t, "worker prompt before restart", func() bool { return len(h.userInputs(workerHolder)) == 1 })
	h.holders.out(workerHolder, stateLine(HarnessRunning), initLine("worker-s1", "dontAsk"))
	h.holders.out(workerHolder, recoveryWorkerReply("worker done")...)
	h.idle(worker.ID)
	if got := len(h.userInputs(workerHolder)); got != 1 {
		t.Fatalf("worker prompts before restart = %d, want 1", got)
	}

	h.runner.Close()
	// The shutdown put the finished worker to sleep and let the lead go.
	if h.holders.opCount("stop "+workerHolder) != 1 || h.holders.opCount("stop "+leadHolder) != 0 {
		t.Fatalf("shutdown stops = %v, want the worker's holder alone", h.holders.opList())
	}
	if rec := h.record(worker.ID); rec.HolderID != "" || rec.HostPID != 0 || rec.ClosedAt != "" {
		t.Fatalf("slept worker record = %+v, want open with no holder", rec)
	}
	if rec := h.record(leadID); rec.HolderID != leadHolder {
		t.Fatalf("lead record = %+v, want its holder kept", rec)
	}
	h.holders.restart()
	entered, release := h.gateScan()
	defer release()
	h.start()
	wait(t, "restart holder scan", entered)
	probe.setState("fresh-after-launch")
	if calls := probe.snapshot(); len(calls) != 0 {
		t.Fatalf("restore rendered before holder replay: %+v", calls)
	}
	if got := len(h.userInputs(leadHolder)); got != 1 {
		t.Fatalf("lead received restore before holder replay: %d inputs", got)
	}

	release()
	eventually(t, "one restart restore for the lead", func() bool {
		return len(probe.snapshot()) == 1 && len(h.userInputs(leadHolder)) == 2
	})
	if text := h.userInputs(leadHolder)[1]; !strings.Contains(text, "restore[fresh-after-launch]") || !strings.Contains(text, "cause=restart") {
		t.Fatalf("lead restart restore = %q", text)
	}
	leadKey, workerKey := h.record(leadID).WSKey, h.record(worker.ID).WSKey
	calls := probe.snapshot()
	if calls[0].key != leadKey || calls[0].cause != RestoreRestart {
		t.Fatalf("restart calls = %+v", calls)
	}
	if children := calls[0].children; len(children) != 1 || children[0].Address != AgentAddress(worker.ID) {
		t.Fatalf("lead direct children = %+v", children)
	}
	// The sleeping worker takes no standalone restore turn: no process
	// starts for it and nothing renders its bundle until a wake.
	time.Sleep(30 * time.Millisecond)
	if calls := probe.snapshot(); len(calls) != 1 {
		t.Fatalf("restart restore beyond the lead's: %+v", calls)
	}
	if n := h.holders.opCount("launch"); n != 2 {
		t.Fatalf("restart launched a holder for the sleeping worker: %v", h.holders.opList())
	}
	if st := h.state(worker.ID); st.Process || st.Status == StatusClosed {
		t.Fatalf("worker after restart = %+v, want asleep", st)
	}

	probe.setState("fresh-at-wake")
	if _, err := h.runner.Send(worker.ID, "follow-up work"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a new holder for the woken worker", func() bool {
		rec := h.record(worker.ID)
		return rec.HolderID != "" && rec.HolderID != workerHolder
	})
	wokenHolder := h.holder(worker.ID)
	eventually(t, "the woken worker's input", func() bool { return len(h.userInputs(wokenHolder)) == 1 })
	text := h.userInputs(wokenHolder)[0]
	restoreAt, workAt := strings.Index(text, "restore[fresh-at-wake]"), strings.Index(text, "follow-up work")
	if restoreAt < 0 || workAt < 0 || restoreAt > workAt || !strings.Contains(text, "cause=restart") {
		t.Fatalf("woken worker input = %q, want the restart restore before the work", text)
	}
	calls = probe.snapshot()
	if len(calls) != 2 || calls[1].key != workerKey || calls[1].cause != RestoreRestart || len(calls[1].children) != 0 {
		t.Fatalf("restore calls after the wake = %+v", calls)
	}
}

func TestRestartRestoreActiveRejoinWaitsForIdle(t *testing.T) {
	probe := &restoreProbe{state: "active"}
	h := newLeadHarness(t)
	h.opts.RestoreBundle = probe.render
	h.start()
	id, holder := h.runLead("one")
	h.holders.out(holder, stateLine(HarnessRunning), initLine("sess-1", "auto"), replyLine("u1", "still running"))
	eventually(t, "active lead", func() bool { return h.state(id).Status == StatusRunning })

	h.runner.Close()
	h.holders.restart()
	h.start()
	eventually(t, "active lead rejoined", func() bool {
		c := h.runner.conv(id)
		if c == nil {
			return false
		}
		ready := false
		c.do(func() { ready = !c.replaying && c.turn && strings.HasPrefix(c.compactNote, restartRestoreMarker) })
		return ready
	})
	if calls := probe.snapshot(); len(calls) != 0 {
		t.Fatalf("active rejoin rendered before idle: %+v", calls)
	}
	if got := len(h.userInputs(holder)); got != 1 {
		t.Fatalf("active rejoin started restore turn: %d inputs", got)
	}

	h.holders.out(holder, resultLine(), stateLine(HarnessIdle))
	eventually(t, "restore after active turn settles", func() bool {
		return len(probe.snapshot()) == 1 && len(h.userInputs(holder)) == 2
	})
	if got := h.userInputs(holder)[1]; !strings.Contains(got, "restore[active]") || !strings.Contains(got, "cause=restart") {
		t.Fatalf("active restart restore = %q", got)
	}
}

func TestRestartRestorePrecedesQueuedInputAndExcludesScanSpawn(t *testing.T) {
	probe := &restoreProbe{state: "restart-order"}
	h := newLeadHarness(t)
	h.opts.RestoreBundle = probe.render
	h.start()
	id, holder := h.runLead("one")
	h.holders.out(holder, turnLines("sess-1", "u1", "done")...)
	h.idle(id)

	h.runner.Close()
	rec := h.record(id)
	rec.Queued = []QueuedInput{{Text: "queued while daemon was down"}}
	if err := writeRecord(wsdashboard.SessionDir(h.dir, id), rec); err != nil {
		t.Fatal(err)
	}
	h.holders.restart()
	entered, release := h.gateScan()
	defer release()
	h.start()
	wait(t, "restart holder scan", entered)

	spawned := h.lead(BackendClaude, LeadOptions{})
	h.runner.conv(spawned.ID).doT(t, func(failf func(string, ...any)) {
		if note := h.runner.conv(spawned.ID).compactNote; note != "" {
			failf("session spawned during scan inherited restart restore: %q", note)
			return
		}
	})
	if got := len(h.userInputs(holder)); got != 1 {
		t.Fatalf("queued input crossed the replay gate: %d inputs", got)
	}

	release()
	eventually(t, "restore prepended to queued input", func() bool { return len(h.userInputs(holder)) == 2 })
	text := h.userInputs(holder)[1]
	if restoreAt, workAt := strings.Index(text, "restore[restart-order]"), strings.Index(text, "queued while daemon was down"); restoreAt < 0 || workAt < 0 || restoreAt > workAt {
		t.Fatalf("restart restore/queued input ordering = %q", text)
	}
	if calls := probe.snapshot(); len(calls) != 1 || calls[0].key != rec.WSKey || calls[0].cause != RestoreRestart {
		t.Fatalf("restart render calls = %+v", calls)
	}
}

func TestRestartRestoreSleepingSessionWaitsForWake(t *testing.T) {
	h, got := childHarness(t)
	w := h.spawnWorker("medium")
	p := h.backend.waitProc(t, 0)
	p.nextSent(t)
	p.reply("done")
	eventually(t, "worker report", func() bool { return len(got.lead()) == 1 })
	h.asleep(w.ID)

	probe := &restoreProbe{state: "stale"}
	h.runner.Close()
	h.config.RestoreBundle = probe.render
	h.start()
	probe.setState("fresh-at-wake")
	time.Sleep(60 * time.Millisecond)
	if calls := probe.snapshot(); len(calls) != 0 {
		t.Fatalf("sleeping restore rendered without a wake: %+v", calls)
	}
	if starts := h.backend.startCount(); starts != 1 {
		t.Fatalf("restore alone woke sleeping session: %d process starts", starts)
	}

	if _, err := h.runner.Send(w.ID, "wake work"); err != nil {
		t.Fatal(err)
	}
	p2 := h.backend.waitProc(t, 1)
	text := p2.nextSent(t)
	if restoreAt, workAt := strings.Index(text, "restore[fresh-at-wake]"), strings.Index(text, "wake work"); restoreAt < 0 || workAt < 0 || restoreAt > workAt {
		t.Fatalf("sleeping wake ordering = %q", text)
	}
	if calls := probe.snapshot(); len(calls) != 1 || calls[0].cause != RestoreRestart || calls[0].key != h.record(w.ID).WSKey {
		t.Fatalf("sleeping restart calls = %+v", calls)
	}
}

func TestRestartRestoreSupersedesLeadSessionState(t *testing.T) {
	h := newLeadHarness(t)
	h.start()
	id, holder := h.runLead("one")
	h.holders.out(holder, turnLines("sess-1", "u1", "done")...)
	h.idle(id)
	if _, err := h.runner.Stop(id); err != nil {
		t.Fatal(err)
	}
	h.runner.Close()
	h.holders.restart()
	h.opts.LeadSessionState = func(string) (string, error) { return "STARTUP STATE", nil }
	h.opts.RestoreBundle = (&restoreProbe{state: "## Session State"}).render
	h.start()
	h.send(id, "two")
	wakeHolder := h.holder(id)
	eventually(t, "the wake input with restore state", func() bool { return len(h.userInputs(wakeHolder)) == 1 })
	got := h.userInputs(wakeHolder)[0]
	if strings.Count(got, "## Session State") != 1 || !strings.Contains(got, "cause=restart") || !strings.Contains(got, h.envelope("two")) || strings.Contains(got, "STARTUP STATE") {
		t.Fatalf("first input after restart = %q", got)
	}
}

func TestRestoreProviderFailureInjectsKeyBearingDiagnostic(t *testing.T) {
	probe := &restoreProbe{state: "partial state must not appear"}
	probe.setError(errors.New("fixture provider unavailable"))
	h := newLeadHarness(t)
	reg := h.opts.Backends[BackendClaude]
	reg.Catalog = ClaudeCatalog()
	h.opts.Backends[BackendClaude] = reg
	h.opts.RestoreBundle = probe.render
	h.start()
	id, holder := h.runLead("one")
	key := h.record(id).WSKey
	h.holders.out(holder, turnLines("sess-1", "u1", "done")...)
	h.idle(id)
	resume := false
	if got := h.runner.Compact(key, "", &resume); got.Status != "queued" {
		t.Fatal(got)
	}
	eventually(t, "native compact", func() bool { return len(h.userInputs(holder)) == 2 })
	h.send(id, "ordinary work")
	compactBoundary(h, id, holder)
	eventually(t, "provider failure diagnostic", func() bool { return len(h.userInputs(holder)) == 3 })
	text := h.userInputs(holder)[2]
	for _, want := range []string{`<ws-restore cause="compaction" status="error">`, key, "could not be rendered", "fixture provider unavailable", "workflow_state", "</ws-restore>\n\n", "ordinary work"} {
		if !strings.Contains(text, want) {
			t.Fatalf("provider diagnostic lacks %q: %q", want, text)
		}
	}
	if strings.Contains(text, "partial state must not appear") {
		t.Fatalf("provider failure injected partial bundle: %q", text)
	}
	if !strings.Contains(h.log.String(), "restore bundle: fixture provider unavailable") {
		t.Fatalf("provider failure was not logged: %s", h.log.String())
	}
}

func TestRestoreBundleT0UsesNoPersistedShapeAcrossNewOldNewStart(t *testing.T) {
	h, got := childHarness(t)
	w := h.spawnWorker("medium")
	p := h.backend.waitProc(t, 0)
	p.nextSent(t)
	p.reply("done")
	eventually(t, "worker report", func() bool { return len(got.lead()) == 1 })
	h.asleep(w.ID)
	h.runner.Close()

	dir := wsdashboard.SessionDir(h.config.Dir, w.ID)
	before, err := os.ReadFile(recordPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	probe := &restoreProbe{state: "new daemon"}
	h.config.RestoreBundle = probe.render
	h.start()
	h.runner.conv(w.ID).doT(t, func(failf func(string, ...any)) {
		if note := h.runner.conv(w.ID).compactNote; !strings.HasPrefix(note, restartRestoreMarker) {
			failf("new daemon did not hold an in-memory restart marker: %q", note)
			return
		}
	})
	afterNew, err := os.ReadFile(recordPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, afterNew) {
		t.Fatalf("new daemon rewrote the existing record for restore\nbefore: %s\nafter: %s", before, afterNew)
	}
	if bytes.Contains(afterNew, []byte("restore")) || bytes.Contains(afterNew, []byte("bundle")) {
		t.Fatalf("restore state leaked into persisted record: %s", afterNew)
	}

	h.runner.Close()
	h.config.RestoreBundle = nil // an old daemon has no runtime provider
	h.start()
	h.runner.conv(w.ID).doT(t, func(failf func(string, ...any)) {
		if note := h.runner.conv(w.ID).compactNote; note != "" {
			failf("old daemon behavior observed a new-only marker: %q", note)
			return
		}
	})
	afterOld, err := os.ReadFile(recordPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, afterOld) {
		t.Fatalf("old daemon behavior did not see identical persisted bytes\nbefore: %s\nafter: %s", before, afterOld)
	}
	if rec, err := readRecord(dir); err != nil || rec.SchemaVersion != recordSchemaVersion || rec.WSKey != h.record(w.ID).WSKey {
		t.Fatalf("new reopen could not reconstruct the unchanged record: rec=%+v err=%v", rec, err)
	}
}
