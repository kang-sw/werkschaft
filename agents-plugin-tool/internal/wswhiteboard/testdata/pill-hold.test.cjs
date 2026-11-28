'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const pillSource = fs.readFileSync(path.join(__dirname, '../assets/pill.js'), 'utf8');
const cardFindSource = fs.readFileSync(path.join(__dirname, '../assets/card-find.js'), 'utf8');

function makeThrowingStorage() {
  const fail = op => () => { throw new Error(`unexpected localStorage.${op} call`); };
  return {
    getItem: fail('getItem'),
    setItem: fail('setItem'),
    removeItem: fail('removeItem')
  };
}

function makePillHarness(itemSpecs, opts = {}) {
  class El {
    constructor(tag) {
      this.tag = tag;
      this.children = [];
      this.style = {};
      this.attrs = {};
      this.cls = {};
      this.q = {};
      this.parent = null;
      this.top = 0;
      this.hidden = false;
      this.textContent = '';
      this.listeners = {};
      const me = this;
      this.classList = {
        contains: c => !!me.cls[c],
        toggle: (c, on) => { me.cls[c] = on === undefined ? !me.cls[c] : !!on; },
        add: c => { me.cls[c] = true; },
        remove: c => { delete me.cls[c]; }
      };
    }
    set className(v) {
      this.cls = {};
      String(v || '').split(/\s+/).forEach(c => { if (c) this.cls[c] = true; });
    }
    get firstChild() { return this.children[0] || null; }
    all(out = []) {
      this.children.forEach(c => { out.push(c); c.all(out); });
      return out;
    }
    querySelectorAll(sel) {
      if (sel in this.q) return this.q[sel];
      if (sel[0] === '.') {
        const cls = sel.slice(1).split('.');
        return this.all([]).filter(e => cls.every(c => e.cls[c]));
      }
      return [];
    }
    querySelector(sel) {
      if (sel in this.q) return this.q[sel][0] || null;
      return this.querySelectorAll(sel)[0] || null;
    }
    closest(sel) {
      const parts = sel.split(',').map(s => s.trim());
      for (let e = this; e; e = e.parent) {
        for (const p of parts) {
          const cls = p.replace(/^[a-z]+/i, '').split('.').filter(Boolean);
          if (cls.length && cls.every(c => e.cls[c])) return e;
        }
      }
      return null;
    }
    getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
    setAttribute(k, v) { this.attrs[k] = String(v); }
    hasAttribute(k) { return k in this.attrs; }
    appendChild(c) { c.parent = this; this.children.push(c); return c; }
    insertBefore(c) { c.parent = this; this.children.unshift(c); return c; }
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
    removeEventListener(type, fn) {
      if (!this.listeners[type]) return;
      this.listeners[type] = this.listeners[type].filter(f => f !== fn);
    }
    getBoundingClientRect() { return { top: this.top, bottom: this.top + 40, width: 100, height: 40 }; }
    scrollIntoView() { jumped.push(this.attrs['data-id'] || this.attrs.id); }
  }

  function makeItem(spec) {
    const el = new El('div');
    el.className = (spec.cls || 'card policy') + (spec.held ? ' wb-held' : '');
    el.attrs.id = 'item-' + spec.id;
    el.attrs['data-id'] = spec.dataId || String(spec.id);
    el.top = spec.top;
    const title = new El('span');
    title.className = 'title';
    title.textContent = spec.title || ('Question ' + spec.id);
    el.appendChild(title);
    if (spec.ctx) {
      const ctx = new El('div');
      ctx.className = 'ctx';
      ctx.textContent = spec.ctx;
      el.appendChild(ctx);
    }
    if (spec.options) {
      spec.options.forEach((opt, i) => {
        const o = new El('button');
        o.className = 'opt';
        o.attrs['data-opt'] = String.fromCharCode(97 + i);
        o.textContent = opt;
        el.appendChild(o);
      });
    }
    if (spec.askEntry) {
      const jump = new El('a');
      jump.className = 'wb-askjump';
      jump.attrs.href = '#' + spec.askEntry;
      if (spec.reasked) {
        const re = new El('span');
        re.className = 'wb-askre';
        re.textContent = 'Asked again ' + spec.reasked;
        jump.appendChild(re);
      }
      el.appendChild(jump);
    }
    const holdBtn = new El('button');
    holdBtn.className = 'btn mini wb-hold-toggle';
    holdBtn.attrs['data-wb-hold'] = '';
    holdBtn.attrs['aria-pressed'] = spec.held ? 'true' : 'false';
    el.appendChild(holdBtn);
    el.holdBtn = holdBtn;
    if (spec.picked) el.q['.opt[data-opt][aria-pressed="true"]'] = [new El('button')];
    if (spec.note) {
      const n = new El('textarea');
      n.value = spec.note;
      el.q['textarea.note'] = [n];
    }
    if (spec.sent) el.cls['sent-sent'] = true;
    return el;
  }

  const storage = opts.storage || makeThrowingStorage();
  const requests = [];
  const serverHeld = new Set((opts.serverHeld || itemSpecs.filter(s => s.held).map(s => String(parseInt(s.id, 10)))));
  const fetchImpl = opts.fetch || (async (url, init) => {
    const body = JSON.parse(init.body);
    requests.push({ url, method: init.method, credentials: init.credentials, body });
    const id = String(parseInt(body.id, 10));
    if (body.held) serverHeld.add(id);
    else serverHeld.delete(id);
    const held = Array.from(serverHeld).sort((a, b) => Number(a) - Number(b));
    return { ok: true, status: 200, json: async () => ({ held }) };
  });

  let items = itemSpecs.map(makeItem);
  const host = new El('div');
  const html = new El('html');
  html.attrs['data-rid'] = opts.rid || 'sess-1';
  if (opts.incarnation) html.attrs['data-board-incarnation'] = opts.incarnation;
  const frames = [], jumped = [], titleCounts = [];
  const doc = {
    body: new El('body'),
    documentElement: html,
    getElementById: id => (id === 'bcol' ? host : null),
    querySelector: sel => (sel === '.topic' && opts.hasTopics !== false ? new El('section') : null),
    querySelectorAll: () => items,
    createElement: tag => new El(tag)
  };
  const win = {
    localStorage: storage,
    fetch: fetchImpl,
    IntersectionObserver: function () { this.observe = () => {}; this.disconnect = () => {}; },
    innerHeight: 800,
    scrollY: 20,
    addEventListener: () => {},
    removeEventListener: () => {},
    wbTitleCount: n => titleCounts.push(n)
  };
  const ctx = vm.createContext({
    console,
    document: doc,
    window: win,
    IntersectionObserver: win.IntersectionObserver,
    Set, Map, Array, Object, JSON, Math, String, Number, parseInt, encodeURIComponent,
    requestAnimationFrame: fn => { frames.push(fn); return frames.length; },
    cancelAnimationFrame: () => {},
    getComputedStyle: () => ({ overflowY: 'visible' }),
    wsT: k => k
  });

  function run() {
    vm.runInContext(pillSource, ctx);
    flush();
  }
  function swap(nextSpecs) {
    items = nextSpecs.map(makeItem);
    run();
    return items;
  }
  function flush() {
    while (frames.length) frames.shift()();
  }
  function pill(idx) { return host.children[idx].children[0]; }
  function shown(p) {
    if (p.hidden) return null;
    return p.querySelectorAll('.oq-seg')
      .filter(b => !b.hidden)
      .map(b => [b.getAttribute('data-k'), b.children[0].textContent, b.querySelector('.n').textContent]);
  }
  function pressSeg(p, k) {
    const b = p.querySelectorAll('.oq-seg').find(s => s.getAttribute('data-k') === k);
    p.onclick({ target: b.children[1] });
  }
  function clickHold(item) {
    const handlers = host.listeners.click || [];
    handlers.forEach(fn => fn({ target: item.holdBtn, preventDefault() {} }));
    flush();
  }
  function makeDetachedItem(spec) { return makeItem(spec); }

  run();
  return { get items() { return items; }, host, storage, win, requests, serverHeld, jumped, titleCounts, flush, pill, shown, pressSeg, clickHold, run, swap, makeDetachedItem };
}

