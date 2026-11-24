package wsagent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmeta"
)

type quotaSendRecord struct {
	TurnID   string
	Text     string
	Operator bool
}

type quotaProc struct {
	events     chan Event
	sends      chan quotaSendRecord
	mu         sync.Mutex
	interrupts int
	closed     bool
}

func newQuotaProc() *quotaProc {
	return &quotaProc{
		events: make(chan Event, 64),
		sends:  make(chan quotaSendRecord, 32),
	}
}

func (p *quotaProc) Events() <-chan Event { return p.events }

func (p *quotaProc) Send(text string) error {
	p.sends <- quotaSendRecord{Text: text}
	return nil
}

func (p *quotaProc) submitTurn(id, text string, steers []durableSteer, interactive bool) error {
	p.sends <- quotaSendRecord{TurnID: id, Text: text, Operator: interactive}
	return nil
}

func (p *quotaProc) SetModel(string) error  { return nil }
func (p *quotaProc) SetEffort(string) error { return nil }

func (p *quotaProc) Interrupt() error {
	p.mu.Lock()
	p.interrupts++
	p.mu.Unlock()
	return nil
}

func (p *quotaProc) Close() error {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.events)
	}
	p.mu.Unlock()
	return nil
}

func (p *quotaProc) nextSend(t *testing.T) quotaSendRecord {
	t.Helper()
	select {
	case rec := <-p.sends:
		return rec
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for process Send")
		return quotaSendRecord{}
	}
}

type quotaBackend struct {
	mu    sync.Mutex
	procs []*quotaProc
}

func (b *quotaBackend) Start(ctx context.Context, spec StartSpec) (Process, error) {
	p := newQuotaProc()
	b.mu.Lock()
	b.procs = append(b.procs, p)
	b.mu.Unlock()
	p.events <- Event{SessionID: "sess-quota"}
	return p, nil
}

func (b *quotaBackend) startLoop(ctx context.Context, spec StartSpec) (Process, error) {
	return b.Start(ctx, spec)
}

func (b *quotaBackend) proc(t *testing.T, idx int) *quotaProc {
	t.Helper()
	var out *quotaProc
	eventually(t, "quota backend proc", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		if len(b.procs) <= idx {
			return false
		}
		out = b.procs[idx]
		return true
	})
	return out
}

type quotaEnv struct {
	t       *testing.T
	dir     string
	root    string
	home    string
	now     time.Time
	nowMu   sync.Mutex
	timers  *leverTimers
	backend *quotaBackend
	mail    *fakeMail
	runner  *Runner
	trusted func(*http.Request) (bool, bool)
}

func newQuotaEnv(t *testing.T, configOverrides map[string]string, jitter time.Duration) *quotaEnv {
	t.Helper()
	dir := t.TempDir()
	root := t.TempDir()
	home := t.TempDir()
	if len(configOverrides) > 0 {
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(map[string]any{"overrides": configOverrides})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "config.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := &quotaEnv{
		t:       t,
		dir:     dir,
		root:    root,
		home:    home,
		now:     time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC),
		timers:  &leverTimers{},
		backend: &quotaBackend{},
		mail:    newFakeMail(),
		trusted: func(*http.Request) (bool, bool) { return true, true },
	}
	env.startRunner(jitter, nil)
	t.Cleanup(func() {
		if env.runner != nil {
			env.runner.Close()
		}
	})
	return env
}

func (e *quotaEnv) currentNow() time.Time {
	e.nowMu.Lock()
	defer e.nowMu.Unlock()
	return e.now
}

func (e *quotaEnv) advance(d time.Duration) {
	e.nowMu.Lock()
	e.now = e.now.Add(d)
	e.nowMu.Unlock()
}

func (e *quotaEnv) startRunner(jitter time.Duration, serving <-chan struct{}) {
	e.t.Helper()
	keys := &fakeKeys{}
	leadKeys := &fakeLeadKeys{}
	r := NewRunner(Options{
		Dir:       e.dir,
		Keys:      keys,
		LeadKeys:  leadKeys,
		Mail:      e.mail,
		Config:    wsconfig.Options{ConfigHome: e.home, RepoRoot: e.root},
		WSMCP:     func(string) (MCPCommand, error) { return MCPCommand{Command: "ws-mcp"}, nil },
		DaemonMCP: func(string) (MCPCommand, error) { return MCPCommand{}, nil },
		Backends: map[string]BackendRegistration{
			BackendClaude: {
				Backend: e.backend, ProcessKind: ProcessDurable, LeadProfile: "lead-claude", PermissionModes: []string{"auto", "default"},
				NativeCompaction: &CompactionProtocol{LeadCommand: true, Lifecycle: true, PromptPersists: true},
			},
			BackendCodex: {Backend: e.backend, ProcessKind: ProcessDurable, LeadProfile: "lead-codex", PermissionModes: []string{"auto", "default"}},
			BackendAgy:   {Backend: e.backend, ProcessKind: ProcessDurable, LeadProfile: "lead-agy", PermissionModes: []string{"auto", "default"}},
			BackendWerk:  {Backend: e.backend, ProcessKind: ProcessDurable, LeadProfile: "lead-werk", PermissionModes: []string{"auto", "default"}},
		},
		Now:                     e.currentNow,
		Timer:                   e.timers.start,
		QuotaContinuationJitter: func(time.Duration) time.Duration { return jitter },
		Trusted: func(req *http.Request) (bool, bool) {
			return e.trusted(req)
		},
		Log: io.Discard,
	})
	r.SetEndpoint("http://127.0.0.1:1")
	r.BindExecJobs(newFakeExecJobs())
	if serving != nil {
		r.StartForPublication(nil, serving)
	} else {
		r.Start(nil)
	}
	e.runner = r
}

func (e *quotaEnv) createLead(backend string) string {
	e.t.Helper()
	st, err := e.runner.CreateLead(backend, e.root, LeadOptions{})
	if err != nil {
		e.t.Fatalf("CreateLead(%s): %v", backend, err)
	}
	return st.ID
}

func (e *quotaEnv) quiesce(id string) {
	e.t.Helper()
	c := e.runner.conv(id)
	if c == nil {
		return
	}
	c.do(func() {})
}

