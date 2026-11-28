package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

type testCompanionHost struct {
	store       wsdashboard.CompanionPhoneStore
	speaks      []string
	faces       []string
	motions     []string
	acts        []wsdashboard.CompanionActRequest
	actFn       func(ctx context.Context, owner string, req wsdashboard.CompanionActRequest) (wsdashboard.CompanionActResult, error)
	soulCalls   []string
	epCalls     []string
	recallCalls []string
	burstMu     sync.Mutex
	burstCounts map[string]int
	delegations []struct {
		caller string
		brief  string
	}
	reports []struct {
		caller    string
		id        string
		status    string
		result    string
		artifacts []string
	}
}

func (h *testCompanionHost) CompanionAct(ctx context.Context, owner string, req wsdashboard.CompanionActRequest) (wsdashboard.CompanionActResult, error) {
	h.acts = append(h.acts, req)
	if h.actFn != nil {
		return h.actFn(ctx, owner, req)
	}
	return wsdashboard.CompanionActResult{
		WakeReason: wsdashboard.CompanionActWakeTimer,
		DelayMS:    wsdashboard.ClampCompanionActDelayMS(req.DelayMS, req.HasDelay),
	}, nil
}

func (h *testCompanionHost) CompanionRememberSoul(_ context.Context, _, content, _, _ string, _ []string) (wsdashboard.RememberSoulResult, error) {
	h.soulCalls = append(h.soulCalls, content)
	return wsdashboard.RememberSoulResult{
		Status: "saved",
		Entry:  &wsdashboard.SoulShardEntry{ID: "soul-1", Content: content},
	}, nil
}

func (h *testCompanionHost) CompanionRememberEpisode(_ context.Context, _, projectKey, summary string, tags []string) (wsdashboard.CompanionEpisode, error) {
	h.epCalls = append(h.epCalls, projectKey+":"+summary)
	return wsdashboard.CompanionEpisode{ID: 7, ProjectKey: projectKey, Summary: summary, Tags: tags}, nil
}

func (h *testCompanionHost) CompanionRecallMemory(_ context.Context, query, scope, projectKey string, _ int) (wsdashboard.CompanionRecallResult, error) {
	if scope == "" {
		scope = "all"
	}
	h.recallCalls = append(h.recallCalls, scope+":"+projectKey+":"+query)
	return wsdashboard.CompanionRecallResult{
		Scope:      scope,
		ProjectKey: projectKey,
		Query:      query,
		Snippets: []wsdashboard.CompanionRecallSnippet{
			{Source: "soul:rapport", Timestamp: "2026-11-28T05:00:00Z", Content: "Prefers short summaries"},
		},
	}, nil
}

func (h *testCompanionHost) CompanionConsumeBurstCall(owner string, limit int) (int, bool) {
	h.burstMu.Lock()
	defer h.burstMu.Unlock()
	if h.burstCounts == nil {
		h.burstCounts = make(map[string]int)
	}
	cur := h.burstCounts[owner]
	if cur >= limit {
		return cur, false
	}
	cur++
	h.burstCounts[owner] = cur
	return cur, true
}

func (h *testCompanionHost) CompanionResetBurst(owner string) {
	h.burstMu.Lock()
	defer h.burstMu.Unlock()
	if h.burstCounts != nil {
		delete(h.burstCounts, owner)
	}
}

func (h *testCompanionHost) CompanionPosture(owner string) string {
	return h.store.PostureFor(owner)
}

func (h *testCompanionHost) CompanionUnreadPhoneCount() int {
	return h.store.UnreadAgentCount()
}

func (h *testCompanionHost) CompanionSpeak(_ string, text string, _ int) bool {
	h.speaks = append(h.speaks, text)
	return true
}

func (h *testCompanionHost) CompanionFace(_ string, expression string, _ int) bool {
	h.faces = append(h.faces, expression)
	return true
}

func (h *testCompanionHost) CompanionMotion(_ string, gesture, prop string, _ int) bool {
	h.motions = append(h.motions, gesture+":"+prop)
	return true
}

func (h *testCompanionHost) CompanionPhonePickup(owner string) (wsdashboard.CompanionPhoneReadResult, error) {
	res, _, err := h.store.PhonePickup(owner)
	return res, err
}

func (h *testCompanionHost) CompanionPhoneRead(owner string) (wsdashboard.CompanionPhoneReadResult, error) {
	res, _, err := h.store.PhoneRead(owner)
	return res, err
}

func (h *testCompanionHost) CompanionPhoneSend(owner, text, tone string) (wsdashboard.CompanionPhoneMessage, error) {
	msg, _, err := h.store.PhoneSend(owner, text, tone, nil)
	return msg, err
}

func (h *testCompanionHost) CompanionPhonePutdown(owner, style, next string) (wsdashboard.CompanionPhoneSnapshot, error) {
	snap, _, err := h.store.PhonePutdown(owner, style, next)
	return snap, err
}

func (h *testCompanionHost) CompanionReleaseOnTurnEnd(owner string) bool {
	_, changed := h.store.ReleaseOnTurnEnd(owner)
	return changed
}

func (h *testCompanionHost) CompanionDelegateWork(callerKey, brief string) (CompanionDelegationAccepted, error) {
	h.delegations = append(h.delegations, struct {
		caller string
		brief  string
	}{caller: callerKey, brief: brief})
	return CompanionDelegationAccepted{DelegationID: "del-1", Status: "accepted"}, nil
}

func (h *testCompanionHost) CompanionDelegationReport(callerKey, delegationID, status, result string, artifacts []string) (CompanionDelegationReportAck, error) {
	h.reports = append(h.reports, struct {
		caller    string
		id        string
		status    string
		result    string
		artifacts []string
	}{caller: callerKey, id: delegationID, status: status, result: result, artifacts: artifacts})
	return CompanionDelegationReportAck{DelegationID: delegationID, Status: status}, nil
}

