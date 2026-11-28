package wsdashboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wslive"
)

// TestIdeShellPhase1Assets pins the Variant D outer chrome, inset stage well,
// 36px topbar, 26px bottom bar, active tonal+left-bar rail row, and scrolled
// well clip rules in the bundled dashboard assets.
func TestIdeShellPhase1Assets(t *testing.T) {
	css := mustAsset(t, "dashboard-ide-shell.css")
	for _, want := range []string{
		"--d-shell-bg: #e7e7e2;",
		"--d-stage-bg: #f4f4f1;",
		"--d-shell-bg: #111216;",
		"--d-stage-bg: #16181d;",
		"--stage-rim: color-mix(in srgb, var(--wb-fg) 8%, transparent);",
		"--botbar-h: 26px;",
		"--rail-w: 280px;",
		"--well-radius: 12px;",
		".stage-well-bg {",
		"box-shadow: inset 0 0 0 1px var(--stage-rim);",
		":root[data-shell] .frames {",
		"bottom: var(--botbar-h, 26px);",
		`:root[data-shell][data-fscrolled]:not([data-fpopover]):not([data-fdim]) .frames {`,
		".shell-bottom {",
		".rail-splitter {",
		`:root[data-rail-collapsed="true"] {`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("dashboard-ide-shell.css lacks %q", want)
		}
	}

	dashCSS := mustAsset(t, "dashboard.css")
	if !strings.Contains(dashCSS, ":root { --toph: 36px; --topgap: 12px; }") {
		t.Errorf("dashboard.css must set --toph: 36px")
	}

	railCSS := mustAsset(t, "dashboard-rail.css")
	for _, want := range []string{
		".rail .rs.cur {",
		"background: color-mix(in srgb, var(--wb-primary) 13%, transparent);",
		".rail .rs.cur:not(.attn)::before {",
		"width: 2.5px;",
		"background: var(--wb-primary);",
	} {
		if !strings.Contains(railCSS, want) {
			t.Errorf("dashboard-rail.css lacks active row rule %q", want)
		}
	}
	if at := strings.Index(railCSS, ".rail .rs.cur {"); at >= 0 {
		curRule := railCSS[at : at+strings.Index(railCSS[at:], "}")+1]
		if strings.Contains(curRule, "box-shadow") {
			t.Errorf(".rail .rs.cur must not carry neon box-shadow glow: %s", curRule)
		}
	}

	tmpl := mustAsset(t, "dashboard.tmpl")
	for _, want := range []string{
		`<div class="stage-well-bg" id="stage-well-bg" aria-hidden="true"></div>`,
		`<div class="rail-splitter" id="rail-splitter"`,
		`<footer class="shell-bottom" id="shell-bottom" aria-label="Status bar">`,
		`ws.dashboard.railWidth`,
		`ws.dashboard.railCollapsed`,
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("dashboard.tmpl lacks %q", want)
		}
	}
}

