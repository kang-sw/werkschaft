const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const ASSETS = path.join(__dirname, '..', 'assets');
const capsulesJS = fs.readFileSync(path.join(ASSETS, 'capsules.js'), 'utf8');
const mdParserJS = fs.readFileSync(path.join(ASSETS, 'md-parser.js'), 'utf8');
const mdEditorJS = fs.readFileSync(path.join(ASSETS, 'md-editor.js'), 'utf8');

function createHarness() {
  const rafQueue = [];
  let rafSeq = 0;
  let now = 10000;

  class EventObj {
    constructor(type, init = {}) {
      this.type = type;
      this.bubbles = !!init.bubbles;
      this.cancelable = !!init.cancelable;
      this.defaultPrevented = false;
      this.detail = init.detail;
      this._stopped = false;
      this.key = init.key || '';
      this.code = init.code || '';
      this.keyCode = init.keyCode || 0;
      this.ctrlKey = !!init.ctrlKey;
      this.metaKey = !!init.metaKey;
      this.shiftKey = !!init.shiftKey;
      this.altKey = !!init.altKey;
      this.isComposing = !!init.isComposing;
      this.inputType = init.inputType || '';
      this.data = init.data || null;
    }
    preventDefault() { if (this.cancelable) this.defaultPrevented = true; }
    stopPropagation() { this._stopped = true; }
    stopImmediatePropagation() { this._stopped = true; }
  }

  class Node {
    constructor(nodeType, nodeName) {
      this.nodeType = nodeType;
      this.nodeName = nodeName;
      this.parentNode = null;
      this.childNodes = [];
      this.ownerDocument = doc;
      this._listeners = [];
    }
    get previousSibling() {
      if (!this.parentNode) return null;
      const idx = this.parentNode.childNodes.indexOf(this);
      return idx > 0 ? this.parentNode.childNodes[idx - 1] : null;
    }
    get nextSibling() {
      if (!this.parentNode) return null;
      const idx = this.parentNode.childNodes.indexOf(this);
      return idx >= 0 && idx + 1 < this.parentNode.childNodes.length ? this.parentNode.childNodes[idx + 1] : null;
    }
    get firstChild() { return this.childNodes[0] || null; }
    contains(other) {
      for (let n = other; n; n = n.parentNode) if (n === this) return true;
      return false;
    }
    appendChild(child) {
      if (child.nodeType === 11) {
        const items = child.childNodes.slice();
        for (const item of items) this.appendChild(item);
        return child;
      }
      if (child.parentNode) child.parentNode.removeChild(child);
      child.parentNode = this;
      this.childNodes.push(child);
      if (this._onMutate) this._onMutate();
      return child;
    }
    removeChild(child) {
      const idx = this.childNodes.indexOf(child);
      if (idx >= 0) {
        this.childNodes.splice(idx, 1);
        child.parentNode = null;
        if (this._onMutate) this._onMutate();
      }
      return child;
    }
    addEventListener(type, fn, capture = false) {
      this._listeners.push({ type, fn, capture: !!capture });
    }
    removeEventListener(type, fn, capture = false) {
      const cap = !!capture;
      this._listeners = this._listeners.filter(l => !(l.type === type && l.fn === fn && l.capture === cap));
    }
    dispatchEvent(ev) {
      ev.target = this;
      for (const l of this._listeners.filter(x => x.type === ev.type && x.capture)) {
        l.fn.call(this, ev);
        if (ev._stopped) return !ev.defaultPrevented;
      }
      for (const l of this._listeners.filter(x => x.type === ev.type && !x.capture)) {
        l.fn.call(this, ev);
        if (ev._stopped) return !ev.defaultPrevented;
      }
      return !ev.defaultPrevented;
    }
  }

  class TextNode extends Node {
    constructor(val) { super(3, '#text'); this.nodeValue = String(val); }
    get textContent() { return this.nodeValue; }
    set textContent(v) { this.nodeValue = String(v); }
  }
  class Fragment extends Node {
    constructor() { super(11, '#document-fragment'); }
  }
  class Element extends Node {
    constructor(tag) {
      super(1, tag.toUpperCase());
      this._attrs = Object.create(null);
      this._value = '';
      this.hidden = false;
      this.disabled = false;
      this.isConnected = true;
      const self = this;
      this.classList = {
        add(...cls) {
          const cur = (self.className || '').split(/\s+/).filter(Boolean);
          for (const c of cls) if (!cur.includes(c)) cur.push(c);
          self.className = cur.join(' ');
        },
        contains(c) { return (self.className || '').split(/\s+/).includes(c); },
      };
    }
    get className() { return this._attrs.class || ''; }
    set className(v) { this._attrs.class = String(v); }
    get nextElementSibling() {
      let s = this.nextSibling;
      while (s && s.nodeType !== 1) s = s.nextSibling;
      return s;
    }
    get textContent() {
      let s = '';
      for (const c of this.childNodes) s += c.textContent;
      return s;
    }
    set textContent(v) {
      for (const c of this.childNodes) c.parentNode = null;
      this.childNodes = [];
      const str = String(v == null ? '' : v);
      if (str.length) this.appendChild(new TextNode(str));
      else if (this._onMutate) this._onMutate();
    }
    setAttribute(k, v) { this._attrs[k] = String(v); }
    getAttribute(k) { return Object.prototype.hasOwnProperty.call(this._attrs, k) ? this._attrs[k] : null; }
    hasAttribute(k) { return Object.prototype.hasOwnProperty.call(this._attrs, k); }
    removeAttribute(k) { delete this._attrs[k]; }
    insertAdjacentElement(pos, el) {
      if (pos === 'afterend' && this.parentNode) {
        const idx = this.parentNode.childNodes.indexOf(this);
        el.parentNode = this.parentNode;
        this.parentNode.childNodes.splice(idx + 1, 0, el);
      }
      return el;
    }
    querySelectorAll(sel) {
      const out = [];
      function match(el) {
        const am = /^\[([^=\]]+)(?:="([^"]*)")?\]$/.exec(sel);
        if (am) return am[2] === undefined ? el.hasAttribute(am[1]) : el.getAttribute(am[1]) === am[2];
        if (sel.startsWith('.')) return el.classList.contains(sel.slice(1));
        return el.nodeName === sel.toUpperCase();
      }
      function walk(n) {
        for (const c of n.childNodes) {
          if (c.nodeType === 1) { if (match(c)) out.push(c); walk(c); }
        }
      }
      walk(this);
      return out;
    }
    querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
    focus() { doc.activeElement = this; this.dispatchEvent(new EventObj('focus')); }
    blur() { if (doc.activeElement === this) doc.activeElement = null; this.dispatchEvent(new EventObj('blur')); }
    remove() { if (this.parentNode) this.parentNode.removeChild(this); }
  }
  class TextArea extends Element {
    constructor() { super('textarea'); }
    get value() { return this._value; }
    set value(v) { this._value = String(v); }
  }

  const selection = {
    anchorNode: null, anchorOffset: 0, focusNode: null, focusOffset: 0, _range: null,
    get rangeCount() { return this._range ? 1 : 0; },
    getRangeAt(i) { if (i !== 0 || !this._range) throw new Error('no range'); return this._range; },
    removeAllRanges() { this._range = null; this.anchorNode = null; this.focusNode = null; },
    addRange(r) {
      this._range = r;
      this.anchorNode = r.startContainer; this.anchorOffset = r.startOffset;
      this.focusNode = r.endContainer; this.focusOffset = r.endOffset;
    },
    setBaseAndExtent(an, ao, fn, fo) {
      this.anchorNode = an; this.anchorOffset = ao; this.focusNode = fn; this.focusOffset = fo;
      this._range = { startContainer: an, startOffset: ao, endContainer: fn, endOffset: fo };
    },
  };

  const doc = {
    activeElement: null,
    defaultView: null,
    body: null,
    createElement(tag) { return tag.toLowerCase() === 'textarea' ? new TextArea() : new Element(tag); },
    createTextNode(t) { return new TextNode(t); },
    createDocumentFragment() { return new Fragment(); },
    createRange() {
      return {
        startContainer: null, startOffset: 0, endContainer: null, endOffset: 0,
        setStart(n, o) { this.startContainer = n; this.startOffset = o; },
        setEnd(n, o) { this.endContainer = n; this.endOffset = o; },
        collapse(toStart) {
          if (toStart) { this.endContainer = this.startContainer; this.endOffset = this.startOffset; }
          else { this.startContainer = this.endContainer; this.startOffset = this.endOffset; }
        },
      };
    },
    execCommand() { return false; },
    addEventListener() {},
  };
  doc.body = new Element('body');

  const FakeDate = class extends Date {
    static now() { return now; }
  };

  const win = {
    document: doc,
    Date: FakeDate,
    Event: EventObj,
    CustomEvent: EventObj,
    KeyboardEvent: EventObj,
    MutationObserver: class { observe() {} disconnect() {} },
    getSelection: () => selection,
    requestAnimationFrame(fn) { const id = ++rafSeq; rafQueue.push({ id, fn }); return id; },
    cancelAnimationFrame(id) {
      const idx = rafQueue.findIndex(x => x.id === id);
      if (idx >= 0) rafQueue.splice(idx, 1);
    },
    wsT: k => k,
  };
  doc.defaultView = win;
  win.window = win;

  const ctx = vm.createContext(win);
  vm.runInContext(capsulesJS, ctx);
  vm.runInContext(mdParserJS, ctx);
  vm.runInContext(mdEditorJS, ctx);

  const field = doc.createElement('textarea');
  doc.body.appendChild(field);
  const wsc = win.WsCapsules.attach(field);
  let externalComposing = false;
  const md = win.WsMdEditor.attach(wsc.editor, {
    isComposing: () => externalComposing,
  });

  function flushRaf() {
    const q = rafQueue.splice(0);
    for (const item of q) item.fn();
  }

  // Simulates native browser typing into the focused contenteditable DOM and firing input.
  function typeChars(str, stepMs = 40) {
    field.focus();
    for (const ch of str) {
      now += stepMs;
      const pos = field.selectionEnd;
      const cur = field.value;
      const next = cur.slice(0, pos) + ch + cur.slice(pos);
      // Emulate browser modifying text node and firing editor input
      const tn = doc.createTextNode(next);
      wsc.editor.textContent = '';
      wsc.editor.appendChild(tn);
      selection.setBaseAndExtent(tn, pos + ch.length, tn, pos + ch.length);
      wsc.editor.dispatchEvent(new EventObj('input', { bubbles: true }));
      flushRaf();
    }
  }

  return {
    win, doc, field, wsc, md, selection, EventObj, flushRaf, typeChars,
    advanceTime: ms => { now += ms; },
    setExternalComposing: v => { externalComposing = v; },
  };
}

