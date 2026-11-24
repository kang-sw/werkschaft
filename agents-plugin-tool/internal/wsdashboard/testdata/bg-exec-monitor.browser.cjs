'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [base, ctl, shotsDir] = process.argv.slice(2);
if (!base || !ctl) throw new Error('usage: node bg-exec-monitor.browser.cjs <base> <ctl> [shotsDir]');

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
  await view.waitForSelector('#composer[data-cexec="1"]', { state: 'attached', timeout: 15000 });
  return { page, view };
}

async function main() {
  if (shotsDir) fs.mkdirSync(shotsDir, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  try {
    const errors = [];
    const context = await browser.newContext({
      viewport: { width: 1280, height: 860 },
      locale: 'en-US',
      colorScheme: 'dark',
    });
    await context.addCookies([{ name: 'ws_dashboard_lang', value: 'en', url: base }]);

    const { page, view } = await openSessionFrame(context, `${base}/s/livesession`, errors);
    await view.waitForSelector('#bgbadge:not([hidden])', { timeout: 10000 });
    await view.waitForSelector('.xgroup .xchip', { timeout: 10000 });

    // 1. Verify #bgbadge displays agent exec count and user exec HUD stays separate.
    const badgeText = await view.locator('#bgbadge .blabel').textContent();
    assert.match(badgeText, /3/, `expected 3 background jobs on #bgbadge, got: ${badgeText}`);
    const badgeHasExec = await view.locator('#bgbadge').evaluate((el) => el.classList.contains('has-exec'));
    assert.equal(badgeHasExec, true, '#bgbadge should carry .has-exec');

    const userChip = view.locator('.xgroup .xchip[data-xid="exec_user_cmd"]');
    assert.equal(await userChip.count(), 1, 'expected user-origin chip in .xgroup');

    // 2. Zero /exec/bg/* polling while popover is closed.
    await sleep(1200);
    const closedStats = await ctlGet('/stats');
    assert.equal(closedStats.counts.bgList || 0, 0, 'zero bg/jobs requests while closed');
    assert.equal(closedStats.counts.bgOutput || 0, 0, 'zero bg/output requests while closed');

    // 3. Open C-variant popover by clicking #bgbadge.
    await view.locator('#bgbadge').click();
    await view.waitForSelector('#bgpop:not([hidden])', { timeout: 5000 });
    await view.waitForSelector('#bgpop .bgp-row', { timeout: 5000 });
    await view.waitForSelector('#bgpop .bgp-tail .bgp-line', { timeout: 5000 });

    const ariaExpanded = await view.locator('#bgbadge').getAttribute('aria-expanded');
    assert.equal(ariaExpanded, 'true', '#bgbadge aria-expanded should be true while open');

    // Verify split pill and 3-level hierarchy rows (excluding origin=user).
    const popText = await view.locator('#bgpop').textContent();
    assert.match(popText, /Self 1 · Desc 2/, `expected split pill "Self 1 · Desc 2" in header, got: ${popText}`);
    assert.ok(!popText.includes('npm run dev'), 'origin=user job must never appear in #bgpop');

    const rows = view.locator('#bgpop .bgp-row');
    assert.equal(await rows.count(), 4, 'expected 3 running + 1 finished agent jobs');

    const subs = await view.locator('#bgpop .bgp-sub').allTextContents();
    assert.ok(subs.some((t) => t.includes('L2') && t.includes('worker-alpha › verify-e2e')), `missing L2 breadcrumb: ${subs}`);
    assert.ok(subs.some((t) => t.includes('L1') && t.includes('worker-alpha')), `missing L1 breadcrumb: ${subs}`);
    assert.ok(subs.some((t) => t.includes('Self')), `missing Self breadcrumb: ${subs}`);

    // First row is newest running (L2 grandchild); verify stdout, stderr styling, and XSS escaping.
    const tailHTML = await view.locator('#bgpop .bgp-tail').innerHTML();
    assert.ok(!tailHTML.includes('<script>alert'), 'raw <script> must be escaped as text');
    const tailText = await view.locator('#bgpop .bgp-tail').textContent();
    assert.ok(tailText.includes('<script>alert(1)</script>'), 'literal script text should render safely');
    assert.equal(await view.locator('#bgpop .bgp-tail .bgp-line.is-err').count(), 1, 'stderr line should have .is-err');

    // Capture AFTER screenshot 1: dark desktop popover open with live tail + user HUD chip.
    if (shotsDir) {
      await page.screenshot({ path: path.join(shotsDir, 'after-dark-popover-open.png') });
    }

    // 4. Incremental tail append while open.
    await ctlPost('/append', { ID: 'exec_l2_run', Stream: 'stdout', Text: 'tail-increment-ok\n' });
    await view.waitForFunction(
      () => (document.querySelector('#bgpop .bgp-tail')?.textContent || '').includes('tail-increment-ok'),
      null,
      { timeout: 6000 },
    );

    // 5. Switch selected row to L1 and verify tail switches cleanly.
    await view.locator('#bgpop .bgp-row[data-bg-id="exec_l1_run"]').click();
    await view.waitForFunction(
      () => (document.querySelector('#bgpop .bgp-tail')?.textContent || '').includes('[vite] building'),
      null,
      { timeout: 5000 },
    );

    // 6. Narrow 480px viewport responsiveness check (zero horizontal overflow).
    await page.setViewportSize({ width: 480, height: 860 });
    await sleep(250);
    const narrowGeom = await view.evaluate(() => {
      const pop = document.getElementById('bgpop').getBoundingClientRect();
      return {
        left: pop.left,
        right: pop.right,
        width: pop.width,
        scrollWidth: document.documentElement.scrollWidth,
        innerWidth: window.innerWidth,
      };
    });
    assert.ok(narrowGeom.left >= 0, `popover left clipped: ${JSON.stringify(narrowGeom)}`);
    assert.ok(narrowGeom.right <= narrowGeom.innerWidth + 0.5, `popover right overflow: ${JSON.stringify(narrowGeom)}`);
    assert.ok(narrowGeom.scrollWidth <= narrowGeom.innerWidth, `document horizontal overflow: ${JSON.stringify(narrowGeom)}`);

    if (shotsDir) {
      await page.screenshot({ path: path.join(shotsDir, 'after-dark-narrow-480.png') });
    }

    // Restore desktop viewport for light theme + hold-to-stop + readonly tests.
    await page.setViewportSize({ width: 1280, height: 860 });
    await sleep(200);

    // 7. Light theme parity screenshot.
    await page.emulateMedia({ colorScheme: 'light' });
    await sleep(200);
    if (shotsDir) {
      await page.screenshot({ path: path.join(shotsDir, 'after-light-popover-open.png') });
    }
    await page.emulateMedia({ colorScheme: 'dark' });
    await sleep(150);

    // 8. Hold-to-stop via WsHoldConfirm (1.5s): short click does NOT stop; 1.75s hold DOES stop.
    const stopBtn = view.locator('#bgpop .bgp-stop');
    assert.equal(await stopBtn.count(), 1, 'hold-to-stop button must exist when can_control=true');
    await stopBtn.click();
    await sleep(250);
    const afterShort = await ctlGet('/stats');
    assert.equal(afterShort.counts.bgStop || 0, 0, 'short click must not stop job');

    const box = await stopBtn.boundingBox();
    assert.ok(box, 'stop button must have bounding box');
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await stopBtn.focus();
    await view.evaluate(() => {
      const pop = document.getElementById('bgpop');
      window.stopBeforePeer = pop.querySelector('.bgp-stop');
      window.rowsBeforePeer = [...pop.querySelectorAll('.bgp-row')];
      window.rowMutations = 0;
      new MutationObserver((records) => { window.rowMutations += records.length; })
        .observe(pop.querySelector('.bgp-list'), { childList: true, subtree: true });
    });
    await page.mouse.down();
    const peerList = page.waitForResponse((r) => r.url().endsWith('/exec/bg/jobs') && r.ok(), { timeout: 1250 });
    await ctlPost('/peer-job');
    await (await peerList).finished();
    await view.evaluate(() => new Promise(requestAnimationFrame));
    assert.equal(await view.evaluate(() => {
      const pop = document.getElementById('bgpop');
      return window.stopBeforePeer === pop.querySelector('.bgp-stop') &&
        window.stopBeforePeer === document.activeElement &&
        window.rowsBeforePeer.every((row, i) => row === pop.querySelectorAll('.bgp-row')[i]) &&
        window.rowMutations === 0;
    }), true, 'a peer tree revision must preserve the active Stop hold and leave its rows untouched');
    await sleep(1750);
    await page.mouse.up();

    await view.waitForFunction(
      () => !document.querySelector('#bgpop .bgp-stop'),
      null,
      { timeout: 5000 },
    );
    const afterHold = await ctlGet('/stats');
    assert.equal(afterHold.counts['bgStop:exec_l1_run'], 1, '1.5s hold must invoke POST /exec/bg/exec_l1_run/stop');
    await ctlPost('/syncsnap');

    // 9. Escape closes popover, and zero /exec/bg/* requests happen after closing.
    await view.locator('#bgpop').focus().catch(() => {});
    await page.keyboard.press('Escape');
    await view.waitForFunction(() => document.getElementById('bgpop')?.hidden === true, null, { timeout: 3000 });
    await ctlPost('/reset');
    await sleep(1500);
    const quietStats = await ctlGet('/stats');
    assert.equal(quietStats.counts.bgList || 0, 0, 'zero bg/jobs requests after close');
    assert.equal(quietStats.counts.bgOutput || 0, 0, 'zero bg/output requests after close');

    // 10. Read-only mode (can_control: false): hold-to-stop completely hidden.
    await context.route('**/exec/bg/jobs', async (route) => {
      const resp = await route.fetch();
      const body = await resp.json();
      body.can_control = false;
      await route.fulfill({ response: resp, body: JSON.stringify(body), contentType: 'application/json' });
    });
    await view.locator('#bgbadge').click();
    await view.waitForSelector('#bgpop:not([hidden])', { timeout: 5000 });
    await view.waitForSelector('#bgpop .bgp-row', { timeout: 5000 });
    await view.locator('#bgpop .bgp-row[data-bg-id="exec_l2_run"]').click();
    await view.waitForSelector('#bgpop .bgp-tail .bgp-line', { timeout: 5000 });
    assert.equal(await view.locator('#bgpop .bgp-stop').count(), 0, 'hold-to-stop must be completely hidden when can_control=false');
    const metaText = await view.locator('#bgpop .bgp-dock-meta').textContent();
    assert.match(metaText, /Read-only view/, `expected read-only pill in dock meta, got: ${metaText}`);

    if (shotsDir) {
      await page.screenshot({ path: path.join(shotsDir, 'after-dark-readonly.png') });
    }

    // 11. Cached-iframe shell hide (ws-page-visibility / shHidden=true while doc.hidden=false)
    //     pauses live-tail polling and 1s elapsed ticker, then resumes cleanly on show.
    await ctlPost('/reset');
    await page.evaluate(() => {
      const f = [...document.querySelectorAll('iframe')].find((el) => (el.src || '').includes('/s/livesession'));
      if (!f || !f.contentWindow) throw new Error('missing session iframe element');
      f.contentWindow.postMessage({ wsShell: 'shown', shown: false }, location.origin);
    });
    await sleep(250);
    await ctlPost('/reset');
    const elBefore = await view.locator('#bgpop .bgp-row[data-bg-id="exec_l2_run"] .bgp-el').textContent();
    await sleep(1600);
    const hiddenStats = await ctlGet('/stats');
    assert.equal(hiddenStats.counts.bgOutput || 0, 0, 'zero bg/output polls while cached iframe is hidden (shHidden=true)');
    const elAfter = await view.locator('#bgpop .bgp-row[data-bg-id="exec_l2_run"] .bgp-el').textContent();
    assert.equal(elAfter, elBefore, '1s elapsed ticker must pause while cached iframe is hidden');

    // Resume shell visibility -> polling resumes and picks up appended tail output.
    await ctlPost('/append', { ID: 'exec_l2_run', Stream: 'stdout', Text: 'resumed-after-shell-show\n' });
    await page.evaluate(() => {
      const f = [...document.querySelectorAll('iframe')].find((el) => (el.src || '').includes('/s/livesession'));
      f.contentWindow.postMessage({ wsShell: 'shown', shown: true }, location.origin);
    });
    await view.waitForFunction(
      () => (document.querySelector('#bgpop .bgp-tail')?.textContent || '').includes('resumed-after-shell-show'),
      null,
      { timeout: 5000 },
    );

    // 12. Evicted output footer formats written & trimmed sizes cleanly with no duplicate "bytes" unit.
    await context.route('**/exec/bg/exec_lead_run/output*', async (route) => {
      const resp = await route.fetch();
      const body = await resp.json();
      body.written = 2048;
      body.evicted = 512;
      await route.fulfill({ response: resp, body: JSON.stringify(body), contentType: 'application/json' });
    });
    await view.locator('#bgpop .bgp-row[data-bg-id="exec_lead_run"]').click();
    await view.waitForFunction(
      () => (document.querySelector('#bgpop .bgp-foot-r')?.textContent || '').includes('trimmed'),
      null,
      { timeout: 5000 },
    );
    const footText = await view.locator('#bgpop .bgp-foot-r').textContent();
    assert.match(footText, /2\.0 KiB \(512 B trimmed\)/, `unexpected evicted footer format: ${footText}`);
    assert.ok(!/bytes/i.test(footText), `evicted footer must not duplicate "bytes" unit: ${footText}`);

    // 13. HTTP 404 on tail output stops polling (no retries) and refreshes job list.
    await context.unroute('**/exec/bg/exec_lead_run/output*');
    let out404Count = 0;
    await context.route('**/exec/bg/exec_l2_run/output*', async (route) => {
      out404Count++;
      await route.fulfill({ status: 404, contentType: 'text/plain', body: 'not found' });
    });
    await ctlPost('/reset');
    await view.locator('#bgpop .bgp-row[data-bg-id="exec_l2_run"]').click();
    await sleep(1600);
    assert.equal(out404Count, 1, 'tail polling must stop immediately after HTTP 404 without retrying');
    const after404Stats = await ctlGet('/stats');
    assert.ok((after404Stats.counts.bgList || 0) >= 1, 'HTTP 404 on tail output should refresh job list');

    assert.deepEqual(errors, [], `unexpected browser errors: ${errors.join('; ')}`);
    console.log('bg-exec-monitor browser acceptance passed');
  } finally {
    await browser.close();
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
