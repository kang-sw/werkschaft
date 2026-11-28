package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

// fakeGuideHost adds the optional DaemonGuideHost surface.
type fakeGuideHost struct {
	fakeDaemonControlHost
	lastHighlight  wsdashboard.DaemonHighlightRequest
	lastMonitor    wsdashboard.DaemonMonitorRequest
	highlightCalls int
	monitorCalls   int
	highlightResp  wsdashboard.DaemonHighlightResponse
	monitorResp    wsdashboard.DaemonMonitorStatus
	guideErr       error
}

func (f *fakeGuideHost) DaemonHighlight(_ context.Context, req wsdashboard.DaemonHighlightRequest) (wsdashboard.DaemonHighlightResponse, error) {
	f.highlightCalls++
	f.lastHighlight = req
	if f.guideErr != nil {
		return wsdashboard.DaemonHighlightResponse{}, f.guideErr
	}
	return f.highlightResp, nil
}

func (f *fakeGuideHost) DaemonMonitor(_ context.Context, req wsdashboard.DaemonMonitorRequest) (wsdashboard.DaemonMonitorStatus, error) {
	f.monitorCalls++
	f.lastMonitor = req
	if f.guideErr != nil {
		return wsdashboard.DaemonMonitorStatus{}, f.guideErr
	}
	return f.monitorResp, nil
}

func newFakeGuideHost() *fakeGuideHost {
	return &fakeGuideHost{
		fakeDaemonControlHost: fakeDaemonControlHost{
			resp:     wsdashboard.DaemonCurlResponse{Status: 200, Body: "ok"},
			tabsResp: []wsdashboard.DaemonTabInfo{{TabID: "tab-1", Visible: true, Focused: true}},
			domResp:  wsdashboard.DaemonDOMSnapshot{TabID: "tab-1", Generation: 1, NodeCount: 1, ByteSize: 32},
			uiResp:   wsdashboard.DaemonUIResponse{OK: true, Action: "navigate", TabID: "tab-1"},
		},
		highlightResp: wsdashboard.DaemonHighlightResponse{OK: true, Action: "show", TabID: "tab-1", ID: "default", Style: "spotlight", TargetKind: "selector", Scope: "frame", Visible: true, Rect: &wsdashboard.DaemonHighlightRect{X: 120, Y: 88, Width: 240, Height: 32}},
		monitorResp:   wsdashboard.DaemonMonitorStatus{Active: true, MonitorID: "gm-1", Events: []string{"tab_switched"}, TTLSec: 600, DebounceMS: 400, RemainingSec: 600},
	}
}

// bodyGuideConnection binds s to a freshly minted companion-body lead key
// whose record is an open top-level body (DashboardControlAccess off).
func bodyGuideConnection(t *testing.T, backend string) (*Server, string, sessionEntry, *fakeGuideHost) {
	t.Helper()
	s, f := hostedBackendConnection(t, backend, descriptorRegistrations())
	t.Cleanup(s.Close)
	bodyKey, err := s.sessions.mintWithCompanionRole(f.root, roleLead, "", "", CompanionRoleBody)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := f.records.RecordByReplyID(s.conversation)
	rec.ReplyID = wsmailbox.ReplyID(bodyKey)
	rec.WSKey = bodyKey
	rec.CompanionRole = CompanionRoleBody
	rec.DashboardControlAccess = false
	f.records.Set(rec)
	s.conversation = rec.ReplyID
	s.hostedKey = bodyKey
	s.dashboardControlEffective = false
	if bc, ok := s.callerBinding.(boundCallers); ok {
		bc.backends[rec.ReplyID] = backend
	}
	entry, ok := s.lookupSession(bodyKey)
	if !ok {
		t.Fatal("lookup body key")
	}
	host := newFakeGuideHost()
	s.daemonControl = host
	return s, bodyKey, entry, host
}

