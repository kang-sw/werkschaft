// Run locally: node agents-plugin-tool/internal/wsdashboard/testdata/lead-popover.test.cjs
const assert = require('node:assert/strict');
const { readAssetSource } = require('./asset-source.cjs');
const vm = require('node:vm');
const source = readAssetSource('wsdashboard', 'dashboard.js');
const tierPickerSrc = readAssetSource('wsdashboard', 'dashboard-tier-picker.js');
// The lead choices and permission helpers live in dashboard-leads.js ahead of
// the session menu; the spawn modal maker is its own dashboard-new-session.js.
const leadsHead = source.match(/  var LEAD_MODES = [\s\S]*?\n(?=  \/\/ The session menu of the viewed hosted session)/);
const modalSource = readAssetSource('wsdashboard', 'dashboard-new-session.js');
const modalMaker = modalSource.match(/  var LEAD_DANGER_MODE = [\s\S]*?\n  function rpLeadMake\(\) \{[\s\S]*?\n  \}\n/);
const maker = leadsHead && modalMaker && [leadsHead[0] + modalMaker[0]];
assert(maker, 'production lead choices, rows and popover maker exist');
const rail = readAssetSource('wsdashboard', 'rail.js');
const legacy = [
  { harness: 'claude', available: true, models: [{ id: 'opus' }], efforts: ['high'] },
  { harness: 'pi', available: true, models: [{ id: 'pi-model' }], efforts: ['off'] }
];

