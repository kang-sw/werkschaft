const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const wb = __dirname + '/../wswhiteboard/';
const read = file => fs.readFileSync(file, 'utf8');
function fixture(file, name) { return read(wb + file).split('const ' + name + ' = `')[1].split('`')[0]; }
function section(source, start, end) { return source.slice(source.indexOf(start), source.indexOf(end, source.indexOf(start))); }

test('pill IO/draft work coalesces and identical counts write nothing', () => {
  const context = { console }; vm.createContext(context);
  const model = fixture('pill_segments_test.go', 'pillSegPrelude');
  const instrumentation = `
var writes=0;
Object.defineProperty(E.prototype,'textContent',{get:function(){return this._text||'';},set:function(v){writes++;this._text=v;}});
Object.defineProperty(E.prototype,'hidden',{get:function(){return !!this._hidden;},set:function(v){writes++;this._hidden=v;}});
var setAttr=E.prototype.setAttribute;E.prototype.setAttribute=function(k,v){writes++;setAttr.call(this,k,v);};
`;
  vm.runInContext(model + instrumentation + read(wb + 'assets/pill.js') + '\nfire([]); writes=0; window.wbPill.update();window.wbPill.update();window.wbPill.update();', context);
  assert.equal(context.frames.length, 1);
  assert.equal(context.writes, 0);
  vm.runInContext('frame();', context);
  assert.equal(context.writes, 0);
  const contract = read(wb + 'pill_segments_test.go').split('script := pillSegPrelude + string(data) + `')[1].split('`')[0];
  context.process = { stdout: { write(value) { assert.equal(value, ''); } } };
  vm.runInContext(contract, context);
});

test('rail ghosts preserve scroll-only transitions with transform/opacity keyframes', () => {
  const contract = read(wb + 'rail_motion_test.go');
  const setup = contract.split('script := railPrelude + `')[1].split('` + string(data) + `')[0].replace('anims.push(this);', 'anims.push(this); keyframes.push(k);');
  const assertions = contract.split('` + string(data) + `')[1].split('`')[0];
  const context = { process: { stdout: { write(value) { assert.equal(value, ''); } } } };
  vm.createContext(context);
  vm.runInContext(fixture('rail_offsets_test.go', 'railPrelude') + '\nvar keyframes=[];\n' + setup + read(wb + 'assets/rail.js') + assertions, context);
  assert.ok(context.keyframes.length > 0);
  for (const frames of context.keyframes) for (const frame of frames) {
    assert.ok(Object.keys(frame).every(key => ['transform', 'opacity', 'offset'].includes(key)), JSON.stringify(frame));
  }
});

test('cards toolbar scrolls coalesce, reuse scroller, and read before writing', () => {
  const frames = [], handlers = {}, order = [], resize = [];
  const style = () => { const values = {}; return { getPropertyValue(k) { return values[k] || ''; }, setProperty(k,v) { order.push('write'); values[k] = v; }, removeProperty(k) { delete values[k]; } }; };
  const rect = (top, height, width = 300) => ({ top, height, width, bottom: top + height });
  const face = { getBoundingClientRect() { order.push('read'); return rect(0, 46); }, firstChild: { children: [{ getBoundingClientRect() { order.push('read'); return rect(0, 30); } }] } };
  const attrs = new Set(), toolbar = { firstChild: face, style: style(), hasAttribute: k => attrs.has(k), toggleAttribute(k,v) { order.push('write'); if (v) attrs.add(k); else attrs.delete(k); } };
  const board = { style: style(), getBoundingClientRect() { order.push('read'); return rect(-20, 1000); } };
  const scroller = { style: style() }; let walks = 0;
  const context = { toolbar, board, maxed: null,
    doc: { scrollingElement: scroller, querySelectorAll() { return []; }, getElementById() { return board; }, addEventListener(type, fn) { handlers[type] = fn; } },
    window: { addEventListener() {}, ResizeObserver: class { constructor(fn) { resize.push(fn); } observe() {} } },
    scroller() { walks++; return scroller; }, requestAnimationFrame(fn) { frames.push(fn); return frames.length; }
  };
  vm.createContext(context);
  vm.runInContext(section(read(__dirname + '/assets/cards.js'), '  function floatToolbar()', '  function filterMark(') + '\nfloatToolbar();', context);
  assert.equal(frames.length, 1); frames.shift()();
  assert.ok(order.lastIndexOf('read') < order.indexOf('write'));
  order.length = 0;
  handlers.scroll(); handlers.scroll(); handlers.scroll();
  assert.equal(frames.length, 1); frames.shift()();
  assert.equal(walks, 1);
  resize[0](); frames.shift()(); assert.equal(walks, 2);
});

