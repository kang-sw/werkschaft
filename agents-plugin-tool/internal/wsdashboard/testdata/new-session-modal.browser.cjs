// Run by TestNewSessionModalBrowser against an isolated page fixture.
// argv: <base> <shots dir|"">
// Covers the redesigned v3 New Session modal in dark & light themes at wide
// (1280px) and narrow (480px) viewports: searchable Named Tier pill, editable
// Model combobox + custom entry, horizontal drag Effort knob + keyboard +
// reset, permission capsules + collapsed Advanced native disclosure + danger
// CTA state, plugin isolation switch, Escape/scrim/close dismissal, and wide
// Model/Effort box getBoundingClientRect() alignment (<= 1px).
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, shots] = process.argv.slice(2);

const LEVELS = ['read-only', 'workdir-write', 'full'];
const CHOICES = {
  harnesses: [
    {
      harness: 'claude', lead: true, available: true,
      models: [{ id: 'claude-opus-4-7' }, { id: 'claude-sonnet-4-6' }, { id: 'claude-haiku-4-5' }],
      efforts: ['low', 'medium', 'high', 'max'],
      permission_modes: ['auto', 'default', 'acceptEdits', 'plan', 'dontAsk', 'bypassPermissions'],
      permission_levels: LEVELS
    },
    {
      harness: 'codex', lead: true, available: true,
      models: [{ id: 'gpt-5.4' }, { id: 'gpt-5.4-mini' }],
      efforts: ['low', 'medium', 'high', 'xhigh'],
      permission_modes: ['read-only', 'workspace-write', 'danger-full-access'],
      permission_levels: LEVELS
    },
    {
      harness: 'agy', lead: true, available: true,
      models: [{ id: 'gemini-3-pro' }],
      efforts: ['low', 'high'],
      permission_modes: ['auto'],
      permission_levels: LEVELS
    },
    {
      harness: 'werk', lead: true, available: true,
      model_providers: ['openrouter', 'openai'],
      models: [{ id: 'vendor/fast', provider: 'openrouter' }, { id: 'gpt-5.4', provider: 'openai', display_name: 'GPT-5.4' }],
      efforts: [],
      permission_modes: [],
      permission_levels: LEVELS
    }
  ],
  tiers: [
    { name: 'deep-design', trace: 'deep-design', harness: 'claude', model: 'claude-opus-4-7', effort: 'max' },
    { name: 'fast-codex', trace: 'fast-codex', harness: 'codex', model: 'gpt-5.4-mini', effort: 'low' },
    { name: 'broken-tier', error: 'tier "broken-tier": unavailable' }
  ],
  last: {}
};

function out(name) { return shots ? path.join(shots, name) : undefined; }

