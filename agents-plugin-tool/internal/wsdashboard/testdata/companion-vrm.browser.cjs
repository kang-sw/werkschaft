// Run by TestCompanionVrmBrowser against the live Werkschaft dashboard shell.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const zlib = require('node:zlib');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [base, shots, vrm0ID, vrm1ID, phase = '1'] = process.argv.slice(2);
const EXPRESSIONS = ['idle', 'idle_doze', 'focused', 'half_lid', 'oo', 'cry', 'happy'];

// Count opaque/semi-opaque pixels in a Playwright PNG buffer (handles 8-bit RGBA PNG).
function countOpaquePngPixels(buf) {
  let pos = 8, width = 0, height = 0, colorType = 6;
  const idat = [];
  while (pos < buf.length) {
    const len = buf.readUInt32BE(pos);
    const type = buf.toString('ascii', pos + 4, pos + 8);
    const data = buf.subarray(pos + 8, pos + 8 + len);
    if (type === 'IHDR') {
      width = data.readUInt32BE(0);
      height = data.readUInt32BE(4);
      colorType = data[9];
    } else if (type === 'IDAT') {
      idat.push(data);
    } else if (type === 'IEND') break;
    pos += 12 + len;
  }
  const raw = zlib.inflateSync(Buffer.concat(idat));
  const bpp = colorType === 6 ? 4 : colorType === 2 ? 3 : 4;
  const stride = width * bpp;
  const prev = new Uint8Array(stride);
  const cur = new Uint8Array(stride);
  let lit = 0;
  for (let y = 0; y < height; y++) {
    const filter = raw[y * (stride + 1)];
    const rowOff = y * (stride + 1) + 1;
    for (let i = 0; i < stride; i++) {
      const x = raw[rowOff + i];
      const a = i >= bpp ? cur[i - bpp] : 0;
      const b = prev[i];
      const c = i >= bpp ? prev[i - bpp] : 0;
      let v = x;
      if (filter === 1) v = (x + a) & 255;
      else if (filter === 2) v = (x + b) & 255;
      else if (filter === 3) v = (x + ((a + b) >> 1)) & 255;
      else if (filter === 4) {
        const p = a + b - c;
        const pa = Math.abs(p - a), pb = Math.abs(p - b), pc = Math.abs(p - c);
        const pr = (pa <= pb && pa <= pc) ? a : (pb <= pc ? b : c);
        v = (x + pr) & 255;
      }
      cur[i] = v;
    }
    if (bpp === 4) {
      for (let i = 3; i < stride; i += 4) if (cur[i] > 20) lit++;
    } else {
      for (let i = 0; i < stride; i += 3) if (cur[i] + cur[i + 1] + cur[i + 2] > 24) lit++;
    }
    prev.set(cur);
  }
  return { width, height, lit };
}

async function saveShot(page, name) {
  if (!shots) return;
  fs.mkdirSync(shots, { recursive: true });
  await page.screenshot({ path: path.join(shots, name + '.png') });
}

async function saveDockShot(page, name) {
  if (!shots) return;
  fs.mkdirSync(shots, { recursive: true });
  const box = await page.evaluate(() => {
    const el = document.querySelector('.ws-companion-hit');
    if (!el) return null;
    const r = el.getBoundingClientRect();
    return {
      x: Math.max(0, Math.floor(r.left - 24)),
      y: Math.max(0, Math.floor(r.top - 24)),
      width: Math.min(window.innerWidth, Math.ceil(r.right + 24)) - Math.max(0, Math.floor(r.left - 24)),
      height: Math.min(window.innerHeight, Math.ceil(r.bottom + 24)) - Math.max(0, Math.floor(r.top - 24))
    };
  });
  if (box && box.width > 0 && box.height > 0) {
    await page.screenshot({ path: path.join(shots, name + '.png'), clip: box });
  }
}

