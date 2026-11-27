const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const assetsDir = path.join(__dirname, '..', 'assets');
const enTable = JSON.parse(fs.readFileSync(path.join(__dirname, '..', '..', 'wsi18n', 'tables', 'en.json'), 'utf8'));
const koTable = JSON.parse(fs.readFileSync(path.join(__dirname, '..', '..', 'wsi18n', 'tables', 'ko.json'), 'utf8'));

function makeClassList(el) {
  const tokens = () => String(el._className || '').split(/\s+/).filter(Boolean);
  const save = (arr) => { el._className = arr.join(' '); };
  return {
    add(...names) {
      const s = new Set(tokens());
      names.forEach((n) => s.add(n));
      save(Array.from(s));
    },
    remove(...names) {
      const drop = new Set(names);
      save(tokens().filter((n) => !drop.has(n)));
    },
    toggle(n, force) {
      const has = tokens().includes(n);
      const want = force === undefined ? !has : !!force;
      if (want) this.add(n);
      else this.remove(n);
      return want;
    },
    contains(n) { return tokens().includes(n); },
  };
}

function parseSimpleSelector(sel) {
  const parts = String(sel || '').split(',').map((s) => s.trim()).filter(Boolean);
  return (el) => parts.some((part) => {
    let rem = part;
    const roleMatch = rem.match(/\[role="([^"]+)"\]/);
    if (roleMatch) {
      if (el.getAttribute('role') !== roleMatch[1]) return false;
      rem = rem.replace(roleMatch[0], '');
    }
    const clsMatches = rem.match(/\.[a-zA-Z0-9_-]+/g) || [];
    for (const c of clsMatches) {
      if (!el.classList.contains(c.slice(1))) return false;
      rem = rem.replace(c, '');
    }
    if (rem && /^[a-zA-Z0-9]+$/.test(rem)) {
      if (el.tagName !== rem.toUpperCase()) return false;
    }
    return clsMatches.length > 0 || !!roleMatch || !!(rem && /^[a-zA-Z0-9]+$/.test(part));
  });
}

