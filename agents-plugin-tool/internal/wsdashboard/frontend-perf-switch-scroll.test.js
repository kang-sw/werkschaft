// Focused regression tests for session-switch cold-boot latency and #bcol
// free-canvas virtual-scroll performance (Part A & Part B).
const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./testdata/asset-source.cjs');

const dashSrc = readAssetSource('wsdashboard', 'dashboard.js');
const anchorSrc = readAssetSource('wsdashboard', 'dashboard-board-anchor.js');
const framesSrc = readAssetSource('wsdashboard', 'dashboard-frames.js');
const cardsSrc = readAssetSource('wsdashboard', 'cards.js');
const streamViewSrc = readAssetSource('wswhiteboard', 'stream-view.js');
const panelDiffSrc = readAssetSource('wswhiteboard', 'panel-diff.js');
const termSrc = readAssetSource('wsdashboard', 'terminal.js');
const trackerSrc = readAssetSource('wsdashboard', 'tracker-render.js');
const wsAdmSrc = readAssetSource('wsdashboard', 'dashboard-workspace-admission.js');
const railSrc = readAssetSource('wsdashboard', 'rail.js');
const dashRailSrc = readAssetSource('wsdashboard', 'dashboard-rail.js');
const shellSrc = readAssetSource('wsdashboard', 'shell.js');
const lifecycleSrc = readAssetSource('wsdashboard', 'dashboard-lifecycle.js');
const revealSrc = readAssetSource('wsdashboard', 'dashboard-board-reveal.js');

function extractFunc(source, name) {
  const re = new RegExp('\\n  function ' + name + '\\([^\\n]*\\{[^\\n]*\\}\\n|\\n  function ' + name + '\\([\\s\\S]*?\\n  \\}\\n');
  const m = source.match(re);
  assert.ok(m, 'missing function ' + name);
  return m[0];
}

function extractVar(source, name) {
  const re = new RegExp('\\n  var ' + name + '\\b[^\\n]*\\n');
  const m = source.match(re);
  assert.ok(m, 'missing var ' + name);
  return m[0];
}

