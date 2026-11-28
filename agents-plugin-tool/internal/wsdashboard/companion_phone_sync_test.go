package wsdashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func latestCompanionGesture(t *testing.T, d *Daemon) (uint64, string) {
	t.Helper()
	seq, payload, _ := d.companionCommands.current()
	if len(payload) == 0 {
		return seq, ""
	}
	var cmd CompanionCommand
	if err := json.Unmarshal(payload, &cmd); err != nil {
		t.Fatalf("decode companion command %s: %v", payload, err)
	}
	return seq, cmd.Gesture
}

func TestCompanionExpiryBroadcastsPhonePutdown(t *testing.T) {
	now := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	d := &Daemon{}
	d.companionPhone.now = func() time.Time { return now }
	if _, err := d.CompanionPhonePickup("body"); err != nil {
		t.Fatal(err)
	}
	seqBefore, _ := latestCompanionGesture(t, d)
	leaseExpired, _, _ := d.CompanionExpireIfPastDeadline(now.Add(CompanionLeaseIdleTimeout+time.Second), false)
	if !leaseExpired {
		t.Fatal("lease did not expire past its deadline")
	}
	seq, gesture := latestCompanionGesture(t, d)
	if seq <= seqBefore || gesture != "phone_putdown" {
		t.Fatalf("expiry gesture = %q (seq %d -> %d), want phone_putdown", gesture, seqBefore, seq)
	}
	if snap := d.companionPhone.Snapshot(); snap.Posture != CompanionPostureLaptop || snap.LeaseOwner != "" {
		t.Fatalf("snapshot after expiry = %+v", snap)
	}

	// A laptop-posture lease expiring publishes no phone gesture.
	d.companionPhone.AcquireOnWake("body")
	seqBefore, _ = latestCompanionGesture(t, d)
	if leaseExpired, _, _ := d.CompanionExpireIfPastDeadline(now.Add(2*CompanionLeaseIdleTimeout), false); !leaseExpired {
		t.Fatal("laptop lease did not expire")
	}
	if seq, _ := latestCompanionGesture(t, d); seq != seqBefore {
		t.Fatalf("laptop-lease expiry published a gesture (seq %d -> %d)", seqBefore, seq)
	}
}

func TestCompanionExpiryDelegationBranchPutsPhoneDown(t *testing.T) {
	now := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	d := &Daemon{}
	d.companionPhone.now = func() time.Time { return now }
	if _, err := d.CompanionPhonePickup("body"); err != nil {
		t.Fatal(err)
	}
	d.companionPhone.SetDelegationInFlight(true)
	_, delegationExpired, _ := d.CompanionExpireIfPastDeadline(now.Add(CompanionLeaseDelegationTimeout+time.Second), false)
	if !delegationExpired {
		t.Fatal("delegation lease did not expire")
	}
	if _, gesture := latestCompanionGesture(t, d); gesture != "phone_putdown" {
		t.Fatalf("delegation expiry gesture = %q, want phone_putdown", gesture)
	}
}

func TestCompanionActAlignsPhoneGesturesWithLease(t *testing.T) {
	d := &Daemon{}
	d.SetCompanionActTimer(func(time.Duration) (<-chan time.Time, func()) {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch, func() {}
	})
	ctx := context.Background()
	d.companionPhone.EnqueueUserMessage("hello")
	d.companionPhone.AcquireOnWake("body")

	// phone_typing without a pickup takes the phone into the owner's hand,
	// leaving the unread inbox for phone_read.
	if _, err := d.CompanionAct(ctx, "body", CompanionActRequest{Gesture: "phone_typing"}); err != nil {
		t.Fatal(err)
	}
	snap := d.companionPhone.Snapshot()
	if snap.Posture != CompanionPosturePhone || snap.LeaseOwner != "body" || snap.UnreadAgent != 1 {
		t.Fatalf("after act phone_typing: %+v", snap)
	}
	if _, gesture := latestCompanionGesture(t, d); gesture != "phone_typing" {
		t.Fatalf("act gesture = %q, want phone_typing", gesture)
	}

	// Another live session cannot raise the phone: its gesture is dropped.
	seqBefore, _ := latestCompanionGesture(t, d)
	if _, err := d.CompanionAct(ctx, "other", CompanionActRequest{Gesture: "phone_pickup", Expression: "happy"}); err != nil {
		t.Fatal(err)
	}
	seq, gesture := latestCompanionGesture(t, d)
	if seq <= seqBefore || gesture != "" {
		t.Fatalf("foreign act gesture = %q (seq %d -> %d), want dropped gesture with the expression still published", gesture, seqBefore, seq)
	}
	if d.companionPhone.Snapshot().LeaseOwner != "body" {
		t.Fatal("foreign act moved the lease")
	}

	// act phone_putdown releases the lease.
	if _, err := d.CompanionAct(ctx, "body", CompanionActRequest{Gesture: "phone_putdown"}); err != nil {
		t.Fatal(err)
	}
	if snap := d.companionPhone.Snapshot(); snap.Posture != CompanionPostureLaptop || snap.LeaseOwner != "" {
		t.Fatalf("after act phone_putdown: %+v", snap)
	}

	// Ownerless motion only passes phone gestures that agree with the posture.
	seqBefore, _ = latestCompanionGesture(t, d)
	d.CompanionMotion("*", "phone_typing", "", 0)
	if seq, gesture := latestCompanionGesture(t, d); seq > seqBefore && gesture != "" {
		t.Fatalf("ownerless phone_typing on the desk passed as %q", gesture)
	}
}

