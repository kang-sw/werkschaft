// Run by TestCompanionSourcesPlacementBrowser with the real shell page and
// disposable stores. On a viewport taller than the dialog with Sources open,
// content that grows the dialog afterwards (a zip import preview, its result)
// must re-place it inside the viewport, a bounded number of times.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const { png, zip } = require('./companion-zip-fixture.cjs');
const [base, shots] = process.argv.slice(2);

function bundle() {
  const market = {
    schema: 'ws-companion-marketplace/v1', name: 'Fixture Pack Bundle', owner: { name: 'Fixture Author' },
    packs: [{ id: 'fixture-fox', name: 'Fixture Fox', version: '1.2.0', author: 'Fixture Author', license: 'CC0-1.0',
      description: 'A generated fixture pack.', path: 'packs/fixture-fox' }]
  };
  return zip([
    ['marketplace.json', Buffer.from(JSON.stringify(market))],
    ['packs/fixture-fox/pack.json', Buffer.from(JSON.stringify({
      schema: 'ws-companion/v1', id: 'fixture-fox', name: 'Fixture Fox', canvas: [512, 512], poses: { stand: { layers: [{ file: 'stand.png' }] } } }))],
    ['packs/fixture-fox/stand.png', png(16)],
  ]);
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1100 }, colorScheme: 'dark' });
    await context.addInitScript(() => {
      if (self !== top) return;
      const workers = navigator.serviceWorker;
      if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
      // Every placement reads the dialog's offsetHeight, and nothing else on
      // the shell reads it: the reads count place() calls.
      const height = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight');
      window.wsPlacements = 0;
      Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get() {
        if (this.classList.contains('ws-companion-settings')) window.wsPlacements++;
        return height.get.call(this);
      } });
    });
    const blocked = [];
    await context.route('**/*', route => {
      const url = new URL(route.request().url());
      if (url.origin !== base) { blocked.push(url.href); return route.abort(); }
      return route.continue();
    });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', e => errors.push(e.message));
    await page.goto(base + '/s/livesession');
    await page.frameLocator('.sframe.on').locator('#grid').waitFor();
    await page.locator('.ws-companion-hit:not([hidden])').waitFor();
    await page.waitForTimeout(400);

    const hit = await page.locator('.ws-companion-hit').boundingBox();
    await page.mouse.move(hit.x + 4, hit.y + hit.height - 6);
    await page.locator('.ws-companion-gear').click();
    const dialog = page.locator('dialog.ws-companion-settings[open]');
    await dialog.waitFor();
    const section = dialog.locator('details.ws-companion-sources');
    await section.locator('summary').click();
    await page.mouse.move(2, 2);
    // settled waits two frames: one for the resize observation, one for its placement.
    const settled = () => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const placement = () => page.evaluate(() => {
      const d = document.querySelector('dialog.ws-companion-settings[open]'), b = d.getBoundingClientRect();
      return { top: b.top, bottom: b.bottom, left: b.left, right: b.right, height: b.height, vw: innerWidth, vh: innerHeight,
        inside: b.left >= 0 && b.top >= 0 && b.right <= innerWidth && b.bottom <= innerHeight, placements: window.wsPlacements };
    });
    await settled();
    const expanded = await placement();
    assert.ok(expanded.inside, 'the expanded dialog starts inside the viewport: ' + JSON.stringify(expanded));
    assert.ok(expanded.height < expanded.vh - 12, 'the viewport is taller than the expanded dialog, so it is not height-capped: ' + JSON.stringify(expanded));

    // The import preview grows the dialog; it moves up to stay inside.
    await section.locator('input[type="file"]').setInputFiles({ name: 'fixture-fox.zip', mimeType: 'application/zip', buffer: bundle() });
    const panel = section.locator('.ws-companion-sources-panel');
    await panel.locator('input[type="checkbox"]').first().waitFor();
    await settled();
    const preview = await placement();
    assert.ok(preview.height > expanded.height, 'the preview grows the dialog: ' + expanded.height + ' -> ' + preview.height);
    assert.ok(preview.inside, 'the grown dialog stays inside the viewport: ' + JSON.stringify(preview));

    // The ZIP import control can be reached: scrolled into view inside the
    // dialog, it lies within the viewport and nothing covers it.
    const file = section.locator('input[type="file"]');
    await file.evaluate(el => el.scrollIntoView({ block: 'nearest' }));
    const reach = await file.evaluate(el => {
      const r = el.getBoundingClientRect(), x = r.left + r.width / 2, y = r.top + r.height / 2;
      return { top: r.top, bottom: r.bottom, vh: innerHeight, hit: document.elementFromPoint(x, y) === el };
    });
    assert.ok(reach.top >= 0 && reach.bottom <= reach.vh && reach.hit, 'the ZIP import control is reachable: ' + JSON.stringify(reach));

    // Import, then close the result: every size change re-places the dialog.
    await panel.locator(':scope > .ws-companion-sources-actions button').first().click();
    await section.locator('.ws-companion-sources-ok').waitFor();
    await settled();
    const result = await placement();
    assert.ok(result.inside, 'the import result stays inside the viewport: ' + JSON.stringify(result));
    await panel.locator(':scope > .ws-companion-sources-actions button').last().click();
    await settled();
    const closed = await placement();
    assert.ok(closed.inside, 'closing the result keeps the dialog inside: ' + JSON.stringify(closed));
    assert.ok(Math.abs(closed.bottom - expanded.bottom) < 1 || closed.top <= 6.5, 'the shrunk dialog returns beside the character: ' + JSON.stringify({ expanded, closed }));

    // place() moves the dialog without resizing it, so it never feeds back:
    // the flow places a bounded number of times and an idle dialog not at all.
    const flow = closed.placements - expanded.placements;
    assert.ok(flow >= 2 && flow <= 16, 'placements during the import flow: ' + flow);
    await page.waitForTimeout(600);
    assert.equal((await placement()).placements, closed.placements, 'an idle dialog is not re-placed');

    if (shots) {
      fs.mkdirSync(shots, { recursive: true });
      await page.screenshot({ path: path.join(shots, 'companion-sources-placement.png') });
    }
    assert.deepEqual(blocked, [], 'no request left the daemon');
    assert.deepEqual(errors, [], 'no page errors');
    console.log('companion sources placement browser: ok', JSON.stringify({ flow, expanded: expanded.height, preview: preview.height }));
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exit(1); });
