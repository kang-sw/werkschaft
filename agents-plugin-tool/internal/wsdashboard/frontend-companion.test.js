const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

function companion(globals = {}) {
  const context = vm.createContext({ window: {}, URLSearchParams, AbortController, ...globals });
  for (const name of ["builtins", "packs", "focus", "deform", "compositor", "scenarios", "menu", "reflex", "quip", "vrm-fx", "vrm-studio", "vrm-props", "vrm-pose", "vrm-colead", "vrm-speech", "vrm", "stage"]) {
    vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", `dashboard-companion-${name}.js`), "utf8"), context);
  }
  return context.window.WsCompanion;
}

function snapshot(...entries) {
  const nodes = entries.map(([rid, x = {}]) => ({
    dataset: {rid, asks: x.asks ?? "", busy: x.busy ? "1" : "", active: x.active ? "1" : ""},
    matches: () => !!x.ended,
    hasAttribute: key => key === "data-ap" ? !!x.approval : key === "data-g" && !!x.parent,
    querySelector: key => (key === ".hb.err" && x.failure || key === ".kap" && x.rollup) ? {} : null,
    closest: key => key === ".rkids" && x.parent ? {dataset: {parent: x.parent}} : null
  }));
  return {querySelectorAll: key => { assert.equal(key, ".rs[data-rid]"); return nodes; }};
}

test("companion rail snapshots baseline silently and arbitrate error, child approval, asks and background turns", () => {
  const { railSignals } = companion(), observer = railSignals();
  const busy = {busy: true, active: true};
  assert.equal(observer.read(snapshot(["active"], ["background", busy], ["parent"], ["child", {parent: "parent"}]), "active", true), null);
  assert.equal(observer.posture("active", "idle").activity, "tool");
  assert.equal(observer.posture("background", "idle").activity, "idle");
  const failed = observer.read(snapshot(["active", {asks: "3 4"}], ["background", {failure: true}],
    ["parent", {rollup: true}], ["child", {parent: "parent", approval: true}]), "active", false);
  assert.equal(failed.kind, "session.error");
  assert.equal(failed.rid, "background");
  assert.equal(observer.posture("active", "thinking").rid, "child");
  assert.equal(observer.posture("active", "error").activity, "permission");
  assert.equal(observer.posture("active", "permission").rid, "active");
  assert.equal(observer.read(snapshot(["active", {asks: "4"}], ["background"], ["parent"], ["child", {parent: "parent"}]), "active", false), null);
  const approved = observer.read(snapshot(["active", {asks: "4"}], ["background", {asks: "1"}],
    ["parent", {rollup: true}], ["child", {parent: "parent", approval: true}]), "active", false);
  assert.equal(approved.token, "perm:child");
  assert.equal(observer.read(snapshot(["active", {asks: "4"}], ["background", {asks: "1"}], ["parent", {rollup: true}]), "active", false).token, "perm:parent");
  assert.equal(observer.read(snapshot(["active", {asks: "4 7"}], ["background", {approval: true}]), "active", false).token, "ask:active:7");
  assert.equal(observer.read(snapshot(["active", busy], ["background", busy]), "active", true), null);
  assert.equal(observer.read(snapshot(["active", busy], ["background", {busy: true}]), "active", false), null);
  const done = observer.read(snapshot(["active"], ["background"]), "active", false);
  assert.equal(done.rid, "background");
  assert.equal(done.kind, "global.session_done");
  assert.equal(observer.read(snapshot(["active"], ["background", {busy: true}]), "active", false), null);
  assert.equal(observer.read(snapshot(["active"], ["background"]), "active", false), null);
  assert.equal(observer.read(snapshot(["active"], ["background", {failure: true, asks: "11"}]), "active", true), null);
  assert.equal(observer.posture("active", "typing_user").activity, "error");
  assert.equal(observer.read(snapshot(["active"], ["background", {failure: true, asks: "11"}]), "active", false), null);
});

test("companion cross-source ask and permission dedup prefers the frame in either arrival order", () => {
  const { signalPairs, scenarioRunner } = companion(), pairs = signalPairs(), runner = scenarioRunner();
  const state = {routeKind: "session", pose: "stand", idleMs: 0};
  for (const [token, first, second] of [["ask:current:4", "rail", "frame"], ["perm:current", "frame", "rail"]]) {
    assert.equal(pairs.observe({token, source: first}, 0), false);
    assert.equal(pairs.observe({token, source: second}, 200), true);
    assert.equal(pairs.observe({token, source: first}, 400), false);
  }
  const rail = {token: "ask:other:3", source: "rail", rid: "other"};
  const frame = {token: "ask:current:4", source: "frame", rid: "current"};
  runner.fire("ask.open", 0, state, rail);
  runner.fire("ask.open", 100, state, frame);
  runner.fire("ask.open", 110, state, rail);
  assert.equal(runner.tick(110).signal.rid, "current");
  runner.reset();
  runner.fire("ask.open", 200, state, rail);
  runner.preferFrame({...rail, source: "frame"});
  assert.equal(runner.tick(300).start, 200);
  assert.equal(runner.tick(300).signal.source, "frame");
});

test("companion keyframes run in seconds, preserve step jumps and hold non-looping endpoints", () => {
  const { motion } = companion();
  const keys = [{t: 0, tx: 0}, {t: 0.5, tx: 10}, {t: 0.5, tx: 40}, {t: 1, tx: 60, sx: 2}];
  const loop = motion({keyframes: keys});
  assert.equal(loop(0.25).tx, 5);
  assert.equal(loop(0.499).tx, 9.98);
  assert.equal(loop(0.5).tx, 40);
  assert.equal(loop(0.75).tx, 50);
  assert.equal(loop(0.75).sx, 1.5);
  assert.equal(loop(1).tx, 0);
  assert.equal(loop(1.5).tx, 40);
  assert.equal(motion({keyframes: keys, loop: false})(3).tx, 60);
  assert.equal(motion({keyframes: [{t: 0, ty: 2}, {t: 0, ty: 9}]})(5).ty, 9);
  assert.equal(motion({keyframes: [{t: 3, ty: 7}]})(0).ty, 7);
});

test("companion knee tracks rotate continuously with independently phased pivots", () => {
  const { motion } = companion();
  const tracks = {knee: {oscillate: {rotDeg: [-7, 7], hz: 0.6}}};
  const a = motion("knee", tracks), b = motion({use: "knee", phase: Math.PI}, tracks);
  assert.equal(a(0).rotDeg, -7);
  assert.equal(b(0).rotDeg, 7);
  const first = a(0.15).rotDeg, second = a(0.16).rotDeg;
  assert.ok(-7 < first && first < second && second < 7);
  assert.ok(Math.abs(a(0.16).rotDeg + b(0.16).rotDeg) < 1e-9);
  assert.equal(motion("wave-pivot")(0).rotDeg, -22);
});

test("companion easing supports anticipation, overshoot, declared and inline curves", () => {
  const { motion, solveBezier } = companion();
  const track = ease => ({loop: false, keyframes: [{t: 0, tx: 0, ease}, {t: 1, tx: 100}]});
  assert.ok(motion(track("anticipate"))(0.2).tx < 0);
  assert.ok(motion(track("overshoot"))(0.7).tx > 100);
  const custom = [0.1, 0.8, 0.2, 0.9];
  assert.ok(motion(track(custom))(0.5).tx > 80);
  assert.equal(motion(track("custom"), {}, {custom})(0.5).tx, motion(track(custom))(0.5).tx);
  assert.equal(motion(track([0, 0, 1.1, 3]))(0.5).tx, 50);
  assert.equal(motion(track(123))(0.5).tx, 50);
  assert.equal(solveBezier(1, custom), 1);
});

test("companion timing overrides safe curves and falls back from opaque v1 extensions", () => {
  const { timing } = companion();
  const defaults = timing({passive: "custom", easings: 123});
  const edited = timing({
    stageEffects: {stay: {keyframes: [{p: 0, lift: 0}, {p: 1, lift: -1}]}},
    propMotions: {attach: {keyframes: [{p: 0, scale: 1}, {p: 1, scale: 2}], edge: "left"},
      "hand-liftoff": {keyframes: [{p: 0, alpha: 2}]},
      broken: {keyframes: [{p: 0.5}, {p: 0.4}]}},
    physics: {drag: {stiffness: 300, damping: 0, impulse: 0, maxDeg: 45, bodyFallbackScale: 0}},
    passive: {typing_user: {gesture: "point"}, streaming: {pose: "legacy", gaze: "prop:drone"}, thinking: {gesture: "bad path"}}
  });
  assert.equal(edited.stage.stay.sample(0.5, {}).lift, -0.5);
  assert.equal(edited.prop.attach.sample(0.5, {}).scale, 1.5);
  assert.equal(edited.prop.attach.edge, "left");
  assert.equal(edited.prop["hand-liftoff"].sample(0.3, {}).along, defaults.prop["hand-liftoff"].sample(0.3, {}).along);
  assert.equal(edited.prop.broken, undefined);
  assert.equal(edited.drag.stiffness, 300);
  assert.equal(edited.drag.damping, 7.5);
  assert.equal(edited.drag.impulse, 0);
  assert.equal(edited.drag.maxSwing, Math.PI / 4);
  assert.equal(edited.drag.bodyFallbackScale, 0);
  assert.equal(edited.passive.typing_user, edited.passive.user_typing);
  assert.equal(edited.passive.user_typing.gesture, "point");
  assert.equal(edited.passive.replying.pose, "legacy");
  assert.equal(edited.passive.replying.gaze, "prop:drone");
  assert.equal(timing({passive: {replying: {}, streaming: {pose: "legacy"}}}).passive.replying.pose, "typing");
  assert.equal(edited.passive.thinking.gesture, "coffee");
  assert.equal(defaults.passive.thinking.fx, "ponder");
});

test("companion starters select motion per beat while preserving legacy scenario overrides", () => {
  const { scenarioRunner } = companion();
  const guards = {routeKind: "session", pose: "stand", idleMs: 0};
  const runner = scenarioRunner({legacy: {on: "session.streaming", pose: "perched"}});
  runner.fire("mail.send", 0, guards);
  assert.equal(runner.tick(0).beat.prop.motion, "hand-liftoff");
  assert.equal(runner.tick(0).beat.prop.from, "hand");
  runner.fire("mail.recv", 1, guards);
  assert.equal(runner.tick(1).beat.prop.motion, "bonk-from-edge");
  assert.equal(runner.tick(1).beat.prop.id, "mail");
  runner.fire("mail.recv", 2, guards);
  assert.equal(runner.tick(2).count, 2);
  runner.fire("user.drag", 3, guards);
  assert.equal(runner.tick(3, true).beat.prop, undefined);
  runner.clear();
  runner.fire("session.streaming", 4, guards);
  assert.equal(runner.tick(4).entry.id, "legacy");
  runner.fire("session.replying", 5, guards);
  assert.equal(runner.tick(5).entry.id, "working_tatata");
  const override = scenarioRunner({mailbox_bonk: {prop: "mail", durationMs: 500}});
  override.fire("mail.recv", 0, guards);
  assert.equal(override.tick(0).beat.prop, "mail");
  assert.equal(override.tick(500), null);
});

function technicalRig() {
  const manifest = {
    canvas: [512, 512],
    head: {egg: {file: "egg.png"}, pivot: [256, 130], eyes: {sclera: "eyes.png", irisLine: "iris.png", maxPan: [13, 8]},
      gazeTiltDeg: 2, propGazeTiltDeg: 17},
    poses: {
      stand: {handAnchor: [250, 275], layers: [{file: "body.png"}]},
      dangle: {layers: [{file: "body.png"},
        {file: "arm.png", pivot: [180, 240], swingWeight: 0.8, swingLag: 0.1},
        {file: "leg.png", pivot: [285, 325], swingWeight: -0.5, swingLag: 0.03}]}
    },
    gestures: {
      cup_hands: {handAnchor: [210, 270], layers: [{file: "hands.png"}]},
      invalid: {handAnchor: [-1, 999]}
    }
  };
  const bitmaps = new Map(["egg", "eyes", "iris", "body", "arm", "leg", "hands"].map(id => [id + ".png", {id}]));
  return {manifest, bitmaps};
}

