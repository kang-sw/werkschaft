// Offline contract: shipped board functions only, no daemon or child process.
const assert = require('node:assert/strict');
const vm = require('node:vm');
const test = require('node:test');
const { readAssetSource } = require('./asset-source.cjs');
const source = readAssetSource('wsdashboard', 'dashboard.js');
const animation = readAssetSource('wsdashboard', 'dashboard-animation.js');
function funcs(names) {
  return names.map(name => {
    const found = source.match(new RegExp('\\n  function ' + name + '\\([^\\n]*\\{[^\\n]*\\}\\n|\\n  function ' + name + '\\([\\s\\S]*?\\n  \\}\\n'));
    assert.ok(found, 'missing shipped function ' + name);
    return found[0];
  }).join('');
}
for (const latched of [[], ['T20'], ['T20', 'NEW']]) {
  test('up-arrow keeps all pending signals until apply, then selects the first topic: latches=' + (latched.join(',') || 'none'), () => {
    const c = vm.createContext({ assert });
    vm.runInContext(`
      var latched = ${JSON.stringify(latched)};
      var swaps = 0, rendered = 'old', selected = [], first = { key: 'T01' };
      var window = { wbPill: { update: function () {} } };
      var deferred = null, boardDragHeld = null, boardOverviewHeld = null, boardAfterApply = [];
      var flashed = [];
      function boardFlash(names, inView) { flashed.push({ names: names, inView: inView }); }
      function topicKey(n) { return n.key; }
      function anchorCards() { return [{ key: null }, first, { key: 'T20' }]; }
      function anchorSelect(n, opt) { selected.push(n); return true; }
      var anchorUserScroll = 0;
      var boardReleaseFrame = 7, boardRelease = false;
      var boardPending = Object.create(null);
      var boardLatest = { hash: 'latest', html: { content: 'new' } };
      latched.forEach(function (k) { boardPending[k] = true; });
      boardPending.T04 = [{}];
      var boardChanges = [{ names: latched.concat('T04') }, { names: latched.slice() }];
      function swap(name, hash, input, still) {
        swaps++;
        assert.equal(boardRelease, true, 'explicit navigation owns the release');
        assert.equal(name, 'board'); assert.equal(hash, 'latest'); assert.equal(still, true);
        rendered = input.content;
        boardPending = Object.create(null);
        boardRecordPending();
      }
    ` + funcs(['boardPendingNames', 'boardPendingBadge', 'boardHeldNow', 'boardApplyPending', 'boardApplied', 'boardHome', 'boardFlushViewed', 'boardRecordPending']) + `
      boardPendingBadge(); assert.equal(window.wbPill.pending, latched.length + 1);
      assert.deepEqual(boardPendingNames(), latched.concat('T04'));
      assert.equal(window.wbPill.apply, boardApplyPending, 'explicit navigation acknowledges through the same apply');
      assert.equal(rendered, 'old'); assert.equal(swaps, 0); assert.deepEqual(selected, []);
      window.wbPill.home();
      assert.equal(swaps, 1); assert.equal(rendered, 'new');
      assert.equal(window.wbPill.pending, 0, 'badge clears only after applying the snapshot');
      assert.deepEqual(boardPendingNames(), []);
      assert.deepEqual(selected, [first], 'the first topic is selected after apply');
      assert.deepEqual(flashed, [{ names: latched.concat('T04'), inView: false }]);

      deferred = {};
      window.wbPill.home();
      assert.equal(boardAfterApply.length, 1, 'a held board update defers the alignment');
      anchorUserScroll++;
      deferred = null;
      boardApplied();
      assert.deepEqual(selected, [first], 'new scroll input supersedes the deferred jump');
    `, c);
  });
}

