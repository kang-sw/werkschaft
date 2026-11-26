package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wscompact"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsconfigservice"
)

func TestCompactionLeadModeConfigResolutionAndWSOnlyGuard(t *testing.T) {
	s, _, key := manifestServer(t)
	s.observeHarness("test", wsagent.BackendClaude)

	// Default is ws for external harnesses.
	rv, err := s.resolveConfigGet(key, wsconfig.ItemCompactionLeadMode)
	if err != nil || rv.Value != wsconfig.CompactionModeWS {
		t.Fatalf("default claude compaction.lead_mode = (%+v, %v), want ws", rv, err)
	}

	// Explicit werk=native is rejected by config.tune.
	r := keyedCall(t, s, 1, key, "config.tune", map[string]any{
		"key": wsconfig.ItemCompactionLeadMode, "value": wsconfig.CompactionModeNative, "harness": wsagent.BackendWerk, "scope": "global",
	})
	if !toolIsError(t, r) {
		t.Fatalf("expected config.tune to reject compaction.lead_mode.werk=native, got: %s", r)
	}

	// Wildcard *=native opt-out is accepted, resolving to native for claude while staying ws for werk.
	r = keyedCall(t, s, 2, key, "config.tune", map[string]any{
		"key": wsconfig.ItemCompactionLeadMode, "value": wsconfig.CompactionModeNative, "harness": "*", "scope": "global",
	})
	if toolIsError(t, r) {
		t.Fatalf("expected wildcard *=native accepted, got: %s", r)
	}
	rv, err = s.resolveConfigGet(key, wsconfig.ItemCompactionLeadMode)
	if err != nil || rv.Value != wsconfig.CompactionModeNative || rv.Scope != string(wsconfig.ScopeGlobal) {
		t.Fatalf("claude compaction.lead_mode after wildcard native opt-out = (%+v, %v), want native/global", rv, err)
	}
	resolver := s.sessionResolver(key, builtinConfigDefaults())
	werkVal, _, err := resolveCompactionValue(&resolver, key, wsconfig.ItemCompactionLeadMode, wsagent.BackendWerk)
	if err != nil || werkVal.Value != wsconfig.CompactionModeWS {
		t.Fatalf("werk effective compaction.lead_mode = (%+v, %v), want ws", werkVal, err)
	}

	// Explicit ws at project scope shadows global native opt-out for claude.
	r = keyedCall(t, s, 3, key, "config.tune", map[string]any{
		"key": wsconfig.ItemCompactionLeadMode, "value": wsconfig.CompactionModeWS, "harness": wsagent.BackendClaude, "scope": "project",
	})
	if toolIsError(t, r) {
		t.Fatalf("config.tune claude=ws failed: %s", r)
	}
	rv, err = s.resolveConfigGet(key, wsconfig.ItemCompactionLeadMode)
	if err != nil || rv.Value != wsconfig.CompactionModeWS || rv.Scope != string(wsconfig.ScopeProject) {
		t.Fatalf("claude compaction.lead_mode after project tune = (%+v, %v), want ws/project", rv, err)
	}

	// wsconfigservice also rejects explicit werk=native.
	cat, err := DashboardConfigCatalog("ws")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.Validate(wsconfig.ScopeGlobal, wsconfigservice.Operation{
		Key: wsconfig.ItemCompactionLeadMode, Action: "set", Value: wsconfig.CompactionModeNative,
		Selectors: map[string]string{"harness": wsagent.BackendWerk},
	}); err == nil {
		t.Fatal("expected wsconfigservice.Validate to reject compaction.lead_mode.werk=native")
	}
}

