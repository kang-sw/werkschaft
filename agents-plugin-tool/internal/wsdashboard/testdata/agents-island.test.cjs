// Run: node agents-plugin-tool/internal/wsdashboard/testdata/agents-island.test.cjs
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');
const phaseSrc = readAssetSource('wsdashboard', 'dashboard-animation.js');
const hudSrc = readAssetSource('wsdashboard', 'dashboard-hud.js');
const compSrc = readAssetSource('wsdashboard', 'dashboard.js');
const code = phaseSrc + '\n' + hudSrc + '\n' + compSrc.slice(compSrc.indexOf('  // --- agents island'), compSrc.indexOf('  function interruptTurn()'));
assert(code.includes('function hudProject(') && code.includes('function agProject()'));
// The contract runs twice: without a ResizeObserver (every update measures, the
// fallback) and with one (an update reads no layout; the observer measures).
function suite(withRO) {
const reads = { n: 0 }, watched = [];
class FakeResizeObserver {
  constructor(cb) { this.cb = cb; fakeRO.push(this); }
  observe(el) { watched.push(el); }
}
const fakeRO = [];

function node(tag = 'div', cls = '', text = '') {
  const n = {
    tagName: tag.toUpperCase(), className: cls, attrs: {}, children: [], listeners: {},
    style: { setProperty(k, v) { this[k] = v; } }, hidden: false, open: false,
    get offsetWidth() { reads.n++; return 140; }, get offsetHeight() { reads.n++; return 56; }, childNodes: [], parentNode: null, nextElementSibling: null,
    getClientRects() { reads.n++; return [1]; },
    getBoundingClientRect() { reads.n++; return { left: 100, top: 20, right: 240, bottom: 76, width: 140, height: 56 }; },
    setAttribute(k, v) { this.attrs[k] = String(v); },
    removeAttribute(k) { delete this.attrs[k]; },
    getAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k) ? this.attrs[k] : ''; },
    appendChild(x) { return this.insertBefore(x, null); },
    insertBefore(x, ref) {
      if (x.parentNode) x.remove();
      const i = ref ? this.children.indexOf(ref) : this.children.length;
      this.children.splice(i, 0, x); x.parentNode = this;
      this.children.forEach((c, j) => { c.nextElementSibling = this.children[j + 1] || null; });
      return x;
    },
    remove() {
      if (!this.parentNode) return;
      const siblings = this.parentNode.children;
      siblings.splice(siblings.indexOf(this), 1);
      this.parentNode = null;
    },
    addEventListener(k, f) { (this.listeners[k] ||= []).push(f); },
    contains(x) { return x === this || this.children.some(c => c.contains && c.contains(x)); },
    focus() { n.focused = true; }, scrollIntoView() { n.scrolled = true; },
    closest(s) {
      let cur = this;
      while (cur) {
        if (s.startsWith('.') && cur.className.split(/\s+/).includes(s.slice(1))) return cur;
        cur = cur.parentNode;
      }
      return null;
    },
    querySelector(s) {
      if (s === ':scope > .tree-kids') return this.children.find(c => c.className === 'tree-kids') || null;
      if (this.map && this.map[s]) return this.map[s];
      const matchPart = (el, part) => {
        if (part.startsWith('.')) {
          const classes = part.slice(1).split('.');
          const have = el.className ? el.className.split(/\s+/) : [];
          return classes.every(c => have.includes(c));
        }
        const [tag, ...classes] = part.split('.');
        if (tag && el.tagName !== tag.toUpperCase()) return false;
        const have = el.className ? el.className.split(/\s+/) : [];
        return classes.every(c => have.includes(c));
      };
      const parts = s.trim().split(/\s+/);
      const findFirst = (root, idx) => {
        for (const c of root.children) {
          if (matchPart(c, parts[idx])) {
            if (idx === parts.length - 1) return c;
            const nested = findFirst(c, idx + 1);
            if (nested) return nested;
          }
          const deep = findFirst(c, idx);
          if (deep) return deep;
        }
        return null;
      };
      return findFirst(this, 0);
    }
  };
  n.classList = {
    contains(k) { return n.className.split(/\s+/).includes(k); },
    toggle(k, force) {
      const set = new Set(n.className ? n.className.split(/\s+/) : []);
      const on = force === undefined ? !set.has(k) : !!force;
      if (on) set.add(k); else set.delete(k);
      n.className = [...set].join(' ');
    },
    add(k) { n.classList.toggle(k, true); },
    remove(k) { n.classList.toggle(k, false); }
  };
  Object.defineProperties(n, {
    firstElementChild: { get() { return this.children[0] || null; } },
    lastElementChild: { get() { return this.children[this.children.length - 1] || null; } },
    parentElement: { get() { return this.parentNode; } }
  });
  let value = text;
  Object.defineProperty(n, 'textContent', {
    get() { return value; },
    set(v) { value = String(v); n.children = []; }
  });
  return n;
}

