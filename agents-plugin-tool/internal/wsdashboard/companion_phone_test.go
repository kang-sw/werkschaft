package wsdashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCompanionPhoneStoreLifecycleAndCaps(t *testing.T) {
	now := time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)
	store := &CompanionPhoneStore{now: func() time.Time { return now }}

	// Initial state: laptop_ambient, lease free.
	snap := store.Snapshot()
	if snap.State != CompanionStateLaptopAmbient || snap.Posture != CompanionPostureLaptop {
		t.Fatalf("initial snapshot = (%q, %q), want (laptop_ambient, laptop)", snap.State, snap.Posture)
	}
	if store.EmbodimentLeaseActive() {
		t.Fatal("initial EmbodimentLeaseActive() = true, want false")
	}

	// Sleepy transition works when lease is free.
	if !store.SetSleepy(true) {
		t.Fatal("SetSleepy(true) = false, want true")
	}
	if got := store.Snapshot().State; got != CompanionStateLaptopSleepy {
		t.Fatalf("state after SetSleepy(true) = %q, want laptop_sleepy", got)
	}

	// PhoneRead / PhoneSend refused while phone is on desk.
	if _, _, err := store.PhoneRead("sess-a"); !errors.Is(err, ErrCompanionPhoneOnDesk) {
		t.Fatalf("PhoneRead on desk err = %v, want ErrCompanionPhoneOnDesk", err)
	}
	if _, _, err := store.PhoneSend("sess-a", "hi", "happy", nil); !errors.Is(err, ErrCompanionPhoneOnDesk) {
		t.Fatalf("PhoneSend on desk err = %v, want ErrCompanionPhoneOnDesk", err)
	}

	// EnqueueUserMessage wakes sleepy companion, queues unread agent inbox, and bumps seq.
	seq0, _, wake0 := store.Current()
	msg1, snap, err := store.EnqueueUserMessage("  hello from user  ")
	if err != nil {
		t.Fatalf("EnqueueUserMessage: %v", err)
	}
	if msg1.Sender != "user" || msg1.Text != "hello from user" {
		t.Fatalf("msg1 = %+v", msg1)
	}
	if snap.State != CompanionStateLaptopAmbient || snap.UnreadAgent != 1 || snap.Seq <= seq0 {
		t.Fatalf("snap after enqueue = %+v", snap)
	}
	select {
	case <-wake0:
	default:
		t.Fatal("EnqueueUserMessage did not close SSE wake channel")
	}

	// PhonePickup locks lease, switches posture to phone, drains unread.
	readRes, snap, err := store.PhonePickup("sess-a")
	if err != nil {
		t.Fatalf("PhonePickup: %v", err)
	}
	if readRes.State != CompanionStatePhoneActive || readRes.Posture != CompanionPosturePhone {
		t.Fatalf("pickup result = %+v", readRes)
	}
	if len(readRes.Unread) != 1 || readRes.Unread[0].Text != "hello from user" {
		t.Fatalf("pickup unread = %+v", readRes.Unread)
	}
	if !store.EmbodimentLeaseActive() {
		t.Fatal("EmbodimentLeaseActive() = false during phone_active")
	}
	if store.PostureFor("sess-a") != CompanionPosturePhone {
		t.Fatalf("PostureFor(sess-a) = %q, want phone", store.PostureFor("sess-a"))
	}
	if store.PostureFor("sess-b") != CompanionPostureLaptop {
		t.Fatalf("PostureFor(sess-b) = %q, want laptop", store.PostureFor("sess-b"))
	}
	if store.SetSleepy(true) {
		t.Fatal("SetSleepy(true) must be refused while lease is active")
	}

	// Another owner cannot steal an active phone lease.
	if _, _, err := store.PhonePickup("sess-b"); !errors.Is(err, ErrCompanionPhoneHeldByOther) {
		t.Fatalf("cross-owner PhonePickup err = %v, want ErrCompanionPhoneHeldByOther", err)
	}
	if _, _, err := store.PhoneRead("sess-b"); !errors.Is(err, ErrCompanionPhoneHeldByOther) {
		t.Fatalf("cross-owner PhoneRead err = %v, want ErrCompanionPhoneHeldByOther", err)
	}
	if _, _, err := store.PhoneSend("sess-b", "nope", "", nil); !errors.Is(err, ErrCompanionPhoneHeldByOther) {
		t.Fatalf("cross-owner PhoneSend err = %v, want ErrCompanionPhoneHeldByOther", err)
	}
	if _, _, err := store.PhonePutdown("sess-b", CompanionPutdownNormal, CompanionPutdownNextAmbient); !errors.Is(err, ErrCompanionPhoneHeldByOther) {
		t.Fatalf("cross-owner PhonePutdown err = %v, want ErrCompanionPhoneHeldByOther", err)
	}

	// Newly arrived user message while phone is held can be drained via PhoneRead.
	if _, _, err := store.EnqueueUserMessage("second ping"); err != nil {
		t.Fatalf("second enqueue: %v", err)
	}
	read2, _, err := store.PhoneRead("sess-a")
	if err != nil {
		t.Fatalf("PhoneRead: %v", err)
	}
	if len(read2.Unread) != 1 || read2.Unread[0].Text != "second ping" {
		t.Fatalf("PhoneRead unread = %+v", read2.Unread)
	}

	// PhoneSend appends companion reply and increments unreadClient.
	reply, snap, err := store.PhoneSend("sess-a", "hehe merong!", "merong", nil)
	if err != nil {
		t.Fatalf("PhoneSend: %v", err)
	}
	if reply.Sender != "companion" || reply.Tone != "merong" || snap.UnreadClient != 1 {
		t.Fatalf("reply=%+v snap=%+v", reply, snap)
	}

	// MarkClientRead clears unreadClient.
	if snap = store.MarkClientRead(); snap.UnreadClient != 0 {
		t.Fatalf("UnreadClient after MarkClientRead = %d, want 0", snap.UnreadClient)
	}

	// PhonePutdown(facedown_merong, ambient) flips phone face-down, frees lease, records Tier-1 summary.
	snap, changed, err := store.PhonePutdown("sess-a", CompanionPutdownFacedownMerong, CompanionPutdownNextAmbient)
	if err != nil || !changed {
		t.Fatalf("PhonePutdown(facedown_merong) = (%+v, %v, %v)", snap, changed, err)
	}
	if snap.State != CompanionStateLaptopAmbient || snap.Posture != CompanionPostureLaptop || !snap.PhoneFaceDown {
		t.Fatalf("snap after merong putdown = %+v", snap)
	}
	if store.EmbodimentLeaseActive() {
		t.Fatal("EmbodimentLeaseActive() = true after ambient putdown")
	}
	if len(snap.Tier1Summary) != 1 || !strings.Contains(snap.Tier1Summary[0], "[merong]") {
		t.Fatalf("Tier1Summary = %v, want 1 [merong] summary", snap.Tier1Summary)
	}

	// PhonePutdown(normal, work) transitions to laptop_work while keeping lease locked;
	// ReleaseOnTurnEnd frees it back to laptop_ambient.
	if _, _, err := store.PhonePickup("sess-a"); err != nil {
		t.Fatalf("re-pickup: %v", err)
	}
	snap, changed, err = store.PhonePutdown("sess-a", CompanionPutdownNormal, CompanionPutdownNextWork)
	if err != nil || !changed {
		t.Fatalf("PhonePutdown(normal, work) = (%+v, %v, %v)", snap, changed, err)
	}
	if snap.State != CompanionStateLaptopWork || snap.Posture != CompanionPostureLaptop || snap.PhoneFaceDown || !store.EmbodimentLeaseActive() {
		t.Fatalf("snap after work putdown = %+v, lease=%v", snap, store.EmbodimentLeaseActive())
	}
	snap, released := store.ReleaseOnTurnEnd("sess-a")
	if !released || snap.State != CompanionStateLaptopAmbient || store.EmbodimentLeaseActive() {
		t.Fatalf("ReleaseOnTurnEnd(laptop_work) = (%+v, %v), lease=%v", snap, released, store.EmbodimentLeaseActive())
	}

	// ReleaseOnTurnEnd also auto-puts down an unclosed phone_active lease.
	if _, _, err := store.PhonePickup("sess-a"); err != nil {
		t.Fatalf("pickup for failsafe: %v", err)
	}
	snap, released = store.ReleaseOnTurnEnd("sess-a")
	if !released || snap.State != CompanionStateLaptopAmbient || snap.Posture != CompanionPostureLaptop || store.EmbodimentLeaseActive() {
		t.Fatalf("ReleaseOnTurnEnd(phone_active) = (%+v, %v)", snap, released)
	}

	// Ring caps: 50 messages and 10 summaries.
	for i := 0; i < 65; i++ {
		if _, _, err := store.EnqueueUserMessage(fmt.Sprintf("msg-%02d", i)); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if got := len(store.Snapshot().Messages); got != companionPhoneMaxMessages {
		t.Fatalf("messages len = %d, want cap %d", got, companionPhoneMaxMessages)
	}
	for i := 0; i < 15; i++ {
		if _, _, err := store.PhonePickup("sess-a"); err != nil {
			t.Fatalf("pickup %d: %v", i, err)
		}
		if _, _, err := store.PhoneSend("sess-a", fmt.Sprintf("reply-%02d", i), "", nil); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		if _, _, err := store.PhonePutdown("sess-a", CompanionPutdownNormal, CompanionPutdownNextAmbient); err != nil {
			t.Fatalf("putdown %d: %v", i, err)
		}
	}
	if got := len(store.Snapshot().Tier1Summary); got != companionPhoneMaxSummaries {
		t.Fatalf("tier1Summary len = %d, want cap %d", got, companionPhoneMaxSummaries)
	}
	if snap = store.ResetSummary(); len(snap.Tier1Summary) != 0 || snap.RotationMeta.Turns != 0 {
		t.Fatalf("ResetSummary snap = %+v", snap)
	}
}