test('IME composition guard forbids DOM rewrites between compositionstart and compositionend, then reconciles cleanly with caret preserved', () => {
  const h = createHarness();
  h.field.focus();
  h.field.value = '# ';
  h.field.setSelectionRange(2, 2);
  h.md.reconcile();
  assert.ok(h.wsc.editor.querySelector('.wsmd-h1'));

  let mutations = 0;
  h.wsc.editor._onMutate = () => { mutations++; };

  // Start Korean IME composition
  h.wsc.editor.dispatchEvent(new h.EventObj('compositionstart'));
  mutations = 0;

  // Browser inserts interim jamo inside existing text node during composition
  const lineSpan = h.wsc.editor.querySelector('.wsmd-h1');
  const interim = h.doc.createTextNode('ㅇ');
  lineSpan.appendChild(interim);
  h.selection.setBaseAndExtent(interim, 1, interim, 1);

  // Even if input, schedule(), and reconcile() fire mid-composition, editorEl must NOT be rewritten
  h.wsc.editor.dispatchEvent(new h.EventObj('input', { bubbles: true }));
  h.md.schedule();
  h.md.reconcile();
  h.flushRaf();
  assert.equal(mutations, 0, 'zero root DOM mutations while composing');

  // Ctrl+Z during composition must not hijack IME
  const undoDuringIme = new h.EventObj('keydown', { key: 'z', ctrlKey: true, cancelable: true, isComposing: true });
  h.wsc.editor.dispatchEvent(undoDuringIme);
  assert.equal(undoDuringIme.defaultPrevented, false);

  // Commit Hangul + markdown syntax on compositionend
  interim.nodeValue = '안녕하세요 **반갑습니다**';
  h.selection.setBaseAndExtent(interim, interim.nodeValue.length, interim, interim.nodeValue.length);
  h.wsc.editor.dispatchEvent(new h.EventObj('compositionend'));

  assert.equal(h.field.value, '# 안녕하세요 **반갑습니다**');
  assert.ok(h.wsc.editor.querySelector('.wsmd-h1'), 'heading reconciled after compositionend');
  assert.ok(h.wsc.editor.querySelector('.wsmd-strong'), 'bold reconciled after compositionend');
  assert.equal(h.field.selectionEnd, h.field.value.length, 'caret offset preserved after compositionend reconcile');

  // External opts.isComposing() guard also blocks DOM mutations
  h.setExternalComposing(true);
  mutations = 0;
  h.md.reconcile();
  assert.equal(mutations, 0, 'external isComposing() blocks reconcile');
  h.setExternalComposing(false);
});

