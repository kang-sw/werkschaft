'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [base, ctl, shotsDir, lanToken] = process.argv.slice(2);
if (!base || !ctl) throw new Error('usage: node exec-completion-row.browser.cjs <base> <ctl> [shotsDir] [lanToken]');

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function ctlGet(p) {
  const r = await fetch(ctl + p);
  if (!r.ok) throw new Error(`${p} -> ${r.status}`);
  return r.json();
}
async function ctlPost(p, body) {
  const r = await fetch(ctl + p, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body || {}),
  });
  if (!r.ok) throw new Error(`${p} -> ${r.status}`);
  return r.json();
}

async function openSessionFrame(context, url, errors) {
  const page = await context.newPage();
  page.on('pageerror', (e) => errors.push('pageerror: ' + e.message));
  page.on('console', (m) => {
    if (m.type() === 'error' && !/status of (404|503)/.test(m.text())) {
      errors.push('console: ' + m.text());
    }
  });
  await page.goto(url);
  let view = null;
  for (let i = 0; i < 100 && !view; i++) {
    view = page.frames().find((x) => x !== page.mainFrame() && x.url().includes('/s/livesession'));
    if (!view) await sleep(150);
  }
  assert.ok(view, 'expected session iframe for /s/livesession');
  await view.waitForSelector('details.exec-notice', { state: 'attached', timeout: 15000 });
  return { page, view };
}

