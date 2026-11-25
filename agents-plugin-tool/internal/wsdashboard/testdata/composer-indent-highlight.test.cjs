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
const highlightBlock = slice(dashboardJS, '  var MD_HIGHLIGHT_GROUPS = [', '// Composer board selection mentions.');
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

function createHighlightHarness({ hasCSSHighlights = true, bang = false } = {}) {
  const ctext = makeField();
  const registry = new Map();
  let rafQueue = [];
  let rafSeq = 0;
  let bangActive = bang;

  class MockHighlight {
    constructor() { this.ranges = []; this.priority = 0; }
    add(r) { this.ranges.push(r); }
  }
  const doc = {
    createRange() {
      return {
        startContainer: null, startOffset: 0, endContainer: null, endOffset: 0,
        setStart(n, o) { this.startContainer = n; this.startOffset = o; },
        setEnd(n, o) { this.endContainer = n; this.endOffset = o; },
      };
    },
  };
  const win = hasCSSHighlights ? { CSS: { highlights: registry }, Highlight: MockHighlight } : { CSS: {} };
  const ctx = {
    window: win,
    CSS: win.CSS,
    Highlight: win.Highlight,
    doc,
    ctext,
    mentions: [],
    bangMode: () => bangActive,
    requestAnimationFrame(fn) { const id = ++rafSeq; rafQueue.push({ id, fn }); return id; },
  };
  vm.createContext(ctx);
  vm.runInContext(highlightBlock, ctx);

  function flushRaf() {
    const q = rafQueue.slice();
    rafQueue = [];
    q.forEach(item => item.fn());
  }
  return { ctx, ctext, doc, registry, flushRaf, rafCount: () => rafQueue.length, setBang(v) { bangActive = v; } };
}

function makeTextNode(value, parentNode = null) {
  return { nodeType: 3, nodeValue: value, parentNode, previousSibling: null, nextSibling: null };
}
function makeEl(tag, attrs = {}, children = []) {
  const el = {
    nodeType: 1,
    nodeName: tag.toUpperCase(),
    childNodes: [],
    parentNode: null,
    previousSibling: null,
    nextSibling: null,
    hasAttribute(k) { return Object.prototype.hasOwnProperty.call(attrs, k); },
    getAttribute(k) { return Object.prototype.hasOwnProperty.call(attrs, k) ? attrs[k] : null; },
  };
  children.forEach((ch, idx) => {
    ch.parentNode = el;
    if (idx > 0) {
      ch.previousSibling = children[idx - 1];
      children[idx - 1].nextSibling = ch;
    }
    el.childNodes.push(ch);
  });
  return el;
}

test('scanComposerMarkdown tokenizes headings, emphasis, strike, inline/fenced code, links, quotes, lists, mentions, and excludes attachment markers', () => {
  const h = createHighlightHarness();
  const scan = (text, mentions = []) => JSON.parse(JSON.stringify(h.ctx.scanComposerMarkdown(text, mentions)));
  const slices = (text, spans) => spans.map(([s, e]) => text.slice(s, e));

  // Headings, bold, italic, strike, link
  const s1 = '# Title **bold** *em* ~~del~~ [docs](https://example.test)';
  const g1 = scan(s1);
  assert.deepEqual(slices(s1, g1['ws-md-head']), [' Title **bold** *em* ~~del~~ [docs](https://example.test)']);
  assert.deepEqual(slices(s1, g1['ws-md-strong']), ['bold']);
  assert.deepEqual(slices(s1, g1['ws-md-em']), ['em']);
  assert.deepEqual(slices(s1, g1['ws-md-strike']), ['del']);
  assert.deepEqual(slices(s1, g1['ws-md-link']), ['docs']);
  assert.ok(slices(s1, g1['ws-md-marker']).includes('#'));
  assert.ok(slices(s1, g1['ws-md-marker']).includes('**'));
  assert.ok(slices(s1, g1['ws-md-marker']).includes('*'));
  assert.ok(slices(s1, g1['ws-md-marker']).includes('~~'));
  assert.ok(slices(s1, g1['ws-md-marker']).includes('['));
  assert.ok(slices(s1, g1['ws-md-marker']).includes('](https://example.test)'));

  // Inline code suppresses inner markdown
  const s2 = 'Run `**not bold** *not em* [a](b)` now';
  const g2 = scan(s2);
  assert.deepEqual(slices(s2, g2['ws-md-code']), ['`**not bold** *not em* [a](b)`']);
  assert.deepEqual(g2['ws-md-strong'], []);
  assert.deepEqual(g2['ws-md-em'], []);
  assert.deepEqual(g2['ws-md-link'], []);

  // Fenced code blocks suppress inner markdown and mentions
  const s3 = '```js\n# not heading\n**not bold** @foo.js\n```\nAfter';
  const mAt = s3.indexOf('@foo.js');
  const g3 = scan(s3, [{ start: mAt, end: mAt + 7, text: '@foo.js' }]);
  assert.deepEqual(slices(s3, g3['ws-md-marker']), ['```js', '```']);
  assert.deepEqual(slices(s3, g3['ws-md-code']), ['# not heading', '**not bold** @foo.js']);
  assert.deepEqual(g3['ws-md-head'], []);
  assert.deepEqual(g3['ws-md-strong'], []);
  assert.deepEqual(g3['ws-md-mention'], []);

  // Blockquotes, list markers, task lists, snake_case safety
  const s4 = '> quote\n- [x] task_name_here\n1. ordered';
  const g4 = scan(s4);
  assert.deepEqual(slices(s4, g4['ws-md-quote']), ['quote']);
  assert.deepEqual(slices(s4, g4['ws-md-marker']), ['> ', '- [x]', '1.']);
  assert.deepEqual(g4['ws-md-em'], [], 'snake_case underscores must not trigger italic');

  // Attachment markers [Image #1] and [Text #1] are excluded even when followed by (...)
  const s5 = 'See [Image #1](caption) and [Text #2](note) and [real](https://a.test)';
  const g5 = scan(s5);
  assert.deepEqual(slices(s5, g5['ws-md-link']), ['real']);

  // @ file mentions outside fenced blocks
  const s6 = 'Check @src/app.js please';
  const g6 = scan(s6, [{ start: 6, end: 17, text: '@src/app.js' }]);
  assert.deepEqual(slices(s6, g6['ws-md-mention']), ['@src/app.js']);
});