function createHarness(opts = {}) {
  const table = opts.lang === 'ko' ? koTable : enTable;
  const docListeners = new Map();
  const winListeners = new Map();
  const toasts = [];
  const clickedLinks = [];
  const fetchCalls = [];
  let execCopyResult = opts.execCopyResult ?? false;
  const execCopiedValues = [];

  function FakeEl(tag, ownerDoc) {
    this.tagName = String(tag).toUpperCase();
    this.ownerDocument = ownerDoc;
    this.parentNode = null;
    this.parentElement = null;
    this.children = [];
    this._attrs = {};
    this._listeners = new Map();
    this._className = '';
    this.hidden = false;
    this.readOnly = false;
    this.type = '';
    this.value = '';
    this.textContent = '';
    this.style = {};
    this.id = '';
    this.title = '';
    this._rect = opts.triggerRect || { left: 200, top: 40, right: 280, bottom: 64, width: 80, height: 24 };
    this.classList = makeClassList(this);
    Object.defineProperty(this, 'className', {
      get: () => this._className,
      set: (v) => { this._className = String(v || ''); },
    });
    Object.defineProperty(this, 'innerHTML', {
      get: () => this._innerHTML || '',
      set: (v) => {
        this._innerHTML = String(v || '');
        if (this._innerHTML === '') {
          this.children.forEach((c) => { c.parentNode = null; c.parentElement = null; });
          this.children = [];
        }
      },
    });
    Object.defineProperty(this, 'firstElementChild', { get: () => this.children[0] || null });
    Object.defineProperty(this, 'firstChild', { get: () => this.children[0] || null });
    Object.defineProperty(this, 'isConnected', {
      get: () => {
        let cur = this;
        while (cur) {
          if (cur === ownerDoc.body || cur === ownerDoc.documentElement) return true;
          cur = cur.parentNode;
        }
        return false;
      },
    });
    Object.defineProperty(this, 'offsetWidth', { get: () => 260 });
    Object.defineProperty(this, 'offsetHeight', { get: () => 76 });
    Object.defineProperty(this, 'offsetTop', { get: () => 0 });
  }
  Object.assign(FakeEl.prototype, {
    appendChild(child) {
      if (child.parentNode && child.parentNode.children) {
        child.parentNode.children = child.parentNode.children.filter((c) => c !== child);
      }
      child.parentNode = this;
      child.parentElement = this;
      this.children.push(child);
      return child;
    },
    removeChild(child) {
      this.children = this.children.filter((c) => c !== child);
      if (child.parentNode === this) { child.parentNode = null; child.parentElement = null; }
      return child;
    },
    remove() { if (this.parentNode) this.parentNode.removeChild(this); },
    setAttribute(k, v) {
      this._attrs[k] = String(v);
      if (k === 'id') this.id = String(v);
    },
    getAttribute(k) { return Object.prototype.hasOwnProperty.call(this._attrs, k) ? this._attrs[k] : null; },
    hasAttribute(k) { return Object.prototype.hasOwnProperty.call(this._attrs, k); },
    removeAttribute(k) { delete this._attrs[k]; },
    addEventListener(type, fn, cap) {
      if (!this._listeners.has(type)) this._listeners.set(type, []);
      this._listeners.get(type).push({ fn, capture: !!(typeof cap === 'boolean' ? cap : cap && cap.capture) });
    },
    dispatchEvent(ev) {
      ev.target = ev.target || this;
      ev.currentTarget = this;
      for (const h of this._listeners.get(ev.type) || []) h.fn.call(this, ev);
      return !ev.defaultPrevented;
    },
    click() {
      if (this.tagName === 'A') clickedLinks.push({ href: this.href, download: this.download });
      const ev = {
        type: 'click',
        target: this,
        currentTarget: this,
        defaultPrevented: false,
        preventDefault() { this.defaultPrevented = true; },
        stopPropagation() {},
      };
      for (const h of (docListeners.get('click') || [])) {
        if (h.capture) h.fn(ev);
      }
      this.dispatchEvent(ev);
      for (const h of (docListeners.get('click') || [])) {
        if (!h.capture) h.fn(ev);
      }
    },
    focus() { doc.activeElement = this; },
    select() { this._selected = true; },
    getBoundingClientRect() { return this._rect; },
    contains(other) {
      let cur = other;
      while (cur) {
        if (cur === this) return true;
        cur = cur.parentNode;
      }
      return false;
    },
    closest(sel) {
      const pred = parseSimpleSelector(sel);
      let cur = this;
      while (cur && cur.tagName) {
        if (pred(cur)) return cur;
        cur = cur.parentNode;
      }
      return null;
    },
    querySelectorAll(sel) {
      const pred = parseSimpleSelector(sel);
      const out = [];
      const walk = (n) => {
        for (const c of n.children || []) {
          if (pred(c)) out.push(c);
          walk(c);
        }
      };
      walk(this);
      return out;
    },
    querySelector(sel) { return this.querySelectorAll(sel)[0] || null; },
  });

  const doc = {
    documentElement: null,
    body: null,
    activeElement: null,
    createElement(tag) { return new FakeEl(tag, doc); },
    addEventListener(type, fn, cap) {
      if (!docListeners.has(type)) docListeners.set(type, []);
      docListeners.get(type).push({ fn, capture: !!(typeof cap === 'boolean' ? cap : cap && cap.capture) });
    },
    execCommand(cmd) {
      if (cmd !== 'copy') return false;
      const ta = doc.body.children.find((c) => c.tagName === 'TEXTAREA' && c._selected) ||
        doc.body.querySelector('.d-bexp-fb-text');
      if (!execCopyResult) return false;
      if (ta) execCopiedValues.push(ta.value);
      return true;
    },
    querySelector(sel) { return doc.body.querySelector(sel); },
    querySelectorAll(sel) { return doc.body.querySelectorAll(sel); },
    getElementById(id) {
      const walk = (n) => {
        for (const c of n.children || []) {
          if (c.id === id) return c;
          const r = walk(c);
          if (r) return r;
        }
        return null;
      };
      return walk(doc.body);
    },
  };
  doc.documentElement = new FakeEl('html', doc);
  doc.body = new FakeEl('body', doc);
  doc.documentElement.appendChild(doc.body);

  const root = doc.documentElement;
  root.setAttribute('data-rid', opts.rid || 'sess-1');
  const cardBoard = new FakeEl('div', doc);
  cardBoard.id = 'board-mount';
  doc.body.appendChild(cardBoard);

  class FakeBlob {
    constructor(parts, o) {
      this.parts = parts;
      this.type = o && o.type;
    }
    text() { return Promise.resolve(this.parts.join('')); }
  }
  class FakeClipboardItem {
    constructor(items) { this.items = items; }
  }

  const sandbox = {
    document: doc,
    doc,
    root,
    rid: opts.rid || 'sess-1',
    cardIdentity: opts.rid || 'sess-1',
    mode: 'board',
    framed: false,
    region: (name) => (name === 'board' ? cardBoard : null),
    frag: () => ({ childNodes: [] }),
    worktree: false,
    live: false,
    cardBoard,
    fpane: null,
    chead: null,
    grid: null,
    filesSheet: null,
    toast: (msg) => toasts.push({ msg, kind: 'info' }),
    toggleStageFill: () => {},
    load: () => null,
    save: () => {},
    gwRender: () => {},
    post: () => {},
    mountFrames: () => {},
    boardBoxes: () => [],
    watchBoard: () => {},
    bandBoard: () => {},
    restoreOpen: () => {},
    mermaid: () => {},
    q: () => [],
    all: () => [],
    $: (id) => doc.getElementById(id),
    tabShown: () => true,
    setTab: () => {},
    galMode: () => 'free',
    PIN_WIDE: { matches: true },
    wsT(key, ...args) {
      let msg = table[key] || enTable[key] || key;
      args.forEach((a, i) => { msg = msg.replace('{' + i + '}', String(a)); });
      return msg;
    },
    requestAnimationFrame: (fn) => { fn(); return 1; },
    cancelAnimationFrame: () => {},
    setTimeout,
    clearTimeout,
    Blob: opts.noBlob ? undefined : FakeBlob,
    ClipboardItem: opts.noClipboardItem ? undefined : FakeClipboardItem,
    Promise,
    Error,
    navigator: opts.navigator || {},
    window: null,
  };

  const win = {
    innerWidth: 1024,
    innerHeight: 768,
    isSecureContext: opts.isSecureContext !== false,
    Blob: sandbox.Blob,
    ClipboardItem: sandbox.ClipboardItem,
    addEventListener(type, fn) {
      if (!winListeners.has(type)) winListeners.set(type, []);
      winListeners.get(type).push(fn);
    },
    fetch: opts.fetchImpl || (async (url, init) => {
      fetchCalls.push({ url, init });
      return {
        ok: true,
        status: 200,
        async text() {
          return JSON.stringify(opts.exportPayload || {
            path: '/tmp/ws/board-exports/whiteboard_repo_sess-1_261127_1415.zip',
            filename: 'whiteboard_repo_sess-1_261127_1415.zip',
            reply_id: 'sess-1',
            mailbox_ref: '<mailbox id="id:sess-1" dir="/repo/wt" root="/repo" branch="develop" harness="claude">Alpha</mailbox>',
            mailbox: {
              id: 'id:sess-1',
              reply_id: 'sess-1',
              dir: '/repo/wt',
              root: '/repo',
              branch: 'develop',
              harness: 'claude',
              label: 'Alpha',
            },
          });
        },
      };
    }),
    WsToast: {
      show(msg, o) { toasts.push({ msg, kind: (o && o.kind) || 'info' }); },
    },
    WsCards: {
      mount(mountEl, mountOpts) {
        const toolbar = doc.createElement('div');
        toolbar.className = 'board-toolbar';
        const acts = doc.createElement('div');
        acts.className = 'board-acts';
        const termSlot = doc.createElement('span');
        termSlot.className = 'board-term-slot';
        const webSlot = doc.createElement('span');
        webSlot.className = 'board-web-slot';
        acts.appendChild(termSlot);
        acts.appendChild(webSlot);
        if (mountOpts.toolbar.download) {
          const dl = doc.createElement('button');
          dl.type = 'button';
          dl.className = 'board-action board-download';
          dl.setAttribute('aria-haspopup', 'menu');
          dl.setAttribute('aria-expanded', 'false');
          dl.addEventListener('click', (e) => mountOpts.toolbar.download(e, dl));
          acts.appendChild(dl);
        }
        toolbar.appendChild(acts);
        mountEl.appendChild(toolbar);
      },
    },
  };
  sandbox.window = win;
  win.document = doc;
  vm.createContext(sandbox);

  const boardSrc = fs.readFileSync(path.join(assetsDir, 'dashboard-board.js'), 'utf8');
  const wrapped = '(function(){\n' + boardSrc + '\n})();';
  vm.runInContext(wrapped, sandbox, { filename: 'dashboard-board.js' });

  return {
    doc,
    win,
    toasts,
    clickedLinks,
    fetchCalls,
    execCopiedValues,
    setExecCopyResult(v) { execCopyResult = v; },
    dispatchDocKey(key, target) {
      const ev = {
        key,
        target: target || doc.activeElement || doc.body,
        defaultPrevented: false,
        preventDefault() { this.defaultPrevented = true; },
        stopPropagation() {},
      };
      for (const h of docListeners.get('keydown') || []) {
        if (h.capture) h.fn(ev);
      }
      return ev;
    },
    clickOutside() {
      const outside = doc.createElement('div');
      doc.body.appendChild(outside);
      outside.click();
    },
  };
}

