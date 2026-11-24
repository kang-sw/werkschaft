// Run by TestUserCommandBrowser against a live daemon whose launch service is a
// fake: the v2 user commands trigger, popover, 3-line inline cards, and 1.5s hold UX in ko and en.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, signals, shots] = process.argv.slice(2);

const TEXT = {
  ko: { count: '2개', stale: '실행 전에 이 명령이 바뀌어 아무것도 실행되지 않았습니다', earlyRun: '1.5초간 꾹 누르면 실행됩니다' },
  en: { count: '2', stale: 'This command changed before it ran, so nothing was run', earlyRun: 'Hold ▶ for 1.5s to run' },
};

async function sessionFrame(page) {
  for (let i = 0; i < 100; i++) {
    const f = page.frames().find(x => x !== page.mainFrame() && x.url().includes('/s/livesession'));
    if (f) { await f.waitForSelector('#composer', { state: 'attached', timeout: 30000 }); return f; }
    await page.waitForTimeout(200);
  }
  throw new Error('no session frame');
}

async function agent(step, tag) {
  const name = step + '.' + tag;
  fs.writeFileSync(path.join(signals, 'do-' + name), '');
  for (let i = 0; i < 200; i++) {
    if (fs.existsSync(path.join(signals, 'ack-' + name))) return;
    await new Promise(r => setTimeout(r, 25));
  }
  throw new Error('the agent step ' + step + ' was not acknowledged');
}

async function openPop(view) {
  const open = await view.locator('.ucmd-pop').isVisible();
  if (!open) {
    await view.locator('#ucmdbadge').click();
    await view.waitForSelector('.ucmd-pop:not([hidden])', { timeout: 5000 });
  }
}

