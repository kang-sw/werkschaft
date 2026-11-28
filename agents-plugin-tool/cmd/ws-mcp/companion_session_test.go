package main

import (
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/mcp"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

func (f *companionFixture) advance(d time.Duration) {
	next := f.now.Load().Add(d)
	f.now.Store(&next)
}

func (f *companionFixture) workIDs() (string, string) {
	f.rt.mu.Lock()
	defer f.rt.mu.Unlock()
	return f.rt.workReplyID, f.rt.workWSKey
}

func TestCompanionBrainReattachAdoptsNewestAndClosesDuplicates(t *testing.T) {
	f := newCompanionFixture(t, "agent")

	info, err := f.rt.EnsureBrainSession(f.daemon)
	if err != nil {
		t.Fatalf("EnsureBrainSession: %v", err)
	}
	firstID := info.ReplyID

	// A stale duplicate Brain left behind by a previous daemon (newer record).
	f.advance(time.Minute)
	dup, err := f.agents.CreateLead(wsagent.BackendClaude, f.root, wsagent.LeadOptions{
		CompanionRole:         mcp.CompanionRoleWork,
		CompanionSystemPrompt: wsdashboard.BuildCompanionBrainSystemPrompt(wsdashboard.CharacteristicSheet{}, "", ""),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a fresh runtime that has not yet adopted a Brain.
	f.rt.mu.Lock()
	f.rt.workReplyID, f.rt.workWSKey = "", ""
	launches := f.rt.workLaunches
	f.rt.mu.Unlock()
	f.daemon.SetCompanionBrainReplyID("")
	f.rt.Start()

	waitCompanionUntil(t, 5*time.Second, "reattach adopted newest brain and closed duplicate", func() bool {
		id, _ := f.workIDs()
		return id == dup.ID && !f.agents.IsConversationOpen(firstID) && f.daemon.CompanionBrainReplyID() == dup.ID
	})
	if got := f.agents.CompanionSessions(mcp.CompanionRoleWork); len(got) != 1 || got[0].ReplyID != dup.ID {
		t.Fatalf("open brains after reattach = %+v, want only %s", got, dup.ID)
	}

	// Subsequent EnsureBrainSession reuses the adopted Brain without launching a new one.
	info2, err := f.rt.EnsureBrainSession(f.daemon)
	if err != nil {
		t.Fatalf("second EnsureBrainSession: %v", err)
	}
	if info2.ReplyID != dup.ID {
		t.Fatalf("EnsureBrainSession replyId = %q, want %q", info2.ReplyID, dup.ID)
	}
	f.rt.mu.Lock()
	gotLaunches := f.rt.workLaunches
	f.rt.mu.Unlock()
	if gotLaunches != launches {
		t.Fatalf("work launches = %d, want %d (reattach must not launch)", gotLaunches, launches)
	}
}

func TestCompanionStartTearsDownLegacyBodySessions(t *testing.T) {
	f := newCompanionFixture(t, "agent")
	legacy, err := f.agents.CreateLead(wsagent.BackendClaude, f.root, wsagent.LeadOptions{
		CompanionRole:         mcp.CompanionRoleBody,
		CompanionSystemPrompt: wsdashboard.BuildCompanionBodyLaunchPrompt(wsdashboard.CharacteristicSheet{}, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !f.agents.IsConversationOpen(legacy.ID) {
		t.Fatalf("expected legacy body %s to be open before Start", legacy.ID)
	}

	f.rt.Start()
	waitCompanionUntil(t, 5*time.Second, "legacy companion-body session closed on Start", func() bool {
		return !f.agents.IsConversationOpen(legacy.ID)
	})
	if got := f.agents.CompanionSessions(mcp.CompanionRoleBody); len(got) != 0 {
		t.Fatalf("open legacy bodies after Start = %+v, want 0", got)
	}
}
