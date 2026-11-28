// Source-only, fake-data LOOK check of the bottom-right global status bar plan
// usage handle (#shell-bottom #slot-bot-right .usage-toggle). No daemon, build,
// credentials or network.
// WS_PLAYWRIGHT_MODULE=/absolute/path/to/playwright node <this-file> <mockup.html> <shots-dir>
// The mockup is the approved Variant D shell redesign; its usage handle is
// captured beside each implementation shot.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [mockup, output] = process.argv.slice(2);
assert.ok(mockup && output, 'pass the approved mockup HTML and output directory');
const { readAssetSource } = require('./asset-source.cjs');
const css = [
  readAssetSource('wswhiteboard', 'page.css'),
  readAssetSource('wsdashboard', 'dashboard.css'),
  readAssetSource('wsdashboard', 'dashboard-ide-shell.css'),
  readAssetSource('wsdashboard', 'usage.css'),
].join('\n');
const scripts = ['rail.js', 'usage.js', 'dashboard.js'].map(p => readAssetSource('wsdashboard', p));
const at = Date.UTC(2026, 10, 15);
function entries(variant) {
  const observed = variant === 'stale' ? at - 42 * 60000 : at;
  const w = (kind, percent, elapsed, asOf) => ({ kind, percent, asOf, resetsAt: asOf + (1 - elapsed) * (kind === 'five_hour' ? 5 * 3600000 : 7 * 86400000) });
  const gpt = { harness: 'codex', source: 'via Pi', windows: [w('seven_day', 38, .45, at)] };
  if (variant === 'weekly') return [gpt];
  return [{ harness: 'claude', source: 'Claude push', windows: [w('five_hour', variant === 'over' ? 64 : 20, 1 / 3, observed), w('seven_day', 53, .66, observed)] }, gpt];
}
(async () => {
  fs.mkdirSync(output, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  try {
    for (const theme of ['dark', 'light']) for (const width of [1280, 360]) {
      const context = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: theme, serviceWorkers: 'block' });
      const requests = [];
      await context.route(/^https?:/, route => { requests.push(route.request().url()); return route.abort(); });
      const reference = await context.newPage();
      await reference.goto(pathToFileURL(path.resolve(mockup)).href);
      for (const variant of ['normal', 'over', 'stale', 'weekly']) {
        const page = await context.newPage(), errors = [];
        page.on('pageerror', e => errors.push(e.message));
        await page.setContent(`<!doctype html><html data-theme="${theme}" data-mode="index" data-shell><head><style>${css}</style><style>
          body { margin: 0; padding: 10px; }
          .rail { position: static !important; transform: none !important; visibility: visible !important; width: 280px !important; max-width: 100%; height: auto !important; }
        </style></head><body><div class="dwrap"><div class="top"></div></div><aside class="rail" id="rail"><div id="r-rail"></div></aside><footer class="shell-bottom" id="shell-bottom" role="contentinfo"><div class="bot-left" id="slot-bot-left"></div><div class="bot-center" id="slot-bot-center"><span class="talert" id="talert" hidden></span></div><div class="bot-right" id="slot-bot-right"><button class="usage-toggle" type="button" aria-label="Plan usage" aria-expanded="false"></button></div></footer><button id="outside" style="position:fixed;top:10px;left:10px">Outside</button></body></html>`);
        await page.evaluate(({ at, entries }) => {
          Date.now = () => at;
          window.wsI18n = { lang: 'en', t: {} }; window.wsT = key => key; window.wsTn = key => key;
          // Disable the stream transport; only usage fetches may occur, and
          // these resolve locally. Load the real dashboard event handlers.
          window.WebSocket = undefined;
          window.fetch = url => {
            if (!String(url).startsWith('/rail/usage')) throw new Error('Unexpected fixture fetch: ' + url);
            return Promise.resolve({ ok: true, json: () => Promise.resolve({ entries, warnPercent: 80, critPercent: 95 }) });
          };
        }, { at, entries: entries(variant) });
        for (const content of scripts) await page.addScriptTag({ content });
        assert.deepEqual(errors, [], `${theme}/${width}/${variant}: initialization`);
        await page.locator('.usage-number').first().waitFor({ state: 'attached' });
        assert.equal(await page.locator('.usage-column').count(), variant === 'weekly' ? 1 : 3);
        assert.equal(await page.locator('.usage-clock').count(), variant === 'stale' ? 1 : 0);
        if (variant === 'stale') {
          const staleCols = page.locator('.usage-column.usage-stale');
          assert.equal(await staleCols.count(), 2);
          assert.deepEqual(await staleCols.locator('.usage-number').allTextContents(), ['20%', '53%']);
          assert.equal(await staleCols.locator('.usage-pace').count(), 0);
          assert.equal(await page.locator('.usage-pace').first().textContent(), '−7%');
        } else {
          assert.equal(await page.locator('.usage-pace').first().textContent(), variant === 'over' ? '+31%' : variant === 'weekly' ? '−7%' : '−13%');
        }
        // Single-line pill inside the 26px bottom status bar; at 360px compact mode (.bu) shows .usage-mini.
        const handle = await page.locator('.usage-toggle').boundingBox();
        assert.ok(handle.height <= 26 && handle.height >= 18, `${theme}/${width}/${variant}: compact bottom-bar handle (${handle.height}px)`);
        const isCompact = await page.locator('#shell-bottom').evaluate(el => el.classList.contains('bu'));
        if (isCompact) {
          assert.equal(await page.locator('.usage-mini').isVisible(), true, `${theme}/${width}/${variant}: compact .usage-mini visible`);
          assert.ok(handle.width <= 26, `${theme}/${width}/${variant}: compact width (${handle.width}px)`);
        } else {
          const clear = await page.evaluate(() => [...document.querySelectorAll('.usage-harness')].every(h => {
            const cols = h.querySelector('.usage-columns').getBoundingClientRect().left, c = h.querySelector('.usage-clock');
            return h.querySelector('.usage-mark').getBoundingClientRect().right <= cols && (!c || c.getBoundingClientRect().right <= cols);
          }));
          assert.ok(clear, `${theme}/${width}/${variant}: marks clear of the columns`);
        }
        if (variant === 'stale') {
          const staleStyle = await page.locator('.usage-column.usage-stale').first().evaluate(e => ({
            decorationLine: getComputedStyle(e.querySelector('.usage-number')).textDecorationLine,
            decorationStyle: getComputedStyle(e.querySelector('.usage-number')).textDecorationStyle,
            hasPace: !!e.querySelector('.usage-pace'),
          }));
          assert.equal(staleStyle.hasPace, false);
          assert.equal(staleStyle.decorationLine, 'none');
          assert.notEqual(staleStyle.decorationStyle, 'dotted');
        }
        const prefix = `${theme}-${width}-${variant}`;
        const refLocator = (await reference.locator('.usage-toggle').count())
          ? reference.locator('.usage-toggle')
          : reference.locator('.sr4-h[data-a="usage"]');
        const expected = await refLocator.first().screenshot({ path: path.join(output, `${prefix}-mockup.png`) });
        const actual = await page.locator('.usage-toggle').screenshot({ path: path.join(output, `${prefix}-actual.png`) });
        const comparison = await context.newPage();
        await comparison.setViewportSize({ width: 720, height: 90 });
        await comparison.setContent(`<body style="margin:0;background:${theme === 'dark' ? '#141518' : '#fff'};color:${theme === 'dark' ? '#e7e7e4' : '#222'};font:12px system-ui"><div style="display:flex;gap:16px;padding:10px"><div>Approved (dark)<br><img src="data:image/png;base64,${expected.toString('base64')}"></div><div>Implementation<br><img src="data:image/png;base64,${actual.toString('base64')}"></div></div></body>`);
        await comparison.screenshot({ path: path.join(output, `${prefix}-comparison.png`) });
        await comparison.close();
        // These are the production dashboard.js open, Escape, outside-click,
        // close-button, aria-expanded and focus-restoration paths, not mocks.
        const button = page.locator('.usage-toggle'), dialog = page.locator('.usage-pop');
        await button.focus(); await page.keyboard.press('Enter');
        await dialog.waitFor({ state: 'visible' });
        const popBox = await dialog.boundingBox();
        const barBox = await page.locator('#shell-bottom').boundingBox();
        assert.ok(popBox && barBox && popBox.y >= 0 && popBox.y + popBox.height <= barBox.y + 1, `${theme}/${width}/${variant}: usage popover opens upward above bottom bar`);
        assert.equal(await button.getAttribute('aria-expanded'), 'true');
        assert.ok((await dialog.textContent()).includes(variant === 'weekly' ? 'Codex' : 'Claude')); // harness names, never the source
        if (variant === 'stale') {
          const staleCells = dialog.locator('.up-nc.is-stale');
          assert.equal(await staleCells.count(), 2);
          assert.equal(await dialog.locator('.up-nc-clk').count(), 2);
          assert.equal(await dialog.locator('.up-sync').getAttribute('data-tone'), 'warn');
        }
        await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' });
        assert.equal(await button.getAttribute('aria-expanded'), 'false');
        assert.equal(await button.evaluate(e => e === document.activeElement), true);
        await button.click(); await dialog.waitFor({ state: 'visible' });
        await page.locator('#outside').click(); await dialog.waitFor({ state: 'hidden' });
        assert.equal(await button.getAttribute('aria-expanded'), 'false');
        await button.click(); await dialog.waitFor({ state: 'visible' });
        await page.locator('.usage-pop .usage-close').click();
        await dialog.waitFor({ state: 'hidden' });
        assert.equal(await button.evaluate(e => e === document.activeElement), true);
        assert.deepEqual(errors, []);
        console.log(`${prefix}: geometry, state, keyboard, Escape and outside-click passed`);
        await page.close();
      }
      assert.deepEqual(requests, [], 'no network requests');
      await context.close();
    }
    console.log('Usage handle checks passed; inspect comparison PNGs in ' + path.resolve(output));
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
