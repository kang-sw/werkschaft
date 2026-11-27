const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { performance } = require('node:perf_hooks');
const { readAssetSource } = require('./asset-source.cjs');

const convSrc = readAssetSource('wsdashboard', 'dashboard-conversation.js');
const boardSrc = readAssetSource('wsdashboard', 'dashboard-board.js');
const dashSrc = readAssetSource('wsdashboard', 'dashboard.js');
const deliverySrc = readAssetSource('wsdashboard', 'dashboard-composer-delivery.js');
const auditSrc = readAssetSource('wsdashboard', 'dashboard-audit.js');
const anchorSrc = readAssetSource('wsdashboard', 'dashboard-board-anchor.js');
const streamSrc = readAssetSource('wswhiteboard', 'stream-view.js');

function extractFn(source, name) {
  const esc = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const m = source.match(new RegExp('(?:^|\\n)( +)function ' + esc + '\\('));
  assert.ok(m, 'missing function ' + name);
  const indent = m[1];
  const tail = source.slice(m.index + (m[0].startsWith('\n') ? 1 : 0));
  const oneLine = tail.match(new RegExp('^' + indent + 'function ' + esc + '\\([^\\n]*\\{[^\\n]*\\}(?=\\r?\\n|$)'));
  if (oneLine) return '\n' + oneLine[0] + '\n';
  const endM = tail.match(new RegExp('\\n' + indent + '\\}(?=\\r?\\n|$)'));
  assert.ok(endM, 'unterminated function ' + name);
  return '\n' + tail.slice(0, endM.index + indent.length + 2) + '\n';
}

function makeDom() {
  function parseSingle(part) {
    part = part.trim();
    const notAttr = [];
    part = part.replace(/:not\(\[([^\]]+)\]\)/g, (_, a) => {
      notAttr.push(a);
      return '';
    });
    let id = null;
    part = part.replace(/#([a-zA-Z0-9_-]+)/g, (_, v) => {
      id = v;
      return '';
    });
    const tagM = part.match(/^[a-zA-Z0-9_-]+/);
    const tag = tagM ? tagM[0].toUpperCase() : null;
    if (tagM) part = part.slice(tagM[0].length);
    const attrs = [];
    const attrRe = /\[([a-zA-Z0-9_-]+)(?:="([^"]*)")?\]/g;
    let am;
    while ((am = attrRe.exec(part))) {
      attrs.push([am[1], am[2]]);
    }
    part = part.replace(/\[[^\]]+\]/g, '');
    const classes = [];
    const clsRe = /\.([a-zA-Z0-9_-]+)/g;
    let cm;
    while ((cm = clsRe.exec(part))) classes.push(cm[1]);
    return { tag, id, attrs, classes, notAttr };
  }

  function matchSingle(el, s) {
    if (!el || el.nodeType !== 1) return false;
    if (s.tag && el.tagName !== s.tag) return false;
    if (s.id && el.getAttribute('id') !== s.id) return false;
    for (const c of s.classes) if (!el.classList.contains(c)) return false;
    for (const a of s.notAttr) if (el.hasAttribute(a)) return false;
    for (const [k, v] of s.attrs) {
      if (!el.hasAttribute(k)) return false;
      if (v !== undefined && el.getAttribute(k) !== v) return false;
    }
    return true;
  }

  function matchCompound(el, compound) {
    const parts = compound.trim().split(/\s+/).filter(Boolean).map(parseSingle);
    if (!parts.length || !matchSingle(el, parts[parts.length - 1])) return false;
    let cur = el.parentNode;
    for (let i = parts.length - 2; i >= 0; i--) {
      let ok = false;
      while (cur) {
        if (matchSingle(cur, parts[i])) {
          ok = true;
          cur = cur.parentNode;
          break;
        }
        cur = cur.parentNode;
      }
      if (!ok) return false;
    }
    return true;
  }

  function matches(el, sel) {
    return String(sel).split(',').some(s => matchCompound(el, s));
  }

  function makeEl(tag, opts = {}) {
    const attrs = Object.assign({}, opts.attrs || {});
    const classes = new Set(String(opts.className || '').split(/\s+/).filter(Boolean));
    let rectCalls = 0;
    let offsetCalls = 0;
    const node = {
      nodeType: 1,
      tagName: String(tag || 'DIV').toUpperCase(),
      attrs,
      children: [],
      childNodes: [],
      parentNode: null,
      textContent: opts.text || '',
      value: opts.value || '',
      checked: !!opts.checked,
      hidden: !!opts.hidden,
      style: {},
      queryCalls: 0,
      queryAllCalls: 0,
      matchCalls: 0,
      get id() { return attrs.id || ''; },
      set id(v) { attrs.id = String(v); },
      get className() { return Array.from(classes).join(' '); },
      set className(v) {
        classes.clear();
        for (const c of String(v || '').split(/\s+/).filter(Boolean)) classes.add(c);
      },
      classList: {
        contains: c => classes.has(c),
        add: c => classes.add(c),
        remove: c => classes.delete(c),
        toggle: (c, force) => {
          const on = force === undefined ? !classes.has(c) : !!force;
          if (on) classes.add(c);
          else classes.delete(c);
          return on;
        },
      },
      getAttribute: k => (Object.prototype.hasOwnProperty.call(attrs, k) ? attrs[k] : null),
      setAttribute: (k, v) => { attrs[k] = String(v); },
      removeAttribute: k => { delete attrs[k]; },
      hasAttribute: k => Object.prototype.hasOwnProperty.call(attrs, k),
      get attributes() {
        return Object.keys(attrs).map(name => ({ name, value: attrs[name] }));
      },
      appendChild(c) {
        if (c.parentNode) c.parentNode.removeChild(c);
        c.parentNode = node;
        node.children.push(c);
        node.childNodes = node.children;
        return c;
      },
      insertBefore(c, ref) {
        if (c.parentNode) c.parentNode.removeChild(c);
        c.parentNode = node;
        const i = ref ? node.children.indexOf(ref) : -1;
        if (i < 0) node.children.push(c);
        else node.children.splice(i, 0, c);
        node.childNodes = node.children;
        return c;
      },
      removeChild(c) {
        const i = node.children.indexOf(c);
        if (i >= 0) {
          node.children.splice(i, 1);
          node.childNodes = node.children;
          c.parentNode = null;
        }
        return c;
      },
      matches(sel) {
        node.matchCalls++;
        return matches(node, sel);
      },
      closest(sel) {
        for (let cur = node; cur; cur = cur.parentNode) {
          if (matches(cur, sel)) return cur;
        }
        return null;
      },
      querySelector(sel) {
        node.queryCalls++;
        return walkFind(node, sel);
      },
      querySelectorAll(sel) {
        node.queryAllCalls++;
        const out = [];
        walkAll(node, sel, out);
        return out;
      },
      getClientRects() {
        rectCalls++;
        return [{ width: 100, height: 100 }];
      },
      getBoundingClientRect() {
        rectCalls++;
        return { top: 0, left: 0, width: 100, height: node._height || 100, bottom: node._height || 100 };
      },
      get offsetHeight() {
        offsetCalls++;
        return node._height || 100;
      },
      _height: opts.height || 0,
      get _rectCalls() { return rectCalls; },
      get _offsetCalls() { return offsetCalls; },
      resetCounters() {
        rectCalls = 0;
        offsetCalls = 0;
        node.queryCalls = 0;
        node.queryAllCalls = 0;
        node.matchCalls = 0;
      },
    };
    Object.defineProperty(node, 'firstChild', { get() { return node.children[0] || null; } });
    Object.defineProperty(node, 'firstElementChild', { get() { return node.children[0] || null; } });
    Object.defineProperty(node, 'isConnected', { get() { return !!node._connected; } });
    for (const c of opts.children || []) node.appendChild(c);
    return node;
  }

  function walkFind(root, sel) {
    for (const c of root.children) {
      if (matches(c, sel)) return c;
      const hit = walkFind(c, sel);
      if (hit) return hit;
    }
    return null;
  }

  function walkAll(root, sel, out) {
    for (const c of root.children) {
      if (matches(c, sel)) out.push(c);
      walkAll(c, sel, out);
    }
  }

  return { makeEl };
}

