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
  for (const name of ["builtins", "packs", "focus", "deform", "compositor", "scenarios", "menu", "vrm-fx", "vrm-studio", "vrm-props", "vrm-pose", "vrm-colead", "vrm", "stage"]) {
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
    classList: {
      add(...names) {
        const cur = new Set(el.className.split(/\s+/).filter(Boolean));
        names.forEach(n => n && cur.add(n));
        el.className = [...cur].join(" ");
      },
      remove(...names) {
        const cur = new Set(el.className.split(/\s+/).filter(Boolean));
        names.forEach(n => cur.delete(n));
        el.className = [...cur].join(" ");
      },
      contains(n) { return el.className.split(/\s+/).includes(n); }
    },
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
    prepend(...items) { this.children.unshift(...items); },
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
        translate() {},
        rotate() {},
        beginPath() {},
        closePath() {},
        moveTo() {},
        lineTo() {},
        arc() {},
        ellipse() {},
        stroke() {},
        fill() {},
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
    let slotW = 340, unregOpen = false;
    slotEl.getBoundingClientRect = () => slotEl.hidden
      ? { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 }
      : { left: 12, top: vpH - 240, right: 12 + slotW, bottom: vpH, width: slotW, height: 240 };
    const unregEl = makeFakeElement("details", doc);
    const railEl = makeFakeElement("aside", doc);
    railEl.id = "rail";
    railEl.getBoundingClientRect = () => ({ left: 12, top: 12, right: 12 + slotW, bottom: vpH, width: slotW, height: vpH - 12 });
    railEl.querySelector = sel => {
      if (sel === "#rail-companion-slot") return slotEl;
      if (sel === "details.rws-unreg[open]") return unregOpen ? unregEl : null;
      return null;
    };
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
    assert.equal(initial.home.y, 960);
    assert.equal(initial.avoiding, false);

    // Expanding details.rws-unreg must NOT shrink --companion-slot or shift home.y.
    const slotVarBefore = railEl.style.getPropertyValue("--companion-slot");
    unregOpen = true;
    for (const fn of winListeners.resize || []) fn();
    step();
    assert.equal(railEl.style.getPropertyValue("--companion-slot"), slotVarBefore, "--companion-slot must remain invariant when details.rws-unreg opens");
    assert.equal(stage.debugState().home.y, 960);
    assert.equal(stage.debugState().vrmHeight, 240);
    unregOpen = false;

    // Resizing the left rail column (220px..360px) resizes the VRM slot width smoothly without a 340px cap.
    slotW = 360;
    for (const fn of winListeners.resize || []) fn();
    step();
    assert.equal(stage.debugState().size, 360);
    slotW = 340;
    for (const fn of winListeners.resize || []) fn();
    step();
    assert.equal(stage.debugState().size, 340);

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

test("companion settings section hides 2D paper-doll wardrobe controls when active pack is VRM", async () => {
  const doc = {
    documentElement: { hasAttribute: () => true },
    createElement: tag => makeFakeElement(tag, doc),
    createElementNS: (_ns, tag) => makeFakeElement(tag, doc),
    querySelector: () => null,
    addEventListener() {}
  };
  const win = { innerWidth: 1200, innerHeight: 800, addEventListener() {} };
  const catalogPacks = {
    resolvedAuto: { packId: "vrm1" },
    packs: [
      { id: "vrm1", name: "Studio VRM", rev: "rv1", renderer: "vrm", source: "local" },
      { id: "default", name: "Default 2D", rev: "d1", renderer: "2d", source: "builtin", builtin: true }
    ]
  };
  const manifest2d = {
    id: "default",
    presets: { day: {} },
    outfits: { street: {} },
    hairs: { bob: {} },
    eyesets: { round: {} },
    attachments: { tail1: { slot: "tail" } },
    palette: { hair: "#112233" }
  };
  const ctx = vm.createContext({
    window: win, document: doc, URL, URLSearchParams,
    Option: class { constructor(text, value, _ds, sel) { this.tagName = "OPTION"; this.textContent = text; this.value = value; this.selected = sel; } },
    fetch: async url => {
      if (url === "/api/companion/packs") return { ok: true, status: 200, json: async () => catalogPacks };
      if (String(url).includes("/default/pack.json")) return { ok: true, status: 200, json: async () => manifest2d };
      return { ok: true, status: 200, json: async () => ({}) };
    }
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", "dashboard-settings-companion.js"), "utf8"), ctx);

  const catalog = JSON.parse(fs.readFileSync(path.join(__dirname, "assets", "dashboard-settings-catalog.json"), "utf8"));
  const specs = new Map(catalog.map(s => [s.key, s]));
  let activePack = "vrm1";
  const makeEntries = () => catalog
    .filter(s => s.section === "companion" && (!s.surface || s.surface === "settings"))
    .map(s => ({
      key: s.key,
      value: s.key === "dashboard.companion_pack" ? activePack : (s.key === "dashboard.companion_enabled" ? true : (s.key === "dashboard.companion_director" ? "rules" : "")),
      default: "",
      editable: true
    }));

  const el = (tag, cls, text) => {
    const node = makeFakeElement(tag, doc);
    if (cls) node.className = cls;
    if (text !== undefined && text !== "") node.textContent = String(text);
    if (tag === "select") node.add = opt => node.children.push(opt);
    return node;
  };
  const append = (parent, ...kids) => { parent.append(...kids.filter(Boolean)); return parent; };
  const button = (cls, text, onClick) => {
    const btn = el("button", cls, text);
    if (onClick) btn.addEventListener("click", onClick);
    return btn;
  };
  const renderOpts = () => ({
    entries: makeEntries(),
    scope: "global",
    filter: "all",
    searchTerm: "",
    tr: k => k,
    el, append, button,
    svgIcon: () => el("svg"),
    specFor: k => specs.get(k),
    selected: e => e.value,
    editable: () => true,
    matches: () => true,
    renderRow: e => { const r = el("article", "krow"); r.dataset.configKey = e.key; return r; },
    renderKeyProvenance: () => ({ node: el("span"), drawer: null }),
    submitConfig: async (e, _a, v) => { if (e.key === "dashboard.companion_pack") activePack = v; return true; },
    rerender() {},
    notifyPacksChanged() {},
    notifyStageAction() {}
  });

  // Initial render fetches /api/companion/packs, second render reads cached catalog.
  win.WsSettingsCompanion.render(renderOpts());
  await new Promise(r => setImmediate(r));
  const vrmPanel = win.WsSettingsCompanion.render(renderOpts());
  assert.ok(vrmPanel.all().some(n => n.dataset && n.dataset.companionWardrobeVrmNote === "1"),
    "VRM mode must show the VRM appearance note instead of 2D wardrobe controls");
  assert.equal(vrmPanel.all().some(n => n.dataset && n.dataset.companionWardrobeGrid === "1"), false,
    "VRM mode must hide 2D paper-doll wardrobe grid");
  assert.ok(vrmPanel.all().some(n => (n.dataset && n.dataset.companionSwitch2d === "1") || n.getAttribute("data-companion-switch-2d") === "1"),
    "VRM note provides a quick switch-to-2D button");

  // Switching to 2D fetches pack.json and renders 2D wardrobe controls.
  activePack = "default";
  win.WsSettingsCompanion.render(renderOpts());
  await new Promise(r => setImmediate(r));
  const panel2d = win.WsSettingsCompanion.render(renderOpts());
  assert.equal(panel2d.all().some(n => n.dataset && n.dataset.companionWardrobeVrmNote === "1"), false);
  const grid = panel2d.all().find(n => n.dataset && n.dataset.companionWardrobeGrid === "1");
  assert.ok(grid, "2D mode renders the 2D wardrobe grid");
  const keys = grid.all().map(n => n.dataset && n.dataset.configKey).filter(Boolean);
  for (const want of ["dashboard.companion_preset", "dashboard.companion_outfit", "dashboard.companion_hair", "dashboard.companion_eyes", "dashboard.companion_attachments", "dashboard.companion_palette"]) {
    assert.ok(keys.includes(want), `2D mode should include wardrobe field ${want}`);
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

test("getCharacterBounds projects vrm.scene character meshes only (excluding scene props), caches within TTL, and updates on resize", async () => {
  const { pathToFileURL } = require("node:url");
  const rt = await import(pathToFileURL(path.join(__dirname, "assets", "companion-vrm-runtime.mjs")).href);
  const { THREE } = rt;

  const root = new THREE.Group();
  const head = new THREE.Object3D();
  head.name = "head";
  head.position.set(0, 1.45, 0);
  root.add(head);
  const chest = new THREE.Object3D();
  chest.name = "chest";
  chest.position.set(0, 1.18, 0);
  root.add(chest);
  const bodyMesh = new THREE.Mesh(new THREE.BoxGeometry(0.32, 0.56, 0.22), new THREE.MeshBasicMaterial());
  bodyMesh.position.set(0, 1.32, 0);
  root.add(bodyMesh);
  root.updateMatrixWorld(true);

  const presets = { neutral: {}, blink: {}, happy: {}, sad: {}, angry: {}, relaxed: {}, surprised: {} };
  const vrmObj = {
    meta: { metaVersion: "1" },
    scene: root,
    humanoid: { getNormalizedBoneNode: name => (name === "head" ? head : name === "chest" ? chest : null) },
    expressionManager: {
      presetExpressionMap: presets,
      customExpressionMap: {},
      expressions: Object.keys(presets).map(expressionName => ({ expressionName })),
      setValue() {}
    },
    lookAt: { target: null },
    update() { root.updateMatrixWorld(true); }
  };

  let capturedScene = null;
  class StubRenderer {
    constructor() { this.shadowMap = { enabled: true }; this.info = { render: { calls: 1, triangles: 12 } }; }
    setClearColor() {}
    setPixelRatio() {}
    setSize() {}
    render(sc) { capturedScene = sc; }
    dispose() {}
    forceContextLoss() {}
  }

  let nowMs = 100;
  let resolveFetch = null;
  const doc = { createElement: tag => makeFakeElement(tag, doc), addEventListener() {} };
  const { C } = loadCompanion({
    document: doc,
    performance: { now: () => nowMs },
    window: { innerWidth: 1680, innerHeight: 960, devicePixelRatio: 1, addEventListener() {} },
    fetch: () => new Promise(resolve => {
      resolveFetch = () => resolve({ ok: true, status: 200, arrayBuffer: async () => new Uint8Array([1, 2, 3, 4]).buffer });
    })
  });
  C.vrmRuntimeLoader = async () => ({
    THREE: { ...THREE, WebGLRenderer: StubRenderer },
    GLTFLoader: class {
      register() {}
      async parseAsync() { return { scene: vrmObj.scene, userData: { vrm: vrmObj } }; }
    },
    VRMLoaderPlugin: class {},
    VRMAnimationLoaderPlugin: class {},
    VRMUtils: { removeUnnecessaryVertices() {}, combineSkeletons() {}, rotateVRM0() {}, deepDispose() {} },
    createVRMAnimationClip: () => ({})
  });

  const rig = C.makeVrmRig({ id: "vrm-bbox", rev: "r1", renderer: "vrm", modelFile: "avatar.vrm", manifest: {}, expressionMap: {}, animations: {} });
  const mountPromise = rig.mount(makeFakeElement("div", doc), { width: 340, height: 240, dpr: 1 });
  for (let i = 0; i < 4; i++) await new Promise(r => setImmediate(r));
  assert.equal(rig.getCharacterBounds(), null, "before VRM loads, getCharacterBounds() returns null");

  resolveFetch();
  await mountPromise;

  // Add a huge wide prop directly to scene (outside vrm.scene) to prove props are excluded.
  const hugeProp = new THREE.Mesh(new THREE.BoxGeometry(10, 10, 0.2), new THREE.MeshBasicMaterial());
  hugeProp.position.set(0, 1.3, 0.1);
  capturedScene.add(hugeProp);
  capturedScene.updateMatrixWorld(true);

  const b1 = rig.getCharacterBounds();
  assert.ok(b1, "loaded VRM must produce projected character bounds");
  assert.ok(b1.x > 40 && b1.x + b1.width < 300, `character bounds must exclude wide scene props (got x=${b1.x}, w=${b1.width})`);
  assert.ok(b1.width > 20 && b1.height > 40);
  assert.deepEqual(rig.debugState().projectedBounds, b1);

  // Within TTL (120ms), cached bounds are returned even if mesh moves; after TTL, bounds recompute.
  bodyMesh.position.x = 0.25;
  nowMs += 40;
  const bCached = rig.getCharacterBounds();
  assert.deepEqual(bCached, b1, "bounds should stay cached within 120ms TTL");

  nowMs += 150;
  const bMoved = rig.getCharacterBounds();
  assert.ok(bMoved.x > b1.x + 15, `bounds should refresh after TTL (before=${b1.x}, after=${bMoved.x})`);

  // Resize invalidates cache immediately and updates bounds to new viewport width.
  rig.resize(240, 240, 1);
  const bResized = rig.getCharacterBounds();
  assert.ok(bResized && bResized.x + bResized.width <= 240, `resized bounds must fit 240px width (got ${JSON.stringify(bResized)})`);

  rig.dispose();
  assert.equal(rig.getCharacterBounds(), null);
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
      constructor(cb) { this.cb = cb; }
      observe(target) { if (target === railEl) mutationCb = this.cb; }
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

test("Co-Lead typing state machine covers idle bursts, working cadence, sleepy pause, reaction pause, hover & periodic glance-up, accent API, and finite bones", () => {
  const { C } = loadCompanion();
  assert.equal(typeof C.createVrmCoLead, "function", "createVrmCoLead must be exported");
  assert.equal(typeof C.vrmCoLeadMath?.computeCoLeadTargets, "function");

  const co = C.createVrmCoLead({});

  // 1. Default idle starts in typing burst, transitions to reading, and periodically glances up.
  let sawTyping = false, sawReading = false, sawGlance = false;
  const glanceRolls = [];
  let t = 0;
  let prevGlance = false;
  for (let i = 0; i < 420; i++) {
    t += 0.05;
    const out = co.step({ dt: 0.05, elapsed: t, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
    for (const [bone, rot] of Object.entries(out.boneTargets)) {
      assert.equal(rot.length, 3, `${bone} must have 3 Euler components`);
      for (const v of rot) assert.ok(Number.isFinite(v), `${bone} has non-finite rotation ${v}`);
    }
    assert.ok(Number.isFinite(out.eyeYaw) && Number.isFinite(out.eyePitch));
    assert.ok(Number.isFinite(out.screenGlow) && out.screenGlow > 0);
    if (out.beat === "typing") sawTyping = true;
    if (out.beat === "reading") sawReading = true;
    if (out.beat === "glance-up") {
      sawGlance = true;
      if (!prevGlance && Math.abs(out.boneTargets.head[2]) > 0.01) {
        glanceRolls.push(Math.sign(out.boneTargets.head[2]));
      }
      prevGlance = true;
    } else {
      prevGlance = false;
    }
  }
  assert.equal(sawTyping, true, "idle must enter typing bursts");
  assert.equal(sawReading, true, "idle must take calm reading pauses");
  assert.equal(sawGlance, true, "idle must periodically glance up at the user");

  // 2. Hover immediately triggers glance-up with warm smile (happy ~0.35-0.48) and head tilt.
  let hoveredOut = null;
  for (let i = 0; i < 40; i++) {
    t += 0.05;
    hoveredOut = co.step({ dt: 0.05, elapsed: t, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: true });
  }
  assert.equal(hoveredOut.glanceUp, true, "hover must trigger glance-up");
  assert.equal(hoveredOut.typing, false, "hover glance-up must pause typing");
  assert.ok(hoveredOut.exprOverlay.happy >= 0.35 && hoveredOut.exprOverlay.happy <= 0.48,
    `glance-up smile happy=${hoveredOut.exprOverlay.happy} should be in [0.35, 0.48]`);
  assert.ok(Math.abs(hoveredOut.boneTargets.head[2] + hoveredOut.boneTargets.neck[2]) > 0.08,
    "glance-up must include charming anime head-tilt roll");

  // Leaving hover returns smoothly to typing.
  let postHoverOut = null;
  for (let i = 0; i < 60; i++) {
    t += 0.05;
    postHoverOut = co.step({ dt: 0.05, elapsed: t, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
  }
  assert.equal(postHoverOut.glanceUp, false, "leaving hover must return from glance-up");

  // 3. Working/thinking state boosts screen glow and keeps brisk typing/reading rhythm without glancing away.
  let workingOut = null;
  let workingTypingFrames = 0;
  for (let i = 0; i < 60; i++) {
    t += 0.05;
    workingOut = co.step({ dt: 0.05, elapsed: t, expression: "focused", rawPose: "typing", scenarioId: "working_tatata", gazeX: 0, gazeY: 0, hover: false });
    if (workingOut.typing) workingTypingFrames++;
  }
  assert.ok(workingTypingFrames > 25, "working state must actively type");
  assert.ok(workingOut.screenGlow > 0.19, `working screen glow (${workingOut.screenGlow}) should brighten`);

  // 4. Sleepy state stops typing cleanly and dims screen bounce glow.
  let sleepyOut = null;
  for (let i = 0; i < 50; i++) {
    t += 0.05;
    sleepyOut = co.step({ dt: 0.05, elapsed: t, expression: "idle_doze", rawPose: "stand", beat: { fx: "zzz" }, gazeX: 0, gazeY: 0, hover: false });
  }
  assert.equal(sleepyOut.typing, false, "sleepy must stop typing");
  assert.ok(sleepyOut.screenGlow < 0.06, `sleepy screen glow (${sleepyOut.screenGlow}) should dim softly`);
  assert.ok(sleepyOut.boneTargets.head[0] + sleepyOut.boneTargets.neck[0] > 0.08, "sleepy should softly nod head down");

  // 5. Reaction state (alert/error/celebrate) pauses typing immediately so reactions read crisply.
  let reactionOut = null;
  for (let i = 0; i < 20; i++) {
    t += 0.05;
    reactionOut = co.step({ dt: 0.05, elapsed: t, expression: "happy", rawPose: "stand", scenarioId: "celebrate_done", gazeX: 0.3, gazeY: -0.1, hover: false });
  }
  assert.equal(reactionOut.typing, false, "reaction scenario must pause typing");

  // 6. Step 3 seams: setLaptopAccent validates hex and setCoLeadBeat deterministically drives beats.
  assert.equal(co.setLaptopAccent("#f59e0b"), "#f59e0b");
  assert.equal(co.setLaptopAccent("#abc"), "#aabbcc");
  assert.equal(co.setLaptopAccent("not-a-color"), "#aabbcc", "invalid accent must be ignored");
  assert.equal(co.setCoLeadBeat("glance-up"), "glance-up");
  assert.equal(co.debugState().beat, "glance-up");
  assert.equal(co.setCoLeadBeat("sleepy"), "sleepy");
  const forcedSleepy = co.step({ dt: 0.05, elapsed: t + 0.05, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
  assert.equal(forcedSleepy.exprOverlay?.mode, "sleepy");
  assert.equal(co.setCoLeadBeat("auto"), "auto");
  co.dispose();
});

test("Co-Lead 3D beveled laptop prop frames strictly in bottom <=19% below chin on BOTH VRM 0.x and VRM 1.0 and disposes cleanly on swap", async () => {
  const { pathToFileURL } = require("node:url");
  const rt = await import(pathToFileURL(path.join(__dirname, "assets", "companion-vrm-runtime.mjs")).href);
  const { THREE } = rt;

  function makeBustVrm(metaVersion) {
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
    const hips = addBone("hips", root, 0, 0.90, 0);
    const spine = addBone("spine", hips, 0, 0.10, 0);
    const chest = addBone("chest", spine, 0, 0.15, 0);
    const upperChest = addBone("upperChest", chest, 0, 0.10, 0);
    const neck = addBone("neck", upperChest, 0, 0.10, 0);
    const head = addBone("head", neck, 0, 0.08, 0);
    const lSign = is0 ? -1 : 1;
    addBone("leftShoulder", upperChest, lSign * 0.06, 0.04, 0);
    const lU = addBone("leftUpperArm", upperChest, lSign * 0.16, 0.04, 0);
    const lL = addBone("leftLowerArm", lU, lSign * 0.22, 0, 0);
    addBone("leftHand", lL, lSign * 0.20, 0, 0);
    addBone("rightShoulder", upperChest, -lSign * 0.06, 0.04, 0);
    const rU = addBone("rightUpperArm", upperChest, -lSign * 0.16, 0.04, 0);
    const rL = addBone("rightLowerArm", rU, -lSign * 0.22, 0, 0);
    addBone("rightHand", rL, -lSign * 0.20, 0, 0);

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

  for (const metaVer of ["1", "0"]) {
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

    const vrmObj = makeBustVrm(metaVer);
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

    const doc = { createElement: tag => makeFakeElement(tag, doc), addEventListener() {} };
    const { C } = loadCompanion({
      document: doc,
      window: { innerWidth: 1680, innerHeight: 960, devicePixelRatio: 1, addEventListener() {} },
      fetch: async () => ({ ok: true, status: 200, arrayBuffer: async () => new Uint8Array([1, 2, 3, 4]).buffer })
    });
    C.vrmRuntimeLoader = async () => stubRuntime;

    const rig = C.makeVrmRig({ id: `vrm-${metaVer}`, rev: "r1", renderer: "vrm", modelFile: "avatar.vrm", manifest: {}, expressionMap: {}, animations: {} });
    await rig.mount(makeFakeElement("div", doc), { width: 340, height: 240, dpr: 1 });

    // Verify beveled ExtrudeGeometry laptop prop is mounted in scene (never raw BoxGeometry).
    const laptopGroup = capturedScene.children.find(c => c.name === "ws-colead-laptop");
    assert.ok(laptopGroup, `VRM ${metaVer}: sleek laptop group must be added to scene`);
    assert.equal(laptopGroup.children[0].geometry.type, "ExtrudeGeometry", "laptop lid must use beveled ExtrudeGeometry");

    // Verify strict framing invariant: top of laptop lid strictly in bottom <=19% and well below chin!
    const coSt = rig.debugState().coLead;
    assert.equal(coSt.hasLaptopProp, true);
    assert.ok(coSt.laptopTopViewportPct >= 10 && coSt.laptopTopViewportPct <= 19.5,
      `VRM ${metaVer}: laptop top (${coSt.laptopTopViewportPct}%) must sit cleanly in bottom 10-19.5%`);
    assert.ok(coSt.laptopTopViewportPct < coSt.chinViewportPct - 20,
      `VRM ${metaVer}: laptop top (${coSt.laptopTopViewportPct}%) must stay far below chin (${coSt.chinViewportPct}%)`);

    // Verify elbows bend forward (+Z in world space) and NEVER backward on BOTH VRM 0.x and VRM 1.0!
    vrmObj.scene.updateMatrixWorld(true);
    const lHandPos = new THREE.Vector3();
    const lElbowPos = new THREE.Vector3();
    const rHandPos = new THREE.Vector3();
    const rElbowPos = new THREE.Vector3();
    vrmObj.bones.get("leftHand").getWorldPosition(lHandPos);
    vrmObj.bones.get("leftLowerArm").getWorldPosition(lElbowPos);
    vrmObj.bones.get("rightHand").getWorldPosition(rHandPos);
    vrmObj.bones.get("rightLowerArm").getWorldPosition(rElbowPos);
    assert.ok(lHandPos.z > lElbowPos.z + 0.08, `VRM ${metaVer}: left forearm must bend forward toward keyboard (handZ=${lHandPos.z.toFixed(3)}, elbowZ=${lElbowPos.z.toFixed(3)})`);
    assert.ok(rHandPos.z > rElbowPos.z + 0.08, `VRM ${metaVer}: right forearm must bend forward toward keyboard (handZ=${rHandPos.z.toFixed(3)}, elbowZ=${rElbowPos.z.toFixed(3)})`);

    // Verify global Step 3 / QA seams forward cleanly to active rig.
    assert.equal(C.setLaptopAccent("#38bdf8"), "#38bdf8");
    assert.equal(C.setCoLeadBeat("glance-up"), "glance-up");
    assert.equal(rig.debugState().coLead.laptopAccent, "#38bdf8");
    assert.equal(rig.debugState().coLead.beat, "glance-up");

    // Verify disposal cleanly removes laptopGroup and screenLight from scene.
    const pointLightsBefore = capturedScene.children.filter(c => c.isPointLight).length;
    assert.equal(pointLightsBefore, 1, "screen bounce PointLight should be attached");
    rig.dispose();
    assert.equal(capturedScene.children.some(c => c.name === "ws-colead-laptop"), false, "dispose must remove laptop prop from scene");
    assert.equal(capturedScene.children.filter(c => c.isPointLight).length, 0, "dispose must remove screen bounce light from scene");
  }
});

test("Step 3: deterministic 6-color session palette, interruptible laptop swap, rapid-switch dizzy egg, recovery, and cooldown", () => {
  const { C } = loadCompanion();
  assert.equal(typeof C.sessionAccentForKey, "function", "sessionAccentForKey must be exported");
  const palette = C.vrmCoLeadMath.SESSION_ACCENT_PALETTE;
  assert.equal(palette.length, 6, "palette must have 6 curated colors");

  // Deterministic mapping and spread across all 6 hues.
  const seenHex = new Set();
  for (let i = 1; i <= 30; i++) {
    const rid = `session-${i}`;
    const a1 = C.sessionAccentForKey(rid);
    const a2 = C.sessionAccentForKey(rid);
    assert.equal(a1.hex, a2.hex, "sessionAccentForKey must be deterministic");
    seenHex.add(a1.hex);
  }
  assert.equal(seenHex.size, 6, "sessionAccentForKey must span all 6 palette colors");

  // Pick two distinct sessions with different accents.
  const ridA = "alpha01";
  const accentA = C.sessionAccentForKey(ridA).hex;
  let ridB = "alpha02";
  for (let i = 2; i <= 20; i++) {
    const cand = `alpha${String(i).padStart(2, "0")}`;
    if (C.sessionAccentForKey(cand).hex !== accentA) { ridB = cand; break; }
  }
  const accentB = C.sessionAccentForKey(ridB).hex;
  assert.notEqual(accentA, accentB);

  const co = C.createVrmCoLead({});
  co.syncInitialSession(ridA);
  assert.equal(co.getCoLeadState().activeSessionId, ridA);
  assert.equal(co.getCoLeadState().laptopAccent, accentA);
  assert.equal(co.getCoLeadState().swapActive, false, "initial sync must not animate swap");

  // Idempotency: re-selecting the same session ID must NOT trigger a swap.
  const sameRes = co.onSessionSwitch(ridA, { nowMs: 100 });
  assert.equal(sameRes.swapped, false, "same rid must not trigger redundant swap");
  assert.equal(co.getCoLeadState().swapActive, false);

  // Full swap lifecycle: tuck-away (old color) -> trough color switch -> slide-in (new color) -> settle -> typing.
  const swRes = co.onSessionSwitch(ridB, { nowMs: 200 });
  assert.equal(swRes.swapped, true);
  assert.equal(swRes.dizzyTriggered, false);
  assert.equal(co.getCoLeadState().laptopAccent, accentA, "outgoing laptop keeps old color before trough");
  assert.equal(co.getCoLeadState().targetLaptopAccent, accentB);

  let t = 0;
  let sawSwapOut = false, sawSwapIn = false, sawSwapSettle = false;
  let minOffsetY = 0;
  let colorSwitchedAt = -1;
  for (let i = 0; i < 24; i++) {
    t += 0.05;
    const out = co.step({ dt: 0.05, elapsed: t, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
    const st = co.getCoLeadState();
    for (const [bone, rot] of Object.entries(out.boneTargets)) {
      for (const v of rot) assert.ok(Number.isFinite(v), `${bone} rotation must remain finite during swap`);
    }
    assert.ok(Number.isFinite(st.liveLaptopOffsetY) && Number.isFinite(st.liveLaptopOffsetX));
    if (st.liveLaptopOffsetY < minOffsetY) minOffsetY = st.liveLaptopOffsetY;
    if (out.mode === "swap-out") sawSwapOut = true;
    if (out.mode === "swap-in") sawSwapIn = true;
    if (out.mode === "swap-settle") sawSwapSettle = true;
    if (colorSwitchedAt < 0 && st.laptopAccent === accentB) colorSwitchedAt = t;
  }
  assert.equal(sawSwapOut, true, "swap must visit swap-out tuck-away phase");
  assert.equal(sawSwapIn, true, "swap must visit swap-in slide-in phase");
  assert.equal(sawSwapSettle, true, "swap must visit swap-settle phase");
  assert.ok(minOffsetY < -0.1, `laptop must glide downward during tuck-away (minOffsetY=${minOffsetY})`);
  assert.ok(colorSwitchedAt >= 0.25 && colorSwitchedAt <= 0.45, `accent should swap at trough (~0.30s, got ${colorSwitchedAt.toFixed(2)}s)`);
  assert.equal(co.getCoLeadState().swapActive, false, "swap must finish cleanly within ~0.85s");
  assert.equal(co.getCoLeadState().laptopAccent, accentB);
  assert.equal(co.getCoLeadState().typing, true, "companion must resume typing after swap settles");

  // Mid-swap interruptibility: interrupting mid-swap continues from live offset without snapping back to 0.
  co.onSessionSwitch("mid-s1");
  for (let i = 0; i < 4; i++) {
    t += 0.05;
    co.step({ dt: 0.05, elapsed: t, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
  }
  const beforeInterruptY = co.getCoLeadState().liveLaptopOffsetY;
  assert.ok(beforeInterruptY < -0.02, "mid-swap laptop should already be moving downward");
  co.onSessionSwitch("mid-s2");
  t += 0.016;
  co.step({ dt: 0.016, elapsed: t, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
  const afterInterruptY = co.getCoLeadState().liveLaptopOffsetY;
  assert.ok(Math.abs(afterInterruptY - beforeInterruptY) < 0.06,
    `mid-swap interrupt must not snap (before=${beforeInterruptY}, after=${afterInterruptY})`);

  // Let sliding window expire (> 3.0s) before testing exact rapid-switch threshold.
  for (let i = 0; i < 70; i++) {
    t += 0.05;
    co.step({ dt: 0.05, elapsed: t, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
  }
  assert.equal(co.getCoLeadState().recentSwitchCount, 0);

  // Rapid-switch threshold: 3 distinct switches within 2.8s -> normal swap (NOT dizzy).
  for (const rid of ["burst-1", "burst-2", "burst-3"]) {
    const r = co.onSessionSwitch(rid);
    assert.equal(r.dizzyTriggered, false, "fewer than 4 switches must not trigger dizzy");
    t += 0.25;
    co.step({ dt: 0.25, elapsed: t, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
  }
  assert.equal(co.getCoLeadState().dizzyActive, false);
  assert.equal(co.getCoLeadState().recentSwitchCount, 3);

  // 4th distinct switch within 2.8s -> triggers dizzy easter egg!
  const trig = co.onSessionSwitch("burst-4");
  assert.equal(trig.dizzyTriggered, true, "4th distinct switch within 2.8s must trigger dizzy easter egg");
  assert.equal(co.getCoLeadState().dizzyActive, true);
  assert.equal(co.getCoLeadState().mode, "dizzy");

  let sawDizzySway = false, sawDizzyFx = false, sawDizzyShake = false, sawDizzySmile = false;
  for (let i = 0; i < 90; i++) {
    t += 0.05;
    const out = co.step({ dt: 0.05, elapsed: t, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
    for (const [bone, rot] of Object.entries(out.boneTargets)) {
      for (const v of rot) assert.ok(Number.isFinite(v), `${bone} rotation must remain finite during dizzy/recovery`);
    }
    if (out.mode === "dizzy") sawDizzySway = true;
    if (out.dizzyFxAlpha > 0.7) sawDizzyFx = true;
    if (out.exprOverlay?.mode === "dizzy-shake") sawDizzyShake = true;
    if (out.exprOverlay?.mode === "dizzy-recover" && out.exprOverlay.happy > 0.25) sawDizzySmile = true;
  }
  assert.equal(sawDizzySway, true, "dizzy must run sway phase");
  assert.equal(sawDizzyFx, true, "dizzy must fade in 2D anime FX alpha");
  assert.equal(sawDizzyShake, true, "dizzy recovery must include blink+head-shake beat");
  assert.equal(sawDizzySmile, true, "dizzy recovery must bloom sheepish warm smile");
  assert.equal(co.getCoLeadState().dizzyActive, false, "dizzy must recover cleanly after ~3.6s");
  assert.equal(co.getCoLeadState().dizzyFxAlpha, 0, "dizzy FX must fade completely out after recovery");
  assert.equal(co.getCoLeadState().typing, true, "companion must resume typing after recovery");

  // Cooldown prevents an immediate infinite re-trigger right after recovery.
  for (const rid of ["cool-1", "cool-2", "cool-3", "cool-4"]) {
    const r = co.onSessionSwitch(rid);
    assert.equal(r.dizzyTriggered, false, "cooldown must prevent immediate dizzy re-trigger");
    t += 0.05;
    co.step({ dt: 0.05, elapsed: t, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
  }
  assert.equal(co.getCoLeadState().dizzyActive, false);

  // After cooldown (> 1.5s), a fresh burst of 4 switches CAN trigger dizzy again.
  for (let i = 0; i < 65; i++) {
    t += 0.05;
    co.step({ dt: 0.05, elapsed: t, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
  }
  let retriggered = false;
  for (const rid of ["fresh-1", "fresh-2", "fresh-3", "fresh-4"]) {
    const r = co.onSessionSwitch(rid);
    if (r.dizzyTriggered) retriggered = true;
    t += 0.2;
    co.step({ dt: 0.2, elapsed: t, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
  }
  assert.equal(retriggered, true, "fresh rapid burst after cooldown must re-trigger dizzy");
  co.dispose();
});

test("Step 3: 2D anime dizzy FX layout clamps safely inside rail bounds and draws crisp spirals + orbiting stars", async () => {
  const { C } = loadCompanion();
  assert.equal(typeof C.vrmDizzyFx?.computeDizzyLayout, "function");
  assert.equal(typeof C.vrmDizzyFx?.drawDizzyFx, "function");

  // Even for extreme head coordinates near viewport edges, spirals & halo stay strictly clamped inside 340x240.
  for (const probe of [
    { headX: 170, headY: 96, headRadius: 46 },
    { headX: 18, headY: 16, headRadius: 60 },
    { headX: 330, headY: 225, headRadius: 55 }
  ]) {
    const layout = C.vrmDizzyFx.computeDizzyLayout({ width: 340, height: 240, ...probe });
    for (const sp of [layout.rightSpiral, layout.leftSpiral]) {
      assert.ok(sp.x - sp.radius >= 8 && sp.x + sp.radius <= 332,
        `spiral X [${sp.x - sp.radius}, ${sp.x + sp.radius}] escaped horizontal bounds`);
      assert.ok(sp.y - sp.radius >= 8 && sp.y + sp.radius <= 232,
        `spiral Y [${sp.y - sp.radius}, ${sp.y + sp.radius}] escaped vertical bounds`);
    }
    assert.ok(layout.halo.cx - layout.halo.rx >= 8 && layout.halo.cx + layout.halo.rx <= 332,
      "star halo escaped horizontal bounds");
    assert.ok(layout.halo.cy - layout.halo.ry >= 6 && layout.halo.cy + layout.halo.ry <= 232,
      "star halo escaped vertical bounds");
  }

  // Verify drawDizzyFx issues stroke + fill calls for arcs, 2 spirals, and 3 stars, and clears when alpha=0.
  let strokes = 0, fills = 0, clears = 0;
  const fakeCtx = {
    save() {}, restore() {}, translate() {}, rotate() {}, beginPath() {}, closePath() {},
    moveTo() {}, lineTo() {}, arc() {}, ellipse() {},
    stroke() { strokes++; },
    fill() { fills++; },
    clearRect() { clears++; }
  };
  C.vrmDizzyFx.drawDizzyFx(fakeCtx, { width: 340, height: 240, headX: 170, headY: 96, headRadius: 46, alpha: 1, time: 0.8 });
  assert.ok(clears >= 1, "drawDizzyFx must clear before drawing");
  assert.ok(strokes >= 6, `expected >= 6 stroke calls (2 halo arcs + 2 dual-pass spirals), got ${strokes}`);
  assert.ok(fills >= 9, `expected >= 9 fill calls (3 orbiting stars x glow/body/core), got ${fills}`);

  const prevStrokes = strokes;
  C.vrmDizzyFx.drawDizzyFx(fakeCtx, { width: 340, height: 240, alpha: 0, time: 1.0 });
  assert.equal(strokes, prevStrokes, "alpha=0 must clear without drawing FX strokes");

  // Verify VRM rig wires .ws-companion-vrm-fx canvas, onSessionSwitch, getCoLeadState, and dizzy beats on VRM 1.0 & 0.x.
  for (const metaVer of ["1", "0"]) {
    const fakeThree = makeFakeVrmRuntime(metaVer);
    const doc = {
      documentElement: { dataset: { rid: "init-s1" }, getAttribute: () => "init-s1" },
      createElement: tag => makeFakeElement(tag, doc),
      addEventListener() {}
    };
    const { C: C2 } = loadCompanion({
      document: doc,
      window: { innerWidth: 1680, innerHeight: 960, devicePixelRatio: 2, addEventListener() {} },
      fetch: async () => ({ ok: true, status: 200, arrayBuffer: async () => new Uint8Array([1, 2, 3, 4]).buffer })
    });
    C2.vrmRuntimeLoader = async () => fakeThree.runtime;

    const container = makeFakeElement("div", doc);
    const rig = C2.makeVrmRig({ id: `vrm-s3-${metaVer}`, rev: "r1", renderer: "vrm", modelFile: "a.vrm", manifest: {}, expressionMap: {}, animations: {} });
    await rig.mount(container, { width: 340, height: 240, dpr: 2 });

    const fxCanvas = container.all().find(el => el.className === "ws-companion-vrm-fx");
    assert.ok(fxCanvas, `VRM ${metaVer}: must mount .ws-companion-vrm-fx 2D overlay canvas`);
    assert.equal(fxCanvas.hidden, true, "FX canvas starts hidden while idle");

    assert.equal(C2.getCoLeadState().activeSessionId, "init-s1");
    const sw = C2.onSessionSwitch("next-s2");
    assert.equal(sw.swapped, true);
    assert.equal(C2.getCoLeadState().activeSessionId, "next-s2");

    // Forced dizzy beat shows FX canvas; returning to typing hides it cleanly.
    assert.equal(C2.setCoLeadBeat("dizzy"), "dizzy");
    const view = rig.view("stand", null, "idle", []);
    view.render(0.05, { x: 0, y: 0 }, null, {});
    assert.equal(fxCanvas.hidden, false, `VRM ${metaVer}: dizzy beat must reveal .ws-companion-vrm-fx canvas`);
    assert.equal(C2.getCoLeadState().dizzyActive, true);

    assert.equal(C2.setCoLeadBeat("typing"), "typing");
    for (let i = 0; i < 4; i++) view.render(0.05, { x: 0, y: 0 }, null, {});
    assert.equal(fxCanvas.hidden, true, `VRM ${metaVer}: returning to typing must hide .ws-companion-vrm-fx canvas`);
    rig.dispose();
  }
});

test("Phase 1 overlay: modular contracts, pixel-continuous asymmetric frustum, zero-resize anchor updates, shrink-clear, and raycastAt", async () => {
  const { C } = loadCompanion();
  assert.equal(typeof C.vrmDizzyFx?.drawDizzyFx, "function", "vrm-fx module exported");
  assert.equal(typeof C.vrmOverlayMath?.computeAsymmetricFrustum, "function", "vrmOverlayMath exported");
  assert.equal(typeof C.vrmCoLeadProps?.createCoLeadProps, "function", "vrm-props module exported");
  assert.equal(typeof C.vrmCoLeadPose?.computeCoLeadTargets, "function", "vrm-pose module exported");
  assert.equal(typeof C.createVrmCoLead, "function", "vrm-colead module exported");
  assert.equal(typeof C.vrmCoLeadMath?.computeCoLeadTargets, "function", "vrmCoLeadMath preserved");

  // 1. Pixel-continuity theorem: a 3D view-space point projects to the exact same viewport pixel
  //    whether rendered in docked, bleed, or full-viewport roam frustums.
  const anchor = { left: 0, top: 694, width: 280, height: 240 };
  const rects = [
    anchor,
    { left: 0, top: 646, width: 328, height: 288 },
    { left: 0, top: 36, width: 1680, height: 898 }
  ];
  const vx = 0.08, vy = -0.05, vz = -1.85;
  const projectedPixels = rects.map(r => {
    const f = C.vrmOverlayMath.computeAsymmetricFrustum(anchor, r, 0.08, 25, 26);
    const ndcX = (2 * f.near * vx / (-vz) - (f.right + f.left)) / (f.right - f.left);
    const ndcY = (2 * f.near * vy / (-vz) - (f.top + f.bottom)) / (f.top - f.bottom);
    return {
      x: r.left + (ndcX * 0.5 + 0.5) * r.width,
      y: r.top + (-ndcY * 0.5 + 0.5) * r.height
    };
  });
  for (let i = 1; i < projectedPixels.length; i++) {
    assert.ok(Math.abs(projectedPixels[i].x - projectedPixels[0].x) < 1e-9, `pixel X drifted for rect ${i}`);
    assert.ok(Math.abs(projectedPixels[i].y - projectedPixels[0].y) < 1e-9, `pixel Y drifted for rect ${i}`);
  }

  // 2. Zero-resize backing store + 3-mode scissor + shrink-clear (exact prevR y-flip & clear-before-render order) + lazy FX canvas + .shell-bottom clamp + raycastAt.
  let setSizeCalls = 0;
  const glOps = [];
  const fakeThree = makeFakeVrmRuntime("1");
  const OrigRenderer = fakeThree.runtime.THREE.WebGLRenderer;
  fakeThree.runtime.THREE.WebGLRenderer = class extends OrigRenderer {
    setSize(w, h, updateStyle) { setSizeCalls++; super.setSize(w, h, updateStyle); }
    setScissorTest(flag) { this._scissorTest = !!flag; }
    setScissor(x, y, w, h) { this._lastScissor = { x, y, w, h }; glOps.push({ op: "scissor", x, y, w, h }); }
    setViewport(x, y, w, h) { this._lastViewport = { x, y, w, h }; }
    clear() { glOps.push({ op: "clear", test: !!this._scissorTest, scissor: this._lastScissor ? { ...this._lastScissor } : null }); }
    render(scene, camera) {
      glOps.push({ op: "render", test: !!this._scissorTest, scissor: this._lastScissor ? { ...this._lastScissor } : null });
      return super.render(scene, camera);
    }
  };
  fakeThree.runtime.THREE.Vector2 = class { constructor(x = 0, y = 0) { this.x = x; this.y = y; } set(x, y) { this.x = x; this.y = y; return this; } };
  fakeThree.runtime.THREE.Raycaster = class {
    setFromCamera(ndc) { this._ndc = { x: ndc.x, y: ndc.y }; }
    intersectObject() {
      return Math.abs(this._ndc.x) < 0.35 && Math.abs(this._ndc.y) < 0.35
        ? [{ distance: 1.5, object: { name: "body" } }]
        : [];
    }
  };
  const OrigCam = fakeThree.runtime.THREE.PerspectiveCamera;
  fakeThree.runtime.THREE.PerspectiveCamera = class extends OrigCam {
    constructor(fov, aspect, near, far) {
      super(fov, aspect, near, far);
      this.projectionMatrix = { makePerspective(l, r, t, b, n, f) { this.frustum = { l, r, t, b, n, f }; return this; } };
      this.projectionMatrixInverse = { copy() { return this; }, invert() { return this; } };
    }
  };

  const botBar = { getBoundingClientRect: () => ({ left: 0, top: 934, width: 1680, height: 26 }) };
  const topBar = { getBoundingClientRect: () => ({ left: 0, top: 0, width: 1680, height: 36 }) };
  const doc = {
    documentElement: { dataset: { rid: "s1" }, getAttribute: () => "s1" },
    createElement: tag => makeFakeElement(tag, doc),
    querySelector: sel => sel === ".shell-bottom" ? botBar : (sel.includes(".shell-top") ? topBar : null),
    addEventListener() {}
  };
  const { C: C2 } = loadCompanion({
    document: doc,
    window: { innerWidth: 1680, innerHeight: 960, devicePixelRatio: 2, addEventListener() {} },
    fetch: async () => ({ ok: true, status: 200, arrayBuffer: async () => new Uint8Array([1, 2, 3, 4]).buffer })
  });
  C2.vrmRuntimeLoader = async () => fakeThree.runtime;

  const container = makeFakeElement("div", doc);
  const rig = C2.makeVrmRig({ id: "vrm-overlay-1", rev: "r1", renderer: "vrm", modelFile: "a.vrm", manifest: {}, expressionMap: {}, animations: {} });
  await rig.mount(container, {
    width: 280, height: 240, dpr: 2,
    viewport: { width: 1680, height: 960, dpr: 2 },
    anchorRect: { left: 0, top: 694, width: 280, height: 240 }
  });
  const initialSetSize = setSizeCalls;
  assert.equal(rig.getRenderMetrics().viewportResizeCount, 1);

  // Overlay 2D FX canvas stays lazily unallocated (0x0) while idle/hidden.
  const fxCanvas = container.all().find(el => el.className === "ws-companion-vrm-fx");
  assert.ok(fxCanvas);
  assert.equal(fxCanvas.hidden, true);
  assert.equal(fxCanvas.width, 0, "hidden overlay 2D FX canvas must remain 0x0 until an FX plays");
  assert.equal(fxCanvas.height, 0);

  // Rail width drags (220px -> 360px -> 280px) must trigger ZERO renderer.setSize calls!
  for (const w of [220, 360, 280]) {
    rig.setAnchorRect({ left: 0, top: 694, width: w, height: 240 });
    rig.view("stand", null, "idle", []).render(0.05, { x: 0, y: 0 }, null, {});
  }
  assert.equal(setSizeCalls, initialSetSize, "rail width drags must never reallocate WebGL backing store");
  assert.equal(rig.getRenderMetrics().viewportResizeCount, 1);
  assert.ok(rig.getRenderMetrics().anchorUpdateCount >= 3);

  // docked -> bleed (clamped above .shell-bottom at 934px) -> roam -> docked (verify exact shrink-clear prevR + clear-before-render order!).
  rig.setRenderMode("bleed");
  rig.setBleedPadding({ top: 48, right: 48, bottom: 48, left: 24 });
  rig.view("stand", null, "idle", []).render(0.10, { x: 0, y: 0 }, null, {});
  const bleedMetrics = rig.getRenderMetrics();
  assert.equal(bleedMetrics.mode, "bleed");
  assert.equal(rig.getRenderMode(), "bleed");
  assert.equal(container.dataset.renderMode, "bleed");
  assert.ok(bleedMetrics.scissorRect.top + bleedMetrics.scissorRect.height <= 960 - 26,
    `bleed scissor bottom must clamp above .shell-bottom: ${JSON.stringify(bleedMetrics.scissorRect)}`);
  assert.equal(bleedMetrics.scissorRect.width, 280 + 48);
  assert.equal(rig.getCoLeadState().breakoutActive, true);

  rig.setRenderMode("roam");
  rig.view("stand", null, "idle", []).render(0.15, { x: 0, y: 0 }, null, {});
  const roamRect = rig.getRenderMetrics().scissorRect;
  assert.equal(rig.getRenderMetrics().mode, "roam");
  assert.ok(roamRect.top + roamRect.height <= 934);

  const shrinkBefore = rig.getRenderMetrics().shrinkClearCount;
  glOps.length = 0;
  rig.setRenderMode("docked");
  rig.view("stand", null, "idle", []).render(0.20, { x: 0, y: 0 }, null, {});
  const dockedMetrics = rig.getRenderMetrics();
  assert.equal(dockedMetrics.mode, "docked");
  assert.equal(container.dataset.renderMode, "docked");
  assert.ok(dockedMetrics.shrinkClearCount > shrinkBefore, "shrinking from roam back to docked must clear prevR");
  assert.equal(rig.getCoLeadState().breakoutActive, false);

  // Assert exact GL ordering & y-flipped coordinates: clear(prevR) strictly BEFORE render(dockedR).
  const clearIdx = glOps.findIndex(op => op.op === "clear");
  const renderIdx = glOps.findIndex(op => op.op === "render");
  assert.ok(clearIdx >= 0 && renderIdx > clearIdx, `shrink-clear must issue clear before render: ${JSON.stringify(glOps)}`);
  const expectedPrevY = 960 - (roamRect.top + roamRect.height);
  assert.equal(glOps[clearIdx].test, true, "shrink-clear must keep scissorTest enabled");
  assert.deepEqual(glOps[clearIdx].scissor, {
    x: roamRect.left, y: expectedPrevY, w: roamRect.width, h: roamRect.height
  }, "shrink-clear must clear the exact previous scissor rect with flipped WebGL y");
  const dockedRect = dockedMetrics.scissorRect;
  const expectedDockedY = 960 - (dockedRect.top + dockedRect.height);
  assert.deepEqual(glOps[renderIdx].scissor, {
    x: dockedRect.left, y: expectedDockedY, w: dockedRect.width, h: dockedRect.height
  }, "post-clear render must use the new docked scissor rect with flipped WebGL y");

  // Also verify explicit bleed -> docked shrink-clear coordinates and order.
  rig.setRenderMode("bleed");
  rig.view("stand", null, "idle", []).render(0.25, { x: 0, y: 0 }, null, {});
  const bleedPrev = rig.getRenderMetrics().scissorRect;
  glOps.length = 0;
  rig.setRenderMode("docked");
  rig.view("stand", null, "idle", []).render(0.30, { x: 0, y: 0 }, null, {});
  const bClearIdx = glOps.findIndex(op => op.op === "clear");
  const bRenderIdx = glOps.findIndex(op => op.op === "render");
  assert.ok(bClearIdx >= 0 && bRenderIdx > bClearIdx, "bleed -> docked must clear before render");
  assert.deepEqual(glOps[bClearIdx].scissor, {
    x: bleedPrev.left, y: 960 - (bleedPrev.top + bleedPrev.height), w: bleedPrev.width, h: bleedPrev.height
  }, "bleed -> docked shrink-clear must clear the exact bleed scissor rect");

  // raycastAt hits character center, misses edge, and reports outside beyond active scissor.
  const centerRay = rig.raycastAt(140, 694 + 120);
  assert.equal(centerRay.hit, true);
  assert.equal(centerRay.area, "character");
  const edgeRay = rig.raycastAt(10, 694 + 10);
  assert.equal(edgeRay.hit, false);
  assert.equal(edgeRay.area, "messenger");
  const outsideRay = rig.raycastAt(900, 200);
  assert.equal(outsideRay.hit, false);
  assert.equal(outsideRay.area, "outside");

  rig.dispose();
});

test("Phase 1 stage: 4px splitter trim, 60fps breakout tier -> 30/6fps docked drop, roam hit-box tracking, and roam raycastAt click path", async () => {
  let raf = null;
  let pendingTimer = null;
  let nowPerf = 1000;
  let slotW = 280;
  const winListeners = {};
  const doc = {
    hidden: false,
    body: null,
    documentElement: {
      dataset: { rid: "s1" },
      style: { setProperty() {}, removeProperty() {} },
      hasAttribute: () => false,
      getAttribute: () => "s1"
    },
    createElement: tag => makeFakeElement(tag, doc),
    createElementNS: (_ns, tag) => makeFakeElement(tag, doc),
    getElementById: id => id === "rail" ? railEl : (id === "rail-companion-slot" ? slotEl : null),
    querySelector: sel => sel === "#rail" || sel === ".rail" ? railEl : (sel === "#rail-companion-slot" ? slotEl : null),
    querySelectorAll: () => [],
    addEventListener() {},
    removeEventListener() {}
  };
  doc.body = makeFakeElement("body", doc);

  const railEl = makeFakeElement("aside", doc);
  railEl.id = "rail";
  railEl.className = "rail";
  railEl.getBoundingClientRect = () => ({ left: 0, top: 36, right: slotW, bottom: 934, width: slotW, height: 898 });
  const slotEl = makeFakeElement("div", doc);
  slotEl.id = "rail-companion-slot";
  slotEl.className = "rail-companion-slot";
  slotEl.hidden = true;
  slotEl.getBoundingClientRect = () => slotEl.hidden
    ? { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 }
    : { left: 0, top: 694, right: slotW, bottom: 934, width: slotW, height: 240 };
  railEl.append(slotEl);
  railEl.querySelector = sel => sel === "#rail-companion-slot" ? slotEl : null;
  railEl.querySelectorAll = sel => sel.includes("#rail-companion-slot") ? [slotEl] : [];

  let renderMode = "docked";
  let renders = 0;
  let setViewportCalls = 0;
  let anchorUpdates = 0;
  let knockCalls = 0;
  let messengerToggles = 0;
  const raycastCalls = [];
  let raycastNextHit = true;
  let roamBounds = { x: 420, y: -394, width: 156, height: 228, viewportX: 420, viewportY: 300 };

  const fakeRig = {
    renderer: "vrm",
    mount() {},
    view(pose, gesture, expression) {
      return {
        pose, gesture, expression, props: [], handAnchor: [140, 140], articulatedDrag: false, seat: 1, bounds: [0, 0, 1, 1],
        render() { renders++; return null; }
      };
    },
    prop() { return { data: null, drawable: false, draw() {} }; },
    setGaze() {},
    setViewport() { setViewportCalls++; return true; },
    setAnchorRect() { anchorUpdates++; return true; },
    setRenderMode(m) { renderMode = m === "bleed" || m === "roam" ? m : "docked"; return renderMode; },
    getRenderMode() { return renderMode; },
    getRenderMetrics() { return { mode: renderMode, overlayMode: true }; },
    getCoLeadState() { return { breakoutActive: renderMode !== "docked", knockActive: false, swapActive: false, dizzyActive: false, phoneMode: "desk" }; },
    getCharacterBounds() {
      return renderMode === "roam"
        ? { ...roamBounds }
        : { x: 70, y: 20, width: 140, height: 200, viewportX: 70, viewportY: 714 };
    },
    raycastAt(x, y) {
      raycastCalls.push({ x, y });
      return { hit: raycastNextHit, area: raycastNextHit ? "character" : "messenger", clientX: x, clientY: y };
    },
    triggerKnock() { knockCalls++; return { kind: "knock_startle", count: knockCalls, wokeFromSleepy: false }; },
    isContextLost() { return false; },
    debugState() { return { loaded: true, contextLost: false }; },
    pause() {},
    resume() {},
    dispose() {}
  };

  const win = {
    innerWidth: 1680,
    innerHeight: 960,
    devicePixelRatio: 2,
    location: { search: "" },
    matchMedia: q => ({ matches: q.includes("1600"), addEventListener() {}, removeEventListener() {} }),
    localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
    addEventListener(name, fn) { (winListeners[name] = winListeners[name] || []).push(fn); },
    removeEventListener() {},
    requestAnimationFrame(fn) { raf = fn; return 1; },
    cancelAnimationFrame() { raf = null; },
    ResizeObserver: class { observe() {} disconnect() {} },
    MutationObserver: class { observe() {} disconnect() {} },
    WsShell: { companionContext: () => ({ routeKind: "home", activity: "idle", sessionId: "s1", landmarks: {}, events: [] }) },
    WsCompanionMessenger: { mount() {}, toggle() { messengerToggles++; return true; }, isOpen: () => false, dispose() {} },
    performance: { now: () => nowPerf }
  };

  const { C } = loadCompanion({
    document: doc,
    window: win,
    getComputedStyle: () => ({ visibility: "visible", display: "flex" }),
    ResizeObserver: win.ResizeObserver,
    MutationObserver: win.MutationObserver,
    OffscreenCanvas: class {
      constructor(w, h) { this.width = w; this.height = h; }
      getContext() { return { canvas: this, clearRect() {}, drawImage() {}, save() {}, restore() {}, getImageData: () => ({ data: [] }) }; }
    },
    wsT: k => k,
    performance: { now: () => nowPerf },
    requestAnimationFrame: win.requestAnimationFrame,
    cancelAnimationFrame: win.cancelAnimationFrame,
    setTimeout: (fn, ms) => { pendingTimer = { fn, ms }; return 1; },
    clearTimeout: () => { pendingTimer = null; },
    fetch: async () => ({ ok: true, status: 200, json: async () => ({}) })
  });
  C.packLoader = onReady => ({
    load() {
      Promise.resolve().then(() => onReady({
        id: "vrm-stage-p1", rev: "r1", renderer: "vrm", modelFile: "a.vrm",
        manifest: { scenarios: [], timing: {} }, expressionMap: {}, animations: {}
      }, null));
    },
    dispose() {}
  });
  C.makeVrmRig = () => fakeRig;

  const stage = C.mount({ pack: "vrm-stage-p1", enabled: true }, { rail: railEl });
  for (let i = 0; i < 6; i++) await new Promise(r => setImmediate(r));

  const step = dtMs => {
    const fn = raf;
    raf = null;
    nowPerf += dtMs;
    if (fn) fn(nowPerf);
  };
  step(16);

  const rootNode = doc.body.children[0];
  const hitEl = rootNode.all().find(el => el.className === "ws-companion-hit");
  const charBtn = rootNode.all().find(el => el.className === "ws-companion-character");
  assert.ok(hitEl && charBtn);

  // (a) 4px splitter trim on docked hit box + zero setViewport across rail width drags.
  assert.equal(hitEl.style.width, "276px", "docked .ws-companion-hit must trim 4px for .rail-splitter (280 - 4)");
  const vpCallsBefore = setViewportCalls;
  slotW = 360;
  for (const fn of winListeners.resize || []) fn();
  step(40);
  assert.equal(hitEl.style.width, "356px", "resized rail slot 360px must keep 4px splitter trim (356px)");
  slotW = 280;
  for (const fn of winListeners.resize || []) fn();
  step(40);
  assert.equal(hitEl.style.width, "276px");
  assert.equal(setViewportCalls, vpCallsBefore, "rail width drags must call setAnchorRect, never setViewport");
  assert.ok(anchorUpdates >= 2);

  // (b) Breakout 60fps tier in bleed/roam -> drops back to 30fps active docked and 6fps idle docked.
  stage.setVrmRenderMode("bleed");
  step(16);
  assert.equal(stage.debugState().vrmBreakoutActive, true);
  assert.equal(stage.debugState().vrmTargetFps, 60);
  const rendersAtBleedStart = renders;
  // Advance 5 frames at 16.7ms (60fps): every frame should render (not throttled to 30fps).
  for (let i = 0; i < 5; i++) step(16.7);
  assert.equal(renders - rendersAtBleedStart, 5, "bleed mode must render at 60fps (every 16.7ms frame)");

  // Switch to roam: still 60fps, and (c) hit-box tracks roam viewport character bounds.
  stage.setVrmRenderMode("roam");
  step(16.7);
  assert.equal(stage.debugState().vrmTargetFps, 60);
  assert.equal(hitEl.style.width, "156px", "roam hit width must track character bounds width");
  assert.equal(hitEl.style.height, "228px", "roam hit height must track character bounds height");
  assert.ok(hitEl.style.transform.includes("translate3d(420px,300px,0)"),
    `roam hit transform must track viewportX/viewportY, got ${hitEl.style.transform}`);

  // (d) Roam click path invokes rig.raycastAt: hit triggers knock (not messenger), miss does nothing.
  raycastNextHit = true;
  charBtn.dispatchEvent({ type: "click", button: 0, clientX: 480, clientY: 390 });
  assert.equal(raycastCalls.length, 1);
  assert.deepEqual(raycastCalls[0], { x: 480, y: 390 });
  assert.equal(knockCalls, 1, "roam raycast hit must trigger knock");
  assert.equal(messengerToggles, 0, "roam raycast hit must never open messenger");

  raycastNextHit = false;
  charBtn.dispatchEvent({ type: "click", button: 0, clientX: 425, clientY: 305 });
  assert.equal(raycastCalls.length, 2);
  assert.equal(knockCalls, 1, "roam raycast miss must not trigger knock");
  assert.equal(messengerToggles, 0, "roam raycast miss must not open messenger");

  // Return to docked -> drops immediately to 30fps active, then 6fps idle after 5s.
  stage.setVrmRenderMode("docked");
  step(16.7);
  assert.equal(stage.debugState().vrmBreakoutActive, false);
  assert.equal(stage.debugState().vrmTargetFps, 30, "returning to docked must drop targetFps to 30fps active");
  assert.equal(hitEl.style.width, "276px", "returning to docked must restore 4px-trimmed slot hit width");
  const dockedActiveRenders = renders;
  step(16); // throttled by 30fps (33.3ms - 5.5ms slack = 27.8ms)
  assert.equal(renders, dockedActiveRenders, "docked 30fps tier must throttle a 16ms frame");
  step(34);
  assert.equal(renders, dockedActiveRenders + 1, "docked 30fps tier must render after 34ms");

  // After > 5000ms idle with no activity, drops to 6fps idle tier (uses idleTimer instead of 60Hz rAF).
  step(5500);
  assert.equal(stage.debugState().vrmIdleMode, true);
  assert.equal(stage.debugState().vrmTargetFps, 6, "idle docked VRM must drop to 6fps tier");
  assert.equal(raf, null, "6fps idle tier must sleep on setTimeout instead of spinning 60Hz rAF");
  assert.ok(pendingTimer && pendingTimer.ms >= 120 && pendingTimer.ms <= 200,
    `6fps idle timer delay should be ~167ms, got ${pendingTimer?.ms}`);
  const idleRenders = renders;
  const fireTimer = pendingTimer.fn;
  pendingTimer = null;
  fireTimer();
  step(40); // throttled if fired too early (< 162.7ms)
  assert.equal(renders, idleRenders, "6fps idle tier must throttle a 40ms frame");
  const fireTimer2 = pendingTimer.fn;
  pendingTimer = null;
  fireTimer2();
  step(170);
  assert.equal(renders, idleRenders + 1, "6fps idle tier must render after ~170ms");

  stage.dispose();
});

test("seamless overlay: projected 3D laptop Box3 hit-zone, fallback region, dynamic --companion-slot resize lockstep, and document gaze pass-through", async () => {
  let raf = null;
  let nowPerf = 1000;
  let slotW = 280;
  const docListeners = {};
  const winListeners = {};
  const storage = new Map();
  const doc = {
    hidden: false,
    body: null,
    documentElement: {
      dataset: { rid: "s1" },
      style: { setProperty() {}, removeProperty() {} },
      hasAttribute: () => false,
      getAttribute: () => "s1",
      addEventListener() {}
    },
    createElement: tag => makeFakeElement(tag, doc),
    createElementNS: (_ns, tag) => makeFakeElement(tag, doc),
    getElementById: id => id === "rail" ? railEl : (id === "rail-companion-slot" ? slotEl : null),
    querySelector: sel => sel === "#rail" || sel === ".rail" ? railEl : (sel === "#rail-companion-slot" ? slotEl : null),
    querySelectorAll: () => [],
    addEventListener(name, fn) { (docListeners[name] = docListeners[name] || []).push(fn); },
    removeEventListener() {}
  };
  doc.body = makeFakeElement("body", doc);

  const railEl = makeFakeElement("aside", doc);
  railEl.id = "rail";
  railEl.className = "rail";
  railEl.getBoundingClientRect = () => ({ left: 0, top: 36, right: slotW, bottom: 934, width: slotW, height: 898 });
  const slotEl = makeFakeElement("div", doc);
  slotEl.id = "rail-companion-slot";
  slotEl.className = "rail-companion-slot";
  slotEl.hidden = true;
  slotEl.getBoundingClientRect = () => {
    if (slotEl.hidden) return { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 };
    const h = parseInt(railEl.style.getPropertyValue("--companion-slot"), 10) || 240;
    return { left: 0, top: 934 - h, right: slotW, bottom: 934, width: slotW, height: h };
  };
  railEl.append(slotEl);
  railEl.querySelector = sel => sel === "#rail-companion-slot" ? slotEl : null;
  railEl.querySelectorAll = sel => sel.includes("#rail-companion-slot") ? [slotEl] : [];

  let laptopBounds = { x: 86, y: 150, width: 108, height: 68, viewportX: 86, viewportY: 844 };
  let laptopCb = null;
  let lastGaze = { x: 0, y: 0 };
  const messengerClicks = [];
  const fakeRig = {
    renderer: "vrm",
    mount() {},
    view(pose, gesture, expression) {
      return {
        pose, gesture, expression, props: [], handAnchor: [140, 140], articulatedDrag: false, seat: 1, bounds: [0, 0, 1, 1],
        render() { return null; }
      };
    },
    prop() { return { data: null, drawable: false, draw() {} }; },
    setGaze(g) { lastGaze = { x: g.x, y: g.y }; },
    setViewport() { return true; },
    setAnchorRect() { return true; },
    setRenderMode(m) { return m || "docked"; },
    getRenderMode() { return "docked"; },
    getRenderMetrics() { return { mode: "docked", overlayMode: true }; },
    getCoLeadState() { return { breakoutActive: false, knockActive: false, swapActive: false, dizzyActive: false, phoneMode: "desk" }; },
    getCharacterBounds() { return { x: 64, y: 18, width: 152, height: 210, viewportX: 64, viewportY: 712 }; },
    getLaptopBounds() { return laptopBounds ? { ...laptopBounds } : null; },
    isContextLost() { return false; },
    debugState() { return { loaded: true, contextLost: false, laptopBounds }; },
    pause() {},
    resume() {},
    dispose() {}
  };

  const win = {
    innerWidth: 1680,
    innerHeight: 960,
    devicePixelRatio: 2,
    location: { search: "" },
    matchMedia: q => ({ matches: q.includes("1600"), addEventListener() {}, removeEventListener() {} }),
    localStorage: {
      getItem: k => storage.has(k) ? storage.get(k) : null,
      setItem: (k, v) => storage.set(k, String(v)),
      removeItem: k => storage.delete(k)
    },
    addEventListener(name, fn) { (winListeners[name] = winListeners[name] || []).push(fn); },
    removeEventListener() {},
    requestAnimationFrame(fn) { raf = fn; return 1; },
    cancelAnimationFrame() { raf = null; },
    ResizeObserver: class { observe() {} disconnect() {} },
    MutationObserver: class { observe() {} disconnect() {} },
    WsShell: { companionContext: () => ({ routeKind: "home", activity: "idle", sessionId: "s1", landmarks: {}, events: [] }) },
    WsCompanionMessenger: { mount() {}, toggle(el) { messengerClicks.push(el); return true; }, isOpen: () => false, dispose() {} },
    performance: { now: () => nowPerf }
  };

  const { C } = loadCompanion({
    document: doc,
    window: win,
    getComputedStyle: () => ({ visibility: "visible", display: "flex" }),
    ResizeObserver: win.ResizeObserver,
    MutationObserver: win.MutationObserver,
    OffscreenCanvas: class {
      constructor(w, h) { this.width = w; this.height = h; }
      getContext() { return { canvas: this, clearRect() {}, drawImage() {}, save() {}, restore() {}, getImageData: () => ({ data: [] }) }; }
    },
    wsT: k => k,
    performance: { now: () => nowPerf },
    requestAnimationFrame: win.requestAnimationFrame,
    cancelAnimationFrame: win.cancelAnimationFrame,
    setTimeout: () => 1,
    clearTimeout: () => {},
    fetch: async () => ({ ok: true, status: 200, json: async () => ({}) })
  });
  C.packLoader = onReady => ({
    load() {
      Promise.resolve().then(() => onReady({
        id: "vrm-seamless-test", rev: "r1", renderer: "vrm", modelFile: "a.vrm",
        manifest: { scenarios: [], timing: {} }, expressionMap: {}, animations: {}
      }, null));
    },
    dispose() {}
  });
  C.makeVrmRig = (_pack, opts) => { laptopCb = opts?.onLaptopBoundsChange || null; return fakeRig; };

  const stage = C.mount({ pack: "vrm-seamless-test", enabled: true }, { rail: railEl });
  for (let i = 0; i < 6; i++) await new Promise(r => setImmediate(r));
  const step = dtMs => {
    const fn = raf;
    raf = null;
    nowPerf += dtMs;
    if (fn) fn(nowPerf);
  };
  step(16);

  const rootNode = doc.body.children[0];
  const laptopBtn = rootNode.all().find(el => el.className === "ws-companion-laptop-hit");
  const resizeGrip = rootNode.all().find(el => el.className === "ws-companion-resize");
  assert.ok(laptopBtn, "must render .ws-companion-laptop-hit button");
  assert.ok(resizeGrip, "must render .ws-companion-resize grip");
  assert.equal(laptopBtn.hidden, false);
  assert.equal(laptopBtn.dataset.source, "projected");
  assert.equal(laptopBtn.style.left, "82px", "projected laptop x padded by 4px");
  assert.equal(laptopBtn.style.width, "116px", "projected laptop width padded by 8px");

  // Clicking the projected laptop button opens Companion Messenger.
  laptopBtn.dispatchEvent({ type: "click", stopPropagation() {} });
  assert.equal(messengerClicks.length, 1);
  assert.equal(messengerClicks[0], laptopBtn);

  // Hiding the 3D laptop prop transitions cleanly to lower-center desk fallback.
  laptopBounds = null;
  laptopCb?.(null);
  assert.equal(laptopBtn.hidden, false);
  assert.equal(laptopBtn.dataset.source, "fallback");
  assert.ok(parseInt(laptopBtn.style.width, 10) >= 48);
  assert.ok(parseInt(laptopBtn.style.height, 10) >= 32);

  // Vertical resize handle drag updates --companion-slot (and therefore .rlist bottom padding) in lockstep.
  const initialSlotPx = parseInt(railEl.style.getPropertyValue("--companion-slot"), 10);
  assert.ok(initialSlotPx >= 152 && initialSlotPx <= 256, `initial --companion-slot=${initialSlotPx}`);
  resizeGrip.dispatchEvent({ type: "pointerdown", button: 0, pointerId: 1, clientX: 12, clientY: 900, preventDefault() {}, stopPropagation() {} });
  resizeGrip.dispatchEvent({ type: "pointermove", pointerId: 1, clientX: 12, clientY: 840, preventDefault() {}, stopPropagation() {} });
  resizeGrip.dispatchEvent({ type: "pointerup", pointerId: 1, clientX: 12, clientY: 840, preventDefault() {}, stopPropagation() {} });
  step(40);
  const resizedSlotPx = parseInt(railEl.style.getPropertyValue("--companion-slot"), 10);
  assert.equal(resizedSlotPx, initialSlotPx + 60, "vertical drag up by 60px must increase --companion-slot by 60px in lockstep");
  assert.equal(stage.debugState().vrmHeight, resizedSlotPx);

  // Reset restores default VRM height and clears persisted vrmHeight.
  stage.resetSize();
  step(40);
  assert.equal(parseInt(railEl.style.getPropertyValue("--companion-slot"), 10), initialSlotPx);

  // Document pointermove updates gaze & slot hover even when target is an .rlist row behind the pass-through overlay.
  const fakeRow = makeFakeElement("div", doc);
  for (const fn of docListeners.pointermove || []) {
    fn({ target: fakeRow, clientX: 250, clientY: 780, pointerId: 2 });
  }
  step(40);
  assert.equal(rootNode.dataset.hover, "1", "pointer over slot rect sets data-hover=1 even when event.target is a passed-through .rlist row");
  assert.ok(lastGaze.x > 0, `expected positive rightward gaze from document pointermove, got ${lastGaze.x}`);

  // Narrow viewport (< 1600px) hides companion and clears --companion-slot + data-companion-dock.
  win.innerWidth = 1280;
  for (const fn of winListeners.resize || []) fn();
  assert.equal(railEl.style.getPropertyValue("--companion-slot"), "", "hidden companion must clear --companion-slot so .rlist has zero wasted space");
  assert.equal(railEl.hasAttribute("data-companion-dock"), false);
  assert.equal(laptopBtn.hidden, true);

  stage.dispose();
});

test("vrm-props projectLaptopBounds projects 3D Box3 corners and returns null when hidden", async () => {
  const { pathToFileURL } = require("node:url");
  const rt = await import(pathToFileURL(path.join(__dirname, "assets", "companion-vrm-runtime.mjs")).href);
  const { THREE } = rt;
  const { C } = loadCompanion();
  assert.equal(typeof C.vrmCoLeadProps?.createCoLeadProps, "function");

  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(28, 280 / 240, 0.1, 20);
  camera.position.set(0, 1.32, 1.55);
  camera.lookAt(0, 1.24, 0);
  camera.updateMatrixWorld(true);
  camera.updateProjectionMatrix();

  const props = C.vrmCoLeadProps.createCoLeadProps({ THREE, scene, camera });
  props.buildPropIfNeeded();
  const lap = props.getLaptopObject();
  assert.ok(lap, "buildPropIfNeeded must construct 3D laptop group");
  lap.position.set(0, 1.12, 0.42);
  lap.scale.setScalar(0.32);
  lap.visible = true;
  scene.updateMatrixWorld(true);

  const b = props.projectLaptopBounds(camera, { left: 0, top: 700, width: 280, height: 240 }, { left: 0, top: 700, width: 280, height: 240 }, true);
  assert.ok(b && b.width > 30 && b.height > 15, `expected projected laptop box, got ${JSON.stringify(b)}`);
  assert.equal(b.viewportX, b.x);
  assert.equal(b.viewportY, 700 + b.y);
  lap.visible = false;
  assert.equal(props.projectLaptopBounds(camera, { left: 0, top: 700, width: 280, height: 240 }, { left: 0, top: 700, width: 280, height: 240 }, true), null, "hidden laptop must return null bounds");
  props.dispose();
});
