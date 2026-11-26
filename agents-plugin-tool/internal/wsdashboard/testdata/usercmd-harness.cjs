// Shared fake-DOM harness for the user command client suites
// (composer-usercmd-v2.test.cjs, composer-usercmd-compact.test.cjs): a vm
// context with #composer, fake timers, fetch, and the ucmd fragments loaded.
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
    wsTn: (k, n) => (EN[k + (n === 1 ? '.one' : '.other')] || k).replace('{0}', String(n)),
    noteWarn: m => warns.push(m),
    noteOk: m => oks.push(m),
    post: (route, body, done) => {
      posts.push({ route, body });
      done(true, { ok: true });
    },
    fetch: (url, opts) => fetchHandler(String(url), opts || {})
  });

  vm.runInContext(readAssetSource('wsdashboard', 'hold-confirm.js'), ctx);
  vm.runInContext(readAssetSource('wsdashboard', 'dashboard-composer-ucmd-editor.js'), ctx);
  vm.runInContext(readAssetSource('wsdashboard', 'dashboard-composer-ucmd-card.js'), ctx);
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

module.exports = { EN, jsonRes, makeUcmdHarness };
