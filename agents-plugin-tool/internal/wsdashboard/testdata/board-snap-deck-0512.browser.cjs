#!/usr/bin/env node
'use strict';

// Playwright Chromium regression tests for 0.51.2 release-gate Important findings:
// #8:  Folded frame-backed Quick Look preview preserves data-overview-live, unfolded
//      body visibility, typed draft value, active focus, and caret selection across
//      an independent SSE update to an unrelated row in the same topic.
// #9:  Conversation effect links hydrate collapsed far topics before target lookup
//      and lazy-history loadTarget so BOTH detached live stream rows AND detached
//      lazy history targets land with exact === true.
// #10: Board search indexes detached far topics (body + stream) AND stashed SSE
//      revisions on collapsed far topics without mutating live DOM, and selecting a
//      match hydrates the topic and jumps to the target.
// #11: Switching Quick Look from Topic A to Topic B after a held revision for Topic B
//      arrived in boardOverviewHeld immediately renders the held revision on Topic B
//      without waiting for another SSE event or Overview exit.
// #12: Steady-state anchorMark / step navigation performs strictly bounded O(1)
//      virtualDescribeNode work with zero virtualStore prunes.

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

function readAsset(dir, name) {
  return fs.readFileSync(path.join(dir, name), 'utf8');
}

function topicHexId(i) {
  // Valid hex topic ID matching /^topic-(?:[0-9a-f]{2})+$/ required by effectJump.
  return 'topic-' + String(i).padStart(2, '0') + 'aa';
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
  const tid = topicHexId(i);
  const rev = opts.rev || `rev-${i}-v1`;
  const bodyNote = opts.bodyNote || `BodyNote-${i}-${rev}`;
  const streamNote = opts.streamNote || `StreamNote-${i}-${rev}`;
  const docKey = `topic:${tid}`;
  const frameBlock = `<div class="tdoc"><div class="doc-body"><div class="doc-frame" data-doc="${docKey}" data-doc-key="${docKey}" data-sandbox="allow-scripts" data-srcdoc="${buildDocFrameSrcdoc(
    docKey,
    `Topic ${i} Spec (${rev})`,
    `<p>${bodyNote}</p><div style="height:120px">Framed spec body ${i}</div>`
  )}"></div></div></div>`;

  const askBlock = opts.withAsk
    ? `<div class="tbody"><div class="card policy" id="item-${i}01" data-id="${i}01">
        <div class="title">Ask question for Topic ${i}</div>
        <textarea class="note" placeholder="Write answer note"></textarea>
      </div></div>`
    : '';

  const lazyAttr = opts.lazyFetch ? ' data-lazy="1"' : '';
  const lazyPayload = opts.lazyFetch
    ? '<template data-history-gap="gap-' + i + '"></template>'
    : `<template data-history-gap="gap-${i}">
        <div class="timeline-row" data-row="t${i}-r0">
          <article class="wb-entry" id="e${i}00" data-entry="e${i}00"><span class="entry-text">ArchivedGapEntry-${i}</span></article>
        </div>
      </template>`;

  const historyBlock = `<div class="topic-stream"><div class="wb-history" data-history="${tid}">
    <div class="history-live">
      <div class="history-gap"${lazyAttr} data-gap-id="gap-${i}" data-count="1">
        <span class="gap-label">⋯ 1</span><span class="gap-range"></span><button type="button" data-load="down" hidden></button>
        ${lazyPayload}
      </div>
      <div class="timeline-row" data-row="t${i}-r2">
        <article class="wb-entry" id="e${i}02" data-entry="e${i}02"><span class="entry-text">${streamNote}</span></article>
      </div>
    </div>
  </div></div>`;

  return `<section class="topic" id="${tid}" data-topic="${tid}" data-th="${rev}" data-state="discussing" data-open-asks="${opts.withAsk ? 'true' : 'false'}">
    <div class="tophead">
      <div class="tbar">
        <span class="tdot"></span>
        <span class="tname">Topic ${i} (${rev})</span>
        <span class="tstate">discussing</span>
      </div>
      <div class="tsub"><div class="trecap"><div class="rtext">${bodyNote}</div></div></div>
      <textarea class="tnote" data-topic="${tid}" hidden></textarea>
    </div>
    ${frameBlock}
    ${askBlock}
    ${historyBlock}
  </section>`;
}

