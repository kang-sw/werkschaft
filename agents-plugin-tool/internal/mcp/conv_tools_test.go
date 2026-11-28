package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconvidx"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

type fakeConvHost struct {
	mu           sync.Mutex
	access       map[string]bool
	lastSessions wsdashboard.ConvSessionsRequest
	lastSearch   wsdashboard.ConvSearchRequest
	lastHistory  wsdashboard.ConvHistoryRequest
	lastRead     wsdashboard.ConvReadRequest
	lastReveal   wsdashboard.ConvRevealRequest

	sessionsResp wsdashboard.ConvSessionsResponse
	searchResp   wsdashboard.ConvSearchResponse
	historyResp  wsdashboard.ConvHistoryResponse
	readResp     wsdashboard.ConvReadResponse
	revealResp   wsdashboard.ConvRevealResponse
	revealErr    error
}

func (f *fakeConvHost) setAccess(id string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.access == nil {
		f.access = map[string]bool{}
	}
	f.access[id] = on
}

func (f *fakeConvHost) ConvHistoryAccess(replyID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.access[replyID]
}

func (f *fakeConvHost) ConvSessions(_ context.Context, req wsdashboard.ConvSessionsRequest) (wsdashboard.ConvSessionsResponse, error) {
	f.mu.Lock()
	f.lastSessions = req
	resp := f.sessionsResp
	f.mu.Unlock()
	return resp, nil
}

func (f *fakeConvHost) ConvSearch(_ context.Context, req wsdashboard.ConvSearchRequest) (wsdashboard.ConvSearchResponse, error) {
	f.mu.Lock()
	f.lastSearch = req
	resp := f.searchResp
	f.mu.Unlock()
	return resp, nil
}

func (f *fakeConvHost) ConvHistory(_ context.Context, req wsdashboard.ConvHistoryRequest) (wsdashboard.ConvHistoryResponse, error) {
	f.mu.Lock()
	f.lastHistory = req
	resp := f.historyResp
	f.mu.Unlock()
	return resp, nil
}

func (f *fakeConvHost) ConvRead(_ context.Context, req wsdashboard.ConvReadRequest) (wsdashboard.ConvReadResponse, error) {
	f.mu.Lock()
	f.lastRead = req
	resp := f.readResp
	f.mu.Unlock()
	return resp, nil
}

func (f *fakeConvHost) ConvRevealSession(_ context.Context, req wsdashboard.ConvRevealRequest) (wsdashboard.ConvRevealResponse, error) {
	f.mu.Lock()
	f.lastReveal = req
	resp, err := f.revealResp, f.revealErr
	f.mu.Unlock()
	return resp, err
}

func listToolNames(t *testing.T, s *Server) map[string]bool {
	t.Helper()
	resp := s.handle(context.Background(), request{ID: json.RawMessage(`1`), Method: "tools/list"})
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}
	res := resp.Result.(map[string]any)
	tools := res["tools"].([]map[string]any)
	out := make(map[string]bool, len(tools))
	for _, tool := range tools {
		out[tool["name"].(string)] = true
	}
	return out
}

