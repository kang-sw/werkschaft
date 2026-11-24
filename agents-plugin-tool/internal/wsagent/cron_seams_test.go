package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wscron"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wsstate"
)

type cronGoneLog struct {
	mu  sync.Mutex
	ids []string
}

func (l *cronGoneLog) ConversationGone(id string) {
	l.mu.Lock()
	l.ids = append(l.ids, id)
	l.mu.Unlock()
}

func (l *cronGoneLog) got() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ids...)
}

type cronRemovals struct {
	mu  sync.Mutex
	ids []string
}

func (l *cronRemovals) add(c wsinbox.Cron) {
	l.mu.Lock()
	l.ids = append(l.ids, c.ID)
	l.mu.Unlock()
}

func (l *cronRemovals) got() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ids...)
}

// TestDeliverCronSteersMidTurnAndDrainsWhenIdle: a prompt that arrives
// mid-turn is written with Steer set, counted as a send, steered into the
// running turn immediately, and reported to the removal observer; a prompt
// that arrives while idle starts the next turn through the normal drain path.
func TestDeliverCronSteersMidTurnAndDrainsWhenIdle(t *testing.T) {
	var removals cronRemovals
	stop := wsinbox.ObserveCronRemoval(removals.add)
	defer stop()
	h := newLeadHarness(t).start()
	id, holder := h.runLead("one")
	h.holders.out(holder, stateLine(HarnessRunning), initLine("sess-1", "auto"), toolUseLine("u1", "toolu_1"))
	h.sync(id, holder)
	if lead, err := h.runner.CronIsLead(id); err != nil || !lead {
		t.Fatalf("CronIsLead = %v, %v", lead, err)
	}
	sendsBefore := h.record(id).Sends
	before := len(h.userInputs(holder))
	mark := wsinbox.Cron{ID: "cron-aa11", Name: "board", At: "2026-03-10T09:00:00+09:00"}
	if ok, err := h.runner.DeliverCron(id, mark, "check the board"); err != nil || !ok {
		t.Fatalf("DeliverCron = %v, %v", ok, err)
	}
	if got := h.record(id).Sends; got != sendsBefore+1 {
		t.Fatalf("sends = %d, want %d", got, sendsBefore+1)
	}
	eventually(t, "the running turn receives the steered cron prompt", func() bool {
		inputs := h.userInputs(holder)
		return len(inputs) > before && strings.Contains(inputs[len(inputs)-1], `<ws:cron id="cron-aa11" name="board" at="2026-03-10T09:00:00+09:00">`)
	})
	if got := removals.got(); len(got) != 1 || got[0] != "cron-aa11" {
		t.Fatalf("steer drain observed %v", got)
	}
	if ids := h.runner.CronHeld(id); len(ids) != 0 {
		t.Fatalf("still held after the steer drain: %v", ids)
	}
	drained, err := wsinbox.Drained(wsinbox.Dir(wsdashboard.SessionDir(h.dir, id)))
	if err != nil || len(drained) == 0 {
		t.Fatalf("drained = %+v, %v", drained, err)
	}
	last := drained[len(drained)-1]
	if !last.Entry.Steer || last.Entry.Cron == nil || last.Entry.Cron.ID != "cron-aa11" {
		t.Fatalf("drained cron entry = %+v", last.Entry)
	}

	// Idle fallback: once the turn ends, DeliverCron kicks a new turn via Drain.
	h.holders.out(holder, turnLines("sess-1", "u2", "done")...)
	h.idle(id)
	beforeIdle := len(h.userInputs(holder))
	mark2 := wsinbox.Cron{ID: "cron-aa22", Name: "idle", At: "2026-03-10T09:05:00+09:00"}
	if ok, err := h.runner.DeliverCron(id, mark2, "check while idle"); err != nil || !ok {
		t.Fatalf("idle DeliverCron = %v, %v", ok, err)
	}
	eventually(t, "the idle lead starts a turn with the scheduled prompt", func() bool {
		inputs := h.userInputs(holder)
		return len(inputs) > beforeIdle && strings.Contains(inputs[len(inputs)-1], `<ws:cron id="cron-aa22" name="idle" at="2026-03-10T09:05:00+09:00">`)
	})
	if got := removals.got(); len(got) != 2 || got[1] != "cron-aa22" {
		t.Fatalf("idle drain observed %v", got)
	}
	if ids := h.runner.CronHeld(id); len(ids) != 0 {
		t.Fatalf("still held after the idle drain: %v", ids)
	}
}

