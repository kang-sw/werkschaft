package wsdashboard

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsi18n"
)

func TestRailHomeButtonLocalized(t *testing.T) {
	f := startPageFixture(t)
	for _, lang := range wsi18n.Languages() {
		for _, path := range []string{"/", "/s/livesession"} {
			code, html := getLang(t, f.base+path, lang.Code, "")
			if code != http.StatusOK {
				t.Fatalf("%s %s = %d", lang.Code, path, code)
			}
			label := wsi18n.T(lang.Code, "rail.home")
			homeBtn := `<button type="button" class="bot-btn rail-home" title="` + label + `" aria-label="` + label + `"><svg class="i li" aria-hidden="true"><use href="#l-house"/></svg><span class="bot-lbl">` + label + `</span></button>`
			if !strings.Contains(html, homeBtn) || !strings.Contains(html, `id="l-house"`) {
				t.Errorf("%s %s lacks the translated Home button or its existing house sprite", lang.Code, path)
			}
			// Shell-framed pages place Home, global settings, and #gear in the
			// bottom-left bar, #talert in bottom-center, and .usage-toggle in
			// bottom-right, retiring .rfoot from the rail and .tcen from .top.
			botIdx := strings.Index(html, `<footer class="shell-bottom" id="shell-bottom"`)
			if botIdx < 0 {
				t.Fatalf("%s %s lacks #shell-bottom", lang.Code, path)
			}
			bot := html[botIdx:]
			if end := strings.Index(bot, `</footer>`); end >= 0 {
				bot = bot[:end]
			}
			homePos := strings.Index(bot, homeBtn)
			cfgPos := strings.Index(bot, `class="bot-btn r-config-global"`)
			gearPos := strings.Index(bot, `class="bot-btn settings-toggle" id="gear"`)
			if homePos < 0 || cfgPos < 0 || gearPos < 0 || !(homePos < cfgPos && cfgPos < gearPos) {
				t.Errorf("%s %s: bottom-left bar order must be Home, global settings, then #gear: %s", lang.Code, path, bot)
			}
			if !strings.Contains(bot, `<div class="bot-center" id="slot-bot-center"><span class="talert" id="talert" hidden></span></div>`) {
				t.Errorf("%s %s: bottom-center lacks #talert slot: %s", lang.Code, path, bot)
			}
			if !strings.Contains(bot, `<div class="bot-right" id="slot-bot-right"><button type="button" class="usage-toggle"`) {
				t.Errorf("%s %s: bottom-right lacks .usage-toggle: %s", lang.Code, path, bot)
			}
			top := html[strings.Index(html, `<div class="top`):]
			top = top[:strings.Index(top, `<div class="dbody">`)]
			if strings.Contains(top, `usage-toggle`) || strings.Contains(top, `id="talert"`) {
				t.Errorf("%s %s: shell topbar must no longer contain usage-toggle or #talert: %s", lang.Code, path, top)
			}
			if strings.Contains(html, `<div class="rfoot">`) {
				t.Errorf("%s %s: shell-framed rail must retire .rfoot", lang.Code, path)
			}
		}
	}
}

// Execute the real click handler, including the early no-op return: the
// shell's later link listener must not navigate or close the rail either.
func TestRailNavigationClick(t *testing.T) {
	js := mustAsset(t, "dashboard.js")
	handler := regexp.MustCompile(`(?s)    railEl\.addEventListener\("click", function \(e\) \{\n.*?\n    \}\);`).FindString(js)
	if handler == "" {
		t.Fatal("missing rail click handler")
	}
	runNode(t, "rail navigation", `
var bad = [];
function check(what, got, want) { if (JSON.stringify(got) !== JSON.stringify(want)) bad.push(what + ": got " + JSON.stringify(got) + ", want " + JSON.stringify(want)); }
var click, calls = [], rid = "selected", shell = true;
var railEl = { addEventListener: function (name, fn) { click = fn; } };
function admissionClick() { return false; }
function saveDraft() { calls.push("draft"); }
var memoPad = { remember: function () { calls.push("memo"); } };
function railOpen(on) { calls.push(["rail", on]); }
function go(href) { calls.push(["go", href]); }
function railMarks() { calls.push("marks"); }
var colSet = { toggle: function (path) { calls.push(["workspace", path]); } };
function kidFold(row, fold) { calls.push(["kids", fold]); }
function target(kind, id) {
  var row = { getAttribute: function () { return id; }, classList: { contains: function () { return false; } } };
  var link = { closest: function (sel) { return sel === ".rs" ? row : null; } };
  return { closest: function (sel) {
    if (kind === "session" && sel === "a[href]") return link;
    if (kind === "home" && sel === ".rail-home") return {};
    if (kind === "workspace" && sel === ".wsbar") return { closest: function () { return row; } };
    if (kind === "kids" && sel === ".kidtog") return { closest: function () { return row; } };
    return null;
  } };
}
function pick(kind, id, modifiers) {
  var e = Object.assign({ target: target(kind, id), button: 0, defaultPrevented: false,
    preventDefault: function () { this.defaultPrevented = true; } }, modifiers || {});
  click(e); return e.defaultPrevented;
}
`+handler+`
[true, false].forEach(function (mode) {
  shell = mode;
  calls = [];
  check("first reselect prevents default", pick("session", rid), true);
  check("second reselect prevents default", pick("session", rid), true);
  check("reselect neither leaves nor closes the rail", calls, []);
  ["ctrlKey", "metaKey", "shiftKey", "altKey", "button"].forEach(function (key) {
    calls = []; var m = {}; m[key] = 1;
    check("modified pick keeps native link behavior: " + key, pick("session", rid, m), false);
    check("modified pick keeps existing rail behavior", calls, mode ? [["rail", false]] : ["draft", "memo", ["rail", false]]);
  });
  calls = [];
  check("a different session keeps native/shell navigation", pick("session", "other"), false);
  check("a different session still saves and closes the overlay", calls, mode ? [["rail", false]] : ["draft", "memo", ["rail", false]]);
  ["selected", ""].forEach(function (id) {
    rid = id; calls = [];
    check("Home handles its click", pick("home"), true);
    check("Home navigates from session or home", calls, mode ? [["rail", false], ["go", "/"]] : ["draft", "memo", ["rail", false], ["go", "/"]]);
  });
  rid = "selected";
});
calls = []; pick("workspace", "/project");
check("workspace collapse is unchanged", calls, [["workspace", "/project"], "marks"]);
calls = []; pick("kids", rid);
check("explicit child collapse is unchanged", calls, [["kids", true]]);
process.stdout.write(bad.join("\n"));
`)
}

// Uses disposable fixture stores and an ephemeral listener, not the shared
// daemon. The desktop case runs the Tauri update bridge over a stub native
// transport in headless Chromium.
func TestRailNavigationBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to use cached Chromium")
	}
	shots := os.Getenv("WS_RAIL_NAVIGATION_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_RAIL_NAVIGATION_SHOTS must be absolute")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", t.TempDir())
	SetDevAssets(nil)
	f := startPageFixture(t, func(o *DaemonOptions) {
		o.Port = 0
		o.LAN = false
		o.bindHost = "127.0.0.1"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "testdata/rail-navigation.browser.cjs", f.base, shots).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium rail navigation: %v", err)
	}
}
