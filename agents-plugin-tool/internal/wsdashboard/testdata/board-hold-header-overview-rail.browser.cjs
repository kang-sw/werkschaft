// Run by TestBoardHoldHeaderOverviewRailBrowser against a live isolated daemon.
'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [base, shots] = process.argv.slice(2);

async function settle(target, ms = 120) {
  await target.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
  if (ms > 0) await new Promise(r => setTimeout(r, ms));
}

async function activeFrame(tab, rid) {
  await tab.waitForFunction(rid => {
    const f = document.querySelector('iframe.sframe.on');
    return !!(f && f.contentDocument?.documentElement.dataset.rid === rid && f.contentWindow?.wbPill && f.contentWindow?.WsOverview);
  }, rid);
  return (await tab.locator('iframe.sframe.on').elementHandle()).contentFrame();
}

async function readTbar(frame, selector) {
  return frame.evaluate(sel => {
    const tbar = document.querySelector(sel);
    if (!tbar) return null;
    const chip = k => {
      const el = tbar.querySelector(`[data-tbar-ask="${k}"]`);
      if (!el) return null;
      const r = el.getBoundingClientRect();
      const cs = getComputedStyle(el);
      return {
        hidden: el.hidden || cs.display === 'none',
        n: (el.querySelector('.n')?.textContent || '').trim(),
        lbl: (el.querySelector('.chip-lbl')?.textContent || '').trim(),
        w: r.width,
        h: r.height,
        bg: cs.backgroundColor,
        color: cs.color,
        bulk: el.getAttribute('data-bulk') || ''
      };
    };
    const ok = tbar.querySelector('.chip.ok');
    return {
      open: chip('open'),
      ans: chip('ans'),
      held: chip('held'),
      ok: ok ? { hidden: ok.hidden, text: ok.textContent.trim(), bulk: ok.getAttribute('data-bulk') || '' } : null
    };
  }, selector);
}

