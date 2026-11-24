package wsdashboard

import (
	"regexp"
	"strings"
	"testing"
)

// TestStickyHeaderNoCornerBleed ensures sticky headers on topic cards, board
// panel cards, and diff file cards do not apply redundant top border-radius
// over square backdrops or unclipped containers.
func TestStickyHeaderNoCornerBleed(t *testing.T) {
	cssBytes, err := dashboardTestAsset("assets/dashboard.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssBytes)

	for _, want := range []string{
		".gal-pane > section.topic { overflow: hidden; overflow: clip; }",
		".bc-in { background: var(--wb-card); border-radius: var(--wb-radius); box-shadow: var(--wb-shadow); overflow: hidden; overflow: clip;",
		".wb-rail .bcard:not([data-max]) .bc-hd.wb-stuck { box-shadow: var(--wb-shadow), 0 -6px 0 6px var(--wb-bg); }",
		"section.topic.quiet[data-max] > .tophead > .tbar { border-top-left-radius: 0; border-top-right-radius: 0; }",
		".overview-preview-stage > section.topic.quiet[data-topic] > .tophead > .tbar { border-top-left-radius: 0; border-top-right-radius: 0; }",
		".dfile { flex: none; background: var(--wb-card); border-radius: 10px; box-shadow: var(--wb-shadow); overflow: hidden; overflow: clip; }",
		".dfile.fold .dfh { border-radius: 10px; box-shadow: none; }",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("dashboard.css missing %q", want)
		}
	}

	if strings.Contains(css, ".gal-pane > section.topic:not(.quiet) > .tophead > .tbar") {
		t.Error("dashboard.css must not set inner top border-radius on .gal-pane > section.topic:not(.quiet) > .tophead > .tbar")
	}

	noBorderRadiusRules := []string{
		`(?m)^\.wb-rail\s+\.bcard:not\(\[data-max\]\)\s+\.bc-hd\s*\{([^}]*)\}`,
		`(?m)^\.bcard\[data-file-state="preview"\]\s*>\s*\.bc-in\s*>\s*\.bc-hd\s*\{([^}]*)\}`,
		`(?m)^\.dfh\s*\{([^}]*)\}`,
	}
	for _, pat := range noBorderRadiusRules {
		re := regexp.MustCompile(pat)
		matches := re.FindAllStringSubmatch(css, -1)
		if len(matches) == 0 {
			t.Fatalf("expected rule matching %s in dashboard.css", pat)
		}
		for _, m := range matches {
			if strings.Contains(m[1], "border-radius") {
				t.Errorf("sticky header rule %q must not carry border-radius: %s", m[0], m[1])
			}
		}
	}

	dfhRule := regexp.MustCompile(`(?:^|\n)\.dfh\s*\{([^}]*)\}`).FindStringSubmatch(css)
	if len(dfhRule) == 0 || !strings.Contains(dfhRule[1], "position: sticky") {
		t.Errorf(".dfh rule must stay sticky, got %v", dfhRule)
	}
}
