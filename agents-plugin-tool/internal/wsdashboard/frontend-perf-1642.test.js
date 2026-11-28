const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const DASH_ASSETS = path.join(__dirname, 'assets');
const WB_ASSETS = path.join(__dirname, '..', 'wswhiteboard', 'assets');

function readAsset(dir, name) {
  return fs.readFileSync(path.join(dir, name), 'utf8');
}

function extractFunction(src, name) {
  const sig = `function ${name}(`;
  const start = src.indexOf(sig);
  assert.notEqual(start, -1, `missing ${sig}`);
  let depth = 0;
  let started = false;
  for (let i = start; i < src.length; i++) {
    const ch = src[i];
    if (ch === '{') {
      depth++;
      started = true;
    } else if (ch === '}') {
      depth--;
      if (started && depth === 0) return src.slice(start, i + 1);
    }
  }
  throw new Error(`unterminated ${sig}`);
}

function makeClassList(initial = []) {
  const set = new Set(initial);
  return {
    add(...xs) { xs.forEach((x) => set.add(x)); },
    remove(...xs) { xs.forEach((x) => set.delete(x)); },
    toggle(x, force) {
      if (force === undefined) {
        if (set.has(x)) { set.delete(x); return false; }
        set.add(x); return true;
      }
      if (force) set.add(x); else set.delete(x);
      return !!force;
    },
    contains(x) { return set.has(x); },
    get size() { return set.size; }
  };
}

function makeEl(tag, opts = {}) {
  const attrs = new Map(Object.entries(opts.attrs || {}));
  const listeners = new Map();
  const el = {
    nodeType: 1,
    tagName: tag.toUpperCase(),
    id: opts.id || '',
    className: (opts.classes || []).join(' '),
    classList: makeClassList(opts.classes || []),
    hidden: !!opts.hidden,
    disabled: !!opts.disabled,
    isConnected: opts.connected !== false,
    parentElement: null,
    parentNode: null,
    children: [],
    childNodes: [],
    style: {},
    scrollTop: opts.scrollTop || 0,
    scrollHeight: opts.scrollHeight || 0,
    clientHeight: opts.clientHeight || 0,
    clientWidth: opts.clientWidth || 0,
    scrollWidth: opts.scrollWidth || 0,
    clientTop: 0,
    clientLeft: 0,
    wbScrollOwner: !!opts.wbScrollOwner,
    get firstChild() { return this.childNodes[0] || null; },
    get firstElementChild() { return this.children[0] || null; },
    get nextElementSibling() {
      if (!this.parentElement) return null;
      const idx = this.parentElement.children.indexOf(this);
      return idx >= 0 ? this.parentElement.children[idx + 1] || null : null;
    },
    getAttribute(k) { return attrs.has(k) ? attrs.get(k) : null; },
    hasAttribute(k) { return attrs.has(k); },
    setAttribute(k, v) { attrs.set(k, String(v)); },
    removeAttribute(k) { attrs.delete(k); },
    appendChild(child) {
      if (child.parentNode && child.parentNode.removeChild) child.parentNode.removeChild(child);
      child.parentNode = this;
      child.parentElement = this;
      this.childNodes.push(child);
      if (child.nodeType === 1) this.children.push(child);
      return child;
    },
    insertBefore(child, ref) {
      if (child.parentNode && child.parentNode.removeChild) child.parentNode.removeChild(child);
      child.parentNode = this;
      child.parentElement = this;
      const i = ref ? this.childNodes.indexOf(ref) : -1;
      if (i >= 0) this.childNodes.splice(i, 0, child); else this.childNodes.push(child);
      if (child.nodeType === 1) {
        const ei = ref ? this.children.indexOf(ref) : -1;
        if (ei >= 0) this.children.splice(ei, 0, child); else this.children.push(child);
      }
      return child;
    },
    removeChild(child) {
      const i = this.childNodes.indexOf(child);
      if (i >= 0) this.childNodes.splice(i, 1);
      const ei = this.children.indexOf(child);
      if (ei >= 0) this.children.splice(ei, 1);
      child.parentNode = null;
      child.parentElement = null;
      return child;
    },
    remove() {
      if (this.parentNode && this.parentNode.removeChild) this.parentNode.removeChild(this);
    },
    addEventListener(type, fn) {
      if (!listeners.has(type)) listeners.set(type, []);
      listeners.get(type).push(fn);
    },
    dispatchEvent(evt) {
      const list = listeners.get(evt.type) || [];
      list.forEach((fn) => fn(evt));
    },
    getBoundingClientRect() {
      return opts.rect ? opts.rect() : { top: 0, bottom: 100, left: 0, right: 400, width: 400, height: 100 };
    },
    querySelector() { return null; },
    querySelectorAll() { return []; },
    focus() {},
    contains(other) {
      for (let p = other; p; p = p.parentNode) if (p === this) return true;
      return false;
    }
  };
  return el;
}

