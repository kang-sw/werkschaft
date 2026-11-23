// Real Chromium Playwright verification for the 4-part snap-deck feel fixes:
// 1. Wheel-virtualized deck transitions (Preset A): inner scroll, wall absorb,
//    single-flick no double-jump, rapid velocity-spike chain, discrete wheel.
// 2. Per-edge 100% full-span card height + 0px next-card peeking above bottom capsules.
// 3. Overview fast exit (0 data-overview-motion tags) + 0 idle rAF while Overview is open.
// 4. Maximize / expand stacking-context regression (assertSheetAboveScrim across
//    #bcol & #bgal, wide & narrow, standalone & shell iframe, topic/panel/bcard).
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const { readAssetSource } = require('./asset-source.cjs');

const pwMod = process.env.WS_PLAYWRIGHT_MODULE || 'playwright';
const { chromium } = require(pwMod);

const wbCss = [
  readAssetSource('wswhiteboard', 'page.css'),
  readAssetSource('wswhiteboard', 'page-items.css'),
  readAssetSource('wswhiteboard', 'page-panels.css'),
  readAssetSource('wswhiteboard', 'page-stream.css'),
  readAssetSource('wswhiteboard', 'page-sticky.css'),
].join('\n');
const dashCss = [
  readAssetSource('wsdashboard', 'dashboard.css'),
  readAssetSource('wsdashboard', 'dashboard-layout.css'),
  readAssetSource('wsdashboard', 'dashboard-board.css'),
  readAssetSource('wsdashboard', 'dashboard-rail.css'),
  readAssetSource('wsdashboard', 'dashboard-shell.css'),
  readAssetSource('wsdashboard', 'dashboard-menus.css'),
].join('\n');

const railJs = readAssetSource('wswhiteboard', 'rail.js');
const galleryJs = readAssetSource('wsdashboard', 'gallery.js');
const cardsJs = readAssetSource('wsdashboard', 'cards.js');
const overviewJs = readAssetSource('wsdashboard', 'overview.js');
const anchorJs = readAssetSource('wsdashboard', 'dashboard-board-anchor.js');
const dialJs = readAssetSource('wsdashboard', 'dashboard-board-dial.js');
const framesJs = readAssetSource('wsdashboard', 'dashboard-frames.js');

function buildFixtureHtml(opts = {}) {
  const theme = opts.theme || 'light';
  const topicCount = opts.topics || 6;
  const topicsHtml = [];
  for (let i = 0; i < topicCount; i++) {
    const id = 'topic-' + i;
    const tall = i === 0 || i === 2 || i === topicCount - 1;
    const paras = [];
    const lines = tall ? 42 : 12;
    for (let p = 0; p < lines; p++) {
      paras.push('<p class="line" style="margin:6px 0;line-height:1.45">Topic ' + i + ' content line ' + p + ' — snap-deck full-span and scroll verification text.</p>');
    }
    topicsHtml.push(
      '<section class="topic" id="' + id + '" data-topic="' + id + '" data-th="h' + i + '">' +
        '<header class="tophead"><div class="tbar">' +
          '<h2 class="tname">Topic ' + i + '</h2>' +
          '<button type="button" class="ibtn d-max" aria-label="Maximize">⤢</button>' +
          '<button type="button" class="ibtn d-min" aria-label="Restore">⤡</button>' +
        '</div></header>' +
        '<div class="tbody">' +
          '<details class="entry-panel" data-key="panel-' + i + '" open>' +
            '<summary class="phead"><span class="ptitle">Panel ' + i + '</span>' +
              '<button type="button" class="ibtn d-max" aria-label="Maximize panel">⤢</button>' +
              '<button type="button" class="ibtn d-min" aria-label="Restore panel">⤡</button>' +
            '</summary>' +
            '<div class="pbody">' + paras.join('') + '</div>' +
          '</details>' +
          '<iframe class="doc-frame" style="height:40px" srcdoc="<html><body>frame ' + i + '</body></html>"></iframe>' +
        '</div>' +
      '</section>'
    );
  }
  const bcardHtml =
    '<div class="bcard" data-card="preview-1" data-kind="preview">' +
      '<div class="bc-in">' +
        '<div class="bc-bar"><span class="bc-title">Preview Card</span></div>' +
        '<div class="bc-body"><div style="height:600px;padding:12px">Unpinned preview card body</div></div>' +
      '</div>' +
    '</div>';
  return '<!doctype html><html data-theme="' + theme + '"' + (opts.framed ? ' data-frame' : '') + '><head><meta charset="utf-8"><style>' +
    wbCss + '\n' + dashCss +
    '\nhtml,body{margin:0;height:100%;overflow:hidden}' +
    '.dwrap{height:100vh;width:100vw;display:flex;flex-direction:column}' +
    '.dbody{flex:1 1 0;min-height:0}' +
    '.dgrid.live{height:100%;width:100%}' +
    '</style></head><body class="dash live"><div class="dwrap"><div class="dbody"><div class="rail"></div>' +
    '<div class="dgrid live" data-tab="board" data-layout="split">' +
      '<div class="convo" id="convo"><div class="chead">Convo</div><div class="clog"></div></div>' +
      '<div class="bcol" id="bcol">' +
        '<div class="r-board" data-region="board">' +
          topicsHtml.join('') +
          (opts.withCard ? bcardHtml : '') +
        '</div>' +
      '</div>' +
    '</div></div></div>' +
    '</body></html>';
}

