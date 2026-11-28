const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { chromium } = require(process.env.WS_PLAYWRIGHT_MODULE);

const [login, shots] = process.argv.slice(2);
const origin = new URL(login).origin;
const frameSelector = '#workflow-settings[open] > iframe';
const demoEndpoints = () => ({
  'ollama-box': {base_url:'http://ollama.example/v1', kind:'ollama'},
  'lab-vllm': {base_url:'https://vllm.example/v1', kind:'vllm'},
});
const configuredKey = (state, reason) => ({
  configured:true, masked:'...9f3c', verification:{state, reason, checked_at:'2026-11-22T01:00:00Z'},
});
const demoAccount = () => ({client_id:'fixture-client', email:'operator@example.test', signed_in:true, plan_enabled:true});

async function setup(page) {
  const state = {
    openrouter: {configured:false}, openai: null, sign_in: {state:'idle'},
    endpoints: demoEndpoints(), localKeys: {'lab-vllm':'...a3b7'},
    slots: {
      claude: [
        {id:'default', label:'Default', kind:'default', active:true, signed_in:true, config_dir:'/Users/demo/.claude'},
        {id:'work-1', label:'Work', kind:'managed', managed:true, active:false, signed_in:true, config_dir:'/Users/demo/.claude-work'},
      ],
      codex: [
        {id:'default', label:'Default', kind:'default', active:true, signed_in:true, config_dir:'/Users/demo/.codex'},
      ],
    },
    harness_auth: [
      {harness:'agy', label:'Antigravity', probed:true, auth_required:false},
    ],
    voice_keys: {},
    voiceSharedOpenRouter: false,
    keyOutcome: 'unverified', signInOutcome: 'waiting', endpointPublishFails: false, prompt: 0, polls: 0,
    requests: [], snapshotScopes: [], errors: [], consoleErrors: [], failedResponses: [],
  };
  page.setDefaultTimeout(7000);
  page.on('pageerror', error => state.errors.push(error.message));
  page.on('console', msg => { if (msg.type() === 'error') state.consoleErrors.push(msg.text()); });
  page.on('response', res => { if (res.status() >= 400) state.failedResponses.push(res.status() + ' ' + res.url()); });
  await page.route(/\/tracker\/stores(?:\?|$)/, route => route.fulfill({status: 200, json: {stores: []}}));
  await page.route(/\/api\/voice\/capabilities(?:\?|$)/, route => route.fulfill({
    status: 200,
    json: {
      order: ['native', 'openrouter'],
      backends: [
        {id: 'native', kind: 'native', available: false},
        {id: 'openrouter', kind: 'external', available: !!state.openrouter?.configured && state.voiceSharedOpenRouter, models: [{id: 'google/gemini-2.5-flash'}]},
      ],
      selected: state.openrouter?.configured && state.voiceSharedOpenRouter ? {backend: 'openrouter', model: 'google/gemini-2.5-flash'} : null,
    },
  }));
  let socket;
  await page.routeWebSocket(/\/rail\/events\?/, ws => {
    socket = ws;
    ws.onClose(() => { if (socket === ws) socket = null; });
  });
  state.reconnect = async () => {
    const previous = socket;
    await previous.close();
    await waitFor(() => socket && socket !== previous);
  };
  state.emit = async (prompt, local = true) => {
    state.prompt = prompt;
    await waitFor(() => socket);
    socket.send(JSON.stringify({event:'openai-consent', data:{prompt, local}}));
  };
  await page.route(/\/api\/config\/snapshot(?:\?|$)/, async route => {
    const scope = new URL(route.request().url()).searchParams.get('scope') || 'global';
    state.snapshotScopes.push(scope);
    const response = await route.fetch();
    const data = await response.json();
    if (scope === 'global') {
      data.entries.find(entry => entry.key === 'agent.werk_endpoints').value = JSON.stringify(state.endpoints);
      if (state.voiceSharedOpenRouter) {
        const shared = data.entries.find(entry => entry.key === 'dashboard.voice_openrouter_shared_key');
        if (shared) {
          shared.value = true;
          shared.layers = [{scope:'global', present:true, value:true}];
        }
        const order = data.entries.find(entry => entry.key === 'dashboard.voice_backend_order');
        if (order) {
          order.value = 'native openrouter';
          order.layers = [{scope:'global', present:true, value:'native openrouter'}];
        }
      }
    }
    const models = [
      {model:'router-model', provider:'openrouter'},
      {model:'chatgpt-model', provider:'openai'},
      {model:'unregistered-model', provider:'local/missing-box'},
      {model:'local-model', provider:'local/lab-vllm'},
    ];
    const rows = data.entries.filter(entry => entry.key === 'agents.tier' && entry.selectors.harness === 'werk');
    rows.forEach((row, i) => {
      row.value = row.default = {backend:'werk', effort:'medium', ...models[i % models.length]};
    });
    await route.fulfill({response, json:data});
  });
  await page.route(/\/api\/account\//, async route => {
    const action = new URL(route.request().url()).pathname.slice('/api/account/'.length);
    const body = route.request().postDataJSON();
    if (body) state.requests.push({action, body});
    const json = (value, status = 200) => route.fulfill({status, json:value});
    const empty = () => route.fulfill({status:204});
    switch (action) {
      case 'status':
        return json({
          loopback_peer:true, openrouter:state.openrouter, openai:state.openai,
          openai_ready:state.openai?.signed_in && state.openai?.plan_enabled && !state.openai?.needsReauth || false,
          sign_in:state.sign_in, consent_prompt:state.prompt,
          local_keys:Object.entries(state.localKeys).map(([name, masked]) => ({provider:'local/' + name, configured:true, masked})),
          slots:state.slots,
          harness_auth:state.harness_auth,
          voice_keys:state.voice_keys,
        });
      case 'slots/rename': {
        const list = state.slots[body.harness] || [];
        const slot = list.find(item => item.id === body.slot_id);
        if (!slot) return json({error:'account_slot_not_found'}, 404);
        if (slot.id === 'default') return json({error:'default_slot_immutable'}, 400);
        slot.label = String(body.label || '').trim();
        return json(slot);
      }
      case 'key':
        if (body.op === 'set') {
          if (state.keyOutcome === 'network-error') return route.abort();
          if (state.keyOutcome === 'rejected') return json({error:'key_rejected'}, 422);
          if (state.keyOutcome === 'invalid') return json({error:'invalid_key'}, 422);
          await new Promise(resolve => setTimeout(resolve, 250));
          state.openrouter = configuredKey('unverified', 'temporary');
        } else if (body.op === 'recheck') {
          state.openrouter = state.keyOutcome === 'recheck-rejected' ? configuredKey('rejected', 'rejected') : configuredKey('verified');
        } else {
          state.openrouter = {configured:false};
        }
        return json({status:state.openrouter, changed:true});
      case 'sign-in/start':
        state.sign_in = {state:'starting', attempt:'fixture-attempt'};
        return json(state.sign_in, 202);
      case 'sign-in/status':
        state.polls++;
        if (state.sign_in.state === 'cancelling') state.sign_in = {state:'cancelled'};
        else if (state.signInOutcome === 'succeeded') {
          state.openai = demoAccount();
          state.sign_in = {state:'succeeded'};
        } else if (state.signInOutcome === 'timed_out') {
          state.sign_in = {state:'failed', reason:'timed_out'};
        } else {
          state.sign_in = {state:'waiting', attempt:'fixture-attempt'};
        }
        return json(state.sign_in);
      case 'sign-in/cancel':
        state.sign_in = {state:'cancelling', attempt:'fixture-attempt'};
        return json(state.sign_in, 202);
      case 'sign-out':
        state.openai = null;
        state.sign_in = {state:'idle'};
        return empty();
      case 'consent':
        if (state.openai) state.openai.background_consent = body.allowed;
        if (body.prompt) await state.emit(0);
        return empty();
      case 'consent/dismiss':
        await state.emit(0);
        return empty();
      case 'endpoints':
        if (state.endpointPublishFails) {
          delete state.localKeys[body.name];
          return json({error:'endpoint_key_retired'}, 500);
        }
        if (body.op === 'remove') {
          delete state.endpoints[body.name];
          delete state.localKeys[body.name];
        } else {
          const normalized = body.base_url.replace(/\/+$/, '');
          if (state.endpoints[body.name]?.base_url !== normalized) delete state.localKeys[body.name];
          state.endpoints[body.name] = {base_url:normalized, kind:body.kind};
          if (body.key) state.localKeys[body.name] = '...c9d0';
        }
        return json({endpoints:Object.entries(state.endpoints).map(([name, item]) => ({name, ...item})), changed:true});
      default:
        throw new Error('Unmocked Account action');
    }
  });
  return state;
}

