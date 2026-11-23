const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const anchorSrc = fs.readFileSync(
  path.join(__dirname, '..', 'assets', 'dashboard-board-anchor.js'),
  'utf8',
);

function makeEl(tag, attrs = {}, children = []) {
  const map = new Map(Object.entries(attrs));
  const el = {
    nodeType: 1,
    tagName: tag.toUpperCase(),
    className: map.get('class') || '',
    id: map.get('id') || '',
    textContent: attrs._text || '',
    childNodes: [],
    parentNode: null,
    isConnected: true,
    hidden: false,
    open: map.has('open'),
    scrollTop: 0,
    value: '',
    style: {
      setProperty() {},
      removeProperty() {},
      getPropertyValue() { return ''; },
    },
    get attributes() {
      const out = [];
      for (const [name, value] of map.entries()) {
        if (name === '_text') continue;
        out.push({ name, value: String(value) });
      }
      return out;
    },
    get children() { return this.childNodes.filter(c => c.nodeType === 1); },
    get firstChild() { return this.childNodes[0] || null; },
    get nextSibling() {
      if (!this.parentNode) return null;
      const idx = this.parentNode.childNodes.indexOf(this);
      return idx >= 0 ? this.parentNode.childNodes[idx + 1] || null : null;
    },
    classList: {
      contains(c) { return (el.className || '').split(/\s+/).includes(c); },
      add(c) {
        const parts = (el.className || '').split(/\s+/).filter(Boolean);
        if (!parts.includes(c)) parts.push(c);
        el.className = parts.join(' ');
      },
      remove(c) {
        el.className = (el.className || '').split(/\s+/).filter(x => x && x !== c).join(' ');
      },
    },
    hasAttribute(k) { return map.has(k); },
    getAttribute(k) { return map.has(k) ? String(map.get(k)) : null; },
    setAttribute(k, v) {
      map.set(k, String(v));
      if (k === 'class') el.className = String(v);
      if (k === 'id') el.id = String(v);
    },
    removeAttribute(k) { map.delete(k); },
    appendChild(child) {
      if (child.parentNode) child.parentNode.removeChild(child);
      child.parentNode = el;
      el.childNodes.push(child);
      return child;
    },
    removeChild(child) {
      const idx = el.childNodes.indexOf(child);
      if (idx >= 0) el.childNodes.splice(idx, 1);
      child.parentNode = null;
      return child;
    },
    replaceChild(next, old) {
      if (next.parentNode) next.parentNode.removeChild(next);
      const idx = el.childNodes.indexOf(old);
      if (idx >= 0) {
        el.childNodes[idx] = next;
        next.parentNode = el;
        old.parentNode = null;
      }
      return old;
    },
    contains(other) {
      for (let p = other; p; p = p.parentNode) if (p === el) return true;
      return false;
    },
    matches(sel) {
      if (sel.includes('section.topic') && el.tagName === 'SECTION' && el.classList.contains('topic')) return true;
      if (sel.includes('.card.policy') && el.classList.contains('policy') && el.id.startsWith('item-')) return true;
      if (sel.includes('.tophead') && el.classList.contains('tophead')) return true;
      if (sel === 'details' && el.tagName === 'DETAILS') return true;
      if (sel === '.tdoc' && el.classList.contains('tdoc')) return true;
      if (sel === '.topic-stream' && el.classList.contains('topic-stream')) return true;
      return false;
    },
    closest(sel) {
      for (let p = el; p; p = p.parentNode) {
        if (sel === '.tophead' && p.classList && p.classList.contains('tophead')) return p;
        if (sel === '.overview-preview-stage' && p.classList && p.classList.contains('overview-preview-stage')) return p;
      }
      return null;
    },
    querySelector(sel) {
      if (sel === ':scope > .tophead') return el.children.find(c => c.classList.contains('tophead')) || null;
      return el.querySelectorAll(sel)[0] || null;
    },
    querySelectorAll(sel) {
      const out = [];
      (function walk(node) {
        for (const c of node.children) {
          let hit = false;
          if (sel === 'details') hit = c.tagName === 'DETAILS';
          else if (sel === '[data-max]') hit = c.hasAttribute('data-max');
          else if (sel === '.xterm, .tm-host') hit = c.classList.contains('xterm') || c.classList.contains('tm-host');
          else if (sel === 'iframe.doc-frame') hit = c.tagName === 'IFRAME' && c.classList.contains('doc-frame');
          else if (sel === 'section.topic[data-topic]') hit = c.tagName === 'SECTION' && c.classList.contains('topic') && c.hasAttribute('data-topic');
          else if (sel.includes('textarea')) hit = c.tagName === 'TEXTAREA' || c.tagName === 'INPUT';
          else if (sel.includes('.card.policy')) hit = c.classList.contains('policy') && c.id.startsWith('item-');
          else if (sel.startsWith('#')) hit = c.id === sel.slice(1);
          if (hit) out.push(c);
          walk(c);
        }
      })(el);
      return out;
    },
    cloneNode(deep) {
      const copyAttrs = {};
      for (const [k, v] of map.entries()) copyAttrs[k] = v;
      const clone = makeEl(tag, copyAttrs);
      clone.open = el.open;
      clone.value = el.value;
      clone.textContent = el.textContent;
      if (deep) {
        for (const c of el.childNodes) clone.appendChild(c.cloneNode(true));
      }
      return clone;
    },
  };
  for (const c of children) el.appendChild(c);
  return el;
}