function buildFixtureHtml(topicCount = 14) {
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
    topics.push(buildTopicHtml(i, { withAsk: i === 1, lazyFetch: i === 13 }));
  }

  return `<!doctype html>
<html data-mode="live" data-theme="dark">
<head>
<meta charset="utf-8">
<style>
${css}
html, body { height: 100%; margin: 0; overflow: hidden; background: var(--wb-bg); color: var(--wb-fg); font-family: system-ui, sans-serif; }
.dwrap { height: 100vh; display: flex; flex-direction: column; }
.top { flex: none; height: 36px; display: flex; align-items: center; padding: 0 12px; }
.dbody { flex: 1 1 0; min-height: 0; }
#bcol { height: 100%; min-height: 0; }
section.topic { background: var(--wb-card); border-radius: 8px; margin-bottom: 16px; }
section.topic > .tophead > .tbar { display: flex; align-items: center; gap: 8px; padding: 8px 12px; min-height: 36px; }
iframe.doc-frame { width: 100%; border: 0; display: block; min-height: 60px; }
</style>
</head>
<body class="dash live">
<div hidden>${sprite}</div>
<div class="dwrap">
  <header class="top">ws dashboard — 0.51.2 regression harness</header>
  <div class="dbody">
    <aside id="rail" class="rail"></aside>
    <main id="grid" class="dgrid live" data-tab="board">
      <div class="convo" id="convo">
        <div id="r-entries" data-region="entries">
          <div class="wbupdate-topic" data-wb-topic="${topicHexId(12)}">
            <a href="#" id="link-live-12" data-wb-jump="e1202">Jump to live row e1202</a>
            <span class="wbupdate-status" id="status-12"></span>
          </div>
          <div class="wbupdate-topic" data-wb-topic="${topicHexId(13)}">
            <a href="#" id="link-lazy-13" data-wb-jump="e1300">Jump to lazy row e1300</a>
            <span class="wbupdate-status" id="status-13"></span>
          </div>
        </div>
      </div>
      <div id="bcol" class="bcol">
        <div id="r-board" class="r-board" data-region="board" data-h="h0">
          ${topics.join('\n')}
        </div>
      </div>
    </main>
  </div>
</div>
<script>
window.wsT = function (k) { return k; };
window.wbToast = function () {};
</script>
</body>
</html>`;
}