test('cards.js wires download trigger with menu ARIA attrs while keeping .board-acts child layout intact', () => {
  const cardsSrc = fs.readFileSync(path.join(assetsDir, 'cards.js'), 'utf8');
  assert.match(cardsSrc, /b\.addEventListener\("click", function \(e\) \{ if \(run\) run\(e, b\); \}\);/);
  assert.match(cardsSrc, /download\.setAttribute\("aria-haspopup", "menu"\);/);
  assert.match(cardsSrc, /download\.setAttribute\("aria-expanded", "false"\);/);
});

test('toolbar download button opens 2-item context menu, handles toggle, Escape, Arrows, outside click, and local download', () => {
  const h = createHarness({ lang: 'en' });
  const btn = h.doc.querySelector('.board-download');
  assert.ok(btn, 'board-download button exists');
  assert.equal(btn.getAttribute('aria-expanded'), 'false');

  btn.click();
  const pop = h.doc.querySelector('.d-bexp-pop');
  assert.ok(pop, 'popover appended to body');
  assert.equal(pop.hidden, false);
  assert.equal(btn.getAttribute('aria-expanded'), 'true');
  const items = pop.querySelectorAll('[role="menuitem"]');
  assert.equal(items.length, 2);
  assert.equal(items[0].textContent, 'Copy handoff prompt for another session');
  assert.equal(items[1].textContent, 'Download local file');
  assert.equal(h.doc.activeElement, items[0]);

  const evDown = h.dispatchDocKey('ArrowDown', items[0]);
  assert.equal(evDown.defaultPrevented, true);
  assert.equal(h.doc.activeElement, items[1]);

  const evUp = h.dispatchDocKey('ArrowUp', items[1]);
  assert.equal(evUp.defaultPrevented, true);
  assert.equal(h.doc.activeElement, items[0]);

  const evEsc = h.dispatchDocKey('Escape', items[0]);
  assert.equal(evEsc.defaultPrevented, true);
  assert.equal(pop.hidden, true);
  assert.equal(btn.getAttribute('aria-expanded'), 'false');
  assert.equal(h.doc.activeElement, btn);

  // Reopen and close on toggle click
  btn.click();
  assert.equal(pop.hidden, false);
  btn.click();
  assert.equal(pop.hidden, true);

  // Reopen and close on outside click
  btn.click();
  assert.equal(pop.hidden, false);
  h.clickOutside();
  assert.equal(pop.hidden, true);

  // Local download item preserves /s/<rid>/board.zip without POSTing board-export
  btn.click();
  items[1].click();
  assert.equal(h.clickedLinks.length, 1);
  assert.equal(h.clickedLinks[0].href, '/s/sess-1/board.zip');
  assert.equal(h.clickedLinks[0].download, '');
  assert.equal(h.fetchCalls.length, 0);
});

