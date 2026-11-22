// Run by TestSettingsComboboxBrowser against the real settings backend.
// Verifies the editable comboboxes on agents.tier and agent.tier_routing bundle modal,
// lazy cached /support/choices loading, keyboard/mouse UX, dynamic backend switching,
// read-only parity, top-layer dialog mounting, and dark/light visual captures.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [login, shots] = process.argv.slice(2);
const shot = name => shots ? path.join(shots, name) : undefined;

const CHOICES = {
  harnesses: [
    {
      harness: 'claude',
      available: true,
      models: [
        { id: 'default' },
        { id: 'sonnet' },
        { id: 'opus' },
        { id: 'haiku' },
        { id: 'claude-sonnet-4-6' },
        { id: 'claude-opus-4-6' }
      ],
      efforts: ['low', 'medium', 'high', 'xhigh', 'max']
    },
    {
      harness: 'codex',
      available: true,
      models: [
        { id: 'gpt-5.4' },
        { id: 'gpt-5.4-mini' },
        { id: 'gpt-5.3-codex' }
      ],
      efforts: ['none', 'minimal', 'low', 'medium', 'high', 'xhigh']
    },
    {
      harness: 'pi',
      available: true,
      models: [
        { id: 'anthropic/claude-sonnet-4-5', thinking: true },
        { id: 'anthropic/claude-opus-4-6', thinking: true },
        { id: 'openai/gpt-5.4', thinking: true },
        { id: 'google/gemini-2.5-flash', thinking: false }
      ],
      efforts: ['off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max']
    },
    {
      harness: 'agy',
      available: false,
      models: [],
      efforts: ['low', 'medium', 'high']
    }
  ],
  last: { harness: 'claude', model: '', effort: '' },
  tiers: []
};