async function main() {
  if (shotsDir) fs.mkdirSync(shotsDir, { recursive: true });
  const browser = await chromium.launch({
    headless: true,
    args: ['--host-resolver-rules=MAP dashboard-lan.test 127.0.0.1', '--no-proxy-server'],
  });
  try {
    // 1. LAN read-only view: compact rows expand inline, View output button stays hidden.
    const lan = await browser.newContext({ viewport: { width: 1280, height: 860 }, locale: 'en-US' });
    const lanURL = new URL(base);
    lanURL.hostname = 'dashboard-lan.test';
    await lan.addCookies([
      { name: 'ws_dashboard_token', value: lanToken, url: lanURL.origin },
      { name: 'ws_dashboard_lang', value: 'en', url: lanURL.origin },
    ]);
    const lanErrors = [];
    const { view: lanView } = await openSessionFrame(lan, lanURL.origin + '/s/livesession', lanErrors);
    assert.equal(await lanView.locator('details.exec-notice').count(), 4, 'LAN renders all 4 exec rows');
    await lanView.locator('details.exec-notice[data-exec-id="exec_done_ok"]').first().locator('summary').click();
    assert.equal(await lanView.locator('details.exec-notice[data-exec-id="exec_done_ok"]').first().locator('.exec-view-out').isVisible(), false, 'View output button must be hidden in LAN read-only view');
    await lan.close();
    assert.deepEqual(lanErrors, [], `LAN errors: ${lanErrors.join('; ')}`);

    // 2. Localhost live session (0 running jobs -> #bgbadge is hidden).
    const errors = [];
    const context = await browser.newContext({
      viewport: { width: 1280, height: 860 },
      locale: 'en-US',
      colorScheme: 'dark',
    });
    await context.addCookies([{ name: 'ws_dashboard_lang', value: 'en', url: base }]);
    const { page, view } = await openSessionFrame(context, `${base}/s/livesession`, errors);

    // Only the 2 fixture-seeded + 1 real user prompt render as .msg.user; all 4 exec notices (3 RingWake + 1 legacy RingUser) are compact rows.
    assert.equal(await view.locator('.msg.user').count(), 3, '2 seeded + 1 real user prompt should render as .msg.user (0 exec notices)');
    const userTexts = await view.locator('.msg.user').allTextContents();
    assert.ok(userTexts.every((t) => !t.includes('[ws exec]')), 'no .msg.user bubble may contain [ws exec] notice');
    assert.equal(await view.locator('details.exec-notice').count(), 4, 'all 4 exec notices should render as details.exec-notice');
    assert.equal(await view.locator('details.exec-notice[data-wake-cause="background_exit"]').count(), 4, 'all 4 exec rows should carry data-wake-cause=background_exit');
    assert.equal(await view.locator('details.exec-notice.fail').count(), 2, 'failed exec rows should carry .fail');
    assert.ok(await view.locator('.msg.user [data-act="rewind"]').count() >= 1, 'real .msg.user bubbles must render rewind controls (non-vacuous selector check)');
    assert.ok(await view.locator('.msg.user [data-act="edit"]').count() >= 1, 'real .msg.user bubbles must render edit controls (non-vacuous selector check)');
    assert.equal(await view.locator('details.exec-notice [data-act="rewind"], details.exec-notice [data-act="edit"], details.exec-notice .d-acts').count(), 0, 'exec rows must never have rewind/edit/msgacts buttons');

    // Collapsed row is compact single-line (height < 42px).
    const rowBox = await view.locator('details.exec-notice[data-exec-id="exec_done_ok"]').first().boundingBox();
    assert.ok(rowBox && rowBox.height > 0 && rowBox.height < 42, `collapsed exec row should be single-line compact, got height=${rowBox && rowBox.height}`);

    // #bgbadge must be hidden (0 running jobs).
    assert.equal(await view.locator('#bgbadge').isVisible(), false, '#bgbadge should be hidden when 0 jobs are running');

    // Expand ok row and verify zero /exec/bg/* requests while monitor is closed.
    const okRow = view.locator('details.exec-notice[data-exec-id="exec_done_ok"]').first();
    await okRow.locator('summary').click();
    await okRow.locator('.wake-body').waitFor({ state: 'visible', timeout: 5000 });
    assert.match(await okRow.locator('.wake-body').textContent(), /PASS: TestExecEnvelope/);
    assert.equal(await okRow.locator('.exec-view-out').isVisible(), true, 'View output button should be visible on live localhost');

    await sleep(600);
    const closedStats = await ctlGet('/stats');
    assert.equal(closedStats.counts.bgList || 0, 0, 'zero bg/jobs requests while monitor is closed');
    assert.equal(closedStats.counts.bgOutput || 0, 0, 'zero bg/output requests while monitor is closed');

    // 3. Click "View output" while #bgbadge is hidden -> opens #bgpop strictly on-screen, selects exec_done_ok, loads tail.
    await okRow.locator('.exec-view-out').click();
    await view.waitForSelector('#bgpop:not([hidden])', { timeout: 5000 });
    await view.waitForFunction(
      () => (document.querySelector('#bgpop .bgp-tail')?.textContent || '').includes('PASS: TestExecEnvelope'),
      null,
      { timeout: 5000 },
    );
    const popGeom = await view.evaluate(() => {
      const r = document.getElementById('bgpop').getBoundingClientRect();
      return { top: r.top, left: r.left, bottom: r.bottom, right: r.right, vw: window.innerWidth, vh: window.innerHeight };
    });
    assert.ok(popGeom.top >= 0 && popGeom.left >= 0 && popGeom.bottom <= popGeom.vh + 1 && popGeom.right <= popGeom.vw + 1,
      `#bgpop must stay strictly on-screen when #bgbadge is hidden: ${JSON.stringify(popGeom)}`);
    const selectedId = await view.locator('#bgpop .bgp-row.is-selected').getAttribute('data-bg-id');
    assert.equal(selectedId, 'exec_done_ok', 'View output should select exec_done_ok');

    if (shotsDir) {
      await page.screenshot({ path: path.join(shotsDir, 'exec-row-jump-open.png') });
    }

    // 4. While #bgpop is open, expanding an expired row immediately hides its View output button and reveals the expired note.
    //    Use DOM summary.click() so #bgpop's right-docked overlay in a short pane does not block pointer hit-testing.
    const expRow2 = view.locator('details.exec-notice[data-exec-id="exec_expired_two"]');
    await expRow2.locator('summary').evaluate((el) => el.click());
    await expRow2.locator('.exec-row-expired').waitFor({ state: 'visible', timeout: 5000 });
    assert.equal(await expRow2.locator('.exec-view-out').isVisible(), false, 'expired row expanded while monitor open should hide View output');
    assert.equal(await view.locator('#bgpop').isVisible(), true, '#bgpop stays open while inspecting an exec row');

    // 5. Close #bgpop, then click View output on the other expired row -> #bgpop opens, stays open, button hides, note shows.
    await page.keyboard.press('Escape');
    await view.waitForFunction(() => document.getElementById('bgpop')?.hidden === true, null, { timeout: 3000 });
    const expRow1 = view.locator('details.exec-notice[data-exec-id="exec_expired_fail"]');
    await expRow1.locator('summary').click();
    assert.equal(await expRow1.locator('.exec-view-out').isVisible(), true, 'before clicking, closed monitor does not probe retention');
    await expRow1.locator('.exec-view-out').click();
    await view.waitForSelector('#bgpop:not([hidden])', { timeout: 5000 });
    await expRow1.locator('.exec-row-expired').waitFor({ state: 'visible', timeout: 5000 });
    assert.equal(await expRow1.locator('.exec-view-out').isVisible(), false, 'clicking View output on expired job must hide its button');
    assert.equal(await view.locator('#bgpop').isVisible(), true, '#bgpop stays open after expired jump');

    // 6. Add a running job and open a fresh session page (empty bgSt.selId,
    //    so default auto-selection would pick the running job exec_run_other),
    //    then jump from the legacy RingUser exec_done_ok row -> bgPendingSel overrides first-running auto-selection!
    await page.keyboard.press('Escape');
    await view.waitForFunction(() => document.getElementById('bgpop')?.hidden === true, null, { timeout: 3000 });
    await page.close();
    await ctlPost('/add-running');
    const { page: page2, view: view2 } = await openSessionFrame(context, `${base}/s/livesession`, errors);

    const legacyRow = view2.locator('details.exec-notice[data-exec-id="exec_done_ok"]').nth(1);
    await legacyRow.locator('summary').click();
    await legacyRow.locator('.exec-view-out').click();
    await view2.waitForSelector('#bgpop:not([hidden])', { timeout: 5000 });
    await view2.waitForFunction(
      () => document.querySelectorAll('#bgpop .bgp-row').length === 2 &&
        document.querySelector('#bgpop .bgp-row.is-selected')?.getAttribute('data-bg-id') === 'exec_done_ok',
      null,
      { timeout: 5000 },
    );
    assert.equal(await view2.locator('#bgpop .bgp-row').count(), 2, 'monitor should show both running and finished job');
    assert.equal(await view2.locator('#bgpop .bgp-row.is-selected').getAttribute('data-bg-id'), 'exec_done_ok',
      'bgPendingSel must override default first-running selection');

    // 7. Close #bgpop and verify zero /exec/bg/* polling after close.
    await page2.keyboard.press('Escape');
    await view2.waitForFunction(() => document.getElementById('bgpop')?.hidden === true, null, { timeout: 3000 });
    await ctlPost('/reset');
    await sleep(1200);
    const quietStats = await ctlGet('/stats');
    assert.equal(quietStats.counts.bgList || 0, 0, 'zero bg/jobs requests after close');
    assert.equal(quietStats.counts.bgOutput || 0, 0, 'zero bg/output requests after close');

    assert.deepEqual(errors, [], `unexpected browser errors: ${errors.join('; ')}`);
    console.log('exec-completion-row browser acceptance passed');
  } finally {
    await browser.close();
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