const pop = node('div', 'agpop'), pin = node('button', 'agpin'), island = node('div', 'agisland');
const compact = node('div', 'agcompact'), idle = node('div', 'agidle'), badge = node('button', 'sbadge'), close = node('button', 'agclose');
const hudCanvas = node('div', 'hud-canvas'), hudLines = node('svg', 'hud-lines'), hudRoots = node('div', 'hud-roots');
const tooltip = node('div', 'hud-tooltip'), convo = node('div', 'convo');
tooltip.hidden = true; pop.hidden = true;
convo.getBoundingClientRect = () => { reads.n++; return { left: 0, top: 0, right: 640, bottom: 480, width: 640, height: 480 }; };
hudCanvas.appendChild(hudLines); hudCanvas.appendChild(hudRoots);
compact.appendChild(hudCanvas);

const els = { agpop: pop, agpin: pin, agisland: island, agcompact: compact, agidle: idle, sbadge: badge, agclose: close, 'hud-tooltip': tooltip, convo };
let hudPayload = { lead: 'coordinator', completed: 0, peers: 0, roots: [] };
let treeDetails = [], treeAgents = [];
const hudDataEl = node('script', 'ag-hud-data');
const tree = {
  querySelector(s) {
    if (s === '.ag-source .ag-hud-data') { hudDataEl.textContent = JSON.stringify(hudPayload); return hudDataEl; }
    return null;
  }
};
const doc = node('div');
doc.createElement = tag => node(tag);
doc.createElementNS = (_, tag) => node(tag);
doc.createTextNode = txt => node('#text', '', txt);
doc.activeElement = null;

const navigated = [];
const win = node('window');
win.innerWidth = 800; win.innerHeight = 600;
const ctx = vm.createContext({
  $: id => els[id] || null, sbadge: badge, doc, window: win, SVGNS: 'http://www.w3.org/2000/svg',
  region: () => tree,
  q(s, el) {
    if (s === '.tree-node > .node-slot > .hud-node') {
      const found = [];
      (function walk(n) { if (n.classList.contains('hud-node')) found.push(n); n.children.forEach(walk); })(el);
      return found;
    }
    if (s === 'details[data-key]' && el === tree) return treeDetails;
    if (s === 'details[data-key]' && el === pop) return treeDetails;
    if (s === '.tgroup .agent') return treeAgents;
    return [];
  },
  wsT(k, ...args) { return args.length ? `${k}:${args.join(',')}` : k; },
  fmtTime(t) { t.textContent = 'live'; },
  dur(ms) { return Math.round(ms / 1000) + 's'; },
  go(href) { navigated.push(href); },
  syncBadge() { badge.synced = (badge.synced || 0) + 1; },
  save() {}, load() { return '1'; },
  requestAnimationFrame(fn) { fn(); return 1; }, setTimeout(fn) { fn(); }, Date,
  ...(withRO ? { ResizeObserver: FakeResizeObserver } : {})
});
vm.runInContext(code, ctx);

assert.equal(island.hidden, true, 'persisted pin does not reveal an unseen island');
assert.equal(pop.hidden, true);
badge.listeners.click[0]();
assert.equal(island.hidden, false, 'badge explicitly opens unseen island');
doc.listeners.keydown[0]({ key: 'Escape', defaultPrevented: false });
assert.equal(island.hidden, true, 'collapsing unseen island hides it again');

