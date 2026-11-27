package wswhiteboard

import (
	"os/exec"
	"strings"
	"testing"
)

// TestMermaidInitNode runs the theme script against a fake DOM and mermaid:
// Option C config, surface-aware drawing, theme and OS-scheme redraws, the
// compact error card, and the lightbox allowlist.
func TestMermaidInitNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; run testdata/mermaid-init.test.cjs locally")
	}
	out, err := exec.Command(node, "--test", "testdata/mermaid-init.test.cjs").CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("mermaid init: %v", err)
	}
}

// TestMermaidStyleContract pins the shipped look's structural fixes in the
// embedded assets (the page and each topic-body frame inline them).
func TestMermaidStyleContract(t *testing.T) {
	assets, err := PageAssets()
	if err != nil {
		t.Fatal(err)
	}
	init := string(assets.MermaidInit)
	wantAll(t, init,
		"surfaceOf(", "edgeLabelBackground", "clusterBkg", "mirrorActors: false",
		"sectionBkgColor", "activeTaskBkgColor", "doneTaskBkgColor", "critBkgColor", "plotColorPalette",
		`addEventListener("change"`, "state.listening", "wb-mmd-err", "소스 복사",
		`[id*=\"-extension\"] path`, `[id*=\"-aggregation\"] path`, `classId-\"] > .divider`, "stroke-width:2.6px",
	)
	// a destructive blanket strip of node outlines must not return
	for _, bad := range []string{".node rect, .node polygon", "stroke: none; }"} {
		if strings.Contains(init, bad) {
			t.Errorf("init still carries the blanket override %q", bad)
		}
	}
	if !strings.Contains(string(assets.CSS), ".wb-mmd-err-raw") || !strings.Contains(string(assets.CSS), "font-variant-ligatures: none") {
		t.Error("error card CSS missing (ligatures must be off so the caret lines up)")
	}
}

// TestSvgViewEdgeLabelAllowlist: an HTML edge-label pill keeps its fill,
// padding and radius when the viewer flattens the SVG.
func TestSvgViewEdgeLabelAllowlist(t *testing.T) {
	b, err := assetFS.ReadFile("assets/svg-view.js")
	if err != nil {
		t.Fatal(err)
	}
	wantAll(t, string(b), `"background-color"`, `"padding"`, `"border-radius"`)
}
