const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const { pathToFileURL } = require('node:url');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, captures, mockup, workspace, root, linked, session, projectPath] = process.argv.slice(2);
const viewport = { width: 1440, height: 960 };
const endpoint = (scope, target, worktree, sid) => {
  const q = new URLSearchParams({ scope });
  if (target) q.set('target', target);
  if (worktree) q.set('worktree', worktree);
  if (sid) q.set('session', sid);
  return base + '/settings?' + q;
};
const secrets = /secret-123|example\.com|secret-env|never-return-this-session-key|\/private\/bin/;
const snapshot = '/api/config/snapshot';

(async () => {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport, locale: 'ko-KR', reducedMotion: 'reduce' });
  try {
    const page = await context.newPage();
    const errors = [];
    page.setDefaultTimeout(10000);
    page.on('pageerror', error => errors.push(error.message));
    const frame = () => page.frameLocator('#workflow-settings[open] > iframe');
    const row = key => frame().locator('[data-config-key=' + JSON.stringify(key) + ']');
    const gear = (scope, target) => page.locator('[data-scope-entry=' + scope + ']' + (target ? '[data-settings-target=' + JSON.stringify(target) + ']' : ''));
    const scopeButton = name => frame().locator('[data-settings-precedence] [data-settings-scope=' + name + ']');
    const proposal = name => frame().locator('[data-settings-proposal-action=' + name + ']');
    const headerBadgeMetrics = () => frame().locator('.shd-top').evaluate(top => {
      const pill = top.querySelector('[data-settings-target-display]').getBoundingClientRect();
      const route = top.querySelector('.route-pill').getBoundingClientRect();
      const pillCenter = pill.top + pill.height / 2;
      const routeCenter = route.top + route.height / 2;
      return { pillCenter, routeCenter, delta: Math.abs(pillCenter - routeCenter) };
    });
    const show = async (scope, target, worktree, sid) => {
      const wanted = new URL(endpoint(scope, target, worktree, sid));
      await (await (await page.locator('#workflow-settings > iframe').elementHandle()).contentFrame()).waitForURL(url => url.pathname === '/settings' &&
        (url.searchParams.get('scope') || 'global') === scope &&
        ['target','worktree','session'].every(key => url.searchParams.get(key) === wanted.searchParams.get(key)));
      await row('workflow.lang').waitFor();
    };
    async function setLanguage(value, snapshotURL, waitsForPost = true) {
      const field = row('workflow.lang').locator('input[type=text]');
      await field.fill(value);
      const wait = waitsForPost ? page.waitForResponse(r => r.url().endsWith('/api/config/set') && r.request().method() === 'POST') : null;
      await row('workflow.lang').locator('button[type=submit]').click();
      if (wait) {
        assert.equal((await wait).status(), 200);
        const snapshotData = await (await page.request.get(snapshotURL)).json();
        assert(snapshotData.entries.some(e => e.key === 'workflow.lang' && e.layers.some(l => l.scope === 'global' && l.value === value)));
      }
    }
    async function pickSection(name) {
      await frame().locator('[data-settings-section=' + name + ']').click();
    }
    async function checkPreferences() {
      const shots = process.env.WS_PREF_SLOT_EDITOR_CAPTURES;
      if (shots) await fs.mkdir(shots, {recursive: true});
      const panel = () => frame().locator('.settings-preferences');
      const slot = name => panel().locator('[data-preference-slot=' + JSON.stringify(name) + ']');
      const names = () => panel().locator('[data-preference-slot]').evaluateAll(elements => elements.map(el => el.dataset.preferenceSlot));
      const action = (name, op) => slot(name).locator('[data-preference-action=' + op + ']');
      const add = () => panel().locator('.preferences-add');
      const name = () => add().locator('input');
      const text = () => add().locator('textarea');
      const preview = () => panel().locator('.preferences-preview');
      const showPreferences = async (scope, target, worktree) => {
        await page.goto(endpoint(scope, target, worktree));
        await show(scope, target, worktree);
        await pickSection('prompts');
        await panel().waitFor();
      };
      const capture = async (state, referenceStage) => {
        const reference = shots ? await context.newPage() : null;
        try {
          for (const width of [1200, 760]) {
            const height = width === 760 ? 1800 : 1300;
            await page.setViewportSize({width, height});
            const grid = panel().locator('.preferences-grid');
            assert.equal(await grid.evaluate(el => getComputedStyle(el).gridTemplateColumns.split(' ').length), width === 1200 ? 2 : 1);
            if (!shots) continue;
            // Show the entire scrollable editor in one image while retaining
            // the modal's regular width and responsive breakpoints.
            const modal = page.locator('#workflow-settings[open]');
            await modal.evaluate(el => { el.style.height = 'calc(100% - 32px)'; });
            await frame().locator('.scontent').evaluate(el => { el.scrollTop = 0; });
            assert(await panel().evaluate(el => el.getBoundingClientRect().bottom <=
              el.closest('.scontent').getBoundingClientRect().bottom), 'capture includes the full preference editor');
            await page.mouse.move(1, 1);
            await page.screenshot({path: path.join(shots, 'dashboard-' + state + '-' + width + '.png')});
            await modal.evaluate(el => { el.style.height = ''; });
            await reference.setViewportSize({width, height});
            const mock = path.resolve(__dirname, '../../../../ai-docs/assets/mockups/261123/settings-preference-slots/preference-slots.html');
            await reference.goto(pathToFileURL(mock).href);
            await reference.locator('.stage').nth(referenceStage).screenshot({path: path.join(shots, 'mockup-' + state + '-' + width + '.png')});
          }
        } finally {
          if (reference) await reference.close();
          await page.setViewportSize(viewport);
        }
      };
      await context.addCookies([{name: 'ws_dashboard_theme', value: 'dark', url: base}]);
      await showPreferences('global');
      assert.equal(await frame().locator('.prompt-picker button').first().getAttribute('data-settings-prompt-point'), 'preference.*');
      assert.equal(await row('prompt.UserPreferenceSection').count(), 0);
      assert.deepEqual(await names(), ['default', 'gpt-leash', 'review-style']);
      const legacyText = await slot('default').locator('textarea').inputValue();
      assert.match(legacyText, /^Reply in Korean/);
      assert(await slot('default').locator('textarea').evaluate(el => el.readOnly));
      assert(await add().locator('[data-preference-target=global][aria-pressed=true]').isVisible());
      assert(await add().locator('[data-preference-target=repo]').isDisabled());
      const review = slot('review-style').locator('textarea');
      const existingReview = await review.elementHandle();
      await review.fill('Review findings: lead with the verdict, then numbered issues with file:line. Skip praise and style nits unless asked.');
      assert.match(await preview().textContent(), /Skip praise/);
      assert(await existingReview.evaluate(el => el.isConnected && el === document.activeElement));
      await name().fill('Commit_Style');
      await text().fill('Commit titles in imperative mood, under 72 characters.');
      assert.equal(await name().getAttribute('aria-invalid'), 'true');
      assert(await add().locator('button[type=submit]').isDisabled());
      assert.doesNotMatch(await preview().textContent(), /imperative mood/);
      await capture('a-global', 0);
      await name().fill('gpt-leash');
      assert(await add().locator('button[type=submit]').isDisabled());
      await name().fill('test-policy');
      await text().fill('   ');
      assert(await add().locator('button[type=submit]').isDisabled());
      await text().fill('Temporary global policy');
      let response = page.waitForResponse(r => r.url().endsWith('/api/config/set'));
      await add().locator('button[type=submit]').click();
      assert.equal((await response).status(), 200);
      await slot('test-policy').waitFor();
      assert(await existingReview.evaluate(el => el.isConnected), 'adding a slot preserves the unchanged textarea');
      response = page.waitForResponse(r => r.url().endsWith('/api/config/reset'));
      await action('test-policy', 'reset').click();
      assert.equal((await response).status(), 200);
      await slot('test-policy').waitFor({state: 'detached'});
      response = page.waitForResponse(r => r.url().endsWith('/api/config/set'));
      await action('review-style', 'save').click();
      assert.equal((await response).status(), 200);
      await action('review-style', 'save').waitFor({state: 'hidden'});
      const writes = [];
      const migration = r => {
        if (/\/api\/config\/(set|reset)$/.test(r.url())) writes.push(r.postDataJSON());
      };
      page.on('request', migration);
      await panel().locator('.preferences-legacy button').click();
      await panel().locator('.preferences-legacy').waitFor({state: 'hidden'});
      page.off('request', migration);
      assert.deepEqual(writes.map(({key, value, selectors}) => ({key, value, selectors})), [
        {key: 'preference.default', value: legacyText, selectors: {}},
        {key: 'prompt.UserPreferenceSection', value: undefined, selectors: {harness: '*'}}
      ]);
      const global = await (await page.request.get(base + snapshot)).json();
      assert.equal(global.entries.find(e => e.key === 'preference.default').value, legacyText);
      assert.equal(global.entries.find(e => e.key === 'prompt.UserPreferenceSection' && e.selectors.harness === '*').layers
        .find(layer => layer.scope === 'global').present, false);

      await showPreferences('repo', root, root);
      assert.deepEqual(await names(), ['default', 'repo-terms', 'review-style', 'gpt-leash']);
      assert.equal(await slot('default').locator('textarea').inputValue(), legacyText);
      assert(await slot('default').locator('textarea.is-inherited').isVisible());
      assert(await action('default', 'reset').isDisabled());
      assert.match(await slot('gpt-leash').locator('del').textContent(), /When delegating/);
      assert.equal(await slot('gpt-leash').locator('.layer-chain .is-winner .lscope').textContent(), '저장소');
      assert.equal(await preview().locator('.is-hidden-slot code').textContent(), 'gpt-leash');
      assert(await add().locator('[data-preference-target=repo][aria-pressed=true]').isVisible());
      await name().fill('test-policy');
      await text().fill('Run go test ./... for touched packages before reporting done; never run the full e2e suite locally.');
      assert.equal(await add().locator('button[type=submit]').textContent(), '편집 제안 초안에 추가');
      assert.doesNotMatch(await preview().textContent(), /test-policy/);
      await capture('b-repo', 1);
      await add().locator('button[type=submit]').click();
      await slot('test-policy').waitFor();
      await action('gpt-leash', 'restore').click();
      await slot('gpt-leash').locator('textarea.is-inherited').waitFor();
      await action('default', 'hide').click();
      await slot('default').locator('del').waitFor();
      const pending = await page.evaluate(target => [...window.wsSettingsRepoDrafts.get(target).values()], root);
      assert.deepEqual(pending.map(({key, action, value}) => [key, action, value]), [
        ['preference.test-policy', 'set', 'Run go test ./... for touched packages before reporting done; never run the full e2e suite locally.'],
        ['preference.gpt-leash', 'reset', undefined],
        ['preference.default', 'set', '']
      ]);
      const repo = await (await page.request.get(base + snapshot + '?scope=repo&target=' + root + '&worktree=' + root)).json();
      assert.equal(repo.entries.find(e => e.key === 'preference.gpt-leash').value, '');
      assert.equal(repo.entries.some(e => e.key === 'preference.test-policy'), false);
      await proposal('clear').click();
      await add().locator('[data-preference-target=global]').click();
      await name().fill('shared-policy');
      await text().fill('Global policy added from the repository view');
      response = page.waitForResponse(r => r.url().endsWith('/api/config/set'));
      await add().locator('button[type=submit]').click();
      const crossLayer = await response;
      assert.equal(crossLayer.status(), 200);
      assert.equal(crossLayer.request().postDataJSON().scope, 'global');
      await slot('shared-policy').locator('textarea.is-inherited').waitFor();
      assert(await action('shared-policy', 'reset').isDisabled());
      console.log('Verified preference slots in global and repo scopes, migration, add validation, live preview, and tombstone proposals.');
    }
    async function ensureRailOpen() {
      if (await page.locator('#workflow-settings[open]').count()) {
        await frame().locator('[data-settings-close]').click();
        await page.locator('#workflow-settings').waitFor({state: 'hidden'});
      }
      const btn = page.locator('#railbtn');
      const opened = await page.locator('html').evaluate(el => el.classList.contains('rail-open'));
      if (await btn.isVisible() && !opened) {
        await btn.click();
        await page.locator('html.rail-open').waitFor();
      }
    }
    async function closeRailIfOpen() {
      const opened = await page.locator('html').evaluate(el => el.classList.contains('rail-open'));
      if (opened) {
        await page.keyboard.press('Escape');
        await page.locator('html:not(.rail-open)').waitFor();
      }
    }
    async function capturePairs() {
      const reference = await context.newPage();
      for (const theme of ['dark', 'light']) {
        await page.setViewportSize({ width: 1440, height: 960 });
        await context.addCookies([{name: 'ws_dashboard_theme', value: theme, url: base},
          {name: 'ws_dashboard_lang', value: 'ko', url: base}]);
        await page.goto(base + '/settings');
        await show('global');
        assert.equal(await frame().locator('[data-settings-modes]').count(), 0);
        assert.match(await frame().locator('[data-settings-precedence] .prec-hint').textContent(), /\S/);

        assert.equal(await page.locator('#frames > iframe[src^="/settings"]').count(), 0);
        assert.equal(await frame().locator('.top').isVisible(), false);
        await row('workflow.whiteboard_enabled').locator('[data-settings-bucket=claude]').click();
        assert.equal(await row('workflow.whiteboard_enabled').locator('[data-settings-bucket=claude]').getAttribute('data-bucket-override'), 'other');
        await row('workflow.profile').locator('[data-settings-bucket=claude]').click();
        assert.equal(await row('workflow.profile').locator('[data-settings-bucket=claude]').getAttribute('data-bucket-override'), 'own');
        const profile = row('workflow.profile');
        await profile.locator('[data-settings-option=orchestrate]').click();
        const configured = page.waitForResponse(r => r.url().endsWith('/api/config/set'));
        await profile.locator('button[type=submit]').click();
        assert.equal((await configured).status(), 200);
        await profile.locator('.inline-warn').waitFor();
        assert.doesNotMatch(await profile.locator('.inline-warn').textContent(), /Unverified harness/);

        // Expand workflow.lang full-width horizontal layer strip and verify width/height & zero dead void.
        const langRow = row('workflow.lang');
        await langRow.locator('[data-settings-origin-pill]').click();
        const drawer = langRow.locator('.layer-drawer');
        await drawer.waitFor({ state: 'visible' });
        const langBox = await langRow.boundingBox();
        const drawerBox = await drawer.boundingBox();
        assert(drawerBox && langBox && drawerBox.width >= langBox.width * 0.9, 'expected full-width layer drawer');
        assert(drawerBox.height <= 90, 'expected compact horizontal layer drawer height, got ' + drawerBox.height);
        await page.screenshot({path: path.join(captures, 'mbp-1440-' + theme + '-workflow.png')});

        if (theme === 'dark') {
          await pickSection('agents');
          const mediumEntry = frame().locator('tbody.tier-entry[data-settings-tier=medium]');
          await mediumEntry.locator('[data-settings-origin-pill]').click();
          const tierDrawer = mediumEntry.locator('tr.layer-tr .layer-drawer');
          await tierDrawer.waitFor({ state: 'visible' });
          const tblBox = await frame().locator('.tier-tbl').boundingBox();
          const tdBox = await tierDrawer.boundingBox();
          assert(tdBox && tblBox && tdBox.width >= tblBox.width * 0.9, 'expected full-width tier layer drawer');
          await page.screenshot({path: path.join(captures, 'mbp-1440-dark-tiers.png')});

          await pickSection('compaction');
          await pickSection('adapters');
          await pickSection('profiles');
          const claudeProfHead = await frame().locator('[data-config-key="agent.profile.agent-worker-claude"] .prof-head').evaluate(head => {
            const hRect = head.getBoundingClientRect();
            const pillRect = head.querySelector('.kprov').getBoundingClientRect();
            return { topDelta: Math.abs(pillRect.top - hRect.top), pillRightDelta: Math.abs(hRect.right - pillRect.right) };
          });
          assert(claudeProfHead.topDelta <= 2, 'expected profile origin pill pinned top-right, topDelta=' + claudeProfHead.topDelta);
          assert(claudeProfHead.pillRightDelta <= 2, 'expected profile origin pill flush right, pillRightDelta=' + claudeProfHead.pillRightDelta);
          await page.screenshot({path: path.join(captures, 'mbp-1440-dark-profiles.png')});

          await pickSection('prompts');
          await frame().locator('[data-settings-prompt-point="prompt.TicketHandlerPersistConfirmation"]').click();
          const prompt = row('prompt.TicketHandlerPersistConfirmation');
          await prompt.locator('[data-settings-bucket=claude]').click();
          await prompt.locator('[data-settings-prompt-editor]').fill('## Claude override\nPrefer concise Korean summaries.');
          const promptSet = page.waitForResponse(r => r.url().endsWith('/api/config/set'));
          await prompt.locator('button[type=submit]').click();
          assert.equal((await promptSet).status(), 200);
          assert.equal(await prompt.locator('[data-settings-bucket=claude]').getAttribute('data-bucket-override'), 'own');
          assert.equal(await frame().locator('[data-settings-prompt-point="prompt.TicketHandlerPersistConfirmation"]').getAttribute('data-bucket-override'), 'own');
          await page.screenshot({path: path.join(captures, 'mbp-1440-dark-prompts.png')});
          const promptReset = page.waitForResponse(r => r.url().endsWith('/api/config/reset'));
          await prompt.locator('.reset-btn').click();
          assert.equal((await promptReset).status(), 200);
          await pickSection('workflow');

          // Verify >=1600px docked 340px rail contract (never 250px squished rail).
          await page.setViewportSize({ width: 1680, height: 1050 });
          await page.locator('#railbtn').waitFor({ state: 'hidden' });
          const docked = await page.evaluate(() => {
            const rail = document.getElementById('rail');
            return {
              pos: getComputedStyle(rail).position,
              width: rail.getBoundingClientRect().width,
              overflowX: document.documentElement.scrollWidth > window.innerWidth
            };
          });
          assert.equal(docked.pos, 'relative');
          assert(docked.width >= 335, 'expected >=335px (340px) docked rail on 1680x1050, got ' + docked.width);
          assert.equal(docked.overflowX, false);
          const wideStage = await frame().locator('[data-settings-view]').boundingBox();
          const modalBox = await page.locator('#workflow-settings > iframe').boundingBox();
          assert(wideStage && modalBox && Math.abs(wideStage.width - (modalBox.width - 32)) <= 2, 'stage fills modal iframe');
          await page.screenshot({path: path.join(captures, 'wide-1680-dark-workflow.png')});
          await page.setViewportSize({ width: 1440, height: 960 });
        }
        await reference.goto(pathToFileURL(mockup).href + '?theme=' + theme);
        await reference.locator('#stage .sync-card').waitFor();

        await ensureRailOpen();
        await gear('repo', root).click();
        await show('repo', root, root);
        await row('workflow.whiteboard_enabled').locator('[data-settings-bucket=claude]').click();
        await row('workflow.profile').locator('[data-settings-bucket=claude]').click();
        await profile.locator('[data-settings-option=direct]').click();
        await profile.locator('button[type=submit]').click();
        await setLanguage('English', null, false);
        await proposal('toggle').click();
        await frame().locator('.proposal-patch').waitFor();
        const repoAlign = await frame().locator('body').evaluate(() => {
          const langRow = document.querySelector('[data-config-key="workflow.lang"]');
          const profRow = document.querySelector('[data-config-key="workflow.profile"]');
          const langCtl = langRow.querySelector('.kctl-wrap').getBoundingClientRect();
          const profCtl = profRow.querySelector('.kctl-wrap').getBoundingClientRect();
          const langResets = Array.from(langRow.querySelectorAll('.reset-btn')).filter(el => el.getBoundingClientRect().width > 0);
          const langLbl = langRow.querySelector('.origin-pill > .lbl');
          const profLbl = profRow.querySelector('.origin-pill > .lbl');
          return {
            ctlLeftDelta: Math.abs(langCtl.left - profCtl.left),
            visibleResetCount: langResets.length,
            langClipped: langLbl.scrollWidth > langLbl.clientWidth + 1,
            profClipped: profLbl.scrollWidth > profLbl.clientWidth + 1
          };
        });
        assert(repoAlign.ctlLeftDelta <= 1, 'expected aligned .kctl-wrap on repo draft rows, delta=' + repoAlign.ctlLeftDelta);
        assert.equal(repoAlign.visibleResetCount, 1, 'expected single visible discard reset button on repo draft row');
        assert.equal(repoAlign.langClipped, false, 'expected workflow.lang origin-pill text not clipped');
        assert.equal(repoAlign.profClipped, false, 'expected workflow.profile origin-pill text not clipped');
        await page.screenshot({path: path.join(captures, 'implementation-repo-' + theme + '.png')});
        await proposal('clear').click();
      }
      await reference.close();
    }
    // Both UI entry points preserve the session, URL, and focus.
    for (const width of [1200, 760]) {
      await page.setViewportSize({width, height: 960});
      for (const entry of ['global', 'rail']) {
        await page.goto(base + '/s/' + session);
        await page.frameLocator('iframe.sframe.on').locator('#grid').waitFor();
        const sessionFrame = page.frames().find(f => new URL(f.url()).pathname === '/s/' + session);
        await sessionFrame.evaluate(() => {
          window.settingsVisibilityMessages = [];
          addEventListener('message', e => {
            if (e.data && e.data.wsShell === 'shown') settingsVisibilityMessages.push(e.data.shown);
          });
        });
        const before = page.url();
        await ensureRailOpen();
        const opener = entry === 'global' ? page.locator('#gmenu a[href="/settings"]') : gear('repo', root);
        if (entry === 'global') await page.locator('#gear').click();
        await opener.click();
        await show(entry === 'global' ? 'global' : 'repo', entry === 'global' ? null : root, entry === 'global' ? null : root);
        assert.equal(page.url(), before);
        assert.equal(await sessionFrame.evaluate(() => settingsVisibilityMessages.includes(false)), false);
        assert.equal(await page.locator('#frames iframe.sframe.on').count(), 1);

        if (process.env.WS_CONFIG_SETTINGS_CAPTURES) {
          await page.screenshot({path: path.join(captures, entry + '-' + width + '.png')});
        }
        if (width === 760) {
          await pickSection('prompts');
          await frame().locator('.settings-preferences').waitFor();
          await pickSection('workflow');
        }
        const search = frame().locator('[data-settings-search]');
        await search.fill('workflow');
        await search.press('Escape');
        assert.equal(await search.inputValue(), '');
        assert.equal(await page.locator('#workflow-settings[open]').count(), 1);
        await search.press('Escape');
        await page.locator('#workflow-settings').waitFor({state: 'hidden'});
        assert.equal(page.url(), before);
        if (entry === 'global') await page.locator('#gear').click();
        await opener.click();
        await show(entry === 'global' ? 'global' : 'repo', entry === 'global' ? null : root, entry === 'global' ? null : root);
        await page.mouse.click(2, 2);
        await page.locator('#workflow-settings').waitFor({state: 'hidden'});
      }
    }
    await capturePairs();
    const direct = await page.goto(base + '/settings');
    assert.equal(direct.status(), 200);
    await show('global');
    assert.match(await scopeButton('session').textContent(), /미평가/);
    assert(await scopeButton('synced').isDisabled());
    assert.equal(await page.evaluate(() => window.wsNativeCloseReady()), true, 'clean Settings frame is ready for native close');

    const auth = await page.request.post(base + '/api/config/set', { headers: { Origin: 'http://elsewhere.test' },
      data: { scope:'global', key:'workflow.lang', value:'blocked' } });
    assert.equal(auth.status(), 403);
    const badContext = await page.request.get(base + snapshot + '?scope=builtin&worktree=stale');
    assert.equal(badContext.status(), 400);
    assert.equal((await badContext.json()).error.code, 'invalid_context');

    await setLanguage('English', base + snapshot);
    const reset = page.waitForResponse(r => r.url().endsWith('/api/config/reset') && r.request().method() === 'POST');
    await row('workflow.lang').locator('.reset-btn').click();
    assert.equal((await reset).status(), 200);
    await setLanguage('Korean', base + snapshot);

    const localSnapshot = await (await page.request.get(base + snapshot)).json();
    const namespaces = localSnapshot.entries.map(e => e.key.split('.')[0]);
    for (const adapter of ['claude', 'pi', 'agy', 'dashboard']) assert(namespaces.includes(adapter));
    await ensureRailOpen();
    await page.locator('#gear').click();
    for (const selector of ['[data-layout=split]', '[data-input=mod]', '[data-theme-set=dark]', '#glang', '#login-copy', 'a[href="/settings"]']) {
      assert(await page.locator('#gmenu ' + selector).isVisible(), selector);
    }
    await page.locator('#settings-close').click();

    await ensureRailOpen();
    await gear('project', workspace).focus();
    await page.keyboard.press('Tab');
    await page.keyboard.press('Shift+Tab');
    assert(await gear('project', workspace).evaluate(el => el.matches(':focus-visible')));
    await gear('project', workspace).press('Enter');
    await show('project', workspace);
    assert.equal(await frame().locator('[data-settings-target-display] code').textContent(), projectPath);
    await scopeButton('builtin').click();
    await show('builtin');
    assert.equal(new URLSearchParams(new URL(page.frames().find(f => f.url().includes('modal=1')).url()).hash.slice(1)).get('project'), workspace);
    await page.goto(endpoint('project', workspace));
    await show('project', workspace);
    await scopeButton('builtin').click();
    await show('builtin');
    await page.reload();
    await show('builtin');
    const badgeCenters = {};
    const recordBadgeCenter = async name => {
      const m = await headerBadgeMetrics();
      assert(m.delta <= 1, `expected header badge aligned with /settings on ${name}, delta=${m.delta}`);
      badgeCenters[name] = m.pillCenter;
    };
    await scopeButton('global').click();
    await show('global');
    await recordBadgeCenter('global');
    await scopeButton('project').click();
    await show('project', workspace);
    await recordBadgeCenter('project');
    assert(await row('workflow.prefer_subagent').locator('button.tg').isDisabled());
    await ensureRailOpen();
    await gear('session', session).click();
    await show('session', session, root, session);
    await recordBadgeCenter('session');
    await scopeButton('builtin').click();
    await show('builtin', null, root, session);
    assert(await row('workflow.lang').locator('input').isDisabled());
    await scopeButton('session').click();
    await show('session', session, root, session);

    await ensureRailOpen();
    await gear('repo', root).click();
    await show('repo', root, root);
    await recordBadgeCenter('repo');
    const centerVals = Object.values(badgeCenters);
    assert(Math.max(...centerVals) - Math.min(...centerVals) <= 1,
      'expected header badge vertical center to match across scopes: ' + JSON.stringify(badgeCenters));
    await scopeButton('builtin').click();
    await show('builtin', null, root);
    assert.match(await scopeButton('session').textContent(), /미평가/);
    await scopeButton('repo').click();
    await show('repo', root, root);

    await pickSection('profiles');
    const profile = row('agent.profile.reviewer-readonly');
    await profile.locator('[data-settings-profile-json] summary').click();
    const raw = await profile.locator('.prof-json').textContent();
    assert.match(raw, /\[redacted\]/);
    assert.match(raw, /"command": "node"/);
    assert.doesNotMatch(raw, secrets);
    const scoped = await page.request.get(base + snapshot + '?scope=repo&target=' + root + '&worktree=' + root);
    assert.doesNotMatch(await scoped.text(), secrets);
    const malformed = await page.request.post(base + '/api/config/set', {
      headers: { Origin: base, 'Content-Type':'application/json' },
      data: '{"scope":"global","key":"agent.profile.test","value": {"mcp_servers":[ "secret-123","https://example.com" ]'
    });
    assert.equal(malformed.status(), 400);
    assert.doesNotMatch(await malformed.text(), secrets);
    await pickSection('prompts');
    await frame().locator('[data-settings-prompt-point="prompt.TicketHandlerPersistConfirmation"]').click();
    const editor = row('prompt.TicketHandlerPersistConfirmation').locator('[data-settings-prompt-editor]');
    await editor.fill('## Standing preferences\n<reviewed>');
    assert(await editor.evaluate(el => el === document.activeElement), 'line diff preserves focus');
    const diff = row('prompt.TicketHandlerPersistConfirmation').locator('[data-settings-prompt-diff]');
    assert.match(await diff.textContent(), /[+] <reviewed>/);
    assert.equal(await diff.locator('reviewed').count(), 0);

    await pickSection('workflow');
    await setLanguage('First draft', null, false);
    await setLanguage('Second draft', null, false);
    assert.equal(await page.evaluate(() => window.wsNativeCloseReady()), false, 'pending repo proposal holds native close');
    await proposal('toggle').click();
    assert.match(await frame().locator('.proposal-patch').textContent(), /Second draft/);
    assert.doesNotMatch(await frame().locator('.proposal-patch').textContent(), /First draft/);
    await scopeButton('builtin').click();
    await show('builtin', null, root);
    assert.equal(await page.locator('.ws-confirm').count(), 0);
    await scopeButton('repo').click();
    await show('repo', root, root);

    const sent = page.waitForResponse(r => r.url().endsWith('/api/config/proposal'));
    await proposal('send').click();
    assert.equal((await sent).status(), 200);
    await frame().locator('[data-settings-proposal-status=delivered]').waitFor();

    await ensureRailOpen();
    await gear('repo', linked).click();
    await show('repo', linked, linked);
    await setLanguage('Manual draft', null, false);
    const noLead = page.waitForResponse(r => r.url().endsWith('/api/config/proposal'));
    await proposal('send').click();
    assert.equal((await noLead).status(), 409);
    await frame().locator('[data-settings-proposal-status=no_active_lead]').waitFor();
    const current = await (await page.locator('#workflow-settings[open] > iframe').elementHandle()).contentFrame();
    await current.evaluate(() => Object.defineProperty(navigator, 'clipboard', {
      configurable: true, value: {writeText: () => Promise.reject(Error('denied for test'))}
    }));
    await proposal('copy').click();
    const manual = frame().locator('[data-settings-proposal-manual]');
    assert.match(await manual.inputValue(), /Manual draft/);
    assert.doesNotMatch(await manual.inputValue(), secrets);
    await proposal('clear').click();
    await proposal('copy').waitFor({ state:'hidden' });
    assert.equal(await page.evaluate(() => window.wsNativeCloseReady()), true, 'cleared repo proposal releases native close');

    // Closing the modal silently retains staged proposals in the shell.
    await setLanguage('Staged before leaving', null, false);
    await frame().locator('[data-settings-close]').click();
    await page.locator('#workflow-settings').waitFor({state: 'hidden'});
    assert.equal(await page.locator('.ws-confirm').count(), 0);
    assert.equal(await page.evaluate(() => [...window.wsSettingsRepoDrafts.values()].some(d => d.size > 0)), true);
    await ensureRailOpen();
    await gear('repo', linked).click();
    await show('repo', linked, linked);
    await proposal('toggle').click();
    assert.match(await frame().locator('.proposal-patch').textContent(), /Staged before leaving/);
    await frame().locator('[data-settings-close]').click();
    await page.evaluate(() => window.wsSettingsRepoDrafts.clear());

    await checkPreferences();
    assert.deepEqual(errors, []);
    console.log('Verified settings scopes, navigation, authorization, mutations, profiles, prompt diff, proposals and 8 visual captures.');
  } finally {
    await browser.close();
  }
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
