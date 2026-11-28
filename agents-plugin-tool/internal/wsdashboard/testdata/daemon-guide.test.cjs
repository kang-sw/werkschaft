"use strict";

// Interactive guide client contracts (dashboard-daemon-guide.js / .css):
// registration next to the daemon bridge, the bridge delegation hook, the
// monitor push wiring, text-only comment rendering, finite motion, file-size
// budgets, i18n keys, and the frame/settings ref resolvers.

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { readAssetSource } = require("./asset-source.cjs");

const root = path.join(__dirname, "..");
const read = (name) => fs.readFileSync(path.join(root, "assets", name), "utf8");
const guideJS = read("dashboard-daemon-guide.js");
const guideCSS = read("dashboard-daemon-guide.css");

function goList(variable) {
  const go = fs.readFileSync(path.join(root, "pages.go"), "utf8");
  const m = go.match(new RegExp("var " + variable + " = \\[\\]string\\{([^}]*)\\}"));
  assert.ok(m, variable + " not found");
  return [...m[1].matchAll(/"([^"]+)"/g)].map((e) => e[1]);
}

test("guide assets load next to the daemon bridge in the shared bundle", () => {
  const js = goList("dashboardScriptFiles");
  assert.equal(js[js.indexOf("dashboard-daemon-bridge.js") + 1], "dashboard-daemon-guide.js");
  const css = goList("dashboardStyleFiles");
  assert.equal(css[css.indexOf("dashboard-daemon-confirm.css") + 1], "dashboard-daemon-guide.css");
  const bundle = readAssetSource("wsdashboard", "dashboard.js");
  assert.ok(bundle.includes("window.WsDaemonGuide = {"));
  assert.ok(readAssetSource("wsdashboard", "dashboard.css").includes(".dg-layer"));
});

test("guide modules stay within the 1,000-line budget", () => {
  for (const [name, src] of [["dashboard-daemon-guide.js", guideJS], ["dashboard-daemon-guide.css", guideCSS], ["dashboard-daemon-bridge.js", read("dashboard-daemon-bridge.js")]]) {
    assert.ok(src.split("\n").length < 1000, name + " exceeds 1,000 lines");
  }
});

test("the bridge delegates guide.highlight before its unknown-op reply", () => {
  const bridge = read("dashboard-daemon-bridge.js");
  const hook = bridge.indexOf('op === "guide.highlight"');
  const fallback = bridge.lastIndexOf('await postDaemonBridgeReply(req, null, "invalid_request");');
  assert.ok(hook > 0 && hook < fallback, "guide.highlight hook precedes the invalid_request fallback");
  assert.match(bridge.slice(hook, fallback), /window\.WsDaemonGuide/);
});

test("the stream relays daemon-guide-monitor pushes and resets them on drop", () => {
  const stream = read("dashboard-stream.js");
  assert.match(stream, /"daemon-guide-monitor": onDaemonGuideMonitor/);
  assert.match(stream, /new CustomEvent\("ws-daemon-guide-monitor"/);
  const drop = stream.slice(stream.indexOf("drop: function () {"), stream.indexOf("events: {"));
  assert.match(drop, /ws-daemon-guide-monitor/, "a dropped stream hides the chip until the next push");
});

test("comments render as text only and the overlay is excluded from snapshots", () => {
  assert.doesNotMatch(guideJS, /innerHTML|outerHTML|insertAdjacentHTML|document\.write/);
  assert.match(guideJS, /text\.textContent = slot\.comment/);
  assert.match(guideJS, /setAttribute\("data-ws-dom-exclude", "1"\)/);
});

test("monitor frames carry structure only, never route_changed or text", () => {
  assert.doesNotMatch(guideJS, /monitor_event:\s*"route_changed"/);
  assert.doesNotMatch(guideJS, /route_changed: 1/);
  const send = guideJS.slice(guideJS.indexOf("function emit("), guideJS.indexOf("function renderChip("));
  assert.doesNotMatch(send, /textContent|\.value\b/);
});

test("motion is finite and transform/opacity only; tracking has one rAF site", () => {
  assert.doesNotMatch(guideCSS, /infinite/);
  const frames = guideCSS.slice(guideCSS.indexOf("@keyframes dg-pulse"));
  const body = frames.slice(0, frames.indexOf("\n}\n"));
  for (const prop of body.matchAll(/([a-z-]+):/g)) {
    assert.ok(prop[1] === "opacity" || prop[1] === "transform", "keyframe animates " + prop[1]);
  }
  assert.match(guideCSS, /prefers-reduced-motion/);
  assert.equal((guideJS.match(/requestAnimationFrame\(/g) || []).length, 1);
});

test("guide i18n keys exist in the English table", () => {
  const en = JSON.parse(fs.readFileSync(path.join(root, "..", "wsi18n", "tables", "en.json"), "utf8"));
  for (const m of guideJS.matchAll(/wsT\("([^"]+)"\)/g)) assert.ok(en[m[1]], "missing " + m[1]);
  assert.equal(en["js.dc.guide.monitor_stop"], "Guide monitoring · Stop");
});

test("the frame-side ref resolver checks generation, presence, and liveness", () => {
  const frameSide = guideJS.slice(0, guideJS.indexOf("if (shell && daemonBridgeEnabled)"));
  const live = { isConnected: true }, dead = { isConnected: false };
  const ctx = { framed: true, dcFrameGen: 3, dcFrameRefs: new Map([["g3-r1", live], ["g3-r2", dead]]), window: {} };
  vm.runInNewContext(frameSide, ctx);
  const resolve = ctx.window.wsDaemonGuideRef;
  assert.equal(typeof resolve, "function");
  assert.equal(resolve(3, "g3-r1").el, live);
  assert.equal(resolve(0, "g3-r1").el, live, "generation 0 skips the assertion");
  assert.equal(resolve(2, "g3-r1").error, "stale_ref");
  assert.equal(resolve(3, "g3-r9").error, "ref_not_found");
  assert.equal(resolve(3, "g3-r2").error, "stale_ref");
  ctx.dcFrameGen = 0;
  assert.equal(resolve(0, "g3-r1").error, "stale_ref", "no snapshot yet");
});

test("the settings page exposes the same ref resolver", () => {
  const settings = read("dashboard-settings.js");
  const at = settings.indexOf("window.wsDaemonGuideRef = function");
  assert.ok(at > 0, "settings resolver present");
  const fn = settings.slice(at, settings.indexOf("\n  };", at) + 5);
  const live = { isConnected: true, closest: () => null };
  const excluded = { isConnected: true, closest: () => ({}) };
  const ctx = { dcSettingsGen: 2, dcSettingsRefs: new Map([["g2-r1", live], ["g2-r2", excluded]]), window: {} };
  vm.runInNewContext(fn, ctx);
  assert.equal(ctx.window.wsDaemonGuideRef(2, "g2-r1").el, live);
  assert.equal(ctx.window.wsDaemonGuideRef(1, "g2-r1").error, "stale_ref");
  assert.equal(ctx.window.wsDaemonGuideRef(2, "nope").error, "ref_not_found");
  assert.equal(ctx.window.wsDaemonGuideRef(2, "g2-r2").error, "target_excluded");
});
