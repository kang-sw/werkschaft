// Run by TestBoardToolbarCollapseBrowser; isolated Chromium against httptest.
const assert = require('node:assert/strict');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [base, captures] = process.argv.slice(2);
const shotWidths = [1120, 1003, 850, 803, 718];
const reportWidths = [1600, 1440, 1280, 1200, 1120, 1003, 1002, 900, 850, 803, 802, 718, 600, 540, 539, 480, 360];

async function settle(page) {
  await page.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(() => requestAnimationFrame(r)))));
}

async function resetScroll(page) {
  await page.evaluate(() => {
    document.scrollingElement.scrollTop = 0;
    const b = document.querySelector('#bcol');
    if (b) {
      b.scrollTop = 0;
      b.dispatchEvent(new Event('scroll', { bubbles: true }));
    }
  });
  await settle(page);
}

async function measureToolbar(page) {
  return page.locator('.board-toolbar').evaluate(toolbar => {
    const rect = n => {
      const r = n.getBoundingClientRect();
      return { x: r.x, y: r.y, width: r.width, height: r.height, right: r.right, bottom: r.bottom };
    };
    const cs = getComputedStyle(toolbar);
    const marginBottom = parseFloat(cs.marginBottom) || 0;
    const face = toolbar.firstElementChild;
    const row = face.firstElementChild;
    const overview = row.querySelector('.board-overview');
    const overviewRect = overview ? rect(overview) : null;
    const actButtons = [...row.querySelectorAll('.board-acts .board-action')]
      .filter(n => n.getClientRects().length > 0)
      .map(n => ({
        cls: n.className,
        rect: rect(n),
        clipped: n.scrollWidth > n.clientWidth + 1
      }));
    const actions = [
      ...(overview ? [{ cls: overview.className, rect: overviewRect, clipped: overview.scrollWidth > overview.clientWidth + 1 }] : []),
      ...actButtons
    ];
    const filter = row.querySelector('.board-state-filter');
    const inline = row.querySelector('.board-state-inline');
    const meta = row.querySelector('.board-state-meta');
    const pipeline = row.querySelector('.board-state-pipeline');
    const trigger = row.querySelector('.board-state-trigger');
    const radios = [...row.querySelectorAll('.board-state-radio')];
    const isDropdown = !!(trigger && !trigger.hidden);
    const filterLabelsVisible = inline && !inline.hidden
      ? radios.filter(n => n.querySelector('.board-state-label') && n.querySelector('.board-state-label').getClientRects().length > 0).length
      : 0;
    const filterVisible = !!(filter && filter.getClientRects().length);
    const faceRect = rect(face);

    // Row 1: .board-acts buttons + (.board-state-meta when inline, else .board-state-trigger).
    const row1Boxes = [...actButtons.map(a => a.rect)];
    if (!isDropdown && meta && meta.getClientRects().length) {
      row1Boxes.push(rect(meta));
    } else if (isDropdown && trigger && trigger.getClientRects().length) {
      row1Boxes.push(rect(trigger));
    }
    const row1Centers = row1Boxes.map(r => r.y + r.height / 2);
    const row1DeltaY = row1Centers.length ? Math.max(...row1Centers) - Math.min(...row1Centers) : 0;
    const row1Sorted = row1Boxes.slice().sort((a, b) => a.x - b.x);
    let maxOverlap = 0;
    for (let i = 1; i < row1Sorted.length; i++) {
      maxOverlap = Math.max(maxOverlap, row1Sorted[i - 1].right - row1Sorted[i].x);
    }
    if (overviewRect && row1Sorted.length) {
      maxOverlap = Math.max(maxOverlap, overviewRect.right - row1Sorted[0].x);
    }

    // Row 2: interlocking powerline chevrons (only when inline).
    let row2DeltaY = 0;
    const segRects = !isDropdown && pipeline && pipeline.getClientRects().length
      ? [...pipeline.querySelectorAll('.board-state-seg')].map(n => rect(n))
      : [];
    if (segRects.length) {
      const segCenters = segRects.map(r => r.y + r.height / 2);
      row2DeltaY = Math.max(...segCenters) - Math.min(...segCenters);
      if (overviewRect) {
        maxOverlap = Math.max(maxOverlap, overviewRect.right - segRects[0].x);
      }
    }

    const allRights = [
      ...actions.map(a => a.rect.right),
      ...row1Boxes.map(r => r.right),
      ...segRects.map(r => r.right)
    ];
    const overflowRight = (allRights.length ? Math.max(...allRights) : 0) - faceRect.right;
    const clippedAction = actions.some(a => a.clipped);
    const clippedRadio = !isDropdown && radios.some(n => n.scrollWidth > n.clientWidth + 1);
    const clippedTrigger = isDropdown && trigger && trigger.scrollWidth > trigger.clientWidth + 1;
    const wrapped = row1DeltaY > 1.5 || row2DeltaY > 1.5;
    return {
      outerWidth: Math.round(rect(toolbar).width * 10) / 10,
      faceWidth: Math.round(faceRect.width * 10) / 10,
      faceHeight: Math.round(faceRect.height * 10) / 10,
      slotHeight: Math.round((rect(toolbar).height + marginBottom) * 10) / 10,
      overviewRect: overviewRect ? { width: Math.round(overviewRect.width * 10) / 10, height: Math.round(overviewRect.height * 10) / 10 } : null,
      filterWidth: filterVisible ? Math.round(rect(filter).width * 10) / 10 : 0,
      mode: toolbar.getAttribute('data-toolbar-fit') || 'full',
      actionCount: actions.length,
      filterMode: isDropdown ? 'dropdown' : 'inline',
      filterLabelsVisible,
      wrapped,
      row1DeltaY: Math.round(row1DeltaY * 10) / 10,
      row2DeltaY: Math.round(row2DeltaY * 10) / 10,
      maxOverlap: Math.round(maxOverlap * 10) / 10,
      overflowRight: Math.round(overflowRight * 10) / 10,
      clippedAction,
      clippedRadio,
      clippedTrigger,
      floating: toolbar.hasAttribute('data-floating')
    };
  });
}

