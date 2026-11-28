package wsagent

import (
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wslive"
)

func TestCompactionHardSteersAtParallelBoundariesUntilRequest(t *testing.T) {
	h := compactionHarness(t)
	id, holder := h.runLead("work")
	c := h.runner.conv(id)
	c.do(func() {
		c.compactHard = wsconfig.Threshold{Tokens: 250_000}
		used := int64(265_000)
		c.handle(Event{Live: &wslive.Event{Kind: wslive.KindStatus, Context: &wslive.UsageContext{Tokens: &used, Window: 1_000_000}}})
		if c.compactPending != nil || c.compactCommand || !c.compactHardSteered {
			t.Error("hard guidance must leave the active turn running")
		}
	})
	got := h.userInputs(holder)
	if len(got) != 2 || !strings.Contains(got[1], "agenda_updates when calling ws/compact") || !strings.Contains(got[1], "Wrap up the current step and end your turn") {
		t.Fatalf("initial hard steer = %q", got)
	}

	batch := newBatchTracker("test")
	feed := func(events []Event) {
		c.do(func() {
			for _, ev := range events {
				c.handle(ev)
			}
		})
	}
	feed(batch.addCall("parallel", "a", "one", "", 0, 1))
	feed(batch.addCall("parallel", "b", "two", "", 0, 2))
	feed(batch.endCall("a", false, 0, 3))
	if got := h.userInputs(holder); len(got) != 2 {
		t.Fatalf("first result of a parallel batch delivered a steer: %q", got)
	}
	feed(batch.endCall("b", false, 0, 4))
	got = h.userInputs(holder)
	if len(got) != 3 || !strings.HasPrefix(got[2], "Hard 250k, +6% (265k).") || strings.Contains(got[2], "\n") {
		t.Fatalf("parallel boundary steer = %q", got)
	}

	if result := h.runner.Compact(h.record(id).WSKey, "", nil); result.Status != "queued" {
		t.Fatal(result)
	}
	feed(batch.addCall("next", "c", "three", "", 0, 5))
	feed(batch.endCall("c", false, 0, 6))
	// A queued request waits for the idle boundary but must not silence hard
	// guidance for a turn that keeps running past hard: the boundary keeps
	// steering the turn to end, and nothing compacts mid-turn.
	got = h.userInputs(holder)
	if len(got) != 4 || !strings.HasPrefix(got[3], "Hard 250k,") {
		t.Fatalf("queued request did not keep steering the open turn: %q", got)
	}
	c.do(func() { c.endTurn("", false) })
	if got := h.userInputs(holder); len(got) != 5 || got[4] != "/compact" {
		t.Fatalf("turn boundary did not dispatch the request once: %q", got)
	}
	compactBoundary(h, id, holder)
	got = h.userInputs(holder)
	if len(got) != 6 || !strings.Contains(got[5], "Context was compacted") || !strings.Contains(got[5], "Continue the work") {
		t.Fatalf("request continuation = %q", got)
	}
}

type compactSteerTestProc struct{ fakeSteerProc }

func (p compactSteerTestProc) steerCompaction(text string) (string, error) {
	_ = p.Steer(text)
	return string(rune('a' + len(p.steers))), nil
}

func TestCompactionHardSteerRetriesLongGuidanceAfterNativeRefusal(t *testing.T) {
	p := compactSteerTestProc{fakeSteerProc{&fakeProc{}}}
	c := &conv{
		r: NewRunner(Options{}), rec: Record{Backend: BackendCodex, Lead: true}, proc: p, turn: true,
		compactHard: wsconfig.Threshold{Percent: 80},
	}
	c.observeCompaction(*compactionUsage(90_000).Live)
	c.steerCompaction(false)
	rejected := c.compactHardSteerID
	c.steerCompaction(true)
	if len(p.steers) != 1 || c.compactHardSteered {
		t.Fatal("short guidance preceded acknowledgement of the long steer")
	}
	c.handle(Event{compactSteerReply: &compactSteerReply{id: rejected, refusal: "late"}})
	c.steerCompaction(true)
	if len(p.steers) != 2 || p.steers[0] != p.steers[1] || c.compactHardSteerID == rejected {
		t.Fatalf("retry did not send the first guidance again: %q", p.steers)
	}
	c.handle(Event{compactSteerReply: &compactSteerReply{id: rejected}})
	if c.compactHardSteered {
		t.Fatal("old acknowledgement changed the current crossing")
	}
	c.handle(Event{compactSteerReply: &compactSteerReply{id: c.compactHardSteerID}})
	c.steerCompaction(true)
	if len(p.steers) != 3 || !strings.HasPrefix(p.steers[2], "Hard 80k,") {
		t.Fatalf("acknowledged guidance did not switch to short steer: %q", p.steers)
	}
}

