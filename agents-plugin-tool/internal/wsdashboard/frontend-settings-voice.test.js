const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

function readAsset(name) {
  return fs.readFileSync(path.join(__dirname, "assets", name), "utf8");
}

function readLocale(name) {
  return JSON.parse(fs.readFileSync(path.join(__dirname, "..", "wsi18n", "tables", name + ".json"), "utf8"));
}

// Minimal DOM harness supporting selectors, datasets, classList, events, forms, and geometry.
function createFakeDOM() {
  class FakeClassList {
    constructor(el) { this.el = el; this.set = new Set(); }
    _sync() { this.el._className = Array.from(this.set).join(" "); }
    _load(str) {
      this.set.clear();
      for (const token of String(str || "").split(/\s+/).filter(Boolean)) this.set.add(token);
      this._sync();
    }
    add(...names) { names.forEach(n => { if (n) this.set.add(n); }); this._sync(); }
    remove(...names) { names.forEach(n => this.set.delete(n)); this._sync(); }
    toggle(name, force) {
      const next = force === undefined ? !this.set.has(name) : !!force;
      if (next) this.set.add(name); else this.set.delete(name);
      this._sync();
      return next;
    }
    contains(name) { return this.set.has(name); }
  }

  function camelToKebab(s) {
    return String(s).replace(/[A-Z]/g, m => "-" + m.toLowerCase());
  }

  class FakeElement {
    constructor(tag, doc) {
      this.tagName = String(tag || "div").toUpperCase();
      this.nodeType = 1;
      this.ownerDocument = doc;
      this.parentNode = null;
      this.childNodes = [];
      this.attributes = Object.create(null);
      this.style = {};
      this.classList = new FakeClassList(this);
      this._className = "";
      this._textContent = "";
      this.listeners = Object.create(null);
      this.hidden = false;
      this.disabled = false;
      this.value = "";
      this.type = "";
      this.placeholder = "";
      this.id = "";
      this.title = "";
      this.tabIndex = 0;
      this.draggable = false;
      this.focused = false;
      this.scrolledIntoView = false;
      this._rect = { left: 0, top: 0, right: 140, bottom: 52, width: 140, height: 52 };
      const self = this;
      this.dataset = new Proxy(Object.create(null), {
        set(target, prop, val) {
          target[prop] = String(val);
          self.attributes["data-" + camelToKebab(prop)] = String(val);
          return true;
        },
        get(target, prop) { return target[prop]; }
      });
    }
    get className() { return this._className; }
    set className(v) { this.classList._load(v); }
    get children() { return this.childNodes.filter(n => n && n.nodeType === 1); }
    get textContent() {
      if (this.childNodes.length) {
        return this.childNodes.map(c => typeof c === "string" ? c : c.textContent).join("");
      }
      return this._textContent;
    }
    set textContent(v) {
      this.childNodes.forEach(c => { if (c && typeof c === "object") c.parentNode = null; });
      const s = String(v == null ? "" : v);
      this.childNodes = s ? [s] : [];
      this._textContent = s;
    }
    get isConnected() {
      let cur = this;
      while (cur) {
        if (cur === this.ownerDocument.body) return true;
        cur = cur.parentNode;
      }
      return false;
    }
    setAttribute(name, val) {
      this.attributes[name] = String(val);
      if (name === "id") this.id = String(val);
      if (name === "class") this.className = String(val);
      if (name.startsWith("data-")) {
        const camel = name.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase());
        this.dataset[camel] = String(val);
      }
    }
    getAttribute(name) { return Object.prototype.hasOwnProperty.call(this.attributes, name) ? this.attributes[name] : null; }
    removeAttribute(name) { delete this.attributes[name]; }
    append(...items) {
      for (const item of items) {
        if (item == null) continue;
        if (typeof item === "string") {
          this.childNodes.push(item);
        } else {
          if (item.parentNode) item.remove();
          item.parentNode = this;
          this.childNodes.push(item);
        }
      }
    }
    appendChild(item) { this.append(item); return item; }
    prepend(...items) {
      const normalized = [];
      for (const item of items) {
        if (item == null) continue;
        if (typeof item === "object") {
          if (item.parentNode) item.remove();
          item.parentNode = this;
        }
        normalized.push(item);
      }
      this.childNodes = normalized.concat(this.childNodes);
    }
    replaceChildren(...items) {
      this.childNodes.forEach(c => { if (c && typeof c === "object") c.parentNode = null; });
      this.childNodes = [];
      this._textContent = "";
      this.append(...items);
    }
    remove() {
      if (!this.parentNode) return;
      const idx = this.parentNode.childNodes.indexOf(this);
      if (idx >= 0) this.parentNode.childNodes.splice(idx, 1);
      this.parentNode = null;
    }
    contains(other) {
      let cur = other;
      while (cur) {
        if (cur === this) return true;
        cur = cur.parentNode;
      }
      return false;
    }
    focus() {
      this.focused = true;
      this.ownerDocument.activeElement = this;
    }
    scrollIntoView() { this.scrolledIntoView = true; }
    getBoundingClientRect() { return this._rect; }
    addEventListener(type, fn) {
      if (!this.listeners[type]) this.listeners[type] = [];
      this.listeners[type].push(fn);
    }
    removeEventListener(type, fn) {
      if (!this.listeners[type]) return;
      this.listeners[type] = this.listeners[type].filter(f => f !== fn);
    }
    dispatchEvent(event) {
      const ev = Object.assign({
        target: this,
        currentTarget: this,
        defaultPrevented: false,
        preventDefault() { this.defaultPrevented = true; },
        stopPropagation() { this._stopped = true; }
      }, event);
      let node = this;
      while (node) {
        ev.currentTarget = node;
        const list = (node.listeners[ev.type] || []).slice();
        for (const fn of list) fn.call(node, ev);
        if (!ev.bubbles || ev._stopped) break;
        node = node.parentNode;
      }
      return !ev.defaultPrevented;
    }
    click() {
      if (this.disabled) return;
      this.dispatchEvent({ type: "click", bubbles: true });
    }
    matches(selector) {
      const parts = selector.trim().split(/\s+/);
      if (parts.length > 1) {
        const last = parts[parts.length - 1];
        if (!this._matchesSimple(last)) return false;
        let cur = this.parentNode;
        for (let i = parts.length - 2; i >= 0; i--) {
          let found = false;
          while (cur) {
            if (cur._matchesSimple(parts[i])) { found = true; cur = cur.parentNode; break; }
            cur = cur.parentNode;
          }
          if (!found) return false;
        }
        return true;
      }
      return this._matchesSimple(selector.trim());
    }
    _matchesSimple(sel) {
      let rest = sel;
      const tagMatch = /^[a-z0-9-]+/i.exec(rest);
      if (tagMatch) {
        if (this.tagName !== tagMatch[0].toUpperCase()) return false;
        rest = rest.slice(tagMatch[0].length);
      }
      const attrRegex = /\[([a-z0-9_-]+)(?:="([^"]*)")?\]/gi;
      let m;
      while ((m = attrRegex.exec(rest))) {
        const val = this.getAttribute(m[1]);
        if (val === null) return false;
        if (m[2] !== undefined && val !== m[2]) return false;
      }
      rest = rest.replace(attrRegex, "");
      const classRegex = /\.([a-z0-9_-]+)/gi;
      while ((m = classRegex.exec(rest))) {
        if (!this.classList.contains(m[1])) return false;
      }
      return true;
    }
    closest(selector) {
      let cur = this;
      while (cur) {
        if (cur.matches && cur.matches(selector)) return cur;
        cur = cur.parentNode;
      }
      return null;
    }
    querySelectorAll(selector) {
      const out = [];
      const walk = (node) => {
        for (const c of node.children) {
          if (c.matches(selector)) out.push(c);
          walk(c);
        }
      };
      walk(this);
      return out;
    }
    querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
  }

  const doc = {
    body: null,
    activeElement: null,
    listeners: Object.create(null),
    createElement(tag) { return new FakeElement(tag, doc); },
    addEventListener(type, fn) {
      if (!this.listeners[type]) this.listeners[type] = [];
      this.listeners[type].push(fn);
    },
    removeEventListener(type, fn) {
      if (!this.listeners[type]) return;
      this.listeners[type] = this.listeners[type].filter(f => f !== fn);
    },
    dispatchEvent(event) {
      const list = (this.listeners[event.type] || []).slice();
      for (const fn of list) fn(event);
    }
  };
  doc.body = new FakeElement("body", doc);
  return { doc, FakeElement };
}

