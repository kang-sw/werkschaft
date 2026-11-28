// Run by TestGitPageBrowser: the gallery's Git page (#gitdock) on a real
// framed page over a real repository, with a 900px #bgal (wide) and a 380px
// #bgal (narrow), in both themes. The page is one panel at every width: the
// strip (icon-only fetch, pull, push; the review pill; the Worktrees button)
// over the commit graph. The stored gallery column stops at 720px, so the
// 900px gallery is the narrow view's overlay over the board column
// (viewport below 1600px), sized by resizing the viewport until the overlay
// measures 900px; the 380px gallery is the 2560x1440 column (the
// always-open column needs the spare width a 3-column 1920 desktop lacks).
// It checks Ctrl+G's idempotent show, the branch line, the single panel's
// width and subject column, the dirty tree's UNSTAGED row above HEAD and
// its commit window (Refresh in place), the strip actions' names, icons,
// and busy state, the Worktrees popover (no pool read until it opens, the
// cache-only first read, the badge, the manual full scan, every close path,
// the commit window's Escape first, no clipping, no overflow), the review
// pill's jump to a frontier past the graph's first page, a commit window,
// the clean tree's missing row, and the no-repository empty state. With a
// shots directory it keeps each state for the side-by-side check against
// the approved mockup.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
// bases and projects: one daemon (and its repository) per git session (dark
// 900, dark 380, light 900, light 380), so each opens on a cold worktree
// size cache and a tree of its own to change.
const [baseList, projectList, plainBase, frontier, shots] = process.argv.slice(2);
const bases = baseList.split(','), projects = projectList.split(',');

function shot(fr, name) {
  if (!shots) return Promise.resolve();
  fs.mkdirSync(shots, { recursive: true });
  return fr.locator('#bgal').screenshot({ path: path.join(shots, name + '.png') });
}
async function frameOf(page) {
  const h = await page.locator('iframe.sframe.on').elementHandle();
  return h.contentFrame();
}
const raf2 = fr => fr.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
async function until(page, what, fn, ms = 10000) {
  const end = Date.now() + ms;
  let last;
  while (Date.now() < end) {
    last = await fn();
    if (last) return last;
    await page.waitForTimeout(100);
  }
  throw new Error('timed out waiting for ' + what + ' (last ' + JSON.stringify(last) + ')');
}
const shown = fr => fr.evaluate(() => {
  const C = window.WsCards, root = document.getElementById('gitpage'), dock = document.getElementById('gitdock');
  return !!C && C.activeSys() === 'git' && !!root && !!dock && root.parentNode === dock && !root.hidden && root.getClientRects().length > 0;
});
async function ctrlG(page, fr) {
  await fr.evaluate(() => { if (document.activeElement && document.activeElement.blur) document.activeElement.blur(); document.body.focus(); });
  await page.keyboard.press('Control+g');
}
const gwinOpen = fr => fr.evaluate(() => { const b = document.querySelector('.gwin-back'); return !!b && !b.hidden; });
const popOpen = fr => fr.evaluate(() => { const p = document.getElementById('gpwtpop'); return !p.hidden && p.getClientRects().length > 0; });
const active = fr => fr.evaluate(() => { const a = document.activeElement; return a ? a.id || a.className : ''; });

// overflow measures the page: the dock, the page, the strip, every shown
// card and the document scroll nothing sideways, and no shown strip or
// header part (the open popover included) leaves the dock.
function overflow(fr) {
  return fr.evaluate(() => {
    const dock = document.getElementById('gitdock'), root = document.getElementById('gitpage'), dr = dock.getBoundingClientRect();
    const sx = e => e ? e.scrollWidth - e.clientWidth : 0;
    const cards = [...root.querySelectorAll('.gp-card')].filter(e => e.getClientRects().length).map(sx).reduce((a, b) => Math.max(a, b), 0);
    const out = [...root.querySelectorAll('.gp-strip > *, .gp-strip-main *, .gp-wtbtn, .gp-wtpop, .gp-wtpop .gp-hd > *, .gp-hd > *')].filter(e => e.getClientRects().length)
      .map(e => ({ e, r: e.getBoundingClientRect() })).filter(o => o.r.width && (o.r.right > dr.right + 0.5 || o.r.left < dr.left - 0.5))
      .map(o => (o.e.id || o.e.className.baseVal || o.e.className) + ' ' + Math.round(o.r.left) + '..' + Math.round(o.r.right) + '/' + Math.round(dr.right));
    const d = document.documentElement;
    return { dock: sx(dock), page: sx(root), strip: sx(root.querySelector('.gp-strip')), cards, doc: d.scrollWidth - d.clientWidth, out };
  });
}
async function noOverflow(fr, label) {
  await raf2(fr);
  assert.deepEqual(await overflow(fr), { dock: 0, page: 0, strip: 0, cards: 0, doc: 0, out: [] }, `${label}: no horizontal overflow`);
}

