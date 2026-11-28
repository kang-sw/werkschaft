const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

function makeEnv(opts = {}) {
  class Element {
    constructor(tag, cls = '', text = '') {
      this.tag = tag; this.cls = cls || ''; this.children = []; this.attrs = {}; this.events = {};
      this.style = {}; this.hidden = false; this.disabled = false; this._value = ''; this.parentNode = null;
      this.text = text == null ? '' : String(text);
      const classes = new Set((this.cls).split(' ').filter(Boolean));
      const self = this;
      this.classList = {
        toggle(k, on) { if (on === undefined ? !classes.has(k) : on) classes.add(k); else classes.delete(k); self.cls = [...classes].join(' '); },
        add(k) { this.toggle(k, true); },
        remove(k) { this.toggle(k, false); },
        contains(k) { return classes.has(k); }
      };
    }
    appendChild(c) { c.parentNode = this; this.children.push(c); return c; }
    insertBefore(c) { c.parentNode = this; if (!this.children.includes(c)) this.children.push(c); return c; }
    set textContent(v) { this.children = []; this.text = v == null ? '' : String(v); }
    get textContent() { return this.text || ''; }
    set value(v) { this._value = v; }
    get value() { return this._value || (this.tag === 'select' && this.children[0] ? this.children[0].value : ''); }
    setAttribute(k, v) { this.attrs[k] = String(v); }
    getAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k) ? this.attrs[k] : ''; }
    removeAttribute(k) { delete this.attrs[k]; }
    toggleAttribute(k, on) { if (on) this.attrs[k] = ''; else delete this.attrs[k]; }
    addEventListener(k, f) { this.events[k] = f; }
    focus() {}
    blur() { if (this.events.blur) this.events.blur({}); }
    showModal() { this.open = true; }
    close() { this.open = false; }
  }
  const dialogs = [], sessionWrites = [];
  const c = {
    mode: 'index', root: { hasAttribute: () => false }, supChoices: opts.supChoices ?? null,
    localStorage: opts.localStorage || { getItem: () => null, setItem() {} },
    sessionStorage: { setItem(k, v) { sessionWrites.push([k, v]); } },
    doc: { body: { appendChild(e) { e.parentNode = this; dialogs.push(e); return e; } } },
    supHarness(k) { return c.supChoices && c.supChoices.harnesses.find(h => h.harness === k); },
    tkFetch: opts.tkFetch || (() => {}),
    wsT: opts.wsT || ((k, ...a) => a.length ? [k, ...a].join('|') : k),
    mk: (tag, cls, text) => new Element(tag, cls, text),
    rpPop: () => new Element('div', 'rpop'),
    rpBtn: (cls, text) => new Element('button', cls, text),
    rpId: () => 'id',
    gIcon: () => new Element('icon'),
    rpLive: () => true, railPopPlace() {}, railPopClose() {}, railOpen() {}, saveDraft() {}, notifAsk() {}, go() {}, toast() {},
    WsRail: { leadBody: (root, v) => v },
    railPost: opts.railPost || (() => {})
  };
  vm.createContext(c);
  const source = fs.readFileSync(__dirname + '/assets/dashboard-leads.js', 'utf8');
  const modal = fs.readFileSync(__dirname + '/assets/dashboard-new-session.js', 'utf8');
  vm.runInContext(fs.readFileSync(__dirname + '/assets/dashboard-tier-picker.js', 'utf8') + source.slice(0, source.indexOf('  // The session menu of the viewed hosted session')) + modal, c);
  const p = c.rpLeadMake(), btn = new Element('button');
  const all = (el, pred) => [ ...(pred(el) ? [el] : []), ...el.children.flatMap(x => all(x, pred)) ];
  const find = (el, pred) => all(el, pred)[0];
  const texts = (el) => [el.text || '', ...el.children.flatMap(texts)].join(' ');
  const tierInput = find(p.el, e => e.attrs['data-tier-picker']);
  const tierBox = tierInput.parentNode.parentNode;
  const tierList = tierBox.children[1];
  const modelInput = find(p.el, e => e.attrs['data-model-input']);
  const modelList = modelInput.parentNode.parentNode.children[1];
  const modelTag = find(p.el, e => e.cls.includes('ns-tag'));
  const effortKnob = find(p.el, e => e.attrs['data-effort-knob']);
  const effortReset = find(p.el, e => e.cls.includes('ns-rst'));
  const start = find(p.el, e => e.cls.includes('ns-cta') && e.tag === 'button');
  const closeBtn = find(p.el, e => e.cls.includes('ns-x'));
  const nativeCap = find(p.el, e => e.cls.includes('ns-native-cap'));
  const seg = find(p.el, e => e.attrs['aria-label'] === 'js.sup.harness');
  function pickTier(val) {
    const li = tierList.children.find(x => x.attrs['data-val'] === val);
    assert(li, 'tier option exists: ' + val);
    li.events.mousedown({ preventDefault() {} });
  }
  function typeModel(val) {
    modelInput.value = val;
    modelInput.events.input();
  }
  function pickModel(val) {
    const li = modelList.children.find(x => x.attrs['data-val'] === val);
    assert(li, 'model option exists: ' + val);
    li.events.mousedown({ preventDefault() {} });
  }
  function modelOptions() {
    return modelList.children.filter(li => !li.cls.includes('none')).map(li => li.attrs['data-val']);
  }
  function effortDots() {
    return effortKnob.children[0].children.filter(d => d.cls.includes('kdot')).map(d => d.attrs['data-effort']);
  }
  function pickEffort(val) {
    if (!val) { effortKnob.events.keydown({ key: 'Backspace', preventDefault() {} }); return; }
    const dots = effortDots();
    const idx = dots.indexOf(val);
    assert(idx >= 0, 'effort dot exists: ' + val);
    effortKnob.events.keydown({ key: 'Home', preventDefault() {} });
    for (let i = 0; i < idx; i++) effortKnob.events.keydown({ key: 'ArrowRight', preventDefault() {} });
  }
  function pickMode(m) {
    const b = nativeCap.children.find(x => x.attrs['data-mode'] === m);
    assert(b, 'native mode capsule exists: ' + m);
    b.events.click();
  }
  function checkedMode() {
    const b = nativeCap.children.find(x => x.attrs['aria-checked'] === 'true');
    return b ? b.attrs['data-mode'] : '';
  }
  function pickHarness(h) {
    const b = find(seg, e => e.attrs['data-harness'] === h);
    assert(b, 'harness button exists: ' + h);
    seg.events.click({ target: { closest: () => b } });
    return b;
  }
  return {
    c, p, btn, dialogs, sessionWrites, all, find, texts,
    tierInput, tierBox, tierList, pickTier,
    modelInput, modelList, modelTag, typeModel, pickModel, modelOptions,
    effortKnob, effortReset, effortDots, pickEffort,
    nativeCap, pickMode, checkedMode,
    seg, pickHarness, start, closeBtn
  };
}

