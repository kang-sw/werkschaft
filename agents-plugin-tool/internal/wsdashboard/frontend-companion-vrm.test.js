const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

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
  for (const name of ["builtins", "packs", "focus", "deform", "compositor", "scenarios", "menu", "sources", "settings", "vrm", "stage"]) {
    vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", `dashboard-companion-${name}.js`), "utf8"), context);
  }
  return { C: context.window.WsCompanion, context };
}

function makeFakeElement(tag, doc) {
  const el = {
    tagName: tag.toUpperCase(),
    className: "",
    id: "",
    title: "",
    textContent: "",
    value: "",
    type: "",
    disabled: false,
    hidden: false,
    open: false,
    tabIndex: 0,
    width: 0,
    height: 0,
    offsetWidth: 160,
    offsetHeight: 32,
    children: [],
    dataset: {},
    attrs: {},
    listeners: {},
    ownerDocument: doc,
    style: {
      setProperty(k, v) { this[k] = v; },
      getPropertyValue(k) { return this[k] || ""; },
      removeProperty(k) { delete this[k]; }
    },
    setAttribute(k, v) {
      this.attrs[k] = String(v);
      if (k.startsWith("data-")) {
        const key = k.slice(5).replace(/-([a-z])/g, (_, ch) => ch.toUpperCase());
        this.dataset[key] = String(v);
      }
    },
    getAttribute(k) { return this.attrs[k] ?? null; },
    hasAttribute(k) { return Object.hasOwn(this.attrs, k); },
    removeAttribute(k) {
      delete this.attrs[k];
      if (k.startsWith("data-")) {
        const key = k.slice(5).replace(/-([a-z])/g, (_, ch) => ch.toUpperCase());
        delete this.dataset[key];
      }
    },
    addEventListener(type, fn, opts) {
      (this.listeners[type] ||= []).push(fn);
      if (opts && opts.signal) {
        opts.signal.addEventListener("abort", () => {
          this.listeners[type] = (this.listeners[type] || []).filter(f => f !== fn);
        });
      }
    },
    dispatchEvent(event) {
      for (const fn of [...(this.listeners[event.type] || [])]) fn(event);
    },
    append(...items) { this.children.push(...items); },
    insertBefore(item) { this.children.push(item); },
    replaceChildren(...items) { this.children = [...items]; },
    remove() { this.removed = true; },
    setPointerCapture() {},
    getContext(kind) {
      if (kind !== "2d") return null;
      return {
        canvas: this,
        clearRect() {},
        drawImage() {},
        setTransform() {},
        save() {},
        restore() {},
        getImageData: () => ({ data: [] })
      };
    },
    getBoundingClientRect() {
      return { left: 0, top: 0, right: this.offsetWidth, bottom: this.offsetHeight, width: this.offsetWidth, height: this.offsetHeight };
    },
    all() {
      return this.children.flatMap(child => [child, ...(child && typeof child.all === "function" ? child.all() : [])]);
    },
    contains(other) { return other === this || this.all().includes(other); },
    querySelector() { return null; },
    querySelectorAll() { return []; },
    show() { this.open = true; },
    close() { this.open = false; },
    focus() {}
  };
  return el;
}

function makeFakeVrmRuntime(metaVersion = "1") {
  const stats = {
    renderersCreated: 0,
    renderersDisposed: 0,
    contextLossForced: 0,
    deepDisposed: 0,
    renders: 0,
    rotateVRM0Calls: 0,
    lastVrm: null
  };

  class Vector3 {
    constructor(x = 0, y = 0, z = 0) { this.x = x; this.y = y; this.z = z; }
    set(x, y, z) { this.x = x; this.y = y; this.z = z; return this; }
    copy(v) { this.x = v.x; this.y = v.y; this.z = v.z; return this; }
  }
  class Quaternion {
    constructor(x = 0, y = 0, z = 0, w = 1) { this.x = x; this.y = y; this.z = z; this.w = w; }
    set(x, y, z, w) { this.x = x; this.y = y; this.z = z; this.w = w; return this; }
  }
  class Object3D {
    constructor() {
      this.position = new Vector3();
      this.rotation = { x: 0, y: 0, z: 0, order: "XYZ", set(x, y, z, o) { this.x = x; this.y = y; this.z = z; if (o) this.order = o; } };
      this.quaternion = new Quaternion();
      this.children = [];
    }
    add(...items) { this.children.push(...items); }
    remove(item) { this.children = this.children.filter(c => c !== item); }
    traverse(fn) { fn(this); this.children.forEach(c => c.traverse?.(fn)); }
    updateMatrixWorld() {}
    getWorldPosition(target) { return target.copy(this.position); }
  }
  class Scene extends Object3D {}
  class PerspectiveCamera extends Object3D {
    constructor(fov, aspect, near, far) {
      super();
      this.fov = fov; this.aspect = aspect; this.near = near; this.far = far;
    }
    lookAt() {}
    updateProjectionMatrix() {}
  }
  class DirectionalLight extends Object3D {}
  class HemisphereLight extends Object3D {}
  class AnimationMixer {
    constructor() {}
    update() {}
    stopAllAction() {}
    clipAction() { return { reset() {}, fadeIn() {}, fadeOut() {}, play() {}, stop() {}, crossFadeTo() {} }; }
  }
  class WebGLRenderer {
    constructor() {
      stats.renderersCreated++;
      this.shadowMap = { enabled: true };
      this.info = { render: { calls: 12, triangles: 8400 } };
    }
    setClearColor() {}
    setPixelRatio(r) { this.dpr = r; }
    setSize(w, h) { this.w = w; this.h = h; }
    render() { stats.renders++; }
    dispose() { stats.renderersDisposed++; }
    forceContextLoss() { stats.contextLossForced++; }
  }
  class GLTFLoader {
    register() {}
    async parseAsync() {
      const bones = new Map();
      const mkBone = (name, y) => {
        const node = new Object3D();
        node.name = name;
        node.position.set(0, y, 0);
        bones.set(name, node);
      };
      mkBone("spine", 1.0);
      mkBone("chest", 1.15);
      mkBone("upperChest", 1.24);
      mkBone("neck", 1.38);
      mkBone("head", 1.48);
      mkBone("leftUpperArm", 1.32);
      mkBone("leftLowerArm", 1.12);
      mkBone("leftHand", 0.92);
      mkBone("rightUpperArm", 1.32);
      mkBone("rightLowerArm", 1.12);
      mkBone("rightHand", 0.92);

      const values = new Map();
      const presetMap = metaVersion === "0"
        ? { neutral: {}, blink: {}, fun: {}, angry: {}, sorrow: {}, joy: {}, o: {} }
        : { neutral: {}, blink: {}, relaxed: {}, angry: {}, sad: {}, happy: {}, surprised: {} };
      const vrm = {
        meta: { metaVersion },
        scene: new Object3D(),
        humanoid: { getNormalizedBoneNode: name => bones.get(name) || null },
        expressionManager: {
          presetExpressionMap: presetMap,
          customExpressionMap: {},
          expressions: Object.keys(presetMap).map(expressionName => ({ expressionName })),
          setValue: (k, v) => values.set(k, v),
          getValue: k => values.get(k) ?? 0
        },
        lookAt: { target: null },
        update() {}
      };
      stats.lastVrm = vrm;
      return { scene: vrm.scene, userData: { vrm } };
    }
  }
  const VRMUtils = {
    removeUnnecessaryVertices() {},
    combineSkeletons() {},
    rotateVRM0() { stats.rotateVRM0Calls++; },
    deepDispose() { stats.deepDisposed++; }
  };
  return {
    stats,
    runtime: {
      THREE: { Vector3, Quaternion, Object3D, Scene, PerspectiveCamera, DirectionalLight, HemisphereLight, AnimationMixer, WebGLRenderer, SRGBColorSpace: "srgb", NoToneMapping: 0 },
      GLTFLoader,
      VRMLoaderPlugin: class {},
      VRMAnimationLoaderPlugin: class {},
      VRMUtils,
      createVRMAnimationClip: () => ({})
    }
  };
}

