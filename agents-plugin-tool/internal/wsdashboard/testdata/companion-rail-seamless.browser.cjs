// Run by TestCompanionRailSeamlessOverlayBrowser against live sparse & rich
// Werkschaft dashboard fixtures with VRM 0.x and VRM 1.0 models.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [sparseBase, richBase, shots, vrm0ID, vrm1ID] = process.argv.slice(2);

async function composeGrid(browser, outPath, title, panels, opts = {}) {
  if (!outPath) return;
  fs.mkdirSync(path.dirname(outPath), { recursive: true });
  const cols = opts.cols || panels.length;
  const page = await browser.newPage({ viewport: { width: 2200, height: 1400 }, deviceScaleFactor: 2 });
  const imgData = panels.map(p => ({
    label: p.label,
    src: 'data:image/png;base64,' + p.buf.toString('base64'),
    maxW: p.maxW || 0
  }));
  await page.setContent(`<!doctype html><html><head><style>
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body { background: #0f1116; color: #e8ecf4; font-family: -apple-system, BlinkMacSystemFont, "SF Pro Text", "Apple SD Gothic Neo", sans-serif; padding: 18px; display: inline-block; }
    h1 { font-size: 15px; font-weight: 700; margin-bottom: 12px; color: #c7d2fe; }
    .grid { display: grid; grid-template-columns: repeat(${cols}, auto); gap: 14px; align-items: start; }
    .card { background: #171a22; border: 1px solid #2b3140; border-radius: 10px; overflow: hidden; display: inline-flex; flex-direction: column; }
    .cap { padding: 7px 11px; font-size: 12px; font-weight: 600; color: #d8dee9; border-bottom: 1px solid #262c3a; background: #1b1f29; }
    .card img { display: block; height: auto; }
  </style></head><body><h1>${title}</h1><div class="grid" id="g"></div></body></html>`);
  await page.evaluate(async items => {
    const g = document.getElementById('g');
    for (const it of items) {
      const card = document.createElement('div');
      card.className = 'card';
      const cap = document.createElement('div');
      cap.className = 'cap';
      cap.textContent = it.label;
      const img = document.createElement('img');
      if (it.maxW) img.style.width = it.maxW + 'px';
      await new Promise((res, rej) => { img.onload = res; img.onerror = rej; img.src = it.src; });
      card.append(cap, img);
      g.append(card);
    }
  }, imgData);
  const bodyBox = await page.locator('body').boundingBox();
  await page.setViewportSize({ width: Math.ceil(bodyBox.width), height: Math.ceil(bodyBox.height) });
  await page.screenshot({ path: outPath, fullPage: true });
  await page.close();
}

async function openDashboardPage(browser, baseUrl, consoleErrors, pageErrors) {
  const context = await browser.newContext({
    viewport: { width: 1680, height: 960 },
    deviceScaleFactor: 2,
    colorScheme: 'dark',
    locale: 'ko-KR',
    extraHTTPHeaders: { 'Accept-Language': 'ko-KR,ko;q=0.9' }
  });
  await context.addInitScript(() => {
    if (self !== top) return;
    const workers = navigator.serviceWorker;
    if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
  });
  const page = await context.newPage();
  // startPageFixture runs without a tracker store; fulfill /tracker/stores so
  // unrelated tracker polling does not mask real 404s in consoleErrors.
  await page.route('**/tracker/stores', route => route.fulfill({ json: { stores: [], rev: 0 } }));
  page.on('pageerror', e => pageErrors.push(e.message));
  page.on('console', msg => {
    if (msg.type() === 'error') consoleErrors.push(msg.text());
  });
  await page.goto(baseUrl + '/s/alpha01');
  await page.frameLocator('.sframe.on').locator('#grid').waitFor();
  return { context, page };
}

async function setTheme(page, theme) {
  await page.emulateMedia({ colorScheme: theme });
  await page.evaluate(t => { document.documentElement.dataset.theme = t; }, theme);
  await page.waitForTimeout(220);
}