func (e *quotaEnv) waitIdle(id string) {
	e.t.Helper()
	eventually(e.t, "conversation idle", func() bool {
		st, err := e.runner.Get(id)
		return err == nil && st.Status == StatusIdle
	})
	e.quiesce(id)
}

func (e *quotaEnv) lastTimer() (int, time.Duration) {
	e.timers.mu.Lock()
	defer e.timers.mu.Unlock()
	if len(e.timers.durs) == 0 {
		return -1, 0
	}
	idx := len(e.timers.durs) - 1
	return idx, e.timers.durs[idx]
}

func (e *quotaEnv) hostOf(id string) *wslive.Host {
	e.t.Helper()
	snap, err := wslive.ReadSnapshot(wslive.SnapshotPath(wsdashboard.SessionDir(e.dir, id)))
	if err != nil {
		e.t.Fatalf("ReadSnapshot: %v", err)
	}
	return snap.Host
}

func (e *quotaEnv) recordOf(id string) Record {
	e.t.Helper()
	var db *wsmeta.Store
	if e.runner != nil {
		db = e.runner.durableRecords
	}
	closeDB := false
	if db == nil {
		var err error
		db, err = wsmeta.Open(e.dir, wsmeta.Options{})
		if err != nil {
			e.t.Fatalf("wsmeta.Open: %v", err)
		}
		closeDB = true
	}
	if closeDB {
		defer db.Close()
	}
	rows, err := db.DurableAgentRecords(context.Background())
	if err != nil {
		e.t.Fatalf("DurableAgentRecords: %v", err)
	}
	for _, row := range rows {
		if row.ReplyID == id {
			rec, err := decodeRecord("durable agent record", row.ReplyID, row.JSON)
			if err != nil {
				e.t.Fatalf("decodeRecord: %v", err)
			}
			return rec
		}
	}
	e.t.Fatalf("durable record %q not found", id)
	return Record{}
}

func (e *quotaEnv) putRecord(rec Record) {
	e.t.Helper()
	db, err := wsmeta.Open(e.dir, wsmeta.Options{})
	if err != nil {
		e.t.Fatalf("wsmeta.Open: %v", err)
	}
	defer db.Close()
	raw, err := json.Marshal(rec)
	if err != nil {
		e.t.Fatalf("marshal record: %v", err)
	}
	if err := db.PutDurableAgentRecord(context.Background(), wsmeta.DurableAgentRecord{
		ReplyID: rec.ReplyID, Backend: rec.Backend, JSON: raw,
	}); err != nil {
		e.t.Fatalf("PutDurableAgentRecord: %v", err)
	}
}

func TestQuotaContinuationKnownResetAutoLifecycle(t *testing.T) {
	jitter := 15 * time.Second
	env := newQuotaEnv(t, map[string]string{
		wsconfig.ItemAgentQuotaContinuation + ".claude": "auto",
	}, jitter)
	id := env.createLead(BackendClaude)

	if _, err := env.runner.Send(id, "original user task"); err != nil {
		t.Fatal(err)
	}
	p := env.backend.proc(t, 0)
	first := p.nextSend(t)
	if !strings.Contains(first.Text, "original user task") {
		t.Fatalf("first send = %q", first.Text)
	}

	resetsAt := env.currentNow().Add(10 * time.Minute).UnixMilli()
	p.events <- Event{TurnStart: true, TurnID: first.TurnID}
	p.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "5-hour limit reached", ResetsAt: resetsAt}, TurnID: first.TurnID}
	p.events <- Event{TurnEnd: true, TurnError: "rate limited", TurnID: first.TurnID}
	env.waitIdle(id)

	wantResumeAt := resetsAt + (quotaResetBuffer + jitter).Milliseconds()
	st, err := env.runner.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if st.QuotaContinuation == nil || st.QuotaContinuation.Status != QuotaContinuationScheduled || st.QuotaContinuation.ResumeAt != wantResumeAt || st.QuotaContinuation.Manual || st.QuotaContinuation.Attempts != 0 || st.QuotaContinuation.MaxAttempts != 0 {
		t.Fatalf("State.QuotaContinuation = %+v, want scheduled at %d", st.QuotaContinuation, wantResumeAt)
	}
	host := env.hostOf(id)
	if host == nil || host.QuotaContinuation == nil || host.QuotaContinuation.Status != QuotaContinuationScheduled || host.QuotaContinuation.ResumeAt != wantResumeAt {
		t.Fatalf("Host.QuotaContinuation = %+v", host)
	}
	rec := env.recordOf(id)
	if rec.QuotaContinuation == nil || rec.QuotaContinuation.ResumeAt != wantResumeAt {
		t.Fatalf("Record.QuotaContinuation = %+v", rec.QuotaContinuation)
	}
	started, _ := env.timers.counts()
	if started != 1 {
		t.Fatalf("timer starts = %d, want 1", started)
	}

	// Fire the scheduled continuation timer.
	env.advance(10*time.Minute + quotaResetBuffer + jitter)
	env.timers.fire(t, 0)
	second := p.nextSend(t)
	if strings.Contains(second.Text, "original user task") {
		t.Fatalf("continuation must not replay original user prompt verbatim: %q", second.Text)
	}
	if !strings.Contains(second.Text, "[system]") || !strings.Contains(second.Text, "usage limit") {
		t.Fatalf("continuation prompt = %q, want downstream-neutral system prompt", second.Text)
	}
	env.quiesce(id)
	st, _ = env.runner.Get(id)
	if st.QuotaContinuation == nil || st.QuotaContinuation.Status != QuotaContinuationInFlight || st.QuotaContinuation.ResumeAt != 0 {
		t.Fatalf("during continuation turn, QuotaContinuation = %+v, want in_flight with resume_at=0", st.QuotaContinuation)
	}

	// Clean turn completion clears QuotaContinuation and Failure.
	p.events <- Event{TurnStart: true, TurnID: second.TurnID}
	p.events <- Event{TurnEnd: true, TurnID: second.TurnID}
	env.waitIdle(id)

	st, _ = env.runner.Get(id)
	if st.QuotaContinuation != nil {
		t.Fatalf("after clean turn, QuotaContinuation = %+v, want nil", st.QuotaContinuation)
	}
	if host := env.hostOf(id); host != nil && host.QuotaContinuation != nil {
		t.Fatalf("after clean turn, Host.QuotaContinuation = %+v, want nil", host.QuotaContinuation)
	}
	if rec := env.recordOf(id); rec.QuotaContinuation != nil {
		t.Fatalf("after clean turn, Record.QuotaContinuation = %+v, want nil", rec.QuotaContinuation)
	}
}

