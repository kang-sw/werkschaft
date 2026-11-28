// Run by TestRailFourStateBrowser against disposable fixture sessions.
// argv: the fixture daemon's base URL, and an optional absolute directory
// for the mockup-matched screenshots.
const assert = require('node:assert/strict');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, shots] = process.argv.slice(2);

const DEVENV = '#r-rail .rws[data-ws$="/devenv"]';
const row = (id) => `#r-rail .rs[data-rid="${id}"]`;

// where reports which list a row sits in: "live", "arch", "del", or "".
async function where(page, id) {
  return page.evaluate((id) => {
    const r = document.querySelector('#r-rail .rs[data-rid="' + id + '"]');
    if (!r) return '';
    const fold = r.closest('.rended');
    return fold ? fold.getAttribute('data-k') : 'live';
  }, id);
}
// holdAction presses and holds a button for holdMs, then releases inside it.
async function holdAction(page, locator, holdMs) {
  await locator.waitFor();
  const box = await locator.boundingBox();
  assert(box && box.width > 0 && box.height > 0, 'action button has bounds');
  const cx = box.x + box.width / 2, cy = box.y + box.height / 2;
  await page.mouse.move(cx, cy);
  await page.mouse.down();
  await page.waitForTimeout(holdMs);
  await page.mouse.up();
}
async function openRowArchive(page, id) {
  await page.mouse.move(5, 5);
  await page.locator(row(id) + ' .rl').click({ button: 'right' });
  const item = page.locator('.rpop.rmenu .rmsess .spact', { hasText: '보관' }).first();
  await item.waitFor();
  return item;
}
async function until(page, what, fn, ms = 8000) {
  const end = Date.now() + ms;
  for (;;) {
    if (await fn()) return;
    if (Date.now() > end) throw new Error('timed out waiting for ' + what);
    await page.waitForTimeout(60);
  }
}
const BULK = '#r-rail .rws[data-ws$="/long-history"]';
const POP = '.rfpop:not([hidden])';
async function half(page, k, ws = DEVENV) {
  await page.locator(`${ws} .rfh[data-k="${k}"]`).click();
}
// openState reports the group's fold buttons, the popup's open tab, and any
// inline .rended list that is displayed (never expected: the lists are only
// the popup's hidden source).
async function openState(page, ws = DEVENV) {
  return page.evaluate((sel) => {
    const halves = [...document.querySelectorAll(sel + ' .rfh')].map((b) => b.getAttribute('data-k') + ':' + b.getAttribute('aria-expanded'));
    const inline = [...document.querySelectorAll('#r-rail .rended')].filter((l) => getComputedStyle(l).display !== 'none').length;
    const p = document.querySelector('.rfpop:not([hidden])');
    const t = p && p.querySelector('.rftab[aria-selected="true"]');
    return { halves, tab: t ? t.getAttribute('data-k') : '', inline };
  }, ws);
}
async function popRows(page) {
  return page.evaluate(() => [...document.querySelectorAll('.rfpop .rflist .rs')].filter((r) => !r.hidden).map((r) => r.getAttribute('data-rid')));
}
async function popClosed(page, what) {
  await until(page, what, async () => (await page.locator(POP).count()) === 0);
}

// showRail opens the rail where it is the overlay (below the pinned-rail
// width), which the top-left button opens.
async function showRail(page) {
  await page.locator(row('mainlead')).waitFor({ state: 'attached' });
  await page.waitForTimeout(300);
  if (!(await page.locator(row('mainlead')).isVisible())) {
    await page.locator('#railbtn').click();
    await page.locator(row('mainlead')).waitFor();
  }
}

async function openPage(browser, scheme, width) {
  const ctx = await browser.newContext({ viewport: { width, height: 1000 }, colorScheme: scheme, deviceScaleFactor: 2, locale: 'ko-KR', extraHTTPHeaders: { 'Accept-Language': 'ko-KR,ko' } });
  // The rail is one server render per language; the cookie picks Korean.
  await ctx.addCookies([{ name: 'ws_dashboard_lang', value: 'ko', url: base }]);
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await page.goto(base + '/');
  await page.locator(row('mainlead')).waitFor({ state: 'attached' });
  await page.addStyleTag({ content: '*{animation-play-state:paused !important; transition:none !important}' });
  await showRail(page);
  await page.waitForTimeout(300);
  return { ctx, page, errors };
}

