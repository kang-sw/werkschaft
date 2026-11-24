const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

function read(name) {
  return fs.readFileSync(path.join(__dirname, "assets", name), "utf8");
}

function readLocale(name) {
  return JSON.parse(fs.readFileSync(path.join(__dirname, "..", "wsi18n", "tables", name + ".json"), "utf8"));
}

const origin = "http://localhost:19544";
const context = vm.createContext({window: {}, URLSearchParams});
vm.runInContext(read("shell.js"), context);
const shell = context.window.WsShell;

const preferenceWindow = {};
vm.runInNewContext(read("dashboard-settings-preferences.js"), {window: preferenceWindow});
const preferences = preferenceWindow.WsSettingsPreferences;

test("preference names follow the config key grammar and reject duplicates", () => {
  const keys = new Set(["preference.in-use"]);
  for (const name of ["default", "0", "1-a_2", "a_", "a--"]) {
    assert.equal(preferences.nameError(name, keys), "", name);
  }
  for (const name of ["_first", "-first", "Upper", "é", "a.b", "a b", "a\n"]) {
    assert.equal(preferences.nameError(name, keys), "name_invalid", name);
  }
  assert.equal(preferences.nameError("", keys), "name_required");
  assert.equal(preferences.nameError("in-use", keys), "name_duplicate");
});

test("preference slots use manual order and distinguish hidden overrides from reset", () => {
  const layer = (scope, value) => ({scope, value, selected: value, present: true});
  const entry = (name, ...layers) => ({key: "preference." + name, selectors: {}, layers});
  const inputs = [
    entry("z", layer("global", "Z")),
    entry("masked", layer("global", "kept text"), layer("repo", "")),
    entry("a", layer("repo", "A")),
    entry("default", layer("global", "Default")),
    entry("a-2", layer("global", "A dash 2")),
    entry("a_1", layer("global", "A underscore 1")),
    entry("unused"),
  ];
  const selection = {scope: "repo"};
  const initial = preferences.slotModel(inputs, selection, [], () => null);
  assert.equal(initial.rows.map(row => row.key.slice(11)).join(" "), "default a a-2 a_1 z masked");
  assert.equal(initial.rows.at(-1).winner.value, "");
  assert.equal(initial.rows.at(-1).masked, "kept text");
  assert.equal(initial.rows[1].resetTo, undefined);
  const key = "preference.masked";
  const hide = preferences.slotOperation(key, "hide");
  const reset = preferences.slotOperation(key, "reset");
  assert.equal(hide.action, "set");
  assert.equal(hide.value, "");
  assert.equal(reset.action, "reset");
  assert(!Object.hasOwn(reset, "value"));
  assert.equal(JSON.stringify(preferences.slotOperation(key, "restore")), JSON.stringify(reset));
  const restored = preferences.slotModel(inputs, selection, [reset], entry => entry.key === key ? reset : null);
  assert.equal(restored.rows.find(row => row.key === key).winner.value, "kept text");
});

test("moving the legacy preference copies its value before resetting its bucket", () => {
  const selections = [{harness: "*"}, {}];
  for (const selectors of selections) {
    const ops = preferences.migrationOperations("legacy", selectors, false);
    assert.deepEqual(JSON.parse(JSON.stringify(ops)), [
      {key: "preference.default", selectors: {}, action: "set", value: "legacy"},
      {key: "prompt.UserPreferenceSection", selectors, action: "reset"}
    ]);
    assert.deepEqual(JSON.parse(JSON.stringify(preferences.migrationOperations("legacy", selectors, true))),
      [JSON.parse(JSON.stringify(ops[1]))]);
  }
  const common = {
    key: "prompt.UserPreferenceSection", selectors: {harness: "*"}, seed: "seed",
    layers: [{scope: "global", bucket: "all", value: "legacy", selected: "legacy", present: true}]
  };
  const state = preferences.slotModel([common], {scope: "global"}, [], () => null);
  assert.equal(state.rows[0].current.value, "legacy");
  assert.equal(state.rows[0].current.legacy, true);
  assert.equal(state.move.copied, false);
  const inherited = preferences.slotModel([common], {scope: "repo"}, [], () => null);
  assert.equal(inherited.rows[0].current.scope, "global");
  assert.equal(inherited.move, undefined);
});

test("companion director labels use key-specific translations and retain translated off", () => {
  const source = read("dashboard-settings.js");
  const settingsContext = {document: {querySelector: () => null}};
  vm.runInNewContext(source, settingsContext);
  assert.equal(JSON.stringify(settingsContext.wsSettingsChoiceLabels), JSON.stringify({
    "dashboard.companion_director": {
      rules: "js.settings.key.dashboard.companion_director.rules",
      agent: "js.settings.key.dashboard.companion_director.agent"
    }
  }));
  assert.match(source, /shownValue\(choice, entry\.key\)/);
  assert.match(source, /value === false \|\| value === "off"\) return tr\("js\.settings\.off"\)/);
});

test("companion labels are present in every supported locale", () => {
  const locales = ["en", "ko", "ja", "zh-Hans", "zh-Hant", "de", "es", "fr", "it", "pt-BR", "ru"];
  const modeKeys = [
    "js.settings.key.dashboard.companion_director.rules",
    "js.settings.key.dashboard.companion_director.agent"
  ];
  const stageKeys = [
    "js.companion.character",
    "js.companion.return_home",
    "js.companion.hide",
    "js.companion.reload",
    "js.companion.reload_denied",
    "js.companion.reload_failed",
    "js.companion.preset",
    "js.companion.preset_failed",
    "js.companion.pack_failed",
    "js.companion.settings",
    "js.companion.close",
    "js.companion.persona",
    "js.companion.persona_none",
    "js.companion.settings_failed",
    "js.companion.settings_readonly"
  ];
  for (const locale of locales) {
    const table = readLocale(locale);
    for (const key of [...modeKeys, ...stageKeys]) {
      assert.equal(typeof table[key], "string", `${locale} is missing ${key}`);
      assert.notEqual(table[key].trim(), "", `${locale} has an empty ${key}`);
    }
  }
  assert.equal(readLocale("ko")[modeKeys[0]], "규칙");
  assert.equal(readLocale("ko")[modeKeys[1]], "에이전트");
});