type stubCompanionRunner struct {
	calls int
}

func (r *stubCompanionRunner) SupportsHarness(string) bool { return true }
func (r *stubCompanionRunner) Execute(context.Context, CompanionEphemeralRequest) (string, error) {
	r.calls++
	return `{"expression":"happy","quip":"hi"}`, nil
}

func TestCompanionDirectorPausedByEmbodimentLease(t *testing.T) {
	d := &Daemon{}
	runner := &stubCompanionRunner{}
	d.director.pool = runner
	setCompanionConfig(d, agentCompanion())
	d.director.streams = map[string]companionFocus{"tok": {seq: 1, sessionID: "sess-1", pack: "default"}}

	// Lock Embodiment Lease via PhonePickup.
	if _, err := d.CompanionPhonePickup("lead-1"); err != nil {
		t.Fatalf("CompanionPhonePickup: %v", err)
	}
	if !d.companionPhone.EmbodimentLeaseActive() {
		t.Fatal("expected EmbodimentLeaseActive() == true")
	}

	// Tier-1 directorFire must drop with embodiment-lease without invoking runner.
	d.directorFire("sess-1", companionTriggerTurnEnd)
	if runner.calls != 0 {
		t.Fatalf("directorFire ran %d times while Embodiment Lease was active, want 0", runner.calls)
	}
	if got := d.director.dropped["embodiment-lease"]; got != 1 {
		t.Fatalf("dropped[embodiment-lease] = %d, want 1", got)
	}
}