test("technical rig uses gesture and pose anchors, independent limb inertia, and head plus pupil prop tracking", () => {
  const paints = [], rotations = [], pivots = [];
  class Canvas {
    getContext() {
      return {
        canvas: this, save() {}, restore() {}, clearRect() {}, scale() {},
        translate(x, y) { pivots.push([x, y]); },
        rotate(theta) { rotations.push(theta); },
        drawImage(image, x, y) { paints.push([image.id, x, y]); },
        getImageData: () => ({data: []})
      };
    }
  }
  const { makeRig, RAD } = companion({OffscreenCanvas: Canvas});
  const rig = makeRig(technicalRig(), null, "");
  assert.deepEqual([...rig.view("stand", "cup_hands", "idle", []).handAnchor], [210, 270]);
  assert.deepEqual([...rig.view("stand", "invalid", "idle", []).handAnchor], [250, 275]);
  const stand = rig.view("stand", null, "idle", []), dangle = rig.view("dangle", null, "idle", []);
  assert.deepEqual([...dangle.handAnchor], [256, 512 * 0.48]);
  assert.equal(stand.articulatedDrag, false);
  assert.equal(dangle.articulatedDrag, true);
  function render(view, gaze, physics) {
    rotations.length = pivots.length = paints.length = 0;
    view.render(0, gaze, physics);
  }
  render(dangle, {x: 0, y: 0}, {swing: 0.25, swingVel: 0.5});
  assert.ok(pivots.some(([x, y]) => x === 180 && y === 240));
  assert.ok(pivots.some(([x, y]) => x === 285 && y === 325));
  assert.ok(Math.abs(rotations[0] - 0.15) < 1e-12);
  assert.ok(Math.abs(rotations[1] + 0.14) < 1e-12);
  render(stand, {x: 1, y: -1, prop: true});
  assert.ok(rotations.includes(17 * RAD));
  assert.ok(paints.some(([id, x, y]) => id === "iris" && x === 13 && y === -8));
  render(stand, {x: -1, y: 0});
  assert.ok(rotations.includes(-2 * RAD));
  assert.ok(paints.some(([id, x]) => id === "iris" && x === -13));
});

test("companion gestures can select a pose or remove slots without adding layers", () => {
  class Canvas {
    constructor(width, height) {
      this.width = width;
      this.height = height;
      this.drawn = [];
    }
    getContext() {
      return {
        canvas: this,
        save() {},
        restore() {},
        clearRect: () => { this.drawn.length = 0; },
        drawImage: image => { this.drawn.push(image); },
        getImageData: () => ({data: []})
      };
    }
  }
  const { makeRig } = companion({OffscreenCanvas: Canvas});
  const pack = {
    manifest: {
      canvas: [512, 512],
      poses: {
        stand: {layers: [{id: "body", file: "body.png"}, {id: "arm", file: "arm.png"}]},
        perched: {layers: [{file: "perched.png"}]}
      },
      gestures: {
        perch: {pose: "perched"},
        tuck: {replaces: ["arm"]},
        hide: {fallback: "tuck"}
      }
    },
    bitmaps: new Map(["body.png", "arm.png", "perched.png"].map(file => [file, {file}]))
  };
  const rig = makeRig(pack, null, "");
  const gaze = {x: 0, y: 0};
  const draw = gesture => {
    const view = rig.view("stand", gesture, "idle", []);
    return [view.pose, Array.from(view.render(0, gaze).drawn, image => image.file)];
  };
  assert.deepEqual(draw(null), ["stand", ["body.png", "arm.png"]]);
  assert.deepEqual(draw("perch"), ["perched", ["perched.png"]]);
  assert.deepEqual(draw("tuck"), ["stand", ["body.png"]]);
  assert.deepEqual(draw("hide"), ["stand", ["body.png"]]);
});

test("companion scenarios evaluate guarded pack overrides and advance multi-beat steps", () => {
  const { scenarioRunner } = companion();
  const runner = scenarioRunner({
    startled_poke: { disabled: true },
    reading: {
      on: "user.poke", priority: 80, cooldownMs: 1000,
      when: {streak: 2, windowMs: 400, idleMs: 500, routeKind: "session", pose: "stand"},
      steps: [{ms: 200, pose: "reading"}, {ms: 100, expression: "smile"}]
    }
  });
  const state = {routeKind: "session", pose: "stand", idleMs: 600};
  runner.fire("user.poke", 100, {...state, routeKind: "home"});
  runner.fire("user.poke", 750, state);
  assert.equal(runner.tick(750), null);
  runner.fire("user.poke", 800, state);
  assert.equal(runner.tick(999).beat.pose, "reading");
  assert.equal(runner.tick(1000).beat.expression, "smile");
  assert.equal(runner.tick(1100), null);
  runner.fire("user.poke", 1200, state);
  runner.fire("user.poke", 1300, state);
  assert.equal(runner.tick(1300), null);
  runner.fire("user.poke", 1850, state);
  runner.fire("user.poke", 1900, state);
  assert.equal(runner.tick(1900).entry.id, "reading");
});

function pack(id, rev) { return {id, rev}; }
function manifest(id) {
  return {
    id, schema: "ws-companion/v1", canvas: [512, 512],
    poses: {stand: {layers: [{file: "a.png"}, {file: "b.png"}]}},
    props: {drone: {}, mail: {}, grab: {}, "prop-typing-desk": {}}
  };
}

test("companion retries all layers against one catalog revision and closes partial bitmaps", async () => {
  const calls = [], closed = [], activated = [];
  let catalogReads = 0;
  const { packLoader } = companion({
    fetch: async url => {
      calls.push(url);
      if (url === "/api/companion/packs?harness=example&model=work") {
        const rev = ++catalogReads === 1 ? "r1" : "r2";
        return {ok: true, json: async () => ({resolvedAuto: {packId: "rig"}, packs: [pack("rig", rev)]})};
      }
      if (url.includes("/pack.json?")) return {ok: true, json: async () => manifest("rig")};
      if (url.endsWith("b.png?rev=r1")) return {ok: false, status: 409};
      return {ok: true, blob: async () => url};
    },
    createImageBitmap: async url => ({url, width: 512, height: 512, close() { closed.push(url); }})
  });
  await new Promise((done, fail) => {
    const loader = packLoader(value => {
      activated.push(value);
      done();
    }, () => fail(new Error("unexpected pack failure")));
    loader.load("auto", {routeKind: "session", harness: "example", model: "work"});
    loader.load("auto", {routeKind: "session", harness: "example", model: "work"});
  });
  assert.equal(catalogReads, 2);
  assert.equal(activated.length, 1);
  assert.equal(activated[0].rev, "r2");
  assert.deepEqual([...activated[0].bitmaps.values()].map(layer => layer.url.slice(-2)), ["r2", "r2"]);
  assert.deepEqual(closed, ["/api/companion/packs/rig/assets/a.png?rev=r1"]);
  assert.ok(calls.includes("/api/companion/packs/rig/pack.json?rev=r2"));
});

test("companion retries removed packs twice, then resolves default without stale session hints", async () => {
  const catalogs = [];
  const { packLoader } = companion({
    fetch: async url => {
      if (url.startsWith("/api/companion/packs?") || url === "/api/companion/packs") {
        catalogs.push(url);
        return {ok: true, json: async () => ({resolvedAuto: {packId: "removed"}, packs: [pack("removed", "old"), pack("default", "new")]})};
      }
      if (url.includes("/removed/")) return {ok: false, status: 404};
      if (url.includes("/pack.json?")) return {ok: true, json: async () => manifest("default")};
      return {ok: true, blob: async () => url};
    },
    createImageBitmap: async () => ({width: 512, height: 512, close() {}})
  });
  const value = await new Promise((done, fail) => {
    packLoader(done, () => fail(new Error("unexpected fallback failure"))).load("auto", {routeKind: "session", harness: "example"});
  });
  assert.equal(value.id, "default");
  assert.deepEqual(catalogs, [...Array(3).fill("/api/companion/packs?harness=example"), "/api/companion/packs"]);
});

test("superseded companion loads discard decoded bitmaps after leaving a session", async () => {
  let releaseOld, signalOld;
  const stale = new Promise(done => { releaseOld = done; });
  const seen = [], closed = [];
  const { packLoader } = companion({
    fetch: async url => {
      if (url.startsWith("/api/companion/packs?")) return {ok: true, json: async () => ({resolvedAuto: {packId: "old"}, packs: [pack("old", "r1")]})};
      if (url === "/api/companion/packs") return {ok: true, json: async () => ({resolvedAuto: {packId: "default"}, packs: [pack("default", "r2")]})};
      if (url.includes("/pack.json?")) return {ok: true, json: async () => manifest(url.includes("/old/") ? "old" : "default")};
      return {ok: true, blob: async () => url};
    },
    createImageBitmap: async url => {
      if (url.includes("/old/")) {
        signalOld();
        await stale;
      }
      return {width: 512, height: 512, close() { closed.push(url); }};
    }
  });
  const ready = new Promise(done => { signalOld = done; });
  let loader;
  const mounted = new Promise((done, fail) => {
    loader = packLoader(value => { seen.push(value.id); done(); }, () => fail(new Error("unexpected pack failure")));
  });
  loader.load("auto", {routeKind: "session", model: "old"});
  await ready;
  loader.load("auto", {routeKind: "settings", model: "old"});
  await mounted;
  releaseOld();
  await new Promise(done => setImmediate(done));
  assert.deepEqual(seen, ["default"]);
  assert.equal(closed.filter(url => url.includes("/old/")).length, 2);
  loader.dispose();
});

function relayFixture() {
  let now = 10000, timer = 0, hidden = false, phase = "idle", children = [], rows = [], subdeep = 0, asks = [], permissions = [], treeAgents = [], completed = 0;
  const timers = new Map(), listeners = {}, posts = [], held = [];
  const doc = {
    body: {classList: {contains: () => false}},
    querySelector(selector) {
      if (selector.includes(".clive")) return {dataset: {phase, subdeep}};
      return null;
    },
    addEventListener(name, fn) { (listeners[name] ||= []).push(fn); }
  };
  const context = vm.createContext({
    doc, window: {WsShell: {companionContext: () => ({landmarks: {}})}, addEventListener() {}},
    AbortController, MutationObserver: class { observe() {} disconnect() {} }, ResizeObserver: class { observe() {} disconnect() {} },
    performance: {now: () => now}, Date: class extends Date { static now() { return now; } },
    setTimeout(fn) { timers.set(++timer, fn); return timer; }, clearTimeout(id) { timers.delete(id); },
    rid: "lead", lead: {}, boardLatest: null, TL_ASK: "ask", companionStreamReady: true,
    q: selector => selector.startsWith("#r-convlive .permwait") ? permissions : selector.startsWith("#r-tree .agent") ? treeAgents : [], boardQ: () => asks, region: () => ({}), hudReadData: () => ({roots: children, completed}),
    entriesBox: () => ({children: rows}), $: () => null, pageHidden: () => hidden,
    showRegions: () => { while (held.length) held.shift()(); }, shPost: (_, payload) => posts.push(payload),
    innerHeight: 1000, innerWidth: 1000
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", "dashboard-companion-relay.js"), "utf8") + "\nthis.relay = startCompanionRelay();", context);
  function flush() { const work = [...timers.values()]; timers.clear(); work.forEach(fn => fn()); }
  function update() { now += 300; listeners.change.forEach(fn => fn()); flush(); }
  function events() { return posts.splice(0).flatMap(p => p.events); }
  flush(); events();
  return {set children(value) { children = value; }, set treeAgents(value) { treeAgents = value; }, set completed(value) { completed = value; }, get rows() { return rows; }, set rows(value) { rows = value; }, set phase(value) { phase = value; }, set subdeep(value) { subdeep = value; }, set asks(value) { asks = value; }, set permissions(value) { permissions = value; },
    update, events, posts, now: () => now,
    input() { listeners.input.forEach(fn => fn({target: {matches: () => true}})); flush(); },
    visibility(value, swap) { hidden = value; if (swap) held.push(swap); listeners.visibilitychange.forEach(fn => fn()); flush(); },
    dispose: () => context.relay.dispose()};
}

function relayRow(key, classes, {childId, ms, cause, child, label} = {}) {
  const names = new Set(classes.split(" "));
  return {dataset: {entry: key, childId, wakeCause: cause},
    hasAttribute: () => false,
    matches(selector) {
      return selector.split(", ").some(part => {
        const negative = [...part.matchAll(/:not\(([^)]+)\)/g)].map(x => x[1]);
        part = part.replace(/:not\([^)]+\)/g, "");
        const matches = s => [...s.matchAll(/\.([\w-]+)/g)].every(x => names.has(x[1])) &&
          (!s.startsWith("article") || names.has("article")) && (!s.includes("[data-board-nudge]") || names.has("board-nudge")) && (!s.includes("[data-mail-wake]") || names.has("mail-wake"));
        return matches(part) && !negative.some(matches);
      });
    },
    querySelector(selector) {
      if (selector === "time[data-ms]") return ms ? {dataset: {ms}} : null;
      if (selector === ".mpeer") return label ? {textContent: label} : null;
      if (selector.startsWith(":scope > details.mailline.")) return child === selector.split(".").pop() ? {} : null;
      return null;
    }};
}