function mountVoiceSection(opts = {}) {
  const { doc } = createFakeDOM();
  const en = readLocale("en");
  const nativeSubscribers = [];
  let nativeSnap = Object.assign({
    desktop: true,
    available: true,
    on_device: true,
    locale: "en-US",
    reason: "",
    ready: true
  }, opts.native || {});

  const win = {
    document: doc,
    wsT(key, ...args) {
      let str = en[key] || key;
      for (const arg of args) str = str.replace("%s", String(arg)).replace("%d", String(arg));
      return str;
    },
    WsVoiceNative: {
      getStatus: () => Object.assign({}, nativeSnap),
      subscribe(fn) {
        nativeSubscribers.push(fn);
        return () => {
          const idx = nativeSubscribers.indexOf(fn);
          if (idx >= 0) nativeSubscribers.splice(idx, 1);
        };
      }
    }
  };
  const ctx = vm.createContext({ window: win, document: doc });
  vm.runInContext(readAsset("dashboard-settings-voice.js"), ctx);

  const status = opts.status || {
    openrouter: { configured: false, masked: "" },
    voice_keys: [
      { provider: "openrouter_voice", configured: false, masked: "" },
      { provider: "openai_api", configured: false, masked: "" },
      { provider: "groq", configured: false, masked: "" }
    ]
  };
  let endpoints = opts.endpoints || {};
  const configWrites = [];
  const accountCalls = [];
  let changeCount = 0;

  function el(tag, className, text) {
    const node = doc.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }
  function button(className, label, action) {
    const node = el("button", className, label);
    node.type = "button";
    if (action) node.addEventListener("click", action);
    return node;
  }
  function append(parent, ...items) { parent.append(...items); return parent; }
  function message(text, tone) { return el("p", "account-note" + (tone ? " " + tone : ""), text); }
  function password(name, placeholder) {
    const inp = el("input");
    inp.type = "password";
    inp.name = name;
    inp.placeholder = placeholder || "";
    return inp;
  }
  function field(label, control, hint) {
    const f = el("label", "account-field");
    f.append(el("span", "", label), control);
    if (hint) f.append(hint);
    return f;
  }
  function hold(_icon, label, action) {
    const b = button("reset-icon-btn", label, action);
    b.dataset.holdRemove = "1";
    return b;
  }

  const section = win.WsSettingsVoice.create({
    window: win,
    document: doc,
    el,
    button,
    append,
    hold,
    message,
    password,
    field,
    errorText: code => code || "error",
    getStatus: () => status,
    getEndpoints: () => endpoints,
    onChange: () => { changeCount++; },
    request: async (url, body) => {
      if (url === "/api/config/set") {
        configWrites.push(body);
        return { ok: true };
      }
      return { entries: opts.entries || [] };
    },
    accountRequest: async (kind, body) => {
      const copy = Object.assign({}, body);
      accountCalls.push({ kind, body: copy });
      if (kind === "key") {
        if (body.op === "set") {
          return { status: { provider: body.provider, configured: true, masked: body.key.slice(0, 6) + "…" } };
        }
        if (body.op === "remove") {
          return { status: { provider: body.provider, configured: false, masked: "" } };
        }
      }
      return {};
    }
  });

  doc.body.append(section.element);
  if (opts.entries) section.applyConfigSnapshot({ entries: opts.entries });

  return {
    win,
    doc,
    section,
    status,
    configWrites,
    accountCalls,
    getChangeCount: () => changeCount,
    setNative(next) {
      nativeSnap = Object.assign({}, nativeSnap, next);
      nativeSubscribers.forEach(fn => fn(Object.assign({}, nativeSnap)));
    },
    setEndpoints(next) { endpoints = next; section.render(); }
  };
}

