package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsauditstore"
	"github.com/kang-sw/devenv/internal/wsconfig"
)

// TestShippedAuditKnobsAreGlobalWithStoreDefaults pins the four audit config
// keys in every package's dashboard manifest: global-only integers whose
// defaults are the store's built-in limits, so an instance without the
// manifest behaves the same.
func TestShippedAuditKnobsAreGlobalWithStoreDefaults(t *testing.T) {
	def := wsauditstore.DefaultLimits()
	want := map[string]int64{
		wsauditstore.KeySessionCapMiB: def.SessionBytes >> 20,
		wsauditstore.KeyTotalCapMiB:   def.TotalBytes >> 20,
		wsauditstore.KeyBodyCapKiB:    def.BodyBytes >> 10,
		wsauditstore.KeyRetentionDays: int64(def.Retention.Hours() / 24),
	}
	for _, pkg := range dashboardPackages {
		declared := loadDeclaredConfig(pkg.own + ";" + pkg.dashboard)
		if len(declared.rejected) != 0 {
			t.Fatalf("%s: rejected %v", pkg.name, declared.rejected)
		}
		for key, value := range want {
			k, ok := declared.lookup(key)
			if !ok || k.Type != "integer" || k.DefaultValue != value || k.DefaultScope != wsconfig.ScopeGlobal {
				t.Errorf("%s: %s = %+v (declared %v), want integer default %d at global scope", pkg.name, key, k, ok, value)
				continue
			}
			if !wsconfig.GlobalOnly(key) {
				t.Errorf("%s is not global-only", key)
			}
			if _, err := k.parse("0"); err == nil {
				t.Errorf("%s accepted 0", key)
			}
		}
	}
}

// TestAuditKnobWriteBreakingTheSetIsRefused pins (2129)=a (4): a config write
// or reset that would break the knob set is refused before it commits, a
// valid one lands, and a stored breaking set loads clamped with warnings.
func TestAuditKnobWriteBreakingTheSetIsRefused(t *testing.T) {
	opts := wsconfig.Options{CacheHome: t.TempDir(), ConfigHome: t.TempDir()}
	r := wsconfig.NewResolver(opts, nil, nil, nil)
	global := wsconfig.SetOptions{ExplicitScope: wsconfig.ScopeGlobal}
	// The default 1 MiB bodies exceed 1/16 of an 8 MiB effective cap, so
	// lowering the total first breaks the set.
	if err := r.Set(wsauditstore.KeyTotalCapMiB, "8", global); err == nil || !strings.Contains(err.Error(), "1/16") {
		t.Fatalf("breaking total cap accepted: %v", err)
	}
	if err := r.Set(wsauditstore.KeyBodyCapKiB, "512", global); err != nil {
		t.Fatalf("valid body cap refused: %v", err)
	}
	if err := r.Set(wsauditstore.KeyTotalCapMiB, "8", global); err != nil {
		t.Fatalf("valid total cap refused: %v", err)
	}
	if err := r.Set(wsauditstore.KeyBodyCapKiB, "1024", global); err == nil {
		t.Fatal("breaking body cap accepted")
	}
	// Resetting the body cap brings back the 1 MiB default, which breaks
	// the set again.
	if err := r.Unset(wsauditstore.KeyBodyCapKiB, global); err == nil {
		t.Fatal("breaking reset accepted")
	}
	if err := r.Set(wsauditstore.KeySessionCapMiB, "2", global); err == nil {
		t.Fatal("per-session cap below the floor accepted")
	}
	got, err := r.Get("", wsauditstore.KeyBodyCapKiB)
	if err != nil || got.Value != "512" {
		t.Fatalf("stored body cap = %+v, %v", got, err)
	}
	limits, warnings, err := dashboardAuditLimits(opts)
	if err != nil || len(warnings) != 0 || limits.TotalBytes != 8<<20 || limits.BodyBytes != 512<<10 {
		t.Fatalf("loaded %+v %v %v", limits, warnings, err)
	}

	// A hand-edited file that breaks the set loads clamped.
	path, err := wsconfig.GlobalPath(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeGlobalOverrides(path, map[string]string{wsauditstore.KeyTotalCapMiB: "8", wsauditstore.KeyBodyCapKiB: strconv.Itoa(4096)}); err != nil {
		t.Fatal(err)
	}
	limits, warnings, err = dashboardAuditLimits(opts)
	if err != nil || limits.BodyBytes != 512<<10 || len(warnings) != 1 || limits.Validate() != nil {
		t.Fatalf("clamped load = %+v %v %v", limits, warnings, err)
	}
}

// writeGlobalOverrides writes a global config file holding overrides, as a
// hand edit would, bypassing the write checks.
func writeGlobalOverrides(path string, overrides map[string]string) error {
	raw, err := json.Marshal(map[string]any{"schema_version": 1, "overrides": overrides})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}
