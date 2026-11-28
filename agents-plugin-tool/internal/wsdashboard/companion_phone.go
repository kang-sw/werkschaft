package wsdashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Companion phone lifecycle states and postures (4-state embodiment contract).
// All state stays strictly in memory (zero per-message/per-tick file I/O per
// performance.md).
const (
	CompanionStateLaptopAmbient = "laptop_ambient"
	CompanionStateLaptopSleepy  = "laptop_sleepy"
	CompanionStatePhoneActive   = "phone_active"
	CompanionStateLaptopWork    = "laptop_work"

	CompanionPostureLaptop = "laptop"
	CompanionPosturePhone  = "phone"

	CompanionPutdownNormal         = "normal"
	CompanionPutdownFacedownMerong = "facedown_merong"

	CompanionPutdownNextAmbient = "ambient"
	CompanionPutdownNextWork    = "work"

	companionPhoneMaxMessages  = 50
	companionPhoneMaxSummaries = 10
	companionPhoneMaxTextRunes = 2000
	companionPhoneBodyLimit    = 16 << 10
	companionPhoneEvent        = "companion-phone"

	companionPhoneThreadRoute       = "/api/companion/phone/thread"
	companionPhoneSendRoute         = "/api/companion/phone/send"
	companionPhoneMarkReadRoute     = "/api/companion/phone/mark-read"
	companionPhoneResetSummaryRoute = "/api/companion/phone/reset-summary"

	// companionPhoneBroadcastSession targets the active shell companion
	// regardless of which session tab is focused.
	companionPhoneBroadcastSession = "*"
)

var (
	// ErrCompanionPhoneOnDesk is returned when a caller tries to read or send on
	// the companion phone while posture is still "laptop".
	ErrCompanionPhoneOnDesk = errors.New("Smartphone is on the desk — call ws/companion.phone_pickup first to check or reply.")
	// ErrCompanionPhoneHeldByOther is returned when another owner holds the
	// active phone Embodiment Lease.
	ErrCompanionPhoneHeldByOther = errors.New("Smartphone is currently held by another session.")
)

// CompanionPhoneConfirmCard is an optional 1-Click confirmation card attached
// to a companion messenger reply (styled in Phase 1, executed in Phase 2).
type CompanionPhoneConfirmCard struct {
	ID      string   `json:"id"`
	Tool    string   `json:"tool,omitempty"`
	Title   string   `json:"title"`
	Desc    string   `json:"desc,omitempty"`
	Targets []string `json:"targets,omitempty"`
	Status  string   `json:"status,omitempty"`
	Receipt string   `json:"receipt,omitempty"`
}

// CompanionPhoneMessage is one entry in the bounded in-memory messenger ring.
type CompanionPhoneMessage struct {
	ID          string                     `json:"id"`
	Sender      string                     `json:"sender"` // "user" | "companion"
	Text        string                     `json:"text"`
	Tone        string                     `json:"tone,omitempty"`
	CreatedAt   int64                      `json:"createdAt"`
	ConfirmCard *CompanionPhoneConfirmCard `json:"confirmCard,omitempty"`
}

// CompanionPhoneRotationMeta summarizes the Tier-1 BoundedRotation memory state.
type CompanionPhoneRotationMeta struct {
	Turns      int `json:"turns"`
	MaxTurns   int `json:"maxTurns"`
	SummaryCap int `json:"summaryCap"`
}

// CompanionPhoneSnapshot is the wire shape returned by GET /api/companion/phone/thread
// and broadcast over the rail SSE stream as event: companion-phone.
type CompanionPhoneSnapshot struct {
	Seq           uint64                     `json:"seq"`
	State         string                     `json:"state"`
	Posture       string                     `json:"posture"`
	LeaseOwner    string                     `json:"leaseOwner,omitempty"`
	PhoneFaceDown bool                       `json:"phoneFaceDown,omitempty"`
	Messages      []CompanionPhoneMessage    `json:"messages"`
	UnreadClient  int                        `json:"unreadClient"`
	UnreadAgent   int                        `json:"unreadAgent"`
	Tier1Summary  []string                   `json:"tier1Summary"`
	RotationMeta  CompanionPhoneRotationMeta `json:"rotationMeta"`
}

