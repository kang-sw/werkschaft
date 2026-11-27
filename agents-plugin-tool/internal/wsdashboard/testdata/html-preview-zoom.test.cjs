'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

const gallerySrc = readAssetSource('wsdashboard', 'gallery.js');
const cardsSrc = readAssetSource('wsdashboard', 'cards.js');
const panelcardSrc = readAssetSource('wsdashboard', 'panelcard.js');
const previewSrc = readAssetSource('wsdashboard', 'preview.js');
const framesSrc = readAssetSource('wsdashboard', 'dashboard-frames.js');
const boardSrc = readAssetSource('wsdashboard', 'dashboard-board.js');
const boardCss = readAssetSource('wsdashboard', 'dashboard-board.css');

function makeDomEnv() {
  const winListeners = {};
  const docListeners = {};

  function parseSimple(simple) {
    const m = simple.match(/^([a-z0-9*]*)(.*)$/i);
    if (!m) return null;
    const tag = m[1] ? m[1].toUpperCase() : '';
    let rest = m[2] || '';
    const checks = [];
    while (rest.length) {
      if (rest[0] === '.') {
        const cm = rest.match(/^\.([\w-]+)/);
        if (!cm) return null;
        checks.push(el => el.cls.has(cm[1]));
        rest = rest.slice(cm[0].length);
      } else if (rest[0] === '#') {
        const im = rest.match(/^#([\w-]+)/);
        if (!im) return null;
        checks.push(el => el.getAttribute('id') === im[1]);
        rest = rest.slice(im[0].length);
      } else if (rest[0] === '[') {
        const am = rest.match(/^\[([\w-]+)(?:="([^"]*)")?\]/);
        if (!am) return null;
        const k = am[1], v = am[2];
        if (v === undefined) checks.push(el => el.hasAttribute(k));
        else checks.push(el => el.getAttribute(k) === v);
        rest = rest.slice(am[0].length);
      } else if (rest.startsWith(':not(')) {
        const nm = rest.match(/^:not\(([^)]+)\)/);
        if (!nm) return null;
        const inner = parseSimple(nm[1]);
        if (!inner) return null;
        checks.push(el => !inner(el));
        rest = rest.slice(nm[0].length);
      } else {
        return null;
      }
    }
    return el => {
      if (!el || el.nodeType !== 1) return false;
      if (tag && tag !== '*' && el.tagName !== tag) return false;
      return checks.every(fn => fn(el));
    };
  }

  function matchComplex(el, sel, scopeEl) {
    return sel.split(',').some(part => {
      let s = part.trim();
      if (!s) return false;
      let requireScopeRoot = false;
      if (s.startsWith(':scope > ')) {
        requireScopeRoot = true;
        s = s.slice(':scope > '.length).trim();
      }
      // Split into tokens alternating simple selector and combinator ('>' or ' ').
      const tokens = [];
      let buf = '';
      let inBracket = false;
      for (let i = 0; i < s.length; i++) {
        const ch = s[i];
        if (ch === '[') inBracket = true;
        else if (ch === ']') inBracket = false;
        if (!inBracket && (ch === '>' || /\s/.test(ch))) {
          if (buf) { tokens.push(buf); buf = ''; }
          if (ch === '>') {
            tokens.push('>');
          } else if (tokens.length && tokens[tokens.length - 1] !== '>' && tokens[tokens.length - 1] !== ' ') {
            tokens.push(' ');
          }
          continue;
        }
        buf += ch;
      }
      if (buf) tokens.push(buf);
      while (tokens.length && tokens[tokens.length - 1] === ' ') tokens.pop();
      // Collapse ' > ' patterns into single '>' combinator.
      const clean = [];
      for (let i = 0; i < tokens.length; i++) {
        if (tokens[i] === ' ' && (tokens[i - 1] === '>' || tokens[i + 1] === '>')) continue;
        clean.push(tokens[i]);
      }
      function walk(node, idx) {
        const fn = parseSimple(clean[idx]);
        if (!fn || !fn(node)) return false;
        if (idx === 0) {
          return !requireScopeRoot || node.parentNode === scopeEl;
        }
        const comb = clean[idx - 1];
        if (comb === '>') {
          return !!node.parentNode && walk(node.parentNode, idx - 2);
        }
        let p = node.parentNode;
        while (p && p.nodeType === 1) {
          if (walk(p, idx - 2)) return true;
          p = p.parentNode;
        }
        return false;
      }
      return walk(el, clean.length - 1);
    });
  }

  class El {
    constructor(tag, cls) {
      this.tagName = String(tag).toUpperCase();
      this.nodeType = 1;
      this.children = [];
      this.parentNode = null;
      this.attrs = {};
      this.cls = new Set(cls || []);
      this._text = '';
      this._html = '';
      this._hidden = false;
      this._disabled = false;
      this._open = false;
      this.listeners = {};
      this.props = new Map();
      this.scrollTop = 0;
      this.scrollLeft = 0;
      this.offsetWidth = 640;
      this.offsetHeight = 480;
      this.clientWidth = 640;
      this.clientHeight = 480;
      this.contentWindow = this.tagName === 'IFRAME' ? { id: Symbol('win') } : null;
      const self = this;
      this.style = new Proxy({}, {
        get(_, k) {
          if (k === 'setProperty') return (p, v) => { self.props.set(String(p), String(v)); };
          if (k === 'getPropertyValue') return p => self.props.get(String(p)) || '';
          if (k === 'removeProperty') return p => { self.props.delete(String(p)); };
          return self.props.get(String(k)) || '';
        },
        set(_, k, v) {
          if (v === '' || v == null) self.props.delete(String(k));
          else self.props.set(String(k), String(v));
          return true;
        }
      });
      this.classList = {
        add: (...cs) => cs.forEach(c => { if (c) self.cls.add(c); }),
        remove: (...cs) => cs.forEach(c => self.cls.delete(c)),
        contains: c => self.cls.has(c),
        toggle: (c, on) => {
          const next = on === undefined ? !self.cls.has(c) : !!on;
          if (next) self.cls.add(c); else self.cls.delete(c);
          return next;
        }
      };
    }
    get childNodes() { return this.children; }
    get isConnected() { return root.contains(this); }
    get firstChild() { return this.children[0] || null; }
    get firstElementChild() { return this.children[0] || null; }
    get lastChild() { return this.children[this.children.length - 1] || null; }
    get lastElementChild() { return this.children[this.children.length - 1] || null; }
    get nextSibling() {
      if (!this.parentNode) return null;
      const i = this.parentNode.children.indexOf(this);
      return i >= 0 ? (this.parentNode.children[i + 1] || null) : null;
    }
    get nextElementSibling() { return this.nextSibling; }
    get previousSibling() {
      if (!this.parentNode) return null;
      const i = this.parentNode.children.indexOf(this);
      return i > 0 ? this.parentNode.children[i - 1] : null;
    }
    get previousElementSibling() { return this.previousSibling; }
    get id() { return this.attrs.id || ''; }
    set id(v) { this.attrs.id = String(v); }
    get className() { return Array.from(this.cls).join(' '); }
    set className(v) { this.cls = new Set(String(v).split(/\s+/).filter(Boolean)); }
    get hidden() { return this._hidden; }
    set hidden(v) { this._hidden = !!v; }
    get disabled() { return this._disabled; }
    set disabled(v) { this._disabled = !!v; }
    get open() { return this.hasAttribute('open'); }
    set open(v) { if (v) this.setAttribute('open', ''); else this.removeAttribute('open'); }
    get title() { return this.attrs.title || ''; }
    set title(v) { this.attrs.title = String(v); }
    get src() { return this.attrs.src || ''; }
    set src(v) { this.attrs.src = String(v); }
    get type() { return this.attrs.type || ''; }
    set type(v) { this.attrs.type = String(v); }
    get textContent() {
      if (this.children.length) return this.children.map(c => c.textContent).join('');
      return this._text;
    }
    set textContent(v) {
      this.children.forEach(c => { c.parentNode = null; });
      this.children = [];
      this._text = v == null ? '' : String(v);
    }
    get innerHTML() { return this._html; }
    set innerHTML(v) {
      this._html = String(v);
      if (this._html === '') {
        this.children.forEach(c => { c.parentNode = null; });
        this.children = [];
      }
    }
    setAttribute(k, v) {
      k = String(k);
      v = String(v);
      if (k === 'class') this.className = v;
      else this.attrs[k] = v;
    }
    getAttribute(k) {
      k = String(k);
      if (k === 'class') return this.className;
      return Object.prototype.hasOwnProperty.call(this.attrs, k) ? this.attrs[k] : null;
    }
    hasAttribute(k) {
      k = String(k);
      if (k === 'class') return this.cls.size > 0;
      return Object.prototype.hasOwnProperty.call(this.attrs, k);
    }
    removeAttribute(k) {
      k = String(k);
      if (k === 'class') this.cls.clear();
      else delete this.attrs[k];
    }
    appendChild(ch) {
      if (!ch) return ch;
      if (ch.nodeType === 11) {
        ch.children.slice().forEach(n => this.appendChild(n));
        return ch;
      }
      if (ch.parentNode) ch.parentNode.removeChild(ch);
      ch.parentNode = this;
      this.children.push(ch);
      return ch;
    }
    insertBefore(ch, ref) {
      if (!ch) return ch;
      if (!ref) return this.appendChild(ch);
      if (ch.nodeType === 11) {
        ch.children.slice().forEach(n => this.insertBefore(n, ref));
        return ch;
      }
      if (ch.parentNode) ch.parentNode.removeChild(ch);
      const i = this.children.indexOf(ref);
      ch.parentNode = this;
      if (i < 0) this.children.push(ch);
      else this.children.splice(i, 0, ch);
      return ch;
    }
    removeChild(ch) {
      const i = this.children.indexOf(ch);
      if (i >= 0) { this.children.splice(i, 1); ch.parentNode = null; }
      return ch;
    }
    remove() { if (this.parentNode) this.parentNode.removeChild(this); }
    replaceWith(...nodes) {
      if (!this.parentNode) return;
      const p = this.parentNode;
      nodes.forEach(n => p.insertBefore(n, this));
      p.removeChild(this);
    }
    contains(n) {
      let cur = n;
      while (cur) {
        if (cur === this) return true;
        cur = cur.parentNode;
      }
      return false;
    }
    matches(sel) { return matchComplex(this, sel, this.parentNode); }
    closest(sel) {
      let cur = this;
      while (cur && cur.nodeType === 1) {
        if (matchComplex(cur, sel, cur.parentNode)) return cur;
        cur = cur.parentNode;
      }
      return null;
    }
    querySelectorAll(sel) {
      const out = [];
      const walk = node => {
        for (const ch of node.children) {
          if (matchComplex(ch, sel, this)) out.push(ch);
          walk(ch);
        }
      };
      walk(this);
      return out;
    }
    querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
    addEventListener(type, fn, opts) {
      const capture = typeof opts === 'boolean' ? opts : !!(opts && opts.capture);
      (this.listeners[type] || (this.listeners[type] = [])).push({ fn, capture });
    }
    removeEventListener(type, fn, opts) {
      const capture = typeof opts === 'boolean' ? opts : !!(opts && opts.capture);
      const list = this.listeners[type];
      if (!list) return;
      this.listeners[type] = list.filter(item => item.fn !== fn || item.capture !== capture);
    }
    dispatchEvent(ev) {
      ev.target = ev.target || this;
      if (typeof ev.preventDefault !== 'function') ev.preventDefault = () => { ev.defaultPrevented = true; };
      if (typeof ev.stopPropagation !== 'function') ev.stopPropagation = () => { ev._stopped = true; };
      const path = [];
      let cur = this;
      while (cur) { path.push(cur); cur = cur.parentNode; }
      // Capture phase: doc then ancestors down to target.
      if (docListeners[ev.type]) {
        docListeners[ev.type].filter(l => l.capture).forEach(l => {
          if (!ev._stopped) { ev.currentTarget = doc; l.fn(ev); }
        });
      }
      for (let i = path.length - 1; i >= 0; i--) {
        const node = path[i];
        if (ev._stopped) break;
        const list = (node.listeners[ev.type] || []).filter(l => l.capture);
        for (const l of list) {
          if (ev._stopped) break;
          ev.currentTarget = node;
          l.fn(ev);
        }
      }
      // Bubble phase: target up through ancestors to doc.
      for (let i = 0; i < path.length; i++) {
        const node = path[i];
        if (ev._stopped) break;
        const list = (node.listeners[ev.type] || []).filter(l => !l.capture);
        for (const l of list) {
          if (ev._stopped) break;
          ev.currentTarget = node;
          l.fn(ev);
        }
      }
      if (!ev._stopped && docListeners[ev.type]) {
        docListeners[ev.type].filter(l => !l.capture).forEach(l => {
          if (!ev._stopped) { ev.currentTarget = doc; l.fn(ev); }
        });
      }
      return !ev.defaultPrevented;
    }
    getBoundingClientRect() { return { top: 20, left: 20, right: 660, bottom: 500, width: 640, height: 480 }; }
    focus() {}
    scrollTo(opts) { if (opts && typeof opts.top === 'number') this.scrollTop = opts.top; }
  }

  class Frag {
    constructor() { this.nodeType = 11; this.children = []; this.parentNode = null; }
    appendChild(ch) {
      if (ch.parentNode) ch.parentNode.removeChild(ch);
      ch.parentNode = this;
      this.children.push(ch);
      return ch;
    }
  }

  const root = new El('html');
  const head = new El('head');
  const body = new El('body');
  const board = new El('main');
  board.id = 'r-board';
  root.appendChild(head);
  root.appendChild(body);
  body.appendChild(board);
  root.setAttribute('data-rid', 's1');

  const doc = {
    documentElement: root,
    head,
    body,
    activeElement: null,
    createElement: tag => new El(tag),
    createElementNS: (_, tag) => new El(tag),
    createDocumentFragment: () => new Frag(),
    querySelector: sel => root.querySelector(sel),
    querySelectorAll: sel => root.querySelectorAll(sel),
    getElementById: id => root.querySelector('#' + id),
    addEventListener: (type, fn, opts) => {
      const capture = typeof opts === 'boolean' ? opts : !!(opts && opts.capture);
      (docListeners[type] || (docListeners[type] = [])).push({ fn, capture });
    },
    removeEventListener: (type, fn, opts) => {
      const capture = typeof opts === 'boolean' ? opts : !!(opts && opts.capture);
      const list = docListeners[type];
      if (!list) return;
      docListeners[type] = list.filter(i => i.fn !== fn || i.capture !== capture);
    },
    dispatchEvent: ev => {
      ev.target = ev.target || doc;
      if (typeof ev.preventDefault !== 'function') ev.preventDefault = () => { ev.defaultPrevented = true; };
      if (typeof ev.stopPropagation !== 'function') ev.stopPropagation = () => { ev._stopped = true; };
      (docListeners[ev.type] || []).forEach(l => { ev.currentTarget = doc; l.fn(ev); });
      return !ev.defaultPrevented;
    }
  };

  class FakeEvent {
    constructor(type, init = {}) {
      this.type = type;
      this.bubbles = init.bubbles !== false;
      this.defaultPrevented = false;
      this._stopped = false;
      Object.assign(this, init);
    }
    preventDefault() { this.defaultPrevented = true; }
    stopPropagation() { this._stopped = true; }
  }

  const win = {
    document: doc,
    Event: FakeEvent,
    Element: El,
    Map,
    Set,
    WeakMap,
    Promise,
    JSON,
    Math,
    Object,
    Array,
    Number,
    String,
    parseFloat,
    isFinite,
    setTimeout,
    clearTimeout,
    requestAnimationFrame: fn => { fn(0); return 1; },
    cancelAnimationFrame: () => {},
    getComputedStyle: () => ({ paddingTop: '0px' }),
    matchMedia: () => ({ matches: false }),
    wsT: (key, arg) => (arg !== undefined ? key + ':' + arg : key),
    fetch: () => Promise.reject(new Error('unexpected fetch')),
    addEventListener: (type, fn, opts) => {
      const capture = typeof opts === 'boolean' ? opts : !!(opts && opts.capture);
      (winListeners[type] || (winListeners[type] = [])).push({ fn, capture });
    },
    removeEventListener: (type, fn, opts) => {
      const capture = typeof opts === 'boolean' ? opts : !!(opts && opts.capture);
      const list = winListeners[type];
      if (!list) return;
      winListeners[type] = list.filter(i => i.fn !== fn || i.capture !== capture);
    },
    dispatchMessage: (data, source) => {
      const ev = { data, source, type: 'message' };
      (winListeners.message || []).forEach(l => l.fn(ev));
    }
  };
  win.window = win;
  win.self = win;
  win.parent = win;

  const ctx = vm.createContext(win);
  vm.runInContext(gallerySrc, ctx);
  vm.runInContext(cardsSrc, ctx);
  vm.runInContext(panelcardSrc, ctx);
  vm.runInContext(previewSrc, ctx);

  let saved = {};
  ctx.WsCards.mount(board, {
    key: 'test-cards',
    load: () => saved,
    save: v => { saved = v; },
    host: body,
    mode: () => ({ width: 1200, tabs: false }),
    layout: () => {},
    toolbar: { overview: () => {} }
  });
  if (ctx.WsPanelCard && ctx.WsPanelCard.setup) {
    ctx.WsPanelCard.setup({
      mountFrames: container => {
        container.querySelectorAll('div.doc-frame[data-mockup-src], div.doc-frame[data-srcdoc]').forEach(ph => {
          const fr = doc.createElement('iframe');
          fr.className = ph.className;
          for (const [k, v] of Object.entries(ph.attrs)) fr.setAttribute(k, v);
          if (ph.hasAttribute('data-mockup-src')) {
            fr.setAttribute('src', ph.getAttribute('data-mockup-src'));
            fr.removeAttribute('data-mockup-src');
          }
          if (ph.hasAttribute('data-srcdoc')) {
            fr.setAttribute('srcdoc', ph.getAttribute('data-srcdoc'));
            fr.removeAttribute('data-srcdoc');
          }
          ph.replaceWith(fr);
          if (ph.hasAttribute('data-mockup-src') && ctx.WsCards.syncMockupElement) {
            ctx.WsCards.syncMockupElement(fr);
          }
        });
      }
    });
  }

  return { El, FakeEvent, doc, root, body, board, win: ctx };
}

function zoomButtons(grp) {
  return [
    grp.querySelector('.d-zoom-out'),
    grp.querySelector('.d-zoom-pct'),
    grp.querySelector('.d-zoom-in')
  ];
}

function makeTopicPanel(dom, { topicName, entryID, panelKey, docKey, isMockup, open = true, historical = false }) {
  const { doc } = dom;
  let sec = doc.querySelector('section.topic[data-topic="' + topicName + '"]');
  if (!sec) {
    sec = doc.createElement('section');
    sec.className = 'topic';
    sec.setAttribute('data-topic', topicName);
    const tophead = doc.createElement('div');
    tophead.className = 'tophead';
    const tbar = doc.createElement('div');
    tbar.className = 'tbar';
    tbar.textContent = topicName;
    tophead.appendChild(tbar);
    sec.appendChild(tophead);
    dom.board.appendChild(sec);
  }
  const art = doc.createElement('article');
  art.className = 'wb-entry panel' + (historical ? ' historical' : '');
  art.setAttribute('data-panel', panelKey);
  art.setAttribute('id', entryID);

  const det = doc.createElement('details');
  det.className = 'entry-panel';
  det.setAttribute('data-key', panelKey);
  det.open = open;

  const sum = doc.createElement('summary');
  const titleSpan = doc.createElement('span');
  titleSpan.className = 'panel-title';
  titleSpan.textContent = isMockup ? 'Mockup ' + docKey : 'Markdown ' + docKey;
  const maxBtn = doc.createElement('button');
  maxBtn.className = 'ibtn d-max';
  maxBtn.type = 'button';
  sum.appendChild(titleSpan);
  sum.appendChild(maxBtn);
  det.appendChild(sum);

  const docBody = doc.createElement('div');
  docBody.className = 'doc-body';
  const wrap = doc.createElement('div');
  wrap.className = isMockup ? 'mockup-wrap' : 'doc-wrap';
  const iframe = doc.createElement('iframe');
  iframe.className = isMockup ? 'doc-frame mockup-frame' : 'doc-frame';
  iframe.setAttribute('data-doc-key', docKey);
  if (isMockup) {
    iframe.setAttribute('src', '/s/s1/board/mockup?entry=' + entryID);
  } else {
    iframe.setAttribute('srcdoc', '<h1>Markdown Doc</h1>');
  }
  wrap.appendChild(iframe);
  docBody.appendChild(wrap);
  det.appendChild(docBody);
  art.appendChild(det);
  sec.appendChild(art);

  return { sec, art, det, sum, maxBtn, docBody, wrap, iframe };
}

test('whiteboard mockup panel zoom controls, summary placement, propagation guard, bounds, and markdown exclusion', () => {
  const dom = makeDomEnv();
  const { win, FakeEvent } = dom;
  const liveMock = makeTopicPanel(dom, {
    topicName: 't-alpha',
    entryID: 'e1',
    panelKey: 'p-live',
    docKey: 'doc-live-1',
    isMockup: true,
    open: true,
    historical: false
  });
  const histMock = makeTopicPanel(dom, {
    topicName: 't-alpha',
    entryID: 'e2',
    panelKey: 'p-hist',
    docKey: 'doc-hist-2',
    isMockup: true,
    open: true,
    historical: true
  });
  const mdPanel = makeTopicPanel(dom, {
    topicName: 't-alpha',
    entryID: 'e3',
    panelKey: 'p-md',
    docKey: 'doc-md-3',
    isMockup: false,
    open: true,
    historical: false
  });

  win.WsCards.decorate();

  // Live mockup panel gets .d-zoom-grp inserted immediately before .d-max and .ppin after title.
  const grp = liveMock.sum.querySelector('.d-zoom-grp');
  assert.ok(grp, 'live mockup summary receives .d-zoom-grp');
  assert.equal(liveMock.det.getAttribute('data-zoom-ctrl'), '');
  assert.equal(liveMock.det.getAttribute('data-zoom-pin'), '');
  assert.equal(grp.nextSibling, liveMock.maxBtn, '.d-zoom-grp sits right before .d-max');

  // Historical mockup panel ALSO gets .d-zoom-grp (with data-zoom-ctrl, without data-zoom-pin since historical panels have no .ppin).
  const histGrp = histMock.sum.querySelector('.d-zoom-grp');
  assert.ok(histGrp, 'historical mockup summary still receives .d-zoom-grp');
  assert.equal(histMock.det.getAttribute('data-zoom-ctrl'), '');
  assert.equal(histMock.det.hasAttribute('data-zoom-pin'), false);

  // Markdown srcdoc panel NEVER gets zoom controls or data-zoom.
  assert.equal(mdPanel.sum.querySelector('.d-zoom-grp'), null, 'srcdoc panel must not get .d-zoom-grp');
  assert.equal(mdPanel.wrap.hasAttribute('data-zoom'), false);
  assert.equal(mdPanel.wrap.style.getPropertyValue('--wb-zoom'), '');

  // Stepping zoom up, down, to bounds, and resetting.
  const [btnOut, btnPct, btnIn] = zoomButtons(grp);
  assert.equal(btnPct.textContent, '100%');
  assert.equal(btnOut.disabled, false);
  assert.equal(btnIn.disabled, false);

  const clickEv = new FakeEvent('click');
  btnIn.dispatchEvent(clickEv);
  assert.equal(clickEv.defaultPrevented, true, 'zoom button click prevents summary toggle');
  assert.equal(clickEv._stopped, true, 'zoom button click stops propagation');
  assert.equal(btnPct.textContent, '125%');
  assert.equal(liveMock.wrap.getAttribute('data-zoom'), '125');
  assert.equal(liveMock.wrap.style.getPropertyValue('--wb-zoom'), '1.25');

  // Step up to 200% upper bound -> + button disabled.
  btnIn.dispatchEvent(new FakeEvent('click'));
  btnIn.dispatchEvent(new FakeEvent('click'));
  assert.equal(btnPct.textContent, '200%');
  assert.equal(liveMock.wrap.getAttribute('data-zoom'), '200');
  assert.equal(liveMock.wrap.style.getPropertyValue('--wb-zoom'), '2');
  assert.equal(btnIn.disabled, true);
  assert.equal(btnOut.disabled, false);

  // Clicking readout resets cleanly to 100% and removes data-zoom / --wb-zoom.
  btnPct.dispatchEvent(new FakeEvent('click'));
  assert.equal(btnPct.textContent, '100%');
  assert.equal(liveMock.wrap.hasAttribute('data-zoom'), false);
  assert.equal(liveMock.wrap.style.getPropertyValue('--wb-zoom'), '');

  // Step down to 25% lower bound -> - button disabled.
  for (let i = 0; i < 10; i++) btnOut.dispatchEvent(new FakeEvent('click'));
  assert.equal(btnPct.textContent, '25%');
  assert.equal(liveMock.wrap.getAttribute('data-zoom'), '25');
  assert.equal(liveMock.wrap.style.getPropertyValue('--wb-zoom'), '0.25');
  assert.equal(btnOut.disabled, true);
  assert.equal(btnIn.disabled, false);

  // Maximized mockup panel retains zoom controls and toggles data-has-sheet-width cleanly.
  liveMock.maxBtn.dispatchEvent(new FakeEvent('click'));
  assert.equal(liveMock.det.hasAttribute('data-max'), true);
  assert.equal(liveMock.wrap.getAttribute('data-zoom'), '25');
  assert.ok(liveMock.sum.querySelector('.d-zoom-grp'));
  dom.doc.dispatchEvent(new FakeEvent('keydown', { key: 'Escape' }));
  assert.equal(liveMock.det.hasAttribute('data-max'), false);
  assert.equal(liveMock.det.hasAttribute('data-has-sheet-width'), false);
});

test('WsPanelCard gallery card mirrors zoom with live panel and omits controls for non-mockup cards', () => {
  const dom = makeDomEnv();
  const { win, FakeEvent } = dom;
  const mock = makeTopicPanel(dom, {
    topicName: 't-sync',
    entryID: 'e10',
    panelKey: 'p-sync',
    docKey: 'shared-mock-key',
    isMockup: true,
    open: true
  });
  const md = makeTopicPanel(dom, {
    topicName: 't-sync',
    entryID: 'e11',
    panelKey: 'p-md',
    docKey: 'shared-md-key',
    isMockup: false,
    open: true
  });
  win.WsCards.decorate();

  const mockCard = win.WsPanelCard.toggle('p-sync', 't-sync', mock.art);
  const mdCard = win.WsPanelCard.toggle('p-md', 't-sync', md.art);
  assert.ok(mockCard && mdCard);

  // Check WsPanelCard header actions (.bc-acts).
  const mockActs = mockCard.el.querySelector('.bc-acts');
  const mockCardGrp = mockActs.querySelector('.d-zoom-grp');
  assert.ok(mockCardGrp, 'WsPanelCard for mockup gets .d-zoom-grp in .bc-acts');
  assert.equal(mdCard.el.querySelector('.bc-acts .d-zoom-grp'), null, 'WsPanelCard for markdown srcdoc omits .d-zoom-grp');

  const cardWrap = mockCard.body.querySelector('.mockup-wrap');
  const cardIframe = mockCard.body.querySelector('iframe.mockup-frame');
  assert.ok(cardWrap && cardIframe);
  const srcBefore = mock.iframe.src;
  const cardSrcBefore = cardIframe.src;

  // Adjust zoom on panel card -> live panel mirrors immediately without reloading iframe src.
  const [, cardPct, cardIn] = zoomButtons(mockCardGrp);
  cardIn.dispatchEvent(new FakeEvent('click'));
  assert.equal(cardPct.textContent, '125%');
  assert.equal(cardWrap.getAttribute('data-zoom'), '125');
  assert.equal(mock.sum.querySelector('.d-zoom-pct').textContent, '125%');
  assert.equal(mock.wrap.getAttribute('data-zoom'), '125');
  assert.equal(mock.wrap.style.getPropertyValue('--wb-zoom'), '1.25');
  assert.equal(mock.iframe.src, srcBefore, 'live mockup src unchanged on zoom');
  assert.equal(cardIframe.src, cardSrcBefore, 'card mockup src unchanged on zoom');

  // Re-syncing a freshly morphed / kept frame element via syncMockupElement preserves scale without reload.
  const keptWrap = dom.doc.createElement('div');
  keptWrap.className = 'mockup-wrap';
  const keptFrame = dom.doc.createElement('iframe');
  keptFrame.className = 'doc-frame mockup-frame';
  keptFrame.setAttribute('data-doc-key', 'shared-mock-key');
  keptWrap.appendChild(keptFrame);
  win.WsCards.syncMockupElement(keptFrame);
  assert.equal(keptWrap.getAttribute('data-zoom'), '125');
  assert.equal(keptWrap.style.getPropertyValue('--wb-zoom'), '1.25');
});

test('preview.js HTML capability card and createPane wrap stage, expose .d-zoom-grp, and carry zoom across Keep', async () => {
  const dom = makeDomEnv();
  const { win, FakeEvent, doc } = dom;
  win.WsPreview.setup({});

  const paneEl = doc.createElement('section');
  paneEl.className = 'file-preview-pane';
  dom.body.appendChild(paneEl);
  let closedCount = 0;
  const pane = win.WsPreview.createPane(paneEl, () => { closedCount++; });

  const frameAnswer = { kind: 'frame', frame: '/s/s1/fs/cap/abc/demo.html', mime: 'text/html' };
  pane.open('demo.html', { answer: frameAnswer });
  await new Promise(r => setTimeout(r, 0));

  const paneCap = paneEl.querySelector('.pv-cap');
  const paneStage = paneEl.querySelector('.pv-sand-stage');
  const paneFrame = paneStage && paneStage.querySelector('iframe');
  assert.ok(paneCap && paneCap.querySelector('.pv-cap-txt'), '.pv-cap contains .pv-cap-txt');
  assert.ok(paneStage && paneFrame, 'iframe wrapped inside .pv-sand-stage');
  assert.equal(paneFrame.getAttribute('sandbox'), 'allow-scripts');

  const paneGrp = paneCap.querySelector('.d-zoom-grp');
  assert.ok(paneGrp, 'pane HTML preview gets .d-zoom-grp');
  const [, panePct, paneIn] = zoomButtons(paneGrp);
  paneIn.dispatchEvent(new FakeEvent('click'));
  paneIn.dispatchEvent(new FakeEvent('click'));
  assert.equal(panePct.textContent, '150%');
  assert.equal(paneStage.getAttribute('data-zoom'), '150');
  assert.equal(paneStage.style.getPropertyValue('--wb-zoom'), '1.5');

  // Seed cached answer for Keep transfer so WsPreview.open opens card with same zoom.
  win.fetch = () => Promise.resolve({ ok: true, json: () => Promise.resolve(frameAnswer) });
  const keepBtn = paneEl.querySelector('.file-preview-head .btn');
  keepBtn.dispatchEvent(new FakeEvent('click'));
  assert.equal(closedCount, 1);
  await new Promise(r => setTimeout(r, 0));

  const cards = win.WsCards.list('preview');
  assert.equal(cards.length, 1);
  const card = cards[0];
  const cardStage = card.body.querySelector('.pv-sand-stage');
  const cardPct = card.body.querySelector('.pv-cap .d-zoom-pct');
  assert.ok(cardStage && cardPct);
  assert.equal(cardPct.textContent, '150%', 'pane zoom transfers to kept preview card');
  assert.equal(cardStage.getAttribute('data-zoom'), '150');
  assert.equal(cardStage.style.getPropertyValue('--wb-zoom'), '1.5');

  // Text/markdown preview does NOT get zoom controls or .pv-sand-stage.
  win.WsPreview.open('notes.md', { keep: true, answer: { kind: 'markdown', html: '<p>hi</p>' } });
  await new Promise(r => setTimeout(r, 0));
  const mdCard = win.WsCards.list('preview')[1];
  assert.equal(mdCard.body.querySelector('.d-zoom-grp'), null);
  assert.equal(mdCard.body.querySelector('.pv-sand-stage'), null);
});

test('Ctrl/Cmd + wheel zoom on host chrome & containers steps zoom cleanly and ignores non-modifier wheel', async () => {
  const dom = makeDomEnv();
  const { win, FakeEvent, doc } = dom;
  win.WsPreview.setup({});

  // Mockup panel: closed ignores Ctrl+wheel, open steps with Ctrl and Cmd (metaKey), ignores plain wheel & md panel.
  const openMock = makeTopicPanel(dom, {
    topicName: 't-wheel',
    entryID: 'ew1',
    panelKey: 'pw1',
    docKey: 'wheel-mock-key',
    isMockup: true,
    open: true
  });
  const closedMock = makeTopicPanel(dom, {
    topicName: 't-wheel',
    entryID: 'ew2',
    panelKey: 'pw2',
    docKey: 'wheel-closed-key',
    isMockup: true,
    open: false
  });
  const md = makeTopicPanel(dom, {
    topicName: 't-wheel',
    entryID: 'ew3',
    panelKey: 'pw3',
    docKey: 'wheel-md-key',
    isMockup: false,
    open: true
  });
  win.WsCards.decorate();

  const noMod = new FakeEvent('wheel', { ctrlKey: false, metaKey: false, deltaY: -120 });
  openMock.wrap.dispatchEvent(noMod);
  assert.equal(noMod.defaultPrevented, false);
  assert.equal(openMock.wrap.hasAttribute('data-zoom'), false);

  const ctrlIn = new FakeEvent('wheel', { ctrlKey: true, deltaY: -120 });
  openMock.sum.dispatchEvent(ctrlIn);
  assert.equal(ctrlIn.defaultPrevented, true);
  assert.equal(openMock.wrap.getAttribute('data-zoom'), '125');

  const metaOut = new FakeEvent('wheel', { metaKey: true, deltaY: 120 });
  openMock.wrap.dispatchEvent(metaOut);
  assert.equal(metaOut.defaultPrevented, true);
  assert.equal(openMock.wrap.hasAttribute('data-zoom'), false);

  const zeroDelta = new FakeEvent('wheel', { ctrlKey: true, deltaY: 0 });
  openMock.wrap.dispatchEvent(zeroDelta);
  assert.equal(zeroDelta.defaultPrevented, true);
  assert.equal(openMock.wrap.hasAttribute('data-zoom'), false);

  // Closed mockup panel does not zoom on Ctrl+wheel.
  const closedWheel = new FakeEvent('wheel', { ctrlKey: true, deltaY: -100 });
  closedMock.sum.dispatchEvent(closedWheel);
  assert.equal(closedWheel.defaultPrevented, false);
  assert.equal(closedMock.wrap.hasAttribute('data-zoom'), false);

  // Markdown srcdoc panel does not zoom on Ctrl+wheel.
  const mdWheel = new FakeEvent('wheel', { ctrlKey: true, deltaY: -100 });
  md.wrap.dispatchEvent(mdWheel);
  assert.equal(mdWheel.defaultPrevented, false);

  // Preview card host chrome (.bc-hd) and .pv-sand handle Ctrl/Cmd + wheel without double-stepping when bubbling.
  win.WsPreview.open('app.html', { keep: true, answer: { kind: 'frame', frame: '/s/s1/fs/cap/x/app.html' } });
  await new Promise(r => setTimeout(r, 0));
  const card = win.WsCards.list('preview')[0];
  const sand = card.body.querySelector('.pv-sand');
  const stage = card.body.querySelector('.pv-sand-stage');
  const hd = card.el.querySelector('.bc-hd');

  const sandWheel = new FakeEvent('wheel', { ctrlKey: true, deltaY: -120 });
  sandWheel.preventDefault = () => {}; // Intentionally keep defaultPrevented=false to verify _wsZoomHandled dedup.
  sand.dispatchEvent(sandWheel);
  assert.equal(stage.getAttribute('data-zoom'), '125', 'bubbling from .pv-sand to card.el must step once, not twice');

  const hdWheel = new FakeEvent('wheel', { metaKey: true, deltaY: -120 });
  hd.dispatchEvent(hdWheel);
  assert.equal(hdWheel.defaultPrevented, true);
  assert.equal(stage.getAttribute('data-zoom'), '150');

  // Non-frame preview card does not preventDefault Ctrl+wheel.
  win.WsPreview.open('plain.txt', { keep: true, answer: { kind: 'text', text: 'hello' } });
  await new Promise(r => setTimeout(r, 0));
  const txtCard = win.WsCards.list('preview')[1];
  const txtWheel = new FakeEvent('wheel', { ctrlKey: true, deltaY: -120 });
  txtCard.body.dispatchEvent(txtWheel);
  assert.equal(txtWheel.defaultPrevented, false);
});

test('window message listener steps/resets ONLY for matching e.source === iframe.contentWindow', async () => {
  const dom = makeDomEnv();
  const { win } = dom;
  win.WsPreview.setup({});

  const mock = makeTopicPanel(dom, {
    topicName: 't-msg',
    entryID: 'em1',
    panelKey: 'pm1',
    docKey: 'msg-mock-key',
    isMockup: true,
    open: true
  });
  const md = makeTopicPanel(dom, {
    topicName: 't-msg',
    entryID: 'em2',
    panelKey: 'pm2',
    docKey: 'msg-md-key',
    isMockup: false,
    open: true
  });
  win.WsCards.decorate();

  win.WsPreview.open('embed.html', { keep: true, answer: { kind: 'frame', frame: '/s/s1/fs/cap/y/embed.html' } });
  await new Promise(r => setTimeout(r, 0));
  const pvCard = win.WsCards.list('preview')[0];
  const pvStage = pvCard.body.querySelector('.pv-sand-stage');
  const pvIframe = pvStage.querySelector('iframe');

  // Foreign / null / markdown sources and bogus payloads are ignored.
  const foreignWin = { id: Symbol('foreign') };
  win.dispatchMessage({ wsPreviewZoom: 1 }, null);
  win.dispatchMessage({ wsPreviewZoom: 1 }, foreignWin);
  win.dispatchMessage({ wsPreviewZoom: 1 }, md.iframe.contentWindow);
  win.dispatchMessage({ wsPreviewZoom: 99 }, mock.iframe.contentWindow);
  win.dispatchMessage('wsPreviewZoom', pvIframe.contentWindow);
  assert.equal(mock.wrap.hasAttribute('data-zoom'), false);
  assert.equal(pvStage.hasAttribute('data-zoom'), false);

  // Matching mockup iframe source steps +1 then resets on 0 without affecting preview iframe.
  win.dispatchMessage({ wsPreviewZoom: 1 }, mock.iframe.contentWindow);
  assert.equal(mock.wrap.getAttribute('data-zoom'), '125');
  assert.equal(pvStage.hasAttribute('data-zoom'), false);

  win.dispatchMessage({ wsPreviewZoom: 0 }, mock.iframe.contentWindow);
  assert.equal(mock.wrap.hasAttribute('data-zoom'), false);

  // Matching preview card iframe source steps -1, +1, +1, then 0 reset.
  win.dispatchMessage({ wsPreviewZoom: -1 }, pvIframe.contentWindow);
  assert.equal(pvStage.getAttribute('data-zoom'), '90');
  win.dispatchMessage({ wsPreviewZoom: 1 }, pvIframe.contentWindow);
  win.dispatchMessage({ wsPreviewZoom: 1 }, pvIframe.contentWindow);
  assert.equal(pvStage.getAttribute('data-zoom'), '125');
  win.dispatchMessage({ wsPreviewZoom: 0 }, pvIframe.contentWindow);
  assert.equal(pvStage.hasAttribute('data-zoom'), false);
});

test('CSS and frame/morph asset contracts stay intact without new :has() rules', () => {
  assert.ok(boardCss.includes('.d-zoom-grp'), 'dashboard-board.css defines .d-zoom-grp');
  assert.ok(boardCss.includes('.pv-sand-stage'), 'dashboard-board.css defines .pv-sand-stage');
  assert.ok(boardCss.includes('.mockup-wrap[data-zoom] > iframe'), 'CSS scales mockup wrap iframe');
  assert.ok(boardCss.includes('.pv-sand-stage[data-zoom] > iframe'), 'CSS scales preview stage iframe');
  assert.equal(/summary:has\([^)]*d-zoom-grp/i.test(boardCss), false, 'no :has(.d-zoom-grp) selector in CSS');
  assert.ok(framesSrc.includes('WsCards.syncMockupElement'), 'mountFrame re-syncs mockup zoom');
  assert.ok(boardSrc.includes('WsCards.syncMockupElement'), 'frameKept re-syncs kept mockup zoom');
});
