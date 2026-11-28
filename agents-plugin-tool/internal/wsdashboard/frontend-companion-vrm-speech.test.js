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
  for (const name of [
    "builtins", "packs", "focus", "deform", "compositor", "scenarios", "menu",
    "reflex", "quip", "vrm-fx", "vrm-studio", "vrm-props", "vrm-pose",
    "vrm-colead", "vrm-speech", "vrm", "stage"
  ]) {
    vm.runInContext(
      fs.readFileSync(path.join(__dirname, "assets", `dashboard-companion-${name}.js`), "utf8"),
      context,
      { filename: `dashboard-companion-${name}.js` }
    );
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
    hidden: false,
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
      return { canvas: this, clearRect() {}, drawImage() {}, setTransform() {}, save() {}, restore() {} };
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
    matches() { return false; },
    closest() { return null; },
    focus() {}
  };
  return el;
}

function makeFakeVrm(metaVersion = "1", extraPresets = {}) {
  const values = new Map();
  const basePresets = metaVersion === "0"
    ? { neutral: {}, blink: {}, Blink_L: {}, Blink_R: {}, fun: {}, angry: {}, sorrow: {}, joy: {}, a: {}, i: {}, u: {}, e: {}, o: {} }
    : { neutral: {}, blink: {}, blinkLeft: {}, blinkRight: {}, relaxed: {}, angry: {}, sad: {}, happy: {}, surprised: {}, aa: {}, ih: {}, ou: {}, ee: {}, oh: {} };
  const presetMap = { ...basePresets, ...extraPresets };
  return {
    meta: { metaVersion },
    values,
    expressionManager: {
      presetExpressionMap: presetMap,
      customExpressionMap: {},
      expressions: Object.keys(presetMap).map(expressionName => ({ expressionName })),
      setValue: (k, v) => values.set(k, v),
      getValue: k => values.get(k) ?? 0
    }
  };
}

function makeFakeVrmRuntime(metaVersion = "1") {
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
    constructor(fov, aspect, near, far) { super(); this.fov = fov; this.aspect = aspect; this.near = near; this.far = far; }
    lookAt() {}
    updateProjectionMatrix() {}
  }
  class AnimationMixer {
    update() {}
    stopAllAction() {}
    clipAction() { return { reset() {}, fadeIn() {}, fadeOut() {}, play() {}, stop() {}, crossFadeTo() {} }; }
  }
  class WebGLRenderer {
    constructor() { this.shadowMap = { enabled: true }; this.info = { render: { calls: 4, triangles: 2000 } }; }
    setClearColor() {}
    setPixelRatio() {}
    setSize() {}
    render() {}
    dispose() {}
    forceContextLoss() {}
  }
  let lastVrm = null;
  class GLTFLoader {
    register() {}
    async parseAsync() {
      const bones = new Map();
      for (const [name, y] of [["spine", 1.0], ["chest", 1.15], ["upperChest", 1.24], ["neck", 1.38], ["head", 1.48]]) {
        const node = new Object3D();
        node.name = name;
        node.position.set(0, y, 0);
        bones.set(name, node);
      }
      const fake = makeFakeVrm(metaVersion);
      const vrm = {
        ...fake,
        scene: new Object3D(),
        humanoid: { getNormalizedBoneNode: name => bones.get(name) || null },
        lookAt: { target: null },
        update() {}
      };
      lastVrm = vrm;
      return { scene: vrm.scene, userData: { vrm } };
    }
  }
  return {
    getLastVrm: () => lastVrm,
    runtime: {
      THREE: { Vector3, Quaternion, Object3D, Scene, PerspectiveCamera, AnimationMixer, WebGLRenderer, SRGBColorSpace: "srgb", NoToneMapping: 0 },
      GLTFLoader,
      VRMLoaderPlugin: class {},
      VRMAnimationLoaderPlugin: class {},
      VRMUtils: { removeUnnecessaryVertices() {}, combineSkeletons() {}, rotateVRM0() {}, deepDispose() {} },
      createVRMAnimationClip: () => ({ duration: 1 })
    }
  };
}

