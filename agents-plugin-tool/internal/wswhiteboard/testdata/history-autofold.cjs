// args: board.html shotdir. Topic "a" loads history above its fold, the owner
// wheels until the whole card is above the viewport, and the loaded rows fold
// back into the gap while a visible line in topic "b" stays put (< 2 px).
// Runs in the page scroller and in a nested one, then checks the dedicated
// GitLab-diff tonal gap band and right-aligned action in both themes.
const assert = require('node:assert/strict');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [file, shots] = process.argv.slice(2);

const state = (p) => p.evaluate(() => {
  const a = document.querySelector('.topic[data-topic="a"]');
  const gap = a.querySelector('.history-live > .history-gap');
  return { rows: a.querySelectorAll('.history-live > .timeline-row').length, gaps: a.querySelectorAll('.history-live > .history-gap').length, count: gap && +gap.getAttribute('data-count') };
});

async function open(b, nested, scheme, width) {
  const p = await b.newPage({ viewport: { width: width || 1280, height: 800 }, colorScheme: scheme || 'dark' });
  await p.goto('file://' + file); await p.waitForTimeout(600);
  if (nested) await p.evaluate(() => {
    const box = document.createElement('div');
    box.id = 'nest'; box.style.cssText = 'height:760px;overflow:auto';
    while (document.body.firstChild) box.appendChild(document.body.firstChild);
    document.body.appendChild(box);
  });
  await p.waitForTimeout(200);
  return p;
}

async function run(b, nested) {
  const key = nested ? 'nested' : 'page', fail = [];
  const p = await open(b, nested);
  const s0 = await state(p);
  assert(s0.gaps >= 1, key + ': topic a has a fold');
  const up = p.locator('.topic[data-topic="a"] .history-gap [data-load="up"]').first();
  await up.scrollIntoViewIfNeeded();
  await up.click();
  await p.waitForTimeout(1800); // the load-above pin settles
  const s1 = await state(p);
  if (!(s1.rows > s0.rows)) fail.push(`${key}: load above admitted no rows ${JSON.stringify([s0, s1])}`);
  if (!nested) {
    await up.scrollIntoViewIfNeeded(); await p.waitForTimeout(300);
    await p.screenshot({ path: `${shots}/before-expanded.png` });
  }
  // Pick the topic-b entry that lands mid-viewport once topic a's bottom is
  // 200 px above the view, and sample its y every frame.
  const plan = await p.evaluate(() => {
    let s = document.getElementById('nest') || document.scrollingElement;
    const a = document.querySelector('.topic[data-topic="a"]').getBoundingClientRect();
    const top = s === document.scrollingElement ? 0 : s.getBoundingClientRect().top;
    const by = Math.round(a.bottom - top + 200);
    const mid = innerHeight / 2 + by;
    const entries = [...document.querySelectorAll('.topic[data-topic="b"] .history-live .wb-entry')];
    const ref = entries.find(e => e.getBoundingClientRect().top >= mid) || entries[entries.length - 1];
    ref.setAttribute('data-test-ref', '1');
    const live = document.querySelector('.topic[data-topic="a"] .history-live');
    const rec = window.__rec = { samples: [], folded: null };
    const y = () => ref.getBoundingClientRect().top;
    (function tick() { rec.samples.push(y()); if (rec.samples.length > 3) rec.samples.shift(); requestAnimationFrame(tick); })();
    new MutationObserver(() => {
      if (rec.folded) return;
      // Callback runs after the fold and its compensation; the last frame
      // sample is the pre-fold position.
      rec.folded = { before: rec.samples[rec.samples.length - 1], sync: y(), at: performance.now() };
      requestAnimationFrame(() => requestAnimationFrame(() => { rec.folded.painted = y(); }));
      setTimeout(() => { rec.folded.later = y(); }, 500);
    }).observe(live, { childList: true });
    return { by };
  });
  const box = await p.evaluate(() => { const s = document.getElementById('nest'); const r = s ? s.getBoundingClientRect() : { left: 0, top: 0, width: innerWidth, height: innerHeight }; return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; });
  await p.mouse.move(box.x, box.y);
  // Owner wheel in steps, like a real scroll; the fold waits for it to end.
  for (let left = plan.by; left > 0; left -= 200) { await p.mouse.wheel(0, Math.min(200, left)); await p.waitForTimeout(16); }
  await p.waitForFunction(() => window.__rec.folded && window.__rec.folded.later !== undefined, null, { timeout: 5000 }).catch(() => {});
  const rec = await p.evaluate(() => window.__rec.folded);
  const out = await p.evaluate(() => document.querySelector('.topic[data-topic="a"]').getBoundingClientRect().bottom);
  const s2 = await state(p);
  if (!rec) fail.push(`${key}: topic a did not fold after leaving the view (bottom ${out}) ${JSON.stringify(s2)}`);
  else {
    const d = { sync: rec.sync - rec.before, painted: rec.painted - rec.before, later: rec.later - rec.before };
    console.log(`${key} fold anchor delta px ${JSON.stringify(d)}`);
    Object.entries(d).forEach(([k, v]) => { if (!(Math.abs(v) < 2)) fail.push(`${key}: visible line moved ${v}px (${k})`); });
    if (!(s2.rows === s0.rows && s2.count === s0.count && s2.gaps === s0.gaps)) fail.push(`${key}: fold did not restore the initial window ${JSON.stringify([s0, s1, s2])}`);
  }
  // Back in view: the fold row is the initial one again and loads again.
  await up.scrollIntoViewIfNeeded();
  await p.waitForTimeout(300);
  if (!nested) await p.screenshot({ path: `${shots}/after-folded.png` });
  await up.click(); await p.waitForTimeout(600);
  const s3 = await state(p);
  if (!(s3.rows === s1.rows)) fail.push(`${key}: reload after fold admitted ${s3.rows} rows, first load ${s1.rows}`);
  console.log(`${key} states ${JSON.stringify({ initial: s0, loaded: s1, folded: s2, reloaded: s3 })}`);
  await p.close();
  return fail;
}