async function runTheme(browser, theme) {
  const context = await browser.newContext({ viewport: { width: 2240, height: 960 }, colorScheme: theme, reducedMotion: 'reduce' });
  context.setDefaultTimeout(12000);
  await context.addInitScript(() => {
    if (self.origin === 'null') return;
    const workers = navigator.serviceWorker;
    if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
    // Instrument localStorage to prove zero ws.wb.holds.* reads or writes.
    window.__wsHoldStorageCalls = [];
    const proto = Storage.prototype;
    for (const fn of ['getItem', 'setItem', 'removeItem']) {
      const orig = proto[fn];
      proto[fn] = function(k) {
        if (this === window.localStorage && String(k).indexOf('ws.wb.holds.') === 0) {
          window.__wsHoldStorageCalls.push([fn, String(k)]);
        }
        return orig.apply(this, arguments);
      };
    }
  });
  await context.route('**/*', route => {
    const u = new URL(route.request().url());
    if (u.origin !== base) return route.abort();
    if (u.pathname === '/favicon.ico') return route.fulfill({ status: 204 });
    if (u.pathname === '/rail/usage') return route.fulfill({ json: { entries: [], warnPercent: 80, critPercent: 95 } });
    return route.continue();
  });

  const tab = await context.newPage();
  const errors = [];
  tab.on('pageerror', e => errors.push(`shell pageerror: ${e.message}`));
  tab.on('console', msg => {
    if (msg.type() === 'error' && !/status of 404/.test(msg.text())) {
      errors.push(`shell console.error: ${msg.text()}`);
    }
  });

  await tab.goto(base + '/s/sessone');
  let frame = await activeFrame(tab, 'sessone');
  await settle(frame, 180);

  // Check initial rail state: both sessone and sesstwo have visible .qdot, rail header policy chip = 6 (4 + 1 + 1).
  const initialRail = await tab.evaluate(() => ({
    sessoneDot: !!document.querySelector('.rs[data-rid="sessone"] .qdot'),
    sesstwoDot: !!document.querySelector('.rs[data-rid="sesstwo"] .qdot'),
    headerPolicy: (document.querySelector('.rhd .chip.policy')?.textContent || '').trim()
  }));
  assert.equal(initialRail.sessoneDot, true, `${theme}: initial sessone .qdot visible`);
  assert.equal(initialRail.sesstwoDot, true, `${theme}: initial sesstwo .qdot visible`);
  assert.match(initialRail.headerPolicy, /6$/, `${theme}: rail header counts 6 unheld asks (${initialRail.headerPolicy})`);

  // 1) Initial board .tbar on topic "design": open=4, ans hidden, held hidden, ack chip=1.
  const designTbarSel = '#bcol .topic[data-topic="design"] > .tophead > .tbar';
  let tbar = await readTbar(frame, designTbarSel);
  assert.equal(tbar.open.hidden, false, `${theme}: initial open chip visible`);
  assert.equal(tbar.open.n, '4', `${theme}: initial open count = 4`);
  assert.equal(tbar.open.bulk, 'policy', `${theme}: bulk action stays on open chip`);
  assert.equal(tbar.ans.hidden, true, `${theme}: initial answered chip hidden`);
  assert.equal(tbar.held.hidden, true, `${theme}: initial held chip hidden`);
  assert.equal(tbar.ok.bulk, 'ack', `${theme}: ack bulk chip unchanged`);

  // 2) Hold item-1 (policy) -> open=3, held=1; rail header drops from 6 to 5 via SSE.
  await frame.locator('#bcol #item-1 .wb-hold-toggle').click();
  await frame.waitForFunction(sel => {
    const h = document.querySelector(`${sel} [data-tbar-ask="held"]`);
    return h && !h.hidden && h.querySelector('.n')?.textContent.trim() === '1';
  }, designTbarSel);
  await tab.waitForFunction(() => /5$/.test((document.querySelector('.rhd .chip.policy')?.textContent || '').trim()));

  // 3) Stage option a on item-2 (policy) -> open=2, ans=1, held=1 (all 3 chips simultaneously visible!).
  await frame.locator('#bcol #item-2 .opt[data-opt="a"]').click();
  await frame.waitForFunction(sel => {
    const a = document.querySelector(`${sel} [data-tbar-ask="ans"]`);
    return a && !a.hidden && a.querySelector('.n')?.textContent.trim() === '1';
  }, designTbarSel);
  await settle(frame, 120);

  tbar = await readTbar(frame, designTbarSel);
  assert.equal(tbar.open.hidden, false, `${theme}: open chip visible in 3-way split`);
  assert.equal(tbar.open.n, '2', `${theme}: open=2 in 3-way split`);
  assert.equal(tbar.ans.hidden, false, `${theme}: answered chip visible in 3-way split`);
  assert.equal(tbar.ans.n, '1', `${theme}: ans=1 in 3-way split`);
  assert.equal(tbar.held.hidden, false, `${theme}: held chip visible in 3-way split`);
  assert.equal(tbar.held.n, '1', `${theme}: held=1 in 3-way split`);
  assert.ok(tbar.open.w > 20 && tbar.open.h > 12, `${theme}: open chip rendered (${tbar.open.w}x${tbar.open.h})`);
  assert.ok(tbar.ans.w > 20 && tbar.ans.h > 12, `${theme}: ans chip rendered (${tbar.ans.w}x${tbar.ans.h})`);
  assert.ok(tbar.held.w > 20 && tbar.held.h > 12, `${theme}: held chip rendered (${tbar.held.w}x${tbar.held.h})`);
  assert.notEqual(tbar.open.bg, tbar.ans.bg, `${theme}: open and answered chips have distinct backgrounds`);
  assert.notEqual(tbar.open.bg, tbar.held.bg, `${theme}: open and held chips have distinct backgrounds`);
  assert.notEqual(tbar.ans.bg, tbar.held.bg, `${theme}: answered and held chips have distinct backgrounds`);

  if (shots) {
    fs.mkdirSync(shots, { recursive: true });
    await tab.screenshot({ path: path.join(shots, `board-tbar-split-${theme}.png`) });
  }

  // 4) Bulk quick-pick on open-ask chip stages item-3 (open policy) and skips item-1 (held policy).
  // Remaining open ask is item-4 (check), so open=1, ans=2, held=1.
  await frame.locator(`${designTbarSel} [data-tbar-ask="open"]`).click();
  await frame.waitForFunction(sel => {
    const o = document.querySelector(`${sel} [data-tbar-ask="open"] .n`);
    const a = document.querySelector(`${sel} [data-tbar-ask="ans"] .n`);
    const h = document.querySelector(`${sel} [data-tbar-ask="held"] .n`);
    return o?.textContent.trim() === '1' && a?.textContent.trim() === '2' && h?.textContent.trim() === '1';
  }, designTbarSel);
  const item1StillHeld = await frame.evaluate(() => document.getElementById('item-1').classList.contains('wb-held'));
  assert.equal(item1StillHeld, true, `${theme}: bulk quick-pick skips held policy card`);

  // 5) Open Overview -> verify Overview tile / Quick Look placeholder mirrors open=1, ans=2, held=1, ack=1,
  // and updates live when unholding inside the Quick Look preview!
  await frame.locator('.board-overview').click();
  await frame.waitForFunction(() => document.documentElement.hasAttribute('data-overview'));
  await settle(frame, 180);

  const phTbarSel = '#bcol .overview-placeholder[data-overview-target-topic="design"] > .tophead > .tbar';
  const alreadyPreviewed = await frame.locator(phTbarSel).count();
  if (!alreadyPreviewed) {
    let ovTile = await readTbar(frame, designTbarSel);
    assert.ok(ovTile, `${theme}: Overview tile rendered for design`);
    assert.equal(ovTile.open.n, '1', `${theme}: Overview tile open=1`);
    assert.equal(ovTile.ans.n, '2', `${theme}: Overview tile ans=2`);
    assert.equal(ovTile.held.n, '1', `${theme}: Overview tile held=1`);
    assert.equal(ovTile.ok.bulk, 'ack', `${theme}: Overview tile preserves ack chip`);
    await frame.locator('#bcol .topic[data-topic="design"]').hover();
  }
  await frame.waitForSelector(phTbarSel, { state: 'visible' });
  await frame.waitForSelector('#bgal .overview-preview #item-1 .wb-hold-toggle', { state: 'visible' });
  await settle(frame, 120);

  let phTbar = await readTbar(frame, phTbarSel);
  assert.ok(phTbar, `${theme}: Overview placeholder rendered for design`);
  assert.equal(phTbar.open.hidden, false, `${theme}: Overview placeholder open visible`);
  assert.equal(phTbar.open.n, '1', `${theme}: Overview placeholder open=1`);
  assert.equal(phTbar.ans.hidden, false, `${theme}: Overview placeholder ans visible`);
  assert.equal(phTbar.ans.n, '2', `${theme}: Overview placeholder ans=2`);
  assert.equal(phTbar.held.hidden, false, `${theme}: Overview placeholder held visible`);
  assert.equal(phTbar.held.n, '1', `${theme}: Overview placeholder held=1`);
  assert.equal(phTbar.ok.bulk, 'ack', `${theme}: Overview placeholder preserves ack chip`);

  if (shots) {
    await tab.screenshot({ path: path.join(shots, `overview-placeholder-${theme}.png`) });
  }

  // Unhold item-1 inside Quick Look preview -> both preview and Overview placeholder update live!
  await frame.locator('#bgal .overview-preview #item-1 .wb-hold-toggle').click();
  await frame.waitForFunction(sel => {
    const o = document.querySelector(`${sel} [data-tbar-ask="open"] .n`);
    const h = document.querySelector(`${sel} [data-tbar-ask="held"]`);
    return o?.textContent.trim() === '2' && h && h.hidden;
  }, phTbarSel);
  await settle(frame, 120);

  // Close Overview.
  await frame.locator('.board-overview').click();
  await frame.waitForFunction(() => !document.documentElement.hasAttribute('data-overview'));

  // 6) Switch to sesstwo via left rail, hold its sole ask -> rail .qdot hides, data-asks stays "1",
  // persists across switching back to sessone and returning to sesstwo, then unhold restores .qdot.
  await tab.locator('.rs[data-rid="sesstwo"] .rl').click();
  const frame2 = await activeFrame(tab, 'sesstwo');
  await settle(frame2, 150);

  await frame2.locator('#bcol #item-1 .wb-hold-toggle').click();
  await frame2.waitForFunction(() => {
    const tbar = document.querySelector('#bcol .topic[data-topic="triage"] > .tophead > .tbar');
    const o = tbar?.querySelector('[data-tbar-ask="open"]');
    const h = tbar?.querySelector('[data-tbar-ask="held"]');
    return o?.hidden && h && !h.hidden && h.querySelector('.n')?.textContent.trim() === '1';
  });
  await tab.waitForFunction(() => {
    const row = document.querySelector('.rs[data-rid="sesstwo"]');
    return row && row.getAttribute('data-asks') === '1' && !row.querySelector('.qdot');
  });

  // Switch back to sessone and confirm sesstwo stays without .qdot while sessone keeps its .qdot.
  await tab.locator('.rs[data-rid="sessone"] .rl').click();
  frame = await activeFrame(tab, 'sessone');
  await settle(frame, 120);
  const afterSwitchBack = await tab.evaluate(() => ({
    sessoneDot: !!document.querySelector('.rs[data-rid="sessone"] .qdot'),
    sesstwoDot: !!document.querySelector('.rs[data-rid="sesstwo"] .qdot'),
    sesstwoAsks: document.querySelector('.rs[data-rid="sesstwo"]')?.getAttribute('data-asks')
  }));
  assert.equal(afterSwitchBack.sessoneDot, true, `${theme}: sessone keeps .qdot after switching back`);
  assert.equal(afterSwitchBack.sesstwoDot, false, `${theme}: sesstwo .qdot stays hidden while inactive`);
  assert.equal(afterSwitchBack.sesstwoAsks, '1', `${theme}: sesstwo data-asks preserved`);

  // Switch to sesstwo again and unhold -> .qdot returns cleanly for next theme run.
  await tab.locator('.rs[data-rid="sesstwo"] .rl').click();
  const frame2Again = await activeFrame(tab, 'sesstwo');
  await frame2Again.locator('#bcol #item-1 .wb-hold-toggle').click();
  await tab.waitForFunction(() => !!document.querySelector('.rs[data-rid="sesstwo"] .qdot'));

  // Zero localStorage hold reads or writes across both shell and session frames.
  const shellCalls = await tab.evaluate(() => window.__wsHoldStorageCalls || []);
  const frameCalls = await frame2Again.evaluate(() => window.__wsHoldStorageCalls || []);
  assert.deepEqual(shellCalls, [], `${theme}: shell made no ws.wb.holds.* localStorage calls`);
  assert.deepEqual(frameCalls, [], `${theme}: session frame made no ws.wb.holds.* localStorage calls`);
  assert.deepEqual(errors, [], `${theme}: expected zero page/console errors`);

  console.log(`${theme}: board header 3-way split, bulk skip-held, Overview placeholder live sync, rail .qdot across session switches, and zero localStorage hold access passed.`);
  await context.close();
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    await runTheme(browser, 'dark');
    await runTheme(browser, 'light');
  } finally {
    await browser.close();
  }
})().catch(err => {
  console.error(err);
  process.exit(1);
});
