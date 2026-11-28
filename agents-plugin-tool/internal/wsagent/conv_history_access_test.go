package wsagent

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestRecordConvHistoryAccessT0UnknownFieldRoundTrip(t *testing.T) {
	raw := `{"id":"lead-t0","backend":"claude","lead":true,"conv_history_access":true,"future_flag":"preserve-me","future_nested":{"a":1}}`
	var rec Record
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !rec.ConvHistoryAccess {
		t.Fatalf("ConvHistoryAccess = false, want true")
	}
	out, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("Unmarshal round-trip: %v", err)
	}
	if decoded["conv_history_access"] != true {
		t.Fatalf("round-trip conv_history_access = %#v, want true", decoded["conv_history_access"])
	}
	if decoded["future_flag"] != "preserve-me" {
		t.Fatalf("unknown field lost on round-trip: %s", out)
	}

	// When false, conv_history_access is omitted (T0 clean wire shape).
	rec.ConvHistoryAccess = false
	outFalse, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal false: %v", err)
	}
	var decodedFalse map[string]any
	if err := json.Unmarshal(outFalse, &decodedFalse); err != nil {
		t.Fatalf("Unmarshal false: %v", err)
	}
	if _, exists := decodedFalse["conv_history_access"]; exists {
		t.Fatalf("false conv_history_access should be omitted: %s", outFalse)
	}
	if decodedFalse["future_flag"] != "preserve-me" {
		t.Fatalf("unknown field lost when conv_history_access=false: %s", outFalse)
	}
}