// relayAgent is an ended-group row of the expanded tree; native marks the
// harness's own subagent, unknown a native row whose observation ended with
// the outcome unknown.
function relayAgent(id, {unknown = false, native = false} = {}) {
  const details = {dataset: {key: "agent-" + id}, querySelector: () => null};
  return {
    closest: selector => selector === ".tgroup.ended" ? {} : null,
    querySelector: selector => selector === ":scope > details[data-key]" ? details : null,
    classList: {contains: name => name === "done" || (native && name === "native")},
    matches: () => true,
    hasAttribute: name => unknown && name === "data-end-unknown"
  };
}

test("relay emits no subagent.done for a native row whose observation ended", () => {
  const f = relayFixture(), done = () => f.events().filter(e => e.kind === "subagent.done");
  f.children = [{id: "agy-native:call_1", state: "thinking", native: true}]; f.update(); f.events();
  f.children = []; f.treeAgents = [relayAgent("agy-native:call_1", {unknown: true, native: true})]; f.update();
  assert.equal(done().length, 0);
  // A native row that ended with a known end is read as done.
  f.children = [{id: "codex-native:call_2", state: "thinking", native: true}]; f.treeAgents = []; f.update(); f.events();
  f.children = []; f.treeAgents = [relayAgent("codex-native:call_2", {native: true})]; f.update();
  assert.equal(done().length, 1);
  f.dispose();
});

test("relay reads no finish from evicted native rows or a skipped transition", () => {
  const f = relayFixture(), done = () => f.events().filter(e => e.kind === "subagent.done").map(e => e.token);
  const agy = i => "agy-native:call_" + i;
  f.children = [0, 1, 2, 3, 4, 5].map(i => ({id: agy(i), state: "thinking", native: true})).concat([{id: "w", state: "thinking"}]);
  f.update(); f.events();
  // Six observations end with the outcome unknown; the finished-row cap
  // evicts call_0 before the page sees it. One ordinary child really
  // finishes and its fold raises the completed count by one.
  f.children = []; f.completed = 1;
  f.treeAgents = [1, 2, 3, 4, 5].map(i => relayAgent(agy(i), {unknown: true, native: true})).concat([relayAgent("w")]);
  f.update();
  assert.deepEqual(done(), ["subagent.done:lead:w:1"]);
  // A native row whose done transition the DOM skipped (running, then gone)
  // spends no later rise either.
  f.children = [{id: "codex-native:call_9", state: "thinking", native: true}, {id: "v", state: "thinking"}]; f.treeAgents = []; f.update(); f.events();
  f.children = [{id: "v", state: "thinking"}]; f.completed = 2; f.update();
  assert.deepEqual(done(), []);
  f.dispose();
});

test("relay reconciles both report orders, holding, resume, terminal and stale reports", () => {
  const f = relayFixture(), child = state => [{id: "c", state}];
  f.children = child("holding"); f.update(); assert.equal(f.events().filter(e => e.kind === "subagent.spawn").length, 0);
  f.children = child("thinking"); f.update(); assert.equal(f.events().filter(e => e.kind === "subagent.spawn").length, 1);
  const first = f.now();
  f.rows = [relayRow("01", "mailline recv child-report", {childId: "c", ms: first})]; f.update();
  assert.equal(f.events().filter(e => e.kind === "subagent.done").length, 1);
  f.children = child("holding"); f.update(); assert.equal(f.events().filter(e => e.kind === "subagent.done").length, 0);
  f.children = child("thinking"); f.update(); f.events();
  f.rows = [...f.rows, relayRow("02", "mailline recv child-report", {childId: "c", ms: first})]; f.update();
  assert.equal(f.events().filter(e => e.kind === "subagent.done").length, 0);
  f.children = child("holding"); f.update(); f.events();
  f.rows = [...f.rows, relayRow("03", "mailline recv child-report", {childId: "c", ms: f.now()})]; f.update();
  assert.equal(f.events().filter(e => e.kind === "subagent.done").length, 1);
  f.children = child("closed"); f.update(); assert.equal(f.events().filter(e => e.kind === "subagent.done").length, 0);
  f.children = child("thinking"); f.update(); f.events(); f.children = child("closed"); f.update();
  assert.equal(f.events().filter(e => e.kind === "subagent.done").length, 1);
  f.dispose();
});

test("relay attributes delayed reports to the prior turn without consuming the resumed turn", () => {
  const f = relayFixture(), child = (state, start = 0) => [{id: "c", state, activeSince: start, elapsedMs: 100}];
  const done = () => f.events().filter(e => e.kind === "subagent.done").map(e => e.token);
  f.children = child("thinking"); f.update(); done();
  f.children = child("holding"); f.update(); assert.deepEqual(done(), []);
  const priorStamp = f.now();
  const resumedStamp = priorStamp + 80;
  f.children = child("thinking", resumedStamp - 100); f.update(); done();
  f.rows = [relayRow("01", "mailline recv child-report", {childId: "c"})]; f.update();
  assert.deepEqual(done(), []);
  f.rows.push(relayRow("02", "mailline recv child-report", {childId: "c", ms: priorStamp})); f.update();
  assert.deepEqual(done(), ["subagent.done:lead:c:1"]);
  f.rows.push(relayRow("03", "mailline recv child-report", {childId: "c", ms: priorStamp + 1})); f.update();
  assert.deepEqual(done(), []);
  f.rows.push(relayRow("04", "mailline recv child-report", {childId: "c", ms: resumedStamp + 20})); f.update();
  assert.deepEqual(done(), ["subagent.done:lead:c:2"]);
  f.children = child("holding"); f.update();
  f.children = child("closed"); f.update();
  assert.deepEqual(done(), []);
  f.dispose();
});

test("relay preserves legacy streaming edges, phase permission and typing during thinking", () => {
  const f = relayFixture();
  f.phase = "tool"; f.update(); let events = f.events();
  assert.equal(events.filter(e => e.kind === "session.streaming").length, 1);
  assert.equal(events.filter(e => e.kind === "session.tool").length, 1);
  f.phase = "replying"; f.update(); events = f.events();
  assert.equal(events.filter(e => e.kind === "session.streaming").length, 0);
  assert.equal(events.filter(e => e.kind === "session.replying").length, 1);
  f.phase = "permission"; f.update(); events = f.events();
  assert.equal(events.find(e => e.kind === "ask.open").token, "perm:lead");
  f.phase = "thinking"; f.update(); f.events(); f.input();
  assert.equal(f.posts.at(-1).activity, "typing_user");
  f.dispose();
});

test("relay classifies peer mail and compaction without nudge or background-exit ghosts", () => {
  const f = relayFixture();
  f.rows = [relayRow("01", "mail-notification board-nudge"), relayRow("02", "mail-notification mail-wake"),
    relayRow("03", "mail-notification"), relayRow("04", "article mail-block", {child: "recv"}),
    relayRow("05", "article mail-block", {child: "sent"}), relayRow("06", "mailline recv agent-card"),
    relayRow("07", "mailline recv agent-note", {childId: "x", cause: "background_exit"}),
    relayRow("08", "sysline compaction"), relayRow("09", "sysline compaction fail")];
  f.update(); const kinds = f.events().map(e => e.kind);
  assert.equal(kinds.filter(k => k === "mail.recv").length, 2);
  assert.equal(kinds.filter(k => k === "mail.send").length, 1);
  assert.equal(kinds.filter(k => k === "session.compacted").length, 1);
  assert.equal(kinds.filter(k => k === "subagent.done").length, 0);
  f.dispose();
});

test("relay applies held regions before visibility baseline", () => {
  const f = relayFixture(); f.visibility(true);
  f.visibility(false, () => { f.rows = [relayRow("01", "mailline recv")]; });
  assert.equal(f.events().filter(e => e.kind === "mail.recv").length, 0);
  f.rows = [relayRow("01", "mailline recv"), relayRow("02", "mailline recv")]; f.update();
  assert.equal(f.events().filter(e => e.kind === "mail.recv").length, 1);
  f.dispose();
});


test("relay suppresses ambiguous reports and tombstones a short-lived child", () => {
  const f = relayFixture();
  f.children = [{id: "a", label: "shared", state: "holding"}, {id: "b", label: "shared", state: "holding"}];
  f.update(); f.events();
  f.rows = [relayRow("01", "mailline recv child-report", {label: "shared"}),
    relayRow("02", "mailline recv child-report", {childId: "short"}),
    relayRow("03", "mailline recv child-report", {childId: "short"})];
  f.update();
  const done = f.events().filter(e => e.kind === "subagent.done");
  assert.equal(done.length, 1);
  assert.ok(done[0].token.endsWith("short:1"));
  f.dispose();
});


test("relay emits canonical whiteboard and child approval tokens", () => {
  const f = relayFixture();
  f.asks = [{id: "item-42", getAttribute: () => "42", getBoundingClientRect: () => ({width: 0})}];
  f.permissions = [{closest(selector) {
    if (selector === ".ag-source") return null;
    return {querySelector: () => ({dataset: {replyId: "child"}})};
  }, querySelector: () => null}];
  f.update();
  const asks = f.events().filter(e => e.kind === "ask.open");
  assert.deepEqual(asks.map(e => e.token).sort(), ["ask:lead:42", "perm:child"]);
  assert.equal(asks.find(e => e.token === "perm:child").rid, "child");
  f.dispose();
});

test("companion resize persists completed pointer and keyboard actions, clamps and resets", () => {
  const data = new Map(), listeners = {}, attrs = {}, captures = [];
  const storage = { getItem: key => data.get(key) ?? null, setItem: (key, value) => data.set(key, value), removeItem: key => data.delete(key) };
  const C = companion();
  // The helper closes over its own window, so load it with a storage-backed window.
  const context = vm.createContext({window: {WsCompanion: C, localStorage: storage}});
  vm.runInContext(fs.readFileSync(path.join(__dirname, "assets/dashboard-companion-stage.js"), "utf8"), context);
  const grip = {addEventListener: (name, fn) => { listeners[name] = fn; }, setAttribute: (name, value) => { attrs[name] = value; }, setPointerCapture: id => captures.push(id)};
  let size = 116, changes = 0;
  const control = C.resizeControl(grip, {}, () => size, () => { changes++; size = control.value(116, 1600, 900); });
  assert.equal(control.value(116, 1600, 900), 116);
  const event = (id, x, y) => ({pointerId: id, clientX: x, clientY: y, button: 0, preventDefault() {}});
  listeners.pointerdown(event(7, 100, 100));
  listeners.pointermove(event(7, 300, 100));
  assert.equal(size, 240);
  assert.equal(data.size, 0);
  assert.deepEqual(captures, [7]);
  listeners.pointerup(event(7, 300, 100));
  assert.equal(data.get("ws.companion.size"), "240");
  listeners.keydown({key: "ArrowDown", preventDefault() {}});
  assert.equal(data.get("ws.companion.size"), "236");
  const restoredGrip = {addEventListener() {}, setAttribute() {}};
  const restored = C.resizeControl(restoredGrip, {}, () => size, () => {});
  assert.equal(restored.value(116, 1600, 900), 236);
  assert.equal(control.value(116, 150, 800), 126);
  assert.equal(attrs["aria-valuemax"], "126");
  assert.equal(control.value(116, 1600, 900), 236);
  listeners.pointerdown(event(8, 100, 100));
  listeners.pointermove(event(8, -1000, 100));
  listeners.pointerup(event(8, -1000, 100));
  assert.equal(data.get("ws.companion.size"), "64");
  control.reset();
  assert.equal(data.size, 0);
  assert.equal(size, 116);
  assert.ok(changes >= 4);
});

