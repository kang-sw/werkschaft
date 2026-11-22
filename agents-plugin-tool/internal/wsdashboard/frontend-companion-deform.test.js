const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

function companion(globals = {}) {
  const context = vm.createContext({ window: {}, URLSearchParams, AbortController, ...globals });
  for (const name of ["builtins", "packs", "focus", "deform", "compositor", "scenarios", "menu", "settings", "stage"]) {
    vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", `dashboard-companion-${name}.js`), "utf8"), context);
  }
  return context.window.WsCompanion;
}

// recorder builds an OffscreenCanvas whose contexts log every call as
// { c: canvas index, op, args }. Canvas 0 is the rig's composite. A canvas
// reads back opaque RGBA wherever cover(x, y) holds.
function recorder(cover = () => true) {
  const log = [], canvases = [];
  let base = 0;
  class Canvas {
    constructor(width, height) { this.width = width; this.height = height; this.index = canvases.push(this) - 1; }
    getContext() {
      const c = this.index, record = op => (...args) => { log.push({ c, op, args }); };
      const ctx = { canvas: this };
      for (const op of ["save", "restore", "clearRect", "translate", "rotate", "scale", "beginPath", "rect", "moveTo", "lineTo",
        "closePath", "clip", "transform", "setTransform", "fill", "stroke", "quadraticCurveTo", "fillRect"]) ctx[op] = record(op);
      ctx.drawImage = (image, ...args) => { log.push({ c, op: "drawImage", image, args }); };
      ctx.getImageData = (x, y, w, h) => {
        const data = new Uint8ClampedArray(w * h * 4);
        for (let row = 0; row < h; row++) for (let col = 0; col < w; col++) data[(row * w + col) * 4 + 3] = cover(x + col, y + row) ? 255 : 0;
        return { data };
      };
      return ctx;
    }
  }
  // mark() names the next canvas made the composite (a rig builds it first).
  return { Canvas, log, canvases, mark: () => { base = canvases.length; }, composite: () => log.filter(entry => entry.c === base), clear: () => { log.length = 0; } };
}

const everything = [[-100, -100], [200, -100], [200, 200], [-100, 200]];
const hold = (low, high = low) => ({ oscillate: { rotDeg: [low, high], hz: 1 } });

// deformPack is an IP-clean 16 x 16 outfit fixture: a static body, a rigid
// tail with its own motion track and the given column deform.
function deformPack(deform, extra = {}) {
  const manifest = {
    canvas: [16, 16],
    head: { pivot: [8, 4], egg: { file: "egg.png" } },
    poses: { stand: { headAnchor: [8, 4], layers: [{ file: "legacy.png" }] } },
    gestures: {},
    defaultOutfit: "street",
    outfits: {
      street: { poses: { stand: { headAnchor: [8, 4], deform, layers: [
        { id: "body", file: "body.png" },
        { id: "tail", file: "tail.png", z: 25, motion: hold(1) }
      ] } } },
      other: { poses: { stand: { headAnchor: [8, 4], deform, layers: [{ id: "body", file: "body.png" }] } } }
    },
    ...extra
  };
  const bitmaps = new Map(["egg", "legacy", "body", "tail", "arm", "hand"].map(id => [id + ".png", { id: id + ".png" }]));
  return { manifest, bitmaps };
}

const rig = (pack, rec, wardrobe) => companion({ OffscreenCanvas: rec.Canvas }).makeRig(pack, null, "", wardrobe);
const gaze = { x: 0, y: 0 };
const near = (actual, expected, message) => assert.ok(Math.abs(actual - expected) < 1e-9, `${message}: ${actual} vs ${expected}`);