test("packs catalog branches on renderer === vrm without 2D decode and fetchPackAsset retries 409 once", async () => {
  let bitmapCalls = 0;
  const fetched = [];
  const { C } = loadCompanion({
    createImageBitmap: async () => { bitmapCalls++; return { width: 64, height: 64, close() {} }; },
    fetch: async url => {
      fetched.push(url);
      if (url === "/api/companion/packs") {
        return {
          ok: true,
          json: async () => ({
            packs: [
              { id: "vrm-sample", rev: "rev2", renderer: "vrm", schema: "ws-companion-vrm/v1" },
              { id: "default", rev: "d1", renderer: "2d", schema: "ws-companion/v1" }
            ]
          })
        };
      }
      if (url === "/api/companion/packs/vrm-sample/pack.json?rev=rev2") {
        return {
          ok: true,
          json: async () => ({
            schema: "ws-companion-vrm/v1",
            id: "vrm-sample",
            model: "avatar.vrm",
            expressionMap: { happy: { preset: "happy", weight: 0.85 } },
            animations: { wave: "wave.vrma", bad: "../escape.vrma" }
          })
        };
      }
      if (url === "/api/companion/packs/vrm-sample/assets/avatar.vrm?rev=rev1") {
        return {
          ok: false,
          status: 409,
          headers: { get: h => h === "X-Companion-Current-Rev" ? "rev2" : null }
        };
      }
      if (url === "/api/companion/packs/vrm-sample/assets/avatar.vrm?rev=rev2") {
        return { ok: true, status: 200, arrayBuffer: async () => new Uint8Array([1, 2, 3, 4]).buffer };
      }
      throw new Error("unexpected fetch: " + url);
    }
  });

  const loaded = await new Promise((resolve, reject) => {
    C.packLoader((pack, shared) => resolve({ pack, shared }), () => reject(new Error("pack failed")))
      .load("vrm-sample", { routeKind: "home" });
  });

  assert.equal(bitmapCalls, 0, "VRM pack must never invoke 2D createImageBitmap");
  assert.equal(loaded.shared ?? null, null, "VRM pack skips default 2D shared-pack decode");
  assert.equal(loaded.pack.renderer, "vrm");
  assert.equal(loaded.pack.modelFile, "avatar.vrm");
  assert.deepEqual({ ...loaded.pack.animations }, { wave: "wave.vrma" });
  assert.equal(loaded.pack.expressionMap.happy.weight, 0.85);
  assert.ok(!fetched.some(u => u.includes("/default/pack.json")), "must not fetch default 2D pack for VRM");

  // Simulate stale rev -> 409 -> catalog re-read -> single retry with rev2.
  const stalePack = { ...loaded.pack, rev: "rev1" };
  const asset = await C.fetchPackAsset(stalePack, "avatar.vrm");
  assert.equal(asset.rev, "rev2");
  assert.equal(stalePack.rev, "rev2");
  assert.equal(asset.buffer.byteLength, 4);
});

test("all 12 built-in scenarios resolve cleanly against the VRM adapter without unknown IDs or throws", () => {
  const { C, context } = loadCompanion();
  const scenarios = context.window.WsCompanionBuiltins.scenarios;
  const ids = Object.keys(scenarios);
  assert.ok(ids.length >= 12, "expected at least 12 built-in scenarios");

  const rig = C.makeVrmRig({ id: "vrm-test", rev: "r1", renderer: "vrm", modelFile: "m.vrm", manifest: {}, expressionMap: {}, animations: {} });
  const validExpressions = new Set(Object.keys(C.vrmVocabulary().expressions));

  for (const id of ids) {
    const def = scenarios[id];
    const beats = Array.isArray(def.steps) && def.steps.length ? def.steps : [def];
    for (const beat of beats) {
      const resolved = C.resolveVrmScenario(id, beat);
      assert.ok(resolved.mode === "full" || resolved.mode === "expression",
        `ground-zero scenario ${id} must resolve cleanly (got ${resolved.mode})`);
      assert.equal(resolved.pose, "stand", `ground-zero scenario ${id} must stay on stand pose`);
      assert.equal(resolved.prop, null, `ground-zero scenario ${id} must drop 3D props`);
      assert.equal(resolved.stage, "stay", `ground-zero scenario ${id} must disable vertical hop/bounce stage effects`);
      assert.ok(validExpressions.has(resolved.expression), `scenario ${id} resolved to unknown expression ${resolved.expression}`);
      const propId = typeof beat.prop === "string" ? beat.prop : beat.prop?.id;
      const view = rig.view(beat.pose || "stand", beat.gesture || null, beat.expression || "idle", propId ? [propId] : []);
      assert.ok(validExpressions.has(view.expression));
      assert.doesNotThrow(() => view.render(0.5, { x: 0.2, y: -0.1 }, null, { progress: 0.5, beat }));
    }
  }
  rig.dispose();
});