func TestDashboardCompactionModeProjectsHarnessPolicy(t *testing.T) {
	catalog, err := DashboardConfigCatalog("ws")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		layers        wsconfigservice.Layers
		werkSource    wsconfig.Scope
		unlockedValue string
	}{
		{name: "builtin", werkSource: wsconfig.ScopeBuiltin, unlockedValue: wsconfig.CompactionModeWS},
		{name: "wildcard native", layers: wsconfigservice.Layers{
			Files: map[wsconfig.Scope]wsconfig.Config{
				wsconfig.ScopeGlobal: {Overrides: map[string]string{wsconfig.ItemCompactionLeadMode: wsconfig.CompactionModeNative}},
			},
		}, werkSource: wsconfig.ScopeGlobal, unlockedValue: wsconfig.CompactionModeNative},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := catalog.KeyEntries(wsconfig.ItemCompactionLeadMode, wsconfig.ScopeGlobal, tc.layers)
			found := false
			for _, entry := range entries {
				switch entry.Selectors["harness"] {
				case wsagent.BackendWerk:
					found = true
					if entry.Default != wsconfig.CompactionModeWS || entry.Value != wsconfig.CompactionModeWS ||
						len(entry.Enum) != 1 || entry.Enum[0] != wsconfig.CompactionModeWS || entry.SourceScope != tc.werkSource {
						t.Fatalf("Werk compaction setting = %+v", entry)
					}
				case wsagent.BackendClaude, "*":
					if entry.Default != wsconfig.CompactionModeWS || entry.Value != tc.unlockedValue || len(entry.Enum) != 2 {
						t.Fatalf("unlocked compaction setting = %+v", entry)
					}
				}
			}
			if !found {
				t.Fatal("missing Werk compaction setting")
			}
		})
	}
}

type compactSchemaRunner struct {
	AgentHost
	state       wsagent.State
	mode        string
	calledWith  *wscompact.LeadSummary
	calledNoArg bool
}

func (*compactSchemaRunner) Watch(func(wsagent.State)) func() { return func() {} }
func (r *compactSchemaRunner) LeadCompactionMode(conversation string) string {
	if r.state.ID != conversation || !r.state.Lead {
		return ""
	}
	return wsagent.EffectiveLeadCompactionMode(r.state.Backend, r.mode)
}
func (r *compactSchemaRunner) Compact(string, string, *bool) wsagent.CompactResult {
	r.calledNoArg = true
	return wsagent.CompactResult{Status: "queued", Reason: "native"}
}
func (r *compactSchemaRunner) CompactWithSummary(_ string, _ string, _ *bool, lead *wscompact.LeadSummary) wsagent.CompactResult {
	r.calledWith = lead
	return wsagent.CompactResult{Status: "queued", Reason: "ws"}
}

func compactToolHasLeadSummary(t *testing.T, s *Server) (bool, int) {
	t.Helper()
	for _, tool := range s.filteredTools() {
		if tool["name"] == "compact" {
			raw, err := json.Marshal(tool)
			if err != nil {
				t.Fatal(err)
			}
			schema, _ := tool["inputSchema"].(map[string]any)
			props, _ := schema["properties"].(map[string]any)
			_, has := props["lead_summary"]
			return has, len(raw)
		}
	}
	t.Fatal("compact tool missing from filteredTools()")
	return false, 0
}

