#!/usr/bin/env node
'use strict';

// Playwright browser tests for Main Board Snap Deck Phase 3:
// 1. Middle-click (button === 1) toggle in #bcol & autoscroll suppression on
//    both mousedown and auxclick, plus interactive/terminal/modal/max exclusions.
// 2. Same-origin iframe.doc-frame middle-click postMessage relay (doc-frame.js
//    + dashboard-frames.js) with source/key/location validation.
// 3. Hero Panel highlight (.overview-here) + Wide 4-col Quick Look live preview
//    (>=1600px, !tabs) for both 0-docked and docked #bgal states, hover ~120ms
//    debounce, ArrowUp/ArrowDown instant preview, column-3 pointer lock,
//    Element.moveBefore state preservation, exact-height placeholder geometry,
//    fast snap-deck exit, and viewport shrink teardown.
// 4. All 4+1 Overview Holding Exemptions:
//    (0) applyFrameHeights sizes previewed doc-frames immediately while holding off-preview ones
//    (a) classify('preview') keeps clicks/focus inside Quick Look live without cancelling
//    (b) editable/.xterm targets inside Quick Look keep ArrowUp/ArrowDown/Enter
//    (c) keyed board patches update the actively previewed topic live without shifting #bcol
//    (d) maximize handoff from Quick Look via WsOverview.after restores DOM cleanly

const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const { test } = require('node:test');

const DEFAULT_PW = 'C:\\Users\\user\\AppData\\Local\\npm-cache\\_npx\\e41f203b7505f1fb\\node_modules\\playwright';
const pwPath = process.env.WS_PLAYWRIGHT_MODULE || (fs.existsSync(DEFAULT_PW) ? DEFAULT_PW : 'playwright');
const { chromium } = require(pwPath);

const REPO = path.resolve(__dirname, '../../../..');
const WSD = path.join(REPO, 'agents-plugin-tool/internal/wsdashboard/assets');
const WSW = path.join(REPO, 'agents-plugin-tool/internal/wswhiteboard/assets');
const SHOTS_DIR = process.env.WS_SNAP_P3_SHOTS || '';

function readAsset(dir, name) {
  return fs.readFileSync(path.join(dir, name), 'utf8');
}

function buildDocFrameSrcdoc(docKey, title, bodyHtml) {
  const docFrameJs = readAsset(WSW, 'doc-frame.js');
  const html = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="wb-doc-key" content="${docKey}">
<style>
  body { margin: 0; font: 13px/1.55 system-ui, sans-serif; color: #222; background: transparent; }
  .doc-body { padding: 8px 12px; }
  a { color: #2457c5; }
</style>
</head>
<body>
<div class="doc-body">
  <h3>${title}</h3>
  ${bodyHtml}
</div>
<script>
window.wbWatchChanges = function () {};
window.wbSvgTarget = function () { return null; };
window.wbFlattenSvg = function () { return null; };
${docFrameJs}
</script>
</body>
</html>`;
  return html.replace(/&/g, '&amp;').replace(/"/g, '&quot;');
}

function buildTopicHtml(i, opts = {}) {
  const states = ['discussing', 'implementing', 'researching', 'waiting', 'awaiting-confirmation'];
  const st = opts.state || states[(i - 1) % states.length];
  const rev = opts.rev || `rev-${i}`;
  const askAnswered = !!opts.askAnswered;
  const hasAsk = opts.hasAsk !== undefined ? opts.hasAsk : (i === 1 || i === 3);
  const askCard = hasAsk
    ? `<div class="card policy" id="item-${i}" data-wb-hold="q-${i}" data-wb-q="q-${i}">
         <div class="phead">${askAnswered ? 'Answered Question #' + i : 'Open Question #' + i}</div>
         <div class="pbody">
           <p class="ask-status">${askAnswered ? 'Answer recorded: Option A' : 'Please pick an option or add a note.'}</p>
           <textarea class="ask-input" rows="2" placeholder="Reply to question #${i}..."></textarea>
           <button type="button" class="ask-submit">Send</button>
         </div>
       </div>`
    : '';
  const docKey = `topic:topic-${i}`;
  const frameBlock = opts.withFrame
    ? `<div class="doc-frame" data-doc="${docKey}" data-doc-key="${docKey}" data-sandbox="allow-scripts" data-srcdoc="${buildDocFrameSrcdoc(
        docKey,
        `Topic ${i} Framed Document`,
        `<p>Framed body content for topic ${i}.</p><p><a href="https://example.com" class="frame-link">External link</a></p><div style="height:180px">Tall framed content block</div>`
      )}"></div>`
    : '';
  const lines = Array.from({ length: 14 }, (_, j) =>
    `<div class="log-line">Topic ${i} detail row ${j + 1} — design context and verification notes</div>`
  ).join('');
  return `<section class="topic" id="topic-${i}" data-topic="topic-${i}" data-th="${rev}" data-state="${st}" data-open-asks="${hasAsk && !askAnswered ? 'true' : 'false'}">
    <div class="tophead">
      <div class="tbar">
        <span class="tdot"></span>
        <span class="tname">Topic ${i} — ${['Architecture', 'Snap Deck Engine', 'Quick Look Preview', 'Middle-Click Relay', 'Gallery Persistence', 'Theme Polish'][(i - 1) % 6]}</span>
        <span class="tstate">${st}</span>
      </div>
      <div class="tsub"><div class="trecap"><span class="rat">L03:15 · just now — </span><div class="rtext">Recap summary for Topic ${i}: concise board overview context.</div></div></div>
    </div>
    ${askCard}
    ${frameBlock}
    <div class="tbody">
      <a class="topic-link" href="https://example.com/topic-${i}">Open doc link</a>
      <div class="tm-host"><div class="xterm">simulated terminal surface</div></div>
      ${lines}
    </div>
  </section>`;
}