// TestIdeShellPhase2AssetsAndUnframedFallback pins the Phase 2 26px 3-column
// global bottom bar CSS/markup, modal dimming, compact .bu rules, and verifies
// that unframed (Frame: false) standalone renders keep their in-page .rfoot
// and .tcen controls per decision (1724).
func TestIdeShellPhase2AssetsAndUnframedFallback(t *testing.T) {
	css := mustAsset(t, "dashboard-ide-shell.css")
	for _, want := range []string{
		// Side cells size to content while #talert is hidden so the usage pill can
		// use the free middle space; a visible #talert restores symmetric 1fr columns.
		"grid-template-columns: minmax(0, auto) minmax(0, 1fr) minmax(0, auto);",
		".shell-bottom:has(.talert:not([hidden])) {",
		"grid-template-columns: minmax(0, 1fr) minmax(0, auto) minmax(0, 1fr);",
		"z-index: 46;",
		":root[data-shell][data-fdim] .shell-bottom,",
		":root[data-shell][data-fdim] .shell-bottom::after {",
		".shell-bottom .bot-left {",
		".shell-bottom .bot-center {",
		".shell-bottom .bot-right {",
		".shell-bottom .talert {",
		".shell-bottom .talert[hidden],",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("dashboard-ide-shell.css lacks Phase 2 rule %q", want)
		}
	}

	usageCSS := mustAsset(t, "usage.css")
	for _, want := range []string{
		".shell-bottom .usage-toggle {",
		".shell-bottom.bu .usage-toggle {",
		".dwrap > .top.tu .tcen > .usage-toggle {",
	} {
		if !strings.Contains(usageCSS, want) {
			t.Errorf("usage.css lacks %q", want)
		}
	}

	tmpl := mustAsset(t, "dashboard.tmpl")
	for _, want := range []string{
		`id="slot-bot-left"`,
		`id="slot-bot-center"`,
		`id="slot-bot-right"`,
		`{{define "rfoot"}}`,
		`{{region . "rail"}}{{template "rfoot"}}</aside>`,
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("dashboard.tmpl lacks Phase 2 markup %q", want)
		}
	}

	// Verify unframed standalone render (Frame: false) still includes in-page
	// .rfoot and .tcen and never includes shell-only #shell-bottom.
	unframedIdx, err := renderIndexPage(IndexView{Lang: "en"}, nil, nil, true, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<div class="rfoot">`, `<span class="tcen">`, `id="gear"`, `id="talert"`, `class="usage-toggle"`} {
		if !strings.Contains(unframedIdx, want) {
			t.Errorf("unframed index render lacks %q", want)
		}
	}
	if strings.Contains(unframedIdx, `id="shell-bottom"`) {
		t.Errorf("unframed index render must not include shell-only #shell-bottom")
	}
	assertSettingsOwner(t, unframedIdx, true)
}

// TestIdeShellPhase2BottomBarJS verifies WsRail.botFit compact step-down and
// upward viewport-clamped popover placement for #shell-bottom anchors.
func TestIdeShellPhase2BottomBarJS(t *testing.T) {
	railJS, _ := json.Marshal(mustAsset(t, "rail.js"))
	popJS, _ := json.Marshal(mustAsset(t, "dashboard-rail-popovers.js"))
	runNode(t, "phase 2 bottom bar fit and upward popover placement", "var RAIL_JS = "+string(railJS)+", POP_JS = "+string(popJS)+";\n"+`
var bad = [];
function check(what, got, want) {
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    bad.push(what + ": got " + JSON.stringify(got) + ", want " + JSON.stringify(want));
  }
}

// 1. WsRail.botFit toggles .bu when .usage-toggle overflows #slot-bot-right and clears when wide.
var rw = {};
new Function("window", "module", RAIL_JS)(rw, undefined);
var R = rw.WsRail;
check("WsRail exports botFit/botWatch", [typeof R.botFit, typeof R.botWatch], ["function", "function"]);

function makeBar(rightClientW, rightScrollW, btnRect, rightRect) {
  var classes = new Set();
  var btn = { getBoundingClientRect: function () { return btnRect; } };
  var right = {
    clientWidth: rightClientW,
    scrollWidth: rightScrollW,
    getBoundingClientRect: function () { return rightRect; },
    querySelector: function (sel) { return sel === ".usage-toggle" ? btn : null; }
  };
  return {
    classes: classes,
    classList: {
      add: function (c) { classes.add(c); },
      remove: function (c) { classes.delete(c); },
      contains: function (c) { return classes.has(c); }
    },
    querySelector: function (sel) { return sel.indexOf("bot-right") >= 0 ? right : null; }
  };
}

var wideBar = makeBar(400, 260, { left: 1000, right: 1260, width: 260 }, { left: 860, right: 1260, width: 400 });
check("botFit keeps full usage when right cell has room", [R.botFit(wideBar), wideBar.classList.contains("bu")], [0, false]);