test("computeEffectiveVoiceConfig honors stored order, legacy voice_backend, and T0 shared-key migration", () => {
  const win = {};
  vm.runInNewContext(readAsset("dashboard-settings-voice.js"), { window: win });
  const { computeEffectiveVoiceConfig } = win.WsSettingsVoice;

  // Fresh install -> native first, shared OFF
  const fresh = computeEffectiveVoiceConfig([
    { key: "dashboard.voice_backend_order", value: "", layers: [] },
    { key: "dashboard.voice_backend", value: "", layers: [] },
    { key: "dashboard.voice_openrouter_shared_key", value: false, layers: [] }
  ]);
  assert.deepEqual(Array.from(fresh.order), ["native"]);
  assert.equal(fresh.sharedEffective, false);

  // Legacy voice_backend=openrouter without explicit toggle or order -> T0 shared migration ON
  const legacy = computeEffectiveVoiceConfig([
    { key: "dashboard.voice_backend_order", value: "", layers: [] },
    { key: "dashboard.voice_backend", value: "openrouter", layers: [{ scope: "global", present: true, value: "openrouter" }] },
    { key: "dashboard.voice_openrouter_shared_key", value: false, layers: [] }
  ]);
  assert.deepEqual(Array.from(legacy.order), ["openrouter"]);
  assert.equal(legacy.sharedEffective, true);

  // Explicit global false override on shared toggle never falls through to legacy
  const explicitOff = computeEffectiveVoiceConfig([
    { key: "dashboard.voice_backend_order", value: "", layers: [] },
    { key: "dashboard.voice_backend", value: "openrouter", layers: [{ scope: "global", present: true, value: "openrouter" }] },
    { key: "dashboard.voice_openrouter_shared_key", value: false, layers: [{ scope: "global", present: true, value: false }] }
  ]);
  assert.equal(explicitOff.sharedExplicit, true);
  assert.equal(explicitOff.sharedEffective, false);

  // Stored order deduplicates and wins over legacy backend
  const stored = computeEffectiveVoiceConfig([
    { key: "dashboard.voice_backend_order", value: "groq native groq openrouter", layers: [{ scope: "global", present: true }] },
    { key: "dashboard.voice_backend", value: "openrouter", layers: [] },
    { key: "dashboard.voice_openrouter_shared_key", value: true, layers: [{ scope: "global", present: true }] }
  ]);
  assert.deepEqual(Array.from(stored.order), ["groq", "native", "openrouter"]);
  assert.equal(stored.sharedEffective, true);
});

