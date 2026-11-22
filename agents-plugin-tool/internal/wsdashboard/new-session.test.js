const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

test('new session saves only successful starts and restores available per-harness choices', () => {
  class Element {
    constructor(tag) { this.tag = tag; this.children = []; this.attrs = {}; this.events = {}; this.classList = { toggle() {} }; this._value = ''; }
    appendChild(c) { this.children.push(c); return c; }
    set textContent(v) { this.children = []; this.text = v; }
    get textContent() { return this.text || ''; }
    set value(v) { this._value = v; }
    get value() { return this._value || (this.tag === 'select' && this.children[0] ? this.children[0].value : ''); }
    setAttribute(k, v) { this.attrs[k] = v; }
    getAttribute(k) { return this.attrs[k] || ''; }
    addEventListener(k, f) { this.events[k] = f; }
    focus() {}
  }
  let raw = '{bad', pending, reply, writes = 0;
  const hs = [ { harness: 'claude', lead: true, available: true, models: [{ id: 'opus' }], efforts: ['high'], permission_modes: ['auto', 'plan'] }, { harness: 'pi', lead: true, available: true, models: [{ id: 'gpt' }], efforts: ['low'], permission_modes: [] } ];
  const c = { mode: 'index', root: { hasAttribute: () => false }, supChoices: null,
    localStorage: { getItem: () => raw, setItem(k, v) { assert.equal(k, 'ws.dashboard.newSession'); raw = v; writes++; } },
    sessionStorage: { setItem() {} }, RENAME_NEW: 'rename',
    supHarness(k) { return c.supChoices && c.supChoices.harnesses.find(h => h.harness === k); },
    tkFetch(path, opt, cb) { pending = cb; }, wsT: s => s, mk: (tag) => new Element(tag), rpPop: () => new Element('div'), rpBtn: () => new Element('button'), rpId: () => 'id',
    gIcon: () => new Element('icon'), rpLive: () => true, railPopPlace() {}, railPopClose() {}, railOpen() {}, saveDraft() {}, notifAsk() {}, go() {}, toast() {},
    WsRail: { leadBody: (root, v) => v }, railPost(path, body, cb) { reply = cb; }
  };
  vm.createContext(c);
  const source = fs.readFileSync(__dirname + '/assets/dashboard-leads.js', 'utf8');
  vm.runInContext(fs.readFileSync(__dirname + '/assets/dashboard-tier-picker.js', 'utf8') + source.slice(0, source.indexOf('  // The session menu of the viewed hosted session')), c);
  const p = c.rpLeadMake(), btn = new Element('button');
  const find = (el, pred) => pred(el) ? el : el.children.map(x => find(x, pred)).find(Boolean);
  const all = (el, tag) => [ ...(el.tag === tag && !el.attrs['data-tier-picker'] ? [el] : []), ...el.children.flatMap(x => all(x, tag)) ];
  const click = el => el.events.click({ target: el });
  p.open('root', 'name', btn);
  assert.equal(writes, 0);
  pending(200, { harnesses: hs });
  let selects = all(p.el, 'select');
  selects[0].value = 'opus'; selects[1].value = 'plan'; selects[2].value = 'high';
  const start = p.el.children.at(-1).children.at(-1);
  click(start); reply(500, {}, 'error'); assert.equal(writes, 0);
  click(start); reply(201, { id: '1' }); assert.equal(writes, 1);
  assert.deepEqual(JSON.parse(raw), { harness: 'claude', harnesses: { claude: { model: 'opus', mode: 'plan', effort: 'high', iso: 'default' } } });
  p.open('root', 'name', btn);
  selects = all(p.el, 'select'); assert.equal(selects[0].value, 'opus'); assert.equal(selects[1].value, 'plan');
  const segment = find(p.el, e => e.attrs['aria-label'] === 'js.sup.harness');
  const pi = find(segment, e => e.attrs['data-harness'] === 'pi');
  segment.events.click({ target: { closest: () => pi } });
  selects[0].value = 'gpt'; selects[2].value = 'low'; click(start); reply(201, { id: '2' });
  assert.equal(JSON.parse(raw).harnesses.claude.model, 'opus');
  c.supChoices = null;
  p.open('root', 'name', btn); assert.equal(start.disabled, true);
  hs[1].models = []; hs[1].efforts = ['high']; pending(200, { harnesses: hs });
  assert.equal(pi.attrs['aria-checked'], 'true');
  assert.equal(selects[0].value, ''); assert.equal(selects[2].value, '');
  const before = raw;
  const cancel = p.el.children.at(-1).children.at(-2);
  click(cancel); assert.equal(raw, before);
  p.open('root', 'name', btn); assert.equal(raw, before);
  raw = JSON.stringify({ harness: 'claude', harnesses: { claude: { model: 'gone', mode: 'gone', effort: 'gone', iso: 'gone' } } });
  p.open('root', 'name', btn);
  assert.equal(selects[0].value, ''); assert.equal(selects[1].value, 'auto'); assert.equal(selects[2].value, '');
  c.localStorage.getItem = () => { throw Error('blocked'); };
  c.localStorage.setItem = () => { throw Error('blocked'); };
  p.open('root', 'name', btn); click(start); reply(201, { id: '3' });
});