test("companion stage creates an FX canvas for semantic-split FX and falls back to spans for non-drawable FX", async () => {
  let raf = null, rafId = 0, now = 0;
  function makeElement(tag) {
    const el = {
      tagName: tag.toUpperCase(), className: "", children: [], childNodes: [], dataset: {},
      style: { setProperty() {}, removeProperty() {} }, hidden: false, textContent: "",
      setAttribute() {}, removeAttribute() {}, addEventListener() {}, setPointerCapture() {}, remove() {},
      append(...items) {
        for (const item of items) {
          this.childNodes.push(item);
          if (item && typeof item === "object") this.children.push(item);
        }
      },
      insertBefore(item) { this.append(item); },
      replaceChildren(...items) { this.children.length = 0; this.childNodes.length = 0; this.append(...items); }
    };
    if (tag === "canvas") {
      el.width = 0;
      el.height = 0;
      el.drawn = [];
      const ctx = {
        canvas: el, save() {}, restore() {}, clearRect() {}, setTransform() {}, translate() {}, rotate() {}, scale() {},
        fillRect() {}, getImageData: () => ({ data: [] }),
        drawImage(image) { el.drawn.push(image); }
      };
      el.getContext = () => ctx;
    }
    return el;
  }
  class Canvas {
    constructor(width, height) { this.width = width; this.height = height; this.drawn = []; }
    getContext() {
      return {
        canvas: this, save() {}, restore() {}, clearRect() {}, setTransform() {}, translate() {}, rotate() {}, scale() {},
        fillRect() {}, getImageData: () => ({ data: [] }),
        drawImage: image => { this.drawn.push(image); }
      };
    }
  }
  const body = makeElement("body");
  const manifestData = {
    id: "default", schema: "ws-companion/v1", canvas: [128, 128],
    poses: { stand: { layers: [{ file: "body.png" }] } },
    fx: {
      semantic: { kind: "float", line: "fx-line.png", accentFill: "fx-fill.png" },
      badge: { kind: "badge", text: "!", color: "#e2582c" },
      sparkles: { kind: "particles", glyphs: ["✦", "✧"] },
      empty_layers: { kind: "badge", text: "?", layers: [] }
    },
    scenarios: {
      semantic_fx: { on: "mail.recv", priority: 95, steps: [{ ms: 40, fx: "semantic" }] },
      badge_fx: { on: "mail.send", priority: 95, steps: [{ ms: 40, fx: "badge" }] },
      particles_fx: { on: "session.compacted", priority: 95, steps: [{ ms: 40, fx: "sparkles" }] },
      empty_fx: { on: "session.error", priority: 95, steps: [{ ms: 40, fx: "empty_layers" }] }
    }
  };
  const doc = {
    documentElement: { dataset: { rid: "" }, classList: { contains: () => false } },
    body, hidden: false,
    getElementById: () => null,
    createElement: makeElement,
    createElementNS: (ns, tag) => makeElement(tag),
    addEventListener() {}
  };
  const win = {
    WsCompanion: {},
    WsShell: { companionContext: routeKind => ({ routeKind: routeKind || "session", activity: "idle", landmarks: {}, events: [] }) },
    addEventListener() {}, innerWidth: 1200, innerHeight: 800, devicePixelRatio: 1
  };
  const context = vm.createContext({
    window: win, document: doc, URLSearchParams, AbortController, OffscreenCanvas: Canvas,
    ResizeObserver: class { observe() {} disconnect() {} },
    MutationObserver: class { observe() {} disconnect() {} },
    performance: { now: () => now },
    wsT: key => key,
    requestAnimationFrame: fn => { raf = fn; return ++rafId; },
    cancelAnimationFrame: () => { raf = null; },
    fetch: async url => {
      if (url.startsWith("/api/companion/packs?")) return { ok: true, json: async () => ({ resolvedAuto: { packId: "default" }, packs: [{ id: "default", rev: "r1" }] }) };
      if (url === "/api/companion/packs") return { ok: true, json: async () => ({ resolvedAuto: { packId: "default" }, packs: [{ id: "default", rev: "r1" }] }) };
      if (url.includes("/pack.json?")) return { ok: true, json: async () => manifestData };
      return { ok: true, blob: async () => url };
    },
    createImageBitmap: async url => ({ id: path.basename(url.split("?")[0]), width: 128, height: 128, close() {} })
  });
  for (const name of ["builtins", "packs", "focus", "deform", "compositor", "scenarios", "menu", "reflex", "quip", "vrm-fx", "vrm-studio", "vrm-props", "vrm-pose", "vrm-colead", "vrm-speech", "vrm", "stage"]) {
    vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", `dashboard-companion-${name}.js`), "utf8"), context);
  }
  const C = context.window.WsCompanion;
  C.quickMenu = () => ({ rebuild() {}, measure() {}, place() {}, close() {} });

  // Compositor unit checks: resolved semantic layers are drawable; missing bitmaps are not.
  const rig = C.makeRig({
    manifest: {
      canvas: [128, 128],
      fx: {
        semantic: { line: "fx-line.png", accentFill: "fx-fill.png" },
        missingFile: { file: "missing.png" },
        missingFrame: { frames: ["missing.png"] },
        textOnly: { kind: "badge", text: "!" }
      }
    },
    bitmaps: new Map([["fx-line.png", { id: "fx-line.png" }], ["fx-fill.png", { id: "fx-fill.png" }]])
  }, null, "");
  assert.equal(rig.prop("semantic", "fx").drawable, true);
  assert.equal(rig.prop("missingFile", "fx").drawable, false);
  assert.equal(rig.prop("missingFrame", "fx").drawable, false);
  assert.equal(rig.prop("textOnly", "fx").drawable, false);

  const stage = C.mount({ pack: "default", preset: "" }, () => {});
  await new Promise(resolve => setImmediate(resolve));
  const root = body.children[0];
  const step = (dt = 16) => {
    const fn = raf;
    raf = null;
    assert.ok(fn, "expected scheduled animation frame");
    now += dt;
    fn(now);
  };
  const fxNode = () => root.children.find(child => child.className === "ws-companion-fx");
  const trigger = (activity, eventKind) => {
    stage.context({
      routeKind: "session", activity, landmarks: {}, sessionId: "s1",
      events: eventKind ? [{ kind: eventKind, token: eventKind + ":" + now }] : []
    }, { left: 0, top: 0, width: 800, height: 600 });
    step();
  };

  trigger("idle", "mail.recv");
  const semanticFx = fxNode();
  assert.ok(semanticFx && !semanticFx.hidden);
  assert.equal(semanticFx.children.length, 1);
  assert.equal(semanticFx.children[0].tagName, "CANVAS");
  assert.equal(semanticFx.children[0].drawn.length, 2);
  assert.equal(semanticFx.children[0].drawn[1].id, "fx-line.png");

  step(50);
  trigger("thinking", null);
  assert.equal(fxNode().children.map(el => el.tagName + ":" + el.textContent).join(","), "SPAN:…");

  trigger("idle", "mail.send");
  assert.equal(fxNode().children.map(el => el.tagName + ":" + el.textContent).join(","), "SPAN:!");

  step(50);
  trigger("idle", "session.compacted");
  assert.equal(fxNode().children.map(el => el.tagName + ":" + el.textContent).join(","), "SPAN:✦,SPAN:✧");

  step(50);
  trigger("idle", "session.error");
  assert.equal(fxNode().children.map(el => el.tagName + ":" + el.textContent).join(","), "SPAN:?");

  // Directed commands: only the shown session's, quips as text, TTL expiry.
  step(50);
  const quipNode = () => root.children.find(child => child.className === "ws-companion-quip");
  assert.equal(stage.command({ sessionId: "s1", quip: "too early", ttl_ms: 1000 }), false, "no shown session yet");
  stage.reset("s1");
  step();
  assert.equal(stage.command({ sessionId: "other", quip: "not mine", ttl_ms: 1000 }), false);
  assert.equal(stage.command({ sessionId: "s1", fx: "unknown", quip: "   " }), false, "nothing valid to show");
  assert.equal(stage.command({ sessionId: "s1", fx: "badge", gesture: "unknown", quip: "<img src=x onerror=alert(1)>", ttl_ms: 1000 }), true);
  step();
  assert.equal(root.dataset.scenario, "directed");
  assert.equal(quipNode().textContent, "<img src=x onerror=alert(1)>", "the quip is set as text");
  assert.equal(quipNode().innerHTML, undefined, "the quip never goes through innerHTML");
  assert.equal(quipNode().hidden, false);
  assert.equal(fxNode().children.map(el => el.tagName + ":" + el.textContent).join(","), "SPAN:!");
  for (let i = 0; i < 22; i++) step(50);
  assert.equal(quipNode().hidden, true, "the quip dismisses when the TTL ends");
  assert.equal(root.dataset.scenario, "", "the rules engine resumes");

  stage.dispose();
});

test("companion commands validate IDs against the pack, its fallback chains, shared props and built-in fx", () => {
  const { directedCommand, scenarioRunner } = companion();
  const runner = scenarioRunner();
  const pack = { manifest: {
    expressions: { happy: { decal: "happy.png" }, grin: { fallback: "happy" }, ghost: { fallback: "missing" } },
    gestures: { wave: { pose: "stand" } }, props: {}, fx: {}
  } };
  const shared = { manifest: { props: { drone: { file: "drone.png" } }, fx: {} } };
  const long = "<b>hi</b>\nthere " + "x".repeat(200);
  const full = directedCommand({ sessionId: "s1", scenario: "celebrate_done", expression: "grin", gesture: "nope",
    prop: "drone", fx: "sparkle", quip: long, ttl_ms: 60000 }, pack, shared, runner);
  assert.equal(full.id, "celebrate_done");
  assert.equal(full.ttl, 15000, "TTL clamps to 15 s");
  assert.equal(full.beats.length, 1);
  const beat = full.beats[0];
  assert.equal(beat.expression, "happy", "a fallback chain resolves to its usable entry");
  assert.equal(beat.gesture, "wave", "an unknown gesture drops; the scenario's own stays");
  assert.equal(beat.prop, "drone", "props may come from the shared default pack");
  assert.equal(beat.fx, "sparkle", "built-in fx badges are known");
  assert.equal(Array.from(beat.quip).length, 140, "quips cut to 140 code points");
  assert.ok(beat.quip.startsWith("<b>hi</b> there"), "control characters fold to spaces; markup stays literal text");

  const plain = directedCommand({ sessionId: "s1", expression: "ghost", gesture: "wave", ttl_ms: 10 }, pack, shared, runner);
  assert.equal(plain.id, "directed");
  assert.equal(plain.ttl, 1000, "TTL clamps to 1 s");
  assert.deepEqual(JSON.parse(JSON.stringify(plain.beats)),
    [{ pose: "stand", expression: "idle", gaze: "cursor", ms: 1000, gesture: "wave" }],
    "a fallback chain ending nowhere drops the field");
  assert.equal(directedCommand({ sessionId: "s1", scenario: "no_such", expression: "Bad ID!", fx: "nope", quip: " \n " },
    pack, shared, runner), null, "a command with nothing valid is dropped");
  assert.equal(directedCommand({ sessionId: "s1", prop: "drone" }, pack, null, runner), null, "no shared pack, no shared props");
  assert.equal(directedCommand("not a command", pack, shared, runner), null);
});

test("companion directed beats override the rules engine until the TTL, then the rules resume", () => {
  const { scenarioRunner } = companion();
  const runner = scenarioRunner(), guards = { routeKind: "session", pose: "stand", idleMs: 0 };
  runner.direct([{ pose: "stand", expression: "happy", ms: 400 }, { pose: "stand", expression: "oo", ms: 300 }], 0, 2000);
  assert.equal(runner.tick(100).entry.id, "directed");
  runner.fire("user.poke", 150, guards);
  runner.fire("ask.open", 160, guards);
  assert.equal(runner.tick(200).beat.expression, "happy", "rules scenarios do not replace a directed beat");
  assert.equal(runner.tick(500).beat.expression, "oo", "directed steps advance");
  assert.equal(runner.tick(1900).beat.expression, "oo", "the last step holds until the TTL");
  runner.endReply();
  assert.ok(runner.tick(1950), "a reply ending leaves a directed beat alone");
  assert.equal(runner.tick(2000), null, "the TTL ends the directed beat");
  runner.fire("user.poke", 2100, guards);
  assert.equal(runner.tick(2150).entry.id, "startled_poke", "the rules engine resumes");

  runner.direct({ expression: "happy", ms: 5000 }, 3000, 10000);
  runner.direct({ expression: "cry", ms: 5000 }, 3500, 1000);
  assert.equal(runner.tick(3600).beat.expression, "cry", "a newer command replaces an older one");
  assert.equal(runner.tick(4500), null, "with its own TTL");
  runner.direct({ expression: "happy", ms: 5000 }, 5000, 5000);
  runner.fire("user.drag", 5100, guards);
  assert.equal(runner.tick(5200, true).entry.id, "drag_dangle", "dragging still dangles the character");
  runner.direct({ expression: "happy" }, 6000, Number.NaN);
  assert.equal(runner.tick(6999).entry.id, "directed");
  assert.equal(runner.tick(7000), null, "a missing TTL clamps to 1 s");
});