func invokeCompanionTestToolRaw(t *testing.T, s *Server, name string, args map[string]any) response {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return s.callTool(context.Background(), request{
		ID:     json.RawMessage(`1`),
		Method: "tools/call",
		Params: raw,
	})
}

func invokeCompanionTestTool(t *testing.T, s *Server, name string, args map[string]any) (string, bool) {
	t.Helper()
	resp := invokeCompanionTestToolRaw(t, s, name, args)
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error calling %s: %+v", name, resp.Error)
	}
	res, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T for %s", resp.Result, name)
	}
	isErr, _ := res["isError"].(bool)
	content, ok := res["content"].([]map[string]string)
	if !ok || len(content) == 0 {
		t.Fatalf("unexpected content shape for %s: %#v", name, res["content"])
	}
	return content[0]["text"], isErr
}

func discoveryToolNames(tools []map[string]any) map[string]bool {
	out := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if name, ok := tool["name"].(string); ok {
			out[name] = true
		}
	}
	return out
}

func mintCompanionTestKey(t *testing.T, s *Server, root, role string) string {
	t.Helper()
	key, err := s.sessions.mintWithCompanionRole(root, roleLead, "", "", role)
	if err != nil {
		t.Fatalf("mintWithCompanionRole(%q): %v", role, err)
	}
	return key
}

func TestCompanionPostureLockDirectAndDiscovery(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	host := &testCompanionHost{}
	if _, _, err := host.store.EnqueueUserMessage("hey check your phone"); err != nil {
		t.Fatalf("EnqueueUserMessage: %v", err)
	}

	s := NewServer(root, "test")
	s.descriptor = connectionDescriptor{callFallback: true}
	s.companion = host
	bodyKey := mintCompanionTestKey(t, s, root, CompanionRoleBody)
	bodyEntry, ok := s.lookupSession(bodyKey)
	if !ok || bodyEntry.companionRole != CompanionRoleBody {
		t.Fatalf("lookup companion-body key failed: ok=%v entry=%+v", ok, bodyEntry)
	}

	// Deprecated legacy phone/delegation tools stay registered as T0 no-op
	// aliases: they never block on posture leases or mutate the phone store.
	laptopDisc := discoveryToolNames(s.callDiscoveryTools(bodyKey, bodyEntry, true))
	if !laptopDisc[companionPhonePickupTool] || !laptopDisc[companionPhoneReadTool] || !laptopDisc[companionPhoneSendTool] || !laptopDisc[companionDelegateWorkTool] || !laptopDisc["runtime.read"] {
		t.Fatalf("callDiscoveryTools missing expected laptop tools: pickup=%v read=%v send=%v delegate=%v runtime.read=%v",
			laptopDisc[companionPhonePickupTool], laptopDisc[companionPhoneReadTool], laptopDisc[companionPhoneSendTool], laptopDisc[companionDelegateWorkTool], laptopDisc["runtime.read"])
	}
	// Tier-1 base allowlist must never expose lead-level mutating tools at any posture ((1740)).
	for _, forbidden := range []string{"config.tune", "git.commit", "git.merge", "tickets.close", "exec.shell", "agent.spawn", delegationReportTool} {
		if laptopDisc[forbidden] {
			t.Fatalf("companion-body exposed forbidden mutating/work tool %q on laptop posture", forbidden)
		}
		resp := invokeCompanionTestToolRaw(t, s, forbidden, map[string]any{"session_key": bodyKey})
		if resp.Error == nil {
			t.Fatalf("companion-body reached forbidden tool %q without RPC profile error: %+v", forbidden, resp)
		}
	}

	for _, tool := range []string{companionPhonePickupTool, companionPhoneReadTool, companionPhoneSendTool, companionPhonePutdownTool, companionDelegateWorkTool} {
		args := map[string]any{"session_key": bodyKey}
		if tool == companionPhoneSendTool {
			args["text"] = "legacy message"
		}
		if tool == companionDelegateWorkTool {
			args["brief"] = "legacy brief"
		}
		txt, isErr := invokeCompanionTestTool(t, s, tool, args)
		if isErr || !strings.Contains(txt, `"deprecated":true`) || !strings.Contains(txt, `"notice"`) {
			t.Fatalf("callTool(%s) = (%q, isErr=%v), want deprecated T0 alias response", tool, txt, isErr)
		}
		txt, isErr = invokeCompanionTestTool(t, s, "call", map[string]any{"tool": tool, "arguments": args})
		if isErr || !strings.Contains(txt, `"deprecated":true`) {
			t.Fatalf("call(%s) = (%q, isErr=%v), want deprecated T0 alias response", tool, txt, isErr)
		}
	}
	// Legacy phone_pickup alias must not acquire a lease.
	if host.store.PostureFor(bodyKey) != wsdashboard.CompanionPostureLaptop {
		t.Fatalf("deprecated phone_pickup unexpectedly acquired phone posture")
	}

	// Cosmetic companion tools work in laptop posture.
	if txt, isErr := invokeCompanionTestTool(t, s, companionSpeakTool, map[string]any{"session_key": bodyKey, "text": "working!"}); isErr || !strings.Contains(txt, "ok") {
		t.Fatalf("companion.speak on laptop = (%q, isErr=%v)", txt, isErr)
	}
	if txt, isErr := invokeCompanionTestTool(t, s, companionFaceTool, map[string]any{"session_key": bodyKey, "expression": "happy"}); isErr || !strings.Contains(txt, "ok") {
		t.Fatalf("companion.face on laptop = (%q, isErr=%v)", txt, isErr)
	}
	if txt, isErr := invokeCompanionTestTool(t, s, companionMotionTool, map[string]any{"session_key": bodyKey, "gesture": "wave"}); isErr || !strings.Contains(txt, "ok") {
		t.Fatalf("companion.motion on laptop = (%q, isErr=%v)", txt, isErr)
	}

	// If a direct store-level phone lease is held, non-deprecated laptop tools
	// still observe posture_locked while deprecated aliases remain non-blocking.
	if _, _, err := host.store.PhonePickup(bodyKey); err != nil {
		t.Fatalf("store.PhonePickup: %v", err)
	}
	phoneDisc := discoveryToolNames(s.callDiscoveryTools(bodyKey, bodyEntry, true))
	if phoneDisc["runtime.read"] || phoneDisc["git.status"] || phoneDisc["tickets.query"] {
		t.Fatalf("callDiscoveryTools exposed work tools while holding phone: %+v", phoneDisc)
	}
	if !phoneDisc[companionPhoneReadTool] || !phoneDisc[companionPhoneSendTool] || !phoneDisc[companionPhonePutdownTool] || !phoneDisc["mailbox.recv"] {
		t.Fatalf("callDiscoveryTools missing phone/mailbox tools while holding phone")
	}
	for _, blocked := range []struct {
		tool string
		args map[string]any
	}{
		{"runtime.read", map[string]any{"session_key": bodyKey}},
		{"git.status", map[string]any{"session_key": bodyKey}},
		{"tickets.query", map[string]any{"session_key": bodyKey}},
	} {
		// runtime.read does not declare session_key; bind hosted key for it.
		if blocked.tool == "runtime.read" {
			s.hostedKey = bodyKey
			blocked.args = map[string]any{}
		} else {
			s.hostedKey = ""
		}
		txt, isErr := invokeCompanionTestTool(t, s, blocked.tool, blocked.args)
		if !isErr || !strings.Contains(txt, `"posture_locked"`) || !strings.Contains(txt, companionPhonePutdownTool) {
			t.Fatalf("callTool(%s) on phone = (%q, isErr=%v), want posture_locked naming %s", blocked.tool, txt, isErr, companionPhonePutdownTool)
		}
		txt, isErr = invokeCompanionTestTool(t, s, "call", map[string]any{"tool": blocked.tool, "arguments": blocked.args})
		if !isErr || !strings.Contains(txt, `"posture_locked"`) || !strings.Contains(txt, companionPhonePutdownTool) {
			t.Fatalf("call(%s) on phone = (%q, isErr=%v), want posture_locked naming %s", blocked.tool, txt, isErr, companionPhonePutdownTool)
		}
	}
	s.hostedKey = ""
	if txt, isErr := invokeCompanionTestTool(t, s, companionDelegateWorkTool, map[string]any{"session_key": bodyKey, "brief": "legacy"}); isErr || !strings.Contains(txt, `"deprecated":true`) {
		t.Fatalf("deprecated delegate_work while phone held = (%q, isErr=%v), want non-blocking deprecated alias", txt, isErr)
	}
	if len(host.delegations) != 0 {
		t.Fatalf("deprecated delegate_work unexpectedly called delegation host: %+v", host.delegations)
	}
}

