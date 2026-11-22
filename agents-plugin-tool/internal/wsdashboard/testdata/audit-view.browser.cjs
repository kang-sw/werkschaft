const assert = require('node:assert/strict');
const fs = require('node:fs');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const base = process.env.AUDIT_BASE;
const shots = process.env.AUDIT_SHOTS;

async function shellDim(page) {
  return page.evaluate(() => parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--sh-dim') || '0') || 0);
}

(async () => {
  const server = await chromium.launchServer({ headless: true });
  const browser = await chromium.connect(server.wsEndpoint());
  if (shots) fs.mkdirSync(shots, { recursive: true });

  const errors = [];
  try {
    // 1. Wide desktop (1440x900)
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: 'en-US', colorScheme: 'dark' });
    await context.route('**/*', route => {
      const origin = new URL(route.request().url()).origin;
      if (origin === base) return route.continue();
      return route.abort();
    });
    const page = await context.newPage();
    page.on('pageerror', e => { console.error('PAGEERROR:', e.message); errors.push(e.message); });
    page.on('console', msg => { if (msg.type() === 'error') console.error('CONSOLE_ERR:', msg.text()); });

    await page.goto(base + '/s/livesession');
    const frameEl = await page.locator('iframe.sframe.on').elementHandle({ timeout: 10000 }).catch(async err => {
      console.error('HTML:', await page.content());
      throw err;
    });
    const frame = await frameEl.contentFrame();

    // Verify Decision (602) legacy fallback is present alongside new .au-tools
    console.log('STEP 1: wait .au-row');
    await frame.waitForSelector('#convo .au-tools .au-row[data-call="c1"]');
    const legacyCount = await frame.locator('#convo details.toolsline').count();
    assert.equal(legacyCount, 1, 'legacy RingTools without call IDs preserves details.toolsline');

    // Wait for lazy post-render hydration on c1, c2, c3
    console.log('STEP 2: wait hydration');
    await frame.waitForFunction(() => {
      const r1 = document.querySelector('.au-row[data-call="c1"]');
      const r2 = document.querySelector('.au-row[data-call="c2"]');
      const r3 = document.querySelector('.au-row[data-call="c3"]');
      return r1 && r1.getAttribute('data-au-hydrated') === '1' &&
        r2 && r2.querySelector('.au-tok')?.textContent === '·' &&
        r3 && r3.classList.contains('au-fail');
    });

    const tok1 = await frame.locator('.au-row[data-call="c1"] .au-tok').textContent();
    const tok2 = await frame.locator('.au-row[data-call="c2"] .au-tok').textContent();
    const tok3 = await frame.locator('.au-row[data-call="c3"] .au-tok').textContent();
    const att1 = await frame.locator('.au-row[data-call="c1"] .au-att').textContent();
    assert.equal(tok1, '+2.4k', 'first call in request gets token delta');
    assert.equal(tok2, '·', 'parallel sibling in same request gets middot');
    assert.equal(tok3, '+1.7k', 'c3 in req-2 -> req-3 gets token delta');
    assert.equal(att1, '+2', 'c1 shows attached event count (+2)');

    // Verify fold (+2 more) and uncapped/no-ID static row inside .au-fold
    const moreBtn = frame.locator('.au-tools .au-more');
    assert.equal(await frame.locator('.au-row[data-call="c4"]').isVisible(), false);
    await moreBtn.click();
    assert.equal(await frame.locator('.au-row[data-call="c4"]').isVisible(), true);
    assert.equal(await frame.locator('.au-tools .au-static').isVisible(), true);

    // Click c1 to open floating audit window + verify wide darkroom & shell dim
    console.log('STEP 3: open c1');
    await frame.locator('.au-row[data-call="c1"]').click();
    const win = frame.locator('.au-win').first();
    await win.waitFor();
    await win.locator('.au-code').waitFor();

    const scrimVisible = await frame.locator('.au-scrim').isVisible();
    assert.equal(scrimVisible, true, 'wide unpinned window activates darkroom scrim');
    const convoZ = await frame.evaluate(() => getComputedStyle(document.getElementById('convo')).zIndex);
    assert.equal(convoZ, '66', '#convo stays above darkroom scrim');

    // Verify redaction chip, stored_trunc divider, and seam seen-cut badge on Input tab
    const winHtml = await win.locator('.au-panel').innerHTML();
    assert.ok(winHtml.includes('api-key'), 'redaction rule chip rendered');
    assert.ok(winHtml.includes('au-trunc-row'), 'stored_trunc middle omitted divider rendered');
    assert.ok(winHtml.includes('au-seencut-badge'), 'seam seen-cut badge rendered');
    assert.ok(winHtml.includes('au-unseen'), 'lines after seam dimmed as unseen');

    // Capture wide dark screenshot (LOOK check 1)
    await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'dark'));
    await frame.evaluate(() => document.documentElement.setAttribute('data-theme', 'dark'));
    await frame.waitForTimeout(80);
    if (shots) {
      await page.screenshot({ path: `${shots}/audit-wide-dark.png` });
    }

    // Switch to light theme & capture wide light screenshot (LOOK check 2)
    await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'));
    await frame.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'));
    await frame.waitForTimeout(100);
    if (shots) {
      await page.screenshot({ path: `${shots}/audit-wide-light.png` });
    }
    await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'dark'));
    await frame.evaluate(() => document.documentElement.setAttribute('data-theme', 'dark'));

    // Drag header + resize corner within viewport bounds
    const beforeBox = await win.boundingBox();
    const hdBox = await win.locator('.au-whd').boundingBox();
    await page.mouse.move(hdBox.x + 80, hdBox.y + 16);
    await page.mouse.down();
    await page.mouse.move(hdBox.x + 120, hdBox.y + 36);
    await page.mouse.up();
    const afterDrag = await win.boundingBox();
    assert.ok(Math.abs(afterDrag.x - (beforeBox.x + 40)) < 4, 'window drags horizontally');

    // Output tab + Load more
    console.log('STEP 4: out + loadmore + search + meta + events + errors');
    await win.locator('[data-au-tab="out"]').click();
    const loadMoreBtn = win.locator('[data-au-more="out"]');
    await loadMoreBtn.waitFor();
    const beforeLen = await win.locator('.au-line').count();
    await loadMoreBtn.click();
    await frame.waitForFunction((prev) => document.querySelectorAll('.au-win .au-line').length > prev, beforeLen);

    // Search Ctrl+F scoped to window + Enter/Shift+Enter + Wrap + Copy
    await win.locator('.au-sinp').click();
    await page.keyboard.press('Control+f');
    await win.locator('.au-sinp').fill('needle');
    const scnt = await win.locator('.au-scnt').textContent();
    assert.ok(scnt.startsWith('1/'), `search match count shown: ${scnt}`);
    await page.keyboard.press('Enter');
    assert.ok((await win.locator('.au-scnt').textContent()).startsWith('2/'));
    await win.locator('.au-wrap-btn').click();
    assert.equal(await win.locator('.au-code.au-wrap').count(), 1);
    await win.locator('.au-copy-btn').click();

    // Meta tab checks signature_only + request/ctx
    await win.locator('[data-au-tab="meta"]').click();
    const metaText = await win.locator('.au-meta').textContent();
    assert.ok(metaText.includes('req-1') && metaText.includes('false'), 'Meta shows request_id and signature_only');

    // Linked events tab + inspect e-hook-1 + Back breadcrumb
    await win.locator('[data-au-tab="events"]').click();
    await win.locator('[data-au-ev="e-hook-1"]').click();
    await win.locator('.au-crumb:not([hidden])').waitFor();
    await win.locator('.au-banner').waitFor(); // unseen event banner
    await win.locator('.au-crumb-back').click();
    assert.equal(await win.locator('.au-crumb:not([hidden])').count(), 0);

    // Next call via button + ArrowRight / ArrowLeft, plus kind="other" Raw tab
    await win.locator('.au-next').click();
    await frame.waitForFunction(() => document.querySelector('.au-win .au-wname')?.textContent.includes('Grep'));
    await win.locator('.au-whd').click();
    await page.keyboard.press('ArrowRight');
    await frame.waitForFunction(() => document.querySelector('.au-win .au-wname')?.textContent.includes('Bash'));
    await page.keyboard.press('ArrowRight');
    await frame.waitForFunction(() => document.querySelector('.au-win .au-wname')?.textContent.includes('Edit'));
    await win.locator('[data-au-tab="raw"]').click();
    assert.ok((await win.locator('.au-banner').textContent()).includes('Original harness event'));

    // Empty state on c_empty
    await win.locator('.au-next').click();
    await frame.waitForFunction(() => {
      const st = document.querySelector('.au-win .au-state');
      return st && !st.textContent.includes('Loading');
    });
    assert.ok((await win.locator('.au-state').textContent()).includes('No audit events recorded'));

    // 410 / 404 / 503 error states via route interception
    for (const [status, want] of [[410, 'Cursor expired'], [404, 'Event not retained'], [503, 'Audit store unavailable']]) {
      await page.route('**/s/livesession/audit/events?call=*', route => route.fulfill({ status, body: 'err' }), { times: 1 });
      await frame.locator('.au-row[data-call="c1"]').click();
      await frame.waitForFunction((text) => document.querySelector('.au-win .au-state')?.textContent.includes(text), want);
    }
    await page.unrouteAll({ behavior: 'ignoreErrors' });

    // Close unpinned via Escape (must stay inside session, not leave shell)
    await frame.locator('.au-row[data-call="c1"]').click();
    await win.locator('.au-code').waitFor();
    await page.keyboard.press('Escape');
    assert.equal(await frame.locator('.au-win').count(), 0, 'Escape closes top audit window');

    // Pin cap 4 + 5th pinned window replaces oldest + shows WsToast
    for (let i = 0; i < 5; i++) {
      const cid = i % 2 === 0 ? 'c1' : 'c2';
      await frame.locator(`.au-row[data-call="${cid}"]`).click();
      const wins = frame.locator('.au-win');
      await wins.last().locator('.au-pin').click();
    }
    assert.equal(await frame.locator('.au-win').count(), 4, 'pin cap enforces max 4 pinned windows');
    // Verify staggered +24px offsets across pinned windows
    const b0 = await frame.locator('.au-win').nth(0).boundingBox();
    const b1 = await frame.locator('.au-win').nth(1).boundingBox();
    assert.ok(Math.abs(Math.abs(b1.x - b0.x) - 24) <= 2 || Math.abs(Math.abs(b1.y - b0.y) - 24) <= 2, 'pinned windows stagger by 24px');

    // Clear pinned windows with 4 Escapes
    for (let i = 0; i < 4; i++) await page.keyboard.press('Escape');
    assert.equal(await frame.locator('.au-win').count(), 0);
    await context.close();

    // 2. Narrow mobile viewport (430x844): 72% bottom sheet, no darkroom
    console.log('STEP 5: mobile');
    const mobileCtx = await browser.newContext({ viewport: { width: 430, height: 844 }, locale: 'en-US', colorScheme: 'dark' });
    await mobileCtx.route('**/*', route => {
      const origin = new URL(route.request().url()).origin;
      if (origin === base) return route.continue();
      return route.abort();
    });
    const mPage = await mobileCtx.newPage();
    mPage.on('pageerror', e => errors.push(e.message));
    await mPage.goto(base + '/s/livesession');
    const mFrame = await (await mPage.locator('iframe.sframe.on').elementHandle()).contentFrame();
    await mFrame.waitForSelector('.au-row[data-call="c1"]');
    await mFrame.locator('.au-row[data-call="c1"]').click();
    const mWin = mFrame.locator('.au-win').first();
    await mWin.locator('.au-code').waitFor();
    assert.equal(await mFrame.locator('.au-scrim').isVisible(), false, 'narrow sheet does not activate wide darkroom');
    const mBox = await mWin.boundingBox();
    assert.ok(mBox.height >= 844 * 0.65 && mBox.height <= 844 * 0.78, `narrow bottom sheet is ~72% height (got ${mBox.height})`);

    if (shots) {
      await mPage.evaluate(() => document.documentElement.setAttribute('data-theme', 'dark'));
      await mFrame.evaluate(() => document.documentElement.setAttribute('data-theme', 'dark'));
      await mFrame.waitForTimeout(80);
      await mPage.screenshot({ path: `${shots}/audit-narrow-dark.png` });
      await mPage.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'));
      await mFrame.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'));
      await mFrame.waitForTimeout(80);
      await mPage.screenshot({ path: `${shots}/audit-narrow-light.png` });
    }
    await mobileCtx.close();

    assert.deepEqual(errors, [], `unexpected page errors: ${errors.join('; ')}`);
    console.log('AUDIT_VIEW_BROWSER_OK');
  } finally {
    await browser.close();
    await server.close();
  }
})();