(async () => {
  if (shots) fs.mkdirSync(shots, { recursive: true });
  const browser = await chromium.launch({ headless: true });
  try {
    for (const theme of ['dark', 'light']) {
      for (const width of [1280, 480]) {
        const posts = [], seq = [], labels = [];
        let leadCreated = false;
        const context = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: theme, locale: 'ko-KR', serviceWorkers: 'block' });
        await context.addCookies([{ name: 'ws_dashboard_lang', value: 'ko', url: base }]);
        await context.route('**/*', async route => {
          const req = route.request(), url = new URL(req.url());
          if (url.origin !== base) return route.abort();
          if (url.pathname === '/rail/usage') return route.fulfill({ json: { entries: [], warnPercent: 80, critPercent: 95 } });
          if (url.pathname === '/support/choices') return route.fulfill({ json: CHOICES, headers: { 'Cache-Control': 'no-store' } });
          if (url.pathname.startsWith('/agents/leads')) {
            posts.push({ path: url.pathname, body: req.postDataJSON() });
            seq.push('lead');
            if (leadCreated) return route.fulfill({ status: 201, json: { id: 'newsess' } });
            return route.fulfill({ status: 500, body: 'fixture: no runner' });
          }
          if (url.pathname === '/s/newsess/label') {
            seq.push('label');
            labels.push(req.postDataJSON());
            return route.fulfill({ json: { label: req.postDataJSON().label } });
          }
          if (url.pathname === '/s/newsess') {
            seq.push('nav');
            return route.fulfill({ contentType: 'text/html', body: '<!doctype html><title>new session</title>' });
          }
          return route.continue();
        });
        const page = await context.newPage();
        const errors = [];
        page.on('pageerror', e => { if (!/serviceWorker/.test(e.message)) errors.push(e.stack || e.message); });
        await page.goto(base + '/');
        await page.waitForTimeout(250);

        async function openModal() {
          if (await page.locator('#railbtn').isVisible() && !await page.locator('html').evaluate(el => el.classList.contains('rail-open'))) {
            await page.locator('#railbtn').click();
            await page.waitForTimeout(200);
          }
          await page.locator('#rail .rnew').first().click();
          const modal = page.locator('.rpop.ns-modal:not([hidden])');
          await modal.waitFor();
          await page.waitForTimeout(200);
          return modal;
        }

        const modal = await openModal();
        assert.equal(await page.locator('.ns-scrim:not([hidden])').isVisible(), true, `${theme}/${width}: backdrop scrim is visible`);

        // Measure layout & rects
        const metrics = await modal.evaluate((el, vpW) => {
          const r = sel => el.querySelector(sel).getBoundingClientRect();
          const modalR = el.getBoundingClientRect();
          const hdR = r('.ns-hd');
          const tierR = r('.ns-tier');
          const modLblR = r('.ns-mod .ns-lbl');
          const effLblR = r('.ns-eff .ns-lbl');
          const modBoxR = r('.ns-mod .ns-cb-in');
          const effBoxR = r('.ns-eff .ns-knob');
          const wrap = el.querySelector('.ns-cta-w'), kids = [...wrap.children];
          const ctaR = r('.ns-cta-w > .ns-cta'), labR = r('.ns-cta-w > .ns-label-in'), wrapR = wrap.getBoundingClientRect();
          const labCs = getComputedStyle(el.querySelector('.ns-label-in'));
          const label = {
            order: kids.map(k => k.className.split(' ')[0]),
            ctaBottom: ctaR.bottom, top: labR.top, bottom: labR.bottom, left: labR.left, right: labR.right, height: labR.height,
            wrapLeft: wrapR.left, wrapRight: wrapR.right, ctaHeight: ctaR.height, maxLength: el.querySelector('.ns-label-in').maxLength,
            bg: labCs.backgroundColor, ink: labCs.color, border: labCs.borderTopWidth,
            hintTop: wrap.querySelector('.ns-ctahint').hidden ? null : r('.ns-ctahint').top, harTop: r('.ns-har').top
          };
          return {
            label,
            viewportW: vpW,
            modal: { x: modalR.left, y: modalR.top, w: modalR.width, h: modalR.height, scrollW: el.scrollWidth, clientW: el.clientWidth },
            hd: { top: hdR.top, bottom: hdR.bottom, right: hdR.right },
            tier: { top: tierR.top, bottom: tierR.bottom, left: tierR.left },
            modLbl: { top: modLblR.top, height: modLblR.height },
            effLbl: { top: effLblR.top, height: effLblR.height },
            modBox: { top: modBoxR.top, bottom: modBoxR.bottom, height: modBoxR.height, right: modBoxR.right, width: modBoxR.width },
            effBox: { top: effBoxR.top, bottom: effBoxR.bottom, height: effBoxR.height, left: effBoxR.left, width: effBoxR.width }
          };
        }, width);

        assert(metrics.modal.x >= 0 && metrics.modal.x + metrics.modal.w <= width + 1, `${theme}/${width}: modal fits horizontally: ${JSON.stringify(metrics.modal)}`);
        assert(metrics.modal.y >= 0 && metrics.modal.y + metrics.modal.h <= 901, `${theme}/${width}: modal fits vertically: ${JSON.stringify(metrics.modal)}`);
        assert(metrics.modal.scrollW <= metrics.modal.clientW + 1, `${theme}/${width}: zero horizontal overflow: ${JSON.stringify(metrics.modal)}`);

        // Session label: the second child of the CTA wrapper, directly under
        // Start, full width, compact and subordinate, clear of what follows.
        const L = metrics.label;
        assert.deepEqual(L.order.slice(0, 2), ['ns-cta', 'ns-label-in'], `${theme}/${width}: label is the sibling directly after Start: ${JSON.stringify(L.order)}`);
        assert(L.top >= L.ctaBottom && L.top - L.ctaBottom <= 12, `${theme}/${width}: label sits right under Start: ${JSON.stringify(L)}`);
        assert(Math.abs(L.left - L.wrapLeft) <= 1 && Math.abs(L.right - L.wrapRight) <= 1, `${theme}/${width}: label is full width of the CTA wrapper: ${JSON.stringify(L)}`);
        assert(L.height >= 36 && L.height <= 38.01 && L.height < L.ctaHeight, `${theme}/${width}: label is compact and subordinate to Start: ${L.height} vs ${L.ctaHeight}`);
        assert.equal(L.maxLength, 64, 'label maxlength matches the route');
        assert.equal(L.border, '0px', `${theme}/${width}: label is borderless`);
        assert(L.bottom <= L.harTop, `${theme}/${width}: label does not overlap the harness bar`);
        console.log(`LABEL ${theme}/${width}: h=${L.height.toFixed(1)} gap=${(L.top - L.ctaBottom).toFixed(1)} bg=${L.bg} ink=${L.ink}`);

        if (width === 1280) {
          const boxTopDiff = Math.abs(metrics.modBox.top - metrics.effBox.top);
          const boxHeightDiff = Math.abs(metrics.modBox.height - metrics.effBox.height);
          const lblTopDiff = Math.abs(metrics.modLbl.top - metrics.effLbl.top);
          const lblHeightDiff = Math.abs(metrics.modLbl.height - metrics.effLbl.height);
          console.log(`RECTS ${theme}/${width}: modBox={top:${metrics.modBox.top.toFixed(2)},h:${metrics.modBox.height.toFixed(2)},w:${metrics.modBox.width.toFixed(2)}} effBox={top:${metrics.effBox.top.toFixed(2)},h:${metrics.effBox.height.toFixed(2)},w:${metrics.effBox.width.toFixed(2)}} topDiff=${boxTopDiff.toFixed(2)}px heightDiff=${boxHeightDiff.toFixed(2)}px lblTopDiff=${lblTopDiff.toFixed(2)}px lblHeightDiff=${lblHeightDiff.toFixed(2)}px`);
          assert(boxTopDiff < 0.005, `${theme}/${width}: Model/Effort box topDiff=${boxTopDiff}px != 0`);
          assert(boxHeightDiff < 0.005, `${theme}/${width}: Model/Effort box heightDiff=${boxHeightDiff}px != 0`);
          assert(lblTopDiff < 0.005, `${theme}/${width}: Model/Effort label topDiff=${lblTopDiff}px != 0`);
          assert(lblHeightDiff < 0.005, `${theme}/${width}: Model/Effort label heightDiff=${lblHeightDiff}px != 0`);
          assert(Math.abs(metrics.modLbl.height - 22) < 0.5 && Math.abs(metrics.modBox.height - 44) < 0.5, `${theme}/${width}: 22px label + 44px control: ${JSON.stringify([metrics.modLbl, metrics.modBox])}`);
          assert(metrics.modBox.right < metrics.effBox.left, `${theme}/${width}: Model and Effort do not overlap horizontally`);
        } else {
          assert(metrics.tier.top >= metrics.hd.bottom - 1, `${theme}/${width}: narrow tier pill drops cleanly below header: ${JSON.stringify(metrics)}`);
          assert(metrics.effBox.top >= metrics.modBox.bottom - 1, `${theme}/${width}: narrow Effort stacks cleanly below Model: ${JSON.stringify(metrics)}`);
          console.log(`RECTS ${theme}/${width}: narrow stacked hd.bottom=${metrics.hd.bottom.toFixed(1)} tier.top=${metrics.tier.top.toFixed(1)} modBox.bottom=${metrics.modBox.bottom.toFixed(1)} effBox.top=${metrics.effBox.top.toFixed(1)}`);
        }

        await page.screenshot({ path: out(`ns-default-${theme}-${width}.png`) });

        // Run full interactive behavior assertions in dark/1280 and light/480
        if ((theme === 'dark' && width === 1280) || (theme === 'light' && width === 480)) {
          // 1. Searchable Named Tier pill
          const tierInp = modal.locator('[data-tier-picker]');
          await tierInp.click();
          await tierInp.fill('fast');
          assert.equal(await modal.locator('.ns-tier .ns-cb-list li:not(.none)').count(), 1, 'tier filter narrows list');
          assert.equal(await modal.locator('.ns-tier .ns-cb-list li[data-val="fast-codex"]').isVisible(), true);
          // Escape closes dropdown first, modal stays open
          await page.keyboard.press('Escape');
          assert.equal(await modal.locator('.ns-tier .ns-cb-list').isVisible(), false, 'first Escape closes tier dropdown');
          assert.equal(await modal.isVisible(), true, 'first Escape keeps modal open');
          // Pick fast-codex
          await tierInp.click();
          await modal.locator('.ns-tier .ns-cb-list li[data-val="fast-codex"]').dispatchEvent('mousedown');
          assert.equal(await tierInp.inputValue(), 'fast-codex');
          assert.equal(await modal.locator('[data-harness="codex"]').getAttribute('aria-checked'), 'true');
          assert.equal(await modal.locator('[data-model-input]').inputValue(), 'gpt-5.4-mini');
          assert.equal(await modal.locator('[data-effort-knob]').getAttribute('aria-valuetext'), 'low');
          assert.match(await modal.locator('.ns-cta-sum').textContent(), /codex · gpt-5\.4-mini · low/i);

          // 2. Editable Model combobox + custom input
          const modInp = modal.locator('[data-model-input]');
          await modInp.click();
          await modInp.fill('gpt-5.4-custom');
          assert.equal(await modal.locator('.ns-tag').isVisible(), true, 'unlisted custom model shows custom badge');
          assert.equal(await tierInp.inputValue(), '', 'editing model manually reverts tier to manual');
          assert.match(await modal.locator('.ns-cta-sum').textContent(), /gpt-5\.4-custom/);
          await page.keyboard.press('Escape'); // closes model list

          // 3. Horizontal drag Effort knob + keyboard + reset
          const knob = modal.locator('[data-effort-knob]');
          const kRect = await knob.locator('.ktrack').boundingBox();
          await page.mouse.move(kRect.x + 2, kRect.y + kRect.height / 2);
          await page.mouse.down();
          await page.mouse.move(kRect.x + kRect.width - 2, kRect.y + kRect.height / 2, { steps: 5 });
          await page.mouse.up();
          assert.equal(await knob.getAttribute('aria-valuetext'), 'xhigh', 'dragging to right end snaps to xhigh');
          assert.equal(await modal.locator('.ns-eff .ns-v').textContent(), 'xhigh');
          await knob.press('ArrowLeft');
          assert.equal(await knob.getAttribute('aria-valuetext'), 'high', 'ArrowLeft steps down to high');
          await knob.press('Home');
          assert.equal(await knob.getAttribute('aria-valuetext'), 'low', 'Home jumps to first effort');
          await knob.press('End');
          assert.equal(await knob.getAttribute('aria-valuetext'), 'xhigh', 'End jumps to last effort');
          await modal.locator('.ns-eff .ns-rst').click();
          assert.equal(await knob.getAttribute('aria-valuetext'), '기본값', 'reset button restores default effort');
          assert.equal(await modal.locator('.ns-eff .ns-rst').isVisible(), false);

          // 4. Werk empty efforts + required model
          await modal.locator('[data-harness="werk"]').click();
          assert.equal(await knob.getAttribute('aria-disabled'), 'true', 'werk with no efforts disables effort knob');
          assert.equal(await modal.locator('.ns-cta.go').isDisabled(), true, 'werk requires non-empty model before Start');

          // 5. Switch back to Claude: Advanced disclosure + danger CTA states + isolation switch
          await modal.locator('[data-harness="claude"]').click();
          const adv = modal.locator('.pmadv');
          assert.equal(await adv.evaluate(d => d.open), false, 'Advanced starts collapsed');
          await adv.locator('summary').click();
          assert.equal(await adv.evaluate(d => d.open), true, 'Advanced expands on click');
          await modal.locator('.ns-adv-cap [data-mode="bypassPermissions"]').click();
          assert.equal(await modal.locator('.ns-cta.go').evaluate(e => e.classList.contains('danger')), true, 'bypassPermissions turns CTA danger-red');
          await modal.locator('.ns-adv-cap [data-mode="auto"]').click();
          assert.equal(await modal.locator('.ns-cta.go').evaluate(e => e.classList.contains('danger')), false);

          // Selecting level locks Advanced native capsules & full turns CTA danger-red
          await modal.locator('.pmseg [data-level="full"]').click();
          assert.equal(await modal.locator('.ns-cta.go').evaluate(e => e.classList.contains('danger')), true, 'full level turns CTA danger-red');
          assert.equal(await modal.locator('.ns-cta-warn').isVisible(), true);
          assert.equal(await modal.locator('.ns-adv-cap [data-mode="auto"]').isDisabled(), true, 'level locks Advanced native capsules');
          await page.screenshot({ path: out(`ns-danger-${theme}-${width}.png`) });

          // Plugin isolation, whiteboard-only, and conv-history switches
          const iso = modal.locator('[data-iso-switch]');
          const wb = modal.locator('[data-wb-only-switch]');
          const conv = modal.locator('[data-conv-history-switch]');
          assert.equal(await iso.getAttribute('aria-checked'), 'false');
          assert.equal(await wb.getAttribute('aria-checked'), 'false');
          assert.equal(await conv.getAttribute('aria-checked'), 'false');
          await iso.click();
          await wb.click();
          await conv.click();
          assert.equal(await iso.getAttribute('aria-checked'), 'true');
          assert.equal(await wb.getAttribute('aria-checked'), 'true');
          assert.equal(await conv.getAttribute('aria-checked'), 'true');
          await page.waitForTimeout(180);
          await page.screenshot({ path: out(`ns-toggles-${theme}-${width}.png`) });

          // Dismiss via x button, reopen, dismiss via backdrop scrim click
          await modal.locator('.ns-x').click();
          assert.equal(await page.locator('.rpop.ns-modal').isVisible(), false, 'x button hides modal completely');
          assert.equal(await page.locator('.ns-scrim:not([hidden])').count(), 0, 'scrim hides on close');
          const m2 = await openModal();
          await page.mouse.click(4, 4);
          assert.equal(await page.locator('.rpop.ns-modal').isVisible(), false, 'backdrop scrim click hides modal completely');
        } else {
          // Capture danger state screenshot for remaining theme/width combos too
          await modal.locator('.pmseg [data-level="full"]').click();
          await page.screenshot({ path: out(`ns-danger-${theme}-${width}.png`) });
          await page.keyboard.press('Escape');
          assert.equal(await page.locator('.rpop.ns-modal').isVisible(), false, 'Escape hides modal completely');
        }

        // Session label flow: a reopened modal starts with an empty label; a
        // typed label is written to the new session, trimmed, before the page
        // opens it; Enter in the field starts the session.
        {
          let m = await openModal();
          const lin = m.locator('.ns-label-in');
          await lin.fill('left over');
          await page.keyboard.press('Escape');
          m = await openModal();
          assert.equal(await lin.inputValue(), '', `${theme}/${width}: reopening clears the label`);
          assert.equal(await lin.getAttribute('placeholder'), '세션 라벨 (선택)');
          if (width === 1280 && theme === 'dark') {
            await page.screenshot({ path: out(`ns-label-empty-${theme}-${width}.png`) });
            await lin.fill('  스모크 라벨  ');
            await page.screenshot({ path: out(`ns-label-filled-${theme}-${width}.png`) });
          } else await lin.fill('  스모크 라벨  ');
          // Enable both opt-in toggles to verify conv_history_access payload & whiteboardFirst seeding
          await m.locator('[data-wb-only-switch]').click();
          await m.locator('[data-conv-history-switch]').click();
          leadCreated = true;
          seq.length = 0; labels.length = 0;
          await lin.focus();
          await Promise.all([page.waitForURL('**/s/newsess'), page.keyboard.press('Enter')]);
          for (let i = 0; i < 40 && !seq.includes('nav'); i++) await page.waitForTimeout(50);
          assert.deepEqual(seq, ['lead', 'label', 'nav'], `${theme}/${width}: create, then label, then navigation`);
          assert.deepEqual(labels, [{ label: '스모크 라벨' }], `${theme}/${width}: trimmed label payload`);
          assert.equal(posts.at(-1).path, '/agents/leads');
          assert.equal(posts.at(-1).body.conv_history_access, true, `${theme}/${width}: conv_history_access=true sent on creation`);
          const stored = await page.evaluate(() => ({
            wbFirst: localStorage.getItem('ws.dashboard.whiteboardFirst.newsess'),
            newSess: JSON.parse(localStorage.getItem('ws.dashboard.newSession') || '{}')
          }));
          assert.equal(stored.wbFirst, '1', `${theme}/${width}: whiteboardFirst.<id> seeded on 201`);
          assert.equal(stored.newSess.whiteboardOnly, true, `${theme}/${width}: whiteboardOnly remembered in ws.dashboard.newSession`);
        }

        assert.deepEqual(errors, [], `${theme}/${width} page errors`);
        await context.close();
      }
    }
    console.log('new-session modal browser QA passed');
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exit(1); });
