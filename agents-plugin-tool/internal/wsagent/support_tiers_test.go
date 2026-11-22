package wsagent

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// The new-session dialog's named-tier picker reads the routing graph's
// bundles from the choices answer, resolved by the daemon as agent.spawn
// resolves a bare bundle name.
func TestSupportChoicesListNamedTiers(t *testing.T) {
	h := routingHarness(t)
	s := NewSupport(SupportOptions{Runner: h.runner, Dir: t.TempDir(), Config: h.config.Config})
	if ans := s.Choices(context.Background()); ans.Tiers == nil || len(ans.Tiers) != 0 || ans.TiersError != "" {
		t.Fatalf("no routing: tiers %#v, error %q", ans.Tiers, ans.TiersError)
	}
	setTierRouting(t, h.config.Config, `{"bundles":{"discussion":{"harness":"pi","model":"prov/thinking","effort":"high"},"agy-deep":{"harness":"agy","model":"claude-opus-4-6-thinking"}}}`)
	ans := s.Choices(context.Background())
	want := []SupportTier{
		{Name: "agy-deep", Trace: "agy-deep", Harness: "agy", Model: "claude-opus-4-6-thinking"},
		{Name: "discussion", Trace: "discussion", Harness: "pi", Model: "prov/thinking", Effort: "high"},
	}
	if ans.TiersError != "" || !reflect.DeepEqual(ans.Tiers, want) {
		t.Fatalf("tiers = %#v (error %q), want %#v", ans.Tiers, ans.TiersError, want)
	}

	// An unusable stored graph lists no presets and says why; the launch
	// fields stay usable by hand.
	setTierRouting(t, h.config.Config, `{"nodes":{"a":{"*":"b:*"},"b":{"*":"a:*"}}}`)
	ans = s.Choices(context.Background())
	if len(ans.Tiers) != 0 || !strings.Contains(ans.TiersError, "cycle") || len(ans.Harnesses) == 0 {
		t.Fatalf("invalid graph: tiers %#v, error %q", ans.Tiers, ans.TiersError)
	}
}
