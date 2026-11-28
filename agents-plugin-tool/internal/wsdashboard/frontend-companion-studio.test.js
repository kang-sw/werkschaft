const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");
const { pathToFileURL } = require("node:url");

// Phase 2 (companion visual polish): finger presets, palm frame handedness,
// held-phone palm grip, theme-adaptive studio lighting, and bust framing solver.

function loadCompanion(globals = {}) {
  const context = vm.createContext({
    window: {},
    URLSearchParams,
    AbortController,
    setTimeout,
    clearTimeout,
    performance: { now: () => 0 },
    ...globals
  });
  context.window = context.window || {};
  for (const name of ["builtins", "packs", "focus", "deform", "compositor", "scenarios", "menu", "vrm-fx", "vrm-studio", "vrm-props", "vrm-pose", "vrm-colead", "vrm-speech", "vrm", "stage"]) {
    vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", `dashboard-companion-${name}.js`), "utf8"), context);
  }
  return { C: context.window.WsCompanion, context };
}

let runtimePromise = null;
function loadThree() {
  runtimePromise ||= import(pathToFileURL(path.join(__dirname, "assets", "companion-vrm-runtime.mjs")).href);
  return runtimePromise.then(rt => rt.THREE);
}

function makeFakeElement(tag, doc) {
  const el = {
    tagName: tag.toUpperCase(),
    className: "",
    children: [],
    dataset: {},
    attrs: {},
    listeners: {},
    width: 0,
    height: 0,
    offsetWidth: 160,
    offsetHeight: 32,
    classList: { add() {}, remove() {}, contains() { return false; }, toggle() {} },
    ownerDocument: doc,
    style: { setProperty(k, v) { this[k] = v; }, getPropertyValue(k) { return this[k] || ""; }, removeProperty(k) { delete this[k]; } },
    setAttribute(k, v) { this.attrs[k] = String(v); },
    getAttribute(k) { return this.attrs[k] ?? null; },
    hasAttribute(k) { return Object.hasOwn(this.attrs, k); },
    removeAttribute(k) { delete this.attrs[k]; },
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); },
    removeEventListener() {},
    dispatchEvent() {},
    append(...items) { this.children.push(...items); },
    prepend(...items) { this.children.unshift(...items); },
    insertBefore(item) { this.children.push(item); },
    replaceChildren(...items) { this.children = [...items]; },
    remove() { this.removed = true; },
    getContext(kind) {
      if (kind !== "2d") return null;
      return { canvas: this, clearRect() {}, drawImage() {}, setTransform() {}, save() {}, restore() {}, translate() {}, rotate() {}, beginPath() {}, closePath() {}, moveTo() {}, lineTo() {}, arc() {}, ellipse() {}, stroke() {}, fill() {}, getImageData: () => ({ data: [] }) };
    },
    getBoundingClientRect() { return { left: 0, top: 0, right: this.offsetWidth, bottom: this.offsetHeight, width: this.offsetWidth, height: this.offsetHeight }; },
    contains(other) { return other === this; },
    querySelector() { return null; },
    querySelectorAll() { return []; },
    focus() {}
  };
  return el;
}