function makeTopic(name, opts = {}) {
  const head = makeEl('div', { class: 'tophead', 'data-rev': opts.rev || 'v1' });
  const body = makeEl('div', { class: 'tdoc', 'data-rev': opts.rev || 'v1' }, [
    makeEl('div', { class: 'doc-body', _text: opts.bodyText || `Body of ${name}` }),
  ]);
  const stream = makeEl('div', { class: 'topic-stream' }, [
    makeEl('article', { class: 'wb-entry', id: `e-${name}`, _text: opts.entryText || `Entry of ${name}` }),
  ]);
  const children = [head, body, stream];
  if (opts.withTerm) children.push(makeEl('div', { class: 'xterm' }));
  return makeEl('section', {
    class: 'topic',
    id: `topic-${name}`,
    'data-topic': name,
    'data-th': opts.rev || 'v1',
  }, children);
}

function loadVirtualWorld(nodes = []) {
  const board = makeEl('div', { id: 'r-board', class: 'r-board' }, nodes);
  const bcol = makeEl('div', { id: 'bcol' }, [board]);
  const ctx = {
    window: null,
    location: { pathname: '/s/test/', search: '' },
    doc: {
      activeElement: null,
      documentElement: makeEl('html'),
      body: makeEl('body', {}, [bcol]),
      querySelector: () => null,
      querySelectorAll: () => [],
    },
    $: id => {
      if (id === 'bcol') return bcol;
      if (id === 'r-board') return board;
      return board.querySelector(`#${id}`);
    },
    region: name => (name === 'board' ? board : null),
    topicKey: n => (n && n.nodeType === 1 && n.tagName === 'SECTION' && n.classList.contains('topic') && n.hasAttribute('data-topic') ? n.getAttribute('data-topic') : null),
    reducedMotion: () => true,
    boardPending: {},
    boardFlushViewed: () => {},
    boardSnap: null,
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
    getComputedStyle: () => ({ overflowY: 'auto', paddingTop: '0' }),
    Date, Math, Set, Map, Array, Object, String, Number, Infinity, isNaN, parseFloat,
  };
  ctx.window = ctx;
  ctx.wbHistory = { capture: () => [], init: () => {}, restore: () => {} };
  ctx.WsCards = { isDocked: n => n.hasAttribute('data-test-docked'), decorate: () => {} };
  vm.createContext(ctx);
  vm.runInContext(anchorSrc, ctx);
  ctx.anchorSized.bounded = true;
  return { ctx, board, bcol };
}

test('Finding #12: steady-state anchorMark navigation is strictly O(1) across N=12 and N=60 with zero prunes', () => {
  function measureSingleSteps(n) {
    const nodes = Array.from({ length: n }, (_, i) => makeTopic(`t${i}`));
    const { ctx } = loadVirtualWorld(nodes);
    // Initial full reconcile at slot 0.
    ctx.anchorMark(nodes[0]);
    assert.equal(ctx.WsBoardVirtual.collapsedCount(), n - 3, `initial collapse leaves ±2 near window for N=${n}`);

    // Measure 5 consecutive single-slot steps in steady state (slots 1..5).
    ctx.WsBoardVirtual.resetStats();
    for (let step = 1; step <= 5; step++) {
      ctx.anchorMark(nodes[step]);
    }
    const stats = ctx.WsBoardVirtual.stats();
    // Verify window invariants still hold at slot 5 (slots 3..7 mounted, all others collapsed).
    for (let i = 0; i < n; i++) {
      const wantCollapsed = Math.abs(i - 5) > 2;
      assert.equal(nodes[i].hasAttribute('data-wb-virtual'), wantCollapsed, `slot ${i} collapsed=${wantCollapsed} at N=${n}`);
    }
    return stats;
  }

  const s12 = measureSingleSteps(12);
  const s60 = measureSingleSteps(60);

  assert.equal(s12.prunes, 0, 'steady-state navigation must never run virtualStore prune');
  assert.equal(s60.prunes, 0, 'steady-state navigation must never run virtualStore prune at N=60');
  assert.equal(s12.fullReconciles, 0, 'steady-state navigation uses delta path, not full reconcile');
  assert.equal(s60.fullReconciles, 0, 'steady-state navigation uses delta path at N=60');
  assert.equal(s12.deltaReconciles, 5);
  assert.equal(s60.deltaReconciles, 5);
  // Stepping from 0 -> 1 -> 2 -> 3 -> 4 -> 5:
  // steps 1, 2 have 0 leaving slots (near window grows from [0..2] -> [0..3] -> [0..4]);
  // steps 3, 4, 5 each have exactly 1 leaving slot (slot 0, slot 1, slot 2) => 3 total describes!
  assert.equal(s12.describes, 3, 'N=12 should inspect only the 3 leaving slots across 5 steps');
  assert.equal(s60.describes, 3, 'N=60 must perform the exact same O(1) describes as N=12');
});