type cronPlanStatus struct {
	allowed bool
}

func (s cronPlanStatus) Credential(context.Context, wscred.Request) (wscred.Credential, error) {
	return wscred.Credential{}, wscred.ErrNotConfigured
}

func (s cronPlanStatus) Status() ([]wscred.ProviderStatus, error) {
	return []wscred.ProviderStatus{{Provider: wscred.ProviderOpenAI, OpenAIAccounts: []wscred.OpenAIAccountStatus{{
		Active: true, SignedIn: true, PlanEnabled: true, BackgroundConsent: &s.allowed,
	}}}}, nil
}

type cronModelRequest struct {
	interactive bool
	input       []json.RawMessage
}

func (r cronModelRequest) contains(text string) bool {
	return slices.ContainsFunc(r.input, func(item json.RawMessage) bool { return strings.Contains(string(item), text) })
}

func nextCronModelRequest(t *testing.T, requests <-chan cronModelRequest) cronModelRequest {
	t.Helper()
	select {
	case req := <-requests:
		return req
	case <-time.After(5 * time.Second):
		t.Fatal("no Werk model request")
		return cronModelRequest{}
	}
}

var cronConsentMark = wsinbox.Cron{ID: "cron-consent", Name: "check", At: "2026-03-10T09:00:00Z"}

const cronConsentPrompt = "scheduled consent fixture"

func waitForCronConsentRefusal(t *testing.T, h *leadHarness, id string) {
	t.Helper()
	want := (&wscred.ProviderError{Provider: wscred.ProviderOpenAI, Err: wscred.ErrConsentRequired}).Error()
	eventually(t, "settled cron consent refusal and covered sends", func() bool {
		rec := werkRecord(t, h, id)
		return rec.LastTurn != nil && rec.LastTurn.Result.Status == TurnFailed && rec.LastTurn.Result.Error == want &&
			rec.LastTurn.Mark == rec.Sends && rec.Drained == rec.Sends && h.state(id).Error == want
	})
}

