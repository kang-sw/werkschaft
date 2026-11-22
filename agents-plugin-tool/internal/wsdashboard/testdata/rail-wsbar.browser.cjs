// Run by TestRailWsbarGearLayoutBrowser against disposable fixture sessions.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, shots, shortWS] = process.argv.slice(2);

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    if (shots) fs.mkdirSync(shots, { recursive: true });
    const errors = [];
    for (const theme of ['dark', 'light']) {
      const ctx = await browser.newContext({
        viewport: { width: 1680, height: 1000 },
        deviceScaleFactor: 2,
        locale: 'en-US',
        colorScheme: theme,
      });
      await ctx.addCookies([
        { name: 'ws_dashboard_theme', value: theme, url: base },
        { name: 'ws_dashboard_lang', value: 'en', url: base },
      ]);
      const page = await ctx.newPage();
      page.on('pageerror', e => errors.push(`${theme} pageerror: ${e.message}`));
      page.on('console', msg => {
        if (msg.type() === 'error') errors.push(`${theme} console.error: ${msg.text()}`);
      });

      await page.goto(base + '/');
      const shortBar = page.locator(`.rws[data-ws=${JSON.stringify(shortWS)}] > .wsbar`);
      await shortBar.waitFor({ state: 'visible' });

      // Move pointer well away from the rail so idle captures have no hover state.
      await page.mouse.move(1200, 500);
      await page.waitForTimeout(200);

      const measure = () => shortBar.evaluate(bar => {
        const rail = document.getElementById('rail');
        const gear = bar.querySelector('.r-config-gear');
        const memo = bar.querySelector('.memo-count');
        const ct = bar.querySelector('.ct');
        const tog = bar.querySelector('.wstog');
        const r = el => el.getBoundingClientRect();
        const b = r(bar), g = r(gear), m = r(memo), c = r(ct), t = r(tog);
        const padRight = parseFloat(getComputedStyle(bar).paddingRight);
        return {
          gearDisplay: getComputedStyle(gear).display,
          gearOpacity: parseFloat(getComputedStyle(gear).opacity),
          barRect: { left: b.left, right: b.right, width: b.width },
          padRight,
          togRect: { left: t.left, right: t.right },
          gearRect: { left: g.left, right: g.right, width: g.width },
          memoRect: { left: m.left, right: m.right, width: m.width },
          ctRect: { left: c.left, right: c.right, width: c.width },
          overflowDoc: document.documentElement.scrollWidth > window.innerWidth,
          overflowRail: rail.scrollWidth > rail.clientWidth,
        };
      });

      const idle = await measure();
      assert.equal(idle.overflowDoc, false, `${theme} idle: no document horizontal overflow`);
      assert.equal(idle.overflowRail, false, `${theme} idle: no rail horizontal overflow`);
      assert.equal(idle.gearOpacity, 0, `${theme} idle: gear starts hidden`);
      assert.ok(idle.gearRect.right < idle.memoRect.left, `${theme} idle: gear precedes memo-count`);
      assert.ok(idle.memoRect.right < idle.ctRect.left, `${theme} idle: memo-count precedes ct`);
      assert.ok(Math.abs((idle.ctRect.left - idle.memoRect.right) - 8) <= 0.5,
        `${theme} idle: expected ~8px gap between memo-count and ct, got ${idle.ctRect.left - idle.memoRect.right}`);
      assert.ok(Math.abs((idle.barRect.right - idle.padRight) - idle.ctRect.right) <= 1,
        `${theme} idle: expected ct flush at right edge of wsbar`);

      if (shots) {
        await page.locator('#rail').screenshot({ path: path.join(shots, `rail-wsbar-idle-${theme}.png`) });
      }

      // Hover the project gear inside the short workspace bar.
      await shortBar.locator('.r-config-gear').hover();
      await page.waitForTimeout(200);
      const hovered = await measure();
      assert.ok(hovered.gearOpacity >= 0.8, `${theme} hover: gear reveals (opacity=${hovered.gearOpacity})`);
      assert.ok(Math.abs(hovered.togRect.right - idle.togRect.right) < 0.1, `${theme} hover: wstog must not shift`);
      assert.ok(Math.abs(hovered.gearRect.left - idle.gearRect.left) < 0.1, `${theme} hover: gear must not shift`);
      assert.ok(Math.abs(hovered.memoRect.left - idle.memoRect.left) < 0.1, `${theme} hover: memo-count must not shift`);
      assert.ok(Math.abs(hovered.ctRect.left - idle.ctRect.left) < 0.1, `${theme} hover: ct must not shift`);

      if (shots) {
        await page.locator('#rail').screenshot({ path: path.join(shots, `rail-wsbar-hover-${theme}.png`) });
      }

      // Without data-can-control, gear is display:none and [memo-count] [ct] remain adjacent at right edge.
      await page.mouse.move(1200, 500);
      await page.evaluate(() => document.documentElement.removeAttribute('data-can-control'));
      await page.waitForTimeout(50);
      const readonly = await measure();
      assert.equal(readonly.gearDisplay, 'none', `${theme} readonly: gear is hidden from layout`);
      assert.ok(Math.abs((readonly.ctRect.left - readonly.memoRect.right) - 8) <= 0.5,
        `${theme} readonly: memo-count and ct stay ~8px apart, got ${readonly.ctRect.left - readonly.memoRect.right}`);
      assert.ok(Math.abs((readonly.barRect.right - readonly.padRight) - readonly.ctRect.right) <= 1,
        `${theme} readonly: ct stays flush at right edge`);
      await page.evaluate(() => document.documentElement.setAttribute('data-can-control', '1'));

      await ctx.close();
    }
    assert.deepEqual(errors, [], `unexpected browser errors: ${errors.join('; ')}`);
    console.log('PASS: rail wsbar gear-left-of-memo layout verified across dark/light, idle/hover, and readonly');
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exitCode = 1; });