test('#1 drRoom caches --shr so unchanged widths skip root custom-property writes', () => {
  const src = readAsset(DASH_ASSETS, 'dashboard-drawer.js');
  const fnSrc = extractFunction(src, 'drRoom');
  const writes = [];
  let sw = 28;
  const ctx = {
    drRoomW: -1,
    shandle: { getBoundingClientRect: () => ({ width: sw }) },
    drheader: { classList: makeClassList() },
    doc: {
      documentElement: {
        style: {
          setProperty(k, v) { writes.push([k, v]); }
        }
      }
    }
  };
  vm.runInNewContext(fnSrc, ctx);
  assert.doesNotMatch(fnSrc, /--ghr|ghandle|drhandle/, 'the git and panels handles retired');

  // Only the search handle hangs at the right edge: --shr is 0 and written once.
  ctx.drRoom();
  assert.deepEqual(writes, [['--shr', '0px'], ['--drhw', '28px']]);

  writes.length = 0;
  ctx.drRoom();
  assert.deepEqual(writes, [], 'second call with identical widths must not rewrite --shr or --drhw');

  sw = 35;
  ctx.drRoom();
  assert.deepEqual(writes, [['--drhw', '35px']], 'a wider handle rewrites only --drhw');

  writes.length = 0;
  ctx.drRoom();
  assert.deepEqual(writes, [], 'steady-state after resize must not rewrite any custom properties');
});

test('#2 overview filter avoids duplicate relayout and cards countTopics guards redundant DOM writes and fitToolbar probes', () => {
  const overviewSrc = readAsset(DASH_ASSETS, 'overview.js');
  assert.match(overviewSrc, /var hadPreview = !!preview;\s*teardownPreview\(\{ skipRelayout: true \}\);/);
  assert.match(overviewSrc, /if \(next && canPreview\(\)\) showPreview\(next\);\s*else if \(hadPreview && window\.WsCards && window\.WsCards\.relayout\) window\.WsCards\.relayout\(\);/);
  assert.match(overviewSrc, /if \(!\(opts && opts\.skipRelayout\) && window\.WsCards && window\.WsCards\.relayout\) window\.WsCards\.relayout\(\);/);

  const cardsSrc = readAsset(DASH_ASSETS, 'cards.js');
  const filterFns = [
    extractFunction(cardsSrc, 'toolbarContentSig'),
    extractFunction(cardsSrc, 'applyToolbarFit'),
    extractFunction(cardsSrc, 'fitToolbar'),
    extractFunction(cardsSrc, 'filtered'),
    extractFunction(cardsSrc, 'countTopics')
  ].join('\n');

  let attrWrites = 0;
  let triggerBuilds = 0;
  let fitProbes = 0;
  let toolbarWidth = 800;

  function makeRadio(filter) {
    const el = makeEl('button', { attrs: { 'data-filter': filter } });
    const countSpan = { textContent: '' };
    const origSet = el.setAttribute.bind(el);
    const origRem = el.removeAttribute.bind(el);
    el.setAttribute = (k, v) => { attrWrites++; origSet(k, v); };
    el.removeAttribute = (k) => { attrWrites++; origRem(k); };
    el.querySelector = (sel) => (sel === '.board-state-count' ? countSpan : null);
    el.title = '';
    return el;
  }

  const filterValues = ['all', 'asks', 'researching', 'discussing', 'waiting', 'implementing', 'awaiting-confirmation', 'closed', 'cards', 'tickets'];
  const radios = filterValues.map(makeRadio);
  const trigger = makeEl('button');
  Object.defineProperty(trigger, 'innerHTML', {
    set() { triggerBuilds++; },
    get() { return ''; }
  });
  const inline = makeEl('div');
  const pop = makeEl('div', { hidden: true });
  const root = makeEl('div');
  root.appendChild(inline);
  root.appendChild(trigger);
  const acts = makeEl('div', { rect: () => ({ width: 120 }) });
  const row = makeEl('div');
  row.querySelector = (sel) => (sel === '.board-acts' ? acts : null);
  const face = makeEl('div');
  face.appendChild(row);
  const toolbar = makeEl('div', { rect: () => ({ width: toolbarWidth }) });
  toolbar.appendChild(face);

  const topicA = makeEl('section', { classes: ['topic'], attrs: { 'data-topic': 'a', 'data-state': 'researching' } });
  const topicB = makeEl('section', { classes: ['topic'], attrs: { 'data-topic': 'b', 'data-state': 'implementing' } });
  const board = makeEl('div');
  board.querySelectorAll = (sel) => (sel === 'section.topic[data-topic]' ? [topicA, topicB] : []);
  const galEl = makeEl('div');

  const ctx = {
    doc: { activeElement: null },
    window: {},
    Set,
    Math,
    Array,
    String,
    board,
    galEl,
    cards: [],
    toolbar,
    stateFilter: 'all',
    filterValues,
    toolbarFit: { width: 0, actsWidth: 0, contentSig: '', probing: false, modes: ['', 'compact', 'dropdown', 'tight'] },
    stateUI: {
      root,
      inline,
      trigger,
      pop,
      radios,
      labels: filterValues.map((v) => v.toUpperCase()),
      totals: null,
      close() {}
    },
    licon: () => '',
    filterMark: () => makeEl('span'),
    mk: (t, c, txt) => { const e = makeEl(t); e.textContent = txt || ''; return e; },
    wsT: (k) => k,
    placeStatePop() {},
    toolbarRowFits() { fitProbes++; return true; }
  };

  vm.runInNewContext(filterFns, ctx);

  // Initial pass populates toolbar and probes once.
  ctx.countTopics();
  assert.equal(fitProbes, 1);
  assert.equal(triggerBuilds, 1);

  // Second pass with identical topics & filter: zero attr writes, zero trigger rebuilds, zero probes.
  attrWrites = 0;
  triggerBuilds = 0;
  fitProbes = 0;
  ctx.countTopics();
  assert.equal(attrWrites, 0, 'steady-state countTopics must not rewrite unchanged radio attrs');
  assert.equal(triggerBuilds, 0, 'steady-state countTopics must not rebuild trigger innerHTML');
  assert.equal(fitProbes, 0, 'steady-state countTopics must skip fitToolbar probing');

  // Changing filter in wide mode updates only the two changed radios' checked/tabindex, does NOT re-probe toolbar width.
  attrWrites = 0;
  triggerBuilds = 0;
  fitProbes = 0;
  ctx.stateFilter = 'researching';
  ctx.countTopics();
  assert.equal(attrWrites, 4, 'only old and new active radios should update aria-checked + tabindex');
  assert.equal(triggerBuilds, 1, 'trigger updates for new filter');
  assert.equal(fitProbes, 0, 'wide-mode filter switch with unchanged counts must not re-probe toolbar');

  // Changing width triggers a single re-probe.
  toolbarWidth = 640;
  ctx.fitToolbar(false);
  assert.equal(fitProbes, 1, 'width change must re-probe toolbar');
});

