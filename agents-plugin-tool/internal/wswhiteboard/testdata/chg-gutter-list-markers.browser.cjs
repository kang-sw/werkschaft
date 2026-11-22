const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const pw = require(process.env.WS_PLAYWRIGHT_MODULE);

const url = process.argv[2];
const shotsDir = process.argv[3] || '';
const shotPhase = process.argv[4] || '';

async function mountFrames(page) {
  await page.evaluate(() => {
    addEventListener('message', event => {
      if (!event.data || event.data.wbDoc !== 'height') return;
      const frame = [...document.querySelectorAll('iframe.doc-frame')].find(f => f.contentWindow === event.source);
      if (frame) frame.style.height = event.data.height + 'px';
    });
    document.querySelectorAll('div.doc-frame[data-srcdoc]').forEach(p => {
      const f = document.createElement('iframe');
      for (const a of p.attributes) if (a.name !== 'data-sandbox') f.setAttribute(a.name, a.value);
      f.setAttribute('sandbox', p.dataset.sandbox);
      f.srcdoc = p.dataset.srcdoc;
      f.style.height = '1px';
      p.replaceWith(f);
    });
  });
}

async function applyTheme(page, theme) {
  await page.evaluate(value => document.documentElement.setAttribute('data-theme', value), theme);
  for (const child of page.frames()) {
    if (child !== page.mainFrame()) {
      await child.evaluate(value => document.documentElement.setAttribute('data-theme', value), theme);
    }
  }
}

async function measureTopic(topicLocator) {
  const frame = topicLocator.frameLocator('iframe.doc-frame');
  await frame.locator('.wb-chg-hit').first().waitFor();
  return frame.locator('body').evaluate(() => {
    const body = document.querySelector('.doc-body');
    const bodyRect = body.getBoundingClientRect();
    const walk = document.createTreeWalker(body, NodeFilter.SHOW_COMMENT);
    const marks = [];
    let c;
    while ((c = walk.nextNode())) {
      if (c.data.indexOf('wbchg:') === 0) marks.push(c);
    }
    const hits = [...document.querySelectorAll('.wb-chg-hit')];
    return {
      bodyLeft: bodyRect.left,
      markCount: marks.length,
      hitCount: hits.length,
      items: marks.map((m, idx) => {
        const kind = m.data.slice(6);
        const el = kind === 'delafter' ? m.previousElementSibling : m.nextElementSibling;
        const hit = hits[idx];
        const bar = hit ? hit.querySelector('.wb-chg-bar') : null;
        const elRect = el.getBoundingClientRect();
        const barRect = bar.getBoundingClientRect();
        const hitRect = hit.getBoundingClientRect();
        const markerWidth = el.tagName === 'LI' ? (parseFloat(getComputedStyle(el, '::marker').width) || 0) : 0;
        const listStyleType = el.tagName === 'LI' ? getComputedStyle(el).listStyleType : '';
        const cb = el.tagName === 'LI' ? el.querySelector(':scope > input[type="checkbox"], :scope > p > input[type="checkbox"]') : null;
        const cbLeft = cb ? cb.getBoundingClientRect().left : null;
        const markerLeft = el.tagName === 'LI' && listStyleType !== 'none'
          ? elRect.left - markerWidth
          : (cbLeft !== null ? Math.min(elRect.left, cbLeft) : elRect.left);
        return {
          idx,
          kind,
          tag: el.tagName,
          parentTag: el.parentElement ? el.parentElement.tagName : '',
          text: el.textContent.trim().split('\n')[0].slice(0, 40),
          bodyLeft: bodyRect.left,
          elLeft: elRect.left,
          markerWidth,
          markerLeft,
          barLeft: barRect.left,
          barRight: barRect.right,
          hitLeft: hitRect.left,
          hitRight: hitRect.right,
        };
      }),
    };
  });
}

