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
    const face = toolbar.firstElementChild;
    const row = face.firstElementChild;
    const actions = [...row.querySelectorAll('.board-overview, .tnew, .wnew, .board-download, .board-files')].map(n => {
      const r = rect(n);
      const label = n.querySelector('.board-label');
      return {
        cls: n.className,
        rect: r,
        labelVisible: !!(label && label.getClientRects().length),
        clipped: n.scrollWidth > n.clientWidth + 1
      };
    });
    const filter = row.querySelector('.board-state-filter');
    const inline = row.querySelector('.board-state-inline');
    const trigger = row.querySelector('.board-state-trigger');
    const radios = [...row.querySelectorAll('.board-state-radio')];
    const filterLabelsVisible = inline && !inline.hidden
      ? radios.filter(n => n.dataset.filter !== 'asks' && n.querySelector('.board-state-label').getClientRects().length > 0).length
      : 0;
    const filterVisible = !!(filter && filter.getClientRects().length);
    const items = [...actions.map(a => a.rect), ...(filterVisible ? [rect(filter)] : [])];
    const centers = items.map(r => r.y + r.height / 2);
    const maxDeltaY = centers.length ? Math.max(...centers) - Math.min(...centers) : 0;
    const wrapped = maxDeltaY > 1;
    const sorted = items.slice().sort((a, b) => a.x - b.x);
    let maxOverlap = 0;
    for (let i = 1; i < sorted.length; i++) {
      maxOverlap = Math.max(maxOverlap, sorted[i - 1].right - sorted[i].x);
    }
    const faceRect = rect(face);
    const overflowRight = Math.max(...items.map(r => r.right)) - faceRect.right;
    const clippedAction = actions.some(a => a.clipped);
    const clippedRadio = inline && !inline.hidden && radios.some(n => n.scrollWidth > n.clientWidth + 1);
    return {
      outerWidth: Math.round(rect(toolbar).width * 10) / 10,
      faceWidth: Math.round(faceRect.width * 10) / 10,
      faceHeight: Math.round(faceRect.height * 10) / 10,
      filterWidth: filterVisible ? Math.round(rect(filter).width * 10) / 10 : 0,
      mode: toolbar.getAttribute('data-toolbar-fit') || 'full',
      actionLabels: actions.filter(a => a.labelVisible).length,
      actionCount: actions.length,
      filterMode: trigger && !trigger.hidden ? 'dropdown' : 'inline',
      filterLabelsVisible,
      wrapped,
      maxDeltaY: Math.round(maxDeltaY * 10) / 10,
      maxOverlap: Math.round(maxOverlap * 10) / 10,
      overflowRight: Math.round(overflowRight * 10) / 10,
      clippedAction,
      clippedRadio,
      floating: toolbar.hasAttribute('data-floating')
    };
  });
}

async function setExactToolbarWidth(page, target) {
  // Adjust viewport so .board-toolbar has exact outer width = target.
  // Above 1600px viewport the docked rail (~356px) joins, so start wide enough
  // for large targets to land cleanly at target.
  const startVW = target >= 1540 ? target + 400 : target + 40;
  await page.setViewportSize({ width: startVW, height: 800 });
  for (let i = 0; i < 8; i++) {
    await settle(page);
    const actual = (await page.locator('.board-toolbar').boundingBox()).width;
    if (Math.abs(actual - target) < 0.25) return actual;
    const cur = page.viewportSize().width;
    await page.setViewportSize({ width: Math.max(320, Math.round(cur + target - actual)), height: 800 });
  }
  await settle(page);
  return (await page.locator('.board-toolbar').boundingBox()).width;
}

function assertSingleRow(m, label) {
  assert.equal(m.wrapped, false, `${label} wrapped (deltaY=${m.maxDeltaY}, mode=${m.mode}, filter=${m.filterMode})`);
  assert(m.maxOverlap <= 0.5, `${label} overlap=${m.maxOverlap}`);
  assert(m.overflowRight <= 0.5, `${label} overflowRight=${m.overflowRight}`);
  assert.equal(m.clippedAction, false, `${label} clippedAction`);
  assert.equal(m.clippedRadio, false, `${label} clippedRadio`);
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

      // Verify wide width: full labelled bar visible on single row.
      const actualWide = await setExactToolbarWidth(page, wideTarget);
      assert(Math.abs(actualWide - wideTarget) < 1, `${lang} reached ${wideTarget}px toolbar width (got ${actualWide})`);
      await resetScroll(page);
      const wide = await measureToolbar(page);
      assertSingleRow(wide, `${lang} w=${wideTarget}`);
      assert.equal(wide.mode, 'full', `${lang} ${wideTarget} must be in full mode`);
      assert.equal(wide.actionLabels, 5, `${lang} ${wideTarget} must show all 5 action labels`);
      assert.equal(wide.filterMode, 'inline', `${lang} ${wideTarget} must use inline filter`);
      assert.equal(wide.filterLabelsVisible, 9, `${lang} ${wideTarget} must show all 9 non-ask filter labels`);

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
        const sig = `${m.mode}|${m.actionLabels}|${m.filterMode}|${m.filterLabelsVisible}`;
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
        console.log(`AFTER ${lang} w=${w}: mode=${m.mode} actions=${m.actionLabels}/${m.actionCount} filter=${m.filterMode}(${m.filterLabelsVisible} labels, ${m.filterWidth}px) faceH=${m.faceHeight} wrapped=${m.wrapped}`);
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

      // Verify floating toolbar state stays single-row too (restore "all" so #bcol overflows and scrolls).
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
      const floating = await measureToolbar(page);
      assert.equal(floating.floating, true, `${lang} scrolled toolbar floats`);
      assertSingleRow(floating, `${lang} floating w=803`);

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