function makeTimerHarness() {
  let rafId = 0, timerId = 0;
  const rafs = new Map(), timers = new Map();
  return {
    requestAnimationFrame(fn) {
      const id = ++rafId;
      rafs.set(id, fn);
      return id;
    },
    cancelAnimationFrame(id) {
      rafs.delete(id);
    },
    setTimeout(fn, ms) {
      const id = ++timerId;
      timers.set(id, { fn, ms });
      return id;
    },
    clearTimeout(id) {
      timers.delete(id);
    },
    get rafCount() { return rafs.size; },
    get timerCount() { return timers.size; },
    flushRaf() {
      const entries = [...rafs.entries()];
      rafs.clear();
      for (const [, fn] of entries) fn(16);
    },
    flushTimers() {
      const entries = [...timers.entries()];
      timers.clear();
      for (const [, t] of entries) t.fn();
    },
  };
}

test('P0-1: wbUpdateQueryBoard uses O(1) live + per-topic virtualStore index and invalidates on virtual updates', () => {
  const { makeEl } = makeDom();
  const board = makeEl('div', { attrs: { id: 'r-board' } });
  const entries = makeEl('div', { attrs: { id: 'r-entries' } });
  const virtualStore = new Map();

  let forEachCalls = 0;
  const rawForEach = virtualStore.forEach.bind(virtualStore);
  virtualStore.forEach = function (cb) {
    forEachCalls++;
    return rawForEach(cb);
  };

  function makeCard(cls, attrs, titleText) {
    return makeEl('div', {
      className: cls,
      attrs,
      children: [makeEl('div', { className: 'head', children: [makeEl('div', { className: 'title', text: titleText })] })],
    });
  }

  // Populate 60 virtualized topics: mix of children arrays and stashed elements,
  // covering open cards (.card.policy, .arow, .ckrow), ask history, and settled rows.
  for (let t = 1; t <= 60; t++) {
    const idStr = String(t);
    const padStr = t < 10 ? '0' + idStr : idStr;
    if (t <= 20) {
      const openCard = makeCard('card policy', { 'data-id': padStr, 'data-class': 'policy' }, 'Policy ' + idStr);
      const askHist = makeEl('div', {
        className: 'wb-hitem original-question policy',
        attrs: { 'data-item': idStr, 'data-state': 'ask' },
      });
      virtualStore.set('topic-' + t, { children: [openCard, askHist], stashed: null });
    } else if (t <= 40) {
      const stashed = makeEl('section', {
        className: 'topic',
        attrs: { 'data-topic': 'topic-' + t },
        children: [
          makeEl('div', {
            className: 'wb-hitem answered',
            attrs: { 'data-item': idStr, 'data-state': 'settled', 'data-class': 'technical' },
            children: [
              makeEl('div', { className: 'wb-hmain', children: [makeEl('b', { text: 'Settled ' + idStr })] }),
              makeEl('div', { className: 'wb-hstatus', text: 'Done' }),
            ],
          }),
        ],
      });
      // Include stale open card in children that must be ignored while rec.stashed is set.
      const staleOpen = makeCard('arow', { 'data-id': padStr }, 'Stale ' + idStr);
      virtualStore.set('topic-' + t, { children: [staleOpen], stashed });
    } else {
      const ck = makeCard('ckrow', { 'data-item': idStr, 'data-id': padStr, 'data-assumed': '1' }, 'Assumed ' + idStr);
      virtualStore.set('topic-' + t, { children: [ck], stashed: null });
    }
  }

  // Live DOM item 15 overrides virtual topic-15 settled/open state.
  board.appendChild(makeCard('arow sent-held', { 'data-id': '15' }, 'Live Overriding 15'));

  // Build 120 chips in #r-entries (2 copies per ID).
  const grp = makeEl('div', { className: 'wbupdate-items' });
  for (let rep = 0; rep < 2; rep++) {
    for (let t = 1; t <= 60; t++) {
      grp.appendChild(makeEl('span', {
        className: 'wbupdate-item-id',
        attrs: { 'data-wb-item-id': String(t) },
        text: '(' + t + ')',
      }));
    }
  }
  entries.appendChild(grp);

  const src = [
    'wbUpdateNormID', 'wbUpdateItemID', 'wbUpdateFlattenMemo', 'wbUpdateIsHeld',
    'wbUpdateClassFromEl', 'wbUpdateTitleFromEl', 'wbUpdateSettledSummary',
    'wbUpdateQueryBoard', 'readBoardQuestionState', 'hydrateWbUpdateChip',
    'hydrateWbUpdateGroup', 'hydrateWbUpdateItems', 'syncWbUpdateItems',
    'wbUpdateInvalidateVirtual', 'wbUpdateBeginBatch', 'wbUpdateEndBatch',
  ].map(n => extractFn(convSrc, n)).join('\n');

  const ctx = vm.createContext({
    Map, WeakMap, Set, Object, Array, String, RegExp, Math, Promise,
    WB_QCAP_CLASSES: {
      policy: 'js.wbupdate.cls.policy',
      technical: 'js.wbupdate.cls.technical',
      announced: 'js.wbupdate.cls.announced',
      check: 'js.wbupdate.cls.check',
    },
    wsT: (k, a) => (a === undefined ? k : k + ':' + a),
    doc: {
      createElement: t => makeEl(t),
      querySelectorAll: s => entries.querySelectorAll(s),
    },
    region: name => (name === 'board' ? board : null),
    entriesBox: () => entries,
    q: (s, r) => (r || entries).querySelectorAll(s),
    virtualStore,
    window: { wbPill: { isHeld: () => false } },
  });
  vm.runInContext(src, ctx);

  board.resetCounters();
  forEachCalls = 0;

  const t0 = performance.now();
  ctx.syncWbUpdateItems();
  const elapsed = performance.now() - t0;

  assert.ok(elapsed < 200, '120 chips over 60 virtualized topics must hydrate fast, got ' + elapsed + 'ms');
  assert.equal(forEachCalls, 1, 'single pass must scan virtualStore once, not per chip');
  assert.equal(board.queryCalls, 0, 'indexed pass must not run per-chip board.querySelector');
  assert.equal(board.queryAllCalls, 1, 'indexed pass must scan live board in a single querySelectorAll');

  // Verify semantic correctness across live override, stashed override, children, and assumed ckrow.
  const state15 = ctx.readBoardQuestionState('15');
  assert.equal(state15.state, 'sent');
  assert.equal(state15.title, 'Live Overriding 15');

  const state5 = ctx.readBoardQuestionState('05');
  assert.equal(state5.state, 'open');
  assert.equal(state5.title, 'Policy 5');

  const state25 = ctx.readBoardQuestionState('25');
  assert.equal(state25.state, 'settled', 'stashed tree must win over stale rec.children');
  assert.equal(state25.title, 'Settled 25');

  const state45 = ctx.readBoardQuestionState('45');
  assert.equal(state45.state, 'settled', 'ckrow with data-assumed must resolve as settled, never open');

  // Repeat sync with unchanged virtualStore must do ZERO virtualStore.forEach scans.
  forEachCalls = 0;
  ctx.syncWbUpdateItems();
  assert.equal(forEachCalls, 0, 'cached virtualStore index must avoid rescanning when virtualStore is unchanged');

  // Mutating a single virtualized topic via virtualStore.set + wbUpdateInvalidateVirtual invalidates cache.
  const topic25 = virtualStore.get('topic-25');
  topic25.stashed = makeEl('section', {
    className: 'topic',
    children: [makeCard('arow sent-sent', { 'data-id': '25' }, 'Sent 25')],
  });
  virtualStore.set('topic-25', topic25);
  ctx.wbUpdateInvalidateVirtual('topic-25');
  assert.equal(ctx.readBoardQuestionState('25').state, 'sent', 'invalidated topic must reflect updated stashed card');
  assert.ok(forEachCalls >= 1, 'virtualStore cache must rebuild once after invalidation');

  // Direct wbUpdateInvalidateVirtual without virtualStore.set (in-place DOM mutation inside stashed).
  const rec30 = virtualStore.get('topic-30');
  rec30.stashed.children[0] = makeCard('arow', { 'data-id': '30' }, 'Open 30');
  ctx.wbUpdateInvalidateVirtual('topic-30');
  assert.equal(ctx.readBoardQuestionState('30').state, 'open', 'explicit invalidation for key clears rec cache');
});

