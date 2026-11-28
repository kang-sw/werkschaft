const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { readAssetSource } = require('./testdata/asset-source.cjs');

const ASSETS = path.join(__dirname, 'assets');
const readAsset = name => fs.readFileSync(path.join(ASSETS, name), 'utf8');
const USAGE_SOURCE = readAssetSource('wsdashboard', 'usage.js');

function makeClassList(initial = '') {
  const set = new Set(initial.split(/\s+/).filter(Boolean));
  return {
    add(c) { set.add(c); },
    remove(c) { set.delete(c); },
    toggle(c, on) {
      const next = on === undefined ? !set.has(c) : !!on;
      if (next) set.add(c); else set.delete(c);
      return next;
    },
    contains(c) { return set.has(c); },
    toString() { return [...set].join(' '); }
  };
}

function usageEnv({ now = Date.UTC(2026, 10, 15, 12, 0), withResizeObserver = false, withRaf = false, initialClientWidth = 0, switcherOn = false } = {}) {
  class Element {
    constructor(tag) {
      this.tagName = tag;
      this.children = [];
      this.parentNode = null;
      this.style = { setProperty(k, v) { this[k] = v; } };
      this.attributes = {};
      this.events = {};
      this._text = '';
      this.className = '';
      this.hidden = false;
      this._clientWidth = null;
      this._offsetWidth = 680;
      this._offsetHeight = 420;
      this.textClearCount = 0;
    }
    get clientWidth() {
      if (this._clientWidth !== null) return this._clientWidth;
      if (this.hidden) return 0;
      return initialClientWidth;
    }
    set clientWidth(v) { this._clientWidth = v; }
    get offsetWidth() { return this.hidden ? 0 : this._offsetWidth; }
    set offsetWidth(v) { this._offsetWidth = v; }
    get offsetHeight() { return this.hidden ? 0 : this._offsetHeight; }
    set offsetHeight(v) { this._offsetHeight = v; }
    set textContent(value) {
      if (value === '') this.textClearCount++;
      this._text = value;
      this.children = [];
    }
    get textContent() { return this._text + this.children.map(c => c.textContent).join(''); }
    appendChild(child) { child.parentNode = this; this.children.push(child); return child; }
    setAttribute(key, value) { this.attributes[key] = String(value); if (key === 'class') this.className = String(value); }
    getAttribute(key) { return Object.prototype.hasOwnProperty.call(this.attributes, key) ? this.attributes[key] : null; }
    addEventListener(key, fn) { (this.events[key] ||= []).push(fn); }
    emit(key, ev) { (this.events[key] || []).forEach(fn => fn(ev)); }
    focus() { this.focused = true; }
    querySelector(selector) {
      const cls = selector.startsWith('.') ? selector.slice(1) : null;
      for (const child of this.children) {
        if (cls ? (child.className || '').split(/\s+/).includes(cls) : child.tagName === selector) return child;
        const found = child.querySelector(selector);
        if (found) return found;
      }
      return null;
    }
  }

  const button = new Element('button');
  button.className = 'usage-toggle';
  const top = new Element('div');
  top.className = 'top';
  top.appendChild(button);
  const doc = {
    hidden: false,
    events: {},
    createElement: tag => new Element(tag),
    createElementNS: (_, tag) => new Element(tag),
    createTextNode: text => { const n = new Element('#text'); n._text = String(text); return n; },
    addEventListener(key, fn) { this.events[key] = fn; }
  };

  let payload = { entries: [], warnPercent: 80, critPercent: 95 };
  let accountPayload = { loopback_peer: true, slots: {}, conversations: [] };
  const observers = [], timers = [], rafs = [];
  const window = {
    setInterval() {},
    setTimeout(fn, ms) { const rec = { fn, ms, live: true }; timers.push(rec); return timers.length; },
    clearTimeout(id) { if (timers[id - 1]) timers[id - 1].live = false; },
    fetch(url) {
      if (String(url).includes('/api/account/status')) {
        return Promise.resolve({ ok: true, json: () => Promise.resolve(accountPayload) });
      }
      return Promise.resolve({ ok: true, json: () => Promise.resolve(payload) });
    }
  };
  if (withRaf) {
    window.requestAnimationFrame = fn => {
      const rec = { fn, cancelled: false };
      rafs.push(rec);
      return rafs.length;
    };
    window.cancelAnimationFrame = id => {
      if (rafs[id - 1]) rafs[id - 1].cancelled = true;
    };
  }
  if (withResizeObserver) {
    window.ResizeObserver = class ResizeObserver {
      constructor(cb) { this.cb = cb; this.target = null; observers.push(this); }
      observe(target) { this.target = target; }
    };
  }
  window.wsI18n = { lang: 'en', t: JSON.parse(fs.readFileSync(path.join(__dirname, '../wsi18n/tables/en.json'), 'utf8')) };
  window.wsT = (key, ...args) => window.wsI18n.t[key].replace(/\{(\d+)\}/g, (m, i) => (i < args.length ? String(args[i]) : m));
  if (switcherOn) window.wsAccountSwitcher = true;
  class Clock extends Date { static now() { return now; } }
  vm.runInNewContext(USAGE_SOURCE, { window, Date: Clock });
  return {
    window,
    doc,
    top,
    button,
    observers,
    timers,
    rafs,
    now(v) { now = v; },
    set(v) { payload = v; },
    setAccount(v) { accountPayload = v; }
  };
}