test("companion gear and quick menu delegate to openSettings without creating a floating modal", async () => {
  let raf = null, now = 0;
  const openedBy = [];
  const makeElement = tag => {
    const el = {
      tagName: tag.toUpperCase(), className: "", id: "", title: "", children: [], dataset: {}, attrs: {}, listeners: {}, value: "", disabled: false,
      style: { setProperty() {}, removeProperty() {} }, hidden: false, textContent: "", offsetWidth: 240, offsetHeight: 200,
      setAttribute(k, v) { this.attrs[k] = String(v); }, getAttribute(k) { return this.attrs[k] ?? null; }, removeAttribute() {},
      addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }, setPointerCapture() {}, remove() {}, focus() {},
      getBoundingClientRect: () => ({ left: 0, right: 60, top: 0, bottom: 80, width: 60, height: 80 }),
      append(...items) { this.children.push(...items.filter(item => item && typeof item === "object")); },
      insertBefore(item) { this.append(item); },
      replaceChildren(...items) { this.children = []; this.append(...items); },
      all() { return this.children.flatMap(child => [child, ...(child.all ? child.all() : [])]); },
      contains(other) { return other === this || this.all().includes(other); },
      querySelector(sel) {
        if (sel === "button") return this.all().find(c => c.tagName === "BUTTON") || null;
        return null;
      }
    };
    if (tag === "canvas") {
      el.width = 0; el.height = 0; el.drawn = [];
      const ctx = { canvas: el, save() {}, restore() {}, clearRect() {}, setTransform() {}, translate() {}, rotate() {}, scale() {},
        fillRect() {}, getImageData: () => ({ data: [] }), drawImage(image) { el.drawn.push(image); } };
      el.getContext = () => ctx;
    }
    return el;
  };
  class Canvas {
    constructor(width, height) { this.width = width; this.height = height; this.drawn = []; }
    getContext() {
      return { canvas: this, save() {}, restore() {}, translate() {}, rotate() {}, scale() {}, fillRect() {}, getImageData: () => ({ data: [] }),
        clearRect: () => { this.drawn.length = 0; }, drawImage: image => { this.drawn.push(image); } };
    }
  }
  const body = makeElement("body");
  const doc = {
    documentElement: { dataset: { rid: "" }, classList: { contains: () => false }, hasAttribute: name => name === "data-sysopen" },
    body, hidden: false, activeElement: null, getElementById: () => null,
    createElement: makeElement, createElementNS: (ns, tag) => makeElement(tag), addEventListener() {}
  };
  const win = {
    WsCompanion: {},
    WsShell: { companionContext: routeKind => ({ routeKind: routeKind || "session", activity: "idle", landmarks: {}, events: [] }) },
    addEventListener() {}, innerWidth: 1200, innerHeight: 800, devicePixelRatio: 1
  };
  const manifestData = { id: "default", schema: "ws-companion/v1", canvas: [128, 128], poses: { stand: { layers: [{ file: "stand.png" }] } } };
  const packs = { resolvedAuto: { packId: "default" }, packs: [{ id: "default", rev: "r1" }] };
  const C = companion({
    window: win, document: doc, OffscreenCanvas: Canvas,
    ResizeObserver: class { observe() {} disconnect() {} }, MutationObserver: class { observe() {} disconnect() {} },
    performance: { now: () => now }, wsT: key => key,
    requestAnimationFrame: fn => { raf = fn; return 1; }, cancelAnimationFrame: () => { raf = null; },
    fetch: async url => {
      if (url.includes("/pack.json?")) return { ok: true, json: async () => manifestData };
      if (url === "/api/companion/packs" || url.startsWith("/api/companion/packs?")) return { ok: true, json: async () => packs };
      return { ok: true, blob: async () => url };
    },
    createImageBitmap: async url => ({ id: path.basename(url.split("?")[0]), width: 128, height: 128, close() {} })
  });
  assert.equal(C.settingsModal, undefined, "retired floating settings modal is no longer registered");

  const stage = C.mount({ pack: "default", preset: "" }, () => {}, opener => openedBy.push(opener));
  await new Promise(resolve => setImmediate(resolve));
  const node = body.children[0];
  const hit = node.children.find(c => c.className === "ws-companion-hit");
  const menu = node.children.find(c => c.className === "ws-companion-menu");
  const character = hit.children.find(c => c.className === "ws-companion-character");
  const gear = hit.children.find(c => c.className === "ws-companion-gear");
  assert.equal(node.all().filter(c => c.tagName === "DIALOG").length, 0, "stage mounts no floating <dialog>");
  assert.equal(gear.hidden, false);

  // Clicking the gear closes any open quick menu and routes to openSettings(gear).
  menu.hidden = false;
  for (const fn of gear.listeners.click) fn({});
  assert.equal(menu.hidden, true, "gear click closes the quick menu");
  assert.deepEqual(openedBy, [gear]);

  // Opening the quick menu includes the Settings action, which closes the menu and routes to openSettings(character).
  for (const fn of character.listeners.contextmenu || []) fn({ preventDefault() {} });
  assert.equal(menu.hidden, false);
  const settingsBtn = menu.children.find(c => c.tagName === "BUTTON" && c.textContent === "js.companion.settings");
  assert.ok(settingsBtn, "quick menu includes a Settings button");
  for (const fn of settingsBtn.listeners.click) fn({});
  assert.equal(menu.hidden, true);
  assert.deepEqual(openedBy, [gear, character]);

  stage.dispose();

  // Without an openSettings callback, the gear button stays hidden.
  body.children = [];
  const stageNoSettings = C.mount({ pack: "default", preset: "" }, () => {});
  const hiddenGear = body.children[0].all().find(c => c.className === "ws-companion-gear");
  assert.equal(hiddenGear.hidden, true);
  stageNoSettings.dispose();
});

test("companion live config broadcast re-dresses the mounted stage without a reload", async () => {
  let raf = null, now = 0;
  const packFetches = [], openedBy = [];
  const makeElement = tag => {
    const el = {
      tagName: tag.toUpperCase(), className: "", id: "", children: [], dataset: {}, attrs: {}, listeners: {}, value: "", disabled: false,
      style: { setProperty() {}, removeProperty() {} }, hidden: false, textContent: "", open: false, offsetWidth: 240, offsetHeight: 200,
      setAttribute(k, v) { this.attrs[k] = String(v); }, getAttribute(k) { return this.attrs[k] ?? null; }, removeAttribute() {},
      addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }, setPointerCapture() {}, remove() {}, focus() {},
      getBoundingClientRect: () => ({ left: 0, right: 60, top: 0, bottom: 80, width: 60, height: 80 }),
      append(...items) { this.children.push(...items.filter(item => item && typeof item === "object")); },
      insertBefore(item) { this.append(item); },
      replaceChildren(...items) { this.children = []; this.append(...items); },
      all() { return this.children.flatMap(child => [child, ...(child.all ? child.all() : [])]); },
      contains(other) { return other === this || this.all().includes(other); },
      querySelector() { return this.all().find(c => ["SELECT", "INPUT", "BUTTON"].includes(c.tagName) && !c.disabled) || null; }
    };
    if (tag === "canvas") {
      el.width = 0; el.height = 0; el.drawn = [];
      const ctx = { canvas: el, save() {}, restore() {}, clearRect() {}, setTransform() {}, translate() {}, rotate() {}, scale() {},
        fillRect() {}, getImageData: () => ({ data: [] }), drawImage(image) { el.drawn.push(image); } };
      el.getContext = () => ctx;
    }
    return el;
  };
  class Canvas {
    constructor(width, height) { this.width = width; this.height = height; this.drawn = []; }
    getContext() {
      return { canvas: this, save() {}, restore() {}, translate() {}, rotate() {}, scale() {}, fillRect() {}, getImageData: () => ({ data: [] }),
        clearRect: () => { this.drawn.length = 0; }, drawImage: image => { this.drawn.push(image); } };
    }
  }
  class Option { constructor(text, value, _default, selected) { this.tagName = "OPTION"; this.textContent = text; this.value = value; this.selected = selected; } }
  const body = makeElement("body"), activeRid = "";
  const doc = {
    documentElement: { dataset: { rid: activeRid }, classList: { contains: () => false }, hasAttribute: name => name === "data-sysopen" },
    body, hidden: false, activeElement: null, getElementById: () => null,
    createElement: makeElement, createElementNS: (ns, tag) => makeElement(tag), addEventListener() {}
  };
  const win = {
    WsCompanion: {},
    WsShell: { companionContext: routeKind => ({ routeKind: routeKind || "session", activity: "idle", landmarks: {}, events: [] }) },
    addEventListener() {}, innerWidth: 1200, innerHeight: 800, devicePixelRatio: 1
  };
  const manifestData = {
    id: "default", schema: "ws-companion/v1", canvas: [128, 128],
    poses: { stand: { layers: [{ file: "legacy.png" }] } },
    defaultOutfit: "street",
    outfits: {
      street: { poses: { stand: { layers: [{ file: "street-stand.png" }] } } },
      gown: { poses: { stand: { layers: [{ file: "gown-stand.png" }] } } }
    }
  };
  const packs = { resolvedAuto: { packId: "default" }, packs: [{ id: "default", rev: "r1" }] };
  const context = vm.createContext({
    window: win, document: doc, URLSearchParams, AbortController, OffscreenCanvas: Canvas, Option,
    ResizeObserver: class { observe() {} disconnect() {} }, MutationObserver: class { observe() {} disconnect() {} },
    performance: { now: () => now }, wsT: key => key,
    requestAnimationFrame: fn => { raf = fn; return 1; }, cancelAnimationFrame: () => { raf = null; },
    fetch: async url => {
      if (url.includes("/pack.json?")) { packFetches.push(url); return { ok: true, json: async () => manifestData }; }
      if (url === "/api/companion/packs" || url.startsWith("/api/companion/packs?")) return { ok: true, json: async () => packs };
      return { ok: true, blob: async () => url };
    },
    createImageBitmap: async url => ({ id: path.basename(url.split("?")[0]), width: 128, height: 128, close() {} })
  });
  for (const name of ["builtins", "packs", "focus", "deform", "compositor", "scenarios", "menu", "reflex", "quip", "vrm-fx", "vrm-studio", "vrm-props", "vrm-pose", "vrm-colead", "vrm-speech", "vrm", "stage"]) {
    vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", `dashboard-companion-${name}.js`), "utf8"), context);
  }
  const C = win.WsCompanion;
  C.quickMenu = () => ({ rebuild() {}, measure() {}, place() {}, close() {} });
  const config = { pack: "default", preset: "", outfit: "", hair: "", eyes: "", attachments: "", palette: "" };

  const stage = C.mount(config, () => {}, opener => openedBy.push(opener));
  await new Promise(resolve => setImmediate(resolve));
  const node = body.children[0], hit = node.children.find(c => c.className === "ws-companion-hit");
  const step = () => { const fn = raf; raf = null; assert.ok(fn, "expected scheduled animation frame"); now += 16; fn(now); };
  // files lists the layer files the character canvas received on its latest draw.
  const files = item => item.id ? [item.id] : (item.drawn || []).flatMap(files);
  const characterCanvas = () => node.all().find(c => c.tagName === "CANVAS");
  const drawn = () => { const canvas = characterCanvas(); canvas.drawn.length = 0; step(); return canvas.drawn.flatMap(files); };

  assert.equal(packFetches.length, 1);
  assert.deepEqual(drawn().filter(f => /-stand\.png$/.test(f)), ["street-stand.png"], "the pack default outfit renders first");

  const gear = hit.children.find(c => c.className === "ws-companion-gear");
  for (const fn of gear.listeners.click) fn({});
  assert.deepEqual(openedBy, [gear], "gear routes to Dashboard Settings");

  stage.apply({ ...config, outfit: "gown" });
  assert.deepEqual(drawn().filter(f => /-stand\.png$/.test(f)), ["gown-stand.png"], "the next frame draws the new outfit with no stale stack");
  assert.equal(packFetches.length, 1, "no pack is fetched again");

  stage.apply({ ...config, outfit: "gown", palette: "skin=#112233" });
  assert.equal(packFetches.length, 1, "a palette broadcast does not reload the pack either");
  assert.deepEqual(drawn().filter(f => /-stand\.png$/.test(f)), ["gown-stand.png"]);
  stage.apply(config);
  assert.deepEqual(drawn().filter(f => /-stand\.png$/.test(f)), ["street-stand.png"], "clearing the pick returns to the pack default");
  assert.equal(packFetches.length, 1);
  stage.dispose();
});

