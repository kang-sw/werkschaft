// Offline regression tests for forwarded doc-frame wheel events driving
// gallery bounce-snap tension in #bgal and narrow-mode Page 0 (#bcol).
// Run: node --test testdata/gallery-frame-wheel.test.cjs (from internal/wsdashboard)
const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const G = require('../assets/gallery.js');
const { readAssetSource } = require('./asset-source.cjs');

const cardsSrc = readAssetSource('wsdashboard', 'cards.js');
const framesSrc = readAssetSource('wsdashboard', 'dashboard-frames.js');

function extractVar(src, name) {
  const m = src.match(new RegExp('\\n  var ' + name + '\\b[^\\n]*\\n'));
  assert.ok(m, 'missing var ' + name);
  return m[0];
}
function extractFunc(src, name) {
  const m = src.match(new RegExp('\\n  function ' + name + '\\([^\\n]*\\{[^\\n]*\\}\\n|\\n  function ' + name + '\\([\\s\\S]*?\\n  \\}\\n'));
  assert.ok(m, 'missing function ' + name);
  return m[0];
}

// Verify dashboard-frames.js routes wbDoc === "wheel" to WsCards for #bgal
// and narrow-mode Page 0 without snap-deck WsBoardAnchor wheel interception.
const frameWheelBranch = framesSrc.match(/\} else if \(m\.wbDoc === "wheel"\) \{[\s\S]*?(?=\n    \} else if \()/);
assert.ok(frameWheelBranch, 'missing wbDoc === "wheel" branch in dashboard-frames.js');
assert.doesNotMatch(frameWheelBranch[0], /onFrameWheel\s*\(\s*f[\s\S]*WsBoardAnchor|WsBoardAnchor[\s\S]*onFrameWheel/);

const shippedCards =
  extractFunc(cardsSrc, 'curPos') +
  extractFunc(cardsSrc, 'pageElAt') +
  extractVar(cardsSrc, 'OWN') +
  extractVar(cardsSrc, 'PANE_BODY') +
  extractFunc(cardsSrc, 'reach') +
  extractFunc(cardsSrc, 'canScroll') +
  extractFunc(cardsSrc, 'gateWheelInput') +
  extractFunc(cardsSrc, 'gateWheel') +
  extractFunc(cardsSrc, 'onFrameWheel');

// Minimal DOM selector matcher supporting the selectors used by cards.js and
// dashboard-board-anchor.js (including :not(.doc-frame), [data-max] *, :scope).
function matchAtom(n, sel) {
  sel = sel.trim();
  if (!sel) return false;
  if (sel === '*') return true;
  if (sel === 'iframe:not(.doc-frame)') return n.tagName === 'IFRAME' && !n.cls.has('doc-frame');
  if (sel.endsWith(' *')) {
    const anc = sel.slice(0, -2).trim();
    for (let p = n.parentNode; p; p = p.parentNode) if (matchAtom(p, anc)) return true;
    return false;
  }
  if (sel.includes(':is(') || sel.includes('>') || sel.includes(' ')) return false;
  const m = sel.match(/^([a-z0-9]*)((?:[.#][\w-]+|\[[\w-]+(?:="[^"]*")?\])*)$/i);
  if (!m) return false;
  if (m[1] && n.tagName !== m[1].toUpperCase()) return false;
  const parts = m[2].match(/[.#][\w-]+|\[[\w-]+(?:="[^"]*")?\]/g) || [];
  return parts.every(t => {
    if (t[0] === '.') return n.cls.has(t.slice(1));
    if (t[0] === '#') return n.attrs.id === t.slice(1);
    const eq = t.indexOf('=');
    if (eq > 0) return n.attrs[t.slice(1, eq)] === t.slice(eq + 2, -2);
    return Object.prototype.hasOwnProperty.call(n.attrs, t.slice(1, -1));
  });
}
function matchSel(n, sel) {
  return sel.split(',').some(s => matchAtom(n, s));
}

class El {
  constructor(tag, o = {}) {
    this.tagName = tag.toUpperCase();
    this.nodeType = 1;
    this.children = [];
    this.parentNode = null;
    this.attrs = Object.assign({}, o.attrs);
    this.cls = new Set(o.cls || []);
    this.scrollTop = o.scrollTop || 0;
    this.scrollHeight = o.scrollHeight || 0;
    this.clientHeight = o.clientHeight || 0;
    this.overflowY = o.overflowY || 'visible';
    this.overscrollBehaviorY = o.overscrollBehaviorY || 'auto';
    this.rect = o.rect || { top: 0, bottom: 400, height: 400 };
    const self = this;
    this.classList = {
      contains: k => self.cls.has(k),
      add: k => self.cls.add(k),
      remove: k => self.cls.delete(k)
    };
  }
  get isConnected() {
    let n = this;
    while (n.parentNode) n = n.parentNode;
    return n.tagName === 'ROOT';
  }
  appendChild(ch) {
    ch.parentNode = this;
    this.children.push(ch);
    return ch;
  }
  contains(n) {
    for (; n; n = n.parentNode) if (n === this) return true;
    return false;
  }
  hasAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k); }
  getAttribute(k) { return this.hasAttribute(k) ? this.attrs[k] : null; }
  closest(sel) {
    for (let n = this; n && n.nodeType === 1; n = n.parentNode) {
      if (matchSel(n, sel)) return n;
    }
    return null;
  }
  querySelector(sel) {
    if (sel.includes('.gal-pbody')) {
      for (const sec of this.children) {
        if (sec.tagName === 'SECTION' && sec.cls.has('topic')) {
          const b = sec.children.find(c => c.cls.has('gal-pbody'));
          if (b) return b;
        }
      }
      return null;
    }
    return null;
  }
  getBoundingClientRect() { return this.rect; }
}

function makeHarness(opts = {}) {
  const root = new El('root');
  const html = new El('html');
  const bcol = new El('div', { attrs: { id: 'bcol' }, scrollHeight: 1000, clientHeight: 500, overflowY: 'auto', rect: { top: 0, bottom: 500, height: 500 } });
  const board = new El('div', { attrs: { id: 'r-board' } });
  const galEl = new El('aside', { attrs: { id: 'bgal' }, cls: ['bgal'] });
  const viewEl = new El('div', { cls: ['gal-viewport'] });
  const plusEl = new El('div', { cls: ['gal-page', 'gal-plus'] });
  const page1 = new El('div', { cls: ['gal-page'], attrs: { 'data-active': '' } });
  const pane1 = new El('div', { cls: ['gal-pane'] });
  const topic1 = new El('section', { cls: ['topic'], attrs: { 'data-topic': 'alpha' } });
  const pbody1 = new El('div', {
    cls: ['gal-pbody'],
    scrollTop: opts.pbodyScrollTop ?? 400,
    scrollHeight: opts.pbodyScrollHeight ?? 800,
    clientHeight: opts.pbodyClientHeight ?? 400,
    overflowY: 'auto'
  });
  const tbody1 = new El('div', { cls: ['tbody'] });
  const docFrame = new El('iframe', { cls: ['doc-frame'], attrs: { 'data-doc': 'alpha', 'data-doc-key': 'alpha' } });
  const plainIframe = new El('iframe', { cls: ['web-frame'] });

  tbody1.appendChild(docFrame);
  tbody1.appendChild(plainIframe);
  pbody1.appendChild(tbody1);
  topic1.appendChild(pbody1);
  pane1.appendChild(topic1);
  page1.appendChild(pane1);

  // Page 2 remains attached in #bgal even while inactive (matching slide()).
  const page2 = new El('div', { cls: ['gal-page'] });
  const pane2 = new El('div', { cls: ['gal-pane'] });
  const topic2 = new El('section', { cls: ['topic'], attrs: { 'data-topic': 'beta' } });
  const pbody2 = new El('div', {
    cls: ['gal-pbody'],
    scrollTop: 400,
    scrollHeight: 800,
    clientHeight: 400,
    overflowY: 'auto'
  });
  const tbody2 = new El('div', { cls: ['tbody'] });
  const docFrame2 = new El('iframe', { cls: ['doc-frame'], attrs: { 'data-doc': 'beta', 'data-doc-key': 'beta' } });
  tbody2.appendChild(docFrame2);
  pbody2.appendChild(tbody2);
  topic2.appendChild(pbody2);
  pane2.appendChild(topic2);
  page2.appendChild(pane2);

  viewEl.appendChild(page1);
  viewEl.appendChild(page2);
  viewEl.appendChild(plusEl);
  galEl.appendChild(viewEl);
  bcol.appendChild(board);
  root.appendChild(html);
  root.appendChild(bcol);
  root.appendChild(galEl);

  // Topics on Main Board (#bcol) for narrow-mode Page 0 tests.
  const bTopicA = new El('section', { cls: ['topic'], attrs: { 'data-topic': 'boardA' }, rect: { top: 0, bottom: 400, height: 400 } });
  const bFrameA = new El('iframe', { cls: ['doc-frame'], attrs: { 'data-doc': 'boardA', 'data-doc-key': 'boardA' } });
  bTopicA.appendChild(bFrameA);
  const bTopicB = new El('section', { cls: ['topic'], attrs: { 'data-topic': 'boardB' }, rect: { top: 0, bottom: 400, height: 400 } });
  const bFrameB = new El('iframe', { cls: ['doc-frame'], attrs: { 'data-doc': 'boardB', 'data-doc-key': 'boardB' } });
  bTopicB.appendChild(bFrameB);
  board.appendChild(bTopicA);
  board.appendChild(bTopicB);

  let now = 1000;
  const events = { pulls: [], offs: 0, flips: [], anchorCalls: [], anchorSelects: [] };

  let gal = {
    v: 1,
    active: opts.narrowHome ? null : 'p1',
    pages: [
      { id: 'p1', root: { topic: 'alpha' } },
      { id: 'p2', root: { topic: 'beta' } }
    ],
    rev: 1
  };
  const pageEls = { p1: page1, p2: page2 };

  const ctx = vm.createContext({
    Math, Date: { now: () => now },
    G,
    doc: { documentElement: html, scrollingElement: html },
    window: {},
    lay: { narrow: !!opts.narrowHome },
    gal,
    galEl,
    viewEl,
    plusEl,
    pageEls,
    sysActive: null,
    storedSys: null,
    sysPageEls: {},
    board,
    homeEl: opts.narrowHome ? bcol : null,
    homeScr: opts.narrowHome ? bcol : null,
    overviewOn: false,
    gate: new G.Gate(),
    gateIn: { dy: 0, now: 0, canUp: false, canDown: false, prev: null, next: null },
    nb: null, nbOf: null, nbNarrow: null,
    clock: () => now,
    getComputedStyle: n => ({ overflowY: n.overflowY, overscrollBehaviorY: n.overscrollBehaviorY }),
    wsT: s => s,
    pullName: p => 'page-' + p,
    pullShow: (el, dir, shift, pct, state, text, target) => {
      events.pulls.push({ el, dir, shift, pct, state, text, target });
    },
    pullOff: () => { events.offs++; },
    show: (to, dir) => {
      events.flips.push({ to, dir });
      ctx.gal = Object.assign({}, ctx.gal, { active: to < 0 ? null : to < ctx.gal.pages.length ? ctx.gal.pages[to].id : G.PLUS });
    },
    gateArm: () => {},
    // Anchor state for #bcol
    $: id => id === 'bcol' ? bcol : null,
    anchorCards: () => [bTopicA, bTopicB],
    num: v => Number(v) || 0
  });
  ctx.window.performance = { now: () => now };

  vm.runInContext(shippedCards, ctx);

  ctx.window.WsCards = {
    gallery: () => galEl,
    onFrameWheel: ctx.onFrameWheel
  };
  ctx.window.WsBoardAnchor = {
    cards: ctx.anchorCards
  };

  // Run the exact shipped wheel routing branch from dashboard-frames.js.
  const routeCode = 'function routeWheel(f, m) { if (false) {' + frameWheelBranch[0] + '\n  } }';
  vm.runInContext(routeCode, ctx);

  return {
    ctx, events, docFrame, docFrame2, plainIframe, pbody1, pbody2, topic1, topic2, pane1, pane2, page1, page2,
    bcol, board, bTopicA, bTopicB, bFrameA, bFrameB,
    tick(ms) { now += ms; },
    send(frame, dy, dx = 0, mode = 0) {
      ctx.routeWheel(frame, { wbDoc: 'wheel', dy, dx, mode });
    }
  };
}

test('docked topic doc-frame at .gal-pbody bottom boundary charges tension and flips to next page, and top boundary flips back', () => {
  const h = makeHarness({ pbodyScrollTop: 400, pbodyScrollHeight: 800, pbodyClientHeight: 400 });

  h.send(h.docFrame, 125);
  assert.equal(h.events.anchorCalls.length, 0, '#bgal frame must short-circuit before WsBoardAnchor');
  assert.equal(h.events.pulls.length, 1, 'first wheel at bottom charges tension');
  assert.equal(h.events.pulls[0].dir, 1);
  assert.equal(h.events.pulls[0].target, 1);
  assert.equal(h.events.flips.length, 0);

  h.tick(30);
  h.send(h.docFrame, 125);
  assert.equal(h.events.pulls.length, 2);
  assert.equal(h.events.flips.length, 0);

  h.tick(30);
  h.send(h.docFrame, 125);
  assert.deepEqual(h.events.flips, [{ to: 1, dir: 1 }], 'reaching 100% tension flips to page 2');

  // Wait past refractory lock (420ms), move pbody2 to top boundary, and wheel up from active page2 to flip back.
  h.tick(500);
  h.pbody2.scrollTop = 0;
  h.events.pulls.length = 0;
  h.events.flips.length = 0;

  h.send(h.docFrame2, -125);
  assert.equal(h.events.pulls.length, 1);
  assert.equal(h.events.pulls[0].dir, -1);
  assert.equal(h.events.pulls[0].target, 0);
  h.tick(30);
  h.send(h.docFrame2, -125);
  h.tick(30);
  h.send(h.docFrame2, -125);
  assert.deepEqual(h.events.flips, [{ to: 0, dir: -1 }], 'top boundary tension flips back to page 1');
});

test('delayed wheel from previously visible (now inactive, still attached) gallery page is rejected and does not change active page tension', () => {
  const h = makeHarness({ pbodyScrollTop: 400, pbodyScrollHeight: 800, pbodyClientHeight: 400 });

  // While page 1 is active, a wheel from attached-but-inactive page 2 is rejected.
  assert.equal(h.ctx.onFrameWheel(h.docFrame2, 125, 0, 0), false, 'inactive page 2 frame must be rejected while page 1 is active');
  h.send(h.docFrame2, 125);
  assert.equal(h.events.pulls.length, 0, 'inactive page 2 must not charge tension on active page 1');
  assert.equal(h.events.flips.length, 0);

  // Flip from page 1 to page 2 so page 1 is now previously visible (still attached in #bgal, inactive).
  h.send(h.docFrame, 125);
  h.tick(30);
  h.send(h.docFrame, 125);
  h.tick(30);
  h.send(h.docFrame, 125);
  assert.deepEqual(h.events.flips, [{ to: 1, dir: 1 }], 'flipped to page 2');

  // Wait past refractory lock and send delayed wheel messages from the previously visible page 1 frame.
  h.tick(500);
  h.events.pulls.length = 0;
  h.events.flips.length = 0;
  h.pbody1.scrollTop = 0;

  assert.equal(h.ctx.onFrameWheel(h.docFrame, -125, 0, 0), false, 'delayed wheel from now-inactive page 1 must be rejected');
  h.send(h.docFrame, -125);
  h.tick(30);
  h.send(h.docFrame, -125);
  h.tick(30);
  h.send(h.docFrame, -125);
  assert.equal(h.events.pulls.length, 0, 'delayed wheel from previously visible page 1 must not change active page 2 tension');
  assert.equal(h.events.flips.length, 0, 'delayed wheel from previously visible page 1 must not flip away from page 2');
});

test('docked topic doc-frame passes through without tension while .gal-pbody still has scroll room', () => {
  const h = makeHarness({ pbodyScrollTop: 100, pbodyScrollHeight: 800, pbodyClientHeight: 400 });
  h.send(h.docFrame, 125);
  h.tick(30);
  h.send(h.docFrame, 125);
  h.tick(30);
  h.send(h.docFrame, 125);
  assert.equal(h.events.pulls.length, 0, 'no tension while .gal-pbody can scroll down');
  assert.equal(h.events.flips.length, 0);

  h.tick(30);
  h.send(h.docFrame, -125);
  assert.equal(h.events.pulls.length, 0, 'no tension while .gal-pbody can scroll up');
  assert.equal(h.events.flips.length, 0);
});

test('non-doc-frame iframe and maximized doc-frame are still excluded by OWN', () => {
  const h = makeHarness({ pbodyScrollTop: 400, pbodyScrollHeight: 800, pbodyClientHeight: 400 });
  h.send(h.plainIframe, 125);
  h.tick(30);
  h.send(h.plainIframe, 125);
  h.tick(30);
  h.send(h.plainIframe, 125);
  assert.equal(h.events.pulls.length, 0, 'non-doc-frame iframe must not charge tension');
  assert.equal(h.events.flips.length, 0);

  // Maximized sheet ([data-max]) still blocks doc-frame wheel.
  h.topic1.attrs['data-max'] = '';
  h.send(h.docFrame, 125);
  assert.equal(h.events.pulls.length, 0, 'doc-frame inside [data-max] must not charge tension');
});

test('horizontal-dominant wheel deltas are ignored', () => {
  const h = makeHarness({ pbodyScrollTop: 400, pbodyScrollHeight: 800, pbodyClientHeight: 400 });
  h.send(h.docFrame, 40, 80, 0);
  h.tick(30);
  h.send(h.docFrame, 1, 2, 1);
  h.tick(30);
  h.send(h.docFrame, 1, 2, 2);
  assert.equal(h.events.pulls.length, 0, 'horizontal-dominant deltas must not charge tension');
  assert.equal(h.events.flips.length, 0);
});

test('narrow-mode Page 0: off-window Card N-1 blocks premature Page 0 -> Page 1 flip, while mounted Card N-1 at bottom edge flips to Page 1', () => {
  // While last card (boardB) is off-window, gateWheelInput must report canDown = true
  // even if #bcol looks like it is near the bottom of its runway, preventing premature flip.
  const h = makeHarness({ narrowHome: true });
  h.bcol.scrollTop = 500; // would look at bottom if not for off-window guard
  h.bTopicB.attrs['data-wb-offwindow'] = 'hidden';
  h.send(h.bFrameA, 125);
  h.tick(30);
  h.send(h.bFrameA, 125);
  h.tick(30);
  h.send(h.bFrameA, 125);
  assert.equal(h.events.pulls.length, 0, 'off-window Card N-1 must never charge Page 0 -> Page 1 tension');
  assert.equal(h.events.flips.length, 0);

  // Similarly, if Card 0 is off-window, upward wheel does not treat #bcol as at top boundary.
  h.bcol.scrollTop = 0;
  h.bTopicA.attrs['data-wb-offwindow'] = 'hidden';
  delete h.bTopicB.attrs['data-wb-offwindow'];
  h.tick(500);
  h.send(h.bFrameB, -125);
  assert.equal(h.events.pulls.length, 0, 'off-window Card 0 must not charge top-edge tension');

  // Once Card N-1 (boardB) is mounted and #bcol is at its bottom edge, wheel charges tension and flips Page 0 -> Page 1.
  delete h.bTopicA.attrs['data-wb-offwindow'];
  h.bcol.scrollTop = 500; // at bottom: 500 + 500 === 1000
  h.bTopicB.rect = { top: 0, bottom: 500, height: 500 };
  h.tick(500);
  h.send(h.bFrameB, 125);
  assert.equal(h.events.pulls.length, 1, 'mounted last card at bottom edge charges WsCards tension');
  assert.equal(h.events.pulls[0].el, h.board);
  assert.equal(h.events.pulls[0].target, 0);
  h.tick(30);
  h.send(h.bFrameB, 125);
  h.tick(30);
  h.send(h.bFrameB, 125);
  assert.deepEqual(h.events.flips, [{ to: 0, dir: 1 }], 'narrow Page 0 last card edge flips to Page 1');
});
