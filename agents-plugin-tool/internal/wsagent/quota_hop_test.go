package wsagent

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
)

type hopProc struct {
	fakeSteerProc
	mu         sync.Mutex
	lastTurnID string
}

func (p *hopProc) submitTurn(id, text string, steers []durableSteer, interactive bool) error {
	p.mu.Lock()
	p.lastTurnID = id
	p.mu.Unlock()
	return p.Send(text)
}

func (p *hopProc) turnID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastTurnID
}

type hopBackend struct {
	*fakeBackend
	durable bool
	mu      sync.Mutex
	byProc  map[*fakeProc]*hopProc
}

func (b *hopBackend) Start(ctx context.Context, spec StartSpec) (Process, error) {
	if _, err := b.fakeBackend.Start(ctx, spec); err != nil {
		return nil, err
	}
	fp := b.fakeBackend.proc(b.fakeBackend.startCount() - 1)
	hp := &hopProc{fakeSteerProc: fakeSteerProc{fp}}
	b.mu.Lock()
	if b.byProc == nil {
		b.byProc = map[*fakeProc]*hopProc{}
	}
	b.byProc[fp] = hp
	b.mu.Unlock()
	if b.durable {
		fp.events <- Event{SessionID: "sess-hop"}
	}
	return hp, nil
}

func (b *hopBackend) startLoop(ctx context.Context, spec StartSpec) (Process, error) {
	return b.Start(ctx, spec)
}

func (b *hopBackend) turnIDOf(fp *fakeProc) string {
	b.mu.Lock()
	hp := b.byProc[fp]
	b.mu.Unlock()
	if hp == nil {
		return ""
	}
	return hp.turnID()
}

type hopTestEnv struct {
	h          *harness
	claudeBack *hopBackend
	werkBack   *hopBackend
	got        *reports
	nowMu      sync.Mutex
	now        time.Time
	timers     *leverTimers
	reset      func(backend, model string) int64
}

func newHopTestEnv(t *testing.T, withAlternate bool, cfgOverrides map[string]string) *hopTestEnv {
	t.Helper()
	setLookPath(t, func(file string) (string, error) { return file, nil })
	h := newHarness(t, map[string]string{
		"chat": chatProfile,
	})
	h.backend.steers = true
	claudeBack := &hopBackend{fakeBackend: h.backend, durable: true}
	werkBack := &hopBackend{fakeBackend: h.backend, durable: false}
	env := &hopTestEnv{
		h:          h,
		claudeBack: claudeBack,
		werkBack:   werkBack,
		got:        &reports{},
		now:        time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC),
		timers:     &leverTimers{},
	}
	h.config.Now = func() time.Time {
		env.nowMu.Lock()
		defer env.nowMu.Unlock()
		return env.now
	}
	h.config.Timer = env.timers.start
	h.config.LeadKeys = &fakeLeadKeys{}
	h.config.DaemonMCP = func(string) (MCPCommand, error) { return MCPCommand{}, nil }
	h.config.QuotaContinuationJitter = func(time.Duration) time.Duration { return 0 }
	h.config.QuotaResetAt = func(backend, model string) int64 {
		env.nowMu.Lock()
		fn := env.reset
		env.nowMu.Unlock()
		if fn == nil {
			return 0
		}
		return fn(backend, model)
	}
	h.config.Report = env.got.add
	h.config.Backends[BackendClaude] = BackendRegistration{
		Backend:         claudeBack,
		ProcessKind:     ProcessDurable,
		Permission:      builtinPermission(BackendClaude),
		Lifecycle:       builtinLifecycle(BackendClaude),
		LeadProfile:     "lead-claude",
		PermissionModes: []string{"auto", "default"},
		WorkerCapable:   true,
		WorkerProfile:   AgentWorkerProfile(BackendClaude),
		WorkerHTTPMCP:   true,
	}
	if len(cfgOverrides) > 0 {
		if err := os.MkdirAll(h.config.Config.ConfigHome, 0o755); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(map[string]any{"overrides": cfgOverrides})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.config.Config.ConfigHome, "config.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if withAlternate {
		h.config.Backends[BackendWerk] = BackendRegistration{
			Backend:       werkBack,
			WorkerCapable: true,
			WorkerProfile: AgentWorkerProfile(BackendWerk),
		}
		for tier, model := range map[string]string{"small": "prov/small-model", "medium": "prov/medium-model", "large": "prov/large-model"} {
			if _, err := wsconfig.SetGlobalAgentsTierForHarness(h.config.Config, tier, "", model, BackendWerk, "low"); err != nil {
				t.Fatal(err)
			}
		}
	}
	for tier, model := range map[string]string{"small": "claude-small", "medium": "claude-medium", "large": "claude-large"} {
		if _, err := wsconfig.SetGlobalAgentsTierForHarness(h.config.Config, tier, "", model, BackendClaude, "high"); err != nil {
			t.Fatal(err)
		}
	}
	h.endpoint = "http://127.0.0.1:1"
	h.runner.Close()
	h.start()
	return env
}

func (e *hopTestEnv) currentNow() time.Time {
	e.nowMu.Lock()
	defer e.nowMu.Unlock()
	return e.now
}

func (e *hopTestEnv) advance(d time.Duration) {
	e.nowMu.Lock()
	e.now = e.now.Add(d)
	e.nowMu.Unlock()
}

func (e *hopTestEnv) setQuotaResetAt(fn func(backend, model string) int64) {
	e.nowMu.Lock()
	e.reset = fn
	e.nowMu.Unlock()
}