const flush = () => new Promise(resolve => setImmediate(resolve));

test('Bundle A Item 1: live dashboard overlays remove backdrop-filter and use tonal surfaces', () => {
  const conv = readAsset('dashboard-conversation.css');
  const ticketmap = readAsset('dashboard-ticketmap.css');
  const rail = readAsset('dashboard-rail.css');
  const newSession = readAsset('dashboard-new-session.css');

  for (const [name, css] of [
    ['dashboard-conversation.css', conv],
    ['dashboard-ticketmap.css', ticketmap],
    ['dashboard-rail.css', rail],
    ['dashboard-new-session.css', newSession]
  ]) {
    assert.doesNotMatch(css, /backdrop-filter\s*:/, `${name} must not use backdrop-filter`);
  }

  assert.match(conv, /\.dgrid\.live > \.agisland\[data-mode="compact"\], \.dgrid\.live > \.agisland\[data-mode="idle"\]\s*\{[^}]*background:\s*color-mix\(in srgb, var\(--wb-fg\) 7%, var\(--wb-card\)\)/);
  assert.match(ticketmap, /\.d-tm-ctip\s*\{[^}]*background:\s*var\(--wb-card\)/);
  assert.match(rail, /\.d-board-dial\[data-scrub\]\s*\{[^}]*background:\s*color-mix\(in srgb, var\(--wb-card\) 94%, var\(--wb-bg\)\)/);
  assert.match(newSession, /\.ns-scrim\s*\{[^}]*background:\s*color-mix\(in srgb, #000 42%, transparent\)/);
});

test('Bundle A Item 2: html.rpop-open toggles cleanly on popover open/close and pauses decorative CSS animations', () => {
  const baseCss = readAsset('dashboard.css');
  const convCss = readAsset('dashboard-conversation.css');
  const railCss = readAsset('dashboard-rail.css');

  assert.match(baseCss, /html\.rpop-open :is\([^)]*\.spin[^)]*\.pulse[^)]*\.ibadge\.busy \.istop[^)]*\.hb \.hbr[^)]*\)\s*\{\s*animation-play-state:\s*paused;\s*\}/);
  assert.match(baseCss, /html\.rpop-open \.hud-node\[data-state\] \.hud-ring\s*\{\s*animation-play-state:\s*paused;\s*\}/);
  assert.match(baseCss, /html\.rpop-open:not\(\[data-animation-paused\]\) \.rpop :is\(\.spin, \.pulse\)\s*\{\s*animation-play-state:\s*running;\s*\}/);
  assert.match(convCss, /html\.rpop-open \.tt-cmp \.d-now::before\s*\{\s*animation-play-state:\s*paused;\s*\}/);
  assert.match(railCss, /html\.rpop-open \.rail \.rs\.attn::before,\s*html\.rpop-open \.rail \.rs\.attn \.hb::after\s*\{\s*animation-play-state:\s*paused;\s*\}/);

  const popJs = readAsset('dashboard-rail-popovers.js');
  const syncBlock = popJs.match(/\n  function railPopSync\([\s\S]*?\n  function railPopRootState\([\s\S]*?\n  \}/)[0];
  const openBlock = popJs.match(/\n  function railConfirmOpen\([\s\S]*?\n  function railPopOpen\([\s\S]*?\n  \}/)[0];
  const rowMenuBlock = popJs.match(/\n  function railRowMenu\([\s\S]*?\n  \}/)[0];

  const root = { classList: makeClassList(), setAttribute() {}, removeAttribute() {} };
  const makeBtn = () => ({
    attrs: {},
    setAttribute(k, v) { this.attrs[k] = String(v); },
    getAttribute(k) { return this.attrs[k] || null; },
    closest() { return null; },
    focus() {},
    getClientRects() { return [1]; }
  });
  const btn = makeBtn();
  const popEl = { hidden: true, parentNode: {}, focus() {} };
  const mkPop = () => ({ el: popEl, open() {}, close() {}, focus() {} });
  const ctx = {
    window: {},
    doc: { body: { appendChild() {} } },
    root,
    railEl: { appendChild() {} },
    rp: { P: null, kind: '', ws: '', btn: null, placeClean: false },
    rpConf: mkPop(),
    rpRow: mkPop(),
    gmenu: mkPop(),
    rpKey: (k) => k + '-key',
    rpMake: () => mkPop(),
    railPopBtn: () => btn,
    railPopPlace() {},
    railPopScrim() {},
    railOpen() {},
    permFullDrop() {},
    layoutMode: 'auto',
    inputMode: 'enter',
    setLayout() {},
    setInput() {},
    markTheme() {},
    markLang() {}
  };
  vm.createContext(ctx);
  vm.runInContext(syncBlock + '\n' + openBlock + '\n' + rowMenuBlock, ctx);

  assert.equal(root.classList.contains('rpop-open'), false);
  ctx.railPopOpen('settings', btn);
  assert.equal(root.classList.contains('rpop-open'), true);
  ctx.railPopClose(false);
  assert.equal(root.classList.contains('rpop-open'), false);

  ctx.railConfirmOpen({ rid: 'r1' }, btn);
  assert.equal(root.classList.contains('rpop-open'), true);
  ctx.railPopClose(false);
  assert.equal(root.classList.contains('rpop-open'), false);

  const rowEl = Object.assign(makeBtn(), {
    attrs: { 'data-rid': 'r1' },
    classList: makeClassList(),
    querySelector: sel => (sel === '.rl' ? btn : null),
    getBoundingClientRect: () => ({ left: 0, top: 0 })
  });
  ctx.railRowMenu(rowEl, { x: 1, y: 2 });
  assert.equal(root.classList.contains('rpop-open'), true);
  ctx.railPopClose(false);
  assert.equal(root.classList.contains('rpop-open'), false);
});

