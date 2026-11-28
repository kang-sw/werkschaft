package wsdashboard

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// ticketMapScripts are the ticket map's script fragments, each one outer
// binding inside the dashboard's single IIFE.
var ticketMapScripts = map[string]string{
	"dashboard-ticketmap.js":       "var ticketMap = (function () {",
	"dashboard-ticketmap-viewb.js": "ticketMap.viewB = (function () {",
	"dashboard-ticketmap-geom.js":  "ticketMap.geom = (function () {",
	"dashboard-ticketmap-graph.js": "ticketMap.graph = (function () {",
}

func tmFileIndex(list []string, name string) int {
	for i, s := range list {
		if s == name {
			return i
		}
	}
	return -1
}

// TestTicketMapAssetPositions pins where the map's fragments join the
// concatenation: the shell right after the drawer (it calls drSetOpen), then
// View B, View C's pure geometry and View C, which
// reads ticketMap.S and ticketMap.geom at load; the stylesheet right after
// the drawer's.
func TestTicketMapAssetPositions(t *testing.T) {
	d := tmFileIndex(dashboardScriptFiles, "dashboard-drawer.js")
	for k, name := range []string{"dashboard-ticketmap.js", "dashboard-ticketmap-viewb.js", "dashboard-ticketmap-geom.js", "dashboard-ticketmap-graph.js"} {
		if d < 0 || tmFileIndex(dashboardScriptFiles, name) != d+1+k {
			t.Errorf("dashboardScriptFiles = %v; want %s at position %d after dashboard-drawer.js", dashboardScriptFiles, name, k+1)
		}
	}
	c := tmFileIndex(dashboardStyleFiles, "dashboard-drawer.css")
	if c < 0 || tmFileIndex(dashboardStyleFiles, "dashboard-ticketmap.css") != c+1 {
		t.Errorf("dashboardStyleFiles = %v; want dashboard-ticketmap.css right after dashboard-drawer.css", dashboardStyleFiles)
	}
}

// TestTicketMapScriptScope pins the single-IIFE rule: ticketMap is declared
// once in the concatenated dashboard.js, and each fragment is exactly one
// top-level statement (its outer binding), so nothing it declares hoists
// over, or is shadowed by, a dashboard function.
func TestTicketMapScriptScope(t *testing.T) {
	js, err := dashboardTestAsset("assets/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(regexp.MustCompile(`(?m)^\s*(?:var|let|const|function)\s+ticketMap\b`).FindAllString(string(js), -1)); n != 1 {
		t.Errorf("ticketMap is declared %d times in dashboard.js; want exactly once", n)
	}
	for name, opener := range ticketMapScripts {
		raw, err := dashboardFS.ReadFile("assets/" + name)
		if err != nil {
			t.Fatal(err)
		}
		var top []string
		for _, line := range strings.Split(string(raw), "\n") {
			if regexp.MustCompile(`^  \S`).MatchString(line) && !strings.HasPrefix(line, "  //") {
				top = append(top, line)
			}
			if regexp.MustCompile(`^\s{0,2}function `).MatchString(line) {
				t.Errorf("%s declares a top-level function: %q", name, line)
			}
		}
		if len(top) != 2 || top[0] != "  "+opener || top[1] != "  })();" {
			t.Errorf("%s top-level statements = %q; want only %q ... \"  })();\"", name, top, opener)
		}
	}
}

// TestTicketMapCSSScope pins the collision rule for the map's stylesheet:
// every class it styles starts with d-tm- and every id with tm (the gallery's
// #tmdock host is reached only through its id). The one exception is the
// dashboard's existing shown-state class .on, only compounded onto a d-tm-
// class (View C's placed labels: .d-tm-lb.on); it is never a new name.
func TestTicketMapCSSScope(t *testing.T) {
	raw, err := dashboardFS.ReadFile("assets/dashboard-ticketmap.css")
	if err != nil {
		t.Fatal(err)
	}
	css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(raw), "")
	classes, ids := 0, 0
	for _, m := range regexp.MustCompile(`([^{}]*)\{`).FindAllStringSubmatch(css, -1) {
		sel := strings.TrimSpace(m[1])
		if strings.HasPrefix(sel, "@") || sel == "" {
			continue
		}
		sel = regexp.MustCompile(`\[[^\]]*\]`).ReplaceAllString(sel, "")
		for _, c := range regexp.MustCompile(`\.([A-Za-z_-][\w-]*)`).FindAllStringSubmatch(sel, -1) {
			classes++
			if c[1] == "on" {
				if !regexp.MustCompile(`\.d-tm-[\w-]+\.on\b`).MatchString(sel) {
					t.Errorf("dashboard-ticketmap.css styles .on outside a d-tm- compound: %q", sel)
				}
				continue
			}
			if !strings.HasPrefix(c[1], "d-tm-") {
				t.Errorf("dashboard-ticketmap.css styles .%s; every class must start with d-tm-", c[1])
			}
		}
		for _, id := range regexp.MustCompile(`#([A-Za-z_-][\w-]*)`).FindAllStringSubmatch(sel, -1) {
			ids++
			if !strings.HasPrefix(id[1], "tm") {
				t.Errorf("dashboard-ticketmap.css styles #%s; every id must start with tm", id[1])
			}
		}
	}
	if classes < 20 {
		t.Errorf("found only %d class selectors in dashboard-ticketmap.css: the scan is stale", classes)
	}
	for _, want := range []string{
		`--d-tm-sim: #6a4bd6; --d-tm-idea: #8a6d2f;`,
		`.d-tm-back { position: fixed; inset: 0; z-index: var(--z-modal);`,
		`background: var(--wb-bg);`,
		`z-index: calc(var(--z-modal) + 2);`,
		`.d-tm-docked { container: tm-pane / inline-size;`,
		`@container tm-pane (max-width: 410px)`,
		`@container tm-pane (max-width: 760px)`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("dashboard-ticketmap.css lacks %q", want)
		}
	}
	if n := strings.Count(string(raw), "--d-tm-sim: #a68cff; --d-tm-idea: #c9a55a;"); n != 2 {
		t.Errorf("the dark map hues are declared %d times; want the media block and :root[data-theme=\"dark\"]", n)
	}
	// View C's soup lightness follows the same three blocks.
	if n := strings.Count(string(raw), "--d-tm-sk-l: 30%;"); n != 1 {
		t.Errorf("the light soup lightness is declared %d times; want once at :root", n)
	}
	if n := strings.Count(string(raw), "--d-tm-sk-l: 80%;"); n != 2 {
		t.Errorf("the dark soup lightness is declared %d times; want the media block and :root[data-theme=\"dark\"]", n)
	}
}

