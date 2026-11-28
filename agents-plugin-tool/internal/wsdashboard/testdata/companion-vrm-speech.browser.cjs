// Run by TestCompanionVrmSpeechBrowser against the live Werkschaft dashboard shell.
// Verifies Korean & English stage quips animate non-zero mouth visemes on BOTH
// VRM 1.0 (aa/ih/ou/ee/oh) and VRM 0.x (a/i/u/e/o), return cleanly to 0 after
// speech ends, honor setSpeechMouthDriver override + null restore, keep 0
// console/page errors, and save close-up comparison screenshots when shotsDir is set.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [base, shotsDir, vrm0ID, vrm1ID, real0Flag, real1Flag] = process.argv.slice(2);
const isReal0 = real0Flag === '1';
const isReal1 = real1Flag === '1';

async function composeComparisonSheet(browser, outPath, title, panels) {
  if (!outPath || !panels.length) return;
  fs.mkdirSync(path.dirname(outPath), { recursive: true });
  const page = await browser.newPage({ viewport: { width: 1600, height: 900 }, deviceScaleFactor: 1 });
  const items = panels.map(p => ({ label: p.label, src: 'data:image/png;base64,' + p.buf.toString('base64') }));
  await page.setContent(`<!doctype html><html><head><style>
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body { background: #0f1116; color: #e8ecf4; font-family: -apple-system, BlinkMacSystemFont, "SF Pro Text", sans-serif; padding: 18px; display: inline-block; }
    h1 { font-size: 15px; font-weight: 700; margin-bottom: 12px; color: #c7d2fe; }
    .grid { display: grid; grid-template-columns: repeat(${panels.length}, auto); gap: 14px; align-items: start; }
    .card { background: #171a22; border: 1px solid #2b3140; border-radius: 10px; overflow: hidden; display: inline-flex; flex-direction: column; }
    .cap { padding: 8px 11px; font-size: 12px; font-weight: 600; color: #d8dee9; border-bottom: 1px solid #262c3a; background: #1b1f29; white-space: pre-line; }
    .card img { display: block; width: 320px; height: auto; }
  </style></head><body><h1>${title}</h1><div class="grid" id="g"></div></body></html>`);
  await page.evaluate(async list => {
    const g = document.getElementById('g');
    for (const it of list) {
      const card = document.createElement('div');
      card.className = 'card';
      const cap = document.createElement('div');
      cap.className = 'cap';
      cap.textContent = it.label;
      const img = document.createElement('img');
      await new Promise((res, rej) => { img.onload = res; img.onerror = rej; img.src = it.src; });
      card.append(cap, img);
      g.append(card);
    }
  }, items);
  const box = await page.locator('body').boundingBox();
  await page.setViewportSize({ width: Math.ceil(box.width), height: Math.ceil(box.height) });
  await page.screenshot({ path: outPath, fullPage: true });
  await page.close();
}

function maxViseme(v) {
  if (!v) return 0;
  return Math.max(v.aa || 0, v.ih || 0, v.ou || 0, v.ee || 0, v.oh || 0);
}

