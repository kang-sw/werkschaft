const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

const FILES = readAssetSource('wsdashboard', 'files.js');

function createDom() {
  class ClassList {
    constructor(el) { this.el = el; this.set = new Set(); }
    syncFrom(str) {
      this.set = new Set(String(str || '').trim().split(/\s+/).filter(Boolean));
    }
    _write() { this.el._className = Array.from(this.set).join(' '); }
    add(...names) { names.forEach(n => { if (n) this.set.add(n); }); this._write(); }
    remove(...names) { names.forEach(n => this.set.delete(n)); this._write(); }
    toggle(name, force) {
      const on = force === undefined ? !this.set.has(name) : !!force;
      if (on) this.set.add(name); else this.set.delete(name);
      this._write();
      return on;
    }
    contains(name) { return this.set.has(name); }
  }

  function matchSimple(el, sel) {
    if (!el || !el.tagName) return false;
    let rest = sel.trim();
    if (!rest) return false;
    const tagMatch = rest.match(/^[a-zA-Z0-9_-]+/);
    if (tagMatch) {
      if (el.tagName !== tagMatch[0].toUpperCase()) return false;
      rest = rest.slice(tagMatch[0].length);
    }
    while (rest) {
      if (rest[0] === '#') {
        const m = rest.slice(1).match(/^[a-zA-Z0-9_-]+/);
        if (!m || el.id !== m[0]) return false;
        rest = rest.slice(1 + m[0].length);
      } else if (rest[0] === '.') {
        const m = rest.slice(1).match(/^[a-zA-Z0-9_-]+/);
        if (!m || !el.classList.contains(m[0])) return false;
        rest = rest.slice(1 + m[0].length);
      } else if (rest[0] === '[') {
        const end = rest.indexOf(']');
        if (end < 0) return false;
        const inner = rest.slice(1, end), eq = inner.indexOf('=');
        if (eq < 0) {
          if (!el.hasAttribute(inner)) return false;
        } else {
          const k = inner.slice(0, eq).trim();
          const v = inner.slice(eq + 1).trim().replace(/^["']|["']$/g, '');
          if (el.getAttribute(k) !== v) return false;
        }
        rest = rest.slice(end + 1);
      } else {
        return false;
      }
    }
    return true;
  }

  function matchSingleSelector(el, selector) {
    const parts = selector.trim().split(/\s+/);
    let cur = el;
    if (!matchSimple(cur, parts[parts.length - 1])) return false;
    for (let i = parts.length - 2; i >= 0; i--) {
      if (parts[i] === '>') {
        i--;
        cur = cur.parentNode;
        if (!cur || !matchSimple(cur, parts[i])) return false;
      } else {
        let p = cur.parentNode;
        while (p && !matchSimple(p, parts[i])) p = p.parentNode;
        if (!p) return false;
        cur = p;
      }
    }
    return true;
  }

  function matchesSelector(el, selector) {
    return selector.split(',').some(part => matchSingleSelector(el, part));
  }

  class El {
    constructor(tag, doc) {
      this.tagName = tag.toUpperCase();
      this.ownerDocument = doc;
      this.children = [];
      this.parentNode = null;
      this.attrs = {};
      this.style = {};
      this.listeners = {};
      this.hidden = false;
      this.disabled = false;
      this.value = '';
      this.checked = false;
      this.type = '';
      this.tabIndex = -1;
      this.scrollTop = 0;
      this.scrollLeft = 0;
      this.offsetWidth = 120;
      this.offsetHeight = 120;
      this._className = '';
      this._text = '';
      this._rect = { left: 20, right: 300, top: 30, bottom: 50 };
      this.classList = new ClassList(this);
    }
    get className() { return this._className; }
    set className(v) { this._className = String(v || ''); this.classList.syncFrom(v); }
    get id() { return this.attrs.id || ''; }
    set id(v) { this.attrs.id = String(v); }
    get textContent() {
      if (this._text) return this._text;
      return this.children.map(c => c.textContent).join('');
    }
    set textContent(v) {
      this.children = [];
      this._text = v == null ? '' : String(v);
    }
    get innerHTML() { return this._html || ''; }
    set innerHTML(v) { this.children = []; this._html = String(v || ''); }
    setAttribute(k, v) { this.attrs[k] = String(v); }
    getAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k) ? this.attrs[k] : null; }
    hasAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k); }
    removeAttribute(k) { delete this.attrs[k]; }
    appendChild(n) {
      if (n.tagName === 'FRAGMENT') {
        n.children.slice().forEach(k => this.appendChild(k));
        return n;
      }
      if (n.parentNode) n.parentNode.removeChild(n);
      this.children.push(n);
      n.parentNode = this;
      return n;
    }
    insertBefore(n, ref) {
      if (!ref) return this.appendChild(n);
      if (n.tagName === 'FRAGMENT') {
        n.children.slice().forEach(k => this.insertBefore(k, ref));
        return n;
      }
      if (n.parentNode) n.parentNode.removeChild(n);
      const i = this.children.indexOf(ref);
      this.children.splice(i < 0 ? this.children.length : i, 0, n);
      n.parentNode = this;
      return n;
    }
    removeChild(n) {
      const i = this.children.indexOf(n);
      if (i >= 0) this.children.splice(i, 1);
      n.parentNode = null;
      return n;
    }
    contains(n) { for (let c = n; c; c = c.parentNode) if (c === this) return true; return false; }
    closest(sel) {
      for (let c = this; c && c.tagName; c = c.parentNode) if (matchesSelector(c, sel)) return c;
      return null;
    }
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
    }
    querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
    removeEventListener(type, fn) { this.listeners[type] = (this.listeners[type] || []).filter(f => f !== fn); }
    fire(type, ev = {}) {
      const e = Object.assign({ target: this, preventDefault() { this.defaultPrevented = true; }, stopPropagation() {} }, ev);
      for (const fn of (this.listeners[type] || []).slice()) fn(e);
      return e;
    }
    click() { this.fire('click'); }
    focus() { this.ownerDocument.activeElement = this; }
    select() {}
    scrollIntoView() {}
    getClientRects() {
      for (let c = this; c; c = c.parentNode) if (c.hidden) return [];
      return [1];
    }
    getBoundingClientRect() { return this._rect; }
  }

  const doc = {
    documentElement: null,
    body: null,
    activeElement: null,
    listeners: {},
    createElement(tag) { return new El(tag, doc); },
    createDocumentFragment() { return new El('fragment', doc); },
    createTextNode(s) { const n = new El('text', doc); n._text = String(s || ''); return n; },
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); },
    removeEventListener(type, fn) { this.listeners[type] = (this.listeners[type] || []).filter(f => f !== fn); },
    fire(type, ev = {}) {
      const e = Object.assign({ target: this.body, preventDefault() { this.defaultPrevented = true; }, stopPropagation() {} }, ev);
      for (const fn of (this.listeners[type] || []).slice()) fn(e);
      return e;
    },
    querySelector(sel) { return this.documentElement.querySelector(sel); },
    querySelectorAll(sel) { return this.documentElement.querySelectorAll(sel); },
  };
  doc.documentElement = new El('html', doc);
  doc.body = doc.documentElement.appendChild(new El('body', doc));
  doc.activeElement = doc.body;
  return doc;
}

