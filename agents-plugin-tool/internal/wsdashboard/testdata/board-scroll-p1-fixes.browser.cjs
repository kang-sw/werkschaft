#!/usr/bin/env node
'use strict';

const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const assert = require('node:assert/strict');
const { test } = require('node:test');

const DEFAULT_PW = 'C:\\Users\\user\\AppData\\Local\\npm-cache\\_npx\\e41f203b7505f1fb\\node_modules\\playwright';
const pwPath = process.env.WS_PLAYWRIGHT_MODULE || (fs.existsSync(DEFAULT_PW) ? DEFAULT_PW : 'playwright');
const { chromium } = require(pwPath);

const REPO = path.resolve(__dirname, '../../../..');
const WSD = path.join(REPO, 'agents-plugin-tool/internal/wsdashboard/assets');
const WSW = path.join(REPO, 'agents-plugin-tool/internal/wswhiteboard/assets');
const SHOTS_DIR = process.env.WS_LOOK_SHOTS || '';

function readAsset(dir, name) {
  return fs.readFileSync(path.join(dir, name), 'utf8');
}

function buildFixtureHtml(opts = {}) {
  const css = [
    readAsset(WSW, 'page.css'),
    readAsset(WSW, 'page-items.css'),
    readAsset(WSW, 'page-panels.css'),
    readAsset(WSW, 'page-stream.css'),
    readAsset(WSW, 'page-sticky.css'),
    readAsset(WSD, 'dashboard.css'),
    readAsset(WSD, 'dashboard-layout.css'),
    readAsset(WSD, 'dashboard-board.css'),
  ].join('\n');

  const sprite = readAsset(WSW, 'sprite.svg');
  const lucide = '';
  const galleryJs = readAsset(WSD, 'gallery.js');
  const cardsJs = readAsset(WSD, 'cards.js');
  const pillJs = readAsset(WSW, 'pill.js');
  const railJs = readAsset(WSW, 'rail.js');
  const anchorJs = readAsset(WSD, 'dashboard-board-anchor.js');

  const makeLines = (prefix, count, lastText) => {
    const out = [];
    for (let i = 1; i <= count; i++) {
      out.push(`<div class="log-line" data-line="${i}">${prefix} Line ${String(i).padStart(3, '0')}: streamed output &amp; status record</div>`);
    }
    if (lastText) out.push(`<div class="log-line last-line" data-last-line>${lastText}</div>`);
    return out.join('');
  };

  const iframeDoc = `<!doctype html><html><head><style>
    html, body { margin: 0; padding: 0; font: 13px/1.5 system-ui, sans-serif; color: #1b2230; background: transparent; }
    .doc-body { padding: 8px 14px; }
    .log-line { padding: 4px 0; border-bottom: 1px solid rgba(120,130,150,.15); }
    .last-line { font-weight: 700; color: #1f5fd6; padding-bottom: 10px; }
  </style></head><body><div class="doc-body">${makeLines('DocFrame', 42, 'IFRAME END LINE 042 — FULLY VISIBLE ABOVE BOTTOM RAIL')}</div><script>document.addEventListener('wheel',function(e){if(!e.defaultPrevented&&!e.ctrlKey)window.parent.postMessage({wbDoc:'wheel',dy:e.deltaY,dx:e.deltaX,mode:e.deltaMode},'*');},{passive:true});</script></body></html>`;
  const escapedIframeDoc = iframeDoc.replace(/&/g, '&amp;').replace(/"/g, '&quot;');

  const topicHtml = (idx, title, opts = {}) => {
    const cls = opts.fold ? 'topic" data-size="fold' : 'topic';
    const hasAsk = opts.ask
      ? `<div class="card policy" id="item-${idx}" data-wb-hold="q-${idx}" data-wb-q="q-${idx}">
           <div class="phead">Open Question #${idx}: Choose scroll alignment</div>
           <div class="opts">
             <label class="opt"><input type="radio" name="q-${idx}" value="a"> Option A</label>
             <label class="opt"><input type="radio" name="q-${idx}" value="b"> Option B</label>
           </div>
         </div>`
      : '';
    const body = opts.iframe
      ? `<div class="tdoc"><iframe class="doc-frame" title="doc-${idx}" scrolling="no" style="width:100%;border:0;height:1480px" srcdoc="${escapedIframeDoc}"></iframe></div>
         <div class="tlog">${makeLines(`Host-${idx}`, 8, `HOST LAST LINE — TOPIC ${idx} MUST NEVER CLIP`)}</div>`
      : opts.tall
        ? `<div class="tlog">${makeLines(`Topic-${idx}`, 48, `END LINE 048 — TOPIC ${idx} LAST LINE UNCLIPPED`)}</div>`
        : `<div class="tlog"><div class="log-line">Short summary for ${title}</div></div>`;
    return `<section class="${cls}" id="topic-${idx}" data-topic="topic-${idx}" data-th="rev-${idx}">
      <div class="tophead">
        <div class="tbar">
          <span class="tdot"></span>
          <span class="tname">${title}</span>
          ${opts.ask ? '<span class="chip policy">1</span>' : ''}
        </div>
        ${opts.summary ? `<div class="tsum">${opts.summary}</div>` : ''}
      </div>
      ${hasAsk}
      <div class="tbody">${body}</div>
    </section>`;
  };

  return `<!doctype html>
<html data-mode="live" data-theme="light">
<head>
<meta charset="utf-8">
<style>
${css}
html, body { height: 100%; margin: 0; overflow: hidden; background: var(--wb-bg); color: var(--wb-fg); font-family: var(--wb-sans, system-ui, sans-serif); }
.dwrap { height: 100vh; display: flex; flex-direction: column; }
.dbody { flex: 1 1 0; min-height: 0; display: flex; }
.dgrid { flex: 1 1 0; min-height: 0; width: 100%; display: grid; grid-template-columns: minmax(0, 1fr); }
#bcol { height: 100%; min-height: 0; overflow-y: auto; padding: 2px 12px 0 12px; }
section.topic { background: var(--wb-card); border-radius: var(--wb-radius); box-shadow: var(--wb-shadow); margin-bottom: 16px; }
section.topic > .tophead { border-bottom: 1px solid var(--wb-muted); }
section.topic > .tophead > .tbar { display: flex; align-items: center; gap: 8px; padding: 8px 12px; min-height: 36px; box-sizing: border-box; font-weight: 700; }
section.topic > .tbody { padding: 6px 14px 10px; }
.log-line { padding: 4px 0; border-bottom: 1px solid color-mix(in srgb, var(--wb-line) 55%, transparent); font-size: 13px; line-height: 1.45; }
.last-line { font-weight: 700; color: var(--wb-primary); }
</style>
</head>
<body class="dash live">
<div hidden>${sprite}${lucide}</div>
<div class="dwrap">
  <div class="dbody">
    <main id="grid" class="dgrid live">
      <div id="bcol" class="bcol">
        <div id="r-board" class="r-board" data-region="board">
          ${typeof opts.buildBoard === 'function' ? opts.buildBoard({ topicHtml, makeLines }) : [
            topicHtml(1, 'Topic 1: Initial Open Question', { ask: true, tall: true, summary: 'Has unanswered policy item for top ↑ pill' }),
            topicHtml(2, 'Topic 2: Short Folded Card', { fold: true }),
            topicHtml(3, 'Topic 3: Post-Fold Target Card', { tall: true, summary: 'Should never snap back to Topic 2 on cap click' }),
            topicHtml(4, 'Topic 4: Iframe Doc + Tall Body', { iframe: true, summary: '2-row top rail + iframe wheel chaining + bottom rail clearance' }),
            topicHtml(5, 'Topic 5: Follow-up Implementation', { tall: true }),
            topicHtml(6, 'Topic 6: Non-current Mutation Target', { tall: true }),
            topicHtml(7, 'Topic 7: Trailing Board Card', { tall: true }),
          ].join('\n          ')}
        </div>
      </div>
    </main>
  </div>
</div>
<script>
window.__TERM_BODY_HTML = ${JSON.stringify(`<div style="padding:10px 14px;">${makeLines('Term', 30, 'TERMINAL END LINE — UNCLIPPED')}</div>`)};
window.wsT = function (k) {
  const map = {
    'js.board.toolbar.overview': 'Overview',
    'js.board.toolbar.overview.title': 'Overview',
    'js.board.toolbar.files': 'Files',
    'js.board.toolbar.download': 'Download',
    'js.board.toolbar.download.title': 'Download',
    'js.pill.top': '↑ Open questions',
    'js.pill.bot': '↓ Open questions'
  };
  return map[k] || k;
};
window.wbToast = function () {};
</script>
</body>
</html>`;
}

function buildBootstrapScript() {
  const galleryJs = readAsset(WSD, 'gallery.js');
  const cardsJs = readAsset(WSD, 'cards.js');
  const pillJs = readAsset(WSW, 'pill.js');
  const railJs = readAsset(WSW, 'rail.js');
  const anchorJs = readAsset(WSD, 'dashboard-board-anchor.js');
  return [
    galleryJs,
    cardsJs,
    pillJs,
    `(function () {
      var doc = document;
      function $(id) { return doc.getElementById(id); }
      function q(s, box) { return Array.prototype.slice.call((box || doc).querySelectorAll(s)); }
      function region(name) { return doc.querySelector('[data-region="' + name + '"]'); }
      function topicKey(n) { return n && n.getAttribute ? n.getAttribute("data-topic") : null; }
      function reducedMotion() { return false; }
      var boardPending = {};
      var boardSnap = null;
      function boardFlushViewed() {}
      function wsT(k) { return window.wsT(k); }
      ${anchorJs}
      if (window.WsCards && window.WsCards.mount) {
        window.WsCards.register("testcard", {
          base: 420,
          open: function (c) {
            c.name.textContent = "Terminal 1";
            c.badge.textContent = "TERM";
            c.body.innerHTML = window.__TERM_BODY_HTML || "";
          }
        });
        var mem = {
          "board-scroll-fixture": JSON.stringify({
            topics: [{ n: "topic-2", f: true }],
            cards: [{ id: "term-1", kind: "testcard", at: 1e15, data: {} }]
          })
        };
        window.WsCards.mount(region("board"), {
          key: "board-scroll-fixture",
          load: function (k) { return mem[k] || null; },
          save: function (k, v) { mem[k] = v; },
          mode: function () { return { width: window.innerWidth, tabs: false }; },
          grid: doc.getElementById("grid"),
          toolbar: { overview: function () {} }
        });
      }
      anchorInit();
      window.addEventListener("message", function (e) {
        if (!e.data || e.data.wbDoc !== "wheel") return;
        var frames = doc.querySelectorAll("iframe.doc-frame");
        for (var i = 0; i < frames.length; i++) {
          if (frames[i].contentWindow === e.source && window.WsCards && window.WsCards.onFrameWheel) {
            window.WsCards.onFrameWheel(frames[i], e.data.dy, e.data.dx, e.data.mode);
            break;
          }
        }
      });
    })();`,
    railJs,
  ];
}

async function waitFrames(page, n = 4) {
  await page.evaluate(count => new Promise(resolve => {
    let i = 0;
    function tick() { if (++i >= count) resolve(); else requestAnimationFrame(tick); }
    requestAnimationFrame(tick);
  }), n);
}

async function waitForScrollSettle(page, timeoutMs = 1500) {
  await page.evaluate(maxMs => new Promise(resolve => {
    const col = document.getElementById('bcol');
    let timer = null, maxTimer = null, settled = false;
    function finish() {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      clearTimeout(maxTimer);
      col.removeEventListener('scroll', bump);
      col.removeEventListener('scrollend', onEnd);
      requestAnimationFrame(() => requestAnimationFrame(resolve));
    }
    function bump() {
      clearTimeout(timer);
      timer = setTimeout(finish, 180);
    }
    function onEnd() {
      clearTimeout(timer);
      timer = setTimeout(finish, 80);
    }
    col.addEventListener('scroll', bump, { passive: true });
    col.addEventListener('scrollend', onEnd, { passive: true });
    timer = setTimeout(finish, 260);
    maxTimer = setTimeout(finish, maxMs);
  }), timeoutMs);
  await waitFrames(page, 4);
}

test('Chromium integration: all 4 board-scroll Phase 1 fixes + LOOK captures + zero non-current geometry reads', async () => {
  if (SHOTS_DIR) fs.mkdirSync(SHOTS_DIR, { recursive: true });
  let executablePath;
  if (!fs.existsSync(chromium.executablePath())) {
    const fallback = 'C:\\Users\\user\\AppData\\Local\\ms-playwright\\chromium_headless_shell-1248\\chrome-headless-shell-win64\\chrome-headless-shell.exe';
    if (fs.existsSync(fallback)) executablePath = fallback;
  }
  const browser = await chromium.launch({ headless: true, executablePath });
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
    const page = await context.newPage();
    page.on('pageerror', err => { throw err; });
    await page.setContent(buildFixtureHtml(), { waitUntil: 'load' });
    for (const content of buildBootstrapScript()) {
      await page.addScriptTag({ content });
    }
    await waitFrames(page, 6);

    // Bug 2: Zero .board-step buttons mounted in toolbar
    const stepBtnCount = await page.locator('.board-step, .board-step-prev, .board-step-next').count();
    assert.equal(stepBtnCount, 0, 'redundant .board-step chevron buttons must be removed');

    // The topic takes its natural height and wheel scrolling flows to #bcol.
    const topicCss = await page.locator('section.topic[data-topic="topic-4"]').evaluate(el => {
      const cs = getComputedStyle(el);
      return { overscrollY: cs.overscrollBehaviorY, scrollMarginTop: cs.scrollMarginTop, innerRange: el.scrollHeight - el.clientHeight };
    });
    assert.equal(topicCss.overscrollY, 'auto', 'free-canvas topics do not trap overscroll');
    assert.equal(topicCss.scrollMarginTop, '0px', 'free-canvas cards use no CSS scroll-margin-top');
    assert.equal(topicCss.innerRange, 0, 'a tall topic uses its natural height');

    // Bug 3 + Bug 4 toolbar-float transition: select Topic 4 from scrollTop=0
    await page.evaluate(() => {
      window.WsBoardAnchor.select(document.getElementById('topic-4'), { smooth: false });
    });
    await page.waitForTimeout(180);
    await waitFrames(page, 6);

    const t4TopMetrics = await page.evaluate(() => {
      const bcol = document.getElementById('bcol');
      const t4 = document.getElementById('topic-4');
      const head = t4.querySelector(':scope > .tophead');
      const topStack = document.querySelector('.wb-rail-wrap.up .wb-rail-stack');
      const caps = Array.from(document.querySelectorAll('.wb-rail-wrap.up .wb-cap')).filter(b => !b.hidden).map(b => b.textContent.trim());
      const oqPill = document.querySelector('.oq-wrap.wb-oq-top .oq-pill');
      const toolbar = document.querySelector('.board-toolbar');
      const face = document.querySelector('.board-toolbar-face');
      const oqRect = oqPill ? oqPill.getBoundingClientRect() : null;
      return {
        key: window.WsBoardAnchor.key(),
        currentAttr: t4.hasAttribute('data-current'),
        floating: toolbar && toolbar.hasAttribute('data-floating'),
        faceH: face ? face.getBoundingClientRect().height : null,
        wbRailInset: bcol.style.getPropertyValue('--wb-rail-inset'),
        padTop: getComputedStyle(bcol).paddingTop,
        scrollTop: bcol.scrollTop,
        scrollHeight: bcol.scrollHeight,
        clientHeight: bcol.clientHeight,
        topCaps: caps,
        topStackBottom: topStack.getBoundingClientRect().bottom,
        topWrapTop: document.querySelector('.wb-rail-wrap.up').getBoundingClientRect().top,
        cardTop: t4.getBoundingClientRect().top,
        headBottom: head.getBoundingClientRect().bottom,
        bcolTop: bcol.getBoundingClientRect().top,
        oqVisible: !!(oqPill && !oqPill.hidden && oqRect.height > 0),
        oqTop: oqRect ? oqRect.top : null,
        railH: document.querySelector('.oq-wrap.wb-oq-top').style.getPropertyValue('--wb-rail-h'),
      };
    });
    console.log('t4TopMetrics:', JSON.stringify(t4TopMetrics, null, 2));

    assert.equal(t4TopMetrics.key, 't:topic-4');
    assert.equal(t4TopMetrics.currentAttr, true);
    assert.equal(t4TopMetrics.floating, false, 'toolbar stays non-floating in bounded free-canvas mode');
    assert.equal(t4TopMetrics.topCaps.length, 2, '2-row top rail bars visible');
    const topGap = t4TopMetrics.cardTop - t4TopMetrics.topStackBottom;
    assert.ok(topGap >= -1 && topGap <= 3, `expected <=3px gap below 2-row top rail, got ${topGap.toFixed(2)}px`);
    assert.equal(t4TopMetrics.oqVisible, true, 'top ↑ pill should be visible for unanswered Topic 1 ask');
    assert.ok(t4TopMetrics.oqTop >= t4TopMetrics.headBottom - 0.5, `↑ pill top (${t4TopMetrics.oqTop}) must be below card header bottom (${t4TopMetrics.headBottom})`);

    if (SHOTS_DIR) await page.screenshot({ path: path.join(SHOTS_DIR, '01-top-rail-and-oq-pill.png') });

    // A real wheel gesture over the doc frame scrolls the outer board.
    const hitPoint = await page.evaluate(() => {
      const col = document.getElementById('bcol');
      const frame = document.querySelector('#topic-4 iframe.doc-frame').getBoundingClientRect();
      return { x: Math.round(frame.left + frame.width / 2), y: Math.round(frame.top + 80), top: col.scrollTop };
    });
    await page.mouse.move(hitPoint.x, hitPoint.y);
    await page.mouse.wheel(0, 260);
    await page.waitForFunction(top => document.getElementById('bcol').scrollTop > top + 100, hitPoint.top);
    const downTop = await page.locator('#bcol').evaluate(el => el.scrollTop);
    await page.mouse.wheel(0, -120);
    await page.waitForFunction(top => document.getElementById('bcol').scrollTop < top, downTop);

    // Bring the final text into view in the outer scroller. The full-height
    // card extends past the viewport, while its text can clear the bottom rail.
    const lastLine = page.locator('#topic-4 .tlog .last-line');
    await lastLine.evaluate(el => el.scrollIntoView({ block: 'center' }));
    await waitFrames(page, 4);
    const t4Bottom = await lastLine.evaluate(el => {
      const col = document.getElementById('bcol').getBoundingClientRect();
      const rail = document.querySelector('.wb-rail-wrap.dn .wb-rail-stack').getBoundingClientRect();
      const line = el.getBoundingClientRect();
      return { lineTop: line.top, lineBottom: line.bottom, colTop: col.top, railTop: rail.top };
    });
    assert.ok(t4Bottom.lineTop >= t4Bottom.colTop && t4Bottom.lineBottom <= t4Bottom.railTop + 0.5,
      'the last line can scroll fully above the bottom rail: ' + JSON.stringify(t4Bottom));

    if (SHOTS_DIR) await page.screenshot({ path: path.join(SHOTS_DIR, '02-tall-card-bottom-clearance.png') });

    // Bug 4 (part B): From Topic 1 (with full-span cards, Topic 2 & Topic 3 sit in bottom rail), click bottom .wb-cap for Topic 3 (after folded Topic 2)
    await page.evaluate(() => {
      window.WsBoardAnchor.select(document.getElementById('topic-1'), { smooth: false });
    });
    await page.waitForTimeout(120);
    await waitFrames(page, 4);

    // Click bottom or top cap matching Topic 3 (scrolls down if in bottom rail; verify which capsule holds Topic 3)
    const capState = await page.evaluate(() => {
      const caps = Array.from(document.querySelectorAll('.wb-cap')).map(b => ({
        hidden: b.hidden,
        stackHidden: b.parentElement.hidden,
        id: b.wbTopic && b.wbTopic.id,
        text: b.textContent.trim()
      }));
      const cap = Array.from(document.querySelectorAll('.wb-cap')).find(b => !b.hidden && b.wbTopic && b.wbTopic.id === 'topic-3');
      if (cap) cap.click();
      return { clicked: !!cap, caps };
    });
    assert.equal(capState.clicked, true, 'Topic 3 must appear in rail capsules from scrollTop=0: ' + JSON.stringify(capState.caps));
    await waitForScrollSettle(page);

    const t3Landed = await page.evaluate(() => {
      const t3 = document.getElementById('topic-3');
      const topStack = document.querySelector('.wb-rail-wrap.up .wb-rail-stack');
      return {
        key: window.WsBoardAnchor.key(),
        currentAttr: t3.hasAttribute('data-current'),
        cardTop: t3.getBoundingClientRect().top,
        topStackBottom: topStack.getBoundingClientRect().bottom,
      };
    });
    assert.equal(t3Landed.key, 't:topic-3', 'clicking .wb-cap after folded Topic 2 must land on t:topic-3 without snapping back');
    assert.equal(t3Landed.currentAttr, true);
    const t3Gap = t3Landed.cardTop - t3Landed.topStackBottom;
    assert.ok(t3Gap >= -1 && t3Gap <= 3, `Topic 3 .wb-cap jump must compensate toolbar float transition (gap=${t3Gap.toFixed(2)}px)`);

    if (SHOTS_DIR) await page.screenshot({ path: path.join(SHOTS_DIR, '03-after-bottom-cap-click.png') });

    // Now test clicking top .wb-cap for Topic 1
    const clickedTopic1 = await page.evaluate(() => {
      const cap = Array.from(document.querySelectorAll('.wb-rail-wrap.up .wb-cap')).find(b => !b.hidden && b.wbTopic && b.wbTopic.id === 'topic-1');
      if (!cap) return false;
      cap.click();
      return true;
    });
    assert.equal(clickedTopic1, true, 'Topic 1 must appear in top rail capsules when on Topic 3');
    await waitForScrollSettle(page);
    const t1Landed = await page.evaluate(() => ({
      key: window.WsBoardAnchor.key(),
      bcolTop: document.getElementById('bcol').scrollTop,
      cardTop: document.getElementById('topic-1').getBoundingClientRect().top,
    }));
    assert.equal(t1Landed.key, 't:topic-1', 'top .wb-cap click must jump cleanly to t:topic-1');

    // Also test .bcard cap click (re-resolving and aligning Terminal 1)
    await page.evaluate(() => {
      const cards = window.WsBoardAnchor.cards();
      const term = document.querySelector('.bcard[data-card="term-1"]');
      const idx = cards.indexOf(term);
      const standOn = cards[idx >= 2 ? idx - 2 : Math.min(cards.length - 1, idx + 2)];
      window.WsBoardAnchor.select(standOn, { smooth: false });
    });
    await page.waitForTimeout(200);
    await waitFrames(page, 4);
    const clickedTermCap = await page.evaluate(() => {
      const cap = Array.from(document.querySelectorAll('.wb-cap')).find(b => !b.hidden && b.wbTopic && b.wbTopic.getAttribute('data-card') === 'term-1');
      if (!cap) return false;
      cap.click();
      return true;
    });
    assert.equal(clickedTermCap, true, 'Terminal 1 .bcard must appear in .wb-cap when an adjacent neighbor is active');
    await waitForScrollSettle(page);
    const termLanded = await page.evaluate(() => {
      const term = document.querySelector('.bcard[data-card="term-1"]');
      const bcol = document.getElementById('bcol');
      const upStack = document.querySelector('.wb-rail-wrap.up .wb-rail-stack');
      const inset = parseFloat(bcol.style.getPropertyValue('--wb-rail-inset')) || 0;
      const padTop = parseFloat(getComputedStyle(bcol).paddingTop) || 0;
      const cards = window.WsBoardAnchor.cards();
      const prevRail = cards.slice(0, cards.indexOf(term)).filter(n => n.hasAttribute('data-topic') || n.hasAttribute('data-rail')).length;
      const expectedTop = upStack && !upStack.hidden && prevRail
        ? upStack.getBoundingClientRect().bottom
        : bcol.getBoundingClientRect().top + padTop + inset;
      return {
        key: window.WsBoardAnchor.key(),
        cardTop: term.getBoundingClientRect().top,
        expectedTop,
        scrollTop: bcol.scrollTop,
        maxScroll: bcol.scrollHeight - bcol.clientHeight,
        smt: getComputedStyle(term).scrollMarginTop,
        spt: getComputedStyle(bcol).scrollPaddingTop,
        runway: bcol.style.getPropertyValue('--board-runway'),
        usable: bcol.style.getPropertyValue('--board-usable'),
      };
    });
    assert.equal(termLanded.key, 'c:term-1', '.bcard cap click must route through WsBoardAnchor.select');
    assert.ok(termLanded.scrollTop === 0 || Math.abs(termLanded.cardTop - termLanded.expectedTop) <= 3,
      `Terminal 1 card top (${termLanded.cardTop}) must align to expected top (${termLanded.expectedTop}): ${JSON.stringify(termLanded)}`);

    if (SHOTS_DIR) await page.screenshot({ path: path.join(SHOTS_DIR, '04-bcard-cap-click.png') });

    // Phase 1 invariant: non-current card DOM/size update performs ZERO outer-board geometry reads
    const geomCounts = await page.evaluate(() => {
      const orig = Element.prototype.getBoundingClientRect;
      const bcol = document.getElementById('bcol');
      const t2 = document.getElementById('topic-2');
      let outerReads = 0, otherReads = 0;
      Element.prototype.getBoundingClientRect = function () {
        if (this === bcol || this === document.documentElement) outerReads++;
        else if (t2.contains(this)) otherReads++;
        return orig.call(this);
      };
      try {
        const extra = document.createElement('div');
        extra.className = 'log-line';
        extra.textContent = 'Non-current streamed line';
        t2.querySelector('.tbody').appendChild(extra);
        window.WsBoardAnchor.select(document.querySelector('.bcard[data-card="term-1"]'), { still: true });
      } finally {
        Element.prototype.getBoundingClientRect = orig;
      }
      return { outerReads, otherReads };
    });
    assert.deepEqual(geomCounts, { outerReads: 0, otherReads: 0 }, 'non-current card update must perform zero geometry reads');

    // Designated-Y free-canvas assertions: no CSS scroll-snap, hidden scrollbar,
    // contain: layout style (no contain: strict) with entry-panel[data-max] &
    // overview lifts, visible .bcard .bc-grip, and .oq-pill 2-level offscreen
    // jump back to Topic 1's #item-1.
    await page.locator('#topic-4').evaluate(n => window.WsBoardAnchor.select(n, { smooth: false }));
    await waitFrames(page, 4);
    const freeCanvas = await page.evaluate(() => {
      const bcol = document.getElementById('bcol');
      const bcolCs = getComputedStyle(bcol);
      const t1 = document.getElementById('topic-1');
      const t4 = document.getElementById('topic-4');
      const card = document.querySelector('.bcard[data-card="term-1"]');
      const cardIn = card.querySelector(':scope > .bc-in');
      const grip = cardIn.querySelector(':scope > .bc-grip');
      const body = cardIn.querySelector(':scope > .bc-body');
      const usable = parseFloat(bcol.style.getPropertyValue('--board-usable'));
      const ep = document.createElement('details');
      ep.className = 'entry-panel';
      ep.setAttribute('data-max', '');
      t4.appendChild(ep);
      t4.setAttribute('data-has-panel-max', '');
      const t4ContainMax = getComputedStyle(t4).contain;
      const epRect = ep.getBoundingClientRect();
      const vw = window.innerWidth, vh = window.innerHeight;
      ep.remove();
      t4.removeAttribute('data-has-panel-max');
      // Verify terminal/web .bc-body preserves overflow: hidden inside #bcol
      const origKind = card.getAttribute('data-kind');
      card.setAttribute('data-kind', 'web');
      const webBodyOverflow = getComputedStyle(body).overflowY;
      if (origKind === null) card.removeAttribute('data-kind'); else card.setAttribute('data-kind', origKind);
      document.documentElement.setAttribute('data-overview', '');
      const ovSnap = getComputedStyle(bcol).scrollSnapType;
      const ovContain = getComputedStyle(t4).contain;
      document.documentElement.removeAttribute('data-overview');
      return {
        freeAttr: bcol.hasAttribute('data-board-free'),
        snapType: bcolCs.scrollSnapType,
        scrollbarWidth: bcolCs.scrollbarWidth,
        usable,
        t1Pos: getComputedStyle(t1).position,
        t1H: t1.getBoundingClientRect().height,
        t1ScrollMarginTop: getComputedStyle(t1).scrollMarginTop,
        cardScrollMarginTop: getComputedStyle(card).scrollMarginTop,
        t4Contain: getComputedStyle(t4).contain,
        t4ContainMax,
        epRect: { top: epRect.top, left: epRect.left, width: epRect.width, height: epRect.height },
        vw, vh,
        webBodyOverflow,
        ovSnap,
        ovContain,
        cardSnap: getComputedStyle(card).scrollSnapAlign,
        cardInH: cardIn.getBoundingClientRect().height,
        hasGrip: !!grip,
        gripDisplay: grip ? getComputedStyle(grip).display : '',
      };
    });
    assert.equal(freeCanvas.freeAttr, true, '#bcol enters Designated-Y free-canvas mode');
    assert.equal(freeCanvas.snapType, 'none', '#bcol uses native free scroll (no CSS scroll-snap)');
    assert.equal(freeCanvas.scrollbarWidth, 'none', '#bcol hides outer scrollbar');
    assert.equal(freeCanvas.t1Pos, 'absolute', 'mounted free-canvas topics use position: absolute');
    assert.ok(freeCanvas.t1H > 0, `topic keeps natural height (${freeCanvas.t1H})`);
    assert.equal(freeCanvas.cardScrollMarginTop, '0px', 'free-canvas slot 0 uses no CSS scroll-margin-top');
    assert.equal(freeCanvas.t1ScrollMarginTop, '0px', 'free-canvas slot 1 uses no CSS scroll-margin-top');
    assert.equal(freeCanvas.t4Contain, 'layout style', 'free-canvas topic uses contain: layout style (never strict/paint)');
    assert.equal(freeCanvas.t4ContainMax, 'none', 'entry-panel[data-max] lifts containment via data-has-panel-max');
    assert.ok(Math.abs(freeCanvas.epRect.top - 52) <= 1 && Math.abs(freeCanvas.epRect.left - 16) <= 1 &&
      Math.abs(freeCanvas.epRect.width - (freeCanvas.vw - 32)) <= 2 && Math.abs(freeCanvas.epRect.height - (freeCanvas.vh - 64)) <= 2 &&
      freeCanvas.epRect.height > freeCanvas.usable + 50,
      `entry-panel[data-max] fills viewport unclipped by #bcol: ${JSON.stringify(freeCanvas.epRect)} vs ${freeCanvas.vw}x${freeCanvas.vh}`);
    assert.equal(freeCanvas.webBodyOverflow, 'hidden', 'web .bcard .bc-body preserves overflow: hidden inside #bcol');
    assert.equal(freeCanvas.ovSnap, 'none', 'overview keeps scroll-snap-type: none');
    assert.equal(freeCanvas.ovContain, 'none', 'overview lifts containment');
    assert.equal(freeCanvas.cardSnap, 'none', '.bcard uses no CSS scroll-snap-align');
    assert.ok(freeCanvas.cardInH > 0, `.bcard > .bc-in keeps positive height (${freeCanvas.cardInH})`);
    assert.equal(freeCanvas.hasGrip, true, '.bc-grip is rendered on free-canvas .bcard');
    assert.notEqual(freeCanvas.gripDisplay, 'none', '.bc-grip is visible on unpinned free-canvas .bcard');

    // Offscreen .oq-pill 2-level jump: from Topic 4 (offscreen from Topic 1), update pill and click.
    await page.evaluate(() => {
      window.WsBoardAnchor.select(document.getElementById('topic-4'), { smooth: false });
    });
    await waitForScrollSettle(page);
    await page.evaluate(() => { if (window.wbPill && window.wbPill.update) window.wbPill.update(); });
    await waitFrames(page, 4);
    const pillTop = page.locator('.wb-oq-top .oq-pill');
    assert.equal(await pillTop.isVisible(), true, 'top .oq-pill visible when Topic 1 ask is offscreen above');
    await page.locator('#item-1').evaluate(el => {
      const scrollIntoView = el.scrollIntoView;
      el.scrollIntoView = function (options) {
        window.__askLandingKey = window.WsBoardAnchor.key();
        return scrollIntoView.call(this, options);
      };
    });
    await pillTop.click();
    await waitForScrollSettle(page);
    const jumped = await page.evaluate(() => {
      const bcol = document.getElementById('bcol').getBoundingClientRect();
      const item = document.getElementById('item-1').getBoundingClientRect();
      return {
        key: window.__askLandingKey,
        visible: item.bottom > bcol.top && item.top < bcol.bottom,
      };
    });
    assert.equal(jumped.key, 't:topic-1', 'offscreen .oq-pill jump selects target card first');
    assert.equal(jumped.visible, true, 'offscreen .oq-pill jump reveals target #item-1 inside card');
  } finally {
    await browser.close();
  }
});