// galWidth measures the shown gallery (#bgal) in the frame.
const galWidth = fr => fr.evaluate(() => { const g = window.WsCards && window.WsCards.gallery && window.WsCards.gallery(); return g ? Math.round(g.getBoundingClientRect().width) : 0; });

// fitOverlay resizes the viewport until the narrow view's gallery overlay
// measures galW (it tracks the board column, which tracks the viewport).
async function fitOverlay(page, fr, galW) {
  // A secant step: the column may take only part of each viewport pixel.
  const settle = async () => { await page.waitForTimeout(200); await raf2(fr); return galWidth(fr); };
  let vp = page.viewportSize(), w = await settle(), prev = null;
  const seen = [];
  for (let i = 0; i < 8 && Math.abs(w - galW) > 2; i++) {
    seen.push(vp.width + ':' + w);
    const slope = prev && prev.w !== w ? (w - prev.w) / (vp.width - prev.vw) : 1;
    prev = { vw: vp.width, w };
    vp = { width: Math.round(vp.width + (galW - w) / (slope > 0.05 ? slope : 1)), height: vp.height };
    await page.setViewportSize(vp);
    w = await settle();
  }
  seen.push(vp.width + ':' + w);
  assert.ok(Math.abs(w - galW) <= 2, `the gallery overlay is ${galW}px (viewport:width ${seen.join(' ')})`);
  return w;
}