test('Bundle A Item 3: usage.css isolates .usage-pop and eliminates SVG opacity transitions; open defers hidden draw so ResizeObserver renders once', async () => {
  const usageCss = readAsset('usage.css');
  assert.match(usageCss, /\.rpop\.usage-pop,\s*\.usage-pop\s*\{[^}]*contain:\s*layout paint style;[^}]*isolation:\s*isolate;/);
  for (const sel of ['.usage-area', '.usage-excess', '.up-ser', '.up-ser .up-fxd', '.up-pin', '.up-leader', '.up-gl', '.up-hr']) {
    const escaped = sel.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    const rule = usageCss.match(new RegExp(escaped + '\\s*\\{[^}]*\\}'));
    if (rule) assert.doesNotMatch(rule[0], /opacity\s+\.14s/, `${sel} should not transition opacity`);
  }
  assert.doesNotMatch(usageCss, /\.up-ser\s*\{/);

  const start = Date.UTC(2026, 10, 15, 8, 0), dur = 5 * 3600000, asOf = start + 0.4 * dur;
  const env = usageEnv({ now: asOf, withResizeObserver: true, initialClientWidth: 680 });
  env.set({
    entries: [
      { harness: 'claude', asOf, windows: [{ kind: 'five_hour', percent: 40, asOf, resetsAt: start + dur }, { kind: 'seven_day', percent: 55, asOf, resetsAt: asOf + 3 * 86400000 }] }
    ],
    history: {
      schema_version: 1, status: 'ok', samples: [
        { harness: 'claude', kind: 'five_hour', resetsAt: start + dur, asOf: start + 0.2 * dur, percent: 15 },
        { harness: 'claude', kind: 'five_hour', resetsAt: start + dur, asOf, percent: 40 }
      ]
    }
  });

  let placeCount = 0;
  const mounted = env.window.WsUsage.mount(env.doc, env.top);
  await flush();
  const pop = mounted.makePopover(() => { placeCount++; }, () => {});
  const chartsBox = pop.el.querySelector('.up-charts');
  assert.ok(chartsBox);

  // Simulate railPopOpen lifecycle: hidden=true during P.open(), then hidden=false, then ResizeObserver fires at 680px.
  pop.el.hidden = true;
  pop.open();
  assert.equal(chartsBox.textClearCount, 0, 'hidden open defers drawCharts until ResizeObserver measures real width');
  assert.equal(env.observers.length, 1);

  pop.el.hidden = false;
  env.observers[0].cb();
  assert.equal(chartsBox.textClearCount, 1, 'first-paint ResizeObserver draws charts once');
  const placesAfterOpen = placeCount;

  // Redundant ResizeObserver callbacks at the same or <4px width do nothing.
  pop.el.clientWidth = 682;
  env.observers[0].cb();
  assert.equal(chartsBox.textClearCount, 1, 'jitter <4px does not redraw charts');
  assert.equal(placeCount, placesAfterOpen);

  // Resolving refresh(true) draws once and updates lastChartW; a subsequent ResizeObserver at same width does not redraw.
  await flush();
  assert.equal(chartsBox.textClearCount, 2, 'post-refresh drawPopover updates charts once');
  env.observers[0].cb();
  assert.equal(chartsBox.textClearCount, 2, 'ResizeObserver after visible draw does not double-render');

  // Real resize (>=4px) schedules a debounced redraw.
  pop.el.clientWidth = 520;
  env.observers[0].cb();
  const rzTimer = env.timers.find(t => t.live && t.ms === 60);
  assert.ok(rzTimer, 'real width change arms 60ms resize redraw');
  rzTimer.fn();
  assert.equal(chartsBox.textClearCount, 3);

  pop.close();
});