test('new session popover rereads the harness choices on every open and keeps the form', () => {
  class Element {
    constructor(tag) { this.tag = tag; this.children = []; this.attrs = {}; this.events = {}; this.classList = { toggle() {} }; this._value = ''; }
    appendChild(c) { this.children.push(c); return c; }
    set textContent(v) { this.children = []; this.text = v; }
    get textContent() { return this.text || ''; }
    set value(v) { this._value = v; }
    get value() { return this._value || (this.tag === 'select' && this.children[0] ? this.children[0].value : ''); }
    setAttribute(k, v) { this.attrs[k] = v; }
    getAttribute(k) { return this.attrs[k] || ''; }
    addEventListener(k, f) { this.events[k] = f; }
    focus() {}
  }
  const pending = [];
  const harness = models => ({ harness: 'codex', lead: true, available: true, models: models.map(id => ({ id })), efforts: ['low'], permission_modes: ['read-only'] });
  const c = { mode: 'index', root: { hasAttribute: () => false }, supChoices: { harnesses: [harness(['gpt-old'])] },
    localStorage: { getItem: () => JSON.stringify({ harness: 'codex' }), setItem() {} }, sessionStorage: { setItem() {} }, RENAME_NEW: 'rename',
    supHarness(k) { return c.supChoices && c.supChoices.harnesses.find(h => h.harness === k); },
    tkFetch(path, opt, cb) { assert.equal(path, '/support/choices'); pending.push(cb); }, wsT: s => s, mk: (tag) => new Element(tag), rpPop: () => new Element('div'), rpBtn: () => new Element('button'), rpId: () => 'id',
    gIcon: () => new Element('icon'), rpLive: () => true, railPopPlace() {}, railPopClose() {}, railOpen() {}, saveDraft() {}, notifAsk() {}, go() {}, toast() {},
    WsRail: { leadBody: (root, v) => v }, railPost() {}
  };
  vm.createContext(c);
  const source = fs.readFileSync(__dirname + '/assets/dashboard-leads.js', 'utf8');
  vm.runInContext(fs.readFileSync(__dirname + '/assets/dashboard-tier-picker.js', 'utf8') + source.slice(0, source.indexOf('  // The session menu of the viewed hosted session')), c);
  const p = c.rpLeadMake(), btn = new Element('button');
  const all = (el, tag) => [ ...(el.tag === tag && !el.attrs['data-tier-picker'] ? [el] : []), ...el.children.flatMap(x => all(x, tag)) ];
  const options = el => el.children.map(o => o.value);
  p.open('root', 'name', btn);
  // The held list answers at once; the read is still sent.
  assert.equal(pending.length, 1);
  const model = all(p.el, 'select')[0];
  assert.deepEqual(options(model), ['', 'gpt-old']);
  model.value = 'gpt-old';
  pending.shift()(200, { harnesses: [harness(['gpt-old', 'gpt-new'])] });
  assert.deepEqual(options(model), ['', 'gpt-old', 'gpt-new']);
  assert.equal(model.value, 'gpt-old', 'a pick survives the refresh');
  // A failed read keeps the held list.
  p.open('root', 'name', btn);
  assert.equal(pending.length, 1);
  pending.shift()(500, {});
  assert.deepEqual(options(model), ['', 'gpt-old', 'gpt-new']);
});