function returningBoard() {
  const c = vm.createContext({ assert });
  vm.runInContext(animation, c);
  vm.runInContext(`
    var timers = new Map(), nextTimer = 1;
    function setTimeout(fn, ms) { var id = nextTimer++; timers.set(id, { fn: fn, ms: ms }); return id; }
    function clearTimeout(id) { timers.delete(id); }
    var shHidden = false, doc = { hidden: false, scrollingElement: null }, scroller = { scrollTop: 240 };
    var anchor = 360, captures = 0, corrections = 0, morphs = 0;
    var el = { firstElementChild: {}, children: [], contains: function (n) { return n === this || n === this.firstElementChild; }, getAttribute: function () { return this.hash; }, setAttribute: function (k, v) { this.hash = v; } };
    function region(name) { return name === 'board' ? el : null; }
    function $(id) { return id === 'bcol' ? scroller : null; }
    function boardBoxes() { return [el]; }
    var window = { WebSocket: function () {}, wbPill: { update: function () {} }, wbHistory: {
      keep: function (node, opt) {
        if (opt && opt.capture === false) return { scroller: scroller };
        captures++;
        var y = anchor - scroller.scrollTop;
        return { apply: function () { corrections++; scroller.scrollTop = anchor - y; } };
      }, init: function () {}, restore: function () {}
    } };
    function boardHome() { throw Error('return must not navigate home'); }
    function boardFlash() { throw Error('return must not flash'); }
    function topicSeen(n) { return n; }
    var boardPending = { T: true }, boardAfterApply = [];
    var boardChanges = [{ names: ['T'] }], boardRelease = false, boardReleaseFrame = 0;
    var boardReturnTimer = 0, boardReturnFresh = false;
    var boardLatest = { hash: 'held-before-leaving', html: { topics: { T: 'before-leaving' }, order: ['T'] } };
    var boardRendered = { topics: { T: 'old' }, order: ['T'] }, boardOffered = null;
    var boardImmediate = {}, boardFrozen = [], boardPagedEdits = [], boardHomeWrites = new WeakSet();
    var boardExposedUnits = null, hiddenRegions = {}, deferred = null, boardOverviewHeld = null, boardDragHeld = null;
    var railGate = null, WsRail = null, railHoverUpdates = null, ctext = null, mpend = null, live = false;
    var TL_REGIONS = {}, tlHeld = {}, sseOn = false;
    function boardStageInput(input) {
      boardPending = {}; boardImmediate = {};
      var topics = Object.assign({}, input.topics), order = input.order.slice();
      // Model a panel the user is reading. The shipped top latch runs too.
      if (!boardRelease) { topics.T = boardRendered.topics.T; boardPending.T = true; }
      boardRendered = { topics: topics, order: order };
      return boardRendered;
    }
    function boardScroller() { return scroller; }
    function boardExposed() { return true; }
    function boardRead() { return {}; }
    function readAnswers() {}
    function frameHeights() {}
    function captureBoardHistory() { return {}; }
    function editing() { return false; }
    function pinned() { return false; }
    function scrollState() { return [[scroller, scroller.scrollTop]]; }
    function restoreScroll(saved) { saved.forEach(function (p) { p[0].scrollTop = p[1]; }); }
    function reducedMotion() { return false; }
    function anchorCards() { return []; }
    function anchorSettle() {}
    function anchorCurrent() { return null; }
    function anchorOwner() { return null; }
    function companionStream() {}
    var companionRelay = null, companionStreamReady = true;
    function morphBoard() { morphs++; anchor += 35; return []; }
    function bandBoard() {}
    function indexFrames() {}
    function bindCapsuleNotes() {}
    function mermaid() {}
    function syncChips() {}
    function applySent() {}
    function boardHoldIndicators() {}
    function boardWithSnap(fn) { return fn(); }
    function boardExposureUnits() { return []; }
    function tlCount() {}
    function tlShown() {}
    function connected() {}
    function railNoteDisconnect() {}
    function trackerLost() {}
    var mainStream = null, mainRetry = null, mainProbe = null, mainEpoch = 0, mainPaused = false;
    var daemonBridgeEnabled = false, daemonTabId = '';
    var mode = 'live', shell = false, worktree = false, framed = true, rid = 'session';
    function held() { return ''; }
    function boardInputFull(input) { return input; }
    function boardInputPatch(previous, ev) { return ev.next; }
    var handlers;
    function openStream(path, h) { handlers = h; return { close: function () {} }; }
  ` + funcs(['docHidden', 'pageHidden', 'swap', 'boardKeep', 'boardPendingNames', 'boardPendingBadge',
    'boardHeldNow', 'boardApplyPending', 'boardApplied', 'boardFlushViewed', 'boardRecordPending', 'boardReturnShown', 'boardReturnEnd',
    'boardReturnApply', 'stopMainStream', 'syncMainStream', 'connect', 'showRegions']) + `
    boardPendingBadge();
  `, c);
  return c;
}

