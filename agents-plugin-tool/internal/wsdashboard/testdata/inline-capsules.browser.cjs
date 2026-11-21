const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [url, shotsDir] = process.argv.slice(2);

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    for (const theme of ['dark', 'light']) {
      const context = await browser.newContext({
        viewport: { width: 1440, height: 900 },
        colorScheme: theme,
      });
      const page = await context.newPage();
      const errors = [];
      page.on('pageerror', e => errors.push(e.message));
      await page.goto(url, { waitUntil: 'networkidle' });

      const frameEl = page.locator('iframe.sframe.on');
      await frameEl.waitFor({ state: 'visible' });
      const frame = await (await frameEl.elementHandle()).contentFrame();
      await frame.locator('.icapsule-selection').waitFor({ state: 'visible' });

      // Apply theme cleanly to both shell and session frame.
      await page.evaluate(t => { document.documentElement.setAttribute('data-theme', t); }, theme);
      await frame.evaluate(t => { document.documentElement.setAttribute('data-theme', t); }, theme);
      await page.waitForTimeout(100);

      const metrics = await frame.evaluate(() => {
        const msgs = [...document.querySelectorAll('.msg.user')];
        const legacy = msgs[msgs.length - 2];
        const hydrated = msgs[msgs.length - 1];
        hydrated.scrollIntoView({ block: 'center' });
        const pills = [...hydrated.querySelectorAll('.icapsule')];
        const hchips = hydrated.querySelector('.hchips');
        const selPill = hydrated.querySelector('.icapsule-selection');
        const bubText = hydrated.querySelector('.bub').textContent;
        return {
          legacyHasHchips: !!legacy.querySelector('.hchips'),
          legacyPillCount: legacy.querySelectorAll('.icapsule').length,
          hydratedPillCount: pills.length,
          hydratedHchipsText: hchips ? hchips.textContent : '',
          heights: pills.map(p => Math.round(p.getBoundingClientRect().height * 10) / 10),
          displays: pills.map(p => getComputedStyle(p).display),
          selLineCount: selPill.getClientRects().length,
          keepsLiteralProse: bubText.includes('Literal (99)=z stays plain.'),
        };
      });

      assert.deepEqual(errors, [], theme + ': no page errors');
      assert.equal(metrics.legacyHasHchips, true, theme + ': legacy message keeps .hchips');
      assert.equal(metrics.legacyPillCount, 0, theme + ': legacy message has no inline pills');
      assert.equal(metrics.hydratedPillCount, 6, theme + ': 6 inline capsules hydrated (81, note, p3, selection, pick, console)');
      assert.ok(metrics.hydratedHchipsText.includes('(82)'), theme + ': unanchored chip 82 remains in .hchips');
      assert.ok(!metrics.hydratedHchipsText.includes('(81)'), theme + ': anchored chip 81 deduplicated from .hchips');
      assert.ok(!metrics.hydratedHchipsText.includes('ship-gate'), theme + ': anchored topic note deduplicated from .hchips');
      assert.ok(!metrics.hydratedHchipsText.includes('#save-btn'), theme + ': anchored pick deduplicated from .hchips');
      assert.ok(!metrics.hydratedHchipsText.includes('console'), theme + ': anchored console deduplicated from .hchips');
      assert.ok(metrics.displays.every(d => d === 'inline-flex'), theme + ': all pills use inline-flex');
      assert.ok(metrics.heights.every(h => h <= 22), theme + ': all pills stay compact single-line (<=22px), got ' + JSON.stringify(metrics.heights));
      assert.equal(metrics.selLineCount, 1, theme + ': selection pill spans 1 line rect');
      assert.ok(metrics.keepsLiteralProse, theme + ': unmatched literal anchor stayed plain text');

      if (shotsDir) {
        fs.mkdirSync(shotsDir, { recursive: true });
        await page.screenshot({ path: path.join(shotsDir, `inline-${theme}.png`) });
      }

      // Hover the multi-line <wb:selection> code mention pill and verify .icapsule-pop.
      const selPill = frame.locator('.icapsule-selection').last();
      await selPill.hover();
      const pop = frame.locator('#icapsule-pop');
      await pop.waitFor({ state: 'visible' });
      await page.waitForTimeout(80);

      const popMetrics = await frame.evaluate(() => {
        const el = document.getElementById('icapsule-pop');
        const r = el.getBoundingClientRect();
        const quote = el.querySelector('.icapsule-pop-quote');
        const comment = el.querySelector('.icapsule-pop-comment');
        return {
          visible: !el.hidden && r.width > 0 && r.height > 0,
          withinViewport: r.left >= 4 && r.top >= 4 && r.right <= window.innerWidth - 4 && r.bottom <= window.innerHeight - 4,
          quoteText: quote ? quote.textContent : '',
          commentText: comment ? comment.textContent : '',
        };
      });

      assert.ok(popMetrics.visible, theme + ': popover opens on hover');
      assert.ok(popMetrics.withinViewport, theme + ': popover clamped inside viewport');
      assert.ok(popMetrics.quoteText.includes('func hydrateProse'), theme + ': popover shows multi-line code snippet');
      assert.ok(popMetrics.commentText.includes('multi-line code snippet'), theme + ': popover shows comment memo');

      if (shotsDir) {
        await page.screenshot({ path: path.join(shotsDir, `inline-popover-${theme}.png`) });
      }

      // Keyboard focus & Escape dismissal check.
      await selPill.focus();
      await pop.waitFor({ state: 'visible' });
      await selPill.press('Escape');
      await pop.waitFor({ state: 'hidden' });

      await context.close();
    }
    console.log('inline capsules real dashboard browser: dark/light pills, deduplication, popover, clamping, keyboard/Escape passed');
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exit(1); });
