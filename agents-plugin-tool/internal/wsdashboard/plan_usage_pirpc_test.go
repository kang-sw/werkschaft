package wsdashboard

import "testing"

func TestPlanUsageClassifyActiveSlotsNeverLeaksPiRPCIntoGPT(t *testing.T) {
	t.Run("registered traits without codex", func(t *testing.T) {
		store := &planUsageStore{
			harnesses: HarnessTraitSet{
				"claude": {PushesClaudeUsage: true},
				"pi-rpc": {ReplayClientName: "ws-pi-rpc-bridge"},
			},
		}
		claudeH, claudeSlot, gptH, gptSlot := store.classifyActiveSlotsLocked(map[string]string{
			"claude": "c-slot",
			"pi-rpc": "pi-slot",
		})
		if claudeH != "claude" || claudeSlot != "c-slot" {
			t.Fatalf("claude = (%q, %q), want (claude, c-slot)", claudeH, claudeSlot)
		}
		if gptH != "" || gptSlot != "" {
			t.Fatalf("pi-rpc leaked into gpt slot: gpt=(%q, %q)", gptH, gptSlot)
		}
	})

	t.Run("empty traits without codex", func(t *testing.T) {
		store := &planUsageStore{
			claude: planUsageEntry{Harness: "claude"},
		}
		claudeH, claudeSlot, gptH, gptSlot := store.classifyActiveSlotsLocked(map[string]string{
			"claude": "c-slot",
			"pi-rpc": "pi-slot",
		})
		if claudeH != "claude" || claudeSlot != "c-slot" {
			t.Fatalf("claude = (%q, %q), want (claude, c-slot)", claudeH, claudeSlot)
		}
		if gptH != "" || gptSlot != "" {
			t.Fatalf("pi-rpc leaked into gpt slot with empty traits: gpt=(%q, %q)", gptH, gptSlot)
		}
	})

	t.Run("empty traits with codex and pi-rpc still classifies codex", func(t *testing.T) {
		store := &planUsageStore{
			claude: planUsageEntry{Harness: "claude"},
		}
		claudeH, claudeSlot, gptH, gptSlot := store.classifyActiveSlotsLocked(map[string]string{
			"claude": "c-slot",
			"codex":  "x-slot",
			"pi-rpc": "pi-slot",
		})
		if claudeH != "claude" || claudeSlot != "c-slot" {
			t.Fatalf("claude = (%q, %q), want (claude, c-slot)", claudeH, claudeSlot)
		}
		if gptH != "codex" || gptSlot != "x-slot" {
			t.Fatalf("gpt = (%q, %q), want (codex, x-slot)", gptH, gptSlot)
		}
	})
}
