// Harness-free: node internal/wsdashboard/testdata/notifications.test.cjs
// The rail's notification selection (WsRail.notices) and the shell page's
// delivery (dashboard-lifecycle.js's notification block) in a VM, with
// fake rows, browser notifications, toasts, and Werkschaft bridge.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');
const tick = () => new Promise(setImmediate);

const railWindow = {};
vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../assets/rail.js'), 'utf8'), { window: railWindow });
const WsRail = railWindow.WsRail;

// row is a fake rail row: rid, then busy, asks, child (its parent's group
// marks it data-g), ended, stopped, ms (last-tool time), msg (last
// message), who (the message's author label), ap (an approval's tool).
function row(rid, o = {}) {
  const attrs = { 'data-rid': rid };
  if (o.asks !== undefined) attrs['data-asks'] = o.asks;
  if (o.busy) attrs['data-busy'] = '1';
  if (o.child) attrs['data-g'] = '/repo';
  if (o.ap) { attrs['data-ap'] = '1'; attrs['data-ap-tool'] = o.ap; attrs['data-ap-args'] = o.args || ''; }
  const classes = new Set(['rs']);
  if (o.ended) classes.add('ended');
  if (o.stopped) classes.add('stopped');
  const group = { querySelector: () => ({ textContent: o.project || 'proj' }) };
  return {
    attrs,
    getAttribute: k => (k in attrs ? attrs[k] : null),
    setAttribute: (k, v) => { attrs[k] = v; },
    removeAttribute: k => { delete attrs[k]; },
    classList: { contains: k => classes.has(k), add: k => classes.add(k), remove: k => classes.delete(k) },
    closest: () => group,
    querySelector(sel) {
      if (sel === 'time.when.t') return o.ms ? { getAttribute: () => String(o.ms) } : null;
      if (sel === '.drmsg .s2') {
        if (!o.msg) return null;
        const by = o.who ? { textContent: o.who } : null;
        return { textContent: (o.who || '') + o.msg, querySelector: () => by };
      }
      return null;
    }
  };
}
// Arrays from the VM's realm fail deepEqual against this realm's [].
const none = (events, message) => assert.equal(events.length, 0, message);
const busy = (r, on) => on ? r.setAttribute('data-busy', '1') : r.removeAttribute('data-busy');

// --- WsRail.notices: the selection alone ------------------------------------
{
  const n = WsRail.notices();
  let parent = row('p', { busy: true, asks: '', ms: 1000 }), kid = row('k', { busy: true, child: true, asks: '' });
  const read = (rows, base) => n.read(rows || [parent, kid], base);
  none(read(), 'first read only baselines');
  busy(kid, false);
  none(read(), 'a child finishing under a busy parent is no notice');
  busy(parent, false);
  let ev = read();
  assert.equal(ev.length, 1, 'the parent going fully idle is one notice');
  assert.equal(ev[0].kind, 'idle');
  assert.equal(ev[0].tag, 'ws-idle:p:1000');
  none(read(), 'staying idle notifies once');
  busy(parent, true); read(); busy(parent, false);
  assert.equal(read()[0].tag, 'ws-idle:p:1000.1', 'a finish repeating the last-tool time gets its own tag');
  busy(parent, true); read(); busy(parent, false);
  assert.equal(read()[0].tag, 'ws-idle:p:1000.2');
  busy(kid, true); read(); busy(kid, false);
  none(read(), 'a child row never notifies of its own finish');

  const gone = row('g', { busy: true, asks: '' });
  read([gone]);
  none(read([]), 'a busy row that disappears is no notice');
  const ending = row('e', { busy: true, asks: '' });
  read([ending]);
  ending.classList.add('ended'); busy(ending, false);
  none(read([ending]), 'a busy row that ends is no notice');
  ending.classList.remove('ended');
  none(read([ending]), 'an ended row was recorded idle');
  const stopping = row('s', { busy: true, asks: '' });
  read([stopping]);
  stopping.classList.add('stopped'); busy(stopping, false);
  none(read([stopping]), 'a busy row that stops is no notice');

  // Unknown background counts read as 0 on the server (2146): the row's
  // data-busy drops at turn end, which is the finish.
  const codex = row('cx', { busy: true, asks: '' });
  read([codex]); busy(codex, false);
  assert.equal(read([codex])[0].tag, 'ws-idle:cx:0', 'no last-tool time tags 0');

  const a = row('a', { asks: '12', child: true });
  read([a]);
  a.setAttribute('data-asks', '12 15 3');
  ev = read([a]);
  assert.equal(ev.length, 1, 'new IDs on a child row notify');
  assert.deepEqual(Array.from(ev[0].ids), ['15', '3']);
  assert.equal(ev[0].tag, 'ws-ask:a:15,3', 'tag holds the sorted new IDs');
  none(read([a]), 'unchanged asks do not notify');
  a.setAttribute('data-asks', '12 3');
  none(read([a]), 'closing does not notify');
  a.setAttribute('data-asks', '12 3 15');
  assert.equal(read([a])[0].tag, 'ws-ask:a:15', 'a reopened ID is new');
  a.setAttribute('data-asks', '12 3 15 99');
  none(read([a], true), 'a reconnect baseline records without notifying');
  none(read([a]), 'baselined IDs stay known');
  const fresh = row('f', { asks: '1 2' });
  none(read([a, fresh]), 'a new row baselines');
  const old = row('o');
  read([old]); old.setAttribute('data-asks', '1');
  none(read([old]), 'an old server without the attribute does nothing');
}

