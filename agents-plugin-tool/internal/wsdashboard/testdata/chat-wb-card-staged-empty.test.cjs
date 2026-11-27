'use strict';

// Offline Node contract for live-UX staged-empty state and zero-rescan
// performance on compact chat whiteboard item capsules (chat-wb-card-staged-empty).
const assert = require('node:assert/strict');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

const jsSource = readAssetSource('wsdashboard', 'dashboard.js');
const cssSource = readAssetSource('wsdashboard', 'dashboard-conversation.css');

function extractBlock(src, re, label) {
  const m = src.match(re);
  assert(m, 'missing expected block: ' + label);
  return m[0];
}

function extractBridgeFunctions(src, names) {
  return names.map((name) => {
    const single = src.match(new RegExp('\\n  function ' + name + '\\([^)]*\\) \\{[^\\n]*\\}\\n'));
    if (single) return single[0];
    const multi = src.match(new RegExp('(?:^|\\n)  function ' + name + '\\([^)]*\\) \\{\\n[\\s\\S]*?\\n  \\}\\n'));
    assert(multi, 'missing production function: ' + name);
    return multi[0];
  }).join('\n');
}

// --- 1. CSS contract: data-wb-empty="1" shares settled empty rules + displays .wbupdate-cap-val ---
{
  for (const sel of [
    '.wbupdate-qitem[data-wb-empty="1"] .wbupdate-cap,',
    '.wbupdate-qitem[data-wb-empty="1"] a.wbupdate-cap:hover,',
    '.wbupdate-qitem[data-wb-empty="1"] .wbupdate-cap-id,',
    '.wbupdate-qitem[data-wb-empty="1"] .wbupdate-cap-cls,',
    '.wbupdate-qitem[data-wb-empty="1"] .wbupdate-cap-ttl,',
    '.wbupdate-qitem[data-wb-empty="1"] a.wbupdate-cap:hover .wbupdate-cap-ttl,',
    '.wbupdate-cap-val {',
    '.wbupdate-cap-val:empty { display: none; }',
    '.wbupdate-cap-val[data-wb-pick="ok"]',
    '.wbupdate-cap-val[data-wb-pick="no"]',
  ]) {
    assert(cssSource.includes(sel), 'dashboard-conversation.css missing selector: ' + sel);
  }
  assert(cssSource.includes('.wbupdate-qitem[data-wb-state="settled"] .wbupdate-cap { background: transparent;'));
  assert(cssSource.includes('.wbupdate-qitem[data-wb-state="settled"] .wbupdate-cap-cls { background: transparent;'));
  assert(cssSource.includes('.wbupdate-qitem[data-wb-state="settled"] .wbupdate-cap-mark { display: inline-flex; }'));
}