test('Bundle A Item 4a: drawFooter skips DOM rebuild when signature is unchanged and rebuilds on change or external wipe', async () => {
  const at = Date.UTC(2026, 10, 15, 12, 0), reset = at + 3600000;
  const env = usageEnv({ now: at });
  const entry = {
    harness: 'claude',
    asOf: at,
    windows: [{ kind: 'five_hour', percent: 35, resetsAt: reset, asOf: at }]
  };
  env.set({ entries: [entry] });
  const mounted = env.window.WsUsage.mount(env.doc, env.top);
  await flush();

  const clearAfterMount = env.button.textClearCount;
  const firstChildRef = env.button.children[0];
  assert.ok(firstChildRef);

  // Same data & same minute -> sync skips rebuild and preserves DOM identity.
  assert.equal(mounted.sync(), false);
  assert.equal(env.button.textClearCount, clearAfterMount);
  assert.equal(env.button.children[0], firstChildRef);

  // Advancing by 60s changes relative age label -> rebuilds.
  env.now(at + 60000);
  assert.equal(mounted.sync(), true);
  assert.equal(env.button.textClearCount, clearAfterMount + 1);
  assert.notEqual(env.button.children[0], firstChildRef);

  // Unchanged at new minute -> skips again.
  const childAfterAge = env.button.children[0];
  assert.equal(mounted.sync(), false);
  assert.equal(env.button.children[0], childAfterAge);

  // Percent change -> rebuilds.
  entry.windows[0].percent = 42;
  assert.equal(mounted.sync(), true);
  assert.equal(env.button.textClearCount, clearAfterMount + 2);

  // External button wipe (e.g. shell bar swap) -> rebuilds even with identical data.
  env.button.textContent = '';
  assert.equal(mounted.sync(), true);
  assert.ok(env.button.children.length > 0);
});