async function applyPack(page, packId) {
  await page.evaluate(id => {
    document.dispatchEvent(new CustomEvent('ws-companion-config', {
      detail: { enabled: true, director: 'rules', pack: id, preset: '' }
    }));
  }, packId);
  await page.waitForFunction(id => {
    const st = window.WsCompanion?.debugStage?.();
    return st && st.renderer === 'vrm' && st.pack === id && st.vrm?.loaded && st.vrm.framesRendered >= 6;
  }, packId, { timeout: 30000 });
  await page.waitForTimeout(280);
}

async function railFullClip(page) {
  return page.evaluate(() => {
    const r = document.getElementById('rail').getBoundingClientRect();
    return { x: Math.max(0, Math.floor(r.left - 8)), y: 0, width: Math.ceil(r.width + 18), height: window.innerHeight };
  });
}

async function railBottomWideClip(page, extraRight = 460) {
  return page.evaluate(extra => {
    const rail = document.getElementById('rail').getBoundingClientRect();
    const top = Math.max(0, Math.floor(window.innerHeight - 620));
    return {
      x: Math.max(0, Math.floor(rail.left - 8)),
      y: top,
      width: Math.min(window.innerWidth, Math.ceil(rail.width + extra)),
      height: Math.min(window.innerHeight - top, Math.ceil(window.innerHeight - top))
    };
  }, extraRight);
}

