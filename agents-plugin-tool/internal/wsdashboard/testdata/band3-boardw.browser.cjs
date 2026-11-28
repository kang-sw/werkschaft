// Run through TestBand3BoardWidthDoesNotLeakBrowser against an isolated fixture daemon.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, shots] = process.argv.slice(2);
if (shots) fs.mkdirSync(shots, { recursive: true });

const KEY = 'ws.dashboard.cards.livesession';

async function openLive(browser, { width, height, colorScheme, record }) {
  const context = await browser.newContext({ viewport: { width, height }, colorScheme, locale: 'en-US', reducedMotion: 'reduce' });
  await context.addCookies([{ name: 'ws_dashboard_theme', value: colorScheme, url: base }]);
  await context.addInitScript(({ key, value }) => {
    try {
      if (localStorage.getItem('band3-seeded')) return;
      localStorage.setItem(key, JSON.stringify(value));
      localStorage.setItem('band3-seeded', '1');
      localStorage.removeItem('ws.dashboard.layout');
    } catch (e) { /* opaque origin */ }
  }, { key: KEY, value: record });
  await context.route('**/*', route => {
    const url = new URL(route.request().url());
    if (url.origin !== base) return route.abort();
    if (url.pathname === '/rail/usage') return route.fulfill({ json: { entries: [], warnPercent: 80, critPercent: 95 } });
    if (url.pathname === '/tracker/stores') return route.fulfill({ json: { rev: 1, stores: [] } });
    // The docked Ticket Map (the wide gallery's default page) reads the
    // graph on load; this fixture has no ticket tree (its 404 logs an error).
    if (url.pathname.endsWith('/tickets/graph')) return route.fulfill({ json: { nodes: [] } });
    return route.continue();
  });
  const tab = await context.newPage();
  const pageErrors = [];
  const consoleErrors = [];
  const failedReqs = [];
  tab.on('pageerror', e => pageErrors.push(e.message));
  tab.on('response', r => { if (r.status() >= 400) failedReqs.push(`${r.status()} ${r.url()}`); });
  tab.on('console', m => { if (m.type() === 'error') consoleErrors.push(m.text()); });
  await tab.goto(base + '/s/livesession');
  const host = tab.locator('iframe.sframe.on');
  await host.waitFor({ state: 'visible' });
  const page = await (await host.elementHandle()).contentFrame();
  await page.locator('.dgrid.live > .dcv-edge').waitFor({ state: 'attached' });
  await page.waitForFunction(() => window.WsCards && window.WsCards.gallery && window.WsCards.gallery());
  await page.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
  return { context, tab, page, pageErrors, consoleErrors, failedReqs };
}

async function measure(page) {
  return page.evaluate(() => {
    const r = document.documentElement;
    const grid = document.querySelector('.dgrid.live');
    const convo = document.getElementById('convo');
    const bcol = document.getElementById('bcol');
    const rail = document.querySelector('.dbody > .rail');
    const bgal = document.getElementById('bgal');
    const edge = grid.querySelector('.dcv-edge');
    const gb = grid.getBoundingClientRect();
    const cb = convo.getBoundingClientRect();
    const bb = bcol.getBoundingClientRect();
    const rb = rail.getBoundingClientRect();
    const eb = edge.getBoundingClientRect();
    const dbody = document.querySelector('.dbody').getBoundingClientRect();
    const galb = bgal.hidden ? null : bgal.getBoundingClientRect();
    return {
      vw: innerWidth,
      gal: r.getAttribute('data-gal'),
      pincol: r.hasAttribute('data-pincol'),
      hasBoardw: r.hasAttribute('data-boardw'),
      boardwVar: r.style.getPropertyValue('--boardw'),
      hasConvow: r.hasAttribute('data-convow'),
      convowVar: r.style.getPropertyValue('--convow'),
      gridW: Math.round(gb.width),
      gridLeft: Math.round(gb.left),
      gridRight: Math.round(gb.right),
      convoW: Math.round(cb.width),
      boardW: Math.round(bb.width),
      railRight: Math.round(rb.right),
      dbodyRight: Math.round(dbody.right),
      edgeCenter: { x: eb.left + eb.width / 2, y: eb.top + eb.height / 2 },
      galW: galb ? Math.round(galb.width) : 0,
      rec: JSON.parse(localStorage.getItem('ws.dashboard.cards.livesession') || '{}'),
    };
  });
}

