// Run by TestShellTopbarBrowser with real pages and disposable stores.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, shots, mockup] = process.argv.slice(2);

const at = Date.now();
const usage = { warnPercent: 80, critPercent: 95, entries: [
  { harness: 'claude', source: 'fixture', asOf: at - 42 * 60000, windows: [
    { kind: 'five_hour', percent: 26, asOf: at - 42 * 60000, resetsAt: at + 2 * 3600000 },
    { kind: 'seven_day', percent: 53, asOf: at - 42 * 60000, resetsAt: at + 3 * 86400000 }] },
  { harness: 'codex', source: 'fixture', asOf: at, windows: [
    { kind: 'seven_day', percent: 79, asOf: at, resetsAt: at + 2 * 86400000 }] }] };

function shot(page, name, opts) {
  if (!shots) return Promise.resolve();
  fs.mkdirSync(shots, { recursive: true });
  return page.screenshot(Object.assign({ path: path.join(shots, name + '.png') }, opts || {}));
}

// geometry reads the shell's strip, its items, the rail and the framed
// page's right-end handles (the frame fills the window, so its coordinates
// are the shell's).
function geometry(page) {
  return page.evaluate(() => {
    const box = el => {
      if (!el) return null;
      const b = el.getBoundingClientRect(), cs = getComputedStyle(el);
      if (!(b.width || b.height) || cs.visibility === 'hidden' || cs.display === 'none') return null;
      return { l: b.left, t: b.top, r: b.right, b: b.bottom, w: b.width, h: b.height };
    };
    const top = document.querySelector('.dwrap > .top'), cs = getComputedStyle(top);
    const left = top.querySelector(':scope > .top-left') || top;
    const center = top.querySelector(':scope > .top-center');
    const items = [...left.querySelectorAll(':scope > :not(.r-head), :scope > .r-head > *')]
      .map(e => ({ cls: e.className, box: box(e) })).filter(i => i.box);
    const fdoc = document.querySelector('.sframe.on').contentDocument;
    const handles = [...fdoc.querySelectorAll('.tk-handle, .dr-search-handle')].map(box).filter(Boolean);
    const rail = document.querySelector('#rail');
    return { vw: innerWidth, vh: innerHeight, top: box(top), left: box(left), center: box(center), padR: parseFloat(cs.paddingRight), cls: top.className,
      rail: box(rail), railZ: getComputedStyle(rail).zIndex, railbtn: box(document.querySelector('#railbtn')),
      search: box(document.querySelector('#qsslot #tkqs')),
      usage: box(document.querySelector('.usage-toggle')), host: box(document.querySelector('.top .host.sse')),
      items, handles, open: document.documentElement.classList.contains('rail-open') };
  });
}