async function settle() {
  for (let i = 0; i < 16; i++) await Promise.resolve();
}

function makeHarness({ localViewer = true, onPost = null, previewResponses = {}, entries = [], session = true, root = '/project' } = {}) {
  const doc = createDom();
  if (localViewer) doc.documentElement.setAttribute('data-sysopen', '1');
  const timers = new Map();
  let nextTimer = 0;
  const requests = [], previews = [], posts = [], toasts = [];
  const win = {
    innerWidth: 1200,
    innerHeight: 900,
    listeners: {},
    addEventListener(t, fn) { (this.listeners[t] ||= []).push(fn); },
    removeEventListener(t, fn) { this.listeners[t] = (this.listeners[t] || []).filter(f => f !== fn); },
    WsCards: { icon: name => '<svg data-icon="' + name + '"></svg>' },
    WsPreview: {
      state: () => '',
      open: (path, o) => previews.push({ path, o }),
    },
  };

  function fetchControlled(url, options) {
    const parsed = new URL(url, 'http://fixture.invalid');
    if (parsed.pathname.endsWith('/fs/list')) {
      const reqRoot = parsed.searchParams.get('root') || root;
      const dir = parsed.searchParams.get('dir') || '';
      const dirEntries = typeof entries === 'function' ? entries(dir, reqRoot) : (dir ? [] : entries);
      const isSession = typeof session === 'function' ? session(reqRoot) : (reqRoot === root ? session : false);
      return Promise.resolve({
        ok: true,
        json: () => ({ root: reqRoot, name: 'project', abs: dir ? reqRoot + '/' + dir : reqRoot, sep: '/', session: isSession, entries: dirEntries }),
      });
    }
    return new Promise((resolve, reject) => {
      requests.push({ url: parsed, options, resolve, reject, taken: false });
    });
  }

  const wsT = (k, v) => v !== undefined ? `${k}:${v}` : k;
  const wsTn = (k, n) => `${k}:${n}`;

  const ctx = vm.createContext({
    document: doc,
    window: win,
    wsT,
    wsTn,
    fetch: fetchControlled,
    AbortController,
    setTimeout(fn) { const id = ++nextTimer; timers.set(id, fn); return id; },
    clearTimeout(id) { timers.delete(id); },
  });
  vm.runInContext(FILES, ctx);

  const container = doc.body.appendChild(doc.createElement('section'));
  container.className = 'fpane';
  const store = {};
  const instance = win.WsFiles.create(container, {
    rid: 's1',
    local: () => doc.documentElement.hasAttribute('data-sysopen'),
    location: { root: session ? '' : root, dir: '' },
    load: k => store[k],
    save: (k, v) => { if (v == null) delete store[k]; else store[k] = v; },
    toast: msg => toasts.push(msg),
    post(route, body, cb) {
      posts.push({ route, body: JSON.parse(JSON.stringify(body)) });
      if (onPost) onPost(route, body, cb);
      else if (cb) cb(true);
    },
  });

  function row(path) {
    const el = container.querySelector('[data-entry="' + path + '"]');
    assert.ok(el, 'missing row for ' + path);
    return el;
  }

  function makeContainersVisible() {
    const dir = container.querySelector('.fdirectory');
    if (dir) dir._rect = { left: 10, right: 510, top: 10, bottom: 600 };
    const hits = container.querySelector('.fhits');
    if (hits) hits._rect = { left: 10, right: 510, top: 10, bottom: 600 };
  }

  function nextReq(route) {
    const req = requests.find(r => !r.taken && r.url.pathname.endsWith('/fs/' + route));
    assert.ok(req, 'expected pending fs/' + route + ' request');
    req.taken = true;
    return req;
  }

  async function flushPreview(path) {
    const req = nextReq('preview');
    assert.equal(req.url.searchParams.get('path'), path);
    const ans = typeof previewResponses === 'function' ? previewResponses(path, req.url) : previewResponses[path];
    assert.ok(ans, 'missing mock preview answer for ' + path);
    req.resolve({ ok: true, json: () => ans });
    await settle();
  }

  async function flushSearch(items) {
    const req = nextReq('find');
    req.resolve({ ok: true, json: () => ({ items }) });
    await settle();
  }

  async function flushTimers() {
    const fns = Array.from(timers.values());
    timers.clear();
    for (const fn of fns) fn();
    await settle();
  }

  function closeMenu() {
    const m = doc.querySelector('.fmenu');
    if (m) m.fire('keydown', { key: 'Escape' });
  }

  return {
    doc, win, container, instance, requests, previews, posts, toasts,
    row, makeContainersVisible, nextReq, flushPreview, flushSearch, flushTimers, closeMenu,
  };
}