function collectPeaks(driver, text, opts, stepSec = 0.025) {
  driver.start(text, opts);
  const peaks = { aa: 0, ih: 0, ou: 0, ee: 0, oh: 0 };
  const samples = [];
  let guard = 0;
  while (driver.isActive() && guard++ < 600) {
    const s = driver.sample(stepSec, guard * stepSec * 1000);
    samples.push(s);
    for (const k of ["aa", "ih", "ou", "ee", "oh"]) {
      if (s[k] > peaks[k]) peaks[k] = s[k];
    }
  }
  return { peaks, samples, steps: guard };
}

test("Hangul medial vowel decomposition maps 아/이/우/에/오 and multi-syllable words accurately", () => {
  const { C } = loadCompanion();
  const S = C.vrmSpeech;
  assert.ok(S, "window.WsCompanion.vrmSpeech must be exported");

  const expectedDominant = [
    ["아", "aa"],
    ["이", "ih"],
    ["우", "ou"],
    ["에", "ee"],
    ["오", "oh"]
  ];
  for (const [char, viseme] of expectedDominant) {
    const drv = S.createProceduralMouthDriver();
    const { peaks } = collectPeaks(drv, char, { durationMs: 600 });
    assert.ok(peaks[viseme] > 0.45, `${char} should drive strong ${viseme} peak, got ${JSON.stringify(peaks)}`);
    for (const other of S.VISEMES) {
      if (other !== viseme) {
        assert.ok(peaks[viseme] > peaks[other] * 4, `${char} should dominate ${viseme} over ${other}: ${JSON.stringify(peaks)}`);
      }
    }
  }

  // Multi-syllable Korean greeting & completion words hit multiple vowel shapes smoothly.
  for (const word of ["안녕하세요", "완료"]) {
    const drv = S.createProceduralMouthDriver();
    const { peaks, samples } = collectPeaks(drv, word);
    const activeVowels = S.VISEMES.filter(v => peaks[v] > 0.15);
    assert.ok(activeVowels.length >= 2, `${word} should articulate >=2 distinct vowels, got ${JSON.stringify(peaks)}`);
    // Verify smooth attack/release (no instantaneous 0 -> 1 jumps).
    let maxStepDelta = 0;
    for (let i = 1; i < samples.length; i++) {
      for (const v of S.VISEMES) {
        maxStepDelta = Math.max(maxStepDelta, Math.abs(samples[i][v] - samples[i - 1][v]));
      }
    }
    assert.ok(maxStepDelta < 0.45, `${word} transitions should be smooth, max delta=${maxStepDelta}`);
    // Final sample returns cleanly to 0 and speaking=false.
    const last = samples[samples.length - 1];
    assert.equal(last.speaking, false);
    for (const v of S.VISEMES) assert.equal(last[v], 0);
  }
});