func TestCompanionLegacyToolsDeprecatedT0Aliases(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	// Nil companion and delegation hosts: deprecated aliases still succeed
	// without unknown-tool errors or host/lease dependencies.
	s := NewServer(root, "test")
	bodyKey := mintCompanionTestKey(t, s, root, CompanionRoleBody)
	workKey := mintCompanionTestKey(t, s, root, CompanionRoleWork)

	cases := []struct {
		tool string
		key  string
		args map[string]any
	}{
		{companionPhonePickupTool, bodyKey, map[string]any{"session_key": bodyKey}},
		{companionPhoneReadTool, bodyKey, map[string]any{"session_key": bodyKey}},
		{companionPhoneSendTool, bodyKey, map[string]any{"session_key": bodyKey, "text": "hello"}},
		{companionPhonePutdownTool, bodyKey, map[string]any{"session_key": bodyKey, "style": "normal", "next": "ambient"}},
		{companionDelegateWorkTool, bodyKey, map[string]any{"session_key": bodyKey, "brief": "task"}},
		{delegationReportTool, workKey, map[string]any{"session_key": workKey, "delegation_id": "del-1", "status": "done", "result": "ok"}},
	}
	for _, tc := range cases {
		txt, isErr := invokeCompanionTestTool(t, s, tc.tool, tc.args)
		if isErr {
			t.Fatalf("%s returned isError=true: %s", tc.tool, txt)
		}
		var payload struct {
			Deprecated bool   `json:"deprecated"`
			Tool       string `json:"tool"`
			Notice     string `json:"notice"`
		}
		if err := json.Unmarshal([]byte(txt), &payload); err != nil {
			t.Fatalf("%s returned non-JSON %q: %v", tc.tool, txt, err)
		}
		if !payload.Deprecated || payload.Tool != tc.tool || strings.TrimSpace(payload.Notice) == "" {
			t.Fatalf("%s payload = %+v, want deprecated=true tool=%s non-empty notice", tc.tool, payload, tc.tool)
		}
	}
}