test("settings panel skips companion-modal and deprecated rows and keeps enabled and director", () => {
  const catalog = JSON.parse(fs.readFileSync(path.join(__dirname, "assets", "dashboard-settings-catalog.json"), "utf8"));
  const specs = new Map(catalog.map(spec => [spec.key, spec]));
  const method = read("dashboard-settings.js").match(/\n  function sectionEntries\(name\) \{[\s\S]*?\n  \}/)[0];
  const keys = catalog.filter(spec => spec.key.startsWith("dashboard.companion_")).map(spec => spec.key);
  const sectionEntries = vm.runInNewContext(method + "\nsectionEntries", {
    snapshot: {entries: keys.map(key => ({key}))},
    specFor: key => specs.get(key),
    adapters: ["claude", "pi", "agy", "dashboard"]
  });
  const shown = sectionEntries("companion").map(entry => entry.key).sort();
  assert.deepEqual(shown, ["dashboard.companion_director", "dashboard.companion_enabled"]);
  const hidden = catalog.filter(spec => spec.surface === "companion-modal").map(spec => spec.key).sort();
  assert.deepEqual(hidden, [
    "dashboard.companion_agent_cooldown_seconds", "dashboard.companion_agent_hourly_cap",
    "dashboard.companion_attachments", "dashboard.companion_eyes", "dashboard.companion_hair",
    "dashboard.companion_outfit", "dashboard.companion_pack", "dashboard.companion_palette",
    "dashboard.companion_preset", "dashboard.companion_sources"
  ]);
  // The 0.51.0 companion_mode alias stays declared but no UI renders it.
  const deprecated = catalog.filter(spec => spec.surface === "deprecated").map(spec => spec.key);
  assert.deepEqual(deprecated, ["dashboard.companion_mode"]);
  assert.equal(keys.length, shown.length + hidden.length + deprecated.length);
});

test("settings links and frames keep the exact inspection identifiers", () => {
  const origins = [
    {scope: "global", target: "", context: {worktree: "", session: ""}},
    {scope: "project", target: "project-a", context: {worktree: "", session: ""}},
    {scope: "repo", target: "tree-a", context: {worktree: "tree-a", session: ""}},
    {scope: "session", target: "session-a", context: {worktree: "tree-a", session: "session-a"}}
  ];
  for (const selection of origins) {
    const href = shell.settingsScopeHref("builtin", selection, () => "");
    const url = new URL(href, origin);
    const frame = shell.settingsFrame(url, origin);
    const src = new URL(frame.src, origin);
    assert.equal(shell.framed(url, origin), null);
    assert.equal(shell.settingsFrame(url, "https://other.example"), null);
    assert.equal(src.pathname, "/settings");
    assert.equal(src.searchParams.get("scope"), "builtin");
    assert.equal(src.searchParams.get("target"), null);
    assert.equal(src.searchParams.get("worktree"), selection.context.worktree || null);
    assert.equal(src.searchParams.get("session"), selection.context.session || null);
    assert.equal(src.searchParams.get("frame"), "1");
    assert.equal(shell.pagePath(src) + src.hash, href);
    assert.equal(src.searchParams.get("modal"), "1");
    assert.equal(shell.settingsFrame(src, origin).href, href);
    assert.equal(frame.href, href);
  }
});

test("scope navigation requires an exact target and never chooses a sibling", () => {
  const projectFor = tree => tree === "tree-a" ? "project-a" : "";
  let selection = {scope: "builtin", target: "", context: {worktree: "tree-a", session: "session-a"}};
  assert.equal(shell.settingsScopeHref("project", selection, projectFor),
    "/settings?scope=project&target=project-a&worktree=tree-a&session=session-a");
  assert.equal(shell.settingsScopeHref("repo", selection, projectFor),
    "/settings?scope=repo&target=tree-a&worktree=tree-a&session=session-a");
  assert.equal(shell.settingsScopeHref("session", selection, projectFor),
    "/settings?scope=session&target=session-a&worktree=tree-a&session=session-a");
  selection.context = {worktree: "stale", session: ""};
  assert.equal(shell.settingsScopeHref("project", selection, projectFor), "");
  assert.equal(shell.settingsScopeHref("session", selection, projectFor), "");
  assert.equal(shell.settingsScopeHref("global", selection, projectFor), "/settings?scope=global&worktree=stale");
  selection.context = {worktree: "", session: ""};
  assert.equal(shell.settingsScopeHref("repo", selection, projectFor), "");
});

test("settings distinguishes an older daemon from a missing target and never posts on load", async () => {
  const calls = [];
  const replies = [
    {status: 404, ok: false, json: async () => { throw new Error("text response"); }},
    {status: 404, ok: false, json: async () => ({error: {code: "target_not_found"}})},
    {status: 400, ok: false, json: async () => ({error: {code: "invalid_context"}})}
  ];
  const source = read("dashboard-settings.js");
  const method = source.match(/\n  async function request\(path, body\) \{[\s\S]*?\n  \}/)[0];
  const load = vm.runInNewContext(method + "\nrequest", {
    fetch: async (url, options) => {
      calls.push([url, options.method]);
      return replies.shift();
    }
  });
  for (const code of ["upgrade_required", "target_not_found", "invalid_context"]) {
    await assert.rejects(load("/api/config/snapshot"), {code});
  }
  assert.deepEqual(calls, Array(3).fill(["/api/config/snapshot", undefined]));
});

test("rail gears preserve native activation and separate it from row actions", () => {
  const markup = read("dashboard.tmpl");
  assert.match(markup, /<button type="button" class="r-config-gear"[^>]*data-scope-entry=/);
  assert.match(markup, /<a class="settings-page-link" href="\/settings">/);
  const rail = read("dashboard-rail.js");
  assert.match(rail, /gear && root.hasAttribute\("data-can-control"\)\) \{\s+e.preventDefault\(\);\s+e.stopPropagation\(\);/);
  const css = read("dashboard-rail.css");
  assert.match(css, /\.r-config-gear \{[^}]*opacity: 0;/);
  assert.match(css, /\.r-config-gear:hover,\n:root\[data-can-control\] \.rail \.r-config-gear:focus-visible,\n:root\[data-can-control\] \.rail \.r-config-gear\[aria-current="page"\] \{ opacity: 1; \}/);
  assert.match(markup, /<div class="wsbar">[\s\S]*?\{\{template "r-config-gear" \(settingsgear "project" \.SettingsID ""\)\}\}<button type="button" class="memo-count"[\s\S]*?<\/button><span class="ct"/);
  assert.doesNotMatch(css, /\.rail \.wsbar \.ct \{[^}]*margin-left:\s*auto/);
  assert.match(css, /:root\[data-can-control\] \.rail \.wsbar > \.r-config-gear,\n:root:not\(\[data-can-control\]\) \.rail \.wsbar > \.r-config-gear \+ \* \{ margin-left: auto; \}/);
});