test('P0-2: wbUpdateBeginBatch/EndBatch and swap("board") coalesce syncWbUpdateItems to one pass', () => {
  const { makeEl } = makeDom();
  const entries = makeEl('div', { attrs: { id: 'r-entries' } });
  const board = makeEl('div', { attrs: { id: 'r-board' } });
  const grp = makeEl('div', { className: 'wbupdate-items' });
  grp.appendChild(makeEl('span', { className: 'wbupdate-item-id', attrs: { 'data-wb-item-id': '1' }, text: '(1)' }));
  entries.appendChild(grp);

  let boardScanCount = 0;
  const origQSA = board.querySelectorAll.bind(board);
  board.querySelectorAll = function (s) {
    boardScanCount++;
    return origQSA(s);
  };

  const convFns = [
    'wbUpdateNormID', 'wbUpdateItemID', 'wbUpdateFlattenMemo', 'wbUpdateIsHeld',
    'wbUpdateClassFromEl', 'wbUpdateTitleFromEl', 'wbUpdateSettledSummary',
    'wbUpdateQueryBoard', 'readBoardQuestionState', 'hydrateWbUpdateChip',
    'hydrateWbUpdateGroup', 'hydrateWbUpdateItems', 'syncWbUpdateItems',
    'wbUpdateInvalidateVirtual', 'wbUpdateBeginBatch', 'wbUpdateEndBatch',
  ].map(n => extractFn(convSrc, n)).join('\n');

  const swapFn = extractFn(boardSrc, 'swap');
  const t0 = makeEl('section', { className: 'topic' });
  const t1 = makeEl('section', { className: 'topic' });
  const timers = makeTimerHarness();

  const ctx = vm.createContext({
    Map, WeakMap, Set, Object, Array, String, RegExp, Math, Promise,
    WB_QCAP_CLASSES: { policy: 'p', technical: 't', announced: 'a', check: 'c' },
    wsT: k => k,
    live: true,
    TAB: { board: 'board', entries: 'convo', convlive: 'convo' },
    grid: { getAttribute: () => 'board' },
    ctext: null,
    mpend: null,
    convoSwapping: 0,
    deferred: null,
    boardReturnTimer: 0,
    boardRelease: false,
    boardRendered: null,
    boardLatest: null,
    boardPending: {},
    boardOverviewHeld: null,
    boardDragHeld: null,
    window: { wbPill: { isHeld: () => false }, wbHistory: null, WsCards: null, WsPanelCard: null, wbForm: null },
    doc: {
      hidden: false,
      activeElement: null,
      contains: () => true,
      createElement: t => makeEl(t),
      querySelectorAll: s => entries.querySelectorAll(s),
    },
    region: name => (name === 'board' ? board : null),
    entriesBox: () => entries,
    q: (s, r) => (r || entries).querySelectorAll(s),
    requestAnimationFrame: timers.requestAnimationFrame,
    setTimeout: timers.setTimeout,
    clearTimeout: timers.clearTimeout,
    swapDotFrame: 0,
    swapDotTimer: 0,
    swapDotPending: Object.create(null),
    queueSwapDot() {},
    pinned: () => false,
    scrollState: () => null,
    boardRead: fn => fn(),
    readAnswers: () => ({}),
    frameHeights: () => ({}),
    captureBoardHistory: () => ({}),
    anchorCards: () => [],
    boardKeep: () => null,
    boardInputFull: h => h,
    boardStageInput: h => h,
    morphBoard: () => [t0, t1],
    bandBoard() {},
    watchBoard() {},
    restoreOpen() {},
    fmtTimes() {},
    sweepOutImgs() {},
    setFrameHeights() {},
    mountFrames() {},
    writeAnswers() { ctx.syncWbUpdateItems(); },
    syncChips() { ctx.syncWbUpdateItems(); },
    applySent() { ctx.syncWbUpdateItems(); },
    indexFrames() {},
    bindCapsuleNotes() {},
    mermaid() {},
    outboxSync() {},
    syncSpinnerPhases() {},
    restoreScroll() {},
    anchorSettle() {},
    TL_REGIONS: {},
    tlHeld: {},
    tlCount() {},
    syncBadge() {},
    convMotionSync() {},
    syncChead() {},
    boardRecordPending() {},
    boardPendingBadge() {},
    boardHoldIndicators() {},
    boardReturnApply() {},
    boardApplied() {},
  });
  vm.runInContext(convFns + '\n' + swapFn, ctx);

  // Outside batch: synchronous calls run immediately.
  ctx.syncWbUpdateItems();
  assert.equal(boardScanCount, 1, 'direct syncWbUpdateItems outside batch must execute immediately');

  // Manual batch: nested calls accumulate and run once on outer end.
  boardScanCount = 0;
  ctx.wbUpdateBeginBatch();
  ctx.wbUpdateBeginBatch();
  ctx.syncWbUpdateItems(['1']);
  ctx.syncWbUpdateItems();
  assert.equal(boardScanCount, 0, 'calls inside batch must be suppressed');
  ctx.wbUpdateEndBatch();
  assert.equal(boardScanCount, 0, 'inner endBatch must wait for outer endBatch');
  ctx.wbUpdateEndBatch();
  assert.equal(boardScanCount, 1, 'outer endBatch must flush once');

  // Inside real swap("board"): 2 writeAnswers + syncChips + applySent + end-of-swap = 5 triggers -> 1 flush!
  boardScanCount = 0;
  ctx.swap('board', 'h2', '<div></div>', false);
  assert.equal(boardScanCount, 1, 'swap("board") must coalesce all writeAnswers/syncChips/applySent/tail syncs to 1 pass');
});