func TestDaemonGuideCompanionBodyAuthoritySplit(t *testing.T) {
	s, bodyKey, entry, host := bodyGuideConnection(t, wsagent.BackendClaude)

	listed := daemonTestListedToolNames(t, s)
	for _, want := range []string{daemonTabsTool, daemonDOMTool, daemonHighlightTool, daemonMonitorTool} {
		if !listed[want] {
			t.Fatalf("companion-body tools/list missing guide tool %s", want)
		}
	}
	for _, banned := range []string{daemonCurlTool, daemonUITool, daemonScreenshotTool} {
		if listed[banned] {
			t.Fatalf("companion-body tools/list exposed %s", banned)
		}
	}
	disc := discoveryToolNames(s.callDiscoveryTools(bodyKey, entry, true))
	for _, want := range []string{daemonTabsTool, daemonDOMTool, daemonHighlightTool, daemonMonitorTool} {
		if !disc[want] {
			t.Fatalf("companion-body discovery missing %s", want)
		}
	}
	for _, banned := range []string{daemonCurlTool, daemonUITool, daemonScreenshotTool} {
		if disc[banned] {
			t.Fatalf("companion-body discovery exposed %s", banned)
		}
	}

	// Guide tools are callable without any confirmation.
	res := callToolOnce(t, s, 1, daemonTabsTool, map[string]any{"session_key": bodyKey})
	if toolIsError(t, res) || host.tabsCalls != 1 {
		t.Fatalf("body daemon.tabs: %s", res)
	}
	res = callToolOnce(t, s, 2, daemonDOMTool, map[string]any{"session_key": bodyKey})
	if toolIsError(t, res) || host.domCalls != 1 {
		t.Fatalf("body daemon.dom: %s", res)
	}
	res = callToolOnce(t, s, 3, daemonHighlightTool, map[string]any{"session_key": bodyKey, "action": "show", "target": map[string]any{"selector": "#x"}, "comment": "here"})
	if toolIsError(t, res) || host.highlightCalls != 1 {
		t.Fatalf("body daemon.highlight: %s", res)
	}
	if !strings.Contains(res, "highlight show id=default tab=tab-1 style=spotlight target=selector scope=frame visible rect=120,88 240x32") {
		t.Fatalf("highlight compact text = %s", res)
	}
	res = callToolOnce(t, s, 4, daemonMonitorTool, map[string]any{"session_key": bodyKey, "action": "start", "events": []any{"tab_switched"}, "ttl_sec": 300, "debounce_ms": 500})
	if toolIsError(t, res) || host.monitorCalls != 1 {
		t.Fatalf("body daemon.monitor: %s", res)
	}
	if host.lastMonitor.Owner != string(s.conversation) || host.lastMonitor.TTLSec != 300 || host.lastMonitor.DebounceMS != 500 || len(host.lastMonitor.Events) != 1 {
		t.Fatalf("monitor request = %+v", host.lastMonitor)
	}
	if host.confirmCalls != 0 {
		t.Fatalf("guide tools requested confirmation %d times", host.confirmCalls)
	}

	// curl/ui/screenshot are refused for the body at call time.
	for i, name := range []string{daemonCurlTool, daemonUITool, daemonScreenshotTool} {
		res := callToolOnce(t, s, 10+i, name, map[string]any{"session_key": bodyKey, "method": "GET", "path": "/health", "action": "navigate", "path_": "/"})
		if !strings.Contains(res, `"error"`) && !toolIsError(t, res) {
			t.Fatalf("companion-body reached %s: %s", name, res)
		}
	}
	if host.calls != 0 || host.uiCalls != 0 || host.shotCalls != 0 {
		t.Fatalf("body reached non-guide host methods: curl=%d ui=%d shot=%d", host.calls, host.uiCalls, host.shotCalls)
	}
}

func TestDaemonGuidePhonePostureLocksBody(t *testing.T) {
	s, bodyKey, entry, _ := bodyGuideConnection(t, wsagent.BackendClaude)
	s.companion = &fakePostureCompanionHost{posture: wsdashboard.CompanionPosturePhone}
	for _, name := range []string{daemonTabsTool, daemonDOMTool, daemonHighlightTool, daemonMonitorTool} {
		if refusal := s.companionPostureRefusal(name, bodyKey, entry, true); !strings.Contains(refusal, companionRefusalHandsBusy) {
			t.Fatalf("phone posture refusal for %s = %q", name, refusal)
		}
	}
}

func TestDaemonGuideWorkersAndUnoptedLeadsNeverSeeGuideTools(t *testing.T) {
	s, f := hostedBackendConnection(t, wsagent.BackendClaude, descriptorRegistrations())
	defer s.Close()
	s.daemonControl = newFakeGuideHost()
	// An unopted regular lead sees none of the daemon tools.
	listed := daemonTestListedToolNames(t, s)
	for _, name := range []string{daemonTabsTool, daemonDOMTool, daemonHighlightTool, daemonMonitorTool} {
		if listed[name] {
			t.Fatalf("unopted lead tools/list exposed %s", name)
		}
	}
	workerKey, err := s.sessions.mint(f.root, roleDelegate, f.hosted)
	if err != nil {
		t.Fatal(err)
	}
	workerEntry, _ := s.lookupSession(workerKey)
	disc := discoveryToolNames(s.callDiscoveryTools(workerKey, workerEntry, true))
	for _, name := range []string{daemonHighlightTool, daemonMonitorTool} {
		if disc[name] {
			t.Fatalf("worker discovered %s", name)
		}
		res := callToolOnce(t, s, 20, name, map[string]any{"session_key": workerKey, "action": "status"})
		if !strings.Contains(res, `"error"`) && !toolIsError(t, res) {
			t.Fatalf("worker reached %s: %s", name, res)
		}
	}
}

