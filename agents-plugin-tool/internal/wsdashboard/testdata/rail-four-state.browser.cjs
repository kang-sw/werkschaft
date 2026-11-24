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
async function half(page, k) {
  await page.locator(`${DEVENV} .rfh[data-k="${k}"]`).click();
}
async function openState(page) {
  return page.evaluate((sel) => {
    const halves = [...document.querySelectorAll(sel + ' .rfh')].map((b) => b.getAttribute('data-k') + ':' + b.getAttribute('aria-expanded'));
    const lists = [...document.querySelectorAll(sel + ' .rended')].filter((l) => getComputedStyle(l).display !== 'none').map((l) => l.getAttribute('data-k'));
    return { halves, lists };
  }, DEVENV);
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
    // The fold row: none open, then archived, then ended, then none.
    assert.deepEqual(await openState(page), { halves: ['arch:false', 'del:false'], lists: [] }, 'nothing open at first');
    await half(page, 'arch');
    assert.deepEqual(await openState(page), { halves: ['arch:true', 'del:false'], lists: ['arch'] }, 'archived open');
    await half(page, 'del');
    assert.deepEqual(await openState(page), { halves: ['arch:false', 'del:true'], lists: ['del'] }, 'one radio: ended open closes archived');
    await half(page, 'del');
    assert.deepEqual(await openState(page), { halves: ['arch:false', 'del:false'], lists: [] }, 'ended closed again');
    const game = '#r-rail .rws[data-ws$="/gunpowder-odyssey"] .rfh[data-k="arch"]';
    assert.equal(await page.locator(game).isDisabled(), true, 'an empty archived half is disabled');
    // A stopped hosted lead stays in place; an ended terminal is folded.
    assert.equal(await where(page, 'hostexit'), 'live', 'stopped hosted lead keeps its row');
    assert.equal(await page.locator(row('hostexit') + ' .rstate').textContent(), '중단됨');
    assert.equal(await page.locator(row('hostexit') + ' time.when.del').count(), 0);
    assert.equal(await where(page, 'termtrial'), 'del', 'ended terminal is folded');
    assert.equal(await page.locator(row('termtrial') + ' time.when.del').count(), 1);
    await half(page, 'del');
    assert.equal(await where(page, 'release49'), 'del', 'deleted lead is folded');
    assert.match(await page.locator(`${DEVENV} .rfh[data-k="del"]`).textContent(), /삭제됨/);
    const del = await page.locator(row('release49') + ' time.when.del').textContent();
    assert.match(del, /^6일 후 삭제$/, 'pending-deletion time: ' + del);
    assert.equal(await page.locator(row('hotfixprobe') + ' time.when.del.soon').count(), 1, 'under a day is warned');
    assert.equal(await where(page, 'execsecond'), 'del', 'retired Pi history stays in the ended fold');
    assert.equal(await page.locator(row('execsecond') + ' time.when.del').count(), 0, 'retired Pi history has no deletion countdown');
    await half(page, 'del');
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
    // Unarchive returns it asleep among the live rows via hover button (immediate click).
    await half(page, 'arch');
    await page.locator(row('reviewer')).hover();
    await page.locator(row('reviewer') + ' .drarch.unarch').click();
    await until(page, 'reviewer back', async () => (await where(page, 'reviewer')) === 'live');
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

    // Re-archive via menu hold, then unarchive via menu click.
    const menuArch = await openRowArchive(page, 'reviewer');
    await holdAction(page, menuArch, 1650);
    await until(page, 'reviewer archived from the menu', async () => (await where(page, 'reviewer')) === 'arch');
    await page.locator(row('reviewer') + ' .rl').click({ button: 'right' });
    const unItem = page.locator('.rpop.rmenu .rmsess .spact', { hasText: '보관 해제' });
    await unItem.waitFor();
    await unItem.click();
    await until(page, 'reviewer unarchived from the menu', async () => (await where(page, 'reviewer')) === 'live');
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
      try {
        await shoot('rail-default');
        await half(page, 'arch');
        await page.mouse.move(5, 999);
        await shoot('rail-archive-open');
        await page.locator(row('docaudit')).hover();
        await page.waitForTimeout(150);
        await shoot('row-unarchive');
        await half(page, 'del');
        await page.mouse.move(5, 999);
        await shoot('rail-stopped-open');
        await half(page, 'del');
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
    if (shots) await capture(browser);
    console.log('four-state rail acceptance passed');
  } finally {
    await browser.close();
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