test('pane scroll bookkeeping uses observer visibility instead of geometry probes', () => {
  const frames = [], handlers = {}, order = []; let observer;
  const pane = { id: 'convo', scrollTop: 80, addEventListener(type, fn) { handlers[type] = fn; }, getClientRects() { throw Error('visibility geometry forbidden'); } };
  const context = { PANES: ['convo'], window: { ResizeObserver: true },
    ResizeObserver: class { constructor(fn) { observer = fn; } observe() {} },
    $(id) { return pane; }, doc: {}, visible() { throw Error('live visibility probe forbidden'); },
    requestAnimationFrame(fn) { frames.push(fn); return frames.length; }
  };
  vm.createContext(context);
  vm.runInContext(section(read(__dirname + '/assets/dashboard.js'), '  var paneScroll =', '  // Restore after'), context);
  observer([{ target: pane, contentRect: { width: 300, height: 200 } }]);
  handlers.scroll(); handlers.scroll(); assert.equal(frames.length, 1); frames.shift()();
  assert.equal(context.paneScroll.convo, 80);
  assert.equal(context.scrollState()[0][1], 80);
  observer([{ target: pane, contentRect: { width: 0, height: 0 } }]);
  pane.scrollTop = 0; handlers.scroll(); frames.shift()();
  assert.equal(context.paneScroll.convo, 80);
  assert.equal(context.scrollState().length, 0);
});

// The page-visibility section of dashboard.js under a stub window: topDoc is
// the shell's top document (same-origin), listeners collects what the section
// registers on window, and swaps counts showRegions flushes of held regions.
// docListeners collects what it registers on the document; reveals counts
// boardRevealSchedule calls and nudges shNativeNudge calls when opts.reveal /
// opts.shell provide them, as the full page script does.
const VISIBILITY_START = '  var nativeVisible = false;';
function visibilityHarness(opts = {}) {
  const attrs = new Set(), events = [], listeners = {}, docListeners = {}, frames = [], flushes = [], reveals = [], nudges = [];
  const topDoc = { hidden: opts.topHidden === true };
  const win = { addEventListener(type, fn) { (listeners[type] = listeners[type] || []).push(fn); } };
  win.top = opts.noTop ? win : opts.top || { document: topDoc };
  const context = {
    shHidden: false, hiddenRegions: opts.held ? { board: { hash: '1', html: '' } } : {},
    doc: { hidden: opts.childHidden === true, addEventListener(type, fn) { (docListeners[type] = docListeners[type] || []).push(fn); }, dispatchEvent(e) { events.push(e.type); } },
    root: { hasAttribute: key => attrs.has(key), toggleAttribute(key, value) { if (value) attrs.add(key); else attrs.delete(key); } },
    CustomEvent: class { constructor(type) { this.type = type; } },
    window: win,
    requestAnimationFrame(fn) { frames.push(fn); return frames.length; },
    showRegions() { if (context.pageHidden()) return; flushes.push(false); context.hiddenRegions = {}; }
  };
  if (opts.focus !== undefined) context.doc.hasFocus = () => context.focused;
  context.focused = opts.focus === true;
  if (opts.reveal) context.boardRevealSchedule = freeOnly => reveals.push(freeOnly);
  context.shell = false;
  vm.createContext(context);
  vm.runInContext(section(read(__dirname + '/assets/dashboard.js'), VISIBILITY_START, '  // --- browser storage:') +
    '\n' + section(read(__dirname + '/assets/dashboard-board.js'), '  function pageHidden()', '\n').replace(/\n$/, ''), context);
  // Replaces the section's own shNativeNudge (tested on its own below).
  if (opts.shell) context.shNativeNudge = (reason, lost) => nudges.push([reason, lost]);
  return { context, attrs, events, listeners, docListeners, frames, flushes, reveals, nudges, topDoc, win,
    fire: type => (listeners[type] || []).forEach(fn => fn({})),
    fireDoc: type => (docListeners[type] || []).forEach(fn => fn({})) };
}