test("Voice Providers section renders rows, dims native in browser, and surfaces desktop unavailable reasons", () => {
  const { section, setNative } = mountVoiceSection({
    native: { desktop: false, available: false, reason: "desktop_only" },
    status: {
      openrouter: { configured: false, masked: "" },
      voice_keys: [
        { provider: "openrouter_voice", configured: false, masked: "" },
        { provider: "openai_api", configured: false, masked: "" },
        { provider: "groq", configured: true, masked: "gsk_12…" }
      ]
    },
    entries: [
      { key: "dashboard.voice_backend_order", value: "native groq", layers: [{ scope: "global", present: true }] },
      { key: "dashboard.voice_openrouter_shared_key", value: false, layers: [] }
    ]
  });

  const rows = section.element.querySelectorAll(".vprow");
  assert.deepEqual(rows.map(r => r.dataset.voiceProvider), ["native", "openrouter", "openai_api", "groq", "local"]);

  // Browser-only: native row dimmed + Desktop only pill; Powerline falls through to groq
  const nativeRow = section.element.querySelector('[data-voice-provider="native"]');
  assert.equal(nativeRow.classList.contains("is-dimmed"), true);
  assert.match(nativeRow.textContent, /Desktop only/);

  const segs = section.element.querySelectorAll(".vpl-seg");
  assert.deepEqual(segs.map(s => s.dataset.voiceId), ["native", "groq"]);
  assert.equal(segs[0].classList.contains("is-unavailable"), true);
  assert.equal(segs[1].classList.contains("is-active"), true);

  // Desktop unavailable reason: permission_denied -> not dimmed, surfaces permission hint
  setNative({ desktop: true, available: false, reason: "permission_denied" });
  const nativeRowAfter = section.element.querySelector('[data-voice-provider="native"]');
  assert.equal(nativeRowAfter.classList.contains("is-dimmed"), false);
  assert.match(nativeRowAfter.textContent, /OS Native STT \(Desktop\)/);
  assert.match(nativeRowAfter.textContent, /Permission required/);

  // Desktop unavailable reason: privacy_disabled -> surfaces Windows privacy hint
  setNative({ desktop: true, available: false, reason: "privacy_disabled" });
  assert.match(section.element.querySelector('[data-voice-provider="native"]').textContent, /Speech privacy disabled/);

  // Desktop unavailable reason: offline_pack_missing -> surfaces language pack hint
  setNative({ desktop: true, available: false, reason: "offline_pack_missing" });
  assert.match(section.element.querySelector('[data-voice-provider="native"]').textContent, /Language pack missing/);

  // Desktop available -> native becomes active, groq becomes standby
  setNative({ desktop: true, available: true, on_device: true, reason: "" });
  const segsReady = section.element.querySelectorAll(".vpl-seg");
  assert.equal(segsReady[0].classList.contains("is-active"), true);
  assert.equal(segsReady[1].classList.contains("is-standby"), true);
});