async function dragSplit(tab, page, dx) {
  const frameBox = await tab.locator('iframe.sframe.on').boundingBox();
  const m = await measure(page);
  const startX = frameBox.x + m.edgeCenter.x;
  const startY = frameBox.y + m.edgeCenter.y;
  await tab.mouse.move(startX, startY);
  await tab.mouse.down();
  await tab.mouse.move(startX + dx, startY, { steps: 8 });
  await tab.mouse.up();
  await page.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    // 1. 1660x960 (Band 3: narrow paged gallery, no 4th column) with legacy
    //    persisted bb=500, cw=380: legacy bb is ignored (no data-boardw/--boardw),
    //    cw=380 applies to convo, and .dcv-edge shifts width between convo and
    //    board while keeping the stage steady.
    for (const colorScheme of ['dark', 'light']) {
      const { context, tab, page, pageErrors, consoleErrors, failedReqs } = await openLive(browser, {
        width: 1660,
        height: 960,
        colorScheme,
        record: { cards: [], topics: [], bb: 500, cw: 380 },
      });
      const before = await measure(page);
      assert.equal(before.gal, 'narrow', `${colorScheme} 1660: Band 3 uses narrow gallery (4th column gated by spare width)`);
      assert.equal(before.pincol, false, `${colorScheme} 1660: data-pincol unset in Band 3`);
      assert.equal(before.hasBoardw, false, `${colorScheme} 1660: legacy bb does not write data-boardw`);
      assert.equal(before.boardwVar, '', `${colorScheme} 1660: legacy bb does not write --boardw`);
      assert(Math.abs(before.convoW - 380) <= 2, `${colorScheme} 1660: persisted cw=380 applies to convo, got ${before.convoW}`);
      assert(Math.abs(before.boardW - (before.gridW - 8 - before.convoW)) <= 2, `${colorScheme} 1660: board takes remaining stage width, got ${before.boardW}`);
      if (shots) await tab.screenshot({ path: path.join(shots, `band3-1660-${colorScheme}.png`) });

      if (colorScheme === 'dark') {
        await dragSplit(tab, page, 50);
        const after = await measure(page);
        assert(Math.abs(after.gridW - before.gridW) <= 2, `1660 drag keeps total .dgrid.live width constant (${before.gridW} -> ${after.gridW})`);
        assert(Math.abs((after.convoW - before.convoW) - 50) <= 2, `1660 drag widens convo by +50px (${before.convoW} -> ${after.convoW})`);
        assert(Math.abs((before.boardW - after.boardW) - 50) <= 2, `1660 drag narrows board by equal-and-opposite -50px (${before.boardW} -> ${after.boardW})`);
        assert.equal(after.hasBoardw, false, '1660 drag never sets data-boardw');
        assert.equal(after.rec.bb, undefined, `1660 drag does not persist legacy bb, got ${after.rec.bb}`);
      }
      assert.deepEqual(pageErrors, [], `${colorScheme} 1660 pageErrors`);
      assert.deepEqual(consoleErrors, [], `${colorScheme} 1660 consoleErrors (${failedReqs.join(', ')})`);
      await context.close();
    }

    // 2. 1920x960 (Band 3) with legacy bb=600, cw=0:
    //    Stage keeps 5fr/7fr split and ignores legacy bb; dragging .dcv-edge
    //    shifts width between convo and board. Widening to 2560x960 crosses
    //    into Band 4 (data-gal="col", data-pincol) with #bgal at 540px.
    {
      const { context, tab, page, pageErrors, consoleErrors, failedReqs } = await openLive(browser, {
        width: 1920,
        height: 960,
        colorScheme: 'dark',
        record: { cards: [], topics: [], bb: 600, cw: 0 },
      });
      const m1920 = await measure(page);
      assert.equal(m1920.gal, 'narrow', '1920: Band 3 uses narrow gallery');
      assert.equal(m1920.pincol, false, '1920: data-pincol unset in Band 3');
      assert.equal(m1920.hasBoardw, false, '1920: legacy bb ignored');
      assert.equal(m1920.hasConvow, false, '1920: cw=0 leaves data-convow unset');
      const wantConvo5fr = Math.round((m1920.gridW - 8) * 5 / 12);
      const wantBoard7fr = Math.round((m1920.gridW - 8) * 7 / 12);
      assert(Math.abs(m1920.convoW - wantConvo5fr) <= 2 && Math.abs(m1920.boardW - wantBoard7fr) <= 2,
        `1920: default 5fr/7fr preserved (got ${m1920.convoW}/${m1920.boardW}, want ${wantConvo5fr}/${wantBoard7fr})`);
      if (shots) await tab.screenshot({ path: path.join(shots, 'band3-1920-dark.png') });

      await dragSplit(tab, page, 100);
      const dragged = await measure(page);
      assert(Math.abs(dragged.gridW - m1920.gridW) <= 2, `1920 drag keeps .dgrid.live constant (${m1920.gridW} -> ${dragged.gridW})`);
      assert(Math.abs((dragged.convoW - m1920.convoW) - 100) <= 2, `1920 drag widens convo by +100px (${m1920.convoW} -> ${dragged.convoW})`);
      assert(Math.abs((m1920.boardW - dragged.boardW) - 100) <= 2, `1920 drag narrows board by -100px (${m1920.boardW} -> ${dragged.boardW})`);

      const fb = await tab.locator('iframe.sframe.on').boundingBox();
      await tab.mouse.dblclick(fb.x + dragged.edgeCenter.x, fb.y + dragged.edgeCenter.y);
      await page.waitForFunction(() => !document.documentElement.hasAttribute('data-convow'));

      await tab.setViewportSize({ width: 2560, height: 960 });
      await page.waitForFunction(() => document.documentElement.getAttribute('data-gal') === 'col');
      await page.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
      const m2560 = await measure(page);
      assert.equal(m2560.gal, 'col', '2560: crosses into Band 4');
      assert.equal(m2560.pincol, true, '2560: data-pincol set in Band 4');
      assert.equal(m2560.hasBoardw, false, '2560: data-boardw stays unset');
      assert(Math.abs(m2560.galW - 540) <= 2, `2560: #bgal keeps default 540px (${m2560.galW}px)`);
      assert(m2560.boardW > m1920.boardW, `2560: flexible board expands with viewport (${m1920.boardW} -> ${m2560.boardW})`);
      if (shots) await tab.screenshot({ path: path.join(shots, 'band4-2560-dark.png') });

      assert.deepEqual(pageErrors, [], '1920->2560 pageErrors');
      assert.deepEqual(consoleErrors, [], `1920->2560 consoleErrors (${failedReqs.join(', ')})`);
      await context.close();
    }
  } finally {
    await browser.close();
  }
})();
