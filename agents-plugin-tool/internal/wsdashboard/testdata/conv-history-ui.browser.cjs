// Run by TestConvHistoryUIBrowser against an isolated live dashboard daemon.
// argv: <base> <ctrlUrl> <pagedEntry> <tooOldEntry> <liveEntry> <shots dir|"">
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, ctrlUrl, pagedEntry, tooOldEntry, liveEntry, shots] = process.argv.slice(2);

function out(name) { return shots ? path.join(shots, name) : undefined; }

async function postJSON(url, body) {
  const r = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body || {})
  });
  if (!r.ok && r.status !== 204) throw new Error(`POST ${url}: HTTP ${r.status}`);
  return r.status === 204 ? null : r.json();
}

async function getJSON(url) {
  const r = await fetch(url);
  if (!r.ok) throw new Error(`GET ${url}: HTTP ${r.status}`);
  return r.json();
}

(async () => {
  if (shots) fs.mkdirSync(shots, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  try {
    for (const theme of ['dark', 'light']) {
      await postJSON(ctrlUrl + '/reset');
      const context = await browser.newContext({
        viewport: { width: 1440, height: 900 },
        colorScheme: theme,
        locale: 'ko-KR',
        serviceWorkers: 'block'
      });
      await context.addCookies([{ name: 'ws_dashboard_lang', value: 'ko', url: base }]);
      const page = await context.newPage();
      const errors = [];
      page.on('pageerror', e => { if (!/serviceWorker/.test(e.message)) errors.push(e.stack || e.message); });

      // 1. Existing-session popover ("대시보드가 실행하는 세션") on /s/leadsess
      await page.goto(base + '/s/leadsess');
      const sessFrame = page.frameLocator('iframe.sframe.on');
      await sessFrame.locator('#convo .centries [data-entry]').first().waitFor({ timeout: 10000 });
      // Open the hosted session popover via the shell header chip (.host.hbtn)
      const hostChip = page.locator('.host.hbtn').first();
      await hostChip.waitFor({ timeout: 10000 });
      await hostChip.click();

      const pop = sessFrame.locator('.rpop:not([hidden])');
      const convSw = pop.locator('[data-conv-history-toggle="1"]');
      const banner = pop.locator('[data-conv-restart-banner="1"]');
      const stopNow = pop.locator('[data-conv-stop-now="1"]');
      await convSw.waitFor({ timeout: 10000 });

      assert.equal(await pop.locator('[data-wb-only-switch]').count(), 0, `${theme}: whiteboard-only not duplicated in session popover`);
      assert.equal(await convSw.getAttribute('aria-checked'), 'false', `${theme}: conv toggle starts OFF`);
      assert.equal(await banner.isVisible(), false, `${theme}: restart banner starts hidden`);

      // Toggle ON -> plain /settings only (process remains running), restart banner appears.
      await convSw.click();
      await banner.waitFor({ state: 'visible', timeout: 10000 });
      assert.equal(await convSw.getAttribute('aria-checked'), 'true', `${theme}: conv toggle flipped ON`);
      const bannerTxt = await banner.textContent();
      assert(bannerTxt.includes('스키마 반영을 위해 중지 후 재시작 필요'), `${theme}: Korean restart hint rendered: ${bannerTxt}`);
      assert(await stopNow.isVisible(), `${theme}: Stop now button visible`);
      await page.waitForTimeout(180);
      await page.screenshot({ path: out(`conv-popover-restart-${theme}.png`) });

      let calls = await getJSON(ctrlUrl + '/calls');
      assert.deepEqual(calls, [{ method: 'POST', path: '/agents/leadsess/settings', body: { conv_history_access: true } }],
        `${theme}: toggle posts only plain /settings without stopping`);

      // Click Stop now -> /agents/leadsess/stop with safe_only:true, restart banner clears.
      await stopNow.click();
      await banner.waitFor({ state: 'hidden', timeout: 10000 });
      calls = await getJSON(ctrlUrl + '/calls');
      assert.equal(calls.length, 2);
      assert.equal(calls[1].path, '/agents/leadsess/stop', `${theme}: Stop now posts /stop`);
      assert.deepEqual(calls[1].body, { safe_only: true }, `${theme}: idle Stop now sends safe_only:true`);

      // Close popover before moving to conv-reveal tests.
      await page.keyboard.press('Escape');
      await pop.waitFor({ state: 'hidden', timeout: 5000 }).catch(() => {});

      // 2. conv-reveal WS event (focus: false) while viewing /s/othersess:
      //    pulses leadsess in rail + mounts Jump badge without auto-navigating.
      await page.goto(base + '/s/othersess');
      if (await page.locator('#railbtn').isVisible() && !await page.locator('html').evaluate(el => el.classList.contains('rail-open'))) {
        await page.locator('#railbtn').click();
        await page.waitForTimeout(260);
      }
      await page.locator('#rail .rs[data-rid="leadsess"]').waitFor({ timeout: 10000 });
      await page.waitForTimeout(300);

      await postJSON(ctrlUrl + '/reveal', {
        replyId: 'leadsess',
        entryId: pagedEntry,
        focus: false,
        reason: 'found in search'
      });
      const targetRow = page.locator('#rail .rs[data-rid="leadsess"]');
      const jumpBadge = targetRow.locator('.conv-reveal-jump');
      await jumpBadge.waitFor({ state: 'visible', timeout: 10000 });
      assert.equal(await targetRow.evaluate(el => el.classList.contains('conv-reveal-pulse')), true, `${theme}: rail row pulsed`);
      assert.equal(await jumpBadge.getAttribute('href'), `/s/leadsess#entry=${encodeURIComponent(pagedEntry)}`);
      assert(new URL(page.url()).pathname === '/s/othersess', `${theme}: focus:false does not auto-navigate`);
      await page.screenshot({ path: out(`conv-reveal-rail-${theme}.png`) });

      // 3. Click Jump badge -> navigates to /s/leadsess#entry=<pagedEntry>,
      //    paginates /s/leadsess/history, scrolls into view & flashes target entry.
      await Promise.all([
        page.waitForURL(u => u.pathname === '/s/leadsess' && u.hash === `#entry=${encodeURIComponent(pagedEntry)}`, { timeout: 10000 }),
        jumpBadge.click()
      ]);
      const leadFrame = page.frameLocator('iframe.sframe.on');
      const pagedEl = leadFrame.locator(`#convo [data-entry="${pagedEntry}"]`);
      await pagedEl.waitFor({ state: 'visible', timeout: 15000 });
      // Wait for smooth scrollIntoView to bring the jumped entry into #convo's viewport.
      await page.waitForFunction((entryKey) => {
        const ifr = document.querySelector('iframe.sframe.on');
        if (!ifr || !ifr.contentDocument) return false;
        const convo = ifr.contentDocument.getElementById('convo');
        const el = convo && convo.querySelector(`[data-entry="${entryKey}"]`);
        if (!convo || !el) return false;
        const cr = convo.getBoundingClientRect(), er = el.getBoundingClientRect();
        return er.top >= cr.top - 4 && er.bottom <= cr.bottom + 4;
      }, pagedEntry, { timeout: 5000 });
      const flashed = await pagedEl.evaluate(el => el.classList.contains('conv-entry-flash'));
      assert.equal(flashed, true, `${theme}: jumped entry received conv-entry-flash highlight`);
      await page.screenshot({ path: out(`conv-entry-flash-${theme}.png`) });

      // 4. Not-found within 5 pages (250 entries): navigate to /s/leadsess#entry=<tooOldEntry>
      //    on a fresh page load so histBefore starts at the live 50-entry boundary.
      await page.goto(base + '/s/leadsess');
      const freshFrame = page.frameLocator('iframe.sframe.on');
      await freshFrame.locator('#convo [data-entry]').first().waitFor({ timeout: 10000 });
      // Scroll to bottom and record distance from bottom before jumping to an unreachably old entry.
      const beforeOffset = await freshFrame.locator('#convo').evaluate(el => {
        el.scrollTop = el.scrollHeight;
        return el.scrollHeight - el.scrollTop;
      });
      await page.evaluate(h => {
        const ifr = document.querySelector('iframe.sframe.on');
        ifr.contentWindow.location.hash = h;
      }, `#entry=${encodeURIComponent(tooOldEntry)}`);
      const notice = freshFrame.locator('.conv-entry-notice:not([hidden])');
      await notice.waitFor({ state: 'visible', timeout: 15000 });
      const noticeTxt = await notice.textContent();
      assert(noticeTxt.includes('항목을 찾을 수 없음'), `${theme}: Korean not-found notice rendered: ${noticeTxt}`);
      assert.equal(await freshFrame.locator(`#convo [data-entry="${tooOldEntry}"]`).count(), 0,
        `${theme}: entry older than 5 pages (250 entries) was not fetched`);
      const afterOffset = await freshFrame.locator('#convo').evaluate(el => el.scrollHeight - el.scrollTop);
      assert(Math.abs(afterOffset - beforeOffset) <= 2,
        `${theme}: visual scroll position preserved when not found (before=${beforeOffset}, after=${afterOffset})`);
      await page.screenshot({ path: out(`conv-entry-not-found-${theme}.png`) });

      // 5. conv-reveal with focus:true navigates an active/focused tab directly
      //    even when the rail drawer is closed.
      await page.goto(base + '/s/othersess');
      await page.locator('#rail .rs[data-rid="leadsess"]').waitFor({ state: 'attached', timeout: 10000 });
      await page.waitForTimeout(300);
      await Promise.all([
        page.waitForURL(u => u.pathname === '/s/leadsess' && u.hash === `#entry=${encodeURIComponent(liveEntry)}`, { timeout: 10000 }),
        postJSON(ctrlUrl + '/reveal', {
          replyId: 'leadsess',
          entryId: liveEntry,
          focus: true,
          reason: 'focus jump'
        })
      ]);
      const liveEl = page.frameLocator('iframe.sframe.on').locator(`#convo [data-entry="${liveEntry}"]`);
      await liveEl.waitFor({ state: 'visible', timeout: 10000 });

      assert.deepEqual(errors, [], `${theme}: zero page errors`);
      await context.close();
    }
    console.log('conv-history UI browser QA passed');
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exit(1); });