test("deform weights are 1 inside a region, fall off over the feather, normalize per sibling set and leave the rest on identity", () => {
  const { deform } = companion();
  const box = [[0, 0], [100, 0], [100, 100], [0, 100]];
  const bones = deform.parseBones({ bones: {
    a: { pivot: [0, 0], region: box, feather: 20 },
    b: { pivot: [0, 0], region: box, feather: 20 }
  } });
  assert.deepEqual([...deform.coefficients(bones, 50, 50)], [0.5, 0.5, 0], "inside both: normalized, nothing left over");
  assert.deepEqual([...deform.coefficients(bones, 500, 500)], [0, 0, 1], "far outside: the rest pose keeps all weight");
  near(deform.regionWeight(bones[0].region, 20, 110, 50), 0.5, "smoothstep midpoint 10 px outside a 20 px feather");
  assert.deepEqual([...deform.coefficients(bones, 110, 50)], [0.5, 0.5, 0], "two half weights fill the sibling set exactly");
  const solo = deform.parseBones({ bones: { a: { pivot: [0, 0], region: box, feather: 20 } } });
  assert.deepEqual([...deform.coefficients(solo, 110, 50)], [0.5, 0.5], "half the weight left over stays on the rest pose");
  const crowd = deform.parseBones({ bones: { a: { pivot: [0, 0], region: box }, b: { pivot: [0, 0], region: box }, c: { pivot: [0, 0], region: box } } });
  near(deform.coefficients(crowd, 50, 50)[0], 1 / 3, "three overlapping siblings normalize to thirds");
  assert.equal(deform.regionWeight(bones[0].region, 20, 130, 50), 0, "beyond the feather");
  assert.equal(deform.regionWeight(bones[0].region, 0, 100.5, 50), 0, "a zero feather is a hard edge");
  const defaults = deform.parseBones({ bones: { a: { pivot: [0, 0], region: box } } });
  assert.equal(defaults[0].feather, 24, "feather defaults to 24 px");
});

test("a child's weight blends its parent's chain against its own, so a shin takes the full knee chain", () => {
  const { deform } = companion();
  const box = [[0, 0], [100, 0], [100, 100], [0, 100]], shin = [[0, 50], [100, 50], [100, 100], [0, 100]];
  const bones = deform.parseBones({ bones: {
    knee: { pivot: [10, 0], parent: "hip", region: shin, feather: 0 },
    hip: { pivot: [0, 0], region: box, feather: 0 }
  } });
  assert.deepEqual([...bones.map(bone => bone.id)], ["hip", "knee"], "parents come first regardless of key order");
  const inShin = [...deform.coefficients(bones, 50, 75)], inThigh = [...deform.coefficients(bones, 50, 25)];
  assert.deepEqual(inShin, [0, 1, 0], "inside hip and knee: the whole knee chain, not a hip/knee average");
  assert.deepEqual(inThigh, [1, 0, 0], "inside the hip only: the hip chain");
  const soft = deform.parseBones({ bones: {
    hip: { pivot: [0, 0], region: box, feather: 0 },
    knee: { pivot: [10, 0], parent: "hip", region: shin, feather: 40 }
  } });
  const mid = [...deform.coefficients(soft, 50, 40)];
  assert.ok(mid[0] > 0 && mid[1] > 0 && Math.abs(mid[0] + mid[1] + mid[2] - 1) < 1e-12, "a feathered knee blends hip and knee and sums to 1");
});

test("a child's chain transform composes after its parent's", () => {
  const { deform } = companion();
  const bones = deform.parseBones({ bones: {
    hip: { pivot: [0, 0], region: everything },
    knee: { pivot: [10, 0], parent: "hip", region: everything }
  } });
  const locals = Float64Array.from([0, 0, Math.PI / 2, 0, 0, -Math.PI / 2]), out = new Float64Array(12);
  deform.chain(bones, locals, out);
  const apply = (o, x, y) => [out[o] * x + out[o + 2] * y + out[o + 4], out[o + 1] * x + out[o + 3] * y + out[o + 5]];
  const [hx, hy] = apply(0, 20, 0), [kx, ky] = apply(6, 20, 0);
  near(hx, 0, "hip x"); near(hy, 20, "hip rotates (20, 0) a quarter turn");
  // The knee turns the point -90° about (10, 0) to (10, -10); the hip then
  // turns that to (10, 10).
  near(kx, 10, "knee chain x"); near(ky, 10, "knee chain y");
});

