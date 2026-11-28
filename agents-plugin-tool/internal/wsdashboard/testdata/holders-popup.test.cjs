'use strict';
// The shell's bottom-bar Holders trigger and popup (assets/dashboard-holders.js)
// in a vm with a minimal DOM: the trigger's state and the no-polling policy,
// the popup's sections, the 1.5s hold on an unattached holder's Stop, the
// inline terminal confirmation, the host anchor's terminal list (plain-text
// folders, stale answers dropped), and the frame bridge for Attach and the
// orphan terminals.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

const src = fs.readFileSync(path.join(__dirname, '..', 'assets', 'dashboard-holders.js'), 'utf8');
const bundle = readAssetSource('wsdashboard', 'dashboard.js');

class El {
  constructor(doc, tag, cls, text) {
    this.doc = doc; this.tag = tag; this.className = cls || ''; this.children = []; this.attrs = {};
    this.parent = null; this.listeners = {}; this.disabled = false; this.title = ''; this.style = { props: {}, setProperty(k, v) { this.props[k] = v; } };
    this._text = text == null ? '' : String(text);
    const self = this;
    this.classList = {
      contains: k => self.className.split(/\s+/).includes(k),
      add: k => { if (!self.classList.contains(k)) self.className = (self.className + ' ' + k).trim(); },
      toggle: (k, on) => {
        const has = self.classList.contains(k);
        if (on === undefined) on = !has;
        if (on && !has) self.classList.add(k);
        if (!on && has) self.className = self.className.split(/\s+/).filter(c => c !== k).join(' ');
        return on;
      }
    };
  }
  get textContent() { return this._text + this.children.map(c => c.textContent).join(''); }
  set textContent(v) { this._text = String(v); this.children.forEach(c => { c.parent = null; }); this.children = []; }
  appendChild(c) {
    if (c.tag === '#fragment') { c.children.slice().forEach(k => this.appendChild(k)); c.children = []; return c; }
    if (c.parent) c.parent.children = c.parent.children.filter(k => k !== c);
    c.parent = this; this.children.push(c); return c;
  }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  hasAttribute(k) { return k in this.attrs; }
  addEventListener(k, fn) { (this.listeners[k] = this.listeners[k] || []).push(fn); }
  focus() { this.doc.activeElement = this; }
  contains(n) { for (; n; n = n.parent) if (n === this) return true; return false; }
  all() { return this.children.flatMap(c => [c, ...c.all()]); }
  matches(sel) {
    const m = /^(\w+)?((?:\.[\w-]+)*)((?:\[[\w-]+(?:="[^"]*")?\])*)$/.exec(sel);
    if (!m) throw new Error('unsupported selector ' + sel);
    if (m[1] && m[1] !== this.tag) return false;
    for (const c of (m[2] || '').split('.').filter(Boolean)) if (!this.classList.contains(c)) return false;
    for (const a of (m[3] || '').match(/\[[^\]]+\]/g) || []) {
      const [, k, v] = /\[([\w-]+)(?:="([^"]*)")?\]/.exec(a);
      if (v === undefined ? !this.hasAttribute(k) : this.getAttribute(k) !== v) return false;
    }
    return true;
  }
  querySelector(sel) { return this.all().find(n => n.matches(sel)) || null; }
  querySelectorAll(sel) { return this.all().filter(n => n.matches(sel)); }
  closest(sel) { for (let n = this; n; n = n.parent) if (n.matches(sel)) return n; return null; }
}

// page runs the holders fragment as the shell runs it: a bottom bar with the
// trigger, the rail popover controller stubbed, fetch and post recorded.
function page(opts = {}) {
  const doc = { activeElement: null, hidden: false, listeners: {}, addEventListener(k, fn) { (this.listeners[k] = this.listeners[k] || []).push(fn); } };
  const mk = (tag, cls, text) => new El(doc, tag, cls, text);
  doc.createDocumentFragment = () => mk('#fragment');
  doc.createTextNode = t => mk('#text', '', t);
  const bar = mk('div', 'shell-bottom'), trig = bar.appendChild(mk('button', 'hd-trigger'));
  trig.appendChild(mk('span', 'hd-count', '0'));
  const fetches = [], posts = [], sent = [], framePosts = [], holds = [];
  const replies = opts.replies || {};
  const win = {
    fetch: true, listeners: {}, addEventListener(k, fn) { (this.listeners[k] = this.listeners[k] || []).push(fn); },
    WsHoldConfirm: { delegate(container, sel, h) { holds.push({ container, sel, h }); } },
    WsTerm: opts.WsTerm
  };
  const ctx = {
    window: win, doc, shell: opts.shell !== false, botBarEl: bar, mk, wsT: (k, ...a) => (a = a.filter(x => x !== undefined)).length ? k + '(' + a.join(',') + ')' : k,
    gIcon: n => mk('svg', 'icon-' + n), CSS: { escape: s => s }, Date,
    rp: { P: null }, rpPop: () => mk('div', 'rpop'), railPopClose() { ctx.rp.P = null; }, railPopPlace() {},
    wtAge: () => 'now', wtStamp: () => 'stamp',
    shOn: opts.shOn || null, shSend: (ent, kind, m) => sent.push([kind, m]), shPost: (kind, m) => framePosts.push([kind, m]),
    post: (url, body, cb) => posts.push({ url, cb }),
    fetch(url, o) {
      return new Promise((resolve, reject) => {
        fetches.push({ url, o, resolve, reject, reply(v, ok = true) {
          resolve({ ok, status: ok ? 200 : 500, text: () => Promise.resolve(JSON.stringify(v)), json: () => Promise.resolve(v) });
        } });
        if (replies[url] !== undefined) fetches[fetches.length - 1].reply(replies[url]);
      });
    }
  };
  vm.createContext(ctx);
  vm.runInContext(src + '\n;this.__hd = hd;', ctx);
  const open = () => { const P = ctx.hdPop(); ctx.rp.P = P; P.open(); return P; };
  return { ctx, doc, trig, fetches, posts, sent, framePosts, holds, open, hd: ctx.__hd };
}
const settle = () => new Promise(r => setImmediate(r));
const act = (P, a, id) => P.list.querySelector('button[data-act="' + a + '"][data-id="' + id + '"]');
const click = (P, b) => P.list.listeners.click.forEach(fn => fn({ target: b }));

const LIST = {
  can_stop: true,
  holders: [
    { id: 'h-run', pid: 41, attached: true, restart_safe: true, session: { kind: 'agent-worker', label: 'worker one', backend: 'claude' }, started_at: 1 },
    { id: 'durable.lead-a', pid: 0, attached: true, session: { kind: 'lead' } },
    { id: 'h-lost', pid: 42, attached: false, cause: 'no_answer', session: { kind: 'exec', exec_id: 'exec-x' } },
    { id: 'h-unsafe', pid: 43, attached: true, restart_safe: false, session: { kind: 'lead', label: 'lead b' } }
  ],
  failures: [{ message: 'spawn <b>failed</b>', at: 2 }]
};
const TERMS = { anchor: true, terminals: [
  { id: 't-run', cwd: '/work/<img onerror=x>/', state: 'running', session: 'host0' },
  { id: 't-done', cwd: '/gone', state: 'exited' }
] };

test('the trigger counts running OS holders, warns on attention, and polls nothing while closed', async () => {
  const h = page();
  assert.equal(h.fetches.length, 1, 'one /holders read on load');
  assert.equal(h.fetches[0].url, '/holders');
  assert.deepEqual([h.fetches[0].o.cache, h.fetches[0].o.credentials], ['no-store', 'same-origin']);
  h.fetches[0].reply({ holders: [], failures: [] });
  await settle();
  assert.equal(h.trig.getAttribute('data-state'), 'zero');
  assert.equal(h.trig.querySelector('.hd-count').textContent, '0');

  h.ctx.hdFetch();
  h.fetches[1].reply({ holders: [LIST.holders[0], LIST.holders[1]], failures: [] });
  await settle();
  assert.equal(h.trig.getAttribute('data-state'), 'ok');
  assert.equal(h.trig.querySelector('.hd-count').textContent, '1', 'a durable in-process session is not an OS process');
  assert.match(h.trig.title, /^js\.holders\.title — js\.holders\.sum\.running\(1\)$/);
  assert.equal(h.trig.getAttribute('aria-label'), h.trig.title);

  h.ctx.hdFetch();
  h.fetches[2].reply(LIST);
  await settle();
  assert.equal(h.trig.getAttribute('data-state'), 'warn');
  assert.equal(h.trig.querySelector('.hd-count').textContent, '3');
  assert.match(h.trig.title, /sum\.lost\(1\).*sum\.warn\(1\).*sum\.fail\(1\)/);

  // A return to the tab refreshes at most once per HD_FRESH; a navigation
  // with the popup closed does the same.
  h.ctx.hdFresh(); h.ctx.hdNavigated();
  assert.equal(h.fetches.length, 3, 'a fresh read is rate-limited');
  h.hd.at = 0; h.doc.hidden = true; h.ctx.hdFresh();
  assert.equal(h.fetches.length, 3, 'a hidden tab never reads');
  h.doc.hidden = false; h.ctx.hdFresh();
  assert.equal(h.fetches.length, 4);
  assert.equal(h.fetches.filter(f => f.url !== '/holders').length, 0, 'a closed popup never reads terminals');
});

test('the popup groups attention, running, and terminals; Stop is a hold only for an unattached holder', async () => {
  const h = page({ replies: { '/holders': LIST, '/s/host0/terminals': TERMS } });
  await settle();
  const P = h.open();
  await settle();
  const urls = h.fetches.map(f => f.url);
  assert.deepEqual(urls, ['/holders', '/holders', '/s/host0/terminals'], 'opening reads holders and the host terminals');
  const tf = h.fetches[2];
  assert.deepEqual([tf.o.cache, tf.o.credentials], ['no-store', 'same-origin']);

  const secs = P.list.querySelectorAll('.hd-sec').map(s => [s.querySelector('.hd-lbl').textContent, s.querySelector('.hd-cnt').textContent]);
  assert.deepEqual(secs, [['js.holders.sec.attention', '3'], ['js.holders.sec.running', '1'], ['js.holders.sec.terms', '2']]);
  assert.equal(P.sum.getAttribute('data-tone'), 'warn');
  assert.deepEqual(P.chips.children.map(c => c.className), ['hd-chip', 'hd-chip hd-warn', 'hd-chip hd-warn', 'hd-chip hd-fail']);

  const holds = P.list.querySelectorAll('.hd-hold');
  assert.deepEqual(holds.map(b => b.getAttribute('data-id')), ['h-lost'], 'only the unattached OS holder offers Stop');
  assert.equal(holds[0].getAttribute('data-act'), 'holder-stop');
  assert.ok(holds[0].classList.contains('d-hold-btn'));
  const lost = P.list.querySelector('.hd-row.hd-lost');
  assert.equal(lost.querySelector('.hd-name').textContent, 'exec-x', 'an unlabelled exec shows its exec id');
  assert.equal(lost.querySelector('.hd-cause').textContent, 'js.hd.cause.noanswer');
  const dur = P.list.querySelector('.hd-row[data-id="durable.lead-a"]');
  assert.equal(dur.querySelector('.hd-name').textContent, 'lead-a');
  assert.equal(dur.querySelector('.hd-badge').textContent, 'js.holders.inproc');
  assert.equal(P.list.querySelector('.hd-row[data-id="h-unsafe"]').querySelector('.hd-unsafe').textContent, 'js.hd.unsafe');
  assert.equal(P.list.querySelector('.hd-fail').querySelector('.hd-msg').textContent, 'spawn <b>failed</b>', 'a failure message is text');

  const run = P.list.querySelector('.hd-trow[data-row="t-run"]');
  assert.equal(run.querySelector('.hd-name').textContent, '<img onerror=x>', 'the folder name is plain text');
  assert.equal(run.querySelector('.hd-cwd').textContent, TERMS.terminals[0].cwd);
  assert.equal(run.querySelector('.hd-cwd').title, TERMS.terminals[0].cwd);
  assert.equal(act(P, 'term-attach', 't-run').disabled, true, 'Attach is off without a frame offering terminals');
  assert.equal(act(P, 'term-attach', 't-done'), null, 'an exited terminal cannot attach');

  // Without can_stop no Stop is offered at all.
  const ro = page({ replies: { '/holders': { ...LIST, can_stop: false }, '/s/host0/terminals': TERMS } });
  await settle();
  const RP = ro.open();
  await settle();
  assert.equal(RP.list.querySelectorAll('.hd-hold').length, 0);
});

test('the hold confirms a stop, a short press explains it, and a render waits for the hold', async () => {
  const h = page({ replies: { '/holders': LIST, '/s/host0/terminals': TERMS } });
  await settle();
  const P = h.open();
  await settle();
  assert.equal(h.holds.length, 1);
  assert.equal(h.holds[0].container, P.list);
  assert.equal(h.holds[0].sel, '.hd-hold');
  const H = h.holds[0].h, b = P.list.querySelector('.hd-hold');

  H.onStart(b);
  h.ctx.hdRender();
  assert.equal(P.list.querySelector('.hd-hold'), b, 'a render during the hold keeps its button');
  assert.equal(h.hd.dirty, true);
  H.onCancel(b, 'early-release');
  assert.equal(P.note.textContent, 'js.holders.hold.hint');
  assert.notEqual(P.list.querySelector('.hd-hold'), b, 'the deferred render runs once the hold ends');
  assert.equal(h.posts.length, 0, 'a short press stops nothing');

  const b2 = P.list.querySelector('.hd-hold');
  H.onStart(b2);
  H.onConfirm(b2, true);
  assert.deepEqual(h.posts.map(p => p.url), ['/holders/h-lost/stop']);
  assert.equal(b2.disabled, true);
  const before = h.fetches.length;
  h.posts[0].cb(true, '');
  assert.equal(P.note.textContent, 'js.holders.stopped(exec-x)');
  assert.equal(h.fetches.length, before + 2, 'a stop re-reads the holders and terminals');

  H.onConfirm(b2, true);
  assert.equal(h.posts.length, 2);
  h.posts[1].cb(false, 'gone');
  assert.equal(P.note.textContent, 'js.hd.stopfail(gone)');
  assert.ok(P.note.classList.contains('hd-err'));
});

test('a running terminal confirms inline; Escape backs out; a closing terminal posts once', async () => {
  const h = page({ replies: { '/holders': LIST, '/s/host0/terminals': TERMS } });
  await settle();
  const P = h.open();
  await settle();
  click(P, act(P, 'term-stop', 't-run'));
  const cf = P.list.querySelector('.hd-confirm');
  assert.equal(cf.getAttribute('role'), 'alertdialog');
  assert.equal(h.doc.activeElement, act(P, 'term-yes', 't-run'), 'focus moves into the confirmation');
  assert.equal(h.posts.length, 0);
  assert.equal(P.back(), true, 'Escape dismisses the confirmation first');
  assert.equal(P.list.querySelector('.hd-confirm'), null);
  assert.equal(h.doc.activeElement, act(P, 'term-stop', 't-run'), 'focus returns to Stop');
  assert.equal(P.back(), false, 'a second Escape closes the popup');

  click(P, act(P, 'term-stop', 't-run'));
  click(P, act(P, 'term-no', 't-run'));
  assert.equal(P.list.querySelector('.hd-confirm'), null);
  click(P, act(P, 'term-stop', 't-run'));
  const yes = act(P, 'term-yes', 't-run');
  click(P, yes);
  click(P, yes);
  assert.deepEqual(h.posts.map(p => p.url), ['/s/host0/terminals/t-run/close'], 'one close per terminal');
  assert.equal(act(P, 'term-stop', 't-run').disabled, true, 'Stop is off while it closes');
  const before = h.fetches.length;
  h.posts[0].cb(true);
  assert.equal(h.fetches.length, before + 1, 'the terminal list is re-read');
  assert.equal(h.fetches.at(-1).url, '/s/host0/terminals');

  click(P, act(P, 'term-stop', 't-done'));
  assert.equal(h.posts.at(-1).url, '/s/host0/terminals/t-done/close', 'an exited terminal goes without asking');
});

test('a stale or anchorless terminal answer shows no host terminals', async () => {
  const h = page({ replies: { '/holders': LIST } });
  await settle();
  const P = h.open();
  const first = h.fetches.find(f => f.url === '/s/host0/terminals');
  h.ctx.hdTerms();
  const second = h.fetches.filter(f => f.url === '/s/host0/terminals')[1];
  second.reply({ anchor: true, terminals: [] });
  await settle();
  first.reply(TERMS);
  await settle();
  assert.equal(P.list.querySelectorAll('.hd-term').length, 0, 'the stale answer is dropped');

  h.ctx.hdTerms();
  h.fetches.at(-1).reply({ anchor: false, terminals: TERMS.terminals });
  await settle();
  assert.equal(P.list.querySelectorAll('.hd-term').length, 0, 'no anchor, no host terminals');
  assert.ok(P.list.textContent.includes('js.holders.noterms'));
});

test('the frame bridge lists orphans for the shell and attaches a terminal on its page', async () => {
  const frameDoc = { documentElement: { hasAttribute: k => k === 'data-term' } };
  const shOn = { el: { contentDocument: frameDoc }, ready: true };
  const h = page({ shOn, replies: { '/holders': LIST, '/s/host0/terminals': { anchor: true, terminals: [] } } });
  await settle();
  const P = h.open();
  await settle();
  const req = h.sent.find(s => s[0] === 'term-list-req');
  assert.ok(req, 'an open popup asks the active frame for its orphans');
  h.ctx.hdFrameTerms(shOn, { seq: req[1].seq + 1, orphans: [{ id: 'o-stale' }] });
  assert.equal(P.list.querySelectorAll('.hd-term').length, 0, 'an answer to another request is dropped');
  h.ctx.hdFrameTerms(shOn, { seq: req[1].seq, orphans: [{ id: 'o-1', cwd: '/x' }, null, { id: 5 }] });
  assert.equal(P.list.querySelectorAll('.hd-term').length, 1, 'malformed orphans are dropped');
  const at = act(P, 'term-attach', 'o-1');
  assert.equal(at.disabled, false);
  click(P, at);
  assert.equal(JSON.stringify(h.sent.at(-1)), JSON.stringify(['term-attach', { term: { id: 'o-1', cwd: '/x' } }]));

  // The frame side answers from WsTerm, and attaches what the shell sends.
  const attached = [];
  const f = page({ shell: false, WsTerm: { list: cb => cb(null, { orphans: [{ id: 'o-2' }] }), attach: t => attached.push(t.id) } });
  assert.equal(f.fetches.length, 0, 'a page without the trigger reads nothing');
  f.ctx.hdFrameTermReq({ wsShell: 'term-list-req', seq: 7 });
  assert.equal(JSON.stringify(f.framePosts), JSON.stringify([['term-list', { seq: 7, orphans: [{ id: 'o-2' }] }]]));
  f.ctx.hdFrameTermReq({ wsShell: 'term-attach', term: { id: 'o-2' } });
  f.ctx.hdFrameTermReq({ wsShell: 'term-attach', term: { id: 3 } });
  assert.deepEqual(attached, ['o-2']);
  const none = page({ shell: false });
  none.ctx.hdFrameTermReq({ wsShell: 'term-list-req', seq: 9 });
  assert.equal(JSON.stringify(none.framePosts), JSON.stringify([['term-list', { seq: 9, orphans: [] }]]), 'a page without terminals answers empty');
});

test('the holders fragment ships in the dashboard bundle after the rail popovers', () => {
  const a = bundle.indexOf('  // --- holders: the shell\'s bottom-bar Holders trigger and popup');
  const b = bundle.indexOf('function railPopOpen(');
  assert.ok(a > 0 && b > 0 && b < a, 'the popover controller precedes the holders fragment');
  assert.ok(bundle.includes('fetch("/s/host0/terminals"'), 'the popup reads the stable anchor\'s unfiltered terminal list');
  assert.doesNotMatch(bundle, /setInterval\([^)]*hd/, 'nothing polls the holders');
});
