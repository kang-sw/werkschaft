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
		{"dashboard.js", []string{"dashboard.js", "dashboard-jank.js", "dashboard-animation.js", "dashboard-board.js", "dashboard-board-anchor.js", "dashboard-board-reveal.js", "dashboard-board-dial.js", "dashboard-conversation.js", "dashboard-conv-motion.js", "dashboard-bundles.js", "dashboard-hud.js", "dashboard-composer.js", "dashboard-composer-references.js", "dashboard-composer-input.js", "dashboard-composer-images.js", "dashboard-composer-textrefs.js", "dashboard-composer-pickers.js", "dashboard-composer-highlight.js", "dashboard-composer-selections.js", "dashboard-composer-delivery.js", "dashboard-whiteboard-only.js", "dashboard-composer-exec.js", "dashboard-composer-ucmd-editor.js", "dashboard-composer-ucmd-card.js", "dashboard-composer-ucmd.js", "dashboard-composer-restore.js", "dashboard-composer-status.js", "dashboard-exec-hud.js", "dashboard-exec-hud-peek.js", "dashboard-bg-exec-monitor.js", "dashboard-composer-keyboard.js", "dashboard-actions.js", "dashboard-drawer.js", "dashboard-ticketmap.js", "dashboard-ticketmap-viewb.js", "dashboard-ticketmap-geom.js", "dashboard-ticketmap-graph.js", "dashboard-workpanel.js", "dashboard-frames.js", "dashboard-git.js", "dashboard-gitpage.js", "dashboard-tracker.js", "dashboard-selection.js", "dashboard-stream.js", "dashboard-daemon-confirm.js", "dashboard-daemon-bridge.js", "dashboard-daemon-guide.js", "dashboard-rail.js", "dashboard-workspace-admission.js", "dashboard-rail-popovers.js", "dashboard-holders.js", "dashboard-leads.js", "dashboard-new-session.js", "dashboard-tier-picker.js", "dashboard-session-view.js", "dashboard-companion-relay.js", "dashboard-audit-window.js", "dashboard-audit.js", "dashboard-audit-rows.js", "dashboard-ide-shell.js", "dashboard-lifecycle.js", "dashboard-voice-ptt.js", "dashboard-voice.js", "dashboard-voice-native.js"}},
		{"dashboard.css", []string{"dashboard.css", "dashboard-conversation.css", "dashboard-conv-motion.css", "dashboard-composer-md.css", "dashboard-board.css", "dashboard-layout.css", "dashboard-drawer.css", "dashboard-ticketmap.css", "dashboard-workpanel.css", "dashboard-git.css", "dashboard-gitpage.css", "dashboard-menus.css", "dashboard-rail.css", "dashboard-new-session.css", "dashboard-shell.css", "dashboard-account-consent.css", "dashboard-daemon-confirm.css", "dashboard-daemon-guide.css", "dashboard-ide-shell.css", "dashboard-holders.css", "dashboard-audit.css", "dashboard-audit-rows.css", "dashboard-exec-hud.css", "dashboard-bg-exec-monitor.css", "dashboard-composer-ucmd.css", "dashboard-composer-ucmd-editor.css", "dashboard-voice.css", "dashboard-jank.css"}},
		{"tracker.js", []string{"tracker.js", "tracker-org-editor.js", "tracker-org-actions.js", "tracker-view-helpers.js", "tracker-item-rows.js", "tracker-panel.js", "tracker-wiki.js", "tracker-window.js", "tracker-forms.js", "tracker-menu.js", "tracker-picker.js", "tracker-writes.js", "tracker-render.js", "tracker-mount.js"}},
		{"dashboard-companion.js", []string{"dashboard-companion-builtins.js", "dashboard-companion-packs.js", "dashboard-companion-focus.js", "dashboard-companion-deform.js", "dashboard-companion-compositor.js", "dashboard-companion-scenarios.js", "dashboard-companion-menu.js", "dashboard-companion-vrm-colead.js", "dashboard-companion-vrm.js", "dashboard-companion-messenger.js", "dashboard-companion-stage.js"}},
		{"dashboard-companion.css", []string{"dashboard-companion.css", "dashboard-companion-messenger.css"}},
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
