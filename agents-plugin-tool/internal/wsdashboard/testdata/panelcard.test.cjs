// Run locally: node agents-plugin-tool/internal/wsdashboard/testdata/panelcard.test.cjs
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const gallerySrc = fs.readFileSync(path.join(__dirname, '../assets/gallery.js'), 'utf8');
const cardsSrc = fs.readFileSync(path.join(__dirname, '../assets/cards.js'), 'utf8');
const panelSrc = fs.readFileSync(path.join(__dirname, '../assets/panelcard.js'), 'utf8');
const framesSrc = fs.readFileSync(path.join(__dirname, '../assets/dashboard-frames.js'), 'utf8');
const boardCssSrc = fs.readFileSync(path.join(__dirname, '../assets/dashboard-board.css'), 'utf8');

function makeClassList(el) {
  const list = () => (el.attrs.class || '').split(/\s+/).filter(Boolean);
  const set = arr => { el.attrs.class = arr.join(' '); };
  return {
    contains: c => list().includes(c),
    add(...cs) { const a = list(); for (const c of cs) if (c && !a.includes(c)) a.push(c); set(a); },
    remove(...cs) { set(list().filter(x => !cs.includes(x))); },
    toggle(c, on) { const has = list().includes(c); const next = on === undefined ? !has : !!on; if (next) this.add(c); else this.remove(c); return next; },
  };
}

