package wsagent

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

func TestDashboardControlRecordT0RoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dashctrl-t0")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Default (false) omits dashboard_control_access and mcp_dashboard_control.
	baseRec := Record{
		SchemaVersion: 1,
		ReplyID:       "dashctrl-t0",
		Profile:       LeadProfileClaude,
		Backend:       BackendClaude,
		Root:          "workspace",
		WSKey:         "private-key",
		Lead:          true,
		CreatedAt:     "2026-11-28T00:00:00Z",
	}
	if err := writeRecord(dir, baseRec); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(recordPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "dashboard_control_access") || strings.Contains(string(raw), "mcp_dashboard_control") {
		t.Fatalf("default false serialized dashboard control fields: %s", raw)
	}

	// Enabled record persists both fields and preserves unknown sibling fields.
	baseRec.DashboardControlAccess = true
	baseRec.MCPDashboardControl = true
	if err := writeRecord(dir, baseRec); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(recordPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["dashboard_control_access"] != true || persisted["mcp_dashboard_control"] != true {
		t.Fatalf("expected persisted dashboard control flags: %s", raw)
	}
	// Simulate an older daemon reading into a Record shape that treats
	// dashboard_control_access as an unknown top-level key and rewrites it.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	obj["label"] = json.RawMessage(`"older-daemon-edit"`)
	rewritten, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath(dir), rewritten, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readRecord(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.DashboardControlAccess || !got.MCPDashboardControl || got.Label != "older-daemon-edit" {
		t.Fatalf("round-trip lost fields: %+v", got)
	}

	// Registration must NEVER carry dashboard_control_access.
	reg := registration(got, time.Now())
	regRaw, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(regRaw), "dashboard_control") {
		t.Fatalf("wsdashboard.Registration must not store dashboard_control_access: %s", regRaw)
	}
}

