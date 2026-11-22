// Run by TestCompanionSourcesBrowser with the real shell page and disposable
// stores. No source URL is ever fetched: the one configured user source is
// never clicked, and every request outside the daemon is aborted and counted.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const zlib = require('node:zlib');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, shots, userSource] = process.argv.slice(2);

const crcTable = Array.from({ length: 256 }, (_, n) => {
  let c = n;
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
  return c >>> 0;
});
function crc32(buf) {
  let c = 0xffffffff;
  for (const b of buf) c = crcTable[(c ^ b) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function png(size) {
  const chunk = (type, data) => {
    const body = Buffer.concat([Buffer.from(type), data]);
    const out = Buffer.alloc(12 + data.length);
    out.writeUInt32BE(data.length, 0);
    body.copy(out, 4);
    out.writeUInt32BE(crc32(body), 8 + data.length);
    return out;
  };
  const head = Buffer.alloc(13);
  head.writeUInt32BE(size, 0);
  head.writeUInt32BE(size, 4);
  head[8] = 8;
  head[9] = 6;
  const row = Buffer.alloc(1 + size * 4, 0x80);
  row[0] = 0;
  const raw = Buffer.concat(Array.from({ length: size }, () => row));
  return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk('IHDR', head),
    chunk('IDAT', zlib.deflateSync(raw)), chunk('IEND', Buffer.alloc(0))]);
}

// zip writes a Store-only archive of regular files.
function zip(files) {
  const locals = [], central = [];
  let offset = 0;
  for (const [name, data] of files) {
    const nameBytes = Buffer.from(name), crc = crc32(data);
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(0x0800, 6);
    local.writeUInt32LE(crc, 14);
    local.writeUInt32LE(data.length, 18);
    local.writeUInt32LE(data.length, 22);
    local.writeUInt16LE(nameBytes.length, 26);
    const entry = Buffer.alloc(46);
    entry.writeUInt32LE(0x02014b50, 0);
    entry.writeUInt16LE(20, 4);
    entry.writeUInt16LE(20, 6);
    entry.writeUInt16LE(0x0800, 8);
    entry.writeUInt32LE(crc, 16);
    entry.writeUInt32LE(data.length, 20);
    entry.writeUInt32LE(data.length, 24);
    entry.writeUInt16LE(nameBytes.length, 28);
    entry.writeUInt32LE((0o100644 << 16) >>> 0, 38);
    entry.writeUInt32LE(offset, 42);
    locals.push(local, nameBytes, data);
    central.push(entry, nameBytes);
    offset += 30 + nameBytes.length + data.length;
  }
  const dir = Buffer.concat(central);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(files.length, 8);
  end.writeUInt16LE(files.length, 10);
  end.writeUInt32LE(dir.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, dir, end]);
}

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