test('#3 rendered() fast-rejects filtered/offwindow nodes without checkVisibility and Overview captures keepFrames before root mutation', () => {
  const svSrc = readAsset(WB_ASSETS, 'stream-view.js');
  const rootEl = makeEl('html');
  const body = makeEl('body');
  rootEl.appendChild(body);
  const win = {
    document: { documentElement: rootEl, body, scrollingElement: rootEl },
    performance: { now: () => 100 },
    getComputedStyle: () => ({ overflowY: 'visible', display: 'block', visibility: 'visible', contentVisibility: 'visible' })
  };
  const ctx = { window: win, document: win.document, Set, Map, WeakMap, Array, Math, Date };
  vm.runInNewContext(svSrc, ctx);
  const kit = ctx.viewKit(win);

  let checkVisCalls = 0;
  const filteredSec = makeEl('section', { classes: ['topic'], attrs: { 'data-topic': 'f', 'data-state-filtered': '' } });
  const filteredChild = makeEl('div');
  filteredChild.checkVisibility = () => { checkVisCalls++; return true; };
  filteredSec.appendChild(filteredChild);
  body.appendChild(filteredSec);
  assert.equal(kit.rendered(filteredChild), false);
  assert.equal(checkVisCalls, 0, 'data-state-filtered ancestor must reject without checkVisibility');

  const offSec = makeEl('section', { classes: ['topic'], attrs: { 'data-topic': 'o', 'data-wb-offwindow': 'hidden' } });
  const offChild = makeEl('div');
  offChild.checkVisibility = () => { checkVisCalls++; return true; };
  offSec.appendChild(offChild);
  body.appendChild(offSec);
  assert.equal(kit.rendered(offChild), false);
  assert.equal(checkVisCalls, 0, 'data-wb-offwindow=hidden must reject without checkVisibility');

  // Ordinary visible node still delegates to checkVisibility (preserves existing contract).
  const normalSec = makeEl('section', { classes: ['topic'], attrs: { 'data-topic': 'n' } });
  const normalChild = makeEl('div');
  normalChild.checkVisibility = () => { checkVisCalls++; return true; };
  normalSec.appendChild(normalChild);
  body.appendChild(normalSec);
  assert.equal(kit.rendered(normalChild), true);
  assert.equal(checkVisCalls, 1, 'ordinary node must still consult checkVisibility');

  // Overview enter() and exit() capture keepFrames BEFORE mutating :root[data-overview].
  const overviewSrc = readAsset(DASH_ASSETS, 'overview.js');
  const enterSrc = extractFunction(overviewSrc, 'enter');
  assert.ok(
    enterSrc.indexOf('o.keepFrames()') < enterSrc.indexOf('root.setAttribute("data-overview", "")'),
    'overview enter() must call keepFrames() before setting data-overview'
  );
  const exitSrc = extractFunction(overviewSrc, 'exit');
  assert.ok(
    exitSrc.indexOf('o.keepFrames()') < exitSrc.indexOf('root.removeAttribute("data-overview")'),
    'overview exit() must call keepFrames() before removing data-overview'
  );

  const boardSrc = readAsset(DASH_ASSETS, 'dashboard-board.js');
  const boardKeepSrc = extractFunction(boardSrc, 'boardKeep');
  assert.match(boardKeepSrc, /root\.hasAttribute\("data-overview"\)/, 'boardKeep skips #r-board in Overview');
});

