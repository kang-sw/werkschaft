// Run by TestIdeShellPhase2Browser with real Go-served pages.
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

function makeUsagePayload(activeCodex) {
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
          { kind: 'seven_day', percent: activeCodex === 'work' ? 41 : 18, asOf: at, resetsAt: at + 5 * 86400000 }
        ]
      }
    ],
    accounts: {
      codex: {
        active: activeCodex,
        slots: [
          { name: 'personal', label: 'personal@example.com', active: activeCodex === 'personal' },
          { name: 'work', label: 'work@example.com', active: activeCodex === 'work' }
        ]
      },
      claude: {
        active: 'primary',
        slots: [
          { name: 'primary', label: 'claude@example.com', active: true }
        ]
      }
    }
  };
}

function assertInViewportUpward(label, pop, anchor, vw, vh) {
  assert.ok(pop && anchor, `${label}: missing popover or anchor box`);
  assert.ok(pop.y >= 7, `${label}: popover top ${pop.y} clipped above viewport`);
  assert.ok(pop.x >= 7, `${label}: popover left ${pop.x} clipped off left edge`);
  assert.ok(pop.x + pop.width <= vw - 7, `${label}: popover right ${pop.x + pop.width} clipped past ${vw}`);
  assert.ok(pop.y + pop.height <= anchor.y + 1, `${label}: popover bottom ${pop.y + pop.height} must sit above anchor top ${anchor.y}`);
  assert.ok(pop.y + pop.height <= vh - 26 + 1, `${label}: popover bottom ${pop.y + pop.height} must clear 26px bar`);
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
      let activeCodex = 'personal';
      let switchPosts = [];
      let usageFetches = 0;

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
        if (url.pathname === '/rail/usage') {
          usageFetches++;
          return route.fulfill({ json: makeUsagePayload(activeCodex) });
        }
        if (url.pathname === '/api/accounts/select' && route.request().method() === 'POST') {
          const body = route.request().postDataJSON();
          switchPosts.push(body);
          if (body && body.harness === 'codex' && body.slot) activeCodex = body.slot;
          return route.fulfill({ json: { ok: true, active: activeCodex, action: 'switched' } });
        }
        return route.continue();
      });

      const page = await context.newPage();
      const errors = [];
      page.on('pageerror', e => errors.push(e.message));

      await page.goto(base + '/s/sess00');
      await page.frameLocator('.sframe.on').locator('#grid').waitFor();
      await page.locator('#shell-bottom .usage-number').first().waitFor();
      await page.waitForTimeout(240);

      // 1. Check 1200x800, 1440x900, 1920x1080:
      //    - 0px horizontal overflow
      //    - 26px 3-col grid bottom bar visible at every viewport
      //    - retired .rfoot (0), single #gear (1), bottom-left reachable without opening rail
      //    - upward-clamped #gmenu & .usage-pop + mutual exclusion
      //    - #talert empty & active states with 0 overlap
      for (const vp of [
        { width: 1200, height: 800, wide: false },
        { width: 1440, height: 900, wide: false },
        { width: 1920, height: 1080, wide: true }
      ]) {
        await page.setViewportSize({ width: vp.width, height: vp.height });
        await page.waitForTimeout(220);

        const baseMetrics = await page.evaluate(() => {
          const root = document.documentElement;
          const fdoc = document.querySelector('.sframe.on').contentDocument;
          const bar = document.getElementById('shell-bottom');
          const left = document.getElementById('slot-bot-left');
          const center = document.getElementById('slot-bot-center');
          const right = document.getElementById('slot-bot-right');
          const alertEl = document.getElementById('talert');
          const home = bar.querySelector('.rail-home');
          const cfg = bar.querySelector('.r-config-global');
          const gear = document.getElementById('gear');
          const usage = bar.querySelector('.usage-toggle');
          return {
            shellOverflow: root.scrollWidth - window.innerWidth,
            frameOverflow: fdoc.documentElement.scrollWidth - fdoc.defaultView.innerWidth,
            rfootCount: document.querySelectorAll('.rfoot').length,
            gearCount: document.querySelectorAll('#gear').length,
            usageCount: document.querySelectorAll('.usage-toggle').length,
            talertCount: document.querySelectorAll('#talert').length,
            barRect: bar.getBoundingClientRect().toJSON(),
            barCols: getComputedStyle(bar).gridTemplateColumns,
            leftRect: left.getBoundingClientRect().toJSON(),
            centerRect: center.getBoundingClientRect().toJSON(),
            rightRect: right.getBoundingClientRect().toJSON(),
            alertDisplay: getComputedStyle(alertEl).display,
            homeRect: home.getBoundingClientRect().toJSON(),
            cfgRect: cfg.getBoundingClientRect().toJSON(),
            gearRect: gear.getBoundingClientRect().toJSON(),
            usageRect: usage.getBoundingClientRect().toJSON(),
            railOpen: root.classList.contains('rail-open'),
            bu: bar.classList.contains('bu')
          };
        });

        assert.ok(baseMetrics.shellOverflow <= 0, `${theme}/${vp.width}: shell overflow ${baseMetrics.shellOverflow}px`);
        assert.ok(baseMetrics.frameOverflow <= 0, `${theme}/${vp.width}: frame overflow ${baseMetrics.frameOverflow}px`);
        assert.equal(baseMetrics.rfootCount, 0, `${theme}/${vp.width}: .rfoot must be retired from shell`);
        assert.equal(baseMetrics.gearCount, 1, `${theme}/${vp.width}: exactly one #gear in DOM`);
        assert.equal(baseMetrics.usageCount, 1, `${theme}/${vp.width}: exactly one .usage-toggle in DOM`);
        assert.equal(baseMetrics.talertCount, 1, `${theme}/${vp.width}: exactly one #talert in DOM`);
        assert.equal(Math.round(baseMetrics.barRect.height), 26, `${theme}/${vp.width}: 26px bottom bar`);
        assert.equal(Math.round(baseMetrics.barRect.bottom), vp.height, `${theme}/${vp.width}: bar flush to viewport bottom`);
        assert.equal(baseMetrics.alertDisplay, 'none', `${theme}/${vp.width}: empty #talert hidden`);
        assert.equal(baseMetrics.bu, false, `${theme}/${vp.width}: full usage pill fits at ${vp.width}px`);
        assert.ok(baseMetrics.homeRect.width > 0 && baseMetrics.cfgRect.width > 0 && baseMetrics.gearRect.width > 0,
          `${theme}/${vp.width}: bottom-left controls visible without opening rail`);
        assert.ok(baseMetrics.leftRect.right <= baseMetrics.rightRect.left + 0.5,
          `${theme}/${vp.width}: bot-left (${baseMetrics.leftRect.right}) does not overlap bot-right (${baseMetrics.rightRect.left})`);

        if (vp.width === 1440 || vp.width === 1920) {
          await shot(page, `phase2-${theme}-${vp.width}x${vp.height}`);
        }

        // Open #gmenu -> upward clamped inside viewport.
        await page.locator('#gear').click();
        await page.locator('#gmenu').waitFor({ state: 'visible' });
        const gmenuBox = await page.locator('#gmenu').boundingBox();
        const gearBox = await page.locator('#gear').boundingBox();
        assertInViewportUpward(`${theme}/${vp.width} #gmenu`, gmenuBox, gearBox, vp.width, vp.height);
        assert.equal(await page.locator('#gear').getAttribute('aria-expanded'), 'true');
        if (theme === 'dark' && vp.width === 1440) await shot(page, 'phase2-dark-1440-gmenu');

        // Mutual exclusion: clicking .usage-toggle closes #gmenu and opens .usage-pop upward.
        await page.locator('#shell-bottom .usage-toggle').click();
        await page.locator('.usage-pop').waitFor({ state: 'visible' });
        await page.locator('#gmenu').waitFor({ state: 'hidden' });
        assert.equal(await page.locator('#gear').getAttribute('aria-expanded'), 'false', `${theme}/${vp.width}: #gmenu closed when usage opened`);
        assert.equal(await page.locator('#shell-bottom .usage-toggle').getAttribute('aria-expanded'), 'true');
        const usagePopBox = await page.locator('.usage-pop').boundingBox();
        const usageBtnBox = await page.locator('#shell-bottom .usage-toggle').boundingBox();
        assertInViewportUpward(`${theme}/${vp.width} .usage-pop`, usagePopBox, usageBtnBox, vp.width, vp.height);
        if (theme === 'dark' && vp.width === 1440) await shot(page, 'phase2-dark-1440-usage-pop');

        // Mutual exclusion reverse: clicking #gear closes .usage-pop and opens #gmenu upward.
        await page.locator('#gear').click();
        await page.locator('#gmenu').waitFor({ state: 'visible' });
        await page.locator('.usage-pop').waitFor({ state: 'hidden' });
        assert.equal(await page.locator('#shell-bottom .usage-toggle').getAttribute('aria-expanded'), 'false', `${theme}/${vp.width}: .usage-pop closed when #gear opened`);
        await page.keyboard.press('Escape');
        await page.locator('#gmenu').waitFor({ state: 'hidden' });

        // #talert active state: centered horizontally at 0px offset, no overlap across 3 clusters.
        await page.evaluate(() => {
          const el = document.getElementById('talert');
          el.textContent = '2 approvals waiting across sessions';
          el.hidden = false;
        });
        await page.waitForTimeout(80);
        const alertMetrics = await page.evaluate(() => {
          const left = document.getElementById('slot-bot-left').getBoundingClientRect();
          const center = document.getElementById('slot-bot-center').getBoundingClientRect();
          const right = document.getElementById('slot-bot-right').getBoundingClientRect();
          const alert = document.getElementById('talert').getBoundingClientRect();
          const leftLast = document.getElementById('gear').getBoundingClientRect();
          const rightFirst = document.querySelector('#slot-bot-right .usage-toggle').getBoundingClientRect();
          return {
            vw: window.innerWidth,
            alertW: alert.width,
            alertCenter: (alert.left + alert.right) / 2,
            leftEdge: leftLast.right,
            centerLeft: center.left,
            centerRight: center.right,
            rightEdge: rightFirst.left,
            overflow: document.documentElement.scrollWidth - window.innerWidth
          };
        });
        assert.ok(alertMetrics.alertW > 40, `${theme}/${vp.width}: active #talert visible`);
        assert.ok(Math.abs(alertMetrics.alertCenter - alertMetrics.vw / 2) <= 2,
          `${theme}/${vp.width}: #talert centered (${alertMetrics.alertCenter} vs ${alertMetrics.vw / 2})`);
        assert.ok(alertMetrics.leftEdge <= alertMetrics.centerLeft + 0.5,
          `${theme}/${vp.width}: bot-left (${alertMetrics.leftEdge}) overlaps #talert (${alertMetrics.centerLeft})`);
        assert.ok(alertMetrics.centerRight <= alertMetrics.rightEdge + 0.5,
          `${theme}/${vp.width}: #talert (${alertMetrics.centerRight}) overlaps bot-right (${alertMetrics.rightEdge})`);
        assert.ok(alertMetrics.overflow <= 0, `${theme}/${vp.width}: 0px overflow with active #talert`);
        if (theme === 'dark' && vp.width === 1440) await shot(page, 'phase2-dark-1440-talert');

        // Clear #talert back to empty collapsed state.
        await page.evaluate(() => {
          const el = document.getElementById('talert');
          el.textContent = '';
          el.hidden = true;
        });
        await page.waitForTimeout(50);

        summary.push({ theme, vp: `${vp.width}x${vp.height}`, barH: Math.round(baseMetrics.barRect.height) });
      }

      // 2. Account switcher & compact step-down check at 1440x900 and 360x760.
      if (theme === 'dark') {
        await page.setViewportSize({ width: 1440, height: 900 });
        await page.waitForTimeout(160);
        await page.locator('#shell-bottom .usage-toggle').click();
        await page.locator('.usage-pop').waitFor({ state: 'visible' });
        const workBtn = page.locator('.usage-pop button', { hasText: 'work' }).first();
        if (await workBtn.count() > 0) {
          const beforeFetches = usageFetches;
          await workBtn.click();
          await page.waitForTimeout(200);
          assert.ok(switchPosts.length >= 1, '1-click account switcher posted selection');
          assert.ok(usageFetches > beforeFetches, 'account switch refreshed usage');
        }
        await page.keyboard.press('Escape');
        await page.locator('.usage-pop').waitFor({ state: 'hidden' });

        // Compact step-down (.bu) triggers on narrow 360px bar with active alert, restores on widen.
        await page.setViewportSize({ width: 360, height: 760 });
        await page.evaluate(() => {
          const el = document.getElementById('talert');
          el.textContent = 'Approval waiting';
          el.hidden = false;
        });
        await page.waitForTimeout(200);
        const narrowBar = await page.evaluate(() => {
          const bar = document.getElementById('shell-bottom');
          return {
            bu: bar.classList.contains('bu'),
            overflow: document.documentElement.scrollWidth - window.innerWidth,
            gearVis: document.getElementById('gear').getBoundingClientRect().width > 0
          };
        });
        assert.equal(narrowBar.bu, true, '360px bar compacts usage toggle (.bu)');
        assert.ok(narrowBar.overflow <= 0, `360px bar has 0px overflow (${narrowBar.overflow})`);
        assert.equal(narrowBar.gearVis, true, '#gear remains visible at 360px');

        // Narrow slide-over rail ends above the 26px bottom bar.
        await page.locator('#railbtn').click();
        await page.waitForTimeout(260);
        const narrowRail = await page.evaluate(() => {
          const rail = document.getElementById('rail').getBoundingClientRect();
          const bar = document.getElementById('shell-bottom').getBoundingClientRect();
          return { railBottom: rail.bottom, barTop: bar.top };
        });
        assert.ok(narrowRail.railBottom <= narrowRail.barTop + 1,
          `narrow overlay rail bottom (${narrowRail.railBottom}) must stop above bottom bar (${narrowRail.barTop})`);
        await page.keyboard.press('Escape');
        await page.evaluate(() => {
          const el = document.getElementById('talert');
          el.textContent = '';
          el.hidden = true;
        });
        await page.setViewportSize({ width: 1920, height: 1080 });
        await page.waitForTimeout(200);
        assert.equal(await page.locator('#shell-bottom').evaluate(el => el.classList.contains('bu')), false, 'widening restores full usage toggle');
      }

      // 2b. A wide multi-provider usage pill (~560px) is wider than half of a
      //     1100px bar but fits in the free middle space: it must stay expanded.
      //     It collapses to .bu only when the bar is genuinely too narrow or a
      //     visible #talert takes the middle.
      if (theme === 'dark') {
        await page.setViewportSize({ width: 1100, height: 800 });
        await page.waitForTimeout(160);
        const pillW = await page.evaluate(target => {
          const t = document.querySelector('#slot-bot-right .usage-toggle');
          const bar = document.getElementById('shell-bottom');
          bar.classList.remove('bu');
          const filler = document.createElement('span');
          filler.id = 'wide-pill-filler';
          filler.style.cssText = 'display:inline-block;height:1px;width:0';
          t.insertBefore(filler, t.firstChild);
          const natural = t.getBoundingClientRect().width;
          filler.style.width = Math.max(0, target - natural) + 'px';
          return t.getBoundingClientRect().width;
        }, 560);
        await page.waitForTimeout(200);
        const wide = await page.evaluate(() => {
          const bar = document.getElementById('shell-bottom');
          const right = document.getElementById('slot-bot-right').getBoundingClientRect();
          const left = document.getElementById('slot-bot-left').getBoundingClientRect();
          const t = document.querySelector('#slot-bot-right .usage-toggle').getBoundingClientRect();
          return { bu: bar.classList.contains('bu'), toggleW: t.width, rightW: right.width, barW: bar.getBoundingClientRect().width, leftRight: left.right, toggleLeft: t.left, overflow: document.documentElement.scrollWidth - window.innerWidth };
        });
        assert.ok(pillW >= 540 && wide.toggleW >= 540 && wide.toggleW > wide.barW / 2, `1100px: pill ~560px (${wide.toggleW}) is wider than half the bar (${wide.barW / 2})`);
        assert.equal(wide.bu, false, `1100px: ~560px usage pill stays expanded in free middle space (${JSON.stringify(wide)})`);
        assert.ok(wide.leftRight <= wide.toggleLeft + 0.5, `1100px: bot-left (${wide.leftRight}) does not collide with usage pill (${wide.toggleLeft})`);
        assert.ok(wide.overflow <= 0, '1100px: 0px horizontal overflow with expanded wide pill');

        await page.setViewportSize({ width: 520, height: 800 });
        await page.waitForTimeout(220);
        const narrow = await page.evaluate(() => {
          const bar = document.getElementById('shell-bottom');
          const left = document.getElementById('slot-bot-left');
          const right = document.getElementById('slot-bot-right').getBoundingClientRect();
          const t = document.querySelector('#slot-bot-right .usage-toggle').getBoundingClientRect();
          return { bu: bar.classList.contains('bu'), toggleLeft: t.left, toggleRight: t.right, rightLeft: right.left, rightRight: right.right, leftClipped: left.scrollWidth > left.clientWidth + 1, overflow: document.documentElement.scrollWidth - window.innerWidth };
        });
        assert.equal(narrow.bu, true, `520px: genuinely too narrow bar collapses to .bu (${JSON.stringify(narrow)})`);
        assert.ok(narrow.toggleLeft >= narrow.rightLeft - 0.5 && narrow.toggleRight <= narrow.rightRight + 0.5, '520px: collapsed handle stays inside the right cell');
        assert.equal(narrow.leftClipped, false, '520px: bot-left is not clipped by collapsed usage');
        assert.ok(narrow.overflow <= 0, '520px: 0px horizontal overflow');

        // Visible wide #talert takes the middle at 1100px: pill cannot keep full width.
        await page.setViewportSize({ width: 1100, height: 800 });
        await page.evaluate(() => {
          const el = document.getElementById('talert');
          el.textContent = 'Approval waiting for a long-running tool call needing attention';
          el.hidden = false;
        });
        await page.waitForTimeout(220);
        const withAlert = await page.evaluate(() => {
          const bar = document.getElementById('shell-bottom');
          const center = document.getElementById('slot-bot-center').getBoundingClientRect();
          const right = document.getElementById('slot-bot-right').getBoundingClientRect();
          const t = document.querySelector('#slot-bot-right .usage-toggle').getBoundingClientRect();
          return { bu: bar.classList.contains('bu'), centerRight: center.right, toggleLeft: t.left, rightLeft: right.left, overflow: document.documentElement.scrollWidth - window.innerWidth };
        });
        assert.equal(withAlert.bu, true, `1100px + wide #talert: pill collapses instead of overlapping (${JSON.stringify(withAlert)})`);
        assert.ok(withAlert.centerRight <= withAlert.toggleLeft + 0.5, '1100px + #talert: alert does not overlap collapsed handle');
        assert.ok(withAlert.overflow <= 0, '1100px + #talert: 0px overflow');

        // Clearing the alert frees the middle again: the full pill returns.
        await page.evaluate(() => {
          const el = document.getElementById('talert');
          el.textContent = '';
          el.hidden = true;
        });
        await page.waitForTimeout(220);
        assert.equal(await page.locator('#shell-bottom').evaluate(el => el.classList.contains('bu')), false, '1100px: pill expands again once #talert clears');
        await page.evaluate(() => { const f = document.getElementById('wide-pill-filler'); if (f) f.remove(); });
        await page.setViewportSize({ width: 1920, height: 1080 });
        await page.waitForTimeout(200);
      }

      // 3. data-fdim modal dimming & click-blocking per (1722).
      await page.setViewportSize({ width: 1440, height: 900 });
      await page.waitForTimeout(160);
      await page.evaluate(() => {
        document.documentElement.style.setProperty('--fdim', '0.45');
        document.documentElement.setAttribute('data-fdim', '');
      });
      await page.waitForTimeout(80);
      const fdimState = await page.evaluate(() => {
        const bar = document.getElementById('shell-bottom');
        const gear = document.getElementById('gear');
        const usage = bar.querySelector('.usage-toggle');
        const scrim = getComputedStyle(bar, '::after');
        return {
          barPE: getComputedStyle(bar).pointerEvents,
          gearPE: getComputedStyle(gear).pointerEvents,
          usagePE: getComputedStyle(usage).pointerEvents,
          scrimContent: scrim.content,
          scrimOpacity: parseFloat(scrim.opacity)
        };
      });
      assert.equal(fdimState.barPE, 'none', `${theme}: data-fdim blocks pointer-events on #shell-bottom`);
      assert.equal(fdimState.gearPE, 'none', `${theme}: data-fdim blocks pointer-events on #gear`);
      assert.equal(fdimState.usagePE, 'none', `${theme}: data-fdim blocks pointer-events on .usage-toggle`);
      assert.ok(fdimState.scrimOpacity > 0.4, `${theme}: data-fdim renders visible dim scrim (${fdimState.scrimOpacity})`);
      if (theme === 'dark') await shot(page, 'phase2-dark-1440-fdim');
      await page.evaluate(() => {
        document.documentElement.removeAttribute('data-fdim');
        document.documentElement.style.removeProperty('--fdim');
      });

      // 4. 0px vertical jitter on #rail-companion-slot at 1920x1080 with .rfoot retired.
      await page.setViewportSize({ width: 1920, height: 1080 });
      await page.waitForTimeout(180);
      await page.evaluate(() => {
        const rail = document.getElementById('rail');
        const slot = document.getElementById('rail-companion-slot');
        rail.dataset.companionDock = 'rail-slot';
        slot.hidden = false;
        slot.style.height = '240px';
      });
      await page.waitForTimeout(80);
      const jitter = await page.evaluate(() => {
        const slot = document.getElementById('rail-companion-slot');
        const rlist = document.getElementById('rlist');
        const unreg = document.querySelector('#rail details.rws-unreg');
        const bar = document.getElementById('shell-bottom');
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
          slotBottom: Math.round(r0.bottom),
          barTop: Math.round(bar.getBoundingClientRect().top)
        };
      });
      assert.equal(jitter.dScroll, 0, `${theme}: 0px scroll jitter on #rail-companion-slot`);
      assert.equal(jitter.dOpen, 0, `${theme}: 0px unreg-open jitter on #rail-companion-slot`);
      assert.equal(jitter.dClose, 0, `${theme}: 0px unreg-close jitter on #rail-companion-slot`);
      assert.ok(Math.abs(jitter.slotBottom - jitter.barTop) <= 1, `${theme}: companion slot ends flush above bottom bar`);

      // 5. Non-session pages (Home /, Worktree /w/..., Settings /settings) render cleanly with #shell-bottom.
      for (const [label, route] of [['home', '/'], ['worktree', wtRoute], ['settings', '/settings']]) {
        await page.goto(base + route);
        await page.waitForTimeout(260);
        const pageCheck = await page.evaluate(() => {
          const bar = document.getElementById('shell-bottom');
          return {
            hasBar: !!bar,
            barH: bar ? Math.round(bar.getBoundingClientRect().height) : 0,
            rfootCount: document.querySelectorAll('.rfoot').length,
            gearCount: document.querySelectorAll('#gear').length,
            overflow: document.documentElement.scrollWidth - window.innerWidth
          };
        });
        assert.equal(pageCheck.hasBar, true, `${theme}/${label}: #shell-bottom present`);
        assert.equal(pageCheck.barH, 26, `${theme}/${label}: #shell-bottom is 26px`);
        assert.equal(pageCheck.rfootCount, 0, `${theme}/${label}: .rfoot absent`);
        assert.equal(pageCheck.gearCount, 1, `${theme}/${label}: single #gear`);
        assert.ok(pageCheck.overflow <= 0, `${theme}/${label}: 0px horizontal overflow`);
      }

      assert.deepEqual(errors, [], `${theme}: unexpected page errors ${JSON.stringify(errors)}`);
      await context.close();
    }

    console.log('ide-shell phase2 browser: ok', JSON.stringify(summary));
  } finally {
    await browser.close();
  }
})().catch(e => {
  console.error(e);
  process.exit(1);
});