test("VrmExpressionController covers all 7 expressions, VRM 0.x fallbacks, expressionMap overrides, and R_y(pi) conjugation", () => {
  const { C } = loadCompanion();

  // R_y(pi) conjugation negates x and z, preserves y (and w for quaternions).
  assert.deepEqual([...C.vrmMath.conjugateVrm0Euler(0.5, 0.3, -1.2, false)], [0.5, 0.3, -1.2]);
  assert.deepEqual([...C.vrmMath.conjugateVrm0Euler(0.5, 0.3, -1.2, true)], [-0.5, 0.3, 1.2]);
  const q = { x: 0.2, y: 0.3, z: -0.4, w: 0.8, set(x, y, z, w) { this.x = x; this.y = y; this.z = z; this.w = w; } };
  C.vrmMath.conjugateVrm0Quat(q, true);
  assert.deepEqual([q.x, q.y, q.z, q.w], [-0.2, 0.3, 0.4, 0.8]);

  function makeMgr(presets, customs = []) {
    const vals = new Map();
    return {
      vals,
      vrm: {
        meta: { metaVersion: "0" },
        expressionManager: {
          presetExpressionMap: Object.fromEntries(presets.map(p => [p, {}])),
          customExpressionMap: Object.fromEntries(customs.map(c => [c, {}])),
          expressions: [...presets, ...customs].map(expressionName => ({ expressionName })),
          setValue: (k, v) => vals.set(k, v)
        }
      }
    };
  }

  // VRM 0.x with oh fallback (no surprised / Surprised / Fcl_ALL_Surprised).
  const m0 = makeMgr(["neutral", "blink", "fun", "angry", "sorrow", "joy", "oh"]);
  const c0 = new C.VrmExpressionController();
  c0.attach(m0.vrm);
  assert.equal(c0.surprisedStrategy, "fallback:oh+blink0");
  const settle = (ctrl, state) => { ctrl.setState(state); for (let i = 0; i < 40; i++) ctrl.update(0.05); };

  settle(c0, "oo");
  assert.ok(m0.vals.get("oh") > 0.58 && m0.vals.get("blink") === 0, "oo falls back to oh(0.60)+blink(0)");
  settle(c0, "happy");
  assert.ok(m0.vals.get("joy") > 0.9, "happy maps to joy on VRM 0.x");
  settle(c0, "cry");
  assert.ok(m0.vals.get("sorrow") > 0.88 && m0.vals.get("blink") > 0.2, "cry maps to sorrow+blink on VRM 0.x");
  settle(c0, "idle_doze");
  assert.ok(m0.vals.get("blink") > 0.98 && m0.vals.get("fun") > 0.38, "idle_doze maps to blink+fun on VRM 0.x");
  settle(c0, "focused");
  assert.ok(m0.vals.get("neutral") > 0.8 && m0.vals.get("blink") > 0.14 && m0.vals.get("angry") > 0.09);
  settle(c0, "half_lid");
  assert.ok(m0.vals.get("blink") > 0.48 && m0.vals.get("fun") > 0.19);

  // VRM 0.x custom Fcl_ALL_Surprised + expressionMap overrides (custom and 1.0->0.x preset alias).
  const mCustom = makeMgr(["neutral", "blink", "joy", "sorrow"], ["Fcl_ALL_Surprised", "BigSmile"]);
  const cCustom = new C.VrmExpressionController({
    happy: { custom: "BigSmile", weight: 0.75 },
    cry: { preset: "sad", weight: 0.8 }
  });
  cCustom.attach(mCustom.vrm);
  assert.equal(cCustom.surprisedStrategy, "custom:Fcl_ALL_Surprised");
  settle(cCustom, "oo");
  assert.ok(mCustom.vals.get("Fcl_ALL_Surprised") > 0.9);
  settle(cCustom, "happy");
  assert.ok(Math.abs(mCustom.vals.get("BigSmile") - 0.75) < 0.01);
  settle(cCustom, "cry");
  assert.ok(Math.abs(mCustom.vals.get("sorrow") - 0.8) < 0.01, "preset alias 'sad' resolves to VRM 0.x 'sorrow'");
});