test("untrusted deform input is bounded: grid clamp, bone cap, short regions, unknown parents and cycles", () => {
  const { deform } = companion();
  assert.deepEqual([...deform.gridSize({})], [12, 16]);
  assert.deepEqual([...deform.gridSize({ grid: [1000, 0] })], [32, 2]);
  assert.deepEqual([...deform.gridSize({ grid: [4, "x"] })], [4, 16]);
  assert.deepEqual([...deform.gridSize({ grid: [5] })], [12, 16]);
  const region = everything, many = {};
  for (let i = 0; i < 40; i++) many["b" + i] = { pivot: [0, 0], region };
  assert.equal(deform.parseBones({ bones: many }).length, 16, "at most 16 bones");
  const bones = deform.parseBones({ bones: {
    short: { pivot: [0, 0], region: [[0, 0], [1, 1]] },
    nopivot: { region },
    orphan: { pivot: [0, 0], region, parent: "ghost" },
    loopA: { pivot: [0, 0], region, parent: "loopB" },
    loopB: { pivot: [0, 0], region, parent: "loopA" },
    loopChild: { pivot: [0, 0], region, parent: "loopA" },
    ok: { pivot: [1, 2], region }
  } });
  assert.deepEqual([...bones.map(bone => bone.id)].sort(), ["ok", "orphan"], "an unknown parent is top-level; cycles and bad bones drop");
  assert.equal(bones.find(bone => bone.id === "orphan").parent, -1);
  assert.equal(deform.parseBones({ bones: { a: { pivot: [0, 0], region: [[0, 0], [1, 1], [NaN, 2]] } } }).length, 0, "a non-finite point leaves too few points");
  assert.deepEqual([...deform.parseBones(null)], []);
});

const whole = { grid: [2, 2], bones: { b: { pivot: [8, 8], region: everything, motion: hold(90) } } };

test("a deformed column draws one clipped affine drawImage per triangle that maps the rest triangle onto the deformed one", () => {
  const rec = recorder();
  const view = rig(deformPack(whole), rec).view("stand", null, "idle", []);
  rec.clear();
  view.render(0, gaze);
  const calls = rec.composite();
  const names = calls.map(call => call.op);
  assert.equal(names.filter(op => op === "transform").length, 8, "a 2 x 2 grid is 8 triangles");
  assert.equal(names.filter(op => op === "setTransform").length, 0);
  assert.equal(names.filter(op => op === "clip").length, 8, "no static cells, so one clip per triangle");
  // Rotating 90° about (8, 8): x' = 16 - y, y' = x for every triangle.
  for (const call of calls.filter(entry => entry.op === "transform")) {
    const [a, b, c, d, e, f] = call.args;
    near(a, 0, "a"); near(b, 1, "b"); near(c, -1, "c"); near(d, 0, "d"); near(e, 16, "e"); near(f, 0, "f");
  }
  // A clip path is the deformed triangle grown by 0.5 px: its inradius is the
  // rotated rest triangle's (8, 8, 8 sqrt 2 legs) plus 0.5.
  const first = calls.findIndex(entry => entry.op === "moveTo");
  const pts = [calls[first].args, calls[first + 1].args, calls[first + 2].args];
  const len = (p, q) => Math.hypot(p[0] - q[0], p[1] - q[1]);
  const [la, lb, lc] = [len(pts[1], pts[2]), len(pts[0], pts[2]), len(pts[0], pts[1])];
  const area = Math.abs((pts[1][0] - pts[0][0]) * (pts[2][1] - pts[0][1]) - (pts[2][0] - pts[0][0]) * (pts[1][1] - pts[0][1])) / 2;
  near(area / ((la + lb + lc) / 2), (16 - 8 * Math.SQRT2) / 2 + 0.5, "clip inradius");
  const draws = calls.filter(entry => entry.op === "drawImage" && entry.args.length === 8);
  assert.equal(draws.length, 8, "each triangle draws a sub-rectangle of the composed canvas");
  assert.ok(draws.every(entry => rec.canvases.includes(entry.image) && entry.image.index > 0), "from a composed canvas, not the composite");
});

