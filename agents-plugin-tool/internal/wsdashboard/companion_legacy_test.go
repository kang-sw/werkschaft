package wsdashboard

import (
	"os"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

func globalFile(t *testing.T, opts wsconfig.Options) string {
	t.Helper()
	path, err := wsconfig.GlobalPath(opts)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCompanionModeEnabled(t *testing.T) {
	for mode, want := range map[string][2]bool{
		"off": {false, true}, "visual": {true, true}, "full": {true, true}, " full ": {true, true},
		"": {false, false}, "on": {false, false}, "Visual": {false, false},
	} {
		if enabled, valid := CompanionModeEnabled(mode); enabled != want[0] || valid != want[1] {
			t.Errorf("CompanionModeEnabled(%q) = %v, %v; want %v, %v", mode, enabled, valid, want[0], want[1])
		}
	}
	for removed, want := range map[string]string{"sidecar": "rules", "lead-tool": "rules"} {
		if got, ok := CompanionLegacyDirector(removed); !ok || got != want {
			t.Errorf("CompanionLegacyDirector(%q) = %q, %v; want %q", removed, got, ok, want)
		}
	}
	for _, current := range []string{"rules", "agent", "", "other"} {
		if _, ok := CompanionLegacyDirector(current); ok {
			t.Errorf("%q is not a removed director value", current)
		}
	}
}

// The settings UI and config.tune both write through wsconfig.Resolver, so the
// mirror registered here covers each writer in one locked file rewrite.
func TestCompanionMirrorKeepsBothKeysInOneWrite(t *testing.T) {
	opts := wsconfig.Options{ConfigHome: t.TempDir(), CacheHome: t.TempDir()}
	resolver := wsconfig.NewResolver(opts, nil, nil, nil)
	global := wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}
	stored := func() map[string]string {
		t.Helper()
		overrides, err := wsconfig.GlobalOverrides(opts)
		if err != nil {
			t.Fatal(err)
		}
		return overrides
	}
	rawMode := func() string {
		// GlobalOverrides reconciles; the mirror's own output is the file text.
		raw, err := os.ReadFile(globalFile(t, opts))
		if err != nil {
			return ""
		}
		for _, mode := range []string{"off", "visual", "full"} {
			if strings.Contains(string(raw), `"`+CompanionModeKey+`": "`+mode+`"`) || strings.Contains(string(raw), `"`+CompanionModeKey+`":"`+mode+`"`) {
				return mode
			}
		}
		return ""
	}

	if err := resolver.Set(CompanionEnabledKey, "true", global); err != nil {
		t.Fatal(err)
	}
	if rawMode() != "visual" || stored()[CompanionEnabledKey] != "true" {
		t.Fatalf("enable stored mode %q, %v", rawMode(), stored())
	}
	if err := resolver.Set(CompanionModeKey, "full", global); err != nil {
		t.Fatal(err)
	}
	if rawMode() != "full" || stored()[CompanionEnabledKey] != "true" {
		t.Fatalf("mode=full stored mode %q, %v", rawMode(), stored())
	}
	if err := resolver.Set(CompanionEnabledKey, "false", global); err != nil {
		t.Fatal(err)
	}
	if rawMode() != "off" || stored()[CompanionEnabledKey] != "false" {
		t.Fatalf("disable stored mode %q, %v", rawMode(), stored())
	}
	// Writing the same value again changes nothing and does not touch the file.
	before, err := os.Stat(globalFile(t, opts))
	if err != nil {
		t.Fatal(err)
	}
	if err := resolver.Set(CompanionEnabledKey, "false", global); err != nil {
		t.Fatal(err)
	}
	if after, err := os.Stat(globalFile(t, opts)); err != nil || !os.SameFile(before, after) {
		t.Fatalf("an unchanged mirrored write replaced the file: %v", err)
	}
	if err := resolver.Unset(CompanionEnabledKey, global); err != nil {
		t.Fatal(err)
	}
	if overrides := stored(); len(overrides) != 0 {
		t.Fatalf("reset left %v", overrides)
	}
	// An unrelated key is never mirrored.
	if err := resolver.Set(CompanionPackKey, "auto", global); err != nil {
		t.Fatal(err)
	}
	if overrides := stored(); len(overrides) != 1 || overrides[CompanionPackKey] != "auto" {
		t.Fatalf("pack write stored %v", overrides)
	}
}
