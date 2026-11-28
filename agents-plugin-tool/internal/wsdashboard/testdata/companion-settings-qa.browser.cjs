// Run by TestCompanionSettingsVrmAndThemeBrowser.
// Verifies Dark & Light themes, borderless Material tonal hierarchy, VRM-active
// vs 2D-fallback appearance switching, disabled-but-editable state, inline 2-step
// delete, Advanced .zip accordion, and gear-to-settings flow. When qaShots is
// non-empty, writes all 4 high-DPI (2x) QA screenshots into qaShots.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, qaShots, userSource] = process.argv.slice(2);

async function checkLayoutAndBorders(frame, label) {
  const metrics = await frame.locator('[data-companion-settings-panel]').evaluate(panel => {
    const sc = document.querySelector('.scontent') || document.documentElement;
    const borderViolations = [];
    const selectors = ['.cs-group', '.cs-vrm-dropzone', '.cs-pack-row', '.cs-avatar-hero', '.cs-btn', '.cs-pill', '.cs-trust-notice', '.cs-source-item', '.cs-wardrobe-field', '.cs-adv-card'];
    for (const sel of selectors) {
      for (const el of panel.querySelectorAll(sel)) {
        const cs = getComputedStyle(el);
        for (const side of ['borderTopWidth', 'borderRightWidth', 'borderBottomWidth', 'borderLeftWidth']) {
          const w = parseFloat(cs[side]) || 0;
          const styleSide = side.replace('Width', 'Style');
          if (w > 0 && cs[styleSide] !== 'none') {
            borderViolations.push({ sel, side, width: cs[side], style: cs[styleSide] });
          }
        }
      }
    }
    return {
      scrollWidth: sc.scrollWidth,
      clientWidth: sc.clientWidth,
      overflow: sc.scrollWidth > sc.clientWidth + 1,
      borderViolations
    };
  });
  assert.equal(metrics.overflow, false, `${label}: horizontal overflow ${metrics.scrollWidth} > ${metrics.clientWidth}`);
  assert.deepEqual(metrics.borderViolations, [], `${label}: unexpected harsh borders on borderless tonal surfaces`);
}

async function captureSettingsFull(page, frame, outPath) {
  if (!qaShots) return;
  fs.mkdirSync(qaShots, { recursive: true });
  const modal = page.locator('#workflow-settings[open]');
  const iframeEl = page.locator('#workflow-settings[open] > iframe');
  const prevHeight = await modal.evaluate(el => el.style.height);
  const contentHeight = await frame.locator('.scontent').evaluate(el => {
    el.scrollTop = 0;
    // Chrome = everything in the stage that is not the scroller (.shd plus the
    // right-column .prec-strip / .filter-bar, which live in .smain).
    const stage = document.querySelector('[data-settings-view]');
    const header = stage ? Math.max(64, stage.getBoundingClientRect().height - el.clientHeight) : 64;
    return Math.ceil(el.scrollHeight + header + 24);
  });
  const targetH = Math.max(920, Math.min(2400, contentHeight));
  await page.setViewportSize({ width: 1440, height: targetH + 64 });
  await modal.evaluate((el, h) => { el.style.height = h + 'px'; el.style.maxHeight = 'none'; }, targetH);
  await page.waitForTimeout(160);
  await iframeEl.screenshot({ path: outPath });
  await modal.evaluate((el, h) => { el.style.height = h; el.style.maxHeight = ''; }, prevHeight);
  await page.setViewportSize({ width: 1440, height: 920 });
  await page.waitForTimeout(100);
}

