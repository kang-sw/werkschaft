// Offline contract for the session view's board anchor (dashboard-session-view.js
// sessionBoardAnchors), saved on the shell's session-switch click path. The
// board keeps no outer pixel anchors: the current card's key (dashboard-board-
// anchor.js) is its saved state, so the session view saves none.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');
const source = readAssetSource('wsdashboard', 'dashboard.js');
function func(name) {
  const m = source.match(new RegExp('\\n  function ' + name + '\\([^\\n]*\\{[^\\n]*\\}\\n|\\n  function ' + name + '\\([\\s\\S]*?\\n  \\}\\n'));
  assert.ok(m, name); return m[0];
}

test('the session view saves no board scroll anchors and reads no layout', () => {
  let reads = 0;
  const c = vm.createContext({ boardBoxes: () => { reads++; return []; }, q: () => { reads++; return []; } });
  vm.runInContext(func('sessionBoardAnchors') + 'this.anchors = sessionBoardAnchors;', c);
  assert.deepEqual(JSON.parse(JSON.stringify(c.anchors())), []);
  assert.equal(reads, 0);
});

test('the board scroll-anchor constants stay retired', () => {
  assert.equal(/BOARD_CV_PARTS/.test(source), false);
});