function fixture(harness, choices = legacy, delayed = false, translate = k => k) {
  const nodes = [], events = [], requests = [], storage = {};
  function mk(tag, cls, text) {
    const n = { tag, cls: cls || '', textContent: text || '', value: '', hidden: false, disabled: false,
      attrs: {}, children: [], listeners: {}, style: {},
      appendChild(child) { child.parentNode = this; this.children.push(child); return child; },
      setAttribute(k, v) { this.attrs[k] = String(v); },
      getAttribute(k) { return this.attrs[k] || ''; },
      removeAttribute(k) { delete this.attrs[k]; },
      toggleAttribute(k, on) { if (on) this.attrs[k] = ''; else delete this.attrs[k]; },
      addEventListener(k, fn) { this.listeners[k] = fn; },
      focus() { events.push('focus'); } };
    Object.defineProperty(n, 'textContent', {
      get() { return this.text || ''; },
      set(value) { this.text = value; this.children = []; }
    });
    n.textContent = text || '';
    const classes = new Set((cls || '').split(' ').filter(Boolean));
    n.classList = {
      toggle(k, on) { if (on) classes.add(k); else classes.delete(k); },
      add(k) { classes.add(k); },
      remove(k) { classes.delete(k); },
      contains(k) { return classes.has(k); }
    };
    nodes.push(n);
    return n;
  }
  let active, answerChoices, seq = 0;
  const ctx = vm.createContext({
    mk, wsT: translate, rpPop: () => mk('div'), rpBtn: (cls, text) => mk('button', cls, text),
    supChoices: delayed ? null : { harnesses: choices },
    rpId: () => 'row-' + ++seq,
    tkFetch: (route, options, done) => { assert.equal(route, '/support/choices'); answerChoices = () => done(200, { harnesses: choices }); },
    window: {},
    sessionStorage: { setItem(key, value) { storage[key] = value; events.push('rename-intent'); } },
    railOpen(open) { assert.equal(open, false); events.push('rail-close'); },
    railPopPlace() {}, notifAsk() {}, gIcon: () => mk('svg'),
    rpLive: (p, gen) => active === p && p.gen === gen,
    railPopClose(refocus) { assert.equal(refocus, false); active.el.hidden = true; active = null; events.push('close'); },
    saveDraft: () => events.push('draft'), go: href => events.push(['navigate', href]),
    toast: text => events.push(['toast', text]), leadErr: (st, text) => text || 'HTTP ' + st,
    railPost: (route, body, done, timeout) => requests.push({ route, body, done, timeout })
  });
  vm.runInContext(rail, ctx);
  ctx.WsRail = ctx.window.WsRail;
  vm.runInContext(source.match(/\n  function supHarness\(name\) \{[\s\S]*?\n  \}\n/)[0] + tierPickerSrc + maker[0], ctx);
  const p = active = ctx.rpLeadMake();
  p.open('/repo', 'repo', mk('button'));
  p.el.hidden = false;
  const harnessGroup = nodes.find(n => n.listeners.click && n.attrs['aria-label'] === translate('js.sup.harness'));
  function pick(key) {
    const button = harnessGroup.children.find(n => n.attrs['data-harness'] === key);
    assert(button, 'harness button exists: ' + key);
    harnessGroup.listeners.click({ target: { closest: () => button } });
  }
  if (harness) pick(harness);
  const model = nodes.find(n => n.attrs['data-model-input']);
  const modelList = model.parentNode.parentNode.children[1];
  const effort = nodes.find(n => n.attrs['data-effort-knob']);
  const modeCaps = nodes.find(n => n.cls.includes('ns-native-cap'));
  const modeNote = nodes.find(n => n.cls === 'ns-adv-note spdesc' && n.parentNode && n.parentNode.cls === 'ns-foot');
  const footOpts = nodes.find(n => n.cls === 'ns-foot-opts' && n.parentNode && n.parentNode.cls === 'ns-foot');
  const start = nodes.find(n => n.cls.includes('ns-cta') && n.tag === 'button');
  const startLbl = nodes.find(n => n.cls === 'ns-cta-lbl');
  const isoSw = nodes.find(n => n.attrs['data-iso-switch']);
  const wbSw = nodes.find(n => n.attrs['data-wb-only-switch']);
  const convSw = nodes.find(n => n.attrs['data-conv-history-switch']);
  return { p, nodes, events, requests, storage, pick, harnessGroup,
    terms: nodes.find(n => n.cls === 'spnote spterms'),
    choicesReady() { answerChoices(); },
    model, modelList, effort, modeCaps, modeNote, footOpts, isoSw, wbSw, convSw,
    modelOptions() { return modelList.children.filter(li => !li.cls.includes('none')).map(li => li.attrs['data-val']); },
    pickModel(val) {
      model.value = val;
      model.listeners.input();
    },
    pickEffort(val) {
      const dots = effort.children[0].children.filter(d => d.cls.includes('kdot'));
      const idx = dots.findIndex(d => d.attrs['data-effort'] === val);
      assert(idx >= 0, 'effort dot exists: ' + val);
      effort.listeners.keydown({ key: 'Home', preventDefault() {} });
      for (let i = 0; i < idx; i++) effort.listeners.keydown({ key: 'ArrowRight', preventDefault() {} });
    },
    modeNames() { return modeCaps.hidden ? [] : modeCaps.children.map(b => b.attrs['data-mode']); },
    checkedMode() {
      if (modeCaps.hidden) return '';
      const b = modeCaps.children.find(c => c.attrs['aria-checked'] === 'true');
      return b ? b.attrs['data-mode'] : '';
    },
    pickMode(m) {
      const b = modeCaps.children.find(c => c.attrs['data-mode'] === m);
      assert(b, 'mode capsule exists: ' + m);
      b.listeners.click();
    },
    newRows(key, values) { const rows = ctx.leadRows(mk('div'), () => {}); rows.set(key, values); return rows; },
    start, status: startLbl,
    error: nodes.find(n => n.cls === 'spnote err'),
    dismiss() { active = null; p.el.hidden = true; } };
}

