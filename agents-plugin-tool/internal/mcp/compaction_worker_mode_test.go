package mcp

import (
	"encoding/json"
	"testing"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsconfigservice"
)

func TestCompactionWorkerModeConfigResolutionAndWerkFixedWS(t *testing.T) {
	s, _, key := manifestServer(t)
	s.observeHarness("test", wsagent.BackendClaude)

	rv, err := s.resolveConfigGet(key, wsconfig.ItemCompactionWorkerMode)
	if err != nil || rv.Value != wsconfig.CompactionModeWS {
		t.Fatalf("default compaction.worker_mode = (%+v, %v), want ws", rv, err)
	}
	r := keyedCall(t, s, 1, key, "config.tune", map[string]any{
		"key": wsconfig.ItemCompactionWorkerMode, "value": wsconfig.CompactionModeNative, "harness": wsagent.BackendWerk, "scope": "global",
	})
	if !toolIsError(t, r) {
		t.Fatalf("expected config.tune to reject compaction.worker_mode.werk=native, got: %s", r)
	}
	r = keyedCall(t, s, 2, key, "config.tune", map[string]any{
		"key": wsconfig.ItemCompactionWorkerMode, "value": wsconfig.CompactionModeNative, "harness": "*", "scope": "global",
	})
	if toolIsError(t, r) {
		t.Fatalf("expected wildcard native accepted, got: %s", r)
	}
	rv, err = s.resolveConfigGet(key, wsconfig.ItemCompactionWorkerMode)
	if err != nil || rv.Value != wsconfig.CompactionModeNative || rv.Scope != string(wsconfig.ScopeGlobal) {
		t.Fatalf("after wildcard native = (%+v, %v)", rv, err)
	}
	resolver := s.sessionResolver(key, builtinConfigDefaults())
	werk, _, err := resolveCompactionValue(&resolver, key, wsconfig.ItemCompactionWorkerMode, wsagent.BackendWerk)
	if err != nil || werk.Value != wsconfig.CompactionModeWS {
		t.Fatalf("werk effective worker mode = (%+v, %v), want ws", werk, err)
	}
	// The lead key is untouched by the worker write.
	lead, err := s.resolveConfigGet(key, wsconfig.ItemCompactionLeadMode)
	if err != nil || lead.Value != wsconfig.CompactionModeWS {
		t.Fatalf("lead mode = (%+v, %v), want ws", lead, err)
	}

	cat, err := DashboardConfigCatalog("ws")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.Validate(wsconfig.ScopeGlobal, wsconfigservice.Operation{
		Key: wsconfig.ItemCompactionWorkerMode, Action: "set", Value: wsconfig.CompactionModeNative,
		Selectors: map[string]string{"harness": wsagent.BackendWerk},
	}); err == nil {
		t.Fatal("expected wsconfigservice.Validate to reject compaction.worker_mode.werk=native")
	}
	if _, err := cat.Validate(wsconfig.ScopeGlobal, wsconfigservice.Operation{
		Key: wsconfig.ItemCompactionWorkerMode, Action: "set", Value: wsconfig.CompactionModeNative,
		Selectors: map[string]string{"harness": wsagent.BackendClaude},
	}); err != nil {
		t.Fatalf("claude native must validate: %v", err)
	}
}

func TestDashboardCompactionWorkerModeProjectsHarnessPolicy(t *testing.T) {
	catalog, err := DashboardConfigCatalog("ws")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range catalog.KeyEntries(wsconfig.ItemCompactionWorkerMode, wsconfig.ScopeGlobal, wsconfigservice.Layers{}) {
		switch entry.Selectors["harness"] {
		case wsagent.BackendWerk:
			found = true
			if entry.Value != wsconfig.CompactionModeWS || len(entry.Enum) != 1 || entry.Enum[0] != wsconfig.CompactionModeWS {
				t.Fatalf("werk worker mode entry = %+v", entry)
			}
		case wsagent.BackendClaude, "*":
			if entry.Default != wsconfig.CompactionModeWS || len(entry.Enum) != 2 {
				t.Fatalf("claude worker mode entry = %+v", entry)
			}
		}
	}
	if !found {
		t.Fatal("missing werk worker mode entry")
	}
}

// The compact tool serves lead_summary to ws-mode leads; agy drops tools over
// 1,500 B including its 39 B description prefix, so the widest variant must
// stay under the cap. Worker mode adds no schema.
func TestCompactToolSchemaStaysUnderAgyLimit(t *testing.T) {
	raw, err := json.Marshal(compactTool())
	if err != nil {
		t.Fatal(err)
	}
	if len(raw)+39 > 1500 || len(raw) > toolSchemaByteCap {
		t.Fatalf("compact tool is %d B (cap %d, agy 1500 incl. 39 B prefix)", len(raw), toolSchemaByteCap)
	}
	stripped, _ := json.Marshal(compactToolWithoutLeadSummary(compactTool()))
	if len(stripped) >= len(raw) {
		t.Fatalf("stripped compact schema (%d B) must be smaller than full (%d B)", len(stripped), len(raw))
	}
}