func TestCompanionMarkerScopingRegression(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	host := &testCompanionHost{}
	if _, _, err := host.store.EnqueueUserMessage("ping 1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := host.store.EnqueueUserMessage("ping 2"); err != nil {
		t.Fatal(err)
	}

	s := NewServer(root, "test")
	s.descriptor = connectionDescriptor{callFallback: true}
	s.companion = host

	leadKey := mintCompanionTestKey(t, s, root, "")
	leadEntry, _ := s.lookupSession(leadKey)
	bodyKey := mintCompanionTestKey(t, s, root, CompanionRoleBody)
	bodyEntry, _ := s.lookupSession(bodyKey)
	workKey := mintCompanionTestKey(t, s, root, CompanionRoleWork)
	workEntry, _ := s.lookupSession(workKey)

	// (m1) Unmarked lead: never receives [phone buzz: N unread], never posture-locked
	// even while companion-body holds the phone, and cannot list or reach phone_* or delegate_work.
	if _, _, err := host.store.PhonePickup(bodyKey); err != nil {
		t.Fatalf("body PhonePickup: %v", err)
	}
	if _, _, err := host.store.EnqueueUserMessage("ping 3 while held"); err != nil {
		t.Fatal(err)
	}

	s.hostedKey = leadKey
	txt, isErr := invokeCompanionTestTool(t, s, "runtime.read", map[string]any{})
	if isErr || strings.Contains(txt, "[phone buzz:") || strings.Contains(txt, "posture_locked") {
		t.Fatalf("(m1) unmarked lead runtime.read leaked buzz or posture lock: (%q, isErr=%v)", txt, isErr)
	}
	leadDisc := discoveryToolNames(s.callDiscoveryTools(leadKey, leadEntry, true))
	for _, hidden := range []string{companionPhonePickupTool, companionPhoneReadTool, companionPhoneSendTool, companionPhonePutdownTool, companionDelegateWorkTool, delegationReportTool} {
		if leadDisc[hidden] {
			t.Fatalf("(m1) unmarked lead discovery exposed %q", hidden)
		}
		resp := invokeCompanionTestToolRaw(t, s, hidden, map[string]any{"session_key": leadKey})
		if resp.Error == nil {
			t.Fatalf("(m1) unmarked lead reached %q: %+v", hidden, resp)
		}
	}
	// Keyless caller must also not inherit phone ownership via any "lead" fallback.
	s.hostedKey = ""
	for _, hidden := range []string{companionPhonePickupTool, companionPhoneReadTool, companionPhoneSendTool, companionPhonePutdownTool, companionDelegateWorkTool} {
		resp := invokeCompanionTestToolRaw(t, s, hidden, map[string]any{})
		if resp.Error == nil {
			t.Fatalf("(m1) keyless caller reached %q: %+v", hidden, resp)
		}
	}

	// (m3) Unmarked lead keeps companion.{speak,face,motion} as landed.
	if !leadDisc[companionSpeakTool] || !leadDisc[companionFaceTool] || !leadDisc[companionMotionTool] {
		t.Fatalf("(m3) unmarked lead missing speak/face/motion in discovery")
	}
	if txt, isErr := invokeCompanionTestTool(t, s, companionSpeakTool, map[string]any{"session_key": leadKey, "text": "hello lead"}); isErr || !strings.Contains(txt, "ok") {
		t.Fatalf("(m3) unmarked lead companion.speak failed: (%q, isErr=%v)", txt, isErr)
	}

	// (m2) companion-body gets [phone buzz: N unread] on non-JSON replies and NOT on JSON replies.
	if _, _, err := host.store.PhonePutdown(bodyKey, wsdashboard.CompanionPutdownNormal, wsdashboard.CompanionPutdownNextAmbient); err != nil {
		t.Fatal(err)
	}
	s.hostedKey = bodyKey
	txt, isErr = invokeCompanionTestTool(t, s, "runtime.read", map[string]any{})
	if isErr || !strings.Contains(txt, "[phone buzz: 1 unread]") {
		t.Fatalf("(m2) companion-body expected [phone buzz: 1 unread], got: %q (isErr=%v)", txt, isErr)
	}
	jsonTxt, isErr := invokeCompanionTestTool(t, s, "runtime.read", map[string]any{"format": "json"})
	if isErr || strings.Contains(jsonTxt, "[phone buzz:") {
		t.Fatalf("(m2) companion-body JSON reply must not carry phone buzz: %q", jsonTxt)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonTxt), &parsed); err != nil {
		t.Fatalf("(m2) JSON output corrupted: %v", err)
	}
	// Error response must not carry phone buzz piggyback.
	errTxt, isErr := invokeCompanionTestTool(t, s, companionSpeakTool, map[string]any{"session_key": bodyKey})
	if !isErr || strings.Contains(errTxt, "[phone buzz:") {
		t.Fatalf("(m2) error reply must not carry phone buzz: %q", errTxt)
	}

	// Tier-2 companion-work never gets the badge, never gets any companion.* tool,
	// and never gets session.request_hire; it DOES get delegation.report.
	s.hostedKey = workKey
	txt, isErr = invokeCompanionTestTool(t, s, "runtime.read", map[string]any{})
	if isErr || strings.Contains(txt, "[phone buzz:") {
		t.Fatalf("companion-work must not receive phone buzz: %q", txt)
	}
	workDisc := discoveryToolNames(s.callDiscoveryTools(workKey, workEntry, true))
	for _, hidden := range []string{companionSpeakTool, companionFaceTool, companionMotionTool, companionPhonePickupTool, companionPhoneReadTool, companionPhoneSendTool, companionPhonePutdownTool, companionDelegateWorkTool, "session.request_hire"} {
		if workDisc[hidden] {
			t.Fatalf("companion-work discovery exposed %q", hidden)
		}
		resp := invokeCompanionTestToolRaw(t, s, hidden, map[string]any{"session_key": workKey})
		if resp.Error == nil {
			t.Fatalf("companion-work reached %q: %+v", hidden, resp)
		}
	}
	if !workDisc[delegationReportTool] {
		t.Fatalf("companion-work discovery missing %s", delegationReportTool)
	}
	// Verify bodyEntry still does not expose delegation.report.
	bodyDisc := discoveryToolNames(s.callDiscoveryTools(bodyKey, bodyEntry, true))
	if bodyDisc[delegationReportTool] {
		t.Fatalf("companion-body exposed %s", delegationReportTool)
	}
}