function buildFixtureHtml(opts = {}) {
  const theme = opts.theme || 'light';
  const topicCount = opts.topicCount || 6;
  const layoutAttr = opts.layout ? ` data-layout="${opts.layout}"` : '';
  const css = [
    readAsset(WSW, 'page.css'),
    readAsset(WSW, 'page-items.css'),
    readAsset(WSW, 'page-panels.css'),
    readAsset(WSW, 'page-stream.css'),
    readAsset(WSW, 'page-sticky.css'),
    readAsset(WSD, 'dashboard.css'),
    readAsset(WSD, 'dashboard-layout.css'),
    readAsset(WSD, 'dashboard-board.css'),
    readAsset(WSD, 'dashboard-rail.css'),
  ].join('\n');
  const sprite = readAsset(WSW, 'sprite.svg');
  const topics = [];
  for (let i = 1; i <= topicCount; i++) {
    topics.push(buildTopicHtml(i, { withFrame: i === 2 || i === 3 || i === 4 }));
  }

  return `<!doctype html>
<html data-mode="live" data-theme="${theme}">
<head>
<meta charset="utf-8">
<style>
${css}
html, body { height: 100%; margin: 0; overflow: hidden; background: var(--wb-bg); color: var(--wb-fg); font-family: var(--wb-sans, system-ui, sans-serif); }
.dwrap { height: 100vh; display: flex; flex-direction: column; }
.top { flex: none; height: 40px; display: flex; align-items: center; padding: 0 14px; font-weight: 700; font-size: 13px; border-bottom: 1px solid var(--wb-line); background: var(--wb-card); }
.dbody { flex: 1 1 0; min-height: 0; }
.rail { padding: 10px; font-size: 12px; color: var(--wb-muted-fg); }
.convo { padding: 12px; font-size: 13px; color: var(--wb-muted-fg); background: var(--wb-card); border-radius: var(--wb-radius); }
#bcol { height: 100%; min-height: 0; }
section.topic { background: var(--wb-card); border-radius: var(--wb-radius); box-shadow: var(--wb-shadow); margin-bottom: 16px; }
section.topic > .tophead { border-bottom: 1px solid var(--wb-muted); }
section.topic > .tophead > .tbar { display: flex; align-items: center; gap: 8px; padding: 8px 12px; min-height: 36px; box-sizing: border-box; font-weight: 700; }
section.topic > .tophead > .tbar > .tname { flex: 1 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
section.topic > .tophead > .tbar > .tstate { font-size: 11px; padding: 2px 7px; border-radius: 999px; background: var(--wb-muted); color: var(--wb-muted-fg); font-weight: 600; }
section.topic > .tophead > .tsub { padding: 2px 12px 6px; font-size: 12px; color: var(--wb-muted-fg); }
section.topic > .tbody { padding: 8px 14px 12px; }
.card.policy { margin: 10px 14px; padding: 10px 12px; border-radius: 8px; border: 1px solid color-mix(in srgb, var(--wb-primary) 40%, var(--wb-line)); background: color-mix(in srgb, var(--wb-primary) 6%, var(--wb-card)); }
.card.policy .phead { font-weight: 700; font-size: 13px; margin-bottom: 6px; }
.card.policy .ask-input { width: 100%; box-sizing: border-box; font: inherit; font-size: 12.5px; padding: 6px 8px; border-radius: 6px; border: 1px solid var(--wb-line); background: var(--wb-bg); color: var(--wb-fg); }
.log-line { padding: 4px 0; border-bottom: 1px solid color-mix(in srgb, var(--wb-line) 55%, transparent); font-size: 13px; }
iframe.doc-frame { width: 100%; border: 0; display: block; height: 0; }
</style>
</head>
<body class="dash live">
<div hidden>${sprite}</div>
<div class="dwrap">
  <header class="top">ws dashboard — snap deck phase 3 fixture</header>
  <div class="dbody">
    <aside id="rail" class="rail"><strong>Sessions</strong><div>develop · active</div></aside>
    <main id="grid" class="dgrid live"${layoutAttr} data-tab="board">
      <div class="convo" id="convo"><strong>Conversation</strong><p>Live session transcript column.</p></div>
      <div id="bcol" class="bcol">
        <div id="r-board" class="r-board" data-region="board" data-h="h0">
          ${topics.join('\n')}
        </div>
      </div>
    </main>
  </div>
</div>
<script>
window.wsT = function (k) {
  const map = {
    'tab.board': 'Board',
    'js.board.toolbar.label': 'Board actions',
    'js.board.toolbar.overview': 'Overview',
    'js.board.toolbar.overview.title': 'Overview (Ctrl+Q)',
    'js.board.overview.hint': 'Click a card to jump, Esc to cancel',
    'js.board.toolbar.files': 'Files',
    'js.board.toolbar.download': 'Download',
    'js.board.toolbar.download.title': 'Download',
    'js.board.dial.label': 'Board panels',
    'js.board.dial.asks': 'Open asks',
    'js.board.dial.held': 'Updated',
    'js.board.filter.closed': 'Closed',
    'js.card.pin': 'Pin',
    'js.card.fold': 'Fold',
    'js.card.raise': 'Raise',
    'js.card.max': 'Maximize',
    'js.card.back': 'Restore',
    'js.card.width.normal': 'Normal width',
    'js.card.width.extend': 'Extend width',
    'js.pins.title': 'Pinned',
    'js.pins.drop': 'Drop here to pin',
    'js.pill.top': 'Open questions above',
    'js.pill.bot': 'Open questions below'
  };
  return map[k] || k;
};
window.wbToast = function () {};
</script>
</body>
</html>`;
}

function buildBootstrapScript(opts = {}) {
  const galleryJs = readAsset(WSD, 'gallery.js');
  const previewJs = readAsset(WSD, 'gal-rail-preview.js');
  const cardsJs = readAsset(WSD, 'cards.js');
  const overviewJs = readAsset(WSD, 'overview.js');
  const anchorJs = readAsset(WSD, 'dashboard-board-anchor.js');
  const framesJs = readAsset(WSD, 'dashboard-frames.js');
  const boardJs = readAsset(WSD, 'dashboard-board.js');

  const savedCards = opts.savedCards || { topics: [], cards: [] };

  return [
    galleryJs,
    previewJs,
    cardsJs,
    overviewJs,
    `(function () {
      var doc = document, root = doc.documentElement, rid = "p3test", mode = "live", live = true, worktree = "", layoutMode = "", framed = false;
      var shHidden = false, sseOn = true, cchips = null, ctext = null, mpend = null, tkApi = null, slash = null, railGate = null;
      var filesSheet = null, filesTab = null, fpane = null, chead = null, convo = $("convo"), grid = $("grid");
      var NEAR = "100%", FAR = "400%", mountList = [], mountTask = false, topicNear = {}, topicRO = null;
      var TL_REGIONS = {}, tlHeld = {}, LAYOUT_KEY = "ws.layout", cardIdentity = "p3test", shStage = null;
      var headHeld = null, railMinGen = 0, railRendered = null, railHoverUpdates = null;
      var TAB = { board: "board", entries: "convo", convlive: "convo" };
      function $(id) { return doc.getElementById(id); }
      function q(s, box) { return Array.prototype.slice.call((box || doc).querySelectorAll(s)); }
      function region(name) { return doc.querySelector('[data-region="' + name + '"]'); }
      function boardBoxes() { return [region("board"), $("bgal")].filter(Boolean); }
      function boardQ(s) { return boardBoxes().reduce(function (a, b) { return a.concat(q(s, b)); }, []); }
      function boardRead(fn) { return boardBoxes().reduce(function (a, b) { return Object.assign(a, fn(b)); }, {}); }
      function inBoard(n) { return boardBoxes().some(function (b) { return b.contains(n); }); }
      function frag(html) { var t = doc.createElement("template"); t.innerHTML = html; return t.content; }
      function wsT(k) { return window.wsT(k); }
      function toast() {}
      function load(k) { return k === "ws.dashboard.cards.p3test" ? ${JSON.stringify(JSON.stringify(savedCards))} : null; }
      function save() {}
      function post() { return Promise.resolve({}); }
      function browsePost() { return Promise.resolve({}); }
      function copyText() {}
      function gwRender() {}
      function railOpen() {}
      function railKey() {}
      function tabShown() { return true; }
      function setTab() {}
      function setCtab() {}
      function toggleStageFill() {}
      function docHidden() { return !!doc.hidden; }
      function visible() { return true; }
      function pinned() { return false; }
      function pinConvo() {}
      function scrollState() { return null; }
      function restoreScroll() {}
      function watchBoard() {}
      function restoreOpen() {}
      function forgetOpen() {}
      function fmtTimes() {}
      function sweepOutImgs() {}
      function syncSpinnerPhases() {}
      function mermaid() {}
      function syncChips() {}
      function applySent() {}
      function outboxSync() {}
      function drawerSync() {}
      function drawerSwapped() {}
      function drawerWait() {}
      function agSwapped() {}
      function gitSwapped() {}
      function syncBadge() {}
      function syncChead() {}
      function connected() {}
      function labelSwapped() {}
      function leadSwapped() {}
      function apScan() {}
      function tlCount() {}
      function labelEditing() { return false; }
      function editing() { return false; }
      function bindCapsuleNotes() {}
      function readAnswers(box) {
        var m = {};
        q("textarea.ask-input", box).forEach(function (ta) {
          var card = ta.closest("[data-wb-q]");
          if (card) m[card.getAttribute("data-wb-q")] = ta.value;
        });
        return m;
      }
      function writeAnswers(box, m) {
        if (!m) return;
        q("textarea.ask-input", box).forEach(function (ta) {
          var card = ta.closest("[data-wb-q]");
          var k = card && card.getAttribute("data-wb-q");
          if (k && Object.prototype.hasOwnProperty.call(m, k)) ta.value = m[k];
        });
      }
      function boardKeep() { return { h: { apply: function () {} }, scroller: function () { return $("bcol"); } }; }
      function boardRecordPending() {}
      function boardPendingBadge() {}
      function boardHoldIndicators() {}
      function boardReturnApply() {}
      function boardApplied() {}
      function boardStageInput(x) { return x; }
      function postDoc(f, msg) {
        if (!f || !f.contentWindow) return;
        var k = f.getAttribute("data-doc-key");
        if (k) msg = Object.assign({ docKey: k }, msg);
        f.contentWindow.postMessage(msg, "*");
      }
      function postTheme(f) {
        postDoc(f, { wbDoc: "theme", theme: root.getAttribute("data-theme") || "" });
      }
      function overviewButton(d) { return d.querySelector(".board-overview"); }
      function reducedMotion() { return true; }
      function boardFlushViewed() {}
      window.wbHistory = {
        place: function (n, opts) {
          var col = $("bcol");
          if (!col || !n) return;
          var r = n.getBoundingClientRect(), cr = col.getBoundingClientRect();
          col.scrollTop += (r.top - cr.top) - (opts && opts.offset || 0);
        },
        capture: function () { return []; },
        init: function () {},
        prepare: function () {},
        restore: function () {},
        sweep: function () {},
        keep: function () { return { apply: function () {} }; }
      };
      ${anchorJs}
      ${framesJs}
      ${boardJs}
      mountBoardOverview();
      anchorInit();
      mountFrames(region("board"));
      indexFrames();
      if (window.WsBoardAnchor) window.WsBoardAnchor.settle();
      window.__testSwapBoard = function (hash, html) { swap("board", hash, html, true); };
      window.__testOverviewHeld = function () { return boardOverviewHeld; };
    })();`,
  ].join('\n');
}