test('#4 dashboard-board.css keeps Overview tiles under content-visibility: auto and contain: layout style while preserving active overrides', () => {
  const css = readAsset(DASH_ASSETS, 'dashboard-board.css');
  assert.doesNotMatch(
    css,
    /:root\[data-overview\] \.r-board :is\(section\.topic, \.bcard\) \{[^}]*content-visibility:\s*visible\s*!important/,
    'blanket content-visibility: visible !important must be removed from collapsed Overview tiles'
  );
  assert.match(
    css,
    /:root\[data-overview\] \.r-board section\.topic\[data-topic\]:not\(\[data-overview-motion\]\):not\(\[data-overview-live\]\):not\(\[data-max\]\),\r?\n:root\[data-overview\] \.r-board \.bcard\[data-card\]:not\(\[data-overview-motion\]\):not\(\[data-overview-live\]\):not\(\[data-max\]\) \{\s*content-visibility:\s*auto;\s*contain-intrinsic-block-size:\s*auto 76px;\s*\}/
  );
  assert.match(
    css,
    /:root\[data-overview\] \.dgrid:is\(\.live, \.history, \.worktree\) > #bcol \.r-board :is\(section\.topic\[data-topic\], \.bcard\[data-card\]\) \{[^}]*position:\s*relative;[^}]*contain:\s*layout style;/
  );
  assert.match(
    css,
    /:root\[data-overview\] \.dgrid:is\(\.live, \.history, \.worktree\) > #bcol \.r-board :is\(section\.topic\[data-topic\], \.bcard\[data-card\]\):is\(\[data-overview-motion\], \[data-overview-live\], \[data-max\]\) \{\s*contain:\s*none;\s*content-visibility:\s*visible;\s*\}/
  );
});

