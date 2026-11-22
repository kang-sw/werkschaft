package wsconfig

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestSettingsConfigWritePreservesUnknownMembersAndKnownSiblingLeaves(t *testing.T) {
	opts := Options{ConfigHome: t.TempDir(), CacheHome: t.TempDir()}
	path, err := GlobalPath(opts)
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"schema_version":1,"future":{"flag":true},"agents":{"future_agent":{"flag":true},"model_aliases":{"small":{"codex":{"backend":"codex","model":"before","future_leaf":{"flag":true}},"pi":{"backend":"pi","model":"sibling"}},"large":{"codex":{"backend":"codex","model":"untouched"}}}},"overrides":{"unknown.setting":"old"},"structured_overrides":{"worktree.profile.old":{"future_object":{"flag":true}}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SetGlobalAgentsTierForHarness(opts, "small", "codex", "after", "codex"); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(opts, nil, nil, nil)
	if err := resolver.Set("workflow.lang", "Korean", SetOptions{ExplicitScope: ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"future", "future_agent", "future_leaf", "future_object", "after", "sibling", "untouched", "unknown.setting"} {
		if !strings.Contains(string(out), `"`+key+`"`) {
			t.Errorf("write dropped %s: %s", key, out)
		}
	}

	// The prior writer reserialized Config without unknown struct members.
	// Known maps and every previously supported tier leaf survive that rewrite.
	var legacy Config
	if err := json.Unmarshal(out, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy.Overrides["legacy.writer"] = "touched"
	rewritten, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, rewritten, 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := FileLayers(opts)
	if err != nil {
		t.Fatal(err)
	}
	if value := files[ScopeGlobal].Overrides["workflow.lang"]; value != "Korean" {
		t.Errorf("legacy rewrite lost settings value: %q", value)
	}
	if _, removed, err := UnsetGlobalAgentsTierForHarness(opts, "small", "codex"); err != nil || !removed {
		t.Fatalf("remove one leaf: removed=%t, err=%v", removed, err)
	}
	files, err = FileLayers(opts)
	if err != nil {
		t.Fatal(err)
	}
	aliases := files[ScopeGlobal].Agents.ModelAliases
	if _, ok := aliases["small"]["codex"]; ok || aliases["small"]["pi"].Model != "sibling" || aliases["large"]["codex"].Model != "untouched" {
		t.Errorf("reset affected the wrong leaves: %#v", aliases)
	}
	if files[ScopeGlobal].Overrides["legacy.writer"] != "touched" || files[ScopeGlobal].StructuredOverrides["worktree.profile.old"] == nil {
		t.Errorf("new writer lost existing map members: %#v", files)
	}
}

func TestSettingsLegacyTierSeedHasProvenanceWithoutASelectedHarnessOverride(t *testing.T) {
	opts := Options{ConfigHome: t.TempDir(), CacheHome: t.TempDir()}
	path, err := GlobalPath(opts)
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"schema_version":1,"agents":{"tiers":{"small":{"backend":"codex","model":"legacy-small"}}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := FileLayers(opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, harness := range []string{"default", "codex", "pi"} {
		info := InspectTier(files, "small", harness)
		layer := info.Layers[ScopeGlobal]
		if info.Scope != ScopeGlobal || info.Value.Model != "legacy-small" || !layer.Legacy || layer.Selected != nil {
			t.Errorf("legacy seed for %s = %#v", harness, info)
		}
	}
	if info := InspectTier(files, "small", "claude"); info.Scope != ScopeBuiltin || info.Value.Model == "legacy-small" {
		t.Errorf("legacy mapping replaced Claude's builtin alias: %#v", info)
	}
	if _, removed, err := UnsetGlobalAgentsTierForHarness(opts, "small", "codex"); err != nil || removed {
		t.Errorf("leaf reset removed a legacy tier: removed=%t, err=%v", removed, err)
	}
	out, err := os.ReadFile(path)
	if err != nil || string(out) != raw {
		t.Errorf("no-op reset changed the legacy file: %s, %v", out, err)
	}
}

// TestSettingsConfigWriteFoldsDifferentlyCasedKnownMembers: the reader
// matches member names case-insensitively, so a hand-edited "Overrides" (or
// "Model_Aliases", "Backend") is the known field. A write replaces it with
// the canonical spelling instead of keeping it as an unknown member that the
// next load would merge back.
func TestSettingsConfigWriteFoldsDifferentlyCasedKnownMembers(t *testing.T) {
	opts := Options{ConfigHome: t.TempDir(), CacheHome: t.TempDir()}
	path, err := GlobalPath(opts)
	if err != nil {
		t.Fatal(err)
	}
	// "ſchema_version" (U+017F long s) folds to schema_version as the decoder
	// reads it.
	raw := `{"ſchema_version":1,"Future":{"flag":true},"Overrides":{"workflow.lang":"Korean","other.setting":"kept"},"Agents":{"Model_Aliases":{"small":{"codex":{"Backend":"codex","Model":"before","future_leaf":{"flag":true}}}}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(opts, nil, nil, nil)
	if err := resolver.Unset("workflow.lang", SetOptions{ExplicitScope: ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	files, err := FileLayers(opts)
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := files[ScopeGlobal].Overrides["workflow.lang"]; ok {
		t.Errorf("reset value came back on load: %q", value)
	}
	if files[ScopeGlobal].Overrides["other.setting"] != "kept" {
		t.Errorf("reset lost a sibling override: %#v", files[ScopeGlobal].Overrides)
	}
	if _, err := SetGlobalAgentsTierForHarness(opts, "small", "codex", "after", "codex"); err != nil {
		t.Fatal(err)
	}
	if out, err := os.ReadFile(path); err != nil || strings.Contains(string(out), `"Model"`) || strings.Contains(string(out), `"Backend"`) || !strings.Contains(string(out), `"future_leaf"`) {
		t.Errorf("leaf write kept a differently cased known leaf field or lost an unknown one: %s, %v", out, err)
	}
	if _, removed, err := UnsetGlobalAgentsTierForHarness(opts, "small", "codex"); err != nil || !removed {
		t.Fatalf("remove leaf: removed=%t, err=%v", removed, err)
	}
	files, err = FileLayers(opts)
	if err != nil {
		t.Fatal(err)
	}
	if leaf, ok := files[ScopeGlobal].Agents.ModelAliases["small"]["codex"]; ok {
		t.Errorf("reset tier leaf came back on load: %#v", leaf)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{`"Overrides"`, `"Agents"`, `"Model_Aliases"`, `"ſchema_version"`} {
		if strings.Contains(string(out), gone) {
			t.Errorf("write kept differently cased known member %s: %s", gone, out)
		}
	}
	if !strings.Contains(string(out), `"Future"`) {
		t.Errorf("write dropped an unknown member: %s", out)
	}
}

func TestJSONFoldNameMatchesDecoder(t *testing.T) {
	type probe struct {
		Field string `json:"schema_version"`
	}
	for _, key := range []string{"schema_version", "SCHEMA_VERSION", "Schema_Version", "ſchema_version", "schema_versioN", "schemaversion", "schema-version", "schema_versio"} {
		var p probe
		if err := json.Unmarshal([]byte(`{"`+key+`":"x"}`), &p); err != nil {
			t.Fatal(err)
		}
		if got, want := jsonNameIs(key, "schema_version"), p.Field == "x"; got != want {
			t.Errorf("jsonNameIs(%q) = %t, decoder matched %t", key, got, want)
		}
	}
}

// TestSettingsConfigWriteMergesEveryCasedSpellingOfAMember: the writer before
// case folding kept "Agents" as an unknown member beside the canonical
// "agents", so a file can hold both. The decoder reads both into one field;
// a write keeps the unknown members of every spelling under the canonical
// one, the canonical spelling winning a conflicting leaf.
func TestSettingsConfigWriteMergesEveryCasedSpellingOfAMember(t *testing.T) {
	opts := Options{ConfigHome: t.TempDir(), CacheHome: t.TempDir()}
	path, err := GlobalPath(opts)
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"schema_version":1,"Agents":{"future_agent":true,"clash":"cased","model_aliases":{"small":{"codex":{"backend":"codex","model":"before","future_leaf":{"flag":true}}}}},"agents":{"clash":"canonical","model_aliases":{"large":{"pi":{"backend":"pi","model":"kept"}}}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(opts, nil, nil, nil)
	if err := resolver.Set("workflow.lang", "Korean", SetOptions{ExplicitScope: ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetGlobalAgentsTierForHarness(opts, "small", "codex", "after", "codex"); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Agents map[string]json.RawMessage `json:"agents"`
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(out, &members); err != nil {
		t.Fatal(err)
	}
	if _, ok := members["Agents"]; ok {
		t.Errorf("write kept the cased spelling: %s", out)
	}
	if err := json.Unmarshal(members["agents"], &doc.Agents); err != nil {
		t.Fatal(err)
	}
	if string(doc.Agents["future_agent"]) != "true" || string(doc.Agents["clash"]) != `"canonical"` {
		t.Errorf("write lost an unknown member of a cased spelling or let it win: %s", out)
	}
	if !strings.Contains(string(out), `"future_leaf"`) || !strings.Contains(string(out), `"after"`) {
		t.Errorf("write lost a cased spelling's tier leaf: %s", out)
	}
	files, err := FileLayers(opts)
	if err != nil {
		t.Fatal(err)
	}
	aliases := files[ScopeGlobal].Agents.ModelAliases
	if aliases["small"]["codex"].Model != "after" || aliases["large"]["pi"].Model != "kept" || files[ScopeGlobal].Overrides["workflow.lang"] != "Korean" {
		t.Errorf("merged write read back as %#v", files[ScopeGlobal])
	}
}
