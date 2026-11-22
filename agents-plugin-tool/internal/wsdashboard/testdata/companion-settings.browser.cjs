// Run by TestCompanionSettingsBrowser with real pages and disposable stores.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, shots] = process.argv.slice(2);

const persona = { name: 'Fixture Fox', voice: 'Speaks in short, upbeat lines.\nNever uses more than one exclamation mark.', quipLanguage: 'en' };

async function shot(page, name) {
  if (!shots) return;
  fs.mkdirSync(shots, { recursive: true });
  const box = await page.evaluate(() => {
    const parts = [document.querySelector('.ws-companion-hit'), document.querySelector('.ws-companion-settings[open]'),
      document.querySelector('.ws-companion-quip:not([hidden])')].filter(Boolean).map(el => el.getBoundingClientRect());
    const l = Math.min(...parts.map(b => b.left)), t = Math.min(...parts.map(b => b.top));
    const r = Math.max(...parts.map(b => b.right)), b = Math.max(...parts.map(b => b.bottom));
    return { x: Math.max(0, l - 40), y: Math.max(0, t - 40), width: Math.min(innerWidth, r + 40) - Math.max(0, l - 40),
      height: Math.min(innerHeight, b + 40) - Math.max(0, t - 40) };
  });
  await page.screenshot({ path: path.join(shots, name + '.png'), clip: box });
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
    const writes = [];
    await context.route('**/*', async route => {
      const request = route.request(), url = new URL(request.url());
      if (url.origin !== base) return route.abort();
      if (url.pathname === '/api/config/set') {
        writes.push(JSON.parse(request.postData()));
        return route.fulfill({ json: { ok: true } });
      }
      if (url.pathname === '/api/companion/packs' && request.method() === 'GET') {
        // The fixture's default pack has no persona; the catalog carries one.
        const response = await route.fetch();
        const catalog = await response.json();
        for (const pack of catalog.packs) if (pack.id === 'default') pack.persona = persona;
        return route.fulfill({ response, json: catalog });
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

    // The gear sits directly under the resize grip and appears on hover.
    const gear = page.locator('.ws-companion-gear');
    // The grip and gear fill the hit box's top-right column; hover its lower left.
    const hoverCharacter = async () => {
      const box = await page.locator('.ws-companion-hit').boundingBox();
      await page.mouse.move(box.x + 4, box.y + box.height - 6);
    };
    assert.equal(await gear.evaluate(el => getComputedStyle(el).opacity), '0', 'gear hidden until hover');
    await hoverCharacter();
    await page.waitForTimeout(100);
    const placement = await page.evaluate(() => {
      const grip = document.querySelector('.ws-companion-resize').getBoundingClientRect();
      const gear = document.querySelector('.ws-companion-gear').getBoundingClientRect();
      return { opacity: Number(getComputedStyle(document.querySelector('.ws-companion-gear')).opacity), gripBottom: grip.bottom,
        gearTop: gear.top, sameColumn: Math.abs(grip.right - gear.right) < 1 };
    });
    assert.ok(placement.opacity > 0.3, 'gear revealed on hover: ' + placement.opacity);
    assert.ok(placement.sameColumn && placement.gearTop >= placement.gripBottom - 0.5 && placement.gearTop - placement.gripBottom < 6,
      'gear directly under the grip: ' + JSON.stringify(placement));
    await shot(page, 'companion-gear-hover');

    // The gear opens a non-blocking dialog beside the companion.
    await gear.click();
    const dialog = page.locator('dialog.ws-companion-settings[open]');
    await dialog.waitFor();
    await page.waitForTimeout(150);
    const modal = await page.evaluate(() => {
      const d = document.querySelector('dialog.ws-companion-settings');
      const b = d.getBoundingClientRect(), hit = document.querySelector('.ws-companion-hit').getBoundingClientRect();
      return { modal: d.matches(':modal'), l: b.left, t: b.top, r: b.right, b: b.bottom, vw: innerWidth, vh: innerHeight,
        beside: b.left >= hit.right - 0.5 || b.right <= hit.left + 0.5,
        expanded: document.querySelector('.ws-companion-gear').getAttribute('aria-expanded'),
        labels: [...d.querySelectorAll('.ws-companion-settings-label')].map(e => e.textContent),
        persona: d.querySelector('.ws-companion-settings-persona').textContent,
        controls: d.querySelectorAll('select, input').length };
    });
    assert.equal(modal.modal, false, 'the settings dialog is non-modal');
    assert.ok(modal.l >= 0 && modal.t >= 0 && modal.r <= modal.vw && modal.b <= modal.vh, 'dialog inside the viewport: ' + JSON.stringify(modal));
    assert.ok(modal.beside, 'dialog beside the companion');
    assert.equal(modal.expanded, 'true');
    assert.deepEqual(modal.labels, ['Pack', 'Preset', 'Agent cooldown', 'Agent hourly cap']);
    assert.equal(modal.controls, 4);
    assert.match(modal.persona, /Fixture Fox/);
    assert.match(modal.persona, /short, upbeat lines/);
    // The page behind stays usable while the dialog is open.
    assert.equal(await page.evaluate(() => document.querySelector('.sframe.on').inert), false);
    await shot(page, 'companion-settings-modal');

    // A number write goes to the global layer as an integer.
    const cooldown = dialog.locator('input[type="number"]').first();
    await cooldown.fill('45');
    await cooldown.press('Tab');
    await page.waitForTimeout(150);
    assert.deepEqual(writes.at(-1), { scope: 'global', key: 'dashboard.companion_agent_cooldown_seconds', value: 45 });
    await dialog.locator('select').first().waitFor();

    // Escape, a second gear click, and the close button each close it.
    await page.keyboard.press('Escape');
    await dialog.waitFor({ state: 'detached' }).catch(() => {});
    assert.equal(await page.locator('dialog.ws-companion-settings[open]').count(), 0, 'Escape closes');
    await hoverCharacter();
    await gear.click();
    await dialog.waitFor();
    await gear.click();
    assert.equal(await page.locator('dialog.ws-companion-settings[open]').count(), 0, 'second gear click closes');
    await gear.click();
    await dialog.locator('.ws-companion-settings-close').click();
    assert.equal(await page.locator('dialog.ws-companion-settings[open]').count(), 0, 'close button closes');
    await page.mouse.move(5, 5);

    // A directed command for another session is ignored; one for the shown
    // session plays its quip as text that never takes the pointer.
    const send = command => page.evaluate(detail => document.dispatchEvent(new CustomEvent('ws-companion-command', { detail })), command);
    await send({ sessionId: 'some-other-session', quip: 'not for this window', ttl_ms: 3000 });
    await page.waitForTimeout(200);
    assert.equal(await page.locator('.ws-companion-quip:not([hidden])').count(), 0, 'other-session command ignored');
    const quipText = '<b>Nice</b> commit — on to the next one';
    await send({ sessionId: 'livesession', expression: 'happy', fx: 'sparkle', gesture: 'no-such-gesture', quip: quipText, ttl_ms: 2500 });
    const quip = page.locator('.ws-companion-quip:not([hidden])');
    await quip.waitFor();
    await page.waitForTimeout(300);
    const bubble = await page.evaluate(() => {
      const q = document.querySelector('.ws-companion-quip');
      const b = q.getBoundingClientRect();
      const under = document.elementFromPoint(b.left + b.width / 2, b.top + b.height / 2);
      return { text: q.textContent, children: q.children.length, pointer: getComputedStyle(q).pointerEvents,
        blocks: !!under && q.contains(under), scenario: document.querySelector('.ws-companion').dataset.scenario };
    });
    assert.equal(bubble.text, quipText, 'quip rendered as text');
    assert.equal(bubble.children, 0, 'no markup from the quip');
    assert.equal(bubble.pointer, 'none');
    assert.equal(bubble.blocks, false, 'the quip never takes the pointer');
    assert.equal(bubble.scenario, 'directed');
    await shot(page, 'companion-directed-quip');
    await page.waitForFunction(() => !document.querySelector('.ws-companion-quip:not([hidden])'), null, { timeout: 5000 });
    assert.equal(await page.evaluate(() => document.querySelector('.ws-companion').dataset.scenario), '', 'rules engine resumes after the TTL');

    assert.deepEqual(errors, [], 'no page errors');
    console.log('companion settings browser: ok', JSON.stringify({ writes: writes.length }));
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exit(1); });
