package wsdashboard

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// CompanionMoodEvent names an in-memory signal that shifts Tier-1's dynamic
// mood state machine (D11). All mood transitions stay strictly in RAM with
// zero disk writes per tick (performance.md).
type CompanionMoodEvent string

const (
	CompanionMoodEventTurnEnd          CompanionMoodEvent = "turn_end"
	CompanionMoodEventToolError        CompanionMoodEvent = "tool_error"
	CompanionMoodEventCommit           CompanionMoodEvent = "commit"
	CompanionMoodEventAsk              CompanionMoodEvent = "ask"
	CompanionMoodEventIdle             CompanionMoodEvent = "idle"
	CompanionMoodEventUserMessage      CompanionMoodEvent = "user_message"
	CompanionMoodEventKnock            CompanionMoodEvent = "knock"
	CompanionMoodEventDelegationStart  CompanionMoodEvent = "delegation_start"
	CompanionMoodEventDelegationDone   CompanionMoodEvent = "delegation_done"
	CompanionMoodEventDelegationFailed CompanionMoodEvent = "delegation_failed"

	companionMoodDefaultDecayTau = 30 * time.Minute
	companionMoodSnapEpsilon     = 0.001
)

// CompanionMoodState is the bounded [0, 1] emotional/attentional vector for
// the Tier-1 embodied companion. Reset to baseline on daemon restart (D11).
type CompanionMoodState struct {
	Energy    float64            `json:"energy"`
	Focus     float64            `json:"focus"`
	Rapport   float64            `json:"rapport"`
	Stress    float64            `json:"stress"`
	Empathy   float64            `json:"empathy"`
	LastEvent CompanionMoodEvent `json:"lastEvent,omitempty"`
	UpdatedAt time.Time          `json:"updatedAt,omitempty"`
}

// CompanionMoodBaseline returns the neutral starting mood state restored on
// restart and converged toward during idle time decay.
func CompanionMoodBaseline() CompanionMoodState {
	return CompanionMoodState{
		Energy:  0.70,
		Focus:   0.55,
		Rapport: 0.60,
		Stress:  0.15,
		Empathy: 0.60,
	}
}

// Label returns a deterministic qualitative tone summary for Tier-1 prompts.
func (s CompanionMoodState) Label() string {
	switch {
	case s.Stress >= 0.50 && s.Empathy >= 0.65:
		return "concerned-supportive"
	case s.Stress >= 0.50:
		return "tense-focused"
	case s.Energy <= 0.35:
		return "sleepy"
	case s.Focus >= 0.75:
		return "deep-focus"
	case s.Energy >= 0.78 && s.Rapport >= 0.68:
		return "upbeat-warm"
	case s.Rapport >= 0.75:
		return "warm"
	case s.Energy >= 0.78:
		return "upbeat"
	default:
		return "calm"
	}
}

// FormatCompact renders the mood state concisely for the <<<MOOD>>> envelope.
func (s CompanionMoodState) FormatCompact() string {
	var b strings.Builder
	fmt.Fprintf(&b, "mood: %s\n", s.Label())
	fmt.Fprintf(&b, "energy: %.2f | focus: %.2f | rapport: %.2f | stress: %.2f | empathy: %.2f",
		s.Energy, s.Focus, s.Rapport, s.Stress, s.Empathy)
	if s.LastEvent != "" {
		fmt.Fprintf(&b, "\nlast_signal: %s", s.LastEvent)
	}
	return b.String()
}

type companionMoodDelta struct {
	energy, focus, rapport, stress, empathy float64
}

var companionMoodDeltas = map[CompanionMoodEvent]companionMoodDelta{
	CompanionMoodEventTurnEnd:          {energy: -0.02, focus: -0.04, stress: -0.05},
	CompanionMoodEventToolError:        {energy: -0.04, focus: +0.08, stress: +0.16, empathy: +0.10},
	CompanionMoodEventCommit:           {energy: +0.10, focus: -0.03, rapport: +0.05, stress: -0.12},
	CompanionMoodEventAsk:              {energy: +0.03, focus: +0.08, empathy: +0.06},
	CompanionMoodEventIdle:             {energy: -0.18, focus: -0.12, stress: -0.08},
	CompanionMoodEventUserMessage:      {energy: +0.08, focus: +0.04, rapport: +0.08, empathy: +0.05},
	CompanionMoodEventKnock:            {energy: +0.12, focus: -0.08, rapport: +0.02, stress: +0.06},
	CompanionMoodEventDelegationStart:  {energy: -0.04, focus: +0.15, stress: +0.05},
	CompanionMoodEventDelegationDone:   {energy: +0.10, focus: -0.08, rapport: +0.06, stress: -0.14},
	CompanionMoodEventDelegationFailed: {energy: -0.08, focus: +0.04, stress: +0.18, empathy: +0.14},
}

// CompanionMoodTracker maintains Tier-1's dynamic mood state strictly in
// memory. Its zero value is ready to use with baseline defaults and time.Now.
type CompanionMoodTracker struct {
	mu          sync.Mutex
	now         func() time.Time
	decayTau    time.Duration
	initialized bool
	state       CompanionMoodState
	ticks       atomic.Int64
}

// NewCompanionMoodTracker creates a mood tracker with an optional injectable
// clock and decay time constant (zero uses companionMoodDefaultDecayTau).
func NewCompanionMoodTracker(now func() time.Time, decayTau time.Duration) *CompanionMoodTracker {
	t := &CompanionMoodTracker{
		now:      now,
		decayTau: decayTau,
	}
	t.initLocked()
	return t
}

func (t *CompanionMoodTracker) clockLocked() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