func TestCompanionWorkApprovalGateAndDelegationReport(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	host := &testCompanionHost{}
	s := NewServer(root, "test")
	s.companion = host
	workKey := mintCompanionTestKey(t, s, root, CompanionRoleWork)

	// 1. No confirmation host wired -> fails closed with structured denied (no_active_tab) and zero side effects.
	txt, isErr := invokeCompanionTestTool(t, s, "config.tune", map[string]any{
		"session_key": workKey,
		"key":         "workflow.lang",
		"scope":       "global",
		"value":       "ko",
	})
	if !isErr || !strings.Contains(txt, `"error":"denied"`) || !strings.Contains(txt, `"no_active_tab"`) || !strings.Contains(txt, `"config.tune"`) {
		t.Fatalf("companion-work config.tune without host = (%q, isErr=%v), want denied/no_active_tab", txt, isErr)
	}
	gotCfg, cfgErr := invokeCompanionTestTool(t, s, "config.get", map[string]any{"session_key": workKey, "key": "workflow.lang"})
	if cfgErr || strings.Contains(gotCfg, "ko") {
		t.Fatalf("denied config.tune mutated config: (%q, isErr=%v)", gotCfg, cfgErr)
	}

	// 2. Wire confirmation host and verify reject, dismiss, timeout, and no_active_tab
	// all return structured error="denied" with CompanionPause=true and zero side effects.
	dcHost := &fakeDaemonControlHost{}
	s.daemonControl = dcHost

	for _, tc := range []struct {
		name   string
		result wsdashboard.DaemonConfirmResult
		reason string
	}{
		{"reject", wsdashboard.DaemonConfirmResult{Approved: false, Code: "denied", Reason: "denied"}, "denied"},
		{"dismiss", wsdashboard.DaemonConfirmResult{Approved: false, Code: "denied", Reason: "dismissed"}, "dismissed"},
		{"timeout", wsdashboard.DaemonConfirmResult{Approved: false, Code: "timeout", Reason: "timeout"}, "timeout"},
		{"no_active_tab", wsdashboard.DaemonConfirmResult{Approved: false, Code: "no_active_tab", Reason: "no_active_tab"}, "no_active_tab"},
	} {
		dcHost.confirmResp = tc.result
		before := dcHost.confirmCalls
		txt, isErr = invokeCompanionTestTool(t, s, "config.tune", map[string]any{
			"session_key": workKey,
			"key":         "workflow.lang",
			"scope":       "global",
			"value":       "ko",
		})
		if !isErr || !strings.Contains(txt, `"error":"denied"`) || !strings.Contains(txt, tc.reason) {
			t.Fatalf("%s: config.tune = (%q, isErr=%v), want error=denied reason=%s", tc.name, txt, isErr, tc.reason)
		}
		if dcHost.confirmCalls != before+1 || !dcHost.lastConfirmReq.CompanionPause || dcHost.lastConfirmReq.Tool != "config.tune" || !strings.Contains(dcHost.lastConfirmReq.Summary, "workflow.lang") {
			t.Fatalf("%s: unexpected confirm request: calls=%d req=%+v", tc.name, dcHost.confirmCalls, dcHost.lastConfirmReq)
		}
		gotCfg, cfgErr = invokeCompanionTestTool(t, s, "config.get", map[string]any{"session_key": workKey, "key": "workflow.lang"})
		if cfgErr || strings.Contains(gotCfg, "ko") {
			t.Fatalf("%s: denied config.tune had side effect: %q", tc.name, gotCfg)
		}
	}

	// 3. Invalid arguments fail fast in precheckToolArguments WITHOUT popping a confirmation banner.
	beforePrecheck := dcHost.confirmCalls
	txt, isErr = invokeCompanionTestTool(t, s, "config.tune", map[string]any{
		"session_key":   workKey,
		"unknown_param": true,
	})
	if !isErr || dcHost.confirmCalls != beforePrecheck {
		t.Fatalf("invalid config.tune args should fail precheck without banner: txt=%q isErr=%v calls=%d/%d", txt, isErr, dcHost.confirmCalls, beforePrecheck)
	}

	// 4. Non-session note mutation routes through 1-click banner and returns denied on reject.
	dcHost.confirmResp = wsdashboard.DaemonConfirmResult{Approved: false, Code: "denied", Reason: "denied"}
	txt, isErr = invokeCompanionTestTool(t, s, "note.write", map[string]any{
		"session_key": workKey,
		"layer":       "machine",
		"reason":      "shared note mutation test",
		"notes":       []map[string]any{{"key": "k", "value": "v"}},
	})
	if !isErr || !strings.Contains(txt, `"error":"denied"`) || dcHost.lastConfirmReq.Tool != "note.write" || !dcHost.lastConfirmReq.CompanionPause {
		t.Fatalf("companion-work machine note.write = (%q, isErr=%v), req=%+v", txt, isErr, dcHost.lastConfirmReq)
	}

	// 5. Allowlisted session state and read tools execute immediately with NO banner,
	// and (1750) allowlist semantics remain unchanged.
	if !companionWorkAllowsWithoutApproval("note.write", map[string]any{"layer": "session"}) {
		t.Fatalf("expected (1750) allowlist to preserve session-layer note.write")
	}
	beforeAllow := dcHost.confirmCalls
	txt, isErr = invokeCompanionTestTool(t, s, "agenda.set", map[string]any{
		"session_key": workKey,
		"key":         "scratch",
		"value":       map[string]any{"status": "ok"},
	})
	if isErr || dcHost.confirmCalls != beforeAllow {
		t.Fatalf("allowlisted agenda.set failed or prompted: txt=%q isErr=%v calls=%d/%d", txt, isErr, dcHost.confirmCalls, beforeAllow)
	}
	txt, isErr = invokeCompanionTestTool(t, s, "config.list", map[string]any{"session_key": workKey})
	if isErr || dcHost.confirmCalls != beforeAllow {
		t.Fatalf("companion-work allowlisted config.list failed or prompted: txt=%q isErr=%v", txt, isErr)
	}

	// 6. Approving the 1-click banner lets config.tune execute and persist its write.
	dcHost.confirmResp = wsdashboard.DaemonConfirmResult{Approved: true, RequestID: "req-approve-1"}
	txt, isErr = invokeCompanionTestTool(t, s, "config.tune", map[string]any{
		"session_key": workKey,
		"key":         "workflow.lang",
		"scope":       "global",
		"value":       "ko",
	})
	if isErr {
		t.Fatalf("approved config.tune failed: %q", txt)
	}
	gotCfg, cfgErr = invokeCompanionTestTool(t, s, "config.get", map[string]any{"session_key": workKey, "key": "workflow.lang"})
	if cfgErr || !strings.Contains(gotCfg, "ko") {
		t.Fatalf("approved config.tune did not persist workflow.lang=ko: (%q, isErr=%v)", gotCfg, cfgErr)
	}

	// 7. ws/delegation.report is a deprecated T0 alias that succeeds without a banner.
	beforeReport := dcHost.confirmCalls
	bigResult := strings.Repeat("detailed-technical-findings-line\n", 300)
	artifacts := []string{"/tmp/report.md", "https://example.local/diff"}
	txt, isErr = invokeCompanionTestTool(t, s, delegationReportTool, map[string]any{
		"session_key":   workKey,
		"delegation_id": "del-42",
		"status":        "done",
		"result":        bigResult,
		"artifacts":     artifacts,
	})
	if isErr || !strings.Contains(txt, `"deprecated":true`) || !strings.Contains(txt, delegationReportTool) || dcHost.confirmCalls != beforeReport {
		t.Fatalf("delegation.report = (%q, isErr=%v, calls=%d/%d)", txt, isErr, dcHost.confirmCalls, beforeReport)
	}
	if len(host.reports) != 0 {
		t.Fatalf("deprecated delegation.report unexpectedly forwarded to host: %+v", host.reports)
	}
}

