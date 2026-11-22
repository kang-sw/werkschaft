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

async function open(browser, { framed, firstShown }) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  const tab = await context.newPage();
  const errors = [], log = { lists: 0, views: [], terminals: 0, ws: [], heads: 0, loadedAt: 0 };
  tab.on('pageerror', e => errors.push(e.message));
  tab.on('console', m => { if (process.env.COLD_DEBUG) console.log('console:', m.text()); });
  tab.on('requestfailed', r => { if (process.env.COLD_DEBUG) console.log('failed:', r.url(), r.failure().errorText); });
  tab.on('websocket', ws => { if (ws.url().includes('/terminals/')) log.ws.push(ws.url()); });
  await context.addInitScript(() => {
    // The page a shell does not frame is the same document without its
    // data-frame mark; the mark goes before any page script runs.
    if (window.name === 'unframed') {
      const mo = new MutationObserver(() => { if (document.documentElement) { document.documentElement.removeAttribute('data-frame'); mo.disconnect(); } });
      mo.observe(document, { childList: true });
    }
    // One restored chat terminal tab, as a returning reader's browser holds.
    try { localStorage.setItem('ws.dashboard.cterm.livesession', JSON.stringify([{ id: 't1', num: 1 }])); } catch (e) { /* a sandboxed body frame has no storage */ }
  });
  await tab.route('**/__host', route => route.fulfill({ contentType: 'text/html', body: '<!doctype html><title>host</title><body></body>' }));
  await tab.route('**/tracker/stores', route => { log.lists++; route.fulfill({ json: { stores: [{ id: 's1', label: 'S1' }], rev: 1 } }); });
  await tab.route('**/tracker/stores/s1/**', route => { log.views.push(new URL(route.request().url()).pathname); route.fulfill({ status: 503, json: { error: 'fixture' } }); });
  await tab.route('**/s/livesession/terminals', route => {
    if (route.request().method() !== 'GET') return route.continue();
    log.terminals++;
    route.fulfill({ json: { terminals: [{ id: 't1', cwd: '/tmp', started_at: Date.now() }], folders: null } });
  });
  await tab.goto(base + '/__host');
  await tab.evaluate(unframed => {
    window.heads = 0;
    addEventListener('message', e => { if (e.data && e.data.wsShell === 'head') window.heads++; });
    const f = document.createElement('iframe');
    f.id = 'f'; f.name = unframed ? 'unframed' : ''; f.style.cssText = 'position:absolute;inset:0;width:100%;height:100%;border:0;visibility:hidden';
    f.src = '/s/livesession?frame=1';
    document.body.append(f);
  }, !framed);
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
