// Run by TestHoldersPopupBrowser against a disposable daemon: the shell's
// bottom-bar Holders trigger and its upward popup, per scenario, theme, and
// viewport. The page's /holders, /holders/metrics, and host-terminal reads
// are fulfilled here from the mockup's fixtures (a lead with two workers,
// one nested by parent_reply_id and one by the listing's parents map, and
// OS processes under them); stops and closes are recorded, never sent.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, shots, mockup] = process.argv.slice(2);

const NOW = Date.now();
// Holder, failure, and terminal times are epoch milliseconds on the wire.
const mins = n => NOW - n * 60000;
const lead = { id: 'h-main', pid: 41208, attached: true, restart_safe: true, started_at: mins(240), session: { kind: 'lead', label: 'main lead', backend: 'agy', reply_id: 'r-main' } };
const workers = [
  { id: 'h-w1', pid: 41955, attached: true, restart_safe: true, started_at: mins(22), session: { kind: 'agent-worker', label: 'worker: Phase 2', backend: 'agy', reply_id: 'r-w1', parent_reply_id: 'r-main' } },
  { id: 'h-w2', pid: 42110, attached: true, restart_safe: true, started_at: mins(9), session: { kind: 'agent-worker', label: 'worker: code-review-lite', backend: 'claude', reply_id: 'r-w2' } }];
const durable = { id: 'durable.werk', attached: true, restart_safe: true };
// The trace's base load per holder (MB, CPU%) and OS processes, as the mockup.
const LOAD = {
  'h-main': [86, 1.4], 'h-w1': [76, 0.3, [[42301, 'go test ./internal/wsdashboard', 98, 4.1], [42340, 'chromium-headless', 210, 2.4]]],
  'h-w2': [142, 1.2], 'h-exec': [380, 7.8], 'h-feat2': [240, 1.0], 'h-art': [190, 0.4]
};
// metrics answers tick n of the trace for the holders of data: each load
// wobbles per tick, and the history grows to 24 points.
function metrics(data, n) {
  const MB = 1048576, holders = {};
  for (const h of data.holders) {
    const l = LOAD[h.id];
    if (!l || !(h.pid > 0)) continue;
    const at = k => 1 + 0.35 * Math.sin(k * 1.7 + h.pid);
    const procs = (data.procs[h.id] || l[2] || []).map(([pid, name, rss, cpu]) => ({ pid, ppid: h.pid, name, rss: rss * MB, cpu: +(cpu * at(n + pid)).toFixed(1) }));
    const tot = k => l[1] * at(k) + procs.reduce((a, p) => a + p.cpu, 0);
    const len = Math.min(24, n + 6), spark = { rss: [], cpu: [] };
    for (let k = n - len + 1; k <= n; k++) { spark.cpu.push(+tot(k).toFixed(1)); spark.rss.push(Math.round((l[0] + procs.reduce((a, p) => a + p.rss / MB, 0)) * (0.97 + 0.03 * at(k)) * MB)); }
    holders[h.id] = { pid: h.pid, rss: spark.rss.at(-1), cpu: spark.cpu.at(-1), spark, procs };
  }
  return { ts: Date.now(), interval_ms: 1500, supported: true, holders };
}
const hostTerms = [
  { id: 'term-h0-a1', cwd: '/Users/dev/devenv/agents-plugin-tool', state: 'running', started_at: mins(95) },
  { id: 'term-h0-b2', cwd: '/Users/dev/devenv/ai-docs', state: 'exited', started_at: mins(310) }];

