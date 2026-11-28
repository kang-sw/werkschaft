// Run only through TestWhiteboardEffectsBrowser after an explicit slot START.
// The Go fixture serves production pages/assets/history/streams on port 0.
// Only store mutations and the provider-free agent-host boundary are synthetic.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, control, output] = process.argv.slice(2);
assert(base && control && output, 'pass disposable page/control URLs and retained artifacts');
for (const address of [base, control]) {
  const u = new URL(address);
  assert(['127.0.0.1', 'localhost', '[::1]'].includes(u.hostname), 'fixture is loopback only');
  assert(u.port && u.port !== '29417', 'never access the installed/shared daemon');
}

async function action(name) {
  const res = await fetch(control + '?action=' + encodeURIComponent(name), { method: 'POST' });
  assert.equal(res.status, 200, 'fixture action ' + name);
  return res.json();
}
async function until(check, message) {
  const end = Date.now() + 10000;
  while (Date.now() < end) {
    if (await check()) return;
    await new Promise(resolve => setTimeout(resolve, 40));
  }
  assert.fail(message);
}

(async () => {
  fs.mkdirSync(output, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  try {
    for (const width of [1280, 360]) {
      const context = await browser.newContext({ viewport: { width, height: 900 }, locale: 'en-US', serviceWorkers: 'allow', reducedMotion: 'reduce' });
      context.setDefaultTimeout(10000);
      const errors = [], consoleErrors = [], unexpected = [], history = [], requests = [];
      const serviceWorkerAttempts = [], serviceWorkerEvents = [];
      await context.exposeBinding('__wsEffectsServiceWorkerAttempt', ({ frame }, scriptURL) => {
        serviceWorkerAttempts.push({ frame: frame.url(), scriptURL });
      });
      context.on('serviceworker', worker => serviceWorkerEvents.push(worker.url()));
      // Playwright's blocker reads Navigator even in opaque sandboxed frames.
      // Keep their native restriction and block registrations in other frames.
      await context.addInitScript(() => {
        if (self.origin === 'null') return;
        const serviceWorkers = navigator.serviceWorker;
        if (!serviceWorkers) return;
        Object.getPrototypeOf(serviceWorkers).register = async function(scriptURL) {
          await globalThis.__wsEffectsServiceWorkerAttempt(typeof scriptURL === 'string' ? scriptURL : { type: typeof scriptURL });
          throw new Error('Service worker registration is forbidden in the effects fixture');
        };
      });
      const tab = await context.newPage();
      tab.on('pageerror', e => errors.push(e.message));
      tab.on('console', m => { if (m.type() === 'error') consoleErrors.push(m.text()); });
      tab.on('request', r => {
        requests.push({ method: r.method(), url: r.url() });
        if (new URL(r.url()).pathname === '/s/effectsession/history') history.push(r.url());
      });
      await context.route('**/*', route => {
        const u = new URL(route.request().url());
        if (u.pathname === '/favicon.ico' && u.origin === new URL(base).origin) return route.fulfill({ status: 204 });
        if (['http:', 'https:'].includes(u.protocol) && ![new URL(base).origin, new URL(control).origin].includes(u.origin)) {
          unexpected.push(u.href);
          return route.abort();
        }
        return route.continue();
      });
      let page;
      async function active(mode) {
        await until(async () => {
          const el = tab.locator('iframe.sframe.on');
          if (!(await el.count())) return false;
          const handle = await el.elementHandle();
          page = handle && await handle.contentFrame();
          return page && (!mode || await page.locator('html').getAttribute('data-mode') === mode);
        }, 'active production session frame reaches ' + mode);
      }
      async function shot(name) {
        if (process.env.WS_BOARD_ACCEPTANCE_LOGIC_ONLY === '1') return;
        await tab.screenshot({ path: path.join(output, `${width}-${name}.png`), fullPage: true });
      }
      async function showTab(name) {
        const pane = page.locator(name === 'board' ? '#bcol' : '#convo');
        if (!(await pane.isVisible())) await page.locator(`[data-tab="${name}"]`).first().click();
        await pane.waitFor({ state: 'visible' });
      }
      async function savedOnly() {
        assert.equal(await page.locator('html').getAttribute('data-mode'), 'history');
        assert.equal(await page.locator('#composer, #ctext, #apbox, #r-tree, #r-convlive, #r-ctx, #r-cmds, #r-sline').count(), 0, 'saved history contains no fabricated live controls/activity');
      }
      async function effectsKeys() {
        return page.locator('.wbeffects[data-entry]').evaluateAll(nodes => nodes.map(e => e.getAttribute('data-entry')));
      }
      async function groupedItems(card) {
        for (const topic of await card.locator('.wbupdate-topic').all()) {
          assert(await topic.locator('.wbupdate-items').count() <= 1, 'one chip group per topic');
          assert.equal(await topic.locator('.wbupdate-row .wbupdate-item-id, .wbupdate-class, .wbupdate-options').count(), 0, 'no redundant item rows');
          if (await topic.locator('.wbupdate-items').count() === 1) {
            const order = await topic.evaluate(t => {
              const items = t.querySelector('.wbupdate-items');
              const ib = items.getBoundingClientRect();
              const others = [...t.querySelectorAll('.wbupdate-rows > .wbupdate-row, .wbupdate-rows > .wbupdate-panel-wrap')];
              return others.map(o => ({
                dom: !!(o.compareDocumentPosition(items) & Node.DOCUMENT_POSITION_FOLLOWING),
                visual: o.getBoundingClientRect().bottom <= ib.top + 0.5,
              }));
            });
            assert(order.every(o => o.dom && o.visual), 'question chips render below panels and logs (DOM and visual order)');
          }
          for (const chip of await topic.locator('.wbupdate-items > a.wbupdate-item-id').all()) {
            const target = await chip.getAttribute('data-wb-jump');
            assert(/^item-[1-9]\d{0,5}$/.test(target), 'chips keep validated item targets');
            assert.equal(await chip.getAttribute('href'), '#' + target, 'chip href points at its validated item target');
            assert.equal(await chip.getAttribute('data-wb-topic'), await topic.getAttribute('data-wb-topic'), 'chip belongs to its topic group');
          }
        }
      }
      async function arrived(update, beforeKeys) {
        assert(update.key && !beforeKeys.includes(update.key), 'append returns a genuinely new effects ring key');
        const fresh = page.locator(`.wbeffects[data-entry="${update.key}"]`);
        await fresh.waitFor({ state: 'attached' });
        assert.equal(await fresh.count(), 1, 'new carrier renders as its own update card');
        assert.equal(await fresh.locator('.wbeffect-details, .wbeffect-values').count(), 0, 'saved-change diagnostics are removed');
        await groupedItems(fresh);
        assert((await fresh.locator('.wbupdate-panel-body').textContent()).includes('PANEL-PAYLOAD-ONLY'), 'panel preview shows bounded panel lead-in');
        assert.equal(await fresh.locator('.wbupdate-panel-body .wbupdate-preview-badge').count(), 1, 'fresh panel preview collapses unsafe fragments into one badge');
        assert.equal(await fresh.locator('.wbupdate-panel-body strong').count(), 1, 'fresh panel preview renders safe Markdown emphasis');
        assert.equal(await fresh.locator('.wbeffect-fallback, script, img, iframe, canvas, style, pre.mermaid').count(), 0, 'a fresh effects card has no fallback or heavy/unsafe embeds');
        assert.equal(await fresh.locator('.wbupdate-panel-body svg, .wbupdate-log svg').count(), 0, 'fresh preview and log emit no inline svg');
        assert.equal(await fresh.locator('.wbupdate-log strong').count(), 0, 'log rows remain plain literal text');
        assert((await fresh.locator('.wbupdate-log .wbupdate-text').allTextContents()).includes(update.payload), 'newly arriving hostile log remains exact literal text');
        assert.equal(await page.evaluate(() => typeof window.effectsAttack), 'undefined', 'new effects payload never executes');
      }
      // Draft restore is an async IndexedDB read; the live frame's #grid stays
      // inert until it lands (dashboard-lifecycle startDraftRecovery), so read
      // the composer only after that gate opens.
      async function draftRestored() {
        await page.waitForFunction(() => { const g = document.getElementById('grid'); return !!g && !g.inert; });
      }
      async function waitStored(draft) {
        await page.waitForFunction(text => Object.keys(localStorage).some(k => (localStorage.getItem(k) || '').includes(text)), draft);
      }
      async function openModal() {
        if (await tab.locator('#railbtn').isVisible() && !(await tab.locator('html').evaluate(e => e.classList.contains('rail-open')))) await tab.locator('#railbtn').click();
        await tab.locator('.rwt .wthd').first().hover();
        await tab.locator('.rnew').first().click();
        const modal = tab.getByRole('dialog', { name: 'New session', exact: true });
        await modal.waitFor({ state: 'visible' });
        const rect = await modal.boundingBox();
        assert(rect && rect.x >= 0 && rect.x + rect.width <= width + 1 && rect.y >= 0 && rect.y + rect.height <= 901, 'production new-session modal fits viewport');
        return modal;
      }
      try {
        const keys = await action('reset');
        await tab.goto(base + '/s/effectsession');
        await active('history');
        await savedOnly();
        await showTab('convo');
        const effect = () => page.locator(`.wbeffects[data-entry="${keys.focus}"]`);
        const fallback = () => page.locator(`.wbeffects[data-entry="${keys.fallback}"]`);
        await effect().waitFor({ state: 'attached' });
        await effect().evaluate(e => { e.effectsBrowserIdentity = 'original-effect-node'; });
        assert.equal(await effect().locator('.wbeffect-details, .wbeffect-values').count(), 0, 'saved-change diagnostics are absent');
        await groupedItems(effect());
        const values = await effect().locator('.wbupdate-log .wbupdate-text').allTextContents();
        assert(values.some(s => s.includes('<img src=x onerror="window.effectsAttack=1">') && s.includes('**plain text**')), 'valid log stays literal, including HTML and Markdown syntax');
        assert.equal(await effect().locator('script, img, iframe, canvas, style, pre.mermaid').count(), 0, 'effects never execute or emit heavy/unsafe embeds');
        assert.equal(await effect().locator('.wbupdate-panel-body svg, .wbupdate-log svg').count(), 0, 'saved preview and log emit no inline svg');
        assert.equal(await effect().locator('.wbupdate-log strong').count(), 0, 'saved log rows never render Markdown');
        assert.equal(await page.evaluate(() => typeof window.effectsAttack), 'undefined', 'saved effects payload never executes');
        assert((await effect().textContent()).includes('Panel recap only'), 'panel has a recap');
        assert((await effect().locator('.wbupdate-panel-body').textContent()).includes('PANEL-PAYLOAD-ONLY'), 'panel body shows bounded preview');
        assert.equal(await effect().locator('.wbupdate-panel-body .wbupdate-preview-badge').count(), 1, 'saved panel script collapsed into HTML preview badge');
        assert((await fallback().textContent()).includes('Details unavailable or unsupported'));
        assert((await fallback().locator('.wbeffect-fallback').textContent()).includes('"v":99'));
        assert.equal(await fallback().locator('img, script, iframe, .wbeffect-values').count(), 0, 'unsupported envelope is escaped fallback, not invented effects');
        await shot('saved-escaped-and-fallback');

        async function verifyPreviewBodyJump(entryKey, label) {
          const card = page.locator(`.wbeffects[data-entry="${entryKey}"]`);
          const preview = card.locator('.wbupdate-panel-body');
          await preview.waitFor({ state: 'attached' });
          assert.equal(await preview.evaluate(el => getComputedStyle(el).cursor), 'pointer', `${label}: preview body has pointer cursor`);
          assert.equal(await preview.locator('a, button').count(), 1, `${label}: preview contains only its markdown link (no nested button/anchor wrapper)`);
          assert.equal(await preview.evaluate(el => !el.closest('a, button')), true, `${label}: preview body is not wrapped inside an anchor or button`);

          // Non-collapsed selection inside preview body must not trigger jump.
          await preview.evaluate(el => {
            const p = el.querySelector('p');
            const range = document.createRange();
            range.selectNodeContents(p.firstChild);
            const sel = window.getSelection();
            sel.removeAllRanges();
            sel.addRange(range);
            p.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 }));
            sel.removeAllRanges();
          });
          assert.notEqual(await page.evaluate(() => document.activeElement && document.activeElement.id), 'p1', `${label}: selection drag does not hijack click to jump`);

          // Interactive link inside preview body is not hijacked by panel jump.
          const linkHijacked = await preview.evaluate(el => {
            const a = el.querySelector('a[href^="https://"]');
            let preventedByJump = null;
            const guard = ev => { preventedByJump = ev.defaultPrevented; ev.preventDefault(); };
            document.addEventListener('click', guard, { once: true });
            a.click();
            return preventedByJump;
          });
          assert.equal(linkHijacked, false, `${label}: markdown link inside preview body is not hijacked`);
          assert.notEqual(await page.evaluate(() => document.activeElement && document.activeElement.id), 'p1', `${label}: markdown link click does not jump board`);

          // Clicking preview body text jumps to #p1 (not the outer topic).
          await preview.locator('p').evaluate(p => p.firstChild && p.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 })));
          await until(async () => await page.evaluate(() => {
            const ae = document.activeElement;
            return !!ae && ae.id === 'p1';
          }), `${label}: clicking panel preview body focuses #p1 instead of outer topic`);
          await page.evaluate(() => { if (document.activeElement) document.activeElement.blur(); });
          await showTab('convo');
        }
        await verifyPreviewBodyJump(keys.navFocus, 'history-replayed');

        const savedKeys = await effectsKeys();
        const refresh = await action('append');
        await page.getByText(refresh.marker, { exact: true }).waitFor({ state: 'attached' });
        await arrived(refresh, savedKeys);
        assert.equal(await effect().evaluate(e => e.effectsBrowserIdentity === 'original-effect-node'), true, 'real stream refresh preserves keyed update node');
        assert.equal(await page.getByText('old-history-marker', { exact: true }).count(), 0, 'old marker is outside initial ring window');
        const older = tab.waitForResponse(r => new URL(r.url()).pathname === '/s/effectsession/history' && r.status() === 200);
        await page.locator('#convo').evaluate(e => { e.scrollTop = 0; e.dispatchEvent(new Event('scroll')); });
        const historyResponse = await older;
        const historyBody = await historyResponse.json();
        assert(historyBody.html.includes('old-history-marker'), 'production history handler supplies older records');
        await page.getByText('old-history-marker', { exact: true }).waitFor({ state: 'attached' });
        assert(history.length > 0, 'real older-history paging occurred');
        assert.equal(await effect().evaluate(e => e.effectsBrowserIdentity === 'original-effect-node'), true, 'paging retains existing keyed entry identity');
        await effect().scrollIntoViewIfNeeded();
        await shot('expanded-after-refresh-and-history');

        await action('live');
        await active('live');
        await page.locator('#ctext + .wsc-editor').waitFor();
        const draft = `Preserve the effects draft at ${width}px`;
        await page.locator('#ctext + .wsc-editor').fill(draft);
        await waitStored(draft);
        const liveNav = await action('append_nav');
        await page.getByText('Jump target ' + liveNav.marker, { exact: true }).waitFor({ state: 'attached' });
        await verifyPreviewBodyJump(liveNav.key, 'live-streamed');
        const liveKeys = await effectsKeys();
        const liveRefresh = await action('append');
        await page.getByText(liveRefresh.marker, { exact: true }).waitFor({ state: 'attached' });
        await arrived(liveRefresh, liveKeys);
        assert.equal(await page.locator('#ctext').inputValue(), draft, 'effects refresh never changes composer input');
        await action('stale');
        await page.locator('#dash-banner').waitFor({ state: 'visible' });
        assert((await page.locator('#dash-banner').textContent()).includes('went away'));
        assert.equal(await page.locator('#ctext').inputValue(), draft, 'adapter loss banners instead of dropping an in-memory draft');
        await tab.reload();
        await active('history');
        await savedOnly();
        await tab.reload();
        await active('history');
        await savedOnly();
        await action('live');
        await active('live');
        await page.locator('#ctext + .wsc-editor').waitFor();
        await draftRestored();
        assert.equal(await page.locator('#ctext').inputValue(), draft, 'saved-history reloads do not overwrite stored composer draft');
        await action('absent');
        await page.locator('#dash-banner').waitFor({ state: 'visible' });
        assert.equal(await page.locator('#ctext').inputValue(), draft, 'no-adapter loss also preserves input');
        await tab.reload();
        await active('history');
        await savedOnly();
        await action('live');
        await active('live');
        await page.locator('#ctext + .wsc-editor').waitFor();
        await draftRestored();
        assert.equal(await page.locator('#ctext').inputValue(), draft, 'the stored draft returns after the no-adapter reload and live re-attach');
        await shot('live-restored-draft');

        // Follow a real link in the sandboxed, production-rendered board.
        // A conversation update must not drag the jumped-to board target.
        await showTab('board');
        await page.locator('section.topic[data-topic="navigation"]').scrollIntoViewIfNeeded();
        const bodyFrame = page.locator('section.topic[data-topic="navigation"] iframe.doc-frame').first();
        await bodyFrame.waitFor();
        const body = await (await bodyFrame.elementHandle()).contentFrame();
        const target = page.locator('#p1');
        async function jumpGeometry() {
          const geometry = await target.evaluate(e => {
            const scroller = el => {
              for (let p = el.parentElement; p; p = p.parentElement) {
                if (p.matches('section.topic[data-topic], #bcol, .gal-pbody') && /(auto|scroll)/.test(getComputedStyle(p).overflowY)) return p;
              }
              return el.closest('#bcol, .gal-pbody');
            };
            const r = e.getBoundingClientRect();
            const board = scroller(e);
            const b = board.getBoundingClientRect();
            const clip = { left: b.left + board.clientLeft, top: b.top + board.clientTop };
            clip.right = clip.left + board.clientWidth;
            clip.bottom = clip.top + board.clientHeight;
            const css = getComputedStyle(e);
            return {
              open: e.open, rendered: r.width > 0 && r.height > 0 && css.display !== 'none' && !['hidden', 'collapse'].includes(css.visibility),
              target: { left: r.left, top: r.top, right: r.right, bottom: r.bottom }, clip,
              offscreen: r.bottom <= clip.top || r.top >= clip.bottom || r.right <= clip.left || r.left >= clip.right,
              inBoard: r.left >= clip.left && r.right <= clip.right && r.top >= clip.top && r.bottom <= clip.bottom
            };
          });
          const bounds = await target.boundingBox();
          const viewport = tab.viewportSize();
          return { ...geometry, bounds, viewport, inBrowser: !!bounds && bounds.x >= 0 && bounds.y >= 0 && bounds.x + bounds.width <= viewport.width && bounds.y + bounds.height <= viewport.height };
        }
        // Frame heights settle in a later rAF (dashboard-frames applyFrameHeights)
        // after scrollIntoViewIfNeeded, so the target's size and place keep moving
        // for a while. Wait until the board's scroll height and the target's
        // rect repeat, then put the board explicitly where the target lies outside
        // its clip (below it, else above it) instead of trusting the earlier scroll.
        {
          let last = '', same = 0;
          await until(async () => {
            const now = await target.evaluate(e => {
              const scroller = el => {
                for (let p = el.parentElement; p; p = p.parentElement) {
                  if (p.matches('section.topic[data-topic], #bcol, .gal-pbody') && /(auto|scroll)/.test(getComputedStyle(p).overflowY)) return p;
                }
                return el.closest('#bcol, .gal-pbody');
              };
              const r = e.getBoundingClientRect(), b = scroller(e);
              return JSON.stringify([b.scrollHeight, Math.round(r.top), Math.round(r.height), [...document.querySelectorAll('iframe.doc-frame')].map(f => f.style.height).join()]);
            });
            same = now === last ? same + 1 : 0;
            last = now;
            return same >= 8;
          }, 'the board target keeps one size and place');
          await target.evaluate(async e => {
            const scroller = el => {
              for (let p = el.parentElement; p; p = p.parentElement) {
                if (p.matches('section.topic[data-topic], #bcol, .gal-pbody') && /(auto|scroll)/.test(getComputedStyle(p).overflowY)) return p;
              }
              return el.closest('#bcol, .gal-pbody');
            };
            const b = scroller(e), frames = () => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)));
            const clip = () => { const c = b.getBoundingClientRect(); return { top: c.top + b.clientTop, bottom: c.top + b.clientTop + b.clientHeight }; };
            // The settled panel is taller than the board (its frame's real
            // height), so it cannot lie wholly outside the clip: what must
            // start outside is its top edge, the part the jump aligns.
            const gone = () => { const r = e.getBoundingClientRect(), c = clip(); return r.height > c.bottom - c.top ? r.top < c.top : r.bottom <= c.top || r.top >= c.bottom; };
            for (const dir of [1, -1]) {
              for (let i = 0; i < 4 && !gone(); i++) {
                const r = e.getBoundingClientRect(), c = clip();
                b.scrollTop += dir > 0 ? r.top - c.top + 24 : r.top - c.bottom - 24;
                await frames();
              }
              if (gone()) return;
            }
          });
        }
        const preJump = await jumpGeometry();
        fs.writeFileSync(path.join(output, `${width}-jump-before.json`), JSON.stringify(preJump, null, 2));
        const preTall = preJump.target.bottom - preJump.target.top > preJump.clip.bottom - preJump.clip.top;
        assert(preJump.rendered && (preTall ? preJump.target.top < preJump.clip.top : preJump.offscreen), 'jump target starts outside the board clipping viewport (its top edge, when taller than the board): ' + JSON.stringify(preJump));
        // Placing the board off the target can unmount its frame (offscreen board
        // frames are paced remounts), which detaches the Frame taken before:
        // bring the link's iframe back into view and resolve the live Frame again.
        await bodyFrame.scrollIntoViewIfNeeded();
        let liveBody = null;
        await until(async () => {
          const f = await (await bodyFrame.elementHandle()).contentFrame();
          if (f && (await f.getByRole('link', { name: 'Jump to target', exact: true }).count())) liveBody = f;
          return !!liveBody;
        }, 'the navigation frame mounts its jump link');
        await liveBody.getByRole('link', { name: 'Jump to target', exact: true }).click();
        let postJump = null;
        try {
          await until(async () => {
            postJump = await jumpGeometry();
            // A target taller than the board cannot sit inside it: the jump
            // top-aligns it (stream-view measure), so its top is what must land.
            const tall = postJump.target.bottom - postJump.target.top > postJump.clip.bottom - postJump.clip.top;
            const topIn = postJump.target.top >= postJump.clip.top - 1 && postJump.target.top < postJump.clip.bottom;
            const browserTopIn = !!postJump.bounds && postJump.bounds.y >= 0 && postJump.bounds.y < postJump.viewport.height;
            return postJump.open && postJump.rendered && (tall ? topIn && browserTopIn : postJump.inBoard && postJump.inBrowser);
          }, 'production jump opens and reveals target inside the board and browser viewports');
        } finally {
          fs.writeFileSync(path.join(output, `${width}-jump-after.json`), JSON.stringify(postJump, null, 2));
        }
        await target.waitFor({ state: 'visible' });
        const before = await target.boundingBox();
        assert(before && before.y >= 0 && before.y < 900, 'production jump brings target into view');
        const jumpedKeys = await effectsKeys();
        const jumpedRefresh = await action('append');
        await page.getByText(jumpedRefresh.marker, { exact: true }).waitFor({ state: 'attached' });
        await arrived(jumpedRefresh, jumpedKeys);
        await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
        const after = await target.boundingBox();
        assert(after && Math.abs(after.y - before.y) <= 16, 'effects refresh preserves the followed board target');
        assert.equal(await page.locator('#ctext').inputValue(), draft, 'the draft survives the followed-target effects refresh');
        await shot('jump-follow-after-effects-refresh');

        // Modal completion runs from the real HTTP response, not an SSE
        // attachment. Held synthetic responses make busy/generation observable.
        const initialRequests = (await action('requests')).count;
        let modal = await openModal();
        const start = modal.locator('.ns-cta.go');
        await start.click();
        await until(async () => (await action('requests')).count === initialRequests + 1, 'one creation request reaches synthetic host');
        assert.equal(await start.isDisabled(), true, 'Start is disabled while the creation request is held');
        assert((await modal.textContent()).includes('Starting…'));
        await start.dispatchEvent('click');
        assert.equal((await action('requests')).count, initialRequests + 1, 'busy blocks duplicate creation');
        await shot('modal-pending');
        await action('complete');
        await tab.waitForURL(base + '/s/createdsession');
        await active('board');
        assert.equal(await modal.isVisible(), false, 'HTTP 201 completes and closes active modal without attach events');
        await tab.goto(base + '/s/effectsession');
        await active('live');
        await draftRestored();
        assert.equal(await page.locator('#ctext').inputValue(), draft, 'modal completion saves the originating session draft');
        modal = await openModal();
        await modal.locator('.ns-cta.go').click();
        await until(async () => (await action('requests')).count === initialRequests + 2, 'second held creation request arrives');
        await modal.getByRole('button', { name: 'Close', exact: true }).click();
        modal = await openModal();
        await action('complete');
        await tab.waitForURL(base + '/s/createdsession');
        await active('board');
        assert.equal(await modal.isVisible(), true, 'late prior-generation success never closes a newer modal');
        assert.equal(await modal.locator('.ns-cta.go').isDisabled(), false, 'the newer modal stays startable');
        await modal.getByRole('button', { name: 'Close', exact: true }).click();
        await shot('modal-generation-completed');
        assert.deepEqual(serviceWorkerAttempts, [], 'no service worker registration attempts');
        assert.deepEqual(serviceWorkerEvents, [], 'no service worker creations');
        assert.deepEqual(context.serviceWorkers(), [], 'no remaining service workers');
        assert.deepEqual(errors, [], 'no production page errors');
        assert.deepEqual(consoleErrors, [], 'no browser console errors');
        assert.deepEqual(unexpected, [], 'no external network access');
        console.log(`PASS ${width}px: real escaped effects/fallback, refresh/history keyed identity, no/stale/live adapter transitions, saved-history controls, drafts, board jump-follow, modal completion/generation`);
      } catch (e) {
        try { await shot('failure'); } catch (_) {}
        throw e;
      } finally {
        fs.writeFileSync(path.join(output, `${width}-service-workers.json`), JSON.stringify({ attempts: serviceWorkerAttempts, events: serviceWorkerEvents, remaining: context.serviceWorkers().map(worker => worker.url()) }, null, 2));
        fs.writeFileSync(path.join(output, `${width}-console-and-requests.json`), JSON.stringify({ errors, consoleErrors, unexpected, history, requests }, null, 2));
        await context.close();
      }
    }
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
