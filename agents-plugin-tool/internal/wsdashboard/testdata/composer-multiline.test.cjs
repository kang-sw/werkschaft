const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

const dashboardJS = readAssetSource('wsdashboard', 'dashboard.js');
const dashboardCSS = readAssetSource('wsdashboard', 'dashboard.css');
const dashboardTmpl = fs.readFileSync(path.resolve(__dirname, '../assets/dashboard.tmpl'), 'utf8');
const en = JSON.parse(fs.readFileSync(path.resolve(__dirname, '../../wsi18n/tables/en.json'), 'utf8'));
const ko = JSON.parse(fs.readFileSync(path.resolve(__dirname, '../../wsi18n/tables/ko.json'), 'utf8'));

function slice(src, from, to) {
  const a = src.indexOf(from);
  const b = src.indexOf(to, a);
  assert(a >= 0 && b > a, 'production anchor exists: ' + from + ' .. ' + to);
  return src.slice(a, b);
}

const inputBlock = slice(dashboardJS, '  var INPUT_KEY = "ws.dashboard.input"', '  function setTab(t) {');
const keyboardBlock = slice(dashboardJS, '  if (ctext) {\n    var csend = $("csend");', '    // The form script\'s own listeners');
const bangKeyBlock = slice(dashboardJS, '  function bangSendKeyLabel() {', '  // --- trigger badge & popup UI');
const bangSyncModeBlock = slice(dashboardJS, '  function bangHistSyncMode() {', '  function histCreateRowEl(cmd) {');
const sendMessageBlock = slice(dashboardJS, '  function sendMessage() {', '  // --- outbox');
const cexecAckBlock = slice(dashboardJS, '  function cexecAcknowledged(text, track) {', '  // cexecSend takes the composer\'s raw text');

function makeElement(id) {
  const classes = new Set();
  const attrs = Object.create(null);
  const listeners = Object.create(null);
  return {
    id,
    value: '',
    selectionStart: 0,
    selectionEnd: 0,
    setSelectionRange(a, b) { this.selectionStart = a; this.selectionEnd = b; },
    title: '',
    hidden: false,
    textContent: '',
    placeholder: '',
    focused: 0,
    children: [],
    classList: {
      toggle(name, force) {
        const on = force === undefined ? !classes.has(name) : !!force;
        if (on) classes.add(name);
        else classes.delete(name);
        return on;
      },
      contains(name) { return classes.has(name); },
    },
    setAttribute(k, v) { attrs[k] = String(v); },
    getAttribute(k) { return Object.prototype.hasOwnProperty.call(attrs, k) ? attrs[k] : null; },
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
    focus() { this.focused++; },
    appendChild(child) { this.children.push(child); return child; },
  };
}