// CompanionPhoneReadResult is returned to MCP callers of phone_pickup / phone_read.
type CompanionPhoneReadResult struct {
	State        string                  `json:"state"`
	Posture      string                  `json:"posture"`
	LeaseOwner   string                  `json:"leaseOwner,omitempty"`
	Unread       []CompanionPhoneMessage `json:"unread"`
	Recent       []CompanionPhoneMessage `json:"recent"`
	Tier1Summary []string                `json:"tier1Summary,omitempty"`
}

// CompanionPhoneStore holds the in-memory 4-state embodiment machine,
// Embodiment Lease, bounded 50-message thread ring, unread user queue, and
// 10-entry Tier-1 summary ring. Zero disk I/O.
type CompanionPhoneStore struct {
	mu            sync.Mutex
	now           func() time.Time
	seq           uint64
	msgSeq        uint64
	state         string
	posture       string
	leaseOwner    string
	phoneFaceDown bool
	messages      []CompanionPhoneMessage
	unreadAgent   []CompanionPhoneMessage
	unreadClient  int
	tier1Summary  []string
	turns         int
	payload       []byte
	wake          chan struct{}
}

func (s *CompanionPhoneStore) initLocked() {
	if s.state == "" {
		s.state = CompanionStateLaptopAmbient
	}
	if s.posture == "" {
		s.posture = CompanionPostureLaptop
	}
	if s.wake == nil {
		s.wake = make(chan struct{})
	}
}

func (s *CompanionPhoneStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *CompanionPhoneStore) snapshotLocked() CompanionPhoneSnapshot {
	s.initLocked()
	msgs := make([]CompanionPhoneMessage, len(s.messages))
	copy(msgs, s.messages)
	summaries := make([]string, len(s.tier1Summary))
	copy(summaries, s.tier1Summary)
	return CompanionPhoneSnapshot{
		Seq:           s.seq,
		State:         s.state,
		Posture:       s.posture,
		LeaseOwner:    s.leaseOwner,
		PhoneFaceDown: s.phoneFaceDown,
		Messages:      msgs,
		UnreadClient:  s.unreadClient,
		UnreadAgent:   len(s.unreadAgent),
		Tier1Summary:  summaries,
		RotationMeta: CompanionPhoneRotationMeta{
			Turns:      s.turns,
			MaxTurns:   companionPoolMaxTurns,
			SummaryCap: companionPhoneMaxSummaries,
		},
	}
}

func (s *CompanionPhoneStore) publishLocked() CompanionPhoneSnapshot {
	s.seq++
	snap := s.snapshotLocked()
	if data, err := json.Marshal(snap); err == nil {
		s.payload = data
	}
	if s.wake != nil {
		close(s.wake)
	}
	s.wake = make(chan struct{})
	return snap
}

// Snapshot returns a copy of the current phone thread and embodiment state.
func (s *CompanionPhoneStore) Snapshot() CompanionPhoneSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// Current returns the latest sequence, marshaled snapshot payload, and wake
// channel for SSE rail stream delivery.
func (s *CompanionPhoneStore) Current() (uint64, []byte, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.payload == nil {
		if data, err := json.Marshal(s.snapshotLocked()); err == nil {
			s.payload = data
		}
	}
	return s.seq, s.payload, s.wake
}

// EmbodimentLeaseActive reports whether the companion embodiment is currently
// leased (phone_active or laptop_work), pausing Tier-1 ambient director ticks.
func (s *CompanionPhoneStore) EmbodimentLeaseActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	return s.state == CompanionStatePhoneActive || s.state == CompanionStateLaptopWork || s.leaseOwner != ""
}

// PostureFor reports the effective posture ("laptop" or "phone") for owner.
// When owner is empty or matches the active lease owner while phone_active, it
// reports "phone"; otherwise "laptop".
func (s *CompanionPhoneStore) PostureFor(owner string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.posture != CompanionPosturePhone {
		return CompanionPostureLaptop
	}
	owner = strings.TrimSpace(owner)
	if owner == "" || s.leaseOwner == "" || s.leaseOwner == owner {
		return CompanionPosturePhone
	}
	return CompanionPostureLaptop
}