func TestQuotaContinuationPastResetClampsToBufferAndJitter(t *testing.T) {
	jitter := 10 * time.Second
	env := newQuotaEnv(t, map[string]string{
		wsconfig.ItemAgentQuotaContinuation + ".all": "auto",
	}, jitter)
	id := env.createLead(BackendCodex)
	if _, err := env.runner.Send(id, "work"); err != nil {
		t.Fatal(err)
	}
	p := env.backend.proc(t, 0)
	first := p.nextSend(t)

	pastReset := env.currentNow().Add(-5 * time.Minute).UnixMilli()
	p.events <- Event{TurnStart: true, TurnID: first.TurnID}
	p.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "limit", ResetsAt: pastReset}, TurnID: first.TurnID}
	p.events <- Event{TurnEnd: true, TurnError: "limit", TurnID: first.TurnID}
	env.waitIdle(id)

	wantResumeAt := env.currentNow().UnixMilli() + (quotaResetBuffer + jitter).Milliseconds()
	st, _ := env.runner.Get(id)
	if st.QuotaContinuation == nil || st.QuotaContinuation.ResumeAt != wantResumeAt {
		t.Fatalf("past ResetsAt clamp: got %+v, want ResumeAt=%d", st.QuotaContinuation, wantResumeAt)
	}
}

func TestQuotaContinuationCodexDuplicateAlertsExhaustSixRetries(t *testing.T) {
	env := newQuotaEnv(t, map[string]string{
		wsconfig.ItemAgentQuotaContinuation + ".codex": "auto",
	}, 0)
	id := env.createLead(BackendCodex)
	if _, err := env.runner.Send(id, "start"); err != nil {
		t.Fatal(err)
	}
	p := env.backend.proc(t, 0)
	first := p.nextSend(t)

	// Codex reports the same failure first as an error and then on completion.
	mapper := newCodexMapper("sess-quota")
	mapper.now = func() int64 { return env.currentNow().UnixMilli() }
	failTurn := func(turnID string) {
		t.Helper()
		const quotaError = `{"message":"usage limit exceeded","codexErrorInfo":"usageLimitExceeded"}`
		for _, line := range []string{
			`{"method":"turn/started","params":{"threadId":"sess-quota","turn":{"id":"` + turnID + `"}}}`,
			`{"method":"error","params":{"threadId":"sess-quota","turnId":"` + turnID + `","error":` + quotaError + `}}`,
			`{"method":"turn/completed","params":{"threadId":"sess-quota","turn":{"id":"` + turnID + `","status":"failed","error":` + quotaError + `}}}`,
		} {
			for _, ev := range mapper.mapLine([]byte(line), nopReplies{}) {
				ev.TurnID = turnID
				p.events <- ev
			}
		}
		env.waitIdle(id)
	}
	failTurn(first.TurnID)

	st, _ := env.runner.Get(id)
	if st.QuotaContinuation == nil || st.QuotaContinuation.Status != QuotaContinuationScheduled || st.QuotaContinuation.Attempts != 0 || st.QuotaContinuation.MaxAttempts != 6 {
		t.Fatalf("initial unknown-reset state = %+v", st.QuotaContinuation)
	}
	timerIdx, dur := env.lastTimer()
	if dur != time.Hour {
		t.Fatalf("fallback delay = %v, want 1h", dur)
	}

	// Execute exactly 6 fallback retries, each hitting NoticeUsageLimit with ResetsAt == 0.
	for attempt := 1; attempt <= 6; attempt++ {
		env.advance(time.Hour)
		env.timers.fire(t, timerIdx)
		retry := p.nextSend(t)
		env.quiesce(id)

		st, _ = env.runner.Get(id)
		if st.QuotaContinuation == nil || st.QuotaContinuation.Status != QuotaContinuationInFlight || st.QuotaContinuation.Attempts != attempt {
			t.Fatalf("attempt %d in_flight = %+v", attempt, st.QuotaContinuation)
		}

		failTurn(retry.TurnID)

		st, _ = env.runner.Get(id)
		if attempt < 6 {
			if st.QuotaContinuation == nil || st.QuotaContinuation.Status != QuotaContinuationScheduled || st.QuotaContinuation.Attempts != attempt {
				t.Fatalf("after attempt %d failure: %+v, want scheduled", attempt, st.QuotaContinuation)
			}
			timerIdx, dur = env.lastTimer()
			if dur != time.Hour {
				t.Fatalf("attempt %d fallback delay = %v, want 1h", attempt, dur)
			}
		} else {
			if st.QuotaContinuation == nil || st.QuotaContinuation.Status != QuotaContinuationExhausted || st.QuotaContinuation.Attempts != 6 || st.QuotaContinuation.MaxAttempts != 6 {
				t.Fatalf("after attempt 6 failure: %+v, want exhausted", st.QuotaContinuation)
			}
		}
	}

	// Verify no 7th quota timer was armed after exhaustion.
	if nextIdx, _ := env.lastTimer(); nextIdx != timerIdx && env.timers.durs[nextIdx] == time.Hour {
		t.Fatalf("armed a 7th 1h quota timer after exhaustion (idx %d -> %d)", timerIdx, nextIdx)
	}
}

func TestQuotaContinuationDueBeforeTurnEnd(t *testing.T) {
	env := newQuotaEnv(t, map[string]string{wsconfig.ItemAgentQuotaContinuation + ".claude": "auto"}, 0)
	id := env.createLead(BackendClaude)
	if _, err := env.runner.Send(id, "work"); err != nil {
		t.Fatal(err)
	}
	p := env.backend.proc(t, 0)
	first := p.nextSend(t)
	p.events <- Event{TurnStart: true, TurnID: first.TurnID}
	p.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "limit"}, TurnID: first.TurnID}
	fireQuotaDuringWork(t, env, id)
	select {
	case send := <-p.sends:
		t.Fatalf("started quota continuation before turn end: %+v", send)
	default:
	}
	p.events <- Event{TurnEnd: true, TurnError: "limit", TurnID: first.TurnID}
	if sent := p.nextSend(t); !strings.Contains(sent.Text, "scheduled retry interval has elapsed") {
		t.Fatalf("post-settlement prompt = %+v", sent)
	}
}

