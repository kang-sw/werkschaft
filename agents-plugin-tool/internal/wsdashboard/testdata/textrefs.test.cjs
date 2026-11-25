const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

const assetsDir = path.join(__dirname, '../assets');

class Evt {
  constructor(type, opts = {}) {
    Object.assign(this, opts);
    this.type = type;
    this.defaultPrevented = !!opts.defaultPrevented;
    this.bubbles = opts.bubbles !== false;
  }
  preventDefault() { this.defaultPrevented = true; }
  stopPropagation() { this.propagationStopped = true; }
  stopImmediatePropagation() { this.stopped = true; this.propagationStopped = true; }
}

function makeDoc() {
  let active = null;
  const docListeners = {};
  const winListeners = {};
  const idMap = new Map();

  class El {
    constructor(name = 'div', type = 1) {
      this.nodeName = name.toUpperCase();
      this.tagName = this.nodeName;
      this.nodeType = type;
      this.childNodes = [];
      this.attrs = {};
      this.listeners = {};
      this.style = {};
      this.className = '';
      this.hidden = false;
      this.disabled = false;
      this.offsetWidth = 240;
      this.offsetHeight = 140;
      this._value = '';
      this.selectionStart = 0;
      this.selectionEnd = 0;
      this.content = name.toLowerCase() === 'template' ? this : null;
    }
    get id() { return this.attrs.id || ''; }
    set id(v) { this.setAttribute('id', v); }
    get value() { return this._value; }
    set value(v) { this._value = String(v); }
    get isConnected() {
      let cur = this;
      while (cur) {
        if (cur === doc.body || cur === doc.documentElement) return true;
        cur = cur.parentNode;
      }
      return false;
    }
    get classList() {
      const el = this;
      return {
        add(...cs) {
          const set = new Set(el.className.split(/\s+/).filter(Boolean));
          cs.forEach(c => set.add(c));
          el.className = Array.from(set).join(' ');
        },
        remove(...cs) {
          const set = new Set(el.className.split(/\s+/).filter(Boolean));
          cs.forEach(c => set.delete(c));
          el.className = Array.from(set).join(' ');
        },
        contains(c) { return el.className.split(/\s+/).includes(c); },
        toggle(c, force) {
          const on = force === undefined ? !this.contains(c) : !!force;
          if (on) this.add(c); else this.remove(c);
          return on;
        },
      };
    }
    get children() { return this.childNodes.filter(n => n.nodeType === 1); }
    get firstChild() { return this.childNodes[0] || null; }
    get previousSibling() {
      if (!this.parentNode) return null;
      return this.parentNode.childNodes[this.parentNode.childNodes.indexOf(this) - 1] || null;
    }
    get nextSibling() {
      if (!this.parentNode) return null;
      return this.parentNode.childNodes[this.parentNode.childNodes.indexOf(this) + 1] || null;
    }
    get nextElementSibling() {
      let n = this.nextSibling;
      while (n && n.nodeType !== 1) n = n.nextSibling;
      return n;
    }
    appendChild(n) {
      if (n.parentNode) n.parentNode.removeChild(n);
      this.childNodes.push(n);
      n.parentNode = this;
      return n;
    }
    insertBefore(n, ref) {
      if (n.parentNode) n.parentNode.removeChild(n);
      const i = ref ? this.childNodes.indexOf(ref) : -1;
      if (i < 0) this.childNodes.push(n);
      else this.childNodes.splice(i, 0, n);
      n.parentNode = this;
      return n;
    }
    insertAdjacentElement(_, n) {
      const p = this.parentNode;
      if (!p) return;
      const i = p.childNodes.indexOf(this);
      p.childNodes.splice(i + 1, 0, n);
      n.parentNode = p;
    }
    remove() {
      if (this.parentNode) this.parentNode.removeChild(this);
    }
    removeChild(n) {
      const i = this.childNodes.indexOf(n);
      if (i >= 0) this.childNodes.splice(i, 1);
      n.parentNode = null;
      return n;
    }
    replaceChildren(...nodes) {
      this.childNodes.forEach(c => { c.parentNode = null; });
      this.childNodes = [];
      nodes.forEach(n => {
        if (n && n.nodeName === 'TEMPLATE' && n.childNodes) {
          n.childNodes.slice().forEach(c => this.appendChild(c));
        } else if (n) {
          this.appendChild(n);
        }
      });
    }
    set textContent(v) {
      this.childNodes.forEach(c => { c.parentNode = null; });
      this.childNodes = [];
      if (this.nodeType === 3) this.nodeValue = String(v ?? '');
      else if (v !== '' && v !== null && v !== undefined) this.appendChild(textNode(String(v)));
    }
    get textContent() {
      return this.nodeType === 3 ? (this.nodeValue || '') : this.childNodes.map(n => n.textContent).join('');
    }
    setAttribute(k, v) {
      this.attrs[k] = String(v);
      if (k === 'id') idMap.set(String(v), this);
    }
    getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
    hasAttribute(k) { return k in this.attrs; }
    removeAttribute(k) { delete this.attrs[k]; }
    contains(n) {
      let cur = n;
      while (cur) {
        if (cur === this) return true;
        cur = cur.parentNode;
      }
      return false;
    }
    matchesCompound(part) {
      if (!part) return false;
      const attrs = [];
      let rest = part.replace(/\[([^\]=]+)(?:="([^"]*)")?\]/g, (_, k, v) => {
        attrs.push([k, v]);
        return '';
      });
      let id = null;
      rest = rest.replace(/#([A-Za-z0-9_-]+)/g, (_, m) => { id = m; return ''; });
      const classes = [];
      rest = rest.replace(/\.([A-Za-z0-9_-]+)/g, (_, c) => { classes.push(c); return ''; });
      const tag = rest.trim().toUpperCase();
      if (tag && this.tagName !== tag) return false;
      if (id && this.id !== id) return false;
      if (!classes.every(c => this.classList.contains(c))) return false;
      if (!attrs.every(([k, v]) => (v === undefined ? this.hasAttribute(k) : this.getAttribute(k) === v))) return false;
      return true;
    }
    matches(sel) {
      return sel.split(',').some(chain => {
        const parts = chain.trim().split(/\s+/).filter(Boolean);
        if (!parts.length) return false;
        if (!this.matchesCompound(parts[parts.length - 1])) return false;
        let cur = this.parentNode;
        for (let i = parts.length - 2; i >= 0; i--) {
          while (cur && cur.nodeType === 1 && !cur.matchesCompound(parts[i])) cur = cur.parentNode;
          if (!cur || cur.nodeType !== 1) return false;
          cur = cur.parentNode;
        }
        return true;
      });
    }
    closest(sel) {
      let cur = this;
      while (cur && cur.nodeType === 1) {
        if (cur.matches(sel)) return cur;
        cur = cur.parentNode;
      }
      return null;
    }
    querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
    querySelectorAll(sel) {
      const out = [];
      for (const c of this.children) {
        if (c.matches(sel)) out.push(c);
        out.push(...c.querySelectorAll(sel));
      }
      return out;
    }
    addEventListener(k, f) { (this.listeners[k] ||= []).push(f); }
    dispatchEvent(e) {
      if (!e.target) e.target = this;
      let cur = this;
      while (cur) {
        for (const f of cur.listeners?.[e.type] || []) {
          f(e);
          if (e.stopped) break;
        }
        if (e.stopped || e.propagationStopped || !e.bubbles) break;
        cur = cur.parentNode;
      }
      if (!e.stopped && !e.propagationStopped && e.bubbles) {
        for (const f of docListeners[e.type] || []) {
          f(e);
          if (e.stopped) break;
        }
      }
      return !e.defaultPrevented;
    }
    focus() { active = this; }
    blur() { if (active === this) active = null; }
    setSelectionRange(a, b) { this.selectionStart = a; this.selectionEnd = b; }
    setRangeText(s, a, b) {
      this._value = this._value.slice(0, a) + s + this._value.slice(b);
      this.selectionStart = this.selectionEnd = a + s.length;
    }
    getBoundingClientRect() { return { left: 20, top: 200, bottom: 256, right: 76, width: 56, height: 56 }; }
    getClientRects() { return this.isConnected ? [this.getBoundingClientRect()] : []; }
    cloneNode(deep) {
      const n = new El(this.nodeName, this.nodeType);
      n.nodeValue = this.nodeValue;
      n.attrs = { ...this.attrs };
      n.className = this.className;
      n._value = this._value;
      n.src = this.src;
      n.alt = this.alt;
      n.title = this.title;
      if (deep) this.childNodes.forEach(c => n.appendChild(c.cloneNode(true)));
      return n;
    }
  }
  function textNode(s) { const n = new El('#text', 3); n.nodeValue = s; return n; }

  let currentRange = null;
  const selection = {
    get rangeCount() { return currentRange ? 1 : 0; },
    get anchorNode() { return currentRange?.startContainer; },
    get focusNode() { return currentRange?.endContainer; },
    get isCollapsed() { return true; },
    getRangeAt() { return currentRange; },
    removeAllRanges() { currentRange = null; },
    addRange(r) { currentRange = r; },
  };
  const doc = {
    documentElement: new El('html'),
    body: new El('body'),
    get activeElement() { return active; },
    set activeElement(v) { active = v; },
    createElement: n => new El(n),
    createElementNS: (_, n) => new El(n),
    createTextNode: textNode,
    getElementById: id => idMap.get(id) || null,
    querySelector: sel => doc.body.querySelector(sel),
    querySelectorAll: sel => doc.body.querySelectorAll(sel),
    addEventListener(k, f) { (docListeners[k] ||= []).push(f); },
    dispatchEvent(e) {
      for (const f of docListeners[e.type] || []) { f(e); if (e.stopped) break; }
      return !e.defaultPrevented;
    },
    execCommand() { return false; },
    createRange() {
      return {
        setStart(n, a) { this.startContainer = n; this.startOffset = a; },
        setEnd(n, a) { this.endContainer = n; this.endOffset = a; },
        collapse() {},
        getBoundingClientRect() { return { left: 10, top: 500, bottom: 520, height: 20 }; },
      };
    },
  };
  doc.documentElement.clientWidth = 1000;
  doc.documentElement.clientHeight = 800;
  doc.documentElement.appendChild(doc.body);
  return { doc, El, selection, winListeners };
}

