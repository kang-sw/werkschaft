const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

// Minimal DOM for testing WsSettingsCompanion's pack/VRM/source/zip surfaces
// while recording every tag created to prove pack metadata never becomes markup.
function dom() {
  const doc = { created: [], activeElement: null };
  function element(tag) {
    doc.created.push(String(tag).toLowerCase());
    const el = {
      tagName: String(tag).toUpperCase(),
      className: "",
      textContent: "",
      value: "",
      type: "",
      href: "",
      target: "",
      rel: "",
      accept: "",
      placeholder: "",
      disabled: false,
      checked: false,
      hidden: false,
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
        contains(n) { return el.className.split(/\s+/).includes(n); }
      },
      setAttribute(k, v) { this.attrs[k] = String(v); },
      getAttribute(k) { return Object.hasOwn(this.attrs, k) ? this.attrs[k] : null; },
      removeAttribute(k) { delete this.attrs[k]; },
      addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); },
      append(...items) { this.children.push(...items.filter(Boolean)); },
      prepend(...items) { this.children.unshift(...items.filter(Boolean)); },
      replaceChildren(...items) { this.children = items.filter(Boolean); },
      add(opt) { this.children.push(opt); },
      contains(other) { return other === this || this.all().includes(other); },
      focus() { doc.activeElement = this; },
      click() { fire(this, "click"); },
      all() { return this.children.flatMap(child => typeof child === "string" ? [] : [child, ...(child.all ? child.all() : [])]); },
      text() { return this.textContent + this.children.map(c => typeof c === "string" ? c : c.text()).join(""); }
    };
    return el;
  }
  doc.createElement = element;
  doc.createElementNS = (_ns, tag) => element(tag);
  doc.querySelector = () => null;
  return doc;
}

const fire = (target, type, event = {}) => {
  for (const fn of target.listeners[type] || []) fn({ preventDefault() {}, ...event });
};
const tick = () => new Promise(resolve => setImmediate(resolve));
const byData = (root, prop, val) => root.all().filter(c => val === undefined ? Object.hasOwn(c.dataset, prop) : c.dataset[prop] === val);
const tagged = (root, tag) => root.all().filter(c => c.tagName === tag);

const catalogSpecs = JSON.parse(fs.readFileSync(path.join(__dirname, "assets", "dashboard-settings-catalog.json"), "utf8"));
const specMap = new Map(catalogSpecs.map(s => [s.key, s]));