test("project rail target survives standalone scope traversal without becoming inspection context", () => {
  const context = {worktree: "", session: ""};
  const project = {scope: "project", target: "project-a", context};
  const unbound = {scope: "builtin", target: "", context};
  const builtin = shell.settingsScopeHref("builtin", project, () => "");
  assert.equal(builtin, "/settings?scope=builtin#project=project-a");
  const remembered = new URLSearchParams(new URL(builtin, origin).hash.slice(1)).get("project");
  assert.equal(shell.settingsScopeHref("global", unbound, () => "", remembered), "/settings?scope=global#project=project-a");
  assert.equal(shell.settingsScopeHref("project", unbound, () => "", remembered), "/settings?scope=project&target=project-a");
});

test("the companion modal maps every companion-modal catalog row to a live config field", () => {
  const catalog = JSON.parse(fs.readFileSync(path.join(__dirname, "assets", "dashboard-settings-catalog.json"), "utf8"));
  const context = vm.createContext({window: {WsCompanion: {}}});
  vm.runInContext(read("dashboard-companion-settings.js"), context);
  const fields = context.window.WsCompanion.companionConfigFields;
  const modal = catalog.filter(spec => spec.surface === "companion-modal").map(spec => spec.key).sort();
  assert.deepEqual(Object.keys(fields).sort(), modal);
  assert.deepEqual(Object.values(fields).sort(), ["agentCooldownSeconds", "agentHourlyCap", "attachments", "eyes", "hair", "outfit", "pack", "palette", "preset", "sources"]);
});

function createFakeDOM() {
  class FakeClassList {
    constructor(el) { this.el = el; this.set = new Set(); }
    _sync() { this.el._className = [...this.set].join(" "); }
    add(...names) { names.forEach(n => n && this.set.add(n)); this._sync(); }
    remove(...names) { names.forEach(n => this.set.delete(n)); this._sync(); }
    toggle(name, force) {
      const on = force === undefined ? !this.set.has(name) : !!force;
      if (on) this.set.add(name); else this.set.delete(name);
      this._sync();
      return on;
    }
    contains(name) { return this.set.has(name); }
    setFromString(str) { this.set = new Set(String(str || "").split(/\s+/).filter(Boolean)); this.el._className = [...this.set].join(" "); }
  }
  class FakeEvent {
    constructor(type, opts = {}) {
      this.type = type;
      this.bubbles = !!opts.bubbles;
      this.defaultPrevented = false;
      this.propagationStopped = false;
      this.key = opts.key || "";
      this.isComposing = !!opts.isComposing;
      this.target = opts.target || null;
      this.relatedTarget = opts.relatedTarget || null;
    }
    preventDefault() { this.defaultPrevented = true; }
    stopPropagation() { this.propagationStopped = true; }
  }
  class FakeElement {
    constructor(tag, doc) {
      this.tagName = tag.toUpperCase();
      this.ownerDocument = doc;
      this.parentNode = null;
      this.children = [];
      this.attrs = {};
      this.dataset = {};
      this.style = {};
      this.listeners = {};
      this.hidden = false;
      this.disabled = false;
      this.readOnly = false;
      this.value = "";
      this.tabIndex = 0;
      this.classList = new FakeClassList(this);
      this._className = "";
      this._textContent = "";
    }
    get className() { return this._className; }
    set className(v) { this.classList.setFromString(v); }
    get textContent() { return this._textContent || this.children.map(c => c.textContent).join(""); }
    set textContent(v) { this.children = []; this._textContent = String(v); }
    get isConnected() {
      let cur = this;
      while (cur) {
        if (cur === this.ownerDocument.body || cur === this.ownerDocument.documentElement) return true;
        cur = cur.parentNode;
      }
      return false;
    }
    setAttribute(k, v) { this.attrs[k] = String(v); }
    hasAttribute(k) { return Object.hasOwn(this.attrs, k); }
    getAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k) ? this.attrs[k] : null; }
    removeAttribute(k) { delete this.attrs[k]; }
    appendChild(child) {
      if (typeof child === "string") {
        const txt = this.ownerDocument.createTextNode(child);
        this.children.push(txt);
        return txt;
      }
      if (child.parentNode) child.parentNode.removeChild(child);
      child.parentNode = this;
      this.children.push(child);
      return child;
    }
    append(...nodes) {
      nodes.forEach(n => this.appendChild(n));
    }
    remove() {
      if (this.parentNode) this.parentNode.removeChild(this);
    }
    replaceWith(...nodes) {
      const parent = this.parentNode;
      if (!parent) return;
      const idx = parent.children.indexOf(this);
      this.parentNode = null;
      if (idx < 0) return;
      const inserted = [];
      for (const n of nodes) {
        if (typeof n === "string") {
          inserted.push(this.ownerDocument.createTextNode(n));
        } else {
          if (n.parentNode) n.parentNode.removeChild(n);
          n.parentNode = parent;
          inserted.push(n);
        }
      }
      parent.children.splice(idx, 1, ...inserted);
    }
    matchesSelector(sel) {
      const parts = sel.split(",").map(s => s.trim()).filter(Boolean);
      return parts.some(part => {
        let rest = part;
        const attrs = [];
        rest = rest.replace(/\[([^\]=]+)(?:=([^\]]+))?\]/g, (_, k, v) => {
          attrs.push([k.trim(), v !== undefined ? v.replace(/^["']|["']$/g, "") : null]);
          return "";
        });
        const tokens = rest.split(".");
        const tag = tokens[0] ? tokens[0].toUpperCase() : "";
        const classes = tokens.slice(1).filter(Boolean);
        if (tag && this.tagName !== tag) return false;
        if (!classes.every(c => this.classList.contains(c))) return false;
        return attrs.every(([k, v]) => {
          let got = this.getAttribute(k);
          if (got === null && k.startsWith("data-")) {
            const camel = k.slice(5).replace(/-([a-z])/g, (_, ch) => ch.toUpperCase());
            if (Object.prototype.hasOwnProperty.call(this.dataset, camel)) got = String(this.dataset[camel]);
          }
          return v === null ? got !== null : got === v;
        });
      });
    }
    querySelectorAll(sel) {
      const out = [];
      const visit = node => {
        for (const ch of node.children) {
          if (ch.tagName && ch.matchesSelector(sel)) out.push(ch);
          if (ch.children) visit(ch);
        }
      };
      visit(this);
      return out;
    }
    querySelector(sel) {
      return this.querySelectorAll(sel)[0] || null;
    }
    removeChild(child) {
      const i = this.children.indexOf(child);
      if (i >= 0) { this.children.splice(i, 1); child.parentNode = null; }
      return child;
    }
    insertBefore(node, ref) {
      if (node.parentNode) node.parentNode.removeChild(node);
      const i = this.children.indexOf(ref);
      node.parentNode = this;
      if (i < 0) this.children.push(node); else this.children.splice(i, 0, node);
      return node;
    }
    replaceChildren(...nodes) {
      this.children.forEach(c => { c.parentNode = null; });
      this.children = [];
      this._textContent = "";
      nodes.forEach(n => this.appendChild(n));
    }
    contains(target) {
      let cur = target;
      while (cur) {
        if (cur === this) return true;
        cur = cur.parentNode;
      }
      return false;
    }
    closest(sel) {
      const wantTag = /^[a-z]+$/i.test(sel) ? sel.toUpperCase() : null;
      const wantCls = sel.startsWith(".") ? sel.slice(1) : (sel.includes(".") ? sel.split(".")[1] : null);
      let cur = this;
      while (cur) {
        if (wantTag && cur.tagName === wantTag && (!wantCls || cur.classList.contains(wantCls))) return cur;
        if (!wantTag && wantCls && cur.classList.contains(wantCls)) return cur;
        cur = cur.parentNode;
      }
      return null;
    }
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
    removeEventListener(type, fn) {
      if (!this.listeners[type]) return;
      this.listeners[type] = this.listeners[type].filter(f => f !== fn);
    }
    dispatchEvent(ev) {
      if (!ev.target) ev.target = this;
      let cur = this;
      while (cur) {
        for (const fn of (cur.listeners[ev.type] || []).slice()) {
          fn.call(cur, ev);
          if (ev.propagationStopped) break;
        }
        if (!ev.bubbles || ev.propagationStopped) break;
        cur = cur.parentNode;
      }
      return !ev.defaultPrevented;
    }
    focus() { this.ownerDocument.activeElement = this; this.dispatchEvent(new FakeEvent("focus")); }
    click() { this.dispatchEvent(new FakeEvent("click", { bubbles: true })); }
    getBoundingClientRect() { return { left: 20, top: 100, right: 220, bottom: 130, width: 200, height: 30 }; }
    scrollIntoView() {}
  }
  const doc = {
    title: "Settings",
    activeElement: null,
    listeners: {},
    querySelector: sel => doc.documentElement.querySelector(sel),
    querySelectorAll: sel => doc.documentElement.querySelectorAll(sel),
    createTextNode: text => ({ textContent: String(text) }),
    createElement(tag) { return new FakeElement(tag, doc); },
    createElementNS(_ns, tag) { return new FakeElement(tag, doc); },
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); },
    removeEventListener(type, fn) {
      if (this.listeners[type]) this.listeners[type] = this.listeners[type].filter(f => f !== fn);
    }
  };
  doc.documentElement = new FakeElement("html", doc);
  doc.body = new FakeElement("body", doc);
  doc.documentElement.appendChild(doc.body);
  const win = {
    innerWidth: 1280,
    innerHeight: 800,
    listeners: {},
    wsT: k => k,
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); },
    removeEventListener(type, fn) {
      if (this.listeners[type]) this.listeners[type] = this.listeners[type].filter(f => f !== fn);
    }
  };
  doc.defaultView = win;
  return { doc, win, FakeEvent };
}

