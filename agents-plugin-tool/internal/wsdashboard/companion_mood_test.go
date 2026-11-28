package wsdashboard

import (
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCompanionMoodBaselineTransitionsAndClamping(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	tracker := NewCompanionMoodTracker(func() time.Time { return now }, 30*time.Minute)

	base := CompanionMoodBaseline()
	got := tracker.Snapshot()
	if got.Energy != base.Energy || got.Focus != base.Focus || got.Rapport != base.Rapport || got.Stress != base.Stress || got.Empathy != base.Empathy {
		t.Fatalf("initial mood = %+v, want baseline %+v", got, base)
	}
	if got.Label() != "calm" {
		t.Fatalf("baseline label = %q, want calm", got.Label())
	}

	// Tool error spikes stress & empathy and slightly boosts focus.
	afterErr := tracker.Observe(CompanionMoodEventToolError)
	if afterErr.Stress <= base.Stress || afterErr.Empathy <= base.Empathy || afterErr.Focus <= base.Focus {
		t.Fatalf("tool_error did not shift stress/empathy/focus: %+v", afterErr)
	}
	if afterErr.LastEvent != CompanionMoodEventToolError {
		t.Fatalf("LastEvent = %q, want tool_error", afterErr.LastEvent)
	}

	// Repeated errors saturate cleanly at 1.0 (never NaN or > 1.0) and trigger concerned-supportive label.
	var stressed CompanionMoodState
	for range 10 {
		stressed = tracker.Observe(CompanionMoodEventToolError)
	}
	if stressed.Stress != 1.0 || stressed.Empathy != 1.0 || stressed.Energy < 0 || stressed.Energy > 1 {
		t.Fatalf("clamping failed after repeated errors: %+v", stressed)
	}
	if stressed.Label() != "concerned-supportive" {
		t.Fatalf("high stress+empathy label = %q, want concerned-supportive", stressed.Label())
	}

	// Reset restores baseline deterministically (simulating restart per D11).
	tracker.Reset()
	reset := tracker.Snapshot()
	if reset.Stress != base.Stress || reset.Energy != base.Energy || reset.LastEvent != "" {
		t.Fatalf("reset state = %+v, want baseline %+v", reset, base)
	}

	// User message raises rapport & energy; knock jolts energy & eases focus; commit relieves stress.
	afterMsg := tracker.Observe(CompanionMoodEventUserMessage)
	if afterMsg.Rapport <= base.Rapport || afterMsg.Energy <= base.Energy {
		t.Fatalf("user_message did not raise rapport/energy: %+v", afterMsg)
	}
	afterKnock := tracker.Observe(CompanionMoodEventKnock)
	if afterKnock.Energy <= afterMsg.Energy || afterKnock.Focus >= afterMsg.Focus {
		t.Fatalf("knock did not jolt energy / break focus: before=%+v after=%+v", afterMsg, afterKnock)
	}
	afterDelStart := tracker.Observe(CompanionMoodEventDelegationStart)
	if afterDelStart.Focus <= afterKnock.Focus {
		t.Fatalf("delegation_start did not boost focus: %+v", afterDelStart)
	}
	afterDelDone := tracker.Observe(CompanionMoodEventDelegationDone)
	if afterDelDone.Stress >= afterDelStart.Stress || afterDelDone.Rapport <= afterDelStart.Rapport {
		t.Fatalf("delegation_done did not ease stress / raise rapport: %+v", afterDelDone)
	}

	// Repeated idle ticks drop energy into "sleepy".
	tracker.Reset()
	var sleepy CompanionMoodState
	for range 4 {
		sleepy = tracker.Observe(CompanionMoodEventIdle)
	}
	if sleepy.Label() != "sleepy" {
		t.Fatalf("idle label = %q, want sleepy (energy=%.2f)", sleepy.Label(), sleepy.Energy)
	}
	compact := sleepy.FormatCompact()
	for _, want := range []string{"mood: sleepy", "energy:", "focus:", "rapport:", "stress:", "empathy:", "last_signal: idle"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("FormatCompact missing %q:\n%s", want, compact)
		}
	}
}