// Bust skeleton in VRM 1.0 or VRM 0.x layout. VRM 0.x models are authored facing
// -Z (character-left at local -X) and rotated by PI around Y (VRMUtils.rotateVRM0),
// so local X and Z are negated while world positions match VRM 1.0 exactly.
function makeBustVrm(THREE, metaVersion, { fingers = true } = {}) {
  const root = new THREE.Group();
  const is0 = metaVersion === "0";
  if (is0) root.rotation.y = Math.PI;
  const xs = is0 ? -1 : 1;
  const zs = is0 ? -1 : 1;
  const bones = new Map();
  const addBone = (name, parent, x, y, z) => {
    const n = new THREE.Object3D();
    n.name = name;
    n.position.set(x * xs, y, z * zs);
    parent.add(n);
    bones.set(name, n);
    return n;
  };
  const hips = addBone("hips", root, 0, 0.90, 0);
  const spine = addBone("spine", hips, 0, 0.10, 0);
  const chest = addBone("chest", spine, 0, 0.15, 0);
  const upperChest = addBone("upperChest", chest, 0, 0.10, 0);
  const neck = addBone("neck", upperChest, 0, 0.10, 0);
  const head = addBone("head", neck, 0, 0.08, 0);
  for (const side of ["left", "right"]) {
    const s = side === "left" ? 1 : -1;
    addBone(side + "Shoulder", upperChest, s * 0.06, 0.04, 0);
    const u = addBone(side + "UpperArm", upperChest, s * 0.16, 0.04, 0);
    const l = addBone(side + "LowerArm", u, s * 0.22, 0, 0);
    const hand = addBone(side + "Hand", l, s * 0.20, 0, 0);
    if (!fingers) continue;
    // Thumb toward +Z (front), little finger toward -Z (back) in T-pose.
    const chain = (digit, segs, base, step) => {
      let parent = hand;
      segs.forEach((seg, i) => {
        const p = i === 0 ? base : step;
        parent = addBone(side + digit + seg, parent, s * p[0], p[1], p[2]);
      });
    };
    chain("Thumb", ["Metacarpal", "Proximal", "Distal"], [0.02, -0.005, 0.025], [0.025, 0, 0.012]);
    chain("Index", ["Proximal", "Intermediate", "Distal"], [0.08, 0, 0.022], [0.032, 0, 0]);
    chain("Middle", ["Proximal", "Intermediate", "Distal"], [0.085, 0, 0.004], [0.034, 0, 0]);
    chain("Ring", ["Proximal", "Intermediate", "Distal"], [0.08, 0, -0.014], [0.031, 0, 0]);
    chain("Little", ["Proximal", "Intermediate", "Distal"], [0.072, 0, -0.03], [0.025, 0, 0]);
  }
  const torsoMesh = new THREE.Mesh(new THREE.BoxGeometry(0.28, 0.34, 0.18), new THREE.MeshBasicMaterial());
  chest.add(torsoMesh);
  const headMesh = new THREE.Mesh(new THREE.BoxGeometry(0.20, 0.24, 0.20), new THREE.MeshBasicMaterial());
  headMesh.position.set(0, 0.10, 0);
  head.add(headMesh);
  root.updateMatrixWorld(true);
  const vals = new Map();
  const presets = is0
    ? { neutral: {}, blink: {}, fun: {}, angry: {}, sorrow: {}, joy: {}, o: {} }
    : { neutral: {}, blink: {}, happy: {}, sad: {}, angry: {}, relaxed: {}, surprised: {} };
  return {
    meta: { metaVersion },
    scene: root,
    bones,
    humanoid: { getNormalizedBoneNode: name => bones.get(name) || null },
    expressionManager: {
      presetExpressionMap: presets,
      customExpressionMap: {},
      expressions: Object.keys(presets).map(expressionName => ({ expressionName })),
      setValue: (k, v) => vals.set(k, v),
      getValue: k => vals.get(k) ?? 0
    },
    lookAt: { target: null },
    update() { root.updateMatrixWorld(true); }
  };
}

async function mountRig(THREE, vrmObj, size = { width: 300, height: 245, dpr: 1 }) {
  let capturedScene = null;
  class StubRenderer {
    constructor() { this.shadowMap = { enabled: true }; this.info = { render: { calls: 2, triangles: 200 } }; }
    setClearColor() {}
    setPixelRatio() {}
    setSize() {}
    render(sc) { capturedScene = sc; }
    dispose() {}
    forceContextLoss() {}
  }
  const stubRuntime = {
    THREE: { ...THREE, WebGLRenderer: StubRenderer },
    GLTFLoader: class {
      register() {}
      async parseAsync() { return { scene: vrmObj.scene, userData: { vrm: vrmObj } }; }
    },
    VRMLoaderPlugin: class {},
    VRMAnimationLoaderPlugin: class {},
    VRMUtils: { removeUnnecessaryVertices() {}, combineSkeletons() {}, rotateVRM0() {}, deepDispose() {} },
    createVRMAnimationClip: () => ({})
  };
  let now = 0;
  const doc = { createElement: tag => makeFakeElement(tag, doc), addEventListener() {} };
  const { C } = loadCompanion({
    document: doc,
    window: { innerWidth: 1680, innerHeight: 960, devicePixelRatio: 1, addEventListener() {} },
    performance: { now: () => now },
    fetch: async () => ({ ok: true, status: 200, arrayBuffer: async () => new Uint8Array([1, 2, 3, 4]).buffer })
  });
  C.vrmRuntimeLoader = async () => stubRuntime;
  const rig = C.makeVrmRig({ id: `vrm-${vrmObj.meta.metaVersion}`, rev: "r1", renderer: "vrm", modelFile: "avatar.vrm", manifest: {}, expressionMap: {}, animations: {} });
  await rig.mount(makeFakeElement("div", doc), size);
  const view = rig.view("stand", "", "idle", []);
  const step = (seconds, fps = 30) => {
    for (let i = 0; i < Math.round(seconds * fps); i++) {
      now += 1000 / fps;
      view.render(now / 1000, { x: 0, y: 0 }, null, { progress: 0.5 });
    }
  };
  return { C, rig, step, scene: () => capturedScene };
}