test("stage drives both a fake adapter and makeVrmRig through rail-docking, narrow hide/pause, context-loss, and disposal", async () => {
  async function runStageScenario(useFakeAdapter) {
    let now = 0, raf = null, rafId = 0, hidden = false;
    let vpW = 1680, vpH = 960;
    const storage = new Map();
    const docListeners = {}, winListeners = {};
    const doc = {
      get hidden() { return hidden; },
      documentElement: { dataset: { rid: "s1" }, classList: { contains: () => false }, hasAttribute: () => true },
      body: null,
      getElementById: id => id === "rail" ? railEl : null,
      createElement: tag => makeFakeElement(tag, doc),
      createElementNS: (_ns, tag) => makeFakeElement(tag, doc),
      addEventListener: (type, fn) => { (docListeners[type] ||= []).push(fn); }
    };
    doc.body = makeFakeElement("body", doc);
    const slotEl = makeFakeElement("div", doc);
    slotEl.id = "rail-companion-slot";
    slotEl.className = "rail-companion-slot";
    slotEl.hidden = true;
    slotEl.getBoundingClientRect = () => slotEl.hidden
      ? { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 }
      : { left: 12, top: 610, right: 352, bottom: 850, width: 340, height: 240 };
    const railEl = makeFakeElement("aside", doc);
    railEl.id = "rail";
    railEl.getBoundingClientRect = () => ({ left: 12, top: 12, right: 352, bottom: vpH - 12, width: 340, height: vpH - 24 });
    railEl.querySelector = sel => sel === "#rail-companion-slot" ? slotEl : null;
    railEl.querySelectorAll = sel => sel.includes("#rail-companion-slot") ? [slotEl] : [];

    let activePackMode = "vrm";
    let vrmAssetFetches = 0;
    const fakeThree = makeFakeVrmRuntime("1");
    const fakeAdapterCalls = [];
    const fakeAdapterDisposes = [];

    const manifest2d = {
      id: "default", schema: "ws-companion/v1", canvas: [128, 128],
      poses: { stand: { layers: [{ file: "stand.png" }] } }
    };

    const { C } = loadCompanion({
      document: doc,
      getComputedStyle: () => ({ visibility: "visible", display: "flex" }),
      window: {
        get innerWidth() { return vpW; },
        get innerHeight() { return vpH; },
        devicePixelRatio: 2,
        matchMedia: q => ({
          get matches() { return q.includes("1600") ? vpW >= 1600 : false; },
          addEventListener(type, fn) { (winListeners["mq:" + type] ||= []).push(fn); }
        }),
        localStorage: {
          getItem: k => storage.has(k) ? storage.get(k) : null,
          setItem: (k, v) => storage.set(k, String(v)),
          removeItem: k => storage.delete(k)
        },
        addEventListener: (type, fn) => { (winListeners[type] ||= []).push(fn); },
        WsShell: { companionContext: (routeKind = "home") => ({ routeKind, activity: "idle", landmarks: {}, events: [], rightColActive: false, drawerOpen: false }) },
        wsCompanionModalRows: []
      },
      OffscreenCanvas: class {
        constructor(w, h) { this.width = w; this.height = h; }
        getContext() { return { canvas: this, clearRect() {}, drawImage() {}, save() {}, restore() {}, getImageData: () => ({ data: [] }) }; }
      },
      ResizeObserver: class { observe() {} disconnect() {} },
      MutationObserver: class { observe() {} disconnect() {} },
      performance: { now: () => now },
      wsT: k => k,
      requestAnimationFrame: fn => { raf = fn; return ++rafId; },
      cancelAnimationFrame: () => { raf = null; },
      fetch: async url => {
        if (url.startsWith("/api/companion/packs?")) {
          return { ok: true, json: async () => ({ resolvedAuto: { packId: activePackMode === "vrm" ? "vrm1" : "default" }, packs: [
            { id: "vrm1", rev: "rv1", renderer: "vrm", schema: "ws-companion-vrm/v1" },
            { id: "default", rev: "d1", renderer: "2d", schema: "ws-companion/v1" }
          ] }) };
        }
        if (url === "/api/companion/packs") {
          return { ok: true, json: async () => ({ packs: [
            { id: "vrm1", rev: "rv1", renderer: "vrm", schema: "ws-companion-vrm/v1" },
            { id: "default", rev: "d1", renderer: "2d", schema: "ws-companion/v1" }
          ] }) };
        }
        if (url.includes("/vrm1/pack.json")) {
          return { ok: true, json: async () => ({ schema: "ws-companion-vrm/v1", id: "vrm1", model: "a.vrm" }) };
        }
        if (url.includes("/vrm1/assets/a.vrm")) {
          vrmAssetFetches++;
          return { ok: true, status: 200, arrayBuffer: async () => new Uint8Array([7, 8, 9]).buffer };
        }
        if (url.includes("/default/pack.json")) return { ok: true, json: async () => manifest2d };
        return { ok: true, blob: async () => url };
      },
      createImageBitmap: async () => ({ width: 128, height: 128, close() {} })
    });

    C.quickMenu = () => ({ rebuild() {}, measure() {}, place() {}, close() {} });
    C.vrmRuntimeLoader = async () => fakeThree.runtime;

    if (useFakeAdapter) {
      C.makeVrmAdapter = () => {
        const viewObj = {
          pose: "stand", gesture: null, expression: "idle", props: [],
          handAnchor: [120, 170], articulatedDrag: false, seat: 1, bounds: [0, 0, 1, 1],
          render() { fakeAdapterCalls.push("render"); }
        };
        return {
          renderer: "vrm", width: 240, height: 280,
          mount() { fakeAdapterCalls.push("mount"); return Promise.resolve(); },
          view(pose, gesture, expr) { fakeAdapterCalls.push(`view:${pose}:${expr}`); viewObj.pose = pose; viewObj.expression = expr; return viewObj; },
          prop() { fakeAdapterCalls.push("prop"); return { data: null, drawable: false, draw() {} }; },
          setGaze() { fakeAdapterCalls.push("setGaze"); },
          resize() { fakeAdapterCalls.push("resize"); },
          pause() { fakeAdapterCalls.push("pause"); },
          resume() { fakeAdapterCalls.push("resume"); },
          setPreset() {}, setWardrobe() {}, wardrobe() { return null; },
          dispose() { fakeAdapterCalls.push("dispose"); fakeAdapterDisposes.push(true); }
        };
      };
    }

    const stage = C.mount({ pack: "vrm1", preset: "" }, () => {});
    for (let i = 0; i < 6; i++) await new Promise(r => setImmediate(r));

    const step = (dt = 40) => {
      const fn = raf;
      raf = null;
      if (fn) { now += dt; fn(now); }
    };
    step();
    const initial = stage.debugState();
    assert.equal(initial.renderer, "vrm");
    assert.equal(initial.dock, "rail-slot");
    assert.equal(initial.vrmRailVisible, true);
    assert.equal(initial.hidden, false);
    assert.equal(slotEl.hidden, false);
    assert.equal(railEl.dataset.companionDock, "rail-slot");
    assert.equal(initial.size, 340);
    assert.equal(initial.vrmHeight, 240);
    assert.equal(initial.home.x, 182);
    assert.equal(initial.home.y, 850);
    assert.equal(initial.avoiding, false);

    // Right-column hover does NOT slide the rail-docked VRM.
    stage.context({
      routeKind: "session", activity: "idle", sessionId: "s1", events: [],
      rightColActive: true, drawerOpen: false,
      landmarks: { "right-col": { x: 1250, y: 0, width: 350, height: 960 } }
    }, { left: 356, top: 0, width: 1324, height: 960 });
    step();
    assert.equal(stage.debugState().avoiding, false);
    assert.equal(stage.debugState().home.x, 182);

    // Narrow viewport (< 1600px) hides VRM, hides rail slot, and pauses WebGL rendering cleanly.
    vpW = 1440;
    vpH = 900;
    for (const fn of winListeners.resize || []) fn();
    assert.equal(stage.debugState().vrmRailVisible, false);
    assert.equal(stage.debugState().hidden, true);
    assert.equal(stage.debugState().dock, "hidden");
    assert.equal(slotEl.hidden, true);
    assert.equal(railEl.dataset.companionDock, undefined);
    assert.equal(raf, null, "narrow viewport must pause rAF loop");
    const rendersWhileNarrow = fakeThree.stats.renders;
    step();
    assert.equal(fakeThree.stats.renders, rendersWhileNarrow, "narrow viewport must render 0 frames");

    // Widening back to >= 1600px resumes VRM in the left rail slot seamlessly.
    vpW = 1680;
    vpH = 960;
    for (const fn of winListeners.resize || []) fn();
    assert.equal(stage.debugState().vrmRailVisible, true);
    assert.equal(stage.debugState().hidden, false);
    assert.equal(stage.debugState().dock, "rail-slot");
    assert.equal(slotEl.hidden, false);
    assert.notEqual(raf, null, "widening to >= 1600px must resume rAF loop");
    step();

    // Directed VRM expression command succeeds using VRM vocabulary.
    assert.equal(stage.command({ sessionId: "s1", expression: "happy", ttl_ms: 2000 }), true);
    step();

    // Hidden visibility pauses adapter and cancels rAF; visible resumes.
    hidden = true;
    for (const fn of docListeners.visibilitychange || []) fn();
    assert.equal(raf, null, "hidden document must cancel rAF");
    hidden = false;
    for (const fn of docListeners.visibilitychange || []) fn();
    step();

    // Real VRM adapter: verify WebGL context loss & restore does NOT refetch .vrm from network.
    if (!useFakeAdapter) {
      assert.equal(vrmAssetFetches, 1);
      const rootNode = doc.body.children[0];
      const vrmCanvas = rootNode.all().find(el => el.className === "ws-companion-vrm-canvas");
      assert.ok(vrmCanvas, "VRM WebGL canvas must be mounted inside separate container");
      let prevented = false;
      vrmCanvas.dispatchEvent({ type: "webglcontextlost", preventDefault() { prevented = true; } });
      assert.equal(prevented, true);
      assert.equal(stage.debugState().vrm.contextLost, true);
      assert.equal(raf, null, "contextLost must pause the stage rAF loop");
      const rendersBefore = fakeThree.stats.renders;
      step();
      assert.equal(fakeThree.stats.renders, rendersBefore, "contextLost must not render");
      vrmCanvas.dispatchEvent({ type: "webglcontextrestored" });
      for (let i = 0; i < 6; i++) await new Promise(r => setImmediate(r));
      assert.equal(stage.debugState().vrm.contextLost, false);
      assert.notEqual(raf, null, "context restore must resume the stage rAF loop");
      assert.equal(fakeThree.stats.contextLossForced, 0, "context restore must not call forceContextLoss on restored canvas");
      assert.equal(vrmAssetFetches, 1, "context restore must reuse cached ArrayBuffer with 0 network refetches");
      assert.ok(fakeThree.stats.renders > rendersBefore, "context restore must resume rendering");
    }

    // Switching VRM -> 2D disposes WebGL resources and leaves ws.companion.size untouched.
    activePackMode = "2d";
    stage.apply({ pack: "default", preset: "" });
    for (let i = 0; i < 6; i++) await new Promise(r => setImmediate(r));
    assert.equal(stage.debugState().renderer, "2d");
    assert.equal(slotEl.hidden, true);
    assert.equal(storage.has("ws.companion.size"), false);

    if (useFakeAdapter) {
      for (const expected of ["mount", "view:stand:idle", "prop", "setGaze", "resize", "pause", "resume", "dispose"]) {
        assert.ok(fakeAdapterCalls.includes(expected), `fake adapter missing expected call ${expected}`);
      }
      assert.equal(fakeAdapterDisposes.length, 1);
    } else {
      assert.ok(fakeThree.stats.renderersDisposed >= 1, "switching VRM -> 2D must call renderer.dispose()");
      assert.ok(fakeThree.stats.contextLossForced >= 1, "switching VRM -> 2D must call forceContextLoss()");
      assert.ok(fakeThree.stats.deepDisposed >= 1, "switching VRM -> 2D must deepDispose VRM scene");
    }

    stage.dispose();
  }

  await runStageScenario(true);
  await runStageScenario(false);
});