test('P0-3: outboxAcked defers boardFlushViewed onto rAF or hidden setTimeout and prevents double-flush', () => {
  const deliverySection = deliverySrc.match(/\n  \/\/ --- outbox [^]*?\n  function outboxSync\([^]*?\n  \}\n/)[0];
  const flushFn = extractFn(boardSrc, 'boardScheduleFlushViewed') + extractFn(boardSrc, 'boardFlushViewed');
  const timers = makeTimerHarness();

  function runCase({ hidden }) {
    const flushes = [];
    const { makeEl } = makeDom();
    const live = makeEl('div', { className: 'clive' });
    const ctx = vm.createContext({
      doc: { hidden, createElement: t => makeEl(t), querySelector: s => (s === '#r-convlive .clive' ? live : null) },
      q: () => [],
      ctext: { value: '', closest: () => null },
      imgs: [],
      imgTrash: {},
      imgSeq: 0,
      Date, Math,
      setTimeout: timers.setTimeout,
      clearTimeout: timers.clearTimeout,
      requestAnimationFrame: timers.requestAnimationFrame,
      cancelAnimationFrame: timers.cancelAnimationFrame,
      release() {},
      saveDraft: () => Promise.resolve(true),
      followConvo() {},
      lastUserEntry: () => 'u1',
      outboxUnrow() {},
      outboxBubble: () => makeEl('div'),
      outboxUnsent: () => false,
      chipList: () => [],
      topicNotes: () => [],
      wsT: x => x,
      boardOwn: {},
      boardPending: { t1: true },
      boardLatest: { hash: 'h1', html: '<div></div>' },
      boardRelease: false,
      boardReleaseFrame: 0,
      boardReleaseTimer: 0,
      pageHidden: () => hidden,
      boardPendingBadge() {},
      swap: (...args) => flushes.push(args),
      draftDetached: false,
      noteWarn() {},
      window: {},
    });
    vm.runInContext(flushFn + '\n' + deliverySection, ctx);
    function msg(u) { return { body: {}, pics: [], own: [], user: u, back: { text: u }, row: makeEl('div') }; }
    return { ctx, flushes, msg };
  }

  // Visible case: defers to rAF, coalesced across multiple ACKs.
  const vis = runCase({ hidden: false });
  const m1 = vis.msg('u1'), m2 = vis.msg('u1');
  vis.ctx.outbox.push(m1, m2);
  vis.ctx.outboxAcked(m1, 'e1');
  vis.ctx.outboxAcked(m2, 'e2');
  assert.equal(vis.flushes.length, 0, 'outboxAcked must not synchronously flush board during send ACK');
  assert.equal(timers.rafCount, 1, 'multiple ACKs must coalesce into 1 rAF');
  assert.equal(timers.timerCount, 0);
  timers.flushRaf();
  assert.equal(vis.flushes.length, 1, 'rAF callback must flush once');
  assert.equal(vis.flushes[0][3], true, 'deferred flush passes explicit=true swap flag');

  // Explicit flush before scheduled rAF cancels the rAF (no double-flush).
  const cancelCase = runCase({ hidden: false });
  const mc = cancelCase.msg('u1');
  cancelCase.ctx.outbox.push(mc);
  cancelCase.ctx.outboxAcked(mc, 'e1');
  assert.equal(timers.rafCount, 1);
  cancelCase.ctx.boardFlushViewed(true);
  assert.equal(timers.rafCount, 0, 'explicit flush must cancel pending boardReleaseFrame');
  assert.equal(cancelCase.flushes.length, 1);
  timers.flushRaf();
  assert.equal(cancelCase.flushes.length, 1, 'no double flush after explicit flush');

  // Hidden case: uses setTimeout(..., 0) fallback instead of stalled rAF.
  const hid = runCase({ hidden: true });
  const mh = hid.msg('u1');
  hid.ctx.outbox.push(mh);
  hid.ctx.outboxAcked(mh, 'e1');
  assert.equal(hid.flushes.length, 0);
  assert.equal(timers.rafCount, 0, 'hidden frame must fall back to timer instead of stalled rAF');
  assert.equal(timers.timerCount, 1);
  timers.flushTimers();
  assert.equal(hid.flushes.length, 1);
});