const dot = (a, b) => a.x * b.x + a.y * b.y + a.z * b.z;
const sub = (a, b) => ({ x: a.x - b.x, y: a.y - b.y, z: a.z - b.z });
const cross = (a, b) => ({ x: a.y * b.z - a.z * b.y, y: a.z * b.x - a.x * b.z, z: a.x * b.y - a.y * b.x });

test("Phase 2 finger presets: 30 bones, all presets cover every bone, left/right mirror, zero-alloc reuse", () => {
  const { C } = loadCompanion();
  const P = C.vrmCoLeadPose;
  assert.equal(P.FINGER_BONES.length, 30);
  assert.equal(new Set(P.FINGER_BONES).size, 30);
  assert.deepEqual(Object.keys(P.FINGER_PRESETS).sort(), ["phone-grip", "point", "relaxed", "typing"]);
  for (const preset of Object.keys(P.FINGER_PRESETS)) {
    const out = P.computeFingerTargets({ left: { preset }, right: { preset } }, {});
    for (const bone of P.FINGER_BONES) {
      assert.ok(Array.isArray(out[bone]) && out[bone].length === 3, `${preset}: ${bone} target missing`);
      assert.ok(out[bone].every(Number.isFinite), `${preset}: ${bone} target not finite`);
    }
    // Right hand mirrors the left across the YZ plane: [x, -y, -z].
    for (const bone of P.FINGER_BONES.filter(b => b.startsWith("left"))) {
      const l = out[bone], r = out["right" + bone.slice(4)];
      assert.ok(Math.abs(l[0] - r[0]) < 1e-9 && Math.abs(l[1] + r[1]) < 1e-9 && Math.abs(l[2] + r[2]) < 1e-9, `${preset}: ${bone} mirror mismatch`);
    }
    // Left curls are -Z (fold toward the palm); point keeps the index nearly straight.
    assert.ok(out.leftMiddleProximal[2] < 0, `${preset}: left middle must curl -Z`);
  }
  const grip = P.computeFingerTargets({ left: { preset: "phone-grip" } }, {});
  const point = P.computeFingerTargets({ left: { preset: "point" } }, {});
  const relaxed = P.computeFingerTargets({}, {});
  assert.ok(grip.leftLittleProximal[2] < relaxed.leftLittleProximal[2] - 0.4, "phone-grip wraps the little finger well past relaxed");
  assert.ok(Math.abs(point.leftIndexProximal[2]) < 0.1, "point keeps the index extended");
  assert.ok(point.leftMiddleProximal[2] < -1.0, "point folds the middle finger");

  // Blend weights interpolate monotonically and reuse caller-owned arrays.
  const out = {};
  P.computeFingerTargets({ left: { typing: 0 } }, out);
  const arr = out.leftIndexProximal;
  const z0 = arr[2];
  P.computeFingerTargets({ left: { typing: 1 } }, out);
  assert.equal(out.leftIndexProximal, arr, "finger target arrays must be reused (zero allocation)");
  assert.ok(arr[2] < z0, "typing curls the index more than relaxed");

  // computeCoLeadTargets routes the 30 finger targets into its boneTargets.
  const fingerOut = {};
  const res = P.computeCoLeadTargets({ elapsed: 1, dt: 1 / 30, fingerOut, fingerPreset: "point" });
  const targets = res?.boneTargets || res;
  assert.ok(P.FINGER_BONES.every(b => Array.isArray(targets[b])), "co-lead targets must include all 30 finger bones");
  assert.ok(targets.leftMiddleProximal[2] < -1.0, "params.fingerPreset forces the preset");
});