test("OpenRouter (Voice) modal shows shared toggle only when top key exists, disables dedicated input when ON, and materializes flag on save", async () => {
  // Case A: Top OpenRouter key configured -> toggle visible
  const m1 = mountVoiceSection({
    status: {
      openrouter: { configured: true, masked: "sk-or-top…" },
      voice_keys: [
        { provider: "openrouter_voice", configured: false, masked: "" },
        { provider: "openai_api", configured: false, masked: "" },
        { provider: "groq", configured: false, masked: "" }
      ]
    },
    entries: [
      { key: "dashboard.voice_backend_order", value: "native", layers: [] },
      { key: "dashboard.voice_openrouter_shared_key", value: false, layers: [] }
    ]
  });

  // Open modal via Configure button
  const orBtn = m1.section.element.querySelector('[data-voice-provider="openrouter"] button');
  orBtn.click();
  let sw = m1.doc.body.querySelector("[data-voice-or-shared]");
  let inp = m1.doc.body.querySelector("[data-voice-or-key-input]");
  assert.ok(sw, "shared toggle must be shown when the Werk OpenRouter key is configured");
  assert.equal(sw.getAttribute("aria-checked"), "false");
  assert.equal(inp.disabled, false);

  // Toggle ON -> dedicated key input disabled
  sw.click();
  sw = m1.doc.body.querySelector("[data-voice-or-shared]");
  inp = m1.doc.body.querySelector("[data-voice-or-key-input]");
  assert.equal(sw.getAttribute("aria-checked"), "true");
  assert.equal(inp.disabled, true);

  // Save -> materializes dashboard.voice_openrouter_shared_key=true AND appends openrouter to order
  m1.doc.body.querySelector("[data-voice-or-save]").click();
  await new Promise(r => setImmediate(r));
  assert.equal(m1.configWrites[0].key, "dashboard.voice_openrouter_shared_key");
  assert.equal(m1.configWrites[0].value, true);
  assert.equal(m1.configWrites[1].key, "dashboard.voice_backend_order");
  assert.equal(m1.configWrites[1].value, "native openrouter");
  assert.equal(m1.accountCalls.length, 0, "no dedicated key API call when using shared key");
  assert.match(m1.section.element.querySelector('[data-voice-provider="openrouter"]').textContent, /Reusing Werk key/);

  // Case B: No Werk OpenRouter key -> toggle hidden, dedicated key input enabled
  const m2 = mountVoiceSection({
    status: {
      openrouter: { configured: false, masked: "" },
      voice_keys: [
        { provider: "openrouter_voice", configured: false, masked: "" },
        { provider: "openai_api", configured: false, masked: "" },
        { provider: "groq", configured: false, masked: "" }
      ]
    },
    entries: [
      { key: "dashboard.voice_backend_order", value: "native", layers: [] },
      { key: "dashboard.voice_openrouter_shared_key", value: false, layers: [] }
    ]
  });

  m2.section.element.querySelector('[data-voice-provider="openrouter"] button').click();
  assert.equal(m2.doc.body.querySelector("[data-voice-or-shared]"), null, "shared toggle must NOT be shown without top key");
  const inp2 = m2.doc.body.querySelector("[data-voice-or-key-input]");
  assert.equal(inp2.disabled, false);

  // Invalid prefix blocked without network calls
  inp2.value = "sk-wrong-123";
  inp2.dispatchEvent({ type: "input" });
  m2.doc.body.querySelector("[data-voice-or-save]").click();
  assert.equal(m2.configWrites.length, 0);
  assert.ok(m2.doc.body.querySelector(".vmodal-err"));

  // Valid sk-or- dedicated key -> materializes shared=false, stores openrouter_voice, appends openrouter to order
  const inp2b = m2.doc.body.querySelector("[data-voice-or-key-input]");
  inp2b.value = "sk-or-v1-dedicated-secret";
  inp2b.dispatchEvent({ type: "input" });
  m2.doc.body.querySelector("[data-voice-or-save]").click();
  await new Promise(r => setImmediate(r));
  assert.equal(m2.configWrites[0].key, "dashboard.voice_openrouter_shared_key");
  assert.equal(m2.configWrites[0].value, false);
  assert.equal(m2.accountCalls[0].kind, "key");
  assert.equal(m2.accountCalls[0].body.provider, "openrouter_voice");
  assert.equal(m2.accountCalls[0].body.op, "set");
  assert.equal(m2.configWrites[1].key, "dashboard.voice_backend_order");
  assert.equal(m2.configWrites[1].value, "native openrouter");
});

