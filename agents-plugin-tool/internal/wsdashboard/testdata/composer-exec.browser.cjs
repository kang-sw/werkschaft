// Run by TestComposerExecBrowser against a live daemon whose command service
// is a fake: the composer's `!` commands in ko and en.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, shots] = process.argv.slice(2);

const TEXT = {
  ko: { exit0: '종료 코드 0', running: '실행 중…', async: '백그라운드에서 계속 실행 중입니다', refused: '실행되지 않음', file: '출력 파일', copy: '경로 복사', unsupported: '이 데몬은 여기서 ! 명령을 실행할 수 없습니다', attachments: '명령에는 첨부를 담을 수 없습니다', unknown: '결과를 알 수 없음' },
  en: { exit0: 'exit 0', running: 'Running…', async: 'Still running in the background', refused: 'Not run', file: 'Output file', copy: 'Copy path', unsupported: 'This daemon cannot run ! commands here', attachments: 'A command takes no attachments', unknown: 'Outcome unknown' },
};

// The session page runs in the shell's iframe.
async function sessionFrame(page) {
  for (let i = 0; i < 100; i++) {
    const f = page.frames().find(x => x !== page.mainFrame() && x.url().includes('/s/livesession'));
    if (f) { await f.waitForSelector('#composer', { state: 'attached', timeout: 30000 }); return f; }
    await page.waitForTimeout(200);
  }
  throw new Error('no session frame');
}
// The composer's editor is a contenteditable over the textarea backing.
const editor = view => view.locator('.cfield [contenteditable]').first();
const draft = view => view.evaluate(() => document.querySelector('#ctext').value);
async function type(view, text) {
  await editor(view).click();
  await view.page().keyboard.press('ControlOrMeta+A');
  await view.page().keyboard.press('Delete');
  await view.page().keyboard.insertText(text);
}
async function send(view, text) {
  await type(view, text);
  await view.click('#csend');
}
const card = (view, n) => view.locator('.d-cexec .cexec').nth(n);

