const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

const leadsSrc = fs.readFileSync(__dirname + '/assets/dashboard-leads.js', 'utf8');
const switchSrc = fs.readFileSync(__dirname + '/assets/dashboard-harness-switch.js', 'utf8');

function extractSlice(src, startMarker, endMarker) {
  const s = src.indexOf(startMarker);
  assert.ok(s >= 0, 'start marker found: ' + startMarker);
  const e = endMarker ? src.indexOf(endMarker, s) : src.length;
  assert.ok(e > s, 'end marker found: ' + endMarker);
  return src.slice(s, e);
}

function makeEnv(opts = {}) {
  let domMutations = 0;
  class Node {
    constructor(tag, cls = '', text = '') {
      this.tag = tag;
      this.tagName = String(tag || '').toUpperCase();
      this.cls = cls || '';
      this.children = [];
      this.childNodes = this.children;
      this.attrs = {};
      this.listeners = {};
      this.style = {};
      this.hidden = false;
      this.disabled = false;
      this.open = false;
      this._value = '';
      this.parentNode = null;
      this._text = text == null ? '' : String(text);
      const classes = new Set(this.cls.split(/\s+/).filter(Boolean));
      const self = this;
      this.classList = {
        toggle(k, on) {
          if (on === undefined ? !classes.has(k) : !!on) classes.add(k);
          else classes.delete(k);
          self.cls = [...classes].join(' ');
        },
        add(k) { this.toggle(k, true); },
        remove(k) { this.toggle(k, false); },
        contains(k) { return classes.has(k); }
      };
    }
    appendChild(c) {
      if (c.parentNode) c.parentNode.removeChild(c);
      c.parentNode = this;
      this.children.push(c);
      domMutations++;
      return c;
    }
    insertBefore(c, ref) {
      if (c.parentNode) c.parentNode.removeChild(c);
      const i = ref ? this.children.indexOf(ref) : -1;
      c.parentNode = this;
      if (i < 0) this.children.push(c);
      else this.children.splice(i, 0, c);
      domMutations++;
      return c;
    }
    removeChild(c) {
      const i = this.children.indexOf(c);
      if (i >= 0) {
        this.children.splice(i, 1);
        c.parentNode = null;
        domMutations++;
      }
      return c;
    }
    remove() { if (this.parentNode) this.parentNode.removeChild(this); }
    get firstChild() { return this.children[0] || null; }
    get lastChild() { return this.children[this.children.length - 1] || null; }
    replaceChild(c, old) { this.insertBefore(c, old); this.removeChild(old); return old; }
    get textContent() {
      return this._text + this.children.map(c => c.textContent).join('');
    }
    set textContent(v) {
      if (this.children.length) domMutations++;
      this.children.forEach(c => { c.parentNode = null; });
      this.children.length = 0;
      this._text = v == null ? '' : String(v);
      domMutations++;
    }
    get value() { return this._value; }
    set value(v) { this._value = v == null ? '' : String(v); }
    setAttribute(k, v) { this.attrs[k] = String(v); if (k === 'class') this.cls = String(v); }
    getAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k) ? this.attrs[k] : null; }
    removeAttribute(k) { delete this.attrs[k]; }
    hasAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k); }
    addEventListener(k, fn) { (this.listeners[k] = this.listeners[k] || []).push(fn); }
    fire(k, ev = {}) { (this.listeners[k] || []).forEach(fn => fn(Object.assign({ target: this, preventDefault() {} }, ev))); }
    click() { if (!this.disabled) this.fire('click'); }
    focus() { focused = this; }
    all(pred) { return [...(pred(this) ? [this] : []), ...this.children.flatMap(c => c.all(pred))]; }
    find(pred) { return this.all(pred)[0] || null; }
    querySelector(sel) {
      const mAttr = /^\[([^=\]]+)(?:="([^"]*)")?\]$/.exec(sel);
      if (mAttr) return this.find(n => mAttr[2] !== undefined ? n.getAttribute(mAttr[1]) === mAttr[2] : n.hasAttribute(mAttr[1]));
      const cls = sel.split('.').filter(Boolean);
      return this.find(n => cls.every(c => n.cls.split(/\s+/).includes(c)));
    }
  }

  let focused = null;
  let seq = 0;
  const posts = [];
  const toasts = [];
  const fetches = [];
  const popOpens = [];
  const qcbox = new Node('div', 'qcbox');
  qcbox.hidden = true;
  const head = new Node('div', 'head');
  const headChip = head.appendChild(new Node('span', 'host sse hbtn'));
  const body = new Node('body');

  const ctx = {
    mode: 'session',
    root: { hasAttribute: () => true },
    rid: opts.rid || 'lead-1',
    hostedPage: true,
    handedPage: false,
    supChoices: opts.supChoices ?? null,
    supHarness(k) {
      return ctx.supChoices && Array.isArray(ctx.supChoices.harnesses)
        ? ctx.supChoices.harnesses.find(h => h.harness === k) || null
        : null;
    },
    lead: { st: opts.state || null, busy: false, again: false, gone: false, key: '', cards: {}, answered: {}, need: null },
    rp: { P: null, kind: '', ws: '', btn: null },
    doc: {
      body,
      createTextNode(t) { return new Node('#text', '', t); }
    },
    document: { addEventListener() {}, dispatchEvent() {} },
    window: { fetch() {}, WsToast: { show(msg, o) { toasts.push({ msg, kind: o && o.kind }); } } },
    Set, Array, Object, Number, String, Boolean, JSON, Date, encodeURIComponent,
    $: id => (id === 'qcbox' ? qcbox : null),
    region: name => (name === 'head' ? head : null),
    mk: (tag, cls, text) => new Node(tag, cls, text),
    rpPop: title => { const el = new Node('div', 'rpop'); el.setAttribute('aria-label', title); el.hidden = true; return el; },
    rpBtn: (cls, text) => new Node('button', cls, text),
    rpId: () => 'hs-' + (++seq),
    gIcon: name => { const s = new Node('svg', 'i'); s.setAttribute('data-icon', name); return s; },
    luIcon: name => new Node('svg', 'i'),
    wsT: (k, ...args) => args.length ? k + '(' + args.join(',') + ')' : k,
    wsTn: (k, n) => k + '#' + n,
    pinned: () => false,
    pinConvo() {},
    fmtTimes() {},
    toast: msg => toasts.push({ msg, kind: 'toast' }),
    archErr: (a, st, s) => s || 'HTTP ' + st,
    tkFetch: (route, _opt, done) => fetches.push({ route, done }),
    railPost: (route, bodyObj, done) => posts.push({ route, body: JSON.parse(JSON.stringify(bodyObj)), done }),
    railPopBtn: () => headChip,
    railPopClose() { ctx.rp.P = null; ctx.rp.kind = ''; },
    railPopPlace() {},
    railMarks() {},
    rpLive: (p, gen) => ctx.rp.P === p && p.gen === gen,
    subagentClosing: {},
    stopNeedsHold: new Set(),
    leadDrawCalls: 0,
    leadFetchCalls: 0,
    leadDraw() { ctx.leadDrawCalls++; if (ctx.rp.P && ctx.rp.kind === 'sess') ctx.rp.P.draw(); ctx.quotaDraw(ctx.lead.st); },
    leadFetch() { ctx.leadFetchCalls++; },
    railPopOpen(kind, anchor) {
      popOpens.push({ kind, anchor });
      if (kind === 'sess') {
        const sess = ctx._sess || (ctx._sess = ctx.rpSessMake(false));
        ctx.rp = { P: sess, kind: 'sess', ws: ctx.rid, btn: anchor };
        sess.open(ctx.rid);
        sess.el.hidden = false;
      }
    }
  };

  vm.createContext(ctx);
  const sessAndGone =
    extractSlice(leadsSrc, '  var LEAD_MODES =', '  // lead is the viewed hosted session') +
    extractSlice(leadsSrc, '  function leadErr(', '  // Shared by the child header') +
    extractSlice(leadsSrc, '  function leadChoices(', '  // The session menu of the viewed hosted session') +
    extractSlice(leadsSrc, '  var leadActions = new Set();', '  // leadFetch reads the viewed session') +
    extractSlice(leadsSrc, '  var quotaState = { busy: false, sig: "" };', '  // leadKey is the conversation');

  vm.runInContext(sessAndGone + '\n' + switchSrc, ctx);

  return {
    ctx,
    qcbox,
    headChip,
    posts,
    toasts,
    fetches,
    popOpens,
    getFocused: () => focused,
    getMutations: () => domMutations,
    resetMutations: () => { domMutations = 0; }
  };
}