function fmtVisemes(v) {
  if (!v) return 'none';
  return ['aa', 'ih', 'ou', 'ee', 'oh'].map(k => `${k}:${(v[k] || 0).toFixed(2)}`).join(' ');
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
      deviceScaleFactor: shotsDir ? 2 : 1
    });
    await context.addInitScript(() => {
      if (self !== top) return;
      const workers = navigator.serviceWorker;
      if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
    });

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
      if (res.status() >= 400 && p !== '/favicon.ico' && p !== '/tracker/stores') {
        badResponses.push(`${res.status()} ${p}`);
      }
    });

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
      await page.waitForTimeout(250);
    };

    const sendCommand = cmd => page.evaluate(detail => {
      document.dispatchEvent(new CustomEvent('ws-companion-command', { detail }));
    }, cmd);

    const getStage = () => page.evaluate(() => window.WsCompanion?.debugStage?.() || null);

    const getHeadCloseUpClip = async () => page.evaluate(() => {
      const m = window.WsCompanion.getVrmRenderMetrics();
      const a = m.anchorRect;
      // Upper 62% of the rail bust slot frames face + mouth cleanly at close-up scale.
      return {
        x: Math.max(0, Math.round(a.left)),
        y: Math.max(0, Math.round(a.top)),
        width: Math.max(64, Math.round(a.width)),
        height: Math.max(64, Math.round(a.height * 0.62))
      };
    });

    let seq = 100;
    const savedShots = [];

    for (const entry of [
      { id: vrm1ID, tag: 'vrm1', label: 'VRM 1.0', isReal: isReal1, wantIsVrm0: false },
      { id: vrm0ID, tag: 'vrm0', label: 'VRM 0.x', isReal: isReal0, wantIsVrm0: true }
    ]) {
      await applyPack(entry.id);
      await page.evaluate(() => {
        window.WsCompanion.setCoLeadBeat('glance-up');
        window.WsCompanion.stopSpeech();
      });
      await page.waitForTimeout(350);

      const idleSt = await getStage();
      const idleSpeech = idleSt.vrm.speech;
      assert.ok(idleSpeech, `${entry.tag}: missing debugState().vrm.speech`);
      assert.equal(idleSpeech.speaking, false, `${entry.tag}: should start idle`);
      assert.equal(idleSpeech.driverName, 'procedural');
      assert.ok(maxViseme(idleSpeech.visemes) <= 0.005, `${entry.tag}: idle visemes must be 0, got ${JSON.stringify(idleSpeech.visemes)}`);

      const hasMorphs = Object.values(idleSpeech.resolvedVisemes || {}).some(Boolean);
      if (entry.isReal) {
        assert.equal(idleSt.vrm.isVrm0, entry.wantIsVrm0, `${entry.tag}: expected isVrm0=${entry.wantIsVrm0}, got vrmVersion=${idleSt.vrm.vrmVersion}`);
        assert.equal(hasMorphs, true, `${entry.tag}: real VRM must expose viseme morphs: ${JSON.stringify(idleSpeech.resolvedVisemes)}`);
        for (const [canon, v0] of [['aa', 'a'], ['ih', 'i'], ['ou', 'u'], ['ee', 'e'], ['oh', 'o']]) {
          const got = idleSpeech.resolvedVisemes[canon];
          assert.ok(got === canon || got === v0, `${entry.tag}: expected ${canon} to resolve to ${canon} or ${v0}, got ${got}`);
        }
      }

      const clip = await getHeadCloseUpClip();
      const panels = [];
      if (shotsDir) {
        fs.mkdirSync(shotsDir, { recursive: true });
        const idlePath = path.join(shotsDir, `${entry.tag}-idle.png`);
        const idleBuf = await page.screenshot({ path: idlePath, clip });
        savedShots.push(idlePath);
        panels.push({ label: `Idle (mouth closed)\n${fmtVisemes(idleSpeech.visemes)}`, buf: idleBuf });
      }

      // Helper to trigger a stage quip, sample peaks, and capture a mid-speech close-up.
      const runQuipCheck = async (kind, quipText) => {
        await sendCommand({
          sessionId: '*',
          seq: ++seq,
          priority: 'high',
          quip: quipText,
          ttl_ms: 4200
        });
        await page.waitForFunction(() => {
          const el = document.querySelector('.ws-companion-quip');
          const st = window.WsCompanion?.debugStage?.();
          return el && !el.hidden && st?.vrm?.speech?.speaking === true;
        }, null, { timeout: 6000 });

        let bestPeak = 0;
        let bestVisemes = null;
        let sawActive30 = false;
        let capturedBuf = null;
        let capturedVisemes = null;
        for (let i = 0; i < 24; i++) {
          const st = await getStage();
          if (st.vrmTargetFps >= 30) sawActive30 = true;
          const v = st.vrm.speech.visemes;
          const m = maxViseme(v);
          if (m > bestPeak) {
            bestPeak = m;
            bestVisemes = { ...v };
          }
          if (shotsDir && !capturedBuf && (m >= 0.30 || i === 10)) {
            const p = path.join(shotsDir, `${entry.tag}-${kind}-speaking.png`);
            capturedBuf = await page.screenshot({ path: p, clip });
            capturedVisemes = { ...v };
            savedShots.push(p);
          }
          await page.waitForTimeout(55);
        }
        assert.equal(sawActive30, true, `${entry.tag}/${kind}: speaking must keep render loop >= 30fps`);
        if (entry.isReal || hasMorphs) {
          assert.ok(bestPeak > 0.15, `${entry.tag}/${kind}: expected non-zero mouth viseme > 0.15, got peak=${bestPeak} (${JSON.stringify(bestVisemes)})`);
        }
        if (capturedBuf) {
          panels.push({
            label: `${kind.toUpperCase()} quip: "${quipText.slice(0, 22)}"\n${fmtVisemes(capturedVisemes)}`,
            buf: capturedBuf
          });
        }
        console.log(`${entry.tag}/${kind}: peak=${bestPeak.toFixed(3)} visemes=${fmtVisemes(bestVisemes)}`);
      };

      // 1. Korean stage quip.
      await runQuipCheck('ko', '안녕하세요! 오늘 작업 완료했습니다.');
      // 2. English stage quip.
      await runQuipCheck('en', 'Hello world! Everything looks awesome today.');

      // 3. Dismiss the active stage quip (double-click character -> returnHome/runner.clear)
      //    and verify quip dismissal automatically stops speech and decays visemes cleanly to 0.
      await page.evaluate(() => {
        const ch = document.querySelector('.ws-companion-character');
        ch?.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
      });
      await page.waitForFunction(() => {
        const el = document.querySelector('.ws-companion-quip');
        const sp = window.WsCompanion?.debugStage?.()?.vrm?.speech;
        if ((el && !el.hidden) || !sp || sp.speaking) return false;
        const v = sp.visemes || {};
        return Math.max(v.aa || 0, v.ih || 0, v.ou || 0, v.ee || 0, v.oh || 0) <= 0.005;
      }, null, { timeout: 5000 });

      // 4. Pluggable speechMouthDriver override + null restoration.
      const customRes = await page.evaluate(() => {
        let active = false;
        const drv = {
          name: 'qa-custom',
          start() { active = true; },
          stop() { active = false; },
          isActive() { return active; },
          sample() { return active ? { aa: 0.78, ih: 0, ou: 0, ee: 0, oh: 0.18, speaking: true } : { aa: 0, ih: 0, ou: 0, ee: 0, oh: 0, speaking: false }; }
        };
        window.WsCompanion.setSpeechMouthDriver(drv);
        window.WsCompanion.startSpeech('custom tts');
        return window.WsCompanion.debugStage()?.vrm?.speech?.driverName;
      });
      assert.equal(customRes, 'qa-custom');
      await page.waitForTimeout(260);
      const customSt = await getStage();
      if (entry.isReal || idleSpeech.resolvedVisemes?.aa) {
        assert.ok(customSt.vrm.speech.visemes.aa > 0.40,
          `${entry.tag}: custom driver aa should exceed 0.40, got ${JSON.stringify(customSt.vrm.speech.visemes)}`);
      }
      if (entry.isReal || idleSpeech.resolvedVisemes?.oh) {
        assert.ok(customSt.vrm.speech.visemes.oh > 0.10,
          `${entry.tag}: custom driver oh should exceed 0.10, got ${JSON.stringify(customSt.vrm.speech.visemes)}`);
      }
      if (shotsDir) {
        const customPath = path.join(shotsDir, `${entry.tag}-custom-driver.png`);
        const customBuf = await page.screenshot({ path: customPath, clip });
        savedShots.push(customPath);
        panels.push({ label: `Custom speechMouthDriver\n${fmtVisemes(customSt.vrm.speech.visemes)}`, buf: customBuf });
      }

      // Restore procedural default with null and verify visemes return to 0.
      await page.evaluate(() => window.WsCompanion.setSpeechMouthDriver(null));
      await page.waitForFunction(() => {
        const sp = window.WsCompanion?.debugStage?.()?.vrm?.speech;
        if (!sp || sp.speaking || sp.driverName !== 'procedural') return false;
        const v = sp.visemes || {};
        return Math.max(v.aa || 0, v.ih || 0, v.ou || 0, v.ee || 0, v.oh || 0) <= 0.005;
      }, null, { timeout: 5000 });

      if (shotsDir && panels.length) {
        const sheetPath = path.join(shotsDir, `lipsync-comparison-${entry.tag}.png`);
        await composeComparisonSheet(browser, sheetPath, `${entry.label} (${entry.id}) — Tier-0 Lip-Sync QA`, panels);
        savedShots.push(sheetPath);
      }
    }

    assert.deepEqual(badResponses, [], `unexpected 4xx/5xx responses: ${badResponses}`);
    assert.deepEqual(pageErrors, [], `unexpected page errors: ${pageErrors}`);
    assert.deepEqual(consoleErrors, [], `unexpected console errors: ${consoleErrors}`);
    console.log('companion vrm speech browser: ok', savedShots.length ? `shots=${JSON.stringify(savedShots)}` : '');
  } finally {
    await browser.close();
  }
})().catch(err => {
  console.error(err);
  process.exit(1);
});
