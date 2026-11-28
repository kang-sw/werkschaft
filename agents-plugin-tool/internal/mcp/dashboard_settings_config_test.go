package mcp

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsconfigservice"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wspackage"
)

func TestDashboardSettingsCatalog(t *testing.T) {
	t.Setenv(wspackage.EnvDevCheckout, "")
	var namespaces = map[string]int{"claude": 13, "agy": 6, "dashboard": 28}
	for _, pkg := range []string{"ws", "wsflow"} {
		t.Run(pkg, func(t *testing.T) {
			catalog, err := DashboardConfigCatalog(pkg)
			if err != nil {
				t.Fatal(err)
			}
			actual := make(map[string]int)
			normalized := make(map[string]bool)
			for _, key := range catalog.Keys() {
				normalized[wsconfigservice.FamilyKey(key)] = true
				namespace, _, _ := strings.Cut(key, ".")
				if _, ok := namespaces[namespace]; ok {
					actual[namespace]++
				}
			}
			if !maps.Equal(actual, namespaces) {
				t.Fatalf("adapter namespaces = %v, want %v", actual, namespaces)
			}
			keys := slices.Sorted(maps.Keys(normalized))
			for _, family := range []string{"prompt.*", "agent.profile.*", "worktree.profile.*", "preference.*"} {
				if !slices.Contains(keys, family) {
					t.Errorf("missing family %q", family)
				}
			}
			if slices.Contains(keys, "dashboard.pi_spawn_enabled") {
				t.Fatal("dashboard catalog still contains the retired Pi setting")
			}
			if err := catalog.Compare(keys); err != nil {
				t.Fatal(err)
			}
			for _, broken := range [][]string{keys[1:], append(slices.Clone(keys), keys[0]), append(slices.Clone(keys), "unknown.setting")} {
				if err := catalog.Compare(broken); err == nil {
					t.Errorf("accepted mismatched UI catalog")
				}
			}
		})
	}
}

func TestDashboardPromptSeedsFollowPlaybookMarkers(t *testing.T) {
	source := fstest.MapFS{"one.md": &fstest.MapFile{Data: []byte(`<!-- ws:override:Outer desc="outer" -->
opening
<!-- ws:override:Inner -->
inner contents
<!-- ws:/override:Inner -->
closing
<!-- ws:/override:Outer -->
<!-- ws:override:Empty -->
<!-- ws:/override:Empty -->`)}}
	points, seeds, err := scanOverridePointsAndSeeds(source)
	if err != nil || len(points) != 3 || seeds["Outer"] != "opening\ninner contents\nclosing" || seeds["Inner"] != "inner contents" {
		t.Fatalf("seed discovery = %#v, %#v, %v", points, seeds, err)
	}
	if empty, ok := seeds["Empty"]; !ok || empty != "" {
		t.Fatalf("empty point = %q, %t", empty, ok)
	}
}

func TestDashboardSettingsKeepsParentSessionResolutionSeparateFromItsOwnOverride(t *testing.T) {
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	src := NewDashboardSessionSource()
	root := t.TempDir()
	parent, err := src.store.mint(root, roleLead, "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := src.store.mint(root, roleDelegate, parent)
	if err != nil {
		t.Fatal(err)
	}
	key := wsconfig.ItemWorkflowLang
	if err := src.store.setOverride(parent, key, "parent"); err != nil {
		t.Fatal(err)
	}
	reg := wsdashboard.Registration{Project: root, ReplyID: wsmailbox.ReplyID(child)}
	catalog, err := wsconfigservice.NewCatalog([]wsconfigservice.Rule{{Key: key}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		value string
		local bool
	}{
		{"parent", false},
		{"child", true},
		{"parent", false},
	} {
		values, writer, resolved, ok := src.settingsSession(reg)
		if !ok || resolved != child {
			t.Fatal("child session is unavailable")
		}
		entry := catalog.Entry(key, nil, wsconfig.ScopeSession, wsconfigservice.Layers{Session: values, SessionAvailable: true})
		session := entry.Layers[0]
		if entry.Value != want.value || entry.SourceInherited == want.local || session.Inherited == want.local || (session.Selected != nil) != want.local {
			t.Errorf("wrong attribution for %q: %#v", want.value, entry)
		}
		if want.local {
			err = writer.DeleteOverride(resolved, key)
		} else {
			err = writer.SetOverride(resolved, key, "child")
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestDashboardAdapterReadLimitsKeepHarnessKeyScopes pins the scope each
// harness-bucketed config key reads under: the named keys the adapters read
// without a worktree or a session, the Pi and Agy startup-only keys, and the
// prefix fallback for a harness key the table does not name.
func TestDashboardAdapterReadLimitsKeepHarnessKeyScopes(t *testing.T) {
	for _, tc := range []struct {
		key            string
		noRepo, noSess bool
	}{
		{"claude.status_band_enabled", false, false},
		{"pi.runaway_threshold", false, false},
		{"pi.settle_delay_ms", false, false},
		{"pi.compaction_advisory_percent", false, false},
		{"pi.compaction_hard_percent", false, false},
		{"pi.context_window_override", false, false},
		{"pi.compaction_dialog_budget_bytes", false, false},
		{"pi.compaction_reread_budget_bytes", false, false},
		{"agy.model_wait_timeout_seconds", false, true},
		{"agy.model_wait_retries", false, true},
		{"agy.stall_log_check", false, true},
		{"agy.http2_frame_trace", false, true},
		{"agy.prefer_native_subagent", false, true},
		{"agy.avoid_agy_subagent", false, true},
		{DashboardPortKey, true, true},
		{"dashboard.companion_body_tier", true, true},
		{"dashboard.companion_work_tier", true, true},
		{"claude.some_unlisted_key", true, true},
		{"unrelated.key", false, false},
	} {
		noRepo, noSession := dashboardAdapterReadLimits(tc.key)
		if noRepo != tc.noRepo || noSession != tc.noSess {
			t.Errorf("%s: limits = (%v, %v), want (%v, %v)", tc.key, noRepo, noSession, tc.noRepo, tc.noSess)
		}
	}
}
