const assert = require('node:assert/strict');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base] = process.argv.slice(2);

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    for (const vp of [{ width: 1680, height: 960 }, { width: 1440, height: 900 }]) {
      const ctx = await browser.newContext({ viewport: vp, colorScheme: 'dark', reducedMotion: 'no-preference', locale: 'en-US' });
      await ctx.addCookies([{ name: 'ws_dashboard_lang', value: 'en', url: base }]);
      const page = await ctx.newPage();
      const errors = [];
      page.on('pageerror', e => errors.push(e.message));
      await page.goto(base + '/s/polishsession', { waitUntil: 'domcontentloaded' });
      await page.waitForSelector('iframe.sframe.on');
      const frame = await (await page.locator('iframe.sframe.on').elementHandle()).contentFrame();
      await frame.waitForSelector('#r-convlive .clive .activity');
      await frame.waitForSelector('#r-entries details.d-turn');
      await page.waitForTimeout(260);

      // 1. Check gear title (minor polish: no stale 'layout' wording)
      const gearTitle = await page.locator('#gear').getAttribute('title');
      assert(gearTitle && !/layout/i.test(gearTitle), `gear title must not mention layout: ${gearTitle}`);

      // 2. Check Fix 1: canvas wrapped in .d-conv-motion-overlay, z-index & clipping, .conv.clive padding-bottom
      const motion = await frame.evaluate(() => {
        const overlay = document.querySelector('#grid > .d-conv-motion-overlay');
        const canvas = overlay && overlay.querySelector('.d-conv-motion-canvas');
        const composer = document.querySelector('#grid > .composer');
        const clive = document.querySelector('#r-convlive .conv.clive');
        const ovCS = overlay ? getComputedStyle(overlay) : null;
        const compCS = composer ? getComputedStyle(composer) : null;
        const cliveCS = clive ? getComputedStyle(clive) : null;
        return {
          hasOverlay: !!overlay,
          hasCanvas: !!canvas,
          overlayOverflow: ovCS && ovCS.overflow,
          overlayZ: ovCS ? Number(ovCS.zIndex) : NaN,
          composerZ: compCS ? Number(compCS.zIndex) : NaN,
          clivePadBottom: cliveCS ? parseFloat(cliveCS.paddingBottom) : NaN,
          overlayHidden: overlay ? overlay.hidden : null,
          canvasHidden: canvas ? canvas.hidden : null,
        };
      });
      assert.equal(motion.hasOverlay, true, 'overlay wrapper mounted on #grid');
      assert.equal(motion.hasCanvas, true, 'canvas nested inside overlay');
      assert.equal(motion.overlayOverflow, 'hidden', 'overlay clips canvas to convo area');
      assert(motion.overlayZ < motion.composerZ, `overlay z (${motion.overlayZ}) must sit below composer z (${motion.composerZ})`);
      assert(motion.clivePadBottom >= 24, `conv.clive needs bottom clearance >= 24px, got ${motion.clivePadBottom}`);
      assert.equal(motion.overlayHidden, false, 'active motion overlay visible');
      assert.equal(motion.canvasHidden, false, 'active motion canvas visible');

      // 3. Check Fix 3: #wbonly-hint removed & #wbonly ON keeps composer-pills un-truncated
      const wbOnly = await frame.evaluate(() => {
        const hint = document.getElementById('wbonly-hint');
        const btn = document.getElementById('wbonly');
        const pills = document.getElementById('composer-pills');
        return { hasHint: !!hint, hasBtn: !!btn, hasPills: !!pills };
      });
      assert.equal(wbOnly.hasHint, false, '#wbonly-hint must be removed');
      assert.equal(wbOnly.hasBtn, true, '#wbonly button present on capable session');
      await frame.locator('#wbonly').click();
      await page.waitForTimeout(80);
      const wbAfter = await frame.evaluate(() => {
        const btn = document.getElementById('wbonly');
        const cfoot = document.querySelector('#composer .cfoot');
        const pills = document.getElementById('composer-pills');
        return {
          pressed: btn.getAttribute('aria-pressed'),
          cfootOverflow: cfoot ? cfoot.scrollWidth - cfoot.clientWidth : 0,
          pillsOverflow: pills ? pills.scrollWidth - pills.clientWidth : 0,
        };
      });
      assert.equal(wbAfter.pressed, 'true', '#wbonly toggles on');
      assert(wbAfter.cfootOverflow <= 1, `cfoot must not overflow (${wbAfter.cfootOverflow}px)`);
      assert(wbAfter.pillsOverflow <= 1, `composer-pills must not truncate (${wbAfter.pillsOverflow}px)`);
      await frame.locator('#wbonly').click();
      await page.waitForTimeout(80);

      // 4. Check Fix 2, 5, 6 & minor polish (mail folding, per-tool .au-tok, flat unified drawer + continuous rail, aria-expanded toggle, .d-turn-tok flex-shrink)
      const turnsBefore = await frame.evaluate(() => {
        const entries = document.querySelector('#r-entries .centries');
        const topLevelMails = [...entries.children].filter(el => el.matches('.mail-block, details.mailline, div.mailline'));
        const turns = [...entries.querySelectorAll('details.d-turn')];
        return {
          topLevelMailCount: topLevelMails.length,
          turnCount: turns.length,
          turnSummaries: turns.map(t => ({
            text: t.querySelector('summary.d-turn-sum').textContent.trim(),
            ariaExpanded: t.querySelector('summary.d-turn-sum').getAttribute('aria-expanded'),
            mailsBadge: t.querySelector('.d-turn-mails')?.textContent.trim() || '',
            tokShrink: t.querySelector('.d-turn-tok') ? getComputedStyle(t.querySelector('.d-turn-tok')).flexShrink : '',
          })),
          liveMailsBadge: document.querySelector('#r-convlive details.d-turn .d-turn-mails')?.textContent.trim() || '',
        };
      });
      assert.equal(turnsBefore.topLevelMailCount, 0, 'in-turn mails must fold into .d-turn, not stay top-level');
      assert.equal(turnsBefore.turnCount, 2, 'completed tool+mail turn and mail-only turn');
      assert.equal(turnsBefore.turnSummaries[0].ariaExpanded, 'false');
      assert.equal(turnsBefore.turnSummaries[0].mailsBadge, '2 mails');
      assert.equal(turnsBefore.turnSummaries[0].tokShrink, '0', '.d-turn-tok must not shrink');
      assert(turnsBefore.turnSummaries[1].text.includes('2 mails'), `mail-only turn must show 2 mails: ${turnsBefore.turnSummaries[1].text}`);
      assert.equal(turnsBefore.liveMailsBadge, '1 mail', 'live turn summary must count folded mail');

      // Expand Turn 1 and verify aria-expanded toggle, per-tool .au-tok, folded mail rows, and flat unified rail geometry
      const firstSummary = frame.locator('#r-entries details.d-turn > summary.d-turn-sum').first();
      await firstSummary.click();
      await page.waitForTimeout(80);
      const expanded = await frame.evaluate(() => {
        const turn = document.querySelector('#r-entries details.d-turn');
        const sum = turn.querySelector(':scope > summary.d-turn-sum');
        const drawer = turn.querySelector(':scope > .d-turn-drawer');
        const audit = turn.querySelector(':scope > .conv-audit-items');
        // Inject a synthetic audit row into .conv-audit-items to verify seamless rail alignment when populated
        if (audit && !audit.children.length) {
          const item = document.createElement('div');
          item.className = 'conv-audit-row';
          item.textContent = 'Synthetic audit row';
          audit.appendChild(item);
        }
        const dCS = getComputedStyle(drawer);
        const aCS = audit ? getComputedStyle(audit) : null;
        const dRect = drawer.getBoundingClientRect();
        const aRect = audit ? audit.getBoundingClientRect() : null;
        const toolToks = [...drawer.querySelectorAll('.au-calls .au-tok')].map(el => el.textContent.trim());
        const mailRows = drawer.querySelectorAll('.d-turn-mail-gap .mailline').length;
        return {
          open: turn.open,
          ariaExpanded: sum.getAttribute('aria-expanded'),
          drawerBg: dCS.backgroundColor,
          drawerShadow: dCS.boxShadow,
          drawerRadius: dCS.borderRadius,
          drawerBorderLeftWidth: parseFloat(dCS.borderLeftWidth),
          auditBorderLeftWidth: aCS ? parseFloat(aCS.borderLeftWidth) : 0,
          leftDelta: aRect ? Math.abs(dRect.left - aRect.left) : 0,
          verticalGap: aRect ? Math.abs(aRect.top - dRect.bottom) : 0,
          toolToks,
          mailRows,
        };
      });
      assert.equal(expanded.open, true);
      assert.equal(expanded.ariaExpanded, 'true', 'summary aria-expanded syncs to true on expand');
      assert.equal(expanded.drawerBg, 'rgba(0, 0, 0, 0)', 'drawer must be flat/transparent, not card');
      assert.equal(expanded.drawerShadow, 'none', 'drawer must have no card shadow');
      assert.equal(expanded.drawerRadius, '0px', 'drawer must have no card border-radius');
      assert(expanded.drawerBorderLeftWidth >= 2, 'drawer must draw left vertical group rail');
      assert(expanded.auditBorderLeftWidth >= 2, 'conv-audit-items must continue left vertical group rail');
      assert(expanded.leftDelta <= 0.5, `drawer & audit rail must share exact left edge (delta=${expanded.leftDelta})`);
      assert(expanded.verticalGap <= 0.5, `drawer & audit rail must connect with 0px seam (gap=${expanded.verticalGap})`);
      assert.equal(expanded.toolToks.length, 2, 'each tool call shows its per-tool .au-tok');
      assert(expanded.toolToks.every(t => t.startsWith('+')), `tool token deltas expected: ${JSON.stringify(expanded.toolToks)}`);
      assert.equal(expanded.mailRows, 2, 'both folded mails render inside drawer');

      // Collapse Turn 1 again and check aria-expanded returns to false
      await firstSummary.click();
      await page.waitForTimeout(60);
      assert.equal(await firstSummary.getAttribute('aria-expanded'), 'false', 'aria-expanded returns to false on collapse');

      // 5. Check Fix 4: scroll containment with 14+ image thumbnails in #cimgs AND horizontal overflow check
      const containment = await frame.evaluate(() => {
        const cimgs = document.getElementById('cimgs');
        for (let i = 0; i < 16; i++) {
          const tile = document.createElement('span');
          tile.className = 'cimg';
          tile.style.cssText = 'display:inline-flex;width:64px;height:64px;background:#333;';
          tile.textContent = 'img' + i;
          cimgs.appendChild(tile);
        }
        const grid = document.getElementById('grid');
        const gridRect = grid.getBoundingClientRect();
        const childOverflows = [...grid.children].map(el => {
          const r = el.getBoundingClientRect();
          return {
            tag: el.tagName.toLowerCase(),
            id: el.id || '',
            cls: el.className || '',
            rightOverflow: Math.round((r.right - gridRect.right) * 100) / 100,
            width: Math.round(r.width * 100) / 100,
            hidden: el.hidden || getComputedStyle(el).display === 'none',
          };
        }).filter(c => !c.hidden && c.rightOverflow > 0.5);
        const dwrap = document.querySelector('.dwrap');
        return {
          cimgsClientH: cimgs.clientHeight,
          cimgsScrollH: cimgs.scrollHeight,
          gridScrollW: grid.scrollWidth,
          gridClientW: grid.clientWidth,
          gridScrollH: grid.scrollHeight,
          gridClientH: grid.clientHeight,
          dwrapScrollH: dwrap ? dwrap.scrollHeight : 0,
          dwrapClientH: dwrap ? dwrap.clientHeight : 0,
          docScrollH: document.documentElement.scrollHeight,
          docClientH: document.documentElement.clientHeight,
          docScrollW: document.documentElement.scrollWidth,
          docClientW: document.documentElement.clientWidth,
          childOverflows,
        };
      });
      const shellContainment = await page.evaluate(() => {
        const dwrap = document.querySelector('.dwrap');
        const rail = document.getElementById('rail');
        return {
          shellDocScrollH: document.documentElement.scrollHeight,
          shellDocClientH: document.documentElement.clientHeight,
          shellDocScrollW: document.documentElement.scrollWidth,
          shellDocClientW: document.documentElement.clientWidth,
          shellDwrapScrollH: dwrap ? dwrap.scrollHeight : 0,
          shellDwrapClientH: dwrap ? dwrap.clientHeight : 0,
          railScrollH: rail ? rail.scrollHeight : 0,
          railClientH: rail ? rail.clientHeight : 0,
        };
      });
      console.log(JSON.stringify({ viewport: vp, motion, wbAfter, expanded, containment, shellContainment }));
      assert(containment.cimgsClientH <= 134, `#cimgs must cap height <= 132px, got ${containment.cimgsClientH}`);
      assert(containment.cimgsScrollH > containment.cimgsClientH, '#cimgs must scroll internally when many images are attached');
      assert(containment.gridScrollW <= containment.gridClientW, `${vp.width}x${vp.height}: #grid horizontal overflow ${containment.gridScrollW} > ${containment.gridClientW} (offenders: ${JSON.stringify(containment.childOverflows)})`);
      assert(containment.docScrollH <= containment.docClientH, `${vp.width}x${vp.height}: frame document vertical overflow ${containment.docScrollH} > ${containment.docClientH}`);
      assert(containment.dwrapScrollH <= containment.dwrapClientH, `${vp.width}x${vp.height}: frame .dwrap vertical overflow ${containment.dwrapScrollH} > ${containment.dwrapClientH}`);
      assert(shellContainment.shellDocScrollH <= shellContainment.shellDocClientH, `${vp.width}x${vp.height}: shell document vertical overflow ${shellContainment.shellDocScrollH} > ${shellContainment.shellDocClientH}`);
      assert(shellContainment.shellDwrapScrollH <= shellContainment.shellDwrapClientH, `${vp.width}x${vp.height}: shell .dwrap vertical overflow ${shellContainment.shellDwrapScrollH} > ${shellContainment.shellDwrapClientH}`);
      assert.deepEqual(errors, [], `page errors at ${vp.width}x${vp.height}: ${errors.join('; ')}`);
      await ctx.close();
    }
    console.log('PASS conv-ux-restyle-live-polish at 1680x960 and 1440x900');
  } finally {
    await browser.close();
  }
})().catch(err => {
  console.error(err);
  process.exitCode = 1;
});