test('single-click on kind==="none" (binary and >2 MiB) keeps row selection only without flashing card or menu', async () => {
  const h = makeHarness({
    localViewer: true,
    entries: [{ name: 'blob.bin' }, { name: 'huge.log' }, { name: 'setup.exe' }, { name: 'notes.txt' }],
    previewResponses: {
      'blob.bin': { kind: 'none', reason: 'Binary file', reason_code: 'binary' },
      'huge.log': { kind: 'none', reason: 'File exceeds 2 MiB preview limit' },
      'setup.exe': { kind: 'none', reason: 'Binary file', reason_code: 'binary' },
      'notes.txt': { kind: 'text', text: 'hello' },
    },
  });
  h.instance.shown();
  await settle();
  h.makeContainersVisible();

  for (const name of ['blob.bin', 'huge.log', 'setup.exe']) {
    h.row(name).fire('click');
    await h.flushPreview(name);
    assert.equal(h.instance.selection()?.path, name, name + ' stays selected');
    assert.equal(h.row(name).classList.contains('sel'), true);
    assert.equal(h.row(name).getAttribute('aria-selected'), 'true');
    assert.equal(h.previews.length, 0, name + ' single-click must not open .pv-none card');
    assert.equal(h.doc.querySelector('.fmenu'), null, name + ' single-click must not pop up row menu');
    assert.equal(h.posts.length, 0, name + ' single-click must not auto-open system app');
  }

  // Previewable file still opens preview on single-click and durable preview on dblclick/Enter.
  h.row('notes.txt').fire('click');
  await h.flushPreview('notes.txt');
  assert.equal(h.previews.length, 1);
  assert.equal(h.previews[0].path, 'notes.txt');
  assert.equal(h.previews[0].o.keep, undefined);
  h.row('notes.txt').fire('dblclick');
  await h.flushPreview('notes.txt');
  assert.equal(h.previews.length, 2);
  assert.equal(h.previews[1].o.keep, true);
  assert.equal(h.posts.length, 0);
  h.instance.destroy();
});