func TestConvHistoryAccessPermissionRoutesAndLiveToggleNoStop(t *testing.T) {
	h := permissionHarness(t)
	h.opts.WSMCP = func(string) (MCPCommand, error) { return MCPCommand{Command: "/fake/ws-mcp"}, nil }
	h.start()

	// 1. Strict permission decoder rejects null and non-bool conv_history_access with HTTP 400.
	for _, bad := range []map[string]any{
		{"root": h.root, "harness": BackendClaude, "permission_level": "read-only", "conv_history_access": nil},
		{"root": h.root, "harness": BackendClaude, "permission_level": "read-only", "conv_history_access": "yes"},
	} {
		res := h.route(localBrowser, http.MethodPost, "/agents/leads/permission", bad)
		if res.status != 400 {
			t.Fatalf("create permission with %v: status=%d body=%s, want 400", bad["conv_history_access"], res.status, res.body)
		}
	}

	// 2. Create lead via /agents/leads/permission with conv_history_access: true.
	res := h.route(localBrowser, http.MethodPost, "/agents/leads/permission", map[string]any{
		"root":                h.root,
		"harness":             BackendClaude,
		"permission_level":    "read-only",
		"conv_history_access": true,
	})
	if res.status != 201 {
		t.Fatalf("create permission with conv_history_access=true: %d %s", res.status, res.body)
	}
	st := decodeState(t, res)
	if !st.ConvHistoryAccess || !st.ConvHistoryAccessEffective {
		t.Fatalf("created state flags: persisted=%v effective=%v, want true/true", st.ConvHistoryAccess, st.ConvHistoryAccessEffective)
	}
	if !h.runner.ConvHistoryAccess(st.ID) {
		t.Fatalf("runner.ConvHistoryAccess(%s) = false, want true", st.ID)
	}

	// 3. Strict /settings/permission rejects null conv_history_access and accepts boolean.
	badSettings := h.route(localBrowser, http.MethodPost, "/agents/"+st.ID+"/settings/permission", map[string]any{
		"permission_level":    "read-only",
		"conv_history_access": nil,
	})
	if badSettings.status != 400 {
		t.Fatalf("settings/permission null conv_history_access: status=%d body=%s, want 400", badSettings.status, badSettings.body)
	}

	// 4. Start the lead's process, then toggle conv_history_access via plain
	// /agents/<id>/settings WITHOUT stopping the running lead.
	h.send(st.ID, "hello")
	h.quiesce(st.ID)
	runningBefore := h.state(st.ID)
	if !runningBefore.Process {
		t.Fatalf("expected started lead to have a running process: %+v", runningBefore)
	}
	if !runningBefore.ConvHistoryAccess || !runningBefore.ConvHistoryAccessEffective {
		t.Fatalf("running flags before toggle: persisted=%v effective=%v, want true/true", runningBefore.ConvHistoryAccess, runningBefore.ConvHistoryAccessEffective)
	}
	rOff := h.route(localBrowser, http.MethodPost, "/agents/"+st.ID+"/settings", map[string]any{
		"conv_history_access": false,
	})
	if rOff.status != 200 {
		t.Fatalf("plain settings toggle off: %d %s", rOff.status, rOff.body)
	}
	stOff := decodeState(t, rOff)
	if !stOff.Process {
		t.Fatalf("plain settings conv_history_access toggle stopped the running lead!")
	}
	if stOff.ConvHistoryAccess {
		t.Fatalf("persisted ConvHistoryAccess = true after toggle off")
	}
	if !stOff.ConvHistoryAccessEffective {
		t.Fatalf("effective ConvHistoryAccessEffective should remain true (start-time snapshot) while still running")
	}
	if h.runner.ConvHistoryAccess(st.ID) {
		t.Fatalf("runner.ConvHistoryAccess(%s) should reflect live persisted false immediately", st.ID)
	}

	// 5. After explicit stop, ConvHistoryAccessEffective matches persisted value (no restart banner needed).
	stopRes := h.route(localBrowser, http.MethodPost, "/agents/"+st.ID+"/stop", map[string]any{})
	if stopRes.status != 200 {
		t.Fatalf("stop: %d %s", stopRes.status, stopRes.body)
	}
	stStopped := h.state(st.ID)
	if stStopped.Process {
		t.Fatalf("expected stopped state")
	}
	if stStopped.ConvHistoryAccess || stStopped.ConvHistoryAccessEffective {
		t.Fatalf("stopped flags: persisted=%v effective=%v, want false/false", stStopped.ConvHistoryAccess, stStopped.ConvHistoryAccessEffective)
	}

	// 6. Plain /agents/leads also accepts conv_history_access: true.
	res2 := h.route(localBrowser, http.MethodPost, "/agents/leads", map[string]any{
		"root":                h.root,
		"harness":             BackendClaude,
		"conv_history_access": true,
	})
	if res2.status != 201 {
		t.Fatalf("create plain lead with conv_history_access=true: %d %s", res2.status, res2.body)
	}
	st2 := decodeState(t, res2)
	if !st2.ConvHistoryAccess || !st2.ConvHistoryAccessEffective {
		t.Fatalf("plain lead flags: persisted=%v effective=%v, want true/true", st2.ConvHistoryAccess, st2.ConvHistoryAccessEffective)
	}

	// 7. ReviveLead unarchives hidden lead and reopens closed lead.
	if rHide := h.route(localBrowser, http.MethodPost, "/agents/"+st.ID+"/hide", map[string]any{}); rHide.status != 200 {
		t.Fatalf("hide lead: %d %s", rHide.status, rHide.body)
	}
	if err := h.runner.ReviveLead(st.ID); err != nil {
		t.Fatalf("ReviveLead archived: %v", err)
	}
	if h.state(st.ID).Hidden {
		t.Fatalf("ReviveLead did not unhide lead")
	}
	if _, err := h.runner.CloseConversation(st.ID); err != nil {
		t.Fatalf("CloseConversation: %v", err)
	}
	if err := h.runner.ReviveLead(st.ID); err != nil {
		t.Fatalf("ReviveLead closed: %v", err)
	}
	if stReopened := h.state(st.ID); stReopened.Status == "closed" || stReopened.Hidden {
		t.Fatalf("ReviveLead did not reopen closed lead: %+v", stReopened)
	}
}