function buildBootstrapScript(savedCards) {
  const galleryJs = readAsset(WSD, 'gallery.js');
  const previewJs = readAsset(WSD, 'gal-rail-preview.js');
  const cardsJs = readAsset(WSD, 'cards.js');
  const overviewJs = readAsset(WSD, 'overview.js');
  const anchorJs = readAsset(WSD, 'dashboard-board-anchor.js');
  const dialJs = readAsset(WSD, 'dashboard-board-dial.js');
  const framesJs = readAsset(WSD, 'dashboard-frames.js');
  const boardJs = readAsset(WSD, 'dashboard-board.js');
  const convoJs = readAsset(WSD, 'dashboard-conversation.js');
  const trackerJs = readAsset(WSD, 'dashboard-tracker.js');
  const streamBundle = [
    readAsset(WSW, 'stream.js'),
    readAsset(WSW, 'stream-navigation.js'),
    readAsset(WSW, 'stream-view.js'),
    readAsset(WSW, 'stream-panels.js'),
    readAsset(WSW, 'stream-events.js'),
  ].join('\n');

  return [
    galleryJs,
    previewJs,
    cardsJs,
    overviewJs,
    streamBundle,
    `(function () {
      var doc = document, root = doc.documentElement, rid = "t0512", mode = "live", live = true, worktree = "", layoutMode = "", framed = false, shell = false;
      var shHidden = false, sseOn = true, cchips = null, ctext = null, mpend = null, tkApi = null, slash = null, railGate = null;
      var filesSheet = null, filesTab = null, fpane = null, chead = null, convo = $("convo"), grid = $("grid");
      var NEAR = "100%", FAR = "400%", mountList = [], mountTask = false, topicNear = {}, topicRO = null;
      var TL_REGIONS = {}, tlHeld = {}, LAYOUT_KEY = "ws.layout", cardIdentity = "t0512", shStage = null;
      var headHeld = null, railMinGen = 0, railRendered = null, railHoverUpdates = null;
      var TAB = { board: "board", entries: "convo", convlive: "convo" };
      function $(id) { return doc.getElementById(id); }
      function q(s, box) { return Array.prototype.slice.call((box || doc).querySelectorAll(s)); }
      function region(name) { return doc.querySelector('[data-region="' + name + '"]'); }
      function entriesBox() { return region("entries"); }
      function boardBoxes() { return [region("board"), $("bgal")].filter(Boolean); }
      function boardQ(s) { return boardBoxes().reduce(function (a, b) { return a.concat(q(s, b)); }, []); }
      function boardRead(fn) { return boardBoxes().reduce(function (a, b) { return Object.assign(a, fn(b)); }, {}); }
      function inBoard(n) { return boardBoxes().some(function (b) { return b.contains(n); }); }
      function frag(html) { var t = doc.createElement("template"); t.innerHTML = html; return t.content; }
      function wsT(k) { return window.wsT(k); }
      function toast() {}
      function load(k) { return k === "ws.dashboard.cards.t0512" ? ${JSON.stringify(JSON.stringify(savedCards || { topics: [], cards: [] }))} : null; }
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
      function loadClosed() { return Promise.resolve(false); }
      function readItem() { return null; }
      function pickKey(b) { return (b.getAttribute("data-opt") || "") + "|" + (b.getAttribute("data-v") || ""); }
      function readAnswers(el) {
        var out = {};
        if (!el || !el.querySelectorAll) return out;
        q(".card[data-id], .arow[data-id], .ckrow[data-id], .srow[data-id]", el).forEach(function (item) {
          var s = {}, note = item.querySelector("textarea.note");
          if (note && note.value) s.note = note.value;
          if (note && !note.hidden) s.noteOpen = true;
          if (s.note || s.noteOpen) out[item.getAttribute("data-id")] = s;
        });
        q(".tnote[data-topic]", el).forEach(function (note) {
          if (note.value || !note.hidden) out["t:" + note.getAttribute("data-topic")] = { note: note.value, noteOpen: !note.hidden };
        });
        return out;
      }
      function writeAnswers(el, saved) {
        if (!el || !saved || !el.querySelectorAll) return;
        q(".card[data-id], .arow[data-id], .ckrow[data-id], .srow[data-id]", el).forEach(function (item) {
          var s = saved[item.getAttribute("data-id")];
          if (!s) return;
          var note = item.querySelector("textarea.note");
          if (note && (s.note || s.noteOpen)) { note.value = s.note || ""; note.hidden = false; }
        });
        q(".tnote[data-topic]", el).forEach(function (note) {
          var s = saved["t:" + note.getAttribute("data-topic")];
          if (!s) return;
          note.value = s.note || "";
          if (s.noteOpen) note.hidden = false;
        });
      }
      function shParentDoc() { return doc; }
      function go() {}
      function postDoc(f, msg) { try { if (f && f.contentWindow) f.contentWindow.postMessage(msg, "*"); } catch (e) {} }
      function postTheme() {}
      function dark() { return root.getAttribute("data-theme") === "dark"; }
      ${anchorJs}
      ${dialJs}
      ${framesJs}
      ${boardJs}
      ${convoJs.slice(convoJs.indexOf('var effectJumpEpoch = 0;'), convoJs.indexOf('var ACT_CONFIRM_MS = 1600;'))}
      ${trackerJs.slice(trackerJs.indexOf('function paletteText(el)'), trackerJs.indexOf('function paletteFs(route,'))}
      mountBoardOverview();
      anchorInit();
      window.__testApi = {
        swapBoard: function (hash, html) { swap("board", hash, html, true); },
        paletteRows: paletteRows,
        palettePick: palettePick,
        effectJump: effectJump,
        hasOverviewHeld: function () { return !!boardOverviewHeld; },
      };
    })();`,
  ].join('\n');
}