for (const harness of ['claude']) {
  const f = fixture(harness);
  f.start.listeners.click();
  assert.equal(f.p.busy, true);
  assert.equal(f.start.disabled, true);
  assert.equal(f.status.textContent, 'js.lead.starting');
  f.start.listeners.click();
  assert.equal(f.requests.length, 1, 'busy prevents duplicate creation');
  assert.equal(f.requests[0].route, '/agents/leads');
  assert.equal(f.requests[0].body.harness, harness);
  assert.equal(f.requests[0].timeout, 20000, 'a spawn that never answers ends instead of staying busy');
  // A completed HTTP response is sufficient: no SSE/attach event or timer.
  f.requests[0].done(201, { id: 'new/session' }, '');
  assert.equal(f.p.el.hidden, true);
  assert.equal(f.p.busy, false);
  assert.equal(f.start.disabled, false);
  assert.equal(f.status.textContent, 'js.lead.start');
  assert.deepEqual(f.events, ['close', 'rail-close', 'draft', ['navigate', '/s/new%2Fsession']]);
  assert.deepEqual(f.storage, {}, 'successful creation does not schedule post-navigation rename');

  for (const status of [400, 500, 0]) {
    const e = fixture(harness);
    e.start.listeners.click();
    e.requests[0].done(status, {}, 'launch failed');
    assert.equal(e.p.el.hidden, false);
    assert.equal(e.p.busy, false);
    assert.equal(e.start.disabled, false);
    assert.equal(e.status.textContent, 'js.lead.start');
    assert.equal(e.error.textContent, 'launch failed');
    assert.deepEqual(e.events, []);
    assert.deepEqual(e.storage, {}, 'failed creation must not schedule rename');
    e.start.listeners.click();
    assert.equal(e.requests.length, 2, 'failure permits retry');
    assert.equal(e.error.textContent, '');
  }

  const dismissed = fixture(harness);
  dismissed.start.listeners.click();
  dismissed.dismiss();
  dismissed.requests[0].done(201, { id: 'created' }, '');
  assert.deepEqual(dismissed.events, ['rail-close', 'draft', ['navigate', '/s/created']], 'late success still opens the created session without closing another popover');
  assert.deepEqual(dismissed.storage, {}, 'late success does not schedule post-navigation rename');
}
for (const provider of ['openrouter', 'local/desk']) {
  const remedy = 'ChatGPT plan usage is disabled; enable it with "credential sign-in openai --enable-plan"';
  const f = fixture('werk', [{ harness: 'werk', lead: true, available: true,
    model_providers: [provider], model_notice: remedy,
    models: [{ id: 'vendor/model', provider }, { id: 'unavailable', provider: 'openai' }] }]);
  const note = f.nodes.find(n => n.id === f.model.attrs['aria-describedby']);
  assert.equal(note.hidden, false);
  assert.equal(note.textContent, remedy);
  assert.equal(f.model.disabled, false);
  assert.deepEqual(f.modelOptions(),
    [provider === 'openrouter' ? 'vendor/model' : provider + ' vendor/model']);
}
// Lead-marked descriptors replace, rather than extend, the fixed buttons.
const custom = { harness: 'custom', profile: '', lead: true, available: true,
  models: [{ id: 'custom-model' }], efforts: ['deep'], permission_modes: ['review', 'auto', 'plan'] };
const unavailable = { harness: 'missing', profile: '', lead: true, available: false,
  error: 'not installed', permission_modes: ['review'] };
