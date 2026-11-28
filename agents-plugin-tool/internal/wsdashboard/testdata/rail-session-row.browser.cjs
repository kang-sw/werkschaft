// TestRailSessionRowBrowser: real served UI against a disposable daemon.
// Only session creation is a canned agent-host boundary; no harness starts.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, shots] = process.argv.slice(2);
const row = id => `#r-rail .rs[data-rid="${id}"]`;
const menu = '.rpop.rmenu:not([hidden])';

async function until(fn, message) {
  for (let i = 0; i < 150; i++) {
    if (await fn()) return;
    await new Promise(r => setTimeout(r, 50));
  }
  assert.fail(message);
}
async function showRail(page) {
  await page.locator(row('plainrow')).waitFor({ state: 'attached' });
  if (!await page.locator(row('plainrow')).isVisible()) await page.locator('#railbtn').click();
  try { await page.locator(row('plainrow')).waitFor({ timeout: 5000 }); }
  catch (e) {
    await shot(page, 'failure-rail');
    console.error('RAIL STATE:', await page.evaluate(() => ({ root: document.documentElement.outerHTML.slice(0, 800), rail: document.querySelector('#rail').getBoundingClientRect().toJSON() })));
    throw e;
  }
}
async function shot(page, name) {
  if (!shots) return;
  // Keep the rename focused, but clear incidental hover cards and the
  // deliberately triggered failure toast before capturing owner evidence.
  const { width, height } = page.viewportSize();
  await page.mouse.move(width - 5, height - 5);
  await page.waitForFunction(() => !document.querySelector('.rpop.shp:not([hidden]), .wstoast'));
  await page.screenshot({ path: path.join(shots, name + '.png'), scale: 'css' });
}
async function openMenu(page, id, keyboard = false) {
  if (keyboard) {
    await page.locator(row(id) + ' .rl').focus();
    await page.keyboard.press('Shift+F10');
  } else await page.locator(row(id) + ' .rid').click({ button: 'right' });
  await page.locator(menu).waitFor();
}
async function checkStaleSafeStop(page, control) {
  const posts = [];
  const routeName = '**/agents/plainrow/stop';
  await page.route(routeName, route => {
    posts.push(route.request().postDataJSON());
    return posts.length === 1
      ? route.fulfill({ status: 409, body: 'session no longer safe to stop' })
      : route.fulfill({ status: 200, json: { id: 'plainrow', can_control: true, process: false } });
  });
  await control.and(page.locator('[data-safe="1"]')).waitFor();
  await control.click();
  await until(() => posts.length > 0, 'safe Stop sent');
  assert.deepEqual(posts[0], { safe_only: true }, 'single-click Stop requests a safe-only admission');
  await control.and(page.locator('[data-safe="0"]')).waitFor();
  await control.focus();
  await page.keyboard.press('Space');
  assert.equal(posts.length, 1, 'single press after refusal sends nothing');
  await page.keyboard.down('Space');
  await control.and(page.locator('[data-hold-state="ready"]')).waitFor();
  await page.keyboard.up('Space');
  await until(() => posts.length > 1, 'confirmed Stop sent');
  assert.deepEqual(posts, [{ safe_only: true }, {}], 'only a completed hold can stop the now-busy session');
  await page.unroute(routeName);
}
async function toggle(page, id, on, keyboard = false) {
  await openMenu(page, id, keyboard);
  const item = page.locator(menu).getByRole('menuitem', { name: on ? '즐겨찾기에 추가' : '즐겨찾기 해제', exact: true });
  assert.equal(await item.getAttribute('data-on'), String(!on));
  if (keyboard) {
    // Rename -> copy -> favorite, using the menu's existing arrow path.
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('ArrowDown');
    assert.equal(await item.evaluate(e => e === document.activeElement), true);
    await page.keyboard.press('Enter');
  } else await item.click();
  await page.locator(menu).waitFor({ state: 'hidden' });
}
async function order(page) {
  return page.locator(row('plainrow')).evaluate(e => [...e.parentNode.children].filter(e => e.classList.contains('rs') && !e.classList.contains('ended')).map(e => e.dataset.rid));
}
async function create(page, label = '') {
  await showRail(page);
  await page.locator('#r-rail .rnew').first().click();
  if (label) await page.locator('.rpop:not([hidden]) .ns-label-in').fill(label);
  const response = page.waitForResponse(r => r.url().endsWith('/agents/leads') && r.request().method() === 'POST');
  const labelSave = label.trim()
    ? page.waitForResponse(r => /\/s\/[^/]+\/label$/.test(r.url()) && r.request().method() === 'POST')
    : null;
  await page.locator('.rpop:not([hidden]) .go').click();
  const r = await response;
  assert.equal(r.status(), 201);
  const { id } = await r.json();
  if (labelSave) assert.equal((await labelSave).status(), 200);
  await page.waitForURL(base + '/s/' + id);
  return id;
}
async function settleNoAutoRename(page, id) {
  await page.waitForFunction(
    rid => document.documentElement.getAttribute('data-rid') === rid && !!document.querySelector('#r-head .sname'),
    id
  );
  await page.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
  assert.equal(await page.locator('#r-rail .drinp, #r-head .sinp').count(), 0, 'new session does not enter rename mode');
  assert.equal(await page.evaluate(() => sessionStorage.getItem('ws.dashboard.renameNew')), null, 'no creation rename intent stored');
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    if (shots) fs.mkdirSync(shots, { recursive: true });
    const errors = [];
    for (const theme of ['light', 'dark']) {
      const ctx = await browser.newContext({ viewport: { width: 1680, height: 900 }, locale: 'ko-KR', colorScheme: theme, reducedMotion: 'reduce' });
      await ctx.addCookies([{ name: 'ws_dashboard_lang', value: 'ko', url: base }]);
      const page = await ctx.newPage();
      page.on('pageerror', e => { errors.push(theme + ': ' + e.message); console.error('PAGE ERROR:', e.message); });
      await page.goto(base + '/s/plainrow');
      await showRail(page);
      if (theme === 'light') {
        const stop = page.locator(row('plainrow') + ' .drstop');
        await page.locator(row('plainrow') + ' .rid').hover();
        await checkStaleSafeStop(page, stop);
      }
      if (theme === 'dark') {
        await page.route('**/agents/plainrow', route => route.fulfill({
          status: 200, json: { id: 'plainrow', can_control: true, process: true, safe_stop: true }
        }));
        await openMenu(page, 'plainrow');
        await checkStaleSafeStop(page, page.locator(menu + ' .rmsess .spact[data-safe]'));
        await page.unroute('**/agents/plainrow');
      }
      assert.equal(await page.locator('#r-rail button.star').count(), 0, 'no inline favorite button');
      assert.equal(await page.locator('#r-rail .drinp, #r-head .sinp').count(), 0, 'opening an existing session does not rename');
      // Saved ordering and grouping still come from the existing layout route.
      await toggle(page, 'agentsrow', true);
      await until(async () => (await order(page))[0] === 'agentsrow', 'favorite moves to the front');
      const badge = page.locator(row('agentsrow') + ' .hstar');
      await badge.waitFor();
      assert.equal(await badge.getAttribute('aria-label'), '즐겨찾기');
      assert.equal(await badge.getAttribute('title'), '즐겨찾기');
      assert.equal(await badge.locator('button').count(), 0, 'badge is display-only');
      const geometry = await badge.evaluate(e => {
        const b = e.getBoundingClientRect(), i = e.parentNode.querySelector('.hb').getBoundingClientRect();
        return [b.width, b.height, b.left - i.left, b.bottom - i.bottom];
      });
      assert.deepEqual(geometry, [10, 10, -4, 4], 'badge matches mockup bottom-left geometry');
      const gap = id => page.locator(row(id)).evaluate(e => e.querySelector('.rid').getBoundingClientRect().left - e.querySelector('.hb').getBoundingClientRect().right);
      assert.equal(await gap('plainrow'), 6, 'no-count gap equals mockup 6px');
      assert.equal(await gap('kidrow'), 6, 'child no-count gap equals mockup 6px');
      // 3996eb829: .sbn overlays the top-right corner of .hb as a robot icon instead of pushing .rid right.
      assert.equal(await gap('agentsrow'), 6, 'overlaid subagent robot icon keeps 6px gap');
      assert.equal(await gap('dotrow'), 6, 'overlaid subagent robot icon keeps 6px gap on two-digit count');
      assert.equal(await page.locator(row('agentsrow') + ' .sbn use').getAttribute('href'), '#i-robot');
      assert.equal(await page.locator(row('dotrow') + ' .sbn use').getAttribute('href'), '#i-robot');
      await openMenu(page, 'agentsrow');
      assert.equal(await page.locator(menu).getByRole('menuitem', { name: '즐겨찾기 해제', exact: true }).count(), 1);
      await page.keyboard.press('Escape');
      // A page refresh keeps the favorite but never starts rename.
      await page.reload();
      await showRail(page);
      await badge.waitFor();
      assert.equal(await page.locator('.drinp, #r-head .sinp').count(), 0);
      // Overlay widths: all four favorite/count combinations (including
      // the child with neither), and the open context menu.
      await toggle(page, 'plainrow', true);
      await until(async () => (await order(page))[0] === 'plainrow', 'favorite without count leads the screenshot rows');
      for (const width of [1280, 760]) {
        await page.setViewportSize({ width, height: 900 });
        await showRail(page);
        await page.mouse.move(width - 5, 895);
        await shot(page, `rows-${theme}-${width}`);
        await openMenu(page, 'agentsrow');
        await shot(page, `menu-${theme}-${width}`);
        await page.keyboard.press('Escape');
      }
      await toggle(page, 'plainrow', false);
      await until(async () => (await order(page))[0] === 'agentsrow', 'snapshot favorite cleared');
      // Child and archived rows retain their original favorite restrictions.
      await openMenu(page, 'kidrow');
      assert.equal(await page.locator(menu + ' .rmstar').count(), 0, 'no child favorite item');
      await page.keyboard.press('Escape');
      assert.equal(await page.locator(row('archivedrow')).getAttribute('data-star'), null, 'archived rows cannot favorite');
      await toggle(page, 'agentsrow', false, true);
      await until(async () => await badge.count() === 0, 'unfavorite removes badge');
      await until(async () => (await order(page))[0] === 'plainrow', 'unfavorite restores normal ordering');
      // Refusal: badge appears optimistically and is then rolled back.
      let release;
      await page.route('**/rail/layout', route => new Promise(resolve => {
        release = async () => { await route.fulfill({ status: 409, contentType: 'application/json', body: '{"applied":false,"reason":"store"}' }); resolve(); };
      }));
      await toggle(page, 'agentsrow', true);
      await until(async () => typeof release === 'function', 'favorite POST in flight');
      assert.equal(await badge.count(), 1, 'optimistic badge');
      await release();
      await until(async () => await badge.count() === 0, 'failed favorite restores badge');
      await until(async () => await page.locator(row('agentsrow')).getAttribute('data-star') === '0', 'failed favorite restores row state');
      await page.unroute('**/rail/layout');
      // Wide rail: new row at the end, no auto-rename for blank or labeled
      // creation, and manual rename (.drren) remains intact.
      await page.setViewportSize({ width: 1680, height: 900 });
      let id = await create(page);
      await settleNoAutoRename(page, id);
      await page.locator(row(id)).waitFor();
      assert.equal((await order(page)).at(-1), id, 'new row appended at group end');
      assert.equal(await page.locator(row(id) + ' .rid').textContent(), id, 'blank label keeps reply-id');
      assert.equal(await page.locator(row(id) + ' .drren').getAttribute('data-label'), '', 'blank creation stores no label');
      // Another tab sees the session and also does not enter rename mode.
      const peer = await ctx.newPage();
      await peer.goto(base + '/s/' + id);
      await peer.locator('#r-head .sname').waitFor();
      assert.equal(await peer.locator('.drinp, #r-head .sinp').count(), 0, 'sessions created elsewhere do not rename');
      await peer.close();
      // Creating with a provided label also skips auto-rename and renders the label.
      id = await create(page, '모달 라벨');
      await settleNoAutoRename(page, id);
      await page.locator(row(id)).waitFor();
      assert.equal(await page.locator(row(id) + ' .rid').textContent(), '모달 라벨', 'provided label renders in rail');
      assert.equal(await page.locator('#r-head .snm').textContent(), '모달 라벨', 'provided label renders in header');
      // Manual rail rename (.drren) still opens, focuses, saves, and selects full existing label.
      await page.locator(row(id) + ' .rid').hover();
      await page.locator(row(id) + ' .drren').click();
      let input = page.locator(row(id) + ' .drinp');
      await input.waitFor();
      await until(() => input.evaluate(e => e === document.activeElement), 'manual rail rename focuses input');
      await input.fill('selected name');
      await input.press('Enter');
      await until(async () => await page.locator(row(id) + ' .rid').textContent() === 'selected name', 'rename saved');
      await page.locator(row(id) + ' .rid').hover();
      await page.locator(row(id) + ' .drren').click();
      input = page.locator(row(id) + ' .drinp');
      await input.waitFor();
      assert.deepEqual(await input.evaluate(e => [e.selectionStart, e.selectionEnd]), [0, 'selected name'.length], 'existing label fully selected');
      await input.press('Escape');
      await until(async () => await page.locator(row(id) + ' .drinp').count() === 0, 'Escape closes manual rail rename');
      // Collapsed rail: blank or labeled creation keeps rail closed and does not
      // enter header rename; manual header rename (.sren) still works.
      for (const width of [1280, 760]) {
        await page.setViewportSize({ width, height: 900 });
        id = await create(page);
        await settleNoAutoRename(page, id);
        assert.equal(await page.locator(row(id)).isVisible(), false, 'collapsed rail stays closed');
        assert.equal(await page.locator('#r-head .snm').textContent(), id, 'blank label keeps reply-id in header');
        id = await create(page, '접힌 라벨');
        await settleNoAutoRename(page, id);
        assert.equal(await page.locator(row(id)).isVisible(), false, 'collapsed rail stays closed');
        assert.equal(await page.locator('#r-head .snm').textContent(), '접힌 라벨', 'provided label shows in header');
        await page.locator('#r-head .sren').click();
        input = page.locator('#r-head .sinp');
        await input.waitFor();
        await until(() => input.evaluate(e => e === document.activeElement), 'manual header rename focuses input');
        if (width === 1280) {
          await input.fill('저장한 세션');
          const saved = page.waitForResponse(r => r.url().endsWith('/s/' + id + '/label') && r.request().method() === 'POST');
          await input.press('Enter');
          assert.equal((await saved).status(), 200);
          await until(async () => await page.locator('#r-head .snm').textContent() === '저장한 세션', 'header rename saved');
        } else {
          await input.fill('discard on escape');
          await input.press('Escape');
          assert.equal(await page.locator('#r-head .snm').textContent(), '접힌 라벨', 'Escape keeps prior label');
        }
        await page.reload();
        await page.locator('#r-head .sname').waitFor();
        assert.equal(await page.locator('.drinp, #r-head .sinp').count(), 0, 'reload stays out of rename mode');
      }
      await ctx.close();
    }
    assert.deepEqual(errors, [], 'no browser page errors');
    console.log('PASS: favorite menu/badge/persistence/rollback, 6px count-free spacing, no auto-rename after create (blank/labeled, wide/header), manual rename intact');
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exitCode = 1; });