test('a stale child document.hidden does not hold a shown frame hidden while the top document is visible', () => {
  const h = visibilityHarness({ childHidden: true, held: true });
  const c = h.context;
  assert.equal(c.docHidden(), false, 'top visible, frame shown: the stale child flag is overridden');
  assert.equal(c.pageHidden(), false);
  assert.equal(h.attrs.has('data-animation-paused'), false, 'initial sync does not pause an active, shown frame');
  c.showRegions();
  assert.deepEqual(h.flushes, [false]);
  assert.deepEqual(c.hiddenRegions, {}, 'held regions flush');
});

test('a hidden top window, a hidden shell frame or a standalone page still suspends', () => {
  let h = visibilityHarness({ childHidden: true, topHidden: true });
  assert.equal(h.context.pageHidden(), true, 'genuinely hidden window');
  assert.equal(h.attrs.has('data-animation-paused'), true);
  h = visibilityHarness({ childHidden: true });
  h.context.shHidden = true;
  assert.equal(h.context.pageHidden(), true, 'frame hidden by the shell');
  h = visibilityHarness({ childHidden: true, noTop: true });
  assert.equal(h.context.pageHidden(), true, 'a standalone page has no top to overrule it');
  h = visibilityHarness({ childHidden: false });
  h.context.shHidden = true; h.context.syncPageVisibility();
  assert.equal(h.attrs.has('data-animation-paused'), true);
  h = visibilityHarness({ childHidden: true });
  Object.defineProperty(h.context.window, 'top', { get() { throw new Error('cross-origin'); } });
  assert.equal(h.context.docHidden(), true, 'an unreadable top leaves the frame state in force');
});

test('focus, pageshow and resize recover a stale pause and flush held regions, cheaply when nothing changed', () => {
  const h = visibilityHarness({ held: true, topHidden: true, childHidden: true });
  const c = h.context;
  assert.equal(h.attrs.has('data-animation-paused'), true);
  h.fire('focus');
  assert.deepEqual(h.flushes, [], 'still hidden: nothing flushes');
  const before = h.events.length;
  h.fire('focus');
  assert.equal(h.events.length, before, 'an unchanged state sends no visibility event');
  h.topDoc.hidden = false;
  h.fire('focus');
  assert.equal(h.attrs.has('data-animation-paused'), false, 'focus clears the stale pause');
  assert.deepEqual(h.flushes, [false], 'and flushes held regions');
  assert.equal(h.events.length, before + 1);
  for (const type of ['pageshow', 'resize']) {
    h.topDoc.hidden = true; c.syncPageVisibility(); c.hiddenRegions = { board: { hash: '2', html: '' } };
    h.flushes.length = 0; h.topDoc.hidden = false;
    h.fire(type);
    if (type === 'resize') { assert.equal(h.frames.length, 1, 'resize coalesces to one frame'); h.fire('resize'); assert.equal(h.frames.length, 1); h.frames.shift()(); }
    assert.equal(h.attrs.has('data-animation-paused'), false, type);
    assert.deepEqual(h.flushes, [false], type + ' flushes held regions');
    assert.deepEqual(c.hiddenRegions, {});
  }
  h.fire('resize'); h.frames.shift()(); h.fire('focus');
  assert.deepEqual(h.flushes, [false], 'with nothing held, nothing flushes');
});

test('native and shell visibility keep pause state and notify each lifecycle transition', () => {
  const attrs = new Set(), events = [], context = {
    shHidden: false, doc: { hidden: false, addEventListener() {}, dispatchEvent(e) { events.push(e.type); } },
    root: { hasAttribute: key => attrs.has(key), toggleAttribute(key, value) { if (value) attrs.add(key); else attrs.delete(key); } },
    CustomEvent: class { constructor(type) { this.type = type; } },
    window: { addEventListener() {} }
  };
  vm.createContext(context);
  vm.runInContext(section(read(__dirname + '/assets/dashboard.js'), VISIBILITY_START, '  // --- browser storage:'), context);
  context.shHidden = true; context.syncPageVisibility(); context.syncPageVisibility();
  assert.equal(attrs.has('data-animation-paused'), true); assert.equal(events.length, 3);
  context.shHidden = false; context.doc.hidden = true; context.syncPageVisibility();
  assert.equal(events.length, 4, 'shell state changes must notify streams even while the native tab stays hidden');
  context.doc.hidden = false; context.syncPageVisibility();
  assert.equal(attrs.has('data-animation-paused'), false); assert.equal(events.length, 5);
});