function createHarness({ platform = 'Win32', table = en, initialInputMode = 'enter' } = {}) {
  const ctext = makeElement('ctext');
  const csend = makeElement('csend');
  const csendKbd = makeElement('csend-kbd');
  const csendRevert = makeElement('csend-revert');
  csendKbd.hidden = true;
  csendRevert.hidden = true;
  csend.title = table['composer.send'];
  csend.setAttribute('aria-label', table['composer.send']);

  const histPop = makeElement('hist-pop');
  const histEmptyHintEl = makeElement('hist-empty-hint');
  const histHintsEl = makeElement('hist-hints');

  const byId = {
    ctext,
    csend,
    'csend-kbd': csendKbd,
    'csend-revert': csendRevert,
  };

  let sent = [];
  let bangActive = false;
  let mentionHandled = false;
  let slashHandled = false;
  let bangHistHandled = false;

  const doc = {
    addEventListener() {},
    createElement(tag) {
      const el = makeElement(tag);
      el.tagName = tag.toUpperCase();
      return el;
    },
    createTextNode(t) { return { nodeType: 3, textContent: String(t) }; },
  };

  const ctx = {
    doc,
    navigator: { platform, userAgent: platform },
    $: id => byId[id] || null,
    q: () => [],
    wsT(k) {
      let s = table[k] || k;
      for (let i = 1; i < arguments.length; i++) s = s.replace('{' + (i - 1) + '}', arguments[i]);
      return s;
    },
    ctext,
    cmdSel: null,
    histPop,
    histEmptyHintEl,
    histHintsEl,
    mention: { key: () => mentionHandled },
    slash: { key: () => slashHandled },
    bangHistKey: () => bangHistHandled,
    bangMode: () => bangActive,
    bangLeave() {},
    uncommand() {},
    queueDraft() {},
    grow() {},
    growCtext() {},
    syncMarkers() {},
    trackMentions() {},
    paintMentions() {},
    indentLines() {},
    editText(start, end, s) {
      ctext.value = ctext.value.slice(0, start) + s + ctext.value.slice(end);
      ctext.selectionStart = ctext.selectionEnd = start + s.length;
      ctext.dispatch('input');
    },
    // External delivery/draft collaborators for production sendMessage & cexecAcknowledged:
    window: {},
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
    cexecSend: () => false,
    outboxInFlight: () => false,
    inlineEditor: () => null,
    sendText: () => ctext.value,
    selectionPrefix: () => '',
    chipList: () => [],
    topicNotes: () => [],
    noteWarn() {},
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
    bangSync() {},
    bangHistClose() {},
  };

  vm.createContext(ctx);
  vm.runInContext(
    inputBlock + '\n' +
    bangKeyBlock + '\n' +
    bangSyncModeBlock + '\n' +
    sendMessageBlock + '\n' +
    cexecAckBlock + '\n' +
    keyboardBlock + '\n  }\n',
    ctx
  );
  ctx.setInput(initialInputMode);

  function keydown(props) {
    return ctext.dispatch('keydown', Object.assign({
      key: 'Enter',
      shiftKey: false,
      ctrlKey: false,
      metaKey: false,
      altKey: false,
      repeat: false,
      isComposing: false,
      keyCode: 13,
    }, props));
  }

  function beforeinput(props) {
    return ctext.dispatch('beforeinput', Object.assign({
      inputType: 'insertParagraph',
      data: null,
      isComposing: false,
    }, props));
  }

  function setValueAndInput(v) {
    ctext.value = v;
    ctext.selectionStart = ctext.selectionEnd = v.length;
    ctext.dispatch('input');
  }

  return {
    ctx,
    ctext,
    csend,
    csendKbd,
    csendRevert,
    histEmptyHintEl,
    histHintsEl,
    sent,
    keydown,
    beforeinput,
    setValueAndInput,
    setBang(v) { bangActive = v; },
    setPopup({ mention = false, slash = false, bangHist = false }) {
      mentionHandled = mention;
      slashHandled = slash;
      bangHistHandled = bangHist;
    },
  };
}

test('Shift+Enter auto-switches to temporary multiline mode, Enter inserts newline, Ctrl+Enter sends and resets', () => {
  const h = createHarness({ platform: 'Win32', table: en });
  assert.equal(h.ctx.effectiveInputMode(), 'enter');
  assert.equal(h.csend.classList.contains('is-multiline'), false);
  assert.equal(h.csendKbd.hidden, true);
  assert.equal(h.csendRevert.hidden, true);

  // Shift+Enter switches to temporary multiline mode and inserts a newline via editText().
  const se = h.keydown({ key: 'Enter', shiftKey: true });
  assert.equal(se.defaultPrevented, true);
  assert.equal(h.sent.length, 0);
  assert.equal(h.ctx.effectiveInputMode(), 'mod');
  assert.equal(h.csend.classList.contains('is-multiline'), true);
  assert.equal(h.csendKbd.hidden, false);
  assert.equal(h.csendKbd.textContent, 'Ctrl+↵');
  assert.equal(h.csendRevert.hidden, false);
  assert.equal(h.csend.title, 'Send (Ctrl+Enter)');
  assert.equal(h.csend.getAttribute('aria-label'), 'Send (Ctrl+Enter)');
  assert.equal(h.ctx.bangSendKeyLabel(), 'Ctrl/Cmd+Enter');
  assert.equal(h.histEmptyHintEl.textContent, en['js.cexec.hist.empty.mod']);

  // Simulate newline inserted by browser.
  h.setValueAndInput('line 1\n');
  assert.equal(h.ctx.effectiveInputMode(), 'mod');

  // Plain Enter now inserts a newline via editText() instead of sending.
  const e1 = h.keydown({ key: 'Enter' });
  assert.equal(e1.defaultPrevented, true);
  assert.equal(h.ctext.value, 'line 1\n\n');
  assert.equal(h.sent.length, 0);

  // Ctrl+Enter sends and resets back to enter mode.
  h.setValueAndInput('line 1\nline 2');
  const e2 = h.keydown({ key: 'Enter', ctrlKey: true });
  assert.equal(e2.defaultPrevented, true);
  assert.deepEqual(h.sent, ['line 1\nline 2']);
  assert.equal(h.ctx.effectiveInputMode(), 'enter');
  assert.equal(h.csend.classList.contains('is-multiline'), false);
  assert.equal(h.csendKbd.hidden, true);
  assert.equal(h.csendRevert.hidden, true);
  assert.equal(h.csend.title, en['composer.send']);
  assert.equal(h.ctx.bangSendKeyLabel(), 'Enter');
  assert.equal(h.histEmptyHintEl.textContent, en['js.cexec.hist.empty.enter']);
});