test('a named tier fills harness, model and effort; a hand edit returns to manual; permission and payload stay concrete', () => {
  class Element {
    constructor(tag) { this.tag = tag; this.children = []; this.attrs = {}; this.events = {}; this.classList = { toggle() {} }; this._value = ''; }
    appendChild(c) { this.children.push(c); return c; }
    insertBefore(c) { if (!this.children.includes(c)) this.children.push(c); return c; }
    set textContent(v) { this.children = []; this.text = v; }
    get textContent() { return this.text || ''; }
    set value(v) { this._value = v; }
    get value() { return this._value || (this.tag === 'select' && this.children[0] ? this.children[0].value : ''); }
    setAttribute(k, v) { this.attrs[k] = v; }
    getAttribute(k) { return this.attrs[k] || ''; }
    addEventListener(k, f) { this.events[k] = f; }
    focus() {}
  }
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
  const c = { mode: 'index', root: { hasAttribute: () => false }, supChoices: null,
    localStorage: { getItem: () => null, setItem() {} }, sessionStorage: { setItem() {} }, RENAME_NEW: 'rename',
    supHarness(k) { return c.supChoices && c.supChoices.harnesses.find(h => h.harness === k); },
    tkFetch(path, opt, cb) { pending.push(cb); }, wsT: (k, ...a) => [k, ...a].join('|'), mk: (tag) => new Element(tag), rpPop: () => new Element('div'), rpBtn: () => new Element('button'), rpId: () => 'id',
    gIcon: () => new Element('icon'), rpLive: () => true, railPopPlace() {}, railPopClose() {}, railOpen() {}, saveDraft() {}, notifAsk() {}, go() {}, toast() {},
    WsRail: { leadBody: (root, v) => v }, railPost(path, body, cb) { posts.push({ path, body, cb }); }
  };
  vm.createContext(c);
  const source = fs.readFileSync(__dirname + '/assets/dashboard-leads.js', 'utf8');
  vm.runInContext(fs.readFileSync(__dirname + '/assets/dashboard-tier-picker.js', 'utf8') + source.slice(0, source.indexOf('  // The session menu of the viewed hosted session')), c);
  const p = c.rpLeadMake(), btn = new Element('button');
  const all = (el, pred) => [ ...(pred(el) ? [el] : []), ...el.children.flatMap(x => all(x, pred)) ];
  const find = (el, pred) => all(el, pred)[0];
  p.open('root', 'name', btn);
  pending.shift()(200, { harnesses: hs });
  const picker = find(p.el, e => e.attrs['data-tier-picker']);
  assert.ok(picker, 'the picker row exists');
  const [model, , effort] = all(p.el, e => e.tag === 'select' && !e.attrs['data-tier-picker']);
  const holder = find(p.el, e => e.children.some(x => x.children && x.children.includes(picker)));
  assert.equal(holder.hidden, true, 'no named tiers: the picker row is hidden and the form reads as before');

  // Bundles arrive with the choices; an unresolvable tier and one whose
  // harness this popover cannot start are listed but cannot be picked.
  p.open('root', 'name', btn);
  pending.shift()(200, { harnesses: hs, tiers });
  assert.equal(holder.hidden, false);
  assert.deepEqual(picker.children.map(o => [o.value, !!o.disabled]), [['', false], ['broken', true], ['deep', false], ['far', true]]);
  assert.equal(p.harness, 'claude');
  // A level the tier's harness does not offer stays the user's pick (the
  // form shows it as dropped and does not send it); the tier never clears it.
  p.level = 'workdir-write';
  picker.value = 'deep'; picker.events.change();
  assert.equal(p.harness, 'codex');
  assert.equal(p.level, 'workdir-write', 'the tier does not rewrite the permission pick');
  p.level = 'read-only';

  // Picking fills the editable fields; the permission level is untouched.
  picker.value = 'deep'; picker.events.change();
  assert.equal(p.harness, 'codex');
  assert.equal(model.value, 'gpt-5');
  assert.equal(effort.value, 'high');
  assert.equal(p.level, 'read-only', 'the permission level is independent of the tier');
  assert.equal(picker.value, 'deep');

  // A hand edit of the model returns the picker to manual and keeps the edit.
  model.value = 'gpt-5-mini'; model.events.change(model);
  assert.equal(picker.value, '', 'a manual model edit returns to manual');
  assert.equal(model.value, 'gpt-5-mini');
  picker.value = 'deep'; picker.events.change();
  effort.value = 'low'; effort.events.change(effort);
  assert.equal(picker.value, '', 'a manual effort edit returns to manual');
  picker.value = 'deep'; picker.events.change();
  const seg = find(p.el, e => e.attrs['aria-label'] === 'js.sup.harness');
  const claude = find(seg, e => e.attrs['data-harness'] === 'claude'), codex = find(seg, e => e.attrs['data-harness'] === 'codex');
  seg.events.click({ target: { closest: () => claude } });
  assert.equal(picker.value, '', 'a harness change returns to manual');
  seg.events.click({ target: { closest: () => codex } });

  // The launch sends the concrete fields and the user's own level; no tier.
  picker.value = 'deep'; picker.events.change();
  p.level = 'read-only';
  const start = p.el.children.at(-1).children.at(-1);
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
  class Element {
    constructor(tag) { this.tag = tag; this.children = []; this.attrs = {}; this.events = {}; this.classList = { toggle() {} }; this._value = ''; }
    appendChild(c) { this.children.push(c); return c; }
    insertBefore(c) { if (!this.children.includes(c)) this.children.push(c); return c; }
    set textContent(v) { this.children = []; this.text = v; }
    get textContent() { return this.text || ''; }
    set value(v) { this._value = v; }
    get value() { return this._value || (this.tag === 'select' && this.children[0] ? this.children[0].value : ''); }
    setAttribute(k, v) { this.attrs[k] = v; }
    getAttribute(k) { return this.attrs[k] || ''; }
    addEventListener(k, f) { this.events[k] = f; }
    focus() {}
  }
  const pending = [], posts = [];
  const hs = [
    { harness: 'claude', lead: true, available: true, models: [{ id: 'opus' }], efforts: ['high'], permission_modes: ['auto', 'plan'], permission_levels: ['read-only'] },
    { harness: 'codex', lead: true, available: true, models: [{ id: 'gpt-5' }], efforts: ['low', 'high'], permission_modes: ['read-only'], permission_levels: ['read-only'] }
  ];
  // The bundle's model and effort are not in codex's catalog lists.
  const offCatalog = { name: 'preview', trace: 'preview', harness: 'codex', model: 'gpt-x-preview', effort: 'xhigh' };
  const tiers = [offCatalog];
  const c = { mode: 'index', root: { hasAttribute: () => false }, supChoices: null,
    localStorage: { getItem: () => null, setItem() {} }, sessionStorage: { setItem() {} }, RENAME_NEW: 'rename',
    supHarness(k) { return c.supChoices && c.supChoices.harnesses.find(h => h.harness === k); },
    tkFetch(path, opt, cb) { pending.push(cb); }, wsT: (k, ...a) => [k, ...a].join('|'), mk: (tag) => new Element(tag), rpPop: () => new Element('div'), rpBtn: () => new Element('button'), rpId: () => 'id',
    gIcon: () => new Element('icon'), rpLive: () => true, railPopPlace() {}, railPopClose() {}, railOpen() {}, saveDraft() {}, notifAsk() {}, go() {}, toast() {},
    WsRail: { leadBody: (root, v) => v }, railPost(path, body, cb) { posts.push({ path, body, cb }); }
  };
  vm.createContext(c);
  const source = fs.readFileSync(__dirname + '/assets/dashboard-leads.js', 'utf8');
  vm.runInContext(fs.readFileSync(__dirname + '/assets/dashboard-tier-picker.js', 'utf8') + source.slice(0, source.indexOf('  // The session menu of the viewed hosted session')), c);
  const p = c.rpLeadMake(), btn = new Element('button');
  const all = (el, pred) => [ ...(pred(el) ? [el] : []), ...el.children.flatMap(x => all(x, pred)) ];
  const find = (el, pred) => all(el, pred)[0];
  const picker = find(p.el, e => e.attrs['data-tier-picker']);
  const [model, , effort] = all(p.el, e => e.tag === 'select' && !e.attrs['data-tier-picker']);
  const start = p.el.children.at(-1).children.at(-1);
  // launch clicks Start; the permission pre-flight re-reads the choices.
  const launch = () => {
    start.events.click({ target: start });
    if (pending.length) pending.shift()(200, { harnesses: hs, tiers });
    const sent = posts.shift();
    return sent && sent.body;
  };
  p.open('root', 'name', btn);
  pending.shift()(200, { harnesses: hs, tiers });

  // Reopened over a held list: the tier is picked before the fresh list
  // arrives, whose redraw would drop the off-catalog values.
  p.open('root', 'name', btn);
  picker.value = 'preview'; picker.events.change();
  assert.equal(model.value, 'gpt-x-preview');
  assert.equal(effort.value, 'xhigh');
  pending.shift()(200, { harnesses: hs, tiers });
  assert.equal(picker.value, 'preview', 'the tier stays picked');
  assert.equal(model.value, 'gpt-x-preview', 'the refresh re-applies the bundle model');
  assert.equal(effort.value, 'xhigh', 'the refresh re-applies the bundle effort');
  p.level = 'read-only';
  let body = launch();
  assert.equal(body.harness, 'codex');
  assert.equal(body.model, 'gpt-x-preview', 'the launch carries the bundle model, not a blank');
  assert.equal(body.effort, 'xhigh');

  // Backstop: fields that disagree with the shown tier are never sent; the
  // tier is put back first.
  p.open('root', 'name', btn);
  pending.shift()(200, { harnesses: hs, tiers });
  picker.value = 'preview'; picker.events.change();
  model.value = ''; effort.value = '';
  p.level = 'read-only';
  body = launch();
  assert.equal(body.model, 'gpt-x-preview');
  assert.equal(body.effort, 'xhigh');

  // The bundle vanished from the refreshed list: back to manual, and what
  // the form shows is what it sends.
  p.open('root', 'name', btn);
  picker.value = 'preview'; picker.events.change();
  pending.shift()(200, { harnesses: hs, tiers: [] });
  assert.equal(picker.value, '', 'a vanished tier returns the form to manual');
  assert.equal(model.value, '');
  assert.equal(effort.value, '');
  p.level = 'read-only';
  body = launch();
  assert.equal(body.model, model.value);
  assert.equal(body.effort, effort.value);
});