func TestConvToolsListGatingAndAgyDirectTools(t *testing.T) {
	host := &fakeConvHost{access: map[string]bool{"lead-1": true}}
	root := t.TempDir()

	// 1. Default (opt-out): conv.* absent from tools/list on standard and agy sessions.
	sOff := NewServerWithOptions(ServerOptions{
		Root:              root,
		Version:           "test",
		Conversation:      "lead-1",
		Conv:              host,
		ConvHistoryAccess: false,
	})
	listedOff := listToolNames(t, sOff)
	for _, name := range []string{convSessionsTool, convSearchTool, convHistoryTool, convReadTool, convRevealTool} {
		if listedOff[name] {
			t.Fatalf("tools/list leaked %s when ConvHistoryAccess=false", name)
		}
	}

	// 2. Opt-in on standard session: all 5 conv.* tools appear in tools/list.
	sOn := NewServerWithOptions(ServerOptions{
		Root:              root,
		Version:           "test",
		Conversation:      "lead-1",
		Conv:              host,
		ConvHistoryAccess: true,
	})
	listedOn := listToolNames(t, sOn)
	for _, name := range []string{convSessionsTool, convSearchTool, convHistoryTool, convReadTool, convRevealTool} {
		if !listedOn[name] {
			t.Fatalf("tools/list missing %s when ConvHistoryAccess=true", name)
		}
	}

	// 3. Agy DirectTools + ToolNameSchemeSanitize: opt-in merges conv.* directly into tools/list; opt-out hides them.
	sAgyOn, _ := hostedBackendConnection(t, wsagent.BackendAgy, wsagent.BuiltinBackendRegistrations())
	defer sAgyOn.Close()
	sAgyOn.conv = host
	sAgyOn.convHistoryListed = true
	agyDesc := sAgyOn.connectionDescriptor()
	agyListedOn := listToolNames(t, sAgyOn)
	for _, name := range []string{convSessionsTool, convSearchTool, convHistoryTool, convReadTool, convRevealTool} {
		adv := agyDesc.aliases.advertised(name)
		if !agyListedOn[adv] {
			t.Fatalf("agy tools/list missing direct tool %s (canonical %s) when opted in", adv, name)
		}
	}

	sAgyOff, _ := hostedBackendConnection(t, wsagent.BackendAgy, wsagent.BuiltinBackendRegistrations())
	defer sAgyOff.Close()
	sAgyOff.conv = host
	sAgyOff.convHistoryListed = false
	agyListedOff := listToolNames(t, sAgyOff)
	for _, name := range []string{convSessionsTool, convSearchTool, convHistoryTool, convReadTool, convRevealTool} {
		adv := agyDesc.aliases.advertised(name)
		if agyListedOff[adv] {
			t.Fatalf("agy tools/list leaked %s when opted out", adv)
		}
	}
}

