// Offline unit & integration tests for single-click title-bar scroll-to-top
// in cards.js (.tbar / .bc-hd across #bcol, #bgal, [data-max], .overview-preview-stage,
// and standalone ?document=1 fallback).
// Run: node --test agents-plugin-tool/internal/wsdashboard/testdata/title-click-scroll-top.test.cjs
const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

const gallerySrc = readAssetSource('wsdashboard', 'gallery.js');
const cardsSrc = readAssetSource('wsdashboard', 'cards.js');
const anchorSrc = readAssetSource('wsdashboard', 'dashboard-board-anchor.js');

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

function splitTopLevelCommas(sel) {
  const out = [];
  let cur = '';
  let depth = 0;
  for (let i = 0; i < sel.length; i++) {
    const ch = sel[i];
    if (ch === '[' || ch === '(') depth++;
    else if (ch === ']' || ch === ')') depth--;
    if (depth === 0 && ch === ',') {
      if (cur.trim()) out.push(cur.trim());
      cur = '';
    } else cur += ch;
  }
  if (cur.trim()) out.push(cur.trim());
  return out;
}

function matchSimple(el, sel, root, docRoot) {
  if (!el || el.nodeType !== 1) return false;
  sel = sel.trim();
  if (!sel) return false;
  if (sel === '*') return true;
  const alts = splitTopLevelCommas(sel);
  if (alts.length > 1) return alts.some(s => matchSimple(el, s, root, docRoot));
  if (sel === ':scope') return el === root;
  if (sel.startsWith(':root')) {
    if (el !== docRoot) return false;
    sel = sel.slice(5).trim();
    if (!sel) return true;
  }
  const nots = [];
  let base = sel.replace(/:not\(([^)]+)\)/g, (_, inner) => { nots.push(inner.trim()); return ''; });
  for (const n of nots) if (matchSimple(el, n, root, docRoot)) return false;
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