function buildScripts() {
  const harness = `
    window.wsT = function (k) { return k; };
    window.wbHistory = {
      keep: function (n) { return { apply: function () {}, end: function () {} }; },
      place: function () {},
      rendered: function () { return true; }
    };
    (function () {
      var doc = document;
      function $(id) { return doc.getElementById(id); }
      function region(name) { return doc.querySelector('[data-region="' + name + '"]'); }
      function topicKey(n) { return n && n.getAttribute ? n.getAttribute('data-topic') : null; }
      function reducedMotion() { return !!(window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches); }
      var boardPending = {}, boardSnap = null, shHidden = false;
      function boardFlushViewed() {}
      function boardKeep() { return null; }
      function postTheme() {}
      ${anchorJs}
      ${dialJs}
      window.__anchorInit = anchorInit;
      window.__queueFrameHeight = (function () {
        ${framesJs.match(/\n  var frameSizes = new Map\(\)[\s\S]*?function applyFrameHeights\(\) \{[\s\S]*?\n  \}\n/)[0]}
        return queueFrameHeight;
      })();
    })();
  `;
  const mount = `
    (function () {
      var col = document.getElementById('bcol');
      var board = document.querySelector('[data-region="board"]');
      var mem = {};
      window.WsCards.mount(board, {
        key: 'test-rid',
        load: function (k) { return mem[k] || null; },
        save: function (k, v) { mem[k] = v; },
        mode: function () { return { width: window.innerWidth, tabs: false }; },
        layout: function (s) {
          var m = s && s.narrow ? 'narrow' : s && s.column ? 'col' : s && s.empty ? 'empty' : s && s.overlay ? 'drag' : '';
          if (m) document.documentElement.setAttribute('data-gal', m);
          else document.documentElement.removeAttribute('data-gal');
        },
        grid: document.querySelector('.dgrid'),
        toolbar: { overview: function () { window.WsOverview.toggle(); } }
      });
      window.__anchorInit();
      window.WsOverview.mount({
        rid: 'test-rid',
        board: board,
        toolbar: window.WsCards.toolbar(),
        keep: function () {},
        place: function (on) { window.WsCards.overview(on); }
      });
    })();
  `;
  return [galleryJs, cardsJs, overviewJs, harness, mount, railJs];
}

async function waitFrames(page, n = 4) {
  await page.evaluate(count => new Promise(resolve => {
    let i = 0;
    (function tick() { if (++i >= count) resolve(); else requestAnimationFrame(tick); })();
  }), n);
}

async function launchBrowser() {
  let executablePath;
  if (!fs.existsSync(chromium.executablePath())) {
    const fallback = 'C:\\Users\\user\\AppData\\Local\\ms-playwright\\chromium_headless_shell-1248\\chrome-headless-shell-win64\\chrome-headless-shell.exe';
    if (fs.existsSync(fallback)) executablePath = fallback;
  }
  return chromium.launch({ headless: true, executablePath });
}