test('macOS shows Cmd micro-badge and Cmd+Enter sends and resets', () => {
  const h = createHarness({ platform: 'MacIntel', table: ko });
  h.keydown({ key: 'Enter', shiftKey: true });
  assert.equal(h.csendKbd.textContent, '⌘+↵');
  assert.equal(h.csend.title, '보내기 (⌘+Enter)');
  assert.equal(h.csend.getAttribute('aria-label'), '보내기 (⌘+Enter)');

  h.setValueAndInput('첫 줄\n둘째 줄');
  const e = h.keydown({ key: 'Enter', metaKey: true });
  assert.equal(e.defaultPrevented, true);
  assert.deepEqual(h.sent, ['첫 줄\n둘째 줄']);
  assert.equal(h.ctx.effectiveInputMode(), 'enter');
  assert.equal(h.csend.title, ko['composer.send']);
});

test('multiline paste requires internal newline, stray edge newlines stay single-line, deleting all newlines auto-exits', () => {
  const h = createHarness();

  // Stray trailing/leading newlines from single-line clipboard pastes must NOT enter autoMultiline.
  for (const stray of ['git status\n', '\ngit status', '\ngit status\n', '\n', '\n\n']) {
    h.setValueAndInput(stray);
    assert.equal(h.ctx.effectiveInputMode(), 'enter', 'stray edge newline stays single-line: ' + JSON.stringify(stray));
    assert.equal(h.csend.classList.contains('is-multiline'), false);
  }

  // Real internal multi-line content enters autoMultiline.
  h.setValueAndInput('first\nsecond');
  assert.equal(h.ctx.effectiveInputMode(), 'mod');
  assert.equal(h.csend.classList.contains('is-multiline'), true);

  // Deleting all newlines back to a single non-empty line auto-exits multiline mode.
  h.setValueAndInput('first');
  assert.equal(h.ctx.effectiveInputMode(), 'enter');
  assert.equal(h.csend.classList.contains('is-multiline'), false);
  assert.equal(h.csendKbd.hidden, true);
  assert.equal(h.csendRevert.hidden, true);

  // Explicit Shift+Enter still enters autoMultiline immediately and stays on with trailing "\n",
  // then auto-exits once the newline is deleted.
  h.keydown({ key: 'Enter', shiftKey: true });
  assert.equal(h.ctx.effectiveInputMode(), 'mod');
  h.setValueAndInput('first\n');
  assert.equal(h.ctx.effectiveInputMode(), 'mod');
  h.setValueAndInput('first');
  assert.equal(h.ctx.effectiveInputMode(), 'enter');

  // Clearing the input completely also stays in enter mode.
  h.setValueAndInput('');
  assert.equal(h.ctx.effectiveInputMode(), 'enter');
  assert.equal(h.csend.classList.contains('is-multiline'), false);
  assert.equal(h.csendKbd.hidden, true);
  assert.equal(h.csendRevert.hidden, true);
});

test('revert × stays reverted while newlines remain, then clears once all newlines are deleted', () => {
  const h = createHarness();
  h.setValueAndInput('line 1\nline 2');
  assert.equal(h.ctx.effectiveInputMode(), 'mod');

  h.csendRevert.dispatch('click');
  assert.equal(h.ctx.effectiveInputMode(), 'enter');
  h.setValueAndInput('line 1\nline 2!');
  assert.equal(h.ctx.effectiveInputMode(), 'enter', 'editing reverted multiline draft stays in enter mode');

  // Deleting all newlines clears autoMultilineReverted so a later multiline paste can re-enter.
  h.setValueAndInput('line 1 line 2!');
  assert.equal(h.ctx.effectiveInputMode(), 'enter');
  h.setValueAndInput('a\nb');
  assert.equal(h.ctx.effectiveInputMode(), 'mod', 're-enters autoMultiline after newlines were cleared');
});