// Alive-idle + holding + permission + failed + linked + overflow + folded HUD forest
hudPayload = {
  lead: 'coord', completed: 5, folded: 3, peers: 5,
  roots: [
    { id: 'a1', label: 'alpha', harness: 'codex', state: 'holding', model: 'gpt-5.4', effort: 'high', elapsedMs: 12000,
      contextTokens: 104000, contextWindow: 272000, contextText: '104k / 272k',
      children: [{ id: 'a2', label: 'beta', harness: 'claude', state: 'permission', replyId: 'r2', linked: true, task: 'bash ls' }] },
    { id: 'a3', label: 'gamma', harness: 'agy', state: 'idle', overflow: 2 },
    { id: 'a4', label: 'delta', harness: 'pi', state: 'failed', contextTokens: 41000, contextText: '41.0k' }
  ]
};
const d1 = node('details'); d1.setAttribute('data-key', 'agent-a1');
const s1 = node('summary', 'ahead'); s1.appendChild(node('span', 'acap', 'waiting on beta'));
const astat1 = node('span', 'astat'); astat1.appendChild(node('span', 'atps', '64 t/s')); s1.appendChild(astat1);
const am1 = node('span', 'am'); am1.appendChild(node('span', 'ok', '\u2713 9')); am1.appendChild(node('span', 'f', '\u2717 1')); s1.appendChild(am1);
d1.appendChild(s1);
const liveOl = node('ol', 'brows live'); const br = node('li', 'brow live');
br.appendChild(node('span', 'tool', 'Read')); br.appendChild(node('span', 'args', 'foo.go')); liveOl.appendChild(br);
const agRow = node('div', 'agent running'); agRow.appendChild(d1); agRow.appendChild(liveOl);
pop.appendChild(agRow);
treeDetails = [d1]; treeAgents = [agRow];

ctx.agSwapped();
assert.equal(badge.synced, 1);
assert.equal(island.hidden, false);
assert.equal(island.attrs['data-mode'], 'compact', 'holding/alive-idle forest opens compact HUD');
assert.equal(island.attrs['data-sizing'], undefined, 'data-sizing attribute is never used');
assert.equal(island.style.height, '56px');
assert.equal(island.style['--ag-cw'], '140px', 'agSize uses measured natural width, not fixed 320px');

const foldedBtn = hudRoots.children.find(c => c.classList && c.classList.contains('hud-folded'));
assert(foldedBtn, 'compact HUD renders .hud-folded badge when folded > 0');
assert.equal(hudRoots.lastElementChild, foldedBtn, '.hud-folded sits at right end of .hud-roots');
assert.equal(foldedBtn.classList.contains('hud-node'), false, '.hud-folded does not take .hud-node');
assert.equal(foldedBtn.textContent, '+3');
assert.equal(foldedBtn.getAttribute('aria-label'), 'js.hud.folded.aria:3');

// Unchanged compact tick is a zero-DOM-churn no-op on width, height, transition, and .hud-folded identity
let styleWrites = 0;
const origW = island.style.width, origH = island.style.height, origT = island.style.transition;
island.style = {
  get width() { return origW; }, set width(_) { styleWrites++; },
  get height() { return origH; }, set height(_) { styleWrites++; },
  get transition() { return origT; }, set transition(_) { styleWrites++; },
  '--ag-cw': island.style['--ag-cw'], '--ag-ch': island.style['--ag-ch'],
  setProperty() { styleWrites++; }, getPropertyValue(k) { return this[k]; }
};
ctx.agSwapped();
assert.equal(styleWrites, 0, 'unchanged compact agSwapped does not mutate island styles');
assert.equal(hudRoots.lastElementChild, foldedBtn, 'unchanged compact tick preserves .hud-folded identity');
assert.equal(island.attrs['data-sizing'], undefined);
island.style = { setProperty(k, v) { this[k] = v; }, width: origW, height: origH, transition: origT, '--ag-cw': origW, '--ag-ch': origH };
if (withRO) {
  // A routine update reads no layout (the sizes come from the observer's
  // cache, the connectors redraw only on a change), and the observer's callback
  // is what re-measures.
  assert(watched.includes(hudCanvas) && watched.includes(convo), 'the observer watches the HUD canvas and the column');
  reads.n = 0;
  ctx.agSwapped(); ctx.agSwapped(); ctx.agSwapped();
  assert.equal(reads.n, 0, 'unchanged compact updates force no layout');
  const ro = fakeRO.find(o => o.cb.name === 'agObserved'), widthBefore = island.style.width;
  hudCanvas.getBoundingClientRect = () => ({ left: 100, top: 20, right: 300, bottom: 76, width: 200, height: 56 });
  ctx.agSwapped();
  assert.equal(island.style.width, widthBefore, 'an update alone does not re-measure');
  ro.cb([{ target: hudCanvas }]);
  assert.equal(island.style['--ag-cw'], '200px', 'the observer callback measures the new canvas');
  assert.equal(island.style.width, '200px');
  hudCanvas.getBoundingClientRect = () => ({ left: 100, top: 20, right: 240, bottom: 76, width: 140, height: 56 });
  ro.cb([{ target: hudCanvas }]);
  assert.equal(island.style.width, '140px');
  reads.n = 0;
  ro.cb([{ target: pop }]);
  assert.equal(reads.n, 0, 'a size the compact mode does not depend on is ignored');
}
const buttons = [];
(function walk(el) { if (el.classList?.contains('hud-node')) buttons.push(el); (el.children || []).forEach(walk); })(hudRoots);
assert.equal(buttons.length, 4);
assert.equal(buttons[1].querySelector('.hud-badge').textContent, '!');
assert.equal(buttons[3].querySelector('.hud-badge').textContent, '\u00d7');
assert.equal(buttons[2].parentNode.querySelector('.hud-overflow').textContent, '+2');

