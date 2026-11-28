package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

type fakeDaemonControlHost struct {
	lastReq        wsdashboard.DaemonCurlRequest
	lastDOMReq     wsdashboard.DaemonDOMRequest
	lastUIReq      wsdashboard.DaemonUIRequest
	lastShotReq    wsdashboard.DaemonScreenshotRequest
	lastConfirmReq wsdashboard.DaemonConfirmRequest
	calls          int
	tabsCalls      int
	domCalls       int
	uiCalls        int
	shotCalls      int
	confirmCalls   int
	resp           wsdashboard.DaemonCurlResponse
	tabsResp       []wsdashboard.DaemonTabInfo
	domResp        wsdashboard.DaemonDOMSnapshot
	uiResp         wsdashboard.DaemonUIResponse
	shotResp       wsdashboard.DaemonScreenshotResponse
	confirmResp    wsdashboard.DaemonConfirmResult
	confirmFn      func(context.Context, wsdashboard.DaemonConfirmRequest) wsdashboard.DaemonConfirmResult
	err            error
}

func (f *fakeDaemonControlHost) DaemonCurl(_ context.Context, req wsdashboard.DaemonCurlRequest) (wsdashboard.DaemonCurlResponse, error) {
	f.calls++
	f.lastReq = req
	if f.err != nil {
		return wsdashboard.DaemonCurlResponse{}, f.err
	}
	return f.resp, nil
}

func (f *fakeDaemonControlHost) DaemonTabs() []wsdashboard.DaemonTabInfo {
	f.tabsCalls++
	return f.tabsResp
}

func (f *fakeDaemonControlHost) DaemonDOM(_ context.Context, req wsdashboard.DaemonDOMRequest) (wsdashboard.DaemonDOMSnapshot, error) {
	f.domCalls++
	f.lastDOMReq = req
	if f.err != nil {
		return wsdashboard.DaemonDOMSnapshot{}, f.err
	}
	return f.domResp, nil
}

func (f *fakeDaemonControlHost) DaemonUI(_ context.Context, req wsdashboard.DaemonUIRequest) (wsdashboard.DaemonUIResponse, error) {
	f.uiCalls++
	f.lastUIReq = req
	if f.err != nil {
		return wsdashboard.DaemonUIResponse{}, f.err
	}
	return f.uiResp, nil
}

func (f *fakeDaemonControlHost) DaemonScreenshot(_ context.Context, req wsdashboard.DaemonScreenshotRequest) (wsdashboard.DaemonScreenshotResponse, error) {
	f.shotCalls++
	f.lastShotReq = req
	if f.err != nil {
		return wsdashboard.DaemonScreenshotResponse{}, f.err
	}
	return f.shotResp, nil
}

func (f *fakeDaemonControlHost) RequestDaemonConfirmation(ctx context.Context, req wsdashboard.DaemonConfirmRequest) wsdashboard.DaemonConfirmResult {
	f.confirmCalls++
	f.lastConfirmReq = req
	if f.confirmFn != nil {
		return f.confirmFn(ctx, req)
	}
	return f.confirmResp
}

type fakePostureCompanionHost struct {
	posture string
}

func (f *fakePostureCompanionHost) CompanionPosture(string) string                 { return f.posture }
func (f *fakePostureCompanionHost) CompanionUnreadPhoneCount() int                 { return 0 }
func (f *fakePostureCompanionHost) CompanionSpeak(string, string, int) bool        { return true }
func (f *fakePostureCompanionHost) CompanionFace(string, string, int) bool         { return true }
func (f *fakePostureCompanionHost) CompanionMotion(string, string, string, int) bool { return true }
func (f *fakePostureCompanionHost) CompanionPhonePickup(string) (wsdashboard.CompanionPhoneReadResult, error) {
	return wsdashboard.CompanionPhoneReadResult{}, nil
}
func (f *fakePostureCompanionHost) CompanionPhoneRead(string) (wsdashboard.CompanionPhoneReadResult, error) {
	return wsdashboard.CompanionPhoneReadResult{}, nil
}
func (f *fakePostureCompanionHost) CompanionPhoneSend(string, string, string) (wsdashboard.CompanionPhoneMessage, error) {
	return wsdashboard.CompanionPhoneMessage{}, nil
}
func (f *fakePostureCompanionHost) CompanionPhonePutdown(string, string, string) (wsdashboard.CompanionPhoneSnapshot, error) {
	return wsdashboard.CompanionPhoneSnapshot{}, nil
}
func (f *fakePostureCompanionHost) CompanionReleaseOnTurnEnd(string) bool { return false }