test('#5 pinned() returns cached stick with zero layout reads and queuePinConvo guards async scroll race', () => {
  const dashSrc = readAsset(DASH_ASSETS, 'dashboard.js');
  const start = dashSrc.indexOf('var convo = $("convo"), stick = true');
  const end = dashSrc.indexOf('// --- layout and tabs', start);
  assert.ok(start > 0 && end > start, 'locate convo pin block in dashboard.js');
  const block = dashSrc.slice(start, end);

  let layoutReads = 0;
  let scrollTop = 400;
  let scrollHeight = 500;
  const clientHeight = 100;
  const convo = makeEl('div', { id: 'convo' });
  Object.defineProperty(convo, 'scrollTop', {
    get() { return scrollTop; },
    set(v) { scrollTop = v; }
  });
  Object.defineProperty(convo, 'scrollHeight', {
    get() { layoutReads++; return scrollHeight; }
  });
  Object.defineProperty(convo, 'clientHeight', {
    get() { layoutReads++; return clientHeight; }
  });

  const rafQueue = [];
  let rafId = 0;
  const ctx = {
    $: (id) => (id === 'convo' ? convo : null),
    paneObserver: null,
    paneVisible: {},
    visible: () => true,
    window: {},
    requestAnimationFrame(cb) { rafQueue.push(cb); return ++rafId; }
  };
  vm.runInNewContext(block, ctx);

  // pinned() reads cached stick without forcing scrollHeight/clientHeight layout reads.
  layoutReads = 0;
  assert.equal(ctx.pinned(), true);
  assert.equal(layoutReads, 0, 'pinned() must not read scrollHeight or clientHeight');

  // Coalesce multiple queuePinConvo calls into a single rAF.
  scrollHeight = 900;
  ctx.queuePinConvo();
  ctx.queuePinConvo();
  assert.equal(rafQueue.length, 1);
  rafQueue.shift()();
  assert.equal(scrollTop, 900);

  // Concurrent swap grows scrollHeight and queues a second pin before browser fires async scroll from first pin.
  scrollHeight = 1400;
  ctx.queuePinConvo();
  convo.dispatchEvent({ type: 'scroll' });
  assert.equal(ctx.pinned(), true, 'stale programmatic scroll event during pending rAF must not unpin stick');
  rafQueue.shift()();
  assert.equal(scrollTop, 1400);

  // User wheel + scroll upward clears programmatic flag and unpins stick.
  convo.dispatchEvent({ type: 'wheel' });
  scrollTop = 200;
  convo.dispatchEvent({ type: 'scroll' });
  assert.equal(ctx.pinned(), false, 'user scroll away from bottom must unpin stick');

  // User scrolls back to bottom: stick re-arms.
  scrollTop = 1300;
  convo.dispatchEvent({ type: 'scroll' });
  assert.equal(ctx.pinned(), true);

  // Callers in dashboard-board.js and dashboard-leads.js defer via queuePinConvo.
  const boardSrc = readAsset(DASH_ASSETS, 'dashboard-board.js');
  assert.match(boardSrc, /if \(pin\) \(typeof queuePinConvo === "function" \? queuePinConvo\(\) : pinConvo\(\)\);/);
  const leadsSrc = readAsset(DASH_ASSETS, 'dashboard-leads.js');
  assert.equal((leadsSrc.match(/queuePinConvo/g) || []).length >= 3, true);
});

test('#6 stream-view scrollerOf and snapshot().scroller fast-path wrappers, #bcol, and WeakMap cache with zero redundant getComputedStyle', () => {
  const svSrc = readAsset(WB_ASSETS, 'stream-view.js');
  const rootEl = makeEl('html', { attrs: { 'data-overview': '' } });
  const body = makeEl('body');
  const bcol = makeEl('div', { id: 'bcol', wbScrollOwner: true });
  const rboard = makeEl('div', { id: 'r-board', classes: ['r-board'] });
  const closedPage = makeEl('div', { classes: ['closed-page'] });
  const topic = makeEl('section', { classes: ['topic'], attrs: { 'data-topic': 't1' } });
  const history = makeEl('div', { classes: ['wb-history'] });
  const leaf = makeEl('div');
  leaf.checkVisibility = () => true;
  rootEl.appendChild(body);
  body.appendChild(bcol);
  bcol.appendChild(rboard);
  rboard.appendChild(closedPage);
  closedPage.appendChild(topic);
  topic.appendChild(history);
  history.appendChild(leaf);

  let styleCalls = 0;
  let now = 100;
  const winListeners = new Map();
  const styleMap = new Map();
  const win = {
    document: { documentElement: rootEl, body, scrollingElement: rootEl },
    performance: { now: () => now },
    addEventListener(type, fn) { winListeners.set(type, fn); },
    getComputedStyle(el) {
      styleCalls++;
      return styleMap.get(el) || { overflowY: 'visible', scrollPaddingTop: '0px', position: 'static', transform: 'none' };
    }
  };
  const ctx = { window: win, document: win.document, Set, Map, WeakMap, Array, Math, Date };
  vm.runInNewContext(svSrc, ctx);
  const kit = ctx.viewKit(win);

  // Overview fast-path: zero getComputedStyle calls in both snapshot().scroller and keep({ capture: false }).
  const snap = kit.snapshot();
  assert.equal(snap.scroller(leaf), bcol);
  assert.equal(styleCalls, 0, 'snapshot().scroller must resolve #bcol through known wrappers with zero getComputedStyle');

  const h1 = kit.keep(leaf, { capture: false });
  assert.equal(h1.scroller, bcol);
  assert.equal(styleCalls, 0, 'scrollerOf must resolve #bcol through known wrappers with zero getComputedStyle');

  // Custom scroller caches in WeakMap across frames (>16ms) and invalidates on resize or reparenting.
  const box = makeEl('div', { scrollHeight: 500, clientHeight: 100 });
  const inner = makeEl('div');
  body.appendChild(box);
  box.appendChild(inner);
  styleMap.set(box, { overflowY: 'auto' });

  styleCalls = 0;
  assert.equal(kit.keep(inner, { capture: false }).scroller, box);
  assert.equal(styleCalls, 1);

  now += 50; // exceed 16ms overflowCache window
  assert.equal(kit.keep(inner, { capture: false }).scroller, box);
  assert.equal(styleCalls, 1, 'WeakMap scrollerCache must survive past 16ms without calling getComputedStyle');

  // Resize invalidates cache; changing style to visible resolves back to docScroller.
  styleMap.set(box, { overflowY: 'visible' });
  winListeners.get('resize')();
  assert.equal(kit.keep(inner, { capture: false }).scroller, rootEl);
  assert.equal(styleCalls, 2);

  // Non-overflowing overflow-y:auto container is NOT speculatively cached so growing it into overflow works.
  const growBox = makeEl('div', { scrollHeight: 80, clientHeight: 100 });
  const growChild = makeEl('div');
  body.appendChild(growBox);
  growBox.appendChild(growChild);
  styleMap.set(growBox, { overflowY: 'auto' });
  assert.equal(kit.keep(growChild, { capture: false }).scroller, rootEl);
  growBox.scrollHeight = 400;
  assert.equal(kit.keep(growChild, { capture: false }).scroller, growBox, 'growing container must become scroller immediately');
});

