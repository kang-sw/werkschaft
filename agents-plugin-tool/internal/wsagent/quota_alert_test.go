package wsagent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wslive"
)

func TestCodexAlertResetsAtFromLimits(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	nowMs := now.UnixMilli()
	fiveH := now.Add(2 * time.Hour).Unix()
	sevenD := now.Add(48 * time.Hour).Unix()
	expired := now.Add(-10 * time.Minute).Unix()

	cases := []struct {
		name   string
		limits []map[string]any
		wantMs int64
	}{
		{
			name:   "saturated 5h window",
			limits: []map[string]any{{"usedPercent": 100, "windowDurationMins": 300, "resetsAt": fiveH}},
			wantMs: fiveH * 1000,
		},
		{
			name:   "saturated 7d window",
			limits: []map[string]any{{"usedPercent": 100, "windowDurationMins": 10080, "resetsAt": sevenD}},
			wantMs: sevenD * 1000,
		},
		{
			name: "multiple saturated windows picks max future",
			limits: []map[string]any{
				{"usedPercent": 100, "windowDurationMins": 300, "resetsAt": fiveH},
				{"usedPercent": 100, "windowDurationMins": 10080, "resetsAt": sevenD},
			},
			wantMs: sevenD * 1000,
		},
		{
			name: "ignores unsaturated window even if later",
			limits: []map[string]any{
				{"usedPercent": 100, "windowDurationMins": 300, "resetsAt": fiveH},
				{"usedPercent": 42, "windowDurationMins": 10080, "resetsAt": sevenD},
			},
			wantMs: fiveH * 1000,
		},
		{
			name:   "expired saturated window falls back to 0",
			limits: []map[string]any{{"usedPercent": 100, "windowDurationMins": 300, "resetsAt": expired}},
			wantMs: 0,
		},
		{
			name:   "missing limits falls back to 0",
			limits: nil,
			wantMs: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newCodexMapper("thr-1")
			m.now = func() int64 { return nowMs }
			if len(tc.limits) > 0 {
				rl := map[string]any{"primary": tc.limits[0]}
				if len(tc.limits) > 1 {
					rl["secondary"] = tc.limits[1]
				}
				raw, _ := json.Marshal(map[string]any{
					"method": "account/rateLimits/updated",
					"params": map[string]any{"rateLimits": rl},
				})
				m.mapLine(raw, nopReplies{})
			}
			errLine, _ := json.Marshal(map[string]any{
				"method": "turn/completed",
				"params": map[string]any{
					"threadId": "thr-1",
					"turn": map[string]any{
						"id":     "turn-1",
						"status": "failed",
						"error": map[string]any{
							"message":        "Usage limit exceeded",
							"codexErrorInfo": "usageLimitExceeded",
						},
					},
				},
			})
			evs := m.mapLine(errLine, nopReplies{})
			var alert *wslive.Notice
			for _, ev := range evs {
				if ev.Alert != nil {
					alert = ev.Alert
				}
			}
			if alert == nil {
				t.Fatalf("expected NoticeUsageLimit alert in %+v", evs)
			}
			if alert.Kind != wslive.NoticeUsageLimit || alert.ResetsAt != tc.wantMs {
				t.Fatalf("alert = %+v, want kind=%q resetsAt=%d", alert, wslive.NoticeUsageLimit, tc.wantMs)
			}
		})
	}
}

func TestWerkMapperUsageLimitAlert(t *testing.T) {
	resetAt := time.Date(2026, 11, 24, 16, 0, 0, 0, time.UTC).UnixMilli()
	m := newWerkMapper("sess-1")

	t.Run("ordinary rate limit", func(t *testing.T) {
		evs := m.mapEvent(werk.Line{
			T:          werk.LineTurnEnd,
			Turn:       1,
			TurnID:     "t1",
			Reason:     werk.ReasonError,
			Error:      "the model endpoint answered 429: too many requests",
			UsageLimit: true,
			PlanPaused: false,
			ResetsAt:   resetAt,
		})
		var alertEv *Event
		var endIdx, alertIdx int = -1, -1
		for i := range evs {
			if evs[i].Alert != nil {
				alertEv = &evs[i]
				alertIdx = i
			}
			if evs[i].TurnEnd {
				endIdx = i
			}
		}
		if alertEv == nil || alertIdx < 0 || endIdx < 0 || alertIdx >= endIdx {
			t.Fatalf("expected Alert before TurnEnd, got %+v", evs)
		}
		if alertEv.Alert.Kind != wslive.NoticeUsageLimit || alertEv.Alert.ResetsAt != resetAt || alertEv.AlertManualOnly {
			t.Fatalf("alertEv = %+v", alertEv)
		}
	})

	t.Run("OpenAI plan pause sets AlertManualOnly", func(t *testing.T) {
		evs := m.mapEvent(werk.Line{
			T:          werk.LineTurnEnd,
			Turn:       2,
			TurnID:     "t2",
			Reason:     werk.ReasonError,
			Error:      "ChatGPT plan paused",
			UsageLimit: true,
			PlanPaused: true,
		})
		var alertEv *Event
		for i := range evs {
			if evs[i].Alert != nil {
				alertEv = &evs[i]
			}
		}
		if alertEv == nil || !alertEv.AlertManualOnly || alertEv.Alert.Kind != wslive.NoticeUsageLimit {
			t.Fatalf("expected manual-only NoticeUsageLimit, got %+v", evs)
		}
	})
}

