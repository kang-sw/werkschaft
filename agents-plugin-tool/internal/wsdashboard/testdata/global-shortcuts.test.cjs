// Offline VM contract for dashboard global shortcuts (Ctrl+Q/G/B/E):
// key-matching, editable/xterm suppression, overlay switching, open Ticket Map
// capture handler, shell forwarding, and doc-frame relay.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

const dashJS = readAssetSource('wsdashboard', 'dashboard.js');
const shellJS = readAssetSource('wsdashboard', 'shell.js');
const docFrameJS = fs.readFileSync(path.join(__dirname, '../../wswhiteboard/assets/doc-frame.js'), 'utf8');

function extractBlock(src, from, to) {
  const start = src.indexOf(from);
  assert.ok(start >= 0, 'missing start marker: ' + from);
  const end = src.indexOf(to, start);
  assert.ok(end > start, 'missing end marker: ' + to);
  return src.slice(start, end);
}

const shortcutBlock = extractBlock(dashJS, '  function overviewButton(d) {', '\n  if (shell && window.WsShell) {');
const frameMsgBlock = extractBlock(dashJS, '  window.addEventListener("message", function (e) {\n    var f = docFrame(e.source)', '\n  // A swap re-creates a changed topic');
const tmapOnKeyBlock = extractBlock(dashJS, '    function onKey(e) {\n      if (!isOpen()) return;', '\n    // Tab stays inside the overlay');