test("Phase 2 palm frame: orthonormal right-handed basis with identical world normal on VRM 0.x and VRM 1.0 skeletons", async () => {
  const THREE = await loadThree();
  const { C } = loadCompanion();
  const P = C.vrmCoLeadPose;
  const S = C.vrmStudio;
  const frames = {};
  for (const ver of ["1", "0"]) {
    const vrm = makeBustVrm(THREE, ver);
    for (const side of ["left", "right"]) {
      const probe = S.createPalmProbe(THREE, side);
      assert.equal(S.samplePalmProbe(vrm.humanoid, probe), true);
      assert.equal(probe.hasFingers, true);
      assert.equal(probe.hasHead, true);
      const f = {};
      assert.equal(P.computePalmFrame(probe.wrist, probe.index, probe.middle, probe.little, side, f), true);
      for (const k of ["palmX", "palmY", "palmZ"]) assert.ok(Math.abs(Math.hypot(f[k].x, f[k].y, f[k].z) - 1) < 1e-6, `${k} unit`);
      assert.ok(Math.abs(dot(f.palmX, f.palmY)) < 1e-6 && Math.abs(dot(f.palmY, f.palmZ)) < 1e-6 && Math.abs(dot(f.palmZ, f.palmX)) < 1e-6, "orthogonal");
      const det = dot(cross(f.palmX, f.palmY), f.palmZ);
      assert.ok(Math.abs(det - 1) < 1e-6, `VRM ${ver} ${side}: basis must be right-handed (det=${det})`);
      // T-pose palms face the floor on both hands; palmY points out along the arm.
      assert.ok(f.palmZ.y < -0.99, `VRM ${ver} ${side}: T-pose palm normal must point down (got ${f.palmZ.y.toFixed(3)})`);
      assert.ok((side === "left" ? f.palmY.x : -f.palmY.x) > 0.99, `VRM ${ver} ${side}: palmY runs out along the arm`);
      frames[`${ver}-${side}`] = f;
    }
  }
  for (const side of ["left", "right"]) {
    const a = frames[`1-${side}`], b = frames[`0-${side}`];
    for (const k of ["palmX", "palmY", "palmZ", "center"]) {
      assert.ok(Math.hypot(a[k].x - b[k].x, a[k].y - b[k].y, a[k].z - b[k].z) < 1e-6, `${side} ${k} must match across VRM 0.x / 1.0`);
    }
  }
  // Degenerate input is rejected without throwing.
  const z = { x: 0, y: 0, z: 0 };
  assert.equal(P.computePalmFrame(z, z, z, z, "left", {}), false);
  assert.equal(P.computePalmFrame(null, z, z, z, "left", {}), false);
  assert.equal(P.computePalmFrame({ x: NaN, y: 0, z: 0 }, z, z, z, "left", {}), false);
  // Missing finger bones: probe falls back to the wrist anchor (hasFingers=false).
  const bare = makeBustVrm(THREE, "1", { fingers: false });
  const probe = S.createPalmProbe(THREE, "left");
  assert.equal(S.samplePalmProbe(bare.humanoid, probe), true);
  assert.equal(probe.hasFingers, false);
  assert.ok(probe.anchor.distanceTo(probe.wrist) < 1e-9);
});

test("Phase 2 finger presets conjugate for VRM 0.x: fingertips curl toward the palm identically on both skeletons", async () => {
  const THREE = await loadThree();
  const { C } = loadCompanion();
  const P = C.vrmCoLeadPose;
  const conj = C.vrmMath.conjugateVrm0Euler;
  const tips = {};
  for (const ver of ["1", "0"]) {
    const vrm = makeBustVrm(THREE, ver);
    const targets = P.computeFingerTargets({ left: { preset: "phone-grip" }, right: { preset: "typing" } }, {});
    for (const bone of P.FINGER_BONES) {
      const [x, y, z] = conj(...targets[bone], ver === "0");
      vrm.bones.get(bone).rotation.set(x, y, z, "XYZ");
    }
    vrm.scene.updateMatrixWorld(true);
    const w = name => vrm.bones.get(name).getWorldPosition(new THREE.Vector3());
    for (const side of ["left", "right"]) {
      const f = {};
      assert.equal(P.computePalmFrame(w(side + "Hand"), w(side + "IndexProximal"), w(side + "MiddleProximal"), w(side + "LittleProximal"), side, f), true);
      for (const digit of ["Index", "Middle", "Ring", "Little"]) {
        const d = sub(w(side + digit + "Distal"), w(side + digit + "Proximal"));
        assert.ok(dot(d, f.palmZ) > 0.005, `VRM ${ver} ${side} ${digit}: distal must curl toward the palm side (got ${dot(d, f.palmZ).toFixed(4)})`);
        tips[`${ver}-${side}-${digit}`] = w(side + digit + "Distal");
      }
    }
  }
  for (const key of Object.keys(tips).filter(k => k.startsWith("1-"))) {
    const a = tips[key], b = tips["0-" + key.slice(2)];
    assert.ok(a.distanceTo(b) < 1e-6, `${key}: VRM 0.x fingertip must match VRM 1.0 after conjugation`);
  }
});