func TestWerkCronSteeringRespectsPlanConsent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider wscred.ProviderID
		allowed  bool
		steers   bool
	}{
		{"plan consent off", wscred.ProviderOpenAI, false, false},
		{"plan consent on", wscred.ProviderOpenAI, true, true},
		{"non-plan provider", wscred.ProviderOpenRouter, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan cronModelRequest, 8)
			observe := func(ctx context.Context, req werk.Request) {
				requests <- cronModelRequest{werk.InteractiveTurn(ctx), req.Input}
			}
			release := make(chan struct{})
			reply := func(ctx context.Context, req werk.Request) (*werk.Response, error) {
				observe(ctx, req)
				return werkMessage("completed")(ctx, req)
			}
			model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
				func(ctx context.Context, req werk.Request) (*werk.Response, error) {
					observe(ctx, req)
					select {
					case <-release:
						return &werk.Response{Output: []json.RawMessage{json.RawMessage(werkCall("cron_tool", "git_status", `{}`))}}, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				},
				reply, reply, reply,
			}}
			h, fleet, _ := werkHarnessWithBackend(t, model, WerkBackend{credentials: cronPlanStatus{allowed: tc.allowed}})
			id := h.lead(BackendWerk, LeadOptions{Provider: string(tc.provider), Model: "fixture-model"}).ID
			h.send(id, "operator prompt")
			if req := nextCronModelRequest(t, requests); !req.interactive || !req.contains("operator prompt") {
				t.Fatalf("first request = %+v", req)
			}
			if ok, err := h.runner.DeliverCron(id, cronConsentMark, cronConsentPrompt); !ok || err != nil {
				t.Fatalf("DeliverCron = %v, %v", ok, err)
			}
			h.send(id, "operator steer")
			c := h.runner.conv(id)
			held, err := wsinbox.Held(c.inbox)
			if err != nil {
				t.Fatal(err)
			}
			operator := slices.IndexFunc(held, func(rec wsinbox.Record) bool { return rec.Entry.Operator })
			if operator < 0 {
				t.Fatal("missing held operator input")
			}
			if _, err := wsinbox.Steer(c.inbox, held[operator].Name); err != nil {
				t.Fatal(err)
			}
			if !c.do(c.steer) {
				t.Fatal("the conversation stopped before steering")
			}
			if ids := h.runner.CronHeld(id); (len(ids) == 0) != tc.steers {
				t.Fatalf("held cron during operator turn = %v, steer expected %v", ids, tc.steers)
			}
			close(release)
			if req := nextCronModelRequest(t, requests); !req.interactive || !req.contains("operator steer") || req.contains(cronConsentPrompt) != tc.steers {
				t.Fatalf("second request interactive=%v, operator steer=%v, cron=%v, want cron=%v", req.interactive, req.contains("operator steer"), req.contains(cronConsentPrompt), tc.steers)
			}
			if tc.steers {
				h.idle(id)
				return
			}
			waitForCronConsentRefusal(t, h, id)
			assertNextOperator := func(text string) {
				t.Helper()
				h.send(id, text)
				if req := nextCronModelRequest(t, requests); !req.interactive || !req.contains(text) || req.contains(cronConsentPrompt) {
					t.Fatalf("operator request %q interactive=%v, current prompt=%v, cron=%v", text, req.interactive, req.contains(text), req.contains(cronConsentPrompt))
				}
				h.idle(id)
			}
			assertNextOperator("operator after cron refusal")
			h.runner.Close()
			fleet.start(h)
			assertNextOperator("operator after reopening")
			drained, err := wsinbox.Drained(c.inbox)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, rec := range drained {
				if isCronInput(rec) {
					count++
				}
			}
			if count != 1 || model.requestCount() != 4 {
				t.Fatalf("after reopening: %d drained cron entries and %d model requests", count, model.requestCount())
			}
		})
	}
}

func TestWerkCronQueuedWithOperatorGetsSeparateConsentRefusal(t *testing.T) {
	requests := make(chan cronModelRequest, 4)
	release := make(chan struct{})
	model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
		func(ctx context.Context, req werk.Request) (*werk.Response, error) {
			requests <- cronModelRequest{werk.InteractiveTurn(ctx), req.Input}
			select {
			case <-release:
				return werkMessage("operator completed")(ctx, req)
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}}
	h, fleet, _ := werkHarnessWithBackend(t, model, WerkBackend{credentials: cronPlanStatus{}})
	var failed atomic.Bool
	h.opts.beforeDurableWrite = func(rec Record) error {
		if rec.LastTurn != nil && rec.LastTurn.Result.Status == TurnFailed && len(rec.DurableInboxClaims) > 0 && !failed.Swap(true) {
			return errors.New("simulated interruption before committing cron refusal")
		}
		return nil
	}
	h.runner.Close()
	fleet.start(h)
	id := h.lead(BackendWerk, LeadOptions{Provider: string(wscred.ProviderOpenAI), Model: "fixture-model"}).ID
	c := h.runner.conv(id)
	var sent bool
	var sendErr, cronErr error
	if !c.do(func() {
		// Both are ready before the loop can drain either one. Put cron
		// first, since it normally has steer priority over a follow-up.
		sent, cronErr = h.runner.DeliverCron(id, cronConsentMark, cronConsentPrompt)
		_, sendErr = h.runner.Send(id, "queued operator prompt")
	}) || !sent || cronErr != nil || sendErr != nil {
		t.Fatalf("enqueue cron and operator: cron=%v, cron error=%v, send error=%v", sent, cronErr, sendErr)
	}
	if req := nextCronModelRequest(t, requests); !req.interactive || !req.contains("queued operator prompt") || req.contains(cronConsentPrompt) {
		t.Fatalf("queued operator request interactive=%v, operator=%v, cron=%v", req.interactive, req.contains("queued operator prompt"), req.contains(cronConsentPrompt))
	}
	if ids := h.runner.CronHeld(id); !slices.Equal(ids, []string{cronConsentMark.ID}) {
		t.Fatalf("held cron during queued operator turn = %v", ids)
	}
	close(release)
	eventually(t, "the claimed cron refusal write was interrupted", func() bool {
		return failed.Load() && strings.Contains(h.state(id).Error, "save scheduled input refusal")
	})
	if rec := c.record(); rec.LastTurn == nil || rec.LastTurn.Mark >= rec.Sends {
		t.Fatalf("operator settlement covered the deferred send: %+v", rec.LastTurn)
	}
	h.runner.Close()
	fleet.start(h)
	waitForCronConsentRefusal(t, h, id)
	if count := model.requestCount(); count != 1 {
		t.Fatalf("%d requests after recovering the cron claim, want only the operator request", count)
	}
}