async function openHarness(browser, opts = {}) {
  const ctx = await browser.newContext({ viewport: { width: 1720, height: 960 } });
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push(String(e)));
  await page.setContent(buildFixtureHtml(opts.topicCount || 14), { waitUntil: 'domcontentloaded' });
  await page.addScriptTag({ content: buildBootstrapScript(opts.savedCards) });
  await page.waitForTimeout(80);
  if (errors.length) throw new Error('Bootstrap pageerror: ' + errors.join('\n'));
  return { ctx, page, errors };
}

test('0.51.2 release-gate findings #8, #9, #10, #11, #12 in Chromium', async (t) => {
  const browser = await chromium.launch({ headless: true });
  t.after(async () => { await browser.close(); });

  await t.test('Finding #8: folded frame-backed Quick Look preview keeps data-overview-live, visibility, value, focus, and caret across SSE update', async () => {
    const savedCards = { topics: [{ n: topicHexId(1), f: true }], cards: [] };
    const { ctx, page, errors } = await openHarness(browser, { topicCount: 6, savedCards });
    t.after(async () => { await ctx.close(); });

    // Enter Overview and Quick Look folded Topic 1.
    await page.evaluate((tid) => {
      window.WsCards.decorate();
      window.WsOverview.toggle();
      const t1 = document.getElementById(tid);
      // Trigger Quick Look preview by hovering/clicking preview on topic 1.
      t1.dispatchEvent(new MouseEvent('pointerenter', { bubbles: true }));
    }, topicHexId(1));
    await page.waitForTimeout(350);

    // Ensure preview is active on Topic 1; if hover timer didn't fire in headless, trigger mouseover/focus.
    const isPreviewing = await page.evaluate((tid) => {
      const n = window.WsOverview.previewNode();
      return !!(n && n.id === tid);
    }, topicHexId(1));
    if (!isPreviewing) {
      const box = await page.locator(`#${topicHexId(1)}`).boundingBox();
      await page.mouse.move(box.x + box.width / 2, box.y + 16);
      await page.waitForTimeout(350);
    }

    // Focus the ask textarea inside the folded, frame-backed preview, type, and place caret mid-text.
    const before = await page.evaluate((tid) => {
      const live = window.WsOverview.previewNode();
      if (!live || live.id !== tid) return { ok: false, reason: 'not previewing' };
      const ta = live.querySelector('textarea.note');
      ta.focus();
      ta.value = 'hello world draft';
      ta.setSelectionRange(5, 5);
      return {
        ok: true,
        hasOverviewLive: live.hasAttribute('data-overview-live'),
        sizeAttr: live.getAttribute('data-size'),
        bodyHeight: live.querySelector('.tbody').getBoundingClientRect().height,
        hasIframe: !!live.querySelector('iframe.doc-frame'),
      };
    }, topicHexId(1));
    assert.equal(before.ok, true, 'Topic 1 must be active in Quick Look preview');
    assert.equal(before.hasOverviewLive, true, 'data-overview-live set before SSE');
    assert.equal(before.sizeAttr, 'fold', 'Topic 1 has persisted data-size="fold"');
    assert.ok(before.bodyHeight > 0, 'Quick Look body visible despite data-size="fold"');

    // Emit an independent SSE update to an unrelated stream row in Topic 1 (frame-graftable!).
    const updatedBoardHtml = Array.from({ length: 6 }, (_, idx) =>
      buildTopicHtml(idx + 1, {
        withAsk: idx === 0,
        rev: idx === 0 ? 'rev-1-v2' : `rev-${idx + 1}-v1`,
        streamNote: idx === 0 ? 'IndependentUnrelatedRowUpdate-v2' : undefined,
      })
    ).join('\n');

    const after = await page.evaluate((html) => {
      window.__testApi.swapBoard('h-sse-8', html);
      const live = window.WsOverview.previewNode();
      const ta = live && live.querySelector('textarea.note');
      return {
        hasOverviewLive: !!(live && live.hasAttribute('data-overview-live')),
        bodyHeight: live ? live.querySelector('.tbody').getBoundingClientRect().height : 0,
        streamText: live ? live.querySelector('#e102').textContent : '',
        value: ta ? ta.value : '',
        focused: document.activeElement === ta,
        start: ta ? ta.selectionStart : -1,
        end: ta ? ta.selectionEnd : -1,
      };
    }, updatedBoardHtml);

    assert.equal(after.hasOverviewLive, true, 'data-overview-live must survive frameGraft + WsCards.decorate');
    assert.ok(after.bodyHeight > 0, 'Quick Look preview body must stay visible (not refolded)');
    assert.equal(after.streamText, 'IndependentUnrelatedRowUpdate-v2', 'SSE update applied to unrelated row');
    assert.equal(after.value, 'hello world draft', 'typed draft value preserved');
    assert.equal(after.focused, true, 'active focus preserved on ask textarea');
    assert.equal(after.start, 5, 'caret selectionStart preserved');
    assert.equal(after.end, 5, 'caret selectionEnd preserved');
    assert.deepEqual(errors, []);
  });

  await t.test('Finding #9: conversation effect links hydrate collapsed far topics for both live stream rows and lazy history targets with exact === true', async () => {
    const { ctx, page, errors } = await openHarness(browser, { topicCount: 14 });
    t.after(async () => { await ctx.close(); });

    // Confirm far topics 12 and 13 are collapsed.
    const collapsedBefore = await page.evaluate(([t12, t13]) => ({
      c12: document.getElementById(t12).hasAttribute('data-wb-virtual'),
      c13: document.getElementById(t13).hasAttribute('data-wb-virtual'),
    }), [topicHexId(12), topicHexId(13)]);
    assert.equal(collapsedBefore.c12, true);
    assert.equal(collapsedBefore.c13, true);

    // (a) Click conversation effect link targeting detached live stream row e1202 in collapsed topic 12.
    const liveRes = await page.evaluate((t12) => new Promise((resolve) => {
      window.__testApi.effectJump(t12, 'e1202', (found, exact) => {
        resolve({
          found,
          exact,
          collapsed: document.getElementById(t12).hasAttribute('data-wb-virtual'),
          activeId: document.activeElement ? document.activeElement.id : '',
        });
      });
    }), topicHexId(12));
    assert.equal(liveRes.found, true);
    assert.equal(liveRes.exact, true, 'detached live stream row must land with exact === true');
    assert.equal(liveRes.collapsed, false, 'topic 12 hydrated');
    assert.equal(liveRes.activeId, 'e1202', 'exact stream entry focused');

    // Navigate back to Topic 1 so Topic 13 is collapsed again, and mock wbHistoryFetch for lazy topic 13.
    const fullTopic13Html = buildTopicHtml(13, { lazyFetch: false });
    const lazyRes = await page.evaluate(([t1, t13, html13]) => {
      window.WsBoardAnchor.select(document.getElementById(t1), { smooth: false });
      const wasCollapsed = document.getElementById(t13).hasAttribute('data-wb-virtual');
      window.wbHistoryFetch = function () {
        return Promise.resolve([{ name: t13, html: html13 }]);
      };
      return new Promise((resolve) => {
        window.__testApi.effectJump(t13, 'e1300', (found, exact) => {
          resolve({
            wasCollapsed,
            found,
            exact,
            collapsedAfter: document.getElementById(t13).hasAttribute('data-wb-virtual'),
            activeId: document.activeElement ? document.activeElement.id : '',
          });
        });
      });
    }, [topicHexId(1), topicHexId(13), fullTopic13Html]);

    assert.equal(lazyRes.wasCollapsed, true, 'topic 13 was collapsed before jump');
    assert.equal(lazyRes.found, true);
    assert.equal(lazyRes.exact, true, 'detached lazy history target must land with exact === true');
    assert.equal(lazyRes.collapsedAfter, false, 'topic 13 hydrated');
    assert.equal(lazyRes.activeId, 'e1300', 'exact lazy history entry focused');
    assert.deepEqual(errors, []);
  });

  await t.test('Finding #10: board search indexes detached far topics and stashed SSE updates, and picking hydrates and jumps', async () => {
    const { ctx, page, errors } = await openHarness(browser, { topicCount: 14 });
    t.after(async () => { await ctx.close(); });

    // (a) Verify collapsed far topic 11's body & stream are searchable without hydrating it.
    const search1 = await page.evaluate((t11) => {
      const sec = document.getElementById(t11);
      const collapsedBefore = sec.hasAttribute('data-wb-virtual');
      const rows = window.__testApi.paletteRows();
      const collapsedAfter = sec.hasAttribute('data-wb-virtual');
      return {
        collapsedBefore,
        collapsedAfter,
        bodyHit: rows.some(r => r.topic === t11 && r.kind === 'board-body' && r.title.includes('BodyNote-11-rev-11-v1')),
        entryHit: rows.some(r => r.topic === t11 && r.kind === 'board-entry' && r.entry === 'e1102' && r.title.includes('StreamNote-11-rev-11-v1')),
      };
    }, topicHexId(11));
    assert.equal(search1.collapsedBefore, true);
    assert.equal(search1.collapsedAfter, true, 'paletteRows must not mutate/hydrate collapsed topics');
    assert.equal(search1.bodyHit, true, 'detached far topic body indexed by board search');
    assert.equal(search1.entryHit, true, 'detached far topic stream entry indexed by board search');

    // (b) Emit a stashed SSE update to collapsed far topic 12 with a brand-new unique phrase.
    const updatedBoardHtml = Array.from({ length: 14 }, (_, idx) =>
      buildTopicHtml(idx + 1, {
        withAsk: idx === 0,
        lazyFetch: idx === 12,
        rev: idx === 11 ? 'rev-12-stashed' : `rev-${idx + 1}-v1`,
        streamNote: idx === 11 ? 'UniqueStashedPhraseXYZ-12' : undefined,
      })
    ).join('\n');

    const search2 = await page.evaluate(([t12, html]) => {
      window.__testApi.swapBoard('h-sse-10', html);
      const sec = document.getElementById(t12);
      const stillCollapsed = sec.hasAttribute('data-wb-virtual');
      const hasStashed = window.WsBoardVirtual.hasStashed(t12);
      const rows = window.__testApi.paletteRows();
      const match = rows.find(r => r.topic === t12 && r.kind === 'board-entry' && r.title.includes('UniqueStashedPhraseXYZ-12'));
      if (match) window.__testApi.palettePick(match, {});
      return {
        stillCollapsed,
        hasStashed,
        matched: !!match,
        collapsedAfterPick: sec.hasAttribute('data-wb-virtual'),
        activeId: document.activeElement ? document.activeElement.id : '',
        entryText: document.getElementById('e1202') ? document.getElementById('e1202').textContent : '',
      };
    }, [topicHexId(12), updatedBoardHtml]);

    assert.equal(search2.stillCollapsed, true, 'far topic 12 stays collapsed on SSE update');
    assert.equal(search2.hasStashed, true, 'SSE update stashed in virtualStore');
    assert.equal(search2.matched, true, 'board search finds stashed SSE phrase on collapsed far topic');
    assert.equal(search2.collapsedAfterPick, false, 'selecting search match hydrates collapsed topic');
    assert.equal(search2.activeId, 'e1202', 'selecting search match focuses the matched entry');
    assert.equal(search2.entryText, 'UniqueStashedPhraseXYZ-12', 'hydrated topic displays stashed SSE content');
    assert.deepEqual(errors, []);
  });

  await t.test('Finding #11: switching Quick Look from Topic A to Topic B immediately applies boardOverviewHeld to Topic B', async () => {
    const { ctx, page, errors } = await openHarness(browser, { topicCount: 6 });
    t.after(async () => { await ctx.close(); });

    // Open Overview and Quick Look Topic 1.
    await page.evaluate(() => { window.WsOverview.toggle(); });
    await page.waitForTimeout(100);
    const box1 = await page.locator(`#${topicHexId(1)}`).boundingBox();
    await page.mouse.move(box1.x + box1.width / 2, box1.y + 16);
    await page.waitForTimeout(350);

    // Emit SSE update changing Topic 2 while Topic 1 is previewed (held in boardOverviewHeld).
    const updatedBoardHtml = Array.from({ length: 6 }, (_, idx) =>
      buildTopicHtml(idx + 1, {
        withAsk: idx === 0,
        rev: idx === 1 ? 'rev-2-held-v2' : `rev-${idx + 1}-v1`,
        streamNote: idx === 1 ? 'HeldRevisionForTopic2-v2' : undefined,
      })
    ).join('\n');

    const heldState = await page.evaluate(([t1, html]) => {
      window.__testApi.swapBoard('h-sse-11', html);
      return {
        previewId: window.WsOverview.previewNode() ? window.WsOverview.previewNode().id : null,
        hasHeld: window.__testApi.hasOverviewHeld(),
      };
    }, [topicHexId(1), updatedBoardHtml]);
    assert.equal(heldState.previewId, topicHexId(1));
    assert.equal(heldState.hasHeld, true, 'SSE update held in boardOverviewHeld');

    // Switch Quick Look to Topic 2 WITHOUT emitting any further board update.
    const box2 = await page.locator(`#${topicHexId(2)}`).boundingBox();
    await page.mouse.move(box2.x + box2.width / 2, box2.y + 16);
    await page.waitForTimeout(350);

    const switched = await page.evaluate((t2) => {
      const live = window.WsOverview.previewNode();
      return {
        previewId: live ? live.id : null,
        hasHeld: window.__testApi.hasOverviewHeld(),
        rev: live ? live.getAttribute('data-th') : '',
        streamText: live && live.querySelector('#e202') ? live.querySelector('#e202').textContent : '',
      };
    }, topicHexId(2));

    assert.deepEqual(errors, []);
    assert.equal(switched.previewId, topicHexId(2), 'Quick Look switched to Topic 2');
    assert.equal(switched.hasHeld, true, 'boardOverviewHeld stays held for Overview tile list / other topics');
    assert.equal(switched.rev, 'rev-2-held-v2', 'Topic 2 immediately reflects held revision data-th');
    assert.equal(switched.streamText, 'HeldRevisionForTopic2-v2', 'Topic 2 immediately renders held stream content');
    assert.deepEqual(errors, []);
  });

  await t.test('Finding #12: steady-state step navigation in Chromium is O(1) with zero prunes across board sizes', async () => {
    async function measure(topicCount) {
      const { ctx, page, errors } = await openHarness(browser, { topicCount });
      try {
        const stats = await page.evaluate(() => {
          window.WsBoardVirtual.resetStats();
          for (let i = 0; i < 6; i++) window.WsBoardAnchor.step(1);
          return window.WsBoardVirtual.stats();
        });
        assert.deepEqual(errors, []);
        return stats;
      } finally {
        await ctx.close();
      }
    }

    const s12 = await measure(12);
    const s24 = await measure(24);
    assert.equal(s12.prunes, 0, 'zero prunes during steady-state step navigation (N=12)');
    assert.equal(s24.prunes, 0, 'zero prunes during steady-state step navigation (N=24)');
    assert.equal(s12.fullReconciles, 0, 'zero full reconciles during steady-state step navigation');
    assert.equal(s24.fullReconciles, 0, 'zero full reconciles during steady-state step navigation');
    assert.equal(s12.deltaReconciles, 6);
    assert.equal(s24.deltaReconciles, 6);
    assert.equal(s12.describes, s24.describes, `describes must be invariant across N=12 (${s12.describes}) and N=24 (${s24.describes})`);
    assert.ok(s24.describes <= 6, `expected <= 6 total describes across 6 steps, got ${s24.describes}`);
  });
});