var tightBar = makeBar(120, 260, { left: 1000, right: 1260, width: 260 }, { left: 1140, right: 1260, width: 120 });
check("botFit adds .bu when right cell overflows", [R.botFit(tightBar), tightBar.classList.contains("bu")], [1, true]);

// 1px rounding slack between scrollWidth and clientWidth must not collapse a pill that fits.
var roundBar = makeBar(400, 401, { left: 1000, right: 1260, width: 260 }, { left: 860, right: 1260, width: 400 });
check("botFit ignores 1px scroll/client rounding noise", [R.botFit(roundBar), roundBar.classList.contains("bu")], [0, false]);
var overBar = makeBar(400, 402, { left: 1000, right: 1260, width: 260 }, { left: 860, right: 1260, width: 400 });
check("botFit still collapses on a real 2px scroll overflow", [R.botFit(overBar), overBar.classList.contains("bu")], [1, true]);

// 2. Extract and run railPopPlace for bottom-bar anchors (upward clamped) vs non-bottom-bar anchors.
var m = POP_JS.match(/function railPopPlace\(\) \{[\s\S]*?\n  \}/);
if (!m) { bad.push("missing railPopPlace in dashboard-rail-popovers.js"); }
else {
  function runPlace(opts) {
    var el = { style: {}, offsetWidth: opts.w, offsetHeight: opts.h };
    var btn = { getBoundingClientRect: function () { return opts.anchor; } };
    var rp = { kind: opts.kind, P: { el: el } };
    var botBarEl = opts.inBar ? { contains: function (x) { return x === btn; } } : null;
    var root = { clientWidth: opts.vw, clientHeight: opts.vh };
    var place = new Function(
      "railPopBtn", "rp", "root", "window", "botBarEl", "railEl", "WsRail",
      m[0] + "\nrailPopPlace();"
    );
    place(
      function () { return btn; }, rp, root, { innerWidth: opts.vw, innerHeight: opts.vh },
      botBarEl, { getBoundingClientRect: function () { return { left: 0, right: 280, width: 280 }; } }, R
    );
    return el.style;
  }

  // Settings (#gear) in bottom-left opens upward, left-aligned to anchor.
  var st1 = runPlace({ kind: "settings", inBar: true, vw: 1200, vh: 800, w: 280, h: 320, anchor: { top: 777, bottom: 797, left: 118, right: 140 } });
  check("settings upward bottom-left placement", [st1.top, st1.left, st1.maxHeight], ["451px", "118px", "763px"]);

  // Usage (.usage-toggle) in bottom-right opens upward, right-aligned to anchor.
  var st2 = runPlace({ kind: "usage", inBar: true, vw: 1200, vh: 800, w: 360, h: 400, anchor: { top: 777, bottom: 797, left: 1020, right: 1192 } });
  check("usage upward bottom-right placement", [st2.top, st2.left, st2.maxHeight], ["371px", "832px", "763px"]);

  // Tall popover in short viewport clamps top to 8px and caps maxHeight to a.top - 14.
  var st3 = runPlace({ kind: "usage", inBar: true, vw: 400, vh: 300, w: 390, h: 500, anchor: { top: 277, bottom: 297, left: 360, right: 392 } });
  check("short narrow viewport clamps top, left, and maxHeight", [st3.top, st3.left, st3.maxHeight], ["8px", "8px", "263px"]);

  // Unframed/non-bottom-bar anchor clears maxHeight and uses downward WsRail.popPlace.
  var st4 = runPlace({ kind: "usage", inBar: false, vw: 1200, vh: 800, w: 300, h: 200, anchor: { top: 0, bottom: 36, left: 500, right: 700 } });
  check("unframed usage anchor clears maxHeight and opens downward", [st4.maxHeight, parseInt(st4.top, 10) >= 36], ["", true]);
}

