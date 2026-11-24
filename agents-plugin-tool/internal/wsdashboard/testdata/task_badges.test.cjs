const test = require('node:test');
const assert = require('node:assert/strict');
const { readAssetSource } = require('./asset-source.cjs');
const vm = require('node:vm');

// Exercise the real badge functions without booting the rest of the page.
const source = readAssetSource('wsdashboard', 'dashboard.js');
const badgeStart = source.indexOf('  function taskBadge(');
const functions = source.slice(badgeStart, source.indexOf('  // --- agents island', badgeStart));
function badge() {
  const label = { textContent: '' };
  const attrs = {};
  return { hidden: true, title: '', label, querySelector: () => label,
    getAttribute: (name) => attrs[name], setAttribute: (name, value) => attrs[name] = value };
}

test('background and monitor pills are separate and zero hides each', () => {
  const context = { bgbadge: badge(), monbadge: badge(), bgTitle: '', monTitle: '', liveMode: true,
    wsTn: (word, n) => `${n} ${word === 'js.bgtasks' ? 'background' : n === 1 ? 'monitor' : 'monitors'}` };
  vm.createContext(context);
  vm.runInContext(functions, context);
  const counts = (bg, mon, execTotal = 0, execSelf = 0, execDesc = 0) => ({
    getAttribute: (name) => {
      if (name === 'data-bg') return String(bg);
      if (name === 'data-monitors') return String(mon);
      if (name === 'data-bg-exec') return execTotal ? String(execTotal) : null;
      if (name === 'data-bg-exec-self') return execSelf ? String(execSelf) : null;
      if (name === 'data-bg-exec-desc') return execDesc ? String(execDesc) : null;
      return null;
    }
  });
  context.bgBadge(counts(2, 1));
  assert.equal(context.bgbadge.hidden, false);
  assert.equal(context.bgbadge.label.textContent, '2 background');
  assert.equal(context.monbadge.hidden, false);
  assert.equal(context.monbadge.label.textContent, '1 monitor');
  context.bgBadge(counts(0, 3));
  assert.equal(context.bgbadge.hidden, true);
  assert.equal(context.monbadge.label.textContent, '3 monitors');
  context.bgBadge(null);
  assert.equal(context.monbadge.hidden, true);
  context.liveMode = false;
  context.bgBadge(counts(1, 1));
  assert.equal(context.bgbadge.hidden, true);
  assert.equal(context.monbadge.hidden, true);
});

test('bgBadge combines native data-bg and agent data-bg-exec with split tooltip', () => {
  const classes = new Set();
  const bg = badge();
  bg.classList = {
    toggle: (cls, on) => { if (on) classes.add(cls); else classes.delete(cls); },
    remove: (cls) => classes.delete(cls),
    contains: (cls) => classes.has(cls),
  };
  const context = {
    bgbadge: bg,
    monbadge: badge(),
    bgTitle: 'native bg tip',
    monTitle: '',
    liveMode: true,
    wsT: (k, ...args) => {
      if (k === 'js.bgexec.split') return `Self ${args[0]} · Desc ${args[1]}`;
      if (k === 'js.bgexec.badge.tip') return 'Click to inspect background commands';
      return k;
    },
    wsTn: (word, n) => `${n} ${word === 'js.bgtasks' ? 'background' : 'monitors'}`,
  };
  vm.createContext(context);
  vm.runInContext(functions, context);
  const counts = (bgN, monN, execTotal = 0, execSelf = 0, execDesc = 0) => ({
    getAttribute: (name) => {
      if (name === 'data-bg') return String(bgN);
      if (name === 'data-monitors') return String(monN);
      if (name === 'data-bg-exec') return execTotal ? String(execTotal) : null;
      if (name === 'data-bg-exec-self') return execSelf ? String(execSelf) : null;
      if (name === 'data-bg-exec-desc') return execDesc ? String(execDesc) : null;
      return null;
    }
  });
  context.bgBadge(counts(0, 0, 3, 1, 2));
  assert.equal(bg.hidden, false);
  assert.equal(bg.label.textContent, '3 background');
  assert.equal(classes.has('has-exec'), true);
  assert.equal(bg.title, '3 background (Self 1 · Desc 2)');
  context.bgBadge(counts(1, 0, 2, 2, 0));
  assert.equal(bg.hidden, false);
  assert.equal(bg.label.textContent, '3 background');
  assert.equal(bg.title, '3 background (1 background)');
  context.bgBadge(counts(0, 0, 0, 0, 0));
  assert.equal(bg.hidden, true);
  assert.equal(classes.has('has-exec'), false);
});
