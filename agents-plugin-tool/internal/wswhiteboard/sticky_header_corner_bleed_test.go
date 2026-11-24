package wswhiteboard

import (
	"regexp"
	"strings"
	"testing"
)

// TestStickyHeaderNoCornerBleed ensures sticky topic headers rely on .topic's
// border-radius + overflow: clip instead of giving .tbar inner top rounding
// that exposes .tophead's square --wb-card backdrop behind stuck corners.
func TestStickyHeaderNoCornerBleed(t *testing.T) {
	cssBytes, err := whiteboardTestAsset("page.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssBytes)

	for _, want := range []string{
		".topic { background: var(--wb-card); border-radius: var(--wb-radius);",
		".topic.quiet .tbar { background: var(--wb-muted); color: var(--wb-muted-fg); border-radius: var(--wb-radius); flex-wrap: wrap; }",
		".wb-rail .topic { overflow: hidden; overflow: clip; }",
		".wb-rail .topic > .tophead { position: sticky; top: var(--wb-rail-top, 0px); z-index: 2; }",
		".wb-rail .topic.quiet > .tophead.wb-stuck > .tbar { border-top-left-radius: 0; border-top-right-radius: 0; }",
		".wb-rail .topic > .tophead.wb-stuck { box-shadow: var(--wb-shadow), 0 -6px 0 6px var(--wb-bg); }",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("page.css missing %q", want)
		}
	}

	if strings.Contains(css, ".wb-rail .topic:not(.quiet) > .tophead > .tbar") {
		t.Error("page.css must not set inner top border-radius on .wb-rail .topic:not(.quiet) > .tophead > .tbar")
	}
	if re := regexp.MustCompile(`(?m)^\.tbar\s*\{[^}]*border-radius[^}]*\}`); re.MatchString(css) {
		for _, match := range re.FindAllString(css, -1) {
			t.Errorf("unexpected bare .tbar border-radius rule %q", match)
		}
	}
}