process.stdout.write(bad.join("\n"));
`)
}

// TestIdeShellRailSplitterAndSync runs dashboard-ide-shell.js and shell.js
// under node to verify clamping (220px-360px, default 280px), RAF-throttled
// pointer drag (zero storage writes during drag, one write on pointerup),
// keyboard resize, double-click reset, collapse/expand, and shell-to-frame
// rail-layout message validation and application.
func TestIdeShellRailSplitterAndSync(t *testing.T) {
	ideJS, _ := json.Marshal(mustAsset(t, "dashboard-ide-shell.js"))
	shellJS, _ := json.Marshal(mustAsset(t, "shell.js"))
	runNode(t, "ide shell splitter and rail-layout sync", "var IDE_JS = "+string(ideJS)+", SHELL_JS = "+string(shellJS)+";\n"+`
var bad = [];
function check(what, got, want) {
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    bad.push(what + ": got " + JSON.stringify(got) + ", want " + JSON.stringify(want));
  }
}

// 1. WsShell.message validates rail-layout messages.
var sw = {};
new Function("window", "module", SHELL_JS)(sw, undefined);
var S = sw.WsShell;
check("rail-layout valid", S.message({ wsShell: "rail-layout", width: 220, collapsed: false }), { wsShell: "rail-layout", width: 220, collapsed: false });
check("rail-layout accepts numeric width and bool collapsed", S.message({ wsShell: "rail-layout", width: 360, collapsed: true }), { wsShell: "rail-layout", width: 360, collapsed: true });
check("rail-layout rejects invalid width", S.message({ wsShell: "rail-layout", width: "abc", collapsed: false }), null);

// 2. Shell mode: hydration, RAF drag, persistence, keyboard, collapse, frame broadcast.
var store = { "ws.dashboard.railWidth": "312" }, writes = [];
var attrs = {}, props = {}, splitterAttrs = {}, btnAttrs = {};
var listeners = {}, winListeners = {}, rafQueue = [], sent = [];
var splitter = {
  setAttribute: function (k, v) { splitterAttrs[k] = String(v); },
  addEventListener: function (ev, fn) { (listeners[ev] = listeners[ev] || []).push(fn); },
  setPointerCapture: function () {},
  hasPointerCapture: function () { return true; },
  releasePointerCapture: function () {}
};
var railBtn = { setAttribute: function (k, v) { btnAttrs[k] = String(v); } };
var root = {
  style: {
    setProperty: function (k, v) { props[k] = String(v); },
    getPropertyValue: function (k) { return props[k] || ""; }
  },
  setAttribute: function (k, v) { attrs[k] = String(v); },
  getAttribute: function (k) { return k in attrs ? attrs[k] : null; },
  removeAttribute: function (k) { delete attrs[k]; }
};
var win = {
  addEventListener: function (ev, fn) { (winListeners[ev] = winListeners[ev] || []).push(fn); }
};
var frames = [{ id: "f1" }, { id: "f2" }];
var fn = new Function(
  "window", "root", "shell", "framed", "railBtn", "railWide", "load", "save", "$",
  "shSend", "shFrames", "shOn", "requestAnimationFrame", "cancelAnimationFrame",
  IDE_JS + "\nreturn { ideRailApplyFromShell: ideRailApplyFromShell };"
);
fn(
  win, root, true, false, railBtn, function () { return true; },
  function (k) { return k in store ? store[k] : null; },
  function (k, v) { writes.push([k, v]); if (v === null) delete store[k]; else store[k] = String(v); return true; },
  function (id) { return id === "rail-splitter" ? splitter : null; },
  function (ent, kind, body) { sent.push({ id: ent.id, kind: kind, body: body }); },
  frames, frames[0],
  function (cb) { rafQueue.push(cb); return rafQueue.length; },
  function () { rafQueue = []; }
);

check("hydrated width from localStorage", [props["--rail-w"], splitterAttrs["aria-valuenow"], btnAttrs["aria-expanded"]], ["312px", "312", "true"]);

