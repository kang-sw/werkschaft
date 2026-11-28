// Run by TestWorkpanelBrowser: the gallery's Workpanel system page (Graybox
// B) on a real framed page at 1920x1080 with the gallery column at 720px
// (wide; the column's maximum) and 380px (narrow), in both themes, and an
// empty session: the panels region adopted into #wpdock and kept live by the
// keyed merge, the container-query layouts, no horizontal overflow, the
// Notes and Manuals tabs, the approval wait on the rail slot and the drawer
// handle, and the drawer slimmed to its worktree and holder cards. With a
// shots directory it keeps the captures beside the approved mockup's presets
// (mock-*) for the side-by-side check.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, updates, shots, mockup] = process.argv.slice(2);
for (const address of [base, updates]) {
  const u = new URL(address);
  assert(['127.0.0.1', 'localhost', '[::1]'].includes(u.hostname) && u.port && u.port !== '29417', 'disposable loopback only');
}

function shot(target, name) {
  if (!shots) return Promise.resolve();
  fs.mkdirSync(shots, { recursive: true });
  return target.screenshot({ path: path.join(shots, name + '.png') });
}
async function frameOf(page) {
  const h = await page.locator('iframe.sframe.on').elementHandle();
  return h.contentFrame();
}
const raf2 = fr => fr.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
async function op(name, value) {
  const r = await fetch(updates + '?' + new URLSearchParams({ op: name, value: value || '' }));
  assert.ok(r.status === 204 || r.status === 200, `fixture op ${name}: ${r.status}`);
  return r.status === 200 ? r.text() : '';
}
async function eventually(fn, label, ms = 8000) {
  const end = Date.now() + ms;
  for (;;) {
    const v = await fn();
    if (v) return v;
    if (Date.now() > end) throw new Error('timed out: ' + label);
    await new Promise(r => setTimeout(r, 50));
  }
}
const shown = (fr, sel) => fr.evaluate(s => { const e = document.querySelector(s); return !!e && e.getClientRects().length > 0 && getComputedStyle(e).visibility !== 'hidden'; }, sel);
const noteRows = fr => fr.evaluate(() => document.querySelectorAll('#wpnoteslist .wp-note-row').length);

// overflow: the Workpanel and its scrollers scroll nothing sideways, and no
// shown descendant leaves the root's box.
async function noOverflow(fr, label) {
  await raf2(fr);
  const o = await fr.evaluate(() => {
    const root = document.getElementById('wproot'), rr = root.getBoundingClientRect();
    const sx = e => e && e.getClientRects().length ? e.scrollWidth - e.clientWidth : 0;
    const out = [...root.querySelectorAll('*')].filter(e => e.getClientRects().length && !e.closest('details.panel'))
      .map(e => ({ e, r: e.getBoundingClientRect() })).filter(x => x.r.width && (x.r.right > rr.right + 0.5 || x.r.left < rr.left - 0.5))
      .slice(0, 6).map(x => (x.e.id || x.e.getAttribute('class') || x.e.tagName) + ' ' + Math.round(x.r.left) + '..' + Math.round(x.r.right) + '/' + Math.round(rr.right));
    return { root: sx(root), memory: sx(document.getElementById('wpmemory')), ref: sx(document.getElementById('wpref')),
      notes: sx(document.getElementById('wpnoteslist')), manuals: sx(document.getElementById('wpmanualslist')), out };
  });
  assert.deepEqual(o, { root: 0, memory: 0, ref: 0, notes: 0, manuals: 0, out: [] }, `${label}: no horizontal overflow`);
}

