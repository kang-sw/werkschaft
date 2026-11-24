// Pure Node: counts layout-forcing reads in the stream message path
// (dashboard-board.js boardKeep over wswhiteboard stream-view.js probes).
const { test } = require('node:test');
const fs = require('node:fs');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const path = require('node:path');

const viewSource = fs.readFileSync(path.join(__dirname, '../wswhiteboard/assets/stream-view.js'), 'utf8');
const boardSource = fs.readFileSync(path.join(__dirname, 'assets/dashboard-board.js'), 'utf8');
const keepStart = boardSource.indexOf('  function boardKeep(nodes) {');
const keepEnd = boardSource.indexOf('\n  }', keepStart) + '\n  }'.length;
assert.ok(keepStart >= 0 && keepEnd > keepStart, 'boardKeep source located');
const keepSource = boardSource.slice(keepStart, keepEnd);

// A fake layout engine: a DOM write dirties it, a geometry/style read while
// dirty is one forced layout (a flush). probes counts every such read.
function fixture(scrollerCount, nodesPerScroller, warm) {
  const stat = { dirty: false, flushes: 0, probes: 0 };
  function read() { stat.probes++; if (stat.dirty) { stat.flushes++; stat.dirty = false; } }
  function write() { stat.dirty = true; }
  const body = {}, root = { style: {} };
  const docScroller = { scrollTop: 0, clientHeight: 100, scrollHeight: 100, addEventListener() {}, style: {} };
  const doc = { body, documentElement: root, scrollingElement: docScroller, querySelectorAll() { return []; } };
  const styles = new Map();
  function el(parent, name) {
    const style = {};
    Object.defineProperty(style, 'overflowAnchor', { set() { write(); }, get() { return ''; } });
    const n = { name, parentElement: parent, isConnected: true, hidden: false, tagName: 'DIV', style, scrollTop: 0, clientTop: 0, clientLeft: 0, clientWidth: 100,
      addEventListener() {}, classList: { contains() { return false; } }, children: [], querySelectorAll() { return []; },
      matches() { return false; }, closest() { return null; }, getAttribute() { return null; } };
    Object.defineProperty(n, 'scrollHeight', { get() { read(); return 1000; } });
    Object.defineProperty(n, 'clientHeight', { get() { read(); return 100; } });
    n.getBoundingClientRect = function () { read(); return { top: 0, bottom: 100, left: 0, right: 100, height: 100 }; };
    n.checkVisibility = function () { read(); return true; };
    styles.set(n, { overflowY: 'visible', scrollPaddingTop: '0px' });
    return n;
  }
  const scrollers = [], nodes = [];
  for (let s = 0; s < scrollerCount; s++) {
    const sc = el(body, 'col' + s); styles.get(sc).overflowY = 'auto'; scrollers.push(sc);
    for (let i = 0; i < nodesPerScroller; i++) {
      const topic = el(sc, 't' + s + '-' + i);
      nodes.push(el(topic, 'u' + s + '-' + i));
    }
  }
  const window = { document: doc, innerHeight: 800, innerWidth: 1000, requestAnimationFrame() {}, addEventListener() {},
    getComputedStyle(n) { read(); return styles.get(n) || { overflowY: 'visible', scrollPaddingTop: '0px', position: 'static', transform: 'none' }; } };
  const context = vm.createContext({ window });
  vm.runInContext(viewSource + '\nwindow.kit = viewKit(window);', context);
  const kit = window.kit;
  window.wbHistory = { keep: kit.keep, snapshot: kit.snapshot, rendered: kit.rendered, own: kit.own };
  if (warm) { scrollers.forEach(function (s) { kit.keep(s.children && nodes.find(n => n.parentElement.parentElement === s), {}).end(); }); }
  const boardKeep = vm.runInContext('(function(){' + keepSource + '\nreturn boardKeep;})()', Object.assign(context, { boardBoxes() { return []; }, anchorCurrent() { return null; }, region() { return null; } }));
  return { stat, nodes, scrollers, boardKeep, write, window };
}

function measure(scrollerCount, nodesPerScroller, warm) {
  const f = fixture(scrollerCount, nodesPerScroller, warm);
  f.stat.flushes = 0; f.stat.probes = 0; f.stat.dirty = false;
  f.write(); // the swap's own DOM write that precedes the capture
  const kept = f.boardKeep(f.nodes);
  assert.ok(kept, 'boardKeep returns a capture');
  return { flushes: f.stat.flushes, probes: f.stat.probes };
}

test('boardKeep message capture: layout-forcing reads after a DOM write', () => {
  const cold = measure(6, 8, false), warm = measure(6, 8, true);
  console.log('boardKeep cold', JSON.stringify(cold), 'warm', JSON.stringify(warm));
  // Pre-change counts (unmodified handler, same fixture): cold 7 flushes /
  // 156 probes, warm 1 flush / 156 probes. Owners are now created together
  // before any capture read, and each node's visibility and each scroller are
  // probed once per pass instead of per call.
  assert.ok(cold.flushes < 7 && cold.flushes <= 2, 'cold flushes ' + cold.flushes + ' (was 7)');
  assert.equal(warm.flushes, 1, 'warm flushes: only the swap write forces layout');
  assert.ok(cold.probes <= 132, 'cold probes ' + cold.probes + ' (was 156)');
  assert.ok(warm.probes <= 132, 'warm probes ' + warm.probes + ' (was 156)');
});

test('boardKeep keeps parity: one capture per scroller, hidden nodes skipped, owners first, fallback without own', () => {
  const f = fixture(3, 4, false);
  const hidden = f.nodes[1]; hidden.checkVisibility = () => false;
  const order = [];
  const w = f.window.wbHistory, keep = w.keep, own = w.own;
  w.own = function (list) { order.push('own:' + list.length); return own(list); };
  w.keep = function (n, opt) { if (!(opt && opt.capture === false)) order.push('keep'); return keep(n, opt); };
  const kept = f.boardKeep(f.nodes);
  assert.deepEqual(order, ['own:3', 'keep', 'keep', 'keep'], 'owners are created before the first capture, one capture per scroller');
  assert.equal(kept.scroller(f.nodes[0]), f.scrollers[0]);
  assert.equal(kept.scroller(f.nodes[5]), f.scrollers[1]);
  assert.equal(kept.scroller(hidden), undefined, 'a non-rendered node is not resolved');
  const g = fixture(2, 2, false); delete g.window.wbHistory.own;
  const calls = []; const gkeep = g.window.wbHistory.keep;
  g.window.wbHistory.keep = function (n, opt) { calls.push(!!(opt && opt.scroller)); return gkeep(n, opt); };
  assert.ok(g.boardKeep(g.nodes), 'works without own');
  assert.equal(calls.filter(Boolean).length, 2);
});