func TestCompactionHardFallbackContinuesWithRestoreWithoutRequest(t *testing.T) {
	h := nativeCompactionHarness(t)
	id, holder := h.runLead("work")
	c := h.runner.conv(id)
	c.doT(t, func(failf func(string, ...any)) {
		usage := compactionUsage(90_000)
		usage.FinalUsage = true
		c.handle(usage)
		if got := h.userInputs(holder); len(got) != 1 {
			failf("hard usage cut the active turn: %q", got)
			return
		}
		c.endTurn("", false)
	})
	if got := h.userInputs(holder); len(got) != 2 || got[1] != "/compact" {
		t.Fatalf("hard turn boundary = %q", got)
	}
	compactBoundary(h, id, holder)
	if got := h.userInputs(holder); len(got) != 3 || !strings.Contains(got[2], "Context was compacted") || !strings.Contains(got[2], "Continue the work") {
		t.Fatalf("automatic restoration and continuation = %q", got)
	}
}

func TestCompactionHardGuidanceFailureLeavesCrossingUnsteered(t *testing.T) {
	h := compactionHarness(t)
	id, holder := h.runLead("work")
	c := h.runner.conv(id)
	h.holders.failInputs("user", 1)
	c.do(func() {
		c.handle(compactionUsage(90_000))
		if c.compactHardSteered {
			t.Error("failed write consumed hard guidance")
		}
		c.steerCompaction(true)
	})
	if got := h.userInputs(holder); len(got) != 2 || !strings.Contains(got[1], "Wrap up the current step") {
		t.Fatalf("next boundary did not deliver first guidance: %q", got)
	}
}

func TestCompactionHardNativeCompletionHandlesQueuedCrossing(t *testing.T) {
	h := compactionHarness(t)
	id, holder := h.runLead("work")
	c := h.runner.conv(id)
	c.do(func() { c.handle(compactionUsage(90_000)) })
	if got := h.runner.Compact(h.record(id).WSKey, "", nil); got.Status != "queued" {
		t.Fatal(got)
	}
	c.do(func() {
		for _, phase := range []string{wslive.CompactStart, wslive.CompactEnd} {
			c.handle(Event{Live: &wslive.Event{Kind: wslive.KindCompact, Phase: phase, Trigger: wslive.TriggerAuto}})
		}
		if c.compactPending != nil || c.compactNoShrink != 1 || c.compactResume {
			t.Error("native completion did not handle the crossing exactly once")
		}
		c.endTurn("", false)
	})
	if got := h.userInputs(holder); len(got) != 2 {
		t.Fatalf("native completion was followed by redundant compaction: %q", got)
	}
	h.send(id, "next input")
	if got := h.userInputs(holder); len(got) != 3 || !strings.Contains(got[2], "Context was compacted") || !strings.Contains(got[2], "next input") {
		t.Fatalf("native completion restore = %q", got)
	}
}