// --- 2. Minimal DOM harness with scan counters ---
function createHarness() {
  const counters = {
    boardQueryAll: 0,
    boardQueryOne: 0,
    entriesQueryAll: 0,
    virtualForEach: 0,
    sentDataQuery: 0,
    setNoCalls: 0,
    syncWbCalls: 0,
  };

  function makeEl(tag, opts) {
    opts = opts || {};
    const attrs = Object.assign({}, opts.attrs || {});
    let classes = new Set((opts.className || '').split(/\s+/).filter(Boolean));
    let _text = opts.text || '';
    const listeners = {};
    const el = {
      nodeType: 1,
      tagName: tag.toUpperCase(),
      attrs,
      childNodes: [],
      parentNode: null,
      id: attrs.id || '',
      hidden: !!opts.hidden,
      checked: !!opts.checked,
      value: opts.value || '',
      dispatched: [],
      get children() { return el.childNodes.filter((c) => c.nodeType === 1); },
      get firstChild() { return el.childNodes[0] || null; },
      get textContent() {
        if (el.childNodes.length === 0) return _text;
        return el.childNodes.map((c) => c.nodeType === 3 ? c.nodeValue : c.textContent).join('');
      },
      set textContent(v) {
        _text = String(v || '');
        el.childNodes = [];
        if (_text) el.appendChild({ nodeType: 3, nodeValue: _text, parentNode: el });
      },
      get className() { return Array.from(classes).join(' '); },
      set className(v) { classes = new Set(String(v || '').split(/\s+/).filter(Boolean)); },
      classList: {
        contains: (c) => classes.has(c),
        add: (...cs) => cs.forEach((c) => classes.add(c)),
        remove: (...cs) => cs.forEach((c) => classes.delete(c)),
        toggle: (c, on) => {
          const next = on === undefined ? !classes.has(c) : !!on;
          if (next) classes.add(c); else classes.delete(c);
          return next;
        },
      },
      getAttribute: (k) => (Object.prototype.hasOwnProperty.call(attrs, k) ? attrs[k] : null),
      setAttribute: (k, v) => { attrs[k] = String(v); if (k === 'id') el.id = String(v); },
      removeAttribute: (k) => { delete attrs[k]; if (k === 'id') el.id = ''; },
      hasAttribute: (k) => Object.prototype.hasOwnProperty.call(attrs, k),
      appendChild(c) {
        if (c.parentNode) c.parentNode.removeChild(c);
        c.parentNode = el;
        el.childNodes.push(c);
        return c;
      },
      insertBefore(c, ref) {
        if (c.parentNode) c.parentNode.removeChild(c);
        c.parentNode = el;
        const i = ref ? el.childNodes.indexOf(ref) : -1;
        if (i < 0) el.childNodes.push(c); else el.childNodes.splice(i, 0, c);
        return c;
      },
      removeChild(c) {
        const i = el.childNodes.indexOf(c);
        if (i >= 0) { el.childNodes.splice(i, 1); c.parentNode = null; }
        return c;
      },
      contains(other) {
        for (let cur = other; cur; cur = cur.parentNode) if (cur === el) return true;
        return false;
      },
      matches: (sel) => matchSelector(el, sel),
      closest: (sel) => {
        for (let cur = el; cur; cur = cur.parentNode) if (cur.nodeType === 1 && matchSelector(cur, sel)) return cur;
        return null;
      },
      querySelector(sel) {
        if (el === board) counters.boardQueryOne++;
        return queryAll(el, sel)[0] || null;
      },
      querySelectorAll(sel) {
        if (el === board) counters.boardQueryAll++;
        if (el === entries) counters.entriesQueryAll++;
        return queryAll(el, sel);
      },
      addEventListener(type, fn) { (listeners[type] = listeners[type] || []).push(fn); },
      dispatchEvent(ev) {
        ev.target = ev.target || el;
        el.dispatched.push(ev);
        (listeners[ev.type] || []).forEach((fn) => fn(ev));
        if (ev.bubbles !== false) (docListeners[ev.type] || []).forEach((fn) => fn(ev));
        return true;
      },
    };
    if (opts.text) el.textContent = opts.text;
    (opts.children || []).forEach((c) => el.appendChild(c));
    return el;
  }

  function matchSingle(el, part) {
    part = part.trim();
    if (!part) return false;
    const notMatch = part.match(/:not\(\[([^\]]+)\]\)/);
    if (notMatch) {
      if (el.hasAttribute(notMatch[1])) return false;
      part = part.replace(notMatch[0], '');
    }
    const idMatch = part.match(/#([a-zA-Z0-9_-]+)/);
    if (idMatch) {
      if (el.id !== idMatch[1] && el.getAttribute('id') !== idMatch[1]) return false;
      part = part.replace(idMatch[0], '');
    }
    const tagMatch = part.match(/^[a-zA-Z]+/);
    if (tagMatch) {
      if (el.tagName !== tagMatch[0].toUpperCase()) return false;
      part = part.slice(tagMatch[0].length);
    }
    const attrRe = /\[([a-zA-Z0-9_-]+)(?:="([^"]*)")?\]/g;
    let m;
    while ((m = attrRe.exec(part))) {
      if (!el.hasAttribute(m[1])) return false;
      if (m[2] !== undefined && el.getAttribute(m[1]) !== m[2]) return false;
    }
    part = part.replace(/\[[^\]]+\]/g, '');
    const clsRe = /\.([a-zA-Z0-9_-]+)/g;
    while ((m = clsRe.exec(part))) {
      if (!el.classList.contains(m[1])) return false;
    }
    return true;
  }

  function matchCompound(el, sel) {
    const parts = sel.trim().split(/\s*>\s*|\s+/).filter(Boolean);
    if (!parts.length || !matchSingle(el, parts[parts.length - 1])) return false;
    let cur = el.parentNode;
    for (let i = parts.length - 2; i >= 0; i--) {
      let ok = false;
      while (cur) {
        if (cur.nodeType === 1 && matchSingle(cur, parts[i])) { ok = true; cur = cur.parentNode; break; }
        cur = cur.parentNode;
      }
      if (!ok) return false;
    }
    return true;
  }

  function matchSelector(el, sel) {
    return sel.split(',').some((s) => matchCompound(el, s));
  }

  function queryAll(root, sel) {
    const out = [];
    (function walk(n) {
      for (const c of n.children) {
        if (matchSelector(c, sel)) out.push(c);
        walk(c);
      }
    })(root);
    return out;
  }

  const board = makeEl('div', { attrs: { id: 'r-board' } });
  const entries = makeEl('div', { attrs: { id: 'r-entries' } });
  const cchips = makeEl('div', { attrs: { id: 'cchips' } });
  const ctext = makeEl('textarea', { attrs: { id: 'ctext' } });
  const docListeners = {};

  // Virtualized far-away topics (instrumented to count scans).
  const rawStore = new Map();
  const virtualStore = {
    get size() { return rawStore.size; },
    get: (k) => rawStore.get(k),
    set: (k, v) => rawStore.set(k, v),
    delete: (k) => rawStore.delete(k),
    forEach(cb, thisArg) {
      counters.virtualForEach++;
      return rawStore.forEach(cb, thisArg);
    },
  };

  let fakeInlineEditor = null;
  const doc = {
    activeElement: null,
    createElement: (tag) => makeEl(tag),
    createTextNode: (v) => ({ nodeType: 3, nodeValue: String(v || ''), parentNode: null }),
    querySelectorAll: (sel) => {
      if (sel.includes('.sentdata')) { counters.sentDataQuery++; return []; }
      return entries.querySelectorAll(sel);
    },
    addEventListener: (type, fn) => { (docListeners[type] = docListeners[type] || []).push(fn); },
  };

  const wbUpdateSrc = extractBridgeFunctions(jsSource, [
    'wbUpdateNormID',
    'wbUpdateItemID',
    'wbUpdateFlattenMemo',
    'wbUpdateIsHeld',
    'wbUpdateClassFromEl',
    'wbUpdateTitleFromEl',
    'wbUpdateSettledSummary',
    'wbUpdateQueryBoard',
    'wbUpdateChipMeta',
    'wbUpdateOnBoard',
    'wbUpdateStagedChip',
    'wbUpdateReadOpenPickMemo',
    'readBoardQuestionState',
    'hydrateWbUpdateChip',
    'hydrateWbUpdateGroup',
    'hydrateWbUpdateItems',
    'syncWbUpdateItems',
  ]);
  const composerSrc = jsSource.slice(jsSource.indexOf('  // --- composer:'), jsSource.indexOf('  function selectionNote('));
  const writeAnswersSrc = extractBlock(jsSource, /\n  function pickKey[\s\S]*?\n  \/\/ A board swap waits while the user is typing/, 'writeAnswers');
  const applySentSrc = extractBlock(jsSource, /\n  function applySent\(force\) \{\n[\s\S]*?\n  \}\n/, 'applySent');
  const setCheckNoSrc = extractBlock(jsSource, /\n  function setCheckNo\(item, on\) \{\n[\s\S]*?\n  \}\n/, 'setCheckNo');
  const actionsClick = extractBlock(jsSource, /\n    if \(cchips && inBoard\(t\)\) syncChips\(t\);\n[\s\S]*?\n      return;\n    \}\n/, 'actions click handler');
  const kbInputChange = extractBlock(jsSource, /\n    \["input", "change"\]\.forEach\(function \(type\) \{\n      doc\.addEventListener\(type, function \(e\) \{\n[\s\S]*?followImgs\(\);\n      \}\);\n    \}\);\n/, 'keyboard input/change');

  const ctx = vm.createContext({
    doc,
    Object,
    Array,
    Map,
    Set,
    Promise,
    Error,
    Event: function (type) { this.type = type; },
    TextEncoder,
    WB_QCAP_CLASSES: {
      policy: 'js.wbupdate.cls.policy',
      technical: 'js.wbupdate.cls.technical',
      announced: 'js.wbupdate.cls.announced',
      check: 'js.wbupdate.cls.check',
    },
    wsT: (k, a) => (a === undefined ? k : k + ':' + a),
    window: { wbPill: { isHeld: (el) => el.classList.contains('wb-held') } },
    virtualStore,
    cimgs: null,
    sentSig: '',
    $: (id) => (id === 'cchips' ? cchips : id === 'ctext' ? ctext : null),
    q: (sel, root) => (root || doc).querySelectorAll(sel),
    region: (name) => (name === 'board' ? board : null),
    entriesBox: () => entries,
    inBoard: (n) => board.contains(n),
    boardQ: (sel) => {
      counters.boardQueryAll++;
      return queryAll(board, sel);
    },
    openNote: (note, s) => {
      note.value = s.note || '';
      if (s.noteOpen) note.hidden = false;
    },
    queueDraft() {},
    effectJump() {},
    syncMarkers() {},
    followImgs() {},
    selectionNote() {},
    clearTopicNote() {},
    topicNotes() { return []; },
    topicNote() { return null; },
    setFakeInlineEditor(ed) { fakeInlineEditor = ed; ctx.window.WsCapsules = ed ? { fieldFor: () => ed } : null; },
  });

  // Override inlineEditor helper hook after running composerSrc so wsc:change + putChip/dropChip work with fakeInlineEditor.
  vm.runInContext(
    setCheckNoSrc +
    '\nfunction topicNotes() { return []; }\n' +
    composerSrc.replace('function setCheckNo', 'function _unusedSetCheckNo') +
    writeAnswersSrc +
    applySentSrc +
    wbUpdateSrc +
    '\nfunction inlineEditor() { return __getInline(); }\n' +
    '\nfunction inlineRows(kind) { var ed = inlineEditor(); return ed ? ed.rows(kind) : []; }\n' +
    '\nfunction handleBoardClick(t) {' + actionsClick + '\n}\n' +
    '\nvar __origSyncWb = syncWbUpdateItems; syncWbUpdateItems = function () { __countSync(); return __origSyncWb.apply(this, arguments); };\n' +
    kbInputChange,
    Object.assign(ctx, { __countSync: () => { counters.syncWbCalls = (counters.syncWbCalls || 0) + 1; } })
  );
  ctx.__getInline = () => fakeInlineEditor;

  function resetCounters() {
    for (const k of Object.keys(counters)) counters[k] = 0;
  }

  return { makeEl, board, entries, cchips, ctext, virtualStore, counters, resetCounters, ctx, docListeners };
}