func (e *hopTestEnv) record(t *testing.T, id string) Record {
	t.Helper()
	c := e.h.runner.conv(id)
	if c == nil {
		t.Fatalf("conversation %q not found", id)
	}
	var rec Record
	c.do(func() { rec = c.rec })
	return rec
}

func (e *hopTestEnv) fireLastTimer(t *testing.T) {
	t.Helper()
	e.timers.mu.Lock()
	idx := len(e.timers.durs) - 1
	e.timers.mu.Unlock()
	if idx < 0 {
		t.Fatal("expected armed timer, none started")
	}
	e.timers.fire(t, idx)
}

func (e *hopTestEnv) turnID(fp *fakeProc) string {
	if tid := e.claudeBack.turnIDOf(fp); tid != "" {
		return tid
	}
	return e.werkBack.turnIDOf(fp)
}

func (e *hopTestEnv) endTurn(fp *fakeProc) {
	tid := e.turnID(fp)
	if tid != "" {
		fp.events <- Event{TurnStart: true, TurnID: tid}
		fp.events <- Event{TurnEnd: true, TurnID: tid}
		return
	}
	fp.events <- Event{TurnEnd: true}
}

func (e *hopTestEnv) failWithUsageLimit(fp *fakeProc, text string, resetsAt int64) {
	tid := e.turnID(fp)
	if tid != "" {
		fp.events <- Event{TurnStart: true, TurnID: tid}
	}
	fp.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: text, ResetsAt: resetsAt}, TurnID: tid}
	fp.events <- Event{TurnEnd: true, TurnError: text, TurnID: tid}
}

func (e *hopTestEnv) createHostedLead(t *testing.T) (State, *fakeProc) {
	t.Helper()
	before := e.h.backend.startCount()
	st, err := e.h.runner.CreateLead(BackendClaude, e.h.root, LeadOptions{Model: "claude-large"})
	if err != nil {
		t.Fatalf("CreateLead: %v", err)
	}
	if _, err := e.h.runner.Send(st.ID, "start"); err != nil {
		t.Fatalf("Send lead start: %v", err)
	}
	proc := e.h.backend.waitProc(t, before)
	proc.nextSent(t)
	e.endTurn(proc)
	eventually(t, "hosted lead idle", func() bool {
		return e.h.state(st.ID).Status == StatusIdle
	})
	return e.h.state(st.ID), proc
}

func (e *hopTestEnv) spawnDirectWorker(t *testing.T, parent ParentRef, backend, label, tier string) (State, *fakeProc, string) {
	t.Helper()
	before := e.h.backend.startCount()
	p := parent
	model := backend + "-" + tier
	if backend == BackendWerk {
		model = "prov/" + tier + "-model"
	}
	st, err := e.h.runner.Spawn(SpawnSpec{
		Profile:            AgentWorkerProfile(backend),
		PermissionLevel:    PermissionWorkdirWrite,
		delegatedAuthority: true,
		Root:               e.h.root,
		Prompt:             "work",
		Tier:               tier,
		Model:              model,
		Label:              label,
		Parent:             &p,
	})
	if err != nil {
		t.Fatalf("Spawn %s: %v", label, err)
	}
	proc := e.h.backend.waitProc(t, before)
	tok := e.h.token(before)
	proc.nextSent(t)
	return e.h.state(st.ID), proc, tok
}

func (e *hopTestEnv) spawnNestedChild(t *testing.T, parentTok, tier, label, prompt string) (State, *fakeProc, string) {
	t.Helper()
	before := e.h.backend.startCount()
	code, body := e.h.tokenCall(parentTok, "agent.spawn", map[string]any{
		"prompt":    prompt,
		"tier":      tier,
		"authority": "workdir-write",
		"label":     label,
	})
	if code != http.StatusCreated {
		t.Fatalf("token spawn %s = %d %s", label, code, body)
	}
	var res AgentSpawnResult
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatal(err)
	}
	proc := e.h.backend.waitProc(t, before)
	tok := e.h.token(before)
	proc.nextSent(t)
	return e.h.state(agentID(res.Address)), proc, tok
}

func (e *hopTestEnv) reportsFor(parentReplyID string) []TurnReport {
	var out []TurnReport
	for _, rep := range e.got.list() {
		if rep.Parent.ReplyID == parentReplyID {
			out = append(out, rep)
		}
	}
	return out
}

func (e *hopTestEnv) reportsForChild(parentReplyID, childID string) []TurnReport {
	var out []TurnReport
	for _, rep := range e.got.list() {
		if rep.Parent.ReplyID == parentReplyID && rep.Child.ID == childID {
			out = append(out, rep)
		}
	}
	return out
}

