package wsdashboard

import (
	"encoding/json"
	"strings"
	"testing"
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
		".rail .rs.cur:not(.rdkid)::after {",
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
		`<footer class="shell-bottom" id="shell-bottom" aria-hidden="true"></footer>`,
		`ws.dashboard.railWidth`,
		`ws.dashboard.railCollapsed`,
	} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("dashboard.tmpl lacks %q", want)
		}
	}
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