test("Latin vowels, digraphs, consonant dips, CJK/digits, punctuation pauses and duration clamping", () => {
  const { C } = loadCompanion();
  const S = C.vrmSpeech;

  // Duration clamping: 0.9s floor, 8.5s ceiling, explicit durationMs honored.
  assert.equal(S.estimateDurationMs(""), 0);
  assert.equal(S.estimateDurationMs("아"), S.MIN_DURATION_MS);
  assert.equal(S.estimateDurationMs("가".repeat(140)), S.MAX_DURATION_MS);
  const midMs = S.estimateDurationMs("안녕하세요! 빌드 완료했습니다.");
  assert.ok(midMs > S.MIN_DURATION_MS && midMs < S.MAX_DURATION_MS, `expected mid duration, got ${midMs}`);

  // Explicit durationMs honored.
  const drvExplicit = S.createProceduralMouthDriver();
  drvExplicit.start("Hello world!", { durationMs: 1200 });
  assert.equal(Math.round(drvExplicit.debugState().durationSec * 1000), 1200);

  // Latin words activate distinct vowels and punctuation produces pause boundaries.
  const analyzed = S.analyzeText("Hello, world! Ready? Done… 123 漢字");
  assert.ok(analyzed.tokens.some(t => t.isPause && t.boundary === "clause"), "comma emits clause pause");
  assert.ok(analyzed.tokens.some(t => t.isPause && t.boundary === "sentence"), "sentence punctuation emits sentence pause");

  const drvLat = S.createProceduralMouthDriver();
  const { peaks, samples } = collectPeaks(drvLat, "Hello, moon! Ready?");
  assert.ok(peaks.ee > 0.2 && peaks.oh > 0.2 && peaks.ou > 0.2, `Latin vowels/digraphs should activate ee/oh/ou: ${JSON.stringify(peaks)}`);
  assert.ok(samples.some(s => s.boundary === "clause" || s.boundary === "sentence"), "sample stream surfaces phrase boundaries");

  // Digits & CJK fallback produce lively non-zero mouth movement.
  const drvCjk = S.createProceduralMouthDriver();
  const { peaks: cjkPeaks } = collectPeaks(drvCjk, "2026 완료 確認");
  assert.ok(Math.max(...Object.values(cjkPeaks)) > 0.35, `CJK/digits should animate visemes: ${JSON.stringify(cjkPeaks)}`);
});

test("Emotion-blending attenuation and speech-blink suppression during eye-closing expressions", () => {
  const { C } = loadCompanion();
  const vrm = makeFakeVrm("1");
  const ctrl = new C.VrmExpressionController();
  ctrl.attach(vrm);

  // Neutral idle: full viseme peak (attenuation === 1).
  ctrl.setState("idle");
  ctrl.setSpeechSample({ aa: 0.8, ih: 0, ou: 0, ee: 0, oh: 0, speaking: true, boundary: "" });
  for (let i = 0; i < 12; i++) ctrl.update(0.033);
  const idleAa = vrm.values.get("aa");
  assert.equal(ctrl.lastVisemeAttenuation, 1);
  assert.ok(idleAa > 0.72, `idle aa should reach ~0.8, got ${idleAa}`);

  // Strong mouth-shaping facial expressions attenuate viseme peaks safely.
  for (const expr of ["happy", "oo", "cry", "merong", "startled"]) {
    ctrl.setState(expr);
    ctrl.setSpeechSample({ aa: 0.8, ih: 0, ou: 0, ee: 0, oh: 0, speaking: true, boundary: "" });
    for (let i = 0; i < 16; i++) ctrl.update(0.033);
    const exprAa = vrm.values.get("aa");
    assert.ok(ctrl.lastVisemeAttenuation < 0.7, `${expr} should attenuate visemes (<0.7), got ${ctrl.lastVisemeAttenuation}`);
    assert.ok(exprAa < idleAa, `${expr} aa (${exprAa}) must be lower than idle aa (${idleAa})`);
    assert.ok(exprAa <= 0.90, `${expr} aa must never clip`);
  }

  // Speech-time blink modulation fires on neutral speech boundaries and periodic cadence.
  ctrl.setState("idle");
  ctrl.blinkTimer = 99; // Suppress idle auto-blink to isolate speech blink.
  ctrl.blinkProgress = -1;
  ctrl.speechBlink.reset();
  let maxSpeechBlink = 0;
  for (let i = 0; i < 90; i++) {
    ctrl.setSpeechSample({ aa: 0.5, ih: 0, ou: 0, ee: 0, oh: 0, speaking: true, boundary: i === 35 ? "sentence" : "" });
    ctrl.update(0.033);
    maxSpeechBlink = Math.max(maxSpeechBlink, ctrl.lastSpeechBlink);
  }
  assert.ok(maxSpeechBlink > 0.45, `neutral speech should trigger micro-blink, peak=${maxSpeechBlink}`);

  // Strictly suppressed during eye-closing expressions (wink, happy, cry, idle_doze).
  for (const closedExpr of ["wink", "happy", "cry", "idle_doze"]) {
    ctrl.setState(closedExpr);
    ctrl.blinkTimer = 99;
    ctrl.speechBlink.reset();
    let leaked = 0;
    for (let i = 0; i < 90; i++) {
      ctrl.setSpeechSample({ aa: 0.5, ih: 0, ou: 0, ee: 0, oh: 0, speaking: true, boundary: i === 35 ? "sentence" : "" });
      ctrl.update(0.033);
      leaked = Math.max(leaked, ctrl.lastSpeechBlink);
    }
    assert.equal(leaked, 0, `speech-blink must be strictly 0 during ${closedExpr}, got ${leaked}`);
  }
});