async function openFixture(browser, opts = {}) {
  const context = await browser.newContext({
    viewport: opts.viewport || { width: 2560, height: 960 },
    colorScheme: opts.theme === 'dark' ? 'dark' : 'light',
    reducedMotion: 'reduce',
  });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.setContent(buildFixtureHtml(opts), { waitUntil: 'domcontentloaded' });
  await page.addScriptTag({ content: buildBootstrapScript(opts) });
  if (errors.length) throw new Error('Bootstrap pageerror: ' + errors.join('; '));
  const globals = await page.evaluate(() => ({
    WsOverview: !!window.WsOverview,
    WsCards: !!window.WsCards,
    WsBoardAnchor: !!window.WsBoardAnchor,
  }));
  if (!globals.WsOverview || !globals.WsCards || !globals.WsBoardAnchor) {
    throw new Error('Missing globals: ' + JSON.stringify(globals) + ' errors=' + JSON.stringify(errors));
  }
  return { context, page, errors };
}

test('Phase 3: middle-click toggle, autoscroll suppression, exclusions & doc-frame relay', async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const { context, page, errors } = await openFixture(browser, { viewport: { width: 1280, height: 900 } });

    // Select Topic 2 as the active Hero Panel before opening Overview.
    await page.evaluate(() => {
      const t2 = document.querySelector('section.topic[data-topic="topic-2"]');
      window.WsBoardAnchor.select(t2, { keep: true, still: false, smooth: false });
    });

    // Wait for topic-2 iframe.doc-frame to mount and size.
    await page.waitForFunction(() => {
      const f = document.querySelector('section.topic[data-topic="topic-2"] iframe.doc-frame');
      return f && parseFloat(f.style.height) > 50;
    });

    // 1. Middle-click on #bcol topic header opens Overview and suppresses autoscroll on both mousedown & auxclick.
    const preventLog = await page.evaluate(() => {
      window.__midLog = [];
      const col = document.getElementById('bcol');
      col.addEventListener('mousedown', e => { if (e.button === 1) window.__midLog.push({ type: 'mousedown', prevented: e.defaultPrevented }); });
      col.addEventListener('auxclick', e => { if (e.button === 1) window.__midLog.push({ type: 'auxclick', prevented: e.defaultPrevented }); });
      return true;
    });
    assert.equal(preventLog, true);

    const t2Head = page.locator('section.topic[data-topic="topic-2"] .tbar');
    const box = await t2Head.boundingBox();
    await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2, { button: 'middle' });
    const dbg = await page.evaluate(({ x, y }) => {
      const el = document.elementFromPoint(x, y);
      return {
        active: window.WsOverview.active(),
        el: el ? el.tagName + '.' + el.className : null,
        log: window.__midLog,
        blockedModal: !!document.querySelector('#tkqpop[open], dialog[open], .tpop:not([hidden]), [popover]:popover-open, .bcard[data-max], section.topic[data-max], details.entry-panel[data-max]'),
        boardMax: document.documentElement.hasAttribute('data-board-max'),
      };
    }, { x: box.x + box.width / 2, y: box.y + box.height / 2 });
    assert.equal(dbg.active, true, 'middle-click must open Overview: ' + JSON.stringify(dbg));
    const openLog = await page.evaluate(() => window.__midLog.slice());
    assert.ok(openLog.some(x => x.type === 'mousedown' && x.prevented === true), 'mousedown must call preventDefault()');
    assert.ok(openLog.some(x => x.type === 'auxclick' && x.prevented === true), 'auxclick must call preventDefault()');

    // Verify Hero Panel highlight is on topic-2.
    const hereTopic = await page.evaluate(() => document.querySelector('.overview-here')?.getAttribute('data-topic'));
    assert.equal(hereTopic, 'topic-2');

    // 2. Middle-clicking again in #bcol cancels Overview and returns to topic-2.
    const t3Tile = page.locator('section.topic[data-topic="topic-3"]');
    const t3Box = await t3Tile.boundingBox();
    await page.mouse.click(t3Box.x + t3Box.width / 2, t3Box.y + t3Box.height / 2, { button: 'middle' });
    await page.waitForFunction(() => window.WsOverview.active() === false);
    const currentAfterCancel = await page.evaluate(() => document.querySelector('section.topic[data-current]')?.getAttribute('data-topic'));
    assert.equal(currentAfterCancel, 'topic-2', 'middle-click while open cancels back to prior Hero Panel');

    // 3. Exclusions: a[href], .xterm, .tm-host do NOT toggle Overview and do NOT preventDefault.
    const excl = await page.evaluate(() => {
      const results = {};
      for (const [name, sel] of [
        ['link', 'section.topic[data-topic="topic-2"] a.topic-link'],
        ['xterm', 'section.topic[data-topic="topic-2"] .xterm'],
        ['tmhost', 'section.topic[data-topic="topic-2"] .tm-host'],
      ]) {
        const el = document.querySelector(sel);
        const md = new MouseEvent('mousedown', { button: 1, bubbles: true, cancelable: true });
        el.dispatchEvent(md);
        const ac = new MouseEvent('auxclick', { button: 1, bubbles: true, cancelable: true });
        el.dispatchEvent(ac);
        results[name] = { mdPrevented: md.defaultPrevented, acPrevented: ac.defaultPrevented, active: window.WsOverview.active() };
      }
      return results;
    });
    for (const [name, r] of Object.entries(excl)) {
      assert.equal(r.mdPrevented, false, `${name} mousedown must not be prevented`);
      assert.equal(r.acPrevented, false, `${name} auxclick must not be prevented`);
      assert.equal(r.active, false, `${name} middle-click must not open Overview`);
    }

    // 4. Same-origin iframe.doc-frame middle-click relay toggles Overview once & suppresses autoscroll.
    const fBox = await page.locator('section.topic[data-topic="topic-2"] iframe.doc-frame').boundingBox();
    await page.mouse.click(fBox.x + 40, fBox.y + 30, { button: 'middle' });
    await page.waitForFunction(() => window.WsOverview.active() === true);

    // Close via Escape.
    await page.keyboard.press('Escape');
    await page.waitForFunction(() => window.WsOverview.active() === false);

    // Middle-clicking a[href] inside doc-frame does NOT open Overview.
    const frameHandle = await page.locator('section.topic[data-topic="topic-2"] iframe.doc-frame').elementHandle();
    const docFrame = await frameHandle.contentFrame();
    await docFrame.locator('a.frame-link').click({ button: 'middle' });
    await page.waitForTimeout(120);
    assert.equal(await page.evaluate(() => window.WsOverview.active()), false, 'middle-click on a[href] inside doc-frame must not toggle Overview');

    assert.deepEqual(errors, []);
    await context.close();
  } finally {
    await browser.close();
  }
});

