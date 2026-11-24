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

function makeElement(id) {
  const classes = new Set();
  const attrs = Object.create(null);
  const listeners = Object.create(null);
  return {
    id,
    value: '',
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
    sendMessage() {
      sent.push(ctext.value);
      ctext.value = '';
      if (typeof ctx.resetAutoMultiline === 'function') ctx.resetAutoMultiline();
    },
  };

  vm.createContext(ctx);
  vm.runInContext(
    inputBlock + '\n' +
    bangKeyBlock + '\n' +
    bangSyncModeBlock + '\n' +
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

  function setValueAndInput(v) {
    ctext.value = v;
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

  // Shift+Enter switches to temporary multiline mode without preventing newline.
  const se = h.keydown({ key: 'Enter', shiftKey: true });
  assert.equal(se.defaultPrevented, false);
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

  // Plain Enter now inserts a newline instead of sending.
  const e1 = h.keydown({ key: 'Enter' });
  assert.equal(e1.defaultPrevented, false);
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

test('multiline paste auto-switches, non-empty 1-line edit does not flicker, empty input resets', () => {
  const h = createHarness();
  h.setValueAndInput('first\nsecond');
  assert.equal(h.ctx.effectiveInputMode(), 'mod');
  assert.equal(h.csend.classList.contains('is-multiline'), true);

  // Deleting back to 1 non-empty line keeps multiline mode (no flicker).
  h.setValueAndInput('first');
  assert.equal(h.ctx.effectiveInputMode(), 'mod');
  assert.equal(h.csend.classList.contains('is-multiline'), true);

  // Clearing the input completely resets back to enter mode.
  h.setValueAndInput('');
  assert.equal(h.ctx.effectiveInputMode(), 'enter');
  assert.equal(h.csend.classList.contains('is-multiline'), false);
  assert.equal(h.csendKbd.hidden, true);
  assert.equal(h.csendRevert.hidden, true);
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

test('programmatic send/clear paths reset temporary multiline state without input event', () => {
  // Verify production source explicitly calls resetAutoMultiline on all direct ctext.value = "" paths.
  assert.match(
    dashboardJS,
    /ctext\.value = ""; grow\(\); clearMentions\(\);\s*if \(typeof resetAutoMultiline === "function"\) resetAutoMultiline\(\);/,
    'sendMessage and cexecAcknowledged reset temporary multiline state'
  );
  assert.match(
    dashboardJS,
    /if \(clearText\) \{ ctext\.value = ""; grow\(\); clearMentions\(\); if \(typeof resetAutoMultiline === "function"\) resetAutoMultiline\(\); \}/,
    'submitCommand resets temporary multiline state'
  );
});

test('template and CSS follow Variant B without touching .cfoot or nesting buttons', () => {
  assert.match(
    dashboardTmpl,
    /<div class="csend-wrap" id="csend-wrap"><button type="button" class="csend" id="csend"[^>]*>\{\{icon "send"\}\}<span class="csend-kbd" id="csend-kbd" aria-hidden="true" hidden><\/span><\/button><button type="button" class="csend-revert" id="csend-revert"[^>]*hidden>×<\/button><\/div>/,
    'Variant B wrapper keeps #csend-revert as a sibling button'
  );
  assert.match(
    dashboardTmpl,
    /<div class="cfoot">\{\{if or \.Hosted \.IsChild\}\}\{\{template "composerpills" \.\}\}\{\{end\}\}\{\{region \. "sline"\}\}<\/div>/,
    '.cfoot bottom-right remains untouched'
  );
  assert.match(dashboardCSS, /\.csend\.is-multiline \{[^}]*width: 48px;[^}]*flex-direction: column;/);
  assert.match(dashboardCSS, /\.csend-revert \{[^}]*position: absolute;/);
});