test('handoff prompt is always fixed English even when UI language is Korean, and matches .snm MailboxRef', () => {
  const h = createHarness({ lang: 'ko' });
  const btn = h.doc.querySelector('.board-download');
  btn.click();
  const items = h.doc.querySelector('.d-bexp-pop').querySelectorAll('[role="menuitem"]');
  assert.equal(items[0].textContent, '다른 세션에 넘길 프롬프트 복사');
  assert.equal(items[1].textContent, '로컬 파일로 다운로드');

  const tagSameRoot = h.win.WsBoardExport.buildMailboxTag({
    reply_id: 'abc',
    dir: '/repo',
    root: '/repo',
    branch: 'develop',
    harness: 'claude',
    label: 'A & B <1>',
  });
  assert.equal(tagSameRoot, '<mailbox id="id:abc" dir="/repo" branch="develop" harness="claude">A &amp; B &lt;1&gt;</mailbox>');

  const tagWorktreeEmptyLabel = h.win.WsBoardExport.buildMailboxTag({
    reply_id: 'xyz',
    dir: 'D:\\work\\wt1',
    root: 'd:\\work\\repo',
    branch: 'feat/x',
    harness: 'codex',
    label: '',
  });
  assert.equal(tagWorktreeEmptyLabel, '<mailbox id="id:xyz" dir="D:\\work\\wt1" root="d:\\work\\repo" branch="feat/x" harness="codex"/>');

  const prompt = h.win.WsBoardExport.buildPrompt({
    path: '/abs/path/board.zip',
    reply_id: 'sess-9',
    mailbox_ref: '<mailbox id="id:sess-9" dir="/repo">Lead</mailbox>',
    mailbox: { reply_id: 'sess-9', dir: '/repo', label: 'Lead' },
  });
  assert.ok(prompt.includes('/abs/path/board.zip'));
  assert.ok(prompt.includes('<mailbox id="id:sess-9" dir="/repo">Lead</mailbox>'));
  assert.ok(prompt.includes('whiteboard.load'));
  assert.ok(prompt.includes('mailbox.send'));
  assert.ok(prompt.includes('id:sess-9'));
  assert.ok(prompt.includes('already been handed over'));
  assert.ok(!/[가-힣]/.test(prompt), 'prompt must stay English regardless of Korean locale');

  // When replyID is empty, never emit broken "id:" or "<mailbox ...>"
  const noRidPrompt = h.win.WsBoardExport.buildPrompt({ path: '/abs/path/board.zip' });
  assert.ok(noRidPrompt.includes('/abs/path/board.zip'));
  assert.ok(noRidPrompt.includes('whiteboard.load'));
  assert.ok(!noRidPrompt.includes('id:'), 'must not emit broken id: prefix');
  assert.ok(!noRidPrompt.includes('<mailbox'), 'must not emit broken mailbox tag');
});