async function drainMicrotasks() {
  for (let i = 0; i < 6; i++) await Promise.resolve();
}

test('held ask posts explicit route request, leaves open count and tab title, surfaces in held pill, and follows server state on swap without localStorage', async () => {
  const h = makePillHarness([
    { id: 1, dataId: '01', top: -300, title: 'Choose storage' },
    { id: 2, dataId: '02', top: -180, title: 'Confirm rollout' },
    { id: 3, dataId: '03', top: 950, title: 'Approve retry cap' }
  ]);
  assert.equal(h.titleCounts.at(-1), 3);
  assert.deepEqual(h.shown(h.pill(0)), [['open', 'js.pill.open', '2']]);
  assert.deepEqual(h.shown(h.pill(1)), [['open', 'js.pill.open', '1']]);

  // Hold item 1 (above, zero-padded data-id "01") and item 3 (below).
  h.clickHold(h.items[0]);
  h.clickHold(h.items[2]);
  assert.equal(h.items[0].classList.contains('wb-held'), true, 'optimistic wb-held applied immediately');
  assert.equal(h.items[0].holdBtn.getAttribute('aria-pressed'), 'true');
  assert.equal(h.win.wbPill.isHeld(h.items[0]), true);
  assert.equal(h.titleCounts.at(-1), 1, 'held items leave tab-title open total');
  assert.deepEqual(h.shown(h.pill(0)), [
    ['open', 'js.pill.open', '1'],
    ['held', 'js.pill.held', '1']
  ]);
  assert.deepEqual(h.shown(h.pill(1)), [['held', 'js.pill.held', '1']]);

  // Clicking the held pill segment jumps directly to the held item.
  h.pressSeg(h.pill(0), 'held');
  h.pressSeg(h.pill(1), 'held');
  assert.deepEqual(h.jumped, ['01', '03']);

  await drainMicrotasks();
  h.flush();
  assert.deepEqual(h.requests, [
    { url: '/s/sess-1/board/hold', method: 'POST', credentials: 'same-origin', body: { id: '01', held: true } },
    { url: '/s/sess-1/board/hold', method: 'POST', credentials: 'same-origin', body: { id: '03', held: true } }
  ]);

  // Board swap renders server-held state for item 3 only (item 1 auto-cleared on server).
  // pill.js trusts the swapped server DOM and never re-asserts item 1 from local state.
  h.swap([
    { id: 1, dataId: '01', top: -300, title: 'Choose storage (updated)', held: false },
    { id: 2, dataId: '02', top: -180, title: 'Confirm rollout', held: false },
    { id: 3, dataId: '03', top: 950, title: 'Approve retry cap', held: true }
  ]);
  assert.equal(h.items[0].classList.contains('wb-held'), false);
  assert.equal(h.items[2].classList.contains('wb-held'), true);
  assert.equal(h.titleCounts.at(-1), 2);
  assert.deepEqual(h.shown(h.pill(0)), [['open', 'js.pill.open', '2']]);
  assert.deepEqual(h.shown(h.pill(1)), [['held', 'js.pill.held', '1']]);

  // wbPill.isHeld works on detached SSE-parsed fragments with zero localStorage access.
  const detachedHeld = h.makeDetachedItem({ id: 3, dataId: '03', top: 0, held: true });
  const detachedOpen = h.makeDetachedItem({ id: 1, dataId: '01', top: 0, held: false });
  assert.equal(h.win.wbPill.isHeld(detachedHeld), true);
  assert.equal(h.win.wbPill.isHeld(detachedOpen), false);
});