test('a stale hidden top document counts as visible only while focused or natively reported visible', () => {
  let h = visibilityHarness({ noTop: true, childHidden: true, focus: true });
  assert.equal(h.context.docHidden(), false, 'a focused top document is visible whatever document.hidden says');
  assert.equal(h.attrs.has('data-animation-paused'), false);
  h.context.focused = false;
  assert.equal(h.context.docHidden(), true, 'without focus or a native report a hidden top document stays hidden');
  assert.equal(h.win.wsTopVisible(), false, 'the corrected top state is what framed pages read');
  h = visibilityHarness({ noTop: true, childHidden: true, focus: false });
  h.context.doc.hasFocus = () => { throw new Error('detached'); };
  assert.equal(h.context.docHidden(), true, 'a throwing hasFocus says nothing');
  h = visibilityHarness({ noTop: true, childHidden: false });
  assert.equal(h.context.docHidden(), false);
});

test('a framed page defers to the top document\'s corrected visibility when it exposes one', () => {
  let topVisible = true;
  const h = visibilityHarness({ childHidden: true, top: { document: { hidden: true }, wsTopVisible: () => topVisible } });
  assert.equal(h.context.docHidden(), false, 'top stale-hidden but focused/nudged: the shown frame is visible');
  topVisible = false;
  assert.equal(h.context.docHidden(), true, 'a truly hidden top hides the frame');
  topVisible = true; h.context.shHidden = true;
  assert.equal(h.context.docHidden(), true, 'a frame the shell hides stays hidden');
});

test('the native nudge un-pauses a stale hidden top, reveals the board and reaches the shown frame; a real hide pauses again', () => {
  const h = visibilityHarness({ noTop: true, childHidden: true, focus: false, held: true, reveal: true, shell: true });
  const c = h.context;
  assert.equal(h.attrs.has('data-animation-paused'), true, 'stale hidden top, unfocused: paused');
  assert.equal(h.win.__wsNativeVisibilityNudge('fullscreen-exit'), true);
  assert.equal(h.attrs.has('data-animation-paused'), false, 'the nudge clears the stale pause');
  assert.deepEqual(h.flushes, [false], 'held regions flush');
  assert.equal(h.reveals.length, 1, 'one board reveal is scheduled');
  assert.deepEqual(h.nudges, [['fullscreen-exit', false]], 'the shown frame is nudged once');
  const events = h.events.length;
  h.win.__wsNativeVisibilityNudge('occlusion');
  assert.equal(h.events.length, events, 'a repeated nudge sends no visibility event');
  assert.equal(h.reveals.length, 2, 'but still reschedules the (coalesced) board reveal');
  assert.deepEqual(h.nudges[1], ['occlusion', false]);
  h.win.__wsNativeVisibilityHidden('occlusion');
  assert.equal(h.attrs.has('data-animation-paused'), true, 'the native hidden call drops the override: paused');
  assert.deepEqual(h.nudges[2], ['occlusion', true], 'and is passed to the shown frame');
  h.win.__wsNativeVisibilityNudge('fullscreen-enter');
  assert.equal(h.attrs.has('data-animation-paused'), false);
  h.fireDoc('visibilitychange');
  assert.equal(h.attrs.has('data-animation-paused'), true, 'a fresh visibilitychange while hidden is a real hide: the override goes');
  h.win.__wsNativeVisibilityNudge('occlusion');
  h.fire('pagehide');
  h.fire('focus');
  assert.equal(h.attrs.has('data-animation-paused'), true, 'pagehide drops the override too');
});

test('a framed page never takes the native override for itself', () => {
  const h = visibilityHarness({ childHidden: true, topHidden: true, reveal: true });
  assert.equal(h.win.__wsNativeVisibilityNudge('native'), false, 'the top is hidden: the frame stays hidden');
  assert.equal(h.attrs.has('data-animation-paused'), true);
  assert.equal(h.reveals.length, 0, 'nothing is revealed while hidden');
  h.topDoc.hidden = false;
  assert.equal(h.win.__wsNativeVisibilityNudge('native'), true);
  assert.equal(h.reveals.length, 1);
});