func TestCompanionPhonePutdownAlwaysEmitsGesture(t *testing.T) {
	d := &Daemon{}
	seqBefore, _ := latestCompanionGesture(t, d)
	if _, err := d.CompanionPhonePutdown("body", CompanionPutdownNormal, CompanionPutdownNextAmbient); err != nil {
		t.Fatal(err)
	}
	seq, gesture := latestCompanionGesture(t, d)
	if seq <= seqBefore || gesture != "phone_putdown" {
		t.Fatalf("no-op putdown gesture = %q (seq %d -> %d), want phone_putdown", gesture, seqBefore, seq)
	}
}

func TestCompanionSnapshotEpochAndSeqMonotonic(t *testing.T) {
	var s CompanionPhoneStore
	a := s.Snapshot()
	if a.Epoch == 0 {
		t.Fatal("snapshot carries no epoch")
	}
	s.EnqueueUserMessage("x")
	b := s.Snapshot()
	if b.Epoch != a.Epoch || b.Seq <= a.Seq {
		t.Fatalf("epoch/seq a=%d/%d b=%d/%d", a.Epoch, a.Seq, b.Epoch, b.Seq)
	}
}

func TestCompanionMessengerPresenceRetainsHoldOnTurnEnd(t *testing.T) {
	now := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	d := &Daemon{}
	d.companionPhone.now = func() time.Time { return now }
	if _, err := d.CompanionPhonePickup("body"); err != nil {
		t.Fatal(err)
	}
	if !d.CompanionMessengerPresence("tab-1", true) {
		t.Fatal("presence open not recorded")
	}
	if !d.companionPhone.Snapshot().MessengerOpen {
		t.Fatal("snapshot does not report messengerOpen")
	}
	if released := d.CompanionReleaseOnTurnEnd("body"); released {
		t.Fatal("turn end released the phone while the messenger is open")
	}
	if snap := d.companionPhone.Snapshot(); snap.Posture != CompanionPosturePhone || snap.LeaseOwner != "body" {
		t.Fatalf("hold not kept: %+v", snap)
	}
	// The idle deadline slides while the messenger stays open.
	now = now.Add(CompanionLeaseIdleTimeout + time.Second)
	d.CompanionMessengerPresence("tab-1", true) // heartbeat
	if leaseExpired, _, _ := d.CompanionExpireIfPastDeadline(now, false); leaseExpired {
		t.Fatal("lease expired while the messenger is open")
	}
	// Force release (close/crash) still releases.
	if !d.CompanionForceRelease("body") {
		t.Fatal("force release kept the hold")
	}

	// With the messenger closed, turn end releases.
	if _, err := d.CompanionPhonePickup("body"); err != nil {
		t.Fatal(err)
	}
	d.CompanionMessengerPresence("tab-1", false)
	if !d.CompanionReleaseOnTurnEnd("body") {
		t.Fatal("turn end kept the phone with the messenger closed")
	}
}

func TestCompanionMessengerPresenceStaleExpiry(t *testing.T) {
	now := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	var s CompanionPhoneStore
	s.now = func() time.Time { return now }
	s.SetMessengerPresence("a", true)
	s.SetMessengerPresence("b", true)
	s.SetMessengerPresence("a", false)
	if !s.MessengerOpen() {
		t.Fatal("ref count: closing one of two clients closed the messenger")
	}
	now = now.Add(companionMessengerPresenceTTL + time.Second)
	if s.MessengerOpen() {
		t.Fatal("stale presence did not expire")
	}
}

