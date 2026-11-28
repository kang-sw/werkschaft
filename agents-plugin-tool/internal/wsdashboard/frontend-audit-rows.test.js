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
      this.offsetHeight = 0;
      // The bar-height sync writes a custom property onto the conversation el.
      this.style = { props: Object.create(null), setProperty(k, v) { this.props[k] = String(v); } };
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
    removeAttribute(k) {
      delete this.attrs[k];
    }
    toggleAttribute(k, force) {
      const on = force === undefined ? !this.hasAttribute(k) : !!force;
      if (on) this.attrs[k] = '';
      else delete this.attrs[k];
      return on;
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

test('auInterleavePlan attaches matching call_ids, falls back orphans chronologically, and anchors sysStart', () => {
  const h = loadAuditRowsHelpers();
  const entries = [
    { el: 'E1', key: 'k1', at: '2026-11-20T10:00:01Z' },
    { el: 'E2', key: 'k2', at: '2026-11-20T10:00:05Z' }
  ];
  const renderedCallIds = { c1: true };
  const events = [
    { id: 'e-attached', seq: 5, kind: 'hook', at: '2026-11-20T10:00:02Z', call_id: 'c1' },
    { id: 'e-orphan', seq: 6, kind: 'other', at: '2026-11-20T10:00:03Z', call_id: 'c-missing' },
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
  const call = new dom.El('DETAILS');
  call.className = 'au-call-details';
  call.appendChild(r1);
  call.appendChild(itemsSlot(dom));
  calls.appendChild(call);
  tools.appendChild(calls);
  dom.centries.appendChild(e1);
  dom.centries.appendChild(tools);
  const turn = addTurn(dom, 'k4', '2026-11-20T10:00:03.500Z');

  const fetched = [];
  const opened = [];
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
      getSelection() { return { isCollapsed: true }; },
      fetch: fetchFn
    }
  };
  ctx.window.WsAudit = {
    fmtBytes: (n) => `${n} B`,
    openCall(id) { opened.push(['call', id]); },
    openEvent(id, opts) { opened.push(['event', id, opts && opts.tab]); },
    visibleRows() { return dom.centries.querySelectorAll('.au-row[data-call], .au-hid[data-event-id], .au-sysrow .au-hid[data-event-id]'); },
    syncSelection() {}
  };
  vm.createContext(ctx);
  vm.runInContext(rowsSrc, ctx);

  // Allow initial queued sync to complete
  await new Promise((r) => setTimeout(r, 20));
  assert.ok(fetched[0].includes('since=2026-11-20T10%3A00%3A02Z') && !fetched[0].includes('until='));
  assert.ok(dom.centries.querySelector('.au-call-details .conv-audit-items .au-hid[data-event-id="ev-hook"]'));

  // Older history page arrives with end: true -> fetches [T1, T2) AND open-below [, T1)
  const e0 = new dom.El('DIV');
  e0.setAttribute('data-entry', 'k1');
  e0.setAttribute('data-at', '2026-11-20T10:00:01Z');
  dom.centries.insertBefore(e0, e1);
  dom.doc.dispatchEvent({ type: 'ws-history-page', detail: { prevOldestAt: '2026-11-20T10:00:02Z', oldestAt: '2026-11-20T10:00:01Z', end: true, empty: false } });
  await new Promise((r) => setTimeout(r, 20));
  assert.ok(fetched.some((u) => u.includes('since=2026-11-20T10%3A00%3A01Z') && u.includes('until=2026-11-20T10%3A00%3A02Z')));
  assert.ok(fetched.some((u) => !u.includes('since=') && u.includes('until=2026-11-20T10%3A00%3A01Z')));
  assert.ok(dom.centries.querySelector('.au-sysrow .au-hid[data-event-id="ev-sys"]'));
  assert.ok(turn.querySelector('.au-hid[data-event-id="ev-think"]'));

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

  // No filter bar is drawn: every kind of hidden-event row stays in the stream.
  assert.equal(dom.convo.querySelector('.au-bar'), null);
  assert.equal(dom.centries.querySelector('.au-hid[data-event-id="ev-think"]').classList.contains('au-kind-hidden'), false);

  // Scoped J/K/Enter: ignores outside containers (.au-win, #composer)
  const composer = new dom.El('DIV');
  composer.id = 'composer';
  const composerBtn = new dom.El('BUTTON');
  composer.appendChild(composerBtn);
  dom.doc.dispatchEvent({ type: 'keydown', key: 'j', target: composerBtn, preventDefault() { throw new Error('must not fire in #composer'); } });

  dom.doc.dispatchEvent({ type: 'keydown', key: 'j', target: dom.convo, preventDefault() {} });
  const initEl = dom.centries.querySelector('.au-sysrow .au-hid');
  assert.equal(initEl.focused, true);
  dom.doc.dispatchEvent({ type: 'keydown', key: 'j', target: initEl, preventDefault() {} });
  assert.equal(r1.focused, true);
  dom.doc.dispatchEvent({ type: 'keydown', key: 'j', target: r1, preventDefault() {} });
  const hookEl = dom.centries.querySelector('.au-hid[data-event-id="ev-hook"]');
  assert.equal(hookEl.focused, true);
  dom.doc.dispatchEvent({ type: 'keydown', key: 'j', target: hookEl, preventDefault() {} });
  const thinkEl = turn.querySelector('.au-hid[data-event-id="ev-think"]');
  assert.equal(thinkEl.focused, true);
  dom.doc.dispatchEvent({ type: 'keydown', key: 'j', target: thinkEl, preventDefault() {} });
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


test('history end continuation cannot start open-below work after a gap reset', async () => {
  const t1 = '2026-11-20T10:00:01Z', t2 = '2026-11-20T10:00:02Z';
  let finish;
  const urls = [];
  const { ctx, dom } = raceRowsHarness((url) => {
    urls.push(url);
    return new Promise((resolve) => { finish = resolve; });
  });
  ctx.auCoveredSince = t2;
  dom.doc.dispatchEvent({ type: 'ws-history-page', detail: { oldestAt: t1, end: true } });
  const savedQueue = ctx.auFetchQueue;
  while (!finish) await new Promise((r) => setTimeout(r, 0));
  assert.equal(new URL(urls[0], 'http://fixture').searchParams.get('until'), t2);
  dom.doc.dispatchEvent({ type: 'ws-history-reset' });
  finish(auditPage({ events: [{ id: 'pre-reset', kind: 'thinking', at: t1 }], end: true }));
  await savedQueue;
  assert.equal(urls.length, 1, 'no open-below until=T1 request escapes the old epoch');
  assert.equal(ctx.auEventsById['pre-reset'], undefined);
  assert.equal(ctx.auOpenBelowDone, false);
  assert.equal(ctx.auCoveredSince, '');
});

test('history end still opens below an already covered interval', async () => {
  const t1 = '2026-11-20T10:00:01Z';
  const urls = [];
  const { ctx, dom } = raceRowsHarness(async (url) => {
    urls.push(url);
    return auditPage({ events: [], end: true });
  });
  ctx.auCoveredSince = t1;
  dom.doc.dispatchEvent({ type: 'ws-history-page', detail: { oldestAt: t1, end: true } });
  await ctx.auFetchQueue;
  assert.equal(urls.length, 1);
  assert.equal(new URL(urls[0], 'http://fixture').searchParams.get('until'), t1);
  assert.equal(ctx.auOpenBelowDone, true);
});

function itemsSlot(dom) {
  const body = new dom.El('DIV');
  body.className = 'conv-audit-items';
  return body;
}

function addTurn(dom, key, at) {
  const turn = new dom.El('DETAILS');
  turn.className = 'turnline';
  turn.setAttribute('data-entry', key);
  turn.setAttribute('data-at', at);
  const counts = new dom.El('SPAN');
  counts.className = 'turn-audit-counts';
  turn.appendChild(counts);
  turn.appendChild(itemsSlot(dom));
  dom.centries.appendChild(turn);
  return turn;
}

function turnFixture(finished = true, fetch = async () => auditPage({ events: [], end: true })) {
  const { ctx, dom } = raceRowsHarness(fetch);
  const at = (seconds) => '2026-11-20T10:00:' + String(seconds).padStart(2, '0') + 'Z';
  const tools = new dom.El('DIV');
  tools.className = 'au-tools';
  tools.setAttribute('data-entry', 'tools');
  tools.setAttribute('data-at', at(10));
  const call = new dom.El('DETAILS');
  call.className = 'au-call-details';
  const summary = new dom.El('SUMMARY');
  summary.className = 'au-row';
  summary.setAttribute('data-call', 'c1');
  summary.textContent = 'Read';
  call.appendChild(summary);
  call.appendChild(itemsSlot(dom));
  tools.appendChild(call);
  dom.centries.appendChild(tools);
  const turn = finished ? addTurn(dom, 'turn', at(20)) : null;
  function owner(kind, seconds, callId) {
    const el = new dom.El('DETAILS');
    el.setAttribute('data-audit-owner', kind);
    if (callId) el.setAttribute('data-approval-call', callId);
    el.setAttribute('data-at', at(seconds));
    el.appendChild(itemsSlot(dom));
    dom.convo.appendChild(el);
    return el;
  }
  const events = {};
  ctx.auEventsById = events;
  function add(id, seq, kind, seconds, extra) {
    events[id] = { id, seq, kind, at: at(seconds), ...extra };
  }
  return { ctx, dom, at, call, turn, owner, add };
}

function countDomOps(dom, fn) {
  const proto = dom.El.prototype;
  const names = ['appendChild', 'insertBefore', 'removeChild', 'setAttribute', 'toggleAttribute'];
  const saved = {};
  let ops = 0;
  for (const n of names) {
    saved[n] = proto[n];
    proto[n] = function (...a) { ops++; return saved[n].apply(this, a); };
  }
  const tc = Object.getOwnPropertyDescriptor(proto, 'textContent');
  Object.defineProperty(proto, 'textContent', { configurable: true, get: tc.get, set(v) { ops++; tc.set.call(this, v); } });
  try { fn(); } finally {
    for (const n of names) proto[n] = saved[n];
    Object.defineProperty(proto, 'textContent', tc);
  }
  return ops;
}

function rowById(dom, id) {
  return dom.convo.querySelector('.au-hid[data-event-id="' + id + '"]');
}

test('audit counterparts stay in their owning disclosures and the finished turn orders the remaining events', () => {
  const f = turnFixture();
  const compact = f.owner('compaction', 14);
  const approval = f.owner('approval', 5, 'c1');
  const completion = f.owner('subagent', 17);
  f.add('prompt', 1, 'system', 1, { subkind: 'init', out_bytes: 20 });
  f.add('thinking', 2, 'thinking', 2);
  f.add('hook', 3, 'hook', 3, { parent_id: 'c1' });
  f.add('request', 4, 'system', 5, { subkind: 'control_request:can_use_tool', call_id: 'c1' });
  f.add('start', 5, 'tool_call', 6, { call_id: 'c1' });
  f.add('task-start', 6, 'subagent', 7, { subkind: 'task_started', agent_id: 'a1' });
  f.add('progress', 7, 'system', 8, { subkind: 'tool_progress', call_id: 'c1' });
  f.add('orphan-progress', 8, 'other', 9, { subkind: 'tool_progress' });
  f.add('reminder', 9, 'system_reminder', 12);
  f.add('compact', 10, 'compaction', 14);
  f.add('task-end', 11, 'subagent', 17, { subkind: 'task_notification', agent_id: 'a1' });
  f.ctx.auReconcileRows();

  const ids = (root) => root.querySelector('.conv-audit-items').children.map((row) => row.getAttribute('data-event-id'));
  assert.deepEqual(ids(f.dom.convo.querySelector('.au-sysrow')), ['prompt']);
  assert.deepEqual(ids(f.call), ['hook', 'progress']);
  assert.deepEqual(ids(compact), ['compact']);
  assert.deepEqual(ids(approval), ['request']);
  assert.deepEqual(ids(completion), ['task-start', 'task-end']);
  assert.deepEqual(ids(f.turn), ['thinking', null, 'reminder']);
  assert.equal(f.turn.querySelector('.au-turn-tool').textContent, 'Read');
  assert.equal(f.turn.querySelector('.turn-audit-counts').textContent,
    'js.audit.kind.system 2 · js.audit.kind.hook 1 · js.audit.kind.reminder 1 · js.audit.kind.thinking 1 · js.audit.kind.subagent 2 · js.audit.kind.compaction 1');
  assert.equal(rowById(f.dom, 'orphan-progress'), null);
  assert.equal(f.dom.centries.querySelectorAll('.au-run').length, 0);
});

test('Antigravity approval audit requests pair with the live owner by call ID', () => {
  const f = turnFixture(false);
  const sameTime = f.owner('approval', 5, 'another-call');
  const approval = f.owner('approval', 9, 'c1');
  f.add('agy-request', 1, 'system', 5, {
    subkind: 'request:session/request_permission', call_id: 'c1'
  });
  f.ctx.auReconcileRows();

  assert.equal(sameTime.querySelector('.conv-audit-items').children.length, 0);
  assert.equal(rowById(f.dom, 'agy-request')?.parentNode, approval.querySelector('.conv-audit-items'));
});

for (const failedPart of ['bundle', 'body']) {
  test('reopening the inline audit preview retries a failed HTTP ' + failedPart, async () => {
    const bundle = '/s/s1/audit/events?call=c1';
    const body = '/s/s1/audit/event/in1?part=in&len=4096';
    const calls = [];
    let finishFailure;
    const f = turnFixture(false, async url => {
      calls.push(url);
      if (!finishFailure && url === (failedPart === 'bundle' ? bundle : body)) {
        return new Promise(resolve => { finishFailure = resolve; });
      }
      if (url === bundle) return auditPage({ call: { id: 'in1', in_bytes: 5 }, events: [] });
      assert.equal(url, body);
      return auditPage({ data: 'input', end: true });
    });
    function toggle(open) {
      f.call.toggleAttribute('open', open);
      f.dom.doc.dispatchEvent({ type: 'toggle', target: f.call });
    }
    const settle = () => new Promise(setImmediate);

    toggle(true);
    await settle();
    assert.equal(typeof finishFailure, 'function');
    const beforeRetry = calls.slice();
    toggle(false);
    toggle(true);
    assert.deepEqual(calls, beforeRetry);
    finishFailure({ ok: false, status: 503 });
    await settle();
    assert.equal(f.call.querySelector('.au-call-input'), null);

    toggle(false);
    toggle(true);
    await settle();
    assert.deepEqual(calls, [...beforeRetry, bundle, body]);
    assert.equal(f.call.querySelector('.au-call-input')?.textContent, 'input');

    toggle(false);
    toggle(true);
    await settle();
    assert.deepEqual(calls, [...beforeRetry, bundle, body]);
  });
}

test('retry detail leaves the live badge and counts under the finished turn on recovery', () => {
  const f = turnFixture(false);
  const retry = f.owner('retry', 13);
  f.add('retry-old', 1, 'system', 12, { subkind: 'api_retry' });
  f.add('retry-live', 2, 'system', 13, { subkind: 'api_retry' });
  f.ctx.auReconcileRows();
  const live = rowById(f.dom, 'retry-live');
  assert.equal(live.parentNode, retry.querySelector('.conv-audit-items'));
  assert.equal(rowById(f.dom, 'retry-old'), null);
  f.dom.convo.removeChild(retry);
  const turn = addTurn(f.dom, 'turn', f.at(20));
  f.ctx.auReconcileRows();
  assert.equal(f.dom.convo.querySelector('[data-audit-owner="retry"]'), null);
  assert.equal(turn.querySelectorAll('[data-kind="system"]').length, 2);
  assert.equal(turn.querySelector('.turn-audit-counts').textContent, 'js.audit.kind.system 2');
});

test('new audit events update the keyed turn disclosure without structural churn', () => {
  const f = turnFixture();
  f.add('s1', 1, 'system', 12);
  f.add('t1', 2, 'thinking', 13);
  f.ctx.auReconcileRows();
  const sum = f.turn.querySelector('.turn-audit-counts');
  const retained = rowById(f.dom, 't1');
  assert.equal(countDomOps(f.dom, () => f.ctx.auReconcileRows()), 0);
  f.turn.setAttribute('open', '');
  f.add('s2', 3, 'system', 14);
  const ops = countDomOps(f.dom, () => f.ctx.auReconcileRows());
  assert.ok(ops <= 22, 'one event and one label cost ' + ops + ' DOM writes');
  assert.equal(rowById(f.dom, 't1'), retained);
  assert.equal(sum.textContent, 'js.audit.kind.system 2 · js.audit.kind.thinking 1');
  assert.equal(f.turn.hasAttribute('open'), true);
  assert.equal(countDomOps(f.dom, () => f.ctx.auReconcileRows()), 0);
});

// A fixture audit store behind the real /audit/events selection: the filter
// (kinds, since <= at < until) first, then the cursor and n, newest page when
// no cursor, oldest-first events, before/after naming the page's ends.
function simAuditFetch(store, log) {
  return async (url) => {
    log.push(url);
    const q = new URL(url, 'http://fixture').searchParams;
    const n = Number(q.get('n') || 100);
    const kinds = q.get('kinds') ? q.get('kinds').split(',') : null;
    const since = q.get('since') ? Date.parse(q.get('since')) : null;
    const until = q.get('until') ? Date.parse(q.get('until')) : null;
    const sel = [];
    store.forEach((ev, i) => {
      const at = Date.parse(ev.at);
      if (kinds && !kinds.includes(ev.kind)) return;
      if (since !== null && at < since) return;
      if (until !== null && at >= until) return;
      sel.push(i);
    });
    const posOf = (id) => store.findIndex((e) => e.id === id);
    let start, stop;
    if (q.get('before')) { stop = sel.filter((i) => i < posOf(q.get('before'))).length; start = Math.max(stop - n, 0); }
    else if (q.get('after')) { start = sel.filter((i) => i <= posOf(q.get('after'))).length; stop = Math.min(start + n, sel.length); }
    else { stop = sel.length; start = Math.max(stop - n, 0); }
    const idx = sel.slice(start, stop);
    return auditPage({ events: idx.map((i) => store[i]), before: idx.length ? store[idx[0]].id : '',
      after: idx.length ? store[idx[idx.length - 1]].id : '', end: start === 0 });
  };
}

const boundBase = Date.parse('2026-11-20T00:00:00Z');
const boundAt = (sec) => new Date(boundBase + sec * 1000).toISOString().replace('.000Z', 'Z');
function boundStore(oldCount, spanCount) {
  const store = [];
  const kinds = ['system', 'thinking', 'hook', 'subagent'];
  for (let i = 0; i < oldCount; i++) store.push({ id: 'old-' + i, seq: i, kind: kinds[i % 4], at: boundAt(i) });
  for (let i = 0; i < spanCount; i++) store.push({ id: 'span-' + i, seq: oldCount + i, kind: kinds[i % 4], at: boundAt(100000 + i) });
  return store;
}
function boundEntries(dom, firstAt) {
  const t0 = Date.parse(firstAt);
  for (let i = 0; i < 3; i++) {
    const el = new dom.El('DIV');
    el.setAttribute('data-entry', 'k' + i);
    el.setAttribute('data-at', new Date(t0 + i * 60000).toISOString().replace('.000Z', 'Z'));
    dom.centries.appendChild(el);
  }
}
const urlParams = (u) => new URL(u, 'http://fixture').searchParams;

test('history end with a short covered span fetches the span and one page below, not the whole older store', async () => {
  const store = boundStore(5000, 30);
  const urls = [];
  const { ctx, dom } = raceRowsHarness(simAuditFetch(store, urls));
  const first = boundAt(100000);
  boundEntries(dom, first);
  dom.doc.dispatchEvent({ type: 'ws-history-page', detail: { oldestAt: first, end: true } });
  await ctx.auFetchQueue;
  const stored = Object.keys(ctx.auEventsById).length;
  assert.ok(urls.length <= 3, 'requests: ' + urls.length);
  assert.ok(stored <= 30 + 200, 'stored events: ' + stored);
  assert.equal(ctx.auOpenBelowDone, true);
  assert.equal(ctx.auCoveredSince, first);
});

test('an oversized covered span stops at the page cap and records the floor it reached', async () => {
  const store = boundStore(0, 6000);
  const urls = [];
  const { ctx, dom } = raceRowsHarness(simAuditFetch(store, urls));
  const first = boundAt(100000);
  boundEntries(dom, first);
  dom.doc.dispatchEvent({ type: 'ws-entries-merged', detail: { oldestAt: first } });
  await ctx.auFetchQueue;
  assert.ok(urls.length <= 10, 'requests: ' + urls.length);
  assert.ok(Object.keys(ctx.auEventsById).length <= 2000);
  const floor = ctx.auCoveredSince;
  assert.ok(floor && ctx.auCmpStrNs(floor, first) > 0, 'covered floor moved up to ' + floor);
  const lowest = Object.values(ctx.auEventsById).map((e) => e.at).sort()[0];
  assert.equal(floor, lowest, 'floor is the oldest fetched event');
});

test('entries without a date fetch one newest page, never the whole store', async () => {
  const store = boundStore(5000, 0);
  const urls = [];
  const { ctx, dom } = raceRowsHarness(simAuditFetch(store, urls));
  dom.doc.dispatchEvent({ type: 'ws-history-page', detail: { oldestAt: '', end: true, empty: true } });
  await ctx.auFetchQueue;
  assert.equal(urls.length, 1);
  assert.equal(Object.keys(ctx.auEventsById).length, 200);
  dom.doc.dispatchEvent({ type: 'ws-entries-merged', detail: { oldestAt: '', empty: true } });
  await ctx.auFetchQueue;
  assert.ok(urls.length <= 2, 'a second look reuses the latest cursor: ' + urls.length);
});

test('the earliest retained init owns the only session-start badge', () => {
  const f = turnFixture();
  f.add('init1', 1, 'system', 5, { subkind: 'init' });
  f.add('init2', 2, 'system', 12, { subkind: 'init' });
  f.ctx.auReconcileRows();
  assert.equal(f.dom.centries.querySelectorAll('.au-sysrow').length, 1);
  assert.equal(f.dom.centries.firstElementChild, f.dom.centries.querySelector('.au-sysrow'));
  assert.equal(f.dom.centries.querySelector('.au-sysrow .au-hid').getAttribute('data-event-id'), 'init1');
  assert.ok(f.turn.querySelector('[data-event-id="init2"]'));

  f.add('init0', 0, 'system', 1, { subkind: 'init' });
  f.ctx.auReconcileRows();
  assert.equal(f.dom.centries.querySelectorAll('.au-sysrow').length, 1);
  assert.equal(f.dom.centries.querySelector('.au-sysrow .au-hid').getAttribute('data-event-id'), 'init0');
  assert.ok(f.turn.querySelector('[data-event-id="init1"]'));
});

test('rows added under a bottom-pinned conversation re-pin it; no-op reconciles and unpinned views do not move it', () => {
  for (const stick of [true, false]) {
    const f = turnFixture();
    let pins = 0;
    f.ctx.stick = stick;
    f.ctx.pinConvo = () => { pins++; };
    f.add('t1', 1, 'thinking', 5);
    f.ctx.auReconcileRows();
    assert.equal(pins, stick ? 1 : 0, 'rows arriving re-pin only a pinned view');
    f.ctx.auReconcileRows();
    assert.equal(pins, stick ? 1 : 0, 'an unchanged reconcile does not touch the scroll');
    f.add('t2', 2, 'hook', 6);
    f.ctx.auReconcileRows();
    assert.equal(pins, stick ? 2 : 0);
  }
});

test('launch_* subagent events pair under subagent owner and wait for turn while running', () => {
  const h = loadAuditRowsHelpers();
  assert.equal(h.auEventOwnerKind({ kind: 'subagent', subkind: 'launch_started' }), 'subagent');
  assert.equal(h.auEventOwnerKind({ kind: 'subagent', subkind: 'launch_completed' }), 'subagent');
  assert.equal(h.auEventOwnerKind({ kind: 'subagent', subkind: 'launch_failed' }), 'subagent');
  assert.equal(h.auWaitForTurn({ kind: 'subagent', subkind: 'launch_started' }), true);
  assert.equal(h.auWaitForTurn({ kind: 'subagent', subkind: 'launch_completed' }), true);

  const f = turnFixture();
  const doneOwner = f.owner('subagent', 15);
  const failOwner = f.owner('subagent', 18);
  f.add('l1-start', 1, 'subagent', 12, { subkind: 'launch_started', agent_id: 'agy:c1', parent_id: 'c1' });
  f.add('l1-end', 2, 'subagent', 15, { subkind: 'launch_completed', agent_id: 'agy:c1', parent_id: 'c1' });
  f.add('l2-start', 3, 'subagent', 16, { subkind: 'launch_started', agent_id: 'agy:c2', parent_id: 'c2' });
  f.add('l2-fail', 4, 'subagent', 18, { subkind: 'launch_failed', agent_id: 'agy:c2', parent_id: 'c2' });
  f.ctx.auReconcileRows();

  const ids = (root) => root.querySelector('.conv-audit-items').children.map((row) => row.getAttribute('data-event-id'));
  assert.deepEqual(ids(doneOwner), ['l1-start', 'l1-end']);
  assert.deepEqual(ids(failOwner), ['l2-start', 'l2-fail']);

  // While running (no finished turn, no completion owner yet), launch_started waits for the upcoming turn.
  const running = turnFixture(false);
  running.add('l3-start', 1, 'subagent', 12, { subkind: 'launch_started', agent_id: 'agy:c3' });
  running.ctx.auReconcileRows();
  assert.equal(rowById(running.dom, 'l3-start'), null);
});

test('estimated compaction renders badge on row and compaction owner summary with zero churn on no-op reconcile', () => {
  const f = turnFixture();
  const compact = f.owner('compaction', 14);
  const sum = new f.dom.El('SUMMARY');
  sum.appendChild(f.dom.doc.createTextNode('context compacted (auto)'));
  const timeEl = new f.dom.El('TIME');
  sum.appendChild(timeEl);
  compact.insertBefore(sum, compact.firstChild);

  f.add('cmp-est', 1, 'compaction', 14, { subkind: 'auto', estimated: true });
  f.ctx.auReconcileRows();

  const row = rowById(f.dom, 'cmp-est');
  assert.ok(row && row.classList.contains('au-estimated'));
  const rowBadge = row.querySelector('.au-est-badge');
  assert.ok(rowBadge && rowBadge.textContent === 'js.audit.compaction.estimated');
  const sumBadge = sum.querySelector('.au-est-badge');
  assert.ok(sumBadge && sumBadge.textContent === 'js.audit.compaction.estimated');
  assert.equal(sumBadge.nextElementSibling, timeEl, 'owner summary badge sits before timestamp');

  // Reconciling again with no changes performs zero DOM operations.
  assert.equal(countDomOps(f.dom, () => f.ctx.auReconcileRows()), 0);
});

test('turn reference fallback uses check/x icons instead of hammer for tool rows', () => {
  assert.ok(!rowsSrc.includes('"hammer"'), 'dashboard-audit-rows.js must not fall back to hammer icon');
  assert.ok(rowsSrc.includes('el.classList.contains("au-fail") ? "x" : "check"'),
    'tool fallback icon must pick #i-x for failed rows and #i-check otherwise');
});