// Own-row task & telemetry fallback from #r-tree, #convo-bounded width, and tooltip display
hudRoots.listeners.mouseover[0]({ target: buttons[0] });
assert.equal(tooltip.hidden, false);
assert.equal(tooltip.style.width, '624px', 'agent tooltip fills #convo width minus 16px padding');
assert.equal(tooltip.style.left, '8px', 'full-width agent tooltip aligns cleanly inside #convo');
assert.equal(tooltip.querySelector('.tt-task').textContent, 'Read foo.go');
assert.equal(tooltip.querySelector('.tt-ok').textContent, '\u2713 9', 'own-row done count read from #r-tree');
assert.equal(tooltip.querySelector('.tt-fail').textContent, '\u2717 1', 'own-row failed count read from #r-tree');
assert.equal(tooltip.querySelector('.tt-tps').textContent, '64 t/s', 'own-row output rate read from #r-tree');
assert.equal(tooltip.querySelector('.tt-hint').textContent, 'js.hud.hint.details');

// Context row: A normal bar, B past the ws soft threshold, C window unknown, none unknown.
const ctxRow = () => tooltip.querySelector('.tt-ctx');
assert.equal(ctxRow().className, 'tt-ctx', 'A: normal level adds no colour class');
assert.equal(ctxRow().querySelector('.pct').textContent, '38%');
assert.equal(ctxRow().querySelector('.fill').style.width, '38.2%');
assert.equal(ctxRow().querySelector('.bar').attrs['aria-valuenow'], '38');
assert.equal(ctxRow().querySelector('.m-soft'), null, 'A: no soft tick without ws thresholds');
assert.equal(ctxRow().querySelector('.tok').textContent, '104k / 272k');
assert.equal(tooltip.children.indexOf(ctxRow()), tooltip.children.indexOf(tooltip.querySelector('.tt-meta')) + 1, 'row sits right under the meta row');
Object.assign(hudPayload.roots[0], { contextTokens: 156000, contextWindow: 200000, contextSoft: 140000, contextLevel: 'warn', contextText: '156k / 200k' });
ctx.agSwapped();
assert.equal(ctxRow().classList.contains('warn'), true, 'B: the hovered card updates in place with the band colour class');
assert.equal(ctxRow().querySelector('.pct').textContent, '78%');
assert.equal(ctxRow().querySelector('.m-soft').style.left, '70.0%');
assert.equal(ctxRow().querySelector('.tok').textContent, '156k / 200k');
Object.assign(hudPayload.roots[0], { contextTokens: 230000, contextLevel: 'crit', contextText: '230k / 200k' });
ctx.agSwapped();
assert.equal(ctxRow().querySelector('.fill').style.width, '100.0%', 'overflow clamps the fill');
assert.equal(ctxRow().querySelector('.bar').attrs['aria-valuenow'], '100');
assert.equal(ctxRow().querySelector('.pct').textContent, '115%');
hudRoots.listeners.mouseout[0]({ target: buttons[0], relatedTarget: null });
hudRoots.listeners.mouseover[0]({ target: buttons[3] });
assert.equal(ctxRow().classList.contains('d-unknown'), true, 'C: window unknown');
assert.equal(ctxRow().querySelector('.bar'), null, 'C: no bar');
assert.equal(ctxRow().querySelector('.tok').textContent, 'js.hud.context:41.0k');
hudRoots.listeners.mouseout[0]({ target: buttons[3], relatedTarget: null });
hudRoots.listeners.mouseover[0]({ target: buttons[2] });
assert.equal(ctxRow(), null, 'unknown tokens: no row');
hudRoots.listeners.mouseout[0]({ target: buttons[2], relatedTarget: null });
delete hudPayload.roots[0].contextTokens; delete hudPayload.roots[0].contextWindow; delete hudPayload.roots[0].contextSoft; delete hudPayload.roots[0].contextLevel; delete hudPayload.roots[0].contextText;
ctx.agSwapped();
hudRoots.listeners.mouseover[0]({ target: buttons[0] });
assert.equal(ctxRow(), null, 'a node that lost its figure drops the row');
hudRoots.listeners.mouseout[0]({ target: buttons[0], relatedTarget: null });