test('Phase 3: wide 4-col Quick Look (0-docked + docked), 4+1 exemptions, moveBefore & screenshots', async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    // --- A. Wide 4-col (Band 4, >=2442px) with 0 docked pages (dark theme for Shot 01, light for Shot 02) ---
    {
      const { context, page, errors } = await openFixture(browser, {
        viewport: { width: 2560, height: 960 },
        theme: 'dark',
      });

      await page.evaluate(() => {
        const t2 = document.querySelector('section.topic[data-topic="topic-2"]');
        window.WsBoardAnchor.select(t2, { keep: true, still: false, smooth: false });
      });

      // Open Overview via middle-click.
      const t2Bar = await page.locator('section.topic[data-topic="topic-2"] .tbar').boundingBox();
      await page.mouse.click(t2Bar.x + 60, t2Bar.y + 15, { button: 'middle' });
      await page.waitForFunction(() => document.documentElement.hasAttribute('data-overview-preview'));

      // Verify 0-docked Quick Look opens column 3 un-dimmed.
      const wideState = await page.evaluate(() => {
        const gal = document.getElementById('bgal');
        const galStyle = getComputedStyle(gal);
        const galRect = gal.getBoundingClientRect();
        const ph = document.querySelector('.overview-placeholder');
        const live = document.querySelector('.overview-preview-stage > section.topic');
        return {
          galHidden: gal.hidden,
          galFilter: galStyle.filter,
          galWidth: Math.round(galRect.width),
          phHere: ph && ph.classList.contains('overview-here'),
          phPreviewing: ph && ph.classList.contains('overview-previewing'),
          liveTopic: live && live.getAttribute('data-topic'),
        };
      });
      assert.equal(wideState.galHidden, false);
      assert.equal(wideState.galFilter, 'none', 'column 3 must be un-dimmed in Quick Look');
      assert.ok(wideState.galWidth >= 280, `column 3 width >= 280px, got ${wideState.galWidth}`);
      assert.equal(wideState.phHere, true);
      assert.equal(wideState.phPreviewing, true);
      assert.equal(wideState.liveTopic, 'topic-2');

      if (SHOTS_DIR) {
        fs.mkdirSync(SHOTS_DIR, { recursive: true });
        await page.screenshot({ path: path.join(SHOTS_DIR, '01-overview-wide-hero-highlight-dark.png') });
      }

      // Switch theme to light for hover Quick Look + Shot 02, and stamp live state on topic-3 before hovering.
      await page.evaluate(() => {
        document.documentElement.setAttribute('data-theme', 'light');
        const t3 = document.querySelector('section.topic[data-topic="topic-3"]');
        t3.__preserveToken = 'alive-via-moveBefore';
        const ta = t3.querySelector('textarea.ask-input');
        if (ta) ta.value = 'Draft answer before preview';
      });

      const t3OrigRect = await page.locator('section.topic[data-topic="topic-3"]').boundingBox();
      await page.mouse.move(t3OrigRect.x + 60, t3OrigRect.y + 20);
      // Should NOT switch immediately (<120ms debounce).
      await page.waitForTimeout(40);
      assert.equal(
        await page.evaluate(() => document.querySelector('.overview-preview-stage > section.topic')?.getAttribute('data-topic')),
        'topic-2',
        'hover switch must wait for ~120ms debounce'
      );

      // After settle, topic-3 moves into column 3 via Element.moveBefore with exact-height placeholder.
      await page.waitForFunction(() => document.querySelector('.overview-preview-stage > section.topic')?.getAttribute('data-topic') === 'topic-3');
      // Wait for Exemption (0): previewed iframe.doc-frame sizes immediately while Overview is holding!
      await page.waitForFunction(() => {
        const f = document.querySelector('.overview-preview-stage section.topic[data-topic="topic-3"] iframe.doc-frame');
        return f && parseFloat(f.style.height) > 50;
      });

      const hoverCheck = await page.evaluate((origH) => {
        const t2 = document.querySelector('#r-board section.topic[data-topic="topic-2"]');
        const ph = document.querySelector('#r-board .overview-placeholder');
        const live = document.querySelector('.overview-preview-stage > section.topic[data-topic="topic-3"]');
        const f4 = document.querySelector('#r-board section.topic[data-topic="topic-4"] iframe.doc-frame');
        return {
          t2StillHere: !!(t2 && t2.classList.contains('overview-here')),
          phHeightDiff: ph ? Math.abs(ph.getBoundingClientRect().height - origH) : 999,
          tokenPreserved: live && live.__preserveToken === 'alive-via-moveBefore',
          draftPreserved: live && live.querySelector('textarea.ask-input')?.value === 'Draft answer before preview',
          previewFrameHeight: parseFloat(live.querySelector('iframe.doc-frame')?.style.height || '0'),
          offPreviewFrameHeight: f4 ? parseFloat(f4.style.height || '0') : 0,
        };
      }, t3OrigRect.height);

      assert.equal(hoverCheck.t2StillHere, true, 'Hero Panel topic-2 stays visibly marked .overview-here');
      assert.ok(hoverCheck.phHeightDiff <= 1.5, `placeholder height matches original within 1.5px, got diff=${hoverCheck.phHeightDiff}`);
      assert.equal(hoverCheck.tokenPreserved, true, 'Element.moveBefore must preserve live DOM node identity');
      assert.equal(hoverCheck.draftPreserved, true, 'Element.moveBefore must preserve form input state');
      assert.ok(hoverCheck.previewFrameHeight > 50, 'Exemption (0): previewed doc-frame must size immediately');

      if (SHOTS_DIR) {
        await page.screenshot({ path: path.join(SHOTS_DIR, '02-overview-wide-quicklook-hover-light.png') });
      }

      // Pointer lock: moving pointer into column 3 locks preview; moving over another tile isn't triggered while locked.
      const prevBox = await page.locator('.overview-preview').boundingBox();
      await page.mouse.move(prevBox.x + 80, prevBox.y + 80);

      // Exemption (a) & (b): click textarea inside Quick Look, type, press ArrowUp/ArrowDown/Enter without exiting Overview.
      const taLoc = page.locator('.overview-preview-stage textarea.ask-input');
      await taLoc.click();
      await page.keyboard.type(' + live edit');
      await page.keyboard.press('ArrowDown');
      await page.keyboard.press('ArrowUp');
      await page.keyboard.press('Enter');
      const editCheck = await page.evaluate(() => ({
        overviewActive: window.WsOverview.active(),
        previewTopic: document.querySelector('.overview-preview-stage > section.topic')?.getAttribute('data-topic'),
        focusedTag: document.activeElement && document.activeElement.tagName,
        value: document.querySelector('.overview-preview-stage textarea.ask-input')?.value,
      }));
      assert.equal(editCheck.overviewActive, true, 'Exemption (a/b): clicking & typing/Arrow/Enter in preview textarea must NOT exit Overview');
      assert.equal(editCheck.previewTopic, 'topic-3', 'Exemption (b): ArrowUp/ArrowDown in textarea must NOT switch preview');
      assert.equal(editCheck.focusedTag, 'TEXTAREA');
      assert.ok(editCheck.value.includes('+ live edit') && editCheck.value.includes('\n'), 'Enter must insert newline inside preview textarea: ' + JSON.stringify(editCheck.value));

      // Exemption (c): live keyed board patch updates previewed topic-3 in place while holding topic-5 in boardOverviewHeld.
      const updatedBoardHtml = [1, 2, 3, 4, 5, 6].map(i =>
        buildTopicHtml(i, {
          withFrame: i === 2 || i === 3 || i === 4,
          rev: i === 3 ? 'rev-3-answered' : i === 5 ? 'rev-5-held' : `rev-${i}`,
          askAnswered: i === 3,
        })
      ).join('\n');
      await page.evaluate((html) => {
        const live = document.querySelector('.overview-preview-stage > section.topic[data-topic="topic-3"]');
        live.scrollTop = 42;
        window.__testSwapBoard('h1', html);
      }, updatedBoardHtml);

      const patchCheck = await page.evaluate(() => {
        const live = document.querySelector('.overview-preview-stage > section.topic[data-topic="topic-3"]');
        const t5 = document.querySelector('#r-board section.topic[data-topic="topic-5"]');
        return {
          overviewActive: window.WsOverview.active(),
          askText: live?.querySelector('.ask-status')?.textContent,
          scrollTop: live?.scrollTop,
          t5RevStillOld: t5?.getAttribute('data-th') === 'rev-5',
          heldHash: window.__testOverviewHeld()?.hash,
        };
      });
      assert.equal(patchCheck.overviewActive, true, 'Exemption (c): live board patch must NOT exit Overview');
      assert.equal(patchCheck.askText, 'Answer recorded: Option A', 'Exemption (c): previewed topic must update live');
      assert.equal(patchCheck.scrollTop, 42, 'Exemption (c): previewed topic must preserve scrollTop');
      assert.equal(patchCheck.t5RevStillOld, true, 'Exemption (c): non-previewed #bcol topics must remain held');
      assert.equal(patchCheck.heldHash, 'h1');

      // Exemption (d): clicking maximize (.d-max) inside Quick Look cleanly exits Overview and maximizes topic-3 in #bcol.
      await page.locator('.overview-preview-stage section.topic[data-topic="topic-3"] .d-max').click();
      await page.waitForTimeout(150);
      const maxDbg = await page.evaluate(() => ({
        overviewActive: window.WsOverview.active(),
        boardMaxAttr: document.documentElement.getAttribute('data-board-max'),
        hasBoardMax: document.documentElement.hasAttribute('data-board-max'),
        maxNodes: document.querySelectorAll('[data-max]').length,
        topic3Parent: document.querySelector('section.topic[data-topic="topic-3"]')?.parentElement?.id || document.querySelector('section.topic[data-topic="topic-3"]')?.parentElement?.className,
        topic3InBoard: !!document.querySelector('#r-board section.topic[data-topic="topic-3"]'),
      }));
      assert.equal(maxDbg.overviewActive, false, 'Exemption (d): Overview must close on .d-max click: ' + JSON.stringify(maxDbg) + ' errors=' + JSON.stringify(errors));
      assert.equal(maxDbg.hasBoardMax, true, 'Exemption (d): data-board-max must be set: ' + JSON.stringify(maxDbg) + ' errors=' + JSON.stringify(errors));
      const maxCheck = await page.evaluate(() => ({
        boardMaxAttr: document.documentElement.getAttribute('data-board-max'),
        overviewPreviewAttr: document.documentElement.hasAttribute('data-overview-preview'),
        placeholdersLeft: document.querySelectorAll('.overview-placeholder').length,
        topic3InBoard: !!document.querySelector('#r-board section.topic[data-topic="topic-3"][data-max]'),
        t5FlushedRev: document.querySelector('#r-board section.topic[data-topic="topic-5"]')?.getAttribute('data-th'),
      }));
      assert.equal(maxCheck.boardMaxAttr, '', 'Exemption (d): maximized topic from Quick Look must have data-board-max="" (not "gal")');
      assert.equal(maxCheck.overviewPreviewAttr, false);
      assert.equal(maxCheck.placeholdersLeft, 0);
      assert.equal(maxCheck.topic3InBoard, true);
      assert.equal(maxCheck.t5FlushedRev, 'rev-5-held', 'held board updates flush cleanly on Overview exit');

      // Minimize via Escape, then test ArrowDown + Enter commit and fast snap-deck exit.
      await page.keyboard.press('Escape');
      await page.waitForFunction(() => !document.documentElement.hasAttribute('data-board-max'));

      await page.evaluate(() => window.WsOverview.toggle());
      await page.waitForFunction(() => window.WsOverview.active() === true);
      await page.keyboard.press('ArrowDown');
      const previewAfterArrow = await page.evaluate(() => document.querySelector('.overview-preview-stage > section.topic')?.getAttribute('data-topic'));
      assert.equal(previewAfterArrow, 'topic-3', 'ArrowDown must switch Quick Look immediately');
      await page.keyboard.press('Enter');
      await page.waitForFunction(() => window.WsOverview.active() === false);
      const committed = await page.evaluate(() => ({
        current: document.querySelector('section.topic[data-current]')?.getAttribute('data-topic'),
        motionLeft: document.querySelectorAll('[data-overview-motion]').length,
        placeholdersLeft: document.querySelectorAll('.overview-placeholder').length,
      }));
      assert.equal(committed.current, 'topic-3', 'Enter commits previewed topic as Hero Panel');
      assert.equal(committed.motionLeft, 0);
      assert.equal(committed.placeholdersLeft, 0);

      // Viewport shrink below 1600px tears down Quick Look cleanly without broken column 3.
      await page.evaluate(() => window.WsOverview.toggle());
      await page.waitForFunction(() => document.documentElement.hasAttribute('data-overview-preview'));
      await page.setViewportSize({ width: 1280, height: 900 });
      await page.waitForFunction(() => !document.documentElement.hasAttribute('data-overview-preview'));
      const narrowCheck = await page.evaluate(() => ({
        overviewActive: window.WsOverview.active(),
        placeholdersLeft: document.querySelectorAll('.overview-placeholder').length,
        herePresent: !!document.querySelector('#r-board .overview-here'),
      }));
      assert.equal(narrowCheck.overviewActive, true);
      assert.equal(narrowCheck.placeholdersLeft, 0);
      assert.equal(narrowCheck.herePresent, true);

      if (SHOTS_DIR) {
        await page.screenshot({ path: path.join(SHOTS_DIR, '04-overview-narrow-hero-highlight-light.png') });
      }

      assert.deepEqual(errors, []);
      await context.close();
    }

    // --- B. Wide 4-col (Band 4, >=2442px) with docked #bgal page preserved (dark theme for Shot 03) ---
    {
      const { context, page, errors } = await openFixture(browser, {
        viewport: { width: 2560, height: 960 },
        theme: 'dark',
        savedCards: {
          topics: [{ n: 'topic-6', p: 1, a: 1, b: null, f: false }],
          cards: [],
        },
      });

      await page.waitForFunction(() => !!document.querySelector('#bgal .gal-viewport section.topic[data-topic="topic-6"]'));
      await page.evaluate(() => {
        const docked = document.querySelector('#bgal .gal-viewport section.topic[data-topic="topic-6"]');
        docked.__dockedToken = 'docked-alive';
        const t1 = document.querySelector('#r-board section.topic[data-topic="topic-1"]');
        window.WsBoardAnchor.select(t1, { keep: true, still: true, smooth: false });
        window.WsOverview.toggle();
      });
      await page.waitForFunction(() => document.documentElement.hasAttribute('data-overview-preview'));

      const dockedDuring = await page.evaluate(() => {
        const vp = document.querySelector('#bgal > .gal-viewport');
        const docked = vp.querySelector('section.topic[data-topic="topic-6"]');
        return {
          vpDisplay: getComputedStyle(vp).display,
          vpInert: vp.inert,
          dockedAlive: docked && docked.__dockedToken === 'docked-alive',
          livePreview: document.querySelector('.overview-preview-stage > section.topic')?.getAttribute('data-topic'),
        };
      });
      assert.notEqual(dockedDuring.vpDisplay, 'none', 'docked .gal-viewport must NEVER use display: none');
      assert.equal(dockedDuring.vpInert, true, 'docked .gal-viewport must be inert during Quick Look');
      assert.equal(dockedDuring.dockedAlive, true);
      assert.equal(dockedDuring.livePreview, 'topic-1');

      if (SHOTS_DIR) {
        await page.screenshot({ path: path.join(SHOTS_DIR, '03-overview-wide-quicklook-docked-preserved-dark.png') });
      }

      // Exit Overview and verify docked page is restored and interactive.
      await page.keyboard.press('Escape');
      await page.waitForFunction(() => window.WsOverview.active() === false);
      const dockedAfter = await page.evaluate(() => {
        const vp = document.querySelector('#bgal > .gal-viewport');
        const docked = vp.querySelector('section.topic[data-topic="topic-6"]');
        return {
          vpInert: vp.inert,
          dockedAlive: docked && docked.__dockedToken === 'docked-alive',
          previewHostsLeft: document.querySelectorAll('.overview-preview').length,
        };
      });
      assert.equal(dockedAfter.vpInert, false);
      assert.equal(dockedAfter.dockedAlive, true);
      assert.equal(dockedAfter.previewHostsLeft, 0);

      assert.deepEqual(errors, []);
      await context.close();
    }
  } finally {
    await browser.close();
  }
});