func TestWerkCronPreviouslyFlattenedQueueKeepsBackgroundInputSeparate(t *testing.T) {
	requests := make(chan cronModelRequest, 2)
	model := &werkModel{steps: []func(context.Context, werk.Request) (*werk.Response, error){
		func(ctx context.Context, req werk.Request) (*werk.Response, error) {
			requests <- cronModelRequest{werk.InteractiveTurn(ctx), req.Input}
			return werkMessage("operator completed")(ctx, req)
		},
	}}
	h, _, _ := werkHarnessWithBackend(t, model, WerkBackend{credentials: cronPlanStatus{}})
	id := h.lead(BackendWerk, LeadOptions{Provider: string(wscred.ProviderOpenAI), Model: "fixture-model"}).ID
	c := h.runner.conv(id)
	var err error
	if !c.do(func() {
		err = c.commitDurableRecord(func(rec *Record) {
			rec.Queued = []QueuedInput{{Text: cronConsentPrompt}, {Text: "queued operator prompt", Operator: true}}
			rec.Drained, rec.Sends = 2, 2
		}, func() { c.drained, c.sends = 2, 2 })
		c.signalInbox()
	}) || err != nil {
		t.Fatalf("stage flattened queue: %v", err)
	}
	if req := nextCronModelRequest(t, requests); !req.interactive || !req.contains("queued operator prompt") || req.contains(cronConsentPrompt) {
		t.Fatalf("recovered operator request interactive=%v, operator=%v, cron=%v", req.interactive, req.contains("queued operator prompt"), req.contains(cronConsentPrompt))
	}
	h.idle(id)
	if count := model.requestCount(); count != 1 {
		t.Fatalf("%d requests after processing the flattened queue, want only the operator request", count)
	}
}

// TestDeliverCronWaitsOutInFlightMCPCallOnAgy: on agy, whose steer replaces
// the running prompt, a scheduled prompt waits while a ws MCP call is in
// flight and steers as soon as that call ends.
func TestDeliverCronWaitsOutInFlightMCPCallOnAgy(t *testing.T) {
	var removals cronRemovals
	stop := wsinbox.ObserveCronRemoval(removals.add)
	defer stop()
	h, f := newAgyLeadHarness(t)
	h.startAt("http://127.0.0.1:1")
	id := h.lead(BackendAgy, LeadOptions{}).ID
	f.replyID = id
	h.send(id, "p1")
	holder := h.holder(id)
	eventually(t, "p1 sent", func() bool { return len(agyPromptIDs(h, holder)) == 1 })
	token := h.record(id).MCPToken
	if token == "" {
		t.Fatal("the agy lead has no MCP session token")
	}
	end := h.runner.MCPCallBegin(token)
	mark := wsinbox.Cron{ID: "cron-agy1", Name: "board", At: "2026-03-10T09:00:00+09:00"}
	if ok, err := h.runner.DeliverCron(id, mark, "check the board"); err != nil || !ok {
		t.Fatalf("DeliverCron = %v, %v", ok, err)
	}
	h.quiesce(id)
	if n := len(agyPromptIDs(h, holder)); n != 1 {
		t.Fatalf("the scheduled prompt steered during the MCP call: %d prompts", n)
	}
	if ids := h.runner.CronHeld(id); len(ids) != 1 || ids[0] != "cron-agy1" {
		t.Fatalf("held cron entries during MCP call = %v", ids)
	}
	if got := removals.got(); len(got) != 0 {
		t.Fatalf("observer fired while held for MCP call: %v", got)
	}
	end()
	eventually(t, "the held cron steers once the MCP call finishes", func() bool {
		return len(agyPromptIDs(h, holder)) == 2
	})
	if got := removals.got(); len(got) != 1 || got[0] != "cron-agy1" {
		t.Fatalf("observer after MCP call = %v", got)
	}
	if ids := h.runner.CronHeld(id); len(ids) != 0 {
		t.Fatalf("still held after MCP call ended: %v", ids)
	}
}