test('#7 capsules.js sets ARIA and contenteditable on detached editor before insertAdjacentElement and guards connected updates', () => {
  const capSrc = readAsset(DASH_ASSETS, 'capsules.js');
  const events = [];
  let connectedWrites = 0;
  let editorConnected = false;
  let observerCb = null;

  function makeTrackedEl(tag) {
    const el = makeEl(tag);
    const origSet = el.setAttribute.bind(el);
    const origRem = el.removeAttribute.bind(el);
    el.setAttribute = (k, v) => {
      events.push(`set:${k}=${v}:connected=${editorConnected}`);
      if (editorConnected) connectedWrites++;
      origSet(k, v);
    };
    el.removeAttribute = (k) => {
      events.push(`rem:${k}:connected=${editorConnected}`);
      if (editorConnected) connectedWrites++;
      origRem(k);
    };
    return el;
  }

  const field = makeEl('textarea', {
    attrs: { 'aria-label': 'Reply', placeholder: 'Type here' }
  });
  field.value = '';
  field.insertAdjacentElement = (pos, node) => {
    events.push(`insert:${pos}`);
    editorConnected = true;
    node.isConnected = true;
    return node;
  };

  const doc = {
    activeElement: null,
    createElement(tag) {
      return tag === 'div' ? makeTrackedEl(tag) : makeEl(tag);
    },
    createTextNode(t) { return { nodeType: 3, nodeValue: t }; },
    addEventListener() {}
  };
  const ctx = {
    window: {},
    document: doc,
    Object,
    Array,
    RegExp,
    MutationObserver: class {
      constructor(cb) { observerCb = cb; }
      observe() {}
    },
    wsT: (k) => k
  };
  ctx.window.document = doc;
  vm.runInNewContext(capSrc, ctx);

  ctx.window.WsCapsules.attach(field);
  const insertIdx = events.indexOf('insert:afterend');
  assert.ok(insertIdx > 0, 'insertAdjacentElement("afterend", editor) must run');
  const contentEditableIdx = events.findIndex((e) => e.startsWith('set:contenteditable=true:'));
  const ariaLabelIdx = events.findIndex((e) => e.startsWith('set:aria-label=Reply:'));
  assert.ok(contentEditableIdx >= 0 && contentEditableIdx < insertIdx, 'contenteditable must be set BEFORE DOM insertion');
  assert.ok(ariaLabelIdx >= 0 && ariaLabelIdx < insertIdx, 'aria-label must be set BEFORE DOM insertion');
  assert.equal(connectedWrites, 0, 'no attribute writes should occur after DOM insertion during attach');

  // No-op MutationObserver callback must not write any attributes on connected editor.
  observerCb();
  assert.equal(connectedWrites, 0, 'unchanged attrs() callback must not write attributes on connected editor');
});