func TestCompanionPhoneHTTPRoutes(t *testing.T) {
	d := &Daemon{}
	mux := http.NewServeMux()
	d.registerCompanionPhoneRoutes(mux)

	// GET /api/companion/phone/thread returns initial laptop_ambient snapshot.
	req := httptest.NewRequest(http.MethodGet, companionPhoneThreadRoute, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET thread status = %d, want 200", rec.Code)
	}
	var snap CompanionPhoneSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode thread: %v", err)
	}
	if snap.State != CompanionStateLaptopAmbient || snap.Posture != CompanionPostureLaptop {
		t.Fatalf("GET thread snap = %+v", snap)
	}

	// Cross-origin POST is forbidden by TrustedWrite.
	cross := httptest.NewRequest(http.MethodPost, companionPhoneSendRoute, bytes.NewBufferString(`{"text":"hi"}`))
	cross.RemoteAddr = "127.0.0.1:43210"
	cross.Host = "127.0.0.1:8080"
	cross.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, cross)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST status = %d, want 403", rec.Code)
	}

	// Same-origin local POST /api/companion/phone/send enqueues message & emits phone_buzz command.
	cmdSeqBefore, _, _ := d.companionCommands.current()
	sendReq := httptest.NewRequest(http.MethodPost, companionPhoneSendRoute, bytes.NewBufferString(`{"text":"check phone"}`))
	sendReq.RemoteAddr = "127.0.0.1:43210"
	sendReq.Host = "127.0.0.1:8080"
	sendReq.Header.Set("Origin", "http://127.0.0.1:8080")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, sendReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST send status = %d, body=%q", rec.Code, rec.Body.String())
	}
	cmdSeqAfter, cmdBytes, _ := d.companionCommands.current()
	if cmdSeqAfter <= cmdSeqBefore || !strings.Contains(string(cmdBytes), `"phone_buzz"`) {
		t.Fatalf("expected phone_buzz companion command, seq %d->%d payload=%s", cmdSeqBefore, cmdSeqAfter, string(cmdBytes))
	}

	// Companion replies -> unreadClient increments -> POST mark-read clears it.
	if _, err := d.CompanionPhonePickup("lead"); err != nil {
		t.Fatalf("pickup: %v", err)
	}
	if _, err := d.CompanionPhoneSend("lead", "got it", "happy"); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if _, err := d.CompanionPhonePutdown("lead", CompanionPutdownNormal, CompanionPutdownNextAmbient); err != nil {
		t.Fatalf("putdown: %v", err)
	}
	if d.companionPhone.Snapshot().UnreadClient != 1 {
		t.Fatalf("expected unreadClient=1 before mark-read")
	}

	markReq := httptest.NewRequest(http.MethodPost, companionPhoneMarkReadRoute, nil)
	markReq.RemoteAddr = "127.0.0.1:43210"
	markReq.Host = "127.0.0.1:8080"
	markReq.Header.Set("Origin", "http://127.0.0.1:8080")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, markReq)
	if rec.Code != http.StatusOK || d.companionPhone.Snapshot().UnreadClient != 0 {
		t.Fatalf("mark-read status=%d unread=%d", rec.Code, d.companionPhone.Snapshot().UnreadClient)
	}

	// POST reset-summary clears Tier-1 summary ring.
	resetReq := httptest.NewRequest(http.MethodPost, companionPhoneResetSummaryRoute, nil)
	resetReq.RemoteAddr = "127.0.0.1:43210"
	resetReq.Host = "127.0.0.1:8080"
	resetReq.Header.Set("Origin", "http://127.0.0.1:8080")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, resetReq)
	if rec.Code != http.StatusOK || len(d.companionPhone.Snapshot().Tier1Summary) != 0 {
		t.Fatalf("reset-summary status=%d summaries=%v", rec.Code, d.companionPhone.Snapshot().Tier1Summary)
	}
}