function makeHarness(opts = {}) {
  const state = {
    shell: !!opts.shell,
    shHidden: !!opts.shHidden,
    shSettingsEntry: opts.shSettingsEntry || null,
    overviewPresent: opts.overviewPresent !== false,
    overviewVisible: opts.overviewVisible !== false,
    overviewHolding: !!opts.overviewHolding,
    gitPresent: opts.gitPresent !== false,
    drOpen: !!opts.drOpen,
    drMode: opts.drMode || 'panels',
    tmapDockPresent: opts.tmapDockPresent !== false,
    tmapOpen: !!opts.tmapOpen,
    filesBtnPresent: opts.filesBtnPresent !== false,
    filesBtnVisible: opts.filesBtnVisible !== false,
    filesBtnDisabled: !!opts.filesBtnDisabled,
    filesSheetOpen: !!opts.filesSheetOpen,
    events: []
  };

  const overviewBtn = {
    getClientRects: () => (state.overviewVisible ? [{}] : []),
    click: () => { state.events.push('overview:click'); state.overviewHolding = !state.overviewHolding; }
  };
  const gitDock = {};
  const tmapDock = {};
  const tmapBack = {
    get hidden() { return !state.tmapOpen; }
  };
  const filesBtn = {
    get disabled() { return state.filesBtnDisabled; },
    getClientRects: () => (state.filesBtnVisible ? [{}] : []),
    click: () => { state.events.push('files:click'); state.filesSheetOpen = !state.filesSheetOpen; }
  };
  const filesSheetEl = {
    get hidden() { return !state.filesSheetOpen; }
  };

  let keydownHandler = null;
  const doc = {
    getElementById(id) {
      if (id === 'gitdock') return state.gitPresent ? gitDock : null;
      if (id === 'tmdock') return state.tmapDockPresent ? tmapDock : null;
      if (id === 'tmback') return state.tmapOpen ? tmapBack : null;
      if (id === 'toolbar-files') return filesSheetEl;
      return null;
    },
    querySelector(sel) {
      if (sel === '.board-overview') return state.overviewPresent ? overviewBtn : null;
      if (sel === '.board-files') return state.filesBtnPresent ? filesBtn : null;
      if (sel === '#gitdock') return this.getElementById('gitdock');
      if (sel === '#tmdock') return this.getElementById('tmdock');
      if (sel === '#tmback') return this.getElementById('tmback');
      if (sel === '#toolbar-files') return this.getElementById('toolbar-files');
      return null;
    },
    addEventListener(kind, fn) { if (kind === 'keydown') keydownHandler = fn; }
  };

  const shOn = opts.noFrame ? null : { el: { contentDocument: doc } };

  const ctx = vm.createContext({
    doc,
    shell: state.shell,
    shHidden: state.shHidden,
    shSettingsEntry: state.shSettingsEntry,
    shOn,
    drawer: {},
    get drOpen() { return state.drOpen; },
    get drMode() { return state.drMode; },
    drSetOpen(open, mode) {
      state.events.push(['drSetOpen', open, mode || state.drMode]);
      if (mode) state.drMode = mode;
      state.drOpen = !!open;
    },
    ticketMap: {
      isOpen: () => state.tmapOpen,
      open: () => {
        if (state.tmapOpen) return;
        state.events.push(['tmap:open']);
      },
      close: (yieldTo) => {
        if (!state.tmapOpen) return;
        state.tmapOpen = false;
        state.events.push(['tmap:close', !!yieldTo]);
      }
    },
    gitPage: { open() { state.events.push('gitPage:open'); } },
    railKey() { state.events.push('railKey'); },
    shSend(ent, kind) {
      assert.equal(ent, shOn);
      state.events.push(['shSend', kind]);
    },
    window: {
      WsCards: {
        dismissFiles() {
          if (!state.filesSheetOpen) return;
          state.filesSheetOpen = false;
          state.events.push('WsCards:dismissFiles');
        }
      },
      WsOverview: {
        holding: () => state.overviewHolding,
        after: (fn) => {
          state.events.push('WsOverview:after');
          state.overviewHolding = false;
          fn();
        }
      }
    }
  });

  vm.runInContext(shortcutBlock, ctx);
  assert.equal(typeof keydownHandler, 'function');

  function fire(attrs, targetSelector) {
    state.events.length = 0;
    const target = attrs && attrs.target !== undefined ? attrs.target : {
      closest(s) {
        if (!targetSelector) return false;
        const sels = Array.isArray(targetSelector) ? targetSelector : [targetSelector];
        return sels.some(sel => s.indexOf(sel) >= 0);
      }
    };
    const e = Object.assign({
      key: '',
      code: '',
      ctrlKey: true,
      metaKey: false,
      altKey: false,
      shiftKey: false,
      isComposing: false,
      keyCode: 0,
      defaultPrevented: false,
      preventDefault() { this.defaultPrevented = true; }
    }, attrs, { target });
    keydownHandler(e);
    return { prevented: !!e.defaultPrevented, events: state.events.slice() };
  }

  return { state, ctx, fire, doc };
}

test('Ctrl+G shows the Git page idempotently and ignores non-git sessions', () => {
  const h = makeHarness();
  let r = h.fire({ key: 'g' });
  assert.equal(r.prevented, true);
  assert.deepEqual(r.events, ['gitPage:open']);

  // A repeat only shows and refocuses the page again: no toggle, no drawer.
  r = h.fire({ key: 'G' });
  assert.equal(r.prevented, true);
  assert.deepEqual(r.events, ['gitPage:open']);
  assert.equal(h.state.drOpen, false);

  // An open panels drawer closes before the page shows.
  h.state.drOpen = true;
  h.state.drMode = 'panels';
  r = h.fire({ key: 'g' });
  assert.equal(r.prevented, true);
  assert.deepEqual(r.events, [['drSetOpen', false, 'panels'], 'gitPage:open']);
  assert.equal(h.state.drOpen, false);

  // Non-git session (#gitdock absent): do nothing, do not swallow.
  const noGit = makeHarness({ gitPresent: false });
  assert.deepEqual(noGit.fire({ key: 'g' }), { prevented: false, events: [] });
});