// Pointer drag: two rapid pointermoves coalesce into 1 rAF and 0 localStorage writes until pointerup.
listeners.pointerdown[0]({ button: 0, pointerId: 7, clientX: 312, preventDefault: function () {} });
check("resizing attribute set on pointerdown", "data-rail-resizing" in attrs, true);
winListeners.pointermove[0]({ pointerId: 7, clientX: 250 });
winListeners.pointermove[0]({ pointerId: 7, clientX: 180 }); // clamps to 220
check("no storage writes during drag", writes.length, 0);
check("single rAF queued for coalesced moves", rafQueue.length, 1);
rafQueue.shift()();
check("drag flush applies clamped 220px and notifies active frame", [props["--rail-w"], sent.length, sent[0]], ["220px", 1, { id: "f1", kind: "rail-layout", body: { width: 220, collapsed: false } }]);
winListeners.pointerup[0]({ pointerId: 7 });
check("pointerup clears resizing, persists once, broadcasts to all frames", [
  "data-rail-resizing" in attrs,
  writes,
  sent.slice(1)
], [
  false,
  [["ws.dashboard.railWidth", "220"]],
  [
    { id: "f1", kind: "rail-layout", body: { width: 220, collapsed: false } },
    { id: "f2", kind: "rail-layout", body: { width: 220, collapsed: false } }
  ]
]);

// Keyboard End -> 360px, dblclick -> 280px, collapse toggle -> data-rail-collapsed.
writes = []; sent = [];
listeners.keydown[0]({ key: "End", preventDefault: function () {} });
check("End key jumps to 360px", [props["--rail-w"], writes[0]], ["360px", ["ws.dashboard.railWidth", "360"]]);
listeners.dblclick[0]({ preventDefault: function () {} });
check("dblclick resets to 280px", props["--rail-w"], "280px");
win.WsIdeShell.toggleCollapse();
check("toggleCollapse sets attribute and aria-expanded=false", [attrs["data-rail-collapsed"], btnAttrs["aria-expanded"], store["ws.dashboard.railCollapsed"]], ["true", "false", "1"]);
win.WsIdeShell.toggleCollapse();
check("toggleCollapse restores expanded state", ["data-rail-collapsed" in attrs, btnAttrs["aria-expanded"], "ws.dashboard.railCollapsed" in store], [false, "true", false]);

// 3. Framed page receives rail-layout and triggers WsCards.relayout + queueGallery + ideFrameQueueHead.
var fAttrs = {}, fProps = {}, relayouts = 0, galleries = 0, heads = 0;
var fRoot = {
  style: {
    setProperty: function (k, v) { fProps[k] = String(v); },
    getPropertyValue: function (k) { return fProps[k] || ""; }
  },
  setAttribute: function (k, v) { fAttrs[k] = String(v); },
  getAttribute: function (k) { return k in fAttrs ? fAttrs[k] : null; },
  removeAttribute: function (k) { delete fAttrs[k]; }
};
var fWin = { WsCards: { relayout: function () { relayouts++; } } };
var fApi = new Function(
  "window", "root", "shell", "framed", "railBtn", "railWide", "load", "save", "$",
  "queueGallery", "onHead",
  IDE_JS + "\nideFrameQueueHead = onHead;\nreturn { apply: ideRailApplyFromShell };"
)(
  fWin, fRoot, false, true, null, function () { return true; },
  function () { return null; }, function () { return false; }, function () { return null; },
  function () { galleries++; },
  function () { heads++; }
);
fApi.apply(220, false);
check("framed page applies 220px and relayouts", [fProps["--rail-w"], relayouts, galleries, heads], ["220px", 1, 1, 1]);
fApi.apply(220, false);
check("duplicate rail-layout is a no-op", [relayouts, galleries, heads], [1, 1, 1]);
fApi.apply(360, true);
check("framed page applies 360px + collapsed", [fProps["--rail-w"], fAttrs["data-rail-collapsed"], relayouts, galleries, heads], ["360px", "true", 2, 2, 2]);
fApi.apply(999, false);
check("framed page clamps out-of-range width to 360px", fProps["--rail-w"], "360px");
fApi.apply(50, false);
check("framed page clamps low width to 220px", fProps["--rail-w"], "220px");