test('new session saves only successful starts and restores available per-harness choices', () => {
  let raw = '{bad', pending, reply, writes = 0;
  const hs = [
    { harness: 'claude', lead: true, available: true, models: [{ id: 'opus' }], efforts: ['high'], permission_modes: ['auto', 'plan'] },
    { harness: 'codex', lead: true, available: true, models: [{ id: 'gpt' }], efforts: ['low'], permission_modes: [] }
  ];
  const env = makeEnv({
    wsT: s => s,
    localStorage: { getItem: () => raw, setItem(k, v) { assert.equal(k, 'ws.dashboard.newSession'); raw = v; writes++; } },
    tkFetch(path, opt, cb) { pending = cb; },
    railPost(path, body, cb) { reply = cb; }
  });
  const { c, p, btn, start, closeBtn, modelInput, effortKnob, seg } = env;
  const click = el => el.events.click({ target: el });
  p.open('root', 'name', btn);
  assert.equal(writes, 0);
  pending(200, { harnesses: hs });
  env.typeModel('opus'); env.pickMode('plan'); env.pickEffort('high');
  click(start); reply(500, {}, 'error'); assert.equal(writes, 0);
  click(start); reply(201, { id: '1' }); assert.equal(writes, 1);
  assert.deepEqual(JSON.parse(raw), { harness: 'claude', harnesses: { claude: { model: 'opus', provider: '', mode: 'plan', effort: 'high', iso: 'default' } } });
  p.open('root', 'name', btn);
  assert.equal(modelInput.value, 'opus');
  assert.equal(env.checkedMode(), 'plan');
  const codex = env.find(seg, e => e.attrs['data-harness'] === 'codex');
  assert.equal(codex.disabled, false);
  env.pickHarness('codex');
  assert.equal(codex.attrs['aria-checked'], 'true');
  env.typeModel('gpt'); env.pickEffort('low'); click(start); reply(201, { id: '2' });
  const saved = JSON.parse(raw);
  assert.equal(saved.harness, 'codex');
  assert.equal(saved.harnesses.codex.model, 'gpt');
  assert.equal(saved.harnesses.claude.model, 'opus');
  c.supChoices = null;
  p.open('root', 'name', btn); assert.equal(start.disabled, true);
  hs[1].models = []; hs[1].efforts = ['high']; pending(200, { harnesses: hs });
  assert.equal(codex.attrs['aria-checked'], 'true');
  assert.equal(modelInput.value, ''); assert.equal(p.effort, '');
  const before = raw;
  click(closeBtn); assert.equal(raw, before);
  p.open('root', 'name', btn); assert.equal(raw, before);
  raw = JSON.stringify({ harness: 'claude', harnesses: { claude: { model: 'gone', mode: 'gone', effort: 'gone', iso: 'gone' } } });
  p.open('root', 'name', btn);
  assert.equal(modelInput.value, ''); assert.equal(env.checkedMode(), 'auto'); assert.equal(p.effort, '');
  assert.equal(effortKnob.attrs['aria-valuetext'], 'js.sup.default');
  c.localStorage.getItem = () => { throw Error('blocked'); };
  c.localStorage.setItem = () => { throw Error('blocked'); };
  p.open('root', 'name', btn); click(start); reply(201, { id: '3' });
});

// labelEnv is a modal whose label route answers through labelReply and whose
// navigation, label writes and storage writes are recorded in order.
function labelEnv() {
  const log = [], labelCalls = [], storage = [];
  let reply, pending;
  const hs = [{ harness: 'claude', lead: true, available: true, models: [{ id: 'opus' }], efforts: ['high'], permission_modes: ['auto'] }];
  const env = makeEnv({
    wsT: s => s,
    localStorage: { getItem: () => null, setItem(k, v) { storage.push(v); } },
    tkFetch(path, opt, cb) { pending = cb; },
    railPost(path, body, cb) { log.push(['post', path]); reply = cb; }
  });
  const { c, p, btn } = env;
  c.go = href => log.push(['go', href]);
  c.setTimeout = setTimeout; c.clearTimeout = clearTimeout;
  c.fetch = (url, opt) => new Promise(resolve => {
    labelCalls.push({ url, opt, resolve });
    log.push(['label', url, JSON.parse(opt.body).label]);
  });
  p.open('root', 'name', btn);
  pending(200, { harnesses: hs });
  const input = env.find(p.el, e => e.attrs['data-session-label']);
  return { ...env, log, labelCalls, storage, input, reply: (...a) => reply(...a), open: () => { p.open('root', 'name', btn); pending(200, { harnesses: hs }); } };
}