function buildComposerFixture() {
  const { doc, El, selection } = makeDoc();
  const convo = doc.body.appendChild(new El('div')); convo.id = 'convo';
  const rEntries = convo.appendChild(new El('div')); rEntries.id = 'r-entries';
  const centries = rEntries.appendChild(new El('div')); centries.className = 'conv centries';
  const rConvlive = convo.appendChild(new El('div')); rConvlive.id = 'r-convlive';
  const clive = rConvlive.appendChild(new El('div')); clive.className = 'conv clive';

  const composer = doc.body.appendChild(new El('div')); composer.id = 'composer';
  const cimgs = composer.appendChild(new El('div')); cimgs.id = 'cimgs'; cimgs.className = 'cimgs';
  const ctext = composer.appendChild(new El('textarea')); ctext.id = 'ctext';

  const warns = [];
  const posted = [];
  const topicCleared = [];
  const timers = [];
  let nextTid = 0;

  const ctx = vm.createContext({
    console,
    document: doc,
    doc,
    root: doc.documentElement,
    convo,
    composer,
    ctext,
    cimgs,
    Event: Evt,
    KeyboardEvent: Evt,
    CustomEvent: Evt,
    TextEncoder,
    TextDecoder,
    Uint8Array,
    Math,
    Date,
    Object,
    Array,
    RegExp,
    JSON,
    Number,
    String,
    Set,
    Map,
    Promise,
    MutationObserver: class { observe() {} },
    URL: { createObjectURL: f => 'blob:mock/' + (f.name || 'img'), revokeObjectURL() {} },
    FileReader: class {
      readAsDataURL() { this.result = 'data:image/png;base64,AAAA'; if (this.onload) this.onload(); }
    },
    $: id => doc.getElementById(id),
    q: (sel, scope) => (scope || doc).querySelectorAll(sel),
    region: name => doc.getElementById('r-' + name),
    entriesBox: () => centries,
    boardQ: () => [],
    boardItem: () => null,
    inBoard: () => false,
    rid: 'sess-1',
    live: false,
    shell: false,
    mode: 'session',
    bselections: [],
    bmentions: [],
    wpicks: [],
    wconsoles: [],
    closeRequests: [],
    mpend: null,
    mimgs: null,
    mnote: null,
    cchips: null,
    ibadge: null,
    gmenu: null,
    chips: {},
    chipOrder: [],
    cmdSel: null,
    restoring: false,
    draftStore: null,
    draftLoaded: true,
    draftDetached: false,
    boardOwn: {},
    mentions: [],
    chipList: () => [],
    topicNotes: () => [],
    validCloseRequests: x => Array.isArray(x) ? x : [],
    validMentions: x => Array.isArray(x) ? x : [],
    validPicks: x => Array.isArray(x) ? x : [],
    validConsoles: x => Array.isArray(x) ? x : [],
    dropChip() {},
    setItem() {},
    putChip() {},
    renderChips() {},
    clearTopicNote: t => topicCleared.push(t),
    focusTopicNote() {},
    closeTopicNotes() {},
    restoreTopics: () => [],
    selectionPrefix: () => '',
    splitSelectionText: s => ({ text: s, groups: [], cuts: [] }),
    sendText: () => ctext.value,
    inlineEditor: () => null,
    grow() {},
    clearMentions() {},
    shiftMentions() {},
    queueDraft() {},
    saveDraft: () => Promise.resolve(true),
    followConvo() {},
    boardFlushViewed() {},
    cexecSend: () => false,
    noteWarn: msg => warns.push(msg),
    noteOk() {},
    wsT: (k, ...args) => k + (args.length ? ':' + args.join('|') : ''),
    post: (route, body, done) => { posted.push({ route, body }); if (done) done(true, { id: '20261125T000000.000000000Z-12345678' }); },
    setTimeout: (fn, ms) => { const id = ++nextTid; timers.push({ id, fn, ms }); return id; },
    clearTimeout: id => { const i = timers.findIndex(t => t.id === id); if (i >= 0) timers.splice(i, 1); },
    getSelection: () => selection,
    innerWidth: 1000,
    innerHeight: 800,
    addEventListener() {},
    removeEventListener() {},
  });
  ctx.window = ctx;

  // Load capsules.js + rail.js
  vm.runInContext(fs.readFileSync(path.join(assetsDir, 'capsules.js'), 'utf8'), ctx);
  vm.runInContext(fs.readFileSync(path.join(assetsDir, 'rail.js'), 'utf8'), ctx);

  // Extract popover section of dashboard-conversation.js
  const convSrc = readAssetSource('wsdashboard', 'dashboard-conversation.js');
  const popStart = convSrc.indexOf('  // --- inline capsule detail popover');
  assert.notEqual(popStart, -1);
  vm.runInContext(convSrc.slice(popStart), ctx);

  // Load images + textrefs + delivery + restore + actions click block
  vm.runInContext(readAssetSource('wsdashboard', 'dashboard-composer-images.js'), ctx);
  vm.runInContext(readAssetSource('wsdashboard', 'dashboard-composer-textrefs.js'), ctx);
  vm.runInContext(readAssetSource('wsdashboard', 'dashboard-composer-delivery.js'), ctx);
  vm.runInContext(readAssetSource('wsdashboard', 'dashboard-composer-restore.js'), ctx);
  const actSrc = readAssetSource('wsdashboard', 'dashboard-actions.js');
  const actEnd = actSrc.indexOf('  // --- link actions:');
  assert.notEqual(actEnd, -1);
  vm.runInContext(actSrc.slice(0, actEnd), ctx);

  // Attach capsules editor to ctext (mirroring real dashboard)
  ctx.WsCapsules.attach(ctext);

  function flushTimers() {
    while (timers.length) {
      const batch = timers.splice(0, timers.length);
      batch.forEach(t => t.fn());
    }
  }
  function makeClipboard({ text = '', rich = '', files = [] } = {}) {
    const types = [];
    if (text) types.push('text/plain');
    if (rich) types.push('application/x-wsc-capsules+json');
    if (files.length) types.push('Files');
    return {
      types,
      items: files.map(f => ({ kind: 'file', getAsFile: () => f })),
      files,
      getData(t) {
        if (t === 'text/plain') return text;
        if (t === 'application/x-wsc-capsules+json') return rich;
        return '';
      },
    };
  }
  function pasteOnEditor(clipOpts) {
    const e = new Evt('paste', { clipboardData: makeClipboard(clipOpts) });
    ctext.wsc.editor.dispatchEvent(e);
    return e;
  }
  return { doc, El, ctx, ctext, cimgs, clive, warns, posted, topicCleared, flushTimers, pasteOnEditor };
}