// pixelCanvas is a small software 2D canvas: nearest-neighbour drawImage under
// an affine transform, source-over and destination-in drawing, and the
// multiply fillRect the compositor's tint cache uses.
function pixelCanvas() {
  function parse(color) {
    const hex = color.slice(1);
    const full = hex.length === 3 ? [...hex].map(c => c + c).join("") : hex;
    return [0, 2, 4].map(i => parseInt(full.slice(i, i + 2), 16));
  }
  class Canvas {
    constructor(width, height) { this.width = width; this.height = height; this.pixels = new Float64Array(width * height * 4); }
    getContext() {
      const canvas = this;
      let m = [1, 0, 0, 1, 0, 0];
      const stack = [];
      const ctx = {
        canvas, fillStyle: "#000", globalCompositeOperation: "source-over",
        save() { stack.push([m, ctx.globalCompositeOperation]); },
        restore() { const top = stack.pop(); if (top) [m, ctx.globalCompositeOperation] = top; },
        translate(x, y) { m = [m[0], m[1], m[2], m[3], m[0] * x + m[2] * y + m[4], m[1] * x + m[3] * y + m[5]]; },
        scale(x, y) { m = [m[0] * x, m[1] * x, m[2] * y, m[3] * y, m[4], m[5]]; },
        rotate(theta) {
          const c = Math.cos(theta), s = Math.sin(theta);
          m = [m[0] * c + m[2] * s, m[1] * c + m[3] * s, m[2] * c - m[0] * s, m[3] * c - m[1] * s, m[4], m[5]];
        },
        clearRect() { canvas.pixels.fill(0); },
        // each visits every target pixel whose centre maps into the user rect.
        each(x, y, w, h, fn) {
          const det = m[0] * m[3] - m[1] * m[2];
          for (let py = 0; py < canvas.height; py++) {
            for (let px = 0; px < canvas.width; px++) {
              const dx = px + 0.5 - m[4], dy = py + 0.5 - m[5];
              const u = (m[3] * dx - m[2] * dy) / det, v = (m[0] * dy - m[1] * dx) / det;
              fn((py * canvas.width + px) * 4, u >= x && u < x + w && v >= y && v < y + h, (u - x) / w, (v - y) / h);
            }
          }
        },
        drawImage(image, x = 0, y = 0, w = image.width, h = image.height) {
          const out = canvas.pixels, src = image.pixels;
          ctx.each(x, y, w, h, (i, inside, fu, fv) => {
            let r = 0, g = 0, b = 0, a = 0;
            if (inside) {
              const j = (Math.floor(fv * image.height) * image.width + Math.floor(fu * image.width)) * 4;
              [r, g, b, a] = [src[j], src[j + 1], src[j + 2], src[j + 3]];
            }
            if (ctx.globalCompositeOperation === "destination-in") { out[i + 3] *= a; return; }
            if (!inside || !a) return;
            const outA = a + out[i + 3] * (1 - a);
            for (const [k, value] of [[0, r], [1, g], [2, b]]) out[i + k] = (value * a + out[i + k] * out[i + 3] * (1 - a)) / outA;
            out[i + 3] = outA;
          });
        },
        fillRect(x, y, w, h) {
          const [r, g, b] = parse(ctx.fillStyle).map(v => v / 255), out = canvas.pixels;
          assert.equal(ctx.globalCompositeOperation, "multiply", "the compositor fills only to tint");
          ctx.each(x, y, w, h, (i, inside) => {
            if (!inside) return;
            const back = out[i + 3];
            for (const [k, value] of [[0, r], [1, g], [2, b]]) out[i + k] = (1 - back) * value + back * out[i + k] * value;
            out[i + 3] = 1;
          });
        },
        getImageData(x, y, w, h) {
          const data = new Uint8ClampedArray(w * h * 4);
          for (let row = 0; row < h; row++) for (let col = 0; col < w; col++) {
            const i = ((y + row) * canvas.width + x + col) * 4, o = (row * w + col) * 4;
            for (let k = 0; k < 3; k++) data[o + k] = Math.round(canvas.pixels[i + k] * 255);
            data[o + 3] = Math.round(canvas.pixels[i + 3] * 255);
          }
          return { data };
        }
      };
      return ctx;
    }
  }
  // bitmap paints a white (or given) RGB at alpha 1 wherever cell(x, y) holds.
  function bitmap(size, cell, rgb = [1, 1, 1]) {
    const image = new Canvas(size, size);
    for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) {
      if (!cell(x, y)) continue;
      image.pixels.set([...rgb, 1], (y * size + x) * 4);
    }
    return image;
  }
  function pixel(canvas, x, y) {
    const i = (y * canvas.width + x) * 4;
    return [0, 1, 2, 3].map(k => Math.round(canvas.pixels[i + k] * 255));
  }
  return { Canvas, bitmap, pixel, parse };
}

// paperDollPack is the IP-clean procedural outfit fixture on a 16 x 16 canvas:
// one head set (egg skinFill + line), two outfits whose exposed skin (arms and
// legs) is the same pure-white skinFill region, and differently cut fills.
function paperDollPack(px) {
  const size = 16;
  const skin = (x, y) => (y >= 6 && y <= 9 && (x === 3 || x === 4 || x === 11 || x === 12)) ||
    (y >= 12 && (x === 6 || x === 7 || x === 9 || x === 10));
  const torso = (x, y) => y >= 5 && y <= 10 && x >= 5 && x <= 10;
  const files = {
    "egg-skin.png": (x, y) => y >= 1 && y <= 3 && x >= 6 && x <= 9,
    "street-skin.png": skin, "street-jacket.png": torso,
    "street-line.png": [(x, y) => y === 11 && x >= 5 && x <= 10, [30, 36, 51].map(v => v / 255)],
    "street-typing-skin.png": skin,
    "gown-skin.png": skin, "gown-fill.png": (x, y) => torso(x, y) || (y === 11 && x >= 4 && x <= 11),
    "gown-trim.png": (x, y) => y === 5 && x >= 5 && x <= 10,
    "gown-glow.png": (x, y) => y === 0 && x === 0,
    "legacy-stand.png": (x, y) => y >= 5 && x >= 2 && x <= 13
  };
  const bitmaps = new Map(Object.entries(files).map(([name, spec]) =>
    [name, Array.isArray(spec) ? px.bitmap(size, spec[0], spec[1]) : px.bitmap(size, spec)]));
  const manifest = {
    canvas: [size, size],
    head: {pivot: [8, 4], egg: {skinFill: "egg-skin.png"}},
    poses: {
      stand: {headAnchor: [8, 4], layers: [{file: "legacy-stand.png"}]},
      typing: {fallback: "stand", headAnchor: [8, 7]}
    },
    gestures: {},
    defaultOutfit: "street",
    outfits: {
      street: {poses: {
        stand: {layers: [{skinFill: "street-skin.png", jacketFill: "street-jacket.png", line: "street-line.png"}]},
        typing: {layers: [{skinFill: "street-typing-skin.png", jacketFill: "street-jacket.png"}]}
      }},
      gown: {palette: {outfitPrimary: "#aa3355", trim: "#123456"}, poses: {
        stand: {layers: [{fills: {skin: "gown-skin.png", outfitPrimary: "gown-fill.png", trim: "gown-trim.png", glow: "gown-glow.png"}}]}
      }}
    }
  };
  return {pack: {manifest, bitmaps}, skin, torso};
}

test("paper doll outfits share exposed-skin pixels tinted by skin at four tints with no outfit fill on skin", () => {
  const px = pixelCanvas();
  const { makeRig } = companion({OffscreenCanvas: px.Canvas});
  const { pack, skin } = paperDollPack(px);
  const rig = makeRig(pack, null, "");
  const gaze = {x: 0, y: 0};
  const torsoByOutfit = {};
  for (const tint of ["#f4cdb4", "#8d5524", "#ffe0bd", "#3b2219"]) {
    const rgb = px.parse(tint);
    for (const outfit of ["street", "gown"]) {
      rig.setWardrobe({outfit, palette: "skin=" + tint});
      assert.equal(rig.wardrobe().outfit, outfit);
      const art = rig.view("stand", null, "idle", []).render(0, gaze);
      for (let y = 0; y < 16; y++) for (let x = 0; x < 16; x++) {
        if (skin(x, y)) assert.deepEqual(px.pixel(art, x, y), [...rgb, 255], `${outfit} ${tint} skin at ${x},${y}`);
      }
      // The head egg shares the skin channel, so exposed skin and head match.
      assert.deepEqual(px.pixel(art, 7, 2), [...rgb, 255], `${outfit} ${tint} head-egg skin`);
      (torsoByOutfit[outfit] ||= new Set()).add(px.pixel(art, 7, 7).join());
    }
  }
  // Each outfit's body stack is its own: the torso keeps its outfit color
  // across skin tints and differs between outfits.
  assert.equal(torsoByOutfit.street.size, 1);
  assert.equal(torsoByOutfit.gown.size, 1);
  assert.deepEqual([...torsoByOutfit.street], [[242, 229, 212, 255].join()], "street jacketFill takes the built-in outfitPrimary");
  assert.deepEqual([...torsoByOutfit.gown], [[170, 51, 85, 255].join()], "gown fills.outfitPrimary takes the outfit palette");
});

test("paper doll fills channels take their palette color, uncolored channels draw untinted, and seated columns lower the head", () => {
  const px = pixelCanvas();
  const { makeRig } = companion({OffscreenCanvas: px.Canvas});
  const { pack } = paperDollPack(px);
  const gaze = {x: 0, y: 0};
  const rig = makeRig(pack, null, "", {outfit: "gown"});
  let art = rig.view("stand", null, "idle", []).render(0, gaze);
  assert.deepEqual(px.pixel(art, 7, 5), [0x12, 0x34, 0x56, 255], "custom trim channel tinted by the outfit palette");
  assert.deepEqual(px.pixel(art, 0, 0), [255, 255, 255, 255], "a channel with no color draws its raster untinted");
  // Precedence: built-in < pack < outfit < preset < config.
  pack.manifest.palette = {trim: "#000001", outfitPrimary: "#000004"};
  art = makeRig(pack, null, "", {outfit: "street"}).view("stand", null, "idle", []).render(0, gaze);
  assert.deepEqual(px.pixel(art, 7, 7), [0, 0, 4, 255], "the pack palette beats the built-in outfitPrimary");
  art = makeRig(pack, null, "", {outfit: "gown"}).view("stand", null, "idle", []).render(0, gaze);
  assert.deepEqual(px.pixel(art, 7, 5), [0x12, 0x34, 0x56, 255], "the outfit palette beats the pack palette");
  assert.deepEqual(px.pixel(art, 7, 7), [170, 51, 85, 255], "the outfit outfitPrimary beats the pack's");
  pack.manifest.presets = {p: {palette: {trim: "#000002"}}};
  const layered = makeRig(pack, null, "p", {outfit: "gown"});
  art = layered.view("stand", null, "idle", []).render(0, gaze);
  assert.deepEqual(px.pixel(art, 7, 5), [0, 0, 2, 255], "the preset palette beats the outfit palette");
  layered.setWardrobe({outfit: "gown", palette: "trim=#000003 skin=bogus glow=#0a0b0c"});
  art = layered.view("stand", null, "idle", []).render(0, gaze);
  assert.deepEqual(px.pixel(art, 7, 5), [0, 0, 3, 255], "the config palette beats the preset");
  assert.deepEqual(px.pixel(art, 0, 0), [10, 11, 12, 255], "a config color adds a channel color");
  assert.equal(layered.wardrobe().palette.skin, "#f4cdb4", "a malformed config pair is skipped");

  // A seated column anchors the head lower at headScale 1.0: same egg size.
  const street = makeRig(paperDollPack(px).pack, null, "");
  const eggRows = view => {
    const canvas = view.render(0, gaze), rows = [];
    for (let y = 0; y < 16; y++) if (px.pixel(canvas, 7, y)[0] === 244 && px.pixel(canvas, 7, y)[2] === 180 && px.pixel(canvas, 8, y)[0] === 244) rows.push(y);
    return rows;
  };
  const standView = street.view("stand", null, "idle", []), typingView = street.view("typing", null, "idle", []);
  assert.equal(typingView.pose, "typing");
  assert.deepEqual(eggRows(standView).filter(y => y < 5), [1, 2, 3]);
  assert.deepEqual(eggRows(typingView).filter(y => y >= 4 && y < 8), [4, 5, 6], "the typing column's head sits three rows lower, unscaled");
});