function matchSimple(el, sel) {
  if (!el || el.nodeType !== 1) return false;
  sel = sel.trim();
  if (!sel) return false;
  if (sel.startsWith(':scope')) sel = sel.slice(6).trim();
  if (sel.includes(',')) return sel.split(',').some(s => matchSimple(el, s));
  // Handle :not(...) clauses
  const nots = [];
  let base = sel.replace(/:not\(([^)]+)\)/g, (_, inner) => { nots.push(inner.trim()); return ''; });
  for (const n of nots) if (matchSimple(el, n)) return false;
  // Attribute selectors
  const attrRe = /\[([a-zA-Z0-9_-]+)(?:=(?:"([^"]*)"|'([^']*)'|([^\]]*)))?\]/g;
  let m;
  while ((m = attrRe.exec(base))) {
    const [, name, v1, v2, v3] = m;
    const expected = v1 !== undefined ? v1 : v2 !== undefined ? v2 : v3;
    if (!(name in el.attrs)) return false;
    if (expected !== undefined && el.attrs[name] !== expected) return false;
  }
  base = base.replace(attrRe, '');
  const parts = base.split(/(?=[.#])/).filter(Boolean);
  for (const p of parts) {
    if (p.startsWith('.')) {
      if (!el.classList.contains(p.slice(1))) return false;
    } else if (p.startsWith('#')) {
      if (el.attrs.id !== p.slice(1)) return false;
    } else {
      if (el.tagName !== p.toUpperCase()) return false;
    }
  }
  return true;
}

function matchChain(el, selector, root) {
  if (!el || el.nodeType !== 1) return false;
  if (selector.includes(',')) return selector.split(',').some(s => matchChain(el, s.trim(), root));
  // Split combinators (> or space) outside brackets/parens
  const tokens = [];
  let cur = '', depth = 0;
  for (let i = 0; i < selector.length; i++) {
    const ch = selector[i];
    if (ch === '[' || ch === '(') depth++;
    else if (ch === ']' || ch === ')') depth--;
    if (depth === 0 && (ch === '>' || /\s/.test(ch))) {
      if (cur) { tokens.push(cur); cur = ''; }
      if (ch === '>') tokens.push('>');
      continue;
    }
    cur += ch;
  }
  if (cur) tokens.push(cur);
  // Normalize space combinators
  const steps = [];
  for (let i = 0; i < tokens.length; i++) {
    if (tokens[i] === '>') {
      if (steps.length && steps[steps.length - 1] === ' ') steps.pop();
      steps.push('>');
    } else {
      if (steps.length && steps[steps.length - 1] !== '>') steps.push(' ');
      steps.push(tokens[i]);
    }
  }
  function check(node, idx) {
    const tok = steps[idx];
    if (tok === ':scope') return node === root;
    if (!matchSimple(node, tok)) return false;
    if (idx === 0) return true;
    const comb = steps[idx - 1];
    if (comb === '>') return !!node.parentNode && check(node.parentNode, idx - 2);
    for (let p = node.parentNode; p; p = p.parentNode) {
      if (check(p, idx - 2)) return true;
    }
    return false;
  }
  return check(el, steps.length - 1);
}

// Tiny HTML parser sufficient for topic/panel markup used in wbHistoryFetch tests.
function parseHTML(html, createElement) {
  const root = createElement('div');
  const stack = [root];
  const tagRe = /<!--[\s\S]*?-->|<\/([a-zA-Z0-9-]+)\s*>|<([a-zA-Z0-9-]+)([^>]*?)(\/?)>|([^<]+)/g;
  let m;
  while ((m = tagRe.exec(html))) {
    if (m[1]) {
      const end = m[1].toUpperCase();
      while (stack.length > 1 && stack[stack.length - 1].tagName !== end) stack.pop();
      if (stack.length > 1) stack.pop();
    } else if (m[2]) {
      const el = createElement(m[2]);
      const attrStr = m[3] || '';
      const aRe = /([a-zA-Z0-9_-]+)(?:=(?:"([^"]*)"|'([^']*)'|([^\s"'>]+)))?/g;
      let am;
      while ((am = aRe.exec(attrStr))) {
        const val = am[2] !== undefined ? am[2] : am[3] !== undefined ? am[3] : am[4] !== undefined ? am[4] : '';
        el.setAttribute(am[1], val.replace(/&quot;/g, '"').replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&'));
      }
      stack[stack.length - 1].appendChild(el);
      if (!m[4]) stack.push(el);
    } else if (m[5]) {
      const txt = m[5];
      if (txt) stack[stack.length - 1]._text += txt;
    }
  }
  return root.children.slice();
}

function createDOM() {
  const docListeners = {};
  function makeEl(tag) {
    const el = {
      nodeType: 1,
      tagName: tag.toUpperCase(),
      attrs: {},
      children: [],
      parentNode: null,
      style: {
        setProperty(k, v) { this[k] = v; },
        removeProperty(k) { delete this[k]; },
      },
      hidden: false,
      inert: false,
      tabIndex: 0,
      listeners: {},
      _text: '',
      get childNodes() { return this.children; },
      get firstChild() { return this.children[0] || null; },
      get firstElementChild() { return this.children[0] || null; },
      get nextSibling() {
        if (!this.parentNode) return null;
        const i = this.parentNode.children.indexOf(this);
        return i >= 0 ? this.parentNode.children[i + 1] || null : null;
      },
      get nextElementSibling() { return this.nextSibling; },
      get isConnected() {
        for (let p = this; p; p = p.parentNode) if (p === document.documentElement || p === document.body) return true;
        return false;
      },
      get className() { return this.attrs.class || ''; },
      set className(v) { this.attrs.class = String(v); },
      get id() { return this.attrs.id || ''; },
      set id(v) { this.attrs.id = String(v); },
      get title() { return this.attrs.title || ''; },
      set title(v) { this.attrs.title = String(v); },
      get attributes() { return Object.entries(this.attrs).map(([name, value]) => ({ name, value })); },
      get textContent() { return this._text + this.children.map(c => c.textContent).join(''); },
      set textContent(v) {
        for (const c of this.children) c.parentNode = null;
        this.children = [];
        this._text = String(v);
      },
      get innerHTML() { return this._innerHTML || ''; },
      set innerHTML(v) {
        this._innerHTML = String(v);
        for (const c of this.children) c.parentNode = null;
        this.children = [];
        this._text = '';
        for (const node of parseHTML(this._innerHTML, makeEl)) this.appendChild(node);
      },
      setAttribute(k, v) { this.attrs[k] = String(v); },
      getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; },
      hasAttribute(k) { return k in this.attrs; },
      removeAttribute(k) { delete this.attrs[k]; },
      appendChild(c) {
        if (c.parentNode) c.parentNode.removeChild(c);
        this.children.push(c);
        c.parentNode = this;
        return c;
      },
      insertBefore(c, ref) {
        if (c.parentNode) c.parentNode.removeChild(c);
        const i = ref ? this.children.indexOf(ref) : -1;
        if (i < 0) this.children.push(c);
        else this.children.splice(i, 0, c);
        c.parentNode = this;
        return c;
      },
      removeChild(c) {
        const i = this.children.indexOf(c);
        if (i >= 0) this.children.splice(i, 1);
        c.parentNode = null;
        return c;
      },
      replaceChild(n, old) {
        const i = this.children.indexOf(old);
        if (i < 0) return old;
        if (n.parentNode) n.parentNode.removeChild(n);
        this.children[i] = n;
        n.parentNode = this;
        old.parentNode = null;
        return old;
      },
      remove() { if (this.parentNode) this.parentNode.removeChild(this); },
      contains(x) { return this === x || this.children.some(c => c.contains(x)); },
      matches(sel) { return matchChain(this, sel, this); },
      closest(sel) {
        for (let p = this; p; p = p.parentNode) if (p.nodeType === 1 && matchChain(p, sel, p)) return p;
        return null;
      },
      querySelectorAll(sel) {
        const out = [];
        const walk = n => {
          for (const c of n.children) {
            if (matchChain(c, sel, this)) out.push(c);
            walk(c);
          }
        };
        walk(this);
        return out;
      },
      querySelector(sel) { return this.querySelectorAll(sel)[0] || null; },
      addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); },
      removeEventListener(type, fn) { this.listeners[type] = (this.listeners[type] || []).filter(x => x !== fn); },
      dispatchEvent(ev) {
        ev.target = ev.target || this;
        ev.defaultPrevented = false;
        ev.preventDefault = () => { ev.defaultPrevented = true; };
        ev.stopped = false;
        ev.stopPropagation = () => { ev.stopped = true; };
        for (let p = this; p; p = p.parentNode) {
          for (const fn of (p.listeners && p.listeners[ev.type]) || []) fn(ev);
          if (ev.stopped) break;
        }
        return !ev.defaultPrevented;
      },
      getBoundingClientRect() { return { left: 0, top: 0, right: 600, bottom: 400, width: 600, height: 400 }; },
      getClientRects() { return [this.getBoundingClientRect()]; },
      focus() {},
      scrollIntoView() {},
    };
    el.classList = makeClassList(el);
    return el;
  }

  const documentElement = makeEl('html');
  const body = makeEl('body');
  documentElement.appendChild(body);
  const document = {
    documentElement,
    body,
    createElement: makeEl,
    createElementNS: (_, tag) => makeEl(tag),
    getElementById(id) { return documentElement.querySelector('#' + id); },
    querySelector(sel) { return documentElement.querySelector(sel); },
    querySelectorAll(sel) { return documentElement.querySelectorAll(sel); },
    addEventListener(type, fn) { (docListeners[type] ||= []).push(fn); },
    removeEventListener(type, fn) { docListeners[type] = (docListeners[type] || []).filter(x => x !== fn); },
  };
  return { document, makeEl };
}

function makePanelHTML({ topic = 'alpha', topicTitle = 'Alpha Topic', panelId = 'p1', entryId = 'e1', recap = 'Design Recap', ver = 'v1', hasBody = true, historical = false, srcdoc = '<p>hello</p>', mockupSrc = '', mockupSha = '', fallbackText = '', omitDataMockupSrcOnMounted = false, mounted = false }) {
  const esc = s => String(s).replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  let frameTag = '';
  if (mockupSrc) {
    const shaAttr = mockupSha ? ` data-mockup-sha="${esc(mockupSha)}"` : '';
    const fbAttr = fallbackText ? ` data-fallback-text="${esc(fallbackText)}"` : '';
    const dataSrcAttr = (mounted && omitDataMockupSrcOnMounted) ? '' : ` data-mockup-src="${esc(mockupSrc)}"`;
    const innerTag = mounted
      ? `<iframe class="doc-frame mockup-frame" data-doc="entry:${entryId}" data-doc-key="entry:${entryId}" data-topic="${topic}" data-entry="${entryId}" data-relay="1" sandbox="allow-scripts" src="${esc(mockupSrc)}"${dataSrcAttr}${shaAttr}${fbAttr} title="${topic}/${entryId}"></iframe>`
      : `<div class="doc-frame mockup-frame" data-doc="entry:${entryId}" data-doc-key="entry:${entryId}" data-topic="${topic}" data-entry="${entryId}" data-relay="1" data-sandbox="allow-scripts"${dataSrcAttr}${shaAttr}${fbAttr} title="${topic}/${entryId}"></div>`;
    frameTag = `<div class="mockup-wrap">${innerTag}</div>`;
  } else {
    frameTag = mounted
      ? `<iframe class="doc-frame" data-doc="${topic}:${entryId}" data-doc-key="${topic}:${panelId}" data-topic="${topic}" data-entry="${entryId}" data-relay="1" sandbox="allow-scripts" data-srcdoc="${esc(srcdoc)}" title="Doc ${panelId}"></iframe>`
      : `<div class="doc-frame" data-doc="${topic}:${entryId}" data-doc-key="${topic}:${panelId}" data-topic="${topic}" data-entry="${entryId}" data-relay="1" data-sandbox="allow-scripts" data-srcdoc="${esc(srcdoc)}" title="Doc ${panelId}"></div>`;
  }
  const maxBtns = hasBody ? `<button type="button" class="ibtn d-max"></button><button type="button" class="ibtn d-min"></button><span class="pchev"></span>` : '';
  const bodyHTML = hasBody ? `<div class="doc-body entry-body">${frameTag}</div>` : '';
  return `<article class="wb-entry panel${historical ? ' historical' : ''}" id="${entryId}" data-entry="${entryId}" data-kind="panel" data-panel="${panelId}">`
    + `<details class="entry-panel${hasBody ? '' : ' msg'}" data-key="entry:${entryId}"${historical ? '' : ` id="${panelId}"`} open>`
    + `<summary><span class="entry-recap">${recap}</span><span class="pmeta"><span class="pfver">${ver}</span></span>${maxBtns}</summary>`
    + `${bodyHTML}</details></article>`;
}

function makeTopicEl(makeEl, topic, title, articlesHTML) {
  const sec = makeEl('section');
  sec.className = 'topic';
  sec.setAttribute('data-topic', topic);
  sec.setAttribute('data-at', '100');
  sec.innerHTML = `<div class="tophead"><div class="tbar"><span class="tname">${title}</span><button type="button" class="tnote-toggle"></button></div></div>`
    + `<div class="topic-stream">${articlesHTML}</div>`;
  return sec;
}

function boot({ initialHTML = [], stored = null, historyFetch = null } = {}) {
  const { document, makeEl } = createDOM();
  const board = makeEl('div');
  board.id = 'r-board';
  board.className = 'r-board';
  document.body.appendChild(board);
  for (const sec of initialHTML) board.appendChild(sec);

  const storage = {};
  if (stored) storage['ws.dashboard.cards.test'] = JSON.stringify(stored);
  const mountCalls = [];
  const jumps = [];

  const ctx = {
    document,
    console,
    Math, JSON, Array, Object, String, Number, Date, RegExp, Promise, Map, Set, WeakMap, Error,
    encodeURIComponent, decodeURIComponent, parseInt, parseFloat, isNaN, isFinite,
    setTimeout: fn => { fn(); return 1; },
    clearTimeout: () => {},
    requestAnimationFrame: fn => { fn(); return 1; },
    cancelAnimationFrame: () => {},
    queueMicrotask: fn => fn(),
    wsT: k => k,
    wbBoardJump: id => jumps.push(id),
    wbHistoryFetch: historyFetch,
    getComputedStyle: () => ({ overflowY: 'auto' }),
    addEventListener: () => {},
    removeEventListener: () => {},
    matchMedia: () => ({ matches: false }),
    innerWidth: 1440,
    innerHeight: 900,
  };
  ctx.window = ctx;
  vm.createContext(ctx);
  vm.runInContext(gallerySrc, ctx);
  vm.runInContext(cardsSrc, ctx);
  vm.runInContext(panelSrc, ctx);

  ctx.WsPanelCard.setup({
    mountFrames: el => {
      mountCalls.push(el);
      // Simulate dashboard-frames.js mountFrame turning div.doc-frame into iframe.doc-frame
      for (const p of el.querySelectorAll('div.doc-frame[data-srcdoc], div.doc-frame[data-mockup-src]')) {
        const f = document.createElement('iframe');
        for (const a of p.attributes) if (a.name !== 'data-sandbox') f.setAttribute(a.name, a.value);
        f.setAttribute('sandbox', p.getAttribute('data-sandbox') || '');
        if (p.hasAttribute('data-mockup-src')) {
          f.setAttribute('src', p.getAttribute('data-mockup-src') || '');
        } else {
          f.setAttribute('srcdoc', p.getAttribute('data-srcdoc') || '');
        }
        p.parentNode.replaceChild(f, p);
      }
    },
    boardBoxes: () => [board, ctx.WsCards.gallery()].filter(Boolean),
  });

  ctx.WsCards.mount(board, {
    key: 'ws.dashboard.cards.test',
    load: k => storage[k] || null,
    save: (k, v) => { storage[k] = v; },
    host: document.body,
    mode: () => 'col',
  });
  ctx.WsPanelCard.sync();

  return { ctx, document, makeEl, board, storage, mountCalls, jumps };
}

test('.ppin injects only on current HasBody panels and toggles pin without toggling details', () => {
  const { makeEl } = createDOM();
  const topic = makeTopicEl(makeEl, 'alpha', 'Alpha', [
    makePanelHTML({ panelId: 'p1', entryId: 'e1', hasBody: true, historical: false }),
    makePanelHTML({ panelId: 'p1', entryId: 'e0', hasBody: true, historical: true }),
    makePanelHTML({ panelId: 'p2', entryId: 'e2', hasBody: false, historical: false }),
  ].join(''));
  const { ctx, board } = boot({ initialHTML: [topic] });

  const currentArt = board.querySelector('#e1');
  const histArt = board.querySelector('#e0');
  const msgArt = board.querySelector('#e2');

  const ppin = currentArt.querySelector('details.entry-panel > summary > .ppin');
  assert.ok(ppin, 'current HasBody panel gets .ppin');
  const summary = currentArt.querySelector('details.entry-panel > summary');
  const dmax = summary.querySelector('.d-max');
  assert.equal(summary.children.indexOf(ppin) + 1, summary.children.indexOf(dmax), '.ppin is placed right before .d-max');
  assert.equal(ppin.getAttribute('aria-pressed'), 'false');

  assert.equal(histArt.querySelector('.ppin'), null, 'historical panel does NOT get .ppin');
  assert.equal(msgArt.querySelector('.ppin'), null, 'recap-only (.msg) panel does NOT get .ppin');

  // Click .ppin -> prevents default & stops propagation, pins card in gallery, no duplicates
  const ev = { type: 'click', target: ppin };
  ppin.dispatchEvent(ev);
  assert.equal(ev.defaultPrevented, true, 'clicking .ppin prevents details toggle');
  assert.equal(ev.stopped, true, 'clicking .ppin stops propagation');
  assert.equal(ctx.WsCards.list('panel').length, 1);
  assert.equal(ctx.WsCards.list('panel')[0].pinned, 1);
  assert.equal(ppin.getAttribute('aria-pressed'), 'true');
  assert.equal(board.querySelector('.bcard[data-kind="panel"]'), null, 'panel card is in gallery, not #r-board');
  assert.ok(ctx.WsCards.gallery().querySelector('.bcard[data-kind="panel"]'), 'panel card is mounted in #bgal');

  // Calling toggle again closes it (no duplicate cards)
  ppin.dispatchEvent({ type: 'click', target: ppin });
  assert.equal(ctx.WsCards.list('panel').length, 0);
  assert.equal(ppin.getAttribute('aria-pressed'), 'false');
});

test('unpinning via .bc-pin or undock closes panel card instead of moving to #bcol', () => {
  const { makeEl } = createDOM();
  const topic = makeTopicEl(makeEl, 'alpha', 'Alpha', makePanelHTML({ panelId: 'p3', entryId: 'e3', hasBody: true }));
  const { ctx, board } = boot({ initialHTML: [topic] });
  const ppin = board.querySelector('.ppin');

  // Pin via .ppin
  ppin.dispatchEvent({ type: 'click', target: ppin });
  let cards = ctx.WsCards.list('panel');
  assert.equal(cards.length, 1);
  const card = cards[0];

  // Unpin via .bc-pin button -> card closes instead of dropping into #r-board
  card.pinBtn.dispatchEvent({ type: 'click', target: card.pinBtn });
  assert.equal(ctx.WsCards.list('panel').length, 0);
  assert.equal(board.querySelector('.bcard[data-kind="panel"]'), null);
  assert.equal(ppin.getAttribute('aria-pressed'), 'false');

  // Pin again, then call card.pin(false) directly (same path as undock) -> closes cleanly
  ppin.dispatchEvent({ type: 'click', target: ppin });
  assert.equal(ctx.WsCards.list('panel').length, 1);
  ctx.WsCards.list('panel')[0].pin(false);
  assert.equal(ctx.WsCards.list('panel').length, 0);
  assert.equal(board.querySelector('.bcard[data-kind="panel"]'), null);
});

test('live sync mirrors safe attributes without id duplication and skips reload when unchanged', () => {
  const { makeEl } = createDOM();
  const topic = makeTopicEl(makeEl, 'alpha', 'Alpha Topic', makePanelHTML({
    panelId: 'p5', entryId: 'e5', recap: 'Initial Recap', ver: 'v1', srcdoc: '<p>v1</p>', mounted: true,
  }));
  const { ctx, board, mountCalls, jumps } = boot({ initialHTML: [topic] });

  const ppin = board.querySelector('.ppin');
  ppin.dispatchEvent({ type: 'click', target: ppin });
  const card = ctx.WsCards.list('panel')[0];
  assert.ok(card);
  assert.equal(mountCalls.length, 1, 'initial pin mounts iframe once');

  const iframe = card.body.querySelector('iframe.doc-frame');
  assert.ok(iframe, 'iframe mounted in card.body');
  assert.equal(iframe.getAttribute('sandbox'), 'allow-scripts', 'copies sandbox from already-mounted iframe');
  assert.equal(iframe.getAttribute('srcdoc'), '<p>v1</p>');
  assert.equal(iframe.getAttribute('data-entry'), 'e5');
  assert.equal(card.el.querySelector('#p5'), null, 'never copies id="p5" into card DOM');
  assert.equal(card.el.querySelector('#e5'), null, 'never copies id="e5" into card DOM');
  assert.equal(card.name.textContent, 'Initial Recap');
  assert.equal(card.badge.textContent, 'v1');

  // Jump button triggers wbBoardJump('p5')
  const jumpBtn = card.el.querySelector('.bc-jump');
  assert.ok(jumpBtn);
  jumpBtn.dispatchEvent({ type: 'click', target: jumpBtn });
  assert.deepEqual(jumps, ['p5']);

  // Calling sync() with unchanged data-entry & data-srcdoc does NOT reload iframe
  ctx.WsPanelCard.sync();
  assert.equal(mountCalls.length, 1, 'unchanged sync skips iframe reload');
  assert.equal(card.body.querySelector('iframe.doc-frame'), iframe, 'same iframe node kept');

  // Newer entry arrives in board DOM -> sync updates header and replaces iframe
  const stream = board.querySelector('.topic-stream');
  stream.innerHTML = makePanelHTML({
    panelId: 'p5', entryId: 'e9', recap: 'Updated Recap', ver: 'v2', srcdoc: '<p>v2</p>', mounted: false,
  });
  ctx.WsPanelCard.sync();
  assert.equal(mountCalls.length, 2, 'changed entry triggers single iframe mount');
  const nextIframe = card.body.querySelector('iframe.doc-frame');
  assert.notEqual(nextIframe, iframe);
  assert.equal(nextIframe.getAttribute('srcdoc'), '<p>v2</p>');
  assert.equal(nextIframe.getAttribute('data-entry'), 'e9');
  assert.equal(card.name.textContent, 'Updated Recap');
  assert.equal(card.badge.textContent, 'v2');
});

test('fallback hydration via wbHistoryFetch when panel is absent from live DOM', async () => {
  const fetched = [];
  const historyFetch = async query => {
    fetched.push(query);
    if (query === 'target=p7') {
      return [{
        name: 'closed-topic',
        html: `<section class="topic closed" data-topic="closed-topic"><div class="tophead"><div class="tbar"><span class="tname">Closed Topic</span></div></div>`
          + makePanelHTML({ panelId: 'p7', entryId: 'e7', recap: 'Hydrated Panel', ver: 'v4', srcdoc: '<p>remote</p>' })
          + `</section>`,
      }];
    }
    return [];
  };
  const stored = {
    v: 3,
    cards: [{ id: 'c1', kind: 'panel', data: { panel: 'p7', topic: 'closed-topic' }, pinned: 1, h: 0, fold: false, at: 1 }],
    gallery: { v: 1, pages: [{ id: 'g1', root: { card: 'c1' } }], active: 0, rev: 1 },
    topics: [],
  };
  const { ctx } = boot({ initialHTML: [], stored, historyFetch });
  await new Promise(r => setImmediate(r));

  assert.deepEqual(fetched, ['target=p7']);
  const cards = ctx.WsCards.list('panel');
  assert.equal(cards.length, 1);
  const card = cards[0];
  const iframe = card.body.querySelector('iframe.doc-frame');
  assert.ok(iframe, 'hydrated iframe mounted');
  assert.equal(iframe.getAttribute('srcdoc'), '<p>remote</p>');
  assert.equal(card.name.textContent, 'Hydrated Panel');
  assert.equal(card.badge.textContent, 'v4');
  assert.equal(card.el.querySelector('#p7'), null);
  assert.equal(card.el.querySelector('#e7'), null);

  // Missing panel shows graceful notice without throwing
  const missing = ctx.WsPanelCard.toggle('p999', 'gone');
  await new Promise(r => setImmediate(r));
  assert.ok(fetched.includes('target=p999'));
  assert.ok(missing.body.querySelector('.panel-card-empty'), 'shows unavailable notice for missing panel');
});

test('dashboard-frames.js frameHeights/setFrameHeights isolate .bcard[data-kind="panel"] iframes', () => {
  const start = framesSrc.indexOf('  function frameHeights(el)');
  assert.ok(start >= 0, 'frameHeights found in dashboard-frames.js');
  const snippet = framesSrc.slice(start);
  const { document, makeEl } = createDOM();
  const root = makeEl('div');
  const boardFrame = makeEl('iframe');
  boardFrame.className = 'doc-frame';
  boardFrame.setAttribute('data-doc', 'alpha:e1');
  boardFrame.style.height = '320px';

  const panelCard = makeEl('article');
  panelCard.className = 'bcard';
  panelCard.setAttribute('data-kind', 'panel');
  const cardFrame = makeEl('iframe');
  cardFrame.className = 'doc-frame';
  cardFrame.setAttribute('data-doc', 'alpha:e1');
  cardFrame.style.height = '580px';
  panelCard.appendChild(cardFrame);

  root.appendChild(boardFrame);
  root.appendChild(panelCard);

  const ctx = {
    q: (sel, el) => (el || document).querySelectorAll(sel),
    Object,
  };
  vm.createContext(ctx);
  vm.runInContext(snippet, ctx);

  // frameHeights reads boardFrame (320px), ignores panelCard's cardFrame (580px)
  const h = ctx.frameHeights(root);
  assert.equal(h['alpha:e1'], '320px');

  // setFrameHeights updates boardFrame, leaves cardFrame untouched
  boardFrame.style.height = '';
  ctx.setFrameHeights(root, { 'alpha:e1': '410px' });
  assert.equal(boardFrame.style.height, '410px');
  assert.equal(cardFrame.style.height, '580px', 'panel card iframe height is never overwritten by board height');
});

test('pinning unmounted and mounted full-HTML mockup panels preserves .mockup-wrap, classes, attributes, and invokes mountFrames', () => {
  const { makeEl } = createDOM();
  const topic = makeTopicEl(makeEl, 'alpha', 'Alpha Topic', [
    makePanelHTML({
      panelId: 'p10',
      entryId: 'e10',
      recap: 'Mockup Placeholder',
      ver: 'v1',
      mockupSrc: '/s/rid/board/mockup?entry=e10',
      mockupSha: 'sha-unmounted',
      fallbackText: 'Fallback description 10',
      mounted: false,
    }),
    makePanelHTML({
      panelId: 'p11',
      entryId: 'e11',
      recap: 'Mockup Mounted',
      ver: 'v2',
      mockupSrc: '/s/rid/board/mockup?entry=e11',
      mockupSha: 'sha-mounted',
      fallbackText: 'Fallback description 11',
      mounted: true,
    }),
    makePanelHTML({
      panelId: 'p12',
      entryId: 'e12',
      recap: 'Mockup Mounted SrcOnly',
      ver: 'v1',
      mockupSrc: '/s/rid/board/mockup?entry=e12',
      mockupSha: 'sha-srconly',
      fallbackText: 'Fallback description 12',
      omitDataMockupSrcOnMounted: true,
      mounted: true,
    }),
  ].join(''));
  const { ctx, board, mountCalls } = boot({ initialHTML: [topic] });

  // 1. Unmounted div.mockup-wrap > div.doc-frame.mockup-frame
  const ppin10 = board.querySelector('#e10 .ppin');
  ppin10.dispatchEvent({ type: 'click', target: ppin10 });
  assert.equal(mountCalls.length, 1, 'pinning unmounted mockup panel invokes hooks.mountFrames(card.body)');
  const card10 = ctx.WsCards.list('panel').find(c => c.data.panel === 'p10');
  assert.ok(card10);
  assert.equal(mountCalls[0], card10.body);
  const doc10 = card10.body.querySelector('.doc-body.panel-card-doc');
  assert.ok(doc10);
  const mwrap10 = doc10.querySelector('.mockup-wrap');
  assert.ok(mwrap10, 'pinned mockup placeholder is wrapped in .mockup-wrap');
  const iframe10 = mwrap10.querySelector('iframe.doc-frame.mockup-frame');
  assert.ok(iframe10, 'pinned mockup iframe keeps doc-frame and mockup-frame classes');
  assert.equal(iframe10.getAttribute('src'), '/s/rid/board/mockup?entry=e10');
  assert.equal(iframe10.getAttribute('data-mockup-src'), '/s/rid/board/mockup?entry=e10');
  assert.equal(iframe10.getAttribute('data-mockup-sha'), 'sha-unmounted');
  assert.equal(iframe10.getAttribute('data-fallback-text'), 'Fallback description 10');
  assert.equal(iframe10.getAttribute('sandbox'), 'allow-scripts');
  assert.equal(iframe10.hasAttribute('srcdoc'), false);
  assert.equal(card10.el.querySelector('#p10'), null);
  assert.equal(card10.el.querySelector('#e10'), null);

  // 2. Already-mounted iframe.doc-frame.mockup-frame
  const ppin11 = board.querySelector('#e11 .ppin');
  ppin11.dispatchEvent({ type: 'click', target: ppin11 });
  assert.equal(mountCalls.length, 2, 'pinning mounted mockup panel invokes hooks.mountFrames(card.body)');
  const card11 = ctx.WsCards.list('panel').find(c => c.data.panel === 'p11');
  assert.ok(card11);
  assert.equal(mountCalls[1], card11.body);
  const iframe11 = card11.body.querySelector('.doc-body.panel-card-doc > .mockup-wrap > iframe.doc-frame.mockup-frame');
  assert.ok(iframe11, 'mounted source iframe produces .panel-card-doc > .mockup-wrap > iframe.doc-frame.mockup-frame');
  assert.equal(iframe11.getAttribute('src'), '/s/rid/board/mockup?entry=e11');
  assert.equal(iframe11.getAttribute('data-mockup-src'), '/s/rid/board/mockup?entry=e11');
  assert.equal(iframe11.getAttribute('data-mockup-sha'), 'sha-mounted');
  assert.equal(iframe11.getAttribute('data-fallback-text'), 'Fallback description 11');
  assert.equal(iframe11.getAttribute('sandbox'), 'allow-scripts');

  // 3. Mounted iframe.mockup-frame with src only (without data-mockup-src)
  const ppin12 = board.querySelector('#e12 .ppin');
  ppin12.dispatchEvent({ type: 'click', target: ppin12 });
  assert.equal(mountCalls.length, 3, 'pinning src-only mounted mockup panel invokes hooks.mountFrames(card.body)');
  const card12 = ctx.WsCards.list('panel').find(c => c.data.panel === 'p12');
  const iframe12 = card12.body.querySelector('.doc-body.panel-card-doc > .mockup-wrap > iframe.doc-frame.mockup-frame');
  assert.ok(iframe12);
  assert.equal(iframe12.getAttribute('data-mockup-src'), '/s/rid/board/mockup?entry=e12');
  assert.equal(iframe12.getAttribute('src'), '/s/rid/board/mockup?entry=e12');
});

test('mockup panel dedup and update: unchanged skips remount; sha/src change or srcdoc<->mockup switch remounts', () => {
  const { makeEl } = createDOM();
  const topic = makeTopicEl(makeEl, 'alpha', 'Alpha Topic', makePanelHTML({
    panelId: 'p20',
    entryId: 'e20',
    recap: 'Mockup Live',
    ver: 'v1',
    mockupSrc: '/s/rid/board/mockup?entry=e20',
    mockupSha: 'sha-1',
    fallbackText: 'Fallback v1',
    mounted: true,
  }));
  const { ctx, board, mountCalls } = boot({ initialHTML: [topic] });

  const ppin = board.querySelector('.ppin');
  ppin.dispatchEvent({ type: 'click', target: ppin });
  const card = ctx.WsCards.list('panel')[0];
  assert.equal(mountCalls.length, 1);
  const firstIframe = card.body.querySelector('iframe.doc-frame.mockup-frame');
  assert.ok(firstIframe);

  // Same entry + mockupSrc + mockupSha -> does NOT remount on sync()
  ctx.WsPanelCard.sync();
  assert.equal(mountCalls.length, 1, 'unchanged mockup panel does not remount on sync()');
  assert.equal(card.body.querySelector('iframe.doc-frame.mockup-frame'), firstIframe);

  // Changing data-mockup-sha with same entry + mockupSrc -> remounts
  const stream = board.querySelector('.topic-stream');
  stream.innerHTML = makePanelHTML({
    panelId: 'p20',
    entryId: 'e20',
    recap: 'Mockup Live',
    ver: 'v1',
    mockupSrc: '/s/rid/board/mockup?entry=e20',
    mockupSha: 'sha-2',
    fallbackText: 'Fallback v1',
    mounted: false,
  });
  ctx.WsPanelCard.sync();
  assert.equal(mountCalls.length, 2, 'changing data-mockup-sha remounts');
  const shaChangedIframe = card.body.querySelector('.mockup-wrap > iframe.doc-frame.mockup-frame');
  assert.ok(shaChangedIframe);
  assert.notEqual(shaChangedIframe, firstIframe);
  assert.equal(shaChangedIframe.getAttribute('data-mockup-sha'), 'sha-2');

  // Changing data-mockup-src with same entry + mockupSha -> remounts
  stream.innerHTML = makePanelHTML({
    panelId: 'p20',
    entryId: 'e20',
    recap: 'Mockup Live',
    ver: 'v1',
    mockupSrc: '/s/rid/board/mockup?entry=e20&v=2',
    mockupSha: 'sha-2',
    fallbackText: 'Fallback v1',
    mounted: false,
  });
  ctx.WsPanelCard.sync();
  assert.equal(mountCalls.length, 3, 'changing data-mockup-src remounts');
  const srcChangedIframe = card.body.querySelector('.mockup-wrap > iframe.doc-frame.mockup-frame');
  assert.ok(srcChangedIframe);
  assert.notEqual(srcChangedIframe, shaChangedIframe);
  assert.equal(srcChangedIframe.getAttribute('data-mockup-src'), '/s/rid/board/mockup?entry=e20&v=2');

  // Switching from mockup to fallback data-srcdoc -> remounts without .mockup-wrap
  stream.innerHTML = makePanelHTML({
    panelId: 'p20',
    entryId: 'e20',
    recap: 'Mockup Unavailable',
    ver: 'v1',
    srcdoc: '<p class="mockup-missing">Mockup snapshot unavailable.</p>',
    mounted: false,
  });
  ctx.WsPanelCard.sync();
  assert.equal(mountCalls.length, 4, 'switching from mockup to fallback data-srcdoc remounts');
  assert.equal(card.body.querySelector('.mockup-wrap'), null, 'fallback srcdoc panel has no .mockup-wrap');
  const fallbackIframe = card.body.querySelector('iframe.doc-frame');
  assert.ok(fallbackIframe);
  assert.equal(fallbackIframe.classList.contains('mockup-frame'), false);
  assert.equal(fallbackIframe.getAttribute('srcdoc'), '<p class="mockup-missing">Mockup snapshot unavailable.</p>');

  // Switching back from fallback data-srcdoc to mockup -> remounts inside .mockup-wrap
  stream.innerHTML = makePanelHTML({
    panelId: 'p20',
    entryId: 'e20',
    recap: 'Mockup Restored',
    ver: 'v1',
    mockupSrc: '/s/rid/board/mockup?entry=e20&v=3',
    mockupSha: 'sha-3',
    fallbackText: 'Fallback v3',
    mounted: false,
  });
  ctx.WsPanelCard.sync();
  assert.equal(mountCalls.length, 5, 'switching from fallback data-srcdoc to mockup remounts');
  const restoredIframe = card.body.querySelector('.mockup-wrap > iframe.doc-frame.mockup-frame');
  assert.ok(restoredIframe, 'restored mockup panel wraps iframe in .mockup-wrap');
  assert.equal(restoredIframe.getAttribute('data-mockup-src'), '/s/rid/board/mockup?entry=e20&v=3');
  assert.equal(restoredIframe.getAttribute('data-mockup-sha'), 'sha-3');
});

test('remote hydration via wbHistoryFetch preserves .mockup-wrap and mockup attributes', async () => {
  const historyFetch = async query => {
    if (query === 'target=p30') {
      return [{
        name: 'closed-mockup-topic',
        html: `<section class="topic closed" data-topic="closed-mockup-topic"><div class="tophead"><div class="tbar"><span class="tname">Closed Mockup</span></div></div>`
          + makePanelHTML({
            panelId: 'p30',
            entryId: 'e30',
            recap: 'Hydrated Mockup',
            ver: 'v3',
            mockupSrc: '/s/rid/board/mockup?entry=e30',
            mockupSha: 'sha-remote',
            fallbackText: 'Remote mockup fallback',
          })
          + `</section>`,
      }];
    }
    return [];
  };
  const stored = {
    v: 3,
    cards: [{ id: 'c30', kind: 'panel', data: { panel: 'p30', topic: 'closed-mockup-topic' }, pinned: 1, h: 0, fold: false, at: 1 }],
    gallery: { v: 1, pages: [{ id: 'g1', root: { card: 'c30' } }], active: 0, rev: 1 },
    topics: [],
  };
  const { ctx, mountCalls } = boot({ initialHTML: [], stored, historyFetch });
  await new Promise(r => setImmediate(r));

  assert.equal(mountCalls.length, 1);
  const card = ctx.WsCards.list('panel')[0];
  const iframe = card.body.querySelector('.doc-body.panel-card-doc > .mockup-wrap > iframe.doc-frame.mockup-frame');
  assert.ok(iframe, 'hydrated mockup iframe is wrapped in .mockup-wrap and mounted');
  assert.equal(iframe.getAttribute('src'), '/s/rid/board/mockup?entry=e30');
  assert.equal(iframe.getAttribute('data-mockup-src'), '/s/rid/board/mockup?entry=e30');
  assert.equal(iframe.getAttribute('data-mockup-sha'), 'sha-remote');
  assert.equal(iframe.getAttribute('data-fallback-text'), 'Remote mockup fallback');
  assert.equal(iframe.getAttribute('sandbox'), 'allow-scripts');
});

test('dashboard-board.css sizes pinned and maximized .mockup-wrap panels cleanly without breaking non-mockup panels', () => {
  assert.match(boardCssSrc, /\.bcard\[data-kind="panel"\]:has\(\.panel-card-doc > \.mockup-wrap\) \.bc-body,\s*\.bcard\[data-kind="panel"\]\[data-max\]:has\(\.panel-card-doc > \.mockup-wrap\) \.bc-body \{\s*display:\s*flex;\s*flex-direction:\s*column;\s*\}/);
  assert.match(boardCssSrc, /\.bcard\[data-kind="panel"\] .panel-card-doc:has\(> \.mockup-wrap\) \{[^}]*flex:\s*1 1 0;[^}]*min-height:\s*240px;[^}]*padding:\s*0;[^}]*display:\s*flex;[^}]*flex-direction:\s*column;[^}]*overflow:\s*hidden;[^}]*\}/);
  assert.match(boardCssSrc, /\.bcard\[data-kind="panel"\] \.panel-card-doc > \.mockup-wrap \{[^}]*flex:\s*1 1 0;[^}]*height:\s*100%;[^}]*min-height:\s*240px;[^}]*resize:\s*none;[^}]*border:\s*0;[^}]*\}/);
  assert.match(boardCssSrc, /\.bcard\[data-kind="panel"\] \.panel-card-doc \{\s*padding:\s*8px 12px 10px;\s*\}/);
});