async function behaviour(browser) {
  const { ctx, page, errors } = await openPage(browser, 'dark', 1440);
  try {
    // The fold row: none open, then the archived popup, then the deleted tab
    // (one popup), then closed; never an inline list.
    assert.deepEqual(await openState(page), { halves: ['arch:false', 'del:false'], tab: '', inline: 0 }, 'nothing open at first');
    await half(page, 'arch');
    assert.deepEqual(await openState(page), { halves: ['arch:true', 'del:false'], tab: 'arch', inline: 0 }, 'archived popup open');
    await half(page, 'del');
    assert.deepEqual(await openState(page), { halves: ['arch:false', 'del:true'], tab: 'del', inline: 0 }, 'one popup: the deleted button shows its tab');
    await half(page, 'del');
    assert.deepEqual(await openState(page), { halves: ['arch:false', 'del:false'], tab: '', inline: 0 }, 'the same button closes it');
    const game = '#r-rail .rws[data-ws$="/gunpowder-odyssey"] .rfh[data-k="arch"]';
    assert.equal(await page.locator(game).isDisabled(), true, 'an empty archived half is disabled');
    // A stopped hosted lead stays in place; an ended terminal is folded.
    assert.equal(await where(page, 'hostexit'), 'live', 'stopped hosted lead keeps its row');
    assert.equal(await page.locator(row('hostexit') + ' .rstate').textContent(), '중단됨');
    assert.equal(await page.locator(row('hostexit') + ' time.when.del').count(), 0);
    assert.equal(await where(page, 'termtrial'), 'del', 'ended terminal is folded');
    assert.equal(await page.locator(row('termtrial') + ' time.when.del').count(), 1);
    assert.equal(await where(page, 'release49'), 'del', 'deleted lead is folded');
    assert.match(await page.locator(`${DEVENV} .rfh[data-k="del"]`).textContent(), /삭제됨/);
    const del = await page.locator(row('release49') + ' time.when.del').textContent();
    assert.match(del, /^6일 후 삭제$/, 'pending-deletion time: ' + del);
    assert.equal(await page.locator(row('hotfixprobe') + ' time.when.del.soon').count(), 1, 'under a day is warned');
    assert.equal(await where(page, 'execsecond'), 'del', 'retired Pi history stays in the ended fold');
    assert.equal(await page.locator(row('execsecond') + ' time.when.del').count(), 0, 'retired Pi history has no deletion countdown');
    // The popup's rows are copies of those rows, with the same countdown.
    await half(page, 'del');
    assert.match(await page.locator('.rfpop .rs[data-rid="release49"] time.when.del').textContent(), /^6일 후 삭제$/);
    await half(page, 'del');
    await popClosed(page, 'the deleted popup closes');
    // Active, asleep, archived, and deleted keep their styling.
    assert.deepEqual(await page.evaluate(() => ['mainlead', 'reviewer', 'docaudit', 'release49'].map((id) => document.querySelector('#r-rail .rs[data-rid="' + id + '"]').className)),
      ['rs hosted-row', 'rs stopped hosted-row', 'rs ended stopped archived hosted-row', 'rs ended'], 'row classes');

    // Live hosted rows show hover stop only when a process is live, never hover archive.
    assert.equal(await page.locator(row('mainlead') + ' .drstop').count(), 1, 'active hosted row offers hover stop');
    assert.equal(await page.locator(row('mainlead') + ' .drarch').count(), 0, 'active hosted row has no hover archive');
    assert.equal(await page.locator(row('reviewer') + ' .drstop').count(), 0, 'asleep hosted row has no hover stop');
    assert.equal(await page.locator(row('reviewer') + ' .drarch').count(), 0, 'asleep hosted row has no hover archive');

    // Archive from the row menu uses a 1.5s press-and-hold (early release cancels
    // and keeps the menu open; holding 1.5s confirms and closes the menu).
    const archItem = await openRowArchive(page, 'reviewer');
    await holdAction(page, archItem, 300);
    await page.waitForTimeout(400);
    assert.equal(await where(page, 'reviewer'), 'live', 'early release does not archive');
    await holdAction(page, archItem, 1650);
    await until(page, 'reviewer archived', async () => (await where(page, 'reviewer')) === 'arch');
    assert.equal(await page.locator(`${DEVENV} .rfh[data-k="arch"] .d-n`).textContent(), '4', 'the archived count');
    // It persists across a reload (the daemon's sidecar, not page state).
    await page.reload();
    await showRail(page);
    assert.equal(await where(page, 'reviewer'), 'arch', 'still archived after reload');
    // Unarchive returns it asleep among the live rows via the popup row's
    // hover button (immediate click); the popup stays open for the rest.
    await half(page, 'arch');
    const popRow = '.rfpop .rs[data-rid="reviewer"]';
    await page.locator(popRow).hover();
    await page.locator(popRow + ' .drarch.unarch').click();
    await until(page, 'reviewer back', async () => (await where(page, 'reviewer')) === 'live');
    await until(page, 'the popup drops the unarchived row', async () => !(await popRows(page)).includes('reviewer'));
    assert.equal((await openState(page)).tab, 'arch', 'popup stays open on archived after unarchive');
    await page.keyboard.press('Escape');
    await popClosed(page, 'Escape closes the popup');
    assert.match(await page.locator(row('reviewer')).getAttribute('class'), /\bstopped\b/, 'unarchived is asleep');

    // A 409 archive leaves the row put, says why, and is not retried.
    const busyArch = await openRowArchive(page, 'assettidy');
    await holdAction(page, busyArch, 1650);
    await page.locator('#wstoasts .wstoast', { hasText: '아직 세션 확인 중이라 보관할 수 없습니다' }).waitFor();
    await page.waitForTimeout(1200);
    assert.equal(await where(page, 'assettidy'), 'live', 'a refused archive leaves the row in place');
    // A 500 archive (the holder refused to stop; the runner rolled back)
    // likewise leaves the row put with a short message, and no retry.
    const failArch = await openRowArchive(page, 'renderer');
    await holdAction(page, failArch, 1650);
    await page.locator('#wstoasts .wstoast', { hasText: '세션을 멈추지 못해 보관하지 못했습니다' }).waitFor();
    await page.waitForTimeout(1200);
    assert.equal(await where(page, 'renderer'), 'live', 'a failed archive leaves the row in place');

    // Re-archive via menu hold, then unarchive from the popup row's button;
    // a right-click on a popup row opens no row menu (it has no menu).
    const menuArch = await openRowArchive(page, 'reviewer');
    await holdAction(page, menuArch, 1650);
    await until(page, 'reviewer archived from the menu', async () => (await where(page, 'reviewer')) === 'arch');
    await half(page, 'arch');
    await page.locator(popRow + ' .rl').click({ button: 'right' });
    await page.waitForTimeout(150);
    assert.equal(await page.locator('.rpop.rmenu:not([hidden])').count(), 0, 'a popup row has no row menu');
    assert.equal(await page.locator(POP).count(), 1, 'the popup stays open after the right-click');
    await page.locator(popRow).hover();
    await page.locator(popRow + ' .drarch.unarch').click();
    await until(page, 'reviewer unarchived from the popup', async () => (await where(page, 'reviewer')) === 'live');
    await page.keyboard.press('Escape');
    await popClosed(page, 'Escape closes the popup');
    assert.deepEqual(errors, [], 'no page errors');
  } finally {
    await ctx.close();
  }
}