test("settings combobox lazy-loads /support/choices once, filters, preserves custom text, and mounts in dialog", async () => {
  const { doc, win, FakeEvent } = createFakeDOM();
  let fetchCount = 0;
  let resolveFetch;
  const fetchPromise = new Promise(r => { resolveFetch = r; });
  const ctx = {
    window: win,
    document: doc,
    Event: FakeEvent,
    fetch: async (url) => {
      assert.equal(url, "/support/choices");
      fetchCount++;
      return fetchPromise;
    }
  };
  win.window = win;
  vm.runInNewContext(read("dashboard-settings-combobox.js"), ctx);
  const cb = win.WsSettingsCombobox;
  assert.ok(cb, "WsSettingsCombobox is exported on window");
  assert.equal(fetchCount, 0, "must not fetch /support/choices until opened or focused");

  const container = doc.createElement("div");
  doc.body.appendChild(container);
  let backendValue = "claude";
  const input = doc.createElement("input");
  input.className = "inp mono";
  input.setAttribute("aria-label", "small · Model");
  container.appendChild(input);

  let dirtyCount = 0;
  container.addEventListener("input", () => { dirtyCount++; });

  const ctrl = cb.attach(input, {
    getOptions: () => cb.modelOptions(backendValue)
  });

  // Concurrent opens before fetch resolves trigger only one request.
  ctrl.button.click();
  ctrl.open(true);
  assert.equal(fetchCount, 1);

  resolveFetch({
    ok: true,
    json: async () => ({
      harnesses: [
        { harness: "claude", available: true, models: [{ id: "sonnet" }, { id: "opus" }, { id: "haiku" }], efforts: ["low", "medium", "high"] },
        { harness: "pi", available: true, models: [{ id: "anthropic/claude-sonnet-4-5", thinking: true }], efforts: ["off", "minimal", "high"] }
      ]
    })
  });
  await new Promise(r => setTimeout(r, 10));

  assert.equal(input.getAttribute("role"), "combobox");
  assert.equal(input.getAttribute("aria-autocomplete"), "list");
  assert.equal(input.getAttribute("aria-expanded"), "true");
  assert.equal(ctrl.popup.parentNode, doc.body, "outside dialog mounts to document.body");
  assert.equal(ctrl.popup.children.length, 3);

  // Typing filters live and preserves custom text on Enter when nothing is highlighted.
  input.value = "op";
  input.dispatchEvent(new FakeEvent("input", { bubbles: true }));
  assert.deepEqual(ctrl.popup.children.map(li => li.dataset.value), ["opus"]);

  input.value = "my-custom-model";
  input.dispatchEvent(new FakeEvent("input", { bubbles: true }));
  assert.equal(input.getAttribute("aria-expanded"), "false");
  input.dispatchEvent(new FakeEvent("keydown", { key: "Enter" }));
  assert.equal(input.value, "my-custom-model", "custom unlisted text is never overwritten");

  // Chevron toggles full unfiltered list even with existing text.
  ctrl.button.click();
  assert.equal(input.getAttribute("aria-expanded"), "true");
  assert.equal(ctrl.popup.children.length, 3);

  // Escape closes popup without reverting typed text and stops propagation.
  const esc = new FakeEvent("keydown", { key: "Escape" });
  input.dispatchEvent(esc);
  assert.equal(esc.defaultPrevented, true);
  assert.equal(esc.propagationStopped, true);
  assert.equal(input.getAttribute("aria-expanded"), "false");
  assert.equal(input.value, "my-custom-model");

  // Backend switch updates options dynamically and strips thinking badge when selecting.
  backendValue = "  PI  ";
  ctrl.button.click();
  assert.deepEqual(ctrl.popup.children.map(li => li.dataset.value), ["anthropic/claude-sonnet-4-5"]);
  assert.equal(ctrl.popup.children[0].children.length, 2, "renders thinking tag beside model id");
  const dirtyBeforeSelect = dirtyCount;
  input.dispatchEvent(new FakeEvent("keydown", { key: "ArrowDown" }));
  assert.equal(input.getAttribute("aria-activedescendant"), ctrl.popup.children[0].id);
  input.dispatchEvent(new FakeEvent("keydown", { key: "Enter" }));
  assert.equal(input.value, "anthropic/claude-sonnet-4-5");
  assert.equal(dirtyCount, dirtyBeforeSelect + 1, "selecting option fires bubbling input event");
  assert.equal(fetchCount, 1, "still only one /support/choices fetch");

  // Inside <dialog>, popup mounts on the dialog itself (top layer).
  const dialog = doc.createElement("dialog");
  doc.body.appendChild(dialog);
  const dlgInput = doc.createElement("input");
  dlgInput.className = "inp mono tr-bundle-model";
  dialog.appendChild(dlgInput);
  const dlgCtrl = cb.attach(dlgInput, { getOptions: () => cb.modelOptions("claude") });
  dlgCtrl.button.click();
  assert.equal(dlgCtrl.popup.parentNode, dialog, "popup inside dialog mounts inside dialog");

  // Disabled input suppresses opening and disables chevron.
  const roInput = doc.createElement("input");
  roInput.disabled = true;
  doc.body.appendChild(roInput);
  const roCtrl = cb.attach(roInput, { getOptions: () => cb.modelOptions("claude") });
  assert.equal(roCtrl.button.disabled, true);
  roCtrl.button.click();
  roInput.dispatchEvent(new FakeEvent("keydown", { key: "ArrowDown" }));
  assert.equal(roInput.getAttribute("aria-expanded"), "false");
});