test('1023 vs 1024 char paste threshold and Ctrl/Cmd+Shift+V raw bypass without leak', () => {
  const f = buildComposerFixture();
  const s1023 = 'a'.repeat(1023);
  const s1024 = 'b'.repeat(1024);

  // 1023 chars pastes inline, no textRef attached
  f.pasteOnEditor({ text: s1023 });
  assert.equal(f.ctext.value, s1023);
  assert.equal(f.ctx.textRefs.length, 0);
  assert.equal(f.cimgs.querySelectorAll('.txtchip').length, 0);

  // Reset and paste 1024 chars -> collapses to [Text #1] + .txtchip
  f.ctext.value = '';
  f.ctext.setSelectionRange(0, 0);
  f.pasteOnEditor({ text: s1024 });
  assert.equal(f.ctext.value.trim(), '[Text #1]');
  assert.equal(f.ctx.textRefs.length, 1);
  assert.equal(f.ctx.textRefs[0].n, 1);
  assert.equal(f.ctx.textRefs[0].body, s1024);
  assert.equal(f.cimgs.querySelectorAll('.txtchip').length, 1);

  // Ctrl+Shift+V raw bypass -> pastes 1024 chars inline without collapsing
  f.ctext.value = '';
  f.ctext.setSelectionRange(0, 0);
  f.ctext.wsc.editor.dispatchEvent(new Evt('keydown', { key: 'V', code: 'KeyV', ctrlKey: true, shiftKey: true }));
  assert.equal(f.ctx.WsCapsules.rawPaste(f.ctext), true);
  f.pasteOnEditor({ text: s1024 });
  assert.equal(f.ctext.value, s1024);
  assert.equal(f.ctx.WsCapsules.rawPaste(f.ctext), false, 'rawPaste flag cleared after paste consumption');

  // Subsequent plain Ctrl+V does NOT inherit raw bypass
  f.ctext.value = '';
  f.ctext.setSelectionRange(0, 0);
  f.pasteOnEditor({ text: s1024 });
  assert.equal(f.ctext.value.trim(), '[Text #2]');

  // Pressing Cmd+Shift+V then keyup (without paste) clears rawPasteArmed
  f.ctext.wsc.editor.dispatchEvent(new Evt('keydown', { key: 'v', code: 'KeyV', metaKey: true, shiftKey: true }));
  assert.equal(f.ctx.WsCapsules.rawPaste(f.ctext), true);
  f.ctext.wsc.editor.dispatchEvent(new Evt('keyup', { key: 'v', code: 'KeyV', metaKey: true, shiftKey: true }));
  assert.equal(f.ctx.WsCapsules.rawPaste(f.ctext), false);

  // Pressing Ctrl+Shift+V then blur clears rawPasteArmed
  f.ctext.wsc.editor.dispatchEvent(new Evt('keydown', { key: 'V', code: 'KeyV', ctrlKey: true, shiftKey: true }));
  assert.equal(f.ctx.WsCapsules.rawPaste(f.ctext), true);
  f.ctext.wsc.editor.dispatchEvent(new Evt('blur'));
  assert.equal(f.ctx.WsCapsules.rawPaste(f.ctext), false);
});

