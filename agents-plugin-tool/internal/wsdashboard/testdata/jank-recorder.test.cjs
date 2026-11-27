const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const ASSETS_DIR = path.join(__dirname, '..', 'assets');
const jankSource = fs.readFileSync(path.join(ASSETS_DIR, 'dashboard-jank.js'), 'utf8');
const shellSource = fs.readFileSync(path.join(ASSETS_DIR, 'shell.js'), 'utf8');

function createFakeDOM(opts = {}) {
  function makeEl(tag) {
    const el = {
      tagName: String(tag || 'DIV').toUpperCase(),
      id: '',
      className: '',
      type: '',
      tabIndex: 0,
      hidden: false,
      disabled: false,
      attrs: Object.create(null),
      children: [],
      parentNode: null,
      isConnected: true,
      listeners: Object.create(null),
      focused: false,
      _text: '',
      setAttribute(k, v) {
        this.attrs[k] = String(v);
        if (k === 'id') this.id = String(v);
      },
      getAttribute(k) {
        return Object.prototype.hasOwnProperty.call(this.attrs, k) ? this.attrs[k] : null;
      },
      hasAttribute(k) {
        return Object.prototype.hasOwnProperty.call(this.attrs, k);
      },
      appendChild(child) {
        child.parentNode = this;
        this.children.push(child);
        return child;
      },
      addEventListener(type, fn) {
        if (!this.listeners[type]) this.listeners[type] = [];
        this.listeners[type].push(fn);
      },
      dispatch(type, ev = {}) {
        const event = Object.assign({ target: this, preventDefault() {} }, ev);
        const list = this.listeners[type] || [];
        for (const fn of list) fn(event);
      },
      focus() {
        this.focused = true;
        doc.activeElement = this;
      },
      querySelectorAll(sel) {
        const cls = sel.startsWith('.') ? sel.slice(1) : null;
        const out = [];
        function walk(node) {
          for (const ch of node.children || []) {
            if (cls && String(ch.className || '').split(/\s+/).includes(cls)) out.push(ch);
            walk(ch);
          }
        }
        walk(this);
        return out;
      },
      querySelector(sel) {
        return this.querySelectorAll(sel)[0] || null;
      }
    };
    Object.defineProperty(el, 'textContent', {
      get() {
        if (this.children.length === 0) return this._text;
        return this._text + this.children.map((c) => c.textContent).join('');
      },
      set(v) {
        this._text = String(v == null ? '' : v);
        this.children = [];
      }
    });
    return el;
  }

  const root = makeEl('html');
  if (opts.mode) root.setAttribute('data-mode', opts.mode);
  if (opts.sysopen !== false) root.setAttribute('data-sysopen', '1');
  const body = makeEl('body');
  const triggerBtn = makeEl('button');
  triggerBtn.id = 'gear';
  body.appendChild(triggerBtn);

  const doc = {
    documentElement: root,
    body,
    activeElement: triggerBtn,
    createElement(tag) { return makeEl(tag); }
  };
  return { doc, body, triggerBtn };
}