(async () => {
  const browser = await chromium.launch({ headless: true });
  const errors = [];
  try {
    if (shots) fs.mkdirSync(shots, { recursive: true });
    for (const lang of ['ko', 'en']) {
      const t = TEXT[lang];
      const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: lang === 'ko' ? 'ko-KR' : 'en-US', reducedMotion: 'reduce' });
      await ctx.addCookies([{ name: 'ws_dashboard_lang', value: lang, url: base }]);
      const page = await ctx.newPage();
      page.on('pageerror', e => { errors.push(lang + ': ' + e.message); console.error('PAGE ERROR:', e.message); });
      const posts = [];
      const messages = [];
      page.on('request', r => { if (r.method() === 'POST') { posts.push(new URL(r.url()).pathname); if (new URL(r.url()).pathname.endsWith('/message')) messages.push(JSON.parse(r.postData() || '{}')); } });
      const shot = async name => { if (shots) await view.locator('#composer').screenshot({ path: path.join(shots, lang + '-' + name + '.png') }); };
      await page.goto(base + '/s/livesession');
      let view = await sessionFrame(page);
      await view.waitForSelector('#composer[data-cexec="1"]', { state: 'attached' });
      assert.equal(await view.locator('.d-cexec').count(), 0, 'no card before a command');

      // done with inline output
      await send(view, '!echo hi');
      await card(view, 0).waitFor();
      await view.waitForSelector('.cexec[data-phase="done"]');
      assert.equal(await card(view, 0).locator('.cmdout').textContent(), 'hi\n');
      assert.ok((await card(view, 0).locator('.cexec-state').textContent()).includes(t.exit0));
      assert.equal(await card(view, 0).locator('code').textContent(), 'echo hi', 'the card shows the command without the bang');
      assert.equal(await draft(view), '', 'the draft goes once the daemon holds the submission');
      assert.ok(posts.every(p => p.endsWith('/composer-exec')), 'a command is never posted as a message: ' + posts);
      await shot('done-inline');

      // submitting/running, then done
      await send(view, '!slow');
      await view.waitForSelector('.cexec[data-phase="running"]');
      assert.ok((await view.locator('.cexec[data-phase="running"] .cexec-state').textContent()).includes(t.running));
      await shot('running');
      await view.waitForSelector('.cexec[data-phase="done"] .cmdout:text("slow")');

      // done with an output file link
      await send(view, '!big');
      const link = view.locator('.cexec-file a');
      await link.waitFor();
      assert.ok((await link.textContent()).startsWith(t.file));
      assert.match(await link.getAttribute('href'), /^\/s\/livesession\/composer-exec\/cx[0-9a-f]+\/output$/);
      assert.equal(await view.locator('.cexec-copy').count(), 1);
      assert.equal(await view.locator('.cexec-copy').textContent(), t.copy);
      const bytes = await view.evaluate(async href => (await (await fetch(href, { credentials: 'same-origin' })).text()).length, await link.getAttribute('href'));
      assert.equal(bytes, 6800, 'the file route serves the whole output');
      await shot('done-file');

      // async-converted: only the job id, then the card finishes on refresh
      await send(view, '!async');
      await view.waitForSelector('.cexec[data-phase="async"]');
      assert.ok((await view.locator('.cexec[data-phase="async"] .cexec-state').textContent()).includes(t.async));
      assert.ok((await view.locator('.cexec[data-phase="async"] .cmdnote').first().textContent()).includes('exec_async'));
      await shot('async');
      await view.waitForSelector('.cexec[data-phase="done"] .cmdout:text("later")', { timeout: 20000 });

      // refused
      await send(view, '!deny');
      await view.waitForSelector('.cexec[data-phase="refused"]');
      assert.ok((await view.locator('.cexec[data-phase="refused"] .cexec-state').textContent()).includes(t.refused));
      await shot('refused');

      // never in the shared transcript: the session page any other viewer
      // gets carries no command or output
      const shared = await (await page.request.get(base + '/s/livesession?frame=1', { headers: { 'Sec-Fetch-Dest': 'iframe' } })).text();
      assert.ok(!shared.includes('echo hi') && !shared.includes('exec_async') && !shared.includes('class="cmdrun cexec"') && !shared.includes('exec_big') && !shared.includes('exec_slow'), 'the shared page holds no card');
      assert.equal(await view.locator('#r-convlive .cexec, [data-region] .cexec').count(), 0, 'the card is not inside a shared region');

      // a reload keeps the cards by id (the daemon still knows them)
      await page.reload();
      view = await sessionFrame(page);
      await view.waitForSelector('.d-cexec .cexec', { state: 'attached' });
      assert.ok(await view.locator('.d-cexec .cexec').count() >= 5, 'cards restored after reload');

      // a leading space or a prompt is an ordinary message, not a command
      const before = posts.length;
      await send(view, ' !not a command');
      await page.waitForTimeout(600);
      assert.ok(posts.slice(before).every(p => p.endsWith('/message')) , 'a leading space is an ordinary prompt: ' + posts.slice(before));

      // attachments: refused, the draft is kept
      await type(view, '!echo with image');
      await view.evaluate(() => {
        const dt = new DataTransfer();
        const png = Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=='), c => c.charCodeAt(0));
        dt.items.add(new File([png], 'a.png', { type: 'image/png' }));
        document.querySelector('.cfield [contenteditable]').dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }));
      });
      await page.waitForTimeout(400);
      const cardsBefore = await view.locator('.d-cexec .cexec').count();
      await view.click('#csend');
      await page.waitForTimeout(500);
      assert.ok((await draft(view)).startsWith('!echo with image'), 'the draft is kept');
      assert.ok(await view.locator('#cimgs').evaluate(el => el.children.length > 0), 'the attachment is kept');
      assert.equal(await view.locator('.d-cexec .cexec').count(), cardsBefore, 'no card for a refused submission');
      assert.ok(await view.locator('.ws-toast, [role=status], #toasts').first().innerText().then(s => s.includes(t.attachments), () => false), 'the attachment refusal is shown');
      await shot('attachments');

      // any attached composer state refuses the command, whatever its kind:
      // an inline pick capsule after the bang keeps the first byte intact
      await type(view, '!echo ');
      await view.evaluate(() => document.querySelector('#ctext').wsc.insertCapsule('pick', { label: 'div.x', anchor: '[pick:div.x]', plain: 'div.x', pick: { url: 'http://localhost/page', selector: 'div.x', text: 'x' } }));
      const beforePick = posts.length, cardsPick = await view.locator('.d-cexec .cexec').count();
      await view.click('#csend');
      await page.waitForTimeout(500);
      assert.equal(posts.length, beforePick, 'a command with a pick capsule posts nothing: ' + posts.slice(beforePick));
      assert.ok((await draft(view)).includes('[pick:div.x]'), 'the pick capsule stays in the draft');
      assert.equal(await view.locator('.d-cexec .cexec').count(), cardsPick, 'no card for a refused submission');
      assert.ok(await view.locator('.ws-toast, [role=status], #toasts').first().innerText().then(s => s.includes(t.attachments), () => false), 'the refusal is shown');
      // The refusal keeps the attachment for a regular send: made an ordinary
      // message (a leading space), the same draft goes once, with its pick, and
      // a second click finds nothing left to send.
      const msgBefore = messages.length;
      await editor(view).click();
      await view.page().keyboard.press('Home');
      await view.page().keyboard.insertText(' ');
      await view.click('#csend');
      for (let i = 0; i < 50 && messages.length === msgBefore; i++) await page.waitForTimeout(100);
      assert.equal(messages.length, msgBefore + 1, 'the regular send went out once');
      assert.ok(JSON.stringify(messages[msgBefore].picks || []).includes('div.x'), 'the pick was carried by the regular send: ' + JSON.stringify(messages[msgBefore]));
      assert.equal(await view.evaluate(() => document.querySelector('#ctext').wsc.ordered().length), 0, 'the pick capsule was consumed');
      assert.equal(await draft(view), '', 'the draft was consumed');
      await view.click('#csend');
      await page.waitForTimeout(400);
      assert.equal(messages.length, msgBefore + 1, 'the consumed attachments are not sent twice');

      // a slow acknowledgement clears only the acknowledged draft: what was
      // typed while the request was pending stays
      await page.route('**/composer-exec', async route => {
        if (route.request().method() === 'POST') await new Promise(r => setTimeout(r, 900));
        await route.continue();
      });
      await send(view, '!echo slowack');
      await editor(view).click();
      await view.page().keyboard.press('End');
      await view.page().keyboard.insertText(' typed meanwhile');
      await view.waitForSelector('.cexec code:text-is("echo slowack")');
      assert.equal(await draft(view), ' typed meanwhile', 'text typed while the request was pending is kept');
      // a replacement draft that begins with the submitted text, and the same
      // text typed again, are new drafts: the acknowledgement leaves both whole
      await send(view, '!echo one');
      await editor(view).click();
      await view.page().keyboard.press('ControlOrMeta+A');
      await view.page().keyboard.insertText('!echo one more');
      await view.waitForSelector('.cexec code:text-is("echo one")');
      assert.equal(await draft(view), '!echo one more', 'a replacement that begins with the submitted text stays whole');
      await send(view, '!echo two');
      await editor(view).click();
      await view.page().keyboard.press('ControlOrMeta+A');
      await view.page().keyboard.press('Delete');
      await view.page().keyboard.insertText('!echo two');
      await view.waitForSelector('.cexec code:text-is("echo two")');
      assert.equal(await draft(view), '!echo two', 'the same text typed again as a new draft stays');
      await page.unroute('**/composer-exec');

      // a lost response: the first request ran on the daemon but its answer
      // never arrived; sending the same text again is a retry of the same
      // submission and the command runs once
      let lostN = 0;
      await page.route('**/composer-exec', async route => {
        if (route.request().method() === 'POST' && lostN++ === 0) { await route.fetch(); await route.abort('failed'); return; }
        await route.continue();
      });
      await send(view, '!echo lost-once');
      await page.waitForTimeout(600);
      assert.equal(await draft(view), '!echo lost-once', 'the draft is kept when the answer was lost');
      assert.equal(await view.evaluate(k => !!window.sessionStorage.getItem(k), 'ws.cexec.attempt.livesession'), true, 'the attempt was recorded before it was sent');
      await view.click('#csend');
      await view.waitForSelector('.cexec code:text-is("echo lost-once")');
      assert.equal(await draft(view), '', 'the retry settles the draft');
      assert.equal(await view.evaluate(k => window.sessionStorage.getItem(k), 'ws.cexec.attempt.livesession'), null, 'the settled attempt is forgotten');
      assert.equal(await view.locator('.cexec code:text-is("echo lost-once")').count(), 1, 'one card for one submission');
      await page.unroute('**/composer-exec');

      // remembered cards render only after a successful private list read
      await page.route('**/composer-exec', route => route.request().method() === 'GET' ? route.fulfill({ status: 404, contentType: 'text/plain', body: 'unknown session' }) : route.continue());
      await page.reload();
      view = await sessionFrame(page);
      await view.waitForSelector('#composer[data-cexec="1"]', { state: 'attached' });
      await page.waitForTimeout(700);
      assert.equal(await view.locator('.d-cexec .cexec').count(), 0, 'a denied list read renders none of the remembered commands');
      assert.ok(!(await view.locator('body').innerText()).includes('echo slowack'), 'no remembered command text is shown');
      await page.unroute('**/composer-exec');
      await page.reload();
      view = await sessionFrame(page);
      await view.waitForSelector('.d-cexec .cexec', { state: 'attached' });

      // the first private list read answers with good headers but a body that is
      // cut short: the send that needed its token ends (busy clears, the draft is
      // kept, nothing runs), and sending again once the read works runs it once
      await page.route('**/composer-exec', route => route.request().method() === 'GET' ? route.fulfill({ status: 200, contentType: 'application/json', body: '{"cards":[' }) : route.continue());
      await page.reload();
      view = await sessionFrame(page);
      await view.waitForSelector('#composer[data-cexec="1"]', { state: 'attached' });
      await page.waitForTimeout(500);
      const beforeBroken = posts.length;
      await send(view, '!echo nojson');
      await page.waitForTimeout(600);
      assert.equal(await draft(view), '!echo nojson', 'the draft is kept when the token read fails after good headers');
      assert.equal(posts.length, beforeBroken, 'nothing was posted without a token');
      await view.click('#csend');
      await page.waitForTimeout(400);
      assert.equal(posts.length, beforeBroken, 'a second click reads again and is not stuck busy');
      await page.unroute('**/composer-exec');
      await view.click('#csend');
      await view.waitForSelector('.cexec code:text-is("echo nojson")');
      assert.equal(await draft(view), '', 'the send that follows a working read runs and settles the draft');
      assert.equal(await view.locator('.cexec code:text-is("echo nojson")').count(), 1, 'the command ran once');

      // an old daemon: the route is unknown. It fails closed: nothing sent
      // as a prompt, the draft stays.
      await page.reload();
      view = await sessionFrame(page);
      await view.waitForSelector('#composer[data-cexec="1"]', { state: 'attached' });
      await page.route('**/composer-exec', route => route.request().method() === 'POST' ? route.fulfill({ status: 404, contentType: 'text/plain', body: '404 page not found' }) : route.continue());
      const beforeOld = posts.length;
      await send(view, '!echo old daemon');
      await page.waitForTimeout(600);
      assert.equal(await draft(view), '!echo old daemon', 'the draft stays on an old daemon');
      assert.ok(posts.slice(beforeOld).every(p => p.endsWith('/composer-exec')), 'nothing was sent as a message: ' + posts.slice(beforeOld));
      assert.ok(await view.locator('body').innerText().then(s => s.includes(t.unsupported)), 'the unsupported notice is shown');
      await shot('unsupported');
      await ctx.close();
    }
    assert.deepEqual(errors, []);
    console.log('composer exec browser acceptance passed');
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exit(1); });