test('the session label field sits directly after Start inside the CTA wrapper and is not a combobox or knob', () => {
  const env = labelEnv();
  const wrap = env.start.parentNode;
  assert.equal(wrap.cls, 'ns-cta-w');
  assert.equal(wrap.children[0], env.start);
  assert.equal(wrap.children[1], env.input);
  assert.equal(env.input.tag, 'input');
  assert.equal(env.input.cls, 'ns-label-in');
  assert.equal(env.input.type, 'text');
  assert.equal(env.input.maxLength, 64);
  assert.equal(env.input.placeholder, 'js.lead.label.placeholder');
  assert.equal(env.input.attrs['aria-label'], 'js.lead.label.aria');
  assert.equal(wrap.children[2].cls, 'ns-ctahint');
});

test('a typed label is posted to the new session before it opens, trimmed and never remembered', async () => {
  const env = labelEnv();
  env.input.value = '  my 라벨 \n';
  env.start.events.click();
  assert.equal(env.input.disabled, true, 'the field is locked with the start');
  env.reply(201, { id: 'abc/1' });
  assert.deepEqual(env.log, [['post', '/agents/leads'], ['label', '/s/abc%2F1/label', 'my 라벨']]);
  assert.equal(env.start.disabled, true, 'start stays busy while the label is written');
  const call = env.labelCalls[0];
  assert.equal(call.opt.method, 'POST');
  assert.equal(call.opt.headers['Content-Type'], 'application/json');
  assert.equal(call.opt.body, JSON.stringify({ label: 'my 라벨' }));
  call.resolve({ status: 200, ok: true });
  await new Promise(r => setImmediate(r));
  assert.deepEqual(env.log[2], ['go', '/s/abc%2F1']);
  assert.equal(env.log.length, 3);
  assert.equal(env.storage.length, 1);
  assert.ok(!env.storage[0].includes('라벨'), 'the label is not stored with the picks');
  env.open();
  assert.equal(env.input.value, '', 'reopening starts with an empty label');
  assert.equal(env.input.disabled, false);
});

test('an empty or whitespace label sends no label request', () => {
  for (const text of ['', '   ', '\t']) {
    const env = labelEnv();
    env.input.value = text;
    env.start.events.click();
    env.reply(201, { id: 'x1' });
    assert.deepEqual(env.log, [['post', '/agents/leads'], ['go', '/s/x1']]);
    assert.equal(env.labelCalls.length, 0);
  }
});

test('a failed label write, 404 retries, and network errors still open the new session', async () => {
  let env = labelEnv();
  env.input.value = 'name';
  env.start.events.click();
  env.reply(201, { id: 'x2' });
  env.labelCalls[0].resolve({ status: 500, ok: false });
  await new Promise(r => setImmediate(r));
  assert.deepEqual(env.log.at(-1), ['go', '/s/x2']);

  env = labelEnv();
  env.input.value = 'name';
  env.start.events.click();
  env.reply(201, { id: 'x3' });
  env.c.fetch = () => Promise.reject(new Error('offline'));
  env.labelCalls[0].resolve({ status: 404, ok: false });
  await new Promise(r => setTimeout(r, 450));
  assert.deepEqual(env.log.at(-1), ['go', '/s/x3'], 'a retry that fails still navigates');
  assert.equal(env.log.filter(e => e[0] === 'go').length, 1);
});

test('Enter in the label field starts like the button, and is ignored while busy or composing', () => {
  const env = labelEnv();
  let prevented = 0;
  const key = (e = {}) => env.input.events.keydown({ key: 'Enter', preventDefault() { prevented++; }, ...e });
  key({ isComposing: true });
  key({ keyCode: 229 });
  key({ key: 'a' });
  assert.deepEqual(env.log, []);
  key();
  assert.deepEqual(env.log, [['post', '/agents/leads']]);
  assert.equal(prevented, 1);
  key();
  assert.equal(env.log.length, 1, 'a second Enter while busy sends nothing');
});

test('new session popover rereads the harness choices on every open and keeps the form', () => {
  const pending = [];
  const harness = models => ({ harness: 'codex', lead: true, available: true, models: models.map(id => ({ id })), efforts: ['low'], permission_modes: ['read-only'] });
  const env = makeEnv({
    wsT: s => s,
    supChoices: { harnesses: [harness(['gpt-old'])] },
    localStorage: { getItem: () => JSON.stringify({ harness: 'codex' }), setItem() {} },
    tkFetch(path, opt, cb) { assert.equal(path, '/support/choices'); pending.push(cb); }
  });
  const { p, btn, modelInput } = env;
  p.open('root', 'name', btn);
  // The held list answers at once; the read is still sent.
  assert.equal(pending.length, 1);
  assert.deepEqual(env.modelOptions(), ['', 'gpt-old']);
  env.pickModel('gpt-old');
  pending.shift()(200, { harnesses: [harness(['gpt-old', 'gpt-new'])] });
  assert.deepEqual(env.modelOptions(), ['', 'gpt-old', 'gpt-new']);
  assert.equal(modelInput.value, 'gpt-old', 'a pick survives the refresh');
  // A failed read keeps the held list.
  p.open('root', 'name', btn);
  assert.equal(pending.length, 1);
  pending.shift()(500, {});
  assert.deepEqual(env.modelOptions(), ['', 'gpt-old', 'gpt-new']);
});

