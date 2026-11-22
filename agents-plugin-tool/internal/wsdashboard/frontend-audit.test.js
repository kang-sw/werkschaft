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
  const res = h.auRenderBodyContent(ev, 'in', { search: 'tail', activeMatch: 0, wrap: true });
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