test('uses ClipboardItem promise synchronously inside click gesture when supported', async () => {
  const written = [];
  let writeCalledSync = false;
  let resolveFetch;
  const h = createHarness({
    lang: 'ko',
    navigator: {
      clipboard: {
        write(items) {
          writeCalledSync = true;
          written.push(items);
          return items[0].items['text/plain'].then((blob) => blob.text());
        },
      },
    },
    fetchImpl: () => new Promise((res) => { resolveFetch = res; }),
  });

  h.doc.querySelector('.board-download').click();
  const handoffBtn = h.doc.querySelector('.d-bexp-pop').querySelectorAll('[role="menuitem"]')[0];
  const pending = h.win.WsBoardExport.handoff(handoffBtn);
  assert.equal(writeCalledSync, true, 'navigator.clipboard.write must run synchronously before export finishes');
  assert.equal(written.length, 1);

  resolveFetch({
    ok: true,
    status: 200,
    text: async () => JSON.stringify({
      path: '/d/board-exports/whiteboard_app_261127.zip',
      reply_id: 'r1',
      mailbox_ref: '<mailbox id="id:r1" dir="/app"/>',
      mailbox: { id: 'id:r1', reply_id: 'r1', dir: '/app' },
    }),
  });
  await pending;

  const copiedText = await written[0][0].items['text/plain'].then((b) => b.text());
  assert.ok(copiedText.includes('/d/board-exports/whiteboard_app_261127.zip'));
  assert.ok(copiedText.includes('<mailbox id="id:r1" dir="/app"/>'));
  assert.equal(h.toasts.length, 1);
  assert.equal(h.toasts[0].msg, '다른 세션에 넘길 프롬프트를 복사했어요');
  assert.equal(h.doc.querySelector('.d-bexp-fallback'), null);
});