func TestQuotaHopWorkerAlertNoLeadContinuation(t *testing.T) {
	env := newHopTestEnv(t, true, map[string]string{
		wsconfig.ItemAgentQuotaContinuation + "." + BackendClaude: "auto",
	})
	lead, _ := env.createHostedLead(t)
	worker, proc, _ := env.spawnDirectWorker(t, ParentRef{Kind: ParentLead, ReplyID: lead.ID}, BackendClaude, "worker-a", "large")

	resetsAt := env.currentNow().Add(2 * time.Hour).UnixMilli()
	env.failWithUsageLimit(proc, "You've hit your 5-hour limit", resetsAt)

	eventually(t, "worker failed", func() bool {
		st := env.h.state(worker.ID)
		return st.Status == StatusIdle && st.Error != ""
	})
	st := env.h.state(worker.ID)
	if st.Failure != nil {
		t.Fatalf("worker state must not expose lead-only Failure chip: %+v", st.Failure)
	}
	if st.QuotaContinuation != nil {
		t.Fatalf("worker state must not expose QuotaContinuation: %+v", st.QuotaContinuation)
	}
	rec := env.record(t, worker.ID)
	if rec.TurnOpen {
		t.Fatalf("worker TurnOpen must be closed after usage-limit failure")
	}
	if rec.QuotaContinuation != nil {
		t.Fatalf("worker record must not schedule QuotaContinuation: %+v", rec.QuotaContinuation)
	}
	if rec.LastTurn == nil || rec.LastTurn.Result.Status != TurnFailed {
		t.Fatalf("worker LastTurn = %+v, want TurnFailed", rec.LastTurn)
	}
	env.h.runner.conv(worker.ID).do(func() {})
	if snap, err := wslive.ReadSnapshot(wslive.SnapshotPath(wsdashboard.SessionDir(env.h.dir, worker.ID))); err == nil && snap.Host != nil {
		if snap.Host.Failure != nil || snap.Host.QuotaContinuation != nil {
			t.Fatalf("worker live Host must not expose lead chips: %+v", snap.Host)
		}
	}
}

func TestQuotaHopDirectHealthyParentsAndReportShape(t *testing.T) {
	t.Run("hosted lead receives cron and alternate tier guidance", func(t *testing.T) {
		env := newHopTestEnv(t, true, nil)
		lead, _ := env.createHostedLead(t)
		worker, proc, _ := env.spawnDirectWorker(t, ParentRef{Kind: ParentLead, ReplyID: lead.ID}, BackendClaude, "impl-worker", "large")

		resetsAt := env.currentNow().Add(90 * time.Minute).UnixMilli()
		env.failWithUsageLimit(proc, "5-hour usage limit reached", resetsAt)

		eventually(t, "single exhaustion report to hosted lead", func() bool {
			return len(env.reportsFor(lead.ID)) == 1
		})
		time.Sleep(50 * time.Millisecond)
		reps := env.reportsFor(lead.ID)
		if len(reps) != 1 {
			t.Fatalf("want exactly 1 report (no duplicate), got %d: %+v", len(reps), reps)
		}
		text := reps[0].Text
		if !strings.HasPrefix(text, "QUOTA EXHAUSTED: impl-worker ("+AgentAddress(worker.ID)+")\n") {
			t.Fatalf("unexpected header: %q", text)
		}
		wantAt := time.UnixMilli(resetsAt).UTC().Add(time.Minute).Truncate(time.Second).Format(time.RFC3339)
		for _, want := range []string{
			"backend: claude\n",
			"model: claude-large\n",
			"error: 5-hour usage limit reached\n",
			"resets_at: " + time.UnixMilli(resetsAt).UTC().Format(time.RFC3339) + " (",
			"skipped_ancestors: none\n",
			"action:\n",
			`mailbox.send(to: "` + AgentAddress(worker.ID) + `")`,
			`ws/cron add {name, at: "` + wantAt + `", prompt}`,
			"agent.spawn with tier ",
			"werk.small",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("exhaustion report missing %q:\n%s", want, text)
			}
		}
		if strings.Contains(text, "claude.small") {
			t.Fatalf("alternate tiers must exclude exhausted backend claude:\n%s", text)
		}
	})

	t.Run("external lead omits ws/cron hint and unknown reset + no alternate omits both hints", func(t *testing.T) {
		env := newHopTestEnv(t, false, nil)
		extParent := ParentRef{Kind: ParentLead, ReplyID: "extlead1"}
		worker, proc, _ := env.spawnDirectWorker(t, extParent, BackendClaude, "ext-worker", "small")

		env.failWithUsageLimit(proc, "usage limit hit", 0)

		eventually(t, "single exhaustion report to external lead", func() bool {
			return len(env.reportsFor(extParent.ReplyID)) == 1
		})
		time.Sleep(50 * time.Millisecond)
		reps := env.reportsFor(extParent.ReplyID)
		if len(reps) != 1 {
			t.Fatalf("want exactly 1 report to external lead, got %d", len(reps))
		}
		text := reps[0].Text
		if !strings.HasPrefix(text, "QUOTA EXHAUSTED: ext-worker ("+AgentAddress(worker.ID)+")\n") {
			t.Fatalf("unexpected header: %q", text)
		}
		if !strings.Contains(text, "resets_at: unknown\n") {
			t.Fatalf("expected resets_at: unknown:\n%s", text)
		}
		if strings.Contains(text, "ws/cron") {
			t.Fatalf("external lead must not get ws/cron hint:\n%s", text)
		}
		if strings.Contains(text, "agent.spawn with tier") {
			t.Fatalf("must omit alternate-harness hint when no alternate harness is available:\n%s", text)
		}
	})

	t.Run("external lead with resets_at still omits ws/cron but includes alternate harness", func(t *testing.T) {
		env := newHopTestEnv(t, true, nil)
		extParent := ParentRef{Kind: ParentLead, ReplyID: "extlead2"}
		_, proc, _ := env.spawnDirectWorker(t, extParent, BackendClaude, "ext-worker-2", "small")

		env.failWithUsageLimit(proc, "usage limit hit", env.currentNow().Add(time.Hour).UnixMilli())

		eventually(t, "exhaustion report to external lead 2", func() bool {
			return len(env.reportsFor(extParent.ReplyID)) == 1
		})
		text := env.reportsFor(extParent.ReplyID)[0].Text
		if strings.Contains(text, "ws/cron") {
			t.Fatalf("external lead must not get ws/cron hint even with resets_at:\n%s", text)
		}
		if !strings.Contains(text, "agent.spawn with tier ") || !strings.Contains(text, "werk.small") {
			t.Fatalf("expected alternate werk tiers in external lead report:\n%s", text)
		}
	})
}