// geometry is the popup contract in numbers: the list is bounded and scrolls
// vertically only, the popup stays inside the viewport and within the rail's
// width, and the live rows have not moved.
async function geometry(page) {
  return page.evaluate(() => {
    const l = document.querySelector('.rfpop .rflist'), p = document.querySelector('.rfpop'), r = document.querySelector('#rail');
    const pb = p.getBoundingClientRect(), rb = r.getBoundingClientRect();
    return {
      listH: l.clientHeight, listScroll: l.scrollHeight, listClientW: l.clientWidth, listScrollW: l.scrollWidth,
      overflowY: getComputedStyle(l).overflowY, overflowX: getComputedStyle(l).overflowX,
      inView: pb.top >= 0 && pb.left >= 0 && pb.bottom <= window.innerHeight && pb.right <= window.innerWidth,
      popW: pb.width, railW: rb.width, vh: window.innerHeight,
    };
  });
}

// popup drives the long-history group's archived popup: bounded scroll with
// 36 rows and unshifted live rows, search and its empty state, tabs, scroll
// and query across a live rail swap, and every way of closing it.
async function popup(browser) {
  const { ctx, page, errors } = await openPage(browser, 'dark', 1440);
  try {
    const live = row('bulklive');
    const before = await page.locator(live).boundingBox();
    const lead = await page.locator(row('mainlead')).boundingBox();
    await half(page, 'arch', BULK);
    await page.locator(POP).waitFor();
    assert.deepEqual(await openState(page, BULK), { halves: ['arch:true', 'del:false'], tab: 'arch', inline: 0 });
    assert.equal(await page.locator(live).evaluate((e) => e.hidden || !e.getClientRects().length), false, 'the live row stays shown');
    assert.deepEqual(await page.locator(live).boundingBox(), before, 'opening the popup does not move the live rows');
    assert.deepEqual(await page.locator(row('mainlead')).boundingBox(), lead, 'nor other groups\' rows');
    assert.match(await page.locator('.rfpop .rfpt').textContent(), /long-history/, 'the header names the workspace');
    assert.equal((await popRows(page)).length, 36, 'every archived row is listed');
    const g = await geometry(page);
    assert.equal(g.overflowY, 'auto');
    assert.equal(g.overflowX, 'hidden');
    assert.ok(g.listH <= Math.min(0.6 * g.vh, 420) + 1, 'bounded list height: ' + g.listH);
    assert.ok(g.listScroll > g.listH, 'a long history scrolls inside the list');
    assert.ok(g.listScrollW <= g.listClientW, 'no horizontal overflow');
    assert.ok(g.inView, 'the popup stays inside the viewport');
    assert.ok(g.popW <= g.railW, 'the popup is no wider than the rail');
    assert.equal(await page.locator('.rfpop .rftab[data-k="del"]').isDisabled(), true, 'the empty deleted tab is disabled');
    // Search: case-insensitive over id, label, harness; the empty state.
    const qin = page.locator('.rfpop .rfq');
    await qin.fill('BULKARCH3');
    assert.deepEqual((await popRows(page)).sort(), ['bulkarch30', 'bulkarch31', 'bulkarch32', 'bulkarch33', 'bulkarch34', 'bulkarch35', 'bulkarch36'], 'id search is case-insensitive');
    await qin.fill('기록 07');
    // The haystack is the row's whole text, so a time or count may match too.
    const byLabel = await popRows(page);
    assert.ok(byLabel.includes('bulkarch07') && byLabel.length < 6, 'label search: ' + byLabel.join(','));
    await qin.fill('zzzz-no-such');
    assert.deepEqual(await popRows(page), []);
    assert.equal(await page.locator('.rfpop .rfnone').isVisible(), true, 'the empty-search message shows');
    await qin.fill('');
    assert.equal((await popRows(page)).length, 36);
    assert.equal(await page.locator('.rfpop .rfnone').isVisible(), false);
    // Query and scroll survive a live rail swap (a star toggles the layout).
    await qin.fill('보관 기록');
    const list = page.locator('.rfpop .rflist');
    await list.evaluate((e) => { e.scrollTop = 260; });
    const top = await list.evaluate((e) => e.scrollTop);
    assert.ok(top > 100, 'the list scrolled: ' + top);
    await page.evaluate(() => { window.__swapTag = document.querySelector('#r-rail .rs[data-rid="bulklive"]'); });
    const star = await page.evaluate(async () => (await fetch('/rail/layout', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ op: 'star', rid: 'mainlead' }) })).status);
    assert.equal(star, 200);
    await until(page, 'a rail swap', async () => page.evaluate(() => { const n = document.querySelector('#r-rail .rs[data-rid="bulklive"]'); return !!n && n !== window.__swapTag; }));
    assert.equal(await qin.inputValue(), '보관 기록', 'the query survives a swap');
    assert.ok(Math.abs((await list.evaluate((e) => e.scrollTop)) - top) <= 2, 'the scroll position survives a swap');
    assert.equal(await page.locator(POP).count(), 1, 'the popup survives a swap');
    // Escape closes; so do a click outside, the header close, and the same button.
    await page.keyboard.press('Escape');
    await popClosed(page, 'Escape closes');
    assert.deepEqual(await openState(page, BULK), { halves: ['arch:false', 'del:false'], tab: '', inline: 0 });
    await half(page, 'arch', BULK);
    await page.locator(POP).waitFor();
    await page.mouse.click(1100, 500);
    await popClosed(page, 'an outside click closes');
    // (the overlay rail closes on that click too, as it always has)
    await showRail(page);
    await half(page, 'arch', BULK);
    await page.locator(POP).waitFor();
    await page.locator('.rfpop .rfx').click();
    await popClosed(page, 'the header x closes');
    await half(page, 'arch', BULK);
    await page.locator(POP).waitFor();
    await half(page, 'arch', BULK);
    await popClosed(page, 'the same button closes');
    // Tabs on a group with both lists; arrow keys switch them.
    await half(page, 'arch');
    await page.locator(POP).waitFor();
    const archRows = await popRows(page);
    assert.ok(archRows.includes('docaudit') && !archRows.includes('release49'), 'archived tab lists archived rows');
    await page.locator('.rfpop .rftab[data-k="del"]').click();
    assert.equal((await openState(page)).tab, 'del');
    const delRows = await popRows(page);
    assert.ok(delRows.includes('release49') && !delRows.includes('docaudit'), 'deleted tab lists deleted rows');
    await page.locator('.rfpop .rftab[data-k="del"]').focus();
    await page.keyboard.press('ArrowLeft');
    assert.equal((await openState(page)).tab, 'arch', 'ArrowLeft goes to the archived tab');
    // A row's link opens its session and closes the popup.
    await page.locator('.rfpop .rs[data-rid="docaudit"] .rl').click();
    await page.waitForURL(/\/s\/docaudit/);
    await popClosed(page, 'following a row closes the popup');
    assert.deepEqual(errors, [], 'no page errors');
  } finally {
    await ctx.close();
  }
}

