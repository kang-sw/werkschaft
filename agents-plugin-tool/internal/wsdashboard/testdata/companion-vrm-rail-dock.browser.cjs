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
  const page = await browser.newPage({ viewport: { width: 2200, height: 1400 }, deviceScaleFactor: 1 });
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
        return st && st.renderer === 'vrm' && st.pack === id && st.vrm?.loaded && st.vrm.framesRendered >= 4;
      }, packId, { timeout: 30000 });
      await page.waitForTimeout(250);
    };

    const railBottomClip = async () => page.evaluate(() => {
      const rail = document.getElementById('rail').getBoundingClientRect();
      const slot = document.getElementById('rail-companion-slot').getBoundingClientRect();
      const top = Math.max(0, Math.floor(slot.top - 92));
      return {
        x: Math.max(0, Math.floor(rail.left - 8)),
        y: top,
        width: Math.ceil(rail.width + 18),
        height: Math.min(window.innerHeight - top, Math.ceil(rail.bottom - top + 8))
      };
    });

    const railFullClip = async () => page.evaluate(() => {
      const r = document.getElementById('rail').getBoundingClientRect();
      return { x: Math.max(0, Math.floor(r.left - 8)), y: 0, width: Math.ceil(r.width + 18), height: window.innerHeight };
    });

    // 1. Load VRM 1.0 and VRM 0.x in 3-column (1680x960) rail dock directly above details.rws-unreg.
    await applyPack(vrm1ID);
    const vrm1Layout = await page.evaluate(() => {
      const slot = document.getElementById('rail-companion-slot');
      const unreg = document.querySelector('#rail details.rws-unreg');
      const rlist = document.getElementById('rlist');
      const hit = document.querySelector('.ws-companion-hit');
      const s = slot.getBoundingClientRect();
      const u = unreg.getBoundingClientRect();
      const l = rlist.getBoundingClientRect();
      const h = hit.getBoundingClientRect();
      return {
        unregText: unreg.querySelector('.unreg-lbl')?.textContent?.trim() || '',
        unregCount: unreg.querySelector('.unreg-count')?.textContent?.trim() || '',
        unregInsideRlist: rlist.contains(unreg),
        slotTop: s.top, slotBottom: s.bottom, slotW: s.width, slotH: s.height,
        unregTop: u.top, rlistBottom: l.bottom,
        hitLeft: h.left, hitBottom: h.bottom, slotLeft: s.left
      };
    });
    assert.equal(vrm1Layout.unregInsideRlist, false, 'details.rws-unreg must sit outside #rlist');
    assert.ok(Number(vrm1Layout.unregCount) >= 3, `expected >= 3 pending unregistered workspaces, got ${vrm1Layout.unregCount}`);
    assert.ok(vrm1Layout.slotTop >= vrm1Layout.rlistBottom - 1, 'slot must sit below #rlist');
    assert.ok(vrm1Layout.slotBottom <= vrm1Layout.unregTop + 1, 'slot must sit directly above details.rws-unreg');
    assert.ok(Math.abs(vrm1Layout.hitLeft - vrm1Layout.slotLeft) <= 2 && Math.abs(vrm1Layout.hitBottom - vrm1Layout.slotBottom) <= 2,
      `VRM hit must align with #rail-companion-slot: ${JSON.stringify(vrm1Layout)}`);

    const vrm1Full = shots ? await page.screenshot() : null;
    const vrm1Crop = shots ? await page.screenshot({ clip: await railBottomClip() }) : null;

    await applyPack(vrm0ID);
    const vrm0Full = shots ? await page.screenshot() : null;
    const vrm0Crop = shots ? await page.screenshot({ clip: await railBottomClip() }) : null;

    if (shots) {
      await composeGrid(browser, path.join(shots, '01-rail-dock-3col.png'),
        '01 · Left-Rail Bottom Dock (>= 1600px, 1680×960) — VRM 1.0 & VRM 0.x directly above 미등록 워크스페이스', [
          { label: 'VRM 1.0 Fixture — Full 3-Column (1680×960)', buf: vrm1Full, maxW: 960 },
          { label: 'VRM 1.0 — Rail Bottom Closeup', buf: vrm1Crop, maxW: 360 },
          { label: 'VRM 0.x Fixture — Full 3-Column (1680×960)', buf: vrm0Full, maxW: 960 },
          { label: 'VRM 0.x — Rail Bottom Closeup', buf: vrm0Crop, maxW: 360 }
        ], { cols: 2 });
    }

    await applyPack(vrm1ID);

    // 2. Scroll long session list to the bottom -> zero dead-zone occlusion, then expand details.rws-unreg.
    const scrollClosed = await page.evaluate(() => {
      const rlist = document.getElementById('rlist');
      rlist.scrollTop = rlist.scrollHeight;
      const rows = Array.from(rlist.querySelectorAll('.rs'));
      const last = rows[rows.length - 1];
      const lr = last.getBoundingClientRect();
      const sr = document.getElementById('rail-companion-slot').getBoundingClientRect();
      const hitEl = document.elementFromPoint(Math.floor(lr.left + lr.width / 2), Math.floor(lr.top + lr.height / 2));
      return {
        overflows: rlist.scrollHeight > rlist.clientHeight,
        lastRid: last.dataset.rid,
        gapToSlot: Math.round(sr.top - lr.bottom),
        clickable: !!(hitEl && last.contains(hitEl))
      };
    });
    assert.equal(scrollClosed.overflows, true, '#rlist must overflow to test bottom scroll');
    assert.equal(scrollClosed.clickable, true, 'last session row must be clickable above companion slot');
    assert.ok(scrollClosed.gapToSlot >= 8, `expected bottom scroll clearance above slot, got ${scrollClosed.gapToSlot}px`);
    const scrollClosedShot = shots ? await page.screenshot({ clip: await railFullClip() }) : null;

    await page.locator('#rail details.rws-unreg > summary').click();
    await page.waitForTimeout(350);
    const scrollOpen = await page.evaluate(() => {
      const rlist = document.getElementById('rlist');
      rlist.scrollTop = rlist.scrollHeight;
      const rows = Array.from(rlist.querySelectorAll('.rs'));
      const last = rows[rows.length - 1];
      const lr = last.getBoundingClientRect();
      const sr = document.getElementById('rail-companion-slot').getBoundingClientRect();
      const ur = document.querySelector('#rail details.rws-unreg').getBoundingClientRect();
      const hr = document.querySelector('.ws-companion-hit').getBoundingClientRect();
      const hitEl = document.elementFromPoint(Math.floor(lr.left + lr.width / 2), Math.floor(lr.top + lr.height / 2));
      return {
        unregOpen: document.querySelector('#rail details.rws-unreg').open,
        lastRid: last.dataset.rid,
        gapToSlot: Math.round(sr.top - lr.bottom),
        slotUnregOverlap: Math.round(sr.bottom - ur.top),
        hitDrift: Math.round(Math.abs(hr.bottom - sr.bottom)),
        clickable: !!(hitEl && last.contains(hitEl))
      };
    });
    assert.equal(scrollOpen.unregOpen, true);
    assert.equal(scrollOpen.clickable, true, 'last session row must stay clickable when .rws-unreg is expanded');
    assert.ok(scrollOpen.slotUnregOverlap <= 1, `slot must not overlap expanded .rws-unreg (${scrollOpen.slotUnregOverlap}px)`);
    assert.ok(scrollOpen.hitDrift <= 2, `VRM hit drifted after .rws-unreg expand (${scrollOpen.hitDrift}px)`);
    const scrollOpenShot = shots ? await page.screenshot({ clip: await railFullClip() }) : null;
    const scrollOpenFull = shots ? await page.screenshot() : null;

    if (shots) {
      await composeGrid(browser, path.join(shots, '02-rail-scroll-no-deadzone.png'),
        '02 · Long Session List Scrolled to Bottom — Zero Dead-Zone Occlusion & Clean Open 미등록 워크스페이스', [
          { label: `Scrolled bottom (${scrollClosed.lastRid}, clickable=true, +${scrollClosed.gapToSlot}px clearance)`, buf: scrollClosedShot, maxW: 360 },
          { label: `미등록 워크스페이스 expanded (${scrollOpen.lastRid} clickable=true, overlap=${scrollOpen.slotUnregOverlap}px)`, buf: scrollOpenShot, maxW: 360 },
          { label: 'Full 3-Column Context with Expanded 미등록 워크스페이스', buf: scrollOpenFull, maxW: 920 }
        ], { cols: 3 });
    }

    await page.locator('#rail details.rws-unreg > summary').click();
    await page.waitForTimeout(200);

    // 3. Narrow viewports (< 1600px): 1440x900 (2-col) and 720x900 (1-col) hide VRM & pause WebGL (0 fps).
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.waitForFunction(() => window.WsCompanion?.debugStage?.()?.vrmRailVisible === false, null, { timeout: 5000 });
    await page.waitForTimeout(200);
    const narrow1440 = await page.evaluate(() => ({
      st: window.WsCompanion.debugStage(),
      hitHidden: document.querySelector('.ws-companion-hit')?.hidden,
      slotDisplay: getComputedStyle(document.getElementById('rail-companion-slot')).display
    }));
    assert.equal(narrow1440.hitHidden, true);
    assert.equal(narrow1440.slotDisplay, 'none');
    assert.equal(narrow1440.st.vrm.paused, true);
    const framesA = narrow1440.st.vrm.framesRendered;
    await page.waitForTimeout(450);
    const framesB = await page.evaluate(() => window.WsCompanion.debugStage().vrm.framesRendered);
    assert.equal(framesB, framesA, 'narrow 1440px viewport must render 0 frames');
    const shot1440 = shots ? await page.screenshot() : null;

    // Even when the slide-out rail drawer opens below 1600px, VRM and slot remain strictly hidden.
    await page.evaluate(() => document.documentElement.classList.add('rail-open'));
    await page.waitForTimeout(200);
    const drawerCheck = await page.evaluate(() => ({
      hitHidden: document.querySelector('.ws-companion-hit')?.hidden,
      slotDisplay: getComputedStyle(document.getElementById('rail-companion-slot')).display,
      vrmRailVisible: window.WsCompanion.debugStage().vrmRailVisible
    }));
    assert.equal(drawerCheck.hitHidden, true, 'VRM must remain hidden even when narrow rail drawer opens');
    assert.equal(drawerCheck.slotDisplay, 'none', '#rail-companion-slot must remain display:none below 1600px');
    assert.equal(drawerCheck.vrmRailVisible, false);
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
    const shot720 = shots ? await page.screenshot() : null;

    if (shots) {
      await composeGrid(browser, path.join(shots, '03-narrow-hidden.png'),
        '03 · Narrow Viewports (< 1600px) — VRM Companion Hidden & WebGL Render Loop Paused (0 fps)', [
          { label: '2-Column Viewport (1440×900) — hidden=true, paused=true, slot=display:none', buf: shot1440, maxW: 960 },
          { label: '1-Column Viewport (720×900) — hidden=true, paused=true, slot=display:none', buf: shot720, maxW: 480 }
        ], { cols: 2 });
    }

    // 4. Widen back to 1680x960 -> verify FX badges (alert, sparkle, zzz) + right-click menu unclipped by .rail.
    await page.setViewportSize({ width: 1680, height: 960 });
    await page.waitForFunction(() => {
      const st = window.WsCompanion?.debugStage?.();
      return st && st.vrmRailVisible === true && !st.hidden && st.vrm && !st.vrm.paused;
    }, null, { timeout: 8000 });
    await page.waitForTimeout(250);

    const fxShots = {};
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
      await page.waitForTimeout(200);
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
      assert.ok(fxBox.fxRect.left >= fxBox.rootRect.left - 1 && fxBox.fxRect.top >= fxBox.rootRect.top - 1,
        `${fx} FX badge clipped: ${JSON.stringify(fxBox)}`);
      if (shots) fxShots[fx] = await page.screenshot({ clip: await railBottomClip() });
    }

    await page.locator('.ws-companion-character').click({ button: 'right' });
    await page.locator('.ws-companion-menu:not([hidden])').waitFor({ timeout: 3000 });
    await page.waitForTimeout(180);
    const menuBox = await page.evaluate(() => {
      const rail = document.getElementById('rail').getBoundingClientRect();
      const menu = document.querySelector('.ws-companion-menu');
      const mr = menu.getBoundingClientRect();
      return {
        inBody: document.body.contains(menu) && !document.getElementById('rail').contains(menu),
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
    assert.ok(menuBox.menuLeft >= menuBox.railRight - 2 && menuBox.menuRight <= menuBox.vw && menuBox.menuBottom <= menuBox.vh,
      `context menu clipped or trapped inside .rail: ${JSON.stringify(menuBox)}`);

    if (shots) {
      const fxMenuClip = await page.evaluate(() => {
        const rail = document.getElementById('rail').getBoundingClientRect();
        const slot = document.getElementById('rail-companion-slot').getBoundingClientRect();
        const top = Math.max(0, Math.floor(slot.top - 70));
        return {
          x: Math.max(0, Math.floor(rail.left - 8)),
          y: top,
          width: Math.min(window.innerWidth, Math.ceil(rail.width + 260)),
          height: Math.min(window.innerHeight - top, Math.ceil(rail.bottom - top + 8))
        };
      });
      const menuShot = await page.screenshot({ clip: fxMenuClip });
      await composeGrid(browser, path.join(shots, '04-rail-fx-and-menu.png'),
        '04 · Rail-Docked VRM Overlays in document.body — FX Badges & Context Menu Unclipped by .rail { overflow: hidden }', [
          { label: 'FX: alert (!)', buf: fxShots.alert, maxW: 360 },
          { label: 'FX: sparkle (✦)', buf: fxShots.sparkle, maxW: 360 },
          { label: 'FX: zzz (Zzz)', buf: fxShots.zzz, maxW: 360 },
          { label: 'Context Menu (right-click) — Unclipped outside .rail', buf: menuShot, maxW: 580 }
        ], { cols: 4 });
    }

    assert.deepEqual(pageErrors, [], `unexpected page errors: ${JSON.stringify(pageErrors)}`);
    console.log('companion vrm rail dock browser: ok', JSON.stringify({
      unregCount: vrm1Layout.unregCount,
      scrollClosed,
      scrollOpen,
      menuOutsideRail: menuBox.menuLeft >= menuBox.railRight - 2
    }));
  } finally {
    await browser.close();
  }
})().catch(err => {
  console.error(err);
  process.exit(1);
});