test('resize and fullscreenchange reveal the board while visible; focus reveals only on recovery', () => {
  const h = visibilityHarness({ reveal: true, shell: true });
  h.fire('focus'); h.fire('pageshow');
  assert.equal(h.reveals.length, 0, 'visible and unchanged: focus/pageshow do no board work');
  assert.equal(h.nudges.length, 0);
  h.fire('resize'); h.fireDoc('fullscreenchange'); h.fireDoc('webkitfullscreenchange');
  assert.equal(h.frames.length, 1, 'one coalesced frame');
  h.frames.shift()();
  assert.equal(h.reveals.length, 1, 'one reveal for the whole burst');
  assert.equal(h.nudges.length, 0, 'a resize does not nudge frames');
  h.frames.length = 0; h.reveals.length = 0;
  h.topDoc.hidden = true; h.context.doc.hidden = true; h.context.syncPageVisibility();
  h.fire('resize'); h.frames.shift()();
  assert.equal(h.reveals.length, 0, 'hidden: no reveal');
  h.topDoc.hidden = false; h.fire('focus');
  assert.equal(h.attrs.has('data-animation-paused'), false);
  assert.equal(h.reveals.length, 1, 'recovery reveals the board');
  assert.deepEqual(h.nudges, [['recover', false]], 'and reaches the shown frame');
});

test('shNativeNudge passes the nudge to the shown frame only, before one layout read', () => {
  const lc = read(__dirname + '/assets/dashboard-lifecycle.js');
  const order = [];
  const frameWin = { __wsNativeVisibilityNudge: r => order.push('nudge:' + r), __wsNativeVisibilityHidden: r => order.push('hidden:' + r) };
  const ent = { el: { contentWindow: frameWin, get offsetHeight() { order.push('read'); return 1; },
    contentDocument: { documentElement: { get offsetHeight() { order.push('read-doc'); return 1; } } } } };
  const context = { shell: true, shOn: ent };
  vm.createContext(context);
  vm.runInContext(section(read(__dirname + '/assets/dashboard.js'), '  function shNativeNudge(', '  // --- browser storage:') +
    section(lc, '  function shNudge(ent)', '  function shDrop('), context);
  context.shNativeNudge('fullscreen-exit', false);
  assert.deepEqual(order, ['nudge:fullscreen-exit', 'read', 'read-doc'], 'the frame writes first, then one read pass');
  order.length = 0;
  context.shNativeNudge('occlusion', true);
  assert.deepEqual(order, ['hidden:occlusion'], 'a hidden call does no layout read');
  order.length = 0;
  context.shOn = null; context.shNativeNudge('x', false);
  context.shOn = ent; context.shell = false; context.shNativeNudge('x', false);
  assert.deepEqual(order, [], 'no shown frame, or not the shell: nothing');
  context.shell = true; ent.el.contentWindow = {};
  context.shNativeNudge('x', false);
  assert.deepEqual(order, ['read', 'read-doc'], 'a frame without the hook (older page) still gets the layout read');
});

test('board reveal\'s visibilitychange respects the corrected docHidden', () => {
  const listeners = {}, scheduled = [];
  const context = {
    doc: { hidden: true, addEventListener(type, fn) { listeners[type] = fn; } },
    window: { addEventListener() {} },
    hidden: false, docHidden() { return context.hidden; }
  };
  vm.createContext(context);
  vm.runInContext(section(read(__dirname + '/assets/dashboard-board-reveal.js'), '  function boardRevealInit()', '\n  }\n') + '\n  }\nfunction boardRevealSchedule() { scheduled.push(1); }\nfunction boardRevealNow() {}\nboardRevealInit();', Object.assign(context, { scheduled }));
  listeners.visibilitychange();
  assert.equal(scheduled.length, 1, 'stale doc.hidden, corrected visible: the board reveal runs');
  context.hidden = true; listeners.visibilitychange();
  assert.equal(scheduled.length, 1, 'truly hidden: no reveal');
});