test('undo trash restore, monotonic non-reuse with gaps, and txtchip vs tchip isolation', () => {
  const f = buildComposerFixture();
  const payload1 = 'x'.repeat(1100);
  const payload2 = 'y'.repeat(1200);

  f.pasteOnEditor({ text: payload1 });
  assert.equal(f.ctx.textRefs.length, 1);
  assert.equal(f.ctx.textRefs[0].n, 1);

  // Verify distinct class & attributes (not .tchip / data-tchip / data-untchip)
  const chip = f.cimgs.querySelector('.txtchip');
  assert.ok(chip);
  assert.equal(chip.getAttribute('data-txtchip'), '1');
  assert.equal(chip.getAttribute('data-tchip'), null);
  const rmBtn = chip.querySelector('.untxt');
  assert.ok(rmBtn);
  assert.equal(rmBtn.getAttribute('data-untxt'), '1');
  assert.equal(rmBtn.getAttribute('data-untchip'), null);

  // Click × on .txtchip -> removes marker, moves to txtTrash, does not touch topic notes
  rmBtn.dispatchEvent(new Evt('click'));
  assert.equal(f.ctx.textRefs.length, 0);
  assert.equal(f.ctext.value.trim(), '');
  assert.equal(f.topicCleared.length, 0);
  assert.ok(f.ctx.txtTrash[1], 'dropped ref waits in txtTrash for undo');

  // Undo brings [Text #1] back -> syncMarkers restores #1 from txtTrash
  f.ctext.value = '[Text #1] ';
  f.ctx.syncMarkers();
  assert.equal(f.ctx.textRefs.length, 1);
  assert.equal(f.ctx.textRefs[0].n, 1);
  assert.equal(f.ctx.textRefs[0].body, payload1);

  // Manual marker deletion drops #1 into txtTrash
  f.ctext.value = '';
  f.ctx.syncMarkers();
  assert.equal(f.ctx.textRefs.length, 0);

  // Next paste allocates #2 (never reuses #1!)
  f.pasteOnEditor({ text: payload2 });
  assert.equal(f.ctx.textRefs.length, 1);
  assert.equal(f.ctx.textRefs[0].n, 2);
  assert.equal(f.ctext.value.trim(), '[Text #2]');
});

