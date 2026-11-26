const test = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

const dashboardJS = readAssetSource('wsdashboard', 'dashboard.js');
const dashboardCSS = readAssetSource('wsdashboard', 'dashboard.css');

function slice(src, from, to) {
  const a = src.indexOf(from);
  const b = src.indexOf(to, a);
  assert(a >= 0 && b > a, 'production anchor exists: ' + from + ' .. ' + to);
  return src.slice(a, b);
}

const indentBlock = slice(dashboardJS, '  function indentLines(out) {', '    // The form script\'s own listeners');
const highlightBlock = slice(dashboardJS, '  var mdComposing = false, mdEditor = null;', '// Composer board selection mentions.');
const inlineSendBlock = slice(dashboardJS, '  function inlineSend() {', '  // --- outbox');

function makeField() {
  const listeners = Object.create(null);
  return {
    value: '',
    selectionStart: 0,
    selectionEnd: 0,
    wsc: null,
    setSelectionRange(a, b) { this.selectionStart = a; this.selectionEnd = b; },
    addEventListener(type, fn) { (listeners[type] = listeners[type] || []).push(fn); },
    dispatch(type, ev) {
      const e = Object.assign({
        type,
        defaultPrevented: false,
        preventDefault() { this.defaultPrevented = true; },
      }, ev || {});
      (listeners[type] || []).forEach(fn => fn(e));
      return e;
    },
    focus() {},
  };
}

function createKeyboardHarness({ inputMode = 'mod', bang = false } = {}) {
  const ctext = makeField();
  let bangActive = bang;
  let sent = [];
  let warned = [];
  const ctx = {
    doc: { addEventListener() {} },
    $: () => null,
    ctext,
    inputMode,
    effectiveInputMode: () => inputMode,
    cmdSel: null,
    mention: null,
    slash: null,
    bangHistKey: null,
    bangMode: () => bangActive,
    bangLeave() {},
    uncommand() {},
    queueDraft() {},
    grow() {},
    syncMarkers() {},
    trackMentions() {},
    paintMentions() {},
    editText(start, end, s) {
      ctext.value = ctext.value.slice(0, start) + s + ctext.value.slice(end);
      ctext.selectionStart = ctext.selectionEnd = start + s.length;
    },
    // sendMessage & inlineSend dependencies:
    cexecSend: () => false,
    window: { WsCapsules: { command: () => null } },
    restoring: false,
    draftStore: null,
    draftLoaded: true,
    bselections: [],
    bmentions: [],
    imgs: [],
    mpend: null,
    wpicks: [],
    wconsoles: [],
    closeRequests: [],
    boardOwn: {},
    outboxInFlight: () => false,
    inlineEditor: () => null,
    sendText: () => ctext.value,
    selectionPrefix: () => '',
    chipList: () => [],
    topicNotes: () => [],
    noteWarn(msg) { warned.push(msg); },
    wsT: k => k,
    saveDraft() {},
    dropImg() {},
    clearMentions() {},
    dropChip() {},
    setItem() {},
    closeTopicNotes() {},
    clearTrash() {},
    renderChips() {},
    renderImgs() {},
    lastUserEntry: () => '',
    outboxAdd(entry) { sent.push(entry.body.text); },
    Date,
    Math,
  };
  vm.createContext(ctx);
  vm.runInContext(inlineSendBlock + '\n' + indentBlock + '\n  }\n', ctx);

  function setState(value, a = value.length, b = a) {
    ctext.value = value;
    ctext.selectionStart = a;
    ctext.selectionEnd = b;
  }
  function pressEnter(opts = {}) {
    return ctext.dispatch('keydown', Object.assign({
      key: 'Enter',
      shiftKey: false,
      ctrlKey: false,
      metaKey: false,
      altKey: false,
      repeat: false,
      isComposing: false,
      keyCode: 13,
    }, opts));
  }
  function pressTab(opts = {}) {
    return ctext.dispatch('keydown', Object.assign({
      key: 'Tab',
      shiftKey: false,
      ctrlKey: false,
      metaKey: false,
      altKey: false,
      repeat: false,
      isComposing: false,
      keyCode: 9,
    }, opts));
  }
  return { ctx, ctext, sent, warned, setState, pressEnter, pressTab, setBang(v) { bangActive = v; } };
}

