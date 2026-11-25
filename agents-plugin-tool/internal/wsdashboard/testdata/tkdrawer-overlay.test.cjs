'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const assetsDir = path.join(__dirname, '../assets');
const trackerRenderSrc = fs.readFileSync(path.join(assetsDir, 'tracker-render.js'), 'utf8');
const trackerMountSrc = fs.readFileSync(path.join(assetsDir, 'tracker-mount.js'), 'utf8');
const dashboardRailSrc = fs.readFileSync(path.join(assetsDir, 'dashboard-rail.js'), 'utf8');
const dashboardLifecycleSrc = fs.readFileSync(path.join(assetsDir, 'dashboard-lifecycle.js'), 'utf8');
const shellSrc = fs.readFileSync(path.join(assetsDir, 'shell.js'), 'utf8');

function extractFn(source, name, endMarker) {
  const start = source.indexOf('\n  function ' + name + '(');
  assert.ok(start >= 0, 'found function ' + name);
  const end = endMarker
    ? source.indexOf(endMarker, start)
    : source.indexOf('\n  }\n', start) + 5;
  assert.ok(end > start, 'found end of function ' + name);
  return source.slice(start, end);
}

function makeEl(id, classes) {
  const cls = new Set(classes || []);
  const attrs = Object.create(null);
  const attrWrites = [];
  const listeners = Object.create(null);
  const el = {
    id: id || '',
    scrollTop: 0,
    innerHTML: '',
    hidden: false,
    open: false,
    firstChild: null,
    firstElementChild: { setAttribute() {}, focus() {} },
    parentNode: null,
    classList: {
      add(c) { cls.add(c); },
      remove(c) { cls.delete(c); },
      contains(c) { return cls.has(c); },
      toggle(c, force) {
        const on = force === undefined ? !cls.has(c) : !!force;
        if (on) cls.add(c); else cls.delete(c);
        return on;
      }
    },
    hasAttribute(k) { return Object.prototype.hasOwnProperty.call(attrs, k); },
    getAttribute(k) { return Object.prototype.hasOwnProperty.call(attrs, k) ? attrs[k] : null; },
    setAttribute(k, v) { attrs[k] = String(v); attrWrites.push(['set', k, String(v)]); },
    removeAttribute(k) { delete attrs[k]; attrWrites.push(['remove', k]); },
    contains(n) {
      for (let cur = n; cur; cur = cur.parentNode) if (cur === el) return true;
      return false;
    },
    querySelectorAll() { return []; },
    querySelector() { return null; },
    addEventListener(type, fn, capture) {
      (listeners[type] || (listeners[type] = [])).push({ fn, capture: !!capture });
    },
    matches(sel) {
      return sel.split(',').map(s => s.trim()).some(part => {
        if (part.startsWith('#')) return el.id === part.slice(1);
        if (part.startsWith('.')) return cls.has(part.slice(1));
        return false;
      });
    },
    closest(sel) {
      for (let cur = el; cur; cur = cur.parentNode) {
        if (cur.matches && cur.matches(sel)) return cur;
      }
      return null;
    },
    _attrs: attrs,
    _attrWrites: attrWrites,
    _listeners: listeners
  };
  return el;
}

function childOf(parent, id, classes) {
  const el = makeEl(id, classes);
  el.parentNode = parent;
  return el;
}