func TestQuotaHopSkipsExhaustedSubLeadsWithoutWakingOrSticking(t *testing.T) {
	env := newHopTestEnv(t, true, nil)
	lead, _ := env.createHostedLead(t)

	// Chain: lead (healthy) <- sub1 (exhausted via QuotaResetAt) <- sub2 (exhausted via failure) <- leaf
	sub1, sub1Proc, sub1Tok := env.spawnDirectWorker(t, ParentRef{Kind: ParentLead, ReplyID: lead.ID}, BackendClaude, "sub-1", "medium")
	sub2, sub2Proc, sub2Tok := env.spawnNestedChild(t, sub1Tok, "claude.small", "sub-2", "manage leaf")
	leaf, leafProc, _ := env.spawnNestedChild(t, sub2Tok, "claude.small", "leaf-worker", "do leaf work")

	// sub1 finishes its initial turn (held waiting for sub2), mark its model exhausted via QuotaResetAt.
	env.endTurn(sub1Proc)
	// sub2 hits usage limit (signal 1) with future reset.
	resetsAt := env.currentNow().Add(2 * time.Hour).UnixMilli()
	env.setQuotaResetAt(func(backend, model string) int64 {
		if backend == BackendClaude && model == "claude-medium" {
			return resetsAt
		}
		return 0
	})
	env.failWithUsageLimit(sub2Proc, "sub2 usage limit", resetsAt)
	eventually(t, "sub2 exhausted and hopped to lead", func() bool {
		return len(env.reportsForChild(lead.ID, sub2.ID)) == 1
	})
	// sub1 must not have been woken by sub2's exhaustion either.
	noSend(t, sub1Proc, "exhausted sub1 must not wake for sub2")

	// Now leaf hits usage limit: must skip BOTH sub2 (failure) and sub1 (QuotaResetAt) and reach top-lead.
	env.failWithUsageLimit(leafProc, "leaf usage limit", resetsAt)
	eventually(t, "leaf exhaustion hopped to top-lead", func() bool {
		return len(env.reportsForChild(lead.ID, leaf.ID)) == 1
	})
	noSend(t, sub2Proc, "exhausted sub2 must not wake for leaf")
	noSend(t, sub1Proc, "exhausted sub1 must not wake for leaf")

	// Verify leaf settled cleanly, PendingReports cleared, and openChildOf(sub2) is false.
	eventually(t, "leaf settled and unblocked", func() bool {
		rec := env.record(t, leaf.ID)
		return !rec.TurnOpen && len(rec.PendingReports) == 0 && !env.h.runner.conv(leaf.ID).openChildOf(ParentRef{Kind: ParentConversation, ReplyID: sub2.ID})
	})
	leafReps := env.reportsForChild(lead.ID, leaf.ID)
	if len(leafReps) != 1 {
		t.Fatalf("expected 1 hopped report for leaf, got %d", len(leafReps))
	}
	rep := leafReps[0]
	wantSkipped := "skipped_ancestors: " + AgentAddress(sub2.ID) + ", " + AgentAddress(sub1.ID) + "\n"
	if !strings.HasPrefix(rep.Text, "QUOTA EXHAUSTED: leaf-worker ("+AgentAddress(leaf.ID)+")\n") {
		t.Fatalf("unexpected hopped header: %q", rep.Text)
	}
	if !strings.Contains(rep.Text, wantSkipped) {
		t.Fatalf("want %q in hopped report:\n%s", wantSkipped, rep.Text)
	}
}

func TestQuotaHopStopsAtHealthyNonLeadSubWorker(t *testing.T) {
	env := newHopTestEnv(t, true, nil)
	lead, _ := env.createHostedLead(t)

	// Chain: lead <- healthySub (werk, non-lead) <- exhaustedSub (claude) <- leaf (claude)
	healthySub, healthyProc, healthyTok := env.spawnDirectWorker(t, ParentRef{Kind: ParentLead, ReplyID: lead.ID}, BackendWerk, "healthy-sub", "medium")
	exhaustedSub, exhaustedProc, exhaustedTok := env.spawnNestedChild(t, healthyTok, "claude.small", "exhausted-sub", "run leaf")
	leaf, leafProc, _ := env.spawnNestedChild(t, exhaustedTok, "claude.small", "leaf-worker", "do work")

	// healthySub finishes its first turn (held for children), exhaustedSub hits quota and hops to healthySub.
	env.endTurn(healthyProc)
	resetsAt := env.currentNow().Add(time.Hour).UnixMilli()
	env.failWithUsageLimit(exhaustedProc, "sub quota hit", resetsAt)
	// healthySub takes exhaustedSub's report and finishes that turn.
	msg1 := healthyProc.nextSent(t)
	if !strings.HasPrefix(msg1, "QUOTA EXHAUSTED: exhausted-sub ("+AgentAddress(exhaustedSub.ID)+")\n") {
		t.Fatalf("healthySub first exhaustion report = %q", msg1)
	}
	env.endTurn(healthyProc)

	// Now leaf hits quota: must skip exhaustedSub and land on healthySub (not top-lead).
	env.failWithUsageLimit(leafProc, "leaf quota hit", resetsAt)
	msg2 := healthyProc.nextSent(t)
	if !strings.HasPrefix(msg2, "QUOTA EXHAUSTED: leaf-worker ("+AgentAddress(leaf.ID)+")\n") {
		t.Fatalf("healthySub hopped report = %q", msg2)
	}
	if !strings.Contains(msg2, "skipped_ancestors: "+AgentAddress(exhaustedSub.ID)+"\n") {
		t.Fatalf("missing skipped_ancestors for exhaustedSub:\n%s", msg2)
	}
	if strings.Contains(msg2, "ws/cron") {
		t.Fatalf("non-lead sub-worker recipient must not get ws/cron hint:\n%s", msg2)
	}
	noSend(t, exhaustedProc, "exhaustedSub must not wake for leaf")
	time.Sleep(50 * time.Millisecond)
	for _, rep := range env.reportsFor(lead.ID) {
		if rep.Child.ID == leaf.ID || rep.Child.ID == exhaustedSub.ID || strings.Contains(rep.Text, "QUOTA EXHAUSTED") {
			t.Fatalf("top-lead must not receive quota report when healthy non-lead sub-worker took it: %+v", rep)
		}
	}
	_ = healthySub
}

