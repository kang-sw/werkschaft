// Run by TestExecHUDBrowser against a live daemon whose exec job service is a
// fake driven through a control server: the exec job HUD in ko and en, light
// and dark. argv: <daemon base> <control base> [<captures dir>] [<mockup index.html>]
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, ctl, shots, mockup] = process.argv.slice(2);

const TEXT = {
  en: { confTitle: 'Stop this job?', sent: 'Agent notified', none: 'No agent notice', stopped: 'Stopped', ended: 'ended', retry: 'Retry', stoppedUser: 'Stopped by you' },
  ko: { confTitle: '이 작업을 중지할까요?', sent: '에이전트에 알림 전달됨', none: '에이전트 알림 없음', stopped: '중지됨', ended: '종료', retry: '다시 시도', stoppedUser: '사용자가 중지함' },
};
const post = (p, b) => fetch(ctl + p, { method: 'POST', body: JSON.stringify(b || {}) }).then(r => r.json());
const get = p => fetch(ctl + p).then(r => r.json());
const J = (id, command, o = {}) => Object.assign({ id, tool: 'exec.shell', command, cwd: 'web/', state: 'running', origin: 'agent', started_at: Date.now() - 90000, ended_at: 0 }, o);
const sleep = ms => new Promise(r => setTimeout(r, ms));
const report = [];
const note = (k, v) => { report.push(k + ' = ' + v); console.log('MEASURE ' + k + ' = ' + v); };

async function openPage(browser, { lang, scheme, width = 1280, reduced = false, filterExecEvent = false, list404 = false }) {
  const ctx = await browser.newContext({ viewport: { width, height: 900 }, locale: lang === 'ko' ? 'ko-KR' : 'en-US', colorScheme: scheme, reducedMotion: reduced ? 'reduce' : 'no-preference', permissions: ['clipboard-read', 'clipboard-write'] });
  await ctx.addCookies([{ name: 'ws_dashboard_lang', value: lang, url: base }]);
  // The HUD's one-second clock callback (it re-fits the strip), kept so a test can run one fitting pass in one task.
  await ctx.addInitScript(() => {
    const si = window.setInterval;
    window.__xhTicks = [];
    window.setInterval = function (fn, ms, ...rest) { if (ms === 1000 && String(fn).includes('xhRender')) window.__xhTicks.push(fn); return si.call(this, fn, ms, ...rest); };
  });
  const page = await ctx.newPage();
  const errors = [], requests = [];
  page.on('pageerror', e => { errors.push('pageerror: ' + e.message); console.error('PAGE ERROR:', e.message); });
  page.on('console', m => { if (m.type() === 'error') errors.push('console: ' + m.text() + ' @ ' + m.location().url); });
  page.on('request', r => { if (r.url().includes('/exec/')) requests.push(r.method() + ' ' + new URL(r.url()).pathname + new URL(r.url()).search); });
  if (filterExecEvent) {
    // A daemon that does not know the HUD never sends its event.
    await page.routeWebSocket(/\/events/, ws => {
      const server = ws.connectToServer();
      server.onMessage(m => { if (!String(m).includes('"event":"execjobs"')) ws.send(m); });
      ws.onMessage(m => server.send(m));
    });
  }
  // The list read answers 404, as it does to a LAN viewer (the route is localhost-only).
  if (list404) await page.route(/\/exec\/jobs$/, r => r.fulfill({ status: 404, contentType: 'text/plain', body: 'not found' }));
  await page.goto(base + '/s/livesession');
  let view = null;
  for (let i = 0; i < 100 && !view; i++) {
    view = page.frames().find(x => x !== page.mainFrame() && x.url().includes('/s/livesession'));
    if (!view) await sleep(200);
  }
  assert.ok(view, 'no session frame');
  try { await view.waitForSelector('#composer[data-cexec="1"]', { state: 'attached', timeout: 15000 }); } catch (e) {
    console.log('PAGE URL', page.url(), 'FRAME', view.url(), 'BODY', (await view.evaluate(() => document.body.innerText.slice(0, 300)).catch(x => 'ERR ' + x.message)), 'ERRORS', JSON.stringify(errors.slice(-5)));
    throw e;
  }
  return { ctx, page, view, errors, requests };
}
const chips = view => view.evaluate(() => [...document.querySelectorAll('.xgroup .xchip:not(.xagg), .xgroup .xmore')].filter(n => n.getClientRects().length).map(n => ({ id: n.getAttribute('data-xid'), kind: n.getAttribute('data-xkind'), text: n.textContent, st: n.getAttribute('data-st') })));
const aggVisible = view => view.evaluate(() => { const a = document.querySelector('.xgroup .xagg'); return !!a && a.getClientRects().length > 0; });
const hudShown = view => view.evaluate(() => { const g = document.querySelector('.xgroup'); return !!g && !g.hidden && g.getClientRects().length > 0; });
async function setJobs(view, jobs) {
  await post('/jobs', jobs);
  await post('/bump');
}
async function fakeBadges(view, o = {}) {
  await view.evaluate(o => {
    const sb = document.getElementById('sbadge'), ib = document.getElementById('ibadge'), bg = document.getElementById('bgbadge');
    sb.hidden = !o.sub; if (o.sub) { sb.classList.remove('idle'); sb.querySelector('.slabel').textContent = o.sub; }
    ib.hidden = !o.ib;
    bg.hidden = !o.bg; if (o.bg) bg.querySelector('.blabel').textContent = o.bg;
  }, o);
  await sleep(120);
}
async function metrics(view) {
  return view.evaluate(() => {
    const m = id => { const e = document.getElementById(id), b = e.getBoundingClientRect(); return { w: Math.round(b.width * 10) / 10, h: Math.round(b.height * 10) / 10, l: b.left, r: b.right, vis: !e.hidden, clipped: e.scrollWidth > e.clientWidth + 0.5 }; };
    const sl = document.querySelector('#sbadge .slabel');
    const g = document.querySelector('.xgroup'), gr = g && !g.hidden && g.getClientRects().length ? g.getBoundingClientRect() : null;
    return { ib: m('ibadge'), sb: m('sbadge'), bg: m('bgbadge'), slabelClipped: sl.scrollWidth > sl.clientWidth + 0.5, group: gr && { l: gr.left, r: gr.right, w: gr.width }, composerW: document.getElementById('composer').clientWidth };
  });
}
async function snap(page, view, name, extra = 0) {
  if (!shots) return;
  const box = await view.evaluate(() => { const c = document.getElementById('composer').getBoundingClientRect(); return { top: c.top, bottom: c.bottom, right: c.right }; });
  const fb = await (await view.frameElement()).boundingBox();
  const y = Math.max(0, fb.y + box.top - 480 - extra), bottom = Math.min(fb.y + fb.height, fb.y + box.bottom + 14);
  await page.screenshot({ path: path.join(shots, name + '.png'), clip: { x: fb.x, y, width: Math.min(fb.width, box.right + 24), height: bottom - y } });
}
const editor = view => view.locator('.cfield [contenteditable]').first();
async function sendLine(view, text) {
  await editor(view).click();
  await view.page().keyboard.press('ControlOrMeta+A');
  await view.page().keyboard.press('Delete');
  await view.page().keyboard.insertText(text);
  await view.click('#csend');
}