async function newSession(browser, theme, galW) {
  // The always-open gallery column (its stored width honoured) needs the
  // spare width a 3-column 1920 desktop lacks: 2560x1440, as the dock test.
  const context = await browser.newContext({ viewport: { width: 2560, height: 1440 }, colorScheme: theme, locale: 'en-US', reducedMotion: 'reduce' });
  await context.addCookies([{ name: 'ws_dashboard_theme', value: theme, url: base }, { name: 'ws_dashboard_lang', value: 'en', url: base }]);
  await context.addInitScript(w => {
    if (self === top) {
      const workers = navigator.serviceWorker;
      if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
    }
    try {
      if (sessionStorage.getItem('wpfixture')) return;
      sessionStorage.setItem('wpfixture', '1');
      localStorage.setItem('ws.dashboard.bgal.w', String(w));
    } catch (_) {}
  }, galW);
  await context.route('**/*', route => {
    const url = new URL(route.request().url());
    if (['http:', 'https:'].includes(url.protocol) && url.origin !== base) return route.abort();
    return route.continue();
  });
  const page = await context.newPage();
  // The fixture serves no tracker stores and no ticket graph (the docked
  // Ticket Map's own read): their 404s are not the Workpanel's.
  const tracker = u => { const p = new URL(u).pathname; return p === '/tracker/stores' || p.endsWith('/tickets/graph'); };
  const errors = [], reqs = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message));
  page.on('console', m => { if (m.type() === 'error' && !(m.location().url && tracker(m.location().url))) errors.push('console: ' + m.text()); });
  page.on('response', r => { if (r.status() >= 400 && !(r.status() === 404 && tracker(r.url()))) errors.push(`response ${r.status()}: ${r.url()}`); });
  page.on('request', r => { const p = new URL(r.url()).pathname; if (/\/(notes|manuals)$/.test(p)) reqs.push(r.method() + ' ' + p); });
  return { context, page, errors, reqs };
}

// openWorkpanel loads a session and brings the gallery's Workpanel page.
async function openWorkpanel(page, rid, tag) {
  await page.goto(base + '/s/' + rid);
  const fr = await frameOf(page);
  const dot = fr.locator('#bgal .gal-dot.sys[data-sys="workpanel"]');
  await dot.waitFor({ state: 'visible' });
  await dot.click();
  await fr.waitForFunction(() => {
    const root = document.getElementById('wproot'), dock = document.getElementById('wpdock');
    return !!root && !root.hidden && !!dock && dock.contains(root) && root.getClientRects().length > 0 &&
      document.querySelector('#bgal .gal-sys-page[data-page="workpanel"][data-active]') !== null;
  });
  await raf2(fr);
  const col = await fr.evaluate(() => Math.round(document.getElementById('bgal').getBoundingClientRect().width));
  return { fr, col };
}

