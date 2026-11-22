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