test("Powerline priority bar supports keyboard ArrowLeft/ArrowRight and drag-and-drop reordering with persistence", async () => {
  const { section, configWrites } = mountVoiceSection({
    native: { desktop: true, available: true, on_device: true, reason: "" },
    status: {
      openrouter: { configured: true, masked: "sk-or-…" },
      voice_keys: [
        { provider: "openrouter_voice", configured: true, masked: "sk-or-v…" },
        { provider: "openai_api", configured: false, masked: "" },
        { provider: "groq", configured: true, masked: "gsk_1…" }
      ]
    },
    entries: [
      { key: "dashboard.voice_backend_order", value: "native openrouter groq", layers: [{ scope: "global", present: true }] },
      { key: "dashboard.voice_openrouter_shared_key", value: false, layers: [{ scope: "global", present: true }] }
    ]
  });

  // Keyboard ArrowRight on first segment (native -> index 1)
  const pl = section.element.querySelector(".vpl");
  const firstSeg = pl.querySelector('[data-voice-id="native"]');
  firstSeg.dispatchEvent({ type: "keydown", key: "ArrowRight", bubbles: true });
  await new Promise(r => setImmediate(r));
  assert.deepEqual(pl.querySelectorAll(".vpl-seg").map(s => s.dataset.voiceId), ["openrouter", "native", "groq"]);
  assert.equal(configWrites.at(-1).key, "dashboard.voice_backend_order");
  assert.equal(configWrites.at(-1).value, "openrouter native groq");
  assert.equal(pl.querySelector('[data-voice-id="native"]').focused, true, "focus stays on moved segment");

  // Keyboard ArrowLeft on groq (index 2 -> index 1)
  pl.querySelector('[data-voice-id="groq"]').dispatchEvent({ type: "keydown", key: "ArrowLeft", bubbles: true });
  await new Promise(r => setImmediate(r));
  assert.deepEqual(pl.querySelectorAll(".vpl-seg").map(s => s.dataset.voiceId), ["openrouter", "groq", "native"]);
  assert.equal(configWrites.at(-1).value, "openrouter groq native");

  // Drag-and-drop: drag "native" (index 2) to the left of segment 0 (clientX = 10)
  const segs = pl.querySelectorAll(".vpl-seg");
  segs[0]._rect = { left: 0, right: 140, width: 140, top: 0, bottom: 52, height: 52 };
  segs[1]._rect = { left: 140, right: 280, width: 140, top: 0, bottom: 52, height: 52 };
  segs[2]._rect = { left: 280, right: 420, width: 140, top: 0, bottom: 52, height: 52 };
  pl._rect = { left: 0, right: 420, width: 420, top: 0, bottom: 52, height: 52 };

  segs[2].dispatchEvent({ type: "dragstart", bubbles: true, dataTransfer: { setData() {} } });
  pl.dispatchEvent({ type: "dragover", bubbles: true, clientX: 10, dataTransfer: {} });
  pl.dispatchEvent({ type: "drop", bubbles: true });
  await new Promise(r => setImmediate(r));
  assert.deepEqual(pl.querySelectorAll(".vpl-seg").map(s => s.dataset.voiceId), ["native", "openrouter", "groq"]);
  assert.equal(configWrites.at(-1).value, "native openrouter groq");
});