test('mixed clipboard attaches both long textref and image side-by-side in #cimgs', () => {
  const f = buildComposerFixture();
  const longText = 'z'.repeat(1050);
  const imgFile = { name: 'clip.png', type: 'image/png', size: 128 };

  f.pasteOnEditor({ text: longText, files: [imgFile] });
  f.flushTimers();

  assert.equal(f.ctx.textRefs.length, 1);
  assert.equal(f.ctx.imgs.length, 1);
  assert.ok(f.ctext.value.includes('[Text #1]'));
  assert.ok(f.ctext.value.includes('[Image #1]'));
  assert.equal(f.cimgs.querySelectorAll('.ichip').length, 1);
  assert.equal(f.cimgs.querySelectorAll('.txtchip').length, 1);
});

test('click-to-preview popover works for both pending .ichip and .txtchip in #cimgs', () => {
  const f = buildComposerFixture();
  const body = 'line1\n<script>alert(1)</script>\n' + 'k'.repeat(1050);
  const imgFile = { name: 'diagram.png', type: 'image/png', size: 256 };

  f.pasteOnEditor({ text: body, files: [imgFile] });
  f.flushTimers();

  const txtChip = f.cimgs.querySelector('.txtchip');
  const imgChip = f.cimgs.querySelector('.ichip');
  assert.ok(txtChip && imgChip);

  // Hover does NOT open click-only chips
  txtChip.dispatchEvent(new Evt('mouseover'));
  assert.equal(f.doc.getElementById('icapsule-pop'), null);

  // Click .txtchip opens pinned popover with full escaped text
  txtChip.dispatchEvent(new Evt('click'));
  const pop = f.doc.getElementById('icapsule-pop');
  assert.ok(pop && !pop.hidden);
  assert.equal(txtChip.getAttribute('aria-expanded'), 'true');
  const pre = pop.querySelector('.icapsule-pop-textref');
  assert.ok(pre);
  assert.equal(pre.textContent, body);

  // Click .ichip switches popover to image preview
  imgChip.dispatchEvent(new Evt('click'));
  assert.equal(pop.hidden, false);
  assert.equal(txtChip.getAttribute('aria-expanded'), 'false');
  assert.equal(imgChip.getAttribute('aria-expanded'), 'true');
  const popImg = pop.querySelector('.icapsule-pop-img');
  assert.ok(popImg);
  assert.equal(popImg.alt, 'diagram.png');

  // Escape closes popover
  f.doc.dispatchEvent(new Evt('keydown', { key: 'Escape' }));
  assert.equal(pop.hidden, true);
  assert.equal(imgChip.getAttribute('aria-expanded'), 'false');

  // Clicking × on .txtchip does NOT open popover
  const unTxt = txtChip.querySelector('.untxt');
  unTxt.dispatchEvent(new Evt('click'));
  assert.equal(pop.hidden, true);
  assert.equal(f.ctx.textRefs.length, 0);
});

