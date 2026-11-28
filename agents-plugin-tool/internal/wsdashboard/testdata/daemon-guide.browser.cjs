// Run by TestDaemonGuideBrowser against an isolated real daemon fixture.
// argv: <daemonBase> <ctrlBase>
const assert = require('node:assert/strict');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);
const [base, ctrl] = process.argv.slice(2);

async function ctrlJSON(path, body) {
  const res = await fetch(ctrl + path, body ? {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  } : undefined);
  assert.equal(res.status, 200, `${path} returned ${res.status}`);
  return res.json();
}

async function waitFor(label, fn, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  let lastErr = null;
  while (Date.now() < deadline) {
    try {
      const val = await fn();
      if (val) return val;
    } catch (e) {
      lastErr = e;
    }
    await new Promise(r => setTimeout(r, 80));
  }
  throw new Error(`timed out waiting for ${label}` + (lastErr ? `: ${lastErr.message}` : ''));
}

const sleep = ms => new Promise(r => setTimeout(r, ms));
const near = (a, b, tol = 1.5) => Math.abs(a - b) <= tol;

function assertRect(label, got, want, tol) {
  for (const k of ['x', 'y', 'width', 'height']) {
    assert.ok(near(got[k], want[k], tol), `${label}: ${k} got ${got[k]} want ${want[k]} (${JSON.stringify(got)} vs ${JSON.stringify(want)})`);
  }
}

async function highlight(body) {
  return ctrlJSON('/ctrl/highlight', body);
}

