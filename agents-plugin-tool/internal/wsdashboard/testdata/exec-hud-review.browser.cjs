// Run by TestExecHUDReviewBrowser against the same live daemon and fake exec job
// service as exec-hud.browser.cjs: the scenarios of the Phase 2 pre-merge review,
// each asserting the behavior that was wrong before its fix. argv: <daemon base>
// <control base> [<captures dir>] [<mockup index.html>]
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, ctl, shots] = process.argv.slice(2);

const TEXT = {
  en: { ended: /already ended/, stopped: /Stopped/, notifying: /notifying/, notified: /agent notified/, notFailed: /NOT notified/, gap: /earlier output was dropped/, exit0: /exited with code 0/, copyfail: /Copy failed/, copied: /Copied/ },
  ko: { ended: /이미 종료/, stopped: /중지됨/, notifying: /알리는 중/, notified: /알림 전송/, notFailed: /알림 실패/, gap: /이전 출력은 버려졌습니다/, exit0: /종료 코드 0로 끝남/, copyfail: /복사 실패/, copied: /복사됨/ },
};
const post = (p, b) => fetch(ctl + p, { method: 'POST', body: JSON.stringify(b || {}) }).then(r => r.json());
const get = p => fetch(ctl + p).then(r => r.json());
const J = (id, command, o = {}) => Object.assign({ id, tool: 'exec.shell', command, cwd: 'web/', state: 'running', origin: 'agent', started_at: Date.now() - 90000, ended_at: 0 }, o);
const waitStat = async (pred, ms, what) => { for (let t = 0; t < ms; t += 25) { const st = await get('/stats'); if (pred(st)) return st; await sleep(25); } assert.fail('timeout waiting for ' + what + ': ' + JSON.stringify(await get('/stats'))); };
const sleep = ms => new Promise(r => setTimeout(r, ms));
const report = [];
const partial = !!(process.env.WS_EXEC_HUD_SECTS || process.env.WS_EXEC_HUD_COMBOS);
const note = (k, v) => { report.push(k + ' = ' + v); console.log('MEASURE ' + k + ' = ' + v); };

async function openPage(browser, { lang, scheme, width = 1280 }) {
  const ctx = await browser.newContext({ viewport: { width, height: 900 }, locale: lang === 'ko' ? 'ko-KR' : 'en-US', colorScheme: scheme, permissions: ['clipboard-read', 'clipboard-write'] });
  await ctx.addCookies([{ name: 'ws_dashboard_lang', value: lang, url: base }]);
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', e => { errors.push('pageerror: ' + e.message); console.error('PAGE ERROR:', e.message); });
  await page.goto(base + '/s/livesession');
  let view = null;
  for (let i = 0; i < 100 && !view; i++) {
    view = page.frames().find(x => x !== page.mainFrame() && x.url().includes('/s/livesession'));
    if (!view) await sleep(200);
  }
  assert.ok(view, 'no session frame');
  await view.waitForSelector('#composer[data-cexec="1"]', { state: 'attached', timeout: 15000 });
  return { ctx, page, view, errors };
}
async function setJobs(jobs) { await post('/jobs', jobs); await post('/bump'); }
async function fakeBadges(view, o = {}) {
  await view.evaluate(o => {
    const sb = document.getElementById('sbadge'), ib = document.getElementById('ibadge'), bg = document.getElementById('bgbadge');
    sb.hidden = !o.sub; if (o.sub) { sb.classList.remove('idle'); sb.querySelector('.slabel').textContent = o.sub; }
    ib.hidden = !o.ib;
    bg.hidden = !o.bg; if (o.bg) bg.querySelector('.blabel').textContent = o.bg;
  }, o);
  await sleep(140);
}
async function snap(page, view, name, extra = 0) {
  if (!shots) return;
  const box = await view.evaluate(() => { const c = document.getElementById('composer').getBoundingClientRect(); return { top: c.top, bottom: c.bottom, right: c.right }; });
  const fb = await (await view.frameElement()).boundingBox();
  const y = Math.max(0, fb.y + box.top - 480 - extra), bottom = Math.min(fb.y + fb.height, fb.y + box.bottom + 14);
  await page.screenshot({ path: path.join(shots, name + '.png'), clip: { x: fb.x, y, width: Math.min(fb.width, box.right + 24), height: bottom - y } });
}
const focusInfo = view => view.evaluate(() => {
  const a = document.activeElement;
  return { tag: a && a.tagName, act: a && a.getAttribute && a.getAttribute('data-xact'), kind: a && a.getAttribute && a.getAttribute('data-xkind'), inPeek: !!(a && a.closest && a.closest('.xpeek')), inGroup: !!(a && a.closest && a.closest('.xgroup')),
    visible: !!(a && a.getClientRects && a.getClientRects().length && !a.closest('[hidden]')) };
});
const lineCount = (view, text) => view.evaluate(t => [...document.querySelectorAll('.xout .l')].filter(n => n.textContent.includes(t)).length, text);
async function openChip(view, id) {
  const c = view.locator('.xchip[data-xid="' + id + '"]');
  if (await c.isVisible()) await c.click(); else await view.evaluate(id => document.querySelector('.xgroup .xchip[data-xid="' + id + '"]').dispatchEvent(new MouseEvent('click', { bubbles: true })), id);
  await view.waitForSelector('.xpeek:not([hidden]) .xout');
}
async function closePeek(page, view) { await page.keyboard.press('Escape'); await view.waitForSelector('.xpeek', { state: 'hidden' }); }

