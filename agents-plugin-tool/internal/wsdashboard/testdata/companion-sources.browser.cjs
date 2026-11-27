// Run by TestCompanionSourcesBrowser with real Dashboard Settings and disposable
// stores. No source URL is ever fetched automatically: the one configured user
// source is never clicked, and every request outside the daemon is aborted and counted.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const { png, zip } = require('./companion-zip-fixture.cjs');
const [base, shots, userSource] = process.argv.slice(2);

function bundle() {
  const packs = ['alpha', 'beta'];
  const market = {
    schema: 'ws-companion-marketplace/v1', name: '<b>Fan</b> Pack Bundle', owner: { name: 'Fixture Author', url: 'https://example.invalid/' },
    packs: packs.map(id => ({ id, name: 'Pack <i>' + id + '</i>', version: '1.0.0', author: 'Fixture Author', license: 'CC0-1.0',
      description: 'A generated fixture pack.', path: 'packs/' + id }))
  };
  const files = [['marketplace.json', Buffer.from(JSON.stringify(market))]];
  for (const id of packs) {
    files.push(['packs/' + id + '/pack.json', Buffer.from(JSON.stringify({
      schema: 'ws-companion/v1', id, name: id, canvas: [512, 512], poses: { stand: { layers: [{ file: 'stand.png' }] } } }))]);
    files.push(['packs/' + id + '/stand.png', png(8 + packs.indexOf(id))]);
  }
  return zip(files);
}