async function setExactToolbarWidth(page, target) {
  // Adjust viewport so .board-toolbar has exact outer width = target.
  // Layout is always auto (split 5fr/7fr from 1100px, tabs below 1100px), and
  // above 1600px the docked rail (~288px) and Band 4 gallery (~540px) join, so
  // iterate with slope estimation to converge cleanly across both regimes.
  const startVW = target >= 1100 ? Math.round(target / 0.58) + 880 : target + 40;
  await page.setViewportSize({ width: startVW, height: 800 });
  let prevVW = null, prevW = null;
  for (let i = 0; i < 16; i++) {
    await settle(page);
    if (!(await page.locator('#bcol').isVisible())) {
      await page.locator('[data-tab="board"]').first().click();
      await settle(page);
    }
    const actual = (await page.locator('.board-toolbar').boundingBox()).width;
    if (Math.abs(actual - target) < 0.25) return actual;
    const cur = page.viewportSize().width;
    let slope = 1;
    if (prevVW !== null && cur !== prevVW && Math.abs(actual - prevW) > 0.5) {
      slope = Math.max(0.35, Math.min(1.2, (actual - prevW) / (cur - prevVW)));
    }
    prevVW = cur;
    prevW = actual;
    const next = Math.max(320, Math.round(cur + (target - actual) / slope));
    if (next === cur) return actual;
    await page.setViewportSize({ width: next, height: 800 });
  }
  await settle(page);
  return (await page.locator('.board-toolbar').boundingBox()).width;
}