async function capture(browser) {
  for (const scheme of ['dark', 'light']) {
    for (const width of [1440, 700]) {
      const { ctx, page, errors } = await openPage(browser, scheme, width);
      const tag = `${width}-${scheme}`;
      const rail = page.locator('#rail');
      const shoot = (name) => rail.screenshot({ path: path.join(shots, `${name}-${tag}.png`) });
      // shootPop saves the rail together with its open popup.
      const shootPop = async (name) => {
        const a = await rail.boundingBox(), m = await page.locator(POP).boundingBox();
        const x = Math.max(0, Math.min(a.x, m.x) - 8), y = Math.max(0, Math.min(a.y, m.y) - 8);
        const clip = { x, y, width: Math.max(a.x + a.width, m.x + m.width) + 8 - x, height: Math.min(1000, Math.max(a.y + a.height, m.y + m.height) + 8) - y };
        await page.screenshot({ path: path.join(shots, `${name}-${tag}.png`), clip });
      };
      try {
        await shoot('rail-default');
        await half(page, 'arch');
        await page.mouse.move(5, 999);
        await page.waitForTimeout(150);
        await shootPop('rail-archive-open');
        await page.locator('.rfpop .rs[data-rid="docaudit"]').hover();
        await page.waitForTimeout(150);
        await shootPop('row-unarchive');
        await half(page, 'del');
        await page.mouse.move(5, 999);
        await page.waitForTimeout(150);
        await shootPop('rail-stopped-open');
        await half(page, 'del');
        await half(page, 'arch', BULK);
        await page.mouse.move(5, 999);
        await page.waitForTimeout(150);
        await shootPop('rail-archive-long');
        await page.locator('.rfpop .rfq').fill('bulkarch3');
        await shootPop('rail-archive-search');
        await page.locator('.rfpop .rfq').fill('zzzz');
        await shootPop('rail-archive-nomatch');
        await page.keyboard.press('Escape');
        await page.locator(row('mainlead')).hover();
        await page.waitForTimeout(150);
        await shoot('row-hover-archive-action');
        await page.mouse.move(5, 999);
        await page.locator(row('reviewer') + ' .rl').click({ button: 'right' });
        await page.locator('.rpop.rmenu .rmsess:not([hidden])').waitFor();
        await page.waitForTimeout(200);
        const a = await rail.boundingBox(), m = await page.locator('.rpop.rmenu').boundingBox();
        const x = Math.max(0, Math.min(a.x, m.x) - 8), y = Math.max(0, Math.min(a.y, m.y) - 8);
        const clip = { x, y, width: Math.max(a.x + a.width, m.x + m.width) + 8 - x, height: Math.min(1000, Math.max(a.y + a.height, m.y + m.height) + 8) - y };
        await page.screenshot({ path: path.join(shots, `row-menu-archive-${tag}.png`), clip });
        assert.deepEqual(errors, [], 'no page errors in ' + tag);
      } finally {
        await ctx.close();
      }
    }
  }
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    await behaviour(browser);
    await popup(browser);
    if (shots) await capture(browser);
    console.log('four-state rail acceptance passed');
  } finally {
    await browser.close();
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