func TestCompanionHiddenFromMailboxCorrespondents(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	s := NewServer(root, "test")

	leadKey := mintCompanionTestKey(t, s, root, "")
	workKey := mintCompanionTestKey(t, s, root, CompanionRoleWork)
	bodyKey := mintCompanionTestKey(t, s, root, CompanionRoleBody)

	// Both companion sessions send mail to the lead; lead drains it.
	leadAddr := wsmailbox.ReplyIDPrefix + wsmailbox.ReplyID(leadKey)
	for _, k := range []string{workKey, bodyKey} {
		if txt, isErr := invokeCompanionTestTool(t, s, "mailbox.send", map[string]any{
			"session_key": k,
			"to":          leadAddr,
			"content":     "internal",
		}); isErr {
			t.Fatalf("companion send failed: %s", txt)
		}
	}
	if txt, isErr := invokeCompanionTestTool(t, s, "mailbox.recv", map[string]any{"session_key": leadKey}); isErr || !strings.Contains(txt, "internal") {
		t.Fatalf("lead recv = (%q, isErr=%v)", txt, isErr)
	}

	// Lead calls mailbox.lookup_peers -> neither companion session may appear in correspondents.
	txt, isErr := invokeCompanionTestTool(t, s, "mailbox.lookup_peers", map[string]any{
		"session_key": leadKey,
		"scope":       "machine",
		"format":      "json",
	})
	if isErr {
		t.Fatalf("lookup_peers failed: %s", txt)
	}
	if strings.Contains(txt, wsmailbox.ReplyID(workKey)) || strings.Contains(txt, wsmailbox.ReplyID(bodyKey)) {
		t.Fatalf("companion reply-ids leaked into mailbox correspondents: %s", txt)
	}
}

func TestCompanionApprovalSummaryRuneClampAndRedaction(t *testing.T) {
	// Non-secret config.tune value appears in the banner summary.
	got := companionApprovalSummary("config.tune", map[string]any{
		"key":   "workflow.lang",
		"scope": "global",
		"value": "ko",
	})
	if !strings.Contains(got, "key=workflow.lang") || !strings.Contains(got, "value=ko") {
		t.Fatalf("companionApprovalSummary(config.tune) = %q, want key and value", got)
	}

	// Sensitive key redacts value completely.
	redacted := companionApprovalSummary("config.tune", map[string]any{
		"key":   "provider.api_key",
		"scope": "global",
		"value": "sk-live-super-secret-value",
	})
	if strings.Contains(redacted, "sk-live") || !strings.Contains(redacted, "value=[redacted]") {
		t.Fatalf("companionApprovalSummary(sensitive) = %q, want redacted value", redacted)
	}

	// Long multibyte Korean text clamps cleanly by rune count without corrupting UTF-8.
	longKorean := strings.Repeat("가나다라마바사아자차카타파하", 20)
	clamped := companionApprovalSummary("wiki.update", map[string]any{
		"key":    longKorean,
		"topic":  longKorean,
		"target": longKorean,
		"name":   longKorean,
		"value":  longKorean,
	})
	if !utf8.ValidString(clamped) {
		t.Fatalf("companionApprovalSummary produced invalid UTF-8: %q", clamped)
	}
	if utf8.RuneCountInString(clamped) > 180 {
		t.Fatalf("companionApprovalSummary exceeded 180 runes: %d", utf8.RuneCountInString(clamped))
	}
}

