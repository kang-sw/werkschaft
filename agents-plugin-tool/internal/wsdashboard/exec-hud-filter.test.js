const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const assets = path.join(__dirname, 'assets');
const hudSrc = fs.readFileSync(path.join(assets, 'dashboard-exec-hud.js'), 'utf8');
const peekSrc = fs.readFileSync(path.join(assets, 'dashboard-exec-hud-peek.js'), 'utf8');

function extractBlock(src, startRe, endRe) {
  const start = src.search(startRe);
  assert.ok(start >= 0, 'start not found: ' + startRe);
  const rest = src.slice(start);
  const end = rest.search(endRe);
  assert.ok(end >= 0, 'end not found: ' + endRe);
  return rest.slice(0, end);
}

const helpersBlock = extractBlock(hudSrc, /  function xhSt\(j\)/, /\n  function xhOwnerLabel\(/);
const scheduleBlock = extractBlock(hudSrc, /  function xhSchedule\(vis, now\)/, /\n  \/\/ layout decides/);
const layoutNowBlock = extractBlock(hudSrc, /  function xhLayoutNow\(\)/, /\n  \/\/ The group is a toolbar/);
const peekJobsBlock = extractBlock(peekSrc, /  function xhPeekJobs\(\)/, /\n  function xhOpenPeek\(/);

function makeNode(tag, cls) {
  const classes = new Set((cls || '').split(/\s+/).filter(Boolean));
  const attrs = Object.create(null);
  return {
    tag,
    hidden: false,
    textContent: '',
    offsetWidth: 40,
    clientWidth: 600,
    scrollWidth: 40,
    children: [],
    get firstChild() { return this.children[0] || null; },
    get className() { return [...classes].join(' '); },
    set className(v) { classes.clear(); String(v || '').split(/\s+/).filter(Boolean).forEach(c => classes.add(c)); },
    classList: {
      add(c) { classes.add(c); },
      remove(c) { classes.delete(c); },
      contains(c) { return classes.has(c); }
    },
    appendChild(child) { this.children.push(child); return child; },
    setAttribute(k, v) { attrs[k] = String(v); },
    getAttribute(k) { return attrs[k] || null; }
  };
}

function createHarness(initial = {}) {
  const timers = { intervals: new Map(), timeouts: new Map(), nextId: 1 };
  const xhGroup = makeNode('span', 'xgroup');
  const xhMore = makeNode('button', 'xmore');
  const xhAgg = makeNode('button', 'xchip xagg');
  const composer = makeNode('div', 'composer');
  composer.querySelector = () => ({ clientWidth: 600 });
  const ctx = {
    XH_PIN_MS: 10000,
    XH_FADE_MS: 12000,
    XH_NARROW_PX: 320,
    XH_GAP: 6,
    xhJobs: initial.jobs || [],
    xhStopping: initial.stopping || Object.create(null),
    xhDismissed: initial.dismissed || Object.create(null),
    xhHidden: 0,
    xhVis: [],
    xhSlots: [],
    xhShown: [],
    xhFolded: [],
    xhGroup,
    xhMore,
    xhAgg,
    xhTip: null,
    xhTick: 0,
    xhFadeTimer: 0,
    xhPk: Object.assign({ open: false, id: '' }, initial.pk || {}),
    composer,
    doc: { hidden: false },
    window: { getComputedStyle: () => ({ paddingLeft: '0', borderLeftWidth: '0', borderRightWidth: '0' }) },
    $: () => null,
    wsT: (k, a) => (a === undefined ? k : k + ':' + a),
    xhEl: (tag, cls, text) => { const n = makeNode(tag, cls); if (text != null) n.textContent = text; return n; },
    xhWorst: () => null,
    xhNode: () => makeNode('span', 'xnode'),
    xhRove: () => {},
    xhRender: () => {},
    setInterval(fn, ms) { const id = timers.nextId++; timers.intervals.set(id, { fn, ms }); return id; },
    clearInterval(id) { timers.intervals.delete(id); },
    setTimeout(fn, ms) { const id = timers.nextId++; timers.timeouts.set(id, { fn, ms }); return id; },
    clearTimeout(id) { timers.timeouts.delete(id); }
  };
  vm.createContext(ctx);
  vm.runInContext(helpersBlock + '\n' + scheduleBlock + '\n' + layoutNowBlock + '\n' + peekJobsBlock, ctx);
  return { ctx, timers };
}

test('xhSkipSyncAgent filters only ended non-attention synchronous agent jobs', () => {
  const { ctx } = createHarness({ stopping: { stopping_sync: true } });
  const cases = [
    // Skipped (ended, non-attention, agent, not async)
    [{ id: 's_ok', state: 'exited', exit_code: 0, origin: 'agent' }, true],
    [{ id: 's_ok_false', state: 'exited', exit_code: 0, origin: 'agent', async: false }, true],
    [{ id: 's_stop', state: 'stopped', origin: 'agent' }, true],
    [{ id: 's_stop_user_sent', state: 'stopped', origin: 'agent', stopped_by_user: true, stop_notice: 'sent' }, true],

    // Kept: running / stopping
    [{ id: 'r_sync', state: 'running', origin: 'agent' }, false],
    [{ id: 'stopping_sync', state: 'running', origin: 'agent' }, false],

    // Kept: attention states even when sync agent
    [{ id: 'f_exit', state: 'exited', exit_code: 1, origin: 'agent' }, false],
    [{ id: 'f_sig', state: 'exited', exit_code: 0, signal: 9, origin: 'agent' }, false],
    [{ id: 'f_nostart', state: 'failed', origin: 'agent' }, false],
    [{ id: 'f_lost', state: 'lost', origin: 'agent' }, false],
    [{ id: 'f_notice_fail', state: 'stopped', origin: 'agent', stopped_by_user: true, stop_notice: 'failed' }, false],
    [{ id: 'f_notice_closed', state: 'stopped', origin: 'agent', stopped_by_user: true, stop_notice: 'closed' }, false],

    // Kept: async agent jobs
    [{ id: 'a_ok', state: 'exited', exit_code: 0, origin: 'agent', async: true }, false],
    [{ id: 'a_stop', state: 'stopped', origin: 'agent', async: true }, false],

    // Kept: all user `!` jobs
    [{ id: 'u_ok', state: 'exited', exit_code: 0, origin: 'user' }, false],
    [{ id: 'u_stop', state: 'stopped', origin: 'user' }, false],
    [{ id: 'u_run', state: 'running', origin: 'user' }, false]
  ];
  for (const [job, want] of cases) {
    assert.equal(ctx.xhSkipSyncAgent(job), want, 'job ' + job.id);
  }
});

test('xhVisible excludes sync ended agent jobs from vis and xhHidden while keeping async, user, running, and attention jobs', () => {
  const now = 200000;
  const { ctx } = createHarness({
    jobs: [
      { id: 'sync_ok_recent', state: 'exited', exit_code: 0, origin: 'agent', started_at: now - 3000, ended_at: now - 1000 },
      { id: 'sync_ok_old', state: 'exited', exit_code: 0, origin: 'agent', started_at: now - 50000, ended_at: now - 40000 },
      { id: 'sync_run', state: 'running', origin: 'agent', started_at: now - 5000 },
      { id: 'sync_fail', state: 'exited', exit_code: 2, origin: 'agent', started_at: now - 50000, ended_at: now - 40000 },
      { id: 'async_ok_recent', state: 'exited', exit_code: 0, origin: 'agent', async: true, started_at: now - 3000, ended_at: now - 1000 },
      { id: 'async_ok_old', state: 'exited', exit_code: 0, origin: 'agent', async: true, started_at: now - 50000, ended_at: now - 40000 },
      { id: 'user_ok_recent', state: 'exited', exit_code: 0, origin: 'user', started_at: now - 2500, ended_at: now - 500 },
      { id: 'user_ok_old', state: 'exited', exit_code: 0, origin: 'user', started_at: now - 50000, ended_at: now - 40000 }
    ]
  });

  const vis = Array.from(ctx.xhVisible(now), j => j.id);
  assert.deepEqual(vis, ['user_ok_recent', 'async_ok_recent', 'sync_fail', 'sync_run']);
  assert.equal(ctx.xhHidden, 2, 'only async_ok_old and user_ok_old count toward ended N');
});

test('xhSchedule does not set a fade timer for ended sync agent jobs, but does for async and user jobs', () => {
  const now = 200000;
  const syncOnly = createHarness({
    jobs: [
      { id: 'sync_ok', state: 'exited', exit_code: 0, origin: 'agent', started_at: now - 2000, ended_at: now - 500 }
    ]
  });
  const visSync = syncOnly.ctx.xhVisible(now);
  syncOnly.ctx.xhSchedule(visSync, now);
  assert.equal(syncOnly.timers.timeouts.size, 0, 'no fade timer for hidden sync agent job');
  assert.equal(syncOnly.timers.intervals.size, 0);

  const asyncOne = createHarness({
    jobs: [
      { id: 'async_ok', state: 'exited', exit_code: 0, origin: 'agent', async: true, started_at: now - 2000, ended_at: now - 500 }
    ]
  });
  const visAsync = asyncOne.ctx.xhVisible(now);
  asyncOne.ctx.xhSchedule(visAsync, now);
  assert.equal(asyncOne.timers.timeouts.size, 1, 'fade timer scheduled for ended async agent job');
});

test('xhLayoutNow hides the strip when only sync ended agent jobs exist and excludes them from quiet xhFolded', () => {
  const now = 200000;
  const syncOnly = createHarness({
    jobs: [
      { id: 'sync_ok', state: 'exited', exit_code: 0, origin: 'agent', started_at: now - 50000, ended_at: now - 40000 }
    ]
  });
  syncOnly.ctx.xhVis = syncOnly.ctx.xhVisible(now);
  syncOnly.ctx.xhLayoutNow();
  assert.equal(syncOnly.ctx.xhGroup.hidden, true, 'no quiet pill for sync-only ended agent jobs');
  assert.equal(syncOnly.ctx.xhFolded.length, 0);

  const mixed = createHarness({
    jobs: [
      { id: 'sync_ok', state: 'exited', exit_code: 0, origin: 'agent', started_at: now - 50000, ended_at: now - 40000 },
      { id: 'async_ok', state: 'exited', exit_code: 0, origin: 'agent', async: true, started_at: now - 50000, ended_at: now - 40000 },
      { id: 'user_ok', state: 'exited', exit_code: 0, origin: 'user', started_at: now - 50000, ended_at: now - 40000 }
    ]
  });
  mixed.ctx.xhVis = mixed.ctx.xhVisible(now);
  mixed.ctx.xhLayoutNow();
  assert.equal(mixed.ctx.xhGroup.hidden, false);
  assert.equal(mixed.ctx.xhAgg.classList.contains('quiet'), true);
  assert.deepEqual(Array.from(mixed.ctx.xhFolded, j => j.id), ['async_ok', 'user_ok']);
});

test('xhPeekJobs excludes ended sync agent jobs unless currently open in peek', () => {
  const jobs = [
    { id: 'sync_ok', state: 'exited', exit_code: 0, origin: 'agent', started_at: 10 },
    { id: 'sync_run', state: 'running', origin: 'agent', started_at: 20 },
    { id: 'sync_fail', state: 'exited', exit_code: 1, origin: 'agent', started_at: 30 },
    { id: 'async_ok', state: 'exited', exit_code: 0, origin: 'agent', async: true, started_at: 40 },
    { id: 'user_ok', state: 'exited', exit_code: 0, origin: 'user', started_at: 50 }
  ];

  const closed = createHarness({ jobs });
  assert.deepEqual(Array.from(closed.ctx.xhPeekJobs(), j => j.id), ['sync_run', 'sync_fail', 'async_ok', 'user_ok']);

  const openOnSync = createHarness({ jobs, pk: { open: true, id: 'sync_ok' } });
  assert.deepEqual(Array.from(openOnSync.ctx.xhPeekJobs(), j => j.id), ['sync_ok', 'sync_run', 'sync_fail', 'async_ok', 'user_ok']);
});