test('Korean/CJK IME Enter beforeinput guard blocks stray newlines in enter mode without breaking Hangul commit, Shift+Enter, or mod mode', () => {
  const h = createHarness();

  // 1. Korean IME composing Enter in single-line "enter" mode:
  //    keydown is bypassed (isComposing / keyCode 229), Hangul commit (insertCompositionText) is allowed,
  //    and native insertParagraph / insertLineBreak is prevented.
  const kd = h.keydown({ key: 'Process', code: 'Enter', keyCode: 229, isComposing: true });
  assert.equal(kd.defaultPrevented, false);
  assert.equal(h.sent.length, 0);
  const commitEv = h.beforeinput({ inputType: 'insertCompositionText', data: '녕', isComposing: true });
  assert.equal(commitEv.defaultPrevented, false, 'Hangul syllable commit must not be blocked');
  const paraEv = h.beforeinput({ inputType: 'insertParagraph', isComposing: false });
  assert.equal(paraEv.defaultPrevented, true, 'stray insertParagraph after IME Enter must be blocked in enter mode');
  const brEv = h.beforeinput({ inputType: 'insertLineBreak', isComposing: false });
  assert.equal(brEv.defaultPrevented, true, 'stray insertLineBreak in enter mode must be blocked');
  assert.equal(h.ctx.effectiveInputMode(), 'enter');

  // 2. Shift+Enter (including during IME composition) allows native insertLineBreak / insertParagraph.
  h.keydown({ key: 'Process', code: 'Enter', keyCode: 229, isComposing: true, shiftKey: true });
  const shiftBr = h.beforeinput({ inputType: 'insertLineBreak', isComposing: false });
  assert.equal(shiftBr.defaultPrevented, false, 'Shift+Enter must still allow line break');

  // 3. Alt+Enter also allows line break.
  h.keydown({ key: 'Enter', code: 'Enter', altKey: true });
  const altBr = h.beforeinput({ inputType: 'insertLineBreak', isComposing: false });
  assert.equal(altBr.defaultPrevented, false, 'Alt+Enter must still allow line break');

  // 4. Plain Enter when already in autoMultiline ("mod") mode allows insertParagraph / insertLineBreak.
  h.keydown({ key: 'Enter', shiftKey: true });
  assert.equal(h.ctx.effectiveInputMode(), 'mod');
  h.keydown({ key: 'Enter', code: 'Enter' });
  const modPara = h.beforeinput({ inputType: 'insertParagraph', isComposing: false });
  assert.equal(modPara.defaultPrevented, false, 'multiline mode must allow native insertParagraph');

  // 5. Global "mod" inputMode also allows insertParagraph / insertLineBreak.
  const hMod = createHarness({ initialInputMode: 'mod' });
  hMod.keydown({ key: 'Process', code: 'Enter', keyCode: 229, isComposing: true });
  assert.equal(hMod.beforeinput({ inputType: 'insertParagraph' }).defaultPrevented, false);
});

test('clicking revert × immediately reverts to enter mode and refocuses without sending', () => {
  const h = createHarness();
  h.setValueAndInput('line 1\nline 2');
  assert.equal(h.ctx.effectiveInputMode(), 'mod');

  h.csendRevert.dispatch('click');
  assert.equal(h.ctx.effectiveInputMode(), 'enter');
  assert.equal(h.csend.classList.contains('is-multiline'), false);
  assert.equal(h.csendRevert.hidden, true);
  assert.equal(h.sent.length, 0);
  assert.equal(h.ctext.focused, 1);

  // Editing the reverted multiline draft does not bounce back into multiline mode.
  h.setValueAndInput('line 1\nline 2!');
  assert.equal(h.ctx.effectiveInputMode(), 'enter');

  // Plain Enter now sends the draft.
  const e = h.keydown({ key: 'Enter' });
  assert.equal(e.defaultPrevented, true);
  assert.deepEqual(h.sent, ['line 1\nline 2!']);

  // Shift+Enter after a manual revert explicitly re-enters multiline mode.
  h.setValueAndInput('a\nb');
  h.csendRevert.dispatch('click');
  assert.equal(h.ctx.effectiveInputMode(), 'enter');
  h.keydown({ key: 'Enter', shiftKey: true });
  assert.equal(h.ctx.effectiveInputMode(), 'mod');
});