test('fast click then dblclick on kind==="none" never flashes card/menu and triggers system open exactly once', async () => {
  const h = makeHarness({
    localViewer: true,
    entries: [{ name: 'archive.zip' }, { name: 'large.log' }],
    previewResponses: {
      'archive.zip': { kind: 'none', reason: 'Binary file', reason_code: 'binary' },
      'large.log': { kind: 'none', reason: 'Exceeds 2 MiB' },
    },
  });
  h.instance.shown();
  await settle();
  h.makeContainersVisible();

  // Case A: First click resolves BEFORE click #2 / dblclick (fast loopback).
  h.row('archive.zip').fire('click');
  await h.flushPreview('archive.zip');
  assert.equal(h.previews.length, 0);
  assert.equal(h.doc.querySelector('.fmenu'), null);
  assert.equal(h.posts.length, 0);

  h.row('archive.zip').fire('click');
  const staleClick2 = h.nextReq('preview');
  h.row('archive.zip').fire('dblclick');
  await h.flushPreview('archive.zip');
  // Resolve click #2 late to confirm superseded preflight is ignored.
  staleClick2.resolve({ ok: true, json: () => ({ kind: 'none', reason_code: 'binary' }) });
  await settle();

  assert.equal(h.posts.length, 1);
  assert.deepEqual(h.posts[0], { route: 'fs/act', body: { path: 'archive.zip', action: 'open', root: '' } });
  assert.equal(h.previews.length, 0);
  assert.equal(h.doc.querySelector('.fmenu'), null);
  assert.equal(h.toasts.at(-1), 'js.toast.openednative');

  // Case B: Enter on >2 MiB file triggers system open and skips .pv-none card.
  h.row('large.log').fire('keydown', { key: 'Enter' });
  await h.flushPreview('large.log');
  assert.equal(h.posts.length, 2);
  assert.deepEqual(h.posts[1], { route: 'fs/act', body: { path: 'large.log', action: 'open', root: '' } });
  assert.equal(h.previews.length, 0);
  assert.equal(h.doc.querySelector('.fmenu'), null);

  // Case C: Non-session explicit root is passed through to fs/act.
  h.instance.newTab({ root: '/other', dir: '' });
  await settle();
  h.makeContainersVisible();
  h.row('archive.zip').fire('dblclick');
  await h.flushPreview('archive.zip');
  assert.equal(h.posts.length, 3);
  assert.deepEqual(h.posts[2], { route: 'fs/act', body: { path: 'archive.zip', action: 'open', root: '/other' } });
  h.instance.destroy();
});