process.stdout.write(bad.join("\n"));
`)
}

// TestIdeShellPhase3AssetsAndTopbarJS pins the Phase 3 3-column 36px IDE topbar
// CSS/markup, Top-Right click-through, centered search pill, .worktree-branch
// conditional telemetry, Phase 2 minor polishes (#memo-pad clearance and #talert
// narrow cap), and WsRail.topOver support for .top-left.
func TestIdeShellPhase3AssetsAndTopbarJS(t *testing.T) {
	css := mustAsset(t, "dashboard-ide-shell.css")
	for _, want := range []string{
		"grid-template-columns: minmax(0, 1fr) auto minmax(var(--sh-right-w, 0px), 1fr);",
		":root[data-shell] .dwrap > .top > .top-left {",
		":root[data-shell] .dwrap > .top > .top-center {",
		":root[data-shell] .dwrap > .top > .top-right {",
		"pointer-events: none;",
		":root[data-shell] .dwrap > .top.tmeas .tph-id {",
		":root[data-frame] .dr-search-handle,",
		"max-width: min(100%, 44vw);",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("dashboard-ide-shell.css lacks Phase 3 rule %q", want)
		}
	}

	memoCSS := mustAsset(t, "memo.css")
	if !strings.Contains(memoCSS, ":root[data-shell] .memo-pad { bottom: calc(var(--botbar-h, 26px) + 12px); }") {
		t.Errorf("memo.css lacks shell bottom-bar clearance for .memo-pad")
	}

	tmpl := mustAsset(t, "dashboard.tmpl")
	for _, want := range []string{
		`<div class="top-left" id="slot-top-left">`,
		`<div class="top-center" id="slot-top-center">`,
		`<div class="top-right" id="slot-top-right" aria-hidden="true"></div>`,
		`class="qsslot" id="qsslot" data-shell-qs="1"`,
		`id="tkqs-kbd"`,
		`class="tph tph-branch"`,
		`class="worktree-branch"`,
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("dashboard.tmpl lacks Phase 3 markup %q", want)
		}
	}

	// Verify git-backed vs non-git session header rendering: .worktree-branch
	// appears only when Git.Branch is non-empty, and never on non-session index.
	gitView := SessionView{
		Lang:    "en",
		ReplyID: "gitsess",
		Label:   "Git Lead",
		Git:     &GitBadge{State: gitOK, Repo: wslive.Repo{Branch: "impl/develop/test"}},
		Harness: "claude",
		Adapter: true,
	}
	gitRegions, err := renderSessionRegions(gitView, nil)
	if err != nil {
		t.Fatal(err)
	}
	if head := string(gitRegions[regionHead].HTML); !strings.Contains(head, `class="worktree-branch"`) || !strings.Contains(head, `impl/develop/test`) {
		t.Errorf("git-backed session header must render .worktree-branch with branch name, got: %s", head)
	}

	nonGitView := SessionView{
		Lang:    "en",
		ReplyID: "plainsess",
		Label:   "Plain Lead",
		Harness: "claude",
		Adapter: true,
	}
	nonGitRegions, err := renderSessionRegions(nonGitView, nil)
	if err != nil {
		t.Fatal(err)
	}
	if head := string(nonGitRegions[regionHead].HTML); strings.Contains(head, `class="worktree-branch"`) {
		t.Errorf("non-git session header must omit .worktree-branch, got: %s", head)
	}

	// Verify WsRail.topOver measures :scope > .top-left when present.
	railJS, _ := json.Marshal(mustAsset(t, "rail.js"))
	runNode(t, "phase 3 topOver measures top-left", "var RAIL_JS = "+string(railJS)+";\n"+`
var bad = [];
function check(what, got, want) {
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    bad.push(what + ": got " + JSON.stringify(got) + ", want " + JSON.stringify(want));
  }
}
var rw = {};
new Function("window", "module", RAIL_JS)(rw, undefined);
var R = rw.WsRail;

