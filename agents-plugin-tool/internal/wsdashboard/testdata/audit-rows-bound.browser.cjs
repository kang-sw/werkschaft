const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const base = process.env.AUDIT_BASE;
const shots = process.env.AUDIT_BOUND_SHOTS || '';
// WS_AUDIT_BOUND_EXPECT=report prints the measurements without asserting
// them (to capture the "before" screenshot against the unfixed asset).
const reportOnly = process.env.AUDIT_BOUND_EXPECT === 'report';

(async () => {
  const server = await chromium.launchServer({ headless: true });
  const browser = await chromium.connect(server.wsEndpoint());
  if (shots) fs.mkdirSync(shots, { recursive: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: 'en-US', colorScheme: 'dark' });
    await context.route('**/*', route => (new URL(route.request().url()).origin === base ? route.fallback() : route.abort()));
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', e => errors.push(e.message));
    const events = [];
    page.on('request', req => { if (req.url().includes('/audit/events')) events.push(req.url()); });
    // Audit pages are held until the conversation is pinned to its bottom, as
    // after a session switch where rows arrive once the view was restored.
    let release;
    const gate = new Promise(r => { release = r; });
    await context.route(/\/audit\/events/, async route => { await gate; await route.continue(); });

    await page.goto(base + '/s/livesession');
    const frame = await (await page.locator('iframe.sframe.on').elementHandle({ timeout: 10000 })).contentFrame();
    await frame.waitForSelector('#r-entries .centries > [data-entry]', { state: 'attached' });
    await page.waitForTimeout(400);
    await frame.evaluate(() => { const c = document.getElementById('convo'); c.scrollTop = c.scrollHeight; c.dispatchEvent(new Event('scroll')); });
    const gap = () => frame.evaluate(() => { const c = document.getElementById('convo'); return Math.round(c.scrollHeight - c.clientHeight - c.scrollTop); });
    assert.ok((await gap()) <= 24, 'pinned to the bottom before rows arrive');
    release();
    await frame.waitForSelector('.au-hid, .au-sysrow', { state: 'attached' });
    await page.waitForTimeout(800);
    const pinnedGap = await gap();
    console.log('PINNED-GAP ' + pinnedGap);
    // The short conversation loads its (empty) older history at once, which ends the history.
    await frame.evaluate(() => { const c = document.getElementById('convo'); c.scrollTop = 0; c.dispatchEvent(new Event('scroll')); });
    await page.waitForTimeout(2500);

    const m = await frame.evaluate(() => {
      const box = document.querySelector('#r-entries .centries');
      const visible = el => !el.hidden && !el.closest('[hidden]');
      return {
        entries: box.querySelectorAll(':scope > [data-entry]').length,
        eventRows: box.querySelectorAll('.au-hid, .au-sysrow').length,
        banners: box.querySelectorAll('.au-sysrow').length,
        visibleEventRows: [...box.querySelectorAll('.au-hid, .au-sysrow')].filter(visible).length,
        foldRows: box.querySelectorAll('.au-run').length,
        scrollHeight: document.getElementById('convo').scrollHeight,
      };
    });
    m.eventRequests = events.length;
    console.log('MEASURE ' + JSON.stringify(m));
    if (shots) {
      await page.screenshot({ path: path.join(shots, (reportOnly ? 'before' : 'after') + '-convo.png') });
      await frame.evaluate(() => { const c = document.getElementById('convo'); c.scrollTop = 0; });
      await page.screenshot({ path: path.join(shots, (reportOnly ? 'before' : 'after') + '-convo-top.png') });
    }
    assert.deepEqual(errors, [], 'no page errors');
    if (!reportOnly) {
      assert.ok(m.eventRequests <= 4, `bounded audit fetch (${m.eventRequests} requests)`);
      assert.ok(m.eventRows <= 240, `rows follow the loaded span plus one page (${m.eventRows})`);
      assert.ok(pinnedGap <= 24, `rows arriving under a bottom-pinned view keep it pinned (${pinnedGap}px off)`);
      assert.ok(m.banners <= 1, `one session banner (${m.banners})`);
      assert.ok(m.visibleEventRows <= 3, `noise rows are folded (${m.visibleEventRows} visible)`);
    }
  } finally {
    await browser.close();
    await server.close();
  }
})().catch(e => { console.error(e); process.exit(1); });