test("settings modal hides 2D paper-doll wardrobe controls when active pack is VRM", () => {
  let currentRenderer = "vrm";
  const doc = {
    documentElement: { hasAttribute: () => true },
    activeElement: null,
    createElement: tag => makeFakeElement(tag, doc),
    addEventListener() {}
  };
  const { C } = loadCompanion({
    document: doc,
    window: { innerWidth: 1200, innerHeight: 800, addEventListener() {} },
    ResizeObserver: class { observe() {} disconnect() {} },
    Option: class { constructor(text, value, _ds, sel) { this.text = text; this.value = value; this.selected = sel; } },
    wsT: k => k,
    fetch: async () => ({ ok: true, json: async () => ({ packs: [{ id: "vrm1", renderer: "vrm" }] }) })
  });
  const node = makeFakeElement("div", doc);
  const gear = makeFakeElement("button", doc);
  const rows = [
    { key: "dashboard.companion_pack", label: "pack", description: "d", control: "select" },
    { key: "dashboard.companion_preset", label: "preset", description: "d", control: "select" },
    { key: "dashboard.companion_outfit", label: "outfit", description: "d", control: "select" },
    { key: "dashboard.companion_hair", label: "hair", description: "d", control: "select" },
    { key: "dashboard.companion_eyes", label: "eyes", description: "d", control: "select" },
    { key: "dashboard.companion_attachments", label: "attachments", description: "d", control: "text" },
    { key: "dashboard.companion_palette", label: "palette", description: "d", control: "text" },
    { key: "dashboard.companion_agent_cooldown_seconds", label: "cooldown", description: "d", control: "number" }
  ];
  const modal = C.settingsModal(node, gear, { signal: new AbortController().signal }, {
    rows: () => rows,
    config: () => ({ pack: "vrm1", preset: "", outfit: "", hair: "", eyes: "", attachments: "", palette: "", agentCooldownSeconds: 15 }),
    renderer: () => currentRenderer,
    persona: () => null,
    presets: () => ({ available: ["day"], preset: "" }),
    wardrobe: () => ({ outfits: ["street"], hairs: ["bob"], eyesets: ["round"], slots: { tail: ["t"] }, channels: ["hair"] }),
    picks: () => null,
    anchor: () => ({ left: 100, top: 100, right: 200, bottom: 200 }),
    resetSize() {},
    packsChanged() {},
    applyPreset() {},
    notice() {}
  });
  gear.dispatchEvent({ type: "click" });
  const labels = () => node.all().filter(el => el.className === "ws-companion-settings-label").map(el => el.textContent);
  assert.deepEqual(labels(), ["pack", "cooldown"], "VRM mode must hide all 2D wardrobe rows");

  currentRenderer = "2d";
  modal.rebuild();
  const labels2d = labels();
  for (const want of ["pack", "preset", "outfit", "hair", "eyes", "attachments", "palette", "cooldown"]) {
    assert.ok(labels2d.includes(want), `2D mode should include wardrobe row ${want}`);
  }
});