(async () => {
  const engines = [['chromium', pw.chromium]];
  if (pw.webkit && fs.existsSync(pw.webkit.executablePath())) {
    engines.push(['webkit', pw.webkit]);
  }
  const failures = [];
  for (const [engineName, browserType] of engines) {
    const browser = await browserType.launch({ headless: true });
    try {
      for (const theme of ['light', 'dark']) {
        const page = await browser.newPage({ viewport: { width: 720, height: 960 }, colorScheme: theme });
        const errors = [];
        page.on('pageerror', e => errors.push(e.message));
        await page.goto(url, { waitUntil: 'load' });
        await mountFrames(page);
        await page.locator('.topic[data-topic="ol-15"] iframe.doc-frame').waitFor();
        const ol15Frame = page.locator('.topic[data-topic="ol-15"]').frameLocator('iframe.doc-frame');
        await ol15Frame.locator('.wb-chg-hit').first().waitFor();
        await applyTheme(page, theme);
        await page.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));

        if (shotsDir) {
          fs.mkdirSync(shotsDir, { recursive: true });
          const prefix = shotPhase ? `${shotPhase}-` : '';
          await page.locator('.topic[data-topic="ol-15"]').screenshot({
            path: path.join(shotsDir, `${prefix}${engineName}-${theme}.png`),
          });
        }

        for (const topicName of ['ol-15', 'lists-matrix']) {
          const card = page.locator(`.topic[data-topic="${topicName}"]`);
          const data = await measureTopic(card);
          assert.equal(data.hitCount, data.markCount, `${engineName}/${theme}/${topicName}: hit count`);
          for (const item of data.items) {
            if (item.barLeft < item.bodyLeft) {
              failures.push(`${engineName}/${theme}/${topicName} [${item.text}]: barLeft=${item.barLeft} < bodyLeft=${item.bodyLeft}`);
            }
            if (item.hitLeft < item.bodyLeft) {
              failures.push(`${engineName}/${theme}/${topicName} [${item.text}]: hitLeft=${item.hitLeft} < bodyLeft=${item.bodyLeft}`);
            }
            if (item.markerLeft < item.bodyLeft) {
              failures.push(`${engineName}/${theme}/${topicName} [${item.text}]: markerLeft=${item.markerLeft} < bodyLeft=${item.bodyLeft} (clipped)`);
            }
            if (!(item.barRight <= item.markerLeft)) {
              failures.push(`${engineName}/${theme}/${topicName} [${item.text}]: barRight=${item.barRight} > markerLeft=${item.markerLeft}`);
            }
            if (!(item.hitRight <= item.markerLeft)) {
              failures.push(`${engineName}/${theme}/${topicName} [${item.text}]: hitRight=${item.hitRight} > markerLeft=${item.markerLeft}`);
            }
          }
        }

        // Verify hover hit area and tooltip still work on item 12 of ol-15.
        const firstHit = ol15Frame.locator('.wb-chg-hit').first();
        await firstHit.scrollIntoViewIfNeeded();
        const hitBox = await firstHit.boundingBox();
        assert.ok(hitBox && hitBox.width >= 12 && hitBox.height >= 12, 'hover hit box');
        await page.mouse.move(hitBox.x + 2, hitBox.y + 6);
        const tip = ol15Frame.locator('.wb-chg-tip');
        await tip.waitFor({ state: 'visible' });
        assert.equal(await tip.textContent(), '본문 5분 전 갱신');
        await page.mouse.move(2, 2);
        await tip.waitFor({ state: 'hidden' });

        assert.deepEqual(errors, [], `${engineName}/${theme} page errors`);
        await page.close();
      }
    } finally {
      await browser.close();
    }
  }
  if (failures.length) {
    console.error(failures.join('\n'));
    process.exitCode = 1;
    return;
  }
  console.log(`PASS: ${engines.map(e => e[0]).join(' + ')} light/dark ol-15 and lists-matrix gutter clearance + tooltip`);
})().catch(err => {
  console.error(err);
  process.exitCode = 1;
});