// TestDeliverCronRefusedForClosedUnknownAndNonLead: nothing is written and
// the outcome is "not accepted"; a close drops a held entry and reports it.
func TestDeliverCronRefusedForClosedUnknownAndNonLead(t *testing.T) {
	var removals cronRemovals
	stop := wsinbox.ObserveCronRemoval(removals.add)
	defer stop()
	h := newLeadHarness(t).start()
	id, holder := h.runLead("one")
	h.holders.out(holder, stateLine(HarnessRunning), initLine("sess-1", "auto"), toolUseLine("u1", "toolu_1"))
	h.sync(id, holder)
	mark := wsinbox.Cron{ID: "cron-bb22", Name: "n", At: "2026-03-10T09:00:00Z"}
	if ok, err := h.runner.DeliverCron("nobody", mark, "x"); err != nil || ok {
		t.Fatalf("unknown conversation = %v, %v", ok, err)
	}
	if ok, err := h.runner.DeliverCron(id, mark, "held then dropped"); err != nil || !ok {
		t.Fatalf("DeliverCron = %v, %v", ok, err)
	}
	if _, err := h.runner.CloseConversation(id); err != nil {
		t.Fatal(err)
	}
	if got := removals.got(); len(got) != 1 || got[0] != "cron-bb22" {
		t.Fatalf("the held-entry drop observed %v", got)
	}
	if ok, err := h.runner.DeliverCron(id, mark, "late"); err != nil || ok {
		t.Fatalf("closed conversation = %v, %v", ok, err)
	}
	if _, err := h.runner.CronIsLead(id); err == nil {
		t.Fatal("a closed conversation is a delivery target")
	}
	if !h.runner.CronKnown(id) {
		t.Fatal("a closed conversation is still known")
	}
	if h.runner.CronKnown("nobody") {
		t.Fatal("an unknown conversation is known")
	}
}

func TestDeliverCronRefusesANonLead(t *testing.T) {
	h := newHarness(t, map[string]string{"chat": chatProfile})
	id := h.create("chat").ID
	if lead, err := h.runner.CronIsLead(id); err != nil || lead {
		t.Fatalf("CronIsLead = %v, %v", lead, err)
	}
	mark := wsinbox.Cron{ID: "cron-cc33", Name: "n", At: "2026-03-10T09:00:00Z"}
	if ok, err := h.runner.DeliverCron(id, mark, "x"); err != nil || ok {
		t.Fatalf("non-lead = %v, %v", ok, err)
	}
	held, _ := wsinbox.Held(wsinbox.Dir(wsdashboard.SessionDir(h.dir, id)))
	if len(held) != 0 {
		t.Fatalf("a refused delivery wrote %+v", held)
	}
}

// TestForgetClosedReportsPermanentEndOnly: ForgetClosed of a closed
// conversation tells the scheduler; one the runner still holds open is not
// gone.
func TestForgetClosedReportsPermanentEndOnly(t *testing.T) {
	var gone cronGoneLog
	h := newLeadHarness(t).start()
	h.runner.BindCron(&gone)
	id, holder := h.runLead("one")
	h.holders.out(holder, turnLines("sess-1", "u1", "done")...)
	h.idle(id)
	h.runner.ForgetClosed(id) // still open: nothing is forgotten
	if got := gone.got(); len(got) != 0 {
		t.Fatalf("an open conversation was reported gone: %v", got)
	}
	if _, err := h.runner.CloseConversation(id); err != nil {
		t.Fatal(err)
	}
	if got := gone.got(); len(got) != 0 {
		t.Fatalf("a close alone was reported gone: %v", got)
	}
	h.runner.ForgetClosed(id)
	if got := gone.got(); len(got) != 1 || got[0] != id {
		t.Fatalf("ForgetClosed reported %v, want [%s]", got, id)
	}
	if h.runner.CronKnown(id) {
		t.Fatal("a forgotten conversation is still known")
	}
	h.runner.BindCron(nil)
	h.runner.ForgetClosed(id)
}

