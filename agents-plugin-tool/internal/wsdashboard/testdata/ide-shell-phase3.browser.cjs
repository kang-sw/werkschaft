// Run by TestIdeShellPhase3Browser with real Go-served pages.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [base, shots, mockup] = process.argv.slice(2);

function shot(page, name, opts) {
  if (!shots) return Promise.resolve();
  fs.mkdirSync(shots, { recursive: true });
  return page.screenshot(Object.assign({ path: path.join(shots, name + '.png') }, opts || {}));
}

function rgbToHex(rgb) {
  const m = String(rgb || '').match(/rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)/i);
  if (!m) return rgb;
  return '#' + [m[1], m[2], m[3]].map(n => Number(n).toString(16).padStart(2, '0')).join('');
}

function makeUsagePayload() {
  const at = Date.now();
  return {
    warnPercent: 80,
    critPercent: 95,
    entries: [
      {
        harness: 'claude',
        source: 'fixture',
        asOf: at,
        windows: [
          { kind: 'five_hour', percent: 24, asOf: at, resetsAt: at + 3 * 3600000 },
          { kind: 'seven_day', percent: 58, asOf: at, resetsAt: at + 4 * 86400000 }
        ]
      },
      {
        harness: 'codex',
        source: 'fixture',
        asOf: at,
        windows: [
          { kind: 'seven_day', percent: 18, asOf: at, resetsAt: at + 5 * 86400000 }
        ]
      }
    ],
    accounts: {
      codex: {
        active: 'personal',
        slots: [
          { name: 'personal', label: 'personal@example.com', active: true },
          { name: 'work', label: 'work@example.com', active: false }
        ]
      }
    }
  };
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const summary = [];
  try {
    if (mockup && shots) {
      for (const theme of ['dark', 'light']) {
        const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: theme });
        const mp = await ctx.newPage();
        await mp.goto(pathToFileURL(mockup).href);
        await mp.evaluate(t => {
          document.documentElement.dataset.theme = t;
          const btn = document.querySelector(`[data-theme-pick="${t}"]`);
          if (btn) btn.click();
        }, theme);
        await mp.waitForTimeout(150);
        await shot(mp, `reference-mockup-${theme}-1440`);
        await ctx.close();
      }
    }

    for (const theme of ['dark', 'light']) {
      const wantShellBg = theme === 'dark' ? '#111216' : '#e7e7e2';
      const wantStageBg = theme === 'dark' ? '#16181d' : '#f4f4f1';

      const context = await browser.newContext({
        viewport: { width: 1920, height: 1080 },
        colorScheme: theme
      });
      await context.addInitScript(() => {
        if (self !== top) return;
        const workers = navigator.serviceWorker;
        if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
      });
      await context.route('**/*', route => {
        const url = new URL(route.request().url());
        if (url.origin !== base) return route.abort();
        if (url.pathname === '/rail/usage') return route.fulfill({ json: makeUsagePayload() });
        return route.continue();
      });

      const page = await context.newPage();
      const errors = [];
      page.on('pageerror', e => errors.push(e.message));

      // 1. Git-backed session across 1200x800, 1440x900, 1920x1080.
      await page.goto(base + '/s/gitsess');
      await page.frameLocator('.sframe.on').locator('#grid').waitFor();
      await page.locator('#shell-bottom .usage-number').first().waitFor();
      await page.locator('.top-left .worktree-branch').waitFor({ state: 'visible' });
      // Wait for tracker handles to mount in the active frame.
      await page.frameLocator('.sframe.on').locator('#tkhandles .tk-handle').first().waitFor({ state: 'visible' });
      await page.waitForTimeout(260);

      for (const vp of [
        { width: 1200, height: 800, wide: false },
        { width: 1440, height: 900, wide: false },
        { width: 1920, height: 1080, wide: true }
      ]) {
        await page.setViewportSize({ width: vp.width, height: vp.height });
        await page.waitForTimeout(240);

        const m = await page.evaluate(() => {
          const box = el => {
            if (!el) return null;
            const r = el.getBoundingClientRect(), cs = getComputedStyle(el);
            if (!(r.width || r.height) || cs.display === 'none' || cs.visibility === 'hidden') return null;
            return { l: r.left, t: r.top, r: r.right, b: r.bottom, w: r.width, h: r.height };
          };
          const root = document.documentElement;
          const fdoc = document.querySelector('.sframe.on').contentDocument;
          const top = document.querySelector('.dwrap > .top');
          const topLeft = top.querySelector(':scope > .top-left');
          const topCenter = top.querySelector(':scope > .top-center');
          const topRight = top.querySelector(':scope > .top-right');
          const well = document.getElementById('stage-well-bg');
          const bot = document.getElementById('shell-bottom');
          const cur = document.querySelector('#rlist .rs.cur');
          const leftItems = [...topLeft.querySelectorAll(':scope > :not(.r-head), :scope > .r-head > *')]
            .map(el => ({ cls: el.className, box: box(el) })).filter(x => x.box);
          const handles = [...fdoc.querySelectorAll('#tkhandles .tk-handle')]
            .map(el => ({ id: el.id || '', cls: el.className, box: box(el) })).filter(x => x.box);
          const shandleBox = box(fdoc.getElementById('shandle'));
          const branch = topLeft.querySelector('.worktree-branch');
          const searchBtn = document.querySelector('#qsslot #tkqs');
          const searchKbd = document.getElementById('tkqs-kbd');
          return {
            vw: window.innerWidth,
            vh: window.innerHeight,
            shellOverflow: root.scrollWidth - window.innerWidth,
            frameOverflow: fdoc.documentElement.scrollWidth - fdoc.defaultView.innerWidth,
            shellBg: getComputedStyle(document.body).backgroundColor,
            stageBg: getComputedStyle(well).backgroundColor,
            wellRadius: getComputedStyle(well).borderTopLeftRadius,
            wellShadow: getComputedStyle(well).boxShadow,
            topRect: box(top),
            topCols: getComputedStyle(top).gridTemplateColumns.split(/\s+/).length,
            topLeftRect: box(topLeft),
            topCenterRect: box(topCenter),
            topRightPE: getComputedStyle(topRight).pointerEvents,
            botRect: box(bot),
            botLeftKids: [...document.getElementById('slot-bot-left').children].map(c => c.className),
            botRightUsage: !!document.querySelector('#slot-bot-right .usage-toggle'),
            // The accent bar is ::before (::after is the drag-nest indicator's).
            curAfterW: cur ? getComputedStyle(cur, '::before').width : '',
            curBg: cur ? getComputedStyle(cur).backgroundColor : '',
            railbtn: box(document.getElementById('railbtn')),
            branchText: branch ? branch.textContent.trim() : '',
            branchBox: box(branch),
            harnessBox: box(topLeft.querySelector('.tph-hs')),
            sseBox: box(topLeft.querySelector('.host.sse')),
            searchBox: box(searchBtn),
            searchKbdText: searchKbd ? searchKbd.textContent.trim() : '',
            shandleBox,
            leftItems,
            handles,
            tkHandleCount: fdoc.querySelectorAll('#tkhandles .tk-handle').length,
            memoBottom: getComputedStyle(document.getElementById('memo-pad')).bottom
          };
        });

        // Visual parity & overflow assertions.
        assert.ok(m.shellOverflow <= 0, `${theme}/${vp.width}: shell overflow ${m.shellOverflow}px`);
        assert.ok(m.frameOverflow <= 0, `${theme}/${vp.width}: frame overflow ${m.frameOverflow}px`);
        assert.equal(rgbToHex(m.shellBg), wantShellBg, `${theme}/${vp.width}: chrome fill`);
        assert.equal(rgbToHex(m.stageBg), wantStageBg, `${theme}/${vp.width}: stage well fill`);
        assert.equal(m.wellRadius, '12px', `${theme}/${vp.width}: 12px well radius`);
        assert.ok(m.wellShadow && m.wellShadow !== 'none', `${theme}/${vp.width}: subtle 1px well rim`);
        assert.equal(Math.round(m.topRect.h), 36, `${theme}/${vp.width}: 36px topbar`);
        assert.equal(Math.round(m.botRect.h), 26, `${theme}/${vp.width}: 26px bottom bar`);
        assert.equal(m.topCols, 3, `${theme}/${vp.width}: 3-column grid topbar`);
        assert.equal(m.topRightPE, 'none', `${theme}/${vp.width}: .top-right passes clicks through`);
        assert.equal(m.memoBottom, '38px', `${theme}/${vp.width}: #memo-pad clears 26px bottom bar (26 + 12 = 38px)`);

        // Bottom-bar cluster contents.
        assert.deepEqual(m.botLeftKids, ['bot-btn rail-home', 'bot-btn r-config-global', 'bot-btn settings-toggle']);
        assert.equal(m.botRightUsage, true, `${theme}/${vp.width}: usage-toggle in bottom-right`);
        if (vp.wide) {
          assert.equal(m.curAfterW, '2.5px', `${theme}/${vp.width}: active rail row accent bar`);
          assert.notEqual(m.curBg, 'rgba(0, 0, 0, 0)', `${theme}/${vp.width}: active rail row tonal fill`);
        }

        // Top-Left per-session telemetry.
        assert.ok(m.railbtn && m.railbtn.w === 26 && m.railbtn.h === 26, `${theme}/${vp.width}: #railbtn visible`);
        assert.ok(m.branchText.length > 0 && m.branchBox, `${theme}/${vp.width}: .worktree-branch visible on git-backed session (${m.branchText})`);
        assert.ok(m.harnessBox, `${theme}/${vp.width}: .tph-hs harness badge visible`);
        assert.ok(m.sseBox, `${theme}/${vp.width}: live stream indicator visible`);

        // Top-Center centered search pill + hidden framed #shandle.
        assert.ok(m.searchBox, `${theme}/${vp.width}: centered #tkqs search pill visible`);
        assert.ok(m.searchKbdText === '⌘K' || m.searchKbdText === 'Ctrl+K', `${theme}/${vp.width}: shortcut badge (${m.searchKbdText})`);
        assert.equal(m.shandleBox, null, `${theme}/${vp.width}: framed #shandle hidden`);
        assert.ok(m.searchBox.t >= 0 && m.searchBox.b <= 36.5, `${theme}/${vp.width}: search pill inside 36px topbar`);

        // True visual center check (when right handles fit within half-viewport - search/2).
        const searchCenter = (m.searchBox.l + m.searchBox.r) / 2;
        assert.ok(Math.abs(searchCenter - m.vw / 2) <= 3,
          `${theme}/${vp.width}: search pill centered (${searchCenter.toFixed(1)} vs ${(m.vw / 2).toFixed(1)})`);

        // Top-Right global tracker handles + legacy session handles present & zero overlap.
        assert.ok(m.tkHandleCount >= 2, `${theme}/${vp.width}: #tkhandles holds store handle + #tkadd (${m.tkHandleCount})`);
        assert.ok(m.handles.length >= 2, `${theme}/${vp.width}: right handles visible (${m.handles.length})`);
        for (const it of m.leftItems) {
          assert.ok(it.box.r <= m.searchBox.l + 0.5,
            `${theme}/${vp.width}: top-left ${it.cls} (${it.box.r}) overlaps search (${m.searchBox.l})`);
        }
        for (const h of m.handles) {
          assert.ok(h.box.t >= 0 && h.box.b <= 36.5,
            `${theme}/${vp.width}: right handle ${h.id || h.cls} fits in 36px bar (${h.box.t}..${h.box.b})`);
          assert.ok(m.searchBox.r <= h.box.l + 0.5,
            `${theme}/${vp.width}: search (${m.searchBox.r}) overlaps right handle ${h.id || h.cls} (${h.box.l})`);
        }

        await shot(page, `phase3-${theme}-${vp.width}x${vp.height}`);
        summary.push({
          theme,
          vp: `${vp.width}x${vp.height}`,
          searchCenter: Math.round(searchCenter),
          vwHalf: m.vw / 2,
          branch: m.branchText,
          handles: m.handles.length
        });
      }

      // 2. Top-Right click-through & handle/drawer alignment at 1440x900.
      await page.setViewportSize({ width: 1440, height: 900 });
      await page.waitForTimeout(200);

      // Click centered search pill -> opens #tkqpop in frame.
      await page.locator('#qsslot #tkqs').click();
      const activeFrame = page.frameLocator('.sframe.on');
      await activeFrame.locator('#tkqpop').waitFor({ state: 'visible' });
      await activeFrame.locator('#tkqin').press('Escape');
      await activeFrame.locator('#tkqpop').waitFor({ state: 'hidden' });

      // The framed page's panels handle retired with the drawer cards (the
      // holders moved to the shell's bottom-bar popup): no #drhandle, and the
      // framed search handle stays hidden behind the shell's search pill.
      const retired = await page.evaluate(() => {
        const fdoc = document.querySelector('.sframe.on').contentDocument;
        return { drhandle: !!fdoc.getElementById('drhandle'), drbody: !!fdoc.getElementById('drbody'), holders: !!fdoc.getElementById('p-holders'),
          drawerOpen: !!fdoc.querySelector('.drawer.open') };
      });
      assert.deepEqual(retired, { drhandle: false, drbody: false, holders: false, drawerOpen: false }, `${theme}: the framed drawer carries no cards or panels handle`);

      // Click through shell Top-Right directly at tracker store .tk-handle!
      const tkBox = await page.evaluate(() => {
        const fdoc = document.querySelector('.sframe.on').contentDocument;
        const h = fdoc.querySelector('#tkhandles .tk-handle:not(.tk-add)');
        const r = h.getBoundingClientRect();
        return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
      });
      await page.mouse.click(tkBox.x, tkBox.y);
      await activeFrame.locator('#tkdrawer[open] #tkclip').waitFor({ state: 'visible', timeout: 10000 });
      await page.waitForTimeout(220);
      const tkAlign = await page.evaluate(() => {
        const fdoc = document.querySelector('.sframe.on').contentDocument;
        const clip = fdoc.getElementById('tkclip').getBoundingClientRect();
        const fold = fdoc.querySelector('#tkhandles .tk-fold:not([hidden])');
        const fr = fold ? fold.getBoundingClientRect() : null;
        return { top: Math.round(clip.top), right: Math.round(clip.right), foldTop: fr ? Math.round(fr.top) : -1, foldBottom: fr ? Math.round(fr.bottom) : -1 };
      });
      assert.ok(tkAlign.top >= 34 && tkAlign.top <= 42, `${theme}: tracker drawer aligns below 36px topbar (${JSON.stringify(tkAlign)})`);
      assert.ok(tkAlign.foldTop >= 0 && tkAlign.foldBottom <= 36.5, `${theme}: tracker fold button aligns inside 36px topbar (${JSON.stringify(tkAlign)})`);
      const tkFoldBox = await page.evaluate(() => {
        const fdoc = document.querySelector('.sframe.on').contentDocument;
        const r = fdoc.querySelector('#tkhandles .tk-fold:not([hidden])').getBoundingClientRect();
        return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
      });
      await page.mouse.click(tkFoldBox.x, tkFoldBox.y);
      await activeFrame.locator('#tkclip').waitFor({ state: 'hidden' });

      // 3. Phase 2 minor polishes: #talert long text cap at narrow width (760px) & #memo-pad clearance.
      if (theme === 'dark') {
        await page.setViewportSize({ width: 760, height: 700 });
        await page.evaluate(() => {
          const el = document.getElementById('talert');
          el.textContent = '3 approvals waiting across very-long-workspace-name and multiple active subagent sessions';
          el.hidden = false;
        });
        await page.waitForTimeout(180);
        const longAlert = await page.evaluate(() => {
          const left = document.getElementById('slot-bot-left').getBoundingClientRect();
          const center = document.getElementById('slot-bot-center').getBoundingClientRect();
          const right = document.getElementById('slot-bot-right').getBoundingClientRect();
          const gear = document.getElementById('gear').getBoundingClientRect();
          const usage = document.querySelector('#slot-bot-right .usage-toggle').getBoundingClientRect();
          const alert = document.getElementById('talert');
          return {
            overflow: document.documentElement.scrollWidth - window.innerWidth,
            leftRight: left.right,
            centerLeft: center.left,
            centerRight: center.right,
            rightLeft: right.left,
            gearW: gear.width,
            usageW: usage.width,
            alertClipped: alert.scrollWidth > alert.clientWidth,
            alertW: center.width
          };
        });
        assert.ok(longAlert.overflow <= 0, `760px long #talert overflow: ${longAlert.overflow}`);
        assert.ok(longAlert.gearW > 12 && longAlert.usageW > 12, `760px side tracks remain uncrushed: ${JSON.stringify(longAlert)}`);
        assert.ok(longAlert.leftRight <= longAlert.centerLeft + 0.5 && longAlert.centerRight <= longAlert.rightLeft + 0.5,
          `760px long #talert does not overlap side clusters: ${JSON.stringify(longAlert)}`);
        assert.equal(longAlert.alertClipped, true, '760px long #talert ellipsizes inside capped center track');
        await page.evaluate(() => {
          const el = document.getElementById('talert');
          el.textContent = '';
          el.hidden = true;
        });

        // Open rail+memo-pad at 1440x900 -> verify #memo-pad bottom clears #shell-bottom top.
        await page.setViewportSize({ width: 1440, height: 900 });
        await page.waitForTimeout(160);
        await page.locator('#railbtn').click();
        await page.waitForTimeout(300);
        const memoClear = await page.evaluate(() => {
          const memo = document.getElementById('memo-pad').getBoundingClientRect();
          const bar = document.getElementById('shell-bottom').getBoundingClientRect();
          return { memoBottom: Math.round(memo.bottom), barTop: Math.round(bar.top) };
        });
        assert.ok(memoClear.memoBottom <= memoClear.barTop - 10,
          `#memo-pad bottom (${memoClear.memoBottom}) must sit clearly above #shell-bottom top (${memoClear.barTop})`);
        await page.keyboard.press('Escape');
        await page.waitForTimeout(180);
      }

      // 4. Non-git-backed session fixture: .worktree-branch absent/hidden, zero JS errors.
      await page.setViewportSize({ width: 1440, height: 900 });
      await page.goto(base + '/s/nongitsess');
      await page.frameLocator('.sframe.on').locator('#grid').waitFor();
      await page.waitForTimeout(240);
      const nonGit = await page.evaluate(() => ({
        branchCount: document.querySelectorAll('.top-left .worktree-branch').length,
        hasId: !!document.querySelector('.top-left .tph-id'),
        hasHarness: !!document.querySelector('.top-left .tph-hs'),
        hasSearch: !!document.querySelector('#qsslot #tkqs'),
        overflow: document.documentElement.scrollWidth - window.innerWidth
      }));
      assert.deepEqual(nonGit, { branchCount: 0, hasId: true, hasHarness: true, hasSearch: true, overflow: 0 },
        `${theme}: non-git session omits .worktree-branch cleanly`);
      if (theme === 'dark') await shot(page, 'phase3-dark-1440-nongit');

      // 5. Non-session routes: Home (/) and Settings (/settings).
      for (const [routeName, routePath] of [['home', '/'], ['settings', '/settings']]) {
        await page.goto(base + routePath);
        await page.waitForTimeout(280);
        const ns = await page.evaluate(() => {
          const vis = sel => {
            const el = document.querySelector(sel);
            if (!el) return false;
            const r = el.getBoundingClientRect(), cs = getComputedStyle(el);
            return (r.width > 0 || r.height > 0) && cs.display !== 'none' && cs.visibility !== 'hidden';
          };
          return {
            branchVis: vis('.top-left .worktree-branch'),
            harnessVis: vis('.top-left .tph-hs'),
            idVis: vis('.top-left .tph-id'),
            sseVis: vis('.top-left #sse'),
            searchVis: vis('#qsslot #tkqs'),
            botBarH: Math.round(document.getElementById('shell-bottom').getBoundingClientRect().height),
            overflow: document.documentElement.scrollWidth - window.innerWidth
          };
        });
        assert.deepEqual(ns, {
          branchVis: false,
          harnessVis: false,
          idVis: false,
          sseVis: false,
          searchVis: true,
          botBarH: 26,
          overflow: 0
        }, `${theme}/${routeName}: per-session telemetry hides cleanly on non-session route`);
        if (theme === 'dark') await shot(page, `phase3-dark-1440-${routeName}`);
      }

      assert.deepEqual(errors, [], `${theme}: unexpected page errors ${JSON.stringify(errors)}`);
      await context.close();
    }
    console.log('ide-shell phase3 browser: ok', JSON.stringify(summary));
  } finally {
    await browser.close();
  }
})().catch(e => {
  console.error(e);
  process.exit(1);
});