test("VRM 0.x (a/i/u/e/o) vs VRM 1.0 (aa/ih/ou/ee/oh) expression application and pluggable speechMouthDriver", async () => {
  for (const metaVersion of ["0", "1"]) {
    const fake = makeFakeVrmRuntime(metaVersion);
    const { C } = loadCompanion();
    C.vrmRuntimeLoader = async () => fake.runtime;
    C.fetchPackAsset = async () => ({ rev: "r1", buffer: new ArrayBuffer(16) });

    const doc = { createElement: tag => makeFakeElement(tag, null), documentElement: { dataset: {} } };
    const container = makeFakeElement("div", doc);
    const rig = C.makeVrmRig({ id: `pack-v${metaVersion}`, rev: "r1", renderer: "vrm", modelFile: "model.vrm" });
    await rig.mount(container, { width: 240, height: 280, dpr: 1 });

    // Default procedural driver.
    assert.equal(rig.getSpeechMouthDriver().name, "procedural");
    assert.equal(rig.isSpeaking(), false);

    // Start Korean speech and verify resolved keys match VRM version.
    assert.equal(rig.startSpeech("아아아", { durationMs: 900 }), true);
    assert.equal(rig.isSpeaking(), true);
    const view = rig.view("stand", null, "idle", []);
    let t = 0.033;
    for (let i = 0; i < 12; i++, t += 0.033) view.render(t, { x: 0, y: 0 }, null, null);

    const vrm = fake.getLastVrm();
    const wantKey = metaVersion === "0" ? "a" : "aa";
    const dbg = rig.debugState().speech;
    assert.equal(dbg.resolvedVisemes.aa, wantKey);
    assert.ok(vrm.values.get(wantKey) > 0.35, `VRM ${metaVersion} should drive ${wantKey}, got ${vrm.values.get(wantKey)}`);

    // Stop speech and verify smooth decay cleanly back to 0.
    rig.stopSpeech();
    assert.equal(rig.isSpeaking(), false);
    for (let i = 0; i < 20; i++, t += 0.033) view.render(t, { x: 0, y: 0 }, null, null);
    assert.equal(vrm.values.get(wantKey), 0, `VRM ${metaVersion} ${wantKey} must decay cleanly to 0`);

    // Pluggable custom driver override.
    let startedWith = null;
    let stopped = false;
    let active = false;
    const customDriver = {
      name: "tts-test",
      start(text, opts) { startedWith = { text, opts }; active = true; },
      stop() { stopped = true; active = false; },
      isActive() { return active; },
      sample() { return active ? { aa: 0, ih: 0, ou: 0.75, ee: 0, oh: 0, speaking: true } : { aa: 0, ih: 0, ou: 0, ee: 0, oh: 0, speaking: false }; }
    };
    assert.equal(rig.setSpeechMouthDriver(customDriver), customDriver);
    assert.equal(rig.getSpeechMouthDriver(), customDriver);
    rig.startSpeech("TTS stream", { durationMs: 2000 });
    assert.deepEqual(startedWith, { text: "TTS stream", opts: { durationMs: 2000 } });
    for (let i = 0; i < 10; i++, t += 0.033) view.render(t, { x: 0, y: 0 }, null, null);
    const ouKey = metaVersion === "0" ? "u" : "ou";
    assert.ok(vrm.values.get(ouKey) > 0.55, `custom driver should drive ${ouKey}, got ${vrm.values.get(ouKey)}`);
    assert.equal(rig.debugState().speech.driverName, "tts-test");

    // Passing null stops custom driver and restores procedural default.
    const restored = rig.setSpeechMouthDriver(null);
    assert.equal(stopped, true);
    assert.equal(restored.name, "procedural");
    assert.equal(rig.getSpeechMouthDriver().name, "procedural");

    // Faulty custom driver throwing in sample() must never crash render.
    rig.setSpeechMouthDriver({
      isActive: () => true,
      sample() { throw new Error("boom"); }
    });
    assert.doesNotThrow(() => view.render(t + 0.033, { x: 0, y: 0 }, null, null));

    rig.dispose();
  }
});