// TestCronIsNotAChildTool: a scheduled prompt can only reach a hosted lead,
// so no worker allowlist lists cron; a worker's call is refused by the
// server (TestCronRefusesAWorker in the mcp package).
func TestCronIsNotAChildTool(t *testing.T) {
	for name, list := range map[string][]string{
		"ChildSafeWSTools": ChildSafeWSTools, "AgentWSTools": AgentWSTools,
		"workerWorkflowTools": workerWorkflowTools,
	} {
		if slices.Contains(list, "cron") {
			t.Errorf("%s lists cron", name)
		}
	}
}

// TestAgyLeadReachesCronThroughTheCallFallback pins the mechanism: agy lists
// only a curated set of tools directly, and cron is not among them, so an agy
// lead calls it through the call fallback its registration enables.
func TestAgyLeadReachesCronThroughTheCallFallback(t *testing.T) {
	_, reg := (AgyBackend{}).registration()
	if slices.Contains(reg.DirectTools, "cron") {
		t.Fatalf("agy lists cron directly: %v", reg.DirectTools)
	}
	if !reg.CallFallback {
		t.Fatal("agy has no call fallback, so its lead could not reach cron")
	}
}

// cronFileOps runs fn and returns the wsstate-counted file operations it
// caused, per operation kind and per source site.
func cronFileOps(fn func()) (kinds, sites map[string]int64) {
	before := wsstate.FileIOCounters()
	fn()
	after := wsstate.FileIOCounters()
	kinds, sites = map[string]int64{}, map[string]int64{}
	for k, v := range after {
		if d := v - before[k]; d != 0 {
			if strings.HasPrefix(k, "file_io_site:") {
				sites[strings.TrimPrefix(k, "file_io_site:")] = d
			} else {
				kinds[k] = d
			}
		}
	}
	return kinds, sites
}

// TestDeliverCronFileOperationsAtTheProductionSeam measures one accepted and
// one suppressed delivery through the runner's real seam: the inbox entry
// write and the record publication an accepted one causes, and nothing for
// a suppressed one. SQLite commits are not file operations in this count (the
// store's own file I/O does not pass through wsstate) and are measured in
// wscron. The logged figures are what the ticket records; the assertions
// bound the kinds that must not appear on this path.
func TestDeliverCronFileOperationsAtTheProductionSeam(t *testing.T) {
	h := newLeadHarness(t).start()
	id, holder := h.runLead("one")
	h.holders.out(holder, stateLine(HarnessRunning), initLine("sess-1", "auto"), toolUseLine("u1", "toolu_1"))
	h.sync(id, holder)
	mark := wsinbox.Cron{ID: "cron-cc33", Name: "n", At: "2026-03-10T09:00:00Z"}
	var ok bool
	kinds, sites := cronFileOps(func() { ok, _ = h.runner.DeliverCron(id, mark, "tick") })
	if !ok {
		t.Fatal("not delivered")
	}
	t.Logf("accepted delivery: file operations by kind %v", kinds)
	t.Logf("accepted delivery: file operations by site %v", sites)
	// The counters are process-wide, so the runner's own follow-ups to an
	// input (a second record rewrite, a registry read after the wake) can
	// land in the sample; what is pinned is that the two writes the delivery
	// itself makes are there and that each was one temp file plus one atomic
	// replace.
	// Sites are matched by file and kind, not line: the line moves with every
	// edit to those files.
	for _, want := range []struct{ file, kind string }{{"wsinbox/inbox_store.go:", ":create_temp"}, {"wsagent/record.go:", ":create_temp"}} {
		found := false
		for site := range sites {
			if strings.HasPrefix(site, want.file) && strings.HasSuffix(site, want.kind) {
				found = true
			}
		}
		if !found {
			t.Errorf("no %s*%s in %v", want.file, want.kind, sites)
		}
	}
	if kinds["file_io_create_temp"] < 2 || kinds["file_io_atomic_replace"] < 2 {
		t.Errorf("the inbox write and the record write are each a temp file and an atomic replace: %v", kinds)
	}
	if _, err := h.runner.CloseConversation(id); err != nil {
		t.Fatal(err)
	}
	kinds, sites = cronFileOps(func() { ok, _ = h.runner.DeliverCron(id, mark, "late") })
	if ok {
		t.Fatal("a closed conversation accepted a delivery")
	}
	t.Logf("suppressed delivery: file operations by kind %v by site %v", kinds, sites)
	if kinds["file_io_total"] != 0 {
		t.Errorf("a suppressed delivery ran file operations: %v %v", kinds, sites)
	}
}