test("settings combobox degrades gracefully when /support/choices fails", async () => {
  const { doc, win, FakeEvent } = createFakeDOM();
  const ctx = {
    window: win,
    document: doc,
    Event: FakeEvent,
    fetch: async () => ({ ok: false, status: 500 })
  };
  win.window = win;
  vm.runInNewContext(read("dashboard-settings-combobox.js"), ctx);
  const cb = win.WsSettingsCombobox;
  const input = doc.createElement("input");
  doc.body.appendChild(input);
  const ctrl = cb.attach(input, { getOptions: () => cb.modelOptions("claude") });
  ctrl.button.click();
  await new Promise(r => setTimeout(r, 10));
  assert.equal(input.getAttribute("aria-expanded"), "false");
  input.value = "sonnet";
  input.dispatchEvent(new FakeEvent("input", { bubbles: true }));
  assert.equal(input.value, "sonnet");
});

test("settings stage caps width at 1280px and centers cleanly at wide and ultrawide viewports", () => {
  const css = read("dashboard-settings.css");
  const stageBlock = css.match(/\.stage\s*\{([^}]*)\}/);
  assert.ok(stageBlock, "expected .stage rule in dashboard-settings.css");
  assert.match(stageBlock[1], /width:\s*100%/);
  assert.match(stageBlock[1], /max-width:\s*1280px/);
  assert.match(stageBlock[1], /box-sizing:\s*border-box/);
  assert.match(stageBlock[1], /margin-inline:\s*auto/);

  const wideStage = css.match(/@media\s*\(min-width:\s*1600px\)\s*\{[\s\S]*?:root\[data-mode="settings"\]\s+\.stage\s*\{([^}]*)\}/);
  assert.ok(wideStage, 'expected >=1600px :root[data-mode="settings"] .stage rule');
  assert.match(wideStage[1], /grid-column:\s*2/);
  assert.match(wideStage[1], /justify-self:\s*center/);
  assert.match(wideStage[1], /align-self:\s*stretch/);
});