function assertSingleRow(m, label) {
  assert.equal(m.wrapped, false, `${label} wrapped (row1DeltaY=${m.row1DeltaY}, row2DeltaY=${m.row2DeltaY}, mode=${m.mode}, filter=${m.filterMode})`);
  assert(m.maxOverlap <= 0.5, `${label} overlap=${m.maxOverlap}`);
  assert(m.overflowRight <= 0.5, `${label} overflowRight=${m.overflowRight}`);
  assert.equal(m.clippedAction, false, `${label} clippedAction`);
  assert.equal(m.clippedRadio, false, `${label} clippedRadio`);
  assert.equal(m.clippedTrigger, false, `${label} clippedTrigger`);
  assert(m.faceHeight <= 51, `${label} faceHeight=${m.faceHeight} exceeds compact 2-row budget`);
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    // 1. Exact toolbar width sweep (1600 down to 360) for Korean and English.
    for (const lang of ['ko', 'en']) {
      const context = await browser.newContext({ viewport: { width: 2000, height: 800 }, locale: lang === 'ko' ? 'ko-KR' : 'en-US', colorScheme: 'light', reducedMotion: 'reduce' });
      await context.route('**/*', route => {
        const u = new URL(route.request().url());
        if (u.origin === new URL(base).origin) return route.continue();
        return route.abort();
      });
      const page = await context.newPage();
      const pageErrors = [];
      page.on('pageerror', err => pageErrors.push(err.message));
      await page.addInitScript(() => {
        try { localStorage.setItem('ws.dashboard.layout', 'tabs'); } catch (_) {}
      });
      await page.goto(`${base}/s/tbcollapse?lang=${lang}&theme=light`, { waitUntil: 'domcontentloaded' });
      await page.locator('.board-toolbar').waitFor({ state: 'attached', timeout: 10000 });
      if (!(await page.locator('#bcol').isVisible())) {
        await page.locator('[data-tab="board"]').first().click();
      }
      // Allow the exact-width sweep to reach full mode past the 1600px
      // docked-rail stage cap (--pbw); adding the 9th filter button ("Tickets")
      // pushes English full-mode past 1600px. Default-layout sweep below keeps
      // untouched production CSS.
      const wideTarget = lang === 'en' ? 1750 : 1600;
      await page.evaluate(() => { document.documentElement.style.setProperty('--pbw', '1900px'); });
      await resetScroll(page);

      // Verify wide width: full compact 2-row bar visible with 50px face / 56px slot and 38x44 overview anchor.
      const actualWide = await setExactToolbarWidth(page, wideTarget);
      assert(Math.abs(actualWide - wideTarget) < 1, `${lang} reached ${wideTarget}px toolbar width (got ${actualWide})`);
      await resetScroll(page);
      const wide = await measureToolbar(page);
      assertSingleRow(wide, `${lang} w=${wideTarget}`);
      assert.equal(wide.mode, 'full', `${lang} ${wideTarget} must be in full mode`);
      assert.equal(wide.filterMode, 'inline', `${lang} ${wideTarget} must use inline filter`);
      assert.equal(wide.filterLabelsVisible, 10, `${lang} ${wideTarget} must show all 10 filter labels`);
      assert.equal(wide.faceHeight, 50, `${lang} ${wideTarget} faceHeight must be 50px`);
      assert.equal(wide.slotHeight, 56, `${lang} ${wideTarget} slotHeight must be 56px`);
      assert.deepEqual(wide.overviewRect, { width: 38, height: 44 }, `${lang} ${wideTarget} overview anchor must be 38x44`);

      if (lang === 'ko') {
        for (const selector of ['.board-state-chip[data-filter="asks"]', '.board-state-seg[data-filter="waiting"]']) {
          await page.locator('.board-files').click();
          await page.locator('#toolbar-files[data-max]').waitFor();
          const box = await page.locator(selector).boundingBox();
          const x = box.x + box.width / 2, y = box.y + box.height / 2;
          const hit = await page.evaluate(({ x, y }) => document.elementFromPoint(x, y).matches('.bc-scrim'), { x, y });
          await page.mouse.click(x, y);
          assert.equal(hit, true, selector + ' is covered by the Files scrim');
          assert.equal(await page.locator('#toolbar-files').isVisible(), false, 'the outside click dismisses Files');
          assert.equal(await page.locator('.board-state-radio[aria-checked="true"]').getAttribute('data-filter'), 'all');
        }

        await setExactToolbarWidth(page, 400);
        await page.locator('.board-overview').click();
        await page.locator('html[data-overview]').waitFor({ state: 'attached' });
        for (const mode of ['dropdown', 'tight']) {
          const toolbar = page.locator('.board-toolbar');
          if (mode === 'tight') await toolbar.evaluate(el => { el.style.maxWidth = '175px'; });
          await resetScroll(page);
          const resting = await measureToolbar(page);
          assert.equal(resting.mode, mode);
          assert.equal(resting.floating, false);
          await page.locator('#bcol').evaluate(el => { el.scrollTop = 300; });
          await settle(page);
          const floating = await measureToolbar(page);
          assert.equal(floating.floating, true, mode + ' reaches the sticky boundary in Overview');
          assert.equal(floating.slotHeight, resting.slotHeight, mode + ' reserves the same in-flow height');
        }
        await page.locator('.board-toolbar').evaluate(el => { el.style.removeProperty('max-width'); });
        await page.locator('.board-overview').click();
        await page.locator('html[data-overview]').waitFor({ state: 'detached' });
      }

      // Verify 680px (and 500px for Korean) stays inline with zero overflow/clipping.
      for (const target of (lang === 'ko' ? [680, 500] : [680])) {
        await setExactToolbarWidth(page, target);
        await resetScroll(page);
        const m = await measureToolbar(page);
        assertSingleRow(m, `${lang} compact-2row w=${target}`);
        assert.equal(m.filterMode, 'inline', `${lang} w=${target} must stay inline in 2-row layout`);
        assert.equal(m.slotHeight, 56, `${lang} w=${target} slotHeight must stay 56px`);
      }

      // Capture requested after screenshots in Korean light theme.
      if (captures && lang === 'ko') {
        for (const w of shotWidths) {
          await setExactToolbarWidth(page, w);
          await resetScroll(page);
          const m = await measureToolbar(page);
          assertSingleRow(m, `ko shot w=${w}`);
          const file = path.join(captures, `after-${w}.png`);
          await page.screenshot({ path: file, clip: { x: 0, y: 0, width: page.viewportSize().width, height: 220 } });
          console.log(`SHOT after-${w}.png:`, JSON.stringify(m));
        }
      }

      // Sweep down to 360 in 5px steps.
      const transitions = [];
      let prevSig = '';
      for (let w = wideTarget; w >= 360; w -= 5) {
        await setExactToolbarWidth(page, w);
        const m = await measureToolbar(page);
        assertSingleRow(m, `${lang} w=${w}`);
        const sig = `${m.mode}|${m.filterMode}|${m.filterLabelsVisible}`;
        if (sig !== prevSig) {
          transitions.push({ w, sig, m });
          prevSig = sig;
        }
      }

      // Sweep +-6px around every transition in 1px steps (and key widths).
      const checked1px = new Set();
      for (const t of transitions) {
        const hi = Math.min(wideTarget, t.w + 6);
        const lo = Math.max(360, t.w - 6);
        for (let w = hi; w >= lo; w--) {
          if (checked1px.has(w)) continue;
          checked1px.add(w);
          await setExactToolbarWidth(page, w);
          const m = await measureToolbar(page);
          assertSingleRow(m, `${lang} boundary w=${w}`);
        }
      }

      // Print summary width table for report verification.
      for (const w of reportWidths) {
        await setExactToolbarWidth(page, w);
        await resetScroll(page);
        const m = await measureToolbar(page);
        assertSingleRow(m, `${lang} report w=${w}`);
        console.log(`AFTER ${lang} w=${w}: mode=${m.mode} actions=${m.actionCount} filter=${m.filterMode}(${m.filterLabelsVisible} labels, ${m.filterWidth}px) faceH=${m.faceHeight} slotH=${m.slotHeight} wrapped=${m.wrapped}`);
      }

      // Verify longest filter label selected ("awaiting-confirmation") stays single-row down to 360px.
      await setExactToolbarWidth(page, wideTarget);
      await resetScroll(page);
      await page.locator('.board-state-radio[data-filter="awaiting-confirmation"]').click();
      await settle(page);
      for (const w of [1600, 1280, 1120, 1003, 1002, 900, 850, 803, 802, 718, 640, 580, 540, 539, 480, 400, 360]) {
        await setExactToolbarWidth(page, w);
        const m = await measureToolbar(page);
        assertSingleRow(m, `${lang} awaiting-confirmation w=${w}`);
      }

      // Verify scrolled toolbar stays non-floating and single-row in bounded snap-deck mode.
      await setExactToolbarWidth(page, 1600);
      await resetScroll(page);
      await page.locator('.board-state-radio[data-filter="all"]').click();
      await settle(page);
      await setExactToolbarWidth(page, 803);
      await page.evaluate(() => {
        const b = document.querySelector('#bcol');
        b.scrollTop = 300;
        b.dispatchEvent(new Event('scroll', { bubbles: true }));
      });
      await settle(page);
      const scrolled = await measureToolbar(page);
      assert.equal(scrolled.floating, false, `${lang} scrolled toolbar stays non-floating in bounded snap-deck mode`);
      assertSingleRow(scrolled, `${lang} scrolled w=803`);

      assert.deepEqual(pageErrors, [], `${lang} no page errors`);
      console.log(`PASS toolbar-width sweep (${lang}): transitions=${JSON.stringify(transitions.map(t => ({ w: t.w, sig: t.sig, faceH: t.m.faceHeight, filterW: t.m.filterWidth })))}`);
      await context.close();
    }

    // 2. Real full-page default layout viewport sweep (1600 down to 360: untouched CSS, split >= 1100, tabs < 1100).
    for (const lang of ['ko', 'en']) {
      const context = await browser.newContext({ viewport: { width: 1600, height: 800 }, locale: lang === 'ko' ? 'ko-KR' : 'en-US', colorScheme: 'light', reducedMotion: 'reduce' });
      await context.route('**/*', route => {
        const u = new URL(route.request().url());
        if (u.origin === new URL(base).origin) return route.continue();
        return route.abort();
      });
      const page = await context.newPage();
      await page.goto(`${base}/s/tbcollapse?lang=${lang}&theme=light`, { waitUntil: 'domcontentloaded' });
      await page.locator('.board-toolbar').waitFor({ state: 'attached', timeout: 10000 });
      for (const vw of [1600, 1440, 1280, 1120, 1100, 1099, 1003, 1002, 900, 850, 803, 802, 718, 600, 540, 539, 480, 360]) {
        await page.setViewportSize({ width: vw, height: 800 });
        await settle(page);
        if (!(await page.locator('#bcol').isVisible())) {
          await page.locator('[data-tab="board"]').first().click();
          await settle(page);
        }
        const m = await measureToolbar(page);
        assertSingleRow(m, `${lang} default-layout vw=${vw} (toolbar=${m.outerWidth})`);
      }
      console.log(`PASS default-layout viewport sweep (${lang})`);
      await context.close();
    }
  } finally {
    await browser.close();
  }
})().catch(err => {
  console.error(err);
  process.exitCode = 1;
});