async function newSession(browser, theme, galW, url) {
  const overlay = galW > 720;
  const context = await browser.newContext({ viewport: overlay ? { width: 1300, height: 1000 } : { width: 2560, height: 1440 }, colorScheme: theme });
  const origin = new URL(url).origin;
  await context.addCookies([{ name: 'ws_dashboard_theme', value: theme, url: origin }]);
  await context.addInitScript(w => {
    if (self === top) {
      const workers = navigator.serviceWorker;
      if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
    }
    try {
      if (sessionStorage.getItem('gpfix')) return;
      sessionStorage.setItem('gpfix', '1');
      localStorage.setItem('ws.dashboard.bgal.w', String(w));
    } catch (_) {}
  }, galW);
  const reqs = [];
  const errors = [];
  // The tracker and (after the page hands the gallery to the Ticket Map) the
  // ticket graph have no store in this fixture; their 404s are not the page's.
  const tracker = u => { const p = new URL(u).pathname; return p === '/tracker/stores' || /\/tickets\/graph$/.test(p); };
  // A full worktree scan answers slowly, so its busy state can be seen; a
  // fetch (the fixture has no remote) answers late with nothing, so the
  // action's busy state can be seen.
  await context.route('**/*', async route => {
    const u = new URL(route.request().url());
    if (u.origin !== origin) return route.abort();
    if (/\/worktrees$/.test(u.pathname) && !u.searchParams.has('no_size')) await new Promise(r => setTimeout(r, 1200));
    if (/\/git\/fetch$/.test(u.pathname)) { await new Promise(r => setTimeout(r, 900)); return route.fulfill({ status: 204, body: '' }); }
    return route.continue();
  });
  const page = await context.newPage();
  page.on('pageerror', e => errors.push('pageerror: ' + e.message));
  page.on('console', m => { if (m.type() === 'error' && !(m.location().url && tracker(m.location().url))) errors.push('console: ' + m.text()); });
  page.on('response', r => { if (r.status() >= 400 && !(r.status() === 404 && tracker(r.url()))) errors.push(`response ${r.status()}: ${r.url()}`); });
  page.on('request', r => { const u = new URL(r.url()); if (/\/(git\/log|git\/status|worktrees|review-ledger)$/.test(u.pathname)) reqs.push(u.pathname.replace(/^\/s\/[^/]+\//, '') + u.search); });
  await page.goto(url + '/s/livesession');
  const fr = await frameOf(page);
  await fr.locator('#gitdock').waitFor({ state: 'attached' });
  return { context, page, fr, reqs, errors };
}

// wtRow reads the graph's working-tree row: its slot, the slot of HEAD's
// row (the next row), its counts, the hollow node and its connector.
const wtRow = fr => fr.evaluate(() => {
  const rows = [...document.querySelectorAll('#glrows .glr')], i = rows.findIndex(r => r.hasAttribute('data-worktree'));
  const svg = document.querySelector('#glrows .glsvg'), node = svg && svg.querySelector('circle.wt'), head = svg && svg.querySelector('circle.head');
  // Without the row, every dashed path is a last row's lane running on past
  // the graph's foot; one ending anywhere else is a stray connector.
  const foot = svg ? +svg.getAttribute('height') - 17 : 0;
  if (i < 0) return { i, node: !!node, conns: svg ? [...svg.querySelectorAll('path.cont')].filter(p => !/[ V]/.test(p.getAttribute('d').slice(-String(foot).length - 1, -String(foot).length)) || !p.getAttribute('d').endsWith(String(foot))).length : 0 };
  const row = rows[i], next = rows[i + 1], cy = c => c ? (+c.getAttribute('cy') - 14) / 28 : -1;
  const wx = node.getAttribute('cx'), wy = node.getAttribute('cy');
  const conn = [...svg.querySelectorAll('path.cont')].find(p => p.getAttribute('d').startsWith('M' + wx + ' ' + wy));
  return { i, slot: cy(node), headSlot: cy(head), nextIsHead: !!next && next.querySelector('.glh') && head && Math.abs(next.offsetTop - row.offsetTop - 28) < 2,
    label: row.querySelector('.glwt-k').textContent, total: row.querySelector('.glwt-n').textContent, aria: row.getAttribute('aria-label'),
    dashed: getComputedStyle(node).strokeDasharray !== 'none', conn: !!conn, connDashed: !!conn && getComputedStyle(conn).strokeDasharray !== 'none',
    sel: row.classList.contains('sel'), role: row.getAttribute('role'), tab: row.tabIndex };
});

async function gitSession(browser, theme, galW, base, project) {
  const tag = `${theme}/gal${galW}`, narrow = galW < 720, view = narrow ? 'narrow' : 'wide';
  const { context, page, fr, reqs, errors } = await newSession(browser, theme, galW, base);
  await page.waitForTimeout(400);
  assert.equal(reqs.filter(r => !r.startsWith('git/status')).length, 0, `${tag}: nothing reads before the page shows ${JSON.stringify(reqs)}`);

  // 1. Ctrl+G shows the page and focuses its host.
  await ctrlG(page, fr);
  await until(page, `${tag} the git page`, () => shown(fr));
  if (galW > 720) await fitOverlay(page, fr, galW);
  else assert.ok(Math.abs(await galWidth(fr) - galW) <= 2, `${tag}: the gallery column is ${galW}px (got ${await galWidth(fr)}px)`);
  assert.equal(await fr.evaluate(() => document.getElementById('gitdock').contains(document.activeElement)), true, `${tag}: Ctrl+G focuses the page`);

  // 2. The branch line and the pill.
  const strip = await until(page, `${tag} branch line and pill`, () => fr.evaluate(() => {
    const br = document.querySelector('#gpref .gp-br'), pill = document.getElementById('gppill');
    return br && pill.dataset.state !== 'loading' ? { br: br.textContent, pill: pill.dataset.state, disabled: pill.disabled, tx: pill.textContent } : null;
  }));
  assert.match(strip.br, /main/, `${tag}: the branch line ${JSON.stringify(strip)}`);
  assert.ok(['ok', 'warn'].includes(strip.pill) && !strip.disabled, `${tag}: the review pill ${JSON.stringify(strip)}`);

  // 3. One panel at every width: no tabs, no change list; the graph fills
  // the page's width and leaves the subject at least 20ch.
  await until(page, `${tag} graph rows`, () => fr.evaluate(() => document.querySelectorAll('#glrows .glr[data-i]').length >= 200));
  const lay = await fr.evaluate(() => {
    const root = document.getElementById('gitpage'), glog = document.getElementById('glog'), cs = getComputedStyle(root);
    const inner = root.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight);
    const sub = document.querySelector('#glrows .glr[data-i] .gls'), probe = document.createElement('span');
    probe.style.cssText = 'position:absolute;visibility:hidden;width:20ch';
    sub.appendChild(probe);
    const ch20 = probe.getBoundingClientRect().width;
    probe.remove();
    return { gone: ['#gpanel', '.gp-tabs', '#gfiles', '.gp-regions', '#wtcard'].filter(s => document.querySelector(s)), tab: root.hasAttribute('data-tab'),
      fill: Math.abs(glog.getBoundingClientRect().width - inner) <= 2, glog: Math.round(glog.getBoundingClientRect().width), inner: Math.round(inner),
      sub: Math.round(sub.getBoundingClientRect().width), ch20: Math.round(ch20) };
  });
  assert.deepEqual([lay.gone, lay.tab, lay.fill], [[], false, true], `${tag}: one full-width panel ${JSON.stringify(lay)}`);
  assert.ok(lay.sub >= lay.ch20, `${tag}: the subject column measures at least 20ch ${JSON.stringify(lay)}`);
  await noOverflow(fr, `${tag} graph`);

  // 4. The dirty fixture's UNSTAGED row: right above HEAD, dashed into it,
  // with the counts (README.md staged, log.txt unstaged, and two untracked:
  // git lists the new src/ and ai-docs/ directories as one entry each).
  const wt = await until(page, `${tag} working-tree row`, () => wtRow(fr).then(w => w.i >= 0 && w));
  assert.deepEqual([wt.label, wt.total, wt.aria, wt.role, wt.tab], ['UNSTAGED', '4', '4 uncommitted files: 1 staged, 1 unstaged, 2 untracked', 'button', 0], `${tag}: the row ${JSON.stringify(wt)}`);
  assert.deepEqual([wt.headSlot, wt.nextIsHead, wt.dashed, wt.conn, wt.connDashed], [wt.slot + 1, true, true, true, true], `${tag}: the row above HEAD ${JSON.stringify(wt)}`);
  await shot(fr, `gp-${theme}-${view}`);

  // 5. The strip actions: icon-only, named, the icon shown at every width,
  // and the busy state on action.
  const acts = await fr.evaluate(() => [...document.querySelectorAll('#gitpage .gact')].map(b => {
    const r = b.getBoundingClientRect(), i = b.querySelector('svg').getBoundingClientRect();
    return { name: b.getAttribute('aria-label'), text: b.textContent.trim(), w: Math.round(r.width), h: Math.round(r.height), icon: i.width >= 12 && i.height >= 12 && getComputedStyle(b.querySelector('svg')).display !== 'none' };
  }));
  assert.deepEqual(acts, ['Fetch', 'Pull', 'Push'].map(name => ({ name, text: '', w: 24, h: 24, icon: true })), `${tag}: icon-only actions ${JSON.stringify(acts)}`);
  await fr.locator('#gitpage .gact[data-git="fetch"]').click();
  const busy = await until(page, `${tag} fetch busy`, () => fr.evaluate(() => {
    const b = document.querySelector('#gitpage .gact[data-git="fetch"]');
    return b.classList.contains('busy') && { aria: b.getAttribute('aria-label'), ab: b.getAttribute('aria-busy'), title: b.title, busy: b.getAttribute('data-busy'), icon: b.querySelector('svg').getBoundingClientRect().width >= 12 };
  }), 3000);
  assert.deepEqual([busy.aria, busy.title, busy.ab, busy.icon], [busy.busy, busy.busy, 'true', true], `${tag}: the busy action ${JSON.stringify(busy)}`);
  await until(page, `${tag} fetch done`, () => fr.evaluate(() => {
    const b = document.querySelector('#gitpage .gact[data-git="fetch"]');
    return !b.classList.contains('busy') && !b.hasAttribute('aria-busy') && b.getAttribute('aria-label') === 'Fetch';
  }));
  await until(page, `${tag} graph after fetch`, () => fr.evaluate(() => document.querySelectorAll('#glrows .glr[data-i]').length >= 200 && !!document.querySelector('#glrows .glr[data-worktree]')));

  // 6. The row opens the commit window on the working tree: click and
  // Enter, the three groups, the Refresh button, the row's .sel.
  await fr.locator('#glrows .glr[data-worktree]').click();
  await until(page, `${tag} worktree window`, () => gwinOpen(fr));
  const win = await fr.evaluate(() => {
    const w = document.querySelector('.gwin'), r = w.querySelector('.gw-refresh');
    return { groups: [...w.querySelectorAll('.ggrp > span:first-child')].map(s => s.textContent), refresh: !!r && !r.hidden && r.getClientRects().length > 0,
      copy: !!w.querySelector('.idcopy') && !w.querySelector('.idcopy').hidden, files: w.querySelectorAll('.gwf').length, sel: document.querySelector('#glrows .glr[data-worktree]').classList.contains('sel') };
  });
  assert.deepEqual(win, { groups: ['Staged', 'Unstaged', 'Untracked'], refresh: true, copy: false, files: 4, sel: true }, `${tag}: the worktree window ${JSON.stringify(win)}`);
  await shot(fr, `gp-${theme}-${view}-worktree`);
  // Refresh: a new file appears in place and focus stays on the button.
  // (At the root: git lists a new directory's files as the directory.)
  fs.writeFileSync(path.join(project, 'added.go'), 'package main\n');
  await fr.locator('.gwin .gw-refresh').click();
  const ref = await until(page, `${tag} refreshed`, () => fr.evaluate(() => {
    const w = document.querySelector('.gwin'), r = w.querySelector('.gw-refresh');
    const has = [...w.querySelectorAll('.gwf')].some(f => /added\.go/.test(f.title));
    return has && !r.classList.contains('busy') && !r.hasAttribute('aria-busy') && { disabled: r.disabled, files: w.querySelectorAll('.gwf').length, focus: document.activeElement === r, total: document.querySelector('#glrows .glr[data-worktree] .glwt-n').textContent };
  })).catch(async e => {
    const st = await fr.evaluate(() => { const w = document.querySelector('.gwin'), r = w.querySelector('.gw-refresh');
      return { titles: [...w.querySelectorAll('.gwf')].map(f => f.title), disabled: r.disabled, cls: r.className, gmsg: document.getElementById('gmsg').textContent, notes: [...w.querySelectorAll('.gw-note')].map(n => n.textContent), active: document.activeElement && document.activeElement.className }; });
    throw new Error(e.message + ' state ' + JSON.stringify(st) + ' reqs ' + JSON.stringify(reqs.slice(-6)));
  });
  assert.deepEqual(ref, { disabled: false, files: 5, focus: true, total: '5' }, `${tag}: Refresh in place ${JSON.stringify(ref)}`);
  await page.keyboard.press('Escape');
  await until(page, `${tag} worktree window closed`, () => gwinOpen(fr).then(o => !o));
  assert.equal(await fr.evaluate(() => document.querySelector('#glrows .glr[data-worktree]').classList.contains('sel')), false, `${tag}: no .sel after the window closes`);
  await fr.evaluate(() => document.querySelector('#glrows .glr[data-worktree]').focus());
  await page.keyboard.press('Enter');
  await until(page, `${tag} worktree window by Enter`, () => gwinOpen(fr));
  assert.equal(await fr.evaluate(() => document.querySelector('.gwin .gw-refresh').hidden), false, `${tag}: Enter opens the worktree window`);
  await page.keyboard.press('Escape');
  await until(page, `${tag} worktree window closed again`, () => gwinOpen(fr).then(o => !o));

  // 7. The Worktrees popover. The page showing read no pool; the first
  // opening reads it cache-only (no sizes, no disk walk) and fills the badge.
  assert.equal(reqs.filter(r => r.startsWith('worktrees')).length, 0, `${tag}: no pool read before the popover opens ${JSON.stringify(reqs)}`);
  const cleanBefore = await fr.evaluate(() => { const b = document.getElementById('wtclean'); return { disabled: b.disabled, title: b.title }; });
  await fr.locator('#gpwtbtn').click();
  await until(page, `${tag} popover`, () => popOpen(fr));
  assert.equal(await fr.evaluate(() => document.getElementById('gpwtbtn').getAttribute('aria-expanded')), 'true');
  await until(page, `${tag} light list`, () => fr.evaluate(() => document.querySelectorAll('#wtlist .wtrow').length === 5));
  assert.equal(await fr.evaluate(() => document.querySelectorAll('#wtlist .wtsize.gp-wt-none').length), 5, `${tag}: no sizes in the light list`);
  assert.ok(reqs.some(r => r === 'worktrees?no_size=1') && !reqs.some(r => r === 'worktrees' || r === 'worktrees?wait=1'), `${tag}: only the cache-only read ${JSON.stringify(reqs)}`);
  const pop = await fr.evaluate(() => {
    const p = document.getElementById('gpwtpop'), r = p.getBoundingClientRect(), g = document.getElementById('bgal').getBoundingClientRect(), c = document.getElementById('wtcount');
    const hit = (x, y) => p.contains(document.elementFromPoint(x, y));
    return { mode: document.getElementById('gpwtmode').dataset.mode, badge: !c.hidden && c.getClientRects().length > 0 ? c.textContent : '',
      inside: r.left >= g.left - 0.5 && r.right <= g.right + 0.5 && r.bottom <= g.bottom + 0.5, hit: hit(r.left + 8, r.top + 8) && hit(r.right - 8, r.bottom - 8) && hit(r.left + r.width / 2, r.top + r.height / 2) };
  });
  assert.deepEqual(pop, { mode: 'light', badge: '2/5', inside: true, hit: true }, `${tag}: the popover, never clipped ${JSON.stringify(pop)}`);
  await noOverflow(fr, `${tag} popover`);
  await shot(fr, `gp-${theme}-${view}-worktrees`);
  // The manual scan: busy, then the sizes and the scanned mode.
  await fr.locator('#gpscan').click();
  await until(page, `${tag} busy scan`, () => fr.evaluate(() => document.getElementById('gpscan').dataset.scan === 'busy'));
  assert.equal(await fr.evaluate(() => document.querySelectorAll('#wtlist .wtsize.gp-wt-scan').length), 5, `${tag}: the unmeasured rows show the scan at once`);
  await shot(fr, `gp-${theme}-${view}-busy`);
  await until(page, `${tag} scanned`, () => fr.evaluate(() => document.getElementById('gpscan').dataset.scan === 'done' && document.getElementById('gpwtmode').dataset.mode === 'scanned'));
  assert.equal(await fr.evaluate(() => document.querySelectorAll('#wtlist .wtsize.gp-wt-none, #wtlist .wtsize.gp-wt-scan').length), 0, `${tag}: every size measured`);
  assert.ok(reqs.some(r => r === 'worktrees' || r === 'worktrees?wait=1'), `${tag}: the scan read the sizes ${JSON.stringify(reqs)}`);
  assert.deepEqual(await fr.evaluate(() => { const b = document.getElementById('wtclean'); return { disabled: b.disabled, title: b.title }; }), cleanBefore, `${tag}: the cleanup button keeps its state`);
  await noOverflow(fr, `${tag} popover scanned`);
  // Escape closes it and gives focus back to the button.
  await fr.locator('#gpscan').focus();
  await page.keyboard.press('Escape');
  await until(page, `${tag} popover closed by Escape`, () => popOpen(fr).then(o => !o));
  assert.deepEqual([await active(fr), await fr.evaluate(() => document.getElementById('gpwtbtn').getAttribute('aria-expanded'))], ['gpwtbtn', 'false'], `${tag}: focus back on the button`);
  // A press outside closes it; the button again closes it.
  await fr.locator('#gpwtbtn').click();
  await until(page, `${tag} popover again`, () => popOpen(fr));
  // The popover may cover the graph header at narrow widths; press the strip's ref corner instead.
  await fr.locator('#gpref').click({ position: { x: 2, y: 2 } });
  await until(page, `${tag} popover closed outside`, () => popOpen(fr).then(o => !o));
  await fr.locator('#gpwtbtn').click();
  await until(page, `${tag} popover once more`, () => popOpen(fr));
  await fr.locator('#gpwtbtn').click();
  await until(page, `${tag} popover closed by its button`, () => popOpen(fr).then(o => !o));
  // With the commit window open over it, Escape closes the window first.
  await fr.locator('#gpwtbtn').click();
  await until(page, `${tag} popover under the window`, () => popOpen(fr));
  await fr.evaluate(() => document.querySelector('#glrows .glr[data-worktree]').focus());
  await page.keyboard.press('Enter');
  await until(page, `${tag} window over the popover`, () => gwinOpen(fr));
  await page.keyboard.press('Escape');
  await until(page, `${tag} window closed first`, () => gwinOpen(fr).then(o => !o));
  assert.equal(await popOpen(fr), true, `${tag}: Escape closed the window, not the popover`);
  await page.keyboard.press('Escape');
  await until(page, `${tag} popover closed after the window`, () => popOpen(fr).then(o => !o));

  // 8. The pill's jump: the frontier is past the first page; the row
  // centres, focuses, and wears the marks.
  await fr.locator('#gppill').click();
  const jumped = await until(page, `${tag} frontier row`, () => fr.evaluate(oid => {
    const row = document.querySelector('#glrows .glr.gp-front');
    if (!row || document.activeElement !== row) return null;
    const sc = document.getElementById('glscroll').getBoundingClientRect(), r = row.getBoundingClientRect();
    // The row scrolls smoothly; wait for it to settle in view.
    if (!(r.top >= sc.top && r.bottom <= sc.bottom)) return null;
    return { rows: document.querySelectorAll('#glrows .glr[data-i]').length, inView: r.top >= sc.top && r.bottom <= sc.bottom,
      badge: (row.querySelector('.gp-rf-front') || {}).textContent || '', ring: !!document.querySelector('#glrows .gp-ring'), oid: row.textContent.includes(oid.slice(0, 7)) };
  }, frontier), 15000);
  assert.ok(jumped.rows > 200, `${tag}: the jump paged the graph ${JSON.stringify(jumped)}`);
  assert.deepEqual([jumped.inView, jumped.badge, jumped.ring, jumped.oid], [true, 'Review Frontier', true, true], `${tag}: the frontier row ${JSON.stringify(jumped)}`);
  const mini = await fr.evaluate(() => { const b = document.getElementById('gpfmini'); return { shown: b.getClientRects().length > 0, text: b.textContent }; });
  if (narrow) assert.equal(mini.shown, false, `${tag}: the narrow graph header drops the frontier chip`);
  else assert.ok(mini.shown && mini.text.includes('Frontier ' + frontier.slice(0, 7)), `${tag}: the graph header's frontier chip ${JSON.stringify(mini)}`);
  await noOverflow(fr, `${tag} jumped`);
  await shot(fr, `gp-${theme}-${view}-frontier`);

  // 9. A commit row opens the commit window on that commit (no Refresh).
  await fr.locator('#glrows .glr[data-i="0"]').click();
  await until(page, `${tag} commit window`, () => gwinOpen(fr));
  assert.equal(await fr.evaluate(() => document.querySelector('.gwin .gw-refresh').hidden), true, `${tag}: a commit window has no Refresh`);
  await page.keyboard.press('Escape');
  await until(page, `${tag} commit window closed`, () => gwinOpen(fr).then(o => !o));

  // 10. A clean tree: once everything is committed, Refresh drops the row
  // and its node and connector.
  await fr.locator('#glrows .glr[data-worktree]').click();
  await until(page, `${tag} worktree window before commit`, () => gwinOpen(fr));
  // Its diff sections read their diffs first: a read the commit overtakes
  // would answer 404 (the file is no longer changed).
  await until(page, `${tag} diffs read before commit`, () => fr.evaluate(() => {
    const s = [...document.querySelectorAll('.gwin .dfile')].map(d => d.getAttribute('data-state'));
    return s.length > 0 && !s.some(x => x === 'loading' || x === 'auto') && s;
  }));
  const git = (...a) => execFileSync('git', ['-C', project, '-c', 'user.name=fixture', '-c', 'user.email=fixture@example.invalid', ...a], { stdio: 'pipe' });
  git('add', '-A');
  for (let i = 0; ; i++) {
    try { git('commit', '-q', '--no-verify', '-m', 'commit the tree'); break; } catch (e) {
      if (i >= 4) throw e;
      await page.waitForTimeout(300);
    }
  }
  await fr.locator('.gwin .gw-refresh').click();
  const clean = await until(page, `${tag} clean tree`, () => wtRow(fr).then(w => w.i < 0 && w));
  assert.deepEqual(clean, { i: -1, node: false, conns: 0 }, `${tag}: a clean tree has no row and no stray connector`);
  await page.keyboard.press('Escape');
  await until(page, `${tag} window closed on a clean tree`, () => gwinOpen(fr).then(o => !o));
  assert.equal(await fr.evaluate(() => document.getElementById('gitdock').contains(document.activeElement)), true, `${tag}: focus stays on the page`);

  // 11. Ctrl+G again: the page stays and only refocuses (no toggle).
  await ctrlG(page, fr);
  await page.waitForTimeout(200);
  assert.equal(await shown(fr), true, `${tag}: a second Ctrl+G keeps the page`);
  assert.equal(await fr.evaluate(() => document.getElementById('gitdock').contains(document.activeElement)), true, `${tag}: a second Ctrl+G refocuses the page`);

  // 12. Leaving the page reads nothing more and closes the popover.
  await fr.locator('#gpwtbtn').click();
  await until(page, `${tag} popover before leaving`, () => popOpen(fr));
  await fr.evaluate(() => window.WsCards.showSys('ticketmap'));
  await page.waitForTimeout(300);
  assert.equal(await fr.evaluate(() => document.getElementById('gpwtpop').hidden), true, `${tag}: leaving the page closes the popover`);
  const left = reqs.length;
  await page.waitForTimeout(800);
  assert.equal(reqs.length, left, `${tag}: nothing reads while the page is hidden`);

  assert.deepEqual(errors, [], `${tag}: console errors, page errors, failed requests`);
  await context.close();
  return { tag, rows: jumped.rows, reqs: reqs.length, subject: lay.sub, ch20: lay.ch20 };
}

async function noRepoSession(browser, theme, galW) {
  const tag = `norepo ${theme}/gal${galW}`;
  const { context, page, fr, reqs, errors } = await newSession(browser, theme, galW, plainBase);
  await ctrlG(page, fr);
  await until(page, `${tag} the git page`, () => shown(fr));
  if (galW > 720) await fitOverlay(page, fr, galW);
  const st = await until(page, `${tag} empty state`, () => fr.evaluate(() => {
    const root = document.getElementById('gitpage'), e = document.getElementById('gpempty');
    return root.hasAttribute('data-norepo') && !e.hidden && e.getClientRects().length > 0 ? { graph: document.getElementById('glog').getClientRects().length, text: e.textContent } : null;
  }));
  assert.equal(st.graph, 0, `${tag}: the graph hides`);
  assert.match(st.text, /Not a git repository/, `${tag}: the empty state's text`);
  await page.waitForTimeout(400);
  assert.equal(reqs.filter(r => !r.startsWith('git/status')).length, 0, `${tag}: no graph, pool or ledger read ${JSON.stringify(reqs)}`);
  await noOverflow(fr, tag);
  await shot(fr, `gp-${theme}-norepo`);
  assert.deepEqual(errors, [], `${tag}: console errors, page errors, failed requests`);
  await context.close();
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const report = [];
  try {
    for (const theme of ['dark', 'light']) {
      for (const galW of [900, 380]) report.push(await gitSession(browser, theme, galW, bases[report.length], projects[report.length]));
      await noRepoSession(browser, theme, 900);
    }
    console.log(JSON.stringify({ frontier, sessions: report }));
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e && e.stack || e); process.exit(1); });
