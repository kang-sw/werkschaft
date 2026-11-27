const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

function makeHarness() {
  const timers = new Map();
  let nextTimerId = 1;
  let now = 10000;
  const window = new EventTarget();
  const document = Object.assign(new EventTarget(), { hidden: false });

  function makeClassList(initial = '') {
    const set = new Set(initial.split(/\s+/).filter(Boolean));
    return {
      add(...xs) { xs.forEach(x => set.add(x)); },
      remove(...xs) { xs.forEach(x => set.delete(x)); },
      toggle(x, on) {
        const next = on === undefined ? !set.has(x) : !!on;
        if (next) set.add(x); else set.delete(x);
        return next;
      },
      contains(x) { return set.has(x); },
      toArray() { return [...set]; }
    };
  }

  function makeEl(tag, cls = '', rect = { left: 10, top: 10, right: 110, bottom: 40, width: 100, height: 30 }) {
    const attrs = {};
    const styles = {};
    let text = '';
    const el = Object.assign(new EventTarget(), {
      tagName: tag.toUpperCase(),
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
      setAttribute(k, v) { attrs[k] = String(v); },
      getAttribute(k) { return k in attrs ? attrs[k] : null; },
      removeAttribute(k) { delete attrs[k]; },
      hasAttribute(k) { return k in attrs; },
      appendChild(child) { child.parentNode = this; this.children.push(child); return child; },
      replaceChild(next, prev) {
        const i = this.children.indexOf(prev);
        if (i >= 0) {
          prev.parentNode = null;
          next.parentNode = this;
          this.children[i] = next;
        }
        return prev;
      },
      contains(other) {
        let cur = other;
        while (cur) {
          if (cur === this) return true;
          cur = cur.parentNode;
        }
        return false;
      },
      matches(sel) {
        if (sel.startsWith('.')) return this.classList.contains(sel.slice(1));
        return this.tagName.toLowerCase() === sel.toLowerCase();
      },
      closest(sel) {
        let cur = this;
        while (cur) {
          if (cur.matches && cur.matches(sel)) return cur;
          cur = cur.parentNode;
        }
        return null;
      },
      getBoundingClientRect() { return rect; },
      _rect: rect
    });
    Object.defineProperties(el, {
      firstChild: { get() { return this.children[0] || null; } },
      lastChild: { get() { return this.children[this.children.length - 1] || null; } },
      textContent: {
        get() { return text || this.children.map(c => c.textContent || '').join(''); },
        set(v) { text = String(v || ''); }
      }
    });
    return el;
  }

  const fakeSetTimeout = (fn, ms) => {
    const id = nextTimerId++;
    timers.set(id, { id, fn, due: now + (ms || 0) });
    return id;
  };
  const fakeClearTimeout = (id) => { timers.delete(id); };
  const advance = (ms) => {
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

  const FakeDate = { now: () => now };
  const ctx = vm.createContext({
    window,
    document,
    Date: FakeDate,
    setTimeout: fakeSetTimeout,
    clearTimeout: fakeClearTimeout
  });
  vm.runInContext(readAssetSource('wsdashboard', 'hold-confirm.js'), ctx);

  function fire(target, type, props = {}) {
    const ev = Object.assign(new Event(type, { bubbles: true, cancelable: true }), props);
    let stoppedImmediate = false;
    const origStopImm = ev.stopImmediatePropagation.bind(ev);
    ev.stopImmediatePropagation = () => { stoppedImmediate = true; origStopImm(); };
    Object.defineProperty(ev, '_stoppedImmediate', { get: () => stoppedImmediate });
    target.dispatchEvent(ev);
    return ev;
  }

  return { window, document, api: window.WsHoldConfirm, makeEl, advance, fire };
}

test('pointer hold requires full duration and release inside bounds, and suppresses trailing click', () => {
  const h = makeHarness();
  const row = h.makeEl('div', 'rs');
  const btn = row.appendChild(h.makeEl('button', 'spact'));
  let starts = 0, readies = 0, confirms = 0, cancels = 0;
  const cancelReasons = [];

  h.api.attach(btn, {
    mode: 'bar',
    duration: 1500,
    onStart: () => starts++,
    onReady: () => readies++,
    onConfirm: () => confirms++,
    onCancel: (_el, reason) => { cancels++; cancelReasons.push(reason); }
  });

  // Early release before 1500ms cancels without confirming.
  h.fire(btn, 'pointerdown', { button: 0, isPrimary: true, pointerId: 1 });
  assert.equal(starts, 1);
  assert.equal(h.api.isHolding(), true);
  assert.equal(btn.classList.contains('d-hold-active'), true);
  assert.equal(row.classList.contains('d-holding'), true);
  h.advance(1499);
  assert.equal(readies, 0);
  h.fire(h.window, 'pointerup', { pointerId: 1, clientX: 50, clientY: 20 });
  assert.equal(confirms, 0);
  assert.equal(cancels, 1);
  assert.deepEqual(cancelReasons, ['early-release']);
  assert.equal(h.api.isHolding(), false);
  assert.equal(h.api.wasJustCancelled(), true);
  assert.equal(row.classList.contains('d-holding'), false);

  // Full hold + release inside confirms once, and trailing click is suppressed.
  h.fire(btn, 'pointerdown', { button: 0, isPrimary: true, pointerId: 2 });
  h.advance(1500);
  assert.equal(readies, 1);
  assert.equal(btn.classList.contains('d-hold-ready'), true);
  assert.equal(btn.getAttribute('data-hold-state'), 'ready');
  h.fire(h.window, 'pointerup', { pointerId: 2, clientX: 50, clientY: 20 });
  assert.equal(confirms, 1);
  assert.equal(h.api.isHolding(), false);
  const clickEv = h.fire(btn, 'click');
  assert.equal(clickEv.defaultPrevented, true);
  assert.equal(confirms, 1);
});

test('pointer leaving bounds, pointercancel, Escape, and blur all cancel active hold', () => {
  const h = makeHarness();
  const btn = h.makeEl('button', 'spact');
  let confirms = 0, cancels = 0;
  h.api.attach(btn, {
    duration: 1500,
    onConfirm: () => confirms++,
    onCancel: () => cancels++
  });

  // Pointer moves outside bounds even after becoming ready -> cancels, release does not confirm.
  h.fire(btn, 'pointerdown', { button: 0, isPrimary: true, pointerId: 1 });
  h.advance(1500);
  h.fire(h.window, 'pointermove', { pointerId: 1, clientX: 500, clientY: 500 });
  assert.equal(h.api.isHolding(), false);
  assert.equal(cancels, 1);
  h.fire(h.window, 'pointerup', { pointerId: 1, clientX: 50, clientY: 20 });
  assert.equal(confirms, 0);

  // pointercancel cancels.
  h.fire(btn, 'pointerdown', { button: 0, isPrimary: true, pointerId: 2 });
  h.advance(500);
  h.fire(h.window, 'pointercancel', { pointerId: 2 });
  assert.equal(cancels, 2);
  assert.equal(h.api.isHolding(), false);

  // Escape cancels and stops immediate propagation so popovers stay open.
  h.fire(btn, 'pointerdown', { button: 0, isPrimary: true, pointerId: 3 });
  const esc = h.fire(h.window, 'keydown', { key: 'Escape' });
  assert.equal(esc.defaultPrevented, true);
  assert.equal(esc._stoppedImmediate, true);
  assert.equal(cancels, 3);
  assert.equal(h.api.isHolding(), false);

  // Window blur cancels.
  h.fire(btn, 'pointerdown', { button: 0, isPrimary: true, pointerId: 4 });
  h.fire(h.window, 'blur');
  assert.equal(cancels, 4);

  // Document visibilitychange (hidden) cancels.
  h.fire(btn, 'pointerdown', { button: 0, isPrimary: true, pointerId: 5 });
  h.document.hidden = true;
  h.fire(h.document, 'visibilitychange');
  assert.equal(cancels, 5);
  assert.equal(confirms, 0);
});

test('keyboard Space/Enter hold ignores repeats, cancels on early keyup, confirms after 1500ms', () => {
  const h = makeHarness();
  const btn = h.makeEl('button', 'spact');
  let confirms = 0, cancels = 0;
  h.api.attach(btn, {
    duration: 1500,
    onConfirm: () => confirms++,
    onCancel: () => cancels++
  });

  // Early release of Space cancels.
  h.fire(btn, 'keydown', { key: ' ', repeat: false });
  assert.equal(h.api.isHolding(), true);
  h.fire(btn, 'keydown', { key: ' ', repeat: true });
  h.advance(1000);
  h.fire(h.window, 'keyup', { key: ' ' });
  assert.equal(confirms, 0);
  assert.equal(cancels, 1);

  // Full Enter hold confirms on keyup.
  h.fire(btn, 'keydown', { key: 'Enter', repeat: false });
  h.advance(1500);
  h.fire(h.window, 'keyup', { key: 'Enter' });
  assert.equal(confirms, 1);
  assert.equal(h.api.isHolding(), false);
});

test('shouldHold() === false runs immediately on single click with no hold', () => {
  const h = makeHarness();
  const btn = h.makeEl('button', 'spact');
  let safe = true, confirms = 0;
  const ctl = h.api.attach(btn, {
    shouldHold: () => !safe,
    onConfirm: () => confirms++
  });
  assert.equal(btn.classList.contains('d-hold-btn'), false);
  h.fire(btn, 'click');
  assert.equal(confirms, 1);

  // Switching to unsafe enables hold mode and blocks plain click.
  safe = false;
  ctl.sync();
  assert.equal(btn.classList.contains('d-hold-btn'), true);
  h.fire(btn, 'click');
  assert.equal(confirms, 1);
});

test('delegate routes safe .drstop to immediate click and unsafe .drstop to radial hold', () => {
  const h = makeHarness();
  const rail = h.makeEl('div', 'rail');
  const safeBtn = rail.appendChild(h.makeEl('button', 'drstop'));
  safeBtn.setAttribute('data-safe', '1');
  const unsafeBtn = rail.appendChild(h.makeEl('button', 'drstop'));
  unsafeBtn.setAttribute('data-safe', '0');

  const confirmed = [];
  let cancels = 0;
  h.api.delegate(rail, '.drstop', {
    mode: 'radial',
    shouldHold: el => el.getAttribute('data-safe') !== '1',
    onConfirm: el => confirmed.push(el),
    onCancel: () => cancels++
  });

  function fireOnRail(target, type, props = {}) {
    const ev = Object.assign(new Event(type, { bubbles: true, cancelable: true }), props);
    Object.defineProperty(ev, 'target', { value: target, configurable: true });
    rail.dispatchEvent(ev);
    return ev;
  }

  // Safe button fires on single click.
  fireOnRail(safeBtn, 'click');
  assert.deepEqual(confirmed, [safeBtn]);

  // Unsafe button ignores plain click.
  fireOnRail(unsafeBtn, 'click');
  assert.deepEqual(confirmed, [safeBtn]);

  // Unsafe button confirms after 1500ms pointer hold + release inside bounds.
  fireOnRail(unsafeBtn, 'pointerdown', { button: 0, isPrimary: true, pointerId: 7 });
  assert.equal(unsafeBtn.getAttribute('data-hold-mode'), 'radial');
  assert.equal(h.api.isHolding(), true);
  h.advance(1500);
  h.fire(h.window, 'pointerup', { pointerId: 7, clientX: 50, clientY: 20 });
  assert.deepEqual(confirmed, [safeBtn, unsafeBtn]);
  assert.equal(cancels, 0);
});

test('rpSessMake uses hold-confirm for unsafe stop, archive hide, and close (no wsConfirm modal)', () => {
  const h = makeHarness();
  const leadsSrc = readAssetSource('wsdashboard', 'dashboard-leads.js');
  const start = leadsSrc.indexOf('  var leadActions = new Set();\n  function rpSessMake(inRow) {');
  const end = leadsSrc.indexOf('\n  // ', start + 100);
  assert(start >= 0 && end > start, 'rpSessMake block exists');
  // Find the closing "  }\n" of rpSessMake cleanly by slicing to P.close + return P.
  const retIdx = leadsSrc.indexOf('    return P;\n  }\n', start);
  assert(retIdx > start, 'rpSessMake end exists');
  const fnSrc = leadsSrc.slice(start, retIdx + '    return P;\n  }\n'.length);

  const posts = [], toasts = [], directCloses = [];
  let confirmCalled = 0, closedPop = 0, subagentModalCalled = 0;

  function mk(tag, cls = '', text = '') {
    const el = h.makeEl(tag, cls || '');
    if (text) el.appendChild({ nodeType: 3, textContent: String(text), parentNode: el });
    return el;
  }
  const rp = { P: null };
  const ctx = vm.createContext({
    window: Object.assign(h.window, {
      WsHoldConfirm: h.api,
      wsConfirm: () => { confirmCalled++; return Promise.resolve(true); }
    }),
    document: h.document,
    Set,
    Array,
    Number,
    String,
    Boolean,
    encodeURIComponent,
    rid: 'other',
    hostedPage: false,
    handedPage: false,
    lead: { st: null },
    subagentClosing: {},
    supChoices: null,
    mk,
    rpPop: () => mk('div', 'rpop'),
    rpBtn: (cls, text) => mk('button', cls, text),
    wsT: k => k,
    wsTn: k => k,
    gIcon: name => { const s = mk('svg', 'i'); s.setAttribute('data-icon', name); return s; },
    luIcon: name => { const s = mk('svg', 'i'); s.setAttribute('data-icon', name); return s; },
    permSeg: () => ({ set: () => {} }),
    permLevels: () => [],
    permState: () => {},
    permMap: () => {},
    permFullDrop: () => {},
    permPreflight: () => {},
    permRefused: () => false,
    leadModes: () => [],
    leadChoices: () => {},
    leadDraw: () => {},
    leadFetch: () => {},
    tkFetch: () => {},
    archErr: (a, st, s) => s || 'HTTP ' + st,
    rp,
    rpLive: () => true,
    railPopBtn: () => null,
    railPopClose: () => { closedPop++; },
    railPopPlace: () => {},
    railMarks: () => {},
    stopNeedsHold: new Set(),
    leadRows: () => ({
      model: { focus: () => {} },
      effort: {},
      set: () => {},
      own: () => {},
      inline: () => {},
      disable: () => {}
    }),
    leadGone: st => !st || st.status === 'closed',
    leadErr: (st, s) => s || 'HTTP ' + st,
    toast: msg => toasts.push(msg),
    railPost: (route, body, done) => {
      posts.push({ route, body });
      if (body.safe_only) {
        done(409, {}, 'session no longer safe to stop');
        return;
      }
      done(200, { id: 's1', status: 'running', can_control: true, process: false }, '');
    },
    subagentClose: () => { subagentModalCalled++; },
    subagentCloseDirect: (id) => { directCloses.push(id); }
  });
  vm.runInContext(fnSrc, ctx);
  const P = ctx.rpSessMake(true);
  rp.P = { sess: P };

  function findAction(container, key) {
    for (const c of container.children) {
      if (c.tagName === 'BUTTON' && c.textContent.includes(key)) return c;
      if (c.children) {
        const hit = findAction(c, key);
        if (hit) return hit;
      }
    }
    return null;
  }

  // 1. Safe stop: moon icon, single click posts immediately without hold or modal.
  P.rid = 's1';
  P.st = { id: 's1', backend: 'claude', status: 'running', can_control: true, process: true, safe_stop: true, hidden: false };
  P.draw();
  assert.equal(findAction(P.el, 'js.lead.handoff'), null, 'deprecated handoff button is omitted from session menu');
  const stopBtn = findAction(P.el, 'js.lead.stop');
  assert(stopBtn, 'stop button exists');
  assert.equal(stopBtn.firstChild.getAttribute('data-icon'), 'moon');
  assert.equal(stopBtn.classList.contains('danger'), false);
  assert.equal(stopBtn.classList.contains('d-hold-btn'), false);
  h.fire(stopBtn, 'click');
  assert.equal(posts.length, 1);
  assert.equal(posts[0].route, '/agents/s1/stop');
  assert.equal(posts[0].body.safe_only, true);
  assert.equal(confirmCalled, 0);

  // 2. The refusal overrides the previously safe state immediately.
  assert.equal(P.st.safe_stop, true);
  assert.equal(closedPop, 0);
  assert.equal(stopBtn.firstChild.getAttribute('data-icon'), 'stop');
  assert.equal(stopBtn.classList.contains('danger'), true);
  assert.equal(stopBtn.classList.contains('d-hold-btn'), true);
  h.fire(stopBtn, 'click');
  assert.equal(posts.length, 1, 'plain click on unsafe stop does not fire');
  h.fire(stopBtn, 'pointerdown', { button: 0, isPrimary: true, pointerId: 1 });
  h.advance(1500);
  h.fire(h.window, 'pointerup', { pointerId: 1, clientX: 50, clientY: 20 });
  assert.equal(posts.length, 2);
  assert.equal(posts[1].route, '/agents/s1/stop');
  assert.deepEqual(Object.keys(posts[1].body), []);
  assert.equal(confirmCalled, 0);

  // 3. Archive (hide) requires 1500ms hold; unhide is immediate single click.
  const archBtn = findAction(P.el, 'js.lead.archive');
  assert(archBtn, 'archive button exists');
  assert.equal(archBtn.classList.contains('d-hold-btn'), true);
  h.fire(archBtn, 'click');
  assert.equal(posts.length, 2, 'plain click on archive hide does not fire');
  h.fire(archBtn, 'pointerdown', { button: 0, isPrimary: true, pointerId: 2 });
  h.advance(1500);
  h.fire(h.window, 'pointerup', { pointerId: 2, clientX: 50, clientY: 20 });
  assert.equal(posts.length, 3);
  assert.equal(posts[2].route, '/agents/s1/hide');
  assert.equal(confirmCalled, 0);

  P.st = { id: 's1', backend: 'claude', status: 'running', can_control: true, process: false, hidden: true };
  P.draw();
  assert.equal(archBtn.classList.contains('d-hold-btn'), false);
  // Trailing synthetic click right after pointerup must not immediately unhide.
  h.fire(archBtn, 'click');
  assert.equal(posts.length, 3);
  h.advance(100);
  h.fire(archBtn, 'click');
  assert.equal(posts.length, 4);
  assert.equal(posts[3].route, '/agents/s1/unhide');

  // 4. Close on a child row uses 1500ms hold and subagentCloseDirect (never subagentClose modal or wsConfirm).
  P.rid = 'child1';
  P.st = { id: 'child1', backend: 'claude', status: 'running', can_control: true, process: true, is_child: true, label: 'Worker' };
  P.draw();
  const closeBtn = findAction(P.el, 'js.lead.close');
  assert(closeBtn, 'close button exists');
  assert.equal(closeBtn.classList.contains('d-hold-btn'), true);
  // Hold buttons stack .gs-idle and .gs-ready in 1/1 grid with zero text swap on ready; non-hold restart stays plain.
  function findClass(node, cls) {
    if (node.classList && node.classList.contains(cls)) return node;
    for (const c of (node.children || [])) {
      const hit = findClass(c, cls);
      if (hit) return hit;
    }
    return null;
  }
  const restartBtn = findAction(P.el, 'js.lead.restart');
  assert(restartBtn, 'restart button exists');
  assert.equal(findClass(restartBtn, 'gs-idle'), null, 'non-hold restart keeps plain .gs');
  assert.equal(findClass(restartBtn, 'gs-ready'), null, 'non-hold restart has no .gs-ready');
  for (const btn of [stopBtn, archBtn, closeBtn]) {
    const idle = findClass(btn, 'gs-idle');
    const ready = findClass(btn, 'gs-ready');
    assert(idle && ready, 'hold button has .gs-idle and .gs-ready');
    assert.equal(ready.textContent, 'js.hold.release');
    assert.equal(idle.getAttribute('aria-hidden'), null);
    assert.equal(ready.getAttribute('aria-hidden'), 'true');
  }
  const closeIdle = findClass(closeBtn, 'gs-idle');
  const closeReady = findClass(closeBtn, 'gs-ready');
  const closeIdleBefore = closeIdle.textContent;
  h.fire(closeBtn, 'click');
  assert.equal(directCloses.length, 0);
  // Armed ready state toggles .d-hold-ready and aria-hidden without swapping .gs-idle text; cancel restores aria-hidden.
  h.fire(closeBtn, 'pointerdown', { button: 0, isPrimary: true, pointerId: 3 });
  h.advance(1500);
  assert.equal(closeBtn.classList.contains('d-hold-ready'), true);
  assert.equal(closeIdle.textContent, closeIdleBefore, 'onReady does not swap idle note text');
  assert.equal(closeReady.textContent, 'js.hold.release');
  assert.equal(closeIdle.getAttribute('aria-hidden'), 'true');
  assert.equal(closeReady.getAttribute('aria-hidden'), null);
  h.fire(h.window, 'pointermove', { pointerId: 3, clientX: 500, clientY: 500 });
  assert.equal(closeBtn.classList.contains('d-hold-ready'), false);
  assert.equal(closeIdle.getAttribute('aria-hidden'), null);
  assert.equal(closeReady.getAttribute('aria-hidden'), 'true');
  assert.equal(directCloses.length, 0);
  // Full hold + release inside bounds confirms and restores aria-hidden.
  h.fire(closeBtn, 'pointerdown', { button: 0, isPrimary: true, pointerId: 4 });
  h.advance(1500);
  h.fire(h.window, 'pointerup', { pointerId: 4, clientX: 50, clientY: 20 });
  assert.equal(closeIdle.getAttribute('aria-hidden'), null);
  assert.equal(closeReady.getAttribute('aria-hidden'), 'true');
  assert.deepEqual(directCloses, ['child1']);
  assert.equal(subagentModalCalled, 0);
  assert.equal(confirmCalled, 0);

  // CSS stacks .gs-idle/.gs-ready in the same 1/1 grid cell so height never shrinks on armed state.
  const railCss = readAssetSource('wsdashboard', 'dashboard-rail.css');
  assert.match(railCss, /\.rpop\s+\.spact\s+\.gs\s*\{[^}]*display:\s*grid/);
  assert.match(railCss, /\.rpop\s+\.spact\s+\.gs\s*>\s*\.gs-idle,\s*\.rpop\s+\.spact\s+\.gs\s*>\s*\.gs-ready\s*\{[^}]*grid-area:\s*1\s*\/\s*1/);
});

test('radial hold ::before fills inherit button border-radius without scale(>1) or negative inset overshoot', () => {
  function cssRuleBody(src, selector) {
    const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&').replace(/\s+/g, '\\s+');
    const m = src.match(new RegExp(`(?:^|\\n)${escaped}\\s*\\{([^}]*)\\}`));
    assert.ok(m, `missing CSS rule ${selector}`);
    return m[1];
  }
  function assertNoOvershoot(body, label) {
    assert.doesNotMatch(body, /inset:\s*-/, `${label} must not use negative inset`);
    assert.doesNotMatch(body, /border-radius:\s*50%/, `${label} must not use border-radius: 50%`);
    for (const m of body.matchAll(/scale\(\s*([0-9.]+)\s*\)/g)) {
      assert.ok(Number(m[1]) <= 1, `${label} must not scale > 1 (got scale(${m[1]}))`);
    }
  }

  const ucmdCss = readAssetSource('wsdashboard', 'dashboard-composer-ucmd.css');
  const ucmdBefore = cssRuleBody(ucmdCss, '.ucmd-ibtn.is-hold::before');
  assert.match(ucmdBefore, /inset:\s*0\s*;/);
  assert.match(ucmdBefore, /border-radius:\s*inherit\s*;/);
  assertNoOvershoot(ucmdBefore, '.ucmd-ibtn.is-hold::before');
  const ucmdActive = cssRuleBody(ucmdCss, '.ucmd-ibtn.is-hold.d-hold-active::before');
  assert.match(ucmdActive, /transform:\s*scale\(1\)\s*;/);
  assertNoOvershoot(ucmdActive, '.ucmd-ibtn.is-hold.d-hold-active::before');
  const ucmdReady = cssRuleBody(ucmdCss, '.ucmd-ibtn.is-hold.d-hold-ready::before');
  assert.match(ucmdReady, /transform:\s*scale\(1\)\s*;/);
  assertNoOvershoot(ucmdReady, '.ucmd-ibtn.is-hold.d-hold-ready::before');

  const railCss = readAssetSource('wsdashboard', 'dashboard-rail.css');
  const railBefore = cssRuleBody(railCss, '.rail .drstop.d-stop-unsafe::before');
  assert.match(railBefore, /inset:\s*0\s*;/);
  assert.match(railBefore, /border-radius:\s*inherit\s*;/);
  assertNoOvershoot(railBefore, '.rail .drstop.d-stop-unsafe::before');
  const railReady = cssRuleBody(railCss, '.rail .drstop.d-stop-unsafe.d-hold-ready::before');
  assertNoOvershoot(railReady, '.rail .drstop.d-stop-unsafe.d-hold-ready::before');
  const radialKf = railCss.match(/@keyframes\s+d-hold-radial\s*\{([^}]*\{[^}]*\}[^}]*\{[^}]*\}[^}]*)\}/);
  assert.ok(radialKf, 'missing @keyframes d-hold-radial');
  assertNoOvershoot(radialKf[1], '@keyframes d-hold-radial');

  const acctCss = readAssetSource('wsdashboard', 'dashboard-settings-account.css');
  const acctBefore = cssRuleBody(acctCss, '.account-icon.d-hold-btn::before');
  assert.match(acctBefore, /inset:\s*0\s*;/);
  assert.match(acctBefore, /border-radius:\s*inherit\s*;/);
  assertNoOvershoot(acctBefore, '.account-icon.d-hold-btn::before');
  const acctReady = cssRuleBody(acctCss, '.account-icon.d-hold-ready::before');
  assertNoOvershoot(acctReady, '.account-icon.d-hold-ready::before');
});