// --- delivery: the lifecycle block in a VM ------------------------------------
const lifecycle = fs.readFileSync(path.join(__dirname, '../assets/dashboard-lifecycle.js'), 'utf8');
const start = lifecycle.indexOf('  // --- notifications ---');
const end = lifecycle.indexOf('  if (hostedPage) {\n    doc.addEventListener("click"');
assert.ok(start >= 0 && end > start, 'notification block found');
new vm.Script(readAssetSource('wsdashboard', 'dashboard.js')); // the shipped concatenation parses
for (const [file, call] of [
  ['dashboard-board.js', 'apScan(el, "rail")'], ['dashboard-board.js', 'apScan(el, name)'],
  ['dashboard-stream.js', 'apScan(railFrag || frag(ev.html), ev.name)'], ['dashboard-stream.js', 'apScan(railFrag, "rail", true)']
]) assert.ok(fs.readFileSync(path.join(__dirname, '../assets', file), 'utf8').includes(call), file + ' calls ' + call);

function page(opts = {}) {
  const shown = [], toasts = [], native = [], gone = [], listeners = {};
  function Notification(title, o) {
    this.title = title; this.body = o.body; this.tag = o.tag; this.close = () => {};
    shown.push(this);
  }
  Notification.permission = opts.permission || 'default';
  Notification.requestPermission = () => {};
  const window = {
    Notification, focus() {},
    WsToast: { show: (text, o) => toasts.push({ text, o }) }
  };
  if (opts.secondary) window.werkschaftSecondary = true;
  if (opts.bridge) window.werkschaftNotify = { notify: (title, body, tag) => { native.push({ title, body, tag }); return opts.bridge(); } };
  const doc = {
    hidden: !!opts.hidden, focused: opts.focused !== false,
    hasFocus() { return this.focused; },
    addEventListener: (type, fn) => { listeners[type] = fn; }
  };
  const ctx = vm.createContext({
    window, doc, WsRail, Promise, framed: !!opts.framed, rid: opts.rid || 'viewed',
    root: { hasAttribute: () => true },
    wsT: (k, ...a) => k + '(' + a.join(',') + ')',
    wsTn: (k, n) => k + (n === 1 ? '.one' : '.other') + '(' + n + ')',
    toolLabel: t => t, saveDraft() {}, go: href => gone.push(href),
    q: (sel, el) => el.querySelectorAll(sel)
  });
  vm.runInContext(lifecycle.slice(start, end), ctx);
  const region = rows => ({
    querySelectorAll: sel => sel === '[data-ap]' ? rows.filter(r => r.attrs['data-ap']) : sel === '.rs[data-rid]' ? rows : []
  });
  return {
    shown, toasts, native, gone, doc, window,
    scan: (rows, name = 'rail') => ctx.apScan(region(rows), name),
    baseline: rows => ctx.apScan(region(rows), 'rail', true),
    show: () => { doc.hidden = false; listeners.visibilitychange(); }
  };
}
// finish drives one idle edge on a fresh page and returns it.
async function finish(opts, rowOpts = {}) {
  const p = page(opts), r = row('s1', { busy: true, asks: '', ms: 7, msg: 'All done', ...rowOpts });
  p.scan([r]); busy(r, false); p.scan([r]);
  await tick();
  return p;
}