func TestDashboardControlHTTPRoutes(t *testing.T) {
	h := permissionHarness(t)
	h.opts.heartbeat = time.Hour
	h.opts.WSMCP = func(string) (MCPCommand, error) { return MCPCommand{Command: "/fake/ws-mcp"}, nil }
	h.start()
	h.globalProfile("permission-worker", permissionWorkerProfile)

	// Create via /agents/leads with dashboard_control_access: true
	res := h.route(localBrowser, http.MethodPost, "/agents/leads", map[string]any{
		"root":                     h.root,
		"harness":                  BackendClaude,
		"dashboard_control_access": true,
	})
	if res.status != http.StatusCreated {
		t.Fatalf("create /agents/leads: %d %s", res.status, res.body)
	}
	st := decodeState(t, res)
	if !st.DashboardControlAccess || !st.DashboardControlAccessEffective {
		t.Fatalf("created lead state = %+v", st)
	}

	// Create via /agents/leads/permission with dashboard_control_access: true
	res = h.route(localBrowser, http.MethodPost, "/agents/leads/permission", map[string]any{
		"root":                     h.root,
		"harness":                  BackendClaude,
		"permission_level":         "read-only",
		"dashboard_control_access": true,
	})
	if res.status != http.StatusCreated {
		t.Fatalf("create /agents/leads/permission: %d %s", res.status, res.body)
	}
	stPerm := decodeState(t, res)
	if !stPerm.DashboardControlAccess || stPerm.PermissionLevel != PermissionReadOnly {
		t.Fatalf("created perm lead state = %+v", stPerm)
	}
	// Null dashboard_control_access rejected on strict /agents/leads/permission.
	res = h.route(localBrowser, http.MethodPost, "/agents/leads/permission", map[string]any{
		"root":                     h.root,
		"harness":                  BackendClaude,
		"permission_level":         "read-only",
		"dashboard_control_access": nil,
	})
	if res.status != http.StatusBadRequest {
		t.Fatalf("null on leads/permission: %d %s", res.status, res.body)
	}

	// Plain lead without permission.json sidecar.
	lead := h.lead(BackendClaude, LeadOptions{})
	wait(t, "initial holder scan", h.runner.rejoined)
	h.mailSettled(lead.ID)
	permSidecar := filepath.Join(wsdashboard.SessionDir(h.dir, lead.ID), "permission.json")
	if _, err := os.Stat(permSidecar); !os.IsNotExist(err) {
		t.Fatalf("expected no permission sidecar before toggle: %v", err)
	}

	// Strict route: non-trusted viewers rejected with 404/403 and zero writes.
	strictPath := "/agents/" + lead.ID + "/settings/dashboard-control"
	for _, tc := range []struct {
		v      viewer
		status int
	}{{lanBrowser, http.StatusNotFound}, {localClient, http.StatusForbidden}} {
		before := permissionHTTPFiles(t, h.dir)
		r := h.route(tc.v, http.MethodPost, strictPath, map[string]any{"dashboard_control_access": true})
		if r.status != tc.status || !reflect.DeepEqual(before, permissionHTTPFiles(t, h.dir)) {
			t.Fatalf("strict gate %v: %d %s", tc.v, r.status, r.body)
		}
	}

	// Strict route: invalid payloads rejected with 400 and zero writes.
	for _, bad := range []map[string]any{
		{},
		{"dashboard_control_access": nil},
		{"dashboard_control_access": "true"},
		{"dashboard_control_access": true, "unknown": 1},
		{"dashboard_control_access": true, "permission_level": "full"},
	} {
		before := permissionHTTPFiles(t, h.dir)
		r := h.route(localBrowser, http.MethodPost, strictPath, bad)
		if r.status != http.StatusBadRequest || !reflect.DeepEqual(before, permissionHTTPFiles(t, h.dir)) {
			t.Fatalf("strict invalid %v: %d %s", bad, r.status, r.body)
		}
	}

	// Strict route: lone toggle succeeds and writes NO permission.json sidecar.
	res = h.route(localBrowser, http.MethodPost, strictPath, map[string]any{"dashboard_control_access": true})
	if res.status != http.StatusOK {
		t.Fatalf("strict enable: %d %s", res.status, res.body)
	}
	if got := decodeState(t, res); !got.DashboardControlAccess || !got.DashboardControlAccessEffective {
		t.Fatalf("after strict enable: %+v", got)
	}
	if _, err := os.Stat(permSidecar); !os.IsNotExist(err) {
		t.Fatalf("strict toggle wrote permission sidecar: %v", err)
	}

	// Permissive /agents/<id>/settings: invalid model must NOT partially write dashboard_control_access.
	settingsPath := "/agents/" + lead.ID + "/settings"
	before := permissionHTTPFiles(t, h.dir)
	res = h.route(localBrowser, http.MethodPost, settingsPath, map[string]any{
		"dashboard_control_access": false,
		"model":                    "bad model",
	})
	if res.status != http.StatusBadRequest || !reflect.DeepEqual(before, permissionHTTPFiles(t, h.dir)) {
		t.Fatalf("settings invalid partial write: %d %s", res.status, res.body)
	}
	if !h.state(lead.ID).DashboardControlAccess {
		t.Fatal("invalid settings request mutated dashboard_control_access")
	}

	// Permissive /agents/<id>/settings: lone toggle disables cleanly without permission sidecar.
	res = h.route(localBrowser, http.MethodPost, settingsPath, map[string]any{"dashboard_control_access": false})
	if res.status != http.StatusOK {
		t.Fatalf("settings lone toggle: %d %s", res.status, res.body)
	}
	if got := decodeState(t, res); got.DashboardControlAccess || got.DashboardControlAccessEffective {
		t.Fatalf("after settings lone disable: %+v", got)
	}
	if _, err := os.Stat(permSidecar); !os.IsNotExist(err) {
		t.Fatalf("settings lone toggle wrote permission sidecar: %v", err)
	}

	// Combined /agents/<id>/settings/permission accepts boolean dashboard_control_access.
	res = h.route(localBrowser, http.MethodPost, "/agents/"+lead.ID+"/settings/permission", map[string]any{
		"permission_level":         "read-only",
		"dashboard_control_access": true,
	})
	if res.status != http.StatusOK {
		t.Fatalf("settings/permission combined: %d %s", res.status, res.body)
	}
	if got := decodeState(t, res); !got.DashboardControlAccess || got.PermissionLevel != PermissionReadOnly {
		t.Fatalf("after settings/permission combined: %+v", got)
	}

	// Worker conversation refuses dashboard_control_access on strict and permissive routes.
	worker, err := h.runner.Create("permission-worker", h.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/agents/" + worker.ID + "/settings/dashboard-control", "/agents/" + worker.ID + "/settings"} {
		r := h.route(localBrowser, http.MethodPost, path, map[string]any{"dashboard_control_access": true})
		if r.status != http.StatusBadRequest {
			t.Fatalf("worker %s: %d %s", path, r.status, r.body)
		}
	}

	// Support advertises dashboard_control on lead harnesses (both built-in
	// support profiles and lead-only backends such as agy/werk).
	if h.runner.opts.Backends == nil {
		h.runner.opts.Backends = map[string]BackendRegistration{}
	}
	h.runner.opts.Backends["agy-lead-test"] = BackendRegistration{
		LeadProfile: "agy-lead-test",
		Backend:     h.runner.opts.Backends[BackendClaude].Backend,
	}
	s := NewSupport(SupportOptions{Runner: h.runner, Dir: t.TempDir()})
	choices := s.Choices(context.Background())
	foundLead, foundExtraLead := false, false
	for _, entry := range choices.Harnesses {
		if entry.Lead {
			foundLead = true
			if entry.Harness == "agy-lead-test" {
				foundExtraLead = true
			}
			if !entry.DashboardControl {
				t.Fatalf("lead harness %s omits dashboard_control support", entry.Harness)
			}
		} else if entry.DashboardControl {
			t.Fatalf("non-lead harness %s advertises dashboard_control", entry.Harness)
		}
	}
	if !foundLead || !foundExtraLead {
		t.Fatalf("support choices missing lead harnesses: foundLead=%v foundExtraLead=%v", foundLead, foundExtraLead)
	}
}

