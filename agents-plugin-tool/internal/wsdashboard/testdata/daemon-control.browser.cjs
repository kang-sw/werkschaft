// Run by TestDaemonControlBrowser against an isolated real daemon fixture.
// argv: <daemonBase> <ctrlBase>
const assert = require('node:assert/strict');
const fs = require('node:fs');
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

const CHOICES_WITH_DC = {
  harnesses: [
    { harness: 'claude', lead: true, available: true, dashboard_control: true, models: [{ id: 'opus-4.7' }], efforts: ['low', 'high'], permission_modes: ['auto', 'plan'], permission_levels: ['read-only', 'workdir-write', 'full'] },
    { harness: 'codex', lead: true, available: true, dashboard_control: false, models: [{ id: 'gpt-5.5' }], efforts: ['low', 'high'], permission_modes: [], permission_levels: ['read-only', 'workdir-write', 'full'] }
  ],
  last: {}
};

(async () => {
  const browser = await chromium.launch({ headless: true });
  const errors = [];
  try {
    const S = {
      choices: CHOICES_WITH_DC,
      state: {
        id: 'livesession',
        lead: true,
        backend: 'claude',
        can_control: true,
        status: 'idle',
        process: true,
        safe_stop: true,
        model: 'opus-4.7',
        effort: 'high',
        held: 0,
        page: '/s/livesession',
        created_at: '2026-11-28T00:00:00Z',
        permission_mode: 'auto',
        active_mode: 'auto',
        permission_level: 'workdir-write',
        permission_source: 'legacy-derived',
        permission_enforcement: 'advisory',
        dashboard_control_access: false,
        dashboard_control_access_effective: false
      },
      posts: []
    };

    const context = await browser.newContext({
      viewport: { width: 1440, height: 900 },
      colorScheme: 'dark',
      locale: 'en-US',
      serviceWorkers: 'block'
    });

    await context.route(url => url.origin === base && (
      url.pathname === '/rail/usage' ||
      url.pathname === '/support/choices' ||
      url.pathname === '/agents' ||
      url.pathname.startsWith('/agents/')
    ), async route => {
      const req = route.request();
      const url = new URL(req.url());
      if (url.pathname === '/rail/usage') {
        return route.fulfill({ json: { entries: [], warnPercent: 80, critPercent: 95 } });
      }
      if (url.pathname === '/support/choices') {
        return route.fulfill({ json: S.choices, headers: { 'Cache-Control': 'no-store' } });
      }
      if (url.pathname === '/agents/livesession' && req.method() === 'GET') {
        return route.fulfill({ json: S.state });
      }
      if (url.pathname === '/agents/livesession/settings/dashboard-control' && req.method() === 'POST') {
        const body = req.postDataJSON();
        S.posts.push({ path: url.pathname, body });
        S.state = { ...S.state, dashboard_control_access: !!body.dashboard_control_access };
        return route.fulfill({ status: 200, json: S.state });
      }
      if (url.pathname === '/agents/livesession/stop' && req.method() === 'POST') {
        const body = req.postDataJSON();
        S.posts.push({ path: url.pathname, body });
        S.state = { ...S.state, process: false, dashboard_control_access_effective: S.state.dashboard_control_access };
        return route.fulfill({ status: 200, json: S.state });
      }
      if ((url.pathname === '/agents/leads' || url.pathname === '/agents/leads/permission') && req.method() === 'POST') {
        const body = req.postDataJSON();
        S.posts.push({ path: url.pathname, body });
        return route.fulfill({ status: 400, body: 'fixture intercepted create' });
      }
      return route.continue();
    });

    // --- Part 1: Multi-tab registration, focused-only banner, non-overlap with
    // data-openai-consent, anti-self-approval, and trusted Approve/Deny/Dismiss.
    const page1 = await context.newPage();
    const page2 = await context.newPage();
    for (const [idx, p] of [[1, page1], [2, page2]]) {
      p.on('pageerror', e => {
        if (!/serviceWorker/.test(e.message)) {
          console.error(`[page${idx} pageerror]`, e.stack || e.message);
          errors.push(e.stack || e.message);
        }
      });
      p.on('console', m => {
        if (m.type() === 'error') console.error(`[page${idx} console.error]`, m.text());
      });
    }

    await page1.goto(base + '/s/livesession');
    await page1.frameLocator('.sframe.on').locator('#grid').waitFor();
    try {
      await page1.locator('[data-openai-consent]:not([hidden])').waitFor({ timeout: 5000 });
    } catch (e) {
      const diag = await page1.evaluate(() => ({
        shell: document.documentElement.hasAttribute('data-shell'),
        sysopen: document.documentElement.hasAttribute('data-sysopen'),
        tabId: window.wsDaemonTabId || '',
        consentHTML: document.querySelector('[data-openai-consent]')?.outerHTML || null,
        confirmHTML: document.querySelector('[data-daemon-confirm]')?.outerHTML || null,
        sseClass: document.querySelector('.dwrap > .top .sse')?.className || null
      }));
      console.error('page1 diag:', JSON.stringify(diag), 'tabs:', JSON.stringify(await ctrlJSON('/ctrl/tabs')));
      throw e;
    }

    await page2.goto(base + '/s/livesession');
    await page2.frameLocator('.sframe.on').locator('#grid').waitFor();
    await page2.locator('[data-openai-consent]:not([hidden])').waitFor();

    const tab1 = await page1.evaluate(() => window.wsDaemonTabId);
    const tab2 = await page2.evaluate(() => window.wsDaemonTabId);
    assert.ok(tab1 && tab2 && tab1 !== tab2, `expected distinct tab IDs, got ${tab1} / ${tab2}`);

    // Make page2 the most recently focused visible tab.
    await page1.evaluate(() => window.dispatchEvent(new Event('blur')));
    await new Promise(r => setTimeout(r, 80));
    await page2.bringToFront();
    await page2.evaluate(() => window.dispatchEvent(new Event('focus')));

    await waitFor('both local shell tabs registered with page2 focused', async () => {
      const { tabs } = await ctrlJSON('/ctrl/tabs');
      const t1 = tabs.find(t => t.tab_id === tab1);
      const t2 = tabs.find(t => t.tab_id === tab2);
      return t1 && t2 && t2.visible && t2.capabilities && t2.capabilities.confirm_banner && t2.last_focused_at >= t1.last_focused_at;
    });

    // 1a. Immediate GET succeeds with zero confirmation banner on either tab.
    const getRes = await ctrlJSON('/ctrl/curl', { method: 'GET', path: '/health' });
    assert.equal(getRes.ok, true, `GET /health failed: ${JSON.stringify(getRes)}`);
    assert.equal(getRes.response.status, 200);
    assert.equal(await page1.locator('section[data-daemon-confirm]').isHidden(), true);
    assert.equal(await page2.locator('section[data-daemon-confirm]').isHidden(), true);

    // 1b. Mutating POST targets ONLY focused page2, coexists without overlap
    // with [data-openai-consent], ignores untrusted clicks, and settles on real Approve.
    const approveCall = ctrlJSON('/ctrl/curl', { method: 'POST', path: '/health' });
    const confirm2 = page2.locator('section[data-daemon-confirm]:not([hidden])');
    await confirm2.waitFor();

    // Unfocused tab 1 must NOT show the confirmation banner.
    assert.equal(await page1.locator('section[data-daemon-confirm]').isHidden(), true, 'unfocused tab must not show confirm banner');
    assert.equal(await confirm2.getAttribute('data-ws-dom-exclude'), '1', 'banner must carry data-ws-dom-exclude="1"');

    // Non-overlap geometry check: [data-openai-consent] above section[data-daemon-confirm],
    // and framed session content (#grid inside .sframe.on) starts cleanly below both banners.
    const geom = await page2.evaluate(() => {
      const c = document.querySelector('section[data-openai-consent]');
      const d = document.querySelector('section[data-daemon-confirm]');
      const f = document.querySelector('.frames');
      const iframe = document.querySelector('.sframe.on');
      const grid = iframe && iframe.contentDocument && iframe.contentDocument.querySelector('#grid');
      const cr = c.getBoundingClientRect();
      const dr = d.getBoundingClientRect();
      const fr = f.getBoundingClientRect();
      const gr = grid ? grid.getBoundingClientRect() : null;
      return {
        rootAccount: document.documentElement.hasAttribute('data-account-banner'),
        rootConfirm: document.documentElement.hasAttribute('data-daemon-confirm'),
        cr: { top: cr.top, bottom: cr.bottom, height: cr.height },
        dr: { top: dr.top, bottom: dr.bottom, height: dr.height },
        fr: { top: fr.top, bottom: fr.bottom, height: fr.height },
        gridViewportTop: gr ? fr.top + gr.top : null
      };
    });
    assert.equal(geom.rootAccount, true, 'root must keep data-account-banner');
    assert.equal(geom.rootConfirm, true, 'root must set data-daemon-confirm');
    assert.ok(geom.cr.height > 10, `consent banner height too small: ${JSON.stringify(geom.cr)}`);
    assert.ok(geom.dr.height > 10, `confirm banner height too small: ${JSON.stringify(geom.dr)}`);
    assert.ok(geom.cr.bottom <= geom.dr.top + 1, `banners overlap: consent=${JSON.stringify(geom.cr)} confirm=${JSON.stringify(geom.dr)}`);
    assert.ok(geom.gridViewportTop !== null && geom.dr.bottom <= geom.gridViewportTop + 1, `confirm banner overlaps framed #grid: confirm=${JSON.stringify(geom.dr)} gridViewportTop=${geom.gridViewportTop}`);

    // Anti-self-approval: synthetic untrusted click must be ignored completely.
    await page2.evaluate(() => {
      const btn = document.querySelector('section[data-daemon-confirm] [data-daemon-confirm-action="approve"]');
      btn.click();
      btn.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    });
    await new Promise(r => setTimeout(r, 200));
    assert.equal(await confirm2.isVisible(), true, 'untrusted synthetic click must NOT dismiss or approve banner');

    // Trusted Playwright click approves and resolves real DaemonCurl (POST /health
    // reaches the real daemon mux and returns 405 Method Not Allowed).
    await confirm2.locator('[data-daemon-confirm-action="approve"]').click();
    const approveRes = await approveCall;
    assert.equal(approveRes.ok, true, `approved POST failed: ${JSON.stringify(approveRes)}`);
    assert.equal(approveRes.response.status, 405);
    await waitFor('confirm banner hidden after approve', () => page2.locator('section[data-daemon-confirm]').isHidden());
    // OpenAI consent banner must remain untouched after confirm banner closes.
    assert.equal(await page2.locator('section[data-openai-consent]').isVisible(), true);

    // 1c. Real trusted Deny settles DaemonCurl with denied.
    const denyCall = ctrlJSON('/ctrl/curl', { method: 'POST', path: '/health', tab_id: tab2 });
    await confirm2.waitFor();
    await confirm2.locator('[data-daemon-confirm-action="deny"]').click();
    const denyRes = await denyCall;
    assert.equal(denyRes.ok, false);
    assert.equal(denyRes.error, 'denied');
    await waitFor('confirm banner hidden after deny', () => page2.locator('section[data-daemon-confirm]').isHidden());

    // 1d. Real trusted Dismiss settles DaemonCurl with denied.
    const dismissCall = ctrlJSON('/ctrl/curl', { method: 'POST', path: '/health', tab_id: tab2 });
    await confirm2.waitFor();
    await confirm2.locator('[data-daemon-confirm-action="dismiss"]').click();
    const dismissRes = await dismissCall;
    assert.equal(dismissRes.ok, false);
    assert.equal(dismissRes.error, 'denied');
    await waitFor('confirm banner hidden after dismiss', () => page2.locator('section[data-daemon-confirm]').isHidden());

    // --- Part 2: New-session modal opt-in switch (default OFF, never saved,
    // hidden when harness lacks dashboard_control: true).
    async function openRail() {
      if (await page2.locator('#railbtn').isVisible() && !await page2.locator('html').evaluate(el => el.classList.contains('rail-open'))) {
        await page2.locator('#railbtn').click();
        await page2.waitForTimeout(200);
      }
    }
    await openRail();
    await page2.locator('#rail .rnew').first().click();
    const newPop = page2.locator('.rpop:not([hidden])').filter({ has: page2.locator('.pmsec') });
    await newPop.waitFor();
    const nsSwitch = newPop.locator('[data-dashboard-control-switch="1"]');
    await nsSwitch.waitFor();
    assert.equal(await nsSwitch.getAttribute('aria-checked'), 'false', 'new-session switch must default OFF');

    // Switching to harness without dashboard_control hides the switch.
    await newPop.locator('.spseg [data-harness="codex"]').click();
    assert.equal(await nsSwitch.isHidden(), true, 'switch must hide when harness lacks dashboard_control');

    // Back to claude: switch visible and OFF; toggle ON and submit.
    await newPop.locator('.spseg [data-harness="claude"]').click();
    assert.equal(await nsSwitch.isVisible(), true);
    assert.equal(await nsSwitch.getAttribute('aria-checked'), 'false');
    await nsSwitch.click();
    assert.equal(await nsSwitch.getAttribute('aria-checked'), 'true');
    S.posts = [];
    await newPop.locator('.ns-cta.go').click();
    await waitFor('create POST intercepted', () => S.posts.length > 0);
    assert.equal(S.posts[0].path, '/agents/leads');
    assert.equal(S.posts[0].body.dashboard_control_access, true);
    // Must never persist dashboard_control_access into localStorage.
    const savedRaw = await page2.evaluate(() => localStorage.getItem('ws.dashboard.newSession') || '');
    assert.equal(savedRaw.includes('dashboard'), false, `dashboard control must not persist in localStorage: ${savedRaw}`);
    await page2.keyboard.press('Escape');
    await waitFor('new-session popover closed', () => newPop.isHidden());

    // Reopening new-session modal resets switch to OFF.
    await openRail();
    await page2.locator('#rail .rnew').first().click();
    await newPop.waitFor();
    assert.equal(await nsSwitch.getAttribute('aria-checked'), 'false', 'reopened new-session switch must reset to OFF');
    await page2.keyboard.press('Escape');
    await waitFor('new-session popover closed again', () => newPop.isHidden());
    if (await page2.locator('html').evaluate(el => el.classList.contains('rail-open'))) {
      await page2.keyboard.press('Escape');
      await waitFor('rail closed', () => page2.locator('html').evaluate(el => !el.classList.contains('rail-open')));
    }

    // --- Part 3: Existing-session popover toggle, inline restart badge, and Stop now.
    await page2.locator('.dwrap > .top .host.hbtn').click();
    const frame2 = page2.frameLocator('.sframe.on');
    const sessPop = frame2.locator('.rpop:not([hidden])').filter({ has: frame2.locator('.pmstate') });
    await sessPop.waitFor();
    const dcToggle = sessPop.locator('[data-dashboard-control-toggle="1"]');
    const dcPending = sessPop.locator('[data-dashboard-control-pending="1"]');
    const dcStop = sessPop.locator('[data-dashboard-control-stop="1"]');
    await dcToggle.waitFor();
    assert.equal(await dcToggle.getAttribute('aria-checked'), 'false');
    assert.equal(await dcPending.isHidden(), true, 'no restart banner when persisted == effective');

    // Toggle ON while running -> posts /agents/livesession/settings/dashboard-control,
    // shows inline restart badge + "Stop now" because effective stays false.
    S.posts = [];
    await dcToggle.click();
    await waitFor('toggle POST sent', () => S.posts.some(p => p.path === '/agents/livesession/settings/dashboard-control'));
    const togglePost = S.posts.find(p => p.path === '/agents/livesession/settings/dashboard-control');
    assert.deepEqual(togglePost.body, { dashboard_control_access: true });
    await dcPending.waitFor();
    assert.equal(await dcToggle.getAttribute('aria-checked'), 'true');
    assert.equal(await dcStop.isVisible(), true);

    // Click "Stop now" -> posts /agents/livesession/stop, updates state to
    // process:false & effective==persisted -> pending bar hides cleanly.
    S.posts = [];
    await dcStop.click();
    await waitFor('stop POST sent', () => S.posts.some(p => p.path === '/agents/livesession/stop'));
    await waitFor('restart banner hides after stop', () => dcPending.isHidden());
    await page2.keyboard.press('Escape');
    await waitFor('session popover closed', () => sessPop.isHidden());

    // --- Part 4: Phase 2 — daemon.tabs, cross-frame daemon.dom (password/token
    // masking + exclusion + refs), and daemon.ui (immediate nav/panel/settings +
    // 1-Click gated click_ref/fill_ref + stale_ref/ref_not_found).
    const tabsResp = await ctrlJSON('/ctrl/tabs');
    assert.ok(tabsResp.tabs.length >= 2 && tabsResp.tabs[0].tab_id === tab2, `expected page2 first in daemon.tabs: ${JSON.stringify(tabsResp)}`);

    // Mount a probe form inside the active framed session document to exercise
    // cross-frame semantic snapshot, password/secret masking, credential URL
    // redaction, exclusion, and click_ref / fill_ref over WsShell postMessage.
    await page2.evaluate(() => {
      const iframe = document.querySelector('.sframe.on');
      const fd = iframe.contentDocument;
      const box = fd.createElement('div');
      box.id = 'dc-phase2-probe';
      box.setAttribute('data-ws-probe', 'phase2');
      box.innerHTML = [
        '<button type="button" data-ws-probe-btn="1">Probe Action</button>',
        '<input type="text" aria-label="Probe Note" data-ws-probe-note="1" value="initial-note" />',
        '<input type="password" aria-label="Vault Password" data-ws-probe-pw="1" value="top-secret-pw-999" />',
        '<input type="text" name="api_token" aria-label="API Token" data-ws-probe-tok="1" value="sk-live-secret-token-1234567890" />',
        '<a href="/s/livesession?token=lanSecret999" data-ws-probe-link="1">Bearer mySecretBearer123456</a>',
        '<div data-ws-dom-exclude="1"><button type="button">Excluded Secret Button</button></div>'
      ].join('');
      const btn = box.querySelector('[data-ws-probe-btn="1"]');
      btn.dataset.clicks = '0';
      btn.addEventListener('click', () => { btn.dataset.clicks = String(Number(btn.dataset.clicks || '0') + 1); });
      const note = box.querySelector('[data-ws-probe-note="1"]');
      note.dataset.events = '0';
      note.addEventListener('input', () => { note.dataset.events = String(Number(note.dataset.events || '0') + 1); });
      fd.body.appendChild(box);
    });

    const dom1Res = await ctrlJSON('/ctrl/dom', {});
    assert.equal(dom1Res.ok, true, `daemon.dom failed: ${JSON.stringify(dom1Res)}`);
    const snap1 = dom1Res.response;
    assert.equal(snap1.tab_id, tab2);
    assert.ok(snap1.generation >= 1);
    assert.equal(snap1.frame && snap1.frame.reply_id, 'livesession');
    assert.ok(snap1.byte_size > 0 && snap1.byte_size <= 65536, `byte_size out of bounds: ${snap1.byte_size}`);
    assert.ok(snap1.node_count > 0 && snap1.node_count <= 1500, `node_count out of bounds: ${snap1.node_count}`);
    assert.ok(snap1.nodes.some(n => n.scope === 'shell'), 'expected shell scope nodes');
    assert.ok(snap1.nodes.some(n => n.scope === 'frame'), 'expected frame scope nodes');

    const snap1JSON = JSON.stringify(snap1);
    for (const leaked of ['top-secret-pw-999', 'sk-live-secret-token-1234567890', 'lanSecret999', 'mySecretBearer123456', 'Excluded Secret Button']) {
      assert.equal(snap1JSON.includes(leaked), false, `daemon.dom leaked ${leaked}`);
    }
    const probeBtnNode = snap1.nodes.find(n => n.data_ws && n.data_ws['data-ws-probe-btn'] === '1');
    const probeNoteNode = snap1.nodes.find(n => n.data_ws && n.data_ws['data-ws-probe-note'] === '1');
    const probePwNode = snap1.nodes.find(n => n.data_ws && n.data_ws['data-ws-probe-pw'] === '1');
    const probeTokNode = snap1.nodes.find(n => n.data_ws && n.data_ws['data-ws-probe-tok'] === '1');
    const probeLinkNode = snap1.nodes.find(n => n.data_ws && n.data_ws['data-ws-probe-link'] === '1');
    assert.ok(probeBtnNode && probeBtnNode.ref, 'missing probe button ref');
    assert.ok(probeNoteNode && probeNoteNode.ref, 'missing probe note ref');
    assert.ok(probePwNode && probePwNode.ref && probePwNode.masked === true && probePwNode.value === '[REDACTED]', `bad pw node: ${JSON.stringify(probePwNode)}`);
    assert.ok(probeTokNode && probeTokNode.masked === true && probeTokNode.value === '[REDACTED]', `bad tok node: ${JSON.stringify(probeTokNode)}`);
    assert.ok(probeLinkNode && probeLinkNode.href.includes('[REDACTED]') && probeLinkNode.name.includes('[REDACTED]'), `bad link node: ${JSON.stringify(probeLinkNode)}`);

    // 4a. Gated click_ref: Deny leaves button unclicked; Approve clicks framed button.
    const denyClick = ctrlJSON('/ctrl/ui', { action: 'click_ref', ref: probeBtnNode.ref, generation: snap1.generation });
    await confirm2.waitFor();
    assert.equal(await page1.locator('section[data-daemon-confirm]').isHidden(), true, 'unfocused tab must not show click_ref banner');
    assert.ok((await confirm2.textContent()).includes('Probe Action'), 'banner must show ref accessible name');
    await confirm2.locator('[data-daemon-confirm-action="deny"]').click();
    const denyClickRes = await denyClick;
    assert.equal(denyClickRes.ok, false);
    assert.equal(denyClickRes.error, 'denied');
    await waitFor('banner hidden after click_ref deny', () => page2.locator('section[data-daemon-confirm]').isHidden());
    assert.equal(await page2.evaluate(() => document.querySelector('.sframe.on').contentDocument.querySelector('[data-ws-probe-btn="1"]').dataset.clicks), '0');

    const okClick = ctrlJSON('/ctrl/ui', { action: 'click_ref', ref: probeBtnNode.ref, generation: snap1.generation });
    await confirm2.waitFor();
    await confirm2.locator('[data-daemon-confirm-action="approve"]').click();
    const okClickRes = await okClick;
    assert.equal(okClickRes.ok, true, `approved click_ref failed: ${JSON.stringify(okClickRes)}`);
    assert.equal(await page2.evaluate(() => document.querySelector('.sframe.on').contentDocument.querySelector('[data-ws-probe-btn="1"]').dataset.clicks), '1');
    await waitFor('banner hidden after click_ref approve', () => page2.locator('section[data-daemon-confirm]').isHidden());

    // 4b. Gated fill_ref on normal input and password input (password summary masked in banner).
    const okFill = ctrlJSON('/ctrl/ui', { action: 'fill_ref', ref: probeNoteNode.ref, value: 'updated-by-daemon-ui' });
    await confirm2.waitFor();
    assert.ok((await confirm2.textContent()).includes('updated-by-daemon-ui'), 'banner must show non-sensitive fill summary');
    await confirm2.locator('[data-daemon-confirm-action="approve"]').click();
    const okFillRes = await okFill;
    assert.equal(okFillRes.ok, true, `approved fill_ref failed: ${JSON.stringify(okFillRes)}`);
    const noteAfter = await page2.evaluate(() => {
      const el = document.querySelector('.sframe.on').contentDocument.querySelector('[data-ws-probe-note="1"]');
      return { value: el.value, events: el.dataset.events };
    });
    assert.equal(noteAfter.value, 'updated-by-daemon-ui');
    assert.ok(Number(noteAfter.events) >= 1, 'fill_ref must dispatch input event');
    await waitFor('banner hidden after fill_ref approve', () => page2.locator('section[data-daemon-confirm]').isHidden());

    const pwFill = ctrlJSON('/ctrl/ui', { action: 'fill_ref', ref: probePwNode.ref, value: 'new-secret-pw-777' });
    await confirm2.waitFor();
    const pwBannerText = await confirm2.textContent();
    assert.equal(pwBannerText.includes('new-secret-pw-777'), false, 'password fill_ref banner must NEVER display plaintext');
    assert.ok(pwBannerText.includes('[REDACTED]'), 'password fill_ref banner must show [REDACTED]');
    await confirm2.locator('[data-daemon-confirm-action="approve"]').click();
    const pwFillRes = await pwFill;
    assert.equal(pwFillRes.ok, true);
    assert.equal(pwFillRes.response.masked, true);
    assert.equal(await page2.evaluate(() => document.querySelector('.sframe.on').contentDocument.querySelector('[data-ws-probe-pw="1"]').value), 'new-secret-pw-777');
    await waitFor('banner hidden after pw fill_ref', () => page2.locator('section[data-daemon-confirm]').isHidden());

    // 4c. Next snapshot invalidates old generation refs -> stale_ref without banner;
    // bogus ref in current generation -> ref_not_found without banner.
    const dom2Res = await ctrlJSON('/ctrl/dom', { interactive_only: true });
    assert.equal(dom2Res.ok, true);
    assert.equal(dom2Res.response.generation, snap1.generation + 1);
    const staleRes = await ctrlJSON('/ctrl/ui', { action: 'click_ref', ref: probeBtnNode.ref });
    assert.equal(staleRes.ok, false);
    assert.equal(staleRes.error, 'stale_ref');
    assert.equal(await page2.locator('section[data-daemon-confirm]').isHidden(), true);

    const missingRefRes = await ctrlJSON('/ctrl/ui', { action: 'click_ref', ref: `g${dom2Res.response.generation}-r99999` });
    assert.equal(missingRefRes.ok, false);
    assert.equal(missingRefRes.error, 'ref_not_found');

    // 4d. Immediate semantic actions: select_panel, open_settings, focus_session (#entry=), navigate.
    const selBoard = await ctrlJSON('/ctrl/ui', { action: 'select_panel', panel: 'board' });
    assert.equal(selBoard.ok, true, `select_panel board failed: ${JSON.stringify(selBoard)}`);
    assert.equal(await page2.evaluate(() => document.querySelector('.sframe.on').contentDocument.getElementById('grid').getAttribute('data-tab')), 'board');
    const selConvo = await ctrlJSON('/ctrl/ui', { action: 'select_panel', panel: 'convo' });
    assert.equal(selConvo.ok, true);
    assert.equal(await page2.evaluate(() => document.querySelector('.sframe.on').contentDocument.getElementById('grid').getAttribute('data-tab')), 'convo');

    const openSet = await ctrlJSON('/ctrl/ui', { action: 'open_settings', scope: 'global', section: 'workflow' });
    assert.equal(openSet.ok, true, `open_settings failed: ${JSON.stringify(openSet)}`);
    await page2.locator('#workflow-settings[open] iframe').waitFor();
    await waitFor('settings iframe loaded', async () => {
      return page2.evaluate(() => {
        const f = document.querySelector('#workflow-settings iframe');
        return !!(f && f.contentDocument && f.contentDocument.querySelector('[data-settings-view]'));
      });
    });
    const domWithSet = await ctrlJSON('/ctrl/dom', { scope: 'all' });
    assert.equal(domWithSet.ok, true);
    assert.equal(domWithSet.response.modals.settings_open, true);
    assert.ok(domWithSet.response.nodes.some(n => n.scope === 'settings'), 'expected settings scope nodes when modal open');

    const focusSess = await ctrlJSON('/ctrl/ui', { action: 'focus_session', reply_id: 'reposession', entry_id: 'e42' });
    assert.equal(focusSess.ok, true, `focus_session failed: ${JSON.stringify(focusSess)}`);
    await waitFor('shell navigated to reposession#entry=e42', () => page2.evaluate(() => location.pathname === '/s/reposession' && location.hash === '#entry=e42'));
    assert.equal(await page2.locator('#workflow-settings[open]').count(), 0, 'focus_session should close open settings modal');

    // Navigation invalidated domWithSet refs -> stale_ref immediately.
    const postNavRef = domWithSet.response.nodes.find(n => n.ref);
    const postNavClick = await ctrlJSON('/ctrl/ui', { action: 'click_ref', ref: postNavRef.ref });
    assert.equal(postNavClick.ok, false);
    assert.equal(postNavClick.error, 'stale_ref');

    const navBack = await ctrlJSON('/ctrl/ui', { action: 'navigate', path: '/s/livesession' });
    assert.equal(navBack.ok, true, `navigate back failed: ${JSON.stringify(navBack)}`);
    await waitFor('shell navigated back to livesession', () => page2.evaluate(() => location.pathname === '/s/livesession'));

    // --- Part 5: Phase 3 — daemon.screenshot (Tier C dom_fallback on a standard
    // browser tab, then Tier B route_render when runtime Playwright module config
    // is enabled, verifying PNG file placement, dimensions, and no phantom tabs).
    const tabsBeforeShot = await ctrlJSON('/ctrl/tabs');
    const tab2Info = tabsBeforeShot.tabs.find(t => t.tab_id === tab2);
    assert.ok(tab2Info && tab2Info.capabilities && tab2Info.capabilities.native_capture === false, `standard browser tab must advertise native_capture=false: ${JSON.stringify(tab2Info)}`);

    // 5a. Tier C (unconfigured runtime Playwright module): honest dom_fallback, immediate (no confirm banner).
    const shotCRes = await ctrlJSON('/ctrl/screenshot', { session_id: 'livesession' });
    assert.equal(shotCRes.ok, true, `daemon.screenshot Tier C failed: ${JSON.stringify(shotCRes)}`);
    const shotC = shotCRes.response;
    assert.equal(shotC.mode, 'dom_fallback');
    assert.equal(shotC.live, false);
    assert.equal(shotC.reason, 'screenshot_unavailable');
    assert.equal(shotC.tab_id, tab2);
    assert.ok(shotC.dom && shotC.dom.tab_id === tab2 && shotC.dom.node_count > 0 && shotC.dom.byte_size <= 65536, `invalid Tier C dom: ${JSON.stringify(shotC.dom)}`);
    assert.equal(await page2.locator('section[data-daemon-confirm]').isHidden(), true, 'daemon.screenshot must not show confirm banner');

    // 5b. Tier B (runtime Playwright module enabled): route_render, live:false, PNG under per-session cache dir.
    await ctrlJSON('/ctrl/screenshot-module', { module: process.env.WS_PLAYWRIGHT_MODULE });
    try {
      const shotBRes = await ctrlJSON('/ctrl/screenshot', { session_id: 'livesession' });
      assert.equal(shotBRes.ok, true, `daemon.screenshot Tier B failed: ${JSON.stringify(shotBRes)}`);
      const shotB = shotBRes.response;
      assert.equal(shotB.mode, 'route_render');
      assert.equal(shotB.live, false);
      assert.equal(shotB.tab_id, tab2);
      assert.equal(shotB.route, '/s/livesession');
      assert.ok(shotB.path && shotB.path.includes('livesession') && shotB.path.includes('screenshots') && shotB.path.endsWith('.png'), `unexpected Tier B path: ${shotB.path}`);
      assert.ok(shotB.width > 0 && shotB.height > 0 && shotB.bytes > 0 && shotB.bytes <= 8 * 1024 * 1024, `invalid Tier B metadata: ${JSON.stringify(shotB)}`);
      assert.equal(shotB.dom, undefined, 'Tier B must not include dom_fallback snapshot');
      const pngBytes = fs.readFileSync(shotB.path);
      assert.equal(pngBytes.length, shotB.bytes, 'disk PNG size must match reported bytes');
      assert.deepEqual(Array.from(pngBytes.subarray(0, 8)), [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a], 'Tier B file must start with PNG magic');
      assert.equal(JSON.stringify(shotB).includes('iVBORw0KGgo'), false, 'Tier B response must never inline base64 PNG bytes');
      // Headless Tier B render must never register a phantom shell tab.
      const tabsAfterShot = await ctrlJSON('/ctrl/tabs');
      assert.equal(tabsAfterShot.tabs.length, tabsBeforeShot.tabs.length, `Tier B leaked a tab into daemon.tabs: ${JSON.stringify(tabsAfterShot.tabs)}`);
    } finally {
      await ctrlJSON('/ctrl/screenshot-module', { module: '' });
    }

    assert.deepEqual(errors, [], `unexpected page errors: ${errors.join('\n')}`);
    console.log('daemon-control browser acceptance passed');
  } finally {
    await browser.close();
  }
})().catch(e => {
  console.error(e.stack || e.message);
  process.exit(1);
});