function matchChain(el, selector, root, docRoot) {
  if (!el || el.nodeType !== 1) return false;
  const alts = splitTopLevelCommas(selector);
  if (alts.length > 1) return alts.some(s => matchChain(el, s, root, docRoot));
  const tokens = [];
  let cur = '';
  let depth = 0;
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
    if (!matchSimple(node, tok, root, docRoot)) return false;
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

function createDOM() {
  const docListeners = {};
  let documentElement;
  function makeEl(tag, opts = {}) {
    const props = {};
    const el = {
      nodeType: 1,
      tagName: tag.toUpperCase(),
      attrs: Object.assign({}, opts.attrs || {}),
      children: [],
      parentNode: null,
      style: {
        top: '',
        minHeight: '',
        cssText: '',
        setProperty(k, v) { props[k] = String(v); },
        removeProperty(k) { delete props[k]; },
        getPropertyValue(k) { return props[k] || ''; },
      },
      hidden: !!opts.hidden,
      inert: false,
      open: !!opts.open,
      tabIndex: 0,
      scrollTop: opts.scrollTop || 0,
      scrollLeft: 0,
      clientTop: 0,
      clientHeight: opts.clientHeight || 0,
      scrollHeight: opts.scrollHeight || 0,
      offsetHeight: opts.offsetHeight || 0,
      offsetTop: opts.offsetTop || 0,
      overflowY: opts.overflowY || 'visible',
      paddingTop: opts.paddingTop || '0px',
      railInsetCss: opts.railInsetCss || '',
      scrollCalls: [],
      listeners: {},
      _text: opts.text || '',
      get childNodes() { return this.children; },
      get firstChild() { return this.children[0] || null; },
      get firstElementChild() { return this.children[0] || null; },
      get nextSibling() {
        if (!this.parentNode) return null;
        const i = this.parentNode.children.indexOf(this);
        return i >= 0 ? this.parentNode.children[i + 1] || null : null;
      },
      get nextElementSibling() { return this.nextSibling; },
      get parentElement() { return this.parentNode; },
      get isConnected() {
        for (let p = this; p; p = p.parentNode) if (p === documentElement || p === document.body) return true;
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
      matches(sel) { return matchChain(this, sel, this, documentElement); },
      closest(sel) {
        for (let p = this; p && p.nodeType === 1; p = p.parentNode) {
          if (matchChain(p, sel, p, documentElement)) return p;
        }
        return null;
      },
      querySelectorAll(sel) {
        const out = [];
        const walk = n => {
          for (const c of n.children) {
            if (matchChain(c, sel, this, documentElement)) out.push(c);
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
        ev.defaultPrevented = !!ev.defaultPrevented;
        ev.preventDefault = () => { ev.defaultPrevented = true; };
        ev.stopped = false;
        ev.stopPropagation = () => { ev.stopped = true; };
        for (let p = this; p; p = p.parentNode) {
          for (const fn of (p.listeners && p.listeners[ev.type]) || []) fn(ev);
          if (ev.stopped) break;
        }
        if (!ev.stopped) {
          for (const fn of docListeners[ev.type] || []) fn(ev);
        }
        return !ev.defaultPrevented;
      },
      scrollTo(arg) {
        const rec = typeof arg === 'object' && arg !== null ? { top: arg.top, behavior: arg.behavior } : { top: arguments[1], behavior: undefined };
        this.scrollCalls.push(rec);
        if (typeof rec.top === 'number') this.scrollTop = rec.top;
      },
      getBoundingClientRect() {
        if (opts.rect) return opts.rect(this);
        const y = parseFloat(this.style.top) || 0;
        const h = this.offsetHeight || this.clientHeight || (this.classList.contains('board-toolbar') ? 40 : 0);
        return { left: 0, top: y, right: 600, bottom: y + h, width: 600, height: h };
      },
      getClientRects() { return [this.getBoundingClientRect()]; },
      focus() {},
      scrollIntoView() {},
    };
    if (opts.cls) el.attrs.class = opts.cls.join(' ');
    el.classList = makeClassList(el);
    return el;
  }

  documentElement = makeEl('html');
  const body = makeEl('body');
  documentElement.appendChild(body);
  const document = {
    documentElement,
    body,
    scrollingElement: documentElement,
    activeElement: null,
    createElement: (tag, o) => makeEl(tag, o),
    createElementNS: (_, tag) => makeEl(tag),
    getElementById(id) { return documentElement.querySelector('#' + id); },
    querySelector(sel) {
      if (sel.startsWith(':root') && matchChain(documentElement, sel, documentElement, documentElement)) {
        return documentElement;
      }
      return documentElement.querySelector(sel);
    },
    querySelectorAll(sel) { return documentElement.querySelectorAll(sel); },
    addEventListener(type, fn) { (docListeners[type] ||= []).push(fn); },
    removeEventListener(type, fn) { docListeners[type] = (docListeners[type] || []).filter(x => x !== fn); },
    dispatchDoc(ev) {
      ev.defaultPrevented = !!ev.defaultPrevented;
      ev.preventDefault = () => { ev.defaultPrevented = true; };
      ev.stopPropagation = () => { ev.stopped = true; };
      for (const fn of docListeners[ev.type] || []) fn(ev);
    },
  };
  return { document, makeEl };
}

function makeTopic(makeEl, topic, opts = {}) {
  const sec = makeEl('section', {
    cls: ['topic'],
    attrs: { id: 'topic-' + topic, 'data-topic': topic, 'data-at': '100', ...(opts.rail ? { 'data-rail': '' } : {}) },
    offsetHeight: opts.height || 600,
    scrollTop: opts.scrollTop || 0,
    rect: opts.rect,
  });
  const tophead = makeEl('div', { cls: ['tophead'] });
  const tbar = makeEl('div', { cls: ['tbar'] });
  const tname = makeEl('span', { cls: ['tname'], text: topic });
  const btn = makeEl('button', { attrs: { type: 'button' }, cls: ['tpin'] });
  const otherBtn = makeEl('button', { attrs: { type: 'button' }, cls: ['t-act'] });
  const link = makeEl('a', { attrs: { href: '#' }, text: 'link' });
  const pop = makeEl('div', { cls: ['pop'] });
  const roleBtn = makeEl('div', { attrs: { role: 'button' }, text: 'act' });
  const label = makeEl('label', { text: 'lbl' });
  tbar.appendChild(tname);
  tbar.appendChild(btn);
  tbar.appendChild(otherBtn);
  tbar.appendChild(link);
  tbar.appendChild(pop);
  tbar.appendChild(roleBtn);
  tbar.appendChild(label);

  const tsub = makeEl('div', { cls: ['tsub'] });
  const trecap = makeEl('div', { cls: ['trecap'], text: 'recap' });
  tsub.appendChild(trecap);
  const tnote = makeEl('div', { cls: ['tnote'], text: 'note' });
  tophead.appendChild(tbar);
  tophead.appendChild(tsub);
  tophead.appendChild(tnote);

  const details = makeEl('details', { cls: ['entry-panel'], open: true });
  const summary = makeEl('summary', { text: 'Summary toggle' });
  details.appendChild(summary);

  sec.appendChild(tophead);
  sec.appendChild(details);
  return { sec, tophead, tbar, tname, btn, otherBtn, link, pop, roleBtn, label, trecap, tnote, details, summary };
}

function makeBcard(makeEl, id, opts = {}) {
  const card = makeEl('div', {
    cls: ['bcard'],
    attrs: { 'data-card': id, ...(opts.kind ? { 'data-kind': opts.kind } : {}), ...(opts.rail ? { 'data-rail': '' } : {}) },
    offsetHeight: opts.height || 400,
    rect: opts.rect,
  });
  const bcIn = makeEl('div', { cls: ['bc-in'] });
  const bcHd = makeEl('div', { cls: ['bc-hd'] });
  const bcTitle = makeEl('span', { cls: ['bc-t'], text: id });
  const bcBtn = makeEl('button', { attrs: { type: 'button' }, cls: ['bc-pin'] });
  bcHd.appendChild(bcTitle);
  bcHd.appendChild(bcBtn);
  const bcBody = makeEl('div', { cls: ['bc-body'], scrollTop: opts.bodyScrollTop || 0 });
  bcIn.appendChild(bcHd);
  bcIn.appendChild(bcBody);
  card.appendChild(bcIn);
  return { card, bcIn, bcHd, bcTitle, bcBtn, bcBody };
}

function fireClickGesture(document, target, opts = {}) {
  const pid = opts.pointerId || 1;
  const x0 = opts.x0 !== undefined ? opts.x0 : 40;
  const y0 = opts.y0 !== undefined ? opts.y0 : 20;
  const dx = opts.dx || 0;
  const dy = opts.dy || 0;
  target.dispatchEvent({
    type: 'pointerdown',
    pointerId: pid,
    isPrimary: opts.isPrimary !== undefined ? opts.isPrimary : true,
    pointerType: opts.pointerType || 'mouse',
    button: opts.button !== undefined ? opts.button : 0,
    clientX: x0,
    clientY: y0,
    target,
  });
  if (dx || dy) {
    document.dispatchDoc({
      type: 'pointermove',
      pointerId: pid,
      pointerType: opts.pointerType || 'mouse',
      buttons: 1,
      clientX: x0 + dx,
      clientY: y0 + dy,
      target,
    });
  }
  document.dispatchDoc({
    type: 'pointerup',
    pointerId: pid,
    pointerType: opts.pointerType || 'mouse',
    clientX: x0 + dx,
    clientY: y0 + dy,
    target,
  });
  const clickEv = {
    type: 'click',
    button: opts.button !== undefined ? opts.button : 0,
    detail: opts.detail !== undefined ? opts.detail : 1,
    ctrlKey: !!opts.ctrlKey,
    metaKey: !!opts.metaKey,
    shiftKey: !!opts.shiftKey,
    altKey: !!opts.altKey,
    defaultPrevented: !!opts.defaultPrevented,
    target,
  };
  target.dispatchEvent(clickEv);
  if (typeof opts.flush === 'function') opts.flush();
}

function bootCards({ freeCanvas = false, reduced = false, collapsed = true, boardOverflowY = 'auto', wide = false } = {}) {
  const dom = createDOM();
  const { document, makeEl } = dom;
  const bcol = makeEl('div', {
    attrs: { id: 'bcol', ...(freeCanvas ? { 'data-board-free': '' } : {}) },
    overflowY: boardOverflowY,
    clientHeight: 800,
    scrollHeight: 4000,
    paddingTop: '12px',
    railInsetCss: '24px',
    rect: () => ({ left: 0, top: 0, right: 900, bottom: 800, width: 900, height: 800 }),
  });
  const board = makeEl('div', { attrs: { id: 'r-board' }, cls: ['r-board'] });
  bcol.appendChild(board);
  document.body.appendChild(bcol);

  const selectCalls = [];
  const state = { reduced, collapsed };
  const storage = {};
  const zeroTimers = [];

  const ctx = {
    document,
    console,
    Math, JSON, Array, Object, String, Number, Date, RegExp, Promise, Map, Set, WeakMap, Error,
    encodeURIComponent, decodeURIComponent, parseInt, parseFloat, isNaN, isFinite,
    setTimeout: (fn, ms) => { if (!ms) zeroTimers.push(fn); return zeroTimers.length; },
    clearTimeout: () => {},
    requestAnimationFrame: fn => { fn(); return 1; },
    cancelAnimationFrame: () => {},
    queueMicrotask: fn => fn(),
    wsT: k => k,
    getComputedStyle: n => ({
      overflowY: n.overflowY || 'visible',
      paddingTop: n.paddingTop || '0px',
      getPropertyValue: k => (k === '--wb-rail-inset' ? (n.railInsetCss || '') : ''),
    }),
    getSelection: () => ({ isCollapsed: state.collapsed }),
    addEventListener: () => {},
    removeEventListener: () => {},
    matchMedia: q => ({ matches: q.includes('prefers-reduced-motion') ? state.reduced : false }),
    innerWidth: wide ? 1600 : 1200,
    innerHeight: 900,
    WsBoardAnchor: freeCanvas ? {
      select(el, opts) { selectCalls.push({ el, opts: opts ? { smooth: opts.smooth, alignTop: opts.alignTop } : opts }); },
    } : undefined,
  };
  ctx.window = ctx;
  vm.createContext(ctx);
  vm.runInContext(gallerySrc, ctx);
  vm.runInContext(cardsSrc, ctx);
  ctx.WsCards.mount(board, {
    key: 'ws.dashboard.cards.test',
    load: k => storage[k] || null,
    save: (k, v) => { storage[k] = v; },
    host: document.body,
    mode: () => ({ width: wide ? 1600 : 1200, tabs: false }),
  });
  function flushZeroTimers() {
    while (zeroTimers.length) zeroTimers.shift()();
  }
  function clickTitle(target, opts = {}) {
    fireClickGesture(document, target, { ...opts, flush: flushZeroTimers });
  }
  return { dom, ctx, document, makeEl, bcol, board, selectCalls, state, clickTitle };
}

test('1. Pinned gallery (#bgal / .gal-pane): .tbar scrolls .gal-pbody and .bc-hd scrolls .bc-body without WsBoardAnchor.select', () => {
  const { ctx, makeEl, board, selectCalls, clickTitle } = bootCards({ freeCanvas: true, wide: true });
  const topic = makeTopic(makeEl, 'pinned-alpha', { scrollTop: 95 });
  board.appendChild(topic.sec);
  ctx.WsCards.settle(board, ctx.WsCards.weave([topic.sec]));

  // Dock the topic into #bgal via .tpin click so gallery wraps it in .gal-pane > .gal-pbody.
  topic.btn.dispatchEvent({ type: 'click', target: topic.btn });
  const galEl = ctx.WsCards.gallery();
  const pane = galEl.querySelector('.gal-pane');
  assert.ok(pane, 'topic docked into .gal-pane inside #bgal');
  const pbody = topic.sec.querySelector(':scope > .gal-pbody');
  assert.ok(pbody, 'docked section.topic contains direct .gal-pbody');
  pbody.scrollTop = 420;
  topic.sec.scrollTop = 95;

  // Click .tbar inside docked topic.
  clickTitle(topic.tname);
  assert.deepEqual(pbody.scrollCalls, [{ top: 0, behavior: 'smooth' }]);
  assert.equal(pbody.scrollTop, 0);
  assert.equal(topic.sec.scrollCalls.length, 0);
  assert.equal(selectCalls.length, 0, 'WsBoardAnchor.select must never be called for #bgal');

  // Docked .bcard and .bcard[data-kind="panel"] inside a .gal-pane.
  const bcard = makeBcard(makeEl, 'term-1', { kind: 'panel', bodyScrollTop: 310 });
  pbody.appendChild(bcard.card);
  clickTitle(bcard.bcTitle);
  assert.deepEqual(bcard.bcBody.scrollCalls, [{ top: 0, behavior: 'smooth' }]);
  assert.equal(bcard.bcBody.scrollTop, 0);
  assert.equal(pbody.scrollCalls.length, 1, 'docked .bcard click scrolls .bc-body, not .gal-pbody');
  assert.equal(selectCalls.length, 0);
});

test('2. Maximized sheets ([data-max]): .tbar scrolls section.topic, .bc-hd scrolls .bc-body, ignores >5px drag, never calls WsBoardAnchor.select', () => {
  const { document, makeEl, board, bcol, selectCalls, clickTitle } = bootCards({ freeCanvas: true });
  const topic = makeTopic(makeEl, 'max-alpha', { scrollTop: 550 });
  topic.sec.setAttribute('data-max', '1');
  document.documentElement.setAttribute('data-board-max', '');
  board.appendChild(topic.sec);

  // Drag > 5px inside [data-max] must be suppressed by titlePress tracking.
  clickTitle(topic.tname, { dx: 8, dy: 0 });
  assert.equal(topic.sec.scrollCalls.length, 0);
  assert.equal(topic.sec.scrollTop, 550);

  // Jitter <= 5px still triggers scroll-to-top.
  clickTitle(topic.tname, { dx: 3, dy: 2 });
  assert.deepEqual(topic.sec.scrollCalls, [{ top: 0, behavior: 'smooth' }]);
  assert.equal(topic.sec.scrollTop, 0);
  assert.equal(selectCalls.length, 0);
  assert.equal(bcol.scrollCalls.length, 0);

  // Maximized .bcard scrolls .bc-in > .bc-body.
  const bcard = makeBcard(makeEl, 'files-1', { bodyScrollTop: 280 });
  bcard.card.setAttribute('data-max', '1');
  board.appendChild(bcard.card);
  clickTitle(bcard.bcTitle);
  assert.deepEqual(bcard.bcBody.scrollCalls, [{ top: 0, behavior: 'smooth' }]);
  assert.equal(bcard.bcBody.scrollTop, 0);
  assert.equal(selectCalls.length, 0);
});

test('3. Overview Quick Look (.overview-preview-stage): scrolls stage card while :root[data-overview] suppresses #bcol', () => {
  const { ctx, document, makeEl, board, bcol, selectCalls, clickTitle } = bootCards({ freeCanvas: true });
  document.documentElement.setAttribute('data-overview', '');
  const galEl = ctx.WsCards.gallery();

  // Clicking a card still on #bcol while :root[data-overview] is active must be suppressed.
  const boardTopic = makeTopic(makeEl, 'board-behind-overview', { scrollTop: 300 });
  board.appendChild(boardTopic.sec);
  clickTitle(boardTopic.tname);
  assert.equal(selectCalls.length, 0);
  assert.equal(bcol.scrollCalls.length, 0);
  assert.equal(boardTopic.sec.scrollCalls.length, 0);

  // Stage inside #bgal with a .gal-pane ancestor: .tbar must scroll section.topic, NOT .gal-pbody.
  const pane = makeEl('div', { cls: ['gal-pane'] });
  const pbody = makeEl('div', { cls: ['gal-pbody'], scrollTop: 150 });
  const stage = makeEl('div', { cls: ['overview-preview-stage'] });
  const stageTopic = makeTopic(makeEl, 'preview-topic', { scrollTop: 640 });
  const stageCard = makeBcard(makeEl, 'preview-card', { bodyScrollTop: 290 });
  stage.appendChild(stageTopic.sec);
  stage.appendChild(stageCard.card);
  pbody.appendChild(stage);
  pane.appendChild(pbody);
  galEl.appendChild(pane);

  clickTitle(stageTopic.tname);
  assert.deepEqual(stageTopic.sec.scrollCalls, [{ top: 0, behavior: 'smooth' }]);
  assert.equal(stageTopic.sec.scrollTop, 0);
  assert.equal(pbody.scrollCalls.length, 0);
  assert.equal(selectCalls.length, 0);

  clickTitle(stageCard.bcTitle);
  assert.deepEqual(stageCard.bcBody.scrollCalls, [{ top: 0, behavior: 'smooth' }]);
  assert.equal(stageCard.bcBody.scrollTop, 0);
  assert.equal(selectCalls.length, 0);
});

test('4. Free-canvas #bcol[data-board-free]: calls WsBoardAnchor.select with { smooth: !calm, alignTop: true }', () => {
  const { makeEl, board, selectCalls, state, clickTitle } = bootCards({ freeCanvas: true, reduced: false });
  const topic = makeTopic(makeEl, 'free-alpha');
  const bcard = makeBcard(makeEl, 'free-card');
  board.appendChild(topic.sec);
  board.appendChild(bcard.card);

  clickTitle(topic.tname);
  assert.equal(selectCalls.length, 1);
  assert.equal(selectCalls[0].el, topic.sec);
  assert.deepEqual(selectCalls[0].opts, { smooth: true, alignTop: true });

  state.reduced = true;
  clickTitle(bcard.bcTitle);
  assert.equal(selectCalls.length, 2);
  assert.equal(selectCalls[1].el, bcard.card);
  assert.deepEqual(selectCalls[1].opts, { smooth: false, alignTop: true });
});

test('5. Standalone / non-free-canvas (?document=1) fallback: scrolls page/board scroller cleanly below sticky offset', () => {
  const { document, makeEl, bcol, board, state, clickTitle } = bootCards({ freeCanvas: false, boardOverflowY: 'visible' });
  const sc = document.scrollingElement;
  sc.scrollTop = 1000;

  let t1Top = 260; // sc.scrollTop (1000) + 260 = 1260; scrollingElement padTop = 0, railInset = 24 => target = 1236
  const topic = makeTopic(makeEl, 'standalone-topic', {
    rect: () => ({ left: 0, top: t1Top, right: 600, bottom: t1Top + 500, width: 600, height: 500 }),
  });
  board.appendChild(topic.sec);
  clickTitle(topic.tname);
  assert.deepEqual(sc.scrollCalls, [{ top: 1236, behavior: 'smooth' }]);
  assert.equal(sc.scrollTop, 1236);

  // Card in [data-rail] adds rail pill clearance (24px) unless [data-anchor-cap="0"].
  const bcard = makeBcard(makeEl, 'rail-card', {
    rail: true,
    rect: () => ({ left: 0, top: 200, right: 600, bottom: 560, width: 600, height: 360 }),
  });
  board.appendChild(bcard.card);
  clickTitle(bcard.bcTitle);
  // 1236 + (200 - (0 + 0 + 24 + 24)) = 1388
  assert.deepEqual(sc.scrollCalls[1], { top: 1388, behavior: 'smooth' });

  // data-anchor-cap="0" resets scroller to 0 with reduced-motion -> behavior: 'auto'.
  state.reduced = true;
  topic.sec.setAttribute('data-anchor-cap', '0');
  clickTitle(topic.tname);
  assert.deepEqual(sc.scrollCalls[2], { top: 0, behavior: 'auto' });

  // Non-window #bcol scroller when bcol itself overflows (includes bcol paddingTop 12px + railInset 24px).
  bcol.overflowY = 'auto';
  bcol.scrollTop = 500;
  topic.sec.removeAttribute('data-anchor-cap');
  t1Top = 180;
  clickTitle(topic.tname);
  // 500 + (180 - (0 + 12 + 24)) = 644
  assert.deepEqual(bcol.scrollCalls[0], { top: 644, behavior: 'auto' });
});

test('6. Guards: ignores non-title siblings, details>summary, prevented/non-primary/modifier/multi-click, interactive children, drags, and non-collapsed selections', () => {
  const { makeEl, board, selectCalls, state, clickTitle } = bootCards({ freeCanvas: true });
  const topic = makeTopic(makeEl, 'guarded');
  board.appendChild(topic.sec);

  // Non-.tbar siblings inside .tophead and details.entry-panel > summary.
  clickTitle(topic.trecap);
  clickTitle(topic.tnote);
  clickTitle(topic.summary);
  // Interactive children inside .tbar (non-pin button, link, pop, role=button, label).
  clickTitle(topic.otherBtn);
  clickTitle(topic.link);
  clickTitle(topic.pop);
  clickTitle(topic.roleBtn);
  clickTitle(topic.label);
  // Prevented, non-left button, modifiers, double-click.
  clickTitle(topic.tname, { defaultPrevented: true });
  clickTitle(topic.tname, { button: 2 });
  clickTitle(topic.tname, { ctrlKey: true });
  clickTitle(topic.tname, { metaKey: true });
  clickTitle(topic.tname, { shiftKey: true });
  clickTitle(topic.tname, { altKey: true });
  clickTitle(topic.tname, { detail: 2 });
  // Drag > 5px.
  clickTitle(topic.tname, { dx: 7, dy: 0 });
  // Non-collapsed selection.
  state.collapsed = false;
  clickTitle(topic.tname);

  assert.equal(selectCalls.length, 0, 'all guarded interactions must leave WsBoardAnchor.select untouched');

  // Verify sanity after clearing guard state.
  state.collapsed = true;
  clickTitle(topic.tname);
  assert.equal(selectCalls.length, 1);
});

test('7. Free-canvas E2E with real dashboard-board-anchor.js: already-anchored scrolled-down tall card scrolls cleanly to its top on pointerdown->pointerup->click', () => {
  const dom = createDOM();
  const { document, makeEl } = dom;
  const body = document.body;

  const bcol = makeEl('div', {
    attrs: { id: 'bcol' },
    overflowY: 'auto',
    clientHeight: 800,
    paddingTop: '12px',
    rect: () => ({ left: 0, top: 0, right: 900, bottom: 800, width: 900, height: 800 }),
  });
  bcol.style.setProperty('--wb-rail-inset', '12px');
  const board = makeEl('div', { attrs: { id: 'r-board' }, cls: ['r-board'] });
  const toolbar = makeEl('div', { cls: ['board-toolbar'], offsetHeight: 40 });
  board.appendChild(toolbar);

  const first = makeTopic(makeEl, 'first', { height: 360 });
  const mid = makeTopic(makeEl, 'mid', { height: 360 });
  const tall = makeTopic(makeEl, 'tall-anchor', { height: 1200 });
  const tails = [0, 1, 2, 3].map(i => makeTopic(makeEl, 'tail-' + i, { height: 600 }));
  board.appendChild(first.sec);
  board.appendChild(mid.sec);
  board.appendChild(tall.sec);
  for (const t of tails) board.appendChild(t.sec);
  bcol.appendChild(board);
  body.appendChild(bcol);

  const rafs = [];
  const zeroTimers = [];
  const timers = new Map();
  let nextTimer = 100;
  const storage = {};
  const state = { reduced: true };

  class FakeRO {
    observe() {}
    unobserve() {}
    disconnect() {}
  }

  const ctx = {
    document,
    doc: document,
    console,
    Math, JSON, Array, Object, String, Number, Date, RegExp, Promise, Map, Set, WeakMap, Error,
    encodeURIComponent, decodeURIComponent, parseInt, parseFloat, isNaN, isFinite,
    location: { pathname: '/s/free/', search: '' },
    $: id => (id === 'bcol' ? bcol : id === 'r-board' ? board : null),
    region: name => (name === 'board' ? board : null),
    topicKey: n => (n && n.nodeType === 1 && n.tagName === 'SECTION' && n.classList.contains('topic') ? n.getAttribute('data-topic') : null),
    reducedMotion: () => state.reduced,
    boardPending: {},
    boardGuards: () => [],
    boardFlushViewed: () => {},
    boardSnap: null,
    dialScrubbing: false,
    ResizeObserver: FakeRO,
    MutationObserver: null,
    sessionStorage: {
      getItem: k => storage['ss:' + k] || null,
      setItem: (k, v) => { storage['ss:' + k] = String(v); },
      removeItem: k => { delete storage['ss:' + k]; },
    },
    setTimeout: (fn, ms) => {
      if (!ms) {
        zeroTimers.push(fn);
        return zeroTimers.length;
      }
      const id = nextTimer++;
      timers.set(id, { fn, ms });
      return id;
    },
    clearTimeout: id => { timers.delete(id); },
    requestAnimationFrame: fn => { rafs.push(fn); return rafs.length; },
    cancelAnimationFrame: () => {},
    queueMicrotask: fn => fn(),
    wsT: k => k,
    getComputedStyle: n => ({
      overflowY: n.overflowY || 'visible',
      paddingTop: n.paddingTop || '0px',
      getPropertyValue: k => (k === '--wb-rail-inset' ? (n.railInsetCss || '') : ''),
    }),
    getSelection: () => ({ isCollapsed: true }),
    matchMedia: q => ({ matches: q.includes('prefers-reduced-motion') ? state.reduced : false }),
    addEventListener: () => {},
    removeEventListener: () => {},
    unmountFrames: () => {},
    mountFrames: () => {},
    mountFramesPaced: () => {},
    readAnswers: () => ({}),
    writeAnswers: () => {},
    frameHeights: () => ({}),
    setFrameHeights: () => {},
    innerWidth: 1200,
    innerHeight: 800,
  };
  ctx.window = ctx;
  vm.createContext(ctx);
  vm.runInContext(gallerySrc, ctx);
  vm.runInContext(cardsSrc, ctx);
  vm.runInContext(anchorSrc, ctx);

  ctx.anchorInit();
  ctx.WsCards.mount(board, {
    key: 'ws.dashboard.cards.test',
    load: k => storage[k] || null,
    save: (k, v) => { storage[k] = v; },
    host: body,
    mode: () => ({ width: 1200, tabs: false }),
  });

  function flushZero() { while (zeroTimers.length) zeroTimers.shift()(); }

  let info = ctx.WsBoardAnchor.windowInfo();
  assert.equal(info.active, true, 'free-canvas mode is active');
  const tallTop = info.posOf(tall.sec);
  assert.ok(tallTop > 0, 'tall card sits below preceding cards');

  // Scroll partway down inside tall-anchor so it is the active anchor (index 2).
  bcol.scrollTop = tallTop + 420;
  ctx.freeOnScroll();
  while (rafs.length) rafs.shift()();
  info = ctx.WsBoardAnchor.windowInfo();
  assert.equal(info.anchorIdx, 2, 'tall-anchor is active anchor while scrolled 420px inside it');

  // Reduced-motion click: immediately aligns top of already-anchored card below topClear.
  fireClickGesture(document, tall.tname, { flush: flushZero });
  while (rafs.length) rafs.shift()();
  const topAfterReduced = ctx.WsBoardAnchor.windowInfo().posOf(tall.sec);
  const railInset = parseFloat(bcol.style.getPropertyValue('--wb-rail-inset')) || 0;
  const topClear = railInset + 44; // live --wb-rail-inset + 2-slot rail cap (44)
  assert.equal(bcol.scrollTop, Math.max(0, Math.round(topAfterReduced - topClear)), 'reduced-motion click aligns already-anchored card top below topClear');

  // Smooth animation: pointerdown runs anchorCancelProg, click launches smooth jump back to card top.
  state.reduced = false;
  bcol.scrollTop = topAfterReduced + 500;
  ctx.freeOnScroll();
  while (rafs.length) rafs.shift()();
  fireClickGesture(document, tall.tname, { flush: flushZero });
  let safety = 0;
  while (rafs.length && safety++ < 200) rafs.shift()(safety * 50);
  const topAfterSmooth = ctx.WsBoardAnchor.windowInfo().posOf(tall.sec);
  assert.equal(bcol.scrollTop, Math.max(0, Math.round(topAfterSmooth - topClear)), 'smooth click animation aligns already-anchored card top below topClear');
});