test('falls back to writeText -> execCommand -> copyable fallback UI with retry and close when clipboard fails', async () => {
  // 1) write rejects -> writeText succeeds
  let writeTextGot = '';
  const h1 = createHarness({
    navigator: {
      clipboard: {
        write: () => Promise.reject(new Error('NotAllowedError')),
        writeText: (s) => { writeTextGot = s; return Promise.resolve(); },
      },
    },
  });
  await h1.win.WsBoardExport.handoff(h1.doc.querySelector('.board-download'));
  assert.ok(writeTextGot.includes('whiteboard.load'));
  assert.equal(h1.toasts[0].msg, 'Copied handoff prompt for another session');
  assert.equal(h1.doc.querySelector('.d-bexp-fallback'), null);

  // 2) write + writeText fail -> execCommand succeeds
  const h2 = createHarness({
    execCopyResult: true,
    navigator: {
      clipboard: {
        write: () => Promise.reject(new Error('denied')),
        writeText: () => Promise.reject(new Error('denied')),
      },
    },
  });
  await h2.win.WsBoardExport.handoff(h2.doc.querySelector('.board-download'));
  assert.equal(h2.execCopiedValues.length, 1);
  assert.ok(h2.execCopiedValues[0].includes('<mailbox id="id:sess-1"'));
  assert.equal(h2.toasts[0].msg, 'Copied handoff prompt for another session');
  assert.equal(h2.doc.querySelector('.d-bexp-fallback'), null);

  // 3) all automatic clipboard writes blocked -> copyable fallback UI appears; retry succeeds and closes it
  const h3 = createHarness({
    execCopyResult: false,
    navigator: {
      clipboard: {
        write: () => Promise.reject(new Error('denied')),
        writeText: () => Promise.reject(new Error('denied')),
      },
    },
  });
  await h3.win.WsBoardExport.handoff(h3.doc.querySelector('.board-download'));
  const fb = h3.doc.querySelector('.d-bexp-fallback');
  assert.ok(fb, 'copyable fallback UI should be shown when all clipboard APIs fail');
  const ta = fb.querySelector('.d-bexp-fb-text');
  assert.equal(ta.readOnly, true);
  assert.ok(ta.value.includes('whiteboard.load'));
  assert.ok(ta.value.includes('mailbox.send'));

  // Retry succeeds once execCommand allows user-click copy
  h3.setExecCopyResult(true);
  fb.querySelector('.d-bexp-fb-copy').click();
  await new Promise((r) => setTimeout(r, 0));
  assert.equal(h3.doc.querySelector('.d-bexp-fallback'), null);
  assert.equal(h3.toasts.at(-1).msg, 'Copied handoff prompt for another session');

  // Escape or close button also dismisses fallback UI
  h3.setExecCopyResult(false);
  await h3.win.WsBoardExport.handoff(h3.doc.querySelector('.board-download'));
  const fb2 = h3.doc.querySelector('.d-bexp-fallback');
  assert.ok(fb2);
  fb2.querySelector('.d-bexp-fb-close').click();
  assert.equal(h3.doc.querySelector('.d-bexp-fallback'), null);
});

test('early clipboard.write rejection waits for export and falls back cleanly', async () => {
  let writeTextGot = '';
  let resolveFetch;
  const h = createHarness({
    navigator: {
      clipboard: {
        write: () => Promise.reject(new Error('immediate NotAllowedError')),
        writeText: (s) => { writeTextGot = s; return Promise.resolve(); },
      },
    },
    fetchImpl: () => new Promise((res) => { resolveFetch = res; }),
  });

  const p = h.win.WsBoardExport.handoff(h.doc.querySelector('.board-download'));
  await new Promise((r) => setTimeout(r, 0));
  resolveFetch({
    ok: true,
    status: 200,
    text: async () => JSON.stringify({
      path: '/d/board-exports/whiteboard_early_261127.zip',
      reply_id: 'r2',
      mailbox_ref: '<mailbox id="id:r2" dir="/app"/>',
    }),
  });
  await p;
  assert.ok(writeTextGot.includes('/d/board-exports/whiteboard_early_261127.zip'));
  assert.equal(h.toasts[0].msg, 'Copied handoff prompt for another session');
  assert.equal(h.doc.querySelector('.d-bexp-fallback'), null);
});

test('export HTTP error shows warning toast and does NOT show clipboard fallback UI', async () => {
  const h = createHarness({
    navigator: {
      clipboard: {
        write(items) {
          return items[0].items['text/plain'];
        },
      },
    },
    fetchImpl: async () => ({
      ok: false,
      status: 500,
      text: async () => 'export failed: disk full',
    }),
  });

  await h.win.WsBoardExport.handoff(h.doc.querySelector('.board-download'));
  assert.equal(h.doc.querySelector('.d-bexp-fallback'), null, 'must never trigger clipboard fallback on export HTTP failure');
  assert.equal(h.toasts.length, 1);
  assert.equal(h.toasts[0].kind, 'warn');
  assert.equal(h.toasts[0].msg, 'Board export failed: export failed: disk full');
});