const noPermission = { harness: 'plain', profile: '', lead: true, available: true };
const harnessNames = f => f.harnessGroup.children.map(n => n.attrs['data-harness']);
{
  const f = fixture(null, [...legacy, unavailable, custom, noPermission]);
  assert.deepEqual(harnessNames(f), ['missing', 'custom', 'plain']);
  assert.equal(f.p.harness, 'custom', 'first available admitted lead is selected');
  assert.equal(f.harnessGroup.children[0].disabled, true);
  f.pick('missing');
  assert.equal(f.p.harness, 'custom', 'unavailable lead cannot be selected');
  assert.deepEqual(f.modeNames(), ['review', 'auto', 'plan']);
  assert.equal(f.checkedMode(), 'review', 'custom default is its first choice, not Claude auto');
  assert.equal(f.modeCaps.children.every(b => !b.disabled), true);
  f.pickModel('custom-model');
  f.pickEffort('deep');
  f.pickMode('plan');
  assert.equal(f.modeNote.hidden, true, 'custom permission names without descriptions hide the footer note instead of echoing raw mode');
  assert.equal(f.modeNote.textContent, '');
  assert.equal(f.modeNote.classList.contains('warn'), false);
  const footChildren = f.modeNote.parentNode.children;
  assert(footChildren.indexOf(f.modeNote) < footChildren.indexOf(f.footOpts), 'pNativeNote precedes .ns-foot-opts in .ns-foot');
  f.isoSw.listeners.click();
  f.start.listeners.click();
  assert.deepEqual(JSON.parse(JSON.stringify(f.requests[0].body)), {
    root: '/repo', harness: 'custom', plugin_isolation: 'isolated',
    model: 'custom-model', effort: 'deep', permission_mode: 'plan',
    conv_history_access: false
  });
  const rows = f.newRows('custom', { mode: 'plan' });
  rows.disable(true);
  assert.equal(rows.mode.disabled, true, 'busy session settings disable custom permission modes too');
  rows.disable(false);
  assert.equal(rows.mode.disabled, false);
}
{
  const f = fixture(null, [custom, noPermission]);
  f.pick('plain');
  assert.equal(f.modeCaps.hidden, true);
  assert.deepEqual(f.modeNames(), []);
  assert.equal(f.model.disabled, true, 'absent model choices do not enable a control');
  assert.equal(f.effort.attrs['aria-disabled'], 'true', 'absent effort choices do not enable a control');
  // Even stale values on unsupported controls never enter the request.
  f.p.mode = 'plan'; f.p.model = ''; f.p.effort = '';
  f.convSw.listeners.click();
  f.start.listeners.click();
  assert.deepEqual(JSON.parse(JSON.stringify(f.requests[0].body)), {
    root: '/repo', harness: 'plain', plugin_isolation: 'default',
    conv_history_access: true
  });
  const rows = f.newRows('plain', {});
  rows.disable(true); rows.disable(false);
  assert.equal(rows.mode.disabled, true, 'a redraw does not enable absent permissions');
}
{
  const f = fixture(null, [{ ...noPermission, harness: 'constructor' }]);
  assert.equal(f.p.harness, 'constructor', 'backend names do not inherit object properties');
  assert.equal(f.effort.attrs['aria-disabled'], 'true');
}
{
  const f = fixture(null, [unavailable]);
  assert.equal(f.start.disabled, true);
  assert.equal(f.modeCaps.children.every(b => b.disabled), true);
  f.start.listeners.click();
  assert.equal(f.requests.length, 0, 'no launch when every admitted lead is unavailable');
  f.p.focus();
  assert.deepEqual(f.events, ['focus']);
}
{
  const f = fixture(null, [{ harness: 'claude', lead: true, available: true }]);
  assert.equal(f.modeCaps.hidden, true, 'descriptor permission absence also applies to Claude');
}
{
  const modes = ['default', 'auto', 'plan'];
  const f = fixture(null, legacy.map(h => ({ ...h, lead: true,
    ...(h.harness === 'claude' ? { permission_modes: modes } : {}) })));
  assert.deepEqual(harnessNames(f), ['claude']);
  assert.equal(f.p.harness, 'claude');
  assert.deepEqual(f.modeNames(), modes, 'Claude also renders only descriptor modes');
  assert.equal(f.checkedMode(), 'auto', 'Claude retains its existing initial mode');
  assert(!harnessNames(f).includes('pi'), 'a legacy lead descriptor does not offer Pi');
}
{
  const f = fixture(null, [...legacy, { ...custom, lead: false }]);
  assert.deepEqual(harnessNames(f), ['claude'], 'an older daemon still offers Claude');
  assert.equal(f.p.harness, 'claude');
  assert.deepEqual(f.modeNames(), ['auto', 'default', 'acceptEdits', 'plan', 'dontAsk', 'bypassPermissions']);
  assert.equal(f.checkedMode(), 'auto');
  assert.equal(f.modeNote.hidden, false);
  assert.equal(f.modeNote.textContent, 'js.lead.mode.auto');
  assert.equal(f.modeNote.classList.contains('warn'), false);
  f.pickMode('bypassPermissions');
  assert.equal(f.modeNote.hidden, false);
  assert.equal(f.modeNote.textContent, 'js.lead.mode.bypass');
  assert.equal(f.modeNote.classList.contains('warn'), true);
  f.pickMode('auto');
  f.start.listeners.click();
  assert.equal(f.requests[0].body.harness, 'claude');
  assert.equal(f.requests[0].body.plugin_isolation, 'default');
}
{
  const f = fixture(null, [{ ...legacy[0], available: false }, legacy[1]]);
  assert(f.start.disabled, 'a retired Pi descriptor cannot replace unavailable Claude');
}
{
  const f = fixture(null, [custom], true);
  assert.deepEqual(harnessNames(f), ['claude']);
  f.choicesReady();
  assert.deepEqual(harnessNames(f), ['custom'], 'async choices replace fallback buttons');
  assert.equal(f.p.harness, 'custom');
  assert.deepEqual(f.modeNames(), ['review', 'auto', 'plan']);
  assert.equal(f.checkedMode(), 'review', 'a newly selected backend does not inherit the fallback mode');
}
// railPost itself: a request that never answers ends once, at the timeout,
// with the timeout text, and aborts the fetch; a late answer is ignored.
{
  const post = source.match(/\n  function railPost\(route, body, done, timeout\) \{[\s\S]*?\n  \}\n/);
  assert(post, 'production railPost exists');
  const timers = [], aborted = [], calls = [];
  let answer;
  const ctx = vm.createContext({
    wsT: k => k,
    setTimeout: (fn, ms) => { timers.push({ fn, ms }); return timers.length; },
    clearTimeout: id => { timers[id - 1].cleared = true; },
    window: {
      fetch: () => new Promise(resolve => { answer = resolve; }),
      AbortController: class { constructor() { this.signal = {}; } abort() { aborted.push(true); } }
    }
  });
  ctx.fetch = ctx.window.fetch;
  vm.runInContext(post[0], ctx);
  ctx.railPost('/agents/leads', {}, (st, v, s) => calls.push([st, s]), 20000);
  assert.equal(timers.length, 1);
  assert.equal(timers[0].ms, 20000);
  timers[0].fn();
  assert.deepEqual(calls, [[0, 'js.post.timeout']]);
  assert.equal(aborted.length, 1);
  answer({ ok: true, status: 201, text: () => Promise.resolve('{"id":"late"}') });
  setImmediate(() => {
    assert.deepEqual(calls, [[0, 'js.post.timeout']], 'a late answer after the timeout is ignored');
    // Without a timeout nothing is armed and the answer arrives as before.
    const plain = [];
    ctx.railPost('/x', {}, (st, v) => plain.push([st, v.id]));
    assert.equal(timers.length, 1);
    answer({ ok: true, status: 201, text: () => Promise.resolve('{"id":"ok"}') });
    setImmediate(() => {
      assert.deepEqual(plain, [[201, 'ok']]);
      console.log('lead popover cases passed');
    });
  });
}