// --- 3. Populate realistic multi-topic board + virtualStore + chat cards and run all 5 UX + perf checks ---
{
  const h = createHarness();
  const { makeEl, board, entries, cchips, virtualStore, counters, resetCounters, ctx, docListeners } = h;

  function makePolicyCard(idPad, title, opts, noteVal) {
    const noteEl = makeEl('textarea', { className: 'note', value: noteVal || '', hidden: !noteVal });
    const btns = opts.map((opt) => makeEl('button', {
      className: 'opt',
      attrs: { 'data-opt': opt, 'aria-pressed': 'false' },
    }));
    return makeEl('div', {
      className: 'card policy',
      attrs: { 'data-id': idPad },
      children: [
        makeEl('div', { className: 'head', children: [makeEl('span', { className: 'title', text: title })] }),
        ...btns,
        noteEl,
      ],
    });
  }

  function makeCheckRow(idPad, title) {
    const box = makeEl('input', { attrs: { type: 'checkbox' }, checked: false });
    const lbl = makeEl('label', { className: 'ck', children: [box] });
    const noBtn = makeEl('button', { className: 'no-toggle', attrs: { 'aria-pressed': 'false' } });
    const noteEl = makeEl('textarea', { className: 'note', value: '', hidden: true });
    const row = makeEl('div', {
      className: 'ckrow',
      attrs: { 'data-id': idPad },
      children: [
        makeEl('div', { className: 'head', children: [makeEl('span', { className: 'title', text: title })] }),
        lbl,
        noBtn,
        noteEl,
      ],
    });
    return { row, box, noBtn, noteEl };
  }

  function makeAckRow(idPad, title) {
    const box = makeEl('input', { attrs: { type: 'checkbox' }, checked: false });
    const lbl = makeEl('label', { className: 'ack', children: [box] });
    const noteEl = makeEl('textarea', { className: 'note', value: '', hidden: true });
    const row = makeEl('div', {
      className: 'arow',
      attrs: { 'data-id': idPad },
      children: [
        makeEl('div', { className: 'head', children: [makeEl('span', { className: 'title', text: title })] }),
        makeEl('span', { className: 'tag announced' }),
        lbl,
        noteEl,
      ],
    });
    return { row, box, noteEl };
  }

  // Board: topic-1 has policy item 01, check item 04, ack item 06, plus settled item 05;
  // plus 10 unrelated items in other visible topics + 25 topics in virtualStore.
  const card01 = makePolicyCard('01', 'Policy question 1', ['a', 'b']);
  const ck04 = makeCheckRow('04', 'Check question 4');
  const ack06 = makeAckRow('06', 'Ack notice 6');
  const settled05 = makeEl('div', {
    className: 'wb-hitem answered',
    attrs: { 'data-item': '5', 'data-class': 'policy', 'data-state': 'settled' },
    children: [
      makeEl('div', { className: 'wb-hmain', children: [makeEl('b', { text: 'Already settled 5' })] }),
      makeEl('div', { className: 'wb-hstatus', text: 'B · chosen earlier' }),
    ],
  });
  const topic1 = makeEl('section', {
    className: 'topic',
    attrs: { 'data-topic': 'topic-main' },
    children: [card01, ck04.row, ack06.row, settled05],
  });
  board.appendChild(topic1);

  for (let i = 10; i < 20; i++) {
    const extraTopic = makeEl('section', {
      className: 'topic',
      attrs: { 'data-topic': 'topic-extra-' + i },
      children: [makePolicyCard(String(i), 'Extra card ' + i, ['a', 'b'])],
    });
    board.appendChild(extraTopic);
  }
  for (let v = 0; v < 25; v++) {
    const vCard = makePolicyCard(String(100 + v), 'Virtual ' + v, ['a', 'b']);
    virtualStore.set('v-topic-' + v, { children: null, stashed: makeEl('section', { children: [vCard] }) });
  }

  // Chat entries containing compact items for 1, 4, 5, 6.
  function makeChatChip(id) {
    return makeEl('a', { className: 'wbupdate-item-id', attrs: { 'data-wb-jump': 'item-' + id }, text: '(' + id + ')' });
  }
  const chip1 = makeChatChip('1');
  const chip4 = makeChatChip('4');
  const chip5 = makeChatChip('5');
  const chip6 = makeChatChip('6');
  const chatGroup = makeEl('div', { className: 'wbupdate-items', children: [chip1, chip4, chip5, chip6] });
  entries.appendChild(chatGroup);

  // Initial hydrate.
  ctx.hydrateWbUpdateItems(entries);
  const wrap1 = chip1.parentNode;
  const wrap4 = chip4.parentNode;
  const wrap5 = chip5.parentNode;
  const wrap6 = chip6.parentNode;

  // Baseline checks: 1, 4, 6 open (filled); 5 settled (empty).
  assert.equal(wrap1.getAttribute('data-wb-state'), 'open');
  assert.equal(wrap1.hasAttribute('data-wb-empty'), false);
  assert.equal(chip1.querySelector('.wbupdate-cap-val').textContent, '');
  assert.equal(wrap5.getAttribute('data-wb-state'), 'settled');
  assert.equal(wrap5.hasAttribute('data-wb-empty'), false);
  assert.equal(chip5.querySelector('.wbupdate-cap-val').textContent, '');

  // Helper simulating user clicking a board control (form.js mutates control first, then dashboard click/change fires).
  function simulateOptionClick(card, opt) {
    card.querySelectorAll('.opt[data-opt]').forEach((b) => {
      b.setAttribute('aria-pressed', b.getAttribute('data-opt') === opt ? 'true' : 'false');
    });
    const btn = card.querySelector('.opt[data-opt="' + opt + '"]');
    ctx.handleBoardClick(btn);
  }
  function simulateCheckboxChange(inputEl, row, checked) {
    inputEl.checked = checked;
    if (checked) ctx.setCheckNo(row, false);
    inputEl.dispatchEvent({ type: 'change', target: inputEl });
  }
  function simulateNoToggle(row, noBtn, on) {
    ctx.setCheckNo(row, on);
    if (on) {
      const b = row.querySelector('label.ck > input');
      if (b) b.checked = false;
    }
    ctx.handleBoardClick(noBtn);
  }
  function simulateNoteInput(noteEl, value) {
    noteEl.value = value;
    noteEl.dispatchEvent({ type: 'input', target: noteEl });
  }

  // Case 1: Checking/staging a choice immediately flips the 1-row chat card to empty + shows value.
  resetCounters();
  simulateOptionClick(card01, 'a');
  assert.equal(wrap1.getAttribute('data-wb-state'), 'staged');
  assert.equal(wrap1.getAttribute('data-wb-empty'), '1', 'policy choice "a" flips card to empty');
  assert.equal(chip1.querySelector('.wbupdate-cap-val').textContent, 'a');
  assert.equal(chip1.querySelector('.wbupdate-cap-val').getAttribute('data-wb-pick'), 'a');
  // Case 5 check inside Case 1: settled card remains settled even after interaction on another card.
  assert.equal(wrap5.getAttribute('data-wb-state'), 'settled');
  assert.equal(wrap5.hasAttribute('data-wb-empty'), false);
  assert.equal(counters.virtualForEach, 0, 'option click must perform ZERO virtualStore scans');
  assert.equal(counters.boardQueryAll, 0, 'option click must perform ZERO full-board boardQ scans');
  assert.equal(counters.boardQueryOne, 0, 'option click with openEl hint must perform ZERO #r-board querySelector scans');

  resetCounters();
  simulateCheckboxChange(ck04.box, ck04.row, true);
  assert.equal(wrap4.getAttribute('data-wb-state'), 'staged');
  assert.equal(wrap4.getAttribute('data-wb-empty'), '1', 'check "ok" flips card to empty');
  assert.equal(chip4.querySelector('.wbupdate-cap-val').textContent, 'ok');
  assert.equal(chip4.querySelector('.wbupdate-cap-val').getAttribute('data-wb-pick'), 'ok');
  assert.equal(counters.virtualForEach, 0, 'checkbox click must perform ZERO virtualStore scans');
  assert.equal(counters.boardQueryAll, 0, 'checkbox click must perform ZERO full-board boardQ scans');
  assert.equal(counters.boardQueryOne, 0, 'checkbox click with openEl hint must perform ZERO #r-board querySelector scans');

  // Case 3: Editing staged choice updates empty card's displayed value.
  resetCounters();
  simulateOptionClick(card01, 'b');
  assert.equal(wrap1.getAttribute('data-wb-empty'), '1', 'switching a -> b stays empty');
  assert.equal(chip1.querySelector('.wbupdate-cap-val').textContent, 'b');
  assert.equal(chip1.querySelector('.wbupdate-cap-val').getAttribute('data-wb-pick'), 'b');
  assert.equal(counters.virtualForEach, 0, 'switching choice must perform ZERO virtualStore scans');

  simulateNoToggle(ck04.row, ck04.noBtn, true);
  assert.equal(wrap4.getAttribute('data-wb-empty'), '1', 'switching ok -> no stays empty');
  assert.equal(chip4.querySelector('.wbupdate-cap-val').textContent, 'no');
  assert.equal(chip4.querySelector('.wbupdate-cap-val').getAttribute('data-wb-pick'), 'no');

  // Case 2a: Clearing via toggling off / unchecking / clearing data-no while open reverts to filled.
  resetCounters();
  simulateCheckboxChange(ck04.box, ck04.row, true);
  simulateCheckboxChange(ck04.box, ck04.row, false);
  assert.equal(wrap4.getAttribute('data-wb-state'), 'open');
  assert.equal(wrap4.hasAttribute('data-wb-empty'), false, 'unchecking while open reverts to filled');
  assert.equal(chip4.querySelector('.wbupdate-cap-val').textContent, '');
  assert.equal(chip4.querySelector('.wbupdate-cap-val').hasAttribute('data-wb-pick'), false);
  assert.equal(counters.virtualForEach, 0);

  // Case 2b: Clearing via composer [data-unchip] button reverts card to filled (even on policy card without .note!).
  const unchip1 = cchips.querySelector('.unchip[data-unchip="01"]');
  assert(unchip1, 'composer chip for 01 rendered');
  resetCounters();
  ctx.handleBoardClick(unchip1);
  assert.equal(card01.querySelector('.opt[data-opt="b"]').getAttribute('aria-pressed'), 'false', 'board card option cleared');
  assert.equal(wrap1.getAttribute('data-wb-state'), 'open', '[data-unchip] reverts card 1 to open');
  assert.equal(wrap1.hasAttribute('data-wb-empty'), false, '[data-unchip] removes empty visual state');
  assert.equal(chip1.querySelector('.wbupdate-cap-val').textContent, '');
  assert.equal(counters.virtualForEach, 0, '[data-unchip] click must perform ZERO virtualStore scans');

  // Case 2c: Deleting inline capsule in composer (wsc:change) reverts card even if board topic is virtualized!
  simulateCheckboxChange(ck04.box, ck04.row, true);
  assert.equal(wrap4.getAttribute('data-wb-empty'), '1');
  assert.equal(chip4.querySelector('.wbupdate-cap-val').textContent, 'ok');
  // Detach ck04.row off live #r-board into virtualStore to simulate scrolled-away virtualized topic.
  topic1.removeChild(ck04.row);
  virtualStore.set('v-topic-ck04', { children: [ck04.row], stashed: null });
  const fakeInlineRows = new Map();
  ctx.setFakeInlineEditor({
    rows: (kind) => (kind === 'answer' ? Array.from(fakeInlineRows.values()) : []),
    upsert: (kind, key, cap) => { if (kind === 'answer') fakeInlineRows.set(key, { key, payload: { answer: cap.answer } }); },
    insertCapsule: (kind, cap, opts) => { if (kind === 'answer') fakeInlineRows.set(opts.key, { key: opts.key, payload: { answer: cap.answer } }); },
    remove: (kind, key) => { if (kind === 'answer') fakeInlineRows.delete(key); },
  });
  ctx.putChip({ id: '04', answer: 'ok', note: '' });
  resetCounters();
  h.ctext.dispatchEvent({
    type: 'wsc:change',
    detail: { removed: [{ kind: 'answer', key: '04' }], added: [] },
  });
  assert.equal(wrap4.getAttribute('data-wb-state'), 'open', 'deleting capsule while topic virtualized reverts chat card to open');
  assert.equal(wrap4.hasAttribute('data-wb-empty'), false);
  assert.equal(chip4.querySelector('.wbupdate-cap-val').textContent, '');
  assert.equal(counters.virtualForEach, 0, 'deleting capsule while virtualized must perform ZERO virtualStore scans');
  ctx.setFakeInlineEditor(null);

  // Put ck04.row back on live board.
  virtualStore.delete('v-topic-ck04');
  topic1.appendChild(ck04.row);
  ctx.setItem('04', null);

  // Case 4: Note-only staged comment WITHOUT a choice stays FILLED!
  const card01Note = card01.querySelector('textarea.note');
  resetCounters();
  simulateNoteInput(card01Note, 'needs clarification before picking');
  assert.equal(wrap1.getAttribute('data-wb-state'), 'staged', 'note-only reports staged state');
  assert.equal(wrap1.hasAttribute('data-wb-empty'), false, 'note-only without choice MUST stay filled (no data-wb-empty)');
  assert.equal(chip1.querySelector('.wbupdate-cap-val').textContent, '', 'note-only shows no choice value badge');
  assert.equal(counters.virtualForEach, 0, 'note input must perform ZERO virtualStore scans');

  // Adding a choice on top of the note flips it to empty + displays choice value.
  simulateOptionClick(card01, 'a');
  assert.equal(wrap1.getAttribute('data-wb-empty'), '1');
  assert.equal(chip1.querySelector('.wbupdate-cap-val').textContent, 'a');
  assert(chip1.parentNode.querySelector('.wbupdate-ans').textContent.includes('a'));
  assert(chip1.parentNode.querySelector('.wbupdate-ans').textContent.includes('needs clarification'));

  // Clearing choice while leaving note reverts to FILLED + clears choice badge.
  card01.querySelector('.opt[data-opt="a"]').setAttribute('aria-pressed', 'false');
  ctx.handleBoardClick(card01.querySelector('.opt[data-opt="a"]'));
  assert.equal(wrap1.getAttribute('data-wb-state'), 'staged');
  assert.equal(wrap1.hasAttribute('data-wb-empty'), false, 'clearing choice while note remains MUST revert to filled');
  assert.equal(chip1.querySelector('.wbupdate-cap-val').textContent, '');

  // Clear note -> back to open filled.
  simulateNoteInput(card01Note, '');
  assert.equal(wrap1.getAttribute('data-wb-state'), 'open');
  assert.equal(wrap1.hasAttribute('data-wb-empty'), false);

  // Case 5 (virtualized settled check): #ack-all or writeAnswers or applySent avoids full virtualStore scans.
  const ackAllEl = makeEl('input', { attrs: { id: 'ack-all', type: 'checkbox' }, checked: true });
  topic1.appendChild(ackAllEl);
  ack06.box.checked = true;
  resetCounters();
  ackAllEl.dispatchEvent({ type: 'change', target: ackAllEl });
  assert.equal(wrap6.getAttribute('data-wb-empty'), '1');
  assert.equal(chip6.querySelector('.wbupdate-cap-val').textContent, 'ok');
  assert.equal(wrap5.getAttribute('data-wb-state'), 'settled', 'settled card 5 untouched');
  assert.equal(counters.virtualForEach, 0, '#ack-all sync must perform ZERO virtualStore scans');

  // writeAnswers on topic with no saved answers in `saved` must not touch chat or virtualStore.
  resetCounters();
  ctx.writeAnswers(topic1, {});
  assert.equal(counters.virtualForEach, 0);
  assert.equal(counters.syncWbCalls || 0, 0, 'writeAnswers with no restored answers skips syncWbUpdateItems');
  assert.equal(counters.entriesQueryAll, 0, 'writeAnswers with no restored answers skips entries queries');

  // applySent with empty .sentdata (no previous marks) skips syncWbUpdateItems.
  resetCounters();
  ctx.applySent(false);
  assert.equal(counters.virtualForEach, 0);
  assert.equal(counters.syncWbCalls || 0, 0, 'applySent with unchanged empty sentdata skips syncWbUpdateItems');
  assert.equal(counters.entriesQueryAll, 0, 'applySent with unchanged empty sentdata skips entries queries');

  // Settled .srow note/unchip does not misclassify settled item as open.
  const srow05 = makeEl('div', {
    className: 'srow',
    attrs: { 'data-id': '05' },
    children: [makeEl('textarea', { className: 'note', value: 'follow-up comment' })],
  });
  topic1.appendChild(srow05);
  simulateNoteInput(srow05.querySelector('textarea.note'), 'follow-up comment');
  assert.equal(wrap5.getAttribute('data-wb-state'), 'settled', 'commenting on settled .srow preserves settled state');
  assert.equal(wrap5.hasAttribute('data-wb-empty'), false);
  ctx.setItem('05', null);
  ctx.dropChip('05', false, false, srow05);
  assert.equal(wrap5.getAttribute('data-wb-state'), 'settled', 'clearing comment on settled .srow preserves settled state');
}

console.log('chat-wb-card-staged-empty: all 5 UX scenarios, composer/inline sync, and zero-rescan perf checks passed');
