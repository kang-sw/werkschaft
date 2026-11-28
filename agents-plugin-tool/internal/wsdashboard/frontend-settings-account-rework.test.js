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

  function matchesSimple(el, sel) {
    const s = sel.trim();
    if (!s) return false;
    let rest = s;
    const tagM = rest.match(/^[a-zA-Z0-9_-]+/);
    if (tagM) {
      if (el.tagName !== tagM[0].toUpperCase()) return false;
      rest = rest.slice(tagM[0].length);
    }
    const idM = rest.match(/#([a-zA-Z0-9_-]+)/);
    if (idM) {
      if (el.id !== idM[1]) return false;
      rest = rest.replace(idM[0], "");
    }
    for (const m of rest.matchAll(/\.([a-zA-Z0-9_-]+)/g)) {
      if (!el.classList.contains(m[1])) return false;
    }
    rest = rest.replace(/\.[a-zA-Z0-9_-]+/g, "");
    for (const m of rest.matchAll(/\[([^=\]]+)(?:=["']?([^"'\]]*)["']?)?\]/g)) {
      const attr = m[1];
      const expected = m[2];
      const actual = el.getAttribute(attr);
      if (expected === undefined) {
        if (actual === null) return false;
      } else if (actual !== expected) {
        return false;
      }
    }
    return true;
  }

  function matchesSelector(el, selector) {
    return selector.split(",").some(part => {
      const tokens = part.trim().split(/\s+/).filter(Boolean);
      if (!tokens.length) return false;
      if (!matchesSimple(el, tokens[tokens.length - 1])) return false;
      let cur = el.parentNode;
      for (let i = tokens.length - 2; i >= 0; i--) {
        while (cur && cur.nodeType === 1 && !matchesSimple(cur, tokens[i])) cur = cur.parentNode;
        if (!cur || cur.nodeType !== 1) return false;
        cur = cur.parentNode;
      }
      return true;
    });
  }

  class FakeElement {
    constructor(tag, doc) {
      this.tagName = String(tag || "div").toUpperCase();
      this.nodeType = 1;
      this.ownerDocument = doc;
      this.parentNode = null;
      this.childNodes = [];
      this.attributes = Object.create(null);
      this.style = {
        _props: Object.create(null),
        setProperty(k, v) { this._props[k] = String(v); },
        removeProperty(k) { delete this._props[k]; }
      };
      this.classList = new FakeClassList(this);
      this._className = "";
      this._textContent = "";
      this.listeners = Object.create(null);
      this.hidden = false;
      this.disabled = false;
      this.readOnly = false;
      this.required = false;
      this.value = "";
      this.type = "";
      this.placeholder = "";
      this.id = "";
      this.title = "";
      this.tabIndex = 0;
      this.maxLength = -1;
      this.focused = false;
      this.scrolledIntoView = false;
      this._rect = { left: 0, top: 0, right: 120, bottom: 32, width: 120, height: 32 };
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
    after(...items) {
      if (!this.parentNode) return;
      const siblings = this.parentNode.childNodes;
      let idx = siblings.indexOf(this) + 1;
      for (const item of items) {
        if (item == null) continue;
        if (typeof item === "object") {
          if (item.parentNode) item.remove();
          item.parentNode = this.parentNode;
        }
        siblings.splice(idx++, 0, item);
      }
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
    closest(sel) {
      let cur = this;
      while (cur && cur.nodeType === 1) {
        if (matchesSelector(cur, sel)) return cur;
        cur = cur.parentNode;
      }
      return null;
    }
    querySelectorAll(sel) {
      const out = [];
      const walk = node => {
        for (const child of node.children) {
          if (matchesSelector(child, sel)) out.push(child);
          walk(child);
        }
      };
      walk(this);
      return out;
    }
    querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
    focus() {
      this.focused = true;
      this.ownerDocument.activeElement = this;
    }
    select() {}
    scrollIntoView() { this.scrolledIntoView = true; }
    getBoundingClientRect() { return this._rect; }
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
    removeEventListener(type, fn) { this.listeners[type] = (this.listeners[type] || []).filter(f => f !== fn); }
    dispatchEvent(ev) {
      const event = Object.assign({
        target: this,
        currentTarget: this,
        defaultPrevented: false,
        preventDefault() { this.defaultPrevented = true; },
        stopPropagation() {},
        stopImmediatePropagation() {}
      }, ev);
      for (const fn of (this.listeners[event.type] || []).slice()) fn(event);
      return !event.defaultPrevented;
    }
    click() {
      if (this.disabled) return;
      this.dispatchEvent({ type: "click" });
    }
  }

  const docListeners = Object.create(null);
  const doc = {
    hidden: false,
    activeElement: null,
    documentElement: null,
    body: null,
    createElement(tag) { return new FakeElement(tag, doc); },
    createElementNS(_ns, tag) { return new FakeElement(tag, doc); },
    addEventListener(type, fn) { (docListeners[type] ||= []).push(fn); },
    removeEventListener(type, fn) { docListeners[type] = (docListeners[type] || []).filter(f => f !== fn); },
    dispatchEvent(ev) {
      const event = Object.assign({
        preventDefault() {},
        stopPropagation() {},
        stopImmediatePropagation() {}
      }, ev);
      for (const fn of (docListeners[event.type] || []).slice()) fn(event);
    }
  };
  doc.documentElement = new FakeElement("html", doc);
  doc.documentElement.lang = "en";
  doc.body = new FakeElement("body", doc);
  doc.documentElement.append(doc.body);
  return { doc, FakeElement };
}

function makeAccountEnv(opts = {}) {
  const { doc } = createFakeDOM();
  const en = readLocale("en");
  const winListeners = Object.create(null);
  const timers = [];
  let nextTimerId = 1;

  const setTimeoutStub = (fn, ms) => {
    const id = nextTimerId++;
    timers.push({ id, fn, ms, cleared: false });
    return id;
  };
  const clearTimeoutStub = (id) => {
    const t = timers.find(item => item.id === id);
    if (t) t.cleared = true;
  };

  const win = {
    document: doc,
    setTimeout: opts.fakeTimers ? setTimeoutStub : setTimeout,
    clearTimeout: opts.fakeTimers ? clearTimeoutStub : clearTimeout,
    wsT(key, ...args) {
      let raw = en[key] || key;
      args.forEach((arg, idx) => { raw = raw.replaceAll("{" + idx + "}", String(arg)); });
      return raw;
    },
    addEventListener(type, fn) { (winListeners[type] ||= []).push(fn); },
    removeEventListener(type, fn) { winListeners[type] = (winListeners[type] || []).filter(f => f !== fn); },
    dispatchEvent(ev) {
      const event = Object.assign({
        preventDefault() {},
        stopPropagation() {},
        stopImmediatePropagation() {}
      }, ev);
      for (const fn of (winListeners[event.type] || []).slice()) fn(event);
    }
  };

  const fetchCalls = [];
  const fetchImpl = async (url, options = {}) => {
    const body = options.body ? JSON.parse(options.body) : undefined;
    fetchCalls.push({ url, method: options.method || "GET", body });
    if (typeof opts.onFetch === "function") return opts.onFetch(url, options, body);
    return { ok: true, status: 200, json: async () => ({}) };
  };

  const ctx = vm.createContext({
    window: win,
    document: doc,
    fetch: fetchImpl,
    URL,
    Date,
    Uint8Array,
    atob: globalThis.atob,
    location: { protocol: "http:", host: "127.0.0.1:4123" },
    setTimeout: win.setTimeout,
    clearTimeout: win.clearTimeout,
    navigator: { clipboard: { writeText: async () => {} } }
  });
  vm.runInContext(readAsset("hold-confirm.js"), ctx);
  vm.runInContext(readAsset("dashboard-settings-account-shared.js"), ctx);
  vm.runInContext(readAsset("dashboard-settings-account-harness.js"), ctx);
  vm.runInContext(readAsset("dashboard-settings-account-runtime.js"), ctx);
  vm.runInContext(readAsset("dashboard-settings-account-pirpc.js"), ctx);
  vm.runInContext(readAsset("dashboard-settings-account.js"), ctx);
  vm.runInContext(readAsset("dashboard-settings-voice.js"), ctx);

  function el(tag, cls, text) {
    const node = doc.createElement(tag);
    if (cls) node.className = cls;
    if (text != null && text !== "") node.textContent = text;
    return node;
  }
  function button(cls, label, onClick) {
    const b = el("button", cls, label);
    b.type = "button";
    if (onClick) b.addEventListener("click", onClick);
    return b;
  }
  function append(parent, ...items) { parent.append(...items); return parent; }
  function svgIcon(name) { const s = el("span", "svg-icon"); s.dataset.icon = name; return s; }

  return { doc, win, el, button, append, svgIcon, fetchCalls, timers };
}

test("split account and voice modules stay well under 1,000 lines and pages.go loads them in dependency order", () => {
  const files = [
    "dashboard-settings-account-shared.js",
    "dashboard-settings-account-harness.js",
    "dashboard-settings-account-runtime.js",
    "dashboard-settings-account-pirpc.js",
    "dashboard-settings-account.js",
    "dashboard-settings-voice.js"
  ];
  for (const name of files) {
    const lines = readAsset(name).split("\n").length;
    assert.ok(lines < 1000, `${name} has ${lines} lines; must remain below 1,000 lines`);
  }
  const pagesGo = fs.readFileSync(path.join(__dirname, "pages.go"), "utf8");
  const sharedIdx = pagesGo.indexOf("dashboard-settings-account-shared.js");
  const harnessIdx = pagesGo.indexOf("dashboard-settings-account-harness.js");
  const runtimeIdx = pagesGo.indexOf("dashboard-settings-account-runtime.js");
  const pirpcIdx = pagesGo.indexOf("dashboard-settings-account-pirpc.js");
  const voiceIdx = pagesGo.indexOf("dashboard-settings-voice.js");
  const accountIdx = pagesGo.indexOf("dashboard-settings-account.js");
  const settingsIdx = pagesGo.indexOf("dashboard-settings.js");
  assert.ok(sharedIdx > 0 && harnessIdx > sharedIdx && runtimeIdx > harnessIdx && pirpcIdx > runtimeIdx && accountIdx > pirpcIdx && settingsIdx > accountIdx,
    "pages.go must load account shared -> harness -> runtime -> pirpc -> coordinator before dashboard-settings.js");
  assert.ok(voiceIdx > 0 && voiceIdx < settingsIdx, "pages.go must load dashboard-settings-voice.js before dashboard-settings.js");
});

test("dashboard-settings.js registers voice section, groups account+voice under account_auth, hides voice without module, and normalizes legacy ?section=account&provider=voice", () => {
  const src = readAsset("dashboard-settings.js");
  assert.match(src, /var sections = \[[^\]]*"account",\s*"voice"/);
  assert.match(src, /voice:\s*"js\.settings\.section\.voice"/);
  assert.match(src, /voice:\s*"js\.settings\.section_note\.voice"/);
  assert.match(src, /if \(name === "voice"\) return !!voice;/);
  assert.match(src, /tr\("js\.settings\.nav\.account_auth"\)/);

  // Extract startup query parsing normalization logic contract
  assert.match(src, /if \(requestedSection === "account" && requestedProvider === "voice"\)\s*\{\s*requestedSection = "voice";\s*requestedProvider = "";\s*\}/);
  // Extract selectSection normalization contract
  assert.match(src, /if \(name === "account" && provider === "voice"\)\s*\{\s*name = "voice";\s*provider = "";\s*\}/);
});

test("Account panel renders 2 purpose groups, inline slot rename validation + API call, disabled OpenAI consent when signed out, OpenRouter voice chip, and SSE rename preservation", async () => {
  let renameResponse = null;
  let statusPayload = {
    loopback_peer: true,
    openrouter: { configured: true, masked: "...9f3c", verification: { state: "verified" } },
    openai: null,
    openai_ready: false,
    sign_in: { state: "idle" },
    consent_prompt: 0,
    local_keys: [],
    slots: {
      claude: [
        { id: "default", label: "Default", kind: "default", active: true, signed_in: true, config_dir: "/home/u/.claude" },
        { id: "work-1", label: "Work", kind: "managed", active: false, signed_in: true, config_dir: "/home/u/.claude-work" }
      ],
      codex: [
        { id: "default", label: "Default", kind: "default", active: true, signed_in: true, config_dir: "/home/u/.codex" }
      ]
    },
    harness_auth: [
      { harness: "agy", label: "Antigravity", probed: true, auth_required: false }
    ],
    voice_keys: {}
  };

  let voiceOpened = 0;
  let voiceShared = true;
  const env = makeAccountEnv({
    onFetch: async (url, _opts, body) => {
      if (url === "/api/account/status") return { ok: true, status: 200, json: async () => JSON.parse(JSON.stringify(statusPayload)) };
      if (url === "/api/account/slots/rename") {
        if (renameResponse) return renameResponse;
        const slot = statusPayload.slots[body.harness].find(s => s.id === body.slot_id);
        slot.label = body.label;
        return { ok: true, status: 200, json: async () => Object.assign({}, slot) };
      }
      return { ok: true, status: 200, json: async () => ({}) };
    }
  });

  const controller = env.win.WsSettingsAccount.create({
    el: env.el,
    button: env.button,
    append: env.append,
    svgIcon: env.svgIcon,
    request: async () => ({ entries: [{ key: "agent.werk_endpoints", value: "{}" }] }),
    showTiers: () => {},
    onChange: () => {},
    endpointSpec: { label: "js.settings.key.agent.werk_endpoints.label", description: "js.settings.key.agent.werk_endpoints.description" },
    openVoice: () => { voiceOpened++; },
    isVoiceOpenRouterShared: () => voiceShared
  });
  env.doc.body.append(controller.panel);
  await controller.refreshStatus();

  // 1. Three purpose groups in order (pirpc hidden when status.pi_runtime is absent)
  const groups = controller.panel.querySelectorAll("[data-account-group]");
  assert.deepEqual(groups.map(g => g.dataset.accountGroup), ["harness", "runtime", "pirpc"]);
  assert.equal(groups[2].hidden, true, "pirpc group must stay hidden when status.pi_runtime is absent");

  // 2. OpenAI consent sub-setting lives inside OpenAI's .acc-kids stem and is visible & disabled when signed out
  const oaBlock = controller.panel.querySelector('[data-account-row="openai"]');
  const consentRow = oaBlock.querySelector('.acc-kids [data-account-row="openai-consent"]');
  assert.ok(consentRow, "OpenAI consent sub-setting must live inside OpenAI's Level-2 .acc-kids stem");
  const consentSwitch = consentRow.querySelector('[role="switch"]');
  assert.equal(consentSwitch.disabled, true);
  assert.equal(consentSwitch.getAttribute("aria-checked"), "false");

  // 3. OpenRouter voice shared chip appears and triggers openVoice()
  const chip = controller.panel.querySelector('[data-account-voice-chip]');
  assert.ok(chip, "OpenRouter row must render clickable Voice chip when shared key mode is active");
  chip.click();
  assert.equal(voiceOpened, 1);

  // When voice stops sharing OpenRouter, syncVoiceState hides the chip
  voiceShared = false;
  controller.syncVoiceState();
  assert.equal(controller.panel.querySelector('[data-account-voice-chip]'), null);

  // 4. Default slot is NOT renameable/removable; non-default slot supports inline rename
  const getClaudeBlock = () => controller.panel.querySelector('[data-account-row="slots-claude"]');
  const slotRows = getClaudeBlock().querySelectorAll("[data-slot-id]");
  assert.equal(slotRows.length, 2);
  const defaultBtns = slotRows[0].querySelectorAll(".acc-row-act button").map(b => b.textContent);
  assert.deepEqual(defaultBtns, [], "Default slot must have no rename/remove buttons");

  const renameBtn = slotRows[1].querySelectorAll(".acc-row-act button").find(b => b.textContent === "Rename");
  assert.ok(renameBtn, "Non-default slot must render Rename button");
  renameBtn.click();

  const renameInput = getClaudeBlock().querySelector("[data-slot-rename-input]");
  assert.ok(renameInput, "Inline rename input should open");
  assert.equal(renameInput.value, "Work");

  // SSE refresh while rename input is open preserves the editor and user's typed draft!
  renameInput.value = "Team Alpha";
  await controller.refreshStatus();
  const preservedInput = getClaudeBlock().querySelector("[data-slot-rename-input]");
  assert.ok(preservedInput, "Inline rename editor must survive background SSE refresh");
  assert.equal(preservedInput.value, "Team Alpha");

  // Validation: empty label rejected client-side without hitting API
  const beforeCalls = env.fetchCalls.length;
  preservedInput.value = "   ";
  preservedInput.parentNode.dispatchEvent({ type: "submit" });
  assert.equal(env.fetchCalls.length, beforeCalls);
  assert.equal(preservedInput.getAttribute("aria-invalid"), "true");

  // Validation: >80 runes rejected client-side without hitting API
  preservedInput.value = "가".repeat(81);
  preservedInput.parentNode.dispatchEvent({ type: "submit" });
  assert.equal(env.fetchCalls.length, beforeCalls);

  // Server error preserves editor and displays mapped error message
  renameResponse = { ok: false, status: 400, json: async () => ({ error: "default_slot_immutable" }) };
  preservedInput.value = "Team Beta";
  preservedInput.parentNode.dispatchEvent({ type: "submit" });
  await new Promise(r => setImmediate(r));
  assert.ok(getClaudeBlock().querySelector("[data-slot-rename-input]"), "Editor stays open on server error");
  assert.match(getClaudeBlock().querySelector(".acc-rename-err").textContent, /cannot be renamed/);

  // Successful rename posts POST /api/account/slots/rename and updates label
  renameResponse = null;
  preservedInput.value = "Team Gamma";
  preservedInput.parentNode.dispatchEvent({ type: "submit" });
  await new Promise(r => setImmediate(r));
  await new Promise(r => setImmediate(r));
  const renameCall = env.fetchCalls.filter(c => c.url === "/api/account/slots/rename").at(-1);
  assert.deepEqual(renameCall.body, { harness: "claude", slot_id: "work-1", label: "Team Gamma" });
  assert.equal(getClaudeBlock().querySelector("[data-slot-rename-input]"), null);
  assert.match(getClaudeBlock().textContent, /Team Gamma/);
  assert.equal(env.doc.activeElement, getClaudeBlock().querySelector('[data-slot-rename="work-1"]'), "Focus returns to Rename button after save");

  // Escape cancels open rename editor cleanly and restores focus
  const renameBtn2 = getClaudeBlock().querySelector('[data-slot-rename="work-1"]');
  renameBtn2.click();
  const inp2 = getClaudeBlock().querySelector("[data-slot-rename-input]");
  inp2.dispatchEvent({ type: "keydown", key: "Escape" });
  assert.equal(getClaudeBlock().querySelector("[data-slot-rename-input]"), null);
  assert.equal(env.doc.activeElement, getClaudeBlock().querySelector('[data-slot-rename="work-1"]'), "Focus returns to Rename button after Escape");

  // OpenAI consent becomes enabled when account is signed in and ready
  statusPayload.openai = { client_id: "cid-1", email: "user@example.test", signed_in: true, plan_enabled: true, background_consent: false };
  statusPayload.openai_ready = true;
  await controller.refreshStatus();
  assert.equal(consentSwitch.disabled, false);
  controller.destroy();
});

test("holdBar cancels on pointerleave, Escape, blur, and visibilitychange, and confirms only after full 1.5s hold", () => {
  const env = makeAccountEnv({ fakeTimers: true });
  const h = env.win.WsSettingsAccountShared.createHelpers({
    el: env.el,
    button: env.button,
    append: env.append,
    svgIcon: env.svgIcon,
    t: env.win.wsT
  });
  let confirmed = 0;
  const btn = h.holdBar("Remove", "Remove slot work", () => { confirmed++; });
  env.doc.body.append(btn);

  // Plain click does not trigger
  btn.click();
  assert.equal(confirmed, 0);

  // 1. Pointer leave cancels before timer completes
  btn.dispatchEvent({ type: "pointerdown", button: 0, pointerId: 1 });
  assert.equal(btn.getAttribute("data-hold-state"), "holding");
  btn.dispatchEvent({ type: "pointerleave" });
  assert.equal(btn.getAttribute("data-hold-state"), null);
  assert.equal(confirmed, 0);

  // 2. Escape cancels keyboard hold
  btn.dispatchEvent({ type: "keydown", key: " " });
  assert.equal(btn.getAttribute("data-hold-state"), "holding");
  env.win.dispatchEvent({ type: "keydown", key: "Escape" });
  assert.equal(btn.getAttribute("data-hold-state"), null);
  assert.equal(confirmed, 0);

  // 3. Window blur cancels hold
  btn.dispatchEvent({ type: "keydown", key: "Enter" });
  assert.equal(btn.getAttribute("data-hold-state"), "holding");
  env.win.dispatchEvent({ type: "blur" });
  assert.equal(btn.getAttribute("data-hold-state"), null);

  // 4. Document visibilitychange (hidden) cancels hold
  btn.dispatchEvent({ type: "pointerdown", button: 0, pointerId: 2 });
  env.doc.hidden = true;
  env.doc.dispatchEvent({ type: "visibilitychange" });
  assert.equal(btn.getAttribute("data-hold-state"), null);
  env.doc.hidden = false;

  // 5. Complete keyboard Space hold for 1500ms confirms on keyup
  btn.dispatchEvent({ type: "keydown", key: " " });
  const activeTimer = env.timers.filter(t => !t.cleared).at(-1);
  assert.equal(activeTimer.ms, 1500);
  activeTimer.fn();
  assert.equal(btn.getAttribute("data-hold-state"), "ready");
  env.win.dispatchEvent({ type: "keyup", key: " " });
  assert.equal(confirmed, 1);
});

test("WsSettingsVoice mounts independently for Settings -> Voice and renders Manage shared key in Account back-link", async () => {
  const env = makeAccountEnv();
  let openedAccount = 0;
  const voice = env.win.WsSettingsVoice.create({
    el: env.el,
    button: env.button,
    append: env.append,
    request: async () => ({ entries: [] }),
    accountRequest: async () => ({}),
    hold: () => {},
    message: (text) => env.el("p", "", text),
    password: () => env.el("input", "inp"),
    field: (label, control) => env.append(env.el("div", ""), env.el("label", "", label), control),
    errorText: (c) => c || "err",
    getStatus: () => ({ openrouter: { configured: true, masked: "...9f3c" }, voice_keys: {} }),
    getEndpoints: () => ({}),
    onOpenAccount: () => { openedAccount++; },
    window: env.win,
    document: env.doc
  });
  env.doc.body.append(voice.element);
  voice.applyConfigSnapshot({
    entries: [
      { key: "dashboard.voice_backend_order", value: "native openrouter" },
      { key: "dashboard.voice_openrouter_shared_key", value: true, layers: [{ scope: "global", present: true }] }
    ]
  });
  assert.equal(voice.isSharedOpenRouterKey(), true);
  const backLink = voice.element.querySelector(".vprow-chip-link");
  assert.ok(backLink, "Voice section must render back-link to Account for managing the shared OpenRouter key");
  backLink.click();
  assert.equal(openedAccount, 1);
  voice.destroy();
});

test("dashboard-settings-account.css uses borderless tonal surfaces, 2px indentation stem, responsive <760px rules, and zero :has()", () => {
  const css = readAsset("dashboard-settings-account.css");
  assert.doesNotMatch(css, /:has\(/, "zero :has() selectors");
  assert.match(css, /\.acc-grp\b/);
  assert.match(css, /\.acc-kids\b/);
  assert.match(css, /--acc-stem/);
  assert.match(css, /@media \(max-width:\s*760px\)/);
  assert.match(css, /\.acc-pi-prog-bar\b/);
  assert.match(css, /transform:\s*scaleX\(var\(--p,\s*0\)\)/);
  assert.match(css, /\.acc-pty-backdrop\b/);
});

test("Managed Pi (pi-rpc) group filters pi-rpc out of Group 1, handles runtime install/progress/uninstall, slot CRUD, choices invalidation, and ephemeral PTY login modal lifecycle", async () => {
  let statusPayload = {
    loopback_peer: true,
    openrouter: { configured: false },
    openai: null,
    openai_ready: false,
    sign_in: { state: "idle" },
    consent_prompt: 0,
    local_keys: [],
    pi_runtime: { installed: false, version: "1.5.2", installing: false, error: "" },
    pi_login: { state: "idle" },
    slots: {
      claude: [{ id: "default", label: "Default", active: true }],
      "pi-rpc": [
        { id: "default", label: "Default", active: true, config_dir: "/home/u/.pi/agent" },
        { id: "work-pi", label: "Work Pi", active: false, managed: true, config_dir: "/tmp/slots/pi-rpc/work-pi" }
      ]
    },
    harness_auth: [],
    voice_keys: {}
  };

  let completeCalls = 0;
  let cancelCalls = 0;
  let invalidated = 0;
  const env = makeAccountEnv({
    fakeTimers: true,
    onFetch: async (url, _opts, body) => {
      if (url === "/api/account/status") {
        return { ok: true, status: 200, json: async () => JSON.parse(JSON.stringify(statusPayload)) };
      }
      if (url === "/api/account/pi/runtime/install") {
        statusPayload.pi_runtime = {
          installed: false, version: "1.5.2", installing: true,
          progress: { phase: "download", downloaded: 10485760, total: 41943040 }, error: ""
        };
        return { ok: true, status: 202, json: async () => JSON.parse(JSON.stringify(statusPayload.pi_runtime)) };
      }
      if (url === "/api/account/pi/runtime/uninstall") {
        statusPayload.pi_runtime = { installed: false, version: "1.5.2", installing: false, error: "" };
        return { ok: true, status: 200, json: async () => JSON.parse(JSON.stringify(statusPayload.pi_runtime)) };
      }
      if (url === "/api/account/slots/switch") {
        statusPayload.slots[body.harness].forEach(s => { s.active = s.id === body.slot_id; });
        return { ok: true, status: 200, json: async () => ({ ok: true }) };
      }
      if (url === "/api/account/slots/rename") {
        const s = statusPayload.slots[body.harness].find(x => x.id === body.slot_id);
        s.label = body.label;
        return { ok: true, status: 200, json: async () => Object.assign({}, s) };
      }
      if (url === "/api/account/slots/remove") {
        statusPayload.slots[body.harness] = statusPayload.slots[body.harness].filter(x => x.id !== body.slot_id);
        return { ok: true, status: 200, json: async () => ({ ok: true }) };
      }
      if (url === "/api/account/pi/login/start") {
        statusPayload.pi_login = {
          state: "running", attempt: "att-1", slot_id: body.existing ? body.slot_id : "new-pi-1",
          label: body.label || "New Pi", existing: !!body.existing, provider: body.provider || "",
          ws_path: "/api/account/pi/login/ws?attempt=att-1"
        };
        return { ok: true, status: 200, json: async () => JSON.parse(JSON.stringify(statusPayload.pi_login)) };
      }
      if (url === "/api/account/pi/login/complete") {
        completeCalls++;
        if (completeCalls === 1) {
          return { ok: false, status: 422, json: async () => ({ error: "auth_not_ready" }) };
        }
        statusPayload.pi_login = { state: "succeeded", attempt: "att-1", slot_id: "new-pi-1", auth_ready: true };
        statusPayload.slots["pi-rpc"].push({ id: "new-pi-1", label: "New Pi", active: false, managed: true, config_dir: "/tmp/slots/pi-rpc/new-pi-1" });
        return { ok: true, status: 200, json: async () => JSON.parse(JSON.stringify(statusPayload.pi_login)) };
      }
      if (url === "/api/account/pi/login/cancel") {
        cancelCalls++;
        statusPayload.pi_login = { state: "cancelled", attempt: body.attempt || "att-1" };
        return { ok: true, status: 200, json: async () => JSON.parse(JSON.stringify(statusPayload.pi_login)) };
      }
      return { ok: true, status: 200, json: async () => ({}) };
    }
  });

  env.doc.documentElement.setAttribute("data-term", "xterm-abc123.js");
  env.win.WsSettingsCombobox = { invalidateChoices() { invalidated++; } };

  // Mock xterm.js, FitAddon, ResizeObserver, WebSocket.
  const termInstances = [];
  env.win.Terminal = class FakeTerminal {
    constructor(opts) {
      this.options = opts;
      this.rows = 24;
      this.cols = 80;
      this.written = [];
      this.disposed = false;
      this._onData = null;
      termInstances.push(this);
    }
    loadAddon(a) { this.addon = a; }
    open(host) { this.host = host; }
    focus() { this.focused = true; }
    onData(fn) { this._onData = fn; }
    write(chunk) { this.written.push(Buffer.from(chunk).toString("utf8")); }
    writeln(line) { this.written.push(line); }
    dispose() { this.disposed = true; }
  };
  env.win.FitAddon = {
    FitAddon: class FakeFitAddon {
      fit() { this.fitted = (this.fitted || 0) + 1; }
    }
  };
  const roInstances = [];
  env.win.ResizeObserver = class FakeRO {
    constructor(cb) { this.cb = cb; this.disconnected = false; roInstances.push(this); }
    observe() {}
    disconnect() { this.disconnected = true; }
  };
  const wsInstances = [];
  env.win.WebSocket = class FakeWS {
    constructor(url) {
      this.url = url;
      this.readyState = 1;
      this.sent = [];
      this.closed = false;
      wsInstances.push(this);
    }
    send(msg) { this.sent.push(JSON.parse(msg)); }
    close() { this.closed = true; this.readyState = 3; }
  };

  const controller = env.win.WsSettingsAccount.create({
    el: env.el,
    button: env.button,
    append: env.append,
    svgIcon: env.svgIcon,
    request: async () => ({ entries: [{ key: "agent.werk_endpoints", value: "{}" }] }),
    showTiers: () => {},
    onChange: () => {},
    endpointSpec: { label: "js.settings.key.agent.werk_endpoints.label", description: "js.settings.key.agent.werk_endpoints.description" }
  });
  env.doc.body.append(controller.panel);
  await controller.refreshStatus();

  // 1. Group 1 (harness) must filter out pi-rpc even though status.slots["pi-rpc"] exists.
  const harnessGrp = controller.panel.querySelector('[data-account-group="harness"]');
  assert.equal(harnessGrp.querySelector('[data-account-row="slots-pi-rpc"]'), null, "pi-rpc must not appear in Group 1");
  assert.ok(harnessGrp.querySelector('[data-account-row="slots-claude"]'), "claude remains in Group 1");

  // 2. Group 3 (pirpc) is visible when status.pi_runtime is present.
  const pirpcGrp = controller.panel.querySelector('[data-account-group="pirpc"]');
  assert.equal(pirpcGrp.hidden, false);

  // 3. Install runtime -> progress track renders with --p ratio (0.25) and MB counter; settling invalidates choices.
  const installBtn = pirpcGrp.querySelector("[data-pi-runtime-install]");
  installBtn.click();
  await new Promise(r => setImmediate(r));
  const progBar = pirpcGrp.querySelector(".acc-pi-prog-bar");
  assert.ok(progBar, "expected progress bar while installing");
  assert.equal(progBar.style._props["--p"], "0.2500");
  assert.match(pirpcGrp.querySelector(".acc-pi-prog-bytes").textContent, /10\.0 \/ 40\.0 MB/);

  statusPayload.pi_runtime = { installed: true, version: "1.5.2", installing: false, error: "" };
  await controller.refreshStatus();
  assert.equal(invalidated, 1, "install settlement must invalidate choices");
  assert.equal(pirpcGrp.querySelector(".acc-pi-prog-bar"), null, "progress bar disappears after install settles");

  // 4. Switch slot invalidates choices; inline rename preserves draft across SSE refresh and saves.
  const swBtn = pirpcGrp.querySelector('[data-slot-switch="work-pi"]');
  swBtn.click();
  await new Promise(r => setImmediate(r));
  assert.equal(invalidated, 2, "slot switch must invalidate choices");

  const rnBtn = pirpcGrp.querySelector('[data-slot-rename="work-pi"]');
  rnBtn.click();
  const rnInput = pirpcGrp.querySelector(".acc-rename-inp");
  rnInput.value = "Work Pi Updated";
  await controller.refreshStatus();
  assert.equal(pirpcGrp.querySelector(".acc-rename-inp").value, "Work Pi Updated", "SSE refresh must preserve open rename draft");
  pirpcGrp.querySelector(".acc-rename-form").dispatchEvent({ type: "submit" });
  await new Promise(r => setImmediate(r));
  assert.equal(statusPayload.slots["pi-rpc"].find(s => s.id === "work-pi").label, "Work Pi Updated");

  // 5. Open Add Account drawer -> launch PTY login modal -> WS hello/out/ping/in/size -> 422 stays open -> 200 closes & invalidates.
  pirpcGrp.querySelector("[data-pi-add-account]").click();
  const drawer = pirpcGrp.querySelector("[data-pi-add-drawer]");
  assert.ok(drawer, "expected add-account pre-launch drawer");
  drawer.querySelector("[data-pi-login-label]").value = "New Pi";
  drawer.querySelector("[data-pi-login-provider]").value = "openai";
  drawer.dispatchEvent({ type: "submit" });
  await new Promise(r => setImmediate(r));
  await new Promise(r => setImmediate(r));

  const modalEl = env.doc.body.querySelector("[data-pi-pty-modal]");
  assert.ok(modalEl, "expected ephemeral PTY login modal in document.body");
  assert.equal(wsInstances.length, 1);
  assert.match(wsInstances[0].url, /\/api\/account\/pi\/login\/ws\?attempt=att-1$/);

  wsInstances[0].onopen();
  wsInstances[0].onmessage({ data: JSON.stringify({ t: "hello", state: "running", rows: 24, cols: 80 }) });
  wsInstances[0].onmessage({ data: JSON.stringify({ t: "out", data: Buffer.from("READY\r\n").toString("base64") }) });
  wsInstances[0].onmessage({ data: JSON.stringify({ t: "ping" }) });
  termInstances[0]._onData("/login\r");

  assert.ok(termInstances[0].written.includes("READY\r\n"));
  assert.ok(wsInstances[0].sent.some(f => f.t === "size" && f.rows === 24 && f.cols === 80));
  assert.ok(wsInstances[0].sent.some(f => f.t === "pong"));
  assert.ok(wsInstances[0].sent.some(f => f.t === "in" && f.data === "/login\r"));

  // First Verify & Save returns 422 auth_not_ready -> error shown, modal stays open.
  modalEl.querySelector("[data-pi-modal-complete]").click();
  await new Promise(r => setImmediate(r));
  assert.ok(env.doc.body.querySelector("[data-pi-pty-modal]"), "422 must keep modal open");
  assert.equal(modalEl.querySelector(".acc-pty-err").hidden, false);

  // Second Verify & Save succeeds -> invalidates choices, closes modal, disposes terminal + RO + WS.
  modalEl.querySelector("[data-pi-modal-complete]").click();
  await new Promise(r => setImmediate(r));
  await new Promise(r => setImmediate(r));
  assert.equal(env.doc.body.querySelector("[data-pi-pty-modal]"), null, "succeeded login closes modal");
  assert.equal(invalidated, 3, "succeeded login must invalidate choices");
  assert.equal(termInstances[0].disposed, true);
  assert.equal(wsInstances[0].closed, true);
  assert.equal(roInstances[0].disconnected, true);

  // 6. Existing-slot Terminal Login -> Cancel sends POST pi/login/cancel and cleans up.
  pirpcGrp.querySelector('[data-pi-slot-login="default"]').click();
  await new Promise(r => setImmediate(r));
  await new Promise(r => setImmediate(r));
  const modal2 = env.doc.body.querySelector("[data-pi-pty-modal]");
  assert.ok(modal2);
  modal2.querySelector("[data-pi-modal-cancel]").click();
  await new Promise(r => setImmediate(r));
  assert.equal(cancelCalls, 1);
  assert.equal(env.doc.body.querySelector("[data-pi-pty-modal]"), null);

  // 7. Hold-confirm Remove slot and Uninstall runtime both invalidate choices.
  function triggerHold(btn) {
    btn.dispatchEvent({ type: "keydown", key: " " });
    const holdTimer = env.timers.filter(t => !t.cleared && t.ms === 1500).at(-1);
    assert.ok(holdTimer, "expected 1500ms hold-confirm timer");
    holdTimer.fn();
    assert.equal(btn.getAttribute("data-hold-state"), "ready");
    env.win.dispatchEvent({ type: "keyup", key: " " });
  }
  triggerHold(pirpcGrp.querySelector('[data-slot-remove="new-pi-1"]'));
  await new Promise(r => setImmediate(r));
  await new Promise(r => setImmediate(r));
  assert.equal(invalidated, 4, "slot remove must invalidate choices");
  assert.equal(statusPayload.slots["pi-rpc"].some(s => s.id === "new-pi-1"), false);

  triggerHold(pirpcGrp.querySelector("[data-pi-runtime-uninstall]"));
  await new Promise(r => setImmediate(r));
  await new Promise(r => setImmediate(r));
  assert.equal(invalidated, 5, "runtime uninstall must invalidate choices");
  assert.equal(statusPayload.pi_runtime.installed, false);

  controller.destroy();
});