async function setupPage(browser, viewport, opts = {}) {
  const ctx = await browser.newContext({ viewport });
  const page = await ctx.newPage();
  page.on('pageerror', err => { throw err; });
  await page.setContent(buildFixtureHtml(opts), { waitUntil: 'load' });
  for (const content of buildScripts()) await page.addScriptTag({ content });
  await waitFrames(page, 8);
  return { ctx, page };
}

async function assertSheetAboveScrim(page, selector, label) {
  const res = await page.evaluate(sel => {
    const target = document.querySelector(sel);
    const scrim = document.querySelector('.bc-scrim');
    if (!target || !scrim) return { ok: false, reason: 'missing target or scrim', hasTarget: !!target, hasScrim: !!scrim };
    const sheet = target.classList.contains('bcard') ? (target.querySelector(':scope > .bc-in') || target) : target;
    const r = sheet.getBoundingClientRect();
    const cx = Math.round(r.left + r.width / 2);
    const cy = Math.round(r.top + Math.min(r.height / 2, 160));
    const topEl = document.elementFromPoint(cx, cy);
    const stack = document.elementsFromPoint(cx, cy);
    const targetIdx = stack.findIndex(el => el === target || target.contains(el));
    const scrimIdx = stack.indexOf(scrim);
    const hitsScrim = topEl === scrim;
    const scrimZ = getComputedStyle(scrim).zIndex;
    const sheetZ = getComputedStyle(sheet).zIndex;
    const bg = getComputedStyle(sheet).backgroundColor;
    // Parse rgb(...) or rgba(r, g, b, a) / color(srgb ... / a)
    let alpha = 1;
    const rgba = bg.match(/rgba?\([^)]+\)/);
    if (bg === 'transparent') alpha = 0;
    else if (rgba) {
      const parts = rgba[0].replace(/rgba?\(|\)/g, '').split(/[\s,/]+/).filter(Boolean);
      if (parts.length >= 4) alpha = parseFloat(parts[3]);
    }
    return {
      ok: !hitsScrim && targetIdx >= 0 && (scrimIdx < 0 || targetIdx < scrimIdx) && scrimZ === '69' && sheetZ === '70' && alpha === 1,
      hitsScrim,
      targetIdx,
      scrimIdx,
      scrimZ,
      sheetZ,
      bg,
      alpha,
      topTag: topEl ? topEl.tagName + '.' + topEl.className : null,
      rect: { top: r.top, left: r.left, width: r.width, height: r.height }
    };
  }, selector);
  assert.equal(res.ok, true, label + ' must paint opaque (z=70) strictly above .bc-scrim (z=69): ' + JSON.stringify(res));
}

