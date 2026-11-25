package werk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wscred"
)

func TestParseRateLimitReset(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	nowMs := now.UnixMilli()

	t.Run("relative durations and Retry-After seconds", func(t *testing.T) {
		h := http.Header{}
		h.Set("Retry-After", "45")
		h.Set("x-ratelimit-reset-requests", "1m30s")
		h.Set("x-ratelimit-reset-tokens", "500ms")
		got := parseRateLimitReset(h, now)
		want := now.Add(90 * time.Second).UnixMilli()
		if got != want {
			t.Fatalf("got %d, want %d", got, want)
		}
	})

	t.Run("Retry-After HTTP-date vs anthropic RFC3339 takes max future", func(t *testing.T) {
		h := http.Header{}
		h.Set("Retry-After", now.Add(5*time.Minute).UTC().Format(http.TimeFormat))
		h.Set("anthropic-ratelimit-requests-reset", now.Add(15*time.Minute).UTC().Format(time.RFC3339))
		h.Set("anthropic-ratelimit-tokens-reset", now.Add(2*time.Minute).UTC().Format(time.RFC3339))
		got := parseRateLimitReset(h, now)
		want := now.Add(15 * time.Minute).UnixMilli()
		if got != want {
			t.Fatalf("got %d, want %d", got, want)
		}
	})

	t.Run("ignores past and unparseable values", func(t *testing.T) {
		h := http.Header{}
		h.Set("Retry-After", "-10")
		h.Set("x-ratelimit-reset-requests", "not-a-duration")
		h.Set("anthropic-ratelimit-tokens-reset", now.Add(-1*time.Minute).UTC().Format(time.RFC3339))
		if got := parseRateLimitReset(h, now); got != 0 {
			t.Fatalf("got %d, want 0 (now=%d)", got, nowMs)
		}
	})
}

func TestClassifyTurnError(t *testing.T) {
	resetAt := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC).UnixMilli()

	t.Run("OpenAI plan paused sentinel", func(t *testing.T) {
		end := classifyTurnError(wscred.ErrOpenAIPlanPaused)
		if !end.UsageLimit || !end.PlanPaused || end.ErrorCode != openAIPlanLimitCode {
			t.Fatalf("end = %+v", end)
		}
	})

	t.Run("OpenAI plan limit HTTP code", func(t *testing.T) {
		end := classifyTurnError(&HTTPError{Status: http.StatusTooManyRequests, Code: openAIPlanLimitCode, Message: "plan paused", ResetsAt: resetAt})
		if !end.UsageLimit || !end.PlanPaused || end.ErrorCode != openAIPlanLimitCode || end.ResetsAt != resetAt {
			t.Fatalf("end = %+v", end)
		}
	})

	t.Run("ordinary 429 rate limit", func(t *testing.T) {
		end := classifyTurnError(&HTTPError{Status: http.StatusTooManyRequests, Code: "rate_limit_exceeded", Message: "too many requests", ResetsAt: resetAt})
		if !end.UsageLimit || end.PlanPaused || end.ResetsAt != resetAt || end.ErrorCode != "rate_limit_exceeded" {
			t.Fatalf("end = %+v", end)
		}
	})

	t.Run("wrapped MaxRetryAfter error preserves HTTPError reset", func(t *testing.T) {
		raw := &HTTPError{Status: http.StatusTooManyRequests, Code: "rate_limit_exceeded", RetryAfter: 10 * time.Minute, ResetsAt: resetAt}
		wrapped := fmt.Errorf("asked to wait longer (%w)", raw)
		end := classifyTurnError(wrapped)
		if !end.UsageLimit || end.PlanPaused || end.ResetsAt != resetAt {
			t.Fatalf("end = %+v", end)
		}
	})

	t.Run("FailedError rate_limit_exceeded", func(t *testing.T) {
		end := classifyTurnError(&FailedError{Code: "rate_limit_exceeded", Message: "slow down"})
		if !end.UsageLimit || end.PlanPaused || end.ErrorCode != "rate_limit_exceeded" {
			t.Fatalf("end = %+v", end)
		}
	})

	t.Run("non-quota error is not UsageLimit", func(t *testing.T) {
		end := classifyTurnError(planProbeError{errors.New("500 internal error")})
		if end.UsageLimit || end.PlanPaused {
			t.Fatalf("end = %+v", end)
		}
	})
}

type stubQuotaEndpoint struct {
	err error
}

func (s stubQuotaEndpoint) Respond(context.Context, Request, StreamHandler) (*Response, error) {
	return nil, s.err
}

func TestAgentTurnEndEmitsUsageLimitFields(t *testing.T) {
	resetAt := time.Now().Add(20 * time.Minute).UnixMilli()
	cases := []struct {
		name           string
		err            error
		wantUsageLimit bool
		wantPlanPaused bool
		wantResetsAt   int64
	}{
		{
			name:           "MaxRetryAfter 429 cutoff",
			err:            &HTTPError{Status: http.StatusTooManyRequests, Code: "rate_limit_exceeded", Message: "quota", RetryAfter: MaxRetryAfter + time.Minute, ResetsAt: resetAt},
			wantUsageLimit: true,
			wantPlanPaused: false,
			wantResetsAt:   resetAt,
		},
		{
			name:           "OpenAI plan paused",
			err:            wscred.ErrOpenAIPlanPaused,
			wantUsageLimit: true,
			wantPlanPaused: true,
			wantResetsAt:   0,
		},
		{
			name:           "ordinary 500 error",
			err:            &HTTPError{Status: http.StatusInternalServerError, Message: "boom"},
			wantUsageLimit: false,
			wantPlanPaused: false,
			wantResetsAt:   0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var lines []Line
			a, err := New(Config{
				Dir:        t.TempDir(),
				Endpoint:   stubQuotaEndpoint{err: tc.err},
				Model:      "test-model",
				Authority:  AuthorityFull,
				MaxRetries: 1,
				Sleep:      func(context.Context, time.Duration) error { return nil },
				Backoff:    func(int) time.Duration { return 0 },
				Emit:       func(l Line) { lines = append(lines, l) },
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer a.Close()
			a.runTurn(context.Background(), "hello")
			var end *Line
			for i := range lines {
				if lines[i].T == LineTurnEnd {
					end = &lines[i]
				}
			}
			if end == nil {
				t.Fatalf("missing LineTurnEnd in %+v", lines)
			}
			raw, err := json.Marshal(end)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			parsed, err := ParseLine(raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if parsed.UsageLimit != tc.wantUsageLimit || parsed.PlanPaused != tc.wantPlanPaused || parsed.ResetsAt != tc.wantResetsAt {
				t.Fatalf("parsed = %+v, want usageLimit=%v planPaused=%v resetsAt=%d", parsed, tc.wantUsageLimit, tc.wantPlanPaused, tc.wantResetsAt)
			}
		})
	}
}
