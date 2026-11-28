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
  await page.screenshot({ path: path.join(shots, name + '.png') });
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 840 }, colorScheme: 'dark' });
    await context.addInitScript(() => {
      if (self !== top) return;
      const workers = navigator.serviceWorker;
      if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
    });
    const writes = [];
    await context.route('**/*', async route => {
      const request = route.request(), url = new URL(request.url());
      if (url.origin !== base) return route.abort();
      if (url.pathname === '/api/config/set' && request.method() === 'POST') {
        writes.push(JSON.parse(request.postData()));
        return route.continue();
      }
      if (url.pathname === '/api/companion/packs' && request.method() === 'GET') {
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
    const hoverCharacter = async () => {
      const box = await page.locator('.ws-companion-hit').boundingBox();
      await page.mouse.move(box.x + 4, box.y + box.height - 6);
    };
    assert.equal(await gear.evaluate(el => getComputedStyle(el).opacity), '0', 'gear hidden until hover');
    await hoverCharacter();
    await page.waitForTimeout(100);
    const placement = await page.evaluate(() => {
      const grip = document.querySelector('.ws-companion-resize').getBoundingClientRect();
      const gearEl = document.querySelector('.ws-companion-gear').getBoundingClientRect();
      return { opacity: Number(getComputedStyle(document.querySelector('.ws-companion-gear')).opacity), gripBottom: grip.bottom,
        gearTop: gearEl.top, sameColumn: Math.abs(grip.right - gearEl.right) < 1 };
    });
    assert.ok(placement.opacity > 0.3, 'gear revealed on hover: ' + placement.opacity);
    assert.ok(placement.sameColumn && placement.gearTop >= placement.gripBottom - 0.5 && placement.gearTop - placement.gripBottom < 6,
      'gear directly under the grip: ' + JSON.stringify(placement));
    await shot(page, 'companion-gear-hover');

    // Clicking the gear opens Dashboard Settings on ?section=companion (no legacy floating modal).
    await gear.click();
    assert.equal(await page.locator('dialog.ws-companion-settings').count(), 0, 'retired floating modal must not render');
    const settingsDialog = page.locator('#workflow-settings[open]');
    await settingsDialog.waitFor();
    const sframe = page.frameLocator('#workflow-settings[open] > iframe');
    const panel = sframe.locator('[data-companion-settings-panel]');
    await panel.waitFor();
    await sframe.locator('[data-companion-wardrobe-grid] [data-config-key="dashboard.companion_preset"] select').waitFor();

    // Verify all 5 groups, persona card, 2D wardrobe controls, and zero horizontal overflow.
    const state = await panel.evaluate(el => {
      const sc = document.querySelector('.scontent') || document.documentElement;
      const groups = [...el.querySelectorAll('[data-companion-group]')].map(g => g.dataset.companionGroup);
      const personaText = el.querySelector('.cs-avatar-persona')?.textContent || '';
      const wardrobeSelects = [...el.querySelectorAll('[data-companion-wardrobe-grid] select')];
      const colors = [...el.querySelectorAll('[data-companion-wardrobe-grid] input[type="color"]')].map(i => i.getAttribute('aria-label'));
      const cooldownInput = el.querySelector('[data-config-key="dashboard.companion_agent_cooldown_seconds"] input');
      const capInput = el.querySelector('[data-config-key="dashboard.companion_agent_hourly_cap"] input');
      return {
        groups,
        personaText,
        selectCount: wardrobeSelects.length,
        presetVal: wardrobeSelects[0]?.value,
        colors,
        cooldownDisabled: cooldownInput?.disabled,
        capDisabled: capInput?.disabled,
        overflow: sc.scrollWidth > sc.clientWidth + 1
      };
    });
    assert.deepEqual(state.groups, ['core', 'avatar', 'memory', 'sources', 'wardrobe', 'advanced']);
    // Verify Memory / Soul drawer renders editors, scrubber approval flow, episodes, and sync tabs cleanly in Chromium.
    await panel.locator('[data-companion-soul-editor="profile.md"]').waitFor();
    await panel.locator('[data-companion-soul-editor="profile.md"]').fill('Contact dev@corp.example for weekly recap');
    await panel.locator('[data-companion-soul-save="profile.md"]').click();
    await panel.locator('[data-companion-memory-feedback="warn"]').waitFor();
    await panel.locator('[data-companion-memory-tab="approvals"]').click();
    await panel.locator('[data-companion-pending-list]').waitFor();
    await panel.locator('[data-companion-pending-editor]').first().fill('Prefers concise weekly recaps.');
    await panel.locator('[data-companion-pending-approve]').first().click();
    await panel.locator('[data-companion-memory-feedback="ok"]').waitFor();
    await panel.locator('[data-companion-memory-tab="episodes"]').click();
    await panel.locator('[data-companion-episodes-search]').waitFor();
    await panel.locator('[data-companion-memory-tab="sync"]').click();
    await panel.locator('[data-companion-soul-export]').waitFor();
    await panel.locator('[data-companion-memory-tab="soul"]').click();
    assert.match(state.personaText, /Fixture Fox/);
    assert.match(state.personaText, /short, upbeat lines/);
    assert.equal(state.selectCount, 1, 'default fixture exposes preset select (no unused empty outfit/hair/eyes selects)');
    assert.equal(state.presetVal, 'werkschaft-d1');
    for (const channel of ['eye', 'hair', 'outfitAccent', 'outfitPrimary', 'skin']) {
      assert.ok(state.colors.includes(channel), 'color swatch for ' + channel + ': ' + state.colors);
    }
    assert.equal(state.cooldownDisabled, true, 'cooldown gated when director=rules');
    assert.equal(state.capDisabled, true, 'hourly cap gated when director=rules');
    assert.equal(state.overflow, false, 'zero horizontal overflow in companion settings');
    await shot(page, 'companion-settings-dashboard');

    // Switch director to "agent" -> cooldown & hourly cap unlock -> write integer 45.
    const directorRow = panel.locator('[data-config-key="dashboard.companion_director"]');
    await directorRow.locator('.seg button').nth(1).click();
    await directorRow.locator('button[type="submit"]').click();
    await page.waitForTimeout(250);
    const cooldownRow = panel.locator('[data-config-key="dashboard.companion_agent_cooldown_seconds"]');
    await cooldownRow.locator('input[type="number"]').waitFor();
    assert.equal(await cooldownRow.locator('input[type="number"]').isDisabled(), false, 'cooldown enabled under agent director');
    await cooldownRow.locator('input[type="number"]').fill('45');
    await cooldownRow.locator('button[type="submit"]').click();
    await page.waitForTimeout(250);
    assert.ok(writes.some(w => w.scope === 'global' && w.key === 'dashboard.companion_agent_cooldown_seconds' && w.value === 45),
      'cooldown integer write recorded: ' + JSON.stringify(writes));

    // Palette color pick writes composed channel pair to global layer.
    const skinPicker = panel.locator('[data-companion-wardrobe-grid] input[type="color"][aria-label="skin"]');
    await skinPicker.evaluate(el => {
      el.value = '#aabbcc';
      el.dispatchEvent(new Event('change', { bubbles: true }));
    });
    await page.waitForTimeout(250);
    assert.ok(writes.some(w => w.scope === 'global' && w.key === 'dashboard.companion_palette' && w.value === 'skin=#aabbcc'),
      'palette write recorded: ' + JSON.stringify(writes));

    // Toggle companion_enabled OFF -> disabled-but-editable banner appears, while VRM dropzone & sources remain editable.
    const enabledRow = panel.locator('[data-config-key="dashboard.companion_enabled"]');
    await enabledRow.locator('button[role="switch"]').click();
    await enabledRow.locator('button[type="submit"]').click();
    await panel.locator('[data-companion-disabled-banner]').waitFor();
    assert.equal(await panel.locator('[data-companion-status-pill]').getAttribute('data-companion-status-pill'), 'off');
    assert.equal(await panel.locator('[data-companion-vrm-choose]').isDisabled(), false, 'VRM upload stays editable when disabled');
    assert.equal(await panel.locator('[data-companion-source-input]').isDisabled(), false, 'Pack Sources stays editable when disabled');

    // Re-enable companion so we can also verify quick-menu -> settings handoff and directed quip.
    const enabledRow2 = panel.locator('[data-config-key="dashboard.companion_enabled"]');
    await enabledRow2.locator('button[role="switch"]').click();
    await enabledRow2.locator('button[type="submit"]').click();
    await panel.locator('[data-companion-status-pill="on"]').waitFor();

    // Close Dashboard Settings via close button.
    await sframe.locator('[data-settings-close]').click();
    await page.locator('#workflow-settings').waitFor({ state: 'hidden' });

    // Re-ensure companion stage is visible, then open quick menu and verify "Companion settings" opens Dashboard Settings.
    await page.evaluate(() => {
      document.dispatchEvent(new CustomEvent('ws-companion-config', {
        detail: { enabled: true, director: 'rules', pack: 'default', preset: 'werkschaft-d1' }
      }));
    });
    await page.locator('.ws-companion-hit:not([hidden])').waitFor();
    await page.locator('.ws-companion-character').dispatchEvent('contextmenu', { bubbles: true, cancelable: true });
    const qmenu = page.locator('.ws-companion-menu:not([hidden])');
    await qmenu.waitFor();
    await qmenu.getByRole('button', { name: /settings|설정/i }).click();
    await settingsDialog.waitFor();
    await sframe.locator('[data-companion-settings-panel]').waitFor();
    assert.equal(await page.locator('dialog.ws-companion-settings').count(), 0, 'quick menu never opens legacy modal');
    await sframe.locator('[data-settings-close]').click();
    await page.locator('#workflow-settings').waitFor({ state: 'hidden' });
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