test('Bundle A Item 4b: railPopSync skips forced layout for stable usage/settings popovers and repositions when dirty or anchor changes', () => {
  const popJs = readAsset('dashboard-rail-popovers.js');
  const placeFn = popJs.match(/\n  function railPopPlace\([\s\S]*?\n  \}/)[0];
  const syncFn = popJs.match(/\n  function railPopSync\([\s\S]*?\n  \}/)[0];
  const scrollSetup = popJs.match(/doc\.addEventListener\("scroll", function \(e\) \{[\s\S]*?\}, true\);/)[0];

  let rectReads = 0, wReads = 0, hReads = 0, placeCalls = 0;
  const el = {
    style: { top: '10px', left: '20px', maxHeight: '300px' },
    get offsetWidth() { wReads++; return 360; },
    get offsetHeight() { hReads++; return 400; }
  };
  const makeAnchor = () => ({
    attrs: { 'aria-expanded': 'true' },
    setAttribute(k, v) { this.attrs[k] = String(v); },
    getAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k) ? this.attrs[k] : null; },
    getBoundingClientRect() { rectReads++; return { top: 777, bottom: 797, left: 1020, right: 1192 }; },
    getClientRects() { return [1]; },
    closest() { return { getBoundingClientRect() { rectReads++; return { top: 100, bottom: 130, left: 0, right: 200 }; } }; }
  });

  let activeBtn = makeAnchor();
  const scrollListeners = [];
  const ctx = {
    window: { innerWidth: 1200, innerHeight: 800 },
    root: { clientWidth: 1200, clientHeight: 800 },
    doc: { addEventListener(ev, fn) { if (ev === 'scroll') scrollListeners.push(fn); } },
    botBarEl: { contains(x) { return x === activeBtn; } },
    railEl: { getBoundingClientRect() { return { left: 0, right: 280, width: 280 }; } },
    WsRail: { popPlace: () => ({ top: 371, left: 832 }) },
    rp: { P: { el }, kind: 'usage', ws: '', btn: activeBtn, placeClean: true },
    railPopBtn() { ctx.rp.btn = activeBtn; return activeBtn; },
    railPopClose() { ctx.rp.P = null; },
    hdPlace() {}
  };
  vm.createContext(ctx);
  vm.runInContext(
    placeFn + '\n' + syncFn + '\nvar placeCalls = 0;\nvar _origPlace = railPopPlace;\n' +
    'railPopPlace = function () { placeCalls++; return _origPlace(); };\n' +
    scrollSetup,
    ctx
  );

  // 1. Clean usage popover with same anchor skips railPopPlace and avoids forced layout reads.
  ctx.railPopSync();
  assert.equal(ctx.placeCalls, 0);
  assert.equal(rectReads, 0);
  assert.equal(wReads, 0);
  assert.equal(hReads, 0);

  // 2. Dirty flag (e.g. usageGutter.sync() rebuilt footer) triggers placement and re-arms placeClean.
  ctx.rp.placeClean = false;
  ctx.railPopSync();
  assert.equal(ctx.placeCalls, 1);
  assert.equal(ctx.rp.placeClean, true);
  assert.ok(rectReads > 0 && wReads > 0 && hReads > 0);

  // 3. New anchor button reference (e.g. shell swap) triggers placement even when placeClean was true.
  activeBtn = makeAnchor();
  ctx.railPopSync();
  assert.equal(ctx.placeCalls, 2);
  assert.equal(ctx.rp.placeClean, true);

  // 4. Row popovers always reposition on sync since sibling rows above them may move.
  ctx.rp.kind = 'row';
  ctx.railPopSync();
  assert.equal(ctx.placeCalls, 3);

  // 5. Scroll on an unrelated container does not place; scroll on an ancestor of rp.btn does.
  ctx.rp.kind = 'usage';
  const unrelatedTarget = { contains: () => false };
  scrollListeners[0]({ target: unrelatedTarget });
  assert.equal(ctx.placeCalls, 3);
  const ancestorTarget = { contains: n => n === activeBtn };
  scrollListeners[0]({ target: ancestorTarget });
  assert.equal(ctx.placeCalls, 4);
});

function collectNodes(root, pred) {
  const out = [];
  for (const child of root.children || []) {
    if (pred(child)) out.push(child);
    out.push(...collectNodes(child, pred));
  }
  return out;
}

const hasClass = (el, cls) => (el.className || '').split(/\s+/).includes(cls);