func TestCompanionMoodIdleDecayAndZeroDiskTouches(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	tracker := NewCompanionMoodTracker(func() time.Time { return now }, 20*time.Minute)

	// Perturb stress and energy upward.
	tracker.Observe(CompanionMoodEventDelegationFailed)
	tracker.Observe(CompanionMoodEventDelegationFailed)
	peaked := tracker.Snapshot()
	base := CompanionMoodBaseline()
	if peaked.Stress <= base.Stress+0.25 {
		t.Fatalf("expected elevated stress before decay: %+v", peaked)
	}

	// Clock moving backwards must not corrupt or reverse-decay mood.
	now = now.Add(-5 * time.Minute)
	if skewed := tracker.Snapshot(); skewed.Stress != peaked.Stress {
		t.Fatalf("backward clock skew mutated stress: got %.4f want %.4f", skewed.Stress, peaked.Stress)
	}

	// Advance by exactly 1*tau -> delta from baseline shrinks by 1/e.
	now = time.Date(2026, 11, 28, 12, 20, 0, 0, time.UTC)
	decayed := tracker.Snapshot()
	wantStress := base.Stress + (peaked.Stress-base.Stress)*math.Exp(-1)
	if math.Abs(decayed.Stress-wantStress) > 0.002 {
		t.Fatalf("stress after 1*tau = %.4f, want %.4f", decayed.Stress, wantStress)
	}

	// Advance far into idle -> converges and snaps cleanly to baseline.
	now = now.Add(6 * time.Hour)
	settled := tracker.Snapshot()
	if settled.Energy != base.Energy || settled.Focus != base.Focus || settled.Rapport != base.Rapport || settled.Stress != base.Stress || settled.Empathy != base.Empathy {
		t.Fatalf("long idle did not settle to baseline: got %+v want %+v", settled, base)
	}
	if settled.LastEvent != "" {
		t.Fatalf("settled baseline retained stale LastEvent %q", settled.LastEvent)
	}

	// Verify daemon mood ticks touch ZERO files on disk (performance.md).
	stateDir := t.TempDir()
	d := &Daemon{dir: stateDir, home: t.TempDir()}
	d.SetCompanionMoodClock(func() time.Time { return now })
	for _, ev := range []CompanionMoodEvent{
		CompanionMoodEventUserMessage,
		CompanionMoodEventKnock,
		CompanionMoodEventToolError,
		CompanionMoodEventCommit,
		CompanionMoodEventDelegationStart,
		CompanionMoodEventDelegationDone,
		CompanionMoodEventIdle,
	} {
		d.ObserveCompanionMood(ev)
		now = now.Add(time.Minute)
		_ = d.CompanionMoodSnapshot()
	}
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatalf("ReadDir(stateDir): %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("mood ticks created unexpected files in stateDir: %v", entries)
	}
	if d.companionMood.TickCount() != 7 {
		t.Fatalf("TickCount = %d, want 7", d.companionMood.TickCount())
	}
}

func TestCompanionMoodConcurrentRaceSafety(t *testing.T) {
	var clockMu sync.Mutex
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	tracker := NewCompanionMoodTracker(func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		now = now.Add(time.Second)
		return now
	}, 10*time.Minute)

	events := []CompanionMoodEvent{
		CompanionMoodEventUserMessage,
		CompanionMoodEventKnock,
		CompanionMoodEventToolError,
		CompanionMoodEventCommit,
		CompanionMoodEventIdle,
		CompanionMoodEventDelegationStart,
		CompanionMoodEventDelegationDone,
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range 50 {
				s := tracker.Observe(events[(id+i)%len(events)])
				if s.Energy < 0 || s.Energy > 1 || s.Stress < 0 || s.Stress > 1 {
					t.Errorf("out-of-range mood state: %+v", s)
				}
				_ = tracker.Snapshot().FormatCompact()
			}
		}(g)
	}
	wg.Wait()
	if tracker.TickCount() != 400 {
		t.Fatalf("TickCount = %d, want 400", tracker.TickCount())
	}
}