// The right-aligned load action sits inside the dedicated gap band and never
// overflows the gap row at standard or narrow widths.
async function layout(b, scheme, width) {
  const p = await open(b, false, scheme, width);
  const gap = p.locator('.topic[data-topic="a"] .history-gap').first();
  await gap.scrollIntoViewIfNeeded();
  const m = await gap.evaluate((g) => {
    const range = g.querySelector('.gap-range').getBoundingClientRect();
    const act = g.querySelector('[data-load="up"]').getBoundingClientRect();
    const row = g.getBoundingClientRect();
    const gutter = g.querySelector('.gap-gutter').getBoundingClientRect();
    return {
      gutterW: gutter.width,
      rangeRight: range.right,
      actLeft: act.left,
      actRight: act.right,
      rowRight: row.right,
      rowH: row.height,
      overflow: g.scrollWidth > g.clientWidth + 1,
      sameLine: Math.abs(act.top - range.top) < 12,
    };
  });
  const r = await gap.boundingBox();
  await p.screenshot({ path: `${shots}/loadmore-layout-${scheme}-${width}.png`, clip: { x: Math.max(0, r.x - 40), y: Math.max(0, r.y - 60), width: Math.min(width, r.width + 80), height: r.height + 120 } });
  await p.close();
  const fail = [];
  if (m.overflow || m.actRight > m.rowRight + 0.5) fail.push(`${scheme} ${width}: action overflows row ${JSON.stringify(m)}`);
  if (m.rowH < 28) fail.push(`${scheme} ${width}: gap band height ${m.rowH} < 28`);
  if (m.gutterW < 36) fail.push(`${scheme} ${width}: gutter missing/too narrow ${m.gutterW}`);
  if (width >= 700 && !(m.sameLine && m.actLeft >= m.rangeRight && m.rowRight - m.actRight <= 24)) fail.push(`${scheme} ${width}: action not right-aligned on same line ${JSON.stringify(m)}`);
  console.log(`layout ${scheme} ${width} ${JSON.stringify(m)}`);
  return fail;
}