func (t *CompanionMoodTracker) tauLocked() time.Duration {
	if t.decayTau > 0 {
		return t.decayTau
	}
	return companionMoodDefaultDecayTau
}

func (t *CompanionMoodTracker) initLocked() {
	if t.initialized {
		return
	}
	t.state = CompanionMoodBaseline()
	t.state.UpdatedAt = t.clockLocked()
	t.initialized = true
}

// SetClock replaces the clock used for idle-decay calculations (test seam).
func (t *CompanionMoodTracker) SetClock(now func() time.Time) {
	t.mu.Lock()
	t.now = now
	if t.initialized {
		t.state.UpdatedAt = t.clockLocked()
	}
	t.mu.Unlock()
}

// SetDecayTau overrides the exponential idle-decay time constant (test seam).
func (t *CompanionMoodTracker) SetDecayTau(tau time.Duration) {
	t.mu.Lock()
	t.decayTau = tau
	t.mu.Unlock()
}

// Reset restores the mood tracker to baseline at the current clock time.
func (t *CompanionMoodTracker) Reset() {
	t.mu.Lock()
	t.state = CompanionMoodBaseline()
	t.state.UpdatedAt = t.clockLocked()
	t.initialized = true
	t.mu.Unlock()
}

// TickCount returns how many Observe transitions have been recorded in memory.
func (t *CompanionMoodTracker) TickCount() int64 {
	return t.ticks.Load()
}

func (t *CompanionMoodTracker) decayToLocked(now time.Time) {
	t.initLocked()
	if t.state.UpdatedAt.IsZero() {
		t.state.UpdatedAt = now
		return
	}
	elapsed := now.Sub(t.state.UpdatedAt)
	if elapsed <= 0 {
		return
	}
	factor := math.Exp(-elapsed.Seconds() / t.tauLocked().Seconds())
	base := CompanionMoodBaseline()
	t.state.Energy = decayMoodAxis(t.state.Energy, base.Energy, factor)
	t.state.Focus = decayMoodAxis(t.state.Focus, base.Focus, factor)
	t.state.Rapport = decayMoodAxis(t.state.Rapport, base.Rapport, factor)
	t.state.Stress = decayMoodAxis(t.state.Stress, base.Stress, factor)
	t.state.Empathy = decayMoodAxis(t.state.Empathy, base.Empathy, factor)
	t.state.UpdatedAt = now
	if moodAtBaseline(t.state, base) {
		t.state.LastEvent = ""
	}
}

// Snapshot decays the mood state to the current clock and returns a copy.
// Zero disk I/O.
func (t *CompanionMoodTracker) Snapshot() CompanionMoodState {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.decayToLocked(t.clockLocked())
	return t.state
}

// Observe decays the mood state to now, applies the signal's transition delta,
// clamps every axis to [0, 1], and returns the updated state. Zero disk I/O.
func (t *CompanionMoodTracker) Observe(event CompanionMoodEvent) CompanionMoodState {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clockLocked()
	t.decayToLocked(now)
	if delta, ok := companionMoodDeltas[event]; ok {
		t.state.Energy = clampMoodUnit(t.state.Energy + delta.energy)
		t.state.Focus = clampMoodUnit(t.state.Focus + delta.focus)
		t.state.Rapport = clampMoodUnit(t.state.Rapport + delta.rapport)
		t.state.Stress = clampMoodUnit(t.state.Stress + delta.stress)
		t.state.Empathy = clampMoodUnit(t.state.Empathy + delta.empathy)
	}
	if event != "" {
		t.state.LastEvent = event
	}
	t.state.UpdatedAt = now
	t.ticks.Add(1)
	return t.state
}

func decayMoodAxis(cur, base, factor float64) float64 {
	next := base + (cur-base)*factor
	if math.Abs(next-base) < companionMoodSnapEpsilon {
		return base
	}
	return clampMoodUnit(next)
}

func moodAtBaseline(s, base CompanionMoodState) bool {
	return math.Abs(s.Energy-base.Energy) < companionMoodSnapEpsilon &&
		math.Abs(s.Focus-base.Focus) < companionMoodSnapEpsilon &&
		math.Abs(s.Rapport-base.Rapport) < companionMoodSnapEpsilon &&
		math.Abs(s.Stress-base.Stress) < companionMoodSnapEpsilon &&
		math.Abs(s.Empathy-base.Empathy) < companionMoodSnapEpsilon
}

func clampMoodUnit(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func companionMoodEventForDirectorTrigger(kind string) CompanionMoodEvent {
	switch kind {
	case companionTriggerTurnEnd:
		return CompanionMoodEventTurnEnd
	case companionTriggerToolError:
		return CompanionMoodEventToolError
	case companionTriggerCommit:
		return CompanionMoodEventCommit
	case companionTriggerAsk:
		return CompanionMoodEventAsk
	case companionTriggerIdle:
		return CompanionMoodEventIdle
	default:
		return ""
	}
}

// CompanionMoodSnapshot returns Tier-1's current in-memory mood state (after
// applying idle decay up to now). Zero disk writes.
func (d *Daemon) CompanionMoodSnapshot() CompanionMoodState {
	return d.companionMood.Snapshot()
}

// ObserveCompanionMood applies an in-memory mood transition event on the
// daemon's Tier-1 mood tracker. Zero disk writes.
func (d *Daemon) ObserveCompanionMood(event CompanionMoodEvent) CompanionMoodState {
	return d.companionMood.Observe(event)
}

// SetCompanionMoodClock installs an injectable clock on the daemon's mood
// tracker for deterministic tests.
func (d *Daemon) SetCompanionMoodClock(now func() time.Time) {
	d.companionMood.SetClock(now)
}