test('#8 railScrollTo defers into rAF + cancels on user input, and restorePreviewNode captures scrollTop before DOM mutations', () => {
  const railSrc = readAsset(DASH_ASSETS, 'dashboard-rail.js');
  const start = railSrc.indexOf('var railScrollRaf = 0');
  const end = railSrc.indexOf('// railMarks applies', start);
  assert.ok(start > 0 && end > start, 'locate railScrollTo block in dashboard-rail.js');
  const block = railSrc.slice(start, end);

  let scrollReads = 0;
  let scrollWrites = [];
  let top = 0;
  const rlist = makeEl('div', { id: 'rlist' });
  Object.defineProperty(rlist, 'scrollTop', {
    get() { scrollReads++; return top; },
    set(v) { top = v; scrollWrites.push(v); }
  });

  const rafs = new Map();
  let nextId = 1;
  const ctx = {
    $: (id) => (id === 'rlist' ? rlist : null),
    requestAnimationFrame(cb) { const id = nextId++; rafs.set(id, cb); return id; },
    cancelAnimationFrame(id) { rafs.delete(id); }
  };
  vm.runInNewContext(block, ctx);

  ctx.railScrollTo(120);
  ctx.railScrollTo(240);
  assert.equal(scrollReads, 0, 'railScrollTo must not read l.scrollTop synchronously after DOM writes');
  assert.deepEqual(scrollWrites, [], 'railScrollTo must not write l.scrollTop synchronously');
  assert.equal(rafs.size, 1, 'multiple railScrollTo calls coalesce into one rAF');

  // Run rAF: applies latest target once.
  const [cb1] = Array.from(rafs.values());
  rafs.clear();
  cb1();
  assert.deepEqual(scrollWrites, [240]);

  // User wheel cancels pending deferred scroll restoration.
  scrollWrites = [];
  ctx.railScrollTo(50);
  assert.equal(rafs.size, 1);
  rlist.dispatchEvent({ type: 'wheel' });
  assert.equal(rafs.size, 0, 'user wheel must cancel pending railScrollTo rAF');

  // restorePreviewNode captures preScrollTop BEFORE removeAttribute/moveNode/ph.remove and passes preScrollTops.
  const overviewSrc = readAsset(DASH_ASSETS, 'overview.js');
  const restoreSrc = extractFunction(overviewSrc, 'restorePreviewNode');
  assert.ok(
    restoreSrc.indexOf('var preScrollTop = node.scrollTop || 0;') < restoreSrc.indexOf('removeAttribute("data-overview-live")'),
    'restorePreviewNode must read node.scrollTop before any DOM write'
  );
  assert.match(restoreSrc, /preScrollTops/);

  // virtualCaptureForCollapse uses preScrollTop without reading sec.scrollTop.
  const anchorSrc = readAsset(DASH_ASSETS, 'dashboard-board-anchor.js');
  const captureSrc = extractFunction(anchorSrc, 'virtualCaptureForCollapse');
  let secScrollReads = 0;
  const sec = {};
  Object.defineProperty(sec, 'scrollTop', { get() { secScrollReads++; return 99; } });
  const anchorCtx = { window: {}, virtualCaptureOpen: () => [] };
  vm.runInNewContext(captureSrc, anchorCtx);
  const snap = anchorCtx.virtualCaptureForCollapse(sec, false, 314);
  assert.equal(snap.scrollTop, 314);
  assert.equal(secScrollReads, 0, 'virtualCaptureForCollapse must not read sec.scrollTop when preScrollTop is supplied');
});