async function shotDialog(page, name) {
  if (!shots) return;
  fs.mkdirSync(shots, { recursive: true });
  const box = await page.locator('dialog.ws-companion-settings[open]').boundingBox();
  await page.screenshot({ path: path.join(shots, name + '.png'),
    clip: { x: Math.max(0, box.x - 12), y: Math.max(0, box.y - 12), width: Math.min(1280, box.width + 24), height: Math.min(800, box.height + 24) } });
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, colorScheme: 'dark' });
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
    await page.waitForTimeout(400);
    assert.ok(await page.evaluate(() => document.documentElement.hasAttribute('data-sysopen')), 'the local shell may write settings');

    const box = await page.locator('.ws-companion-hit').boundingBox();
    await page.mouse.move(box.x + 4, box.y + box.height - 6);
    await page.locator('.ws-companion-gear').click();
    const dialog = page.locator('dialog.ws-companion-settings[open]');
    await dialog.waitFor();
    const section = dialog.locator('details.ws-companion-sources');
    assert.equal(await section.evaluate(el => el.open), false, 'the Sources section starts collapsed');
    const narrow = await dialog.evaluate(d => d.getBoundingClientRect().width);
    await section.locator('summary').click();
    await page.waitForTimeout(200);

    // Layout: the dialog widens, stays inside the viewport, and scrolls on its own.
    const layout = await page.evaluate(() => {
      const d = document.querySelector('dialog.ws-companion-settings'), b = d.getBoundingClientRect();
      return { width: b.width, inside: b.left >= 0 && b.top >= 0 && b.right <= innerWidth && b.bottom <= innerHeight, modal: d.matches(':modal') };
    });
    assert.ok(layout.width > narrow, `the dialog widens for Sources: ${narrow} -> ${layout.width}`);
    assert.ok(layout.inside && !layout.modal, 'still floating and inside the viewport: ' + JSON.stringify(layout));

    // The official source is always there and carries no warning; the user source shows its full URL and the warning.
    const rows = section.locator('.ws-companion-sources-source');
    assert.equal(await rows.count(), 2, 'the official source plus the configured one');
    assert.match(await rows.nth(0).textContent(), /official/i);
    assert.equal(await rows.nth(0).locator('.ws-companion-sources-warn').count(), 0, 'no warning for the official source');
    assert.equal(await rows.nth(0).getByRole('button', { name: /remove/i }).count(), 0, 'the official source cannot be removed');
    assert.equal(await rows.nth(1).locator('.ws-companion-sources-url').textContent(), userSource, 'the full source URL');
    assert.equal(await rows.nth(1).locator('a.ws-companion-sources-url').getAttribute('rel'), 'noopener noreferrer');
    assert.equal(await rows.nth(1).locator('.ws-companion-sources-warn').count(), 1, 'the warning on a user source');
    assert.ok(await section.locator('input[type="text"]').isEnabled(), 'the local shell may add a source');

    // Show packs on the official source: its built-in default pack, no warning.
    await rows.nth(0).getByRole('button').first().click();
    const panel = section.locator('.ws-companion-sources-panel');
    await panel.waitFor();
    assert.deepEqual(sourceCalls, [{ source: 'official' }], 'only the official source was fetched');
    const official = await panel.evaluate(el => ({
      cards: [...el.querySelectorAll('.ws-companion-sources-card')].map(card => ({
        text: card.textContent, buttons: card.querySelectorAll('button').length,
        badges: [...card.querySelectorAll('.ws-companion-sources-badge')].map(b => b.textContent) })),
      warn: el.querySelectorAll('.ws-companion-sources-warn').length }));
    assert.equal(official.warn, 0, 'no warning on the official listing');
    assert.equal(official.cards.length, 1);
    assert.match(official.cards[0].text, /default/);
    assert.equal(official.cards[0].badges.length, 1, 'a built-in badge');
    assert.equal(official.cards[0].buttons, 0, 'the built-in pack has no action');
    await dialog.evaluate(d => d.querySelector('.ws-companion-sources').scrollIntoView({ block: 'start' }));
    await shotDialog(page, 'companion-sources-official');

    // Local import preview of a generated zip, then the import itself.
    const zipBytes = bundle();
    await section.locator('input[type="file"]').setInputFiles({ name: 'fan-pack.zip', mimeType: 'application/zip', buffer: zipBytes });
    await panel.locator('input[type="checkbox"]').first().waitFor();
    assert.equal(importCalls.length, 1);
    assert.equal(importCalls[0].path, '/api/companion/packs/validate?name=fan-pack.zip');
    assert.ok(importCalls[0].bytes.equals(zipBytes), 'validate received the generated zip bytes');
    const preview = await panel.evaluate(el => ({
      text: el.textContent, boxes: [...el.querySelectorAll('input[type="checkbox"]')].map(b => [b.checked, b.disabled]),
      markup: el.querySelectorAll('b, i').length, warn: el.querySelectorAll('.ws-companion-sources-warn').length,
      importDisabled: [...el.querySelectorAll('button')].find(b => /import/i.test(b.textContent))?.disabled }));
    assert.deepEqual(preview.boxes, [[true, false], [true, false]], 'both passing packs are preselected');
    assert.match(preview.text, /<b>Fan<\/b> Pack Bundle/, 'marketplace text shown as text');
    assert.match(preview.text, /Pack <i>alpha<\/i>/);
    assert.equal(preview.markup, 0, 'no element came from pack text');
    assert.match(preview.text, /fan-pack\.zip/, 'the local file origin');
    assert.equal(preview.warn, 1, 'a local import carries the warning');
    assert.equal(preview.importDisabled, false);
    await dialog.evaluate(d => d.querySelector('.ws-companion-sources-panel').scrollIntoView({ block: 'start' }));
    await shotDialog(page, 'companion-sources-import-preview');

    // Import only alpha; the same bytes go back with the chosen id.
    await panel.locator('input[type="checkbox"]').nth(1).uncheck();
    await panel.getByRole('button', { name: /^import/i }).click();
    await section.locator('.ws-companion-sources-ok').waitFor();
    assert.equal(importCalls.length, 2);
    assert.equal(importCalls[1].path, '/api/companion/packs/import?name=fan-pack.zip&id=alpha');
    assert.ok(importCalls[1].bytes.equals(zipBytes), 'import re-sent the same bytes');
    const installed = await section.locator('.ws-companion-sources-group', { hasText: /alpha/ }).last().textContent();
    assert.match(installed, /alpha/);
    assert.deepEqual(await page.evaluate(() => fetch('/api/companion/packs').then(r => r.json()).then(c => c.packs.map(p => p.id).sort())), ['alpha', 'default']);
    await dialog.evaluate(d => { d.scrollTop = d.scrollHeight; });
    await shotDialog(page, 'companion-sources-imported');

    // Uninstall removes it again.
    await section.locator('.ws-companion-sources-card', { hasText: /alpha/ }).last().getByRole('button', { name: /uninstall/i }).click();
    await section.locator('.ws-companion-sources-ok', { hasText: /alpha/ }).waitFor();
    assert.equal(importCalls.at(-1).path, '/api/companion/packs/alpha/uninstall');
    assert.deepEqual(await page.evaluate(() => fetch('/api/companion/packs').then(r => r.json()).then(c => c.packs.map(p => p.id).sort())), ['default']);

    assert.deepEqual(sourceCalls, [{ source: 'official' }], 'the configured user source was never fetched');
    assert.deepEqual(blocked, [], 'no request left the daemon');
    assert.deepEqual(errors, [], 'no page errors');
    console.log('companion sources browser: ok', JSON.stringify({ imports: importCalls.length }));
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exit(1); });
