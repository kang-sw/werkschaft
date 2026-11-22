'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');

const bridge = fs.readFileSync(path.resolve(__dirname, '../src/notify-bridge.js'), 'utf8');
const origin = 'http://127.0.0.1:49152';

// A page as the bridge sees it: its own global, frame, origin, and the
// native transport (WebKit's message handler or WebView2's webview).
function page({ pageOrigin = origin, top = true, gate = false, transport = 'mac', throws = false } = {}) {
  const posted = [];
  const timers = [];
  const ctx = {
    location: { origin: pageOrigin },
    document: { documentElement: { hasAttribute: (name) => gate && name === 'data-login-gate' } },
    setTimeout: (fn, ms) => timers.push({ fn, ms }),
  };
  const post = (message) => {
    if (throws) throw new Error('transport closed');
    posted.push(message);
  };
  if (transport === 'mac') ctx.webkit = { messageHandlers: { werkschaftNotify: { postMessage: post } } };
  if (transport === 'windows') ctx.chrome = { webview: { postMessage: post } };
  ctx.window = ctx;
  ctx.top = top ? ctx : {};
  vm.createContext(ctx);
  const install = () => vm.runInContext(`${bridge}\ninstallWerkschaftNotifyBridge(${JSON.stringify(origin)});`, ctx);
  install();
  return { ctx, posted, timers, install };
}

function payload(message) {
  assert.ok(message.startsWith('werkschaft:notify:'), message);
  return JSON.parse(message.slice('werkschaft:notify:'.length));
}

test('installs only in the pinned top frame with a native transport, once', () => {
  for (const options of [{ pageOrigin: 'http://127.0.0.1:1' }, { top: false }, { gate: true }, { transport: 'none' }]) {
    assert.equal(page(options).ctx.werkschaftNotify, undefined, JSON.stringify(options));
  }
  for (const transport of ['mac', 'windows']) {
    const { ctx, install } = page({ transport });
    const installed = ctx.werkschaftNotify;
    assert.equal(typeof installed.notify, 'function');
    install();
    assert.equal(ctx.werkschaftNotify, installed);
  }
});

test('a notification resolves with the shell answer for its own id', async () => {
  const { ctx, posted } = page();
  const first = ctx.werkschaftNotify.notify('ws/lead finished', 'line', 'ws-idle:lead:1');
  const second = ctx.werkschaftNotify.notify('ws/lead asks', '', 'ws-ask:lead:q1');
  const [a, b] = posted.map(payload);
  assert.deepEqual(a, { id: 1, title: 'ws/lead finished', body: 'line', tag: 'ws-idle:lead:1' });
  assert.equal(b.id, 2);
  ctx.__werkschaftNotifyResult(2, false);
  ctx.__werkschaftNotifyResult(1, true);
  // A repeated or unknown answer changes nothing.
  ctx.__werkschaftNotifyResult(1, false);
  ctx.__werkschaftNotifyResult(9, true);
  assert.equal(await first, true);
  assert.equal(await second, false);
});

test('fields are bounded before they leave the page', () => {
  const { ctx, posted } = page();
  ctx.werkschaftNotify.notify('t'.repeat(300), 'b'.repeat(2000), 'g'.repeat(500));
  const sent = payload(posted[0]);
  assert.deepEqual([sent.title.length, sent.body.length, sent.tag.length], [200, 1000, 300]);
});

test('no answer, or a transport that throws, counts as a failed native delivery', async () => {
  const silent = page();
  const pending = silent.ctx.werkschaftNotify.notify('t', 'b', 'tag');
  assert.equal(silent.timers.length, 1);
  assert.equal(silent.timers[0].ms, 10000);
  silent.timers[0].fn();
  assert.equal(await pending, false);
  // A late answer after the timeout is ignored.
  silent.ctx.__werkschaftNotifyResult(1, true);

  const broken = page({ throws: true });
  assert.equal(await broken.ctx.werkschaftNotify.notify('t', 'b', 'tag'), false);
});