func TestCompactionHardSteerWorkerRoles(t *testing.T) {
	for _, backend := range []string{BackendClaude, BackendPi} {
		t.Run(backend, func(t *testing.T) {
			p := fakeSteerProc{&fakeProc{}}
			c := &conv{
				r: NewRunner(Options{Backends: map[string]BackendRegistration{backend: {Lifecycle: builtinLifecycle(backend)}}}), rec: Record{Backend: backend}, proc: p, turn: true,
				workerPermissionLevel: PermissionReadOnly, compactHard: wsconfig.Threshold{Percent: 80},
			}
			c.observeCompaction(*compactionUsage(90_000).Live)
			c.steerCompaction(false)
			c.steerCompaction(true)
			if backend == BackendClaude {
				if len(p.steers) != 0 {
					t.Fatalf("Claude worker received guidance: %q", p.steers)
				}
				return
			}
			if len(p.steers) != 2 || !strings.Contains(p.steers[0], "in your report") || strings.Contains(p.steers[0], "agenda") {
				t.Fatalf("read-only guidance = %q", p.steers)
			}
		})
	}
}

func TestCompactionRatioDefaultsAcrossContextWindows(t *testing.T) {
	t.Run("builtin defaults", func(t *testing.T) {
		for _, lead := range []bool{false, true} {
			c := &conv{r: NewRunner(Options{}), rec: Record{Lead: lead}}
			c.loadCompaction()
			for _, tc := range []struct {
				window int64
				soft   int64
				hard   int64
				m1, m2 int64
			}{
				{window: 128_000, soft: 76_800, hard: 102_400, m1: 85_333, m2: 93_867},
				{window: 500_000, soft: 180_000, hard: 230_000, m1: 196_667, m2: 213_333},
				{window: 1_000_000, soft: 180_000, hard: 230_000, m1: 196_667, m2: 213_333},
			} {
				got := c.usageCompaction(tc.window)
				if got == nil || got.Soft != tc.soft || got.Hard != tc.hard || len(got.Milestones) != 2 || got.Milestones[0] != tc.m1 || got.Milestones[1] != tc.m2 {
					t.Fatalf("lead=%v window=%d compaction thresholds %+v", lead, tc.window, got)
				}
			}
		}
	})
	t.Run("explicit ratio overrides", func(t *testing.T) {
		cfg := wsconfig.Options{CacheHome: t.TempDir(), ConfigHome: t.TempDir()}
		resolver := wsconfig.NewResolver(cfg, nil, nil, nil)
		for _, kv := range [][2]string{
			{"compaction.lead_advisory", "50%"},
			{"compaction.lead_hard", "80%"},
			{"compaction.worker_advisory", "50%"},
			{"compaction.worker_hard", "80%"},
		} {
			if err := resolver.Set(kv[0], kv[1], wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}); err != nil {
				t.Fatal(err)
			}
		}
		r := NewRunner(Options{Config: cfg})
		for _, lead := range []bool{false, true} {
			c := &conv{r: r, rec: Record{Lead: lead}}
			c.loadCompaction()
			for _, window := range []int64{128_000, 500_000, 1_000_000} {
				got := c.usageCompaction(window)
				if got == nil || got.Soft != window/2 || got.Hard != window*8/10 || len(got.Milestones) != 2 || got.Milestones[0] != window*6/10 || got.Milestones[1] != window*7/10 {
					t.Fatalf("lead=%v window=%d compaction thresholds %+v", lead, window, got)
				}
			}
		}
	})
}

func TestAgyHardCompactionSteersOnceAcrossToolBoundaries(t *testing.T) {
	p := fakeSteerProc{&fakeProc{}}
	agyReg := BuiltinBackendRegistrations()[BackendAgy]
	if !agyReg.Lifecycle.SingleHardCompactionSteer {
		t.Fatal("AgyBackend registration must set SingleHardCompactionSteer")
	}
	c := &conv{
		r: NewRunner(Options{Backends: map[string]BackendRegistration{BackendAgy: agyReg}}),
		rec: Record{
			Backend: BackendAgy,
			Lead:    true,
			WSKey:   "k-agy",
		},
		proc:        p,
		turn:        true,
		compactHard: wsconfig.Threshold{Percent: 80},
	}
	c.observeCompaction(*compactionUsage(90_000).Live)
	c.steerCompaction(false)
	c.steerCompaction(true)
	c.steerCompaction(true)
	c.steerCompaction(true)
	if len(p.steers) != 1 || !strings.Contains(p.steers[0], "has reached the hard point") {
		t.Fatalf("agy steers = %q, want single initial hard steer without boundary repeats", p.steers)
	}
}