test('executable/script extensions (case-insensitive, binary or >2 MiB) fall back to row menu instead of auto-open; explicit Open still works', async () => {
  const execFiles = [
    'app.exe', 'RUN.EXE', 'build.bat', 'BUILD.Bat', 'script.cmd', 'setup.ps1', 'DEPLOY.PS1',
    'installer.msi', 'shortcut.lnk', 'macro.vbs', 'old.com', 'saver.scr', 'dialog.hta',
    'deploy.sh', 'RUN.SH', 'Bundle.App', 'launch.command', 'dos.pif', 'enc.jse',
    'task.wsf', 'opts.wsh', 'keys.reg', 'snap.msc',
  ];
  const entries = [
    { name: 'dir.exe', dir: true },
    ...execFiles.map(name => ({ name })),
    { name: 'ok.sh' }, // small text script remains previewable
  ];
  const h = makeHarness({
    localViewer: true,
    entries(dir) {
      if (dir === 'dir.exe') return [{ name: 'payload.bin' }];
      return entries;
    },
    previewResponses(path) {
      if (path === 'ok.sh') return { kind: 'text', text: '#!/bin/sh\necho ok\n' };
      if (path === 'DEPLOY.PS1') return { kind: 'none', reason: 'Exceeds 2 MiB' }; // >2 MiB script without binary reason_code
      return { kind: 'none', reason: 'Binary file', reason_code: 'binary' };
    },
  });
  h.instance.shown();
  await settle();
  h.makeContainersVisible();

  for (let i = 0; i < execFiles.length; i++) {
    const name = execFiles[i];
    const row = h.row(name);
    if (i % 2 === 0) row.fire('dblclick');
    else row.fire('keydown', { key: 'Enter' });
    await h.flushPreview(name);
    assert.equal(h.posts.length, 0, name + ' must never auto-execute via fs/act');
    assert.equal(h.previews.length, 0, name + ' must not open .pv-none card');
    const menu = h.doc.querySelector('.fmenu');
    assert.ok(menu, name + ' must fall back to row context menu');
    const labels = menu.querySelectorAll('button').map(b => b.textContent);
    assert.equal(labels.includes('js.preview.openext'), true, name + ' menu offers explicit Open');
    assert.equal(labels.includes('js.files.menu.reveal'), true, name + ' menu offers Show in Explorer');
    if (name === 'app.exe' || name === 'DEPLOY.PS1') {
      const openBtn = menu.querySelectorAll('button').find(b => b.textContent === 'js.preview.openext');
      openBtn.click();
      assert.equal(h.posts.at(-1).route, 'fs/act');
      assert.deepEqual(h.posts.at(-1).body, { path: name, action: 'open', root: '' });
      h.posts.length = 0;
    } else {
      h.closeMenu();
    }
    assert.equal(h.doc.querySelector('.fmenu'), null);
  }

  // Directory ending in .exe must NOT suppress non-executable files inside it.
  h.instance.navigate('dir.exe');
  await settle();
  h.makeContainersVisible();
  h.row('dir.exe/payload.bin').fire('dblclick');
  await h.flushPreview('dir.exe/payload.bin');
  assert.equal(h.posts.length, 1);
  assert.deepEqual(h.posts[0], { route: 'fs/act', body: { path: 'dir.exe/payload.bin', action: 'open', root: '' } });

  // Small UTF-8 script (kind === "text") still previews cleanly.
  h.instance.navigate('');
  await settle();
  h.row('ok.sh').fire('dblclick');
  await h.flushPreview('ok.sh');
  assert.equal(h.posts.length, 1);
  assert.equal(h.previews.length, 1);
  assert.equal(h.previews[0].path, 'ok.sh');
  h.instance.destroy();
});