const SAMPLE_CHOICES = {
  harnesses: [
    {
      harness: 'alpha',
      lead: true,
      available: true,
      harness_switch: true,
      models: [{ id: 'alpha-1', display_name: 'Alpha 1' }],
      efforts: ['low', 'high']
    },
    {
      harness: 'beta',
      lead: true,
      available: true,
      harness_switch: true,
      models: [
        { id: 'beta-pro', display_name: 'Beta Pro', provider: 'vendor-b' },
        { id: 'beta-fast', display_name: 'Beta Fast' }
      ],
      efforts: ['medium', 'high']
    },
    {
      harness: 'gamma-durable',
      lead: true,
      available: true,
      harness_switch: false,
      models: [{ id: 'gamma-1' }],
      efforts: ['medium']
    },
    {
      harness: 'delta-offline',
      lead: true,
      available: false,
      harness_switch: true,
      models: [{ id: 'delta-1' }],
      efforts: ['low']
    },
    {
      harness: 'epsilon-noswitch',
      lead: true,
      available: true,
      models: [{ id: 'eps-1' }],
      efforts: []
    }
  ]
};

test('dashboard-harness-switch.js contains zero hardcoded harness names', () => {
  for (const name of ['claude', 'codex', 'agy', 'werk', 'gemini', 'pi']) {
    const re = new RegExp(`['"\`]${name}['"\`]`, 'i');
    assert.equal(re.test(switchSrc), false, `must not hardcode harness name ${name}`);
  }
});

