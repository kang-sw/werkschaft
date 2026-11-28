// Run by TestGitPageBrowser: the gallery's Git page (#gitdock) on a real
// framed page over a real repository, with a 900px #bgal (wide: three regions
// side by side) and a 380px #bgal (narrow: three tabs), in both themes. The
// stored gallery column stops at 720px, so the 900px gallery is the narrow
// view's overlay over the board column (viewport below 1600px), sized by
// resizing the viewport until the overlay measures 900px; the 380px gallery
// is the 2560x1440 column (the always-open column needs the spare width a
// 3-column 1920 desktop lacks). It checks Ctrl+G's idempotent show, the branch
// line, the review pill and its jump to a frontier past the graph's first
// page (narrow: the graph tab first), the frontier marks, the light
// worktree list (cache-only, no disk walk) and its manual full scan, the
// commit and file windows (.gwin-back), no horizontal overflow, and the
// no-repository empty state. With a shots directory it keeps each state for
// the side-by-side check against the approved mockup.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
// bases: one daemon per git session (dark 900, dark 380, light 900, light
// 380), so each opens on a cold worktree size cache.
const [baseList, plainBase, frontier, shots] = process.argv.slice(2);
const bases = baseList.split(',');

function shot(fr, name) {
  if (!shots) return Promise.resolve();
  fs.mkdirSync(shots, { recursive: true });
  return fr.locator('#bgal').screenshot({ path: path.join(shots, name + '.png') });
}
async function frameOf(page) {
  const h = await page.locator('iframe.sframe.on').elementHandle();
  return h.contentFrame();
}
const raf2 = fr => fr.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
async function until(page, what, fn, ms = 10000) {
  const end = Date.now() + ms;
  let last;
  while (Date.now() < end) {
    last = await fn();
    if (last) return last;
    await page.waitForTimeout(100);
  }
  throw new Error('timed out waiting for ' + what + ' (last ' + JSON.stringify(last) + ')');
}
const shown = fr => fr.evaluate(() => {
  const C = window.WsCards, root = document.getElementById('gitpage'), dock = document.getElementById('gitdock');
  return !!C && C.activeSys() === 'git' && !!root && !!dock && root.parentNode === dock && !root.hidden && root.getClientRects().length > 0;
});
async function ctrlG(page, fr) {
  await fr.evaluate(() => { if (document.activeElement && document.activeElement.blur) document.activeElement.blur(); document.body.focus(); });
  await page.keyboard.press('Control+g');
}

// overflow measures the page: the dock, the page, the strip, every shown
// region and the document scroll nothing sideways, and no shown strip or
// header part leaves the dock.
function overflow(fr) {
  return fr.evaluate(() => {
    const dock = document.getElementById('gitdock'), root = document.getElementById('gitpage'), dr = dock.getBoundingClientRect();
    const sx = e => e ? e.scrollWidth - e.clientWidth : 0;
    const cards = [...root.querySelectorAll('.gp-card')].filter(e => e.getClientRects().length).map(sx).reduce((a, b) => Math.max(a, b), 0);
    const out = [...root.querySelectorAll('.gp-strip *, .gp-hd > *, .gp-tabs > *')].filter(e => e.getClientRects().length)
      .map(e => ({ e, r: e.getBoundingClientRect() })).filter(o => o.r.width && (o.r.right > dr.right + 0.5 || o.r.left < dr.left - 0.5))
      .map(o => (o.e.id || o.e.className.baseVal || o.e.className) + ' ' + Math.round(o.r.left) + '..' + Math.round(o.r.right) + '/' + Math.round(dr.right));
    const d = document.documentElement;
    return { dock: sx(dock), page: sx(root), strip: sx(root.querySelector('.gp-strip')), cards, doc: d.scrollWidth - d.clientWidth, out };
  });
}
async function noOverflow(fr, label) {
  await raf2(fr);
  assert.deepEqual(await overflow(fr), { dock: 0, page: 0, strip: 0, cards: 0, doc: 0, out: [] }, `${label}: no horizontal overflow`);
}

// galWidth measures the shown gallery (#bgal) in the frame.
const galWidth = fr => fr.evaluate(() => { const g = window.WsCards && window.WsCards.gallery && window.WsCards.gallery(); return g ? Math.round(g.getBoundingClientRect().width) : 0; });

