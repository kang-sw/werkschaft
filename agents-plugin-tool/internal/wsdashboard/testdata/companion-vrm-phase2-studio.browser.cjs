// Run by TestCompanionVrmPhase2StudioBrowser against the live Werkschaft dashboard shell.
// Phase 2 visual polish acceptance on real VRM 1.0 and VRM 0.x avatars:
//   - bust framing sweep at 220 / 280 / 360px rail widths in Dark and Light themes
//     (10-14% headroom from the pixel mask, no head/hair/shoulder edge contact,
//     laptop lid 14-19% from the slot bottom and >= 20% below the chin, hands in frame);
//   - theme-adaptive studio lighting follows data-theme / prefers-color-scheme live,
//     with no blown-out or crushed avatar exposure in either theme;
//   - held phone sits flush in the palm (palm-frame grip, screen toward the face).
// When a shots dir is passed, writes the Phase 2 visual QA contact sheets.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [base, shots, vrm0ID, vrm1ID] = process.argv.slice(2);
const WIDTHS = [220, 280, 360];
const THEMES = ['dark', 'light'];
// Optional crisp per-pose PNGs (absolute dir, 2x device scale) for visual tuning;
// also extends the held-phone checks to every rail width.
const pngDir = process.env.WS_COMPANION_PHASE2_QA_PNGS || '';
const pngPath = (tag, theme, w, pose) =>
  path.join(pngDir, `${tag === 'VRM 1.0' ? 'vrm1' : 'vrm0x'}-${theme}-${w}px-${pose}.png`);

