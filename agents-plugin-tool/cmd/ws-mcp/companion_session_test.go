package main

import (
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/mcp"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// replyAndPutdown is a body handler that answers every delivery with one
// messenger reply and a putdown, then settles the turn.
func replyAndPutdown(f *companionFixture) func(p *companionTestProc, text string) {
	return func(p *companionTestProc, text string) {
		if p.role == mcp.CompanionRoleBody {
			_, _, _ = f.callTool(p.wsKey, "companion.phone_pickup", map[string]any{"session_key": p.wsKey})
			_, _, _ = f.callTool(p.wsKey, "companion.phone_send", map[string]any{"session_key": p.wsKey, "text": "reply", "tone": "happy"})
			_, _, _ = f.callTool(p.wsKey, "companion.phone_putdown", map[string]any{"session_key": p.wsKey, "style": "normal", "next": "ambient"})
		}
		p.emitTurn("ok", "")
	}
}

func (f *companionFixture) advance(d time.Duration) {
	next := f.now.Load().Add(d)
	f.now.Store(&next)
}

func (f *companionFixture) bodyIDs() (string, string) {
	f.rt.mu.Lock()
	defer f.rt.mu.Unlock()
	return f.rt.bodyReplyID, f.rt.bodyWSKey
}

func TestCompanionBodyReattachAdoptsNewestAndClosesDuplicates(t *testing.T) {
	f := newCompanionFixture(t, "agent")
	f.backend.mu.Lock()
	f.backend.handler = replyAndPutdown(f)
	f.backend.mu.Unlock()

	f.postUserMessage("hello")
	waitCompanionUntil(t, 5*time.Second, "first body turn settled", func() bool {
		id, _ := f.bodyIDs()
		return id != "" && f.daemon.CompanionPhone().Snapshot().LeaseOwner == "" && f.daemon.CompanionPhone().UnreadAgentCount() == 0
	})
	firstID, _ := f.bodyIDs()

	// A stale duplicate body a previous daemon left behind (newer record).
	f.advance(time.Minute)
	dup, err := f.agents.CreateLead(wsagent.BackendClaude, f.root, wsagent.LeadOptions{
		CompanionRole:         mcp.CompanionRoleBody,
		CompanionSystemPrompt: wsdashboard.BuildCompanionBodyLaunchPrompt(wsdashboard.CharacteristicSheet{}, ""),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a fresh runtime that has not yet learned which body is current.
	f.rt.mu.Lock()
	f.rt.bodyReplyID, f.rt.bodyWSKey = "", ""
	launches := f.rt.bodyLaunches
	f.rt.mu.Unlock()
	f.rt.Start()

	waitCompanionUntil(t, 5*time.Second, "reattach adopted the newest body and closed the duplicate", func() bool {
		id, _ := f.bodyIDs()
		return id == dup.ID && !f.agents.IsConversationOpen(firstID)
	})
	if got := f.agents.CompanionSessions(mcp.CompanionRoleBody); len(got) != 1 || got[0].ReplyID != dup.ID {
		t.Fatalf("open bodies after reattach = %+v, want only %s", got, dup.ID)
	}

	// The next wake reuses the adopted body: no new launch.
	f.postUserMessage("again")
	waitCompanionUntil(t, 5*time.Second, "second message read", func() bool {
		return f.daemon.CompanionPhone().UnreadAgentCount() == 0
	})
	f.rt.mu.Lock()
	gotLaunches := f.rt.bodyLaunches
	f.rt.mu.Unlock()
	if gotLaunches != launches {
		t.Fatalf("body launches = %d, want %d (reattach must not launch)", gotLaunches, launches)
	}
}

func TestCompanionBodyHandoffOnSpecChangeCarriesCheckpoint(t *testing.T) {
	f := newCompanionFixture(t, "agent")
	f.backend.mu.Lock()
	f.backend.handler = replyAndPutdown(f)
	f.backend.mu.Unlock()

	f.postUserMessage("remember the blue kite")
	waitCompanionUntil(t, 5*time.Second, "first body turn settled", func() bool {
		id, _ := f.bodyIDs()
		return id != "" && f.daemon.CompanionPhone().UnreadAgentCount() == 0 && f.daemon.CompanionPhone().Snapshot().LeaseOwner == ""
	})
	oldID, _ := f.bodyIDs()
	waitCompanionUntil(t, 5*time.Second, "old body idle", func() bool {
		f.rt.mu.Lock()
		defer f.rt.mu.Unlock()
		return f.rt.bodyPendingTurns == 0
	})

	// The configured body tier changes.
	f.rt.resolveLaunch = func(role string) (companionLaunchSpec, bool) {
		return companionLaunchSpec{Backend: wsagent.BackendClaude, Model: "claude-sonnet-5-5"}, true
	}
	f.postUserMessage("still there?")
	waitCompanionUntil(t, 5*time.Second, "handoff to a new body", func() bool {
		id, _ := f.bodyIDs()
		return id != "" && id != oldID && f.daemon.CompanionPhone().UnreadAgentCount() == 0
	})
	if f.agents.IsConversationOpen(oldID) {
		t.Fatal("handed-off body is still open")
	}
	f.rt.mu.Lock()
	handoffs := f.rt.bodyHandoffs
	f.rt.mu.Unlock()
	if handoffs != 1 {
		t.Fatalf("body handoffs = %d, want 1", handoffs)
	}
	procs := f.backend.procsFor(mcp.CompanionRoleBody)
	last := procs[len(procs)-1]
	if !strings.Contains(last.instructions, "[companion handoff]") || !strings.Contains(last.instructions, "remember the blue kite") {
		t.Fatalf("replacement body launch prompt lacks the handoff checkpoint/digest:\n%s", last.instructions)
	}
	if got := f.agents.CompanionSessions(mcp.CompanionRoleBody); len(got) != 1 {
		t.Fatalf("open bodies after handoff = %d, want 1", len(got))
	}
}

func TestCompanionMessengerOpenKeepsPhoneAndCloseNotifiesBody(t *testing.T) {
	f := newCompanionFixture(t, "agent")
	f.daemon.SetCompanionMessengerCloseDebounce(20 * time.Millisecond)
	f.rt.messengerCloseFallback = 150 * time.Millisecond
	released := make(chan struct{}, 1)
	f.backend.mu.Lock()
	f.backend.handler = func(p *companionTestProc, text string) {
		if p.role != mcp.CompanionRoleBody {
			p.emitTurn("ok", "")
			return
		}
		if strings.Contains(text, "[messenger closed]") {
			// Never puts the phone down and never settles: the fallback must.
			go func() {
				waitCompanionUntil(t, 5*time.Second, "fallback release", func() bool {
					return f.daemon.CompanionPhone().Snapshot().LeaseOwner == ""
				})
				released <- struct{}{}
			}()
			return
		}
		_, _, _ = f.callTool(p.wsKey, "companion.phone_pickup", map[string]any{"session_key": p.wsKey})
		_, _, _ = f.callTool(p.wsKey, "companion.phone_send", map[string]any{"session_key": p.wsKey, "text": "hi", "tone": "happy"})
		// Messenger open: end the turn still holding the phone.
		p.emitTurn("ok", "")
	}
	f.backend.mu.Unlock()

	f.daemon.CompanionMessengerPresence("tab-1", true)
	f.postUserMessage("hello")
	waitCompanionUntil(t, 5*time.Second, "turn settled with the phone kept", func() bool {
		f.rt.mu.Lock()
		pending := f.rt.bodyPendingTurns
		f.rt.mu.Unlock()
		snap := f.daemon.CompanionPhone().Snapshot()
		return pending == 0 && snap.UnreadAgent == 0 && len(snap.Messages) == 2
	})
	_, bodyKey := f.bodyIDs()
	time.Sleep(50 * time.Millisecond)
	if snap := f.daemon.CompanionPhone().Snapshot(); snap.Posture != wsdashboard.CompanionPosturePhone || snap.LeaseOwner != bodyKey {
		t.Fatalf("phone not kept across turn end with the messenger open: %+v", snap)
	}

	f.daemon.CompanionMessengerPresence("tab-1", false)
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("phone never released after the messenger closed")
	}
	procs := f.backend.procsFor(mcp.CompanionRoleBody)
	var notices int
	for _, s := range procs[len(procs)-1].receivedSends() {
		if strings.Contains(s, "[messenger closed]") {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("messenger-closed notices = %d, want exactly 1", notices)
	}
}

func TestCompanionFailedTurnReleasesPhoneEvenWithMessengerOpen(t *testing.T) {
	f := newCompanionFixture(t, "agent")
	f.backend.mu.Lock()
	f.backend.handler = func(p *companionTestProc, text string) {
		if p.role == mcp.CompanionRoleBody {
			_, _, _ = f.callTool(p.wsKey, "companion.phone_pickup", map[string]any{"session_key": p.wsKey})
			_, _, _ = f.callTool(p.wsKey, "companion.phone_send", map[string]any{"session_key": p.wsKey, "text": "hi", "tone": "happy"})
		}
		p.emitTurn("", "boom")
	}
	f.backend.mu.Unlock()

	f.daemon.CompanionMessengerPresence("tab-1", true)
	f.postUserMessage("hello")
	waitCompanionUntil(t, 5*time.Second, "failed turn released the phone", func() bool {
		f.rt.mu.Lock()
		pending := f.rt.bodyPendingTurns
		id := f.rt.bodyReplyID
		f.rt.mu.Unlock()
		snap := f.daemon.CompanionPhone().Snapshot()
		return id != "" && pending == 0 && snap.UnreadAgent == 0 && len(snap.Messages) == 2 && snap.LeaseOwner == ""
	})
	if snap := f.daemon.CompanionPhone().Snapshot(); snap.Posture == wsdashboard.CompanionPosturePhone {
		t.Fatalf("failed turn kept the phone in hand with the messenger open: %+v", snap)
	}
}

func TestCompanionMessengerCloseWithoutHoldIsZeroTurns(t *testing.T) {
	f := newCompanionFixture(t, "agent")
	f.daemon.SetCompanionMessengerCloseDebounce(10 * time.Millisecond)
	f.backend.mu.Lock()
	f.backend.handler = replyAndPutdown(f)
	f.backend.mu.Unlock()
	f.postUserMessage("hello")
	waitCompanionUntil(t, 5*time.Second, "turn settled", func() bool {
		return f.daemon.CompanionPhone().UnreadAgentCount() == 0 && f.daemon.CompanionPhone().Snapshot().LeaseOwner == ""
	})
	procs := f.backend.procsFor(mcp.CompanionRoleBody)
	before := len(procs[0].receivedSends())
	f.daemon.CompanionMessengerPresence("tab", true)
	f.daemon.CompanionMessengerPresence("tab", false)
	time.Sleep(100 * time.Millisecond)
	if after := len(procs[0].receivedSends()); after != before {
		t.Fatalf("closing the messenger without a held phone delivered %d turns", after-before)
	}
}