test("every bone at rest draws the column rigidly with no mesh calls", () => {
  const rec = recorder();
  const view = rig(deformPack({ grid: [2, 2], bones: { b: { pivot: [8, 8], region: everything, motion: hold(0) },
    idle: { pivot: [0, 0], region: everything } } }), rec).view("stand", null, "idle", []);
  rec.clear();
  view.render(0, gaze);
  const ops = rec.composite().map(call => call.op);
  for (const op of ["transform", "setTransform", "clip", "beginPath", "rect", "moveTo"]) assert.ok(!ops.includes(op), op);
  const composed = rec.composite().filter(entry => entry.op === "drawImage" && entry.image?.index > 0 && entry.args.length === 2);
  assert.equal(composed.length, 1, "one plain drawImage of the composed column");
});

test("static cells draw in one rigid clip and fully transparent cells draw nothing", () => {
  const rec = recorder((x, y) => !(x >= 8 && y >= 8));
  const view = rig(deformPack({ grid: [4, 2], bones: { b: { pivot: [8, 8], region: [[7, -100], [200, -100], [200, 200], [7, 200]], feather: 0, motion: hold(10) } } }), rec)
    .view("stand", null, "idle", []);
  rec.clear();
  view.render(0, gaze);
  const calls = rec.composite();
  const rects = calls.filter(entry => entry.op === "rect").map(entry => entry.args);
  assert.deepEqual(rects, [[-0.5, -0.5, 5, 9], [-0.5, 7.5, 5, 9]], "the still left column, one run per row, widened by 0.5 px");
  // The bottom-right quadrant is transparent, so the moving cells are columns
  // 1-3 of the top row and column 1 of the bottom row.
  const cells = calls.filter(entry => entry.op === "transform").length / 2;
  assert.equal(cells, 4, "transparent cells draw nothing");
});

test("static layers share one composed canvas at their lowest z while motion layers draw rigidly at their own z", () => {
  const rec = recorder();
  const view = rig(deformPack(whole), rec).view("stand", null, "idle", []);
  rec.clear();
  view.render(0, gaze);
  const draws = rec.composite().filter(entry => entry.op === "drawImage");
  const firstMesh = draws.findIndex(entry => entry.args.length === 8);
  const tail = draws.findIndex(entry => entry.image.id === "tail.png");
  assert.ok(firstMesh >= 0 && tail > firstMesh, "the mesh draws below the z 25 tail");
  assert.ok(!draws.some(entry => entry.image.id === "body.png"), "the static body reaches the composite only through the mesh");
});

test("the composed canvas is cached per outfit, column and tint", () => {
  const rec = recorder();
  const r = rig(deformPack(whole), rec);
  const composed = () => rec.log.filter(entry => entry.op === "drawImage" && entry.image.id === "body.png").length;
  const stand = r.view("stand", null, "idle", []);
  stand.render(0, gaze); stand.render(0.5, gaze);
  r.view("stand", null, "sleepy", []).render(0, gaze);
  assert.equal(composed(), 1, "re-renders and another expression share the composed canvas");
  r.setWardrobe({ palette: "skin=#112233" });
  r.view("stand", null, "idle", []).render(0, gaze);
  assert.equal(composed(), 2, "a tint change recomposes");
  r.setWardrobe({ outfit: "other" });
  r.view("stand", null, "idle", []).render(0, gaze);
  assert.equal(composed(), 3, "an outfit change recomposes");
});