func TestConvToolsLiveRevokeAndEnableBeforeRestart(t *testing.T) {
	host := &fakeConvHost{
		access: map[string]bool{"lead-live": true},
		sessionsResp: wsdashboard.ConvSessionsResponse{
			Sessions: []wsdashboard.ConvSessionItem{{SessionID: "sess-a", Status: "active", EntryCount: 4}},
			Total:    1,
			Limit:    20,
		},
	}
	root := t.TempDir()

	// Case A: Started WITH conv_history_access=true, then revoked live before process stop.
	sRevoke := NewServerWithOptions(ServerOptions{
		Root:              root,
		Version:           "test",
		Conversation:      "lead-live",
		Conv:              host,
		ConvHistoryAccess: true,
	})
	sRevoke.descriptorResolved = true
	sRevoke.descriptor = connectionDescriptor{callFallback: true}

	if txt, isErr := invokeCompanionTestTool(t, sRevoke, convSessionsTool, map[string]any{}); isErr || !strings.Contains(txt, "sess-a") {
		t.Fatalf("conv.sessions while enabled = (%q, isErr=%v)", txt, isErr)
	}

	// Revoke live -> immediate refusal on direct call AND call envelope AND hidden from callDiscoveryTools.
	host.setAccess("lead-live", false)
	if txt, isErr := invokeCompanionTestTool(t, sRevoke, convSessionsTool, map[string]any{}); !isErr || !strings.Contains(txt, "permission denied") {
		t.Fatalf("conv.sessions after live revoke = (%q, isErr=%v), want permission denied", txt, isErr)
	}
	if txt, isErr := invokeCompanionTestTool(t, sRevoke, "call", map[string]any{"tool": convSessionsTool, "arguments": map[string]any{}}); !isErr || !strings.Contains(txt, "permission denied") {
		t.Fatalf("call(conv.sessions) after live revoke = (%q, isErr=%v), want permission denied", txt, isErr)
	}
	disc := discoveryToolNames(sRevoke.callDiscoveryTools("", sessionEntry{scope: roleLead}, true))
	if disc[convSessionsTool] {
		t.Fatalf("callDiscoveryTools still advertised conv.sessions after live revoke")
	}

	// Case B (Decision 1643): Started WITH conv_history_access=false, then enabled live without restart.
	// Tools remain UNLISTED in tools/list and call() discovery, yet CALLABLE by name (direct + agy call route).
	sEnable := NewServerWithOptions(ServerOptions{
		Root:              root,
		Version:           "test",
		Conversation:      "lead-live",
		Conv:              host,
		ConvHistoryAccess: false,
	})
	sEnable.descriptorResolved = true
	sEnable.descriptor = connectionDescriptor{callFallback: true, directTools: []string{"todo.list"}}

	host.setAccess("lead-live", true)
	listed := listToolNames(t, sEnable)
	if listed[convSessionsTool] {
		t.Fatalf("enable-before-restart must keep conv.* unlisted in tools/list")
	}
	discUnlisted := discoveryToolNames(sEnable.callDiscoveryTools("", sessionEntry{scope: roleLead}, true))
	if discUnlisted[convSessionsTool] {
		t.Fatalf("enable-before-restart must keep conv.* unlisted in callDiscoveryTools")
	}
	if txt, isErr := invokeCompanionTestTool(t, sEnable, convSessionsTool, map[string]any{}); isErr || !strings.Contains(txt, "sess-a") {
		t.Fatalf("enable-before-restart direct call failed: (%q, isErr=%v)", txt, isErr)
	}
	if txt, isErr := invokeCompanionTestTool(t, sEnable, "call", map[string]any{"tool": convSessionsTool, "arguments": map[string]any{}}); isErr || !strings.Contains(txt, "sess-a") {
		t.Fatalf("enable-before-restart call() route failed: (%q, isErr=%v)", txt, isErr)
	}
}