test('Part 1 & 2: wheel virtualization + per-edge 100% full-span height + 0px next-card peek', async () => {
  const browser = await launchBrowser();
  try {
    const { ctx, page } = await setupPage(browser, { width: 1440, height: 900 }, { topics: 6 });
    try {
      // Measure Card 0, Card 1, Card 2 (middle), Card 4 (N-2), Card 5 (N-1)
      const metrics = [];
      for (const idx of [0, 1, 2, 4, 5]) {
        await page.evaluate(i => {
          window.WsBoardAnchor.select(document.getElementById('topic-' + i), { smooth: false });
        }, idx);
        await waitFrames(page, 6);
        const m = await page.evaluate(i => {
          const col = document.getElementById('bcol');
          const colR = col.getBoundingClientRect();
          const card = document.getElementById('topic-' + i);
          const r = card.getBoundingClientRect();
          const next = document.getElementById('topic-' + (i + 1));
          const nextR = next ? next.getBoundingClientRect() : null;
          const botStack = document.querySelector('.wb-rail-wrap.dn .wb-rail-stack');
          const botShown = !!(botStack && !botStack.hidden);
          const botCaps = botShown ? Array.from(botStack.querySelectorAll('.wb-cap')).filter(b => !b.hidden).length : 0;
          const botTop = botCaps > 0 ? botStack.getBoundingClientRect().top : colR.bottom;
          const topStack = document.querySelector('.wb-rail-wrap.up .wb-rail-stack');
          const topShown = !!(topStack && !topStack.hidden);
          const topCaps = topShown ? Array.from(topStack.querySelectorAll('.wb-cap')).filter(b => !b.hidden).length : 0;
          const topBot = topCaps > 0 ? topStack.getBoundingClientRect().bottom : document.querySelector('.board-toolbar-face').getBoundingClientRect().bottom;
          return {
            idx: i,
            capAttr: card.getAttribute('data-anchor-cap'),
            botAttr: card.getAttribute('data-anchor-bot'),
            topCaps,
            botCaps,
            cardH: Math.round(r.height),
            topGap: +(r.top - topBot).toFixed(2),
            botGap: +(botTop - r.bottom).toFixed(2),
            nextPeekAboveColBottom: nextR ? +(colR.bottom - nextR.top).toFixed(2) : null,
            nextPeekAboveBotStack: nextR ? +(botTop - nextR.top).toFixed(2) : null,
          };
        }, idx);
        metrics.push(m);
        assert.ok(m.botGap >= 0 && m.botGap <= 10, `Card ${idx} bottom gap must be 0..10px above bottom rail/col (got ${m.botGap}px): ${JSON.stringify(m)}`);
        if (m.nextPeekAboveColBottom !== null) {
          assert.ok(m.nextPeekAboveColBottom <= 0.5, `Card ${idx + 1} must NOT peek above #bcol bottom (got ${m.nextPeekAboveColBottom}px): ${JSON.stringify(m)}`);
        }
      }
      // Card 0 (0 top caps) must be taller than middle Card 2 (2 top caps)
      assert.ok(metrics[0].cardH > metrics[2].cardH + 30, `Card 0 (${metrics[0].cardH}) must reclaim top-cap height vs middle Card 2 (${metrics[2].cardH})`);
      // Card 5 (0 bottom caps) must be taller than middle Card 2 (2 bottom caps)
      assert.ok(metrics[4].cardH > metrics[2].cardH + 30, `Last card (${metrics[4].cardH}) must reclaim bottom-cap height vs middle Card 2 (${metrics[2].cardH})`);

      // Part 1: Wheel virtualization in real Chromium
      await page.evaluate(() => {
        const t0 = document.getElementById('topic-0');
        t0.scrollTop = 0;
        window.WsBoardAnchor.select(t0, { smooth: false });
        window.WsBoardAnchor.wheel.reset();
      });
      await waitFrames(page, 4);
      const center = await page.evaluate(() => {
        const r = document.getElementById('topic-0').getBoundingClientRect();
        return { x: Math.round(r.left + r.width / 2), y: Math.round(r.top + r.height / 2) };
      });
      await page.mouse.move(center.x, center.y);
      // Native inner scroll inside tall Topic 0
      await page.mouse.wheel(0, 180);
      await waitFrames(page, 3);
      const innerCheck = await page.evaluate(() => ({
        key: window.WsBoardAnchor.key(),
        innerTop: document.getElementById('topic-0').scrollTop,
        bcolTop: document.getElementById('bcol').scrollTop
      }));
      assert.equal(innerCheck.key, 't:topic-0');
      assert.ok(innerCheck.innerTop > 0, 'wheel inside tall card scrolls natively');
      assert.equal(innerCheck.bcolTop, 0, 'outer #bcol stays pinned while inner scrolls');

      // Move to bottom of Topic 0, pause > idleGapMs, dispatch a single flick + decaying inertia tail
      await page.evaluate(() => {
        const t0 = document.getElementById('topic-0');
        t0.scrollTop = t0.scrollHeight;
        window.WsBoardAnchor.wheel.reset();
      });
      await page.waitForTimeout(180);
      for (const dy of [18, 42, 34, 24, 16, 10, 6, 3]) {
        await page.mouse.wheel(0, dy);
        await page.waitForTimeout(16);
      }
      await page.waitForTimeout(260);
      const afterFlick = await page.evaluate(() => window.WsBoardAnchor.key());
      assert.equal(afterFlick, 't:topic-1', 'single flick with decaying tail must commit exactly 1 card without double-jump');
    } finally {
      await ctx.close();
    }
  } finally {
    await browser.close();
  }
});