(async () => {
  const browser = await chromium.launch({
    headless: true,
    args: ['--use-gl=angle', '--use-angle=swiftshader', '--enable-unsafe-swiftshader', '--ignore-gpu-blocklist']
  });
  try {
    const context = await browser.newContext({ viewport: { width: 1680, height: 960 }, colorScheme: 'dark' });
    await context.addInitScript(() => {
      if (self !== top) return;
      const workers = navigator.serviceWorker;
      if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
      window.__webglContextsCreated = 0;
      window.__lastWebglCtx = null;
      const origGetContext = HTMLCanvasElement.prototype.getContext;
      HTMLCanvasElement.prototype.getContext = function (type, ...args) {
        const res = origGetContext.call(this, type, ...args);
        if (res && (type === 'webgl' || type === 'webgl2')) {
          window.__webglContextsCreated++;
          window.__lastWebglCtx = res;
        }
        return res;
      };
    });

    const vrmRuntimeRequests = [];
    const vrmModelRequests = [];
    const pageErrors = [];
    const consoleErrors = [];
    const badResponses = [];

    const page = await context.newPage();
    page.on('pageerror', e => pageErrors.push(e.message));
    page.on('console', msg => {
      if (msg.type() === 'error') {
        const txt = msg.text();
        if (!txt.includes('404 (Not Found)')) consoleErrors.push(txt);
      }
    });
    page.on('response', res => {
      const p = new URL(res.url()).pathname;
      if (res.status() >= 400 && p !== '/favicon.ico' && p !== '/tracker/stores') badResponses.push(`${res.status()} ${p}`);
    });
    page.on('request', req => {
      const p = new URL(req.url()).pathname;
      if (/^\/s\/companion-vrm-[0-9a-f]+\.mjs$/.test(p)) vrmRuntimeRequests.push(p);
      if (p.endsWith('.vrm')) vrmModelRequests.push(p);
    });

    // 1. Companion disabled: 0 bytes of VRM runtime fetched, 0 WebGL contexts created.
    await page.goto(base + '/s/livesession');
    await page.frameLocator('.sframe.on').locator('#grid').waitFor();
    await page.waitForTimeout(400);
    assert.equal(vrmRuntimeRequests.length, 0, 'disabled companion must not fetch VRM runtime');
    assert.equal(await page.evaluate(() => window.__webglContextsCreated), 0, 'disabled companion must create 0 WebGL contexts');

    const applyConfig = cfg => page.evaluate(detail => {
      document.dispatchEvent(new CustomEvent('ws-companion-config', { detail }));
    }, cfg);
    const sendCommand = cmd => page.evaluate(detail => {
      document.dispatchEvent(new CustomEvent('ws-companion-command', { detail }));
    }, cmd);

    // 2. Enable companion on default 2D pack: still 0 VRM runtime fetches and 0 WebGL contexts.
    await applyConfig({ enabled: true, director: 'rules', pack: 'default', preset: 'werkschaft-d1' });
    await page.locator('.ws-companion-hit:not([hidden])').waitFor();
    await page.waitForTimeout(500);
    assert.equal(vrmRuntimeRequests.length, 0, '2D pack must not fetch VRM runtime');
    assert.equal(await page.evaluate(() => window.__webglContextsCreated), 0, '2D pack must create 0 WebGL contexts');
    assert.equal(await page.evaluate(() => window.WsCompanion.debugStage().renderer), '2d');

    const vrmPacks = [
      vrm1ID ? { id: vrm1ID, label: 'vrm1', expectVrm0: false } : null,
      vrm0ID ? { id: vrm0ID, label: 'vrm0', expectVrm0: true } : null
    ].filter(Boolean);
    assert.equal(vrmPacks.length, 2, 'both VRM 1.0 and VRM 0.x fixtures must be staged');

    // 3. Load both VRM packs (1.0 and 0.x), verify borderless Digital Dissolve CSS,
    //    rendered pixels, 7 unified expressions, and settings modal 2D-wardrobe hiding.
    for (const pack of vrmPacks) {
      await applyConfig({ enabled: true, director: 'rules', pack: pack.id, preset: '' });
      try {
        await page.waitForFunction(id => {
          const st = window.WsCompanion.debugStage();
          return st && st.renderer === 'vrm' && st.pack === id && st.vrm && st.vrm.loaded && st.vrm.framesRendered >= 3;
        }, pack.id, { timeout: 25000 });
      } catch (err) {
        const dbg = await page.evaluate(async () => ({
          stage: window.WsCompanion?.debugStage?.(),
          overlay: document.querySelector('.ws-companion-vrm-overlay')?.textContent || '',
          catalog: await fetch('/api/companion/packs').then(r => r.json()).catch(e => String(e))
        }));
        throw new Error(`wait for ${pack.id} failed: ${err.message}\ndbg=${JSON.stringify(dbg)}\npageErrors=${JSON.stringify(pageErrors)}\nconsoleErrors=${JSON.stringify(consoleErrors)}\nbadResponses=${JSON.stringify(badResponses)}\nvrmRuntimeRequests=${JSON.stringify(vrmRuntimeRequests)}`);
      }

      const visuals = await page.evaluate(() => {
        const wrap = document.querySelector('.ws-companion-vrm-wrap');
        const cwrap = document.querySelector('.ws-companion-vrm-canvas-wrap');
        const grid = document.querySelector('.ws-dissolve-grid');
        const slot = document.getElementById('rail-companion-slot');
        const hit = document.querySelector('.ws-companion-hit');
        const rlist = document.getElementById('rlist');
        const unreg = document.querySelector('#rail details.rws-unreg');
        const rfoot = document.querySelector('#rail .rfoot');
        const wStyle = getComputedStyle(wrap);
        const cwStyle = getComputedStyle(cwrap);
        const rectOf = el => {
          if (!el) return null;
          const r = el.getBoundingClientRect();
          return { left: r.left, top: r.top, right: r.right, bottom: r.bottom, width: r.width, height: r.height };
        };
        return {
          wrapBg: wStyle.backgroundColor,
          wrapContain: wStyle.contain,
          maskImage: cwStyle.maskImage || cwStyle.webkitMaskImage || '',
          hasDissolveGrid: !!grid,
          slotHidden: slot ? slot.hidden : true,
          slotRect: rectOf(slot),
          hitRect: rectOf(hit),
          rlistRect: rectOf(rlist),
          railRect: rectOf(document.getElementById('rail')),
          panelRect: rectOf(document.querySelector('#rail .rail-session-panel')),
          aboveRect: rectOf(rfoot || unreg || rlist),
          unregInsideRlist: !!(unreg && rlist && rlist.contains(unreg)),
          stage: window.WsCompanion.debugStage()
        };
      });
      const st = visuals.stage;
      assert.equal(st.vrm.isVrm0, pack.expectVrm0, `${pack.label} metaVersion mismatch: ${JSON.stringify(st.vrm)}`);
      assert.equal(st.vrm.pose, 'stand');
      assert.equal(st.vrm.prop, null, `${pack.label} ground-zero baseline must not show 3D props`);
      assert.equal(st.vrm.framing?.usedBox3, true, `${pack.label} must frame bust using THREE.Box3`);
      assert.ok(st.vrm.framing.topY >= st.vrm.framing.headY + 0.18, `${pack.label} framing topY must include head/hair margin`);
      assert.equal(st.dock, 'rail-slot', `${pack.label} must dock in left rail slot at >= 1600px`);
      assert.equal(st.vrmRailVisible, true);
      assert.equal(st.avoiding, false);
      assert.equal(visuals.unregInsideRlist, false, 'details.rws-unreg must sit outside #rlist');
      assert.equal(visuals.slotHidden, false, '#rail-companion-slot must be visible at >= 1600px');
      assert.ok(visuals.slotRect && visuals.slotRect.width >= 220 && visuals.slotRect.width <= 360 && visuals.slotRect.height >= 160,
        `unexpected rail slot rect: ${JSON.stringify(visuals.slotRect)}`);
      assert.ok(visuals.slotRect.top >= visuals.rlistRect.bottom - 1,
        `slot (${JSON.stringify(visuals.slotRect)}) must sit below #rlist (${JSON.stringify(visuals.rlistRect)})`);
      assert.ok(visuals.panelRect && visuals.slotRect.top >= visuals.panelRect.bottom - 1,
        `slot (${JSON.stringify(visuals.slotRect)}) must sit below .rail-session-panel (${JSON.stringify(visuals.panelRect)})`);
      assert.ok(visuals.aboveRect && visuals.slotRect.top >= visuals.aboveRect.bottom - 1,
        `slot (${JSON.stringify(visuals.slotRect)}) must sit below unreg/footer (${JSON.stringify(visuals.aboveRect)})`);
      assert.ok(visuals.railRect && Math.abs(visuals.slotRect.bottom - visuals.railRect.bottom) <= 1,
        `slot (${JSON.stringify(visuals.slotRect)}) must sit flush at #rail bottom (${JSON.stringify(visuals.railRect)})`);
      assert.ok(Math.abs(visuals.hitRect.left - visuals.slotRect.left) <= 2 &&
        Math.abs(visuals.hitRect.bottom - visuals.slotRect.bottom) <= 2 &&
        Math.abs(visuals.hitRect.width - (visuals.slotRect.width - 4)) <= 2 &&
        Math.abs(visuals.hitRect.height - visuals.slotRect.height) <= 2,
        `VRM hit rect ${JSON.stringify(visuals.hitRect)} must align with #rail-companion-slot minus 4px splitter clearance ${JSON.stringify(visuals.slotRect)}`);
      assert.equal(visuals.wrapBg, 'rgba(0, 0, 0, 0)', 'VRM wrap must have transparent borderless background');
      assert.match(visuals.wrapContain, /layout/, 'VRM wrap must declare contain: layout style');
      assert.match(visuals.maskImage, /linear-gradient/, 'VRM canvas wrap must apply bottom Digital Dissolve mask');
      assert.equal(visuals.hasDissolveGrid, true, 'Digital Dissolve pixel scatter element must exist');
      assert.equal(vrmRuntimeRequests.length, 1, 'VRM runtime must lazy-load once and stay cached');

      // Check non-transparent pixels actually rendered into the composited WebGL canvas.
      const canvasPng = await page.locator('.ws-companion-vrm-canvas').screenshot({ omitBackground: true });
      const pixelCheck = countOpaquePngPixels(canvasPng);
      assert.ok(pixelCheck.lit > 1200, `${pack.label} WebGL canvas appears blank (${JSON.stringify(pixelCheck)})`);
      await saveShot(page, `${pack.label}-dock-full`);
      await saveDockShot(page, `${pack.label}-stand-idle`);

      // Cycle all 7 unified expressions via directed commands.
      for (const expr of EXPRESSIONS) {
        await sendCommand({ sessionId: 'livesession', expression: expr, ttl_ms: 1500 });
        await page.waitForTimeout(320);
        const cur = await page.evaluate(() => window.WsCompanion.debugStage()?.vrm?.expression);
        assert.equal(cur, expr, `${pack.label} failed to apply expression ${expr} (got ${cur})`);
        await saveDockShot(page, `${pack.label}-expr-${expr}`);
      }
      await saveDockShot(page, `${pack.label}-happy`);

      // Ground-zero baseline: verify FX badges render their own glyphs, stay strictly clamped, and clear cleanly on idle.
      const fxExpect = { alert: '!', sparkle: '✦', zzz: 'Zzz' };
      for (const fxName of ['alert', 'sparkle', 'zzz']) {
        await sendCommand({ sessionId: 'livesession', fx: fxName, ttl_ms: 1800 });
        await page.waitForTimeout(260);
        const fxCheck = await page.evaluate(() => {
          const st = window.WsCompanion.debugStage();
          const root = document.querySelector('.ws-companion');
          const fxEl = document.querySelector('.ws-companion-fx');
          const rootRect = root ? root.getBoundingClientRect() : null;
          const fxRect = fxEl ? fxEl.getBoundingClientRect() : null;
          return {
            st,
            fxHidden: !fxEl || fxEl.hidden,
            fxText: fxEl ? fxEl.textContent : '',
            rootRect: rootRect ? { left: rootRect.left, top: rootRect.top, right: rootRect.right, bottom: rootRect.bottom } : null,
            fxRect: fxRect ? { left: fxRect.left, top: fxRect.top, right: fxRect.right, bottom: fxRect.bottom, width: fxRect.width, height: fxRect.height } : null
          };
        });
        assert.equal(fxCheck.fxHidden, false, `${pack.label}/${fxName}: FX element should be visible`);
        assert.ok(fxCheck.fxText.includes(fxExpect[fxName]),
          `${pack.label}/${fxName}: expected FX text containing ${fxExpect[fxName]}, got ${JSON.stringify(fxCheck.fxText)}`);
        assert.ok(fxCheck.fxRect && fxCheck.rootRect, `${pack.label}/${fxName}: missing FX or root rect`);
        assert.ok(fxCheck.fxRect.top >= fxCheck.rootRect.top - 1,
          `${pack.label}/${fxName}: FX clipped top edge: fx=${JSON.stringify(fxCheck.fxRect)} root=${JSON.stringify(fxCheck.rootRect)}`);
        assert.ok(fxCheck.fxRect.left >= fxCheck.rootRect.left - 1,
          `${pack.label}/${fxName}: FX clipped left edge: fx=${JSON.stringify(fxCheck.fxRect)} root=${JSON.stringify(fxCheck.rootRect)}`);
        assert.ok(fxCheck.fxRect.right <= fxCheck.rootRect.right + 1,
          `${pack.label}/${fxName}: FX clipped right edge: fx=${JSON.stringify(fxCheck.fxRect)} root=${JSON.stringify(fxCheck.rootRect)}`);
        assert.equal(fxCheck.st.vrm.prop, null, `${pack.label}/${fxName}: ground-zero baseline must not spawn 3D props`);
        assert.ok(Math.abs(fxCheck.st.position.y - fxCheck.st.home.y) < 1,
          `${pack.label}/${fxName}: VRM companion must not hop vertically`);
        await saveDockShot(page, `${pack.label}-fx-${fxName}`);
      }
      await sendCommand({ sessionId: 'livesession', expression: 'idle', ttl_ms: 800 });
      await page.waitForFunction(() => {
        const fxEl = document.querySelector('.ws-companion-fx');
        return !fxEl || fxEl.hidden;
      }, null, { timeout: 3000 });

      // Verify gear routes to Dashboard Settings (/settings?section=companion)
      // without the retired floating modal, and 2D paper-doll wardrobe controls
      // are replaced by the VRM-active notice.
      await page.evaluate(async packId => {
        for (const [key, value] of [['dashboard.companion_enabled', true], ['dashboard.companion_pack', packId]]) {
          const res = await fetch('/api/config/set', {
            method: 'POST', credentials: 'same-origin',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ scope: 'global', key, value })
          });
          if (!res.ok) throw new Error('set ' + key + ' failed: ' + res.status);
        }
      }, pack.id);
      await page.locator('.ws-companion-gear').click({ force: true });
      assert.equal(await page.locator('dialog.ws-companion-settings').count(), 0, 'retired floating modal must not render');
      await page.locator('#workflow-settings[open]').waitFor();
      const settingsFrame = page.frameLocator('#workflow-settings[open] > iframe');
      await settingsFrame.locator('[data-companion-settings-panel]').waitFor();
      await settingsFrame.locator('[data-companion-wardrobe-vrm-note]').waitFor();
      assert.equal(await settingsFrame.locator('[data-companion-wardrobe-grid]').count(), 0,
        `VRM Dashboard Settings must hide 2D wardrobe controls for ${pack.label}`);
      await settingsFrame.locator('[data-settings-close]').click();
      await page.locator('#workflow-settings').waitFor({ state: 'hidden' });
      await page.mouse.move(120, 120);
      await page.evaluate(() => document.activeElement?.blur?.());
      await page.waitForTimeout(120);
    }

    // 4. Rail-docked stability: hovering #bcol does NOT slide, dragging does NOT detach,
    //    context menu opens outside .rail clip, rail refresh preserves WebGL context,
    //    and narrowing < 1600px hides + pauses cleanly (0 frames) before resuming >= 1600px.
    const frame = page.frameLocator('.sframe.on');
    const rightColLoc = frame.locator('#bcol');
    const rcBox = await rightColLoc.boundingBox();
    assert.ok(rcBox && rcBox.width > 40, 'expected visible #bcol in live session frame');
    const homeDockBefore = await page.locator('.ws-companion-hit').boundingBox();
    await page.mouse.move(rcBox.x + rcBox.width / 2, rcBox.y + Math.min(200, rcBox.height / 2));
    await page.waitForTimeout(350);
    const dockAfterRightHover = await page.locator('.ws-companion-hit').boundingBox();
    assert.equal(await page.evaluate(() => window.WsCompanion.debugStage()?.avoiding), false, 'rail-docked VRM must not run right-col avoidance');
    assert.ok(Math.abs(dockAfterRightHover.x - homeDockBefore.x) < 2, 'rail-docked VRM must stay anchored on right-col hover');

    // Attempt free drag on rail-docked VRM -> must stay locked in rail slot.
    await page.mouse.move(homeDockBefore.x + homeDockBefore.width * 0.5, homeDockBefore.y + homeDockBefore.height * 0.5);
    await page.mouse.down();
    await page.mouse.move(homeDockBefore.x + 360, homeDockBefore.y - 200, { steps: 6 });
    await page.mouse.up();
    await page.waitForTimeout(200);
    const dockAfterDrag = await page.locator('.ws-companion-hit').boundingBox();
    assert.ok(Math.abs(dockAfterDrag.x - homeDockBefore.x) < 2 && Math.abs(dockAfterDrag.y - homeDockBefore.y) < 2,
      `rail-docked VRM must not detach on drag: before=${JSON.stringify(homeDockBefore)}, after=${JSON.stringify(dockAfterDrag)}`);

    // Right-click opens context menu in document.body to the right of the rail with zero clipping.
    await page.locator('.ws-companion-character').click({ button: 'right' });
    await page.locator('.ws-companion-menu:not([hidden])').waitFor({ timeout: 3000 });
    const menuCheck = await page.evaluate(() => {
      const rail = document.getElementById('rail').getBoundingClientRect();
      const menu = document.querySelector('.ws-companion-menu').getBoundingClientRect();
      return { railRight: rail.right, menuLeft: menu.left, menuRight: menu.right, menuWidth: menu.width, menuHeight: menu.height, vw: window.innerWidth };
    });
    assert.ok(menuCheck.menuLeft >= menuCheck.railRight - 2 && menuCheck.menuRight <= menuCheck.vw && menuCheck.menuWidth > 80,
      `context menu must render unclipped beside the rail: ${JSON.stringify(menuCheck)}`);
    await page.keyboard.press('Escape');

    // Rail refresh must never duplicate .rws-unreg and must never recreate/lose the WebGL context.
    const ctxCountBeforeRefresh = await page.evaluate(() => window.__webglContextsCreated);
    await page.locator('#railref').click();
    await page.waitForTimeout(400);
    const afterRefresh = await page.evaluate(() => ({
      ctxCount: window.__webglContextsCreated,
      unregCount: document.querySelectorAll('#rail details.rws-unreg').length,
      slotCount: document.querySelectorAll('#rail #rail-companion-slot').length,
      slotHidden: document.getElementById('rail-companion-slot')?.hidden,
      contextLost: window.WsCompanion.debugStage()?.vrm?.contextLost
    }));
    assert.equal(afterRefresh.ctxCount, ctxCountBeforeRefresh, 'rail refresh must never recreate WebGL context');
    assert.ok(afterRefresh.unregCount <= 1, `rail refresh duplicated details.rws-unreg (${afterRefresh.unregCount})`);
    assert.equal(afterRefresh.slotCount, 1, 'rail refresh must keep exactly one #rail-companion-slot');
    assert.equal(afterRefresh.slotHidden, false, '#rail-companion-slot must remain visible after rail refresh');
    assert.equal(afterRefresh.contextLost, false);

    // Narrow viewport (< 1600px) hides VRM and pauses WebGL rendering (0 frames).
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.waitForFunction(() => window.WsCompanion.debugStage()?.vrmRailVisible === false, null, { timeout: 5000 });
    const narrowState = await page.evaluate(() => ({
      st: window.WsCompanion.debugStage(),
      hitHidden: document.querySelector('.ws-companion-hit')?.hidden,
      slotDisplay: getComputedStyle(document.getElementById('rail-companion-slot')).display
    }));
    assert.equal(narrowState.hitHidden, true, 'VRM hit must be hidden below 1600px');
    assert.equal(narrowState.slotDisplay, 'none', '#rail-companion-slot must be display:none below 1600px');
    assert.equal(narrowState.st.vrm.paused, true, 'VRM render loop must pause below 1600px');
    const narrowFramesA = narrowState.st.vrm.framesRendered;
    await page.waitForTimeout(700);
    const narrowFramesB = await page.evaluate(() => window.WsCompanion.debugStage().vrm.framesRendered);
    assert.equal(narrowFramesB, narrowFramesA, `VRM must render 0 frames below 1600px (got ${narrowFramesB - narrowFramesA})`);

    // Widen back to >= 1600px -> resumes smoothly without recreating WebGL context.
    await page.setViewportSize({ width: 1680, height: 960 });
    await page.waitForFunction(prev => {
      const st = window.WsCompanion.debugStage();
      return st && st.vrmRailVisible === true && !st.hidden && st.vrm && !st.vrm.paused && st.vrm.framesRendered > prev + 2;
    }, narrowFramesB, { timeout: 8000 });
    assert.equal(await page.evaluate(() => window.__webglContextsCreated), ctxCountBeforeRefresh, 'widening back must reuse existing WebGL context');

    // 5. Simulated WebGL context loss & restore without network refetch of .vrm.
    const fetchesBeforeLoss = vrmModelRequests.length;
    const lostOk = await page.evaluate(() => {
      const gl = window.__lastWebglCtx;
      const ext = gl && gl.getExtension('WEBGL_lose_context');
      if (!ext) return false;
      window.__loseExt = ext;
      ext.loseContext();
      return true;
    });
    assert.ok(lostOk, 'WEBGL_lose_context extension unavailable');
    await page.waitForFunction(() => {
      const st = window.WsCompanion.debugStage()?.vrm;
      const ov = document.querySelector('.ws-companion-vrm-overlay');
      return st && st.contextLost === true && ov && !ov.hidden;
    }, null, { timeout: 5000 });
    const framesWhileLostA = await page.evaluate(() => window.WsCompanion.debugStage().vrm.framesRendered);
    await page.waitForTimeout(250);
    const framesWhileLostB = await page.evaluate(() => window.WsCompanion.debugStage().vrm.framesRendered);
    assert.equal(framesWhileLostB, framesWhileLostA, 'must not render frames while WebGL context is lost');

    await page.evaluate(() => window.__loseExt.restoreContext());
    await page.waitForFunction(prev => {
      const st = window.WsCompanion.debugStage()?.vrm;
      const ov = document.querySelector('.ws-companion-vrm-overlay');
      return st && !st.contextLost && st.loaded && (!ov || ov.hidden) && st.framesRendered > prev + 2;
    }, framesWhileLostB, { timeout: 15000 });
    assert.equal(vrmModelRequests.length, fetchesBeforeLoss, 'context restore must not refetch .vrm over network');
    assert.equal(await page.evaluate(() => window.WsCompanion.debugStage().vrm.modelFetchCount), 1, 'adapter modelFetchCount must stay 1');

    // 6. Adaptive FPS measurement: active (~30 fps), idle after 5s (~6 fps), hidden (0 fps over 2s).
    await page.waitForTimeout(1600); // let prior directed command TTL expire
    const activeStart = await page.evaluate(() => ({
      t: performance.now(),
      frames: window.WsCompanion.debugStage().vrm.framesRendered
    }));
    for (let i = 0; i < 15; i++) {
      await page.mouse.move(140 + i * 8, 220 + (i % 3) * 10);
      await page.waitForTimeout(100);
    }
    const activeEnd = await page.evaluate(() => ({
      t: performance.now(),
      frames: window.WsCompanion.debugStage().vrm.framesRendered,
      idleMode: window.WsCompanion.debugStage().vrmIdleMode
    }));
    const activeFps = (activeEnd.frames - activeStart.frames) / ((activeEnd.t - activeStart.t) / 1000);
    assert.equal(activeEnd.idleMode, false, 'active motion must not enter idle mode');

    // Wait >5s completely still so idle throttle kicks in.
    await page.waitForFunction(() => window.WsCompanion.debugStage()?.vrmIdleMode === true, null, { timeout: 8000 });
    await page.waitForTimeout(300);
    const idleStart = await page.evaluate(() => ({
      t: performance.now(),
      frames: window.WsCompanion.debugStage().vrm.framesRendered
    }));
    await page.waitForTimeout(2000);
    const idleEnd = await page.evaluate(() => ({
      t: performance.now(),
      frames: window.WsCompanion.debugStage().vrm.framesRendered,
      idleMode: window.WsCompanion.debugStage().vrmIdleMode
    }));
    const idleFps = (idleEnd.frames - idleStart.frames) / ((idleEnd.t - idleStart.t) / 1000);
    assert.equal(idleEnd.idleMode, true, 'must stay in idle mode while still');

    // Simulate document.hidden === true via visibilitychange and verify 0 frames over 2s.
    await page.evaluate(() => {
      Object.defineProperty(document, 'hidden', { configurable: true, get: () => true });
      Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'hidden' });
      document.dispatchEvent(new Event('visibilitychange'));
    });
    await page.waitForTimeout(150);
    const hiddenStart = await page.evaluate(() => window.WsCompanion.debugStage().vrm.framesRendered);
    await page.waitForTimeout(2000);
    const hiddenEnd = await page.evaluate(() => window.WsCompanion.debugStage().vrm.framesRendered);
    const hiddenFrames = hiddenEnd - hiddenStart;
    await page.evaluate(() => {
      Object.defineProperty(document, 'hidden', { configurable: true, get: () => false });
      Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'visible' });
      document.dispatchEvent(new Event('visibilitychange'));
    });

    assert.equal(hiddenFrames, 0, `expected 0 frames while hidden, got ${hiddenFrames}`);
    assert.ok(idleFps >= 4.8 && idleFps <= 7.2, `idleFps=${idleFps.toFixed(2)} outside 6 ±20% (4.8..7.2)`);
    assert.ok(activeFps >= 24 && activeFps <= 36, `activeFps=${activeFps.toFixed(2)} outside 30 ±20% (24..36)`);

    // 7. Switch back to 2D pack and (in Phase 2) run 10x VRM <-> 2D switch soak check.
    let switchCycles = 1;
    if (phase === '2') {
      switchCycles = 10;
      for (let cycle = 0; cycle < switchCycles; cycle++) {
        await applyConfig({ enabled: true, director: 'rules', pack: 'default', preset: 'werkschaft-d1' });
        await page.waitForFunction(() => window.WsCompanion.debugStage()?.renderer === '2d', null, { timeout: 5000 });
        await applyConfig({ enabled: true, director: 'rules', pack: vrm1ID, preset: '' });
        await page.waitForFunction(() => {
          const st = window.WsCompanion.debugStage();
          return st && st.renderer === 'vrm' && st.vrm && st.vrm.loaded && st.vrm.framesRendered >= 2;
        }, null, { timeout: 15000 });
      }
    }
    await applyConfig({ enabled: true, director: 'rules', pack: 'default', preset: 'werkschaft-d1' });
    await page.waitForFunction(() => window.WsCompanion.debugStage()?.renderer === '2d', null, { timeout: 5000 });

    assert.deepEqual(badResponses, [], `unexpected 4xx/5xx responses: ${badResponses}`);
    assert.deepEqual(pageErrors, [], 'unexpected page errors');
    assert.deepEqual(consoleErrors, [], 'unexpected console errors');
    console.log('companion vrm browser: ok', JSON.stringify({
      phase,
      packsTested: vrmPacks.map(p => p.label),
      switchCycles,
      activeFps: Number(activeFps.toFixed(2)),
      idleFps: Number(idleFps.toFixed(2)),
      hiddenFrames,
      vrmRuntimeRequests: vrmRuntimeRequests.length,
      vrmModelRequests: vrmModelRequests.length
    }));
  } finally {
    await browser.close();
  }
})().catch(err => {
  console.error(err);
  process.exit(1);
});