async function liveSession(browser, theme, galW) {
  const tag = `${theme}/gal${galW}`, wide = galW >= 540;
  await op('reset');
  const { context, page, errors, reqs } = await newSession(browser, theme, galW);
  const { fr, col } = await openWorkpanel(page, 'livesession', tag);
  assert.ok(Math.abs(col - galW) <= 2, `${tag}: the gallery column is ${galW}px (${col})`);
  const rail = await fr.evaluate(() => [...document.querySelectorAll('#bgal .gal-dot')].map(b => (b.dataset.sys || b.dataset.count || b.textContent) + (b.classList.contains('active') ? '*' : '')));
  assert.deepEqual(rail.slice(-4), ['+', 'workpanel*', 'ticketmap', 'git'], `${tag}: rail order ${JSON.stringify(rail)}`);

  // 1. One panels region, adopted into the Workpanel; the drawer holds none.
  assert.deepEqual(await fr.evaluate(() => ({
    panels: document.querySelectorAll('#r-panels').length,
    inDock: document.getElementById('wpmemory').contains(document.getElementById('r-panels')),
    cards: [...document.querySelectorAll('#r-panels > details.panel')].map(p => p.id),
    open: [...document.querySelectorAll('#r-panels > details.panel')].every(p => p.open),
    drawer: [...document.querySelectorAll('#drbody details.panel')].map(p => p.id).sort(),
    notesCard: !!document.getElementById('p-notes'),
  })), { panels: 1, inDock: true, cards: ['p-todo', 'p-agenda'], open: true, drawer: [], notesCard: false }, `${tag}: panels in the Workpanel, the drawer slimmed`);
  // Todo's progress reads the chip: 1/4.
  const prog = () => fr.evaluate(() => {
    const fill = document.getElementById('wpprog'), bar = fill.closest('[role=progressbar]');
    return { count: document.querySelector('[data-wp-count="todo"]').textContent, now: bar.getAttribute('aria-valuenow'), transform: getComputedStyle(fill).transform };
  });
  assert.deepEqual(await prog(), { count: '1/4', now: '25', transform: 'matrix(0.25, 0, 0, 1, 0, 0)' }, `${tag}: todo progress`);

  // 2. The container-query layouts.
  const lay = () => fr.evaluate(() => {
    const R = id => { const e = document.getElementById(id); return e && e.getClientRects().length ? e.getBoundingClientRect() : null; };
    const t = R('p-todo'), a = R('p-agenda');
    return { seg: getComputedStyle(document.querySelector('.wp-mem-seg')).display !== 'none', todo: !!t, agenda: !!a, side: !!(t && a && a.left >= t.right - 0.5 && Math.abs(a.top - t.top) < 1) };
  });
  if (wide) {
    assert.deepEqual(await lay(), { seg: false, todo: true, agenda: true, side: true }, `${tag}: Todo and Agenda side by side`);
  } else {
    assert.deepEqual(await lay(), { seg: true, todo: true, agenda: false, side: false }, `${tag}: the segment shows Todo`);
    await fr.locator('.wp-seg-btn[data-mem-tab="agenda"]').click();
    assert.deepEqual(await lay(), { seg: true, todo: false, agenda: true, side: false }, `${tag}: the segment shows Agenda`);
    assert.equal(await fr.locator('.wp-seg-btn[data-mem-tab="agenda"]').getAttribute('aria-selected'), 'true');
    await fr.locator('.wp-seg-btn[data-mem-tab="todo"]').click();
    assert.equal((await lay()).todo, true, `${tag}: back to Todo`);
  }
  // A card's header is no toggle.
  await fr.locator('#p-todo > summary').click({ position: { x: 8, y: 8 } });
  assert.equal(await fr.evaluate(() => document.getElementById('p-todo').open), true, `${tag}: a header click keeps the card open`);

  // 3. Notes: read on first view, four layers, one muted record.
  await eventually(async () => (await noteRows(fr)) === 5, `${tag}: notes rows`);
  assert.equal(reqs.filter(r => r.endsWith('/notes')).length >= 1, true, `${tag}: notes read on view`);
  assert.equal(reqs.some(r => r.endsWith('/manuals')), false, `${tag}: manuals not read before the tab opens`);
  await noOverflow(fr, `${tag} notes`);
  await shot(fr.locator('#bgal'), `wp-${theme}-${wide ? 'wide' : 'narrow'}`);
  if (theme === 'dark' && wide) await shot(page, `wp-page-${theme}-wide`);
  await fr.locator('.wp-lchip[data-layer="repo"]').click();
  assert.equal(await noteRows(fr), 1, `${tag}: the repo chip keeps one`);
  assert.equal(await fr.locator('.wp-lchip[data-layer="repo"]').getAttribute('aria-pressed'), 'true');
  await fr.locator('.wp-lchip[data-layer="all"]').click();
  await fr.locator('#wpnotesq').fill('handoff');
  assert.equal(await noteRows(fr), 1, `${tag}: search narrows`);
  assert.equal(await fr.locator('[data-wp-count="notes"]').textContent(), '1/5');
  await fr.locator('[data-clear="wpnotesq"]').click();
  assert.equal(await noteRows(fr), 5, `${tag}: clearing restores`);
  await fr.locator('#wpnotesmuted').click();
  assert.equal(await noteRows(fr), 4, `${tag}: the muted toggle hides muted notes`);
  await fr.locator('#wpnotesmuted').click();
  assert.equal(await fr.locator('#wpnoteslist .wp-note-row.is-muted').count(), 1, `${tag}: the muted note marked`);
  const body = fr.locator('#wpnoteslist .wp-note-body').first();
  await body.click();
  assert.equal(await body.getAttribute('aria-expanded'), 'true', `${tag}: a note expands`);
  const before = +(await op('refreshes'));
  await fr.locator('#wpnotesrefresh').click();
  await eventually(() => reqs.includes('POST /s/livesession/notes'), `${tag}: refresh posts`);
  await eventually(async () => +(await op('refreshes')) > before, `${tag}: refresh forces a read`);
  await eventually(async () => (await noteRows(fr)) === 5 && !(await fr.locator('#wpnotesrefresh').isDisabled()), `${tag}: refreshed`);
  assert.equal(await fr.locator('#wpnoteslist .wp-note-body[aria-expanded="true"]').count(), 1, `${tag}: the expanded note stays expanded`);

  // 4. A LAN viewer's repo-only answer (routed): the hint, local layers gone.
  await page.route('**/s/livesession/notes*', route => route.fulfill({ status: 200, contentType: 'application/json',
    body: JSON.stringify({ layers: [{ layer: 'repo', notes: [{ key: 'handoff', value: 'public repo handoff', priority: 1, written_at: '', visible: true }] }], loading: false, local_only: true }) }));
  await fr.locator('#wpnotesrefresh').click();
  await eventually(async () => (await noteRows(fr)) === 1, `${tag}: repo-only rows`);
  assert.deepEqual(await fr.evaluate(() => ({
    hint: !!document.querySelector('#wpnoteslist .wp-hint'),
    chips: [...document.querySelectorAll('.wp-lchip')].filter(b => !b.hidden).map(b => b.dataset.layer),
  })), { hint: true, chips: ['all', 'repo'] }, `${tag}: LAN repo-only`);
  await page.unroute('**/s/livesession/notes*');
  await fr.locator('#wpnotesrefresh').click();
  await eventually(async () => (await noteRows(fr)) === 5, `${tag}: local again`);

  // 5. Manuals: read once when the tab opens; a row opens the file preview.
  await fr.evaluate(() => {
    window.__wpOpened = [];
    window.WsPreview = window.WsPreview || {};
    window.WsPreview.open = (p, o) => { window.__wpOpened.push([p, !!(o && o.keep)]); };
  });
  await fr.locator('#wptab-manuals').click();
  await fr.locator('#wpmanualslist .wp-manual-btn').nth(1).waitFor();
  assert.equal(await fr.locator('#wpmanualslist .wp-manual-btn').count(), 2, `${tag}: two manuals`);
  assert.equal(await shown(fr, '#wppanel-notes'), false, `${tag}: the notes pane hides`);
  await noOverflow(fr, `${tag} manuals`);
  await shot(fr.locator('#bgal'), `wp-${theme}-${wide ? 'wide' : 'narrow'}-manuals`);
  await fr.locator('#wpmanualsq').fill('perf');
  assert.equal(await fr.locator('#wpmanualslist .wp-manual-btn').count(), 1, `${tag}: manuals search`);
  await fr.locator('#wpmanualslist .wp-manual-btn').click();
  assert.deepEqual(await fr.evaluate(() => window.__wpOpened), [['ai-docs/manuals/performance.md', true]], `${tag}: preview opened`);
  await fr.locator('[data-clear="wpmanualsq"]').click();
  await fr.locator('#wptab-notes').click();
  await fr.locator('#wptab-manuals').click();
  assert.equal(reqs.filter(r => r.endsWith('/manuals')).length, 1, `${tag}: manuals read once`);
  await fr.locator('#wptab-notes').click();

  // 6. The keyed merge keeps the Workpanel live without moving the region.
  const region = await fr.evaluateHandle(() => document.getElementById('r-panels'));
  await op('todo', 'live');
  await fr.waitForFunction(() => document.getElementById('p-todo').textContent.includes('live step 00'));
  await raf2(fr);
  assert.deepEqual(await prog(), { count: '2/4', now: '50', transform: 'matrix(0.5, 0, 0, 1, 0, 0)' }, `${tag}: live todo progress`);
  assert.equal(await fr.evaluate(r => r.isConnected && document.getElementById('wpmemory').contains(r), region), true, `${tag}: the region stays in place`);
  await op('agenda', 'review running');
  await fr.waitForFunction(() => document.getElementById('p-agenda').textContent.includes('review running'));
  assert.equal(await fr.evaluate(() => [...document.querySelectorAll('#r-panels > details.panel')].every(p => p.open)), true, `${tag}: cards stay open after a swap`);

  // 7. The approval wait lights the rail slot (the drawer's panels handle,
  // which shared it, retired with the drawer cards).
  const wait = () => fr.evaluate(() => {
    const slot = document.querySelector('#bgal .gal-dot.sys[data-sys="workpanel"]');
    return { slot: slot.classList.contains('wait'), label: /; /.test(slot.getAttribute('aria-label') || '') };
  });
  assert.deepEqual(await wait(), { slot: false, label: false }, `${tag}: no wait at first`);
  await op('permission');
  await eventually(async () => (await wait()).slot, `${tag}: wait on the slot`);
  assert.deepEqual(await wait(), { slot: true, label: true }, `${tag}: wait on the slot`);
  await shot(fr.locator('#bgal > .gal-rail'), `wp-${theme}-${wide ? 'wide' : 'narrow'}-wait-rail`);
  await op('clear');
  await eventually(async () => !(await wait()).slot, `${tag}: wait cleared`);
  assert.deepEqual(await wait(), { slot: false, label: false }, `${tag}: cleared everywhere`);

  // 8. The drawer carries no cards: only the search sheet and its handle.
  assert.deepEqual(await fr.evaluate(() => ({ drhandle: !!document.getElementById('drhandle'), drbody: !!document.getElementById('drbody'), holders: !!document.getElementById('p-holders') })),
    { drhandle: false, drbody: false, holders: false }, `${tag}: the drawer's cards retired`);
  assert.equal(await fr.locator('#shandle').count(), 1, `${tag}: the search handle stays`);
  assert.equal(await shown(fr, '#wproot'), true, `${tag}: the Workpanel stays shown`);

  await page.waitForTimeout(150);
  assert.deepEqual(errors, [], `${tag}: console errors, page errors, failed requests`);
  await context.close();
  return { tag, col };
}