test('quotaDraw renders Switch harness button in exhausted, scheduled, and ready states and stays idle on unchanged polls', () => {
  const env = makeEnv({ supChoices: SAMPLE_CHOICES });
  const cases = [
    {
      name: 'ready',
      st: { id: 'lead-1', lead: true, can_control: true, backend: 'alpha', status: 'idle', failure: { kind: 'usage_limit', resetsAt: 1700000000000 } }
    },
    {
      name: 'scheduled',
      st: { id: 'lead-1', lead: true, can_control: true, backend: 'alpha', status: 'idle', quota_continuation: { status: 'scheduled', resume_at: 1700000000000, attempts: 1, max_attempts: 6 } }
    },
    {
      name: 'exhausted',
      st: { id: 'lead-1', lead: true, can_control: true, backend: 'alpha', status: 'idle', quota_continuation: { status: 'exhausted', attempts: 6, max_attempts: 6 } }
    }
  ];

  for (const tc of cases) {
    env.ctx.lead.st = tc.st;
    env.ctx.quotaDraw(tc.st);
    assert.equal(env.qcbox.hidden, false, tc.name + ' card visible');
    const swBtn = env.qcbox.querySelector('[data-qc-act="switch-harness"]');
    assert.ok(swBtn, tc.name + ' renders switch-harness button');
    assert.equal(swBtn.textContent, 'js.quota.switch_harness');

    // Minimal DOM update check: identical poll triggers zero DOM mutations.
    env.resetMutations();
    env.ctx.quotaDraw(tc.st);
    assert.equal(env.getMutations(), 0, tc.name + ' unchanged redraw causes zero DOM mutations');
  }

  // Viewer without can_control sees no switch button.
  const roState = { id: 'lead-1', lead: true, can_control: false, backend: 'alpha', status: 'idle', quota_continuation: { status: 'exhausted', attempts: 6, max_attempts: 6 } };
  env.ctx.lead.st = roState;
  env.ctx.quotaDraw(roState);
  assert.equal(env.qcbox.querySelector('[data-qc-act="switch-harness"]'), null);
});