// Harness warnings use translated warning toasts, once per distinct notice
// for the page lifetime, without inserting a persistent element.
{
  const table = {
    'js.lead.harness.unverified': 'Harness version {0} is outside the verified range {1}; running on the capabilities of {2}.',
    'js.lead.harness.unreported': 'unreported',
    'js.lead.harness.demoted': 'Unavailable in this session: {0}.'
  };
  const wsT = (k, ...a) => (table[k] || k).replace(/\{(\d+)\}/g, (m, i) => String(a[Number(i)]));
  const calls = [];
  const noDOM = () => assert.fail('harness warnings must not access or insert DOM elements');
  const window = { WsToast: { show(text, opts) { calls.push({ text, kind: opts.kind }); } } };
  const leadsSource = readAssetSource('wsdashboard', 'dashboard-leads.js');
  const ctx = vm.createContext({ wsT, window, mk: noDOM, luIcon: noDOM, $: noDOM, document: { createElement: noDOM } });
  vm.runInContext(leadsSource.match(/  function leadGone\(st\) \{.*\}\n/)[0] +
    leadsSource.match(/  var harnessNotices = new Set\(\);\n  function harnessDraw\(st\) \{[\s\S]*?\n  \}\n/)[0], ctx);
  const caps = extra => ({ status: 'running', capabilities: Object.assign({ role: 'lead', profile_version: '2.1.326', oldest: '2.1.322', newest: '2.1.326' }, extra) });

  ctx.harnessDraw({ status: 'running' });
  ctx.harnessDraw(caps({ version: '2.1.326' }));
  ctx.harnessDraw(caps({ version: '2.1.326', demoted: [] }));
  ctx.harnessDraw({ status: 'closed', capabilities: { unverified: true, demoted: ['closed-only'] } });
  assert.equal(calls.length, 0, 'missing capabilities, clean and closed states are silent');

  const a = caps({ version: '2.1.399', unverified: true });
  const b = caps({ unverified: true, demoted: ['compaction', 'rewind'] });
  ctx.harnessDraw(a);
  assert.deepEqual(calls, [{
    text: 'Harness version 2.1.399 is outside the verified range 2.1.322–2.1.326; running on the capabilities of 2.1.326.', kind: 'warn'
  }]);
  ctx.harnessDraw(a);
  assert.equal(calls.length, 1, 'unchanged state emits only once');

  ctx.harnessDraw(b);
  assert.deepEqual(calls[1], {
    text: 'Harness version unreported is outside the verified range 2.1.322–2.1.326; running on the capabilities of 2.1.326. Unavailable in this session: compaction, rewind.',
    kind: 'warn'
  }, 'translated lines are joined with a space in one warning toast');
  ctx.harnessDraw(a);
  assert.equal(calls.length, 2, 'A -> B -> A does not repeat A');

  const demoted = caps({ version: '2.1.326', demoted: ['skill_list'] });
  ctx.harnessDraw(demoted);
  assert.deepEqual(calls[2], { text: 'Unavailable in this session: skill_list.', kind: 'warn' }, 'demotion alone warns');
  ctx.harnessDraw(caps({ version: '2.1.326' }));
  ctx.harnessDraw({ status: 'running' });
  ctx.harnessDraw({ status: 'closed', capabilities: { unverified: true, demoted: ['closed-only'] } });
  ctx.harnessDraw(a);
  ctx.harnessDraw(b);
  ctx.harnessDraw(demoted);
  assert.equal(calls.length, 3, 'clearing or closing does not reset page-lifetime deduplication');

  delete window.WsToast;
  const late = caps({ demoted: ['late-toast'] });
  ctx.harnessDraw(late);
  window.WsToast = { show(text, opts) { calls.push({ text, kind: opts.kind }); } };
  ctx.harnessDraw(late);
  ctx.harnessDraw(late);
  assert.deepEqual(calls[3], { text: 'Unavailable in this session: late-toast.', kind: 'warn' });
  assert.equal(calls.length, 4, 'an unavailable toast API does not consume the notice key');
}

