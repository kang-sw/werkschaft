'use strict';
const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const base = process.env.QCARDS_BASE;
const captureDir = process.env.WS_QUESTION_CARDS_CAPTURE_DIR || '';

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1360, height: 900 } });
    const page = await context.newPage();
    page.on('pageerror', err => console.error('PAGEERROR:', err));
    page.on('console', msg => { if (msg.type() === 'error') console.error('CONSOLE:', msg.text()); });

    // Spy on localStorage.getItem to guarantee zero direct reads of ws.wb.holds.*
    // from dashboard-conversation.js (Decision 5 & 10).
    await page.addInitScript(() => {
      window.__holdStorageReads = [];
      const origGet = Storage.prototype.getItem;
      Storage.prototype.getItem = function (key) {
        if (typeof key === 'string' && key.indexOf('ws.wb.holds.') === 0) {
          const stack = (new Error()).stack || '';
          window.__holdStorageReads.push({ key, stack });
        }
        return origGet.call(this, key);
      };
    });

    await page.goto(`${base}/s/qcards?lang=en&theme=dark`, { waitUntil: 'domcontentloaded' });
    await page.waitForSelector('.wbupdate-cap[data-wb-item-id="1"]', { timeout: 10000 });

    // 1. All 4 classes hydrated as 1-line 22px cellophane capsules + outside dim status in .wbupdate-ans.
    const initial = await page.evaluate(() => {
      const card = document.querySelector('[data-entry="e-001"] .wbupdate-topic');
      const items = [1, 2, 3, 4, 5, 6, 7, 8].map(id => {
        const cap = card.querySelector(`.wbupdate-cap[data-wb-item-id="${id}"]`);
        const wrap = cap && cap.closest('.wbupdate-qitem');
        const ans = wrap && wrap.querySelector('.wbupdate-ans');
        const rect = cap ? cap.getBoundingClientRect() : null;
        return {
          id,
          cls: cap ? cap.className : '',
          idText: cap && cap.querySelector('.wbupdate-cap-id')?.textContent,
          clsText: cap && cap.querySelector('.wbupdate-cap-cls')?.textContent,
          ttlText: cap && cap.querySelector('.wbupdate-cap-ttl')?.textContent,
          arrText: cap && cap.querySelector('.wbupdate-cap-arr')?.textContent,
          height: rect ? Math.round(rect.height) : 0,
          state: wrap ? wrap.getAttribute('data-wb-state') : '',
          ansClass: ans ? ans.className : '',
          ansText: ans ? ans.textContent : '',
        };
      });
      const missing = card.querySelector('.wbupdate-item-id[data-wb-jump="item-999"]');
      const statusText = card.querySelector('.wbupdate-status')?.textContent || '';
      return { items, missingClass: missing?.className || '', missingText: missing?.textContent || '', statusText };
    });

    assert.equal(initial.items[0].cls.includes('wbupdate-cls-policy'), true, 'item 1 policy class');
    assert.equal(initial.items[1].cls.includes('wbupdate-cls-technical'), true, 'item 2 technical class');
    assert.equal(initial.items[2].cls.includes('wbupdate-cls-announced'), true, 'item 3 announced class');
    assert.equal(initial.items[3].cls.includes('wbupdate-cls-check'), true, 'item 4 check class');
    for (const it of initial.items) {
      assert.equal(it.idText, `(${it.id})`, `item ${it.id} id`);
      assert.ok(it.clsText && it.clsText.length > 0, `item ${it.id} class micro-badge`);
      assert.ok(it.ttlText && it.ttlText.length > 0, `item ${it.id} title`);
      assert.equal(it.arrText, '↗', `item ${it.id} hover arrow`);
      assert.equal(it.height, 22, `item ${it.id} capsule height must be 22px`);
    }
    // Open items 1..4 are open unanswered (ask-time row for item 1 must NOT drive settled status).
    for (let i = 0; i < 4; i++) {
      assert.equal(initial.items[i].state, 'open', `item ${i + 1} open state`);
      assert.equal(initial.items[i].ansText, 'unanswered', `item ${i + 1} unanswered`);
    }
    // Settled items 5..8 are settled at load.
    assert.equal(initial.items[4].state, 'settled');
    assert.ok(initial.items[4].ansText.startsWith('✓') && initial.items[4].ansText.includes('B'), `item 5 settled: ${initial.items[4].ansText}`);
    assert.equal(initial.items[5].state, 'settled');
    assert.ok(initial.items[5].ansText.startsWith('✓'), `item 6 settled: ${initial.items[5].ansText}`);
    assert.equal(initial.items[6].state, 'settled');
    assert.ok(initial.items[6].ansText.startsWith('✓'), `item 7 assumed settled: ${initial.items[6].ansText}`);
    assert.equal(initial.items[7].state, 'settled');
    assert.ok(initial.items[7].ansText.startsWith('✓'), `item 8 withdrawn settled: ${initial.items[7].ansText}`);
    // Missing item 999 stays bare fallback (999) chip.
    assert.equal(initial.missingClass, 'wbupdate-item-id');
    assert.equal(initial.missingText, '(999)');
    // Dedicated .wbupdate-status live region untouched.
    assert.equal(initial.statusText, '');

    // 2. Interactive sync: hold via pill.js -> staged pick -> memo with capsules & XSS -> sent-held/sent-sent -> precedence.
    // Toggle hold on item 1 via board hold button (handled by pill.js).
    await page.locator('#r-board #item-1 .wb-hold-toggle').click();
    await page.waitForFunction(() => {
      const a = document.querySelector('[data-entry="e-001"] .wbupdate-qitem[data-wb-qitem="1"] .wbupdate-ans');
      const b = document.querySelector('[data-entry="e-003"] .wbupdate-qitem[data-wb-qitem="1"] .wbupdate-ans');
      return a && b && a.textContent === 'held' && b.textContent === 'held';
    });

    // Verify duplicate refs synced without disrupting convo scrollTop.
    await page.evaluate(() => { const c = document.getElementById('convo'); if (c) c.scrollTop = 12; });
    const scrollBefore = await page.evaluate(() => document.getElementById('convo')?.scrollTop || 0);

    // Stage policy pick a on item 1 (while still held! Decision 9: staged > hold).
    await page.locator('#r-board #item-1 .opt[data-opt="a"]').click();
    await page.waitForFunction(() => {
      const a = document.querySelector('[data-entry="e-001"] .wbupdate-qitem[data-wb-qitem="1"] .wbupdate-ans');
      const b = document.querySelector('[data-entry="e-003"] .wbupdate-qitem[data-wb-qitem="1"] .wbupdate-ans');
      return a && b && a.textContent === 'a selected' && b.textContent === 'a selected';
    });
    const scrollAfter = await page.evaluate(() => document.getElementById('convo')?.scrollTop || 0);
    assert.equal(scrollAfter, scrollBefore, 'syncing duplicate references must not disrupt scrollTop');

    // Type memo with capsule tokens + hostile XSS into item 1 textarea.note.
    await page.evaluate(() => {
      const note = document.querySelector('#r-board #item-1 textarea.note');
      note.hidden = false;
      note.value = '#Design [Image #1] <mailbox from="lead">hi</mailbox> <img src=x onerror="window.__xss=1">';
      note.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await page.waitForFunction(() => {
      const a = document.querySelector('[data-entry="e-001"] .wbupdate-qitem[data-wb-qitem="1"] .wbupdate-ans');
      return a && a.textContent.includes('#Design [Image #1] hi <img src=x');
    });
    const xssTriggered = await page.evaluate(() => !!window.__xss);
    assert.equal(xssTriggered, false, 'memo preview must be XSS-safe (textContent only)');

    // Check ok / no staging (including without attribute mutation on checkbox).
    await page.locator('#r-board #item-4 label.ck input[type="checkbox"]').click();
    await page.waitForFunction(() => {
      const a = document.querySelector('[data-entry="e-001"] .wbupdate-qitem[data-wb-qitem="4"] .wbupdate-ans');
      const b = document.querySelector('[data-entry="e-003"] .wbupdate-qitem[data-wb-qitem="4"] .wbupdate-ans');
      return a && b && a.textContent === 'ok selected' && b.textContent === 'ok selected' && a.classList.contains('st-staged-ok');
    });

    // Switch check 4 to no.
    await page.locator('#r-board #item-4 button.no-toggle').click();
    await page.waitForFunction(() => {
      const a = document.querySelector('[data-entry="e-001"] .wbupdate-qitem[data-wb-qitem="4"] .wbupdate-ans');
      return a && a.textContent === 'no selected' && a.classList.contains('st-staged-no');
    });

    // Memo-only staged state on item 2 (no pick), then clear back to unanswered.
    await page.evaluate(() => {
      const note = document.querySelector('#r-board #item-2 textarea.note');
      note.hidden = false;
      note.value = 'check caching later';
      note.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await page.waitForFunction(() => {
      const a2 = document.querySelector('[data-entry="e-001"] .wbupdate-qitem[data-wb-qitem="2"] .wbupdate-ans');
      return a2 && a2.textContent === 'note: check caching later' && a2.classList.contains('st-staged');
    });
    await page.evaluate(() => {
      const note = document.querySelector('#r-board #item-2 textarea.note');
      note.value = '';
      note.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await page.waitForFunction(() => {
      const a2 = document.querySelector('[data-entry="e-001"] .wbupdate-qitem[data-wb-qitem="2"] .wbupdate-ans');
      return a2 && a2.textContent === 'unanswered' && a2.classList.contains('st-open');
    });

    // Bulk ack (#ack-all) stages technical (2) and announced (3) to ok without attr mutation.
    await page.evaluate(() => {
      const all = document.querySelector('#r-board #ack-all');
      all.checked = true;
      all.dispatchEvent(new Event('change', { bubbles: true }));
    });
    await page.waitForFunction(() => {
      const a2 = document.querySelector('[data-entry="e-001"] .wbupdate-qitem[data-wb-qitem="2"] .wbupdate-ans');
      const a3 = document.querySelector('[data-entry="e-001"] .wbupdate-qitem[data-wb-qitem="3"] .wbupdate-ans');
      return a2 && a3 && a2.textContent === 'ok selected' && a3.textContent === 'ok selected';
    });

    // Unstage item 2 via composer chip remove button (dropChip -> syncWbUpdateItems) when present, else checkbox.
    const unchipBtn = page.locator('#cchips .achip .unchip[data-unchip="02"], #cchips .achip .unchip[data-unchip="2"]').first();
    if (await unchipBtn.count() > 0) {
      await unchipBtn.click();
    } else {
      await page.locator('#r-board #item-2 label.ack input[type="checkbox"]').click();
    }
    await page.waitForFunction(() => {
      const a2 = document.querySelector('[data-entry="e-001"] .wbupdate-qitem[data-wb-qitem="2"] .wbupdate-ans');
      return a2 && a2.textContent === 'unanswered';
    });

    // In-flight sent precedence (.sent-held and .sent-sent > staged > hold).
    await page.evaluate(() => {
      document.querySelector('#r-board #item-1').classList.add('sent-held');
      document.querySelector('#r-board #item-3').classList.add('sent-sent');
    });
    await page.waitForFunction(() => {
      const a1 = document.querySelector('[data-entry="e-001"] .wbupdate-qitem[data-wb-qitem="1"] .wbupdate-ans');
      const a3 = document.querySelector('[data-entry="e-001"] .wbupdate-qitem[data-wb-qitem="3"] .wbupdate-ans');
      return a1 && a3 && a1.textContent === 'sent · held' && a3.textContent === 'sent · waiting';
    });

    // Capture dark screenshot while all states are visible (open, staged, held, sent, settled).
    if (captureDir) {
      fs.mkdirSync(captureDir, { recursive: true });
      await page.locator('#r-board #item-2 .wb-hold-toggle').click();
      await page.waitForFunction(() => {
        const a2 = document.querySelector('[data-entry="e-001"] .wbupdate-qitem[data-wb-qitem="2"] .wbupdate-ans');
        return a2 && a2.textContent === 'held';
      });
      await page.locator('[data-entry="e-001"] .wbupdate-topic').screenshot({ path: path.join(captureDir, 'live-dark.png') });
    }

    // Settle item 1 after mount (simulating SSE board update with real server-rendered page.tmpl).
    const settledBoardHtml = await page.evaluate(async (url) => {
      const res = await fetch(url);
      return await res.text();
    }, `${base}/s/qcards/settle?lang=en`);
    await page.evaluate((html) => {
      const board = document.getElementById('r-board');
      board.innerHTML = html;
    }, settledBoardHtml);
    await page.waitForFunction(() => {
      const a1 = document.querySelector('[data-entry="e-001"] .wbupdate-qitem[data-wb-qitem="1"] .wbupdate-ans');
      const b1 = document.querySelector('[data-entry="e-003"] .wbupdate-qitem[data-wb-qitem="1"] .wbupdate-ans');
      return a1 && b1 && a1.textContent.startsWith('✓') && a1.textContent.toLowerCase().includes('a') && b1.textContent === a1.textContent;
    });

    // Live-inserted .wbupdate-topic hydrates immediately via WsWbUpdateCards.hydrate.
    await page.evaluate(() => {
      const box = document.querySelector('.centries');
      const art = document.createElement('article');
      art.setAttribute('data-entry', 'e-004');
      art.className = 'wbeffects wbupdate';
      art.innerHTML = '<section class="wbupdate-topic" data-wb-topic="topic-design"><div class="wbupdate-rows"><div class="wbupdate-items"><a class="wbupdate-item-id" href="#item-1" data-wb-jump="item-1" data-wb-topic="topic-design">(1)</a></div></div><span class="wbupdate-status" role="status" aria-live="polite"></span></section>';
      box.appendChild(art);
      window.WsWbUpdateCards.hydrate(art);
    });
    const liveHydrated = await page.evaluate(() => {
      const cap = document.querySelector('[data-entry="e-004"] .wbupdate-cap[data-wb-item-id="1"]');
      const ans = document.querySelector('[data-entry="e-004"] .wbupdate-ans');
      return { ok: !!cap, ans: ans ? ans.textContent : '' };
    });
    assert.equal(liveHydrated.ok, true, 'live-inserted card hydrates immediately');
    assert.ok(liveHydrated.ans.startsWith('✓'), 'live-inserted card reflects settled state');

    // Click capsule jumps to board item (focuses target on #r-board and leaves .wbupdate-status empty).
    await page.locator('[data-entry="e-001"] .wbupdate-cap[data-wb-item-id="4"]').click();
    const jumpState = await page.evaluate(() => ({
      status: document.querySelector('[data-entry="e-001"] .wbupdate-status')?.textContent || '',
      focusedId: document.activeElement ? document.activeElement.id : '',
      inBoard: !!(document.activeElement && document.activeElement.closest('#r-board'))
    }));
    assert.equal(jumpState.status, '', 'clicking valid capsule jumps without missing error');
    assert.equal(jumpState.inBoard, true, `clicking valid capsule focuses target on #r-board: ${JSON.stringify(jumpState)}`);
    assert.equal(jumpState.focusedId, 'item-4', `clicking valid capsule focuses #item-4: ${JSON.stringify(jumpState)}`);

    // Verify ZERO localStorage ws.wb.holds.* reads from dashboard-conversation.js.
    const badReads = await page.evaluate(() =>
      (window.__holdStorageReads || []).filter(r => !r.stack.includes('wbPill') && r.stack.includes('wbUpdate'))
    );
    assert.equal(badReads.length, 0, `dashboard-conversation.js must not read ws.wb.holds.* directly: ${JSON.stringify(badReads)}`);

    // 3. Light theme capture + verification.
    if (captureDir) {
      const pageLight = await context.newPage();
      await pageLight.goto(`${base}/s/qcards?lang=en&theme=light&reset=1`, { waitUntil: 'domcontentloaded' });
      await pageLight.waitForSelector('.wbupdate-cap[data-wb-item-id="1"]');
      await pageLight.locator('#r-board #item-1 .opt[data-opt="a"]').click();
      await pageLight.locator('#r-board #item-2 .wb-hold-toggle').click();
      await pageLight.locator('#r-board #item-4 label.ck input[type="checkbox"]').click();
      await pageLight.evaluate(() => {
        document.querySelector('#r-board #item-3').classList.add('sent-sent');
      });
      await pageLight.waitForFunction(() => {
        const a3 = document.querySelector('[data-entry="e-001"] .wbupdate-qitem[data-wb-qitem="3"] .wbupdate-ans');
        return a3 && a3.textContent === 'sent · waiting';
      });
      await pageLight.locator('[data-entry="e-001"] .wbupdate-topic').screenshot({ path: path.join(captureDir, 'live-light.png') });
      await pageLight.close();
    }

    // 4. Graceful fallback in history-only view (no #r-board).
    const pageHist = await context.newPage();
    await pageHist.goto(`${base}/s/qcards?lang=en&theme=dark&history=1`, { waitUntil: 'domcontentloaded' });
    const histState = await pageHist.evaluate(() => {
      const caps = document.querySelectorAll('.wbupdate-cap').length;
      const bare = document.querySelectorAll('.wbupdate-item-id').length;
      return { caps, bare };
    });
    assert.equal(histState.caps, 0, 'history-only view without board must not hydrate fake capsules');
    assert.ok(histState.bare > 0, 'history-only view retains bare (ID) chips');
    await pageHist.close();

    console.log('ALL QUESTION CARD BROWSER CHECKS PASSED');
  } finally {
    await browser.close();
  }
})().catch(err => {
  console.error(err);
  process.exit(1);
});