test('Bundle A-2 CSS: zero .12s hover transitions, isolated chart panels/layers/callout, and zero display:none SVG hover rules', () => {
  const usageCss = readAsset('usage.css');
  assert.doesNotMatch(usageCss, /\.12s\b/, 'usage.css must not contain .12s transitions');
  for (const sel of ['.up-as-opt', '.up-ibtn', '.up-sp', '.up-hr', '.up-cbtn']) {
    const escaped = sel.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    const rule = usageCss.match(new RegExp(escaped + '\\s*\\{[^}]*\\}'));
    assert.ok(rule, `${sel} rule exists`);
    assert.doesNotMatch(rule[0], /transition\s*:/, `${sel} must not declare transition`);
  }

  assert.match(usageCss, /\.up-cpanel\s*\{[^}]*contain:\s*layout style;[^}]*transform:\s*translateZ\(0\);/);
  assert.match(usageCss, /\.up-cp-body\s*\{[^}]*contain:\s*layout style;/);
  assert.match(usageCss, /\.up-csvg\s*\{[^}]*overflow:\s*visible;/);
  assert.match(usageCss, /\.up-clayer-base\s*\{[^}]*transform:\s*translateZ\(0\);/);
  assert.match(usageCss, /\.up-clayer\s*\{[^}]*pointer-events:\s*none;[^}]*transform:\s*translateZ\(0\);[^}]*will-change:\s*opacity;/);
  assert.match(usageCss, /\.up-callout\s*\{[^}]*will-change:\s*transform;[^}]*transform:\s*translate3d\(/);

  assert.match(usageCss, /\.up-clayer-focus\s*\{\s*opacity:\s*0;\s*\}/);
  assert.match(usageCss, /\.up-clayer-focus\.is-focus\s*\{\s*opacity:\s*1;\s*\}/);
  assert.match(usageCss, /\.up-clayer-preview\s*\{\s*opacity:\s*0;\s*pointer-events:\s*none;\s*\}/);
  assert.match(usageCss, /\.up-clayer-preview\.is-preview-on\s*\{\s*opacity:\s*1;\s*\}/);
  for (const line of usageCss.split('\n')) {
    if (/\.up-(clayer|pser|ppin|pgl|ser|pin|gl)\b/.test(line)) {
      assert.doesNotMatch(line, /display\s*:\s*(none|inline)\b/, `chart layer rule must not toggle display: ${line}`);
    }
  }
});