func TestDashboardControlEffectiveSnapshotAndLiveToggle(t *testing.T) {
	h := newLeadHarness(t)
	h.opts.WSMCP = func(string) (MCPCommand, error) {
		return MCPCommand{Command: "/fake/ws-mcp", Env: map[string]string{"WS_NS": "ws"}}, nil
	}
	h.start()
	h.runner.SetEndpoint("http://127.0.0.1:4242")

	lead := h.lead(BackendClaude, LeadOptions{})
	if lead.DashboardControlAccess || lead.DashboardControlAccessEffective {
		t.Fatalf("initial state = %+v", lead)
	}

	// Start the process with dashboard_control_access=false.
	h.send(lead.ID, "hello")
	holder := h.holder(lead.ID)
	h.holders.out(holder, stateLine(HarnessRunning), initLine("sess-dc-1", "default"), replyLine("u1", "ok"), resultLine(), stateLine(HarnessIdle))
	h.idle(lead.ID)

	st := h.state(lead.ID)
	if !st.Process || st.DashboardControlAccess || st.DashboardControlAccessEffective {
		t.Fatalf("running false state = %+v", st)
	}
	rec, ok := h.runner.RecordByReplyID(lead.ID)
	if !ok || rec.MCPToken == "" || rec.MCPDashboardControl {
		t.Fatalf("record after false start = %+v (ok=%v)", rec, ok)
	}
	hello, _, ok := h.runner.ResolveMCPToken(rec.MCPToken)
	if !ok || hello.DashboardControlAccess {
		t.Fatalf("token hello after false start: ok=%v access=%v", ok, hello.DashboardControlAccess)
	}

	// Enable while running: must NOT stop the process; persisted flips true, effective stays false.
	res := h.route(localBrowser, http.MethodPost, "/agents/"+lead.ID+"/settings/dashboard-control", map[string]any{"dashboard_control_access": true})
	if res.status != http.StatusOK {
		t.Fatalf("live enable: %d %s", res.status, res.body)
	}
	st = decodeState(t, res)
	if !st.Process || !st.DashboardControlAccess || st.DashboardControlAccessEffective {
		t.Fatalf("after live enable: %+v", st)
	}
	hello, _, ok = h.runner.ResolveMCPToken(rec.MCPToken)
	if !ok || hello.DashboardControlAccess {
		t.Fatalf("live enable must not retroactively flip attached token hello: ok=%v access=%v", ok, hello.DashboardControlAccess)
	}
	if cur, _ := h.runner.RecordByReplyID(lead.ID); !cur.DashboardControlAccess {
		t.Fatalf("RecordByReplyID did not reflect persisted enable")
	}

	// Stop the process: effective now tracks persisted (true).
	if _, err := h.runner.Stop(lead.ID); err != nil {
		t.Fatal(err)
	}
	st = h.state(lead.ID)
	if st.Process || !st.DashboardControlAccess || !st.DashboardControlAccessEffective {
		t.Fatalf("after stop: %+v", st)
	}

	// Restart process via next send: effective is true and new MCP token resolves true.
	h.send(lead.ID, "again")
	holder2 := h.holder(lead.ID)
	h.holders.out(holder2, stateLine(HarnessRunning), initLine("sess-dc-1", "default"), replyLine("u2", "ok2"), resultLine(), stateLine(HarnessIdle))
	h.idle(lead.ID)

	st = h.state(lead.ID)
	if !st.Process || !st.DashboardControlAccess || !st.DashboardControlAccessEffective {
		t.Fatalf("after restart enabled: %+v", st)
	}
	rec2, _ := h.runner.RecordByReplyID(lead.ID)
	if rec2.MCPToken == "" || !rec2.MCPDashboardControl {
		t.Fatalf("record after enabled start = %+v", rec2)
	}
	hello2, _, ok := h.runner.ResolveMCPToken(rec2.MCPToken)
	if !ok || !hello2.DashboardControlAccess {
		t.Fatalf("token hello after enabled start: ok=%v access=%v", ok, hello2.DashboardControlAccess)
	}

	// Revoke while running: must NOT stop process; RecordByReplyID flips false immediately
	// while effective stays true (so UI shows restart badge and MCP call guard denies via persisted&&effective).
	res = h.route(localBrowser, http.MethodPost, "/agents/"+lead.ID+"/settings", map[string]any{"dashboard_control_access": false})
	if res.status != http.StatusOK {
		t.Fatalf("live revoke: %d %s", res.status, res.body)
	}
	st = decodeState(t, res)
	if !st.Process || st.DashboardControlAccess || !st.DashboardControlAccessEffective {
		t.Fatalf("after live revoke: %+v", st)
	}
	if cur, _ := h.runner.RecordByReplyID(lead.ID); cur.DashboardControlAccess {
		t.Fatalf("RecordByReplyID did not immediately reflect live revoke")
	}
}
