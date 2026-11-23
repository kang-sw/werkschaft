#!/usr/bin/env node
'use strict';

// Playwright Chromium tests for Main Board Snap Deck Phase 4:
// 1. Windowed O(1) topic slot virtualization (±2 snap slots around anchorCurrent)
//    with bounded mounted DOM nodes and bounded mounted iframe.doc-frame count.
// 2. SSE / morphBoard update to a collapsed far topic stashes latest revision
//    without mounting its body, updating .tophead in place, and hydrates cleanly
//    when scrolled/jumped into view.
// 3. Preserved inner invariants across collapse -> re-hydration (both without
//    and with stashed SSE revision): loaded history rows, details[open], lazy
//    history gaps (.history-gap[data-lazy]).
// 4. Strict exemptions: unpinned .bcard widgets (terminal, web, preview,
//    explorer), #bgal-docked panes, active Overview Quick Look previewed far
//    topic, and .xterm/.tm-host PTYs are NEVER evicted.
// 5. Zero layout shift (scrollTop / anchor stability) across rapid dial scrub
//    (WsBoardAnchor.scrubbing() deferral + reconcile on settle) and fast wheel
//    flicks, plus zero idle rAF after settle.
// 6. Phase 1 dead-code cleanup: no .bc-grip, no .bdrop, no --board-runway.

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
const SHOTS_DIR = process.env.WS_SNAP_P4_SHOTS || '';

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
  body { margin: 0; font: 13px/1.55 system-ui, sans-serif; color: #334155; background: transparent; }
  .doc-body { padding: 8px 12px; }
  h3 { margin: 0 0 6px; font-size: 14px; }
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
  const rev = opts.rev || `rev-${i}-v1`;
  const titleSuffix = opts.titleSuffix || '';
  const bodyNote = opts.bodyNote || `Server revision ${rev} content for Topic ${i}.`;
  const docKey = `topic:topic-${i}`;
  const frameBlock = `<div class="tdoc"><div class="doc-frame" data-doc="${docKey}" data-doc-key="${docKey}" data-sandbox="allow-scripts" data-srcdoc="${buildDocFrameSrcdoc(
    docKey,
    `Topic ${i} Spec (${rev})`,
    `<p>${bodyNote}</p><div style="height:140px">Framed spec body for topic ${i}</div>`
  )}"></div></div>`;
  const ptyBlock = opts.withPty
    ? `<div class="tm-host" data-pty-id="pty-${i}"><div class="xterm">live pty surface topic-${i}</div></div>`
    : '';
  const extraRows = (opts.extraRows || []).map(r =>
    `<div class="timeline-row" data-row="${r.id}"><div class="wb-entry" id="${r.id}">${r.text}</div></div>`
  ).join('');
  const historyBlock = `<div class="topic-stream"><div class="wb-history" data-history="topic-${i}">
    <div class="history-live">
      <div class="history-gap" data-lazy="1" data-gap-id="gap-${i}">
        <template data-history-gap="gap-${i}">
          <div class="timeline-row" data-row="t${i}-r0"><div class="wb-entry" id="e${i}00">Archived row 0 for Topic ${i}</div></div>
        </template>
      </div>
      ${extraRows}
      <div class="timeline-row" data-row="t${i}-r1">
        <details class="entry-panel" data-key="panel-${i}"${opts.detailOpen ? ' open' : ''}>
          <summary><span>Decision Record #${i} (${rev})</span></summary>
          <div class="panel-body"><p class="rev-marker">${bodyNote}</p></div>
        </details>
      </div>
      <div class="timeline-row" data-row="t${i}-r2">
        <div class="wb-entry" id="e${i}02">Latest timeline update for Topic ${i} (${rev})</div>
      </div>
    </div>
  </div></div>`;
  const lines = Array.from({ length: 10 }, (_, j) =>
    `<div class="log-line">Topic ${i} line ${j + 1} (${rev})</div>`
  ).join('');
  return `<section class="topic" id="topic-${i}" data-topic="topic-${i}" data-th="${rev}" data-state="${st}" data-open-asks="false">
    <div class="tophead">
      <div class="tbar">
        <span class="tdot"></span>
        <span class="tname">Topic ${i} — ${['Snap Deck', 'Virtualizer', 'Quick Look', 'Frame Bands', 'History Stream', 'PTY Bridge'][(i - 1) % 6]}${titleSuffix}</span>
        <span class="tstate">${st}</span>
      </div>
      <div class="tsub"><div class="trecap"><span class="rat">L04:20 · just now — </span><div class="rtext">${bodyNote}</div></div></div>
    </div>
    ${frameBlock}
    ${historyBlock}
    <div class="tbody">
      ${ptyBlock}
      ${lines}
    </div>
  </section>`;
}