func TestQuotaContinuationDueDuringNativeCompact(t *testing.T) {
	for _, name := range []string{"compaction resume", "operator input"} {
		t.Run(name, func(t *testing.T) {
			env := newQuotaEnv(t, map[string]string{wsconfig.ItemAgentQuotaContinuation + ".claude": "auto"}, 0)
			id := env.createLead(BackendClaude)
			if _, err := env.runner.Send(id, "work"); err != nil {
				t.Fatal(err)
			}
			p := env.backend.proc(t, 0)
			first := p.nextSend(t)
			p.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "limit"}, TurnID: first.TurnID}
			p.events <- Event{TurnEnd: true, TurnError: "limit", TurnID: first.TurnID}
			env.waitIdle(id)
			resume := name == "compaction resume"
			if result := env.runner.Compact(env.recordOf(id).WSKey, "", &resume); result.Status != "queued" {
				t.Fatalf("compact result = %+v", result)
			}
			if sent := p.nextSend(t); sent.Text != "/compact" {
				t.Fatalf("compact command = %+v", sent)
			}

			fireQuotaDuringWork(t, env, id)
			st, _ := env.runner.Get(id)
			if qc := st.QuotaContinuation; qc == nil || qc.Status != QuotaContinuationScheduled || qc.Attempts != 0 {
				t.Fatalf("continuation while compacting = %+v, want scheduled with zero attempts", qc)
			}
			if !resume {
				if _, err := env.runner.Send(id, "operator input"); err != nil {
					t.Fatal(err)
				}
				env.quiesce(id)
			}
			p.events <- Event{Live: &wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactEnd}, compactCommandDone: true}
			sent := p.nextSend(t)
			if resume {
				if !strings.Contains(sent.Text, "scheduled retry interval has elapsed") {
					t.Fatalf("want quota continuation with compaction restore, got %+v", sent)
				}
				p.events <- Event{TurnEnd: true, TurnID: sent.TurnID}
				env.waitIdle(id)
				select {
				case duplicate := <-p.sends:
					t.Fatalf("separate compaction continuation followed quota continuation: %+v", duplicate)
				default:
				}
			} else if !strings.Contains(sent.Text, "operator input") || strings.Contains(sent.Text, "scheduled retry interval") {
				t.Fatalf("want operator input alone, got %+v", sent)
			}
		})
	}
}

func fireQuotaDuringWork(t *testing.T, env *quotaEnv, id string) {
	t.Helper()
	eventually(t, "quota timer armed", func() bool {
		env.timers.mu.Lock()
		defer env.timers.mu.Unlock()
		return slices.Contains(env.timers.durs, quotaFallbackDelay)
	})
	done := make(chan struct{})
	c := env.runner.conv(id)
	c.do(func() { c.onQuotaAfterFence = func() { close(done) } })
	env.timers.mu.Lock()
	idx := slices.Index(env.timers.durs, quotaFallbackDelay)
	env.timers.mu.Unlock()
	env.advance(time.Hour)
	env.timers.fire(t, idx)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("quota timer did not fire")
	}
	c.do(func() { c.onQuotaAfterFence = nil })
}