(async () => {
  let p = await finish({ bridge: () => Promise.resolve(true), permission: 'granted' });
  assert.equal(p.native.length, 1, 'native first');
  assert.deepEqual({ ...p.native[0] }, { title: 'js.notif.idle.title(proj/s1)', body: 'All done', tag: 'ws-idle:s1:7' });
  assert.equal(p.shown.length + p.toasts.length, 0, 'native true: no browser notification, no toast');

  p = await finish({ bridge: () => Promise.resolve(false), permission: 'granted' });
  assert.equal(p.native.length, 1);
  assert.equal(p.shown.length, 1, 'native false falls back to the granted browser notification');
  assert.equal(p.shown[0].tag, 'ws-idle:s1:7');
  assert.equal(p.toasts.length, 0, 'never both');
  p.shown[0].onclick();
  assert.deepEqual(p.gone, ['/s/s1'], 'a click opens the session');

  p = await finish({ bridge: () => Promise.resolve(false) });
  assert.equal(p.shown.length, 0);
  assert.equal(p.toasts.length, 1, 'native false without permission falls back to a toast');
  assert.equal(p.toasts[0].text, 'js.notif.idle.title(proj/s1) · All done');

  p = await finish({ bridge: () => Promise.reject(new Error('gone')), permission: 'granted' });
  assert.equal(p.shown.length, 1, 'native rejection falls back');
  p = await finish({ bridge: () => { throw new Error('sync'); }, permission: 'granted' });
  assert.equal(p.shown.length, 1, 'a throwing bridge falls back');

  p = await finish({ permission: 'granted' });
  assert.equal(p.shown.length, 1, 'no bridge: the browser notification as before');
  p = await finish({});
  assert.equal(p.toasts.length, 1, 'no bridge, no permission: a toast as before');

  p = await finish({ secondary: true, bridge: () => Promise.resolve(true), permission: 'granted' });
  assert.equal(p.native.length + p.shown.length + p.toasts.length, 0, 'a secondary Werkschaft window delivers nothing');
  p = await finish({ framed: true, permission: 'granted' });
  assert.equal(p.shown.length + p.toasts.length, 0, 'a framed page leaves notifications to its shell');

  p = await finish({ rid: 's1', permission: 'granted' });
  assert.equal(p.shown.length, 0, 'the viewed, visible, focused session is suppressed');
  p = await finish({ rid: 's1', permission: 'granted', hidden: true });
  assert.equal(p.shown.length, 1, 'the viewed session in a hidden tab still notifies');
  p = await finish({ rid: 's1', permission: 'granted', focused: false });
  assert.equal(p.shown.length, 1, 'the viewed session in an unfocused window still notifies');

  p = await finish({ permission: 'granted' }, { who: 'You', msg: 'go on' });
  assert.equal(p.shown[0].body, 'You · go on', 'the author label is set apart');
  p = await finish({ permission: 'granted' }, { msg: 'x'.repeat(500) });
  assert.equal(p.shown[0].body.length, 200, 'the body line is bounded');
  p = await finish({ permission: 'granted' }, { msg: undefined });
  assert.equal(p.shown[0].body, '', 'no last message, no body line');

  // A child finishing under a busy parent, then the parent.
  p = page({ bridge: () => Promise.resolve(true) });
  let parent = row('p', { busy: true, asks: '', ms: 5 }), kid = row('k', { busy: true, child: true, asks: '' });
  p.scan([parent, kid]); busy(kid, false); p.scan([parent, kid]); await tick();
  assert.equal(p.native.length, 0, 'child done, parent busy: nothing');
  busy(parent, false); p.scan([parent, kid]); await tick();
  assert.deepEqual(p.native.map(n => n.tag), ['ws-idle:p:5'], 'parent idle: one notification');
  p.scan([parent, kid]); await tick();
  assert.equal(p.native.length, 1, 'exactly once');
  p.scan([parent, kid], 'sessions'); p.scan([parent, kid]); await tick();
  assert.equal(p.native.length, 1, 'an index sessions scan does not disturb the rail baseline');

  // Questions.
  p = page({ bridge: () => Promise.resolve(true) });
  const q1 = row('q', { asks: '4' });
  p.scan([q1]);
  q1.setAttribute('data-asks', '4 9'); p.scan([q1]); await tick();
  assert.deepEqual({ ...p.native[0] }, { title: 'js.notif.ask.title(proj/q)', body: 'js.notif.ask.body.one(1)', tag: 'ws-ask:q:9' });
  p.scan([q1]); await tick();
  assert.equal(p.native.length, 1, 'unchanged asks do not notify');
  q1.setAttribute('data-asks', '4 9 10 11'); p.baseline([q1]); p.scan([q1]); await tick();
  assert.equal(p.native.length, 1, 'a reconnect full rail baselines its questions');
  q1.setAttribute('data-asks', '4 9 10'); p.scan([q1]);
  q1.setAttribute('data-asks', '4 9 10 11'); p.scan([q1]); await tick();
  assert.equal(p.native[1].tag, 'ws-ask:q:11', 'a reopened question notifies');
  q1.setAttribute('data-asks', '1 2 4 9 10 11'); p.scan([q1]); await tick();
  assert.equal(p.native[2].body, 'js.notif.ask.body.other(2)');

  // Held toasts in a hidden tab: shown once visible unless the event ended.
  p = page({ hidden: true });
  const h1 = row('h1', { asks: '' }), h2 = row('h2', { busy: true, asks: '' });
  p.scan([h1, h2]);
  h1.setAttribute('data-asks', '7'); busy(h2, false); p.scan([h1, h2]); await tick();
  assert.equal(p.toasts.length, 0, 'held while hidden');
  h1.setAttribute('data-asks', ''); busy(h2, true); p.scan([h1, h2]);
  p.show();
  assert.equal(p.toasts.length, 0, 'a held question that closed and a held finish whose session works again are dropped');
  p.doc.hidden = true;
  h1.setAttribute('data-asks', '8'); busy(h2, false); p.scan([h1, h2]); await tick();
  p.show();
  assert.equal(p.toasts.length, 2, 'still-current held items toast once the tab shows');

  // Approvals keep their selection and tag, now native first.
  p = page({ bridge: () => Promise.resolve(true), permission: 'granted' });
  const w = row('w', { asks: '' });
  p.scan([w]);
  w.attrs['data-ap'] = '1'; w.attrs['data-ap-tool'] = 'Bash'; w.attrs['data-ap-args'] = 'ls';
  p.scan([w]); await tick();
  assert.deepEqual({ ...p.native[0] }, { title: 'js.notif.title(proj/w)', body: 'Bash · ls', tag: 'ws-approval:w\nBash\nls' });
  assert.equal(p.shown.length, 0, 'approval native true: no browser duplicate');
  w.attrs['data-ap-args'] = 'pwd';
  p.baseline([w]); p.scan([w]); await tick();
  assert.deepEqual(p.native.map(n => n.tag), ['ws-approval:w\nBash\nls', 'ws-approval:w\nBash\npwd'], 'a reconnect rail still notifies a new wait, once');
  p = page({ bridge: () => Promise.resolve(false), permission: 'granted', rid: 'w' });
  const v = row('w', { asks: '' }); p.scan([v]);
  v.attrs['data-ap'] = '1'; v.attrs['data-ap-tool'] = 'Bash'; p.scan([v]); await tick();
  assert.equal(p.native.length + p.shown.length, 0, 'the viewed session\'s own wait stays suppressed');
  p = page({ bridge: () => Promise.resolve(false), permission: 'granted' });
  const x = row('x', { asks: '' }); p.scan([x], 'sessions');
  x.attrs['data-ap'] = '1'; x.attrs['data-ap-tool'] = 'Edit'; p.scan([x], 'sessions'); await tick();
  assert.equal(p.shown[0].tag, 'ws-approval:x\nEdit\n', 'an index sessions wait falls back to the browser');

  console.log('notifications: ok');
})().catch(e => { console.error(e); process.exit(1); });