async function holdButton(page, locator, ms = 1600) {
  const box = await locator.boundingBox();
  assert.ok(box, 'hold target must have a bounding box');
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.waitForTimeout(ms);
  await page.mouse.up();
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const errors = [];
  try {
    if (shots) fs.mkdirSync(shots, { recursive: true });
    for (const lang of ['ko', 'en']) {
      const t = TEXT[lang];
      await agent('reset', lang);
      const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: lang === 'ko' ? 'ko-KR' : 'en-US', reducedMotion: 'reduce' });
      await ctx.addCookies([{ name: 'ws_dashboard_lang', value: lang, url: base }]);
      const page = await ctx.newPage();
      page.on('pageerror', e => { errors.push(lang + ': ' + e.message); console.error('PAGE ERROR:', e.message); });
      const posts = [], gets = [];
      page.on('request', r => {
        const p = new URL(r.url()).pathname;
        if (r.method() === 'POST') posts.push({ path: p, body: r.postData() || '' });
        else if (p.endsWith('/user-commands')) gets.push(p);
      });
      const shot = async name => { if (shots) await page.screenshot({ path: path.join(shots, 'ucmd-v2-' + lang + '-' + name + '.png') }); };
      await page.goto(base + '/s/livesession');
      let view = await sessionFrame(page);
      await view.waitForSelector('#composer[data-cexec="1"]', { state: 'attached' });
      await view.evaluate(() => {
        window.__admitted = window.__admitted || [];
        if (!window.__admittedWired) {
          window.__admittedWired = true;
          window.addEventListener('ws:cexec-admitted', e => window.__admitted.push(e.detail));
        }
      });
      await page.waitForTimeout(400);
      await view.waitForSelector('#ucmdbadge:not([hidden])', { timeout: 5000 });
      assert.equal(await view.locator('#ucmdbadge.is-empty').count(), 1, 'trigger is always visible even at 0 commands');
      await openPop(view);
      assert.equal(await view.locator('.ucmd-empty:not([hidden])').count(), 1, 'empty state shown at 0 commands');
      await shot('empty');

      // Agent registers two commands: open page updates via SSE with no reload or polling.
      const getsBefore = gets.length;
      await agent('register', lang);
      await view.waitForSelector('.ucmd-card[data-ucmd="test"]', { timeout: 10000 });
      assert.ok(gets.length - getsBefore <= 2, 'refetched once per change, not by polling: ' + (gets.length - getsBefore));
      assert.equal(await view.locator('.ucmd-card').count(), 2);
      assert.equal(await view.locator('.ucmd-count').textContent(), t.count);
      const build = view.locator('.ucmd-card[data-ucmd="build"]');
      assert.equal(await build.locator('.ucmd-title').textContent(), 'Build');
      assert.equal(await build.locator('.ucmd-inline-input.is-cmd').inputValue(), 'make all');
      assert.equal(await view.locator('.ucmd-card[data-ucmd="test"] .ucmd-inline-input.is-cmd').inputValue(), 'make test\n./run --verbose');
      await shot('list');

      // Quick click does NOT run and shows reminder toast.
      await build.locator('.ucmd-ibtn-run').click();
      await page.waitForTimeout(200);
      assert.equal(posts.filter(p => p.path.endsWith('/user-commands/build/run')).length, 0, 'quick click never runs');
      assert.ok(await view.locator('.ws-toast, [role=status], #toasts').first().innerText().then(s => s.includes(t.earlyRun), () => false), 'early-release hint toast shown');

      // 1.5s hold runs the stored line and emits headless admission with 0px composer height growth.
      const hBefore = await view.evaluate(() => document.getElementById('composer').getBoundingClientRect().height);
      await holdButton(page, build.locator('.ucmd-ibtn-run'));
      await view.waitForFunction(() => (window.__admitted || []).some(e => e.command === 'make all'));
      const buildAdmit = await view.evaluate(() => window.__admitted.find(e => e.command === 'make all'));
      assert.equal(buildAdmit.origin, 'ucmd');
      assert.ok(buildAdmit.exec_id);
      assert.equal(await view.locator('.d-cexec').count(), 0);
      assert.equal(await view.evaluate(() => document.getElementById('composer').getBoundingClientRect().height), hBefore);
      const runs = posts.filter(p => p.path.endsWith('/user-commands/build/run'));
      assert.equal(runs.length, 1);
      const body = JSON.parse(runs[0].body);
      assert.deepEqual(Object.keys(body).sort(), ['retry', 'revision', 'source', 'submission_id', 'token'], 'unmodified hold names scope+id+revision, never text');
      assert.ok(posts.every(p => !p.path.endsWith('/message') && !p.path.endsWith('/composer-exec')));

      // Stale command replaced behind the page's back: hold runs nothing, warns, refreshes card.
      await openPop(view);
      const admittedBeforeStale = await view.evaluate(() => window.__admitted.length);
      await page.route('**/user-commands', route => route.request().method() === 'GET' ? route.abort() : route.continue());
      await agent('replace-build', lang);
      await page.waitForTimeout(400);
      assert.equal(await build.locator('.ucmd-inline-input.is-cmd').inputValue(), 'make all');
      await holdButton(page, build.locator('.ucmd-ibtn-run'));
      await view.waitForFunction(() => {
        const el = document.querySelector('.ucmd-card[data-ucmd="build"] .ucmd-inline-input.is-cmd');
        return el && el.value === 'make clean all';
      }, null, { timeout: 10000 });
      assert.ok(await view.locator('.ws-toast, [role=status], #toasts').first().innerText().then(s => s.includes(t.stale), () => false), 'stale rejection shown');
      assert.equal(await view.evaluate(() => window.__admitted.length), admittedBeforeStale);
      await page.unroute('**/user-commands');

      if (lang === 'en') {
        await agent('hold-slot', 'presses');
        const entry = view.locator('.ucmd-card[data-ucmd="hold"]');
        const line = entry.locator('.is-cmd');
        await entry.waitFor();
        for (const [action, step, key] of [
          ['run', 'hold-run', null], ['ask', 'hold-ask', 'Space'], ['del', 'hold-delete', 'Enter'],
        ]) {
          const button = entry.locator('.ucmd-ibtn-' + action);
          const before = posts.length;
          if (key) {
            await button.focus();
            await page.keyboard.down(key);
          } else {
            await button.hover();
            await page.mouse.down();
          }
          await button.and(view.locator('[data-hold-state="ready"]')).waitFor();
          await agent(step, 'presses');
          await view.waitForFunction(expected => document.querySelector('.ucmd-card[data-ucmd="hold"] .is-cmd').value === expected, 'echo changed ' + step.split('-')[1]);
          assert.equal(await button.getAttribute('data-hold-state'), null, action + ' hold canceled on refresh');
          if (key) await page.keyboard.up(key);
          else await page.mouse.up();
          assert.equal(posts.length, before, action + ' did not submit a refreshed command');
        }

        await agent('hold-slot', 'multiline');
        const prompt = entry.locator('.is-prompt.ucmd-inline-input');
        const original = '  Keep the first line.\r\nAnd the second.  ';
        await view.waitForFunction(() => document.querySelector('.ucmd-card[data-ucmd="hold"] .is-prompt.ucmd-inline-input').value.includes('Keep the first line.'));
        assert.equal(await prompt.inputValue(), original.replace('\r\n', '\n'), 'prompt editor displays both lines');
        assert.equal(await entry.evaluate(e => e.classList.contains('is-dirty')), false, 'textarea normalization stays clean');
        await prompt.fill('Draft line one\nDraft line two');
        await entry.locator('.ucmd-inline-input.is-prompt + .ucmd-rev-btn').click();
        assert.equal(await prompt.inputValue(), original.replace('\r\n', '\n'), 'revert restores the multiline prompt');

        let admitSave;
        const saveArrived = new Promise(resolve => { admitSave = resolve; });
        let releaseSave;
        const saveBlocked = new Promise(resolve => { releaseSave = resolve; });
        const savePath = '**/user-commands/hold';
        await page.route(savePath, async route => {
          admitSave(route.request().postDataJSON());
          await saveBlocked;
          await route.continue();
        });
        await line.fill('echo edited');
        await entry.locator('.ucmd-ibtn-save').click();
        const patch = await saveArrived;
        try {
          assert.equal(patch.line, 'echo edited');
          assert.equal(Object.hasOwn(patch, 'prompt'), false, 'patch omits the untouched prompt');
          assert.equal(await prompt.evaluate(e => e.readOnly), true, 'prompt is read-only during Save');
          assert.equal(await line.evaluate(e => e.readOnly), true, 'command line is read-only during Save');
          await line.focus();
          await page.keyboard.press('End');
          await page.keyboard.type(' cannot edit');
          assert.equal(await line.inputValue(), 'echo edited');
        } finally {
          releaseSave();
        }
        await entry.locator('.ucmd-ibtn-save:enabled').waitFor();
        await page.unroute(savePath);
        const saved = await (await page.request.get(base + '/s/livesession/user-commands')).json();
        assert.equal(saved.commands.find(c => c.id === 'hold').prompt, original, 'unrelated Save leaves the stored prompt byte-for-byte intact');

        let asked;
        await page.route('**/s/livesession/message', route => {
          asked = route.request().postDataJSON().text;
          return route.fulfill({ status: 200, body: '{}' });
        });
        await holdButton(page, entry.locator('.ucmd-ibtn-ask'));
        await view.waitForSelector('.ucmd-pop', { state: 'hidden' });
        assert.ok(asked.endsWith('\n' + original), 'Ask sends the exact stored prompt');
        await page.unroute('**/s/livesession/message');
        await openPop(view);
        await holdButton(page, entry.locator('.ucmd-ibtn-del'));
        await entry.waitFor({ state: 'detached' });
      }

      // Agent unregisters "test" -> card disappears.
      await agent('unregister-test', lang);
      await view.waitForSelector('.ucmd-card[data-ucmd="test"]', { state: 'detached', timeout: 10000 });

      // Hold-to-delete removes "build"; popover shows empty state.
      await holdButton(page, view.locator('.ucmd-card[data-ucmd="build"] .ucmd-ibtn-del'));
      await view.waitForSelector('.ucmd-empty:not([hidden])', { timeout: 10000 });
      assert.ok(posts.some(p => p.path.endsWith('/user-commands/build/remove')));

      // Command named "constructor" is runnable (null-prototype busy table).
      await agent('register-extra', lang);
      await view.waitForSelector('.ucmd-card[data-ucmd="constructor"]', { timeout: 10000 });
      const ctor = view.locator('.ucmd-card[data-ucmd="constructor"] .ucmd-ibtn-run');
      assert.equal(await ctor.isDisabled(), false);
      await holdButton(page, ctor);
      await view.waitForFunction(() => (window.__admitted || []).some(e => e.command === 'echo ctor'), null, { timeout: 10000 });
      assert.equal(posts.filter(p => p.path.endsWith('/user-commands/constructor/run')).length, 1);

      // Reopen during the post-ack guard and retry before the admission poll.
      await openPop(view);
      const dbl = view.locator('.ucmd-card[data-ucmd="dbl"] .ucmd-ibtn-run');
      const runPath = '**/user-commands/dbl/run';
      let releaseRun;
      const runResponse = new Promise(resolve => { releaseRun = resolve; });
      await page.route(runPath, async route => {
        await runResponse;
        await route.continue();
      });
      await holdButton(page, dbl);
      await view.waitForFunction(() => JSON.parse(sessionStorage.getItem('ws.ucmd.attempt.livesession') || '{}')['agent:dbl']);
      releaseRun();
      const guard = await view.waitForFunction(() => {
        if (JSON.parse(sessionStorage.getItem('ws.ucmd.attempt.livesession') || '{}')['agent:dbl']) return false;
        document.getElementById('ucmdbadge').click();
        const button = document.querySelector('.ucmd-card[data-ucmd="dbl"] .ucmd-ibtn-run');
        const disabled = button.disabled;
        button.click();
        return { disabled };
      });
      assert.equal((await guard.jsonValue()).disabled, true, 'the acknowledged run still guards the slot');
      await view.waitForFunction(() => (window.__admitted || []).some(e => e.command === 'echo dbl'));
      await view.waitForFunction(() => !document.querySelector('.ucmd-card[data-ucmd="dbl"] .ucmd-ibtn-run').disabled);
      assert.equal(posts.filter(p => p.path.endsWith('/user-commands/dbl/run')).length, 1, 'the retry did not submit a second run');
      await page.unroute(runPath);

      // Out-of-order removal answer does not clobber a newer SSE list.
      await openPop(view);
      let held = null;
      await page.route('**/user-commands/dbl/remove', async route => { held = { route, response: await route.fetch() }; });
      await holdButton(page, view.locator('.ucmd-card[data-ucmd="dbl"] .ucmd-ibtn-del'));
      for (let i = 0; i < 100 && !held; i++) await page.waitForTimeout(25);
      assert.ok(held, 'removal reached daemon');
      await agent('register-late', lang);
      await view.waitForSelector('.ucmd-card[data-ucmd="late"]', { timeout: 10000 });
      await held.route.fulfill({ response: held.response });
      await page.waitForTimeout(600);
      assert.equal(await view.locator('.ucmd-card[data-ucmd="late"]').count(), 1);
      await page.unroute('**/user-commands/dbl/remove');

      // Refused GET hides trigger and clears list; a held write answer does not bring it back.
      let heldDenied = null;
      await page.route('**/user-commands', route => route.request().method() === 'GET' ? route.fulfill({ status: 404, body: 'unknown session' }) : route.continue());
      await page.route('**/user-commands/late/remove', async route => { heldDenied = { route, response: await route.fetch() }; });
      await holdButton(page, view.locator('.ucmd-card[data-ucmd="late"] .ucmd-ibtn-del'));
      for (let i = 0; i < 100 && !heldDenied; i++) await page.waitForTimeout(25);
      assert.ok(heldDenied, 'denied-phase removal reached daemon');
      await view.waitForSelector('#ucmdbadge', { state: 'hidden', timeout: 10000 });
      await heldDenied.route.fulfill({ response: heldDenied.response });
      await page.waitForTimeout(400);
      assert.equal(await view.locator('#ucmdbadge:not([hidden])').count(), 0, 'write answer did not bring back denied trigger');
      await page.unroute('**/user-commands');
      await page.unroute('**/user-commands/late/remove');

      // Never in shared transcript.
      const shared = await (await page.request.get(base + '/s/livesession?frame=1', { headers: { 'Sec-Fetch-Dest': 'iframe' } })).text();
      assert.ok(!shared.includes('make clean all') && !shared.includes('compile everything'), 'shared page holds no command');
      await ctx.close();
    }
    assert.deepEqual(errors, []);
    console.log('user command v2 browser acceptance passed');
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exit(1); });
