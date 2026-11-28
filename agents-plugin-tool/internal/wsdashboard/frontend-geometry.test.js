const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

function asset(name) {
  return fs.readFileSync(path.join(__dirname, 'assets', name), 'utf8');
}

function section(source, start, end) {
  const a = source.indexOf(start), b = source.indexOf(end, a);
  assert.ok(a >= 0 && b > a, 'asset section exists');
  return source.slice(a, b);
}

test('the search-only drawer runs no card grid fill', () => {
  // The drawer's cards retired (holders moved to the shell bottom bar), and
  // with them the rAF-coalesced grid fill and its span writes.
  const source = asset('dashboard-drawer.js');
  assert.doesNotMatch(source, /drFill|drFilled|gridRow|gridColumn/);
});

test('lifecycle post coalesces on rAF and reads layout before lifting the frame', () => {
  const source = section(asset('dashboard-lifecycle.js'), '    function post() {', '    if (typeof compact === "undefined" || !compact) {');
  const frames = [], events = [], messages = [];
  const top = { offsetHeight: 42 }, head = { innerHTML: 'header' }, qs = {};
  const attrs = new Set(['data-pincol']);
  const root = {
    get clientWidth() { events.push('read:width'); return 990; },
    hasAttribute(name) { events.push('read:' + name); return attrs.has(name); },
    setAttribute(name) { events.push('write:lifted'); attrs.add(name); },
    removeAttribute(name) { events.push('write:lifted'); attrs.delete(name); }
  };
  const context = {
    queued: null, head: null, headDirty: true, lastHandlesKey: '', root, shHidden: false, compact: false,
    doc: { querySelector() { return top; } },
    region() { return head; },
    $(id) { return id === 'qsslot' ? qs : null; },
    inline() { return 'search'; },
    window: { innerWidth: 1000 },
    getComputedStyle() { events.push('read:style'); return { paddingRight: '5px' }; },
    q() { return [{ classList: { contains() { return false; } }, getClientRects() { events.push('read:rects'); return [1]; } }]; },
    dim() { events.push('read:dim'); return 0.5; },
    send(kind, value) { messages.push([kind, value]); },
    requestAnimationFrame(fn) { frames.push(fn); return frames.length; }
  };
  vm.createContext(context);
  vm.runInContext(source, context);
  context.queue(); context.queue();
  assert.equal(frames.length, 1);
  assert.deepEqual(events, []);
  frames.shift()();
  assert.ok(events.includes('read:dim'));
  assert.ok(events.lastIndexOf('read:dim') < events.indexOf('write:lifted'));
  assert.ok(events.filter(e => e.startsWith('read:')).every(e => events.indexOf(e) < events.indexOf('write:lifted')));
  assert.equal(messages[0][0], 'head');
  assert.equal(messages[0][1].pad, 15);
  assert.equal(messages[1][0], 'layout');
  assert.equal(messages[1][1].dim, 0.5);
  assert.equal(messages[1][1].popover, true);
  events.length = 0;
  context.queue();
  assert.equal(frames.length, 1);
  frames.shift()();
  assert.ok(!events.includes('read:style') && !events.includes('read:width'), 'overlay-only post reuses header geometry');
  context.queue();
  assert.equal(frames.length, 1);
  context.shHidden = true;
  const sent = messages.length;
  events.length = 0;
  frames.shift()();
  context.queue();
  assert.equal(frames.length, 0, 'hidden cached frames do not schedule shell geometry posts');
  assert.deepEqual(events, []);
  assert.equal(messages.length, sent);
});