func TestQuotaHopSignalsInFlightAndElapsedResets(t *testing.T) {
	env := newHopTestEnv(t, true, nil)
	lead, _ := env.createHostedLead(t)
	leadConv := env.h.runner.conv(lead.ID)

	// Signal 2: QuotaContinuationInFlight counts as exhausted (and is NOT parkable, so delivers immediately as ALL ANCESTORS).
	leadConv.do(func() {
		leadConv.update(func(rec *Record) {
			rec.QuotaContinuation = &QuotaContinuation{Status: QuotaContinuationInFlight}
		})
	})
	w1, p1, _ := env.spawnDirectWorker(t, ParentRef{Kind: ParentLead, ReplyID: lead.ID}, BackendClaude, "w-inflight", "small")
	env.failWithUsageLimit(p1, "limit", env.currentNow().Add(time.Hour).UnixMilli())
	eventually(t, "report delivered while lead InFlight", func() bool {
		return len(env.reportsFor(lead.ID)) == 1
	})
	if !strings.HasPrefix(env.reportsFor(lead.ID)[0].Text, "ALL ANCESTORS QUOTA-EXHAUSTED: w-inflight ("+AgentAddress(w1.ID)+")\n") {
		t.Fatalf("InFlight lead must be treated as exhausted: %q", env.reportsFor(lead.ID)[0].Text)
	}

	// Elapsed failure reset (ResetsAt <= now) and elapsed scheduled ResumeAt <= now are NOT exhausted.
	past := env.currentNow().Add(-time.Minute).UnixMilli()
	leadConv.do(func() {
		leadConv.setStatus(func() {
			leadConv.failure = &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "old", ResetsAt: past}
		})
		leadConv.update(func(rec *Record) {
			rec.QuotaContinuation = &QuotaContinuation{Status: QuotaContinuationScheduled, ResumeAt: past}
		})
	})
	w2, p2, _ := env.spawnDirectWorker(t, ParentRef{Kind: ParentLead, ReplyID: lead.ID}, BackendClaude, "w-elapsed", "small")
	env.failWithUsageLimit(p2, "limit", env.currentNow().Add(time.Hour).UnixMilli())
	eventually(t, "report delivered after elapsed reset", func() bool {
		return len(env.reportsFor(lead.ID)) == 2
	})
	if !strings.HasPrefix(env.reportsFor(lead.ID)[1].Text, "QUOTA EXHAUSTED: w-elapsed ("+AgentAddress(w2.ID)+")\n") {
		t.Fatalf("elapsed reset/resume must count as healthy: %q", env.reportsFor(lead.ID)[1].Text)
	}
}

func TestQuotaHopCyclicParentChainTerminates(t *testing.T) {
	env := newHopTestEnv(t, true, nil)
	lead, _ := env.createHostedLead(t)

	subA, procA, tokA := env.spawnDirectWorker(t, ParentRef{Kind: ParentLead, ReplyID: lead.ID}, BackendClaude, "sub-a", "small")
	subB, procB, tokB := env.spawnNestedChild(t, tokA, "claude.small", "sub-b", "work")
	leaf, procLeaf, _ := env.spawnNestedChild(t, tokB, "claude.small", "leaf", "work")

	// Finish subA's initial turn first so settling does not clear its synthetic exhaustion state.
	// Keep subB's initial turn open so subB does not emit a turn-done report first.
	env.endTurn(procA)
	eventually(t, "subA idle", func() bool {
		return env.h.state(subA.ID).Status == StatusIdle
	})

	// Mark both subA and subB exhausted, then create a synthetic cycle subA.Parent -> subB (and subB.Parent -> subA).
	resetsAt := env.currentNow().Add(time.Hour).UnixMilli()
	env.h.runner.conv(subA.ID).do(func() {
		c := env.h.runner.conv(subA.ID)
		c.setStatus(func() { c.failure = &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "a limit", ResetsAt: resetsAt} })
		c.update(func(rec *Record) { rec.Parent = &ParentRef{Kind: ParentConversation, ReplyID: subB.ID} })
	})
	env.h.runner.conv(subB.ID).do(func() {
		c := env.h.runner.conv(subB.ID)
		c.setStatus(func() { c.failure = &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "b limit", ResetsAt: resetsAt} })
	})

	env.failWithUsageLimit(procLeaf, "leaf limit", resetsAt)
	// Walk: leaf -> subB (exhausted) -> subA (exhausted) -> subB (seen! stops) -> outermost reached is subA.
	got := procA.nextSent(t)
	if !strings.HasPrefix(got, "ALL ANCESTORS QUOTA-EXHAUSTED: leaf ("+AgentAddress(leaf.ID)+")\n") {
		t.Fatalf("cyclic chain fallback report = %q", got)
	}
	if !strings.Contains(got, "skipped_ancestors: "+AgentAddress(subB.ID)+"\n") {
		t.Fatalf("expected subB in skipped_ancestors:\n%s", got)
	}
	_ = procB
}