test('#9a shShow sequences WS handoff via wsHideFrame + hidden-ack, cancels stale handoffs on rapid switch, and falls back on timeout (without #9b)', () => {
  const shellSrc = readAsset(DASH_ASSETS, 'shell.js');
  const shellWin = { location: { origin: 'http://localhost' } };
  vm.runInNewContext(shellSrc, { window: shellWin, URL, Object, Array });
  assert.ok(shellWin.WsShell.message({ wsShell: 'hidden-ack', token: 7 }), 'hidden-ack with numeric token is valid');
  assert.equal(shellWin.WsShell.message({ wsShell: 'hidden-ack' }), null, 'hidden-ack without token is rejected');

  const lcSrc = readAsset(DASH_ASSETS, 'dashboard-lifecycle.js');
  assert.doesNotMatch(lcSrc, /srcdocCache|cacheSrcdoc/i, '#9b about:srcdoc caching must not be implemented');

  const funcs = [
    'var shShowGen = 0, shHandoff = null, SH_HIDE_ACK_MS = 80;',
    extractFunction(lcSrc, 'shClearHandoff'),
    extractFunction(lcSrc, 'shFinishHandoff'),
    extractFunction(lcSrc, 'shNudge'),
    extractFunction(lcSrc, 'shDrop'),
    extractFunction(lcSrc, 'shPrune'),
    extractFunction(lcSrc, 'shShow')
  ].join('\n');

  const sent = [];
  const timers = new Map();
  let nextTimer = 1;
  let hideOrder = [];

  function makeFrame(rid, opts = {}) {
    const el = makeEl('iframe');
    el.inert = false;
    el.contentWindow = {
      focus() {},
      wsCanCacheSession: () => true,
      wsHideFrame: opts.syncHide ? () => { hideOrder.push(`syncHide:${rid}`); return true; } : undefined
    };
    return {
      el,
      f: { rid, path: `/s/${rid}`, hash: '' },
      head: { html: `<p>${rid}</p>` },
      title: rid,
      loaded: true,
      ready: true,
      hideAck: !!opts.hideAck,
      settingsSent: true,
      shownSent: false,
      cached: false
    };
  }

  const ctx = {
    window: { WsShell: { cacheLimit: 5 } },
    doc: { title: '' },
    root: makeEl('html'),
    shFrames: [],
    shOn: null,
    shWant: null,
    shLeaving: null,
    shSettingsEntry: null,
    headHeld: null,
    region: () => null,
    labelEditing: () => false,
    labelEnd() {},
    shCacheable: () => true,
    shSettings() {}, hdNavigated() {},
    shSend(ent, kind, m) {
      sent.push({ rid: ent.f.rid, kind, m });
      if (kind === 'shown' && m.shown === false) hideOrder.push(`postHide:${ent.f.rid}:${m.token}`);
      if (kind === 'shown' && m.shown === true) hideOrder.push(`postShow:${ent.f.rid}`);
    },
    shCompanionTarget() {},
    shRid() {},
    shHead() {},
    shLayout() {},
    shScroll() {},
    setTimeout(fn, ms) { const id = nextTimer++; timers.set(id, { fn, ms }); return id; },
    clearTimeout(id) { timers.delete(id); },
    Math
  };
  vm.runInNewContext(funcs, ctx);

  // 1) Initial show A -> immediate shown:true.
  const A = makeFrame('A', { syncHide: true, hideAck: true });
  const B = makeFrame('B', { syncHide: true, hideAck: true });
  const C = makeFrame('C', { syncHide: true, hideAck: true });
  ctx.shFrames.push(A, B, C);
  ctx.shShow(A);
  assert.deepEqual(sent.map((x) => `${x.rid}:${x.m.shown}`), ['A:true']);

  // 2) Switch A -> B: syncHide(A) runs first, shown:false(token) sent to A, B waits for hidden-ack.
  sent.length = 0;
  hideOrder = [];
  ctx.shShow(B);
  assert.deepEqual(hideOrder, ['syncHide:A', 'postHide:A:2']);
  assert.equal(B.shownSent, false, 'incoming frame B must wait for outgoing hidden-ack before shown:true');
  assert.equal(timers.size, 1, 'bounded fallback timer armed');

  // A posts matching hidden-ack -> B gets shown:true and timer clears.
  ctx.shFinishHandoff(2);
  assert.equal(B.shownSent, true);
  assert.equal(timers.size, 0, 'fallback timer cleared on ack');
  assert.deepEqual(hideOrder, ['syncHide:A', 'postHide:A:2', 'postShow:B']);

  // 3) Rapid double-switch B -> A -> C before B acks: stale token ignored, C gets shown:true on timeout fallback.
  sent.length = 0;
  hideOrder = [];
  ctx.shShow(A); // gen 3 (waiting for B)
  assert.equal(A.shownSent, false);
  ctx.shShow(C); // gen 4 (supersedes gen 3, waiting for A)
  assert.equal(C.shownSent, false);
  ctx.shFinishHandoff(3); // stale ack from B -> must be ignored!
  assert.equal(sent.filter((x) => x.rid === 'A' && x.m.shown === true).length, 0, 'superseded frame A must never receive shown:true');
  assert.equal(C.shownSent, false, 'stale token must not complete gen 4 handoff');

  // Trigger bounded 80ms fallback timer -> C activates cleanly.
  assert.equal(timers.size, 1);
  const [entry] = Array.from(timers.values());
  assert.equal(entry.ms, 80);
  entry.fn();
  assert.equal(C.shownSent, true, 'fallback timeout activates incoming frame C');
  assert.equal(sent.filter((x) => x.rid === 'C' && x.m.shown === true).length, 1);
  assert.equal(sent.filter((x) => x.rid === 'A' && x.m.shown === true).length, 0);
});