test('Enter preserves spaces/tabs indentation and continues unordered, ordered, task, and blockquote lines', () => {
  const h = createKeyboardHarness({ inputMode: 'mod' });

  // Leading spaces
  h.setState('    hello');
  h.pressEnter();
  assert.equal(h.ctext.value, '    hello\n    ');
  assert.equal(h.ctext.selectionStart, h.ctext.value.length);

  // Leading tab
  h.setState('\tcode');
  h.pressEnter();
  assert.equal(h.ctext.value, '\tcode\n\t');

  // Unordered list markers (-, *, +)
  for (const bullet of ['- ', '* ', '+ ']) {
    h.setState('  ' + bullet + 'item');
    h.pressEnter();
    assert.equal(h.ctext.value, '  ' + bullet + 'item\n  ' + bullet);
  }

  // Ordered list increments number
  h.setState('  1. first');
  h.pressEnter();
  assert.equal(h.ctext.value, '  1. first\n  2. ');

  h.setState('9. nine');
  h.pressEnter();
  assert.equal(h.ctext.value, '9. nine\n10. ');

  // Task list continues as unchecked preserving bullet
  h.setState('  - [x] done');
  h.pressEnter();
  assert.equal(h.ctext.value, '  - [x] done\n  - [ ] ');

  h.setState('* [X] ALSO');
  h.pressEnter();
  assert.equal(h.ctext.value, '* [X] ALSO\n* [ ] ');

  // Blockquote preserves prefix including nested quotes
  h.setState('> quoted');
  h.pressEnter();
  assert.equal(h.ctext.value, '> quoted\n> ');

  h.setState('> > nested');
  h.pressEnter();
  assert.equal(h.ctext.value, '> > nested\n> > ');

  // Caret in the middle of a list line splits content onto continued item
  h.setState('- hello world', 7);
  h.pressEnter();
  assert.equal(h.ctext.value, '- hello\n-  world');
});

test('Enter on empty list/quote marker exits list cleanly, and respects IME and bang-mode guards', () => {
  const h = createKeyboardHarness({ inputMode: 'mod' });

  for (const emptyLine of ['- ', '  * ', '  1.   ', '  - [ ] ', '> ', '  > > ']) {
    h.setState('prev\n' + emptyLine);
    const ev = h.pressEnter();
    assert.equal(ev.defaultPrevented, true);
    assert.equal(h.ctext.value, 'prev\n', 'empty marker exits cleanly: ' + JSON.stringify(emptyLine));
  }

  // Non-empty marker line with caret right after marker does NOT exit
  h.setState('- keep', 2);
  h.pressEnter();
  assert.equal(h.ctext.value, '- \n- keep');

  // IME isComposing and Safari keyCode 229 are skipped
  h.setState('- item');
  const eComp = h.pressEnter({ isComposing: true });
  assert.equal(eComp.defaultPrevented, false);
  assert.equal(h.ctext.value, '- item');

  const e229 = h.pressEnter({ keyCode: 229 });
  assert.equal(e229.defaultPrevented, false);
  assert.equal(h.ctext.value, '- item');

  // ! bang mode does not auto-indent or intercept newline
  h.setBang(true);
  h.setState('!  echo hi');
  const eBang = h.pressEnter();
  assert.equal(eBang.defaultPrevented, false);
  assert.equal(h.ctext.value, '!  echo hi');
});

test('Smart Tab nests list/quote lines and leading-whitespace carets, preserves mid-prose Tab and Shift+Tab', () => {
  const h = createKeyboardHarness({ inputMode: 'mod' });

  // Caret at end of list item -> indents line start and advances caret by +2
  h.setState('- item', 6);
  h.pressTab();
  assert.equal(h.ctext.value, '  - item');
  assert.equal(h.ctext.selectionStart, 8);
  assert.equal(h.ctext.selectionEnd, 8);

  // Caret mid-line on ordered list -> indents line start and advances caret by +2
  h.setState('  1. first', 7);
  h.pressTab();
  assert.equal(h.ctext.value, '    1. first');
  assert.equal(h.ctext.selectionStart, 9);

  // Blockquote line -> indents line start
  h.setState('> quote', 4);
  h.pressTab();
  assert.equal(h.ctext.value, '  > quote');
  assert.equal(h.ctext.selectionStart, 6);

  // Caret inside leading whitespace of plain text -> indents line start and advances caret by +2
  h.setState('  hello', 1);
  h.pressTab();
  assert.equal(h.ctext.value, '    hello');
  assert.equal(h.ctext.selectionStart, 3);

  // Mid-prose on plain text -> inserts 2 spaces at caret
  h.setState('hello world', 5);
  h.pressTab();
  assert.equal(h.ctext.value, 'hello   world');
  assert.equal(h.ctext.selectionStart, 7);

  // Shift+Tab outdents up to 2 leading spaces
  h.setState('  - item', 8);
  h.pressTab({ shiftKey: true });
  assert.equal(h.ctext.value, '- item');
  assert.equal(h.ctext.selectionStart, 6);
});