func TestQuotaContinuationDefaultOffAndWerkPlanPauseRules(t *testing.T) {
	// 1. Builtin default is off: NoticeUsageLimit does not auto-schedule.
	envOff := newQuotaEnv(t, nil, 0)
	idOff := envOff.createLead(BackendClaude)
	if _, err := envOff.runner.Send(idOff, "hi"); err != nil {
		t.Fatal(err)
	}
	pOff := envOff.backend.proc(t, 0)
	firstOff := pOff.nextSend(t)
	pOff.events <- Event{TurnStart: true, TurnID: firstOff.TurnID}
	pOff.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "limit"}, TurnID: firstOff.TurnID}
	pOff.events <- Event{TurnEnd: true, TurnError: "limit", TurnID: firstOff.TurnID}
	envOff.waitIdle(idOff)
	if st, _ := envOff.runner.Get(idOff); st.QuotaContinuation != nil {
		t.Fatalf("default off scheduled unexpectedly: %+v", st.QuotaContinuation)
	}

	// 2. (1556)=b: werk OpenAI plan pause (AlertManualOnly: true) suppresses auto
	// mode even when agent.quota_continuation = auto, while explicit manual
	// reservation schedules with Manual: true and injects with Operator: true.
	env := newQuotaEnv(t, map[string]string{
		wsconfig.ItemAgentQuotaContinuation + ".werk": "auto",
	}, 0)
	id := env.createLead(BackendWerk)
	if _, err := env.runner.Send(id, "work"); err != nil {
		t.Fatal(err)
	}
	p := env.backend.proc(t, 0)
	first := p.nextSend(t)
	if !first.Operator {
		t.Fatal("initial user send should have Operator=true")
	}

	p.events <- Event{TurnStart: true, TurnID: first.TurnID}
	p.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "ChatGPT plan paused"}, AlertManualOnly: true, TurnID: first.TurnID}
	p.events <- Event{TurnEnd: true, TurnError: "plan paused", TurnID: first.TurnID}
	env.waitIdle(id)

	if st, _ := env.runner.Get(id); st.QuotaContinuation != nil {
		t.Fatalf("AlertManualOnly must suppress auto scheduling, got %+v", st.QuotaContinuation)
	}

	// Explicit manual schedule sets Manual: true and injects with Operator: true.
	st, err := env.runner.ScheduleQuotaContinuation(id)
	if err != nil {
		t.Fatalf("ScheduleQuotaContinuation: %v", err)
	}
	if st.QuotaContinuation == nil || !st.QuotaContinuation.Manual || st.QuotaContinuation.Status != QuotaContinuationScheduled || st.QuotaContinuation.MaxAttempts != 6 {
		t.Fatalf("manual schedule = %+v", st.QuotaContinuation)
	}

	env.advance(time.Hour)
	env.timers.fire(t, 0)
	probe1 := p.nextSend(t)
	if !probe1.Operator {
		t.Fatal("manual UI reservation must inject with Operator=true")
	}

	// Repeated AlertManualOnly on Manual: true reschedules (attempt 1 of 6).
	p.events <- Event{TurnStart: true, TurnID: probe1.TurnID}
	p.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "ChatGPT plan still paused"}, AlertManualOnly: true, TurnID: probe1.TurnID}
	p.events <- Event{TurnEnd: true, TurnError: "plan paused", TurnID: probe1.TurnID}
	env.waitIdle(id)
	st, _ = env.runner.Get(id)
	if st.QuotaContinuation == nil || st.QuotaContinuation.Status != QuotaContinuationScheduled || !st.QuotaContinuation.Manual || st.QuotaContinuation.Attempts != 1 {
		t.Fatalf("after repeated AlertManualOnly on manual schedule: %+v", st.QuotaContinuation)
	}

	// Non-quota failure (e.g. planProbeError) clears in_flight to nil.
	env.advance(time.Hour)
	env.timers.fire(t, 1)
	probe2 := p.nextSend(t)
	if !probe2.Operator {
		t.Fatal("second manual probe must inject with Operator=true")
	}
	p.events <- Event{TurnStart: true, TurnID: probe2.TurnID}
	p.events <- Event{TurnEnd: true, TurnError: "planProbeError: network timeout", TurnID: probe2.TurnID}
	env.waitIdle(id)
	if st, _ := env.runner.Get(id); st.QuotaContinuation != nil {
		t.Fatalf("non-quota error on in_flight must clear to nil, got %+v", st.QuotaContinuation)
	}

	// Auto in_flight (Manual: false, Operator: false) hitting AlertManualOnly clears to nil.
	if _, err := env.runner.Send(id, "next work"); err != nil {
		t.Fatal(err)
	}
	nextWork := p.nextSend(t)
	p.events <- Event{TurnStart: true, TurnID: nextWork.TurnID}
	p.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "429 rate limited"}, AlertManualOnly: false, TurnID: nextWork.TurnID}
	p.events <- Event{TurnEnd: true, TurnError: "429", TurnID: nextWork.TurnID}
	env.waitIdle(id)
	st, _ = env.runner.Get(id)
	if st.QuotaContinuation == nil || st.QuotaContinuation.Manual || st.QuotaContinuation.Status != QuotaContinuationScheduled {
		t.Fatalf("ordinary 429 should auto-schedule with Manual=false, got %+v", st.QuotaContinuation)
	}
	env.advance(time.Hour)
	env.timers.fire(t, 2)
	autoTurn := p.nextSend(t)
	if autoTurn.Operator {
		t.Fatal("auto continuation must inject with Operator=false")
	}
	p.events <- Event{TurnStart: true, TurnID: autoTurn.TurnID}
	p.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "ChatGPT plan paused"}, AlertManualOnly: true, TurnID: autoTurn.TurnID}
	p.events <- Event{TurnEnd: true, TurnError: "plan paused", TurnID: autoTurn.TurnID}
	env.waitIdle(id)
	if st, _ := env.runner.Get(id); st.QuotaContinuation != nil {
		t.Fatalf("auto in_flight hitting AlertManualOnly must clear to nil, got %+v", st.QuotaContinuation)
	}
}

func TestQuotaContinuationRestartRecoveryAndNonDoubleFire(t *testing.T) {
	// Case A: scheduled persists across restart and fires once publication opens.
	env := newQuotaEnv(t, map[string]string{
		wsconfig.ItemAgentQuotaContinuation + ".claude": "auto",
	}, 0)
	id := env.createLead(BackendClaude)
	if _, err := env.runner.Send(id, "work"); err != nil {
		t.Fatal(err)
	}
	p := env.backend.proc(t, 0)
	first := p.nextSend(t)
	resetsAt := env.currentNow().Add(5 * time.Minute).UnixMilli()
	p.events <- Event{TurnStart: true, TurnID: first.TurnID}
	p.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "limit", ResetsAt: resetsAt}, TurnID: first.TurnID}
	p.events <- Event{TurnEnd: true, TurnError: "limit", TurnID: first.TurnID}
	env.waitIdle(id)

	// Simulate daemon restart after ResumeAt is already overdue, gated behind serving.
	env.runner.Close()
	env.advance(10 * time.Minute)
	env.timers = &leverTimers{}
	serving := make(chan struct{})
	env.startRunner(0, serving)

	eventually(t, "re-armed timer on restart", func() bool {
		n, _ := env.timers.counts()
		return n == 1
	})
	if env.timers.durs[0] != 0 {
		t.Fatalf("overdue restart delay = %v, want 0", env.timers.durs[0])
	}
	env.timers.fire(t, 0)
	env.quiesce(id)

	// Must not start process before serving closes.
	if len(env.backend.procs) != 1 {
		t.Fatalf("process started before publication ready: %d", len(env.backend.procs))
	}
	close(serving)
	p2 := env.backend.proc(t, 1)
	p2.nextSend(t)
	env.quiesce(id)
	if st, _ := env.runner.Get(id); st.QuotaContinuation == nil || st.QuotaContinuation.Status != QuotaContinuationInFlight {
		t.Fatalf("after restart fire, state = %+v, want in_flight", st.QuotaContinuation)
	}

	// Case B: daemon restarts while Status == "in_flight" and no active/durable turn
	// rejoins it -> MUST NOT double-fire; clears to nil.
	env.runner.Close()
	env.runner = nil
	rec := env.recordOf(id)
	rec.DurableTurn = nil
	rec.TurnOpen = false
	env.putRecord(rec)
	env.timers = &leverTimers{}
	env.startRunner(0, nil)
	env.quiesce(id)

	if started, _ := env.timers.counts(); started != 0 {
		t.Fatalf("in_flight restart armed %d timers, want 0", started)
	}
	if len(env.backend.procs) != 2 {
		t.Fatalf("in_flight restart spawned another process (%d), want 2 total", len(env.backend.procs))
	}
	if st, _ := env.runner.Get(id); st.QuotaContinuation != nil {
		t.Fatalf("orphaned in_flight after restart = %+v, want nil", st.QuotaContinuation)
	}
}