func daemonTestListedToolNames(t *testing.T, s *Server) map[string]bool {
	t.Helper()
	var resp struct {
		Result struct {
			Tools []struct {
				Name string         `json:"name"`
				Meta map[string]any `json:"_meta"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(callToolsList(t, s)), &resp); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]bool, len(resp.Result.Tools))
	for _, tool := range resp.Result.Tools {
		out[tool.Name] = true
	}
	return out
}

func TestDaemonControlOptInGatingPersistedAndEffective(t *testing.T) {
	regs := descriptorRegistrations()

	for _, tc := range []struct {
		backend        string
		advertisedName string
	}{
		{wsagent.BackendClaude, "daemon.curl"},
		{wsagent.BackendAgy, "daemon_curl"},
	} {
		t.Run(tc.backend, func(t *testing.T) {
			// 1. Default OFF: omitted from tools/list (and agy DirectTools), refused on call.
			sOff, fOff := hostedBackendConnection(t, tc.backend, regs)
			defer sOff.Close()
			hostOff := &fakeDaemonControlHost{resp: wsdashboard.DaemonCurlResponse{Status: 200, Body: "ok"}}
			sOff.daemonControl = hostOff

			if names := daemonTestListedToolNames(t, sOff); names[tc.advertisedName] {
				t.Fatalf("default-off %s listed %s", tc.backend, tc.advertisedName)
			}
			res := callToolOnce(t, sOff, 1, tc.advertisedName, map[string]any{"method": "GET", "path": "/health"})
			if !strings.Contains(res, "not enabled") && !strings.Contains(res, `"error"`) {
				t.Fatalf("default-off call unexpectedly succeeded: %s", res)
			}
			if hostOff.calls != 0 {
				t.Fatalf("default-off invoked DaemonCurl %d times", hostOff.calls)
			}

			// 2. Grant AFTER attach (persisted=true, effective=false) stays hidden and denied until restart.
			recOff, _ := fOff.records.RecordByReplyID(sOff.conversation)
			recOff.DashboardControlAccess = true
			fOff.records.Set(recOff)
			if names := daemonTestListedToolNames(t, sOff); names[tc.advertisedName] {
				t.Fatalf("grant-after-attach listed %s before restart", tc.advertisedName)
			}
			res = callToolOnce(t, sOff, 2, tc.advertisedName, map[string]any{"method": "GET", "path": "/health"})
			if !strings.Contains(res, "restart") && !strings.Contains(res, `"error"`) {
				t.Fatalf("grant-after-attach unexpectedly succeeded: %s", res)
			}
			if hostOff.calls != 0 {
				t.Fatalf("grant-after-attach invoked DaemonCurl %d times", hostOff.calls)
			}

			// 3. Opted in at attach (persisted=true && effective=true): listed & callable.
			sOn, fOn := hostedBackendConnection(t, tc.backend, regs)
			defer sOn.Close()
			recOn, _ := fOn.records.RecordByReplyID(sOn.conversation)
			recOn.DashboardControlAccess = true
			fOn.records.Set(recOn)
			sOn.dashboardControlEffective = true
			hostOn := &fakeDaemonControlHost{resp: wsdashboard.DaemonCurlResponse{
				Status:  200,
				Headers: map[string]string{"content-type": "application/json"},
				Body:    `{"ok":true}`,
			}}
			sOn.daemonControl = hostOn

			if names := daemonTestListedToolNames(t, sOn); !names[tc.advertisedName] {
				t.Fatalf("opted-in %s omitted %s from tools/list: %v", tc.backend, tc.advertisedName, names)
			}
			res = callToolOnce(t, sOn, 3, tc.advertisedName, map[string]any{
				"method":  "GET",
				"path":    "/health",
				"query":   map[string]any{"a": "1"},
				"headers": map[string]any{"Accept": "application/json"},
			})
			if toolIsError(t, res) {
				t.Fatalf("opted-in call failed: %s", res)
			}
			if hostOn.calls != 1 || hostOn.lastReq.Path != "/health" || hostOn.lastReq.Query["a"] != "1" {
				t.Fatalf("unexpected forwarded request: calls=%d req=%+v", hostOn.calls, hostOn.lastReq)
			}
			if !strings.Contains(res, `\"status\":200`) {
				t.Fatalf("expected JSON curl response in %s", res)
			}

			// 4. Structured DaemonControlError surfaces its error code.
			hostOn.err = &wsdashboard.DaemonControlError{Code: "forbidden_path", Message: "forbidden"}
			errRes := callToolOnce(t, sOn, 4, tc.advertisedName, map[string]any{"method": "GET", "path": "/settings"})
			if !toolIsError(t, errRes) || !strings.Contains(errRes, "forbidden_path") {
				t.Fatalf("expected forbidden_path error, got: %s", errRes)
			}
			hostOn.err = nil

			// 5. Live revocation (persisted=false && effective=true): immediately hidden and denied without killing session.
			recOn.DashboardControlAccess = false
			fOn.records.Set(recOn)
			if names := daemonTestListedToolNames(t, sOn); names[tc.advertisedName] {
				t.Fatalf("live-revoked %s still listed %s", tc.backend, tc.advertisedName)
			}
			revokedRes := callToolOnce(t, sOn, 5, tc.advertisedName, map[string]any{"method": "GET", "path": "/health"})
			if !strings.Contains(revokedRes, "revoked") && !strings.Contains(revokedRes, `"error"`) {
				t.Fatalf("live-revoked call unexpectedly succeeded: %s", revokedRes)
			}
			if hostOn.calls != 2 { // calls 3 and 4 only; call 5 blocked before handler
				t.Fatalf("live-revoked call reached DaemonCurl: calls=%d", hostOn.calls)
			}
		})
	}
}

