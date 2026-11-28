const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

function makeFakeElement(tag, doc) {
  const el = {
    tagName: tag.toUpperCase(),
    className: "",
    id: "",
    title: "",
    textContent: "",
    value: "",
    type: "",
    rows: 1,
    scrollHeight: 28,
    scrollTop: 0,
    clientHeight: 300,
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
        quadraticCurveTo() {},
        stroke() {},
        fill() {},
        fillText() {},
        measureText: txt => ({ width: String(txt || "").length * 7 }),
        getImageData: () => ({ data: [] })
      };
    },
    getBoundingClientRect() {
      return { left: 12, top: 720, right: 352, bottom: 960, width: 340, height: 240 };
    },
    all() {
      return this.children.flatMap(child => [child, ...(child && typeof child.all === "function" ? child.all() : [])]);
    },
    contains(other) { return other === this || this.all().includes(other); },
    querySelector(sel) {
      if (sel.startsWith("#")) return this.all().find(n => n.id === sel.slice(1)) || null;
      if (sel.startsWith(".")) return this.all().find(n => (n.className || "").split(/\s+/).includes(sel.slice(1))) || null;
      return null;
    },
    querySelectorAll() { return []; },
    focus() { this.focused = true; }
  };
  return el;
}

function loadCompanionWithMessenger(globals = {}) {
  const win = {
    innerWidth: 1680,
    innerHeight: 960,
    devicePixelRatio: 2,
    addEventListener() {},
    ...(globals.window || {})
  };
  const context = vm.createContext({
    window: win,
    URLSearchParams,
    AbortController,
    setTimeout,
    clearTimeout,
    performance: { now: () => 0 },
    ...globals,
    window: win
  });
  // Keep globalThis aliases consistent with browser window.
  context.globalThis = context;
  for (const name of ["builtins", "packs", "focus", "deform", "compositor", "scenarios", "menu", "vrm-fx", "vrm-studio", "vrm-props", "vrm-pose", "vrm-colead", "vrm", "stage", "messenger-md", "messenger"]) {
    vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", `dashboard-companion-${name}.js`), "utf8"), context);
  }
  return { C: context.window.WsCompanion, M: context.window.WsCompanionMessenger, context };
}

