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
      for (const [name, value] of map.entries()) out.push({ name, value: String(value) });
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
  const body = makeEl('div', { class: 'tdoc', 'data-rev': opts.rev || 'v1' });
  const det = makeEl('details', { 'data-key': `fold-${name}` });
  det.open = !!opts.detailOpen;
  body.appendChild(det);
  const children = [head, body];
  if (opts.withTerm) children.push(makeEl('div', { class: 'xterm' }));
  if (opts.withAsk) children.push(makeEl('div', { class: 'tbody' }, [makeEl('div', { class: 'card policy', id: `item-${name}` })]));
  return makeEl('section', {
    class: 'topic',
    id: `topic-${name}`,
    'data-topic': name,
    'data-th': opts.rev || 'v1',
  }, children);
}

function makeCard(id, kind = 'terminal') {
  return makeEl('article', { class: 'bcard', 'data-card': id, 'data-kind': kind }, [
    makeEl('div', { class: 'bc-in' }, [makeEl('div', { class: 'bc-hd' }), makeEl('div', { class: 'bc-body' })]),
  ]);
}

function loadVirtualWorld(nodes = []) {
  const board = makeEl('div', { id: 'r-board', class: 'r-board' }, nodes);
  const bcol = makeEl('div', { id: 'bcol' }, [board]);
  const calls = { unmounted: [], restoredHistory: [], restoredOpen: [], mountedFrames: [], answersWritten: [] };
  const ctx = {
    window: null,
    location: { pathname: '/s/test/', search: '' },
    doc: {
      activeElement: null,
      documentElement: makeEl('html'),
      body: makeEl('body', {}, [bcol]),
      querySelector: () => null,
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
    unmountFrames: sec => calls.unmounted.push(sec.getAttribute('data-topic')),
    mountFrames: sec => calls.mountedFrames.push({ topic: sec.getAttribute('data-topic'), immediate: true }),
    mountFramesPaced: sec => calls.mountedFrames.push({ topic: sec.getAttribute('data-topic'), immediate: false }),
    readAnswers: sec => ({ [`ans-${sec.getAttribute('data-topic')}`]: 'draft-1' }),
    writeAnswers: (sec, a) => calls.answersWritten.push({ topic: sec.getAttribute('data-topic'), answers: a }),
    frameHeights: sec => ({ [`doc-${sec.getAttribute('data-topic')}`]: '320px' }),
    setFrameHeights: () => {},
    restoreOpen: sec => calls.restoredOpen.push(sec.getAttribute('data-topic')),
    watchBoard: () => {},
    bandBoard: () => {},
    fmtTimes: () => {},
    sweepOutImgs: () => {},
    indexFrames: () => {},
    mermaid: () => {},
    applySent: () => {},
    getComputedStyle: () => ({ overflowY: 'auto', paddingTop: '0' }),
    Date,
    Math,
    Set,
    Map,
    Array,
    Object,
    String,
    Number,
    Infinity,
    isNaN,
    parseFloat,
  };
  ctx.window = ctx;
  ctx.wbHistory = {
    capture: sec => [{ key: sec.getAttribute('data-topic'), rows: ['r1', 'r2'] }],
    init: () => {},
    restore: (sec, hist) => calls.restoredHistory.push({ topic: sec.getAttribute('data-topic'), hist }),
  };
  ctx.WsCards = {
    isDocked: n => n.hasAttribute('data-test-docked'),
    decorate: () => {},
  };
  vm.createContext(ctx);
  vm.runInContext(anchorSrc, ctx);
  ctx.anchorSized.bounded = true;
  return { ctx, board, bcol, calls };
}

test('planBoardVirtualWindow enforces ±2 near window around active slot', () => {
  const { ctx } = loadVirtualWorld();
  const items = Array.from({ length: 12 }, (_, i) => ({
    key: `t:t${i}`,
    isTopic: true,
    collapsed: false,
  }));

  const p0 = ctx.WsBoardVirtual.plan(items, 0, { nearSlots: 2, enabled: true, scrubbing: false });
  assert.equal(p0.skip, false);
  assert.deepEqual(
    p0.actions.map(a => a.action),
    ['keep', 'keep', 'keep', 'collapse', 'collapse', 'collapse', 'collapse', 'collapse', 'collapse', 'collapse', 'collapse', 'collapse'],
  );

  // Move to slot 6: slots 4..8 are near; if previously collapsed they hydrate, slots 0..3 & 9..11 collapse/stay collapsed.
  const itemsMid = items.map((it, idx) => ({ ...it, collapsed: idx > 2 }));
  const p6 = ctx.WsBoardVirtual.plan(itemsMid, 6, { nearSlots: 2, enabled: true, scrubbing: false });
  assert.deepEqual(
    p6.actions.map(a => a.action),
    ['collapse', 'collapse', 'collapse', 'keep-collapsed', 'hydrate', 'hydrate', 'hydrate', 'hydrate', 'hydrate', 'keep-collapsed', 'keep-collapsed', 'keep-collapsed'],
  );
});

test('planBoardVirtualWindow strictly exempts bcards, docked, overview-live, maximized, terminals, active edits, and held selections', () => {
  const { ctx } = loadVirtualWorld();
  const items = [
    { key: 't:active', isTopic: true, collapsed: false },
    { key: 't:near1', isTopic: true, collapsed: false },
    { key: 't:near2', isTopic: true, collapsed: false },
    { key: 'c:term-1', isTopic: false, collapsed: false },
    { key: 't:docked', isTopic: true, collapsed: false, docked: true },
    { key: 't:ov', isTopic: true, collapsed: true, overviewLive: true },
    { key: 't:max', isTopic: true, collapsed: false, maximized: true },
    { key: 't:pty', isTopic: true, collapsed: false, hasTerminal: true },
    { key: 't:edit', isTopic: true, collapsed: false, hasActiveEdit: true },
    { key: 't:sel', isTopic: true, collapsed: false, hasSelection: true },
    { key: 't:far-normal', isTopic: true, collapsed: false },
  ];

  const plan = ctx.WsBoardVirtual.plan(items, 0, { nearSlots: 2, enabled: true, scrubbing: false });
  assert.deepEqual(plan.actions.map(a => a.action), [
    'keep',
    'keep',
    'keep',
    'exempt',
    'keep',
    'hydrate',
    'keep',
    'keep',
    'keep',
    'keep',
    'collapse',
  ]);
});

test('planBoardVirtualWindow defers during scrubbing and hydrates all when unbounded', () => {
  const { ctx } = loadVirtualWorld();
  const items = [
    { key: 't:0', isTopic: true, collapsed: false },
    { key: 't:1', isTopic: true, collapsed: true },
    { key: 't:2', isTopic: true, collapsed: true },
    { key: 't:3', isTopic: true, collapsed: true },
  ];

  const scrub = ctx.WsBoardVirtual.plan(items, 3, { nearSlots: 2, enabled: true, scrubbing: true });
  assert.equal(scrub.skip, true);
  assert.equal(scrub.actions.length, 0);

  const unbounded = ctx.WsBoardVirtual.plan(items, 0, { nearSlots: 2, enabled: false, scrubbing: false });
  assert.equal(unbounded.skip, false);
  assert.deepEqual(Array.from(unbounded.actions, a => a.action), ['keep', 'hydrate', 'hydrate', 'hydrate']);
});

test('virtualCollapse, virtualStashUpdate, and virtualHydrate preserve details[open], history, scrollTop, and reconcile stashed revisions', () => {
  const nodes = [
    makeCard('term-1', 'terminal'),
    ...Array.from({ length: 8 }, (_, i) => makeTopic(`t${i}`, { detailOpen: i === 6 })),
  ];
  const { ctx, calls } = loadVirtualWorld(nodes);
  ctx.anchorMark(nodes[0]); // slot 0 active => slots 0..2 near, slots 3..8 far

  // .bcard at slot 0 is never collapsed; topics t0, t1 (slots 1, 2) stay hydrated; t2..t7 (slots 3..8) collapse.
  assert.equal(nodes[0].hasAttribute('data-wb-virtual'), false);
  assert.equal(nodes[1].hasAttribute('data-wb-virtual'), false);
  assert.equal(nodes[2].hasAttribute('data-wb-virtual'), false);
  assert.equal(nodes[3].hasAttribute('data-wb-virtual'), true);
  assert.equal(nodes[7].hasAttribute('data-wb-virtual'), true); // t6
  assert.equal(nodes[7].children.length, 1, 'collapsed far topic keeps only .tophead');
  assert.equal(nodes[7].children[0].className, 'tophead');

  // Stash an incoming server revision v2 for collapsed far topic t6.
  const incomingV2 = makeTopic('t6', { rev: 'v2', detailOpen: false });
  const stashed = ctx.virtualStashUpdate(nodes[7], incomingV2);
  assert.equal(stashed, true);
  assert.equal(ctx.WsBoardVirtual.hasStashed('t6'), true);
  assert.equal(nodes[7].children.length, 1, 'stashed update does not mount body while collapsed');
  assert.equal(nodes[7].children[0].getAttribute('data-rev'), 'v2', '.tophead updated in place');
  assert.equal(nodes[7].getAttribute('data-th'), 'v2', 'data-th updated in place');

  // Re-entering ±2 window (selecting t6) hydrates stashed revision v2 and restores details[open] + history + answers.
  ctx.anchorMark(nodes[7]);
  assert.equal(nodes[7].hasAttribute('data-wb-virtual'), false);
  assert.equal(ctx.WsBoardVirtual.hasStashed('t6'), false);
  assert.equal(nodes[7].children.length, 2, 'body mounted on hydrate');
  assert.equal(nodes[7].children[1].getAttribute('data-rev'), 'v2', 'hydrated body is latest stashed revision v2');
  const det = nodes[7].querySelector('details');
  assert.equal(det.open, true, 'details[open] preserved across collapse -> stashed revision -> hydrate');
  assert.ok(calls.restoredHistory.some(x => x.topic === 't6' && x.hist[0].rows.length === 2), 'wbHistory.restore called with captured rows');
  assert.ok(calls.answersWritten.some(x => x.topic === 't6' && x.answers['ans-t6'] === 'draft-1'), 'answers restored');

  // Now t0 and t1 (slots 1, 2) are far from slot 7 and should be collapsed, while .bcard at slot 0 remains exempt!
  assert.equal(nodes[0].hasAttribute('data-wb-virtual'), false, '.bcard remains strictly exempt when far');
  assert.equal(nodes[1].hasAttribute('data-wb-virtual'), true, 'far topic t0 collapsed after moving to t6');
  assert.equal(nodes[2].hasAttribute('data-wb-virtual'), true, 'far topic t1 collapsed after moving to t6');
});