test("VRM adapter recovers from webglcontextlost during initial .vrm fetch and gates Phase 2 .vrma playback", async () => {
  const doc = {
    createElement: tag => makeFakeElement(tag, doc),
    addEventListener() {}
  };
  let resolveFetch = null;
  let fetchCalls = [];
  const fakeThree = makeFakeVrmRuntime("1");
  const { C } = loadCompanion({
    document: doc,
    window: { innerWidth: 1200, innerHeight: 800, devicePixelRatio: 1, addEventListener() {} },
    fetch: url => {
      fetchCalls.push(url);
      return new Promise(resolve => {
        resolveFetch = () => resolve({ ok: true, status: 200, arrayBuffer: async () => new Uint8Array([1, 2, 3, 4]).buffer });
      });
    }
  });
  C.vrmRuntimeLoader = async () => fakeThree.runtime;

  let lostCount = 0, restoredCount = 0, readyCount = 0;
  const pack = {
    id: "vrm-midload", rev: "r1", renderer: "vrm", modelFile: "avatar.vrm",
    manifest: {}, expressionMap: {}, animations: { wave: "wave.vrma" }
  };
  const rig = C.makeVrmRig(pack, {
    onContextLost: () => { lostCount++; },
    onContextRestored: () => { restoredCount++; },
    onReady: () => { readyCount++; }
  });
  const container = makeFakeElement("div", doc);
  const mountPromise = rig.mount(container, { width: 240, height: 280, dpr: 1 });
  for (let i = 0; i < 4; i++) await new Promise(r => setImmediate(r));
  assert.equal(fetchCalls.length, 1, "initial .vrm fetch should be in flight");

  // Lose and restore WebGL context BEFORE the .vrm fetch completes.
  const canvas = container.all().find(el => el.className === "ws-companion-vrm-canvas");
  const overlay = container.all().find(el => el.className === "ws-companion-vrm-overlay");
  canvas.dispatchEvent({ type: "webglcontextlost", preventDefault() {} });
  assert.equal(lostCount, 1);
  assert.equal(rig.isContextLost(), true);
  assert.equal(overlay.hidden, false);

  canvas.dispatchEvent({ type: "webglcontextrestored" });
  assert.equal(restoredCount, 1);
  assert.equal(rig.isContextLost(), false);
  assert.equal(overlay.hidden, true);

  resolveFetch();
  await mountPromise;
  for (let i = 0; i < 6; i++) await new Promise(r => setImmediate(r));

  assert.equal(rig.debugState().loaded, true, "rig must finish loading after mid-fetch context restore");
  assert.equal(rig.debugState().modelFetchCount, 1, "in-flight fetch must be reused without refetching");
  assert.equal(readyCount, 1, "single parse should fire onReady once without duplicate mounts");
  assert.ok(fakeThree.stats.renders >= 1);

  // stand does not fetch wave.vrma.
  const vStand = rig.view("stand", null, "idle", []);
  vStand.render(0.5, { x: 0, y: 0 }, null, {});
  for (let i = 0; i < 4; i++) await new Promise(r => setImmediate(r));
  assert.equal(fetchCalls.some(u => u.includes("wave.vrma")), false);
  rig.dispose();
});

test("THREE.Box3 bust framing includes tall hair/ears with headroom, and gesture registry extension seam works", async () => {
  const { pathToFileURL } = require("node:url");
  const rt = await import(pathToFileURL(path.join(__dirname, "assets", "companion-vrm-runtime.mjs")).href);
  const { THREE } = rt;

  // Build a minimal real-Three.js VRM scene with tall hair/ears extending +0.48m above head bone.
  function makeTallHairVrm(metaVersion) {
    const root = new THREE.Group();
    const is0 = metaVersion === "0";
    if (is0) root.rotation.y = Math.PI;
    const bones = new Map();
    const addBone = (name, parent, x, y, z) => {
      const n = new THREE.Object3D();
      n.name = name;
      n.position.set(x, y, z);
      parent.add(n);
      bones.set(name, n);
      return n;
    };
    const hips = addBone("hips", root, 0, 0.88, 0);
    const spine = addBone("spine", hips, 0, 0.10, 0);
    const chest = addBone("chest", spine, 0, 0.14, 0);
    const upperChest = addBone("upperChest", chest, 0, 0.10, 0);
    const neck = addBone("neck", upperChest, 0, 0.10, 0);
    const head = addBone("head", neck, 0, 0.08, 0); // world y = 1.40
    const lSign = is0 ? -1 : 1;
    addBone("leftShoulder", upperChest, lSign * 0.06, 0.04, 0);
    const lU = addBone("leftUpperArm", upperChest, lSign * 0.16, 0.04, 0);
    const lL = addBone("leftLowerArm", lU, lSign * 0.22, 0, 0);
    addBone("leftHand", lL, lSign * 0.20, 0, 0);
    addBone("rightShoulder", upperChest, -lSign * 0.06, 0.04, 0);
    const rU = addBone("rightUpperArm", upperChest, -lSign * 0.16, 0.04, 0);
    const rL = addBone("rightLowerArm", rU, -lSign * 0.22, 0, 0);
    addBone("rightHand", rL, -lSign * 0.20, 0, 0);

    // Tall hair / ornament mesh peaking at head.y + 0.48 = 1.88m.
    const hairGeo = new THREE.BoxGeometry(0.26, 0.48, 0.24);
    const hairMesh = new THREE.Mesh(hairGeo, new THREE.MeshBasicMaterial());
    hairMesh.position.set(0, 0.24, 0); // top at +0.48 above head bone
    head.add(hairMesh);
    root.updateMatrixWorld(true);

    const vals = new Map();
    const presets = { neutral: {}, blink: {}, happy: {}, sad: {}, angry: {}, relaxed: {}, surprised: {} };
    return {
      meta: { metaVersion },
      scene: root,
      bones,
      humanoid: { getNormalizedBoneNode: name => bones.get(name) || null },
      expressionManager: {
        presetExpressionMap: presets,
        customExpressionMap: {},
        expressions: Object.keys(presets).map(expressionName => ({ expressionName })),
        setValue: (k, v) => vals.set(k, v)
      },
      lookAt: { target: null },
      update() { root.updateMatrixWorld(true); }
    };
  }

  let capturedCamera = null;
  class StubRenderer {
    constructor() { this.shadowMap = { enabled: true }; this.info = { render: { calls: 1, triangles: 12 } }; }
    setClearColor() {}
    setPixelRatio() {}
    setSize() {}
    render(_scene, cam) { capturedCamera = cam; }
    dispose() {}
    forceContextLoss() {}
  }

  const vrm1 = makeTallHairVrm("1");
  const stubRuntime = {
    THREE: { ...THREE, WebGLRenderer: StubRenderer },
    GLTFLoader: class {
      register() {}
      async parseAsync() { return { scene: vrm1.scene, userData: { vrm: vrm1 } }; }
    },
    VRMLoaderPlugin: class {},
    VRMAnimationLoaderPlugin: class {},
    VRMUtils: { removeUnnecessaryVertices() {}, combineSkeletons() {}, rotateVRM0() {}, deepDispose() {} },
    createVRMAnimationClip: () => ({})
  };

  const doc = { createElement: tag => makeFakeElement(tag, doc), addEventListener() {} };
  const { C } = loadCompanion({
    document: doc,
    window: { innerWidth: 1200, innerHeight: 800, devicePixelRatio: 1, addEventListener() {} },
    fetch: async () => ({ ok: true, status: 200, arrayBuffer: async () => new Uint8Array([1, 2, 3, 4]).buffer })
  });
  C.vrmRuntimeLoader = async () => stubRuntime;

  const pack = { id: "vrm-tall", rev: "r1", renderer: "vrm", modelFile: "avatar.vrm", manifest: {}, expressionMap: {}, animations: {} };
  const rig = C.makeVrmRig(pack);
  await rig.mount(makeFakeElement("div", doc), { width: 240, height: 280, dpr: 1 });

  const st = rig.debugState();
  assert.equal(st.framing.usedBox3, true, "autoFrameBust must use THREE.Box3 on posed meshes");
  assert.ok(st.framing.topY >= 1.87, `expected topY >= 1.87 to include tall hair (got ${st.framing.topY})`);
  assert.ok(capturedCamera, "camera must be used in render");
  capturedCamera.updateMatrixWorld(true);

  // Project the front-most top vertex of the tall hair into NDC and verify comfortable headroom.
  const hairTopFront = new THREE.Vector3(0, 1.88, 0.12).project(capturedCamera);
  assert.ok(hairTopFront.y < 0.85, `tall hair top projected too close to/past top edge (ndcY=${hairTopFront.y.toFixed(3)})`);
  assert.ok(hairTopFront.y > 0.25, `tall hair top projected too low (ndcY=${hairTopFront.y.toFixed(3)})`);

  // Verify relaxed lowered-arm stand pose (never T-pose).
  assert.ok(vrm1.bones.get("leftUpperArm").rotation.z < -1.1, "leftUpperArm must be lowered naturally");
  assert.ok(vrm1.bones.get("rightUpperArm").rotation.z > 1.1, "rightUpperArm must be lowered naturally");

  // Verify clean gesture extension seam: registerVrmGesture -> resolve -> update -> unregister.
  let gestureCalls = 0;
  assert.equal(C.registerVrmGesture("custom_nod", ctx => {
    gestureCalls++;
    ctx.setBoneEuler("head", 0.22, 0, 0);
  }), true);
  assert.ok(Object.hasOwn(C.vrmVocabulary(pack).gestures, "custom_nod"));
  const res = C.resolveVrmScenario("test", { pose: "stand", gesture: "custom_nod", expression: "happy" }, pack);
  assert.equal(res.mode, "full");
  assert.equal(res.gesture, "custom_nod");
  const v = rig.view("stand", "custom_nod", "happy", []);
  v.render(0.5, { x: 0, y: 0 }, null, { progress: 0.5 });
  assert.equal(gestureCalls, 1, "registered custom VRM gesture handler must be invoked on render");
  assert.equal(C.unregisterVrmGesture("custom_nod"), true);
  assert.equal(Object.hasOwn(C.vrmVocabulary(pack).gestures, "custom_nod"), false);

  rig.dispose();
});