test("Step 4 Phase 1: cosmetic glass knock transitions (awake startle, 3+ rapid annoyed_tap, sleepy wakeup_jolt)", () => {
  const { C } = loadCompanionWithMessenger();
  const co = C.createVrmCoLead({});

  // 1. Single knock while awake -> knock_startle.
  const k1 = co.triggerKnock({ nowMs: 1000, sleepy: false });
  assert.equal(k1.kind, "knock_startle");
  assert.equal(k1.wokeFromSleepy, false);
  const st1 = co.step({ dt: 0.05, elapsed: 1.05, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
  assert.equal(st1.mode, "knock-startle");
  assert.equal(st1.actionFxKind, "knock-startle");
  assert.ok(st1.exprOverlay && st1.exprOverlay.surprised > 0.8);

  // 2. Two more rapid knocks within 4s window -> 3rd triggers playful annoyed_tap.
  const k2 = co.triggerKnock({ nowMs: 1400, sleepy: false });
  assert.equal(k2.kind, "knock_startle");
  const k3 = co.triggerKnock({ nowMs: 1800, sleepy: false });
  assert.equal(k3.kind, "annoyed_tap");
  assert.equal(k3.wokeFromSleepy, false);
  const st3 = co.step({ dt: 0.05, elapsed: 1.85, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
  assert.equal(st3.mode, "annoyed-tap");
  assert.equal(st3.actionFxKind, "annoyed-tap");
  assert.ok(st3.exprOverlay && st3.exprOverlay.angry > 0.5);

  // 3. Sleepy knock -> crisp wakeup_jolt & clears sleepy weight.
  co.setCoLeadBeat("sleepy");
  const kSleep = co.triggerKnock({ nowMs: 7000, sleepy: true });
  assert.equal(kSleep.kind, "wakeup_jolt");
  assert.equal(kSleep.wokeFromSleepy, true);
  assert.equal(co.getCoLeadState().overrideBeat, "auto", "wakeup knock must exit sleepy beat");
  const stWake = co.step({ dt: 0.05, elapsed: 7.05, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
  assert.equal(stWake.mode, "wakeup-jolt");
  assert.equal(stWake.actionFxKind, "wakeup-jolt");
  assert.ok(stWake.exprOverlay && stWake.exprOverlay.surprised > 0.9);

  // Verify drawActionFx renders non-empty canvas operations for all Step 4 cues.
  let fills = 0, strokes = 0, texts = 0;
  const fakeCtx = {
    save() {}, restore() {}, translate() {}, rotate() {}, beginPath() {}, closePath() {},
    moveTo() {}, lineTo() {}, arc() {}, quadraticCurveTo() {},
    fill() { fills++; },
    stroke() { strokes++; },
    fillText() { texts++; },
    measureText: txt => ({ width: String(txt || "").length * 7 })
  };
  for (const kind of ["knock-startle", "wakeup-jolt", "annoyed-tap", "phone-buzz", "phone-facedown-merong"]) {
    const before = fills + strokes + texts;
    C.vrmDizzyFx.drawActionFx(fakeCtx, { width: 340, height: 240, headX: 170, headY: 96, headRadius: 42, crownY: 42, kind, alpha: 1, time: 0.5 });
    assert.ok(fills + strokes + texts > before, `drawActionFx(${kind}) must draw visible vector & badge FX`);
  }
  co.dispose();
});

test("Step 4 Phase 1: 3D desk smartphone posture choreography (buzz -> pickup -> typing -> normal putdown & facedown_merong)", () => {
  const { C } = loadCompanionWithMessenger();
  const co = C.createVrmCoLead({});

  // Initial desk state.
  assert.equal(co.getCoLeadState().phonePose, "desk");
  assert.equal(co.getCoLeadState().phoneFaceDown, false);

  // 1. Incoming user message -> phone_buzz.
  co.setPhoneState({ state: "laptop_ambient", posture: "laptop", unreadAgent: 1, phoneFaceDown: false });
  let t = 0.05;
  let out = co.step({ dt: 0.05, elapsed: t, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
  assert.equal(out.phonePose, "buzz");
  assert.equal(out.actionFxKind, "phone-buzz");
  assert.ok(co.getCoLeadState().phoneScreenGlow > 0.25, "buzzing phone screen must brighten");

  // 2. Companion picks up phone -> phone_active / held posture.
  co.setPhoneState({ state: "phone_active", posture: "phone", unreadAgent: 0, phoneFaceDown: false });
  for (let i = 0; i < 16; i++) {
    t += 0.05;
    out = co.step({ dt: 0.05, elapsed: t, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
  }
  assert.equal(out.phonePose, "held");
  assert.equal(out.phoneFaceDown, false);
  // Phase 2 palm grip: elbow stays tucked at the side while the forearm folds up
  // and twists so the palm (phone screen) faces the face at upper-chest height.
  assert.ok(out.boneTargets.leftUpperArm[1] > 0.4, `phone-side upper arm should swing the elbow in (got y=${out.boneTargets.leftUpperArm[1]})`);
  assert.ok(out.boneTargets.leftLowerArm[1] < -1.6, `phone-side forearm should fold up toward the chest (got y=${out.boneTargets.leftLowerArm[1]})`);
  assert.ok(out.boneTargets.leftLowerArm[0] > 0.9, `phone-side forearm should twist the palm toward the face (got x=${out.boneTargets.leftLowerArm[0]})`);

  // 3. Normal putdown -> returns smoothly to desk face-up.
  co.setPhoneState({ state: "laptop_ambient", posture: "laptop", unreadAgent: 0, phoneFaceDown: false });
  for (let i = 0; i < 24; i++) {
    t += 0.05;
    out = co.step({ dt: 0.05, elapsed: t, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
  }
  assert.equal(out.phonePose, "desk");
  assert.equal(out.phoneFaceDown, false);

  // 4. Cheeky facedown_merong putdown -> wink/merong overlay + phoneFaceDown persisted!
  co.triggerGesture("phone_merong_facedown");
  t += 0.05;
  out = co.step({ dt: 0.05, elapsed: t, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
  assert.equal(out.mode, "phone-facedown-merong");
  assert.equal(out.actionFxKind, "phone-facedown-merong");
  assert.ok(out.exprOverlay && out.exprOverlay.wink > 0.9 && out.exprOverlay.merong > 0.8);
  for (let i = 0; i < 40; i++) {
    t += 0.05;
    out = co.step({ dt: 0.05, elapsed: t, expression: "idle", rawPose: "stand", gazeX: 0, gazeY: 0, hover: false });
  }
  assert.equal(out.phoneFaceDown, true, "facedown_merong must leave smartphone face-down on desk");
  assert.equal(out.phonePose, "facedown");
  co.dispose();
});

test("Step 4 Phase 1: direct VRM click performs cosmetic glass knock and NEVER opens messenger; #companion-chat-btn toggles messenger", async () => {
  let now = 0, raf = null, rafId = 0;
  const docListeners = {}, winListeners = {};
  const doc = {
    hidden: false,
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
  slotEl.hidden = true;
  slotEl.getBoundingClientRect = () => slotEl.hidden
    ? { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 }
    : { left: 12, top: 720, right: 352, bottom: 960, width: 340, height: 240 };
  const railEl = makeFakeElement("aside", doc);
  railEl.id = "rail";
  railEl.getBoundingClientRect = () => ({ left: 12, top: 12, right: 352, bottom: 960, width: 340, height: 948 });
  railEl.querySelector = sel => sel === "#rail-companion-slot" ? slotEl : null;
  railEl.querySelectorAll = sel => sel.includes("#rail-companion-slot") ? [slotEl] : [];

  const knockCalls = [];
  const phoneCalls = [];
  const { C, M } = loadCompanionWithMessenger({
    document: doc,
    getComputedStyle: () => ({ visibility: "visible", display: "flex" }),
    window: {
      innerWidth: 1680,
      innerHeight: 960,
      devicePixelRatio: 2,
      matchMedia: () => ({ matches: true, addEventListener() {} }),
      localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
      addEventListener: (type, fn) => { (winListeners[type] ||= []).push(fn); },
      WsShell: { companionContext: () => ({ routeKind: "home", activity: "idle", landmarks: {}, events: [], rightColActive: false, drawerOpen: false }) },
      wsCompanionModalRows: []
    },
    ResizeObserver: class { observe() {} disconnect() {} },
    MutationObserver: class { observe() {} disconnect() {} },
    performance: { now: () => now },
    wsT: k => k,
    requestAnimationFrame: fn => { raf = fn; return ++rafId; },
    cancelAnimationFrame: () => { raf = null; },
    fetch: async url => {
      if (url.startsWith("/api/companion/packs?")) {
        return { ok: true, json: async () => ({ resolvedAuto: { packId: "vrm1" }, packs: [{ id: "vrm1", rev: "rv1", renderer: "vrm", schema: "ws-companion-vrm/v1" }] }) };
      }
      if (url === "/api/companion/packs") {
        return { ok: true, json: async () => ({ packs: [{ id: "vrm1", rev: "rv1", renderer: "vrm", schema: "ws-companion-vrm/v1" }] }) };
      }
      if (url.includes("/vrm1/pack.json")) {
        return { ok: true, json: async () => ({ schema: "ws-companion-vrm/v1", id: "vrm1", model: "a.vrm" }) };
      }
      if (url === "/api/companion/phone/thread" || url === "/api/companion/phone/mark-read") {
        return { ok: true, json: async () => ({ seq: 1, state: "laptop_ambient", posture: "laptop", messages: [], unreadClient: 0, unreadAgent: 0, tier1Summary: [] }) };
      }
      return { ok: true, json: async () => ({}) };
    }
  });

  C.quickMenu = () => ({ rebuild() {}, measure() {}, place() {}, close() {} });
  C.makeVrmAdapter = () => {
    const viewObj = {
      pose: "stand", gesture: null, expression: "idle", props: [],
      handAnchor: [120, 170], articulatedDrag: false, seat: 1, bounds: [0, 0, 1, 1],
      render() {}
    };
    return {
      renderer: "vrm", width: 340, height: 240,
      mount() { return Promise.resolve(); },
      view() { return viewObj; },
      prop() { return { data: null, drawable: false, draw() {} }; },
      setGaze() {}, resize() {}, pause() {}, resume() {},
      setPreset() {}, setWardrobe() {}, wardrobe() { return null; },
      getCharacterBounds() { return { x: 90, y: 30, width: 160, height: 200 }; },
      getCoLeadState() { return { mode: "typing", sleepyWeight: 0 }; },
      triggerKnock(opts) { knockCalls.push(opts); return { kind: "knock_startle", count: knockCalls.length, wokeFromSleepy: false }; },
      setPhoneState(snap) { phoneCalls.push(snap); },
      dispose() {}
    };
  };

  const stage = C.mount({ pack: "vrm1", preset: "" }, () => {}, () => {});
  for (let i = 0; i < 6; i++) await new Promise(r => setImmediate(r));
  if (raf) { now += 40; raf(now); }

  const companionRoot = doc.body.children.find(el => el.className === "ws-companion");
  assert.ok(companionRoot, "companion stage root mounted in document.body");
  const popup = doc.body.children.find(el => el.id === "companion-messenger");
  assert.ok(popup, "messenger popup mounted directly under document.body");
  const characterBtn = companionRoot.all().find(el => el.className === "ws-companion-character");
  const chatBtn = companionRoot.all().find(el => el.id === "companion-chat-btn");
  const unreadBadge = companionRoot.all().find(el => el.className === "ws-companion-unread");
  const knockLayer = companionRoot.all().find(el => el.className === "ws-companion-knock-fx");
  assert.ok(characterBtn && chatBtn && unreadBadge && knockLayer);

  // Direct click on VRM character MUST knock the glass and MUST NEVER open messenger.
  assert.equal(M.isOpen(), false);
  characterBtn.dispatchEvent({ type: "click", clientX: 180, clientY: 820 });
  assert.equal(knockCalls.length, 1, "direct VRM click must trigger cosmetic knock");
  assert.equal(M.isOpen(), false, "direct VRM click must NEVER open messenger popup");
  assert.ok(knockLayer.all().some(el => el.className.includes("ws-knock-ring")), "knock must spawn glass ripple rings");
  assert.ok(knockLayer.all().some(el => el.className === "ws-knock-word"), "knock must spawn soundless cue label");

  // Unread phone state updates #companion-chat-btn badge.
  stage.phoneState({ seq: 2, state: "laptop_ambient", posture: "laptop", unreadClient: 3, unreadAgent: 0 });
  assert.equal(unreadBadge.hidden, false);
  assert.equal(unreadBadge.textContent, "3");
  assert.equal(chatBtn.dataset.unread, "1");
  assert.equal(phoneCalls.length, 1);

  // Clicking #companion-chat-btn opens messenger popup and updates aria-expanded.
  chatBtn.dispatchEvent({ type: "click", stopPropagation() {} });
  assert.equal(M.isOpen(), true, "clicking #companion-chat-btn must open messenger popup");
  assert.equal(chatBtn.getAttribute("aria-expanded"), "true");

  // Clicking #companion-chat-btn again closes it cleanly.
  chatBtn.dispatchEvent({ type: "click", stopPropagation() {} });
  assert.equal(M.isOpen(), false);
  assert.equal(chatBtn.getAttribute("aria-expanded"), "false");

  stage.dispose();
});

test("Step 4 Phase 1: messenger popup anchoring, Variant D rail widths, viewport clamping, pin/Escape, and send flow", async () => {
  const { M } = loadCompanionWithMessenger();

  // 1. Standard left-rail dock at 1680x960 (340px wide rail).
  const r1 = M.computePopupRect(
    { left: 12, top: 720, right: 352, bottom: 960, width: 340, height: 240 },
    { width: 1680, height: 960 },
    { width: 360, height: 540 }
  );
  assert.equal(r1.left, 364, "popup should anchor 12px right of rail slot");
  assert.equal(r1.width, 360);
  assert.equal(r1.height, 540);
  assert.equal(r1.top, 408, "popup bottom aligns cleanly 12px above viewport bottom");
  assert.equal(r1.caretSide, "left");

  // 2. Variant D resized narrow rail (220px) and wide rail (360px) never overlap rail or clip viewport.
  for (const railW of [220, 280, 360]) {
    const r = M.computePopupRect(
      { left: 0, top: 720, right: railW, bottom: 960, width: railW, height: 240 },
      { width: 1680, height: 960 },
      { width: 360, height: 540 }
    );
    assert.ok(r.left >= railW + 12, `railW=${railW}: popup must sit right of rail without overlapping`);
    assert.ok(r.left + r.width <= 1680 - 12, `railW=${railW}: popup must stay inside viewport right edge`);
    assert.ok(r.top >= 12 && r.top + r.height <= 960 - 12, `railW=${railW}: popup must stay inside viewport height`);
  }

  // 3. Right-side anchor flips caret to "right" and stays inside viewport.
  const rRight = M.computePopupRect(
    { left: 1320, top: 700, right: 1660, bottom: 940, width: 340, height: 240 },
    { width: 1680, height: 960 },
    { width: 360, height: 540 }
  );
  assert.equal(rRight.caretSide, "right");
  assert.equal(rRight.left, 1320 - 12 - 360);

  // 4. Tiny viewport clamps popup dimensions without negative coordinates.
  const rTiny = M.computePopupRect(
    { left: 0, top: 300, right: 200, bottom: 480, width: 200, height: 180 },
    { width: 400, height: 420 },
    { width: 360, height: 540 }
  );
  assert.ok(rTiny.left >= 12 && rTiny.top >= 12);
  assert.ok(rTiny.width <= 376 && rTiny.height <= 396);

  // 5. Controller mount/open/send/pin/Esc/Tier-1 flow.
  const docListeners = {};
  const doc = {
    body: null,
    createElement: tag => makeFakeElement(tag, doc),
    createElementNS: (_ns, tag) => makeFakeElement(tag, doc),
    addEventListener(type, fn, opts) {
      (docListeners[type] ||= []).push(fn);
      if (opts && opts.signal) {
        opts.signal.addEventListener("abort", () => {
          docListeners[type] = (docListeners[type] || []).filter(f => f !== fn);
        });
      }
    }
  };
  doc.body = makeFakeElement("body", doc);

  const sentBodies = [];
  let resetCalls = 0;
  let settingsOpens = 0;
  M.mount({
    document: doc,
    getAnchorRect: () => ({ left: 12, top: 720, right: 352, bottom: 960, width: 340, height: 240 }),
    openSettings: () => { settingsOpens++; },
    fetch: async (url, init) => {
      if (url === "/api/companion/phone/thread") {
        return {
          ok: true,
          json: async () => ({
            seq: 1,
            state: "laptop_ambient",
            posture: "laptop",
            messages: [
              { id: "m1", sender: "companion", text: "Ready on laptop!", tone: "laugh", createdAt: Date.now() }
            ],
            unreadClient: 0,
            unreadAgent: 0,
            tier1Summary: ["Checked build status on develop"],
            rotationMeta: { turns: 3, maxTurns: 20, summaryCap: 10 }
          })
        };
      }
      if (url === "/api/companion/phone/send") {
        const body = JSON.parse(init.body);
        sentBodies.push(body);
        return {
          ok: true,
          json: async () => ({
            message: { id: "m2", sender: "user", text: body.text, createdAt: Date.now() },
            snapshot: {
              seq: 2,
              state: "laptop_ambient",
              posture: "laptop",
              messages: [
                { id: "m1", sender: "companion", text: "Ready on laptop!", tone: "laugh", createdAt: Date.now() },
                { id: "m2", sender: "user", text: body.text, createdAt: Date.now() }
              ],
              unreadClient: 0,
              unreadAgent: 1,
              tier1Summary: ["Checked build status on develop"],
              rotationMeta: { turns: 3, maxTurns: 20, summaryCap: 10 }
            }
          })
        };
      }
      if (url === "/api/companion/phone/reset-summary") {
        resetCalls++;
        return {
          ok: true,
          json: async () => ({
            seq: 3, state: "laptop_ambient", posture: "laptop",
            messages: [], unreadClient: 0, unreadAgent: 0, tier1Summary: [],
            rotationMeta: { turns: 0, maxTurns: 20, summaryCap: 10 }
          })
        };
      }
      return { ok: true, json: async () => ({}) };
    }
  });

  M.open();
  await new Promise(r => setImmediate(r));
  assert.equal(M.isOpen(), true);
  const popup = doc.body.children.find(el => el.id === "companion-messenger");
  assert.ok(popup && !popup.classList.contains("is-closed"));

  // Send message with Enter.
  const input = popup.all().find(el => el.className === "ws-cm-input");
  input.value = "Check tests please";
  input.dispatchEvent({ type: "input" });
  input.dispatchEvent({ type: "keydown", key: "Enter", shiftKey: false, isComposing: false, preventDefault() {} });
  await new Promise(r => setImmediate(r));
  assert.deepEqual(sentBodies, [{ text: "Check tests please" }]);
  assert.equal(input.value, "");

  // Toggle Tier-1 summary drawer and click reset.
  const tier1Btn = popup.all().find(el => el.dataset?.action === "toggle-tier1");
  const tier1Strip = popup.all().find(el => el.className === "ws-cm-tier1-strip");
  const tier1Reset = popup.all().find(el => el.className === "ws-cm-tier1-reset");
  assert.equal(tier1Strip.hidden, true);
  tier1Btn.dispatchEvent({ type: "click" });
  assert.equal(tier1Strip.hidden, false);
  tier1Reset.dispatchEvent({ type: "click" });
  await new Promise(r => setImmediate(r));
  assert.equal(resetCalls, 1);

  // Pin prevents Escape dismissal; unpinning allows Escape dismissal.
  const pinBtn = popup.all().find(el => el.dataset?.action === "toggle-pin");
  pinBtn.dispatchEvent({ type: "click" });
  assert.equal(M.isPinned(), true);
  for (const fn of docListeners.keydown || []) fn({ key: "Escape", preventDefault() {} });
  assert.equal(M.isOpen(), true, "Escape must not close pinned messenger");

  // Settings jump triggers callback without closing while pinned.
  const settingsBtn = popup.all().find(el => el.dataset?.action === "open-settings");
  settingsBtn.dispatchEvent({ type: "click" });
  assert.equal(settingsOpens, 1);
  assert.equal(M.isOpen(), true);

  pinBtn.dispatchEvent({ type: "click" });
  assert.equal(M.isPinned(), false);
  for (const fn of docListeners.keydown || []) fn({ key: "Escape", preventDefault() {} });
  assert.equal(M.isOpen(), false, "Escape must close unpinned messenger");

  // Unavailable state renders dedicated status dot + subtitle and recovers on clean snapshot.
  M.open();
  await new Promise(r => setImmediate(r));
  M.applySnapshot({
    seq: 4, state: "laptop_ambient", posture: "laptop", unavailable: true,
    messages: [], unreadClient: 0, unreadAgent: 1, tier1Summary: []
  });
  const dot = popup.all().find(el => el.className === "ws-cm-status-dot");
  const subtitle = popup.all().find(el => el.className === "ws-cm-subtitle");
  assert.equal(dot.dataset.state, "unavailable", "unavailable snapshot must set dot data-state=unavailable");
  assert.ok(subtitle.textContent.toLowerCase().includes("unavailable"), `expected unavailable subtitle, got ${subtitle.textContent}`);
  M.applySnapshot({
    seq: 5, state: "laptop_ambient", posture: "laptop", unavailable: false,
    messages: [], unreadClient: 0, unreadAgent: 1, tier1Summary: []
  });
  assert.equal(dot.dataset.state, "buzz", "clearing unavailable with unread agent message must show buzz");
  M.applySnapshot({
    seq: 6, state: "laptop_ambient", posture: "laptop", unavailable: false,
    messages: [], unreadClient: 0, unreadAgent: 0, tier1Summary: []
  });
  assert.equal(dot.dataset.state, "laptop", "clearing unavailable with no unread messages must restore laptop dot state");

  // Phase 2: pendingApproval renders pause status echo + approval status dot,
  // and clears cleanly when omitted from a subsequent snapshot (Go omitempty).
  M.applySnapshot({
    seq: 7, state: "laptop_work", posture: "laptop", unavailable: false,
    pendingApproval: {
      requestId: "req-42",
      tool: "config.tune",
      summary: "config.tune (key=workflow.lang, scope=global)",
      createdAt: Date.now()
    },
    messages: [{ id: "m1", sender: "companion", text: "On it!", createdAt: Date.now() }],
    unreadClient: 0, unreadAgent: 0, tier1Summary: []
  });
  assert.equal(dot.dataset.state, "approval", "pendingApproval must set dot data-state=approval");
  assert.ok(subtitle.textContent.toLowerCase().includes("approval"), `expected approval subtitle, got ${subtitle.textContent}`);
  const pauseEcho = popup.all().find(el => el.className === "ws-cm-pause-echo");
  assert.ok(pauseEcho, "pendingApproval must render .ws-cm-pause-echo status card in thread");
  assert.equal(pauseEcho.getAttribute("role"), "status");
  assert.equal(pauseEcho.getAttribute("data-request-id"), "req-42");
  const pauseTool = pauseEcho.all().find(el => el.className === "ws-cm-pause-tool");
  const pauseSummary = pauseEcho.all().find(el => el.className === "ws-cm-pause-summary");
  assert.equal(pauseTool?.textContent, "config.tune");
  assert.equal(pauseSummary?.textContent, "config.tune (key=workflow.lang, scope=global)");

  // Subsequent snapshot omitting pendingApproval (Go omitempty) must remove the echo and restore laptop_work dot state.
  M.applySnapshot({
    seq: 8, state: "laptop_work", posture: "laptop", unavailable: false,
    messages: [{ id: "m1", sender: "companion", text: "On it!", createdAt: Date.now() }],
    unreadClient: 0, unreadAgent: 0, tier1Summary: []
  });
  assert.equal(popup.all().find(el => el.className === "ws-cm-pause-echo"), undefined, "omitted pendingApproval must remove .ws-cm-pause-echo");
  assert.equal(dot.dataset.state, "work", "clearing pendingApproval during laptop_work must restore work dot state");

  M.dispose();
});

// ---------------------------------------------------------------------------
// Composer (WsInput) + bubble Markdown rendering
// ---------------------------------------------------------------------------

function textOf(node) {
  if (!node) return "";
  if (node.nodeType === 3) return node.textContent;
  const own = node.children && node.children.length ? "" : (node.textContent || "");
  return own + (node.children || []).map(textOf).join("");
}

function makeMessengerHarness({ globals = {}, window: winExtra = {}, fetchImpl, realWsInput = false } = {}) {
  const counts = {};
  const docListeners = {};
  const doc = {
    body: null,
    createElement: tag => {
      counts[tag] = (counts[tag] || 0) + 1;
      const el = makeFakeElement(tag, doc);
      // Bubbles must never reach innerHTML.
      Object.defineProperty(el, "innerHTML", { set() { throw new Error("innerHTML is forbidden in messenger rendering"); }, get() { return ""; } });
      return el;
    },
    createElementNS: (_ns, tag) => makeFakeElement(tag, doc),
    createTextNode: s => ({ nodeType: 3, textContent: String(s) }),
    addEventListener(type, fn) { (docListeners[type] ||= []).push(fn); }
  };
  doc.body = makeFakeElement("body", doc);
  const { M, context } = loadCompanionWithMessenger({
    document: doc,
    Event: class { constructor(type, init) { this.type = type; Object.assign(this, init); } },
    ...globals,
    window: winExtra
  });
  if (realWsInput) {
    vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", "ws-input.js"), "utf8"), context, { filename: "ws-input.js" });
  }
  const fetchLog = [];
  const baseFetch = fetchImpl || (async () => ({ ok: true, json: async () => ({}) }));
  M.mount({
    document: doc,
    getAnchorRect: () => ({ left: 12, top: 720, right: 352, bottom: 960, width: 340, height: 240 }),
    fetch: async (url, init) => { fetchLog.push({ url, init }); return baseFetch(url, init); }
  });
  const popup = doc.body.children.find(el => el.id === "companion-messenger");
  return { M, context, doc, popup, counts, fetchLog, win: context.window };
}

function snapshotWith(messages, extra = {}) {
  return { seq: 1, state: "laptop_ambient", posture: "laptop", messages, unreadClient: 0, unreadAgent: 0, tier1Summary: [], ...extra };
}

test("Messenger bubbles render Markdown + badges XSS-safely (DOM only, never innerHTML)", () => {
  const { M, popup } = makeMessengerHarness({ window: {} });
  const hostile = [
    "<img src=x onerror=alert(1)> and <script>alert(2)</script>",
    "**bold** *it* ~~gone~~ `inline <b>code</b>`",
    "[evil](javascript:alert(3)) [good](https://example.com/a?b=1) https://example.org/x.",
    "ping @alice and mail@example.com then <mailbox id=\"id:abcdef123456\" dir=\"/tmp/proj\" label=\"Lead\"/>",
    "# Title\n\n- one\n- [x] two\n\n> quoted\n\n```js\n<b>raw</b>\n```"
  ];
  M.applySnapshot(snapshotWith(hostile.map((text, i) => ({ id: "h" + i, sender: i % 2 ? "companion" : "user", text, createdAt: 1 }))));
  const all = popup.all();
  const tags = all.map(el => el.tagName);
  assert.ok(!tags.includes("IMG"), "<img> text must stay inert");
  assert.ok(!tags.includes("SCRIPT"), "<script> text must stay inert");
  assert.ok(!tags.includes("B"), "<b> inside code/fence must stay literal");
  const bubbles = all.filter(el => el.className === "ws-cm-bubble ws-cm-md");
  assert.equal(bubbles.length, 5);
  assert.ok(textOf(bubbles[0]).includes("<img src=x onerror=alert(1)>"), "raw <img> text preserved literally");
  assert.ok(textOf(bubbles[0]).includes("<script>alert(2)</script>"), "raw <script> text preserved literally");

  const b1 = bubbles[1];
  assert.equal(b1.all().find(el => el.tagName === "STRONG")?.children.map(textOf).join(""), "bold");
  assert.ok(b1.all().some(el => el.tagName === "EM"));
  assert.ok(b1.all().some(el => el.tagName === "DEL"));
  assert.equal(b1.all().find(el => el.className === "ws-cm-md-code")?.textContent, "inline <b>code</b>");

  const links = bubbles[2].all().filter(el => el.tagName === "A");
  assert.deepEqual(links.map(a => a.getAttribute("href")), ["https://example.com/a?b=1", "https://example.org/x"]);
  assert.ok(links.every(a => a.getAttribute("rel") === "noopener noreferrer" && a.getAttribute("target") === "_blank"));
  assert.ok(textOf(bubbles[2]).includes("evil"), "javascript: link keeps only its label text");
  assert.ok(!bubbles[2].all().some(el => String(el.getAttribute?.("href") || "").startsWith("javascript")));

  const badges = bubbles[3].all().filter(el => (el.className || "").startsWith("ws-cm-badge"));
  const mention = badges.find(el => el.className.includes("mention"));
  const mailbox = badges.find(el => el.className.includes("mailbox"));
  assert.equal(mention?.textContent, "@alice", "@mention renders a badge");
  assert.equal(badges.filter(el => el.className.includes("mention")).length, 1, "email address is not a mention");
  assert.equal(mailbox?.textContent, "Lead", "<mailbox/> tag renders a labelled badge");
  assert.ok(mailbox.title.includes("id:abcdef123456"));

  const b4 = bubbles[4];
  assert.ok(b4.all().some(el => el.className?.startsWith("ws-cm-md-h")));
  assert.equal(b4.all().filter(el => el.tagName === "LI").length, 2);
  assert.ok(b4.all().some(el => el.tagName === "BLOCKQUOTE"));
  assert.equal(b4.all().find(el => el.tagName === "PRE")?.children[0].textContent, "<b>raw</b>");
  M.dispose();
});

test("Messenger bubble badges use WsCapsules mailbox label/tooltip when available", () => {
  const { M, popup } = makeMessengerHarness({
    window: {
      WsCapsules: {
        parseMailboxTag: (s, at) => /^<mailbox /.test(s.slice(at)) ? { end: s.indexOf("/>", at) + 2, tag: { id: "id:zz", label: "L" } } : null,
        formatMailboxLabel: tag => "fmt:" + tag.label,
        formatMailboxTooltip: tag => "tip:" + tag.id
      }
    }
  });
  M.applySnapshot(snapshotWith([{ id: "a", sender: "companion", text: "see <mailbox id=\"id:zz\" label=\"L\"/> ok", createdAt: 1 }]));
  const badge = popup.all().find(el => (el.className || "").includes("ws-cm-badge mailbox"));
  assert.equal(badge.textContent, "fmt:L");
  assert.equal(badge.title, "tip:id:zz");
  M.dispose();
});

test("Messenger thread patches by key: state-only snapshots re-use bubble nodes and never re-parse Markdown", () => {
  const { M, popup, counts } = makeMessengerHarness();
  const msgs = Array.from({ length: 50 }, (_, i) => ({ id: "m" + i, sender: i % 3 ? "companion" : "user", text: "msg **" + i + "**", createdAt: 1000 + i }));
  M.applySnapshot(snapshotWith(msgs));
  const thread = popup.all().find(el => el.className === "ws-cm-thread");
  const rowsBefore = [...thread.children];
  assert.equal(rowsBefore.length, 50);
  const strongBefore = counts.strong || 0;
  const timeBefore = counts.time || 0;
  assert.ok(strongBefore >= 50, "each message parsed its Markdown once (plus the header name)");

  // Typing/state-only snapshot: same messages, different state.
  M.applySnapshot(snapshotWith(msgs, { seq: 2, state: "phone_active" }));
  assert.deepEqual(thread.children, rowsBefore, "unchanged rows must be the very same nodes");
  assert.equal((counts.strong || 0) - strongBefore, 0, "no Markdown is re-parsed");
  assert.equal((counts.time || 0) - timeBefore, 0, "no row is re-created");

  // One appended message builds exactly one new row; one edited message rebuilds only itself.
  const grown = [...msgs, { id: "m50", sender: "user", text: "new", createdAt: 2000 }];
  M.applySnapshot(snapshotWith(grown, { seq: 3 }));
  assert.equal(thread.children.length, 51);
  assert.deepEqual(thread.children.slice(0, 50), rowsBefore);
  const edited = grown.map(m => (m.id === "m10" ? { ...m, text: "edited" } : m));
  M.applySnapshot(snapshotWith(edited, { seq: 4 }));
  assert.notEqual(thread.children[10], rowsBefore[10]);
  assert.equal(thread.children.filter((row, i) => i !== 10 && row === (i < 50 ? rowsBefore[i] : row)).length, 50);
  M.dispose();
});

test("Messenger send applies server {message, thread}, falls back to snapshot, Enter sends and Shift+Enter does not (plain textarea fallback)", async () => {
  const thread = snapshotWith([{ id: "s1", sender: "user", text: "hello **there**", createdAt: 5 }], { seq: 9 });
  let reply = { message: { id: "s1" }, thread };
  const { M, popup, fetchLog } = makeMessengerHarness({
    fetchImpl: async url => (url === "/api/companion/phone/send" ? { ok: true, json: async () => reply } : { ok: true, json: async () => ({}) })
  });
  const input = popup.all().find(el => el.className === "ws-cm-input");
  const sendBtn = popup.all().find(el => el.className === "ws-cm-send-btn");
  const sends = () => fetchLog.filter(f => f.url === "/api/companion/phone/send");
  assert.equal(sendBtn.disabled, true);

  input.value = "hello **there**";
  input.dispatchEvent({ type: "input" });
  assert.equal(sendBtn.disabled, false);
  let prevented = false;
  input.dispatchEvent({ type: "keydown", key: "Enter", shiftKey: true, isComposing: false, preventDefault() { prevented = true; } });
  await new Promise(r => setImmediate(r));
  assert.equal(sends().length, 0, "Shift+Enter must not send");
  assert.equal(prevented, false);
  input.dispatchEvent({ type: "keydown", key: "Enter", shiftKey: false, isComposing: true, preventDefault() {} });
  await new Promise(r => setImmediate(r));
  assert.equal(sends().length, 0, "IME composition Enter must not send");
  input.dispatchEvent({ type: "keydown", key: "Enter", shiftKey: false, isComposing: false, preventDefault() {} });
  await new Promise(r => setImmediate(r));
  assert.equal(sends().length, 1);
  assert.equal(input.value, "", "composer clears after send");
  assert.equal(input.focused, true, "composer re-focused after send");
  assert.equal(M.getState().snapshot.seq, 9, "data.thread must be applied");
  assert.ok(popup.all().some(el => el.tagName === "STRONG" && textOf(el) === "there"), "thread rendered with Markdown");

  // Legacy shape: {message, snapshot}.
  reply = { message: { id: "s2" }, snapshot: snapshotWith([], { seq: 12 }) };
  input.value = "again";
  input.dispatchEvent({ type: "input" });
  input.dispatchEvent({ type: "keydown", key: "Enter", shiftKey: false, isComposing: false, preventDefault() {} });
  await new Promise(r => setImmediate(r));
  assert.equal(M.getState().snapshot.seq, 12, "data.snapshot fallback must still apply");
  M.dispose();
});

test("Messenger composer attaches WsInput (markdown/capsules/list continuation/auto-grow) and follows its keymap + submit contract", async () => {
  const attached = [];
  let destroyed = 0;
  const WsInput = {
    attach(target, o) {
      const api = { field: target, setValues: [], setValue(v) { target.value = v; api.setValues.push(v); }, destroy() { destroyed++; } };
      attached.push({ target, o, api });
      return api;
    }
  };
  const { M, popup, fetchLog } = makeMessengerHarness({
    window: { WsInput },
    fetchImpl: async () => ({ ok: true, json: async () => ({ thread: snapshotWith([], { seq: 2 }) }) })
  });
  assert.equal(attached.length, 1);
  const { target, o, api } = attached[0];
  assert.equal(target.className, "ws-cm-input", "WsInput attaches to the composer textarea");
  assert.equal(target.tagName, "TEXTAREA");
  assert.deepEqual(
    { markdown: o.features.markdown, capsules: o.features.capsules, listContinuation: o.features.listContinuation, autoGrow: o.features.autoGrow },
    { markdown: true, capsules: true, listContinuation: true, autoGrow: true }
  );
  assert.equal(o.features.mentions || o.features.topics || o.features.slash, false, "no file/topic/slash popups without providers");
  assert.equal(target.listeners.keydown, undefined, "WsInput owns keydown; messenger adds no competing handler");

  const sendBtn = popup.all().find(el => el.className === "ws-cm-send-btn");
  assert.equal(o.keymap(), "enter-send");
  assert.equal(sendBtn.title, "Send message");

  // A multiline draft keeps Enter-send (Shift+Enter is the newline key).
  target.value = "line one\nline two";
  o.onChange("line one\nline two", api);
  assert.equal(o.keymap(), "enter-send");
  assert.equal(sendBtn.title, "Send message");
  assert.equal(sendBtn.disabled, false);

  o.onSubmit(api, {});
  await new Promise(r => setImmediate(r));
  assert.equal(fetchLog.filter(f => f.url === "/api/companion/phone/send").length, 1);
  assert.deepEqual(JSON.parse(fetchLog.find(f => f.url === "/api/companion/phone/send").init.body), { text: "line one\nline two" });
  assert.deepEqual(api.setValues, [""], "composer cleared through WsInput.setValue");
  assert.equal(o.keymap(), "enter-send");

  M.dispose();
  assert.equal(destroyed, 1, "dispose destroys the WsInput instance");
});

test("Messenger composer with the real WsInput: Enter/Shift+Enter/IME, list continuation, and WsMdEditor wired to the capsule editor", async () => {
  const mdAttach = [];
  const editorEl = makeFakeElement("div", null);
  const WsCapsules = { attach() { return { editor: editorEl }; } };
  const WsMdEditor = {
    attach(el, o) { mdAttach.push({ el, o }); return { schedule() {}, reconcile() {}, clear() {}, detach() {} }; }
  };
  const { M: M2, popup: popup2, fetchLog: sent } = makeMessengerHarness({
    realWsInput: true,
    window: { WsCapsules, WsMdEditor },
    fetchImpl: async () => ({ ok: true, json: async () => ({ thread: snapshotWith([], { seq: 4 }) }) })
  });
  const input = popup2.all().find(el => el.className === "ws-cm-input");
  assert.ok(input.wsInput, "real WsInput instance is attached to the composer textarea");
  assert.equal(mdAttach.length, 1, "WsMdEditor is attached once");
  assert.equal(mdAttach[0].el, editorEl, "WsMdEditor binds to the capsule editor");
  assert.equal(mdAttach[0].o.isComposing(), false);
  assert.equal(mdAttach[0].o.getMentions().length, 0);

  const key = (k, extra = {}) => {
    let prevented = false;
    input.dispatchEvent({ type: "keydown", key: k, code: k, shiftKey: false, ctrlKey: false, metaKey: false, altKey: false, isComposing: false, keyCode: 13, preventDefault() { prevented = true; this.defaultPrevented = true; }, ...extra });
    return prevented;
  };
  const setValue = v => { input.value = v; input.selectionStart = input.selectionEnd = v.length; input.dispatchEvent({ type: "input" }); };

  setValue("hi there");
  assert.equal(key("Enter", { shiftKey: true }), false, "Shift+Enter leaves the newline to the editor");
  await new Promise(r => setImmediate(r));
  assert.equal(sent.filter(f => f.url === "/api/companion/phone/send").length, 0, "Shift+Enter does not send");
  assert.equal(key("Enter", { isComposing: true, keyCode: 229 }), false);
  await new Promise(r => setImmediate(r));
  assert.equal(sent.filter(f => f.url === "/api/companion/phone/send").length, 0, "IME composition Enter does not send");
  assert.equal(key("Enter"), true, "plain Enter submits");
  await new Promise(r => setImmediate(r));
  assert.equal(sent.filter(f => f.url === "/api/companion/phone/send").length, 1);
  assert.equal(input.value, "");

  // listContinuation on Shift+Enter inside a markdown list.
  setValue("- item");
  assert.equal(key("Enter", { shiftKey: true }), true, "list continuation takes over Shift+Enter");
  assert.equal(input.value, "- item\n- ");
  M2.dispose();
});

test("Stage VRM slot expanded messenger toggle: outside bbox toggles once, inside knocks only, drag >6px ignored, null bounds toggles, pointermove updates data-area without layout reads", async () => {
  let now = 0, raf = null, rafId = 0;
  const docListeners = {};
  const doc = {
    hidden: false,
    documentElement: { dataset: { rid: "s1" }, classList: { contains: () => false }, hasAttribute: () => true },
    body: null,
    getElementById: id => id === "rail" ? railEl : null,
    createElement: tag => makeFakeElement(tag, doc),
    createElementNS: (_ns, tag) => makeFakeElement(tag, doc),
    addEventListener(type, fn, opts) {
      (docListeners[type] ||= []).push(fn);
      if (opts && opts.signal) {
        opts.signal.addEventListener("abort", () => {
          docListeners[type] = (docListeners[type] || []).filter(f => f !== fn);
        });
      }
    }
  };
  doc.body = makeFakeElement("body", doc);
  const slotEl = makeFakeElement("div", doc);
  slotEl.id = "rail-companion-slot";
  slotEl.hidden = true;
  slotEl.getBoundingClientRect = () => slotEl.hidden
    ? { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 }
    : { left: 12, top: 720, right: 352, bottom: 960, width: 340, height: 240 };
  const railEl = makeFakeElement("aside", doc);
  railEl.id = "rail";
  railEl.getBoundingClientRect = () => ({ left: 12, top: 12, right: 352, bottom: 960, width: 340, height: 948 });
  railEl.querySelector = sel => sel === "#rail-companion-slot" ? slotEl : null;
  railEl.querySelectorAll = sel => sel.includes("#rail-companion-slot") ? [slotEl] : [];

  let currentBounds = { x: 90, y: 30, width: 160, height: 200 };
  const knockCalls = [];
  const knockFetches = [];
  const { C, M } = loadCompanionWithMessenger({
    document: doc,
    getComputedStyle: () => ({ visibility: "visible", display: "flex" }),
    window: {
      innerWidth: 1680,
      innerHeight: 960,
      devicePixelRatio: 2,
      matchMedia: () => ({ matches: true, addEventListener() {} }),
      localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
      addEventListener() {},
      WsShell: { companionContext: () => ({ routeKind: "home", activity: "idle", landmarks: {}, events: [], rightColActive: false, drawerOpen: false }) },
      wsCompanionModalRows: []
    },
    ResizeObserver: class { observe() {} disconnect() {} },
    MutationObserver: class { observe() {} disconnect() {} },
    performance: { now: () => now },
    wsT: k => k,
    requestAnimationFrame: fn => { raf = fn; return ++rafId; },
    cancelAnimationFrame: () => { raf = null; },
    fetch: async (url, opts) => {
      if (url === "/api/companion/knock") {
        knockFetches.push({ url, opts });
        return { ok: true, json: async () => ({ ok: true, woken: 0 }) };
      }
      if (url.startsWith("/api/companion/packs?")) {
        return { ok: true, json: async () => ({ resolvedAuto: { packId: "vrm1" }, packs: [{ id: "vrm1", rev: "rv1", renderer: "vrm", schema: "ws-companion-vrm/v1" }] }) };
      }
      if (url === "/api/companion/packs") {
        return { ok: true, json: async () => ({ packs: [{ id: "vrm1", rev: "rv1", renderer: "vrm", schema: "ws-companion-vrm/v1" }] }) };
      }
      if (url.includes("/vrm1/pack.json")) {
        return { ok: true, json: async () => ({ schema: "ws-companion-vrm/v1", id: "vrm1", model: "a.vrm" }) };
      }
      return { ok: true, json: async () => ({ seq: 1, state: "laptop_ambient", posture: "laptop", messages: [], unreadClient: 0, unreadAgent: 0, tier1Summary: [] }) };
    }
  });

  C.quickMenu = () => ({ rebuild() {}, measure() {}, place() {}, close() {} });
  C.makeVrmAdapter = () => {
    const viewObj = {
      pose: "stand", gesture: null, expression: "idle", props: [],
      handAnchor: [120, 170], articulatedDrag: false, seat: 1, bounds: [0, 0, 1, 1],
      render() {}
    };
    return {
      renderer: "vrm", width: 340, height: 240,
      mount() { return Promise.resolve(); },
      view() { return viewObj; },
      prop() { return { data: null, drawable: false, draw() {} }; },
      setGaze() {}, resize() {}, pause() {}, resume() {},
      setPreset() {}, setWardrobe() {}, wardrobe() { return null; },
      getCharacterBounds() { return currentBounds; },
      getCoLeadState() { return { mode: "typing", sleepyWeight: 0 }; },
      triggerKnock(opts) { knockCalls.push(opts); return { kind: "knock_startle", count: knockCalls.length, wokeFromSleepy: false }; },
      setPhoneState() {},
      dispose() {}
    };
  };

  const stage = C.mount({ pack: "vrm1", preset: "" }, () => {}, () => {});
  for (let i = 0; i < 6; i++) await new Promise(r => setImmediate(r));
  if (raf) { now += 40; raf(now); }

  const companionRoot = doc.body.children.find(el => el.className === "ws-companion");
  const hitEl = companionRoot.all().find(el => el.className === "ws-companion-hit");
  const characterBtn = companionRoot.all().find(el => el.className === "ws-companion-character");
  const chatBtn = companionRoot.all().find(el => el.id === "companion-chat-btn");

  const dispatchDoc = event => {
    for (const fn of [...(docListeners[event.type] || [])]) fn(event);
  };
  const clickAt = (target, x, y, dx = 0, dy = 0, button = 0) => {
    const pd = { type: "pointerdown", pointerId: 1, button, target, clientX: x, clientY: y };
    target.dispatchEvent(pd);
    dispatchDoc(pd);
    if (dx || dy) {
      const pm = { type: "pointermove", pointerId: 1, target, clientX: x + dx, clientY: y + dy };
      target.dispatchEvent(pm);
      dispatchDoc(pm);
    }
    const pu = { type: "pointerup", pointerId: 1, button, target, clientX: x + dx, clientY: y + dy };
    target.dispatchEvent(pu);
    dispatchDoc(pu);
    target.dispatchEvent({ type: "click", button, target, clientX: x + dx, clientY: y + dy, stopPropagation() {} });
  };

  // Track open/close transitions via chatBtn aria-expanded updates so double-toggles are caught.
  let openCalls = 0, closeCalls = 0;
  const origSetAttr = chatBtn.setAttribute.bind(chatBtn);
  chatBtn.setAttribute = (k, v) => {
    if (k === "aria-expanded") {
      if (v === "true") openCalls++;
      else if (v === "false") closeCalls++;
    }
    return origSetAttr(k, v);
  };

  // (a) Click outside character bbox opens messenger and never knocks.
  assert.equal(M.isOpen(), false);
  clickAt(characterBtn, 40, 760); // local (28, 40) -> outside [90..250, 30..230]
  assert.equal(M.isOpen(), true, "click outside character bbox must open messenger");
  assert.equal(openCalls, 1);
  assert.equal(closeCalls, 0);
  assert.equal(knockCalls.length, 0, "click outside character bbox must NOT knock");

  // (b) Click outside character bbox again closes messenger once (never closes on pointerdown then reopens on click).
  clickAt(characterBtn, 320, 780); // local (308, 60) -> outside bbox
  assert.equal(M.isOpen(), false, "second click outside character bbox must close messenger");
  assert.equal(openCalls, 1);
  assert.equal(closeCalls, 1);
  assert.equal(knockCalls.length, 0);

  // (c) Click inside character bbox knocks, posts POST /api/companion/knock (D10), and NEVER opens messenger.
  clickAt(characterBtn, 180, 820); // local (168, 100) -> inside bbox
  assert.equal(M.isOpen(), false, "click inside character bbox must NOT open messenger");
  assert.equal(knockCalls.length, 1, "click inside character bbox must knock");
  assert.equal(knockFetches.length, 1, "glass knock must fire POST /api/companion/knock");
  assert.equal(knockFetches[0].opts.method, "POST");
  assert.equal(knockFetches[0].opts.credentials, "same-origin");
  assert.deepEqual(JSON.parse(knockFetches[0].opts.body), { kind: "knock_startle", count: 1 });

  // (d) Pointer drag > 6px outside bbox does NOT toggle messenger.
  clickAt(characterBtn, 40, 760, 12, 0);
  assert.equal(M.isOpen(), false, "drag > 6px outside bbox must NOT toggle messenger");
  assert.equal(openCalls, 1);

  // Small jitter <= 6px still counts as a toggle click.
  clickAt(characterBtn, 40, 760, 3, 4); // hypot = 5 <= 6
  assert.equal(M.isOpen(), true, "click with <= 6px jitter must still toggle open");
  assert.equal(openCalls, 2);

  // Non-primary button does not toggle.
  clickAt(characterBtn, 40, 760, 0, 0, 2);
  assert.equal(M.isOpen(), true, "secondary button must not toggle messenger");

  // (e) #companion-chat-btn toggles once (closes, then opens).
  clickAt(chatBtn, 310, 745);
  assert.equal(M.isOpen(), false, "#companion-chat-btn must close messenger once");
  assert.equal(closeCalls, 2);
  clickAt(chatBtn, 310, 745);
  assert.equal(M.isOpen(), true, "#companion-chat-btn must open messenger once");
  assert.equal(openCalls, 3);
  clickAt(chatBtn, 310, 745);
  assert.equal(M.isOpen(), false);

  // (f) VRM loading / absent (null bounds): stage click outside HUD buttons toggles messenger gracefully.
  currentBounds = null;
  assert.equal(stage.debugState().projectedBounds, null);
  clickAt(characterBtn, 180, 820);
  assert.equal(M.isOpen(), true, "stage click when character bounds are null must toggle open");
  clickAt(characterBtn, 180, 820);
  assert.equal(M.isOpen(), false, "stage click when character bounds are null must toggle closed");
  assert.equal(knockCalls.length, 1);

  // (g) pointermove updates hit.dataset.area without calling getBoundingClientRect().
  currentBounds = { x: 90, y: 30, width: 160, height: 200 };
  let rectReads = 0;
  hitEl.getBoundingClientRect = () => { rectReads++; return { left: 12, top: 720, right: 352, bottom: 960, width: 340, height: 240 }; };
  characterBtn.dispatchEvent({ type: "pointermove", target: characterBtn, clientX: 40, clientY: 760 });
  assert.equal(hitEl.dataset.area, "messenger");
  characterBtn.dispatchEvent({ type: "pointermove", target: characterBtn, clientX: 180, clientY: 820 });
  assert.equal(hitEl.dataset.area, "character");
  assert.equal(rectReads, 0, "pointermove hit-test must use cached bodyBox without DOM getBoundingClientRect reads");

  stage.dispose();
});

test("Messenger drops a stale snapshot from the same daemon epoch and accepts a restarted daemon's", () => {
  const { M } = makeMessengerHarness({ window: {} });
  const msg = { id: "cm-1", sender: "user", text: "hi", createdAt: 1 };
  M.applySnapshot(snapshotWith([msg], { epoch: 100, seq: 5 }));
  M.applySnapshot(snapshotWith([], { epoch: 100, seq: 3 }));
  assert.equal(M.getState().snapshot.seq, 5, "an older seq in the same epoch must be dropped");
  assert.equal(M.getState().snapshot.messages.length, 1);
  M.applySnapshot(snapshotWith([], { epoch: 200, seq: 1 }));
  assert.equal(M.getState().snapshot.seq, 1, "a new daemon epoch restarts seq");
  M.dispose();
});

test("Phase 2: Messenger mounts compact Brain iframe, pauses on close, syncs theme, reloads on handoff, bridges origin-checked user-send, pops out, and retries fallback", async () => {
  const winListeners = {};
  const openedUrls = [];
  const reflexSends = [];
  const postedToFrame = [];
  let hideFrameCalls = 0;
  let brainStep = 0;

  const doc = {
    documentElement: { getAttribute: k => (k === "data-theme" ? "dark" : null) },
    body: null,
    createElement(tag) {
      const el = makeFakeElement(tag, doc);
      if (tag === "iframe") {
        el.contentWindow = {
          wsHideFrame() { hideFrameCalls++; },
          postMessage(payload, targetOrigin) { postedToFrame.push({ payload, targetOrigin }); }
        };
      }
      return el;
    },
    createElementNS: (_ns, tag) => makeFakeElement(tag, doc),
    createTextNode: s => ({ nodeType: 3, textContent: String(s) }),
    addEventListener() {}
  };
  doc.body = makeFakeElement("body", doc);

  const { M, context } = loadCompanionWithMessenger({
    document: doc,
    window: {
      location: { origin: "http://127.0.0.1:41234" },
      open(url, target, features) { openedUrls.push({ url, target, features }); },
      addEventListener(type, fn, opts) {
        (winListeners[type] ||= []).push(fn);
        if (opts && opts.signal) {
          opts.signal.addEventListener("abort", () => {
            winListeners[type] = (winListeners[type] || []).filter(f => f !== fn);
          });
        }
      }
    }
  });
  context.window.WsCompanion.reflex = { onUserSend: data => reflexSends.push(data) };

  M.mount({
    document: doc,
    getAnchorRect: () => ({ left: 12, top: 720, right: 352, bottom: 960, width: 340, height: 240 }),
    fetch: async url => {
      if (url === "/api/companion/brain") {
        brainStep++;
        if (brainStep === 1) {
          return { ok: false, status: 503, json: async () => ({ ready: false, error: "Brain warming up" }) };
        }
        return {
          ok: true,
          status: 200,
          json: async () => ({
            ready: true,
            replyId: "brain-rid-1",
            harness: "agy",
            model: "gemini-pro",
            personaName: "Mika"
          })
        };
      }
      return { ok: true, status: 200, json: async () => ({ seq: 1, state: "laptop_ambient", posture: "laptop", messages: [], unreadClient: 0, unreadAgent: 0, tier1Summary: [] }) };
    }
  });

  const popup = doc.body.children.find(el => el.id === "companion-messenger");
  const fallback = popup.all().find(el => el.className === "ws-cm-brain-fallback");
  const retryBtn = popup.all().find(el => el.dataset?.action === "retry-brain");
  const popoutBtn = popup.all().find(el => el.dataset?.action === "open-full-tab");
  const brainWrap = popup.all().find(el => el.className === "ws-cm-brain-wrap");
  const nameEl = popup.all().find(el => el.className === "ws-cm-name");
  const subtitleEl = popup.all().find(el => el.className === "ws-cm-subtitle");

  // 1. First open hits ready:false fallback banner.
  M.open();
  await new Promise(r => setImmediate(r));
  assert.equal(popup.dataset.mode, "legacy");
  assert.equal(fallback.hidden, false, "fallback status must show when brain is not ready");
  assert.equal(brainWrap.hidden, true);
  assert.equal(popoutBtn.hidden, true);

  // 2. Clicking retry fetches /api/companion/brain again and mounts compact iframe.
  retryBtn.dispatchEvent({ type: "click" });
  await new Promise(r => setImmediate(r));
  assert.equal(popup.dataset.mode, "brain");
  assert.equal(fallback.hidden, true);
  assert.equal(brainWrap.hidden, false);
  assert.equal(popoutBtn.hidden, false);
  assert.equal(nameEl.textContent, "Mika");
  assert.equal(subtitleEl.textContent, "agy · gemini-pro");
  const frame = brainWrap.children[0];
  assert.ok(frame && frame.tagName === "IFRAME" && frame.className === "ws-cm-frame");
  assert.equal(frame.getAttribute("src"), "/s/brain-rid-1?frame=1&compact=1");

  // Fire iframe load -> syncs theme + shown:true.
  postedToFrame.length = 0;
  frame.dispatchEvent({ type: "load" });
  assert.ok(postedToFrame.some(m => m.payload.wsShell === "theme" && m.payload.theme === "dark" && m.targetOrigin === "http://127.0.0.1:41234"));
  assert.ok(postedToFrame.some(m => m.payload.wsShell === "shown" && m.payload.shown === true));

  // 3. Theme sync pushes wsShell:theme to the compact iframe.
  postedToFrame.length = 0;
  M.syncTheme("light");
  assert.deepEqual(JSON.parse(JSON.stringify(postedToFrame)), [{ payload: { wsShell: "theme", theme: "light" }, targetOrigin: "http://127.0.0.1:41234" }]);

  // 4. Handoff notification updates iframe src cleanly to the new Brain replyId.
  M.onBrainChanged({ kind: "handoff", replyId: "brain-rid-2" });
  assert.equal(frame.getAttribute("src"), "/s/brain-rid-2?frame=1&compact=1");

  // 5. Origin-checked user-send postMessage bridge: rejects foreign origin / foreign source, forwards valid frame message.
  const dispatchMsg = ev => { for (const fn of winListeners.message || []) fn(ev); };
  dispatchMsg({ origin: "https://evil.example", source: frame.contentWindow, data: { wsCompanionCompact: "user-send", rid: "brain-rid-2" } });
  dispatchMsg({ origin: "http://127.0.0.1:41234", source: {}, data: { wsCompanionCompact: "user-send", rid: "brain-rid-2" } });
  assert.equal(reflexSends.length, 0, "foreign origin or wrong source must be ignored");
  dispatchMsg({ origin: "http://127.0.0.1:41234", source: frame.contentWindow, data: { wsCompanionCompact: "user-send", rid: "brain-rid-2" } });
  assert.equal(reflexSends.length, 1);
  assert.equal(reflexSends[0].rid, "brain-rid-2");

  // 6. Pop-out button opens full session tab with noopener.
  popoutBtn.dispatchEvent({ type: "click" });
  assert.deepEqual(JSON.parse(JSON.stringify(openedUrls)), [{ url: "/s/brain-rid-2", target: "_blank", features: "noopener" }]);

  // 7. Closing messenger calls wsHideFrame() and posts wsShell:shown false to pause SSE/WS.
  postedToFrame.length = 0;
  M.close();
  assert.equal(hideFrameCalls, 1, "close must invoke synchronous wsHideFrame()");
  assert.ok(postedToFrame.some(m => m.payload.wsShell === "shown" && m.payload.shown === false));

  M.dispose();
});