function loadJankEnv(opts = {}) {
  let perfClock = typeof opts.startClock === 'number' ? opts.startClock : 1000;
  let wallClock = typeof opts.startWall === 'number' ? opts.startWall : 1700000000000;
  let observerRegistered = null;
  const { doc, body, triggerBtn } = createFakeDOM(opts);
  const toasts = [];

  class FakePerformanceObserver {
    constructor(cb) {
      this._cb = cb;
      observerRegistered = this;
      this.options = null;
      this.disconnected = false;
    }
    observe(options) {
      if (opts.throwOnObserve && opts.throwOnObserve.includes(options.type)) {
        throw new Error('unsupported type ' + options.type);
      }
      this.options = options;
    }
    disconnect() {
      this.disconnected = true;
    }
  }
  FakePerformanceObserver.supportedEntryTypes = opts.supportedEntryTypes !== undefined
    ? opts.supportedEntryTypes
    : ['long-animation-frame', 'longtask'];

  const ctx = {
    console,
    Math,
    Date: Object.assign(function (...a) { return a.length ? new Date(...a) : new Date(wallClock); }, { now: () => wallClock }),
    JSON,
    Array,
    Object,
    String,
    Number,
    RegExp,
    Error,
    isFinite,
    performance: { now: () => perfClock },
    PerformanceObserver: opts.noObserver ? undefined : FakePerformanceObserver,
    document: doc,
    location: { pathname: opts.pathname || '/shell', origin: 'http://127.0.0.1:8080' },
    window: null,
    wsSourceMap: opts.sourceMap || null,
    WsToast: { show(msg) { toasts.push(msg); } },
    wsT(key, ...args) {
      return args.length ? key + ':' + args.join(',') : key;
    }
  };
  ctx.window = ctx;
  ctx.globalThis = ctx;

  vm.runInNewContext(jankSource, ctx, { filename: 'dashboard-jank.js' });
  if (opts.withShell) {
    vm.runInNewContext(shellSource, ctx, { filename: 'shell.js' });
  }

  return {
    WsJank: ctx.WsJank,
    WsShell: ctx.WsShell,
    ctx,
    doc,
    body,
    triggerBtn,
    toasts,
    getObserver: () => observerRegistered,
    advance(ms) {
      perfClock += ms;
      wallClock += ms;
    },
    emitObserver(entries) {
      assert.ok(observerRegistered, 'expected registered PerformanceObserver');
      observerRegistered._cb({ getEntries: () => entries });
    }
  };
}

