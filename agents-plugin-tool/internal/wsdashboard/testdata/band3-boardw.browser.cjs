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
    // 1. 1660x960 (Band 3) with persisted small bb=500, cw=380 (total 896px < 1292px)
    //    Dark and light captures + .dcv-edge drag keeping total stage constant.
    for (const colorScheme of ['dark', 'light']) {
      const { context, tab, page, pageErrors, consoleErrors, failedReqs } = await openLive(browser, {
        width: 1660,
        height: 960,
        colorScheme,
        record: { cards: [], topics: [], bb: 500, cw: 380 },
      });
      const before = await measure(page);
      assert.equal(before.gal, null, `${colorScheme} 1660: Band 3 has no 4th column`);
      assert.equal(before.hasBoardw, true, `${colorScheme} 1660: bb stays loaded in root attr`);
      assert.equal(before.boardwVar, '500px', `${colorScheme} 1660: --boardw preserved`);
      assert(Math.abs(before.gridW - 1272) <= 2, `${colorScheme} 1660: .dgrid.live fills Band 3 stage (~1272px, not 896px), got ${before.gridW}`);
      assert(Math.abs(before.convoW - 380) <= 2, `${colorScheme} 1660: persisted cw=380 applies to convo, got ${before.convoW}`);
      assert(Math.abs(before.boardW - (before.gridW - 16 - before.convoW)) <= 2, `${colorScheme} 1660: board takes remaining stage width, got ${before.boardW}`);
      if (shots) await tab.screenshot({ path: path.join(shots, `band3-1660-${colorScheme}.png`) });

      if (colorScheme === 'dark') {
        await dragSplit(tab, page, 120);
        const after = await measure(page);
        assert(Math.abs(after.gridW - before.gridW) <= 2, `1660 drag keeps total .dgrid.live width constant (${before.gridW} -> ${after.gridW})`);
        assert(Math.abs((after.convoW - before.convoW) - 120) <= 2, `1660 drag widens convo by +120px (${before.convoW} -> ${after.convoW})`);
        assert(Math.abs((before.boardW - after.boardW) - 120) <= 2, `1660 drag narrows board by equal-and-opposite -120px (${before.boardW} -> ${after.boardW})`);
        assert.equal(after.rec.bb, 500, `1660 drag preserves persisted bb=500, got ${after.rec.bb}`);
      }
      assert.deepEqual(pageErrors, [], `${colorScheme} 1660 pageErrors`);
      assert.deepEqual(consoleErrors, [], `${colorScheme} 1660 consoleErrors (${failedReqs.join(', ')})`);
      await context.close();
    }

    // 2. 1920x960 (Band 3) with persisted bb=600, cw=0 (unresized convo):
    //    Stage stays centered at 1292px with 5fr/7fr split (NOT 1045px / 429px).
    //    Dragging .dcv-edge shifts width between convo and board within 1292px.
    //    Widening to 2560x960 enters Band 4 and still applies persisted bb=600.
    {
      const { context, tab, page, pageErrors, consoleErrors, failedReqs } = await openLive(browser, {
        width: 1920,
        height: 960,
        colorScheme: 'dark',
        record: { cards: [], topics: [], bb: 600, cw: 0 },
      });
      const m1920 = await measure(page);
      assert.equal(m1920.gal, null, '1920: Band 3 has no 4th column');
      assert.equal(m1920.hasBoardw, true, '1920: data-boardw preserved');
      assert.equal(m1920.hasConvow, false, '1920: cw=0 leaves data-convow unset');
      assert(Math.abs(m1920.gridW - 1292) <= 2, `1920: .dgrid.live equals 1292px (not ~1045px), got ${m1920.gridW}`);
      const leftPad = m1920.gridLeft - (m1920.railRight + 16);
      const rightPad = m1920.dbodyRight - m1920.gridRight;
      assert(Math.abs(leftPad - 120) <= 3 && Math.abs(rightPad - 120) <= 3, `1920: stage centered in column 2 (left=${leftPad}, right=${rightPad})`);
      const wantConvo5fr = Math.round((1292 - 16) * 5 / 12);
      const wantBoard7fr = Math.round((1292 - 16) * 7 / 12);
      assert(Math.abs(m1920.convoW - wantConvo5fr) <= 2 && Math.abs(m1920.boardW - wantBoard7fr) <= 2,
        `1920: default 5fr/7fr preserved (got ${m1920.convoW}/${m1920.boardW}, want ${wantConvo5fr}/${wantBoard7fr})`);
      if (shots) await tab.screenshot({ path: path.join(shots, 'band3-1920-dark.png') });

      await dragSplit(tab, page, 100);
      const dragged = await measure(page);
      assert(Math.abs(dragged.gridW - 1292) <= 2, `1920 drag keeps .dgrid.live at 1292px, got ${dragged.gridW}`);
      assert(Math.abs((dragged.convoW - m1920.convoW) - 100) <= 2, `1920 drag widens convo by +100px (${m1920.convoW} -> ${dragged.convoW})`);
      assert(Math.abs((m1920.boardW - dragged.boardW) - 100) <= 2, `1920 drag narrows board by -100px (${m1920.boardW} -> ${dragged.boardW})`);
      assert.equal(dragged.rec.bb, 600, `1920 drag keeps persisted bb=600, got ${dragged.rec.bb}`);

      // Reset cw via dblclick so Band 4 also verifies bb=600 with default cw,
      // then pin a topic and widen to 2560x960 (Band 4).
      const fb = await tab.locator('iframe.sframe.on').boundingBox();
      await tab.mouse.dblclick(fb.x + dragged.edgeCenter.x, fb.y + dragged.edgeCenter.y);
      await page.waitForFunction(() => !document.documentElement.hasAttribute('data-convow'));

      await tab.setViewportSize({ width: 2560, height: 960 });
      await page.waitForFunction(() => document.documentElement.getAttribute('data-gal') === 'empty');
      await page.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
      const m2560 = await measure(page);
      assert.equal(m2560.gal, 'empty', '2560: widens into Band 4');
      assert.equal(m2560.hasBoardw, true, '2560: data-boardw still active');
      assert(Math.abs(m2560.boardW - 600) <= 2, `2560: Band 4 applies persisted bb=600 to board, got ${m2560.boardW}`);
      assert(m2560.galW >= 280, `2560: 4th gallery column rendered (${m2560.galW}px)`);
      if (shots) await tab.screenshot({ path: path.join(shots, 'band4-2560-dark.png') });

      assert.deepEqual(pageErrors, [], '1920->2560 pageErrors');
      assert.deepEqual(consoleErrors, [], `1920->2560 consoleErrors (${failedReqs.join(', ')})`);
      await context.close();
    }
  } finally {
    await browser.close();
  }
})();