// runnerCronSessions is the runner as the real scheduler's target side, the
// way the daemon wires it (cmd/ws-mcp cronSessions).
type runnerCronSessions struct{ r *Runner }

func (s runnerCronSessions) IsLead(id string) (bool, error) { return s.r.CronIsLead(id) }
func (s runnerCronSessions) Exists(id string) bool          { return s.r.CronKnown(id) }
func (s runnerCronSessions) HeldSchedules(id string) []string {
	return s.r.CronHeld(id)
}
func (s runnerCronSessions) Deliver(id string, mark wsinbox.Cron, prompt string) (wscron.Delivery, error) {
	ok, err := s.r.DeliverCron(id, mark, prompt)
	if err != nil {
		return 0, err
	}
	if !ok {
		return wscron.Suppressed, nil
	}
	return wscron.Delivered, nil
}

// TestForgetClosedRemovesAHostedLeadsStoredSchedules drives a hosted lead
// through close and the real ForgetClosed with the real scheduler and store:
// the rows are gone the moment ForgetClosed returns (no restart reaping), a
// close alone keeps them, and the lead's retained binding is refused after.
func TestForgetClosedRemovesAHostedLeadsStoredSchedules(t *testing.T) {
	h := newLeadHarness(t).start()
	id, holder := h.runLead("one")
	h.holders.out(holder, turnLines("sess-1", "u1", "done")...)
	h.idle(id)
	store, err := wscron.OpenStore(filepath.Join(t.TempDir(), wscron.StoreName))
	if err != nil {
		t.Fatal(err)
	}
	sched, err := wscron.New(wscron.Options{Store: store, Sessions: runnerCronSessions{r: h.runner}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sched.Close() })
	h.runner.BindCron(sched)
	t.Cleanup(func() { h.runner.BindCron(nil) })
	rows := func() int {
		got, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	for _, name := range []string{"a", "b"} {
		if _, _, err := sched.Add(id, name, "@daily", "tick", ""); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}
	if rows() != 2 {
		t.Fatalf("stored rows = %d, want 2", rows())
	}

	// A close keeps the schedules: the conversation is closed, not gone.
	if _, err := h.runner.CloseConversation(id); err != nil {
		t.Fatal(err)
	}
	if rows() != 2 || len(sched.List(id)) != 2 {
		t.Fatalf("a close removed schedules: %d rows, %d live", rows(), len(sched.List(id)))
	}
	// The retained binding is refused while closed: no delivery target.
	if lead, err := sched.IsLead(id); lead || err == nil {
		t.Fatalf("a closed lead is a target: %v, %v", lead, err)
	}

	h.runner.ForgetClosed(id)
	if got := rows(); got != 0 {
		t.Fatalf("stored rows right after ForgetClosed = %d, want 0", got)
	}
	if got := len(sched.List(id)); got != 0 {
		t.Fatalf("%d live schedules after ForgetClosed", got)
	}
	if lead, err := sched.IsLead(id); lead || err == nil {
		t.Fatalf("a forgotten lead is a target: %v, %v", lead, err)
	}
	if _, _, err := sched.Add(id, "late", "@daily", "tick", ""); !errors.Is(err, wscron.ErrNotLead) {
		t.Fatalf("add through the retained binding = %v, want ErrNotLead", err)
	}
	if got := rows(); got != 0 {
		t.Fatalf("a refused add wrote %d rows", got)
	}
}