test('P1-4: swap() runs zero synchronous getClientRects() and defers unread dot update using paneVisible', () => {
  const { makeEl } = makeDom();
  const timers = makeTimerHarness();

  const bcol = makeEl('div', { attrs: { id: 'bcol' } });
  const board = makeEl('div', { attrs: { id: 'r-board' } });
  const convo = makeEl('div', { attrs: { id: 'convo' } });
  const entries = makeEl('div', { attrs: { id: 'r-entries' } });
  const fpane = makeEl('div', { attrs: { id: 'files' } });
  const cterms = makeEl('div', { attrs: { id: 'cterm-panes' } });
  const grid = makeEl('div', { attrs: { 'data-ctab': 'convo' } });
  const bDot = makeEl('span', { className: 'tdot' });
  const cDot = makeEl('span', { className: 'tdot' });

  const allNodes = [bcol, board, convo, entries, fpane, cterms, grid, bDot, cDot];
  const byId = { bcol, 'r-board': board, convo, 'r-entries': entries, files: fpane, 'cterm-panes': cterms };

  const paneShownFn = extractFn(dashSrc, 'paneShown');
  const tabShownFn = extractFn(dashSrc, 'tabShown');
  const convoVisFn = extractFn(dashSrc, 'convoVisible');
  const queueDotFns = boardSrc.match(/\n  var swapDotFrame[\s\S]*?\n  function queueSwapDot\([\s\S]*?\n  \}\n/)[0];
  const swapFn = extractFn(boardSrc, 'swap');

  const ctx = vm.createContext({
    Object, Array, String,
    $: id => byId[id] || null,
    doc: {
      hidden: false,
      activeElement: null,
      contains: () => true,
      querySelector: () => null,
    },
    region: name => (name === 'board' ? board : name === 'entries' ? entries : null),
    visible: el => {
      if (!el) return false;
      el.getClientRects();
      return true;
    },
    paneObserver: {},
    paneVisible: { bcol: false, convo: false, files: true, 'cterm-panes': false },
    PANE: { board: 'bcol', convo: 'convo' },
    TAB: { board: 'board', entries: 'convo' },
    convo,
    fpane,
    grid,
    live: true,
    ctext: null,
    mpend: null,
    convoSwapping: 0,
    deferred: null,
    boardReturnTimer: 0,
    boardRelease: false,
    boardRendered: null,
    boardLatest: null,
    boardPending: {},
    boardOverviewHeld: null,
    boardDragHeld: null,
    entryRendered: null,
    requestAnimationFrame: timers.requestAnimationFrame,
    setTimeout: timers.setTimeout,
    clearTimeout: timers.clearTimeout,
    pageHidden: () => false,
    tabDot: t => (t === 'board' ? bDot : null),
    ctabDot: () => cDot,
    window: {},
    pinned: () => false,
    scrollState: () => null,
    boardRead: () => null,
    readAnswers: () => ({}),
    frameHeights: () => ({}),
    captureBoardHistory: () => null,
    anchorCards: () => [],
    boardKeep: () => null,
    boardInputFull: h => h,
    boardStageInput: h => h,
    morphBoard: () => [],
    bandBoard() {},
    entryInputFull: h => h,
    mergeEntriesRegion() {},
    outboxSync() {},
    syncSpinnerPhases() {},
    restoreScroll() {},
    anchorSettle() {},
    TL_REGIONS: {},
    tlHeld: {},
    tlCount() {},
    syncBadge() {},
    convMotionSync() {},
    syncChead() {},
    boardRecordPending() {},
    boardPendingBadge() {},
    boardHoldIndicators() {},
    boardReturnApply() {},
    boardApplied() {},
    indexFrames() {},
    bindCapsuleNotes() {},
    mermaid() {},
    syncChips() {},
    applySent() {},
    queuePinConvo() {},
    TL_REGIONS: {},
    tlHeld: {},
    tlCount() {},
    syncBadge() {},
    convMotionSync() {},
    syncChead() {},
  });
  vm.runInContext([paneShownFn, tabShownFn, convoVisFn, queueDotFns, swapFn].join('\n'), ctx);

  allNodes.forEach(n => n.resetCounters());
  ctx.swap('board', 'hb1', '<div></div>', false);
  ctx.swap('entries', 'he1', '<div></div>', false);

  const syncRects = allNodes.reduce((sum, n) => sum + n._rectCalls, 0);
  assert.equal(syncRects, 0, 'swap() must trigger zero synchronous getClientRects() calls');
  assert.equal(timers.rafCount, 1, 'multiple swaps in same frame coalesce into 1 dot check rAF');

  timers.flushRaf();
  assert.ok(bDot.classList.contains('on'), 'hidden board tab lights up tdot via cached paneVisible');
  assert.ok(cDot.classList.contains('on'), 'convo ctab lights up tdot when files pane covers convo');
  const totalRects = allNodes.reduce((sum, n) => sum + n._rectCalls, 0);
  assert.equal(totalRects, 0, 'cached paneVisible avoids getClientRects() inside flushSwapDots too');
});