func TestAgyMapperQuotaClassificationAndWatchdogExclusion(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	nowMs := now.UnixMilli()
	resetRFC := now.Add(3 * time.Hour).UTC().Format(time.RFC3339)

	t.Run("ACP msg.Error with RESOURCE_EXHAUSTED and data reset_time", func(t *testing.T) {
		m := newAgyMapper("sess-agy")
		m.now = func() int64 { return nowMs }
		m.inTurn = true
		raw, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      "prompt-1",
			"error": map[string]any{
				"code":    8,
				"message": "RESOURCE_EXHAUSTED: model quota exceeded",
				"data":    map[string]any{"reset_time": resetRFC},
			},
		})
		var msg agyMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		evs := m.endTurn(msg, nowMs)
		var alert *wslive.Notice
		var alertIdx, endIdx int = -1, -1
		for i, ev := range evs {
			if ev.Alert != nil {
				alert = ev.Alert
				alertIdx = i
			}
			if ev.TurnEnd {
				endIdx = i
			}
		}
		if alert == nil || alertIdx >= endIdx {
			t.Fatalf("expected Alert before TurnEnd, got %+v", evs)
		}
		wantMs := now.Add(3 * time.Hour).UnixMilli()
		if alert.Kind != wslive.NoticeUsageLimit || alert.ResetsAt != wantMs {
			t.Fatalf("alert = %+v, want resetsAt=%d", alert, wantMs)
		}
	})

	t.Run("anchored in-band modelFailure with 429 quota text emits NoticeUsageLimit and skips watchdog retry", func(t *testing.T) {
		failure := "⚠ API error (attempt 1): HTTP 429 RESOURCE_EXHAUSTED: You have exhausted your capacity on this model."
		if !isAgyQuotaText(failure) {
			t.Fatalf("expected isAgyQuotaText(%q) == true", failure)
		}
		m := newAgyMapper("sess-agy-2")
		m.now = func() int64 { return nowMs }
		m.inTurn = true
		m.modelFailure = failure
		evs := m.endTurn(agyMessage{Result: json.RawMessage(`{"stopReason":"end_turn"}`)}, nowMs)
		var alert *wslive.Notice
		for _, ev := range evs {
			if ev.Alert != nil {
				alert = ev.Alert
			}
		}
		if alert == nil || alert.Kind != wslive.NoticeUsageLimit || alert.Text != failure {
			t.Fatalf("expected NoticeUsageLimit with failure text, got %+v", evs)
		}

		// Watchdog must exclude quota-classified modelFailure from short-backoff continue retries.
		c := &conv{
			modelWaitArmed:      true,
			modelWaitMaxRetries: 3,
			turn:                true,
			proc:                &fakeWatchdogProc{},
		}
		c.onModelFailure(failure)
		if !c.modelWaitFailureExcluded || c.modelWaitFailure != failure {
			t.Fatalf("expected quota failure tracked without short-backoff retry, got excluded=%v failure=%q", c.modelWaitFailureExcluded, c.modelWaitFailure)
		}
		if c.interceptModelWaitTurnEnd(Event{TurnEnd: true, ModelFailure: failure}) {
			t.Fatalf("expected interceptModelWaitTurnEnd to return false for quota failure")
		}
	})

	t.Run("transient 503 UNAVAILABLE is not classified as quota", func(t *testing.T) {
		failure := "⚠ API error (attempt 1): HTTP 503 UNAVAILABLE: no capacity"
		if isAgyQuotaText(failure) {
			t.Fatalf("503 no capacity should not be classified as quota")
		}
	})

	t.Run("non-HTTP 429 substrings are not classified as quota", func(t *testing.T) {
		for _, text := range []string{
			"compile error at line 1429",
			"failed to connect to localhost port 4290",
			"request id 42999 timed out",
		} {
			if isAgyQuotaText(text) {
				t.Fatalf("isAgyQuotaText(%q) = true, want false", text)
			}
		}
		for _, text := range []string{
			"HTTP 429 error",
			"status: 429",
			"error 429: slowed down",
			"429 Too Many",
		} {
			if !isAgyQuotaText(text) {
				t.Fatalf("isAgyQuotaText(%q) = false, want true", text)
			}
		}
	})
}

type fakeWatchdogProc struct {
	Process
}

func (f *fakeWatchdogProc) Interrupt() error { return nil }

func TestConvAlertUsesQuotaResetAtFallback(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	cachedReset := now.Add(90 * time.Minute).UnixMilli()
	var gotBackend, gotModel string
	r := NewRunner(Options{
		Dir: t.TempDir(),
		Now: func() time.Time { return now },
		QuotaResetAt: func(backend, model string) int64 {
			gotBackend, gotModel = backend, model
			return cachedReset
		},
	})
	c := &conv{
		r:    r,
		rec:  Record{ReplyID: "lead-1", Backend: BackendAgy, Model: "gemini-2.5-pro", Lead: true},
		live: newLiveWriter(t.TempDir(), "", "lead-1", func() time.Time { return now }, r.opts.Log),
	}
	c.alert(wslive.Notice{Kind: wslive.NoticeUsageLimit, Text: "quota exceeded"}, false, false)
	if gotBackend != BackendAgy || gotModel != "gemini-2.5-pro" {
		t.Fatalf("QuotaResetAt called with (%q, %q)", gotBackend, gotModel)
	}
	if c.failure == nil || c.failure.ResetsAt != cachedReset {
		t.Fatalf("c.failure = %+v, want ResetsAt=%d", c.failure, cachedReset)
	}
}