test('custom model input, effort knob reset, and danger native CTA state work cleanly', () => {
  const pending = [], posts = [];
  const hs = [
    { harness: 'claude', lead: true, available: true, models: [{ id: 'opus' }], efforts: ['low', 'high'], permission_modes: ['auto', 'bypassPermissions'] }
  ];
  const env = makeEnv({
    wsT: s => s,
    tkFetch(path, opt, cb) { pending.push(cb); },
    railPost(path, body, cb) { posts.push({ path, body, cb }); }
  });
  const { p, btn, modelTag, effortReset, start } = env;
  p.open('root', 'name', btn);
  pending.shift()(200, { harnesses: hs });
  assert.equal(modelTag.hidden, true);
  env.typeModel('claude-custom-preview');
  assert.equal(modelTag.hidden, false, 'unlisted custom model shows custom badge');
  env.pickEffort('high');
  assert.equal(p.effort, 'high');
  assert.equal(effortReset.hidden, false, 'reset button shows when effort is set');
  effortReset.events.click();
  assert.equal(p.effort, '', 'reset button clears effort to default');
  assert.equal(effortReset.hidden, true);
  assert.equal(start.classList.contains('danger'), false);
  env.pickMode('bypassPermissions');
  assert.equal(start.classList.contains('danger'), true, 'bypassPermissions turns hero CTA danger-red');
  start.events.click({ target: start });
  assert.equal(posts.length, 1);
  assert.equal(posts[0].body.model, 'claude-custom-preview');
  assert.equal(posts[0].body.effort, '');
  assert.equal(posts[0].body.mode, 'bypassPermissions');
});

test('a named tier fills harness, model and effort; a hand edit returns to manual; permission and payload stay concrete', () => {
  const pending = [], posts = [];
  const hs = [
    { harness: 'claude', lead: true, available: true, models: [{ id: 'opus' }], efforts: ['high'], permission_modes: ['auto', 'plan'], permission_levels: ['read-only', 'workdir-write'] },
    { harness: 'codex', lead: true, available: true, models: [{ id: 'gpt-5' }, { id: 'gpt-5-mini' }], efforts: ['low', 'high'], permission_modes: ['read-only'], permission_levels: ['read-only'] }
  ];
  const tiers = [
    { name: 'broken', error: 'tier "broken": harness gone' },
    { name: 'deep', trace: 'deep', harness: 'codex', model: 'gpt-5', effort: 'high' },
    { name: 'far', trace: 'far', harness: 'agy', model: 'm' }
  ];
  const env = makeEnv({
    tkFetch(path, opt, cb) { pending.push(cb); },
    railPost(path, body, cb) { posts.push({ path, body, cb }); }
  });
  const { p, btn, tierInput, tierBox, tierList, modelInput, start } = env;
  p.open('root', 'name', btn);
  pending.shift()(200, { harnesses: hs });
  assert.ok(tierInput, 'the tier picker exists');
  assert.equal(tierBox.hidden, true, 'no named tiers: the picker pill is hidden');

  // Bundles arrive with the choices; an unresolvable tier and one whose
  // harness this modal cannot start are listed but cannot be picked.
  p.open('root', 'name', btn);
  pending.shift()(200, { harnesses: hs, tiers });
  assert.equal(tierBox.hidden, false);
  assert.deepEqual(
    tierList.children.map(o => [o.attrs['data-val'], o.attrs['aria-disabled'] === 'true']),
    [['', false], ['broken', true], ['deep', false], ['far', true]]
  );
  assert.equal(p.harness, 'claude');
  p.level = 'workdir-write';
  env.pickTier('deep');
  assert.equal(p.harness, 'codex');
  assert.equal(p.level, 'workdir-write', 'the tier does not rewrite the permission pick');
  p.level = 'read-only';

  // Picking fills the editable fields; the permission level is untouched.
  env.pickTier('deep');
  assert.equal(p.harness, 'codex');
  assert.equal(modelInput.value, 'gpt-5');
  assert.equal(p.effort, 'high');
  assert.equal(p.level, 'read-only', 'the permission level is independent of the tier');
  assert.equal(tierInput.value, 'deep');

  // A hand edit of the model returns the picker to manual and keeps the edit.
  env.typeModel('gpt-5-mini');
  assert.equal(tierInput.value, '', 'a manual model edit returns to manual');
  assert.equal(modelInput.value, 'gpt-5-mini');
  env.pickTier('deep');
  env.pickEffort('low');
  assert.equal(tierInput.value, '', 'a manual effort edit returns to manual');
  env.pickTier('deep');
  env.pickHarness('claude');
  assert.equal(tierInput.value, '', 'a harness change returns to manual');
  env.pickHarness('codex');

  // The launch sends the concrete fields and the user's own level; no tier.
  env.pickTier('deep');
  p.level = 'read-only';
  start.events.click({ target: start });
  pending.shift()(200, { harnesses: hs, tiers });
  assert.equal(posts.length, 1);
  assert.equal(posts[0].path, '/agents/leads/permission');
  const body = posts[0].body;
  assert.equal(body.harness, 'codex');
  assert.equal(body.model, 'gpt-5');
  assert.equal(body.effort, 'high');
  assert.equal(body.level, 'read-only');
  assert.equal('tier' in body, false, 'the launch carries no tier name');
});