test('UTF-8 byte framing round-trips multibyte text and rejects malformed/UTF-16 counts', () => {
  const f = buildComposerFixture();
  const body = '한글 테스트 🚀\n</textref>\n<wb:selection><wb:mention topic="t">x</wb:mention></wb:selection>';
  const expectedBytes = Buffer.byteLength(body, 'utf8');
  assert.notEqual(expectedBytes, body.length, 'must test UTF-8 byte len != UTF-16 code unit len');

  const block = f.ctx.textRefBlock(3, body);
  assert.ok(block.startsWith(`<textref id="#3" bytes="${expectedBytes}">`));

  const stripped = f.ctx.stripTextRefs('Intro [Text #3]\n\n' + block);
  assert.equal(stripped.text, 'Intro [Text #3]');
  assert.equal(stripped.refs.length, 1);
  assert.equal(stripped.refs[0].n, 3);
  assert.equal(stripped.refs[0].body, body);
  assert.equal(stripped.refs[0].bytes, expectedBytes);

  // Malformed: UTF-16 length instead of UTF-8 byte length -> literal fallback
  const badUtf16 = `Hi <textref id="#1" bytes="${body.length}">${body}</textref>`;
  const badRes = f.ctx.stripTextRefs(badUtf16);
  assert.equal(badRes.refs.length, 0);
  assert.equal(badRes.text, badUtf16);
});