test('dashboard-jank.js preserves strict zero-IO, zero-timer, zero-layout-read invariants', () => {
  assert.doesNotMatch(jankSource, /\bsetInterval\b|\bsetTimeout\b/);
  assert.doesNotMatch(jankSource, /\blocalStorage\b|\bsessionStorage\b|\bindexedDB\b|\bfetch\b|\bXMLHttpRequest\b/);
  assert.doesNotMatch(jankSource, /\bgetBoundingClientRect\b|\bgetClientRects\b|\boffsetHeight\b|\boffsetWidth\b|\bscrollHeight\b|\bgetComputedStyle\b/);
  assert.doesNotMatch(jankSource, /new\s+Error\s*\(/);
});

test('ignores frames, longtasks, and spans strictly below 400ms threshold', () => {
  const env = loadJankEnv();
  assert.equal(env.WsJank.mode(), 'loaf');
  assert.deepEqual(JSON.parse(JSON.stringify(env.getObserver().options)), { type: 'long-animation-frame', buffered: false });

  let notified = 0;
  env.WsJank.onChange(() => { notified++; });

  const tok = env.WsJank.begin('board', 'swap');
  env.advance(250);
  env.WsJank.end(tok, { region: 'board' });

  env.emitObserver([
    { entryType: 'long-animation-frame', startTime: 2000, duration: 399, blockingDuration: 349, scripts: [] },
    { entryType: 'longtask', startTime: 2600, duration: 399.9 }
  ]);

  assert.equal(env.WsJank.getStalls().length, 0);
  assert.equal(env.WsJank.getCrumbs().length, 1);
  assert.equal(notified, 0);
});

test('captures >= 400ms LoAF details, top 5 scripts sorted, and mapped source locations', () => {
  const env = loadJankEnv({
    mode: 'session',
    pathname: '/s/abc123def456',
    sourceMap: {
      scriptFile: 'dashboard-0123456789abcdef01234567.js',
      termFile: 'dashboard-fedcba9876543210fedcba98.js',
      totalChars: 600,
      totalLines: 30,
      files: [
        { file: 'dashboard.js', startChar: 0, endChar: 200, startLine: 1, endLine: 10 },
        { file: 'dashboard-board.js', startChar: 200, endChar: 600, startLine: 11, endLine: 30 }
      ]
    }
  });

  const tok = env.WsJank.begin('stream', 'ws.onmessage');
  env.advance(10);
  env.WsJank.end(tok, { event: 'region', bytes: 2048 });

  const scripts = [];
  for (let i = 0; i < 7; i++) {
    scripts.push({
      invoker: 'WS.onmessage',
      invokerType: 'event-listener',
      sourceURL: i === 6
        ? 'http://127.0.0.1:8080/s/dashboard-0123456789abcdef01234567.js?v=1'
        : 'http://127.0.0.1:8080/s/abc123def456',
      sourceFunctionName: 'fn' + i,
      sourceCharPosition: i === 6 ? 350 : 120,
      startTime: 2000 + i * 50,
      duration: 50 + i * 30,
      forcedStyleAndLayoutDuration: i === 6 ? 45 : 2,
      pauseDuration: i === 6 ? 12 : 0,
      windowAttribution: 'self'
    });
  }

  env.emitObserver([{
    entryType: 'long-animation-frame',
    startTime: 2000,
    duration: 512.4,
    blockingDuration: 462.4,
    renderStart: 2450,
    styleAndLayoutStart: 2480,
    firstUIEventTimestamp: 1980,
    scripts
  }]);

  const stalls = env.WsJank.getStalls();
  assert.equal(stalls.length, 1);
  const st = stalls[0];
  assert.equal(st.source, 'loaf');
  assert.equal(st.frameKind, 'session');
  assert.equal(st.durMs, 512.4);
  assert.equal(st.blockingMs, 462.4);
  assert.equal(st.renderMs, 62.4);
  assert.equal(st.forcedStyleMs, 32.4);
  assert.equal(st.firstUIEventDeltaMs, 20);
  assert.equal(st.scripts.length, 5);
  assert.equal(st.scripts[0].fn, 'fn6');
  assert.equal(st.scripts[0].mappedFile, 'dashboard-board.js');
  assert.equal(st.scripts[0].charOffset, 150);
  assert.equal(st.scripts[0].durMs, 230);
  assert.equal(st.crumbs.length, 1);
  assert.equal(st.crumbs[0].event, 'region');
  assert.equal(st.crumbs[0].bytes, 2048);

  // Also verify terminal.js & exact line:col mapping
  const termLoc = env.WsJank.mapLocation({
    url: 'http://127.0.0.1:8080/s/dashboard-fedcba9876543210fedcba98.js',
    line: 261,
    col: 9
  });
  assert.equal(termLoc.loc, 'terminal.js:261:9');
  const bundleLineLoc = env.WsJank.mapLocation({
    url: 'http://127.0.0.1:8080/s/dashboard-0123456789abcdef01234567.js',
    line: 15,
    col: 4
  });
  assert.equal(bundleLineLoc.loc, 'dashboard-board.js:5:4');
});

test('falls back to longtask (buffered:false) when LoAF is unavailable', () => {
  const env = loadJankEnv({ supportedEntryTypes: ['longtask'] });
  assert.equal(env.WsJank.mode(), 'longtask');
  assert.deepEqual(JSON.parse(JSON.stringify(env.getObserver().options)), { type: 'longtask', buffered: false });

  env.emitObserver([{
    entryType: 'longtask',
    startTime: 3000,
    duration: 460,
    attribution: [{ name: 'self', containerType: 'window' }]
  }]);

  const stalls = env.WsJank.getStalls();
  assert.equal(stalls.length, 1);
  assert.equal(stalls[0].source, 'longtask');
  assert.equal(stalls[0].blockingMs, 410);
});

test('merges overlapping Layer C nested spans + subsequent LoAF into one stall while keeping disjoint stalls separate', () => {
  const relayed = [];
  const env = loadJankEnv({ startClock: 1000 });
  env.WsJank.setRelay((s) => relayed.push(s));

  const outer = env.WsJank.begin('stream', 'ws.onmessage');
  env.advance(10);
  const inner = env.WsJank.begin('board', 'morphBoard');
  env.advance(420);
  env.WsJank.end(inner, { count: 12 });
  env.advance(5);
  env.WsJank.end(outer, { event: 'region' });

  // Emits twice via relay with the EXACT same id, upserted on LoAF arrival.
  assert.equal(env.WsJank.getStalls().length, 1);
  const id = env.WsJank.getStalls()[0].id;
  assert.equal(relayed.length, 2);
  assert.equal(relayed[1].id, id);

  env.emitObserver([{
    entryType: 'long-animation-frame',
    startTime: 1000,
    duration: 440,
    blockingDuration: 390,
    scripts: [{ invoker: 'ws.onmessage', duration: 425, sourceURL: '/shell' }]
  }]);

  const stalls = env.WsJank.getStalls();
  assert.equal(stalls.length, 1);
  assert.equal(stalls[0].id, id);
  assert.deepEqual(JSON.parse(JSON.stringify(stalls[0].sources.sort())), ['loaf', 'span']);
  assert.equal(stalls[0].durMs, 440);
  assert.equal(stalls[0].scripts.length, 1);

  // Adjacent/disjoint subsequent stall starting right at end of previous (1440..1900) stays distinct.
  env.emitObserver([{
    entryType: 'long-animation-frame',
    startTime: 1440,
    duration: 460,
    blockingDuration: 410,
    scripts: []
  }]);
  assert.equal(env.WsJank.getStalls().length, 2);
});

test('keeps 128-slot crumb buffer and 10-stall FIFO buffer cleanly bounded', () => {
  const env = loadJankEnv();
  for (let i = 0; i < 200; i++) {
    env.WsJank.crumb('convo', 'op-' + i);
  }
  const crumbs = env.WsJank.getCrumbs();
  assert.equal(crumbs.length, 128);
  assert.equal(crumbs[0].op, 'op-72');
  assert.equal(crumbs[127].op, 'op-199');

  for (let s = 1; s <= 15; s++) {
    env.emitObserver([{
      entryType: 'long-animation-frame',
      startTime: s * 1000,
      duration: 400 + s,
      blockingDuration: 350 + s,
      scripts: []
    }]);
  }
  const stalls = env.WsJank.getStalls();
  assert.equal(stalls.length, 10);
  assert.equal(stalls[0].durMs, 406);
  assert.equal(stalls[9].durMs, 415);
  assert.equal(stalls[9].crumbs.length, 25);
});

test('cross-frame WsShell validation and shell ingest survive iframe pruning and scrub secrets', () => {
  const env = loadJankEnv({ withShell: true });
  assert.equal(env.WsShell.message({ wsShell: 'jank-stall', stall: null }), null);
  assert.equal(env.WsShell.message({ wsShell: 'jank-stall', stall: [] }), null);

  const iframe = loadJankEnv({
    mode: 'session',
    pathname: '/s/deadbeefcafebabe'
  });
  iframe.WsJank.setRelay((stall) => {
    const msg = env.WsShell.message({ wsShell: 'jank-stall', stall });
    assert.ok(msg, 'expected valid WsShell message');
    env.WsJank.ingest(msg.stall);
  });

  const secretTok = iframe.WsJank.begin('stream', {
    op: 'receiveRegion',
    event: 'Bearer super_secret_jwt_12345 C:\\Users\\alice\\secret.txt',
    prompt: 'PRIVATE CHAT CONTENT MUST BE DROPPED',
    ptyOutput: 'SECRET SHELL OUTPUT MUST BE DROPPED'
  });
  iframe.advance(450);
  iframe.WsJank.end(secretTok, {
    region: 'board',
    html: '<div>sensitive conversation DOM</div>',
    hex: '0123456789abcdef0123456789abcdef'
  });

  // Dispose iframe instance; shell still keeps the relayed stall.
  iframe.WsJank.clear();
  const topStalls = env.WsJank.getStalls();
  assert.equal(topStalls.length, 1);
  const raw = env.WsJank.formatJSON();
  assert.doesNotMatch(raw, /PRIVATE CHAT CONTENT|SECRET SHELL OUTPUT|sensitive conversation DOM|super_secret_jwt_12345|alice|deadbeefcafebabe|0123456789abcdef0123456789abcdef/);
  assert.match(raw, /Bearer <redacted>/);
  assert.match(raw, /<home>/);
});

test('modal UI handles empty state, stall selection, keyboard Escape, Copy Markdown/JSON, and Clear', () => {
  const env = loadJankEnv();
  const copied = [];
  let closedMenu = false;

  env.WsJank.openModal({
    copyText(txt, quiet) { copied.push({ txt, quiet }); },
    triggerEl: env.triggerBtn,
    closeMenu() { closedMenu = true; }
  });
  assert.equal(closedMenu, true);

  const scrim = env.body.querySelector('.d-jank-scrim');
  assert.ok(scrim && !scrim.hidden);
  const emptyBox = scrim.querySelector('.d-jank-empty');
  assert.equal(emptyBox.hidden, false);

  env.emitObserver([{
    entryType: 'long-animation-frame',
    startTime: 5000,
    duration: 615,
    blockingDuration: 565,
    scripts: [{ invoker: 'morphBoard', duration: 600, sourceURL: '/shell' }]
  }]);
  assert.equal(emptyBox.hidden, true);
  assert.equal(scrim.querySelectorAll('.d-jank-item').length, 1);

  const buttons = scrim.querySelectorAll('.d-jank-btn');
  buttons[0].dispatch('click'); // Copy Markdown
  buttons[1].dispatch('click'); // Copy JSON
  assert.equal(copied.length, 2);
  assert.match(copied[0].txt, /## Dashboard UI Stall Report/);
  assert.match(copied[1].txt, /"schema": "wsdashboard\.jank\.v1"/);
  assert.equal(env.toasts.length, 2);

  buttons[2].dispatch('click'); // Clear
  assert.equal(env.WsJank.count(), 0);
  assert.equal(emptyBox.hidden, false);

  const dialog = scrim.querySelector('.d-jank-modal');
  dialog.dispatch('keydown', { key: 'Escape' });
  assert.equal(scrim.hidden, true);
  assert.equal(env.triggerBtn.focused, true);
});

test('mapLocation resolves FrameScriptFile sub-files from frameFiles before frame-core fallback', () => {
  const env = loadJankEnv();
  const map = {
    scriptFile: 'dashboard-0123456789abcdef01234567.js',
    frameScriptFile: 'dashboard-fedcba9876543210fedcba98.js',
    files: [],
    frameFiles: [
      { file: 'pill.js', startChar: 100, endChar: 500, startLine: 10, endLine: 29 },
      { file: 'stream-view.js', startChar: 5000, endChar: 15000, startLine: 3102, endLine: 3601 },
      { file: 'cards.js', startChar: 20000, endChar: 30000, startLine: 4001, endLine: 4800 }
    ]
  };

  const byLine = env.WsJank.mapLocation({
    url: 'http://127.0.0.1:8080/s/dashboard-fedcba9876543210fedcba98.js',
    line: 3375,
    col: 9
  }, map);
  assert.equal(byLine.mappedFile, 'stream-view.js');
  assert.equal(byLine.localLine, 274);
  assert.equal(byLine.loc, 'stream-view.js:274:9');

  const byChar = env.WsJank.mapLocation({
    url: '/s/dashboard-fedcba9876543210fedcba98.js',
    charPos: 23655
  }, map);
  assert.equal(byChar.mappedFile, 'cards.js');
  assert.equal(byChar.charOffset, 3655);
  assert.ok(byChar.approxLine >= 200 && byChar.approxLine <= 400);
  assert.match(byChar.loc, /^cards\.js:~/);

  const fallback = env.WsJank.mapLocation({
    url: '/s/dashboard-fedcba9876543210fedcba98.js',
    line: 15
  }, { frameScriptFile: 'dashboard-fedcba9876543210fedcba98.js', files: [] });
  assert.equal(fallback.mappedFile, 'frame-core.js');
  assert.equal(fallback.loc, 'frame-core.js:15');
});

test('100-399ms LoAF running counters do not evict >=400ms ring and surface in modal, exports, and shell relay', () => {
  const env = loadJankEnv({ withShell: true });
  assert.equal(env.WsShell.message({ wsShell: 'jank-minor', durMs: 'bad', topInvoker: 'x' }), null);

  for (let i = 0; i < 10; i++) {
    env.emitObserver([{
      entryType: 'long-animation-frame',
      startTime: 1000 + i * 1000,
      duration: 450 + i,
      blockingDuration: 400 + i,
      scripts: [{ invoker: 'heavyOp' + i, duration: 420 }]
    }]);
  }
  assert.equal(env.WsJank.count(), 10);
  const firstIdBefore = env.WsJank.getStalls()[0].id;

  // Sub-100ms stays ignored; 100-399ms updates counters without touching >=400ms stalls.
  env.emitObserver([
    { entryType: 'long-animation-frame', startTime: 20000, duration: 85, scripts: [{ invoker: 'ignored', duration: 80 }] },
    { entryType: 'long-animation-frame', startTime: 21000, duration: 112.4, scripts: [{ invoker: 'EventTarget.onmessage', sourceFunctionName: 'syncWbUpdateItems', duration: 105 }] },
    { entryType: 'long-animation-frame', startTime: 22000, duration: 248.6, scripts: [{ invoker: 'ResizeObserver.callback', duration: 220 }] },
    { entryType: 'longtask', startTime: 23000, duration: 180 }
  ]);

  assert.equal(env.WsJank.count(), 10);
  assert.equal(env.WsJank.getStalls()[0].id, firstIdBefore);

  const stats = JSON.parse(JSON.stringify(env.WsJank.getMinorStats()));
  assert.deepEqual(stats, {
    count100to399: 2,
    maxMs100to399: 248.6,
    topInvoker: 'ResizeObserver.callback'
  });

  // Relayed minor LoAF from iframe via WsShell increments shell counters.
  const minorMsg = env.WsShell.message({ wsShell: 'jank-minor', durMs: 310, topInvoker: 'writeAnswers' });
  assert.ok(minorMsg);
  assert.equal(env.WsJank.ingestMinor(minorMsg), true);

  const parsedJson = JSON.parse(env.WsJank.formatJSON());
  assert.deepEqual(parsedJson.minorLoaf, {
    count100to399: 3,
    maxMs100to399: 310,
    topInvoker: 'writeAnswers'
  });

  const md = env.WsJank.formatMarkdown();
  assert.match(md, /\*\*100-399ms LoAFs\*\*: 3 \(max 310ms, top: `writeAnswers`\)/);

  // Open modal even with 0 >=400ms stalls: minor LoAF appears in subtitle and exports stay enabled.
  env.WsJank.clear();
  env.emitObserver([{ entryType: 'long-animation-frame', startTime: 30000, duration: 145, scripts: [{ invoker: 'outboxAcked', duration: 130 }] }]);
  env.WsJank.openModal({});
  const scrim = env.body.querySelector('.d-jank-scrim');
  const subText = scrim.querySelector('.d-jank-sub').textContent;
  assert.match(subText, /js\.jank\.minor\.summary:1,145,.*outboxAcked/);
  assert.equal(scrim.querySelectorAll('.d-jank-btn')[0].disabled, false);

  scrim.querySelectorAll('.d-jank-btn')[2].dispatch('click'); // Clear
  assert.deepEqual(JSON.parse(JSON.stringify(env.WsJank.getMinorStats())), {
    count100to399: 0,
    maxMs100to399: 0,
    topInvoker: ''
  });
});