function buildFixtureHtml(opts = {}) {
  const theme = opts.theme || 'dark';
  const topicCount = opts.topicCount || 18;
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
    topics.push(buildTopicHtml(i, { withPty: i === 17 }));
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
section.topic > .tbody, section.topic > .topic-stream { padding: 6px 14px 10px; }
details.entry-panel { border: 1px solid var(--wb-line); border-radius: 8px; padding: 6px 10px; margin: 6px 0; background: color-mix(in srgb, var(--wb-bg) 60%, var(--wb-card)); }
details.entry-panel > summary { cursor: pointer; font-weight: 600; font-size: 13px; }
.log-line { padding: 3px 0; border-bottom: 1px solid color-mix(in srgb, var(--wb-line) 45%, transparent); font-size: 12.5px; }
.tm-host { margin: 6px 0; padding: 8px 10px; border-radius: 6px; background: #0f172a; color: #e2e8f0; font: 12px/1.4 monospace; }
iframe.doc-frame { width: 100%; border: 0; display: block; height: 0; }
.widget-iframe { width: 100%; height: 180px; border: 1px solid var(--wb-line); border-radius: 6px; background: var(--wb-bg); }
.widget-term { padding: 10px 12px; background: #0b1120; color: #38bdf8; font: 12.5px/1.5 monospace; border-radius: 6px; margin: 10px; }
</style>
</head>
<body class="dash live">
<div hidden>${sprite}</div>
<div class="dwrap">
  <header class="top">ws dashboard — snap deck phase 4 (O(1) windowed virtualization)</header>
  <div class="dbody">
    <aside id="rail" class="rail"><strong>Sessions</strong><div>develop · active</div><div>20-slot stress deck</div></aside>
    <main id="grid" class="dgrid live" data-tab="board">
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
    'js.card.close': 'Close',
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
  const dialJs = readAsset(WSD, 'dashboard-board-dial.js');
  const framesJs = readAsset(WSD, 'dashboard-frames.js');
  const boardJs = readAsset(WSD, 'dashboard-board.js');

  const savedCards = opts.savedCards || { topics: [], cards: [] };

  return [
    galleryJs,
    previewJs,
    cardsJs,
    overviewJs,
    `(function () {
      var doc = document, root = doc.documentElement, rid = "p4test", mode = "live", live = true, worktree = "", layoutMode = "", framed = false;
      var shHidden = false, sseOn = true, cchips = null, ctext = null, mpend = null, tkApi = null, slash = null, railGate = null;
      var filesSheet = null, filesTab = null, fpane = null, chead = null, convo = $("convo"), grid = $("grid");
      var NEAR = "100%", FAR = "400%", mountList = [], mountTask = false, topicNear = {}, topicRO = null;
      var TL_REGIONS = {}, tlHeld = {}, LAYOUT_KEY = "ws.layout", cardIdentity = "p4test", shStage = null;
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
      function load(k) { return k === "ws.dashboard.cards.p4test" ? ${JSON.stringify(JSON.stringify(savedCards))} : null; }
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
      function readAnswers() { return {}; }
      function writeAnswers() {}
      function boardKeep() { return { h: { apply: function () {} }, scroller: function () { return $("bcol"); } }; }
      function boardRecordPending() {}
      function boardPendingBadge() {}
      function boardHoldIndicators() {}
      function boardReturnApply() {}
      function boardApplied() {}
      function boardStageInput(x) { return x; }
      function boardFlushViewed() {}
      function reducedMotion() { return false; }
      function postDoc(f, msg) { try { if (f && f.contentWindow) f.contentWindow.postMessage(msg, "*"); } catch (e) {} }
      function postTheme(f) { postDoc(f, { wbDoc: "theme", theme: root.getAttribute("data-theme") || "dark" }); }
      function openLinkMenu() {}
      function webLink() { return false; }
      function desktopPreview() { return false; }
      function frameMention() {}
      function hideMention() {}
      function tkSearch() {}
      function overviewButton() { return doc.querySelector(".board-overview"); }
      function frameGraftable() { return null; }
      function frameKeep() { return null; }
      function frameGrafts() { return []; }
      function frameGraft() {}
      function frameKept() {}

      // Minimal wbHistory stub with real capture/restore for .timeline-row & details[data-key]
      window.wbHistory = {
        bands: true,
        watch: function (t, cfg) {
          if (cfg && cfg.onChange) setTimeout(function () { cfg.onChange({ visible: true, rect: t.getBoundingClientRect() }); }, 0);
          return function () {};
        },
        init: function () {},
        capture: function (box) {
          var hs = Array.prototype.slice.call((box || doc).querySelectorAll('.wb-history[data-history]'));
          return hs.map(function (h) {
            return {
              key: h.getAttribute('data-history'),
              rows: Array.prototype.slice.call(h.querySelectorAll('.timeline-row')).map(function (r) {
                return { id: r.getAttribute('data-row'), html: r.outerHTML };
              }),
              folds: Array.prototype.slice.call(h.querySelectorAll('details[data-key]')).map(function (d) {
                return [d.getAttribute('data-key'), !!d.open];
              })
            };
          });
        },
        restore: function (box, snap) {
          (snap || []).forEach(function (saved) {
            var h = (box || doc).querySelector('.wb-history[data-history="' + saved.key + '"]');
            if (!h) return;
            var liveBox = h.querySelector('.history-live') || h;
            var existing = new Set(Array.prototype.slice.call(h.querySelectorAll('.timeline-row')).map(function (r) { return r.getAttribute('data-row'); }));
            var firstRow = liveBox.querySelector('.timeline-row');
            (saved.rows || []).forEach(function (r) {
              if (!r.id || existing.has(r.id)) return;
              var t = doc.createElement('template');
              t.innerHTML = r.html;
              var node = t.content.firstElementChild;
              if (node) liveBox.insertBefore(node, firstRow || null);
            });
            var foldMap = new Map(saved.folds || []);
            Array.prototype.forEach.call(h.querySelectorAll('details[data-key]'), function (d) {
              var dk = d.getAttribute('data-key');
              if (foldMap.has(dk)) d.open = !!foldMap.get(dk);
            });
          });
        },
        place: function () {},
        loadTarget: function () { return null; },
        keep: function () { return { apply: function () {}, end: function () {}, visible: function () { return true; }, scroller: $("bcol") }; },
        rendered: function () { return true; }
      };

      // Register all 4 unpinned .bcard kinds (terminal, web, preview, explorer)
      window.WsCards.register("terminal", {
        base: 320,
        open: function (c) {
          c.title({ icon: "terminal", name: "Dev Terminal", sub: "zsh · live pty", badge: "Terminal" });
          var box = doc.createElement("div");
          box.className = "widget-term tm-host";
          box.innerHTML = '<div class="xterm" data-term-token="alive-term">$ go test ./internal/wsdashboard/...\\nPASS (0.8s)</div>';
          c.body.appendChild(box);
        }
      });
      window.WsCards.register("web", {
        base: 320,
        open: function (c) {
          c.title({ icon: "globe", name: "Local Preview", sub: "http://127.0.0.1:5173", badge: "Web" });
          var f = doc.createElement("iframe");
          f.className = "widget-iframe";
          f.setAttribute("data-web-iframe", "1");
          f.srcdoc = '<!doctype html><html><body style="font:13px system-ui;padding:12px;margin:0"><div id="web-state">Web widget alive</div></body></html>';
          c.body.appendChild(f);
        }
      });
      window.WsCards.register("preview", {
        base: 280,
        open: function (c) {
          c.title({ icon: "file-text", name: "README.md", sub: "docs/README.md", badge: "Preview" });
          var d = doc.createElement("div");
          d.style.padding = "12px";
          d.setAttribute("data-preview-token", "alive-preview");
          d.textContent = "Preview card content stays mounted.";
          c.body.appendChild(d);
        }
      });
      window.WsCards.register("explorer", {
        base: 280,
        open: function (c) {
          c.title({ icon: "folder-open", name: "Workspace Files", sub: "agents-plugin-tool/", badge: "Explorer" });
          var d = doc.createElement("div");
          d.style.padding = "12px";
          d.setAttribute("data-explorer-token", "alive-explorer");
          d.textContent = "Explorer tree stays mounted.";
          c.body.appendChild(d);
        }
      });

      ${framesJs}
      ${anchorJs}
      ${dialJs}
      ${boardJs}

      // Open all 4 unpinned .bcard kinds in the Main Board flow and dock topic-18 into #bgal
      window.WsCards.open("terminal", { id: "term-1" });
      window.WsCards.open("web", { id: "web-1" });
      window.WsCards.open("preview", { id: "prev-1" });
      window.WsCards.open("explorer", { id: "exp-1" });
      window.WsCards.move($("topic-18"), { pin: true });
      var mainDot = doc.querySelector(".gal-dot.main");
      if (mainDot) mainDot.click();

      mountBoardOverview();
      anchorInit();
      bandBoard(region("board"));
      mountFrames(region("board"));
      window.__p4SwapBoard = function (hash, html) { swap("board", hash, html, true); };
    })();`,
  ].join('\n');
}

async function waitFrames(page, n = 4) {
  await page.evaluate(count => new Promise(resolve => {
    let left = count;
    (function tick() {
      if (--left <= 0) resolve();
      else requestAnimationFrame(tick);
    })();
  }), n);
}

test('Phase 4: O(1) ±2 topic virtualization, stashed SSE reconciliation, preserved state, strict exemptions, scrub stability, and dead-code removal', async () => {
  if (SHOTS_DIR) fs.mkdirSync(SHOTS_DIR, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1760, height: 960 }, reducedMotion: 'reduce' });
    const page = await context.newPage();
    const pageErrors = [];
    page.on('pageerror', err => pageErrors.push(String(err && err.stack || err)));
    await page.setContent(buildFixtureHtml({ theme: 'dark', topicCount: 18 }), { waitUntil: 'domcontentloaded' });
    await page.addScriptTag({ content: buildBootstrapScript() });
    await waitFrames(page, 10);
    assert.deepEqual(pageErrors, [], 'no uncaught page errors during bootstrap');

    // 1. Dead-code cleanup checks
    const deadCode = await page.evaluate(() => {
      const bcol = document.getElementById('bcol');
      return {
        gripCount: document.querySelectorAll('.bc-grip').length,
        bdropCount: document.querySelectorAll('.bdrop').length,
        runwayVar: bcol.style.getPropertyValue('--board-runway'),
      };
    });
    assert.equal(deadCode.gripCount, 0, 'retired .bc-grip must not exist in DOM');
    assert.equal(deadCode.bdropCount, 0, 'retired .bdrop must not exist in DOM');
    assert.equal(deadCode.runwayVar, '', 'retired --board-runway must not be set on #bcol');

    // Stamp mutable tokens on unpinned .bcard widgets + web iframe so we can prove zero eviction/reload
    await page.evaluate(() => {
      document.querySelectorAll('#r-board .bcard').forEach((card, i) => {
        card.__p4Identity = 'card-' + i;
      });
      const webIframe = document.querySelector('#r-board .bcard[data-kind="web"] iframe[data-web-iframe]');
      if (webIframe && webIframe.contentWindow) {
        webIframe.contentWindow.__p4Alive = 'never-reloaded';
      }
      const docked = document.querySelector('#bgal section.topic[data-topic="topic-18"]');
      if (docked) docked.__p4DockedIdentity = 'docked-18';
    });

    // 2. Jump to middle topic (topic-9) and verify O(1) ±2 window + bounded mounted DOM & doc-frames
    await page.evaluate(() => {
      window.WsBoardAnchor.select(document.getElementById('topic-9'), { smooth: false });
    });
    await waitFrames(page, 10);

    const midStats = await page.evaluate(() => {
      const cards = window.WsBoardAnchor.cards();
      const cur = document.querySelector('#r-board [data-current]');
      const curIdx = cards.indexOf(cur);
      const boardTopics = Array.from(document.querySelectorAll('#r-board > section.topic[data-topic]'));
      const hydratedTopics = boardTopics.filter(t => !t.hasAttribute('data-wb-virtual'));
      const collapsedTopics = boardTopics.filter(t => t.hasAttribute('data-wb-virtual'));
      const mountedDocFrames = document.querySelectorAll('#r-board iframe.doc-frame').length;
      const collapsedWithTopheadOnly = collapsedTopics.every(t => t.children.length === 1 && t.firstElementChild.classList.contains('tophead'));
      const bcardsCollapsed = Array.from(document.querySelectorAll('#r-board .bcard')).some(c => c.hasAttribute('data-wb-virtual'));
      const ptyTopic = document.getElementById('topic-17');
      const dockedTopic = document.querySelector('#bgal section.topic[data-topic="topic-18"]');
      return {
        curKey: window.WsBoardAnchor.key(),
        curIdx,
        totalBoardTopics: boardTopics.length,
        hydratedNames: hydratedTopics.map(t => t.getAttribute('data-topic')),
        collapsedCount: collapsedTopics.length,
        collapsedWithTopheadOnly,
        mountedDocFrames,
        bcardsCollapsed,
        ptyTopicCollapsed: ptyTopic.hasAttribute('data-wb-virtual'),
        dockedTopicCollapsed: dockedTopic ? dockedTopic.hasAttribute('data-wb-virtual') : null,
      };
    });

    assert.equal(midStats.curKey, 't:topic-9');
    assert.equal(midStats.collapsedWithTopheadOnly, true, 'every collapsed far topic keeps .tophead mounted and detaches body');
    // Hydrated topics in #r-board: ±2 around topic-9 (topics 7, 8, 9, 10, 11) + exempt topic-17 (has .xterm/.tm-host) = 6
    assert.deepEqual(
      midStats.hydratedNames,
      ['topic-7', 'topic-8', 'topic-9', 'topic-10', 'topic-11', 'topic-17'],
      'only ±2 near topics plus exempt .xterm/.tm-host topic remain hydrated',
    );
    assert.ok(midStats.collapsedCount >= 11, `at least 11 far topics collapsed (got ${midStats.collapsedCount})`);
    assert.ok(midStats.mountedDocFrames <= 6, `mounted iframe.doc-frame count strictly bounded O(1) (got ${midStats.mountedDocFrames})`);
    assert.equal(midStats.bcardsCollapsed, false, 'unpinned .bcard widgets are NEVER collapsed');
    assert.equal(midStats.ptyTopicCollapsed, false, 'topic with .xterm/.tm-host is NEVER collapsed');
    assert.equal(midStats.dockedTopicCollapsed, false, '#bgal-docked topic is NEVER collapsed');

    if (SHOTS_DIR) {
      await page.screenshot({ path: path.join(SHOTS_DIR, '01-large-board-near-window-dark.png') });
    }

    // 3. Preserved inner invariants (loaded history row, details[open], lazy history gap) + stashed SSE update
    // First jump to topic-12, expand details[data-key="panel-12"], and append a loaded history row.
    await page.evaluate(() => {
      const t12 = document.getElementById('topic-12');
      window.WsBoardAnchor.select(t12, { smooth: false });
    });
    await waitFrames(page, 6);
    await page.evaluate(() => {
      const t12 = document.getElementById('topic-12');
      const det = t12.querySelector('details[data-key="panel-12"]');
      det.open = true;
      const liveBox = t12.querySelector('.history-live');
      const loadedRow = document.createElement('div');
      loadedRow.className = 'timeline-row';
      loadedRow.setAttribute('data-row', 't12-loaded-extra');
      loadedRow.innerHTML = '<div class="wb-entry" id="e1299">Loaded earlier history row preserved across eviction</div>';
      liveBox.insertBefore(loadedRow, liveBox.querySelector('.timeline-row'));
    });

    // Jump far away to topic-1 so topic-12 collapses
    await page.evaluate(() => {
      window.WsBoardAnchor.select(document.getElementById('topic-1'), { smooth: false });
    });
    await waitFrames(page, 6);
    const t12Collapsed = await page.evaluate(() => ({
      collapsed: document.getElementById('topic-12').hasAttribute('data-wb-virtual'),
      children: document.getElementById('topic-12').children.length,
    }));
    assert.equal(t12Collapsed.collapsed, true, 'topic-12 collapsed when far from topic-1');
    assert.equal(t12Collapsed.children, 1, 'topic-12 keeps only .tophead while collapsed');

    // Fire an SSE / morphBoard swap updating collapsed far topic-12 to rev-12-v2
    const updatedBoardHtml = Array.from({ length: 18 }, (_, idx) => {
      const i = idx + 1;
      if (i === 12) {
        return buildTopicHtml(12, {
          rev: 'rev-12-v2',
          titleSuffix: ' [SSE v2]',
          bodyNote: 'Fresh SSE revision v2 arrived while topic-12 was collapsed.',
        });
      }
      return buildTopicHtml(i, { withPty: i === 17 });
    }).join('\n');

    await page.evaluate(html => {
      window.__p4SwapBoard('h-v2', html);
    }, updatedBoardHtml);
    await waitFrames(page, 6);

    const stashedCheck = await page.evaluate(() => {
      const t12 = document.getElementById('topic-12');
      return {
        stillCollapsed: t12.hasAttribute('data-wb-virtual'),
        childCount: t12.children.length,
        hasStashed: window.WsBoardVirtual.hasStashed('topic-12'),
        headTitle: t12.querySelector('.tname').textContent,
        dataTh: t12.getAttribute('data-th'),
      };
    });
    assert.equal(stashedCheck.stillCollapsed, true, 'SSE update to far topic-12 does NOT mount its body');
    assert.equal(stashedCheck.childCount, 1, 'topic-12 still has only .tophead while collapsed');
    assert.equal(stashedCheck.hasStashed, true, 'latest revision is stashed in memory');
    assert.ok(stashedCheck.headTitle.includes('[SSE v2]'), '.tophead updated in place for Overview/dial fidelity');
    assert.equal(stashedCheck.dataTh, 'rev-12-v2');

    // Switch to light theme and jump back to topic-12 -> hydrates v2 + preserves details[open], loaded history row, and lazy gap!
    await page.evaluate(() => {
      document.documentElement.setAttribute('data-theme', 'light');
      window.WsBoardAnchor.select(document.getElementById('topic-12'), { smooth: false });
    });
    await waitFrames(page, 8);

    const rehydrated = await page.evaluate(() => {
      const t12 = document.getElementById('topic-12');
      const det = t12.querySelector('details[data-key="panel-12"]');
      const loadedRow = t12.querySelector('.timeline-row[data-row="t12-loaded-extra"]');
      const lazyGap = t12.querySelector('.history-gap[data-lazy="1"]');
      const revMarker = t12.querySelector('.rev-marker');
      return {
        collapsed: t12.hasAttribute('data-wb-virtual'),
        hasStashed: window.WsBoardVirtual.hasStashed('topic-12'),
        detailOpen: !!(det && det.open),
        hasLoadedRow: !!loadedRow,
        hasLazyGap: !!lazyGap,
        revText: revMarker ? revMarker.textContent : '',
      };
    });
    assert.equal(rehydrated.collapsed, false, 'topic-12 hydrated on jump');
    assert.equal(rehydrated.hasStashed, false, 'stashed revision consumed on hydrate');
    assert.equal(rehydrated.detailOpen, true, 'details[open] survived collapse + stashed SSE update + re-hydration');
    assert.equal(rehydrated.hasLoadedRow, true, 'loaded history row survived collapse + stashed SSE update + re-hydration');
    assert.equal(rehydrated.hasLazyGap, true, 'lazy history gap marker survived collapse + stashed SSE update + re-hydration');
    assert.ok(rehydrated.revText.includes('Fresh SSE revision v2'), 'hydrated body shows latest stashed server revision');

    if (SHOTS_DIR) {
      await page.screenshot({ path: path.join(SHOTS_DIR, '02-rehydrated-far-topic-preserved-light.png') });
    }

    // 4. Overview with collapsed far topics + live hydrated Quick Look of a far collapsed topic
    await page.evaluate(() => {
      document.documentElement.setAttribute('data-theme', 'dark');
      window.WsBoardAnchor.select(document.getElementById('topic-2'), { smooth: false });
    });
    await waitFrames(page, 6);
    await page.evaluate(() => { window.WsOverview.toggle(); });
    await page.waitForFunction(() => document.documentElement.hasAttribute('data-overview-preview'));
    await page.waitForTimeout(280);
    await waitFrames(page, 4);

    // Verify topic-15 is collapsed in Overview tile list with intact .tophead/.trecap, then Quick Look it
    const ovBefore = await page.evaluate(() => {
      const t15 = document.getElementById('topic-15');
      t15.scrollIntoView({ block: 'center' });
      return {
        collapsed: t15.hasAttribute('data-wb-virtual'),
        title: t15.querySelector('.tname').textContent,
        recap: t15.querySelector('.trecap .rtext').textContent,
      };
    });
    assert.equal(ovBefore.collapsed, true, 'far topic-15 stays collapsed in Overview tile grid');
    assert.ok(ovBefore.title.includes('Topic 15'), 'collapsed far topic Overview tile title intact');
    assert.ok(ovBefore.recap.length > 0, 'collapsed far topic Overview tile recap intact');

    // Hover topic-15 in Overview to trigger Quick Look preview (120ms debounce)
    await waitFrames(page, 2);
    const t15Box = await page.locator('#topic-15').boundingBox();
    assert.ok(t15Box && t15Box.height > 20, 'collapsed far topic-15 has visible Overview tile box');
    const hx = t15Box.x + Math.min(60, t15Box.width / 2);
    const hy = t15Box.y + Math.min(20, t15Box.height / 2);
    await page.mouse.move(hx - 10, hy);
    await page.mouse.move(hx, hy, { steps: 3 });
    try {
      await page.waitForFunction(() => document.querySelector('.overview-preview-stage > section.topic')?.getAttribute('data-topic') === 'topic-15', null, { timeout: 3000 });
    } catch (err) {
      const dbg = await page.evaluate(({ x, y }) => {
        const hit = document.elementFromPoint(x, y);
        const stage = document.querySelector('.overview-preview-stage');
        const t15 = document.getElementById('topic-15');
        return {
          hit: hit ? hit.tagName + '#' + hit.id + '.' + hit.className : null,
          stageChild: stage && stage.firstElementChild ? stage.firstElementChild.getAttribute('data-topic') : null,
          t15Rect: t15 ? t15.getBoundingClientRect() : null,
          overviewActive: window.WsOverview && window.WsOverview.active(),
          previewAttr: document.documentElement.hasAttribute('data-overview-preview'),
        };
      }, { x: hx, y: hy });
      throw new Error('Quick Look hover on topic-15 did not switch: ' + JSON.stringify(dbg) + ' pageErrors=' + JSON.stringify(pageErrors));
    }
    await waitFrames(page, 6);

    const ovQuickLook = await page.evaluate(() => {
      const livePreview = document.querySelector('.overview-preview-stage > section.topic[data-topic="topic-15"]');
      return {
        inStage: !!livePreview,
        collapsed: livePreview ? livePreview.hasAttribute('data-wb-virtual') : null,
        hasBody: !!(livePreview && livePreview.querySelector('.tbody')),
        hasFrame: !!(livePreview && livePreview.querySelector('iframe.doc-frame')),
      };
    });
    assert.equal(ovQuickLook.inStage, true, 'far topic-15 moved into Quick Look stage');
    assert.equal(ovQuickLook.collapsed, false, 'Quick Look immediately hydrates far collapsed topic');
    assert.equal(ovQuickLook.hasBody, true, 'Quick Look preview renders full live body');
    assert.equal(ovQuickLook.hasFrame, true, 'Quick Look mounts doc-frame inside previewed far topic');

    if (SHOTS_DIR) {
      await page.screenshot({ path: path.join(SHOTS_DIR, '03-overview-with-far-collapsed-topics-dark.png') });
    }

    // Exit Overview
    await page.keyboard.press('Escape');
    await waitFrames(page, 6);

    // 5. Dial scrub deferral + zero layout shift + zero idle rAF after settle
    const dialBox = await page.locator('.d-board-dial').boundingBox();
    assert.ok(dialBox && dialBox.height > 40, 'board dial is visible');
    const firstDot = await page.locator('.d-board-dial-dot').first().boundingBox();
    const lastDot = await page.locator('.d-board-dial-dot').last().boundingBox();
    await page.mouse.move(firstDot.x + firstDot.width / 2, firstDot.y + firstDot.height / 2);
    await page.mouse.down();
    await page.mouse.move(lastDot.x + lastDot.width / 2, lastDot.y + lastDot.height / 2, { steps: 8 });
    const duringScrub = await page.evaluate(() => ({
      scrubbing: window.WsBoardAnchor.scrubbing(),
    }));
    assert.equal(duringScrub.scrubbing, true, 'WsBoardAnchor.scrubbing() is true during active dial scrub');
    await page.mouse.up();
    await waitFrames(page, 8);

    const afterScrub = await page.evaluate(() => {
      const bcol = document.getElementById('bcol');
      const cur = document.querySelector('#r-board [data-current]');
      const stBefore = bcol.scrollTop;
      // Reconcile again — must cause 0px scrollTop shift
      window.WsBoardAnchor.reconcile();
      const stAfter = bcol.scrollTop;
      return {
        scrubbing: window.WsBoardAnchor.scrubbing(),
        curCollapsed: cur ? cur.hasAttribute('data-wb-virtual') : null,
        scrollShift: Math.abs(stAfter - stBefore),
      };
    });
    assert.equal(afterScrub.scrubbing, false, 'scrubbing ends on pointerup');
    assert.equal(afterScrub.curCollapsed, false, 'settled card hydrated immediately on scrub end');
    assert.equal(afterScrub.scrollShift, 0, 'zero layout shift / scrollTop jump on reconcile');

    // Verify zero idle rAF loops after settle
    const idleRafCount = await page.evaluate(() => new Promise(resolve => {
      let count = 0;
      const orig = window.requestAnimationFrame;
      window.requestAnimationFrame = function (cb) {
        count++;
        return orig.call(window, cb);
      };
      setTimeout(() => {
        window.requestAnimationFrame = orig;
        resolve(count);
      }, 320);
    }));
    assert.equal(idleRafCount, 0, `zero idle rAF callbacks after settle (got ${idleRafCount})`);

    // 6. Verify all 4 unpinned .bcard widgets + docked topic survived far scrolling with 0 eviction/reload
    await page.evaluate(() => {
      document.documentElement.setAttribute('data-theme', 'light');
      const termCard = document.querySelector('#r-board .bcard[data-kind="terminal"]');
      window.WsBoardAnchor.select(termCard, { smooth: false });
    });
    await waitFrames(page, 8);

    const exemptions = await page.evaluate(() => {
      const cards = Array.from(document.querySelectorAll('#r-board .bcard'));
      const webIframe = document.querySelector('#r-board .bcard[data-kind="web"] iframe[data-web-iframe]');
      const docked = document.querySelector('#bgal section.topic[data-topic="topic-18"]');
      return {
        cardCount: cards.length,
        identitiesIntact: cards.every((c, i) => c.__p4Identity === 'card-' + i),
        noneCollapsed: cards.every(c => !c.hasAttribute('data-wb-virtual')),
        webAlive: !!(webIframe && webIframe.contentWindow && webIframe.contentWindow.__p4Alive === 'never-reloaded'),
        dockedIntact: !!(docked && docked.__p4DockedIdentity === 'docked-18' && !docked.hasAttribute('data-wb-virtual')),
      };
    });
    assert.equal(exemptions.cardCount, 4);
    assert.equal(exemptions.identitiesIntact, true, 'all 4 unpinned .bcard DOM nodes kept exact identity');
    assert.equal(exemptions.noneCollapsed, true, 'no .bcard was ever collapsed');
    assert.equal(exemptions.webAlive, true, 'unpinned web .bcard iframe never reloaded across far scrolls');
    assert.equal(exemptions.dockedIntact, true, '#bgal-docked topic kept identity and stayed hydrated');

    if (SHOTS_DIR) {
      await page.screenshot({ path: path.join(SHOTS_DIR, '04-bcard-exempt-unpinned-widget-light.png') });
    }
  } finally {
    await browser.close();
  }
});