// Compaction line: D compacting now (chip, then count and origin), E count only,
// F none; absent fields reset on an in-place update, and an unknown origin shows
// the count alone.
const cmpRow = () => tooltip.querySelector('.tt-cmp');
Object.assign(hudPayload.roots[0], { contextTokens: 156000, contextWindow: 200000, contextSoft: 140000, contextLevel: 'warn', contextText: '156k / 200k' });
ctx.agSwapped();
hudRoots.listeners.mouseover[0]({ target: buttons[0] });
assert.equal(cmpRow(), null, 'F: never compacted, no line');
Object.assign(hudPayload.roots[0], { compacting: true, compactions: 2, compactOrigin: 'auto' });
ctx.agSwapped();
assert.equal(cmpRow().querySelector('.d-now').textContent, 'js.hud.compacting', 'D: chip while compacting');
assert.equal(cmpRow().querySelector('.cnt').textContent, 'js.hud.compactions:2 \u00b7 js.hud.compactorigin.auto');
assert.equal(cmpRow().parentNode, ctxRow(), 'compaction folds inline inside .tt-ctx when context bar is shown');
assert.equal(cmpRow().classList.contains('tt-cmp-inline'), true);
Object.assign(hudPayload.roots[0], { compacting: false, compactions: 3, compactOrigin: 'manual' });
ctx.agSwapped();
assert.equal(cmpRow().querySelector('.d-now'), null, 'E: no chip when not compacting');
assert.equal(cmpRow().querySelector('.cnt').textContent, 'js.hud.compactions:3 \u00b7 js.hud.compactorigin.manual');
Object.assign(hudPayload.roots[0], { compactOrigin: 'native' });
ctx.agSwapped();
assert.equal(cmpRow().querySelector('.cnt').textContent, 'js.hud.compactions:3 \u00b7 js.hud.compactorigin.native');
Object.assign(hudPayload.roots[0], { compactOrigin: '' });
ctx.agSwapped();
assert.equal(cmpRow().querySelector('.cnt').textContent, 'js.hud.compactions:3', 'unknown origin: count alone');
Object.assign(hudPayload.roots[0], { compacting: true, compactions: 0, compactOrigin: '' });
ctx.agSwapped();
assert.equal(cmpRow().querySelector('.d-now').textContent, 'js.hud.compacting', 'compacting with no completed one: chip alone');
assert.equal(cmpRow().querySelector('.cnt'), null);
delete hudPayload.roots[0].compacting; delete hudPayload.roots[0].compactions; delete hudPayload.roots[0].compactOrigin;
ctx.agSwapped();
assert.equal(cmpRow(), null, 'absent fields drop the line in place');

// Enriched branch & dir metadata on linked node
Object.assign(hudPayload.roots[0].children[0], { branch: 'impl/develop/wide-tip', dir: 'wt-wide', done: 4, outputTps: 52 });
ctx.agSwapped();
hudRoots.listeners.mouseover[0]({ target: buttons[1] });
assert.equal(tooltip.querySelector('.tt-br').textContent, 'impl/develop/wide-tip', 'branch chip renders in .tt-meta');
assert.equal(tooltip.querySelector('.tt-ok').textContent, '\u2713 4');
assert.equal(tooltip.querySelector('.tt-tps').textContent, '52 t/s');
assert.equal(tooltip.querySelector('.tt-hint-main').textContent, 'js.hud.hint.link:r2');
assert.equal(tooltip.querySelector('.tt-hint-meta').textContent, 'wt-wide', 'worktree dir renders right-aligned in .tt-hint');
hudRoots.listeners.mouseout[0]({ target: buttons[1], relatedTarget: null });