func TestCompanionPhoneStoreForceReleaseDeadlinesAndHolderLiveness(t *testing.T) {
	now := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	alive := map[string]bool{"tier1-a": true, "tier1-b": true}
	store := &CompanionPhoneStore{
		now:         func() time.Time { return now },
		holderAlive: func(h string) bool { return alive[h] },
	}

	// AcquireOnWake acquires in laptop posture without prematurely showing laptop_work.
	snap, ok := store.AcquireOnWake("tier1-a")
	if !ok {
		t.Fatal("AcquireOnWake(tier1-a) = false, want true")
	}
	if snap.State != CompanionStateLaptopAmbient || snap.Posture != CompanionPostureLaptop || snap.LeaseOwner != "tier1-a" {
		t.Fatalf("snap after AcquireOnWake = %+v", snap)
	}
	if !store.EmbodimentLeaseActive() {
		t.Fatal("EmbodimentLeaseActive() = false after AcquireOnWake")
	}

	// Live holder blocks another Tier-1 key even in laptop posture (CAS check).
	if _, _, err := store.PhonePickup("tier1-b"); !errors.Is(err, ErrCompanionPhoneHeldByOther) {
		t.Fatalf("PhonePickup by live-blocked tier1-b err = %v, want ErrCompanionPhoneHeldByOther", err)
	}
	if _, _, err := store.PhonePutdown("tier1-b", CompanionPutdownNormal, CompanionPutdownNextAmbient); !errors.Is(err, ErrCompanionPhoneHeldByOther) {
		t.Fatalf("PhonePutdown by live-blocked tier1-b err = %v, want ErrCompanionPhoneHeldByOther", err)
	}

	// Once old holder dies, CAS acquire by relaunched tier1-b succeeds immediately.
	alive["tier1-a"] = false
	if _, _, err := store.PhonePickup("tier1-b"); err != nil {
		t.Fatalf("PhonePickup after old holder died = %v, want nil", err)
	}
	if got := store.Snapshot().LeaseOwner; got != "tier1-b" {
		t.Fatalf("LeaseOwner = %q, want tier1-b", got)
	}

	// Sliding 5m idle deadline: renew shifts it; expiry releases lease and reports unread.
	now = now.Add(CompanionLeaseIdleTimeout - time.Second)
	if _, expired, _, _ := store.ExpireIfPastDeadline(now, false); expired {
		t.Fatal("expired before idle deadline")
	}
	if !store.RenewLease("tier1-b") {
		t.Fatal("RenewLease(tier1-b) = false")
	}
	now = now.Add(CompanionLeaseIdleTimeout - time.Second)
	if _, expired, _, _ := store.ExpireIfPastDeadline(now, false); expired {
		t.Fatal("expired before renewed idle deadline")
	}
	if _, _, err := store.EnqueueUserMessage("ping during hold"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	now = now.Add(2 * time.Second)
	snap, leaseExpired, delegExpired, hasUnread := store.ExpireIfPastDeadline(now, false)
	if !leaseExpired || delegExpired || !hasUnread {
		t.Fatalf("ExpireIfPastDeadline(idle) = (lease=%v, deleg=%v, unread=%v), want (true, false, true)", leaseExpired, delegExpired, hasUnread)
	}
	if snap.State != CompanionStateLaptopAmbient || snap.Posture != CompanionPostureLaptop || snap.LeaseOwner != "" {
		t.Fatalf("snap after idle expiry = %+v", snap)
	}

	// Delegation in flight: putdown(normal, ambient) settles to laptop_work with lease held;
	// ForceRelease clears holder so relaunched Tier-1 can acquire while laptop_work continues.
	if _, _, err := store.PhonePickup("tier1-b"); err != nil {
		t.Fatalf("pickup: %v", err)
	}
	store.SetDelegationInFlight(true)
	snap, changed, err := store.PhonePutdown("tier1-b", CompanionPutdownNormal, CompanionPutdownNextAmbient)
	if err != nil || !changed {
		t.Fatalf("PhonePutdown during delegation = (%+v, %v, %v)", snap, changed, err)
	}
	if snap.State != CompanionStateLaptopWork || snap.Posture != CompanionPostureLaptop || !snap.DelegationInFlight || !store.EmbodimentLeaseActive() {
		t.Fatalf("snap after putdown during delegation = %+v", snap)
	}
	snap, changed, _ = store.ForceRelease("tier1-b")
	if !changed || snap.LeaseOwner != "" || snap.State != CompanionStateLaptopWork || !snap.DelegationInFlight {
		t.Fatalf("snap after ForceRelease during delegation = (%+v, %v)", snap, changed)
	}
	// Relaunched Tier-1 can pick up phone while delegation stays in flight.
	alive["tier1-c"] = true
	if _, _, err := store.PhonePickup("tier1-c"); err != nil {
		t.Fatalf("relaunched pickup during delegation: %v", err)
	}
	if _, _, err := store.PhonePutdown("tier1-c", CompanionPutdownNormal, CompanionPutdownNextAmbient); err != nil {
		t.Fatalf("putdown tier1-c: %v", err)
	}

	// In-flight 30m deadline does NOT expire when Tier-2 has live children; expires once children finish.
	now = now.Add(CompanionLeaseDelegationTimeout + time.Second)
	if _, leaseExpired, delegExpired, _ := store.ExpireIfPastDeadline(now, true); leaseExpired || delegExpired {
		t.Fatalf("in-flight deadline expired while Tier-2 has live children")
	}
	now = now.Add(CompanionLeaseDelegationTimeout + time.Second)
	snap, leaseExpired, delegExpired, _ = store.ExpireIfPastDeadline(now, false)
	if !leaseExpired || !delegExpired || snap.DelegationInFlight || snap.State != CompanionStateLaptopAmbient {
		t.Fatalf("in-flight deadline expiry = (lease=%v, deleg=%v, snap=%+v)", leaseExpired, delegExpired, snap)
	}

	// Wake failure bound: 3 consecutive failures enter unavailable; next user message resets it.
	for i := 1; i < CompanionWakeMaxRetries; i++ {
		if snap, unavail := store.RecordWakeFailure(); unavail || snap.Unavailable {
			t.Fatalf("failure %d entered unavailable early: %+v", i, snap)
		}
	}
	if snap, unavail := store.RecordWakeFailure(); !unavail || !snap.Unavailable {
		t.Fatalf("failure %d did not enter unavailable: (%v, %+v)", CompanionWakeMaxRetries, unavail, snap)
	}
	if _, snap, err := store.EnqueueUserMessage("retry now"); err != nil || snap.Unavailable {
		t.Fatalf("EnqueueUserMessage did not clear unavailable: err=%v snap=%+v", err, snap)
	}
}

func TestCompanionForceReleaseTriggersUnreadRewakeAndDirectorSummaryBackfeed(t *testing.T) {
	d := &Daemon{}
	wakes := make(chan string, 4)
	d.SetCompanionWake(func(reason string) {
		wakes <- reason
	})

	if _, err := d.CompanionPhonePickup("tier1-key"); err != nil {
		t.Fatalf("pickup: %v", err)
	}
	if _, err := d.CompanionPhoneSend("tier1-key", "Checking tests!", "happy"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, _, err := d.companionPhone.EnqueueUserMessage("one more thing"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !d.CompanionForceRelease("tier1-key") {
		t.Fatal("CompanionForceRelease = false, want true")
	}
	select {
	case reason := <-wakes:
		if reason != "release-unread" {
			t.Fatalf("wake reason = %q, want release-unread", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for release-unread wake callback")
	}

	// Ambient director digest receives 1-line recent_messenger summary backfeed ((1730)=a).
	summary := d.companionPhone.LatestSummary()
	if summary == "" {
		t.Fatal("expected non-empty LatestSummary after phone release")
	}
	digest := companionDigest(companionCall{phase: "idle"}, nil, summary)
	if !strings.Contains(digest, "recent_messenger: ") || !strings.Contains(digest, "Checking tests!") {
		t.Fatalf("companionDigest missing recent_messenger backfeed:\n%s", digest)
	}
}