test('Ctrl+B activates the docked Ticket Map, closes the expanded one, and respects unavailability', () => {
  const h = makeHarness();
  let r = h.fire({ key: 'b' });
  assert.equal(r.prevented, true);
  assert.deepEqual(r.events, [['tmap:open']], 'Ctrl+B activates and focuses the docked page');
  r = h.fire({ key: 'b' });
  assert.deepEqual(r.events, [['tmap:open']], 'a repeat Ctrl+B re-activates the docked page, never a modal toggle');

  // No docked page (#tmdock absent: no gallery) does nothing when closed.
  const none = makeHarness({ tmapDockPresent: false });
  assert.deepEqual(none.fire({ key: 'b' }), { prevented: false, events: [] });

  // The expanded #tmback closes on Ctrl+B, even with no docked page.
  const expanded = makeHarness({ tmapDockPresent: false, tmapOpen: true });
  r = expanded.fire({ key: 'b' });
  assert.equal(r.prevented, true);
  assert.deepEqual(r.events, [['tmap:close', false]]);
  assert.equal(expanded.state.tmapOpen, false);
});

test('open Ticket Map capture onKey closes on Ctrl+B and routes Ctrl+G/E/Q/` cleanly', () => {
  const h = makeHarness({ tmapOpen: true });
  let closed = [];
  const tmapCtx = vm.createContext({
    isOpen: () => h.state.tmapOpen,
    close: (yieldTo) => {
      h.state.tmapOpen = false;
      closed.push(!!yieldTo);
    },
    cur: () => null,
    E: { full: { hidden: true }, q: {} },
    G: null,
    S: { view: 'B' },
    typing: () => true,
    escape: () => { closed.push('escape'); },
    focusSearch: () => {},
    railKey: () => h.state.events.push('railKey'),
    runGlobalShortcut: (kind) => h.ctx.runGlobalShortcut(kind)
  });
  vm.runInContext(tmapOnKeyBlock, tmapCtx);

  function fireCapture(attrs) {
    closed = [];
    h.state.events.length = 0;
    const e = Object.assign({
      key: '',
      code: '',
      ctrlKey: true,
      metaKey: false,
      altKey: false,
      shiftKey: false,
      isComposing: false,
      keyCode: 0,
      defaultPrevented: false,
      stopped: false,
      target: tmapCtx.E.q,
      preventDefault() { this.defaultPrevented = true; },
      stopImmediatePropagation() { this.stopped = true; }
    }, attrs);
    tmapCtx.onKey(e);
    return { prevented: !!e.defaultPrevented, stopped: !!e.stopped, closed, events: h.state.events.slice() };
  }

  // Ctrl+B closes Ticket Map even while E.q input is focused.
  let r = fireCapture({ key: 'b' });
  assert.equal(r.prevented, true);
  assert.equal(r.stopped, true);
  assert.deepEqual(r.closed, [false]);
  assert.equal(h.state.tmapOpen, false);

  // Ctrl+G while Ticket Map is open closes Ticket Map (yielding focus) and shows the Git page.
  h.state.tmapOpen = true;
  r = fireCapture({ key: 'g' });
  assert.equal(r.prevented, true);
  assert.equal(r.stopped, true);
  assert.equal(h.state.tmapOpen, false);
  assert.deepEqual(r.events, [['tmap:close', true], 'gitPage:open']);

  // Ctrl+G while Ticket Map is open on a non-git session keeps Ticket Map open.
  h.state.tmapOpen = true;
  h.state.gitPresent = false;
  r = fireCapture({ key: 'g' });
  assert.equal(r.prevented, false);
  assert.equal(h.state.tmapOpen, true);
  h.state.gitPresent = true;

  // Ctrl+E while Ticket Map is open closes Ticket Map and opens floating Files.
  h.state.tmapOpen = true;
  h.state.drOpen = false;
  r = fireCapture({ key: 'e' });
  assert.equal(r.prevented, true);
  assert.equal(h.state.tmapOpen, false);
  assert.deepEqual(r.events, [['tmap:close', true], 'files:click']);

  // Ctrl+Q while Ticket Map is open closes Ticket Map and toggles Overview.
  h.state.tmapOpen = true;
  h.state.filesSheetOpen = false;
  r = fireCapture({ key: 'q' });
  assert.equal(r.prevented, true);
  assert.equal(h.state.tmapOpen, false);
  assert.deepEqual(r.events, [['tmap:close', true], 'overview:click']);

  // Ctrl+` while Ticket Map is open closes Ticket Map (yielding focus) and toggles Rail.
  h.state.tmapOpen = true;
  r = fireCapture({ key: '`', code: 'Backquote' });
  assert.equal(r.prevented, true);
  assert.equal(r.stopped, true);
  assert.deepEqual(r.closed, [true]);
  assert.deepEqual(r.events, ['railKey']);
});

