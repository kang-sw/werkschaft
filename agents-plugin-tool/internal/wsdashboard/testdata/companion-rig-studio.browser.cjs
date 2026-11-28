// Run by TestCompanionRigStudioBrowser against the live Werkschaft dashboard + Studio.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const zlib = require('node:zlib');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [base, shots, vrm0ID, vrm1ID, vrmaPath] = process.argv.slice(2);

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
    } else if (type === 'IDAT') idat.push(data);
    else if (type === 'IEND') break;
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
      if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No SW in fixture'); };
    });

    const dashErrors = [];
    const studioErrors = [];
    const studioRuntimeReqs = [];
    const studioMainVrmReqs = [];

    // =========================================================================
    // 1. Open Dashboard Page, enable VRM 1.0 pack, verify Settings Open Studio
    // =========================================================================
    const dashPage = await context.newPage();
    dashPage.on('pageerror', e => dashErrors.push('pageerror: ' + e.message));
    dashPage.on('console', msg => {
      if (msg.type() === 'error' && !msg.text().includes('404 (Not Found)')) dashErrors.push('console: ' + msg.text());
    });

    await dashPage.goto(base + '/s/livesession');
    await dashPage.frameLocator('.sframe.on').locator('#grid').waitFor();

    // Persist VRM 1.0 pack as active companion pack, then verify Settings Open Studio button.
    await dashPage.evaluate(async packId => {
      for (const [key, value] of [['dashboard.companion_enabled', true], ['dashboard.companion_pack', packId]]) {
        const res = await fetch('/api/config/set', {
          method: 'POST', credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ scope: 'global', key, value })
        });
        if (!res.ok) throw new Error('set ' + key + ' failed: ' + res.status);
      }
      document.dispatchEvent(new CustomEvent('ws-companion-config', {
        detail: { enabled: true, director: 'rules', pack: packId, preset: '' }
      }));
    }, vrm1ID);

    await dashPage.waitForFunction(id => {
      const st = window.WsCompanion?.debugStage?.();
      return st && st.renderer === 'vrm' && st.pack === id && st.vrm?.loaded && st.vrm.framesRendered >= 3;
    }, vrm1ID, { timeout: 25000 });

    // Check Settings modal has enabled Open Studio button pointing at /companion/studio?pack=<vrm1ID>
    await dashPage.locator('.ws-companion-gear').click({ force: true });
    await dashPage.locator('#workflow-settings[open]').waitFor();
    const settingsFrame = dashPage.frameLocator('#workflow-settings[open] > iframe');
    const openStudioBtn = settingsFrame.locator('[data-companion-open-studio]');
    await openStudioBtn.waitFor();
    assert.equal(await openStudioBtn.isDisabled(), false, 'Open Studio button must be enabled for local VRM pack');
    await settingsFrame.locator('[data-settings-close]').click();
    await dashPage.locator('#workflow-settings').waitFor({ state: 'hidden' });

    // =========================================================================
    // 2. Verify non-authorable pack (?pack=default) shows in-page error state
    // =========================================================================
    const errPage = await context.newPage();
    const errResp = await errPage.goto(base + '/companion/studio?pack=default');
    assert.equal(errResp.status(), 200, 'non-authorable pack must render HTTP 200 in-page error state');
    await errPage.waitForFunction(() => window.WsRigStudio?.getState()?.errorVisible === true, null, { timeout: 10000 });
    await errPage.close();

    // =========================================================================
    // 3. Open Control-Rig Studio for VRM 1.0 pack, verify isolated runtime
    // =========================================================================
    const studioPage = await context.newPage();
    await studioPage.setViewportSize({ width: 1440, height: 900 });
    studioPage.on('pageerror', e => studioErrors.push('pageerror: ' + e.message));
    studioPage.on('console', msg => {
      if (msg.type() === 'error') studioErrors.push('console: ' + msg.text());
    });
    studioPage.on('request', req => {
      const p = new URL(req.url()).pathname;
      if (/^\/s\/companion-rig-studio-[0-9a-f]+\.mjs$/.test(p)) studioRuntimeReqs.push(p);
      if (/^\/s\/companion-vrm-[0-9a-f]+\.mjs$/.test(p)) studioMainVrmReqs.push(p);
    });

    await studioPage.goto(base + '/companion/studio?pack=' + encodeURIComponent(vrm1ID));
    try {
      await studioPage.waitForFunction(() => {
        const s = window.WsRigStudio?.getState();
        return s && s.stage && s.stage.ready && s.stage.renderCount >= 3;
      }, null, { timeout: 30000 });
    } catch (e) {
      const diag = await studioPage.evaluate(() => ({
        state: window.WsRigStudio?.getState?.() || null,
        banner: document.getElementById('studioBannerMsg')?.textContent || '',
        loading: document.getElementById('vpLoading')?.textContent || '',
        errTitle: document.getElementById('studioErrorTitle')?.textContent || '',
        errBody: document.getElementById('studioErrorBody')?.textContent || ''
      })).catch(err => ({ evalErr: err.message }));
      console.error('STUDIO_DIAG:', JSON.stringify({ diag, studioErrors, dashErrors, studioRuntimeReqs, studioMainVrmReqs }, null, 2));
      throw e;
    }

    assert.equal(studioRuntimeReqs.length, 1, 'studio must fetch companion-rig-studio runtime once');
    assert.equal(studioMainVrmReqs.length, 0, 'studio must NOT load main dashboard companion-vrm runtime');
    const stVrm1 = await studioPage.evaluate(() => window.WsRigStudio.getState());
    assert.equal(stVrm1.stage.isVrm0, false, 'VRM 1.0 fixture must report isVrm0=false');
    assert.equal(stVrm1.errorVisible, false);

    // Check rendered WebGL pixels on Studio canvas (must not be blank).
    const studioCanvasPng = await studioPage.locator('#studioCanvas').screenshot({ omitBackground: true });
    const studioPx = countOpaquePngPixels(studioCanvasPng);
    assert.ok(studioPx.lit > 2500, `Studio VRM 1.0 canvas appears blank: ${JSON.stringify(studioPx)}`);

    // =========================================================================
    // 4. Author a custom clip with IK + Target Attach + prop, Save -> live dashboard playback
    // =========================================================================
    await studioPage.locator('#btnNewClip').click();
    await studioPage.waitForFunction(() => window.WsRigStudio?.getState()?.selectedKey === 'custom_action_1');

    // Rename draft clip to wave_qa and expose it on Agent tab, verify live <<<ACTIONS>>> preview.
    await studioPage.locator('.rs-tab[data-tab="agent"]').click();
    await studioPage.locator('#agentKeyInp').fill('wave_qa');
    await studioPage.locator('#agentKeyInp').press('Enter');
    await studioPage.locator('#agentKeyInp').dispatchEvent('change');
    await studioPage.waitForFunction(() => window.WsRigStudio?.getState()?.selectedKey === 'wave_qa');
    await studioPage.locator('#agentExposeChk').click({ force: true });
    await studioPage.locator('#agentDescTa').fill('Cheerful wave to the user while holding phone');
    const actionsPreviewText = await studioPage.locator('#agentPreviewCode').textContent();
    assert.match(actionsPreviewText, /- wave_qa: Cheerful wave to the user while holding phone/, 'Agent preview must render wave_qa');

    // Capture Agent Expose tab QA shot before switching back to Rig tab.
    await studioPage.waitForTimeout(250);
    await saveShot(studioPage, 'vrm1-studio-agent-tab');

    // Switch back to Rig tab, enable smartphone prop, set Target Attach (Right Hand -> phone grip, Gaze -> camera).
    await studioPage.locator('.rs-tab[data-tab="rig"]').click();
    await studioPage.locator('#segProp button[data-prop="smartphone"]').click();
    await studioPage.locator('[data-attach-select="hand_r"]').selectOption('prop:smartphone#grip');
    await studioPage.locator('[data-attach-select="gaze"]').selectOption('camera');

    // Scrub playhead to 78% (~1.09s) and add a snapshot keyframe on the timeline (2 -> 3 keyframes).
    const trackBox = await studioPage.locator('#track').boundingBox();
    await studioPage.mouse.click(trackBox.x + trackBox.width * 0.78, trackBox.y + trackBox.height * 0.5);
    await studioPage.locator('#btnSnap').click();
    const kfCount = await studioPage.locator('#lane .rs-kfc').count();
    assert.equal(kfCount, 3, 'expected 3 keyframes after + Snapshot');

    // Select keyframe 0 (which carries hand_r -> prop:smartphone#grip and gaze -> camera)
    // so Target Attach tethers render visibly in the overlay SVG and QA screenshots.
    await studioPage.locator('#lane .rs-kfc').first().click();
    await studioPage.waitForFunction(() => (window.WsRigStudio?.getState()?.stage?.tetherCount || 0) >= 1);

    // Capture full 220/280/360px x Dark/Light matrix for VRM 1.0.
    for (const th of ['dark', 'light']) {
      await studioPage.locator(`#segTheme button[data-theme="${th}"]`).click();
      for (const sl of ['280', '220', '360']) {
        await studioPage.locator(`#segSlot button[data-slot="${sl}"]`).click();
        await studioPage.waitForTimeout(300);
        await saveShot(studioPage, `vrm1-studio-${th}-${sl}`);
      }
    }
    await studioPage.locator('#segSlot button[data-slot="280"]').click();

    // Restore Dark theme and Save to Pack -> verify dirtyCount drops to 0.
    await studioPage.locator('#segTheme button[data-theme="dark"]').click();
    const revBefore = await studioPage.evaluate(() => window.WsRigStudio.getState().rev);
    await studioPage.locator('#btnSave').click();
    await studioPage.waitForFunction(prev => {
      const s = window.WsRigStudio?.getState();
      return s && s.dirtyCount === 0 && s.rev !== prev;
    }, revBefore, { timeout: 15000 });

    // Verify open Dashboard page picked up the new pack revision via BroadcastChannel WITHOUT reload
    // and plays `wave_qa` when commanded!
    await dashPage.waitForTimeout(500);
    await dashPage.evaluate(() => {
      document.dispatchEvent(new CustomEvent('ws-companion-command', {
        detail: { sessionId: 'livesession', gesture: 'wave_qa', ttl_ms: 4000 }
      }));
    });
    await dashPage.waitForFunction(() => {
      const st = window.WsCompanion?.debugStage?.();
      return st && st.vrm && st.vrm.activeClip && st.vrm.activeClip.key === 'wave_qa';
    }, null, { timeout: 10000 });
    await dashPage.waitForTimeout(250);
    await saveShot(dashPage, 'vrm1-dashboard-live-clip');

    // =========================================================================
    // 5. Sentinel override (idle) + verify idle cadence stays calm
    // =========================================================================
    await studioPage.locator('.rs-clip-row[data-clip-key="idle"]').click();
    await studioPage.locator('#btnCreateOverride').click();
    const revBeforeSentinel = await studioPage.evaluate(() => window.WsRigStudio.getState().rev);
    await studioPage.locator('#btnSave').click();
    await studioPage.waitForFunction(prev => {
      const s = window.WsRigStudio?.getState();
      return s && s.dirtyCount === 0 && s.rev !== prev;
    }, revBeforeSentinel, { timeout: 15000 });

    // Wait for wave_qa TTL to clear on dashboard and confirm idle sentinel runs with loop=true, isSentinel=true.
    await dashPage.evaluate(() => {
      document.dispatchEvent(new CustomEvent('ws-companion-command', {
        detail: { sessionId: 'livesession', expression: 'idle', ttl_ms: 200 }
      }));
    });
    await dashPage.waitForFunction(() => {
      const st = window.WsCompanion?.debugStage?.();
      return st && st.vrm && st.vrm.activeClip && st.vrm.activeClip.key === 'idle' && st.vrm.activeClip.isSentinel === true && st.vrm.activeClip.loop === true;
    }, null, { timeout: 10000 });

    // =========================================================================
    // 6. Import .vrma file and verify playback in Studio
    // =========================================================================
    await studioPage.locator('#btnImport').click();
    await studioPage.locator('#importScrim:not([hidden])').waitFor();
    await studioPage.locator('#vrmaFileInput').setInputFiles(vrmaPath);
    await studioPage.locator('#impKeyInp').fill('dance_demo');
    await studioPage.locator('#impSubmit').click();
    await studioPage.waitForFunction(() => {
      const s = window.WsRigStudio?.getState();
      return s && s.selectedKey === 'dance_demo' && s.stage && s.stage.vrmaLoaded;
    }, null, { timeout: 15000 });

    // Toggle play/pause on imported VRMA clip and verify frames advance cleanly.
    const framesBeforePlay = await studioPage.evaluate(() => window.WsRigStudio.getState().stage.renderCount);
    await studioPage.locator('#btnPlay').click();
    await studioPage.waitForTimeout(350);
    await studioPage.locator('#btnPlay').click();
    const framesAfterPlay = await studioPage.evaluate(() => window.WsRigStudio.getState().stage.renderCount);
    assert.ok(framesAfterPlay > framesBeforePlay + 2, 'VRMA playback must render animation frames');

    // Verify idle rAF auto-pauses after >2.2s of inactivity when not playing.
    await studioPage.waitForTimeout(2800);
    const idleState = await studioPage.evaluate(() => window.WsRigStudio.getState().stage);
    assert.equal(idleState.rafActive, false, 'Studio rAF must pause after 2s of idle');

    // =========================================================================
    // 7. Switch to VRM 0.x pack via pack pill, verify VRM 0.x renders in Dark & Light
    // =========================================================================
    await studioPage.locator('#packPill').click();
    await studioPage.locator(`.rs-pack-menu button[data-pack-id="${vrm0ID}"]`).click();
    await studioPage.waitForFunction(id => {
      const s = window.WsRigStudio?.getState();
      return s && s.packId === id && s.stage && s.stage.ready && s.stage.isVrm0 === true && s.stage.renderCount >= 3;
    }, vrm0ID, { timeout: 30000 });

    const vrm0CanvasPng = await studioPage.locator('#studioCanvas').screenshot({ omitBackground: true });
    const vrm0Px = countOpaquePngPixels(vrm0CanvasPng);
    assert.ok(vrm0Px.lit > 2500, `Studio VRM 0.x canvas appears blank: ${JSON.stringify(vrm0Px)}`);

    // Capture full 220/280/360px x Dark/Light matrix for VRM 0.x.
    for (const th of ['dark', 'light']) {
      await studioPage.locator(`#segTheme button[data-theme="${th}"]`).click();
      for (const sl of ['280', '220', '360']) {
        await studioPage.locator(`#segSlot button[data-slot="${sl}"]`).click();
        await studioPage.waitForTimeout(300);
        await saveShot(studioPage, `vrm0-studio-${th}-${sl}`);
      }
    }

    assert.deepEqual(dashErrors, [], `Dashboard console/page errors: ${JSON.stringify(dashErrors)}`);
    assert.deepEqual(studioErrors, [], `Studio console/page errors: ${JSON.stringify(studioErrors)}`);
    console.log(JSON.stringify({
      ok: true,
      vrm1Pixels: studioPx.lit,
      vrm0Pixels: vrm0Px.lit,
      studioRuntime: studioRuntimeReqs[0],
      shotsDir: shots || null
    }));
  } finally {
    await browser.close();
  }
})();