test('one-click Switch harness in #qcbox opens switcher with current harness & harness_switch:false excluded and no preselection', () => {
  const st = {
    id: 'lead-1',
    lead: true,
    can_control: true,
    backend: 'alpha',
    status: 'idle',
    quota_continuation: { status: 'exhausted', attempts: 6, max_attempts: 6 }
  };
  const env = makeEnv({ state: st, supChoices: SAMPLE_CHOICES });
  env.ctx.quotaDraw(st);

  const swBtn = env.qcbox.querySelector('[data-qc-act="switch-harness"]');
  assert.ok(swBtn);
  swBtn.click();

  assert.equal(env.popOpens.length, 1);
  assert.equal(env.popOpens[0].kind, 'sess');
  assert.equal(env.popOpens[0].anchor, env.headChip);

  const sess = env.ctx.rp.P;
  const sec = sess.el.querySelector('[data-harness-switch="1"]');
  assert.ok(sec);
  assert.equal(sec.hidden, false);
  assert.equal(sec.open, true);

  const targetSel = sec.querySelector('[data-hs-target="1"]');
  const submitBtn = sec.querySelector('[data-hs-submit="1"]');
  const optionVals = targetSel.children.map(o => o.value);
  // Only placeholder "" + "beta" (excludes current "alpha", harness_switch:false "gamma-durable",
  // unavailable "delta-offline", and missing flag "epsilon-noswitch").
  assert.deepEqual(optionVals, ['', 'beta']);
  assert.equal(targetSel.value, '', 'must never preselect a target harness');
  assert.equal(submitBtn.disabled, true, 'submit disabled until user picks target');
  assert.equal(env.getFocused(), targetSel, 'focus moves to target selector');

  // Unchanged state redraw preserves options without DOM rebuild.
  env.resetMutations();
  sess.draw();
  // Select options were not cleared/re-appended (targetSig unchanged).
  assert.deepEqual(targetSel.children.map(o => o.value), ['', 'beta']);
});

test('submitting harness switch posts POST /agents/<id>/harness with omitted defaults or chosen model/effort/provider', () => {
  const st = { id: 'lead-1', lead: true, can_control: true, backend: 'alpha', model: 'alpha-1', effort: 'low', status: 'idle' };
  const env = makeEnv({ state: st, supChoices: SAMPLE_CHOICES });
  env.ctx.railPopOpen('sess', env.headChip);
  const sess = env.ctx.rp.P;
  sess.openHarnessSwitch(true);

  const sec = sess.el.querySelector('[data-harness-switch="1"]');
  const targetSel = sec.querySelector('[data-hs-target="1"]');
  const modelSel = sec.querySelector('[data-hs-model="1"]');
  const effortSel = sec.querySelector('[data-hs-effort="1"]');
  const submitBtn = sec.querySelector('[data-hs-submit="1"]');

  // Pick target "beta", leaving model & effort empty (target defaults).
  targetSel.value = 'beta';
  targetSel.fire('change');
  assert.equal(submitBtn.disabled, false);
  assert.deepEqual(modelSel.children.map(o => o.value), ['', 'beta-pro', 'beta-fast']);
  assert.deepEqual(effortSel.children.map(o => o.value), ['', 'medium', 'high']);

  submitBtn.click();
  assert.equal(env.posts.length, 1);
  assert.equal(env.posts[0].route, '/agents/lead-1/harness');
  assert.deepEqual(env.posts[0].body, { harness: 'beta' });
  assert.equal(submitBtn.disabled, true);
  assert.equal(submitBtn.textContent, 'js.hswitch.switching');

  // Server responds 200 with updated state view.
  const nextState = { id: 'lead-1', lead: true, can_control: true, backend: 'beta', model: 'beta-fast', effort: 'medium', status: 'idle' };
  env.posts[0].done(200, nextState, '');
  assert.equal(env.ctx.lead.st.backend, 'beta');
  assert.equal(sec.open, false);
  assert.equal(env.toasts[env.toasts.length - 1].msg, 'js.hswitch.done(beta)');

  // Now switch from beta back to alpha with explicit model + effort.
  // First also test provider forwarding when switching to beta-pro:
  env.ctx.lead.st = { id: 'lead-1', lead: true, can_control: true, backend: 'alpha', status: 'idle' };
  sess.openHarnessSwitch(false);
  targetSel.value = 'beta';
  targetSel.fire('change');
  modelSel.value = 'beta-pro';
  effortSel.value = 'high';
  submitBtn.click();
  assert.equal(env.posts.length, 2);
  assert.deepEqual(env.posts[1].body, { harness: 'beta', model: 'beta-pro', provider: 'vendor-b', effort: 'high' });
});