test('the full-permission grant launches the request the warning named, even after a refresh re-applies the tier elsewhere', () => {
  class Element {
    constructor(tag) { this.tag = tag; this.children = []; this.attrs = {}; this.events = {}; this.classList = { toggle() {} }; this._value = ''; }
    appendChild(c) { this.children.push(c); return c; }
    insertBefore(c) { if (!this.children.includes(c)) this.children.push(c); return c; }
    set textContent(v) { this.children = []; this.text = v; }
    get textContent() { return this.text || ''; }
    set value(v) { this._value = v; }
    get value() { return this._value || (this.tag === 'select' && this.children[0] ? this.children[0].value : ''); }
    setAttribute(k, v) { this.attrs[k] = v; }
    getAttribute(k) { return this.attrs[k] || ''; }
    addEventListener(k, f) { this.events[k] = f; }
    focus() {}
    showModal() { this.open = true; }
    close() { this.open = false; }
  }
  const pending = [], posts = [], dialogs = [];
  const levels = ['read-only', 'workdir-write', 'full'];
  const hs = [
    { harness: 'claude', lead: true, available: true, models: [{ id: 'opus' }, { id: 'sonnet' }], efforts: ['high', 'max'], permission_modes: ['auto', 'plan'], permission_levels: levels },
    { harness: 'codex', lead: true, available: true, models: [{ id: 'gpt-5' }], efforts: ['low', 'high'], permission_modes: ['read-only'], permission_levels: levels }
  ];
  const onCodex = [{ name: 'name', trace: 'name', harness: 'codex', model: 'gpt-5', effort: 'low' }];
  const onClaude = [{ name: 'name', trace: 'name', harness: 'claude', model: 'sonnet', effort: 'max' }];
  const c = { mode: 'index', root: { hasAttribute: () => false }, supChoices: null,
    localStorage: { getItem: () => null, setItem() {} }, sessionStorage: { setItem() {} }, RENAME_NEW: 'rename',
    doc: { body: { appendChild(e) { e.parentNode = this; dialogs.push(e); return e; } } },
    supHarness(k) { return c.supChoices && c.supChoices.harnesses.find(h => h.harness === k); },
    tkFetch(path, opt, cb) { pending.push(cb); }, wsT: (k, ...a) => [k, ...a].join('|'), mk: (tag) => new Element(tag), rpPop: () => new Element('div'), rpBtn: () => new Element('button'), rpId: () => 'id',
    gIcon: () => new Element('icon'), rpLive: () => true, railPopPlace() {}, railPopClose() {}, railOpen() {}, saveDraft() {}, notifAsk() {}, go() {}, toast() {},
    WsRail: { leadBody: (root, v) => v }, railPost(path, body, cb) { posts.push({ path, body, cb }); }
  };
  vm.createContext(c);
  const source = fs.readFileSync(__dirname + '/assets/dashboard-leads.js', 'utf8');
  vm.runInContext(fs.readFileSync(__dirname + '/assets/dashboard-tier-picker.js', 'utf8') + source.slice(0, source.indexOf('  // The session menu of the viewed hosted session')), c);
  const p = c.rpLeadMake(), btn = new Element('button');
  const all = (el, pred) => [ ...(pred(el) ? [el] : []), ...el.children.flatMap(x => all(x, pred)) ];
  const find = (el, pred) => all(el, pred)[0];
  const texts = (el) => [el.text || '', ...el.children.flatMap(texts)].join(' ');
  const picker = find(p.el, e => e.attrs['data-tier-picker']);
  const [model, , effort] = all(p.el, e => e.tag === 'select' && !e.attrs['data-tier-picker']);
  const start = p.el.children.at(-1).children.at(-1);
  p.open('root', 'name', btn);
  pending.shift()(200, { harnesses: hs, tiers: onCodex });

  // Reopened over the held list: /support/choices is still out while the
  // tier is picked on codex and Start opens the full warning.
  p.open('root', 'name', btn);
  picker.value = 'name'; picker.events.change();
  assert.equal(p.harness, 'codex');
  p.level = 'full';
  start.events.click({ target: start });
  const dlg = dialogs[0];
  assert.ok(dlg && dlg.open, 'the full warning is open');
  assert.match(texts(dlg), /name · codex/, 'the warning names codex');
  assert.equal(posts.length, 0);

  // The held read lands: the same bundle now maps to claude, and the
  // refresh re-applies it while the warning stays open.
  pending.shift()(200, { harnesses: hs, tiers: onClaude });
  assert.equal(p.harness, 'claude', 'the refresh re-applied the tier on claude');
  assert.equal(model.value, 'sonnet');

  // Tick and grant: the launch is the one the warning named.
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
  assert.equal(effort.value, 'max', 'the form itself still shows the refreshed tier');
});