test("saving a dedicated OpenAI or Groq voice key appends the provider to dashboard.voice_backend_order", async () => {
  const { section, configWrites, accountCalls } = mountVoiceSection({
    entries: [
      { key: "dashboard.voice_backend_order", value: "native", layers: [{ scope: "global", present: true }] },
      { key: "dashboard.voice_openrouter_shared_key", value: false, layers: [] }
    ]
  });

  const groqRow = section.element.querySelector('[data-voice-provider="groq"]');
  groqRow.querySelector("button").click();
  const form = groqRow.querySelector("form");
  const input = form.querySelector("input");
  input.value = "gsk_test_key_999";
  form.dispatchEvent({ type: "submit" });
  await new Promise(r => setImmediate(r));

  assert.equal(accountCalls[0].kind, "key");
  assert.equal(accountCalls[0].body.provider, "groq");
  assert.equal(accountCalls[0].body.op, "set");
  assert.equal(configWrites[0].key, "dashboard.voice_backend_order");
  assert.equal(configWrites[0].value, "native groq");
});

test("section.focus('voice') focuses and scrolls the Voice Providers section", () => {
  const { section } = mountVoiceSection();
  assert.equal(section.focus("voice"), true);
  assert.equal(section.element.focused, true);
  assert.equal(section.element.scrolledIntoView, true);
  assert.equal(section.element.classList.contains("vsec-focus"), true);

  assert.equal(section.focus("openrouter_voice"), true);
  const orRow = section.element.querySelector('[data-voice-provider="openrouter"]');
  assert.equal(orRow.focused, true);
  assert.equal(orRow.scrolledIntoView, true);
  assert.equal(section.focus("unknown-provider"), false);
});

test("Voice Providers CSS has zero :has() selectors, uses tonal tokens, and account file shrank", () => {
  const css = readAsset("dashboard-settings-voice.css");
  assert.doesNotMatch(css, /:has\(/, "dashboard-settings-voice.css must not use :has()");
  assert.match(css, /var\(--wb-primary\)/);
  assert.match(css, /prefers-reduced-motion/);

  const accountLines = readAsset("dashboard-settings-account.js").split("\n").length;
  assert.ok(accountLines < 1446, `expected dashboard-settings-account.js (${accountLines}) < 1446 baseline`);
});

test("all js.settings.voice.* i18n keys exist and are non-empty across all 11 locales", () => {
  const locales = ["en", "ko", "ja", "zh-Hans", "zh-Hant", "de", "es", "fr", "it", "pt-BR", "ru"];
  const en = readLocale("en");
  const voiceKeys = Object.keys(en).filter(k => k.startsWith("js.settings.voice."));
  assert.ok(voiceKeys.length >= 60, `expected >= 60 voice settings i18n keys, got ${voiceKeys.length}`);
  for (const locale of locales) {
    const table = readLocale(locale);
    for (const key of voiceKeys) {
      assert.equal(typeof table[key], "string", `${locale} missing ${key}`);
      assert.notEqual(table[key].trim(), "", `${locale} empty ${key}`);
    }
  }
});
