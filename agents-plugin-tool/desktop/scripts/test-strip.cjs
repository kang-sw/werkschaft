'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.resolve(__dirname, '../ui/strip.js'), 'utf8');

function classList() {
  const set = new Set();
  return {
    set,
    add: (c) => set.add(c),
    remove: (c) => set.delete(c),
    toggle: (c, on) => { if (on) set.add(c); else set.delete(c); },
    contains: (c) => set.has(c),
  };
}

// An element with just the surface strip.js touches. `inside` lists the
// selectors target.closest() should match for this element.
function element({ dataset = {}, inside = [], rect = { left: 0, bottom: 0 } } = {}) {
  const listeners = {};
  const attrs = new Map();
  return {
    dataset, classList: classList(), textContent: '', title: '', listeners, attrs,
    setAttribute: (k, v) => attrs.set(k, v),
    getBoundingClientRect: () => rect,
    closest: (sel) => (inside.includes(sel) ? true : null),
    addEventListener: (type, fn) => { listeners[type] = fn; },
  };
}

// Loads strip.js against stubbed DOM and Tauri bridge. `invoke` results are
// controlled per call so the menu guard release can be observed.
function load({ invoke } = {}) {
  const calls = [];
  const pending = [];
  const title = element();
  const max = element({ dataset: { cap: 'strip_toggle_maximize' }, inside: ['.menu, .cap'] });
  const min = element({ dataset: { cap: 'strip_minimize' } });
  const menus = ['File', 'Edit'].map((m, i) => element({ dataset: { menu: m }, inside: ['.menu, .cap'], rect: { left: 10 + i * 40, bottom: 0 } }));
  const strip = element({ rect: { left: 0, bottom: 30 } });
  const rootEl = element();
  rootEl.dataset = {};
  const docListeners = {};
  const doc = {
    documentElement: rootEl,
    getElementById: (id) => (id === 'title' ? title : strip),
    querySelector: (sel) => { assert.equal(sel, '.cap.max'); return max; },
    querySelectorAll: (sel) => (sel === '.menu' ? menus : [min, max]),
    addEventListener: (type, fn) => { docListeners[type] = fn; },
  };
  const ctx = {
    document: doc,
    __TAURI_INTERNALS__: {
      invoke: invoke || ((cmd, args) => {
        calls.push({ cmd, args });
        return new Promise((resolve) => pending.push(resolve));
      }),
    },
    Array, Object, Promise,
  };
  ctx.window = ctx;
  vm.createContext(ctx);
  vm.runInContext(source, ctx);
  return { ctx, calls, pending, title, max, min, menus, strip, rootEl, docListeners };
}

const flush = () => new Promise((r) => setImmediate(r));

test('caption buttons dispatch their command on a primary click only', () => {
  const s = load();
  s.min.listeners.click({ button: 0 });
  s.max.listeners.click({ button: 0 });
  s.min.listeners.click({ button: 2 });
  assert.deepEqual(s.calls.map((c) => c.cmd), ['strip_minimize', 'strip_toggle_maximize']);
});

test('a menu label pops the native menu once and releases the guard when it closes', async () => {
  const s = load();
  const prevented = [];
  const down = (button) => s.menus[0].listeners.mousedown({ button, preventDefault: () => prevented.push(1) });
  down(2);
  assert.equal(s.calls.length, 0);
  down(0);
  assert.equal(JSON.stringify(s.calls), JSON.stringify([{ cmd: 'strip_popup', args: { menu: 'File', x: 10, y: 30 } }]));
  assert.ok(s.menus[0].classList.contains('is-open'));
  // While the menu is open a second press (either label) opens nothing.
  down(0);
  s.menus[1].listeners.mousedown({ button: 0, preventDefault() {} });
  assert.equal(s.calls.length, 1);
  s.pending[0]();
  await flush();
  assert.ok(!s.menus[0].classList.contains('is-open'));
  down(0);
  assert.equal(s.calls.length, 2);
});

test('the guard is released even when the command throws or rejects', async () => {
  for (const invoke of [() => { throw new Error('x'); }, () => Promise.reject(new Error('x'))]) {
    const s = load({ invoke });
    s.ctx.werkschaftStrip.openMenu('Edit');
    await flush();
    assert.ok(!s.menus[1].classList.contains('is-open'));
    s.ctx.werkschaftStrip.openMenu('Edit');
    await flush();
    assert.ok(!s.menus[1].classList.contains('is-open'));
  }
});

test('openMenu from the shell ignores an unknown menu', () => {
  const s = load();
  s.ctx.werkschaftStrip.openMenu('Nope');
  assert.equal(s.calls.length, 0);
});

test('the strip drags, double click maximizes, menus and caps are excluded', () => {
  const s = load();
  const press = (e) => { let p = false; s.strip.listeners.mousedown({ preventDefault: () => { p = true; }, target: element(), ...e }); return p; };
  assert.equal(press({ button: 0, detail: 1 }), true);
  assert.equal(press({ button: 0, detail: 2 }), true);
  assert.equal(press({ button: 2, detail: 1 }), false);
  assert.equal(press({ button: 0, detail: 1, target: element({ inside: ['.menu, .cap'] }) }), false);
  assert.deepEqual(s.calls.map((c) => c.cmd), ['strip_drag', 'strip_toggle_maximize']);
});

test('context menu is suppressed', () => {
  const s = load();
  let p = false;
  s.docListeners.contextmenu({ preventDefault: () => { p = true; } });
  assert.ok(p);
});

test('set applies maximized, focus, title, theme and the hit-test states', () => {
  const s = load();
  const { set } = s.ctx.werkschaftStrip;
  set({ maximized: true, focused: false, title: 'Doc', theme: 'dark', maxHover: true, maxPressed: true });
  assert.ok(s.rootEl.classList.contains('maximized'));
  assert.ok(s.rootEl.classList.contains('unfocused'));
  assert.equal(s.max.attrs.get('aria-label'), 'Restore');
  assert.equal(s.title.textContent, 'Doc');
  assert.equal(s.title.title, 'Doc');
  assert.equal(s.rootEl.dataset.theme, 'dark');
  assert.ok(s.max.classList.contains('is-hover'));
  assert.ok(s.max.classList.contains('is-pressed'));
  set({ maximized: false, focused: true, theme: 'sepia', maxHover: false, maxPressed: false });
  assert.ok(!s.rootEl.classList.contains('maximized'));
  assert.ok(!s.rootEl.classList.contains('unfocused'));
  assert.equal(s.max.attrs.get('aria-label'), 'Maximize');
  assert.equal(s.rootEl.dataset.theme, undefined);
  assert.ok(!s.max.classList.contains('is-hover'));
  // Absent keys leave state alone; a non-string title and non-objects are ignored.
  set({ title: 5 });
  set(null);
  assert.equal(s.title.textContent, 'Doc');
});

test('werkschaftStrip is frozen and cannot be replaced', () => {
  const s = load();
  assert.ok(Object.isFrozen(s.ctx.werkschaftStrip));
  assert.throws(() => vm.runInContext("'use strict'; window.werkschaftStrip = 1", s.ctx), /read only/);
});