func TestCompanionPhase3ToolsPostureAccessAndNoteIsolation(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	host := &testCompanionHost{}
	s := NewServer(root, "test")
	s.descriptor = connectionDescriptor{callFallback: true}
	s.companion = host

	leadKey := mintCompanionTestKey(t, s, root, "")
	leadEntry, _ := s.lookupSession(leadKey)
	bodyKey := mintCompanionTestKey(t, s, root, CompanionRoleBody)
	bodyEntry, _ := s.lookupSession(bodyKey)
	workKey := mintCompanionTestKey(t, s, root, CompanionRoleWork)
	workEntry, _ := s.lookupSession(workKey)

	memoryTools := []string{
		companionRememberSoulTool,
		companionRememberEpisodeTool,
		companionRecallMemoryTool,
	}

	// 1. companion-body exposes companion.act at BOTH laptop and phone postures,
	// has ZERO access to ws/note.*, and NEVER accesses Soul/Episodes memory tools
	// (exclusive ownership moved to Brain companion-work per D4).
	laptopDisc := discoveryToolNames(s.callDiscoveryTools(bodyKey, bodyEntry, true))
	if !laptopDisc[companionActTool] {
		t.Fatalf("companion-body laptop discovery missing %q", companionActTool)
	}
	for _, tool := range memoryTools {
		if laptopDisc[tool] {
			t.Fatalf("companion-body laptop discovery leaked Brain-only %q", tool)
		}
		if resp := invokeCompanionTestToolRaw(t, s, tool, map[string]any{"session_key": bodyKey}); resp.Error == nil {
			t.Fatalf("companion-body reached Brain-only %q: %+v", tool, resp)
		}
	}
	for _, noteTool := range []string{"note.query", "note.read", "note.write", "note.delete"} {
		if laptopDisc[noteTool] {
			t.Fatalf("companion-body laptop discovery leaked %q", noteTool)
		}
		if resp := invokeCompanionTestToolRaw(t, s, noteTool, map[string]any{"session_key": bodyKey}); resp.Error == nil {
			t.Fatalf("companion-body reached forbidden %q: %+v", noteTool, resp)
		}
	}

	// Call companion.act in laptop posture.
	if txt, isErr := invokeCompanionTestTool(t, s, companionActTool, map[string]any{"session_key": bodyKey, "expression": "happy", "fx": "sparkle", "speech": "hi"}); isErr || !strings.Contains(txt, "wake_reason: timer") || !strings.Contains(txt, "delay_ms: 3000") {
		t.Fatalf("act on laptop = (%q, isErr=%v)", txt, isErr)
	}

	// Switch to phone posture: companion.act remains discoverable and callable; memory tools stay refused.
	if _, _, err := host.store.PhonePickup(bodyKey); err != nil {
		t.Fatalf("PhonePickup: %v", err)
	}
	phoneDisc := discoveryToolNames(s.callDiscoveryTools(bodyKey, bodyEntry, true))
	if !phoneDisc[companionActTool] {
		t.Fatalf("companion-body phone discovery missing %q", companionActTool)
	}
	for _, tool := range memoryTools {
		if phoneDisc[tool] {
			t.Fatalf("companion-body phone discovery leaked Brain-only %q", tool)
		}
	}
	if txt, isErr := invokeCompanionTestTool(t, s, companionActTool, map[string]any{"session_key": bodyKey, "delay_ms": 2000}); isErr || !strings.Contains(txt, "delay_ms: 2000") {
		t.Fatalf("act on phone = (%q, isErr=%v)", txt, isErr)
	}

	// 2. companion-work (Brain) exclusively discovers and calls remember_soul,
	// remember_episode, and recall_memory without confirmation banner ((1796)=a),
	// while still refusing companion.act and companion.phone_*.
	dcHost := &fakeDaemonControlHost{confirmResp: wsdashboard.DaemonConfirmResult{Approved: false, Code: "denied", Reason: "denied"}}
	s.daemonControl = dcHost
	workDisc := discoveryToolNames(s.callDiscoveryTools(workKey, workEntry, true))
	leadDisc := discoveryToolNames(s.callDiscoveryTools(leadKey, leadEntry, true))
	for _, tool := range memoryTools {
		if !workDisc[tool] {
			t.Fatalf("companion-work discovery missing Brain memory tool %q", tool)
		}
		if !companionWorkAllowsWithoutApproval(tool, nil) {
			t.Fatalf("companionWorkAllowsWithoutApproval(%q) = false, want true", tool)
		}
	}
	beforeConfirm := dcHost.confirmCalls
	if txt, isErr := invokeCompanionTestTool(t, s, companionRememberSoulTool, map[string]any{"session_key": workKey, "content": "Likes jazz while coding"}); isErr || !strings.Contains(txt, "status: saved") {
		t.Fatalf("remember_soul on brain = (%q, isErr=%v)", txt, isErr)
	}
	if txt, isErr := invokeCompanionTestTool(t, s, companionRememberEpisodeTool, map[string]any{"session_key": workKey, "summary": "Verified build"}); isErr || !strings.Contains(txt, "saved episode #7") {
		t.Fatalf("remember_episode on brain = (%q, isErr=%v)", txt, isErr)
	}
	if txt, isErr := invokeCompanionTestTool(t, s, companionRecallMemoryTool, map[string]any{"session_key": workKey, "query": "jazz"}); isErr || !strings.Contains(txt, "Prefers short summaries") {
		t.Fatalf("recall_memory on brain = (%q, isErr=%v)", txt, isErr)
	}
	if txt, isErr := invokeCompanionTestTool(t, s, "call", map[string]any{
		"tool":      companionRecallMemoryTool,
		"arguments": map[string]any{"session_key": workKey, "scope": "soul"},
	}); isErr || !strings.Contains(txt, "scope: soul") {
		t.Fatalf("call(recall_memory) on brain = (%q, isErr=%v)", txt, isErr)
	}
	if dcHost.confirmCalls != beforeConfirm {
		t.Fatalf("companion-work memory tools unexpectedly triggered confirmation banner (%d -> %d)", beforeConfirm, dcHost.confirmCalls)
	}

	// Brain must NOT discover or reach companion.act; unmarked lead and keyless callers must NOT reach act or memory tools.
	if workDisc[companionActTool] {
		t.Fatalf("companion-work discovery exposed %q", companionActTool)
	}
	if resp := invokeCompanionTestToolRaw(t, s, companionActTool, map[string]any{"session_key": workKey}); resp.Error == nil {
		t.Fatalf("companion-work reached %q: %+v", companionActTool, resp)
	}
	for _, tool := range append([]string{companionActTool}, memoryTools...) {
		if leadDisc[tool] {
			t.Fatalf("unmarked lead discovery exposed %q", tool)
		}
		if resp := invokeCompanionTestToolRaw(t, s, tool, map[string]any{"session_key": leadKey}); resp.Error == nil {
			t.Fatalf("unmarked lead reached %q: %+v", tool, resp)
		}
		if resp := invokeCompanionTestToolRaw(t, s, tool, map[string]any{}); resp.Error == nil {
			t.Fatalf("keyless caller reached %q: %+v", tool, resp)
		}
	}
}

