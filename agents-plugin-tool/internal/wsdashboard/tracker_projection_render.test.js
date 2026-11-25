// Focused renderer contract tests; no DOM, daemon, or detail requests.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const panel = fs.readFileSync(path.join(__dirname, 'assets/tracker-panel.js'), 'utf8');
const { readAssetSource } = require('./testdata/asset-source.cjs');
const tracker = readAssetSource('wsdashboard', 'tracker.js');
function harness() {
  const c = vm.createContext({
    VS: { now: '2026-11-18T12:00:00Z', items: {}, assignments: {}, members: {} },
    esc: s => String(s == null ? '' : s).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('"', '&quot;').replaceAll("'", '&#39;'),
    nm: s => s || '', stLabel: s => s, wrapped: s => s === 'done' || s === 'dropped',
    wsT: (key, ...args) => JSON.stringify([key, ...args]),
    wsTn: (key, ...args) => JSON.stringify([key, ...args]),
    dayNum: s => Date.parse(s) / 86400000,
    partHtml: (cls, tip, text) => JSON.stringify([cls, tip, text]),
    SI: { info: s => ({ label: s }) },
    ENDED: { done: 1, dropped: 1, declined: 1, returned: 1, handed: 1, withdrawn: 1 }
  });
  vm.runInContext(panel, c);
  for (const name of ['today', 'itemDueState', 'dueChip', 'dueHtml']) {
    const start = tracker.indexOf('  function ' + name + '(');
    const end = tracker.indexOf('\n  }', start) + 4;
    // today is a one-line helper.
    vm.runInContext(name === 'today' ? tracker.slice(start, tracker.indexOf('\n', start)) : tracker.slice(start, end), c);
  }
  c.A = id => c.VS.assignments[id];
  return c;
}

test('projected feed sentence variants escape all plain inputs', () => {
  const c = harness();
  const variants = [
    ['assigned', {}, 'assigned'], ['assigned', { inherited: true, ruleLevels: 0 }, 'assigned.all'],
    ['pending', {}, 'pending'], ['pending', { handoff: true }, 'pending.handoff'],
    ['request', { to: 'me' }, 'request'], ['request', {}, 'request.of'],
    ['done', {}, 'finished'], ['blocked', {}, 'blocked'], ['dropped', {}, 'dropped'],
    ['answer', { noticeKind: 'declined' }, 'declined'],
    ['answer', { noticeKind: 'returned' }, 'returned'], ['answer', { handoff: true }, 'returned.handoff'],
    ['reference', { noticeKind: 'handoff' }, 'tookover'], ['reference', {}, 'accepted'],
    ['reference', { handoff: true }, 'accepted.handoff'], ['confirm', {}, 'confirm'],
    ['reply', {}, 'reply'], ['mention', {}, 'mention'], ['mention', { bodyMention: true }, 'mention.body'],
    ['cc', { type: 'comment.add' }, 'cc.comment'],
    ['cc', { type: 'item.status', by: '<to>' }, 'cc.status.own'],
    ['cc', { type: 'item.status' }, 'cc.status'],
    ...['close', 'confirm', 'reject', 'reopen'].map(t => ['cc', { type: 'item.' + t }, 'cc.' + t])
  ];
  for (const [kind, extra, suffix] of variants) {
    const x = { kind, by: '<by>', to: '<to>', from: '<from>', endedBy: '<ender>', statusBy: '<statusby>', status: '<status>', snippet: '<script>"&', ...extra };
    const html = c.projectedFeedTxt(x, 'me');
    assert.ok(html.startsWith('["js.tk.feed.' + suffix + '"'), html);
    assert.ok(!html.includes('<'), html);
  }
  assert.equal(c.projectedFeedTxt({ kind: 'due', overdue: false }, 'me'), '["js.tk.due.today"]');
  assert.equal(c.projectedFeedTxt({ kind: 'due', overdue: true, due: '<date>' }, 'me'), '["js.tk.feed.overdue","&lt;date>"]');
});

test('summary feed needs no legacy payloads, retains keys/order/events, and empty means empty', () => {
  const c = harness();
  c.VS.items.i = {};
  c.VS.feed = [{ key: 'k', kind: 'reply', item: 'i', ts: 'ts', ev: 'event', by: '<by>', snippet: '😀'.repeat(77) + '...' }];
  const row = c.feedEntries('me')[0];
  assert.equal(row.key, 'k'); assert.equal(row.ev, 'event');
  assert.equal(row.html, c.wsT('js.tk.feed.reply', '&lt;by>', '&ldquo;' + '😀'.repeat(77) + '...&rdquo;'));
  c.VS.feed = [];
  assert.equal(c.feedEntries('me').length, 0);
  assert.equal(c.snippet('😀'.repeat(81)), '&ldquo;' + '😀'.repeat(77) + '...&rdquo;');
});

test('legacy feed uses identical localized reply and mention sentences', () => {
  const c = harness();
  c.VS.items.i = { id: 'i', author: 'me', mentions: [] };
  c.VS.events = []; c.VS.notices = [];
  c.VS.my = { assignments: [], received: { pending: [] }, today: { confirms: [] } };
  c.listOf = x => x || []; c.vals = Object.values;
  c.VS.comments = { q: { id: 'q', author: 'me' }, r: { id: 'r', item: 'i', author: '<by>', reply_to: 'q', mentions: ['me'], body: '<text>', ts: 'ts' } };
  const legacy = c.feedEntries('me');
  for (const row of legacy) assert.equal(row.html, c.projectedFeedTxt({ kind: row.kind, by: '<by>', snippet: '<text>' }, 'me'));
  assert.equal(legacy.length, 2);
});