function trackerRenderHarness() {
  const renderSrc = extractFn(trackerRenderSrc, 'render', '\n  // A store comes on screen:');
  const collapseSrc = trackerRenderSrc.match(/\n  function collapse\(\)[^\n]*\n/)[0];
  const nodes = {
    tkdrawer: makeEl('tkdrawer', ['tk', 'tk-drawer']),
    tkpage: makeEl('tkpage', ['tk', 'tk-spage']),
    tkbody: makeEl('tkbody'),
    tkwin: makeEl('tkwin', ['tk', 'tk-iwin-back']),
    tkwinbody: makeEl('tkwinbody'),
    tkmenu: makeEl('tkmenu', ['tk', 'tk-menu'])
  };
  const syncCalls = [];
  const view = { events: [], items: {}, comments: {}, members: {} };
  const ctx = vm.createContext({
    console,
    $: id => nodes[id] || null,
    document: { body: makeEl('body'), activeElement: null, querySelector() { return null; } },
    CSS: { escape: s => String(s) },
    order: ['s1'],
    vss: {},
    EV: {},
    VS: null,
    UNREADS: null,
    FLAGGED: {},
    UNREAD: {},
    MN: null,
    MP: null,
    HV: null,
    windowHold: null,
    lastInput: 'pointer',
    renderDue: false,
    ui: { store: 's1', open: null, page: null, win: false, item: null, sel: null, focus: null, itemChanged: false, clearDraft: false },
    AD: { view: n => (n === 's1' ? view : null) },
    NK: {},
    syncHandles() {},
    dropGone() {},
    renderHandles() {},
    hideTip() {},
    insPar() {},
    setInert() {},
    dropEditorState() {},
    afterRender() {},
    measure() {},
    wkFrameSync() {},
    viewerOf() { return 'me'; },
    memberOf() { return 'me'; },
    readMap() { return {}; },
    feedEntries() { return []; },
    drawerHtml() { return '<div class="card"></div>'; },
    placeSub() {},
    placeNew() {},
    fitDrawer() {},
    wkAfterDraw() {},
    observeDetails() {},
    syncNow(n, push) { syncCalls.push([n, push]); }
  });
  vm.runInContext(renderSrc + '\n' + collapseSrc, ctx);
  return { ctx, nodes, syncCalls };
}

test('tracker-render syncs and removes #tkdrawer open attribute without redundant writes', () => {
  const { ctx, nodes, syncCalls } = trackerRenderHarness();
  const tkd = nodes.tkdrawer;

  // Initial closed render: no open class/attribute and no attribute writes.
  ctx.render();
  assert.equal(tkd.classList.contains('open'), false);
  assert.equal(tkd.hasAttribute('open'), false);
  assert.deepEqual(tkd._attrWrites, []);

  // Opening the drawer sets both the class and the open boolean attribute once.
  ctx.ui.open = 's1';
  ctx.render();
  assert.equal(tkd.classList.contains('open'), true);
  assert.equal(tkd.hasAttribute('open'), true);
  assert.equal(tkd.getAttribute('open'), '');
  assert.deepEqual(tkd._attrWrites, [['set', 'open', '']]);

  // Re-rendering while still open does not redundantly rewrite the open attribute.
  ctx.render();
  assert.equal(tkd.hasAttribute('open'), true);
  assert.deepEqual(tkd._attrWrites, [['set', 'open', '']]);

  // Collapsing clears ui.open, pushes sync, and removes both class and open attribute.
  ctx.collapse();
  assert.equal(ctx.ui.open, null);
  assert.deepEqual(syncCalls, [['s1', true]]);
  assert.equal(tkd.classList.contains('open'), false);
  assert.equal(tkd.hasAttribute('open'), false);
  assert.deepEqual(tkd._attrWrites, [['set', 'open', ''], ['remove', 'open']]);

  // Re-rendering while still closed does not redundantly remove the open attribute.
  ctx.render();
  assert.equal(tkd.hasAttribute('open'), false);
  assert.deepEqual(tkd._attrWrites, [['set', 'open', ''], ['remove', 'open']]);
});