async function waitFor(condition) {
  const deadline = Date.now() + 7000;
  while (!await condition()) {
    if (Date.now() > deadline) throw new Error('Timed out waiting for Account fixture state');
    await new Promise(resolve => setTimeout(resolve, 30));
  }
}

const row = (frame, name) => frame.locator('[data-account-row=' + JSON.stringify(name) + ']');
const section = (frame, name) => frame.locator('[data-settings-section=' + JSON.stringify(name) + ']');

async function hold(page, locator) {
  await locator.focus();
  await page.keyboard.down('Space');
  await page.waitForTimeout(1550);
  await page.keyboard.up('Space');
}

async function exercise(page) {
  const state = await setup(page);
  await page.goto(login);
  await page.waitForURL(url => url.pathname === '/' && !url.searchParams.has('login'));
  const banner = page.locator('[data-openai-consent]');
  const frame = page.frameLocator(frameSelector);
  await state.emit(1);
  const deepLink = banner.getByRole('link', {name:'View OpenAI in Account'});
  await deepLink.click();
  const oa = row(frame, 'openai');
  await oa.waitFor();
  assert.equal(await oa.evaluate(node => node === node.ownerDocument.activeElement), true);
  await frame.locator('[data-settings-close]').click();
  await banner.getByRole('button', {name:/Close until/}).click();
  await banner.waitFor({state:'hidden'});
  assert.deepEqual(state.requests.at(-1), {action:'consent/dismiss', body:{prompt:1}});
  await state.reconnect();
  const successorPrompt = 4503599627370497;
  await state.emit(successorPrompt);
  await banner.getByRole('button', {name:'Don’t allow'}).click();
  assert.deepEqual(state.requests.at(-1), {action:'consent', body:{prompt:successorPrompt, allowed:false}});
  await state.emit(3);
  await banner.getByRole('button', {name:'Allow', exact:true}).click();
  assert.deepEqual(state.requests.at(-1), {action:'consent', body:{prompt:3, allowed:true}});

  await state.emit(4, false);
  await banner.getByText('Open the dashboard on the daemon’s machine to respond.').waitFor();
  assert.equal(await banner.locator('a, button').count(), 0);
  await state.emit(0, false);
  await banner.waitFor({state:'hidden'});
  await page.goto(origin + '/settings?scope=builtin&section=account');
  const or = row(frame, 'openrouter');
  await or.getByRole('button', {name:'Add key'}).waitFor();
  assert.equal(await frame.locator('[data-settings-target-display] > span').textContent(), 'This whole machine');
  assert.equal(await frame.locator('[data-settings-precedence]').isVisible(), false);
  assert.equal(await frame.locator('.filter-bar').isVisible(), false);
  assert.equal(await frame.locator('.builtin-banner').count(), 0);
  assert.equal(await section(frame, 'account').locator('.cnt').textContent(), '2');

  await or.getByRole('button', {name:/Choose OpenRouter models/}).click();
  assert.equal(await frame.locator('[data-settings-scope=builtin]').getAttribute('aria-current'), 'page');
  assert.equal(await frame.locator('[data-bucket-key="agents.tier"][data-settings-bucket=werk]').getAttribute('aria-selected'), 'true');
  assert.equal(await frame.locator('tr.account-focus').count(), 1);
  assert.equal(await frame.locator('.tier-section').getByText('agent.werk_endpoints').count(), 0);
  const missing = frame.locator('[data-account-tier-provider="local/missing-box"]');
  await missing.getByText(/missing-box is missing/).waitFor();
  await missing.getByRole('button', {name:'Manage in Account'}).click();
  assert.equal(await row(frame, 'local').evaluate(node => node === node.ownerDocument.activeElement), true);

  await or.getByRole('button', {name:'Add key'}).click();
  const keyInput = or.locator('input[type=password]');
  assert.equal(await keyInput.inputValue(), '');
  assert.equal(await keyInput.getAttribute('autocomplete'), 'off');
  await keyInput.fill('fixture-input');
  await or.getByText(/usually start with sk-or/).waitFor();
  const save = or.getByRole('button', {name:'Save', exact:true});
  for (const [outcome, message] of [
    ['network-error', 'Your entry is still'],
    ['invalid', 'Use an ASCII key'],
    ['rejected', 'OpenRouter rejected this key'],
  ]) {
    state.keyOutcome = outcome;
    await save.click();
    await or.getByText(new RegExp(message)).waitFor();
    assert.equal(await keyInput.inputValue(), 'fixture-input');
  }
  state.keyOutcome = 'unverified';
  await save.click();
  await or.getByRole('button', {name:'Checking…'}).waitFor();
  assert.equal(await keyInput.isDisabled(), true);
  await or.getByText('OpenRouter was temporarily unable', {exact:false}).waitFor();
  assert.equal(await keyInput.count(), 0);
  assert.equal(await or.getByText('...9f3c').count(), 1);
  assert.equal(await or.getByText('fixture-input').count(), 0);
  assert.equal(await or.getByRole('button', {name:'Check again'}).count(), 1);
  state.keyOutcome = 'recheck-rejected';
  await or.getByRole('button', {name:'Check again'}).click();
  await or.getByText('Key rejected', {exact:true}).waitFor();
  state.keyOutcome = 'verified';
  await or.getByRole('button', {name:'Check again'}).click();
  await or.getByText('OpenRouter verified this key.').waitFor();
  await or.getByRole('button', {name:'Replace', exact:true}).click();
  assert.equal(await keyInput.inputValue(), '');
  await or.getByRole('button', {name:'Cancel'}).click();
  const removeKey = or.getByRole('button', {name:'Remove OpenRouter key'});
  await removeKey.click();
  assert.equal(state.requests.at(-1).body.op, 'recheck');
  await removeKey.focus();
  await page.keyboard.down('Space');
  await page.keyboard.press('Escape');
  await page.keyboard.up('Space');
  assert.equal(await or.isVisible(), true);
  await hold(page, removeKey);
  await or.getByText('Not set', {exact:true}).waitFor();

  const signIn = oa.getByRole('button', {name:'Sign in with ChatGPT'});
  await signIn.click();
  await oa.getByText('Signing in…', {exact:true}).waitFor();
  await waitFor(() => state.polls > 0);
  await oa.getByRole('button', {name:'Cancel'}).click();
  await oa.getByText('Sign-in cancelled.').waitFor();
  assert.deepEqual(state.requests.at(-1), {action:'sign-in/cancel', body:{attempt:'fixture-attempt'}});
  const done = state.polls;
  await page.waitForTimeout(1050);
  assert.equal(state.polls, done);
  state.signInOutcome = 'timed_out';
  await signIn.click();
  await oa.getByText('Sign-in timed out. Try again.').waitFor();

  state.openai = {...demoAccount(), plan_enabled:false};
  state.sign_in = {state:'idle'};
  await state.emit(5);
  const retryPlan = oa.getByRole('button', {name:'Sign in again to allow plan use'});
  await retryPlan.waitFor();
  state.signInOutcome = 'succeeded';
  await retryPlan.click();
  assert.deepEqual(state.requests.at(-1), {action:'sign-in/start', body:{enable_plan:true, client_id:'fixture-client'}});
  await oa.getByText('Signed in', {exact:true}).waitFor();
  await oa.getByText('ChatGPT plan', {exact:true}).waitFor();
  assert.equal(await oa.getByText('operator@example.test', {exact:true}).count(), 1);
  const toggle = row(frame, 'openai-consent').getByRole('switch');
  await toggle.click();
  await waitFor(async () => await toggle.getAttribute('aria-checked') === 'true');
  assert.deepEqual(state.requests.at(-1), {action:'consent', body:{client_id:'fixture-client', allowed:true}});
  await toggle.click();
  await waitFor(async () => await toggle.getAttribute('aria-checked') === 'false');
  state.openai.plan_paused = true;
  await state.emit(6);
  assert.equal(await oa.getByRole('link', {name:'Review ChatGPT usage'}).getAttribute('href'), 'https://chatgpt.com/settings/usage');
  state.openai = {...state.openai, signed_in:false, plan_enabled:false, background_consent:true};
  await state.emit(7);
  await oa.getByRole('button', {name:'Sign in again', exact:true}).waitFor();
  await toggle.click();
  await waitFor(async () => await toggle.getAttribute('aria-checked') === 'false');
  await hold(page, oa.getByRole('button', {name:/Sign out of OpenAI/}));
  await oa.getByText('Signed out', {exact:true}).waitFor();
  assert.equal(await toggle.isVisible(), true);
  assert.equal(await toggle.isDisabled(), true);
  assert.equal(await toggle.getAttribute('aria-checked'), 'false');

  const endpoints = row(frame, 'local');
  assert(state.snapshotScopes.includes('global') && state.snapshotScopes.includes('builtin'));
  const lab = frame.locator('[data-endpoint-name=lab-vllm]');
  await lab.getByText('...a3b7').waitFor();
  await endpoints.getByText('View stored format (JSON)').click();
  const projection = () => endpoints.locator('pre').textContent().then(JSON.parse);
  assert.deepEqual(await projection(), state.endpoints);
  await lab.getByRole('button', {name:'Edit lab-vllm'}).click();
  const endpointURL = frame.getByLabel('Base URL', {exact:true});
  const endpointInput = frame.getByLabel('API key (optional)', {exact:true});
  assert.equal(await endpointInput.inputValue(), '');
  await endpointURL.fill('https://user@vllm.example/v1?query=1');
  assert.equal(await endpoints.getByRole('button', {name:'Save', exact:true}).isDisabled(), true);
  await endpointURL.fill('https://vllm2.example/v1/');
  await endpoints.getByText('Saving a new URL deletes the key stored for the old URL.').waitFor();
  state.endpointPublishFails = true;
  await endpoints.getByRole('button', {name:'Save', exact:true}).click();
  await endpoints.getByText('The endpoint configuration did not change. The previous key was removed.').waitFor();
  await lab.getByText('No key').waitFor();
  assert.equal((await projection())['lab-vllm'].base_url, 'https://vllm.example/v1');
  state.endpointPublishFails = false;
  await endpoints.getByRole('button', {name:'Save', exact:true}).click();
  await endpointURL.waitFor({state:'detached'});
  await waitFor(async () => (await projection())['lab-vllm'].base_url === 'https://vllm2.example/v1');

  await endpoints.getByRole('button', {name:'+ Endpoint'}).click();
  const endpointName = frame.getByLabel('Name', {exact:true});
  await endpointName.fill('ollama-box');
  await endpoints.getByText('This endpoint name is already registered.').waitFor();
  await endpointName.fill('new-box');
  await endpointURL.fill('http://new-box.example/v1');
  await endpointInput.fill('fixture-endpoint-input');
  await endpoints.getByRole('button', {name:'Add', exact:true}).click();
  const added = frame.locator('[data-endpoint-name=new-box]');
  await added.getByText('...c9d0').waitFor();
  assert.deepEqual(await projection(), state.endpoints);
  assert.equal((await endpoints.locator('pre').textContent()).includes('fixture-endpoint-input'), false);
  await hold(page, added.getByRole('button', {name:'Remove new-box and its saved API key'}));
  await added.waitFor({state:'detached'});
  assert.equal(Object.hasOwn(await projection(), 'new-box'), false);

  // Verify inline slot rename on non-default Claude slot
  const claude = row(frame, 'slots-claude');
  await claude.locator('[data-slot-rename="work-1"]').click();
  const rnInput = claude.locator('[data-slot-rename-input]');
  await rnInput.fill('Work Renamed');
  await rnInput.press('Enter');
  await claude.getByText('Work Renamed', {exact:true}).waitFor();
  assert.deepEqual(state.requests.at(-1), {action:'slots/rename', body:{harness:'claude', slot_id:'work-1', label:'Work Renamed'}});

  // Verify legacy /settings?section=account&provider=voice deep link normalizes to Voice section
  await page.goto(origin + '/settings?section=account&provider=voice');
  await section(frame, 'voice').waitFor();
  assert.equal(await section(frame, 'voice').getAttribute('aria-current'), 'true');
  await frame.locator('[data-voice-section]').waitFor();

  // Regression: the precedence strip and filter bar live in the right column (.smain),
  // so toggling scoped vs non-scoped sections never shifts the left .snav vertically.
  for (const viewport of [{width:1680, height:1000}, {width:1280, height:900}, {width:1120, height:800}, {width:390, height:844}]) {
    await page.setViewportSize(viewport);
    await page.goto(origin + '/settings?section=workflow');
    await section(frame, 'workflow').waitFor();
    const snavTop = () => frame.locator('.snav').evaluate(node => node.getBoundingClientRect().top);
    const baseline = await snavTop();
    for (const name of ['workflow', 'account', 'voice', 'workflow']) {
      await section(frame, name).click();
      await section(frame, name).evaluate(node => node.getAttribute('aria-current') === 'true' || Promise.reject(new Error('section not current')));
      const scoped = name !== 'account' && name !== 'voice';
      assert.equal(await frame.locator('[data-settings-precedence]').isVisible(), scoped, `${viewport.width}: ${name} precedence strip visibility`);
      assert.equal(await frame.locator('.snav').evaluate(node => node.getBoundingClientRect().top), baseline, `${viewport.width}: .snav top moved on ${name}`);
      assert.equal(await frame.locator('[data-settings-view]').evaluate(node => node.scrollWidth <= node.clientWidth + 1), true, `${viewport.width}: horizontal overflow on ${name}`);
    }
    assert.equal(await snavTop(), baseline, `${viewport.width}: .snav top moved after round trip`);
  }
  await page.setViewportSize({width:1280, height:900});

  assert.deepEqual(state.errors, []);
  assert.deepEqual(state.consoleErrors.filter(msg => !msg.startsWith('Failed to load resource:')), []);
  console.log('Account lifecycle, slot rename, Voice split, scope, model remedies, masked key operations, and local/LAN consent stream passed.');
}