test('per-item recent boundaries override hourly skew, legacy fallback and search remain', () => {
  const c = harness();
  const rec = { id: 'a', item: 'i', state: 'live', status: 'done', lastUpdate: '2026-11-04T12:15:00Z' };
  c.VS.assignments.a = rec;
  c.VS.items.i = { assignments: ['a'], lastActivity: rec.lastUpdate };
  assert.equal(c.recentlyClosed('i'), true);
  assert.equal(c.recentlyClosed('i', rec), true);
  c.VS.itemTime = { i: { recentlyClosed: false, recentAssignments: { a: false } } };
  assert.equal(c.recentlyClosed('i'), false);
  assert.equal(c.recentlyClosed('i', rec), false);
  c.CLOSED = { entries: [], older: 0 };
  c.addClosed(rec.lastUpdate, 1, 'row', false);
  assert.equal(c.CLOSED.older, 1);
  c.SQ = 'search'; c.addClosed(rec.lastUpdate, 1, 'row', false);
  assert.equal(c.CLOSED.entries.length, 1);
});

test('an editor resumed across an epoch reset pins the newly matching body and revision', () => {
  const c = harness(), writes = fs.readFileSync(path.join(__dirname, 'assets/tracker-writes.js'), 'utf8');
  vm.runInContext(writes.slice(writes.indexOf('  function startEdit('), writes.indexOf('  function endEdit(')), c);
  c.VS.items.i = { id: 'i', title: 'old', body: 'stale', bodyRev: ['old'], labels: [] };
  c.ui = { store: 's', item: 'i', win: true, edpv: {}, eatt: {} };
  let ready = false, continuation;
  const replacement = { items: { i: { id: 'i', title: 'new', body: 'matching new', bodyRev: ['new'], labels: [] } } };
  c.AD = { detailReady: () => ready, detail: (s, ids, done) => { continuation = done; } };
  c.showItem = () => {}; c.$ = () => null; c.render = () => { c.VS = replacement; };
  c.startEdit('i');
  assert.equal(c.ui.iedit, undefined);
  ready = true; continuation();
  assert.equal(c.ui.iedit.body, 'matching new');
  assert.equal(c.ui.iedit.base[0], 'new');
});

test('due chips and row dates are equivalent with projections and legacy clock', () => {
  const c = harness();
  for (const [due, state] of [['2026-11-17', 'over'], ['2026-11-18', 'today'], ['2026-11-20', 'future']]) {
    const it = { id: 'i', kind: 'task', due, status: 'todo' };
    delete c.VS.itemTime;
    const chip = c.dueChip(it), row = c.dueHtml(it, 'todo', false);
    c.VS.itemTime = { i: { dueState: state } };
    assert.equal(c.dueChip(it), chip);
    assert.equal(c.dueHtml(it, 'todo', false), row);
    it.status = 'done';
    assert.ok(!c.dueChip(it).includes('due over'));
  }
});

test('gantt left labels stay single-line with ellipsis, indent cap, and title tooltip', () => {
  const css = fs.readFileSync(path.join(__dirname, 'assets/tracker.css'), 'utf8');
  assert.match(css, /\.tk\s+\.grow-axis,\s*\.tk\s+\.grow-row\s*\{[^}]*grid-template-columns:\s*minmax\(220px,\s*38%\)\s+1fr/);
  assert.match(css, /\.tk\s+\.glabel\s*\{[^}]*flex-wrap:\s*nowrap[^}]*overflow:\s*hidden[^}]*calc\(min\(var\(--ind,\s*0\),\s*5\)\s*\*\s*14px\)/);
  assert.match(css, /\.tk\s+\.glabel\s+\.title\s*\{[^}]*white-space:\s*nowrap[^}]*overflow:\s*hidden[^}]*text-overflow:\s*ellipsis[^}]*min-width:\s*0[^}]*flex:\s*1\s+1\s+0/);
  assert.match(css, /\.tk\s+\.glabel\s*>\s*\.muted\s*\{[^}]*text-overflow:\s*ellipsis[^}]*white-space:\s*nowrap/);
  assert.match(css, /\.tk\s+\.glabel\s+\.tmore\s*\{[^}]*white-space:\s*nowrap/);

  const c = harness();
  c.attr = (k, v) => ' data-' + k + '="' + c.esc(v) + '"';
  c.alias = id => id;
  for (const name of ['tipAttr', 'titleRef']) {
    const start = tracker.indexOf('  function ' + name + '(');
    const end = name === 'tipAttr' ? tracker.indexOf('\n', start) : tracker.indexOf('\n  }', start) + 4;
    vm.runInContext(tracker.slice(start, end), c);
  }
  c.VS.items.i1 = { id: 'i1', title: 'Very long "quoted" & <escaped> timeline title' };
  const plain = c.titleRef('i1');
  const tipped = c.titleRef('i1', true);
  assert.ok(!plain.includes('data-tip='), 'default titleRef omits data-tip');
  assert.ok(tipped.includes('data-tip="Very long &quot;quoted&quot; &amp; &lt;escaped> timeline title"'), tipped);
  const ganttSrc = fs.readFileSync(path.join(__dirname, 'assets/tracker-window.js'), 'utf8');
  assert.match(ganttSrc, /titleRef\(it\.id,\s*true\)/, 'ganttBody passes tip=true to titleRef');
});
