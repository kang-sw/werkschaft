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
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark' });
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
        const wStyle = getComputedStyle(wrap);
        const cwStyle = getComputedStyle(cwrap);
        return {
          wrapBg: wStyle.backgroundColor,
          wrapContain: wStyle.contain,
          maskImage: cwStyle.maskImage || cwStyle.webkitMaskImage || '',
          hasDissolveGrid: !!grid,
          stage: window.WsCompanion.debugStage()
        };
      });
      const st = visuals.stage;
      assert.equal(st.vrm.isVrm0, pack.expectVrm0, `${pack.label} metaVersion mismatch: ${JSON.stringify(st.vrm)}`);
      assert.equal(st.vrm.pose, 'stand');
      assert.equal(st.size, 240);
      assert.equal(st.vrmHeight, 280);
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
        await page.waitForTimeout(180);
        const cur = await page.evaluate(() => window.WsCompanion.debugStage()?.vrm?.expression);
        assert.equal(cur, expr, `${pack.label} failed to apply expression ${expr} (got ${cur})`);
      }
      await saveDockShot(page, `${pack.label}-happy`);

      // Verify 2D paper-doll wardrobe controls are hidden in settings modal.
      await page.locator('.ws-companion-gear').click({ force: true });
      await page.locator('dialog.ws-companion-settings[open]').waitFor();
      const modalLabels = await page.evaluate(() =>
        Array.from(document.querySelectorAll('.ws-companion-settings-label')).map(el => el.textContent.trim())
      );
      for (const forbidden of ['Preset', 'Outfit', 'Hair', 'Eyes', 'Attachments', 'Palette']) {
        assert.ok(!modalLabels.includes(forbidden), `VRM settings modal must hide 2D control ${forbidden}`);
      }
      await page.keyboard.press('Escape');
      await page.mouse.move(120, 120);
      await page.evaluate(() => document.activeElement?.blur?.());
      await page.waitForTimeout(120);
    }

    // 4. Right-column (#bcol / #bgal) hover slide avoidance & return home.
    const frame = page.frameLocator('.sframe.on');
    const rightColLoc = frame.locator('#bcol');
    const rcBox = await rightColLoc.boundingBox();
    assert.ok(rcBox && rcBox.width > 40, 'expected visible #bcol in live session frame');
    const homeDockBefore = await page.locator('.ws-companion-hit').boundingBox();
    await page.mouse.move(rcBox.x + rcBox.width / 2, rcBox.y + Math.min(200, rcBox.height / 2));
    await page.waitForFunction(() => window.WsCompanion.debugStage()?.avoiding === true, null, { timeout: 5000 });
    await page.waitForTimeout(550);
    const dockAvoiding = await page.locator('.ws-companion-hit').boundingBox();
    const intersects = dockAvoiding.x + dockAvoiding.width > rcBox.x + 1 && dockAvoiding.x < rcBox.x + rcBox.width - 1;
    assert.equal(intersects, false, `avoiding dock ${JSON.stringify(dockAvoiding)} still intersects right-col ${JSON.stringify(rcBox)}`);
    await saveShot(page, 'vrm-avoiding-right-col');

    // Move mouse away from right-col -> returns home.
    await page.mouse.move(120, 120);
    await page.waitForFunction(() => window.WsCompanion.debugStage()?.avoiding === false, null, { timeout: 5000 });
    await page.waitForTimeout(550);
    const dockReturned = await page.locator('.ws-companion-hit').boundingBox();
    assert.ok(Math.abs(dockReturned.x - homeDockBefore.x) < 12,
      `dock did not return home: before=${homeDockBefore.x}, after=${dockReturned.x}`);

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

    // 7. Switch back to 2D pack and verify clean disposal + no errors.
    await applyConfig({ enabled: true, director: 'rules', pack: 'default', preset: 'werkschaft-d1' });
    await page.waitForFunction(() => window.WsCompanion.debugStage()?.renderer === '2d', null, { timeout: 5000 });

    assert.deepEqual(badResponses, [], `unexpected 4xx/5xx responses: ${badResponses}`);
    assert.deepEqual(pageErrors, [], 'unexpected page errors');
    assert.deepEqual(consoleErrors, [], 'unexpected console errors');
    console.log('companion vrm browser: ok', JSON.stringify({
      phase,
      packsTested: vrmPacks.map(p => p.label),
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
