// Run by TestBoardHoldScrollBrowser against a live isolated daemon.
//
// Clicking an ask's hold toggle on a scrolled free-canvas board must leave
// #bcol's scroll and the clicked toggle where they were, through the click and
// through the board SSE echo of the hold, both holding and releasing. The echo
// changes only the topic's hold markers, so the live section stays (no
// replacement), its chips follow, and a second tab follows the hold too.
'use strict';
const assert = require('node:assert/strict');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [base] = process.argv.slice(2);
const TOL = 2;

async function settle(target, ms = 120) {
  await target.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
  if (ms > 0) await new Promise(r => setTimeout(r, ms));
}

async function activeFrame(tab, rid) {
  await tab.waitForFunction(rid => {
    const f = document.querySelector('iframe.sframe.on');
    return !!(f && f.contentDocument?.documentElement.dataset.rid === rid && f.contentWindow?.wbPill && f.contentWindow?.WsBoardAnchor);
  }, rid);
  return (await tab.locator('iframe.sframe.on').elementHandle()).contentFrame();
}

async function open(browser) {
  const context = await browser.newContext({ viewport: { width: 1400, height: 900 }, reducedMotion: 'reduce' });
  context.setDefaultTimeout(12000);
  await context.addInitScript(() => {
    if (self.origin === 'null') return;
    const workers = navigator.serviceWorker;
    if (workers) Object.getPrototypeOf(workers).register = async () => { throw new Error('No service workers in fixture'); };
  });
  await context.route('**/*', async route => {
    const u = new URL(route.request().url());
    if (u.origin !== base) return route.abort();
    // The click's pointerdown claims #bcol for the user for 200ms; a live
    // daemon's echo lands after that, where the board swap's scroll keep runs.
    if (u.pathname.endsWith('/board/hold')) await new Promise(r => setTimeout(r, 400));
    if (u.pathname === '/favicon.ico') return route.fulfill({ status: 204 });
    if (u.pathname === '/rail/usage') return route.fulfill({ json: { entries: [], warnPercent: 80, critPercent: 95 } });
    return route.continue();
  });
  const tab = await context.newPage();
  const errors = [];
  tab.on('pageerror', e => errors.push(`pageerror: ${e.message}`));
  await tab.goto(base + '/s/sessone');
  const frame = await activeFrame(tab, 'sessone');
  await settle(frame, 600);
  return { context, tab, frame, errors };
}

// The board region's hash, which the SSE echo of a hold changes.
function boardHash(frame) {
  return frame.evaluate(() => document.getElementById('r-board').getAttribute('data-h'));
}

function probe(frame, item) {
  return frame.evaluate(item => {
    const col = document.getElementById('bcol');
    const el = document.getElementById(item), btn = el && el.querySelector('.wb-hold-toggle');
    const topic = el && el.closest('section.topic[data-topic]');
    const chip = sel => { const c = topic && topic.querySelector(':scope > .tophead .tbar ' + sel); return c ? (c.hidden ? 'hidden' : c.querySelector('.n').textContent) : 'none'; };
    return {
      scrollTop: col.scrollTop,
      btnY: btn ? btn.getBoundingClientRect().top - col.getBoundingClientRect().top : null,
      pressed: btn ? btn.getAttribute('aria-pressed') : null,
      heldClass: !!(el && el.classList.contains('wb-held')),
      sameTopic: !!(topic && topic.__holdMark === true),
      open: chip('[data-tbar-ask="open"]'),
      held: chip('[data-tbar-ask="held"]'),
      anchor: window.WsBoardAnchor.key()
    };
  }, item);
}

function mark(frame, item) {
  return frame.evaluate(item => { document.getElementById(item).closest('section.topic[data-topic]').__holdMark = true; }, item);
}

async function wheelTo(tab, frame, wheel) {
  const box = await (await frame.locator('#bcol').elementHandle()).boundingBox();
  await tab.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  for (let left = wheel; left > 0; left -= 300) { await tab.mouse.wheel(0, Math.min(300, left)); await settle(frame, 30); }
  await settle(frame, 700);
}

// Toggles inside the scroller that a pointer reaches (no sticky head over them).
function visibleToggles(frame) {
  return frame.evaluate(() => {
    const col = document.getElementById('bcol'), c = col.getBoundingClientRect();
    return Array.from(col.querySelectorAll('.wb-hold-toggle[data-wb-hold]'))
      .filter(b => {
        const r = b.getBoundingClientRect();
        return r.height && r.top > c.top && r.bottom < c.bottom - 60 && b.contains(document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2));
      })
      .map(b => ({ id: b.closest('[id^="item-"]').id, topic: 't:' + b.closest('section.topic[data-topic]').getAttribute('data-topic') }));
  });
}

