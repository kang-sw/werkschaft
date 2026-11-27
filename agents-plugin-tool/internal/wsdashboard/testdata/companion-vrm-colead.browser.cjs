// Run by TestCompanionVrmCoLeadBrowser against the live Werkschaft dashboard shell.
// Verifies Co-Lead typing posture, beveled laptop framing invariant, soft screen glow,
// hover/periodic glance-up choreography, and accent seam on both VRM 1.0 and VRM 0.x.
// When shots dir is passed, writes the 4 Co-Lead visual QA contact sheets.
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
    body { background: #0f1116; color: #e8ecf4; font-family: -apple-system, BlinkMacSystemFont, "SF Pro Text", sans-serif; padding: 18px; display: inline-block; }
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
      colorScheme: 'dark'
    });
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
      await page.waitForTimeout(300);
    };

    // Keep VRM at active 30fps while settling a Co-Lead beat for deterministic captures.
    const settleBeat = async (beat, settleMs = 650) => {
      await page.evaluate(b => {
        window.WsCompanion.setVrmHover(null);
        window.WsCompanion.setCoLeadBeat(b);
      }, beat);
      // Small mouse nudge in neutral area keeps active 30fps without hovering companion.
      const steps = Math.max(3, Math.ceil(settleMs / 120));
      for (let i = 0; i < steps; i++) {
        await page.mouse.move(760 + (i % 3) * 14, 180 + (i % 2) * 10);
        await page.waitForTimeout(Math.round(settleMs / steps));
      }
      return page.evaluate(() => window.WsCompanion.debugStage()?.vrm?.coLead || null);
    };

    const dockCloseupClip = async () => page.evaluate(() => {
      const slot = document.getElementById('rail-companion-slot').getBoundingClientRect();
      return {
        x: Math.max(0, Math.floor(slot.left - 8)),
        y: Math.max(0, Math.floor(slot.top - 36)),
        width: Math.ceil(slot.width + 16),
        height: Math.min(window.innerHeight - Math.max(0, Math.floor(slot.top - 36)), Math.ceil(slot.height + 54))
      };
    });

    const railContextClip = async () => page.evaluate(() => {
      const rail = document.getElementById('rail').getBoundingClientRect();
      return {
        x: Math.max(0, Math.floor(rail.left - 6)),
        y: 0,
        width: Math.ceil(rail.width + 12),
        height: window.innerHeight
      };
    });

    // Helper to assert Co-Lead framing & prop invariants on each VRM version.
    const assertCoLeadInvariants = (label, co) => {
      assert.ok(co, `${label}: missing coLead debug state`);
      assert.equal(co.hasLaptopProp, true, `${label}: laptop prop must be mounted`);
      assert.ok(co.laptopTopViewportPct >= 10 && co.laptopTopViewportPct <= 19.5,
        `${label}: laptop top (${co.laptopTopViewportPct}%) must stay in bottom 10-19.5%`);
      assert.ok(co.laptopTopViewportPct < co.chinViewportPct - 20,
        `${label}: laptop top (${co.laptopTopViewportPct}%) must stay strictly below chin (${co.chinViewportPct}%)`);
    };

    const shots01 = [];
    const shots02 = [];
    const shots03 = [];
    const shots04 = [];
    const summary = {};

    for (const entry of [
      { id: vrm1ID, tag: 'VRM 1.0' },
      { id: vrm0ID, tag: 'VRM 0.x' }
    ]) {
      await applyPack(entry.id);

      // Sheet 1: Active Co-Lead typing pose inside left-rail dock (rail context + closeup).
      const typingSt = await settleBeat('typing', 650);
      assertCoLeadInvariants(entry.tag, typingSt);
      assert.equal(typingSt.beat, 'typing');
      assert.equal(typingSt.typing, true);
      summary[entry.tag] = {
        laptopTopViewportPct: typingSt.laptopTopViewportPct,
        chinViewportPct: typingSt.chinViewportPct,
        idleGlow: typingSt.screenGlow
      };
      if (shots) {
        shots01.push({
          label: `${entry.tag} — Left-Rail 3-Col Context (lid=${typingSt.laptopTopViewportPct}% / chin=${typingSt.chinViewportPct}%)`,
          buf: await page.screenshot({ clip: await railContextClip() }),
          maxW: 340
        });
        shots01.push({
          label: `${entry.tag} — Co-Lead Typing Closeup (340×240 dock)`,
          buf: await page.screenshot({ clip: await dockCloseupClip() }),
          maxW: 520
        });
      }

      // Sheet 2: Cute "glance up at user" beat (head-tilt + warm smile + camera eye contact).
      const glanceSt = await settleBeat('glance-up', 700);
      assert.equal(glanceSt.glanceUp, true, `${entry.tag}: glance-up beat must engage`);
      assert.equal(glanceSt.typing, false, `${entry.tag}: glance-up beat must pause typing`);
      if (shots) {
        shots02.push({
          label: `${entry.tag} — Glance-Up + Anime Head-Tilt & Warm Smile`,
          buf: await page.screenshot({ clip: await dockCloseupClip() }),
          maxW: 560
        });
      }

      // Also verify real pointer hover over .ws-companion-hit triggers glance-up automatically!
      await page.evaluate(() => window.WsCompanion.setCoLeadBeat('auto'));
      const hitBox = await page.locator('.ws-companion-hit').boundingBox();
      await page.mouse.move(hitBox.x + hitBox.width * 0.5, hitBox.y + hitBox.height * 0.5);
      await page.waitForFunction(() => window.WsCompanion.debugStage()?.vrm?.coLead?.glanceUp === true, null, { timeout: 5000 });
      await page.mouse.move(760, 180);

      // Sheet 3: Working/Thinking focused typing (luminous bounce glow) vs Sleepy/Doze (dimmed rest).
      const workSt = await settleBeat('working', 700);
      assert.equal(workSt.typing, true);
      assert.ok(workSt.screenGlow > 0.19, `${entry.tag}: working glow (${workSt.screenGlow}) should brighten`);
      const workBuf = shots ? await page.screenshot({ clip: await dockCloseupClip() }) : null;

      const sleepSt = await settleBeat('sleepy', 750);
      assert.equal(sleepSt.typing, false);
      assert.ok(sleepSt.screenGlow < 0.06, `${entry.tag}: sleepy glow (${sleepSt.screenGlow}) should dim`);
      const sleepBuf = shots ? await page.screenshot({ clip: await dockCloseupClip() }) : null;
      summary[entry.tag].workingGlow = workSt.screenGlow;
      summary[entry.tag].sleepyGlow = sleepSt.screenGlow;

      if (shots) {
        shots03.push({
          label: `${entry.tag} — Working / Focused Typing (glow=${workSt.screenGlow.toFixed(2)})`,
          buf: workBuf,
          maxW: 520
        });
        shots03.push({
          label: `${entry.tag} — Sleepy / Doze Rest (glow=${sleepSt.screenGlow.toFixed(2)})`,
          buf: sleepBuf,
          maxW: 520
        });
      }

      // Sheet 4: 4-frame sequence strip (left-scan -> right-scan -> glance-up smile -> back to typing).
      // Also verify Step 3 setLaptopAccent seam visibly updates without error.
      await page.evaluate(() => window.WsCompanion.setLaptopAccent('#7aa2ff'));
      const seqFrames = [
        { beat: 'reading-left', caption: `${entry.tag} · 1) Typing Left-Scan` },
        { beat: 'reading-right', caption: `${entry.tag} · 2) Typing Right-Scan` },
        { beat: 'glance-up', caption: `${entry.tag} · 3) Glance-Up Smile` },
        { beat: 'typing', caption: `${entry.tag} · 4) Back to Typing` }
      ];
      for (const sf of seqFrames) {
        await settleBeat(sf.beat, 650);
        if (shots) {
          shots04.push({
            label: sf.caption,
            buf: await page.screenshot({ clip: await dockCloseupClip() }),
            maxW: 400
          });
        }
      }
      const accentApplied = await page.evaluate(() => window.WsCompanion.setLaptopAccent('#f59e0b'));
      assert.equal(accentApplied, '#f59e0b');
      await page.evaluate(() => {
        window.WsCompanion.setLaptopAccent('#7aa2ff');
        window.WsCompanion.setCoLeadBeat('auto');
      });
    }

    if (shots) {
      await composeGrid(browser, path.join(shots, '01-typing-idle-vrm1-vrm0.png'),
        '01 · Co-Lead Typing Pose in Left-Rail 3-Column Dock (1680×960) — VRM 1.0 & VRM 0.x',
        shots01, { cols: 2 });
      await composeGrid(browser, path.join(shots, '02-glance-up-smile.png'),
        '02 · Co-Lead Glance-Up Beat — Anime Head-Tilt, Camera Eye Contact & Warm Smile',
        shots02, { cols: 2 });
      await composeGrid(browser, path.join(shots, '03-working-vs-sleepy.png'),
        '03 · Working / Focused Typing (Luminous Screen Glow) vs Sleepy / Doze (Dimmed Rest)',
        shots03, { cols: 2 });
      await composeGrid(browser, path.join(shots, '04-pose-sequence-strip.png'),
        '04 · 4-Frame Pose Sequence Strip — Left-Scan → Right-Scan → Glance-Up Smile → Back to Typing',
        shots04, { cols: 4 });
    }

    assert.deepEqual(pageErrors, [], `unexpected page errors: ${JSON.stringify(pageErrors)}`);
    assert.deepEqual(consoleErrors, [], `unexpected console errors: ${JSON.stringify(consoleErrors)}`);
    console.log('companion vrm colead browser: ok', JSON.stringify(summary));
  } finally {
    await browser.close();
  }
})().catch(err => {
  console.error(err);
  process.exit(1);
});