test('a picked named tier keeps its off-catalog model and effort across a choices refresh, and goes manual when it vanishes', () => {
  const pending = [], posts = [];
  const hs = [
    { harness: 'claude', lead: true, available: true, models: [{ id: 'opus' }], efforts: ['high'], permission_modes: ['auto', 'plan'], permission_levels: ['read-only'] },
    { harness: 'codex', lead: true, available: true, models: [{ id: 'gpt-5' }], efforts: ['low', 'high'], permission_modes: ['read-only'], permission_levels: ['read-only'] }
  ];
  const offCatalog = { name: 'preview', trace: 'preview', harness: 'codex', model: 'gpt-x-preview', effort: 'xhigh' };
  const tiers = [offCatalog];
  const env = makeEnv({
    tkFetch(path, opt, cb) { pending.push(cb); },
    railPost(path, body, cb) { posts.push({ path, body, cb }); }
  });
  const { p, btn, tierInput, modelInput, start } = env;
  const launch = () => {
    start.events.click({ target: start });
    if (pending.length) pending.shift()(200, { harnesses: hs, tiers });
    const sent = posts.shift();
    return sent && sent.body;
  };
  p.open('root', 'name', btn);
  pending.shift()(200, { harnesses: hs, tiers });

  // Reopened over a held list: the tier is picked before the fresh list arrives.
  p.open('root', 'name', btn);
  env.pickTier('preview');
  assert.equal(modelInput.value, 'gpt-x-preview');
  assert.equal(p.effort, 'xhigh');
  pending.shift()(200, { harnesses: hs, tiers });
  assert.equal(tierInput.value, 'preview', 'the tier stays picked');
  assert.equal(modelInput.value, 'gpt-x-preview', 'the refresh re-applies the bundle model');
  assert.equal(p.effort, 'xhigh', 'the refresh re-applies the bundle effort');
  p.level = 'read-only';
  let body = launch();
  assert.equal(body.harness, 'codex');
  assert.equal(body.model, 'gpt-x-preview', 'the launch carries the bundle model, not a blank');
  assert.equal(body.effort, 'xhigh');

  // Backstop: fields that disagree with the shown tier are never sent; the tier is put back first.
  p.open('root', 'name', btn);
  pending.shift()(200, { harnesses: hs, tiers });
  env.pickTier('preview');
  p.model = ''; p.effort = '';
  p.level = 'read-only';
  body = launch();
  assert.equal(body.model, 'gpt-x-preview');
  assert.equal(body.effort, 'xhigh');

  // The bundle vanished from the refreshed list: back to manual, fields stay.
  p.open('root', 'name', btn);
  env.pickTier('preview');
  pending.shift()(200, { harnesses: hs, tiers: [] });
  assert.equal(tierInput.value, '', 'a vanished tier returns the form to manual');
  assert.equal(modelInput.value, 'gpt-x-preview');
  assert.equal(p.effort, 'xhigh');
  p.level = 'read-only';
  body = launch();
  assert.equal(body.model, modelInput.value);
  assert.equal(body.effort, p.effort);
});

test('the full-permission grant launches the request the warning named, even after a refresh re-applies the tier elsewhere', () => {
  const pending = [], posts = [];
  const levels = ['read-only', 'workdir-write', 'full'];
  const hs = [
    { harness: 'claude', lead: true, available: true, models: [{ id: 'opus' }, { id: 'sonnet' }], efforts: ['high', 'max'], permission_modes: ['auto', 'plan'], permission_levels: levels },
    { harness: 'codex', lead: true, available: true, models: [{ id: 'gpt-5' }], efforts: ['low', 'high'], permission_modes: ['read-only'], permission_levels: levels }
  ];
  const onCodex = [{ name: 'name', trace: 'name', harness: 'codex', model: 'gpt-5', effort: 'low' }];
  const onClaude = [{ name: 'name', trace: 'name', harness: 'claude', model: 'sonnet', effort: 'max' }];
  const env = makeEnv({
    tkFetch(path, opt, cb) { pending.push(cb); },
    railPost(path, body, cb) { posts.push({ path, body, cb }); }
  });
  const { p, btn, dialogs, texts, modelInput, start } = env;
  p.open('root', 'name', btn);
  pending.shift()(200, { harnesses: hs, tiers: onCodex });

  p.open('root', 'name', btn);
  env.pickTier('name');
  assert.equal(p.harness, 'codex');
  p.level = 'full';
  start.events.click({ target: start });
  const dlg = dialogs[0];
  assert.ok(dlg && dlg.open, 'the full warning is open');
  assert.match(texts(dlg), /name · codex/i, 'the warning names codex');
  assert.equal(posts.length, 0);

  pending.shift()(200, { harnesses: hs, tiers: onClaude });
  assert.equal(p.harness, 'claude', 'the refresh re-applied the tier on claude');
  assert.equal(modelInput.value, 'sonnet');

  const box = dlg.children[3].children[0], grant = dlg.children[4].children[1];
  box.checked = true; box.events.change();
  grant.events.click();
  assert.equal(pending.length, 1, 'the grant runs the permission pre-flight');
  pending.shift()(200, { harnesses: hs, tiers: onClaude });
  assert.equal(posts.length, 1);
  assert.equal(posts[0].path, '/agents/leads/permission');
  const body = posts[0].body;
  assert.equal(body.harness, 'codex', 'the grant launches the harness the warning named');
  assert.equal(body.model, 'gpt-5');
  assert.equal(body.effort, 'low');
  assert.equal(body.level, 'full');
  assert.equal(body.ack, true);
  assert.equal(p.effort, 'max', 'the form itself still shows the refreshed tier');
});