test('a choices read after a stream reconnect re-applies the picked tier, so its note and the fields agree', () => {
  class Element {
    constructor(tag) { this.tag = tag; this.children = []; this.attrs = {}; this.events = {}; this.classList = { toggle() {} }; this._value = ''; }
    appendChild(c) { this.children.push(c); return c; }
    insertBefore(c) { if (!this.children.includes(c)) this.children.push(c); return c; }
    set textContent(v) { this.children = []; this.text = v; }
    get textContent() { return this.text || ''; }
    set value(v) { this._value = v; }
    get value() { return this._value || (this.tag === 'select' && this.children[0] ? this.children[0].value : ''); }
    setAttribute(k, v) { this.attrs[k] = v; }
    getAttribute(k) { return this.attrs[k] || ''; }
    addEventListener(k, f) { this.events[k] = f; }
    focus() {}
  }
  const pending = [];
  const hs = [
    { harness: 'claude', lead: true, available: true, models: [{ id: 'opus' }], efforts: ['high'], permission_modes: ['auto'], permission_levels: ['read-only'] },
    { harness: 'codex', lead: true, available: true, models: [{ id: 'gpt-5' }, { id: 'gpt-5-mini' }], efforts: ['low', 'high'], permission_modes: ['read-only'], permission_levels: ['read-only'] }
  ];
  const before = [{ name: 'deep', trace: 'deep', harness: 'codex', model: 'gpt-5', effort: 'high' }];
  const after = [{ name: 'deep', trace: 'deep', harness: 'codex', model: 'gpt-5-mini', effort: 'low' }];
  const c = { mode: 'index', root: { hasAttribute: () => false }, supChoices: null,
    localStorage: { getItem: () => null, setItem() {} }, sessionStorage: { setItem() {} }, RENAME_NEW: 'rename',
    supHarness(k) { return c.supChoices && c.supChoices.harnesses.find(h => h.harness === k); },
    tkFetch(path, opt, cb) { pending.push(cb); }, wsT: (k, ...a) => [k, ...a].join('|'), mk: (tag) => new Element(tag), rpPop: () => new Element('div'), rpBtn: () => new Element('button'), rpId: () => 'id',
    gIcon: () => new Element('icon'), rpLive: () => true, railPopPlace() {}, railPopClose() {}, railOpen() {}, saveDraft() {}, notifAsk() {}, go() {}, toast() {},
    WsRail: { leadBody: (root, v) => v }, railPost() {}
  };
  vm.createContext(c);
  const source = fs.readFileSync(__dirname + '/assets/dashboard-leads.js', 'utf8');
  vm.runInContext(fs.readFileSync(__dirname + '/assets/dashboard-tier-picker.js', 'utf8') + source.slice(0, source.indexOf('  // The session menu of the viewed hosted session')), c);
  const p = c.rpLeadMake(), btn = new Element('button');
  const all = (el, pred) => [ ...(pred(el) ? [el] : []), ...el.children.flatMap(x => all(x, pred)) ];
  const find = (el, pred) => all(el, pred)[0];
  const picker = find(p.el, e => e.attrs['data-tier-picker']);
  const [model, , effort] = all(p.el, e => e.tag === 'select' && !e.attrs['data-tier-picker']);
  const note = () => find(p.el, e => /^js\.lead\.tier\.(picked|hint)/.test(e.text || '')).text;
  p.open('root', 'name', btn);
  pending.shift()(200, { harnesses: hs, tiers: before });
  picker.value = 'deep'; picker.events.change();
  assert.equal(model.value, 'gpt-5');

  // The stream came back; the re-read lists the bundle with new values.
  p.permSync(true);
  assert.equal(pending.length, 1, 'the reconnect re-reads the choices');
  pending.shift()(200, { harnesses: hs, tiers: after });
  assert.equal(picker.value, 'deep', 'the tier stays picked');
  assert.match(note(), /gpt-5-mini/, 'the note shows the refreshed bundle');
  assert.equal(model.value, 'gpt-5-mini', 'the model field follows the note');
  assert.equal(effort.value, 'low', 'the effort field follows the note');

  // A tier the re-read no longer lists returns the form to manual.
  p.permSync(true);
  pending.shift()(200, { harnesses: hs, tiers: [] });
  assert.equal(picker.value, '');
  assert.match(note(), /^js\.lead\.tier\.hint/);
});