test('IME composition and #/@///! popup handlers prevent Shift+Enter auto-switch', () => {
  const h = createHarness();

  // IME isComposing
  h.keydown({ key: 'Enter', shiftKey: true, isComposing: true });
  assert.equal(h.ctx.effectiveInputMode(), 'enter');

  // Safari keyCode 229
  h.keydown({ key: 'Enter', shiftKey: true, keyCode: 229 });
  assert.equal(h.ctx.effectiveInputMode(), 'enter');

  // Mention / slash / bang-history popup handlers
  for (const popup of [{ mention: true }, { slash: true }, { bangHist: true }]) {
    h.setPopup(popup);
    h.keydown({ key: 'Enter', shiftKey: true });
    assert.equal(h.ctx.effectiveInputMode(), 'enter');
  }
  h.setPopup({});
  h.keydown({ key: 'Enter', shiftKey: true });
  assert.equal(h.ctx.effectiveInputMode(), 'mod');
});

test('global mod inputMode never shows temporary badge or overrides global behavior', () => {
  const h = createHarness({ initialInputMode: 'mod' });
  assert.equal(h.ctx.effectiveInputMode(), 'mod');
  assert.equal(h.csend.classList.contains('is-multiline'), false);
  assert.equal(h.csendKbd.hidden, true);
  assert.equal(h.csendRevert.hidden, true);
  assert.equal(h.csend.title, 'Send (Ctrl+Enter)');

  h.keydown({ key: 'Enter', shiftKey: true });
  h.setValueAndInput('a\nb');
  assert.equal(h.csend.classList.contains('is-multiline'), false);
  assert.equal(h.csendKbd.hidden, true);
  assert.equal(h.csendRevert.hidden, true);

  // Switching to global mod while temporary multiline is active clears the badge.
  h.ctx.setInput('enter');
  h.setValueAndInput('a\nb');
  assert.equal(h.csend.classList.contains('is-multiline'), true);
  h.ctx.setInput('mod');
  assert.equal(h.csend.classList.contains('is-multiline'), false);
  assert.equal(h.csendKbd.hidden, true);
  assert.equal(h.csendRevert.hidden, true);
});

test('production sendMessage resets temporary multiline state independently without input event', () => {
  const h = createHarness({ platform: 'Win32', table: en });
  h.keydown({ key: 'Enter', shiftKey: true });
  h.setValueAndInput('line 1\nline 2');
  assert.equal(h.ctx.effectiveInputMode(), 'mod');
  assert.equal(h.csend.classList.contains('is-multiline'), true);
  assert.equal(h.csendKbd.hidden, false);
  assert.equal(h.csendRevert.hidden, false);

  // Exercise production sendMessage() directly (as clicking #csend does).
  h.ctx.sendMessage();
  assert.deepEqual(h.sent, ['line 1\nline 2']);
  assert.equal(h.ctext.value, '');
  assert.equal(h.ctx.effectiveInputMode(), 'enter', 'production sendMessage must reset temporary multiline mode');
  assert.equal(h.csend.classList.contains('is-multiline'), false, 'production sendMessage must clear .is-multiline');
  assert.equal(h.csendKbd.hidden, true, 'production sendMessage must hide micro-badge');
  assert.equal(h.csendRevert.hidden, true, 'production sendMessage must hide revert button');
  assert.equal(h.csend.title, en['composer.send']);
});

