'use strict';
// Runs assets/mermaid-init.js against a small fake DOM and a fake mermaid:
// theme config, surface-aware drawing, redraw on theme change and on the OS
// scheme event (one listener only), and the compact error card.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const initSrc = fs.readFileSync(path.join(__dirname, '../assets/mermaid-init.js'), 'utf8');
const svgSrc = fs.readFileSync(path.join(__dirname, '../assets/svg-view.js'), 'utf8');

class El {
  constructor(tag) {
    this.tag = tag; this.children = []; this.attrs = {}; this.style = {}; this.parentElement = null;
    this.className = ''; this._text = ''; this.innerHTML = ''; this.hidden = false; this.listeners = {};
    this.isConnected = true; this.bg = 'rgba(0, 0, 0, 0)';
  }
  get textContent() { return this.children.length ? this.children.map(c => c.textContent).join('') : this._text; }
  set textContent(v) { this.children = []; this._text = String(v); }
  appendChild(c) { c.parentElement = this; this.children.push(c); return c; }
  removeChild(c) { this.children = this.children.filter(x => x !== c); c.parentElement = null; return c; }
  get parentNode() { return this.parentElement; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  hasAttribute(k) { return k in this.attrs; }
  removeAttribute(k) { delete this.attrs[k]; }
  addEventListener(t, f) { (this.listeners[t] = this.listeners[t] || []).push(f); }
  click() { (this.listeners.click || []).forEach(f => f()); }
  find(cls) {
    const out = [];
    const walk = e => { e.children.forEach(c => { if (c.className.split(' ').includes(cls)) out.push(c); walk(c); }); };
    walk(this);
    return out;
  }
}

const TOKENS = {
  dark: { '--wb-bg': 'rgb(20, 21, 24)', '--wb-card': 'rgb(30, 32, 36)', '--wb-fg': 'rgb(231, 231, 228)', '--wb-muted': 'rgb(43, 45, 50)', '--wb-muted-fg': 'rgb(162, 166, 173)', '--wb-primary': 'rgb(122, 162, 255)', '--wb-now': 'rgb(36, 52, 87)', '--wb-on-now': 'rgb(211, 222, 251)' },
  light: { '--wb-bg': 'rgb(243, 243, 240)', '--wb-card': 'rgb(255, 255, 255)', '--wb-fg': 'rgb(27, 29, 33)', '--wb-muted': 'rgb(233, 232, 227)', '--wb-muted-fg': 'rgb(95, 100, 107)', '--wb-primary': 'rgb(43, 98, 217)', '--wb-now': 'rgb(43, 98, 217)', '--wb-on-now': 'rgb(255, 255, 255)' },
};

function harness(opts = {}) {
  const root = new El('html'), body = new El('body'); root.appendChild(body);
  if (opts.pinned) root.setAttribute('data-theme', opts.pinned);
  let os = opts.os || 'dark';
  const mqListeners = [];
  const mq = { get matches() { return os === 'dark'; }, addEventListener: (t, f) => mqListeners.push(f) };
  const pres = [];
  const inits = [], renders = [];
  const tone = () => (root.getAttribute('data-theme') || os);
  const doc = {
    documentElement: root, body,
    createElement: t => new El(t),
    querySelectorAll: sel => (sel === 'pre.mermaid' ? pres.slice() : []),
    getElementById: () => null,
    execCommand: () => true,
  };
  const win = {
    document: doc, innerWidth: 800,
    matchMedia: () => mq,
    getComputedStyle: el => ({
      backgroundColor: el.bg,
      fontFamily: 'system-ui',
      get color() { const m = /var\((--[\w-]+)\)/.exec(el.style.color || ''); return m ? (TOKENS[tone()][m[1]] || '') : ''; },
      getPropertyValue: n => (el === root ? (TOKENS[tone()][n] || '') : ''),
    }),
    mermaid: {
      initialize: c => inits.push(c),
      render: async (id, src) => {
        renders.push({ id, src });
        if (/BAD/.test(src)) throw new Error('Parse error on line 2:\n...BAD\n-----^\nExpecting X');
        return { svg: '<svg data-src="' + src + '"></svg>' };
      },
    },
  };
  win.window = win;
  const ctx = vm.createContext(Object.assign(win, { navigator: {}, Promise, setTimeout, setImmediate }));
  vm.runInContext('var getComputedStyle = window.getComputedStyle;', ctx);
  return {
    root, body, doc, pres, inits, renders, mqListeners,
    setOS(v) { os = v; },
    pre(src, bg) {
      const host = new El('div'); host.bg = bg || 'rgba(0, 0, 0, 0)'; body.appendChild(host);
      const p = new El('pre'); p.className = 'mermaid'; p._text = src; host.appendChild(p); pres.push(p);
      return p;
    },
    run() { vm.runInContext(initSrc, ctx); },
    async settle() { for (let i = 0; i < 20; i++) await new Promise(r => setImmediate(r)); },
  };
}

test('option C config: semantic theme, structure fixes, error handling on', async () => {
  const h = harness();
  h.pre('graph TD; A-->B', 'rgb(30, 32, 36)');
  h.run(); await h.settle();
  assert.equal(h.inits.length, 1);
  const c = h.inits[0];
  assert.equal(c.theme, 'base');
  assert.equal(c.securityLevel, 'strict');
  assert.equal(c.suppressErrorRendering, true);
  assert.equal(c.sequence.mirrorActors, false);
  const v = c.themeVariables;
  for (const k of ['sectionBkgColor', 'taskBkgColor', 'activeTaskBkgColor', 'doneTaskBkgColor', 'critBkgColor', 'gridColor', 'todayLineColor', 'noteBkgColor']) assert.ok(v[k], k);
  assert.equal(v.xyChart.plotColorPalette.split(',').length, 6);
  assert.match(v.xyChart.plotColorPalette, /^#7aa2ff,#fb923c,#6cc39a/);
  assert.match(c.themeCSS, /stroke-width:2\.6px/);
  assert.match(c.themeCSS, /rx:4px/);
  assert.match(c.themeCSS, /\.node\.store rect/);
  // the old global strip-all stroke is gone
  assert.doesNotMatch(c.themeCSS, /\.node rect, \.node polygon[^}]*stroke: none/);
  assert.doesNotMatch(c.themeCSS, /\.node (rect|polygon|path)[^{]*\{[^}]*stroke:none/);
  assert.match(c.themeCSS, /\[id\*="-extension"\] path[^}]*fill:#1e2024/); // hollow marker, filled with the surface
  assert.match(c.themeCSS, /classId-"\] > \.divider/); // header divider emphasised
  assert.match(c.themeCSS, /annotation-group/);
  assert.ok(h.pres[0].getAttribute('data-processed'));
  assert.match(h.pres[0].innerHTML, /<svg/);
});

test('surface aware: chat bubble (--wb-card) vs board card (--wb-bg)', async () => {
  const h = harness();
  h.pre('graph TD; A-->B', 'rgb(30, 32, 36)');
  h.pre('graph TD; C-->D', 'rgb(20, 21, 24)');
  h.run(); await h.settle();
  assert.equal(h.inits.length, 2);
  assert.equal(h.inits[0].themeVariables.edgeLabelBackground, '#1e2024');
  assert.equal(h.inits[1].themeVariables.edgeLabelBackground, '#141518');
  assert.notEqual(h.inits[0].themeVariables.clusterBkg, h.inits[1].themeVariables.clusterBkg);
  // a transparent wrapper hands the question to the next ancestor
  const h2 = harness();
  const p = h2.pre('graph TD; A-->B', 'rgb(30, 32, 36)');
  const inner = new El('div'); const host = p.parentElement; host.removeChild(p); inner.appendChild(p); host.appendChild(inner);
  h2.run(); await h2.settle();
  assert.equal(h2.inits[0].themeVariables.edgeLabelBackground, '#1e2024');
});

test('light tone uses the light tokens', async () => {
  const h = harness({ os: 'light' });
  h.pre('graph TD; A-->B', 'rgb(255, 255, 255)');
  h.run(); await h.settle();
  assert.equal(h.inits[0].darkMode, false);
  assert.equal(h.inits[0].themeVariables.edgeLabelBackground, '#ffffff');
  assert.equal(h.pres[0].getAttribute('data-wb-tone'), 'light');
});

test('pinned theme redraws on a manual switch; drawn blocks of the same tone are left alone', async () => {
  const h = harness({ pinned: 'dark' });
  h.pre('graph TD; A-->B', 'rgb(30, 32, 36)');
  h.run(); await h.settle();
  assert.equal(h.renders.length, 1);
  h.run(); await h.settle();
  assert.equal(h.renders.length, 1, 'same tone: no second draw');
  h.root.setAttribute('data-theme', 'light');
  h.run(); await h.settle();
  assert.equal(h.renders.length, 2);
  assert.equal(h.inits[h.inits.length - 1].darkMode, false);
  assert.equal(h.pres[0].getAttribute('data-wb-src'), 'graph TD; A-->B');
});

test('OS auto: matchMedia change redraws, with one listener however often the script runs', async () => {
  const h = harness({ os: 'dark' });
  h.pre('graph TD; A-->B', 'rgb(30, 32, 36)');
  h.run(); await h.settle(); h.run(); await h.settle(); h.run(); await h.settle();
  assert.equal(h.mqListeners.length, 1);
  assert.equal(h.renders.length, 1);
  h.setOS('light');
  h.mqListeners[0](); await h.settle();
  assert.equal(h.renders.length, 2);
  assert.equal(h.inits[h.inits.length - 1].darkMode, false);
  // no change in tone: the event does no work
  h.mqListeners[0](); await h.settle();
  assert.equal(h.renders.length, 2);
});

test('a pinned theme ignores the OS event', async () => {
  const h = harness({ pinned: 'dark' });
  h.pre('graph TD; A-->B', 'rgb(30, 32, 36)');
  h.run(); await h.settle();
  h.setOS('light');
  h.mqListeners[0](); await h.settle();
  assert.equal(h.renders.length, 1);
});

test('requests made during a draw coalesce into one follow-up pass', async () => {
  const h = harness();
  h.pre('graph TD; A-->B', 'rgb(30, 32, 36)');
  h.run(); h.run(); h.run(); h.run();
  await h.settle();
  assert.equal(h.renders.length, 1);
});

test('compact error card: summary, toggle, copy; no bomb svg', async () => {
  const h = harness();
  const p = h.pre('graph TD\n  BAD', 'rgb(30, 32, 36)');
  h.run(); await h.settle();
  assert.ok(p.getAttribute('data-processed'));
  const [card] = p.find('wb-mmd-err');
  assert.ok(card, 'card');
  assert.equal(p.innerHTML, '');
  assert.equal(p.find('wb-mmd-err-sum')[0].textContent, 'Parse error on line 2:');
  const [raw] = p.find('wb-mmd-err-raw'), [msg] = p.find('wb-mmd-err-msg');
  assert.equal(raw.textContent, 'graph TD\n  BAD');
  assert.equal(raw.hidden, true);
  const [toggle, copy] = p.find('wb-mmd-err-btn');
  toggle.click();
  assert.equal(raw.hidden, false);
  assert.equal(msg.hidden, false);
  toggle.click();
  assert.equal(raw.hidden, true);
  assert.ok(copy);
  // a tone change restores the source and draws the block again
  h.setOS('light'); h.mqListeners[0](); await h.settle();
  assert.equal(h.renders.length, 2);
});

test('svg-view lightbox keeps edge-label pill styling', () => {
  const m = /var htm = \[([^\]]*)\]/.exec(svgSrc);
  const names = m[1].match(/"[^"]+"/g).map(s => s.slice(1, -1));
  for (const n of ['background-color', 'padding', 'border-radius']) assert.ok(names.includes(n), n);
});