test('P1-5: dashboard-audit MutationObserver coalesces onto rAF and filters to unhydrated rows', () => {
  const { makeEl } = makeDom();
  const timers = makeTimerHarness();
  const convo = makeEl('div', { attrs: { id: 'convo' } });

  // Pre-existing hydrated row in history + new unhydrated streaming row.
  const oldRow = makeEl('div', { className: 'au-row', attrs: { 'data-call': 'c-old', 'data-au-hydrated': '1' } });
  const oldTok = makeEl('span', { className: 'au-tok', text: '' });
  oldRow.appendChild(oldTok);
  const newRow = makeEl('div', { className: 'au-row', attrs: { 'data-call': 'c-new' } });
  const newTok = makeEl('span', { className: 'au-tok', text: '' });
  newRow.appendChild(newTok);
  convo.appendChild(oldRow);
  convo.appendChild(newRow);

  const observerFn = auditSrc.match(/\n  if \(window\.MutationObserver && \$\("convo"\)\) \{[\s\S]*?\n  \}\n/)[0];
  const fns = ['auCallElements', 'auSyncSelectedMarks', 'auApplyHydration', 'auHasUnhydratedRows', 'auScheduleHydration']
    .map(n => extractFn(auditSrc, n)).join('\n');

  let observerCb = null;
  const querySelectors = [];
  const origQSA = convo.querySelectorAll.bind(convo);
  convo.querySelectorAll = s => {
    querySelectors.push(s);
    return origQSA(s);
  };

  const docObj = {
    hidden: false,
    documentElement: makeEl('html'),
    querySelectorAll: s => convo.querySelectorAll(s),
  };

  function FakeMutationObserver(cb) {
    observerCb = cb;
    return { observe() {} };
  }

  const ctx = vm.createContext({
    Object, Array, isFinite,
    MutationObserver: FakeMutationObserver,
    window: { MutationObserver: FakeMutationObserver },
    doc: docObj,
    $: id => (id === 'convo' ? convo : null),
    q: (s, r) => (r || docObj).querySelectorAll(s),
    requestAnimationFrame: timers.requestAnimationFrame,
    setTimeout: timers.setTimeout,
    auWindows: [],
    auFocusedWin: null,
    auHydrateTimer: null,
    auHydratedSessions: {
      s1: {
        events: [
          { call_id: 'c-old', kind: 'tool_call', seq: 1, in_bytes: 999 },
          { call_id: 'c-new', kind: 'tool_call', seq: 2, in_bytes: 4000 },
        ],
      },
    },
    auSessionRid: () => 's1',
    auHydrateCalls() {},
    auEstContextText: (i, o) => (i + o) + 'B',
    auEstContextTitle: () => 'ctx',
    auFmtSpan: n => String(n),
    auUpdateNavButtons() {},
  });
  vm.runInContext(fns + '\n' + observerFn, ctx);
  assert.ok(observerCb, 'MutationObserver registered on #convo');

  // Burst 20 mutations synchronously.
  for (let i = 0; i < 20; i++) {
    observerCb([{ addedNodes: [newRow] }]);
  }
  assert.equal(timers.rafCount, 1, '20 burst mutations coalesce into 1 rAF');
  assert.equal(newRow.hasAttribute('data-au-hydrated'), false, 'no synchronous hydration inside microtask');
  assert.equal(querySelectors.length, 0, 'no synchronous full #convo scan during mutation callback');

  timers.flushRaf();
  assert.equal(newRow.getAttribute('data-au-hydrated'), '1', 'coalesced rAF hydrates unhydrated row');
  assert.equal(newTok.textContent, '4000B');
  assert.equal(oldTok.textContent, '', 'already-hydrated row was NOT rescanned/rewritten during incremental pass');
  assert.ok(
    querySelectors.some(s => s.includes('.au-row[data-call]:not([data-au-hydrated])')),
    'incremental pass uses :not([data-au-hydrated]) selector'
  );
  assert.equal(
    querySelectors.filter(s => s.includes('.au-hid[data-event-id]')).length,
    0,
    'empty auWindows skips scanning all audit rows inside auSyncSelectedMarks'
  );

  // Hidden page falls back to setTimeout(0).
  docObj.hidden = true;
  const newRow2 = makeEl('div', { className: 'au-row', attrs: { 'data-call': 'c-new2' } });
  newRow2.appendChild(makeEl('span', { className: 'au-tok', text: '' }));
  convo.appendChild(newRow2);
  observerCb([{ addedNodes: [newRow2] }]);
  assert.equal(timers.rafCount, 0);
  assert.equal(timers.timerCount, 1, 'hidden page falls back to timer for observer flush');
});

