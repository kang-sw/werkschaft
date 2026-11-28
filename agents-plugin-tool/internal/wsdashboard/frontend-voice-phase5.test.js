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

class StubEl {
  constructor(tag, id, cls) {
    this.tagName = tag.toUpperCase();
    this.id = id || "";
    this.className = cls || "";
    this.classList = {
      contains: (c) => this.className.split(/\s+/).includes(c)
    };
    this.attributes = Object.create(null);
    this.listeners = Object.create(null);
    this.children = [];
    this.parentNode = null;
    this.style = {};
    this.hidden = false;
    this.disabled = false;
    this.value = "";
    this.selectionStart = 0;
    this.selectionEnd = 0;
  }
  appendChild(child) {
    child.parentNode = this;
    this.children.push(child);
    return child;
  }
  setAttribute(k, v) { this.attributes[k] = String(v); }
  getAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attributes, k) ? this.attributes[k] : null; }
  removeAttribute(k) { delete this.attributes[k]; }
  hasAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attributes, k); }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
  removeEventListener(type, fn) { this.listeners[type] = (this.listeners[type] || []).filter(f => f !== fn); }
  dispatchEvent(ev) {
    for (const fn of (this.listeners[ev.type] || []).slice()) fn(ev);
  }
  click() { this.dispatchEvent({ type: "click" }); }
  focus() {}
  querySelector() { return null; }
}

function loadComposerEnv(opts = {}) {
  const composer = new StubEl("div", "composer", "composer");
  const ctext = new StubEl("textarea", "ctext", "");
  const csend = new StubEl("button", "csend", "csend");
  const cap = new StubEl("div", "cvox-cap", "cvox-cap");
  const cfoot = new StubEl("div", "cfoot", "cfoot");
  if (opts.emptyFoot) {
    cfoot.setAttribute("data-cfoot-empty", "1");
    cfoot.hidden = true;
  }
  const slot = new StubEl("span", "cvox-slot", "cvox-slot");
  slot.hidden = true;
  const btn = new StubEl("button", "cvox", "cvox");
  btn.hidden = true;
  slot.appendChild(btn);
  cfoot.appendChild(slot);
  composer.appendChild(ctext);
  composer.appendChild(cap);
  composer.appendChild(csend);
  composer.appendChild(cfoot);

  const docListeners = Object.create(null);
  const winListeners = Object.create(null);
  let ready = false;
  const doc = {
    hidden: false,
    documentElement: new StubEl("html", "", ""),
    getElementById(id) {
      if (!ready) return null;
      return { composer, ctext, csend, "cvox-cap": cap, cvox: btn, "cvox-slot": slot }[id] || null;
    },
    createElement(tag) { return new StubEl(tag, "", ""); },
    addEventListener(t, fn) { (docListeners[t] ||= []).push(fn); },
    removeEventListener(t, fn) { docListeners[t] = (docListeners[t] || []).filter(f => f !== fn); },
    dispatchEvent(ev) { for (const fn of (docListeners[ev.type] || []).slice()) fn(ev); }
  };

  const win = {
    document: doc,
    location: { origin: "http://127.0.0.1:19544", href: "http://127.0.0.1:19544/s/abc" },
    wsT: (k) => k,
    addEventListener(t, fn) { (winListeners[t] ||= []).push(fn); },
    removeEventListener(t, fn) { winListeners[t] = (winListeners[t] || []).filter(f => f !== fn); },
    dispatchEvent(ev) { for (const fn of (winListeners[ev.type] || []).slice()) fn(ev); }
  };
  win.top = win;
  win.parent = opts.parent || win;
  if (opts.wsOpenSettings) win.wsOpenSettings = opts.wsOpenSettings;

  const ctx = vm.createContext({
    window: win,
    document: doc,
    Event: class FakeEvent { constructor(type) { this.type = type; } },
    setTimeout,
    clearTimeout
  });
  vm.runInContext(readAsset("dashboard-voice-ptt.js"), ctx);
  vm.runInContext(readAsset("dashboard-voice.js"), ctx);
  ready = true;
  return { win, doc, composer, ctext, csend, cap, cfoot, slot, btn };
}

test("dashboard.tmpl places #cvox outside #csend-wrap in a self-contained bottom-left #cvox-slot", () => {
  const tmpl = readAsset("dashboard.tmpl");
  const sendWrapMatch = tmpl.match(/<div class="csend-wrap" id="csend-wrap">[\s\S]*?<\/div>/);
  assert.ok(sendWrapMatch, "#csend-wrap must exist");
  assert.doesNotMatch(sendWrapMatch[0], /id="cvox"/, "#cvox must not live inside #csend-wrap");

  const cfootMatch = tmpl.match(/<div class="cfoot"[\s\S]*?<\/div><\/div>/);
  assert.ok(cfootMatch, ".cfoot must exist inside #composer");
  assert.match(cfootMatch[0], /<span class="cvox-slot" id="cvox-slot" data-voice-slot="bottom-left" hidden><button type="button" class="cvox" id="cvox"/);
  assert.match(cfootMatch[0], /<span class="cvox-badge" aria-hidden="true"><\/span>/);
});