func TestConvToolsAllFiveHandlersTextAndJSON(t *testing.T) {
	host := &fakeConvHost{
		access: map[string]bool{"lead-all": true},
		sessionsResp: wsdashboard.ConvSessionsResponse{
			Sessions: []wsdashboard.ConvSessionItem{{SessionID: "s1", Label: "Auth work", Workspace: "/ws/a", Status: "archived", EntryCount: 12}},
			Total:    1,
			Limit:    20,
			Indexing: "partial",
		},
		searchResp: wsdashboard.ConvSearchResponse{
			Hits: []wsdashboard.ConvSearchHit{{
				SessionID: "s1", SessionLabel: "Auth work", SessionStatus: "archived",
				EntryID: "1000-e1", Kind: "reply", TS: 1700000000000, Snippet: "JWT middleware fix", Bytes: 24,
			}},
			Indexing: "partial",
		},
		historyResp: wsdashboard.ConvHistoryResponse{
			SessionID: "s1",
			Items:     []wsconvidx.HistoryItem{{EntryID: "1000-e1", Kind: "reply", TS: 1700000000000, Preview: "JWT middleware fix", Bytes: 24}},
			HasMore:   true,
		},
		readResp: wsdashboard.ConvReadResponse{
			SessionID: "s1",
			Mode:      "entry",
			Entry: &wsconvidx.EntryRecord{
				SessionID: "s1", EntryID: "1000-e1", Kind: "reply", TS: 1700000000000, Text: "Full verbatim reply", FullBytes: 19,
			},
			ByteOffset: 0, SliceBytes: 19, NextOffset: 19, HasMore: false,
		},
		revealResp: wsdashboard.ConvRevealResponse{
			SessionID: "s1", EntryID: "1000-e1", Status: "active", Revived: true, Focused: false, Emitted: true,
		},
	}
	root := t.TempDir()
	s := NewServerWithOptions(ServerOptions{
		Root:              root,
		Version:           "test",
		Conversation:      "lead-all",
		Conv:              host,
		ConvHistoryAccess: true,
	})

	// conv.sessions text + json + indexing: partial
	txt, isErr := invokeCompanionTestTool(t, s, convSessionsTool, map[string]any{"status": "archived", "since": "2026-11-01"})
	if isErr || !strings.Contains(txt, "indexing: partial") || !strings.Contains(txt, "s1") {
		t.Fatalf("conv.sessions text = (%q, isErr=%v)", txt, isErr)
	}
	if host.lastSessions.CallerWorkspace != root {
		t.Fatalf("CallerWorkspace = %q, want %q", host.lastSessions.CallerWorkspace, root)
	}
	jsTxt, isErr := invokeCompanionTestTool(t, s, convSessionsTool, map[string]any{"format": "json"})
	if isErr || !strings.Contains(jsTxt, `"indexing":"partial"`) {
		t.Fatalf("conv.sessions json = (%q, isErr=%v)", jsTxt, isErr)
	}

	// conv.search text + json + indexing: partial
	txt, isErr = invokeCompanionTestTool(t, s, convSearchTool, map[string]any{"query": "JWT", "order": "time", "kinds": []any{"reply"}})
	if isErr || !strings.Contains(txt, "indexing: partial") || !strings.Contains(txt, "JWT middleware fix") {
		t.Fatalf("conv.search text = (%q, isErr=%v)", txt, isErr)
	}
	if host.lastSearch.Order != "time" || len(host.lastSearch.Kinds) != 1 || host.lastSearch.Kinds[0] != "reply" {
		t.Fatalf("conv.search forwarded req = %+v", host.lastSearch)
	}

	// conv.history
	txt, isErr = invokeCompanionTestTool(t, s, convHistoryTool, map[string]any{"session_id": "s1", "limit": 10})
	if isErr || !strings.Contains(txt, "1000-e1") || !strings.Contains(txt, "has_more: true") {
		t.Fatalf("conv.history text = (%q, isErr=%v)", txt, isErr)
	}

	// conv.read (single entry + context_before=0 distinguished from omitted)
	txt, isErr = invokeCompanionTestTool(t, s, convReadTool, map[string]any{"session_id": "s1", "entry_id": "1000-e1", "max_bytes": 4096})
	if isErr || !strings.Contains(txt, "Full verbatim reply") {
		t.Fatalf("conv.read text = (%q, isErr=%v)", txt, isErr)
	}
	if host.lastRead.HasBefore || host.lastRead.HasAfter {
		t.Fatalf("expected HasBefore/HasAfter=false when omitted")
	}
	_, isErr = invokeCompanionTestTool(t, s, convReadTool, map[string]any{"session_id": "s1", "around_entry_id": "1000-e1", "context_before": 0})
	if isErr || !host.lastRead.HasBefore || host.lastRead.ContextBefore != 0 {
		t.Fatalf("context_before=0 not distinguished: %+v (isErr=%v)", host.lastRead, isErr)
	}

	// conv.reveal success + error propagation
	txt, isErr = invokeCompanionTestTool(t, s, convRevealTool, map[string]any{"session_id": "s1", "entry_id": "1000-e1", "revive": true, "reason": "found match"})
	if isErr || !strings.Contains(txt, "revived: true") || !strings.Contains(txt, "emitted: true") {
		t.Fatalf("conv.reveal text = (%q, isErr=%v)", txt, isErr)
	}
	host.revealErr = errors.New("cannot revive child session")
	txt, isErr = invokeCompanionTestTool(t, s, convRevealTool, map[string]any{"session_id": "child-1", "revive": true})
	if !isErr || !strings.Contains(txt, "cannot revive child session") {
		t.Fatalf("conv.reveal error = (%q, isErr=%v)", txt, isErr)
	}
}