test('managed undo/redo stack coalesces bursts, splits boundaries, and round-trips styled text + caret via Ctrl+Z, Ctrl+Shift+Z, Ctrl+Y, and beforeinput', () => {
  const h = createHarness();
  h.field.focus();

  // Burst 1: "#" then " Title" (space boundary splits word bursts)
  h.typeChars('#');
  h.typeChars(' Title');
  assert.equal(h.field.value, '# Title');
  assert.ok(h.wsc.editor.querySelector('.wsmd-h1'));

  // Pause > 600ms and type " **bold**"
  h.advanceTime(800);
  h.typeChars(' **bold**');
  assert.equal(h.field.value, '# Title **bold**');
  assert.ok(h.wsc.editor.querySelector('.wsmd-strong'));

  // Ctrl+Z undoes " **bold**" in one word burst
  const evUndo1 = new h.EventObj('keydown', { key: 'z', ctrlKey: true, cancelable: true });
  h.wsc.editor.dispatchEvent(evUndo1);
  assert.equal(evUndo1.defaultPrevented, true);
  assert.equal(h.field.value, '# Title');
  assert.equal(h.field.selectionEnd, '# Title'.length);
  assert.ok(h.wsc.editor.querySelector('.wsmd-h1'));
  assert.equal(h.wsc.editor.querySelector('.wsmd-strong'), null);

  // beforeinput historyUndo undoes " Title" -> "#"
  const evBeforeUndo = new h.EventObj('beforeinput', { inputType: 'historyUndo', cancelable: true });
  h.wsc.editor.dispatchEvent(evBeforeUndo);
  assert.equal(evBeforeUndo.defaultPrevented, true);
  assert.equal(h.field.value, '#');

  // Ctrl+Shift+Z redoes " Title" -> "# Title"
  const evRedo1 = new h.EventObj('keydown', { key: 'Z', ctrlKey: true, shiftKey: true, cancelable: true });
  h.wsc.editor.dispatchEvent(evRedo1);
  assert.equal(evRedo1.defaultPrevented, true);
  assert.equal(h.field.value, '# Title');

  // Ctrl+Y redoes " **bold**" -> "# Title **bold**"
  const evRedo2 = new h.EventObj('keydown', { key: 'y', ctrlKey: true, cancelable: true });
  h.wsc.editor.dispatchEvent(evRedo2);
  assert.equal(evRedo2.defaultPrevented, true);
  assert.equal(h.field.value, '# Title **bold**');
  assert.ok(h.wsc.editor.querySelector('.wsmd-strong'));
  assert.equal(h.field.selectionEnd, h.field.value.length);

  // Undo/redo via beforeinput historyUndo + historyRedo
  h.wsc.editor.dispatchEvent(new h.EventObj('beforeinput', { inputType: 'historyUndo', cancelable: true }));
  assert.equal(h.field.value, '# Title');
  h.wsc.editor.dispatchEvent(new h.EventObj('beforeinput', { inputType: 'historyRedo', cancelable: true }));
  assert.equal(h.field.value, '# Title **bold**');
});

