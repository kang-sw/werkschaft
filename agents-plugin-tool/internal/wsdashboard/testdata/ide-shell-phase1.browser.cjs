// Run by TestIdeShellPhase1Browser with real Go-served pages.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [base, wtRoute, shots, mockup] = process.argv.slice(2);

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
        if (url.pathname === '/rail/usage') return route.fulfill({ json: { entries: [], warnPercent: 80, critPercent: 95 } });
        return route.continue();
      });

      const page = await context.newPage();
      const errors = [];
      page.on('pageerror', e => errors.push(e.message));

      // 1. Check 1200x800, 1440x900, 1920x1080: 0px horizontal overflow, chrome/well geometry & colors.
      await page.goto(base + '/s/sess00');
      await page.frameLocator('.sframe.on').locator('#grid').waitFor();
      await page.waitForTimeout(250);

      for (const vp of [
        { width: 1200, height: 800, wide: false },
        { width: 1440, height: 900, wide: false },
        { width: 1920, height: 1080, wide: true }
      ]) {
        await page.setViewportSize({ width: vp.width, height: vp.height });
        await page.waitForTimeout(220);
        const m = await page.evaluate(() => {
          const root = document.documentElement;
          const well = document.getElementById('stage-well-bg');
          const bot = document.getElementById('shell-bottom');
          const top = document.querySelector('.dwrap > .top');
          const rail = document.getElementById('rail');
          const frames = document.getElementById('frames');
          const fdoc = document.querySelector('.sframe.on').contentDocument;
          const cur = document.querySelector('#rlist .rs.cur');
          return {
            shellOverflow: root.scrollWidth - window.innerWidth,
            frameOverflow: fdoc.documentElement.scrollWidth - fdoc.defaultView.innerWidth,
            shellBg: getComputedStyle(document.body).backgroundColor,
            stageBg: getComputedStyle(well).backgroundColor,
            wellRadius: getComputedStyle(well).borderTopLeftRadius,
            wellShadow: getComputedStyle(well).boxShadow,
            wellRect: well.getBoundingClientRect().toJSON(),
            topRect: top.getBoundingClientRect().toJSON(),
            botRect: bot.getBoundingClientRect().toJSON(),
            framesRect: frames.getBoundingClientRect().toJSON(),
            railRect: rail.getBoundingClientRect().toJSON(),
            curBeforeW: cur ? getComputedStyle(cur, '::before').width : '',
            curBeforeBg: cur ? getComputedStyle(cur, '::before').backgroundColor : '',
            curBg: cur ? getComputedStyle(cur).backgroundColor : ''
          };
        });
        assert.ok(m.shellOverflow <= 0, `${theme}/${vp.width}: shell overflow ${m.shellOverflow}px`);
        assert.ok(m.frameOverflow <= 0, `${theme}/${vp.width}: frame overflow ${m.frameOverflow}px`);
        assert.equal(rgbToHex(m.shellBg), wantShellBg, `${theme}/${vp.width}: outer chrome fill`);
        assert.equal(rgbToHex(m.stageBg), wantStageBg, `${theme}/${vp.width}: stage well fill`);
        assert.equal(m.wellRadius, '12px', `${theme}/${vp.width}: well radius 12px`);
        assert.ok(m.wellShadow && m.wellShadow !== 'none', `${theme}/${vp.width}: whisper 1px rim shadow`);
        assert.equal(Math.round(m.topRect.height), 36, `${theme}/${vp.width}: 36px topbar`);
        assert.equal(Math.round(m.botRect.height), 26, `${theme}/${vp.width}: 26px bottom bar`);
        assert.equal(Math.round(m.framesRect.bottom), vp.height - 26, `${theme}/${vp.width}: frames end above 26px bottom bar`);
        assert.equal(Math.round(m.wellRect.top), 36, `${theme}/${vp.width}: well starts at 36px`);
        assert.equal(Math.round(m.wellRect.bottom), vp.height - 26, `${theme}/${vp.width}: well ends above 26px bar`);
        assert.equal(Math.round(vp.width - m.wellRect.right), 6, `${theme}/${vp.width}: 6px right margin`);
        if (vp.wide) {
          assert.equal(Math.round(m.railRect.left), 0, `${theme}/${vp.width}: rail flush left`);
          assert.equal(Math.round(m.railRect.width), 280, `${theme}/${vp.width}: default rail width 280px`);
          assert.equal(Math.round(m.railRect.top), 36, `${theme}/${vp.width}: rail starts below 36px topbar`);
          assert.equal(Math.round(m.railRect.bottom), vp.height - 26, `${theme}/${vp.width}: rail ends above 26px bottom bar`);
          assert.equal(Math.round(m.wellRect.left), 280, `${theme}/${vp.width}: well left touches 280px rail`);
          assert.equal(m.curBeforeW, '2.5px', `${theme}/${vp.width}: active rail row left accent bar`);
          assert.notEqual(m.curBg, 'rgba(0, 0, 0, 0)', `${theme}/${vp.width}: active rail row tonal fill`);
        } else {
          assert.equal(Math.round(m.wellRect.left), 6, `${theme}/${vp.width}: narrow well has 6px left margin`);
        }
        await shot(page, `phase1-${theme}-${vp.width}x${vp.height}`);
        summary.push({ theme, vp: `${vp.width}x${vp.height}`, wellLeft: Math.round(m.wellRect.left), railW: Math.round(m.railRect.width) });
      }

      // 2. At 1920x1080: drag splitter 280 -> 220 -> 360, verify persistence across reload,
      //    frame --rail-w sync, double-click reset to 280, and collapse/expand via Ctrl+` + #railbtn.
      const splitter = page.locator('#rail-splitter');
      const sb = await splitter.boundingBox();
      assert.ok(sb && sb.width >= 6, `${theme}: splitter visible at 1920`);
      const cy = sb.y + sb.height / 2;

      // Drag left past min -> clamps to 220px.
      await page.mouse.move(sb.x + sb.width / 2, cy);
      await page.mouse.down();
      await page.mouse.move(140, cy, { steps: 6 });
      await page.mouse.up();
      await page.waitForTimeout(160);
      let dragState = await page.evaluate(() => {
        const fdoc = document.querySelector('.sframe.on').contentDocument;
        return {
          railW: Math.round(document.getElementById('rail').getBoundingClientRect().width),
          wellL: Math.round(document.getElementById('stage-well-bg').getBoundingClientRect().left),
          stored: localStorage.getItem('ws.dashboard.railWidth'),
          frameVar: getComputedStyle(fdoc.documentElement).getPropertyValue('--rail-w').trim(),
          framePhW: Math.round(fdoc.querySelector('.rail-ph').getBoundingClientRect().width)
        };
      });
      assert.deepEqual(dragState, { railW: 220, wellL: 220, stored: '220', frameVar: '220px', framePhW: 220 }, `${theme}: drag clamps to 220px and syncs frame`);

      // Drag right past max -> clamps to 360px.
      const sb220 = await splitter.boundingBox();
      await page.mouse.move(sb220.x + sb220.width / 2, cy);
      await page.mouse.down();
      await page.mouse.move(520, cy, { steps: 8 });
      await page.mouse.up();
      await page.waitForTimeout(160);
      dragState = await page.evaluate(() => {
        const fdoc = document.querySelector('.sframe.on').contentDocument;
        return {
          railW: Math.round(document.getElementById('rail').getBoundingClientRect().width),
          wellL: Math.round(document.getElementById('stage-well-bg').getBoundingClientRect().left),
          stored: localStorage.getItem('ws.dashboard.railWidth'),
          frameVar: getComputedStyle(fdoc.documentElement).getPropertyValue('--rail-w').trim(),
          framePhW: Math.round(fdoc.querySelector('.rail-ph').getBoundingClientRect().width)
        };
      });
      assert.deepEqual(dragState, { railW: 360, wellL: 360, stored: '360', frameVar: '360px', framePhW: 360 }, `${theme}: drag clamps to 360px and syncs frame`);

      // Reload persists 360px cleanly.
      await page.reload();
      await page.frameLocator('.sframe.on').locator('#grid').waitFor();
      await page.waitForTimeout(220);
      const afterReloadW = await page.evaluate(() => Math.round(document.getElementById('rail').getBoundingClientRect().width));
      assert.equal(afterReloadW, 360, `${theme}: persisted 360px rail width survives reload`);

      // Double-click splitter resets to 280px.
      await page.locator('#rail-splitter').dblclick();
      await page.waitForTimeout(160);
      const afterResetW = await page.evaluate(() => Math.round(document.getElementById('rail').getBoundingClientRect().width));
      assert.equal(afterResetW, 280, `${theme}: double-click resets rail to 280px`);

      // Collapse via Ctrl+` -> rail hides, well left becomes 6px, #railbtn shows, frame gets data-rail-collapsed="true".
      await page.keyboard.press('Control+Backquote');
      await page.waitForTimeout(180);
      const collapsed = await page.evaluate(() => {
        const fdoc = document.querySelector('.sframe.on').contentDocument;
        return {
          shellAttr: document.documentElement.getAttribute('data-rail-collapsed'),
          frameAttr: fdoc.documentElement.getAttribute('data-rail-collapsed'),
          railDisplay: getComputedStyle(document.getElementById('rail')).display,
          wellL: Math.round(document.getElementById('stage-well-bg').getBoundingClientRect().left),
          btnDisplay: getComputedStyle(document.getElementById('railbtn')).display,
          shellOverflow: document.documentElement.scrollWidth - window.innerWidth,
          frameOverflow: fdoc.documentElement.scrollWidth - fdoc.defaultView.innerWidth
        };
      });
      assert.deepEqual(collapsed, {
        shellAttr: 'true',
        frameAttr: 'true',
        railDisplay: 'none',
        wellL: 6,
        btnDisplay: 'flex',
        shellOverflow: 0,
        frameOverflow: 0
      }, `${theme}: collapsed rail state`);
      if (theme === 'dark') await shot(page, 'phase1-dark-1920x1080-collapsed');

      // Expand back via #railbtn click.
      await page.locator('#railbtn').click();
      await page.waitForTimeout(180);
      const expanded = await page.evaluate(() => ({
        shellAttr: document.documentElement.getAttribute('data-rail-collapsed'),
        wellL: Math.round(document.getElementById('stage-well-bg').getBoundingClientRect().left),
        railW: Math.round(document.getElementById('rail').getBoundingClientRect().width)
      }));
      assert.deepEqual(expanded, { shellAttr: null, wellL: 280, railW: 280 }, `${theme}: clicking #railbtn expands rail`);

      // 3. Band 4 four-column fit follows live rail width (220px vs 360px at 2400px viewport).
      if (theme === 'dark') {
        await page.setViewportSize({ width: 2400, height: 1080 });
        await page.evaluate(() => window.WsIdeShell.setWidth(220));
        await page.waitForTimeout(200);
        const galAt220 = await page.evaluate(() => document.querySelector('.sframe.on').contentDocument.documentElement.getAttribute('data-gal'));
        assert.ok(galAt220 === 'empty' || galAt220 === 'col', `dark/2400 with 220px rail enters Band 4, got data-gal=${galAt220}`);
        await page.evaluate(() => window.WsIdeShell.setWidth(360));
        await page.waitForTimeout(200);
        const galAt360 = await page.evaluate(() => document.querySelector('.sframe.on').contentDocument.documentElement.getAttribute('data-gal'));
        assert.ok(galAt360 !== 'empty' && galAt360 !== 'col', `dark/2400 with 360px rail exits Band 4, got data-gal=${galAt360}`);
        await page.evaluate(() => window.WsIdeShell.setWidth(280));
        await page.setViewportSize({ width: 1920, height: 1080 });
        await page.waitForTimeout(180);
      }

      // 4. 0px vertical jitter on #rail-companion-slot when toggling details.rws-unreg & scrolling #rlist.
      await page.evaluate(() => {
        const rail = document.getElementById('rail');
        const slot = document.getElementById('rail-companion-slot');
        rail.dataset.companionDock = 'rail-slot';
        slot.hidden = false;
        slot.style.height = '240px';
      });
      await page.waitForTimeout(100);
      const jitter = await page.evaluate(() => {
        const slot = document.getElementById('rail-companion-slot');
        const rlist = document.getElementById('rlist');
        const unreg = document.querySelector('#rail details.rws-unreg');
        const r0 = slot.getBoundingClientRect();
        rlist.scrollTop = rlist.scrollHeight;
        const r1 = slot.getBoundingClientRect();
        unreg.open = true;
        const r2 = slot.getBoundingClientRect();
        unreg.open = false;
        const r3 = slot.getBoundingClientRect();
        return {
          dScroll: Math.abs(r1.top - r0.top) + Math.abs(r1.bottom - r0.bottom),
          dOpen: Math.abs(r2.top - r0.top) + Math.abs(r2.bottom - r0.bottom),
          dClose: Math.abs(r3.top - r0.top) + Math.abs(r3.bottom - r0.bottom),
          slotW: Math.round(r0.width)
        };
      });
      assert.deepEqual(jitter, { dScroll: 0, dOpen: 0, dClose: 0, slotW: 280 }, `${theme}: 0px vertical jitter on #rail-companion-slot`);

      // 5. Scrolled Index (/) and Worktree pages stay clipped inside the stage well (decision 1721).
      for (const [label, route] of [['index', '/'], ['worktree', wtRoute]]) {
        await page.goto(base + route);
        await page.waitForFunction(() => {
          const f = document.querySelector('.sframe.on');
          return f && f.contentDocument && f.contentDocument.readyState === 'complete';
        });
        await page.waitForTimeout(260);
        const clipRes = await page.evaluate(() => {
          const f = document.querySelector('.sframe.on');
          const fwin = f.contentWindow;
          const fdoc = f.contentDocument;
          // Ensure page has scrollable height if fixture content is short.
          if (fdoc.documentElement.scrollHeight <= fwin.innerHeight + 100) {
            const pad = fdoc.createElement('div');
            pad.style.height = '1200px';
            fdoc.body.appendChild(pad);
          }
          fwin.scrollTo(0, 260);
          return { scrollY: fwin.scrollY };
        });
        assert.ok(clipRes.scrollY >= 200, `${theme}/${label}: frame scrolled (${clipRes.scrollY})`);
        await page.waitForFunction(() => document.documentElement.hasAttribute('data-fscrolled'), null, { timeout: 3000 });
        const scrolledState = await page.evaluate(() => {
          const cp = getComputedStyle(document.getElementById('frames')).clipPath;
          return {
            hasAttr: document.documentElement.hasAttribute('data-fscrolled'),
            clipPath: cp
          };
        });
        assert.equal(scrolledState.hasAttr, true, `${theme}/${label}: data-fscrolled active while scrolled`);
        assert.ok(scrolledState.clipPath && scrolledState.clipPath.startsWith('inset('), `${theme}/${label}: frames clipped to stage well (${scrolledState.clipPath})`);
        if (theme === 'dark') await shot(page, `phase1-dark-scrolled-${label}`);

        // Scroll back to top -> clip clears so top-band handles remain interactive.
        await page.evaluate(() => document.querySelector('.sframe.on').contentWindow.scrollTo(0, 0));
        await page.waitForFunction(() => !document.documentElement.hasAttribute('data-fscrolled'), null, { timeout: 3000 });
        const topClip = await page.evaluate(() => getComputedStyle(document.getElementById('frames')).clipPath);
        assert.equal(topClip, 'none', `${theme}/${label}: clip clears at scroll top`);
      }

      assert.deepEqual(errors, [], `${theme}: unexpected page errors ${JSON.stringify(errors)}`);
      await context.close();
    }
    console.log('ide-shell phase1 browser: ok', JSON.stringify(summary));
  } finally {
    await browser.close();
  }
})().catch(e => {
  console.error(e);
  process.exit(1);
});
