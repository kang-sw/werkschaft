// Run by TestRailLifecycleDragBrowser against disposable fixture sessions.
// argv: base URL of the disposable dashboard daemon.
const assert = require('node:assert/strict');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [base] = process.argv.slice(2);
const row = (id) => `#r-rail .rs[data-rid="${id}"]`;
const DEVENV_MAIN = '#r-rail .rwt[data-g$="/devenv"]';
const DEVENV_SOLO = '#r-rail .rwt[data-g$="/devenv-solo"]';
const GAME = '#r-rail .rwt[data-g$="/game"]';

async function apiPost(page, url, body) {
  return page.evaluate(async ({ url, body }) => {
    const r = await fetch(url, {
      method: 'POST',
      headers: body !== undefined ? { 'Content-Type': 'application/json' } : {},
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    let data = null;
    try { data = await r.json(); } catch {}
    return { status: r.status, data };
  }, { url, body });
}

async function where(page, id) {
  return page.evaluate((id) => {
    const r = document.querySelector('#r-rail .rs[data-rid="' + id + '"]');
    if (!r) return '';
    const fold = r.closest('.rended');
    return fold ? fold.getAttribute('data-k') : 'live';
  }, id);
}

async function until(page, what, fn, ms = 8000) {
  const end = Date.now() + ms;
  for (;;) {
    if (await fn()) return;
    if (Date.now() > end) throw new Error('timed out waiting for ' + what);
    await page.waitForTimeout(50);
  }
}

async function groupOrder(page, wtSel) {
  return page.evaluate((wtSel) => {
    const list = document.querySelector(wtSel + ' > .rsessions');
    if (!list) return [];
    const out = [];
    for (const c of list.children) {
      if (c.classList.contains('rs')) out.push(c.getAttribute('data-rid'));
    }
    return out;
  }, wtSel);
}

async function box(page, sel) {
  let b = null;
  for (let i = 0; i < 15; i++) {
    try {
      b = await page.locator(sel).first().boundingBox({ timeout: 250 });
      if (b) return b;
    } catch {}
    await page.waitForTimeout(40);
  }
  assert.ok(b, 'missing bounding box for ' + sel);
  return b;
}

async function showRail(page) {
  await page.locator(row('starlead')).waitFor({ state: 'attached' });
  await page.waitForTimeout(200);
  if (!(await page.locator('html').evaluate((e) => e.classList.contains('rail-open')))) {
    await page.locator('#railbtn').click();
  }
  await page.locator(row('starlead')).waitFor();
  await page.addStyleTag({ content: '*{animation-play-state:paused !important; transition:none !important}' });
}

// openFoldPopup opens the archived/deleted popup from a group's fold button
// and waits for the tab for k to be the selected one.
async function openFoldPopup(page, wtSel, k) {
  const btn = page.locator(`${wtSel} > .rsessions > .rfold .rfh[data-k="${k}"]`);
  if ((await btn.getAttribute('aria-expanded')) !== 'true') await btn.click();
  await page.locator(`.rfpop:not([hidden]) .rftab[data-k="${k}"][aria-selected="true"]`).waitFor();
}
async function closeFoldPopup(page) {
  if (await page.locator('.rfpop:not([hidden])').count()) await page.keyboard.press('Escape');
  await page.locator('.rfpop').waitFor({ state: 'hidden' });
}
// life restores a row through the lifecycle API the popup buttons use, since
// the popup's rows are not drag sources.
async function life(page, id, act, body) {
  const r = await apiPost(page, '/agents/' + encodeURIComponent(id) + '/' + act, body || {});
  assert.ok(r.status >= 200 && r.status < 300, act + ' ' + id + ' -> ' + r.status);
}

// drag points from startSel (or row's .rid/.drgrip) past threshold so revealed
// 0/0 folds are laid out BEFORE evaluating targetFn(), then moves to targetFn().
async function drag(page, fromId, targetFn, { grip = false, inspect = null, waitMs = 150 } = {}) {
  const fb = await box(page, row(fromId) + (grip ? ' .drgrip' : ' .rid'));
  const sx = fb.x + fb.width / 2;
  const sy = fb.y + Math.min(fb.height / 2, 10);
  await page.mouse.move(sx, sy);
  await page.mouse.down();
  await page.mouse.move(sx, sy + 8, { steps: 2 });
  await page.waitForTimeout(30);
  const [tx, ty] = await targetFn();
  await page.mouse.move(tx, ty, { steps: 10 });
  await page.waitForTimeout(40);
  const state = inspect ? await inspect() : null;
  await page.mouse.up();
  await page.waitForTimeout(waitMs);
  return state;
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 1000 }, colorScheme: 'dark', locale: 'en-US' });
  await ctx.addCookies([{ name: 'ws_dashboard_lang', value: 'en', url: base }]);
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));

  try {
    await page.goto(base + '/');
    await showRail(page);

    // Seed parent-child, star, and game row order via /rail/layout, then reload cleanly.
    assert.equal((await apiPost(page, '/rail/layout', { op: 'star', rid: 'starlead' })).status, 200);
    assert.equal((await apiPost(page, '/rail/layout', { op: 'attach', rid: 'kidrow', parent: 'kidsrow' })).status, 200);
    assert.equal((await apiPost(page, '/rail/layout', { op: 'move', rid: 'gameone', before: 'gametwo' })).status, 200);
    await page.reload();
    await showRail(page);
    assert.equal(page.url(), base + '/');
    assert.equal((await groupOrder(page, GAME)).join(','), 'gameone,gametwo');

    // 1. 0/0 empty fold hidden at rest, margin-top buffer preserves Active reorder.
    const restEmptyDisplays = await page.evaluate(({ solo, game }) => [solo, game].map((s) => {
      const f = document.querySelector(s + ' > .rsessions > .rfold[data-empty="1"]');
      return f ? getComputedStyle(f).display : 'missing';
    }), { solo: DEVENV_SOLO, game: GAME });
    assert.deepEqual(restEmptyDisplays, ['none', 'none'], '0/0 empty folds stay hidden at rest');

    const bufLayoutP = page.waitForResponse((r) => r.url().endsWith('/rail/layout') && r.request().method() === 'POST', { timeout: 5000 });
    const bufDrag = await drag(page, 'gameone', async () => {
      const b = await box(page, row('gametwo'));
      return [b.x + b.width / 2, b.y + b.height + 6];
    }, {
      inspect: () => page.evaluate((g) => {
        const rs = document.querySelector(g + ' > .rsessions');
        const f = rs.querySelector(':scope > .rfold[data-empty="1"]');
        const l = document.querySelector('.rdrop');
        return {
          srcClass: rs.classList.contains('d-rlife-src'),
          foldDisplay: f ? getComputedStyle(f).display : '',
          archHighlight: !!document.querySelector(g + ' .rfh.d-rdrop-arch'),
          lineHidden: !l || l.hidden,
        };
      }, GAME),
      waitMs: 300,
    });
    assert.equal((await bufLayoutP).status(), 200, 'Case 1 layout reorder persisted');
    assert.equal(bufDrag.srcClass, true, 'source group gets d-rlife-src');
    assert.equal(bufDrag.foldDisplay, 'flex', '0/0 fold reveals for eligible hosted drag');
    assert.equal(bufDrag.archHighlight, false, '6px below last row stays in Active reorder zone, not fold');
    assert.equal(bufDrag.lineHidden, false, 'drop line renders for Active reorder');
    await until(page, 'gameone reordered after gametwo', async () => (await groupOrder(page, GAME)).join(',') === 'gametwo,gameone');

    // 2. 0/0 fold reveal and immediate Active -> Archived drop (no risk confirm).
    const soloSnap = await drag(page, 'solorow', async () => {
      const b = await box(page, `${DEVENV_SOLO} > .rsessions > .rfold .rfh[data-k="arch"]`);
      return [b.x + b.width / 2, b.y + b.height / 2];
    }, {
      inspect: () => page.evaluate((s) => {
        const f = document.querySelector(s + ' > .rsessions > .rfold[data-empty="1"]');
        const b = document.querySelector(s + ' > .rsessions > .rfold .rfh[data-k="arch"]');
        const l = document.querySelector('.rdrop');
        const c = document.querySelector('.rpop.d-rconf');
        return {
          foldDisplay: f ? getComputedStyle(f).display : '',
          archDrop: !!b && b.classList.contains('d-rdrop-arch'),
          lineHidden: !l || l.hidden,
          confirmOpen: !!c && !c.hidden,
        };
      }, DEVENV_SOLO),
    });
    assert.equal(soloSnap.foldDisplay, 'flex');
    assert.equal(soloSnap.archDrop, true, 'hovering revealed 0/0 arch tab marks d-rdrop-arch');
    assert.equal(soloSnap.lineHidden, true, 'row reorder line hides while hovering fold target');
    assert.equal(soloSnap.confirmOpen, false);
    assert.equal(await page.evaluate(() => {
      const c = document.querySelector('.rpop.d-rconf');
      return !c || c.hidden;
    }), true, 'idle solo lead skips risk confirm');
    await until(page, 'solorow moved to archived in solo worktree', async () => (await where(page, 'solorow')) === 'arch');

    async function assertRefused(id, targetSel, label) {
      const refused = await drag(page, id, async () => {
        const b = await box(page, targetSel);
        return [b.x + b.width / 2, b.y + b.height / 2];
      }, {
        inspect: () => page.evaluate((sel) => ({
          archDrop: !!document.querySelector(sel + '.d-rdrop-arch'),
          hintShown: (() => { const h = document.querySelector('.drhint'); return !!h && !h.hidden; })(),
        }), targetSel),
      });
      assert.equal(refused.archDrop, false, label + ' must not mark d-rdrop-arch');
      assert.equal(refused.hintShown, true, label + ' must reveal .drhint');
    }

    // Check cross-worktree refusal while DEVENV_SOLO still has solorow in arch (visible .rfold).
    await assertRefused('peer2', `${DEVENV_SOLO} > .rsessions > .rfold .rfh[data-k="arch"]`, 'cross-worktree');

    // 3. Restore solorow from the archived popup (its unarchive button) into
    // the 0-active-row group above .rfold.
    await openFoldPopup(page, DEVENV_SOLO, 'arch');
    await page.locator('.rfpop .rs[data-rid="solorow"]').hover();
    await page.locator('.rfpop .rs[data-rid="solorow"] .drarch.unarch').click();
    await until(page, 'solorow restored to empty active list', async () => (await where(page, 'solorow')) === 'live');
    await until(page, 'popup closes when its last row leaves', async () => (await page.locator('.rfpop:not([hidden])').count()) === 0);
    assert.match(await page.locator(row('solorow')).getAttribute('class'), /\bstopped\b/, 'restored solorow stays asleep');
    assert.equal(page.url(), base + '/', 'restore never navigates to /s/<id>');

    // 4. Refusal hints (.drhint) and non-draggable blocked rows.
    await assertRefused('termrow', `${DEVENV_MAIN} > .rsessions > .rfold .rfh[data-k="arch"]`, 'non-hosted CLI row');
    await assertRefused('kidrow', `${DEVENV_MAIN} > .rsessions > .rfold .rfh[data-k="arch"]`, 'child row');
    // Populate GAME's arch fold first so its .rfold stays visible when dragging peer2 from DEVENV_MAIN.
    await drag(page, 'gameone', async () => {
      const b = await box(page, `${GAME} > .rsessions > .rfold .rfh[data-k="arch"]`);
      return [b.x + b.width / 2, b.y + b.height / 2];
    });
    await until(page, 'gameone moved to game archived fold', async () => (await where(page, 'gameone')) === 'arch');
    await assertRefused('peer2', `${GAME} > .rsessions > .rfold .rfh[data-k="arch"]`, 'cross-workspace');

    // Rows in the popup are not drag sources: no grip, and pressing and
    // dragging one never starts a lifecycle drag.
    await openFoldPopup(page, DEVENV_MAIN, 'del');
    const pr = (id) => `.rfpop .rs[data-rid="${id}"]`;
    assert.equal(await page.locator(pr('delblocked')).count(), 1, 'popup lists the deleted row');
    assert.equal(await page.locator(pr('delblocked') + ' .drgrip').count(), 0, 'popup rows carry no drag grip');
    const pb = await box(page, pr('delblocked') + ' .rid');
    await page.mouse.move(pb.x + pb.width / 2, pb.y + pb.height / 2);
    await page.mouse.down();
    await page.mouse.move(900, 500, { steps: 10 });
    const popDrag = await page.evaluate(() => document.documentElement.classList.contains('rdragging') || document.documentElement.classList.contains('rdragging-life'));
    await page.mouse.up();
    assert.equal(popDrag, false, 'dragging a popup row never starts a rail drag');
    await page.waitForTimeout(150);
    assert.equal(await where(page, 'delblocked'), 'del', 'blocked deleted row stays in deleted fold');
    await closeFoldPopup(page);

    // 5. Bidirectional transitions & positioned restores (Active <-> Archived <-> Deleted).
    await drag(page, 'peer2', async () => {
      const b = await box(page, `${DEVENV_MAIN} > .rsessions > .rfold .rfh[data-k="del"]`);
      return [b.x + b.width / 2, b.y + b.height / 2];
    });
    await until(page, 'peer2 moved Active -> Deleted', async () => (await where(page, 'peer2')) === 'del');

    // Folded rows are not drag sources, so Archived <-> Deleted and the
    // restores run through the same lifecycle routes the drops post to.
    await life(page, 'delreopen', 'reopen', { hidden: true });
    await until(page, 'delreopen moved Deleted -> Archived (reopen {hidden: true})', async () => (await where(page, 'delreopen')) === 'arch');

    await life(page, 'delreopen', 'unhide');
    await until(page, 'delreopen restored Archived -> Active', async () => (await where(page, 'delreopen')) === 'live');

    await drag(page, 'delreopen', async () => {
      const b = await box(page, `${DEVENV_MAIN} > .rsessions > .rfold .rfh[data-k="del"]`);
      return [b.x + b.width / 2, b.y + b.height / 2];
    });
    await until(page, 'delreopen moved back to Deleted', async () => (await where(page, 'delreopen')) === 'del');

    await life(page, 'delreopen', 'reopen', { hidden: false });
    await until(page, 'delreopen restored Deleted -> Active', async () => (await where(page, 'delreopen')) === 'live');
    assert.match(await page.locator(row('delreopen')).getAttribute('class'), /\bstopped\b/, 'reopened delreopen stays asleep');
    assert.equal(await page.locator(row('delreopen') + ' .drgrip').getAttribute('data-proc'), null, 'reopened delreopen has no live process');
    assert.equal(page.url(), base + '/', 'Deleted -> Active restore never navigates to /s/<id>');

    // 6. Risk confirmation popovers: synthetic click guard, cancel, confirm (proc, kids, bg).
    await drag(page, 'procrow', async () => {
      const b = await box(page, `${DEVENV_MAIN} > .rsessions > .rfold .rfh[data-k="arch"]`);
      return [b.x + b.width / 2, b.y + b.height / 2];
    });
    await page.waitForTimeout(220);
    assert.equal(await page.locator('.rpop.d-rconf').evaluate((e) => e.hidden), false, 'confirm popover stays open past synthetic release click');
    assert.equal(await page.locator('.rpop.d-rconf .d-rconf-row').count(), 1);
    const confCancelSel = '.rpop.d-rconf .d-rconf-row .d-rconf-btn:not(.go):not(.d-rconf-danger)';
    assert.match(await page.locator('.rpop.d-rconf .d-rconf-risks').innerText(), /Stops the running session process/);
    await page.locator(confCancelSel).click();
    assert.equal(await page.locator('.rpop.d-rconf').evaluate((e) => e.hidden), true);
    assert.equal(await where(page, 'procrow'), 'live', 'cancel keeps procrow live');

    await drag(page, 'procrow', async () => {
      const b = await box(page, `${DEVENV_MAIN} > .rsessions > .rfold .rfh[data-k="arch"]`);
      return [b.x + b.width / 2, b.y + b.height / 2];
    });
    await page.locator('.rpop.d-rconf .d-rconf-row .d-rconf-btn.go').click();
    await until(page, 'procrow archived on confirm', async () => (await where(page, 'procrow')) === 'arch');

    await drag(page, 'bgrow', async () => {
      const b = await box(page, `${DEVENV_MAIN} > .rsessions > .rfold .rfh[data-k="arch"]`);
      return [b.x + b.width / 2, b.y + b.height / 2];
    });
    assert.equal(await page.locator('.rpop.d-rconf').evaluate((e) => e.hidden), false, 'bgrow opens risk confirmation');
    assert.match(await page.locator('.rpop.d-rconf .d-rconf-risks').innerText(), /Stops running background jobs/);
    await page.locator(confCancelSel).click();
    assert.equal(await where(page, 'bgrow'), 'live');

    await drag(page, 'kidsrow', async () => {
      const b = await box(page, `${DEVENV_MAIN} > .rsessions > .rfold .rfh[data-k="del"]`);
      return [b.x + b.width / 2, b.y + b.height / 2];
    });
    assert.equal(await page.locator('.rpop.d-rconf').evaluate((e) => e.hidden), false, 'kidsrow opens risk confirmation');
    assert.match(await page.locator('.rpop.d-rconf .d-rconf-risks').innerText(), /Has child sessions linked to it/);
    await page.locator('.rpop.d-rconf .d-rconf-danger').click();
    await until(page, 'kidsrow deleted on confirm', async () => (await where(page, 'kidsrow')) === 'del');

    // 7. The archived popup never shows as an inline list and the live row
    // still lands on the fold button (no inline list drop target remains);
    // then 500 error recovery and !data-can-control refusal.
    await openFoldPopup(page, DEVENV_MAIN, 'arch');
    assert.equal(await page.evaluate((m) => [...document.querySelectorAll(m + ' .rended')].every((l) => getComputedStyle(l).display === 'none'), DEVENV_MAIN), true, 'fold sources stay hidden while the popup is open');
    await closeFoldPopup(page);
    const listSnap = await drag(page, 'peer1', async () => {
      const b = await box(page, `${DEVENV_MAIN} > .rsessions > .rfold .rfh[data-k="arch"]`);
      return [b.x + b.width / 2, b.y + b.height / 2];
    }, {
      inspect: () => page.evaluate(() => ({
        archDrop: !!document.querySelector('.rfh.d-rdrop-arch'),
        popupOpen: !!document.querySelector('.rfpop:not([hidden])'),
        listDrop: !!document.querySelector('.d-rdrop-list'),
      })),
    });
    assert.equal(listSnap.archDrop, true, 'live row over the fold button marks d-rdrop-arch');
    assert.equal(listSnap.listDrop, false, 'no inline list drop target exists');
    assert.equal(listSnap.popupOpen, false);
    await until(page, 'peer1 moved into the archived fold', async () => (await where(page, 'peer1')) === 'arch');

    await drag(page, 'archfail', async () => {
      const b = await box(page, `${DEVENV_MAIN} > .rsessions > .rfold .rfh[data-k="arch"]`);
      return [b.x + b.width / 2, b.y + b.height / 2];
    });
    await page.locator('#wstoasts .wstoast').first().waitFor();
    assert.equal(await where(page, 'archfail'), 'live', '500 failure keeps row active');

    await page.evaluate(() => document.documentElement.removeAttribute('data-can-control'));
    const noControl = await drag(page, 'archfail', async () => {
      const b = await box(page, `${DEVENV_MAIN} > .rsessions > .rfold .rfh[data-k="arch"]`);
      return [b.x + b.width / 2, b.y + b.height / 2];
    }, {
      inspect: () => page.evaluate(() => ({
        hintShown: (() => { const h = document.querySelector('.drhint'); return !!h && !h.hidden; })(),
        archDrop: !!document.querySelector('.rfh.d-rdrop-arch'),
      })),
    });
    assert.equal(noControl.archDrop, false, 'non-owner cannot target lifecycle drop');
    assert.equal(noControl.hintShown, true, 'non-owner receives .drhint control refusal');
    assert.equal(await where(page, 'archfail'), 'live');

    assert.equal(page.url(), base + '/', 'page stayed on / across all lifecycle drags');
    assert.deepEqual(errors, [], 'zero uncaught page errors');
    console.log('rail lifecycle drag acceptance passed');
  } finally {
    await ctx.close();
    await browser.close();
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
