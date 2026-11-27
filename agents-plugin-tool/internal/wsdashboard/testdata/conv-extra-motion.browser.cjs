// Real Chromium acceptance & optional visual-capture harness for Extra Motion
// in the real shell + session iframe layout.
// Run via TestConvExtraMotionBrowser when WS_PLAYWRIGHT_MODULE is set.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [base, mockupPath] = process.argv.slice(2);
const shotDir = process.env.WS_MOTION_SHOT_DIR || '';

async function waitSessionFrame(page) {
  await page.waitForSelector('iframe.sframe.on');
  const handle = await page.locator('iframe.sframe.on').elementHandle();
  const frame = await handle.contentFrame();
  await frame.waitForSelector('#r-convlive .clive .activity');
  return frame;
}

async function countNonTransparentPixels(frame) {
  return frame.evaluate(() => {
    const c = document.querySelector('.d-conv-motion-canvas');
    if (!c || c.hidden || c.width === 0 || c.height === 0) return 0;
    const ctx = c.getContext('2d');
    const data = ctx.getImageData(0, 0, c.width, c.height).data;
    let n = 0;
    for (let i = 3; i < data.length; i += 4) {
      if (data[i] > 12) n++;
    }
    return n;
  });
}

(async () => {
  if (shotDir) fs.mkdirSync(shotDir, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  try {
    // 1. Dark mode (default) — full live motion, flights, +m popup, hover mute, shell<->frame Settings sync
    const darkCtx = await browser.newContext({ viewport: { width: 1700, height: 900 }, colorScheme: 'dark', reducedMotion: 'no-preference' });
    darkCtx.setDefaultTimeout(10000);
    const page = await darkCtx.newPage();
    const errors = [];
    page.on('pageerror', e => errors.push(e.message));
    await page.goto(base + '/s/livesession', { waitUntil: 'domcontentloaded' });
    const frame = await waitSessionFrame(page);

    async function openSettings() {
      const gear = page.locator('.settings-toggle');
      if (!await gear.isVisible()) await page.locator('#railbtn').click();
      await gear.click();
      await page.waitForSelector('#gmenu:not([hidden])');
    }
    async function closeSettings() {
      await page.keyboard.press('Escape');
      await page.waitForSelector('#gmenu[hidden]', { state: 'attached' });
    }

    // Verify shell Settings popover: no layout switcher, Extra Motion present and default ON
    await openSettings();
    assert.equal(await page.locator('#gmenu [data-layout]').count(), 0, 'obsolete layout switcher must be absent');
    assert.equal(await page.locator('#gmenu [data-extra-motion="on"]').getAttribute('aria-pressed'), 'true');
    assert.equal(await page.locator('#gmenu [data-extra-motion="off"]').getAttribute('aria-pressed'), 'false');
    assert.equal(await frame.locator('#grid').getAttribute('data-layout'), 'auto');

    // Wait animation ticks so live tool sway paints onto the swap-stable canvas
    await page.waitForTimeout(280);
    const activePixels = await countNonTransparentPixels(frame);
    assert.ok(activePixels > 40, `expected visible canvas paint during live tool sway, got ${activePixels} pixels`);
    if (shotDir) {
      await page.screenshot({ path: path.join(shotDir, 'dark-settings-and-sway.png') });
    }
    await closeSettings();

    // Trigger OK flight + exact +340 popup and hover .activity to reveal inline mute button
    await frame.evaluate(() => {
      const eng = window.WsConvMotion && window.WsConvMotion.getInstance();
      if (!eng) throw new Error('WsConvMotion instance missing');
      eng.launchFlight('exec', 'ok', 340);
      eng.relayout();
    });
    await frame.hover('#r-convlive .clive .activity');
    await page.waitForTimeout(320);
    const muteOpacity = await frame.locator('#r-convlive .clive .activity [data-motion-mute]').evaluate(el => parseFloat(getComputedStyle(el).opacity));
    assert.ok(muteOpacity > 0.85, `hovering .activity must reveal mute button, opacity=${muteOpacity}`);
    if (shotDir) {
      await page.screenshot({ path: path.join(shotDir, 'dark-ok-flight-and-hover-mute.png') });
    }
    await page.waitForTimeout(680);
    if (shotDir) {
      await page.screenshot({ path: path.join(shotDir, 'dark-ok-impact-popup.png') });
    }

    // Trigger FAIL flight + +180 popup
    await frame.evaluate(() => {
      const eng = window.WsConvMotion.getInstance();
      eng.launchFlight('git', 'fail', 180);
      eng.relayout();
    });
    await page.waitForTimeout(520);
    if (shotDir) {
      await page.screenshot({ path: path.join(shotDir, 'dark-fail-flight.png') });
    }
    await page.waitForTimeout(760);
    if (shotDir) {
      await page.screenshot({ path: path.join(shotDir, 'dark-fail-impact-popup.png') });
    }

    // Click inline hover mute button inside session frame -> turns off Extra Motion, 0 rAF, hides canvas, syncs shell Settings + localStorage
    await frame.hover('#r-convlive .clive .activity');
    await frame.click('#r-convlive .clive .activity [data-motion-mute]');
    await page.waitForTimeout(120);
    const afterMuteFrame = await frame.evaluate(() => ({
      st: window.WsConvMotion.getInstance().getState(),
      ls: localStorage.getItem('ws.dashboard.extraMotion'),
      fallback: document.querySelector('#r-convlive .clive .activity').getAttribute('data-motion-fallback')
    }));
    const afterMuteShell = await page.evaluate(() => ({
      ls: localStorage.getItem('ws.dashboard.extraMotion'),
      offPressed: document.querySelector('#gmenu [data-extra-motion="off"]').getAttribute('aria-pressed'),
      onPressed: document.querySelector('#gmenu [data-extra-motion="on"]').getAttribute('aria-pressed')
    }));
    assert.equal(afterMuteFrame.st.rafActive, false, 'mute must stop rAF');
    assert.equal(afterMuteFrame.st.canvasHidden, true, 'mute must hide canvas');
    assert.equal(afterMuteFrame.ls, 'off', 'frame localStorage persisted off');
    assert.equal(afterMuteShell.ls, 'off', 'shell localStorage synced off');
    assert.equal(afterMuteShell.offPressed, 'true', 'shell Settings OFF button synced');
    assert.equal(afterMuteShell.onPressed, 'false', 'shell Settings ON button synced');
    assert.equal(afterMuteFrame.fallback, '1', 'static semantic dot fallback active');

    // Re-enable from shell Settings popover -> resumes frame motion
    await openSettings();
    await page.click('#gmenu [data-extra-motion="on"]');
    await page.waitForTimeout(200);
    const afterReenable = await frame.evaluate(() => window.WsConvMotion.getInstance().getState());
    assert.equal(afterReenable.rafActive, true, 're-enabling via shell Settings resumes rAF');
    assert.equal(afterReenable.canvasHidden, false, 're-enabling via shell Settings unhides canvas');

    // 2. Light mode visual check
    await page.click('#gmenu [data-theme-set="light"]');
    await page.waitForTimeout(160);
    await frame.evaluate(() => {
      const eng = window.WsConvMotion.getInstance();
      eng.launchFlight('tickets', 'ok', 280);
      eng.relayout();
    });
    await page.waitForTimeout(420);
    const lightPixels = await countNonTransparentPixels(frame);
    assert.ok(lightPixels > 40, `expected visible canvas paint on light theme, got ${lightPixels} pixels`);
    if (shotDir) {
      await page.screenshot({ path: path.join(shotDir, 'light-settings-and-motion.png') });
    }
    assert.deepEqual(errors, [], `unexpected page errors in dark/light run: ${errors.join('; ')}`);
    await darkCtx.close();

    // 3. prefers-reduced-motion: reduce — strict 0 rAF + static semantic dot fallback
    const redCtx = await browser.newContext({ viewport: { width: 1320, height: 860 }, colorScheme: 'dark', reducedMotion: 'reduce' });
    redCtx.setDefaultTimeout(10000);
    const rpage = await redCtx.newPage();
    const rErrors = [];
    rpage.on('pageerror', e => rErrors.push(e.message));
    await rpage.goto(base + '/s/livesession', { waitUntil: 'domcontentloaded' });
    const rframe = await waitSessionFrame(rpage);
    await rpage.waitForTimeout(150);
    const redState = await rframe.evaluate(() => ({
      st: window.WsConvMotion.getInstance().getState(),
      fallback: document.querySelector('#r-convlive .clive .activity').getAttribute('data-motion-fallback')
    }));
    assert.equal(redState.st.reducedMotion, true);
    assert.equal(redState.st.rafActive, false, 'reduced-motion must enforce 0 rAF');
    assert.equal(redState.st.canvasHidden, true, 'reduced-motion must hide canvas');
    assert.equal(redState.fallback, '1', 'reduced-motion must show static semantic dot fallback');
    if (shotDir) {
      await rpage.screenshot({ path: path.join(shotDir, 'reduced-motion-fallback.png') });
    }
    assert.deepEqual(rErrors, [], `unexpected page errors in reduced-motion run: ${rErrors.join('; ')}`);
    await redCtx.close();

    // 4. Optional mockup.html reference screenshot for visual diffing
    if (shotDir && mockupPath && fs.existsSync(mockupPath)) {
      const mCtx = await browser.newContext({ viewport: { width: 1320, height: 860 }, colorScheme: 'dark', reducedMotion: 'no-preference' });
      const mPage = await mCtx.newPage();
      await mPage.goto('file://' + mockupPath, { waitUntil: 'domcontentloaded' });
      await mPage.waitForTimeout(450);
      await mPage.screenshot({ path: path.join(shotDir, 'mockup-reference.png') });
      await mCtx.close();
    }

    console.log('Conv Extra Motion browser acceptance (dark, light, hover-mute, shell<->frame Settings sync, reduced-motion) passed.');
  } finally {
    await browser.close();
  }
})().catch(err => {
  console.error(err);
  process.exit(1);
});