// .hud-folded hover shows tooltip, updates in place when count changes, and click opens #agpop
hudRoots.listeners.mouseover[0]({ target: foldedBtn });
assert.equal(tooltip.hidden, false);
assert.equal(tooltip.style.width, '', 'folded tooltip stays compact instead of stretching across #convo');
assert.equal(tooltip.querySelector('.tt-title').textContent, 'js.hud.folded.title:3');
assert.equal(tooltip.querySelector('.tt-pill').textContent, '+3');
assert.equal(tooltip.querySelector('.tt-hint').textContent, 'js.hud.hint.details');
hudPayload.folded = 5;
ctx.agSwapped();
assert.equal(hudRoots.lastElementChild, foldedBtn, 'count update reuses .hud-folded button');
assert.equal(foldedBtn.textContent, '+5');
assert.equal(tooltip.hidden, false);
assert.equal(tooltip.querySelector('.tt-title').textContent, 'js.hud.folded.title:5');
assert.equal(tooltip.querySelector('.tt-pill').textContent, '+5');
hudRoots.listeners.mouseout[0]({ target: foldedBtn, relatedTarget: null });
assert.equal(tooltip.hidden, true);
hudRoots.listeners.mouseover[0]({ target: foldedBtn });
assert.equal(tooltip.hidden, false);
hudRoots.listeners.click[0]({ target: foldedBtn });
assert.equal(island.attrs['data-mode'], 'full');
assert.equal(tooltip.hidden, true, 'clicking .hud-folded hides tooltip and opens #agpop');
doc.listeners.keydown[0]({ key: 'Escape', defaultPrevented: false });
assert.equal(island.attrs['data-mode'], 'compact');

// Clearing folded removes .hud-folded
hudPayload.folded = 0;
ctx.agSwapped();
assert.equal(hudRoots.children.some(c => c.classList && c.classList.contains('hud-folded')), false);
hudPayload.folded = 2;
ctx.agSwapped();
assert.equal(hudRoots.lastElementChild.classList.contains('hud-folded'), true);

// Background click opens the full popover.
compact.listeners.click[0]({ target: compact });
assert.equal(island.attrs['data-mode'], 'full');
assert.equal(compact.attrs['aria-expanded'], 'true');
assert.equal(tooltip.hidden, true, 'opening full view hides compact tooltip');
doc.listeners.keydown[0]({ key: 'Escape', defaultPrevented: false });
assert.equal(island.attrs['data-mode'], 'compact');

// Unlinked node click opens #agpop and focuses <details data-key="agent-a1">
hudRoots.listeners.click[0]({ target: buttons[0] });
assert.equal(island.attrs['data-mode'], 'full');
assert.equal(d1.open, true);
assert.equal(s1.focused, true);
doc.listeners.keydown[0]({ key: 'Escape', defaultPrevented: false });

// Linked node click navigates to /s/<replyId>
hudRoots.listeners.click[0]({ target: buttons[1] });
assert.deepEqual(navigated, ['/s/r2']);

// A tree tick keeps keyed buttons, rings and emblems; only the changed state
// changes their animation phase and badge.
const oldRing = buttons[0].querySelector('.hud-ring');
const oldEmblem = buttons[0].querySelector('.hud-emblem');
const oldDelay = oldRing.style.animationDelay;
hudPayload.roots[0].state = 'tool';
hudPayload.roots[0].children[0].state = 'failed';
hudPayload.roots[1].overflow = 0;
ctx.agSwapped();
assert.equal(hudRoots.children[0].firstElementChild.firstElementChild, buttons[0]);
assert.equal(buttons[0].querySelector('.hud-ring'), oldRing);
assert.equal(buttons[0].querySelector('.hud-emblem'), oldEmblem);
assert.equal(buttons[0].getAttribute('data-state'), 'tool');
assert.notEqual(oldRing.style.animationDelay, oldDelay);
assert.equal(buttons[1].querySelector('.hud-badge').className, 'hud-badge fail');
assert.equal(buttons[2].parentNode.querySelector('.hud-overflow'), null);
const toolDelay = oldRing.style.animationDelay;
const toolStamp = oldRing.wsAnimationStamp;
ctx.agSwapped();
assert.equal(oldRing.style.animationDelay, toolDelay, 'running ring is not restamped on an unchanged tick');
hudPayload.roots[0].state = 'thinking';
ctx.agSwapped();
assert.equal(buttons[0].getAttribute('data-state'), 'thinking');
assert.equal(oldRing.wsAnimationStamp, toolStamp, 'tool -> thinking keeps the shared spin stamp');
assert.equal(oldRing.style.animationDelay, toolDelay, 'tool -> thinking preserves animationDelay without phase jump');
hudPayload.roots[0].state = 'tool';
ctx.agSwapped();
assert.equal(oldRing.style.animationDelay, toolDelay, 'thinking -> tool preserves animationDelay without phase jump');
hudPayload.roots[0].state = 'holding';
ctx.agSwapped();
// Compare the stamp, not the delay: the delay is derived from the live clock
// and can coincide across the two periods by chance.
assert.notEqual(oldRing.wsAnimationStamp, toolStamp, 'entering holding from spinning restamps');
hudPayload.roots[0].state = 'idle';
ctx.agSwapped();
assert.equal(oldRing.style.animationDelay, '', 'leaving an animated state clears its delay');