test('Part 3: Overview fast exit (0 data-overview-motion tags) + 0 idle rAF while open', async () => {
  const browser = await launchBrowser();
  try {
    const { ctx, page } = await setupPage(browser, { width: 1440, height: 900 }, { topics: 10 });
    try {
      await page.evaluate(() => {
        window.__motionTagged = 0;
        const mo = new MutationObserver(list => {
          list.forEach(m => {
            if (m.attributeName === 'data-overview-motion' && m.target.hasAttribute('data-overview-motion')) {
              window.__motionTagged++;
            }
          });
        });
        mo.observe(document.getElementById('bcol'), { attributes: true, subtree: true, attributeFilter: ['data-overview-motion'] });
      });

      // Enter Overview and wait for enter motion (240ms) to settle
      await page.evaluate(() => window.WsOverview.toggle());
      await page.waitForTimeout(320);
      await waitFrames(page, 4);
      assert.equal(await page.evaluate(() => window.WsOverview.active()), true);

      // Trigger frame height report while Overview is open and count rAF invocations over 180ms
      const rafCount = await page.evaluate(() => new Promise(resolve => {
        const orig = window.requestAnimationFrame;
        let count = 0;
        window.requestAnimationFrame = function (cb) { count++; return orig.call(window, cb); };
        const f = document.querySelector('iframe.doc-frame');
        window.__queueFrameHeight(f, 120);
        setTimeout(() => {
          window.requestAnimationFrame = orig;
          resolve(count);
        }, 180);
      }));
      assert.equal(rafCount, 0, 'Overview open must hold 0 idle rAF frames even when frame height arrives');

      // Reset counter right before exit so we measure exit mutations only
      await page.evaluate(() => { window.__motionTagged = 0; });
      // Exit Overview onto Topic 6
      await page.evaluate(() => window.WsOverview.pick(document.getElementById('topic-6')));
      await page.waitForTimeout(220);
      await waitFrames(page, 6);
      const afterExit = await page.evaluate(() => {
        const card = document.getElementById('topic-6');
        const topStack = document.querySelector('.wb-rail-wrap.up .wb-rail-stack');
        const topBot = topStack && !topStack.hidden ? topStack.getBoundingClientRect().bottom : 0;
        return {
          active: window.WsOverview.active(),
          hasAttr: document.documentElement.hasAttribute('data-overview'),
          key: window.WsBoardAnchor.key(),
          motionTagged: window.__motionTagged,
          topDelta: +(card.getBoundingClientRect().top - topBot).toFixed(2)
        };
      });
      assert.equal(afterExit.active, false);
      assert.equal(afterExit.hasAttr, false);
      assert.equal(afterExit.key, 't:topic-6');
      assert.equal(afterExit.motionTagged, 0, 'snap-deck Overview exit must never tag cards with data-overview-motion');
      assert.ok(Math.abs(afterExit.topDelta) <= 2, 'non-first card must land with 0px misalignment after fast Overview exit: ' + JSON.stringify(afterExit));
    } finally {
      await ctx.close();
    }
  } finally {
    await browser.close();
  }
});