func TestDaemonGuideArgsFormatAndHostFallback(t *testing.T) {
	s, bodyKey, _, host := bodyGuideConnection(t, wsagent.BackendClaude)

	// format=json returns the structured object.
	res := callToolOnce(t, s, 1, daemonMonitorTool, map[string]any{"session_key": bodyKey, "action": "status", "format": "json"})
	if toolIsError(t, res) || !strings.Contains(res, `\"monitor_id\":\"gm-1\"`) {
		t.Fatalf("monitor json = %s", res)
	}
	res = callToolOnce(t, s, 2, daemonMonitorTool, map[string]any{"session_key": bodyKey, "action": "status"})
	if !strings.Contains(res, "monitor status active id=gm-1 events=tab_switched") {
		t.Fatalf("monitor compact = %s", res)
	}

	// Argument validation happens before the host.
	before := host.highlightCalls
	for i, args := range []map[string]any{
		{"action": "show", "target": "nope"},
		{"action": "show", "target": map[string]any{"xpath": "//a"}},
		{"action": "show", "target": map[string]any{"ref": 7}},
		{"action": "show", "target": map[string]any{"ref": "g1-r1", "generation": -1}},
		{"action": "show", "format": "yaml"},
	} {
		args["session_key"] = bodyKey
		res := callToolOnce(t, s, 10+i, daemonHighlightTool, args)
		if !toolIsError(t, res) && !strings.Contains(res, `"error"`) {
			t.Fatalf("invalid highlight args %v accepted: %s", args, res)
		}
	}
	for i, args := range []map[string]any{
		{"action": "start", "events": "tab_switched"},
		{"action": "start", "events": []any{1}},
		{"action": "start", "ttl_sec": 0},
	} {
		args["session_key"] = bodyKey
		res := callToolOnce(t, s, 20+i, daemonMonitorTool, args)
		if !toolIsError(t, res) && !strings.Contains(res, `"error"`) {
			t.Fatalf("invalid monitor args %v accepted: %s", args, res)
		}
	}
	if host.highlightCalls != before {
		t.Fatalf("invalid args reached the host")
	}

	// Structured daemon errors pass through.
	host.guideErr = &wsdashboard.DaemonControlError{Code: "guide_unavailable", Reason: "client_unsupported", Message: "reload"}
	res = callToolOnce(t, s, 30, daemonHighlightTool, map[string]any{"session_key": bodyKey, "action": "clear_all"})
	if !toolIsError(t, res) || !strings.Contains(res, "client_unsupported") {
		t.Fatalf("guide error passthrough = %s", res)
	}

	// A host without DaemonGuideHost answers guide_unavailable.
	s.daemonControl = &fakeDaemonControlHost{}
	res = callToolOnce(t, s, 31, daemonHighlightTool, map[string]any{"session_key": bodyKey, "action": "clear_all"})
	if !toolIsError(t, res) || !strings.Contains(res, "guide_unavailable") {
		t.Fatalf("missing guide host = %s", res)
	}
}

func TestDaemonGuideCompanionWorkNoConfirmation(t *testing.T) {
	s, f := hostedBackendConnection(t, wsagent.BackendClaude, descriptorRegistrations())
	defer s.Close()
	workKey, err := s.sessions.mintWithCompanionRole(f.root, roleLead, "", "", CompanionRoleWork)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := f.records.RecordByReplyID(s.conversation)
	rec.ReplyID = wsmailbox.ReplyID(workKey)
	rec.WSKey = workKey
	rec.CompanionRole = CompanionRoleWork
	f.records.Set(rec)
	s.conversation = rec.ReplyID
	s.hostedKey = workKey
	if bc, ok := s.callerBinding.(boundCallers); ok {
		bc.backends[rec.ReplyID] = wsagent.BackendClaude
	}
	host := newFakeGuideHost()
	s.daemonControl = host
	res := callToolOnce(t, s, 1, daemonHighlightTool, map[string]any{"session_key": workKey, "action": "show", "target": map[string]any{"text": "Settings"}})
	if toolIsError(t, res) {
		t.Fatalf("work highlight: %s", res)
	}
	res = callToolOnce(t, s, 2, daemonMonitorTool, map[string]any{"session_key": workKey, "action": "start"})
	if toolIsError(t, res) {
		t.Fatalf("work monitor: %s", res)
	}
	if host.confirmCalls != 0 {
		t.Fatalf("companion-work guide tools requested confirmation %d times", host.confirmCalls)
	}
}