test('staged draft on a server-held item clears the hold once via route (404/409 treated as success) and removing draft does not restore hold', async () => {
  const responses = [
    { ok: true, status: 200, json: async () => ({ held: ['21', '22'] }) },
    { ok: false, status: 404, json: async () => ({}) },
    { ok: false, status: 409, json: async () => ({}) }
  ];
  const calls = [];
  const h = makePillHarness([
    { id: 20, dataId: '20', top: -300, title: 'Pick mode', held: true },
    { id: 21, dataId: '21', top: -200, title: 'Ack change', held: true },
    { id: 22, dataId: '22', top: -100, title: 'Check item', held: true }
  ], {
    rid: 'board-b',
    fetch: async (url, init) => {
      calls.push({ url, body: JSON.parse(init.body) });
      return responses.shift() || { ok: true, status: 200, json: async () => ({ held: [] }) };
    }
  });
  assert.equal(h.titleCounts.at(-1), 0);
  assert.equal(calls.length, 0);

  // Stage a draft pick on item 20 -> clears hold optimistically and posts held:false once.
  h.items[0].q['.opt[data-opt][aria-pressed="true"]'] = [h.makeDetachedItem({ id: 99, top: 0 })];
  h.win.wbPill.update();
  h.flush();
  h.win.wbPill.update();
  h.flush();
  await drainMicrotasks();
  h.flush();
  assert.equal(h.items[0].classList.contains('wb-held'), false);
  assert.deepEqual(calls, [{ url: '/s/board-b/board/hold', body: { id: '20', held: false } }]);

  // Removing the draft does not restore the hold.
  delete h.items[0].q['.opt[data-opt][aria-pressed="true"]'];
  h.win.wbPill.update();
  h.flush();
  assert.equal(h.items[0].classList.contains('wb-held'), false);
  assert.equal(h.titleCounts.at(-1), 1);

  // Staging a draft on item 21 (404) and item 22 (409) treats both as success without reverting.
  h.items[1].cls['sent-sent'] = true;
  h.items[2].q['textarea.note'] = [{ value: 'looks good' }];
  h.win.wbPill.update();
  h.flush();
  await drainMicrotasks();
  h.flush();
  assert.equal(h.items[1].classList.contains('wb-held'), false);
  assert.equal(h.items[2].classList.contains('wb-held'), false);
  assert.deepEqual(calls.slice(1), [
    { url: '/s/board-b/board/hold', body: { id: '21', held: false } },
    { url: '/s/board-b/board/hold', body: { id: '22', held: false } }
  ]);
  assert.deepEqual(h.shown(h.pill(0)), [
    ['open', 'js.pill.open', '1'],
    ['ans', 'js.pill.answered', '2'],
    ['nt', 'js.pill.noted', '1']
  ]);
});

test('toggleHold reverts optimistic state when hold route fails', async () => {
  const h = makePillHarness([
    { id: 30, dataId: '30', top: -200, title: 'Failing hold' }
  ], {
    rid: 'board-err',
    fetch: async () => ({ ok: false, status: 500, json: async () => ({}) })
  });
  h.clickHold(h.items[0]);
  assert.equal(h.items[0].classList.contains('wb-held'), true, 'optimistic hold before failure');
  await drainMicrotasks();
  h.flush();
  assert.equal(h.items[0].classList.contains('wb-held'), false, 'reverted after 500 response');
  assert.equal(h.items[0].holdBtn.getAttribute('aria-pressed'), 'false');
  assert.equal(h.titleCounts.at(-1), 1);
});

test('out-of-order hold response does not overwrite newer settled server state', async () => {
  const resolvers = [];
  const h = makePillHarness([
    { id: 40, dataId: '40', top: -200, title: 'First ask' },
    { id: 41, dataId: '41', top: -100, title: 'Second ask' }
  ], {
    rid: 'board-ooo',
    fetch: () => new Promise(resolve => { resolvers.push(resolve); })
  });
  // Click item 40 (seq 1) then item 41 (seq 2).
  h.clickHold(h.items[0]);
  h.clickHold(h.items[1]);
  assert.equal(resolvers.length, 2);

  // Newer response (seq 2) settles first with both held.
  resolvers[1]({ ok: true, status: 200, json: async () => ({ held: ['40', '41'] }) });
  await drainMicrotasks();
  h.flush();
  assert.equal(h.items[0].classList.contains('wb-held'), true);
  assert.equal(h.items[1].classList.contains('wb-held'), true);

  // Older response (seq 1) arrives late with stale snapshot ['40'] only -> must NOT unhold 41.
  resolvers[0]({ ok: true, status: 200, json: async () => ({ held: ['40'] }) });
  await drainMicrotasks();
  h.flush();
  assert.equal(h.items[0].classList.contains('wb-held'), true);
  assert.equal(h.items[1].classList.contains('wb-held'), true, 'stale response must not overwrite newer hold');
});