func TestQuotaHopConcurrentAncestorMutationRace(t *testing.T) {
	env := newHopTestEnv(t, true, nil)
	lead, _ := env.createHostedLead(t)
	leadConv := env.h.runner.conv(lead.ID)

	sub, subProc, subTok := env.spawnDirectWorker(t, ParentRef{Kind: ParentLead, ReplyID: lead.ID}, BackendClaude, "sub", "small")
	subConv := env.h.runner.conv(sub.ID)
	env.endTurn(subProc)

	const workers = 6
	type wpair struct {
		id   string
		proc *fakeProc
	}
	var list []wpair
	for range workers {
		st, p, _ := env.spawnNestedChild(t, subTok, "claude.small", "race-w", "work")
		list = append(list, wpair{id: st.ID, proc: p})
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		future := env.currentNow().Add(time.Hour).UnixMilli()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			subConv.do(func() {
				if i%2 == 0 {
					subConv.setStatus(func() {
						subConv.failure = &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "busy", ResetsAt: future}
					})
				} else {
					subConv.setStatus(func() { subConv.failure = nil })
				}
			})
			leadConv.do(func() {
				if i%2 == 0 {
					leadConv.setStatus(func() {
						leadConv.failure = &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "busy", ResetsAt: future}
					})
				} else {
					leadConv.setStatus(func() { leadConv.failure = nil })
				}
			})
		}
	}()

	resetsAt := env.currentNow().Add(time.Hour).UnixMilli()
	for _, w := range list {
		env.failWithUsageLimit(w.proc, "quota race", resetsAt)
	}
	for _, w := range list {
		eventually(t, "race worker settled", func() bool {
			rec := env.record(t, w.id)
			return !rec.TurnOpen && len(rec.PendingReports) == 0
		})
	}
	close(stop)
	wg.Wait()
}

func TestQuotaHopAllExhaustedParksSurvivesNoInputKicksRestartAndFiresWithContinuation(t *testing.T) {
	env := newHopTestEnv(t, true, map[string]string{
		wsconfig.ItemAgentQuotaContinuation + "." + BackendClaude: "auto",
	})
	lead, leadProc := env.createHostedLead(t)
	leadConv := env.h.runner.conv(lead.ID)

	// Put the hosted lead into usage-limit failure so auto QuotaContinuation schedules.
	resetsAt := env.currentNow().Add(10 * time.Minute).UnixMilli()
	if _, err := env.h.runner.Send(lead.ID, "work"); err != nil {
		t.Fatal(err)
	}
	leadProc.nextSent(t)
	env.failWithUsageLimit(leadProc, "lead quota hit", resetsAt)
	eventually(t, "lead continuation scheduled", func() bool {
		qc := env.h.state(lead.ID).QuotaContinuation
		return qc != nil && qc.Status == QuotaContinuationScheduled && qc.ResumeAt > env.currentNow().UnixMilli()
	})

	// Spawn a direct child and fail it on quota.
	directW, directProc, _ := env.spawnDirectWorker(t, ParentRef{Kind: ParentLead, ReplyID: lead.ID}, BackendClaude, "direct-w", "small")
	env.failWithUsageLimit(directProc, "direct worker quota", resetsAt)

	eventually(t, "exhaustion report parked on lead", func() bool {
		rec := env.record(t, lead.ID)
		return len(rec.Reports) == 1 && strings.HasPrefix(rec.Reports[0], "ALL ANCESTORS QUOTA-EXHAUSTED: direct-w ("+AgentAddress(directW.ID)+")\n")
	})

	// Zero Options.Report wakeups, continuation still Scheduled, no turn started.
	if len(env.reportsFor(lead.ID)) != 0 {
		t.Fatalf("parked exhaustion must not emit Options.Report: %+v", env.reportsFor(lead.ID))
	}
	if qc := env.h.state(lead.ID).QuotaContinuation; qc == nil || qc.Status != QuotaContinuationScheduled {
		t.Fatalf("parking must not cancel continuation: %+v", qc)
	}
	noSend(t, leadProc, "parking must not start a turn before ResumeAt")

	// No-input kicks on the live idle lead must NOT start a turn on parked reports alone.
	leadConv.signalInbox() // inboxChanged with held == 0
	leadConv.do(func() {
		if leadConv.hasPendingKick() {
			t.Errorf("hasPendingKick must be false when only parked reports exist")
		}
		leadConv.kick()
	})
	bgZero := 0
	leadProc.events <- Event{BackgroundTasks: &bgZero}
	noSend(t, leadProc, "no-input kicks and bg-task settlement must not wake parked lead")
	if qc := env.h.state(lead.ID).QuotaContinuation; qc == nil || qc.Status != QuotaContinuationScheduled {
		t.Fatalf("no-input kicks must not cancel continuation: %+v", qc)
	}

	// Simulate daemon restart while continuation is still Scheduled (before ResumeAt).
	env.h.runner.Close()
	env.timers = &leverTimers{}
	env.h.config.Timer = env.timers.start
	env.h.start()

	// After restart: no turn started before ResumeAt, parked report still in Record.Reports, continuation still Scheduled.
	leadConv = env.h.runner.conv(lead.ID)
	eventually(t, "continuation restored after restart", func() bool {
		qc := env.h.state(lead.ID).QuotaContinuation
		return qc != nil && qc.Status == QuotaContinuationScheduled
	})
	leadConv.do(func() {
		if !leadConv.reportsParked() {
			t.Errorf("restored reports must count as parked while continuation is Scheduled")
		}
		if leadConv.hasPendingKick() {
			t.Errorf("hasPendingKick must stay false after restart with parked reports")
		}
		leadConv.kick()
	})
	time.Sleep(60 * time.Millisecond)
	if env.h.state(lead.ID).Process {
		t.Fatalf("restarted lead must not start a process before ResumeAt on parked reports alone")
	}

	// Advance clock past ResumeAt and fire the continuation timer.
	resumeAt := env.h.state(lead.ID).QuotaContinuation.ResumeAt
	env.advance(time.Duration(resumeAt-env.currentNow().UnixMilli())*time.Millisecond + time.Second)
	beforeProcs := env.h.backend.startCount()
	env.fireLastTimer(t)
	resumedProc := env.h.backend.waitProc(t, beforeProcs)
	sent := resumedProc.nextSent(t)
	if !strings.Contains(sent, "usage limit was reached") {
		t.Fatalf("resumed turn missing continuation prompt: %q", sent)
	}
	if !strings.Contains(sent, "ALL ANCESTORS QUOTA-EXHAUSTED: direct-w ("+AgentAddress(directW.ID)+")") {
		t.Fatalf("resumed turn missing parked exhaustion report: %q", sent)
	}
	eventually(t, "parked reports cleared after resumed turn", func() bool {
		rec := env.record(t, lead.ID)
		var parked int
		leadConv.do(func() { parked = leadConv.parkedReports })
		return len(rec.Reports) == 0 && parked == 0
	})
}

