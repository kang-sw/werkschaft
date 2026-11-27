// Run by TestCompanionVrmSwapDizzyBrowser against the live Werkschaft dashboard shell.
// Verifies Step 3 Gesture #2: real session-switch laptop swap (deterministic 6-color
// palette, smooth tuck-out/slide-in, strict bottom framing invariant) and rapid-switch
// dizzy easter egg (4 rapid left-rail session clicks -> cute sway + crisp 2D anime FX
// swirl/stars -> blink-shake + sheepish smile recovery -> typing resume) on BOTH VRM 1.0
// and VRM 0.x inside the 1680x960 left-rail dock.
// When shots dir is passed, writes the 4 Step 3 visual QA contact sheets.
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

    const keepActiveWait = async (ms = 600) => {
      const steps = Math.max(3, Math.ceil(ms / 110));
      for (let i = 0; i < steps; i++) {
        await page.mouse.move(760 + (i % 3) * 14, 180 + (i % 2) * 10);
        await page.waitForTimeout(Math.round(ms / steps));
      }
    };

    const settleBeat = async (beat, settleMs = 620) => {
      await page.evaluate(b => {
        window.WsCompanion.setVrmHover(null);
        window.WsCompanion.setCoLeadBeat(b);
      }, beat);
      await keepActiveWait(settleMs);
      return page.evaluate(() => window.WsCompanion.getCoLeadState());
    };

    const dockCloseupClip = async () => page.evaluate(() => {
      const slot = document.getElementById('rail-companion-slot').getBoundingClientRect();
      return {
        x: Math.max(0, Math.floor(slot.left - 8)),
        y: Math.max(0, Math.floor(slot.top - 26)),
        width: Math.ceil(slot.width + 16),
        height: Math.min(window.innerHeight - Math.max(0, Math.floor(slot.top - 26)), Math.ceil(slot.height + 42))
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

    const clickRailSession = async rid => {
      const link = page.locator(`#rlist a[href="/s/${rid}"]`).first();
      await link.waitFor({ state: 'visible', timeout: 10000 });
      await link.click();
    };

    const countVisibleFxPixels = async () => page.evaluate(() => {
      const c = document.querySelector('.ws-companion-vrm-fx');
      if (!c || c.hidden || !c.width || !c.height) return { visible: false, nonZeroPixels: 0 };
      const ctx = c.getContext('2d');
      const data = ctx.getImageData(0, 0, c.width, c.height).data;
      let nonZero = 0;
      for (let i = 3; i < data.length; i += 4) {
        if (data[i] > 18) nonZero++;
      }
      return { visible: true, nonZeroPixels: nonZero, width: c.width, height: c.height };
    });

    const assertFraming = (label, st) => {
      assert.ok(st, `${label}: missing coLead state`);
      assert.equal(st.hasLaptopProp, true, `${label}: laptop prop must be mounted`);
      assert.ok(st.laptopTopViewportPct >= 10 && st.laptopTopViewportPct <= 19.5,
        `${label}: laptop top (${st.laptopTopViewportPct}%) must stay strictly in bottom 10-19.5%`);
      assert.ok(st.laptopTopViewportPct < st.chinViewportPct - 20,
        `${label}: laptop top (${st.laptopTopViewportPct}%) must stay far below chin (${st.chinViewportPct}%)`);
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
      await page.evaluate(() => window.WsCompanion.setCoLeadBeat('auto'));

      // 1. Live single session switch via left-rail click -> deterministic laptop accent change & swap!
      await clickRailSession('alpha01');
      await page.waitForFunction(() => window.WsCompanion.getCoLeadState()?.activeSessionId === 'alpha01', null, { timeout: 8000 });
      await keepActiveWait(1050);
      const stA = await page.evaluate(() => window.WsCompanion.getCoLeadState());
      assertFraming(`${entry.tag} alpha01`, stA);

      // Find another rail session with a guaranteed distinct accent hex.
      const nextTarget = await page.evaluate(curHex => {
        for (let i = 2; i <= 8; i++) {
          const rid = `alpha0${i}`;
          const acc = window.WsCompanion.sessionAccentForKey(rid);
          if (acc && acc.hex !== curHex) return { rid, hex: acc.hex, id: acc.id };
        }
        return { rid: 'alpha02', hex: '', id: '' };
      }, stA.laptopAccent);

      await clickRailSession(nextTarget.rid);
      await page.waitForFunction(rid => window.WsCompanion.getCoLeadState()?.activeSessionId === rid, nextTarget.rid, { timeout: 8000 });
      // Wait for the ~0.85s swap animation to finish cleanly onto the new accent.
      await keepActiveWait(1150);
      const stB = await page.evaluate(() => window.WsCompanion.getCoLeadState());
      assert.equal(stB.laptopAccent, nextTarget.hex, `${entry.tag}: live rail click must swap laptop to ${nextTarget.rid}'s accent`);
      assert.equal(stB.swapActive, false, `${entry.tag}: laptop swap must settle back to rest`);
      assert.equal(stB.dizzyActive, false, `${entry.tag}: single switch must not trigger dizzy`);
      assertFraming(`${entry.tag} ${nextTarget.rid}`, stB);

      // 2. Live 4 rapid clicks in the left rail -> triggers dizzy easter egg + visible 2D anime FX -> clean recovery!
      // Wait >3s so sliding window starts fresh before the rapid 4-click test.
      await keepActiveWait(3000);
      const rapidSeq = ['alpha03', 'alpha04', 'alpha05', 'alpha06', 'alpha07'];
      let sawLiveDizzy = false;
      for (const rid of rapidSeq) {
        if (rid === (await page.evaluate(() => window.WsCompanion.getCoLeadState()?.activeSessionId))) continue;
        await clickRailSession(rid);
        await page.waitForTimeout(160);
        const cur = await page.evaluate(() => window.WsCompanion.getCoLeadState());
        if (cur?.dizzyActive) {
          sawLiveDizzy = true;
          break;
        }
      }
      assert.equal(sawLiveDizzy, true, `${entry.tag}: 4 rapid left-rail session clicks must trigger dizzy easter egg`);

      // Keep active so dizzy sway renders and verify 2D FX canvas actually paints crisp non-zero pixels!
      await keepActiveWait(420);
      const liveFx = await countVisibleFxPixels();
      assert.equal(liveFx.visible, true, `${entry.tag}: .ws-companion-vrm-fx must be visible during live dizzy`);
      assert.ok(liveFx.nonZeroPixels > 120, `${entry.tag}: dizzy FX canvas must paint crisp spirals & stars (got ${liveFx.nonZeroPixels} px)`);

      // Wait for dizzy + recovery (~3.6s total) to finish and return cleanly to typing with FX hidden!
      await page.waitForFunction(() => {
        const st = window.WsCompanion.getCoLeadState();
        const fx = document.querySelector('.ws-companion-vrm-fx');
        return st && !st.dizzyActive && st.typing && fx && fx.hidden;
      }, null, { timeout: 9000 });

      summary[entry.tag] = {
        liveSwapFrom: stA.laptopAccent,
        liveSwapTo: stB.laptopAccent,
        liveDizzyFxPixels: liveFx.nonZeroPixels,
        laptopTopViewportPct: stB.laptopTopViewportPct,
        chinViewportPct: stB.chinViewportPct
      };

      // 3. Deterministic visual QA contact sheet captures (when shots dir is provided).
      if (shots) {
        // Sheet 1: 4-frame Laptop Swap Sequence (Session A sapphire -> Tuck-Away -> Session B coral slide-in -> Session B settled typing).
        await page.evaluate(() => window.WsCompanion.setLaptopAccent('#5b9eff'));
        await settleBeat('typing', 600);
        shots01.push({ label: `${entry.tag} · 1) Session A Typing (Sapphire)`, buf: await page.screenshot({ clip: await dockCloseupClip() }), maxW: 390 });

        await settleBeat('swap-out', 520);
        shots01.push({ label: `${entry.tag} · 2) Mid-Swap Tuck-Away`, buf: await page.screenshot({ clip: await dockCloseupClip() }), maxW: 390 });

        await page.evaluate(() => window.WsCompanion.setLaptopAccent('#fb7185'));
        await settleBeat('swap-in', 520);
        shots01.push({ label: `${entry.tag} · 3) Session B Slide-In (Coral)`, buf: await page.screenshot({ clip: await dockCloseupClip() }), maxW: 390 });

        await settleBeat('typing', 620);
        shots01.push({ label: `${entry.tag} · 4) Session B Settled Typing`, buf: await page.screenshot({ clip: await dockCloseupClip() }), maxW: 390 });

        // Sheet 2: 4-Color Session Accent Variety (sapphire, mint-teal, amber, coral).
        const accents = [
          { name: 'Sapphire', hex: '#5b9eff' },
          { name: 'Mint-Teal', hex: '#34d399' },
          { name: 'Warm Amber', hex: '#f59e0b' },
          { name: 'Coral-Rose', hex: '#fb7185' }
        ];
        for (const ac of accents) {
          await page.evaluate(h => window.WsCompanion.setLaptopAccent(h), ac.hex);
          const acSt = await settleBeat('typing', 540);
          assertFraming(`${entry.tag} ${ac.name}`, acSt);
          shots02.push({
            label: `${entry.tag} · ${ac.name} (${ac.hex}) · lid=${acSt.laptopTopViewportPct}%`,
            buf: await page.screenshot({ clip: await dockCloseupClip() }),
            maxW: 390
          });
        }

        // Sheet 3: Peak Cute Dizzy Easter Egg (rail context + dock closeup).
        await page.evaluate(() => window.WsCompanion.setLaptopAccent('#a78bfa'));
        await settleBeat('dizzy', 680);
        const dizzyFx = await countVisibleFxPixels();
        assert.ok(dizzyFx.nonZeroPixels > 120, `${entry.tag}: dizzy beat must paint 2D spiral & stars FX`);
        shots03.push({
          label: `${entry.tag} · Left-Rail Context — Peak Dizzy + Unclipped FX`,
          buf: await page.screenshot({ clip: await railContextClip() }),
          maxW: 340
        });
        shots03.push({
          label: `${entry.tag} · Peak Dizzy Closeup (Sway + Flustered + Spirals & Stars)`,
          buf: await page.screenshot({ clip: await dockCloseupClip() }),
          maxW: 520
        });

        // Sheet 4: 3-Frame Recovery Strip (Peak Dizzy -> Blink + Head-Shake Clear -> Sheepish Smile & Typing).
        shots04.push({
          label: `${entry.tag} · 1) Peak Dizzy`,
          buf: await page.screenshot({ clip: await dockCloseupClip() }),
          maxW: 440
        });
        await settleBeat('dizzy-shake', 560);
        shots04.push({
          label: `${entry.tag} · 2) Blink + Head-Shake Clear`,
          buf: await page.screenshot({ clip: await dockCloseupClip() }),
          maxW: 440
        });
        await settleBeat('dizzy-recover', 620);
        shots04.push({
          label: `${entry.tag} · 3) Sheepish Smile & Back to Typing`,
          buf: await page.screenshot({ clip: await dockCloseupClip() }),
          maxW: 440
        });
      }

      await page.evaluate(() => window.WsCompanion.setCoLeadBeat('auto'));
    }

    // 4. Verify narrow-viewport hide/pause invariant remains intact after Step 3 swaps & dizzy!
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.waitForTimeout(300);
    const narrowState = await page.evaluate(() => window.WsCompanion.debugStage());
    assert.equal(narrowState.vrmRailVisible, false, 'narrow viewport (<1600px) must hide VRM');
    assert.equal(narrowState.hidden, true, 'narrow viewport must pause stage');
    const framesAtNarrow = narrowState.vrm.framesRendered;
    await page.waitForTimeout(400);
    const framesAfterNarrow = await page.evaluate(() => window.WsCompanion.debugStage()?.vrm?.framesRendered);
    assert.equal(framesAfterNarrow, framesAtNarrow, 'narrow viewport must render 0 VRM frames');

    if (shots) {
      await composeGrid(browser, path.join(shots, '01-laptop-swap-sequence.png'),
        '01 · Session-Switch Laptop Swap Sequence (1680×960 Left-Rail Dock) — VRM 1.0 & VRM 0.x',
        shots01, { cols: 4 });
      await composeGrid(browser, path.join(shots, '02-session-accent-variety.png'),
        '02 · Per-Session Laptop Accent Variety (Sapphire, Mint-Teal, Amber, Coral) — VRM 1.0 & VRM 0.x',
        shots02, { cols: 4 });
      await composeGrid(browser, path.join(shots, '03-dizzy-easter-egg.png'),
        '03 · Rapid-Switch Dizzy Easter Egg — Anime Sway, Flustered Expression & 2D Spiral/Stars FX',
        shots03, { cols: 2 });
      await composeGrid(browser, path.join(shots, '04-dizzy-recovery.png'),
        '04 · Dizzy Recovery Strip — Peak Dizzy → Blink/Head-Shake Clear → Sheepish Smile & Typing',
        shots04, { cols: 3 });
    }

    assert.deepEqual(pageErrors, [], `unexpected page errors: ${JSON.stringify(pageErrors)}`);
    assert.deepEqual(consoleErrors, [], `unexpected console errors: ${JSON.stringify(consoleErrors)}`);
    console.log('companion vrm swap+dizzy browser: ok', JSON.stringify(summary));
  } finally {
    await browser.close();
  }
})().catch(err => {
  console.error(err);
  process.exit(1);
});
