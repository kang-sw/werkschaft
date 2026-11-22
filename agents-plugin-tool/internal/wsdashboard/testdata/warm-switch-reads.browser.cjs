// Run by TestWarmSwitchReadsBrowser: region messages that arrive in one frame
// (a shown frame's stream sends every region that changed meanwhile) share one
// scroll read phase instead of reading each scroller's offset per message.
const assert = require('node:assert/strict');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base] = process.argv.slice(2);
const wait = ms => new Promise(r => setTimeout(r, ms));
const CHANGEABLE = ['convlive', 'entries', 'tree', 'ctx', 'git', 'cmds', 'sline'];

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1700, height: 1000 } });
    const tab = await context.newPage();
    const errors = [];
    tab.on('pageerror', e => errors.push(e.message));
    await context.addInitScript(() => {
      // The page's stream sockets, so a test can deliver messages to them in
      // one task (one frame) without a network.
      const Socket = window.WebSocket;
      window.pageSockets = [];
      window.WebSocket = class extends Socket { constructor(...a) { super(...a); window.pageSockets.push(this); } };
    });
    await tab.route('**/__host', route => route.fulfill({ contentType: 'text/html', body: '<!doctype html><title>host</title><body></body>' }));
    await tab.goto(base + '/__host');
    await tab.evaluate(() => {
      const f = document.createElement('iframe');
      f.id = 'f'; f.style.cssText = 'position:absolute;inset:0;width:100%;height:100%;border:0';
      f.src = '/s/livesession?frame=1';
      document.body.append(f);
    });
    const frame = async () => (await (await tab.locator('#f').elementHandle()).contentFrame());
    for (let i = 0; i < 200; i++) { try { if (await (await frame()).evaluate(() => document.readyState === 'complete')) break; } catch (e) { /* loading */ } await wait(25); }
    const f = await frame();
    await f.waitForSelector('#bcol');
    await tab.evaluate(() => document.getElementById('f').contentWindow.postMessage({ wsShell: 'shown', shown: true }, location.origin));
    await wait(1200);
    await f.evaluate(() => {
      const desc = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTop');
      window.phase = { bcol: 0, root: 0 };
      Object.defineProperty(Element.prototype, 'scrollTop', { configurable: true, set(v) { desc.set.call(this, v); },
        get() {
          if ((this.id === 'bcol' || this === document.scrollingElement) && new Error().stack.includes('scrollState')) window.phase[this.id === 'bcol' ? 'bcol' : 'root']++;
          return desc.get.call(this);
        } });
    });
    const cdp = await context.newCDPSession(tab);
    await cdp.send('Performance.enable');
    const layouts = async () => (await cdp.send('Performance.getMetrics')).metrics.find(m => m.name === 'LayoutCount').value;
    const before = await layouts();
    const delivered = await f.evaluate(names => {
      const sock = window.pageSockets.filter(s => s.url.includes('/events') && s.readyState === 1).pop();
      let n = 0;
      names.forEach((name, i) => {
        const el = document.querySelector('[data-region="' + name + '"]');
        if (!el) return;
        // One task: every message below is handled before the next frame.
        sock.onmessage({ data: JSON.stringify({ event: 'region', data: { name, hash: 'warm' + i, html: el.innerHTML + '<div class="warm" style="height:' + (4 + i) + 'px"></div>' } }) });
        n++;
      });
      return n;
    }, CHANGEABLE);
    await wait(500);
    const phase = await f.evaluate(() => window.phase);
    const forced = await layouts() - before;
    assert.ok(delivered >= 4, 'several region messages were delivered in one task (' + delivered + ')');
    const applied = await f.evaluate(names => names.map(n => (document.querySelector('[data-region="' + n + '"]') || document.body).getAttribute('data-h')), CHANGEABLE);
    assert.equal(applied.filter(h => /^warm/.test(h)).length, delivered, 'every delivered region landed');
    console.log('messages=' + delivered + ' scroll read phases: bcol=' + phase.bcol + ' document=' + phase.root + ' layouts in the window=' + forced);
    assert.equal(phase.bcol, 1, 'the board column offset is read once for ' + delivered + ' messages in one frame');
    assert.ok(phase.root <= 1, 'the document offset is read at most once');
    // A frame later the share has ended: the next message reads afresh.
    await f.evaluate(() => { window.phase.bcol = 0; });
    await f.evaluate(() => {
      const sock = window.pageSockets.filter(s => s.url.includes('/events') && s.readyState === 1).pop();
      const el = document.querySelector('[data-region="convlive"]');
      sock.onmessage({ data: JSON.stringify({ event: 'region', data: { name: 'convlive', hash: 'later', html: el.innerHTML + '<div class="warm" style="height:3px"></div>' } }) });
    });
    await wait(200);
    assert.equal(await f.evaluate(() => window.phase.bcol), 1, 'a message in a later frame reads the offsets again');
    assert.deepEqual(errors, []);
    console.log('PASS ' + delivered + ' region messages in one frame share one scroll read phase');
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exitCode = 1; });