function scenario(name) {
  const d = { holders: [lead, ...workers, durable], parents: { 'h-w2': 'r-main' }, failures: [], incomplete: false, can_stop: true, terms: hostTerms, procs: {} };
  if (name === 'empty') return { holders: [], failures: [], incomplete: false, can_stop: true, terms: [], procs: {} };
  if (name === 'healthy') {
    d.holders = d.holders.concat([{ id: 'h-exec', pid: 40877, attached: true, restart_safe: true, started_at: mins(130), session: { kind: 'exec', label: 'exec · pnpm dev' } }]);
    d.procs['h-exec'] = [[40902, 'node vite --host', 164, 1.6]];
    d.terms = [hostTerms[0]];
    return d;
  }
  d.holders = d.holders.concat([
    { id: 'h-exec', pid: 40877, attached: true, restart_safe: false, started_at: mins(130), session: { kind: 'exec', label: 'exec · go build watcher' } },
    { id: 'h-feat2', pid: 48211, attached: false, cause: 'no_answer', protocol: 3, started_at: mins(14), session: { kind: 'lead', label: 'feature 2', backend: 'agy' } },
    { id: 'h-art', pid: 47102, attached: false, cause: 'token', protocol: 3, started_at: mins(62), session: { kind: 'lead', label: '' } }]);
  d.failures = [{ message: 'holder dial /tmp/ws-holder-9f2c.sock: connection refused', at: mins(3) }];
  if (name === 'incomplete') {
    d.holders = d.holders.filter(h => h.id !== 'h-art').map(h => h.id === 'h-feat2' ? Object.assign({}, h, { cause: 'protocol' }) : h);
    d.incomplete = true;
  }
  return d;
}

function shot(target, name) {
  if (!shots) return Promise.resolve();
  fs.mkdirSync(shots, { recursive: true });
  return target.screenshot({ path: path.join(shots, name + '.png'), animations: 'disabled' });
}

const VIEWPORTS = { wide: 1440, compact: 1180 };
// Every scenario at dark/wide; the warning one also in every theme x viewport.
const RUNS = [];
for (const s of ['warning', 'healthy', 'empty', 'noframe', 'incomplete']) RUNS.push({ theme: 'dark', vp: 'wide', s });
RUNS.push({ theme: 'light', vp: 'wide', s: 'warning' }, { theme: 'dark', vp: 'compact', s: 'warning' }, { theme: 'light', vp: 'compact', s: 'warning' }, { theme: 'light', vp: 'compact', s: 'empty' });

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    if (mockup && shots) {
      const page = await browser.newPage({ viewport: { width: 1600, height: 900 } });
      await page.goto(pathToFileURL(mockup).href);
      for (const r of RUNS) {
        for (const [ctl, val] of [['theme', r.theme], ['viewport', r.vp], ['scenario', r.s]]) await page.locator(`.m-seg[data-ctl="${ctl}"] .m-opt[data-val="${val}"]`).click();
        if (await page.locator('#holders-pop').isHidden()) await page.locator('#holders-trigger').click();
        await page.waitForTimeout(150);
        await shot(page.locator('#viewport'), `mockup-${r.theme}-${r.vp}-${r.s}`);
      }
      await page.close();
    }
    for (const r of RUNS) await run(browser, r);
    console.log('PASS holders popup: ' + RUNS.map(r => `${r.theme}/${r.vp}/${r.s}`).join(', '));
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });

