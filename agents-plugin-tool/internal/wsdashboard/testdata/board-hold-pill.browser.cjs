'use strict';
// Full production page and websocket; only the store and network are fixtures.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {pathToFileURL} = require('node:url');
const {chromium} = require(process.env.WS_PLAYWRIGHT_MODULE);
const [origin, output] = process.argv.slice(2);
const reference = path.resolve('../../../ai-docs/tickets/assets/261115-feat-ws-dashboard-board-reorder-hold-rewrite/nav-capsule-accepted.html');
const report = {cases: [], screenshots: [], errors: []};
(async () => {
  const browser = await chromium.launch({headless: true, args: ['--disable-background-networking', '--no-proxy-server']});
  let active;
  try {
    const matrix = [1440, 700].flatMap(width => ['light', 'dark'].flatMap(theme => [true, false].map(reduced => ({width, theme, reduced, documentMode: false}))));
    matrix.push({width: 1440, theme: 'light', reduced: true, documentMode: true});
    for (const {width, theme, reduced, documentMode} of matrix) {
      const id = `${width}-${theme}-${reduced ? 'reduced' : 'motion'}${documentMode ? '-document' : ''}`;
      const context = await browser.newContext({viewport: {width, height: 900}, colorScheme: theme, reducedMotion: reduced ? 'reduce' : 'no-preference', locale: 'ko-KR'});
      await context.route('**/*', route => {
        const u = route.request().url();
        return u.startsWith(origin + '/') || u.startsWith('data:') || u === 'about:blank' ? route.continue() : route.abort();
      });
      await context.addInitScript(() => {
        const state = window.__hold = {scrolls: [], setters: [], mounts: 0};
        const scroll = Element.prototype.scrollTo;
        Element.prototype.scrollTo = function(...args) {
          if (this.id === 'bcol' || this === document.scrollingElement) state.scrolls.push({args, stack: new Error().stack});
          return scroll.apply(this, args);
        };
        const prop = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTop');
        Object.defineProperty(Element.prototype, 'scrollTop', {...prop, set(value) {
          if (this.id === 'bcol' || this === document.scrollingElement) state.setters.push({value, stack: new Error().stack});
          return prop.set.call(this, value);
        }});
        new MutationObserver(records => {
          for (const r of records) for (const n of r.addedNodes) if (n.nodeType === 1) state.mounts += (n.matches('iframe') ? 1 : 0) + n.querySelectorAll('iframe').length;
        }).observe(document, {childList: true, subtree: true});
      });
      const page = active = await context.newPage();
      page.on('pageerror', e => report.errors.push(`${id}: ${e.stack || e}`));
      await page.request.post(origin + '/reset');
      await page.goto(origin + '/s/holdpill' + (documentMode ? '?document=1' : ''), {waitUntil: 'domcontentloaded'});
      await page.waitForFunction(() => window.wbPill?.update && window.wbHistory && document.querySelectorAll('section.topic').length === 25);
      // Let the first full stream establish the immutable baseline.
      await page.waitForFunction(() => document.querySelector('[data-region="board"]').getAttribute('data-h'));
      async function frames(n = 8) { await page.evaluate(async n => {while (n--) await new Promise(requestAnimationFrame);}, n); }
      await frames();
      if (!(await page.locator('#bcol').isVisible())) await page.locator('[data-tab="board"]').first().click();
      await page.evaluate(() => {window.__holdScroller = () => window.wbHistory.keep(document.querySelector('section.topic'), {capture: false}).scroller;});
      assert.equal(await page.evaluate(() => window.__holdScroller() === document.scrollingElement), documentMode, 'fixture uses the intended real scroll root');
      const pill = page.locator('.wb-oq-top .oq-pill');
      const label = page.locator('.wb-oq-top .oq-updates');
      assert.equal(await page.locator('.board-viewed-pending').count(), 0, 'removed bottom bar never mounts');
      // One genuinely answered and one open item above the reading window.
      await page.locator('.opt[data-opt]').first().click();
      async function scrollToTopic(name) {
        await page.evaluate(name => {
          const s = window.__holdScroller(), n = document.querySelector(`section.topic[data-topic="${name}"]`);
          s.scrollTop += n.getBoundingClientRect().top - (s === document.scrollingElement ? 0 : s.getBoundingClientRect().top) - 120;
        }, name);
        await page.waitForFunction(() => window.__holdScroller().scrollTop > 4);
        await frames(20);
        await page.evaluate(() => window.wbPill.update());
      }
      async function geometry() {
        return page.evaluate(() => ({scroll: window.__holdScroller().scrollTop, topics: [...document.querySelectorAll('section.topic')].map(n => ({name: n.dataset.topic, y: n.getBoundingClientRect().top, h: n.getBoundingClientRect().height}))}));
      }
      async function update(topic, close = false, rise = false) {
        const receipt = await page.evaluate(async ({topic, close, rise}) => {
          const r = await fetch('/update?topic=' + topic + (close ? '&close=1' : '') + (rise ? '&rise=1' : ''), {method: 'POST'});
          if (!r.ok) throw Error(await r.text());
          return r.json();
        }, {topic, close, rise});
        await page.waitForFunction(hash => document.querySelector('[data-region="board"]').getAttribute('data-h') === hash, receipt.hash);
        await frames();
        await page.evaluate(async () => {await Promise.all(document.getAnimations().filter(a => a.animationName === 'board-hold-nudge').map(a => a.finished.catch(() => {})));});
      }
      const refContext = await browser.newContext({viewport: {width, height: 900}, colorScheme: theme, reducedMotion: reduced ? 'reduce' : 'no-preference'});
      const ref = await refContext.newPage();
      await ref.goto(pathToFileURL(reference).href);
      await ref.evaluate(width => { if (width === 700) document.querySelector('#ttPage').className = 'page n'; }, width);
      let topToolbarY = null;
      async function capture(state, counts) {
        await frames(10);
        const full = path.join(output, `impl-${id}-${state}.png`);
        await page.screenshot({path: full});
        const box = await pill.boundingBox();
        // cbf0bf9e1: the capsule is hidden at idle top, so there is nothing to compare.
        if (counts.upd === 0 && await page.evaluate(() => window.__holdScroller().scrollTop <= 4)) { assert.equal(box, null, `${id} ${state}: capsule hidden at the top with no pending update`); return; }
        assert(box, `${id} ${state}: home capsule is always visible outside overview`);
        assert.equal(box.height, 28, `${id} ${state}: capsule retains accepted 28px height`);
        const anchor = box;
        const clip = {x: Math.max(0, anchor.x + anchor.width / 2 - 200), y: Math.max(0, anchor.y - 20), width: 400, height: 76};
        const implCrop = await page.screenshot({clip});
        await ref.evaluate(counts => tt.set(counts), counts);
        await ref.evaluate(async () => {for(let n=0;n<32;n++) await new Promise(requestAnimationFrame);});
        const rb = await ref.locator('#cap').boundingBox();
        const refCrop = await ref.screenshot({clip: {x: rb.x + rb.width / 2 - 200, y: rb.y - 20, width: 400, height: 76}});
        const refPath = path.join(output, `ref-${id}-${state}.png`);
        fs.writeFileSync(refPath, refCrop);
        const pair = await refContext.newPage();
        await pair.setViewportSize({width: 808, height: 104});
        await pair.setContent(`<body style="margin:0;background:${theme === 'dark' ? '#141518' : '#f3f3f0'};font:12px system-ui;color:${theme === 'dark' ? '#eee' : '#222'}"><div style="display:flex;gap:8px"><div>Implementation · ${id} · ${state}<br><img src="data:image/png;base64,${implCrop.toString('base64')}"></div><div>Accepted reference<br><img src="data:image/png;base64,${refCrop.toString('base64')}"></div></div></body>`);
        const sbs = path.join(output, `sbs-${id}-${state}.png`);
        await pair.screenshot({path: sbs});
        await pair.close();
        const layout = await page.evaluate(() => {
          const rect = n => { if (!n) return null; const r = n.getBoundingClientRect(); return {x: r.x, y: r.y, w: r.width, h: r.height}; };
          const s = window.__holdScroller();
          return {scroll: s.scrollTop, toolbar: rect(document.querySelector('.board-toolbar')), wrap: rect(document.querySelector('.oq-wrap.wb-oq-top')), scroller: rect(s === document.scrollingElement ? document.documentElement : s)};
        });
        report.screenshots.push({id, state, full, reference: refPath, sideBySide: sbs, box, layout});
        // The capsule replaces the original top nav pill: it floats in the pill's zero-height sticky
        // wrapper over the card panel, so pending state never moves the toolbar.
        if (layout.scroll <= 4 && layout.toolbar) {
          if (topToolbarY === null) topToolbarY = layout.toolbar.y;
          assert(Math.abs(layout.toolbar.y - topToolbarY) < 0.5, `${id} ${state}: toolbar top ${layout.toolbar.y} moved from ${topToolbarY}`);
        }
        if (layout.scroller && !documentMode) assert(box.y >= layout.scroller.y - 0.5 && box.x >= layout.scroller.x - 0.5 && box.x + box.width <= layout.scroller.x + layout.scroller.w + 0.5, `${id} ${state}: capsule lies inside the card scroll area`);
        if (layout.wrap) assert(Math.abs(layout.wrap.h) < 0.5, `${id} ${state}: top pill wrapper stays zero-height`);
      }
      await page.evaluate(() => {window.__holdScroller().scrollTop = 0; window.wbPill.update();});
      await frames(14);
      // cbf0bf9e1: the top capsule hides at the very top with no pending update and nothing above.
      assert.equal(await pill.locator('.oq-arr').isVisible(), false, 'home arrow is hidden at idle top');
      assert.equal(await label.isVisible(), false);
      await capture('idle-top', {upd: 0, open: 0, ans: 0});
      await scrollToTopic('T04');
      await pill.waitFor({state: 'visible'});
      assert.equal(await pill.locator('.oq-arr').isVisible(), true, 'home arrow exists with zero updates');
      // Idle-scrolled arrow-only state: use a window before the questions are
      // above the viewport, without fabricating a pending inventory.
      await page.evaluate(() => {window.__holdScroller().scrollTop = 20; window.wbPill.update();});
      await frames(14);
      assert.equal(await label.isVisible(), false);
      assert.equal(await pill.locator('.oq-seg:visible').count(), 0);
      await capture('idle-scrolled', {upd: 0, open: 0, ans: 0});
      await scrollToTopic('T04');
      const stable = await geometry();
      await update('T04');
      assert.equal(await label.textContent(), '보드 업데이트 1');
      const one = await geometry();
      for (const a of stable.topics.filter(n => n.y > 120 && n.y < 800)) {
        const b = one.topics.find(n => n.name === a.name);
        assert(Math.abs(a.y - b.y) <= 1, `${id} ${a.name}: badge/marker does not change layout (${a.y} -> ${b.y})`);
      }
      assert.equal(await page.locator('[data-board-held="moved"] .board-hold-tag').first().textContent(), '이동됨 ↑');
      await capture('pending-1-chips', {upd: 1, open: 1, ans: 1});
      await update('T04');
      assert.equal(await label.textContent(), '보드 업데이트 1', 'same topic revised twice counts once');
      const freeCanvas = await page.evaluate(() => window.WsBoardAnchor.free());
      assert.equal(freeCanvas, !documentMode, 'fixture uses the intended board mode');
      // Designated-Y holds other visible topics too. The document-scrolled
      // fallback still holds only the current topic.
      await update('T05');
      assert.equal(await label.textContent(), freeCanvas ? '보드 업데이트 2' : '보드 업데이트 1');
      const nextTopic = page.locator('section[data-topic="T05"]');
      assert.equal(await nextTopic.getAttribute('data-board-held'), freeCanvas ? 'moved' : null);
      assert.equal(await nextTopic.evaluate(n => n.textContent.includes('Pending review update')), !freeCanvas);
      const chips = await pill.locator('.oq-seg:visible').evaluateAll(nodes => nodes.map(n => ({kind: n.dataset.k, label: getComputedStyle(n.firstElementChild).display, h: n.getBoundingClientRect().height, x: n.getBoundingClientRect().x})));
      assert.deepEqual(chips.map(c => c.kind), ['open', 'ans']);
      const lb = await label.boundingBox();
      assert(chips.every(c => c.label === 'none' && c.h === 18 && c.x > lb.x + lb.width), 'small count bubbles follow the plain update text');
      const style = await pill.evaluate(n => ({animation: getComputedStyle(n).animationName, duration: getComputedStyle(n).animationDuration}));
      assert.equal(style.animation, reduced ? 'none' : 'board-pill-sway');
      if (!reduced) assert.equal(style.duration, '2.6s');
      assert(await page.locator('section[data-topic="T04"] iframe.doc-frame').count() > 0, 'the static mount check includes a real hydrated panel');
      assert.equal(await page.locator('.board-hold-nudge').first().evaluate(n => getComputedStyle(n).animationName), reduced ? 'none' : 'board-hold-nudge');
      const mounts = await page.evaluate(() => window.__hold.mounts);
      await frames(24);
      assert.equal(await page.evaluate(() => window.__hold.mounts), mounts, 'static viewport has zero iframe mount churn');
      const before = await page.evaluate(() => ({morphs: window.__holdMorphs || 0, setters: window.__hold.setters.length, scrolls: window.__hold.scrolls.length}));
      await label.click();
      assert.deepEqual(await page.locator('.board-fresh').evaluateAll(nodes => nodes.map(n => n.dataset.topic).sort()), freeCanvas ? ['T04', 'T05'] : ['T04'], 'held topics flash once on explicit apply');
      assert.equal(await page.locator('.board-fresh').first().evaluate(n => getComputedStyle(n).animationDuration), reduced ? '0s' : '0.6s');
      await page.waitForFunction(() => window.__holdScroller().scrollTop <= 1);
      await page.waitForFunction(() => !window.wbHistory.keeping(window.__holdScroller()));
      await frames();
      const after = await page.evaluate(() => ({morphs: window.__holdMorphs || 0, pending: window.wbPill.pending, setters: window.__hold.setters, scrolls: window.__hold.scrolls, held: document.querySelectorAll('[data-board-held]').length}));
      assert.equal(after.morphs - before.morphs, 1, 'one release settle');
      assert.equal(after.pending, 0, 'click clears the distinct pending count');
      assert.equal(after.held, 0, 'released topics lose their hold indicators');
      const homeWrites = after.setters.slice(before.setters);
      const homeScrolls = after.scrolls.slice(before.scrolls);
      if (freeCanvas) {
        assert(homeWrites.every(s => s.stack.includes('freeLayout')), 'Designated-Y owns direct home scroll writes');
        const scroll = homeScrolls[0];
        if (scroll) assert.equal(scroll.args[0].behavior, 'smooth');
        else assert(homeWrites.some(s => s.value === 0), 'the home action reaches the top through the canvas');
      } else {
        assert.equal(homeWrites.length, 0, 'no keep compensation writer during document home navigation');
        assert(homeScrolls.length >= 1 && homeScrolls.every(s => s.args[0].top === 0), 'the navigation owner targets absolute zero');
        assert.equal(homeScrolls[0].args[0].behavior, 'auto', 'the reduced-motion document scroll uses auto behavior');
      }
      // The home arrow hides at idle top once pending clears.
      await capture('after-click', {upd: 0, open: 0, ans: 0});
      await page.waitForFunction(() => !document.querySelector('.board-fresh'));
      // Pending at top: current card T01 is staged until explicit apply.
      await page.evaluate(() => {window.__holdScroller().scrollTop = 0;});
      await frames(18);
      const topBefore = await geometry();
      await update('T01');
      assert.equal(await label.textContent(), '보드 업데이트 1');
      assert.equal(await label.isVisible(), true, 'pending text also shows at top');
      const topAfter = await geometry();
      assert.equal(topAfter.scroll, 0);
      for (const a of topBefore.topics.filter(n => n.y >= 0 && n.y < 700)) assert(Math.abs(a.y - topAfter.topics.find(n => n.name === a.name).y) <= 1, 'top pending signal does not change visible layout');
      await capture('pending-1', {upd: 1, open: 0, ans: 0});
      await update('T25');
      assert.equal(await label.textContent(), '보드 업데이트 1', 'unrelated offscreen change preserves the latched signal');
      assert.equal(await page.locator('section[data-topic="T01"]').getAttribute('data-board-held'), 'moved', 'held top topic retains its accent/tag until release');
      await update('T01');
      assert.equal(await label.textContent(), '보드 업데이트 1', 'a new revision still counts the latched topic once');
      await pill.locator('.oq-arr').click();
      await page.waitForFunction(() => window.wbPill.pending === 0);
      assert.equal(await page.locator('[data-board-held]').count(), 0, 'home arrow click releases held top topic');
      // Leaving a held card via board navigation releases its staged update automatically (03466dd2e).
      await scrollToTopic('T04');
      await update('T04');
      assert.equal(await page.evaluate(() => window.wbPill.pending), 1, 'setup: T04 is held while current');
      await page.evaluate(() => window.WsBoardAnchor.select(document.querySelector('section.topic[data-topic="T05"]')));
      await page.waitForFunction(() => window.wbPill.pending === 0);
      await frames();
      assert.equal(await page.locator('section[data-topic="T04"]').evaluate(n => n.textContent.includes('Pending review update')), true, 'leaving a held card admits its staged update');
      // Close the topic with the panel while it is in view.
      await scrollToTopic('T04');
      await update('T04', true);
      assert.equal(await page.locator('section[data-topic="T04"]').getAttribute('data-board-held'), 'closed');
      assert.equal(await page.locator('section[data-topic="T04"] .board-hold-tag').textContent(), '닫힘', 'server-closed staged topic gets the closed tag');
      await label.click();
      await page.waitForFunction(() => window.wbPill.pending === 0 && !window.wbHistory.keeping(window.__holdScroller()));
      // Overview hides the capsule; Escape closes overview.
      await page.locator('.board-overview').click();
      await page.waitForFunction(() => document.documentElement.hasAttribute('data-overview'));
      assert.equal(await pill.isVisible(), false, 'capsule hidden in overview');
      await page.waitForFunction(() => !document.querySelector('[data-overview-motion]'));
      await page.keyboard.press('Escape');
      await page.waitForFunction(() => !document.documentElement.hasAttribute('data-overview'));
      // Fresh canonical stream: under per-card staging (03466dd2e), offscreen
      // rises and new topics admit immediately into canonical order while the
      // current card's own updates stay held until apply.
      await page.request.post(origin + '/reset');
      await page.evaluate(() => window.addEventListener('pagehide', () => sessionStorage.removeItem('ws.dashboard.session-views')));
      await page.goto(origin + '/s/holdpill' + (documentMode ? '?document=1' : ''), {waitUntil: 'domcontentloaded'});
      await page.waitForFunction(() => window.wbPill?.update && window.wbHistory && document.querySelectorAll('section.topic').length === 25);
      if (!(await page.locator('#bcol').isVisible())) await page.locator('[data-tab="board"]').first().click();
      await page.evaluate(() => {window.__holdScroller = () => window.wbHistory.keep(document.querySelector('section.topic'), {capture: false}).scroller;});
      await page.evaluate(() => {window.__holdScroller().scrollTop = 0; window.wbPill.update();});
      await frames(18);
      await update('T20', false, true);
      assert.equal(await page.locator('section[data-topic="T20"]').evaluate(n => n.textContent.includes('Pending review update')), true, 'offscreen rise admits immediately under per-card staging');
      await update('NEW', false, true);
      assert.equal(await page.locator('section[data-topic="NEW"]').count(), 1, 'new topic admits immediately into canonical order');
      assert.equal((await geometry()).topics[0].name, 'NEW', 'new topic lands at canonical head');
      report.cases.push({id, staging: freeCanvas ? 'visible cards' : 'current card', releaseMorphs: 1, animation: style, homeBehavior: homeScrolls[0]?.args[0].behavior || 'auto', mounts, status: 'PASS'});
      console.log(`PASS ${id}: card staging, capsule geometry, bubbles, top, overview, one release, home scroll, leave release, no mount churn`);
      await refContext.close();
      await context.close();
    }
    assert.deepEqual(report.errors, [], 'no browser page errors (full stacks retained)');
  } catch (e) {
    report.failure = {message: e.message, stack: e.stack};
    if (active && !active.isClosed()) {
      await active.screenshot({path: path.join(output, 'failure.png')}).catch(() => {});
      report.failure.dom = await active.evaluate(() => ({pill: document.querySelector('.wb-oq-top')?.outerHTML, top: document.querySelector('#bcol')?.scrollTop, topics: [...document.querySelectorAll('section.topic')].slice(0, 8).map(n => ({name: n.dataset.topic, y: n.getBoundingClientRect().top, h: n.getBoundingClientRect().height, held: n.dataset.boardHeld}))})).catch(() => null);
    }
    throw e;
  } finally {
    fs.writeFileSync(path.join(output, 'browser-report.json'), JSON.stringify(report, null, 2) + '\n');
    await browser.close();
  }
})().catch(e => {console.error(e.stack || e); process.exitCode = 1;});