test('production cexecAcknowledged resets temporary multiline state when emptying draft and when remaining draft is single-line or multiline', () => {
  // Case 1: acknowledgement empties the entire composer draft.
  const h1 = createHarness({ platform: 'Win32', table: en });
  h1.keydown({ key: 'Enter', shiftKey: true });
  h1.setValueAndInput('!echo one\ntwo');
  assert.equal(h1.ctx.effectiveInputMode(), 'mod');
  assert.equal(h1.csend.classList.contains('is-multiline'), true);

  h1.ctx.cexecAcknowledged('!echo one\ntwo', { intact: true });
  assert.equal(h1.ctext.value, '');
  assert.equal(h1.ctx.effectiveInputMode(), 'enter', 'cexecAcknowledged (empty remainder) must reset temporary multiline mode');
  assert.equal(h1.csend.classList.contains('is-multiline'), false);
  assert.equal(h1.csendKbd.hidden, true);
  assert.equal(h1.csendRevert.hidden, true);

  // Case 2: acknowledgement leaves remaining single-line draft text typed while exec was in flight.
  const h2 = createHarness({ platform: 'Win32', table: en });
  h2.keydown({ key: 'Enter', shiftKey: true });
  h2.setValueAndInput('!echo one\ntwonext single line');
  assert.equal(h2.ctx.effectiveInputMode(), 'mod');
  assert.equal(h2.csend.classList.contains('is-multiline'), true);

  h2.ctx.cexecAcknowledged('!echo one\ntwo', { intact: true });
  assert.equal(h2.ctext.value, 'next single line');
  assert.equal(h2.ctx.effectiveInputMode(), 'enter', 'cexecAcknowledged (single-line remainder) must reset temporary multiline mode');
  assert.equal(h2.csend.classList.contains('is-multiline'), false);
  assert.equal(h2.csendKbd.hidden, true);
  assert.equal(h2.csendRevert.hidden, true);

  // Case 3: acknowledgement leaves remaining multiline draft text -> resets then re-syncs multiline mode.
  const h3 = createHarness({ platform: 'Win32', table: en });
  h3.keydown({ key: 'Enter', shiftKey: true });
  h3.setValueAndInput('!echo one\ntwoline A\nline B');
  h3.ctx.cexecAcknowledged('!echo one\ntwo', { intact: true });
  assert.equal(h3.ctext.value, 'line A\nline B');
  assert.equal(h3.ctx.effectiveInputMode(), 'mod', 'cexecAcknowledged (multiline remainder) re-syncs multiline mode');
  assert.equal(h3.csend.classList.contains('is-multiline'), true);
});

test('programmatic submitCommand clear path resets temporary multiline state', () => {
  assert.match(
    dashboardJS,
    /if \(clearText\) \{ ctext\.value = ""; grow\(\); clearMentions\(\); if \(typeof resetAutoMultiline === "function"\) resetAutoMultiline\(\); \}/,
    'submitCommand resets temporary multiline state'
  );
});

test('template and CSS follow Variant B without touching .cfoot or nesting buttons', () => {
  assert.match(
    dashboardTmpl,
    /<div class="csend-wrap" id="csend-wrap">(?:<button type="button" class="cvox" id="cvox"[^>]*>\{\{icon "mic"\}\}<\/button>)?<button type="button" class="csend" id="csend"[^>]*>\{\{icon "send"\}\}<span class="csend-kbd" id="csend-kbd" aria-hidden="true" hidden><\/span><\/button><button type="button" class="csend-revert" id="csend-revert"[^>]*hidden>×<\/button><\/div>/,
    'Variant B wrapper keeps #csend-revert as a sibling button'
  );
  assert.match(
    dashboardTmpl,
    /<div class="cfoot"\{\{if not \(or \.Hosted \.IsChild\)\}\} data-cfoot-empty="1" hidden\{\{end\}\}>(?:<span class="cvox-slot" id="cvox-slot" data-voice-slot="bottom-left" hidden>.*?<\/span>)?\{\{if or \.Hosted \.IsChild\}\}\{\{template "composerpills" \.\}\}\{\{end\}\}\{\{region \. "sline"\}\}(?:\{\{if \.WhiteboardCapable\}\}<button type="button" class="wbonly-btn" id="wbonly"[^>]*><span class="wbonly-dot" aria-hidden="true"><\/span><span>\{\{t "composer\.wbonly"\}\}<\/span><\/button>\{\{end\}\})?<\/div>/,
    '.cfoot keeps hosted pills, sline, voice slot, and optional whiteboard-only toggle without wbonly-hint'
  );
  assert.match(dashboardCSS, /\.csend\.is-multiline \{[^}]*width: 48px;[^}]*flex-direction: column;/);
  assert.match(dashboardCSS, /\.csend-revert \{[^}]*position: absolute;/);
});