test('remote / LAN viewer (data-sysopen off) never auto-opens and preserves existing fallback', async () => {
  const h = makeHarness({
    localViewer: false,
    entries: [{ name: 'archive.zip' }, { name: 'large.txt' }, { name: 'tool.exe' }],
    previewResponses: {
      'archive.zip': { kind: 'none', reason: 'Binary file', reason_code: 'binary' },
      'large.txt': { kind: 'none', reason: 'File exceeds 2 MiB preview limit' },
      'tool.exe': { kind: 'none', reason: 'Binary file', reason_code: 'binary' },
    },
  });
  h.instance.shown();
  await settle();
  h.makeContainersVisible();

  // Single click keeps row selection only.
  for (const name of ['archive.zip', 'large.txt']) {
    h.row(name).fire('click');
    await h.flushPreview(name);
    assert.equal(h.instance.selection()?.path, name);
    assert.equal(h.posts.length, 0);
    assert.equal(h.previews.length, 0);
    assert.equal(h.doc.querySelector('.fmenu'), null);
  }

  // Double-click / Enter on binary preserves row-menu fallback without Open/Reveal items or fs/act.
  h.row('archive.zip').fire('dblclick');
  await h.flushPreview('archive.zip');
  assert.equal(h.posts.length, 0);
  const menu = h.doc.querySelector('.fmenu');
  assert.ok(menu, 'remote binary dblclick opens row menu');
  assert.equal(menu.querySelectorAll('button').some(b => b.textContent === 'js.preview.openext' || b.textContent === 'js.files.menu.reveal'), false);
  h.closeMenu();

  // Double-click / Enter on >2 MiB preserves .pv-none preview fallback without fs/act.
  h.row('large.txt').fire('keydown', { key: 'Enter' });
  await h.flushPreview('large.txt');
  assert.equal(h.posts.length, 0);
  assert.equal(h.previews.length, 1);
  assert.deepEqual(h.previews[0].path, 'large.txt');
  assert.equal(h.previews[0].o.answer.kind, 'none');
  assert.equal(h.previews[0].o.keep, true);
  h.instance.destroy();
});

test('search hits respect single-click no-op, dblclick/Enter system open, and executable row-menu fallback', async () => {
  const h = makeHarness({
    localViewer: true,
    entries: [],
    previewResponses: {
      'deep/data.bin': { kind: 'none', reason: 'Binary file', reason_code: 'binary' },
      'deep/install.Ps1': { kind: 'none', reason: 'Exceeds 2 MiB' },
    },
  });
  h.instance.shown();
  await settle();
  h.makeContainersVisible();

  const q = h.container.querySelector('.fq');
  q.value = 'deep';
  q.fire('input');
  await h.flushTimers();
  await h.flushSearch([{ path: 'deep/data.bin' }, { path: 'deep/install.Ps1' }]);

  const hitsEl = h.container.querySelector('.fhits');
  const hitRows = hitsEl.querySelectorAll('.hit');
  assert.equal(hitRows.length, 2);

  // Single-click search hit -> no card, no menu, no fs/act.
  hitsEl.fire('click', { target: hitRows[0] });
  await h.flushPreview('deep/data.bin');
  assert.equal(h.previews.length, 0);
  assert.equal(h.doc.querySelector('.fmenu'), null);
  assert.equal(h.posts.length, 0);

  // Double-click non-executable hit -> fs/act open.
  hitsEl.fire('dblclick', { target: hitRows[0] });
  await h.flushPreview('deep/data.bin');
  assert.equal(h.posts.length, 1);
  assert.deepEqual(h.posts[0], { route: 'fs/act', body: { path: 'deep/data.bin', action: 'open', root: '' } });

  // Enter on executable hit -> falls back to row menu without fs/act.
  hitRows[1].focus();
  hitsEl.fire('keydown', { key: 'Enter' });
  await h.flushPreview('deep/install.Ps1');
  assert.equal(h.posts.length, 1);
  assert.ok(h.doc.querySelector('.fmenu'));
  h.closeMenu();
  h.instance.destroy();
});
