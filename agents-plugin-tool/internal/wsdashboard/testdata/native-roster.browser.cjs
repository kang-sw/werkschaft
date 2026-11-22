// Native subagent rows in the chat's agent HUD, in disposable Chromium.
// env: NATIVE_BASE (fixture daemon), NATIVE_SHOTS (optional capture dir),
// WS_PLAYWRIGHT_MODULE (playwright module path).
const assert = require('node:assert/strict');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const base = process.env.NATIVE_BASE;
const shots = process.env.NATIVE_SHOTS;

async function run(browser, lang) {
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 840 }, deviceScaleFactor: 2, colorScheme: 'dark', locale: lang === 'ko' ? 'ko-KR' : 'en-US' });
  await ctx.addCookies([{ name: 'ws_dashboard_lang', value: lang, url: base }]);
  const tab = await ctx.newPage();
  const errors = [];
  tab.on('pageerror', e => errors.push(e.message));
  await tab.goto(`${base}/s/livesession`);
  const frame = await (await tab.locator('iframe.sframe.on').elementHandle()).contentFrame();
  await frame.locator('#agisland[data-mode="compact"]:not([hidden])').waitFor();
  await frame.waitForTimeout(350);

  // (a) compact HUD: the running agy native row, its tooltip, and the count
  // of rows whose observation ended.
  const nativeNode = frame.locator('#agcompact .hud-node[data-native="1"]');
  assert.equal(await nativeNode.count(), 1, 'one running native node in the compact HUD');
  assert.equal(await nativeNode.getAttribute('data-linked'), null, 'a native node never links');
  const obs = frame.locator('#agcompact .hud-obs');
  assert.equal(await obs.count(), 1, 'observation-ended count in the compact HUD');
  assert(await obs.isVisible(), 'the observation-ended count is visible');
  {
    const island = await frame.locator('#agisland').boundingBox(), chip = await obs.boundingBox();
    assert(chip.y >= island.y - 1 && chip.y + chip.height <= island.y + island.height + 1 && chip.x >= island.x - 1 && chip.x + chip.width <= island.x + island.width + 1,
      `observation-ended count inside the island: ${JSON.stringify({ island, chip })}`);
    if (shots) await frame.locator('#agisland').screenshot({ path: path.join(shots, `compact-hud-observation-ended-${lang}.png`) });
  }
  await nativeNode.hover();
  const tooltip = frame.locator('#hud-tooltip:not([hidden])');
  await tooltip.waitFor();
  assert.equal(await tooltip.locator('.tt-native').count(), 1);
  assert.equal(await tooltip.locator('.tt-life time').count(), 1, 'a running native row shows its start only');
  assert.equal(await tooltip.locator('.tt-unknown').count(), 0);
  assert(!(await tooltip.locator('.tt-meta').innerText()).trim().startsWith('\u00b7'), 'no orphan leading separator in the tooltip meta');
  await frame.waitForTimeout(150);
  if (shots) {
    const island = await frame.locator('#agisland').boundingBox();
    const tip = await tooltip.boundingBox();
    const iframeBox = await tab.locator('iframe.sframe.on').boundingBox();
    const x = Math.max(0, Math.min(island.x, tip.x) + iframeBox.x - 16);
    const y = Math.max(0, Math.min(island.y, tip.y) + iframeBox.y - 16);
    const right = Math.max(island.x + island.width, tip.x + tip.width) + iframeBox.x + 16;
    const bottom = Math.max(island.y + island.height, tip.y + tip.height) + iframeBox.y + 16;
    await tab.screenshot({ path: path.join(shots, `compact-hud-running-agy-${lang}.png`), clip: { x, y, width: right - x, height: bottom - y } });
  }

  // (b) the expanded tree's ended group: an end-unknown agy row and a
  // completed Codex row, each with its lifecycle.
  await tab.mouse.move(5, 5);
  await frame.locator('#agcompact').click({ position: { x: 4, y: 4 } });
  await frame.locator('#agisland[data-mode="full"]').waitFor();
  assert.equal(await frame.locator('#agpop .agent.running.native .bguide').count(), 0, 'running native row has no placeholder line');
  const ended = frame.locator('#agpop .tgroup.ended');
  await ended.waitFor();
  const unknownRow = ended.locator('.agent[data-end-unknown="1"]');
  assert.equal(await unknownRow.count(), 1, 'one end-unknown row in the ended group');
  assert.equal(await unknownRow.locator('.amk').first().innerText(), '?');
  assert.equal(await unknownRow.locator('.nbadge').count(), 1);
  assert.equal(await unknownRow.locator('.alife time').count(), 1, 'end-unknown row shows its start only');
  {
    const chip = await unknownRow.locator('.astatus').boundingBox(), row = await unknownRow.locator('.ahead').boundingBox();
    assert(chip.width < row.width / 2, `status chip is bounded: ${JSON.stringify({ chip, row })}`);
    assert.equal(await unknownRow.locator('.astatus').getAttribute('title') !== null, true, 'long status stays in the tooltip');
    for (const t of await unknownRow.locator('.alife').all()) {
      const full = await t.evaluate(el => el.scrollWidth <= el.clientWidth);
      assert(full, 'lifecycle columns are not clipped');
    }
  }
  assert.equal(await ended.locator('.agent.native .bguide').count(), 0, 'no placeholder slots on native rows');
  const codexRow = ended.locator('.agent.native:not([data-end-unknown])');
  assert.equal(await codexRow.count(), 1, 'one completed native row');
  assert.equal(await codexRow.locator('.alife time').count(), 2, 'completed native row shows start and end');
  assert.equal(await codexRow.locator('.amk').first().innerText(), '✓');
  await frame.waitForTimeout(200);
  if (shots) {
    await frame.locator('#agpop').screenshot({ path: path.join(shots, `expanded-ended-group-${lang}.png`) });
  }
  assert.deepEqual(errors, [], 'no page errors');
  await ctx.close();
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    for (const lang of ['en', 'ko']) await run(browser, lang);
  } finally {
    await browser.close();
  }
  console.log('native roster browser ok');
})().catch(e => { console.error(e); process.exit(1); });