func TestDaemonControlBlocksWorkersCompanionBodyAndNonHosted(t *testing.T) {
	regs := descriptorRegistrations()
	s, f := hostedBackendConnection(t, wsagent.BackendClaude, regs)
	defer s.Close()
	rec, _ := f.records.RecordByReplyID(s.conversation)
	rec.DashboardControlAccess = true
	f.records.Set(rec)
	s.dashboardControlEffective = true
	host := &fakeDaemonControlHost{resp: wsdashboard.DaemonCurlResponse{Status: 200, Body: "ok"}}
	s.daemonControl = host

	// Spawned worker keys (both roleDelegate and roleLead-with-parent) are blocked.
	delegateKey, err := s.sessions.mint(f.root, roleDelegate, f.hosted)
	if err != nil {
		t.Fatal(err)
	}
	workerLeadKey, err := s.sessions.mint(f.root, roleLead, f.hosted)
	if err != nil {
		t.Fatal(err)
	}
	for label, key := range map[string]string{"delegate": delegateKey, "worker-lead": workerLeadKey} {
		res := callToolOnce(t, s, 10, daemonCurlTool, map[string]any{
			"session_key": key,
			"method":      "GET",
			"path":        "/health",
		})
		if !strings.Contains(res, `"error"`) {
			t.Fatalf("%s worker key unexpectedly allowed: %s", label, res)
		}
	}

	// Tier-1 companion-body record is blocked (both by Profile and by CompanionRole)
	// even if Lead=true and DashboardControlAccess=true.
	rec.Profile = "companion-body"
	f.records.Set(rec)
	if names := daemonTestListedToolNames(t, s); names[daemonCurlTool] {
		t.Fatal("companion-body profile must not see daemon.curl in tools/list")
	}
	res := callToolOnce(t, s, 11, daemonCurlTool, map[string]any{"method": "GET", "path": "/health"})
	if !strings.Contains(res, `"error"`) {
		t.Fatalf("companion-body profile call unexpectedly allowed: %s", res)
	}
	rec.Profile = "claude-lead"
	rec.CompanionRole = CompanionRoleBody
	f.records.Set(rec)
	if names := daemonTestListedToolNames(t, s); names[daemonCurlTool] {
		t.Fatal("companion-body role must not see daemon.curl in tools/list")
	}
	res = callToolOnce(t, s, 13, daemonCurlTool, map[string]any{"method": "GET", "path": "/health"})
	if !strings.Contains(res, `"error"`) {
		t.Fatalf("companion-body role call unexpectedly allowed: %s", res)
	}

	// Non-hosted / launcher connection (s.conversation == "") is blocked even with a top-level lead key.
	nonHosted := NewServer(f.root, "test")
	defer nonHosted.Close()
	nonHosted.daemonControl = host
	leadKey, err := nonHosted.sessions.mint(f.root, roleLead, "")
	if err != nil {
		t.Fatal(err)
	}
	res = callToolOnce(t, nonHosted, 12, daemonCurlTool, map[string]any{
		"session_key": leadKey,
		"method":      "GET",
		"path":        "/health",
	})
	if !strings.Contains(res, `"error"`) && !toolIsError(t, res) {
		t.Fatalf("non-hosted call unexpectedly allowed: %s", res)
	}
	if host.calls != 0 {
		t.Fatalf("blocked callers reached DaemonCurl %d times", host.calls)
	}
}