func TestQuotaHopParkedReportWithOrdinaryTakeReportAndHeldInput(t *testing.T) {
	t.Run("ordinary takeReport starts turn with parked report and turnSettled cancels continuation", func(t *testing.T) {
		env := newHopTestEnv(t, true, map[string]string{
			wsconfig.ItemAgentQuotaContinuation + "." + BackendClaude: "auto",
		})
		lead, leadProc := env.createHostedLead(t)
		leadConv := env.h.runner.conv(lead.ID)

		resetsAt := env.currentNow().Add(10 * time.Minute).UnixMilli()
		if _, err := env.h.runner.Send(lead.ID, "work"); err != nil {
			t.Fatal(err)
		}
		leadProc.nextSent(t)
		env.failWithUsageLimit(leadProc, "lead quota hit", resetsAt)
		eventually(t, "continuation scheduled", func() bool {
			qc := env.h.state(lead.ID).QuotaContinuation
			return qc != nil && qc.Status == QuotaContinuationScheduled
		})

		w, p, _ := env.spawnDirectWorker(t, ParentRef{Kind: ParentLead, ReplyID: lead.ID}, BackendClaude, "w-parked", "small")
		env.failWithUsageLimit(p, "worker quota hit", resetsAt)
		eventually(t, "report parked", func() bool { return len(env.record(t, lead.ID).Reports) == 1 })

		// An ordinary takeReport arriving before ResumeAt kicks a turn carrying both reports without cancelling at wake.
		leadConv.do(func() {
			leadConv.takeReport("[subagent id:other] turn done\n\nfinished fine\n")
		})
		sent := leadProc.nextSent(t)
		if !strings.Contains(sent, "ALL ANCESTORS QUOTA-EXHAUSTED: w-parked ("+AgentAddress(w.ID)+")") || !strings.Contains(sent, "finished fine") {
			t.Fatalf("turn must carry both parked and ordinary report: %q", sent)
		}
		// Continuation still exists until turn settles cleanly without usage-limit alert.
		if qc := env.h.state(lead.ID).QuotaContinuation; qc == nil || qc.Status != QuotaContinuationScheduled {
			t.Fatalf("takeReport must not cancel continuation before settlement: %+v", qc)
		}
		env.endTurn(leadProc)
		eventually(t, "clean turn settlement cancels continuation", func() bool {
			return env.h.state(lead.ID).QuotaContinuation == nil
		})
		var parked int
		leadConv.do(func() { parked = leadConv.parkedReports })
		if parked != 0 {
			t.Fatalf("parkedReports = %d, want 0", parked)
		}
	})

	t.Run("held inbox input cancels continuation and carries parked report", func(t *testing.T) {
		env := newHopTestEnv(t, true, map[string]string{
			wsconfig.ItemAgentQuotaContinuation + "." + BackendClaude: "auto",
		})
		lead, leadProc := env.createHostedLead(t)
		leadConv := env.h.runner.conv(lead.ID)

		resetsAt := env.currentNow().Add(10 * time.Minute).UnixMilli()
		if _, err := env.h.runner.Send(lead.ID, "work"); err != nil {
			t.Fatal(err)
		}
		leadProc.nextSent(t)
		env.failWithUsageLimit(leadProc, "lead quota hit", resetsAt)
		eventually(t, "continuation scheduled", func() bool {
			qc := env.h.state(lead.ID).QuotaContinuation
			return qc != nil && qc.Status == QuotaContinuationScheduled
		})

		w, p, _ := env.spawnDirectWorker(t, ParentRef{Kind: ParentLead, ReplyID: lead.ID}, BackendClaude, "w-parked-2", "small")
		env.failWithUsageLimit(p, "worker quota hit", resetsAt)
		eventually(t, "report parked", func() bool { return len(env.record(t, lead.ID).Reports) == 1 })

		if _, err := env.h.runner.Send(lead.ID, "operator override"); err != nil {
			t.Fatal(err)
		}
		sent := leadProc.nextSent(t)
		if !strings.Contains(sent, "operator override") || !strings.Contains(sent, "ALL ANCESTORS QUOTA-EXHAUSTED: w-parked-2 ("+AgentAddress(w.ID)+")") {
			t.Fatalf("operator turn must carry both input and parked report: %q", sent)
		}
		eventually(t, "held inbox input cancelled continuation", func() bool {
			return env.h.state(lead.ID).QuotaContinuation == nil
		})
		var parked int
		leadConv.do(func() { parked = leadConv.parkedReports })
		if parked != 0 {
			t.Fatalf("parkedReports = %d, want 0", parked)
		}
	})
}