function setup({
  rawSources = "",
  scope = "global",
  editable = true,
  pack = "auto",
  catalog = { resolvedAuto: { packId: "default" }, packs: [{ id: "default", name: "Default 2D", renderer: "2d", rev: "d1", builtin: true, source: "builtin" }] },
  handler
} = {}) {
  const doc = dom();
  const calls = [], configWrites = [], packsChangedCalls = [], stageActions = [];
  const state = {
    rawSources,
    pack,
    editable,
    scope,
    catalog
  };

  const fetch = async (url, init = {}) => {
    const parsedBody = typeof init.body === "string" ? (() => { try { return JSON.parse(init.body); } catch { return init.body; } })() : init.body;
    calls.push({ url: String(url), init, body: parsedBody });
    if (String(url) === "/api/companion/packs" && (!init.method || init.method === "GET")) {
      return { ok: true, status: 200, json: async () => state.catalog };
    }
    const out = await (handler?.(String(url), init, parsedBody) ?? { status: 200, body: {} });
    return { ok: out.status >= 200 && out.status < 300, status: out.status, json: async () => out.body };
  };

  const win = { innerWidth: 1200, innerHeight: 800, localStorage: { removeItem() {} } };
  const context = vm.createContext({
    window: win, document: doc, URL, URLSearchParams, AbortController, fetch,
    Option: class { constructor(text, value, _ds, selected) { this.tagName = "OPTION"; this.textContent = text; this.value = value; this.selected = !!selected; } }
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", "dashboard-settings-companion.js"), "utf8"), context);
  const mod = win.WsSettingsCompanion;

  const tr = (k, ...a) => a.length ? k + ":" + a.join("|") : k;
  const el = (tag, cls, text) => {
    const node = doc.createElement(tag);
    if (cls) node.className = cls;
    if (text !== undefined && text !== "") node.textContent = String(text);
    return node;
  };
  const append = (parent, ...kids) => { parent.append(...kids); return parent; };
  const button = (cls, text, onClick) => {
    const btn = el("button", cls, text);
    if (onClick) btn.addEventListener("click", onClick);
    return btn;
  };

  let root = null;
  function makeEntries() {
    return catalogSpecs
      .filter(s => s.section === "companion" && (!s.surface || s.surface === "settings"))
      .map(s => {
        let value = "";
        if (s.key === "dashboard.companion_enabled") value = true;
        else if (s.key === "dashboard.companion_director") value = "rules";
        else if (s.key === "dashboard.companion_pack") value = state.pack;
        else if (s.key === "dashboard.companion_sources") value = state.rawSources;
        return { key: s.key, value, default: value, editable: state.editable };
      });
  }

  function render() {
    root = mod.render({
      entries: makeEntries(),
      scope: state.scope,
      filter: "all",
      searchTerm: "",
      tr, el, append, button,
      svgIcon: () => el("svg"),
      specFor: k => specMap.get(k),
      selected: e => e.value,
      editable: () => state.editable,
      matches: () => true,
      renderRow: e => { const r = el("article", "krow"); r.dataset.configKey = e.key; return r; },
      renderKeyProvenance: () => ({ node: el("span"), drawer: null }),
      submitConfig: async (entry, action, value) => {
        configWrites.push({ key: entry.key, action, value });
        if (entry.key === "dashboard.companion_sources") state.rawSources = action === "reset" ? "" : value;
        if (entry.key === "dashboard.companion_pack") state.pack = action === "reset" ? "auto" : value;
        return true;
      },
      rerender: render,
      notifyPacksChanged: () => packsChangedCalls.push(true),
      notifyStageAction: action => stageActions.push(action)
    });
    return root;
  }

  render();
  return {
    doc, mod, state, calls, configWrites, packsChangedCalls, stageActions,
    render,
    get root() { return root; }
  };
}

const listing = (over = {}) => ({
  source: "https://example.invalid/packs.git", kind: "git", official: false, commit: "0123456789abcdef0123", name: "Example Packs",
  owner: { name: "Example Author", url: "https://example.invalid/" }, packs: [], errors: [], ...over
});
const pack = (id, status, over = {}) => ({ id, name: "Pack " + id, version: "1.0.0", author: "Author", license: "CC-BY-4.0", description: "About " + id, status, ...over });

test("add and remove sources validate https origins, enforce 16-source limit, and use inline confirmation", async () => {
  const a = "https://example.invalid/a.git", b = "https://example.invalid/b.zip";
  const t = setup({ rawSources: `${a} ${b}` });
  await tick();

  assert.equal(byData(t.root, "companionSourceItem").length, 3, "official plus two custom sources");

  // Live origin preview and adding a valid source.
  const input = byData(t.root, "companionSourceInput")[0];
  const form = tagged(t.root, "FORM")[0];
  input.value = "  https://example.invalid/c.git ";
  fire(input, "input");
  assert.match(t.root.text(), /js\.settings\.companion\.sources\.preview:https:\/\/example\.invalid\/c\.git/);
  fire(form, "submit");
  await tick();
  assert.deepEqual(t.configWrites.at(-1), { key: "dashboard.companion_sources", action: "set", value: `${a} ${b} https://example.invalid/c.git` });

  // Duplicate and non-https URLs are rejected without writing.
  const writesBefore = t.configWrites.length;
  byData(t.root, "companionSourceInput")[0].value = b;
  fire(tagged(t.root, "FORM")[0], "submit");
  await tick();
  assert.equal(t.configWrites.length, writesBefore);
  assert.match(t.root.text(), /js\.settings\.companion\.sources\.duplicate/);

  byData(t.root, "companionSourceInput")[0].value = "http://example.invalid/insecure.git";
  fire(tagged(t.root, "FORM")[0], "submit");
  await tick();
  assert.equal(t.configWrites.length, writesBefore);
  assert.match(t.root.text(), /js\.settings\.companion\.sources\.invalid_url/);

  // Removing a source requires inline 2-step confirmation.
  const rmBtn = byData(t.root, "companionSourceRemove", a)[0];
  rmBtn.click();
  const confirmYes = t.root.all().find(c => c.tagName === "BUTTON" && c.textContent === "js.settings.companion.packs.confirm");
  assert.ok(confirmYes, "remove shows inline confirmation");
  confirmYes.click();
  await tick();
  assert.deepEqual(t.configWrites.at(-1), { key: "dashboard.companion_sources", action: "set", value: `${b} https://example.invalid/c.git` });

  // 16-source cap refuses a 17th entry.
  const full = Array.from({ length: 16 }, (_, i) => `https://example.invalid/s${i}.git`).join(" ");
  const tFull = setup({ rawSources: full });
  await tick();
  byData(tFull.root, "companionSourceInput")[0].value = "https://example.invalid/extra.git";
  fire(tagged(tFull.root, "FORM")[0], "submit");
  await tick();
  assert.equal(tFull.configWrites.length, 0);
  assert.match(tFull.root.text(), /js\.settings\.companion\.sources\.limit/);
});

test("marketplace and pack metadata renders as plain text, never markup", async () => {
  const hostile = "<img src=x onerror=alert(1)>";
  const t = setup({
    rawSources: "https://example.invalid/packs.git",
    handler: () => ({
      status: 200,
      body: {
        listing: listing({
          name: hostile,
          owner: { name: hostile, url: "javascript:alert(1)" },
          packs: [
            pack("evil", "available", { name: hostile, description: hostile, version: hostile }),
            pack("oops", "available", { error: { id: "oops", path: "pack.json", message: hostile } })
          ]
        })
      }
    })
  });
  await tick();

  const before = t.doc.created.length;
  byData(t.root, "companionSourceFetch", "https://example.invalid/packs.git")[0].click();
  await tick(); await tick();
  const created = t.doc.created.slice(before);
  assert.ok(!created.some(tag => ["img", "script", "iframe"].includes(tag)), "no injected tags from pack text: " + created);
  assert.ok(t.root.text().includes(hostile), "hostile string is displayed safely as text");
  assert.equal(byData(t.root, "companionSourceInstall", "oops").length, 0, "erroring pack offers no install button");
});

test("each pack status maps cleanly to its badge or action button and install/uninstall notify shell", async () => {
  const origin = { source: "https://example.invalid/packs.git", kind: "git", version: "1.0.0", official: false };
  const freshCatalog = {
    resolvedAuto: { packId: "default" },
    packs: [
      { id: "default", name: "Default", renderer: "2d", builtin: true, source: "builtin" },
      { id: "avail", name: "Pack avail", renderer: "2d", origin }
    ]
  };
  const t = setup({
    rawSources: "https://example.invalid/packs.git",
    handler: (url) => {
      if (url.endsWith("/sources/fetch")) {
        return {
          status: 200,
          body: {
            listing: listing({
              packs: [
                pack("avail", "available"),
                pack("inst", "installed"),
                pack("upd", "update"),
                pack("other", "other"),
                pack("manual", "manual"),
                pack("builtin", "builtin")
              ]
            })
          }
        };
      }
      if (url.endsWith("/sources/install")) {
        t.state.catalog = freshCatalog;
        return { status: 200, body: { catalog: freshCatalog, listing: listing({ packs: [pack("avail", "installed", { installed: true })] }) } };
      }
      if (url.endsWith("/uninstall")) {
        t.state.catalog = { resolvedAuto: { packId: "default" }, packs: [freshCatalog.packs[0]] };
        return { status: 200, body: t.state.catalog };
      }
      return { status: 500, body: {} };
    }
  });
  await tick();

  byData(t.root, "companionSourceFetch", "https://example.invalid/packs.git")[0].click();
  await tick(); await tick();

  const cardText = id => byData(t.root, "companionCatalogPack", id)[0].text();
  assert.equal(byData(t.root, "companionSourceInstall", "avail").length, 1);
  assert.equal(byData(t.root, "companionSourceInstall", "upd").length, 1);
  assert.equal(byData(t.root, "companionSourceInstall", "other").length, 1);
  assert.equal(byData(t.root, "companionSourceInstall", "inst").length, 0);
  assert.equal(byData(t.root, "companionSourceInstall", "manual").length, 0);
  assert.equal(byData(t.root, "companionSourceInstall", "builtin").length, 0);
  assert.match(cardText("inst"), /js\.settings\.companion\.sources\.installed/);
  assert.match(cardText("manual"), /js\.settings\.companion\.badge\.local/);
  assert.match(cardText("builtin"), /js\.settings\.companion\.badge\.builtin/);

  // Install avail -> catalog updates & shell notified.
  byData(t.root, "companionSourceInstall", "avail")[0].click();
  await tick(); await tick();
  assert.deepEqual(t.calls.at(-1).body, { source: "https://example.invalid/packs.git", ids: ["avail"] });
  assert.equal(t.packsChangedCalls.length, 1);
  assert.equal(byData(t.root, "companionPackRow", "avail").length, 1);

  // Inline 2-step delete uninstalls avail.
  byData(t.root, "companionPackDelete", "avail")[0].click();
  byData(t.root, "companionPackConfirmYes", "avail")[0].click();
  await tick(); await tick();
  assert.equal(t.calls.at(-1).url, "/api/companion/packs/avail/uninstall");
  assert.equal(t.packsChangedCalls.length, 2);
  assert.equal(byData(t.root, "companionPackRow", "avail").length, 0);
});

test("advanced .zip import validates, re-sends selected IDs, and reports partial 200 errors", async () => {
  const file = { name: "Fan Pack.zip", size: 3 };
  let partialMode = false;
  const t = setup({
    handler: (url) => {
      if (url.startsWith("/api/companion/packs/validate")) {
        return {
          status: 200,
          body: {
            listing: listing({
              source: "Fan Pack.zip", kind: "local", name: "Fan",
              packs: [pack("a", "available"), pack("b", "available"), pack("bad", "available", { error: { id: "bad", path: "pack.json", message: "no poses.stand" } })]
            })
          }
        };
      }
      if (partialMode) {
        return {
          status: 200,
          body: {
            listing: listing({
              kind: "local",
              packs: [
                pack("a", "installed", { installed: true }),
                pack("b", "available", { error: { id: "b", path: "", message: "cannot move staged pack" } })
              ]
            })
          }
        };
      }
      return {
        status: 200,
        body: {
          listing: listing({ kind: "local", packs: [pack("a", "installed", { installed: true })] }),
          catalog: { resolvedAuto: { packId: "default" }, packs: [{ id: "default", builtin: true, renderer: "2d" }, { id: "a", renderer: "2d" }] }
        }
      };
    }
  });
  await tick();

  // Open Advanced accordion.
  byData(t.root, "companionAdvancedToggle")[0].click();
  const zipInput = byData(t.root, "companionZipInput")[0];
  assert.equal(zipInput.accept, ".zip,application/zip");
  zipInput.files = [file];
  fire(zipInput, "change");
  await tick(); await tick();
  assert.equal(t.calls.at(-1).url, "/api/companion/packs/validate?name=Fan%20Pack.zip");

  const checkboxes = tagged(t.root, "INPUT").filter(c => c.type === "checkbox" && !c.dataset.companionAutoCheck);
  // First checkbox is vrm auto-activate; zip checkboxes are the last 3.
  const zipBoxes = checkboxes.slice(-3);
  assert.deepEqual(zipBoxes.map(b => [b.checked, b.disabled]), [[true, false], [true, false], [false, true]]);

  // Uncheck b, import only a.
  zipBoxes[1].checked = false;
  fire(zipBoxes[1], "change");
  byData(t.root, "companionZipImport")[0].click();
  await tick(); await tick();
  assert.equal(t.calls.at(-1).url, "/api/companion/packs/import?name=Fan%20Pack.zip&id=a");
  assert.equal(t.calls.at(-1).init.body, file);
  assert.match(t.root.text(), /js\.settings\.companion\.advanced\.zip_imported:1/);

  // Partial failure in 200 response reports error alert, not success.
  partialMode = true;
  const zipInput2 = byData(t.root, "companionZipInput")[0];
  zipInput2.files = [file];
  fire(zipInput2, "change");
  await tick(); await tick();
  byData(t.root, "companionZipImport")[0].click();
  await tick(); await tick();
  const alert = t.root.all().find(c => c.attrs.role === "alert");
  assert.ok(alert, "partial import failure shows an alert");
  assert.match(alert.text(), /b: cannot move staged pack/);
  assert.match(alert.text(), /1\/2/);
});

test("local .vrm file picker and drag-and-drop call import-vrm and auto-activate", async () => {
  const picked = { name: "My Avatar.vrm", size: 128 };
  const dropped = { name: "Second.vrm", size: 256 };
  const t = setup({
    handler: (url) => {
      if (url.startsWith("/api/companion/packs/import-vrm")) {
        const isSecond = url.includes("Second");
        const entry = { id: isSecond ? "second" : "my-avatar", name: isSecond ? "Second" : "My Avatar", renderer: "vrm", rev: "abc", origin: { kind: "local", source: isSecond ? "Second.vrm" : "My Avatar.vrm" } };
        t.state.catalog = { resolvedAuto: { packId: entry.id }, packs: [...t.state.catalog.packs, entry] };
        return { status: 200, body: entry };
      }
      return { status: 404, body: {} };
    }
  });
  await tick();

  // Official VRM banner is shown while no VRM pack is installed.
  assert.equal(byData(t.root, "companionOfficialVrmBanner").length, 1);

  const vrmInput = byData(t.root, "companionVrmInput")[0];
  assert.equal(vrmInput.accept, ".vrm");
  vrmInput.files = [picked];
  fire(vrmInput, "change");
  await tick(); await tick();
  const vrmCall = t.calls.find(c => c.url.startsWith("/api/companion/packs/import-vrm"));
  assert.ok(vrmCall);
  assert.equal(vrmCall.url, "/api/companion/packs/import-vrm?name=My%20Avatar&filename=My%20Avatar.vrm");
  assert.equal(vrmCall.init.headers["Content-Type"], "model/gltf-binary");
  assert.equal(vrmCall.init.body, picked);
  assert.deepEqual(t.configWrites.at(-1), { key: "dashboard.companion_pack", action: "set", value: "my-avatar" });
  assert.equal(byData(t.root, "companionOfficialVrmBanner").length, 0, "official VRM banner hides once a VRM pack is installed");

  // Non-.vrm drop is rejected locally without network calls.
  const callsBefore = t.calls.length;
  const dropzone = byData(t.root, "companionVrmDropzone")[0];
  fire(dropzone, "dragover");
  assert.equal(dropzone.dataset.drag, "over");
  fire(dropzone, "drop", { dataTransfer: { files: [{ name: "notes.txt" }] } });
  await tick();
  assert.equal(t.calls.length, callsBefore);
  assert.match(t.root.text(), /js\.settings\.companion\.vrm\.invalid_ext/);

  // Dropping a .vrm uploads and activates it.
  fire(byData(t.root, "companionVrmDropzone")[0], "drop", { dataTransfer: { files: [dropped] } });
  await tick(); await tick();
  assert.ok(t.calls.some(c => c.url === "/api/companion/packs/import-vrm?name=Second&filename=Second.vrm"));
  assert.deepEqual(t.configWrites.at(-1), { key: "dashboard.companion_pack", action: "set", value: "second" });
});

test("read-only or non-global scope disables pack/source mutation controls while keeping authoring doc links", async () => {
  const t = setup({
    scope: "repo",
    editable: false,
    rawSources: "https://example.invalid/packs.git"
  });
  await tick();
  assert.equal(byData(t.root, "companionVrmChoose")[0].disabled, true);
  assert.equal(byData(t.root, "companionSourceInput")[0].disabled, true);
  assert.equal(byData(t.root, "companionSourceAdd")[0].disabled, true);
  assert.equal(byData(t.root, "companionSourceRemove").length, 0);

  byData(t.root, "companionAdvancedToggle")[0].click();
  assert.equal(byData(t.root, "companionZipChoose")[0].disabled, true);
  const links = tagged(t.root, "A").map(a => ({ href: a.href, rel: a.rel, target: a.target }));
  assert.deepEqual(links, [
    { href: "/api/companion/authoring/guide.md", rel: "noopener noreferrer", target: "_blank" },
    { href: "/api/companion/authoring/pack.schema.json", rel: "noopener noreferrer", target: "_blank" },
    { href: "/api/companion/authoring/marketplace.schema.json", rel: "noopener noreferrer", target: "_blank" }
  ]);
});

test("companion disabled runs zero WebGL contexts and zero extra fetches", async () => {
  let webglCalls = 0;
  const fetches = [];
  let mountCalls = 0;
  let disposeCalls = 0;
  function stubEl(tag) {
    const children = [];
    const node = {
      tagName: String(tag || "div").toUpperCase(),
      className: "", textContent: "", title: "", type: "", value: "",
      style: { setProperty() {} }, dataset: {}, hidden: false, open: false,
      children,
      classList: { toggle() {}, add() {}, remove() {}, contains: () => false },
      getContext(kind) {
        if (String(kind).includes("webgl")) { webglCalls++; return null; }
        return {
          clearRect() {}, save() {}, restore() {}, scale() {}, translate() {}, rotate() {},
          setTransform() {}, drawImage() {}, fillRect() {}, beginPath() {}, closePath() {},
          arc() {}, fill() {}, stroke() {}
        };
      },
      setAttribute() {}, removeAttribute() {}, getAttribute: () => null, hasAttribute: () => false,
      addEventListener() {}, removeEventListener() {}, contains: () => false,
      append(...items) { children.push(...items); }, replaceChildren(...items) { children.length = 0; children.push(...items); },
      remove() {}, close() {}, show() { node.open = true; },
      querySelector: () => null, querySelectorAll: () => [],
      getBoundingClientRect: () => ({ left: 0, top: 0, right: 120, bottom: 160, width: 120, height: 160 })
    };
    return node;
  }
  const stageDoc = {
    documentElement: { hasAttribute: () => true, dataset: {} },
    body: stubEl("body"),
    hidden: false,
    visibilityState: "visible",
    activeElement: null,
    createElement: stubEl,
    createElementNS: (_ns, tag) => stubEl(tag),
    getElementById: () => null,
    querySelector: () => null,
    querySelectorAll: () => [],
    addEventListener() {},
    removeEventListener() {}
  };
  const win = {
    innerWidth: 1200, innerHeight: 800, devicePixelRatio: 1,
    location: { origin: "http://127.0.0.1" },
    localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
    matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
    addEventListener() {}, removeEventListener() {},
    WsShell: { companionContext: () => ({ routeKind: "home", hasComposer: false }) }
  };
  const context = vm.createContext({
    window: win, document: stageDoc, URLSearchParams, AbortController,
    performance: { now: () => 0 },
    ResizeObserver: class { observe() {} disconnect() {} },
    MutationObserver: class { observe() {} disconnect() {} },
    requestAnimationFrame: () => 0,
    cancelAnimationFrame() {},
    setTimeout: () => 1, clearTimeout() {},
    fetch: async url => {
      fetches.push(String(url));
      return { ok: true, json: async () => ({ defaultPack: "default", packs: [] }) };
    },
    wsT: k => k
  });
  for (const name of ["builtins", "packs", "focus", "deform", "compositor", "scenarios", "menu", "vrm-fx", "vrm-studio", "vrm-props", "vrm-pose", "vrm-colead", "vrm", "stage"]) {
    vm.runInContext(fs.readFileSync(path.join(__dirname, "assets", `dashboard-companion-${name}.js`), "utf8"), context);
  }
  const origMount = context.window.WsCompanion.mount;
  context.window.WsCompanion.mount = function (...args) {
    mountCalls++;
    const handle = origMount.apply(this, args);
    const origDispose = handle.dispose;
    handle.dispose = function () { disposeCalls++; return origDispose.apply(this, arguments); };
    return handle;
  };
  const lifecycleSrc = fs.readFileSync(path.join(__dirname, "assets", "dashboard-lifecycle.js"), "utf8");
  const start = lifecycleSrc.indexOf("  var shCompanion = null");
  const end = lifecycleSrc.indexOf("  function shHistoryIndex()");
  assert.ok(start > 0 && end > start, "dashboard-lifecycle.js companion gate block found");
  const gateBlock = lifecycleSrc.slice(start, end);
  vm.runInContext(`var shWant = null, shOn = null; function shSend() {}\n${gateBlock}\nwindow.__testConfigure = shCompanionConfigure;`, context);

  // Disabled config: real shCompanionConfigure must not mount, fetch, or create WebGL contexts.
  context.window.__testConfigure({ enabled: false, pack: "default" });
  await tick();
  assert.equal(mountCalls, 0, "companion_enabled=false must not mount WsCompanion");
  assert.equal(fetches.length, 0, "companion_enabled=false must perform zero fetches");
  assert.equal(webglCalls, 0, "companion_enabled=false must create zero WebGL contexts");

  // Positive control: enabling through the same gate mounts and fetches the pack catalog, still with zero WebGL.
  context.window.__testConfigure({ enabled: true, pack: "default" });
  await tick();
  assert.equal(mountCalls, 1, "companion_enabled=true mounts WsCompanion through the same gate");
  assert.ok(fetches.some(url => url.includes("/api/companion/packs")), "enabled companion fetches pack catalog");
  assert.equal(webglCalls, 0, "2D default companion creates zero WebGL contexts");

  // Toggling back to disabled disposes the mounted stage.
  const fetchesBeforeDisable = fetches.length;
  context.window.__testConfigure({ enabled: false, pack: "default" });
  await tick();
  assert.equal(disposeCalls, 1, "toggling companion_enabled=false disposes mounted stage");
  assert.equal(fetches.length, fetchesBeforeDisable, "disabling triggers no extra fetches");
});
