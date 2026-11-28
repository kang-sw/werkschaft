// End-to-end headless Chromium verification + side-by-side visual comparison of
// the unified 2-chart usage popover (Mode A + Mode B), compact slot rows,
// sessions drawer, Track 1 bottom-bar upward placement, and Phase 3 inactive-slot
// hover preview across Dark & Light themes at 680px, 480px, and 360px popover widths.
//
// Usage:
//   WS_PLAYWRIGHT_MODULE=/path/to/playwright node testdata/usage-popover.browser.cjs <mockup.html> <shots-dir>
'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const { readAssetSource } = require('./asset-source.cjs');
const { createScenarioFixture } = require('./usage-popover-fixture.cjs');

const [mockupHtml, outDir] = process.argv.slice(2);
assert.ok(mockupHtml && outDir, 'pass mockup index.html and output directory');

const css = [
  readAssetSource('wswhiteboard', 'page.css'),
  readAssetSource('wsdashboard', 'dashboard.css'),
  readAssetSource('wsdashboard', 'dashboard-ide-shell.css'),
  readAssetSource('wsdashboard', 'usage.css'),
].join('\n');
const scripts = ['rail.js', 'usage.js', 'dashboard.js'].map(p => readAssetSource('wsdashboard', p));
const koTable = JSON.parse(fs.readFileSync(path.join(__dirname, '..', '..', 'wsi18n', 'tables', 'ko.json'), 'utf8'));

function buildHtml(theme) {
  return `<!doctype html><html data-theme="${theme}" data-mode="index" data-shell data-sysopen><head><style>${css}</style><style>
    body { margin: 0; background: var(--wb-bg); color: var(--wb-fg); min-height: 100vh; }
    .rail { position: fixed; top: 0; left: -300px; width: 260px; }
  </style></head><body>
    <div class="dwrap"><div class="top"></div></div>
    <aside class="rail" id="rail"><div id="r-rail"></div></aside>
    <footer class="shell-bottom" id="shell-bottom" role="contentinfo">
      <div class="bot-left" id="slot-bot-left"></div>
      <div class="bot-center" id="slot-bot-center"><span class="talert" id="talert" hidden></span></div>
      <div class="bot-right" id="slot-bot-right">
        <button class="usage-toggle" type="button" aria-label="플랜 사용량" aria-expanded="false"></button>
      </div>
    </footer>
    <button id="outside" style="position:fixed;top:8px;left:8px;z-index:1">Outside</button>
  </body></html>`;
}

async function mountScenarioPage(context, theme, viewportWidth, scenario) {
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.setViewportSize({ width: viewportWidth, height: 900 });
  const fx = createScenarioFixture(scenario);

  await page.exposeFunction('__fixtureFetch', (url, method, rawBody) => {
    const m = (method || 'GET').toUpperCase();
    const u = String(url);
    if (m === 'GET' && u.startsWith('/rail/usage')) return { ok: true, status: 200, body: fx.usagePayload() };
    if (m === 'GET' && u === '/api/account/status') return { ok: true, status: 200, body: fx.accountStatusPayload() };
    if (m === 'POST' && u === '/api/account/slots/switch') return fx.switchSlot(JSON.parse(rawBody || '{}'));
    return { ok: false, status: 404, body: { error: 'unexpected ' + m + ' ' + u } };
  });

  await page.setContent(buildHtml(theme));
  await page.evaluate(({ now, koTable }) => {
    Date.now = () => now;
    window.wsI18n = { lang: 'ko', t: koTable };
    window.wsT = (key, ...args) => (koTable[key] || key).replace(/\{(\d+)\}/g, (m, i) => (i < args.length ? String(args[i]) : m));
    window.wsTn = key => key;
    window.WebSocket = undefined;
    window.__fetchLog = [];
    window.fetch = async (url, options = {}) => {
      window.__fetchLog.push({ url: String(url), method: (options.method || 'GET').toUpperCase(), body: options.body || null });
      const res = await window.__fixtureFetch(String(url), options.method || 'GET', options.body || null);
      return { ok: res.ok, status: res.status, json: async () => res.body };
    };
  }, { now: fx.now, koTable });
  for (const content of scripts) await page.addScriptTag({ content });
  await page.locator('.usage-toggle .usage-harness').first().waitFor({ state: 'attached' });
  return { page, errors };
}

