const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

const winSrc = fs.readFileSync(__dirname + '/assets/dashboard-audit-window.js', 'utf8');
const ctrlSrc = fs.readFileSync(__dirname + '/assets/dashboard-audit.js', 'utf8');

function loadAuditWindowHelpers() {
  const ctx = {
    Math,
    Number,
    String,
    Array,
    Object,
    JSON,
    Map,
    Set,
    isFinite,
    wsT(key, a, b) {
      let s = key;
      if (a !== undefined) s += ':' + a;
      if (b !== undefined) s += ':' + b;
      return s;
    }
  };
  vm.createContext(ctx);
  // Extract pure helper functions from dashboard-audit-window.js before auBuildWindowDOM.
  const endIdx = winSrc.indexOf('  function auBuildWindowDOM(');
  vm.runInContext(winSrc.slice(0, endIdx), ctx);
  return ctx;
}

test('auFmtBytes and auFmtSpan format ranges cleanly', () => {
  const h = loadAuditWindowHelpers();
  assert.equal(h.auFmtBytes(0), '0 B');
  assert.equal(h.auFmtBytes(512), '512 B');
  assert.equal(h.auFmtBytes(2048), '2.0 KB');
  assert.equal(h.auFmtBytes(2 * 1024 * 1024), '2.0 MB');
  assert.equal(h.auFmtSpan(0), '');
  assert.equal(h.auFmtSpan(45), '<0.1s');
  assert.equal(h.auFmtSpan(1250), '1.3s');
  assert.equal(h.auFmtSpan(15000), '15s');
  assert.equal(h.auFmtSpan(75000), '1m 15s');
});

test('Decision (610): token growth deduplicates parallel request_id siblings and skips nulls', () => {
  const h = loadAuditWindowHelpers();
  const seen = Object.create(null);

  // Parallel calls share request_id: first gets +delta, second gets "·".
  const first = h.auTokenGrowthDisplay({ request_id: 'req-1', ctx_before: 10000, ctx_after: 12400 }, seen);
  assert.equal(first.text, '+2.4k');
  assert.equal(first.sibling, false);
  assert.equal(first.delta, 2400);

  const second = h.auTokenGrowthDisplay({ request_id: 'req-1', ctx_before: 10000, ctx_after: 12400 }, seen);
  assert.equal(second.text, '·');
  assert.equal(second.sibling, true);
  assert.equal(second.delta, null);

  // Missing ctx_before or ctx_after produces blank regardless of request_id.
  const nullBefore = h.auTokenGrowthDisplay({ request_id: 'req-2', ctx_before: null, ctx_after: 12400 }, seen);
  assert.equal(nullBefore.text, '');
  const nullAfter = h.auTokenGrowthDisplay({ request_id: 'req-3', ctx_before: 12400, ctx_after: null }, seen);
  assert.equal(nullAfter.text, '');
});

test('auRenderBodyContent handles redactions, stored_trunc, seam seen-cut, search hits, and loadmore', () => {
  const h = loadAuditWindowHelpers();
  // Head = "line1 ***\n" (10 bytes), Tail = "tail line here\n" (15 bytes), original 100 bytes.
  // Redaction at byte offset 6 in head ("***"), seen cut at exact seam (10 bytes).
  const ev = {
    id: 'ev1',
    model_seen: true,
    in: 'line1 ***\ntail line here\n',
    redactions: [{ field: 'in', offset: 6, rule: 'api-key', length: 32, count: 1 }],
    stored_trunc: { in: { original_bytes: 100, head_bytes: 10, tail_bytes: 15 } },
    seen: { in: 10 },
    truncated: true,
    range: { in: { offset: 25, total: 200 } }
  };
  const res = h.auRenderBodyContent(ev, 'in', { search: 'tail', activeMatch: 0, wrap: true, matches: { in: { spans: [{ line: 2, start: 0, end: 4 }] } } });
  assert.equal(res.matchCount, 1);
  assert.ok(res.html.includes('class="au-code au-wrap"'));
  assert.ok(res.html.includes('🔒 api-key · 32B'));
  assert.ok(res.html.includes('au-trunc-row'));
  assert.ok(res.html.includes('au-seencut-badge'));
  assert.ok(res.html.includes('au-unseen'));
  assert.ok(res.html.includes('class="au-hit au-hit-cur"'));
  assert.ok(res.html.includes('data-au-more="in"'));
});

