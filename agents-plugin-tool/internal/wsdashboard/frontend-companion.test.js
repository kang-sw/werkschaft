const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

function companion(globals = {}) {
  const context = vm.createContext({ window: {}, URLSearchParams, AbortController, ...globals });
  for (const name of ["packs", "compositor", "scenarios"]) {
    vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", `dashboard-companion-${name}.js`), "utf8"), context);
  }
  return context.window.WsCompanion;
}

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