func TestCompanionMessengerCloseNotifiesOnceAfterDebounce(t *testing.T) {
	d := &Daemon{}
	d.SetCompanionMessengerCloseDebounce(20 * time.Millisecond)
	var calls atomic.Int32
	got := make(chan string, 4)
	d.SetCompanionMessengerClosed(func(holder string) {
		calls.Add(1)
		got <- holder
	})

	// Close while not holding: no notification.
	d.CompanionMessengerPresence("tab", true)
	d.CompanionMessengerPresence("tab", false)

	// Close then quick reopen while holding: debounce absorbs it.
	if _, err := d.CompanionPhonePickup("body"); err != nil {
		t.Fatal(err)
	}
	d.CompanionMessengerPresence("tab", true)
	d.CompanionMessengerPresence("tab", false)
	d.CompanionMessengerPresence("tab", true)
	time.Sleep(60 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Fatalf("notifications after close+reopen = %d, want 0", n)
	}

	// A real close while holding notifies once with the holder.
	d.CompanionMessengerPresence("tab", false)
	select {
	case holder := <-got:
		if holder != "body" {
			t.Fatalf("holder = %q, want body", holder)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no close notification")
	}
	time.Sleep(40 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("notifications = %d, want 1", n)
	}
}

func TestCompanionMessengerPresenceRoute(t *testing.T) {
	d := &Daemon{}
	mux := http.NewServeMux()
	d.registerCompanionPhoneRoutes(mux)
	post := func(body, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, companionMessengerPresenceRoute, bytes.NewBufferString(body))
		req.RemoteAddr = "127.0.0.1:43210"
		req.Host = "127.0.0.1:8080"
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "text/plain;charset=UTF-8") // sendBeacon
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(`{"open":true,"client":"c1"}`, "https://evil.example"); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin presence = %d, want 403", rec.Code)
	}
	if rec := post(`{"open":true,"client":"c1"}`, "http://127.0.0.1:8080"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"open":true`) {
		t.Fatalf("open presence = %d %s", rec.Code, rec.Body.String())
	}
	if !d.CompanionMessengerOpen() {
		t.Fatal("route did not record presence")
	}
	if rec := post(`{"open":false,"client":"c1"}`, "http://127.0.0.1:8080"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"open":false`) {
		t.Fatalf("close presence = %d %s", rec.Code, rec.Body.String())
	}
}

func TestCompanionMigrateLeaseKeepsHold(t *testing.T) {
	var s CompanionPhoneStore
	if _, _, err := s.PhonePickup("old"); err != nil {
		t.Fatal(err)
	}
	if !s.MigrateLease("old", "new") {
		t.Fatal("lease did not migrate")
	}
	snap := s.Snapshot()
	if snap.LeaseOwner != "new" || snap.Posture != CompanionPosturePhone {
		t.Fatalf("after migrate: %+v", snap)
	}
	if s.MigrateLease("old", "x") {
		t.Fatal("migrated a lease the source no longer holds")
	}
}

func TestCompanionMessengerRingPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	open := func() (*Daemon, *companionMessengerRing) {
		d := &Daemon{dir: dir}
		d.openCompanionMessengerRing()
		if d.messengerRing == nil {
			t.Fatal("messenger ring did not open")
		}
		return d, d.messengerRing
	}

	d1, _ := open()
	d1.companionPhone.EnqueueUserMessage("first message")
	if _, err := d1.CompanionPhonePickup("body"); err != nil {
		t.Fatal(err)
	}
	if _, err := d1.CompanionPhoneSend("body", "a reply", "happy"); err != nil {
		t.Fatal(err)
	}
	if _, err := d1.CompanionPhonePutdown("body", CompanionPutdownNormal, CompanionPutdownNextAmbient); err != nil {
		t.Fatal(err)
	}
	before := d1.companionPhone.Snapshot()
	d1.closeCompanionMessengerRing()

	d2, _ := open()
	defer d2.closeCompanionMessengerRing()
	after := d2.companionPhone.Snapshot()
	if len(after.Messages) != 2 || after.Messages[0].Text != "first message" || after.Messages[1].Text != "a reply" {
		t.Fatalf("hydrated messages = %+v", after.Messages)
	}
	if len(after.Tier1Summary) != len(before.Tier1Summary) || len(after.Tier1Summary) == 0 {
		t.Fatalf("hydrated summaries = %v, want %v", after.Tier1Summary, before.Tier1Summary)
	}
	if after.UnreadAgent != 0 || after.UnreadClient != 0 {
		t.Fatalf("hydrated unread = agent %d client %d, want 0/0", after.UnreadAgent, after.UnreadClient)
	}
	// Message ids keep counting from the persisted sequence: no collision.
	msg, _, _ := d2.companionPhone.EnqueueUserMessage("after restart")
	for _, m := range after.Messages {
		if m.ID == msg.ID {
			t.Fatalf("new message id %s collides with hydrated history", msg.ID)
		}
	}
}