async function deliveries() {
  const { deliveries: list } = await ctrlJSON('/ctrl/deliveries');
  return list.map(d => {
    const lines = d.body.split('\n');
    return { owner: d.owner, header: lines[0], payload: JSON.parse(lines[lines.length - 1]) };
  });
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const errors = [];
  try {
    const context = await browser.newContext({
      viewport: { width: 1280, height: 860 },
      colorScheme: 'dark',
      locale: 'en-US',
      serviceWorkers: 'block'
    });
    await context.route(url => url.origin === base && url.pathname === '/rail/usage', route =>
      route.fulfill({ json: { entries: [], warnPercent: 80, critPercent: 95 } }));
    const page = await context.newPage();
    page.on('pageerror', e => {
      if (!/serviceWorker/.test(e.message)) {
        console.error('[pageerror]', e.stack || e.message);
        errors.push(e.stack || e.message);
      }
    });
    page.on('console', m => { if (m.type() === 'error') console.error('[console.error]', m.text()); });

    await page.goto(base + '/s/livesession');
    await page.frameLocator('.sframe.on').locator('#grid').waitFor();
    const tabId = await page.evaluate(() => window.wsDaemonTabId);
    assert.ok(tabId, 'local shell has a daemon tab id');
    await waitFor('tab registered', async () => {
      const { tabs } = await ctrlJSON('/ctrl/tabs');
      return tabs.some(t => t.tab_id === tabId && t.visible);
    });
    assert.equal(await page.evaluate(() => typeof window.WsDaemonGuide?.highlight), 'function');

    // Fixtures: a shell target, an excluded shell control, and a scrollable
    // session-frame target. Gaze events are recorded on the shell document.
    await page.evaluate(() => {
      const b = document.createElement('button');
      b.id = 'dg-shell-target';
      b.textContent = 'Guide shell target';
      b.style.cssText = 'position:fixed;left:240px;top:320px;width:150px;height:32px;z-index:5';
      document.body.appendChild(b);
      const ex = document.createElement('div');
      ex.setAttribute('data-ws-dom-exclude', '1');
      ex.innerHTML = '<button id="dg-excluded" style="position:fixed;left:20px;top:600px;z-index:5">Excluded guide control</button>';
      document.body.appendChild(ex);
      // User-editable content must never answer a text probe.
      const ta = document.createElement('textarea');
      ta.id = 'dg-draft';
      ta.textContent = 'Private draft phrase';
      ta.style.cssText = 'position:fixed;left:420px;top:600px;z-index:5';
      document.body.appendChild(ta);
      const ce = document.createElement('div');
      ce.contentEditable = 'true';
      ce.innerHTML = '<span>Editable draft words</span>';
      ce.style.cssText = 'position:fixed;left:620px;top:600px;z-index:5';
      document.body.appendChild(ce);
      window.__gaze = [];
      document.addEventListener('ws:guide-highlight-change', e => window.__gaze.push(e.detail));
    });
    const frame = page.frameLocator('.sframe.on');
    const frameHandle = await page.locator('.sframe.on').elementHandle();
    const fdoc = await frameHandle.contentFrame();
    await fdoc.evaluate(() => {
      const box = document.createElement('div');
      box.id = 'dg-scroller';
      box.style.cssText = 'position:fixed;left:120px;top:140px;width:320px;height:220px;overflow:auto;z-index:9999;background:#222';
      box.innerHTML = '<div style="height:1200px;padding-top:160px"><button id="dg-frame-target" style="width:140px;height:30px">Frame guide target</button></div>';
      document.body.appendChild(box);
    });
    await frame.locator('#dg-frame-target').waitFor();

    const shellRectOf = sel => page.evaluate(s => {
      const r = document.querySelector(s).getBoundingClientRect();
      return { x: r.left, y: r.top, width: r.width, height: r.height };
    }, sel);
    const frameRectOf = sel => page.evaluate(s => {
      const f = document.querySelector('.sframe.on');
      const fr = f.getBoundingClientRect();
      const r = f.contentDocument.querySelector(s).getBoundingClientRect();
      return { x: r.left + fr.left + f.clientLeft, y: r.top + fr.top + f.clientTop, width: r.width, height: r.height };
    }, sel);
    const spotRect = id => page.evaluate(i => {
      const spots = [...document.querySelectorAll('.dg-spot')].filter(s => !s.hidden);
      const s = spots[i || 0];
      if (!s) return null;
      const r = s.getBoundingClientRect();
      return { x: r.left + 4, y: r.top + 4, width: r.width - 8, height: r.height - 8 };
    }, id);

    // --- 1. Shell target: spotlight + card, projected rect, gaze event.
    let res = await highlight({ action: 'show', target: { selector: '#dg-shell-target' }, comment: 'Click here <b>first</b>', style: 'spotlight' });
    assert.equal(res.ok, true, JSON.stringify(res));
    assert.equal(res.response.scope, 'shell');
    assert.equal(res.response.visible, true);
    assert.equal(res.response.tab_id, tabId);
    assertRect('shell response rect', res.response.rect, await shellRectOf('#dg-shell-target'));
    assertRect('shell spot rect', await spotRect(0), await shellRectOf('#dg-shell-target'));
    const card = page.locator('.dg-card:not([hidden])');
    assert.equal(await card.locator('.dg-card-text').textContent(), 'Click here <b>first</b>', 'comment renders as text only');
    assert.equal(await page.locator('.dg-card b').count(), 0, 'comment is never parsed as HTML');
    assert.equal(await page.locator('.dg-layer').getAttribute('data-ws-dom-exclude'), '1');
    const gaze = await page.evaluate(() => window.__gaze.slice());
    assert.ok(gaze.some(g => g.id === 'default' && g.visible && g.style === 'spotlight' && g.has_comment && g.rect.width > 0), 'gaze show event: ' + JSON.stringify(gaze));

    // The overlay never appears in a daemon.dom snapshot.
    const dom = await ctrlJSON('/ctrl/dom', {});
    assert.equal(dom.ok, true, JSON.stringify(dom));
    assert.ok(!JSON.stringify(dom.response).includes('Click here'), 'overlay excluded from daemon.dom');

    // --- 2. × dismissal clears the slot and emits a gaze clear.
    await page.locator('.dg-card-close').click();
    await waitFor('slot cleared by ×', async () => (await page.locator('.dg-spot').count()) === 0);
    assert.ok((await page.evaluate(() => window.__gaze)).some(g => g.id === 'default' && !g.visible), 'gaze clear event');

    // --- 3. Session-frame target by text: projection follows scroll and resize.
    res = await highlight({ action: 'show', id: 'frame', target: { text: 'frame guide target' }, style: 'ring', comment: 'Inside the session' });
    assert.equal(res.ok, true, JSON.stringify(res));
    assert.equal(res.response.scope, 'frame');
    assert.equal(res.response.visible, true);
    assertRect('frame response rect', res.response.rect, await frameRectOf('#dg-frame-target'));
    assertRect('frame spot rect', await spotRect(0), await frameRectOf('#dg-frame-target'));
    await fdoc.evaluate(() => { document.getElementById('dg-scroller').scrollTop = 60; });
    await waitFor('spot follows frame scroll', async () => {
      const want = await frameRectOf('#dg-frame-target');
      const got = await spotRect(0);
      return got && near(got.y, want.y) && near(got.x, want.x);
    });
    await fdoc.evaluate(() => { document.getElementById('dg-frame-target').style.width = '220px'; });
    await waitFor('spot follows target resize', async () => {
      const got = await spotRect(0);
      return got && near(got.width, 220);
    });
    await page.setViewportSize({ width: 1180, height: 820 });
    await waitFor('spot follows viewport resize', async () => {
      const want = await frameRectOf('#dg-frame-target');
      const got = await spotRect(0);
      return got && near(got.x, want.x) && near(got.y, want.y);
    });
    // Scrolled out of its scroller's frame viewport: hidden, then back.
    await fdoc.evaluate(() => { document.getElementById('dg-scroller').style.top = '-400px'; });
    await waitFor('off-screen target hides', async () => (await spotRect(0)) === null);
    await fdoc.evaluate(() => { document.getElementById('dg-scroller').style.top = '140px'; });
    await waitFor('target visible again', async () => (await spotRect(0)) !== null);

    // --- 4. Idle frame-freedom: no rAF and no running pulse while static.
    await sleep(2600);
    const s1 = await page.evaluate(() => window.WsDaemonGuide.stats());
    await sleep(900);
    const s2 = await page.evaluate(() => window.WsDaemonGuide.stats());
    assert.equal(s2.raf_count, s1.raf_count, `rAF scheduled while static: ${JSON.stringify([s1, s2])}`);
    assert.equal(s2.raf_pending, false);
    const running = await page.evaluate(() => document.getAnimations().filter(a => a.animationName === 'dg-pulse' && a.playState === 'running').length);
    assert.equal(running, 0, 'finite pulse settled');

    // --- 5. ESC clears the most recent slot.
    await page.keyboard.press('Escape');
    await waitFor('ESC cleared the frame slot', async () => (await page.locator('.dg-spot').count()) === 0);

    // --- 6. Refusals: excluded, missing, invalid selector, slot cap; clear is idempotent.
    res = await highlight({ action: 'show', target: { text: 'Excluded guide control' } });
    assert.equal(res.ok, false);
    assert.equal(res.error, 'target_excluded', JSON.stringify(res));
    res = await highlight({ action: 'show', target: { selector: '#dg-nope' } });
    assert.equal(res.error, 'target_not_found', JSON.stringify(res));
    for (const probe of ['Private draft phrase', 'draft phrase', 'Editable draft words']) {
      res = await highlight({ action: 'show', target: { text: probe } });
      assert.equal(res.error, 'target_not_found', 'editable content is not a text target: ' + probe + ' ' + JSON.stringify(res));
    }
    res = await highlight({ action: 'show', target: { selector: 'button[[' } });
    assert.equal(res.error, 'invalid_request', JSON.stringify(res));
    assert.equal(res.reason, 'invalid_selector', JSON.stringify(res));
    for (let i = 0; i < 8; i++) {
      res = await highlight({ action: 'show', id: 'cap' + i, target: { selector: '#dg-shell-target' }, style: 'pulse' });
      assert.equal(res.ok, true, JSON.stringify(res));
    }
    res = await highlight({ action: 'show', id: 'cap8', target: { selector: '#dg-shell-target' } });
    assert.equal(res.error, 'too_many_highlights', JSON.stringify(res));
    res = await highlight({ action: 'show', id: 'cap3', target: { text: 'Guide shell target' }, comment: 'replaced' });
    assert.equal(res.ok, true, 'show on an existing id replaces it: ' + JSON.stringify(res));
    assert.equal(await page.locator('.dg-spot').count(), 8);
    res = await highlight({ action: 'clear', id: 'missing' });
    assert.equal(res.ok, true);
    assert.equal(res.response.cleared || 0, 0);
    res = await highlight({ action: 'clear_all' });
    assert.equal(res.ok, true);
    assert.equal(res.response.cleared, 8);
    assert.equal(await page.locator('.dg-spot').count(), 0);

    // --- 7. Monitor: chip, structural hooks coalesced, highlight click, stop chip.
    assert.equal(await page.locator('.dg-chip').count(), 0, 'no chip without a monitor');
    let mon = await ctrlJSON('/ctrl/monitor', { action: 'start', debounce_ms: 600 });
    assert.equal(mon.ok, true, JSON.stringify(mon));
    const monitorId = mon.response.monitor_id;
    const chip = page.locator('.dg-chip');
    await chip.waitFor();
    assert.equal((await chip.textContent()).trim(), 'Guide monitoring · Stop');

    // tab_switched (twice, debounced client side) + modal_opened inside one
    // server window coalesce into one delivered turn.
    await fdoc.evaluate(() => {
      const g = document.getElementById('grid');
      g.setAttribute('data-tab', 'board');
      g.setAttribute('data-tab', 'convo-x');
    });
    await page.evaluate(() => {
      const d = document.createElement('dialog');
      d.id = 'dg-test-dialog';
      d.textContent = 'secret typed text';
      document.body.appendChild(d);
      d.showModal();
    });
    await waitFor('coalesced structural delivery', async () => (await deliveries()).some(d => d.payload.event === 'tab_switched' || d.payload.event === 'modal_opened'), 5000);
    await sleep(900);
    let got = await deliveries();
    const structural = got.filter(d => d.payload.monitor_id === monitorId && d.payload.event !== 'monitor_stopped');
    assert.equal(structural.length, 1, 'one coalesced turn: ' + JSON.stringify(got));
    const turn = structural[0].payload;
    assert.deepEqual([...turn.events].sort(), ['modal_opened', 'tab_switched'], JSON.stringify(turn));
    assert.ok(turn.coalesced_count >= 2, JSON.stringify(turn));
    assert.equal(turn.tab_id, tabId);
    assert.ok(!JSON.stringify(got).includes('secret typed text'), 'never text content');
    assert.ok(structural[0].header.startsWith('[ws/daemon.monitor]'));

    // modal_closed, then highlight_clicked from a real click on the target.
    await page.evaluate(() => document.getElementById('dg-test-dialog').close());
    await waitFor('modal_closed delivered', async () => (await deliveries()).some(d => d.payload.event === 'modal_closed' && d.payload.modal === 'dg-test-dialog'), 5000);
    res = await highlight({ action: 'show', id: 'click-me', target: { selector: '#dg-shell-target' } });
    assert.equal(res.ok, true, JSON.stringify(res));
    await page.locator('#dg-shell-target').click();
    await waitFor('highlight_clicked delivered', async () => (await deliveries()).some(d => d.payload.event === 'highlight_clicked' && d.payload.highlight_id === 'click-me'), 5000);
    await highlight({ action: 'clear_all' });

    // The stop chip ends the monitor with stopped_by_user.
    await chip.click();
    await waitFor('chip hidden', async () => (await page.locator('.dg-chip').count()) === 0);
    await waitFor('stopped_by_user notice', async () => (await deliveries()).some(d => d.payload.event === 'monitor_stopped' && d.payload.reason === 'stopped_by_user'), 5000);
    mon = await ctrlJSON('/ctrl/monitor', { action: 'status' });
    assert.equal(mon.response.active, false);
    assert.equal(mon.response.stop_reason, 'stopped_by_user');
    // Hooks detach once no monitor is active: no further deliveries.
    const before = (await deliveries()).length;
    await fdoc.evaluate(() => document.getElementById('grid').setAttribute('data-tab', 'board'));
    await sleep(1200);
    assert.equal((await deliveries()).length, before, 'no events after stop');
    assert.equal((await page.evaluate(() => window.WsDaemonGuide.stats())).hooks, 0);

    assert.deepEqual(errors, []);
    console.log('daemon guide browser test: ok');
  } finally {
    await browser.close();
  }
})().catch(e => {
  console.error(e && e.stack || e);
  process.exit(1);
});
