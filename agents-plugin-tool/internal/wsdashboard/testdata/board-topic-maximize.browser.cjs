// Run by TestBoardTopicMaximizeBrowser: maximizing and restoring a tall topic
// card (or a panel inside it) from mid-scroll when its free-canvas Y has
// drifted away from FREE_RUNWAY_MID (50000) must keep the card visible at its
// pre-maximize viewport offset without blank runway or height-cache pollution.
const assert = require('node:assert/strict');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base] = process.argv.slice(2);

const wait = ms => new Promise(r => setTimeout(r, ms));
const frames = (page, n = 4) => page.evaluate(
  n => new Promise(r => {
    const step = () => (--n > 0 ? requestAnimationFrame(step) : r());
    requestAnimationFrame(step);
  }),
  n,
);

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, reducedMotion: 'reduce' });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', e => errors.push(e.message));
    await context.addInitScript(() => {
      const Socket = window.WebSocket;
      window.pageSockets = [];
      window.WebSocket = class extends Socket {
        constructor(...a) { super(...a); window.pageSockets.push(this); }
      };
    });

    await page.goto(base + '/s/wbmax');
    await page.waitForFunction(() => window.WsBoardAnchor && window.WsBoardAnchor.free());
    await frames(page, 6);

    const col = page.locator('#bcol');

    const snapshot = (topicName) => page.evaluate((name) => {
      const bcol = document.getElementById('bcol');
      const br = bcol.getBoundingClientRect();
      const sec = bcol.querySelector(`section.topic[data-topic="${name}"]`);
      const sr = sec ? sec.getBoundingClientRect() : null;
      const info = window.WsBoardAnchor.windowInfo();
      const hit = document.elementFromPoint(br.left + br.width / 2, br.top + br.height / 2);
      const hitTopic = hit && hit.closest ? hit.closest('section.topic[data-topic]') : null;
      return {
        scrollTop: bcol.scrollTop,
        colH: br.height,
        colTop: br.top,
        colBottom: br.bottom,
        posY: sec ? info.posOf(sec) : undefined,
        laidH: sec ? info.heightOf(sec) : undefined,
        styleTop: sec ? sec.style.top : '',
        hasMax: !!(sec && sec.hasAttribute('data-max')),
        hasPanelMax: !!(sec && sec.hasAttribute('data-has-panel-max')),
        offWindow: sec ? sec.getAttribute('data-wb-offwindow') : null,
        rectTop: sr ? sr.top : null,
        rectBottom: sr ? sr.bottom : null,
        rectH: sr ? sr.height : null,
        offset: sr ? sr.top - br.top : null,
        intersects: !!(sr && sr.height > 0 && sr.bottom > br.top + 10 && sr.top < br.bottom - 10),
        midHitTopic: hitTopic ? hitTopic.getAttribute('data-topic') : null,
        anchorKey: window.WsBoardAnchor.key(),
        winStart: info.start,
        winEnd: info.end,
      };
    }, topicName);

    // Ensure max-10 is tall (wait for its sandboxed doc-frame height report, or
    // boost its body block height deterministically if iframe is short).
    async function ensureTallTopic(name, minHeight) {
      await page.evaluate(({ name, minHeight }) => {
        const sec = document.querySelector(`#bcol section.topic[data-topic="${name}"]`);
        if (!sec) throw new Error('missing topic ' + name);
        window.WsBoardAnchor.select(sec, { smooth: false });
        if (sec.offsetHeight < minHeight) {
          const pad = document.createElement('div');
          pad.className = 'test-tall-spacer';
          pad.style.height = minHeight + 'px';
          pad.textContent = 'Tall topic content ' + name;
          const head = sec.querySelector(':scope > .tophead');
          sec.insertBefore(pad, head ? head.nextSibling : null);
        }
      }, { name, minHeight });
      await frames(page, 6);
    }

    await ensureTallTopic('max-10', 2200);
    await ensureTallTopic('max-02', 2200);

    // --- Case 1: Drifted mid-board tall topic (start > 0, posY != 50000) ---
    // Select max-15 (placed at FREE_RUNWAY_MID = 50000), then scroll up step by
    // step into the middle of tall max-10 so max-10's freePos drifts well below 50000.
    await page.evaluate(() => {
      const target = document.querySelector('#bcol section.topic[data-topic="max-15"]');
      window.WsBoardAnchor.select(target, { smooth: false });
    });
    await frames(page, 4);

    // Scroll upward in steps until max-10 is mounted in window and centered mid-card.
    for (let step = 0; step < 30; step++) {
      const st = await snapshot('max-10');
      if (typeof st.posY === 'number' && st.offWindow === null) break;
      await col.evaluate(el => { el.scrollTop = Math.max(0, el.scrollTop - 800); });
      await frames(page, 2);
    }
    await page.evaluate(() => {
      const bcol = document.getElementById('bcol');
      const sec = bcol.querySelector('section.topic[data-topic="max-10"]');
      const info = window.WsBoardAnchor.windowInfo();
      const y = info.posOf(sec);
      const h = info.heightOf(sec);
      // Scroll to the middle of the tall card.
      bcol.scrollTop = Math.round(y + h * 0.45);
    });
    await frames(page, 4);
    // Let idle recenter timer pass (y stays within [10000, 90000], so it stays drifted).
    await wait(250);
    await frames(page, 3);

    const before1 = await snapshot('max-10');
    assert.equal(before1.midHitTopic, 'max-10', 'precondition: mid-viewport hits max-10: ' + JSON.stringify(before1));
    assert.ok(before1.laidH >= 2000, 'precondition: max-10 is tall: ' + JSON.stringify(before1));
    assert.ok(Math.abs(before1.posY - 50000) > 1500, 'precondition: max-10 freePos drifted away from 50000: ' + JSON.stringify(before1));

    // Maximize -> trigger settle() while maximized -> restore (both button and Escape).
    for (const exit of ['button', 'Escape']) {
      const pre = await snapshot('max-10');
      await page.locator('#bcol section.topic[data-topic="max-10"] > .tophead .tbar > .d-max').click();
      await page.waitForFunction(() => document.querySelector('section.topic[data-topic="max-10"]').hasAttribute('data-max'));
      await frames(page, 3);

      // Simulate a layout settle while maximized (e.g. SSE update / resize / reveal).
      await page.evaluate(() => window.WsBoardAnchor.settle());
      await frames(page, 3);

      const whileMax = await snapshot('max-10');
      assert.equal(whileMax.hasMax, true, 'topic is maximized');
      assert.ok(typeof whileMax.posY === 'number', 'freePos preserved across maximize: ' + JSON.stringify(whileMax));
      assert.ok(whileMax.laidH >= pre.laidH - 5, 'freeHeightCache/freeLaidH not polluted by viewport height while maximized: ' + JSON.stringify({ pre, whileMax }));
      assert.ok(whileMax.rectH < pre.laidH - 500, 'maximized fixed sheet box is viewport-bounded: ' + JSON.stringify({ pre, whileMax }));

      if (exit === 'button') {
        await page.locator('section.topic[data-topic="max-10"] > .tophead .tbar > .d-min').click();
      } else {
        await page.keyboard.press('Escape');
      }
      await page.waitForFunction(() => !document.querySelector('section.topic[data-topic="max-10"]').hasAttribute('data-max'));
      await frames(page, 2);

      const after = await snapshot('max-10');
      assert.equal(after.hasMax, false, `${exit}: topic restored`);
      assert.equal(after.intersects, true, `${exit}: restored topic intersects #bcol (not blank): ` + JSON.stringify({ pre, after }));
      assert.equal(after.midHitTopic, 'max-10', `${exit}: middle of #bcol hits max-10 immediately without manual scroll: ` + JSON.stringify({ pre, after }));
      assert.ok(Math.abs(after.offset - pre.offset) <= 4, `${exit}: visual offset preserved: ` + JSON.stringify({ pre, after }));
      assert.ok(after.laidH > whileMax.rectH + 500 && Math.abs(after.laidH - after.rectH) <= 10, `${exit}: laid height tracks natural topic height (never viewport height): ` + JSON.stringify({ pre, whileMax, after }));
      assert.ok(after.styleTop && after.styleTop !== '', `${exit}: inline style.top restored: ` + JSON.stringify(after));
    }

    // Verify ResizeObserver was re-attached on restore: growing max-10 updates freeLaidH.
    const hBeforeGrow = (await snapshot('max-10')).laidH;
    await page.evaluate(() => {
      const spacer = document.querySelector('#bcol section.topic[data-topic="max-10"] .test-tall-spacer');
      spacer.style.height = (parseFloat(spacer.style.height) + 180) + 'px';
    });
    await frames(page, 6);
    const hAfterGrow = (await snapshot('max-10')).laidH;
    assert.ok(hAfterGrow >= hBeforeGrow + 150, `ResizeObserver re-observed topic after restore: ${hBeforeGrow} -> ${hAfterGrow}`);

    // --- Case 2: SSE board update shifts runway / modifies topic while maximized ---
    const preSse = await snapshot('max-10');
    await page.locator('#bcol section.topic[data-topic="max-10"] > .tophead .tbar > .d-max').click();
    await page.waitForFunction(() => document.querySelector('section.topic[data-topic="max-10"]').hasAttribute('data-max'));
    await frames(page, 2);

    // Inject a live board swap adding content to max-09 (above) and changing max-10.
    const sseSent = await page.evaluate(() => {
      const sock = window.pageSockets.filter(s => s.url.includes('/events') && s.readyState === 1).pop();
      if (!sock) return false;
      const el = document.querySelector('[data-region="board"]');
      // Clone current board topics HTML and modify max-09 to grow above max-10.
      const clone = el.cloneNode(true);
      clone.querySelectorAll('.board-toolbar, .bcard').forEach(n => n.remove());
      const t9 = clone.querySelector('section.topic[data-topic="max-09"]');
      if (t9) {
        t9.setAttribute('data-th', 'sse-max09-taller');
        const extra = document.createElement('div');
        extra.style.height = '320px';
        extra.textContent = 'SSE growth above maximized topic';
        t9.appendChild(extra);
      }
      sock.onmessage({ data: JSON.stringify({ event: 'region', data: { name: 'board', hash: 'sse-while-max', html: clone.innerHTML } }) });
      return true;
    });
    assert.ok(sseSent, 'delivered SSE board update while topic was maximized');
    await frames(page, 4);

    await page.locator('section.topic[data-topic="max-10"] > .tophead .tbar > .d-min').click();
    await page.waitForFunction(() => !document.querySelector('section.topic[data-topic="max-10"]').hasAttribute('data-max'));
    await frames(page, 3);

    const afterSse = await snapshot('max-10');
    assert.equal(afterSse.midHitTopic, 'max-10', 'after SSE update during maximize, restore lands on max-10: ' + JSON.stringify({ preSse, afterSse }));
    assert.ok(Math.abs(afterSse.offset - preSse.offset) <= 6, 'after SSE update during maximize, pre-maximize visual offset is preserved: ' + JSON.stringify({ preSse, afterSse }));

    // --- Case 3: Panel maximize inside the tall drifted topic ---
    await page.evaluate(() => {
      const bcol = document.getElementById('bcol');
      const panel = bcol.querySelector('section.topic[data-topic="max-10"] #p-mid');
      const pr = panel.getBoundingClientRect();
      const br = bcol.getBoundingClientRect();
      bcol.scrollTop += Math.round(pr.top - br.top - 180);
    });
    await frames(page, 4);
    const prePanel = await snapshot('max-10');
    await page.locator('#bcol section.topic[data-topic="max-10"] #p-mid > summary > .d-max').click();
    await page.waitForFunction(() => document.querySelector('#p-mid').hasAttribute('data-max'));
    await page.evaluate(() => window.WsBoardAnchor.settle());
    await frames(page, 3);
    await page.keyboard.press('Escape');
    await page.waitForFunction(() => !document.querySelector('#p-mid').hasAttribute('data-max'));
    await frames(page, 3);
    const afterPanel = await snapshot('max-10');
    const panelVisible = await page.evaluate(() => {
      const bcol = document.getElementById('bcol');
      const br = bcol.getBoundingClientRect();
      const pr = document.querySelector('#bcol section.topic[data-topic="max-10"] #p-mid').getBoundingClientRect();
      return pr.height > 0 && pr.bottom > br.top + 10 && pr.top < br.bottom - 10;
    });
    assert.equal(afterPanel.intersects, true, 'panel restore keeps tall drifted topic intersecting #bcol: ' + JSON.stringify({ prePanel, afterPanel }));
    assert.equal(panelVisible, true, 'panel restore keeps #p-mid visible in #bcol: ' + JSON.stringify({ prePanel, afterPanel }));
    assert.ok(Math.abs(afterPanel.offset - prePanel.offset) <= 6, 'panel restore preserves tall topic visual offset: ' + JSON.stringify({ prePanel, afterPanel }));
    assert.ok(afterPanel.laidH >= prePanel.laidH - 10, 'panel maximize did not pollute topic height: ' + JSON.stringify({ prePanel, afterPanel }));

    // --- Case 4: Top-of-board tall topic (start === 0, posY << 50000) ---
    await page.evaluate(() => {
      const t0 = document.querySelector('#bcol section.topic[data-topic="max-00"]');
      window.WsBoardAnchor.select(t0, { smooth: false });
    });
    await frames(page, 4);
    await page.evaluate(() => {
      const bcol = document.getElementById('bcol');
      const sec = bcol.querySelector('section.topic[data-topic="max-02"]');
      const info = window.WsBoardAnchor.windowInfo();
      bcol.scrollTop = Math.round(info.posOf(sec) + info.heightOf(sec) * 0.45);
    });
    await frames(page, 4);
    const preTop = await snapshot('max-02');
    assert.equal(preTop.winStart, 0, 'precondition: start === 0');
    assert.ok(preTop.posY < 5000, 'precondition: top-of-board topic posY << 50000: ' + JSON.stringify(preTop));
    assert.equal(preTop.midHitTopic, 'max-02', 'precondition: mid-viewport hits max-02');

    await page.locator('#bcol section.topic[data-topic="max-02"] > .tophead .tbar > .d-max').click();
    await page.waitForFunction(() => document.querySelector('section.topic[data-topic="max-02"]').hasAttribute('data-max'));
    await frames(page, 2);
    await page.locator('section.topic[data-topic="max-02"] > .tophead .tbar > .d-min').click();
    await page.waitForFunction(() => !document.querySelector('section.topic[data-topic="max-02"]').hasAttribute('data-max'));
    await frames(page, 3);

    const afterTop = await snapshot('max-02');
    assert.equal(afterTop.midHitTopic, 'max-02', 'top-of-board tall topic restore stays mid-card (does not jump to scrollTop=0): ' + JSON.stringify({ preTop, afterTop }));
    assert.ok(Math.abs(afterTop.offset - preTop.offset) <= 4, 'top-of-board tall topic visual offset preserved: ' + JSON.stringify({ preTop, afterTop }));

    assert.deepEqual(errors, [], 'no page errors');
    console.log('PASS board topic maximize: drifted mid-scroll, SSE-while-max, panel-max, and start=0 tall-card restore all preserved visibility and heights');
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exitCode = 1; });