test('Ctrl+E toggles floating board files sheet and allows closing from #toolbar-files filter input', () => {
  const h = makeHarness();
  let r = h.fire({ key: 'e' });
  assert.equal(r.prevented, true);
  assert.deepEqual(r.events, ['files:click']);
  assert.equal(h.state.filesSheetOpen, true);

  // Pressing Ctrl+E while focus is inside #toolbar-files .fq input closes cleanly.
  r = h.fire({ key: 'e' }, ['input', '#toolbar-files']);
  assert.equal(r.prevented, true);
  assert.deepEqual(r.events, ['WsCards:dismissFiles']);
  assert.equal(h.state.filesSheetOpen, false);

  // When closed, Ctrl+E inside a regular input is suppressed.
  r = h.fire({ key: 'e' }, 'input');
  assert.deepEqual(r, { prevented: false, events: [] });

  // Worktree/index (no .board-files) or hidden board tab: do nothing, do not swallow.
  assert.deepEqual(makeHarness({ filesBtnPresent: false }).fire({ key: 'e' }), { prevented: false, events: [] });
  assert.deepEqual(makeHarness({ filesBtnVisible: false }).fire({ key: 'e' }), { prevented: false, events: [] });
});

test('smooth overlay switching between Overview, the Git page, Ticket Map, and Floating Files', () => {
  // Overview -> Ctrl+G exits Overview via WsOverview.after then shows the Git page.
  const h1 = makeHarness({ overviewHolding: true });
  assert.deepEqual(h1.fire({ key: 'g' }), {
    prevented: true,
    events: ['WsOverview:after', 'gitPage:open']
  });

  // Overview -> Ctrl+B exits Overview via WsOverview.after then opens Ticket Map.
  const h2 = makeHarness({ overviewHolding: true });
  assert.deepEqual(h2.fire({ key: 'b' }), {
    prevented: true,
    events: ['WsOverview:after', ['tmap:open']]
  });

  // Floating Files -> Ctrl+G dismisses Files and shows the Git page.
  const h3 = makeHarness({ filesSheetOpen: true });
  assert.deepEqual(h3.fire({ key: 'g' }), {
    prevented: true,
    events: ['WsCards:dismissFiles', 'gitPage:open']
  });

  // Panels drawer -> Ctrl+E closes drawer and opens Floating Files.
  const h4 = makeHarness({ drOpen: true, drMode: 'panels' });
  assert.deepEqual(h4.fire({ key: 'e' }), {
    prevented: true,
    events: [['drSetOpen', false, 'panels'], 'files:click']
  });
});

test('editable, terminal, IME, modifier, and settings guards apply to Ctrl+G/B/E', () => {
  const h = makeHarness();
  for (const key of ['g', 'b', 'e']) {
    for (const sel of ['input', 'textarea', 'select', '[contenteditable]', '.xterm', '.tm-host']) {
      assert.deepEqual(h.fire({ key }, sel), { prevented: false, events: [] }, `${key} in ${sel}`);
    }
    assert.deepEqual(h.fire({ key, target: { isContentEditable: true } }), { prevented: false, events: [] });
    for (const bad of [{ ctrlKey: false }, { metaKey: true }, { altKey: true }, { shiftKey: true }, { isComposing: true }, { keyCode: 229 }]) {
      assert.deepEqual(h.fire(Object.assign({ key }, bad)), { prevented: false, events: [] });
    }
    assert.deepEqual(h.fire({ key, defaultPrevented: true }), { prevented: true, events: [] });
  }
  const inSettings = makeHarness({ shSettingsEntry: {} });
  for (const key of ['g', 'b', 'e']) {
    assert.deepEqual(inSettings.fire({ key }), { prevented: false, events: [] });
  }
});

