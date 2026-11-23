// Offline contracts for the Main Board card anchor: shipped dashboard-board-anchor.js,
// dashboard-board.js and dashboard-frames.js functions in node:vm over a fake DOM.
// The fake swap models only DOM replacement; staging, settling, apply and home are shipped.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');
const G = require('../assets/gallery.js');
const anchorSrc = readAssetSource('wsdashboard', 'dashboard-board-anchor.js');
const boardSrc = readAssetSource('wsdashboard', 'dashboard-board.js');
const framesSrc = readAssetSource('wsdashboard', 'dashboard-frames.js');
function funcs(source, names) {
  return names.map(name => {
    const found = source.match(new RegExp('\\n  function ' + name + '\\([^\\n]*\\{[^\\n]*\\}\\n|\\n  function ' + name + '\\([\\s\\S]*?\\n  \\}\\n'));
    assert.ok(found, 'missing shipped function ' + name);
    return found[0];
  }).join('');
}
function vars(source, names) {
  return names.map(name => {
    const found = source.match(new RegExp('\\n  var ' + name + '\\b[^\\n]*\\n'));
    assert.ok(found, 'missing shipped var ' + name);
    return found[0];
  }).join('');
}
const shipped = vars(anchorSrc, ['ANCHOR_STORE', 'anchorKey', 'anchorBtns', 'anchorLastCard', 'ANCHOR_OWN', 'anchorBurst']) +
  funcs(anchorSrc, ['anchorId', 'anchorCards', 'anchorNode', 'anchorCurrent', 'anchorStore', 'anchorMark', 'anchorSelect', 'anchorStep', 'anchorFallback',
    'anchorSettle', 'anchorOwner', 'anchorView', 'anchorExposed', 'anchorControls', 'anchorMount', 'anchorSize', 'anchorCanScroll', 'anchorWheel', 'anchorScrollEnd', 'anchorWatchLast', 'anchorInit']) +
  vars(boardSrc, ['boardSeen', 'boardOwn', 'boardChanges', 'boardPaged']) +
  funcs(boardSrc, ['topicKey', 'topicSeen', 'boardKeep', 'boardWithSnap', 'boardExposed', 'boardLivePanels', 'boardPanelNodes', 'boardPanelKey', 'boardPanelMap',
    'boardRenderBlockers', 'boardRecordPending', 'boardOwnerSnapshot', 'boardStageInput', 'boardGuards', 'boardStageSnap', 'boardPendingNames', 'boardPendingBadge',
    'boardHeldNow', 'boardApplyPending', 'boardApplied', 'boardFlash', 'boardHome', 'boardFlushViewed']) +
  vars(framesSrc, ['frameSizes']) + funcs(framesSrc, ['queueFrameHeight', 'applyFrameHeights']);

