// Runtime tests for the shared-input (non-composer) Spacebar PTT hub.
// The real dashboard-voice-ptt.js, dashboard-voice.js and dashboard-voice-field.js
// run inside a vm against a small DOM harness that implements capture/bubble
// dispatch (window -> document -> target -> document -> window).
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

const assets = path.join(__dirname, "assets");
const read = name => fs.readFileSync(path.join(assets, name), "utf8");
const pttJS = read("dashboard-voice-ptt.js");
const voiceJS = read("dashboard-voice.js");
const fieldJS = read("dashboard-voice-field.js");
const css = read("dashboard-voice.css");
const flush = () => new Promise(r => setImmediate(r));

let opsLog = [];

class Target {
  constructor() { this.listeners = new Map(); }
  addEventListener(type, fn, opt) {
    const capture = opt === true || !!(opt && opt.capture);
    if (!this.listeners.has(type)) this.listeners.set(type, []);
    this.listeners.get(type).push({ fn, capture });
  }
  removeEventListener(type, fn, opt) {
    const capture = opt === true || !!(opt && opt.capture);
    const list = this.listeners.get(type);
    if (!list) return;
    const i = list.findIndex(l => l.fn === fn && l.capture === capture);
    if (i >= 0) list.splice(i, 1);
  }
  count(type) { return (this.listeners.get(type) || []).length; }
  total() { let n = 0; for (const l of this.listeners.values()) n += l.length; return n; }
  // Plain dispatch (custom events): every listener, either phase.
  dispatchEvent(ev) {
    ev = typeof ev === "string" ? { type: ev } : ev;
    for (const l of (this.listeners.get(ev.type) || []).slice()) l.fn(ev);
    return true;
  }
}

class Node_ extends Target {
  constructor(tag, cls) {
    super();
    this.tagName = String(tag || "div").toUpperCase();
    this.className = cls || "";
    this.id = "";
    this.parentNode = null;
    this.childNodes = [];
    this.attrs = new Map();
    this.style = new Proxy({}, { set(t, k, v) { opsLog.push("write:style"); t[k] = v; return true; } });
    this._hidden = false;
    this._disabled = false;
    this.value = "";
    this.selectionStart = 0;
    this.selectionEnd = 0;
    this.rect = { left: 100, top: 10, width: 400, height: 60, bottom: 70 };
    this._text = "";
    this.nodeType = 1;
  }
  get hidden() { return this._hidden; }
  set hidden(v) { if (this.className === "cvox-cap") opsLog.push("write:hidden"); this._hidden = !!v; }
  get disabled() { return this._disabled; }
  set disabled(v) { this._disabled = !!v; }
  get children() { return this.childNodes.filter(n => n.nodeType === 1); }
  get nextSibling() {
    if (!this.parentNode) return null;
    const i = this.parentNode.childNodes.indexOf(this);
    return this.parentNode.childNodes[i + 1] || null;
  }
  get previousElementSibling() {
    if (!this.parentNode) return null;
    const sibs = this.parentNode.children;
    return sibs[sibs.indexOf(this) - 1] || null;
  }
  get textContent() {
    return this.childNodes.length ? this.childNodes.map(n => (n.nodeType === 3 ? n.nodeValue : n.textContent)).join("") : this._text;
  }
  set textContent(v) {
    this._text = String(v == null ? "" : v);
    this.childNodes.forEach(c => { c.parentNode = null; });
    this.childNodes = [];
  }
  setAttribute(k, v) { this.attrs.set(k, String(v)); }
  getAttribute(k) { return this.attrs.has(k) ? this.attrs.get(k) : null; }
  hasAttribute(k) { return this.attrs.has(k); }
  removeAttribute(k) { this.attrs.delete(k); }
  appendChild(c) {
    if (c.parentNode) c.parentNode.removeChild(c);
    c.parentNode = this;
    this.childNodes.push(c);
    return c;
  }
  insertBefore(c, ref) {
    if (c.parentNode) c.parentNode.removeChild(c);
    c.parentNode = this;
    const i = ref ? this.childNodes.indexOf(ref) : -1;
    if (i >= 0) this.childNodes.splice(i, 0, c); else this.childNodes.push(c);
    return c;
  }
  removeChild(c) {
    const i = this.childNodes.indexOf(c);
    if (i >= 0) this.childNodes.splice(i, 1);
    c.parentNode = null;
    return c;
  }
  remove() { if (this.parentNode) this.parentNode.removeChild(this); }
  contains(n) { return n === this || this.childNodes.some(c => c === n || (c.nodeType === 1 && c.contains(n))); }
  focus() { this.focused = true; }
  getBoundingClientRect() { opsLog.push("read"); return this.rect; }
  setRangeText(rep, a, b, mode) {
    this.value = this.value.slice(0, a) + rep + this.value.slice(b);
    if (mode === "end") this.selectionStart = this.selectionEnd = a + rep.length;
  }
  setSelectionRange(a, b) { this.selectionStart = a; this.selectionEnd = b; }
  querySelector(sel) {
    const eq = sel.match(/^\[([^=\]]+)="([^"]*)"\]$/);
    const walk = n => {
      for (const c of n.children) {
        const hit = sel.startsWith(".") ? c.className.split(/\s+/).includes(sel.slice(1))
          : eq ? c.getAttribute(eq[1]) === eq[2] : false;
        if (hit) return c;
        const f = walk(c);
        if (f) return f;
      }
      return null;
    };
    return walk(this);
  }
}