func TestDaemonControlCompanionPostureHandsBusyLock(t *testing.T) {
	regs := descriptorRegistrations()
	s, f := hostedBackendConnection(t, wsagent.BackendClaude, regs)
	defer s.Close()
	rec, _ := f.records.RecordByReplyID(s.conversation)
	rec.DashboardControlAccess = true
	f.records.Set(rec)
	s.dashboardControlEffective = true
	host := &fakeDaemonControlHost{resp: wsdashboard.DaemonCurlResponse{Status: 200, Body: "ok"}}
	s.daemonControl = host
	comp := &fakePostureCompanionHost{posture: wsdashboard.CompanionPosturePhone}
	s.companion = comp

	// Companion-body sessions holding the phone are refused by companionPostureRefusal
	// (hands-busy lock) and never see daemon.curl in callDiscoveryTools.
	bodyEntry := sessionEntry{scope: roleLead, companionRole: CompanionRoleBody}
	if refusal := s.companionPostureRefusal(daemonCurlTool, f.hosted, bodyEntry, true); !strings.Contains(refusal, companionRefusalHandsBusy) {
		t.Fatalf("companion-body phone posture refusal = %q, want %q", refusal, companionRefusalHandsBusy)
	}
	for _, tool := range s.callDiscoveryTools(f.hosted, bodyEntry, true) {
		if tool["name"] == daemonCurlTool {
			t.Fatal("daemon.curl must be hidden in callDiscoveryTools for companion-body during phone posture")
		}
	}

	// Regular unmarked opted-in leads are not posture-locked (261127 (1740)).
	res := callToolOnce(t, s, 21, daemonCurlTool, map[string]any{"method": "GET", "path": "/health"})
	if toolIsError(t, res) || host.calls != 1 {
		t.Fatalf("unmarked lead call failed: res=%s calls=%d", res, host.calls)
	}
}