test('outside-click dismisses #tkdrawer via collapse() while excluding all tracker overlays', () => {
  const ownSrc = trackerMountSrc.match(/\n  function own\(t\)[^\n]*\n/)[0];
  const listenSrc = extractFn(trackerMountSrc, 'listen', '\n  function mount(');
  const docListeners = [];
  const nodes = {};
  for (const id of ['tkqpop', 'tkqin', 'tkpm', 'tkmenu', 'tkcap', 'tkmpop', 'tkorged', 'tkdrawer', 'tkdlg']) {
    nodes[id] = makeEl(id, ['tk']);
  }
  const collapseCalls = [];
  const menuOutsideCalls = [];
  const ctx = vm.createContext({
    console,
    $: id => nodes[id] || makeEl(id),
    document: {
      addEventListener(type, fn, capture) { docListeners.push({ type, fn, capture: !!capture }); }
    },
    window: { addEventListener() {} },
    O: {},
    ui: { open: 's1', store: 's1', page: null, win: false, orged: null },
    qs: { open: false, S: {} },
    MN: null,
    MP: null,
    AD: {},
    MV: null,
    MVS: null,
    tipFor: null,
    lastPointer: 'mouse',
    lastInput: 'pointer',
    imgRetry() {},
    insMove() {},
    mvOver() {},
    mvDropped() {},
    pmClick() {},
    pmInput() {},
    pmKey() {},
    menuFocusOut() {},
    rangeDown() {},
    rangeMove() {},
    rangeUp() {},
    wkFramePlace() {},
    keepItemDraft() {},
    applyRoute() {},
    mentionBox() { return false; },
    menuOutside(e, t) { menuOutsideCalls.push(t); return !!ctx.MN; },
    collapse() { collapseCalls.push(ctx.ui.open); ctx.ui.open = null; }
  });
  vm.runInContext(ownSrc + '\n' + listenSrc + '\nlisten();', ctx);

  const captureClick = docListeners.find(l => l.type === 'click' && l.capture);
  assert.ok(captureClick, 'outside-click listener registers in capture phase');
  const bubbleClick = docListeners.find(l => l.type === 'click' && !l.capture);
  assert.ok(bubbleClick, 'existing tracker bubble click listener exists');

  function fireClick(target) {
    const ev = { target, defaultPrevented: false, preventDefault() { this.defaultPrevented = true; }, stopPropagation() {} };
    captureClick.fn(ev);
    bubbleClick.fn(ev);
  }

  // Every tracker overlay, trigger, dialog, confirm modal, and generic .tk subtree keeps #tkdrawer open.
  const tkdrawer = makeEl('tkdrawer', ['tk', 'tk-drawer']);
  const tkclip = childOf(tkdrawer, 'tkclip', ['tk-dr-clip']);
  const tkhandles = childOf(tkdrawer, 'tkhandles', ['tk-handles']);
  const tkwin = makeEl('tkwin', ['tk', 'tk-iwin-back']);
  const excludedTargets = [
    ['#tkdrawer', tkdrawer],
    ['#tkclip child', childOf(tkclip, '', ['irow'])],
    ['#tkhandles', tkhandles],
    ['.tk-handle inside #tkhandles', childOf(tkhandles, 'tkh-s1', ['tk-handle'])],
    ['.tk-fold inside #tkhandles', childOf(tkhandles, 'tkf-s1', ['tk-fold'])],
    ['#tkmenu child', childOf(makeEl('tkmenu', ['tk', 'tk-menu']), '', ['mopt'])],
    ['#tkcap', makeEl('tkcap', ['tk', 'tk-mcap'])],
    ['#tkwin', tkwin],
    ['#tkmpop inside #tkwin', childOf(childOf(tkwin, 'tkmpop', ['tk-mpop']), '', ['mp-row'])],
    ['#tkdlg child', childOf(makeEl('tkdlg', ['tk', 'tk-dlg']), '', ['btn'])],
    ['#tkpm child', childOf(makeEl('tkpm', ['tk', 'tk-pm']), '', ['btn'])],
    ['#tkorged child', childOf(makeEl('tkorged', ['tk', 'tk-dlg', 'tk-orged']), '', ['btn'])],
    ['#tkqpop child', childOf(makeEl('tkqpop', ['tk', 'tk-palette']), '', ['tk-qrow'])],
    ['#tkqs', makeEl('tkqs', ['tk-qsbtn'])],
    ['.ws-confirm child', childOf(makeEl('', ['ws-confirm']), '', ['btn'])],
    ['#tkframe (.tk own(t))', childOf(makeEl('tkframe', ['tk', 'tk-wkframe']), '', ['inner'])],
    ['#tkpage (.tk own(t))', childOf(makeEl('tkpage', ['tk', 'tk-spage']), '', ['inner'])],
    ['#tktoast (.tk own(t))', makeEl('tktoast', ['tk', 'tk-toast'])],
    ['#tktip (.tk own(t))', makeEl('tktip', ['tk', 'tk-tip'])]
  ];
  for (const [label, target] of excludedTargets) {
    ctx.ui.open = 's1';
    collapseCalls.length = 0;
    captureClick.fn({ target });
    assert.equal(ctx.ui.open, 's1', label + ' must not dismiss #tkdrawer');
    assert.deepEqual(collapseCalls, [], label + ' must not call collapse()');
  }

  // Non-element targets without closest() are safely ignored.
  ctx.ui.open = 's1';
  captureClick.fn({ target: {} });
  assert.equal(ctx.ui.open, 's1');
  assert.deepEqual(collapseCalls, []);

  // While a tracker menu (MN) is open, an outside click defers to menuOutside instead of collapsing #tkdrawer.
  const hostBoard = makeEl('board', ['board']);
  ctx.ui.open = 's1';
  ctx.MN = { kind: 'item' };
  fireClick(hostBoard);
  assert.equal(ctx.ui.open, 's1', 'open menu lets menuOutside handle the first outside click');
  assert.deepEqual(collapseCalls, []);
  assert.equal(menuOutsideCalls.length, 1);

  // Once MN is closed, clicking outside on host UI collapses #tkdrawer and clears ui.open.
  ctx.MN = null;
  fireClick(hostBoard);
  assert.equal(ctx.ui.open, null);
  assert.deepEqual(collapseCalls, ['s1']);

  // Clicking outside when already closed is a no-op.
  collapseCalls.length = 0;
  fireClick(makeEl('rail-ph', ['rail', 'rail-ph']));
  assert.deepEqual(collapseCalls, []);
});