async function emptySession(browser, theme) {
  const tag = `${theme}/empty`;
  const { context, page, errors } = await newSession(browser, theme, 720);
  await context.route('**/s/boardsession/notes*', route => route.fulfill({ status: 200, contentType: 'application/json', body: '{"layers":[],"loading":false,"local_only":false}' }));
  await context.route('**/s/boardsession/manuals', route => route.fulfill({ status: 200, contentType: 'application/json', body: '{"manuals":[]}' }));
  const { fr } = await openWorkpanel(page, 'boardsession', tag);
  await fr.locator('#wpnoteslist .wp-empty').waitFor();
  assert.deepEqual(await fr.evaluate(() => ({
    cards: document.querySelectorAll('#r-panels > details.panel').length,
    empty: [...document.querySelectorAll('.wp-mem-empty')].filter(e => !e.hidden && e.getClientRects().length).map(e => e.dataset.for).sort(),
    tools: [...document.querySelectorAll('.wp-mem-tools')].filter(e => !e.hidden).length,
  })), { cards: 0, empty: ['agenda', 'todo'], tools: 0 }, `${tag}: the empty memory`);
  await noOverflow(fr, tag);
  await shot(fr.locator('#bgal'), `wp-${theme}-empty`);
  await fr.locator('#wptab-manuals').click();
  await fr.locator('#wpmanualslist .wp-empty').waitFor();
  await page.waitForTimeout(150);
  assert.deepEqual(errors, [], `${tag}: console errors, page errors, failed requests`);
  await context.close();
  return { tag };
}

// The approved mockup's presets in both themes.
async function mockupShots(browser) {
  if (!mockup || !shots) return;
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage();
  await page.goto(pathToFileURL(mockup).href);
  for (const theme of ['dark', 'light']) {
    await page.locator(`[data-theme-btn="${theme}"]`).click();
    for (const view of ['wide', 'narrow', 'empty']) {
      await page.locator(`[data-view-btn="${view}"]`).click();
      await page.waitForTimeout(200);
      await shot(page, `mock-${view}-${theme}`);
    }
  }
  await context.close();
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const report = [];
  try {
    await mockupShots(browser);
    for (const theme of ['dark', 'light']) {
      for (const galW of [720, 380]) report.push(await liveSession(browser, theme, galW));
      report.push(await emptySession(browser, theme));
    }
    console.log(JSON.stringify({ sessions: report }));
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e && e.stack || e); process.exit(1); });