func TestQuotaContinuationQuietReplayIgnoredAndWorkerNonParticipation(t *testing.T) {
	env := newQuotaEnv(t, map[string]string{
		wsconfig.ItemAgentQuotaContinuation + ".all": "auto",
	}, 0)
	id := env.createLead(BackendClaude)
	c := env.runner.conv(id)
	c.do(func() {
		c.alert(wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "replayed limit"}, false, true)
	})
	if st, _ := env.runner.Get(id); st.QuotaContinuation != nil {
		t.Fatalf("quiet replay alert scheduled continuation: %+v", st.QuotaContinuation)
	}
	if started, _ := env.timers.counts(); started != 0 {
		t.Fatalf("quiet replay armed %d timers, want 0", started)
	}

	// Worker (Lead: false) never participates.
	h := newHarness(t, map[string]string{"chat": chatProfile})
	wID := h.create("chat").ID
	if _, err := h.runner.ScheduleQuotaContinuation(wID); requestStatus(err) != http.StatusBadRequest {
		t.Fatalf("ScheduleQuotaContinuation(worker) = %v, want 400", err)
	}
	if _, err := h.runner.CancelQuotaContinuation(wID); requestStatus(err) != http.StatusBadRequest {
		t.Fatalf("CancelQuotaContinuation(worker) = %v, want 400", err)
	}
	wc := h.runner.conv(wID)
	wc.do(func() {
		wc.alert(wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "worker limit"}, false, false)
	})
	if st := h.state(wID); st.QuotaContinuation != nil {
		t.Fatalf("worker scheduled continuation: %+v", st.QuotaContinuation)
	}
}

func TestQuotaContinuationAutoCancelTriggers(t *testing.T) {
	setupScheduled := func(t *testing.T) (*quotaEnv, string, *quotaProc) {
		t.Helper()
		env := newQuotaEnv(t, map[string]string{
			wsconfig.ItemAgentQuotaContinuation + ".claude": "auto",
		}, 0)
		id := env.createLead(BackendClaude)
		if _, err := env.runner.Send(id, "work"); err != nil {
			t.Fatal(err)
		}
		p := env.backend.proc(t, 0)
		first := p.nextSend(t)
		p.events <- Event{TurnStart: true, TurnID: first.TurnID}
		p.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "limit"}, TurnID: first.TurnID}
		p.events <- Event{TurnEnd: true, TurnError: "limit", TurnID: first.TurnID}
		env.waitIdle(id)
		if st, _ := env.runner.Get(id); st.QuotaContinuation == nil || st.QuotaContinuation.Status != QuotaContinuationScheduled {
			t.Fatalf("setup failed: %+v", st.QuotaContinuation)
		}
		return env, id, p
	}

	t.Run("Runner.Send", func(t *testing.T) {
		env, id, p := setupScheduled(t)
		if _, err := env.runner.Send(id, "user follow-up"); err != nil {
			t.Fatal(err)
		}
		p.nextSend(t)
		env.quiesce(id)
		if st, _ := env.runner.Get(id); st.QuotaContinuation != nil {
			t.Fatalf("Send did not cancel: %+v", st.QuotaContinuation)
		}
	})

	t.Run("Runner.PageInput", func(t *testing.T) {
		env, id, p := setupScheduled(t)
		inbox := wsinbox.Dir(wsdashboard.SessionDir(env.dir, id))
		if err := env.runner.PageInput(id, func() bool {
			_, _, err := wsinbox.Send(inbox, wsinbox.Entry{Parts: []string{"composer message"}, Operator: true}, env.currentNow())
			return err == nil
		}); err != nil {
			t.Fatal(err)
		}
		p.nextSend(t)
		env.quiesce(id)
		if st, _ := env.runner.Get(id); st.QuotaContinuation != nil {
			t.Fatalf("PageInput did not cancel: %+v", st.QuotaContinuation)
		}
	})

	t.Run("HeldNonOperatorInboxMessage", func(t *testing.T) {
		env, id, p := setupScheduled(t)
		inbox := wsinbox.Dir(wsdashboard.SessionDir(env.dir, id))
		if _, _, err := wsinbox.Send(inbox, wsinbox.Entry{Parts: []string{"child report"}, Operator: false}, env.currentNow()); err != nil {
			t.Fatal(err)
		}
		env.runner.conv(id).signalInbox()
		p.nextSend(t)
		env.quiesce(id)
		if st, _ := env.runner.Get(id); st.QuotaContinuation != nil {
			t.Fatalf("held background message did not cancel: %+v", st.QuotaContinuation)
		}
	})

	t.Run("Runner.Interrupt", func(t *testing.T) {
		env, id, _ := setupScheduled(t)
		st, _, err := env.runner.Interrupt(id)
		if err != nil {
			t.Fatal(err)
		}
		if st.QuotaContinuation != nil {
			t.Fatalf("Interrupt did not cancel: %+v", st.QuotaContinuation)
		}
	})

	t.Run("CancelQuotaContinuation", func(t *testing.T) {
		env, id, _ := setupScheduled(t)
		st, err := env.runner.CancelQuotaContinuation(id)
		if err != nil {
			t.Fatal(err)
		}
		if st.QuotaContinuation != nil {
			t.Fatalf("CancelQuotaContinuation did not cancel: %+v", st.QuotaContinuation)
		}
		// Idempotent when already nil.
		if _, err := env.runner.CancelQuotaContinuation(id); err != nil {
			t.Fatalf("idempotent cancel failed: %v", err)
		}
	})

	t.Run("CloseConversation", func(t *testing.T) {
		env, id, _ := setupScheduled(t)
		if _, err := env.runner.CloseConversation(id); err != nil {
			t.Fatal(err)
		}
		if rec := env.recordOf(id); rec.QuotaContinuation != nil {
			t.Fatalf("CloseConversation left QuotaContinuation in record: %+v", rec.QuotaContinuation)
		}
	})

	t.Run("SetHidden", func(t *testing.T) {
		env, id, _ := setupScheduled(t)
		st, err := env.runner.SetHidden(id, true)
		if err != nil {
			t.Fatal(err)
		}
		if st.QuotaContinuation != nil {
			t.Fatalf("SetHidden(true) did not cancel: %+v", st.QuotaContinuation)
		}
	})

	t.Run("Release", func(t *testing.T) {
		env, id, _ := setupScheduled(t)
		c := env.runner.conv(id)
		c.do(func() { c.release() })
		if rec := env.recordOf(id); rec.QuotaContinuation != nil {
			t.Fatalf("release left QuotaContinuation in record: %+v", rec.QuotaContinuation)
		}
	})
}