// ---------------------------------------------------------------------------
async function run(browser, { lang, scheme, full }) {
  const T = TEXT[lang], tag = lang + '-' + scheme;
  const now = Date.now();
  await post('/set', { notice: 'sent', stopMs: 0, bind: true });
  await setJobs(null, []);
  const { ctx, page, view, errors, requests } = await openPage(browser, { lang, scheme });
  console.log('--- ' + tag);

  // 1. idle: no jobs, nothing drawn, no list asked of a quiet page beyond the connect read.
  await sleep(600);
  assert.equal(await hudShown(view), false, 'idle: no HUD');
  await fakeBadges(view, { ib: true, sub: '3', bg: '2' });
  await snap(page, view, tag + '-01-idle');

  // 2. one running job.
  await setJobs(view, [J('exec_a', 'pnpm vitest --watch', { started_at: Date.now() - 134000 })]);
  await view.waitForSelector('.xgroup:not([hidden]) .xchip[data-xid="exec_a"]');
  let c = await chips(view);
  assert.equal(c.length, 1);
  assert.match(c[0].text, /pnpm vitest/);
  assert.match(c[0].text, /2:\d\d/, 'elapsed shown');
  assert.equal(await view.evaluate(() => Math.round(document.querySelector('.xchip[data-xid="exec_a"]').getBoundingClientRect().height)), 24);
  await snap(page, view, tag + '-02-running');

  // 3. many jobs fold; the strip's priority layout across widths.
  const many = [
    J('exec_a', 'pnpm vitest --watch', { started_at: Date.now() - 612000 }), J('exec_b', 'go test ./internal/execjob/... -run TestHosted -count=1 -v', { started_at: Date.now() - 91000 }),
    J('exec_c', 'python3 -m http.server 8080 --directory ./ai-docs', { started_at: Date.now() - 305000 }), J('exec_d', 'cd engine && make -j8 all 2>&1 | tee /tmp/b.log', { started_at: Date.now() - 47000 }),
    J('exec_e', 'git fetch --all --prune', { origin: 'user', started_at: Date.now() - 22000 }), J('exec_f', 'tail -f ~/.local/state/ws/daemon.log', { started_at: Date.now() - 1840000 }),
    J('exec_g', 'for f in $(git ls-files); do wc -l "$f"; done', { started_at: Date.now() - 9000, cwd: 'docs/' }),
  ];
  await setJobs(view, many);
  await view.waitForFunction(() => document.querySelectorAll('.xgroup .xchip[data-xid]').length >= 7);
  await fakeBadges(view, { ib: true, sub: '3', bg: '2' });
  c = await chips(view);
  assert.ok(c.some(x => x.kind === 'more' && /^\+\d+/.test(x.text)), 'a +N chip folds the rest: ' + JSON.stringify(c));
  await snap(page, view, tag + '-03-many');

  // One fitting pass is one read phase: the layouts it forces are counted around a single run of the
  // strip's clock callback (ai-docs/manuals/performance.md, Frontend Minimal Updates 4 and 6). It may
  // cost one layout for the measurement and one for the flush that closes the probe, not one per
  // measured element (the pass used to flush after each of +N, the aggregate and its compact fallback).
  const cdp = await ctx.newCDPSession(page);
  await cdp.send('Performance.enable');
  const layoutCount = async () => (await cdp.send('Performance.getMetrics')).metrics.find(m => m.name === 'LayoutCount').value;
  const fitLayouts = async () => {
    await view.evaluate(() => document.body.offsetHeight);
    const before = await layoutCount();
    const ran = await view.evaluate(() => { const tick = window.__xhTicks[window.__xhTicks.length - 1]; if (!tick) return false; tick(); document.body.offsetHeight; return true; });
    assert.ok(ran, 'the HUD clock is running while jobs run');
    return (await layoutCount()) - before;
  };
  const foldedLayouts = await fitLayouts();
  note('forced layouts of one fitting pass (chips + fold)', foldedLayouts);
  assert.ok(foldedLayouts <= 2, 'a fitting pass measures in one read phase: ' + foldedLayouts + ' layouts');

  // widths: the interrupt and subagent badges never change, whatever the chips do.
  const widths = [1280, 900, 700, 560, 480, 420, 360, 320];
  const shownByWidth = [];
  for (const cfg of [{ ib: true, sub: '3' }, { ib: true, sub: '12(+34)', bg: '2' }]) {
    for (const w of widths) {
      await page.setViewportSize({ width: w, height: 900 });
      await sleep(250);
      await setJobs(view, []);
      await view.waitForFunction(() => !document.querySelector('.xgroup') || document.querySelector('.xgroup').hidden);
      await fakeBadges(view, cfg);
      const without = await metrics(view);
      await setJobs(view, many);
      await view.waitForFunction(() => document.querySelectorAll('.xgroup .xchip[data-xid]').length >= 7);
      await fakeBadges(view, cfg);
      await sleep(150);
      const withJobs = await metrics(view), cs = await chips(view), agg = await aggVisible(view);
      const label = 'width ' + w + ' ' + JSON.stringify(cfg);
      for (const k of ['ib', 'sb', 'bg']) {
        if (k === 'bg' && !cfg.bg) continue;
        assert.equal(withJobs[k].w, without[k].w, label + ': ' + k + ' width with jobs ' + withJobs[k].w + ' vs without ' + without[k].w);
        assert.equal(withJobs[k].h, 24, label + ': ' + k + ' stays one line of 24px');
        assert.equal(withJobs[k].l, without[k].l, label + ': ' + k + ' does not move');
        assert.equal(withJobs[k].clipped, false, label + ': ' + k + ' is not clipped');
      }
      assert.equal(withJobs.slabelClipped, false, label + ': the subagent label is not ellipsized');
      if (withJobs.group) {
        assert.ok(withJobs.group.l >= withJobs.ib.r - 0.5, label + ': chips stay right of Interrupt (' + withJobs.group.l + ' < ' + withJobs.ib.r + ')');
        assert.ok(withJobs.group.r <= Math.min(withJobs.bg.vis ? withJobs.bg.l : 1e9, withJobs.sb.l) + 0.5, label + ': chips stay left of the badges');
      }
      if (!cfg.bg) shownByWidth.push({ viewport: w, composer: withJobs.composerW, chips: cs.filter(x => !x.kind).length, more: cs.some(x => x.kind === 'more'), agg });
    }
  }
  note('fold by width (typical badges: Interrupt + "3 running")', JSON.stringify(shownByWidth));
  shownByWidth.sort((x, y) => y.composer - x.composer);
  for (let i = 1; i < shownByWidth.length; i++) assert.ok(shownByWidth[i].chips <= shownByWidth[i - 1].chips, 'chips never grow as the strip narrows: ' + JSON.stringify(shownByWidth));
  const narrow = shownByWidth[shownByWidth.length - 1];
  assert.equal(narrow.chips, 0, 'no chip at 320px');
  assert.ok(narrow.agg, 'one aggregate chip at 320px: ' + JSON.stringify(narrow));
  assert.ok(shownByWidth.some(x => x.chips > 0 && x.more), 'a +N fold appears at an intermediate width');
  await page.setViewportSize({ width: 320, height: 900 });
  await sleep(300);
  await setJobs(view, many);
  await fakeBadges(view, { ib: true, sub: '3' });
  await snap(page, view, tag + '-04-aggregate-320');
  const aggLayouts = await fitLayouts();
  note('forced layouts of one fitting pass (aggregate at 320px)', aggLayouts);
  assert.ok(aggLayouts <= 2, 'the aggregate fit, compact fallback included, adds no layout read: ' + aggLayouts + ' layouts');
  await page.setViewportSize({ width: 1000, height: 900 });
  await sleep(300);

  // 4. ended states, pinning and the fade timers.
  const t0 = Date.now();
  const ended = [
    J('exec_ok', 'make -j8', { state: 'exited', exit_code: 0, started_at: t0 - 88000, ended_at: t0 - 11200 }),
    J('exec_exit', 'go test ./...', { state: 'exited', exit_code: 2, started_at: t0 - 130000, ended_at: t0 - 44000 }),
    J('exec_stopped', 'python3 -m http.server', { state: 'stopped', stopped_by_user: true, stop_notice: 'sent', started_at: t0 - 410000, ended_at: t0 - 63000 }),
    J('exec_lost', 'tail -f daemon.log', { state: 'lost', started_at: t0 - 3200000, ended_at: t0 - 1800000 }),
    J('exec_nostart', 'git fetch', { state: 'failed', origin: 'user', started_at: t0 - 5000, ended_at: t0 - 5000 }),
    J('exec_run', 'pnpm vitest --watch', { started_at: t0 - 201000 }),
    J('exec_just', 'cargo build', { state: 'exited', exit_code: 0, started_at: t0 - 20000, ended_at: t0 - 2000 }),
  ];
  await setJobs(view, ended);
  await view.waitForSelector('.xgroup:not([hidden]) .xchip');
  await fakeBadges(view, { ib: false, sub: '', bg: '' });
  await sleep(200);
  let c2 = await chips(view);
  let ids = c2.filter(x => x.id).map(x => x.id);
  // rank 0 (failures, lost, never started, just ended) then running; the stopped job is past its fade window.
  assert.ok(ids.indexOf('exec_exit') >= 0 && ids.indexOf('exec_lost') >= 0 && ids.indexOf('exec_nostart') >= 0, 'failures shown: ' + ids);
  assert.ok(!ids.includes('exec_stopped'), 'a stopped job past 12s is hidden: ' + ids);
  assert.ok(ids.indexOf('exec_run') > ids.indexOf('exec_exit'), 'pinned failures come before the running job: ' + ids);
  assert.ok(ids.indexOf('exec_just') >= 0 && ids.indexOf('exec_just') < ids.indexOf('exec_run'), 'an ok job that just ended is pinned ahead of the running job: ' + ids);
  const okVisible = ids.includes('exec_ok');
  assert.ok(okVisible, 'an ok job inside its 12 s window is still shown at load: ' + ids);
  await sleep(1100);
  ids = (await chips(view)).filter(x => x.id).map(x => x.id);
  assert.ok(!ids.includes('exec_ok'), 'an ok job fades 12 s after it ended: ' + ids);
  const states = await view.evaluate(() => Object.fromEntries([...document.querySelectorAll('.xchip[data-xid]')].map(n => [n.getAttribute('data-xid'), n.textContent])));
  assert.match(states.exec_exit, /2/);
  assert.match(states.exec_lost, /lost|유실/);
  assert.match(states.exec_nostart, /no start|시작 안 됨/);
  await snap(page, view, tag + '-05-ended');
  // dismiss a failure: it stays dismissed across a reload; the rest folds into "ended N".
  await view.hover('.xslot[data-xid="exec_exit"]');
  await view.click('.xslot[data-xid="exec_exit"] .xstop');
  assert.equal(await view.locator('.xchip[data-xid="exec_exit"]').count(), 0, 'dismissed');
  // reload: a job that ended before the page loaded is past its windows.
  await post('/set', { notice: 'sent' });
  await setJobs(view, [J('exec_ok', 'make -j8', { state: 'exited', exit_code: 0, ended_at: Date.now() - 100000 }), J('exec_bad', 'go vet ./...', { state: 'exited', exit_code: 1, ended_at: Date.now() - 100000 }), J('exec_stopped2', 'sleep 1000', { state: 'stopped', ended_at: Date.now() - 100000 }), J('exec_stopfail', 'node server.js', { state: 'stopped', stopped_by_user: true, stop_notice: 'failed', ended_at: Date.now() - 90000 })]);
  await page.reload();
  await view.waitForSelector('#composer[data-cexec="1"]', { state: 'attached' }).catch(() => {});
  const view2 = page.frames().find(x => x !== page.mainFrame() && x.url().includes('/s/livesession'));
  await view2.waitForSelector('.xchip[data-xid="exec_bad"]', { timeout: 15000 });
  ids = (await chips(view2)).filter(x => x.id).map(x => x.id);
  assert.deepEqual(ids, ['exec_stopfail', 'exec_bad'], 'after reload only the failure and the stop whose notice failed show: ' + ids);
  await view2.evaluate(() => { document.querySelector('.xslot[data-xid="exec_bad"] .xstop').click(); document.querySelector('.xslot[data-xid="exec_stopfail"] .xstop').click(); });
  await sleep(200);
  assert.equal(await hudShown(view2), true, 'the quiet "ended" chip remains');
  assert.match(await view2.evaluate(() => document.querySelector('.xgroup .xagg.quiet').textContent), new RegExp('2'), 'ended N counts the faded jobs');
  await snap(page, view2, tag + '-06-quiet-ended');
  await page.reload();
  const view3 = page.frames().find(x => x !== page.mainFrame() && x.url().includes('/s/livesession'));
  await view3.waitForSelector('.xgroup .xagg.quiet', { timeout: 15000 });
  assert.equal(await view3.locator('.xchip[data-xid="exec_bad"]').count(), 0, 'a dismissed failure stays dismissed across a reload');
  const v = view3;

  // 5. tooltip, escaping, the 7-line clamp.
  const evil = '<img src=x onerror="window.__pwn=1">&amp; line\n' + Array.from({ length: 12 }, (_, i) => 'echo line ' + (i + 2)).join('\n');
  await setJobs(v, [J('exec_evil', evil, { cwd: 'web/<b>', started_at: Date.now() - 5000 }), J('exec_u', 'npm run dev -- --port 5174', { origin: 'user', started_at: Date.now() - 9000 })]);
  await v.waitForSelector('.xchip[data-xid="exec_evil"]');
  await post('/out', { ID: 'exec_evil', Stream: 'stdout', Text: '<script>window.__pwn=2</script><b>bold</b>\n' });
  await v.hover('.xchip[data-xid="exec_evil"]');
  await v.waitForSelector('.xtip:not([hidden])');
  const tip = await v.evaluate(() => { const t = document.querySelector('.xtip'), cmd = t.querySelector('.xtt-cmd'); return { text: t.textContent, cmd: cmd.textContent, lines: Math.round(cmd.clientHeight / parseFloat(getComputedStyle(cmd).lineHeight)), clamp: getComputedStyle(cmd).webkitLineClamp, imgs: t.querySelectorAll('img,b').length, scroll: cmd.scrollHeight > cmd.clientHeight }; });
  assert.equal(tip.cmd, evil, 'the tooltip shows the command as text');
  assert.equal(tip.imgs, 0, 'no markup from the command');
  assert.equal(tip.clamp, '7');
  assert.ok(tip.scroll, 'the 13-line command is clamped');
  assert.ok(tip.text.includes('web/<b>'), 'cwd shown as text');
  await snap(page, v, tag + '-07-tooltip');
  await page.mouse.move(5, 5);

  // 6. peek: tabs, merged output with stderr tint, copy, escaping, polling only while open.
  await post('/out', { ID: 'exec_evil', Stream: 'stderr', Text: 'warn: slow thing\n' });
  await post('/reset');
  await v.click('.xchip[data-xid="exec_evil"]');
  await v.waitForSelector('.xpeek:not([hidden]) .xout .l');
  await sleep(500);
  const peek = await v.evaluate(() => { const o = document.querySelector('.xout'); return { text: o.textContent, err: [...o.querySelectorAll('.l.e')].map(n => n.textContent), html: o.querySelectorAll('b,script,img').length, tabs: document.querySelectorAll('.xp-tab').length, label: document.querySelector('.xpeek').getAttribute('aria-label') }; });
  assert.ok(peek.text.includes('<script>window.__pwn=2</script><b>bold</b>'), 'output is text');
  assert.equal(peek.html, 0, 'no markup from output');
  assert.deepEqual(peek.err, ['warn: slow thing'], 'stderr is attributed');
  assert.equal(peek.tabs, 2);
  assert.equal(await v.evaluate(() => window.__pwn), undefined, 'nothing from a command or output ran');
  await snap(page, v, tag + '-08-peek');
  await post('/out', { ID: 'exec_evil', Stream: 'stdout', Text: 'more output\n' });
  await v.waitForFunction(() => document.querySelector('.xout').textContent.includes('more output'), null, { timeout: 8000 });
  let st = await get('/stats');
  assert.ok(st.counts['output:exec_evil'] >= 2, 'the open peek polls: ' + JSON.stringify(st.counts));
  assert.ok(st.from.exec_evil > 0, 'a poll reads from the last offset, not from zero');
  // copy
  await v.click('.xpeek [data-xact="copy"]');
  assert.equal(await v.evaluate(() => navigator.clipboard.readText()), evil, 'copy puts the command on the clipboard');
  // close, then no more polling
  await page.keyboard.press('Escape');
  await v.waitForSelector('.xpeek', { state: 'hidden' });
  await sleep(300);
  await post('/reset');
  await sleep(3800);
  st = await get('/stats');
  assert.equal(st.counts.output || 0, 0, 'a closed peek does not poll: ' + JSON.stringify(st.counts));
  const listBefore = st.counts.list || 0;
  assert.equal(listBefore, 0, 'an idle page does not poll the list: ' + JSON.stringify(st.counts));

  // 7. follow the tail without jumping when scrolled up; "↓ N new".
  await post('/jobs', [J('exec_log', 'tail -f app.log', { started_at: Date.now() - 20000 }), J('exec_u', 'npm run dev', { origin: 'user' })]);
  for (let i = 0; i < 80; i++) await post('/out', { ID: 'exec_log', Stream: i % 17 === 0 ? 'stderr' : 'stdout', Text: 'line ' + i + ' of the log output that is long enough to look real\n' });
  await post('/bump');
  await v.waitForSelector('.xchip[data-xid="exec_log"]');
  await v.click('.xchip[data-xid="exec_log"]');
  await v.waitForFunction(() => document.querySelector('.xout').textContent.includes('line 79'));
  assert.ok(await v.evaluate(() => { const o = document.querySelector('.xout'); return o.scrollHeight - o.scrollTop - o.clientHeight < 12; }), 'follows the tail');
  await v.evaluate(() => { document.querySelector('.xout').scrollTop = 0; });
  await sleep(150);
  for (let i = 80; i < 83; i++) await post('/out', { ID: 'exec_log', Stream: 'stdout', Text: 'late line ' + i + '\n' });
  await v.waitForSelector('.xnew:not([hidden])', { timeout: 9000 });
  const nw = await v.evaluate(() => ({ text: document.querySelector('.xnew').textContent, top: document.querySelector('.xout').scrollTop }));
  assert.match(nw.text, /3/, 'N new counts the new lines: ' + nw.text);
  assert.ok(nw.top < 2, 'the view did not jump: ' + nw.top);
  await snap(page, v, tag + '-09-peek-new');
  await v.click('.xnew');
  assert.ok(await v.evaluate(() => { const o = document.querySelector('.xout'); return o.scrollHeight - o.scrollTop - o.clientHeight < 12; }), 'the jump goes to the tail');

  // 8. stop: confirm wording, focus, Esc, timeout, notice, toast.
  await post('/jobs', [J('exec_s1', 'pnpm vitest --watch'), J('exec_s2', 'python3 -m http.server'), J('exec_s3', 'make watch'), J('exec_u1', 'npm run dev', { origin: 'user' })]);
  await post('/bump');
  await v.waitForSelector('.xchip[data-xid="exec_s1"]');
  await post('/set', { stopMs: 700, notice: 'sent' });
  await v.click('.xchip[data-xid="exec_s1"]');
  await v.waitForSelector('.xpeek [data-xact="stop"]');
  await v.click('.xpeek [data-xact="stop"]');
  await v.waitForSelector('.xconf');
  assert.equal(await v.evaluate(() => document.activeElement.getAttribute('data-xact')), 'cancel', 'focus on Cancel');
  const conf = await v.evaluate(() => document.querySelector('.xconf').textContent);
  assert.ok(conf.includes(T.confTitle), conf);
  assert.ok(/TERM/.test(conf) && /KILL/.test(conf), 'names TERM then KILL: ' + conf);
  assert.ok(!/SIGKILL|no cleanup|정리 과정 없음/.test(conf), 'does not promise an immediate kill with no cleanup');
  assert.equal(await v.locator('.xconf [data-xact="confirm"]').textContent(), lang === 'ko' ? '작업 중지' : 'Stop job', 'wording is "Stop job", distinct from Interrupt');
  await snap(page, v, tag + '-10-confirm');
  await page.keyboard.press('Escape');
  await v.waitForSelector('.xconf', { state: 'detached' });
  assert.equal(await v.evaluate(() => document.activeElement.getAttribute('data-xact')), 'stop', 'Esc cancels the confirm and returns focus to Stop');
  assert.equal(await v.locator('.xpeek').isVisible(), true, 'the first Esc keeps the panel');
  assert.equal((await get('/stats')).counts['stop'] || 0, 0, 'no stop sent yet');
  if (full) {
    await v.click('.xpeek [data-xact="stop"]');
    await v.waitForSelector('.xconf');
    await sleep(8700);
    assert.equal(await v.locator('.xconf').count(), 0, 'the confirm reverts after 8 s');
    assert.equal((await get('/stats')).counts['stop'] || 0, 0, 'a reverted confirm stops nothing');
  }
  await v.click('.xpeek [data-xact="stop"]');
  await v.waitForSelector('.xconf');
  await v.click('.xconf [data-xact="confirm"]');
  await v.waitForSelector('.xchip[data-xid="exec_s1"][data-st="stopping"]', { timeout: 3000 });
  assert.equal(await v.locator('.xpeek [data-xact="stop"]').isDisabled(), true, 'Stop is disabled while stopping');
  await v.waitForSelector('.xnotice[data-ns="sent"]', { timeout: 6000 });
  assert.ok((await v.locator('.xnotice').textContent()).includes(T.sent));
  assert.equal(await v.locator('.xchip[data-xid="exec_s1"]').getAttribute('data-st'), 'stopped');
  assert.equal((await get('/stats')).counts['stop:exec_s1'], 1);
  await snap(page, v, tag + '-11-notice');
  // tooltip of the stopped job names who stopped it and that the agent was told.
  await page.keyboard.press('Escape');
  await v.hover('.xchip[data-xid="exec_s1"]');
  await v.waitForSelector('.xtip:not([hidden])');
  assert.ok((await v.locator('.xtip .xtt-note').textContent()).includes(T.stoppedUser));
  await page.mouse.move(5, 5);
  // a stop that finishes while the peek shows another job toasts.
  await v.click('.xchip[data-xid="exec_s2"]');
  await v.waitForSelector('.xpeek [data-xact="stop"]');
  await v.click('.xpeek [data-xact="stop"]');
  await v.waitForSelector('.xconf');
  await v.click('.xconf [data-xact="confirm"]');
  await v.click('.xp-tab[data-xtab="exec_s3"]');
  await v.waitForSelector('.xtoast:not([hidden])', { timeout: 6000 });
  assert.match(await v.locator('.xtoast').textContent(), lang === 'ko' ? /중지됨/ : /Stopped/);
  await snap(page, v, tag + '-12-toast');
  // the user's own `!` job: no agent notice.
  await v.click('.xp-tab[data-xtab="exec_u1"]');
  await v.click('.xpeek [data-xact="stop"]');
  await v.waitForSelector('.xconf');
  assert.ok((await v.locator('.xconf').textContent()).includes(lang === 'ko' ? '깨우지 않습니다' : 'not woken'), 'the ! confirm says the agent is not notified');
  await v.click('.xconf [data-xact="confirm"]');
  await v.waitForSelector('.xnotice[data-ns="none"]', { timeout: 6000 });
  assert.ok((await v.locator('.xnotice').textContent()).includes(T.none));
  // a failed notice offers Retry; Retry lands it.
  await post('/set', { stopMs: 0, notice: 'failed-once' });
  await v.click('.xp-tab[data-xtab="exec_s3"]');
  await v.click('.xpeek [data-xact="stop"]');
  await v.waitForSelector('.xconf');
  await v.click('.xconf [data-xact="confirm"]');
  await v.waitForSelector('.xnotice[data-ns="failed"] [data-xact="retry"]', { timeout: 6000 });
  await snap(page, v, tag + '-13-notice-failed');
  await v.click('.xnotice [data-xact="retry"]');
  await v.waitForSelector('.xnotice[data-ns="sent"]', { timeout: 6000 });
  assert.equal((await get('/stats')).counts['stop:exec_s3'], 2, 'retry asks the daemon once more');

  // 9. keyboard: one tab stop, arrows, Enter, Esc, focus returns.
  await post('/set', { stopMs: 0, notice: 'sent' });
  await post('/jobs', [J('exec_k1', 'npm test'), J('exec_k2', 'cargo watch'), J('exec_k3', 'tsc --watch')]);
  await post('/bump');
  await page.keyboard.press('Escape');
  await v.waitForSelector('.xchip[data-xid="exec_k3"]');
  const stops = await v.evaluate(() => [...document.querySelectorAll('.xgroup .xchip, .xgroup .xmore')].filter(n => n.getClientRects().length && n.tabIndex === 0).length);
  assert.equal(stops, 1, 'the toolbar has one tab stop');
  await v.evaluate(() => document.querySelector('.xgroup .xchip[tabindex="0"]').focus());
  const first = await v.evaluate(() => document.activeElement.getAttribute('data-xid'));
  await page.keyboard.press('ArrowRight');
  const second = await v.evaluate(() => document.activeElement.getAttribute('data-xid'));
  assert.notEqual(first, second, 'ArrowRight moves along the chips');
  await page.keyboard.press('ArrowLeft');
  assert.equal(await v.evaluate(() => document.activeElement.getAttribute('data-xid')), first);
  await page.keyboard.press('End');
  assert.equal(await v.evaluate(() => document.activeElement.getAttribute('data-xid')), 'exec_k3');
  await page.keyboard.press('Home');
  assert.equal(await v.evaluate(() => document.activeElement.getAttribute('data-xid')), first);
  await v.evaluate(() => { window.__escSeen = 0; document.addEventListener('keydown', e => { if (e.key === 'Escape') window.__escSeen++; }); });
  await page.keyboard.press('Enter');
  await v.waitForSelector('.xpeek:not([hidden])');
  assert.equal(await v.evaluate(() => document.activeElement.classList.contains('xchip')), true, 'focus stays on the chip when the panel opens by keyboard');
  await page.keyboard.press('Escape');
  await v.waitForSelector('.xpeek', { state: 'hidden' });
  assert.equal(await v.evaluate(() => window.__escSeen), 0, 'the Escape that closes the panel is consumed (the rail, the drawer and the shell never see it)');
  assert.equal(await v.evaluate(() => document.activeElement.classList.contains('xchip')), true, 'Esc from the chip returns focus to a chip');
  await page.keyboard.press('Enter');
  await v.waitForSelector('.xpeek:not([hidden])');
  await v.evaluate(() => document.querySelector('.xp-tab[aria-selected="true"]').focus());
  const tab0 = await v.evaluate(() => document.activeElement.getAttribute('data-xtab'));
  await page.keyboard.press('ArrowRight');
  assert.notEqual(await v.evaluate(() => document.activeElement.getAttribute('data-xtab')), tab0, 'arrows move across the tabs');
  assert.equal(await v.evaluate(() => document.querySelector('.xpeek [aria-selected="true"]') === document.activeElement), true);
  await page.keyboard.press('Escape');
  await v.waitForSelector('.xpeek', { state: 'hidden' });
  assert.equal(await v.evaluate(() => document.activeElement.classList.contains('xchip')), true, 'focus returns to a chip');

  // 10. jump to the `!` command's card.
  await post('/jobs', [J('exec_hi', 'echo hi', { origin: 'user', state: 'exited', exit_code: 0, ended_at: Date.now() - 1000, started_at: Date.now() - 2000 })]);
  await sendLine(v, '!echo hi');
  await v.waitForSelector('.cexec[data-phase="done"]');
  await post('/bump');
  await v.waitForSelector('.xchip[data-xid="exec_hi"]');
  await v.click('.xchip[data-xid="exec_hi"]');
  await v.waitForSelector('.xpeek [data-xact="jump"]');
  assert.equal(await v.locator('.xpeek [data-xact="jump"]').isDisabled(), false, 'the card is there');
  await v.click('.xpeek [data-xact="jump"]');
  await v.waitForSelector('.cexec.xflash');
  await v.waitForSelector('.xpeek', { state: 'hidden' });

  // 11. reduced motion and the motion it removes.
  const motion = await v.evaluate(() => { const r = document.querySelector('.xring'); return r ? getComputedStyle(r, '::before').animationName : 'none'; });
  await post('/jobs', [J('exec_m', 'sleep 100')]);
  await post('/bump');
  await v.waitForSelector('.xchip[data-xid="exec_m"]');
  const motionRunning = await v.evaluate(() => getComputedStyle(document.querySelector('.xchip[data-xid="exec_m"] .xring'), '::before').animationName);
  assert.equal(motionRunning, 'xh-spin', 'a running ring spins');
  note('ring animation (no reduced-motion preference)', motionRunning + ' (was ' + motion + ')');

  await ctx.close();
  return { errors, requests };
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const execErrors = [];
  try {
    if (shots) fs.mkdirSync(shots, { recursive: true });
    const only = process.env.WS_EXEC_HUD_COMBOS ? process.env.WS_EXEC_HUD_COMBOS.split(',') : null;
    const combos = [['en', 'light', true], ['ko', 'dark', false], ['en', 'dark', false], ['ko', 'light', false]].filter(c => !only || only.includes(c[0] + '-' + c[1]));
    for (const [lang, scheme, full] of combos) {
      const r = await run(browser, { lang, scheme, full });
      r.errors.filter(e => e.includes('/exec/') && !e.includes('404')).forEach(e => execErrors.push(e));
    }
    assert.deepEqual(execErrors, [], 'no console error from the HUD routes');

    // reduced motion
    {
      await post('/set', { stopMs: 0, notice: 'sent' });
      await post('/jobs', [J('exec_m', 'sleep 100')]);
      const { ctx, view } = await openPage(browser, { lang: 'en', scheme: 'light', reduced: true });
      await view.waitForSelector('.xchip[data-xid="exec_m"]');
      assert.equal(await view.evaluate(() => getComputedStyle(document.querySelector('.xchip[data-xid="exec_m"] .xring'), '::before').animationName), 'none', 'reduced motion: the ring does not spin');
      assert.equal(await view.evaluate(() => getComputedStyle(document.querySelector('.xchip[data-xid="exec_m"]')).animationName), 'none', 'reduced motion: chips do not rise');
      await view.click('.xchip[data-xid="exec_m"]');
      assert.equal(await view.evaluate(() => getComputedStyle(document.querySelector('.xpeek')).animationName), 'none', 'reduced motion: the panel does not animate');
      await ctx.close();
    }

    // new page against a daemon without the HUD: its event never comes; nothing is asked, nothing shown, no error.
    {
      await post('/jobs', [J('exec_old', 'sleep 100')]);
      const { ctx, view, errors, requests } = await openPage(browser, { lang: 'en', scheme: 'light', filterExecEvent: true });
      await sleep(1500);
      assert.equal(await hudShown(view), false, 'old daemon: no HUD');
      assert.deepEqual(requests, [], 'old daemon: the page asks for nothing: ' + requests);
      assert.deepEqual(errors.filter(e => e.includes('/exec/') || e.startsWith('pageerror')), [], 'old daemon: no console error');
      await ctx.close();
    }
    // the list read answers 404 (what a LAN viewer gets from the localhost-only route): the page asked
    // exactly once, mounted no HUD, and never asks again this load.
    {
      await post('/jobs', [J('exec_lan', 'sleep 100')]);
      const { ctx, view, errors, requests } = await openPage(browser, { lang: 'en', scheme: 'light', list404: true });
      await sleep(1500);
      assert.equal(await hudShown(view), false, '404 on the list: no HUD');
      assert.deepEqual(requests, ['GET /s/livesession/exec/jobs'], '404 on the list: asked once: ' + requests);
      assert.deepEqual(errors.filter(e => e.startsWith('pageerror')), []);
      await post('/bump');
      await post('/bump');
      await sleep(600);
      assert.equal(requests.length, 1, '404 on the list: later events are not read: ' + requests);
      await ctx.close();
    }
    // the service is unbound (a lead that closed): the daemon still announces its revision on connect, the
    // route answers 404 for the missing service, and the page asks exactly once, mounts nothing, asks no more.
    {
      await post('/set', { unbind: true });
      const { ctx, view, errors, requests } = await openPage(browser, { lang: 'en', scheme: 'light' });
      await sleep(1500);
      assert.equal(await hudShown(view), false, 'unbound service: no HUD');
      assert.deepEqual(requests, ['GET /s/livesession/exec/jobs'], 'unbound service: asked once: ' + requests);
      assert.deepEqual(errors.filter(e => e.startsWith('pageerror')), []);
      await post('/bump');
      await post('/bump');
      await sleep(600);
      assert.equal(requests.length, 1, 'unbound service: later events are not read: ' + requests);
      await ctx.close();
      await post('/set', { bind: true });
    }

    // performance: 200 jobs.
    {
      const jobs = [];
      for (let i = 0; i < 200; i++) jobs.push(J('exec_p' + i, 'cmd' + i + ' --flag value', i % 7 === 0 ? { state: 'exited', exit_code: i % 2, ended_at: Date.now() - 200000 } : { started_at: Date.now() - i * 1000 }));
      jobs[1].command = 'tail -f big.log';
      await post('/jobs', jobs);
      for (let i = 0; i < 16; i++) await post('/out', { ID: 'exec_p1', Stream: 'stdout', Text: ('0123456789abcdef'.repeat(63) + '\n').repeat(64) });
      const { ctx, page, view } = await openPage(browser, { lang: 'en', scheme: 'light' });
      await view.waitForSelector('.xgroup:not([hidden]) .xchip, .xgroup:not([hidden]) .xmore, .xgroup:not([hidden]) .xagg');
      for (let i = 0; i < 12; i++) { await post('/bump'); await sleep(120); }
      const r = await view.evaluate(() => {
        const ms = performance.getEntriesByName('ws.xhud.render').map(e => e.duration).sort((a, b) => a - b);
        const res = performance.getEntriesByType('resource').filter(e => e.name.includes('/exec/jobs')).map(e => e.duration).sort((a, b) => a - b);
        const d = performance.getEntriesByName('ws.xhud.render').map(e => e.detail).filter(Boolean).pop() || {};
        return { renders: ms.length, p50: ms[Math.floor(ms.length / 2)], max: ms[ms.length - 1], list: res, detail: d, chips: document.querySelectorAll('.xgroup .xslot').length, nodes: document.querySelectorAll('.xgroup *').length };
      });
      note('200 jobs: chip render ms (n, p50, max)', r.renders + ', ' + r.p50.toFixed(2) + ', ' + r.max.toFixed(2));
      note('200 jobs: list fetch ms (p50, max)', r.list[Math.floor(r.list.length / 2)].toFixed(1) + ', ' + r.list[r.list.length - 1].toFixed(1));
      note('200 jobs: chips built / dom nodes in the strip', r.chips + ' / ' + r.nodes);
      assert.ok(r.nodes < 400, 'the strip stays small: ' + r.nodes);
      assert.ok(r.max < 100, 'chip render stays cheap with 200 jobs: ' + r.max);
      assert.ok(r.chips <= 12, 'only the chips that can fit are built: ' + r.chips);
      // an open peek on the 1 MiB job: tail read, then steady polls.
      await view.evaluate(() => performance.clearResourceTimings());
      await view.locator('.xgroup .xmore:visible, .xgroup .xagg:visible').first().click();
      await view.waitForSelector('.xpeek:not([hidden])');
      await view.click('.xp-tab[data-xtab="exec_p1"]');
      await view.waitForSelector('.xpeek:not([hidden]) .xout .l');
      await sleep(2500);
      const p = await view.evaluate(() => {
        const ms = performance.getEntriesByName('ws.xhud.poll').map(e => e.duration).sort((a, b) => a - b);
        const res = performance.getEntriesByType('resource').filter(e => e.name.includes('/exec_p1/output?')).map(e => ({ d: e.duration, size: e.encodedBodySize, decoded: e.decodedBodySize }));
        return { apply: ms, res, lines: document.querySelector('.xout').childNodes.length };
      });
      const pr = await view.evaluate(() => performance.getEntriesByName('ws.xhud.peek-render').map(e => ({ d: e.duration, tabs: e.detail && e.detail.tabs })));
      note('peek render ms with 200 tabs (n, max)', pr.length + ', ' + Math.max(...pr.map(x => x.d)).toFixed(2) + ' (' + pr[pr.length - 1].tabs + ' tabs)');
      note('peek on a 1 MiB job: polls in 2.5 s', p.res.length);
      note('peek poll on the 1 MiB job: apply ms (max) / fetch ms (max) / body bytes (max)', (p.apply.length ? p.apply[p.apply.length - 1].toFixed(2) : 'n/a') + ' / ' + Math.max(...p.res.map(x => x.d)).toFixed(1) + ' / ' + Math.max(...p.res.map(x => x.decoded)));
      assert.ok(p.res.length > 0 && Math.max(...p.res.map(x => x.decoded)) <= 40000, 'a poll is bounded: ' + JSON.stringify(p.res));
      assert.ok(p.res.length <= 8, 'idle backoff keeps polls few: ' + p.res.length);
      await page.keyboard.press('Escape');
      await ctx.close();
    }

    // the approved mockup for side-by-side review.
    if (shots && mockup) {
      for (const [lang, scheme] of [['en', 'light'], ['ko', 'dark'], ['en', 'dark'], ['ko', 'light']]) {
        const ctx = await browser.newContext({ viewport: { width: 1300, height: 900 } });
        const page = await ctx.newPage();
        await page.goto('file://' + mockup + '?gallery');
        await page.click('#seg-lang [data-v="' + lang + '"]');
        await page.click('#seg-theme [data-v="' + scheme + '"]');
        await sleep(400);
        await page.screenshot({ path: path.join(shots, 'mockup-gallery-' + lang + '-' + scheme + '.png'), fullPage: true });
        await ctx.close();
      }
    }
    console.log('MEASUREMENTS\n' + report.join('\n'));
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exit(1); });