test("Stage quip auto-wires startSpeech/stopSpeech, 30fps->6fps idle cadence, narrow hide and document.hidden", async () => {
  let fakeNow = 1000;
  let rafQueue = [];
  let rafId = 0;
  let innerWidth = 1720;
  let docHidden = false;
  const docListeners = {};
  const winListeners = {};
  const slotEl = {
    hidden: false,
    getBoundingClientRect: () => ({ left: 12, top: 640, right: 272, bottom: 900, width: 260, height: 260 })
  };
  const railEl = {
    style: { setProperty(k, v) { this[k] = v; }, getPropertyValue(k) { return this[k] || ""; }, removeProperty(k) { delete this[k]; } },
    dataset: {},
    removeAttribute(k) { delete this[k]; },
    addEventListener() {},
    querySelector: sel => (sel === "#rail-companion-slot" ? slotEl : null),
    querySelectorAll: () => [],
    getBoundingClientRect: () => ({ left: 0, top: 0, right: 280, bottom: 920, width: 280, height: 920 })
  };
  const doc = {
    get hidden() { return docHidden; },
    documentElement: {
      dataset: { rid: "sess-1" },
      classList: { contains: () => false },
      getAttribute: () => null,
      addEventListener() {}
    },
    body: { append() {} },
    getElementById: id => (id === "rail" ? railEl : null),
    createElement: tag => makeFakeElement(tag, doc),
    createElementNS: (_, tag) => makeFakeElement(tag, doc),
    addEventListener(type, fn) { (docListeners[type] ||= []).push(fn); }
  };

  const fakeVrm = makeFakeVrmRuntime("1");
  const { C } = loadCompanion({
    document: doc,
    wsT: k => k,
    window: {
      get innerWidth() { return innerWidth; },
      innerHeight: 960,
      devicePixelRatio: 1,
      addEventListener(type, fn) { (winListeners[type] ||= []).push(fn); },
      matchMedia: () => ({ get matches() { return innerWidth >= 1600; }, addEventListener() {} }),
      wsT: k => k,
      WsShell: { companionContext: () => ({ routeKind: "session", activity: "idle", landmarks: {}, events: [] }) }
    },
    performance: { now: () => fakeNow },
    requestAnimationFrame: fn => { rafQueue.push(fn); return ++rafId; },
    cancelAnimationFrame: () => { rafQueue = []; },
    ResizeObserver: class { observe() {} disconnect() {} },
    MutationObserver: class { observe() {} disconnect() {} },
    getComputedStyle: () => ({ visibility: "visible", display: "block" }),
    CSS: { escape: s => s }
  });

  C.vrmRuntimeLoader = async () => fakeVrm.runtime;
  C.fetchPackAsset = async () => ({ rev: "r1", buffer: new ArrayBuffer(16) });
  C.quickMenu = () => ({ rebuild() {}, measure() {}, place() {}, close() {} });

  const speechLog = [];
  let mountedRig = null;
  const origMakeVrmRig = C.makeVrmRig;
  C.makeVrmRig = (pack, opts) => {
    const r = origMakeVrmRig(pack, opts);
    const origStart = r.startSpeech.bind(r);
    const origStop = r.stopSpeech.bind(r);
    r.startSpeech = (text, o) => { speechLog.push(["start", text, o?.durationMs]); return origStart(text, o); };
    r.stopSpeech = () => { speechLog.push(["stop"]); return origStop(); };
    mountedRig = r;
    return r;
  };

  C.packLoader = onLoad => ({
    load() {
      onLoad({ id: "vrm-pack", rev: "r1", renderer: "vrm", modelFile: "model.vrm", manifest: { scenarios: {} } }, null);
    },
    reload: async () => {},
    dispose() {}
  });

  const stage = C.mount({ enabled: true, pack: "vrm-pack", preset: "" }, () => {}, null);
  await new Promise(res => setImmediate(res));

  const stepFrame = (ms = 34) => {
    fakeNow += ms;
    const batch = rafQueue.splice(0);
    for (const fn of batch) fn(fakeNow);
  };

  // Settle initial frames (so gaze converges) and let idle mode kick in after 5s.
  for (let i = 0; i < 12; i++) stepFrame(34);
  fakeNow += 5500;
  stepFrame(200);
  assert.equal(stage.debugState().vrmIdleMode, true);
  assert.equal(stage.debugState().vrmTargetFps, 6);

  // 1. Puppeteer / companion_speak quip auto-starts lip-sync & wakes to 30fps.
  const ok = stage.command({ sessionId: "*", quip: "안녕하세요! 작업 완료!", ttl_ms: 4000 });
  assert.equal(ok, true);
  stepFrame(34);
  assert.ok(speechLog.some(e => e[0] === "start" && e[1] === "안녕하세요! 작업 완료!"), `expected startSpeech on quip, log=${JSON.stringify(speechLog)}`);
  assert.equal(mountedRig.isSpeaking(), true);
  assert.equal(stage.debugState().vrmTargetFps, 30);

  // 2. Document hidden immediately stops speech.
  docHidden = true;
  for (const fn of docListeners.visibilitychange || []) fn();
  assert.equal(mountedRig.isSpeaking(), false);
  assert.equal(speechLog[speechLog.length - 1][0], "stop");

  // Resume visibility and trigger another quip, then drop viewport < 1600px.
  docHidden = false;
  for (const fn of docListeners.visibilitychange || []) fn();
  stepFrame(34);
  stage.command({ sessionId: "*", quip: "테스트 중입니다", ttl_ms: 4000 });
  stepFrame(34);
  assert.equal(mountedRig.isSpeaking(), true);

  innerWidth = 1400;
  for (const fn of winListeners.resize || []) fn();
  assert.equal(mountedRig.isSpeaking(), false, "viewport < 1600px must call stopSpeech");

  // 3. Standalone startSpeech wakes to 30fps and drops immediately to 6fps when speech finishes.
  innerWidth = 1720;
  for (const fn of winListeners.resize || []) fn();
  stage.returnHome(); // Clears prior directed beat cleanly.
  for (let i = 0; i < 6; i++) stepFrame(34);
  fakeNow += 5500;
  stepFrame(200);
  assert.equal(stage.debugState().vrmTargetFps, 6);

  mountedRig.startSpeech("아", { durationMs: 250 });
  stepFrame(34);
  assert.equal(stage.debugState().vrmTargetFps, 30);
  assert.equal(stage.debugState().vrmIdleMode, false);

  // Advance past 250ms speech duration -> finishes and drops straight to 6fps idle.
  for (let i = 0; i < 10; i++) stepFrame(34);
  assert.equal(mountedRig.isSpeaking(), false);
  assert.equal(stage.debugState().vrmIdleMode, true, "must drop immediately to idle mode after speech finishes");
  assert.equal(stage.debugState().vrmTargetFps, 6, "must drop immediately to 6fps after speech finishes");

  stage.dispose();
});