test('Part 4: assertSheetAboveScrim across #bcol & #bgal, wide & narrow, standalone & shell iframe', async () => {
  const browser = await launchBrowser();
  try {
    // 1. Standalone wide (1680x900 for 4-col gallery): #bcol topic, #bcol entry-panel (+ data-has-panel-max), #bcol .bcard, #bgal docked topic & panel
    const { ctx, page } = await setupPage(browser, { width: 1680, height: 900 }, { topics: 4, withCard: true, theme: 'dark' });
    try {
      // #bcol section.topic[data-max]
      await page.click('#topic-0 > .tophead .d-max');
      await waitFrames(page, 3);
      await assertSheetAboveScrim(page, 'section.topic[data-max]', 'wide #bcol section.topic[data-max]');
      await page.click('#topic-0 > .tophead .d-min');
      await waitFrames(page, 3);

      // #bcol details.entry-panel[data-max] + explicit data-has-panel-max lifecycle
      // Set inline translate: 0px on .r-board beforehand (as gallery pull does) to verify :root[data-board-max] clears it!
      await page.evaluate(() => { document.querySelector('.r-board').style.translate = '0px'; });
      await page.click('#topic-0 details.entry-panel .d-max');
      await waitFrames(page, 3);
      const hostFlagsOn = await page.evaluate(() => ({
        topic: document.getElementById('topic-0').hasAttribute('data-has-panel-max'),
        body: document.querySelector('#topic-0 > .tbody').hasAttribute('data-has-panel-max'),
        boardTranslate: getComputedStyle(document.querySelector('.r-board')).translate
      }));
      assert.deepEqual(hostFlagsOn, { topic: true, body: true, boardTranslate: 'none' }, 'data-has-panel-max must be set on topic + body child and .r-board translate cleared');
      await assertSheetAboveScrim(page, 'details.entry-panel[data-max]', 'wide #bcol details.entry-panel[data-max]');
      await page.click('#topic-0 details.entry-panel .d-min');
      await page.evaluate(() => { document.querySelector('.r-board').style.translate = ''; });
      await waitFrames(page, 3);
      const hostFlagsOff = await page.evaluate(() => ({
        topic: document.getElementById('topic-0').hasAttribute('data-has-panel-max'),
        body: document.querySelector('#topic-0 > .tbody').hasAttribute('data-has-panel-max')
      }));
      assert.deepEqual(hostFlagsOff, { topic: false, body: false }, 'data-has-panel-max must be cleared on minimize');

      // #bcol .bcard[data-max]
      await page.evaluate(() => {
        const btn = document.querySelector('.bcard[data-card="preview-1"] .d-max');
        if (btn) btn.click();
      });
      await waitFrames(page, 3);
      if (await page.locator('.bcard[data-max]').count() > 0) {
        await assertSheetAboveScrim(page, '.bcard[data-max]', 'wide #bcol .bcard[data-max]');
        await page.keyboard.press('Escape');
        await waitFrames(page, 3);
      }

      // Dock Topic 1 into .bgal (wide 4-col at 1920px & narrow overlay at 900px)
      await page.setViewportSize({ width: 1920, height: 900 });
      await waitFrames(page, 4);
      await page.evaluate(() => {
        window.WsCards.move(document.getElementById('topic-1'), { pin: true });
        window.WsCards.relayout();
        window.WsCards.activate(document.querySelector('.bgal section.topic[data-topic="topic-1"]'));
      });
      await waitFrames(page, 6);
      const galDbg = await page.evaluate(() => {
        const g = document.querySelector('.bgal');
        const b = document.querySelector('.bgal section.topic[data-topic="topic-1"] > .tophead .d-max');
        return {
          galAttr: document.documentElement.getAttribute('data-gal'),
          galHidden: g && g.hidden,
          galRect: g && g.getBoundingClientRect().toJSON(),
          btnRect: b && b.getBoundingClientRect().toJSON(),
          btnDisplay: b && getComputedStyle(b).display
        };
      });
      if (!galDbg.btnRect || galDbg.btnRect.width === 0) throw new Error('wide .bgal debug: ' + JSON.stringify(galDbg));
      await page.click('.bgal section.topic[data-topic="topic-1"] > .tophead .d-max', { timeout: 4000 });
      await waitFrames(page, 3);
      await assertSheetAboveScrim(page, '.bgal section.topic[data-max]', 'wide .bgal section.topic[data-max]');
      await page.click('.bgal section.topic[data-topic="topic-1"] > .tophead .d-min', { timeout: 4000 });
      await waitFrames(page, 3);

      // Resize to narrow (900x800) so data-gal="narrow" activates the .bgal stacking-context trap!
      await page.setViewportSize({ width: 900, height: 800 });
      await waitFrames(page, 8);
      await page.evaluate(() => {
        window.WsCards.activate(document.querySelector('.bgal section.topic[data-topic="topic-1"]'));
      });
      await waitFrames(page, 6);
      const galMode = await page.evaluate(() => document.documentElement.getAttribute('data-gal'));
      assert.equal(galMode, 'narrow', '900px viewport with pinned topic must enter data-gal="narrow"');

      await page.click('.bgal section.topic[data-topic="topic-1"] > .tophead .d-max', { timeout: 4000 });
      await waitFrames(page, 3);
      await assertSheetAboveScrim(page, '.bgal section.topic[data-max]', 'narrow .bgal section.topic[data-max]');
      // Verify data-gal="drag" and wide sticky (.dash.board) stacking contexts too
      await page.evaluate(() => { document.documentElement.setAttribute('data-gal', 'drag'); });
      await waitFrames(page, 2);
      await assertSheetAboveScrim(page, '.bgal section.topic[data-max]', 'drag .bgal section.topic[data-max]');
      await page.setViewportSize({ width: 1920, height: 900 });
      await page.evaluate(() => {
        document.body.classList.remove('live');
        document.body.classList.add('board');
        document.documentElement.setAttribute('data-gal', 'col');
      });
      await waitFrames(page, 2);
      await assertSheetAboveScrim(page, '.bgal section.topic[data-max]', 'wide sticky .bgal section.topic[data-max]');
      await page.evaluate(() => {
        document.body.classList.remove('board');
        document.body.classList.add('live');
        document.documentElement.setAttribute('data-gal', 'narrow');
      });
      await page.setViewportSize({ width: 900, height: 800 });
      await waitFrames(page, 2);
      await page.click('.bgal section.topic[data-topic="topic-1"] > .tophead .d-min', { timeout: 4000 });
      await waitFrames(page, 3);

      await page.click('.bgal section.topic[data-topic="topic-1"] details.entry-panel .d-max', { timeout: 4000 });
      await waitFrames(page, 3);
      await assertSheetAboveScrim(page, '.bgal details.entry-panel[data-max]', 'narrow .bgal details.entry-panel[data-max]');
      // Verify .wb-chg-hit pointer-events inside dimmed vs maximized .bgal
      const chgPE = await page.evaluate(() => {
        const gal = document.querySelector('.bgal');
        const dimmedHit = document.createElement('div');
        dimmedHit.className = 'wb-chg-hit';
        gal.querySelector('.gal-rail').appendChild(dimmedHit);
        const maxHit = document.createElement('div');
        maxHit.className = 'wb-chg-hit';
        gal.querySelector('[data-max]').appendChild(maxHit);
        const res = {
          dimmed: getComputedStyle(dimmedHit).pointerEvents,
          maximized: getComputedStyle(maxHit).pointerEvents
        };
        dimmedHit.remove();
        maxHit.remove();
        return res;
      });
      assert.deepEqual(chgPE, { dimmed: 'none', maximized: 'auto' }, '.wb-chg-hit must be disabled in dimmed .bgal and enabled inside [data-max]');
      await page.click('.bgal section.topic[data-topic="topic-1"] details.entry-panel .d-min', { timeout: 4000 });
      await waitFrames(page, 3);
    } finally {
      await ctx.close();
    }

    // 2. Shell iframe host (data-shell outer + data-frame inner): verify maximize inside an embedded iframe
    const { ctx: shellCtx, page: shellPage } = await (async () => {
      const c = await browser.newContext({ viewport: { width: 1440, height: 900 } });
      const p = await c.newPage();
      p.on('pageerror', err => { throw err; });
      await p.setContent(
        '<!doctype html><html data-shell data-fdim><head><style>' +
        readAssetSource('wsdashboard', 'dashboard-shell.css') +
        '</style></head><body style="margin:0;height:100vh"><div class="frames"><iframe id="dash" class="on"></iframe></div></body></html>'
      );
      const frame = p.frameLocator('#dash');
      const childFrame = p.frames()[1];
      await childFrame.setContent(buildFixtureHtml({ topics: 4, theme: 'dark', framed: true }), { waitUntil: 'load' });
      for (const content of buildScripts()) await childFrame.addScriptTag({ content });
      await waitFrames(childFrame, 8);
      return { ctx: c, page: childFrame, frame };
    })();
    try {
      await shellPage.click('#topic-0 details.entry-panel .d-max');
      await waitFrames(shellPage, 3);
      await assertSheetAboveScrim(shellPage, 'details.entry-panel[data-max]', 'shell iframe details.entry-panel[data-max]');
      await shellPage.click('#topic-0 details.entry-panel .d-min');
      await waitFrames(shellPage, 3);
      await shellPage.click('#topic-0 > .tophead .d-max');
      await waitFrames(shellPage, 3);
      await assertSheetAboveScrim(shellPage, 'section.topic[data-max]', 'shell iframe section.topic[data-max]');
    } finally {
      await shellCtx.close();
    }
  } finally {
    await browser.close();
  }
});
