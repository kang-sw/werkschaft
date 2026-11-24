package wsdashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wslive"
)

type fakeQuotaAgents struct {
	mu           sync.Mutex
	replyID      string
	canControl   bool
	status       string
	failure      *wslive.Notice
	qc           *wslive.QuotaContinuation
	resetsAt     int64
	resumeAt     int64
	scheduleHits int
	cancelHits   int
	messageHits  int
	slot         HostedSlot
}

func (a *fakeQuotaAgents) publishLocked() {
	if a.slot == nil {
		return
	}
	var host *wslive.Host
	if a.failure != nil || a.qc != nil {
		host = &wslive.Host{}
		if a.failure != nil {
			f := *a.failure
			host.Failure = &f
		}
		if a.qc != nil {
			q := *a.qc
			host.QuotaContinuation = &q
		}
	}
	var lead wslive.Lead
	if a.status == "running" {
		lead.TurnStartedAt = 1
	}
	a.slot.Publish(wslive.Snapshot{
		SchemaVersion: 1,
		Adapter:       "claude",
		UpdatedAt:     time.Now().UnixMilli(),
		HeartbeatMs:   30000,
		Host:          host,
		Lead:          lead,
	})
}

func (a *fakeQuotaAgents) statePayloadLocked() map[string]any {
	st := map[string]any{
		"id":          a.replyID,
		"backend":     "claude",
		"status":      a.status,
		"process":     true,
		"lead":        true,
		"can_control": a.canControl,
	}
	if a.failure != nil {
		st["failure"] = a.failure
	}
	if a.qc != nil {
		st["quota_continuation"] = map[string]any{
			"status":       a.qc.Status,
			"resume_at":    a.qc.ResumeAt,
			"attempts":     a.qc.Attempts,
			"max_attempts": a.qc.MaxAttempts,
			"manual":       a.qc.Manual,
			"notice_text":  a.qc.NoticeText,
		}
	}
	return st
}