test('a refused full grant for a harness the form no longer shows leaves the form permission untouched', () => {
  const pending = [], posts = [];
  const levels = ['read-only', 'workdir-write', 'full'];
  const claude = { harness: 'claude', lead: true, available: true, models: [{ id: 'opus' }, { id: 'sonnet' }], efforts: ['high', 'max'], permission_modes: ['auto', 'plan'], permission_levels: levels };
  const codex = { harness: 'codex', lead: true, available: true, models: [{ id: 'gpt-5' }], efforts: ['low', 'high'], permission_modes: ['read-only'], permission_levels: levels };
  const hs = [claude, codex];
  const codexGone = [claude, { ...codex, available: false, error: 'not installed' }];
  const onCodex = [{ name: 'name', trace: 'name', harness: 'codex', model: 'gpt-5', effort: 'low' }];
  const onClaude = [{ name: 'name', trace: 'name', harness: 'claude', model: 'sonnet', effort: 'max' }];
  const env = makeEnv({
    tkFetch(path, opt, cb) { pending.push(cb); },
    railPost(path, body, cb) { posts.push({ path, body, cb }); }
  });
  const { p, btn, dialogs, texts, start } = env;
  p.open('root', 'name', btn);
  pending.shift()(200, { harnesses: hs, tiers: onCodex });

  p.open('root', 'name', btn);
  env.pickTier('name');
  p.level = 'full';
  start.events.click({ target: start });
  const dlg = dialogs[0];
  assert.ok(dlg && dlg.open, 'the full warning is open');
  assert.match(texts(dlg), /name · codex/i);

  pending.shift()(200, { harnesses: codexGone, tiers: onClaude });
  assert.equal(p.harness, 'claude');

  const grant = () => {
    const box = dlg.children[3].children[0];
    box.checked = true; box.events.change();
    dlg.children[4].children[1].events.click();
  };
  grant();
  assert.equal(pending.length, 1, 'the grant runs the permission pre-flight');
  pending.shift()(200, { harnesses: codexGone, tiers: onClaude });
  assert.equal(posts.length, 0, 'a refused snapshot sends nothing');
  assert.ok(!dlg.open, 'the warning closed');
  assert.match(texts(p.el), /js\.perm\.unsent/, 'the refusal shows');
  assert.equal(p.dropped, '', 'the refusal does not drop the level of the form now on claude');
  assert.equal(p.level, 'full');

  start.events.click({ target: start });
  assert.equal(posts.length, 0, 'Start asks again before a full start');
  assert.ok(dlg.open, 'the full warning opened again');
  assert.match(texts(dlg), /name · claude/i);
  grant();
  pending.shift()(200, { harnesses: codexGone, tiers: onClaude });
  assert.equal(posts.length, 1);
  assert.equal(posts[0].path, '/agents/leads/permission');
  assert.equal(posts[0].body.harness, 'claude');
  assert.equal(posts[0].body.level, 'full');
  assert.equal(posts[0].body.ack, true);
});

test('a choices read after a stream reconnect re-applies the picked tier, so its note and the fields agree', () => {
  const pending = [];
  const hs = [
    { harness: 'claude', lead: true, available: true, models: [{ id: 'opus' }], efforts: ['high'], permission_modes: ['auto'], permission_levels: ['read-only'] },
    { harness: 'codex', lead: true, available: true, models: [{ id: 'gpt-5' }, { id: 'gpt-5-mini' }], efforts: ['low', 'high'], permission_modes: ['read-only'], permission_levels: ['read-only'] }
  ];
  const before = [{ name: 'deep', trace: 'deep', harness: 'codex', model: 'gpt-5', effort: 'high' }];
  const after = [{ name: 'deep', trace: 'deep', harness: 'codex', model: 'gpt-5-mini', effort: 'low' }];
  const env = makeEnv({
    tkFetch(path, opt, cb) { pending.push(cb); }
  });
  const { p, btn, tierInput, tierBox, modelInput } = env;
  p.open('root', 'name', btn);
  pending.shift()(200, { harnesses: hs, tiers: before });
  env.pickTier('deep');
  assert.equal(modelInput.value, 'gpt-5');

  p.permSync(true);
  assert.equal(pending.length, 1, 'the reconnect re-reads the choices');
  pending.shift()(200, { harnesses: hs, tiers: after });
  assert.equal(tierInput.value, 'deep', 'the tier stays picked');
  assert.match(tierBox.title, /gpt-5-mini/, 'the tooltip note shows the refreshed bundle');
  assert.equal(modelInput.value, 'gpt-5-mini', 'the model field follows the note');
  assert.equal(p.effort, 'low', 'the effort field follows the note');

  p.permSync(true);
  pending.shift()(200, { harnesses: hs, tiers: [] });
  assert.equal(tierInput.value, '');
  assert.match(tierBox.title, /^js\.lead\.tier\.hint/);
});