func TestDaemonControlHostedRenderAndLeadToolNames(t *testing.T) {
	for _, tool := range []string{daemonCurlTool, daemonTabsTool, daemonDOMTool, daemonUITool, daemonScreenshotTool, daemonHighlightTool, daemonMonitorTool} {
		if !slices.Contains(LeadToolNames(), tool) {
			t.Fatalf("%s missing from LeadToolNames", tool)
		}
		if !isLeadOnlyTool(tool) {
			t.Fatalf("%s must be in isLeadOnlyTool", tool)
		}
	}

	// Agy HostedRender includes daemon_* [direct] ONLY when the lead opted in.
	setupWorkflowManualNoteEnv(t)
	root := t.TempDir()
	initGit(t, root)
	key, err := NewAgentKeyStore().MintLead(root)
	if err != nil {
		t.Fatal(err)
	}
	records := newTestRecordReader()
	replyID := wsmailbox.ReplyID(key)
	records.Set(wsagent.Record{
		ReplyID: replyID, WSKey: key, Root: root, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Backend: wsagent.BackendAgy, Lead: true, DashboardControlAccess: false,
	})
	agyReg := descriptorRegistrations()[wsagent.BackendAgy]
	render := HostedRender{
		Harness:          agyReg.ManualHarness,
		PlaybookCommands: agyReg.PlaybookCommands,
		ToolNameScheme:   agyReg.ToolNameScheme,
		CallFallback:     agyReg.CallFallback,
		DirectTools:      agyReg.DirectTools,
		Records:          records,
	}
	offManual, err := RenderLeadManual(nil, root, render, key)
	if err != nil {
		t.Fatalf("RenderLeadManual (off): %v", err)
	}
	for _, alias := range []string{"daemon_curl", "daemon_tabs", "daemon_dom", "daemon_ui", "daemon_screenshot", "daemon_highlight", "daemon_monitor"} {
		if strings.Contains(offManual, alias) {
			t.Fatalf("default-off agy manual unexpectedly listed %s", alias)
		}
	}

	records.Set(wsagent.Record{
		ReplyID: replyID, WSKey: key, Root: root, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Backend: wsagent.BackendAgy, Lead: true, DashboardControlAccess: true,
	})
	onManual, err := RenderLeadManual(nil, root, render, key)
	if err != nil {
		t.Fatalf("RenderLeadManual (on): %v", err)
	}
	for _, alias := range []string{"daemon_curl [direct]", "daemon_tabs [direct]", "daemon_dom [direct]", "daemon_ui [direct]", "daemon_screenshot [direct]", "daemon_highlight [direct]", "daemon_monitor [direct]"} {
		if !strings.Contains(onManual, alias) {
			t.Fatalf("opted-in agy manual missing %q", alias)
		}
	}
}