func TestQuotaContinuationT0RecordUnknownFieldRoundTrip(t *testing.T) {
	rec := Record{
		SchemaVersion: recordSchemaVersion,
		ReplyID:       "r1",
		Profile:       "lead",
		Backend:       BackendClaude,
		Root:          "/repo",
		WSKey:         "key-1",
		CreatedAt:     "2026-11-24T12:00:00Z",
		Lead:          true,
		QuotaContinuation: &QuotaContinuation{
			Status:      QuotaContinuationScheduled,
			ResumeAt:    1795525200000,
			Attempts:    2,
			MaxAttempts: 6,
			Manual:      true,
			NoticeText:  "usage limit reached",
		},
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate an older daemon reading into a Record-shaped unknown-field map,
	// mutating an unrelated field, and rewriting the JSON.
	var oldMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &oldMap); err != nil {
		t.Fatal(err)
	}
	if _, ok := oldMap["quota_continuation"]; !ok {
		t.Fatalf("marshaled record missing quota_continuation: %s", raw)
	}
	oldMap["model"] = json.RawMessage(`"opus"`)
	rewritten, err := json.Marshal(oldMap)
	if err != nil {
		t.Fatal(err)
	}

	// Reopening with the new daemon restores QuotaContinuation intact.
	var reopened Record
	if err := json.Unmarshal(rewritten, &reopened); err != nil {
		t.Fatal(err)
	}
	if reopened.QuotaContinuation == nil || *reopened.QuotaContinuation != *rec.QuotaContinuation {
		t.Fatalf("reopened QuotaContinuation = %+v, want %+v", reopened.QuotaContinuation, rec.QuotaContinuation)
	}
	if reopened.Model != "opus" {
		t.Fatalf("reopened Model = %q, want opus", reopened.Model)
	}
}