test("Phase 2 rig: finger bones are driven when present and missing finger bones are a silent no-op", async () => {
  const THREE = await loadThree();
  for (const ver of ["1", "0"]) {
    const vrm = makeBustVrm(THREE, ver);
    const { rig, step } = await mountRig(THREE, vrm);
    assert.equal(rig.debugState().fingerBoneCount, 30, `VRM ${ver}: all 30 finger bones detected`);
    step(1.2);
    const idx = vrm.bones.get("leftIndexProximal").rotation;
    assert.ok(Math.abs(idx.z) > 0.05, `VRM ${ver}: co-lead typing must curl the left index (z=${idx.z.toFixed(3)})`);
    assert.ok(ver === "1" ? idx.z < 0 : idx.z > 0, `VRM ${ver}: left curl sign must follow VRM ${ver} conjugation`);
    rig.dispose();
  }
  const bare = makeBustVrm(THREE, "1", { fingers: false });
  const { rig, step, C } = await mountRig(THREE, bare);
  assert.equal(rig.debugState().fingerBoneCount, 0);
  C.setCoLeadBeat("phone-typing");
  assert.doesNotThrow(() => step(1.0));
  const co = rig.debugState().coLead;
  assert.equal(co.palmGrip.mode, "anchor", "rigs without finger bones keep the legacy knuckle anchor");
  rig.dispose();
});

test("Phase 2 held phone: phone screen normal is flush with the palm normal and invariant across VRM 0.x / 1.0", async () => {
  const THREE = await loadThree();
  const quats = {};
  for (const ver of ["1", "0"]) {
    const vrm = makeBustVrm(THREE, ver);
    const { rig, step, C, scene } = await mountRig(THREE, vrm);
    C.setCoLeadBeat("phone-typing");
    step(1.5);
    const st = rig.debugState();
    const grip = st.coLead.palmGrip;
    assert.equal(grip.mode, "palm", `VRM ${ver}: held phone must use the palm frame`);
    assert.ok(grip.weight > 0.99, `VRM ${ver}: hold weight settles at 1`);
    const phone = scene().children.find(c => c.name === "ws-colead-phone");
    assert.ok(phone, "phone group in scene");
    phone.updateMatrixWorld(true);
    const q = phone.getWorldQuaternion(new THREE.Quaternion());
    const screenN = new THREE.Vector3(0, 0, 1).applyQuaternion(q);
    // Recompute the palm normal from the live skeleton.
    const S = C.vrmStudio;
    const probe = S.createPalmProbe(THREE, "left");
    S.samplePalmProbe(vrm.humanoid, probe);
    const f = {};
    assert.equal(C.vrmCoLeadPose.computePalmFrame(probe.wrist, probe.index, probe.middle, probe.little, "left", f), true);
    assert.ok(dot(screenN, f.palmZ) > 0.995, `VRM ${ver}: phone +Z must equal palm normal (dot=${dot(screenN, f.palmZ).toFixed(4)})`);
    const longAxis = new THREE.Vector3(0, 1, 0).applyQuaternion(q);
    assert.ok(dot(longAxis, f.palmY) > 0.99, `VRM ${ver}: phone long axis runs along palmY`);
    // Phone sits on the palm side, not inside or behind the hand.
    const pos = phone.getWorldPosition(new THREE.Vector3());
    const off = sub(pos, f.center);
    assert.ok(dot(off, f.palmZ) > 0, `VRM ${ver}: phone must sit in front of the palm surface`);
    assert.ok(Math.hypot(off.x, off.y, off.z) < 0.12, `VRM ${ver}: phone stays in the hand`);
    quats[ver] = q;
    rig.dispose();
  }
  assert.ok(Math.abs(quats["1"].dot(quats["0"])) > 0.999, "held phone orientation must be identical on VRM 0.x and VRM 1.0");
});