function simple(n, s) {
  s = s.trim();
  if (!s || /[\s>+~:]/.test(s) || s === '*') return false;
  const m = s.match(/^([a-z0-9]*)((?:[.#][\w-]+|\[[\w-]+\])*)$/i);
  if (!m) return false;
  if (m[1] && n.tagName !== m[1].toUpperCase()) return false;
  return (m[2].match(/[.#][\w-]+|\[[\w-]+\]/g) || []).every(t => t[0] === '.' ? n.cls.has(t.slice(1)) : t[0] === '#' ? n.attrs.id === t.slice(1) : n.hasAttribute(t.slice(1, -1)));
}
const matches = (n, sel) => sel.split(',').some(s => simple(n, s));

// world builds a fake board, loads the shipped functions and counts reads.
function world(spec, opt = {}) {
  const D = { rect: { outer: 0, cur: 0, other: 0 }, top: { outer: 0, cur: 0, other: 0 }, vis: 0, style: 0, ops: 0, into: 0, scrollTo: 0, keep: [], rendered: [] };
  const clone = () => ({ rect: Object.assign({}, D.rect), top: Object.assign({}, D.top), vis: 0, style: 0, ops: 0, into: 0, scrollTo: 0, keep: [], rendered: [] });
  const C = { d: clone(), reset() { this.d = clone(); } };
  let c, bcol, html;
  const kind = n => (n === bcol || n === html) ? 'outer' : (c.anchorLast && (n === c.anchorLast || c.anchorLast.contains(n))) ? 'cur' : 'other';
  class El {
    constructor(tag, o = {}) {
      this.tagName = tag.toUpperCase(); this.nodeType = 1; this.children = []; this.parentNode = null; this.attrs = Object.assign({}, o.attrs);
      this.cls = new Set(o.cls || []); this.hidden = !!o.hidden; this.name = o.name || tag; this.listeners = {}; this.st = 0;
      this.scrollHeight = 0; this.clientHeight = 0; this.clientTop = 0; this.disabled = false;
      this.style = { setProperty() { C.d.ops++; } };
      const self = this;
      this.classList = { contains: k => self.cls.has(k), add: k => self.cls.add(k), remove: k => self.cls.delete(k), toggle() {} };
    }
    get isConnected() { let n = this; while (n.parentNode) n = n.parentNode; return n === c.root; }
    get id() { return this.attrs.id; }
    get firstChild() { return this.children[0] || null; }
    get firstElementChild() { return this.children[0] || null; }
    get scrollTop() { C.d.top[kind(this)]++; return this.st; }
    set scrollTop(v) { this.st = v; }
    get outerHTML() { return this.attrs['data-th'] || this.name; }
    hasAttribute(k) { return k in this.attrs; }
    getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
    setAttribute(k, v) { C.d.ops++; this.attrs[k] = String(v); }
    removeAttribute(k) { C.d.ops++; delete this.attrs[k]; }
    detach() { if (this.parentNode) { this.parentNode.children.splice(this.parentNode.children.indexOf(this), 1); this.parentNode = null; } }
    appendChild(n) { C.d.ops++; n.detach(); n.parentNode = this; this.children.push(n); return n; }
    insertBefore(n, ref) { C.d.ops++; n.detach(); n.parentNode = this; const i = ref ? this.children.indexOf(ref) : -1; if (i < 0) this.children.push(n); else this.children.splice(i, 0, n); return n; }
    remove() { C.d.ops++; this.detach(); }
    replaceWith(n) { C.d.ops++; const p = this.parentNode, i = p.children.indexOf(this); n.detach(); this.detach(); n.parentNode = p; p.children.splice(i, 0, n); }
    contains(n) { for (; n; n = n.parentNode) if (n === this) return true; return false; }
    closest(sel) { for (let n = this; n; n = n.parentNode) if (matches(n, sel)) return n; return null; }
    querySelector(sel) { for (const ch of this.children) { if (matches(ch, sel)) return ch; const r = ch.querySelector(sel); if (r) return r; } return null; }
    addEventListener(t, fn) { this.listeners[t] = fn; }
    scrollIntoView() { C.d.into++; }
    scrollTo() { C.d.scrollTo++; }
    getBoundingClientRect() { C.d.rect[kind(this)]++; return { top: 0, bottom: 600, left: 0, right: 800, height: 600, width: 800 }; }
    checkVisibility() { C.d.vis++; return true; }
  }
  const topic = (k, rev) => {
    const n = new El('section', { cls: ['topic'], attrs: { 'data-topic': k, 'data-th': rev || 'r1' }, name: 't:' + k });
    const body = new El('div', { cls: ['tbody'], name: 'body:' + k }), f = new El('iframe', { name: 'frame:' + k });
    f.style.height = '40px'; body.appendChild(f); n.appendChild(body);
    n.appendChild(new El('div', { cls: ['entry-panel'], attrs: { id: 'p-' + k, 'data-th': rev || 'r1' }, name: 'panel:' + k }));
    return n;
  };
  const card = id => new El('div', { cls: ['bcard'], attrs: { 'data-card': id }, name: 'c:' + id });
  const root = new El('root'); html = new El('html', { name: 'html' });
  bcol = new El('div', { name: 'bcol' });
  const board = new El('div', { attrs: { id: 'r-board' }, name: 'board' });
  const bar = new El('div', { name: 'bar' }), tools = new El('div', { cls: ['board-tools'] });
  const gallery = new El('div', { name: 'gallery' });
  bar.appendChild(tools); root.appendChild(html); root.appendChild(bcol); bcol.appendChild(board); root.appendChild(gallery);
  function build(list, parent) {
    list.forEach(s => {
      let n;
      if (s.t) n = topic(s.t, s.rev);
      else if (s.c) n = card(s.c);
      else { n = new El('div', { cls: ['closed-page'], name: 'closed' }); build(s.closed, n); }
      if (s.filtered) n.attrs['data-state-filtered'] = '';
      if (s.hidden) n.hidden = true;
      (s.docked ? gallery : parent).appendChild(n);
    });
  }
  build(spec, board);
  const store = new Map(Object.entries(opt.store || {}));
  const clock = { now: 1000 };
  function walk(box, f) { box.children.forEach(n => { if (n.cls.has('closed-page')) walk(n, f); else f(n); }); }
  const spy = {
    snapshot: () => ({ rendered: n => { C.d.rendered.push(n); return true; }, scroller: n => n.closest('.tbody') || n, visible: () => true }),
    own() {}, rendered: n => { C.d.rendered.push(n); return true; },
    keep: (n, o) => { C.d.keep.push(n); return { scroller: (o && o.scroller) || n, apply() {}, end() {} }; },
  };
  const g = {
    console, Map, Set, location: { pathname: '/s/x', search: '' }, Date: { now: () => clock.now },
    window: { sessionStorage: { getItem: k => store.has(k) ? store.get(k) : null, setItem: (k, v) => store.set(k, v), removeItem: k => store.delete(k) },
      WsGallery: G, WsCards: { toolbar: () => bar }, wbPill: { update() {} }, wbHistory: opt.history ? spy : undefined },
    doc: { body: new El('body'), documentElement: html, scrollingElement: html, activeElement: null, createElement: t => new El(t), addEventListener() {} },
    region: n => n === 'board' ? board : null, $: id => id === 'bcol' ? bcol : null,
    q: (s, box) => {
      const parts = s.split(',').map(p => p.trim().replace(/:not([^)]*)/, '')), out = [];
      (function w(n) { n.children.forEach(ch => { if (parts.some(p => simple(ch, p))) out.push(ch); w(ch); }); })(box);
      return out;
    },
    boardBoxes: () => [board], editing: () => false, reducedMotion: () => true, wsT: s => s, setTimeout() { return 1; }, clearTimeout() {}, postTheme() {},
    shHidden: false, deferred: null, boardDragHeld: null, boardOverviewHeld: null, heldSwap: null, raf: [],
    requestAnimationFrame(fn) { g.raf.push(fn); return g.raf.length; },
    getComputedStyle: n => { C.d.style++; return { overflowY: n.overflowY || 'visible' }; },
    applyBoard(staged) {
      staged.order.forEach(k => {
        let live = null; walk(board, n => { if (n.attrs['data-topic'] === k) live = n; });
        const src = staged.topics[k];
        if (live && src && live.attrs['data-th'] !== src.attrs['data-th']) { live.replaceWith(topic(k, src.attrs['data-th'])); }
      });
    },
    swap(name, hash, h) {
      if (g.boardDragHeld) { g.heldSwap = { hash, html: h }; return; }
      c.boardOffered = !c.boardLatest || c.boardLatest.hash !== hash ? { hash, previous: c.boardLatest ? c.boardLatest.html : c.boardRendered, input: h } : null;
      c.boardLatest = { hash, html: h };
      const staged = c.boardStageInput(h), before = c.anchorCards(), kept = c.boardKeep();
      g.applyBoard(staged);
      c.anchorSettle(before);
      if (kept) { kept.h.apply(); kept.h.end(); }
      c.boardRecordPending(); c.boardPendingBadge(); c.boardApplied();
    },
  };
  c = vm.createContext(g);
  c.root = root;
  vm.runInContext(shipped, c);
  const srcs = {};
  walk(board, n => { if (n.attrs['data-topic'] !== undefined) srcs[n.attrs['data-topic']] = topic(n.attrs['data-topic'], n.attrs['data-th']); });
  c.boardRendered = { topics: srcs, order: Object.keys(srcs) }; c.boardLatest = { hash: 'h0', html: c.boardRendered };
  C.reset();
  let seq = 0;
  const w = {
    c, g, C, topic, card, board, bcol, bar, store, clock,
    node: key => { let r = null; walk(board, n => { if ((n.attrs['data-topic'] !== undefined ? 't:' + n.attrs['data-topic'] : n.attrs['data-card'] !== undefined ? 'c:' + n.attrs['data-card'] : null) === key) r = n; }); return r; },
    keys: () => Array.from(c.anchorCards(), c.anchorId),
    cur: () => c.anchorKey,
    // update a set of topic revisions; the result has the shape the stream hands swap
    update(revs) {
      const topics = Object.assign({}, c.boardLatest.html.topics);
      Object.keys(revs).forEach(k => { topics[k] = topic(k, revs[k]); });
      c.swap('board', 'h' + (++seq), { topics, order: c.boardLatest.html.order.slice() });
    },
    select: key => c.anchorSelect(w.node(key), { still: true }),
    wheel(target, dy) {
      const e = { target, deltaY: dy, deltaMode: 0, ctrlKey: false, defaultPrevented: false, preventDefault() { this.defaultPrevented = true; } };
      c.anchorWheel(e);
      return e;
    },
  };
  return w;
}
const T = n => ({ t: n }), Cd = n => ({ c: n });

test('order: stacked cards across topics and unpinned cards, closed pages in place, filtered ones out', () => {
  const w = world([T('A'), Cd('u1'), T('B'), { closed: [T('C'), { closed: [Cd('u2'), T('D')] }] }, { t: 'E', filtered: true }, { t: 'F', hidden: true }, { c: 'u3', filtered: true }, { t: 'G', docked: true }, Cd('u4')]);
  assert.deepEqual(w.keys(), ['t:A', 'c:u1', 't:B', 't:C', 'c:u2', 't:D', 'c:u4']);
});

test('key retention across insertion and reorder; replaced node keeps the key', () => {
  const w = world([T('A'), T('B'), T('C')]);
  w.select('t:B');
  const b = w.node('t:B');
  w.board.insertBefore(w.card('new'), b); w.c.anchorSettle(null);
  assert.equal(w.cur(), 't:B'); assert.ok(b.hasAttribute('data-current'));
  w.board.appendChild(b); w.c.anchorSettle(null);
  assert.equal(w.cur(), 't:B'); assert.equal(w.c.anchorCurrent(), b);
  const nb = w.topic('B', 'r2'); b.replaceWith(nb); w.c.anchorSettle(null);
  assert.equal(w.cur(), 't:B'); assert.ok(nb.hasAttribute('data-current')); assert.ok(!b.hasAttribute('data-current'));
});

test('removal of the current card falls back to the next card, else the previous', () => {
  let w = world([T('A'), T('B'), Cd('u'), T('C')]);
  w.select('t:B');
  let before = w.c.anchorCards(); w.node('t:B').remove(); w.c.anchorSettle(before);
  assert.equal(w.cur(), 'c:u');
  before = w.c.anchorCards(); w.node('c:u').remove(); w.node('t:C').remove(); w.c.anchorSettle(before);
  assert.equal(w.cur(), 't:A', 'no next card left: the previous one');
  w = world([T('A'), T('B'), T('C')]); w.select('t:B');
  before = w.c.anchorCards(); w.node('t:B').hidden = true; w.c.anchorSettle(before);
  assert.equal(w.cur(), 't:C', 'a card that leaves the order (hidden) hands over too');
});

test('wheel: a scrollable card body consumes the wheel until its edge', () => {
  const w = world([T('A'), T('B')]);
  w.select('t:A');
  const body = w.node('t:A').children[0];
  body.scrollHeight = 500; body.clientHeight = 200; body.overflowY = 'auto'; body.st = 100;
  const e = w.wheel(body, 50);
  assert.equal(e.defaultPrevented, false, 'body keeps its native scroll'); assert.equal(w.cur(), 't:A');
  w.clock.now += 1000; body.st = 300; // at the bottom edge
  const e2 = w.wheel(body, 50);
  assert.equal(e2.defaultPrevented, true); assert.equal(w.cur(), 't:A', 'the first gesture at the edge stops');
  w.clock.now += G.C.INERTIA_MS + 1;
  w.wheel(body, 50);
  assert.equal(w.cur(), 't:B', 'a new same-direction gesture moves one card');
});

test('wheel: gap >= INERTIA_MS opens a new gesture; opposite direction and bursts never move', () => {
  const w = world([T('A'), T('B'), T('C')]);
  w.select('t:B');
  const at = dy => w.wheel(w.node(w.cur()), dy);
  at(50); assert.equal(w.cur(), 't:B', 'first gesture only stops');
  w.clock.now += G.C.INERTIA_MS - 1; at(50);
  assert.equal(w.cur(), 't:B', 'inside the inertia window it is the same gesture');
  w.clock.now += G.C.INERTIA_MS; at(-50);
  assert.equal(w.cur(), 't:B', 'the opposite direction restarts at a stop');
  w.clock.now += G.C.INERTIA_MS; at(-50);
  assert.equal(w.cur(), 't:A', 'previous card after a new gesture');
  // a continuous burst starting at an edge never moves, however long
  const b = world([T('A'), T('B')]); b.select('t:A');
  b.clock.now += 5000;
  for (let i = 0; i < 60; i++) { b.clock.now += 16; b.wheel(b.node('t:A'), 40); }
  assert.equal(b.cur(), 't:A', 'burst of 60 events (~1s) never moves');
  b.clock.now += G.C.INERTIA_MS + 1; b.wheel(b.node('t:A'), 40);
  assert.equal(b.cur(), 't:B');
  b.clock.now += G.C.INERTIA_MS + 1; b.wheel(b.node('t:B'), 40);
  assert.equal(b.cur(), 't:B', 'wheel right after a move is locked out (LOCK_MS)');
});

test('toolbar step buttons move both ways across an interleaved sequence', () => {
  const w = world([T('A'), Cd('term'), T('B'), Cd('web'), Cd('preview'), T('C')]);
  w.c.anchorInit();
  const group = w.bar.children[0].children[0], [prev, next] = group.children;
  assert.equal(w.cur(), 't:A', 'first card taken'); assert.equal(prev.disabled, true); assert.equal(next.disabled, false);
  const seen = [];
  for (let i = 0; i < 5; i++) { next.listeners.click(); seen.push(w.cur()); }
  assert.deepEqual(seen, ['c:term', 't:B', 'c:web', 'c:preview', 't:C']);
  assert.equal(next.disabled, true); assert.equal(prev.disabled, false);
  const before = w.C.d.scrollTo; next.listeners.click();
  assert.equal(w.cur(), 't:C', 'past the last card only the board tail is reached'); assert.equal(w.C.d.scrollTo, before + 1);
  const back = [];
  for (let i = 0; i < 5; i++) { prev.listeners.click(); back.push(w.cur()); }
  assert.deepEqual(back, ['c:preview', 'c:web', 't:B', 'c:term', 't:A']);
  prev.listeners.click(); assert.equal(w.cur(), 't:A');
  assert.equal(w.node('t:A').hasAttribute('data-current'), true); assert.equal(w.node('t:C').hasAttribute('data-current'), false);
});

test('restore: key-only sessionStorage for a topic and an unpinned card; first card when the key is gone', () => {
  const key = 'ws.dashboard.board-card./s/x';
  for (const saved of ['t:C', 'c:u2']) {
    const w = world([T('A'), Cd('u1'), T('C'), Cd('u2')], { store: { [key]: saved } });
    w.c.anchorInit();
    assert.equal(w.cur(), saved); assert.equal(w.C.d.into, 1, 'aligned once');
  }
  const w = world([T('A'), T('B')], { store: { [key]: 't:gone' } });
  w.c.anchorInit();
  assert.equal(w.cur(), 't:A', 'first card');
  assert.equal(w.store.get(key), 't:A'); assert.equal(w.C.d.into, 0, 'fallback does not scroll the board');
  w.select('t:B'); assert.equal(w.store.get(key), 't:B', 'selection persists the key only');
});

test('staging: other card admitted, current card staged, leaving applies the newest revision', () => {
  const w = world([T('A'), T('B'), T('C')]);
  w.select('t:B');
  w.update({ A: 'a2' });
  assert.equal(w.node('t:A').attrs['data-th'], 'a2', 'update to another card is admitted');
  assert.equal(Object.keys(w.c.boardPending).length, 0); assert.equal(w.g.window.wbPill.pending, 0);
  w.update({ B: 'b2' });
  assert.equal(w.node('t:B').attrs['data-th'], 'r1', 'update to the current card stages');
  assert.deepEqual(Object.keys(w.c.boardPending), ['B']); assert.equal(w.g.window.wbPill.pending, 1);
  w.update({ B: 'b3' });
  assert.equal(w.g.window.wbPill.pending, 1, 'repeated revisions count once'); assert.equal(w.node('t:B').attrs['data-th'], 'r1');
  w.c.anchorSelect(w.node('t:C'), { still: true });
  assert.equal(w.cur(), 't:C'); assert.equal(w.node('t:B').attrs['data-th'], 'b3', 'newest revision applied on leaving');
  assert.equal(w.g.window.wbPill.pending, 0); assert.equal(Object.keys(w.c.boardPending).length, 0);
});

test('home: apply then select the first eligible topic; skips filtered and docked, keeps the card when none', () => {
  let w = world([{ t: 'D', docked: true }, { t: 'X', filtered: true }, T('A'), T('B')]);
  w.select('t:B'); w.update({ B: 'b2' });
  assert.equal(w.g.window.wbPill.pending, 1);
  w.g.window.wbPill.home();
  assert.equal(w.node('t:B').attrs['data-th'], 'b2'); assert.equal(w.g.window.wbPill.pending, 0); assert.equal(w.cur(), 't:A');
  w = world([Cd('u1'), Cd('u2'), T('A')]); w.select('c:u2'); w.c.boardHome();
  assert.equal(w.cur(), 't:A', 'earlier unpinned cards do not stop the climb to the first topic');
  w = world([Cd('u1'), Cd('u2'), { t: 'A', filtered: true }]); w.select('c:u2'); w.c.boardHome();
  assert.equal(w.cur(), 'c:u2', 'no eligible topic keeps the card');
});

test('home waits for the deferred swap to land before selecting', () => {
  const w = world([T('A'), T('B')]);
  w.select('t:B'); w.update({ B: 'b2' });
  w.g.boardDragHeld = {};
  w.c.boardHome();
  assert.ok(w.g.heldSwap, 'the swap was deferred');
  assert.equal(w.cur(), 't:B', 'still on the card while the swap is held');
  assert.equal(w.c.boardAfterApply.length, 1);
  w.g.boardDragHeld = null;
  const h = w.g.heldSwap; w.g.heldSwap = null;
  w.c.swap('board', h.hash, h.html);
  assert.equal(w.node('t:B').attrs['data-th'], 'b2', 'the applied revision is on the board');
  assert.equal(w.cur(), 't:A', 'selected after the revision landed'); assert.equal(w.c.boardAfterApply.length, 0); assert.equal(w.g.window.wbPill.pending, 0);
});

test('counts: board updates read nothing outside the current card', () => {
  const spec = [];
  for (let i = 1; i <= 40; i++) { const k = 'T' + String(i).padStart(2, '0'); spec.push(T(k)); if (i === 10) spec.push(Cd('term')); if (i === 25) spec.push(Cd('web')); }
  spec.push(Cd('preview'));
  const w = world(spec, { history: true });
  const frame = k => w.node(k).children[0].children[0];
  w.select('t:T20'); w.C.reset();
  const rows = [];
  function row(label) {
    const d = w.C.d;
    rows.push([label, d.rect.outer, d.top.outer, d.vis, d.style, d.rect.other, d.rect.cur, d.keep.length, d.rendered.length, d.ops]);
    w.C.reset();
  }
  // (a) content update to a non-current topic
  w.update({ T05: 'r2' });
  assert.equal(w.node('t:T05').attrs['data-th'], 'r2'); assert.equal(w.cur(), 't:T20');
  assert.deepEqual([w.C.d.rect.outer, w.C.d.top.outer, w.C.d.vis, w.C.d.rect.other, w.C.d.rect.cur], [0, 0, 0, 0, 0]);
  const inCur = n => w.node('t:T20').contains(n);
  assert.ok(w.C.d.rendered.every(inCur) && w.C.d.keep.every(inCur), 'the swap captures only the current card inner scroller (rendered/keep: ' + w.C.d.rendered.length + '/' + w.C.d.keep.length + ')');
  row('(a) content, non-current');
  // (b) size-only changes of non-current cards above and below: late frame heights and the last-card observer
  const last = w.node('c:preview'); w.c.anchorLastCard = last;
  [frame('t:T05'), frame('t:T35'), frame('t:T01')].forEach(f => w.c.queueFrameHeight(f, 300));
  assert.equal(w.g.raf.length, 1, 'one frame callback for the batch');
  w.g.raf.shift()();
  assert.equal(frame('t:T05').style.height, '300px'); assert.equal(frame('t:T35').style.height, '300px');
  w.c.anchorSize([{ target: last, borderBoxSize: [{ blockSize: 250 }], contentRect: { height: 250 } }]);
  assert.equal(w.cur(), 't:T20');
  assert.deepEqual([w.C.d.rect.outer, w.C.d.top.outer, w.C.d.vis, w.C.d.rect.other, w.C.d.rect.cur], [0, 0, 0, 0, 0]);
  assert.equal(w.C.d.keep.length, 0, 'frames of other cards are never captured'); assert.equal(w.C.d.rendered.length, 0);
  row('(b) size-only, above/below');
  // (c) update to the current topic: staged, reads bounded to the current card
  w.update({ T20: 'r2' });
  assert.equal(w.cur(), 't:T20'); assert.equal(w.node('t:T20').attrs['data-th'], 'r1', 'staged');
  assert.equal(w.C.d.rect.other, 0); assert.equal(w.C.d.vis, 0); assert.ok(w.C.d.rect.outer <= 1, 'one board rect for the usable view');
  assert.ok(w.C.d.rect.cur <= 2); assert.ok(w.C.d.rendered.every(n => w.node('t:T20').contains(n)));
  row('(c) content, current (staged)');
  // (d) late frame height inside the current card
  w.c.queueFrameHeight(frame('t:T20'), 500); w.g.raf.shift()();
  assert.equal(w.C.d.rect.other, 0); assert.equal(w.C.d.rect.outer, 0); assert.equal(w.C.d.vis, 0);
  assert.ok(w.C.d.keep.length === 1 && w.C.d.keep.every(n => w.node('t:T20').contains(n)));
  row('(d) frame height, current');
  const head = ['update', 'outerRect', 'outerTop', 'chkVis', 'cmpStyle', 'otherRect', 'curRect', 'keeps', 'rendered', 'domOps'];
  console.log('board anchor counts (40 topics + 3 cards, current = T20)\n' + [head].concat(rows).map(r => r.map((x, i) => String(x).padEnd(i ? 10 : 32)).join('')).join('\n'));
});

test('step past the last card scrolls #bcol to its end, key unchanged', () => {
  const w = world([T('A'), T('B')]);
  w.select('t:B');
  w.bcol.scrollHeight = 1234; let to = null; w.bcol.scrollTo = o => { to = o; };
  assert.equal(w.c.anchorStep(1), false); assert.equal(w.cur(), 't:B');
  assert.equal(to.top, 1234, 'the tail (Show more) is reachable');
  to = null; w.select('t:A');
  assert.equal(w.c.anchorStep(-1), false); assert.equal(to, null, 'past the first card nothing scrolls');
});

test('native scroll end re-selects the nearest card', () => {
  const w = world([T('A'), T('B'), T('C')]);
  w.select('t:A');
  const tops = { 't:A': -700, 't:B': 40, 't:C': 700 };
  Object.keys(tops).forEach(k => { w.node(k).getBoundingClientRect = () => ({ top: tops[k] }); });
  w.bcol.getBoundingClientRect = () => ({ top: 0 });
  w.c.anchorScrollEnd({ target: w.board });
  assert.equal(w.cur(), 't:A', 'other targets are ignored');
  w.c.anchorScrollEnd({ target: w.bcol });
  assert.equal(w.cur(), 't:B'); assert.ok(w.node('t:B').hasAttribute('data-current')); assert.ok(!w.node('t:A').hasAttribute('data-current'));
});

test('boardKeep without a current card keeps the main box first child; with one it does not', () => {
  const w = world([T('A'), T('B')], { history: true });
  assert.equal(w.cur(), null);
  w.c.boardKeep();
  assert.deepEqual(w.C.d.keep, [w.board.firstElementChild], 'fallback keeps the main box first child');
  w.C.reset(); w.select('t:B');
  w.c.boardKeep();
  assert.ok(w.C.d.keep.length === 1 && w.node('t:B').contains(w.C.d.keep[0]), 'only the current card inside the main board');
});