func TestCompanionMessengerRingTornTailAndRetention(t *testing.T) {
	dir := t.TempDir()
	ring, _, err := openCompanionMessengerRing(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := CompanionPhoneMessage{ID: "cm-1", Sender: "user", Text: "kept"}
	ring.append(companionMessengerRecord{Type: companionRingMessage, Message: &m, MsgSeq: 1})
	ring.close()

	segs, err := companionRingSegments(dir)
	if err != nil || len(segs) == 0 {
		t.Fatalf("segments = %v, %v", segs, err)
	}
	last := filepath.Join(dir, companionRingSegmentName(segs[len(segs)-1]))
	f, err := os.OpenFile(last, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"msg","message":{"id":"cm-2","sender":"us`) // torn, no newline
	f.Close()

	const segCap = 32 << 10
	ring, hist, err := openCompanionMessengerRingCap(dir, nil, segCap)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist.Messages) != 1 || hist.Messages[0].Text != "kept" || hist.MsgSeq != 1 {
		t.Fatalf("recovered history = %+v", hist)
	}
	segs, _ = companionRingSegments(dir)
	burstStart := segs[len(segs)-1]
	firstSeen := segs[0]

	// Rotation: a delta stream past the (lowered) cap rolls segments mid-run,
	// in separate flushes and within one large batch; old ones are deleted.
	big := strings.Repeat("x", 200)
	for i := 0; i < 400; i++ {
		msg := CompanionPhoneMessage{ID: fmt.Sprintf("cm-x%d", i), Sender: "user", Text: big}
		ring.append(companionMessengerRecord{Type: companionRingMessage, Message: &msg, MsgSeq: uint64(2 + i)})
		if i < 200 && i%10 == 9 {
			waitRingDrained(t, ring)
		}
	}
	ring.close()
	segs, _ = companionRingSegments(dir)
	if len(segs) > companionRingKeepSegments {
		t.Fatalf("segments after rotation = %d, want <= %d", len(segs), companionRingKeepSegments)
	}
	if newest := segs[len(segs)-1]; newest < burstStart+2 {
		t.Fatalf("newest segment %d, want rotation past %d (at least two rotations)", newest, burstStart)
	}
	if segs[0] <= firstSeen {
		t.Fatalf("oldest segment %d survived retention (first seen %d)", segs[0], firstSeen)
	}
	for _, n := range segs {
		path := filepath.Join(dir, companionRingSegmentName(n))
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		line, _, _ := strings.Cut(string(raw), "\n")
		var rec companionMessengerRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.Type != companionRingSnapshot {
			t.Fatalf("segment %d does not open with a snapshot: %.80q", n, line)
		}
	}
	ring, hist, err = openCompanionMessengerRingCap(dir, nil, segCap)
	if err != nil {
		t.Fatal(err)
	}
	defer ring.close()
	if len(hist.Messages) != companionPhoneMaxMessages || hist.MsgSeq != 401 {
		t.Fatalf("history after rotation: %d messages, msgSeq %d", len(hist.Messages), hist.MsgSeq)
	}
	if got := hist.Messages[len(hist.Messages)-1].ID; got != "cm-x399" {
		t.Fatalf("newest recovered message = %s, want cm-x399", got)
	}
}

// waitRingDrained waits until the writer has taken every queued record.
func waitRingDrained(t *testing.T, r *companionMessengerRing) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		n := len(r.pending)
		r.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("ring writer never drained")
}

func TestCompanionMessengerDigest(t *testing.T) {
	d := &Daemon{}
	d.companionPhone.EnqueueUserMessage("hello there")
	d.CompanionPhonePickup("body")
	d.CompanionPhoneSend("body", "hi!", "")
	d.CompanionPhonePutdown("body", CompanionPutdownNormal, CompanionPutdownNextAmbient)
	digest := d.CompanionMessengerDigest(6)
	if !strings.Contains(digest, "user: hello there") || !strings.Contains(digest, "you: hi!") || !strings.Contains(digest, "Recent messenger exchanges") {
		t.Fatalf("digest = %q", digest)
	}
}
