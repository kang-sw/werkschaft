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
        <div class="head"><span class="id">#${i}01</span><span class="title">Ask question for Topic ${i}</span></div>
        <textarea class="note" placeholder="Write answer note"></textarea>
      </div></div>`
    : '';

  const lazyAttr = opts.lazyFetch ? ' data-lazy' : '';
  const gap1Payload = opts.lazyFetch
    ? '<template data-history-gap></template>'
    : `<template data-history-gap>
        <div class="timeline-row" data-row="t${i}-r0">
          <article class="wb-entry" id="e${i}00" data-entry="e${i}00"><span class="entry-text">ArchivedGap1Phrase-${i}</span></article>
        </div>
      </template>`;
  const gap2Block = opts.twoGaps
    ? `<div class="timeline-row" data-row="t${i}-r1">
        <article class="wb-entry" id="e${i}01" data-entry="e${i}01"><span class="entry-text">LiveMiddleRow-${i}</span></article>
      </div>
      <div class="history-gap"${lazyAttr} data-count="1" data-from="200" data-to="200">
        <span class="gap-label">⋯ 1</span><span class="gap-range"></span><span class="gap-actions"><button type="button" data-load="down" hidden></button></span>
        ${opts.lazyFetch
          ? '<template data-history-gap></template>'
          : `<template data-history-gap>
              <div class="timeline-row" data-row="t${i}-r3">
                <article class="wb-entry" id="e${i}04" data-entry="e${i}04"><span class="entry-text">ArchivedGap2Phrase-${i}</span></article>
              </div>
            </template>`}
      </div>`
    : '';

  const historyBlock = `<div class="topic-stream"><div class="wb-history" data-history="${tid}">
    <div class="history-live">
      <div class="history-gap"${lazyAttr} data-count="1" data-from="100" data-to="100">
        <span class="gap-label">⋯ 1</span><span class="gap-range"></span><span class="gap-actions"><button type="button" data-load="down" hidden></button></span>
        ${gap1Payload}
      </div>
      ${gap2Block}
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
    topics.push(buildTopicHtml(i, { withAsk: i === 1, lazyFetch: i === 13, twoGaps: i === 13 }));
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
  const composerStatusJs = readAsset(WSD, 'dashboard-composer-status.js');
  const composerJs = readAsset(WSD, 'dashboard-composer.js');
  const boardItemMatch = composerJs.match(/\n  function boardItem\(id\) \{[\s\S]*?\n  \}/);
  const applySentMatch = composerStatusJs.match(/\n  var sentSig = null;[\s\S]*?\n  function applySent\(force\) \{[\s\S]*?\n  \}/);
  assert.ok(boardItemMatch, 'production boardItem found');
  assert.ok(applySentMatch, 'production applySent found');
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
      function shellMemo() { return null; }
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
      ${boardItemMatch[0]}
      ${applySentMatch[0]}
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
        paletteHistory: paletteHistory,
        palettePick: palettePick,
        applySent: applySent,
        effectJump: effectJump,
        hasOverviewHeld: function () { return !!boardOverviewHeld; },
      };
    })();`,
  ].join('\n');
}

async function openHarness(browser, opts = {}) {
  // Band 4 (true 4-column, >=2441px) so Overview Quick Look preview is active.
  const ctx = await browser.newContext({ viewport: opts.viewport || { width: 2560, height: 960 } });
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
    const fullTopic13Html = buildTopicHtml(13, { lazyFetch: false, twoGaps: true });
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

  await t.test('0.51.3 Important #1 (strategy-driller-graceless): detached two-gap lazy-history search matches production markup, recovers after failed fetch, returns distinct gap 1 & gap 2 hits, and lands accurately on gap 2', async () => {
    const { ctx, page, errors } = await openHarness(browser, { topicCount: 14 });
    t.after(async () => { await ctx.close(); });

    const fullTopic13Html = buildTopicHtml(13, { lazyFetch: false, twoGaps: true });
    const t13 = topicHexId(13);

    // 1. Confirm collapsed Topic 13 has two production-shaped lazy gaps (data-count/data-from/data-to, NO data-gap-id).
    const beforeState = await page.evaluate((tid) => {
      const sec = document.getElementById(tid);
      const roots = window.WsBoardVirtual.searchRoots(tid);
      const gaps = [];
      roots.forEach(r => r.querySelectorAll('.history-gap[data-lazy]').forEach(g => gaps.push({
        hasGapId: g.hasAttribute('data-gap-id'),
        count: g.getAttribute('data-count'),
        from: g.getAttribute('data-from'),
        to: g.getAttribute('data-to'),
      })));
      return { collapsed: sec.hasAttribute('data-wb-virtual'), gaps };
    }, t13);
    assert.equal(beforeState.collapsed, true, 'topic 13 starts collapsed');
    assert.equal(beforeState.gaps.length, 2, 'topic 13 has two detached lazy gaps');
    assert.equal(beforeState.gaps[0].hasGapId, false, 'production markup has no data-gap-id');
    assert.equal(beforeState.gaps[1].hasGapId, false, 'production markup has no data-gap-id');
    assert.equal(beforeState.gaps[0].from, '100');
    assert.equal(beforeState.gaps[1].from, '200');

    // 2. First fetch rejects -> verify data-lazy is NOT stripped and retry is NOT poisoned.
    const afterFail = await page.evaluate((tid) => {
      let attempts = 0;
      window.__failFetchCount = 0;
      window.wbHistoryFetch = function () {
        attempts++;
        window.__failFetchCount = attempts;
        return Promise.reject(new Error('simulated transient network error'));
      };
      return new Promise((resolve) => {
        let updated = false;
        window.__testApi.paletteHistory(() => { updated = true; });
        setTimeout(() => {
          const roots = window.WsBoardVirtual.searchRoots(tid);
          let lazyLeft = 0;
          roots.forEach(r => { lazyLeft += r.querySelectorAll('.history-gap[data-lazy]').length; });
          resolve({
            attempts: window.__failFetchCount,
            updated,
            lazyLeft,
            collapsed: document.getElementById(tid).hasAttribute('data-wb-virtual'),
          });
        }, 60);
      });
    }, t13);
    assert.equal(afterFail.attempts, 1, 'initial fetch attempted once');
    assert.equal(afterFail.updated, false, 'failed fetch does not fire palette update');
    assert.equal(afterFail.lazyLeft, 2, 'failed fetch must NOT strip data-lazy from detached gaps');
    assert.equal(afterFail.collapsed, true, 'topic 13 remains collapsed after failed fetch');

    // 3. Retry succeeds -> verify distinct hits in BOTH gap 1 and gap 2 without duplicating gap 1 into gap 2.
    const afterRetry = await page.evaluate(([tid, html13]) => {
      let retryCalls = 0;
      window.wbHistoryFetch = function (names) {
        retryCalls++;
        return Promise.resolve([{ name: tid, html: html13 }]);
      };
      return new Promise((resolve) => {
        window.__testApi.paletteHistory((rows) => {
          const t13Rows = rows.filter(r => r.topic === tid && r.kind === 'board-entry');
          const gap1 = t13Rows.find(r => r.entry === 'e1300');
          const gap2 = t13Rows.find(r => r.entry === 'e1304');
          const roots = window.WsBoardVirtual.searchRoots(tid);
          let lazyLeft = 0;
          roots.forEach(r => { lazyLeft += r.querySelectorAll('.history-gap[data-lazy]').length; });
          window.__gap2Match = gap2 || null;
          window.__gap1Match = gap1 || null;
          resolve({
            retryCalls,
            lazyLeft,
            collapsed: document.getElementById(tid).hasAttribute('data-wb-virtual'),
            gap1Title: gap1 ? gap1.title : '',
            gap2Title: gap2 ? gap2.title : '',
            gap1Count: t13Rows.filter(r => r.title.includes('ArchivedGap1Phrase-13')).length,
            gap2Count: t13Rows.filter(r => r.title.includes('ArchivedGap2Phrase-13')).length,
          });
        });
      });
    }, [t13, fullTopic13Html]);

    assert.equal(afterRetry.retryCalls, 1, 'retry fetch executed cleanly after prior failure');
    assert.equal(afterRetry.lazyLeft, 0, 'both detached lazy gaps hydrated on retry');
    assert.equal(afterRetry.collapsed, true, 'detached lazy-history search must NOT hydrate live DOM');
    assert.ok(afterRetry.gap1Title.includes('ArchivedGap1Phrase-13'), `gap 1 hit present, got "${afterRetry.gap1Title}"`);
    assert.ok(afterRetry.gap2Title.includes('ArchivedGap2Phrase-13'), `gap 2 hit present, got "${afterRetry.gap2Title}"`);
    assert.equal(afterRetry.gap1Count, 1, 'gap 1 entry appears exactly once (not duplicated into gap 2)');
    assert.equal(afterRetry.gap2Count, 1, 'gap 2 entry appears exactly once');

    if (process.env.WS_LOOK_SHOTS) {
      const shotDir = path.join(os.tmpdir(), 'snap-deck-0513-followups-shots');
      fs.mkdirSync(shotDir, { recursive: true });
      await page.evaluate(() => {
        const g1 = window.__gap1Match, g2 = window.__gap2Match;
        const overlay = document.createElement('div');
        overlay.id = '__shot_palette';
        overlay.style.cssText = 'position:fixed;inset:0;background:rgba(15,23,42,0.55);display:flex;align-items:flex-start;justify-content:center;padding-top:72px;z-index:9999;';
        overlay.innerHTML = `<div style="width:640px;background:#1e293b;border:1px solid #475569;border-radius:10px;box-shadow:0 20px 50px rgba(0,0,0,0.45);overflow:hidden;color:#f8fafc;font:13px/1.5 system-ui,sans-serif;">
          <div style="padding:12px 16px;border-bottom:1px solid #334155;font-weight:600;">Search Board: <span style="color:#38bdf8;">ArchivedGap</span> (detached 2-gap lazy history after retry)</div>
          <div style="padding:8px;">
            <div style="padding:8px 12px;border-radius:6px;margin-bottom:4px;background:#0f172a;">
              <div style="font-size:11px;color:#94a3b8;">Topic 13 · Gap 1 (data-from=100, data-to=100) · entry ${g1.entry}</div>
              <div style="color:#e2e8f0;">${g1.title}</div>
            </div>
            <div style="padding:8px 12px;border-radius:6px;background:#1d4ed8;">
              <div style="font-size:11px;color:#bfdbfe;">Topic 13 · Gap 2 (data-from=200, data-to=200) · entry ${g2.entry} [selected]</div>
              <div style="color:#ffffff;font-weight:600;">${g2.title}</div>
            </div>
          </div>
        </div>`;
        document.body.appendChild(overlay);
      });
      await page.screenshot({ path: path.join(shotDir, '01-detached-two-gap-search-palette.png') });
      await page.evaluate(() => {
        const el = document.getElementById('__shot_palette');
        if (el) el.remove();
      });
    }

    // 4. Selecting the gap-2 hit hydrates Topic 13, materializes gap 2 into live DOM, and focuses e1304 accurately.
    const afterPick = await page.evaluate((tid) => {
      const match = window.__gap2Match;
      window.__testApi.palettePick(match, {});
      const sec = document.getElementById(tid);
      const target = document.getElementById('e1304');
      return {
        collapsedAfterPick: sec.hasAttribute('data-wb-virtual'),
        inLive: !!(target && target.closest('.history-live')),
        activeId: document.activeElement ? document.activeElement.id : '',
        entryText: target ? target.textContent : '',
      };
    }, t13);
    assert.equal(afterPick.collapsedAfterPick, false, 'selecting gap-2 hit hydrates collapsed topic 13');
    assert.equal(afterPick.inLive, true, 'gap-2 entry materialized into .history-live');
    assert.equal(afterPick.activeId, 'e1304', 'gap-2 entry focused accurately');
    assert.ok(afterPick.entryText.includes('ArchivedGap2Phrase-13'), 'gap-2 entry text rendered');

    // Also verify exact === true through loadTarget / effectJump for gap 2.
    const exactCheck = await page.evaluate((tid) => new Promise((resolve) => {
      window.__testApi.effectJump(tid, 'e1304', (found, exact) => resolve({ found, exact }));
    }), t13);
    assert.equal(exactCheck.found, true);
    assert.equal(exactCheck.exact, true, 'gap-2 target lands with exact === true');

    if (process.env.WS_LOOK_SHOTS) {
      await page.waitForTimeout(80);
      await page.screenshot({ path: path.join(os.tmpdir(), 'snap-deck-0513-followups-shots', '02-detached-gap2-selected-landed.png') });
    }
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

  await t.test('Finding #12: Designated-Y step navigation measures a bounded window across board sizes', async () => {
    async function measure(topicCount) {
      const { ctx, page, errors } = await openHarness(browser, { topicCount });
      try {
        const stats = await page.evaluate(() => {
          const { get: readHeight } = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight');
          let topicMeasures = 0;
          Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
            configurable: true,
            get() {
              if (this.matches('#r-board section.topic[data-topic]')) topicMeasures++;
              return readHeight.call(this);
            },
          });
          const anchor = window.WsBoardAnchor;
          const keys = [];
          window.WsBoardVirtual.resetStats();
          try {
            for (let i = 0; i < 6; i++) {
              if (!anchor.step(1)) throw new Error('step must move to the next card');
              keys.push(anchor.key());
            }
          } finally {
            Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { get: readHeight });
          }
          const { active, start, end } = anchor.windowInfo();
          return { ...window.WsBoardVirtual.stats(), keys, active, mounted: end - start + 1, topicMeasures };
        });
        assert.deepEqual(errors, []);
        return stats;
      } finally {
        await ctx.close();
      }
    }

    const s24 = await measure(24);
    const s48 = await measure(48);
    for (const stat of [s24, s48]) {
      assert.equal(stat.active, true, 'the browser uses Designated-Y');
      assert.equal(new Set(stat.keys).size, 6, 'all six steps reach distinct cards');
      assert.equal(stat.prunes, 0, 'steady-state navigation never prunes the full board');
      assert.equal(stat.fullReconciles, 0, 'steady-state navigation never reconciles the full board');
      assert.ok(stat.mounted <= 17, 'at most eight cards on either side of the anchor are mounted');
      assert.ok(stat.topicMeasures > 0 && stat.topicMeasures <= 6 * 17, 'steps measure only their mounted cards');
    }
    assert.deepEqual(s24.keys, s48.keys);
    assert.equal(s24.topicMeasures, s48.topicMeasures, 'measurement count is independent of board size');
  });

  await t.test('Important #4 (Chromium): entering SSE-updated stashed topic stays O(1) across board sizes (N=24 vs N=36) and far exempt draft counts (2 vs 12)', async () => {
    async function measureStashedEntry(topicCount, farExemptCount, sentRowCount = 10) {
      const { ctx, page, errors } = await openHarness(browser, { topicCount });
      try {
        const targetIdx = 10;
        const itemId = `${targetIdx}01`;
        const targetId = topicHexId(targetIdx);
        const initialHtml = Array.from({ length: topicCount }, (_, idx) => {
          const i = idx + 1;
          return buildTopicHtml(i, { withAsk: i === 1, rev: `rev-${i}-v1` });
        }).join('\n');
        const updatedHtml = Array.from({ length: topicCount }, (_, idx) => {
          const i = idx + 1;
          return buildTopicHtml(i, {
            withAsk: i === 1 || i === targetIdx,
            rev: i === targetIdx ? 'rev-target-sse-stashed' : `rev-${i}-v1`,
            streamNote: i === targetIdx ? 'StashedSseStreamNote' : undefined,
          });
        }).join('\n');

        const result = await page.evaluate(async ({ tid, itemId, initHtml, html, farExemptCount, sentRowCount }) => {
          // Establish baseline boardRendered hash and flush initial viewed hold before adding drafts/SSE update.
          window.__testApi.swapBoard('h-init', initHtml);
          const allTopics = Array.from(document.querySelectorAll('#r-board > section.topic[data-topic]'));
          window.WsBoardAnchor.select(allTopics[1]);
          window.WsBoardAnchor.select(allTopics[0]);

          // Populate .sentdata entries (10 vs 3,000 rows), including the target and unrelated topics.
          let sentBox = document.querySelector('.sentdata');
          if (!sentBox) {
            sentBox = document.createElement('div');
            sentBox.className = 'sentdata';
            sentBox.hidden = true;
            document.body.appendChild(sentBox);
          }
          const rows = [
            '<i data-id="101" data-state="held" data-answer="later" data-note="far-1"></i>',
            `<i data-id="${itemId}" data-state="sent" data-answer="yes" data-note="approved"></i>`,
            '<i data-id="1101" data-state="sent" data-answer="ok" data-note="far-11"></i>',
            '<i data-id="1201" data-state="held" data-answer="no" data-note="far-12"></i>',
          ];
          for (let r = rows.length; r < sentRowCount; r++) {
            rows.push(`<i data-id="9${String(r).padStart(4, '0')}" data-state="sent" data-answer="ok" data-note="bulk-${r}"></i>`);
          }
          sentBox.innerHTML = rows.join('');

          // Park other topics beyond either mounted window with drafts.
          const exemptIndices = [];
          for (let idx = 18; idx < allTopics.length && exemptIndices.length < farExemptCount; idx++) {
            exemptIndices.push(idx);
          }
          for (const idx of exemptIndices) {
            const sec = allTopics[idx];
            window.WsBoardVirtual.hydrate(sec, { immediate: false });
            const tn = sec.querySelector('.tnote[data-topic]');
            if (tn) { tn.hidden = false; tn.value = 'Draft on ' + sec.id; }
          }
          window.WsBoardAnchor.settle();

          // 2. Stash an SSE update on the collapsed target topic (leaving all other topics' data-th unchanged).
          window.__testApi.swapBoard('h-sse-i4', html);
          // Apply any draft bookkeeping queued by the fixture's off-window edits.
          window.WsBoardAnchor.select(allTopics[0], { still: true });
          const hasStashedBefore = window.WsBoardVirtual.hasStashed(tid);
          const parkedDrafts = exemptIndices.filter(idx => allTopics[idx].getAttribute('data-wb-offwindow') === 'parked').length;

          // 3. Wrap WsCards.decorate, window.wbBindBoard, window.wbForm, document.querySelector/All,
          //    and whole-board #r-board/#bgal querySelectorAll to verify scoped O(1) hydration only
          //    (zero boardItem/boardQ whole-board scans AND zero document-wide .sentdata scans).
          const origDecorate = window.WsCards.decorate;
          const origWbBindBoard = window.wbBindBoard;
          const origWbForm = window.wbForm;
          const boardEl = document.getElementById('r-board');
          const galEl = document.getElementById('bgal');
          const origBoardQSA = boardEl.querySelectorAll;
          const origGalQSA = galEl ? galEl.querySelectorAll : null;
          const origDocQS = document.querySelector;
          const origDocQSA = document.querySelectorAll;
          const origDocClass = document.getElementsByClassName;
          let decorateScoped = 0, decorateWholeBoard = 0, bindBoardScoped = 0, bindBoardWholeBoard = 0, wbFormCalls = 0, wholeBoardSentScans = 0, docSentScans = 0;
          document.querySelector = function (sel) {
            if (typeof sel === 'string' && sel.includes('.sentdata')) docSentScans++;
            return origDocQS.apply(this, arguments);
          };
          document.querySelectorAll = function (sel) {
            if (typeof sel === 'string' && sel.includes('.sentdata')) docSentScans++;
            return origDocQSA.apply(this, arguments);
          };
          document.getElementsByClassName = function (name) {
            if (name === 'sentdata') docSentScans++;
            return origDocClass.apply(this, arguments);
          };
          boardEl.querySelectorAll = function (sel) {
            if (typeof sel === 'string' && (sel.includes('[data-id=') || sel.includes('.sentmark') || sel.includes('.sent-sent'))) wholeBoardSentScans++;
            return origBoardQSA.apply(this, arguments);
          };
          if (galEl && origGalQSA) {
            galEl.querySelectorAll = function (sel) {
              if (typeof sel === 'string' && (sel.includes('[data-id=') || sel.includes('.sentmark') || sel.includes('.sent-sent'))) wholeBoardSentScans++;
              return origGalQSA.apply(this, arguments);
            };
          }
          window.WsCards.decorate = function (scope) {
            if (scope && scope.nodeType === 1) decorateScoped++;
            else decorateWholeBoard++;
            return origDecorate.apply(this, arguments);
          };
          window.wbBindBoard = function (scope) {
            if (scope && scope.nodeType === 1) bindBoardScoped++;
            else bindBoardWholeBoard++;
            return origWbBindBoard ? origWbBindBoard.apply(this, arguments) : undefined;
          };
          window.wbForm = function () {
            wbFormCalls++;
            return origWbForm ? origWbForm.apply(this, arguments) : undefined;
          };

          // 4. Reset stats and navigate straight into the SSE-updated stashed topic.
          window.WsBoardVirtual.resetStats();
          const targetSec = document.getElementById(tid);
          const activeFreeCanvas = window.WsBoardAnchor.free();
          const wasOffWindow = targetSec.getAttribute('data-wb-offwindow') === 'hidden';
          const origGetAttr = Element.prototype.getAttribute;
          let sentRowReads = 0;
          Element.prototype.getAttribute = function () {
            if (this.parentNode === sentBox) sentRowReads++;
            return origGetAttr.apply(this, arguments);
          };
          try {
            window.WsBoardAnchor.select(targetSec);
          } finally {
            Element.prototype.getAttribute = origGetAttr;
          }
          const stats = window.WsBoardVirtual.stats();
          const hydratedText = targetSec.textContent || '';
          const hydratedItem = targetSec.querySelector(`#item-${itemId}`);
          const sentMark = hydratedItem ? hydratedItem.querySelector('.sentmark.sent') : null;

          // Restore whole-board / decorate / bind / form wrappers before the follow-up swapBoard
          // (swapBoard runs a full board swap, whereas the follow-up select tests scoped hydration).
          boardEl.querySelectorAll = origBoardQSA;
          if (galEl && origGalQSA) galEl.querySelectorAll = origGalQSA;
          window.WsCards.decorate = origDecorate;
          window.wbBindBoard = origWbBindBoard;
          window.wbForm = origWbForm;

          // Verify incremental append keeps sentIndex accurate on next stashed hydration with zero doc .sentdata scans.
          const extra = document.createElement('i');
          extra.setAttribute('data-id', itemId);
          extra.setAttribute('data-state', 'held');
          extra.setAttribute('data-answer', 'revised');
          extra.setAttribute('data-note', 'followup');
          sentBox.appendChild(extra);
          window.WsBoardAnchor.select(allTopics[0]);
          window.__testApi.swapBoard('h-sse-i4-append', html.replace('rev-target-sse-stashed', 'rev-target-sse-stashed-2'));
          window.WsBoardAnchor.select(targetSec);
          const marksAfterAppend = targetSec.querySelectorAll(`#item-${itemId} .sentmark`).length;

          // Observer maintenance runs before the next frame's topic hydration.
          const first = sentBox.querySelector(`i[data-id="${itemId}"]`);
          const unrelated = sentBox.querySelector('i[data-id^="9"]');
          let unrelatedReads = 0;
          const read = unrelated.getAttribute;
          unrelated.getAttribute = function (name) { unrelatedReads++; return read.call(this, name); };
          first.remove();
          extra.setAttribute('data-answer', 'updated');
          await Promise.resolve();
          window.__testApi.applySent(true, targetSec);
          const texts = () => Array.from(targetSec.querySelectorAll(`#item-${itemId} .sentmark`), el => el.textContent);
          const marksAfterEdit = texts();
          const replacement = sentBox.cloneNode(false);
          replacement.innerHTML = `<i data-id="${itemId}" data-state="sent" data-answer="replacement"></i>`;
          sentBox.replaceWith(replacement);
          window.__testApi.applySent(false);
          window.__testApi.applySent(true, targetSec);
          const marksAfterReplace = texts();

          document.querySelector = origDocQS;
          document.querySelectorAll = origDocQSA;
          document.getElementsByClassName = origDocClass;
          return {
            activeFreeCanvas,
            wasOffWindow,
            hasStashedBefore,
            sentRowReads,
            stillCollapsed: targetSec.hasAttribute('data-wb-virtual'),
            renderedSse: hydratedText.includes('StashedSseStreamNote'),
            hydratedItemHasSentClass: !!(hydratedItem && hydratedItem.classList.contains('sent-sent')),
            sentMarkText: sentMark ? sentMark.textContent : null,
            marksAfterAppend,
            marksAfterEdit,
            marksAfterReplace,
            unrelatedReads,
            wholeBoardSentScans,
            docSentScans,
            parkedDrafts,
            stats,
            decorateScoped,
            decorateWholeBoard,
            bindBoardScoped,
            bindBoardWholeBoard,
            wbFormCalls,
          };
        }, { tid: targetId, itemId, initHtml: initialHtml, html: updatedHtml, farExemptCount, sentRowCount });

        assert.deepEqual(errors, []);
        assert.equal(result.parkedDrafts, farExemptCount);
        assert.equal(result.activeFreeCanvas, true, 'the browser uses Designated-Y');
        assert.equal(result.wasOffWindow, true, 'target starts beyond the mounted window');
        assert.equal(result.sentRowReads, 0, 'hydration uses the warm index without reading any sent DOM rows');
        assert.equal(result.hasStashedBefore, true, 'target topic had stashed SSE revision');
        assert.equal(result.stillCollapsed, false, 'target topic hydrated on navigation');
        assert.equal(result.renderedSse, true, 'stashed SSE content rendered on hydration');
        assert.equal(result.wholeBoardSentScans, 0, 'applySent(true, sec) must NEVER run whole-board boardItem/boardQ scans on #r-board or #bgal');
        assert.equal(result.docSentScans, 0, `applySent(true, sec) must NEVER run document-wide .sentdata scans (sentRowCount=${sentRowCount})`);
        assert.equal(result.hydratedItemHasSentClass, true, 'hydrated stashed topic item still receives .sent-sent class');
        assert.equal(result.sentMarkText, 'sent · yes', 'hydrated stashed topic item still receives accurate .sentmark text');
        assert.equal(result.marksAfterAppend, 2, 'incremental .sentdata append updates scoped sentIndex on next hydration');
        assert.deepEqual(result.marksAfterEdit, ['held · updated'], 'scoped hydration sees both the removal and attribute update');
        assert.deepEqual(result.marksAfterReplace, ['sent · replacement'], 'scoped hydration reads the replacement conversation box');
        assert.equal(result.unrelatedReads, 0, 'editing or replacing a sent box never rereads unrelated old rows');
        assert.equal(result.decorateWholeBoard, 0, 'hydrating one stashed topic must NEVER call whole-board WsCards.decorate()');
        assert.equal(result.decorateScoped, 1, 'hydrating one stashed topic calls scoped WsCards.decorate(sec) exactly once');
        assert.ok(result.bindBoardScoped >= 1, 'hydrating one stashed topic calls scoped wbBindBoard(sec)');
        assert.equal(result.bindBoardWholeBoard, 0, 'hydrating one stashed topic must NEVER call whole-board wbBindBoard()');
        assert.equal(result.wbFormCalls, 0, 'hydrating one stashed topic must NEVER call whole-board wbForm()');
        return result;
      } finally {
        await ctx.close();
      }
    }

    const small = await measureStashedEntry(24, 2, 10);
    const large = await measureStashedEntry(36, 2, 10);
    const manySent = await measureStashedEntry(36, 12, 3000);

    for (const result of [small, large, manySent]) assert.equal(result.stats.prunes, 0);
    for (const key of ['describes', 'slotVisits']) {
      assert.equal(small.stats[key], large.stats[key], key + ' must be invariant across board sizes');
      assert.equal(large.stats[key], manySent.stats[key], key + ' must be invariant across far draft and sent-row counts');
    }
  });

  await t.test('Important #5 (Chromium): lazy-history reply arriving after navigation reattaches topic runs live admission and surfaces archived matches in palette', async () => {
    const { ctx, page, errors } = await openHarness(browser, { topicCount: 14 });
    t.after(async () => { await ctx.close(); });

    const t13 = topicHexId(13);
    const fullTopic13Html = buildTopicHtml(13, { lazyFetch: false, twoGaps: true });

    // Defer wbHistoryFetch so navigation can reattach Topic 13 while the fetch is in flight.
    await page.evaluate(([tid, html13]) => {
      window.__historyLoadEvents = 0;
      document.addEventListener('wbhistoryload', () => { window.__historyLoadEvents++; });
      window.__resolvePendingHistory = null;
      window.__paletteUpdatedRows = null;
      window.wbHistoryFetch = function () {
        return new Promise((resolve) => {
          window.__resolvePendingHistory = () => resolve([{ name: tid, html: html13 }]);
        });
      };
    }, [t13, fullTopic13Html]);

    // 1. Confirm Topic 13 starts collapsed with lazy gaps, then kick off palette lazy-history search.
    const started = await page.evaluate(async (tid) => {
      const sec = document.getElementById(tid);
      const collapsedBefore = sec.hasAttribute('data-wb-virtual');
      window.__testApi.paletteHistory((rows) => { window.__paletteUpdatedRows = rows; });
      // Wait a microtask tick so hydrateAll settles and hydrateLazySearch invokes wbHistoryFetch.
      await new Promise((r) => setTimeout(r, 20));
      return {
        collapsedBefore,
        fetchPending: typeof window.__resolvePendingHistory === 'function',
      };
    }, t13);
    assert.equal(started.collapsedBefore, true, 'Topic 13 starts collapsed');
    assert.equal(started.fetchPending, true, 'lazy-history fetch is pending');

    // 2. Navigate to Topic 13 BEFORE the fetch resolves so it reattaches to live DOM while fetch is in flight.
    const reattached = await page.evaluate((tid) => {
      const sec = document.getElementById(tid);
      window.WsBoardAnchor.select(sec);
      return {
        collapsedAfterNav: sec.hasAttribute('data-wb-virtual'),
        stillHasLazyGap: !!sec.querySelector('.history-gap[data-lazy]'),
      };
    }, t13);
    assert.equal(reattached.collapsedAfterNav, false, 'navigation reattached Topic 13 to live DOM');
    assert.equal(reattached.stillHasLazyGap, true, 'lazy gap is still pending before fetch resolves');

    // 3. Now resolve the in-flight fetch and assert BOTH live admission AND palette search matches!
    const afterResolve = await page.evaluate(async (tid) => {
      window.__resolvePendingHistory();
      await new Promise((r) => setTimeout(r, 40));
      const sec = document.getElementById(tid);
      const rows = window.__paletteUpdatedRows || window.__testApi.paletteRows();
      const gap1Match = rows.find((r) => r.topic === tid && r.entry === 'e1300');
      const gap2Match = rows.find((r) => r.topic === tid && r.entry === 'e1304');
      return {
        paletteCallbackFired: Array.isArray(window.__paletteUpdatedRows),
        historyLoadEvents: window.__historyLoadEvents,
        remainingLazyGaps: sec.querySelectorAll('.history-gap[data-lazy]').length,
        hasGap1Match: !!gap1Match,
        hasGap2Match: !!gap2Match,
      };
    }, t13);

    assert.deepEqual(errors, []);
    assert.equal(afterResolve.paletteCallbackFired, true, 'palette update callback fired after reattached fetch resolved');
    assert.ok(afterResolve.historyLoadEvents >= 1, 'live admission dispatched wbhistoryload on reattached topic');
    assert.equal(afterResolve.remainingLazyGaps, 0, 'all lazy gaps on live reattached topic were filled');
    assert.equal(afterResolve.hasGap1Match, true, 'gap 1 archived match appears in search palette');
    assert.equal(afterResolve.hasGap2Match, true, 'gap 2 archived match appears in search palette');
  });

  await t.test('a palette reply fills a topic evicted after the live request starts', async () => {
    const { ctx, page, errors } = await openHarness(browser, { topicCount: 30 });
    t.after(() => ctx.close());
    const id = topicHexId(13);
    const result = await page.evaluate(async ([id, far, html]) => {
      const sec = document.getElementById(id);
      window.WsBoardAnchor.select(sec, { smooth: false });
      const live = !sec.hasAttribute('data-wb-virtual') && !!sec.querySelector('.history-gap[data-lazy]');
      let finish;
      window.wbHistoryFetch = query => new URLSearchParams(query).getAll('name').includes(id)
        ? new Promise(resolve => { finish = resolve; })
        : Promise.resolve([]);
      let updated;
      window.__testApi.paletteHistory(rows => { updated = rows; });
      await Promise.resolve();
      if (!finish) throw new Error('The live palette request did not include the target topic');
      window.WsBoardAnchor.select(document.getElementById(far), { smooth: false });
      const evicted = sec.hasAttribute('data-wb-virtual');
      finish([{ name: id, html }]);
      await new Promise(resolve => setTimeout(resolve, 0));
      return { live, evicted, entry: updated?.some(row => row.topic === id && row.entry === 'e1304'),
        stillEvicted: sec.hasAttribute('data-wb-virtual') };

    }, [id, topicHexId(29), buildTopicHtml(13, { twoGaps: true })]);
    assert.deepEqual(errors, []);
    assert.deepEqual(result, { live: true, evicted: true, entry: true, stillEvicted: true });
  });

  await t.test('Important #7 & #8 (Chromium): Overview Quick Look folded placeholder matches folded geometry and exposes accessible name with inert controls', async () => {
    const { ctx, page, errors } = await openHarness(browser, { topicCount: 6 });
    t.after(async () => { await ctx.close(); });

    const t1 = topicHexId(1);
    const t2 = topicHexId(2);
    // Fold BOTH Topic 1 and Topic 2 through their real .t-fold buttons, then enter Overview.
    // Topic 1 becomes the Quick Look placeholder in #r-board while Topic 2 remains a non-previewed
    // folded topic right beside it in #r-board, giving an exact apples-to-apples geometry baseline.
    await page.evaluate(([id1, id2]) => {
      for (const tid of [id1, id2]) {
        const sec = document.getElementById(tid);
        const foldBtn = sec.querySelector(':scope > .tophead > .tbar > .t-fold');
        if (foldBtn) foldBtn.click();
        else sec.setAttribute('data-size', 'fold');
      }
      window.WsOverview.toggle();
    }, [t1, t2]);
    // Wait past the 240ms Overview FLIP entry transition so t2 settles to its final folded height.
    await page.waitForTimeout(320);

    const before = await page.evaluate((tid2) => {
      const sec = document.getElementById(tid2);
      const foldRect = sec.getBoundingClientRect();
      const foldBar = sec.querySelector(':scope > .tophead > .tbar');
      const foldSub = sec.querySelector(':scope > .tophead > .tsub');
      return {
        height: Math.round(foldRect.height),
        tbarPadRight: foldBar ? getComputedStyle(foldBar).paddingRight : '',
        tsubDisplay: foldSub ? getComputedStyle(foldSub).display : '',
      };
    }, t2);

    // Ensure Topic 1 is previewed in Quick Look and its placeholder is mounted in #r-board.
    const t1Name = await page.evaluate((tid1) => {
      const sec = document.getElementById(tid1);
      return sec && sec.querySelector('.tname') ? sec.querySelector('.tname').textContent.trim() : '';
    }, t1);
    before.tname = t1Name;
    if (!(await page.locator('#r-board .overview-placeholder').count())) {
      const box1 = await page.locator(`#${t1}`).boundingBox();
      await page.mouse.move(box1.x + box1.width / 2, box1.y + 16);
      await page.waitForTimeout(350);
    }

    const after = await page.evaluate(() => {
      const ph = document.querySelector('#r-board .overview-placeholder');
      if (ph) ph.focus();
      const phRect = ph ? ph.getBoundingClientRect() : null;
      const phBar = ph ? ph.querySelector(':scope > .tophead > .tbar') : null;
      const phSub = ph ? ph.querySelector(':scope > .tophead > .tsub') : null;
      const phHead = ph ? ph.querySelector(':scope > .tophead') : null;
      return {
        tagName: ph ? ph.tagName : null,
        height: phRect ? Math.round(phRect.height) : null,
        tbarPadRight: phBar ? getComputedStyle(phBar).paddingRight : '',
        tsubDisplay: phSub ? getComputedStyle(phSub).display : '',
        role: ph ? ph.getAttribute('role') : null,
        ariaLabel: ph ? ph.getAttribute('aria-label') : null,
        activeIsPh: document.activeElement === ph,
        headInert: phHead ? !!phHead.inert : false,
        headAriaHidden: phHead ? phHead.getAttribute('aria-hidden') : null,
      };
    });

    const ariaSnap = await page.locator('#r-board .overview-placeholder').ariaSnapshot();

    assert.deepEqual(errors, []);
    assert.equal(before.tsubDisplay, 'none', 'folded topic hides .tsub before Overview');
    assert.equal(after.tagName, 'SECTION', 'folded topic placeholder uses <section>');
    assert.equal(after.tsubDisplay, 'none', 'folded topic placeholder hides cloned .tsub');
    assert.equal(after.tbarPadRight, before.tbarPadRight, 'folded topic placeholder preserves .tbar padding-right');
    assert.equal(after.height, before.height, 'folded topic placeholder outer height matches folded topic');
    assert.equal(after.activeIsPh, true, 'keyboard focus lands on placeholder');
    assert.equal(after.role, 'button', 'placeholder exposes button role');
    assert.equal(after.ariaLabel, before.tname, 'placeholder aria-label matches original topic title');
    assert.equal(after.headInert, true, 'cloned header controls stay inert');
    assert.equal(after.headAriaHidden, 'true', 'cloned header stays aria-hidden');
    assert.ok(ariaSnap.includes(`button "${before.tname}"`), `expected ariaSnapshot to expose button "${before.tname}", got: ${ariaSnap}`);
  });
});