func TestCompanionBurstLimitEnforcementAndReset(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	host := &testCompanionHost{}
	s := NewServer(root, "test")
	s.descriptor = connectionDescriptor{callFallback: true}
	s.companion = host
	bodyKey := mintCompanionTestKey(t, s, root, CompanionRoleBody)

	// Burst-counted Tier-1 companion.act calls up to companionBurstMaxCalls (12).
	for i := 0; i < companionBurstMaxCalls; i++ {
		txt, isErr := invokeCompanionTestTool(t, s, companionActTool, map[string]any{"session_key": bodyKey, "expression": "happy"})
		if isErr {
			t.Fatalf("burst call %d (act) unexpectedly failed: %s", i+1, txt)
		}
	}

	// 13th counted call returns structured burst_limit error (both direct and via call envelope).
	txt, isErr := invokeCompanionTestTool(t, s, companionActTool, map[string]any{"session_key": bodyKey})
	if !isErr || !strings.Contains(txt, `"burst_limit"`) {
		t.Fatalf("13th call (act) = (%q, isErr=%v), want burst_limit", txt, isErr)
	}
	txt, isErr = invokeCompanionTestTool(t, s, "call", map[string]any{
		"tool":      companionActTool,
		"arguments": map[string]any{"session_key": bodyKey},
	})
	if !isErr || !strings.Contains(txt, `"burst_limit"`) {
		t.Fatalf("13th call envelope (act) = (%q, isErr=%v), want burst_limit", txt, isErr)
	}

	// Deprecated phone and delegation aliases are NOT burst-blocked.
	if txt, isErr := invokeCompanionTestTool(t, s, companionPhonePickupTool, map[string]any{"session_key": bodyKey}); isErr || !strings.Contains(txt, `"deprecated":true`) {
		t.Fatalf("phone_pickup blocked by burst limit: %s", txt)
	}
	if txt, isErr := invokeCompanionTestTool(t, s, companionPhonePutdownTool, map[string]any{"session_key": bodyKey, "style": "normal", "next": "ambient"}); isErr || !strings.Contains(txt, `"deprecated":true`) {
		t.Fatalf("phone_putdown blocked by burst limit: %s", txt)
	}
	if txt, isErr := invokeCompanionTestTool(t, s, companionDelegateWorkTool, map[string]any{"session_key": bodyKey, "brief": "legacy"}); isErr || !strings.Contains(txt, `"deprecated":true`) {
		t.Fatalf("delegate_work blocked by burst limit: %s", txt)
	}

	// Resetting burst on turn end restores full budget.
	s.ResetCompanionBurst(bodyKey)
	if txt, isErr := invokeCompanionTestTool(t, s, companionActTool, map[string]any{"session_key": bodyKey}); isErr || !strings.Contains(txt, "wake_reason: timer") {
		t.Fatalf("act after burst reset = (%q, isErr=%v)", txt, isErr)
	}
}

func TestCompanionActDrainContextCancellation(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	started := make(chan struct{}, 1)
	host := &testCompanionHost{
		actFn: func(ctx context.Context, _ string, req wsdashboard.CompanionActRequest) (wsdashboard.CompanionActResult, error) {
			started <- struct{}{}
			<-ctx.Done()
			return wsdashboard.CompanionActResult{
				WakeReason: wsdashboard.CompanionActWakeCancelled,
				DelayMS:    wsdashboard.ClampCompanionActDelayMS(req.DelayMS, req.HasDelay),
			}, nil
		},
	}
	s := NewServer(root, "test")
	s.companion = host
	bodyKey := mintCompanionTestKey(t, s, root, CompanionRoleBody)

	resultCh := make(chan string, 1)
	go func() {
		txt, isErr := invokeCompanionTestTool(t, s, companionActTool, map[string]any{"session_key": bodyKey, "delay_ms": 6000})
		if isErr {
			t.Errorf("act on drain returned error: %s", txt)
		}
		resultCh <- txt
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for companion.act to start")
	}

	// Server.Drain cancels s.drainContext(), unblocking companion.act immediately.
	drainCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if !s.Drain(drainCtx) {
		t.Fatal("Server.Drain did not finish promptly while companion.act was blocked")
	}
	select {
	case txt := <-resultCh:
		if !strings.Contains(txt, "wake_reason: cancelled") {
			t.Fatalf("drained companion.act result = %q, want wake_reason: cancelled", txt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for companion.act result after drain")
	}
}