// TestTicketMapPageIntegration pins the edits outside the map's own files:
// the shell lifts its frame over the expanded overlay (dim), overview treats
// a click inside it as the palette's, the gallery's ticketmap system page
// hosts the docked root (#tmdock), Ctrl+B finds the docked page, and the
// drawer keeps no ticket map handle (#thandle, thandleSync and --thr are
// retired).
func TestTicketMapPageIntegration(t *testing.T) {
	life := dashboardJSBlock(t, "\n    function dim() {", "\n    function post() {")
	if !strings.Contains(life, ".d-tm-back:not([hidden])") {
		t.Error("dashboard-lifecycle.js dim() does not list .d-tm-back:not([hidden])")
	}
	ov, err := dashboardFS.ReadFile("assets/overview.js")
	if err != nil {
		t.Fatal(err)
	}
	classify := regexp.MustCompile(`(?s)function classify\(t\) \{.*?\n  \}`).FindString(string(ov))
	if !regexp.MustCompile(`t\.closest\("[^"]*\.d-tm-back[^"]*"\)\) return "palette"`).MatchString(classify) {
		t.Errorf("overview.js classify() palette selector does not list .d-tm-back:\n%s", classify)
	}
	cards, err := dashboardFS.ReadFile("assets/cards.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"gal-page gal-sys-page gal-sys-tmpage"`, `mk("div", "tm-docked")`, `tmDock.id = "tmdock"`, `notifyGalChange("layout")`} {
		if !strings.Contains(string(cards), want) {
			t.Errorf("cards.js's docked ticket map page lacks %s", want)
		}
	}
	avail := dashboardJSBlock(t, "\n  function ticketMapAvailable(d) {", "\n  function filesSheetOpenEl(d) {")
	if !strings.Contains(avail, `"tmdock"`) || !strings.Contains(avail, `"tmback"`) {
		t.Errorf("ticketMapAvailable does not accept the docked page or the expanded overlay:\n%s", avail)
	}
	js, err := dashboardTestAsset("assets/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"thandle", "--thr", "d-tm-handle", "js.tmap.open"} {
		if strings.Contains(string(js), gone) {
			t.Errorf("dashboard.js still names the retired %s", gone)
		}
	}
	for _, name := range []string{"assets/dashboard-drawer.css", "assets/dashboard-ticketmap.css", "assets/dashboard-ide-shell.css", "assets/dashboard-rail.css"} {
		raw, err := dashboardFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "d-tm-handle") || strings.Contains(string(raw), "--thr") {
			t.Errorf("%s still styles the retired ticket map handle", name)
		}
	}
}

// TestTicketMapSourcesNameNoRepositoryTickets is the shipped-surface check
// for the map's page code: no ticket stem and no ticket path of a real board.
func TestTicketMapSourcesNameNoRepositoryTickets(t *testing.T) {
	stem := regexp.MustCompile(`\b\d{6}-(feat|bug|refactor|chore|research|epic)-[a-z0-9-]+`)
	path := regexp.MustCompile(`ai-docs/tickets/(ready|todo|idea|\.done|\.dropped)/\d`)
	for _, name := range []string{
		"assets/dashboard-ticketmap.js", "assets/dashboard-ticketmap-viewb.js", "assets/dashboard-ticketmap.css",
		"assets/dashboard-ticketmap-geom.js", "assets/dashboard-ticketmap-graph.js",
		"assets/dashboard-drawer.js", "assets/dashboard-lifecycle.js", "assets/overview.js", "pages.go",
	} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if m := stem.Find(raw); m != nil {
			t.Errorf("%s names a ticket stem: %s", name, m)
		}
		if m := path.Find(raw); m != nil {
			t.Errorf("%s names a ticket path: %s", name, m)
		}
	}
}