func TestDaemonControlTabsDOMAndUIDispatchAndErrors(t *testing.T) {
	regs := descriptorRegistrations()
	s, f := hostedBackendConnection(t, wsagent.BackendClaude, regs)
	defer s.Close()
	rec, _ := f.records.RecordByReplyID(s.conversation)
	rec.DashboardControlAccess = true
	f.records.Set(rec)
	s.dashboardControlEffective = true
	host := &fakeDaemonControlHost{
		tabsResp: []wsdashboard.DaemonTabInfo{{TabID: "tab-1", Visible: true, Focused: true, ActiveRoute: "/s/abc"}},
		domResp:  wsdashboard.DaemonDOMSnapshot{TabID: "tab-1", Generation: 3, Route: "/s/abc", NodeCount: 2, ByteSize: 120},
		uiResp:   wsdashboard.DaemonUIResponse{OK: true, Action: "click_ref", TabID: "tab-1", Ref: "g3-r1"},
		shotResp: wsdashboard.DaemonScreenshotResponse{
			Mode:   "dom_fallback",
			Live:   false,
			TabID:  "tab-1",
			Route:  "/s/abc",
			Reason: "screenshot_unavailable",
			DOM:    &wsdashboard.DaemonDOMSnapshot{TabID: "tab-1", Generation: 4, Route: "/s/abc", NodeCount: 1, ByteSize: 64},
		},
	}
	s.daemonControl = host

	names := daemonTestListedToolNames(t, s)
	for _, want := range []string{daemonCurlTool, daemonTabsTool, daemonDOMTool, daemonUITool, daemonScreenshotTool} {
		if !names[want] {
			t.Fatalf("opted-in tools/list missing %s", want)
		}
	}

	resTabs := callToolOnce(t, s, 30, daemonTabsTool, map[string]any{})
	if toolIsError(t, resTabs) || host.tabsCalls != 1 || !strings.Contains(resTabs, "tab-1") {
		t.Fatalf("daemon.tabs failed: res=%s calls=%d", resTabs, host.tabsCalls)
	}

	resDOM := callToolOnce(t, s, 31, daemonDOMTool, map[string]any{"tab_id": "tab-1", "scope": "frame", "interactive_only": true, "max_nodes": 50})
	if toolIsError(t, resDOM) || host.domCalls != 1 || host.lastDOMReq.Scope != "frame" || !host.lastDOMReq.InteractiveOnly || host.lastDOMReq.MaxNodes != 50 {
		t.Fatalf("daemon.dom failed: res=%s req=%+v", resDOM, host.lastDOMReq)
	}

	resUI := callToolOnce(t, s, 32, daemonUITool, map[string]any{"tab_id": "tab-1", "action": "click_ref", "ref": "g3-r1", "generation": 3})
	if toolIsError(t, resUI) || host.uiCalls != 1 || host.lastUIReq.Action != "click_ref" || host.lastUIReq.Ref != "g3-r1" || host.lastUIReq.Generation != 3 {
		t.Fatalf("daemon.ui failed: res=%s req=%+v", resUI, host.lastUIReq)
	}

	resShot := callToolOnce(t, s, 34, daemonScreenshotTool, map[string]any{"tab_id": "tab-1"})
	if toolIsError(t, resShot) || host.shotCalls != 1 || host.lastShotReq.TabID != "tab-1" || host.lastShotReq.SessionID != s.conversation {
		t.Fatalf("daemon.screenshot failed: res=%s calls=%d req=%+v", resShot, host.shotCalls, host.lastShotReq)
	}
	if !strings.Contains(resShot, `\"mode\":\"dom_fallback\"`) || !strings.Contains(resShot, `\"live\":false`) {
		t.Fatalf("daemon.screenshot missing expected fallback contract fields: %s", resShot)
	}

	host.err = &wsdashboard.DaemonControlError{Code: "payload_too_large", Message: "too large"}
	resTooBig := callToolOnce(t, s, 35, daemonScreenshotTool, map[string]any{"tab_id": "tab-1"})
	if !toolIsError(t, resTooBig) || !strings.Contains(resTooBig, "payload_too_large") {
		t.Fatalf("expected payload_too_large error, got %s", resTooBig)
	}

	host.err = &wsdashboard.DaemonControlError{Code: "stale_ref", Message: "stale"}
	resStale := callToolOnce(t, s, 33, daemonUITool, map[string]any{"action": "click_ref", "ref": "g1-r1"})
	if !toolIsError(t, resStale) || !strings.Contains(resStale, "stale_ref") {
		t.Fatalf("expected stale_ref error, got %s", resStale)
	}
}