test("paper doll gesture head anchors move the head only in outfit mode", () => {
  const px = pixelCanvas();
  const { makeRig } = companion({OffscreenCanvas: px.Canvas});
  const gaze = {x: 0, y: 0};
  const { pack } = paperDollPack(px);
  pack.manifest.gestures.nod = {pose: "stand", headAnchor: [8, 6]};
  pack.manifest.outfits.street.gestures = {nod: {headAnchor: [8, 5], layers: [{skinFill: "street-skin.png"}]}};
  // Egg rows: both centre columns carry the default skin tint.
  const eggRows = view => {
    const canvas = view.render(0, gaze), rows = [];
    for (let y = 0; y < 8; y++) if ([7, 8].every(x => px.pixel(canvas, x, y).join() === "244,205,180,255")) rows.push(y);
    return rows;
  };
  const rig = makeRig(pack, null, "");
  assert.deepEqual(eggRows(rig.view("stand", null, "idle", [])), [1, 2, 3]);
  assert.deepEqual(eggRows(rig.view("stand", "nod", "idle", [])), [2, 3, 4], "a bound gesture column's own head anchor wins");
  rig.setWardrobe({outfit: "gown"});
  assert.deepEqual(eggRows(rig.view("stand", "nod", "idle", [])), [3, 4, 5], "an unbound gesture keeps the registry head anchor");
  const { outfits, defaultOutfit, ...legacy } = pack.manifest;
  const plain = makeRig({manifest: legacy, bitmaps: pack.bitmaps}, null, "");
  assert.deepEqual(eggRows(plain.view("stand", "nod", "idle", [])), [1, 2, 3], "a legacy pack ignores a gesture head anchor");
});

// stackCanvas records what each canvas draws; stack() reads a view's drawn
// layers back as file names (tinted rasters read "<file>@<color>").
function stackCanvas() {
  class Canvas {
    constructor(width, height) { this.width = width; this.height = height; this.drawn = []; }
    getContext() {
      const canvas = this;
      return {
        canvas, fillStyle: "", globalCompositeOperation: "source-over",
        save() {}, restore() {}, translate() {}, rotate() {}, scale() {},
        clearRect() { canvas.drawn.length = 0; },
        fillRect() { canvas.tint = this.fillStyle; },
        drawImage(image) { canvas.drawn.push(image); },
        getImageData: () => ({data: []})
      };
    }
  }
  // A tint buffer reads "<file>@<color>", another buffer (the iris) lists
  // what it composited.
  const label = image => image.file || (image.tint ? image.drawn[0]?.file + "@" + image.tint :
    "[" + image.drawn.map(item => item.file).join("+") + "]");
  return { Canvas, stack: (view, t = 0) => Array.from(view.render(t, {x: 0, y: 0}).drawn, label) };
}

function wardrobePack() {
  const names = ["legacy-stand", "legacy-typing", "legacy-wave", "street-stand", "street-arm", "street-typing", "street-wave",
    "gown-stand", "gown-perched", "bare-perched", "egg", "eyes-default", "eyes-round", "iris-round", "hair-bob", "hair-bun",
    "cap", "ears", "tail", "tail-seated", "tail-cheer"];
  const bitmaps = new Map(names.map(name => [name + ".png", {file: name + ".png"}]));
  const manifest = {
    canvas: [512, 512],
    head: {egg: {file: "egg.png"}, eyes: {frameLower: "eyes-default.png"}},
    hairs: {bob: {front: {file: "hair-bob.png"}}, bun: {front: {file: "hair-bun.png"}}},
    eyesets: {round: {sclera: "eyes-round.png", irisLine: "iris-round.png"}},
    attachments: {
      cap: {slot: "headdress", bind: "head", layers: [{file: "cap.png"}]},
      ears: {slot: "ears", bind: "head", layers: [{file: "ears.png"}]},
      tail: {slot: "tail", bind: "body", layers: [{file: "tail.png"}],
        columns: {typing: {layers: [{file: "tail-seated.png"}]}, cheer: {layers: [{file: "tail-cheer.png"}]}}},
      mislabeled: {slot: "ears", bind: "head", layers: [{file: "cap.png"}]}
    },
    poses: {
      stand: {layers: [{file: "legacy-stand.png"}]},
      typing: {fallback: "stand", layers: [{file: "legacy-typing.png"}]},
      slouch: {fallback: "typing"},
      perched: {}
    },
    gestures: {
      wave: {pose: "stand", replaces: ["arm"], layers: [{file: "legacy-wave.png"}]},
      tuck: {replaces: ["arm"]},
      cheer: {pose: "stand", handAnchor: [100, 200]}
    },
    presets: {
      evening: {outfit: "gown", hair: "bun", eyes: "round", attachments: {headdress: "cap", tail: "tail"}},
      ghost: {outfit: "missing", hair: "missing", eyes: "missing"}
    },
    defaultOutfit: "street",
    outfits: {
      street: {
        poses: {
          stand: {layers: [{id: "body", file: "street-stand.png"}, {slot: "arm", file: "street-arm.png"}]},
          typing: {layers: [{file: "street-typing.png"}]}
        },
        gestures: {wave: {layers: [{file: "street-wave.png"}]}}
      },
      gown: {poses: {stand: {layers: [{file: "gown-stand.png"}]}, perched: {headAnchor: [256, 300], layers: [{file: "gown-perched.png"}]}}},
      bare: {poses: {perched: {layers: [{file: "bare-perched.png"}]}}}
    }
  };
  return {manifest, bitmaps};
}

test("paper doll columns resolve inside the active outfit, never borrow, and end at the legacy projection", () => {
  const { Canvas, stack } = stackCanvas();
  const { makeRig } = companion({OffscreenCanvas: Canvas});
  const rig = makeRig(wardrobePack(), null, "");
  const body = (pose, gesture = null) => {
    const view = rig.view(pose, gesture, "idle", []);
    return [view.pose, stack(view).filter(file => !["egg.png", "eyes-default.png", "hair-bob.png"].includes(file))];
  };
  assert.equal(rig.wardrobe().outfit, "street", "pack defaultOutfit");
  assert.deepEqual(body("stand"), ["stand", ["street-stand.png", "street-arm.png"]]);
  assert.deepEqual(body("slouch"), ["typing", ["street-typing.png"]], "a custom fallback chain runs inside the outfit");
  assert.deepEqual(body("perched"), ["stand", ["street-stand.png", "street-arm.png"]], "the implicit stand step runs inside the outfit");
  rig.setWardrobe({outfit: "gown"});
  assert.deepEqual(body("typing"), ["stand", ["gown-stand.png"]], "an unbound column never borrows another outfit's column");
  assert.deepEqual(body("perched"), ["perched", ["gown-perched.png"]]);
  rig.setWardrobe({outfit: "bare"});
  assert.deepEqual(body("stand"), ["stand", ["legacy-stand.png"]], "a chain ending unbound draws the legacy projection");
  assert.deepEqual(body("typing"), ["typing", ["legacy-typing.png"]]);
  assert.deepEqual(body("perched"), ["perched", ["bare-perched.png"]]);
});

test("paper doll gesture columns replace by id or slot in their own outfit and never draw another outfit's gesture", () => {
  const { Canvas, stack } = stackCanvas();
  const { makeRig } = companion({OffscreenCanvas: Canvas});
  const rig = makeRig(wardrobePack(), null, "");
  const body = gesture => stack(rig.view("stand", gesture, "idle", [])).filter(file => !["egg.png", "eyes-default.png", "hair-bob.png"].includes(file));
  assert.deepEqual(body("wave"), ["street-stand.png", "street-wave.png"], "the outfit's wave replaces the slot-matched arm");
  assert.deepEqual(body("tuck"), ["street-stand.png"], "a pure-removal gesture still removes inside an outfit");
  rig.setWardrobe({outfit: "gown"});
  assert.deepEqual(body("wave"), ["gown-stand.png"], "an unbound gesture keeps the outfit's stack and draws no legacy art");
  const cheer = rig.view("stand", "cheer", "idle", []);
  assert.deepEqual([...cheer.handAnchor], [100, 200], "an unbound gesture keeps its anchors");
});

test("paper doll attachments honor an explicit z and fills-only outfit frames draw their channels", () => {
  const { Canvas, stack } = stackCanvas();
  const { makeRig } = companion({OffscreenCanvas: Canvas});
  const pack = wardrobePack();
  pack.manifest.attachments.cap.z = 5;
  pack.manifest.attachments.tail.z = 25;
  for (const file of ["frame-a.png", "frame-b.png"]) pack.bitmaps.set(file, {file});
  pack.manifest.outfits.street.poses.stand.layers = [{fps: 1, frames: [{fills: {skin: "frame-a.png"}}, {fills: {glow: "frame-b.png"}}]}];
  const rig = makeRig(pack, null, "", {attachments: "headdress=cap tail=tail"});
  const view = rig.view("stand", null, "idle", []);
  assert.deepEqual(stack(view), ["cap.png", "frame-a.png@#f4cdb4", "tail.png", "egg.png", "eyes-default.png", "hair-bob.png"],
    "an explicit z moves the headdress below the body and the tail above it; frame 0 draws its skin channel tinted");
  assert.deepEqual(stack(view, 1).slice(1, 2), ["frame-b.png"], "frame 1 draws an uncolored channel untinted");
});

test("paper doll leaves legacy pack stacks unchanged", () => {
  const { Canvas, stack } = stackCanvas();
  const { makeRig } = companion({OffscreenCanvas: Canvas});
  const manifest = {
    canvas: [512, 512],
    poses: {stand: {headAnchor: [200, 100], layers: [{id: "body", file: "body.png"}, {id: "arm", file: "arm.png"}, {slot: "arm", file: "sleeve.png"}]}},
    gestures: {wave: {replaces: ["arm"], headAnchor: [10, 10], layers: [{file: "wave.png"}]}}
  };
  const bitmaps = new Map(["body.png", "arm.png", "sleeve.png", "wave.png"].map(file => [file, {file}]));
  for (const wardrobe of [undefined, {outfit: "street", hair: "bob", eyes: "round", attachments: "tail=tail"}]) {
    const rig = makeRig({manifest, bitmaps}, null, "", wardrobe);
    assert.deepEqual(stack(rig.view("stand", null, "idle", [])), ["body.png", "arm.png", "sleeve.png"]);
    assert.deepEqual(stack(rig.view("stand", "wave", "idle", [])), ["body.png", "sleeve.png", "wave.png"], "legacy replaces match ids only");
    assert.equal(rig.wardrobe().outfit, null);
  }
});

