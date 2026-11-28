// Run by TestTicketMapDockBrowser: the Ticket Map docked in the gallery's
// ticketmap system page (#tmdock) on a real framed page over the synthetic
// board, at 2560x1440 with the gallery column at 540px and at 380px, in both
// themes: the rail order, the two-row toolbar with no horizontal overflow,
// the bottom-sheet inspector (closed on load, opened by a View B card or a
// View C star, grip collapse, Esc), the container-scoped 1 / 2 / / / Esc
// keys that never reach #ctext, #composer or #cvox, and the one root shared
// with the expanded #tmback (⤢, the sheet's ⤢, Ctrl+B). With a shots
// directory it keeps the docked states beside the approved mockup's
// presets (mock-dock-*) for the side-by-side check.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, graphFile, epicStem, shots, mockup] = process.argv.slice(2);

const G = JSON.parse(fs.readFileSync(graphFile, 'utf8'));
const epic = G.nodes.find(n => n.stem === epicStem);
assert.ok(epic, 'the fixture plants the epic');

function shot(page, name) {
  if (!shots) return Promise.resolve();
  fs.mkdirSync(shots, { recursive: true });
  return page.screenshot({ path: path.join(shots, name + '.png') });
}
async function frameOf(page) {
  const h = await page.locator('iframe.sframe.on').elementHandle();
  return h.contentFrame();
}
const raf2 = fr => fr.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
const dockShown = fr => fr.evaluate(() => { const r = document.querySelector('#tmdock > .d-tm-root'); return !!r && r.getClientRects().length > 0; });
const st = fr => fr.evaluate(() => ({ view: window.__tmap.view, sel: window.__tmap.sel, q: document.getElementById('tmq').value }));
const sheet = fr => fr.evaluate(() => {
  const i = document.getElementById('tminsp'), root = document.querySelector('.d-tm-root');
  const r = i.getBoundingClientRect(), rr = root.getBoundingClientRect();
  return { shown: !i.hidden && r.height > 0, min: i.classList.contains('d-tm-sheet-min'), h: r.height, rootH: rr.height,
    bottom: Math.abs(r.bottom - rr.bottom), stem: (i.querySelector('.d-tm-istem') || {}).textContent || '',
    grip: document.getElementById('tmgrip').getAttribute('aria-expanded') };
});
const active = fr => fr.evaluate(() => document.activeElement && (document.activeElement.id || document.activeElement.tagName));
const modalOpen = fr => fr.evaluate(() => { const b = document.getElementById('tmback'); return !!b && !b.hidden; });

// overflow measures the docked pane: the root, both toolbar rows and the
// matrix scroll nothing sideways, and no shown toolbar part leaves the pane.
function overflow(fr) {
  return fr.evaluate(() => {
    const root = document.querySelector('#tmdock > .d-tm-root'), rr = root.getBoundingClientRect();
    const sx = e => e ? e.scrollWidth - e.clientWidth : 0;
    const out = [...root.querySelectorAll('.d-tm-r1 > *, .d-tm-r2 > *, .d-tm-r1 > * > *, .d-tm-r2 > * > *')].filter(e => e.getClientRects().length)
      .map(e => ({ e, r: e.getBoundingClientRect() })).filter(o => o.r.width && (o.r.right > rr.right + 0.5 || o.r.left < rr.left - 0.5))
      .map(o => (o.e.id || o.e.className) + ' ' + Math.round(o.r.left) + '..' + Math.round(o.r.right));
    const d = document.documentElement;
    return { root: sx(root), r1: sx(root.querySelector('.d-tm-r1')), r2: sx(root.querySelector('.d-tm-r2')), vb: sx(document.getElementById('tmvb')),
      doc: d.scrollWidth - d.clientWidth, out };
  });
}
async function noOverflow(fr, label) {
  await raf2(fr);
  const o = await overflow(fr);
  // Name what crosses View B's right edge when it scrolls sideways.
  const wide = o.vb ? await fr.evaluate(() => {
    const vb = document.getElementById('tmvb'), R = vb.getBoundingClientRect().right;
    return [...vb.querySelectorAll('*')].map(e => ({ e, r: e.getBoundingClientRect() })).filter(x => x.r.width && x.r.right > R + 0.5)
      .slice(0, 6).map(x => (x.e.id || x.e.getAttribute('class') || x.e.tagName) + ' ' + Math.round(x.r.left) + '..' + Math.round(x.r.right) + '/' + Math.round(R));
  }) : [];
  assert.deepEqual(o, { root: 0, r1: 0, r2: 0, vb: 0, doc: 0, out: [] }, `${label}: no horizontal overflow ${JSON.stringify(wide)}`);
}