test('over-cap (>64 KiB) warns on paste and refuses send while keeping draft intact', () => {
  const f = buildComposerFixture();
  const huge = '한'.repeat(23000); // 69,000 UTF-8 bytes > 65,536 bytes

  f.pasteOnEditor({ text: huge });
  assert.equal(f.ctx.textRefs.length, 1, 'still collapses into draft');
  assert.ok(f.warns.some(w => w.startsWith('js.textref.overcap.paste:')), 'warns immediately on oversized paste');

  // Attempt send -> refused, zero POSTs, draft stays completely intact
  f.ctx.sendMessage();
  assert.equal(f.posted.length, 0, 'must not POST over-cap message');
  assert.ok(f.warns.some(w => w.startsWith('js.textref.overcap.send:')), 'warns on over-cap send');
  assert.equal(f.ctx.textRefs.length, 1, 'textRefs kept intact after refusal');
  assert.ok(f.ctext.value.includes('[Text #1]'), 'composer text kept intact after refusal');
});

test('sendMessage serializes <textref> inline and outboxBubble hydrates opaquely alongside outer <mailbox>', () => {
  const f = buildComposerFixture();
  const hiddenMailbox = '<mailbox id="id:hidden123" dir="/work/devenv" branch="develop">Hidden</mailbox>';
  const outerMailbox = '<mailbox id="id:outer123456" dir="/work/devenv" branch="develop" harness="claude">Lead A</mailbox>';
  const body = `Check <wb:selection><wb:mention topic="t">x</wb:mention></wb:selection> and [Image #1] and ${hiddenMailbox} ` + 'm'.repeat(1024);
  f.ctext.value = `Ping ${outerMailbox} and inspect `;
  f.ctext.setSelectionRange(f.ctext.value.length, f.ctext.value.length);
  f.pasteOnEditor({ text: body });
  f.ctext.value += ' and literal [Text #9]';

  f.ctx.sendMessage();
  assert.equal(f.ctx.textRefs.length, 0, 'composer cleared on send');
  assert.equal(f.ctext.value, '');

  // Pump outbox promise chain -> builds optimistic outboxBubble
  return Promise.resolve().then(() => {
    assert.equal(f.posted.length, 1);
    const wire = f.posted[0].body.text;
    assert.ok(wire.includes('<textref id="#1" bytes="' + Buffer.byteLength(body, 'utf8') + '">'));
    const bubble = f.clive.querySelector('.msg.user.held');
    assert.ok(bubble, 'optimistic held bubble rendered');
    const tag = bubble.querySelector('.txttag');
    assert.ok(tag, 'matched [Text #1] hydrated as .txttag');
    const mbPills = bubble.querySelectorAll('.icapsule.icapsule-mailbox');
    assert.equal(mbPills.length, 1, 'only outer <mailbox> hydrates; <mailbox> inside <textref> stays opaque');
    assert.equal(mbPills[0].textContent, 'devenv:develop · Lead A');
    assert.equal(bubble.querySelector('.icapsule-selection'), null, 'selection inside textref stayed opaque');
    const thumb = bubble.querySelector('.thumbs .textref-thumb');
    assert.ok(thumb, '56x56 .textref-thumb rendered in .thumbs');
    assert.equal(thumb.getAttribute('data-textref'), '1');
    const tplPre = thumb.querySelector('.icapsule-pop-textref');
    assert.equal(tplPre.textContent, body);
  });
});

test('restoreHeld extracts & renumbers <textref> blocks above current txtSeq', () => {
  const f = buildComposerFixture();
  const existing = 'e'.repeat(1050);
  const cancelled = 'c'.repeat(1050);
  f.pasteOnEditor({ text: existing });
  assert.equal(f.ctx.textRefs[0].n, 1);

  f.ctx.restoreHeld([{
    text: 'Look [Text #1]\n\n' + f.ctx.textRefBlock(1, cancelled),
    sentAt: Date.now(),
  }]);
  assert.equal(f.ctx.textRefs.length, 2);
  assert.equal(f.ctx.textRefs[0].n, 1);
  assert.equal(f.ctx.textRefs[0].body, existing);
  assert.equal(f.ctx.textRefs[1].n, 2);
  assert.equal(f.ctx.textRefs[1].body, cancelled);
  assert.ok(f.ctext.value.includes('Look [Text #2]'));
  assert.ok(!f.ctext.value.includes('<textref'));
});