function checkStrip(label, g) {
  assert.ok(g.top, label + ': header shows');
  assert.equal(g.top.t, 0, label + ': header hangs from the top edge');
  assert.equal(g.top.h, 36, label + ': header is one 36px strip');
  const end = g.left ? g.left.r : g.top.r - g.padR;
  for (const it of g.items) {
    if (!/\b(tph|back|tcen)\b/.test(it.cls)) continue;
    assert.ok(it.box.t >= 0 && it.box.b <= 36.5, `${label}: ${it.cls} stays inside 36px strip (${it.box.t}..${it.box.b})`);
    assert.ok(it.box.h >= 20 && it.box.h <= 36, `${label}: ${it.cls} fits inside 36px strip (h=${it.box.h})`);
    assert.ok(it.box.r <= end + 0.5, `${label}: ${it.cls} ends at ${it.box.r}, past left zone ${end}`);
    if (g.search) assert.ok(it.box.r <= g.search.l + 0.5, `${label}: ${it.cls} (${it.box.r}) collides with centered search at ${g.search.l}`);
    for (const h of g.handles) assert.ok(it.box.r <= h.l + 0.5, `${label}: ${it.cls} (${it.box.r}) collides with a right-end handle at ${h.l}`);
  }
  if (g.search) {
    assert.ok(g.search.t >= 0 && g.search.b <= 36.5, `${label}: centered search stays inside 36px strip`);
    for (const h of g.handles) assert.ok(g.search.r <= h.l + 0.5, `${label}: centered search (${g.search.r}) collides with right-end handle at ${h.l}`);
  }
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const report = [];
  try {
    if (mockup && shots) {
      // The approved mockup's states, for the side-by-side look.
      const page = await browser.newPage({ viewport: { width: 1680, height: 900 } });
      await page.goto(pathToFileURL(mockup).href);
      const frame = page.locator('.sr4-frame');
      await frame.screenshot({ path: path.join(shots, 'mockup-wide.png') });
      await page.locator('.sr4-h[data-a="usage"]').click();
      await frame.screenshot({ path: path.join(shots, 'mockup-wide-usage.png') });
      await page.locator('.sr4-cb[data-k="narrow"]').click();
      await frame.screenshot({ path: path.join(shots, 'mockup-narrow.png') });
      await page.locator('.sr4-h[data-a="tg"]').click();
      await frame.screenshot({ path: path.join(shots, 'mockup-narrow-open.png') });
      await page.close();
    }
    for (const theme of ['dark', 'light']) {
      const context = await browser.newContext({ viewport: { width: 1920, height: 900 }, colorScheme: theme });
      await context.addInitScript(() => {
        if (self !== top) return;
        const workers = navigator.serviceWorker;
        if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
      });
      await context.route('**/*', route => {
        const url = new URL(route.request().url());
        if (url.origin !== base) return route.abort();
        if (url.pathname === '/rail/usage') return route.fulfill({ json: usage });
        return route.continue();
      });
      const page = await context.newPage();
      const errors = [];
      page.on('pageerror', e => errors.push(e.message));
      await page.goto(base + '/s/livesession');
      await page.waitForFunction(() => document.querySelector('.dwrap > .top').style.minHeight !== '');
      await page.frameLocator('.sframe.on').locator('#grid').waitFor();
      await page.locator('.usage-number').first().waitFor();
      await page.waitForTimeout(300);

      // Wide: the Variant D topbar spans full width (0..vw) while the docked rail
      // sits flush on the left below the 36px topbar and ends above the 26px bottom bar;
      // #railbtn stays visible in Top-Left to toggle wide collapse.
      let g = await geometry(page);
      checkStrip(`${theme}/1920`, g);
      assert.deepEqual([g.rail.l, g.rail.t, g.rail.b], [0, 36, g.vh - 26], `${theme}/1920: rail spans flush left between 36px topbar and 26px bottom bar`);
      assert.equal(g.rail.w, 280, `${theme}/1920: default rail width is 280px`);
      assert.equal(Math.round(g.top.l), 0, `${theme}/1920: full-width topbar starts at x=0`);
      assert.equal(Math.round(g.top.w), g.vw, `${theme}/1920: full-width topbar spans viewport`);
      assert.ok(g.railbtn && g.railbtn.w === 26 && g.railbtn.h === 26, `${theme}/1920: rail toggle button sits in Top-Left`);
      assert.ok(g.search && Math.abs((g.search.l + g.search.r) / 2 - g.vw / 2) <= 2, `${theme}/1920: search pill is centered at vw/2`);
      assert.equal(await page.locator('#shell-bottom').evaluate(el => el.classList.contains('bu')), false, `${theme}/1920: plan usage shows in full in bottom bar`);
      // The badge mark and its stale clock stay clear of the columns inside the bottom bar.
      const marks = await page.evaluate(() => [...document.querySelectorAll('#shell-bottom .usage-harness')].map(h => {
        const mark = h.querySelector('.usage-mark').getBoundingClientRect(), cols = h.querySelector('.usage-columns').getBoundingClientRect();
        const clock = h.querySelector('.usage-clock'), badge = h.querySelector('.usage-mark .hb');
        return { mark: mark.right, clock: clock ? clock.getBoundingClientRect().right : null, cols: cols.left, badge: [badge.offsetWidth, badge.offsetHeight] };
      }));
      assert.equal(marks.length, 2);
      for (const m of marks) {
        assert.deepEqual(m.badge, [19, 12], `${theme}: usage mark is the small badge`);
        assert.ok(m.mark <= m.cols + 1 && (m.clock === null || m.clock <= m.cols + 1), `${theme}: usage mark overlaps its columns ${JSON.stringify(m)}`);
      }
      report.push({ theme, width: 1920, rail: g.rail, top: g.top, items: g.items.map(i => i.cls) });
      await shot(page, `${theme}-wide`);
      await shot(page, `${theme}-wide-strip`, { clip: { x: 0, y: 0, width: 1920, height: 60 } });
      // The usage popover opens upward above the bottom-right handle, clamped inside the viewport.
      await page.locator('#shell-bottom .usage-toggle').click();
      const dialog = page.locator('.usage-pop');
      await dialog.waitFor({ state: 'visible' });
      const pop = await dialog.boundingBox(), handle = await page.locator('#shell-bottom .usage-toggle').boundingBox();
      assert.ok(pop.y + pop.height <= handle.y + 1, `${theme}: usage popover opens upward above its handle`);
      assert.ok(pop.x >= 8 && pop.x + pop.width <= g.vw - 7, `${theme}: usage popover stays clamped horizontally`);
      assert.equal(await page.locator('#shell-bottom .usage-toggle').getAttribute('aria-expanded'), 'true');
      await shot(page, `${theme}-wide-usage`);
      await page.keyboard.press('Escape');
      await dialog.waitFor({ state: 'hidden' });

      if (theme === 'dark') {
        // From 1600px the rail stays permanently docked (never evicted on pin).
        await page.setViewportSize({ width: 1800, height: 900 });
        await page.evaluate(() => document.documentElement.setAttribute('data-gal', 'narrow'));
        await page.waitForTimeout(100);
        g = await geometry(page);
        checkStrip('dark/1800/narrow', g);
        assert.ok(g.railbtn && g.railbtn.w === 26, 'dark/1800/narrow: rail stays docked, collapse button stays in Top-Left');
        assert.equal(g.rail.l, 0, 'dark/1800/narrow: rail stays docked flush at left edge');
        assert.notEqual(g.railZ, '45', 'dark/1800/narrow: rail is not an overlay');
        await page.evaluate(() => document.documentElement.removeAttribute('data-gal'));

        // Narrow: the rail button sits at Top-Left inside the 36px bar; the open
        // rail and its scrim cover the strip.
        await page.setViewportSize({ width: 1280, height: 900 });
        await page.waitForTimeout(200);
        g = await geometry(page);
        checkStrip('dark/1280', g);
        assert.deepEqual([Math.round(g.railbtn.l), Math.round(g.railbtn.t), Math.round(g.railbtn.w), Math.round(g.railbtn.h)], [8, 5, 26, 26], 'dark/1280: rail button is a 26px flat handle inside the 36px top-left');
        await shot(page, 'dark-narrow');
        await shot(page, 'dark-narrow-strip', { clip: { x: 0, y: 0, width: 1280, height: 60 } });
        await page.locator('#railbtn').click();
        await page.waitForTimeout(400);
        g = await geometry(page);
        assert.ok(g.open, 'dark/1280: rail button opens the rail');
        assert.deepEqual([g.rail.l, g.rail.t], [12, 12], 'dark/1280: overlay rail sits 12px in');
        const covered = await page.evaluate(() => {
          const b = document.querySelector('#railbtn').getBoundingClientRect(), u = document.querySelector('#shell-bottom .usage-toggle').getBoundingClientRect();
          const hit = (x, y, sel) => { const e = document.elementFromPoint(x, y); return e && e.closest(sel) ? true : (e ? e.tagName + '.' + e.className + '#' + e.id : 'none'); };
          return [hit(b.left + b.width / 2, b.top + b.height / 2, '#rail, #rscrim, #memo-pad'), hit(u.left + u.width / 2, u.top + u.height / 2, '#shell-bottom')];
        });
        assert.deepEqual(covered, [true, true], 'dark/1280: open rail covers topbar #railbtn while #shell-bottom stays above rail: ' + JSON.stringify(covered));
        await shot(page, 'dark-narrow-open');
        // Global controls (Home, global settings, #gear) live in #slot-bot-left and fit at 360px; .rfoot is retired.
        await page.setViewportSize({ width: 360, height: 760 });
        await page.waitForTimeout(300);
        const foot = await page.evaluate(() => {
          const f = document.querySelector('#slot-bot-left');
          return {
            retiredRfoot: document.querySelectorAll('#rail .rfoot').length,
            fits: f.scrollWidth <= f.clientWidth,
            kids: [...f.children].map(c => c.className)
          };
        });
        assert.equal(foot.retiredRfoot, 0, '.rfoot is retired from shell rail');
        assert.ok(foot.fits, 'bottom-left bar fits at 360px');
        assert.deepEqual(foot.kids, ['bot-btn rail-home', 'bot-btn r-config-global', 'bot-btn settings-toggle'], 'bottom-left bar is Home, global settings, then #gear');
        await page.keyboard.press('Escape');
        await page.waitForTimeout(300);

        // 760px, the desktop shell's minimum: the strip ellipsizes and hides
        // low-priority chips cleanly while usage stays unclipped in #shell-bottom.
        await page.setViewportSize({ width: 760, height: 700 });
        await page.waitForTimeout(300);
        g = await geometry(page);
        checkStrip('dark/760', g);
        const steps = [
          [0, null, null],
          [1, 'livesession-renamed-with-a-much-longer-label', null],
          [2, 'livesession-renamed-with-a-much-longer-label', 'a-project-folder-with-an-unusually-long-name-for-the-header-strip'],
        ];
        let state = null;
        for (const [step, label, project] of steps) {
          await page.evaluate(({ label, project }) => {
            const top = document.querySelector('.dwrap > .top');
            if (label) top.querySelector('.snm').textContent = label;
            if (project) {
              top.querySelector('.tph-r1').textContent = project;
              top.querySelector('.tph-chips').insertAdjacentHTML('afterbegin', '<span class="nolive">stores only</span><span class="ended-chip">ended</span>');
            }
          }, { label, project });
          await page.waitForTimeout(150);
          g = await geometry(page);
          checkStrip(`dark/760/step${step}`, g);
          state = await page.evaluate(() => {
            const top = document.querySelector('.dwrap > .top'), id = top.querySelector('.tph-id');
            const clip = e => e.scrollWidth > e.clientWidth + 0.5;
            return { tch: top.classList.contains('tch'), clipped: clip(id) || [...id.querySelectorAll('.snm, .tph-r1')].some(clip) };
          });
          for (const [name, b] of [['railbtn', g.railbtn], ['usage', g.usage], ['host', g.host]]) {
            assert.ok(b && b.w > 0 && b.l >= 0 && b.r <= g.vw, `dark/760/step${step}: ${name} shows unclipped`);
          }
          await shot(page, `dark-760-step${step}-strip`, { clip: { x: 0, y: 0, width: 760, height: 60 } });
          report.push({ width: 760, step, state, items: g.items.map(i => [i.cls, Math.round(i.box.l), Math.round(i.box.r)]), handles: g.handles.map(h => Math.round(h.l)) });
        }
        assert.ok(state.tch, 'dark/760: the longest head hides chips ' + JSON.stringify(state));
        await shot(page, 'dark-760');
        await shot(page, 'dark-760-strip', { clip: { x: 0, y: 0, width: 760, height: 60 } });
        await page.locator('#shell-bottom .usage-toggle').click();
        await page.locator('.usage-pop').waitFor({ state: 'visible' });
        await shot(page, 'dark-760-usage');
        await page.keyboard.press('Escape');
        // Widening back restores the full handle in #shell-bottom.
        await page.setViewportSize({ width: 1920, height: 900 });
        await page.waitForTimeout(300);
        assert.equal(await page.locator('#shell-bottom').evaluate(el => el.classList.contains('bu')), false, 'dark/1920 again: plan usage shows in full');
      }
      assert.deepEqual(errors, [], theme + ': page errors ' + JSON.stringify(errors));
      await context.close();
    }
    console.log(JSON.stringify(report));
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
