const assert = require('node:assert/strict');
const fs = require('node:fs');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const base = process.env.AUDIT_BASE;
const shots = process.env.AUDIT_SHOTS;

async function pixel(page, x, y) {
  const buf = await page.screenshot({ clip: { x, y, width: 1, height: 1 } });
  return page.evaluate(async b64 => {
    const bytes = Uint8Array.from(atob(b64), c => c.charCodeAt(0));
    const bmp = await createImageBitmap(new Blob([bytes], { type: 'image/png' }));
    const c = document.createElement('canvas');
    c.width = c.height = 1;
    const g = c.getContext('2d');
    g.drawImage(bmp, 0, 0);
    return Array.from(g.getImageData(0, 0, 1, 1).data.slice(0, 3));
  }, buf.toString('base64'));
}

const sameRGB = (a, b) => a.every((v, i) => Math.abs(v - b[i]) <= 2);

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
        r2 && r2.getAttribute('data-au-hydrated') === '1' &&
        r3 && r3.getAttribute('data-au-hydrated') === '1';
    });

    const tok1 = await frame.locator('.au-row[data-call="c1"] .au-tok').textContent();
    const tok2 = await frame.locator('.au-row[data-call="c2"] .au-tok').textContent();
    const tok3 = await frame.locator('.au-row[data-call="c3"] .au-tok').textContent();
    const tok1Title = await frame.locator('.au-row[data-call="c1"] .au-tok').getAttribute('title');
    const att1 = await frame.locator('.au-row[data-call="c1"] .au-att').textContent();
    assert.equal(tok1, '+2.2M', 'c1 shows estimated +(in+out)/4 token size');
    assert.equal(tok2, '+2.2M', 'parallel sibling c2 shows its own estimated +(in+out)/4 instead of middot');
    assert.equal(tok3, '+20', 'c3 shows estimated +(in+out)/4 token size');
    assert.ok(tok1Title && tok1Title.includes('48000 \u2192 50400'), 'tooltip retains ctx_before -> ctx_after');
    assert.equal(att1, '+2', 'c1 shows attached event count (+2)');

    // No group frame: no header line, no fold or "+N more" button. Every call
    // row (ID'd or not) is rendered visible from the start.
    assert.equal(await frame.locator('.au-thead, .au-more, .au-fold').count(), 0, 'tool-call frame shell is gone');
    assert.equal(await frame.locator('.au-row[data-call="c4"]').isVisible(), true);
    assert.equal(await frame.locator('.au-tools .au-static').isVisible(), true);

    // Click c1 to open floating audit window + verify wide darkroom & shell dim
    console.log('STEP 3: open c1');
    await frame.locator('.au-row[data-call="c1"]').click();
    assert.equal(await frame.locator('.au-win').count(), 0, 'summary expands without opening the inspector');
    await frame.locator('.au-row[data-call="c1"]').locator('xpath=..').locator('.au-open-call').click();
    const win = frame.locator('.au-win').first();
    await win.waitFor();
    await win.locator('.au-code').waitFor();

    const scrimVisible = await frame.locator('.au-scrim').isVisible();
    assert.equal(scrimVisible, true, 'wide unpinned window activates darkroom scrim');
    const convoZ = await frame.evaluate(() => getComputedStyle(document.getElementById('convo')).zIndex);
    assert.equal(convoZ, '66', '#convo stays above darkroom scrim');

    // Integrated shell-vanish check: shell sets data-fdim and keeps topbar visible beneath frame scrim at 1440x900
    await page.waitForFunction(() => document.documentElement.hasAttribute('data-fdim'), null, { timeout: 5000 });
    assert.equal(await page.locator('.dwrap > .top').isVisible(), true, 'shell topbar remains visible while dimmed');

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

    // At 1920x900 where #rail is pinned open, verify #rail remains visible & distinct under data-fdim
    await page.setViewportSize({ width: 1920, height: 900 });
    await page.waitForTimeout(80);
    assert.equal(await page.locator('#rail').isVisible(), true, 'shell #rail remains visible while dimmed at 1920x900');
    const railBox = await page.locator('#rail').boundingBox();
    if (railBox && railBox.x >= 4) {
      const px = Math.round(railBox.x + railBox.width / 2);
      const py = Math.round(railBox.y + railBox.height - 40);
      const gx = Math.max(1, Math.round(railBox.x / 2));
      const railRGB = await pixel(page, px, py);
      const gutterRGB = await pixel(page, gx, py);
      assert.ok(!sameRGB(railRGB, gutterRGB), `shell rail remains distinct from background under audit scrim (${railRGB} vs ${gutterRGB})`);
    }
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.waitForTimeout(80);

    // Drag header + resize clamping near bottom-right viewport edge
    const beforeBox = await win.boundingBox();
    const hdBox = await win.locator('.au-whd').boundingBox();
    await page.mouse.move(hdBox.x + 80, hdBox.y + 16);
    await page.mouse.down();
    await page.mouse.move(hdBox.x + 120, hdBox.y + 36);
    await page.mouse.up();
    const afterDrag = await win.boundingBox();
    assert.ok(Math.abs(afterDrag.x - (beforeBox.x + 40)) < 4, 'window drags horizontally');

    // Push window near bottom-right corner and drag .au-resize outward: must clamp within viewport
    const edgeHd = await win.locator('.au-whd').boundingBox();
    await page.mouse.move(edgeHd.x + 40, edgeHd.y + 12);
    await page.mouse.down();
    await page.mouse.move(edgeHd.x + 500, edgeHd.y + 420);
    await page.mouse.up();
    const nearEdge = await win.boundingBox();
    await page.mouse.move(nearEdge.x + nearEdge.width - 4, nearEdge.y + nearEdge.height - 4);
    await page.mouse.down();
    await page.mouse.move(nearEdge.x + nearEdge.width + 320, nearEdge.y + nearEdge.height + 320);
    await page.mouse.up();
    const afterResize = await win.boundingBox();
    assert.ok(afterResize.x >= 0 && afterResize.y >= 0, 'resized window stays inside top/left viewport bounds');
    assert.ok(afterResize.x + afterResize.width <= 1440 && afterResize.y + afterResize.height <= 900,
      `resized window stays within 1440x900 bounds (right=${afterResize.x + afterResize.width}, bottom=${afterResize.y + afterResize.height})`);

    // Decision (605): keyboard outside .au-win must NOT step/close/hijack find on audit window
    await frame.locator('.au-row[data-call="c1"]').focus();
    await page.keyboard.press('ArrowRight');
    assert.ok((await win.locator('.au-wname').textContent()).includes('Read'), 'ArrowRight outside .au-win does not step audit window');
    await win.locator('.au-pin').click(); // pin -> uncover #convo/#composer so focus can move outside .au-win
    await frame.evaluate(() => {
      const target = document.querySelector('#text, textarea, .au-row[data-call="c1"]');
      if (target && typeof target.focus === 'function') target.focus();
      if (document.activeElement && document.activeElement.closest && document.activeElement.closest('.au-win')) {
        document.activeElement.blur();
      }
    });
    assert.equal(await frame.evaluate(() => !!document.activeElement?.closest('.au-win')), false, 'focus moved outside .au-win before outside key test');
    await page.keyboard.press('ArrowRight');
    await page.keyboard.press('ArrowLeft');
    await page.keyboard.press('Control+f');
    await page.keyboard.press('Escape');
    assert.equal(await frame.locator('.au-win').count(), 1, 'Escape outside .au-win does not close audit window');
    assert.ok((await win.locator('.au-wname').textContent()).includes('Read'), 'Arrows outside .au-win do not step call');
    assert.equal(await frame.evaluate(() => !!document.activeElement?.closest('.au-win')), false, 'Ctrl+F outside .au-win does not move focus into .au-sinp');
    await win.locator('.au-pin').click(); // return to unpinned

    // Output tab reads e-res-1 + Load more works on e-res-1
    console.log('STEP 4: out + loadmore + search + meta + events + errors');
    await win.locator('[data-au-tab="out"]').click();
    await frame.waitForFunction(() => document.querySelector('.au-win .au-code')?.textContent.includes('ok line needle here in paged output'));
    const loadMoreBtn = win.locator('[data-au-more="out"]');
    await loadMoreBtn.waitFor();
    // Load more is guarded per envelope+part: event A's pending request must not block event B's
    // same-part request after the window steps to the next call, and B still sends only one.
    {
      const hits = { 'e-res-1': 0, 'e-res-2': 0 };
      let release;
      const gate = new Promise(r => { release = r; });
      const hold = async route => {
        const m = /event\/(e-res-\d)\?/.exec(route.request().url());
        if (m) hits[m[1]]++;
        await gate;
        await route.continue();
      };
      await context.route(/part=out&offset=/, hold);
      await loadMoreBtn.click();
      await page.waitForTimeout(100);
      assert.equal(hits['e-res-1'], 1, 'event A requested its range');
      await win.locator('.au-next').click();
      await win.locator('.au-next').click();
      await frame.waitForFunction(() => document.querySelector('.au-win .au-wname')?.textContent.includes('Grep'));
      await win.locator('[data-au-tab="out"]').click();
      await win.locator('[data-au-more="out"]').waitFor();
      await win.locator('[data-au-more="out"]').click();
      await page.waitForTimeout(200);
      assert.equal(hits['e-res-2'], 1, 'event B loads more while A is pending');
      await win.locator('.au-wrap-btn').click();
      await win.locator('[data-au-more="out"]').click();
      await win.locator('.au-wrap-btn').click();
      await page.waitForTimeout(200);
      assert.equal(hits['e-res-2'], 1, 'still one request per envelope+part');
      const bBefore = await win.locator('.au-line').count();
      release();
      await frame.waitForFunction((prev) => document.querySelectorAll('.au-win .au-line').length > prev, bBefore);
      await page.waitForTimeout(300);
      await context.unroute(/part=out&offset=/, hold);
      await win.locator('.au-prev').click();
      await win.locator('.au-prev').click();
      await frame.waitForFunction(() => document.querySelector('.au-win .au-wname')?.textContent.includes('Read'));
      await win.locator('[data-au-tab="out"]').click();
      await win.locator('[data-au-more="out"]').waitFor();
    }
    const beforeLen = await win.locator('.au-line').count();
    // A re-render (Wrap) while a range request is pending replaces the button;
    // a second Load more must not fetch and append the same chunk again.
    let rangeRequests = 0;
    let releaseRange;
    const rangeGate = new Promise(r => { releaseRange = r; });
    const holdRange = async route => { rangeRequests++; await rangeGate; await route.continue(); };
    await context.route(/part=out&offset=/, holdRange);
    await loadMoreBtn.click();
    await win.locator('.au-wrap-btn').click();
    await win.locator('[data-au-more="out"]').click();
    await win.locator('.au-wrap-btn').click();
    await win.locator('[data-au-more="out"]').click();
    await page.waitForTimeout(200);
    assert.equal(rangeRequests, 1, 'one range request while the first is pending');
    releaseRange();
    await frame.waitForFunction((prev) => document.querySelectorAll('.au-win .au-line').length > prev, beforeLen);
    await page.waitForTimeout(200);
    await context.unroute(/part=out&offset=/, holdRange);
    assert.equal(rangeRequests, 1, 'the pending chunk was appended once');
    if (shots) await page.screenshot({ path: `${shots}/audit-paged.png` });

    // Search Ctrl+F scoped to window + Enter/Shift+Enter + Wrap + Copy
    await win.locator('.au-sinp').click();
    await page.keyboard.press('Control+f');
    await win.locator('.au-sinp').fill('needle');
    // The count is the server's spans, asked for after the debounce.
    await frame.waitForFunction(() => /^\d+\//.test(document.querySelector('.au-win .au-scnt')?.textContent || ''));
    const scnt = await win.locator('.au-scnt').textContent();
    assert.ok(scnt.startsWith('1/'), `search match count shown: ${scnt}`);
    await page.keyboard.press('Enter');
    assert.ok((await win.locator('.au-scnt').textContent()).startsWith('2/'));
    await win.locator('.au-wrap-btn').click();
    assert.equal(await win.locator('.au-code.au-wrap').count(), 1);
    await win.locator('.au-copy-btn').click();

    // Meta tab checks signature_only + request/ctx + non-zero out_bytes from e-res-1
    await win.locator('[data-au-tab="meta"]').click();
    const metaText = await win.locator('.au-meta').textContent();
    assert.ok(metaText.includes('req-1') && metaText.includes('false'), 'Meta shows request_id and signature_only');
    assert.ok(!/\bout 0\b/.test(metaText), `Meta reflects non-zero e-res-1 out_bytes (${metaText})`);

    // Linked events tab + inspect e-hook-1 + Back breadcrumb (restoring resultEnvelope too)
    await win.locator('[data-au-tab="events"]').click();
    await win.locator('[data-au-ev="e-hook-1"]').click();
    await win.locator('.au-crumb:not([hidden])').waitFor();
    await win.locator('.au-banner').waitFor(); // unseen event banner
    await win.locator('.au-crumb-back').click();
    assert.equal(await win.locator('.au-crumb:not([hidden])').count(), 0);

    // Next call via button + ArrowRight / ArrowLeft, plus c3 tool_result failed pill & output, plus kind="other" Raw tab
    // Next/prev step through visible rows inside expanded owning disclosures.
    await frame.waitForSelector('.au-call-details .au-hid[data-event-id="e-hook-1"]', { state: 'attached' });
    assert.equal(await frame.locator('.au-hid[data-event-id="e-hook-1"]').isVisible(), true, 'hook is inside the expanded call');
    await frame.evaluate(() => { for (const s of document.querySelectorAll('.au-call-details:not([open]) > summary, .turnline:not([open]) > summary, .boundary-badge:not([open]) > summary')) s.click(); });
    await win.locator('.au-next').click();
    await frame.waitForSelector('.au-call-details .au-hid[data-event-id="e-hook-1"].au-sel');
    await win.locator('.au-next').click();
    await frame.waitForFunction(() => document.querySelector('.au-win .au-wname')?.textContent.includes('Grep'));
    await win.locator('.au-whd').click();
    await page.keyboard.press('ArrowRight');
    await frame.waitForFunction(() => document.querySelector('.au-win .au-wname')?.textContent.includes('Bash') && document.querySelector('.au-win .au-wstatus')?.classList.contains('au-err'));
    assert.ok((await win.locator('.au-wstatus').textContent()).includes('failed'), 'c3 header pill shows failed from e-res-3 meta.is_error');
    await win.locator('[data-au-tab="out"]').click();
    await frame.waitForFunction(() => document.querySelector('.au-win .au-code')?.textContent.includes('exit status 1'));
    const bashOutText = await win.locator('.au-code').textContent();
    assert.ok(bashOutText.includes('TestSomething') && bashOutText.includes('exit status 1'), 'c3 Output tab shows e-res-3 failure output');
    await win.locator('[data-au-tab="meta"]').click();
    assert.ok((await win.locator('.au-meta').textContent()).includes('is_error'), 'c3 Meta tab shows result is_error');
    await win.locator('.au-whd').click();
    await page.keyboard.press('ArrowRight');
    await frame.waitForFunction(() => document.querySelector('.au-win .au-wname')?.textContent.includes('Edit'));
    await win.locator('[data-au-tab="raw"]').click();
    assert.ok((await win.locator('.au-banner').textContent()).includes('Original harness event'));

    // Empty state on c_empty (stepping past c4's attached e-call-4 hidden row first)
    await win.locator('.au-next').click();
    await frame.waitForSelector('.au-call-details .au-hid[data-event-id="e-call-4"].au-sel');
    await win.locator('.au-next').click();
    await frame.waitForFunction(() => {
      const st = document.querySelector('.au-win .au-state');
      return st && !st.textContent.includes('Loading');
    });
    assert.ok((await win.locator('.au-state').textContent()).includes('No audit events recorded'));

    // 410 / 404 / 503 error states via route interception
    for (const [status, want] of [[410, 'Cursor expired'], [404, 'Event not retained'], [503, 'Audit store unavailable']]) {
      await page.route('**/s/livesession/audit/events?call=*', route => route.fulfill({ status, body: 'err' }), { times: 1 });
      await frame.locator('.au-row[data-call="c1"]').evaluate(el => { el.closest('details').open = true; });
      await frame.locator('.au-row[data-call="c1"]').locator('xpath=..').locator('.au-open-call').click();
      await frame.waitForFunction((text) => document.querySelector('.au-win .au-state')?.textContent.includes(text), want);
    }
    await page.unrouteAll({ behavior: 'ignoreErrors' });

    // Result event error (404 on e-res-2) on a fresh window keeps Input tab intact and shows error only on Output tab
    await win.locator('.au-close').click();
    await frame.waitForFunction(() => document.querySelectorAll('.au-win').length === 0);
    await page.route('**/s/livesession/audit/event/e-res-2*', route => route.fulfill({ status: 404, body: 'missing' }), { times: 1 });
    await frame.locator('.au-row[data-call="c2"]').evaluate(el => { el.closest('details').open = true; });
    await frame.locator('.au-row[data-call="c2"]').locator('xpath=..').locator('.au-open-call').click();
    await frame.waitForFunction(() => document.querySelector('.au-win .au-wname')?.textContent.includes('Grep') && document.querySelector('.au-win .au-code')?.textContent.includes('sessionSecret'));
    await win.locator('[data-au-tab="out"]').click();
    await frame.waitForFunction(() => document.querySelector('.au-win .au-state')?.textContent.includes('Event not retained'));
    await win.locator('[data-au-tab="in"]').click();
    assert.ok((await win.locator('.au-code').textContent()).includes('sessionSecret'), 'Input tab stays intact when result envelope returns 404');

    // Close unpinned via Escape (must stay inside session, clear shell data-fdim)
    await frame.locator('.au-row[data-call="c1"]').evaluate(el => { el.closest('details').open = true; });
    await frame.locator('.au-row[data-call="c1"]').locator('xpath=..').locator('.au-open-call').click();
    await win.locator('.au-code').waitFor();
    await win.focus();
    await page.keyboard.press('Escape');
    assert.equal(await frame.locator('.au-win').count(), 0, 'Escape closes top audit window');
    await page.waitForFunction(() => !document.documentElement.hasAttribute('data-fdim'), null, { timeout: 5000 });

    // Pin cap 4 + 5th pinned window replaces oldest + shows WsToast
    for (let i = 0; i < 5; i++) {
      const cid = i % 2 === 0 ? 'c1' : 'c2';
      await frame.locator(`.au-row[data-call="${cid}"]`).evaluate(el => { el.closest('details').open = true; });
      await frame.locator(`.au-open-call[data-call="${cid}"]`).click();
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
    await mFrame.locator('.au-row[data-call="c1"]').evaluate(el => { el.closest('details').open = true; });
    await mFrame.locator('.au-row[data-call="c1"]').locator('xpath=..').locator('.au-open-call').click();
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
