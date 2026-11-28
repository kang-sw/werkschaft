// Offline unit tests for the Designated-Y free-canvas virtual scroll engine
// and its cross-module integrations (stream-view.js, rail.js, pill.js,
// overview.js, cards.js, dashboard-conversation.js).
const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

const anchorSrc = readAssetSource('wsdashboard', 'dashboard-board-anchor.js');
const boardCssSrc = readAssetSource('wsdashboard', 'dashboard-board.css');
const selectionsSrc = readAssetSource('wsdashboard', 'dashboard-composer-selections.js');
const overviewSrc = readAssetSource('wsdashboard', 'overview.js');
const convSrc = readAssetSource('wsdashboard', 'dashboard-conversation.js');
const cardsSrc = readAssetSource('wsdashboard', 'cards.js');
const streamViewSrc = readAssetSource('wswhiteboard', 'stream-view.js');
const railSrc = readAssetSource('wswhiteboard', 'rail.js');
const pillSrc = readAssetSource('wswhiteboard', 'pill.js');

function makeEl(tag, opts = {}) {
  const attrs = new Map(Object.entries(opts.attrs || {}));
  const props = new Map();
  const cls = new Set(opts.cls || []);
  const listeners = new Map();
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
    _innerHTML: '',
    get innerHTML() { return el._innerHTML; },
    set innerHTML(html) {
      el._innerHTML = String(html || '');
      while (el.childNodes.length) el.removeChild(el.childNodes[0]);
      const re = /<([a-z0-9]+)(?:\s+class="([^"]*)")?[^>]*>([^<]*)<\/\1>/gi;
      let m;
      while ((m = re.exec(el._innerHTML))) {
        el.appendChild(makeEl(m[1], {
          cls: m[2] ? m[2].split(/\s+/).filter(Boolean) : [],
          text: m[3] || '',
        }));
      }
    },
    scrollTop: opts.scrollTop || 0,
    scrollLeft: 0,
    clientTop: 0,
    clientHeight: opts.clientHeight || 0,
    scrollHeight: opts.scrollHeight || 0,
    offsetHeight: opts.offsetHeight || 0,
    offsetTop: opts.offsetTop || 0,
    overflowY: opts.overflowY || 'visible',
    listeners,
    style: {
      top: '',
      minHeight: '',
      cssText: '',
      setProperty(k, v) { props.set(k, String(v)); },
      removeProperty(k) { props.delete(k); },
      getPropertyValue(k) { return props.get(k) || ''; },
    },
    get id() { return attrs.get('id') || ''; },
    set id(v) { attrs.set('id', String(v)); },
    get parentElement() { return el.parentNode; },
    get className() { return Array.from(cls).join(' '); },
    set className(v) {
      cls.clear();
      String(v || '').split(/\s+/).filter(Boolean).forEach(c => cls.add(c));
    },
    get isConnected() {
      let p = el;
      while (p.parentNode) p = p.parentNode;
      return p.tagName === 'ROOT' || p.tagName === 'HTML';
    },
    get children() { return el.childNodes.filter(c => c.nodeType === 1); },
    get firstChild() { return el.childNodes[0] || null; },
    get firstElementChild() { return el.children[0] || null; },
    get nextSibling() {
      if (!el.parentNode) return null;
      const i = el.parentNode.childNodes.indexOf(el);
      return i >= 0 ? el.parentNode.childNodes[i + 1] || null : null;
    },
    get attributes() {
      const out = [];
      for (const [name, value] of attrs.entries()) out.push({ name, value: String(value) });
      return out;
    },
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
      attrs.set(k, String(v));
      if (k === 'class') el.className = String(v);
      if (k === 'inert') el.inert = true;
    },
    removeAttribute(k) {
      attrs.delete(k);
      if (k === 'inert') el.inert = false;
    },
    appendChild(ch) {
      if (ch.parentNode) ch.parentNode.removeChild(ch);
      ch.parentNode = el;
      el.childNodes.push(ch);
      return ch;
    },
    insertBefore(ch, ref) {
      if (ch.parentNode) ch.parentNode.removeChild(ch);
      ch.parentNode = el;
      const i = ref ? el.childNodes.indexOf(ref) : -1;
      if (i < 0) el.childNodes.push(ch); else el.childNodes.splice(i, 0, ch);
      return ch;
    },
    removeChild(ch) {
      const i = el.childNodes.indexOf(ch);
      if (i >= 0) el.childNodes.splice(i, 1);
      ch.parentNode = null;
      return ch;
    },
    replaceChild(next, old) {
      if (next.parentNode) next.parentNode.removeChild(next);
      const i = el.childNodes.indexOf(old);
      if (i >= 0) {
        el.childNodes[i] = next;
        next.parentNode = el;
        old.parentNode = null;
      }
      return old;
    },
    contains(other) {
      for (let p = other; p; p = p.parentNode) if (p === el) return true;
      return false;
    },
    addEventListener(type, fn) {
      if (!listeners.has(type)) listeners.set(type, []);
      listeners.get(type).push(fn);
    },
    removeEventListener(type, fn) {
      const list = listeners.get(type);
      if (!list) return;
      const i = list.indexOf(fn);
      if (i >= 0) list.splice(i, 1);
    },
    scrollTo(o) {
      if (o && typeof o.top === 'number') el.scrollTop = o.top;
    },
    getBoundingClientRect() {
      if (opts.rect) return opts.rect(el);
      const y = parseFloat(el.style.top) || 0;
      const h = el.hasAttribute('data-wb-offwindow') && el.getAttribute('data-wb-offwindow') === 'hidden' ? 0 : (el.offsetHeight || el.clientHeight || 0);
      return { top: y, bottom: y + h, left: 0, right: 800, width: h ? 800 : 0, height: h };
    },
    matches(sel) {
      return sel.split(',').some(part => {
        part = part.trim();
        if (!part) return false;
        if (part.includes('section.topic[data-topic]') || part === 'section.topic') {
          return el.tagName === 'SECTION' && cls.has('topic') && attrs.has('data-topic');
        }
        if (part.startsWith('button.closed-more')) {
          if (el.tagName !== 'BUTTON' || !cls.has('closed-more')) return false;
          if (part.includes('[data-closed-next]') && !attrs.has('data-closed-next')) return false;
          if (part.includes(':not([hidden])') && el.hidden) return false;
          if (part.includes(':not([data-state-filtered])') && attrs.has('data-state-filtered')) return false;
          return true;
        }
        if (/[\s>+~:]/.test(part)) return false;
        const m = part.match(/^([a-z0-9]*)((?:[.#][\w-]+|\[[\w-]+(?:="[^"]*")?\])*)$/i);
        if (!m) return false;
        if (m[1] && el.tagName !== m[1].toUpperCase()) return false;
        const tokens = m[2].match(/[.#][\w-]+|\[[\w-]+(?:="[^"]*")?\]/g) || [];
        return tokens.every(t => {
          if (t[0] === '.') return cls.has(t.slice(1));
          if (t[0] === '#') return attrs.get('id') === t.slice(1);
          const eq = t.indexOf('=');
          if (eq > 0) return attrs.get(t.slice(1, eq)) === t.slice(eq + 2, -2);
          return attrs.has(t.slice(1, -1));
        });
      });
    },
    closest(sel) {
      for (let p = el; p && p.nodeType === 1; p = p.parentNode) {
        if (p.matches && p.matches(sel)) return p;
      }
      return null;
    },
    querySelector(sel) {
      if (sel.startsWith(':scope > ')) {
        const rest = sel.slice(9).trim();
        if (!/[\s>+~]/.test(rest)) return el.children.find(c => c.matches(rest)) || null;
        return null;
      }
      return el.querySelectorAll(sel)[0] || null;
    },
    querySelectorAll(sel) {
      const out = [];
      (function walk(node) {
        for (const c of node.children) {
          let hit = false;
          if (sel === '.xterm, .tm-host') hit = c.classList.contains('xterm') || c.classList.contains('tm-host');
          else if (sel === 'details') hit = c.tagName === 'DETAILS';
          else if (sel === '[data-max]') hit = c.hasAttribute('data-max');
          else if (sel === 'iframe.doc-frame') hit = c.tagName === 'IFRAME' && c.classList.contains('doc-frame');
          else if (sel.includes('textarea')) hit = c.tagName === 'TEXTAREA' || c.tagName === 'INPUT';
          else if (sel.includes('.card.policy')) hit = c.classList.contains('policy') && c.id.startsWith('item-');
          else if (sel.startsWith('#')) hit = c.id === sel.slice(1);
          else hit = c.matches(sel);
          if (hit) out.push(c);
          walk(c);
        }
      })(el);
      return out;
    },
  };
  return el;
}

function makeTopic(name, opts = {}) {
  const sec = makeEl('section', {
    cls: ['topic'],
    attrs: { id: 'topic-' + name, 'data-topic': name, 'data-th': opts.rev || 'v1' },
    offsetHeight: opts.height || 300,
  });
  const head = makeEl('div', { cls: ['tophead'], offsetHeight: 36 });
  const body = makeEl('div', { cls: ['tdoc'] });
  const stream = makeEl('div', { cls: ['topic-stream'] });
  sec.appendChild(head);
  sec.appendChild(body);
  sec.appendChild(stream);
  if (opts.withTerm) sec.appendChild(makeEl('div', { cls: ['xterm'] }));
  if (opts.withDraft) {
    const ta = makeEl('textarea', { cls: ['note'], value: 'unsaved draft' });
    sec.appendChild(ta);
  }
  return sec;
}

function makeBcard(id, opts = {}) {
  const c = makeEl('div', {
    cls: ['bcard'],
    attrs: { 'data-card': id, ...(opts.rail ? { 'data-rail': '' } : {}) },
    offsetHeight: opts.height || 360,
  });
  const inner = makeEl('div', { cls: ['bc-in'] });
  const hd = makeEl('div', { cls: ['bc-hd'], offsetHeight: 32 });
  const body = makeEl('div', { cls: ['bc-body'], offsetHeight: (opts.height || 360) - 32 });
  inner.appendChild(hd);
  inner.appendChild(body);
  c.appendChild(inner);
  return c;
}

function makeFreeCanvasWorld(spec, opts = {}) {
  const root = makeEl('root');
  const html = makeEl('html');
  const body = makeEl('body');
  const bcol = makeEl('div', {
    attrs: { id: 'bcol' },
    overflowY: 'auto',
    clientHeight: opts.viewH || 800,
  });
  const board = makeEl('div', { attrs: { id: 'r-board' } });
  let toolbar = null;
  if (opts.toolbarH) {
    toolbar = makeEl('div', { cls: ['board-toolbar'], offsetHeight: opts.toolbarH });
    board.appendChild(toolbar);
  }
  const nodes = [];
  for (const s of spec) {
    const n = s.card ? makeBcard(s.card, s) : makeTopic(s.topic, s);
    board.appendChild(n);
    nodes.push(n);
  }
  let moreBtn = null;
  if (opts.closedMore) {
    moreBtn = makeEl('button', {
      cls: ['closed-more'],
      attrs: { 'data-closed-next': '20' },
      offsetHeight: opts.closedMoreH || 40,
    });
    board.appendChild(moreBtn);
  }
  bcol.appendChild(board);
  body.appendChild(bcol);
  html.appendChild(body);
  root.appendChild(html);

  // Track ResizeObserver instances created by dashboard-board-anchor.js.
  const ros = [];
  class FakeRO {
    constructor(cb) {
      this.cb = cb;
      this.observed = new Set();
      ros.push(this);
    }
    observe(n) { this.observed.add(n); }
    unobserve(n) { this.observed.delete(n); }
    disconnect() { this.observed.clear(); }
    fire(entries) { this.cb(entries); }
  }

  let nextTimer = 1;
  const timers = new Map();
  const rafs = [];
  const flushes = [];
  const store = new Map(Object.entries(opts.store || {}));

  const ctx = {
    window: null,
    location: { pathname: '/s/free/', search: '' },
    doc: {
      activeElement: null,
      documentElement: html,
      body,
      querySelector: () => null,
      querySelectorAll: () => [],
    },
    $: id => (id === 'bcol' ? bcol : id === 'r-board' ? board : null),
    region: name => (name === 'board' ? board : null),
    topicKey: n => (n && n.nodeType === 1 && n.tagName === 'SECTION' && n.classList.contains('topic') ? n.getAttribute('data-topic') : null),
    reducedMotion: () => true,
    boardPending: {},
    boardGuards: () => ctx.anchorVisibleCards().map(c => ctx.topicKey(c)).filter(Boolean),
    boardFlushViewed: force => { flushes.push(!!force); },
    boardSnap: null,
    dialScrubbing: false,
    ResizeObserver: FakeRO,
    MutationObserver: null,
    sessionStorage: {
      getItem: k => (store.has(k) ? store.get(k) : null),
      setItem: (k, v) => store.set(k, String(v)),
      removeItem: k => store.delete(k),
    },
    setTimeout(fn, ms) {
      const id = nextTimer++;
      timers.set(id, { fn, ms });
      return id;
    },
    clearTimeout(id) { timers.delete(id); },
    requestAnimationFrame(fn) { rafs.push(fn); return rafs.length; },
    cancelAnimationFrame() {},
    getComputedStyle: n => ({ overflowY: n.overflowY || 'visible', paddingTop: '0px' }),
    unmountFrames: () => {},
    mountFrames: () => {},
    mountFramesPaced: () => {},
    readAnswers: () => ({}),
    writeAnswers: () => {},
    frameHeights: () => ({}),
    setFrameHeights: () => {},
    restoreOpen: () => {},
    watchBoard: () => {},
    bandBoard: () => {},
    fmtTimes: () => {},
    sweepOutImgs: () => {},
    indexFrames: () => {},
    mermaid: () => {},
    applySent: () => {},
    wbBindBoard: () => {},
    bindCapsuleNotes: () => {},
    WsCards: {
      toolbar: () => toolbar,
      isDocked: () => false,
      decorate: () => {},
    },
    wbHistory: { capture: () => [], init: () => {}, restore: () => {} },
    Date, Math, Set, Map, Array, Object, String, Number, Infinity, isNaN, parseFloat,
  };
  Object.assign(ctx, opts.extra || {});
  ctx.window = ctx;
  ctx.innerHeight = opts.viewH || 800;
  vm.createContext(ctx);
  vm.runInContext(anchorSrc, ctx);
  ctx.anchorInit();

  const cardRO = () => ros.find(r => r !== ctx.anchorRO);
  return {
    ctx, bcol, board, html, nodes, moreBtn, ros, cardRO, timers, rafs, flushes,
    fireIdle() {
      const entries = Array.from(timers.entries());
      timers.clear();
      for (const [, t] of entries) t.fn();
    },
    flushRaf() {
      while (rafs.length) rafs.shift()();
    },
  };
}

test('releasing a composer mention lets the next scroll evict its parked topic', () => {
  const spec = Array.from({ length: 30 }, (_, i) => ({ topic: `t${i}`, height: 450 }));
  const { ctx, nodes, bcol } = makeFreeCanvasWorld(spec);
  vm.runInContext(selectionsSrc.match(/^  function hideMention\(keep, keepImages\) \{[\s\S]*?^  \}/m)[0], ctx);
  const source = nodes[4];
  const frame = makeEl('iframe', { cls: ['doc-frame'] });
  ctx.WsBoardAnchor.select(source, { smooth: false });
  source.querySelector('.tdoc').appendChild(frame);
  Object.assign(ctx, { mpend: { frame }, mnote: null, mbtn: null, cimgs: null });
  ctx.WsBoardAnchor.select(nodes[16], { smooth: false });
  assert.equal(source.getAttribute('data-wb-offwindow'), 'parked');
  ctx.hideMention();
  bcol.scrollTop += 12;
  ctx.freeScrollTick();
  assert.equal(source.getAttribute('data-wb-offwindow'), 'hidden');
  assert.equal(source.contains(frame), false);
});

test('1. Designated-Y upward/downward placement and ResizeObserver 0px viewport shift on k < anchor vs k >= anchor', () => {
  const spec = Array.from({ length: 20 }, (_, i) => ({ topic: `t${i}`, height: 300 + (i % 3) * 80 }));
  const w = makeFreeCanvasWorld(spec, { viewH: 800 });
  const { ctx, nodes, bcol } = w;

  // Jump synchronously to middle card 10.
  assert.equal(ctx.WsBoardAnchor.select(nodes[10], { smooth: false }), true);
  const info = ctx.WsBoardAnchor.windowInfo();
  assert.equal(info.active, true);
  assert.equal(info.anchorIdx, 10);
  // 1.5 * 800 = 1200px coverage above/below; with ~380px cards + 16px gap, ±3..±4 covers >= 1200px.
  assert.ok(info.start <= 7 && info.start >= 2, `start=${info.start} within [2..7]`);
  assert.ok(info.end >= 13 && info.end <= 18, `end=${info.end} within [13..18]`);

  // Verify exact anchor-relative upward & downward coordinates around y_10 = 50000.
  assert.equal(info.posOf(nodes[10]), 50000);
  for (let i = 11; i <= info.end; i++) {
    assert.equal(info.posOf(nodes[i]), info.posOf(nodes[i - 1]) + nodes[i - 1].offsetHeight + 16);
  }
  for (let i = 9; i >= info.start; i--) {
    assert.equal(info.posOf(nodes[i]), info.posOf(nodes[i + 1]) - 16 - nodes[i].offsetHeight);
  }

  // ResizeObserver observes strictly mounted in-window cards (none off-window).
  const ro = w.cardRO();
  assert.ok(ro, 'free-canvas ResizeObserver created');
  for (let i = 0; i < nodes.length; i++) {
    const inWin = i >= info.start && i <= info.end;
    assert.equal(ro.observed.has(nodes[i]), inWin, `RO observed[${i}] === ${inWin}`);
  }

  // Position scrollTop so card 10 is the topmost viewport-intersecting card.
  bcol.scrollTop = 50000;
  const cacheBefore = ctx.anchorCache;
  const yBefore = [];
  for (let i = info.start; i <= info.end; i++) yBefore[i] = info.posOf(nodes[i]);

  // Above-anchor buffer card k=8 (< anchor=10) grows by +120px:
  // ONLY j <= 8 shift upward by -120px; 9, 10 (anchor), and 11..end stay at 0px shift; scrollTop untouched!
  const kAbove = 8;
  const newHAbove = nodes[kAbove].offsetHeight + 120;
  nodes[kAbove].offsetHeight = newHAbove;
  ro.fire([{ target: nodes[kAbove], borderBoxSize: [{ blockSize: newHAbove }], contentRect: { height: newHAbove } }]);

  assert.equal(ctx.anchorCache, cacheBefore, 'ResizeObserver height update must not invalidate anchorCache');
  assert.equal(bcol.scrollTop, 50000, 'scrollTop untouched on non-clamped k < anchor resize');
  for (let i = info.start; i <= kAbove; i++) {
    assert.equal(info.posOf(nodes[i]), yBefore[i] - 120, `j=${i} <= k shifts upward by -120`);
  }
  for (let i = kAbove + 1; i <= info.end; i++) {
    assert.equal(info.posOf(nodes[i]), yBefore[i], `j=${i} > k has exact 0px shift`);
  }

  // Anchor card k=10 grows by +80px: ONLY j > 10 shift downward by +80px.
  const yBeforeDown = [];
  for (let i = info.start; i <= info.end; i++) yBeforeDown[i] = info.posOf(nodes[i]);
  const newHAnchor = nodes[10].offsetHeight + 80;
  nodes[10].offsetHeight = newHAnchor;
  ro.fire([{ target: nodes[10], borderBoxSize: [{ blockSize: newHAnchor }], contentRect: { height: newHAnchor } }]);

  for (let i = info.start; i <= 10; i++) {
    assert.equal(info.posOf(nodes[i]), yBeforeDown[i], `j=${i} <= anchor untouched`);
  }
  for (let i = 11; i <= info.end; i++) {
    assert.equal(info.posOf(nodes[i]), yBeforeDown[i] + 80, `j=${i} > anchor shifts downward by +80`);
  }

  // Sub-epsilon resize (< 0.5px) triggers zero style mutations.
  const top11Before = nodes[11].style.top;
  ro.fire([{ target: nodes[10], borderBoxSize: [{ blockSize: newHAnchor + 0.3 }], contentRect: { height: newHAnchor + 0.3 } }]);
  assert.equal(nodes[11].style.top, top11Before, 'sub-epsilon delta (<0.5px) ignored');
});

test('2. Top boundary clamping (y_0 = topPad, scrollTop sync, no recenter) and bottom boundary clamping (closed-more, tight minHeight, default 100000px)', () => {
  const spec = Array.from({ length: 16 }, (_, i) => ({ topic: `t${i}`, height: 420 }));
  const w = makeFreeCanvasWorld(spec, { viewH: 800, toolbarH: 40, closedMore: true, closedMoreH: 44 });
  const { ctx, nodes, bcol, board, moreBtn } = w;

  // Initial anchor is card 0 (winStart === 0): y_0 = topPad (40 + 16 = 56), minHeight = 100000px, closed-more hidden.
  let info = ctx.WsBoardAnchor.windowInfo();
  assert.equal(info.start, 0);
  assert.ok(info.end < nodes.length - 1);
  assert.equal(info.posOf(nodes[0]), 56, 'y_0 clamped to toolbarH + gap');
  assert.equal(board.style.minHeight, '100000px', 'default RUNWAY_HEIGHT = 100000px when Card N-1 is off-window');
  assert.equal(moreBtn.getAttribute('data-wb-offwindow'), 'hidden');

  // Idle recentering must be skipped while winStart === 0 even though y_0 = 56 < 10000.
  ctx.freeOnScroll();
  w.fireIdle();
  assert.equal(ctx.WsBoardAnchor.windowInfo().posOf(nodes[0]), 56, 'no idle recenter while winStart === 0');

  // Resize k=0 (< anchor=2) while winStart === 0: y_0 re-clamps to 56 and scrollTop shifts by +dh (0px visual shift on anchor).
  const y2Before = ctx.WsBoardAnchor.windowInfo().posOf(nodes[2]);
  bcol.scrollTop = y2Before;
  nodes[0].offsetHeight = 520; // +100px
  w.cardRO().fire([{ target: nodes[0], borderBoxSize: [{ blockSize: 520 }], contentRect: { height: 520 } }]);
  const y2After = ctx.WsBoardAnchor.windowInfo().posOf(nodes[2]);
  assert.equal(ctx.WsBoardAnchor.windowInfo().posOf(nodes[0]), 56, 'y_0 stays clamped to topPad');
  assert.equal(y2After - bcol.scrollTop, y2Before - y2Before, '0px visual shift on anchor during winStart===0 resize');
  assert.equal(bcol.scrollTop, y2Before + 100);

  // Jump to Card N-1 (winEnd === N-1): closed-more visible below last card, minHeight tightly clamped, no idle recenter.
  ctx.WsBoardAnchor.select(nodes[15], { smooth: false });
  info = ctx.WsBoardAnchor.windowInfo();
  assert.equal(info.end, 15);
  assert.equal(moreBtn.hasAttribute('data-wb-offwindow'), false, 'closed-more un-hidden at bottom boundary');
  const expectedMoreTop = Math.round(info.posOf(nodes[15]) + nodes[15].offsetHeight + 16);
  assert.equal(moreBtn.style.top, expectedMoreTop + 'px');
  const expectedMinH = Math.ceil(expectedMoreTop + 44 + 48);
  assert.equal(board.style.minHeight, expectedMinH + 'px', 'minHeight clamped tightly at bottom boundary');

  // Force y_first < 10000 while winEnd === N-1: idle recentering still skipped!
  const y15Before = info.posOf(nodes[15]);
  ctx.freeOnScroll();
  w.fireIdle();
  assert.equal(ctx.WsBoardAnchor.windowInfo().posOf(nodes[15]), y15Before, 'no idle recenter while winEnd === N-1');
});

test('3. Idle runway recentering (0px visual delta, skipped during dialScrubbing) and synchronous jump centering (short centered vs tall top-aligned)', () => {
  const spec = Array.from({ length: 24 }, (_, i) => ({
    topic: `t${i}`,
    height: i === 12 ? 220 : i === 14 ? 1100 : 400,
  }));
  const w = makeFreeCanvasWorld(spec, { viewH: 800 });
  const { ctx, nodes, bcol } = w;

  // Jump to short card 12 (h = 220 < usableH): synchronously centered in usable viewport.
  ctx.WsBoardAnchor.select(nodes[12], { smooth: false });
  assert.equal(nodes[12].hasAttribute('data-wb-offwindow'), false);
  const y12 = ctx.WsBoardAnchor.windowInfo().posOf(nodes[12]);
  assert.equal(y12, 50000);
  // topClear = padTop(0) + railInset(0) + capH(2)=44; botClear = botReserve(2)=47; usableH = 800-44-47 = 709.
  // Centered scrollTop = 50000 - 44 - (709 - 220) / 2 = 49711.5 -> 49712.
  assert.equal(bcol.scrollTop, Math.round(50000 - 44 - (709 - 220) / 2), 'short card centered in usable viewport');

  // Jump to tall card 14 (h = 1100 >= usableH): top-aligned at y - topClear.
  ctx.WsBoardAnchor.select(nodes[14], { smooth: false });
  const y14 = ctx.WsBoardAnchor.windowInfo().posOf(nodes[14]);
  assert.equal(bcol.scrollTop, Math.round(y14 - 44), 'tall card top-aligned below sticky rail headers');

  // Simulate drifting near top runway margin (y_first < 10000) while winStart > 0 && winEnd < N-1.
  const info = ctx.WsBoardAnchor.windowInfo();
  assert.ok(info.start > 0 && info.end < nodes.length - 1);
  const shiftNearTop = -44000;
  for (let i = info.start; i <= info.end; i++) {
    const ny = info.posOf(nodes[i]) + shiftNearTop;
    ctx.freePos.set(nodes[i], ny);
    nodes[i].style.top = Math.round(ny) + 'px';
  }
  bcol.scrollTop = bcol.scrollTop + shiftNearTop;
  const visualBefore = ctx.WsBoardAnchor.windowInfo().posOf(nodes[14]) - bcol.scrollTop;
  assert.ok(ctx.WsBoardAnchor.windowInfo().posOf(nodes[info.start]) < 10000);

  // While dialScrubbing is true, idle recenter is skipped.
  ctx.dialScrubbing = true;
  ctx.freeOnScroll();
  w.fireIdle();
  assert.equal(ctx.WsBoardAnchor.windowInfo().posOf(nodes[14]), y14 + shiftNearTop, 'idle recenter skipped during dialScrubbing');

  // Once scrubbing ends, idle recenter rebases anchor back to RUNWAY_MID (50000) with 0px visual shift.
  ctx.dialScrubbing = false;
  ctx.freeOnScroll();
  w.fireIdle();
  assert.equal(ctx.WsBoardAnchor.windowInfo().posOf(nodes[14]), 50000, 'anchor rebased to RUNWAY_MID');
  assert.equal(ctx.WsBoardAnchor.windowInfo().posOf(nodes[14]) - bcol.scrollTop, visualBefore, 'exact 0px visual delta after recentering');
});

test('4. Split data-wb-offwindow hidden vs parked+inert, VIRTUAL_KEEP_ATTRS, Overview suspend/resume, releaseNode, empty board, and loadClosed settlement', () => {
  const spec = [
    { topic: 't0', height: 400 },
    { card: 'term1', height: 400 }, // exempt .bcard widget
    { topic: 't2', height: 400, withTerm: true }, // exempt topic (.xterm)
    { topic: 't3', height: 400, withDraft: true }, // exempt topic (draft textarea)
    ...Array.from({ length: 12 }, (_, i) => ({ topic: `t${i + 4}`, height: 400 })),
  ];
  const w = makeFreeCanvasWorld(spec, { viewH: 800 });
  const { ctx, nodes, bcol, board } = w;

  // Jump to last topic so indices 0..3 are off-window.
  ctx.WsBoardAnchor.select(nodes[15], { smooth: false });
  assert.equal(nodes[0].getAttribute('data-wb-offwindow'), 'hidden', 'ordinary off-window topic is hidden');
  assert.equal(nodes[0].hasAttribute('data-wb-virtual'), true, 'ordinary off-window topic body evicted');
  assert.equal(nodes[0].inert, false, 'hidden topic does not set inert');

  for (const idx of [1, 2, 3]) {
    assert.equal(nodes[idx].getAttribute('data-wb-offwindow'), 'parked', `exempt node[${idx}] is parked (never hidden)`);
    assert.equal(nodes[idx].inert, true, `parked node[${idx}] has inert`);
    assert.equal(nodes[idx].hasAttribute('data-wb-virtual'), false, `exempt node[${idx}] body preserved`);
    assert.equal(w.cardRO().observed.has(nodes[idx]), false, `parked node[${idx}] unobserved by ResizeObserver`);
  }

  // VIRTUAL_KEEP_ATTRS preserves data-wb-offwindow across stashed SSE header sync.
  assert.equal(ctx.VIRTUAL_KEEP_ATTRS['data-wb-offwindow'], true);

  // releaseNode strips data-wb-offwindow, inert, style.top (used on #bgal dock & [data-max] maximize).
  ctx.WsBoardAnchor.releaseNode(nodes[1]);
  assert.equal(nodes[1].hasAttribute('data-wb-offwindow'), false);
  assert.equal(nodes[1].inert, false);
  assert.equal(nodes[1].style.top, '');

  // Re-entering window clears parked + inert and hydrates hidden topic.
  ctx.WsBoardAnchor.select(nodes[0], { smooth: false });
  assert.equal(nodes[0].hasAttribute('data-wb-offwindow'), false);
  assert.equal(nodes[0].hasAttribute('data-wb-virtual'), false);
  assert.equal(nodes[2].hasAttribute('data-wb-offwindow'), false);
  assert.equal(nodes[2].inert, false);

  // suspendForOverview strips data-board-free, board.style.minHeight, all inline style.top, offwindow & inert.
  ctx.WsBoardAnchor.select(nodes[15], { smooth: false });
  ctx.WsBoardAnchor.suspendForOverview();
  assert.equal(bcol.hasAttribute('data-board-free'), false);
  assert.equal(board.style.minHeight, '');
  for (const n of nodes) {
    assert.equal(n.hasAttribute('data-wb-offwindow'), false);
    assert.equal(n.inert, false);
    assert.equal(n.style.top, '');
  }

  // resumeFromOverview + select restores free-canvas centered on selected card.
  ctx.WsBoardAnchor.resumeFromOverview();
  ctx.WsBoardAnchor.select(nodes[8], { smooth: false });
  assert.equal(bcol.hasAttribute('data-board-free'), true);
  assert.equal(ctx.WsBoardAnchor.windowInfo().active, true);
  assert.equal(ctx.WsBoardAnchor.windowInfo().anchorIdx, 8);

  // Empty board suspends free-canvas so .empty renders normally.
  while (board.children.length) board.removeChild(board.children[0]);
  ctx.WsBoardAnchor.settle();
  assert.equal(bcol.hasAttribute('data-board-free'), false);
  assert.equal(ctx.WsBoardAnchor.windowInfo().active, false);

  // Cross-module call-site contracts in overview.js, cards.js, dashboard-conversation.js.
  assert.match(overviewSrc, /WsBoardAnchor\.suspendForOverview/);
  assert.match(overviewSrc, /WsBoardAnchor\.resumeFromOverview/);
  assert.match(overviewSrc, /WsBoardAnchor\.releaseNode/);
  assert.match(cardsSrc, /WsBoardAnchor\.releaseNode/);
  assert.match(convSrc, /kept\.h\.apply\(\);[\s\S]{0,160}WsBoardAnchor\.settle\(\)[\s\S]{0,120}boardDialSync\(\)/);
});

test('5a. stream-view.js wbHistory.keep skips compensation for above-anchor buffer cards (k < anchor) and preserves tall anchor card (k === anchor) & #bgal', () => {
  const root = makeEl('root');
  const html = makeEl('html');
  const bcol = makeEl('div', { attrs: { id: 'bcol', 'data-board-free': '' }, overflowY: 'auto', scrollTop: 50000, clientHeight: 800, scrollHeight: 100000 });
  const bgal = makeEl('div', { attrs: { id: 'bgal' }, overflowY: 'auto', scrollTop: 300, clientHeight: 800, scrollHeight: 2000 });
  const board = makeEl('div', { attrs: { id: 'r-board' } });
  const cardAbove = makeTopic('above', { height: 400 });
  const cardAnchor = makeTopic('anchor', { height: 1200 });
  const cardGal = makeTopic('docked', { height: 1000 });
  const itemAbove = makeEl('article', { cls: ['wb-entry'], attrs: { id: 'e-above' }, offsetHeight: 100 });
  const itemAnchor = makeEl('article', { cls: ['wb-entry'], attrs: { id: 'e-anchor' }, offsetHeight: 100 });
  const itemGal = makeEl('article', { cls: ['wb-entry'], attrs: { id: 'e-gal' }, offsetHeight: 100 });
  cardAbove.appendChild(itemAbove);
  cardAnchor.appendChild(itemAnchor);
  cardGal.appendChild(itemGal);
  board.appendChild(cardAbove);
  board.appendChild(cardAnchor);
  bcol.appendChild(board);
  bgal.appendChild(cardGal);
  html.appendChild(bcol);
  html.appendChild(bgal);
  root.appendChild(html);

  let anchorShift = 0;
  let galShift = 0;
  itemAbove.getBoundingClientRect = () => ({ top: -200, bottom: -100, height: 100, left: 0, right: 600, width: 600 });
  itemAnchor.getBoundingClientRect = () => ({ top: 200 + anchorShift, bottom: 300 + anchorShift, height: 100, left: 0, right: 600, width: 600 });
  itemGal.getBoundingClientRect = () => ({ top: 200 + galShift, bottom: 300 + galShift, height: 100, left: 0, right: 600, width: 600 });
  bcol.getBoundingClientRect = () => ({ top: 0, bottom: 800, height: 800, left: 0, right: 800, width: 800 });
  bgal.getBoundingClientRect = () => ({ top: 0, bottom: 800, height: 800, left: 0, right: 800, width: 800 });

  const ctx = {
    window: null,
    document: {
      documentElement: html,
      body: html,
      scrollingElement: html,
      getElementById: id => (id === 'e-anchor' ? itemAnchor : id === 'e-gal' ? itemGal : id === 'e-above' ? itemAbove : null),
      querySelectorAll: () => [],
      addEventListener: () => {},
    },
    getComputedStyle: n => ({ overflowY: n.overflowY || 'visible', display: 'block', visibility: 'visible' }),
    requestAnimationFrame: fn => { fn(); return 1; },
    cancelAnimationFrame: () => {},
    addEventListener: () => {},
    Date, Math, Set, Map, WeakMap, Array, Object,
    WsBoardAnchor: {
      windowInfo: () => ({
        active: true,
        anchorIdx: 1,
        indexOf: n => (n === cardAbove ? 0 : n === cardAnchor ? 1 : -1),
      }),
      owner: n => (cardAbove.contains(n) ? cardAbove : cardAnchor.contains(n) ? cardAnchor : null),
    },
  };
  ctx.window = ctx;
  ctx.innerHeight = 800;
  vm.createContext(ctx);
  vm.runInContext(streamViewSrc, ctx);
  const kit = ctx.viewKit(ctx);

  // Above-anchor buffer card (k=0 < anchor=1): keep returns no-op and never modifies bcol.scrollTop.
  const hAbove = kit.keep(itemAbove, { scroller: bcol, offset: 100 });
  hAbove.apply();
  hAbove.end();
  assert.equal(bcol.scrollTop, 50000, 'above-anchor buffer card skips wbHistory.keep compensation');

  // Anchor card (k=1 === anchor=1): keep compensates bcol.scrollTop when item shifts down by +150px.
  const hAnchor = kit.keep(itemAnchor, { scroller: bcol, offset: 200 });
  anchorShift = 150;
  hAnchor.apply();
  hAnchor.end();
  assert.equal(bcol.scrollTop, 50150, 'tall anchor card retains wbHistory.keep scroll compensation');

  // Docked card in #bgal: unchanged normal compensation.
  const hGal = kit.keep(itemGal, { scroller: bgal, offset: 200 });
  galShift = 90;
  hGal.apply();
  hGal.end();
  assert.equal(bgal.scrollTop, 390, '#bgal keeps normal scroll compensation');
});

test('5a2. stream-view.js wbHistory.keep on the free canvas reads its reference unit only inside the anchor card', () => {
  const root = makeEl('root');
  const html = makeEl('html');
  const bcol = makeEl('div', { attrs: { id: 'bcol', 'data-board-free': '' }, overflowY: 'auto', scrollTop: 50000, clientHeight: 800, scrollHeight: 100000 });
  const board = makeEl('div', { attrs: { id: 'r-board' } });
  const cardAnchor = makeTopic('anchor', { height: 1200 });
  const cardBelow = makeTopic('below', { height: 1200 });
  const frameAnchor = makeEl('div', { cls: ['doc-frame'], attrs: { 'data-doc-key': 'entry:e1' } });
  const frameBelow = makeEl('div', { cls: ['doc-frame'], attrs: { 'data-doc-key': 'entry:e2' } });
  cardAnchor.appendChild(frameAnchor);
  cardBelow.appendChild(frameBelow);
  board.appendChild(cardAnchor);
  board.appendChild(cardBelow);
  bcol.appendChild(board);
  html.appendChild(bcol);
  root.appendChild(html);
  // The below card's unit sits nearer the reading line than the anchor's, as
  // when the anchor's head is near the top and its frame starts lower: the
  // unscoped rule picks it.
  let anchorTop = 300, belowTop = 100;
  frameAnchor.getBoundingClientRect = () => ({ top: anchorTop, bottom: anchorTop + 100, height: 100, left: 0, right: 600, width: 600 });
  frameBelow.getBoundingClientRect = () => ({ top: belowTop, bottom: belowTop + 100, height: 100, left: 0, right: 600, width: 600 });
  bcol.getBoundingClientRect = () => ({ top: 0, bottom: 800, height: 800, left: 0, right: 800, width: 800 });
  bcol.clientWidth = 800;
  const ctx = {
    window: null,
    innerWidth: 1000,
    document: { documentElement: html, body: html, scrollingElement: html, getElementById: () => null, querySelectorAll: () => [], addEventListener: () => {} },
    getComputedStyle: n => ({ overflowY: n.overflowY || 'visible', display: 'block', visibility: 'visible' }),
    requestAnimationFrame: fn => { fn(); return 1; },
    cancelAnimationFrame: () => {},
    addEventListener: () => {},
    Date, Math, Set, Map, WeakMap, Array, Object,
    WsBoardAnchor: {
      windowInfo: () => ({ active: true, anchorIdx: 0, list: [cardAnchor, cardBelow], indexOf: n => (n === cardAnchor ? 0 : n === cardBelow ? 1 : -1) }),
      owner: n => (cardAnchor.contains(n) ? cardAnchor : cardBelow.contains(n) ? cardBelow : null),
    },
  };
  ctx.window = ctx;
  ctx.innerHeight = 800;
  vm.createContext(ctx);
  vm.runInContext(streamViewSrc, ctx);
  const kit = ctx.viewKit(ctx);

  // A relayout moves the below card 600px up while the anchor card stays: no scroll.
  let h = kit.keep(cardAnchor.firstElementChild, { scroller: bcol });
  belowTop -= 600;
  h.apply();
  assert.equal(bcol.scrollTop, 50000, 'another card moving never scrolls the free canvas');

  // Content above the reading line inside the anchor card still keeps its place.
  belowTop = 100;
  h = kit.keep(cardAnchor.firstElementChild, { scroller: bcol });
  anchorTop += 150;
  h.apply();
  assert.equal(bcol.scrollTop, 50150, 'the anchor card unit keeps its compensation');

  // No anchor unit in view: no reference, so a moving neighbour is ignored.
  anchorTop = -500;
  h = kit.keep(cardAnchor.firstElementChild, { scroller: bcol });
  belowTop -= 600;
  h.apply();
  assert.equal(bcol.scrollTop, 50150, 'without an anchor unit in view nothing is compensated');
});

test('5b. rail.js free-canvas reads rects ONLY for mounted window cards, computes offBefore/offAfter capsules & +N counts, and skips dropGhost on #r-board height resize', () => {
  const root = makeEl('root');
  const html = makeEl('html');
  const bcol = makeEl('div', {
    attrs: { id: 'bcol', 'data-board-free': '' },
    overflowY: 'auto',
    scrollTop: 50000,
    clientHeight: 800,
    scrollHeight: 100000,
  });
  const board = makeEl('div', { attrs: { id: 'r-board' } });
  bcol.appendChild(board);
  html.appendChild(bcol);
  root.appendChild(html);

  const rectReads = new Map();
  const topics = [];
  for (let i = 0; i < 20; i++) {
    const t = makeTopic(`t${i}`, { height: 400 });
    t.children[0].appendChild(makeEl('span', { cls: ['tname'], text: `Topic ${i}` }));
    const idx = i;
    t.getBoundingClientRect = () => {
      rectReads.set(idx, (rectReads.get(idx) || 0) + 1);
      // Window is [7..13]: place t7, t8 above viewport; t9 current spanning top; t10 visible; t11..t13 below bottom line.
      const top = (idx - 9) * 420 - 40;
      return { top, bottom: top + 400, height: 400, left: 0, right: 800, width: 800 };
    };
    board.appendChild(t);
    topics.push(t);
  }
  bcol.getBoundingClientRect = () => ({ top: 0, bottom: 800, height: 800, left: 0, right: 800, width: 800 });

  const railROs = [];
  class RailRO {
    constructor(cb) { this.cb = cb; railROs.push(this); }
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  const rafs = [];
  const docEl = {
    documentElement: html,
    body: html,
    scrollingElement: html,
    getElementById: id => (id === 'bcol' ? bcol : id === 'r-board' ? board : null),
    createElement: tag => makeEl(tag),
    querySelector: () => null,
    querySelectorAll: () => [],
    addEventListener: () => {},
  };
  const ctx = {
    window: null,
    document: docEl,
    ResizeObserver: RailRO,
    MutationObserver: class { observe() {} disconnect() {} },
    requestAnimationFrame: fn => { rafs.push(fn); return rafs.length; },
    cancelAnimationFrame: () => {},
    getComputedStyle: n => ({ overflowY: n.overflowY || 'visible', paddingTop: '0px', paddingBottom: '0px', fontSize: '13px', color: '#000', backgroundColor: '#fff', borderRadius: '8px', getPropertyValue: () => '' }),
    matchMedia: () => ({ matches: true }),
    addEventListener: () => {},
    Math, Set, Map, Array, Object, String, Number, parseFloat,
    WsBoardAnchor: {
      windowInfo: () => ({
        active: true,
        start: 7,
        end: 13,
        anchorIdx: 9,
        list: topics,
        railList: topics,
        railBefore: topics.map((_, i) => i),
        railTotal: topics.length,
      }),
    },
  };
  ctx.window = ctx;
  ctx.innerHeight = 800;
  vm.createContext(ctx);
  vm.runInContext(railSrc, ctx);
  while (rafs.length) rafs.shift()();

  // Only mounted window cards [7..13] had getBoundingClientRect called (0 reads on [0..6] and [14..19]).
  for (let i = 0; i < 20; i++) {
    const reads = rectReads.get(i) || 0;
    if (i >= 7 && i <= 13) assert.ok(reads > 0, `mounted topic ${i} measured`);
    else assert.equal(reads, 0, `off-window topic ${i} must have 0 rect reads`);
  }

  // Top capsules show far (with +upMore) and near; bottom includes off-window downMore > 0.
  const topWrap = bcol.children.find(c => c.classList.contains('wb-rail-wrap') && c.classList.contains('up'));
  const botWrap = bcol.children.find(c => c.classList.contains('wb-rail-wrap') && c.classList.contains('dn'));
  assert.ok(topWrap && botWrap, 'rail wrappers mounted');
  const topBtns = topWrap.querySelectorAll('.wb-cap').filter(b => !b.hidden);
  const botBtns = botWrap.querySelectorAll('.wb-cap').filter(b => !b.hidden);
  assert.equal(topBtns.length, 2, '2 top capsules mounted');
  const topFar = topWrap.querySelector('.wb-cap.far');
  assert.equal(topFar.querySelector(':scope > .cx').textContent, '+7', 'top +N reflects offBefore=7 + mounted above');
  assert.equal(botBtns.length, 2, '2 bottom capsules mounted');
  const botFar = botWrap.querySelector('.wb-cap.far');
  const botExtra = botFar.querySelector(':scope > .cx').textContent;
  assert.ok(botExtra.startsWith('+') && parseInt(botExtra.slice(1), 10) >= 6, `bottom +N (${botExtra}) reflects offAfter=6`);

  // ResizeObserver callback for #r-board height-only change in free-canvas kicks without dropping ghosts.
  const ro = railROs[0];
  assert.ok(ro, 'rail ResizeObserver registered');
  const rafBefore = rafs.length;
  ro.cb([{ target: board, contentRect: { width: 800, height: 100000 } }]);
  assert.ok(rafs.length > rafBefore, '#r-board resize schedules kick()');
  assert.match(railSrc, /if \(onlyBoardHeight\) \{ kick\(\); return; \}/);
});

test('5c. pill.js classifies open asks inside data-wb-offwindow topics via WsBoardAnchor window indices instead of dropping 0x0 rects', () => {
  const root = makeEl('root');
  const html = makeEl('html');
  const bcol = makeEl('div', {
    attrs: { id: 'bcol', 'data-board-free': '' },
    overflowY: 'auto',
    scrollTop: 50000,
    clientHeight: 800,
    rect: () => ({ top: 0, bottom: 800, height: 800, left: 0, right: 800, width: 800 }),
  });
  const board = makeEl('div', { attrs: { id: 'r-board' } });
  bcol.appendChild(board);
  html.appendChild(bcol);
  root.appendChild(html);

  // Build 4 topics: t0 (offwindow above), t1 (offwindow above, filtered -> skip),
  // t2 (mounted visible), t3 (offwindow parked below).
  const t0 = makeTopic('t0'); t0.setAttribute('data-wb-offwindow', 'hidden');
  const t1 = makeTopic('t1'); t1.setAttribute('data-wb-offwindow', 'hidden'); t1.setAttribute('data-state-filtered', '');
  const t2 = makeTopic('t2');
  t2.getBoundingClientRect = () => ({ top: 100, bottom: 500, height: 400, left: 0, right: 800, width: 800 });
  const t3 = makeTopic('t3'); t3.setAttribute('data-wb-offwindow', 'parked');

  const ask0 = makeEl('div', { cls: ['card', 'policy'], attrs: { id: 'item-0', 'data-state': 'open' }, rect: () => ({ top: 0, bottom: 0, height: 0, width: 0 }) });
  const ask1 = makeEl('div', { cls: ['card', 'policy'], attrs: { id: 'item-1', 'data-state': 'open' }, rect: () => ({ top: 0, bottom: 0, height: 0, width: 0 }) });
  const ask2 = makeEl('div', { cls: ['card', 'policy'], attrs: { id: 'item-2', 'data-state': 'open' }, rect: () => ({ top: 200, bottom: 300, height: 100, width: 600 }) });
  const ask3 = makeEl('div', { cls: ['card', 'policy'], attrs: { id: 'item-3', 'data-state': 'open' }, rect: () => ({ top: 0, bottom: 0, height: 0, width: 0 }) });
  t0.appendChild(ask0); t1.appendChild(ask1); t2.appendChild(ask2); t3.appendChild(ask3);
  [t0, t1, t2, t3].forEach(t => board.appendChild(t));

  const rafs = [];
  const idxMap = new Map([[t0, 0], [t2, 2], [t3, 3]]);
  const docEl = {
    documentElement: html,
    body: html,
    scrollingElement: html,
    getElementById: id => (id === 'bcol' ? bcol : id === 'r-board' ? board : null),
    createElement: tag => makeEl(tag),
    querySelector: sel => (sel === '.topic' ? t0 : null),
    querySelectorAll: sel => (sel.includes('.card.policy') ? [ask0, ask1, ask2, ask3] : []),
    addEventListener: () => {},
    removeEventListener: () => {},
  };
  const ctx = {
    window: null,
    document: docEl,
    location: { pathname: '/s/free/' },
    localStorage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
    IntersectionObserver: class { observe() {} disconnect() {} },
    ResizeObserver: class { observe() {} disconnect() {} },
    requestAnimationFrame: fn => { rafs.push(fn); return rafs.length; },
    cancelAnimationFrame: () => {},
    getComputedStyle: n => ({ overflowY: n.overflowY || 'visible' }),
    addEventListener: () => {},
    removeEventListener: () => {},
    wsT: s => s,
    Math, Set, Map, Array, Object, String, Number, JSON,
    WsBoardAnchor: {
      windowInfo: () => ({
        active: true,
        start: 2,
        end: 2,
        indexOf: n => (idxMap.has(n) ? idxMap.get(n) : -1),
      }),
    },
  };
  ctx.window = ctx;
  ctx.innerHeight = 800;
  vm.createContext(ctx);
  vm.runInContext(pillSrc, ctx);
  while (rafs.length) rafs.shift()();

  // Inspect actual top and bottom pills produced by shipped pill.js!
  const topWrap = bcol.children.find(c => c.classList.contains('wb-oq-top'));
  const botWrap = bcol.children.find(c => c.classList.contains('wb-oq-bot'));
  assert.ok(topWrap && botWrap, 'top and bottom .oq-wrap created by pill.js');
  const topOpenBtn = topWrap.querySelectorAll('.oq-seg').find(b => b.getAttribute('data-k') === 'open');
  const botOpenBtn = botWrap.querySelectorAll('.oq-seg').find(b => b.getAttribute('data-k') === 'open');
  assert.ok(topOpenBtn && !topOpenBtn.hidden, 'top open segment visible for off-window ask above window');
  assert.equal(topOpenBtn.querySelector('.n').textContent.trim(), '1', 'filtered t1 skipped; 1 open ask above window');
  assert.ok(botOpenBtn && !botOpenBtn.hidden, 'bottom open segment visible for off-window parked ask below window');
  assert.equal(botOpenBtn.querySelector('.n').textContent.trim(), '1', '1 open ask below window');
});

test('6. Smooth select (mounted vs off-window), anchorMO inset preserve, and post-scrub deferred collapse', () => {
  const scrollToCalls = [];
  let moCallback = null;
  class FakeMO {
    constructor(cb) { this.cb = cb; }
    observe(target) {
      if (target && target.id === 'bcol') moCallback = this.cb;
    }
    takeRecords() { return []; }
    disconnect() {}
  }

  const spec25 = Array.from({ length: 25 }, (_, i) => ({ topic: `t${i}`, height: 400 }));
  const h = makeFreeCanvasWorld(spec25, { viewH: 800, toolbarH: 40 });

  // Enable smooth scroll path (!reducedMotion) and spy on scrollTo.
  h.ctx.reducedMotion = () => false;
  const origScrollTo = h.bcol.scrollTo.bind(h.bcol);
  h.bcol.scrollTo = arg => {
    scrollToCalls.push(arg);
    if (arg && typeof arg === 'object' && arg.behavior === 'smooth') {
      // Simulate async smooth scroll: scrollTop stays at old value until frames tick!
      return;
    }
    origScrollTo(arg);
  };

  // 6b-1: Smooth jump to an ALREADY-MOUNTED neighbor preserves its freePos Y (no 50000px teleport) and uses smooth scrollTo.
  const y1Before = parseFloat(h.nodes[1].style.top);
  assert.ok(y1Before < 2000, 'Card 1 starts near topPad (clamped window)');
  h.ctx.WsBoardAnchor.select(h.nodes[1], { smooth: true });
  assert.equal(parseFloat(h.nodes[1].style.top), y1Before, 'smooth select on mounted neighbor preserves its Y without teleporting to 50000');
  assert.equal(scrollToCalls.length, 1, 'smooth select on mounted neighbor invokes scrollTo');
  assert.equal(scrollToCalls[0].behavior, 'smooth', 'mounted neighbor scroll uses smooth behavior');

  // 6b-2: Smooth select to an OFF-WINDOW card (Card 20) sets scrollTop synchronously (never smooth-scrolls across 50000px blank runway).
  scrollToCalls.length = 0;
  h.ctx.WsBoardAnchor.select(h.nodes[20], { smooth: true });
  assert.deepEqual(scrollToCalls.map(call => call.behavior), ['instant'], 'off-window jump cancels the outstanding smooth scroll');
  assert.ok(h.bcol.scrollTop > 40000, 'off-window jump synchronously moves scrollTop to Card 20 near 50000px');
  assert.equal(h.ctx.WsBoardAnchor.windowInfo().anchorIdx, 20, 'anchorIdx is 20 after off-window jump');

  // 6c: dialScrubbing defers virtualCollapse during scrub and flushes deferred collapses once scrubbing ends.
  h.ctx.WsBoardAnchor.select(h.nodes[0], { smooth: false });
  assert.equal(h.nodes[0].hasAttribute('data-wb-virtual'), false, 'Card 0 starts hydrated');
  h.ctx.dialScrubbing = true;
  h.ctx.WsBoardAnchor.select(h.nodes[15], { smooth: false });
  assert.equal(h.nodes[0].getAttribute('data-wb-offwindow'), 'hidden', 'Card 0 marked hidden during scrub');
  assert.equal(h.nodes[0].hasAttribute('data-wb-virtual'), false, 'Card 0 body collapse deferred while dialScrubbing=true');
  // End scrub on the SAME target (prevStart..prevEnd === start..end) and verify Card 0 collapses!
  h.ctx.dialScrubbing = false;
  h.ctx.WsBoardAnchor.select(h.nodes[15], { smooth: false });
  assert.equal(h.nodes[0].hasAttribute('data-wb-virtual'), true, 'deferred Card 0 collapses once dialScrubbing ends');
  assert.equal(h.nodes[15].hasAttribute('data-wb-virtual'), false, 'target Card 15 is hydrated once dialScrubbing ends');

  // 6a: anchorMO --wb-rail-inset mutation when freeWinStart === 0 preserves freeAnchorIdx (> 0).
  const spec15 = Array.from({ length: 15 }, (_, i) => ({ topic: `m${i}`, height: 400 }));
  const hMO = makeFreeCanvasWorld(spec15, { viewH: 800, toolbarH: 40 });
  // Attach a fake MO capturing #bcol's observer by re-running with MutationObserver stub
  hMO.ctx.MutationObserver = FakeMO;
  hMO.ctx.anchorInit();
  hMO.ctx.WsBoardAnchor.select(hMO.nodes[2], { smooth: false });
  assert.equal(hMO.ctx.WsBoardAnchor.windowInfo().start, 0, 'Card 2 window is top-clamped (winStart === 0)');
  assert.equal(hMO.ctx.WsBoardAnchor.windowInfo().anchorIdx, 2, 'anchorIdx is 2 before inset mutation');
  hMO.bcol.style.setProperty('--wb-rail-inset', '64px');
  assert.ok(moCallback, 'anchorMO registered on #bcol');
  moCallback();
  assert.equal(hMO.ctx.WsBoardAnchor.windowInfo().anchorIdx, 2, 'anchorMO inset change preserves freeAnchorIdx (does not reset to 0)');
});

test('7. Scrolling upward from bottom defers start===0 runway rebase until idle (or top reach) and completes cleanly on freeIdleRecenter', () => {
  // 8 cards (320px each, viewH=800 -> need=1200px -> 4 cards above anchor):
  // jumping to the bottom card (index 7) places anchorY = 50000 with start = 3 > 0,
  // and scrolling upward to card 4 hits start === 0 while the viewport is still
  // well below ys[0] (~47648px).
  const spec = Array.from({ length: 8 }, (_, i) => ({ topic: `u${i}`, height: 320 }));
  const w = makeFreeCanvasWorld(spec, { viewH: 800, toolbarH: 40, closedMore: true, closedMoreH: 40 });
  const { ctx, nodes, bcol, board, moreBtn } = w;
  const topPad = 40 + 16; // toolbarH + FREE_CARD_GAP

  // Jump to bottom card (index 7): anchorY = 50000, minHeight ~ 50424px.
  assert.equal(ctx.WsBoardAnchor.select(nodes[7], { smooth: false }), true);
  let info = ctx.WsBoardAnchor.windowInfo();
  assert.equal(info.anchorIdx, 7);
  assert.equal(info.posOf(nodes[7]), 50000);
  assert.ok(info.start > 0, `initial jump to bottom leaves start=${info.start} > 0`);
  const initialMinH = parseFloat(board.style.minHeight);
  assert.ok(initialMinH > 50000, `initial minHeight=${initialMinH} > 50000`);

  // Simulate user scrolling upward toward card 4 (still well below y_0 ~ 47648).
  const y4 = info.posOf(nodes[4]);
  bcol.scrollTop = y4;
  ctx.freeOnScroll();
  w.flushRaf();

  info = ctx.WsBoardAnchor.windowInfo();
  assert.equal(info.start, 0, 'upward pre-mount window reaches start === 0');
  assert.equal(bcol.scrollTop, y4, 'scrollTick must NOT jump scrollTop mid-gesture while viewport > ys[0]');
  assert.ok(info.posOf(nodes[0]) > 40000, `ys[0]=${info.posOf(nodes[0])} stays on high runway during active scroll`);
  assert.equal(parseFloat(board.style.minHeight), initialMinH, 'minHeight stays stable during active scroll');

  // Record visual offset of card 4 before idle recenter.
  const visualBeforeIdle = info.posOf(nodes[4]) - bcol.scrollTop;

  // Idle timer fires: freeIdleRecenter rebases ys[0] to topPad, shifts scrollTop
  // with 0px visual delta, updates closed-more, and shrinks board.style.minHeight.
  w.fireIdle();
  info = ctx.WsBoardAnchor.windowInfo();
  assert.equal(info.posOf(nodes[0]), topPad, 'idle recenter rebases ys[0] to topPad');
  assert.equal(info.posOf(nodes[4]) - bcol.scrollTop, visualBeforeIdle, 'idle top-boundary rebase preserves 0px visual offset');
  const shrunkMinH = parseFloat(board.style.minHeight);
  assert.ok(shrunkMinH < 5000, `idle recenter shrinks minHeight to tight board height (${shrunkMinH}px)`);
  assert.equal(moreBtn.style.top, Math.round(info.posOf(nodes[7]) + 320 + 16) + 'px', 'closed-more repositioned after idle rebase');

  // Edge case: continuous upward swipe all the way past ys[0] after an Overview
  // jump to the bottom card rebases immediately so the user cannot scroll into
  // 47000px of empty runway above card 0.
  ctx.WsBoardAnchor.suspendForOverview();
  ctx.WsBoardAnchor.resumeFromOverview();
  ctx.WsBoardAnchor.select(nodes[7], { smooth: false });
  const y4Again = ctx.WsBoardAnchor.windowInfo().posOf(nodes[4]);
  bcol.scrollTop = y4Again;
  ctx.freeOnScroll();
  w.flushRaf();
  const y0High = ctx.WsBoardAnchor.windowInfo().posOf(nodes[0]);
  assert.ok(y0High > 40000, 'second upward scroll defers rebase while scrollTop > ys[0]');

  // Now swipe past ys[0] into the runway above card 0.
  bcol.scrollTop = y0High - 200;
  ctx.freeOnScroll();
  w.flushRaf();
  assert.equal(ctx.WsBoardAnchor.windowInfo().posOf(nodes[0]), topPad, 'swiping past ys[0] rebases card 0 to topPad immediately');
  assert.equal(bcol.scrollTop, 0, 'scrollTop clamps at 0 instead of entering empty runway above card 0');
});

test('8. WsBoardAnchor.keepFrame protects active mounted window cards, freeOnCardResize defers y0 > topPad mid-scroll, and CSS disables overflow-anchor under data-board-free', () => {
  const spec = Array.from({ length: 16 }, (_, i) => ({ topic: `k${i}`, height: 320 }));
  const w = makeFreeCanvasWorld(spec, { viewH: 800, toolbarH: 40 });
  const { ctx, nodes, bcol } = w;
  const topPad = 40 + 16;

  // Jump to middle card 8: active window cards are protected by keepFrame even when idle,
  // while off-window evicted cards return false so FAR IO still unmounts them.
  ctx.WsBoardAnchor.select(nodes[8], { smooth: false });
  let info = ctx.WsBoardAnchor.windowInfo();
  for (let i = 0; i < nodes.length; i++) {
    const inWin = i >= info.start && i <= info.end;
    assert.equal(ctx.WsBoardAnchor.keepFrame(nodes[i]), inWin, `keepFrame(node[${i}]) === ${inWin}`);
  }

  // Simulate active scroll with winStart === 0 and y0 > topPad (deferred top rebase).
  const spec8 = Array.from({ length: 8 }, (_, i) => ({ topic: `r${i}`, height: 320 }));
  const w2 = makeFreeCanvasWorld(spec8, { viewH: 800, toolbarH: 40 });
  w2.ctx.WsBoardAnchor.select(w2.nodes[7], { smooth: false });
  const y4 = w2.ctx.WsBoardAnchor.windowInfo().posOf(w2.nodes[4]);
  w2.bcol.scrollTop = y4;
  w2.ctx.freeOnScroll();
  w2.flushRaf();
  assert.equal(w2.ctx.WsBoardAnchor.windowInfo().start, 0);
  const y0BeforeResize = w2.ctx.WsBoardAnchor.windowInfo().posOf(w2.nodes[0]);
  assert.ok(y0BeforeResize > topPad);

  // Above-anchor card 2 resizes while freeIdleTimer is active: must NOT snap y0 to topPad via freeShiftScroll.
  w2.nodes[2].offsetHeight = 420;
  w2.cardRO().fire([{ target: w2.nodes[2], borderBoxSize: [{ blockSize: 420 }], contentRect: { height: 420 } }]);
  assert.equal(w2.bcol.scrollTop, y4, 'freeOnCardResize does not call freeShiftScroll to snap y0 > topPad mid-scroll');
  assert.ok(w2.ctx.WsBoardAnchor.windowInfo().posOf(w2.nodes[0]) > 40000, 'y0 remains on high runway until idle');

  // Once idle fires, deferred rebase completes cleanly.
  w2.fireIdle();
  assert.equal(w2.ctx.WsBoardAnchor.windowInfo().posOf(w2.nodes[0]), topPad, 'idle completes rebase deferred by freeOnCardResize');

  // When y0 < topPad during active scroll (card 0 grows upward past topPad), freeOnCardResize MUST still shift downward.
  w2.bcol.scrollTop = w2.ctx.WsBoardAnchor.windowInfo().posOf(w2.nodes[2]);
  const stBeforeGrow = w2.bcol.scrollTop;
  w2.ctx.freeOnScroll(); // arm freeIdleTimer
  w2.nodes[0].offsetHeight = 420; // +100px above anchor pushes y0 to topPad - 100 < topPad
  w2.cardRO().fire([{ target: w2.nodes[0], borderBoxSize: [{ blockSize: 420 }], contentRect: { height: 420 } }]);
  assert.equal(w2.ctx.WsBoardAnchor.windowInfo().posOf(w2.nodes[0]), topPad, 'y0 < topPad still shifts downward immediately');
  assert.equal(w2.bcol.scrollTop, stBeforeGrow + 100, 'scrollTop compensates positive downward shift');

  // CSS contract: #bcol[data-board-free] disables native overflow-anchor with higher specificity than .dgrid.<mode> > .bcol.
  assert.match(boardCssSrc, /\.dgrid:is\(\.live,\s*\.history,\s*\.worktree\)\s*>\s*#bcol\[data-board-free\]\s*\{\s*overflow-anchor:\s*none;\s*\}/);
});

test('evicted-frame reload: shell-held card heights seed the runway and the saved reading offset lands once', () => {
  const spec = Array.from({ length: 16 }, (_, i) => ({ topic: `t${i}`, height: 300 + i * 20 }));
  // The shell table (dashboard-session-view.js boardHeightCache) survives the
  // frame; a fresh anchor module reads and writes the same Map.
  const shared = new Map([['t:t9', 777]]);
  const taken = [];
  const w = makeFreeCanvasWorld(spec, {
    viewH: 800,
    store: { 'ws.dashboard.board-card./s/free/': 't:t6' },
    extra: { boardHeightCache: () => shared, boardOffsetTake: key => { taken.push(key); return 130; } },
  });
  const { ctx, nodes, bcol } = w;
  assert.equal(vm.runInContext('freeHeightCache', ctx), shared, 'anchor uses the shell-held table');
  assert.deepEqual(taken, ['t:t6'], 'offset is taken once, for the restored card');
  const y6 = ctx.WsBoardAnchor.windowInfo().posOf(nodes[6]);
  assert.equal(bcol.scrollTop, Math.round(y6 - 130), 'restored card lands at its saved reading line, not centered');
  assert.equal(shared.get('t:t6'), 420, 'measured heights write through to the shell table');
  // A later select centers as before: the offset applied only to the restore.
  ctx.WsBoardAnchor.select(nodes[12], { smooth: false });
  assert.deepEqual(taken, ['t:t6']);
});

test('9. Uncached card entering via scrollTick reconciles real height without overlap and caches 320px sub-eps match', () => {
  const spec = Array.from({ length: 24 }, (_, i) => ({ topic: `t${i}`, height: 320 }));
  const w = makeFreeCanvasWorld(spec, { viewH: 800, toolbarH: 40 });
  const { ctx, nodes, bcol } = w;

  let info = ctx.WsBoardAnchor.windowInfo();
  assert.ok(info.end < 10, `expected initial window end (${info.end}) before card 10`);
  assert.equal(ctx.freeHeightCache.has('t:t12'), false);
  assert.equal(ctx.freeHeightCache.has('t:t13'), false);

  // Simulate tall DOM card at index 12 whose height is not yet in freeHeightCache.
  nodes[12].offsetHeight = 900;

  // Scroll down via scrollTick until cards 12 and 13 enter the mounted window while viewTopY is above card 12.
  for (let step = 0; step < 30; step++) {
    info = ctx.WsBoardAnchor.windowInfo();
    if (info.end >= 14 && info.start <= 12) break;
    bcol.scrollTop += 400;
    ctx.freeOnScroll();
    w.flushRaf();
  }
  info = ctx.WsBoardAnchor.windowInfo();
  assert.ok(info.start <= 12 && info.end >= 14, `cards 12..14 in window: ${info.start}..${info.end}`);
  assert.ok(info.anchorIdx < 12, `anchor (${info.anchorIdx}) stays above card 12`);
  assert.equal(ctx.freeHeightCache.has('t:t12'), false, 'scrollTick leaves uncached card 12 out of cache before RO');
  assert.equal(info.heightOf(nodes[12]), 320, 'scrollTick places uncached card at 320px fallback initially');

  const pos12Before = info.posOf(nodes[12]);
  const pos13Before = info.posOf(nodes[13]);
  const pos14Before = info.posOf(nodes[14]);
  assert.equal(pos13Before - pos12Before, 320 + 16);

  // ResizeObserver reconciles card 12 (900px) and card 13 (320px exact fallback match).
  w.cardRO().fire([
    { target: nodes[12], borderBoxSize: [{ blockSize: 900 }], contentRect: { height: 900 } },
    { target: nodes[13], borderBoxSize: [{ blockSize: 320 }], contentRect: { height: 320 } },
  ]);

  assert.equal(ctx.freeHeightCache.get('t:t12'), 900, 'uncached resized card populates freeHeightCache');
  assert.equal(ctx.freeHeightCache.get('t:t13'), 320, 'uncached 320px card populates freeHeightCache even with dh === 0');

  const infoAfter = ctx.WsBoardAnchor.windowInfo();
  assert.equal(infoAfter.heightOf(nodes[12]), 900);
  assert.equal(infoAfter.posOf(nodes[12]), pos12Before, 'below-anchor card 12 keeps its top');
  assert.equal(infoAfter.posOf(nodes[13]), pos13Before + 580, 'sibling card 13 shifts by +580px');
  assert.equal(nodes[13].style.top, `${Math.round(pos13Before + 580)}px`);
  assert.equal(infoAfter.posOf(nodes[14]), pos14Before + 580, 'sibling card 14 shifts by +580px');
  for (let i = infoAfter.start; i < infoAfter.end; i++) {
    const gap = infoAfter.posOf(nodes[i + 1]) - (infoAfter.posOf(nodes[i]) + infoAfter.heightOf(nodes[i]));
    assert.ok(gap >= 16, `zero overlap between mounted cards ${i} and ${i + 1}: gap=${gap}`);
  }
});

test('10. Multi-entry RO batch, tall straddling anchor, duplicate batch entry, and cache clear stability', () => {
  const spec = Array.from({ length: 24 }, (_, i) => ({ topic: `m${i}`, height: 320 }));
  const w = makeFreeCanvasWorld(spec, { viewH: 800, toolbarH: 40 });
  const { ctx, nodes, bcol } = w;

  let info = ctx.WsBoardAnchor.windowInfo();
  for (let step = 0; step < 30; step++) {
    info = ctx.WsBoardAnchor.windowInfo();
    if (info.start <= 11 && info.end >= 15) break;
    bcol.scrollTop += 400;
    ctx.freeOnScroll();
    w.flushRaf();
  }
  info = ctx.WsBoardAnchor.windowInfo();
  assert.ok(info.start <= 11 && info.end >= 15, `expected window covering 11..15, got ${info.start}..${info.end}`);
  assert.equal(ctx.freeHeightCache.has('t:m11'), false);
  assert.equal(ctx.freeHeightCache.has('t:m12'), false);
  assert.equal(ctx.freeHeightCache.has('t:m13'), false);

  nodes[11].offsetHeight = 900;
  nodes[12].offsetHeight = 700;
  nodes[13].offsetHeight = 800;

  // Position viewTopY inside card 12 (anchor === 12).
  const anchorYBefore = info.posOf(nodes[12]);
  bcol.scrollTop = anchorYBefore + 120;

  // Multi-entry batch touching above-anchor (11), anchor (12 + duplicate 12), below-anchor (13).
  w.cardRO().fire([
    { target: nodes[11], borderBoxSize: [{ blockSize: 900 }], contentRect: { height: 900 } },
    { target: nodes[12], borderBoxSize: [{ blockSize: 700 }], contentRect: { height: 700 } },
    { target: nodes[13], borderBoxSize: [{ blockSize: 800 }], contentRect: { height: 800 } },
    { target: nodes[12], borderBoxSize: [{ blockSize: 700 }], contentRect: { height: 700 } },
  ]);

  const afterBatch = ctx.WsBoardAnchor.windowInfo();
  assert.equal(afterBatch.posOf(nodes[12]), anchorYBefore, 'anchor card keeps exact pos during multi-entry batch');
  assert.equal(afterBatch.posOf(nodes[11]) + 900 + 16, afterBatch.posOf(nodes[12]), 'above-anchor card 11 abuts anchor cleanly');
  assert.equal(afterBatch.posOf(nodes[12]) + 700 + 16, afterBatch.posOf(nodes[13]), 'below-anchor card 13 abuts anchor without duplicate double-shift');
  assert.equal(afterBatch.posOf(nodes[13]) + 800 + 16, afterBatch.posOf(nodes[14]), 'card 14 shifts by exact combined delta');

  // Clear freeHeightCache while cards remain mounted, then straddle tall card 11 past 320px fallback.
  ctx.freeHeightCache.clear();
  assert.equal(ctx.WsBoardAnchor.windowInfo().heightOf(nodes[11]), 900, 'freeLaidHeight retains 900px despite cache clear');
  const y11BeforeGrow = ctx.WsBoardAnchor.windowInfo().posOf(nodes[11]);
  const y12BeforeGrow = ctx.WsBoardAnchor.windowInfo().posOf(nodes[12]);
  bcol.scrollTop = y11BeforeGrow + 500;
  ctx.freeOnScroll();
  w.flushRaf();
  assert.equal(ctx.WsBoardAnchor.windowInfo().anchorIdx, 11, 'freeFindTopVisibleIdx picks tall card 11 (900px) straddling viewTopY');

  // Further resize of card 11 computes delta against freeLaidH (900 -> 960 = +60px, not +640px).
  nodes[11].offsetHeight = 960;
  w.cardRO().fire([
    { target: nodes[11], borderBoxSize: [{ blockSize: 960 }], contentRect: { height: 960 } },
  ]);
  const afterGrow = ctx.WsBoardAnchor.windowInfo();
  assert.equal(afterGrow.posOf(nodes[11]), y11BeforeGrow, 'straddling anchor card 11 stays pinned on resize');
  assert.equal(afterGrow.posOf(nodes[12]), y12BeforeGrow + 60, 'card 12 shifts by +60px rather than falling back to 320px');
  assert.equal(afterGrow.posOf(nodes[11]) + 960 + 16, afterGrow.posOf(nodes[12]));
});

test('11. releaseNode({ keepCoords: true }) and freeMeasureHeight([data-max]) preserve drifted freePos and laid/cached heights across maximize/restore', () => {
  const spec = Array.from({ length: 20 }, (_, i) => ({
    topic: `mx${i}`,
    height: i === 10 ? 2200 : 400,
  }));
  const w = makeFreeCanvasWorld(spec, { viewH: 800 });
  const { ctx, nodes, bcol } = w;

  // Place node 14 at FREE_RUNWAY_MID (50000), then scroll up to node 10 so its
  // freePos drifts well below 50000 while staying in-window.
  ctx.WsBoardAnchor.select(nodes[14], { smooth: false });
  for (let step = 0; step < 12; step++) {
    const info = ctx.WsBoardAnchor.windowInfo();
    if (info.posOf(nodes[10]) !== undefined) break;
    bcol.scrollTop = Math.max(0, bcol.scrollTop - 600);
    ctx.freeOnScroll();
    w.flushRaf();
  }
  const driftedY = ctx.WsBoardAnchor.windowInfo().posOf(nodes[10]);
  assert.ok(typeof driftedY === 'number' && Math.abs(driftedY - 50000) > 1000, `node 10 drifted from 50000: ${driftedY}`);
  bcol.scrollTop = driftedY + 900;
  ctx.freeOnScroll();
  w.flushRaf();
  assert.equal(ctx.WsBoardAnchor.key(), 't:mx10');
  assert.equal(ctx.freeHeightCache.get('t:mx10'), 2200);

  // Maximize: releaseNode with keepCoords clears style.top & RO without wiping freePos/freeLaidH.
  ctx.WsBoardAnchor.releaseNode(nodes[10], { keepCoords: true });
  nodes[10].setAttribute('data-max', '');
  nodes[10].offsetHeight = 736; // simulated viewport-sized fixed box
  assert.equal(nodes[10].style.top, '', 'inline style.top cleared for position: fixed');
  assert.equal(w.cardRO().observed.has(nodes[10]), false, 'ResizeObserver unobserved while maximized');
  assert.equal(ctx.WsBoardAnchor.windowInfo().posOf(nodes[10]), driftedY, 'drifted freePos preserved across maximize');
  assert.equal(ctx.WsBoardAnchor.windowInfo().heightOf(nodes[10]), 2200, 'freeLaidH preserved across maximize');

  // Settle while maximized must NOT overwrite freeHeightCache/freeLaidH or style.top.
  ctx.WsBoardAnchor.settle();
  assert.equal(ctx.freeHeightCache.get('t:mx10'), 2200, 'freeHeightCache not polluted by fixed viewport height');
  assert.equal(ctx.WsBoardAnchor.windowInfo().heightOf(nodes[10]), 2200, 'freeLaidH not polluted during settle');
  assert.equal(ctx.WsBoardAnchor.windowInfo().posOf(nodes[10]), driftedY, 'drifted freePos unchanged during settle');
  assert.equal(nodes[10].style.top, '', 'style.top stays empty while data-max is set');

  // Restore: remove data-max, restore natural height, settle restores drifted Y, style.top, and RO.
  nodes[10].removeAttribute('data-max');
  nodes[10].offsetHeight = 2200;
  ctx.WsBoardAnchor.settle();
  assert.equal(ctx.WsBoardAnchor.windowInfo().posOf(nodes[10]), driftedY, 'restore keeps drifted Y (never falls back to 50000)');
  assert.equal(nodes[10].style.top, Math.round(driftedY) + 'px', 'inline style.top restored');
  assert.equal(w.cardRO().observed.has(nodes[10]), true, 'ResizeObserver re-observed after restore');
});
