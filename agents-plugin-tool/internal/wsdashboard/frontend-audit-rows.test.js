const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

const rowsSrc = fs.readFileSync(__dirname + '/assets/dashboard-audit-rows.js', 'utf8');

function loadAuditRowsHelpers() {
  const ctx = {
    Date,
    Math,
    Number,
    String,
    Array,
    Object,
    JSON,
    Promise,
    isFinite,
    wsT(key) {
      return key;
    }
  };
  vm.createContext(ctx);
  const ctrlIdx = rowsSrc.indexOf('  // Controller active only');
  assert.ok(ctrlIdx > 0, 'helper slice marker exists');
  vm.runInContext(rowsSrc.slice(0, ctrlIdx), ctx);
  return ctx;
}

function makeFakeDOM() {
  class ClassList {
    constructor(el) {
      this.el = el;
      this.set = new Set();
    }
    _syncFrom(cls) {
      this.set = new Set(String(cls || '').trim().split(/\s+/).filter(Boolean));
    }
    _push() {
      this.el._className = Array.from(this.set).join(' ');
    }
    add(...names) {
      for (const n of names) if (n) this.set.add(n);
      this._push();
    }
    remove(...names) {
      for (const n of names) this.set.delete(n);
      this._push();
    }
    contains(n) {
      return this.set.has(n);
    }
    toggle(n, force) {
      const on = force === undefined ? !this.set.has(n) : !!force;
      if (on) this.set.add(n);
      else this.set.delete(n);
      this._push();
      return on;
    }
  }

  class El {
    constructor(tag) {
      this.tagName = String(tag || 'DIV').toUpperCase();
      this.attrs = Object.create(null);
      this.children = [];
      this.parentNode = null;
      this._className = '';
      this.classList = new ClassList(this);
      this.listeners = Object.create(null);
      this._textContent = '';
      this.tabIndex = 0;
      this.focused = false;
      this.id = '';
    }
    get isConnected() {
      let cur = this;
      while (cur.parentNode) cur = cur.parentNode;
      return cur._isRoot === true;
    }
    get className() {
      return this._className;
    }
    set className(v) {
      this._className = String(v || '');
      this.classList._syncFrom(this._className);
    }
    get textContent() {
      if (this.children.length === 0) return this._textContent;
      return this.children.map((c) => c.textContent).join('');
    }
    set textContent(v) {
      this.children = [];
      this._textContent = String(v || '');
    }
    setAttribute(k, v) {
      if (k === 'id') this.id = String(v);
      this.attrs[k] = String(v);
    }
    getAttribute(k) {
      if (k === 'id' && this.id) return this.id;
      return Object.prototype.hasOwnProperty.call(this.attrs, k) ? this.attrs[k] : null;
    }
    hasAttribute(k) {
      return Object.prototype.hasOwnProperty.call(this.attrs, k);
    }
    appendChild(child) {
      if (child.tagName === '#text') {
        this._textContent += child._textContent;
        return child;
      }
      if (child.parentNode) child.parentNode.removeChild(child);
      child.parentNode = this;
      this.children.push(child);
      return child;
    }
    insertBefore(child, ref) {
      if (!ref) return this.appendChild(child);
      if (child.parentNode) child.parentNode.removeChild(child);
      const idx = this.children.indexOf(ref);
      child.parentNode = this;
      if (idx < 0) this.children.push(child);
      else this.children.splice(idx, 0, child);
      return child;
    }
    removeChild(child) {
      const idx = this.children.indexOf(child);
      if (idx >= 0) {
        this.children.splice(idx, 1);
        child.parentNode = null;
      }
      return child;
    }
    get firstChild() { return this.children[0] || null; }
    get firstElementChild() { return this.children[0] || null; }
    get lastElementChild() { return this.children[this.children.length - 1] || null; }
    get nextSibling() {
      if (!this.parentNode) return null;
      const i = this.parentNode.children.indexOf(this);
      return i >= 0 ? this.parentNode.children[i + 1] || null : null;
    }
    get nextElementSibling() { return this.nextSibling; }
    get previousElementSibling() {
      if (!this.parentNode) return null;
      const i = this.parentNode.children.indexOf(this);
      return i > 0 ? this.parentNode.children[i - 1] || null : null;
    }
    addEventListener(ev, fn) {
      if (!this.listeners[ev]) this.listeners[ev] = [];
      this.listeners[ev].push(fn);
    }
    dispatchEvent(ev) {
      ev.target = ev.target || this;
      for (const fn of this.listeners[ev.type] || []) fn(ev);
    }
    focus() { this.focused = true; }
    scrollIntoView() {}
    matches(sel) {
      for (const rawPart of sel.split(',')) {
        const part = rawPart.trim();
        if (!part) continue;
        if (matchSingle(this, part)) return true;
      }
      return false;
    }
    closest(sel) {
      let cur = this;
      while (cur && cur.tagName) {
        if (cur.matches(sel)) return cur;
        cur = cur.parentNode;
      }
      return null;
    }
    querySelectorAll(sel) {
      const out = [];
      const walk = (n) => {
        for (const c of n.children || []) {
          if (c.matches(sel)) out.push(c);
          walk(c);
        }
      };
      walk(this);
      return out;
    }
    querySelector(sel) {
      return this.querySelectorAll(sel)[0] || null;
    }
  }

  function matchSingle(el, part) {
    if (part.includes(' ')) {
      const segs = part.trim().split(/\s+/);
      if (!matchSimple(el, segs[segs.length - 1])) return false;
      let cur = el.parentNode;
      let idx = segs.length - 2;
      while (cur && idx >= 0) {
        if (matchSimple(cur, segs[idx])) idx--;
        cur = cur.parentNode;
      }
      return idx < 0;
    }
    return matchSimple(el, part);
  }

  function matchSimple(el, simple) {
    let rest = simple;
    const idMatch = /^#([A-Za-z0-9_-]+)/.exec(rest);
    if (idMatch) {
      if (el.id !== idMatch[1]) return false;
      rest = rest.slice(idMatch[0].length);
    }
    const tagMatch = /^([A-Za-z0-9_-]+)/.exec(rest);
    if (tagMatch) {
      if (el.tagName !== tagMatch[1].toUpperCase()) return false;
      rest = rest.slice(tagMatch[0].length);
    }
    const clsMatches = rest.match(/\.[A-Za-z0-9_-]+/g) || [];
    for (const c of clsMatches) {
      if (!el.classList.contains(c.slice(1))) return false;
    }
    const attrMatches = rest.match(/\[[^\]]+\]/g) || [];
    for (const am of attrMatches) {
      const eq = am.indexOf('=');
      if (eq < 0) {
        const k = am.slice(1, -1);
        if (!el.hasAttribute(k)) return false;
      } else {
        const k = am.slice(1, eq);
        const v = am.slice(eq + 1, -1).replace(/^['"]|['"]$/g, '');
        if (el.getAttribute(k) !== v) return false;
      }
    }
    return true;
  }

  const rootEl = new El('HTML');
  rootEl._isRoot = true;
  rootEl.setAttribute('data-rid', 's1');
  const convo = new El('DIV');
  convo.id = 'convo';
  const rEntries = new El('DIV');
  rEntries.id = 'r-entries';
  const centries = new El('DIV');
  centries.className = 'centries';
  rEntries.appendChild(centries);
  convo.appendChild(rEntries);
  rootEl.appendChild(convo);

  const doc = {
    listeners: Object.create(null),
    createElement(tag) { return new El(tag); },
    createTextNode(t) {
      const n = new El('#text');
      n._textContent = String(t);
      return n;
    },
    getElementById(id) {
      if (id === 'convo') return convo;
      if (id === 'r-entries') return rEntries;
      return null;
    },
    addEventListener(ev, fn) {
      if (!this.listeners[ev]) this.listeners[ev] = [];
      this.listeners[ev].push(fn);
    },
    dispatchEvent(ev) {
      for (const fn of this.listeners[ev.type] || []) fn(ev);
    }
  };
  return { El, rootEl, convo, rEntries, centries, doc };
}

test('auParseRFC3339Ns and auCmpNs order sub-millisecond timestamps and offsets accurately', () => {
  const h = loadAuditRowsHelpers();
  const a = h.auParseRFC3339Ns('2026-11-20T10:00:00.123456789Z');
  const b = h.auParseRFC3339Ns('2026-11-20T10:00:00.123456790Z');
  const c = h.auParseRFC3339Ns('2026-11-20T19:00:00.123456789+09:00');
  assert.ok(a && b && c);
  assert.equal(h.auCmpNs(a, b), -1);
  assert.equal(h.auCmpNs(b, a), 1);
  assert.equal(h.auCmpNs(a, c), 0);
  assert.equal(h.auParseRFC3339Ns('not-a-timestamp'), null);
});

test('auCmpAuditEvent breaks same-nanosecond ties numerically on seq then lexicographically on id', () => {
  const h = loadAuditRowsHelpers();
  const ts = '2026-11-20T10:00:00.000000100Z';
  assert.equal(h.auCmpAuditEvent({ at: ts, seq: 9, id: 'z' }, { at: ts, seq: 10, id: 'a' }), -1);
  assert.equal(h.auCmpAuditEvent({ at: ts, seq: 10, id: 'a' }, { at: ts, seq: 10, id: 'b' }), -1);
  assert.equal(h.auCmpAuditEvent({ at: ts, seq: 10, id: 'a' }, { at: ts, seq: 10, id: 'a' }), 0);
});

test('auIsHiddenAuditKind and auIsSessionStartEvent classify all audit kinds accurately', () => {
  const h = loadAuditRowsHelpers();
  for (const k of ['thinking', 'hook', 'system_reminder', 'subagent', 'compaction', 'system', 'other']) {
    assert.equal(h.auIsHiddenAuditKind(k), true, `expected ${k} to be hidden kind`);
  }
  for (const k of ['tool_call', 'tool_result', 'assistant_text', 'user_input', 'bogus']) {
    assert.equal(h.auIsHiddenAuditKind(k), false, `expected ${k} to not be hidden kind`);
  }
  assert.equal(h.auIsSessionStartEvent({ kind: 'system', subkind: 'system_prompt' }), true);
  assert.equal(h.auIsSessionStartEvent({ kind: 'system', subkind: 'session_start' }), true);
  assert.equal(h.auIsSessionStartEvent({ kind: 'system', subkind: 'system:init' }), true);
  assert.equal(h.auIsSessionStartEvent({ kind: 'system', subkind: 'rate_limit' }), false);
  assert.equal(h.auIsSessionStartEvent({ kind: 'hook', subkind: 'system_prompt' }), false);
});

test('auReadKindsPref and auWriteKindsPref default cleanly and persist toggles', () => {
  const h = loadAuditRowsHelpers();
  const store = new Map();
  const storage = {
    getItem(k) { return store.has(k) ? store.get(k) : null; },
    setItem(k, v) { store.set(k, String(v)); }
  };
  const def = h.auReadKindsPref(storage, 'ws.dashboard.auditkinds');
  assert.equal(def.thinking, true);
  assert.equal(def.other, true);

  def.thinking = false;
  def.unknown_ignored = false;
  h.auWriteKindsPref(storage, 'ws.dashboard.auditkinds', def);
  const reread = h.auReadKindsPref(storage, 'ws.dashboard.auditkinds');
  assert.equal(reread.thinking, false);
  assert.equal(reread.hook, true);
  assert.equal(reread.unknown_ignored, undefined);

  store.set('ws.dashboard.auditkinds', '{broken json');
  const fallback = h.auReadKindsPref(storage, 'ws.dashboard.auditkinds');
  assert.equal(fallback.thinking, true);
});

test('auInterleavePlan attaches matching call_ids, falls back orphans chronologically, and anchors sysStart', () => {
  const h = loadAuditRowsHelpers();
  const entries = [
    { el: 'E1', key: 'k1', at: '2026-11-20T10:00:01Z' },
    { el: 'E2', key: 'k2', at: '2026-11-20T10:00:05Z' }
  ];
  const renderedCallIds = { c1: true };
  const events = [
    { id: 'e-attached', seq: 5, kind: 'hook', at: '2026-11-20T10:00:02Z', call_id: 'c1' },
    { id: 'e-orphan', seq: 6, kind: 'thinking', at: '2026-11-20T10:00:03Z', call_id: 'c-missing' },
    { id: 'e-sys-eq', seq: 1, kind: 'system', subkind: 'system_prompt', at: '2026-11-20T10:00:01Z' },
    { id: 'e-tail', seq: 8, kind: 'compaction', at: '2026-11-20T10:00:08Z' },
    { id: 'e-non-hidden', seq: 2, kind: 'tool_call', at: '2026-11-20T10:00:02Z', call_id: 'c1' }
  ];
  const plan = h.auInterleavePlan(entries, events, renderedCallIds);
  assert.deepEqual(Array.from(plan.attached.c1.map((e) => e.id)), ['e-attached']);
  assert.deepEqual(Array.from(plan.standalone.map((s) => [s.event.id, s.sysStart, s.beforeIndex])), [
    ['e-sys-eq', true, 0],
    ['e-orphan', false, 1],
    ['e-tail', false, 2]
  ]);
});

test('controller executes (607)/(681) paging, open-below oldest, 410 reset, 503 retry, and scoped J/K/Enter keys', async () => {
  const dom = makeFakeDOM();
  const e1 = new dom.El('DIV');
  e1.setAttribute('data-entry', 'k2');
  e1.setAttribute('data-at', '2026-11-20T10:00:02Z');
  const tools = new dom.El('DIV');
  tools.className = 'au-tools';
  tools.setAttribute('data-entry', 'k3');
  tools.setAttribute('data-at', '2026-11-20T10:00:03Z');
  const calls = new dom.El('OL');
  calls.className = 'au-calls';
  const r1 = new dom.El('LI');
  r1.className = 'au-row au-done';
  r1.setAttribute('data-call', 'c1');
  calls.appendChild(r1);
  tools.appendChild(calls);
  dom.centries.appendChild(e1);
  dom.centries.appendChild(tools);

  const fetched = [];
  const opened = [];
  const store = new Map();
  let mode = 'normal';

  const fetchFn = async (url) => {
        fetched.push(url);
        if (mode === '503') return { ok: false, status: 503, async json() { return {}; } };
        if (mode === '410' && url.includes('after=')) return { ok: false, status: 410, async json() { return {}; } };
        if (url.includes('until=2026-11-20T10%3A00%3A01Z')) {
          return {
            ok: true, status: 200,
            async json() {
              return {
                events: [{ id: 'ev-sys', seq: 1, kind: 'system', subkind: 'system_prompt', at: '2026-11-20T10:00:00Z' }],
                end: true
              };
            }
          };
        }
        if (url.includes('until=2026-11-20T10%3A00%3A02Z')) {
          return {
            ok: true, status: 200,
            async json() {
              return {
                events: [{ id: 'ev-think', seq: 3, kind: 'thinking', at: '2026-11-20T10:00:01.500000000Z', title: 'thought' }],
                end: true
              };
            }
          };
        }
        if (url.includes('after=cur2')) return { ok: true, status: 200, async json() { return { events: [], after: 'cur2' }; } };
        if (url.includes('after=cur1')) {
          return {
            ok: true, status: 200,
            async json() {
              return {
                events: [{ id: 'ev-other', seq: 9, kind: 'other', at: '2026-11-20T10:00:04Z', title: 'raw item' }],
                after: 'cur2', end: true
              };
            }
          };
        }
        return {
          ok: true, status: 200,
          async json() {
            return {
              events: [{ id: 'ev-hook', seq: 4, kind: 'hook', at: '2026-11-20T10:00:03Z', call_id: 'c1', subkind: 'PreToolUse' }],
              after: 'cur1', end: true
            };
          }
        };
      };

  const ctx = {
    Date, Math, Number, String, Array, Object, JSON, Promise, isFinite,
    encodeURIComponent,
    fetch: fetchFn,
    root: dom.rootEl,
    rid: 's1',
    doc: dom.doc,
    $(id) { return dom.doc.getElementById(id); },
    region(name) { return name === 'entries' ? dom.rEntries : null; },
    wsT(k) { return k; },
    CustomEvent: function CustomEvent(type, init) { this.type = type; this.detail = (init || {}).detail || {}; },
    window: {
      localStorage: {
        getItem(k) { return store.has(k) ? store.get(k) : null; },
        setItem(k, v) { store.set(k, String(v)); }
      },
      getSelection() { return { isCollapsed: true }; },
      fetch: fetchFn
    }
  };
  ctx.window.WsAudit = {
    fmtBytes: (n) => `${n} B`,
    openCall(id) { opened.push(['call', id]); },
    openEvent(id, opts) { opened.push(['event', id, opts && opts.tab]); },
    visibleRows() { return dom.centries.querySelectorAll('.au-row[data-call], .au-hid[data-event-id], .au-sysrow[data-event-id]').filter((el) => !el.classList.contains('au-kind-hidden')); },
    syncSelection() {}
  };
  vm.createContext(ctx);
  vm.runInContext(rowsSrc, ctx);

  // Allow initial queued sync to complete
  await new Promise((r) => setTimeout(r, 20));
  assert.ok(fetched[0].includes('since=2026-11-20T10%3A00%3A02Z') && !fetched[0].includes('until='));
  assert.ok(dom.centries.querySelector('.au-hid.au-inner[data-event-id="ev-hook"]'));

  // Older history page arrives with end: true -> fetches [T1, T2) AND open-below [, T1)
  const e0 = new dom.El('DIV');
  e0.setAttribute('data-entry', 'k1');
  e0.setAttribute('data-at', '2026-11-20T10:00:01Z');
  dom.centries.insertBefore(e0, e1);
  dom.doc.dispatchEvent({ type: 'ws-history-page', detail: { prevOldestAt: '2026-11-20T10:00:02Z', oldestAt: '2026-11-20T10:00:01Z', end: true, empty: false } });
  await new Promise((r) => setTimeout(r, 20));
  assert.ok(fetched.some((u) => u.includes('since=2026-11-20T10%3A00%3A01Z') && u.includes('until=2026-11-20T10%3A00%3A02Z')));
  assert.ok(fetched.some((u) => !u.includes('since=') && u.includes('until=2026-11-20T10%3A00%3A01Z')));
  assert.ok(dom.centries.querySelector('.au-sysrow[data-event-id="ev-sys"]'));
  assert.ok(dom.centries.querySelector('.au-hid[data-event-id="ev-think"]'));

  // 503 preserves cur1 for subsequent retry; 410 resets once and recovers
  mode = '503';
  dom.doc.dispatchEvent({ type: 'ws-entries-merged', detail: { oldestAt: '2026-11-20T10:00:01Z', empty: false } });
  await new Promise((r) => setTimeout(r, 20));
  mode = 'normal';
  dom.doc.dispatchEvent({ type: 'ws-entries-merged', detail: { oldestAt: '2026-11-20T10:00:01Z', empty: false } });
  await new Promise((r) => setTimeout(r, 20));
  assert.ok(fetched.filter((u) => u.includes('after=cur1')).length >= 2, '503 kept after=cur1 for retry');
  assert.ok(dom.centries.querySelector('.au-hid[data-event-id="ev-other"]'));

  mode = '410';
  dom.doc.dispatchEvent({ type: 'ws-entries-merged', detail: { oldestAt: '2026-11-20T10:00:01Z', empty: false } });
  await new Promise((r) => setTimeout(r, 20));
  assert.ok(fetched.some((u) => u.includes('after=cur2')), '410 triggered once then reset from floor');

  // Kind filter chip toggle hides thinking rows and persists preference
  const thinkChip = dom.convo.querySelector('.au-fchip[data-au-kind="thinking"]');
  dom.convo.querySelector('.au-bar').dispatchEvent({ type: 'click', target: thinkChip });
  assert.equal(dom.centries.querySelector('.au-hid[data-event-id="ev-think"]').classList.contains('au-kind-hidden'), true);
  assert.equal(JSON.parse(store.get('ws.dashboard.auditkinds')).thinking, false);

  // Scoped J/K/Enter: skips hidden thinking, ignores outside containers (.au-win, #composer)
  const composer = new dom.El('DIV');
  composer.id = 'composer';
  const composerBtn = new dom.El('BUTTON');
  composer.appendChild(composerBtn);
  dom.doc.dispatchEvent({ type: 'keydown', key: 'j', target: composerBtn, preventDefault() { throw new Error('must not fire in #composer'); } });

  dom.doc.dispatchEvent({ type: 'keydown', key: 'j', target: dom.convo, preventDefault() {} });
  assert.equal(dom.centries.querySelector('.au-sysrow').focused, true);
  dom.doc.dispatchEvent({ type: 'keydown', key: 'j', target: dom.centries.querySelector('.au-sysrow'), preventDefault() {} });
  // Skips thinking (hidden) and lands on r1 (c1)
  assert.equal(r1.focused, true);
  dom.doc.dispatchEvent({ type: 'keydown', key: 'j', target: r1, preventDefault() {} });
  const hookEl = dom.centries.querySelector('.au-hid[data-event-id="ev-hook"]');
  assert.equal(hookEl.focused, true);
  dom.doc.dispatchEvent({ type: 'keydown', key: 'j', target: hookEl, preventDefault() {} });
  const otherEl = dom.centries.querySelector('.au-hid[data-event-id="ev-other"]');
  assert.equal(otherEl.focused, true);
  dom.doc.dispatchEvent({ type: 'keydown', key: 'Enter', target: otherEl, preventDefault() {} });
  assert.deepEqual(opened[opened.length - 1], ['event', 'ev-other', 'raw']);
});


function raceRowsHarness(fetchFn) {
  const dom = makeFakeDOM();
  const ctx = {
    Date, Math, Number, String, Array, Object, JSON, Promise, isFinite, encodeURIComponent,
    fetch: fetchFn, root: dom.rootEl, rid: 's1', doc: dom.doc,
    $(id) { return dom.doc.getElementById(id); },
    region() { return dom.rEntries; }, wsT(k) { return k; },
    window: { fetch: fetchFn, localStorage: { getItem() { return null; }, setItem() {} },
      WsAudit: { syncSelection() {}, fmtBytes(n) { return String(n); } } }
  };
  vm.createContext(ctx);
  vm.runInContext(rowsSrc.replace('    auInitialSync();', ''), ctx);
  return { ctx, dom };
}
const auditPage = (page) => ({ ok: true, status: 200, async json() { return page; } });

test('gap reset invalidates pending interval and refetches displayed coverage', async () => {
  let finish;
  const urls = [];
  const { ctx, dom } = raceRowsHarness((url) => {
    urls.push(url);
    if (urls.length === 1) return new Promise((resolve) => { finish = resolve; });
    return Promise.resolve(auditPage({ events: [], after: 'fresh', end: true }));
  });
  ctx.auLatestAfter = 'stale'; ctx.auCoveredSince = 'old';
  ctx.auOpenBelowDone = ctx.auUnboundedDone = ctx.auReset410Done = true;
  const pending = ctx.auFetchInterval('old', '', true);
  dom.doc.dispatchEvent({ type: 'ws-history-reset' });
  assert.equal(ctx.auLatestAfter, '');
  assert.equal(ctx.auCoveredSince, '');
  assert.equal(ctx.auOpenBelowDone, false);
  assert.equal(ctx.auUnboundedDone, false);
  assert.equal(ctx.auReset410Done, false);
  finish(auditPage({ events: [{ id: 'stale', kind: 'thinking' }], after: 'stale', end: true }));
  assert.equal((await pending).ok, false);
  assert.equal(ctx.auEventsById.stale, undefined);
  dom.doc.dispatchEvent({ type: 'ws-entries-merged', detail: { oldestAt: '2026-11-20T10:00:02Z' } });
  await ctx.auFetchQueue;
  assert.match(urls[1], /since=/);
  assert.doesNotMatch(urls[1], /after=/);
  assert.equal(ctx.auLatestAfter, 'fresh');
});

test('page two 503 leaves interval uncommitted and next merge retries full interval', async () => {
  const urls = [];
  let fail = true;
  const { ctx, dom } = raceRowsHarness(async (url) => {
    urls.push(url);
    if (url.includes('before=older')) {
      if (fail) return { ok: false, status: 503 };
      return auditPage({ events: [], end: true });
    }
    if (url.includes('after=')) return auditPage({ events: [], after: 'latest' });
    return auditPage({ events: [], before: 'older', after: 'latest', end: false });
  });
  const merge = () => dom.doc.dispatchEvent({ type: 'ws-entries-merged', detail: { oldestAt: '2026-11-20T10:00:02Z' } });
  merge(); await ctx.auFetchQueue;
  assert.equal(ctx.auLatestAfter, '');
  assert.equal(ctx.auCoveredSince, '');
  fail = false;
  merge(); await ctx.auFetchQueue;
  assert.match(urls[2], /since=/);
  assert.doesNotMatch(urls[2], /after=|before=/);
  assert.equal(ctx.auLatestAfter, 'latest');
  assert.equal(ctx.auCoveredSince, '2026-11-20T10:00:02Z');
});

test('live refresh drains reconnect backlog beyond 200 events', async () => {
  const cursors = [];
  const { ctx } = raceRowsHarness(async (url) => {
    const cursor = new URL(url, 'http://fixture').searchParams.get('after');
    cursors.push(cursor);
    const n = Number(cursor);
    return auditPage({ events: n < 2 ? Array.from({ length: 200 }, (_, i) => ({ id: n + '-' + i, kind: 'tool_call' })) : [],
      after: String(n < 2 ? n + 1 : n) });
  });
  ctx.auLatestAfter = '0';
  await ctx.auRefreshLatestAfter();
  assert.deepEqual(cursors, ['0', '1', '2']);
  assert.equal(ctx.auLatestAfter, '2');
});