test('inlineSend and sendMessage fallback preserve line-1 leading indent while stripping blank lines and blocking whitespace-only', () => {
  const h = createKeyboardHarness({ inputMode: 'mod' });

  // Fallback sendMessage() path (no inlineEditor)
  h.setState('\n  \r\n  - nested\n    - child  \n\n');
  h.ctx.sendMessage();
  assert.deepEqual(h.sent, ['  - nested\n    - child']);

  // Whitespace-only is still rejected
  h.sent.length = 0;
  h.warned.length = 0;
  h.setState('   \n\t  ');
  h.ctx.sendMessage();
  assert.equal(h.sent.length, 0);
  assert.equal(h.warned.length, 1);

  // inlineSend() path preserves line-1 leading indent
  h.ctx.inlineEditor = () => ({ ordered: () => [], snapshot: () => [] });
  h.setState('\n\t  - item\n    - sub  ');
  const data = h.ctx.inlineSend();
  assert.equal(data.text, '\t  - item\n    - sub');

  h.setState('   \r\n  ');
  assert.equal(h.ctx.inlineSend().text, '');
});

function createHighlightHarness({ hasMdEditor = true, bang = false } = {}) {
  const ctext = makeField();
  let bangActive = bang;
  let attachCalls = 0;
  let reconcileCalls = 0;
  let scheduleCalls = 0;
  let bangPaintCalls = 0;
  let lastOpts = null;

  const editor = { nodeType: 1, nodeName: 'DIV', childNodes: [] };
  ctext.wsc = { editor };

  const win = hasMdEditor ? {
    WsMdEditor: {
      attach(el, opts) {
        attachCalls++;
        assert.equal(el, editor);
        lastOpts = opts;
        return {
          reconcile() { reconcileCalls++; if (opts.onReconciled) opts.onReconciled(); },
          schedule() { scheduleCalls++; },
          detach() {},
        };
      },
    },
  } : {};

  const ctx = {
    window: win,
    ctext,
    mentions: [],
    bangMode: () => bangActive,
    bangPaint: () => { bangPaintCalls++; },
  };
  vm.createContext(ctx);
  vm.runInContext(highlightBlock, ctx);

  return {
    ctx,
    ctext,
    getCounts: () => ({ attachCalls, reconcileCalls, scheduleCalls, bangPaintCalls }),
    getOpts: () => lastOpts,
    setBang(v) { bangActive = v; },
  };
}

test('composer highlight bridge wires #ctext to WsMdEditor, pauses during IME composition, passes bang/mention getters, and no-ops without WsMdEditor', () => {
  const h = createHighlightHarness({ hasMdEditor: true });
  assert.equal(h.getCounts().attachCalls, 1);

  h.ctx.mentions = [{ start: 0, end: 5, text: '@a.js' }];
  assert.deepEqual(h.getOpts().getMentions(), [{ start: 0, end: 5, text: '@a.js' }]);
  assert.equal(h.getOpts().isBang(), false);
  assert.equal(h.getOpts().isComposing(), false);

  h.ctx.scheduleComposerHighlights();
  assert.equal(h.getCounts().scheduleCalls, 1);

  // IME composition pauses scheduling until compositionend
  h.ctext.dispatch('compositionstart');
  assert.equal(h.getOpts().isComposing(), true);
  h.ctx.scheduleComposerHighlights();
  assert.equal(h.getCounts().scheduleCalls, 1, 'no schedule while composing');
  h.ctext.dispatch('compositionend');
  assert.equal(h.getOpts().isComposing(), false);
  assert.equal(h.getCounts().scheduleCalls, 2, 'compositionend schedules refresh');

  // Bang mode triggers bangPaint on reconcile
  h.setBang(true);
  assert.equal(h.getOpts().isBang(), true);
  h.ctx.applyComposerHighlights();
  assert.equal(h.getCounts().reconcileCalls, 1);
  assert.equal(h.getCounts().bangPaintCalls, 1);

  // Missing WsMdEditor gracefully no-ops
  const noApi = createHighlightHarness({ hasMdEditor: false });
  assert.doesNotThrow(() => {
    noApi.ctx.scheduleComposerHighlights();
    noApi.ctx.applyComposerHighlights();
    noApi.ctx.clearComposerHighlights();
  });
});

test('dashboard CSS replaces ::highlight(ws-md-*) with structured .wsmd-* token-backed rules while keeping ::highlight(ws-bang)', () => {
  assert.match(dashboardCSS, /::highlight\(ws-bang\)/, 'shell-mode bang highlight preserved');
  assert.doesNotMatch(dashboardCSS, /::highlight\(ws-md-/, 'retired CSS.highlights ws-md-* rules removed');
  for (const cls of ['wsmd-h1', 'wsmd-h6', 'wsmd-code', 'wsmd-fence-body', 'wsmd-quote', 'wsmd-task-done', 'wsmd-table-head', 'wsmd-mention']) {
    assert.match(dashboardCSS, new RegExp(`\\.${cls}\\b`), `missing structured markdown rule .${cls}`);
  }
});