// Reordering an existing animated wrapper detaches and remounts it in Chromium.
// Its delay must advance to the current clock; the sibling shifted by the
// insertion remains mounted and keeps its original delay.
doc.timeline = { currentTime: 500 };
hudPayload.roots[0].state = 'tool';
hudPayload.roots[2].state = 'thinking';
ctx.agSwapped();
const movedRing = buttons[3].querySelector('.hud-ring');
const movedDelay = movedRing.style.animationDelay;
const stableDelay = oldRing.style.animationDelay;
doc.timeline.currentTime = 700;
hudPayload.roots = [hudPayload.roots[2], hudPayload.roots[0], hudPayload.roots[1]];
ctx.agSwapped();
assert.equal(hudRoots.children[0].firstElementChild.firstElementChild, buttons[3]);
assert.equal(buttons[3].querySelector('.hud-ring'), movedRing);
assert.notEqual(movedRing.style.animationDelay, movedDelay, 'moved ring is restamped');
assert.equal(movedRing.style.animationDelay,
  (-((700 + ctx.animationPhaseOffset('a4', 1000)) % 1000)).toFixed(1) + 'ms');
assert.equal(oldRing.style.animationDelay, stableDelay, 'unmoved sibling is not restamped');

// Native rows: a dashed native node with its lifecycle in the tooltip; a
// terminal native row without an end reads observation ended, completion
// unknown; the observation-ended count shows apart from completed.
hudPayload = { lead: 'coord', completed: 1, observationEnded: 1, peers: 0, roots: [
  { id: 'agy-native:call_1', label: 'Kiwi Responder', harness: 'agy', state: 'thinking', native: true, startedAt: 1000 },
  { id: 'agy-native:call_2', label: 'research', harness: 'agy', state: 'failed', native: true, startedAt: 900, endUnknown: true },
  { id: 'codex-native:call_3', label: 'ok_reply', harness: 'codex', state: 'failed', native: true, startedAt: 800, endedAt: 1800 }
] };
ctx.agSwapped();
const nativeButtons = [];
(function walk(el) { if (el.classList?.contains('hud-node')) nativeButtons.push(el); (el.children || []).forEach(walk); })(hudRoots);
assert.equal(nativeButtons[0].getAttribute('data-native'), '1');
assert.equal(nativeButtons[0].getAttribute('data-linked'), '');
hudRoots.listeners.mouseover[0]({ target: nativeButtons[0] });
assert.equal(tooltip.querySelector('.tt-native').textContent, 'js.hud.native');
assert(tooltip.querySelector('.tt-life'), 'running native tooltip shows its lifecycle');
assert.equal(tooltip.querySelector('.tt-unknown'), null, 'a running native row is not end-unknown');
assert.equal(tooltip.querySelector('.tt-hint').textContent, 'js.hud.hint.details', 'a native row never links');
hudRoots.listeners.mouseover[0]({ target: nativeButtons[1] });
assert.equal(tooltip.querySelector('.tt-unknown').textContent, 'js.hud.unknownend');
hudRoots.listeners.mouseover[0]({ target: nativeButtons[2] });
assert.equal(tooltip.querySelector('.tt-unknown'), null, 'a native row with an end shows it');
hudRoots.listeners.mouseout[0]({ target: nativeButtons[2], relatedTarget: null });
hudRoots.listeners.click[0]({ target: nativeButtons[0] });
assert.deepEqual(navigated, ['/s/r2'], 'a native node click opens details, never a transcript');
doc.listeners.keydown[0]({ key: 'Escape', defaultPrevented: false });
assert.equal(hudCanvas.querySelector('.hud-obs').textContent, 'js.hud.obsended:1');
hudPayload = { lead: 'coord', completed: 4, observationEnded: 2, peers: 2, roots: [] };
ctx.agSwapped();
assert.equal(island.attrs['data-mode'], 'idle');
assert.equal(idle.querySelector('.hud-obs').textContent, 'js.hud.obsended:2');
assert.equal(idle.children.filter(c => c.className === 'hud-obs').length, 1, 'the count is updated in place');
assert.equal(hudCanvas.querySelector('.hud-obs').textContent, 'js.hud.obsended:2');

