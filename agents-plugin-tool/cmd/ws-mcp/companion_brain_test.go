package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/mcp"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

func TestCompanionBrainEnsureSeedAndCrossHarnessHandoff(t *testing.T) {
	f := newCompanionFixture(t, "agent")
	f.daemon.SetCompanionBrain(f.rt.BrainEndpoint)

	// Seed legacy Messenger history in memory before first Brain launch.
	if _, _, err := f.daemon.CompanionPhone().EnqueueUserMessage("remember the crimson kite"); err != nil {
		t.Fatal(err)
	}

	// 1. First GET /api/companion/brain lazily launches companion-work Brain with
	// legacy seed and registers Brain replyID on daemon.
	info, err := f.rt.EnsureBrainSession(f.daemon)
	if err != nil {
		t.Fatalf("EnsureBrainSession: %v", err)
	}
	if !info.Ready || info.ReplyID == "" || info.WSKey == "" {
		t.Fatalf("brain info = %+v", info)
	}
	if !info.SeededFromLegacy {
		t.Fatalf("expected SeededFromLegacy=true on brand-new Brain with legacy messages")
	}
	if got := f.daemon.CompanionBrainReplyID(); got != info.ReplyID {
		t.Fatalf("daemon CompanionBrainReplyID = %q, want %q", got, info.ReplyID)
	}

	// Verify record is unlisted and has CompanionRoleWork.
	rec, ok := f.agents.CompanionRecord(info.ReplyID)
	if !ok || rec.Listed || rec.CompanionRole != mcp.CompanionRoleWork {
		t.Fatalf("brain record = %+v (ok=%v)", rec, ok)
	}
	// Verify launched instructions carry both Full Lead framing and legacy seed + Persona.
	if !strings.Contains(rec.CompanionSystemPrompt, "Companion Brain Role") || !strings.Contains(rec.CompanionSystemPrompt, "remember the crimson kite") {
		t.Fatalf("brain prompt missing role or legacy seed: %q", rec.CompanionSystemPrompt)
	}

	// 2. Second EnsureBrainSession reuses the exact same Brain without relaunching.
	info2, err := f.rt.EnsureBrainSession(f.daemon)
	if err != nil {
		t.Fatalf("second EnsureBrainSession: %v", err)
	}
	if info2.ReplyID != info.ReplyID {
		t.Fatalf("second EnsureBrainSession launched new replyID %q != %q", info2.ReplyID, info.ReplyID)
	}
	_, workLaunches := f.rt.launchCounts()
	if workLaunches != 1 {
		t.Fatalf("workLaunches = %d, want 1", workLaunches)
	}

	// 3. Brain has full Lead memory tools (remember_soul, remember_episode, recall_memory)
	// and refuses companion.act.
	txt, isErr, rpcErr := f.callTool(info.WSKey, "companion.remember_soul", map[string]any{
		"session_key": info.WSKey,
		"content":     "Prefers concise diffs",
	})
	if rpcErr != "" || isErr || !strings.Contains(txt, "saved") {
		t.Fatalf("brain remember_soul = (%q, isErr=%v, rpcErr=%q)", txt, isErr, rpcErr)
	}
	_, _, actRPCErr := f.callTool(info.WSKey, "companion.act", map[string]any{
		"session_key": info.WSKey,
		"expression":  "happy",
	})
	if actRPCErr == "" {
		t.Fatalf("brain unexpectedly allowed to call companion.act")
	}

	// 4. Changing configured work tier triggers a clean cross-harness/model handoff
	// (CompanionHandoffCheckpoint + CreateLead + SetCompanionBrainReplyID + CloseConversation).
	f.rt.resolveLaunch = func(role string) (companionLaunchSpec, bool) {
		return companionLaunchSpec{Backend: wsagent.BackendClaude, Model: "claude-opus-4-6"}, true
	}
	info3, err := f.rt.EnsureBrainSession(f.daemon)
	if err != nil {
		t.Fatalf("handoff EnsureBrainSession: %v", err)
	}
	if !info3.Ready || info3.ReplyID == "" || info3.ReplyID == info.ReplyID {
		t.Fatalf("expected new brain replyID after spec change, got %+v (old=%s)", info3, info.ReplyID)
	}
	if got := f.daemon.CompanionBrainReplyID(); got != info3.ReplyID {
		t.Fatalf("daemon CompanionBrainReplyID after handoff = %q, want %q", got, info3.ReplyID)
	}
	if f.agents.IsConversationOpen(info.ReplyID) {
		t.Fatalf("old brain %s still open after handoff", info.ReplyID)
	}
	newRec, ok := f.agents.CompanionRecord(info3.ReplyID)
	if !ok || !strings.Contains(newRec.CompanionSystemPrompt, "[companion handoff]") {
		t.Fatalf("handed-off brain prompt missing handoff checkpoint: %q", newRec.CompanionSystemPrompt)
	}
	if info3.SeededFromLegacy {
		t.Fatalf("handed-off brain should not re-seed legacy messages")
	}
	f.rt.mu.Lock()
	handoffs := f.rt.workHandoffs
	f.rt.mu.Unlock()
	if handoffs != 1 {
		t.Fatalf("workHandoffs = %d, want 1", handoffs)
	}

	// 5. GET /api/companion/brain HTTP route enforces localBrowseViewer auth and
	// returns the live Brain info for an authenticated local viewer.
	ep := f.daemon.Endpoint()
	brainURL := fmt.Sprintf("http://127.0.0.1:%d/api/companion/brain", ep.Port)
	unauthResp, err := http.Get(brainURL)
	if err != nil {
		t.Fatalf("unauth GET /api/companion/brain: %v", err)
	}
	unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth GET /api/companion/brain status = %d, want 401", unauthResp.StatusCode)
	}
	req, err := http.NewRequest(http.MethodGet, brainURL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.AddCookie(f.authCookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/companion/brain: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/companion/brain status = %d", resp.StatusCode)
	}
	var gotInfo wsdashboard.CompanionBrainInfo
	if err := json.NewDecoder(resp.Body).Decode(&gotInfo); err != nil {
		t.Fatalf("decode /api/companion/brain: %v", err)
	}
	if gotInfo.ReplyID != info3.ReplyID || !gotInfo.Ready {
		t.Fatalf("/api/companion/brain = %+v, want replyId=%s ready=true", gotInfo, info3.ReplyID)
	}

	// 6. Restart reattach adopts the open Brain and registers its replyID without launching a duplicate.
	f.rt.mu.Lock()
	f.rt.workReplyID, f.rt.workWSKey = "", ""
	launchesBefore := f.rt.workLaunches
	f.rt.mu.Unlock()
	f.daemon.SetCompanionBrainReplyID("")
	f.rt.Start()
	waitCompanionUntil(t, 5*time.Second, "brain reattached on Start", func() bool {
		return f.daemon.CompanionBrainReplyID() == info3.ReplyID
	})
	f.rt.mu.Lock()
	launchesAfter := f.rt.workLaunches
	f.rt.mu.Unlock()
	if launchesAfter != launchesBefore {
		t.Fatalf("reattach unexpectedly launched new brain (%d -> %d)", launchesBefore, launchesAfter)
	}
}