test('auRenderRawTab differentiates mapped envelopes from other events', () => {
  const h = loadAuditWindowHelpers();
  const mapped = h.auRenderRawTab({ id: 'e1', kind: 'tool_call', harness: 'claude', in: 'foo', raw: 'ignored' }, {});
  assert.ok(mapped.html.includes('js.audit.raw.normalized.title'));
  assert.ok(!mapped.plainText.includes('ignored'));

  const other = h.auRenderRawTab({ id: 'e2', kind: 'other', harness: 'pi', raw: '{"raw":true}' }, {});
  assert.ok(other.html.includes('js.audit.raw.other.title'));
  assert.equal(other.plainText, '{"raw":true}');
});

test('controller module has valid syntax and exposes WsAudit surface', () => {
  const fn = new Function(winSrc + '\n' + ctrlSrc);
  assert.equal(typeof fn, 'function');
  assert.ok(ctrlSrc.includes('AU_PIN_MAX = 4'));
  assert.ok(ctrlSrc.includes('AU_PIN_STEP = 24'));
});

function loadAuditControllerHarness(fetchImpl) {
  function makeEl(tag) {
    const attrs = Object.create(null);
    const cls = new Set();
    const listeners = Object.create(null);
    const el = {
      tagName: String(tag || 'DIV').toUpperCase(),
      children: [],
      parentNode: null,
      style: {},
      hidden: false,
      tabIndex: 0,
      textContent: '',
      title: '',
      value: '',
      isConnected: true,
      classList: {
        add: (...names) => names.forEach(n => cls.add(n)),
        remove: (...names) => names.forEach(n => cls.delete(n)),
        toggle: (n, f) => { if (f === undefined) f = !cls.has(n); if (f) cls.add(n); else cls.delete(n); return f; },
        contains: (n) => cls.has(n)
      },
      set className(v) { cls.clear(); String(v || '').split(/\s+/).filter(Boolean).forEach(n => cls.add(n)); },
      get className() { return Array.from(cls).join(' '); },
      setAttribute(k, v) { attrs[k] = String(v); },
      getAttribute(k) { return k in attrs ? attrs[k] : null; },
      hasAttribute(k) { return k in attrs; },
      toggleAttribute(k, f) { if (f) attrs[k] = ''; else delete attrs[k]; return !!f; },
      appendChild(child) { child.parentNode = el; el.children.push(child); return child; },
      removeChild(child) { el.children = el.children.filter(c => c !== child); child.parentNode = null; return child; },
      addEventListener(type, fn) { (listeners[type] = listeners[type] || []).push(fn); },
      dispatchEvent(ev) {
        ev.target = ev.target || el;
        (listeners[ev.type] || []).slice().forEach(fn => fn(ev));
      },
      focus() { doc.activeElement = el; },
      select() {},
      contains(other) {
        let cur = other;
        while (cur) { if (cur === el) return true; cur = cur.parentNode; }
        return false;
      },
      matches(sel) {
        if (sel === '.au-win') return cls.has('au-win');
        if (sel === '.au-row[data-call]') return cls.has('au-row') && ('data-call' in attrs);
        if (sel === '.au-row[data-call]:not([data-au-hydrated])') return cls.has('au-row') && ('data-call' in attrs) && !('data-au-hydrated' in attrs);
        return false;
      },
      closest(sel) {
        let cur = el;
        while (cur) { if (cur.matches && cur.matches(sel)) return cur; cur = cur.parentNode; }
        return null;
      },
      querySelector(sel) { return el.querySelectorAll(sel)[0] || null; },
      querySelectorAll(sel) {
        const out = [];
        const visit = (node) => {
          for (const c of node.children || []) {
            if (sel === '.au-row[data-call]' && c.matches(sel)) out.push(c);
            else if (sel === '#convo .au-row[data-call]:not([data-au-hydrated])' && c.matches('.au-row[data-call]:not([data-au-hydrated])')) out.push(c);
            else if (sel === '.au-mk use') {
              if (c.classList && c.classList.contains('au-mk')) {
                for (const sub of c.querySelectorAll('use')) out.push(sub);
              }
            }
            else if (sel.startsWith('.') && c.classList && c.classList.contains(sel.slice(1).split(/[\s\[]/)[0])) out.push(c);
            else if (sel === 'use' && c.tagName === 'USE') out.push(c);
            visit(c);
          }
        };
        visit(el);
        return out;
      },
      getBoundingClientRect() { return { left: 40, top: 60, width: 600, height: 500, right: 640, bottom: 560 }; },
      set innerHTML(_) {}
    };
    return el;
  }

  const root = makeEl('HTML');
  root.setAttribute('data-rid', 's1');
  const body = makeEl('BODY');
  const convo = makeEl('DIV');
  convo.id = 'convo';
  body.appendChild(convo);
  root.appendChild(body);
  const docListeners = Object.create(null);
  const doc = {
    documentElement: root,
    body,
    activeElement: body,
    createElement: makeEl,
    querySelector: (s) => s === '#convo' ? convo : null,
    addEventListener(type, fn) { (docListeners[type] = docListeners[type] || []).push(fn); },
    dispatchEvent(ev) {
      for (const fn of (docListeners[ev.type] || []).slice()) fn(ev);
    }
  };
  const ctx = {
    Math, Number, String, Array, Object, JSON, Map, Set, Promise, Error, isFinite, URL,
    encodeURIComponent, setTimeout, clearTimeout,
    innerWidth: 1440,
    innerHeight: 900,
    CustomEvent: function (name) { this.type = name; },
    wsT: (k) => k,
    toast: () => {},
    fetch: fetchImpl,
    addEventListener() {},
    removeEventListener() {},
    $: (id) => id === 'convo' ? convo : null,
    q: (sel, base) => (base || doc.body).querySelectorAll(sel),
    root, doc, rid: 's1'
  };
  ctx.window = ctx;
  ctx.document = doc;
  vm.createContext(ctx);
  vm.runInContext(winSrc + '\n' + ctrlSrc, ctx);
  return { ctx, doc, convo, makeEl };
}

function makeCallRow(makeEl, cid) {
  const row = makeEl('LI');
  row.className = 'au-row au-done';
  row.setAttribute('data-call', cid);
  for (const c of ['au-mk', 'au-tool', 'au-args', 'au-att', 'au-dur', 'au-tok']) {
    const sp = makeEl('SPAN');
    sp.className = c;
    row.appendChild(sp);
  }
  return row;
}

test('hydration pages backward across before cursors and refreshes forward via after cursor', async () => {
  const urls = [];
  const pages = {
    '': {
      events: [{ id: 'e2', seq: 2, kind: 'tool_call', call_id: 'c2', duration_ms: 100, request_id: 'r2', ctx_before: 1000, ctx_after: 1500 }],
      before: 'e2', after: 'e2', end: false
    },
    'before=e2': {
      events: [{ id: 'e1', seq: 1, kind: 'tool_call', call_id: 'c1', duration_ms: 50, request_id: 'r1', ctx_before: 500, ctx_after: 1000 }],
      before: 'e1', after: 'e1', end: true
    },
    'after=e2': {
      events: [{ id: 'e3', seq: 3, kind: 'tool_call', call_id: 'c3', duration_ms: 25, request_id: 'r3', ctx_before: 1500, ctx_after: 2000 }],
      before: 'e3', after: 'e3', end: false
    }
  };
  const h = loadAuditControllerHarness(async (url) => {
    urls.push(url);
    assert.ok(url.includes('kinds=tool_call,tool_result&n=500'), 'requests tool_call,tool_result with n=500: ' + url);
    const u = new URL('http://localhost' + url);
    const key = u.searchParams.get('before') ? 'before=' + u.searchParams.get('before')
      : u.searchParams.get('after') ? 'after=' + u.searchParams.get('after') : '';
    return { ok: true, status: 200, async json() { return pages[key]; } };
  });

  const r1 = makeCallRow(h.makeEl, 'c1');
  const r2 = makeCallRow(h.makeEl, 'c2');
  const rMissing = makeCallRow(h.makeEl, 'c_missing');
  h.convo.appendChild(r1);
  h.convo.appendChild(r2);
  h.convo.appendChild(rMissing);

  await h.ctx.WsAudit.hydrate();
  assert.equal(urls.length, 2, 'walks initial page then before cursor page');
  assert.equal(r1.getAttribute('data-au-hydrated'), '1');
  assert.equal(r2.getAttribute('data-au-hydrated'), '1');
  assert.equal(rMissing.getAttribute('data-au-hydrated'), '1', 'marks remaining unmatched rows only after state.end=true');

  // Append a live call row and verify forward refresh uses after=e2 (newest, not e1)
  const r3 = makeCallRow(h.makeEl, 'c3');
  h.convo.appendChild(r3);
  await h.ctx.WsAudit.hydrate();
  assert.equal(urls.length, 3);
  assert.ok(urls[2].includes('&after=e2'), 'live refresh uses after=e2: ' + urls[2]);
  assert.equal(r3.getAttribute('data-au-hydrated'), '1');
});

test('scheduleHydration refreshes live rows when st.end is true and after empty initial session', async () => {
  // Case 1: initial fetch ends with end:true, then live row c2 arrives via scheduleHydration()
  const urls1 = [];
  const h1 = loadAuditControllerHarness(async (url) => {
    urls1.push(url);
    const u = new URL('http://localhost' + url);
    if (u.searchParams.get('after') === 'e1') {
      return {
        ok: true, status: 200,
        async json() {
          return {
            events: [{ id: 'e2', seq: 2, kind: 'tool_call', call_id: 'c2', duration_ms: 42 }],
            before: 'e2', after: 'e2', end: false
          };
        }
      };
    }
    return {
      ok: true, status: 200,
      async json() {
        return {
          events: [{ id: 'e1', seq: 1, kind: 'tool_call', call_id: 'c1', duration_ms: 15 }],
          before: 'e1', after: 'e1', end: true
        };
      }
    };
  });
  const c1 = makeCallRow(h1.makeEl, 'c1');
  h1.convo.appendChild(c1);
  h1.ctx.WsAudit.scheduleHydration();
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(urls1.length, 1);
  assert.equal(c1.getAttribute('data-au-hydrated'), '1');

  const c2 = makeCallRow(h1.makeEl, 'c2');
  h1.convo.appendChild(c2);
  h1.ctx.WsAudit.scheduleHydration();
  assert.equal(c2.getAttribute('data-au-hydrated'), null, 'scheduleHydration must not pre-mark unmatched live row before forward walk');
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(urls1.length, 2, 'forward after= fetch executes for newly appended live row');
  assert.ok(urls1[1].includes('&after=e1'), 'forward walk uses after=e1: ' + urls1[1]);
  assert.equal(c2.getAttribute('data-au-hydrated'), '1');
  assert.equal(c2.querySelector('.au-dur').textContent, '<0.1s');
  assert.equal(c2.querySelector('.au-dur').title, '42 ms');

  // Case 2: initial fetch returns events:[], end:true (empty session), then live row c1 arrives
  const urls2 = [];
  let callIdx = 0;
  const h2 = loadAuditControllerHarness(async (url) => {
    urls2.push(url);
    callIdx++;
    if (callIdx === 1) {
      return { ok: true, status: 200, async json() { return { events: [], before: '', after: '', end: true }; } };
    }
    return {
      ok: true, status: 200,
      async json() {
        return {
          events: [{ id: 'e10', seq: 1, kind: 'tool_call', call_id: 'c1', duration_ms: 88 }],
          before: 'e10', after: 'e10', end: true
        };
      }
    };
  });
  const seed = makeCallRow(h2.makeEl, 'c_seed');
  h2.convo.appendChild(seed);
  h2.ctx.WsAudit.scheduleHydration();
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(urls2.length, 1);
  assert.equal(seed.getAttribute('data-au-hydrated'), '1');

  const liveRow = makeCallRow(h2.makeEl, 'c1');
  h2.convo.appendChild(liveRow);
  h2.ctx.WsAudit.scheduleHydration();
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(urls2.length, 2, 'empty initial session fetches fresh latest page when live row arrives');
  assert.equal(liveRow.getAttribute('data-au-hydrated'), '1');
  assert.equal(liveRow.querySelector('.au-dur').textContent, '<0.1s');
  assert.equal(liveRow.querySelector('.au-dur').title, '88 ms');
});

test('hydration leaves rows unhydrated on 503 so retry succeeds, and resets once on 410 stale cursor', async () => {
  let attempt = 0;
  const urls = [];
  const h = loadAuditControllerHarness(async (url) => {
    urls.push(url);
    attempt++;
    if (attempt === 1) {
      return { ok: false, status: 503, async text() { return 'unavailable'; } };
    }
    if (attempt === 2) {
      return {
        ok: true, status: 200,
        async json() { return { events: [], before: 'stale', after: 'stale', end: false }; }
      };
    }
    if (attempt === 3 && url.includes('before=stale')) {
      return { ok: false, status: 410, async text() { return 'stale_cursor'; } };
    }
    return {
      ok: true, status: 200,
      async json() {
        return {
          events: [{ id: 'e10', seq: 10, kind: 'tool_call', call_id: 'c10', duration_ms: 80 }],
          before: 'e10', after: 'e10', end: true
        };
      }
    };
  });

  const r10 = makeCallRow(h.makeEl, 'c10');
  h.convo.appendChild(r10);

  // First attempt hits 503 -> must NOT mark hydrated
  await h.ctx.WsAudit.hydrate();
  assert.equal(r10.getAttribute('data-au-hydrated'), null, '503 leaves row unhydrated for retry');

  // Retry hits empty page 1 + 410 stale cursor -> resets cursors cleanly and fetches page fresh
  await h.ctx.WsAudit.hydrate();
  assert.equal(r10.getAttribute('data-au-hydrated'), '1', 'retry succeeds after 503 and single 410 reset');
});

test('Decision (605): Escape and ArrowLeft/ArrowRight trigger only when focus target is inside .au-win', () => {
  const h = loadAuditControllerHarness(async () => ({ ok: true, status: 200, async json() { return { call: null, events: [] }; } }));
  const r1 = makeCallRow(h.makeEl, 'c1');
  h.convo.appendChild(r1);
  h.ctx.WsAudit.openCall('c1', r1);
  assert.equal(h.ctx.WsAudit.windows().length, 1);

  const outsideTarget = h.makeEl('TEXTAREA');
  let prevented = false;
  h.doc.dispatchEvent({ type: 'keydown', key: 'Escape', target: outsideTarget, preventDefault() { prevented = true; }, stopPropagation() {} });
  h.doc.dispatchEvent({ type: 'keydown', key: 'ArrowRight', target: outsideTarget, preventDefault() { prevented = true; } });
  assert.equal(prevented, false, 'Escape/Arrows outside .au-win do not preventDefault');
  assert.equal(h.ctx.WsAudit.windows().length, 1, 'Escape outside .au-win keeps audit window open');

  const winEl = h.ctx.WsAudit.windows()[0].el;
  let stopped = false;
  h.doc.dispatchEvent({ type: 'keydown', key: 'Escape', target: winEl, preventDefault() { prevented = true; }, stopImmediatePropagation() { stopped = true; } });
  assert.equal(prevented, true);
  assert.equal(stopped, true);
  assert.equal(h.ctx.WsAudit.windows().length, 0, 'Escape inside .au-win closes the audit window');
});

test('openEvent auto-selects out for non-tool_call out-only envelopes unless opts.tab is set', async () => {
  const envs = {
    'ev-rem': { id: 'ev-rem', kind: 'system_reminder', subkind: 'policy', out: 'Check captures.' },
    'ev-sub': { id: 'ev-sub', kind: 'subagent', subkind: 'spawn', in: '{"role":"verifier"}' },
    'ev-other': { id: 'ev-other', kind: 'other', subkind: 'custom', raw: '{}' },
    'ev-call': { id: 'ev-call', kind: 'tool_call', subkind: 'Read', call_id: 'c1', out: 'unused' },
    'ev-trunc-out': { id: 'ev-trunc-out', kind: 'thinking', stored_trunc: { out: { original_bytes: 20, head_bytes: 10, tail_bytes: 10 } } }
  };
  const h = loadAuditControllerHarness(async (url) => {
    const id = decodeURIComponent(url.split('/').pop());
    const ev = envs[id];
    if (!ev) return { ok: false, status: 404, async text() { return 'missing'; } };
    return { ok: true, status: 200, async json() { return ev; } };
  });

  h.ctx.WsAudit.openEvent('ev-rem');
  await new Promise((r) => setTimeout(r, 10));
  assert.equal(h.ctx.WsAudit.windows()[0].tab, 'out');

  h.ctx.WsAudit.openEvent('ev-sub');
  await new Promise((r) => setTimeout(r, 10));
  assert.equal(h.ctx.WsAudit.windows()[0].tab, 'in');

  h.ctx.WsAudit.openEvent('ev-other');
  await new Promise((r) => setTimeout(r, 10));
  assert.equal(h.ctx.WsAudit.windows()[0].tab, 'raw');

  h.ctx.WsAudit.openEvent('ev-call');
  await new Promise((r) => setTimeout(r, 10));
  assert.equal(h.ctx.WsAudit.windows()[0].tab, 'in');

  h.ctx.WsAudit.openEvent('ev-trunc-out');
  await new Promise((r) => setTimeout(r, 10));
  assert.equal(h.ctx.WsAudit.windows()[0].tab, 'out');

  h.ctx.WsAudit.openEvent('ev-rem', { tab: 'in' });
  await new Promise((r) => setTimeout(r, 10));
  assert.equal(h.ctx.WsAudit.windows()[0].tab, 'in');
});


test('cached hidden event invalidates pending tool lookup and envelope responses', async () => {
  const pending = [];
  const h = loadAuditControllerHarness((url) => new Promise((resolve) => pending.push({ url, resolve })));
  h.ctx.WsAudit.openEvent('hidden');
  pending.shift().resolve({ ok: true, async json() { return { id: 'hidden', kind: 'thinking', out: 'cached' }; } });
  await new Promise((r) => setTimeout(r, 0));
  const win = h.ctx.WsAudit.windows()[0];
  const row = makeCallRow(h.makeEl, 'late');
  h.convo.appendChild(row);
  h.ctx.WsAudit.openCall('late', row);
  const token = win.reqToken;
  h.ctx.WsAudit.openEvent('hidden');
  assert.ok(win.reqToken > token);
  for (const req of pending.splice(0)) req.resolve({ ok: true, async json() {
    return { call: { id: 'tool', kind: 'tool_call', call_id: 'late' }, events: [] };
  } });
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(win.activeEnvelope.id, 'hidden');
  h.ctx.WsAudit.openEvent('uncached');
  assert.equal(win.activeEnvelope, null);
  assert.equal(win.resultLoading, false);
  assert.equal(win.resultEnvelope, null);
  h.ctx.WsAudit.openEvent('hidden');
  for (const req of pending.splice(0)) req.resolve({ ok: true, async json() { return { id: 'uncached', kind: 'other' }; } });
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(win.activeEnvelope.id, 'hidden');
});

test('estimated compaction renders banner, localized meta row, and au-est window status', async () => {
  const h = loadAuditWindowHelpers();
  const estEv = {
    id: 'cmp-est',
    kind: 'compaction',
    subkind: 'auto',
    harness: 'agy',
    at: '2026-11-24T00:00:00.000000000Z',
    model_seen: true,
    meta: { estimated: true, inferred_by: 'usage_drop', pre_tokens: 180000, post_tokens: 30000 }
  };
  const body = h.auRenderBodyContent(estEv, 'in', {});
  assert.ok(body.html.includes('au-banner'));
  assert.ok(body.html.includes('js.audit.compaction.estimated.title'));
  assert.ok(body.html.includes('js.audit.compaction.estimated.note'));
  assert.ok(body.html.includes('js.audit.part.empty'));

  const explicit = h.auRenderBodyContent({ id: 'cmp-exp', kind: 'compaction', subkind: 'auto', model_seen: true }, 'in', {});
  assert.ok(!explicit.html.includes('js.audit.compaction.estimated.title'));

  const meta = h.auRenderMetaGrid({ activeEnvelope: estEv });
  assert.ok(meta.includes('>js.audit.meta.estimated<'));
  assert.ok(meta.includes('>js.audit.meta.yes<'));
  assert.ok(meta.includes('>meta.inferred_by<'));
  assert.ok(!meta.includes('>meta.estimated<'));

  const pending = [];
  const ctrl = loadAuditControllerHarness((url) => new Promise((resolve) => pending.push({ url, resolve })));
  ctrl.ctx.WsAudit.openEvent('cmp-est');
  const win = ctrl.ctx.WsAudit.windows()[0];
  const statEl = ctrl.makeEl('SPAN');
  statEl.className = 'au-wstatus';
  win.el.appendChild(statEl);
  pending.shift().resolve({ ok: true, status: 200, async json() { return estEv; } });
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(statEl.textContent, 'js.audit.compaction.estimated');
  assert.equal(statEl.className, 'au-wstatus au-est');
  assert.equal(statEl.hidden, false);
});

test('estimated context size formats (in+out)/4 uniformly across parallel calls and upgrades error icons', async () => {
  const h = loadAuditWindowHelpers();
  assert.equal(h.auEstContextTokens(0, 0), 0);
  assert.equal(h.auEstContextText(0, 0), '');
  assert.equal(h.auEstContextTokens(1, 0), 1);
  assert.equal(h.auEstContextText(1, 0), '+1');
  assert.equal(h.auEstContextTokens(100, 68), 42);
  assert.equal(h.auEstContextText(100, 68), '+42');
  assert.equal(h.auEstContextText(2048, 5325), '+1.8k');
  assert.equal(h.auEstContextText(0, 480000), '+120k');
  assert.equal(h.auEstContextText(0, 4800000), '+1.2M');
  assert.equal(
    h.auEstContextTitle(2048, 5325, null),
    '~1.8k tok (in 2.0 KB + out 5.2 KB)'
  );
  assert.equal(
    h.auEstContextTitle(2048, 5325, { ctx_before: 12000, ctx_after: 14500 }),
    '~1.8k tok (in 2.0 KB + out 5.2 KB) · 12000 → 14500'
  );

  // Hydration: parallel siblings sharing request_id BOTH get +(in+out)/4 (no "·"),
  // real ctx_before -> ctx_after appends to tooltip, and tool_result error swaps #i-check -> #i-x.
  const ctrl = loadAuditControllerHarness(async () => ({
    ok: true, status: 200,
    async json() {
      return {
        events: [
          { id: 'e1', seq: 1, kind: 'tool_call', call_id: 'p1', request_id: 'req-1', in_bytes: 2048, ctx_before: 10000, ctx_after: 12500 },
          { id: 'e2', seq: 2, kind: 'tool_call', call_id: 'p2', request_id: 'req-1', in_bytes: 400, ctx_before: 10000, ctx_after: 12500 },
          { id: 'e3', seq: 3, kind: 'tool_result', call_id: 'p1', out_bytes: 5325 },
          { id: 'e4', seq: 4, kind: 'tool_result', call_id: 'p2', out_bytes: 800, subkind: 'error' }
        ],
        before: 'e1', after: 'e4', end: true
      };
    }
  }));

  const makeRowWithIcon = (cid) => {
    const row = makeCallRow(ctrl.makeEl, cid);
    row.classList.add('au-done');
    const mk = row.querySelector('.au-mk');
    const svg = ctrl.makeEl('SVG');
    const use = ctrl.makeEl('USE');
    use.setAttribute('href', '#i-check');
    svg.appendChild(use);
    mk.appendChild(svg);
    return row;
  };
  const r1 = makeRowWithIcon('p1');
  const r2 = makeRowWithIcon('p2');
  ctrl.convo.appendChild(r1);
  ctrl.convo.appendChild(r2);

  await ctrl.ctx.WsAudit.hydrate();
  assert.equal(r1.querySelector('.au-tok').textContent, '+1.8k');
  assert.equal(r1.querySelector('.au-tok').classList.contains('au-sib'), false);
  assert.equal(r1.querySelector('.au-tok').title, '~1.8k tok (in 2.0 KB + out 5.2 KB) · 10000 → 12500');
  assert.equal(r1.querySelector('.au-mk use').getAttribute('href'), '#i-check');

  assert.equal(r2.querySelector('.au-tok').textContent, '+300', 'parallel sibling shows own +(in+out)/4, not dot placeholder');
  assert.equal(r2.querySelector('.au-tok').classList.contains('au-sib'), false);
  assert.equal(r2.querySelector('.au-tok').title, '~300 tok (in 400 B + out 800 B) · 10000 → 12500');
  assert.equal(r2.classList.contains('au-fail'), true);
  assert.equal(r2.classList.contains('au-done'), false);
  assert.equal(r2.querySelector('.au-mk use').getAttribute('href'), '#i-x', 'error hydration swaps check icon to x');
});

test('hydration keeps walking backward when tool_result precedes tool_call across pages', async () => {
  const urls = [];
  const ctrl = loadAuditControllerHarness(async (url) => {
    urls.push(url);
    if (!url.includes('before=')) {
      return {
        ok: true, status: 200,
        async json() {
          return {
            events: [{ id: 'e2', seq: 2, kind: 'tool_result', call_id: 'split-1', out_bytes: 800 }],
            before: 'e2', after: 'e2', end: false
          };
        }
      };
    }
    return {
      ok: true, status: 200,
      async json() {
        return {
          events: [{ id: 'e1', seq: 1, kind: 'tool_call', call_id: 'split-1', in_bytes: 400, duration_ms: 1500 }],
          before: 'e1', after: 'e1', end: true
        };
      }
    };
  });
  const r = makeCallRow(ctrl.makeEl, 'split-1');
  r.classList.add('au-done');
  ctrl.convo.appendChild(r);

  await ctrl.ctx.WsAudit.hydrate();
  assert.equal(urls.length, 2, 'must walk backward to fetch matching tool_call');
  assert.equal(r.querySelector('.au-tok').textContent, '+300');
  assert.equal(r.querySelector('.au-tok').title, '~300 tok (in 400 B + out 800 B)');
  assert.equal(r.querySelector('.au-dur').textContent, '1.5s');
  assert.equal(r.getAttribute('data-au-hydrated'), '1');
});

test('activity row polish CSS contracts: duration clipping, empty tok hiding, and trace palette tokens', () => {
  const dashCSS = fs.readFileSync(__dirname + '/assets/dashboard.css', 'utf8');
  const convCSS = fs.readFileSync(__dirname + '/assets/dashboard-conversation.css', 'utf8');
  const auditCSS = fs.readFileSync(__dirname + '/assets/dashboard-audit.css', 'utf8');

  assert.ok(convCSS.includes('grid-template-columns: 14px 20px minmax(0, 1fr) minmax(44px, auto);'),
    'col 4 must grow for duration + token estimate without clipping');
  assert.ok(auditCSS.includes('justify-content: flex-end;'), '.au-right must right-align items');
  assert.ok(/\.au-tok:empty\s*\{\s*display:\s*none;\s*\}/.test(auditCSS), '.au-tok:empty must collapse cleanly');

  for (const tok of ['--conv-trace-fg:', '--conv-trace-dim:', '--conv-trace-accent:']) {
    const count = dashCSS.split(tok).length - 1;
    assert.equal(count, 3, tok + ' must be defined in light :root, @media dark, and [data-theme="dark"]');
  }
});