async function dockSession(browser, theme, galW) {
  // 2560px is a 4-column width (the Ticket Map docks by default there);
  // 1920px with the rail open keeps the 3-column narrow layout.
  const W = 2560, H = 1440, tag = `${theme}/${W}x${H}/gal${galW}`;
  const context = await browser.newContext({ viewport: { width: W, height: H }, colorScheme: theme });
  await context.addCookies([{ name: 'ws_dashboard_theme', value: theme, url: base }]);
  await context.addInitScript(w => {
    if (self === top) {
      const workers = navigator.serviceWorker;
      if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
    } else {
      // The map's test hooks install when it first builds, on page load.
      const mark = () => { if (!document.documentElement) return false; document.documentElement.setAttribute('data-test-hooks', ''); return true; };
      if (!mark()) new MutationObserver((_, o) => { if (mark()) o.disconnect(); }).observe(document, { childList: true });
    }
    // A sandboxed frame (a preview) has no storage.
    try {
      if (sessionStorage.getItem('tmdock')) return;
      sessionStorage.setItem('tmdock', '1');
      localStorage.setItem('ws.dashboard.bgal.w', String(w));
      localStorage.setItem('ws.ticketmap.view', 'B');
    } catch (_) {}
  }, galW);
  await context.route('**/*', route => {
    const url = new URL(route.request().url());
    if (url.origin !== base) return route.abort();
    return route.continue();
  });
  const page = await context.newPage();
  const tracker = u => new URL(u).pathname === '/tracker/stores';
  const errors = [];
  page.on('pageerror', e => errors.push('pageerror: ' + e.message));
  page.on('console', m => { if (m.type() === 'error' && !(m.location().url && tracker(m.location().url))) errors.push('console: ' + m.text()); });
  page.on('requestfailed', r => { if (new URL(r.url()).origin === base) errors.push('requestfailed: ' + r.url() + ' ' + (r.failure() || {}).errorText); });
  page.on('response', r => { if (r.status() >= 400 && !(r.status() === 404 && tracker(r.url()))) errors.push(`response ${r.status()}: ${r.url()}`); });
  let graphReqs = 0;
  page.on('request', r => { if (new URL(r.url()).pathname.endsWith('/tickets/graph')) graphReqs++; });

  await page.goto(base + '/s/livesession');
  const fr = await frameOf(page);
  await fr.locator('#tmdock').waitFor({ state: 'attached' });
  // A wide idle view shows the docked Ticket Map by default, unprompted.
  let shownByDefault = false;
  for (let i = 0; i < 40 && !shownByDefault; i++) {
    shownByDefault = await dockShown(fr);
    if (!shownByDefault) await page.waitForTimeout(100);
  }
  assert.equal(shownByDefault, true, `${tag}: the wide idle view shows the docked Ticket Map by default`);
  await fr.locator('#tmdock #tmvb .d-tm-card').first().waitFor({ state: 'visible' });
  await raf2(fr);
  assert.equal(await fr.evaluate(() => !!window.__tmap), true, `${tag}: the map's test hooks`);

  // 1. The rail keeps + · workpanel · ticketmap · git, the Ticket Map page
  // active and hosting the one docked root; one request.
  const rail = await fr.evaluate(() => [...document.querySelectorAll('#bgal .gal-dot')].map(b => (b.dataset.sys || b.dataset.count || b.textContent) + (b.classList.contains('active') ? '*' : '')));
  assert.deepEqual(rail.slice(-4), ['+', 'workpanel', 'ticketmap*', 'git'], `${tag}: rail order ${JSON.stringify(rail)}`);
  assert.equal(await fr.evaluate(() => {
    const pg = document.querySelector('#bgal .gal-sys-page[data-page="ticketmap"]');
    return !!pg && pg.hasAttribute('data-active') && document.querySelectorAll('.d-tm-root').length === 1 && !!pg.querySelector('#tmdock > .d-tm-root.d-tm-docked');
  }), true, `${tag}: the Ticket Map page hosts the one docked root`);
  assert.equal(graphReqs, 1, `${tag}: the docked page sends one request`);

  // 2. The two-row toolbar: row 1 views, search, clusters, refresh and ⤢;
  // row 2 the pills and the scope badge; the expanded-only parts hide.
  const bar = await fr.evaluate(() => {
    const R = s => { const e = document.querySelector(s); return e && e.getClientRects().length ? e.getBoundingClientRect() : null; };
    const r1 = R('.d-tm-r1'), r2 = R('.d-tm-r2'), pane = R('#tmdock'), col = R('#bgal'), dots = R('#bgal > .gal-rail');
    return { r1: r1 && Math.round(r1.height), r2: r2 && Math.round(r2.height), below: !!(r1 && r2 && r2.top >= r1.bottom - 0.5),
      pane: pane && Math.round(pane.width), col: col && Math.round(col.width), room: col && dots && Math.round(col.right - dots.right), exp: !!R('#tmexpand'), close: !!R('#tmclose'), scope: !!R('.d-tm-scope'), finsp: !!R('#tmfinsp'), brand: !!R('.d-tm-brand') };
  });
  assert.deepEqual([bar.r1, bar.r2, bar.below], [40, 30, true], `${tag}: two toolbar rows ${JSON.stringify(bar)}`);
  assert.deepEqual([bar.exp, bar.scope, bar.close, bar.brand], [true, true, false, false], `${tag}: docked toolbar parts ${JSON.stringify(bar)}`);
  assert.ok(Math.abs(bar.col - galW) <= 2, `${tag}: the gallery column is ${galW}px ${JSON.stringify(bar)}`);
  assert.ok(bar.room > 0 && bar.pane >= bar.room - 16 && bar.pane <= bar.room, `${tag}: the pane fills the column beside the dot rail ${JSON.stringify(bar)}`);
  await noOverflow(fr, `${tag} matrix`);

  // 3. The sheet starts closed; a View B card opens it at about 42%.
  let sh = await sheet(fr);
  assert.equal(sh.shown, false, `${tag}: the sheet starts closed`);
  await shot(page, `dock-b-${theme}-gal${galW}`);
  await fr.locator(`#tmdock #tmvb .d-tm-card[title="${epicStem}"]`).click();
  await raf2(fr);
  sh = await sheet(fr);
  assert.equal(sh.shown, true, `${tag}: a card click opens the sheet`);
  assert.equal(sh.stem, epicStem, `${tag}: the sheet names the selection`);
  assert.equal(sh.grip, 'true');
  assert.ok(sh.bottom < 1, `${tag}: the sheet sits on the pane's bottom edge`);
  assert.ok(sh.h / sh.rootH > 0.3 && sh.h / sh.rootH < 0.56, `${tag}: sheet height ${sh.h} of ${sh.rootH}`);
  assert.equal(await fr.evaluate(() => !!document.getElementById('tmhero')), true, `${tag}: View B focuses the card`);
  assert.equal(await fr.locator('#tminsp .d-tm-ihd .d-tm-mention').count(), 1, `${tag}: the sheet can mention`);
  assert.equal(await fr.locator('#tminsp .d-tm-iacts .d-tm-btn').first().isVisible(), true, `${tag}: the sheet has Full View`);
  await noOverflow(fr, `${tag} focus + sheet`);
  await shot(page, `dock-sheet-${theme}-gal${galW}`);
  await fr.locator('#tmgrip').click();
  sh = await sheet(fr);
  assert.deepEqual([sh.min, Math.round(sh.h), sh.grip], [true, 32, 'false'], `${tag}: the grip collapses the sheet`);
  await fr.locator('#tmgrip').click();
  sh = await sheet(fr);
  assert.deepEqual([sh.min, sh.h > 100, sh.grip], [false, true, 'true'], `${tag}: the grip restores the sheet`);

  // 4. Esc inside the pane: the sheet, then the focus, then nothing.
  await fr.locator('#tmgrip').focus();
  await page.keyboard.press('Escape');
  sh = await sheet(fr);
  assert.equal(sh.shown, false, `${tag}: Esc closes the sheet first`);
  assert.equal(await fr.evaluate(() => !!document.getElementById('tmhero')), true, `${tag}: the focus stays`);
  assert.equal(await fr.evaluate(() => document.getElementById('tmdock').contains(document.activeElement)), true, `${tag}: the pane keeps the keyboard focus`);
  await page.keyboard.press('Escape');
  assert.equal(await fr.evaluate(() => !!document.querySelector('#tmvb .d-tm-mx')), true, `${tag}: Esc returns to the matrix`);
  await page.keyboard.press('Escape');
  assert.equal(await dockShown(fr), true, `${tag}: the docked map never closes`);

  // 5. Scoped keys: 1 / 2 / '/' act inside the pane only.
  await fr.focus('#tmdock');
  assert.equal(await fr.evaluate(() => document.getElementById('tmdock').hasAttribute('data-focus')), true, `${tag}: the pane marks its focus`);
  await page.keyboard.press('2');
  assert.equal((await st(fr)).view, 'C', `${tag}: 2 shows View C`);
  await page.keyboard.press('1');
  assert.equal((await st(fr)).view, 'B', `${tag}: 1 shows View B`);
  await page.keyboard.press('/');
  assert.equal(await active(fr), 'tmq', `${tag}: '/' focuses the search`);
  await page.keyboard.type('orchard');
  await page.waitForTimeout(80);
  assert.equal((await st(fr)).q, 'orchard', `${tag}: typing in the search`);
  await page.keyboard.press('Escape');
  assert.equal((await st(fr)).q, '', `${tag}: Esc clears the query`);
  await fr.locator(`#tmdock #tmvb .d-tm-card[title="${epicStem}"]`).click();
  await raf2(fr);
  assert.equal((await sheet(fr)).shown, true);
  const before = await st(fr);
  await fr.evaluate(() => { const f = document.getElementById('ctext'); f.value = ''; f.focus(); });
  assert.equal(await fr.evaluate(() => document.getElementById('tmdock').hasAttribute('data-focus')), false, `${tag}: focus outside clears the mark`);
  await page.keyboard.type('12/');
  await page.keyboard.press('Escape');
  assert.equal(await fr.evaluate(() => document.getElementById('ctext').value), '12/', `${tag}: #ctext keeps its keys`);
  assert.deepEqual(await st(fr), before, `${tag}: keys in #ctext never reach the map`);
  assert.equal((await sheet(fr)).shown, true, `${tag}: Esc in #ctext leaves the sheet`);
  const others = await fr.evaluate(() => {
    const out = {};
    for (const id of ['composer', 'cvox']) {
      const e = document.getElementById(id);
      if (!e) { out[id] = 'absent'; continue; }
      for (const key of ['1', '2', '/', 'Escape']) e.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }));
      out[id] = window.__tmap.view + ':' + window.__tmap.sel + ':' + !document.getElementById('tminsp').hidden;
    }
    return out;
  });
  for (const id of ['composer', 'cvox']) {
    if (others[id] !== 'absent') assert.equal(others[id], `B:${before.sel}:true`, `${tag}: keys on #${id} never reach the map`);
  }
  await fr.evaluate(() => { document.getElementById('ctext').value = ''; });
  // A bare / outside the pane and outside a text field is the page's own:
  // the search palette opened for it closes again.
  if (await fr.evaluate(() => { const p = document.getElementById('tkqpop'); return !!p && !p.hidden; })) {
    await fr.focus('#tkqin');
    await page.keyboard.press('Escape');
    await fr.waitForFunction(() => document.getElementById('tkqpop').hidden);
  }

  // 6. A View C star click opens the sheet; Esc closes it before the
  // selection.
  await fr.focus('#tmdock');
  await page.keyboard.press('Escape');
  await page.keyboard.press('Escape');
  assert.equal((await st(fr)).sel, null, `${tag}: back to no selection`);
  await page.keyboard.press('2');
  await fr.waitForFunction(() => window.__tmapc && window.__tmapc.stats().W > 0 && document.querySelectorAll('#tmworld .d-tm-cnd').length > 0);
  await page.waitForTimeout(150);
  assert.equal((await sheet(fr)).shown, false, `${tag}: View C keeps the sheet closed`);
  const star = await fr.evaluate(() => {
    const vp = document.getElementById('tmvp').getBoundingClientRect();
    for (const el of document.querySelectorAll('#tmworld .d-tm-cnd')) {
      const r = el.getBoundingClientRect(), x = r.left + r.width / 2, y = r.top + r.height / 2;
      if (!(r.width > 0) || x < vp.left + 30 || x > vp.right - 30 || y < vp.top + 60 || y > vp.bottom - 30) continue;
      const hit = document.elementFromPoint(x, y), nd = hit && hit.closest && hit.closest('.d-tm-cnd');
      if (nd === el) return { i: +el.getAttribute('data-i'), x, y };
    }
    return { none: [...document.querySelectorAll('#tmworld .d-tm-cnd')].slice(0, 3).map(e => { const r = e.getBoundingClientRect(), h = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2); return [e.getAttribute('data-i'), Math.round(r.left), Math.round(r.top), Math.round(r.width), h && (h.id || h.getAttribute('class') || h.tagName)]; }), vp: [vp.left, vp.top, vp.width, vp.height] };
  });
  assert.ok(star && !star.none, `${tag}: a clickable star in the docked View C ${JSON.stringify(star)}`);
  const off = await page.locator('iframe.sframe.on').boundingBox();
  await page.mouse.click(off.x + star.x, off.y + star.y);
  await raf2(fr);
  sh = await sheet(fr);
  assert.equal(sh.shown, true, `${tag}: a star click opens the sheet`);
  assert.equal(sh.stem, G.nodes[star.i].stem, `${tag}: the sheet names the star`);
  await noOverflow(fr, `${tag} View C + sheet`);
  await shot(page, `dock-c-sheet-${theme}-gal${galW}`);
  await fr.focus('#tmdock');
  await page.keyboard.press('Escape');
  assert.deepEqual([(await sheet(fr)).shown, (await st(fr)).sel], [false, star.i], `${tag}: Esc closes the sheet, the selection stays`);

  // 7. ⤢ expands the one root with every state; Ctrl+B there closes it,
  // and Ctrl+B in the dock never opens it.
  // View B focuses the star; Esc returns to the matrix and its epic card.
  await page.keyboard.press('1');
  await page.keyboard.press('Escape');
  await fr.locator('#tmdock #tmvb .d-tm-mx').waitFor();
  await fr.locator(`#tmdock #tmvb .d-tm-card[title="${epicStem}"]`).click();
  await raf2(fr);
  await fr.focus('#tmdock');
  await page.keyboard.press('Control+b');
  await page.waitForTimeout(150);
  assert.equal(await modalOpen(fr), false, `${tag}: Ctrl+B in the dock is a no-op`);
  assert.equal(await dockShown(fr), true);
  await fr.locator('#tmexpand').click();
  await fr.waitForFunction(() => !document.getElementById('tmback').hidden);
  await raf2(fr);
  assert.deepEqual(await fr.evaluate(() => ({ docked: !!document.querySelector('#tmdock > .d-tm-root'), modal: !!document.querySelector('#tmback > .d-tm-root'),
    hero: (document.querySelector('#tmhero .d-tm-stem') || {}).textContent, view: window.__tmap.view, exp: document.getElementById('tmexpand').getAttribute('aria-expanded') })),
  { docked: false, modal: true, hero: epicStem, view: 'B', exp: 'true' }, `${tag}: ⤢ moves the root with its focus`);
  // The overlay opens on the search field; 2 acts from outside it.
  await fr.focus('#tmviewb');
  await page.keyboard.press('2');
  assert.equal((await st(fr)).view, 'C', `${tag}: the expanded map takes 2`);
  await page.keyboard.press('Control+b');
  await fr.waitForFunction(() => document.getElementById('tmback').hidden);
  await raf2(fr);
  assert.deepEqual(await fr.evaluate(() => ({ docked: !!document.querySelector('#tmdock > .d-tm-root'), view: window.__tmap.view, sel: window.__tmap.sel })),
    { docked: true, view: 'C', sel: epic.i }, `${tag}: Ctrl+B docks the root back with the expanded changes`);
  assert.equal((await sheet(fr)).shown, true, `${tag}: the sheet shows the shared selection`);
  assert.equal(await active(fr), 'tmviewb', `${tag}: the focus stays on its control, now docked`);
  await fr.locator('#tminspexp').click();
  await fr.waitForFunction(() => !document.getElementById('tmback').hidden);
  assert.equal((await st(fr)).sel, epic.i, `${tag}: the sheet's ⤢ expands the same selection`);
  await fr.locator('#tmclose').click();
  await fr.waitForFunction(() => document.getElementById('tmback').hidden);
  assert.equal(await dockShown(fr), true, `${tag}: closing docks the root back`);
  await page.keyboard.press('1');

  await page.waitForTimeout(200);
  assert.equal(graphReqs, 1, `${tag}: docking and expanding share the one request`);
  assert.deepEqual(errors, [], `${tag}: console errors, page errors, failed requests`);
  await context.close();
  return { tag, pane: bar.pane, sheetRatio: Math.round(100 * sh.h / sh.rootH) / 100 };
}

// The approved mockup's docked presets in both themes.
async function mockupShots(browser) {
  if (!mockup || !shots) return;
  const context = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const page = await context.newPage();
  for (const theme of ['dark', 'light']) {
    for (const preset of ['wide-tm', 'narrow-col']) {
      await page.goto(pathToFileURL(mockup).href);
      await page.waitForTimeout(300);
      if (await page.evaluate(() => document.documentElement.dataset.theme) !== theme) await page.locator('#m-theme-toggle').click();
      await page.locator(`.m-tab[data-preset="${preset}"]`).click();
      await page.waitForTimeout(300);
      await shot(page, `mock-dock-${preset}-${theme}`);
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
      for (const galW of [540, 380]) report.push(await dockSession(browser, theme, galW));
    }
    console.log(JSON.stringify({ nodes: G.nodes.length, epic: epicStem, sessions: report }));
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e && e.stack || e); process.exit(1); });