test('a choices re-read never changes the fields on its own: manual off-catalog values and the harness stay', () => {
  const pending = [], posts = [];
  const claude = { harness: 'claude', lead: true, available: true, models: [{ id: 'opus' }], efforts: ['high'], permission_modes: ['auto'], permission_levels: ['read-only'] };
  const codex = { harness: 'codex', lead: true, available: true, models: [{ id: 'gpt-5' }, { id: 'gpt-5-mini' }], efforts: ['low', 'high'], permission_modes: ['read-only'], permission_levels: ['read-only'] };
  const hs = [claude, codex];
  const tiers = [{ name: 'preview', trace: 'preview', harness: 'codex', model: 'gpt-5', effort: 'xhigh' }];
  const env = makeEnv({
    tkFetch(path, opt, cb) { pending.push(cb); },
    railPost(path, body, cb) { posts.push({ path, body, cb }); }
  });
  const { p, btn, tierInput, modelInput, start } = env;
  const launch = (list) => {
    start.events.click({ target: start });
    if (pending.length) pending.shift()(200, list);
    const sent = posts.shift();
    return sent && sent.body;
  };
  p.open('root', 'name', btn);
  pending.shift()(200, { harnesses: hs, tiers });

  env.pickTier('preview');
  assert.equal(p.effort, 'xhigh');
  env.typeModel('gpt-5-mini');
  assert.equal(tierInput.value, '', 'the hand edit returns to manual');
  p.permSync(true);
  pending.shift()(200, { harnesses: hs, tiers });
  assert.equal(modelInput.value, 'gpt-5-mini');
  assert.equal(p.effort, 'xhigh', 'the re-read keeps the off-catalog effort');
  p.level = 'read-only';
  let body = launch({ harnesses: hs, tiers });
  assert.equal(body.harness, 'codex');
  assert.equal(body.model, 'gpt-5-mini');
  assert.equal(body.effort, 'xhigh', 'the launch still carries the effort the form shows');

  // A manual model the refreshed catalog dropped stays picked and is sent.
  p.open('root', 'name', btn);
  pending.shift()(200, { harnesses: hs, tiers });
  env.pickHarness('codex');
  env.typeModel('gpt-5-mini');
  env.pickEffort('low');
  const dropped = [claude, { ...codex, models: [{ id: 'gpt-5' }] }];
  p.permSync(true);
  pending.shift()(200, { harnesses: dropped, tiers });
  assert.equal(p.harness, 'codex');
  assert.equal(modelInput.value, 'gpt-5-mini', 'a model the catalog dropped stays');
  p.level = 'read-only';
  body = launch({ harnesses: dropped, tiers });
  assert.equal(body.model, 'gpt-5-mini');
  assert.equal(body.effort, 'low');

  // A harness that turned unavailable is not swapped for another: Start refuses it.
  p.open('root', 'name', btn);
  pending.shift()(200, { harnesses: hs, tiers });
  env.pickHarness('codex');
  assert.equal(p.harness, 'codex');
  p.permSync(true);
  pending.shift()(200, { harnesses: [claude, { ...codex, available: false }], tiers });
  assert.equal(p.harness, 'codex', 'the re-read keeps the shown harness');
  assert.equal(start.disabled, true, 'Start refuses the unavailable harness');
  start.events.click({ target: start });
  assert.equal(posts.length, 0, 'nothing is launched on another harness');
});

for (const field of ['model', 'effort']) {
  test(`an empty ${field} list on an available harness keeps the shown ${field} enabled and sends it`, () => {
    const pending = [], posts = [];
    const claude = { harness: 'claude', lead: true, available: true, models: [{ id: 'opus' }], efforts: ['high'], permission_modes: ['auto'], permission_levels: ['read-only'] };
    const codex = { harness: 'codex', lead: true, available: true, models: [{ id: 'gpt-5' }, { id: 'gpt-5-mini' }], efforts: ['low', 'high'], permission_modes: ['read-only'], permission_levels: ['read-only'] };
    const hs = [claude, codex];
    const empty = [claude, { ...codex, [field === 'model' ? 'models' : 'efforts']: [] }];
    const tiers = [];
    const env = makeEnv({
      tkFetch(path, opt, cb) { pending.push(cb); },
      railPost(path, body, cb) { posts.push({ path, body, cb }); }
    });
    const { p, btn, modelInput, effortKnob, start } = env;
    const launch = (list) => {
      start.events.click({ target: start });
      if (pending.length) pending.shift()(200, list);
      const sent = posts.shift();
      return sent && sent.body;
    };

    p.open('root', 'name', btn);
    pending.shift()(200, { harnesses: hs, tiers });
    env.pickHarness('codex');
    env.typeModel('gpt-5-mini');
    env.pickEffort('low');
    p.permSync(true);
    pending.shift()(200, { harnesses: empty, tiers });
    assert.equal(p.harness, 'codex');
    if (field === 'model') {
      assert.equal(modelInput.value, 'gpt-5-mini', 'the model stays shown');
      assert.equal(modelInput.disabled, false, 'the model combobox stays enabled');
    } else {
      assert.equal(p.effort, 'low', 'the effort stays shown');
      assert.ok(env.effortDots().includes('low'), 'it is kept as a dot');
      assert.equal(effortKnob.attrs['aria-disabled'], 'false', 'the effort knob stays enabled');
    }
    assert.equal(start.disabled, false);
    p.level = 'read-only';
    let body = launch({ harnesses: empty, tiers });
    assert.ok(body, 'Start launched');
    assert.equal(body.model, 'gpt-5-mini', 'the launch carries the shown model');
    assert.equal(body.effort, 'low', 'the launch carries the shown effort');

    // With no value held, an empty list keeps the default: disabled and empty.
    p.open('root', 'name', btn);
    pending.shift()(200, { harnesses: hs, tiers });
    env.pickHarness('codex');
    assert.equal(field === 'model' ? modelInput.value : p.effort, '');
    p.permSync(true);
    pending.shift()(200, { harnesses: empty, tiers });
    if (field === 'model') assert.equal(modelInput.disabled, true, 'an empty model list with no pick stays disabled');
    else assert.equal(effortKnob.attrs['aria-disabled'], 'true', 'an empty effort list with no pick stays disabled');
    p.level = 'read-only';
    body = launch({ harnesses: empty, tiers });
    assert.ok(body, 'Start launched');
    assert.equal(body[field], '');
  });
}