function makeThemeEnv(initialAttr, prefersDark) {
  const attrs = initialAttr ? { "data-theme": initialAttr } : {};
  const observers = [];
  const mqListeners = new Set();
  const mq = {
    matches: prefersDark,
    addEventListener(type, fn) { if (type === "change") mqListeners.add(fn); },
    removeEventListener(type, fn) { mqListeners.delete(fn); }
  };
  const doc = {
    documentElement: {
      getAttribute: k => attrs[k] ?? null,
      dataset: {}
    }
  };
  class FakeMO {
    constructor(cb) { this.cb = cb; this.active = false; observers.push(this); }
    observe(target, opts) { this.active = true; this.target = target; this.opts = opts; }
    disconnect() { this.active = false; }
  }
  const win = { matchMedia: () => mq, MutationObserver: FakeMO };
  return {
    doc,
    win,
    setAttr(v) {
      if (v == null) delete attrs["data-theme"]; else attrs["data-theme"] = v;
      for (const o of observers) if (o.active) o.cb([{ attributeName: "data-theme" }]);
    },
    setPrefersDark(v) { mq.matches = v; for (const fn of mqListeners) fn({ matches: v }); },
    observers,
    mqListeners
  };
}

test("Phase 2 studio lighting: Dark/Light preset selection, live theme following, prop-only env, clean dispose", async () => {
  const THREE = await loadThree();
  const { C } = loadCompanion();
  const S = C.vrmStudio;

  assert.equal(S.resolveThemeMode(makeThemeEnv("light", true).doc, makeThemeEnv("light", true).win), "light", "data-theme wins over media");
  assert.equal(S.resolveThemeMode(makeThemeEnv("dark", false).doc, makeThemeEnv("dark", false).win), "dark");
  const media = makeThemeEnv(null, false);
  assert.equal(S.resolveThemeMode(media.doc, media.win), "light", "prefers-color-scheme light");
  assert.equal(S.resolveThemeMode(null, null), "dark", "defaults to dark without DOM");

  const env = makeThemeEnv(null, true);
  const scene = new THREE.Scene();
  const fakeEnvTex = { isTexture: true, disposed: false, dispose() { this.disposed = true; } };
  // PMREM returns a render target; disposing it (not just .texture) frees the FBO.
  const fakeEnvTarget = { texture: fakeEnvTex, disposed: false, dispose() { this.disposed = true; this.texture.dispose(); } };
  class FakePMREM {
    fromScene(envScene) { assert.ok(envScene.children.length >= 1, "procedural env scene"); return fakeEnvTarget; }
    dispose() {}
  }
  const renderer = { getRenderTarget() { return null; }, setRenderTarget() {} };
  const changes = [];
  const studio = S.createStudioLighting({
    THREE: { ...THREE, PMREMGenerator: FakePMREM },
    scene,
    renderer,
    doc: env.doc,
    win: env.win,
    onThemeChange: m => changes.push(m)
  });
  const names = scene.children.map(c => c.name).sort();
  assert.deepEqual(names, ["ws-studio-fill", "ws-studio-hemi", "ws-studio-key", "ws-studio-rim"]);
  assert.equal(scene.children.filter(c => c.isPointLight).length, 0, "studio adds no point lights");
  let st = studio.getState();
  assert.equal(st.mode, "dark");
  assert.equal(st.observing, true);
  assert.equal(st.followsMedia, true);
  const key = scene.getObjectByName("ws-studio-key");
  const rim = scene.getObjectByName("ws-studio-rim");
  const hemi = scene.getObjectByName("ws-studio-hemi");
  assert.equal(key.color.getHex(), S.STUDIO_PRESETS.dark.key.color);
  assert.equal(rim.intensity, S.STUDIO_PRESETS.dark.rim.intensity);
  const keyColorObj = key.color;

  // OS scheme flips to light (no explicit data-theme) -> light preset in place.
  env.setPrefersDark(false);
  st = studio.getState();
  assert.equal(st.mode, "light");
  assert.equal(key.color, keyColorObj, "theme switch mutates colors in place (no allocation)");
  assert.equal(key.color.getHex(), S.STUDIO_PRESETS.light.key.color);
  assert.ok(rim.intensity < S.STUDIO_PRESETS.dark.rim.intensity, "light theme lowers the separation rim");
  assert.equal(hemi.groundColor.getHex(), S.STUDIO_PRESETS.light.hemi.ground);

  // Explicit data-theme overrides the media query, observed via MutationObserver.
  env.setAttr("dark");
  assert.equal(studio.getState().mode, "dark");
  env.setAttr("dark");
  assert.deepEqual(changes, ["dark", "light", "dark"], "redundant notifications do not re-apply");
  assert.equal(env.observers[0].opts.attributeFilter[0], "data-theme");

  // Prop-only PMREM env: metallic Standard props only; MToon / matte / avatar untouched.
  const metal = new THREE.MeshStandardMaterial({ metalness: 0.8 });
  const matte = new THREE.MeshStandardMaterial({ metalness: 0.05 });
  const mtoonLike = new THREE.MeshStandardMaterial({ metalness: 1 });
  mtoonLike.isMToonMaterial = true;
  const avatarMat = new THREE.MeshStandardMaterial({ metalness: 1 });
  const shadowMat = new THREE.MeshBasicMaterial({ transparent: true });
  const propRoot = new THREE.Group();
  const shadowMesh = new THREE.Mesh(new THREE.PlaneGeometry(1, 1), shadowMat);
  shadowMesh.userData.contactShadow = true;
  propRoot.add(new THREE.Mesh(new THREE.BoxGeometry(), metal), new THREE.Mesh(new THREE.BoxGeometry(), matte), new THREE.Mesh(new THREE.BoxGeometry(), mtoonLike), shadowMesh);
  scene.add(new THREE.Mesh(new THREE.BoxGeometry(), avatarMat));
  assert.equal(studio.attachPropEnvironment([propRoot]), 1);
  assert.equal(metal.envMap, fakeEnvTex);
  assert.equal(metal.envMapIntensity, S.STUDIO_PRESETS.dark.envIntensity);
  assert.equal(matte.envMap, null);
  assert.equal(mtoonLike.envMap, null, "MToon never receives the prop env");
  assert.equal(avatarMat.envMap, null);
  assert.equal(scene.environment, null, "scene.environment stays unset");
  assert.equal(shadowMat.userData.studioOpacity, S.STUDIO_PRESETS.dark.shadowOpacity);
  assert.equal(studio.attachPropEnvironment([propRoot]), 1, "re-attach is idempotent");
  env.setAttr("light");
  assert.equal(metal.envMapIntensity, S.STUDIO_PRESETS.light.envIntensity);
  assert.equal(shadowMat.userData.studioOpacity, S.STUDIO_PRESETS.light.shadowOpacity);
  st = studio.getState();
  assert.equal(st.envReady, true);
  assert.equal(st.envMaterialCount, 1);

  studio.dispose();
  assert.equal(scene.children.some(c => /^ws-studio-/.test(c.name)), false);
  assert.equal(metal.envMap, null);
  assert.equal(fakeEnvTarget.disposed, true, "PMREM render target disposed");
  assert.equal(fakeEnvTex.disposed, true);
  assert.equal(env.observers[0].active, false);
  assert.equal(env.mqListeners.size, 0);

  // Renderers without render-target support skip PMREM gracefully.
  const env2 = makeThemeEnv("light", true);
  const s2 = S.createStudioLighting({ THREE, scene: new THREE.Scene(), renderer: {}, doc: env2.doc, win: env2.win });
  const m2 = new THREE.MeshStandardMaterial({ metalness: 1 });
  const r2 = new THREE.Group();
  r2.add(new THREE.Mesh(new THREE.BoxGeometry(), m2));
  assert.equal(s2.attachPropEnvironment([r2]), 0);
  assert.equal(m2.envMap, null);
  assert.equal(s2.getState().mode, "light");
  s2.dispose();
});

