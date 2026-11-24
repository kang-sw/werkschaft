package wsdashboard

import (
	"testing"
	"time"
)

func TestPlanUsageStoreQuotaResetAtMemoryOnly(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	fiveH := now.Add(2 * time.Hour).UnixMilli()
	sevenD := now.Add(24 * time.Hour).UnixMilli()
	expired := now.Add(-5 * time.Minute).UnixMilli()

	s := &planUsageStore{
		now: func() time.Time { return now },
		claude: planUsageEntry{
			Harness: "claude",
			Windows: []planUsageWindow{
				{Kind: "five_hour", Percent: 100, ResetsAt: fiveH},
				{Kind: "seven_day", Percent: 50, ResetsAt: sevenD},
			},
		},
		gpt: planUsageEntry{
			Harness: "codex",
			Windows: []planUsageWindow{
				{Kind: "five_hour", Percent: 100, ResetsAt: expired},
			},
		},
		agyGroups: []planUsageEntry{
			{
				Harness: "agy",
				Group:   "gemini",
				Windows: []planUsageWindow{
					{Kind: "five_hour", Percent: 95, ResetsAt: fiveH},
				},
			},
			{
				Harness: "agy",
				Group:   "claude_gpt",
				Windows: []planUsageWindow{
					{Kind: "five_hour", Percent: 100, ResetsAt: fiveH},
					{Kind: "seven_day", Percent: 100, ResetsAt: sevenD},
				},
			},
		},
		readFile: func(string) ([]byte, error) {
			t.Fatalf("quotaResetAt must never perform disk reads")
			return nil, nil
		},
	}

	if got := s.quotaResetAt("agy", "gemini-2.5-pro"); got != fiveH {
		t.Fatalf("agy gemini = %d, want %d", got, fiveH)
	}
	if got := s.quotaResetAt("agy", "claude-sonnet-4-6"); got != sevenD {
		t.Fatalf("agy claude_gpt = %d, want %d", got, sevenD)
	}
	if got := s.quotaResetAt("claude", "claude-opus-4-6"); got != fiveH {
		t.Fatalf("claude = %d, want %d", got, fiveH)
	}
	if got := s.quotaResetAt("codex", "gpt-5"); got != 0 {
		t.Fatalf("expired codex = %d, want 0", got)
	}
	if got := s.quotaResetAt("werk", "gpt-5"); got != 0 {
		t.Fatalf("werk = %d, want 0", got)
	}
}