test("the head anchor is displaced by the deform field at the column's head anchor", () => {
  const lift = { oscillate: { ty: [-4, -4], hz: 1 } };
  const headTranslate = (deform, expected) => {
    const rec = recorder();
    const view = rig(deformPack(deform), rec).view("stand", null, "idle", []);
    rec.clear();
    view.render(0, gaze);
    const calls = rec.composite().filter(entry => entry.op === "translate").map(entry => entry.args);
    assert.ok(calls.some(([x, y]) => x === 8 && Math.abs(y - expected) < 1e-9), `head translate to y ${expected}: ${JSON.stringify(calls)}`);
  };
  headTranslate({ grid: [2, 2], bones: { breath: { pivot: [8, 8], region: everything, motion: lift } } }, 0);
  headTranslate({ grid: [2, 2], bones: { breath: { pivot: [8, 8], region: [[0, 0], [16, 0], [16, 8], [0, 8]], motion: lift } } }, 0);
  headTranslate({ grid: [2, 2], bones: { hand: { pivot: [8, 8], region: [[0, 10], [16, 10], [16, 16], [0, 16]], feather: 0, motion: lift } } }, 4);
});

test("deform.roll rotates the composed stack about the head anchor by rotDeg only", () => {
  const roll = { motion: { oscillate: { rotDeg: [2, 2], tx: [5, 5], hz: 1 } } };
  const rec = recorder();
  const { RAD } = companion();
  const view = rig(deformPack({ roll }), rec).view("stand", null, "idle", []);
  rec.clear();
  view.render(0, gaze);
  const calls = rec.composite();
  const turn = calls.findIndex(entry => entry.op === "rotate");
  assert.deepEqual([...calls[turn - 1].args], [8, 4], "pivot defaults to the head anchor");
  near(calls[turn].args[0], 2 * RAD, "rotDeg");
  assert.deepEqual([...calls[turn + 1].args], [-8, -4]);
  assert.ok(!calls.some(entry => entry.op === "translate" && entry.args[0] === 13), "tx is ignored");
  assert.ok(!calls.some(entry => entry.op === "transform" || entry.op === "clip"), "roll alone runs no mesh pass");
  const pivoted = recorder();
  rig(deformPack({ roll: { pivot: [3, 5], motion: roll.motion } }), pivoted).view("stand", null, "idle", []).render(0, gaze);
  assert.deepEqual([...pivoted.composite().find(entry => entry.op === "translate").args], [3, 5], "an explicit pivot wins");
});

test("roll composes with the mesh pass through transform so the rotation survives", () => {
  const rec = recorder();
  const view = rig(deformPack({ ...whole, roll: { motion: hold(2) } }), rec).view("stand", null, "idle", []);
  rec.clear();
  view.render(0, gaze);
  const ops = rec.composite().map(entry => entry.op);
  assert.ok(ops.indexOf("rotate") < ops.indexOf("clip"), "the mesh draws inside the roll");
  assert.ok(!ops.includes("setTransform"), "setTransform would discard the roll");
});

test("deform is read from outfit columns only and is never inherited from the registry column", () => {
  const rec = recorder();
  const pack = deformPack(undefined);
  pack.manifest.poses.stand.deform = whole;
  rig(pack, rec).view("stand", null, "idle", []).render(0, gaze);
  assert.ok(!rec.log.some(entry => ["transform", "clip"].includes(entry.op)), "a registry deform never meshes");
  rec.clear();
  const legacy = deformPack(whole);
  delete legacy.manifest.outfits; delete legacy.manifest.defaultOutfit;
  legacy.manifest.poses.stand.deform = whole;
  rig(legacy, rec).view("stand", null, "idle", []).render(0, gaze);
  assert.ok(!rec.log.some(entry => ["transform", "clip"].includes(entry.op)), "a legacy stack never meshes");
});

test("a gesture column meshes its own static layers under its own deform and removes replaced pose layers rigidly", () => {
  const rec = recorder();
  const pack = deformPack(whole);
  pack.manifest.outfits.street.poses.stand.layers.splice(1, 1, { id: "arm", file: "arm.png" });
  pack.manifest.gestures.wave = { replaces: ["arm"] };
  pack.manifest.outfits.street.gestures = { wave: { replaces: ["arm"], deform: whole, layers: [{ file: "hand.png" }] } };
  const r = rig(pack, rec);
  const view = r.view("stand", "wave", "idle", []);
  const composed = id => rec.log.filter(entry => entry.op === "drawImage" && entry.image?.id === id).length;
  assert.equal(composed("arm.png"), 0, "the replaced layer is removed");
  assert.equal(composed("hand.png"), 1, "the gesture's own layer composes under its own deform");
  assert.equal(composed("body.png"), 1, "the pose column's remaining static layer composes under the pose deform");
  rec.clear();
  view.render(0, gaze);
  assert.equal(rec.composite().filter(entry => entry.op === "transform").length, 16, "the pose and gesture groups each mesh 8 triangles");
});

