'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');

const bridge = fs.readFileSync(path.resolve(__dirname, '../src/theme-bridge.js'), 'utf8');
const origin = 'http://127.0.0.1:49152';

// A page as the bridge sees it: <html> with an optional pinned data-theme, the
// OS colour scheme, and WebView2's message channel.
function page({ pageOrigin = origin, top = true, transport = true, pin = null, os = 'light', throws = false } = {}) {
  const posted = [];
  let failing = throws;
  const attributes = new Map(pin ? [['data-theme', pin]] : []);
  let observe = null;
  const listeners = [];
  const media = {
    matches: os === 'dark',
    addEventListener: (type, fn) => type === 'change' && listeners.push(fn),
  };
  const ctx = {
    location: { origin: pageOrigin },
    document: {
      documentElement: { getAttribute: (name) => (attributes.has(name) ? attributes.get(name) : null) },
    },
    matchMedia: (query) => {
      assert.equal(query, '(prefers-color-scheme: dark)');
      return media;
    },
    MutationObserver: class {
      constructor(callback) { observe = callback; }
      observe(target, options) { assert.equal(JSON.stringify(options), JSON.stringify({ attributes: true, attributeFilter: ['data-theme'] })); }
    },
  };
  if (transport) {
    ctx.chrome = { webview: { postMessage: (message) => { if (failing) throw new Error('closed'); posted.push(message); } } };
  }
  ctx.window = ctx;
  ctx.top = top ? ctx : {};
  vm.createContext(ctx);
  const install = () => vm.runInContext(`${bridge}\ninstallWerkschaftThemeBridge(${JSON.stringify(origin)});`, ctx);
  install();
  return {
    ctx, posted, install,
    pin: (value) => { if (value) attributes.set('data-theme', value); else attributes.delete('data-theme'); observe(); },
    fail: (value) => { failing = value; },
    os: (value) => { media.matches = value === 'dark'; listeners.forEach((fn) => fn()); },
  };
}

test('installs only in the pinned top frame with the WebView2 channel, once', () => {
  for (const options of [{ pageOrigin: 'http://127.0.0.1:1' }, { top: false }, { transport: false }]) {
    const { ctx, posted } = page(options);
    assert.equal(ctx.werkschaftThemeBridge, undefined, JSON.stringify(options));
    assert.deepEqual(posted, []);
  }
  const { ctx, posted, install } = page({ pin: 'dark' });
  assert.equal(ctx.werkschaftThemeBridge, true);
  install();
  assert.deepEqual(posted, ['werkschaft:theme:dark']);
});

test('the first report is the pinned theme, else the OS preference', () => {
  assert.deepEqual(page({ pin: 'light', os: 'dark' }).posted, ['werkschaft:theme:light']);
  assert.deepEqual(page({ pin: 'dark', os: 'light' }).posted, ['werkschaft:theme:dark']);
  assert.deepEqual(page({ os: 'dark' }).posted, ['werkschaft:theme:dark']);
  assert.deepEqual(page({ os: 'light' }).posted, ['werkschaft:theme:light']);
  // A pin that is neither theme is no pin.
  assert.deepEqual(page({ pin: 'sepia', os: 'dark' }).posted, ['werkschaft:theme:dark']);
});

test('only a change of the resolved theme is reported again', () => {
  const p = page({ os: 'light' });
  p.pin('light');
  assert.deepEqual(p.posted, ['werkschaft:theme:light']);
  p.pin('dark');
  assert.deepEqual(p.posted, ['werkschaft:theme:light', 'werkschaft:theme:dark']);
  // The OS preference does not matter while a theme is pinned.
  p.os('light');
  assert.equal(p.posted.length, 2);
  // Unpinning follows the OS preference.
  p.pin(null);
  assert.deepEqual(p.posted.slice(2), ['werkschaft:theme:light']);
  p.os('dark');
  assert.deepEqual(p.posted.slice(3), ['werkschaft:theme:dark']);
});

test('a refused post is tried again at the next change', () => {
  const p = page({ throws: true, os: 'dark' });
  assert.deepEqual(p.posted, []);
  p.fail(false);
  p.os('dark');
  assert.deepEqual(p.posted, ['werkschaft:theme:dark']);
});

test('every message is one of the two fixed texts', () => {
  const p = page({ os: 'dark' });
  for (const value of ['light', 'dark', null, 'dark', 'light']) p.pin(value);
  for (const message of p.posted) assert.match(message, /^werkschaft:theme:(light|dark)$/);
});