// A minimal element tree for the hold-echo comparison: attributes in order,
// child nodes with text, and the few queries holdOnly/adoptHolds use.
function tree(tag, attrs, ...kids) {
  const el = {
    nodeType: 1, tagName: tag.toUpperCase(), parentNode: null, childNodes: [], attrs: Object.assign({}, attrs),
    get attributes() { return Object.keys(el.attrs).map(name => ({ name, value: el.attrs[name] })); },
    get id() { return el.attrs.id || ''; },
    get title() { return el.attrs.title || ''; },
    set title(v) { el.attrs.title = String(v); },
    getAttribute: k => (k in el.attrs ? el.attrs[k] : null),
    setAttribute: (k, v) => { el.attrs[k] = String(v); },
    hasAttribute: k => k in el.attrs,
    classList: {
      contains: c => (el.attrs.class || '').split(/\s+/).includes(c),
      toggle: (c, on) => {
        const list = (el.attrs.class || '').split(/\s+/).filter(x => x && x !== c);
        if (on) list.push(c);
        el.attrs.class = list.join(' ');
      }
    },
    matches: sel => sel === 'section.topic' && el.tagName === 'SECTION' && el.classList.contains('topic'),
    all() { return el.childNodes.filter(n => n.nodeType === 1).flatMap(n => [n, ...n.all()]); },
    querySelectorAll(sel) {
      if (sel === '[id^="item-"]') return el.all().filter(n => n.id.startsWith('item-'));
      if (sel === '.wb-hold-toggle[data-wb-hold]') return el.all().filter(n => n.classList.contains('wb-hold-toggle') && n.hasAttribute('data-wb-hold'));
      throw new Error('unexpected selector ' + sel);
    },
    querySelector(sel) {
      if (sel === '.wb-hold-toggle') return el.all().find(n => n.classList.contains('wb-hold-toggle')) || null;
      throw new Error('unexpected selector ' + sel);
    },
    closest(sel) {
      if (sel !== '[id^="item-"]') throw new Error('unexpected selector ' + sel);
      for (let p = el; p; p = p.parentNode) if (p.id.startsWith('item-')) return p;
      return null;
    }
  };
  kids.forEach(k => {
    const n = typeof k === 'string' ? { nodeType: 3, nodeValue: k, parentNode: null } : k;
    n.parentNode = el;
    el.childNodes.push(n);
  });
  return el;
}

// A topic as page.tmpl renders it, with the given held item numbers.
function renderedTopic(held, opts = {}) {
  const item = n => tree('div', { class: 'card policy' + (held.includes(n) ? ' wb-held' : ''), id: 'item-' + n, 'data-id': String(n) },
    tree('span', { class: 'title' }, opts.title && n === 1 ? opts.title : 'Ask ' + n),
    tree('button', { type: 'button', class: 'btn mini wb-hold-toggle', 'data-wb-hold': '', 'aria-pressed': String(held.includes(n)), title: held.includes(n) ? 'Release' : 'Hold', 'aria-label': held.includes(n) ? 'Release' : 'Hold' }));
  const chip = (kind, count) => tree('span', Object.assign({ class: 'chip', 'data-tbar-ask': kind }, count ? {} : { hidden: '' }),
    tree('span', { class: 'chip-lbl' }, kind), tree('span', { class: 'n' }, String(count)));
  return tree('section', { class: 'topic', id: 'topic-a', 'data-topic': 'a', 'data-th': 'th-' + held.join('-') },
    tree('div', { class: 'tophead' }, tree('div', { class: 'tbar' }, chip('open', 2 - held.length), chip('held', held.length),
      tree('span', { class: 'n' }, opts.count || '7'))),
    tree('div', { class: 'tdoc' }, opts.body || 'Body'),
    item(1), item(2));
}

test('holdOnly tells a hold-only topic echo from any other change, and adoptHolds takes the item markers in place', () => {
  const h = makePillHarness([{ id: 1, dataId: '1', top: -100 }]);
  const { holdOnly, adoptHolds } = h.win.wbPill;
  const prev = renderedTopic([]);
  assert.equal(holdOnly(prev, renderedTopic([2])), true, 'held class, toggle state and labels, ask chips and data-th only');
  assert.equal(holdOnly(renderedTopic([2]), renderedTopic([])), true, 'releasing is hold-only too');
  assert.equal(holdOnly(prev, renderedTopic([2], { body: 'Body edited' })), false, 'a body change');
  assert.equal(holdOnly(prev, renderedTopic([], { title: 'Ask 1 edited' })), false, 'an item title change');
  assert.equal(holdOnly(prev, renderedTopic([], { count: '8' })), false, 'a count outside the ask chips');
  const extra = renderedTopic([2]);
  extra.childNodes[2].attrs.class += ' sent-sent';
  assert.equal(holdOnly(prev, extra), false, 'another class token beside wb-held');
  assert.equal(holdOnly(null, prev), false);

  // The live section carries what the page added (a mounted frame), so it
  // is never compared; only its item markers follow the echo.
  const live = renderedTopic([1]);
  live.childNodes.push(tree('iframe', { class: 'doc-frame' }));
  adoptHolds(live, renderedTopic([2]));
  const [one, two] = live.querySelectorAll('[id^="item-"]');
  assert.equal(one.classList.contains('wb-held'), false);
  assert.equal(one.querySelector('.wb-hold-toggle').getAttribute('aria-pressed'), 'false');
  assert.equal(two.classList.contains('wb-held'), true);
  assert.equal(two.querySelector('.wb-hold-toggle').getAttribute('aria-pressed'), 'true');
  assert.equal(two.querySelector('.wb-hold-toggle').getAttribute('aria-label'), 'js.pill.unhold');
});

test('pill.js contains zero browser-local hold machinery or localStorage access', () => {
  for (const forbidden of [
    'localStorage',
    'HOLD_PREFIX',
    'holdStorageKey',
    'itemFingerprint',
    'loadHolds',
    'saveHolds',
    'reconcileHolds',
    'onStorage',
    'st.fingerprint',
    'st.boardKey'
  ]) {
    assert.equal(pillSource.includes(forbidden), false, `pill.js must not contain ${forbidden}`);
  }
  const h = makePillHarness([{ id: 1, dataId: '01', top: -100, title: 'Ask' }]);
  assert.equal(typeof h.win.wbPill.isHeld, 'function');
  assert.equal(typeof h.win.wbPill.toggleHold, 'function');
  assert.equal(h.win.wbPill.fingerprint, undefined);
  assert.equal(h.win.wbPill.boardKey, undefined);
});