func TestQuotaContinuationHTTPAuthAndLifecycle(t *testing.T) {
	env := newQuotaEnv(t, nil, 0)
	id := env.createLead(BackendClaude)
	handler := env.runner.Handler()

	post := func(path, contentType, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{}`))
		req.Host = "127.0.0.1:29417"
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	// 1. Remote/LAN viewer (!local) -> 404 on both schedule and cancel.
	env.trusted = func(*http.Request) (bool, bool) { return false, false }
	if rr := post("/agents/"+id+"/quota-continuation", "application/json", "http://127.0.0.1:29417"); rr.Code != http.StatusNotFound {
		t.Fatalf("remote schedule status = %d, want 404", rr.Code)
	}
	if rr := post("/agents/"+id+"/quota-continuation/cancel", "application/json", "http://127.0.0.1:29417"); rr.Code != http.StatusNotFound {
		t.Fatalf("remote cancel status = %d, want 404", rr.Code)
	}

	// 2. Local viewer with cross-origin (!own) -> 403.
	env.trusted = func(*http.Request) (bool, bool) { return true, false }
	if rr := post("/agents/"+id+"/quota-continuation", "application/json", "http://127.0.0.1:29417"); rr.Code != http.StatusForbidden {
		t.Fatalf("cross-origin schedule status = %d, want 403", rr.Code)
	}

	// 3. Non-JSON content type -> 403 (writeAllowed).
	env.trusted = func(*http.Request) (bool, bool) { return true, true }
	if rr := post("/agents/"+id+"/quota-continuation", "text/plain", "http://127.0.0.1:29417"); rr.Code != http.StatusForbidden {
		t.Fatalf("non-json schedule status = %d, want 403", rr.Code)
	}

	// 4. Not in usage-limit state -> 409 Conflict.
	if rr := post("/agents/"+id+"/quota-continuation", "application/json", "http://127.0.0.1:29417"); rr.Code != http.StatusConflict {
		t.Fatalf("schedule without usage limit status = %d, want 409 (%s)", rr.Code, rr.Body.String())
	}

	// Put lead in active usage-limit state.
	if _, err := env.runner.Send(id, "work"); err != nil {
		t.Fatal(err)
	}
	p := env.backend.proc(t, 0)
	first := p.nextSend(t)
	p.events <- Event{TurnStart: true, TurnID: first.TurnID}
	p.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "limit"}, TurnID: first.TurnID}
	p.events <- Event{TurnEnd: true, TurnError: "limit", TurnID: first.TurnID}
	env.waitIdle(id)

	// 5. Valid schedule -> 200 with State.QuotaContinuation populated.
	rr := post("/agents/"+id+"/quota-continuation", "application/json", "http://127.0.0.1:29417")
	if rr.Code != http.StatusOK {
		t.Fatalf("valid schedule status = %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	var st State
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.QuotaContinuation == nil || st.QuotaContinuation.Status != QuotaContinuationScheduled || !st.QuotaContinuation.Manual {
		t.Fatalf("schedule response QuotaContinuation = %+v", st.QuotaContinuation)
	}

	// 6. Valid cancel -> 200 with State.QuotaContinuation == nil.
	rr = post("/agents/"+id+"/quota-continuation/cancel", "application/json", "http://127.0.0.1:29417")
	if rr.Code != http.StatusOK {
		t.Fatalf("valid cancel status = %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	st = State{}
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.QuotaContinuation != nil {
		t.Fatalf("cancel response QuotaContinuation = %+v, want nil", st.QuotaContinuation)
	}
}

func TestQuotaContinuationInputRaceDuringFire(t *testing.T) {
	for _, kind := range []string{"Runner.Send", "Runner.PageInput", "PageInputAfterFenceBeforeMu"} {
		t.Run(kind, func(t *testing.T) {
			env := newQuotaEnv(t, map[string]string{
				wsconfig.ItemAgentQuotaContinuation + ".claude": "auto",
			}, 0)
			id := env.createLead(BackendClaude)
			if _, err := env.runner.Send(id, "initial"); err != nil {
				t.Fatal(err)
			}
			p := env.backend.proc(t, 0)
			first := p.nextSend(t)
			p.events <- Event{TurnStart: true, TurnID: first.TurnID}
			p.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "limit", ResetsAt: env.currentNow().Add(5 * time.Minute).UnixMilli()}, TurnID: first.TurnID}
			p.events <- Event{TurnEnd: true, TurnError: "limit", TurnID: first.TurnID}
			env.waitIdle(id)

			// Inject concurrent user input in the target race window.
			c := env.runner.conv(id)
			var once sync.Once
			racedDone := make(chan error, 1)
			trigger := func() {
				once.Do(func() {
					go func() {
						if kind == "Runner.Send" {
							_, err := env.runner.Send(id, "user raced in")
							racedDone <- err
						} else {
							err := env.runner.PageInput(id, func() bool {
								_, _, werr := wsinbox.Send(c.inbox, wsinbox.Entry{Parts: []string{"page raced in"}, Operator: true}, env.currentNow())
								return werr == nil
							})
							racedDone <- err
						}
					}()
					if err := <-racedDone; err != nil {
						t.Errorf("concurrent %s failed: %v", kind, err)
					}
				})
			}
			c.do(func() {
				if kind == "PageInputAfterFenceBeforeMu" {
					c.onQuotaAfterFence = trigger
				} else {
					c.onQuotaBeforeSend = trigger
				}
			})

			env.advance(6 * time.Minute)
			env.timers.fire(t, 0)
			second := p.nextSend(t)
			if strings.Contains(second.Text, "[system]") {
				t.Fatalf("continuation prompt was co-mingled or injected after %s race: %q", kind, second.Text)
			}
			wantSub := "user raced in"
			if kind != "Runner.Send" {
				wantSub = "page raced in"
			}
			if !strings.Contains(second.Text, wantSub) {
				t.Fatalf("delivered prompt = %q, want %q", second.Text, wantSub)
			}
			env.quiesce(id)
			if st, _ := env.runner.Get(id); st.QuotaContinuation != nil {
				t.Fatalf("State.QuotaContinuation after %s race = %+v, want nil", kind, st.QuotaContinuation)
			}
			if host := env.hostOf(id); host != nil && host.QuotaContinuation != nil {
				t.Fatalf("Host.QuotaContinuation after %s race = %+v, want nil", kind, host.QuotaContinuation)
			}
			if rec := env.recordOf(id); rec.QuotaContinuation != nil {
				t.Fatalf("Record.QuotaContinuation after %s race = %+v, want nil", kind, rec.QuotaContinuation)
			}
		})
	}
}

func TestQuotaContinuationMailChangedAutoCancelAndDelivery(t *testing.T) {
	env := newQuotaEnv(t, map[string]string{
		wsconfig.ItemAgentQuotaContinuation + ".claude": "auto",
	}, 0)
	id := env.createLead(BackendClaude)
	if _, err := env.runner.Send(id, "initial"); err != nil {
		t.Fatal(err)
	}
	p := env.backend.proc(t, 0)
	first := p.nextSend(t)
	p.events <- Event{TurnStart: true, TurnID: first.TurnID}
	p.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "limit", ResetsAt: env.currentNow().Add(5 * time.Minute).UnixMilli()}, TurnID: first.TurnID}
	p.events <- Event{TurnEnd: true, TurnError: "limit", TurnID: first.TurnID}
	env.waitIdle(id)

	c := env.runner.conv(id)
	wsKey := env.recordOf(id).WSKey
	// Deliver a mailbox message through fakeMail -> Watch -> signalMail -> mailChanged.
	// Also set quotaPendingFire to verify kick() falls through to kickInput()
	// instead of stranding the mail.
	c.do(func() { c.quotaPendingFire = true })
	env.mail.mu.Lock()
	env.mail.queued[wsKey] = append(env.mail.queued[wsKey], ClaimedMail{Text: "mail from peer lead"})
	watch := env.mail.watches[wsKey]
	env.mail.mu.Unlock()
	if watch == nil {
		t.Fatalf("expected mail watch registered for %q", wsKey)
	}
	watch()

	second := p.nextSend(t)
	if !strings.Contains(second.Text, "mail from peer lead") {
		t.Fatalf("expected mail turn delivered, got %q", second.Text)
	}
	if strings.Contains(second.Text, "[system]") {
		t.Fatalf("continuation prompt must not be injected with mail: %q", second.Text)
	}
	env.quiesce(id)
	if st, _ := env.runner.Get(id); st.QuotaContinuation != nil {
		t.Fatalf("QuotaContinuation after mailChanged = %+v, want nil", st.QuotaContinuation)
	}
	if host := env.hostOf(id); host != nil && host.QuotaContinuation != nil {
		t.Fatalf("Host.QuotaContinuation after mailChanged = %+v, want nil", host.QuotaContinuation)
	}
}

func TestQuotaContinuationHasPendingKickForStartForPublication(t *testing.T) {
	env := newQuotaEnv(t, map[string]string{
		wsconfig.ItemAgentQuotaContinuation + ".claude": "auto",
	}, 0)
	id := env.createLead(BackendClaude)
	if _, err := env.runner.Send(id, "initial"); err != nil {
		t.Fatal(err)
	}
	p := env.backend.proc(t, 0)
	first := p.nextSend(t)
	p.events <- Event{TurnStart: true, TurnID: first.TurnID}
	p.events <- Event{Alert: &wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "limit", ResetsAt: env.currentNow().Add(5 * time.Minute).UnixMilli()}, TurnID: first.TurnID}
	p.events <- Event{TurnEnd: true, TurnError: "limit", TurnID: first.TurnID}
	env.waitIdle(id)

	env.runner.Close()
	env.runner = nil
	env.advance(10 * time.Minute)
	env.timers = &leverTimers{}
	serving := make(chan struct{})
	env.startRunner(0, serving)
	eventually(t, "re-armed timer on StartForPublication", func() bool {
		n, _ := env.timers.counts()
		return n == 1
	})
	env.timers.fire(t, 0)
	env.quiesce(id)

	c := env.runner.conv(id)
	var pending bool
	c.do(func() {
		pending = c.quotaPendingFire && c.hasPendingKick()
	})
	if !pending {
		t.Fatalf("expected hasPendingKick() == true when quotaPendingFire is set before publication")
	}
	close(serving)
	p2 := env.backend.proc(t, 1)
	p2.nextSend(t)
}