(async () => {
  const browser = await chromium.launch({
    headless: true,
    args: ['--use-gl=angle', '--use-angle=swiftshader', '--enable-unsafe-swiftshader', '--ignore-gpu-blocklist']
  });
  try {
    const context = await browser.newContext({
      viewport: { width: 1440, height: 920 },
      deviceScaleFactor: qaShots ? 2 : 1,
      colorScheme: 'dark'
    });
    await context.addInitScript(() => {
      if (self !== top) return;
      const workers = navigator.serviceWorker;
      if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
    });
    const blocked = [];
    await context.route('**/*', route => {
      const url = new URL(route.request().url());
      if (url.origin !== base) { blocked.push(url.href); return route.abort(); }
      return route.continue();
    });
    const page = await context.newPage();
    const errors = [];
    const consoleErrors = [];
    const failedUrls = [];
    page.on('pageerror', e => errors.push(e.message));
    page.on('console', m => { if (m.type() === 'error') consoleErrors.push(m.text()); });
    page.on('response', r => { if (r.status() >= 400) failedUrls.push(r.status() + ' ' + r.url()); });

    // Load live session with dark theme explicitly applied.
    await page.goto(base + '/s/livesession');
    await page.frameLocator('.sframe.on').locator('#grid').waitFor();
    await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'dark'));

    // Navigate directly to /settings?section=companion inside shell modal to test Shot 1 (Dark + VRM + inline 2-step delete + Pack Sources).
    await page.evaluate(() => {
      document.querySelector('#rail [data-scope-entry="global"]')?.click();
    });
    await page.locator('#workflow-settings[open]').waitFor();
    const sframe = page.frameLocator('#workflow-settings[open] > iframe');
    await sframe.locator('[data-settings-section="companion"]').click();
    const panel = sframe.locator('[data-companion-settings-panel]');
    await panel.waitFor();
    await panel.locator('[data-companion-avatar-hero][data-active-renderer="vrm"]').waitFor();
    await panel.locator('[data-companion-wardrobe-vrm-note]').waitFor();
    assert.equal(await panel.locator('[data-companion-wardrobe-grid]').count(), 0, 'VRM active hides 2D wardrobe grid');
    assert.equal(await panel.locator('[data-companion-source-item]').count(), 2, 'Official + custom source rendered');
    assert.equal(await panel.locator('[data-companion-source-item="' + userSource + '"]').count(), 1);

    // Reveal inline 2-step delete state on alt-vrm (without actually deleting yet) and browse Official source.
    await panel.locator('[data-companion-pack-delete="alt-vrm"]').click();
    await panel.locator('[data-companion-pack-confirm="alt-vrm"]').waitFor();
    await panel.locator('[data-companion-source-fetch="official"]').click();
    await panel.locator('[data-companion-catalog-pack="default"]').waitFor();
    await page.waitForTimeout(150);

    await checkLayoutAndBorders(sframe, 'dark-vrm');
    if (qaShots) {
      await captureSettingsFull(page, sframe, path.join(qaShots, '01-companion-settings-dark-vrm.png'));
    }

    // Dismiss inline delete confirmation before switching to Light theme for Shot 2.
    await panel.locator('[data-companion-pack-confirm="alt-vrm"] button').nth(1).click();
    await page.evaluate(() => {
      document.documentElement.setAttribute('data-theme', 'light');
      const frame = document.querySelector('#workflow-settings[open] > iframe');
      frame?.contentWindow?.postMessage({ wsShell: 'theme', theme: 'light' }, location.origin);
    });
    await sframe.locator('html[data-theme="light"]').waitFor();
    await page.waitForTimeout(150);
    await checkLayoutAndBorders(sframe, 'light-vrm');
    if (qaShots) {
      await captureSettingsFull(page, sframe, path.join(qaShots, '02-companion-settings-light.png'));
    }

    // Shot 3: Switch back to dark, disable companion (companion_enabled=false), switch to 2D default pack,
    // verify disabled-but-editable banner + 2D wardrobe swatches + open Advanced .zip accordion.
    await page.evaluate(() => {
      document.documentElement.setAttribute('data-theme', 'dark');
      const frame = document.querySelector('#workflow-settings[open] > iframe');
      frame?.contentWindow?.postMessage({ wsShell: 'theme', theme: 'dark' }, location.origin);
    });
    await sframe.locator('html[data-theme="dark"]').waitFor();
    await panel.locator('[data-companion-switch-2d]').click();
    await panel.locator('[data-companion-wardrobe-grid] [data-config-key="dashboard.companion_preset"] select').waitFor();
    assert.equal(await panel.locator('[data-companion-wardrobe-vrm-note]').count(), 0, '2D active shows wardrobe grid');
    assert.equal(await panel.locator('[data-companion-wardrobe-grid] input[type="color"]').count(), 5, '5 palette swatches');

    const enabledRow = panel.locator('[data-config-key="dashboard.companion_enabled"]');
    await enabledRow.locator('button[role="switch"]').click();
    await enabledRow.locator('button[type="submit"]').click();
    await panel.locator('[data-companion-disabled-banner]').waitFor();
    assert.equal(await panel.locator('[data-companion-status-pill]').getAttribute('data-companion-status-pill'), 'off');
    assert.equal(await panel.locator('[data-companion-vrm-choose]').isDisabled(), false, 'VRM dropzone remains editable when disabled');

    await panel.locator('[data-companion-advanced-toggle]').click();
    await panel.locator('[data-companion-zip-choose]').waitFor();
    await page.waitForTimeout(150);
    await checkLayoutAndBorders(sframe, 'disabled-2d-advanced');
    if (qaShots) {
      await captureSettingsFull(page, sframe, path.join(qaShots, '03-companion-settings-disabled-2d.png'));
    }

    // Shot 4: Re-enable companion on 2D default pack, close modal, open via companion gear button on shell -> verify /settings?section=companion handoff.
    const enabledRow2 = panel.locator('[data-config-key="dashboard.companion_enabled"]');
    await enabledRow2.locator('button[role="switch"]').click();
    await enabledRow2.locator('button[type="submit"]').click();
    await panel.locator('[data-companion-status-pill="on"]').waitFor();
    await sframe.locator('[data-settings-close]').click();
    await page.locator('#workflow-settings').waitFor({ state: 'hidden' });

    await page.evaluate(() => {
      document.dispatchEvent(new CustomEvent('ws-companion-config', {
        detail: { enabled: true, director: 'rules', pack: 'default', preset: 'werkschaft-d1' }
      }));
    });
    await page.locator('.ws-companion-hit:not([hidden])').waitFor();
    await page.waitForTimeout(250);
    const hit = await page.locator('.ws-companion-hit').boundingBox();
    await page.mouse.move(hit.x + 4, hit.y + hit.height - 6);
    await page.waitForTimeout(100);
    await page.locator('.ws-companion-gear').click();
    await page.locator('#workflow-settings[open]').waitFor();
    const sframe2 = page.frameLocator('#workflow-settings[open] > iframe');
    await sframe2.locator('[data-companion-settings-panel]').waitFor();
    assert.equal(await page.locator('dialog.ws-companion-settings').count(), 0, 'legacy floating modal never opens');
    const activeTab = await sframe2.locator('[data-settings-section="companion"]').getAttribute('aria-current');
    assert.equal(activeTab, 'true', 'companion nav tab is active on gear handoff');
    await page.waitForTimeout(150);
    if (qaShots) {
      await page.screenshot({ path: path.join(qaShots, '04-gear-to-settings-flow.png') });
    }

    const appFailedUrls = failedUrls.filter(u => !u.includes('/tracker/stores'));
    assert.deepEqual(blocked, [], 'no external requests left the daemon');
    assert.deepEqual(errors, [], 'no page errors');
    assert.deepEqual(appFailedUrls, [], 'no failed companion/settings/asset requests');
    console.log('companion settings VRM/theme QA: ok', JSON.stringify({ qaShots: qaShots || null }));
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exit(1); });
