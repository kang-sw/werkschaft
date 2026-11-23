// Offline unit tests for the shipped WsBoardWheel state machine (Preset A).
const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

const anchorSrc = readAssetSource('wsdashboard', 'dashboard-board-anchor.js');
const defaultsMatch = anchorSrc.match(/\n  var WHEEL_DEFAULTS = \{[\s\S]*?\n  \};\n/);
const createMatch = anchorSrc.match(/\n  function createBoardWheel\(opts\) \{[\s\S]*?\n  \}\n/);
assert.ok(defaultsMatch, 'missing WHEEL_DEFAULTS');
assert.ok(createMatch, 'missing createBoardWheel');

function makeWheel(opts) {
  const ctx = vm.createContext({ Date, Math, Object, window: {} });
  vm.runInContext(defaultsMatch[0] + createMatch[0] + '\nwindow.WsBoardWheel = { defaults: WHEEL_DEFAULTS, create: createBoardWheel };', ctx);
  return { api: ctx.window.WsBoardWheel, ctrl: ctx.window.WsBoardWheel.create(opts) };
}

test('Preset A defaults match calibrated parameters', () => {
  const { api } = makeWheel();
  assert.equal(api.defaults.threshold, 52);
  assert.equal(api.defaults.peekPx, 0);
  assert.equal(api.defaults.wallGuardMs, 140);
  assert.equal(api.defaults.chainWindowMs, 360);
  assert.equal(api.defaults.minStepMs, 165);
  assert.equal(api.defaults.animMs, 220);
  assert.equal(api.defaults.idleGapMs, 140);
});

test('macOS single trackpad flick commits once and absorbs entire decaying inertia tail', () => {
  const { ctrl } = makeWheel();
  const flick = [18, 42, 38, 30, 22, 16, 11, 7, 4, 2];
  const actions = [];
  let now = 1000;
  for (const dy of flick) {
    actions.push(ctrl.step({ dy, dx: 0, deltaMode: 0, now, canInnerScroll: false, atFirst: false, atLast: false }));
    now += 16;
  }
  assert.equal(actions[0].action, 'charge');
  assert.equal(actions[1].action, 'commit');
  assert.equal(actions[1].dir, 1);
  for (let i = 2; i < actions.length; i++) {
    assert.equal(actions[i].action, 'absorb', 'event ' + i + ' must be absorbed');
    assert.equal(actions[i].reason, 'coast');
  }
  assert.equal(actions.filter(a => a.action === 'commit').length, 1);
});

test('velocity spike after minStepMs unlocks COAST_ABSORB for rapid chain glide', () => {
  const { ctrl } = makeWheel();
  let now = 1000;
  ctrl.step({ dy: 20, dx: 0, deltaMode: 0, now, canInnerScroll: false, atFirst: false, atLast: false });
  now += 16;
  const c1 = ctrl.step({ dy: 40, dx: 0, deltaMode: 0, now, canInnerScroll: false, atFirst: false, atLast: false });
  assert.equal(c1.action, 'commit');
  // Decay tail down to 6px over 160ms
  for (const dy of [28, 18, 12, 8, 6]) {
    now += 32;
    const r = ctrl.step({ dy, dx: 0, deltaMode: 0, now, canInnerScroll: false, atFirst: false, atLast: false });
    assert.equal(r.action, 'absorb');
  }
  // Now at t=1176 (+160ms since commit at 1016): still < minStepMs (165), spike stays absorbed
  now = 1016 + 150;
  assert.equal(ctrl.step({ dy: 30, dx: 0, deltaMode: 0, now, canInnerScroll: false, atFirst: false, atLast: false }).action, 'absorb');
  // At t=1016+180 (>= minStepMs 165, <= chainWindowMs 360): fresh flick spikes above minAbs and chains!
  now = 1016 + 180;
  const r1 = ctrl.step({ dy: 28, dx: 0, deltaMode: 0, now, canInnerScroll: true, atFirst: false, atLast: false });
  assert.equal(r1.action, 'charge');
  now += 16;
  const r2 = ctrl.step({ dy: 32, dx: 0, deltaMode: 0, now, canInnerScroll: true, atFirst: false, atLast: false });
  assert.equal(r2.action, 'commit', 'fresh spike within chainWindowMs steps deck even on a tall card');
  assert.equal(r2.dir, 1);
});