test('buildEditorTextMap skips .wsc-capsule subtrees and maps canonical offsets accurately across capsules and line breaks', () => {
  const h = createHighlightHarness();
  const t1 = makeTextNode('See ');
  const cap = makeEl('span', {
    'data-wsc-kind': 'topic',
    'data-wsc-id': 'auth',
    'data-wsc-label': 'auth',
  }, [makeTextNode('#auth')]);
  const t2 = makeTextNode(' **bold**');
  const br = makeEl('br');
  const t3 = makeTextNode('> quoted');
  const editor = makeEl('div', {}, [t1, cap, t2, br, t3]);

  // Canonical value: "See [auth] **bold**\n> quoted"
  // "See " = 0..4, "[auth]" = 4..10 (capsule skipped), " **bold**" = 10..19, "\n" = 19..20, "> quoted" = 20..28
  const segs = h.ctx.buildEditorTextMap(editor);
  assert.equal(segs.length, 3);
  assert.equal(segs[0].node, t1); assert.equal(segs[0].start, 0); assert.equal(segs[0].end, 4);
  assert.equal(segs[1].node, t2); assert.equal(segs[1].start, 10); assert.equal(segs[1].end, 19);
  assert.equal(segs[2].node, t3); assert.equal(segs[2].start, 20); assert.equal(segs[2].end, 28);

  // Mapping "bold" (13..17) lands strictly inside t2 [3..7]
  const ranges = h.ctx.valueSpanToDomRanges(h.doc, segs, 13, 17);
  assert.equal(ranges.length, 1);
  assert.equal(ranges[0].startContainer, t2);
  assert.equal(ranges[0].startOffset, 3);
  assert.equal(ranges[0].endOffset, 7);

  // Mapping a span that crosses the capsule (0..19) splits into t1 and t2, never entering cap
  const split = h.ctx.valueSpanToDomRanges(h.doc, segs, 0, 19);
  assert.equal(split.length, 2);
  assert.equal(split[0].startContainer, t1);
  assert.equal(split[1].startContainer, t2);
});

test('highlight scheduling coalesces via rAF, pauses during IME composition, clears in bang mode, and no-ops without CSS.highlights', () => {
  const h = createHighlightHarness({ hasCSSHighlights: true });
  const t1 = makeTextNode('**hi** @a.js');
  const editor = makeEl('div', {}, [t1]);
  h.ctext.value = '**hi** @a.js';
  h.ctext.wsc = { editor };
  h.ctx.mentions = [{ start: 7, end: 12, text: '@a.js' }];

  // Multiple schedule calls coalesce into one rAF
  h.ctx.scheduleComposerHighlights();
  h.ctx.scheduleComposerHighlights();
  assert.equal(h.rafCount(), 1);
  h.flushRaf();
  assert.equal(h.registry.has('ws-md-strong'), true);
  assert.equal(h.registry.has('ws-md-mention'), true);
  assert.ok(h.registry.get('ws-md-code') === undefined);

  // IME composition pauses scheduling until compositionend
  h.ctext.dispatch('compositionstart');
  h.ctx.scheduleComposerHighlights();
  assert.equal(h.rafCount(), 0, 'no rAF scheduled while composing');
  h.ctext.dispatch('compositionend');
  assert.equal(h.rafCount(), 1, 'compositionend schedules refresh');
  h.flushRaf();

  // Bang mode clears all ws-md-* highlights
  h.setBang(true);
  h.ctx.scheduleComposerHighlights();
  h.flushRaf();
  assert.equal(h.registry.size, 0, 'bang mode clears all ws-md-* groups');

  // Missing CSS.highlights / Highlight gracefully no-ops
  const noApi = createHighlightHarness({ hasCSSHighlights: false });
  noApi.ctext.value = '**hi**';
  noApi.ctext.wsc = { editor };
  assert.doesNotThrow(() => {
    noApi.ctx.scheduleComposerHighlights();
    noApi.ctx.applyComposerHighlights();
    noApi.ctx.clearComposerHighlights();
  });
});

test('dashboard CSS defines ::highlight(ws-md-*) rules using canonical --wb-* tokens', () => {
  for (const name of ['ws-md-marker', 'ws-md-code', 'ws-md-head', 'ws-md-quote', 'ws-md-em', 'ws-md-strong', 'ws-md-strike', 'ws-md-link', 'ws-md-mention']) {
    assert.match(dashboardCSS, new RegExp(`::highlight\\(${name}\\)\\s*\\{[^}]*--wb-`), `missing token-backed ::highlight(${name})`);
  }
});
