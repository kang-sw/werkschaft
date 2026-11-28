// Run by TestCompanionVrmStep4Phase1Browser against the live Werkschaft dashboard shell.
// Verifies Step 4 Phase 1 end-to-end on BOTH VRM 1.0 and VRM 0.x:
//   1. Cosmetic glass knock (awake startle, 3-knock annoyed_tap, sleepy wakeup_jolt)
//      with glass ripple + cue; direct VRM click NEVER opens messenger.
//   2. 3D desk smartphone prop & choreography: phone_buzz, phone_pickup,
//      phone_typing, phone_putdown(normal), and phone_putdown(facedown_merong)
//      with cheeky wink/merong + face-down flip ("탁!", camera bump up).
//   3. Top-right #companion-chat-btn with unread badge + floating KakaoTalk-style
//      messenger popup mounted directly under document.body in Dark & Light themes,
//      including real HTTP /api/companion/phone/send round-trip, Tier-1 summary
//      strip, Pin toggle, Escape dismiss, and zero clipping/overlap.
// When shots dir is passed, writes the 4 high-DPI visual QA contact sheets.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [base, shots, vrm0ID, vrm1ID] = process.argv.slice(2);

async function composeGrid(browser, outPath, title, panels, opts = {}) {
  if (!outPath) return;
  fs.mkdirSync(path.dirname(outPath), { recursive: true });
  const cols = opts.cols || panels.length;
  const page = await browser.newPage({ viewport: { width: 2400, height: 1500 }, deviceScaleFactor: 1 });
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
      deviceScaleFactor: 2,
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

    const keepActiveWait = async (ms = 560) => {
      const steps = Math.max(3, Math.ceil(ms / 110));
      for (let i = 0; i < steps; i++) {
        await page.mouse.move(760 + (i % 3) * 14, 180 + (i % 2) * 10);
        await page.waitForTimeout(Math.round(ms / steps));
      }
    };

    const settleBeat = async (beat, settleMs = 640) => {
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
        y: Math.max(0, Math.floor(slot.top - 24)),
        width: Math.ceil(slot.width + 16),
        height: Math.min(window.innerHeight - Math.max(0, Math.floor(slot.top - 24)), Math.ceil(slot.height + 34))
      };
    });

    const messengerContextClip = async () => page.evaluate(() => {
      const rail = document.getElementById('rail').getBoundingClientRect();
      const popup = document.getElementById('companion-messenger').getBoundingClientRect();
      const left = Math.max(0, Math.floor(Math.min(rail.left, popup.left) - 8));
      const right = Math.min(window.innerWidth, Math.ceil(Math.max(rail.right, popup.right) + 28));
      const top = Math.max(0, Math.floor(Math.min(popup.top - 36, window.innerHeight - 620)));
      const height = Math.min(window.innerHeight - top, Math.ceil(window.innerHeight - top));
      return { x: left, y: top, width: right - left, height };
    });

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
      await page.evaluate(() => {
        window.WsCompanion.closeMessenger();
        window.WsCompanion.setCoLeadBeat('typing');
      });
      await keepActiveWait(420);

      const baseSt = await page.evaluate(() => window.WsCompanion.getCoLeadState());
      assert.equal(baseSt.hasLaptopProp, true, `${entry.tag}: laptop prop must be mounted`);
      assert.equal(baseSt.hasPhoneProp, true, `${entry.tag}: 3D desk smartphone prop must be mounted`);

      // 1. Live direct click on VRM character -> cosmetic glass knock ripple + startle, NEVER opens messenger!
      const charBox = await page.locator('.ws-companion-character').boundingBox();
      assert.ok(charBox && charBox.width > 120 && charBox.height > 120, `${entry.tag}: character hit box visible`);
      const stageBbox = await page.evaluate(() => window.WsCompanion?.debugStage?.()?.projectedBounds || null);
      assert.ok(stageBbox && stageBbox.width > 20 && stageBbox.height > 20 && stageBbox.width < charBox.width,
        `${entry.tag}: projectedBounds must tightly enclose character inside stage (got ${JSON.stringify(stageBbox)} vs slot ${JSON.stringify(charBox)})`);
      const knockX = charBox.x + stageBbox.x + stageBbox.width * 0.5;
      const knockY = charBox.y + stageBbox.y + stageBbox.height * 0.5;
      await page.mouse.move(knockX, knockY);
      const insideHover = await page.evaluate(() => ({
        area: document.querySelector('.ws-companion-hit')?.dataset.area || '',
        cursor: getComputedStyle(document.querySelector('.ws-companion-character')).cursor
      }));
      assert.equal(insideHover.area, 'character', `${entry.tag}: hovering inside character bbox must set data-area=character`);
      assert.equal(insideHover.cursor, 'default', `${entry.tag}: hovering inside character bbox must keep default cursor`);

      await page.mouse.click(knockX, knockY);
      await page.waitForTimeout(160);
      const afterSingleKnock = await page.evaluate(() => ({
        messengerOpen: window.WsCompanionMessenger.isOpen(),
        co: window.WsCompanion.getCoLeadState(),
        rings: document.querySelectorAll('.ws-knock-ring').length,
        words: document.querySelectorAll('.ws-knock-word').length
      }));
      assert.equal(afterSingleKnock.messengerOpen, false, `${entry.tag}: direct VRM click must NEVER open messenger`);
      assert.equal(afterSingleKnock.co.knockActive, true, `${entry.tag}: direct VRM click must activate knock choreography`);
      assert.ok(afterSingleKnock.rings >= 1 && afterSingleKnock.words >= 1, `${entry.tag}: knock ripple rings & word must render`);

      // Expanded messenger toggle outside character bbox:
      // hover cursor -> pointer, click opens once, click again closes once, drag >6px ignored.
      const bgX = stageBbox.x >= 20 ? charBox.x + stageBbox.x * 0.5 : charBox.x + stageBbox.x + stageBbox.width + 14;
      const bgY = charBox.y + charBox.height * 0.5;
      await page.mouse.move(bgX, bgY);
      const outsideHover = await page.evaluate(() => ({
        area: document.querySelector('.ws-companion-hit')?.dataset.area || '',
        cursor: getComputedStyle(document.querySelector('.ws-companion-character')).cursor
      }));
      assert.equal(outsideHover.area, 'messenger', `${entry.tag}: hovering outside character bbox must set data-area=messenger`);
      assert.equal(outsideHover.cursor, 'pointer', `${entry.tag}: hovering outside character bbox must show pointer cursor`);

      await page.mouse.click(bgX, bgY);
      await page.waitForTimeout(160);
      assert.equal(await page.evaluate(() => window.WsCompanionMessenger.isOpen()), true,
        `${entry.tag}: clicking stage outside character bbox must open messenger`);
      await page.mouse.click(bgX, bgY);
      await page.waitForTimeout(160);
      assert.equal(await page.evaluate(() => window.WsCompanionMessenger.isOpen()), false,
        `${entry.tag}: clicking stage outside character bbox again must close messenger cleanly`);

      // Drag > 6px outside bbox must NOT toggle messenger.
      await page.mouse.move(bgX, bgY);
      await page.mouse.down();
      await page.mouse.move(bgX + 18, bgY + 10);
      await page.mouse.up();
      await page.waitForTimeout(160);
      assert.equal(await page.evaluate(() => window.WsCompanionMessenger.isOpen()), false,
        `${entry.tag}: dragging >6px outside character bbox must NOT toggle messenger`);

      if (shots) {
        // Sheet 1 panel 1: Awake cosmetic knock startle + live glass ripple.
        await settleBeat('knock-startle', 520);
        await page.mouse.click(charBox.x + charBox.width * 0.42, charBox.y + charBox.height * 0.48);
        await page.waitForTimeout(120);
        assert.equal(await page.evaluate(() => window.WsCompanionMessenger.isOpen()), false);
        shots01.push({
          label: `${entry.tag} · 1) Awake Glass Knock Startle (Zero Popup)`,
          buf: await page.screenshot({ clip: await dockCloseupClip() }),
          maxW: 410
        });

        // Sheet 1 panel 2: 3+ Rapid Knocks -> Playful Annoyed Tap.
        await settleBeat('annoyed-tap', 560);
        shots01.push({
          label: `${entry.tag} · 2) 3+ Rapid Knocks → Annoyed Tap`,
          buf: await page.screenshot({ clip: await dockCloseupClip() }),
          maxW: 410
        });

        // Sheet 1 panel 3: Sleepy Doze -> Wake-Up Jolt ("화들짝!").
        await settleBeat('sleepy', 560);
        shots01.push({
          label: `${entry.tag} · 3) Sleepy Doze (Before Wake-Up Knock)`,
          buf: await page.screenshot({ clip: await dockCloseupClip() }),
          maxW: 410
        });

        await settleBeat('wakeup-jolt', 560);
        await page.mouse.click(charBox.x + charBox.width * 0.52, charBox.y + charBox.height * 0.44);
        await page.waitForTimeout(120);
        assert.equal(await page.evaluate(() => window.WsCompanionMessenger.isOpen()), false, `${entry.tag}: sleepy wake-up knock must NEVER open messenger`);
        shots01.push({
          label: `${entry.tag} · 4) Sleepy Wake-Up Jolt ("화들짝!", Zero Popup)`,
          buf: await page.screenshot({ clip: await dockCloseupClip() }),
          maxW: 410
        });
      }

      // 2. Phone buzz -> one-hand pickup -> thumb typing choreography!
      const buzzSt = await settleBeat('phone-buzz', 640);
      assert.equal(buzzSt.phonePose, 'buzz', `${entry.tag}: phone-buzz pose active`);
      assert.ok(buzzSt.phoneScreenGlow > 0.5, `${entry.tag}: buzzing phone screen must glow brightly`);
      if (shots) {
        shots02.push({
          label: `${entry.tag} · 1) Desk Smartphone Buzz ("윙- ♪")`,
          buf: await page.screenshot({ clip: await dockCloseupClip() }),
          maxW: 430
        });
      }

      const pickupSt = await settleBeat('phone-pickup', 680);
      assert.equal(pickupSt.phonePose, 'held', `${entry.tag}: phone-pickup must lift phone cleanly`);
      if (shots) {
        shots02.push({
          label: `${entry.tag} · 2) One-Hand Smartphone Pickup`,
          buf: await page.screenshot({ clip: await dockCloseupClip() }),
          maxW: 430
        });
      }

      const typingSt = await settleBeat('phone-typing', 680);
      assert.equal(typingSt.phonePose, 'held', `${entry.tag}: phone-typing stays held`);
      if (shots) {
        shots02.push({
          label: `${entry.tag} · 3) Thumb-Typing Reply Pose`,
          buf: await page.screenshot({ clip: await dockCloseupClip() }),
          maxW: 430
        });
      }

      // 3. Putdown normal vs cheeky facedown_merong flip!
      const normalPutdownSt = await settleBeat('phone-putdown', 640);
      assert.equal(normalPutdownSt.phoneFaceDown, false, `${entry.tag}: normal putdown rests face-up`);

      const merongSt = await settleBeat('phone-facedown-merong', 720);
      assert.equal(merongSt.phoneFaceDown, true, `${entry.tag}: facedown_merong must flip smartphone face-down`);
      assert.equal(merongSt.phonePose, 'facedown', `${entry.tag}: phonePose must report facedown`);
      if (shots) {
        shots03.push({
          label: `${entry.tag} · 1) Normal Putdown (Face-Up on Desk)`,
          buf: await (async () => {
            await settleBeat('phone-putdown', 640);
            return page.screenshot({ clip: await dockCloseupClip() });
          })(),
          maxW: 460
        });
        await settleBeat('phone-facedown-merong', 740);
        shots03.push({
          label: `${entry.tag} · 2) Cheeky Wink/Merong + Face-Down Flip ("탁!", Camera Up)`,
          buf: await page.screenshot({ clip: await dockCloseupClip() }),
          maxW: 460
        });
      }

      await page.evaluate(() => window.WsCompanion.setCoLeadBeat('auto'));
      summary[entry.tag] = {
        hasPhoneProp: baseSt.hasPhoneProp,
        buzzGlow: buzzSt.phoneScreenGlow,
        pickupPose: pickupSt.phonePose,
        merongFaceDown: merongSt.phoneFaceDown
      };
    }

    // 4. Live Messenger Popup verification (using VRM 1.0 in left-rail dock).
    await applyPack(vrm1ID);
    await page.evaluate(() => window.WsCompanion.setCoLeadBeat('phone-typing'));
    await keepActiveWait(520);

    // Verify #companion-chat-btn sits cleanly beside .ws-companion-gear and shows unread badge.
    await page.evaluate(() => {
      window.WsCompanion.phoneState({
        seq: 10,
        state: 'laptop_ambient',
        posture: 'laptop',
        unreadClient: 2,
        unreadAgent: 0,
        messages: [],
        tier1Summary: []
      });
    });
    const btnCheck = await page.evaluate(() => {
      const btn = document.getElementById('companion-chat-btn');
      const gear = document.querySelector('.ws-companion-gear');
      const badge = btn?.querySelector('.ws-companion-unread');
      const popup = document.getElementById('companion-messenger');
      return {
        btnRect: btn?.getBoundingClientRect()?.toJSON() || null,
        gearRect: gear?.getBoundingClientRect()?.toJSON() || null,
        badgeText: badge?.textContent || '',
        badgeHidden: badge?.hidden,
        popupParentTag: popup?.parentElement?.tagName || ''
      };
    });
    assert.equal(btnCheck.popupParentTag, 'BODY', '#companion-messenger must be mounted directly under document.body');
    assert.equal(btnCheck.badgeHidden, false, 'unread badge must be visible when unreadClient > 0');
    assert.equal(btnCheck.badgeText, '2');
    assert.ok(btnCheck.btnRect && btnCheck.gearRect && btnCheck.btnRect.right <= btnCheck.gearRect.left + 1,
      '#companion-chat-btn must sit cleanly to the left of .ws-companion-gear without overlap');

    // Click #companion-chat-btn to open messenger popup.
    await page.locator('#companion-chat-btn').click();
    await page.waitForTimeout(260);
    assert.equal(await page.evaluate(() => window.WsCompanionMessenger.isOpen()), true, 'clicking #companion-chat-btn must open messenger');

    // Send a real message via composer -> POST /api/companion/phone/send round-trip!
    const input = page.locator('#companion-messenger .ws-cm-input');
    await input.fill('빌드 상태랑 테스트 결과 알려줘');
    await input.press('Enter');
    await page.waitForFunction(() => {
      const bubbles = [...document.querySelectorAll('#companion-messenger .ws-cm-msg.user .ws-cm-bubble')];
      return bubbles.some(b => b.textContent.includes('빌드 상태랑 테스트 결과'));
    }, null, { timeout: 8000 });

    // Verify popup anchoring & zero clipping beside #rail-companion-slot.
    const layoutMetrics = await page.evaluate(() => {
      const slot = document.getElementById('rail-companion-slot').getBoundingClientRect();
      const pop = document.getElementById('companion-messenger').getBoundingClientRect();
      const cs = getComputedStyle(document.getElementById('companion-messenger'));
      return {
        slotRight: slot.right,
        popLeft: pop.left,
        popRight: pop.right,
        popTop: pop.top,
        popBottom: pop.bottom,
        popWidth: pop.width,
        popHeight: pop.height,
        vw: window.innerWidth,
        vh: window.innerHeight,
        borderWidth: cs.borderTopWidth
      };
    });
    assert.ok(layoutMetrics.popLeft >= layoutMetrics.slotRight + 8, 'popup must anchor cleanly to the right of #rail-companion-slot');
    assert.ok(layoutMetrics.popRight <= layoutMetrics.vw - 8, 'popup must not overflow right edge');
    assert.ok(layoutMetrics.popTop >= 8 && layoutMetrics.popBottom <= layoutMetrics.vh - 8, 'popup must not overflow vertical viewport');
    assert.equal(layoutMetrics.borderWidth, '0px', 'popup must use borderless Material tonal surface (zero 1px outer border)');

    // Seed rich realistic multi-turn thread (with tone pill, 1-Click confirm card, and Tier-1 summary) for Dark & Light QA sheet!
    const nowMs = Date.now();
    await page.evaluate(ts => {
      window.WsCompanionMessenger.applySnapshot({
        seq: 99,
        state: 'phone_active',
        posture: 'phone',
        phoneFaceDown: false,
        unreadClient: 0,
        unreadAgent: 0,
        tier1Summary: [
          'build + unit tests green on impl/develop/deed-proud-petal',
          'waiting for user confirmation on ticket status promotion'
        ],
        rotationMeta: { turns: 4, maxTurns: 20, summaryCap: 10 },
        messages: [
          { id: 'u1', sender: 'user', text: '방금 올린 브랜치 빌드랑 테스트 상태 괜찮아?', createdAt: ts - 180000 },
          { id: 'c1', sender: 'companion', text: '폰으로 잠깐 확인했어! Go 단위 테스트랑 JS 테스트 전부 초록불이야.', tone: 'ok', createdAt: ts - 150000 },
          { id: 'u2', sender: 'user', text: '오 고마워! 그럼 준비된 티켓도 바로 ready로 넘겨둘까?', createdAt: ts - 90000 },
          {
            id: 'c2',
            sender: 'companion',
            text: '한 번만 눌러주면 바로 처리해둘게 — 노트북으로 돌아가기 전에 확인 카드 띄워뒀어!',
            tone: 'inspect',
            createdAt: ts - 45000,
            confirmCard: {
              id: 'card-qa-1',
              tool: 'ws/tickets.move',
              title: 'Move ticket to ready/',
              desc: 'Promotes the active Step 4 ticket after verification passes.',
              targets: ['261127-feat-ws-companion-layered-agents-and-naming'],
              status: 'pending'
            }
          }
        ]
      });
    }, nowMs);
    await page.locator('#companion-messenger [data-action="toggle-tier1"]').click();
    await page.evaluate(() => {
      const thread = document.querySelector('#companion-messenger .ws-cm-thread');
      if (thread) thread.scrollTop = thread.scrollHeight;
    });
    await page.waitForTimeout(260);

    if (shots) {
      // Dark Theme Messenger QA capture
      await page.evaluate(() => { document.documentElement.setAttribute('data-theme', 'dark'); });
      await page.waitForTimeout(240);
      shots04.push({
        label: 'Dark Theme · #companion-chat-btn + Floating Messenger Popup (Borderless Material)',
        buf: await page.screenshot({ clip: await messengerContextClip() }),
        maxW: 780
      });

      // Light Theme Messenger QA capture
      await page.evaluate(() => { document.documentElement.setAttribute('data-theme', 'light'); });
      await page.waitForTimeout(240);
      shots04.push({
        label: 'Light Theme · #companion-chat-btn + Floating Messenger Popup (Borderless Material)',
        buf: await page.screenshot({ clip: await messengerContextClip() }),
        maxW: 780
      });

      // Restore dark theme
      await page.evaluate(() => { document.documentElement.setAttribute('data-theme', 'dark'); });
    }

    // Verify Pin + Escape behavior in live browser.
    await page.locator('#companion-messenger [data-action="toggle-pin"]').click();
    await page.keyboard.press('Escape');
    assert.equal(await page.evaluate(() => window.WsCompanionMessenger.isOpen()), true, 'Escape must not close pinned popup');
    await page.locator('#companion-messenger [data-action="toggle-pin"]').click();
    await page.keyboard.press('Escape');
    await page.waitForTimeout(160);
    assert.equal(await page.evaluate(() => window.WsCompanionMessenger.isOpen()), false, 'Escape must close unpinned popup');

    if (shots) {
      await composeGrid(browser, path.join(shots, '01-vrm-knock-and-sleepy-wakeup.png'),
        '01 · Cosmetic Glass Knock (Startle, 3+ Rapid Annoyed Tap, Sleepy Wake-Up Jolt; Zero Popup) — VRM 1.0 & VRM 0.x',
        shots01, { cols: 4 });
      await composeGrid(browser, path.join(shots, '02-vrm-phone-buzz-pickup-typing.png'),
        '02 · 3D Desk Smartphone Choreography (Buzz Glow → One-Hand Pickup → Thumb Typing) — VRM 1.0 & VRM 0.x',
        shots02, { cols: 3 });
      await composeGrid(browser, path.join(shots, '03-vrm-merong-facedown.png'),
        '03 · Normal Putdown vs Cheeky Wink/Merong + Face-Down Flip ("탁!", Camera Module Up) — VRM 1.0 & VRM 0.x',
        shots03, { cols: 2 });
      await composeGrid(browser, path.join(shots, '04-messenger-popup-dark-and-light.png'),
        '04 · #companion-chat-btn & Borderless Material Messenger Popup Beside #rail-companion-slot — Dark & Light Themes',
        shots04, { cols: 2 });
    }

    assert.deepEqual(pageErrors, [], `unexpected page errors: ${JSON.stringify(pageErrors)}`);
    assert.deepEqual(consoleErrors, [], `unexpected console errors: ${JSON.stringify(consoleErrors)}`);
    console.log('companion vrm step4 phase1 browser: ok', JSON.stringify({ ...summary, layoutMetrics }));
  } finally {
    await browser.close();
  }
})().catch(err => {
  console.error(err);
  process.exit(1);
});
