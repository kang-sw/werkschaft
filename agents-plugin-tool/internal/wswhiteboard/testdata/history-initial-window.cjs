// args: long.html short.html. Asserts the 1000px initial window and chunked loads.
const assert = require('node:assert/strict');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
(async () => {
  const b = await chromium.launch();
  try {
    const meas = (p) => p.evaluate(() => {
      const rows = [...document.querySelectorAll('.history-live > .timeline-row')];
      return { rows: rows.length, gaps: document.querySelectorAll('.history-live > .history-gap').length, h: rows.reduce((s, r) => s + r.getBoundingClientRect().height, 0) };
    });
    const long = await b.newPage({ viewport: { width: 1600, height: 900 } });
    await long.goto('file://' + process.argv[2]); await long.waitForTimeout(600);
    const m0 = await meas(long);
    assert(m0.h >= 1000 && m0.gaps === 1, 'initial window >= 1000px with one fold: ' + JSON.stringify(m0));
    const gapBox = await long.evaluate(() => {
      const gap = document.querySelector('.history-live > .history-gap');
      const hist = document.querySelector('.wb-history');
      const r = gap.getBoundingClientRect(), hr = hist.getBoundingClientRect();
      return { h: r.height, w: r.width, hw: hr.width, overflow: gap.scrollWidth > gap.clientWidth + 1 };
    });
    assert(gapBox.h >= 28, 'gap band height >= 28px: ' + JSON.stringify(gapBox));
    assert(Math.abs(gapBox.w - gapBox.hw) <= 2, 'gap band spans history width: ' + JSON.stringify(gapBox));
    assert(!gapBox.overflow, 'no horizontal overflow at wide width: ' + JSON.stringify(gapBox));
    // Single-direction top gap: clicking row body (.gap-label) loads upward.
    await long.click('.history-gap .gap-label'); await long.waitForTimeout(400);
    const m1 = await meas(long);
    assert(m1.h - m0.h >= 1000, 'one row click adds >= 1000px: ' + JSON.stringify([m0, m1]));
    // Check zero horizontal overflow at standard (680px) and narrow (480px) topic widths.
    for (const topicWidth of [680, 480]) {
      await long.setViewportSize({ width: topicWidth + 40, height: 900 });
      await long.evaluate((w) => {
        const t = document.querySelector('section.topic');
        if (t) { t.style.width = w + 'px'; t.style.maxWidth = w + 'px'; }
      }, topicWidth);
      await long.waitForTimeout(100);
      const ov = await long.evaluate(() => {
        const t = document.querySelector('section.topic');
        const gaps = [...document.querySelectorAll('.history-gap')];
        return {
          topicOverflow: t.scrollWidth > t.clientWidth + 1,
          gapOverflow: gaps.some(g => g.scrollWidth > g.clientWidth + 1),
        };
      });
      assert(!ov.topicOverflow && !ov.gapOverflow, `zero overflow at ${topicWidth}px: ` + JSON.stringify(ov));
    }
    const short = await b.newPage({ viewport: { width: 1600, height: 900 } });
    await short.goto('file://' + process.argv[3]); await short.waitForTimeout(600);
    const s = await meas(short);
    assert(s.h < 1000 && s.gaps === 0, 'short history shows without a fold: ' + JSON.stringify(s));
    console.log(JSON.stringify({ initial: m0, afterClick: m1, short: s }));
  } finally { await b.close(); }
})().catch(e => { console.error(e); process.exit(1); });