test("Phase 2 bust framing solver keeps 10-14% headroom and clear side margins at 220-360px slot aspects", async () => {
  const THREE = await loadThree();
  const { C } = loadCompanion();
  const S = C.vrmStudio;
  const body = { topY: 1.62, waistY: 1.02, bustSpan: 0.3, upperWidth: 0.46, frontOffset: 0.12 };
  const wideHair = { ...body, upperWidth: 0.74 };
  const limits = new Set();
  // Slot ~245px tall; 220/280/360px rails give aspects ~0.9 / 1.15 / 1.47.
  for (const [aspect, b] of [[0.9, body], [1.15, body], [1.47, body], [0.9, wideHair], [1.15, wideHair]]) {
    const fr = S.solveBustFraming({ ...b, aspect, fov: 26 });
    limits.add(fr.limitedBy);
    assert.ok(fr.headroomRatio >= 0.10 && fr.headroomRatio <= 0.14);
    const cam = new THREE.PerspectiveCamera(26, aspect, 0.01, 50);
    cam.position.set(0, fr.targetY, fr.dist);
    cam.lookAt(0, fr.targetY, 0);
    cam.updateProjectionMatrix();
    const qa = S.measureFraming(THREE, cam, { topY: b.topY, chestY: 1.3, upperWidth: b.upperWidth, centerX: 0, headZ: 0, limitedBy: fr.limitedBy });
    assert.ok(qa.headroomPct >= 10 && qa.headroomPct <= 14, `aspect ${aspect}: headroom ${qa.headroomPct}% outside 10-14%`);
    assert.ok(qa.sideMarginPct >= 0, `aspect ${aspect}: upper body clips horizontally (${qa.sideMarginPct}%)`);
    // Width must also fit on the nearer front plane (hair/shoulders in front of the head).
    const front = S.measureFraming(THREE, cam, { topY: b.topY, chestY: 1.3, upperWidth: b.upperWidth, centerX: 0, headZ: b.frontOffset, limitedBy: fr.limitedBy });
    assert.ok(front.sideMarginPct >= -0.01, `aspect ${aspect}: front plane clips horizontally (${front.sideMarginPct}%)`);
    if (fr.limitedBy === "width") assert.ok(front.sideMarginPct < 0.5, `aspect ${aspect}: width-limited fit is tight on the front plane (${front.sideMarginPct}%)`);
  }
  assert.ok(limits.has("width") && limits.has("height"), "wide hair in a narrow slot is width-limited (frame grows downward)");
  // Out-of-range headroom requests are clamped into 10-14%.
  assert.equal(S.solveBustFraming({ ...body, headroomRatio: 0.3 }).headroomRatio, 0.14);
  assert.equal(S.solveBustFraming({ ...body, headroomRatio: 0.01 }).headroomRatio, 0.10);
});