// Minimal selector matcher for the rules in dashboard-voice.css: compound
// selectors (tag, .class, #id, [attr="v"]) joined by descendant and "+" combinators.
function matchCompound(el, compound) {
  const parts = compound.match(/^[a-z][a-z0-9]*|[.#][\w-]+|\[[^\]]+\]/gi) || [];
  return parts.every(p => {
    if (p[0] === ".") return el.className.split(/\s+/).includes(p.slice(1));
    if (p[0] === "#") return el.id === p.slice(1);
    if (p[0] === "[") {
      const m = p.match(/^\[([\w-]+)(?:="([^"]*)")?\]$/);
      return m[2] === undefined ? el.hasAttribute(m[1]) : el.getAttribute(m[1]) === m[2];
    }
    return el.tagName === p.toUpperCase();
  });
}
function matchSelector(el, selector) {
  const toks = selector.replace(/\s*\+\s*/g, " + ").trim().split(/\s+/);
  function at(node, i) {
    if (!node || !matchCompound(node, toks[i])) return false;
    if (i === 0) return true;
    if (toks[i - 1] === "+") return at(node.previousElementSibling, i - 2);
    for (let p = node.parentNode; p; p = p.parentNode) if (at(p, i - 1)) return true;
    return false;
  }
  return at(el, toks.length - 1);
}
function disablingSelectors() {
  const out = [];
  const re = /([^{}]+)\{([^}]*)\}/g;
  let m;
  while ((m = re.exec(css.replace(/\/\*[\s\S]*?\*\//g, "")))) {
    if (/pointer-events:\s*none/.test(m[2]) && /data-voice-locked/.test(m[1])) {
      m[1].split(",").forEach(s => out.push(s.trim()));
    }
  }
  return out;
}

function makeEnv() {
  opsLog = [];
  const win = new Target();
  const doc = new Target();
  const html = new Node_("html");
  const body = new Node_("body");
  html.appendChild(body);
  Object.assign(doc, { hidden: false, documentElement: html, body, defaultView: win });
  doc.createElement = tag => { const el = new Node_(tag); el.ownerDocument = doc; env.created[tag] = (env.created[tag] || 0) + 1; return el; };

  // Event path: window capture -> document capture -> target -> document bubble -> window bubble.
  function fire(target, type, init) {
    const ev = Object.assign({
      type, target, defaultPrevented: false, _sp: false, _si: false,
      preventDefault() { this.defaultPrevented = true; },
      stopPropagation() { this._sp = true; },
      stopImmediatePropagation() { this._si = true; this._sp = true; }
    }, init || {});
    const stages = [
      [win, true], [doc, true], [target, null], [doc, false], [win, false]
    ];
    for (const [t, capture] of stages) {
      for (const l of (t.listeners.get(type) || []).slice()) {
        if (capture !== null && l.capture !== capture) continue;
        l.fn(ev);
        if (ev._si) return ev;
      }
      if (ev._sp) return ev;
    }
    return ev;
  }

  const timers = [];
  let now = 0, nextId = 1;
  win.setTimeout = (fn, ms) => { const id = nextId++; timers.push({ id, fn, due: now + ms }); return id; };
  win.clearTimeout = id => { const i = timers.findIndex(t => t.id === id); if (i >= 0) timers.splice(i, 1); };
  win.innerWidth = 1000;
  win.innerHeight = 800;
  win.wsT = k => k;
  win.document = doc;
  win.location = { origin: "http://127.0.0.1:8787" };
  win.navigator = { language: "en-US" };
  win.window = win;
  win.Event = class { constructor(type) { this.type = type; } };
  let fetchCount = 0;
  win.fetch = async () => {
    fetchCount++;
    return { ok: true, json: async () => ({ order: ["fake"], backends: [{ id: "fake", kind: "native", available: true }], selected: { backend: "fake" } }) };
  };

  const ctx = vm.createContext({
    window: win, document: doc, navigator: win.navigator, Event: win.Event, Promise, Error, Math, JSON, Object, String, Array,
    Blob, TextDecoder, TextEncoder, AbortController, ArrayBuffer, DataView, Float32Array, Uint8Array, console, setTimeout, clearTimeout
  });
  vm.runInContext(pttJS, ctx);
  vm.runInContext(voiceJS, ctx);

  const backend = {
    id: "fake", kind: "native", starts: 0, stops: 0, cancels: 0, retries: 0,
    available: () => true,
    start() { backend.starts++; return Promise.resolve(); },
    stop() { backend.stops++; return new Promise((res, rej) => { backend.resolveStop = res; backend.rejectStop = rej; }); },
    retry() { backend.retries++; return Promise.resolve({ text: "retried" }); },
    hasLastAudio: () => true,
    cancel() { backend.cancels++; },
    discardLast() {}
  };
  win.WsVoiceInput.registerBackend(backend);

  const env = {
    win, doc, body, backend, fire, ctx, created: {},
    fetchCount: () => fetchCount,
    advance(ms) {
      now += ms;
      for (const t of timers.slice()) if (t.due <= now) { timers.splice(timers.indexOf(t), 1); t.fn(); }
    },
    baseline: 0,
    field(withEditor) {
      const el = new Node_("textarea");
      el.id = "ta" + (body.childNodes.length + 1);
      el.ownerDocument = doc;
      if (withEditor) {
        const editor = new Node_("div", "wsc-editor");
        editor.setAttribute("contenteditable", "true");
        el.wsc = { editor };
      }
      body.appendChild(el);
      return el;
    },
    attach(el) {
      const handle = win.WsVoiceInput.attachField(el);
      return handle;
    },
    key(el, type, init) { return fire(el, type, Object.assign({ key: " ", code: "Space", repeat: false }, init)); },
    startHold(el) { env.key(el, "keydown"); env.advance(500); },
    release(el) { return env.key(el, "keyup"); },
    hub: () => doc.__wsVoiceFieldHub,
    listenerTotal: () => win.total() + doc.total()
  };
  vm.runInContext(fieldJS, ctx);
  env.baseline = env.listenerTotal();
  return env;
}

async function ready(env, ...els) {
  const handles = els.map(el => env.attach(el));
  await flush();
  return handles;
}

// ---- WsInput integration (static wiring that cannot run without the full bundle) ----

test("all six shared WsInput surfaces opt into voice and the bundle loads the hub after the voice engine", () => {
  ["dashboard-composer-pickers.js", "dashboard-composer-selections.js", "dashboard-composer.js",
    "dashboard-companion-messenger.js", "memo.js", "dashboard-tracker.js"
  ].forEach(name => assert.match(read(name), /voice: true/));
  const pages = fs.readFileSync(path.join(__dirname, "pages.go"), "utf8");
  assert.ok(pages.indexOf('"dashboard-voice.js"') < pages.indexOf('"dashboard-voice-field.js"'));
});

// ---- Typing is never disturbed ----

test("normal typing, two-key rollover, Space key-repeat and IME never swallow keys or cancel the hold", async () => {
  const env = makeEnv();
  const el = env.field();
  await ready(env, el);
  const seen = [];
  env.doc.addEventListener("keydown", e => seen.push(e), false);

  // Normal typing passes through untouched.
  const a = env.key(el, "keydown", { key: "a", code: "KeyA" });
  assert.equal(a.defaultPrevented, false);
  assert.equal(a._si, false);
  assert.equal(seen.length, 1);

  // Space then another key within the hold window: rollover is typing, not PTT.
  env.key(el, "keydown");
  assert.equal(env.hub().fields[0].ptt.isArmed(), true);
  const b = env.key(el, "keydown", { key: "b", code: "KeyB" });
  assert.equal(b.defaultPrevented, false);
  assert.equal(env.hub().fields[0].ptt.isArmed(), false);
  env.advance(600);
  assert.equal(env.backend.starts, 0);
  env.release(el);

  // Space key-repeat during the 500 ms hold keeps the hold armed; it starts on time.
  seen.length = 0;
  env.key(el, "keydown");
  for (let i = 0; i < 4; i++) {
    env.advance(100);
    const rep = env.key(el, "keydown", { repeat: true });
    assert.equal(rep._si, false, "key-repeat must not be swallowed by the voice hub");
    assert.equal(env.hub().fields[0].ptt.isArmed(), true);
  }
  assert.equal(seen.length, 5, "every Space keydown reached document listeners");
  env.advance(100);
  assert.equal(env.backend.starts, 1);
  assert.equal(el.disabled, true);
  env.release(el);
  await flush();

  // IME composition keys never arm.
  const env2 = makeEnv();
  const el2 = env2.field();
  await ready(env2, el2);
  env2.key(el2, "keydown", { isComposing: true });
  env2.key(el2, "keydown", { key: "Process", code: "", keyCode: 229 });
  assert.equal(env2.hub().fields[0].ptt.isArmed(), false);
  env2.advance(600);
  assert.equal(env2.backend.starts, 0);
});

// ---- Escape ----

test("Escape cancels only voice when armed or live and is invisible to document capture listeners; idle Escape propagates", async () => {
  const env = makeEnv();
  const drawerCapture = [], overviewBubble = [];
  env.doc.addEventListener("keydown", e => { if (e.key === "Escape") drawerCapture.push(e); }, true);
  env.doc.addEventListener("keydown", e => { if (e.key === "Escape") overviewBubble.push(e); }, false);
  const el = env.field();
  await ready(env, el);
  const esc = () => env.fire(el, "keydown", { key: "Escape", code: "Escape" });

  // Idle: normal propagation, nothing prevented.
  let ev = esc();
  assert.equal(ev.defaultPrevented, false);
  assert.equal(drawerCapture.length, 1);
  assert.equal(overviewBubble.length, 1);

  // Armed (hold pending): Escape disarms and neither document phase sees it.
  env.key(el, "keydown");
  assert.equal(env.hub().fields[0].ptt.isArmed(), true);
  ev = esc();
  assert.equal(ev.defaultPrevented, true);
  assert.equal(env.hub().fields[0].ptt.isArmed(), false);
  assert.equal(drawerCapture.length, 1);
  assert.equal(overviewBubble.length, 1);
  env.advance(600);
  assert.equal(env.backend.starts, 0);
  env.release(el);

  // Live recording: Escape cancels and discards, isolated again.
  env.startHold(el);
  assert.equal(env.hub().controller.getState(), "recording");
  ev = esc();
  assert.equal(ev.defaultPrevented, true);
  assert.equal(env.backend.cancels, 1);
  assert.equal(env.hub().controller.getState(), "idle");
  assert.equal(el.disabled, false);
  assert.equal(drawerCapture.length, 1);
  assert.equal(overviewBubble.length, 1);
  env.release(el);

  // Non-Escape keys during a live hold are never intercepted.
  env.startHold(el);
  ev = env.fire(el, "keydown", { key: "Enter", code: "Enter" });
  assert.equal(ev.defaultPrevented, false);
  assert.equal(ev._si, false);
  assert.equal(env.hub().controller.getState(), "recording");
});

// ---- CSS lock scope ----

test("locking the main composer disables its field only, never #composer or the #cvox stop button", () => {
  const composer = new Node_("div", "composer");
  composer.id = "composer";
  const cfield = new Node_("div", "cfield");
  const textarea = new Node_("textarea");
  const editor = new Node_("div", "wsc-editor");
  const cvox = new Node_("button", "cvox");
  cvox.id = "cvox";
  cfield.appendChild(textarea);
  cfield.appendChild(editor);
  composer.appendChild(cfield);
  composer.appendChild(cvox);
  composer.setAttribute("data-voice-locked", "1");
  const rules = disablingSelectors();
  assert.ok(rules.length >= 3);
  const hit = el => rules.some(s => matchSelector(el, s));
  assert.equal(hit(composer), false, "#composer must keep pointer events");
  assert.equal(hit(cvox), false, "#cvox stop/cancel button must stay clickable");
  assert.equal(hit(cfield), false);
  assert.equal(hit(textarea), true);
  assert.equal(hit(editor), true);

  // Secondary field lock: textarea and its editor, nothing else.
  const host = new Node_("div", "memo");
  const ta = new Node_("textarea");
  const ed = new Node_("div", "wsc-editor");
  const other = new Node_("div", "wsc-editor");
  host.appendChild(ta);
  host.appendChild(ed);
  host.appendChild(other);
  ta.setAttribute("data-voice-locked", "1");
  assert.equal(hit(ta), true);
  assert.equal(hit(ed), true);
  assert.equal(hit(other), false);
  assert.equal(hit(host), false);
  assert.ok(!rules.some(s => /^\[data-voice-locked/.test(s)), "no bare attribute selector");
});

test("floating capsule CSS: token-relative z-index, whisper edge, no border", () => {
  const rule = /\.cvox-cap\[data-floating="1"\]\s*\{([^}]*)\}/.exec(css)[1];
  assert.match(rule, /z-index:\s*calc\(var\(--z-modal, 70\) \+ 10\)/);
  assert.match(rule, /inset 0 0 0 1px var\(--wb-line\)/);
  assert.doesNotMatch(rule, /border:/);
});

// ---- Selection popover (mnote) ----

test("locking mnote does not trigger the selection popover focusout dismissal", async () => {
  const src = read("dashboard-composer-selections.js");
  const m = /mbtn\.addEventListener\("focusout", (function \(e\) \{[\s\S]*?\n      \})\);/.exec(src);
  assert.ok(m, "focusout handler found");
  const env = makeEnv();
  const mnote = env.field(true);
  let addMentionCalls = 0;
  const mbtn = { contains: () => false };
  const handler = new Function("mnote", "mpend", "mbtn", "addMention", "return (" + m[1] + ");")(
    mnote, { edit: true }, mbtn, () => { addMentionCalls++; });
  // A disabled textarea blurs synchronously in browsers.
  Object.defineProperty(mnote, "disabled", {
    get() { return this._d; },
    set(v) { this._d = !!v; if (v) handler({ relatedTarget: null }); }
  });
  await ready(env, mnote);
  env.startHold(mnote);
  assert.equal(mnote.disabled, true);
  assert.equal(mnote.getAttribute("data-voice-locked"), "1");
  assert.equal(mnote.wsc.editor.getAttribute("contenteditable"), "false");
  assert.equal(mnote.wsc.editor.getAttribute("aria-disabled"), "true");
  assert.equal(addMentionCalls, 0, "voice lock must not dismiss the popover");
  env.hub().cancelActive();
  assert.equal(mnote.getAttribute("data-voice-locked"), null);
  assert.equal(mnote.wsc.editor.getAttribute("contenteditable"), "true");
  assert.equal(mnote.wsc.editor.getAttribute("aria-disabled"), "false");

  // Control: a genuine blur still saves the note.
  handler({ relatedTarget: null });
  assert.equal(addMentionCalls, 1);
});

// ---- Window blur / lifecycle ----

test("window blur stops the hold and still transcribes the spoken audio instead of discarding it", async () => {
  const env = makeEnv();
  const el = env.field();
  await ready(env, el);
  env.startHold(el);
  assert.equal(env.hub().controller.getState(), "recording");
  env.win.dispatchEvent({ type: "blur" });
  assert.equal(env.backend.stops, 1);
  assert.equal(env.backend.cancels, 0, "blur must not cancel");
  assert.equal(env.hub().controller.getState(), "transcribing");
  env.backend.resolveStop({ text: "hello" });
  await flush();
  assert.equal(env.hub().controller.getState(), "idle");
  assert.equal(el.value, "hello");
  assert.equal(el.disabled, false);

  // Blur while a transcription is already in flight leaves it alone too.
  env.startHold(el);
  env.release(el);
  assert.equal(env.hub().controller.getState(), "transcribing");
  env.win.dispatchEvent({ type: "blur" });
  assert.equal(env.hub().controller.getState(), "transcribing");
  assert.equal(env.backend.cancels, 0);
  env.backend.resolveStop({ text: "world" });
  await flush();
  assert.equal(el.value, "hello world");
});

test("page lifecycle events tear the live hold down and capability events refresh it", async () => {
  const env = makeEnv();
  const el = env.field();
  await ready(env, el);
  const cases = [
    () => { env.doc.hidden = true; env.doc.dispatchEvent({ type: "visibilitychange" }); env.doc.hidden = false; },
    () => { env.doc.documentElement.setAttribute("data-animation-paused", ""); env.doc.dispatchEvent({ type: "ws-page-visibility" }); env.doc.documentElement.removeAttribute("data-animation-paused"); },
    () => env.doc.dispatchEvent({ type: "ws-native-speech-hidden" }),
    () => env.win.dispatchEvent({ type: "pagehide" }),
    () => env.win.dispatchEvent({ type: "beforeunload" })
  ];
  let cancels = 0;
  for (const trigger of cases) {
    env.startHold(el);
    assert.equal(el.disabled, true);
    trigger();
    cancels++;
    assert.equal(env.backend.cancels, cancels);
    assert.equal(env.hub().controller.getState(), "idle");
    assert.equal(el.disabled, false);
    env.release(el);
  }
  // Visible, non-paused tab: ws-page-visibility is a no-op.
  env.startHold(el);
  env.doc.dispatchEvent({ type: "ws-page-visibility" });
  assert.equal(env.hub().controller.getState(), "recording");
  env.hub().cancelActive();

  const before = env.fetchCount();
  env.win.dispatchEvent({ type: "ws-voice-capabilities-refresh" });
  env.doc.dispatchEvent({ type: "ws-account-changed" });
  assert.equal(env.fetchCount(), before + 2);
});

// ---- Multi-field contention ----

test("switching fields clears the previous error capsule, stale Retry cannot hijack, destroy releases everything", async () => {
  const env = makeEnv();
  const A = env.field(), B = env.field();
  const [hA, hB] = await ready(env, A, B);
  const hub = env.hub();
  const caps = () => env.body.children.filter(c => c.className === "cvox-cap");

  // A fails: error capsule, produced by a single render.
  env.startHold(A);
  env.release(A);
  env.created.button = 0;
  env.backend.rejectStop({ code: "upstream_timeout" });
  await flush();
  assert.equal(hub.controller.getState(), "error");
  assert.equal(caps().length, 1);
  const capA = caps()[0];
  assert.equal(capA.hidden, false);
  assert.equal(capA.getAttribute("data-state"), "error");
  assert.equal(env.created.button, 2, "error capsule rendered once (resend + dismiss), not twice");
  assert.equal(capA.getAttribute("role"), "status");
  assert.equal(capA.getAttribute("aria-live"), "polite");
  const staleResend = capA.querySelector('[data-voice-action="resend"]').listeners.get("click")[0].fn;
  assert.equal(A.disabled, false);

  // B takes over: A's capsule disappears and cannot cancel B.
  env.startHold(B);
  assert.equal(hub.active.el, B);
  assert.equal(capA.hidden, true);
  assert.equal(B.disabled, true);
  assert.equal(hub.controller.getState(), "recording");

  // Stale Retry from A's old capsule must not hijack the hub or restart anything.
  staleResend({});
  assert.equal(hub.active.el, B);
  assert.equal(env.backend.retries, 0);
  assert.equal(hub.controller.getState(), "recording");

  // Destroying the live field cancels it, clears hub.active and removes its capsule.
  const capB = caps().find(c => c !== capA);
  assert.ok(capB);
  hB.destroy();
  assert.equal(hub.active, null);
  assert.equal(B.disabled, false);
  assert.equal(caps().includes(capB), false);
  assert.equal(hub.fields.length, 1);
  assert.equal(hub.controller.getState(), "idle");

  hA.destroy();
  assert.equal(caps().length, 0);
  assert.equal(env.hub(), undefined);
  assert.equal(env.listenerTotal(), 0, "hub removed every document/window listener");
});

test("Retry from the owning field retries and rebinds nothing else", async () => {
  const env = makeEnv();
  const A = env.field();
  await ready(env, A);
  env.startHold(A);
  env.release(A);
  env.backend.rejectStop({ code: "upstream_timeout" });
  await flush();
  const capA = env.body.children.find(c => c.className === "cvox-cap");
  capA.querySelector('[data-voice-action="resend"]').listeners.get("click")[0].fn({});
  await flush();
  assert.equal(env.backend.retries, 1);
  assert.equal(A.value, "retried");
  assert.equal(env.hub().active.el, A);
});

// ---- Floating capsule geometry ----

test("floating capsule reads geometry before writing, clamps to the viewport and follows resize", async () => {
  const env = makeEnv();
  const el = env.field();
  el.rect = { left: 900, top: 40, width: 80, height: 30, bottom: 70 };
  await ready(env, el);
  env.startHold(el);
  env.release(el);
  opsLog.length = 0;
  env.backend.rejectStop({ code: "upstream_timeout" });
  await flush();
  const cap = env.body.children.find(c => c.className === "cvox-cap");
  const lastRead = opsLog.lastIndexOf("read");
  const firstWrite = opsLog.findIndex(o => o.startsWith("write"));
  assert.ok(lastRead >= 0 && firstWrite > lastRead, "all reads precede the first write: " + opsLog.join(","));
  // Falls back to the field rect (not <body>); 1000 - 280 - 4 clamps the left edge.
  assert.equal(cap.style.left, "716px");
  assert.equal(cap.style.top, "74px");
  assert.equal(env.win.count("resize"), 1);
  env.win.innerWidth = 500;
  env.win.dispatchEvent({ type: "resize" });
  assert.equal(cap.style.left, "216px");
  assert.equal(env.win.count("scroll"), 1);

  // Dismissing hides the capsule and releases the viewport listeners.
  cap.querySelector('[data-voice-action="dismiss"]').listeners.get("click")[0].fn({});
  assert.equal(cap.hidden, true);
  assert.equal(env.win.count("resize"), 0);
  assert.equal(env.win.count("scroll"), 0);
});

// ---- Stylistic regressions on the source ----

test("hub registers Escape on window capture and drops the stopPropagation-only form", () => {
  assert.match(fieldJS, /\[win, "keydown", onEscape, true\]/);
  assert.match(fieldJS, /e\.key !== "Escape"/);
  assert.match(fieldJS, /stopImmediatePropagation/);
  assert.doesNotMatch(fieldJS, /\[doc, "keydown"/);
});