(async () => {
  fs.mkdirSync(outDir, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  try {
    // 1. Visual side-by-side comparison + geometry across Dark & Light at 680 / 480 / 360 popover widths
    const widths = [
      { label: '680', viewport: 1280, mockW: 'auto', minW: 660, maxW: 682 },
      { label: '480', viewport: 496, mockW: 'compact', minW: 460, maxW: 482 },
      { label: '360', viewport: 376, mockW: 'narrow', minW: 340, maxW: 362 },
    ];
    for (const theme of ['dark', 'light']) {
      const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, colorScheme: theme, serviceWorkers: 'block' });
      const netRequests = [];
      await context.route(/^https?:/, route => { netRequests.push(route.request().url()); return route.abort(); });

      // Load approved v2 mockup page for side-by-side capture
      const mockPage = await context.newPage();
      await mockPage.setViewportSize({ width: 1280, height: 960 });
      await mockPage.goto(pathToFileURL(path.resolve(mockupHtml)).href);
      await mockPage.locator('#usage-pop .csvg').first().waitFor({ state: 'visible' });
      if (theme === 'light') {
        await mockPage.locator('#m-theme .m-opt[data-v="light"]').click();
      }

      for (const w of widths) {
        const { page, errors } = await mountScenarioPage(context, theme, w.viewport, 'multi');
        // Check Track 1 compact .usage-mini at 360px viewport
        if (w.label === '360') {
          const isCompact = await page.locator('#shell-bottom').evaluate(el => el.classList.contains('bu'));
          assert.equal(isCompact, true, `${theme}/360: bottom bar enters compact .bu mode`);
          assert.equal(await page.locator('.usage-mini').isVisible(), true, `${theme}/360: .usage-mini visible`);
        }
        await page.locator('.usage-toggle').click();
        const pop = page.locator('.usage-pop');
        await pop.waitFor({ state: 'visible' });
        await pop.locator('.up-csvg').first().waitFor({ state: 'visible' });
        await pop.locator('.up-sp').first().waitFor({ state: 'visible' });

        // Upward placement above #shell-bottom
        const checkAboveBar = async tag => {
          const popBox = await pop.boundingBox();
          const barBox = await page.locator('#shell-bottom').boundingBox();
          assert.ok(popBox && barBox, `${tag}: boxes exist`);
          assert.ok(popBox.y >= 0 && popBox.y + popBox.height <= barBox.y + 2, `${tag}: popover (${popBox.y}+${popBox.height}) stays above bottom bar (${barBox.y})`);
          assert.ok(popBox.width >= w.minW && popBox.width <= w.maxW, `${tag}: width ${popBox.width} within [${w.minW}, ${w.maxW}]`);
        };
        await checkAboveBar(`${theme}/${w.label}/modeA`);

        // At 680px verify Claude and Codex slot pills stay on a single line (no wrap).
        if (w.label === '680') {
          for (const k of ['claude', 'codex']) {
            const wrapped = await pop.locator(`.up-hr[data-k="${k}"] .up-sp`).evaluateAll(btns => {
              const tops = btns.map(b => Math.round(b.getBoundingClientRect().top));
              return Math.max(...tops) - Math.min(...tops) > 2;
            });
            assert.equal(wrapped, false, `${theme}/680: ${k} slot pills must not wrap`);
          }
          // Verify Light-theme Codex badge uses high-contrast dark background + white text.
          if (theme === 'light') {
            const cxColors = await pop.locator('.up-hb[data-k="codex"]').first().evaluate(el => ({
              bg: getComputedStyle(el).backgroundColor,
              fg: getComputedStyle(el).color,
            }));
            assert.equal(cxColors.fg, 'rgb(255, 255, 255)', 'Light-theme Codex badge uses white foreground');
            assert.notEqual(cxColors.bg, 'rgb(242, 242, 242)', 'Light-theme Codex badge uses dark charcoal bg, not pale gray');
          }
        }

        // Capture Mode A implementation + mockup side-by-side
        await mockPage.locator(`#m-w .m-opt[data-v="${w.mockW}"]`).click();
        await mockPage.locator('#axis-seg .as-opt[data-v="progress"]').click();
        await mockPage.waitForTimeout(100);
        const mockShotA = await mockPage.locator('#usage-pop').screenshot();
        const implShotA = await pop.screenshot({ path: path.join(outDir, `impl-${theme}-${w.label}-modeA.png`) });
        const cmpA = await context.newPage();
        await cmpA.setViewportSize({ width: 1440, height: 920 });
        await cmpA.setContent(`<body style="margin:0;padding:12px;background:${theme === 'dark' ? '#121316' : '#f4f5f7'};color:${theme === 'dark' ? '#e7e7e4' : '#1e1f22'};font:12px system-ui"><div style="display:flex;gap:16px;align-items:flex-start"><div><b>Approved Mockup (${theme} ${w.label}px Mode A)</b><br><img src="data:image/png;base64,${mockShotA.toString('base64')}"></div><div><b>Implementation (${theme} ${w.label}px Mode A)</b><br><img src="data:image/png;base64,${implShotA.toString('base64')}"></div></div></body>`);
        await cmpA.screenshot({ path: path.join(outDir, `cmp-${theme}-${w.label}-modeA.png`), fullPage: true });
        await cmpA.close();

        // Switch to Mode B (Timeline · Gantt), assert date labels, Gantt lanes, upward placement
        await pop.locator('.up-as-opt[data-v="timeline"]').click();
        await checkAboveBar(`${theme}/${w.label}/modeB`);
        assert.equal(await pop.locator('.up-cpanel[data-kind="five_hour"] .up-gl').count(), 4);
        assert.ok((await pop.locator('.up-cpanel[data-kind="seven_day"] .usage-day-label').count()) >= 2);
        assert.equal(await pop.locator('.up-cpanel[data-kind="five_hour"] .up-bound-label').count(), 2);

        await mockPage.locator('#axis-seg .as-opt[data-v="timeline"]').click();
        await mockPage.waitForTimeout(100);
        const mockShotB = await mockPage.locator('#usage-pop').screenshot();
        const implShotB = await pop.screenshot({ path: path.join(outDir, `impl-${theme}-${w.label}-modeB.png`) });
        const cmpB = await context.newPage();
        await cmpB.setViewportSize({ width: 1440, height: 980 });
        await cmpB.setContent(`<body style="margin:0;padding:12px;background:${theme === 'dark' ? '#121316' : '#f4f5f7'};color:${theme === 'dark' ? '#e7e7e4' : '#1e1f22'};font:12px system-ui"><div style="display:flex;gap:16px;align-items:flex-start"><div><b>Approved Mockup (${theme} ${w.label}px Mode B)</b><br><img src="data:image/png;base64,${mockShotB.toString('base64')}"></div><div><b>Implementation (${theme} ${w.label}px Mode B)</b><br><img src="data:image/png;base64,${implShotB.toString('base64')}"></div></div></body>`);
        await cmpB.screenshot({ path: path.join(outDir, `cmp-${theme}-${w.label}-modeB.png`), fullPage: true });
        await cmpB.close();

        // Phase 3: Hover preview check at every width & theme (zero fetch, zero SVG re-render)
        await pop.locator('.up-as-opt[data-v="progress"]').click();
        const beforeSnap = await page.evaluate(() => {
          window.__svgRefs = [...document.querySelectorAll('.usage-pop .up-csvg')];
          return window.__fetchLog.length;
        });
        await pop.locator('.up-hr[data-k="claude"] .up-sp[data-slot-id="work"]').hover();
        const workCheck = await page.evaluate(beforeCount => {
          const curSvgs = [...document.querySelectorAll('.usage-pop .up-csvg')];
          const sameSvgs = curSvgs.length === window.__svgRefs.length && curSvgs.every((s, i) => s === window.__svgRefs[i]);
          const pinTexts = [...document.querySelectorAll('.usage-pop .up-ppin.is-preview-on')].map(n => n.textContent);
          const visibleCallouts = [...document.querySelectorAll('.usage-pop .up-pcallout')].filter(n => !n.hidden).map(n => n.textContent);
          return {
            fetchDelta: window.__fetchLog.length - beforeCount,
            sameSvgs,
            previewCurves: document.querySelectorAll('.usage-pop .up-pser.is-preview-on').length,
            pinTexts,
            visibleCallouts,
            rowPreviewVisible: [...document.querySelectorAll('.up-hr[data-k="claude"] .up-nc-prev[data-preview-slot="work"]')].every(n => !n.hidden),
          };
        }, beforeSnap);
        assert.equal(workCheck.fetchDelta, 0, `${theme}/${w.label}: zero fetch on Work hover`);
        assert.equal(workCheck.sameSvgs, true, `${theme}/${w.label}: zero SVG re-render on Work hover`);
        assert.equal(workCheck.previewCurves, 2);
        assert.ok(workCheck.pinTexts[0].includes('Cl·Work · 12분 전'), workCheck.pinTexts[0]);
        assert.equal(workCheck.rowPreviewVisible, true);
        if (w.label === '680') {
          await pop.screenshot({ path: path.join(outDir, `preview-cached-${theme}-680.png`) });
        }

        // Hover Personal (never visited -> no cache callout)
        await pop.locator('.up-hr[data-k="claude"] .up-sp[data-slot-id="personal"]').hover();
        const personalCheck = await page.evaluate(beforeCount => ({
          fetchDelta: window.__fetchLog.length - beforeCount,
          noCacheCallouts: [...document.querySelectorAll('.usage-pop .up-nocache-callout')].filter(n => !n.hidden).map(n => n.textContent),
          noCacheRowText: document.querySelector('.up-hr[data-k="claude"] .up-hr-nocache[data-preview-slot="personal"]').textContent,
          noCacheRowHidden: document.querySelector('.up-hr[data-k="claude"] .up-hr-nocache[data-preview-slot="personal"]').hidden,
        }), beforeSnap);
        assert.equal(personalCheck.fetchDelta, 0, `${theme}/${w.label}: zero fetch on Personal hover`);
        assert.equal(personalCheck.noCacheCallouts.length, 2);
        assert.ok(personalCheck.noCacheCallouts[0].includes('캐시 없음 · 클릭해 전환 후 조회'));
        assert.equal(personalCheck.noCacheRowHidden, false);
        if (w.label === '680') {
          await pop.screenshot({ path: path.join(outDir, `preview-nocache-${theme}-680.png`) });
        }

        // Leave to restore
        await page.mouse.move(10, 10);
        const restored = await page.evaluate(() => ({
          previewsOn: document.querySelectorAll('.usage-pop .up-pser.is-preview-on').length,
          liveVisible: [...document.querySelectorAll('.up-hr[data-k="claude"] .up-nc-live')].every(n => !n.hidden),
        }));
        assert.equal(restored.previewsOn, 0);
        assert.equal(restored.liveVisible, true);

        // Full interaction suite once per theme at 680px
        if (w.label === '680') {
          // Hover focus dims other harnesses to 0.18 with zero SVG re-render; click locks; Esc unlocks; second Esc closes.
          await pop.locator('.up-hr[data-k="codex"] .up-hr-name').hover();
          await page.waitForFunction(() => parseFloat(getComputedStyle(document.querySelector('.up-ser[data-k="claude"]')).opacity) <= 0.22);
          const focusCheck = await page.evaluate(() => {
            const curSvgs = [...document.querySelectorAll('.usage-pop .up-csvg')];
            const sameSvgs = curSvgs.every((s, i) => s === window.__svgRefs[i]);
            const dimmed = document.querySelector('.up-ser[data-k="claude"]').classList.contains('is-dim');
            const dimmedOpacity = parseFloat(getComputedStyle(document.querySelector('.up-ser[data-k="claude"]')).opacity);
            return { sameSvgs, dimmed, dimmedOpacity };
          });
          assert.equal(focusCheck.sameSvgs, true, `${theme}: zero SVG re-render on focus hover`);
          assert.equal(focusCheck.dimmed, true, `${theme}: dimmed class applied`);
          assert.ok(focusCheck.dimmedOpacity <= 0.22, `${theme}: dimmed opacity ~0.18 (got ${focusCheck.dimmedOpacity})`);

          await pop.locator('.up-hr[data-k="codex"] .up-hr-name').click();
          await page.mouse.move(10, 10);
          assert.equal(await pop.locator('.up-hr[data-k="codex"].is-focus').count(), 1, `${theme}: click locks focus`);
          await page.keyboard.press('Escape');
          assert.equal(await pop.isVisible(), true, `${theme}: first Escape unlocks focus without closing popover`);
          assert.equal(await pop.locator('.up-hr[data-k="codex"].is-focus').count(), 0);

          // Slot pill click posts exactly one /api/account/slots/switch and redraws
          const postsBefore = await page.evaluate(() => window.__fetchLog.filter(x => x.url === '/api/account/slots/switch').length);
          await pop.locator('.up-hr[data-k="claude"] .up-sp[data-slot-id="work"]').click();
          await pop.locator('.up-rbanner').waitFor({ state: 'visible' });
          const switchLogs = await page.evaluate(() => window.__fetchLog.filter(x => x.url === '/api/account/slots/switch'));
          assert.equal(switchLogs.length - postsBefore, 1, `${theme}: slot pill posts exactly one switch`);
          assert.deepEqual(JSON.parse(switchLogs.at(-1).body), { harness: 'claude', slot_id: 'work' });
          assert.equal(await pop.locator('.up-hr[data-k="claude"] .up-sp[data-slot-id="work"]').getAttribute('aria-pressed'), 'true');

          // Expand sessions drawer, check upward placement above bottom bar, and verify queue + now confirm actions
          await pop.locator('.up-sess-hd').click();
          await pop.locator('.up-sess-body').waitFor({ state: 'visible' });
          await checkAboveBar(`${theme}/680/drawer-open`);
          await pop.locator('.up-srow[data-conv-id="x-201"] .up-sp[data-slot-id="team-a"]').click();
          await pop.locator('.up-confirm').waitFor({ state: 'visible' });
          await pop.locator('.up-confirm .up-cbtn[data-mode="queue"]').click();
          await page.waitForFunction(() => window.__fetchLog.filter(x => x.url === '/api/account/slots/switch').length >= 2);
          const lastSwitch = await page.evaluate(() => JSON.parse(window.__fetchLog.filter(x => x.url === '/api/account/slots/switch').at(-1).body));
          assert.deepEqual(lastSwitch, { conversation_id: 'x-201', slot_id: 'team-a', mode: 'queue' });

          // Escape now closes popover since focus is not locked
          await page.keyboard.press('Escape');
          await pop.waitFor({ state: 'hidden' });
        }

        assert.deepEqual(errors, [], `${theme}/${w.label}: zero page errors`);
        await page.close();
      }

      // Also smoke-check over-pace (stale) and unauth scenarios in Dark/Light at 680px
      for (const scn of ['over', 'unauth']) {
        const { page, errors } = await mountScenarioPage(context, theme, 1280, scn);
        await page.locator('.usage-toggle').click();
        const pop = page.locator('.usage-pop');
        await pop.waitFor({ state: 'visible' });
        await pop.locator('.up-csvg').first().waitFor({ state: 'visible' });
        if (scn === 'over') {
          assert.equal(await pop.locator('.up-sync').getAttribute('data-tone'), 'warn');
          await pop.locator('.up-as-opt[data-v="timeline"]').click();
          const staleDotLeft = await pop.evaluate(() => {
            const panel = document.querySelector('.up-cpanel[data-kind="five_hour"]');
            const dot = panel.querySelector('.up-ser[data-k="claude"] .usage-point-current');
            const nowLine = panel.querySelector('.usage-now');
            return Number(dot.getAttribute('cx')) < Number(nowLine.getAttribute('x1')) - 5;
          });
          assert.equal(staleDotLeft, true, `${theme}/over: stale Claude pin anchored at X(s.at), not Now`);
        } else {
          assert.ok((await pop.locator('.up-pill.fail').count()) >= 1, `${theme}/unauth: sign-in CTA shown`);
        }
        assert.deepEqual(errors, [], `${theme}/${scn}: zero page errors`);
        await page.close();
      }

      assert.deepEqual(netRequests, [], `${theme}: zero external network requests`);
      await mockPage.close();
      await context.close();
    }
    console.log('Usage popover unified 2-chart + hover preview browser checks passed; PNGs written to ' + path.resolve(outDir));
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exitCode = 1; });