test("agy subagent settings catalog, translations, enabled_when precedence, and dependent row updates", () => {
  const catalog = JSON.parse(fs.readFileSync(path.join(__dirname, "assets", "dashboard-settings-catalog.json"), "utf8"));
  const specs = new Map(catalog.map(spec => [spec.key, spec]));
  const preferSpec = specs.get("agy.prefer_native_subagent");
  const avoidSpec = specs.get("agy.avoid_agy_subagent");
  assert.ok(preferSpec && preferSpec.section === "agy" && preferSpec.control === "toggle");
  assert.ok(avoidSpec && avoidSpec.section === "agy" && avoidSpec.control === "toggle");
  assert.deepEqual(avoidSpec.enabled_when, {
    key: "agy.prefer_native_subagent",
    equals: false,
    reason: "requires_native_subagent_off"
  });

  const requiredKeys = [
    "js.settings.disabled.dependency_unmet",
    "js.settings.disabled.requires_native_subagent_off",
    "js.settings.key.agy.prefer_native_subagent.label",
    "js.settings.key.agy.prefer_native_subagent.description",
    "js.settings.key.agy.avoid_agy_subagent.label",
    "js.settings.key.agy.avoid_agy_subagent.description"
  ];
  for (const locale of ["en", "de", "es", "fr", "it", "ja", "ko", "pt-BR", "ru", "zh-Hans", "zh-Hant"]) {
    const table = readLocale(locale);
    for (const k of requiredKeys) {
      assert.equal(typeof table[k], "string", `${locale} missing ${k}`);
      assert.ok(table[k].length > 0, `${locale} empty ${k}`);
    }
  }

  const source = read("dashboard-settings.js");
  const extract = fn => source.match(new RegExp(`\\n  function ${fn}\\([^)]*\\) \\{[\\s\\S]*?\\n  \\}`))[0];
  const snippet = [
    extract("enabledWhenMet"),
    extract("baseEditable"),
    extract("editable"),
    extract("refreshDependentRows"),
    extract("rowReason"),
    "({enabledWhenMet, baseEditable, editable, refreshDependentRows, rowReason})"
  ].join("\n");

  const makeEntry = (key, value, overrides = {}) => Object.assign({
    key, value, type: "boolean", editable: true, selectors: {}, layers: []
  }, overrides);

  const evalCase = (opts) => {
    const ctx = {
      scope: opts.scope,
      selection: opts.selection || {scope: opts.scope, target: "t"},
      extension: opts.extension || {},
      snapshot: {entries: opts.entries},
      catalog,
      content: opts.content || {querySelectorAll: () => []},
      renderRow: opts.renderRow || (() => ({})),
      matches: opts.matches || (() => true)
    };
    return vm.runInNewContext(snippet, ctx);
  };

  // Builtin beats enabled_when.
  let h = evalCase({scope: "builtin", entries: [makeEntry("agy.prefer_native_subagent", true), makeEntry("agy.avoid_agy_subagent", true)]});
  assert.equal(h.editable(h_entry(true), avoidSpec), false);
  assert.equal(h.rowReason(h_entry(true), avoidSpec), "builtin_read_only");

  function h_entry(ed, disabled_reason = "") {
    return makeEntry("agy.avoid_agy_subagent", true, {editable: ed, disabled_reason});
  }

  // Server entry.disabled_reason (session scope_unsupported) beats enabled_when.
  h = evalCase({scope: "session", entries: [makeEntry("agy.prefer_native_subagent", true), h_entry(false, "scope_unsupported")]});
  assert.equal(h.editable(h_entry(false, "scope_unsupported"), avoidSpec), false);
  assert.equal(h.rowReason(h_entry(false, "scope_unsupported"), avoidSpec), "scope_unsupported");

  // Repo inspection without extension.editable beats enabled_when.
  h = evalCase({scope: "repo", entries: [makeEntry("agy.prefer_native_subagent", true), h_entry(true)]});
  assert.equal(h.editable(h_entry(true), avoidSpec), false);
  assert.equal(h.rowReason(h_entry(true), avoidSpec), "repo_read_only");

  // Editable repo/project with prefer_native_subagent === true triggers dependency lock.
  for (const [scope, extension] of [["project", {}], ["repo", {editable: () => true}]]) {
    h = evalCase({scope, extension, entries: [makeEntry("agy.prefer_native_subagent", true), h_entry(true)]});
    assert.equal(h.editable(h_entry(true), avoidSpec), false);
    assert.equal(h.rowReason(h_entry(true), avoidSpec), "requires_native_subagent_off");
  }

  // Effective value === false unlocks agy.avoid_agy_subagent.
  h = evalCase({scope: "project", entries: [makeEntry("agy.prefer_native_subagent", false), h_entry(true)]});
  assert.equal(h.editable(h_entry(true), avoidSpec), true);

  // refreshDependentRows replaces only the dependent row, not unrelated rows.
  let replaced = 0, unrelatedReplaced = 0;
  const avoidRow = {dataset: {configKey: "agy.avoid_agy_subagent"}, replaceWith(n) { replaced++; avoidRow._next = n; }};
  const unrelatedRow = {dataset: {configKey: "agy.stall_log_check"}, replaceWith() { unrelatedReplaced++; }};
  h = evalCase({
    scope: "project",
    entries: [makeEntry("agy.prefer_native_subagent", false), h_entry(true)],
    content: {querySelectorAll: () => [avoidRow, unrelatedRow]},
    renderRow: (entry, spec) => ({entry, spec})
  });
  h.refreshDependentRows("agy.prefer_native_subagent");
  assert.equal(replaced, 1);
  assert.equal(unrelatedReplaced, 0);
  assert.equal(avoidRow._next.entry.key, "agy.avoid_agy_subagent");
});

test("agy subagent settings DOM lifecycle: renderRow lock badge, makeSubmit toggle off/on, editable filter, and repo draft deferral", async () => {
  const catalog = JSON.parse(read("dashboard-settings-catalog.json"));
  const source = read("dashboard-settings.js");
  const flush = () => new Promise(r => setImmediate(r));

  async function mountSettings({ scope, initialPrefer = true, extension = {}, onSet }) {
    const { doc, win, FakeEvent } = createFakeDOM();
    const view = doc.createElement("div");
    view.setAttribute("data-settings-view", "1");
    const search = doc.createElement("input");
    search.setAttribute("data-settings-search", "1");
    const count = doc.createElement("span");
    count.setAttribute("data-settings-count", "1");
    const targetDisplay = doc.createElement("span");
    targetDisplay.setAttribute("data-settings-target-display", "1");
    targetDisplay.append(doc.createElement("span"), doc.createElement("code"));
    const precedence = doc.createElement("div");
    precedence.setAttribute("data-settings-precedence", "1");
    const sectionsNav = doc.createElement("nav");
    sectionsNav.setAttribute("data-settings-sections", "1");
    const filtersBar = doc.createElement("div");
    filtersBar.setAttribute("data-settings-filters", "1");
    const content = doc.createElement("div");
    content.setAttribute("data-settings-content", "1");
    const proposal = doc.createElement("div");
    proposal.setAttribute("data-settings-proposal", "1");
    view.append(search, count, targetDisplay, precedence, sectionsNav, filtersBar, content, proposal);
    doc.body.appendChild(view);

    const makeSnapEntry = (key, value, overrides = {}) => Object.assign({
      key,
      value,
      default: true,
      type: "boolean",
      editable: true,
      selectors: {},
      layers: [{ scope: "builtin", value: true }, { scope, selected: value === true ? undefined : value }]
    }, overrides);

    let preferVal = initialPrefer;
    const initialEntries = [
      makeSnapEntry("agy.prefer_native_subagent", initialPrefer),
      makeSnapEntry("agy.avoid_agy_subagent", true),
      makeSnapEntry("agy.stall_log_check", false, { default: false })
    ];

    win.window = win;
    win.parent = {
      location: { hash: "" },
      document: { querySelectorAll: () => [] },
      postMessage() {}
    };
    win.WsShell = shell;
    win.WsSettingsCatalog = catalog;
    win.WsSettingsExtension = extension;

    const url = new URL(`http://localhost:19544/settings?scope=${scope}&target=t1&worktree=wt1&frame=1`);
    const ctx = {
      window: win,
      document: doc,
      location: url,
      URLSearchParams,
      Map,
      Set,
      Event: FakeEvent,
      fetch: async (reqUrl, opts) => {
        if (String(reqUrl).startsWith("/api/config/snapshot")) {
          return {
            ok: true,
            status: 200,
            json: async () => ({
              scope,
              target: "t1",
              context: { worktree: "wt1", session: "" },
              entries: initialEntries
            })
          };
        }
        if (String(reqUrl) === "/api/config/set") {
          const body = JSON.parse(opts.body);
          preferVal = body.value;
          if (onSet) onSet(body);
          const fresh = makeSnapEntry(body.key, preferVal, {
            layers: [{ scope: "builtin", value: true }, { scope, selected: preferVal }]
          });
          return { ok: true, status: 200, json: async () => ({ entry: fresh, warnings: [] }) };
        }
        throw new Error("unexpected fetch " + reqUrl);
      }
    };
    vm.runInNewContext(source, ctx);
    await flush();

    // Navigate to adapters -> agy.
    const adaptersBtn = sectionsNav.querySelectorAll("[data-settings-section]")
      .find(b => b.dataset.settingsSection === "adapters");
    adaptersBtn.click();
    const agyTab = content.querySelectorAll(".subtab")
      .find(b => b.textContent.includes("js.settings.section.agy"));
    agyTab.click();

    const rowFor = key => content.querySelectorAll(".krow").find(r => r.dataset.configKey === key);
    const submitToggle = async (key) => {
      const row = rowFor(key);
      const toggle = row.querySelector(".tg");
      const form = row.querySelector("form");
      toggle.click();
      form.dispatchEvent(new FakeEvent("submit", { bubbles: true }));
      await flush();
    };
    return { content, filtersBar, rowFor, submitToggle, FakeEvent };
  }

  // 1. Project scope: initially prefer=true locks avoid row with requires_native_subagent_off badge;
  //    toggling false -> unlocks in DOM without rebuilding unrelated row; toggling true -> locks again.
  const proj = await mountSettings({ scope: "project", initialPrefer: true });
  let avoidRow = proj.rowFor("agy.avoid_agy_subagent");
  const stallBefore = proj.rowFor("agy.stall_log_check");
  assert.equal(avoidRow.querySelector(".tg").disabled, true);
  assert.equal(avoidRow.querySelector(".tag.lock").textContent, "js.settings.disabled.requires_native_subagent_off");

  await proj.submitToggle("agy.prefer_native_subagent");
  avoidRow = proj.rowFor("agy.avoid_agy_subagent");
  assert.equal(avoidRow.querySelector(".tg").disabled, false);
  assert.equal(avoidRow.querySelector(".tag.lock"), null);
  assert.equal(proj.rowFor("agy.stall_log_check"), stallBefore, "unrelated row must not be rebuilt");

  await proj.submitToggle("agy.prefer_native_subagent");
  avoidRow = proj.rowFor("agy.avoid_agy_subagent");
  assert.equal(avoidRow.querySelector(".tg").disabled, true);
  assert.equal(avoidRow.querySelector(".tag.lock").textContent, "js.settings.disabled.requires_native_subagent_off");

  // 2. Editable filter: toggling prefer false inserts avoid row, toggling back removes it.
  const editableFilterBtn = proj.filtersBar.querySelectorAll(".fchip")
    .find(b => b.textContent === "js.settings.filter.editable");
  editableFilterBtn.click();
  assert.equal(proj.rowFor("agy.avoid_agy_subagent"), undefined);
  await proj.submitToggle("agy.prefer_native_subagent");
  assert.ok(proj.rowFor("agy.avoid_agy_subagent"), "dependent row appears under editable filter");
  await proj.submitToggle("agy.prefer_native_subagent");
  assert.equal(proj.rowFor("agy.avoid_agy_subagent"), undefined, "dependent row disappears under editable filter");

  // 3. Repo draft proposal queueing (extension.submit returns Promise.resolve({})):
  //    must not mutate snapshot.entries or prematurely unlock agy.avoid_agy_subagent.
  const queuedOps = [];
  const repo = await mountSettings({
    scope: "repo",
    initialPrefer: true,
    extension: {
      editable: () => true,
      submit: (op) => {
        queuedOps.push(op);
        return Promise.resolve({});
      }
    }
  });
  let repoAvoid = repo.rowFor("agy.avoid_agy_subagent");
  assert.equal(repoAvoid.querySelector(".tg").disabled, true);
  await repo.submitToggle("agy.prefer_native_subagent");
  assert.equal(queuedOps.length, 1);
  assert.equal(queuedOps[0].value, false);
  repoAvoid = repo.rowFor("agy.avoid_agy_subagent");
  assert.equal(repoAvoid.querySelector(".tg").disabled, true, "draft queueing must not unlock dependent row before snapshot update");
  assert.equal(repoAvoid.querySelector(".tag.lock").textContent, "js.settings.disabled.requires_native_subagent_off");
});

