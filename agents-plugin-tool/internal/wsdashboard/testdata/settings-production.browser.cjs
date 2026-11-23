const assert = require('node:assert/strict');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [login, selectedSession, siblingSession] = process.argv.slice(2);

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 960 }, locale: 'en-US' });
    page.setDefaultTimeout(8000);
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    const frame = page.frameLocator('#workflow-settings[open] > iframe');
    const row = key => frame.locator('[data-config-key=' + JSON.stringify(key) + ']');
    await page.goto(login);
    await page.waitForURL(url => url.pathname === '/settings' && !url.searchParams.has('login'));
    const origin = new URL(page.url()).origin;
    const api = origin + '/api/config/';
    const get = async (scope, context) => {
      const params = new URLSearchParams({scope, ...context});
      const response = await page.request.get(api + 'snapshot?' + params);
      assert.equal(response.status(), 200);
      return (await response.json()).entries;
    };
    const global = await get('global');
    for (const [adapter, count] of Object.entries({claude:13, agy:6, pi:0, dashboard:21})) {
      assert.equal(global.filter(entry => entry.key.startsWith(adapter + '.')).length, count);
    }
    assert.equal(global.find(e => e.key === 'dashboard.port').type, 'integer');
    const invalid = await page.request.post(api + 'set', {headers:{Origin:origin},
      data:{scope:'global', key:'dashboard.port', value:9000000}});
    assert.equal(invalid.status(), 400);

    const profile = row('workflow.profile');
    await profile.locator('[data-settings-option=orchestrate]').click();
    let mutation = page.waitForResponse(r => r.url() === api + 'set');
    await profile.locator('button[type=submit]').click();
    assert.equal((await mutation).status(), 200);
    await profile.locator('.inline-warn').waitFor();
    await profile.locator('[data-settings-bucket=codex]').click();
    assert.equal(await profile.locator('[data-settings-option=orchestrate]').getAttribute('aria-pressed'), 'true');
    await profile.locator('[data-settings-bucket="*"]').click();
    mutation = page.waitForResponse(r => r.url() === api + 'reset');
    await profile.locator('.reset-btn').click();
    assert.equal((await mutation).status(), 200);
    await profile.locator('[data-settings-bucket=codex]').click();
    assert.equal(await profile.locator('[data-settings-option=direct]').getAttribute('aria-pressed'), 'true');

    await frame.locator('[data-settings-close]').click();
    const gear = id => page.locator('[data-scope-entry=session][data-settings-target=' + JSON.stringify(id) + ']');
    if (await page.locator('#railbtn').isVisible() && !await page.locator('html').evaluate(el => el.classList.contains('rail-open'))) {
      await page.locator('#railbtn').click();
    }
    await gear(selectedSession).click();
    await (await (await page.locator('#workflow-settings > iframe').elementHandle()).contentFrame()).waitForURL(url => url.searchParams.get('target') === selectedSession);
    const worktree = new URL(page.frames().find(f => f.url().includes('modal=1')).url()).searchParams.get('worktree');
    assert(worktree);
    const contextFor = sid => ({worktree, target:sid, session:sid});
    const language = row('workflow.lang');
    const input = language.locator('input[type=text]');
    await input.fill('Portuguese');
    mutation = page.waitForResponse(r => r.url() === api + 'set');
    await language.locator('button[type=submit]').click();
    assert.equal((await mutation).status(), 200);
    let entries = await get('session', contextFor(selectedSession));
    assert.equal(entries.find(e => e.key === 'workflow.lang').value, 'Portuguese');
    entries = await get('session', contextFor(siblingSession));
    assert.equal(entries.find(e => e.key === 'workflow.lang').value, 'English');
    mutation = page.waitForResponse(r => r.url() === api + 'reset');
    await language.locator('.reset-btn').click();
    assert.equal((await mutation).status(), 200);
    await input.waitFor();
    assert.equal(await input.inputValue(), 'English');

    await frame.locator('[data-settings-section=prompts]').click();
    await frame.locator('[data-settings-prompt-point="prompt.TicketHandlerPersistConfirmation"]').click();
    const prompt = row('prompt.TicketHandlerPersistConfirmation');
    assert.match(await prompt.locator('.seed-pre').textContent(), /the confirmation record/);
    const editor = prompt.locator('[data-settings-prompt-editor]');
    await editor.fill('Use <literal> terminology.');
    assert.match(await prompt.locator('[data-settings-prompt-diff]').textContent(), /\+ Use <literal> terminology\./);
    mutation = page.waitForResponse(r => r.url() === api + 'set');
    await prompt.locator('button[type=submit]').click();
    assert.equal((await mutation).status(), 200);
    const savedPrompt = (await get('session', contextFor(selectedSession))).find(e =>
      e.key === 'prompt.TicketHandlerPersistConfirmation' && e.selectors.harness === '*');
    assert.equal(savedPrompt.value, 'Use <literal> terminology.');
    await page.goto(origin + '/settings');
    await frame.locator('[data-settings-section=prompts]').click();
    const legacy = frame.locator('.preferences-legacy');
    const defaultSlot = frame.locator('[data-preference-slot=default] textarea');
    assert.equal(await defaultSlot.inputValue(), 'A preference saved before harness buckets.');
    mutation = page.waitForResponse(r => r.url() === api + 'reset');
    await legacy.locator('button').click();
    assert.equal((await mutation).status(), 200);
    await legacy.waitFor({state: 'hidden'});
    const migrated = await get('global');
    assert.equal(migrated.find(e => e.key === 'preference.default').value, 'A preference saved before harness buckets.');
    assert.equal(migrated.find(e => e.key === 'prompt.UserPreferenceSection' && e.selectors.harness === '*').layers
      .find(layer => layer.scope === 'global').present, false);
    assert.deepEqual(errors, []);
    console.log('Production manifests, bucket refresh, live session isolation and packaged prompt seeds verified.');
  } finally {
    await browser.close();
  }
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