func (a *fakeQuotaAgents) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/agents/"), "/")
		a.mu.Lock()
		defer a.mu.Unlock()
		switch {
		case rest == a.replyID && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(a.statePayloadLocked())
		case rest == a.replyID+"/quota-continuation" && r.Method == http.MethodPost:
			if !a.canControl {
				http.Error(w, "unknown agent route", http.StatusNotFound)
				return
			}
			a.scheduleHits++
			a.qc = &wslive.QuotaContinuation{
				Status:   "scheduled",
				ResumeAt: a.resumeAt,
				Manual:   true,
			}
			a.publishLocked()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(a.statePayloadLocked())
		case rest == a.replyID+"/quota-continuation/cancel" && r.Method == http.MethodPost:
			if !a.canControl {
				http.Error(w, "unknown agent route", http.StatusNotFound)
				return
			}
			a.cancelHits++
			a.qc = nil
			a.publishLocked()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(a.statePayloadLocked())
		case rest == a.replyID+"/test-state" && r.Method == http.MethodPost:
			var body struct {
				CanControl *bool                     `json:"can_control"`
				Status     string                    `json:"status"`
				QC         *wslive.QuotaContinuation `json:"qc"`
				ClearQC    bool                      `json:"clear_qc"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if body.CanControl != nil {
				a.canControl = *body.CanControl
			}
			if body.Status != "" {
				a.status = body.Status
			}
			if body.ClearQC {
				a.qc = nil
			} else if body.QC != nil {
				q := *body.QC
				a.qc = &q
			}
			a.publishLocked()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(a.statePayloadLocked())
		default:
			http.NotFound(w, r)
		}
	})
}

func (a *fakeQuotaAgents) SetAuditSink(wsaudit.Sink)           {}
func (a *fakeQuotaAgents) Start(func(replyID, project string)) {}
func (a *fakeQuotaAgents) InboxChanged(string)                 {}
func (a *fakeQuotaAgents) PageInput(id string, write func() bool) error {
	if id == a.replyID && write() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.messageHits++
		a.qc = nil
		a.status = "running"
		a.publishLocked()
	}
	return nil
}
func (a *fakeQuotaAgents) Close() {}

// TestQuotaContinuationUIBrowser verifies the Phase 3 quota-continuation UI
// end-to-end in Chromium: manual schedule CTA, scheduled static <time data-f="dt">
// with zero countdown timers/idle animations, cancel CTA, in_flight hiding,
// exhausted state with re-schedule CTA, and read-only (can_control=false) button
// omission. When WS_QUOTA_CONTINUATION_SHOT_DIR is set, saves dark/light shots.
func TestQuotaContinuationUIBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Chromium quota continuation UI test")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	shots := os.Getenv("WS_QUOTA_CONTINUATION_SHOT_DIR")
	if shots != "" {
		if err := os.MkdirAll(shots, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now()
	resetsAt := now.Add(2 * time.Hour).UnixMilli()
	resumeAt := now.Add(2*time.Hour + 45*time.Second).UnixMilli()
	agents := &fakeQuotaAgents{
		replyID:    "livesession",
		canControl: true,
		status:     "running",
		resetsAt:   resetsAt,
		resumeAt:   resumeAt,
		failure: &wslive.Notice{
			Kind:     wslive.NoticeUsageLimit,
			Text:     "You've hit your usage limit for this window.",
			ResetsAt: resetsAt,
		},
	}

	f := startPageFixture(t, func(o *DaemonOptions) {
		o.Agents = agents
	})
	// Mark livesession as hosted in its registration so dashboard.tmpl renders
	// data-hosted="1" and #qcbox.
	regPath := filepath.Join(SessionDir(f.dir, "livesession"), "registration.json")
	regData, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatal(err)
	}
	var reg Registration
	if err := json.Unmarshal(regData, &reg); err != nil {
		t.Fatal(err)
	}
	reg.Hosted = true
	updated, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regPath, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	f.d.Index().Refresh()
	slot, ok := f.d.OpenHostedSlot(SessionDir(f.dir, "livesession"))
	if !ok {
		t.Fatal("no hosted slot for livesession")
	}
	agents.mu.Lock()
	agents.slot = slot
	agents.mu.Unlock()

	if err := wslive.AppendRing(wslive.RingDir(SessionDir(f.dir, "livesession")), []wslive.RingEntry{
		{
			Kind: wslive.RingNotice,
			Notice: &wslive.Notice{
				Kind:     wslive.NoticeUsageLimit,
				Text:     "You've hit your usage limit for this window.",
				ResetsAt: resetsAt,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	slot.RingAppended()
	agents.mu.Lock()
	agents.publishLocked()
	agents.mu.Unlock()

	script := `
const assert = require('node:assert/strict');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

async function setState(targetFrame, payload) {
  const status = await targetFrame.evaluate(async (body) => {
    const res = await fetch('/agents/livesession/test-state', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    return res.status;
  }, payload);
  assert.equal(status, 200, 'test-state status');
}

(async () => {
  const base = process.env.QC_BASE;
  const shots = process.env.QC_SHOTS || '';
  const resumeAt = Number(process.env.QC_RESUME_AT);
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark', locale: 'en-US' });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', e => errors.push(e.message));
    await page.goto(base + '/s/livesession', { waitUntil: 'networkidle' });
    const frame = await (await page.locator('iframe.sframe.on').elementHandle()).contentFrame();

    // 1. The quota alert is visible in the live region while the lead is
    // running. Settlement alone should make manual scheduling available.
    await frame.waitForSelector('.clive[data-qc-fail="usage_limit"][data-turn]');
    assert.equal(await frame.locator('#qcbox button[data-qc-act="schedule"]').count(), 0);
    await setState(frame, { status: 'idle' });
    await frame.waitForSelector('.clive[data-qc-fail="usage_limit"]:not([data-turn])');
    await frame.waitForSelector('#qcbox .qccard[data-qc-mode="ready"]', { timeout: 10000 });
    const schedBtn = frame.locator('#qcbox button[data-qc-act="schedule"]');
    assert.equal(await schedBtn.count(), 1, 'schedule button rendered');
    assert.equal((await schedBtn.textContent()).trim(), 'Schedule continuation');
    assert.equal(await frame.locator('#qcbox button[data-qc-act="cancel"]').count(), 0);
    if (shots) await page.screenshot({ path: shots + '/after-dark-manual-cta.png' });

    // 2. Click "Schedule continuation" -> transitions to scheduled state with static <time data-f="dt"> and Cancel CTA
    await schedBtn.click();
    await frame.waitForSelector('#qcbox .qccard[data-qc-mode="scheduled"]', { timeout: 10000 });
    const timeEl = frame.locator('#qcbox .qccard time.t[data-f="dt"]');
    assert.equal(await timeEl.count(), 1, 'static dt time element rendered');
    assert.equal(await timeEl.getAttribute('data-ms'), String(resumeAt));
    const timeBefore = (await timeEl.textContent()).trim();
    assert.ok(timeBefore.length > 0, 'formatted resume timestamp is non-empty');
    assert.equal(await frame.locator('#qcbox button[data-qc-act="cancel"]').count(), 1, 'cancel button rendered');
    // Verify ZERO idle CSS animations on #qcbox and ZERO per-second text mutation on static <time data-f="dt">.
    const animCount = await frame.evaluate(() => {
      const box = document.getElementById('qcbox');
      return box ? box.getAnimations({ subtree: true }).length : -1;
    });
    assert.equal(animCount, 0, 'zero idle CSS animations in #qcbox');
    await page.waitForTimeout(1200);
    const timeAfter = (await timeEl.textContent()).trim();
    assert.equal(timeAfter, timeBefore, 'static resume timestamp does not tick');
    if (shots) await page.screenshot({ path: shots + '/after-dark-scheduled.png' });

    // 3. Light theme scheduled state
    await page.emulateMedia({ colorScheme: 'light' });
    await page.waitForTimeout(150);
    if (shots) await page.screenshot({ path: shots + '/after-light-scheduled.png' });
    await page.emulateMedia({ colorScheme: 'dark' });

    // 4. Click "Cancel" -> returns to manual CTA state
    await frame.locator('#qcbox button[data-qc-act="cancel"]').click();
    await frame.waitForSelector('#qcbox .qccard[data-qc-mode="ready"]', { timeout: 10000 });

    // Sending an operator message through the composer's ordinary
    // message route removes the scheduled UI through the live update.
    await schedBtn.click();
    await frame.waitForSelector('#qcbox .qccard[data-qc-mode="scheduled"]');
    await frame.locator('#ctext + .wsc-editor').fill('Continue with this new input');
    const sent = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith('/message'));
    await frame.locator('#csend').click();
    assert.equal((await sent).status(), 200, 'composer message accepted');
    await frame.waitForSelector('#qcbox .qccard[data-qc-mode="scheduled"]', { state: 'hidden' });
    assert.equal(await frame.locator('#qcbox button[data-qc-act="cancel"]').count(), 0);

    // 5. in_flight hides #qcbox and header qc-chip
    await setState(frame, { qc: { status: 'in_flight' } });
    await frame.waitForFunction(() => {
      const b = document.getElementById('qcbox');
      return b && b.hidden === true && !document.querySelector('.qc-chip');
    }, null, { timeout: 10000 });

    // 6. Exhausted state (dark) with re-schedule CTA
    await setState(frame, { qc: { status: 'exhausted', attempts: 6, maxAttempts: 6 } });
    await frame.waitForSelector('#qcbox .qccard[data-qc-mode="exhausted"]', { timeout: 10000 });
    assert.equal(await frame.locator('#qcbox button[data-qc-act="schedule"]').count(), 1, 're-schedule button rendered on exhausted');
    if (shots) await page.screenshot({ path: shots + '/after-dark-exhausted.png' });

    // 7. Read-only / LAN viewer (can_control=false): scheduled status shown, ZERO buttons in DOM
    await setState(frame, { can_control: false, qc: { status: 'scheduled', resumeAt: resumeAt, attempts: 1, maxAttempts: 6 } });
    await page.reload({ waitUntil: 'networkidle' });
    const roFrame = await (await page.locator('iframe.sframe.on').elementHandle()).contentFrame();
    await roFrame.waitForSelector('#qcbox .qccard[data-qc-mode="scheduled"]', { timeout: 10000 });
    assert.equal(await roFrame.locator('#qcbox button').count(), 0, 'LAN read-only must have ZERO buttons in #qcbox');
    assert.equal(await roFrame.locator('#qcbox .qccard time.t[data-f="dt"]').count(), 1, 'LAN read-only still shows static resume time');
    if (shots) await page.screenshot({ path: shots + '/after-dark-readonly.png' });

    assert.deepEqual(errors, [], 'no pageerrors');
    console.log('DONE');
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exit(1); });
`
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "-e", script)
	cmd.Env = append(os.Environ(),
		"QC_BASE="+f.base,
		"QC_SHOTS="+shots,
		"QC_RESUME_AT="+strconv.FormatInt(resumeAt, 10),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("quota continuation browser test failed: %v\n%s", err, string(out))
	}
	agents.mu.Lock()
	sh, ch, mh := agents.scheduleHits, agents.cancelHits, agents.messageHits
	agents.mu.Unlock()
	if sh != 2 || ch != 1 || mh != 1 {
		t.Fatalf("expected 2 schedule, 1 cancel and 1 composer POST, got schedule=%d cancel=%d composer=%d", sh, ch, mh)
	}
	if shots != "" {
		for _, name := range []string{
			"after-dark-manual-cta.png",
			"after-dark-scheduled.png",
			"after-light-scheduled.png",
			"after-dark-exhausted.png",
			"after-dark-readonly.png",
		} {
			if _, err := os.Stat(filepath.Join(shots, name)); err != nil {
				t.Errorf("missing screenshot %s: %v", name, err)
			}
		}
	}
}