test('P1-6: hidden ?frame=1 and doc.hidden gate freeMeasureHeight, anchorVisibleCards, and stream reading()', () => {
  const { makeEl } = makeDom();

  // Part A: dashboard-board-anchor.js
  const docEl = makeEl('html');
  const col = makeEl('div', { attrs: { id: 'bcol' } });
  const card = makeEl('section', { className: 'topic', attrs: { 'data-topic': 't1' }, height: 320 });
  col._connected = true;
  card._connected = true;

  const anchorCtx = vm.createContext({
    Map, WeakMap, Set, Object, Array, Math,
    FREE_FALLBACK_H: 200,
    freeActive: false,
    freeWinStart: 0,
    freeWinEnd: -1,
    freePos: new Map(),
    freeHeightCache: new Map([['t:t1', 320]]),
    anchorSized: { h: 600, padTop: 0 },
    shHidden: true,
    doc: { hidden: false, documentElement: docEl },
    $: id => (id === 'bcol' ? col : null),
    anchorId: n => (n && n.getAttribute('data-topic') ? 't:' + n.getAttribute('data-topic') : null),
    anchorCurrent: () => card,
    anchorCards: () => [card],
    anchorRailInset: () => 0,
    anchorBotReserve: () => 0,
    freeLaidHeight: () => 320,
  });
  vm.runInContext(extractFn(anchorSrc, 'freeMeasureHeight') + extractFn(anchorSrc, 'anchorVisibleCards'), anchorCtx);

  card.resetCounters();
  assert.equal(anchorCtx.freeMeasureHeight(card), 320, 'hidden shHidden uses cached height without layout');
  assert.equal(card._offsetCalls, 0, 'no offsetHeight read while shHidden');
  assert.equal(card._rectCalls, 0, 'no getBoundingClientRect read while shHidden');
  assert.equal(anchorCtx.freeHeightCache._skippedHidden, true, 'records skipped measurement for later restore');
  assert.equal(anchorCtx.anchorVisibleCards().length, 0, 'anchorVisibleCards returns empty while shHidden');

  // Verify doc.hidden and data-animation-paused also gate cleanly, then re-enables when visible.
  anchorCtx.shHidden = false;
  anchorCtx.doc.hidden = true;
  assert.equal(anchorCtx.freeMeasureHeight(makeEl('section', { attrs: { 'data-topic': 'uncached' }, height: 450 })), 200);
  assert.equal(anchorCtx.anchorVisibleCards().length, 0);

  anchorCtx.doc.hidden = false;
  docEl.setAttribute('data-animation-paused', '');
  assert.equal(anchorCtx.anchorVisibleCards().length, 0);

  docEl.removeAttribute('data-animation-paused');
  card.resetCounters();
  assert.equal(anchorCtx.freeMeasureHeight(card), 320);
  assert.equal(card._offsetCalls, 1, 'visible card measures offsetHeight normally');
  assert.equal(anchorCtx.anchorVisibleCards().length, 1, 'visible card returned by anchorVisibleCards');

  // Part B: wswhiteboard/assets/stream-view.js reading(scroller)
  const scroller = makeEl('div', { attrs: { id: 'bcol' } });
  let efpCalls = 0, qsaCalls = 0;
  scroller.querySelectorAll = () => { qsaCalls++; return []; };

  const readSrc = extractFn(streamSrc, 'reading');
  const readDocEl = makeEl('html');
  const readDoc = {
    hidden: true,
    documentElement: readDocEl,
    elementFromPoint() { efpCalls++; return null; },
  };
  const readCtx = vm.createContext({
    doc: readDoc,
    docScroller: () => scroller,
    viewport: () => ({ left: 0, top: 0, width: 800, height: 600 }),
  });
  vm.runInContext(readSrc, readCtx);
  assert.equal(readCtx.reading(scroller), null, 'reading returns null when doc.hidden');
  assert.equal(efpCalls, 0);
  assert.equal(qsaCalls, 0);

  readDoc.hidden = false;
  readDocEl.setAttribute('data-animation-paused', '');
  assert.equal(readCtx.reading(scroller), null, 'reading returns null when data-animation-paused is set');
});
