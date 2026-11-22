const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

function companion(globals = {}) {
  const context = vm.createContext({ window: {}, URLSearchParams, AbortController, ...globals });
  for (const name of ["packs", "compositor", "scenarios", "stage"]) {
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
  let now = 10000, timer = 0, hidden = false, phase = "idle", children = [], rows = [], subdeep = 0, asks = [], permissions = [];
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
    q: selector => selector.startsWith("#r-convlive .permwait") ? permissions : [], boardQ: () => asks, region: () => ({}), hudReadData: () => ({roots: children, completed: 0}),
    entriesBox: () => ({children: rows}), $: () => null, pageHidden: () => hidden,
    showRegions: () => { while (held.length) held.shift()(); }, shPost: (_, payload) => posts.push(payload),
    innerHeight: 1000, innerWidth: 1000
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", "dashboard-companion-relay.js"), "utf8") + "\nthis.relay = startCompanionRelay();", context);
  function flush() { const work = [...timers.values()]; timers.clear(); work.forEach(fn => fn()); }
  function update() { now += 300; listeners.change.forEach(fn => fn()); flush(); }
  function events() { return posts.splice(0).flatMap(p => p.events); }
  flush(); events();
  return {set children(value) { children = value; }, get rows() { return rows; }, set rows(value) { rows = value; }, set phase(value) { phase = value; }, set subdeep(value) { subdeep = value; }, set asks(value) { asks = value; }, set permissions(value) { permissions = value; },
    update, events, posts, now: () => now,
    input() { listeners.input.forEach(fn => fn({target: {matches: () => true}})); flush(); },
    visibility(value, swap) { hidden = value; if (swap) held.push(swap); listeners.visibilitychange.forEach(fn => fn()); flush(); },
    dispose: () => context.relay.dispose()};
}

function relayRow(key, classes, {childId, ms, cause, child, label} = {}) {
  const names = new Set(classes.split(" "));
  return {dataset: {entry: key, childId, wakeCause: cause},
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
  for (const name of ["packs", "compositor", "scenarios", "stage"]) {
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

  stage.dispose();
});
