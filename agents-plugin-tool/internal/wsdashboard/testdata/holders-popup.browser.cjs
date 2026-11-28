// Run by TestHoldersPopupBrowser against a disposable daemon: the shell's
// bottom-bar Holders trigger and its upward popup, per scenario, theme, and
// viewport. The page's /holders and host-terminal reads are fulfilled here
// (the mockup's fixtures, flattened to Phase 1's single level); stops and
// closes are recorded, never sent.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, shots, mockup] = process.argv.slice(2);

const NOW = Date.now();
// Holder, failure, and terminal times are epoch milliseconds on the wire.
const mins = n => NOW - n * 60000;
const lead = { id: 'h-main', pid: 41208, attached: true, restart_safe: true, started_at: mins(240), session: { kind: 'lead', label: 'main lead', backend: 'agy' } };
const durable = { id: 'durable.werk', attached: true, restart_safe: true };
const hostTerms = [
  { id: 'term-h0-a1', cwd: '/Users/dev/devenv/agents-plugin-tool', state: 'running', started_at: mins(95) },
  { id: 'term-h0-b2', cwd: '/Users/dev/devenv/ai-docs', state: 'exited', started_at: mins(310) }];

function scenario(name) {
  const d = { holders: [lead, durable], failures: [], incomplete: false, can_stop: true, terms: hostTerms };
  if (name === 'empty') return { holders: [], failures: [], incomplete: false, can_stop: true, terms: [] };
  if (name === 'healthy') {
    d.holders = d.holders.concat([{ id: 'h-exec', pid: 40877, attached: true, restart_safe: true, started_at: mins(130), session: { kind: 'exec', label: 'exec · pnpm dev' } }]);
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
  const posts = [], reads = [];
  await context.route('**/*', route => {
    const req = route.request(), url = new URL(req.url());
    if (url.origin !== base) return route.abort();
    if (url.pathname === '/holders' && req.method() === 'GET') {
      reads.push('holders');
      const { terms, ...list } = data;
      return route.fulfill({ json: list, headers: { 'Cache-Control': 'no-store' } });
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

  if (r.s === 'noframe') await page.frameLocator('.sframe.on').locator('html').evaluate(h => h.removeAttribute('data-term'));
  const hasTerm = await page.frameLocator('.sframe.on').locator('html').evaluate(h => h.hasAttribute('data-term'));

  await trig.click();
  const pop = page.locator('#holders-pop');
  await pop.waitFor({ state: 'visible' });
  assert.equal(await trig.getAttribute('aria-expanded'), 'true', `${label}: trigger reports the open popup`);
  await page.waitForFunction(n => document.querySelectorAll('#hdlist .hd-term').length === n, data.terms.length);
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
    // A short press only explains the hold.
    const hold = pop.locator('.hd-hold').first();
    await hold.click();
    await page.waitForFunction(() => document.querySelector('#hdnote').textContent.trim() !== '');
    assert.equal(posts.length, 0, `${label}: a click never stops`);
    // A 1.5s hold stops once.
    const hb = await hold.boundingBox();
    await page.mouse.move(hb.x + hb.width / 2, hb.y + hb.height / 2);
    await page.mouse.down();
    await page.waitForTimeout(700);
    await shot(page, `live-${label.replace(/\//g, '-')}-holding`);
    await page.waitForTimeout(1100);
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