// A fake chat block for the msgacts handler: a batch entry holding two
// owner bubbles, each with a pill (rewind disabled, copy).
function chatBlocks() {
  class El {
    constructor(cls, attrs, parent) { this.cls = cls; this.attrs = Object.assign({}, attrs); this.parentNode = parent || null; this.kids = []; if (parent) parent.kids.push(this); }
    getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
    setAttribute(k, v) { this.attrs[k] = String(v); }
    removeAttribute(k) { delete this.attrs[k]; }
    hasAttribute(k) { return k in this.attrs; }
    matches(sel) { return sel === '[data-entry]' ? this.hasAttribute('data-entry') : sel.split('.').filter(Boolean).every(c => this.cls.includes(c)); }
    closest(sel) { for (let n = this; n; n = n.parentNode) if (n.matches(sel)) return n; return null; }
    all(sel) { const out = []; for (const k of this.kids) { if (k.matches(sel)) out.push(k); out.push(...k.all(sel)); } return out; }
    querySelectorAll(sel) { return this.all(sel); }
    contains(n) { for (; n; n = n.parentNode) if (n === this) return true; return false; }
  }
  const box = new El(['conv', 'centries'], {});
  const batch = new El(['user-batch'], { 'data-entry': 'K1.json' }, box);
  const bubble = () => {
    const msg = new El(['msg', 'user'], {}, batch), bar = new El(['d-acts'], {}, msg);
    return { msg, bar, rewind: new El(['d-act'], { 'data-act': 'rewind', 'aria-disabled': 'true' }, bar), copy: new El(['d-act'], { 'data-act': 'copy' }, bar) };
  };
  return { box, first: bubble(), second: bubble() };
}

function chatActions(answer, extra) {
  const blocks = chatBlocks(), fetched = [], copied = [], toasts = [], timers = [];
  let handler = null;
  const context = Object.assign({
    rid: 'sess', Promise, Array, Blob: class { constructor(parts) { this.text = parts.join(''); } },
    window: { fetch: true }, navigator: {},
    doc: { addEventListener(type, fn) { if (type === 'click') handler = fn; } },
    entriesBox: () => blocks.box,
    fetch(url) { fetched.push(url); return answer instanceof Error ? Promise.reject(answer) : Promise.resolve({ ok: true, json: () => Promise.resolve(answer) }); },
    copyText(s, quiet) { copied.push([s, quiet]); return Promise.resolve(true); },
    toast(s) { toasts.push(s); }, wsT: k => k,
    setTimeout(fn) { timers.push(fn); return timers.length; }, clearTimeout() {}
  }, extra);
  vm.createContext(context);
  vm.runInContext(section(read(__dirname + '/assets/dashboard-conversation.js'), '  var ACT_CONFIRM_MS', '  // --- older history:'), context);
  const click = target => { const e = { target, prevented: false, preventDefault() { this.prevented = true; } }; handler(e); return e; };
  const settle = () => new Promise(r => setImmediate(r));
  return { blocks, fetched, copied, toasts, timers, click, settle };
}

test('chat block copy copies the fetched text of its own bubble, quietly, then confirms', async () => {
  const t = chatActions({ blocks: ['first raw', 'second **raw**'], truncated: false });
  assert.equal(t.click(t.blocks.second.copy).prevented, true);
  await t.settle();
  assert.deepEqual(t.fetched, ['/s/sess/entry?key=K1.json']);
  assert.deepEqual(t.copied, [['second **raw**', true]]);
  assert.equal(t.blocks.second.copy.hasAttribute('data-done'), true);
  assert.equal(t.blocks.second.bar.hasAttribute('data-pinned'), true);
  assert.equal(t.toasts.length, 0);
  t.timers.pop()();
  assert.equal(t.blocks.second.copy.hasAttribute('data-done'), false);
  assert.equal(t.blocks.second.bar.hasAttribute('data-pinned'), false);
});

test('chat block copy writes a pending clipboard item inside the click when the browser takes one', async () => {
  const written = [];
  const t = chatActions({ blocks: ['only'], truncated: false }, {
    window: { fetch: true, ClipboardItem: true, isSecureContext: true },
    ClipboardItem: class { constructor(parts) { this.parts = parts; } },
    navigator: { clipboard: { write(items) { written.push(items[0].parts['text/plain']); return Promise.resolve(); } } }
  });
  t.click(t.blocks.first.copy);
  assert.equal(written.length, 1, 'the write must start inside the click');
  assert.equal((await written[0]).text, 'only');
  await t.settle();
  assert.deepEqual(t.copied, []);
  assert.equal(t.blocks.first.copy.hasAttribute('data-done'), true);
});

test('chat block disabled actions only show their tooltip, and a failed fetch toasts', async () => {
  const t = chatActions(new Error('daemon gone'));
  t.click(t.blocks.first.rewind);
  assert.equal(t.blocks.first.rewind.hasAttribute('data-tipshow'), true);
  assert.deepEqual(t.fetched, []);
  t.click(t.blocks.first.copy);
  await t.settle();
  assert.deepEqual(t.copied, []);
  assert.deepEqual(t.toasts, ['js.entry.copyfail']);
  assert.equal(t.blocks.first.copy.hasAttribute('data-done'), false);
});