function makeTop(leftW, scrollW, childRight) {
  var classes = new Set();
  var left = {
    clientWidth: leftW,
    scrollWidth: scrollW,
    getBoundingClientRect: function () { return { left: 0, right: leftW, width: leftW }; },
    querySelectorAll: function () { return [{ getBoundingClientRect: function () { return { left: 0, right: childRight, width: childRight }; } }]; }
  };
  return {
    classes: classes,
    classList: {
      add: function () { Array.prototype.forEach.call(arguments, function (c) { classes.add(c); }); },
      remove: function () { Array.prototype.forEach.call(arguments, function (c) { classes.delete(c); }); },
      contains: function (c) { return classes.has(c); }
    },
    querySelector: function (sel) { return sel === ":scope > .top-left" ? left : null; }
  };
}

var fitsTop = makeTop(400, 260, 260);
check("topFit keeps level 0 when top-left fits", [R.topFit(fitsTop, {}), fitsTop.classList.contains("tch")], [0, false]);

var tightTop = makeTop(200, 320, 320);
check("topFit steps to level 2 (.tch) when top-left overflows", [R.topFit(tightTop, {}), tightTop.classList.contains("tch")], [2, true]);

process.stdout.write(bad.join("\n"));
`)
}

// TestIdeShellDaemonConfirmOverlayCSS pins shell-mode overrides for the 1-click
// daemon confirmation banner ([data-daemon-confirm]): dashboard-ide-shell.css
// loads after dashboard-daemon-confirm.css, keeps the non-shell fallback intact,
// aligns .daemon-confirm-banner to --rail-reserve, includes --daemon-confirm-space
// in .stage-well-bg and .rail-splitter top offsets, and omits --toph from
// docked .rail margin-block-start under :root[data-shell].
func TestIdeShellDaemonConfirmOverlayCSS(t *testing.T) {
	indexOf := func(name string) int {
		for i, f := range dashboardStyleFiles {
			if f == name {
				return i
			}
		}
		return -1
	}
	confirmIdx := indexOf("dashboard-daemon-confirm.css")
	shellIdx := indexOf("dashboard-ide-shell.css")
	if confirmIdx < 0 || shellIdx <= confirmIdx {
		t.Fatalf("dashboardStyleFiles must load dashboard-ide-shell.css (%d) after dashboard-daemon-confirm.css (%d)", shellIdx, confirmIdx)
	}

	shellCSS := mustAsset(t, "dashboard-ide-shell.css")
	for _, want := range []string{
		"top: calc(var(--toph, 36px) + var(--account-banner-space, 0px) + var(--daemon-confirm-space, 0px));",
		"top: calc(var(--toph, 36px) + var(--account-banner-space, 0px) + var(--daemon-confirm-space, 0px) + 8px);",
		":root[data-shell] .daemon-confirm-banner {",
		"margin-inline-start: calc(var(--rail-reserve, 288px) + var(--sh-stage-side, 0px));",
		":root[data-shell][data-account-banner] .dwrap .dbody > .rail,\n  :root[data-shell][data-daemon-confirm] .dwrap .dbody > .rail {",
		"margin-block-start: calc(-1 * (var(--account-banner-space, 0px) + var(--daemon-confirm-space, 0px)));",
	} {
		if !strings.Contains(shellCSS, want) {
			t.Errorf("dashboard-ide-shell.css lacks daemon-confirm shell rule %q", want)
		}
	}

	confirmCSS := mustAsset(t, "dashboard-daemon-confirm.css")
	for _, want := range []string{
		".daemon-confirm-banner { margin-inline-start: calc(348px + var(--sh-stage-side, 0px)); }",
		":root[data-daemon-confirm] .dwrap .dbody > .rail {\n    margin-block-start: calc(-1 * (var(--toph) + var(--account-banner-space, 0px) + var(--daemon-confirm-space, 0px)));",
	} {
		if !strings.Contains(confirmCSS, want) {
			t.Errorf("dashboard-daemon-confirm.css non-shell fallback changed unexpectedly; lacks %q", want)
		}
	}
}