test("VRM rail dock disables dragging, zeroes vertical lift/hop, survives rail DOM swaps, and renders FX inside viewport bounds", async () => {
  let now = 0, raf = null, rafId = 0;
  let mutationCb = null;
  const storage = new Map();
  const docListeners = {};
  const doc = {
    hidden: false,
    documentElement: {
      dataset: { rid: "s1" },
      classList: { contains: () => false },
      hasAttribute: () => true,
      addEventListener() {}
    },
    body: null,
    getElementById: id => id === "rail" ? railEl : null,
    createElement: tag => makeFakeElement(tag, doc),
    createElementNS: (_ns, tag) => makeFakeElement(tag, doc),
    addEventListener: (type, fn) => { (docListeners[type] ||= []).push(fn); }
  };
  doc.body = makeFakeElement("body", doc);
  let slotEl = makeFakeElement("div", doc);
  slotEl.id = "rail-companion-slot";
  slotEl.hidden = true;
  slotEl.getBoundingClientRect = () => slotEl.hidden
    ? { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 }
    : { left: 12, top: 610, right: 352, bottom: 850, width: 340, height: 240 };
  const railEl = makeFakeElement("aside", doc);
  railEl.id = "rail";
  railEl.getBoundingClientRect = () => ({ left: 12, top: 12, right: 352, bottom: 948, width: 340, height: 936 });
  railEl.querySelector = sel => sel === "#rail-companion-slot" ? slotEl : null;
  railEl.querySelectorAll = sel => sel.includes("#rail-companion-slot") ? [slotEl] : [];

  const fakeThree = makeFakeVrmRuntime("1");
  const { C } = loadCompanion({
    document: doc,
    getComputedStyle: () => ({ visibility: "visible", display: "flex" }),
    window: {
      innerWidth: 1680,
      innerHeight: 960,
      devicePixelRatio: 1,
      matchMedia: q => ({ matches: q.includes("1600"), addEventListener() {} }),
      localStorage: {
        getItem: k => storage.has(k) ? storage.get(k) : null,
        setItem: (k, v) => storage.set(k, String(v)),
        removeItem: k => storage.delete(k)
      },
      addEventListener() {},
      WsShell: { companionContext: (routeKind = "home") => ({ routeKind, activity: "idle", landmarks: {}, events: [], rightColActive: false, drawerOpen: false }) },
      wsCompanionModalRows: []
    },
    ResizeObserver: class { observe() {} disconnect() {} },
    MutationObserver: class {
      constructor(cb) { mutationCb = cb; }
      observe() {}
      disconnect() {}
    },
    performance: { now: () => now },
    wsT: k => k,
    requestAnimationFrame: fn => { raf = fn; return ++rafId; },
    cancelAnimationFrame: () => { raf = null; },
    fetch: async url => {
      if (url.includes("/api/companion/packs?")) {
        return { ok: true, json: async () => ({ resolvedAuto: { packId: "vrm1" }, packs: [{ id: "vrm1", rev: "rv1", renderer: "vrm", schema: "ws-companion-vrm/v1" }] }) };
      }
      if (url === "/api/companion/packs") {
        return { ok: true, json: async () => ({ packs: [{ id: "vrm1", rev: "rv1", renderer: "vrm", schema: "ws-companion-vrm/v1" }] }) };
      }
      if (url.includes("/vrm1/pack.json")) {
        return { ok: true, json: async () => ({ schema: "ws-companion-vrm/v1", id: "vrm1", model: "a.vrm" }) };
      }
      return { ok: true, status: 200, arrayBuffer: async () => new Uint8Array([1, 2, 3, 4]).buffer };
    }
  });
  C.quickMenu = () => ({ rebuild() {}, measure() {}, place() {}, close() {} });
  C.vrmRuntimeLoader = async () => fakeThree.runtime;

  const stage = C.mount({ pack: "vrm1", preset: "" }, () => {});
  for (let i = 0; i < 6; i++) await new Promise(r => setImmediate(r));
  const step = (dt = 40) => { const fn = raf; raf = null; if (fn) { now += dt; fn(now); } };
  step();

  const rootNode = doc.body.children[0];
  const hitEl = rootNode.all().find(el => el.className === "ws-companion-hit");
  const charEl = rootNode.all().find(el => el.className === "ws-companion-character");
  const startPos = { ...stage.debugState().position };

  // Attempting to drag the rail-docked VRM must NOT detach it from its rail slot.
  charEl.dispatchEvent({ type: "pointerdown", button: 0, pointerId: 1, clientX: 180, clientY: 720 });
  charEl.dispatchEvent({ type: "pointermove", pointerId: 1, clientX: 600, clientY: 400 });
  charEl.dispatchEvent({ type: "pointerup", pointerId: 1, clientX: 600, clientY: 400 });
  step(40);
  assert.equal(stage.debugState().position.x, startPos.x, "rail-docked VRM must not drag horizontally");
  assert.equal(stage.debugState().position.y, startPos.y, "rail-docked VRM must not drag vertically");

  // Trigger hop-bounce scenario (e.g. celebrate) -> VRM must have ZERO vertical lift/hop.
  const parseY = tf => {
    const m = /translate3d\([^,]+,\s*([-\d.]+)px/.exec(tf || "");
    return m ? Number(m[1]) : NaN;
  };
  const baseY = parseY(hitEl.style.transform);
  assert.equal(stage.command({ sessionId: "s1", scenario: "celebrate", fx: "alert", ttl_ms: 3000 }), true);
  for (let i = 0; i < 15; i++) {
    step(40);
    const curY = parseY(hitEl.style.transform);
    assert.ok(Math.abs(curY - baseY) < 0.01, `VRM must not hop vertically during celebrate (baseY=${baseY}, curY=${curY})`);
  }

  // FX badge must be visible and inside viewport bounds (in document.body, outside rail clip).
  const fxEl = rootNode.all().find(el => el.className === "ws-companion-fx");
  assert.ok(fxEl && !fxEl.hidden, "FX badge should be visible");
  const fxY = parseY(fxEl.style.transform);
  assert.ok(fxY >= 8 && fxY < 960 - 8, `FX badge Y (${fxY}) escaped viewport bounds`);

  // Simulate #r-rail innerHTML swap replacing #rail-companion-slot with a fresh hidden node:
  // WebGL context survives (still 1 renderer) and the new slot is immediately synced visible.
  const nextSlot = makeFakeElement("div", doc);
  nextSlot.id = "rail-companion-slot";
  nextSlot.hidden = true;
  nextSlot.getBoundingClientRect = () => nextSlot.hidden
    ? { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 }
    : { left: 12, top: 620, right: 352, bottom: 860, width: 340, height: 240 };
  slotEl = nextSlot;
  mutationCb?.();
  step(40);
  assert.equal(nextSlot.hidden, false, "new #rail-companion-slot after rail swap must become visible");
  assert.equal(stage.debugState().home.y, 860, "stage must re-anchor to swapped slot rect");
  assert.equal(fakeThree.stats.renderersCreated, 1, "rail swap must never recreate WebGL context");

  stage.dispose();
});

test("Optional .vrma clip playback cross-fades on matching gesture/pose and negatively caches failed loads", async () => {
  const doc = { createElement: tag => makeFakeElement(tag, doc), addEventListener() {} };
  const fakeThree = makeFakeVrmRuntime("1");
  let clipLoads = 0, fadeIns = 0, fadeOuts = 0, mixerUpdates = 0;
  fakeThree.runtime.createVRMAnimationClip = () => { clipLoads++; return { name: "typing-clip" }; };
  fakeThree.runtime.THREE.AnimationMixer = class {
    update() { mixerUpdates++; }
    stopAllAction() {}
    clipAction() {
      return {
        reset() {},
        fadeIn() { fadeIns++; },
        fadeOut() { fadeOuts++; },
        play() {},
        stop() {},
        crossFadeTo() {}
      };
    }
  };
  const origParse = fakeThree.runtime.GLTFLoader.prototype.parseAsync;
  fakeThree.runtime.GLTFLoader.prototype.parseAsync = async function (buf) {
    if (buf.byteLength === 2) return { userData: { vrmAnimations: [{ mock: true }] } };
    if (buf.byteLength === 3) return { userData: { vrmAnimations: [] } };
    return origParse.call(this, buf);
  };

  const fetched = [];
  const { C } = loadCompanion({
    document: doc,
    window: { innerWidth: 1200, innerHeight: 800, devicePixelRatio: 1, addEventListener() {} },
    fetch: async url => {
      fetched.push(url);
      if (url.includes("typing.vrma")) return { ok: true, status: 200, arrayBuffer: async () => new Uint8Array([9, 9]).buffer };
      if (url.includes("broken.vrma")) return { ok: false, status: 404, arrayBuffer: async () => new Uint8Array(0).buffer };
      if (url.includes("empty.vrma")) return { ok: true, status: 200, arrayBuffer: async () => new Uint8Array([1, 2, 3]).buffer };
      return { ok: true, status: 200, arrayBuffer: async () => new Uint8Array([1, 2, 3, 4]).buffer };
    }
  });
  C.vrmRuntimeLoader = async () => fakeThree.runtime;

  const pack = {
    id: "vrm-vrma", rev: "r1", renderer: "vrm", modelFile: "avatar.vrm",
    manifest: {}, expressionMap: {},
    animations: { typing: "typing.vrma", wave: "broken.vrma", point: "empty.vrma" }
  };
  const rig = C.makeVrmRig(pack);
  await rig.mount(makeFakeElement("div", doc), { width: 240, height: 280, dpr: 1 });

  // Trigger typing -> fetches typing.vrma and activates mixer.
  const vTyping = rig.view("typing", null, "focused", []);
  vTyping.render(0.1, { x: 0, y: 0 }, null, {});
  for (let i = 0; i < 6; i++) await new Promise(r => setImmediate(r));
  vTyping.render(0.2, { x: 0, y: 0 }, null, {});
  assert.equal(fetched.some(u => u.includes("typing.vrma")), true, "matching animations.typing must fetch typing.vrma");
  assert.equal(clipLoads, 1);
  assert.equal(fadeIns, 1);
  assert.equal(rig.debugState().activeVrmaKey, "typing");
  assert.ok(mixerUpdates >= 1);

  // Return to stand -> fades out .vrma cleanly and resumes procedural stand pose.
  const vStand = rig.view("stand", null, "idle", []);
  vStand.render(0.3, { x: 0, y: 0 }, null, {});
  vStand.render(0.55, { x: 0, y: 0 }, null, {});
  assert.equal(fadeOuts, 1);
  assert.equal(rig.debugState().activeVrmaKey, "");

  // Failed (404) and empty .vrma loads must be negatively cached (1 fetch total across many frames).
  const vWave = rig.view("stand", "wave", "happy", []);
  for (let i = 0; i < 8; i++) {
    vWave.render(0.6 + i * 0.05, { x: 0, y: 0 }, null, {});
    await new Promise(r => setImmediate(r));
  }
  assert.equal(fetched.filter(u => u.includes("broken.vrma")).length, 1, "failed .vrma must be negatively cached");

  const vPoint = rig.view("stand", "point", "oo", []);
  for (let i = 0; i < 8; i++) {
    vPoint.render(1.1 + i * 0.05, { x: 0, y: 0 }, null, {});
    await new Promise(r => setImmediate(r));
  }
  assert.equal(fetched.filter(u => u.includes("empty.vrma")).length, 1, "empty .vrma must be negatively cached");

  rig.dispose();
});
