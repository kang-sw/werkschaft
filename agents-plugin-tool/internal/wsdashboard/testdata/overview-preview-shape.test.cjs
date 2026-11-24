'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

const overviewSrc = readAssetSource('wsdashboard', 'overview.js');
const boardCSS = readAssetSource('wsdashboard', 'dashboard-board.css');

function makeMiniDom() {
  const stats = { rafs: 0 };

  function matchSingle(el, sel) {
    sel = sel.trim();
    if (!sel) return false;
    const m = sel.match(/^([a-z0-9]*)((?:[.#][\w-]+|\[[\w-]+(?:="[^"]*")?\])*)$/i);
    if (!m) return false;
    if (m[1] && el.tagName !== m[1].toUpperCase()) return false;
    const tokens = m[2].match(/[.#][\w-]+|\[[\w-]+(?:="[^"]*")?\]/g) || [];
    return tokens.every(tok => {
      if (tok[0] === '.') return el.classList.contains(tok.slice(1));
      if (tok[0] === '#') return el.getAttribute('id') === tok.slice(1);
      const eq = tok.indexOf('=');
      if (eq < 0) return el.hasAttribute(tok.slice(1, -1));
      const k = tok.slice(1, eq);
      const v = tok.slice(eq + 2, -2);
      return el.getAttribute(k) === v;
    });
  }

  class El {
    constructor(tag, attrs = {}, children = []) {
      this.nodeType = 1;
      this.tagName = tag.toUpperCase();
      this.attrs = {};
      this.children = [];
      this.parentNode = null;
      this.style = {};
      this.hidden = false;
      this.inert = false;
      this._text = attrs._text || '';
      this._rect = attrs._rect || { top: 20, bottom: 108, left: 0, right: 400, width: 400, height: 88 };
      this.listeners = {};
      for (const [k, v] of Object.entries(attrs)) {
        if (k === '_text' || k === '_rect') continue;
        this.attrs[k] = String(v);
      }
      const self = this;
      this.classList = {
        contains: c => (self.attrs.class || '').split(/\s+/).filter(Boolean).includes(c),
        add: c => {
          const parts = (self.attrs.class || '').split(/\s+/).filter(Boolean);
          if (!parts.includes(c)) parts.push(c);
          self.attrs.class = parts.join(' ');
        },
        remove: c => {
          self.attrs.class = (self.attrs.class || '').split(/\s+/).filter(x => x && x !== c).join(' ');
        },
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
      if (this.children.length === 0) return this._text;
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
      if (i < 0) this.children.push(n);
      else this.children.splice(i, 0, n);
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
      const copyAttrs = Object.assign({}, this.attrs);
      copyAttrs._text = this._text;
      copyAttrs._rect = Object.assign({}, this._rect);
      const clone = new El(this.tagName.toLowerCase(), copyAttrs);
      if (deep) {
        for (const ch of this.children) clone.appendChild(ch.cloneNode(true));
      }
      return clone;
    }
    querySelectorAll(sel) {
      const parts = sel.split(',').map(s => s.trim()).filter(Boolean);
      const hasChild = parts.some(s => s.includes(' > '));
      if (hasChild) {
        const out = [];
        for (const s of parts) {
          if (s.includes(' > ')) {
            const fromScope = s.startsWith(':scope > ');
            const chain = (fromScope ? s.slice(':scope > '.length) : s).split(' > ').map(x => x.trim());
            let cur = fromScope ? [this] : this.querySelectorAll(chain.shift());
            for (const seg of chain) {
              const next = [];
              for (const node of cur) {
                for (const ch of node.children) {
                  if (matchSingle(ch, seg)) next.push(ch);
                }
              }
              cur = next;
            }
            for (const hit of cur) if (!out.includes(hit)) out.push(hit);
          } else {
            for (const hit of this.querySelectorAll(s)) if (!out.includes(hit)) out.push(hit);
          }
        }
        return out;
      }
      const out = [];
      (function walk(node) {
        for (const ch of node.children) {
          if (parts.some(s => matchSingle(ch, s))) out.push(ch);
          walk(ch);
        }
      })(this);
      return out;
    }
    querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
    getBoundingClientRect() { return Object.assign({}, this._rect); }
    focus() { doc.activeElement = this; }
    addEventListener(type, fn) { (this.listeners[type] || (this.listeners[type] = [])).push(fn); }
    removeEventListener(type, fn) {
      if (!this.listeners[type]) return;
      this.listeners[type] = this.listeners[type].filter(f => f !== fn);
    }
  }

  const html = new El('html');
  const body = new El('body');
  html.appendChild(body);
  const docListeners = {};
  const doc = {
    documentElement: html,
    body,
    scrollingElement: html,
    activeElement: null,
    createElement: tag => new El(tag),
    getElementById: id => html.querySelector('#' + id),
    querySelector: sel => html.querySelector(sel),
    querySelectorAll: sel => html.querySelectorAll(sel),
    addEventListener: (t, fn) => { (docListeners[t] || (docListeners[t] = [])).push(fn); },
    dispatch: (t, evt) => { (docListeners[t] || []).forEach(fn => fn(evt)); },
  };
  return { El, html, body, doc, stats };
}

function collectIds(el, out = []) {
  if (el.hasAttribute('id')) out.push(el.getAttribute('id'));
  for (const ch of el.children) collectIds(ch, out);
  return out;
}

test('buildPlaceholder clones inert, id-free summary shell for both section.topic and .bcard', () => {
  const { El, body, doc, stats } = makeMiniDom();
  const grid = new El('main', { class: 'dgrid live' });
  const bcol = new El('div', { id: 'bcol' });
  const board = new El('div', { id: 'r-board', class: 'r-board' });
  const toolbar = new El('div', { class: 'board-toolbar', _rect: { top: -80, bottom: -40, height: 40 } }, [
    new El('button', { class: 'board-overview', _rect: { top: -80, bottom: -40, height: 40 } }),
  ]);
  const bgal = new El('div', { id: 'bgal', class: 'bgal' }, [
    new El('div', { class: 'gal-viewport' }),
    new El('div', { class: 'gal-rail' }),
  ]);
  bgal.hidden = true;

  const topic = new El('section', {
    class: 'topic quiet',
    id: 'topic-alpha',
    'data-topic': 'alpha',
    'data-state': 'discussing',
    'data-size': 'open',
    'data-board-held': '',
    _rect: { top: 20, bottom: 112, height: 92 },
  }, [
    new El('div', { class: 'tophead', id: 'th-alpha' }, [
      new El('div', { class: 'tbar', id: 'tb-alpha' }, [
        new El('span', { class: 'tname', id: 'tn-alpha', _text: 'Alpha Topic' }),
        new El('span', { class: 'wb-state-pill discuss', id: 'pill-alpha', _text: 'Discussing' }),
      ]),
      new El('div', { class: 'trecap', id: 'rc-alpha' }, [
        new El('div', { class: 'rtext', _text: 'Formatted recap text' }),
      ]),
    ]),
    new El('div', { class: 'tbody', id: 'body-alpha', _text: 'Full body should not be cloned' }),
  ]);

  const card = new El('div', {
    class: 'bcard',
    id: 'card-term-1',
    'data-card': 'term-1',
    'data-kind': 'terminal',
    'data-file-state': 'preview',
    'data-exited': '',
    'data-size': 'open',
    _rect: { top: 118, bottom: 154, height: 36 },
  }, [
    new El('div', { class: 'bc-in', id: 'bcin-term-1' }, [
      new El('div', { class: 'bc-hd', id: 'bchd-term-1' }, [
        new El('span', { class: 'bc-nm', id: 'bcnm-term-1', _text: 'Terminal 1' }),
        new El('span', { class: 'bc-kind', _text: 'TERM' }),
      ]),
      new El('div', { class: 'bc-body', id: 'bcbody-term-1', _text: 'xterm body should not be cloned' }),
    ]),
  ]);

  board.appendChild(toolbar);
  board.appendChild(topic);
  board.appendChild(card);
  bcol.appendChild(board);
  grid.appendChild(bcol);
  body.appendChild(grid);
  body.appendChild(bgal);

  const storage = {};
  const win = {
    innerWidth: 1600,
    innerHeight: 900,
    matchMedia: () => ({ matches: true }),
    WsCards: { isFourCol: () => true, relayout: () => {} },
    addEventListener: () => {},
    removeEventListener: () => {},
  };

  const ctx = vm.createContext({
    document: doc,
    window: win,
    sessionStorage: {
      getItem: k => (k in storage ? storage[k] : null),
      setItem: (k, v) => { storage[k] = String(v); },
      removeItem: k => { delete storage[k]; },
    },
    getComputedStyle: () => ({ overflowY: 'visible', marginBottom: '6px', scrollPaddingTop: '0', getPropertyValue: () => '' }),
    requestAnimationFrame: fn => { stats.rafs++; return 1; },
    performance: { now: () => 0 },
    innerHeight: 900,
    innerWidth: 1600,
    setTimeout: () => 1,
    clearTimeout: () => {},
    wsT: k => k,
    Event: function Event(type) { this.type = type; },
  });
  vm.runInContext(overviewSrc, ctx);

  win.WsOverview.mount({
    board,
    toolbar,
    rid: 'test-rid',
    place: () => {},
    keep: () => {},
  });

  // Enter Overview: topic-alpha immediately enters Quick Look preview.
  win.WsOverview.toggle();
  assert.equal(win.WsOverview.active(), true);
  assert.equal(stats.rafs, 0, 'reduced-motion Overview entry queues 0 rAF');

  const topicPh = board.querySelector('.overview-placeholder');
  assert.ok(topicPh, 'topic placeholder inserted into board');
  assert.equal(topicPh.style.height, '92px');
  assert.equal(topicPh.getAttribute('data-overview-target-topic'), 'alpha');
  assert.equal(topicPh.hasAttribute('data-topic'), false, 'placeholder must not carry data-topic');
  assert.equal(topicPh.classList.contains('overview-previewing'), true);
  assert.equal(topicPh.classList.contains('topic'), true);
  assert.equal(topicPh.classList.contains('quiet'), true);
  assert.equal(topicPh.classList.contains('overview-here'), true);
  assert.equal(topicPh.getAttribute('data-state'), 'discussing');
  assert.equal(topicPh.getAttribute('data-size'), 'open');
  assert.equal(topicPh.hasAttribute('data-board-held'), true);

  const clonedHead = topicPh.querySelector(':scope > .tophead');
  assert.ok(clonedHead, 'topic placeholder clones :scope > .tophead');
  assert.equal(clonedHead.inert, true, 'cloned .tophead must be inert');
  assert.equal(clonedHead.getAttribute('aria-hidden'), 'true', 'cloned .tophead must be aria-hidden');
  assert.equal(topicPh.querySelector('.tbody'), null, 'topic body must not be cloned into placeholder');
  assert.equal(topicPh.querySelector('.tname').textContent, 'Alpha Topic');
  assert.equal(topicPh.querySelector('.rtext').textContent, 'Formatted recap text');
  assert.deepEqual(collectIds(topicPh), [], 'topic placeholder subtree must contain 0 id attributes');

  // Live topic moved to Quick Look stage keeps its own ids and data-overview-live.
  const stageTopic = bgal.querySelector('.overview-preview-stage > section.topic[data-topic="alpha"]');
  assert.ok(stageTopic, 'live topic moved into .overview-preview-stage');
  assert.equal(stageTopic.hasAttribute('data-overview-live'), true);
  assert.ok(collectIds(stageTopic).includes('th-alpha'), 'live topic keeps its original ids');

  // ArrowDown switches Quick Look preview to .bcard[data-card="term-1"].
  doc.dispatch('keydown', {
    key: 'ArrowDown',
    target: topicPh,
    preventDefault: () => {},
    stopImmediatePropagation: () => {},
  });

  const cardPh = board.querySelector('.overview-placeholder');
  assert.ok(cardPh, 'bcard placeholder inserted into board');
  assert.equal(cardPh.style.height, '36px');
  assert.equal(cardPh.getAttribute('data-overview-target-card'), 'term-1');
  assert.equal(cardPh.hasAttribute('data-card'), false, 'placeholder must not carry data-card');
  assert.equal(cardPh.classList.contains('overview-previewing'), true);
  assert.equal(cardPh.classList.contains('bcard'), true);
  assert.equal(cardPh.getAttribute('data-kind'), 'terminal');
  assert.equal(cardPh.getAttribute('data-file-state'), 'preview');
  assert.equal(cardPh.hasAttribute('data-exited'), true);
  assert.equal(cardPh.getAttribute('data-size'), 'open');

  const clonedBcIn = cardPh.querySelector(':scope > .bc-in');
  const clonedBcHd = cardPh.querySelector(':scope > .bc-in > .bc-hd');
  assert.ok(clonedBcIn, 'bcard placeholder wraps header in .bc-in');
  assert.ok(clonedBcHd, 'bcard placeholder clones .bc-hd');
  assert.equal(clonedBcIn.inert, true, 'cloned .bc-in must be inert');
  assert.equal(clonedBcIn.getAttribute('aria-hidden'), 'true');
  assert.equal(clonedBcHd.inert, true, 'cloned .bc-hd must be inert');
  assert.equal(clonedBcHd.getAttribute('aria-hidden'), 'true');
  assert.equal(cardPh.querySelector('.bc-body'), null, 'bcard body must not be cloned into placeholder');
  assert.equal(cardPh.querySelector('.bc-nm').textContent, 'Terminal 1');
  assert.deepEqual(collectIds(cardPh), [], 'bcard placeholder subtree must contain 0 id attributes');

  // Live bcard is now in Quick Look stage, and topic-alpha is restored to board.
  assert.ok(bgal.querySelector('.overview-preview-stage > .bcard[data-card="term-1"]'));
  assert.ok(board.querySelector('section.topic[data-topic="alpha"]'));
  assert.equal(topic.hasAttribute('data-overview-live'), false);
});

test('dashboard-board.css Overview summary card geometry and inset ring contract', () => {
  // 1. Overview topic clips vertically so all 4 border-radius corners render cleanly.
  assert.match(
    boardCSS,
    /:root\[data-overview\] \.dgrid:is\(\.live, \.history, \.worktree\) > #bcol \.r-board section\.topic\[data-topic\] \{[^}]*overflow-y:\s*clip;/,
    'Overview section.topic must use overflow-y: clip'
  );
  assert.doesNotMatch(
    boardCSS,
    /:root\[data-overview\] \.dgrid:is\(\.live, \.history, \.worktree\) > #bcol \.r-board section\.topic\[data-topic\] \{[^}]*overflow-y:\s*visible;/,
    'Overview section.topic must not use overflow-y: visible'
  );

  // 2. Sticky-mask negative box-shadow is cleared on .tophead in Overview.
  assert.match(
    boardCSS,
    /:root\[data-overview\] \.r-board \.tophead \{[^}]*box-shadow:\s*none;/,
    'Overview .tophead must clear sticky-mask box-shadow'
  );

  // 3. Overview placeholder uses clean inset primary ring, border: 0, padding: 0, overflow: clip, no dashed border or outline-offset: -1px.
  const phRuleMatch = boardCSS.match(/:root\[data-overview\] \.r-board \.overview-placeholder \{([^}]*)\}/);
  assert.ok(phRuleMatch, 'found .overview-placeholder rule');
  const phBody = phRuleMatch[1];
  assert.match(phBody, /padding:\s*0;/);
  assert.match(phBody, /border:\s*0;/);
  assert.match(phBody, /border-radius:\s*var\(--wb-radius\);/);
  assert.match(phBody, /overflow:\s*clip;/);
  assert.match(phBody, /background:\s*var\(--wb-card\);/);
  assert.match(phBody, /box-shadow:\s*inset 0 0 0 1\.5px var\(--wb-primary\), var\(--wb-shadow\);/);
  assert.doesNotMatch(phBody, /dashed/, '.overview-placeholder must not use dashed border');

  // 4. No protruding outline-offset: -1px on Overview items or placeholder hover.
  const overviewBlockMatch = boardCSS.match(/\/\* ===== overview picker \(overview\.js\) =====[\s\S]*?\/\* ===== preview cards/);
  assert.ok(overviewBlockMatch, 'found overview CSS block');
  assert.doesNotMatch(overviewBlockMatch[0], /outline-offset:\s*-1px/, 'Overview block must not use outline-offset: -1px');
  assert.doesNotMatch(overviewBlockMatch[0], /border:\s*1\.5px dashed/, 'Overview block must not use 1.5px dashed border');
  assert.doesNotMatch(boardCSS, /\.overview-placeholder-(head|title|badge|recap)/, 'obsolete plain-text placeholder classes removed');
});