async function band(b, scheme) {
  const p = await open(b, false, scheme);
  const gap = p.locator('.topic[data-topic="a"] .history-gap').first();
  const up = gap.locator('[data-load="up"]');
  await gap.scrollIntoViewIfNeeded();
  const st = await gap.evaluate((g) => {
    const gc = getComputedStyle(g), gr = g.getBoundingClientRect();
    const btn = g.querySelector('[data-load="up"]'), bc = getComputedStyle(btn);
    return {
      rowBg: gc.backgroundColor,
      rowH: gr.height,
      btnBw: bc.borderTopWidth,
      btnBs: bc.borderTopStyle,
      btnColor: bc.color,
    };
  });
  const r = await gap.boundingBox();
  await p.screenshot({ path: `${shots}/loadmore-band-${scheme}.png`, clip: { x: Math.max(0, r.x - 40), y: Math.max(0, r.y - 60), width: Math.min(1280, r.width + 80), height: r.height + 120 } });
  await gap.hover(); await p.waitForTimeout(150);
  const hoverBg = await gap.evaluate((g) => getComputedStyle(g).backgroundColor);
  await p.close();
  const fail = [];
  const alpha = (c) => { const m = /rgba?\(([^)]+)\)/.exec(c); if (!m) return 1; const v = m[1].split(/[ ,/]+/).filter(Boolean); return v.length > 3 ? +v[3] : 1; };
  if (st.rowBg === 'transparent' || !(alpha(st.rowBg) > 0)) fail.push(`${scheme}: gap band background ${st.rowBg}`);
  if (st.rowH < 28) fail.push(`${scheme}: gap band height ${st.rowH} < 28`);
  if (!(st.btnBw === '0px' || st.btnBs === 'none')) fail.push(`${scheme}: action button border ${st.btnBw} ${st.btnBs}`);
  if (hoverBg === st.rowBg) fail.push(`${scheme}: no row hover change (${hoverBg})`);
  console.log(`band ${scheme} ${JSON.stringify(Object.assign(st, { hoverBg }))}`);
  return fail;
}

