// Run by TestSelectionCapsuleBrowser against a static render of four
// whiteboard selections: mention+comment, mention only, two mentions+comment,
// and a long topic with a long quote. Verifies compact single-line inline
// pills (.icapsule-selection) and inert escaped popover templates.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [file, shots] = process.argv.slice(2);

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const failures = [];
    for (const theme of ['dark', 'light']) {
      for (const width of [1440, 700]) {
        const page = await browser.newPage({ viewport: { width, height: 900 } });
        const errors = [];
        page.on('pageerror', e => errors.push(e.message));
        await page.setContent(fs.readFileSync(file, 'utf8').replace('THEME', theme));
        if (shots) {
          fs.mkdirSync(shots, { recursive: true });
          await page.screenshot({ path: path.join(shots, `${theme}-${width}.png`), fullPage: true });
        }
        const m = await page.evaluate(() => {
          return [...document.querySelectorAll('.msg.user .bub')].map((bub) => {
            const capsules = bub.querySelectorAll('.icapsule.icapsule-selection');
            const cap = capsules[0];
            const cs = cap ? getComputedStyle(cap) : null;
            const rect = cap ? cap.getBoundingClientRect() : null;
            const tpl = cap ? cap.querySelector('template.icapsule-source') : null;
            const quotes = tpl ? [...tpl.content.querySelectorAll('.icapsule-pop-item')] : [];
            const comment = tpl && tpl.content.querySelector('.icapsule-pop-comment');
            return {
              capsules: capsules.length,
              inlineFlex: cs && cs.display === 'inline-flex' && cs.alignItems === 'center',
              singleLine: rect && rect.height <= 24,
              hasSource: !!tpl,
              quotes: quotes.length,
              hasComment: !!(comment && comment.textContent),
              overflow: bub.scrollWidth > bub.clientWidth + 1,
            };
          });
        });
        const where = `${theme}@${width}`;
        try {
          assert.deepEqual(errors, [], where + ': no page errors');
          assert.equal(m.length, 4, where + ': four selection messages');
          assert.deepEqual(m.map((x) => x.quotes), [1, 1, 2, 1], where + ': one quote item per mention');
          assert.deepEqual(m.map((x) => x.hasComment), [true, false, true, true], where + ': comment presence matches fixture');
          m.forEach((x, i) => {
            assert.equal(x.capsules, 1, `${where} #${i}: exactly one inline selection capsule`);
            assert.ok(x.inlineFlex, `${where} #${i}: compact inline-flex pill`);
            assert.ok(x.singleLine, `${where} #${i}: fits on a single line`);
            assert.ok(x.hasSource, `${where} #${i}: carries inert icapsule-source template`);
            assert.equal(x.overflow, false, `${where} #${i}: nothing overflows the bubble`);
          });
        } catch (e) {
          failures.push(e.message);
        }
        await page.close();
      }
    }
    if (failures.length) throw new Error(failures.join('\n'));
    console.log('selection capsule ok');
  } finally {
    await browser.close();
  }
})().catch((e) => { console.error(e); process.exit(1); });