test('pausing > chainWindowMs enters native inner scroll on newly arrived tall card', () => {
  const { ctrl } = makeWheel();
  ctrl.step({ dy: 60, dx: 0, deltaMode: 0, now: 1000, canInnerScroll: false, atFirst: false, atLast: false });
  // Pause 400ms (> 360ms chainWindowMs)
  const r = ctrl.step({ dy: 24, dx: 0, deltaMode: 0, now: 1400, canInnerScroll: true, atFirst: false, atLast: false });
  assert.equal(r.action, 'inner');
});

test('wall-slam absorption absorbs residual fling when inner scroll hits boundary within wallGuardMs', () => {
  const { ctrl } = makeWheel();
  let now = 2000;
  assert.equal(ctrl.step({ dy: 35, dx: 0, deltaMode: 0, now, canInnerScroll: true, atFirst: false, atLast: false }).action, 'inner');
  now += 16;
  assert.equal(ctrl.step({ dy: 28, dx: 0, deltaMode: 0, now, canInnerScroll: true, atFirst: false, atLast: false }).action, 'inner');
  // Hits bottom boundary 16ms later with decaying inertia
  for (const dy of [22, 16, 10, 5]) {
    now += 16;
    const r = ctrl.step({ dy, dx: 0, deltaMode: 0, now, canInnerScroll: false, atFirst: false, atLast: false });
    assert.equal(r.action, 'absorb');
    assert.equal(r.reason, 'wall');
  }
  // Idle gap >= 140ms clears WALL_ABSORB so next deliberate swipe commits
  now += 160;
  assert.equal(ctrl.step({ dy: 30, dx: 0, deltaMode: 0, now, canInnerScroll: false, atFirst: false, atLast: false }).action, 'charge');
  now += 16;
  assert.equal(ctrl.step({ dy: 30, dx: 0, deltaMode: 0, now, canInnerScroll: false, atFirst: false, atLast: false }).action, 'commit');
});

test('direction reversal immediately unlocks COAST_ABSORB and WALL_ABSORB', () => {
  const { ctrl } = makeWheel();
  ctrl.step({ dy: 60, dx: 0, deltaMode: 0, now: 1000, canInnerScroll: false, atFirst: false, atLast: false });
  assert.equal(ctrl.state(), 'COAST_ABSORB');
  // Reverse direction while still inside minStepMs -> immediate unlock!
  const rev = ctrl.step({ dy: -20, dx: 0, deltaMode: 0, now: 1030, canInnerScroll: true, atFirst: false, atLast: false });
  assert.equal(rev.action, 'inner');
  assert.equal(ctrl.state(), 'IDLE');
});

test('discrete mouse wheel commits immediately on first notch', () => {
  const { ctrl } = makeWheel();
  // Line-mode wheel
  const r1 = ctrl.step({ dy: 3, dx: 0, deltaMode: 1, now: 1000, canInnerScroll: false, atFirst: false, atLast: false });
  assert.equal(r1.action, 'commit');
  assert.equal(r1.dir, 1);
  // Pixel-mode discrete mouse notch (>= 48px after >= 60ms) after minStepMs
  const r2 = ctrl.step({ dy: 100, dx: 0, deltaMode: 0, now: 1200, canInnerScroll: false, atFirst: false, atLast: false });
  assert.equal(r2.action, 'commit');
  assert.equal(r2.dir, 1);
});

test('deck boundaries return edge without committing; horizontal gestures are ignored', () => {
  const { ctrl } = makeWheel();
  assert.equal(ctrl.step({ dy: 4, dx: 20, deltaMode: 0, now: 1000, canInnerScroll: false, atFirst: false, atLast: false }).action, 'ignore');
  // Line-mode horizontal wheel must scale dx by deltaMode alongside dy
  assert.equal(ctrl.step({ dy: 1, dx: 2, deltaMode: 1, now: 1050, canInnerScroll: false, atFirst: false, atLast: false }).action, 'ignore');
  assert.equal(ctrl.step({ dy: 1, dx: 2, deltaMode: 2, now: 1060, canInnerScroll: false, atFirst: false, atLast: false }).action, 'ignore');
  const topEdge = ctrl.step({ dy: -60, dx: 0, deltaMode: 0, now: 1100, canInnerScroll: false, atFirst: true, atLast: false });
  assert.equal(topEdge.action, 'edge');
  assert.equal(topEdge.dir, -1);
  const botEdge = ctrl.step({ dy: 60, dx: 0, deltaMode: 0, now: 1300, canInnerScroll: false, atFirst: false, atLast: true });
  assert.equal(botEdge.action, 'edge');
  assert.equal(botEdge.dir, 1);
});