function makeEl(tag, opts = {}) {
  const attrs = new Map(Object.entries(opts.attrs || {}));
  const props = new Map();
  const cls = new Set(opts.cls || []);
  const listeners = new Map();
  let _scrollTop = opts.scrollTop || 0;
  let _offsetHeight = opts.offsetHeight || 0;
  const el = {
    nodeType: 1,
    tagName: tag.toUpperCase(),
    childNodes: [],
    parentNode: null,
    hidden: !!opts.hidden,
    inert: false,
    open: !!opts.open,
    value: opts.value || '',
    textContent: opts.text || '',
    scrollLeft: 0,
    clientTop: 0,
    clientHeight: opts.clientHeight || 0,
    scrollHeight: opts.scrollHeight || 0,
    overflowY: opts.overflowY || 'visible',
    listeners,
    style: {
      top: '',
      minHeight: '',
      height: '',
      setProperty(k, v) { props.set(k, String(v)); },
      removeProperty(k) { props.delete(k); },
      getPropertyValue(k) { return props.get(k) || ''; },
    },
    get scrollTop() {
      if (opts.onReadScrollTop) opts.onReadScrollTop(el);
      return _scrollTop;
    },
    set scrollTop(v) {
      if (opts.onWriteScrollTop) opts.onWriteScrollTop(el, v);
      _scrollTop = v;
    },
    get offsetHeight() {
      if (opts.onReadOffsetHeight) opts.onReadOffsetHeight(el);
      return _offsetHeight;
    },
    set offsetHeight(v) { _offsetHeight = v; },
    get id() { return attrs.get('id') || ''; },
    set id(v) { attrs.set('id', String(v)); },
    get parentElement() { return el.parentNode; },
    get isConnected() {
      let p = el;
      while (p.parentNode) p = p.parentNode;
      return p.tagName === 'ROOT' || p.tagName === 'HTML';
    },
    get children() { return el.childNodes.filter(c => c.nodeType === 1); },
    get firstChild() { return el.childNodes[0] || null; },
    get firstElementChild() { return el.children[0] || null; },
    classList: {
      contains: c => cls.has(c),
      add: (...cs) => cs.forEach(c => cls.add(c)),
      remove: (...cs) => cs.forEach(c => cls.delete(c)),
      toggle: (c, force) => {
        const on = force === undefined ? !cls.has(c) : !!force;
        if (on) cls.add(c); else cls.delete(c);
        return on;
      },
    },
    hasAttribute: k => attrs.has(k),
    getAttribute: k => attrs.has(k) ? String(attrs.get(k)) : null,
    setAttribute(k, v) {
      if (opts.onSetAttribute) opts.onSetAttribute(el, k, v);
      attrs.set(k, String(v));
      if (k === 'inert') el.inert = true;
    },
    removeAttribute(k) {
      if (opts.onRemoveAttribute) opts.onRemoveAttribute(el, k);
      attrs.delete(k);
      if (k === 'inert') el.inert = false;
    },
    appendChild(ch) {
      if (ch.parentNode) ch.parentNode.removeChild(ch);
      ch.parentNode = el;
      el.childNodes.push(ch);
      return ch;
    },
    removeChild(ch) {
      if (opts.onRemoveChild) opts.onRemoveChild(el, ch);
      const i = el.childNodes.indexOf(ch);
      if (i >= 0) {
        el.childNodes.splice(i, 1);
        ch.parentNode = null;
      }
      return ch;
    },
    matches(sel) {
      return sel.split(',').some(part => {
        part = part.trim();
        if (part.startsWith(':scope > ')) part = part.slice(9).trim();
        const tagM = part.match(/^([a-z0-9]+)/i);
        if (tagM && el.tagName !== tagM[1].toUpperCase()) return false;
        const clsMs = [...part.matchAll(/\.([a-z0-9_-]+)/gi)];
        for (const m of clsMs) if (!cls.has(m[1])) return false;
        const attrMs = [...part.matchAll(/\[([a-z0-9_-]+)(?:="([^"]*)")?\]/gi)];
        for (const m of attrMs) {
          if (!attrs.has(m[1])) return false;
          if (m[2] !== undefined && String(attrs.get(m[1])) !== m[2]) return false;
        }
        return true;
      });
    },
    closest(sel) {
      let p = el;
      while (p) {
        if (p.matches && p.matches(sel)) return p;
        p = p.parentNode;
      }
      return null;
    },
    contains(other) {
      let p = other;
      while (p) {
        if (p === el) return true;
        p = p.parentNode;
      }
      return false;
    },
    querySelector(sel) {
      if (sel.startsWith(':scope > ')) {
        const sub = sel.slice(9).trim();
        return el.children.find(c => c.matches(sub)) || null;
      }
      const stack = [...el.children];
      while (stack.length) {
        const n = stack.shift();
        if (n.matches(sel)) return n;
        stack.unshift(...n.children);
      }
      return null;
    },
    querySelectorAll(sel) {
      const out = [];
      const walk = n => {
        for (const c of n.children) {
          if (c.matches(sel)) out.push(c);
          walk(c);
        }
      };
      walk(el);
      return out;
    },
    addEventListener(type, fn) {
      if (!listeners.has(type)) listeners.set(type, []);
      listeners.get(type).push(fn);
    },
    removeEventListener(type, fn) {
      const arr = listeners.get(type);
      if (!arr) return;
      const i = arr.indexOf(fn);
      if (i >= 0) arr.splice(i, 1);
    },
    dispatch(type, ev = {}) {
      ev.target = ev.target || el;
      ev.currentTarget = el;
      for (const fn of (listeners.get(type) || []).slice()) fn(ev);
    },
    getBoundingClientRect() {
      const y = parseFloat(el.style.top) || 0;
      return { top: y, bottom: y + _offsetHeight, left: 0, right: 800, height: _offsetHeight, width: 800 };
    },
  };
  return el;
}

// ---------------------------------------------------------------------------
// 1. Batched off-window virtualization: read-before-write & cold-load skip
// ---------------------------------------------------------------------------
test('freeApplyOffWindow and virtualCollapse batch all reads before writes and skip scrollTop on cold load', () => {
  const ops = [];
  const root = makeEl('html');
  const makeTopic = (id) => {
    const sec = makeEl('section', {
      cls: ['topic'],
      attrs: { 'data-topic': id, 'data-rail': '1' },
      scrollTop: 42,
      onReadScrollTop: () => ops.push('read:scrollTop:' + id),
      onSetAttribute: (_, k, v) => {
        if (k === 'data-wb-offwindow' || k === 'data-wb-virtual') ops.push('write:' + k + '=' + v + ':' + id);
      },
      onRemoveChild: () => ops.push('write:removeChild:' + id),
    });
    sec.appendChild(makeEl('div', { cls: ['tophead'] }));
    sec.appendChild(makeEl('div', { cls: ['tbody'], text: 'body-' + id }));
    root.appendChild(sec);
    return sec;
  };

  const ctx = {
    Map, Set, WeakSet, Object, Array, Math,
    doc: { activeElement: null },
    window: {},
    freePin: null,
    freeReleasePin() {},
    topicKey: n => n && n.getAttribute ? n.getAttribute('data-topic') : null,
    anchorId: n => n && n.getAttribute ? 't:' + n.getAttribute('data-topic') : null,
    unmountFrames: sec => ops.push('write:unmountFrames:' + sec.getAttribute('data-topic')),
    readAnswers: sec => { ops.push('read:answers:' + sec.getAttribute('data-topic')); return {}; },
    frameHeights: sec => { ops.push('read:frameH:' + sec.getAttribute('data-topic')); return {}; },
  };
  const snippet = [
    extractVar(anchorSrc, 'freePos'),
    extractVar(anchorSrc, 'freeCardRO'),
    extractVar(anchorSrc, 'virtualStore'),
    extractVar(anchorSrc, 'virtualExemptSet'),
    extractVar(anchorSrc, 'virtualStats'),
    extractVar(anchorSrc, 'VIRTUAL_ASK_SEL'),
    extractFunc(anchorSrc, 'virtualIsItemExempt'),
    extractFunc(anchorSrc, 'virtualHasActiveEdit'),
    extractFunc(anchorSrc, 'virtualHasHeldFrame'),
    extractFunc(anchorSrc, 'virtualDescribeNode'),
    extractFunc(anchorSrc, 'virtualCaptureOpen'),
    extractFunc(anchorSrc, 'virtualHasAskItem'),
    extractFunc(anchorSrc, 'virtualCaptureForCollapse'),
    extractFunc(anchorSrc, 'virtualCollapse'),
    extractFunc(anchorSrc, 'freeSetOffWindow'),
    extractFunc(anchorSrc, 'freeApplyOffWindow'),
  ].join('\n');
  vm.createContext(ctx);
  vm.runInContext(snippet, ctx);

  // Warm multi-card batch: every read must precede every write.
  const warmNodes = ['1', '2', '3', '4', '5'].map(makeTopic);
  ctx.freeApplyOffWindow(warmNodes, false, { cold: false });
  const firstWrite = ops.findIndex(o => o.startsWith('write:'));
  const lastRead = ops.map((o, i) => o.startsWith('read:') ? i : -1).reduce((a, b) => Math.max(a, b), -1);
  assert.ok(firstWrite > 0 && lastRead >= 0, 'both reads and writes should occur: ' + JSON.stringify(ops));
  assert.ok(lastRead < firstWrite, 'all reads must finish before any write begins: ' + JSON.stringify(ops));
  assert.equal(warmNodes.every(n => n.getAttribute('data-wb-virtual') === '1'), true);

  // Standalone virtualCollapse: reads scrollTop BEFORE unmountFrames/removeChild.
  ops.length = 0;
  const solo = makeTopic('solo');
  ctx.virtualCollapse(solo);
  const soloRead = ops.indexOf('read:scrollTop:solo');
  const soloUnmount = ops.indexOf('write:unmountFrames:solo');
  assert.ok(soloRead >= 0 && soloUnmount > soloRead, 'standalone virtualCollapse reads scrollTop before unmountFrames');

  // Cold-load batch: zero scrollTop reads!
  ops.length = 0;
  const coldNodes = ['c1', 'c2', 'c3'].map(makeTopic);
  ctx.freeApplyOffWindow(coldNodes, false, { cold: true });
  assert.deepEqual(ops.filter(o => o.startsWith('read:scrollTop')), [], 'cold load skips sec.scrollTop reads');
  assert.equal(coldNodes.every(n => n.getAttribute('data-wb-virtual') === '1'), true);
});

// ---------------------------------------------------------------------------
// 2. #bcol free-canvas active scroll: cached heights, single scrollTop read,
//    rAF scrollend, hysteresis deferral & idle flush
// ---------------------------------------------------------------------------
test('free-canvas scroll tick uses cached heights, single scrollTop read, rAF scrollend, and scroll hysteresis', () => {
  const root = makeEl('html');
  const bcol = makeEl('div', { attrs: { id: 'bcol' }, overflowY: 'auto', clientHeight: 600 });
  const board = makeEl('div', { attrs: { id: 'r-board' } });
  bcol.appendChild(board);
  root.appendChild(bcol);

  let colScrollTopReads = 0;
  let offsetHeightReads = 0;
  const unmounted = [];
  const dialValues = [];
  let idleEvents = 0;

  let bcolST = 0;
  Object.defineProperty(bcol, 'scrollTop', {
    get() { colScrollTopReads++; return bcolST; },
    set(v) { bcolST = v; },
    configurable: true,
  });

  const topics = [];
  for (let i = 0; i < 24; i++) {
    const sec = makeEl('section', {
      cls: ['topic'],
      attrs: { 'data-topic': String(i), 'data-rail': '1' },
      offsetHeight: 320,
      onReadOffsetHeight: () => { offsetHeightReads++; },
    });
    sec.appendChild(makeEl('div', { cls: ['tophead'] }));
    sec.appendChild(makeEl('div', { cls: ['tbody'], text: 'body-' + i }));
    board.appendChild(sec);
    topics.push(sec);
  }

  let rafId = 0, timerId = 0;
  const rafs = new Map();
  const timers = new Map();
  const flushRaf = () => {
    const pending = Array.from(rafs.entries());
    rafs.clear();
    for (const [, fn] of pending) fn();
  };
  const flushTimers = () => {
    const pending = Array.from(timers.entries());
    timers.clear();
    for (const [, fn] of pending) fn();
  };

  const docListeners = new Map();
  const ctx = {
    Map, Set, WeakSet, Object, Array, Math, Date,
    CustomEvent: function CustomEvent(type, init) { this.type = type; this.detail = init && init.detail; },
    requestAnimationFrame: fn => { const id = ++rafId; rafs.set(id, fn); return id; },
    cancelAnimationFrame: id => rafs.delete(id),
    setTimeout: (fn) => { const id = ++timerId; timers.set(id, fn); return id; },
    clearTimeout: id => timers.delete(id),
    location: { pathname: '/s/test', search: '' },
    window: { innerHeight: 600, parent: null },
    doc: {
      activeElement: null,
      scrollingElement: root,
      documentElement: root,
      addEventListener(type, fn) { (docListeners.get(type) || docListeners.set(type, []).get(type)).push(fn); },
      dispatchEvent(ev) { for (const fn of (docListeners.get(ev.type) || [])) fn(ev); },
    },
    $: id => id === 'bcol' ? bcol : null,
    region: name => name === 'board' ? board : null,
    topicKey: n => n && n.getAttribute ? n.getAttribute('data-topic') : null,
    getComputedStyle: () => ({ overflowY: 'auto', paddingTop: '0px', paddingBottom: '0px' }),
    load: () => null,
    save: () => {},
    reducedMotion: () => false,
    unmountFrames: sec => unmounted.push(sec.getAttribute('data-topic')),
    mountFramesPaced: () => {},
    boardDialScroll: v => dialValues.push(v),
    boardDialInit: () => {},
    boardDialSync: () => {},
    readAnswers: () => ({}),
    frameHeights: () => ({}),
    writeAnswers: () => {},
    bindCapsuleNotes: () => {},
    fmtTimes: () => {},
  };
  ctx.window.parent = ctx.window;
  ctx.doc.addEventListener('ws-board-scroll-idle', () => idleEvents++);

  vm.createContext(ctx);
  vm.runInContext(anchorSrc + '\nanchorInit();', ctx);

  // Jump cleanly to card 10 so cards are in the middle of the runway.
  ctx.WsBoardAnchor = ctx.window.WsBoardAnchor;
  ctx.WsBoardAnchor.select(topics[10], { keep: true, smooth: false });

  // Reset counters before simulating an active user scroll forward by 1 card.
  colScrollTopReads = 0;
  offsetHeightReads = 0;
  unmounted.length = 0;
  dialValues.length = 0;

  const y11 = ctx.WsBoardAnchor.windowInfo().posOf(topics[11]);
  bcolST = y11 + 10;
  bcol.dispatch('scroll');
  // freeScheduleIdle must perform ZERO col.scrollTop reads during scroll event.
  assert.equal(colScrollTopReads, 0, 'scroll event handler (freeOnScroll/freeScheduleIdle) reads scrollTop 0 times');
  assert.equal(ctx.WsBoardAnchor.scrolling(), true, 'WsBoardAnchor.scrolling() is true while rAF/idle active');

  // Execute the rAF scroll tick.
  flushRaf();
  assert.equal(colScrollTopReads, 1, 'freeScrollTick reads bcol.scrollTop at most once');
  assert.equal(offsetHeightReads, 0, 'freeLayout scrollTick uses cached heights with zero sync offsetHeight reads');
  assert.equal(dialValues.length, 1, 'boardDialScroll updates using top + shift without re-reading scrollTop');

  // Card that just exited the window (within FREE_EVICT_MARGIN=4 slots) is hidden
  // but deferred (not collapsed yet, keepFrame returns true).
  const winAfter = ctx.WsBoardAnchor.windowInfo();
  const justExited = topics[winAfter.start - 1];
  assert.equal(justExited.getAttribute('data-wb-offwindow'), 'hidden');
  assert.equal(justExited.hasAttribute('data-wb-virtual'), false, 'hysteresis defers virtualCollapse within margin during scroll');
  assert.equal(ctx.WsBoardAnchor.keepFrame(justExited), true, 'keepFrame protects deferred card from topicBand unmount');

  // Farther card (> FREE_EVICT_MARGIN slots away) stays collapsed.
  assert.equal(topics[0].getAttribute('data-wb-virtual'), '1', 'far card remains collapsed');

  // scrollend on #bcol schedules via rAF instead of running synchronously.
  const readsBeforeEnd = colScrollTopReads;
  bcol.dispatch('scrollend', { target: bcol });
  assert.equal(colScrollTopReads, readsBeforeEnd, 'anchorScrollEnd schedules rAF instead of synchronous scrollTop read');
  assert.equal(rafs.size, 1, 'anchorScrollEnd queued one rAF');
  flushRaf();

  // Idle timer fires: flushes deferred hysteresis collapses and notifies scroll-idle.
  colScrollTopReads = 0;
  flushTimers();
  assert.equal(colScrollTopReads, 0, 'freeScheduleIdle callback checks generation without reading col.scrollTop');
  assert.equal(justExited.getAttribute('data-wb-virtual'), '1', 'idle recenter flushes deferred hysteresis collapse');
  assert.equal(ctx.WsBoardAnchor.scrolling(), false, 'scrolling() clears once idle timer completes');
  assert.equal(idleEvents >= 1, true, 'ws-board-scroll-idle dispatched on scroll idle');
});

// ---------------------------------------------------------------------------
// 3. cards.js:floatToolbar skips #bcol scroll events when !overviewOn
// ---------------------------------------------------------------------------
test('cards.js floatToolbar ignores #bcol scroll events outside Overview and updates when overview toggles', () => {
  const bcol = makeEl('div', { attrs: { id: 'bcol' } });
  bcol.wbScrollOwner = true;
  const board = makeEl('div', { attrs: { id: 'r-board' } });
  const toolbar = makeEl('div', { attrs: { id: 'btoolbar' } });
  toolbar.toggleAttribute = (k, on) => { if (on) toolbar.setAttribute(k, ''); else toolbar.removeAttribute(k); };
  const face = makeEl('div', { cls: ['board-toolbar-face'] });
  const row = makeEl('div', { cls: ['board-toolbar-actions'] });
  const btn = makeEl('button');
  row.appendChild(btn);
  face.appendChild(row);
  toolbar.appendChild(face);
  bcol.appendChild(toolbar);
  bcol.appendChild(board);

  let rectReads = 0;
  face.getBoundingClientRect = () => { rectReads++; return { top: 20, bottom: 52, height: 32, width: 600 }; };
  btn.getBoundingClientRect = () => { rectReads++; return { top: 24, bottom: 48, height: 24, width: 80 }; };
  bcol.getBoundingClientRect = () => { rectReads++; return { top: 0, bottom: 700, height: 700, width: 800 }; };
  board.getBoundingClientRect = () => { rectReads++; return { top: -100, bottom: 900, height: 1000, width: 800 }; };

  const rafs = [];
  const docListeners = new Map();
  const winListeners = new Map();
  const ctx = {
    Map, Set, WeakSet, Object, Array, Math,
    requestAnimationFrame: fn => { rafs.push(fn); return rafs.length; },
    cancelAnimationFrame: () => {},
    window: {
      innerHeight: 700,
      innerWidth: 1200,
      ResizeObserver: function () { this.observe = () => {}; },
      addEventListener(type, fn) { (winListeners.get(type) || winListeners.set(type, []).get(type)).push(fn); },
      getComputedStyle: () => ({ overflowY: 'auto', paddingTop: '0px', paddingBottom: '0px', gap: '8px' }),
    },
    document: {
      documentElement: makeEl('html'),
      body: makeEl('body'),
      scrollingElement: makeEl('html'),
      getElementById: id => ({ bcol, 'r-board': board, btoolbar: toolbar }[id] || null),
      querySelector: () => null,
      querySelectorAll: () => [],
      addEventListener(type, fn) { (docListeners.get(type) || docListeners.set(type, []).get(type)).push(fn); },
    },
    $: id => ({ bcol, 'r-board': board, btoolbar: toolbar }[id] || null),
    region: () => board,
    scroller: () => bcol,
    maxed: null,
    wsT: k => k,
  };
  ctx.doc = ctx.document;
  vm.createContext(ctx);
  const snippet = [
    'var overviewOn = false, floatQueue = null, toolbar = $("btoolbar"), col = $("bcol"), board = region("board"), root = document.documentElement;',
    extractFunc(cardsSrc, 'floatToolbar'),
    'floatToolbar();',
    'var triggerOverview = function (on) { overviewOn = !!on; if (floatQueue) floatQueue(); };',
  ].join('\n');
  vm.runInContext(snippet, ctx);

  // Drain initial boot rAF if queued.
  while (rafs.length) rafs.shift()();
  rectReads = 0;

  // Scroll #bcol while overviewOn === false -> zero rAFs, zero rect reads!
  const scrollHandlers = docListeners.get('scroll') || [];
  assert.equal(scrollHandlers.length, 1, 'floatToolbar registered scroll handler');
  for (let i = 0; i < 5; i++) {
    for (const fn of scrollHandlers) fn({ target: bcol });
  }
  assert.equal(rafs.length, 0, 'floatToolbar must not queue rAF on #bcol scroll when !overviewOn');
  assert.equal(rectReads, 0, 'floatToolbar performs zero layout reads on #bcol scroll when !overviewOn');

  // Turning Overview on queues floatQueue and re-enables #bcol scroll handling.
  ctx.triggerOverview(true);
  assert.equal(rafs.length, 1, 'WsCards.overview(true) triggers floatQueue');
  rafs.shift()();
  assert.ok(rectReads > 0, 'floatToolbar measures when overview is active');
});

// ---------------------------------------------------------------------------
// 4. stream-view.js: owner() scroll listener zero reads in user-scrolling &
//    scrollerOf() #bcol fast-path
// ---------------------------------------------------------------------------
test('stream-view owner() scroll listener skips scrollTop reads during user-scrolling and scrollerOf fast-paths #bcol', () => {
  let now = 1000;
  let scrollTopReads = 0;
  let styleReads = 0;
  const root = makeEl('html');
  const body = makeEl('body');
  const bcol = makeEl('div', { attrs: { id: 'bcol' }, overflowY: 'auto', clientHeight: 600, scrollHeight: 2000 });
  bcol.wbScrollOwner = true;
  const board = makeEl('div', { attrs: { id: 'r-board' } });
  const item = makeEl('div', { cls: ['wb-entry'] });
  board.appendChild(item);
  bcol.appendChild(board);
  body.appendChild(bcol);
  root.appendChild(body);

  let st = 100;
  Object.defineProperty(bcol, 'scrollTop', {
    get() { scrollTopReads++; return st; },
    set(v) { st = v; },
    configurable: true,
  });

  const win = {
    performance: { now: () => now },
    innerHeight: 600,
    innerWidth: 800,
    document: {
      documentElement: root,
      body,
      scrollingElement: root,
      activeElement: null,
      addEventListener() {},
    },
    addEventListener() {},
    getComputedStyle(el) {
      styleReads++;
      return { overflowY: el === bcol ? 'auto' : 'visible', transform: 'none', scrollPaddingTop: '0px', display: 'block', visibility: 'visible' };
    },
  };
  const ctx = { Map, Set, WeakSet, Object, Array, Math, Date, window: win };
  vm.createContext(ctx);
  vm.runInContext(streamViewSrc + '\nvar kit = viewKit(window);', ctx);

  // Initialize owner + test scrollerOf #bcol fast-path across 10 calls.
  styleReads = 0;
  for (let i = 0; i < 10; i++) {
    const h = ctx.kit.keep(item, { capture: false });
    h.end();
  }
  assert.ok(styleReads <= 2, 'scrollerOf caches/fast-paths #bcol with minimal getComputedStyle calls: ' + styleReads);

  // Enter active user-scrolling via wheel, then fire 10 scroll events within 200ms.
  bcol.dispatch('wheel');
  scrollTopReads = 0;
  for (let i = 0; i < 10; i++) {
    now += 16;
    st += 40;
    bcol.dispatch('scroll');
  }
  assert.equal(scrollTopReads, 0, 'owner() scroll listener performs zero scrollTop reads while user-scrolling');

  // Once 200ms cooldown expires, a scroll event reads scrollTop at most once.
  now += 250;
  st += 50;
  bcol.dispatch('scroll');
  assert.equal(scrollTopReads, 1, 'owner() scroll listener reads scrollTop at most once when waking from idle');
});

// ---------------------------------------------------------------------------
// 5. Part A & Item 10 regressions: shShow early ready reveal, terminal rgb(),
//    panel-diff layout(), tracker-render measure(), wsAdmissionSync, railGate
// ---------------------------------------------------------------------------
test('shell reveals frame early on ready+head without waiting for iframe load and stays idempotent on load', () => {
  const sentToA = [], sentToB = [], swaps = [];
  const makeFrame = (rid, sent, initialClass) => {
    const cls = new Set((initialClass || 'sframe').split(/\s+/).filter(Boolean));
    const listeners = {};
    const f = {
      _src: '/s/' + rid + '?frame=1',
      get src() { return this._src; },
      set src(v) { this._src = v; },
      get className() { return Array.from(cls).join(' '); },
      set className(v) { cls.clear(); String(v || '').split(/\s+/).filter(Boolean).forEach(c => cls.add(c)); },
      inert: false,
      style: {},
      classList: {
        add(c) { cls.add(c); },
        remove(c) { cls.delete(c); },
        contains(c) { return cls.has(c); },
        toggle(c, force) {
          const on = force === undefined ? !cls.has(c) : !!force;
          if (on) cls.add(c); else cls.delete(c);
          return on;
        },
      },
      contentDocument: { readyState: 'loading', activeElement: null },
      contentWindow: {
        location: { href: 'http://h:1/s/' + rid + '?frame=1', hash: '', reload() {} },
        postMessage(m) { sent.push(m); },
        focus() {},
        wsCanCacheSession() { return true; },
      },
      addEventListener(t, fn) { (listeners[t] ||= []).push(fn); },
      dispatch(t) { for (const fn of (listeners[t] || []).slice()) fn(); },
      focus() {},
      blur() {},
      remove() {},
      getBoundingClientRect() { return { top: 0, left: 0, width: 800, height: 600 }; },
    };
    return f;
  };
  const frameA = makeFrame('a', sentToA, 'sframe on');
  frameA.contentDocument.readyState = 'complete';
  const frameB = makeFrame('b', sentToB, 'sframe');

  const winListeners = {};
  const framesBox = { children: [frameA], appendChild(el) { this.children.push(el); return el; } };
  const topEl = makeEl('div', { cls: ['top'] });
  const rootEl = makeEl('html');
  const timers = new Map();
  let nextTimer = 1;
  const doc = {
    title: 'old',
    activeElement: null,
    documentElement: rootEl,
    body: makeEl('body'),
    createElement(tag) { return tag === 'iframe' ? frameB : makeEl(tag); },
    getElementById: id => ({ frames: framesBox }[id] || null),
    querySelector: sel => {
      if (sel === '.dwrap > .top') return topEl;
      if (sel === '#frames > iframe') return frameA;
      return null;
    },
    querySelectorAll: () => [],
    addEventListener() {},
    dispatchEvent() {},
  };
  const loc = { origin: 'http://h:1', href: 'http://h:1/s/a', pathname: '/s/a', search: '', hash: '' };
  const hist = {
    state: { wsShellIndex: 0 },
    pushState(s, _t, u) {
      this.state = s;
      const parsed = new URL(u, loc.origin);
      loc.href = parsed.href; loc.pathname = parsed.pathname; loc.search = parsed.search; loc.hash = parsed.hash;
    },
    replaceState(s, _t, u) {
      this.state = s;
      const parsed = new URL(u, loc.origin);
      loc.href = parsed.href; loc.pathname = parsed.pathname; loc.search = parsed.search; loc.hash = parsed.hash;
    },
  };
  const win = {
    location: loc,
    history: hist,
    addEventListener(t, fn) { (winListeners[t] ||= []).push(fn); },
  };
  const ctx = {
    Map, Set, URL, URLSearchParams, Object, Array, Math, Date, JSON, Promise, Number,
    CustomEvent: function CustomEvent(type, init) { this.type = type; this.detail = init && init.detail; },
    window: win, doc, location: loc, history: hist,
    root: rootEl,
    shell: true, framed: false, rid: 'a',
    layoutMode: 'split', inputMode: 'enter', headHeld: null,
    $: id => doc.getElementById(id),
    q: () => [],
    region: () => null,
    railMarks() {}, railOpen() {}, railShown: () => false, railPopClose() {}, railRenameNew() {}, railSettingsMark() {},
    railBtn: null, hdNavigated() {},
    labelEditing: () => false, labelEnd() {},
    setTheme() {},
    swap(name, hash, html) { swaps.push({ name, hash, html }); },
    setTimeout(fn, ms) { const id = nextTimer++; timers.set(id, { fn, ms }); return id; },
    clearTimeout(id) { timers.delete(id); },
  };
  vm.createContext(ctx);
  vm.runInContext(shellSrc, ctx);
  assert.ok(ctx.window.WsShell.message({ wsShell: 'ready' }), 'shell.js accepts T0 ready message');

  // Execute the real shell glue + shShellInit from dashboard-lifecycle.js.
  const shellGlueMatch = lifecycleSrc.match(/\n  var shFrames = \[\][\s\S]*?\n  \/\/ shFrameInit wires/);
  const shellInitMatch = lifecycleSrc.match(/\n  function shShellInit\(\) \{[\s\S]*?\n  \}\n/);
  assert.ok(shellGlueMatch && shellInitMatch, 'dashboard-lifecycle.js shell glue and shShellInit present');
  vm.runInContext(shellGlueMatch[0] + '\n' + shellInitMatch[0] + '\nshShellInit();\n', ctx);
  assert.equal(ctx.shOn.el, frameA, 'initial loaded frame A is active');

  // Open /s/b: frame B is created and staged as shWant while frame A remains visible.
  ctx.shOpen('/s/b', true);
  assert.equal(ctx.shWant.el, frameB, 'frame B becomes pending destination');
  assert.equal(ctx.shOn.el, frameA, 'frame A stays visible while frame B boots');
  assert.equal(frameB.classList.contains('on'), false, 'frame B is hidden before ready+head');
  assert.equal(sentToB.length, 0, 'no messages posted to frame B before ready');

  const dispatchMsg = (source, data) => {
    for (const fn of (winListeners.message || [])) fn({ origin: loc.origin, source, data });
  };

  // Frame B posts ready before head: settings are sent once and headTimer waits, no reveal yet.
  dispatchMsg(frameB.contentWindow, { wsShell: 'ready' });
  assert.equal(ctx.shWant.ready, true, 'ready message marks entry ready');
  assert.equal(ctx.shWant.loaded, false, 'iframe load has not fired yet');
  assert.equal(sentToB.filter(m => m.wsShell === 'settings').length, 1, 'settings sent once on ready');
  assert.equal(sentToB.filter(m => m.wsShell === 'shown').length, 0, 'shown not sent before head');
  assert.equal(frameB.classList.contains('on'), false, 'frame B stays hidden until head arrives');
  assert.equal(timers.size, 1, 'head wait fallback timer armed');

  // Frame B posts head before iframe load: shShow reveals frame B immediately.
  dispatchMsg(frameB.contentWindow, { wsShell: 'head', html: '<b>B</b>', qs: '', pad: 0, top: 44 });
  assert.equal(timers.size, 0, 'head wait timer cleared on reveal');
  assert.equal(ctx.shOn.el, frameB, 'frame B revealed early on ready+head before iframe load');
  assert.equal(ctx.shWant, null, 'pending destination cleared after reveal');
  assert.equal(ctx.shOn.loaded, false, 'frame B is revealed while iframe load is still pending');
  assert.equal(frameB.classList.contains('on'), true, 'frame B has .on class');
  assert.equal(frameA.classList.contains('on'), false, 'frame A loses .on class');
  assert.equal(frameB.inert, false, 'frame B is interactive');
  assert.equal(ctx.rid, 'b', 'shell updates active rid');
  assert.deepEqual(JSON.parse(JSON.stringify(sentToB.filter(m => m.wsShell === 'shown'))), [{ wsShell: 'shown', shown: true }], 'frame B receives shown:true once');
  assert.equal(sentToA.filter(m => m.wsShell === 'shown').at(-1).shown, false, 'frame A receives shown:false');
  assert.ok(swaps.some(s => s.name === 'head' && s.html === '<b>B</b>'), 'shell mirrors frame B header');

  // Late iframe load event after early reveal must stay idempotent (no duplicate settings/shown).
  frameB.dispatch('load');
  assert.equal(ctx.shOn.loaded, true, 'load marks entry loaded');
  assert.equal(sentToB.filter(m => m.wsShell === 'settings').length, 1, 'late load does not re-send settings');
  assert.equal(sentToB.filter(m => m.wsShell === 'shown').length, 1, 'late load does not re-send shown');
  assert.equal(ctx.shOn.el, frameB, 'late load keeps frame B active');

  // Execute real shStaged/shUnstage (dashboard.js) + mountFrame/mountFrames/mountFramesPaced (dashboard-frames.js).
  const stageBlock = dashSrc.match(/\n  var shStaged = framed[\s\S]*?\n  \}\n/);
  assert.ok(stageBlock, 'dashboard.js shStaged/shUnstage block present');
  const frameFuncs = ['frameShown', 'mountFrame', 'mountFrames', 'mountFramesPaced', 'mountSlice']
    .map(n => extractFunc(framesSrc, n)).join('\n');

  const htmlRoot = makeEl('html');
  const bodyEl = makeEl('body');
  const boardEl = makeEl('div', { attrs: { id: 'r-board' } });
  const placeholder = makeEl('div', {
    cls: ['doc-frame'],
    attrs: { class: 'doc-frame', 'data-srcdoc': '<p>hello</p>', 'data-sandbox': 'allow-scripts' },
  });
  Object.defineProperty(placeholder, 'attributes', {
    get() {
      return ['class', 'data-srcdoc', 'data-sandbox']
        .filter(k => placeholder.hasAttribute(k))
        .map(k => ({ name: k, value: placeholder.getAttribute(k) }));
    },
  });
  boardEl.replaceChild = (next, old) => {
    const i = boardEl.childNodes.indexOf(old);
    if (i >= 0) {
      boardEl.childNodes[i] = next;
      next.parentNode = boardEl;
      old.parentNode = null;
    }
    return old;
  };
  boardEl.appendChild(placeholder);
  bodyEl.appendChild(boardEl);
  htmlRoot.appendChild(bodyEl);

  let createdIframes = 0;
  let stagedCallbacksRan = 0;
  const fCtx = {
    Array, WeakMap,
    framed: true, shHidden: false,
    mountList: [], mountTask: false, mountTimer: 0,
    frameWins: new WeakMap(),
    performance: { now: () => 0 },
    setTimeout: () => 1,
    doc: {
      createElement(tag) {
        if (tag === 'iframe') createdIframes++;
        return makeEl(tag);
      },
    },
    q: (sel, el) => (el || htmlRoot).querySelectorAll(sel),
    frameNear: () => true,
  };
  vm.createContext(fCtx);
  vm.runInContext(stageBlock[0] + '\n' + frameFuncs, fCtx);

  assert.equal(fCtx.shHolding(), true, 'framed page starts staged');
  fCtx.shStage(() => { stagedCallbacksRan++; });
  assert.equal(stagedCallbacksRan, 0, 'shStage defers callbacks while staged');
  assert.equal(fCtx.mountFrame(placeholder), placeholder, 'mountFrame defers while shStaged');
  fCtx.mountFrames(boardEl);
  fCtx.mountFramesPaced(boardEl);
  assert.equal(createdIframes, 0, 'zero doc-frame iframes created while shStaged');
  assert.equal(boardEl.children[0].tagName, 'DIV', 'placeholder remains unmounted while shStaged');

  // Once shell delivers shown:true -> shUnstage() runs and mountFramesPaced mounts the iframe.
  fCtx.shUnstage();
  assert.equal(fCtx.shHolding(), false, 'shUnstage clears staged state');
  assert.equal(stagedCallbacksRan, 1, 'shUnstage flushes deferred callbacks');
  fCtx.mountFramesPaced(boardEl);
  assert.equal(createdIframes, 1, 'mountFramesPaced mounts doc-frame iframe after unstage');
  assert.equal(boardEl.children[0].tagName, 'IFRAME', 'placeholder replaced with iframe');
  assert.equal(boardEl.children[0].getAttribute('srcdoc'), '<p>hello</p>', 'mounted iframe carries srcdoc');
  assert.equal(boardEl.children[0].getAttribute('sandbox'), 'allow-scripts', 'mounted iframe carries sandbox');
});

test('terminal rgb() avoids DOM probe for hex/rgb colors, panel-diff layout() and tracker measure() batch read-before-write', () => {
  // 5b. terminal.js rgb()
  const rgbMatch = termSrc.match(/  var rgbCache = Object\.create\(null\);[\s\S]*?  function rgb\(color\) \{[\s\S]*?\n  \}\n/);
  assert.ok(rgbMatch, 'terminal.js rgbCache + rgb() present');
  const tCtx = {
    Object, Math, parseInt, parseFloat,
    doc: { createElement() { throw new Error('DOM probe should not run for hex/rgb colors'); } },
  };
  vm.runInNewContext(rgbMatch[0] + '; globalThis.out = [rgb("#abc"), rgb("#112233"), rgb("rgb(10, 20, 30)"), rgb("rgba(10, 20, 30, 0.5)")];', tCtx);
  assert.deepEqual(JSON.parse(JSON.stringify(tCtx.out)), [[170, 187, 204], [17, 34, 51], [10, 20, 30], [10, 20, 30]]);

  // 5c. panel-diff.js layout() read-before-write
  const pdMatch = panelDiffSrc.match(/  function layout\(state\) \{[\s\S]*?\n  \}\n/);
  assert.ok(pdMatch, 'panel-diff.js layout() present');
  const pdOps = [];
  const makeItem = (i) => ({
    change: { k: 'edit' },
    gone: [],
    block: {
      getBoundingClientRect() { pdOps.push('read:block:' + i); return { top: i * 40, height: 30, bottom: i * 40 + 30 }; },
      querySelector() { return null; },
    },
    hit: {
      classList: { toggle(c) { pdOps.push('write:cls:' + c + ':' + i); } },
      style: {
        set left(v) { pdOps.push('write:left:' + i); },
        set top(v) { pdOps.push('write:top:' + i); },
        set height(v) { pdOps.push('write:height:' + i); },
      },
    },
  });
  const pdState = {
    body: {
      getBoundingClientRect() { pdOps.push('read:bodyRect'); return { left: 10, top: 0, width: 500, bottom: 400 }; },
    },
    layer: {
      getBoundingClientRect() { pdOps.push('read:layerRect'); return { left: 10, top: 0 }; },
      contains() { return false; },
    },
    items: [makeItem(0), makeItem(1), makeItem(2), makeItem(3)],
  };
  const pdCtx = { Math, framed: false, active: null, place() {}, state: pdState };
  vm.runInNewContext(pdMatch[0] + '; layout(state);', pdCtx);
  const pdFirstWrite = pdOps.findIndex(o => o.startsWith('write:'));
  const pdLastRead = pdOps.map((o, i) => o.startsWith('read:') ? i : -1).reduce((a, b) => Math.max(a, b), -1);
  assert.ok(pdFirstWrite > 0 && pdLastRead < pdFirstWrite, 'panel-diff layout reads all geometry before writing styles: ' + JSON.stringify(pdOps));

  // 5d. tracker-render.js measure() read-before-write + cache skip
  const mMatch = trackerSrc.match(/  var lastTkhw = '', lastTktop = '', measureTopFrame = 0;[\s\S]*?  function measure\(\) \{[\s\S]*?\n  \}\n/);
  assert.ok(mMatch, 'tracker-render.js measure() present');
  const trOps = [];
  const trStyle = { setProperty(k, v) { trOps.push('write:' + k + '=' + v); } };
  const trDrawerStyle = { setProperty(k, v) { trOps.push('write:' + k + '=' + v); } };
  const trCtx = {
    Math,
    $: id => id === 'tkhandles'
      ? { getBoundingClientRect() { trOps.push('read:tkhandles'); return { width: 120.4 }; } }
      : (id === 'tkdrawer' ? { style: trDrawerStyle } : null),
    O: { header: { getBoundingClientRect() { trOps.push('read:header'); return { bottom: 48.2 }; } } },
    document: { documentElement: { style: trStyle } },
    requestAnimationFrame: () => 1,
  };
  vm.createContext(trCtx);
  vm.runInContext(mMatch[0] + '; measure();', trCtx);
  const trFirstWrite = trOps.findIndex(o => o.startsWith('write:'));
  const trLastRead = trOps.map((o, i) => o.startsWith('read:') ? i : -1).reduce((a, b) => Math.max(a, b), -1);
  assert.ok(trLastRead < trFirstWrite, 'tracker measure reads #tkhandles and header before writing CSS vars: ' + JSON.stringify(trOps));
  trOps.length = 0;
  vm.runInContext('measure();', trCtx);
  assert.deepEqual(trOps.filter(o => o.startsWith('write:')), [], 'unchanged tracker measure skips CSS var writes');

  // 5e. dashboard-workspace-admission.js skips scrollTop write when .rws-unreg is closed
  let unregWrites = 0;
  const details = makeEl('details', { cls: ['rws-unreg'], open: false });
  const unregList = makeEl('div', {
    cls: ['unreg-list'],
    scrollTop: 0,
    onWriteScrollTop: () => { unregWrites++; },
  });
  details.appendChild(unregList);
  const railBox = makeEl('div');
  railBox.appendChild(details);
  const admCtx = {
    Object, Array, Set, JSON,
    railEl: null,
    region: () => railBox,
    q: (sel, r) => (r || railBox).querySelectorAll(sel),
    load: () => null, save: () => {}, doc: { activeElement: null, addEventListener() {} },
    wsT: k => k,
    admissionSync() {},
  };
  vm.createContext(admCtx);
  vm.runInContext(wsAdmSrc + '\nwsUnregScroll = 80; wsAdmissionSync();', admCtx);
  assert.equal(unregWrites, 0, 'wsAdmissionSync skips scrollTop write while .rws-unreg is closed');
  details.open = true;
  vm.runInContext('wsAdmissionSync();', admCtx);
  assert.equal(unregWrites, 1, 'wsAdmissionSync restores scrollTop once .rws-unreg is open');

  // 5f. rail.js rowInputAttentionRows + dashboard-rail.js railGate scroll deferral
  const rCtx = {
    window: { addEventListener() {}, MutationObserver: class { observe() {} } },
    document: { addEventListener() {}, createElement: t => makeEl(t), body: makeEl('body') },
    Object, Array, Set, Map, JSON,
  };
  vm.createContext(rCtx);
  vm.runInContext(railSrc, rCtx);
  const att = rCtx.window.WsRail.attention();
  const baseInput = {
    shell: ['<div>', '', '</div>'],
    order: ['s1', 's2'],
    rows: {
      s1: { html: '<div class="rs" data-rid="s1" data-asks=""></div>', asks: '' },
      s2: { html: '<div class="rs" data-rid="s2" data-asks="a1"></div>', asks: 'a1' },
    },
  };
  const rows0 = rCtx.window.WsRail.rowInputAttentionRows(baseInput);
  att.observe(rows0, 's1', true);
  const nextInput = {
    shell: baseInput.shell,
    order: ['s1', 's2'],
    rows: {
      s1: baseInput.rows.s1,
      s2: { html: '<div class="rs" data-rid="s2" data-asks="a1 a2"></div>', asks: 'a1 a2' },
    },
  };
  att.observe(rCtx.window.WsRail.rowInputAttentionRows(nextInput), 's1', false);
  const domRow = makeEl('div', { cls: ['rs'], attrs: { 'data-rid': 's2', 'data-asks': 'a1 a2' } });
  att.mark([domRow], 's1', 'needs attention');
  assert.equal(domRow.classList.contains('attn'), true, 'rowInputAttentionRows triggers attention without reparsing HTML');

  // Execute real dashboard-rail.js boardScrolling(), railGate, railRelease(), and ws-board-scroll-idle wiring.
  const railGateBlock = dashRailSrc.match(/\n  var rdrag = null, railSaving = 0, railMinGen = 0;[\s\S]*?\n  var railGate = [^\n]+\n/);
  const railReleaseBlock = dashRailSrc.match(/\n  function railRelease\(\) \{[\s\S]*?\n  function railEditing\(\) \{[^\n]+\}\n/);
  assert.ok(railGateBlock && railReleaseBlock, 'dashboard-rail.js boardScrolling/railGate/railRelease blocks present');

  let directScrolling = false;
  let framedScrolling = false;
  const railSwaps = [];
  const railDoc = makeEl('document');
  const railWin = {
    addEventListener() {},
    MutationObserver: class { observe() {} },
    WsBoardAnchor: { scrolling: () => directScrolling },
  };
  const gateCtx = {
    window: railWin,
    document: railDoc,
    doc: railDoc,
    Object, Array, Set, Map, JSON,
    railEl: null,
    shell: true,
    shOn: { el: { contentWindow: { WsBoardAnchor: { scrolling: () => framedScrolling } } } },
    swap(name, hash, html) { railSwaps.push({ name, hash, html }); },
  };
  vm.createContext(gateCtx);
  vm.runInContext(railSrc + '\nvar WsRail = window.WsRail;\n' + railGateBlock[0] + '\n' + railReleaseBlock[0], gateCtx);

  // Direct board scroll holds rail swaps and flushes the latest held swap on ws-board-scroll-idle.
  directScrolling = true;
  assert.equal(gateCtx.boardScrolling(), true, 'boardScrolling() reports true during direct board scroll');
  assert.equal(gateCtx.railGate.offer({ hash: 'h1', html: '<div>rail-1</div>' }), true, 'railGate holds swap during active scroll');
  assert.equal(gateCtx.railGate.offer({ hash: 'h2', html: '<div>rail-2</div>' }), true, 'railGate coalesces later swap while held');
  railDoc.dispatch('ws-board-scroll-idle');
  assert.equal(railSwaps.length, 0, 'railRelease does not flush while scroll is still active');

  directScrolling = false;
  railDoc.dispatch('ws-board-scroll-idle');
  assert.deepEqual(railSwaps, [{ name: 'rail', hash: 'h2', html: '<div>rail-2</div>' }], 'ws-board-scroll-idle flushes latest held rail swap');
  railDoc.dispatch('ws-board-scroll-idle');
  assert.equal(railSwaps.length, 1, 'subsequent ws-board-scroll-idle with nothing held is a no-op');
  assert.equal(gateCtx.railGate.offer({ hash: 'h-idle', html: '<div>idle</div>' }), false, 'railGate does not hold once scrolling is idle');

  // Shell-framed board scroll holds rail swaps via shOn.el.contentWindow.WsBoardAnchor and flushes via window.wsBoardScrollIdle.
  framedScrolling = true;
  assert.equal(gateCtx.boardScrolling(), true, 'boardScrolling() detects scrolling in active shell frame');
  assert.equal(gateCtx.railGate.offer({ hash: 'h3', html: '<div>rail-3</div>' }), true, 'railGate holds swap while shell frame is scrolling');
  railWin.wsBoardScrollIdle();
  assert.equal(railSwaps.length, 1, 'window.wsBoardScrollIdle does not flush while frame is still scrolling');

  framedScrolling = false;
  railWin.wsBoardScrollIdle();
  assert.deepEqual(railSwaps, [
    { name: 'rail', hash: 'h2', html: '<div>rail-2</div>' },
    { name: 'rail', hash: 'h3', html: '<div>rail-3</div>' },
  ], 'window.wsBoardScrollIdle flushes held rail swap once shell frame scroll settles');
});

// Bug: a frame loaded or kept hidden lays #bcol out at 0px, and nothing re-measured or settled
// the free-canvas windows when it became visible, leaving the middle viewport blank.
function makeRevealCtx(over = {}) {
  const rafs = [], docH = {}, winH = {};
  const calls = { settle: 0, apply: 0, cancelled: [] };
  const col = { clientHeight: 700, clientWidth: 900 };
  const ctx = {
    calls, rafs, docH, winH, col,
    doc: { hidden: false, documentElement: { hasAttribute: () => false }, addEventListener: (t, f) => { (docH[t] ||= []).push(f); } },
    shHidden: false,
    anchorSized: { w: 0, h: 0, bar: 0, padTop: 0, bounded: false },
    freeHeightCache: Object.assign(new Map(), { _skippedHidden: false }),
    free: true,
    $: id => (id === 'bcol' ? col : null),
    getComputedStyle: () => ({ overflowY: 'auto', paddingTop: '10px', paddingBottom: '6px', paddingLeft: '0px', paddingRight: '0px' }),
    anchorApplySizes: () => { calls.apply++; },
    anchorCards: () => [{}],
    freeEnabled() { return ctx.free; },
    requestAnimationFrame: fn => { rafs.push(fn); return rafs.length; },
    cancelAnimationFrame: id => { calls.cancelled.push(id); rafs[id - 1] = null; },
    flush() { for (let i = 0; i < 10 && rafs.some(Boolean); i++) rafs.splice(0).forEach(f => f && f()); },
    ...over,
  };
  ctx.window = { WsBoardAnchor: { settle: () => { calls.settle++; } }, addEventListener: (t, f) => { winH[t] = f; } };
  vm.createContext(ctx);
  vm.runInContext(revealSrc + '\nboardRevealInit();', ctx);
  return ctx;
}

test('every hidden-to-visible edge re-measures #bcol and settles, with no _skippedHidden gate and no scroll event', () => {
  const ctx = makeRevealCtx();
  ctx.anchorSized.h = 0; // measured while hidden
  ctx.shHidden = true;
  docEmit(ctx, 'ws-page-visibility');
  assert.equal(ctx.calls.settle, 0, 'hiding never settles');
  ctx.shHidden = false;
  assert.equal(ctx.freeHeightCache._skippedHidden, false, 'precondition: nothing was skipped');
  docEmit(ctx, 'ws-page-visibility');
  assert.equal(ctx.calls.settle, 1, 'visible edge settles even when no card measurement was skipped');
  assert.equal(ctx.calls.apply, 1);
  assert.equal(ctx.anchorSized.h, 700 - 10 - 6, 'content-box height re-read from the laid-out column');
  assert.equal(ctx.anchorSized.bounded, true);
  assert.equal(ctx.rafs.length, 0, 'the edge itself schedules no frame loop');
});

test('boardRevealSchedule coalesces to one post-layout settle (double rAF) and cancels the superseded pass', () => {
  const ctx = makeRevealCtx();
  vm.runInContext('boardRevealSchedule(); boardRevealSchedule(); boardRevealSchedule();', ctx);
  assert.equal(ctx.rafs.filter(Boolean).length, 1, 'only the latest scheduled frame stays pending');
  assert.equal(ctx.calls.cancelled.length, 2);
  ctx.rafs.splice(0).forEach(f => f && f());
  assert.equal(ctx.calls.settle, 0, 'first frame only waits for layout to commit');
  assert.equal(ctx.rafs.filter(Boolean).length, 1);
  ctx.flush();
  assert.equal(ctx.calls.settle, 1, 'three requests settle exactly once; cancelled passes never settle');
  assert.equal(ctx.rafs.filter(Boolean).length, 0, 'idle again: no frames scheduled');
});

test('visibilitychange, pageshow, and filter/tab transitions schedule a reveal; free-only gates on freeEnabled', () => {
  const ctx = makeRevealCtx();
  ctx.doc.hidden = true;
  docEmit(ctx, 'visibilitychange');
  assert.equal(ctx.rafs.length, 0, 'becoming hidden does not schedule');
  ctx.doc.hidden = false;
  docEmit(ctx, 'visibilitychange');
  ctx.flush();
  assert.equal(ctx.calls.settle, 1);
  ctx.winH.pageshow();
  ctx.flush();
  assert.equal(ctx.calls.settle, 2);
  ctx.free = false;
  vm.runInContext('boardRevealSchedule(true);', ctx);
  assert.equal(ctx.rafs.length, 0, 'filter/tab/view transitions skip boards outside free-canvas mode');
  ctx.free = true;
  vm.runInContext('boardRevealSchedule(true);', ctx);
  ctx.flush();
  assert.equal(ctx.calls.settle, 3);
  ctx.shHidden = true;
  vm.runInContext('boardRevealSchedule();', ctx);
  ctx.flush();
  assert.equal(ctx.calls.settle, 3, 'a pass that lands while hidden is dropped');
});

test('a still-unsized #bcol keeps the last valid sizes but still settles', () => {
  const ctx = makeRevealCtx();
  ctx.anchorSized.h = 555;
  ctx.col.clientHeight = 0;
  docEmit(ctx, 'ws-page-visibility');
  assert.equal(ctx.anchorSized.h, 555);
  assert.equal(ctx.calls.apply, 0);
  assert.equal(ctx.calls.settle, 1);
});

test('anchorSize ignores a transient hidden 0x0 #bcol report so it cannot clobber sizes or mark the board settled', () => {
  const col = {};
  const calls = { settle: 0, apply: 0 };
  const ctx = {
    $: id => (id === 'bcol' ? col : null),
    anchorObserveAux() {}, anchorFaceEl: null, anchorReplyEl: null,
    anchorSized: { w: 800, h: 600, bar: 0, reply: 0, padTop: 8, bounded: true },
    getComputedStyle: () => ({ overflowY: 'auto', paddingTop: '8px' }),
    anchorApplySizes() { calls.apply++; }, anchorSettle() { calls.settle++; },
    freeActive: false, freeWinStart: 0, anchorProg: null, anchorRealign() {},
    boardDialSync() {}, virtualReconcile() {},
  };
  vm.createContext(ctx);
  vm.runInContext(extractFunc(anchorSrc, 'anchorSize'), ctx);
  const rep = (w, h) => [{ target: col, contentBoxSize: [{ inlineSize: w, blockSize: h }], contentRect: { width: w, height: h } }];
  ctx.anchorSize(rep(0, 0));
  assert.equal(ctx.anchorSized.h, 600, 'hidden report leaves the valid height alone');
  assert.equal(ctx.anchorSized.w, 800);
  assert.equal(calls.settle, 0, 'a zero box never marks the board settled');
  ctx.anchorSize(rep(800, 420));
  assert.equal(ctx.anchorSized.h, 420);
  assert.equal(calls.settle, 1, 'the first real box settles');
});

test('session switch (shown:true) schedules a coalesced reveal after showRegions and mountFramesPaced; anchor no longer gates on _skippedHidden', () => {
  const shown = lifecycleSrc.slice(lifecycleSrc.indexOf('shHidden = false;\n        syncPageVisibility();'));
  const iShow = shown.indexOf('showRegions();'), iMount = shown.indexOf('mountFramesPaced(doc);'), iReveal = shown.indexOf('boardRevealSchedule()');
  assert.ok(iShow > 0 && iMount > iShow && iReveal > iMount && iReveal < shown.indexOf('queue();'), 'reveal is scheduled once the held board swap and frames were applied');
  assert.ok(!anchorSrc.includes('freeHeightCache._skippedHidden) {'), 'ws-page-visibility handler is no longer gated on _skippedHidden');
  assert.ok(anchorSrc.includes('boardRevealInit()'));
});

function docEmit(ctx, type) { (ctx.docH[type] || []).forEach(f => f()); }
