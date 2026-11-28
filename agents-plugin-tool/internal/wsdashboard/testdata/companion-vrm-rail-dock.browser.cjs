// Run by TestCompanionVrmRailDockBrowser against the live Werkschaft dashboard shell.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [base, shots, vrm0ID, vrm1ID] = process.argv.slice(2);

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
    .cap { padding: 7px 11px; font-size: 12.5px; font-weight: 600; color: #d8dee9; border-bottom: 1px solid #262c3a; background: #1b1f29; }
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

(async () => {
  const browser = await chromium.launch({
    headless: true,
    args: ['--use-gl=angle', '--use-angle=swiftshader', '--enable-unsafe-swiftshader', '--ignore-gpu-blocklist']
  });
  try {
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

    const pageErrors = [];
    const page = await context.newPage();
    page.on('pageerror', e => pageErrors.push(e.message));

    await page.goto(base + '/s/alpha01');
    await page.frameLocator('.sframe.on').locator('#grid').waitFor();

    const applyPack = async packId => {
      await page.evaluate(id => {
        document.dispatchEvent(new CustomEvent('ws-companion-config', {
          detail: { enabled: true, director: 'rules', pack: id, preset: '' }
        }));
      }, packId);
      await page.waitForFunction(id => {
        const st = window.WsCompanion?.debugStage?.();
        return st && st.renderer === 'vrm' && st.pack === id && st.vrm?.loaded && st.vrm.framesRendered >= 6;
      }, packId, { timeout: 30000 });
      await page.waitForTimeout(260);
    };

    const readLayout = async () => page.evaluate(() => {
      const rail = document.getElementById('rail');
      const panel = rail.querySelector('.rail-session-panel');
      const slot = document.getElementById('rail-companion-slot');
      const unreg = rail.querySelector('details.rws-unreg');
      const rfoot = rail.querySelector('.rfoot');
      const rlist = document.getElementById('rlist');
      const hit = document.querySelector('.ws-companion-hit');
      const slotStyle = getComputedStyle(slot);
      const panelStyle = getComputedStyle(panel);
      const railStyle = getComputedStyle(rail);
      const rect = el => {
        if (!el) return null;
        const r = el.getBoundingClientRect();
        return { left: r.left, top: r.top, right: r.right, bottom: r.bottom, width: r.width, height: r.height };
      };
      return {
        vw: window.innerWidth,
        vh: window.innerHeight,
        theme: document.documentElement.dataset.theme || 'auto',
        unregOpen: !!unreg?.open,
        unregCount: unreg?.querySelector('.unreg-count')?.textContent?.trim() || '',
        unregInsideRlist: !!(unreg && rlist && rlist.contains(unreg)),
        unregInsidePanel: !!(unreg && panel && panel.contains(unreg)),
        rfootInsidePanel: !!(rfoot && panel && panel.contains(rfoot)),
        slotInsidePanel: !!(slot && panel && panel.contains(slot)),
        slotHidden: slot.hidden,
        slotDisplay: slotStyle.display,
        slotBorderTopWidth: slotStyle.borderTopWidth,
        slotBorderBottomWidth: slotStyle.borderBottomWidth,
        slotBgImage: slotStyle.backgroundImage,
        slotBgColor: slotStyle.backgroundColor,
        slotPointerEvents: slotStyle.pointerEvents,
        hitPointerEvents: hit ? getComputedStyle(hit).pointerEvents : '',
        charPointerEvents: document.querySelector('.ws-companion-character') ? getComputedStyle(document.querySelector('.ws-companion-character')).pointerEvents : '',
        canvasPointerEvents: document.querySelector('.ws-companion-vrm-canvas') ? getComputedStyle(document.querySelector('.ws-companion-vrm-canvas')).pointerEvents : '',
        rlistPaddingBottom: rlist ? parseFloat(getComputedStyle(rlist).paddingBottom) || 0 : 0,
        panelBg: panelStyle.backgroundColor,
        railBg: railStyle.backgroundColor,
        railRect: rect(rail),
        panelRect: rect(panel),
        rlistRect: rect(rlist),
        unregRect: rect(unreg),
        rfootRect: rect(rfoot),
        slotRect: rect(slot),
        hitRect: rect(hit),
        laptopHitRect: rect(document.querySelector('.ws-companion-laptop-hit')),
        laptopHitSource: document.querySelector('.ws-companion-laptop-hit')?.dataset?.source || '',
        stage: window.WsCompanion?.debugStage?.() || null
      };
    });

    const railBottomClip = async () => page.evaluate(() => {
      const rail = document.getElementById('rail').getBoundingClientRect();
      const panel = document.querySelector('#rail .rail-session-panel').getBoundingClientRect();
      const slot = document.getElementById('rail-companion-slot').getBoundingClientRect();
      const top = Math.max(0, Math.floor(Math.min(slot.top - 128, panel.bottom - 120)));
      return {
        x: Math.max(0, Math.floor(rail.left - 8)),
        y: top,
        width: Math.ceil(rail.width + 18),
        height: Math.min(window.innerHeight - top, Math.ceil(window.innerHeight - top))
      };
    });

    const railFullClip = async () => page.evaluate(() => {
      const r = document.getElementById('rail').getBoundingClientRect();
      return { x: Math.max(0, Math.floor(r.left - 8)), y: 0, width: Math.ceil(r.width + 18), height: window.innerHeight };
    });

    // 1. VRM 1.0 & VRM 0.x in 3-column (1680x960), seamless Variant A overlay:
    //    details.rws-unreg lives inline inside #rlist right after registered workspaces,
    //    #rlist runs the full height of the unified --wb-card rail with dynamic bottom scroll
    //    padding (--companion-slot + 28px), and #rail-companion-slot floats transparently in the
    //    foreground (z-index: 4, pointer-events: none) flush above the 26px bottom bar.
    await applyPack(vrm1ID);
    const collapsed = await readLayout();
    assert.equal(collapsed.unregInsideRlist, true, 'details.rws-unreg must sit inline inside #rlist');
    assert.equal(collapsed.unregInsidePanel, true, 'details.rws-unreg must sit inside .rail-session-panel');
    assert.equal(collapsed.rfootInsidePanel, false, 'retired .rfoot must be absent from shell .rail-session-panel');
    assert.equal(collapsed.rfootRect, null, 'retired .rfoot must not render inside shell rail');
    assert.equal(collapsed.slotInsidePanel, false, '#rail-companion-slot must sit outside .rail-session-panel as final sibling');
    assert.ok(Number(collapsed.unregCount) >= 3, `expected >= 3 pending unregistered workspaces, got ${collapsed.unregCount}`);
    assert.ok(Math.abs(collapsed.rlistRect.bottom - collapsed.railRect.bottom) <= 2,
      `#rlist must run full height behind #rail-companion-slot: rlist=${JSON.stringify(collapsed.rlistRect)} rail=${JSON.stringify(collapsed.railRect)}`);
    assert.ok(collapsed.rlistPaddingBottom >= collapsed.slotRect.height + 24,
      `#rlist padding-bottom (${collapsed.rlistPaddingBottom}) must reserve slot height (${collapsed.slotRect.height}) + clearance`);
    assert.ok(Math.abs(collapsed.slotRect.bottom - collapsed.railRect.bottom) <= 1,
      `slot must be flush to #rail bottom: slot=${JSON.stringify(collapsed.slotRect)} rail=${JSON.stringify(collapsed.railRect)}`);
    assert.ok(Math.abs(collapsed.slotRect.bottom - (collapsed.vh - 26)) <= 1,
      `slot must sit flush above the 26px bottom bar: slotBottom=${collapsed.slotRect.bottom} vh=${collapsed.vh}`);
    assert.equal(collapsed.slotBorderTopWidth, '0px', '#rail-companion-slot must be borderless');
    assert.equal(collapsed.slotBorderBottomWidth, '0px', '#rail-companion-slot must be borderless');
    assert.equal(collapsed.slotBgImage, 'none', '#rail-companion-slot must have no gradient backdrop');
    assert.equal(collapsed.slotBgColor, 'rgba(0, 0, 0, 0)', '#rail-companion-slot must have transparent background');
    assert.equal(collapsed.slotPointerEvents, 'none', '#rail-companion-slot must be pointer-events:none');
    assert.equal(collapsed.hitPointerEvents, 'none', '.ws-companion-hit must be pointer-events:none in docked overlay');
    assert.equal(collapsed.charPointerEvents, 'none', '.ws-companion-character must be pointer-events:none in docked overlay');
    assert.equal(collapsed.canvasPointerEvents, 'none', '.ws-companion-vrm-canvas must be pointer-events:none');
    assert.notEqual(collapsed.railBg, 'rgba(0, 0, 0, 0)', 'Variant A outer .rail keeps unified --wb-card background');
    assert.ok(collapsed.laptopHitRect && collapsed.laptopHitRect.width >= 24 && collapsed.laptopHitRect.height >= 20,
      `expected interactive .ws-companion-laptop-hit box, got ${JSON.stringify(collapsed.laptopHitRect)}`);
    assert.ok(Math.abs(collapsed.hitRect.left - collapsed.slotRect.left) <= 2 &&
      Math.abs(collapsed.hitRect.bottom - collapsed.slotRect.bottom) <= 2 &&
      Math.abs(collapsed.hitRect.width - (collapsed.slotRect.width - 4)) <= 2 &&
      Math.abs(collapsed.hitRect.height - collapsed.slotRect.height) <= 2,
      `VRM hit must align with #rail-companion-slot minus 4px splitter clearance: hit=${JSON.stringify(collapsed.hitRect)} slot=${JSON.stringify(collapsed.slotRect)}`);

    const vrm1CollapsedFull = shots ? await page.screenshot() : null;
    const vrm1CollapsedRail = shots ? await page.screenshot({ clip: await railFullClip() }) : null;
    const vrm1CollapsedCrop = shots ? await page.screenshot({ clip: await railBottomClip() }) : null;

    await applyPack(vrm0ID);
    const vrm0Collapsed = await readLayout();
    assert.ok(Math.abs(vrm0Collapsed.slotRect.bottom - (vrm0Collapsed.vh - 26)) <= 1,
      `VRM 0.x slot must sit flush above the 26px bottom bar: slotBottom=${vrm0Collapsed.slotRect.bottom} vh=${vrm0Collapsed.vh}`);
    const vrm0CollapsedFull = shots ? await page.screenshot() : null;
    const vrm0CollapsedCrop = shots ? await page.screenshot({ clip: await railBottomClip() }) : null;

    if (shots) {
      await composeGrid(browser, path.join(shots, '01-rail-unreg-collapsed.png'),
        '01 · Bottom-Left Companion Handover (1680×960, Unregistered Collapsed) — VRM 1.0 & VRM 0.x Flush to Viewport Bottom', [
          { label: `VRM 1.0 — Full 3-Column (slot.bottom=${Math.round(collapsed.slotRect.bottom)} / vh=${collapsed.vh})`, buf: vrm1CollapsedFull, maxW: 960 },
          { label: 'VRM 1.0 — Bottom-Left Handover Closeup', buf: vrm1CollapsedCrop, maxW: 360 },
          { label: `VRM 0.x — Full 3-Column (slot.bottom=${Math.round(vrm0Collapsed.slotRect.bottom)} / vh=${vrm0Collapsed.vh})`, buf: vrm0CollapsedFull, maxW: 960 },
          { label: 'VRM 0.x — Bottom-Left Handover Closeup', buf: vrm0CollapsedCrop, maxW: 360 }
        ], { cols: 2 });
    }

    await applyPack(vrm1ID);

    // 2. Zero vertical jitter invariant: scroll #rlist to bottom, toggle details.rws-unreg
    //    open & closed, and assert strictly 0px shift on #rail-companion-slot (top, bottom, height).
    const beforeExpand = await readLayout();
    const scrollBefore = await page.evaluate(() => {
      const rlist = document.getElementById('rlist');
      rlist.scrollTop = rlist.scrollHeight;
      const rows = Array.from(rlist.querySelectorAll(':scope > section.rws .rs'));
      const last = rows[rows.length - 1];
      const lr = last.getBoundingClientRect();
      const hitEl = document.elementFromPoint(Math.floor(lr.left + lr.width / 2), Math.floor(lr.top + lr.height / 2));
      return {
        overflows: rlist.scrollHeight > rlist.clientHeight,
        lastRid: last.dataset.rid,
        clickable: !!(hitEl && last.contains(hitEl))
      };
    });
    assert.equal(scrollBefore.overflows, true, '#rlist must overflow to verify bottom scroll');
    assert.equal(scrollBefore.clickable, true, 'last session row must be clickable above unreg/footer');

    // 2a. Transparent click pass-through & wheel pass-through over #rail-companion-slot:
    //     scroll to top so rows / unreg sit behind #rail-companion-slot, verify
    //     elementFromPoint in the transparent upper area hits #rlist content directly
    //     (not intercepted by Companion), and verify mouse wheel over the Companion
    //     scrolls #rlist down and back up smoothly.
    const passThrough = await page.evaluate(() => {
      const rlist = document.getElementById('rlist');
      const slot = document.getElementById('rail-companion-slot').getBoundingClientRect();
      const laptop = document.querySelector('.ws-companion-laptop-hit').getBoundingClientRect();
      rlist.scrollTop = 0;
      const probeY = Math.floor(slot.top + Math.min(48, Math.max(18, (laptop.top - slot.top) * 0.45)));
      const probeX = Math.floor(slot.left + 26);
      const hitEl = document.elementFromPoint(probeX, probeY);
      const row = hitEl ? hitEl.closest('.rs, .rws, .rws-unreg, #rlist') : null;
      return {
        probeX,
        probeY,
        hitTag: hitEl?.tagName || '',
        hitClass: hitEl?.className || '',
        hitsRlistRow: !!(row && (row === rlist || rlist.contains(row))),
        hitsCompanion: !!(hitEl && hitEl.closest('.ws-companion')),
        scrollBeforeWheel: rlist.scrollTop,
        maxScroll: rlist.scrollHeight - rlist.clientHeight,
        slotCenterX: Math.floor(slot.left + slot.width * 0.5),
        slotCenterY: Math.floor(slot.top + 36)
      };
    });
    assert.equal(passThrough.hitsCompanion, false, `upper companion area must pass through to #rlist, got ${JSON.stringify(passThrough)}`);
    assert.equal(passThrough.hitsRlistRow, true, `probe inside upper companion area must hit #rlist content, got ${JSON.stringify(passThrough)}`);
    await page.mouse.move(passThrough.slotCenterX, passThrough.slotCenterY);
    await page.mouse.wheel(0, 180);
    await page.waitForFunction(prev => document.getElementById('rlist').scrollTop > prev, passThrough.scrollBeforeWheel, { timeout: 3000 });
    await page.waitForTimeout(180);
    const scrollAfterWheelDown = await page.evaluate(() => document.getElementById('rlist').scrollTop);
    assert.ok(scrollAfterWheelDown > passThrough.scrollBeforeWheel,
      `mouse wheel down over #rail-companion-slot must scroll #rlist: before=${passThrough.scrollBeforeWheel} after=${scrollAfterWheelDown} max=${passThrough.maxScroll}`);
    await page.mouse.wheel(0, -240);
    await page.waitForFunction(prev => document.getElementById('rlist').scrollTop < prev, scrollAfterWheelDown, { timeout: 3000 });
    const scrollAfterWheelUp = await page.evaluate(() => document.getElementById('rlist').scrollTop);
    assert.ok(scrollAfterWheelUp < scrollAfterWheelDown,
      `mouse wheel up over #rail-companion-slot must scroll #rlist back up: down=${scrollAfterWheelDown} up=${scrollAfterWheelUp}`);

    // 2b. Clicking .ws-companion-laptop-hit opens Companion Messenger and toggles cleanly.
    const laptopHitLoc = page.locator('.ws-companion-laptop-hit');
    await laptopHitLoc.click();
    await page.waitForFunction(() => window.WsCompanionMessenger?.isOpen?.() === true, null, { timeout: 3000 });
    await laptopHitLoc.click();
    await page.waitForFunction(() => window.WsCompanionMessenger?.isOpen?.() === false, null, { timeout: 3000 });
    await page.evaluate(() => {
      const rlist = document.getElementById('rlist');
      rlist.scrollTop = rlist.scrollHeight;
    });
    await page.waitForTimeout(120);

    await page.locator('#rail details.rws-unreg > summary').click();
    await page.waitForTimeout(350);
    const expanded = await readLayout();
    assert.equal(expanded.unregOpen, true, 'details.rws-unreg must open');
    const shiftOpen = {
      top: expanded.slotRect.top - beforeExpand.slotRect.top,
      bottom: expanded.slotRect.bottom - beforeExpand.slotRect.bottom,
      height: expanded.slotRect.height - beforeExpand.slotRect.height
    };
    assert.equal(shiftOpen.top, 0, `slot top shifted on unreg expand: ${JSON.stringify(shiftOpen)}`);
    assert.equal(shiftOpen.bottom, 0, `slot bottom shifted on unreg expand: ${JSON.stringify(shiftOpen)}`);
    assert.equal(shiftOpen.height, 0, `slot height shifted on unreg expand: ${JSON.stringify(shiftOpen)}`);
    assert.ok(Math.abs(expanded.hitRect.bottom - expanded.slotRect.bottom) <= 1,
      `VRM hit drifted after unreg expand: hit=${JSON.stringify(expanded.hitRect)} slot=${JSON.stringify(expanded.slotRect)}`);

    const scrollAfterExpand = await page.evaluate(() => {
      const rlist = document.getElementById('rlist');
      rlist.scrollTop = rlist.scrollHeight;
      const rows = Array.from(rlist.querySelectorAll(':scope > section.rws .rs'));
      const last = rows[rows.length - 1];
      const lr = last.getBoundingClientRect();
      const ur = document.querySelector('#rail details.rws-unreg').getBoundingClientRect();
      const sr = document.getElementById('rail-companion-slot').getBoundingClientRect();
      const hitEl = document.elementFromPoint(Math.floor(lr.left + lr.width / 2), Math.floor(lr.top + lr.height / 2));
      return {
        lastRid: last.dataset.rid,
        clickable: !!(hitEl && last.contains(hitEl)),
        rowAboveUnreg: lr.bottom <= ur.top + 2,
        unregAboveSlot: ur.bottom <= sr.top + 2
      };
    });
    assert.equal(scrollAfterExpand.clickable, true, 'last session row must stay clickable when .rws-unreg is expanded');
    assert.equal(scrollAfterExpand.unregAboveSlot, true, 'max-bottom scroll must clear expanded details.rws-unreg above #rail-companion-slot');

    const expandedRailShot = shots ? await page.screenshot({ clip: await railFullClip() }) : null;
    const expandedFullShot = shots ? await page.screenshot() : null;

    // Collapse back and verify strictly 0px shift on collapse as well.
    await page.locator('#rail details.rws-unreg > summary').click();
    await page.waitForTimeout(300);
    const afterCollapse = await readLayout();
    assert.equal(afterCollapse.unregOpen, false, 'details.rws-unreg must close');
    const shiftClose = {
      top: afterCollapse.slotRect.top - beforeExpand.slotRect.top,
      bottom: afterCollapse.slotRect.bottom - beforeExpand.slotRect.bottom,
      height: afterCollapse.slotRect.height - beforeExpand.slotRect.height
    };
    assert.equal(shiftClose.top, 0, `slot top shifted on unreg collapse: ${JSON.stringify(shiftClose)}`);
    assert.equal(shiftClose.bottom, 0, `slot bottom shifted on unreg collapse: ${JSON.stringify(shiftClose)}`);
    assert.equal(shiftClose.height, 0, `slot height shifted on unreg collapse: ${JSON.stringify(shiftClose)}`);

    if (shots) {
      await composeGrid(browser, path.join(shots, '02-rail-unreg-expanded-0px-shift.png'),
        `02 · Zero Vertical Jitter — Unregistered Expanded (Δtop=${shiftOpen.top}px, Δbottom=${shiftOpen.bottom}px, Δheight=${shiftOpen.height}px)`, [
          { label: `Collapsed (slot.top=${Math.round(beforeExpand.slotRect.top)}px, h=${Math.round(beforeExpand.slotRect.height)}px)`, buf: vrm1CollapsedRail, maxW: 360 },
          { label: `Expanded (slot.top=${Math.round(expanded.slotRect.top)}px, Δ=0px, ${scrollAfterExpand.lastRid} clickable)`, buf: expandedRailShot, maxW: 360 },
          { label: 'Full 3-Column Context — Expanded Unregistered Workspaces', buf: expandedFullShot, maxW: 920 }
        ], { cols: 3 });
    }

    // 3. Dark & Light theme tonal separation (borderless Material surfaces).
    const darkRailShot = shots ? await page.screenshot({ clip: await railFullClip() }) : null;
    const darkCropShot = shots ? await page.screenshot({ clip: await railBottomClip() }) : null;

    await page.emulateMedia({ colorScheme: 'light' });
    await page.evaluate(() => { document.documentElement.dataset.theme = 'light'; });
    await page.waitForTimeout(250);
    const lightLayout = await readLayout();
    assert.equal(lightLayout.slotBorderTopWidth, '0px', 'light theme slot must remain borderless');
    assert.equal(lightLayout.slotBgImage, 'none', 'light theme slot must remain transparent (no backdrop gradient)');
    const lightRailShot = shots ? await page.screenshot({ clip: await railFullClip() }) : null;
    const lightCropShot = shots ? await page.screenshot({ clip: await railBottomClip() }) : null;

    if (shots) {
      await composeGrid(browser, path.join(shots, '03-dark-and-light-tonal-separation.png'),
        '03 · Dark & Light Theme Tonal Separation — Borderless Material Upper Card & Bottom-Left Companion Stage', [
          { label: 'Dark Theme — Full Left Rail', buf: darkRailShot, maxW: 360 },
          { label: 'Dark Theme — Handover & Bottom Edge Closeup', buf: darkCropShot, maxW: 360 },
          { label: 'Light Theme — Full Left Rail', buf: lightRailShot, maxW: 360 },
          { label: 'Light Theme — Handover & Bottom Edge Closeup', buf: lightCropShot, maxW: 360 }
        ], { cols: 4 });
    }

    // Restore dark theme for remaining checks.
    await page.emulateMedia({ colorScheme: 'dark' });
    await page.evaluate(() => { document.documentElement.dataset.theme = 'dark'; });
    await page.waitForTimeout(200);

    // 4. Responsive rail width check (Variant D splitter range: 220px, 280px, 360px)
    //    + Phase 1 overlay invariants: zero WebGL resize/context loss, .rail-splitter
    //    hit-testability, [data-fdim] modal inertness, and docked/bleed/roam scissor transitions.
    const resizeBefore = await page.evaluate(() => window.WsCompanion?.getVrmRenderMetrics?.()?.viewportResizeCount ?? -1);
    for (const testW of [220, 280, 360]) {
      await page.evaluate(w => {
        if (window.WsIdeShell) window.WsIdeShell.setWidth(w);
        else document.documentElement.style.setProperty('--rail-w', `${w}px`);
      }, testW);
      await page.waitForFunction((expectedW) => {
        const slot = document.getElementById('rail-companion-slot')?.getBoundingClientRect();
        const hit = document.querySelector('.ws-companion-hit')?.getBoundingClientRect();
        return !!(slot && hit && Math.abs(slot.width - expectedW) <= 2 && Math.abs(hit.width - (slot.width - 4)) <= 2);
      }, testW, { timeout: 3000 });
      const resized = await readLayout();
      assert.ok(Math.abs(resized.slotRect.width - resized.railRect.width) <= 1,
        `#rail-companion-slot must match .rail width (${resized.railRect.width}px), got ${resized.slotRect.width}`);
      assert.ok(Math.abs(resized.slotRect.width - testW) <= 2,
        `#rail-companion-slot must resize responsively to ${testW}px, got ${resized.slotRect.width}`);
      assert.ok(Math.abs(resized.hitRect.width - (resized.slotRect.width - 4)) <= 2,
        `VRM hit must track resized slot width minus 4px splitter clearance (${resized.slotRect.width - 4}px), got ${resized.hitRect.width}`);
    }
    const afterDrags = await page.evaluate(() => {
      const m = window.WsCompanion?.getVrmRenderMetrics?.();
      const st = window.WsCompanion?.debugStage?.();
      const splitter = document.getElementById('rail-splitter') || document.querySelector('.rail-splitter');
      const slot = document.getElementById('rail-companion-slot').getBoundingClientRect();
      const sr = splitter ? splitter.getBoundingClientRect() : null;
      const hitAtSplitter = sr ? document.elementFromPoint(Math.floor(sr.left + sr.width / 2), Math.floor(slot.top + slot.height / 2)) : null;
      document.documentElement.setAttribute('data-fdim', '');
      const fdimHitPe = getComputedStyle(document.querySelector('.ws-companion-hit')).pointerEvents;
      const fdimRootPe = getComputedStyle(document.querySelector('.ws-companion')).pointerEvents;
      const fdimMenuPe = getComputedStyle(document.querySelector('.ws-companion-menu')).pointerEvents;
      const fdimAtSlot = document.elementFromPoint(Math.floor(slot.left + slot.width / 2), Math.floor(slot.top + slot.height / 2));
      const fdimHitsCompanion = !!(fdimAtSlot && fdimAtSlot.closest?.('.ws-companion'));
      document.documentElement.removeAttribute('data-fdim');
      return {
        resizeCount: m?.viewportResizeCount ?? -1,
        contextLost: !!st?.vrm?.contextLost,
        hasSplitter: !!splitter,
        splitterHit: !!(splitter && hitAtSplitter && (hitAtSplitter === splitter || splitter.contains(hitAtSplitter))),
        fdimHitPe,
        fdimRootPe,
        fdimMenuPe,
        fdimHitsCompanion
      };
    });
    assert.equal(afterDrags.resizeCount, resizeBefore, 'rail width drags 220<->360 must perform zero WebGL viewport resizes');
    assert.equal(afterDrags.contextLost, false, 'rail width drags must cause zero WebGL context loss');
    assert.equal(afterDrags.hasSplitter, true, 'Variant D .rail-splitter must exist in the shell');
    assert.equal(afterDrags.splitterHit, true, '.rail-splitter must remain directly hit-testable alongside #rail-companion-slot');
    assert.equal(afterDrags.fdimHitPe, 'none', '[data-fdim] must suppress pointer events on .ws-companion-hit');
    assert.equal(afterDrags.fdimRootPe, 'none', '[data-fdim] must suppress pointer events on .ws-companion');
    assert.equal(afterDrags.fdimMenuPe, 'none', '[data-fdim] must suppress pointer events on .ws-companion-menu');
    assert.equal(afterDrags.fdimHitsCompanion, false, '[data-fdim] elementFromPoint over slot must not hit .ws-companion');

    // Verify docked -> bleed -> roam -> docked scissor clamping above .shell-bottom and zero ghost pixels.
    const scissorCheck = await page.evaluate(async () => {
      const waitFrames = async n => {
        for (let i = 0; i < n; i++) await new Promise(r => requestAnimationFrame(r));
      };
      const readMask = () => {
        const cs = getComputedStyle(document.querySelector('.ws-companion-vrm-canvas-wrap'));
        return cs.maskImage || cs.webkitMaskImage || '';
      };
      const botRect = document.querySelector('.shell-bottom')?.getBoundingClientRect();
      const maxAllowedBottom = botRect && botRect.height > 0 ? Math.round(window.innerHeight - botRect.height) : (window.innerHeight - 26);
      const shrinkInitial = window.WsCompanion.getVrmRenderMetrics().shrinkClearCount;

      window.WsCompanion.setVrmRenderMode('bleed');
      await waitFrames(4);
      const bleedMetrics = window.WsCompanion.getVrmRenderMetrics();
      const bleedMask = readMask();

      window.WsCompanion.setVrmRenderMode('roam');
      await waitFrames(4);
      const roamMetrics = window.WsCompanion.getVrmRenderMetrics();
      const roamMask = readMask();

      window.WsCompanion.setVrmRenderMode('docked');
      await waitFrames(4);
      const dockedMetrics = window.WsCompanion.getVrmRenderMetrics();
      const dockedMask = readMask();

      // Explicit bleed -> docked shrink-clear check + pixel probe outside docked scissor rect.
      window.WsCompanion.setVrmRenderMode('bleed');
      await waitFrames(3);
      const bleedShrinkBefore = window.WsCompanion.getVrmRenderMetrics().shrinkClearCount;
      window.WsCompanion.setVrmRenderMode('docked');
      await waitFrames(3);
      const afterBleedDocked = window.WsCompanion.getVrmRenderMetrics();

      let ghostPixelsOutsideDock = -1;
      const vrmCanvas = document.querySelector('.ws-companion-vrm-canvas');
      if (vrmCanvas && vrmCanvas.width > 0 && vrmCanvas.height > 0) {
        const probe = document.createElement('canvas');
        probe.width = vrmCanvas.width;
        probe.height = vrmCanvas.height;
        const pctx = probe.getContext('2d', { willReadFrequently: true });
        pctx.drawImage(vrmCanvas, 0, 0);
        const sr = afterBleedDocked.scissorRect;
        const scaleX = vrmCanvas.width / Math.max(1, window.innerWidth);
        const scaleY = vrmCanvas.height / Math.max(1, window.innerHeight);
        // Sample the bleed-only strip immediately to the right of the docked scissor rect.
        const sx = Math.min(vrmCanvas.width - 1, Math.ceil((sr.left + sr.width + 4) * scaleX));
        const sy = Math.max(0, Math.floor(sr.top * scaleY));
        const sw = Math.max(1, Math.min(36, vrmCanvas.width - sx));
        const sh = Math.max(1, Math.min(Math.floor(sr.height * scaleY), vrmCanvas.height - sy));
        const data = pctx.getImageData(sx, sy, sw, sh).data;
        ghostPixelsOutsideDock = 0;
        for (let i = 3; i < data.length; i += 4) {
          if (data[i] > 4) ghostPixelsOutsideDock++;
        }
      }

      return {
        maxAllowedBottom,
        shrinkInitial,
        bleedMetrics,
        bleedMask,
        roamMetrics,
        roamMask,
        dockedMetrics,
        dockedMask,
        bleedShrinkBefore,
        afterBleedDocked,
        ghostPixelsOutsideDock
      };
    });
    assert.equal(scissorCheck.bleedMetrics.mode, 'bleed');
    assert.equal(scissorCheck.bleedMask, 'none', 'bleed mode must disable docked bottom-rim mask');
    assert.ok(scissorCheck.bleedMetrics.scissorRect.top + scissorCheck.bleedMetrics.scissorRect.height <= scissorCheck.maxAllowedBottom,
      `bleed scissor must clamp above .shell-bottom (${scissorCheck.maxAllowedBottom}): ${JSON.stringify(scissorCheck.bleedMetrics.scissorRect)}`);
    assert.equal(scissorCheck.roamMetrics.mode, 'roam');
    assert.equal(scissorCheck.roamMask, 'none', 'roam mode must disable docked bottom-rim mask');
    assert.ok(scissorCheck.roamMetrics.scissorRect.top + scissorCheck.roamMetrics.scissorRect.height <= scissorCheck.maxAllowedBottom,
      `roam scissor must clamp above .shell-bottom (${scissorCheck.maxAllowedBottom}): ${JSON.stringify(scissorCheck.roamMetrics.scissorRect)}`);
    assert.equal(scissorCheck.dockedMetrics.mode, 'docked');
    assert.match(scissorCheck.dockedMask, /linear-gradient/, 'docked mode must restore subtle bottom-rim mask');
    assert.ok(scissorCheck.dockedMetrics.shrinkClearCount > scissorCheck.shrinkInitial,
      'docked -> bleed -> roam -> docked cycle must trigger shrink-clear invariant');
    assert.ok(scissorCheck.afterBleedDocked.shrinkClearCount > scissorCheck.bleedShrinkBefore,
      'bleed -> docked return must trigger shrink-clear invariant');
    assert.equal(scissorCheck.ghostPixelsOutsideDock, 0,
      `expected zero ghost pixels outside docked scissor after bleed -> docked, got ${scissorCheck.ghostPixelsOutsideDock}`);

    await page.evaluate(() => {
      if (window.WsIdeShell) window.WsIdeShell.setWidth(280);
    });
    await page.waitForTimeout(220);

    // 5. Narrow viewports (< 1600px): 1440x900 (2-col) and 720x900 (1-col) hide VRM & pause WebGL,
    //    and .rail-session-panel expands smoothly to fill 100% of .rail height.
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.waitForFunction(() => window.WsCompanion?.debugStage?.()?.vrmRailVisible === false, null, { timeout: 5000 });
    await page.waitForTimeout(200);
    const narrow1440 = await page.evaluate(() => ({
      st: window.WsCompanion.debugStage(),
      hitHidden: document.querySelector('.ws-companion-hit')?.hidden,
      slotDisplay: getComputedStyle(document.getElementById('rail-companion-slot')).display,
      dockAttr: document.getElementById('rail').dataset.companionDock || ''
    }));
    assert.equal(narrow1440.hitHidden, true);
    assert.equal(narrow1440.slotDisplay, 'none');
    assert.equal(narrow1440.dockAttr, '');
    assert.equal(narrow1440.st.vrm.paused, true);
    const framesA = narrow1440.st.vrm.framesRendered;
    await page.waitForTimeout(420);
    const framesB = await page.evaluate(() => window.WsCompanion.debugStage().vrm.framesRendered);
    assert.equal(framesB, framesA, 'narrow 1440px viewport must render 0 frames');

    await page.evaluate(() => document.documentElement.classList.add('rail-open'));
    await page.waitForTimeout(260);
    const drawerCheck = await page.evaluate(() => {
      const rail = document.getElementById('rail').getBoundingClientRect();
      const panel = document.querySelector('#rail .rail-session-panel').getBoundingClientRect();
      return {
        hitHidden: document.querySelector('.ws-companion-hit')?.hidden,
        slotDisplay: getComputedStyle(document.getElementById('rail-companion-slot')).display,
        vrmRailVisible: window.WsCompanion.debugStage().vrmRailVisible,
        panelFillsRail: Math.abs(panel.height - rail.height) <= 2
      };
    });
    assert.equal(drawerCheck.hitHidden, true, 'VRM must remain hidden even when narrow rail drawer opens');
    assert.equal(drawerCheck.slotDisplay, 'none', '#rail-companion-slot must remain display:none below 1600px');
    assert.equal(drawerCheck.vrmRailVisible, false);
    assert.equal(drawerCheck.panelFillsRail, true, 'session rail must fill 100% height below 1600px');
    await page.evaluate(() => document.documentElement.classList.remove('rail-open'));

    await page.setViewportSize({ width: 720, height: 900 });
    await page.waitForTimeout(200);
    const narrow720 = await page.evaluate(() => ({
      st: window.WsCompanion.debugStage(),
      hitHidden: document.querySelector('.ws-companion-hit')?.hidden,
      slotDisplay: getComputedStyle(document.getElementById('rail-companion-slot')).display
    }));
    assert.equal(narrow720.hitHidden, true);
    assert.equal(narrow720.slotDisplay, 'none');
    assert.equal(narrow720.st.vrm.paused, true);

    // 6. Widen back to 1680x960 -> verify hover gear button reachability, FX badges,
    //    Co-Lead laptop bottom-flush framing, and quick menu clamping (right-click & keyboard).
    await page.setViewportSize({ width: 1680, height: 960 });
    await page.waitForFunction(() => {
      const st = window.WsCompanion?.debugStage?.();
      return st && st.vrmRailVisible === true && !st.hidden && st.vrm && !st.vrm.paused;
    }, null, { timeout: 8000 });
    await page.waitForTimeout(280);

    // Hover over companion slot -> gear button is unclipped and clickable inside #rail-companion-slot.
    const hoverTarget = await readLayout();
    await page.mouse.move(
      Math.floor(hoverTarget.slotRect.left + hoverTarget.slotRect.width * 0.5),
      Math.floor(hoverTarget.slotRect.top + hoverTarget.slotRect.height * 0.5)
    );
    await page.waitForTimeout(180);
    const hoverCheck = await page.evaluate(() => {
      const gear = document.querySelector('.ws-companion-gear');
      const slot = document.getElementById('rail-companion-slot').getBoundingClientRect();
      const gr = gear.getBoundingClientRect();
      const style = getComputedStyle(gear);
      const hitEl = document.elementFromPoint(Math.floor(gr.left + gr.width / 2), Math.floor(gr.top + gr.height / 2));
      return {
        gearHidden: gear.hidden,
        gearOpacity: parseFloat(style.opacity),
        gearInsideSlot: gr.left >= slot.left - 1 && gr.right <= slot.right + 1 && gr.top >= slot.top - 1 && gr.bottom <= slot.bottom + 1,
        gearReachable: !!(hitEl && (hitEl === gear || gear.contains(hitEl))),
        slotBottom: slot.bottom,
        vh: window.innerHeight,
        laptopInfo: window.WsCompanion.debugStage()?.vrm?.colead || null
      };
    });
    assert.equal(hoverCheck.gearHidden, false, 'gear control should be present');
    assert.ok(hoverCheck.gearOpacity >= 0.5, `hovered gear button should be visible, got opacity=${hoverCheck.gearOpacity}`);
    assert.equal(hoverCheck.gearInsideSlot, true, 'gear button must stay inside #rail-companion-slot');
    assert.equal(hoverCheck.gearReachable, true, 'gear button must be directly clickable');
    const hoverLaptopShot = shots ? await page.screenshot({ clip: await railBottomClip() }) : null;

    // Trigger FX badges to verify they stay unclipped above/beside the bottom-left bust.
    let fxAlertShot = null;
    for (const fx of ['alert', 'sparkle', 'zzz']) {
      const expr = fx === 'sparkle' ? 'happy' : (fx === 'zzz' ? 'idle_doze' : 'oo');
      await page.evaluate(([f, e]) => {
        document.dispatchEvent(new CustomEvent('ws-companion-command', {
          detail: { sessionId: 'alpha01', fx: f, expression: e, ttl_ms: 4000 }
        }));
      }, [fx, expr]);
      await page.waitForFunction(() => {
        const el = document.querySelector('.ws-companion-fx');
        return el && !el.hidden && el.textContent.trim().length > 0;
      }, null, { timeout: 3000 });
      await page.waitForTimeout(180);
      const fxBox = await page.evaluate(() => {
        const fxEl = document.querySelector('.ws-companion-fx');
        const root = document.querySelector('.ws-companion');
        return {
          inBody: document.body.contains(fxEl) && !document.getElementById('rail').contains(fxEl),
          fxRect: fxEl.getBoundingClientRect().toJSON(),
          rootRect: root.getBoundingClientRect().toJSON()
        };
      });
      assert.equal(fxBox.inBody, true, `${fx} FX badge must live in document.body outside .rail`);
      assert.ok(fxBox.fxRect.left >= fxBox.rootRect.left - 1 && fxBox.fxRect.top >= fxBox.rootRect.top - 1 &&
        fxBox.fxRect.bottom <= fxBox.rootRect.bottom + 1,
        `${fx} FX badge clipped: ${JSON.stringify(fxBox)}`);
      if (shots && fx === 'alert') fxAlertShot = await page.screenshot({ clip: await railBottomClip() });
    }

    // Open quick menu via keyboard Shift+F10 on the interactive laptop hit button.
    await page.locator('.ws-companion-laptop-hit').focus();
    await page.keyboard.press('Shift+F10');
    await page.locator('.ws-companion-menu:not([hidden])').waitFor({ timeout: 3000 });
    const kbMenuBox = await page.evaluate(() => {
      const mr = document.querySelector('.ws-companion-menu').getBoundingClientRect();
      return { left: mr.left, top: mr.top, right: mr.right, bottom: mr.bottom, vw: window.innerWidth, vh: window.innerHeight };
    });
    assert.ok(kbMenuBox.left >= 6 && kbMenuBox.top >= 6 && kbMenuBox.right <= kbMenuBox.vw && kbMenuBox.bottom <= kbMenuBox.vh,
      `keyboard quick menu clipped: ${JSON.stringify(kbMenuBox)}`);
    await page.keyboard.press('Escape');
    await page.waitForTimeout(120);

    // Open quick menu via right-click on the laptop hit button -> verify upward/rightward placement 100% inside viewport.
    await page.locator('.ws-companion-laptop-hit').click({ button: 'right' });
    await page.locator('.ws-companion-menu:not([hidden])').waitFor({ timeout: 3000 });
    await page.waitForTimeout(180);
    const menuBox = await page.evaluate(() => {
      const rail = document.getElementById('rail').getBoundingClientRect();
      const menu = document.querySelector('.ws-companion-menu');
      const mr = menu.getBoundingClientRect();
      const style = getComputedStyle(menu);
      return {
        inBody: document.body.contains(menu) && !document.getElementById('rail').contains(menu),
        borderWidth: style.borderTopWidth,
        railRight: rail.right,
        menuLeft: mr.left,
        menuRight: mr.right,
        menuTop: mr.top,
        menuBottom: mr.bottom,
        vw: window.innerWidth,
        vh: window.innerHeight
      };
    });
    assert.equal(menuBox.inBody, true, 'context menu must live in document.body outside .rail');
    assert.equal(menuBox.borderWidth, '0px', 'quick menu must use borderless whisper inset shadow');
    assert.ok(menuBox.menuLeft >= menuBox.railRight - 2 && menuBox.menuLeft >= 6 &&
      menuBox.menuRight <= menuBox.vw && menuBox.menuTop >= 6 && menuBox.menuBottom <= menuBox.vh,
      `context menu clipped or trapped inside .rail: ${JSON.stringify(menuBox)}`);

    if (shots) {
      const menuWideClip = await page.evaluate(() => {
        const rail = document.getElementById('rail').getBoundingClientRect();
        const slot = document.getElementById('rail-companion-slot').getBoundingClientRect();
        const top = Math.max(0, Math.floor(slot.top - 96));
        return {
          x: Math.max(0, Math.floor(rail.left - 8)),
          y: top,
          width: Math.min(window.innerWidth, Math.ceil(rail.width + 260)),
          height: Math.min(window.innerHeight - top, Math.ceil(window.innerHeight - top))
        };
      });
      const menuShot = await page.screenshot({ clip: menuWideClip });
      const menuFullShot = await page.screenshot();
      await composeGrid(browser, path.join(shots, '04-quick-menu-and-laptop-bottom-flush.png'),
        `04 · Quick Menu Clamping & Bottom-Flush Co-Lead Controls (menu.bottom=${Math.round(menuBox.menuBottom)} <= ${menuBox.vh}, slot.bottom=${Math.round(hoverCheck.slotBottom)})`, [
          { label: 'Hover Gear Control & Bottom-Flush Laptop Edge', buf: hoverLaptopShot, maxW: 360 },
          { label: 'FX Badge Unclipped in document.body', buf: fxAlertShot, maxW: 360 },
          { label: `Quick Menu Unclipped (bottom=${Math.round(menuBox.menuBottom)}/${menuBox.vh})`, buf: menuShot, maxW: 540 },
          { label: 'Full 3-Column Bottom-Left Context + Quick Menu', buf: menuFullShot, maxW: 920 }
        ], { cols: 2 });
    }

    assert.deepEqual(pageErrors, [], `unexpected page errors: ${JSON.stringify(pageErrors)}`);
    console.log('companion vrm rail dock browser: ok', JSON.stringify({
      unregCount: collapsed.unregCount,
      slotBottom: Math.round(collapsed.slotRect.bottom),
      vh: collapsed.vh,
      shiftOpen,
      shiftClose,
      menuBottom: Math.round(menuBox.menuBottom),
      gearReachable: hoverCheck.gearReachable
    }));
  } finally {
    await browser.close();
  }
})().catch(err => {
  console.error(err);
  process.exit(1);
});