// Re-opening full after compact must re-measure full dimensions instead of
// reusing compact's cached box (regression for 04af032ab leaving agDone="full|").
const origPopDesc = Object.getOwnPropertyDescriptor(pop, 'offsetHeight');
Object.defineProperty(pop, 'offsetHeight', { configurable: true, get() { reads.n++; return 320; } });
try {
  hudPayload = { lead: 'coord', completed: 0, peers: 0, roots: [
    { id: 'a-reopen', label: 'worker', harness: 'claude', state: 'thinking', startedAt: 1000 }
  ] };
  ctx.agSwapped();
  assert.equal(island.attrs['data-mode'], 'compact');
  badge.listeners.click[0]();
  assert.equal(island.attrs['data-mode'], 'full');
  assert.equal(island.style.height, '320px');
  close.listeners.click[0]();
  assert.equal(island.attrs['data-mode'], 'compact');
  assert.equal(island.style.height, '56px');
  badge.listeners.click[0]();
  assert.equal(island.attrs['data-mode'], 'full');
  assert.equal(island.style.height, '320px', 're-opening full after compact restores full height');
  close.listeners.click[0]();
} finally {
  if (origPopDesc) Object.defineProperty(pop, 'offsetHeight', origPopDesc);
}

// Empty visible HUD forest falls back to idle pill even when folded > 0
hudPayload = { lead: 'coord', completed: 4, folded: 4, peers: 2, roots: [] };
ctx.agSwapped();
assert.equal(island.attrs['data-mode'], 'idle');
assert.equal(idle.querySelector('.agidle-label'), null);
assert.equal(idle.querySelector('.hud-obs'), null, 'no observation-ended count without such rows');
assert.equal(hudRoots.children.some(c => c.classList && c.classList.contains('hud-folded')), false);

// Preserve drawerWait and tlPerms exclusions of .ag-source
const drawerSrc = readAssetSource('wsdashboard', 'dashboard-workpanel.js');
const drawerWait = drawerSrc.match(/\n  function drawerWait\(\) \{[\s\S]*?\n  \}\n/);
assert(drawerWait, 'dashboard-workpanel.js holds drawerWait');
function el(inSource) { return { closest(s) { return s === '.ag-source' && inSource ? {} : null; } }; }
let found = [], selector = '';
const handle = { classList: { toggle(k, v) { handle[k] = v; } } };
const dctx = vm.createContext({ drhandle: handle, drfold: null, drTitle() {}, doc: { querySelector() { return null; } }, wsT: k => k, q(sel) { selector = sel; return found; } });
vm.runInContext(drawerWait[0], dctx);
found = [el(true)]; dctx.drawerWait(); assert.equal(handle.wait, false);
found = [el(true), el(false)]; dctx.drawerWait(); assert.equal(handle.wait, true);
for (const src of ['#r-convlive .permwait', '#r-tree .phase.permission', '#r-tree .brow.perm']) assert(selector.includes(src));

const streamSrc = readAssetSource('wsdashboard', 'dashboard-stream.js');
const tlPerms = streamSrc.match(/\n  function tlPerms\(\) \{[\s\S]*?\n  \}\n/);
assert(tlPerms, 'dashboard-stream.js holds tlPerms');
function pill(agent, inSource) { return { closest(s) { return s === '.ag-source' ? (inSource ? {} : null) : agent; } }; }
const tctx = vm.createContext({ tlRoot: name => name === 'tree' ? { querySelector: () => ({}) } : null, q: () => [pill({}, false), pill(null, true)] });
vm.runInContext(tlPerms[0], tctx);
assert.equal(tctx.tlPerms(), 1);
}
suite(false);
suite(true);
console.log('agents island contract ok');
