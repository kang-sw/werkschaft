// Run by TestColdInitStagingBrowser: a framed session page the shell loads
// hidden holds its nonessential initial loads (the tracker's per-store views,
// the chat tabs' terminal replay) until its first show; a page that is not
// framed starts them at load.
const assert = require('node:assert/strict');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base] = process.argv.slice(2);
const wait = ms => new Promise(r => setTimeout(r, ms));
async function until(fn, label, ms = 8000) {
  const end = Date.now() + ms;
  while (Date.now() < end) { if (await fn()) return; await wait(25); }
  throw new Error('timeout: ' + label);
}

// One store's summary: a single task, and one key the viewer has not seen.
const now = '2026-11-18T12:00:00Z';
const globals = {
  store: { name: 's1', label: '', remote: null }, viewer: { member: 'me', stream: 'me.pc', isMember: true, admin: true }, now,
  sync: { online: true, unpushed: [], wikiWarnings: [] },
  members: { me: { id: 'me', name: 'Me', leads: [], keys: [], revoked: [], profile: { given: 'Me' } } },
  priority: { names: ['Normal'], default: 0 }, attachments: { max_bytes: 1000000 }, labels: [], clones: [], admins: ['me'], rolesProblems: [], heads: {},
  stats: { events: 0, commits: 0, flagged: 0, concurrent: 0 }, itemOrder: ['i0'],
  my: { badge: ['i0'], assignments: ['a0'], given: [], team: [], created: [], received: { pending: [], answered: [], sent: [] },
    today: { work: ['a0'], pending: [], confirms: [] }, requests: { incoming: [], outgoing: [] }, related: { cc: [], mentioned: [], closed: [] }, ccNotices: [] },
};
const task = { id: 'i0', alias: 'i0', kind: 'task', title: 'Task', author: 'me', ts: now, lastActivity: now, status: 'todo', mentions: [], cc: [], assignments: ['a0'], pending: [], thread: [], children: [], backrefs: [], chain: ['i0'], depth: 0,
  parent: null, labels: [], priority: 0, priorityLevel: 0, priorityName: 'Normal', concurrent: 0, project: '', due: null, rollup: { total: 0, counts: {}, blocked: [] }, requests: [], refs: [] };
const summary = { cursor: 'epoch.1', reset: true, removed: [], feed: [], globals,
  items: { i0: { detailVersion: 'v1', seen: { badge: [], read: [] }, data: { item: task,
    assignments: { a0: { id: 'a0', item: 'i0', by: 'me', to: 'me', sender: 'me', state: 'live', status: 'todo', ts: now, lastUpdate: now, completion: 'assignee', chain: [] } },
    requests: {}, rules: {}, notices: [], time: { recentlyClosed: false, recentAssignments: {} } } } } };

