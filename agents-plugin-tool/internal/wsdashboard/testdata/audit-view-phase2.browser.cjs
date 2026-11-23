const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const base = process.env.AUDIT_BASE;
const shots = process.env.AUDIT_P2_SHOTS || '';

(async () => {
  const server = await chromium.launchServer({ headless: true });
  const browser = await chromium.connect(server.wsEndpoint());
  if (shots) fs.mkdirSync(shots, { recursive: true });

  const errors = [];
  try {
    // 1. Wide desktop (1440x900) dark
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: 'en-US', colorScheme: 'dark' });
    await context.route('**/*', route => {
      const origin = new URL(route.request().url()).origin;
      if (origin === base) return route.fallback();
      return route.abort();
    });
    const page = await context.newPage();
    page.on('pageerror', e => { console.error('PAGEERROR:', e.message); errors.push(e.message); });
    page.on('console', msg => { if (msg.type() === 'error') console.error('CONSOLE_ERR:', msg.text()); });

    const eventsRequests = [];
    page.on('request', req => {
      if (req.url().includes('/audit/events')) eventsRequests.push(req.url());
    });

    await page.goto(base + '/s/livesession');
    const frameEl = await page.locator('iframe.sframe.on').elementHandle({ timeout: 10000 });
    const frame = await frameEl.contentFrame();

    // Verify no harness chip in #chead, and data-at on rendered entries
    assert.equal(await frame.locator('#chead .hb').count(), 0, '#chead draws no harness chip');
    const atCount = await frame.locator('#r-entries .centries > [data-entry][data-at]').count();
    assert.ok(atCount >= 40, `rendered ConvEntry elements carry data-at (got ${atCount})`);

    // Wait for initial interleaved rows (attached hook inside c1, reminder, subagent, compaction, other)
    await frame.waitForSelector('.au-hid.au-inner[data-event-id="p2-attach-hook"]');
    await frame.waitForSelector('.au-hid[data-event-id="p2-other"]');
    await frame.waitForSelector('.au-hid[data-event-id="p2-orphan-hook"]:not(.au-inner)');

    // Check attached hook is positioned directly below p2-c1
    const hookPrevCall = await frame.evaluate(() => {
      const h = document.querySelector('.au-hid.au-inner[data-event-id="p2-attach-hook"]');
      return h && h.previousElementSibling ? h.previousElementSibling.getAttribute('data-call') : null;
    });
    assert.equal(hookPrevCall, 'p2-c1', 'attached hidden event sits directly below its matching call row');

    // Scroll to top to load older /history (with end:true) -> triggers [T_oldest, T_prev) + open-below [, T_oldest)
    await frame.evaluate(() => {
      const c = document.getElementById('convo');
      c.scrollTop = 0;
      c.dispatchEvent(new Event('scroll'));
    });
    await frame.waitForSelector('.au-sysrow[data-event-id="p2-sys-start"]');
    await frame.waitForSelector('.au-hid[data-event-id="p2-think-hist"]');
    assert.ok(eventsRequests.some(u => u.includes('since=') && u.includes('until=')), 'history page issued half-open [since, until) query');
    assert.ok(eventsRequests.some(u => !u.includes('since=') && u.includes('until=')), 'end:true issued open-below until= query');

    // Verify open-below sysrow is placed at very top of .centries
    const firstIsSysrow = await frame.evaluate(() => {
      const first = document.querySelector('#r-entries .centries > :first-child');
      return first ? first.getAttribute('data-event-id') : null;
    });
    assert.equal(firstIsSysrow, 'p2-sys-start', 'session_start system event renders centered at very top');

    // Scroll down near recent entries for keyboard nav + capture
    await frame.evaluate(() => {
      const c = document.getElementById('convo');
      c.scrollTop = c.scrollHeight;
    });

    // No filter bar is drawn, so every hidden-event row stays visible, also after a reload.
    assert.equal(await frame.locator('#convo .au-bar, .au-fchip').count(), 0, 'no audit filter bar or chips');
    assert.equal(await frame.locator('.au-hid[data-event-id="p2-subagent"]').evaluate(el => el.classList.contains('au-kind-hidden')), false);

    await page.reload();
    const frame2El = await page.locator('iframe.sframe.on').elementHandle({ timeout: 10000 });
    const frame2 = await frame2El.contentFrame();
    await frame2.waitForSelector('.au-hid[data-event-id="p2-subagent"]', { state: 'attached' });
    assert.equal(await frame2.locator('#convo .au-bar, .au-fchip').count(), 0, 'no audit filter bar or chips after reload');

    // Click kind="other" row -> auto-opens Raw tab, marks .au-sel
    await frame2.evaluate(() => {
      const row = document.querySelector('.au-hid[data-event-id="p2-other"]');
      if (row) row.scrollIntoView({ block: 'center' });
    });
    await frame2.locator('.au-hid[data-event-id="p2-other"]').click();
    const win = frame2.locator('.au-win').first();
    await win.waitFor();
    await frame2.waitForFunction(() => document.querySelector('.au-win .au-tab[aria-selected="true"]')?.getAttribute('data-au-tab') === 'raw');
    assert.equal(await frame2.locator('.au-hid[data-event-id="p2-other"].au-sel').count(), 1, 'openEvent marks .au-sel');

    if (shots) {
      await frame2.evaluate(() => {
        const c = document.getElementById('convo');
        c.scrollTop = c.scrollHeight;
      });
      await page.screenshot({ path: path.join(shots, 'audit-p2-wide-dark.png') });
    }

    // ArrowLeft steps from p2-other back into p2-compact (hidden row)
    await win.locator('.au-whd').click();
    await page.keyboard.press('ArrowLeft');
    await frame2.waitForFunction(() => document.querySelector('.au-hid[data-event-id="p2-compact"]')?.classList.contains('au-sel'));

    // Close window cleanly, then test J / K / Enter strictly inside #convo vs #composer
    await win.locator('.au-close').click();
    await frame2.waitForFunction(() => document.querySelectorAll('.au-win').length === 0);

    await frame2.locator('.au-row[data-call="p2-c1"]').focus();
    await page.keyboard.press('j');
    assert.equal(await frame2.evaluate(() => document.activeElement?.getAttribute('data-event-id')), 'p2-attach-hook', 'J advances from call row to attached hidden row');
    await page.keyboard.press('k');
    assert.equal(await frame2.evaluate(() => document.activeElement?.getAttribute('data-call')), 'p2-c1', 'K steps back to call row');

    // Outside #convo (.bcol, #composer, .au-win) -> J must NOT steal focus or advance audit rows
    const composerTA = frame2.locator('#composer textarea').first();
    if (await composerTA.count() > 0 && await composerTA.isVisible()) {
      await composerTA.focus();
      await page.keyboard.press('j');
      assert.equal(await frame2.evaluate(() => document.activeElement?.tagName), 'TEXTAREA', 'J inside #composer does not hijack typing');
    } else {
      await frame2.evaluate(() => {
        const bcol = document.getElementById('bcol');
        bcol.tabIndex = 0;
        bcol.focus();
      });
      await page.keyboard.press('j');
      assert.equal(await frame2.evaluate(() => document.activeElement?.id), 'bcol', 'J inside #bcol does not hijack focus');
    }

    // Press Enter on focused hidden row -> out-only system_reminder opens on Output tab and shows payload
    await frame2.locator('.au-hid[data-event-id="p2-reminder"]').focus();
    await page.keyboard.press('Enter');
    await frame2.locator('.au-win').first().waitFor();
    await frame2.waitForFunction(() => document.querySelector('.au-win .au-tab[aria-selected="true"]')?.getAttribute('data-au-tab') === 'out');
    await frame2.waitForFunction(() => document.querySelector('.au-win .au-panel')?.textContent.includes('Remember to inspect screenshot captures.'));
    assert.equal(await frame2.locator('.au-hid[data-event-id="p2-reminder"].au-sel').count(), 1, 'Enter on focused hidden row opens audit window');
    await frame2.locator('.au-win .au-close').click();

    // Click in-only subagent hidden row -> opens on Input tab
    await frame2.locator('.au-hid[data-event-id="p2-subagent"]').click();
    await frame2.locator('.au-win').first().waitFor();
    await frame2.waitForFunction(() => document.querySelector('.au-win .au-tab[aria-selected="true"]')?.getAttribute('data-au-tab') === 'in');
    await frame2.waitForFunction(() => document.querySelector('.au-win .au-panel')?.textContent.includes('verifier'));
    await frame2.locator('.au-win .au-close').click();

    // Verify 410 reset and 503 retry route handling without crashing
    await page.route('**/s/livesession/audit/events*', route => route.fulfill({ status: 503, body: 'busy' }), { times: 1 });
    await frame2.evaluate(() => document.dispatchEvent(new CustomEvent('ws-entries-merged', { detail: { oldestAt: '', empty: false } })));
    await frame2.waitForTimeout(50);
    await page.route('**/s/livesession/audit/events*', route => route.fulfill({ status: 410, body: 'gone' }), { times: 1 });
    await frame2.evaluate(() => document.dispatchEvent(new CustomEvent('ws-entries-merged', { detail: { oldestAt: '', empty: false } })));
    await frame2.waitForTimeout(80);
    await page.unrouteAll({ behavior: 'ignoreErrors' });

    if (shots) {
      // Wide light (1440x900)
      const lightCtx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: 'en-US', colorScheme: 'light' });
      const lightPage = await lightCtx.newPage();
      await lightPage.goto(base + '/s/livesession');
      const lFrameEl = await lightPage.locator('iframe.sframe.on').elementHandle({ timeout: 10000 });
      const lFrame = await lFrameEl.contentFrame();
      await lFrame.waitForSelector('.au-hid[data-event-id="p2-other"]');
      await lFrame.locator('.au-hid[data-event-id="p2-reminder"]').click();
      await lFrame.locator('.au-win').first().waitFor();
      await lFrame.waitForFunction(() => document.querySelector('.au-win .au-tab[aria-selected="true"]')?.getAttribute('data-au-tab') === 'out' && document.querySelector('.au-win .au-panel')?.textContent.includes('Remember to inspect screenshot captures.'));
      await lFrame.evaluate(() => { const c = document.getElementById('convo'); c.scrollTop = c.scrollHeight; });
      await lightPage.screenshot({ path: path.join(shots, 'audit-p2-wide-light.png') });
      await lightCtx.close();

      // Narrow dark (390x844)
      const narrowDarkCtx = await browser.newContext({ viewport: { width: 390, height: 844 }, locale: 'en-US', colorScheme: 'dark' });
      const ndPage = await narrowDarkCtx.newPage();
      await ndPage.goto(base + '/s/livesession');
      const ndFrameEl = await ndPage.locator('iframe.sframe.on').elementHandle({ timeout: 10000 });
      const ndFrame = await ndFrameEl.contentFrame();
      await ndFrame.waitForSelector('.au-hid[data-event-id="p2-other"]');
      await ndFrame.locator('.au-hid[data-event-id="p2-other"]').click();
      await ndFrame.locator('.au-win').first().waitFor();
      await ndFrame.waitForFunction(() => document.querySelector('.au-win .au-tab[aria-selected="true"]')?.getAttribute('data-au-tab') === 'raw');
      await ndPage.screenshot({ path: path.join(shots, 'audit-p2-narrow-dark.png') });
      await narrowDarkCtx.close();

      // Narrow light (390x844)
      const narrowLightCtx = await browser.newContext({ viewport: { width: 390, height: 844 }, locale: 'en-US', colorScheme: 'light' });
      const nlPage = await narrowLightCtx.newPage();
      await nlPage.goto(base + '/s/livesession');
      const nlFrameEl = await nlPage.locator('iframe.sframe.on').elementHandle({ timeout: 10000 });
      const nlFrame = await nlFrameEl.contentFrame();
      await nlFrame.waitForSelector('.au-hid[data-event-id="p2-other"]');
      await nlFrame.locator('.au-hid[data-event-id="p2-reminder"]').click();
      await nlFrame.locator('.au-win').first().waitFor();
      await nlFrame.waitForFunction(() => document.querySelector('.au-win .au-tab[aria-selected="true"]')?.getAttribute('data-au-tab') === 'out' && document.querySelector('.au-win .au-panel')?.textContent.includes('Remember to inspect screenshot captures.'));
      await nlPage.screenshot({ path: path.join(shots, 'audit-p2-narrow-light.png') });
      await narrowLightCtx.close();
    }

    assert.equal(errors.length, 0, 'no uncaught page errors: ' + errors.join('; '));
    console.log('AUDIT_VIEW_PHASE2_BROWSER_OK');
  } finally {
    await browser.close();
    await server.close();
  }
})();