test('Finding #12: virtualExemptSet tracks far exempt topics and collapses them once exemption clears, and virtualPrune removes dead keys', () => {
  const nodes = Array.from({ length: 12 }, (_, i) => makeTopic(`t${i}`, { withTerm: i === 1 }));
  const { ctx, board } = loadVirtualWorld(nodes);
  ctx.anchorMark(nodes[0]);
  // Step to slot 5: t1 leaves near window but has .xterm so it stays exempt and mounted.
  ctx.anchorMark(nodes[5]);
  assert.equal(nodes[1].hasAttribute('data-wb-virtual'), false, 'far topic with .xterm remains mounted');

  // Remove the .xterm from t1, then step to slot 6: delta reconcile re-checks virtualExemptSet and collapses t1!
  const term = nodes[1].querySelector('.xterm, .tm-host');
  term.parentNode.removeChild(term);
  ctx.WsBoardVirtual.resetStats();
  ctx.anchorMark(nodes[6]);
  assert.equal(nodes[1].hasAttribute('data-wb-virtual'), true, 'previously exempt far topic collapses once exemption clears');
  assert.equal(ctx.WsBoardVirtual.stats().describes, 2, 'only leaving slot + 1 previously exempt slot described');

  // Remove collapsed topic t11 from board and run keyed virtualPrune.
  board.removeChild(nodes[11]);
  nodes[11].isConnected = false;
  const beforeCount = ctx.WsBoardVirtual.collapsedCount();
  const pruned = ctx.WsBoardVirtual.prune();
  assert.equal(pruned, 1, 'virtualPrune drops removed topic t11');
  assert.equal(ctx.WsBoardVirtual.collapsedCount(), beforeCount - 1);
  assert.equal(ctx.WsBoardVirtual.stats().prunes, 1);
});

test('Finding #10: WsBoardVirtual.searchRoots / detachedNodes returns detached children and stashed revisions read-only without mutating live DOM', () => {
  const nodes = Array.from({ length: 10 }, (_, i) => makeTopic(`t${i}`, {
    bodyText: `UniqueBodyPhrase-${i}`,
    entryText: `UniqueEntryPhrase-${i}`,
  }));
  const { ctx } = loadVirtualWorld(nodes);
  ctx.anchorMark(nodes[0]);

  // Near topic t0 is mounted => searchRoots returns empty array.
  assert.equal(ctx.WsBoardVirtual.searchRoots('t0').length, 0);

  // Collapsed far topic t8 => searchRoots returns detached children without re-attaching them to live DOM.
  assert.equal(nodes[8].hasAttribute('data-wb-virtual'), true);
  assert.equal(nodes[8].children.length, 1, 'only .tophead connected');
  const roots = ctx.WsBoardVirtual.searchRoots('t8');
  assert.equal(roots.length, 2, 'returns detached .tdoc and .topic-stream');
  assert.equal(nodes[8].children.length, 1, 'live DOM remains untouched after searchRoots');
  assert.equal(roots[0].querySelector('.doc-body') || roots[0].children[0].textContent, 'UniqueBodyPhrase-8');

  // After a stashed SSE update on collapsed topic t8, searchRoots returns the stashed section with latest text.
  const incomingV2 = makeTopic('t8', {
    rev: 'v2',
    bodyText: 'StashedV2BodyPhrase-8',
    entryText: 'StashedV2EntryPhrase-8',
  });
  assert.equal(ctx.virtualStashUpdate(nodes[8], incomingV2), true);
  const stashedRoots = ctx.WsBoardVirtual.searchRoots('t8');
  assert.equal(stashedRoots.length, 1);
  assert.equal(stashedRoots[0].tagName, 'SECTION');
  assert.equal(stashedRoots[0].getAttribute('data-th'), 'v2');
  assert.equal(nodes[8].hasAttribute('data-wb-virtual'), true, 'topic stays collapsed after stashed search read');
});