async function clickHold(view, other, item, label) {
  const { tab, frame } = view;
  await mark(frame, item);
  if (other) await mark(other.frame, item);
  const before = await probe(frame, item);
  const hash = await boardHash(frame);
  const otherHash = other ? await boardHash(other.frame) : null;
  const want = before.pressed === 'true' ? 'false' : 'true';
  const posted = tab.waitForResponse(r => r.url().endsWith('/board/hold') && r.request().method() === 'POST');
  const box = await frame.locator(`#${item} .wb-hold-toggle`).boundingBox();
  await tab.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
  const resp = await posted;
  assert.equal(resp.status(), 200, `${label}: hold POST status`);
  await frame.waitForFunction(h => document.getElementById('r-board').getAttribute('data-h') !== h, hash);
  await settle(frame, 500);
  const after = await probe(frame, item);
  const msg = `${label} ${item} ${JSON.stringify(before)} => ${JSON.stringify(after)}`;
  console.log(msg);
  assert.equal(after.pressed, want, `${msg}: toggle pressed`);
  assert.equal(after.heldClass, want === 'true', `${msg}: wb-held class`);
  assert.ok(Math.abs(after.scrollTop - before.scrollTop) <= TOL, `${msg}: #bcol scrollTop moved ${after.scrollTop - before.scrollTop}px`);
  assert.ok(Math.abs(after.btnY - before.btnY) <= TOL, `${msg}: toggle moved ${after.btnY - before.btnY}px in the viewport`);
  assert.ok(after.sameTopic, `${msg}: the hold echo replaced the topic section`);
  const step = want === 'true' ? 1 : -1;
  const n = v => v === 'hidden' ? 0 : +v;
  assert.equal(n(after.held), n(before.held) + step, `${msg}: held chip`);
  assert.equal(n(after.open), n(before.open) - step, `${msg}: open chip`);
  if (other) {
    await other.frame.waitForFunction(h => document.getElementById('r-board').getAttribute('data-h') !== h, otherHash);
    await settle(other.frame, 300);
    const seen = await probe(other.frame, item);
    assert.equal(seen.pressed, want, `${label} other tab ${item}: toggle pressed ${JSON.stringify(seen)}`);
    assert.equal(seen.heldClass, want === 'true', `${label} other tab ${item}: wb-held class`);
    assert.equal(seen.held, after.held, `${label} other tab ${item}: held chip`);
    assert.ok(seen.sameTopic, `${label} other tab ${item}: the hold echo replaced the topic section`);
  }
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const other = await open(browser);
    const order = await other.frame.evaluate(() => Array.from(document.querySelectorAll('#bcol section.topic[data-topic]')).map(s => 't:' + s.getAttribute('data-topic')));
    // wheel 3300 parks the anchor topic's head near the top with its asks in
    // view (the case that jumped 600px); 3900 lands partway down a tall
    // anchor topic; the third scrolls on from 3900 until an ask of a topic
    // below the anchor is in view.
    for (const [name, wheel, below] of [['anchor-top', 3300, false], ['partway', 3900, false], ['below-anchor', 3900, true]]) {
      const view = await open(browser);
      const free = await view.frame.evaluate(() => { const c = document.getElementById('bcol'); return c.hasAttribute('data-board-free') && getComputedStyle(c).overflowY === 'auto'; });
      assert.ok(free, 'fixture renders the bounded free-canvas board');
      await wheelTo(view.tab, view.frame, wheel);
      const anchor = () => view.frame.evaluate(() => window.WsBoardAnchor.key());
      const pickBelow = async () => { const a = order.indexOf(await anchor()); return (await visibleToggles(view.frame)).filter(v => order.indexOf(v.topic) > a); };
      let visible = below ? await pickBelow() : (await visibleToggles(view.frame)).filter(v => v.topic === 't:t3' || v.topic === 't:t4');
      for (let step = 0; below && !visible.length && step < 20; step++) { await wheelTo(view.tab, view.frame, 150); visible = await pickBelow(); }
      assert.ok(visible.length >= 1, `${name}: no reachable toggle`);
      const items = [...new Set([visible[0].id, visible[visible.length - 1].id])];
      for (const item of items) {
        const label = `${name} anchor=${await anchor()} scrollTop=${await view.frame.evaluate(() => document.getElementById('bcol').scrollTop)}`;
        await clickHold(view, other, item, label + ' hold');
        await clickHold(view, other, item, label + ' release');
      }
      assert.deepEqual(view.errors, [], `${name}: page errors`);
      await view.context.close();
    }
    assert.deepEqual(other.errors, [], 'other tab: page errors');
    await other.context.close();
  } finally {
    await browser.close();
  }
})().catch(err => { console.error(err); process.exit(1); });
