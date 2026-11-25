package mcp

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

func TestWerkWebSearchConfigCatalogTuneAndCaveats(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	cacheHome := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	s := NewServerWithOptions(ServerOptions{
		Root:    root,
		Version: "test",
		Env: []string{
			"XDG_CACHE_HOME=" + cacheHome,
			"XDG_CONFIG_HOME=" + configHome,
		},
	})
	key, err := NewAgentKeyStore().MintLead(root)
	if err != nil {
		t.Fatal(err)
	}

	// 1. config.list JSON exposes all five knobs with expected defaults.
	listRaw, failed, _ := invokeToolRaw(t, s, "config.list", map[string]any{"session_key": key, "format": "json"})
	if failed {
		t.Fatalf("config.list failed: %s", listRaw)
	}
	var listView configListView
	if err := json.Unmarshal([]byte(listRaw), &listView); err != nil {
		t.Fatalf("unmarshal config.list: %v\n%s", err, listRaw)
	}
	knobs := map[string]tuningKnob{}
	for _, k := range listView.Knobs {
		knobs[k.ID] = k
	}
	wantDefaults := map[string]string{
		wsconfig.ItemWerkWebSearch:           "true",
		wsconfig.ItemWerkWebSearchMaxUses:    "2",
		wsconfig.ItemWerkWebSearchMaxResults: "5",
		wsconfig.ItemWerkWebSearchEngine:     "exa",
		wsconfig.ItemWerkWebSearchSIWC:       "false",
	}
	for id, wantVal := range wantDefaults {
		knob, ok := knobs[id]
		if !ok {
			t.Fatalf("config.list missing knob %s", id)
		}
		if knob.Kind != "web_search" {
			t.Errorf("%s kind = %q, want web_search", id, knob.Kind)
		}
		cur, ok := knob.Current.(map[string]any)
		if !ok || cur["value"] != wantVal || cur["scope"] != string(wsconfig.ScopeBuiltin) {
			t.Errorf("%s current = %#v, want value=%q scope=builtin", id, knob.Current, wantVal)
		}
	}

	// Verify tuning descriptions state both caveats.
	if !strings.Contains(strings.ToLower(knobs[wsconfig.ItemWerkWebSearchEngine].Description), "native") ||
		!strings.Contains(strings.ToLower(knobs[wsconfig.ItemWerkWebSearchEngine].Description), "exceed") {
		t.Errorf("engine knob description missing per-request overrun caveat: %q", knobs[wsconfig.ItemWerkWebSearchEngine].Description)
	}
	if !strings.Contains(strings.ToLower(knobs[wsconfig.ItemWerkWebSearchSIWC].Description), "best-effort") {
		t.Errorf("siwc knob description missing best-effort caveat: %q", knobs[wsconfig.ItemWerkWebSearchSIWC].Description)
	}

	// 2. config.tune accepts valid writes and rejects invalid values.
	for _, tc := range []struct {
		key, val string
		wantErr  bool
	}{
		{wsconfig.ItemWerkWebSearch, "false", false},
		{wsconfig.ItemWerkWebSearch, "maybe", true},
		{wsconfig.ItemWerkWebSearchMaxUses, "4", false},
		{wsconfig.ItemWerkWebSearchMaxUses, "0", true},
		{wsconfig.ItemWerkWebSearchMaxResults, "10", false},
		{wsconfig.ItemWerkWebSearchMaxResults, "-1", true},
		{wsconfig.ItemWerkWebSearchEngine, "parallel", false},
		{wsconfig.ItemWerkWebSearchEngine, "unknown-engine", true},
		{wsconfig.ItemWerkWebSearchSIWC, "true", false},
		{wsconfig.ItemWerkWebSearchSIWC, "yes", true},
	} {
		text, failed, _ := invokeToolRaw(t, s, "config.tune", map[string]any{
			"session_key": key,
			"key":         tc.key,
			"value":       tc.val,
			"scope":       "global",
		})
		if failed != tc.wantErr {
			t.Errorf("config.tune(%s=%q) failed=%v wantErr=%v text=%q", tc.key, tc.val, failed, tc.wantErr, text)
		}
	}

	// 3. Reset restores builtin default.
	if text, failed, _ := invokeToolRaw(t, s, "config.tune", map[string]any{
		"session_key": key,
		"key":         wsconfig.ItemWerkWebSearchEngine,
		"scope":       "global",
		"reset":       true,
	}); failed {
		t.Fatalf("reset engine failed: %s", text)
	}
	res, err := s.resolveConfigGet(key, wsconfig.ItemWerkWebSearchEngine)
	if err != nil {
		t.Fatal(err)
	}
	if res.Value != "exa" || res.Scope != string(wsconfig.ScopeBuiltin) {
		t.Fatalf("after reset engine = %+v, want exa/builtin", res)
	}

	// 4. Verify English settings UI i18n strings explicitly state both caveats.
	raw, err := os.ReadFile("../wsi18n/tables/en.json")
	if err != nil {
		t.Fatal(err)
	}
	var en map[string]string
	if err := json.Unmarshal(raw, &en); err != nil {
		t.Fatal(err)
	}
	engDesc := strings.ToLower(en["js.settings.key.agent.werk_web_search_engine.description"])
	if !strings.Contains(engDesc, "native") || (!strings.Contains(engDesc, "exceed") && !strings.Contains(engDesc, "overrun")) {
		t.Errorf("en.json engine description missing native/auto per-request overrun caveat: %q", engDesc)
	}
	siwcDesc := strings.ToLower(en["js.settings.key.agent.werk_web_search_siwc.description"])
	if !strings.Contains(siwcDesc, "best-effort") {
		t.Errorf("en.json siwc description missing best-effort caveat: %q", siwcDesc)
	}
}