test('managed undo/redo restores atomic WsCapsules across insertion and removal', () => {
  const h = createHarness();
  h.field.focus();

  h.field.value = '# Plan';
  h.field.setSelectionRange(6, 6);
  h.md.reconcile();
  assert.equal(h.wsc.snapshot().length, 0);

  // Insert a topic capsule
  h.wsc.insert('topic', 'auth', 'Auth Flow', { start: 6, end: 6 });
  assert.equal(h.field.value, '# Plan[auth] ');
  assert.equal(h.wsc.snapshot().length, 1);
  assert.ok(h.wsc.editor.querySelector('[data-wsc-kind="topic"]'));
  assert.ok(h.wsc.editor.querySelector('.wsmd-h1'));

  // Insert a generic ticket capsule
  h.wsc.insertCapsule('ticket', { anchor: '[261126-md]', label: 'MD Ticket' });
  assert.equal(h.wsc.snapshot().length, 2);
  assert.ok(h.wsc.editor.querySelector('[data-wsc-kind="ticket"]'));

  // Undo ticket capsule insertion
  h.wsc.editor.dispatchEvent(new h.EventObj('keydown', { key: 'z', ctrlKey: true, cancelable: true }));
  assert.equal(h.field.value, '# Plan[auth] ');
  assert.equal(h.wsc.snapshot().length, 1);
  assert.equal(h.wsc.editor.querySelector('[data-wsc-kind="ticket"]'), null);
  assert.ok(h.wsc.editor.querySelector('[data-wsc-kind="topic"]'));

  // Undo topic capsule insertion
  h.wsc.editor.dispatchEvent(new h.EventObj('keydown', { key: 'z', ctrlKey: true, cancelable: true }));
  assert.equal(h.field.value, '# Plan');
  assert.equal(h.wsc.snapshot().length, 0);
  assert.equal(h.wsc.editor.querySelector('[data-wsc-kind="topic"]'), null);

  // Redo both capsules back
  h.wsc.editor.dispatchEvent(new h.EventObj('keydown', { key: 'y', ctrlKey: true, cancelable: true }));
  assert.equal(h.field.value, '# Plan[auth] ');
  assert.equal(h.wsc.snapshot().length, 1);
  assert.ok(h.wsc.editor.querySelector('[data-wsc-kind="topic"]'));

  h.wsc.editor.dispatchEvent(new h.EventObj('keydown', { key: 'y', ctrlKey: true, cancelable: true }));
  assert.equal(h.wsc.snapshot().length, 2);
  assert.ok(h.wsc.editor.querySelector('[data-wsc-kind="ticket"]'));

  // Remove ticket capsule and verify undo restores it
  h.wsc.remove('ticket', h.wsc.snapshot()[1].key);
  assert.equal(h.wsc.snapshot().length, 1);
  h.wsc.editor.dispatchEvent(new h.EventObj('keydown', { key: 'z', ctrlKey: true, cancelable: true }));
  assert.equal(h.wsc.snapshot().length, 2);
  assert.ok(h.wsc.editor.querySelector('[data-wsc-kind="ticket"]'));

  // Detach cleans up key interception
  h.md.detach();
  const afterDetach = new h.EventObj('keydown', { key: 'z', ctrlKey: true, cancelable: true });
  h.wsc.editor.dispatchEvent(afterDetach);
  assert.equal(afterDetach.defaultPrevented, false);
});