async function capture(browser, authenticated) {
  await fs.mkdir(shots, {recursive:true});
  const auth = await authenticated.context().storageState();
  const reference = await browser.newPage();
  const voiceRef = await browser.newPage();
  const actual = await browser.newPage({locale:'ko-KR', storageState:auth});
  const state = await setup(actual);
  state.openrouter = configuredKey('verified');
  state.openai = {...demoAccount(), background_consent:true};
  state.voiceSharedOpenRouter = true;
  const accountMock = path.resolve('../../../ai-docs/assets/mockups/261128/account-panel-rework/index.html');
  const voiceMock = path.resolve('../../../ai-docs/assets/mockups/261128/voice-provider-ui/index.html');
  for (const width of [1280, 390]) {
    for (const theme of ['dark', 'light']) {
      const viewport = {width, height:width === 390 ? 844 : 900};
      await actual.setViewportSize(viewport);
      await reference.setViewportSize(viewport);
      await voiceRef.setViewportSize(viewport);
      await actual.emulateMedia({colorScheme:theme});
      const name = width + '-' + theme;

      // 1. Live Settings -> Account vs 261128 approved Account mockup
      await actual.goto(origin + '/settings?section=account');
      const frame = actual.frameLocator(frameSelector);
      const stage = frame.locator('[data-settings-view]');
      await row(frame, 'local').locator('[data-endpoint-name=lab-vllm]').waitFor();
      assert.equal(await section(frame, 'account').getByText('계정', {exact:true}).count(), 1);
      assert.equal(await section(frame, 'voice').getByText('음성', {exact:true}).count(), 1);
      await row(frame, 'openai').getByText('로그인됨', {exact:true}).waitFor();
      await frame.locator('[data-account-voice-chip]').waitFor();
      assert(await stage.evaluate(node => node.scrollWidth <= node.clientWidth + 1), 'Account stage has horizontal overflow at ' + name);
      await stage.screenshot({path:path.join(shots, 'account-' + name + '.png'), animations:'disabled'});
      if (width === 1280) {
        await frame.locator('[data-account-group="runtime"]').screenshot({path:path.join(shots, 'account-runtime-' + name + '.png'), animations:'disabled'});
      }

      await reference.goto(pathToFileURL(accountMock).href);
      await reference.locator('.grp').first().waitFor();
      await reference.locator('#simTheme button[data-theme="' + theme + '"]').click();
      await reference.locator('.shell').screenshot({path:path.join(shots, 'mockup-account-' + name + '.png'), animations:'disabled'});
      if (width === 1280) {
        await reference.locator('.grp').nth(1).screenshot({path:path.join(shots, 'mockup-account-runtime-' + name + '.png'), animations:'disabled'});
      }

      // 2. Live Settings -> Voice vs 261125 Phase 4 Voice Providers mockup
      await section(frame, 'voice').click();
      await frame.locator('[data-voice-section]').waitFor();
      await frame.locator('.vprow-chip-link').waitFor();
      assert(await stage.evaluate(node => node.scrollWidth <= node.clientWidth + 1), 'Voice stage has horizontal overflow at ' + name);
      await stage.screenshot({path:path.join(shots, 'voice-' + name + '.png'), animations:'disabled'});

      await voiceRef.goto(pathToFileURL(voiceMock).href);
      await voiceRef.locator('[data-sim="theme"][data-val="' + theme + '"]').click();
      await voiceRef.locator('[data-sim="view"][data-val="settings"]').click();
      await voiceRef.locator('#voiceSection').screenshot({path:path.join(shots, 'mockup-voice-' + name + '.png'), animations:'disabled'});
    }
  }
  await actual.goto(origin + '/');
  await state.emit(2, false);
  const banner = actual.locator('[data-openai-consent]');
  await banner.getByText('응답하려면 데몬이 실행되는 컴퓨터에서 대시보드를 여세요.').waitFor();
  assert.equal(await banner.locator('button, a').count(), 0);
  await banner.screenshot({path:path.join(shots, 'lan-consent-390-light.png'), animations:'disabled'});
  assert.deepEqual(state.errors, []);
  if (state.consoleErrors.length) console.error('Failed responses:', state.failedResponses);
  assert.deepEqual(state.consoleErrors, []);
  await Promise.all([reference.close(), voiceRef.close(), actual.close()]);
  console.log('Captured Account, Voice, and approved mockups at 1280px and 390px, dark and light, plus LAN consent.');
}

(async () => {
  const browser = await chromium.launch({headless:true});
  try {
    const page = await browser.newPage({viewport:{width:1280, height:900}, locale:'en-US'});
    await exercise(page);
    if (shots) await capture(browser, page);
  } finally {
    await browser.close();
  }
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