func TestCompactToolListConditionalLeadSummaryAndAgyWSCall(t *testing.T) {
	setupMailboxTestEnv(t)
	root := t.TempDir()

	// 1. Non-hosted CLI session: stripped.
	cli := NewServerWithOptions(ServerOptions{Root: root, Version: "test"})
	t.Cleanup(cli.Close)
	if has, size := compactToolHasLeadSummary(t, cli); has || size > toolSchemaByteCap {
		t.Fatalf("CLI compact: hasLeadSummary=%v size=%d", has, size)
	}

	// 2. Hosted worker: stripped even if compaction.lead_mode=ws.
	workerRunner := &compactSchemaRunner{state: wsagent.State{ID: "w1", Lead: false, Backend: wsagent.BackendClaude}}
	ws := NewServerWithOptions(ServerOptions{Root: root, Version: "test", Agents: workerRunner})
	t.Cleanup(ws.Close)
	ws.conversation = "w1"
	if has, _ := compactToolHasLeadSummary(t, ws); has {
		t.Fatal("worker should not see lead_summary in tools/list")
	}

	// 3. Hosted external lead with default ws mode: tools/list and ws_call schema discovery expose lead_summary.
	leadRunner := &compactSchemaRunner{state: wsagent.State{ID: "lead1", Lead: true, Backend: wsagent.BackendAgy}}
	ls := NewServerWithOptions(ServerOptions{Root: root, Version: "test", Agents: leadRunner})
	t.Cleanup(ls.Close)
	ls.conversation = "lead1"
	ls.observeHarness("test", wsagent.BackendAgy)
	ls.descriptor = connectionDescriptor{callFallback: true}
	ls.descriptorResolved = true
	leadKey, err := ls.sessions.mint(root, roleLead, "")
	if err != nil {
		t.Fatal(err)
	}
	has, size := compactToolHasLeadSummary(t, ls)
	if !has {
		t.Fatal("default ws-mode hosted lead should see lead_summary in tools/list")
	}
	if size > toolSchemaByteCap {
		t.Fatalf("compact schema with lead_summary = %d bytes, exceeds cap %d", size, toolSchemaByteCap)
	}

	// agy ws_call schema discovery reflects lead_summary under default ws mode.
	schemaResp := callToolOnce(t, ls, 1, "call", map[string]any{
		"tool": "compact",
	})
	if toolIsError(t, schemaResp) || !strings.Contains(toolText(t, schemaResp), `"lead_summary"`) {
		t.Fatalf("agy ws_call schema discovery missing lead_summary: %s", toolText(t, schemaResp))
	}

	// 4. Opt-out compaction.lead_mode=native for agy -> lead_summary stripped from tools/list.
	tuneResp := keyedCall(t, ls, 2, leadKey, "config.tune", map[string]any{
		"key": wsconfig.ItemCompactionLeadMode, "value": wsconfig.CompactionModeNative, "harness": wsagent.BackendAgy, "scope": "project",
	})
	if toolIsError(t, tuneResp) {
		t.Fatalf("config.tune agy=native failed: %s", tuneResp)
	}
	leadRunner.mode = wsconfig.CompactionModeNative
	if has, _ := compactToolHasLeadSummary(t, ls); has {
		t.Fatal("native-mode hosted lead should not see lead_summary in tools/list")
	}

	// Single server acceptance contract: even while native-mode (lead_summary stripped
	// from tools/list), calling compact with lead_summary via call succeeds!
	summaryArg := map[string]any{
		"user_preferences": "concise", "working_practices": "", "unrecorded_decisions": "",
		"current_work": "phase 1", "next_step": "test", "mood_rapport": "", "residual_details": "",
	}
	callResp := callToolOnce(t, ls, 3, "call", map[string]any{
		"tool":      "compact",
		"arguments": map[string]any{"session_key": leadKey, "lead_summary": summaryArg},
	})
	if toolIsError(t, callResp) || leadRunner.calledWith == nil {
		t.Fatalf("ws_call compact(lead_summary) should succeed under single acceptance contract: %+v", callResp)
	}

	// 5. Hosted werk lead always sees lead_summary even when native mode is requested.
	werkRunner := &compactSchemaRunner{state: wsagent.State{ID: "werk1", Lead: true, Backend: wsagent.BackendWerk}, mode: wsconfig.CompactionModeNative}
	werks := NewServerWithOptions(ServerOptions{Root: root, Version: "test", Agents: werkRunner})
	t.Cleanup(werks.Close)
	werks.conversation = "werk1"
	if has, _ := compactToolHasLeadSummary(t, werks); !has {
		t.Fatal("werk hosted lead should always see lead_summary in tools/list")
	}
}