test("paper doll head set swaps change only their own layers and selection runs config, preset, pack default", () => {
  const { Canvas, stack } = stackCanvas();
  const { makeRig } = companion({OffscreenCanvas: Canvas});
  const rig = makeRig(wardrobePack(), null, "");
  const draw = (pose = "stand", gesture = null) => stack(rig.view(pose, gesture, "idle", []));
  const base = draw();
  assert.deepEqual(base, ["street-stand.png", "street-arm.png", "egg.png", "eyes-default.png", "hair-bob.png"]);
  const without = (list, ...files) => list.filter(file => !files.includes(file));

  rig.setWardrobe({hair: "bun"});
  assert.deepEqual(without(draw(), "hair-bun.png"), without(base, "hair-bob.png"), "a hair swap changes only the hair");
  rig.setWardrobe({attachments: "headdress=cap tail=tail"});
  const dressed = draw();
  assert.deepEqual(without(dressed, "cap.png", "tail.png"), base, "attachments add only their own layers");
  assert.ok(dressed.indexOf("tail.png") < dressed.indexOf("street-stand.png"), "tail defaults behind the body (z11)");
  assert.ok(dressed.indexOf("cap.png") > dressed.indexOf("hair-bob.png"), "headdress defaults above the front hair (z56)");
  assert.deepEqual(without(draw("typing"), "egg.png", "eyes-default.png", "hair-bob.png", "cap.png"), ["tail-seated.png", "street-typing.png"],
    "a body attachment follows the column's layers");
  assert.ok(draw("stand", "cheer").includes("tail-cheer.png"), "a gesture column entry wins over the pose column");
  rig.setWardrobe({eyes: "round"});
  assert.deepEqual(without(draw(), "eyes-round.png", "[iris-round.png+eyes-round.png]"), without(base, "eyes-default.png"),
    "an eye-set swap changes only the eyes");
  assert.ok(draw().includes("[iris-round.png+eyes-round.png]"), "the swapped eye set has a sclera, so the iris buffer is recomputed and drawn");
  rig.setWardrobe({});
  assert.deepEqual(draw(), base, "clearing the picks re-renders the pack defaults with no stale cache");

  // Selection: config key > preset > pack default; an ID the pack lacks
  // falls back to the next source; attachments merge per slot.
  rig.setPreset("evening");
  assert.deepEqual(JSON.parse(JSON.stringify(rig.wardrobe())), {outfit: "gown", hair: "bun", eyes: "round",
    attachments: {headdress: "cap", tail: "tail"},
    palette: {hair: "#ec8468", eye: "#dfa040", skin: "#f4cdb4", outfitPrimary: "#f2e5d4", outfitAccent: "#e6aa34"}},
    "with no palette in the pack, outfit or preset, the built-in colors apply");
  rig.setWardrobe({outfit: "street", hair: "bob", eyes: "missing", attachments: "headdress=none ears=ears tail=mislabeled Bad=x ears2"});
  const picks = rig.wardrobe();
  assert.equal(picks.outfit, "street");
  assert.equal(picks.hair, "bob");
  assert.equal(picks.eyes, "round", "an absent eye set falls back to the preset");
  assert.deepEqual({...picks.attachments}, {ears: "ears", tail: "tail"}, "none clears, a wrong-slot ID and malformed pairs are skipped");
  rig.setWardrobe({outfit: "missing", hair: "missing"});
  assert.equal(rig.wardrobe().outfit, "gown", "an absent outfit falls back to the preset");
  assert.equal(rig.wardrobe().hair, "bun");
  rig.setPreset("ghost");
  rig.setWardrobe({});
  assert.equal(rig.wardrobe().outfit, "street", "an absent preset outfit falls back to the pack default");
  assert.equal(rig.wardrobe().eyes, null, "the pack-default eyes are head.eyes");
});

test("paper doll wardrobe pairs parse flat config strings and skip malformed pairs", () => {
  const { attachmentPairs, palettePairs } = companion();
  assert.deepEqual({...attachmentPairs("headdress=cap  tail=none")}, {headdress: "cap", tail: "none"});
  assert.deepEqual({...attachmentPairs(" Head=cap ears=Ears tail= =x ears ok=a=b tail=tail tail=other ")}, {tail: "tail"});
  assert.deepEqual({...palettePairs("skin=#AABBCC outfit_2=#010203 eye=#abc hair=red 9x=#000000")}, {skin: "#AABBCC", outfit_2: "#010203"});
  assert.deepEqual({...attachmentPairs(undefined)}, {});
  const many = Array.from({length: 20}, (_, i) => "s" + i + "=a").join(" ");
  assert.equal(Object.keys(attachmentPairs(many)).length, 16, "at most 16 pairs");
});

test("paper doll wardrobe options list selectable outfits, hairs, eye sets, slots and channels", () => {
  const { wardrobeOptions } = companion();
  const manifest = wardrobePack().manifest;
  manifest.outfits.Fancy = {poses: {}};
  manifest.attachments.none = {slot: "tail"};
  manifest.attachments.odd = {slot: "Head"};
  manifest.palette = {trim: "#000000", "bad id": "#000000"};
  manifest.outfits.street.poses.stand.layers.push({fills: {glow: "x.png"}, frames: [{fills: {spark: "y.png"}}]});
  const options = JSON.parse(JSON.stringify(wardrobeOptions(manifest)));
  assert.deepEqual(options.outfits, ["bare", "gown", "street"]);
  assert.deepEqual(options.hairs, ["bob", "bun"]);
  assert.deepEqual(options.eyesets, ["round"]);
  assert.deepEqual(options.slots, {headdress: ["cap"], ears: ["ears", "mislabeled"], tail: ["tail"]});
  assert.deepEqual(options.channels, ["eye", "glow", "hair", "outfitAccent", "outfitPrimary", "skin", "spark", "trim"]);
  assert.deepEqual(JSON.parse(JSON.stringify(wardrobeOptions(null))).outfits, []);
});

test("companion loader fetches every paper-doll raster the server enumerates", async () => {
  const requested = [];
  const manifestData = {
    id: "kit", schema: "ws-companion/v1", canvas: [512, 512],
    head: {egg: {skinFill: "egg.png", fills: {blush: "blush.png"}}, eyes: {sclera: "h-sclera.png", closedL: "h-closed-l.png", closedR: "h-closed-r.png"}},
    hairs: {bob: {front: {line: "hair.png", fills: {tip: "tip.png"}}}},
    poses: {stand: {layers: [{line: "stand.png", fills: {trim: "trim.png"}}]}},
    outfits: {o: {poses: {stand: {layers: [{fills: {skin: "o-skin.png"}}, {fps: 2, frames: [{fills: {skin: "o-frame.png"}}, "o-frame2.png"]}]}},
      gestures: {wave: {layers: [{line: "o-wave.png"}]}}}},
    attachments: {t: {layers: [{file: "t.png"}], columns: {stand: {layers: [{fills: {hair: "t-col.png"}}]}}}},
    eyesets: {round: {sclera: "r-sclera.png", irisFill: "r-iris.png", closedL: "r-closed-l.png", closedR: "r-closed-r.png", note: "ignored.png"}},
    future: {file: "never.png"}
  };
  const { packLoader } = companion({
    fetch: async url => {
      if (url.startsWith("/api/companion/packs?") || url === "/api/companion/packs") {
        return {ok: true, json: async () => ({resolvedAuto: {packId: "kit"}, packs: [{id: "kit", rev: "r"}, {id: "default", rev: "d"}]})};
      }
      if (url.includes("/pack.json?")) return {ok: true, json: async () => ({...manifestData, props: {drone: {}, mail: {}, "prop-typing-desk": {}}})};
      requested.push(decodeURIComponent(url.split("/assets/")[1].split("?")[0]));
      return {ok: true, blob: async () => url};
    },
    createImageBitmap: async () => ({width: 512, height: 512, close() {}})
  });
  await new Promise((done, fail) => packLoader(done, () => fail(new Error("unexpected failure"))).load("kit", {}));
  assert.deepEqual(requested.sort(), ["blush.png", "egg.png", "h-closed-l.png", "h-closed-r.png", "h-sclera.png", "hair.png", "o-frame.png", "o-frame2.png", "o-skin.png", "o-wave.png",
    "r-closed-l.png", "r-closed-r.png", "r-iris.png", "r-sclera.png", "stand.png", "t-col.png", "t.png", "tip.png", "trim.png"]);
});

function focusHarness(visibility = "visible") {
  const listeners = {}, calls = [];
  const doc = { visibilityState: visibility, addEventListener: (name, fn) => { listeners[name] = fn; } };
  const env = { document: doc, fetch: (url, init) => {
    const call = { url, ...init, body: JSON.parse(init.body) };
    calls.push(call);
    return env.respond(call);
  }, respond: () => Promise.resolve({ ok: true, status: 204 }) };
  const reporter = companion().focusReporter(env);
  const bodies = () => calls.map(c => c.body);
  const hide = state => { doc.visibilityState = state; listeners.visibilitychange(); };
  return { reporter, calls, bodies, env, hide };
}

const agentConfig = { enabled: true, director: "agent", pack: "auto" };

test("companion focus reports the shown session and resolved pack on switches and dedups", () => {
  const { reporter, calls, bodies } = focusHarness();
  reporter.config(agentConfig);
  reporter.show("rid1", "default");
  assert.equal(calls.length, 0, "no token yet");
  reporter.token("t1");
  assert.deepEqual(bodies(), [{ token: "t1", sessionId: "rid1", pack: "default" }]);
  assert.equal(calls[0].url, "/api/companion/focus");
  assert.equal(calls[0].method, "POST");
  assert.equal(calls[0].credentials, "same-origin");
  assert.equal(calls[0].headers["Content-Type"], "application/json");
  reporter.show("rid1", "default");
  reporter.config({ ...agentConfig, preset: "other" });
  assert.equal(calls.length, 1, "same token, session and pack are not re-sent");
  reporter.show("rid2", "default");
  reporter.show("rid2", "mint");
  assert.deepEqual(bodies().slice(1), [{ token: "t1", sessionId: "rid2", pack: "default" }, { token: "t1", sessionId: "rid2", pack: "mint" }]);
  reporter.show("rid2", "");
  assert.deepEqual(bodies().at(-1), { token: "t1", sessionId: "", pack: "" }, "an unresolved pack clears");
  reporter.show("", "");
  assert.equal(calls.length, 4, "a second clear is not re-sent");
});

test("companion focus clears when hidden and reports again when visible", () => {
  const { reporter, bodies, hide } = focusHarness();
  reporter.config(agentConfig);
  reporter.token("t1");
  reporter.show("rid1", "default");
  hide("hidden");
  assert.deepEqual(bodies().slice(1), [{ token: "t1", sessionId: "", pack: "" }]);
  hide("hidden");
  reporter.show("rid2", "default");
  assert.equal(bodies().length, 2, "nothing is sent while hidden");
  hide("visible");
  assert.deepEqual(bodies().at(-1), { token: "t1", sessionId: "rid2", pack: "default" });
  assert.equal(bodies().length, 3);
});

test("companion focus sends nothing in rules mode or when disabled", () => {
  const { reporter, calls, hide } = focusHarness();
  reporter.token("t1");
  reporter.show("rid1", "default");
  for (const config of [{ enabled: true, director: "rules" }, { enabled: false, director: "agent" }, { director: "agent" }, null]) {
    reporter.config(config);
    reporter.show("rid2", "default");
    hide("hidden");
    hide("visible");
    reporter.token("t2");
  }
  assert.equal(calls.length, 0);
});

test("companion focus clears on leaving agent mode and reports on token change", () => {
  const { reporter, bodies } = focusHarness();
  reporter.token("t1");
  reporter.show("rid1", "default");
  reporter.config(agentConfig);
  assert.deepEqual(bodies(), [{ token: "t1", sessionId: "rid1", pack: "default" }]);
  reporter.token("t2");
  assert.deepEqual(bodies().at(-1), { token: "t2", sessionId: "rid1", pack: "default" }, "a reconnect reports again");
  reporter.config({ ...agentConfig, director: "rules" });
  assert.deepEqual(bodies().at(-1), { token: "t2", sessionId: "", pack: "" });
  assert.equal(bodies().length, 3);
  reporter.config({ ...agentConfig, enabled: false });
  assert.equal(bodies().length, 3, "no repeat clear");
  reporter.config(agentConfig);
  assert.deepEqual(bodies().at(-1), { token: "t2", sessionId: "rid1", pack: "default" });
  reporter.config({ ...agentConfig, enabled: false });
  assert.deepEqual(bodies().at(-1), { token: "t2", sessionId: "", pack: "" });
  reporter.token("t3");
  assert.equal(bodies().length, 5, "a new token after a clear sends nothing");
});

test("companion focus ignores refusals, a gone stream, an older daemon and network errors", async () => {
  for (const respond of [
    () => Promise.resolve({ ok: false, status: 404 }),
    () => Promise.resolve({ ok: false, status: 410 }),
    () => Promise.resolve({ ok: false, status: 403 }),
    () => Promise.reject(new Error("offline")),
    () => { throw new Error("sync failure"); }
  ]) {
    const { reporter, calls, env } = focusHarness();
    env.respond = respond;
    reporter.config(agentConfig);
    reporter.token("t1");
    reporter.show("rid1", "default");
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(calls.length, 1, "no retry after a failure");
    reporter.show("rid2", "default");
    assert.equal(calls.length, 2, "later state changes still report");
  }
});