test('Ctrl+Backquote in framed page and shell railOpen collapse #tkdrawer and clear data-lifted', () => {
  const railKeySrc = extractFn(dashboardRailSrc, 'railKey');
  const railOpenSrc = extractFn(dashboardRailSrc, 'railOpen');
  const shFrameInitSrc = dashboardLifecycleSrc.match(/\n  function shFrameInit\(\) \{[\s\S]*?\n  \}\n  \/\/ shShellInit/)[0];

  const trace = [];
  const frames = new Map();
  let seq = 0;
  const observers = [];
  const docListeners = Object.create(null);
  const winListeners = Object.create(null);
  const rootAttrs = Object.create(null);
  const rootClasses = new Set();
  const tkdrawer = makeEl('tkdrawer', ['tk', 'tk-drawer']);
  tkdrawer.getClientRects = () => (tkdrawer.hasAttribute('open') ? [{}] : []);
  const parentWin = {};

  function notifyBodyAttr() {
    const bodyObs = observers.find(o => o.nodes.some(([n, opts]) => n === frameCtx.doc.body && opts && opts.attributeFilter));
    if (bodyObs) bodyObs.fn([]);
  }

  const frameCtx = vm.createContext({
    console,
    framed: true,
    shell: false,
    shSettingsEntry: null,
    shHidden: false,
    shTrackerRev: null,
    railEl: null,
    WsRail: null,
    memoPad: null,
    railBtn: null,
    location: { origin: 'http://localhost', hash: '' },
    history: {},
    requestAnimationFrame(fn) { const id = ++seq; frames.set(id, fn); return id; },
    cancelAnimationFrame(id) { frames.delete(id); },
    getComputedStyle() { return { paddingRight: '12px', backgroundColor: 'rgba(0,0,0,.5)' }; },
    MutationObserver: function MutationObserver(fn) {
      this.fn = fn;
      this.nodes = [];
      this.observe = (n, opts) => { this.nodes.push([n, opts]); };
      observers.push(this);
    },
    ResizeObserver: function ResizeObserver() { this.observe = () => {}; },
    root: {
      clientWidth: 1180,
      hasAttribute: k => Object.prototype.hasOwnProperty.call(rootAttrs, k),
      setAttribute: (k, v) => { rootAttrs[k] = String(v); },
      removeAttribute: k => { delete rootAttrs[k]; },
      classList: {
        contains: c => rootClasses.has(c),
        toggle: (c, on) => { if (on) rootClasses.add(c); else rootClasses.delete(c); }
      }
    },
    doc: {
      body: {},
      title: 'Session',
      querySelector: () => ({ innerHTML: 'head', offsetHeight: 40 }),
      addEventListener(type, fn) { (docListeners[type] || (docListeners[type] = [])).push(fn); }
    },
    window: {
      innerWidth: 1200,
      scrollY: 0,
      parent: parentWin,
      addEventListener(type, fn) { (winListeners[type] || (winListeners[type] = [])).push(fn); }
    },
    region: () => ({ innerHTML: 'head', offsetHeight: 40 }),
    $: id => (id === 'tkdrawer' ? tkdrawer : null),
    q(sel) {
      if (sel === '.rpop:not([hidden]), .lmenu, #tkdrawer[open]') return tkdrawer.hasAttribute('open') ? [tkdrawer] : [];
      return [];
    },
    tkApi: {
      closeStore() {
        trace.push('closeStore');
        if (tkdrawer.hasAttribute('open')) {
          tkdrawer.removeAttribute('open');
          notifyBodyAttr();
        }
      }
    },
    shPost(kind, m) { trace.push('post:' + kind + (m && m.key ? ':' + m.key : '')); }
  });
  frameCtx.window.MutationObserver = frameCtx.MutationObserver;
  frameCtx.window.ResizeObserver = frameCtx.ResizeObserver;
  vm.runInContext(shellSrc + '\n' + railKeySrc + '\n' + railOpenSrc + '\n' + shFrameInitSrc + '\nshFrameInit();', frameCtx);

  function flushFrames() {
    const batch = Array.from(frames.values());
    frames.clear();
    batch.forEach(fn => fn());
  }

  // Open #tkdrawer -> frame lifts (data-lifted + layout popover:true).
  tkdrawer.setAttribute('open', '');
  notifyBodyAttr();
  flushFrames();
  assert.equal(frameCtx.root.hasAttribute('data-lifted'), true);

  // Ctrl+Backquote inside the framed page collapses #tkdrawer BEFORE posting key:rail and lowers the frame.
  trace.length = 0;
  let prevented = false;
  docListeners.keydown.forEach(fn => fn({
    code: 'Backquote', ctrlKey: true, altKey: false, metaKey: false, shiftKey: false, isComposing: false,
    preventDefault() { prevented = true; }
  }));
  assert.equal(prevented, true);
  assert.equal(tkdrawer.hasAttribute('open'), false);
  assert.deepEqual(trace, ['closeStore', 'post:key:rail']);
  flushFrames();
  assert.equal(frameCtx.root.hasAttribute('data-lifted'), false);
  assert.equal(trace[trace.length - 1], 'post:layout');

  // Parent-shell railOpen(true) sends a valid rail-open message to shOn; delivering it to the frame collapses #tkdrawer.
  const shellSent = [];
  const memoCalls = [];
  const shellClasses = new Set();
  const shellCtx = vm.createContext({
    console,
    window: {},
    framed: false,
    shell: true,
    shSettingsEntry: null,
    shOn: { popover: true },
    railEl: {},
    WsRail: {},
    railBtn: { setAttribute() {} },
    memoPad: { open(on, wide) { memoCalls.push([on, wide]); } },
    railWide: () => false,
    railShown: () => shellClasses.has('rail-open'),
    root: { classList: { toggle(c, on) { if (on) shellClasses.add(c); else shellClasses.delete(c); } } },
    shSend(ent, kind) { shellSent.push([ent, kind]); }
  });
  vm.runInContext(shellSrc + '\n' + railKeySrc + '\n' + railOpenSrc, shellCtx);

  shellCtx.railOpen(true);
  assert.equal(shellClasses.has('rail-open'), true);
  assert.deepEqual(memoCalls, [[true, false]]);
  assert.equal(shellSent.length, 1);
  assert.equal(shellSent[0][1], 'rail-open');
  assert.ok(shellCtx.window.WsShell.message({ wsShell: 'rail-open' }), 'WsShell.message accepts rail-open');

  // Closing the rail does not emit rail-open.
  shellSent.length = 0;
  shellCtx.railOpen(false);
  assert.deepEqual(shellSent, []);

  // Re-open #tkdrawer in the frame, then deliver the shell's rail-open message.
  tkdrawer.setAttribute('open', '');
  notifyBodyAttr();
  flushFrames();
  assert.equal(frameCtx.root.hasAttribute('data-lifted'), true);

  trace.length = 0;
  winListeners.message.forEach(fn => fn({
    source: parentWin,
    origin: 'http://localhost',
    data: { wsShell: 'rail-open' }
  }));
  assert.equal(tkdrawer.hasAttribute('open'), false);
  assert.deepEqual(trace, ['closeStore']);
  flushFrames();
  assert.equal(frameCtx.root.hasAttribute('data-lifted'), false);
  assert.equal(trace[trace.length - 1], 'post:layout');

  // Standalone (unframed, non-shell) railOpen(true) also collapses #tkdrawer directly via tkApi.closeStore().
  let standaloneClosed = 0;
  const standaloneClasses = new Set();
  const standaloneCtx = vm.createContext({
    console,
    window: {},
    framed: false,
    shell: false,
    railEl: {},
    railBtn: null,
    memoPad: null,
    railWide: () => false,
    root: { classList: { toggle(c, on) { if (on) standaloneClasses.add(c); else standaloneClasses.delete(c); } } },
    tkApi: { closeStore() { standaloneClosed++; } }
  });
  vm.runInContext(railOpenSrc, standaloneCtx);
  standaloneCtx.railOpen(true);
  assert.equal(standaloneClosed, 1);
  standaloneCtx.railOpen(false);
  assert.equal(standaloneClosed, 1);
});