test('Phase 3 review regressions: #1 boardRendered sync, #2 bgal relayout, #3 far doc-frame, #4 fold unfold, #5 hidden destructive btns, #6 replacePreviewNode, #7 pointerleave cancel', async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const { context, page, errors } = await openFixture(browser, {
      viewport: { width: 2560, height: 960 },
      theme: 'dark',
      savedCards: {
        topics: [{ n: 'topic-4', p: 0, a: 0, b: null, f: true }],
        cards: [],
      },
    });

    // Inject a folded topic, a folded terminal bcard, and a far-offscreen doc-frame topic.
    await page.evaluate(() => {
      const board = document.getElementById('r-board');
      const t4 = board.querySelector('section.topic[data-topic="topic-4"]');
      t4.setAttribute('data-size', 'fold');
      // Add pin/fold/raise buttons to topic-4 to verify #5 hiding inside Quick Look.
      const tbar = t4.querySelector(':scope > .tophead > .tbar');
      ['tpin', 't-fold', 't-raise'].forEach(cls => {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = 'ibtn ' + cls;
        tbar.appendChild(b);
      });
    });

    // Select topic-2 as Hero Panel and open Overview.
    await page.evaluate(() => {
      const t2 = document.querySelector('section.topic[data-topic="topic-2"]');
      window.WsBoardAnchor.select(t2, { keep: true, still: false, smooth: false });
      window.WsOverview.toggle();
    });
    await page.waitForFunction(() => document.documentElement.hasAttribute('data-overview-preview'));

    // --- Regression #7: pointerleave on #r-board cancels pending 120ms hover switch ---
    const t3Box = await page.locator('#r-board section.topic[data-topic="topic-3"]').boundingBox();
    await page.mouse.move(t3Box.x + 40, t3Box.y + 15);
    await page.waitForTimeout(30);
    await page.evaluate(() => {
      document.getElementById('r-board').dispatchEvent(new PointerEvent('pointerleave', { bubbles: false }));
    });
    await page.waitForTimeout(160);
    assert.equal(
      await page.evaluate(() => document.querySelector('.overview-preview-stage > section.topic')?.getAttribute('data-topic')),
      'topic-2',
      '#7: pointerleave on #r-board must cancel pending 120ms hover preview switch'
    );

    // --- Regression #2: WsCards.relayout() during Quick Look keeps #bgal visible; exit re-hides it ---
    const galDuringRelayout = await page.evaluate(() => {
      window.WsCards.relayout();
      return {
        hidden: document.getElementById('bgal').hidden,
        hasPreviewAttr: document.documentElement.hasAttribute('data-overview-preview'),
      };
    });
    assert.equal(galDuringRelayout.hidden, false, '#2: paintGallery must keep #bgal visible while data-overview-preview is active');
    assert.equal(galDuringRelayout.hasPreviewAttr, true);

    // --- Regression #6: replacePreviewNode preserves .overview-here and migrates tabs Map ---
    const replaceCheck = await page.evaluate(() => {
      const oldNode = window.WsOverview.previewNode();
      const hadHere = oldNode.classList.contains('overview-here');
      const clone = oldNode.cloneNode(true);
      clone.classList.remove('overview-here');
      clone.removeAttribute('data-overview-live');
      oldNode.parentNode.replaceChild(clone, oldNode);
      window.WsOverview.replacePreviewNode(oldNode, clone);
      return {
        hadHere,
        newHasHere: clone.classList.contains('overview-here'),
        newIsPreview: window.WsOverview.previewNode() === clone,
      };
    });
    assert.equal(replaceCheck.hadHere, true);
    assert.equal(replaceCheck.newHasHere, true, '#6: replacePreviewNode must copy .overview-here');
    assert.equal(replaceCheck.newIsPreview, true);

    // --- Regression #4 & #5: folded topic-4 unfolds in Quick Look, destructive btns hidden, .d-max visible ---
    await page.evaluate(() => {
      // Simulate band state being far-offscreen on topic-4 before previewing (#3).
      const t4 = document.querySelector('#r-board section.topic[data-topic="topic-4"]');
      t4.wbNear = false;
    });
    await page.keyboard.press('ArrowDown'); // topic-3
    await page.keyboard.press('ArrowDown'); // topic-4
    await page.waitForFunction(() => document.querySelector('.overview-preview-stage > section.topic')?.getAttribute('data-topic') === 'topic-4');
    // Wait for far-offscreen doc-frame inside topic-4 to mount and size (#3).
    await page.waitForFunction(() => {
      const f = document.querySelector('.overview-preview-stage section.topic[data-topic="topic-4"] iframe.doc-frame');
      return f && parseFloat(f.style.height) > 50;
    });

    const t4Checks = await page.evaluate(() => {
      const t4 = document.querySelector('.overview-preview-stage > section.topic[data-topic="topic-4"]');
      const body = t4.querySelector('.tbody');
      const frame = t4.querySelector('iframe.doc-frame');
      return {
        bodyDisplay: getComputedStyle(body).display,
        tpinDisplay: getComputedStyle(t4.querySelector('.tpin')).display,
        tfoldDisplay: getComputedStyle(t4.querySelector('.t-fold')).display,
        traiseDisplay: getComputedStyle(t4.querySelector('.t-raise')).display,
        dmaxDisplay: getComputedStyle(t4.querySelector('.d-max')).display,
        frameMounted: !!frame && frame.tagName === 'IFRAME',
      };
    });
    assert.notEqual(t4Checks.bodyDisplay, 'none', '#4: folded topic must unfold inside Quick Look');
    assert.equal(t4Checks.tpinDisplay, 'none', '#5: .tpin hidden in Quick Look');
    assert.equal(t4Checks.tfoldDisplay, 'none', '#5: .t-fold hidden in Quick Look');
    assert.equal(t4Checks.traiseDisplay, 'none', '#5: .t-raise hidden in Quick Look');
    assert.notEqual(t4Checks.dmaxDisplay, 'none', '#5: .d-max stays visible in Quick Look');
    assert.equal(t4Checks.frameMounted, true, '#3: far-offscreen doc-frame must mount in Quick Look');

    // Append a folded terminal .bcard inside #r-board and navigate to it (topic-5 -> topic-6 -> card-term-1) to verify #4 & #5 on cards.
    await page.evaluate(() => {
      const board = document.getElementById('r-board');
      const card = document.createElement('article');
      card.className = 'bcard';
      card.setAttribute('data-card', 'card-term-1');
      card.setAttribute('data-kind', 'terminal');
      card.setAttribute('data-size', 'fold');
      card.innerHTML =
        '<div class="bc-in">' +
          '<div class="bc-hd"><span class="bc-nm">Term Card</span><div class="bc-acts">' +
            '<button type="button" class="ibtn bc-pin"></button>' +
            '<button type="button" class="ibtn bc-fold"></button>' +
            '<button type="button" class="ibtn bc-raise"></button>' +
            '<button type="button" class="ibtn bc-max"></button>' +
            '<button type="button" class="ibtn bc-close"></button>' +
          '</div></div>' +
          '<div class="bc-folded">folded summary</div>' +
          '<div class="bc-body"><div class="tm-host">terminal body</div></div>' +
        '</div>';
      const spacer = board.querySelector('.overview-spacer');
      board.insertBefore(card, spacer || null);
    });
    await page.keyboard.press('ArrowDown'); // topic-5
    await page.keyboard.press('ArrowDown'); // topic-6
    await page.keyboard.press('ArrowDown'); // card-term-1
    await page.waitForFunction(() => document.querySelector('.overview-preview-stage > .bcard')?.getAttribute('data-card') === 'card-term-1');
    const cardChecks = await page.evaluate(() => {
      const c = document.querySelector('.overview-preview-stage > .bcard[data-card="card-term-1"]');
      return {
        bodyDisplay: getComputedStyle(c.querySelector('.bc-body')).display,
        foldedDisplay: getComputedStyle(c.querySelector('.bc-folded')).display,
        pinDisplay: getComputedStyle(c.querySelector('.bc-pin')).display,
        foldDisplay: getComputedStyle(c.querySelector('.bc-fold')).display,
        raiseDisplay: getComputedStyle(c.querySelector('.bc-raise')).display,
        closeDisplay: getComputedStyle(c.querySelector('.bc-close')).display,
        maxDisplay: getComputedStyle(c.querySelector('.bc-max')).display,
      };
    });
    assert.equal(cardChecks.bodyDisplay, 'flex', '#4: folded terminal bcard must preserve display:flex in Quick Look');
    assert.equal(cardChecks.foldedDisplay, 'none', '#4: .bc-folded stays hidden in Quick Look');
    assert.equal(cardChecks.pinDisplay, 'none', '#5: .bc-pin hidden in Quick Look');
    assert.equal(cardChecks.foldDisplay, 'none', '#5: .bc-fold hidden in Quick Look');
    assert.equal(cardChecks.raiseDisplay, 'none', '#5: .bc-raise hidden in Quick Look');
    assert.equal(cardChecks.closeDisplay, 'none', '#5: .bc-close hidden in Quick Look');
    assert.notEqual(cardChecks.maxDisplay, 'none', '#5: .bc-max stays visible in Quick Look');

    // --- Regression #1: patchOverviewPreview updates boardRendered.topics so exit never reverts ---
    await page.keyboard.press('ArrowUp'); // topic-6 (card-term-1 returns to #r-board without data-overview-live)
    const cardRefolded = await page.evaluate(() => {
      const c = document.querySelector('#r-board .bcard[data-card="card-term-1"]');
      return {
        hasLive: c.hasAttribute('data-overview-live'),
        sizeAttr: c.getAttribute('data-size'),
        bodyDisplay: getComputedStyle(c.querySelector('.bc-body')).display,
      };
    });
    assert.equal(cardRefolded.hasLive, false, '#4: leaving Quick Look removes data-overview-live from folded bcard');
    assert.equal(cardRefolded.sizeAttr, 'fold');
    assert.equal(cardRefolded.bodyDisplay, 'none', '#4: folded bcard re-folds cleanly after leaving Quick Look');

    await page.keyboard.press('ArrowUp'); // topic-5
    await page.keyboard.press('ArrowUp'); // topic-4
    await page.keyboard.press('ArrowUp'); // topic-3
    await page.waitForFunction(() => document.querySelector('.overview-preview-stage > section.topic')?.getAttribute('data-topic') === 'topic-3');
    const patchedHtml = [1, 2, 3, 4, 5, 6].map(i =>
      buildTopicHtml(i, {
        withFrame: i === 2 || i === 3 || i === 4,
        rev: i === 3 ? 'rev-3-post-patch' : `rev-${i}`,
        askAnswered: i === 3,
      })
    ).join('\n');
    await page.evaluate((html) => window.__testSwapBoard('h-reg-1', html), patchedHtml);

    // Exit Overview via Escape: topic-3 must keep its live-patched content, #bgal re-hides, folded topic re-folds, tabindex restored.
    await page.keyboard.press('Escape');
    await page.waitForFunction(() => window.WsOverview.active() === false);
    const postExit = await page.evaluate(() => {
      const t3 = document.querySelector('#r-board section.topic[data-topic="topic-3"]');
      const t2 = document.querySelector('#r-board section.topic[data-topic="topic-2"]');
      const t4 = document.querySelector('#r-board section.topic[data-topic="topic-4"]');
      return {
        t3AskStatus: t3?.querySelector('.ask-status')?.textContent,
        t3Rev: t3?.getAttribute('data-th'),
        galHidden: document.getElementById('bgal').hidden,
        t4BodyDisplay: getComputedStyle(t4.querySelector('.tbody')).display,
        t4FoldedAttr: t4.getAttribute('data-size'),
        t2Tabindex: t2.getAttribute('tabindex'),
      };
    });
    assert.equal(postExit.t3AskStatus, 'Answer recorded: Option A', '#1: Overview exit must preserve live-patched topic without reverting');
    assert.equal(postExit.t3Rev, 'rev-3-post-patch');
    assert.equal(postExit.galHidden, false, '#2: Band 4 idle empty slot remains visible in #bgal after Overview exit');
    assert.equal(postExit.t4BodyDisplay, 'none', '#4: folded topic must re-fold cleanly on Overview exit');
    assert.equal(postExit.t4FoldedAttr, 'fold');
    assert.equal(postExit.t2Tabindex, null, '#6: unrove() must restore tabindex on replaced preview node');

    assert.deepEqual(errors, []);
    await context.close();
  } finally {
    await browser.close();
  }
});