async function run(browser, { lang, scheme }) {
  const T = TEXT[lang], tag = lang + '-' + scheme;
  let st, fi;
  // sect runs one review scenario; WS_EXEC_HUD_SECTS narrows a development run to some of them (such a run
  // exits non-zero at the end, so it is never mistaken for a pass).
  const sect = async (name, fn) => {
    if (process.env.WS_EXEC_HUD_SECTS && !process.env.WS_EXEC_HUD_SECTS.split(',').includes(name)) return;
    return fn();
  };
  await post('/set', { notice: 'sent' });
  await setJobs([]);
  const { ctx, page, view, errors } = await openPage(browser, { lang, scheme });
  console.log('--- ' + tag);
  await fakeBadges(view, { ib: true, sub: '3' });

  // I1: reselecting the open tab changes nothing: no second copy, no rewind.
  await sect('I1', async () => {
    await setJobs([J('exec_t1', 'tail -f a.log', { started_at: Date.now() - 20000 }), J('exec_t2', 'tail -f b.log', { started_at: Date.now() - 10000 })]);
    await post('/out', { ID: 'exec_t1', Stream: 'stdout', Text: 'UNIQUE-LINE-ONE\n' });
    await view.waitForSelector('.xchip[data-xid="exec_t2"]');
    await openChip(view, 'exec_t1');
    await view.waitForFunction(() => document.querySelector('.xout').textContent.includes('UNIQUE-LINE-ONE'));
    await sleep(600);
    await post('/reset');
    await view.click('.xp-tab[aria-selected="true"]');
    await view.locator('.xp-tab[aria-selected="true"]').focus();
    await page.keyboard.press('Enter');
    await sleep(1500);
    assert.equal(await lineCount(view, 'UNIQUE-LINE-ONE'), 1, 'I1: reselecting the open tab does not repeat its output');
    st = await get('/stats');
    const froms = (st.fromLog.exec_t1 || []);
    assert.ok(froms.length > 0 && Math.min(...froms) >= 16, 'I1: the read cursor never rewinds: ' + JSON.stringify(froms));
    await closePeek(page, view);
  });

  // Minor: reopening the same job through the aggregate (320 px) keeps its output. It seeds its own jobs.
  await sect('REOPEN', async () => {
    await setJobs([J('exec_r1', 'tail -f r.log', { started_at: Date.now() - 20000 }), J('exec_r2', 'tail -f s.log', { started_at: Date.now() - 10000 })]);
    await post('/out', { ID: 'exec_r1', Stream: 'stdout', Text: 'REOPEN-LINE\n' });
    await page.setViewportSize({ width: 320, height: 900 });
    await sleep(300);
    await view.waitForSelector('.xgroup .xagg', { state: 'visible' });
    await view.click('.xgroup .xagg');
    await view.waitForFunction(() => document.querySelector('.xpeek:not([hidden]) .xout') && document.querySelector('.xout').textContent.includes('REOPEN-LINE'));
    assert.equal(await view.evaluate(() => document.querySelector('.xp-tab[aria-selected="true"]').getAttribute('data-xtab')), 'exec_r1', 'the aggregate opens its first folded job');
    await sleep(500);
    await post('/reset');
    await view.click('.xgroup .xagg');
    await waitStat(s => (s.fromLog.exec_r1 || []).length > 0, 5000, 'a read after the aggregate reopened the open job');
    await sleep(600);
    assert.equal(await lineCount(view, 'REOPEN-LINE'), 1, 'aggregate reopen of the open job does not repeat its output');
    st = await get('/stats');
    assert.ok(Math.min(...st.fromLog.exec_r1) >= 12, 'reopen: no rewind: ' + JSON.stringify(st.fromLog));
    await page.keyboard.press('Escape');
    await page.setViewportSize({ width: 1280, height: 900 });
    await sleep(300);
    await view.evaluate(() => { const p = document.querySelector('.xpeek'); if (p && !p.hidden) document.querySelector('.xpeek [data-xact="close"]').click(); });
  });

  // I2: a read in flight across a stop and an end: one read at a time, and an older answer takes nothing back.
  await sect('I2', async () => {
    await setJobs([J('exec_d1', 'long task one'), J('exec_d2', 'long task two', { started_at: Date.now() - 80000 })]);
    await post('/out', { ID: 'exec_d1', Stream: 'stdout', Text: 'D1-LINE\n' });
    await post('/out', { ID: 'exec_d2', Stream: 'stdout', Text: 'D2-LINE\n' });
    await view.waitForSelector('.xchip[data-xid="exec_d1"]');
    await post('/set', { notice: 'sent', outDelayMs: 700 });
    await post('/reset');
    await view.evaluate(() => {
      window.__hist = [];
      new MutationObserver(() => { const c = document.querySelector('.xchip[data-xid="exec_d1"]'), v = c && c.getAttribute('data-st'); if (v && window.__hist[window.__hist.length - 1] !== v) window.__hist.push(v); }).observe(document.querySelector('.xgroup'), { subtree: true, childList: true, attributes: true });
    });
    await openChip(view, 'exec_d1');
    await view.click('.xpeek [data-xact="stop"]');
    await view.waitForSelector('.xconf');
    // confirm while the first read is actually out (the fake holds each answer 700 ms)
    await waitStat(s => (s.inflight.exec_d1 || 0) === 1, 8000, 'a read in flight for exec_d1');
    await view.click('.xconf [data-xact="confirm"]');
    await view.waitForSelector('.xchip[data-xid="exec_d1"][data-st="stopped"]', { timeout: 5000 });
    await sleep(2600);
    const hist = await view.evaluate(() => window.__hist);
    const firstStopped = hist.indexOf('stopped');
    assert.ok(firstStopped >= 0 && !hist.slice(firstStopped).includes('running'), 'I2: a stale running snapshot never takes the stop back: ' + JSON.stringify(hist));
    st = await get('/stats');
    assert.equal(st.maxInflight.exec_d1, 1, 'I2: one output read in flight per tab across a stop: ' + JSON.stringify(st.maxInflight));
    assert.equal(await lineCount(view, 'D1-LINE'), 1, 'I2: no repeated output');
    assert.equal(await view.locator('.xnotice[data-ns="sent"]').count(), 1, 'the notice settles');
    // an end plus a burst of events while a read is out: still one read at a time, wake-ups coalesced.
    await openChip(view, 'exec_d2').catch(async () => { await view.click('.xp-tab[data-xtab="exec_d2"]'); });
    await view.waitForFunction(() => document.querySelector('.xout').textContent.includes('D2-LINE'));
    await post('/reset');
    await post('/out', { ID: 'exec_d2', Stream: 'stdout', Text: 'D2-FINAL\n' });
    await post('/patch', { id: 'exec_d2', state: 'exited', exit_code: 0 });
    for (let i = 0; i < 5; i++) { await post('/bump'); await sleep(40); }
    await view.waitForFunction(re => { const t = document.querySelector('.xout').textContent; return t.includes('D2-FINAL') && new RegExp(re).test(t); }, T.exit0.source, { timeout: 8000 });
    await sleep(2500);
    st = await get('/stats');
    assert.ok((st.counts['output:exec_d2'] || 0) >= 1, 'I2: the end drained the output with a read: ' + JSON.stringify(st.counts));
    assert.equal(await lineCount(view, 'D2-FINAL'), 1, 'I2: the final output is shown once');
    assert.ok((st.maxInflight.exec_d2 || 0) <= 1, 'I2: a burst of events keeps one read in flight: ' + JSON.stringify(st.maxInflight));
    assert.ok((st.counts['output:exec_d2'] || 0) <= 4, 'I2: wake-ups coalesce into a few reads: ' + JSON.stringify(st.counts));
    assert.equal(await lineCount(view, 'D2-LINE'), 1, 'I2: no repeated output after the end');
    assert.equal(await view.evaluate(re => [...document.querySelectorAll('.xout .l.sys')].filter(n => new RegExp(re).test(n.textContent)).length, T.exit0.source), 1, 'I2: one terminal marker');
    await post('/set', { notice: 'sent', outDelayMs: 0 });
    await view.evaluate(() => { const p = document.querySelector('.xpeek'); if (p && !p.hidden) document.querySelector('.xpeek [data-xact="close"]').click(); });
  });

  // I3: one endless line: a bounded suffix, and memory bounded by bytes.
  await sect('I3', async () => {
    await setJobs([J('exec_big', 'cat build.json')]);
    await post('/out', { ID: 'exec_big', Stream: 'stdout', Text: 'x'.repeat(40000) + 'TAILMARK' });
    await view.waitForSelector('.xchip[data-xid="exec_big"]');
    await openChip(view, 'exec_big');
    await view.waitForFunction(() => document.querySelector('.xout').textContent.includes('TAILMARK'), null, { timeout: 8000 });
    let big = await view.evaluate(() => { const o = document.querySelector('.xout'); return { len: o.textContent.length, cut: o.querySelectorAll('.l.cut').length, end: o.textContent.endsWith('TAILMARK') }; });
    assert.ok(big.end && big.len > 1000 && big.len <= 16500 && big.cut === 1, 'I3: a >16 KiB single line shows its bounded suffix, marked cut: ' + JSON.stringify(big));
    assert.equal(await view.evaluate(() => getComputedStyle(document.querySelector('.xout .l.cut'), '::before').content), '"…"', 'I3: a cut line starts with the ellipsis');
    await snap(page, view, tag + '-16-long-line');
    for (let i = 0; i < 8; i++) await post('/out', { ID: 'exec_big', Stream: 'stdout', Text: 'y'.repeat(60000) + (i === 7 ? 'ENDMARK' : '') });
    await view.waitForFunction(() => document.querySelector('.xout').textContent.endsWith('ENDMARK'), null, { timeout: 20000 });
    big = await view.evaluate(() => { const o = document.querySelector('.xout'); return { len: o.textContent.length, nodes: o.querySelectorAll('.l').length, cut: o.querySelectorAll('.l.cut').length }; });
    assert.ok(big.len <= 200000 && big.cut >= 1, 'I3: a growing partial line is capped by bytes: ' + JSON.stringify(big));
    for (let i = 0; i < 3000; i++) if (i % 500 === 0) await post('/out', { ID: 'exec_big', Stream: 'stdout', Text: Array.from({ length: 500 }, (_, k) => 'row ' + (i + k) + ' ' + 'z'.repeat(190)).join('\n') + '\n' });
    await view.waitForFunction(() => document.querySelector('.xout').textContent.includes('row 2999'), null, { timeout: 20000 });
    big = await view.evaluate(() => { const o = document.querySelector('.xout'); return { len: o.textContent.length, nodes: o.childNodes.length }; });
    assert.ok(big.len <= 200000 && big.nodes <= 1500, 'I3: many lines are capped by bytes as well as lines: ' + JSON.stringify(big));
    note('retained peek text after 1.1 MB (UTF-16 code units, line nodes)', big.len + ', ' + big.nodes);
    await closePeek(page, view);
  });

  // I3b: trimming with mixed stdout/stderr keeps the stderr tint on the surviving span (one endless line).
  await sect('I3b', async () => {
    await setJobs([J('exec_mix', 'noisy build')]);
    await view.waitForSelector('.xchip[data-xid="exec_mix"]');
    await openChip(view, 'exec_mix');
    await post('/out', { ID: 'exec_mix', Stream: 'stdout', Text: 'o'.repeat(60000) });
    await post('/out', { ID: 'exec_mix', Stream: 'stderr', Text: 'e'.repeat(100000) });
    await post('/out', { ID: 'exec_mix', Stream: 'stdout', Text: 'c'.repeat(60000) + 'MIXEND' });
    await view.waitForFunction(() => document.querySelector('.xout').textContent.endsWith('MIXEND'), null, { timeout: 20000 });
    await sleep(400);
    const m = await view.evaluate(() => {
      const o = document.querySelector('.xout'), ls = [...o.querySelectorAll('.l')];
      return { len: o.textContent.length, spans: ls.map(l => ({ err: l.classList.contains('e'), ch: l.textContent.charAt(0), n: l.textContent.length })) };
    });
    assert.ok(m.len <= 196608, 'I3b: the cap holds in UTF-16 code units: ' + m.len);
    assert.ok(m.spans.some(x => x.err && x.ch === 'e' && x.n > 1000), 'I3b: the surviving stderr span keeps its tint: ' + JSON.stringify(m.spans));
    assert.ok(m.spans.every(x => x.err === (x.ch === 'e')), 'I3b: tint follows the stream of each span: ' + JSON.stringify(m.spans));
    assert.ok(!m.spans.some(x => x.ch === 'o' && x.n === 60000), 'I3b: the oldest span was trimmed: ' + JSON.stringify(m.spans));
    await closePeek(page, view);
  });

  // I4: Escape in the HUD does not close the real drawer; the drawer's own Escape still does.
  await sect('I4', async () => {
    await setJobs([J('exec_e1', 'sleep 300')]);
    await view.waitForSelector('.xchip[data-xid="exec_e1"]');
    await openChip(view, 'exec_e1');
    await view.click('.xpeek [data-xact="pin"]');
    await view.click('#shandle');
    await view.waitForSelector('.drawer.open', { state: 'attached' });
    await view.locator('.xp-tab[aria-selected="true"]').focus();
    await page.keyboard.press('Escape');
    await view.waitForSelector('.xpeek', { state: 'hidden' });
    assert.equal(await view.locator('.drawer.open').count(), 1, 'I4: the HUD Escape leaves the drawer open');
    await sleep(150);
    await view.evaluate(() => document.querySelector('.xchip[data-xid="exec_e1"]').focus());
    await view.evaluate(() => document.getElementById('shandle').blur());
    await view.evaluate(() => { const c = document.querySelector('.xchip[data-xid="exec_e1"]'); if (c) c.blur(); document.body.focus(); });
    await page.keyboard.press('Escape');
    await view.waitForFunction(() => !document.querySelector('.drawer.open'), null, { timeout: 4000 });
  });

  // I5: the keyboard survives a confirmation timeout, an end under the confirmation, and a fold.
  await sect('I5', async () => {
    await setJobs([J('exec_f1', 'npm run dev'), J('exec_f2', 'cargo watch', { started_at: Date.now() - 50000 })]);
    await view.waitForSelector('.xchip[data-xid="exec_f2"]');
    await openChip(view, 'exec_f1');
    await view.locator('.xpeek [data-xact="stop"]').focus();
    await page.keyboard.press('Enter');
    await view.waitForSelector('.xconf');
    assert.equal((await focusInfo(view)).act, 'cancel');
    await sleep(8800);
    assert.equal(await view.locator('.xconf').count(), 0, 'the confirmation timed out');
    fi = await focusInfo(view);
    assert.ok(fi.inPeek && fi.visible, 'I5: focus stays in the panel after the confirmation timed out: ' + JSON.stringify(fi));
    await page.keyboard.press('Escape');
    await view.waitForSelector('.xpeek', { state: 'hidden' });
    fi = await focusInfo(view);
    assert.ok(fi.inGroup && fi.visible, 'I5: Escape after the timeout closed the panel and focus is on the strip: ' + JSON.stringify(fi));
    // an end under the confirmation
    await openChip(view, 'exec_f2');
    await view.locator('.xpeek [data-xact="stop"]').focus();
    await page.keyboard.press('Enter');
    await view.waitForSelector('.xconf');
    await post('/patch', { id: 'exec_f2', state: 'exited', exit_code: 0 });
    await view.waitForSelector('.xconf', { state: 'detached', timeout: 5000 });
    fi = await focusInfo(view);
    assert.ok(fi.inPeek && fi.visible, 'I5: focus stays in the panel when the job ends under the confirmation: ' + JSON.stringify(fi));
    await page.keyboard.press('Escape');
    await view.waitForSelector('.xpeek', { state: 'hidden' });
    // the fold takes the focused chip
    const many = Array.from({ length: 6 }, (_, i) => J('exec_g' + i, 'job number ' + i + ' --watch', { started_at: Date.now() - (60 - i) * 1000 }));
    await page.setViewportSize({ width: 1400, height: 900 });
    await setJobs(many);
    await fakeBadges(view, { ib: true, sub: '3' });
    await view.waitForSelector('.xchip[data-xid="exec_g0"]', { state: 'visible' });
    const lastShown = await view.evaluate(() => { const v = [...document.querySelectorAll('.xgroup .xchip[data-xid]')].filter(n => n.getClientRects().length); v[v.length - 1].focus(); return v[v.length - 1].getAttribute('data-xid'); });
    await page.keyboard.press('Enter');
    await view.waitForSelector('.xpeek:not([hidden])');
    await page.setViewportSize({ width: 520, height: 900 });
    await sleep(500);
    fi = await focusInfo(view);
    assert.ok(fi.inGroup && fi.visible, 'I5: focus moves to a drawn control when the chip folds away (was ' + lastShown + '): ' + JSON.stringify(fi) + JSON.stringify(await view.evaluate(() => ({ g: !document.querySelector('.xgroup').hidden, vis: [...document.querySelectorAll('.xgroup .xchip, .xgroup .xmore')].map(n => n.className + ':' + n.getClientRects().length + ':' + n.hidden) }))));
    await page.keyboard.press('Escape');
    await view.waitForSelector('.xpeek', { state: 'hidden' });
    fi = await focusInfo(view);
    assert.ok(fi.inGroup && fi.visible, 'I5: closing a panel whose chip folded focuses a drawn control: ' + JSON.stringify(fi));
    await page.keyboard.press('ArrowRight');
    fi = await focusInfo(view);
    assert.ok(fi.inGroup && fi.visible, 'I5: the toolbar still takes arrows: ' + JSON.stringify(fi));
    await page.setViewportSize({ width: 1280, height: 900 });
  });

  // I6: an ended job is dismissed by keyboard from the panel when only the aggregate is drawn.
  await sect('I6', async () => {
    await page.setViewportSize({ width: 320, height: 900 });
    await sleep(300);
    await setJobs([J('exec_x1', 'go vet ./...', { state: 'exited', exit_code: 1, started_at: Date.now() - 9000, ended_at: Date.now() - 4000 }), J('exec_x2', 'make test', { state: 'exited', exit_code: 2, started_at: Date.now() - 8000, ended_at: Date.now() - 3000 })]);
    await view.waitForSelector('.xgroup .xagg', { state: 'visible' });
    assert.equal(await view.evaluate(() => [...document.querySelectorAll('.xgroup .xchip[data-xid]')].filter(n => n.getClientRects().length).length), 0, 'only the aggregate is drawn at 320 px');
    await view.locator('.xgroup .xagg').focus();
    await page.keyboard.press('Enter');
    await view.waitForSelector('.xpeek:not([hidden])');
    await view.locator('.xp-tab[aria-selected="true"]').focus();
    await sleep(350);
    await snap(page, view, tag + '-14-peek-ended-dismiss');
    let reached = false;
    for (let i = 0; i < 14 && !reached; i++) { await page.keyboard.press('Tab'); reached = (await focusInfo(view)).act === 'dismiss'; }
    assert.ok(reached, 'I6: Tab reaches a Dismiss button in the panel');
    const firstTab = await view.evaluate(() => document.querySelector('.xp-tab[aria-selected="true"]').getAttribute('data-xtab'));
    await page.keyboard.press('Enter');
    await sleep(250);
    assert.equal(await view.locator('.xp-tab[data-xtab="' + firstTab + '"]').count(), 0, 'I6: the dismissed job is gone from the panel');
    fi = await focusInfo(view);
    assert.ok(fi.inPeek, 'I6: the panel moved to the other job and keeps the keyboard: ' + JSON.stringify(fi));
    assert.notEqual(await view.evaluate(() => document.querySelector('.xp-tab[aria-selected="true"]').getAttribute('data-xtab')), firstTab);
    reached = false;
    for (let i = 0; i < 14 && !reached; i++) { await page.keyboard.press('Tab'); reached = (await focusInfo(view)).act === 'dismiss'; }
    await page.keyboard.press('Enter');
    await view.waitForSelector('.xpeek', { state: 'hidden', timeout: 3000 });
    fi = await focusInfo(view);
    assert.ok(!fi.inPeek, 'I6: the last dismissal closes the panel: ' + JSON.stringify(fi));
    await page.setViewportSize({ width: 1280, height: 900 });
  });

  // I7: the strip stays inside the protected gap: rendered children, three-digit counts, changing badge widths.
  await sect('I7', async () => {
    const crowd = Array.from({ length: 120 }, (_, i) => J('exec_n' + i, 'worker ' + i + ' --serve', { started_at: Date.now() - (2000 + i * 500) }));
    crowd[3] = J('exec_n3', 'failing task', { state: 'exited', exit_code: 1, started_at: Date.now() - 9000, ended_at: Date.now() - 2000 });
    await setJobs(crowd);
    await view.waitForSelector('.xgroup:not([hidden])');
    const bounds = () => view.evaluate(() => {
      const r = e => { const b = e.getBoundingClientRect(); return { l: b.left, r: b.right, w: b.width }; };
      const ib = document.getElementById('ibadge'), sb = document.getElementById('sbadge'), bg = document.getElementById('bgbadge'), cb = document.querySelector('.cbadges');
      const vis = e => e && !e.hidden && e.getClientRects().length;
      const left = vis(ib) ? r(ib).r : r(cb).l, right = Math.min(r(cb).r, vis(sb) ? r(sb).l : 1e9, vis(bg) ? r(bg).l : 1e9);
      const kids = [...document.querySelectorAll('.xgroup .xchip, .xgroup .xmore')].filter(n => n.getClientRects().length && !n.closest('[hidden]'));
      const agg = document.querySelector('.xgroup .xagg');
      let full = null;
      if (agg && vis(agg) && agg.classList.contains('compact')) { agg.classList.remove('compact'); full = agg.getBoundingClientRect().width; agg.classList.add('compact'); }
      return { left, right, kids: kids.map(n => ({ k: n.className, ...r(n) })), compact: !!(agg && vis(agg) && agg.classList.contains('compact')), aggW: agg && vis(agg) ? r(agg).w : 0, full, counts: kids.map(n => n.textContent) };
    });
    let compactSeen = 0, checks = 0;
    for (const cfg of [{ ib: true, sub: '3' }, { ib: true, sub: '12(+34)', bg: '2' }, { ib: true, sub: '999(+999)', bg: '88' }, { ib: false, sub: '' }]) {
      for (let w = 1400; w >= 300; w -= 16) {
        await page.setViewportSize({ width: w, height: 900 });
        await fakeBadges(view, cfg);
        const b = await bounds();
        for (const k of b.kids) {
          assert.ok(k.l >= b.left - 0.75 && k.r <= b.right + 0.75, 'I7: ' + k.k + ' [' + k.l.toFixed(1) + ',' + k.r.toFixed(1) + '] inside the gap [' + b.left.toFixed(1) + ',' + b.right.toFixed(1) + '] at viewport ' + w + ' ' + JSON.stringify(cfg) + ' ' + JSON.stringify(b.counts));
        }
        checks++;
        if (b.compact) { compactSeen++; assert.ok(b.aggW < b.full - 4, 'I7: the compact aggregate is narrower (' + b.aggW + ' vs ' + b.full + ')'); }
      }
    }
    note('I7 gap checks / widths where the aggregate compacts', checks + ' / ' + compactSeen);
    assert.ok(compactSeen > 0, 'I7: the aggregate compacts at some width (it is the last step before hiding)');
    // badge widths change under a fixed viewport
    await page.setViewportSize({ width: 560, height: 900 });
    for (const cfg of [{ ib: true, sub: '3' }, { ib: true, sub: '999(+999)', bg: '88' }, { ib: true, sub: '12(+34)', bg: '2' }, { ib: true, sub: '3' }, { ib: false, sub: '9' }]) {
      await fakeBadges(view, cfg);
      await sleep(120);
      const b = await bounds();
      for (const k of b.kids) assert.ok(k.l >= b.left - 0.75 && k.r <= b.right + 0.75, 'I7: ' + k.k + ' inside the gap after the badges changed ' + JSON.stringify(cfg) + ' ' + JSON.stringify(b));
    }
    await page.setViewportSize({ width: 380, height: 900 });
    await fakeBadges(view, { ib: true, sub: '12(+34)', bg: '2' });
    await snap(page, view, tag + '-15-compact-aggregate');
    await page.setViewportSize({ width: 1280, height: 900 });
    await fakeBadges(view, { ib: true, sub: '3' });
  });

  // I8: a toast says what happened. A job that exited before the stop landed was not "stopped";
  await sect('I8', async () => {
    // a toast still up follows the notice, and it offers View only (the approved mockup).
    await post('/set', { notice: 'pending', stopMs: 700 });
    await setJobs([J('exec_ta', 'watch A', { started_at: Date.now() - 70000 }), J('exec_tb', 'watch B', { started_at: Date.now() - 60000 }), J('exec_tc', 'watch C', { started_at: Date.now() - 50000 })]);
    await view.waitForSelector('.xchip[data-xid="exec_tc"]', { state: 'attached' });
    await openChip(view, 'exec_tb');
    await view.click('.xpeek [data-xact="stop"]');
    await view.waitForSelector('.xconf');
    await view.click('.xconf [data-xact="confirm"]');
    await post('/patch', { id: 'exec_tb', state: 'exited', exit_code: 0 });
    await view.click('.xp-tab[data-xtab="exec_ta"]');
    await view.waitForSelector('.xtoast:not([hidden])', { timeout: 6000 });
    let toast = await view.locator('.xtoast').textContent();
    assert.match(toast, T.ended, 'I8: the job exited before the stop landed: ' + toast);
    assert.doesNotMatch(toast, T.stopped, 'I8: a natural exit is not reported as stopped: ' + toast);
    assert.equal(await view.locator('.xtoast button').count(), 1, 'the toast offers View only');
    await snap(page, view, tag + '-17-toast-ended');
    await view.evaluate(() => { document.querySelector('.xtoast').hidden = true; });
    // pending notice, then it lands
    await view.click('.xp-tab[data-xtab="exec_tc"]');
    await view.click('.xpeek [data-xact="stop"]');
    await view.waitForSelector('.xconf');
    await view.click('.xconf [data-xact="confirm"]');
    await view.click('.xp-tab[data-xtab="exec_ta"]');
    await view.waitForSelector('.xtoast:not([hidden])', { timeout: 6000 });
    toast = await view.locator('.xtoast').textContent();
    assert.match(toast, T.notifying, 'I8: the toast opens on "notifying": ' + toast);
    await post('/patch', { id: 'exec_tc', state: 'stopped', stopped_by_user: true, stop_notice: 'sent' });
    await view.waitForFunction(re => new RegExp(re).test(document.querySelector('.xtoast').textContent), T.notified.source, { timeout: 4000 });
    assert.equal(await view.locator('.xtoast').isVisible(), true);
    // pending notice, then it fails
    await view.evaluate(() => { document.querySelector('.xtoast').hidden = true; });
    await setJobs([J('exec_td', 'watch D', { started_at: Date.now() - 40000 }), J('exec_te', 'watch E', { started_at: Date.now() - 30000 })]);
    await post('/set', { notice: 'pending', stopMs: 700 });
    await view.waitForSelector('.xchip[data-xid="exec_te"]', { state: 'attached' });
    await openChip(view, 'exec_td');
    await view.click('.xpeek [data-xact="stop"]');
    await view.waitForSelector('.xconf');
    await view.click('.xconf [data-xact="confirm"]');
    await view.click('.xp-tab[data-xtab="exec_te"]');
    await view.waitForSelector('.xtoast:not([hidden])', { timeout: 6000 });
    assert.match(await view.locator('.xtoast').textContent(), T.notifying, 'I8: the toast opens on "notifying" again');
    await post('/patch', { id: 'exec_td', state: 'stopped', stopped_by_user: true, stop_notice: 'failed' });
    await view.waitForFunction(re => new RegExp(re).test(document.querySelector('.xtoast').textContent), T.notFailed.source, { timeout: 4000 });
    assert.equal(await view.locator('.xtoast button').count(), 1, 'I8: the failed toast still offers View only');
    await post('/set', { notice: 'sent', stopMs: 0 });
    await closePeek(page, view);
  });

  // Minor: the copy label follows the outcome.
  await sect('COPY', async () => {
    await setJobs([J('exec_cp', 'echo copy me')]);
    await view.waitForSelector('.xchip[data-xid="exec_cp"]');
    await openChip(view, 'exec_cp');
    await view.click('.xpeek [data-xact="copy"]');
    await view.waitForFunction(re => new RegExp(re).test(document.querySelector('.xpeek [data-xact="copy"]').textContent), T.copied.source);
    await sleep(1600);
    await view.evaluate(() => { navigator.clipboard.writeText = () => Promise.reject(new Error('denied')); document.execCommand = () => false; });
    await view.click('.xpeek [data-xact="copy"]');
    await view.waitForFunction(re => new RegExp(re).test(document.querySelector('.xpeek [data-xact="copy"]').textContent), T.copyfail.source, { timeout: 3000 });
    assert.doesNotMatch(await view.locator('.xpeek [data-xact="copy"]').textContent(), T.copied, 'a failed copy is not reported as copied');
    const onCopy = () => view.evaluate(() => { const a = document.activeElement; return { copy: !!(a && a.getAttribute && a.getAttribute('data-xact') === 'copy'), id: a && a.id, tag: a && a.tagName }; });
    // a slow rejection while the keyboard stays on Copy: Copy gets it back from the scratch field
    await sleep(1600);
    await view.evaluate(() => { navigator.clipboard.writeText = () => new Promise((_, rej) => setTimeout(() => rej(new Error('denied')), 700)); });
    await view.locator('.xpeek [data-xact="copy"]').focus();
    await page.keyboard.press('Enter');
    await view.waitForFunction(re => new RegExp(re).test(document.querySelector('.xpeek [data-xact="copy"]').textContent), T.copyfail.source, { timeout: 3000 });
    assert.equal((await onCopy()).copy, true, 'the keyboard stayed on Copy: it is on Copy after the slow failure');
    // a slow rejection after the user moved on to the composer: Copy does not take focus back
    await sleep(1600);
    await view.click('.xpeek [data-xact="pin"]');
    await view.locator('.xpeek [data-xact="copy"]').focus();
    await page.keyboard.press('Enter');
    await view.evaluate(() => { const t = document.getElementById('ctext'), ed = (t.wsc && t.wsc.editor) || t; ed.focus(); });
    fi = await focusInfo(view);
    assert.ok(!fi.inPeek && fi.tag !== 'BODY', 'the composer holds the keyboard while the copy is pending: ' + JSON.stringify(fi));
    await view.waitForFunction(re => new RegExp(re).test(document.querySelector('.xpeek [data-xact="copy"]').textContent), T.copyfail.source, { timeout: 3000 });
    const after = await onCopy();
    assert.equal(after.copy, false, 'COPY: a delayed rejection does not steal focus back to Copy: ' + JSON.stringify(after));
    await view.click('.xpeek [data-xact="pin"]');
    await closePeek(page, view).catch(async () => { await view.evaluate(() => document.querySelector('.xpeek [data-xact="close"]').click()); });
  });

  // ORDER (round 2, I1): a stop or retry answer is one snapshot among others; the newest list wins, and
  // a real retry (failed -> pending -> sent) reaches a second tab.
  const stopped = (id, cmd, notice, o = {}) => J(id, cmd, Object.assign({ state: 'stopped', stopped_by_user: true, stop_notice: notice, started_at: Date.now() - 30000, ended_at: Date.now() - 5000 }, o));
  const closeAny = () => view.evaluate(() => { const t = document.querySelector('.xtoast'); if (t) t.hidden = true; const p = document.querySelector('.xpeek'); if (p && !p.hidden) document.querySelector('.xpeek [data-xact="close"]').click(); });
  await sect('ORDER-RETRY', async () => {
    const B = await openPage(browser, { lang, scheme });
    const bv = B.view;
    await post('/set', { notice: 'sent' });
    await setJobs([stopped('exec_o1', 'deploy --prod', 'failed')]);
    await view.waitForSelector('.xchip[data-xid="exec_o1"]');
    await bv.waitForSelector('.xchip[data-xid="exec_o1"]');
    await openChip(view, 'exec_o1');
    await openChip(bv, 'exec_o1');
    await view.waitForSelector('.xnotice[data-ns="failed"]');
    await bv.waitForSelector('.xnotice[data-ns="failed"]');
    // the retry lands as pending: the other tab, holding failed, takes it
    await post('/set', { notice: 'pending' });
    await view.click('.xnotice [data-xact="retry"]');
    await bv.waitForSelector('.xnotice[data-ns="sending"]', { timeout: 4000 });
    assert.equal(await bv.locator('.xnotice[data-ns="failed"]').count(), 0, 'ORDER: the second tab no longer shows the failed notice with Retry');
    await post('/patch', { id: 'exec_o1', state: 'stopped', stopped_by_user: true, stop_notice: 'sent' });
    await bv.waitForSelector('.xnotice[data-ns="sent"]', { timeout: 4000 });
    await view.waitForSelector('.xnotice[data-ns="sent"]', { timeout: 4000 });
    await B.ctx.close();
    await closeAny();
  });

  await sect('ORDER-LATE-LIST', async () => {
    // a list answered late, taken before the retry, does not take it back in the initiating tab
    await post('/set', { notice: 'sent' });
    await setJobs([stopped('exec_o1', 'deploy --prod', 'failed')]);
    await view.waitForSelector('.xchip[data-xid="exec_o1"]');
    await openChip(view, 'exec_o1');
    await view.waitForSelector('.xnotice[data-ns="failed"]');
    await view.evaluate(() => {
      window.__ns = [];
      new MutationObserver(() => { const c = document.querySelector('.xpeek .xnotice'), v = c ? c.getAttribute('data-ns') : '-'; if (window.__ns[window.__ns.length - 1] !== v) window.__ns.push(v); }).observe(document.querySelector('.xpeek'), { subtree: true, childList: true, attributes: true });
    });
    await post('/set', { notice: 'pending', listDelayMs: 1500 });
    await post('/bump');
    await sleep(150);
    await view.click('.xnotice [data-xact="retry"]');
    await sleep(3800);
    const ns = await view.evaluate(() => window.__ns);
    const firstSending = ns.indexOf('sending');
    assert.ok(firstSending >= 0, 'ORDER: the retry shows sending: ' + JSON.stringify(ns));
    assert.ok(!ns.slice(firstSending).includes('failed'), 'ORDER: an older list answered late does not bring the failed notice back: ' + JSON.stringify(ns));
    await post('/set', { notice: 'sent' });
    await post('/patch', { id: 'exec_o1', state: 'stopped', stopped_by_user: true, stop_notice: 'sent' });
    await closeAny();
  });

  // a stop's answer (pending) arriving after a newer list (sent): no stale toast, no stale state
  await sect('ORDER-STOP', async () => {
    await setJobs([J('exec_o3', 'worker three', { started_at: Date.now() - 60000 }), J('exec_o4', 'worker four', { started_at: Date.now() - 50000 })]);
    await view.waitForSelector('.xchip[data-xid="exec_o4"]', { state: 'attached' });
    await post('/set', { notice: 'pending', stopAfterMs: 1800 });
    await openChip(view, 'exec_o3');
    await view.evaluate(() => {
      window.__tt = [];
      const t = document.querySelector('.xtoast');
      new MutationObserver(() => { if (!t.hidden && t.textContent) window.__tt.push(t.textContent); }).observe(t, { subtree: true, childList: true, characterData: true, attributes: true });
    });
    await view.click('.xpeek [data-xact="stop"]');
    await view.waitForSelector('.xconf');
    await view.click('.xconf [data-xact="confirm"]');
    await view.click('.xp-tab[data-xtab="exec_o4"]');
    await sleep(300);
    await post('/patch', { id: 'exec_o3', state: 'stopped', stopped_by_user: true, stop_notice: 'sent' });
    await view.waitForSelector('.xtoast:not([hidden])', { timeout: 6000 });
    await sleep(1500);
    const tt = await view.evaluate(() => window.__tt);
    assert.ok(tt.length > 0 && tt.every(x => !T.notifying.test(x)), 'ORDER: the late stop answer shows no stale "notifying" toast: ' + JSON.stringify(tt));
    assert.match(await view.locator('.xtoast').textContent(), T.notified, 'ORDER: the toast follows the newer list');
    await view.click('.xp-tab[data-xtab="exec_o3"]');
    assert.equal(await view.locator('.xnotice[data-ns="sent"]').count(), 1, 'ORDER: the notice card follows the newer list too');
    await post('/set', { notice: 'sent' });
    await view.evaluate(() => { document.querySelector('.xtoast').hidden = true; const p = document.querySelector('.xpeek'); if (p && !p.hidden) document.querySelector('.xpeek [data-xact="close"]').click(); });
  });

  // GAP (round 2, I2): a stale list size must not hide a real eviction when the first read is re-seated at the tail.
  await sect('GAP', async () => {
    await setJobs([J('exec_gap', 'tail -f big.log', { evicted: 262144 })]);
    await view.waitForSelector('.xchip[data-xid="exec_gap"]');
    await post('/out', { ID: 'exec_gap', Stream: 'stdout', Text: ('g'.repeat(1023) + '\n').repeat(511) + 'GAP-TAIL-LINE\n' });
    await post('/reset');
    await openChip(view, 'exec_gap');
    await view.waitForFunction(() => document.querySelector('.xout').textContent.includes('GAP-TAIL-LINE'), null, { timeout: 8000 });
    st = await get('/stats');
    assert.equal(st.fromLog.exec_gap[0], 0, 'GAP: the first read used the stale list size: ' + JSON.stringify(st.fromLog));
    assert.ok(st.fromLog.exec_gap[1] > 400000, 'GAP: it was then re-seated at the tail: ' + JSON.stringify(st.fromLog));
    assert.equal(await view.evaluate(re => [...document.querySelectorAll('.xout .l.sys')].filter(n => new RegExp(re).test(n.textContent)).length, T.gap.source), 1, 'GAP: the eviction notice is shown once');
    await closePeek(page, view);
  });

  // POLL (round 2): a wake coalesced into a 404 is not lost; a read that never answers releases the tab.
  await sect('POLL-404', async () => {
    await setJobs([J('exec_gone', 'sleep 999')]);
    await view.waitForSelector('.xchip[data-xid="exec_gone"]');
    await post('/set', { notice: 'sent', outDelayMs: 700, outGone: ['exec_gone'] });
    await post('/reset');
    await openChip(view, 'exec_gone');
    await waitStat(s => (s.counts['output:exec_gone'] || 0) >= 1, 3000, 'the first read');
    await view.evaluate(() => document.dispatchEvent(new Event('visibilitychange')));
    await sleep(2400);
    st = await get('/stats');
    assert.ok((st.counts['output:exec_gone'] || 0) >= 2, 'POLL: the wake that came during the 404 read led to another read: ' + JSON.stringify(st.counts));
    await post('/set', { notice: 'sent' });
    await closePeek(page, view);
  });
  await sect('POLL-HANG', async () => {
    // a read that never answers: aborted after the request timeout, then reading goes on
    await setJobs([J('exec_gone', 'sleep 999')]);
    await view.waitForSelector('.xchip[data-xid="exec_gone"]');
    await post('/set', { notice: 'sent', outHang: true });
    await post('/reset');
    await openChip(view, 'exec_gone');
    await waitStat(s => (s.inflight.exec_gone || 0) === 1, 3000, 'the hanging read');
    await sleep(11800);
    st = await get('/stats');
    assert.ok((st.counts['output:exec_gone'] || 0) >= 2, 'POLL: a read that never answered was aborted and reading resumed: ' + JSON.stringify(st.counts));
    await post('/out', { ID: 'exec_gone', Stream: 'stdout', Text: 'HANG-LINE\n' });
    await post('/set', { notice: 'sent' });
    await view.waitForFunction(() => document.querySelector('.xout').textContent.includes('HANG-LINE'), null, { timeout: 9000 });
    await closePeek(page, view);
  });

  // Minor: the 10 s pin ends: an ok job that just ended is ahead of a running one, then behind it.
  await sect('PIN', async () => {
    await setJobs([J('exec_run', 'watch it', { started_at: Date.now() - 300000 }), J('exec_ok', 'make', { state: 'exited', exit_code: 0, started_at: Date.now() - 20000, ended_at: Date.now() - 8600 })]);
    await view.waitForSelector('.xchip[data-xid="exec_ok"]');
    const order = () => view.evaluate(() => [...document.querySelectorAll('.xgroup .xchip[data-xid]')].filter(n => n.getClientRects().length).map(n => n.getAttribute('data-xid')));
    assert.deepEqual(await order(), ['exec_ok', 'exec_run'], 'before 10 s a just-ended ok job is pinned ahead of the running one');
    await sleep(1900);
    assert.deepEqual(await order(), ['exec_run', 'exec_ok'], 'after 10 s it ranks behind the running job (still shown until 12 s)');
    await sleep(2300);
    assert.deepEqual(await order(), ['exec_run'], 'after 12 s it fades');
  });

  await ctx.close();
  return errors;
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    if (shots) fs.mkdirSync(shots, { recursive: true });
    const only = process.env.WS_EXEC_HUD_COMBOS ? process.env.WS_EXEC_HUD_COMBOS.split(',') : null;
    for (const [lang, scheme] of [['en', 'light'], ['ko', 'dark']].filter(c => !only || only.includes(c[0] + '-' + c[1]))) {
      const errors = await run(browser, { lang, scheme });
      assert.deepEqual(errors, [], 'no page error');
    }
    console.log('MEASUREMENTS\n' + report.join('\n'));
    if (partial) { console.error('PARTIAL RUN (WS_EXEC_HUD_SECTS / WS_EXEC_HUD_COMBOS set): not a verification'); process.exit(3); }
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exit(1); });