// UnreadAgentCount returns the number of unread user phone messages waiting
// for pickup/read. O(1) memory-only read.
func (s *CompanionPhoneStore) UnreadAgentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.unreadAgent)
}

func boundCompanionPhoneText(text string) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= companionPhoneMaxTextRunes {
		return text
	}
	r := []rune(text)
	return string(r[:companionPhoneMaxTextRunes])
}

func appendBoundedMessage(ring []CompanionPhoneMessage, msg CompanionPhoneMessage, capLimit int) []CompanionPhoneMessage {
	ring = append(ring, msg)
	if len(ring) > capLimit {
		ring = append([]CompanionPhoneMessage(nil), ring[len(ring)-capLimit:]...)
	}
	return ring
}

func appendBoundedSummary(ring []string, line string, capLimit int) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ring
	}
	ring = append(ring, line)
	if len(ring) > capLimit {
		ring = append([]string(nil), ring[len(ring)-capLimit:]...)
	}
	return ring
}

// EnqueueUserMessage records a user-authored messenger message, queues it in
// the unread agent inbox, and wakes SSE listeners.
func (s *CompanionPhoneStore) EnqueueUserMessage(text string) (CompanionPhoneMessage, CompanionPhoneSnapshot, error) {
	text = boundCompanionPhoneText(text)
	if text == "" {
		return CompanionPhoneMessage{}, CompanionPhoneSnapshot{}, errors.New("message text is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	s.msgSeq++
	msg := CompanionPhoneMessage{
		ID:        "cm-" + strconv.FormatUint(s.msgSeq, 10),
		Sender:    "user",
		Text:      text,
		CreatedAt: s.clock().UnixMilli(),
	}
	s.messages = appendBoundedMessage(s.messages, msg, companionPhoneMaxMessages)
	s.unreadAgent = appendBoundedMessage(s.unreadAgent, msg, companionPhoneMaxMessages)
	// A buzzing notification wakes a sleepy companion and turns attention back
	// to the phone screen.
	if s.state == CompanionStateLaptopSleepy {
		s.state = CompanionStateLaptopAmbient
	}
	s.phoneFaceDown = false
	snap := s.publishLocked()
	return msg, snap, nil
}

// PhonePickup transitions posture to "phone" / state to "phone_active", locks
// the Embodiment Lease to owner, and drains unread user messages.
func (s *CompanionPhoneStore) PhonePickup(owner string) (CompanionPhoneReadResult, CompanionPhoneSnapshot, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		owner = "lead"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.posture == CompanionPosturePhone && s.leaseOwner != "" && s.leaseOwner != owner {
		return CompanionPhoneReadResult{}, CompanionPhoneSnapshot{}, ErrCompanionPhoneHeldByOther
	}
	s.posture = CompanionPosturePhone
	s.state = CompanionStatePhoneActive
	s.leaseOwner = owner
	s.phoneFaceDown = false

	unread := make([]CompanionPhoneMessage, len(s.unreadAgent))
	copy(unread, s.unreadAgent)
	s.unreadAgent = nil

	snap := s.publishLocked()
	return CompanionPhoneReadResult{
		State:        snap.State,
		Posture:      snap.Posture,
		LeaseOwner:   snap.LeaseOwner,
		Unread:       unread,
		Recent:       recentPhoneMessages(snap.Messages, 10),
		Tier1Summary: snap.Tier1Summary,
	}, snap, nil
}

// PhoneRead drains newly arrived unread messages while posture is "phone".
func (s *CompanionPhoneStore) PhoneRead(owner string) (CompanionPhoneReadResult, CompanionPhoneSnapshot, error) {
	owner = strings.TrimSpace(owner)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.posture != CompanionPosturePhone {
		return CompanionPhoneReadResult{}, CompanionPhoneSnapshot{}, ErrCompanionPhoneOnDesk
	}
	if owner != "" && s.leaseOwner != "" && s.leaseOwner != owner {
		return CompanionPhoneReadResult{}, CompanionPhoneSnapshot{}, ErrCompanionPhoneHeldByOther
	}
	unread := make([]CompanionPhoneMessage, len(s.unreadAgent))
	copy(unread, s.unreadAgent)
	s.unreadAgent = nil

	var snap CompanionPhoneSnapshot
	if len(unread) > 0 {
		snap = s.publishLocked()
	} else {
		snap = s.snapshotLocked()
	}
	return CompanionPhoneReadResult{
		State:        snap.State,
		Posture:      snap.Posture,
		LeaseOwner:   snap.LeaseOwner,
		Unread:       unread,
		Recent:       recentPhoneMessages(snap.Messages, 10),
		Tier1Summary: snap.Tier1Summary,
	}, snap, nil
}

// PhoneSend appends a companion reply while posture is "phone", increments
// unreadClient, and notifies SSE listeners.
func (s *CompanionPhoneStore) PhoneSend(owner, text, tone string, confirmCard *CompanionPhoneConfirmCard) (CompanionPhoneMessage, CompanionPhoneSnapshot, error) {
	owner = strings.TrimSpace(owner)
	text = boundCompanionPhoneText(text)
	tone = strings.TrimSpace(tone)
	if text == "" {
		return CompanionPhoneMessage{}, CompanionPhoneSnapshot{}, errors.New("reply text is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.posture != CompanionPosturePhone {
		return CompanionPhoneMessage{}, CompanionPhoneSnapshot{}, ErrCompanionPhoneOnDesk
	}
	if owner != "" && s.leaseOwner != "" && s.leaseOwner != owner {
		return CompanionPhoneMessage{}, CompanionPhoneSnapshot{}, ErrCompanionPhoneHeldByOther
	}
	s.msgSeq++
	msg := CompanionPhoneMessage{
		ID:          "cm-" + strconv.FormatUint(s.msgSeq, 10),
		Sender:      "companion",
		Text:        text,
		Tone:        tone,
		CreatedAt:   s.clock().UnixMilli(),
		ConfirmCard: confirmCard,
	}
	s.messages = appendBoundedMessage(s.messages, msg, companionPhoneMaxMessages)
	s.unreadClient++
	s.turns++
	snap := s.publishLocked()
	return msg, snap, nil
}

// PhonePutdown returns posture to "laptop" with style ("normal"|"facedown_merong")
// and next ("ambient"|"work"). Already-laptop calls are idempotent for turn-end
// failsafe cleanup.
func (s *CompanionPhoneStore) PhonePutdown(owner, style, next string) (CompanionPhoneSnapshot, bool, error) {
	owner = strings.TrimSpace(owner)
	style = strings.TrimSpace(style)
	if style == "" {
		style = CompanionPutdownNormal
	}
	if style != CompanionPutdownNormal && style != CompanionPutdownFacedownMerong {
		return CompanionPhoneSnapshot{}, false, fmt.Errorf("invalid putdown style %q (want %q or %q)", style, CompanionPutdownNormal, CompanionPutdownFacedownMerong)
	}
	next = strings.TrimSpace(next)
	if next == "" {
		next = CompanionPutdownNextAmbient
	}
	if next != CompanionPutdownNextAmbient && next != CompanionPutdownNextWork {
		return CompanionPhoneSnapshot{}, false, fmt.Errorf("invalid putdown next %q (want %q or %q)", next, CompanionPutdownNextAmbient, CompanionPutdownNextWork)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.posture != CompanionPosturePhone {
		// Idempotent no-op when already on laptop, except releasing a held
		// laptop_work lease back to laptop_ambient when requested by owner.
		if s.state == CompanionStateLaptopWork && next == CompanionPutdownNextAmbient && (owner == "" || s.leaseOwner == "" || s.leaseOwner == owner) {
			s.leaseOwner = ""
			s.state = CompanionStateLaptopAmbient
			return s.publishLocked(), true, nil
		}
		return s.snapshotLocked(), false, nil
	}
	if owner != "" && s.leaseOwner != "" && s.leaseOwner != owner {
		return CompanionPhoneSnapshot{}, false, ErrCompanionPhoneHeldByOther
	}

	s.posture = CompanionPostureLaptop
	s.phoneFaceDown = style == CompanionPutdownFacedownMerong
	if next == CompanionPutdownNextWork {
		if owner != "" {
			s.leaseOwner = owner
		}
		s.state = CompanionStateLaptopWork
	} else {
		s.leaseOwner = ""
		s.state = CompanionStateLaptopAmbient
		if line := summarizeLatestPhoneExchange(s.messages, style); line != "" {
			s.tier1Summary = appendBoundedSummary(s.tier1Summary, line, companionPhoneMaxSummaries)
		}
	}
	return s.publishLocked(), true, nil
}

// ReleaseOnTurnEnd is the turn-end failsafe hook: if owner still holds
// phone_active, it performs an automatic normal putdown to laptop_ambient; if
// owner holds laptop_work, it releases the lease to laptop_ambient.
func (s *CompanionPhoneStore) ReleaseOnTurnEnd(owner string) (CompanionPhoneSnapshot, bool) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return s.Snapshot(), false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.leaseOwner != owner {
		return s.snapshotLocked(), false
	}
	if s.posture == CompanionPosturePhone || s.state == CompanionStatePhoneActive {
		s.posture = CompanionPostureLaptop
		s.phoneFaceDown = false
		s.leaseOwner = ""
		s.state = CompanionStateLaptopAmbient
		if line := summarizeLatestPhoneExchange(s.messages, CompanionPutdownNormal); line != "" {
			s.tier1Summary = appendBoundedSummary(s.tier1Summary, line, companionPhoneMaxSummaries)
		}
		return s.publishLocked(), true
	}
	if s.state == CompanionStateLaptopWork {
		s.leaseOwner = ""
		s.state = CompanionStateLaptopAmbient
		return s.publishLocked(), true
	}
	return s.snapshotLocked(), false
}

// SetSleepy toggles between laptop_ambient and laptop_sleepy when the
// Embodiment Lease is free.
func (s *CompanionPhoneStore) SetSleepy(sleepy bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.leaseOwner != "" || s.state == CompanionStatePhoneActive || s.state == CompanionStateLaptopWork {
		return false
	}
	want := CompanionStateLaptopAmbient
	if sleepy {
		want = CompanionStateLaptopSleepy
	}
	if s.state == want {
		return false
	}
	s.state = want
	s.publishLocked()
	return true
}

// MarkClientRead zeroes unreadClient when the user opens or views the popup.
func (s *CompanionPhoneStore) MarkClientRead() CompanionPhoneSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.unreadClient == 0 {
		return s.snapshotLocked()
	}
	s.unreadClient = 0
	return s.publishLocked()
}

// ResetSummary clears the Tier-1 backfeed summary ring and resets the rotation counter.
func (s *CompanionPhoneStore) ResetSummary() CompanionPhoneSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	s.tier1Summary = nil
	s.turns = 0
	return s.publishLocked()
}

func recentPhoneMessages(msgs []CompanionPhoneMessage, limit int) []CompanionPhoneMessage {
	if len(msgs) <= limit {
		out := make([]CompanionPhoneMessage, len(msgs))
		copy(out, msgs)
		return out
	}
	out := make([]CompanionPhoneMessage, limit)
	copy(out, msgs[len(msgs)-limit:])
	return out
}

func summarizeLatestPhoneExchange(msgs []CompanionPhoneMessage, style string) string {
	if len(msgs) == 0 {
		return ""
	}
	var lastUser, lastCompanion string
	for i := len(msgs) - 1; i >= 0; i-- {
		if lastCompanion == "" && msgs[i].Sender == "companion" {
			lastCompanion = companionCut(msgs[i].Text, 64)
		} else if lastUser == "" && msgs[i].Sender == "user" {
			lastUser = companionCut(msgs[i].Text, 64)
		}
		if lastUser != "" && lastCompanion != "" {
			break
		}
	}
	tag := ""
	if style == CompanionPutdownFacedownMerong {
		tag = " [merong]"
	}
	switch {
	case lastUser != "" && lastCompanion != "":
		return fmt.Sprintf("User: %s -> Companion: %s%s", lastUser, lastCompanion, tag)
	case lastCompanion != "":
		return fmt.Sprintf("Companion: %s%s", lastCompanion, tag)
	case lastUser != "":
		return fmt.Sprintf("User: %s%s", lastUser, tag)
	default:
		return ""
	}
}

func companionPhoneToneToExpression(tone string) string {
	switch strings.ToLower(strings.TrimSpace(tone)) {
	case "laugh", "happy", "smile", "cheer":
		return "happy"
	case "merong", "cheeky", "playful", "wink":
		return "merong"
	case "annoyed", "pout", "grumpy":
		return "annoyed"
	case "startled", "surprised", "oo":
		return "startled"
	case "sad", "cry", "sorry":
		return "cry"
	case "focused", "serious", "work":
		return "focused"
	case "sleepy", "tired":
		return "idle_doze"
	default:
		return ""
	}
}

// --- Daemon bridge methods (also satisfy internal/mcp CompanionHost) ---------

// CompanionPosture reports the current posture ("laptop" or "phone") for owner.
func (d *Daemon) CompanionPosture(owner string) string {
	return d.companionPhone.PostureFor(owner)
}

// CompanionUnreadPhoneCount reports the number of unread user phone messages.
func (d *Daemon) CompanionUnreadPhoneCount() int {
	return d.companionPhone.UnreadAgentCount()
}

// CompanionSpeak publishes a quip/speech bubble command to the companion stage.
func (d *Daemon) CompanionSpeak(sessionID, text string, ttlMS int) bool {
	if sessionID == "" {
		sessionID = companionPhoneBroadcastSession
	}
	if ttlMS <= 0 {
		ttlMS = companionDefaultTTL
	}
	return d.PublishCompanionCommand(CompanionCommand{
		SessionID: sessionID,
		Quip:      text,
		TTLms:     ttlMS,
	})
}

// CompanionFace publishes an expression command to the companion stage.
func (d *Daemon) CompanionFace(sessionID, expression string, ttlMS int) bool {
	if sessionID == "" {
		sessionID = companionPhoneBroadcastSession
	}
	if ttlMS <= 0 {
		ttlMS = companionDefaultTTL
	}
	return d.PublishCompanionCommand(CompanionCommand{
		SessionID:  sessionID,
		Expression: strings.TrimSpace(expression),
		TTLms:      ttlMS,
	})
}

// CompanionMotion publishes a gesture/prop command to the companion stage.
func (d *Daemon) CompanionMotion(sessionID, gesture, prop string, ttlMS int) bool {
	if sessionID == "" {
		sessionID = companionPhoneBroadcastSession
	}
	if ttlMS <= 0 {
		ttlMS = companionDefaultTTL
	}
	return d.PublishCompanionCommand(CompanionCommand{
		SessionID: sessionID,
		Gesture:   strings.TrimSpace(gesture),
		Prop:      strings.TrimSpace(prop),
		TTLms:     ttlMS,
	})
}

// CompanionPhonePickup picks up the desk smartphone, locks the Embodiment Lease,
// drains unread messages, and broadcasts both companion-phone and companion-command.
func (d *Daemon) CompanionPhonePickup(owner string) (CompanionPhoneReadResult, error) {
	res, _, err := d.companionPhone.PhonePickup(owner)
	if err != nil {
		return CompanionPhoneReadResult{}, err
	}
	d.PublishCompanionCommand(CompanionCommand{
		SessionID: companionPhoneBroadcastSession,
		Gesture:   "phone_pickup",
		Prop:      "smartphone",
		TTLms:     CompanionCommandMaxTTL,
	})
	return res, nil
}

// CompanionPhoneRead reads newly arrived unread messages while holding the phone.
func (d *Daemon) CompanionPhoneRead(owner string) (CompanionPhoneReadResult, error) {
	res, _, err := d.companionPhone.PhoneRead(owner)
	if err != nil {
		return CompanionPhoneReadResult{}, err
	}
	return res, nil
}

// CompanionPhoneSend posts a companion reply into the messenger thread and
// triggers an optional facial expression reaction on the VRM stage.
func (d *Daemon) CompanionPhoneSend(owner, text, tone string) (CompanionPhoneMessage, error) {
	msg, _, err := d.companionPhone.PhoneSend(owner, text, tone, nil)
	if err != nil {
		return CompanionPhoneMessage{}, err
	}
	expr := companionPhoneToneToExpression(tone)
	cmd := CompanionCommand{
		SessionID:  companionPhoneBroadcastSession,
		Gesture:    "phone_typing",
		Expression: expr,
		Prop:       "smartphone",
		TTLms:      companionDefaultTTL,
	}
	d.PublishCompanionCommand(cmd)
	return msg, nil
}

// CompanionPhonePutdown puts the smartphone back on the desk ("normal" or
// "facedown_merong") and transitions to "ambient" (free lease) or "work" (keep lease).
func (d *Daemon) CompanionPhonePutdown(owner, style, next string) (CompanionPhoneSnapshot, error) {
	snap, changed, err := d.companionPhone.PhonePutdown(owner, style, next)
	if err != nil {
		return CompanionPhoneSnapshot{}, err
	}
	if changed {
		gesture := "phone_putdown"
		expr := "idle"
		if style == CompanionPutdownFacedownMerong {
			gesture = "phone_merong_facedown"
			expr = "merong"
		}
		d.PublishCompanionCommand(CompanionCommand{
			SessionID:  companionPhoneBroadcastSession,
			Gesture:    gesture,
			Expression: expr,
			Prop:       "smartphone",
			TTLms:      4500,
		})
	}
	return snap, nil
}

// CompanionReleaseOnTurnEnd releases any held Embodiment Lease at turn end.
func (d *Daemon) CompanionReleaseOnTurnEnd(owner string) bool {
	_, changed := d.companionPhone.ReleaseOnTurnEnd(owner)
	if changed {
		d.PublishCompanionCommand(CompanionCommand{
			SessionID: companionPhoneBroadcastSession,
			Gesture:   "phone_putdown",
			TTLms:     3000,
		})
	}
	return changed
}

// --- HTTP routes -------------------------------------------------------------

func (d *Daemon) registerCompanionPhoneRoutes(mux *http.ServeMux) {
	mux.HandleFunc(companionPhoneThreadRoute, d.handleCompanionPhoneThread)
	mux.HandleFunc(companionPhoneSendRoute, d.handleCompanionPhoneSend)
	mux.HandleFunc(companionPhoneMarkReadRoute, d.handleCompanionPhoneMarkRead)
	mux.HandleFunc(companionPhoneResetSummaryRoute, d.handleCompanionPhoneResetSummary)
}

func (d *Daemon) handleCompanionPhoneThread(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if !companionReadMethod(w, r) {
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		return
	}
	writeJSON(w, http.StatusOK, d.companionPhone.Snapshot())
}

func (d *Daemon) handleCompanionPhoneSend(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	local, own := TrustedWrite(r)
	if !local {
		http.NotFound(w, r)
		return
	}
	if !own {
		http.Error(w, "same-origin request required", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, companionPhoneBodyLimit)
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid companion phone payload", http.StatusBadRequest)
		return
	}
	msg, snap, err := d.companionPhone.EnqueueUserMessage(body.Text)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	d.PublishCompanionCommand(CompanionCommand{
		SessionID: companionPhoneBroadcastSession,
		Gesture:   "phone_buzz",
		Prop:      "smartphone",
		TTLms:     4000,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"message": msg,
		"thread":  snap,
	})
}

func (d *Daemon) handleCompanionPhoneMarkRead(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	local, own := TrustedWrite(r)
	if !local {
		http.NotFound(w, r)
		return
	}
	if !own {
		http.Error(w, "same-origin request required", http.StatusForbidden)
		return
	}
	writeJSON(w, http.StatusOK, d.companionPhone.MarkClientRead())
}

func (d *Daemon) handleCompanionPhoneResetSummary(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	local, own := TrustedWrite(r)
	if !local {
		http.NotFound(w, r)
		return
	}
	if !own {
		http.Error(w, "same-origin request required", http.StatusForbidden)
		return
	}
	writeJSON(w, http.StatusOK, d.companionPhone.ResetSummary())
}