// fitOverlay resizes the viewport until the narrow view's gallery overlay
// measures galW (it tracks the board column, which tracks the viewport).
async function fitOverlay(page, fr, galW) {
  // A secant step: the column may take only part of each viewport pixel.
  const settle = async () => { await page.waitForTimeout(200); await raf2(fr); return galWidth(fr); };
  let vp = page.viewportSize(), w = await settle(), prev = null;
  const seen = [];
  for (let i = 0; i < 8 && Math.abs(w - galW) > 2; i++) {
    seen.push(vp.width + ':' + w);
    const slope = prev && prev.w !== w ? (w - prev.w) / (vp.width - prev.vw) : 1;
    prev = { vw: vp.width, w };
    vp = { width: Math.round(vp.width + (galW - w) / (slope > 0.05 ? slope : 1)), height: vp.height };
    await page.setViewportSize(vp);
    w = await settle();
  }
  seen.push(vp.width + ':' + w);
  assert.ok(Math.abs(w - galW) <= 2, `the gallery overlay is ${galW}px (viewport:width ${seen.join(' ')})`);
  return w;
}

async function newSession(browser, theme, galW, url) {
  const overlay = galW > 720;
  const context = await browser.newContext({ viewport: overlay ? { width: 1300, height: 1000 } : { width: 2560, height: 1440 }, colorScheme: theme });
  const origin = new URL(url).origin;
  await context.addCookies([{ name: 'ws_dashboard_theme', value: theme, url: origin }]);
  await context.addInitScript(w => {
    if (self === top) {
      const workers = navigator.serviceWorker;
      if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
    }
    try {
      if (sessionStorage.getItem('gpfix')) return;
      sessionStorage.setItem('gpfix', '1');
      localStorage.setItem('ws.dashboard.bgal.w', String(w));
    } catch (_) {}
  }, galW);
  const reqs = [];
  const errors = [];
  // The tracker and (after the page hands the gallery to the Ticket Map) the
  // ticket graph have no store in this fixture; their 404s are not the page's.
  const tracker = u => { const p = new URL(u).pathname; return p === '/tracker/stores' || /\/tickets\/graph$/.test(p); };
  // A full worktree scan answers slowly, so its busy state can be seen.
  await context.route('**/*', async route => {
    const u = new URL(route.request().url());
    if (u.origin !== origin) return route.abort();
    if (/\/worktrees$/.test(u.pathname) && !u.searchParams.has('no_size')) await new Promise(r => setTimeout(r, 1200));
    return route.continue();
  });
  const page = await context.newPage();
  page.on('pageerror', e => errors.push('pageerror: ' + e.message));
  page.on('console', m => { if (m.type() === 'error' && !(m.location().url && tracker(m.location().url))) errors.push('console: ' + m.text()); });
  page.on('response', r => { if (r.status() >= 400 && !(r.status() === 404 && tracker(r.url()))) errors.push(`response ${r.status()}: ${r.url()}`); });
  page.on('request', r => { const u = new URL(r.url()); if (/\/(git\/log|git\/status|worktrees|review-ledger)$/.test(u.pathname)) reqs.push(u.pathname.replace(/^\/s\/[^/]+\//, '') + u.search); });
  await page.goto(url + '/s/livesession');
  const fr = await frameOf(page);
  await fr.locator('#gitdock').waitFor({ state: 'attached' });
  return { context, page, fr, reqs, errors };
}

async function gitSession(browser, theme, galW, base) {
  const tag = `${theme}/gal${galW}`, narrow = galW < 720;
  const { context, page, fr, reqs, errors } = await newSession(browser, theme, galW, base);
  await page.waitForTimeout(400);
  assert.equal(reqs.filter(r => !r.startsWith('git/status')).length, 0, `${tag}: nothing reads before the page shows ${JSON.stringify(reqs)}`);

  // 1. Ctrl+G shows the page and focuses its host.
  await ctrlG(page, fr);
  await until(page, `${tag} the git page`, () => shown(fr));
  if (galW > 720) await fitOverlay(page, fr, galW);
  else assert.ok(Math.abs(await galWidth(fr) - galW) <= 2, `${tag}: the gallery column is ${galW}px (got ${await galWidth(fr)}px)`);
  assert.equal(await fr.evaluate(() => document.getElementById('gitdock').contains(document.activeElement)), true, `${tag}: Ctrl+G focuses the page`);

  // 2. The branch line, the pill, the counts.
  const strip = await until(page, `${tag} branch line and pill`, () => fr.evaluate(() => {
    const br = document.querySelector('#gpref .gp-br'), pill = document.getElementById('gppill');
    return br && pill.dataset.state !== 'loading' ? { br: br.textContent, pill: pill.dataset.state, disabled: pill.disabled, tx: pill.textContent } : null;
  }));
  assert.match(strip.br, /main/, `${tag}: the branch line ${JSON.stringify(strip)}`);
  assert.ok(['ok', 'warn'].includes(strip.pill) && !strip.disabled, `${tag}: the review pill ${JSON.stringify(strip)}`);
  const files = await until(page, `${tag} changed files`, () => fr.evaluate(() => { const n = document.querySelectorAll('#gfiles .gf[data-side]').length; return n ? n : 0; }));
  assert.equal(files, 4, `${tag}: four changed files`);
  assert.equal(await fr.evaluate(() => document.getElementById(document.querySelector('#gitpage .gp-tabs').offsetWidth ? 'gpnfiles' : 'gpfcount').textContent.trim()), '4', `${tag}: the changes count`);

  // 3. Wide: three regions side by side and no tabs; narrow: the tabs and
  // one region.
  const lay = await fr.evaluate(() => {
    const vis = s => { const e = document.querySelector(s); return !!e && e.getClientRects().length > 0; };
    return { tabs: vis('#gitpage .gp-tabs'), graph: vis('#glog'), diff: vis('#gpanel'), wt: vis('#wtcard'), tab: document.getElementById('gitpage').dataset.tab };
  });
  assert.deepEqual(lay, narrow ? { tabs: true, graph: true, diff: false, wt: false, tab: 'graph' } : { tabs: false, graph: true, diff: true, wt: true, tab: 'graph' }, `${tag}: layout`);
  await until(page, `${tag} graph rows`, () => fr.evaluate(() => document.querySelectorAll('#glrows .glr').length >= 200));
  await noOverflow(fr, `${tag} graph`);
  if (narrow) {
    await fr.locator('.gp-tab[data-gp-tab="worktrees"]').click();
    await until(page, `${tag} worktrees tab`, () => fr.evaluate(() => document.getElementById('wtcard').getClientRects().length > 0));
  }

  // 4. The light worktree list: cache-only, no sizes, no full read.
  await until(page, `${tag} light list`, () => fr.evaluate(() => document.querySelectorAll('#wtlist .wtrow').length === 5));
  assert.equal(await fr.evaluate(() => document.querySelectorAll('#wtlist .wtsize.gp-wt-none').length), 5, `${tag}: no sizes in the light list`);
  assert.ok(reqs.some(r => r === 'worktrees?no_size=1') && !reqs.some(r => r === 'worktrees' || r === 'worktrees?wait=1'), `${tag}: only the cache-only read ${JSON.stringify(reqs)}`);
  assert.equal(await fr.evaluate(() => document.getElementById('gpwtmode').dataset.mode), 'light');
  await noOverflow(fr, `${tag} worktrees`);
  await shot(fr, `gp-${theme}-${narrow ? 'narrow' : 'wide'}`);

  // 5. The manual scan: busy, then the sizes and the scanned mode.
  await fr.locator('#gpscan').click();
  await until(page, `${tag} busy scan`, () => fr.evaluate(() => document.getElementById('gpscan').dataset.scan === 'busy'));
  assert.equal(await fr.evaluate(() => document.querySelectorAll('#wtlist .wtsize.gp-wt-scan').length), 5, `${tag}: the unmeasured rows show the scan at once`);
  await shot(fr, `gp-${theme}-${narrow ? 'narrow' : 'wide'}-busy`);
  await until(page, `${tag} scanned`, () => fr.evaluate(() => document.getElementById('gpscan').dataset.scan === 'done' && document.getElementById('gpwtmode').dataset.mode === 'scanned'));
  assert.equal(await fr.evaluate(() => document.querySelectorAll('#wtlist .wtsize.gp-wt-none, #wtlist .wtsize.gp-wt-scan').length), 0, `${tag}: every size measured`);
  assert.ok(reqs.some(r => r === 'worktrees' || r === 'worktrees?wait=1'), `${tag}: the scan read the sizes ${JSON.stringify(reqs)}`);

  // 6. The pill's jump: the frontier is past the first page; narrow switches
  // to the graph tab first; the row centres, focuses, and wears the marks.
  await fr.locator('#gppill').click();
  const jumped = await until(page, `${tag} frontier row`, () => fr.evaluate(oid => {
    const row = document.querySelector('#glrows .glr.gp-front');
    if (!row || document.activeElement !== row) return null;
    const sc = document.getElementById('glscroll').getBoundingClientRect(), r = row.getBoundingClientRect();
    // The row scrolls smoothly; wait for it to settle in view.
    if (!(r.top >= sc.top && r.bottom <= sc.bottom)) return null;
    return { rows: document.querySelectorAll('#glrows .glr').length, tab: document.getElementById('gitpage').dataset.tab, inView: r.top >= sc.top && r.bottom <= sc.bottom,
      badge: (row.querySelector('.gp-rf-front') || {}).textContent || '', ring: !!document.querySelector('#glrows .gp-ring'), oid: row.textContent.includes(oid.slice(0, 7)) };
  }, frontier), 15000);
  assert.ok(jumped.rows > 200, `${tag}: the jump paged the graph ${JSON.stringify(jumped)}`);
  assert.deepEqual([jumped.tab, jumped.inView, jumped.badge, jumped.ring, jumped.oid], ['graph', true, 'Review Frontier', true, true], `${tag}: the frontier row ${JSON.stringify(jumped)}`);
  const mini = await fr.evaluate(() => { const b = document.getElementById('gpfmini'); return { shown: b.getClientRects().length > 0, text: b.textContent }; });
  if (narrow) assert.equal(mini.shown, false, `${tag}: the narrow graph header drops the frontier chip`);
  else assert.ok(mini.shown && mini.text.includes('Frontier ' + frontier.slice(0, 7)), `${tag}: the graph header's frontier chip ${JSON.stringify(mini)}`);
  await noOverflow(fr, `${tag} jumped`);
  await shot(fr, `gp-${theme}-${narrow ? 'narrow' : 'wide'}-frontier`);

  // 7. The commit window and the file window open over the page (.gwin-back).
  await fr.locator('#glrows .glr').first().click();
  await until(page, `${tag} commit window`, () => fr.evaluate(() => { const b = document.querySelector('.gwin-back'); return !!b && !b.hidden; }));
  await page.keyboard.press('Escape');
  await until(page, `${tag} commit window closed`, () => fr.evaluate(() => document.querySelector('.gwin-back').hidden));
  if (narrow) await fr.locator('.gp-tab[data-gp-tab="diff"]').click();
  await fr.locator('#gfiles .gf[data-side]').first().click();
  await until(page, `${tag} file window`, () => fr.evaluate(() => { const b = document.querySelector('.gwin-back'); return !!b && !b.hidden; }));
  await page.keyboard.press('Escape');
  await until(page, `${tag} file window closed`, () => fr.evaluate(() => document.querySelector('.gwin-back').hidden));

  // 8. Ctrl+G again: the page stays and only refocuses (no toggle).
  await ctrlG(page, fr);
  await page.waitForTimeout(200);
  assert.equal(await shown(fr), true, `${tag}: a second Ctrl+G keeps the page`);
  assert.equal(await fr.evaluate(() => document.getElementById('gitdock').contains(document.activeElement)), true, `${tag}: a second Ctrl+G refocuses the page`);

  // 9. Leaving the page reads nothing more and trims the graph.
  await fr.evaluate(() => window.WsCards.showSys('ticketmap'));
  await page.waitForTimeout(300);
  const left = reqs.length;
  await page.waitForTimeout(800);
  assert.equal(reqs.length, left, `${tag}: nothing reads while the page is hidden`);

  assert.deepEqual(errors, [], `${tag}: console errors, page errors, failed requests`);
  await context.close();
  return { tag, rows: jumped.rows, reqs: reqs.length };
}

async function noRepoSession(browser, theme, galW) {
  const tag = `norepo ${theme}/gal${galW}`;
  const { context, page, fr, reqs, errors } = await newSession(browser, theme, galW, plainBase);
  await ctrlG(page, fr);
  await until(page, `${tag} the git page`, () => shown(fr));
  if (galW > 720) await fitOverlay(page, fr, galW);
  const st = await until(page, `${tag} empty state`, () => fr.evaluate(() => {
    const root = document.getElementById('gitpage'), e = document.getElementById('gpempty');
    return root.hasAttribute('data-norepo') && !e.hidden && e.getClientRects().length > 0 ? { regions: root.querySelector('.gp-regions').getClientRects().length, text: e.textContent } : null;
  }));
  assert.equal(st.regions, 0, `${tag}: the regions hide`);
  assert.match(st.text, /Not a git repository/, `${tag}: the empty state's text`);
  await page.waitForTimeout(400);
  assert.equal(reqs.filter(r => !r.startsWith('git/status')).length, 0, `${tag}: no graph, pool or ledger read ${JSON.stringify(reqs)}`);
  await noOverflow(fr, tag);
  await shot(fr, `gp-${theme}-norepo`);
  assert.deepEqual(errors, [], `${tag}: console errors, page errors, failed requests`);
  await context.close();
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const report = [];
  try {
    for (const theme of ['dark', 'light']) {
      for (const galW of [900, 380]) report.push(await gitSession(browser, theme, galW, bases[report.length]));
      await noRepoSession(browser, theme, 900);
    }
    console.log(JSON.stringify({ frontier, sessions: report }));
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e && e.stack || e); process.exit(1); });