test("modal settings iframe owns navigation, close, and Escape precedence", () => {
  function mount(modal) {
    const messages = [], keys = [], close = {};
    const search = {value: "", dispatchEvent() { this.cleared = true; }};
    const root = {setAttribute(name) { this[name] = true; }};
    const doc = {
      documentElement: root,
      querySelector(selector) {
        if (selector === "[data-settings-view]") return view;
        if (selector === "dialog[open]") return this.dialog;
        return null;
      },
      addEventListener(type, fn, capture) {
        if (type === "keydown") keys.push({fn, capture});
      }
    };
    const view = {
      hasAttribute: () => false,
      querySelector(selector) {
        if (selector === "[data-settings-search]") return search;
        if (selector === "[data-settings-close]") return {addEventListener(type, fn) { close.click = fn; }};
        return {};
      }
    };
    const win = {
      parent: {
        location: {hash: "#project=parent"},
        document: {querySelectorAll: () => []},
        postMessage(message, target) { messages.push({message, target}); }
      },
      WsShell: shell, WsSettingsCatalog: [], addEventListener() {}
    };
    const location = new URL(origin + "/settings?frame=1" + (modal ? "&modal=1" : "") + "#project=iframe");
    let source = read("dashboard-settings.js").split("  function scopeName(name)")[0];
    source += "window.testScopeHref = scopeHref; window.testNavigate = navigate; })();";
    vm.runInNewContext(source, {window: win, document: doc, location, URLSearchParams, Map, Set,
      Event: class {constructor(type) { this.type = type; }}});
    function key(fields = {}) {
      const event = Object.assign({key: "Escape", preventDefault() { this.defaultPrevented = true; },
        stopPropagation() { this.stopped = true; }}, fields);
      for (const listener of keys.filter(k => k.capture)) listener.fn(event);
      if (!event.stopped) for (const listener of keys.filter(k => !k.capture)) listener.fn(event);
      return event;
    }
    return {win, doc, root, search, close, messages, key};
  }

  const m = mount(true);
  assert.equal(m.root["data-settings-modal"], true);
  assert.equal(new URL(m.win.testScopeHref("project"), origin).searchParams.get("target"), "iframe");
  m.win.testNavigate("/settings?scope=global#project=iframe");
  assert.deepEqual(JSON.parse(JSON.stringify(m.messages.pop())), {
    message: {wsShell: "settings-navigate", href: "/settings?scope=global#project=iframe"}, target: origin
  });
  m.close.click();
  assert.equal(m.messages.pop().message.wsShell, "settings-close");
  m.key({key: "`", code: "Backquote", ctrlKey: true});
  assert.equal(m.messages.length, 0);
  let open = true;
  m.win.WsSettingsCombobox = {closeActive() { const was = open; open = false; return was; }};
  assert.equal(m.key().defaultPrevented, true);
  assert.equal(m.messages.length, 0);
  m.doc.dialog = {};
  m.key(); // Native dialog cancel owns this key.
  assert.equal(m.messages.length, 0);
  m.doc.dialog = null;
  m.search.value = "filter";
  m.key({target: m.search});
  assert.equal(m.search.value, "");
  assert.equal(m.search.cleared, true);
  assert.equal(m.messages.length, 0);
  m.key({repeat: true});
  assert.equal(m.messages.length, 0);
  m.key();
  assert.deepEqual(JSON.parse(JSON.stringify(m.messages.pop().message)), {wsShell: "settings-close"});

  const frame = mount(false);
  assert.equal(frame.root["data-settings-modal"], undefined);
  assert.equal(new URL(frame.win.testScopeHref("project"), origin).searchParams.get("target"), "parent");
  frame.key();
  assert.equal(frame.messages.pop().message.key, "esc");
  frame.win.testNavigate("/settings?scope=global");
  assert.equal(frame.messages.pop().message.wsShell, "nav");
});