async function shot(page, name) {
  if (!shots) return;
  fs.mkdirSync(shots, { recursive: true });
  await page.screenshot({ path: path.join(shots, name + '.png') });
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1360, height: 900 }, colorScheme: 'dark' });
    await context.addInitScript(() => {
      if (self !== top) return;
      const workers = navigator.serviceWorker;
      if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
    });
    const blocked = [], sourceCalls = [], importCalls = [];
    await context.route('**/*', async route => {
      const request = route.request(), url = new URL(request.url());
      if (url.origin !== base) { blocked.push(request.url()); return route.abort(); }
      if (url.pathname === '/api/companion/sources/fetch') sourceCalls.push(JSON.parse(request.postData()));
      if (url.pathname.startsWith('/api/companion/packs/') && request.method() === 'POST') {
        importCalls.push({ path: url.pathname + url.search, bytes: request.postDataBuffer() });
      }
      return route.continue();
    });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', e => errors.push(e.message));

    await page.goto(base + '/s/livesession');
    await page.frameLocator('.sframe.on').locator('#grid').waitFor();
    await page.locator('.ws-companion-hit:not([hidden])').waitFor();
    await page.waitForTimeout(300);

    // Open Dashboard Settings -> Companion section via the gear button.
    const hit = await page.locator('.ws-companion-hit').boundingBox();
    await page.mouse.move(hit.x + 4, hit.y + hit.height - 6);
    await page.locator('.ws-companion-gear').click();
    await page.locator('#workflow-settings[open]').waitFor();
    const sframe = page.frameLocator('#workflow-settings[open] > iframe');
    const panel = sframe.locator('[data-companion-settings-panel]');
    await panel.waitFor();

    // Pack Sources group: single calm trust notice, Official + configured user source.
    const sourcesGroup = panel.locator('[data-companion-group="sources"]');
    await sourcesGroup.waitFor();
    assert.equal(await sourcesGroup.locator('.cs-trust-notice').count(), 1, 'single calm trust notice');
    const items = sourcesGroup.locator('[data-companion-source-item]');
    assert.equal(await items.count(), 2, 'official plus configured user source');
    assert.equal(await items.nth(0).getAttribute('data-companion-source-item'), 'official');
    assert.equal(await items.nth(0).locator('[data-companion-source-remove]').count(), 0, 'official source cannot be removed');
    assert.equal(await items.nth(1).getAttribute('data-companion-source-item'), userSource);
    assert.equal(await items.nth(1).locator('.cs-source-url').textContent(), userSource);
    assert.equal(await sourcesGroup.locator('[data-companion-source-input]').isEnabled(), true);

    // Browse Official source: fetches only official, displays built-in default card with no install button.
    await items.nth(0).locator('[data-companion-source-fetch="official"]').click();
    const catalogCard = items.nth(0).locator('[data-companion-catalog-pack="default"]');
    await catalogCard.waitFor();
    assert.deepEqual(sourceCalls, [{ source: 'official' }], 'only official source fetched');
    assert.equal(await catalogCard.locator('button').count(), 0, 'built-in pack has no install action');
    await shot(page, 'companion-sources-official');

    // Open Advanced accordion -> local .zip bundle validate + preview.
    const advGroup = panel.locator('[data-companion-group="advanced"]');
    const advToggle = advGroup.locator('[data-companion-advanced-toggle]');
    assert.equal(await advToggle.getAttribute('aria-expanded'), 'false', 'Advanced starts collapsed');
    await advToggle.click();
    assert.equal(await advToggle.getAttribute('aria-expanded'), 'true', 'Advanced opens on click');

    const zipBytes = bundle();
    await advGroup.locator('[data-companion-zip-input]').setInputFiles({ name: 'fan-pack.zip', mimeType: 'application/zip', buffer: zipBytes });
    const zipPreview = advGroup.locator('.cs-zip-preview');
    await zipPreview.waitFor();
    assert.equal(importCalls.length, 1);
    assert.equal(importCalls[0].path, '/api/companion/packs/validate?name=fan-pack.zip');
    assert.ok(importCalls[0].bytes.equals(zipBytes), 'validate received generated zip bytes');

    const preview = await zipPreview.evaluate(el => ({
      text: el.textContent,
      boxes: [...el.querySelectorAll('input[type="checkbox"]')].map(b => [b.checked, b.disabled]),
      markup: el.querySelectorAll('b, i').length,
      importDisabled: el.querySelector('[data-companion-zip-import]')?.disabled
    }));
    assert.deepEqual(preview.boxes, [[true, false], [true, false]], 'both valid packs preselected');
    assert.match(preview.text, /<b>Fan<\/b> Pack Bundle/, 'marketplace name escaped as plain text');
    assert.match(preview.text, /Pack <i>alpha<\/i>/, 'pack name escaped as plain text');
    assert.equal(preview.markup, 0, 'no HTML elements injected from zip metadata');
    assert.match(preview.text, /fan-pack\.zip/);
    assert.equal(preview.importDisabled, false);
    await shot(page, 'companion-sources-zip-preview');

    // Uncheck beta and import only alpha; installed pack list refreshes.
    await zipPreview.locator('input[type="checkbox"]').nth(1).uncheck();
    await zipPreview.locator('[data-companion-zip-import]').click();
    await advGroup.locator('.cs-feedback.ok').waitFor();
    assert.equal(importCalls.length, 2);
    assert.equal(importCalls[1].path, '/api/companion/packs/import?name=fan-pack.zip&id=alpha');
    assert.ok(importCalls[1].bytes.equals(zipBytes), 'import re-sent exact zip bytes');
    await panel.locator('[data-companion-pack-row="alpha"]').waitFor();
    assert.deepEqual(await page.evaluate(() => fetch('/api/companion/packs').then(r => r.json()).then(c => c.packs.map(p => p.id).sort())), ['alpha', 'default']);

    // Inline 2-step delete confirmation (no window.confirm): Delete -> Cancel -> Delete -> Confirm.
    const alphaRow = panel.locator('[data-companion-pack-row="alpha"]');
    await alphaRow.locator('[data-companion-pack-delete="alpha"]').click();
    await alphaRow.locator('[data-companion-pack-confirm="alpha"]').waitFor();
    await alphaRow.locator('[data-companion-pack-confirm="alpha"] button').nth(1).click();
    assert.equal(await alphaRow.locator('[data-companion-pack-confirm="alpha"]').count(), 0, 'cancel dismisses inline confirm');
    await alphaRow.locator('[data-companion-pack-delete="alpha"]').click();
    await alphaRow.locator('[data-companion-pack-confirm-yes="alpha"]').click();
    await alphaRow.waitFor({ state: 'detached' });
    assert.equal(importCalls.at(-1).path, '/api/companion/packs/alpha/uninstall');
    assert.deepEqual(await page.evaluate(() => fetch('/api/companion/packs').then(r => r.json()).then(c => c.packs.map(p => p.id).sort())), ['default']);

    // Partial-error 200 import shows alert with written count and failing pack error (not ok banner).
    await context.route('**/api/companion/packs/import*', route => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        listing: {
          source: 'fan-pack.zip', kind: 'local', official: false, name: 'Fan',
          packs: [
            { id: 'alpha', name: 'Pack alpha', version: '1.0.0', status: 'available', installed: true },
            { id: 'beta', name: 'Pack beta', version: '1.0.0', status: 'available', error: { id: 'beta', path: '', message: 'cannot move the staged pack into place' } }
          ],
          errors: []
        }
      })
    }));
    await advGroup.locator('[data-companion-zip-input]').setInputFiles({ name: 'fan-pack.zip', mimeType: 'application/zip', buffer: zipBytes });
    await zipPreview.waitFor();
    await zipPreview.locator('[data-companion-zip-import]').click();
    const alert = advGroup.locator('.cs-feedback.err[role="alert"]');
    await alert.waitFor();
    assert.equal(await advGroup.locator('.cs-feedback.ok').count(), 0, 'partial result is not announced as success');
    assert.match(await alert.textContent(), /beta: cannot move the staged pack into place/);
    assert.match(await alert.textContent(), /1\/2/);
    await context.unroute('**/api/companion/packs/import*');

    // Authoring links point at the daemon's three guide routes and each returns 200.
    const links = await advGroup.locator('.cs-doc-link').evaluateAll(list => list.map(a => ({ href: a.getAttribute('href'), rel: a.rel, target: a.target })));
    assert.deepEqual(links.map(l => l.href), ['/api/companion/authoring/guide.md', '/api/companion/authoring/pack.schema.json', '/api/companion/authoring/marketplace.schema.json']);
    assert.ok(links.every(l => l.rel === 'noopener noreferrer' && l.target === '_blank'));
    for (const { href } of links) {
      const served = await page.evaluate(async url => { const r = await fetch(url); return { status: r.status }; }, href);
      assert.equal(served.status, 200, href + ' is served');
    }

    // Zero horizontal overflow across the whole section.
    const overflow = await panel.evaluate(() => {
      const sc = document.querySelector('.scontent') || document.documentElement;
      return sc.scrollWidth > sc.clientWidth + 1;
    });
    assert.equal(overflow, false, 'no horizontal overflow');
    assert.deepEqual(sourceCalls, [{ source: 'official' }], 'configured user source was never fetched');
    assert.deepEqual(blocked, [], 'no request left the daemon');
    assert.deepEqual(errors, [], 'no page errors');
    console.log('companion sources browser: ok', JSON.stringify({ imports: importCalls.length }));
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exit(1); });
