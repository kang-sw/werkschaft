package wsagent

import (
	"encoding/json"
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
	if len(got) != 2 || !strings.Contains(got[1], "Save the state needed to resume in your agenda") || !strings.Contains(got[1], "Wrap up the current step and end your turn") {
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
	feed(batch.addCall("parallel", "a", "one", "", 1))
	feed(batch.addCall("parallel", "b", "two", "", 2))
	feed(batch.endCall("a", false, 3))
	if got := h.userInputs(holder); len(got) != 2 {
		t.Fatalf("first result of a parallel batch delivered a steer: %q", got)
	}
	feed(batch.endCall("b", false, 4))
	got = h.userInputs(holder)
	if len(got) != 3 || !strings.HasPrefix(got[2], "Hard 250k, +6% (265k).") || strings.Contains(got[2], "\n") {
		t.Fatalf("parallel boundary steer = %q", got)
	}

	if result := h.runner.Compact(h.record(id).WSKey, "", nil); result.Status != "queued" {
		t.Fatal(result)
	}
	feed(batch.addCall("next", "c", "three", "", 5))
	feed(batch.endCall("c", false, 6))
	if got := h.userInputs(holder); len(got) != 3 {
		t.Fatalf("pending request allowed more guidance or mid-turn compaction: %q", got)
	}
	c.do(func() { c.endTurn("", false) })
	if got := h.userInputs(holder); len(got) != 4 || got[3] != "/compact" {
		t.Fatalf("turn boundary did not dispatch the request once: %q", got)
	}
	compactBoundary(h, id, holder)
	got = h.userInputs(holder)
	if len(got) != 5 || !strings.Contains(got[4], "Context was compacted") || !strings.Contains(got[4], "Continue the work") {
		t.Fatalf("request continuation = %q", got)
	}
}

func TestCompactionPiAcceptedLeverTakesIdleBoundary(t *testing.T) {
	for _, failed := range []bool{false, true} {
		m := newPiMapper(StartSpec{Lead: &LeadSpec{}})
		r := nopReplies{}
		m.mapLine([]byte(`{"type":"agent_start"}`), r)
		result := `{"type":"tool_execution_end","toolName":"ws-compact","isError":false}`
		if failed {
			result = strings.ReplaceAll(result, "false", "true")
		}
		m.mapLine([]byte(result), r)
		got := m.mapLine([]byte(`{"type":"agent_settled","aborted":true}`), r)
		if len(got) != 1 || !got[0].TurnEnd || got[0].CompactRequested == failed {
			t.Fatalf("failed=%v, turn end = %+v", failed, got)
		}
	}
}

func TestCompactionSteerRepliesDoNotAcknowledgeWorkerReports(t *testing.T) {
	const piReply = `{"type":"response","command":"steer","id":-4503599627370496,"success":true}`
	// The base runner only decodes piLine. Its decoder must discard this
	// private reply before its worker report acknowledgement path.
	var base piLine
	if err := json.Unmarshal([]byte(piReply), &base); err == nil {
		t.Fatal("base Pi wire shape accepted the compaction-only acknowledgement")
	}
	codex := newCodexMapper("sid")
	codex.worker = true
	for _, tc := range []struct {
		name             string
		m                lineMapper
		guidance, report string
	}{
		{"Pi", newPiMapper(StartSpec{}), piReply, `{"type":"response","command":"steer","success":true}`},
		{"Codex", codex, `{"id":"wsc/compactSteer/e/1","result":{}}`, `{"id":"wsc/control/e/2","result":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := nopReplies{}
			if got := tc.m.mapLine([]byte(tc.guidance), r); len(got) != 1 || got[0].compactSteerReply == nil || got[0].SteerAnswer != nil {
				t.Fatalf("guidance was treated as a report answer: %+v", got)
			}
			if got := tc.m.mapLine([]byte(tc.report), r); len(got) != 1 || got[0].SteerAnswer == nil {
				t.Fatalf("worker report answer = %+v", got)
			}
		})
	}
	m := newAgyMapper("sid")
	m.promptSentText("ws-p-e-1", "work")
	m.promptSentText("ws-p-e-2", "")
	m.promptSentText("ws-p-e-3", "report")
	r := nopReplies{}
	if got := m.mapLine([]byte(`{"jsonrpc":"2.0","id":"ws-p-e-1","result":{"stopReason":"end_turn"}}`), r); len(got) != 0 {
		t.Fatalf("agy guidance was treated as a report answer: %+v", got)
	}
	got := m.mapLine([]byte(`{"jsonrpc":"2.0","id":"ws-p-e-2","result":{"stopReason":"end_turn"}}`), r)
	if len(got) != 2 || got[0].SteerAnswer == nil || got[1].UserTaken != "report" {
		t.Fatalf("agy worker report answer = %+v", got)
	}
}

func TestCompactionPiNumericSteerFallsBackOnce(t *testing.T) {
	for _, command := range []string{"steer", "parse"} {
		t.Run(command, func(t *testing.T) {
			h, _ := newFakePiRecoveryHarness(t)
			id := h.lead(BackendPi, LeadOptions{}).ID
			h.send(id, "work")
			holder := h.holder(id)
			c := h.runner.conv(id)
			c.do(func() { c.handle(compactionUsage(90_000)) })
			inputs := h.holders.inputs(holder, "steer")
			if len(inputs) != 1 {
				t.Fatalf("initial hard steer = %q", inputs)
			}
			var first struct {
				ID      int64
				Message string
			}
			if err := json.Unmarshal([]byte(inputs[0]), &first); err != nil || first.ID > -piCompactSteerIDBase {
				t.Fatalf("initial hard steer lacks reserved numeric ID: %q", inputs[0])
			}
			reply := map[string]any{"type": "response", "command": command, "success": false, "error": "ID must be a string"}
			if command == "steer" {
				reply["id"] = first.ID
			}
			h.holders.out(holder, jsonLine(reply), jsonLine(reply))
			eventually(t, "fallback steer", func() bool { return len(h.holders.inputs(holder, "steer")) >= 2 })
			var fallback struct{ ID, Message string }
			if err := json.Unmarshal([]byte(h.holders.inputs(holder, "steer")[1]), &fallback); err != nil || !strings.HasPrefix(fallback.ID, piCompactSteerID) || fallback.Message != first.Message {
				t.Fatalf("fallback did not resend the guidance with a string ID: %+v, %v", fallback, err)
			}
			reply["id"], reply["command"] = fallback.ID, "steer"
			h.holders.out(holder, jsonLine(reply))
			eventually(t, "fallback refusal consumed", func() bool {
				var cleared bool
				c.do(func() { cleared = c.compactHardSteerID == "" })
				return cleared
			})
			if got := h.holders.inputs(holder, "steer"); len(got) != 2 {
				t.Fatalf("more than one fallback was sent: %q", got)
			}
		})
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
	h := compactionHarness(t)
	id, holder := h.runLead("work")
	c := h.runner.conv(id)
	c.do(func() {
		usage := compactionUsage(90_000)
		usage.FinalUsage = true
		c.handle(usage)
		if got := h.userInputs(holder); len(got) != 1 {
			t.Fatalf("hard usage cut the active turn: %q", got)
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
		if c.compactPending != nil || !c.compactHardHandled || c.compactResume {
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
				r: NewRunner(Options{}), rec: Record{Backend: backend}, proc: p, turn: true,
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

func TestCompactionPiAdvisoryUsesCurrentThresholdsAndLever(t *testing.T) {
	r := NewRunner(Options{Backends: map[string]BackendRegistration{BackendPi: {NativeCompaction: PiCompaction()}}})
	c := &conv{r: r, rec: Record{Backend: BackendPi, Lead: true}, compactLoaded: true, compactAdvisoryEnabled: true}
	c.compactAdvisory = wsconfig.Threshold{Percent: 50, Tokens: 200_000}
	c.compactHard = wsconfig.Threshold{Percent: 80, Tokens: 350_000}
	c.compactTokens, c.compactWindow = 230_000, 1_000_000
	c.stageCompactionNudge(c.compactHard.Point(c.compactWindow))
	text := c.compactNudge.text
	for _, want := range []string{"advisory point 200000", "hard point is 350000", "call ws-compact", piCompactPreparation} {
		if !strings.Contains(strings.ToLower(text), want) {
			t.Fatalf("missing %q from Pi advisory %q", want, text)
		}
	}
	c.rec.PluginWarning = piExtensionFallbackMarker
	c.stageCompactionNudge(c.compactHard.Point(c.compactWindow))
	if text := c.compactNudge.text; !strings.Contains(text, "call ws/compact") || strings.Contains(text, piCompactPreparation) {
		t.Fatalf("MCP-only Pi advisory = %q", text)
	}
}

func TestCompactionPiDaemonFallbackChecksBundleCapability(t *testing.T) {
	for _, tc := range []struct {
		name, instruction string
	}{
		{"legacy", ""},
		{"capable", "<!-- ws-pi-daemon-auto-compact -->"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newFakePiRecoveryHarness(t)
			reg := h.runner.opts.Backends[BackendPi]
			reg.NativeCompaction = PiCompaction()
			h.runner.opts.Backends[BackendPi] = reg
			id := h.lead(BackendPi, LeadOptions{}).ID
			h.send(id, "work")
			holder := h.holder(id)
			h.holders.out(holder, `{"type":"agent_start"}`)
			if tc.instruction != "" {
				const status = `{"kind":"status","background":0,"monitors":0,"capabilities":["pi_compaction_preparation_v1"]}`
				h.holders.out(holder, jsonLine(map[string]any{"type": "extension_ui_request", "method": "setWidget", "widgetKey": piEventsWidgetKey, "widgetLines": []string{status}}))
			}
			h.holders.out(holder,
				`{"type":"response","command":"get_session_stats","success":true,"data":{"contextUsage":{"tokens":90000,"contextWindow":100000}}}`,
				`{"type":"agent_settled"}`,
			)
			eventually(t, "one Pi fallback command", func() bool { return len(h.holders.inputs(holder, "compact")) == 1 })
			var command struct {
				Instructions string `json:"customInstructions"`
			}
			if err := json.Unmarshal([]byte(h.holders.inputs(holder, "compact")[0]), &command); err != nil || command.Instructions != tc.instruction {
				t.Fatalf("Pi fallback command = %+v, err %v", command, err)
			}
		})
	}
}

func TestCompactionRatioDefaultsAcrossContextWindows(t *testing.T) {
	for _, lead := range []bool{false, true} {
		c := &conv{r: NewRunner(Options{}), rec: Record{Lead: lead}}
		c.loadCompaction()
		for _, window := range []int64{128_000, 500_000, 1_000_000} {
			got := c.usageCompaction(window)
			if got == nil || got.Soft != window/2 || got.Hard != window*8/10 || len(got.Milestones) != 2 || got.Milestones[0] != window*6/10 || got.Milestones[1] != window*7/10 {
				t.Fatalf("lead=%v window=%d compaction thresholds %+v", lead, window, got)
			}
		}
	}
}