// agy is still a hosted lead harness: picking it shows the Antigravity
// terms warning in the dashboard's locale, and Start still creates the
// lead. No other harness shows it.
{
  const fs = require('node:fs');
  const path = require('node:path');
  const tables = Object.fromEntries(['en', 'ko'].map(lang => [lang,
    JSON.parse(fs.readFileSync(path.resolve(__dirname, '../../wsi18n/tables', lang + '.json'), 'utf8'))]));
  const withAgy = [...legacy.map(h => ({ ...h, lead: true })), { harness: 'agy', lead: true, available: true, models: [{ id: 'gemini' }], efforts: ['high'] }];
  for (const lang of ['en', 'ko']) {
    const text = tables[lang]['js.lead.agy.terms'];
    assert(text && text.includes('Antigravity'), lang + ' table carries the agy terms warning');
    const f = fixture('agy', withAgy, false, k => tables[lang][k] || k);
    assert(f.terms, 'the warning element exists');
    assert.equal(f.terms.hidden, false, lang + ': agy shows the warning');
    assert.equal(f.terms.textContent, text);
    assert.equal(f.terms.attrs.role, 'note');
    assert.equal(f.start.disabled, false, lang + ': the warning does not block creation');
    f.start.listeners.click();
    assert.equal(f.requests.length, 1, lang + ': creation still submits');
    assert.equal(f.requests[0].route, '/agents/leads');
    assert.equal(f.requests[0].body.harness, 'agy');
  }
  const f = fixture('claude', withAgy);
  assert.equal(f.terms.hidden, true, 'claude shows no agy warning');
  f.pick('agy');
  assert.equal(f.terms.hidden, false);
  f.pick('claude');
  assert.equal(f.terms.hidden, true, 'leaving agy hides the warning');
  for (const harness of ['claude']) {
    assert.equal(fixture(harness).terms.hidden, true, harness + ' shows no agy warning');
  }
}