test("eligible-but-unconfigured #cvox click opens /settings?section=voice via wsOpenSettings (top-level) and postMessage (framed)", async () => {
  // Top-level / shell path via wsOpenSettings
  const opened = [];
  const env1 = loadComposerEnv({
    emptyFoot: true,
    wsOpenSettings(href, opener) { opened.push({ href, opener }); }
  });
  const handle1 = env1.win.WsVoiceInput.attachComposer({
    window: env1.win,
    document: env1.doc,
    autoDiscover: false,
    fetch: async () => ({ ok: true, json: async () => ({ order: ["native"], backends: [{ id: "native", kind: "native", available: false }], selected: null }) })
  });
  await handle1.controller.refreshCapabilities();
  assert.equal(env1.btn.hidden, false);
  assert.equal(env1.slot.hidden, false);
  assert.equal(env1.cfoot.hidden, false, "non-hosted empty cfoot unhides when #cvox is eligible");
  assert.equal(env1.btn.getAttribute("data-state"), "unconfigured");
  assert.equal(env1.btn.getAttribute("aria-label"), "js.voice.setup");

  env1.btn.click();
  assert.equal(opened.length, 1);
  assert.equal(opened[0].href, "/settings?section=voice");
  assert.equal(opened[0].opener, env1.btn);
  handle1.destroy();

  // Framed session fallback path via parent.postMessage({ wsShell: "nav", href })
  const posted = [];
  const fakeParent = { postMessage(msg, targetOrigin) { posted.push({ msg, targetOrigin }); } };
  const env2 = loadComposerEnv({ parent: fakeParent });
  const handle2 = env2.win.WsVoiceInput.attachComposer({
    window: env2.win,
    document: env2.doc,
    autoDiscover: false,
    fetch: async () => ({ ok: true, json: async () => ({ order: ["native"], backends: [], selected: null }) })
  });
  await handle2.controller.refreshCapabilities();
  env2.btn.click();
  assert.equal(posted.length, 1);
  assert.deepEqual(JSON.parse(JSON.stringify(posted[0].msg)), {
    wsShell: "nav",
    href: "http://127.0.0.1:19544/settings?section=voice"
  });
  assert.equal(posted[0].targetOrigin, "http://127.0.0.1:19544");
  handle2.destroy();
});

test("ineligible capabilities (HTTP 404 / LAN HTTP viewer) keep #cvox, #cvox-slot, and empty .cfoot hidden", async () => {
  const env = loadComposerEnv({ emptyFoot: true });
  const handle = env.win.WsVoiceInput.attachComposer({
    window: env.win,
    document: env.doc,
    autoDiscover: false,
    fetch: async () => ({ ok: false, status: 404, json: async () => ({}) })
  });
  await handle.controller.refreshCapabilities();
  assert.equal(env.btn.hidden, true);
  assert.equal(env.slot.hidden, true);
  assert.equal(env.cfoot.hidden, true);
  handle.destroy();
});

test("ws-voice-capabilities-refresh transitions #cvox from unconfigured to idle once a provider is configured", async () => {
  let available = false;
  const env = loadComposerEnv();
  const handle = env.win.WsVoiceInput.attachComposer({
    window: env.win,
    document: env.doc,
    autoDiscover: false,
    fetch: async () => ({
      ok: true,
      json: async () => ({
        order: ["groq"],
        backends: [{ id: "groq", kind: "external", available, models: [{ id: "whisper-large-v3-turbo" }] }],
        selected: available ? { backend: "groq", model: "whisper-large-v3-turbo" } : null
      })
    })
  });
  await handle.controller.refreshCapabilities();
  assert.equal(env.btn.getAttribute("data-state"), "unconfigured");

  available = true;
  env.win.dispatchEvent({ type: "ws-voice-capabilities-refresh" });
  await new Promise(r => setImmediate(r));
  assert.equal(env.btn.getAttribute("data-state"), null);
  assert.equal(env.btn.getAttribute("aria-label"), "js.voice.start");
  handle.destroy();
});

test("dashboard-voice.css uses tonal tokens, compositor-only animations, multi-line #cvox-cap fallback, and zero :has()", () => {
  const css = readAsset("dashboard-voice.css");
  assert.doesNotMatch(css, /:has\(/, "zero :has() selectors");
  assert.doesNotMatch(css, /\.csend-wrap/, "no coupling to #csend-wrap");
  assert.match(css, /\.cvox-slot/);
  assert.match(css, /\.cvox\[data-state="unconfigured"\]/);
  assert.match(css, /\[data-voice-span\]/);
  assert.match(css, /\[data-voice-span="refining"\]/);
  assert.match(css, /\.cvox-text\s*\{[^}]*white-space:\s*pre-wrap/);
  for (const name of ["cvox-pulse", "cvox-ring", "cvox-span-pulse"]) {
    const m = css.match(new RegExp(`^@keyframes ${name}\\b[^\\n]*\\n(?:[ \\t][^\\n]*\\n)*`, "m"));
    assert.ok(m, `missing @keyframes ${name}`);
    assert.doesNotMatch(m[0], /box-shadow|background-position|filter|width|height|left|top/, `${name} must be compositor-only`);
  }
  assert.match(css, /:root\[data-animation-paused\]/);
  assert.match(css, /@media \(prefers-reduced-motion: reduce\)/);

  // dashboard-voice.js stays strictly below its 1,019-line baseline
  const voiceLines = readAsset("dashboard-voice.js").split("\n").length;
  assert.ok(voiceLines < 1019, `expected dashboard-voice.js (${voiceLines}) < 1019`);

  // js.voice.setup exists in all 11 locale tables
  const locales = ["en", "ko", "ja", "zh-Hans", "zh-Hant", "de", "es", "fr", "it", "pt-BR", "ru"];
  for (const loc of locales) {
    const tbl = readLocale(loc);
    assert.equal(typeof tbl["js.voice.setup"], "string", `${loc} missing js.voice.setup`);
    assert.notEqual(tbl["js.voice.setup"].trim(), "", `${loc} empty js.voice.setup`);
  }
});
