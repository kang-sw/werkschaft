// Offline unit & performance cost-counting tests for Phase 2:
// - Right-side board dial (dashboard-board-dial.js)
// - Left .gal-rail hover & title-drag-hover preview popover (gal-rail-preview.js)
'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');
const G = require('../assets/gallery.js');
const previewSrc = readAssetSource('wsdashboard', 'gal-rail-preview.js');
const anchorSrc = readAssetSource('wsdashboard', 'dashboard-board-anchor.js');
const dialSrc = readAssetSource('wsdashboard', 'dashboard-board-dial.js');

function makeFakeDom() {
  const stats = { writes: 0, reads: 0, rafs: 0, cancels: 0, flushes: 0, selects: [] };
  const rafQueue = [];

  function simpleMatch(el, sel) {
    sel = sel.trim();
    if (!sel) return false;
    if (sel.startsWith(':scope > ')) {
      // Handled by caller or simplified for direct child checks
      return false;
    }
    const m = sel.match(/^([a-z0-9]*)((?:[.#][\w-]+|\[[\w-]+(?:="[^"]*")?\])*)$/i);
    if (!m) return false;
    if (m[1] && el.tagName !== m[1].toUpperCase()) return false;
    const tokens = m[2].match(/[.#][\w-]+|\[[\w-]+(?:="[^"]*")?\]/g) || [];
    return tokens.every(tok => {
      if (tok[0] === '.') return el.cls.has(tok.slice(1));
      if (tok[0] === '#') return el.attrs.id === tok.slice(1);
      const eq = tok.indexOf('=');
      if (eq < 0) return el.hasAttribute(tok.slice(1, -1));
      const k = tok.slice(1, eq), v = tok.slice(eq + 2, -2);
      return el.getAttribute(k) === v;
    });
  }

  class El {
    constructor(tag, cls = []) {
      this.tagName = tag.toUpperCase();
      this.nodeType = 1;
      this.children = [];
      this.parentNode = null;
      this.attrs = {};
      this.cls = new Set(cls);
      this._hidden = false;
      this._text = '';
      this.listeners = {};
      this.props = new Map();
      this._rect = { top: 40, bottom: 54, left: 20, right: 44, width: 24, height: 14 };
      this.animations = [];
      const self = this;
      this.style = new Proxy({}, {
        get(_, k) {
          if (k === 'setProperty') return (p, v) => { stats.writes++; self.props.set(p, String(v)); };
          if (k === 'getPropertyValue') return p => self.props.get(p) || '';
          if (k === 'removeProperty') return p => { stats.writes++; self.props.delete(p); };
          return self.props.get(String(k)) || '';
        },
        set(_, k, v) {
          stats.writes++;
          self.props.set(String(k), String(v));
          return true;
        }
      });
      this.classList = {
        contains: c => self.cls.has(c),
        add: c => { stats.writes++; self.cls.add(c); },
        remove: c => { stats.writes++; self.cls.delete(c); },
        toggle: (c, on) => { stats.writes++; if (on === undefined ? !self.cls.has(c) : on) self.cls.add(c); else self.cls.delete(c); }
      };
    }
    get id() { return this.attrs.id || ''; }
    set id(v) { stats.writes++; this.attrs.id = String(v); }
    get className() { return Array.from(this.cls).join(' '); }
    set className(v) { stats.writes++; this.cls = new Set(String(v).split(/\s+/).filter(Boolean)); }
    get hidden() { return this._hidden; }
    set hidden(v) { stats.writes++; this._hidden = !!v; }
    get textContent() { return this._text; }
    set textContent(v) {
      stats.writes++;
      this._text = String(v);
      if (this._text === '') {
        this.children.forEach(ch => { ch.parentNode = null; });
        this.children = [];
      }
    }
    get firstChild() { return this.children[0] || null; }
    get nextSibling() {
      if (!this.parentNode) return null;
      const i = this.parentNode.children.indexOf(this);
      return i >= 0 ? this.parentNode.children[i + 1] || null : null;
    }
    get isConnected() {
      let n = this;
      while (n.parentNode) n = n.parentNode;
      return n === root;
    }
    hasAttribute(k) { return k in this.attrs; }
    getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
    setAttribute(k, v) { stats.writes++; this.attrs[k] = String(v); }
    removeAttribute(k) { stats.writes++; delete this.attrs[k]; }
    appendChild(n) {
      stats.writes++;
      if (n.parentNode) n.parentNode.removeChild(n);
      n.parentNode = this;
      this.children.push(n);
      return n;
    }
    insertBefore(n, ref) {
      stats.writes++;
      if (n.parentNode) n.parentNode.removeChild(n);
      n.parentNode = this;
      const i = ref ? this.children.indexOf(ref) : -1;
      if (i < 0) this.children.push(n); else this.children.splice(i, 0, n);
      return n;
    }
    removeChild(n) {
      stats.writes++;
      const i = this.children.indexOf(n);
      if (i >= 0) this.children.splice(i, 1);
      n.parentNode = null;
      return n;
    }
    contains(n) { for (; n; n = n.parentNode) if (n === this) return true; return false; }
    closest(sel) { for (let n = this; n; n = n.parentNode) if (simpleMatch(n, sel)) return n; return null; }
    querySelector(sel) {
      if (sel.includes(',')) {
        for (const part of sel.split(',')) {
          const hit = this.querySelector(part.trim());
          if (hit) return hit;
        }
        return null;
      }
      if (sel.startsWith(':scope > ')) {
        const rest = sel.slice(':scope > '.length);
        const parts = rest.split(' > ');
        let cur = [this];
        for (let i = 0; i < parts.length; i++) {
          const p = parts[i].trim();
          const spaceIdx = p.indexOf(' ');
          const direct = spaceIdx >= 0 ? p.slice(0, spaceIdx) : p;
          const desc = spaceIdx >= 0 ? p.slice(spaceIdx + 1).trim() : '';
          const next = [];
          for (const node of cur) {
            for (const ch of node.children) {
              if (!simpleMatch(ch, direct)) continue;
              if (desc) {
                const sub = ch.querySelector(desc);
                if (sub) next.push(sub);
              } else {
                next.push(ch);
              }
            }
          }
          cur = next;
        }
        return cur[0] || null;
      }
      if (sel.includes(' ')) {
        const idx = sel.indexOf(' ');
        const first = sel.slice(0, idx).trim();
        const rest = sel.slice(idx + 1).trim();
        const stack = [...this.children];
        while (stack.length) {
          const ch = stack.shift();
          if (simpleMatch(ch, first)) {
            const hit = ch.querySelector(rest);
            if (hit) return hit;
          }
          stack.push(...ch.children);
        }
        return null;
      }
      for (const ch of this.children) {
        if (simpleMatch(ch, sel)) return ch;
        const hit = ch.querySelector(sel);
        if (hit) return hit;
      }
      return null;
    }
    addEventListener(type, fn) { (this.listeners[type] || (this.listeners[type] = [])).push(fn); }
    dispatch(type, evt = {}) {
      const list = this.listeners[type] || [];
      evt.type = type;
      evt.target = evt.target || this;
      list.forEach(fn => fn(evt));
    }
    setPointerCapture() {}
    releasePointerCapture() {}
    focus() {}
    getBoundingClientRect() { stats.reads++; return Object.assign({}, this._rect); }
    animate(frames, opts) {
      const a = { frames, opts, cancelled: false, cancel() { this.cancelled = true; } };
      this.animations.push(a);
      return a;
    }
  }

  const root = new El('document');
  const html = new El('html');
  const body = new El('body');
  root.appendChild(html);
  html.appendChild(body);

  const doc = {
    documentElement: html,
    body,
    listeners: {},
    createElement: tag => new El(tag),
    createElementNS: (_, tag) => new El(tag),
    addEventListener(t, fn) { (this.listeners[t] || (this.listeners[t] = [])).push(fn); },
    dispatch(t, e) { (this.listeners[t] || []).forEach(fn => fn(e)); }
  };

  function requestAnimationFrame(fn) {
    stats.rafs++;
    const id = rafQueue.length + 1;
    rafQueue.push({ id, fn, cancelled: false });
    return id;
  }
  function cancelAnimationFrame(id) {
    stats.cancels++;
    const item = rafQueue.find(x => x.id === id);
    if (item) item.cancelled = true;
  }
  function flushRaf() {
    const items = rafQueue.splice(0, rafQueue.length);
    items.forEach(x => { if (!x.cancelled) x.fn(); });
  }

  return { El, root, html, body, doc, stats, requestAnimationFrame, cancelAnimationFrame, flushRaf, rafQueue };
}

function buildDialContext(cardSpecs, opts = {}) {
  const dom = makeFakeDom();
  const grid = new dom.El('main', ['dgrid', opts.gridClass || 'live']);
  const bcol = new dom.El('div', ['bcol']);
  bcol.attrs.id = 'bcol';
  const board = new dom.El('div', ['r-board']);
  board.attrs['data-region'] = 'board';
  dom.body.appendChild(grid);
  grid.appendChild(bcol);
  bcol.appendChild(board);

  const cards = cardSpecs.map(spec => {
    const el = new dom.El(spec.card ? 'div' : 'section', spec.card ? ['bcard'] : ['topic']);
    if (spec.card) {
      el.attrs['data-card'] = spec.card;
      if (spec.kind) el.attrs['data-kind'] = spec.kind;
      const hd = new dom.El('div', ['bc-hd']);
      const title = new dom.El('span', ['bc-nm']);
      title.attrs['data-rail-name'] = '';
      title._text = spec.title || spec.card;
      hd.appendChild(title);
      if (spec.badge) {
        const badge = new dom.El('span', ['bc-kind']);
        badge._text = spec.badge;
        hd.appendChild(badge);
      }
      el.appendChild(hd);
    } else {
      el.attrs['data-topic'] = spec.topic;
      if (spec.state) el.attrs['data-state'] = spec.state;
      if (spec.openAsks !== undefined) el.attrs['data-open-asks'] = String(spec.openAsks);
      if (spec.held) el.attrs['data-board-held'] = '';
      if (spec.closed) el.cls.add('closed');
      if (spec.title) {
        const tophead = new dom.El('div', ['tophead']);
        const tbar = new dom.El('div', ['tbar']);
        const tlead = new dom.El('div', ['tlead']);
        const tname = new dom.El('span', ['tname']);
        tname._text = spec.title;
        tlead.appendChild(tname);
        tbar.appendChild(tlead);
        tophead.appendChild(tbar);
        el.appendChild(tophead);
      }
      if (spec.bodyPolicyCard) {
        const body = new dom.El('div', ['tbody']);
        const pc = new dom.El('div', ['card', 'policy']);
        pc.attrs.id = 'item-99';
        body.appendChild(pc);
        el.appendChild(body);
      }
    }
    board.appendChild(el);
    return el;
  });

  let curCard = cards[opts.active || 0] || null;
  const boardPending = opts.pending ? { 't:1': true } : {};

  const ctx = vm.createContext({
    console,
    Math,
    Date,
    Object,
    Array,
    String,
    Number,
    parseInt,
    isNaN,
    setTimeout: fn => { fn(); return 1; },
    clearTimeout: () => {},
    requestAnimationFrame: dom.requestAnimationFrame,
    cancelAnimationFrame: dom.cancelAnimationFrame,
    doc: dom.doc,
    window: { innerWidth: 1200, innerHeight: 800, WsGallery: G },
    $: id => (id === 'bcol' ? bcol : null),
    region: name => (name === 'board' ? board : null),
    wsT: k => k,
    anchorSized: { bounded: opts.bounded !== false },
    anchorCards: () => cards.slice(),
    anchorCurrent: () => curCard,
    anchorSelect: (n, o) => {
      dom.stats.selects.push({ card: n, opt: o });
      curCard = n;
      ctx.boardDialMark(n);
      return true;
    },
    anchorStep: d => {
      const idx = Math.max(0, Math.min(cards.length - 1, cards.indexOf(curCard) + d));
      curCard = cards[idx];
      dom.stats.selects.push({ card: curCard, step: d });
      ctx.boardDialMark(curCard);
      return true;
    },
    boardPending,
    boardFlushViewed: () => { dom.stats.flushes++; },
  });

  vm.runInContext(dialSrc, ctx);
  ctx.boardDialInit();
  return { dom, grid, bcol, board, cards, ctx, setCur: c => { curCard = c; } };
}

test('dial sliding window and tier math for <=18 and >18 cards', () => {
  const { ctx } = buildDialContext([{ topic: 't1' }, { topic: 't2' }]);
  const plain = v => JSON.parse(JSON.stringify(v));
  assert.deepEqual(plain(ctx.boardDialWindow(5, 2, 18)), { start: 0, end: 5, moreUp: false, moreDown: false });
  assert.deepEqual(plain(ctx.boardDialWindow(25, 0, 18)), { start: 0, end: 18, moreUp: false, moreDown: true });
  assert.deepEqual(plain(ctx.boardDialWindow(25, 12, 18)), { start: 3, end: 21, moreUp: true, moreDown: true });
  assert.deepEqual(plain(ctx.boardDialWindow(25, 24, 18)), { start: 7, end: 25, moreUp: true, moreDown: false });
  assert.equal(ctx.boardDialTier(5, 5), 0);
  assert.equal(ctx.boardDialTier(4, 5), 1);
  assert.equal(ctx.boardDialTier(7, 5), 2);
  assert.equal(ctx.boardDialTier(1, 5), 3);
});

test('dial never mounts on unbounded .dgrid.board and costs 0 writes on unchanged sync', () => {
  const unbounded = buildDialContext([{ topic: 'a' }, { topic: 'b' }], { bounded: false, gridClass: 'board' });
  assert.equal(unbounded.dom.html.hasAttribute('data-board-dial'), false);
  assert.equal(unbounded.grid.children.length, 1);

  const bounded = buildDialContext([
    { topic: 'a', title: 'Alpha Human Title', state: 'discussing', openAsks: true },
    { topic: 'b', openAsks: false, bodyPolicyCard: true, held: true },
    { topic: 'c', closed: true },
    { card: 'term-1', title: 'Terminal', kind: 'terminal', badge: 'TERM' }
  ]);
  assert.equal(bounded.dom.html.hasAttribute('data-board-dial'), true);
  const dial = bounded.grid.children[1];
  assert.equal(dial.id, 'bdial');
  const dots = dial.firstChild.children;
  assert.equal(dots.length, 4);
  // Human titles from .tname (topic) and .bc-nm (bcard)
  assert.equal(dots[0].getAttribute('aria-label'), 'Alpha Human Title');
  assert.equal(dots[1].getAttribute('aria-label'), 'b');
  assert.equal(dots[3].getAttribute('aria-label'), 'Terminal');
  // Ask ring reads data-open-asks, NEVER .card.policy inside topic body
  assert.equal(dots[0].getAttribute('data-ask'), '1');
  assert.equal(dots[1].getAttribute('data-ask'), null);
  assert.equal(dots[1].getAttribute('data-held'), '1');
  assert.equal(dots[2].getAttribute('data-closed'), '1');
  assert.equal(dots[3].getAttribute('data-kind'), 'card');

  // Hover tooltip renders human title + state/kind badge
  dial.dispatch('pointerover', { target: dots[0] });
  const tip = bounded.dom.body.children.find(ch => ch.cls.has('d-board-dial-tip'));
  assert.ok(tip && !tip.hidden);
  assert.equal(tip.querySelector('.d-board-dial-tip-title').textContent, 'Alpha Human Title');
  assert.equal(tip.querySelector('.d-board-dial-tip-state').getAttribute('data-state'), 'discuss');
  dial.dispatch('pointerover', { target: dots[3] });
  assert.equal(tip.querySelector('.d-board-dial-tip-title').textContent, 'Terminal');
  assert.equal(tip.querySelector('.d-board-dial-tip-state').textContent, 'TERM');
  dial.dispatch('pointerleave', {});
  assert.equal(tip.hidden, true);

  // Performance rule 6: unchanged boardDialSync writes 0 DOM attributes and queues 0 idle rAF
  bounded.dom.stats.writes = 0;
  bounded.dom.stats.rafs = 0;
  bounded.ctx.boardDialSync();
  assert.equal(bounded.dom.stats.writes, 0);
  assert.equal(bounded.dom.stats.rafs, 0);

  // O(1) boardDialMark updates only adjacent tiers, zero DOM rebuild
  bounded.dom.stats.writes = 0;
  bounded.ctx.boardDialMark(bounded.cards[1]);
  assert.ok(bounded.dom.stats.writes > 0 && bounded.dom.stats.writes <= 10);
  assert.equal(dots[1].getAttribute('data-tier'), '0');
  assert.equal(dots[1].getAttribute('aria-current'), 'true');
  assert.equal(dots[0].getAttribute('data-tier'), '1');
});

test('dial scrub >18 freezes window, throttles aligns to 1 rAF/frame, defers flush to pointerup', () => {
  const specs = Array.from({ length: 24 }, (_, i) => ({ topic: 't' + i }));
  const { dom, grid, ctx } = buildDialContext(specs, { active: 10, pending: true });
  const dial = grid.children[1];
  const dots = dial.firstChild.children;
  assert.equal(dots.length, 18);
  const initialFirstIdx = dots[0].getAttribute('data-idx');

  dom.stats.rafs = 0;
  dom.stats.flushes = 0;
  dom.stats.selects.length = 0;

  dial.dispatch('pointerdown', { pointerId: 1, button: 0, clientY: 100, target: dots[9] });
  dom.doc.dispatch('pointermove', { pointerId: 1, clientY: 114, preventDefault() {} });
  dom.doc.dispatch('pointermove', { pointerId: 1, clientY: 142, preventDefault() {} });
  assert.equal(dom.stats.rafs, 1, 'at most 1 rAF queued across multiple moves in a frame');
  assert.equal(dom.stats.flushes, 0, 'boardFlushViewed deferred while scrubbing');
  assert.equal(dots[0].getAttribute('data-idx'), initialFirstIdx, 'sliding window frozen during scrub');

  dom.flushRaf();
  assert.equal(dom.stats.selects.length, 1);
  assert.equal(dom.stats.selects[0].opt.keep, true);
  assert.equal(dom.stats.flushes, 0);

  dom.doc.dispatch('pointerup', { pointerId: 1 });
  assert.equal(dom.stats.flushes, 1, 'boardFlushViewed runs on pointerup');
  assert.equal(dom.rafQueue.length, 0, 'zero idle rAF after pointerup');
});

test('dial wheel uses WsGallery.RailWheel and calls preventDefault', () => {
  const { dom, grid } = buildDialContext([{ topic: 'a' }, { topic: 'b' }, { topic: 'c' }]);
  const dial = grid.children[1];
  let prevented = 0;
  dom.stats.selects.length = 0;
  dial.dispatch('wheel', { deltaY: 120, deltaMode: 0, preventDefault() { prevented++; } });
  assert.equal(prevented, 1);
  assert.equal(dom.stats.selects.length, 1);
  assert.equal(dom.stats.selects[0].step, 1);
});

test('gal-rail-preview hover, drag-hover, spring WAAPI, Escape, and 0-write unchanged dragOver', () => {
  const dom = makeFakeDom();
  const ctx = vm.createContext({
    console,
    Math,
    Object,
    Array,
    String,
    Number,
    parseInt,
    isNaN,
    document: dom.doc,
    window: { innerWidth: 1200, innerHeight: 800 }
  });
  vm.runInContext(previewSrc, ctx);
  const ctrl = ctx.window.WsGalPreview.createController();

  const railEl = new dom.El('div', ['gal-rail']);
  const dotsEl = new dom.El('div', ['gal-dots']);
  railEl.appendChild(dotsEl);
  dom.body.appendChild(railEl);

  const mkDot = (pos, top) => {
    const b = new dom.El('button', ['gal-dot']);
    b.attrs['data-pos'] = String(pos);
    b._rect = { top, bottom: top + 28, left: 6, right: 36, width: 30, height: 28 };
    dotsEl.appendChild(b);
    return b;
  };
  const dotHome = mkDot(-1, 60);
  const dotPage = mkDot(0, 96);
  const dotPlus = mkDot(1, 132);

  const gal = {
    pages: [{
      id: 'p1',
      root: { axis: 'row', ratio: 0.6, a: { ref: 't:alpha' }, b: { ref: 'c:term1' } }
    }]
  };

  let overview = false;
  ctrl.attach({
    railEl,
    dotsEl,
    t: k => ({
      'js.gal.preview.home': '홈',
      'js.gal.preview.home.drag': '홈으로 언독',
      'js.gal.preview.new': '새 페이지',
      'js.gal.preview.new.drag': '새 페이지에 도킹',
      'js.gal.preview.append': '이 페이지에 분할 추가',
      'js.gal.preview.spring': '길게 대면 페이지 열기'
    }[k] || k),
    isOverview: () => overview,
    isDragging: () => false,
    getState: () => ({ gal, narrow: true, pos: -1 }),
    getLeafInfo: ref => ref === 't:alpha'
      ? { title: 'Alpha Topic', iconHref: '#i-chat', iconState: 'plain', state: 'discussing', stateCode: 'discuss', stateLabel: '논의 중', sub: 'Summary line', asks: true, asksCount: '2', kind: 'topic' }
      : { title: 'Terminal 1', iconHref: '#l-square-terminal', iconState: 'card', state: 'card', stateCode: 'card', stateLabel: 'TERM', sub: 'npm test', asks: false, kind: 'terminal' }
  });

  // Hover Home pill
  dom.stats.reads = 0;
  dotsEl.dispatch('pointerover', { target: dotHome });
  const pop = dom.body.children.find(ch => ch.cls.has('gal-pop'));
  assert.ok(pop, 'single reused .gal-pop created');
  assert.equal(pop.hidden, false);
  assert.equal(pop.getAttribute('data-mode'), 'pill');
  // Dot rect read before writes + pop rect read once after write = 2 total getBoundingClientRect calls (<= 1 forced layout)
  assert.equal(dom.stats.reads, 2);

  // Hover docked split page: NO outer header, NO ratio metadata, topic dot + state pill rendered
  dotsEl.dispatch('pointerover', { target: dotPage });
  assert.equal(pop.getAttribute('data-mode'), 'page');
  assert.equal(pop.querySelector('.gal-pop-layout') !== null, true);
  assert.equal(pop.querySelector('.gal-pop-drop').hidden, true);
  const firstCard = pop.querySelector('.gal-pop-card');
  assert.equal(firstCard.getAttribute('data-state'), 'discuss');
  assert.ok(firstCard.querySelector('.gal-pop-card-dot'));
  assert.equal(firstCard.querySelector('.gal-pop-card-state').textContent, '논의 중');

  // Escape hides WITHOUT preventDefault or stopPropagation
  let escPrevented = false, escStopped = false;
  dom.doc.dispatch('keydown', { key: 'Escape', preventDefault() { escPrevented = true; }, stopPropagation() { escStopped = true; } });
  assert.equal(pop.hidden, true);
  assert.equal(escPrevented, false);
  assert.equal(escStopped, false);

  // Drag mode: append shows drop strip; unchanged dragOver does 0 writes & 0 reads
  ctrl.dragOver(dotPage, { gal: 'append', pos: 0 });
  assert.equal(pop.hidden, false);
  const dropEl = pop.querySelector('.gal-pop-drop');
  assert.equal(dropEl.hidden, false);
  assert.equal(dropEl.textContent, '이 페이지에 분할 추가');

  dom.stats.writes = 0;
  dom.stats.reads = 0;
  ctrl.dragOver(dotPage, { gal: 'append', pos: 0 });
  assert.equal(dom.stats.writes, 0, 'unchanged dragOver must perform 0 DOM writes');
  assert.equal(dom.stats.reads, 0, 'unchanged dragOver must perform 0 layout reads');

  // Drag over own page dot (to === null): hides drop strip
  ctrl.dragOver(dotPage, null);
  assert.equal(dropEl.hidden, true);

  // Spring progress uses WAAPI transform animation
  ctrl.springStart(dotPage, 650);
  const springBar = pop.querySelector('.gal-pop-spring-bar');
  assert.equal(pop.querySelector('.gal-pop-spring').hidden, false);
  assert.equal(springBar.animations.length, 1);
  assert.deepEqual(JSON.parse(JSON.stringify(springBar.animations[0].frames)), [{ transform: 'scaleX(0)' }, { transform: 'scaleX(1)' }]);

  // Sprung suppresses re-show on same dot until hit changes
  ctrl.sprung(dotPage);
  assert.equal(pop.hidden, true);
  ctrl.dragOver(dotPage, null);
  assert.equal(pop.hidden, true, 'popover stays hidden after sprung on same dot');
  ctrl.dragOver(dotPlus, { gal: 'dock' });
  assert.equal(pop.hidden, false, 'popover resumes when hit changes');

  // Leaving dots hides popover in drag mode
  ctrl.dragOver(null, null);
  assert.equal(pop.hidden, true);
  ctrl.dragEnd();
  assert.equal(dom.stats.rafs, 0, 'zero idle rAF in gal-rail-preview');
});