test('Bundle A-2 charts & popover: stacked compositor layers, conditional setPreview place(), and rAF-coalesced mouseover', async () => {
  const start = Date.UTC(2026, 10, 15, 8, 0), dur = 5 * 3600000, asOf = start + 0.4 * dur;
  const env = usageEnv({ now: asOf, withRaf: true });
  env.set({
    entries: [
      { harness: 'claude', asOf, windows: [{ kind: 'five_hour', percent: 45, asOf, resetsAt: start + dur }, { kind: 'seven_day', percent: 60, asOf, resetsAt: asOf + 3 * 86400000 }] },
      { harness: 'codex', asOf, windows: [{ kind: 'five_hour', percent: 25, asOf, resetsAt: start + dur }, { kind: 'seven_day', percent: 30, asOf, resetsAt: asOf + 4 * 86400000 }] }
    ],
    history: {
      schema_version: 1, status: 'ok', samples: [
        { harness: 'claude', kind: 'five_hour', resetsAt: start + dur, asOf: start + 0.2 * dur, percent: 20 },
        { harness: 'claude', kind: 'five_hour', resetsAt: start + dur, asOf, percent: 45 },
        { harness: 'codex', kind: 'five_hour', resetsAt: start + dur, asOf: start + 0.2 * dur, percent: 10 },
        { harness: 'codex', kind: 'five_hour', resetsAt: start + dur, asOf, percent: 25 }
      ]
    },
    slotPreviews: {
      claude: {
        work: {
          asOf: asOf - 60000,
          windows: [{ kind: 'five_hour', percent: 70, asOf: asOf - 60000, resetsAt: start + dur }],
          samples: [
            { harness: 'claude', kind: 'five_hour', resetsAt: start + dur, asOf: start + 0.2 * dur, percent: 30 },
            { harness: 'claude', kind: 'five_hour', resetsAt: start + dur, asOf: asOf - 60000, percent: 70 }
          ]
        }
      }
    }
  });
  env.setAccount({
    loopback_peer: true,
    slots: {
      claude: [{ id: 'default', label: 'Default', active: true }, { id: 'work', label: 'Work', active: false }]
    },
    conversations: []
  });

  let placeCount = 0;
  const mounted = env.window.WsUsage.mount(env.doc, env.top, { accountSwitcher: true });
  await flush();
  const pop = mounted.makePopover(() => { placeCount++; }, () => {});
  pop.el.hidden = false;
  pop.open();
  await flush();

  // Verify stacked compositor SVG layers exist (base, focus, series, pin, preview) and no inner display styles exist.
  const stages = collectNodes(pop.el, n => hasClass(n, 'up-cstage'));
  assert.equal(stages.length, 2);
  const focusLayers = collectNodes(pop.el, n => hasClass(n, 'up-clayer-focus'));
  const seriesLayers = collectNodes(pop.el, n => hasClass(n, 'up-clayer-ser'));
  const pinLayers = collectNodes(pop.el, n => hasClass(n, 'up-clayer-pin'));
  const previewLayers = collectNodes(pop.el, n => hasClass(n, 'up-clayer-preview'));
  assert.ok(focusLayers.length >= 2 && seriesLayers.length >= 2 && pinLayers.length >= 2);
  assert.ok(previewLayers.length >= 2);
  for (const layer of [...focusLayers, ...seriesLayers, ...pinLayers, ...previewLayers]) {
    assert.equal(layer.style.display, undefined);
  }

  // Hover mouseover coalesces via rAF: repeated mouseover on same target schedules nothing extra; different targets collapse into one frame.
  const clPin = collectNodes(pop.el, n => hasClass(n, 'up-pin') && n.getAttribute('data-k') === 'claude')[0];
  const cxRow = collectNodes(pop.el, n => hasClass(n, 'up-hr') && n.getAttribute('data-k') === 'codex')[0];
  assert.ok(clPin && cxRow);

  pop.el.emit('mouseover', { target: clPin });
  assert.equal(env.rafs.length, 1);
  pop.el.emit('mouseover', { target: clPin });
  assert.equal(env.rafs.length, 1, 'same e.target short-circuits without scheduling another rAF');
  pop.el.emit('mouseover', { target: cxRow });
  assert.equal(env.rafs.length, 1, 'rapid target switch coalesces into the pending rAF');
  assert.equal(focusLayers.some(n => hasClass(n, 'is-focus')), false, 'focus does not mutate synchronously before rAF');

  env.rafs[0].fn();
  assert.equal(focusLayers.some(n => n.getAttribute('data-k') === 'codex' && hasClass(n, 'is-focus')), true);
  assert.equal(seriesLayers.some(n => n.getAttribute('data-k') === 'claude' && hasClass(n, 'is-dim')), true);
  assert.equal(pinLayers.some(n => n.getAttribute('data-k') === 'claude' && hasClass(n, 'is-dim')), true);

  // mouseleave cancels pending rAF and clears focus.
  pop.el.emit('mouseover', { target: clPin });
  assert.equal(env.rafs.length, 2);
  assert.equal(env.rafs[1].cancelled, false);
  pop.el.emit('mouseleave', {});
  assert.equal(env.rafs[1].cancelled, true);
  assert.equal(focusLayers.some(n => hasClass(n, 'is-focus')), false);

  // setPreview() via inactive slot pill mouseenter does NOT call place() when popover dimensions stay unchanged.
  const workPill = collectNodes(pop.el, n => hasClass(n, 'up-sp') && n.getAttribute('data-slot-id') === 'work')[0];
  assert.ok(workPill);
  const placesBeforePreview = placeCount;
  workPill.emit('mouseenter', {});
  assert.equal(placeCount, placesBeforePreview, 'setPreview skips place() when popover size is unchanged');
  assert.ok(previewLayers.some(n => hasClass(n, 'is-preview-on')), 'preview layers activate via .is-preview-on');
  for (const layer of previewLayers) {
    assert.equal(layer.style.display, undefined, 'preview activation never sets inline style.display');
  }
  assert.ok(seriesLayers.some(n => n.getAttribute('data-k') === 'claude' && hasClass(n, 'is-preview-base')));

  // If popover height actually changes during preview, setPreview() calls place().
  pop.el.offsetHeight = 460;
  workPill.emit('mouseleave', {});
  assert.equal(placeCount, placesBeforePreview + 1, 'setPreview calls place() when popover dimensions changed');
  assert.equal(previewLayers.some(n => hasClass(n, 'is-preview-on')), false);

  // close() cancels any pending rAF.
  pop.el.emit('mouseover', { target: clPin });
  assert.equal(env.rafs.length, 3);
  pop.close();
  assert.equal(env.rafs[2].cancelled, true);
});