func TestQuotaHopAllExhaustedWithoutContinuationAndExternalTop(t *testing.T) {
	t.Run("hosted lead without continuation gets immediate ALL ANCESTORS delivery", func(t *testing.T) {
		env := newHopTestEnv(t, true, nil) // continuation is off
		lead, leadProc := env.createHostedLead(t)
		resetsAt := env.currentNow().Add(time.Hour).UnixMilli()
		if _, err := env.h.runner.Send(lead.ID, "work"); err != nil {
			t.Fatal(err)
		}
		leadProc.nextSent(t)
		env.failWithUsageLimit(leadProc, "lead quota hit", resetsAt)
		eventually(t, "lead in usage limit failure", func() bool {
			f := env.h.state(lead.ID).Failure
			return f != nil && f.Kind == wslive.NoticeUsageLimit
		})

		w, p, _ := env.spawnDirectWorker(t, ParentRef{Kind: ParentLead, ReplyID: lead.ID}, BackendClaude, "w-nocont", "small")
		env.failWithUsageLimit(p, "worker quota hit", resetsAt)
		eventually(t, "immediate ALL ANCESTORS delivery", func() bool {
			return len(env.reportsFor(lead.ID)) == 1
		})
		text := env.reportsFor(lead.ID)[0].Text
		if !strings.HasPrefix(text, "ALL ANCESTORS QUOTA-EXHAUSTED: w-nocont ("+AgentAddress(w.ID)+")\n") {
			t.Fatalf("unexpected header: %q", text)
		}
		if !strings.Contains(text, "ws/cron add") {
			t.Fatalf("hosted lead with resets_at must get ws/cron hint:\n%s", text)
		}
		if len(env.record(t, lead.ID).Reports) != 0 {
			t.Fatalf("must not park when no continuation is scheduled")
		}
	})

	t.Run("exhausted sub-lead under external lead delivers ALL ANCESTORS immediately", func(t *testing.T) {
		env := newHopTestEnv(t, true, nil)
		ext := ParentRef{Kind: ParentLead, ReplyID: "exttoplead"}
		sub, subProc, subTok := env.spawnDirectWorker(t, ext, BackendClaude, "ext-sub", "small")
		leaf, leafProc, _ := env.spawnNestedChild(t, subTok, "claude.small", "ext-leaf", "work")

		resetsAt := env.currentNow().Add(time.Hour).UnixMilli()
		env.failWithUsageLimit(subProc, "sub quota hit", resetsAt)
		eventually(t, "sub exhaustion delivered", func() bool { return len(env.reportsFor(ext.ReplyID)) == 1 })

		env.failWithUsageLimit(leafProc, "leaf quota hit", resetsAt)
		eventually(t, "leaf hopped past sub to external lead", func() bool { return len(env.reportsFor(ext.ReplyID)) == 2 })
		noSend(t, subProc, "exhausted sub under external lead must not wake")

		text := env.reportsFor(ext.ReplyID)[1].Text
		if !strings.HasPrefix(text, "ALL ANCESTORS QUOTA-EXHAUSTED: ext-leaf ("+AgentAddress(leaf.ID)+")\n") {
			t.Fatalf("unexpected header: %q", text)
		}
		if !strings.Contains(text, "skipped_ancestors: "+AgentAddress(sub.ID)+"\n") {
			t.Fatalf("missing skipped_ancestors:\n%s", text)
		}
		if strings.Contains(text, "ws/cron") {
			t.Fatalf("external top lead must not get ws/cron hint:\n%s", text)
		}
	})
}

func TestQuotaHopSuppressReportDeliveryHook(t *testing.T) {
	env := newHopTestEnv(t, true, nil)
	env.h.runner.opts.suppressReportDelivery = func(rec Record) bool { return true }

	ext := ParentRef{Kind: ParentLead, ReplyID: "extsuppressed"}
	w, proc, _ := env.spawnDirectWorker(t, ext, BackendClaude, "suppressed-w", "small")
	env.failWithUsageLimit(proc, "quota hit", env.currentNow().Add(time.Hour).UnixMilli())
	eventually(t, "worker settled", func() bool {
		rec := env.record(t, w.ID)
		return !rec.TurnOpen && rec.LastTurn != nil
	})
	time.Sleep(50 * time.Millisecond)
	if len(env.reportsFor(ext.ReplyID)) != 0 {
		t.Fatalf("suppressReportDelivery must suppress exhaustion report: %+v", env.reportsFor(ext.ReplyID))
	}
}