func TestDaemonControlCompanionWorkAutoGrantAndIsolation(t *testing.T) {
	regs := descriptorRegistrations()

	for _, tc := range []struct {
		backend   string
		wantNames []string
		curlName  string
		uiName    string
	}{
		{
			backend:   wsagent.BackendClaude,
			wantNames: []string{daemonCurlTool, daemonTabsTool, daemonDOMTool, daemonUITool, daemonScreenshotTool, daemonHighlightTool, daemonMonitorTool},
			curlName:  daemonCurlTool,
			uiName:    daemonUITool,
		},
		{
			backend:   wsagent.BackendAgy,
			wantNames: []string{"daemon_curl", "daemon_tabs", "daemon_dom", "daemon_ui", "daemon_screenshot", "daemon_highlight", "daemon_monitor"},
			curlName:  "daemon_curl",
			uiName:    "daemon_ui",
		},
	} {
		t.Run(tc.backend, func(t *testing.T) {
			s, f := hostedBackendConnection(t, tc.backend, regs)
			defer s.Close()
			// Companion-work record with DashboardControlAccess=false and
			// dashboardControlEffective=false must STILL be auto-granted ws/daemon.*.
			workKey, err := s.sessions.mintWithCompanionRole(f.root, roleLead, "", "", CompanionRoleWork)
			if err != nil {
				t.Fatal(err)
			}
			rec, _ := f.records.RecordByReplyID(s.conversation)
			rec.ReplyID = wsmailbox.ReplyID(workKey)
			rec.WSKey = workKey
			rec.CompanionRole = CompanionRoleWork
			rec.DashboardControlAccess = false
			f.records.Set(rec)
			s.conversation = rec.ReplyID
			s.hostedKey = workKey
			s.dashboardControlEffective = false
			if bc, ok := s.callerBinding.(boundCallers); ok {
				bc.backends[rec.ReplyID] = tc.backend
			}
			workEntry, ok := s.lookupSession(workKey)
			if !ok {
				t.Fatal("failed to lookup minted companion-work key")
			}

			host := &fakeDaemonControlHost{
				resp:     wsdashboard.DaemonCurlResponse{Status: 200, Body: "ok"},
				tabsResp: []wsdashboard.DaemonTabInfo{{TabID: "tab-1", Visible: true, Focused: true}},
				domResp:  wsdashboard.DaemonDOMSnapshot{TabID: "tab-1", Generation: 1, NodeCount: 1, ByteSize: 32},
				uiResp:   wsdashboard.DaemonUIResponse{OK: true, Action: "click_ref", TabID: "tab-1", Ref: "g1-r1"},
				shotResp: wsdashboard.DaemonScreenshotResponse{Mode: "dom_fallback", Live: false, TabID: "tab-1"},
			}
			s.daemonControl = host

			// 1. tools/list and callDiscoveryTools advertise all 5 ws/daemon.* tools without opt-in.
			listed := daemonTestListedToolNames(t, s)
			for _, want := range tc.wantNames {
				if !listed[want] {
					t.Fatalf("companion-work %s tools/list missing %s: %v", tc.backend, want, listed)
				}
			}
			disc := discoveryToolNames(s.callDiscoveryTools(workKey, workEntry, true))
			for _, want := range tc.wantNames {
				if !disc[want] {
					t.Fatalf("companion-work %s callDiscoveryTools missing %s", tc.backend, want)
				}
			}

			// 2. Dispatch forwards CompanionPause=true for daemon.curl and daemon.ui without double-prompting.
			resCurl := callToolOnce(t, s, 40, tc.curlName, map[string]any{
				"session_key": workKey,
				"method":      "POST",
				"path":        "/health",
			})
			if toolIsError(t, resCurl) || host.calls != 1 || !host.lastReq.CompanionPause {
				t.Fatalf("companion-work %s failed or missed CompanionPause: res=%s calls=%d req=%+v", tc.curlName, resCurl, host.calls, host.lastReq)
			}
			resUI := callToolOnce(t, s, 41, tc.uiName, map[string]any{
				"session_key": workKey,
				"action":      "click_ref",
				"ref":         "g1-r1",
			})
			if toolIsError(t, resUI) || host.uiCalls != 1 || !host.lastUIReq.CompanionPause {
				t.Fatalf("companion-work %s failed or missed CompanionPause: res=%s calls=%d req=%+v", tc.uiName, resUI, host.uiCalls, host.lastUIReq)
			}
			if host.confirmCalls != 0 {
				t.Fatalf("daemon.* calls must not double-prompt in companionWorkApprovalGate: confirmCalls=%d", host.confirmCalls)
			}

			// 3. Worker subagent keys spawned from companion-work remain strictly blocked.
			workerKey, err := s.sessions.mint(f.root, roleDelegate, workKey)
			if err != nil {
				t.Fatal(err)
			}
			workerEntry, _ := s.lookupSession(workerKey)
			workerDisc := discoveryToolNames(s.callDiscoveryTools(workerKey, workerEntry, true))
			for _, want := range tc.wantNames {
				if workerDisc[want] {
					t.Fatalf("worker subagent under companion-work unexpectedly discovered %s", want)
				}
			}
			resWorker := callToolOnce(t, s, 42, tc.curlName, map[string]any{
				"session_key": workerKey,
				"method":      "GET",
				"path":        "/health",
			})
			if !strings.Contains(resWorker, `"error"`) {
				t.Fatalf("worker subagent reached %s: %s", tc.curlName, resWorker)
			}
			if host.calls != 1 {
				t.Fatalf("blocked worker reached DaemonCurl: calls=%d", host.calls)
			}
		})
	}

	// 4. Agy RenderLeadManual auto-grants daemon_* [direct] for companion-work
	// even with DashboardControlAccess=false, and blocks companion-body even with DashboardControlAccess=true.
	setupWorkflowManualNoteEnv(t)
	root := t.TempDir()
	initGit(t, root)
	key, err := NewAgentKeyStore().MintLead(root)
	if err != nil {
		t.Fatal(err)
	}
	records := newTestRecordReader()
	replyID := wsmailbox.ReplyID(key)
	agyReg := descriptorRegistrations()[wsagent.BackendAgy]
	render := HostedRender{
		Harness:          agyReg.ManualHarness,
		PlaybookCommands: agyReg.PlaybookCommands,
		ToolNameScheme:   agyReg.ToolNameScheme,
		CallFallback:     agyReg.CallFallback,
		DirectTools:      agyReg.DirectTools,
		Records:          records,
	}

	records.Set(wsagent.Record{
		ReplyID: replyID, WSKey: key, Root: root, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Backend: wsagent.BackendAgy, Lead: true, CompanionRole: CompanionRoleWork, DashboardControlAccess: false,
	})
	workManual, err := RenderLeadManual(nil, root, render, key)
	if err != nil {
		t.Fatalf("RenderLeadManual (companion-work): %v", err)
	}
	for _, alias := range []string{"daemon_curl [direct]", "daemon_tabs [direct]", "daemon_dom [direct]", "daemon_ui [direct]", "daemon_screenshot [direct]", "daemon_highlight [direct]", "daemon_monitor [direct]"} {
		if !strings.Contains(workManual, alias) {
			t.Fatalf("companion-work agy manual missing %q", alias)
		}
	}

	records.Set(wsagent.Record{
		ReplyID: replyID, WSKey: key, Root: root, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Backend: wsagent.BackendAgy, Lead: true, CompanionRole: CompanionRoleBody, DashboardControlAccess: true,
	})
	bodyManual, err := RenderLeadManual(nil, root, render, key)
	if err != nil {
		t.Fatalf("RenderLeadManual (companion-body): %v", err)
	}
	// Tier-1 body: exactly the guide subset is [direct]; curl/ui/screenshot never appear.
	for _, alias := range []string{"daemon_tabs [direct]", "daemon_dom [direct]", "daemon_highlight [direct]", "daemon_monitor [direct]"} {
		if !strings.Contains(bodyManual, alias) {
			t.Fatalf("companion-body agy manual missing guide tool %q", alias)
		}
	}
	for _, alias := range []string{"daemon_curl", "daemon_ui", "daemon_screenshot"} {
		if strings.Contains(bodyManual, alias) {
			t.Fatalf("companion-body agy manual unexpectedly listed %s", alias)
		}
	}
}