test('WsRail.emptyDraft recognizes textRefs', () => {
  const f = buildComposerFixture();
  assert.equal(f.ctx.WsRail.emptyDraft({ text: '', textRefs: [] }), true);
  assert.equal(f.ctx.WsRail.emptyDraft({ text: '', textRefs: [{ n: 2, body: 'abc' }] }), false);
});

test('draft save/restore round-trips textRefs and preserves txtSeq gaps', () => {
  const lifeSrc = readAssetSource('wsdashboard', 'dashboard-lifecycle.js');
  const readStart = lifeSrc.indexOf('  function readDraft() {');
  const readEnd = lifeSrc.indexOf('  // The key leaves out record.projection');
  const restoreStart = lifeSrc.indexOf('  function restoreDraft(saved, recoveredImages, mine) {');
  const restoreEnd = lifeSrc.indexOf('  if (draftStore) {');
  assert.ok(readStart > 0 && readEnd > readStart && restoreStart > 0 && restoreEnd > restoreStart);
  const draftCode = lifeSrc.slice(readStart, readEnd) + '\n' + lifeSrc.slice(restoreStart, restoreEnd);

  function loadDraftFns(f) {
    Object.assign(f.ctx, {
      openState: {},
      boardIncarnation: 'inc-1',
      boardDraft: d => d,
      selectionRanges: r => r || [],
      ccmd: null,
      liveMention: () => false,
      mentionWas: '',
      paintMentions() {},
      restoreOpen() {},
      restoreDraftMarkers() {},
      attachSrc: r => '/a/' + r,
    });
    vm.runInContext(draftCode, f.ctx);
  }

  const f1 = buildComposerFixture();
  loadDraftFns(f1);
  const body1 = '1'.repeat(1030);
  const body2 = '2'.repeat(1040);
  const body3 = '3'.repeat(1050);

  f1.pasteOnEditor({ text: body1 });
  f1.pasteOnEditor({ text: body2 });
  f1.pasteOnEditor({ text: body3 });
  assert.equal(f1.ctx.txtSeq, 3);

  // Remove #1 and #3 -> leaves a gap (#2 only, txtSeq stays 3)
  f1.ctx.unattachTextRef(1);
  f1.ctx.unattachTextRef(3);
  assert.deepEqual(Array.from(f1.ctx.textRefs, r => r.n), [2]);
  assert.equal(f1.ctx.txtSeq, 3);

  const saved = JSON.parse(JSON.stringify(f1.ctx.readDraft()));
  assert.equal(saved.txtSeq, 3);
  assert.deepEqual(saved.textRefs, [{ n: 2, body: body2 }]);

  // Restore into a fresh session fixture
  const f2 = buildComposerFixture();
  loadDraftFns(f2);
  f2.ctx.restoreDraft(saved, [], null);
  assert.deepEqual(Array.from(f2.ctx.textRefs, r => r.n), [2]);
  assert.equal(f2.ctx.textRefs[0].body, body2);
  assert.equal(f2.ctx.txtSeq, 3);
  assert.equal(f2.cimgs.querySelectorAll('.txtchip').length, 1);
  assert.equal(f2.cimgs.querySelector('.txtchip').getAttribute('data-txtchip'), '2');

  // Next paste allocates #4 (never reuses #1 or #3)
  f2.ctext.setSelectionRange(f2.ctext.value.length, f2.ctext.value.length);
  f2.pasteOnEditor({ text: '4'.repeat(1060) });
  assert.deepEqual(Array.from(f2.ctx.textRefs, r => r.n), [2, 4]);
  assert.equal(f2.ctx.txtSeq, 4);
  assert.ok(f2.ctext.value.includes('[Text #4]'));
});

test('new textref/popover CSS rules use zero :has() selectors', () => {
  const css = fs.readFileSync(path.join(assetsDir, 'dashboard-conversation.css'), 'utf8');
  const lines = css.split('\n').filter(l => /\.(txtchip|textref|txttag|untxt|ichip|icapsule-pop-textref|icapsule-pop-img)/.test(l));
  assert.ok(lines.length >= 5, 'expected textref/popover CSS rules in dashboard-conversation.css');
  for (const line of lines) {
    assert.equal(/:has\s*\(/.test(line), false, 'forbidden :has() in rule: ' + line);
  }
});