test('Chromium free-canvas virtual scroll: tall uncached and lazy-expanded cards never overlap mounted siblings', async () => {
  let executablePath;
  if (!fs.existsSync(chromium.executablePath())) {
    const fallback = 'C:\\Users\\user\\AppData\\Local\\ms-playwright\\chromium_headless_shell-1248\\chrome-headless-shell-win64\\chrome-headless-shell.exe';
    if (fs.existsSync(fallback)) executablePath = fallback;
  }
  const browser = await chromium.launch({ headless: true, executablePath });
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', err => errors.push(`pageerror: ${err.message}`));
    const html = buildFixtureHtml({
      buildBoard: ({ topicHtml }) => {
        const items = [];
        for (let i = 1; i <= 22; i++) {
          if (i >= 9 && i <= 16) {
            items.push(topicHtml(i, `Topic ${i}: Tall Uncached History`, { tall: true }));
          } else if (i >= 18) {
            items.push(topicHtml(i, `Topic ${i}: Trailing Tall Card`, { tall: true }));
          } else {
            items.push(topicHtml(i, `Topic ${i}: Compact Card`, {}));
          }
        }
        return items.join('\n          ');
      },
    });
    await page.setContent(html, { waitUntil: 'load' });
    for (const content of buildBootstrapScript()) {
      await page.addScriptTag({ content });
    }
    await waitFrames(page, 6);

    const startup = await page.evaluate(() => {
      const info = window.WsBoardAnchor.windowInfo();
      return {
        active: info.active,
        start: info.start,
        end: info.end,
        hasAttr: document.getElementById('bcol').hasAttribute('data-board-free'),
      };
    });
    assert.equal(startup.active && startup.hasAttr, true, 'expected free-canvas mode');
    assert.ok(startup.end < 10, `expected trailing tall cards to start off-window/uncached (end=${startup.end})`);

    const checkMountedOrder = async phase => {
      const report = await page.evaluate(() => {
        const board = document.getElementById('r-board');
        const info = window.WsBoardAnchor.windowInfo();
        const cards = Array.from(board.querySelectorAll('section.topic'))
          .filter(el => !el.hasAttribute('data-wb-offwindow'));
        const rows = cards.map(el => {
          const r = el.getBoundingClientRect();
          return { id: el.id, top: r.top, bottom: r.bottom, height: r.height, styleTop: el.style.top };
        });
        return { info: { active: info.active, start: info.start, end: info.end, anchorIdx: info.anchorIdx }, rows };
      });
      assert.ok(report.rows.length >= 2, `${phase}: expected mounted cards, got ${report.rows.length}`);
      for (let i = 0; i < report.rows.length - 1; i++) {
        const a = report.rows[i];
        const b = report.rows[i + 1];
        assert.ok(
          b.top >= a.bottom,
          `${phase}: card overlap between ${a.id} (bottom=${a.bottom}, h=${a.height}, top=${a.styleTop}) and ${b.id} (top=${b.top}, styleTop=${b.styleTop})`
        );
      }
      return report;
    };

    // Scroll down incrementally via scrollTick into tall uncached cards (topic-9..topic-17).
    let reached17 = false;
    for (let step = 0; step < 35; step++) {
      await page.evaluate(() => {
        const bcol = document.getElementById('bcol');
        bcol.scrollTop += 420;
      });
      await waitFrames(page, 4);
      const rep = await checkMountedOrder(`scroll-step-${step}`);
      if (rep.info.end >= 17 && rep.rows.some(r => r.id === 'topic-17')) {
        reached17 = true;
        const tallCount = rep.rows.filter(r => r.height > 320).length;
        assert.ok(tallCount >= 2, `expected mounted >320px tall uncached cards, saw ${tallCount}`);
        break;
      }
    }
    assert.equal(reached17, true, 'scrolled into lazy target card topic-17');

    // Simulate lazy/closed content expanding topic-17 past 320px without calling anchorSettle().
    const t17Height = await page.evaluate(() => {
      const tlog = document.querySelector('#topic-17 .tlog');
      for (let k = 1; k <= 40; k++) {
        const d = document.createElement('div');
        d.className = 'log-line';
        d.textContent = `Lazy closed item ${k} — tall card must push trailing siblings down`;
        tlog.appendChild(d);
      }
      return document.getElementById('topic-17').getBoundingClientRect().height;
    });
    assert.ok(t17Height > 700, `expanded #topic-17 height (${t17Height}) must exceed 700px`);
    await waitFrames(page, 6);
    await waitForScrollSettle(page, 2500);
    await checkMountedOrder('after-lazy-expand-topic-17');

    // Scroll all the way to trailing uncached cards (topic-18..topic-22) and verify settled layout.
    for (let step = 0; step < 30; step++) {
      const endReached = await page.evaluate(() => window.WsBoardAnchor.windowInfo().end >= 21);
      if (endReached) break;
      await page.evaluate(() => {
        const bcol = document.getElementById('bcol');
        bcol.scrollTop += 480;
      });
      await waitFrames(page, 4);
      await checkMountedOrder(`tail-step-${step}`);
    }
    await waitForScrollSettle(page, 2500);
    const tailReport = await checkMountedOrder('settled-tail');
    assert.equal(tailReport.info.end, 21, 'reached final card index 21');
    assert.deepEqual(errors, [], `unexpected browser errors: ${errors.join('; ')}`);
  } finally {
    await browser.close();
  }
});