async function composeGrid(browser, outPath, title, panels, opts = {}) {
  if (!outPath || !panels.length) return;
  fs.mkdirSync(path.dirname(outPath), { recursive: true });
  const cols = opts.cols || panels.length;
  const page = await browser.newPage({ viewport: { width: 2400, height: 1400 }, deviceScaleFactor: 1 });
  const imgData = panels.map(p => ({ label: p.label, src: 'data:image/png;base64,' + p.buf.toString('base64'), maxW: p.maxW || 0 }));
  await page.setContent(`<!doctype html><html><head><style>
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body { background: #0f1116; color: #e8ecf4; font-family: -apple-system, BlinkMacSystemFont, "SF Pro Text", sans-serif; padding: 18px; display: inline-block; }
    h1 { font-size: 15px; font-weight: 700; margin-bottom: 12px; color: #c7d2fe; }
    .grid { display: grid; grid-template-columns: repeat(${cols}, auto); gap: 14px; align-items: start; }
    .card { background: #171a22; border: 1px solid #2b3140; border-radius: 10px; overflow: hidden; display: inline-flex; flex-direction: column; }
    .cap { padding: 7px 11px; font-size: 12px; font-weight: 600; color: #d8dee9; border-bottom: 1px solid #262c3a; background: #1b1f29; max-width: 560px; }
    .card img { display: block; height: auto; image-rendering: auto; }
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

// Pixel ground truth: diff of the slot with and without the WebGL canvas gives the
// rendered avatar+prop mask. Measures headroom, upper-band edge contact, and exposure.
async function analyzeMask(analysisPage, withBuf, withoutBuf) {
  return analysisPage.evaluate(async ([a, b]) => {
    const load = async b64 => {
      const blob = await (await fetch('data:image/png;base64,' + b64)).blob();
      const bmp = await createImageBitmap(blob);
      const cv = new OffscreenCanvas(bmp.width, bmp.height);
      const ctx = cv.getContext('2d');
      ctx.drawImage(bmp, 0, 0);
      return ctx.getImageData(0, 0, bmp.width, bmp.height);
    };
    const A = await load(a);
    const B = await load(b);
    const w = A.width, h = A.height;
    const mask = new Uint8Array(w * h);
    let topRow = -1;
    for (let y = 0; y < h; y++) {
      let rowHits = 0;
      for (let x = 0; x < w; x++) {
        const i = (y * w + x) * 4;
        const d = Math.abs(A.data[i] - B.data[i]) + Math.abs(A.data[i + 1] - B.data[i + 1]) + Math.abs(A.data[i + 2] - B.data[i + 2]);
        if (d > 36) { mask[y * w + x] = 1; rowHits++; }
      }
      // Require a few pixels so single antialias specks never count as the hair top.
      if (topRow < 0 && rowHits >= 2) topRow = y;
    }
    const upperBand = Math.round(h * 0.45);
    let leftEdge = 0, rightEdge = 0, minX = w, maxX = -1;
    for (let y = 0; y < upperBand; y++) {
      for (let x = 0; x < w; x++) {
        if (!mask[y * w + x]) continue;
        if (x < 2) leftEdge++;
        if (x >= w - 2) rightEdge++;
        if (x < minX) minX = x;
        if (x > maxX) maxX = x;
      }
    }
    let n = 0, sum = 0, blown = 0, crushed = 0;
    for (let i = 0; i < w * h; i++) {
      if (!mask[i]) continue;
      const r = A.data[i * 4], g = A.data[i * 4 + 1], bl = A.data[i * 4 + 2];
      const lum = 0.2126 * r + 0.7152 * g + 0.0722 * bl;
      n++; sum += lum;
      if (r >= 252 && g >= 252 && bl >= 252) blown++;
      if (lum < 6) crushed++;
    }
    return {
      w, h,
      headroomPct: topRow < 0 ? -1 : Number((topRow / h * 100).toFixed(2)),
      leftEdgeUpper: leftEdge,
      rightEdgeUpper: rightEdge,
      upperMinXPct: Number((minX / w * 100).toFixed(2)),
      upperMaxXPct: Number((maxX / w * 100).toFixed(2)),
      maskPct: Number((n / (w * h) * 100).toFixed(2)),
      meanLum: n ? Number((sum / n).toFixed(1)) : 0,
      blownPct: n ? Number((blown / n * 100).toFixed(2)) : 0,
      crushedPct: n ? Number((crushed / n * 100).toFixed(2)) : 0
    };
  }, [withBuf.toString('base64'), withoutBuf.toString('base64')]);
}

(async () => {
  const browser = await chromium.launch({
    headless: true,
    args: ['--use-gl=angle', '--use-angle=swiftshader', '--enable-unsafe-swiftshader', '--ignore-gpu-blocklist']
  });
  try {
    if (pngDir) fs.mkdirSync(pngDir, { recursive: true });
    const context = await browser.newContext({ viewport: { width: 1680, height: 960 }, colorScheme: 'dark', deviceScaleFactor: pngDir ? 2 : 1 });
    await context.addInitScript(() => {
      if (self !== top) return;
      const workers = navigator.serviceWorker;
      if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
    });
    const pageErrors = [];
    const consoleErrors = [];
    const page = await context.newPage();
    page.on('pageerror', e => pageErrors.push(e.message));
    page.on('console', msg => {
      if (msg.type() === 'error') {
        const txt = msg.text();
        if (!txt.includes('404 (Not Found)')) consoleErrors.push(txt);
      }
    });
    const analysisPage = await browser.newPage();
    await analysisPage.setContent('<!doctype html><html><body></body></html>');

    await page.goto(base + '/s/alpha01');
    await page.frameLocator('.sframe.on').locator('#grid').waitFor();

    const applyPack = async packId => {
      await page.evaluate(id => {
        document.dispatchEvent(new CustomEvent('ws-companion-config', { detail: { enabled: true, director: 'rules', pack: id, preset: '' } }));
      }, packId);
      await page.waitForFunction(id => {
        const st = window.WsCompanion?.debugStage?.();
        return st && st.renderer === 'vrm' && st.pack === id && st.vrm?.loaded && st.vrm.framesRendered >= 6;
      }, packId, { timeout: 30000 });
      await page.waitForTimeout(300);
    };

    const settleBeat = async (beat, settleMs = 700) => {
      await page.evaluate(b => {
        window.WsCompanion.setVrmHover(null);
        window.WsCompanion.setCoLeadBeat(b);
      }, beat);
      const steps = Math.max(3, Math.ceil(settleMs / 120));
      for (let i = 0; i < steps; i++) {
        await page.mouse.move(900 + (i % 3) * 14, 180 + (i % 2) * 10);
        await page.waitForTimeout(Math.round(settleMs / steps));
      }
      // Per-frame followers converge in frames, not wall time: under heavy host
      // load (~15fps) a fixed delay can sample mid-transition. Wait (bounded)
      // until the lid/chin layout stops moving across rendered frames.
      const layoutSample = () => page.evaluate(() => {
        const v = window.WsCompanion.debugStage()?.vrm;
        const co = v?.coLead || {};
        return { f: v?.framesRendered || 0, lid: co.laptopTopViewportPct, chin: co.chinViewportPct };
      });
      let prev = await layoutSample();
      for (let i = 0, stable = 0; i < 40 && stable < 2; i++) {
        await page.waitForTimeout(120);
        const cur = await layoutSample();
        if (cur.f <= prev.f) continue;
        const still = Math.abs((cur.lid ?? 0) - (prev.lid ?? 0)) < 0.1 && Math.abs((cur.chin ?? 0) - (prev.chin ?? 0)) < 0.1;
        stable = still ? stable + 1 : 0;
        prev = cur;
      }
      return page.evaluate(() => window.WsCompanion.debugStage()?.vrm || null);
    };

    const setTheme = async theme => {
      await page.emulateMedia({ colorScheme: theme });
      await page.evaluate(t => { document.documentElement.dataset.theme = t; }, theme);
      await page.waitForFunction(t => {
        const studio = window.WsCompanion?.debugStage?.()?.vrm?.studio;
        return !!studio && studio.mode === t;
      }, theme, { timeout: 3000 });
    };

    const setWidth = async w => {
      await page.evaluate(v => {
        if (window.WsIdeShell) window.WsIdeShell.setWidth(v);
        else document.documentElement.style.setProperty('--rail-w', `${v}px`);
      }, w);
      await page.waitForFunction(v => {
        const slot = document.getElementById('rail-companion-slot')?.getBoundingClientRect();
        const hit = document.querySelector('.ws-companion-hit')?.getBoundingClientRect();
        return !!(slot && hit && Math.abs(slot.width - v) <= 2 && Math.abs(hit.width - (slot.width - 4)) <= 2);
      }, w, { timeout: 4000 });
      await page.waitForTimeout(150);
      // Re-framing re-arms the pixel-true headroom pass; measure only after it ran.
      await page.waitForFunction(() => {
        const v = window.WsCompanion?.debugStage?.()?.vrm;
        return !v || !v.loaded || v.headroomCalibPending === false;
      }, null, { timeout: 8000 });
    };

    // Exact WebGL anchor rect (the docked scissor) in CSS px.
    const anchorClip = async () => page.evaluate(() => {
      const a = window.WsCompanion.getVrmRenderMetrics().anchorRect;
      return { x: Math.round(a.left), y: Math.round(a.top), width: Math.round(a.width), height: Math.round(a.height) };
    });

    const captureMask = async () => {
      const clip = await anchorClip();
      const withBuf = await page.screenshot({ clip });
      await page.evaluate(() => { for (const c of document.querySelectorAll('.ws-companion-vrm-canvas')) c.style.visibility = 'hidden'; });
      const withoutBuf = await page.screenshot({ clip });
      await page.evaluate(() => { for (const c of document.querySelectorAll('.ws-companion-vrm-canvas')) c.style.visibility = ''; });
      return { clip, withBuf, stats: await analyzeMask(analysisPage, withBuf, withoutBuf) };
    };

    const inFrame = (p, label) => {
      if (!p) return;
      check(p.x >= 1 && p.x <= 99 && p.y >= 0.5 && p.y <= 99, `${label} outside the slot (${JSON.stringify(p)})`);
    };

    const failures = [];
    const check = (cond, msg) => { if (!cond) failures.push(msg); };
    const checkEq = (a, b, msg) => { if (a !== b) failures.push(`${msg} (got ${JSON.stringify(a)}, want ${JSON.stringify(b)})`); };
    const sweepShots = { dark: [], light: [] };
    const phoneShots = [];
    const summary = {};

    for (const entry of [{ id: vrm1ID, tag: 'VRM 1.0' }, { id: vrm0ID, tag: 'VRM 0.x' }]) {
      await applyPack(entry.id);
      await setTheme('dark');
      await setWidth(280);
      const base0 = await page.evaluate(() => window.WsCompanion.debugStage().vrm);
      const fingers = base0.fingerBoneCount || 0;
      summary[entry.tag] = { fingerBoneCount: fingers, sweep: [] };

      for (const theme of THEMES) {
        const changesBefore = (await page.evaluate(() => window.WsCompanion.debugStage().vrm.studio?.themeChanges)) || 0;
        await setTheme(theme);
        const studio = await page.evaluate(() => window.WsCompanion.debugStage().vrm.studio);
        checkEq(studio.mode, theme, `${entry.tag}: studio lighting must follow the ${theme} theme`);
        checkEq(studio.lightCount, 4, `${entry.tag}: key/fill/rim/hemisphere lights`);
        if (theme === 'light') check(studio.themeChanges > changesBefore, `${entry.tag}: theme switch observed live`);
        check(studio.envReady, `${entry.tag}: prop-only PMREM environment must be built`);
        check(studio.envMaterialCount >= 2, `${entry.tag}: metallic prop materials receive the env (${studio.envMaterialCount})`);

        for (const w of WIDTHS) {
          await setWidth(w);
          const st = await settleBeat('typing', 750);
          const label = `${entry.tag} ${theme} ${w}px`;
          const co = st.coLead;
          const qa = st.framing.qa || {};
          const { withBuf, stats } = await captureMask();
          const row = {
            theme, w,
            maskHeadroom: stats.headroomPct,
            solverHeadroom: qa.headroomPct,
            probeHeadroom: qa.pixelHeadroomPct,
            calibPasses: qa.calibPasses,
            sideMargin: qa.sideMarginPct,
            limitedBy: qa.limitedBy,
            lid: co.laptopTopViewportPct,
            chin: co.chinViewportPct,
            edges: [stats.leftEdgeUpper, stats.rightEdgeUpper],
            upperX: [stats.upperMinXPct, stats.upperMaxXPct],
            meanLum: stats.meanLum,
            blown: stats.blownPct,
            crushed: stats.crushedPct,
            fr: Object.fromEntries(['topY', 'headY', 'chestY', 'waistY', 'frameHeight', 'targetY', 'upperWidth', 'bustSpan']
              .map(k => [k, Number((st.framing[k] || 0).toFixed(3))])),
            hands: st.slotPoints ? [st.slotPoints.leftHand, st.slotPoints.rightHand] : null
          };
          summary[entry.tag].sweep.push(row);

          check(stats.headroomPct >= 10 && stats.headroomPct <= 14, `${label}: pixel headroom ${stats.headroomPct}% outside 10-14%`);
          checkEq(stats.leftEdgeUpper + stats.rightEdgeUpper, 0, `${label}: head/hair/shoulders clip the slot edge (${JSON.stringify(row)})`);
          check(co.laptopTopViewportPct >= 14 && co.laptopTopViewportPct <= 19, `${label}: laptop top ${co.laptopTopViewportPct}% outside 14-19%`);
          check(co.laptopTopViewportPct <= co.chinViewportPct - 20, `${label}: laptop top ${co.laptopTopViewportPct}% must sit >= 20% below chin ${co.chinViewportPct}%`);
          const sp = st.slotPoints || {};
          inFrame(sp.head, `${label} head`);
          // Typing hands are either fully in frame or tucked behind the laptop lid
          // (never cut by a visible slot edge above the lid).
          for (const k of ['leftHand', 'rightHand', 'leftMiddleDistal', 'rightMiddleDistal']) {
            const p = sp[k];
            if (p && p.y > co.laptopTopViewportPct) inFrame(p, `${label} ${k}`);
            if (p) check(p.x >= 1 && p.x <= 99, `${label} ${k} clipped horizontally (${JSON.stringify(p)})`);
          }
          check(stats.blownPct < 4, `${label}: blown highlights ${stats.blownPct}%`);
          check(stats.crushedPct < 6, `${label}: crushed shadows ${stats.crushedPct}%`);
          check(stats.meanLum > 45 && stats.meanLum < 225, `${label}: avatar mean luminance ${stats.meanLum}`);
          if (pngDir) fs.writeFileSync(pngPath(entry.tag, theme, w, 'laptop_work'), withBuf);
          if (shots) {
            sweepShots[theme].push({
              label: `${label} · head ${stats.headroomPct}% · lid ${co.laptopTopViewportPct}% · chin ${co.chinViewportPct}% · ${qa.limitedBy}`,
              buf: withBuf,
              maxW: Math.round(w * 1.25)
            });
          }
        }

        // Held phone: palm grip at the middle rail width (every width with pngDir).
        for (const pw of pngDir ? WIDTHS : [280]) {
          await setWidth(pw);
          const ph = await settleBeat('phone-typing', 1100);
          const grip = ph.coLead.palmGrip;
          const pl = `${entry.tag} ${theme} ${pw}px`;
          check(grip && grip.weight > 0.95, `${pl}: phone must be held (${JSON.stringify(grip)})`);
          if (fingers >= 9) {
            checkEq(grip.mode, 'palm', `${pl}: finger rigs grip the phone with the palm frame`);
            check(grip.screenFacing > 0.2, `${pl}: phone screen must face the avatar (${grip.screenFacing})`);
          } else {
            checkEq(grip.mode, 'anchor', `${pl}: finger-less rigs fall back to the knuckle anchor`);
          }
          const sp = ph.slotPoints || {};
          inFrame(sp.phone, `${pl} phone`);
          inFrame(sp.leftHand, `${pl} held hand`);
          const clip = (shots || pngDir) ? await anchorClip() : null;
          const buf = clip ? await page.screenshot({ clip }) : null;
          if (pngDir) fs.writeFileSync(pngPath(entry.tag, theme, pw, 'phone_hold'), buf);
          if (pw !== 280) continue;
          summary[entry.tag][`phone-${theme}`] = { mode: grip.mode, screenFacing: Number((grip.screenFacing || 0).toFixed(3)), phoneUpDot: Number((grip.phoneUpDot || 0).toFixed(3)), phone: sp.phone };
          if (shots) {
            phoneShots.push({
              label: `${entry.tag} ${theme} · held phone (${grip.mode}, facing ${(grip.screenFacing || 0).toFixed(2)})`,
              buf,
              maxW: 420
            });
          }
        }
        await page.evaluate(() => window.WsCompanion.setCoLeadBeat('typing'));
      }
      await page.evaluate(() => window.WsCompanion.setCoLeadBeat('auto'));
    }

    if (shots) {
      await composeGrid(browser, path.join(shots, 'p2-01-framing-dark.png'), 'Phase 2 · Bust Framing Sweep 220 / 280 / 360px — Dark', sweepShots.dark, { cols: 3 });
      await composeGrid(browser, path.join(shots, 'p2-02-framing-light.png'), 'Phase 2 · Bust Framing Sweep 220 / 280 / 360px — Light', sweepShots.light, { cols: 3 });
      await composeGrid(browser, path.join(shots, 'p2-03-held-phone.png'), 'Phase 2 · Held Phone Palm Grip — VRM 1.0 / VRM 0.x × Dark / Light', phoneShots, { cols: 2 });
    }

    if (shots) fs.writeFileSync(path.join(shots, 'p2-summary.json'), JSON.stringify({ summary, failures }, null, 1));
    console.log('phase2 summary', JSON.stringify(summary));
    assert.deepEqual(failures, [], `Phase 2 studio invariants failed:\n  ${failures.join('\n  ')}`);
    assert.deepEqual(pageErrors, [], `unexpected page errors: ${JSON.stringify(pageErrors)}`);
    assert.deepEqual(consoleErrors, [], `unexpected console errors: ${JSON.stringify(consoleErrors)}`);
    console.log('companion vrm phase2 studio browser: ok', JSON.stringify(summary));
  } finally {
    await browser.close();
  }
})().catch(err => {
  console.error(err);
  process.exit(1);
});