// A two-sided gap ([data-gap-dir="both"]) renders two full-width stacked
// compact rows (~25px each, ~50px total) with individual row hover wash,
// pointer-events: none overlays on .gap-info and .gap-gutter so clicks over
// the label/range/gutter route to the underlying up/down buttons, and zero
// overflow even on narrow split panes.
async function bothLayout(b, scheme, width) {
  const p = await open(b, false, scheme, width);
  // Materialize an early entry inside topic a's initial gap so split() creates
  // a genuine bidirectional middle gap ([data-gap-dir="both"]).
  const hasBoth = await p.evaluate(() => {
    window.wbHistory.loadTarget(document, 'e10');
    return !!document.querySelector('.topic[data-topic="a"] .history-gap[data-gap-dir="both"]');
  });
  await p.waitForTimeout(300);
  const fail = [];
  if (!hasBoth) {
    await p.close();
    return [`both ${scheme} ${width}: failed to create bidirectional gap`];
  }
  const gap = p.locator('.topic[data-topic="a"] .history-gap[data-gap-dir="both"]').first();
  await gap.scrollIntoViewIfNeeded();
  await p.waitForTimeout(150);
  const m = await gap.evaluate((g) => {
    const gr = g.getBoundingClientRect();
    const up = g.querySelector('[data-load="up"]');
    const down = g.querySelector('[data-load="down"]');
    const ur = up.getBoundingClientRect();
    const dr = down.getBoundingClientRect();
    const label = g.querySelector('.gap-label');
    const range = g.querySelector('.gap-range');
    const lr = label.getBoundingClientRect();
    const rr = range.getBoundingClientRect();
    const gutter = g.querySelector('.gap-gutter');
    const gutR = gutter.getBoundingClientRect();
    const hitAt = (x, y) => {
      const el = document.elementFromPoint(x, y);
      const btn = el && el.closest && el.closest('[data-load]');
      return btn ? btn.getAttribute('data-load') : (el ? el.className || el.tagName : null);
    };
    return {
      rowW: gr.width, rowH: gr.height,
      overflow: g.scrollWidth > g.clientWidth + 1,
      upW: ur.width, upH: ur.height, upTop: ur.top, upBottom: ur.bottom,
      downW: dr.width, downH: dr.height, downTop: dr.top, downBottom: dr.bottom,
      gutterW: gutR.width,
      labelInsideTop: lr.top >= ur.top - 1 && lr.bottom <= ur.bottom + 1,
      labelHit: hitAt(lr.left + lr.width / 2, lr.top + lr.height / 2),
      rangeHit: rr.width > 0 ? hitAt(rr.left + rr.width / 2, rr.top + rr.height / 2) : 'empty',
      gutterTopHit: hitAt(gutR.left + gutR.width / 2, ur.top + ur.height / 2),
      gutterBottomHit: hitAt(gutR.left + gutR.width / 2, dr.top + dr.height / 2),
      upBg: getComputedStyle(up).backgroundColor,
      downBg: getComputedStyle(down).backgroundColor,
      downBorderTop: getComputedStyle(down).borderTopWidth,
    };
  });
  const r = await gap.boundingBox();
  await p.screenshot({ path: `${shots}/both-gap-${scheme}-${width}.png`, clip: { x: Math.max(0, r.x - 40), y: Math.max(0, r.y - 60), width: Math.min(width, r.width + 80), height: r.height + 120 } });
  // Hover top row and verify only top row washes.
  await gap.locator('[data-load="up"]').hover();
  await p.waitForTimeout(150);
  const hover = await gap.evaluate((g) => ({
    upBg: getComputedStyle(g.querySelector('[data-load="up"]')).backgroundColor,
    downBg: getComputedStyle(g.querySelector('[data-load="down"]')).backgroundColor,
  }));
  // Click at .gap-label center coordinates and verify upward load fires.
  const beforeRows = await p.evaluate(() => document.querySelectorAll('.topic[data-topic="a"] .history-live > .timeline-row').length);
  const lblBox = await gap.locator('.gap-label').boundingBox();
  await p.mouse.click(lblBox.x + lblBox.width / 2, lblBox.y + lblBox.height / 2);
  await p.waitForTimeout(400);
  const afterRows = await p.evaluate(() => document.querySelectorAll('.topic[data-topic="a"] .history-live > .timeline-row').length);
  await p.close();
  if (m.overflow) fail.push(`both ${scheme} ${width}: horizontal overflow ${JSON.stringify(m)}`);
  if (Math.abs(m.upW - m.rowW) > 1 || Math.abs(m.downW - m.rowW) > 1) fail.push(`both ${scheme} ${width}: buttons not full-width ${JSON.stringify(m)}`);
  if (m.downTop < m.upBottom - 1) fail.push(`both ${scheme} ${width}: rows not stacked vertically ${JSON.stringify(m)}`);
  if (m.upH < 24 || m.downH < 24 || m.rowH < 48 || m.rowH > 62) fail.push(`both ${scheme} ${width}: unexpected stacked row height ${JSON.stringify(m)}`);
  if (m.gutterW < 36) fail.push(`both ${scheme} ${width}: gutter too narrow ${m.gutterW}`);
  if (!m.labelInsideTop) fail.push(`both ${scheme} ${width}: .gap-label not on top row ${JSON.stringify(m)}`);
  if (m.labelHit !== 'up') fail.push(`both ${scheme} ${width}: .gap-label click did not route to up (got ${m.labelHit})`);
  if (m.rangeHit !== 'up' && m.rangeHit !== 'empty') fail.push(`both ${scheme} ${width}: .gap-range click did not route to up (got ${m.rangeHit})`);
  if (m.gutterTopHit !== 'up' || m.gutterBottomHit !== 'down') fail.push(`both ${scheme} ${width}: gutter dead zone (${m.gutterTopHit}, ${m.gutterBottomHit})`);
  if (hover.upBg === m.upBg) fail.push(`both ${scheme} ${width}: top row hover did not wash (${hover.upBg})`);
  if (hover.downBg !== m.downBg) fail.push(`both ${scheme} ${width}: hovering top row washed bottom row (${m.downBg} -> ${hover.downBg})`);
  if (!(afterRows > beforeRows)) fail.push(`both ${scheme} ${width}: clicking .gap-label coordinates did not load above (${beforeRows} -> ${afterRows})`);
  console.log(`both ${scheme} ${width} ${JSON.stringify({ rowH: m.rowH, upH: m.upH, downH: m.downH, labelHit: m.labelHit, gutter: [m.gutterTopHit, m.gutterBottomHit], rows: [beforeRows, afterRows] })}`);
  return fail;
}

(async () => {
  const b = await chromium.launch();
  let fail = [];
  try {
    fail = fail.concat(await run(b, false));
    fail = fail.concat(await run(b, true));
    fail = fail.concat(await band(b, 'dark'));
    fail = fail.concat(await band(b, 'light'));
    for (const scheme of ['dark', 'light']) for (const width of [1440, 700, 480]) fail = fail.concat(await layout(b, scheme, width));
    for (const scheme of ['dark', 'light']) for (const width of [1280, 420]) fail = fail.concat(await bothLayout(b, scheme, width));
  } finally { await b.close(); }
  if (fail.length) { console.error(fail.join('\n')); process.exit(1); }
})().catch(e => { console.error(e); process.exit(1); });