test("settings combobox offers the pickers' models beside the harnesses, with their availability", async () => {
  const { doc, win, FakeEvent } = createFakeDOM();
  const ctx = {
    window: win,
    document: doc,
    Event: FakeEvent,
    fetch: async () => ({
      ok: true,
      json: async () => ({
        harnesses: [{ harness: "claude", available: true, models: [{ id: "sonnet" }], efforts: ["low"] }],
        pickers: [{ harness: "werk", available: false, error: "no key", models: [{ id: "vendor/m", thinking: true }], efforts: ["low", "high"] }]
      })
    })
  };
  win.window = win;
  vm.runInNewContext(read("dashboard-settings-combobox.js"), ctx);
  const cb = win.WsSettingsCombobox;
  await cb.loadChoices();
  assert.equal(JSON.stringify(cb.backendOptions().map(o => o.id)), JSON.stringify(["claude", "werk"]));
  assert.equal(cb.backendOptions()[1].unavailable, true, "a picker harness that is not available yet says so");
  assert.equal(JSON.stringify(cb.modelOptions("werk")), JSON.stringify([{ id: "openrouter vendor/m", thinking: true }]));
  assert.equal(JSON.stringify(cb.effortOptions("werk").map(e => e.id)), JSON.stringify(["low", "high"]));
});

test("settings combobox asks again when a picker's model list is still being fetched and refreshes the open list", async () => {
  const { doc, win, FakeEvent } = createFakeDOM();
  const answers = [
    { harnesses: [{ harness: "claude", available: true, models: [], efforts: [] }], pickers_pending: true },
    { harnesses: [{ harness: "claude", available: true, models: [], efforts: [] }], pickers: [{ harness: "werk", available: true, models: [{ id: "vendor/m" }, { id: "vendor/n" }], efforts: ["low"] }] }
  ];
  let fetches = 0;
  const timers = [];
  const ctx = {
    window: win,
    document: doc,
    Event: FakeEvent,
    setTimeout: fn => { timers.push(fn); },
    fetch: async () => ({ ok: true, json: async () => answers[Math.min(fetches++, answers.length - 1)] })
  };
  win.window = win;
  vm.runInNewContext(read("dashboard-settings-combobox.js"), ctx);
  const cb = win.WsSettingsCombobox;

  const container = doc.createElement("div");
  doc.body.appendChild(container);
  const input = doc.createElement("input");
  input.className = "inp mono";
  input.setAttribute("aria-label", "werk · Model");
  container.appendChild(input);
  const ctrl = cb.attach(input, { getOptions: () => cb.modelOptions("werk") });

  // The picker is opened while the first, pending answer has no models.
  ctrl.button.click();
  await new Promise(r => setImmediate(r));
  assert.equal(fetches, 1);
  assert.equal(cb.modelOptions("werk").length, 0, "the first answer has no picker yet");
  assert.equal(input.getAttribute("aria-expanded"), "false", "nothing to list yet");
  assert.equal(timers.length, 1, "a pending answer schedules one retry");

  // The retry fires on its own; the open picker re-renders with no further
  // interaction from the test or the user.
  timers.shift()();
  await new Promise(r => setImmediate(r));
  assert.equal(fetches, 2);
  assert.equal(input.getAttribute("aria-expanded"), "true", "the open picker opens once its models arrive");
  assert.deepEqual(ctrl.popup.children.map(li => li.dataset.value), ["openrouter vendor/m", "openrouter vendor/n"]);
  assert.equal(timers.length, 0, "an answer that is not pending schedules no retry");
});


test("tier models distinguish endpoints and preserve inherited providers", async () => {
  const win = {};
  vm.runInNewContext(read("dashboard-settings-combobox.js"), {window: win,
    fetch: async () => ({ok: true, json: async () => ({harnesses: [{harness: "werk",
      model_providers: ["openrouter", "openai", "local/office", "local/home"],
      models: [{id: "same", provider: "local/office"}, {id: "same", provider: "local/home"},
        {id: "same", provider: "openai"}, {id: "same", provider: "openrouter"}]}]})})});
  const cb = win.WsSettingsCombobox;
  await cb.loadChoices();
  assert.deepEqual(Array.from(cb.modelOptions("werk"), m => m.id),
    ["local/office same", "local/home same", "openai same", "openrouter same"]);
  for (const provider of ["local/office", "local/home", "openai", "openrouter"]) {
    assert.equal(cb.modelSelection(cb.modelKey("same", provider)).provider, provider);
    assert.equal(cb.modelSelection("edited-model", provider).provider, provider);
    assert.equal(cb.modelSelection("openrouter same", provider).provider, "openrouter");
  }
});

test("lead model picks retain endpoint identity for spawning and active-lead switching", () => {
  const source = read("dashboard-leads.js");
  const helpers = source.slice(source.indexOf("  function leadModelKey("), source.indexOf("  function leadEfforts("));
  const harness = {model_providers: ["openrouter", "local/office", "local/home"], models: [
    {id: "same"}, {id: "same", provider: "local/office"}, {id: "same", provider: "local/home"}
  ]};
  const ctx = {supHarness: () => harness};
  vm.createContext(ctx);
  vm.runInContext(helpers, ctx);
  assert.deepEqual(Array.from(ctx.leadModels("werk", "", true)), ["same", "local/office same", "local/home same"]);
  for (const provider of ["local/office", "local/home"]) {
    const chosen = ctx.leadModelPick(ctx.leadModelKey("same", provider));
    assert.equal(chosen.provider, provider);
    assert.equal(chosen.model, "same");
    assert.deepEqual(Array.from(ctx.leadModels("werk", provider, false)), ["same"]);
    assert.match(ctx.leadModelLabels("werk", "", true)[provider + " same"], new RegExp(provider));
  }
});
