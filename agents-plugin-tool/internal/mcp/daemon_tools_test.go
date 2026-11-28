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
	lastReq wsdashboard.DaemonCurlRequest
	calls   int
	resp    wsdashboard.DaemonCurlResponse
	err     error
}

func (f *fakeDaemonControlHost) DaemonCurl(_ context.Context, req wsdashboard.DaemonCurlRequest) (wsdashboard.DaemonCurlResponse, error) {
	f.calls++
	f.lastReq = req
	if f.err != nil {
		return wsdashboard.DaemonCurlResponse{}, f.err
	}
	return f.resp, nil
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

	// Tier-1 companion-body record is blocked even if Lead=true and DashboardControlAccess=true.
	rec.Profile = "companion-body"
	f.records.Set(rec)
	if names := daemonTestListedToolNames(t, s); names[daemonCurlTool] {
		t.Fatal("companion-body must not see daemon.curl in tools/list")
	}
	res := callToolOnce(t, s, 11, daemonCurlTool, map[string]any{"method": "GET", "path": "/health"})
	if !strings.Contains(res, `"error"`) {
		t.Fatalf("companion-body call unexpectedly allowed: %s", res)
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

	// While holding phone, daemon.curl is blocked by companionPostureRefusal (hands busy)
	// and omitted from callDiscoveryTools.
	entry, found := s.lookupSession(f.hosted)
	for _, tool := range s.callDiscoveryTools(f.hosted, entry, found) {
		if tool["name"] == daemonCurlTool {
			t.Fatal("daemon.curl must be hidden in callDiscoveryTools during phone posture")
		}
	}
	res := callToolOnce(t, s, 20, daemonCurlTool, map[string]any{"method": "GET", "path": "/health"})
	if !toolIsError(t, res) || !strings.Contains(res, companionRefusalHandsBusy) {
		t.Fatalf("phone posture call = %s, want %q", res, companionRefusalHandsBusy)
	}
	if host.calls != 0 {
		t.Fatalf("phone posture call reached DaemonCurl %d times", host.calls)
	}

	// Putting phone down restores access.
	comp.posture = wsdashboard.CompanionPostureLaptop
	res = callToolOnce(t, s, 21, daemonCurlTool, map[string]any{"method": "GET", "path": "/health"})
	if toolIsError(t, res) || host.calls != 1 {
		t.Fatalf("laptop posture call failed: res=%s calls=%d", res, host.calls)
	}
}

func TestDaemonControlHostedRenderAndLeadToolNames(t *testing.T) {
	if !slices.Contains(LeadToolNames(), daemonCurlTool) {
		t.Fatal("daemon.curl missing from LeadToolNames")
	}
	if !isLeadOnlyTool(daemonCurlTool) {
		t.Fatal("daemon.curl must be in isLeadOnlyTool")
	}

	// Agy HostedRender includes daemon_curl [direct] ONLY when the lead opted in.
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
	if strings.Contains(offManual, "daemon_curl") {
		t.Fatalf("default-off agy manual unexpectedly listed daemon_curl")
	}

	records.Set(wsagent.Record{
		ReplyID: replyID, WSKey: key, Root: root, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Backend: wsagent.BackendAgy, Lead: true, DashboardControlAccess: true,
	})
	onManual, err := RenderLeadManual(nil, root, render, key)
	if err != nil {
		t.Fatalf("RenderLeadManual (on): %v", err)
	}
	if !strings.Contains(onManual, "daemon_curl [direct]") {
		t.Fatalf("opted-in agy manual missing 'daemon_curl [direct]'")
	}
}