test("Phase 2 pixel headroom probe reads the slot rect bottom-up and ignores antialias specks", () => {
  const { C } = loadCompanion();
  const S = C.vrmStudio;
  // 10x20 CSS-px slot at (4, 6) in a 40px-tall viewport, dpr 2 -> 20x40 device px.
  const calls = [];
  const makeGl = (fill) => ({
    RGBA: 0x1908, UNSIGNED_BYTE: 0x1401,
    readPixels(x, y, w, h, fmt, type, buf) { calls.push({ x, y, w, h }); buf.fill(0, 0, w * h * 4); fill(w, h, buf); }
  });
  const setAlpha = (buf, w, glRow, col, a = 255) => { buf[(glRow * w + col) * 4 + 3] = a; };
  const rect = { left: 4, top: 6, width: 10, height: 20 };
  // Silhouette top 8 device rows below the slot top (GL row h-1-8), plus a lone speck at row 2.
  const gl = makeGl((w, h, buf) => {
    setAlpha(buf, w, h - 1 - 2, 5);
    setAlpha(buf, w, h - 1 - 8, 3); setAlpha(buf, w, h - 1 - 8, 4);
    for (let r = 9; r < h; r++) for (let c = 6; c < 14; c++) setAlpha(buf, w, h - 1 - r, c);
  });
  const p = S.measureAlphaTopRow(gl, rect, 40, 2);
  assert.deepEqual(calls[0], { x: 8, y: 28, w: 20, h: 40 }, "GL origin is bottom-left in device px");
  assert.equal(p.topRow, 8);
  assert.equal(p.height, 40);
  assert.equal(p.ratio, 0.2);
  // Faint (alpha <= 12) pixels never count; an empty slot reports ratio -1.
  const faint = S.measureAlphaTopRow(makeGl((w, h, buf) => { for (let c = 0; c < w; c++) setAlpha(buf, w, h - 1, c, 10); }), rect, 40, 2);
  assert.equal(faint.ratio, -1);
  assert.equal(S.measureAlphaTopRow(null, rect, 40, 2), null);
  assert.equal(S.measureAlphaTopRow(makeGl(() => { throw new Error("lost"); }), rect, 40, 2), null);
});