for (const restored of [false, true]) test('shell return applies pre-existing and reconnect holds in place, restore=' + restored, () => {
  const c = returningBoard();
  vm.runInContext(`
    assert.equal(window.wbPill.pending, 1);
    shHidden = true; syncMainStream();
    assert.equal(mainPaused, true);
    assert.equal(window.wbPill.pending, 1, 'leaving alone does not mutate the board');
    ${restored ? "hiddenRegions.board = { hash: 'while-hidden', html: { topics: { T: 'hidden' }, order: ['T'] } };" : ''}
    shHidden = false; syncMainStream(); showRegions();
    assert.equal(boardRendered.topics.T, ${restored ? "'hidden'" : "'before-leaving'"});
    assert.equal(window.wbPill.pending, 0);
    assert.ok(boardReturnTimer, 'old restore does not consume the reconnect window');
    handlers.events['board-topics']({ hash: 'reconnect', next: { topics: { T: 'current' }, order: ['T'] } });
    assert.equal(boardRendered.topics.T, 'current');
    assert.equal(window.wbPill.pending, 0);
    assert.equal(boardReturnTimer, 0, 'first fresh snapshot ends the exemption');
    assert.equal(timers.size, 0);
    handlers.events['board-topics']({ hash: 'reading-again', next: { topics: { T: 'later' }, order: ['T'] } });
    assert.equal(boardRendered.topics.T, 'current', 'normal exposure holding resumes immediately');
    assert.equal(window.wbPill.pending, 1);
  `, c);
});

test('an already-current reconnect snapshot also closes the return window', () => {
  const c = returningBoard();
  vm.runInContext(`
    shHidden = true; syncMainStream(); shHidden = false; syncMainStream();
    assert.ok(boardReturnTimer);
    var before = morphs;
    handlers.events.region({ name: 'board', hash: boardLatest.hash, html: boardLatest.html });
    assert.equal(morphs, before, 'current bytes need no second morph');
    assert.equal(boardReturnTimer, 0);
    assert.equal(window.wbPill.pending, 0);
  `, c);
});

test('shell return with no new snapshot expires; browser-tab visibility alone never opens it', () => {
  const c = returningBoard();
  vm.runInContext(`
    doc.hidden = true; syncMainStream();
    doc.hidden = false; syncMainStream(); showRegions();
    assert.equal(boardReturnTimer, 0);
    assert.equal(window.wbPill.pending, 1, 'browser-tab visibility keeps existing hold behavior');
    shHidden = true; syncMainStream(); shHidden = false; syncMainStream();
    assert.equal(window.wbPill.pending, 0);
    var timeout = timers.get(boardReturnTimer);
    assert.equal(timeout.ms, 1500);
    timeout.fn(); assert.equal(boardReturnTimer, 0);
    handlers.events.region({ name: 'board', hash: 'after-timeout', html: { topics: { T: 'later' }, order: ['T'] } });
    assert.equal(window.wbPill.pending, 1);
    assert.equal(boardRendered.topics.T, 'before-leaving');
    shHidden = true; syncMainStream(); shHidden = false; syncMainStream();
    assert.ok(boardReturnTimer);
    shHidden = true; syncMainStream();
    assert.equal(boardReturnTimer, 0, 'leaving again cancels the old window');
  `, c);
});
