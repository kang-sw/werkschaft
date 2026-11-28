'use strict';

// Ticket map geometry, pure: drRoom's right-edge handle offset
// (assets/dashboard-drawer.js) with and without the search handle (the
// ticket map docks in the gallery, so no map handle takes room),
// and View B's layoutHier column solver
// (assets/dashboard-ticketmap-viewb.js, the block between "tm:hier-begin"
// and "tm:hier-end").
// Run: node --test internal/wsdashboard/testdata/ticketmap-*.test.cjs
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const drawer = fs.readFileSync(path.resolve(__dirname, '..', 'assets', 'dashboard-drawer.js'), 'utf8');
const viewb = fs.readFileSync(path.resolve(__dirname, '..', 'assets', 'dashboard-ticketmap-viewb.js'), 'utf8');
function section(text, start, end) {
  const i = text.indexOf(start), j = text.indexOf(end, i + start.length);
  if (i < 0 || j < 0) throw new Error('missing section ' + start);
  return text.slice(i, j);
}

// room runs drRoom with the search handle's measured width, or no search
// handle (0). The git and panels handles retired (the Git page and the
// shell's Holders popup took their content), so the search handle is the
// only one at the right edge.
function room(search) {
  const props = {}, toggles = {};
  const box = width => ({ getBoundingClientRect: () => ({ width }) });
  const ctx = vm.createContext({
    drRoomW: -1,
    shandle: search ? box(search) : null,
    drheader: { classList: { toggle: (k, on) => { toggles[k] = on; } } },
    doc: { documentElement: { style: { setProperty: (k, v) => { props[k] = v; } } } }
  });
  vm.runInContext(section(drawer, '\n  function drRoom() {', '\n  // drFoldShow') + '\ndrRoom();', ctx);
  return { props, room: toggles['dr-room'] };
}

test('drRoom places the search handle at the right edge, with or without it', () => {
  for (const search of [0, 35]) {
    const got = room(search), label = 'search ' + search;
    assert.equal(got.props['--shr'], '0px', label + ' --shr');
    assert.equal(got.props['--drhw'], search + 'px', label + ' --drhw');
    assert.equal(got.props['--ghr'], undefined, label + ' writes no --ghr (the git handle retired)');
    assert.equal(got.props['--thr'], undefined, label + ' writes no --thr');
    assert.equal(got.room, search > 0, label + ' header room');
  }
  assert.doesNotMatch(section(drawer, '\n  function drRoom() {', '\n  // drFoldShow'), /drhandle|drHas/, 'the panels handle retired');
});

const hier = vm.createContext({});
vm.runInContext(section(viewb, '// tm:hier-begin', '// tm:hier-end'), hier);
// cols runs the solver; rows splits each group's n cards into its rows.
function cols(W1, ns) {
  const r = JSON.parse(JSON.stringify(hier.tmHierCols(W1, ns)));
  r.rows = ns.map((n, ix) => {
    const out = [], c = r.cols[ix];
    for (let left = Math.min(n, 99); left > 0; left -= c) out.push(Math.min(c, left));
    return out;
  });
  return r;
}
// width is the tier's width at the solved columns.
const width = r => r.cols.reduce((s, k) => s + k * r.cw + (k - 1) * 16, 0) + 40 * (r.cols.length - 1);

test('layoutHier balances five cards as 3+2, never 4+1', () => {
  const r = cols(1000, [5]);
  assert.equal(r.cw, 232);
  assert.deepEqual(r.rows, [[3, 2]], 'four columns fit 1000px, rebalanced to 3+2');
  assert.deepEqual(cols(1600, [5]).rows, [[5]], 'all five fit one row at 1600px');
  assert.deepEqual(cols(560, [5]).rows, [[2, 2, 1]], 'only two columns fit 560px: the rebalance never adds one');
});

test('layoutHier lays out an epic with 11 children at W1 = 1200 and 800', () => {
  // Collapsed: 8 shown behind the +3 toggle.
  let r = cols(1200, [8]);
  assert.deepEqual(r.rows, [[4, 4]]);
  assert.ok(width(r) <= 1200);
  r = cols(1200, [11]);
  assert.deepEqual(r.rows, [[4, 4, 3]]);
  r = cols(800, [8]);
  assert.deepEqual(r.rows, [[3, 3, 2]]);
  assert.ok(width(r) <= 800);
  r = cols(800, [11]);
  assert.deepEqual(r.rows, [[3, 3, 3, 2]]);
  // Two groups share the tier: the group with the most rows grows first.
  r = cols(1200, [8, 2]);
  assert.equal(r.cw, 232);
  assert.ok(width(r) <= 1200, 'fits: ' + JSON.stringify(r));
  assert.deepEqual(r.rows, [[3, 3, 2], [1, 1]]);
});

test('layoutHier narrows cards to share a tight column and caps n at 99', () => {
  const r = cols(300, [1, 1]);
  assert.equal(r.cw, 130, 'floor((300 - 40) / 2)');
  assert.deepEqual(r.cols, [1, 1]);
  const big = cols(1200, [150]);
  assert.deepEqual(big.cols, [4]);
  assert.equal(big.rows[0].length, 25, '99 cards in 4 columns');
  assert.deepEqual(cols(1200, [150]), cols(1200, [99]));
  assert.deepEqual(cols(1200, [0]).cols, [1], 'an empty group keeps one column');
});
