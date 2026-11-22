package wsdashboard

import (
	"errors"
	"strings"
	"testing"
)

// dashboardTestAsset reads logical sources as the renderer does. Individual
// fragments are intentionally not valid standalone scripts.
func dashboardTestAsset(name string) ([]byte, error) {
	return readDashboardSource(embeddedDashboardAsset, strings.TrimPrefix(name, "assets/"))
}

func TestDashboardAssetSourceAssembly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []string
	}{
		{"dashboard.js", []string{"dashboard.js", "dashboard-animation.js", "dashboard-board.js", "dashboard-conversation.js", "dashboard-bundles.js", "dashboard-hud.js", "dashboard-composer.js", "dashboard-composer-references.js", "dashboard-composer-input.js", "dashboard-composer-images.js", "dashboard-composer-pickers.js", "dashboard-composer-selections.js", "dashboard-composer-delivery.js", "dashboard-composer-exec.js", "dashboard-composer-ucmd.js", "dashboard-composer-restore.js", "dashboard-composer-status.js", "dashboard-composer-keyboard.js", "dashboard-actions.js", "dashboard-drawer.js", "dashboard-ticketmap.js", "dashboard-ticketmap-viewb.js", "dashboard-ticketmap-geom.js", "dashboard-ticketmap-graph.js", "dashboard-frames.js", "dashboard-git.js", "dashboard-tracker.js", "dashboard-selection.js", "dashboard-stream.js", "dashboard-rail.js", "dashboard-workspace-admission.js", "dashboard-rail-popovers.js", "dashboard-leads.js", "dashboard-tier-picker.js", "dashboard-session-view.js", "dashboard-companion-relay.js", "dashboard-audit-window.js", "dashboard-audit.js", "dashboard-audit-rows.js", "dashboard-audit-search.js", "dashboard-lifecycle.js"}},
		{"dashboard.css", []string{"dashboard.css", "dashboard-conversation.css", "dashboard-board.css", "dashboard-layout.css", "dashboard-drawer.css", "dashboard-ticketmap.css", "dashboard-menus.css", "dashboard-rail.css", "dashboard-shell.css", "dashboard-audit.css", "dashboard-audit-rows.css", "dashboard-audit-search.css"}},
		{"tracker.js", []string{"tracker.js", "tracker-org-editor.js", "tracker-org-actions.js", "tracker-view-helpers.js", "tracker-item-rows.js", "tracker-panel.js", "tracker-wiki.js", "tracker-window.js", "tracker-forms.js", "tracker-menu.js", "tracker-picker.js", "tracker-writes.js", "tracker-render.js", "tracker-mount.js"}},
		{"dashboard-companion.js", []string{"dashboard-companion-builtins.js", "dashboard-companion-packs.js", "dashboard-companion-focus.js", "dashboard-companion-deform.js", "dashboard-companion-compositor.js", "dashboard-companion-scenarios.js", "dashboard-companion-menu.js", "dashboard-companion-sources.js", "dashboard-companion-settings.js", "dashboard-companion-stage.js"}},
		{"dashboard-companion.css", []string{"dashboard-companion.css"}},
		{"rail.js", []string{"rail.js"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			raw, err := readDashboardSource(func(name string) ([]byte, error) {
				calls = append(calls, name)
				return []byte("<" + name + ">"), nil
			}, tc.name)
			want := "<" + strings.Join(tc.files, "><") + ">"
			if err != nil || string(raw) != want || strings.Join(calls, ",") != strings.Join(tc.files, ",") {
				t.Fatalf("assembly = %q, %v, reads %v; want %q, reads %v", raw, err, calls, want, tc.files)
			}
			for _, missing := range tc.files {
				failure := errors.New("missing " + missing)
				_, err := readDashboardSource(func(name string) ([]byte, error) {
					if name == missing {
						return nil, failure
					}
					return []byte(name), nil
				}, tc.name)
				if !errors.Is(err, failure) {
					t.Errorf("missing %s: got %v", missing, err)
				}
			}
		})
	}
}
