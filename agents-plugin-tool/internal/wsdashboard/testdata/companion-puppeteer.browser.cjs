// Run by TestCompanionPuppeteerCompactMessengerBrowser against the live Werkschaft dashboard shell.
const assert = require('node:assert/strict');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [base, brainRid, vrm0ID, vrm1ID] = process.argv.slice(2);

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

    const applyConfig = cfg => page.evaluate(detail => {
      document.dispatchEvent(new CustomEvent('ws-companion-config', { detail }));
    }, cfg);
    const sendCommand = cmd => page.evaluate(detail => {
      document.dispatchEvent(new CustomEvent('ws-companion-command', { detail }));
    }, cmd);

    await page.goto(base + '/s/livesession');
    await page.frameLocator('.sframe.on').locator('#grid').waitFor();

    // Test both VRM 1.0 and VRM 0.x packs.
    for (const pack of [{ id: vrm1ID, label: 'vrm1' }, { id: vrm0ID, label: 'vrm0' }]) {
      await applyConfig({ enabled: true, director: 'rules', pack: pack.id, preset: '' });
      await page.waitForFunction(id => {
        const st = window.WsCompanion?.debugStage?.();
        return st && st.renderer === 'vrm' && st.pack === id && st.vrm && st.vrm.loaded && st.vrm.framesRendered >= 2;
      }, pack.id, { timeout: 25000 });

      // 1. Open Companion Messenger and verify compact Brain iframe mounts with data-compact="1".
      await page.locator('#companion-chat-btn').click();
      const cmFrame = page.locator('iframe.ws-cm-frame');
      await cmFrame.waitFor({ state: 'visible', timeout: 10000 });
      const frameSrc = await cmFrame.getAttribute('src');
      assert.ok(frameSrc && frameSrc.includes(`/s/${brainRid}?frame=1&compact=1`),
        `${pack.label}: expected compact Brain iframe src, got ${frameSrc}`);

      const inner = page.frameLocator('iframe.ws-cm-frame');
      await inner.locator('#convo').waitFor({ state: 'visible', timeout: 10000 });
      const compactCheck = await page.evaluate(() => {
        const fr = document.querySelector('iframe.ws-cm-frame');
        const doc = fr && fr.contentDocument;
        if (!doc) return null;
        const root = doc.documentElement;
        const topEl = doc.querySelector('.top');
        const chead = doc.getElementById('chead');
        const bcol = doc.getElementById('bcol');
        return {
          dataFrame: root.getAttribute('data-frame'),
          dataCompact: root.getAttribute('data-compact'),
          topDisplay: topEl ? getComputedStyle(topEl).display : 'none',
          cheadDisplay: chead ? getComputedStyle(chead).display : 'none',
          bcolDisplay: bcol ? getComputedStyle(bcol).display : 'none',
          hasConvo: !!doc.getElementById('convo'),
          hasComposer: !!doc.getElementById('composer')
        };
      });
      assert.equal(compactCheck.dataFrame, '1');
      assert.equal(compactCheck.dataCompact, '1');
      assert.equal(compactCheck.topDisplay, 'none', 'compact iframe must hide .top');
      assert.equal(compactCheck.cheadDisplay, 'none', 'compact iframe must hide #chead');
      assert.equal(compactCheck.bcolDisplay, 'none', 'compact iframe must hide #bcol');
      assert.equal(compactCheck.hasConvo, true);
      assert.equal(compactCheck.hasComposer, true);

      // 2. Trigger 0ms Client Physical Reflex via genuine same-origin postMessage from the compact iframe.
      await inner.locator('body').evaluate((_, rid) => {
        window.parent.postMessage({ wsCompanionCompact: 'user-send', rid }, window.location.origin);
      }, brainRid);
      await page.waitForFunction(() => window.WsCompanion?.reflex?.isPhoneHoldActive?.() === true, null, { timeout: 3000 });

      // 3. Yield to authoritative Puppeteer command with stage quip (XSS textContent-only check).
      const quipText = '<b>Checking</b> that now!';
      await sendCommand({
        sessionId: '*',
        seq: pack.label === 'vrm1' ? 10 : 20,
        priority: 'high',
        expression: 'happy',
        gesture: 'phone_typing',
        quip: quipText,
        ttl_ms: 6000
      });
      await page.waitForFunction(() => {
        const el = document.querySelector('.ws-companion-quip');
        return el && !el.hidden && el.dataset.open === '1';
      }, null, { timeout: 5000 });

      const quipCheck = await page.evaluate(() => {
        const el = document.querySelector('.ws-companion-quip');
        const tailStyle = getComputedStyle(el, '::after');
        const bottom = document.querySelector('.shell-bottom');
        const r = el.getBoundingClientRect();
        const b = bottom ? bottom.getBoundingClientRect() : null;
        return {
          text: el.textContent,
          childBTag: !!el.querySelector('b'),
          tailX: el.style.getPropertyValue('--quip-tail-x'),
          tailContent: tailStyle ? tailStyle.content : '',
          placement: el.dataset.placement,
          rect: { left: r.left, top: r.top, right: r.right, bottom: r.bottom, width: r.width, height: r.height },
          vw: window.innerWidth,
          vh: window.innerHeight,
          bottomTop: b ? b.top : window.innerHeight,
          phoneHoldActive: window.WsCompanion?.reflex?.isPhoneHoldActive?.()
        };
      });
      assert.equal(quipCheck.text, quipText, 'quip must render verbatim via textContent');
      assert.equal(quipCheck.childBTag, false, 'quip must never parse HTML tags');
      assert.ok(quipCheck.tailX && quipCheck.tailX.endsWith('px'), `quip missing --quip-tail-x: ${JSON.stringify(quipCheck)}`);
      assert.ok(quipCheck.tailContent && quipCheck.tailContent !== 'none', `quip ::after speech tail missing: ${JSON.stringify(quipCheck)}`);
      assert.equal(quipCheck.phoneHoldActive, true, 'Puppeteer phone_typing must keep phone hold active');
      assert.ok(quipCheck.rect.width > 20 && quipCheck.rect.height > 16, `quip rect too small: ${JSON.stringify(quipCheck.rect)}`);
      assert.ok(quipCheck.rect.left >= 0 && quipCheck.rect.right <= quipCheck.vw + 1, `quip clipped horizontally: ${JSON.stringify(quipCheck.rect)}`);
      assert.ok(quipCheck.rect.top >= 0 && quipCheck.rect.bottom <= quipCheck.bottomTop + 2, `quip overlaps .shell-bottom: ${JSON.stringify(quipCheck)}`);

      // 4. Verify Light theme renders without errors and keeps bubble visible.
      await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'));
      await page.waitForTimeout(100);
      await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'dark'));

      // 5. Brain idle clears reflex/Puppeteer hold cleanly.
      await page.evaluate(({ rid, seq }) => {
        window.WsCompanion?.reflex?.onBrainState?.({ kind: 'brain_idle', replyId: rid, seq });
      }, { rid: brainRid, seq: pack.label === 'vrm1' ? 999 : 1000 });
      await page.waitForFunction(() => window.WsCompanion?.reflex?.isPhoneHoldActive?.() === false, null, { timeout: 3000 });

      // 6. Close Messenger popup and verify closed state.
      await page.locator('#companion-chat-btn').click();
      await page.waitForFunction(() => {
        const panel = document.querySelector('.ws-cm-popup');
        return !panel || (panel.classList.contains('is-closed') && panel.getAttribute('aria-hidden') === 'true');
      }, null, { timeout: 5000 });
    }

    assert.deepEqual(badResponses, [], `unexpected 4xx/5xx responses: ${badResponses}`);
    assert.deepEqual(pageErrors, [], `unexpected page errors: ${pageErrors}`);
    assert.deepEqual(consoleErrors, [], `unexpected console errors: ${consoleErrors}`);
    console.log('companion puppeteer compact messenger browser: ok');
  } finally {
    await browser.close();
  }
})().catch(err => {
  console.error(err);
  process.exit(1);
});