(async () => {
  const browser = await chromium.launch({
    headless: true,
    args: ['--use-gl=angle', '--use-angle=swiftshader', '--enable-unsafe-swiftshader', '--ignore-gpu-blocklist']
  });
  const consoleErrors = [];
  const pageErrors = [];
  const summary = {};
  try {
    // =========================================================================
    // (a) SPARSE RAIL — Dark & Light × VRM 1.0 & VRM 0.x
    //     Verify details.rws-unreg hugs registered workspaces at the top with
    //     ZERO mid-rail void.
    // =========================================================================
    const sparse = await openDashboardPage(browser, sparseBase, consoleErrors, pageErrors);
    const sparsePanels = [];
    for (const [label, packId] of [['VRM 1.0', vrm1ID], ['VRM 0.x', vrm0ID]]) {
      await applyPack(sparse.page, packId);
      for (const theme of ['dark', 'light']) {
        await setTheme(sparse.page, theme);
        const m = await sparse.page.evaluate(() => {
          const rlist = document.getElementById('rlist');
          const wsList = Array.from(rlist.querySelectorAll(':scope > section.rws'));
          const lastWs = wsList[wsList.length - 1];
          const unreg = rlist.querySelector(':scope > details.rws-unreg');
          const slot = document.getElementById('rail-companion-slot');
          const wr = lastWs.getBoundingClientRect();
          const ur = unreg.getBoundingClientRect();
          const sr = slot.getBoundingClientRect();
          return {
            unregInsideRlist: rlist.contains(unreg),
            gapPx: Math.round(ur.top - wr.bottom),
            clearAboveSlotPx: Math.round(sr.top - ur.bottom),
            slotTop: Math.round(sr.top)
          };
        });
        assert.equal(m.unregInsideRlist, true, `${label} ${theme}: details.rws-unreg must live inside #rlist`);
        assert.ok(m.gapPx >= 0 && m.gapPx <= 20, `${label} ${theme}: sparse unreg must hug top workspaces with no void, got gap=${m.gapPx}px`);
        assert.ok(m.clearAboveSlotPx > 120, `${label} ${theme}: sparse unreg should sit high above Companion slot, got clear=${m.clearAboveSlotPx}px`);
        if (shots) {
          const buf = await sparse.page.screenshot({ clip: await railFullClip(sparse.page) });
          sparsePanels.push({ label: `${label} · ${theme.toUpperCase()} (gap=${m.gapPx}px above unreg)`, buf, maxW: 360 });
        }
      }
    }
    if (shots) {
      await composeGrid(browser, path.join(shots, '01-sparse-rail-dark-light.png'),
        '01 · (a) Sparse Rail — Top-Aligned Unregistered Workspaces (Zero Mid-Rail Void) · VRM 1.0 & VRM 0.x · Dark & Light',
        sparsePanels, { cols: 4 });
    }
    await sparse.context.close();

    // =========================================================================
    // (b), (c), (d) RICH SCROLLABLE RAIL — Dark & Light × VRM 1.0 & VRM 0.x
    // =========================================================================
    const rich = await openDashboardPage(browser, richBase, consoleErrors, pageErrors);
    const midOverlapPanels = [];
    const maxBottomPanels = [];
    const laptopMessengerPanels = [];

    for (const [label, packId] of [['VRM 1.0', vrm1ID], ['VRM 0.x', vrm0ID]]) {
      await applyPack(rich.page, packId);
      for (const theme of ['dark', 'light']) {
        await setTheme(rich.page, theme);

        // --- (b) Mid-scroll overlap + transparent pass-through + wheel + gaze ---
        await rich.page.evaluate(() => {
          const unreg = document.querySelector('#rail details.rws-unreg');
          if (unreg) unreg.open = false;
          const rlist = document.getElementById('rlist');
          rlist.scrollTop = Math.floor((rlist.scrollHeight - rlist.clientHeight) * 0.35);
        });
        await rich.page.waitForTimeout(180);

        const overlapCheck = await rich.page.evaluate(() => {
          const rlist = document.getElementById('rlist');
          const slot = document.getElementById('rail-companion-slot').getBoundingClientRect();
          const laptop = document.querySelector('.ws-companion-laptop-hit').getBoundingClientRect();
          const rows = Array.from(rlist.querySelectorAll(':scope > section.rws .rs'));
          // Find a row whose vertical band intersects the transparent upper companion region (above the laptop).
          const upperLimit = Math.min(laptop.top - 8, slot.top + slot.height * 0.65);
          const behindRows = rows.filter(el => {
            const r = el.getBoundingClientRect();
            return r.height > 0 && r.bottom > slot.top + 8 && r.top < upperLimit;
          });
          const targetRow = behindRows[0] || null;
          let hitTag = '', hitOk = false, hitComp = false, probePt = null, targetRid = '';
          if (targetRow) {
            const tr = targetRow.getBoundingClientRect();
            const px = Math.floor(tr.left + 32);
            const py = Math.floor(Math.max(slot.top + 10, Math.min(upperLimit - 4, tr.top + tr.height * 0.5)));
            probePt = { x: px, y: py };
            const hitEl = document.elementFromPoint(px, py);
            hitTag = hitEl?.tagName || '';
            hitComp = !!(hitEl && hitEl.closest('.ws-companion'));
            hitOk = !!(hitEl && targetRow.contains(hitEl));
            targetRid = targetRow.dataset.rid || '';
          }
          return {
            overflowPx: rlist.scrollHeight - rlist.clientHeight,
            scrollTop: rlist.scrollTop,
            behindCount: behindRows.length,
            targetRid,
            probePt,
            hitTag,
            hitOk,
            hitComp,
            slotCenterX: Math.floor(slot.left + slot.width * 0.5),
            slotUpperY: Math.floor(slot.top + 42)
          };
        });
        assert.ok(overlapCheck.overflowPx > 260, `${label} ${theme}: rich rail must overflow substantially, got ${overlapCheck.overflowPx}px`);
        assert.ok(overlapCheck.behindCount >= 2, `${label} ${theme}: expected >=2 rows visibly behind Companion head/shoulders, got ${overlapCheck.behindCount}`);
        assert.equal(overlapCheck.hitComp, false, `${label} ${theme}: transparent area beside/above head must not be intercepted by Companion`);
        assert.equal(overlapCheck.hitOk, true, `${label} ${theme}: elementFromPoint over row behind Companion must hit row ${overlapCheck.targetRid}: ${JSON.stringify(overlapCheck)}`);

        if (shots) {
          const buf = await rich.page.screenshot({ clip: await railFullClip(rich.page) });
          midOverlapPanels.push({
            label: `${label} · ${theme.toUpperCase()} — Mid-Scroll (${overlapCheck.behindCount} rows behind head, ${overlapCheck.targetRid} clickable)`,
            buf,
            maxW: 360
          });
        }

        // Actually click the passed-through row beside/above Companion's head and confirm it works!
        await rich.page.mouse.click(overlapCheck.probePt.x, overlapCheck.probePt.y);
        await rich.page.waitForTimeout(180);
        const clickedActive = await rich.page.evaluate(rid => {
          const cur = document.querySelector('#rlist .rs.cur');
          return cur ? cur.dataset.rid : '';
        }, overlapCheck.targetRid);
        assert.equal(clickedActive, overlapCheck.targetRid,
          `${label} ${theme}: clicking passed-through row ${overlapCheck.targetRid} behind Companion should activate it, got ${clickedActive}`);

        // Verify wheel scrolling directly over the Companion scrolls #rlist down and up.
        const beforeWheel = await rich.page.evaluate(() => document.getElementById('rlist').scrollTop);
        await rich.page.mouse.move(overlapCheck.slotCenterX, overlapCheck.slotUpperY);
        await rich.page.mouse.wheel(0, 180);
        await rich.page.waitForFunction(prev => document.getElementById('rlist').scrollTop > prev, beforeWheel, { timeout: 3000 });
        await rich.page.waitForTimeout(180);
        const afterWheelDown = await rich.page.evaluate(() => document.getElementById('rlist').scrollTop);
        assert.ok(afterWheelDown > beforeWheel, `${label} ${theme}: wheel down over Companion must scroll #rlist (${beforeWheel} -> ${afterWheelDown})`);
        await rich.page.mouse.wheel(0, -240);
        await rich.page.waitForFunction(prev => document.getElementById('rlist').scrollTop < prev, afterWheelDown, { timeout: 3000 });
        const afterWheelUp = await rich.page.evaluate(() => document.getElementById('rlist').scrollTop);
        assert.ok(afterWheelUp < afterWheelDown, `${label} ${theme}: wheel up over Companion must scroll #rlist back (${afterWheelDown} -> ${afterWheelUp})`);

        // Verify gaze tracking follows document pointermove across the dashboard despite pointer-events:none canvas.
        await rich.page.mouse.move(1480, 180);
        await rich.page.waitForTimeout(140);
        const gazeRight = await rich.page.evaluate(() => window.WsCompanion?.debugStage?.()?.vrm?.gaze || null);
        await rich.page.mouse.move(40, 880);
        await rich.page.waitForTimeout(140);
        const gazeLeft = await rich.page.evaluate(() => window.WsCompanion?.debugStage?.()?.vrm?.gaze || null);
        if (gazeRight && gazeLeft && Number.isFinite(gazeRight.x) && Number.isFinite(gazeLeft.x)) {
          assert.ok(gazeRight.x > gazeLeft.x, `${label} ${theme}: gaze must track pointer across dashboard (${JSON.stringify(gazeLeft)} -> ${JSON.stringify(gazeRight)})`);
        }

        // --- (c) Max-bottom scroll: collapsed & expanded details.rws-unreg clear Companion's head ---
        await rich.page.evaluate(() => {
          const unreg = document.querySelector('#rail details.rws-unreg');
          unreg.open = false;
          const rlist = document.getElementById('rlist');
          rlist.scrollTop = rlist.scrollHeight;
        });
        await rich.page.waitForTimeout(140);
        const maxCollapsed = await rich.page.evaluate(() => {
          const ur = document.querySelector('#rail details.rws-unreg').getBoundingClientRect();
          const sr = document.getElementById('rail-companion-slot').getBoundingClientRect();
          const rlist = document.getElementById('rlist');
          return {
            clearancePx: Math.round(sr.top - ur.bottom),
            padBottom: parseFloat(getComputedStyle(rlist).paddingBottom) || 0,
            slotH: Math.round(sr.height)
          };
        });
        assert.ok(maxCollapsed.clearancePx >= 16,
          `${label} ${theme}: max-bottom scroll (collapsed) must clear details.rws-unreg above Companion head: ${JSON.stringify(maxCollapsed)}`);

        // Expand details.rws-unreg and scroll to max bottom again.
        await rich.page.locator('#rail details.rws-unreg > summary').click();
        await rich.page.waitForTimeout(200);
        await rich.page.evaluate(() => {
          const rlist = document.getElementById('rlist');
          rlist.scrollTop = rlist.scrollHeight;
        });
        await rich.page.waitForTimeout(160);
        const maxExpanded = await rich.page.evaluate(() => {
          const ur = document.querySelector('#rail details.rws-unreg').getBoundingClientRect();
          const sr = document.getElementById('rail-companion-slot').getBoundingClientRect();
          return {
            open: document.querySelector('#rail details.rws-unreg').open,
            clearancePx: Math.round(sr.top - ur.bottom)
          };
        });
        assert.equal(maxExpanded.open, true);
        assert.ok(maxExpanded.clearancePx >= 16,
          `${label} ${theme}: max-bottom scroll (expanded) must clear all pending rows above Companion head: ${JSON.stringify(maxExpanded)}`);

        if (shots) {
          const buf = await rich.page.screenshot({ clip: await railFullClip(rich.page) });
          maxBottomPanels.push({
            label: `${label} · ${theme.toUpperCase()} — Max-Bottom Expanded (+${maxExpanded.clearancePx}px above head)`,
            buf,
            maxW: 360
          });
        }

        // Collapse unreg back before laptop hit-zone check.
        await rich.page.locator('#rail details.rws-unreg > summary').click();
        await rich.page.waitForTimeout(140);

        // --- (d) Projected 3D Laptop Hit-Zone Proof & Click -> Companion Messenger ---
        const laptopInfo = await rich.page.evaluate(() => {
          const btn = document.querySelector('.ws-companion-laptop-hit');
          const slot = document.getElementById('rail-companion-slot').getBoundingClientRect();
          const br = btn.getBoundingClientRect();
          return {
            source: btn.dataset.source || '',
            hidden: btn.hidden,
            pointerEvents: getComputedStyle(btn).pointerEvents,
            rect: { left: Math.round(br.left), top: Math.round(br.top), width: Math.round(br.width), height: Math.round(br.height) },
            insideSlot: br.left >= slot.left - 2 && br.right <= slot.right + 2 && br.top >= slot.top - 2 && br.bottom <= slot.bottom + 2,
            lowerHalf: br.top >= slot.top + slot.height * 0.45
          };
        });
        assert.equal(laptopInfo.hidden, false, `${label} ${theme}: .ws-companion-laptop-hit must be visible`);
        assert.equal(laptopInfo.pointerEvents, 'auto', `${label} ${theme}: .ws-companion-laptop-hit must have pointer-events:auto`);
        assert.equal(laptopInfo.source, 'projected', `${label} ${theme}: 3D VRM laptop hit-zone must use Box3 projection, got ${laptopInfo.source}`);
        assert.equal(laptopInfo.insideSlot, true, `${label} ${theme}: laptop hit-zone must lie inside #rail-companion-slot: ${JSON.stringify(laptopInfo)}`);
        assert.equal(laptopInfo.lowerHalf, true, `${label} ${theme}: laptop hit-zone must sit over the lower-half laptop prop: ${JSON.stringify(laptopInfo)}`);

        // Highlight the projected laptop hit-zone temporarily, click it to open Companion Messenger, capture proof!
        await rich.page.evaluate(() => {
          const btn = document.querySelector('.ws-companion-laptop-hit');
          btn.style.outline = '2px dashed #38bdf8';
          btn.style.background = 'rgba(56, 189, 248, 0.22)';
        });
        await rich.page.locator('.ws-companion-laptop-hit').click();
        await rich.page.waitForFunction(() => window.WsCompanionMessenger?.isOpen?.() === true, null, { timeout: 3000 });
        await rich.page.waitForTimeout(220);

        const messengerRect = await rich.page.evaluate(() => {
          const el = document.getElementById('companion-messenger');
          if (!el || el.hidden) return null;
          const r = el.getBoundingClientRect();
          return { left: Math.round(r.left), top: Math.round(r.top), width: Math.round(r.width), height: Math.round(r.height) };
        });
        assert.ok(messengerRect && messengerRect.width > 200 && messengerRect.height > 200,
          `${label} ${theme}: clicking laptop hit-zone must open visible Companion Messenger, got ${JSON.stringify(messengerRect)}`);

        if (shots) {
          const buf = await rich.page.screenshot({ clip: await railBottomWideClip(rich.page, 460) });
          laptopMessengerPanels.push({
            label: `${label} · ${theme.toUpperCase()} — Projected Laptop Hit (${laptopInfo.rect.width}×${laptopInfo.rect.height}px) → Messenger Open`,
            buf,
            maxW: 540
          });
        }

        // Remove debug outline and toggle Messenger closed via second laptop click.
        await rich.page.evaluate(() => {
          const btn = document.querySelector('.ws-companion-laptop-hit');
          btn.style.outline = '';
          btn.style.background = '';
        });
        await rich.page.locator('.ws-companion-laptop-hit').click();
        await rich.page.waitForFunction(() => window.WsCompanionMessenger?.isOpen?.() === false, null, { timeout: 3000 });
        await rich.page.waitForTimeout(120);

        summary[`${label}_${theme}`] = {
          behindCount: overlapCheck.behindCount,
          clickedRow: clickedActive,
          maxCollapsedClearPx: maxCollapsed.clearancePx,
          maxExpandedClearPx: maxExpanded.clearancePx,
          laptopHit: laptopInfo.rect,
          laptopSource: laptopInfo.source
        };
      }
    }

    // Verify vertical Companion resize handle updates .rlist padding-bottom in lockstep.
    await setTheme(rich.page, 'dark');
    const resizeBefore = await rich.page.evaluate(() => ({
      slotH: Math.round(document.getElementById('rail-companion-slot').getBoundingClientRect().height),
      padB: Math.round(parseFloat(getComputedStyle(document.getElementById('rlist')).paddingBottom) || 0)
    }));
    await rich.page.evaluate(() => {
      const grip = document.querySelector('.ws-companion-resize');
      grip.focus();
    });
    await rich.page.keyboard.press('ArrowUp');
    await rich.page.keyboard.press('ArrowUp');
    await rich.page.waitForTimeout(200);
    const resizeAfter = await rich.page.evaluate(() => ({
      slotH: Math.round(document.getElementById('rail-companion-slot').getBoundingClientRect().height),
      padB: Math.round(parseFloat(getComputedStyle(document.getElementById('rlist')).paddingBottom) || 0)
    }));
    assert.ok(resizeAfter.slotH > resizeBefore.slotH, `resize handle must grow slot height: ${JSON.stringify(resizeBefore)} -> ${JSON.stringify(resizeAfter)}`);
    assert.equal(resizeAfter.padB - resizeBefore.padB, resizeAfter.slotH - resizeBefore.slotH,
      `.rlist padding-bottom must track Companion height in lockstep: ${JSON.stringify(resizeBefore)} -> ${JSON.stringify(resizeAfter)}`);
    summary.resizeLockstep = { before: resizeBefore, after: resizeAfter };

    if (shots) {
      await composeGrid(browser, path.join(shots, '02-mid-scroll-overlap-dark-light.png'),
        '02 · (b) Mid-Scroll Overlap — Workspace Rows Passing Behind Companion Silhouette (Click & Wheel Pass-Through Verified)',
        midOverlapPanels, { cols: 4 });
      await composeGrid(browser, path.join(shots, '03-max-bottom-scroll-clear-dark-light.png'),
        '03 · (c) Max-Bottom Scroll — Expanded Unregistered Workspaces Cleared Cleanly Above Companion Head',
        maxBottomPanels, { cols: 4 });
      await composeGrid(browser, path.join(shots, '04-laptop-hitzone-and-messenger-dark-light.png'),
        '04 · (d) Projected 3D Laptop Hit-Zone Overlay Proof & Opened Companion Messenger After Laptop Click',
        laptopMessengerPanels, { cols: 2 });
    }

    await rich.context.close();

    assert.deepEqual(pageErrors, [], `unexpected page errors: ${JSON.stringify(pageErrors)}`);
    assert.deepEqual(consoleErrors, [], `unexpected console errors: ${JSON.stringify(consoleErrors)}`);
    console.log('companion rail seamless overlay browser: ok', JSON.stringify(summary));
  } finally {
    await browser.close();
  }
})().catch(err => {
  console.error(err);
  process.exit(1);
});
