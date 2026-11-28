// Run by TestBoardRevealBrowser: a framed whiteboard page the shell loads
// hidden (display:none, so #bcol lays out at 0px), then shows with
// shown:true, must fill the middle of #bcol with topic cards without any user
// scroll, also when a board swap was held while it was hidden.
const assert = require('node:assert/strict');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base] = process.argv.slice(2);
const wait = ms => new Promise(r => setTimeout(r, ms));
// A reveal must land within a few frames of the show, not whenever a later trigger happens.
const frames = (page, n = 4) => page.evaluate(n => new Promise(r => { const step = () => (--n > 0 ? requestAnimationFrame(step) : r()); requestAnimationFrame(step); }), n);
async function until(fn, label, ms = 8000) {
  const end = Date.now() + ms;
  while (Date.now() < end) { if (await fn()) return; await wait(25); }
  throw new Error('timeout: ' + label);
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1500, height: 900 } });
    const tab = await context.newPage();
    const errors = [];
    tab.on('pageerror', e => errors.push(e.message));
    await context.addInitScript(() => {
      const Socket = window.WebSocket;
      window.pageSockets = [];
      window.WebSocket = class extends Socket { constructor(...a) { super(...a); window.pageSockets.push(this); } };
      // Only user input counts as a manual scroll; the page's own layout may set scrollTop.
      window.userScroll = 0;
      for (const t of ['wheel', 'touchmove', 'keydown', 'pointerdown']) addEventListener(t, () => { window.userScroll++; }, true);
    });
    await tab.route('**/__host', route => route.fulfill({ contentType: 'text/html', body: '<!doctype html><title>host</title><body style="margin:0"></body>' }));
    await tab.goto(base + '/__host');
    await tab.evaluate(() => {
      const f = document.createElement('iframe');
      f.id = 'f'; f.style.cssText = 'position:absolute;inset:0;width:100%;height:100%;border:0;visibility:hidden';
      f.src = '/s/wbreveal?frame=1';
      document.body.append(f);
    });
    const frame = async () => (await (await tab.locator('#f').elementHandle()).contentFrame());
    await until(async () => (await frame()).evaluate(() => document.readyState === 'complete').catch(() => false), 'hidden frame load');
    const f = await frame();
    await wait(600); // the hidden page's own init and ResizeObserver reports have run

    const show = visible => tab.evaluate(v => {
      const el = document.getElementById('f');
      el.style.visibility = v ? 'visible' : 'hidden';
      el.contentWindow.postMessage({ wsShell: 'shown', shown: v }, location.origin);
    }, visible);
    // The middle viewport is filled when the point at the center of #bcol's
    // box lands inside a laid-out topic card that really has height.
    const middle = () => f.evaluate(() => {
      const col = document.getElementById('bcol'), r = col.getBoundingClientRect();
      const x = r.left + r.width / 2, y = r.top + r.height / 2;
      const hit = document.elementFromPoint(x, y);
      const card = hit && hit.closest('[data-topic]');
      const cr = card && card.getBoundingClientRect();
      const visibleCards = [...col.querySelectorAll('[data-topic]')].filter(n => {
        const b = n.getBoundingClientRect();
        return n.getClientRects().length > 0 && b.height > 0 && b.bottom > r.top && b.top < r.bottom && getComputedStyle(n).visibility !== 'hidden';
      }).length;
      return { h: col.clientHeight, card: card ? card.getAttribute('data-topic') : null, cardH: cr ? cr.height : 0, visibleCards, userScroll: window.userScroll };
    });

    // 1. First show of a page loaded hidden.
    await show(true);
    await frames(f);
    assert.ok((await middle()).card, 'middle viewport cards after the first show: ' + JSON.stringify(await middle()));
    let m = await middle();
    assert.ok(m.h > 0 && m.cardH > 0 && m.visibleCards >= 1, 'first show fills the middle viewport: ' + JSON.stringify(m));
    assert.equal(m.userScroll, 0, 'no manual scroll was needed');

    // 2. Switch away, a board swap arrives while hidden (held), then switch back.
    await show(false);
    await wait(200);
    const held = await f.evaluate(() => {
      const sock = window.pageSockets.filter(s => s.url.includes('/events') && s.readyState === 1).pop();
      const el = document.querySelector('[data-region="board"]');
      const first = el.querySelector('[data-topic]');
      const html = el.innerHTML + first.outerHTML.split(first.getAttribute('data-topic')).join('reveal-held-new');
      sock.onmessage({ data: JSON.stringify({ event: 'region', data: { name: 'board', hash: 'held-swap', html } }) });
      return !!sock;
    });
    assert.ok(held, 'the held board swap was delivered');
    await wait(200);
    await show(true);
    await frames(f);
    assert.ok((await middle()).card, 'middle viewport cards after a held swap: ' + JSON.stringify(await middle()));
    m = await middle();
    assert.ok(await f.evaluate(() => document.querySelector('[data-region="board"]').getAttribute('data-h') === 'held-swap'), 'the held swap applied at the show');
    assert.ok(m.h > 0 && m.cardH > 0 && m.visibleCards >= 1, 'a held swap still fills the middle viewport: ' + JSON.stringify(m));

    // 3. Cached frame returning with stale scroll: scroll, hide, show again.
    // The reader scrolls once before leaving (real wheel input); the hide/show
    // round trip itself then needs no input.
    await tab.mouse.move(750, 450);
    await tab.mouse.wheel(0, 1500);
    await frames(f);
    assert.ok((await middle()).card, 'middle viewport cards after the reader scrolled: ' + JSON.stringify(await middle()));
    const scrolled = (await middle()).userScroll;
    await show(false);
    await wait(200);
    await show(true);
    await frames(f);
    assert.ok((await middle()).card, 'middle viewport cards after a cached return: ' + JSON.stringify(await middle()));
    m = await middle();
    assert.ok(m.h > 0 && m.cardH > 0 && m.visibleCards >= 1, 'a cached return fills the middle viewport: ' + JSON.stringify(m));
    assert.equal(m.userScroll, scrolled, 'no user input across the hide/show round trip');
    assert.deepEqual(errors, []);
    console.log('PASS board reveal: middle viewport filled after hidden load, held swap and cached return with 0 user scroll events: ' + JSON.stringify(m));
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exitCode = 1; });