test('Phase 3 Band 3 (3-col) regression: Quick Look stays off in 3-col and toggles cleanly across Band 3 <-> Band 4 resize', async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    // 1720px on default 5:7 live grid is Band 3 (3-col: >=1600px, <2442px).
    const { context, page, errors } = await openFixture(browser, {
      viewport: { width: 1720, height: 960 },
      theme: 'dark',
    });

    await page.evaluate(() => {
      const t2 = document.querySelector('section.topic[data-topic="topic-2"]');
      window.WsBoardAnchor.select(t2, { keep: true, still: false, smooth: false });
      window.WsOverview.toggle();
    });
    await page.waitForFunction(() => window.WsOverview.active() === true);

    const band3Open = await page.evaluate(() => ({
      innerWidth: window.innerWidth,
      isFourCol: window.WsCards.isFourCol(),
      hasPreviewAttr: document.documentElement.hasAttribute('data-overview-preview'),
      previewHostCount: document.querySelectorAll('.overview-preview').length,
      placeholderCount: document.querySelectorAll('.overview-placeholder').length,
      galHidden: document.getElementById('bgal').hidden,
      t2InBoard: !!document.querySelector('#r-board section.topic[data-topic="topic-2"]'),
    }));
    assert.ok(band3Open.innerWidth >= 1600, 'Band 3 viewport is >= 1600px');
    assert.equal(band3Open.isFourCol, false, '1720px live grid must NOT be 4-col (Band 4 starts at 2442px)');
    assert.equal(band3Open.hasPreviewAttr, false, 'Band 3 Overview must NOT set data-overview-preview');
    assert.equal(band3Open.previewHostCount, 0, 'Band 3 Overview must NOT mount .overview-preview');
    assert.equal(band3Open.placeholderCount, 0);
    assert.equal(band3Open.galHidden, false, 'Band 3 keeps #bgal mounted for system pages in narrow mode');
    assert.equal(band3Open.t2InBoard, true);

    // Hovering and pressing ArrowDown in Band 3 must still NOT open Quick Look.
    const t3Box = await page.locator('#r-board section.topic[data-topic="topic-3"]').boundingBox();
    await page.mouse.move(t3Box.x + 40, t3Box.y + 15);
    await page.waitForTimeout(160);
    await page.keyboard.press('ArrowDown');
    const band3AfterNav = await page.evaluate(() => ({
      hasPreviewAttr: document.documentElement.hasAttribute('data-overview-preview'),
      previewHostCount: document.querySelectorAll('.overview-preview').length,
    }));
    assert.equal(band3AfterNav.hasPreviewAttr, false, 'hover/ArrowDown in Band 3 must NOT set data-overview-preview');
    assert.equal(band3AfterNav.previewHostCount, 0, 'hover/ArrowDown in Band 3 must NOT mount .overview-preview');

    // Resize UP from Band 3 (1720px) to Band 4 (2560px) while Overview is open -> Quick Look enables cleanly.
    await page.setViewportSize({ width: 2560, height: 960 });
    await page.waitForFunction(() => document.documentElement.hasAttribute('data-overview-preview'));
    const band4Up = await page.evaluate(() => ({
      isFourCol: window.WsCards.isFourCol(),
      overviewActive: window.WsOverview.active(),
      hasPreviewAttr: document.documentElement.hasAttribute('data-overview-preview'),
      previewHostCount: document.querySelectorAll('.overview-preview').length,
      liveTopic: document.querySelector('.overview-preview-stage > section.topic')?.getAttribute('data-topic'),
      galHidden: document.getElementById('bgal').hidden,
    }));
    assert.equal(band4Up.isFourCol, true);
    assert.equal(band4Up.overviewActive, true);
    assert.equal(band4Up.hasPreviewAttr, true, 'resizing Band 3 -> Band 4 while Overview is open must enable Quick Look');
    assert.equal(band4Up.previewHostCount, 1);
    assert.equal(band4Up.liveTopic, 'topic-3', 'Quick Look after Band 3 -> Band 4 resize must preview the roved topic');
    assert.equal(band4Up.galHidden, false);

    // Layout mode toggle at 2560px while Overview is open: tabs tears down Quick Look, grid remounts it.
    const tabsMode = await page.evaluate(() => {
      const grid = document.querySelector('.dgrid');
      grid.setAttribute('data-layout', 'tabs');
      window.WsCards.relayout();
      const t3 = document.querySelector('#r-board section.topic[data-topic="topic-3"]');
      return {
        isFourCol: window.WsCards.isFourCol(),
        hasPreviewAttr: document.documentElement.hasAttribute('data-overview-preview'),
        previewHostCount: document.querySelectorAll('.overview-preview').length,
        t3InBoard: !!t3,
        t3HasLiveAttr: !!(t3 && t3.hasAttribute('data-overview-live')),
      };
    });
    assert.equal(tabsMode.isFourCol, false);
    assert.equal(tabsMode.hasPreviewAttr, false, 'switching to tabs layout at 2560px must tear down Quick Look');
    assert.equal(tabsMode.previewHostCount, 0);
    assert.equal(tabsMode.t3InBoard, true);
    assert.equal(tabsMode.t3HasLiveAttr, false);

    const gridMode = await page.evaluate(() => {
      const grid = document.querySelector('.dgrid');
      grid.setAttribute('data-layout', 'grid');
      window.WsCards.relayout();
      return {
        isFourCol: window.WsCards.isFourCol(),
        hasPreviewAttr: document.documentElement.hasAttribute('data-overview-preview'),
        previewHostCount: document.querySelectorAll('.overview-preview').length,
        liveTopic: document.querySelector('.overview-preview-stage > section.topic')?.getAttribute('data-topic'),
      };
    });
    assert.equal(gridMode.isFourCol, true);
    assert.equal(gridMode.hasPreviewAttr, true, 'switching back to grid layout at 2560px must remount Quick Look');
    assert.equal(gridMode.previewHostCount, 1);
    assert.equal(gridMode.liveTopic, 'topic-3');

    // Resize DOWN from Band 4 (2560px) to Band 3 (1720px) while Overview is open -> Quick Look tears down cleanly.
    await page.setViewportSize({ width: 1720, height: 960 });
    await page.waitForFunction(() => !document.documentElement.hasAttribute('data-overview-preview'));
    const band3Down = await page.evaluate(() => {
      const t3 = document.querySelector('#r-board section.topic[data-topic="topic-3"]');
      return {
        isFourCol: window.WsCards.isFourCol(),
        overviewActive: window.WsOverview.active(),
        hasPreviewAttr: document.documentElement.hasAttribute('data-overview-preview'),
        previewHostCount: document.querySelectorAll('.overview-preview').length,
        placeholderCount: document.querySelectorAll('.overview-placeholder').length,
        t3InBoard: !!t3,
        t3HasLiveAttr: !!(t3 && t3.hasAttribute('data-overview-live')),
        galHidden: document.getElementById('bgal').hidden,
      };
    });
    assert.equal(band3Down.isFourCol, false);
    assert.equal(band3Down.overviewActive, true, 'Overview stays open after Band 4 -> Band 3 resize');
    assert.equal(band3Down.hasPreviewAttr, false);
    assert.equal(band3Down.previewHostCount, 0);
    assert.equal(band3Down.placeholderCount, 0);
    assert.equal(band3Down.t3InBoard, true, 'previewed topic must be restored to #r-board on Band 4 -> Band 3 teardown');
    assert.equal(band3Down.t3HasLiveAttr, false, 'restored topic must not retain data-overview-live');
    assert.equal(band3Down.galHidden, false, '#bgal stays mounted in Band 3 narrow mode for system pages');

    assert.deepEqual(errors, []);
    await context.close();
  } finally {
    await browser.close();
  }
});
