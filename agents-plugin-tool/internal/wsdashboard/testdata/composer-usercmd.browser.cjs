// Run by TestUserCommandBrowser against a live daemon whose launch service is a
// fake: the user commands' strip and a slot-run card in ko and en.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, signals, shots] = process.argv.slice(2);

const TEXT = {
  ko: { title: '명령 (2)', stale: '실행 전에 이 명령이 바뀌어 아무것도 실행되지 않았습니다', exit0: '종료 코드 0' },
  en: { title: 'Commands (2)', stale: 'This command changed before it ran, so nothing was run', exit0: 'exit 0' },
};

async function sessionFrame(page) {
  for (let i = 0; i < 100; i++) {
    const f = page.frames().find(x => x !== page.mainFrame() && x.url().includes('/s/livesession'));
    if (f) { await f.waitForSelector('#composer', { state: 'attached', timeout: 30000 }); return f; }
    await page.waitForTimeout(200);
  }
  throw new Error('no session frame');
}
// The Go side changes the registry as the agent would: a signal file per step.
async function agent(step, tag) {
  const name = step + '.' + tag;
  fs.writeFileSync(path.join(signals, 'do-' + name), '');
  for (let i = 0; i < 200; i++) {
    if (fs.existsSync(path.join(signals, 'ack-' + name))) return;
    await new Promise(r => setTimeout(r, 25));
  }
  throw new Error('the agent step ' + step + ' was not acknowledged');
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
      const shot = async name => { if (shots) await view.locator('#composer').screenshot({ path: path.join(shots, 'slots-' + lang + '-' + name + '.png') }); };
      await page.goto(base + '/s/livesession');
      let view = await sessionFrame(page);
      await view.waitForSelector('#composer[data-cexec="1"]', { state: 'attached' });
      await page.waitForTimeout(500);
      assert.equal(await view.locator('.d-ucmd:not([hidden]) .ucmd').count(), 0, 'no strip without commands');

      // the agent registers two commands: an open page shows them with no reload
      // and no polling (the refetch rides the stream's revision event)
      const getsBefore = gets.length;
      await agent('register', lang);
      await view.waitForSelector('.d-ucmd .ucmd[data-ucmd="test"]', { timeout: 10000 });
      assert.ok(gets.length - getsBefore <= 2, 'the strip refetched once per change, not by polling: ' + (gets.length - getsBefore));
      assert.equal(await view.locator('.d-ucmd .ucmd').count(), 2);
      assert.equal(await view.locator('.d-ucmd > summary').textContent(), t.title);
      const build = view.locator('.ucmd[data-ucmd="build"]');
      assert.equal(await build.locator('.ucmd-run').textContent(), '▶ Build');
      assert.equal(await build.locator('.ucmd-desc').textContent(), 'compile everything');
      assert.equal(await build.locator('.ucmd-line').textContent(), 'make all', 'the exact stored command is shown');
      assert.equal(await view.locator('.ucmd[data-ucmd="test"] .ucmd-line').textContent(), 'make test\n./run --verbose', 'a multiline command is shown whole');
      await shot('buttons');

      // a click runs the stored line through the composer's route as a slot-run card
      await build.locator('.ucmd-run').click();
      await view.waitForSelector('.cexec[data-phase="done"]');
      const c0 = view.locator('.d-cexec .cexec').first();
      assert.equal(await c0.locator('.cexec-label').textContent(), 'Build');
      assert.equal(await c0.locator('code').textContent(), 'make all');
      assert.equal(await c0.locator('.cmdout').textContent(), 'hi\n');
      assert.ok((await c0.locator('.cexec-state').textContent()).includes(t.exit0));
      const runs = posts.filter(p => p.path.endsWith('/user-commands/build/run'));
      assert.equal(runs.length, 1);
      const body = JSON.parse(runs[0].body);
      assert.deepEqual(Object.keys(body).sort(), ['retry', 'revision', 'submission_id', 'token'], 'a click names the command and its revision, never text');
      assert.ok(posts.every(p => !p.path.endsWith('/message') && !p.path.endsWith('/composer-exec')), 'a slot click posts only to its own route: ' + JSON.stringify(posts.map(p => p.path)));
      await shot('slot-run');

      // an agent replacement the page has not heard of yet (its refetch is held
      // back): the click is stale, runs nothing, warns, and the strip refreshes
      await page.route('**/user-commands', route => route.request().method() === 'GET' ? route.abort() : route.continue());
      await agent('replace-build', lang);
      await page.waitForTimeout(400);
      assert.equal(await build.locator('.ucmd-line').textContent(), 'make all', 'the held-back page still shows the old command');
      await build.locator('.ucmd-run').click();
      await view.locator('.ucmd[data-ucmd="build"] .ucmd-line:text-is("make clean all")').waitFor({ timeout: 10000 });
      assert.ok(await view.locator('.ws-toast, [role=status], #toasts').first().innerText().then(s => s.includes(t.stale), () => false), 'the stale rejection is shown');
      assert.equal(await view.locator('.d-cexec .cexec').count(), 1, 'a stale click made no card');
      await shot('stale');
      await page.unroute('**/user-commands');

      // the agent withdraws a command: it disappears from the open page
      await agent('unregister-test', lang);
      await view.waitForSelector('.d-ucmd:not([hidden]) .ucmd[data-ucmd="test"]', { state: 'detached', timeout: 10000 });

      // the user's remove control withdraws the last one; the strip hides
      await view.locator('.ucmd[data-ucmd="build"] .ucmd-x').click();
      await view.waitForSelector('.d-ucmd', { state: 'hidden', timeout: 10000 });
      assert.ok(posts.some(p => p.path.endsWith('/user-commands/build/remove')), 'the remove control posted to its route');

      // two more commands: one named like an Object property (its button must
      // not be disabled by the busy table), and one clicked twice in a row
      await agent('register-extra', lang);
      await view.waitForSelector('.ucmd[data-ucmd="dbl"]', { timeout: 10000 });
      const ctor = view.locator('.ucmd[data-ucmd="constructor"] .ucmd-run');
      assert.equal(await ctor.isDisabled(), false, 'a command named "constructor" is runnable');
      const runsOf = id => posts.filter(p => p.path.endsWith('/user-commands/' + id + '/run')).length;
      await ctor.click();
      await view.waitForSelector('.cexec:has(code:text-is("echo ctor"))', { timeout: 10000 });
      assert.equal(runsOf('constructor'), 1);
      await page.waitForTimeout(900);
      assert.equal(await ctor.isDisabled(), false, 'it is runnable again once the guard passes');
      // a real double click is one run
      const dbl = view.locator('.ucmd[data-ucmd="dbl"] .ucmd-run');
      await dbl.dblclick();
      await page.waitForTimeout(900);
      assert.equal(runsOf('dbl'), 1, 'a double click made ' + runsOf('dbl') + ' requests');
      // two rapid separate clicks, the second one after the first one's
      // acknowledgement arrived (no click count tells them apart), are one run
      await page.waitForTimeout(300);
      const acked = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/user-commands/dbl/run'));
      await dbl.evaluate(el => el.click());
      await acked;
      await dbl.evaluate(el => el.click());
      await page.waitForTimeout(900);
      assert.equal(runsOf('dbl'), 2, 'two rapid clicks made ' + (runsOf('dbl') - 1) + ' requests');
      await shot('double-click');

      // answers arrive out of order: the user's removal of "dbl" is processed by
      // the daemon, but its answer is held back while the agent registers another
      // command and the stream's refetch shows the newer list; the older answer
      // then lands and must not replace it
      let held = null;
      await page.route('**/user-commands/dbl/remove', async route => { held = { route, response: await route.fetch() }; });
      await view.locator('.ucmd[data-ucmd="dbl"] .ucmd-x').click();
      for (let i = 0; i < 100 && !held; i++) await page.waitForTimeout(25);
      assert.ok(held, 'the removal reached the daemon');
      await agent('register-late', lang);
      await view.waitForSelector('.ucmd[data-ucmd="late"]', { timeout: 10000 });
      assert.equal(await view.locator('.ucmd[data-ucmd="dbl"]').count(), 0, 'the newer list no longer has the removed command');
      await held.route.fulfill({ response: held.response });
      await page.waitForTimeout(700);
      assert.equal(await view.locator('.ucmd[data-ucmd="late"]').count(), 1, 'an older removal answer did not replace the newer list');
      assert.equal(await view.locator('.d-ucmd .ucmd').count(), 2);
      await page.unroute('**/user-commands/dbl/remove');
      await shot('out-of-order');

      // never in the shared transcript: the session page any other viewer gets
      // carries no command text
      const shared = await (await page.request.get(base + '/s/livesession?frame=1', { headers: { 'Sec-Fetch-Dest': 'iframe' } })).text();
      assert.ok(!shared.includes('make clean all') && !shared.includes('compile everything') && !shared.includes('d-ucmd'), 'the shared page holds no command');
      await ctx.close();
    }
    assert.deepEqual(errors, []);
    console.log('user command browser acceptance passed');
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exit(1); });