async function run(browser, r) {
  const label = `${r.theme}/${r.vp}/${r.s}`, data = scenario(r.s);
  const context = await browser.newContext({ viewport: { width: VIEWPORTS[r.vp], height: 860 }, colorScheme: r.theme });
  await context.addCookies([{ name: 'ws_dashboard_theme', value: r.theme, url: base }]);
  await context.addInitScript(() => {
    if (self !== top) return;
    const workers = navigator.serviceWorker;
    if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
  });
  const posts = [], reads = [], traced = [];
  await context.route('**/*', route => {
    const req = route.request(), url = new URL(req.url());
    if (url.origin !== base) return route.abort();
    if (url.pathname === '/holders' && req.method() === 'GET') {
      reads.push('holders');
      const { terms, procs, ...list } = data;
      return route.fulfill({ json: list, headers: { 'Cache-Control': 'no-store' } });
    }
    if (url.pathname === '/holders/metrics' && req.method() === 'GET') {
      traced.push(Date.now());
      return route.fulfill({ json: metrics(data, traced.length), headers: { 'Cache-Control': 'no-store' } });
    }
    if (url.pathname === '/s/host0/terminals' && req.method() === 'GET') {
      reads.push('terms');
      return route.fulfill({ json: { anchor: { session: 'host0' }, terminals: data.terms } });
    }
    if (req.method() === 'POST' && (/^\/holders\/[^/]+\/stop$/.test(url.pathname) || /^\/s\/host0\/terminals\/[^/]+\/close$/.test(url.pathname))) {
      posts.push(url.pathname);
      return route.fulfill({ status: 204, body: '' });
    }
    return route.continue();
  });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.goto(base + '/s/livesession');
  await page.frameLocator('.sframe.on').locator('#grid').waitFor();
  const trig = page.locator('#holders-trigger');
  await trig.waitFor();

  // The trigger: state, count of running OS holders (not durable), breakdown.
  const running = data.holders.filter(h => h.pid > 0 && !h.id.startsWith('durable.')).length;
  const warn = data.holders.some(h => !h.attached || h.restart_safe === false) || data.failures.length || data.incomplete;
  const state = warn ? 'warn' : running ? 'ok' : 'zero';
  await page.waitForFunction(s => document.querySelector('#holders-trigger').dataset.state === s && /—/.test(document.querySelector('#holders-trigger').title), state);
  assert.equal(await page.locator('#hd-count').textContent(), String(running), `${label}: trigger counts running OS holders`);
  const tb = await trig.boundingBox(), bar = await page.locator('#shell-bottom').boundingBox();
  assert.ok(tb.y >= bar.y - 0.5 && tb.y + tb.height <= bar.y + bar.height + 0.5, `${label}: trigger sits inside the bottom bar`);
  assert.ok(tb.x + tb.width <= VIEWPORTS[r.vp] + 0.5 && tb.x + tb.width > VIEWPORTS[r.vp] - 200, `${label}: trigger sits at the bar's right end`);
  // Closed, nothing polls: one load read, no terminal read.
  await page.waitForTimeout(400);
  assert.deepEqual(reads, ['holders'], `${label}: the closed popup reads /holders once and never its terminals`);
  assert.equal(traced.length, 0, `${label}: the closed popup never traces`);

  if (r.s === 'noframe') await page.frameLocator('.sframe.on').locator('html').evaluate(h => h.removeAttribute('data-term'));
  const hasTerm = await page.frameLocator('.sframe.on').locator('html').evaluate(h => h.hasAttribute('data-term'));

  await trig.click();
  const pop = page.locator('#holders-pop');
  await pop.waitFor({ state: 'visible' });
  assert.equal(await trig.getAttribute('aria-expanded'), 'true', `${label}: trigger reports the open popup`);
  await page.waitForFunction(n => document.querySelectorAll('#hdlist .hd-term').length === n, data.terms.length);
  // The trace runs while open: the first answer fills the metric columns,
  // the totals, and the live pill.
  await page.waitForFunction(() => {
    const p = document.querySelector('#holders-pop'), agg = document.querySelector('#hd-agg'), t = document.querySelector('#hd-trace');
    return p.classList.contains('hd-mx-on') && agg && !agg.hidden && t && !t.hidden && t.dataset.on === 'true';
  });
  assert.ok(traced.length >= 1, `${label}: opening starts the trace`);
  if (r.s !== 'empty') {
    const tree = await page.evaluate(() => {
      const q = s => document.querySelector('#holders-pop ' + s);
      const chev = id => q(`.hd-row[data-id="${id}"] .hd-chev`);
      return {
        w1: !!q('[data-node="h-main"] > .hd-kids > [data-node="h-w1"]'),
        w2: !!q('[data-node="h-main"] > .hd-kids > [data-node="h-w2"]'),
        mainOpen: chev('h-main').getAttribute('aria-expanded'),
        w1Open: chev('h-w1').getAttribute('aria-expanded'),
        w2Chev: chev('h-w2').disabled,
        w1Os: document.querySelectorAll('#holders-pop [data-node="h-w1"] .hd-os').length,
        rss: q('[data-k="h-main:rss"]').textContent,
        cpu: q('[data-k="h-main:cpu"]').textContent,
        spark: q('[data-k="h-main:spark"] .hd-sp-cpu').getAttribute('points'),
        agg: q('[data-k="agg:rss"]').textContent
      };
    });
    assert.ok(tree.w1 && tree.w2, `${label}: workers nest under their lead (parent_reply_id and the parents map)`);
    assert.equal(tree.mainOpen, 'true', `${label}: a holder with spawned holders starts open`);
    assert.equal(tree.w1Open, 'false', `${label}: a holder with only OS processes starts closed`);
    assert.equal(tree.w2Chev, true, `${label}: a childless holder's chevron is inert`);
    assert.equal(tree.w1Os, 2, `${label}: the worker's OS processes are its children`);
    assert.match(tree.rss, /^\d+(\.\d+)? (MB|GB)$/, `${label}: RSS cell`);
    assert.match(tree.cpu, /^\d+\.\d%$/, `${label}: CPU cell`);
    assert.match(tree.spark, /^[\d.]+,[\d.]+( [\d.]+,[\d.]+)+$/, `${label}: sparkline drawn`);
    assert.match(tree.agg, /(MB|GB)$/, `${label}: totals drawn`);
  }
  await page.waitForTimeout(250);

  // Geometry: over the bar, inside the viewport, no horizontal overflow.
  const g = await page.evaluate(() => {
    const p = document.querySelector('#holders-pop').getBoundingClientRect(), b = document.querySelector('#shell-bottom').getBoundingClientRect();
    const list = document.querySelector('#hdlist');
    return { p: { l: p.left, t: p.top, r: p.right, b: p.bottom, w: p.width }, barTop: b.top, vw: innerWidth, over: list.scrollWidth - list.clientWidth };
  });
  assert.ok(g.p.b <= g.barTop + 0.5, `${label}: popup stands above the bar (${g.p.b} > ${g.barTop})`);
  assert.ok(g.p.t >= 7.5 && g.p.l >= 7.5 && g.p.r <= g.vw - 7.5, `${label}: popup stays inside the viewport ${JSON.stringify(g.p)}`);
  assert.ok(g.over <= 1, `${label}: popup body does not scroll sideways (${g.over}px)`);
  assert.ok(Math.abs(g.p.r - (tb.x + tb.width)) <= 9, `${label}: popup right-aligns to the trigger`);

  // Content per scenario.
  const lost = data.holders.filter(h => !h.attached && !h.id.startsWith('durable.')).length;
  assert.equal(await pop.locator('.hd-hold').count(), lost, `${label}: a hold-to-stop on each unattached holder only`);
  assert.equal(await pop.locator('.hd-banner').count(), data.incomplete ? 1 : 0, `${label}: incomplete banner`);
  assert.equal(await pop.locator('.hd-trow.hd-fail').count(), data.failures.length, `${label}: failed starts listed`);
  assert.ok(!/NaN/.test(await pop.textContent()), `${label}: ages read as times`);
  if (r.s === 'empty') {
    assert.ok((await pop.locator('.hd-empty').count()) >= 2, `${label}: empty sections say so`);
  }
  const attach = pop.locator('button[data-act="term-attach"]');
  for (let i = 0; i < await attach.count(); i++) {
    assert.equal(await attach.nth(i).isDisabled(), !hasTerm, `${label}: Attach follows the frame's terminal support`);
  }
  if (r.s === 'noframe') assert.ok(await attach.count() > 0 && !hasTerm, `${label}: Attach present and off without a terminal frame`);
  await shot(page, `live-${r.theme}-${r.vp}-${r.s}`);

  if (r.s === 'warning' && r.theme === 'dark' && r.vp === 'wide') {
    const tag = label.replace(/\//g, '-');
    // The chevron opens a worker's OS processes and folds a lead's subtree.
    const w1 = pop.locator('.hd-row[data-id="h-w1"] .hd-chev');
    await w1.click();
    assert.equal(await w1.getAttribute('aria-expanded'), 'true', `${label}: chevron opens`);
    assert.equal(await pop.locator('[data-node="h-w1"] .hd-os:visible').count(), 2, `${label}: opened OS processes show`);
    assert.equal(await pop.locator('.hd-row[data-id="h-w1/42301"] .hd-meta').textContent(), 'PID 42301 → 41955', `${label}: an OS process names its parent`);
    let over = await page.evaluate(() => { const l = document.querySelector('#hdlist'); return l.scrollWidth - l.clientWidth; });
    assert.ok(over <= 1, `${label}: the deepest rows do not scroll sideways (${over}px)`);
    await shot(page, `live-${tag}-tree`);
    const main = pop.locator('.hd-row[data-id="h-main"] .hd-chev');
    await main.click();
    assert.ok(await pop.locator('.hd-row[data-id="h-w1"]').isHidden(), `${label}: folding the lead hides its workers`);
    await main.click();
    assert.ok(await pop.locator('[data-node="h-w1"] .hd-os').first().isVisible(), `${label}: refolding keeps the worker's own choice`);
    // Pause stops the trace's reads; resuming asks at once.
    const pause = pop.locator('#hd-pause');
    await pause.click();
    assert.equal(await pause.getAttribute('aria-pressed'), 'true', `${label}: pause pressed`);
    assert.equal(await pop.locator('#hd-trace').getAttribute('data-on'), 'false', `${label}: the pill reads paused`);
    await page.waitForTimeout(150);
    let n = traced.length;
    await page.waitForTimeout(2200);
    assert.equal(traced.length, n, `${label}: a paused trace reads nothing`);
    await shot(page, `live-${tag}-paused`);
    await pause.click();
    await page.waitForTimeout(300);
    assert.ok(traced.length > n, `${label}: resuming reads again`);
    // A short press only explains the hold.
    const hold = pop.locator('.hd-hold').first();
    await hold.click();
    await page.waitForFunction(() => document.querySelector('#hdnote').textContent.trim() !== '');
    assert.equal(posts.length, 0, `${label}: a click never stops`);
    // A 1.5s hold stops once, through trace ticks that keep the held button.
    await hold.evaluate(b => { window.__held = b; });
    const spark0 = await pop.locator('[data-k="h-main:spark"] .hd-sp-cpu').getAttribute('points'), ticks0 = traced.length;
    const hb = await hold.boundingBox();
    await page.mouse.move(hb.x + hb.width / 2, hb.y + hb.height / 2);
    await page.mouse.down();
    await page.waitForTimeout(700);
    await shot(page, `live-${tag}-holding`);
    await page.waitForTimeout(900);
    assert.ok(traced.length > ticks0, `${label}: the trace ticks during the hold`);
    assert.notEqual(await pop.locator('[data-k="h-main:spark"] .hd-sp-cpu').getAttribute('points'), spark0, `${label}: the sparkline moves during the hold`);
    assert.ok(await page.evaluate(() => window.__held.isConnected && window.__held === document.querySelector('#holders-pop .hd-hold[data-id="h-feat2"]')), `${label}: a tick keeps the held button`);
    await page.waitForTimeout(200);
    await page.mouse.up();
    await page.waitForFunction(() => /h-feat2|feature 2/.test(document.querySelector('#hdnote').textContent));
    assert.deepEqual(posts, ['/holders/h-feat2/stop'], `${label}: the hold posts one stop`);
    // A running terminal asks first; Escape dismisses the question, then the popup.
    await pop.locator('button[data-act="term-stop"][data-id="term-h0-a1"]').click();
    await pop.locator('.hd-confirm').waitFor();
    assert.equal(await page.evaluate(() => document.activeElement.getAttribute('data-act')), 'term-yes', `${label}: confirm takes focus`);
    await shot(page, `live-${label.replace(/\//g, '-')}-confirm`);
    await page.keyboard.press('Escape');
    await page.waitForFunction(() => !document.querySelector('#holders-pop .hd-confirm'));
    assert.ok(await pop.isVisible(), `${label}: first Escape keeps the popup`);
    assert.equal(await page.evaluate(() => document.activeElement.getAttribute('data-act')), 'term-stop', `${label}: focus returns to Stop`);
    assert.equal(posts.length, 1, `${label}: dismissing never closes the terminal`);
    await page.keyboard.press('Escape');
    await pop.waitFor({ state: 'hidden' });
    assert.equal(await trig.getAttribute('aria-expanded'), 'false', `${label}: second Escape closes the popup`);
    // Closed, the trace stops.
    await page.waitForTimeout(150);
    n = traced.length;
    await page.waitForTimeout(2200);
    assert.equal(traced.length, n, `${label}: a closed popup reads no metrics`);
    // Confirming closes the terminal once; an exited terminal goes at once.
    await trig.click();
    await pop.waitFor({ state: 'visible' });
    await pop.locator('button[data-act="term-stop"][data-id="term-h0-a1"]').click();
    await pop.locator('button[data-act="term-yes"]').click();
    await pop.locator('button[data-act="term-stop"][data-id="term-h0-b2"]').click();
    await page.waitForTimeout(200);
    assert.deepEqual(posts.slice(1), ['/s/host0/terminals/term-h0-a1/close', '/s/host0/terminals/term-h0-b2/close'], `${label}: one close per terminal`);
    // The bottom-bar popovers exclude each other.
    const usage = page.locator('#shell-bottom .usage-toggle');
    if (await usage.count() && await usage.isVisible()) {
      await usage.click();
      await pop.waitFor({ state: 'hidden' });
    }
  }
  assert.deepEqual(errors, [], `${label}: page errors`);
  await context.close();
}
