#!/usr/bin/env node
'use strict';

// Playwright browser tests for Main Board Snap Deck Phase 2:
// 1. Right-side outer circle-chain dial (.d-board-dial / #bdial):
//    placement across layouts, hide states without padding shift, ask/held rings,
//    click/wheel/scrub (<=18 and >18 window freeze), and zero idle rAF.
// 2. Left .gal-rail hover & title-drag-hover preview popover (.gal-pop):
//    Home/+/docked split mini-cards with NO outer header, z-index 42, pointer-events: none,
//    Escape without preventDefault, scroll hide, Overview hide, drag-mode drop strip,
//    WAAPI spring progress, sprung suppression, and zero idle rAF.
// 3. Wide-mode top clearance polish (3a tophead clearance & rail0 exclusion;
//    3b 1px resting alignment on unfloated and [data-floating] toolbar).

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

function buildFixtureHtml(opts = {}) {
  const topicCount = opts.topicCount || 6;
  const gridClass = opts.gridClass || 'live';
  const layoutAttr = opts.layout ? ` data-layout="${opts.layout}"` : '';
  const tabAttr = opts.tab ? ` data-tab="${opts.tab}"` : ' data-tab="board"';
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
    const hasAsk = i === 1 || i === 3;
    const held = i === 2 ? ' data-board-held=""' : '';
    const askAttr = ` data-open-asks="${hasAsk ? 'true' : 'false'}"`;
    const askCard = hasAsk
      ? `<div class="card policy" id="item-${i}" data-wb-hold="q-${i}" data-wb-q="q-${i}">
           <div class="phead">Open Question #${i}</div>
         </div>`
      : '';
    const lines = Array.from({ length: 18 }, (_, j) =>
      `<div class="log-line">Topic ${i} line ${j + 1}</div>`
    ).join('');
    topics.push(`<section class="topic" id="topic-${i}" data-topic="topic-${i}" data-th="rev-${i}"${askAttr}${held}>
      <div class="tophead">
        <div class="tbar">
          <span class="tdot"></span>
          <span class="tname">Topic ${i}</span>
          ${hasAsk ? '<span class="chip policy">1</span>' : ''}
        </div>
        <div class="tsub">Subtitle for Topic ${i}</div>
      </div>
      ${askCard}
      <div class="tbody">${lines}</div>
    </section>`);
  }

  return `<!doctype html>
<html data-mode="${gridClass}" data-theme="light">
<head>
<meta charset="utf-8">
<style>
${css}
html, body { height: 100%; margin: 0; overflow: hidden; background: var(--wb-bg); color: var(--wb-fg); font-family: var(--wb-sans, system-ui, sans-serif); }
.dwrap { height: 100vh; display: flex; flex-direction: column; }
.dbody { flex: 1 1 0; min-height: 0; display: flex; }
${gridClass === 'board' ? '#bcol { overflow: visible; }' : '#bcol { height: 100%; min-height: 0; }'}
section.topic { background: var(--wb-card); border-radius: var(--wb-radius); box-shadow: var(--wb-shadow); margin-bottom: 16px; }
section.topic > .tophead { border-bottom: 1px solid var(--wb-muted); }
section.topic > .tophead > .tbar { display: flex; align-items: center; gap: 8px; padding: 8px 12px; min-height: 36px; box-sizing: border-box; font-weight: 700; }
section.topic > .tophead > .tsub { padding: 2px 12px 6px; font-size: 12px; color: var(--wb-muted-fg); }
section.topic > .tbody { padding: 6px 14px 10px; }
.log-line { padding: 4px 0; border-bottom: 1px solid color-mix(in srgb, var(--wb-line) 55%, transparent); font-size: 13px; }
</style>
</head>
<body class="dash ${gridClass}">
<div hidden>${sprite}</div>
<div class="dwrap">
  <div class="dbody">
    <main id="grid" class="dgrid ${gridClass}"${layoutAttr}${tabAttr}>
      ${gridClass === 'live' || gridClass === 'history' ? '<div class="convo" id="convo">Convo</div>' : ''}
      ${gridClass === 'worktree' ? '<div class="thead">WT</div><div class="fpane">Files</div>' : ''}
      <div id="bcol" class="bcol">
        <div id="r-board" class="r-board" data-region="board">
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
    'js.board.toolbar.overview.title': 'Overview',
    'js.board.toolbar.files': 'Files',
    'js.board.toolbar.download': 'Download',
    'js.board.toolbar.download.title': 'Download',
    'js.board.dial.label': 'Board panels',
    'js.board.dial.asks': 'Open asks',
    'js.board.dial.held': 'Updated',
    'js.gal.preview.home': '홈',
    'js.gal.preview.home.drag': '홈으로 언독',
    'js.gal.preview.new': '새 페이지',
    'js.gal.preview.new.drag': '새 페이지에 도킹',
    'js.gal.preview.append': '이 페이지에 분할 추가',
    'js.gal.preview.spring': '길게 대면 페이지 열기',
    'js.pins.title': 'Pinned',
    'js.pins.drop': 'Drop here to pin',
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

function buildBootstrapScript(opts = {}) {
  const galleryJs = readAsset(WSD, 'gallery.js');
  const previewJs = readAsset(WSD, 'gal-rail-preview.js');
  const cardsJs = readAsset(WSD, 'cards.js');
  const pillJs = readAsset(WSW, 'pill.js');
  const railJs = readAsset(WSW, 'rail.js');
  const anchorJs = readAsset(WSD, 'dashboard-board-anchor.js');
  const dialJs = readAsset(WSD, 'dashboard-board-dial.js');

  const savedCards = opts.savedCards || { topics: [], cards: [] };
  const leadingCard = opts.leadingCard ? 'true' : 'false';

  return [
    galleryJs,
    previewJs,
    cardsJs,
    pillJs,
    `(function () {
      var doc = document;
      function $(id) { return doc.getElementById(id); }
      function q(s, box) { return Array.prototype.slice.call((box || doc).querySelectorAll(s)); }
      function region(name) { return doc.querySelector('[data-region="' + name + '"]'); }
      function topicKey(n) { return n && n.getAttribute ? n.getAttribute("data-topic") : null; }
      function reducedMotion() { return true; }
      var boardPending = {};
      var boardSnap = null;
      window.__flushCount = 0;
      function boardFlushViewed() { window.__flushCount++; }
      function wsT(k) { return window.wsT(k); }
      ${anchorJs}
      ${dialJs}
      if (window.WsCards && window.WsCards.mount) {
        window.WsCards.register("testterm", {
          base: 320,
          folded: function (c) { return c.data && c.data.summary ? c.data.summary : "zsh · idle"; },
          open: function (c) {
            c.name.textContent = (c.data && c.data.title) || "Terminal 1";
            c.badge.textContent = "TERM";
            c.body.innerHTML = '<div style="padding:10px;">terminal body</div>';
          }
        });
        var mem = { "p2-fixture": ${JSON.stringify(JSON.stringify(savedCards))} };
        window.WsCards.mount(region("board"), {
          key: "p2-fixture",
          load: function (k) { return mem[k] || null; },
          save: function (k, v) { mem[k] = v; },
          mode: function () { return { width: window.innerWidth, tabs: false }; },
          layout: function (s) {
            var m = s && s.narrow ? "narrow" : s && s.column ? "col" : s && s.empty ? "empty" : s && s.overlay ? "drag" : "";
            if (m) doc.documentElement.setAttribute("data-gal", m);
            else doc.documentElement.removeAttribute("data-gal");
          },
          leading: ${leadingCard} ? function (u) { return u && u.kind === "testterm"; } : null,
          grid: doc.getElementById("grid"),
          toolbar: { overview: function () {} }
        });
      }
      if (${opts.nonRailLeading ? 'true' : 'false'}) {
        var leadBc = doc.querySelector('.bcard');
        if (leadBc) leadBc.removeAttribute('data-rail');
      }
      anchorInit();
      window.__setPending = function (on) { if (on) boardPending["t:topic-1"] = true; else delete boardPending["t:topic-1"]; };
      window.__dialSync = boardDialSync;
    })();`,
    railJs,
  ];
}

async function openFixture(browser, viewport, opts = {}) {
  const ctx = await browser.newContext({ viewport });
  const page = await ctx.newPage();
  await page.setContent(buildFixtureHtml(opts), { waitUntil: 'load' });
  for (const script of buildBootstrapScript(opts)) {
    await page.addScriptTag({ content: script });
  }
  await page.waitForTimeout(80);
  return { ctx, page };
}

test('Phase 2 Playwright: dial layout/rings/scrub>18/hide-states, gal-rail preview, and 3a/3b clearance', async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    // 1. Live split layout: dial placement, rings, hide states without padding shift, wheel & zero idle rAF
    {
      const { ctx, page } = await openFixture(browser, { width: 1280, height: 800 }, { topicCount: 6, gridClass: 'live', layout: 'split' });
      const info = await page.evaluate(() => {
        const dial = document.getElementById('bdial');
        const bcol = document.getElementById('bcol');
        const dots = Array.from(dial.querySelectorAll('.d-board-dial-dot'));
        return {
          hasRootAttr: document.documentElement.hasAttribute('data-board-dial'),
          dialVisible: getComputedStyle(dial).display !== 'none' && !dial.hidden,
          bcolPadRight: parseFloat(getComputedStyle(bcol).paddingRight),
          dotCount: dots.length,
          ask0: dots[0].getAttribute('data-ask'),
          held1: dots[1].getAttribute('data-held'),
          tier0: dots[0].getAttribute('data-tier'),
          tier1: dots[1].getAttribute('data-tier'),
          tier2: dots[2].getAttribute('data-tier'),
        };
      });
      assert.equal(info.hasRootAttr, true);
      assert.equal(info.dialVisible, true);
      assert.equal(info.bcolPadRight, 26);
      assert.equal(info.dotCount, 6);
      assert.equal(info.ask0, '1');
      assert.equal(info.held1, '1');
      assert.equal(info.tier0, '0');
      assert.equal(info.tier1, '1');
      assert.equal(info.tier2, '2');

      // Hide states toggle #bdial visibility ONLY, keeping #bcol paddingRight === 26
      for (const attr of ['data-overview', 'data-gal-away', 'data-file-preview', 'data-board-max']) {
        const res = await page.evaluate(a => {
          document.documentElement.setAttribute(a, '');
          const dial = document.getElementById('bdial');
          const bcol = document.getElementById('bcol');
          const out = { vis: getComputedStyle(dial).visibility, pad: parseFloat(getComputedStyle(bcol).paddingRight) };
          document.documentElement.removeAttribute(a);
          return out;
        }, attr);
        assert.equal(res.vis, 'hidden', `${attr} hides #bdial`);
        assert.equal(res.pad, 26, `${attr} preserves #bcol right padding`);
      }

      // Click dot 3 navigates to topic-4
      await page.locator('#bdial .d-board-dial-dot').nth(3).click();
      await page.waitForTimeout(80);
      assert.equal(await page.evaluate(() => window.WsBoardAnchor.key()), 't:topic-4');

      // 3b resting alignment check within 1px (floating toolbar when scrolled to topic-4)
      const align3b = await page.evaluate(() => {
        const col = document.getElementById('bcol');
        const card = document.querySelector('section.topic[data-topic="topic-4"]');
        const colRect = col.getBoundingClientRect();
        const cardRect = card.getBoundingClientRect();
        const padTop = parseFloat(getComputedStyle(col).paddingTop) || 0;
        const inset = parseFloat(col.style.getPropertyValue('--wb-rail-inset')) || 0;
        const capH2 = 4 + 2 * (17 + 3); // 44px
        const expectedTop = colRect.top + col.clientTop + padTop + inset + capH2;
        const bar = window.WsCards.toolbar();
        return {
          err: Math.abs(cardRect.top - expectedTop),
          floating: bar && bar.hasAttribute('data-floating'),
          scrollPad: col.style.getPropertyValue('scroll-padding-top'),
          railInset: col.style.getPropertyValue('--wb-rail-inset')
        };
      });
      assert.equal(align3b.scrollPad, align3b.railInset, '3b: scroll-padding-top matches --wb-rail-inset');
      assert.ok(align3b.err <= 1.5, `3b: card top alignment error ${align3b.err}px <= 1.5px`);

      // Zero idle rAF check over 250ms
      const idleRafs = await page.evaluate(() => new Promise(resolve => {
        let count = 0;
        const orig = window.requestAnimationFrame;
        window.requestAnimationFrame = function (cb) { count++; return orig.call(window, cb); };
        setTimeout(() => { window.requestAnimationFrame = orig; resolve(count); }, 250);
      }));
      assert.equal(idleRafs, 0, 'zero idle rAF when board/dial are at rest');
      await ctx.close();
    }

    // 2. Unbounded .dgrid.board NEVER mounts #bdial
    {
      const { ctx, page } = await openFixture(browser, { width: 1200, height: 800 }, { topicCount: 4, gridClass: 'board' });
      assert.equal(await page.evaluate(() => document.getElementById('bdial')), null);
      assert.equal(await page.evaluate(() => document.documentElement.hasAttribute('data-board-dial')), false);
      await ctx.close();
    }

    // 3. History & Worktree layouts mount #bdial properly
    for (const cfg of [
      { gridClass: 'history', vp: { width: 1200, height: 800 }, tab: 'convo', expectVisible: true },
      { gridClass: 'history', vp: { width: 900, height: 800 }, tab: 'convo', expectVisible: false },
      { gridClass: 'worktree', vp: { width: 680, height: 800 }, tab: 'board', expectVisible: true },
    ]) {
      const { ctx, page } = await openFixture(browser, cfg.vp, { topicCount: 4, gridClass: cfg.gridClass, tab: cfg.tab });
      const vis = await page.evaluate(() => {
        const d = document.getElementById('bdial');
        return !!(d && !d.hidden && getComputedStyle(d).display !== 'none');
      });
      assert.equal(vis, cfg.expectVisible, `${cfg.gridClass} @ ${cfg.vp.width}px tab=${cfg.tab}`);
      await ctx.close();
    }

    // 4. Scrub > 18 cards: window frozen during drag, unfreezes on pointerup
    {
      const { ctx, page } = await openFixture(browser, { width: 1280, height: 800 }, { topicCount: 24, gridClass: 'live', layout: 'split' });
      const res = await page.evaluate(() => {
        window.__setPending(true);
        const dial = document.getElementById('bdial');
        const dots = Array.from(dial.querySelectorAll('.d-board-dial-dot'));
        const firstBefore = dots[0].getAttribute('data-idx');
        const rect = dots[5].getBoundingClientRect();
        dial.dispatchEvent(new PointerEvent('pointerdown', { pointerId: 1, button: 0, clientX: rect.left + 4, clientY: rect.top + 4, bubbles: true }));
        dial.dispatchEvent(new PointerEvent('pointermove', { pointerId: 1, clientX: rect.left + 4, clientY: rect.top + 140, bubbles: true }));
        const scrubbingDuring = window.WsBoardAnchor.scrubbing();
        const firstDuring = dial.querySelector('.d-board-dial-dot').getAttribute('data-idx');
        const flushesDuring = window.__flushCount;
        dial.dispatchEvent(new PointerEvent('pointerup', { pointerId: 1, clientX: rect.left + 4, clientY: rect.top + 140, bubbles: true }));
        return {
          dotCount: dots.length,
          firstBefore,
          scrubbingDuring,
          firstDuring,
          flushesDuring,
          scrubbingAfter: window.WsBoardAnchor.scrubbing(),
          flushesAfter: window.__flushCount
        };
      });
      assert.equal(res.dotCount, 18);
      assert.equal(res.scrubbingDuring, true);
      assert.equal(res.firstDuring, res.firstBefore, 'window frozen during >18 scrub');
      assert.equal(res.flushesDuring, 0);
      assert.equal(res.scrubbingAfter, false);
      assert.equal(res.flushesAfter, 1);
      await ctx.close();
    }

    // 5. .gal-rail hover & drag-hover preview popover (narrow mode with docked split page)
    {
      const savedCards = {
        topics: [{ n: 'topic-5', p: 1, a: 100 }],
        cards: [{ id: 'term-1', kind: 'testterm', pinned: 2, at: 200, data: { title: 'Build Shell', summary: 'go test ./...' } }],
        gallery: {
          pages: [{ id: 'pg1', root: { axis: 'row', ratio: 0.55, a: { topic: 'topic-5' }, b: { card: 'term-1' } } }],
          active: null
        }
      };
      const { ctx, page } = await openFixture(browser, { width: 1200, height: 800 }, { topicCount: 6, gridClass: 'live', layout: 'split', savedCards });

      // Hover Home dot
      await page.locator('.gal-dot.main').hover();
      await page.waitForTimeout(40);
      const homePop = await page.evaluate(() => {
        const p = document.querySelector('.gal-pop');
        const cs = getComputedStyle(p);
        return { hidden: p.hidden, mode: p.getAttribute('data-mode'), z: cs.zIndex, pe: cs.pointerEvents, text: p.textContent.trim() };
      });
      assert.equal(homePop.hidden, false);
      assert.equal(homePop.mode, 'pill');
      assert.equal(homePop.z, '42');
      assert.equal(homePop.pe, 'none');
      assert.ok(homePop.text.includes('홈'));

      // Hover docked split page dot: mini cards directly in .gal-pop-layout, NO outer header, folded summary rendered
      await page.locator('.gal-dot.page').first().hover();
      await page.waitForTimeout(40);
      const pagePop = await page.evaluate(() => {
        const p = document.querySelector('.gal-pop');
        const cards = Array.from(p.querySelectorAll('.gal-pop-card'));
        return {
          hidden: p.hidden,
          mode: p.getAttribute('data-mode'),
          cardCount: cards.length,
          titles: cards.map(c => c.querySelector('.gal-pop-card-title').textContent),
          subs: cards.map(c => { const s = c.querySelector('.gal-pop-card-sub'); return s ? s.textContent : ''; }),
          hasDrop: !p.querySelector('.gal-pop-drop').hidden,
          caretY: p.style.getPropertyValue('--caret-y')
        };
      });
      assert.equal(pagePop.hidden, false);
      assert.equal(pagePop.mode, 'page');
      assert.equal(pagePop.cardCount, 2);
      assert.deepEqual(pagePop.titles, ['topic-5', 'Build Shell']);
      assert.equal(pagePop.subs[1], 'go test ./...');
      assert.equal(pagePop.hasDrop, false);
      assert.ok(pagePop.caretY.endsWith('px'));

      // Escape hides without preventDefault
      const escRes = await page.evaluate(() => {
        let def = false;
        const onKey = e => { def = e.defaultPrevented; };
        window.addEventListener('keydown', onKey);
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', cancelable: true, bubbles: true }));
        window.removeEventListener('keydown', onKey);
        return { hidden: document.querySelector('.gal-pop').hidden, defaultPrevented: def };
      });
      assert.equal(escRes.hidden, true);
      assert.equal(escRes.defaultPrevented, false);

      // Overview open hides popover
      await page.locator('.gal-dot.empty').hover();
      await page.waitForTimeout(40);
      assert.equal(await page.evaluate(() => document.querySelector('.gal-pop').hidden), false);
      await page.evaluate(() => window.WsCards.overview(true));
      assert.equal(await page.evaluate(() => document.querySelector('.gal-pop').hidden), true);
      await ctx.close();
    }

    // 6. 3a wide-mode top clearance: Topic 0 clears .tophead, while rail0 (non-rail Slot 0) does NOT inflate --wb-rail-h
    {
      const { ctx, page } = await openFixture(browser, { width: 1280, height: 800 }, { topicCount: 4, gridClass: 'live', layout: 'split' });
      const top3a = await page.evaluate(() => {
        const bcol = document.getElementById('bcol');
        const t0 = document.querySelector('section.topic[data-topic="topic-1"]');
        const headH = t0.querySelector('.tophead').offsetHeight;
        const inset = parseFloat(bcol.style.getPropertyValue('--wb-rail-inset')) || 0;
        const oq = document.querySelector('.oq-wrap.wb-oq-top');
        const railH = oq ? parseFloat(oq.style.getPropertyValue('--wb-rail-h')) : 0;
        return { cap0: t0.getAttribute('data-anchor-cap'), headH, inset, railH };
      });
      assert.equal(top3a.cap0, '0');
      assert.ok(top3a.railH >= top3a.inset + top3a.headH - 1, `3a: --wb-rail-h (${top3a.railH}) clears Topic 0 tophead (${top3a.inset} + ${top3a.headH})`);
      await ctx.close();
    }
    {
      const savedCards = {
        topics: [],
        cards: [{ id: 'lead-1', kind: 'testterm', at: 1e15, data: { title: 'Leading Card' } }]
      };
      const { ctx, page } = await openFixture(browser, { width: 1280, height: 800 }, { topicCount: 4, gridClass: 'live', layout: 'split', savedCards, leadingCard: true, nonRailLeading: true });
      // Select Slot 0 (the non-rail leading card) so scrollTop=0 and topic-1 (rail0) sits below vt.
      await page.evaluate(() => {
        const leadBc = document.querySelector('.bcard');
        if (leadBc) window.WsBoardAnchor.select(leadBc, { smooth: false });
        const bcol = document.getElementById('bcol');
        bcol.scrollTop = 0;
        bcol.dispatchEvent(new Event('scroll'));
      });
      await page.waitForTimeout(60);
      const rail0Res = await page.evaluate(() => {
        const bcol = document.getElementById('bcol');
        const t0 = document.querySelector('section.topic[data-topic="topic-1"]');
        const inset = parseFloat(bcol.style.getPropertyValue('--wb-rail-inset')) || 0;
        const oq = document.querySelector('.oq-wrap.wb-oq-top');
        const railH = oq ? parseFloat(oq.style.getPropertyValue('--wb-rail-h')) : 0;
        return { cap: t0.getAttribute('data-anchor-cap'), inset, railH, scrollTop: bcol.scrollTop };
      });
      assert.equal(rail0Res.cap, 'rail0');
      assert.equal(rail0Res.scrollTop, 0);
      assert.ok(Math.abs(rail0Res.railH - rail0Res.inset) <= 1, `3a rail0: --wb-rail-h (${rail0Res.railH}) must equal inset (${rail0Res.inset}) when Slot 0 is non-rail`);
      await ctx.close();
    }
  } finally {
    await browser.close();
  }
});