test('whiteboard-only toggle remembers last choice and seeds whiteboardFirst.<id> before navigation', () => {
  const store = new Map(), log = [];
  let pending, reply;
  const hs = [{ harness: 'claude', lead: true, available: true, models: [{ id: 'opus' }], efforts: ['high'], permission_modes: ['auto'] }];
  const env = makeEnv({
    wsT: s => s,
    localStorage: {
      getItem: k => store.has(k) ? store.get(k) : null,
      setItem: (k, v) => { store.set(k, v); log.push(['ls', k, v]); }
    },
    tkFetch(path, opt, cb) { pending = cb; },
    railPost(path, body, cb) { log.push(['post', path, body]); reply = cb; }
  });
  env.c.go = href => log.push(['go', href]);
  const wbSw = env.find(env.p.el, e => e.attrs['data-wb-only-switch']);
  const convSw = env.find(env.p.el, e => e.attrs['data-conv-history-switch']);
  assert.ok(wbSw && convSw, 'both footer opt-in switches exist');
  assert.equal(wbSw.attrs['aria-checked'], 'false');
  assert.equal(convSw.attrs['aria-checked'], 'false');

  env.p.open('root', 'name', env.btn);
  pending(200, { harnesses: hs });
  wbSw.events.click();
  convSw.events.click();
  assert.equal(wbSw.attrs['aria-checked'], 'true');
  assert.equal(convSw.attrs['aria-checked'], 'true');

  // Failed start writes neither storage key.
  env.start.events.click({ target: env.start });
  assert.equal(log[0][2].convHistoryAccess, true);
  reply(500, {}, 'fail');
  assert.equal(store.size, 0);

  // Successful 201 saves wbOnly in newSession and seeds whiteboardFirst.<id>="1" before go().
  log.length = 0;
  env.start.events.click({ target: env.start });
  reply(201, { id: 'sess9' });
  const wbIdx = log.findIndex(e => e[0] === 'ls' && e[1] === 'ws.dashboard.whiteboardFirst.sess9');
  const goIdx = log.findIndex(e => e[0] === 'go' && e[1] === '/s/sess9');
  assert.ok(wbIdx >= 0 && goIdx > wbIdx, 'whiteboardFirst.<id> is seeded before go()');
  assert.equal(store.get('ws.dashboard.whiteboardFirst.sess9'), '1');
  assert.equal(JSON.parse(store.get('ws.dashboard.newSession')).whiteboardOnly, true);

  // Reopening restores whiteboardOnly=true while convHistory resets to false (never remembered).
  env.p.open('root', 'name', env.btn);
  pending(200, { harnesses: hs });
  assert.equal(wbSw.attrs['aria-checked'], 'true', 'whiteboardOnly restores from ws.dashboard.newSession');
  assert.equal(convSw.attrs['aria-checked'], 'false', 'convHistory resets to false on reopen');

  // Turning whiteboardOnly back OFF does not seed whiteboardFirst for the next session.
  wbSw.events.click();
  assert.equal(wbSw.attrs['aria-checked'], 'false');
  env.start.events.click({ target: env.start });
  reply(201, { id: 'sess10' });
  assert.equal(store.has('ws.dashboard.whiteboardFirst.sess10'), false);
  assert.equal('whiteboardOnly' in JSON.parse(store.get('ws.dashboard.newSession')), false);
});

test('creating a session never triggers post-navigation auto-rename (with label or blank), while manual rename remains intact', async () => {
  // 1. Blank/default label creation writes no rename intent to sessionStorage.
  const blankEnv = labelEnv();
  blankEnv.input.value = '   ';
  blankEnv.start.events.click({ target: blankEnv.start });
  blankEnv.reply(201, { id: 'blank-sess' });
  assert.deepEqual(blankEnv.log, [['post', '/agents/leads'], ['go', '/s/blank-sess']]);
  assert.deepEqual(blankEnv.sessionWrites, []);

  // 2. Provided label creation posts the label and navigates without writing rename intent.
  const labeledEnv = labelEnv();
  labeledEnv.input.value = '초기 라벨';
  labeledEnv.start.events.click({ target: labeledEnv.start });
  labeledEnv.reply(201, { id: 'labeled-sess' });
  labeledEnv.labelCalls[0].resolve({ status: 200, ok: true });
  await new Promise(r => setImmediate(r));
  assert.deepEqual(labeledEnv.log, [['post', '/agents/leads'], ['label', '/s/labeled-sess/label', '초기 라벨'], ['go', '/s/labeled-sess']]);
  assert.deepEqual(labeledEnv.sessionWrites, []);

  // 3. Rail and shell lifecycle scripts carry no post-create auto-rename hook while keeping manual rename.
  const railSrc = fs.readFileSync(__dirname + '/assets/dashboard-rail.js', 'utf8');
  const lifeSrc = fs.readFileSync(__dirname + '/assets/dashboard-lifecycle.js', 'utf8');
  const actionsSrc = fs.readFileSync(__dirname + '/assets/dashboard-actions.js', 'utf8');
  assert.equal(/RENAME_NEW|railRenameNew|ws\.dashboard\.renameNew/.test(railSrc), false);
  assert.equal(/railRenameNew/.test(lifeSrc), false);
  assert.match(railSrc, /function railRename\(row\)/);
  assert.match(actionsSrc, /function labelStart\(ed\)/);
  assert.match(actionsSrc, /function labelHead\(sn\)/);
});