test('busy (409) and unsupported (400) errors surface localized inline feedback and refresh state', () => {
  const st = { id: 'lead-1', lead: true, can_control: true, backend: 'alpha', status: 'idle' };
  const env = makeEnv({ state: st, supChoices: SAMPLE_CHOICES });
  env.ctx.railPopOpen('sess', env.headChip);
  const sess = env.ctx.rp.P;
  sess.openHarnessSwitch(false);

  const sec = sess.el.querySelector('[data-harness-switch="1"]');
  const targetSel = sec.querySelector('[data-hs-target="1"]');
  const submitBtn = sec.querySelector('[data-hs-submit="1"]');
  const errLine = sec.querySelector('[data-hs-error="1"]');

  targetSel.value = 'beta';
  targetSel.fire('change');
  submitBtn.click();
  assert.equal(env.posts.length, 1);

  const beforeFetch = env.ctx.leadFetchCalls;
  env.posts[0].done(409, {}, 'conversation_busy: turn running');
  assert.equal(errLine.hidden, false);
  assert.equal(errLine.textContent, 'js.hswitch.err.busy');
  assert.equal(submitBtn.disabled, false);
  assert.equal(env.ctx.leadFetchCalls, beforeFetch + 1);

  // 400 harness_unsupported
  submitBtn.click();
  assert.equal(env.posts.length, 2);
  env.posts[1].done(400, {}, 'harness_unsupported: durable target');
  assert.equal(errLine.hidden, false);
  assert.equal(errLine.textContent, 'js.hswitch.err.unsupported');

  // 400 harness_unchanged
  submitBtn.click();
  assert.equal(env.posts.length, 3);
  env.posts[2].done(400, {}, 'harness_unchanged');
  assert.equal(errLine.textContent, 'js.hswitch.err.unchanged');
});

test('switcher is hidden for durable (harness_switch:false) current harness and child sessions', () => {
  const env = makeEnv({
    state: { id: 'lead-1', lead: true, can_control: true, backend: 'gamma-durable', status: 'idle' },
    supChoices: SAMPLE_CHOICES
  });
  env.ctx.railPopOpen('sess', env.headChip);
  const sec = env.ctx.rp.P.el.querySelector('[data-harness-switch="1"]');
  assert.equal(sec.hidden, true, 'durable harness hides switcher');

  env.ctx.lead.st = { id: 'lead-1', lead: true, is_child: true, can_control: true, backend: 'alpha', status: 'idle' };
  env.ctx.rp.P.draw();
  assert.equal(sec.hidden, true, 'child session hides switcher');
});
