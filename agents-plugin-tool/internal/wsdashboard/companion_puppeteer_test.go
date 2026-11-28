package wsdashboard

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wslive"
)

func TestCompanionDirectorBrainPuppeteerBusAndTriggers(t *testing.T) {
	calls := make(chan CompanionEphemeralRequest, 16)
	f := startPageFixture(t, func(o *DaemonOptions) {
		o.CompanionPool = fakeCompanionPool{
			exec: func(_ context.Context, req CompanionEphemeralRequest) (string, error) {
				calls <- req
				return `{"expression":"happy","gesture":"wave","fx":"sparkle","quip":"Watching Brain!","ttl_ms":3500}`, nil
			},
		}
	})
	f.d.config = &ConfigBackend{Options: wsconfig.Options{ConfigHome: t.TempDir()}}
	f.d.director.coalesceWindow = 25 * time.Millisecond
	f.d.director.idleAfter = 40 * time.Millisecond
	setCompanionConfig(f.d, CompanionConfig{
		Enabled:              true,
		Director:             "agent",
		Pack:                 "default",
		AgentCooldownSeconds: 10,
		AgentHourlyCap:       30,
	})
	f.d.SetCompanionPuppeteerTier(func() (CompanionPuppeteerTier, error) {
		return CompanionPuppeteerTier{
			Harness:  "agy",
			Model:    "agy-small",
			Effort:   "low",
			Provider: "google",
		}, nil
	})
	// Hold a phone lease AND focus a completely different tab: Brain Puppeteer
	// must still fire without requiring tab focus or dropping on embodiment lease.
	f.d.CompanionPhone().AcquireOnWake("legacy-holder")
	token := f.d.director.openStream()
	f.d.director.report(token, "other-tab-session", "default")

	rail := openSSE(t, f.base+"/rail/events")
	collect(rail)

	// Register Brain session -> emits handoff SSE.
	f.d.SetCompanionBrainReplyID("livesession")
	slot, ok := f.d.OpenHostedSlot(SessionDir(f.dir, "livesession"))
	if !ok {
		t.Fatal("OpenHostedSlot failed")
	}
	defer slot.Close()

	// Seed initial Brain snapshot.
	slot.Publish(wslive.Snapshot{SchemaVersion: 1, UpdatedAt: 1, Lead: wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}}})

	// Tab switch refocus must NOT reset brainWatch.
	f.d.director.report(token, "third-tab-session", "default")
	f.d.director.mu.Lock()
	brainSeeded := f.d.director.brainWatch.seeded && f.d.director.brainWatch.sessionID == "livesession"
	f.d.director.mu.Unlock()
	if !brainSeeded {
		t.Fatal("refocusLocked clobbered brainWatch")
	}

	// 1. brain_work_start (normal)
	prevSeq, _, _ := f.d.companionCommands.current()
	slot.Publish(wslive.Snapshot{SchemaVersion: 1, UpdatedAt: 2, Lead: wslive.Lead{TurnStartedAt: 100, Activity: wslive.Activity{Phase: wslive.PhaseThinking}}})
	select {
	case req := <-calls:
		if req.Harness != "agy" || req.Model != "agy-small" || req.Effort != "low" || req.Provider != "google" {
			t.Fatalf("injected puppeteer tier not used: %+v", req)
		}
		if !strings.Contains(req.Prompt, companionTriggerText[companionTriggerBrainWorkStart]) {
			t.Fatalf("prompt missing brain_work_start trigger:\n%s", req.Prompt)
		}
		if !strings.Contains(req.Prompt, "never reply in Messenger") {
			t.Fatalf("prompt missing side-comment rule:\n%s", req.Prompt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for brain_work_start call")
	}
	waitFor(t, "wildcard command publish", func() bool {
		next, payload, _ := f.d.companionCommands.current()
		if next == prevSeq {
			return false
		}
		var cmd CompanionCommand
		if err := json.Unmarshal(payload, &cmd); err != nil {
			return false
		}
		if cmd.SessionID != "*" || cmd.Source != "puppeteer" || cmd.Priority != companionPriorityNormal || cmd.Seq <= 0 {
			t.Errorf("unexpected brain command: %+v", cmd)
		}
		return true
	})

	// Wait for running slot to clear.
	waitFor(t, "running slot clear", func() bool {
		f.d.director.mu.Lock()
		defer f.d.director.mu.Unlock()
		return !f.d.director.running
	})

	// 2. High-priority brain_reply bypasses the 10s cooldown floor!
	slot.Publish(wslive.Snapshot{SchemaVersion: 1, UpdatedAt: 3, Lead: wslive.Lead{TurnStartedAt: 0, Activity: wslive.Activity{Phase: wslive.PhaseIdle}}})
	select {
	case req := <-calls:
		if !strings.Contains(req.Prompt, companionTriggerText[companionTriggerBrainReply]) {
			t.Fatalf("prompt missing brain_reply trigger:\n%s", req.Prompt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("high-priority brain_reply was blocked by cooldown")
	}

	// Verify rail SSE received companion-brain-state transitions.
	events, _ := collect(rail)
	var kinds []string
	for _, ev := range events {
		if ev.name != companionBrainEvent {
			continue
		}
		var st CompanionBrainStateEvent
		if err := json.Unmarshal([]byte(ev.data), &st); err != nil {
			t.Fatalf("decode companion-brain-state: %v", err)
		}
		kinds = append(kinds, st.Kind)
	}
	for _, want := range []string{"handoff", companionTriggerBrainWorkStart, companionTriggerBrainReply} {
		if !slices.Contains(kinds, want) {
			t.Fatalf("companion-brain-state SSE kinds = %v, missing %q", kinds, want)
		}
	}

	// Cleanup timers.
	f.d.SetCompanionBrainReplyID("")
}

func TestCompanionDirectorBrainCoalescingPreemptionAndCap(t *testing.T) {
	var (
		mu      sync.Mutex
		prompts []string
	)
	fired := make(chan string, 8)
	d := authFixture(t)
	d.config = &ConfigBackend{Options: wsconfig.Options{ConfigHome: t.TempDir()}}
	d.director.pool = fakeCompanionPool{
		exec: func(_ context.Context, req CompanionEphemeralRequest) (string, error) {
			mu.Lock()
			prompts = append(prompts, req.Prompt)
			mu.Unlock()
			fired <- req.Prompt
			return `{"quip":"ok","ttl_ms":2000}`, nil
		},
	}
	d.director.coalesceWindow = 300 * time.Millisecond
	setCompanionConfig(d, CompanionConfig{
		Enabled:              true,
		Director:             "agent",
		Pack:                 "default",
		AgentCooldownSeconds: 30,
		AgentHourlyCap:       2,
	})
	d.SetCompanionPuppeteerTier(func() (CompanionPuppeteerTier, error) {
		return CompanionPuppeteerTier{Harness: "agy", Model: "agy-small"}, nil
	})
	d.SetCompanionBrainReplyID("brainsess")

	// Enqueue normal trigger (starts 300ms coalesce window), then high-priority
	// user_message 10ms later -> high preempts immediately!
	start := time.Now()
	d.directorEnqueueBrain("brainsess", companionTriggerBrainWorkStart)
	d.directorEnqueueBrain("brainsess", companionTriggerSubagentEvent)
	d.directorEnqueueBrain("brainsess", companionTriggerUserMessage)

	select {
	case p := <-fired:
		if !strings.Contains(p, companionTriggerText[companionTriggerUserMessage]) {
			t.Fatalf("expected preempting user_message trigger, got:\n%s", p)
		}
		if elapsed := time.Since(start); elapsed >= 250*time.Millisecond {
			t.Fatalf("high-priority trigger waited %v instead of preempting immediately", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for preempted high-priority call")
	}

	waitFor(t, "first call done", func() bool {
		d.director.mu.Lock()
		defer d.director.mu.Unlock()
		return !d.director.running
	})

	// Second high-priority trigger (error) bypasses cooldown and fills hourly cap=2.
	d.directorEnqueueBrain("brainsess", companionTriggerError)
	select {
	case p := <-fired:
		if !strings.Contains(p, companionTriggerText[companionTriggerError]) {
			t.Fatalf("expected error trigger, got:\n%s", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for second high-priority call")
	}
	waitFor(t, "second call done", func() bool {
		d.director.mu.Lock()
		defer d.director.mu.Unlock()
		return !d.director.running
	})

	// Third high-priority trigger MUST still obey hourly cap=2!
	d.director.coalesceWindow = 10 * time.Millisecond
	d.directorEnqueueBrain("brainsess", companionTriggerBrainReply)
	waitFor(t, "cap drop recorded", func() bool {
		d.director.mu.Lock()
		defer d.director.mu.Unlock()
		return d.director.dropped["cap"] >= 1
	})
	select {
	case extra := <-fired:
		t.Fatalf("hourly cap was bypassed: %s", extra)
	case <-time.After(60 * time.Millisecond):
	}
	d.SetCompanionBrainReplyID("")
}

func TestCompanionDirectorBrainLoopPreventionAndSubagent(t *testing.T) {
	var w companionWatch
	// Seed
	w.observeBrain(&wslive.Snapshot{Lead: wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}}})

	// Self-originated Puppeteer tool batch must NOT fire brain_work_start or error.
	selfSnap := &wslive.Snapshot{
		Lead: wslive.Lead{
			TurnStartedAt: 10,
			Activity: wslive.Activity{
				Phase:  wslive.PhaseTool,
				Failed: 1,
				Batches: []wslive.BatchView{{
					Seq:     1,
					Running: &wslive.RunningView{ToolName: "ws/companion.act", ArgsPreview: `{"source":"puppeteer"}`},
				}},
			},
		},
	}
	if got := w.observeBrain(selfSnap); len(got) != 0 {
		t.Fatalf("self-originated snapshot triggered %v, want none", got)
	}

	// Subagent spawn & status change triggers subagent_event.
	subSnap := &wslive.Snapshot{
		Lead:   wslive.Lead{TurnStartedAt: 20, Activity: wslive.Activity{Phase: wslive.PhaseThinking}},
		Agents: []wslive.Agent{{ID: "child-1", Status: wslive.StatusRunning}},
	}
	got := w.observeBrain(subSnap)
	if !slices.Contains(got, companionTriggerBrainWorkStart) || !slices.Contains(got, companionTriggerSubagentEvent) {
		t.Fatalf("subagent + work start triggers = %v", got)
	}

	// Self-originated ring record detection.
	for _, rec := range []wslive.RingRecord{
		{RingEntry: wslive.RingEntry{Kind: wslive.RingUser, Source: "puppeteer", Text: "hi"}},
		{RingEntry: wslive.RingEntry{Kind: wslive.RingReply, Origin: "puppeteer", Text: "hi"}},
		{RingEntry: wslive.RingEntry{Kind: wslive.RingCommand, Command: "companion-command wave"}},
		{RingEntry: wslive.RingEntry{Kind: wslive.RingReply, Text: `{"source":"puppeteer","quip":"x"}`}},
	} {
		if !isPuppeteerSelfRing(rec) {
			t.Fatalf("expected isPuppeteerSelfRing=true for %+v", rec)
		}
	}
	if isPuppeteerSelfRing(wslive.RingRecord{RingEntry: wslive.RingEntry{Kind: wslive.RingUser, Text: "Hello Brain!"}}) {
		t.Fatal("normal user message falsely flagged as self-originated")
	}
}

func TestCompanionAllowedIDsCustomCatalogAndQuipClamp(t *testing.T) {
	pack := companionPackData{
		renderer: companionPackRendererVRM,
		dicts: map[string][]string{
			"expressions": append([]string(nil), companionVrmExpressions...),
			"gestures":    append([]string(nil), companionVrmGestures...),
			"props":       append([]string(nil), companionVrmProps...),
		},
	}
	catalog := &CompanionActionCatalog{
		Actions: []CompanionAction{
			{Key: "bow_deep", Description: "A polite deep bow"},
			{Key: "bad key\nwith newline", Description: "Must be stripped"},
		},
	}
	allowed := companionAllowedIDs(pack, catalog)
	if !slices.Contains(allowed["gesture"], "bow_deep") {
		t.Fatalf("custom action bow_deep missing from allowed gestures: %v", allowed["gesture"])
	}
	if slices.Contains(allowed["gesture"], "bad key\nwith newline") {
		t.Fatalf("hostile custom action leaked into allowed gestures: %v", allowed["gesture"])
	}

	longQuip := strings.Repeat("가", CompanionQuipMaxRunes+25)
	reply := `{"gesture":"bow_deep","expression":"happy","quip":"` + longQuip + `","ttl_ms":4000}`
	cmd, ok := parseCompanionReply(reply, allowed)
	if !ok {
		t.Fatal("parseCompanionReply rejected custom action bow_deep")
	}
	if cmd.Gesture != "bow_deep" || cmd.Expression != "happy" {
		t.Fatalf("parsed cmd = %+v, want gesture=bow_deep expression=happy", cmd)
	}
	if got := utf8.RuneCountInString(cmd.Quip); got != CompanionQuipMaxRunes {
		t.Fatalf("clamped quip runes = %d, want %d", got, CompanionQuipMaxRunes)
	}

	prompt := companionPromptWithCatalog(companionTriggerUserMessage, &CompanionPersona{Name: "Luna", Voice: "Warm"}, "ko", allowed, catalog, "turn: active")
	if !strings.Contains(prompt, "bow_deep: A polite deep bow") {
		t.Fatalf("prompt missing custom action catalog description:\n%s", prompt)
	}
}

func TestCompanionActPureCosmeticZeroWaitAndZeroUnreadDrain(t *testing.T) {
	f := startPageFixture(t)
	rail := openSSE(t, f.base+"/rail/events")
	collect(rail)

	// Enqueue an unread user message to prove cosmetic act does NOT consume it.
	if _, _, err := f.d.CompanionPhone().EnqueueUserMessage("Do not drain me"); err != nil {
		t.Fatalf("EnqueueUserMessage: %v", err)
	}
	var timerStarted atomic.Int32
	f.d.SetCompanionActTimer(func(time.Duration) (<-chan time.Time, func()) {
		timerStarted.Add(1)
		return make(chan time.Time), func() {}
	})

	// Case 1: No phone lease active -> pure cosmetic zero wait.
	res1, err := f.d.CompanionAct(context.Background(), "legacy-owner", CompanionActRequest{
		Expression: "happy",
		Gesture:    "phone_pickup",
		Speech:     "Zero wait!",
		Priority:   "high",
	})
	if err != nil {
		t.Fatalf("CompanionAct no-lease error: %v", err)
	}
	if res1.WakeReason != CompanionActWakeCosmetic || res1.DelayMS != 0 {
		t.Fatalf("no-lease act result = %+v, want cosmetic/0", res1)
	}
	if timerStarted.Load() != 0 {
		t.Fatalf("pure-cosmetic act started blocking timer %d times", timerStarted.Load())
	}
	if got := f.d.CompanionPhone().UnreadAgentCount(); got != 1 {
		t.Fatalf("pure-cosmetic act drained unread inbox: %d", got)
	}
	if f.d.CompanionPhone().EmbodimentLeaseActive() {
		t.Fatal("pure-cosmetic act mutated phone lease")
	}

	// Case 2: Phone lease IS active, but CosmeticOnly=true -> still pure cosmetic zero wait!
	f.d.CompanionPhone().AcquireOnWake("lease-owner")
	res2, err := f.d.CompanionAct(context.Background(), "lease-owner", CompanionActRequest{
		Expression:   "wink",
		Gesture:      "wave",
		CosmeticOnly: true,
		Priority:     "normal",
	})
	if err != nil {
		t.Fatalf("CompanionAct CosmeticOnly error: %v", err)
	}
	if res2.WakeReason != CompanionActWakeCosmetic || res2.DelayMS != 0 || timerStarted.Load() != 0 {
		t.Fatalf("CosmeticOnly act result=%+v timerStarted=%d", res2, timerStarted.Load())
	}

	// Verify rail broadcast carries monotonic Seq and Priority.
	events, _ := collect(rail)
	var cmds []CompanionCommand
	for _, ev := range events {
		if ev.name != companionCommandEvent {
			continue
		}
		var cmd CompanionCommand
		if err := json.Unmarshal([]byte(ev.data), &cmd); err != nil {
			t.Fatalf("decode companion-command: %v", err)
		}
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 0 {
		t.Fatal("expected companion-command SSE broadcast")
	}
	last := cmds[len(cmds)-1]
	if last.Seq <= 0 || last.Source != "act" || last.Expression != "wink" {
		t.Fatalf("unexpected broadcast command: %+v", last)
	}
}