test('.tbar splits policy+check asks into open/answered/held chips, excludes acks, and mirrors live updates to Overview placeholders', async () => {
  const formSource = fs.readFileSync(path.join(__dirname, '../assets/form.js'), 'utf8');
  const overviewSource = fs.readFileSync(path.join(__dirname, '../../wsdashboard/assets/overview.js'), 'utf8');

  function matchSingle(el, sel) {
    sel = sel.trim();
    if (!sel) return false;
    const m = sel.match(/^([a-z0-9]*)((?:[.#][\w-]+|\[[\w-]+(?:\^?="[^"]*")?\]|:not\(\[[\w-]+\]\))*)$/i);
    if (!m) return false;
    if (m[1] && el.tagName !== m[1].toUpperCase()) return false;
    const tokens = m[2].match(/[.#][\w-]+|\[[\w-]+(?:\^?="[^"]*")?\]|:not\(\[[\w-]+\]\)/g) || [];
    return tokens.every(tok => {
      if (tok[0] === '.') return el.classList.contains(tok.slice(1));
      if (tok[0] === '#') return el.getAttribute('id') === tok.slice(1);
      if (tok.startsWith(':not([')) return !el.hasAttribute(tok.slice(6, -2));
      const pfx = tok.indexOf('^=');
      if (pfx >= 0) return String(el.getAttribute(tok.slice(1, pfx)) || '').startsWith(tok.slice(pfx + 3, -2));
      const eq = tok.indexOf('=');
      if (eq < 0) return el.hasAttribute(tok.slice(1, -1));
      return el.getAttribute(tok.slice(1, eq)) === tok.slice(eq + 2, -2);
    });
  }

  class NodeEl {
    constructor(tag, attrs = {}, children = []) {
      this.nodeType = 1;
      this.tagName = tag.toUpperCase();
      this.attrs = {};
      this.children = [];
      this.parentNode = null;
      this.style = {};
      this.hidden = !!attrs._hidden;
      this.inert = false;
      this.checked = !!attrs._checked;
      this.value = attrs._value || '';
      this._text = attrs._text || '';
      this._rect = attrs._rect || { top: -200, bottom: -140, left: 0, right: 400, width: 400, height: 60 };
      this.listeners = {};
      for (const [k, v] of Object.entries(attrs)) {
        if (k.startsWith('_')) continue;
        this.attrs[k] = String(v);
      }
      const me = this;
      this.classList = {
        contains: c => (me.attrs.class || '').split(/\s+/).filter(Boolean).includes(c),
        add: c => {
          const p = (me.attrs.class || '').split(/\s+/).filter(Boolean);
          if (!p.includes(c)) p.push(c);
          me.attrs.class = p.join(' ');
        },
        remove: c => {
          me.attrs.class = (me.attrs.class || '').split(/\s+/).filter(x => x && x !== c).join(' ');
        },
        toggle: (c, on) => {
          const want = on === undefined ? !me.classList.contains(c) : !!on;
          if (want) me.classList.add(c); else me.classList.remove(c);
          return want;
        }
      };
      for (const ch of children) this.appendChild(ch);
    }
    get parentElement() { return this.parentNode; }
    get firstChild() { return this.children[0] || null; }
    get className() { return this.attrs.class || ''; }
    set className(v) { this.attrs.class = String(v); }
    get id() { return this.attrs.id || ''; }
    set id(v) { this.attrs.id = String(v); }
    get textContent() {
      if (!this.children.length) return this._text;
      return this._text + this.children.map(c => c.textContent).join('');
    }
    set textContent(v) {
      this._text = String(v);
      this.children.forEach(c => { c.parentNode = null; });
      this.children = [];
    }
    get isConnected() {
      for (let p = this; p; p = p.parentNode) if (p === html) return true;
      return false;
    }
    hasAttribute(k) { return k in this.attrs; }
    getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
    setAttribute(k, v) { this.attrs[k] = String(v); }
    removeAttribute(k) { delete this.attrs[k]; }
    appendChild(n) {
      if (n.parentNode) n.parentNode.removeChild(n);
      n.parentNode = this;
      this.children.push(n);
      return n;
    }
    insertBefore(n, ref) {
      if (n.parentNode) n.parentNode.removeChild(n);
      n.parentNode = this;
      const i = ref ? this.children.indexOf(ref) : -1;
      if (i < 0) this.children.push(n); else this.children.splice(i, 0, n);
      return n;
    }
    removeChild(n) {
      const i = this.children.indexOf(n);
      if (i >= 0) this.children.splice(i, 1);
      n.parentNode = null;
      return n;
    }
    remove() { if (this.parentNode) this.parentNode.removeChild(this); }
    contains(n) {
      for (let p = n; p; p = p.parentNode) if (p === this) return true;
      return false;
    }
    closest(sel) {
      const parts = sel.split(',').map(s => s.trim());
      for (let p = this; p; p = p.parentNode) {
        if (parts.some(part => matchSingle(p, part))) return p;
      }
      return null;
    }
    cloneNode(deep) {
      const a = Object.assign({}, this.attrs);
      a._text = this._text;
      a._hidden = this.hidden;
      a._rect = Object.assign({}, this._rect);
      const clone = new NodeEl(this.tagName.toLowerCase(), a);
      if (deep) for (const ch of this.children) clone.appendChild(ch.cloneNode(true));
      return clone;
    }
    querySelectorAll(sel) {
      const parts = sel.split(',').map(s => s.trim()).filter(Boolean);
      const out = [];
      for (const s of parts) {
        if (s.includes(' > ')) {
          const fromScope = s.startsWith(':scope > ');
          const chain = (fromScope ? s.slice(':scope > '.length) : s).split(' > ').map(x => x.trim());
          let cur = fromScope ? [this] : this.querySelectorAll(chain.shift());
          for (const seg of chain) {
            const next = [];
            for (const node of cur) {
              for (const ch of node.children) if (matchSingle(ch, seg)) next.push(ch);
            }
            cur = next;
          }
          for (const hit of cur) if (!out.includes(hit)) out.push(hit);
        } else {
          const toks = s.split(/\s+/).filter(Boolean);
          const matchAncestors = (node, idx) => {
            if (!matchSingle(node, toks[idx])) return false;
            if (idx === 0) return true;
            for (let p = node.parentNode; p && p !== this.parentNode; p = p.parentNode) {
              if (matchAncestors(p, idx - 1)) return true;
            }
            return false;
          };
          const walk = node => {
            for (const ch of node.children) {
              if (matchAncestors(ch, toks.length - 1) && !out.includes(ch)) out.push(ch);
              walk(ch);
            }
          };
          walk(this);
        }
      }
      return out;
    }
    querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
    getBoundingClientRect() { return Object.assign({}, this._rect); }
    focus() { doc.activeElement = this; }
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
    removeEventListener(type, fn) {
      if (!this.listeners[type]) return;
      this.listeners[type] = this.listeners[type].filter(f => f !== fn);
    }
    dispatch(type, evt = {}) {
      const e = Object.assign({ type, target: this, preventDefault() {}, stopImmediatePropagation() {} }, evt);
      for (let p = this; p; p = p.parentNode) {
        for (const fn of (p.listeners[type] || [])) fn(e);
      }
    }
  }

  function makePolicy(id, opts = {}) {
    const optA = new NodeEl('button', { class: 'opt', 'data-opt': 'a', 'aria-pressed': 'false', _text: 'Option A' });
    const optB = new NodeEl('button', { class: 'opt rec', 'data-opt': 'b', 'aria-pressed': 'false', _text: 'Option B' });
    const holdBtn = new NodeEl('button', { class: 'btn mini wb-hold-toggle', 'data-wb-hold': '', 'aria-pressed': opts.held ? 'true' : 'false' });
    const card = new NodeEl('div', {
      class: 'card policy' + (opts.held ? ' wb-held' : ''),
      id: 'item-' + id,
      'data-id': String(id)
    }, [optA, optB, holdBtn]);
    return { card, optA, optB, holdBtn };
  }
  function makeCheck(id, opts = {}) {
    const input = new NodeEl('input', { type: 'checkbox', 'data-id': String(id) });
    const label = new NodeEl('label', { class: 'ck' }, [input]);
    const holdBtn = new NodeEl('button', { class: 'btn mini wb-hold-toggle', 'data-wb-hold': '', 'aria-pressed': opts.held ? 'true' : 'false' });
    const row = new NodeEl('div', {
      class: 'ckrow' + (opts.held ? ' wb-held' : ''),
      id: 'item-' + id,
      'data-id': String(id)
    }, [label, holdBtn]);
    return { row, input, holdBtn };
  }
  function makeAck(id, opts = {}) {
    const input = new NodeEl('input', { type: 'checkbox', 'data-id': String(id) });
    const label = new NodeEl('label', { class: 'ack' }, [input]);
    const holdBtn = new NodeEl('button', { class: 'btn mini wb-hold-toggle', 'data-wb-hold': '', 'aria-pressed': opts.held ? 'true' : 'false' });
    const row = new NodeEl('div', {
      class: 'arow' + (opts.held ? ' wb-held' : ''),
      id: 'item-' + id,
      'data-id': String(id)
    }, [label, holdBtn]);
    return { row, input, holdBtn };
  }
  function makeTopic(name, items, initial) {
    const openChip = new NodeEl('button', {
      type: 'button', class: 'chip policy bulk', 'data-bulk': 'policy', 'data-tbar-ask': 'open',
      _hidden: initial.open === 0
    }, [new NodeEl('span', { class: 'chip-lbl', _text: '미결' }), new NodeEl('span', { class: 'n', _text: String(initial.open) })]);
    const ansChip = new NodeEl('span', {
      class: 'chip wb-chip-ans', 'data-tbar-ask': 'ans', _hidden: true
    }, [new NodeEl('span', { class: 'chip-lbl', _text: '응답함' }), new NodeEl('span', { class: 'n', _text: '0' })]);
    const heldChip = new NodeEl('span', {
      class: 'chip wb-chip-held', 'data-tbar-ask': 'held', _hidden: initial.held === 0
    }, [new NodeEl('span', { class: 'chip-lbl', _text: '보류' }), new NodeEl('span', { class: 'n', _text: String(initial.held) })]);
    const ackChip = new NodeEl('button', {
      type: 'button', class: 'chip ok bulk', 'data-bulk': 'ack'
    }, [new NodeEl('span', { _text: String(initial.ack || 0) })]);
    const tbar = new NodeEl('div', { class: 'tbar' }, [
      new NodeEl('span', { class: 'tname', _text: name }),
      openChip, ansChip, heldChip, ackChip
    ]);
    const tophead = new NodeEl('div', { class: 'tophead' }, [tbar]);
    const tbody = new NodeEl('div', { class: 'tbody' }, items);
    const sec = new NodeEl('section', {
      class: 'topic', id: 'topic-' + name, 'data-topic': name, 'data-size': 'open',
      _rect: { top: 20, bottom: 112, width: 400, height: 92 }
    }, [tophead, tbody]);
    return { sec, openChip, ansChip, heldChip, ackChip };
  }

  const html = new NodeEl('html', { 'data-rid': 'sess-tbar' });
  const body = new NodeEl('body');
  html.appendChild(body);
  const grid = new NodeEl('main', { class: 'dgrid live' });
  const bcol = new NodeEl('div', { id: 'bcol' });
  const board = new NodeEl('div', { id: 'r-board', class: 'r-board' });
  const toolbar = new NodeEl('div', { class: 'board-toolbar', _rect: { top: -80, bottom: -40, height: 40 } }, [
    new NodeEl('button', { class: 'board-overview', _rect: { top: -80, bottom: -40, height: 40 } })
  ]);
  const bgal = new NodeEl('div', { id: 'bgal', class: 'bgal', _hidden: true }, [
    new NodeEl('div', { class: 'gal-viewport' }),
    new NodeEl('div', { class: 'gal-rail' })
  ]);

  const p1 = makePolicy(1);
  const p2 = makePolicy(2);
  const c3 = makeCheck(3);
  const a4 = makeAck(4);
  const topic = makeTopic('alpha', [p1.card, p2.card, c3.row, a4.row], { open: 3, held: 0, ack: 1 });
  board.appendChild(toolbar);
  board.appendChild(topic.sec);
  bcol.appendChild(board);
  grid.appendChild(bcol);
  body.appendChild(grid);
  body.appendChild(bgal);

  const frames = [];
  const doc = {
    documentElement: html,
    body,
    scrollingElement: html,
    activeElement: null,
    createElement: tag => new NodeEl(tag),
    getElementById: id => html.querySelector('#' + id),
    querySelector: sel => html.querySelector(sel),
    querySelectorAll: sel => html.querySelectorAll(sel),
    addEventListener: () => {}
  };
  const serverHeld = new Set();
  const win = {
    localStorage: makeThrowingStorage(),
    innerWidth: 1600,
    innerHeight: 900,
    scrollY: 0,
    matchMedia: () => ({ matches: true }),
    WsCards: { isFourCol: () => true, relayout: () => {} },
    IntersectionObserver: function () { this.observe = () => {}; this.disconnect = () => {}; },
    addEventListener: () => {},
    removeEventListener: () => {},
    fetch: async (url, init) => {
      const req = JSON.parse(init.body);
      if (req.held) serverHeld.add(req.id); else serverHeld.delete(req.id);
      return { ok: true, status: 200, json: async () => ({ held: Array.from(serverHeld) }) };
    }
  };
  const sessionStore = {};
  const ctx = vm.createContext({
    console,
    document: doc,
    window: win,
    IntersectionObserver: win.IntersectionObserver,
    sessionStorage: {
      getItem: k => (k in sessionStore ? sessionStore[k] : null),
      setItem: (k, v) => { sessionStore[k] = String(v); },
      removeItem: k => { delete sessionStore[k]; }
    },
    Set, Map, Array, Object, JSON, Math, String, Number, parseInt, encodeURIComponent,
    requestAnimationFrame: fn => { frames.push(fn); return frames.length; },
    cancelAnimationFrame: () => {},
    performance: { now: () => 0 },
    innerWidth: 1600,
    innerHeight: 900,
    setTimeout: () => 1,
    clearTimeout: () => {},
    getComputedStyle: () => ({ overflowY: 'visible', marginBottom: '6px', scrollPaddingTop: '0', getPropertyValue: () => '' }),
    wsT: k => k,
    Event: function Event(type) { this.type = type; }
  });
  const flush = () => { while (frames.length) frames.shift()(); };
  vm.runInContext(overviewSource, ctx);
  vm.runInContext('(function(){\n' + formSource + '\n})();', ctx);
  vm.runInContext(pillSource, ctx);
  flush();

  const readBar = rootEl => {
    const bar = rootEl.querySelector(':scope > .tophead > .tbar');
    const chip = sel => {
      const el = bar.querySelector(sel);
      if (!el || el.hidden) return 0;
      const n = el.querySelector('.n');
      return Number((n ? n.textContent : el.textContent).trim());
    };
    return {
      open: chip('.chip.policy'),
      ans: chip('.chip.wb-chip-ans'),
      held: chip('.chip.wb-chip-held'),
      ack: chip('.chip.ok')
    };
  };

  // Mount Overview and enter Quick Look preview so alpha moves into #bgal and leaves a placeholder in #r-board.
  win.WsOverview.mount({ board, toolbar, rid: 'sess-tbar', place: () => {}, keep: () => {} });
  win.WsOverview.toggle();
  const ph = board.querySelector('.overview-placeholder');
  assert.ok(ph, 'Overview placeholder created');
  assert.deepEqual(readBar(topic.sec), { open: 3, ans: 0, held: 0, ack: 1 });
  assert.deepEqual(readBar(ph), { open: 3, ans: 0, held: 0, ack: 1 });

  // 1. Hold policy #1 inside Quick Look stage (#bgal) -> open 2, ans 0, held 1 on BOTH live topic and placeholder.
  p1.holdBtn.dispatch('click');
  flush();
  await drainMicrotasks();
  flush();
  assert.deepEqual(readBar(topic.sec), { open: 2, ans: 0, held: 1, ack: 1 });
  assert.deepEqual(readBar(ph), { open: 2, ans: 0, held: 1, ack: 1 });

  // 2. Hold ack #4 -> ack does NOT affect policy+check open/ans/held split or .chip.ok.
  a4.holdBtn.dispatch('click');
  flush();
  await drainMicrotasks();
  flush();
  assert.equal(a4.row.classList.contains('wb-held'), true);
  assert.deepEqual(readBar(topic.sec), { open: 2, ans: 0, held: 1, ack: 1 });
  assert.deepEqual(readBar(ph), { open: 2, ans: 0, held: 1, ack: 1 });

  // 3. Bulk quick-pick on open-ask chip picks unheld policy #2 only (leaving held #1 alone) -> open 1 (check #3), ans 1 (#2), held 1 (#1).
  topic.openChip.dispatch('click');
  flush();
  assert.equal(p2.optB.getAttribute('aria-pressed'), 'true', 'unheld policy picked by bulk button');
  assert.equal(p1.optB.getAttribute('aria-pressed'), 'false', 'held policy skipped by bulk button');
  assert.deepEqual(readBar(topic.sec), { open: 1, ans: 1, held: 1, ack: 1 });
  assert.deepEqual(readBar(ph), { open: 1, ans: 1, held: 1, ack: 1 });

  // 4. Stage check #3 -> open 0 (hidden), ans 2, held 1.
  c3.input.checked = true;
  c3.input.dispatch('change');
  flush();
  assert.deepEqual(readBar(topic.sec), { open: 0, ans: 2, held: 1, ack: 1 });
  assert.deepEqual(readBar(ph), { open: 0, ans: 2, held: 1, ack: 1 });

  // 5. Unhold policy #1 -> open 1, ans 2, held 0.
  p1.holdBtn.dispatch('click');
  flush();
  await drainMicrotasks();
  flush();
  assert.deepEqual(readBar(topic.sec), { open: 1, ans: 2, held: 0, ack: 1 });
  assert.deepEqual(readBar(ph), { open: 1, ans: 2, held: 0, ack: 1 });

  // 6. Mark policy #1 sent-sent -> open 0, ans 3, held 0.
  p1.card.classList.add('sent-sent');
  win.wbPill.update();
  flush();
  assert.deepEqual(readBar(topic.sec), { open: 0, ans: 3, held: 0, ack: 1 });
  assert.deepEqual(readBar(ph), { open: 0, ans: 3, held: 0, ack: 1 });

  // 7. Server settles all asks and SSE replaces the preview node -> placeholder clears ask chips cleanly.
  const settledTopic = new NodeEl('section', {
    class: 'topic quiet', id: 'topic-alpha', 'data-topic': 'alpha', 'data-size': 'open'
  }, [
    new NodeEl('div', { class: 'tophead' }, [
      new NodeEl('div', { class: 'tbar' }, [new NodeEl('span', { class: 'tname', _text: 'alpha' })])
    ])
  ]);
  topic.sec.parentNode.insertBefore(settledTopic, topic.sec);
  topic.sec.parentNode.removeChild(topic.sec);
  win.WsOverview.replacePreviewNode(topic.sec, settledTopic);
  assert.deepEqual(readBar(ph), { open: 0, ans: 0, held: 0, ack: 0 });
  assert.equal(ph.classList.contains('quiet'), true);
});

test('in-card find resolves cards only when focus or pointer is inside a non-terminal card and wraps step index', () => {
  const listeners = {};
  const card = {
    nodeType: 1, isConnected: true, tagName: 'DETAILS',
    getClientRects: () => [{ width: 200, height: 100 }],
    closest: sel => (sel === 'details.entry-panel[open]:not(.msg)' ? card : null)
  };
  const insideCard = {
    nodeType: 1, isConnected: true,
    closest: sel => (sel === 'details.entry-panel[open]:not(.msg)' ? card : null)
  };
  const outsideChat = {
    nodeType: 1, isConnected: true,
    closest: () => null
  };
  const inTerminal = {
    nodeType: 1, isConnected: true,
    closest: sel => (sel.includes('.xterm') ? inTerminal : null)
  };
  const doc = {
    activeElement: outsideChat,
    body: outsideChat,
    querySelectorAll: () => [],
    addEventListener: (t, fn) => { listeners[t] = fn; },
    elementFromPoint: (x, y) => (x > 500 ? insideCard : outsideChat)
  };
  const win = { addEventListener: () => {}, getComputedStyle: () => ({ visibility: 'visible' }) };
  const ctx = vm.createContext({ document: doc, window: win, getComputedStyle: win.getComputedStyle, Set, Array });
  vm.runInContext(cardFindSource, ctx);
  const api = win.wbCardFind;

  // Outside card (focus + pointer in chat) -> null (native browser find untouched).
  assert.equal(api.resolveTarget(outsideChat, outsideChat, { x: 100, y: 100 }, null, doc), null);
  // Pointer inside card -> resolves card even when focus is outside.
  assert.equal(api.resolveTarget(outsideChat, outsideChat, { x: 600, y: 100 }, null, doc), card);
  // Focus inside card -> resolves card even when pointer is null or elsewhere.
  assert.equal(api.resolveTarget(insideCard, insideCard, { x: 100, y: 100 }, null, doc), card);
  // Focus inside terminal -> never intercepted.
  assert.equal(api.resolveTarget(inTerminal, inTerminal, { x: 600, y: 100 }, null, doc), null);

  // Step wrap-around.
  assert.equal(api.stepIndex(-1, 3, 1), 0);
  assert.equal(api.stepIndex(0, 3, 1), 1);
  assert.equal(api.stepIndex(2, 3, 1), 0);
  assert.equal(api.stepIndex(0, 3, -1), 2);
  assert.equal(api.stepIndex(0, 0, 1), -1);
});

test('in-card find keeps a frame as one capped index segment whatever count it reports', () => {
  const doc = { body: {}, querySelectorAll: () => [], addEventListener: () => {} };
  const win = { addEventListener: () => {}, getComputedStyle: () => ({ visibility: 'visible' }) };
  const ctx = vm.createContext({ document: doc, window: win, getComputedStyle: win.getComputedStyle, Set, Array });
  vm.runInContext(cardFindSource, ctx);
  const api = win.wbCardFind;
  const frame = { isConnected: true };
  api.state.card = { isConnected: true };
  api.state.query = 'x';
  api.state.seq = 7;
  api.state.frames = [{ el: frame, count: 0 }];
  const started = Date.now();
  api.onFrameResult(frame, 7, 2147483647);
  assert.ok(Date.now() - started < 1000, 'a huge reported count does no per-match host work');
  assert.equal(api.state.items.length, 1);
  assert.equal(api.state.total, 100000);
  assert.equal(api.state.index, 0);
  assert.deepEqual({ ...api.itemAt(99999) }, { kind: 'frame', frame, local: 99999 });
  assert.equal(api.itemAt(100000), null);
  api.step(-1);
  assert.equal(api.state.index, 99999);
  api.onFrameResult(frame, 6, 5); // stale search
  assert.equal(api.state.total, 100000);
});