(async () => {
  if (shots) fs.mkdirSync(shots, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 960 }, locale: 'en-US', reducedMotion: 'reduce' });
    const page = await context.newPage();
    page.setDefaultTimeout(8000);
    const errors = [];
    page.on('pageerror', e => { if (!/serviceWorker/.test(e.message)) errors.push('pageerror: ' + (e.stack || e.message)); });
    page.on('console', msg => {
      if (msg.type() === 'error') errors.push('console: ' + msg.text());
    });

    let choicesRequests = 0;
    await page.route('**/support/choices', async route => {
      choicesRequests++;
      await route.fulfill({ json: CHOICES, headers: { 'Cache-Control': 'no-store' } });
    });

    await page.goto(login);
    await page.waitForURL(url => url.pathname === '/settings' && !url.searchParams.has('login'));
    const base = new URL(page.url()).origin;
    const frame = () => page.frameLocator('iframe.sframe.on');

    async function setTheme(theme) {
      await context.addCookies([{ name: 'ws_dashboard_theme', value: theme, url: base }]);
      await page.goto(base + '/settings');
      await frame().locator('[data-settings-section=agents]').click();
      await frame().locator('tbody.tier-entry[data-settings-tier=medium]').waitFor();
      await frame().locator('[data-tier-routing] .tr-node').first().waitFor();
    }

    async function checkNoOverflow() {
      const overflow = await frame().locator('body').evaluate(() => ({
        docX: document.documentElement.scrollWidth > window.innerWidth,
        stageX: (document.querySelector('.scontent')?.scrollWidth || 0) > (document.querySelector('.scontent')?.clientWidth || 1) + 1
      }));
      assert.equal(overflow.docX, false, 'horizontal overflow in settings frame');
      assert.equal(overflow.stageX, false, 'horizontal overflow in settings content');
    }

    // 1. Dark theme: tier matrix model combobox open + filtering + custom + backend switch + bundle modal
    await setTheme('dark');
    assert.equal(choicesRequests, 0, 'lazy: /support/choices must not be fetched before interacting with combobox');

    const mediumRow = frame().locator('tbody.tier-entry[data-settings-tier=medium]');
    const mediumInputs = mediumRow.locator('input.scb-inp');
    const backendInp = mediumInputs.nth(0);
    const modelInp = mediumInputs.nth(1);
    const effortInp = mediumInputs.nth(2);

    assert.equal(await modelInp.getAttribute('role'), 'combobox');
    assert.equal(await modelInp.getAttribute('aria-autocomplete'), 'list');

    // Clicking chevron shows full unfiltered list even if a model is already filled
    const modelBtn = mediumRow.locator('.scb-btn').nth(1);
    await modelBtn.click();
    const popup = frame().locator('.scb-pop');
    await popup.waitFor({ state: 'visible' });
    assert.equal(choicesRequests, 1, 'fetched /support/choices lazily on first open');
    assert.equal(await modelInp.getAttribute('aria-expanded'), 'true');
    const initialBackend = (await backendInp.inputValue()).trim().toLowerCase();
    const initialHarness = CHOICES.harnesses.find(h => h.harness === initialBackend);
    assert.ok(initialHarness, 'expected initial backend in CHOICES: ' + initialBackend);
    const optCount = await popup.locator('.scb-opt').count();
    assert.equal(optCount, initialHarness.models.length);

    // Ensure popup is not clipped by .tier-scroll or .sgroup
    const clipCheck = await frame().locator('body').evaluate(() => {
      const pop = document.querySelector('.scb-pop').getBoundingClientRect();
      const tbl = document.querySelector('.tier-scroll').getBoundingClientRect();
      return { popTop: pop.top, popBottom: pop.bottom, popWidth: pop.width, tblBottom: tbl.bottom, inBody: document.querySelector('.scb-pop').parentElement === document.body };
    });
    assert.equal(clipCheck.inBody, true);
    assert(clipCheck.popWidth >= 176);

    await checkNoOverflow();
    if (shots) await page.screenshot({ path: shot('tier-matrix-model-open-dark.png') });

    // Switch backend to claude & verify live filtering
    await backendInp.fill('claude');
    await modelInp.fill('son');
    const filteredIds = await popup.locator('.scb-opt .scb-opt-id').allTextContents();
    assert.deepEqual(filteredIds, ['sonnet', 'claude-sonnet-4-6']);
    if (shots) await page.screenshot({ path: shot('tier-matrix-filter-custom-dark.png') });

    // Custom unlisted text stays and enables Save
    await modelInp.fill('custom-unlisted-model');
    assert.equal(await popup.count(), 0, 'unmatched custom text hides popup cleanly');
    assert.equal(await mediumRow.locator('.save-btn').isDisabled(), false, 'Save button enabled');

    // Dynamic backend switch: changing backend to pi immediately updates model & effort choices
    await backendInp.fill('pi');
    await modelBtn.click();
    await popup.waitFor({ state: 'visible' });
    const piModels = await popup.locator('.scb-opt .scb-opt-id').allTextContents();
    assert.deepEqual(piModels, CHOICES.harnesses[2].models.map(m => m.id));
    assert(await popup.locator('.scb-opt-tag').first().textContent() === 'thinking');

    // Keyboard ArrowDown + Enter selects raw id only (no 'thinking' tag)
    await modelInp.press('ArrowDown');
    assert.ok(await modelInp.getAttribute('aria-activedescendant'));
    await modelInp.press('Enter');
    assert.equal(await modelInp.inputValue(), 'anthropic/claude-sonnet-4-5');
    assert.equal(await popup.count(), 0);

    // Effort combobox reflects Pi efforts
    await mediumRow.locator('.scb-btn').nth(2).click();
    await popup.waitFor({ state: 'visible' });
    assert.deepEqual(await popup.locator('.scb-opt .scb-opt-id').allTextContents(), CHOICES.harnesses[2].efforts);
    await effortInp.press('Escape');
    assert.equal(await popup.count(), 0);
    assert.equal(choicesRequests, 1, 'zero repeated /support/choices requests across interactions');

    // Save works with custom/selected values
    const saveResp = page.waitForResponse(r => r.url().endsWith('/api/config/set') && r.request().method() === 'POST');
    await mediumRow.locator('.save-btn').click();
    assert.equal((await saveResp).status(), 200);

    // Bundle modal in dark theme: popup mounts inside <dialog> on top of top-layer modal!
    await frame().locator('[data-id="b:deep-design"]').click();
    const modal = frame().locator('dialog[data-tier-modal=bundle]');
    await modal.waitFor();
    const bundleModelInp = modal.locator('.tr-bundle-model');
    const bundleEffortInp = modal.locator('.tr-bundle-effort');
    assert.equal(await bundleModelInp.getAttribute('role'), 'combobox');
    assert.equal(await bundleEffortInp.getAttribute('role'), 'combobox');
    await modal.locator('.scb-btn').first().click();
    const dlgPopup = modal.locator('.scb-pop');
    await dlgPopup.waitFor({ state: 'visible' });

    const topLayer = await frame().locator('body').evaluate(() => {
      const dlg = document.querySelector('dialog[data-tier-modal=bundle]');
      const pop = dlg.querySelector('.scb-pop');
      const dlgRect = dlg.getBoundingClientRect();
      const popRect = pop.getBoundingClientRect();
      const inpRect = dlg.querySelector('.tr-bundle-model').getBoundingClientRect();
      return {
        mountedInDialog: pop.parentElement === dlg,
        position: getComputedStyle(pop).position,
        alignedWithInput: Math.abs(popRect.left - inpRect.left) <= 2,
        visibleAboveDialogBounds: popRect.width > 0 && popRect.height > 0 && dlgRect.width > 0
      };
    });
    assert.equal(topLayer.mountedInDialog, true);
    assert.equal(topLayer.position, 'fixed');
    assert.equal(topLayer.alignedWithInput, true);
    assert.equal(topLayer.visibleAboveDialogBounds, true);

    if (shots) await page.screenshot({ path: shot('bundle-modal-model-open-dark.png') });

    // Escape closes ONLY the popup, keeping the dialog open!
    await bundleModelInp.press('Escape');
    assert.equal(await dlgPopup.count(), 0);
    assert.equal(await modal.isVisible(), true, 'Escape closed only dropdown, dialog stays open');

    // Switching harness select updates bundle model & effort options
    await modal.locator('.tr-bundle-harness').selectOption('pi');
    await modal.locator('.scb-btn').first().click();
    await dlgPopup.waitFor({ state: 'visible' });
    assert.deepEqual(await dlgPopup.locator('.scb-opt .scb-opt-id').allTextContents(), CHOICES.harnesses[2].models.map(m => m.id));
    await dlgPopup.locator('.scb-opt').first().click();
    assert.equal(await bundleModelInp.inputValue(), 'anthropic/claude-sonnet-4-5');
    await modal.locator('.tr-modal-x').click();
    await modal.waitFor({ state: 'detached' });

    // 2. Light theme visual captures
    await setTheme('light');
    const lightMedium = frame().locator('tbody.tier-entry[data-settings-tier=medium]');
    await lightMedium.locator('.scb-btn').nth(1).click();
    await frame().locator('.scb-pop').waitFor({ state: 'visible' });
    await checkNoOverflow();
    if (shots) await page.screenshot({ path: shot('tier-matrix-model-open-light.png') });
    await lightMedium.locator('input.scb-inp').nth(1).press('Escape');

    await frame().locator('[data-id="b:deep-design"]').click();
    const lightModal = frame().locator('dialog[data-tier-modal=bundle]');
    await lightModal.waitFor();
    await lightModal.locator('.scb-btn').first().click();
    await lightModal.locator('.scb-pop').waitFor({ state: 'visible' });
    if (shots) await page.screenshot({ path: shot('bundle-modal-model-open-light.png') });
    await lightModal.locator('.tr-modal-x').click();

    // 3. Builtin (read-only) parity: inputs and chevron buttons disabled, never opens
    await frame().locator('[data-settings-scope=builtin]').click();
    await frame().locator('.builtin-banner').waitFor();
    await frame().locator('[data-settings-section=agents]').click();
    const roRow = frame().locator('tbody.tier-entry[data-settings-tier=medium]');
    assert.equal(await roRow.locator('input.scb-inp').nth(1).isDisabled(), true);
    assert.equal(await roRow.locator('.scb-btn').nth(1).isDisabled(), true);
    await roRow.locator('.scb-btn').nth(1).click({ force: true });
    assert.equal(await frame().locator('.scb-pop').count(), 0);

    // 4. Ultrawide and responsive .stage max-width cap, centering, and combobox popup alignment
    await context.addCookies([{ name: 'ws_dashboard_theme', value: 'dark', url: base }]);
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(base + '/settings');
    await frame().locator('[data-config-key="workflow.lang"]').waitFor();
    await assertStageGeometry(frame().locator('body'), 1440, 900, 'shell 1440x900 workflow');
    if (shots) await page.screenshot({ path: shot('settings-1440-dark.png') });

    await page.setViewportSize({ width: 2560, height: 1080 });
    await assertStageGeometry(frame().locator('body'), 2560, 1080, 'shell 2560x1080 workflow dark');
    if (shots) await page.screenshot({ path: shot('settings-2560-general-dark.png') });

    await context.addCookies([{ name: 'ws_dashboard_theme', value: 'light', url: base }]);
    await page.goto(base + '/settings');
    await frame().locator('[data-config-key="workflow.lang"]').waitFor();
    await assertStageGeometry(frame().locator('body'), 2560, 1080, 'shell 2560x1080 workflow light');
    if (shots) await page.screenshot({ path: shot('settings-2560-general-light.png') });

    await context.addCookies([{ name: 'ws_dashboard_theme', value: 'dark', url: base }]);
    await page.goto(base + '/settings');
    await frame().locator('[data-settings-section=agents]').click();
    await frame().locator('tbody.tier-entry[data-settings-tier=medium]').waitFor();

    async function assertStageGeometry(rootLocator, vw, vh, label) {
      const m = await rootLocator.evaluate(({ vw }) => {
        const stage = document.querySelector('[data-settings-view]');
        const rect = stage.getBoundingClientRect();
        const cs = getComputedStyle(stage);
        const scontent = document.querySelector('.scontent');
        const tierScroll = document.querySelector('.tier-scroll');
        return {
          width: rect.width,
          height: rect.height,
          left: rect.left,
          right: rect.right,
          maxWidth: cs.maxWidth,
          docOverflowX: document.documentElement.scrollWidth > window.innerWidth,
          contentOverflowX: !!scontent && scontent.scrollWidth > scontent.clientWidth + 1,
          tierOverflowX: !!tierScroll && tierScroll.scrollWidth > tierScroll.clientWidth + 1
        };
      }, { vw });
      assert.equal(m.maxWidth, '1280px', `${label}: computed max-width`);
      assert.equal(m.docOverflowX, false, `${label}: document horizontal overflow`);
      assert.equal(m.contentOverflowX, false, `${label}: .scontent horizontal overflow`);
      assert.equal(m.tierOverflowX, false, `${label}: .tier-scroll horizontal overflow`);
      if (vw >= 1600) {
        const col2Width = vw - 388;
        const expectedWidth = Math.min(1280, col2Width);
        const col2Center = (vw + 356) / 2;
        assert(Math.abs(m.width - expectedWidth) <= 2, `${label}: expected width ~${expectedWidth}, got ${m.width}`);
        assert(m.width <= 1280.5, `${label}: width ${m.width} exceeds 1280px`);
        assert(Math.abs((m.left + m.width / 2) - col2Center) <= 2, `${label}: expected centered in column 2 (${col2Center}), got ${m.left + m.width / 2}`);
        assert(m.height >= vh - 30, `${label}: expected full height (>=${vh - 30}), got ${m.height}`);
      } else {
        const expectedWidth = Math.min(1280, vw - 32);
        assert(Math.abs(m.width - expectedWidth) <= 2, `${label}: expected width ~${expectedWidth}, got ${m.width}`);
        assert(Math.abs((m.left + m.width / 2) - vw / 2) <= 2, `${label}: expected centered (${vw / 2}), got ${m.left + m.width / 2}`);
        assert(m.height >= vh - 80, `${label}: expected full height (>=${vh - 80}), got ${m.height}`);
      }
      return m;
    }

    for (const [vw, vh] of [[1280, 800], [1440, 900], [1600, 900], [2560, 1080], [3440, 1200]]) {
      await page.setViewportSize({ width: vw, height: vh });
      await assertStageGeometry(frame().locator('body'), vw, vh, `shell ${vw}x${vh}`);
      const shellOverflowX = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
      assert.equal(shellOverflowX, false, `shell outer horizontal overflow at ${vw}x${vh}`);
    }

    // Verify combobox popup alignment inside the 1280px capped stage at 2560x1080.
    await page.setViewportSize({ width: 2560, height: 1080 });
    const uwMedium = frame().locator('tbody.tier-entry[data-settings-tier=medium]');
    await uwMedium.locator('.scb-btn').nth(1).click();
    await frame().locator('.scb-pop').waitFor({ state: 'visible' });
    const uwPopAlign = await frame().locator('body').evaluate(() => {
      const stage = document.querySelector('[data-settings-view]').getBoundingClientRect();
      const inp = document.querySelectorAll('tbody.tier-entry[data-settings-tier=medium] .scb')[1].getBoundingClientRect();
      const pop = document.querySelector('.scb-pop').getBoundingClientRect();
      return {
        leftDelta: Math.abs(pop.left - inp.left),
        withinViewport: pop.right <= window.innerWidth && pop.left >= 0,
        withinStageX: pop.left >= stage.left && pop.right <= stage.right,
        belowOrAbove: pop.top >= inp.bottom || pop.bottom <= inp.top
      };
    });
    assert(uwPopAlign.leftDelta <= 2, 'expected combobox popup aligned at 2560x1080, delta=' + uwPopAlign.leftDelta);
    assert.equal(uwPopAlign.withinViewport, true);
    assert.equal(uwPopAlign.withinStageX, true);
    assert.equal(uwPopAlign.belowOrAbove, true);
    if (shots) await page.screenshot({ path: shot('settings-2560-tiers-dark.png') });
    await uwMedium.locator('input.scb-inp').nth(1).press('Escape');

    // Verify navigating to /settings?scope=global&frame=1 redirects to shell and keeps .stage centered/capped at 2560x1080.
    await page.goto(base + '/settings?scope=global&frame=1');
    await frame().locator('[data-settings-section=agents]').waitFor();
    await frame().locator('[data-settings-section=agents]').click();
    await frame().locator('tbody.tier-entry[data-settings-tier=medium]').waitFor();
    await assertStageGeometry(frame().locator('body'), 2560, 1080, 'frame-redirected 2560x1080');

    assert.deepEqual(errors, [], 'unexpected console/page errors: ' + errors.join('; '));
    console.log('settings combobox acceptance passed (choicesRequests=' + choicesRequests + ')');
  } finally {
    await browser.close();
  }
})();
