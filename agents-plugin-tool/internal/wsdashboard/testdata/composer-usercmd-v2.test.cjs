const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

const EN = JSON.parse(fs.readFileSync(path.resolve(__dirname, '../../wsi18n/tables/en.json'), 'utf8'));

function jsonRes(obj, status = 200) {
  return Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(obj),
    text: () => Promise.resolve(JSON.stringify(obj))
  });
}

function makeUcmdHarness(initialFetchImpl) {
  const timers = new Map();
  let nextTimerId = 1;
  let now = 10000;
  const window = new EventTarget();
  const storageMap = new Map();
  const windowStorage = {
    getItem(k) { return storageMap.has(k) ? storageMap.get(k) : null; },
    setItem(k, v) { storageMap.set(k, String(v)); },
    removeItem(k) { storageMap.delete(k); }
  };

  function makeClassList(initial = '') {
    const set = new Set(initial.split(/\s+/).filter(Boolean));
    return {
      add(...xs) { xs.forEach(x => x && set.add(x)); },
      remove(...xs) { xs.forEach(x => set.delete(x)); },
      toggle(x, on) {
        const next = on === undefined ? !set.has(x) : !!on;
        if (next) set.add(x); else set.delete(x);
        return next;
      },
      contains(x) { return set.has(x); },
      toString() { return [...set].join(' '); }
    };
  }

  let activeElement = null;
  const document = Object.assign(new EventTarget(), { hidden: false });
  Object.defineProperty(document, 'activeElement', {
    get() { return activeElement; },
    set(el) { activeElement = el; }
  });

  function matchSimple(el, sel) {
    sel = sel.trim();
    if (!sel) return false;
    // Split selector like ".ucmd-ibtn.is-hold" or '[data-f="label"]' or 'button.foo'
    const attrMatches = [...sel.matchAll(/\[([^\]=]+)(?:="([^"]*)")?\]/g)];
    for (const m of attrMatches) {
      const k = m[1], v = m[2];
      if (!el.hasAttribute(k)) return false;
      if (v !== undefined && el.getAttribute(k) !== v) return false;
    }
    const stripped = sel.replace(/\[[^\]]+\]/g, '');
    const parts = stripped.split(/(?=[.#])/).filter(Boolean);
    for (const p of parts) {
      if (p.startsWith('.')) {
        if (!el.classList.contains(p.slice(1))) return false;
      } else if (p.startsWith('#')) {
        if (el.id !== p.slice(1)) return false;
      } else {
        if (el.tagName.toLowerCase() !== p.toLowerCase()) return false;
      }
    }
    return true;
  }

  function matchesSelector(el, sel) {
    return sel.split(',').some(branch => {
      const tokens = branch.trim().split(/\s+/).filter(Boolean);
      if (!tokens.length) return false;
      if (!matchSimple(el, tokens[tokens.length - 1])) return false;
      let cur = el.parentNode;
      for (let i = tokens.length - 2; i >= 0; i--) {
        let found = false;
        while (cur) {
          if (cur.tagName && matchSimple(cur, tokens[i])) { found = true; cur = cur.parentNode; break; }
          cur = cur.parentNode;
        }
        if (!found) return false;
      }
      return true;
    });
  }

  function makeEl(tag, cls = '') {
    const attrs = {};
    const styles = {};
    let text = '';
    let value = '';
    const el = Object.assign(new EventTarget(), {
      tagName: tag.toUpperCase(),
      id: '',
      type: '',
      title: '',
      placeholder: '',
      disabled: false,
      hidden: false,
      isConnected: true,
      parentNode: null,
      children: [],
      classList: makeClassList(cls),
      style: {
        setProperty(k, v) { styles[k] = String(v); },
        removeProperty(k) { delete styles[k]; },
        getPropertyValue(k) { return styles[k] || ''; }
      },
      setAttribute(k, v) {
        attrs[k] = String(v);
        if (k === 'id') this.id = String(v);
      },
      getAttribute(k) { return k in attrs ? attrs[k] : null; },
      removeAttribute(k) { delete attrs[k]; },
      hasAttribute(k) { return k in attrs; },
      focus() { activeElement = this; },
      appendChild(child) {
        if (child.parentNode) child.parentNode.removeChild(child);
        child.parentNode = this;
        this.children.push(child);
        return child;
      },
      insertBefore(child, ref) {
        if (!ref) return this.appendChild(child);
        if (child.parentNode) child.parentNode.removeChild(child);
        const idx = this.children.indexOf(ref);
        if (idx < 0) return this.appendChild(child);
        child.parentNode = this;
        this.children.splice(idx, 0, child);
        return child;
      },
      removeChild(child) {
        const idx = this.children.indexOf(child);
        if (idx >= 0) { this.children.splice(idx, 1); child.parentNode = null; }
        return child;
      },
      contains(other) {
        let cur = other;
        while (cur) { if (cur === this) return true; cur = cur.parentNode; }
        return false;
      },
      matches(sel) { return matchesSelector(this, sel); },
      closest(sel) {
        let cur = this;
        while (cur) { if (cur.tagName && matchesSelector(cur, sel)) return cur; cur = cur.parentNode; }
        return null;
      },
      querySelectorAll(sel) {
        const out = [];
        const walk = node => {
          for (const c of node.children) {
            if (matchesSelector(c, sel)) out.push(c);
            walk(c);
          }
        };
        walk(this);
        return out;
      },
      querySelector(sel) { return this.querySelectorAll(sel)[0] || null; },
      getBoundingClientRect() { return { left: 10, top: 10, right: 36, bottom: 36, width: 26, height: 26 }; }
    });
    Object.defineProperties(el, {
      className: {
        get() { return this.classList.toString(); },
        set(v) { this.classList = makeClassList(String(v || '')); }
      },
      nextSibling: {
        get() {
          if (!this.parentNode) return null;
          const i = this.parentNode.children.indexOf(this);
          return i >= 0 ? (this.parentNode.children[i + 1] || null) : null;
        }
      },
      textContent: {
        get() { return this.children.length ? this.children.map(c => c.textContent).join('') : text; },
        set(v) { this.children.length = 0; text = String(v ?? ''); }
      },
      value: {
        get() { return value; },
        set(v) {
          const s = String(v ?? '');
          // Emulate real HTML <input type="text"> stripping newlines!
          value = (this.tagName === 'INPUT') ? s.replace(/[\r\n]/g, '') : s;
        }
      }
    });
    return el;
  }

  document.createElement = tag => makeEl(tag);
  document.createElementNS = (_ns, tag) => makeEl(tag);

  // Build #composer DOM matching dashboard.tmpl
  const root = makeEl('div', 'root');
  const composer = root.appendChild(makeEl('div', 'composer'));
  composer.id = 'composer';
  composer.setAttribute('data-cexec', '1');
  const cbadges = composer.appendChild(makeEl('div', 'cbadges'));
  const sbwrap = cbadges.appendChild(makeEl('span', 'sbwrap'));
  const sbadge = sbwrap.appendChild(makeEl('button', 'sbadge'));
  sbadge.id = 'sbadge';
  const bgbadge = sbwrap.appendChild(makeEl('span', 'bgbadge'));
  bgbadge.id = 'bgbadge';
  const ctext = composer.appendChild(makeEl('textarea'));
  ctext.id = 'ctext';
  ctext.value = 'unsent composer draft';

  const byId = id => root.querySelector('#' + id);
  document.getElementById = byId;

  const fakeSetTimeout = (fn, ms) => {
    const id = nextTimerId++;
    timers.set(id, { id, fn, due: now + (ms || 0) });
    return id;
  };
  const fakeClearTimeout = id => timers.delete(id);
  const advance = ms => {
    now += ms;
    while (true) {
      let next = null;
      for (const t of timers.values()) {
        if (t.due <= now && (!next || t.due < next.due)) next = t;
      }
      if (!next) break;
      timers.delete(next.id);
      next.fn();
    }
  };
  const flushMicrotasks = () => new Promise(r => setImmediate(r));

  const warns = [], oks = [], posts = [], tracked = [], observed = [];
  let hudLayouts = 0, bangSyncs = 0, nextSubId = 1;
  let fetchHandler = initialFetchImpl;

  window.sessionStorage = windowStorage;
  window.setTimeout = fakeSetTimeout;
  window.clearTimeout = fakeClearTimeout;

  const ctx = vm.createContext({
    window,
    document,
    doc: document,
    Date: Object.assign(function FakeDate() { return { getTimezoneOffset: () => -540 }; }, { now: () => now }),
    setTimeout: fakeSetTimeout,
    clearTimeout: fakeClearTimeout,
    encodeURIComponent,
    Object,
    Array,
    JSON,
    Math,
    String,
    Number,
    Boolean,
    parseInt,
    $: byId,
    rid: 'sess1',
    composer,
    ctext,
    cexecEnabled: true,
    cexecToken: 'tok1',
    cexecNewID: () => 'sub-' + (nextSubId++),
    cexecTrackSubmission: (id, line, body, origin) => tracked.push({ id, line, body, origin }),
    cexecUnreachable: why => 'unreachable: ' + why,
    cexecList: cb => cb([], ''),
    bangHistSyncBinding: () => bangSyncs++,
    xhRo: { observe: el => observed.push(el) },
    xhLayout: () => hudLayouts++,
    wsT: (k, ...args) => {
      let s = EN[k] || k;
      args.forEach((a, i) => { s = s.replace('{' + i + '}', String(a)); });
      return s;
    },
    noteWarn: m => warns.push(m),
    noteOk: m => oks.push(m),
    post: (route, body, done) => {
      posts.push({ route, body });
      done(true, { ok: true });
    },
    fetch: (url, opts) => fetchHandler(String(url), opts || {})
  });

  vm.runInContext(readAssetSource('wsdashboard', 'hold-confirm.js'), ctx);
  vm.runInContext(readAssetSource('wsdashboard', 'dashboard-composer-ucmd.js'), ctx);

  function fire(target, type, props = {}) {
    const ev = Object.assign(new Event(type, { bubbles: true, cancelable: true }), props);
    Object.defineProperty(ev, 'target', { value: target, configurable: true });
    // Dispatch up the parent chain manually so delegated listeners on ancestors receive it.
    let cur = target;
    let stopped = false;
    const origStop = ev.stopPropagation.bind(ev);
    ev.stopPropagation = () => { stopped = true; origStop(); };
    const origStopImm = ev.stopImmediatePropagation.bind(ev);
    ev.stopImmediatePropagation = () => { stopped = true; origStopImm(); };
    while (cur && !stopped) {
      cur.dispatchEvent(ev);
      cur = cur.parentNode;
    }
    if (!stopped && target !== document && target !== window) {
      document.dispatchEvent(ev);
      if (!stopped) window.dispatchEvent(ev);
    }
    return ev;
  }

  function jsonRes(obj, status = 200) {
    return Promise.resolve({
      ok: status >= 200 && status < 300,
      status,
      json: () => Promise.resolve(obj),
      text: () => Promise.resolve(JSON.stringify(obj))
    });
  }

  function holdConfirm(btn, holdMs = 1000, opts = {}) {
    fire(btn, 'pointerdown', { button: 0, isPrimary: true, pointerId: 1 });
    advance(holdMs);
    if (opts.leave) {
      fire(window, 'pointermove', { pointerId: 1, clientX: 500, clientY: 500 });
    } else if (opts.escape) {
      fire(window, 'keydown', { key: 'Escape' });
    } else {
      fire(window, 'pointerup', { pointerId: 1, clientX: 20, clientY: 20 });
    }
  }

  return {
    ctx, window, document, composer, sbwrap, sbadge, ctext,
    warns, oks, posts, tracked, observed,
    get hudLayouts() { return hudLayouts; },
    get bangSyncs() { return bangSyncs; },
    setFetch(fn) { fetchHandler = fn; },
    jsonRes, advance, flushMicrotasks, fire, holdConfirm
  };
}

test('trigger button mounts right after #sbadge, stays visible at 0 commands, hides on 403', async () => {
  const calls = [];
  const h = makeUcmdHarness((url, opts) => {
    calls.push({ url, method: opts.method || 'GET' });
    return jsonRes({ rev: 'e1.1.r1', token: 'tok1', commands: [] });
  });
  await h.flushMicrotasks();

  const badge = h.document.getElementById('ucmdbadge');
  assert(badge, '#ucmdbadge created');
  assert.equal(h.sbadge.nextSibling, badge, 'inserted right after #sbadge');
  assert.equal(badge.hidden, false, 'always visible even at 0 commands');
  assert.equal(badge.classList.contains('is-empty'), true);
  assert.equal(h.observed.includes(badge), true, 'registered with xhRo');
  assert.ok(h.hudLayouts >= 1, 'triggers xhLayout');

  // Opening popover at 0 commands shows empty state.
  h.fire(badge, 'click');
  const pop = h.composer.querySelector('.ucmd-pop');
  assert.equal(pop.hidden, false);
  assert.equal(h.composer.querySelector('.ucmd-empty').hidden, false);
  assert.equal(h.composer.querySelector('.ucmd-list').hidden, true);

  // Denied fetch (403) hides trigger and closes popover.
  h.setFetch(() => Promise.resolve({ ok: false, status: 403, json: () => Promise.resolve({}), text: () => Promise.resolve('forbidden') }));
  h.ctx.ucmdFetch();
  await h.flushMicrotasks();
  assert.equal(badge.hidden, true);
  assert.equal(pop.hidden, true);
});

test('dual-scope same-ID coexistence, inline editing with zero focus loss, Enter guard, revert, and save PUT', async () => {
  const reqs = [];
  const baseCommands = [
    { id: 'build', source: 'agent', label: 'Agent Build', line: 'go build ./...', prompt: 'Check build', cwd: 'backend', revision: 'r1' },
    { id: 'build', source: 'user', label: 'My Build', line: 'make all\nmake check', prompt: '', cwd: '', revision: 'r2' }
  ];
  const h = makeUcmdHarness((url, opts) => {
    const method = opts.method || 'GET';
    const body = opts.body ? JSON.parse(opts.body) : null;
    reqs.push({ url, method, body });
    if (method === 'GET') return jsonRes({ rev: 'e1.1.r1', token: 'tok1', commands: baseCommands });
    if (method === 'PUT') {
      const updated = baseCommands.map(c =>
        c.id === 'build' && c.source === body.source
          ? Object.assign({}, c, { line: body.line, prompt: body.prompt, revision: 'r3' })
          : c
      );
      return jsonRes({ rev: 'e1.2.r1', token: 'tok1', commands: updated });
    }
    return jsonRes({}, 404);
  });
  await h.flushMicrotasks();

  const badge = h.document.getElementById('ucmdbadge');
  assert.equal(badge.classList.contains('is-empty'), false);
  assert.equal(badge.querySelector('.ucmd-trigger-cnt').textContent, '2');
  h.fire(badge, 'click');

  const cards = h.composer.querySelectorAll('.ucmd-card');
  assert.equal(cards.length, 2, 'both agent:build and user:build coexist');
  assert.equal(cards[0].getAttribute('data-ucmd-key'), 'agent:build');
  assert.equal(cards[1].getAttribute('data-ucmd-key'), 'user:build');

  // Multiline user command uses textarea and preserves newline.
  const userCmdIn = cards[1].querySelector('.ucmd-inline-input.is-cmd');
  assert.equal(userCmdIn.tagName, 'TEXTAREA');
  assert.equal(userCmdIn.value, 'make all\nmake check');

  // Inline edit on agent:build keeps focus and toggles dirty in place without DOM rebuild.
  const agentCard = cards[0];
  const cmdIn = agentCard.querySelector('.ucmd-inline-input.is-cmd');
  const prIn = agentCard.querySelector('.ucmd-inline-input.is-prompt');
  cmdIn.focus();
  cmdIn.value = 'go build -race ./...';
  h.fire(cmdIn, 'input');
  assert.equal(h.document.activeElement, cmdIn, 'focus preserved on input');
  assert.equal(agentCard.classList.contains('is-dirty'), true);
  assert.equal(agentCard.querySelector('.ucmd-inline-bar').classList.contains('is-dirty'), true);
  assert.equal(agentCard.querySelector('.ucmd-rev-btn').disabled, false);

  // Incoming SSE refresh preserves active input focus and unsaved draft!
  h.ctx.ucmdApply({ rev: 'e1.1.r1', token: 'tok1', commands: baseCommands }, false);
  assert.equal(h.composer.querySelectorAll('.ucmd-card')[0], agentCard, 'same DOM card node reused');
  assert.equal(h.document.activeElement, cmdIn, 'focus preserved across SSE update');
  assert.equal(cmdIn.value, 'go build -race ./...', 'unsaved draft preserved across SSE update');

  // Enter on Line 2 never runs; focuses ▶ button and shows reminder toast.
  const enterEv = h.fire(cmdIn, 'keydown', { key: 'Enter' });
  assert.equal(enterEv.defaultPrevented, true);
  assert.equal(h.document.activeElement, agentCard.querySelector('.ucmd-ibtn-run'));
  assert.equal(h.warns.at(-1), EN['js.ucmd.enter.hint']);
  assert.equal(reqs.filter(r => r.url.endsWith('/run')).length, 0);

  // Enter on Line 3 never asks; focuses ↗ button and shows reminder toast.
  prIn.focus();
  const enterPr = h.fire(prIn, 'keydown', { key: 'Enter' });
  assert.equal(enterPr.defaultPrevented, true);
  assert.equal(h.document.activeElement, agentCard.querySelector('.ucmd-ibtn-ask'));
  assert.equal(h.posts.length, 0);

  // Single-click ↺ reverts command and clears dirty.
  h.fire(agentCard.querySelector('.ucmd-rev-btn'), 'click');
  assert.equal(cmdIn.value, 'go build ./...');
  assert.equal(agentCard.classList.contains('is-dirty'), false);

  // Edit again and click 💾 -> PUT with source, revision, line, prompt.
  cmdIn.value = 'go build -v ./...';
  h.fire(cmdIn, 'input');
  prIn.value = 'Check verbose build';
  h.fire(prIn, 'input');
  h.fire(agentCard.querySelector('.ucmd-ibtn-save'), 'click');
  await h.flushMicrotasks();

  const put = reqs.find(r => r.method === 'PUT');
  assert(put, 'PUT request sent');
  assert.equal(put.url, '/s/sess1/user-commands/build');
  assert.deepEqual(put.body, {
    source: 'agent',
    revision: 'r1',
    line: 'go build -v ./...',
    prompt: 'Check verbose build'
  });
  assert.equal(agentCard.classList.contains('is-dirty'), false, 'dirty cleared after save');
});

test('1s hold confirmation on Run, Ask Agent (preserving #composer draft), Delete, and Escape/early-release safety', async () => {
  const reqs = [];
  const cmds = [
    { id: 'test', source: 'agent', label: 'Run Tests', line: 'go test ./...', prompt: 'Check failures', cwd: 'pkg', revision: 'r1' }
  ];
  const h = makeUcmdHarness((url, opts) => {
    const method = opts.method || 'GET';
    const body = opts.body ? JSON.parse(opts.body) : null;
    reqs.push({ url, method, body });
    if (method === 'GET') return jsonRes({ rev: 'e1.1.r1', token: 'tok1', commands: cmds });
    if (url.endsWith('/run')) return jsonRes({ id: body.submission_id, command: body.line || 'go test ./...', exec_id: 'ex1', token: 'tok1' });
    if (url.endsWith('/remove')) return jsonRes({ rev: 'e1.2.r1', token: 'tok1', commands: [] });
    return jsonRes({}, 404);
  });
  await h.flushMicrotasks();

  const badge = h.document.getElementById('ucmdbadge');
  const pop = h.composer.querySelector('.ucmd-pop');
  h.fire(badge, 'click');
  const card = h.composer.querySelector('.ucmd-card');
  const runBtn = card.querySelector('.ucmd-ibtn-run');
  const askBtn = card.querySelector('.ucmd-ibtn-ask');
  const delBtn = card.querySelector('.ucmd-ibtn-del');

  // 1) Early release (<1000ms) on ▶ cancels and emits quick-click reminder toast; nothing runs.
  h.holdConfirm(runBtn, 400);
  assert.equal(reqs.filter(r => r.url.endsWith('/run')).length, 0);
  assert.equal(h.warns.at(-1), EN['js.ucmd.hold.early.run']);

  // 2) Escape during active hold cancels ONLY the hold, keeping popover open!
  h.holdConfirm(runBtn, 600, { escape: true });
  assert.equal(pop.hidden, false, 'popover stays open when Escape cancels a hold');
  assert.equal(reqs.filter(r => r.url.endsWith('/run')).length, 0);

  // 3) Full 1000ms hold on ↗ sends immediate structured POST /message WITHOUT touching #composer draft.
  const cmdIn = card.querySelector('.ucmd-inline-input.is-cmd');
  cmdIn.value = 'go test -race ./...';
  h.fire(cmdIn, 'input');
  h.holdConfirm(askBtn, 1000);
  await h.flushMicrotasks();
  assert.equal(h.posts.length, 1);
  assert.equal(h.posts[0].route, 'message');
  assert.match(h.posts[0].body.text, /Run Tests/);
  assert.match(h.posts[0].body.text, /go test -race \.\//);
  assert.match(h.posts[0].body.text, /Check failures/);
  assert.match(h.posts[0].body.text, /cwd: pkg/);
  assert.equal(h.ctext.value, 'unsent composer draft', '#composer draft untouched');
  assert.equal(pop.hidden, true, 'popover closes after ask send');

  // 4) Reopen and full 1000ms hold on ▶ sends edited line override + source to /run and tracks ucmd origin.
  h.fire(badge, 'click');
  h.holdConfirm(runBtn, 1000);
  await h.flushMicrotasks();
  const runReq = reqs.find(r => r.url.endsWith('/run'));
  assert(runReq, 'run POST sent');
  assert.equal(runReq.body.source, 'agent');
  assert.equal(runReq.body.line, 'go test -race ./...');
  assert.equal(h.tracked.length, 1);
  assert.equal(h.tracked[0].origin, 'ucmd');
  assert.equal(pop.hidden, true, 'popover closes after run');

  // 5) After guard expires, reopen and full 1000ms hold on × deletes command.
  h.advance(700);
  h.fire(badge, 'click');
  h.holdConfirm(delBtn, 1000);
  await h.flushMicrotasks();
  const delReq = reqs.find(r => r.url.endsWith('/remove'));
  assert(delReq, 'remove POST sent');
  assert.deepEqual(delReq.body, { revision: 'r1', source: 'agent' });
  assert.equal(h.composer.querySelectorAll('.ucmd-card').length, 0);
  assert.equal(h.composer.querySelector('.ucmd-empty').hidden, false);
});

test('inline + New command form validates required fields, shows 400 error inline, and adds user command on 201', async () => {
  let postAttempt = 0;
  const h = makeUcmdHarness((url, opts) => {
    const method = opts.method || 'GET';
    const body = opts.body ? JSON.parse(opts.body) : null;
    if (method === 'GET') return jsonRes({ rev: 'e1.1.r1', token: 'tok1', commands: [] });
    if (method === 'POST' && url.endsWith('/user-commands')) {
      postAttempt++;
      if (postAttempt === 1) return jsonRes({ error: 'cwd escapes worktree root' }, 400);
      return jsonRes({
        rev: 'e1.2.r1',
        token: 'tok1',
        commands: [{ id: 'deploy', source: 'user', label: body.label, line: body.line, cwd: body.cwd, prompt: body.prompt, revision: 'r1' }]
      }, 201);
    }
    return jsonRes({}, 404);
  });
  await h.flushMicrotasks();

  const badge = h.document.getElementById('ucmdbadge');
  h.fire(badge, 'click');
  h.fire(h.composer.querySelector('.ucmd-add-btn'), 'click');

  const form = h.composer.querySelector('.ucmd-add-form');
  const errEl = form.querySelector('.ucmd-form-err');
  assert.equal(form.hidden, false);
  assert.equal(h.composer.querySelector('.ucmd-empty').hidden, true, 'empty card hidden while adding');

  // Empty submit shows client validation error.
  h.fire(form, 'submit');
  assert.equal(errEl.hidden, false);
  assert.equal(errEl.textContent, EN['js.ucmd.form.req']);

  // Fill fields with bad cwd -> server 400 renders inline, form stays open with values preserved.
  form.querySelector('[data-f="label"]').value = 'Deploy';
  form.querySelector('[data-f="cwd"]').value = '../outside';
  form.querySelector('[data-f="line"]').value = './deploy.sh';
  form.querySelector('[data-f="prompt"]').value = 'Verify rollout';
  h.fire(form, 'submit');
  await h.flushMicrotasks();
  assert.equal(form.hidden, false);
  assert.equal(errEl.hidden, false);
  assert.equal(errEl.textContent, 'cwd escapes worktree root');
  assert.equal(form.querySelector('[data-f="label"]').value, 'Deploy');

  // Fix cwd -> 201 closes form, clears fields, and renders the new user card.
  form.querySelector('[data-f="cwd"]').value = 'scripts';
  h.fire(form, 'submit');
  await h.flushMicrotasks();
  assert.equal(form.hidden, true);
  assert.equal(h.composer.querySelectorAll('.ucmd-card').length, 1);
  assert.equal(h.composer.querySelector('.ucmd-card').getAttribute('data-source'), 'user');
  assert.equal(h.composer.querySelector('.ucmd-cwd').textContent, 'scripts');
});

test('keyboard Space/Enter hold-to-confirm, empty-line ask guard, and 409 stale/withdrawn on delete preserve drafts and warn', async () => {
  const reqs = [];
  let delAttempt = 0;
  const cmds = [
    { id: 'build', source: 'agent', label: 'Build', line: 'go build ./...', prompt: 'Check errors', cwd: '', revision: 'r1' }
  ];
  const h = makeUcmdHarness((url, opts) => {
    const method = opts.method || 'GET';
    const body = opts.body ? JSON.parse(opts.body) : null;
    reqs.push({ url, method, body });
    if (method === 'GET') return jsonRes({ rev: 'e1.1.r1', token: 'tok1', commands: cmds });
    if (url.endsWith('/run')) return jsonRes({ id: body.submission_id, command: body.line || 'go build ./...', exec_id: 'ex1', token: 'tok1' });
    if (url.endsWith('/remove')) {
      delAttempt++;
      if (delAttempt === 1) {
        return jsonRes({
          error: 'stale',
          rev: 'e1.2.r1',
          token: 'tok1',
          commands: [{ id: 'build', source: 'agent', label: 'Build', line: 'go build ./...', prompt: 'Updated by agent', cwd: '', revision: 'r2' }]
        }, 409);
      }
      return jsonRes({ error: 'withdrawn', rev: 'e1.3.r1', token: 'tok1', commands: [] }, 409);
    }
    return jsonRes({}, 404);
  });
  await h.flushMicrotasks();

  const badge = h.document.getElementById('ucmdbadge');
  h.fire(badge, 'click');
  const card = h.composer.querySelector('.ucmd-card');
  const cmdIn = card.querySelector('.ucmd-inline-input.is-cmd');
  const runBtn = card.querySelector('.ucmd-ibtn-run');
  const askBtn = card.querySelector('.ucmd-ibtn-ask');
  const delBtn = card.querySelector('.ucmd-ibtn-del');

  // 1) Keyboard Space early release (<1000ms) cancels and emits quick-click toast.
  runBtn.focus();
  h.fire(runBtn, 'keydown', { key: ' ', repeat: false });
  h.advance(300);
  h.fire(runBtn, 'keyup', { key: ' ' });
  assert.equal(reqs.filter(r => r.url.endsWith('/run')).length, 0);
  assert.equal(h.warns.at(-1), EN['js.ucmd.hold.early.run']);

  // 2) Keyboard Enter full 1000ms hold + release confirms run.
  h.fire(runBtn, 'keydown', { key: 'Enter', repeat: false });
  h.advance(1000);
  h.fire(runBtn, 'keyup', { key: 'Enter' });
  await h.flushMicrotasks();
  assert.equal(reqs.filter(r => r.url.endsWith('/run')).length, 1);

  // 3) Empty Line 2 prevents Ask Agent from dispatching POST /message.
  h.advance(700);
  h.fire(badge, 'click');
  cmdIn.value = '   ';
  h.fire(cmdIn, 'input');
  h.holdConfirm(askBtn, 1000);
  assert.equal(h.posts.length, 0, 'empty command line does not send ask message');
  assert.equal(h.warns.at(-1), EN['js.ucmd.save.empty']);

  // 4) 409 stale on delete keeps unsaved inline command draft and shows stale warning.
  cmdIn.value = 'go build -race ./...';
  h.fire(cmdIn, 'input');
  h.holdConfirm(delBtn, 1000);
  await h.flushMicrotasks();
  assert.equal(h.warns.at(-1), EN['js.ucmd.stale']);
  assert.equal(h.oks.length, 0);
  const refreshedCard = h.composer.querySelector('.ucmd-card');
  assert.equal(refreshedCard.querySelector('.ucmd-inline-input.is-cmd').value, 'go build -race ./...', 'stale 409 preserves unsaved inline draft');
  assert.equal(refreshedCard.classList.contains('is-dirty'), true);

  // 5) 409 withdrawn on delete warns withdrawn and clears card.
  h.holdConfirm(refreshedCard.querySelector('.ucmd-ibtn-del'), 1000);
  await h.flushMicrotasks();
  assert.equal(h.warns.at(-1), EN['js.ucmd.withdrawn']);
  assert.equal(h.composer.querySelectorAll('.ucmd-card').length, 0);
});
