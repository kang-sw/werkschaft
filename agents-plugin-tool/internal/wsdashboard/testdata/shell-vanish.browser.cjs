// Run by TestShellVanishBrowser with a real session page and disposable stores.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const pw = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, shots] = process.argv.slice(2);

function shot(page, name) {
  if (!shots) return Promise.resolve();
  fs.mkdirSync(shots, { recursive: true });
  return page.screenshot({ path: path.join(shots, name + '.png') });
}

// pixel reads one rendered pixel of the window by decoding a 1x1 screenshot.
async function pixel(page, x, y) {
  const buf = await page.screenshot({ clip: { x, y, width: 1, height: 1 } });
  return page.evaluate(async b64 => {
    const bytes = Uint8Array.from(atob(b64), c => c.charCodeAt(0));
    const bmp = await createImageBitmap(new Blob([bytes], { type: 'image/png' }));
    const c = document.createElement('canvas');
    c.width = c.height = 1;
    const g = c.getContext('2d');
    g.drawImage(bmp, 0, 0);
    return Array.from(g.getImageData(0, 0, 1, 1).data.slice(0, 3));
  }, buf.toString('base64'));
}
// WS_SHELL_VANISH_SOFT=1 reports a failed check and goes on, to capture
// the broken state.
const soft = process.env.WS_SHELL_VANISH_SOFT === '1';
const check = (ok, msg) => { if (soft && !ok) console.log('FAILED: ' + msg); else assert.ok(ok, msg); };
const same = (a, b) => a.every((v, i) => Math.abs(v - b[i]) <= 2);

async function run(name, type) {
  let browser;
  try { browser = await type.launch({ headless: true }); } catch (e) {
    console.log(`shell vanish ${name}: SKIP (${String(e.message).split('\n')[0]})`);
    return;
  }
  try {
    for (const theme of ['dark', 'light']) {
      const context = await browser.newContext({ viewport: { width: 1920, height: 900 }, colorScheme: theme });
      await context.addInitScript(() => {
        if (self !== top) return;
        const workers = navigator.serviceWorker;
        if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
      });
      await context.route('**/*', route => new URL(route.request().url()).origin === base ? route.continue() : route.abort());
      const page = await context.newPage();
      const errors = [];
      page.on('pageerror', e => errors.push(e.message));
      await page.goto(base + '/s/livesession');
      await page.waitForFunction(() => document.querySelector('.dwrap > .top').style.minHeight !== '');
      await page.frameLocator('.sframe.on').locator('#grid').waitFor();
      await page.waitForTimeout(400);
      const tag = `${name}/${theme}`;
      const rail = await page.locator('#rail').boundingBox();
      const px = Math.round(rail.x + rail.width / 2), py = Math.round(rail.y + rail.height - 40);
      // Variant D docks the rail flush at x=0 on continuous outer chrome; compare
      // against the inset stage well just right of the rail (outside the 1300px
      // stage grid at 1920px). An opaque iframe would wipe the rail to the same
      // background as the well.
      const wx = Math.round(rail.x + rail.width + 24);
      const calm = { rail: await pixel(page, px, py), well: await pixel(page, wx, py) };
      check(!same(calm.rail, calm.well), `${tag}: rail chrome differs from the inset stage well at rest (${calm.rail} vs ${calm.well})`);
      await shot(page, `${name}-${theme}-normal`);

      const frame = page.frames().find(f => f !== page.mainFrame());
      await frame.evaluate(() => { window.__dlg = window.wsConfirm('Stop this session?'); });
      const dlg = frame.locator('dialog.ws-confirm[open]');
      await dlg.waitFor({ state: 'visible' });
      await page.waitForFunction(() => document.documentElement.hasAttribute('data-fdim'), null, { timeout: 5000 });
      await page.waitForTimeout(100);
      const during = { rail: await pixel(page, px, py), well: await pixel(page, wx, py) };
      check(!same(during.rail, during.well), `${tag}: rail stays distinct from stage well while the dialog is open (${during.rail} vs ${during.well})`);
      await shot(page, `${name}-${theme}-dialog`);

      // Hold the frame's layout report back, as a slow page would, so the
      // shell still holds the dim after the dialog is gone.
      await frame.evaluate(() => {
        const raf = window.requestAnimationFrame.bind(window);
        window.__lateRaf = true;
        window.requestAnimationFrame = cb => window.__lateRaf ? setTimeout(() => raf(cb), 1200) : raf(cb);
      });
      await dlg.locator('.pbtn:not(.danger)').click();
      await dlg.waitFor({ state: 'detached' });
      check(await page.evaluate(() => document.documentElement.hasAttribute('data-fdim')), `${tag}: shell still holds data-fdim just after close`);
      const after = { rail: await pixel(page, px, py), well: await pixel(page, wx, py) };
      check(!same(after.rail, after.well) && same(after.rail, calm.rail), `${tag}: rail stays rail-colored just after close (${after.rail} vs ${after.well}, calm ${calm.rail})`);
      await shot(page, `${name}-${theme}-after-close`);
      await frame.evaluate(() => { window.__lateRaf = false; });
      await page.waitForFunction(() => !document.documentElement.hasAttribute('data-fdim'), null, { timeout: 5000 });
      assert.deepEqual(errors, [], `${tag}: page errors`);

      // The overview picker dims the shell's rail through its own scrim.
      await frame.evaluate(() => { window.WsOverview.toggle(); });
      await page.waitForFunction(() => document.querySelector('.sframe.on').contentDocument.documentElement.hasAttribute('data-overview') && document.documentElement.hasAttribute('data-fdim'), null, { timeout: 5000 });
      await page.waitForTimeout(400);
      const ov = await pixel(page, px, py);
      const lum = c => c[0] + c[1] + c[2];
      check(lum(ov) < lum(calm.rail) * 0.6, `${tag}: rail is darker in the overview (${ov} vs ${calm.rail} at rest)`);
      check(!same(ov, await pixel(page, wx, py)) || lum(calm.rail) < 60, `${tag}: rail stays distinct from the stage well in the overview`);
      await shot(page, `${name}-${theme}-overview`);
      await context.close();
      console.log(`shell vanish ${tag}: PASS`);
    }
  } finally {
    await browser.close();
  }
}

(async () => {
  await run('chromium', pw.chromium);
  await run('webkit', pw.webkit);
})().catch(e => { console.error(e); process.exitCode = 1; });