test("an unusable deform leaves every layer rigid", () => {
  for (const deform of [{}, { bones: {} }, { bones: { a: { pivot: [0, 0], region: [[0, 0]] } } }, "bad", [], null]) {
    const rec = recorder();
    rig(deformPack(deform), rec).view("stand", null, "idle", []).render(0, gaze);
    assert.ok(!rec.log.some(entry => ["transform", "clip"].includes(entry.op)), JSON.stringify(deform));
    assert.ok(rec.log.some(entry => entry.op === "drawImage" && entry.image.id === "body.png"), "the body still draws rigidly");
  }
});

test("a bone with swingWeight makes the column articulated and follows the drag swing", () => {
  const rec = recorder();
  const view = rig(deformPack({ grid: [2, 2], bones: { b: { pivot: [8, 8], region: everything, swingWeight: 1 } } }), rec).view("stand", null, "idle", []);
  assert.equal(view.articulatedDrag, true);
  rec.clear();
  view.render(0, gaze);
  assert.ok(!rec.log.some(entry => entry.op === "transform"), "no swing, no mesh");
  view.render(0, gaze, { swing: Math.PI / 2, swingVel: 0 });
  assert.equal(rec.log.filter(entry => entry.op === "transform").length, 8);
});

// The shared synthetic fixture column (also captured in headless Chromium):
// a two-bone chain per leg, a breath bone with pinned hands and a roll.
test("the synthetic dangle fixture chains hip and knee, pins the hands under the breath and rides the head", () => {
  const { deformFixture } = require("./testdata/companion-deform-fixture.cjs");
  const { deform } = companion();
  const rec = recorder();
  const make = (w, h) => new rec.Canvas(w, h);
  const { manifest, bitmaps } = deformFixture(make);
  const spec = manifest.outfits.plain.poses.dangle.deform;
  const bones = deform.parseBones(spec), ids = [...bones.map(bone => bone.id)];
  assert.equal(ids.indexOf("hipL") < ids.indexOf("kneeL"), true, "the hip precedes its knee");
  const at = (x, y) => Object.fromEntries([...ids, "rest"].map((id, i) => [id, +deform.coefficients(bones, x, y)[i].toFixed(6)]));
  const shin = at(236, 450), thigh = at(236, 370), hand = at(200, 270), shoulder = at(256, 190);
  assert.deepEqual([shin.hipL, shin.kneeL], [0, 1], "a shin vertex takes the full knee chain");
  assert.ok(thigh.hipL > 0.99 && thigh.kneeL < 0.01, "a thigh vertex rides the hip only");
  assert.ok(hand.breath === 0 && hand.rest === 1, "hands lie outside the breath falloff, so they are pinned");
  assert.ok(shoulder.breath === 1, "the shoulder carries the whole breath");
  assert.deepEqual([spec.roll.pivot[0], spec.roll.pivot[1]], [256, 150], "roll pivots at the neck");

  // Breath only: the head anchor (10 px above the breath region, half a
  // feather away) rises by half the lift, and nothing else moves.
  const lift = deformFixture(make, { legs: 0, roll: 0, breath: 4 });
  rec.mark();
  const view = companion({ OffscreenCanvas: rec.Canvas }).makeRig(lift, null, "").view("dangle", null, "idle", []);
  rec.clear();
  view.render(0.5, gaze);
  const ys = rec.composite().filter(entry => entry.op === "translate").map(entry => entry.args[1]);
  assert.ok(ys.some(y => Math.abs(y - 148) < 1e-6), `the head rides the breath by half the lift: ${JSON.stringify(ys)}`);
  assert.ok(bitmaps.size >= 4);
});