test('shell forwards Ctrl+G/B/E to shown frame and WsShell validates new message kinds', () => {
  const shCtx = vm.createContext({ window: {} });
  vm.runInContext(shellJS, shCtx);
  for (const kind of ['overview', 'git', 'ticketmap', 'files']) {
    assert.ok(shCtx.window.WsShell.message({ wsShell: kind }), 'WsShell accepts ' + kind);
  }

  const h = makeHarness({ shell: true });
  assert.deepEqual(h.fire({ key: 'g' }), { prevented: true, events: [['shSend', 'git']] });
  assert.deepEqual(h.fire({ key: 'b' }), { prevented: true, events: [['shSend', 'ticketmap']] });
  assert.deepEqual(h.fire({ key: 'e' }), { prevented: true, events: [['shSend', 'files']] });

  // Unavailable target in shown frame is not forwarded or swallowed.
  const unavail = makeHarness({ shell: true, gitPresent: false, tmapDockPresent: false, filesBtnPresent: false });
  assert.deepEqual(unavail.fire({ key: 'g' }), { prevented: false, events: [] });
  assert.deepEqual(unavail.fire({ key: 'b' }), { prevented: false, events: [] });
  assert.deepEqual(unavail.fire({ key: 'e' }), { prevented: false, events: [] });
});

test('doc-frame posts wbDoc for Ctrl+Q/G/B/E and dashboard-frames routes them to runGlobalShortcut', () => {
  const docKeyBlock = extractBlock(
    docFrameJS,
    '  // Like the session document, leave editing, terminal and IME input native.\n',
    '  // Ctrl/Cmd+F inside a framed card'
  );
  let posted = [];
  let docHandler = null;
  const dfCtx = vm.createContext({
    d: { addEventListener(kind, fn) { if (kind === 'keydown') docHandler = fn; } },
    post(m) { posted.push(m); }
  });
  vm.runInContext(docKeyBlock, dfCtx);
  for (const [key, want] of [['q', 'overview'], ['g', 'git'], ['b', 'ticketmap'], ['e', 'files']]) {
    posted = [];
    const e = { key, ctrlKey: true, target: { closest: () => false }, preventDefault() { this.defaultPrevented = true; } };
    docHandler(e);
    assert.equal(e.defaultPrevented, true);
    assert.deepEqual(JSON.parse(JSON.stringify(posted)), [{ wbDoc: want }]);
  }

  // Host receiver in dashboard-frames.js routes each wbDoc to runGlobalShortcut under userActivation.
  const ran = [];
  let msgHandler = null;
  const frameWin = {};
  const frameEl = { getAttribute: () => 'topic:body' };
  const hostCtx = vm.createContext({
    window: { addEventListener(kind, fn) { if (kind === 'message') msgHandler = fn; } },
    docFrame: (src) => (src === frameWin ? frameEl : null),
    navigator: { userActivation: { isActive: true } },
    shHidden: false,
    runGlobalShortcut: (kind) => { ran.push(kind); return true; }
  });
  vm.runInContext(frameMsgBlock, hostCtx);
  for (const kind of ['overview', 'git', 'ticketmap', 'files']) {
    msgHandler({ source: frameWin, data: { wbDoc: kind, docKey: 'topic:body' } });
  }
  assert.deepEqual(ran, ['overview', 'git', 'ticketmap', 'files']);
});