async function open(browser, { framed, hash = '', hold = false }) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  const tab = await context.newPage();
  const errors = [], log = { lists: 0, views: [], terminals: 0, ws: [], checks: 0, seen: [], checkGate: null, viewGates: [] };
  tab.on('pageerror', e => errors.push(e.message));
  tab.on('websocket', ws => { if (ws.url().includes('/terminals/')) log.ws.push(ws.url()); });
  await context.addInitScript(() => {
    // The page a shell does not frame is the same document without its
    // data-frame mark; the mark goes before any page script runs.
    if (window.name === 'unframed') {
      const mo = new MutationObserver(() => { if (document.documentElement) { document.documentElement.removeAttribute('data-frame'); mo.disconnect(); } });
      mo.observe(document, { childList: true });
    }
    // The page's own sockets, so a test can deliver a stream event to them.
    const Socket = window.WebSocket;
    window.pageSockets = [];
    window.WebSocket = class extends Socket { constructor(...a) { super(...a); window.pageSockets.push(this); } };
    // One restored chat terminal tab, as a returning reader's browser holds.
    try { localStorage.setItem('ws.dashboard.cterm.livesession', JSON.stringify([{ id: 't1', num: 1 }])); } catch (e) { /* a sandboxed body frame has no storage */ }
  });
  await tab.route('**/__host', route => route.fulfill({ contentType: 'text/html', body: '<!doctype html><title>host</title><body></body>' }));
  await tab.route('**/tracker/stores', route => { log.lists++; route.fulfill({ json: { stores: [{ id: 's1', label: 'S1' }], rev: 1 } }); });
  await tab.route('**/tracker/stores/s1/**', async route => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith('/check')) {
      log.checks++;
      // A held check answers when the test says so, as a slow daemon does.
      if (hold) await new Promise(r => { log.checkGate = r; });
      return route.fulfill({ json: {} });
    }
    if (url.pathname.endsWith('/seen')) { log.seen.push(route.request().postDataJSON()); return route.fulfill({ json: {} }); }
    log.views.push(url.pathname);
    // The room's own store has a view with one new key; any other read fails.
    if (hold && /\/(summary|delta)$/.test(url.pathname)) await new Promise(r => log.viewGates.push(r));
    if (hash) route.fulfill({ json: summary }); else route.fulfill({ status: 503, json: { error: 'fixture' } });
  });
  await tab.route('**/s/livesession/terminals', route => {
    if (route.request().method() !== 'GET') return route.continue();
    log.terminals++;
    route.fulfill({ json: { terminals: [{ id: 't1', cwd: '/tmp', started_at: Date.now() }], folders: null } });
  });
  await tab.goto(base + '/__host');
  await tab.evaluate(([unframed, hash]) => {
    window.heads = 0;
    addEventListener('message', e => { if (e.data && e.data.wsShell === 'head') window.heads++; });
    const f = document.createElement('iframe');
    f.id = 'f'; f.name = unframed ? 'unframed' : ''; f.style.cssText = 'position:absolute;inset:0;width:100%;height:100%;border:0;visibility:hidden';
    f.src = '/s/livesession?frame=1' + hash;
    document.body.append(f);
  }, [!framed, hash]);
  const frame = async () => (await (await tab.locator('#f').elementHandle()).contentFrame());
  await until(async () => (await frame()).evaluate(() => document.readyState === 'complete').catch(() => false), 'frame load');
  const f = await frame();
  await until(() => f.evaluate(() => !!document.querySelector('#ctabs [data-ctab-term="t1"]')).catch(() => false), 'restored terminal tab').catch(async e => { console.log(JSON.stringify(await f.evaluate(() => ({ ctabs: document.getElementById('ctabs')?.outerHTML, term: !!window.WsTerm, ls: localStorage.getItem('ws.dashboard.cterm.livesession'), termAttr: document.documentElement.dataset.term }))), log); throw e; });
  await wait(1200);
  const shown = value => tab.evaluate(v => document.getElementById('f').contentWindow.postMessage({ wsShell: 'shown', shown: v }, location.origin), value);
  return { context, tab, log, errors, shown, f, heads: () => tab.evaluate(() => window.heads) };
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    // A page the shell loads hidden: ready for its show, but quiet.
    {
      const p = await open(browser, { framed: true });
      assert.ok(p.log.lists >= 1, 'the tracker store list (the header search button mounts from it) is read at load');
      assert.ok(await p.f.evaluate(() => !!document.getElementById('tkpage') && document.querySelector('.dwrap > .top').classList.contains('tk-room')), 'the tracker renderer (header room, search palette) mounted without the views');
      assert.ok(await p.heads() >= 1, 'the head the shell shows the page by is posted at load');
      assert.equal(p.log.terminals >= 1, true, 'the terminal list is read at load, so the tab exists');
      assert.deepEqual(p.log.views, [], 'no tracker store view is read before the first show');
      assert.deepEqual(p.log.ws, [], 'no terminal replay starts before the first show');
      await p.shown(false);
      await wait(600);
      assert.deepEqual(p.log.views, [], 'a hidden notice is not a first show');
      assert.deepEqual(p.log.ws, [], 'a hidden notice starts no replay');
      await p.shown(true);
      await until(() => p.log.views.length >= 1 && p.log.ws.length >= 1, 'deferred loads at the first show');
      assert.match(p.log.ws[0], /\/terminals\/t1\/ws\?from=0$/);
      const views = p.log.views.length, sockets = p.log.ws.length;
      // The fixture daemon answers the replay socket with a close, so the
      // card's own retry adds sockets; only the staged load's count matters.
      await p.shown(false); await wait(200); await p.shown(true); await wait(800);
      assert.equal(p.log.views.length, views, 'a later show repeats no deferred view load');
      assert.deepEqual(p.errors, []);
      console.log('PASS hidden framed page: lists=' + p.log.lists + ' views before show=0 after=' + views + ' replay before show=0 after=' + sockets);
      await p.context.close();
    }
    // A page the shell loads hidden at a store route: the route's own entry
    // reads that store once, and the viewed store is marked seen from that
    // read; the first show then reads no second summary for it.
    {
      const p = await open(browser, { framed: true, hash: '#/s1' });
      await until(() => p.log.seen.length >= 1, 'the entered store is marked seen').catch(async e => { console.log(JSON.stringify(p.log), p.errors); throw e; });
      assert.deepEqual(p.log.seen[0], { badge: ['i0'], read: [] });
      const summaries = () => p.log.views.filter(v => /\/(summary|delta)$/.test(v)).length;
      assert.equal(summaries(), 1, 'the route entry reads one view (summary) while the page is hidden');
      assert.deepEqual(p.log.ws, [], 'a route does not start the terminal replay');
      await p.shown(true);
      await until(() => p.log.ws.length >= 1, 'replay at the first show');
      await wait(800);
      assert.equal(summaries(), 1, 'the first show adds no second summary for the entered store');
      assert.equal(p.log.seen.length, 1);
      assert.deepEqual(p.errors, []);
      await p.context.close();
      console.log('PASS hidden framed page at #/s1: summaries=1 seen=1 across the first show');
    }
    // The first show lands while the route entry's check is still out, and the
    // daemon's revision bump (the check's own TrackerChanged) reaches the page
    // as a tracker event before the entry's view answers: that nudge must not
    // start a second read that supersedes the entry's, or the viewed store's
    // new key would load unmarked.
    {
      const p = await open(browser, { framed: true, hash: '#/s1', hold: true });
      await until(() => p.log.checks >= 1 && p.log.checkGate, 'the entry check is out');
      await p.shown(true);
      await wait(400);
      assert.deepEqual(p.log.views, [], 'a pending claim leaves the first show no store view to read');
      p.log.checkGate();
      await until(() => p.log.viewGates.length >= 1, 'the entry read starts after the check');
      await p.f.evaluate(() => {
        const sock = window.pageSockets.filter(s => s.url.includes('/events') && s.readyState === 1).pop();
        sock.onmessage({ data: JSON.stringify({ event: 'tracker', data: { rev: 2 } }) });
      });
      await wait(400);
      // Answer every read the page makes, in the order it asks.
      for (let i = 0; i < 20; i++) { while (p.log.viewGates.length) p.log.viewGates.shift()(); await wait(100); }
      assert.equal(p.log.seen.length, 1, 'the viewed store is marked seen once, from the data its entry asked for');
      assert.deepEqual(p.log.seen[0], { badge: ['i0'], read: [] });
      assert.deepEqual(p.errors, []);
      await p.context.close();
      console.log('PASS hidden framed page at #/s1 shown while the entry check is out: views=' + p.log.views.length + ' seen=1');
    }
    // Selecting a restored chat tab on a page still held starts its replay
    // at once, so the pane never shows an empty emulator.
    {
      const p = await open(browser, { framed: true });
      assert.deepEqual(p.log.ws, []);
      await p.f.evaluate(() => document.querySelector('#ctabs [data-ctab-term="t1"]').click());
      await until(() => p.log.ws.length >= 1, 'replay at the tab selection');
      assert.match(p.log.ws[0], /\/terminals\/t1\/ws\?from=0$/);
      await p.context.close();
      console.log('PASS a selected tab starts its replay before the first show');
    }
    // A page shown at once: the first show comes with the load.
    {
      const p = await open(browser, { framed: true });
      await p.shown(true);
      await until(() => p.log.views.length >= 1 && p.log.ws.length >= 1, 'loads at an immediate show');
      await p.context.close();
      console.log('PASS immediately shown framed page starts its loads');
    }
    // A page that is not framed loads everything at once, as before.
    {
      const p = await open(browser, { framed: false });
      await until(() => p.log.views.length >= 1 && p.log.ws.length >= 1, 'standalone loads').catch(e => { console.log(JSON.stringify(p.log), p.errors); throw e; });
      assert.deepEqual(p.errors, []);
      console.log('PASS unframed page: views=' + p.log.views.length + ' replay=' + p.log.ws.length + ' with no shown message');
      await p.context.close();
    }
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exitCode = 1; });