func TestConvHistoryAccessTokenHelloSnapshotRejoinAndChildRefusal(t *testing.T) {
	h := cutoverHarness(t)
	st := h.lead(BackendClaude, LeadOptions{PermissionMode: "default", ConvHistoryAccess: true})
	_, rec := leadLaunch(t, h, st.ID)
	if rec.MCPToken == "" {
		t.Fatal("expected lead to carry an HTTP ws-mcp token")
	}
	if !rec.MCPConvHistoryAccess {
		t.Fatalf("record MCPConvHistoryAccess = false, want true")
	}

	// 1. ResolveMCPToken yields ProxyHello.ConvHistoryAccess == true (start-time snapshot).
	hello, _, ok := h.runner.ResolveMCPToken(rec.MCPToken)
	if !ok || !hello.ConvHistoryAccess {
		t.Fatalf("ResolveMCPToken initial hello = %+v (ok=%v), want ConvHistoryAccess=true", hello, ok)
	}

	// 2. Live toggle off: persisted flag flips immediately, but the running process's
	// MCP token keeps start-time snapshot ConvHistoryAccess=true (drives tools/list)
	// while Runner.ConvHistoryAccess(id) returns false immediately (call-time revoke).
	rOff := h.route(localBrowser, http.MethodPost, "/agents/"+st.ID+"/settings", map[string]any{
		"conv_history_access": false,
	})
	if rOff.status != http.StatusOK {
		t.Fatalf("live toggle off status = %d body=%s", rOff.status, rOff.body)
	}
	if h.runner.ConvHistoryAccess(st.ID) {
		t.Fatalf("Runner.ConvHistoryAccess(%s) = true after live toggle off, want false", st.ID)
	}
	helloAfterOff, _, ok := h.runner.ResolveMCPToken(rec.MCPToken)
	if !ok || !helloAfterOff.ConvHistoryAccess {
		t.Fatalf("running token hello after live toggle off = %+v (ok=%v), want start-time snapshot true", helloAfterOff, ok)
	}

	// 3. Daemon restart rejoins the running holder and restores MCPConvHistoryAccess
	// into the rejoined token's ProxyHello.ConvHistoryAccess and ConvHistoryAccessEffective.
	h.runner.Close()
	h.holders.restart()
	h.runner = NewRunner(h.opts)
	h.runner.SetEndpoint("http://127.0.0.1:1")
	h.runner.Start(func(string, string) {})
	h.runner.BindExecJobs(h.execJobs)
	t.Cleanup(h.runner.Close)
	h.quiesce(st.ID)

	rejoinedHello, _, ok := h.runner.ResolveMCPToken(rec.MCPToken)
	if !ok || !rejoinedHello.ConvHistoryAccess {
		t.Fatalf("rejoined token hello = %+v (ok=%v), want ConvHistoryAccess=true", rejoinedHello, ok)
	}
	rejoinedState := h.state(st.ID)
	if rejoinedState.ConvHistoryAccess || !rejoinedState.ConvHistoryAccessEffective {
		t.Fatalf("rejoined state flags: persisted=%v effective=%v, want false/true",
			rejoinedState.ConvHistoryAccess, rejoinedState.ConvHistoryAccessEffective)
	}

	// 4. Child/worker sessions are refused HTTP 403 on both /settings and /settings/permission
	// when attempting to set conv_history_access.
	ch, _ := agentLeadHarness(t)
	parentLead := ch.lead(BackendClaude, LeadOptions{PermissionMode: "auto"})
	spawned, err := ch.runner.AgentSpawn(ch.caller(parentLead), AgentSpawnRequest{
		Prompt:    "child task",
		Tier:      "claude.small",
		Authority: "read-only",
	})
	if err != nil {
		t.Fatalf("AgentSpawn child worker: %v", err)
	}
	childID := agentID(spawned.Address)
	if ch.runner.ConvHistoryAccess(childID) {
		t.Fatalf("child worker ConvHistoryAccess = true, want false")
	}
	for _, tc := range []struct {
		path string
		body map[string]any
	}{
		{"/agents/" + childID + "/settings", map[string]any{"conv_history_access": true}},
		{"/agents/" + childID + "/settings/permission", map[string]any{"permission_level": "read-only", "conv_history_access": true}},
	} {
		resp := ch.route(localBrowser, http.MethodPost, tc.path, tc.body)
		if resp.status != http.StatusForbidden {
			t.Fatalf("POST %s on child worker: status=%d body=%s, want 403", tc.path, resp.status, resp.body)
		}
	}
}
