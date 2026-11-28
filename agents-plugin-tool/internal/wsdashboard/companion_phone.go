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

	"github.com/kang-sw/devenv/internal/wsconfig"
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

	// CompanionLeaseIdleTimeout is the sliding idle deadline for a held
	// Embodiment Lease when no Tier-2 delegation is in flight ((1739)).
	CompanionLeaseIdleTimeout = 5 * time.Minute
	// CompanionLeaseDelegationTimeout is the sliding in-flight deadline while at
	// least one Tier-2 delegation is active or queued ((1739), (1748)).
	CompanionLeaseDelegationTimeout = 30 * time.Minute
	// CompanionWakeMaxRetries bounds consecutive Tier-1 wake/turn failures
	// before the messenger enters the unavailable state ((1749)).
	CompanionWakeMaxRetries = 3

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
	// ErrCompanionPhoneHeldByOther is returned when another live owner holds the
	// active Embodiment Lease.
	ErrCompanionPhoneHeldByOther = errors.New("lease_held: Smartphone is currently held by another session.")
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

// CompanionPendingApproval describes an active companion-work 1-click approval
// banner request rendered as a daemon-owned pause status echo in the Tier-1
// messenger thread ((1728)=a, (1750)).
type CompanionPendingApproval struct {
	RequestID string `json:"requestId"`
	Tool      string `json:"tool"`
	Summary   string `json:"summary,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// CompanionPhoneSnapshot is the wire shape returned by GET /api/companion/phone/thread
// and broadcast over the rail SSE stream as event: companion-phone.
type CompanionPhoneSnapshot struct {
	Seq uint64 `json:"seq"`
	// Epoch identifies this store instance (its first-use time in Unix ms):
	// Seq restarts with a new daemon, so a client accepts any snapshot whose
	// epoch differs and otherwise drops one whose seq went backwards.
	Epoch              int64                      `json:"epoch,omitempty"`
	MessengerOpen      bool                       `json:"messengerOpen,omitempty"`
	State              string                     `json:"state"`
	Posture            string                     `json:"posture"`
	LeaseOwner         string                     `json:"leaseOwner,omitempty"`
	PhoneFaceDown      bool                       `json:"phoneFaceDown,omitempty"`
	Typing             bool                       `json:"typing,omitempty"`
	DelegationInFlight bool                       `json:"delegationInFlight,omitempty"`
	Unavailable        bool                       `json:"unavailable,omitempty"`
	PendingApproval    *CompanionPendingApproval  `json:"pendingApproval,omitempty"`
	Messages           []CompanionPhoneMessage    `json:"messages"`
	UnreadClient       int                        `json:"unreadClient"`
	UnreadAgent        int                        `json:"unreadAgent"`
	Tier1Summary       []string                   `json:"tier1Summary"`
	RotationMeta       CompanionPhoneRotationMeta `json:"rotationMeta"`
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
	mu                 sync.Mutex
	now                func() time.Time
	holderAlive        func(holder string) bool
	onUserMessage      func(CompanionPhoneMessage)
	seq                uint64
	msgSeq             uint64
	state              string
	posture            string
	leaseOwner         string
	deadline           time.Time
	delegationInFlight bool
	wakeAcquire        bool
	typing             bool
	unavailable        bool
	wakeFailures       int
	phoneFaceDown      bool
	pendingApprovals   []CompanionPendingApproval
	messages           []CompanionPhoneMessage
	unreadAgent        []CompanionPhoneMessage
	unreadClient       int
	tier1Summary       []string
	turns              int
	payload            []byte
	wake               chan struct{}
	epoch              int64
	// presence maps a Messenger client id to its last open/heartbeat time
	// (companion_phone_presence.go); messengerOpen caches len(presence) > 0.
	presence      map[string]time.Time
	messengerOpen bool
	// persist receives each history delta under s.mu; it must never block
	// (companion_messenger_ring.go queues to its own writer goroutine).
	persist func(companionMessengerRecord)
}

func (s *CompanionPhoneStore) initLocked() {
	if s.epoch == 0 {
		s.epoch = time.Now().UnixMilli()
	}
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
	var pending *CompanionPendingApproval
	if len(s.pendingApprovals) > 0 {
		p := s.pendingApprovals[0]
		pending = &p
	}
	return CompanionPhoneSnapshot{
		Seq:                s.seq,
		Epoch:              s.epoch,
		MessengerOpen:      s.messengerOpen,
		State:              s.state,
		Posture:            s.posture,
		LeaseOwner:         s.leaseOwner,
		PhoneFaceDown:      s.phoneFaceDown,
		Typing:             s.typing,
		DelegationInFlight: s.delegationInFlight,
		Unavailable:        s.unavailable,
		PendingApproval:    pending,
		Messages:           msgs,
		UnreadClient:       s.unreadClient,
		UnreadAgent:        len(s.unreadAgent),
		Tier1Summary:       summaries,
		RotationMeta: CompanionPhoneRotationMeta{
			Turns:      s.turns,
			MaxTurns:   companionPoolMaxTurns,
			SummaryCap: companionPhoneMaxSummaries,
		},
	}
}

func (s *CompanionPhoneStore) renewDeadlineLocked() {
	if s.delegationInFlight {
		s.deadline = s.clock().Add(CompanionLeaseDelegationTimeout)
	} else {
		s.deadline = s.clock().Add(CompanionLeaseIdleTimeout)
	}
}

// holderIsLiveLocked reports whether s.leaseOwner currently names a live
// session. When holderAlive is configured and reports false, the stale holder
// is cleared so a relaunched Tier-1 with a new key can acquire immediately
// ((1739)).
func (s *CompanionPhoneStore) holderIsLiveLocked() bool {
	if s.leaseOwner == "" {
		return false
	}
	if s.holderAlive != nil && !s.holderAlive(s.leaseOwner) {
		s.leaseOwner = ""
		s.wakeAcquire = false
		return false
	}
	return true
}

// SetHolderAlive installs an optional live-holder predicate checked during CAS
// acquire. Nil treats any non-empty holder as live.
func (s *CompanionPhoneStore) SetHolderAlive(fn func(holder string) bool) {
	s.mu.Lock()
	s.holderAlive = fn
	s.mu.Unlock()
}

// SetOnUserMessage installs an optional in-memory hook invoked (after releasing
// s.mu) whenever a user messenger message is enqueued.
func (s *CompanionPhoneStore) SetOnUserMessage(fn func(CompanionPhoneMessage)) {
	s.mu.Lock()
	s.onUserMessage = fn
	s.mu.Unlock()
}

// LatestSummary returns the most recent 1-line messenger exchange summary for
// the ambient director backfeed ((1730)=a), or "" when none exists.
func (s *CompanionPhoneStore) LatestSummary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tier1Summary) == 0 {
		return ""
	}
	return s.tier1Summary[len(s.tier1Summary)-1]
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
// leased (phone_active, laptop_work, wake-acquired, or delegation in flight),
// pausing Tier-1 ambient director ticks.
func (s *CompanionPhoneStore) EmbodimentLeaseActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	hadOwner := s.leaseOwner != ""
	live := s.holderIsLiveLocked()
	if hadOwner && !live {
		s.publishLocked()
	}
	return s.state == CompanionStatePhoneActive || s.state == CompanionStateLaptopWork || s.delegationInFlight || live
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

// OldestUnreadID returns the ID of the oldest unread user phone message, or ""
// when the unread queue is empty. Used by companionRuntime to detect a Tier-1
// turn that exited without making progress on unread mail ((1749)).
func (s *CompanionPhoneStore) OldestUnreadID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.unreadAgent) == 0 {
		return ""
	}
	return s.unreadAgent[0].ID
}

// LatestUnreadHint returns a compact one-line preview of the newest unread user
// message for recall-provider queries (never echoed verbatim before phone_pickup).
func (s *CompanionPhoneStore) LatestUnreadHint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.unreadAgent) == 0 {
		return ""
	}
	return companionCut(s.unreadAgent[len(s.unreadAgent)-1].Text, 160)
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
	s.initLocked()
	s.msgSeq++
	msg := CompanionPhoneMessage{
		ID:        "cm-" + strconv.FormatUint(s.msgSeq, 10),
		Sender:    "user",
		Text:      text,
		CreatedAt: s.clock().UnixMilli(),
	}
	s.messages = appendBoundedMessage(s.messages, msg, companionPhoneMaxMessages)
	s.persistMessageLocked(msg)
	s.unreadAgent = appendBoundedMessage(s.unreadAgent, msg, companionPhoneMaxMessages)
	// A buzzing notification wakes a sleepy companion, clears unavailable/retry
	// state ((1749)), and turns attention back to the phone screen.
	if s.state == CompanionStateLaptopSleepy {
		s.state = CompanionStateLaptopAmbient
	}
	s.phoneFaceDown = false
	s.wakeFailures = 0
	s.unavailable = false
	snap := s.publishLocked()
	cb := s.onUserMessage
	s.mu.Unlock()
	if cb != nil {
		cb(msg)
	}
	return msg, snap, nil
}

// ConsumeUnreadForAct drains and returns all currently unread user messages for
// an interrupting ws/companion.act wake, publishing the updated snapshot
// (UnreadAgent=0) so content-free wake nudges and turn-end release checks see
// an empty inbox and never re-wake Tier-1 for messages already delivered (D7, D11).
func (s *CompanionPhoneStore) ConsumeUnreadForAct() []CompanionPhoneMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if len(s.unreadAgent) == 0 {
		return nil
	}
	unread := make([]CompanionPhoneMessage, len(s.unreadAgent))
	copy(unread, s.unreadAgent)
	s.unreadAgent = nil
	s.wakeFailures = 0
	s.unavailable = false
	s.publishLocked()
	return unread
}

// AcquireOnWake acquires the Embodiment Lease in laptop posture for a Tier-1
// wake delivery (user message or delegation completion) without switching visual
// state into laptop_work until a delegation is in flight or Tier-1 invokes a
// laptop-posture work tool ((1739)).
func (s *CompanionPhoneStore) AcquireOnWake(owner string) (CompanionPhoneSnapshot, bool) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return s.Snapshot(), false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.leaseOwner != "" && s.leaseOwner != owner && s.holderIsLiveLocked() {
		return s.snapshotLocked(), false
	}
	if s.posture == CompanionPosturePhone && s.leaseOwner == owner {
		s.renewDeadlineLocked()
		return s.snapshotLocked(), true
	}
	changed := s.leaseOwner != owner || s.posture != CompanionPostureLaptop || s.state == CompanionStateLaptopSleepy
	s.leaseOwner = owner
	s.posture = CompanionPostureLaptop
	if s.delegationInFlight {
		if s.state != CompanionStateLaptopWork {
			s.state = CompanionStateLaptopWork
			changed = true
		}
		s.wakeAcquire = false
	} else if s.state != CompanionStateLaptopWork {
		s.state = CompanionStateLaptopAmbient
		s.wakeAcquire = true
	}
	s.renewDeadlineLocked()
	if changed {
		return s.publishLocked(), true
	}
	return s.snapshotLocked(), true
}

// SetDelegationInFlight marks whether at least one Tier-2 delegation is active
// or queued ((1739), (1748)). While true, laptop posture shows laptop_work and
// uses the longer delegation deadline.
func (s *CompanionPhoneStore) SetDelegationInFlight(inFlight bool) CompanionPhoneSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	changed := s.delegationInFlight != inFlight
	s.delegationInFlight = inFlight
	if inFlight {
		s.wakeAcquire = false
		if s.posture == CompanionPostureLaptop && s.state != CompanionStateLaptopWork {
			s.state = CompanionStateLaptopWork
			changed = true
		}
		s.renewDeadlineLocked()
	} else {
		if s.posture == CompanionPostureLaptop && s.state == CompanionStateLaptopWork && !s.holderIsLiveLocked() {
			s.state = CompanionStateLaptopAmbient
			s.deadline = time.Time{}
			changed = true
		} else if s.holderIsLiveLocked() || s.posture == CompanionPosturePhone {
			s.renewDeadlineLocked()
		} else {
			s.deadline = time.Time{}
		}
	}
	if changed {
		return s.publishLocked()
	}
	return s.snapshotLocked()
}

// RenewLease refreshes the sliding deadline for owner (5m idle or 30m when a
// delegation is in flight).
func (s *CompanionPhoneStore) RenewLease(owner string) bool {
	owner = strings.TrimSpace(owner)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if owner != "" && s.leaseOwner != owner {
		return false
	}
	if s.leaseOwner == "" && !s.delegationInFlight {
		return false
	}
	s.renewDeadlineLocked()
	return true
}

// RenewDelegationLease refreshes the sliding in-flight delegation deadline on
// Tier-2 or spawned engineering-agent turn activity ((1739), (1748)).
func (s *CompanionPhoneStore) RenewDelegationLease() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if !s.delegationInFlight {
		return false
	}
	s.deadline = s.clock().Add(CompanionLeaseDelegationTimeout)
	return true
}

// SetTyping updates the phone_active typing visual state on the companion-phone
// SSE snapshot ((1743)).
func (s *CompanionPhoneStore) SetTyping(owner string, typing bool) bool {
	owner = strings.TrimSpace(owner)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.posture != CompanionPosturePhone {
		typing = false
	}
	if owner != "" && s.leaseOwner != "" && s.leaseOwner != owner {
		return false
	}
	if s.typing == typing {
		return false
	}
	s.typing = typing
	s.publishLocked()
	return true
}

// BeginApprovalPause records a pending companion-work 1-click confirmation
// request so the Tier-1 messenger renders a daemon-owned status echo while
// paused ((1728)=a, (1750)). Purely in-memory (zero disk I/O).
func (s *CompanionPhoneStore) BeginApprovalPause(requestID, tool, summary string) CompanionPhoneSnapshot {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return s.Snapshot()
	}
	tool = strings.TrimSpace(tool)
	summary = clampString(strings.TrimSpace(summary), 240)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	for _, existing := range s.pendingApprovals {
		if existing.RequestID == requestID {
			return s.snapshotLocked()
		}
	}
	s.pendingApprovals = append(s.pendingApprovals, CompanionPendingApproval{
		RequestID: requestID,
		Tool:      tool,
		Summary:   summary,
		CreatedAt: s.clock().UnixMilli(),
	})
	return s.publishLocked()
}

// EndApprovalPause clears the matching pending companion-work approval pause
// echo once the 1-click confirmation banner settles (approve, deny, dismiss,
// timeout, or tab disconnect). Purely in-memory (zero disk I/O).
func (s *CompanionPhoneStore) EndApprovalPause(requestID string) CompanionPhoneSnapshot {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return s.Snapshot()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	idx := -1
	for i, existing := range s.pendingApprovals {
		if existing.RequestID == requestID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return s.snapshotLocked()
	}
	s.pendingApprovals = append(s.pendingApprovals[:idx], s.pendingApprovals[idx+1:]...)
	return s.publishLocked()
}

// RecordWakeFailure increments consecutive Tier-1 wake/turn failure count and
// marks the messenger unavailable once CompanionWakeMaxRetries is reached
// ((1749)).
func (s *CompanionPhoneStore) RecordWakeFailure() (CompanionPhoneSnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	s.wakeFailures++
	if s.wakeFailures >= CompanionWakeMaxRetries {
		if !s.unavailable {
			s.unavailable = true
			return s.publishLocked(), true
		}
		return s.snapshotLocked(), true
	}
	return s.snapshotLocked(), false
}

// ResetWakeFailures clears consecutive wake failure count and unavailable state.
func (s *CompanionPhoneStore) ResetWakeFailures() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	wasUnavailable := s.unavailable
	s.wakeFailures = 0
	s.unavailable = false
	if wasUnavailable {
		s.publishLocked()
	}
}

// PhonePickup transitions posture to "phone" / state to "phone_active", atomically
// acquires the Embodiment Lease via CAS (failing with ErrCompanionPhoneHeldByOther
// only while a different holder's session is live), and drains unread user messages.
func (s *CompanionPhoneStore) PhonePickup(owner string) (CompanionPhoneReadResult, CompanionPhoneSnapshot, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		owner = "lead"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.leaseOwner != "" && s.leaseOwner != owner && s.holderIsLiveLocked() {
		return CompanionPhoneReadResult{}, CompanionPhoneSnapshot{}, ErrCompanionPhoneHeldByOther
	}
	s.posture = CompanionPosturePhone
	s.state = CompanionStatePhoneActive
	s.leaseOwner = owner
	s.wakeAcquire = false
	s.phoneFaceDown = false
	s.wakeFailures = 0
	s.unavailable = false
	s.renewDeadlineLocked()

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
	if owner != "" && s.leaseOwner != "" && s.leaseOwner != owner && s.holderIsLiveLocked() {
		return CompanionPhoneReadResult{}, CompanionPhoneSnapshot{}, ErrCompanionPhoneHeldByOther
	}
	s.renewDeadlineLocked()
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
	if owner != "" && s.leaseOwner != "" && s.leaseOwner != owner && s.holderIsLiveLocked() {
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
	s.persistMessageLocked(msg)
	s.wakeFailures = 0
	s.unavailable = false
	s.renewDeadlineLocked()
	snap := s.publishLocked()
	return msg, snap, nil
}

// PhonePutdown returns posture to "laptop" with style ("normal"|"facedown_merong")
// and next ("ambient"|"work"). When a delegation is in flight, next="ambient"
// settles to laptop_work with the lease still held ((1739)).
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
	if owner != "" && s.leaseOwner != "" && s.leaseOwner != owner && s.holderIsLiveLocked() {
		return CompanionPhoneSnapshot{}, false, ErrCompanionPhoneHeldByOther
	}
	if s.posture != CompanionPosturePhone {
		if next == CompanionPutdownNextWork {
			if owner != "" {
				s.leaseOwner = owner
			}
			changed := s.state != CompanionStateLaptopWork || s.wakeAcquire
			s.state = CompanionStateLaptopWork
			s.wakeAcquire = false
			s.renewDeadlineLocked()
			if changed {
				return s.publishLocked(), true, nil
			}
			return s.snapshotLocked(), false, nil
		}
		if s.delegationInFlight {
			changed := s.state != CompanionStateLaptopWork || s.wakeAcquire
			s.state = CompanionStateLaptopWork
			s.wakeAcquire = false
			s.renewDeadlineLocked()
			if changed {
				return s.publishLocked(), true, nil
			}
			return s.snapshotLocked(), false, nil
		}
		if s.state == CompanionStateLaptopWork || s.leaseOwner != "" {
			s.leaseOwner = ""
			s.wakeAcquire = false
			s.deadline = time.Time{}
			s.state = CompanionStateLaptopAmbient
			return s.publishLocked(), true, nil
		}
		return s.snapshotLocked(), false, nil
	}

	s.posture = CompanionPostureLaptop
	s.phoneFaceDown = style == CompanionPutdownFacedownMerong
	s.wakeAcquire = false
	s.typing = false
	if next == CompanionPutdownNextAmbient {
		s.appendSummaryLocked(summarizeLatestPhoneExchange(s.messages, style))
	}
	if next == CompanionPutdownNextWork || s.delegationInFlight {
		if owner != "" {
			s.leaseOwner = owner
		}
		s.state = CompanionStateLaptopWork
		s.renewDeadlineLocked()
	} else {
		s.leaseOwner = ""
		s.deadline = time.Time{}
		s.state = CompanionStateLaptopAmbient
	}
	return s.publishLocked(), true, nil
}

// ForceRelease releases holder's Embodiment Lease (on turn end, Tier-1 close/crash,
// or explicit forced cleanup). Passing an empty holder releases regardless of
// current holder. When a delegation is in flight, holder is cleared so a
// relaunched Tier-1 can acquire, while posture settles to laptop_work and visual
// delegation state continues ((1739), (1743)). Returns (snapshot, changed, hasUnread).
func (s *CompanionPhoneStore) ForceRelease(holder string) (CompanionPhoneSnapshot, bool, bool) {
	snap, changed, _, hasUnread := s.forceRelease(holder)
	return snap, changed, hasUnread
}

func (s *CompanionPhoneStore) forceRelease(holder string) (CompanionPhoneSnapshot, bool, bool, bool) {
	holder = strings.TrimSpace(holder)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if holder != "" && s.leaseOwner != "" && s.leaseOwner != holder {
		return s.snapshotLocked(), false, false, false
	}
	wasPhone := s.posture == CompanionPosturePhone || s.state == CompanionStatePhoneActive
	wasHeld := wasPhone || s.leaseOwner != "" || (!s.delegationInFlight && s.state == CompanionStateLaptopWork)
	if !wasHeld {
		return s.snapshotLocked(), false, false, len(s.unreadAgent) > 0
	}
	if wasPhone {
		s.posture = CompanionPostureLaptop
		s.phoneFaceDown = false
		s.appendSummaryLocked(summarizeLatestPhoneExchange(s.messages, CompanionPutdownNormal))
	}
	s.leaseOwner = ""
	s.wakeAcquire = false
	s.typing = false
	if s.delegationInFlight {
		s.state = CompanionStateLaptopWork
		if s.deadline.IsZero() {
			s.renewDeadlineLocked()
		}
	} else {
		s.state = CompanionStateLaptopAmbient
		s.deadline = time.Time{}
	}
	return s.publishLocked(), true, wasPhone, len(s.unreadAgent) > 0
}

// ReleaseOnTurnEnd is the turn-end failsafe hook: delegates to ForceRelease so
// an unclosed phone_active or laptop_work lease settles cleanly per (1739).
func (s *CompanionPhoneStore) ReleaseOnTurnEnd(owner string) (CompanionPhoneSnapshot, bool) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return s.Snapshot(), false
	}
	snap, changed, _ := s.ForceRelease(owner)
	return snap, changed
}

// ExpireIfPastDeadline checks whether the active Embodiment Lease has passed
// its sliding deadline. While a delegation is in flight and hasLiveTier2Children
// is true, the deadline is renewed instead of expiring ((1748)). Returns
// (snap, leaseExpired, delegationExpired, hasUnread).
func (s *CompanionPhoneStore) ExpireIfPastDeadline(now time.Time, hasLiveTier2Children bool) (CompanionPhoneSnapshot, bool, bool, bool) {
	snap, leaseExpired, delegationExpired, _, hasUnread := s.expireIfPastDeadline(now, hasLiveTier2Children)
	return snap, leaseExpired, delegationExpired, hasUnread
}

// expireIfPastDeadline is ExpireIfPastDeadline that also reports whether the
// expired lease held the phone, so the daemon can broadcast phone_putdown.
// While the Messenger is open and a live holder keeps the phone in hand, the
// idle deadline slides instead of expiring: the companion stays on the phone
// as long as the user is looking at the conversation.
func (s *CompanionPhoneStore) expireIfPastDeadline(now time.Time, hasLiveTier2Children bool) (snap CompanionPhoneSnapshot, leaseExpired, delegationExpired, wasPhone, hasUnread bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.deadline.IsZero() || now.Before(s.deadline) {
		return s.snapshotLocked(), false, false, false, false
	}
	wasPhone = s.posture == CompanionPosturePhone || s.state == CompanionStatePhoneActive
	if s.delegationInFlight {
		if hasLiveTier2Children {
			s.deadline = now.Add(CompanionLeaseDelegationTimeout)
			return s.snapshotLocked(), false, false, false, false
		}
		s.delegationInFlight = false
		s.leaseOwner = ""
		s.wakeAcquire = false
		s.typing = false
		s.posture = CompanionPostureLaptop
		s.phoneFaceDown = false
		s.state = CompanionStateLaptopAmbient
		s.deadline = time.Time{}
		return s.publishLocked(), true, true, wasPhone, len(s.unreadAgent) > 0
	}
	if wasPhone && s.messengerOpenLocked(now) && s.holderIsLiveLocked() {
		s.deadline = now.Add(CompanionLeaseIdleTimeout)
		return s.snapshotLocked(), false, false, false, false
	}
	if wasPhone {
		s.appendSummaryLocked(summarizeLatestPhoneExchange(s.messages, CompanionPutdownNormal))
	}
	s.leaseOwner = ""
	s.wakeAcquire = false
	s.typing = false
	s.posture = CompanionPostureLaptop
	s.phoneFaceDown = false
	s.state = CompanionStateLaptopAmbient
	s.deadline = time.Time{}
	return s.publishLocked(), true, false, wasPhone, len(s.unreadAgent) > 0
}

// HoldForAct puts the phone in owner's hand for a ws/companion.act
// phone_pickup/phone_typing gesture, so the visual never shows a phone the
// lease does not hold. Unlike PhonePickup it leaves the unread inbox alone
// (the act carries no read). Reports false when another live session holds
// the lease; the caller then drops the gesture.
func (s *CompanionPhoneStore) HoldForAct(owner string) bool {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.leaseOwner != "" && s.leaseOwner != owner && s.holderIsLiveLocked() {
		return false
	}
	changed := s.posture != CompanionPosturePhone || s.state != CompanionStatePhoneActive || s.leaseOwner != owner || s.phoneFaceDown
	s.posture = CompanionPosturePhone
	s.state = CompanionStatePhoneActive
	s.leaseOwner = owner
	s.wakeAcquire = false
	s.phoneFaceDown = false
	s.renewDeadlineLocked()
	if changed {
		s.publishLocked()
	}
	return true
}

// MigrateLease moves an Embodiment Lease held by from to to, keeping posture,
// state, and deadline: a companion-body handoff swaps sessions without the
// phone visibly leaving the companion's hand. Reports whether it moved.
func (s *CompanionPhoneStore) MigrateLease(from, to string) bool {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if from == "" || to == "" || from == to {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.leaseOwner != from {
		return false
	}
	s.leaseOwner = to
	s.publishLocked()
	return true
}

// RetainHoldOnTurnEnd keeps owner's phone in hand across a turn end while the
// Messenger is open: it renews the idle deadline and reports true when owner
// holds the phone and at least one Messenger client is open. The caller then
// skips the turn-end release.
func (s *CompanionPhoneStore) RetainHoldOnTurnEnd(owner string) bool {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if s.posture != CompanionPosturePhone || s.leaseOwner != owner || !s.messengerOpenLocked(s.clock()) {
		return false
	}
	s.typing = false
	s.renewDeadlineLocked()
	s.publishLocked()
	return true
}

// appendSummaryLocked appends one exchange line to the Tier-1 summary ring
// and records it to the history log.
func (s *CompanionPhoneStore) appendSummaryLocked(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	s.tier1Summary = appendBoundedSummary(s.tier1Summary, line, companionPhoneMaxSummaries)
	if s.persist != nil {
		s.persist(companionMessengerRecord{Type: companionRingSummary, Summary: line, MsgSeq: s.msgSeq, Turns: s.turns})
	}
}

func (s *CompanionPhoneStore) persistMessageLocked(msg CompanionPhoneMessage) {
	if s.persist != nil {
		m := msg
		s.persist(companionMessengerRecord{Type: companionRingMessage, Message: &m, MsgSeq: s.msgSeq, Turns: s.turns})
	}
}

// SetSleepy toggles between laptop_ambient and laptop_sleepy when the
// Embodiment Lease is free.
func (s *CompanionPhoneStore) SetSleepy(sleepy bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	hadOwner := s.leaseOwner != ""
	live := s.holderIsLiveLocked()
	cleared := hadOwner && !live
	if s.delegationInFlight || live || s.state == CompanionStatePhoneActive || s.state == CompanionStateLaptopWork {
		if cleared {
			s.publishLocked()
		}
		return false
	}
	want := CompanionStateLaptopAmbient
	if sleepy {
		want = CompanionStateLaptopSleepy
	}
	if s.state == want {
		if cleared {
			s.publishLocked()
		}
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
	if s.persist != nil {
		s.persist(companionMessengerRecord{Type: companionRingResetSummary, MsgSeq: s.msgSeq})
	}
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
	gesture = d.alignCompanionPhoneGesture("", gesture)
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
	// The putdown gesture goes out even when the store had nothing to clear:
	// a stage whose pose drifted from the lease (a lost snapshot or command)
	// re-converges, and the stage treats a putdown on a resting phone as a
	// no-op.
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
	if changed && snap.UnreadAgent > 0 && !snap.Unavailable && snap.Posture == CompanionPostureLaptop && snap.LeaseOwner == "" {
		d.notifyCompanionWake("release-unread")
	}
	return snap, nil
}

// CompanionReleaseOnTurnEnd releases any held Embodiment Lease at turn end,
// except that while the Messenger is open a phone held by owner stays in
// hand (deadline renewed) so the conversation does not bounce the phone to
// the desk after every reply. Close, crash, failed-turn, and zero-progress
// paths use CompanionForceRelease, which always releases.
func (d *Daemon) CompanionReleaseOnTurnEnd(owner string) bool {
	if d.companionPhone.RetainHoldOnTurnEnd(owner) {
		d.CompanionResetBurst(owner)
		return false
	}
	return d.CompanionForceRelease(owner)
}

// CompanionExpireIfPastDeadline runs the sliding-deadline check, first
// expiring stale Messenger presence (a tab that vanished without a close
// beacon), and broadcasts phone_putdown when the expired lease held the
// phone so the stage never keeps a phone the server already put down.
// Returns (leaseExpired, delegationExpired, hasUnread).
func (d *Daemon) CompanionExpireIfPastDeadline(now time.Time, hasLiveTier2Children bool) (bool, bool, bool) {
	d.pruneCompanionMessengerPresence(now)
	_, leaseExpired, delegationExpired, wasPhone, hasUnread := d.companionPhone.expireIfPastDeadline(now, hasLiveTier2Children)
	if leaseExpired && wasPhone {
		d.PublishCompanionCommand(CompanionCommand{
			SessionID: companionPhoneBroadcastSession,
			Gesture:   "phone_putdown",
			Prop:      "smartphone",
			TTLms:     3000,
		})
	}
	return leaseExpired, delegationExpired, hasUnread
}

// alignCompanionPhoneGesture reconciles a phone gesture an agent asked for
// (ws/companion.act or ws/companion.motion) with the Embodiment Lease, so the
// stage pose and the server lease cannot disagree: pickup/typing take the
// phone into owner's hand (or are dropped when another live session holds
// it), and putdown/merong release it. Non-phone gestures pass through.
//
// An ownerless caller (ws/companion.motion) cannot move the lease, so its
// phone gesture passes only when it agrees with the current posture.
func (d *Daemon) alignCompanionPhoneGesture(owner, gesture string) string {
	g := strings.ToLower(strings.TrimSpace(gesture))
	owner = strings.TrimSpace(owner)
	if owner == "" {
		held := d.companionPhone.PostureFor("") == CompanionPosturePhone
		switch g {
		case "phone_pickup", "phone_typing":
			if !held {
				return ""
			}
		case "phone_putdown", "phone_merong_facedown":
			if held {
				return ""
			}
		}
		return gesture
	}
	switch g {
	case "phone_pickup", "phone_typing":
		if !d.companionPhone.HoldForAct(owner) {
			return ""
		}
	case "phone_putdown", "phone_merong_facedown":
		style := CompanionPutdownNormal
		if g == "phone_merong_facedown" {
			style = CompanionPutdownFacedownMerong
		}
		snap, changed, err := d.companionPhone.PhonePutdown(owner, style, CompanionPutdownNextAmbient)
		if err != nil {
			return ""
		}
		if changed && snap.UnreadAgent > 0 && !snap.Unavailable && snap.Posture == CompanionPostureLaptop && snap.LeaseOwner == "" {
			d.notifyCompanionWake("release-unread")
		}
	}
	return gesture
}

// CompanionForceRelease releases holder's Embodiment Lease (turn end, close, or
// crash), emits phone_putdown visuals when needed, and triggers an unread re-wake
// when messages are still waiting and the messenger is not unavailable ((1739),
// (1743), (1749)).
func (d *Daemon) CompanionForceRelease(holder string) bool {
	d.CompanionResetBurst(holder)
	snap, changed, wasPhone, hasUnread := d.companionPhone.forceRelease(holder)
	if changed && wasPhone {
		d.PublishCompanionCommand(CompanionCommand{
			SessionID: companionPhoneBroadcastSession,
			Gesture:   "phone_putdown",
			TTLms:     3000,
		})
	}
	if changed && hasUnread && !snap.Unavailable {
		d.notifyCompanionWake("release-unread")
	}
	return changed
}

// SetCompanionWake registers the daemon-side Tier-1 burst wake callback.
func (d *Daemon) SetCompanionWake(fn func(reason string)) {
	if fn == nil {
		d.companionWake.Store(nil)
		return
	}
	d.companionWake.Store(&fn)
}

// SetCompanionHolderAlive installs the live-session check used during CAS
// Embodiment Lease acquisition ((1739)).
func (d *Daemon) SetCompanionHolderAlive(fn func(holder string) bool) {
	d.companionPhone.SetHolderAlive(fn)
}

func (d *Daemon) notifyCompanionWake(reason string) {
	if fn := d.companionWake.Load(); fn != nil && *fn != nil {
		go (*fn)(reason)
	}
}

// CompanionPhone returns the daemon's in-memory CompanionPhoneStore.
func (d *Daemon) CompanionPhone() *CompanionPhoneStore {
	return &d.companionPhone
}

// CompanionDirectorEnabled reports whether companion agent mode is active
// (dashboard.companion_enabled && dashboard.companion_director == "agent").
func (d *Daemon) CompanionDirectorEnabled() bool {
	cfg := d.companion.current().config
	return cfg.Enabled && cfg.Director == "agent"
}

// CompanionActivePersona returns the currently focused pack's persona and
// resolved language for building the Tier-1 Companion persona prompt.
func (d *Daemon) CompanionActivePersona() (*CompanionPersona, string) {
	packID := companionDefaultID
	d.director.mu.Lock()
	var bestSeq uint64
	for _, f := range d.director.streams {
		if f.seq >= bestSeq && f.pack != "" {
			bestSeq = f.seq
			packID = f.pack
		}
	}
	d.director.mu.Unlock()
	pack := d.companionPackFor(packID)
	lang := ""
	if pack.persona != nil {
		lang = pack.persona.QuipLanguage
	}
	if lang == "" {
		if overrides, err := wsconfig.GlobalOverrides(d.companionOptions()); err == nil {
			lang = overrides[wsconfig.ItemWorkflowLang]
		}
	}
	return pack.persona, lang
}

// CompanionActiveRoot resolves the workspace root for daemon-launched companion
// sessions: the focused session's project root first, then any live indexed
// session's project root, then a registered workspace root (preferring Git
// repositories). Returns "" when no session or workspace is registered.
func (d *Daemon) CompanionActiveRoot() string {
	d.director.mu.Lock()
	focusID := d.director.focusLocked().sessionID
	d.director.mu.Unlock()
	if focusID != "" && d.index != nil {
		if s, ok := d.index.Session(focusID); ok && strings.TrimSpace(s.Project) != "" {
			return s.Project
		}
	}
	if d.index != nil {
		var fallback string
		for _, s := range d.index.Sessions() {
			if s.Hidden || strings.TrimSpace(s.Project) == "" {
				continue
			}
			if s.Live {
				return s.Project
			}
			if fallback == "" {
				fallback = s.Project
			}
		}
		if fallback != "" {
			return fallback
		}
	}
	if d.workspaces != nil {
		var fallback string
		for _, w := range d.workspaces.workspaces() {
			if strings.TrimSpace(w.Path) == "" {
				continue
			}
			if w.Repo {
				return w.Path
			}
			if fallback == "" {
				fallback = w.Path
			}
		}
		if fallback != "" {
			return fallback
		}
	}
	return ""
}

// --- HTTP routes -------------------------------------------------------------

func (d *Daemon) registerCompanionPhoneRoutes(mux *http.ServeMux) {
	mux.HandleFunc(companionPhoneThreadRoute, d.handleCompanionPhoneThread)
	mux.HandleFunc(companionPhoneSendRoute, d.handleCompanionPhoneSend)
	mux.HandleFunc(companionPhoneMarkReadRoute, d.handleCompanionPhoneMarkRead)
	mux.HandleFunc(companionPhoneResetSummaryRoute, d.handleCompanionPhoneResetSummary)
	mux.HandleFunc(companionMessengerPresenceRoute, d.handleCompanionMessengerPresence)
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
	d.ObserveCompanionMood(CompanionMoodEventUserMessage)
	d.PublishCompanionCommand(CompanionCommand{
		SessionID: companionPhoneBroadcastSession,
		Gesture:   "phone_buzz",
		Prop:      "smartphone",
		TTLms:     4000,
	})
	d.notifyCompanionWake("user-message")
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
