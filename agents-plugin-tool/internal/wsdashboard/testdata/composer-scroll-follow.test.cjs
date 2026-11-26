// Deterministic unit tests for composer Enter/newline caret scroll-follow.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { readAssetSource } = require('./asset-source.cjs');

const assetsRoot = path.resolve(__dirname, '../assets');
const dashboardJS = readAssetSource('wsdashboard', 'dashboard.js');

function slice(src, from, to) {
  const a = src.indexOf(from);
  const b = src.indexOf(to, a);
  assert(a >= 0 && b > a, 'production anchor exists: ' + from + ' .. ' + to);
  return src.slice(a, b);
}

const revealAndEditBlock = slice(
  dashboardJS,
  '  var revealFrame = 0, revealEl = null, revealMirror = null;',
  '  // The explorer has already resolved'
);

function makeCapsulesHarness({ withRaf = true } = {}) {
  const frames = [];
  let frameId = 0;

  class Evt {
    constructor(type, opts = {}) {
      Object.assign(this, opts);
      this.type = type;
      this.defaultPrevented = !!opts.defaultPrevented;
    }
    preventDefault() { this.defaultPrevented = true; }
    stopPropagation() { this.propagationStopped = true; }
    stopImmediatePropagation() { this.stopped = true; this.propagationStopped = true; }
  }

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
      this.scrollHeight = 160;
      this.clientHeight = 160;
      this._scrollTop = 0;
      this.scrollWrites = [];
      this.rect = { top: 100, bottom: 260, left: 0, height: 160 };
    }
    get scrollTop() { return this._scrollTop; }
    set scrollTop(v) { this.scrollWrites.push(v); this._scrollTop = v; }
    get value() { return this._value || ''; }
    set value(v) { this._value = v; }
    get classList() {
      return {
        add: c => { this.className += ' ' + c; },
        contains: c => this.className.split(/\s+/).includes(c),
        toggle() {},
      };
    }
    get children() { return this.childNodes.filter(n => n.nodeType === 1); }
    get firstChild() { return this.childNodes[0]; }
    get previousSibling() { return this.parentNode?.childNodes[this.parentNode.childNodes.indexOf(this) - 1]; }
    get nextSibling() { return this.parentNode?.childNodes[this.parentNode.childNodes.indexOf(this) + 1]; }
    get nextElementSibling() { let n = this.nextSibling; while (n && n.nodeType !== 1) n = n.nextSibling; return n; }
    appendChild(n) { this.childNodes.push(n); n.parentNode = this; return n; }
    insertAdjacentElement(_, n) { const p = this.parentNode; p.childNodes.splice(p.childNodes.indexOf(this) + 1, 0, n); n.parentNode = p; }
    remove() { if (!this.parentNode) return; this.parentNode.childNodes.splice(this.parentNode.childNodes.indexOf(this), 1); this.parentNode = null; }
    set textContent(v) { this.childNodes = []; if (this.nodeType === 3) this.nodeValue = v; else if (v) this.appendChild(textNode(v)); }
    get textContent() { return this.nodeType === 3 ? this.nodeValue : this.childNodes.map(n => n.textContent).join(''); }
    setAttribute(k, v) { this.attrs[k] = String(v); }
    getAttribute(k) { return this.attrs[k] ?? null; }
    hasAttribute(k) { return k in this.attrs; }
    removeAttribute(k) { delete this.attrs[k]; }
    contains(n) { return this === n || this.childNodes.some(c => c.contains(n)); }
    addEventListener(k, f) { (this.listeners[k] ||= []).push(f); }
    dispatchEvent(e) { e.target = this; for (const f of this.listeners[e.type] || []) { f(e); if (e.stopped) break; } return !e.defaultPrevented; }
    focus() { doc.activeElement = this; }
    blur() { doc.activeElement = null; }
    getBoundingClientRect() { return this.rect; }
  }

  function textNode(s) {
    const n = new El('#text', 3);
    n.nodeValue = s;
    return n;
  }

  let currentRange = null;
  let collapsedRect = { top: 120, bottom: 140, height: 20 };
  let probeRect = { top: 265, bottom: 285, height: 20 };

  function makeRange() {
    return {
      startContainer: null,
      startOffset: 0,
      endContainer: null,
      endOffset: 0,
      setStart(n, a) { this.startContainer = n; this.startOffset = a; },
      setEnd(n, a) { this.endContainer = n; this.endOffset = a; },
      collapse() { this.startContainer = this.endContainer; this.startOffset = this.endOffset; },
      cloneRange() {
        const c = makeRange();
        c.startContainer = this.startContainer;
        c.startOffset = this.startOffset;
        c.endContainer = this.endContainer;
        c.endOffset = this.endOffset;
        return c;
      },
      getBoundingClientRect() {
        const collapsed = this.startContainer === this.endContainer && this.startOffset === this.endOffset;
        return collapsed ? collapsedRect : probeRect;
      },
      getClientRects() {
        const r = this.getBoundingClientRect();
        return r && r.height > 0 ? [r] : [];
      },
    };
  }

  const selection = {
    get rangeCount() { return currentRange ? 1 : 0; },
    get anchorNode() { return currentRange?.startContainer; },
    get focusNode() { return currentRange?.endContainer; },
    getRangeAt() { return currentRange; },
    removeAllRanges() { currentRange = null; },
    addRange(r) { currentRange = r; },
  };

  const doc = {
    body: new El('body'),
    activeElement: null,
    createElement: n => new El(n),
    createTextNode: textNode,
    execCommand() { return false; },
    createRange: makeRange,
    addEventListener() {},
  };

  const ctx = {
    console,
    document: doc,
    doc,
    Event: Evt,
    KeyboardEvent: Evt,
    MutationObserver: class { observe() {} },
    getComputedStyle: () => ({ paddingTop: '8px', paddingBottom: '8px', lineHeight: '20px', fontSize: '13.5px' }),
    getSelection: () => selection,
    wsT: k => k,
    ctext: null,
  };
  if (withRaf) {
    ctx.requestAnimationFrame = fn => { frames.push(fn); return ++frameId; };
  }
  ctx.window = ctx;
  vm.createContext(ctx);
  vm.runInContext(fs.readFileSync(path.join(assetsRoot, 'capsules.js'), 'utf8'), ctx);
  vm.runInContext(revealAndEditBlock, ctx);

  const field = doc.body.appendChild(new El('textarea'));
  ctx.ctext = field;
  ctx.WsCapsules.attach(field);

  return {
    ctx,
    doc,
    field,
    editor: field.wsc.editor,
    frames,
    flush() {
      const q = frames.splice(0);
      q.forEach(fn => fn());
    },
    pressEnter(opts = {}) {
      const e = new Evt('keydown', { key: 'Enter', ...opts });
      field.wsc.editor.dispatchEvent(e);
      return e;
    },
    setCollapsedRect(r) { collapsedRect = r; },
    setProbeRect(r) { probeRect = r; },
  };
}

test('capsules.js Enter at bottom of overflowing .wsc-editor coalesces rAF and scrolls to bottom without redundant writes', () => {
  const h = makeCapsulesHarness();
  h.field.value = 'line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8';
  h.field.focus();
  h.field.setSelectionRange(h.field.value.length, h.field.value.length);
  h.editor.scrollHeight = 240;
  h.editor.clientHeight = 160;
  h.editor._scrollTop = 60;
  h.editor.scrollWrites.length = 0;

  // Pressing Enter twice in the same frame coalesces into 1 rAF.
  h.pressEnter();
  h.pressEnter();
  assert.equal(h.frames.length, 1, 'multiple Enters coalesce to one rAF');
  assert.equal(h.editor.scrollWrites.length, 0, 'no synchronous scrollTop write');

  h.flush();
  assert.deepEqual(h.editor.scrollWrites, [80], 'scrolls to exact maxScroll (240 - 160)');

  // Calling revealCaret again while already at maxScroll writes nothing.
  h.editor.scrollWrites.length = 0;
  h.field.wsc.revealCaret();
  h.flush();
  assert.deepEqual(h.editor.scrollWrites, [], 'already at bottom avoids redundant scrollTop write');
});

test('capsules.js Enter skips reveal during IME composition, when blurred, or when not overflowing', () => {
  const h = makeCapsulesHarness();
  h.field.value = 'line1\nline2';
  h.field.focus();
  h.editor.scrollHeight = 240;
  h.editor.clientHeight = 160;

  h.pressEnter({ isComposing: true });
  h.pressEnter({ keyCode: 229 });
  assert.equal(h.frames.length, 0, 'IME Enter never schedules revealCaret');

  // Blurred before rAF fires -> no scroll write.
  h.pressEnter();
  assert.equal(h.frames.length, 1);
  h.field.blur();
  h.flush();
  assert.deepEqual(h.editor.scrollWrites, [], 'blurred editor skips scroll');

  // Not overflowing -> no scroll write.
  h.field.focus();
  h.editor.scrollHeight = 120;
  h.editor.clientHeight = 160;
  h.pressEnter();
  h.flush();
  assert.deepEqual(h.editor.scrollWrites, [], 'non-overflowing editor skips scroll');
});

test('middle-line Enter in .wsc-editor preserves scroll when visible, scrolls block:nearest when outside viewport, and handles zero-height collapsed range without DOM mutation', () => {
  const h = makeCapsulesHarness();
  h.field.value = 'line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8';
  h.field.focus();
  h.editor.scrollHeight = 300;
  h.editor.clientHeight = 160;
  h.editor.rect = { top: 100, bottom: 260, left: 0, height: 160 };

  // 1) Visible earlier line: scrollTop stays 0 (no jump to bottom).
  h.field.setSelectionRange(5, 5);
  h.editor._scrollTop = 0;
  h.editor.scrollWrites.length = 0;
  h.setCollapsedRect({ top: 128, bottom: 148, height: 20 });
  h.ctx.editField(h.field, 5, 5, '\n');
  assert.equal(h.frames.length, 1);
  h.flush();
  assert.deepEqual(h.editor.scrollWrites, [], 'visible middle line does not jump scrollTop');

  // 2) Line below bottom edge with zero-height collapsed range (Blink empty-line quirk):
  //    fallback measures via probe range without mutating editor.childNodes, and scrolls by exact delta.
  const childCountBefore = h.editor.childNodes.length;
  h.editor._scrollTop = 20;
  h.editor.scrollWrites.length = 0;
  h.setCollapsedRect({ top: 0, bottom: 0, height: 0 });
  h.setProbeRect({ top: 248, bottom: 268, height: 20 }); // edRect.bottom - padBottom = 252 -> delta = +16
  h.ctx.editField(h.field, 6, 6, '\n');
  h.flush();
  assert.equal(h.editor.childNodes.length, childCountBefore, 'zero-height fallback never mutates editor DOM');
  assert.deepEqual(h.editor.scrollWrites, [36], 'scrolls down by exact bottom overflow (20 + (268 - 252))');

  // 3) Line above top edge: scrolls up by exact delta.
  h.editor._scrollTop = 50;
  h.editor.scrollWrites.length = 0;
  h.setCollapsedRect({ top: 96, bottom: 116, height: 20 }); // edRect.top + padTop = 108 -> delta = -12
  h.ctx.editField(h.field, 7, 7, '\n');
  h.flush();
  assert.deepEqual(h.editor.scrollWrites, [38], 'scrolls up by exact top overflow (50 - (108 - 96))');

  // 4) Non-newline editField (e.g. Tab indent "  ") never schedules revealCaret.
  h.ctx.editField(h.field, 0, 0, '  ');
  assert.equal(h.frames.length, 0, 'non-newline editField does not schedule revealCaret');
});

test('plain textarea fallback scrolls at end, uses reusable mirror with trailing-newline ZWSP in middle, and guards missing rAF', () => {
  // Missing rAF guard.
  const noRaf = makeCapsulesHarness({ withRaf: false });
  noRaf.field.value = 'abc';
  noRaf.field.focus();
  assert.doesNotThrow(() => {
    noRaf.pressEnter();
    noRaf.ctx.editField(noRaf.field, 3, 3, '\n');
  });

  // Plain textarea (no .wsc).
  const frames = [];
  let frameId = 0;
  let mirrorCreated = 0;
  let mirrorHeight = 44;
  const parent = {
    children: [],
    appendChild(el) { this.children.push(el); el.parentNode = this; return el; },
    removeChild(el) { this.children = this.children.filter(c => c !== el); el.parentNode = null; },
  };
  const ta = {
    value: 'a\nb\nc\nd\ne\nf\ng\nh',
    selectionStart: 0,
    selectionEnd: 0,
    scrollHeight: 280,
    clientHeight: 160,
    clientWidth: 300,
    scrollTop: 0,
    parentNode: parent,
    style: {},
    focus() { doc.activeElement = this; },
    setSelectionRange(a, b) { this.selectionStart = a; this.selectionEnd = b; },
    setRangeText(s, a, b) {
      this.value = this.value.slice(0, a) + s + this.value.slice(b);
      this.selectionStart = this.selectionEnd = a + s.length;
    },
    dispatchEvent() {},
  };
  const doc = {
    activeElement: ta,
    execCommand() { return false; },
    createElement() {
      mirrorCreated++;
      return {
        className: '',
        style: {},
        textContent: '',
        setAttribute() {},
        get offsetHeight() { return mirrorHeight; },
      };
    },
  };
  const ctx = {
    doc,
    ctext: ta,
    chl: null,
    Event: class { constructor(t) { this.type = t; } },
    requestAnimationFrame(fn) { frames.push(fn); return ++frameId; },
    getComputedStyle: () => ({ paddingTop: '8px', paddingBottom: '8px', lineHeight: '20px', fontSize: '13.5px' }),
  };
  ctx.window = ctx;
  vm.createContext(ctx);
  vm.runInContext(revealAndEditBlock, ctx);

  // Caret at end -> scrolls to maxScroll (280 - 160 = 120) without creating mirror.
  ctx.editField(ta, ta.value.length, ta.value.length, '\n');
  assert.equal(frames.length, 1);
  frames.shift()();
  assert.equal(ta.scrollTop, 120);
  assert.equal(mirrorCreated, 0, 'end-of-text scroll does not create mirror');

  // Caret in middle, visible -> preserves scrollTop and appends \u200b after trailing \n.
  ta.scrollTop = 0;
  mirrorHeight = 56; // caretBottom = 48, caretTop = 28 -> inside [8, 152]
  ctx.editField(ta, 1, 1, '\n');
  frames.shift()();
  assert.equal(ta.scrollTop, 0, 'visible middle line preserves scrollTop');
  assert.equal(mirrorCreated, 1);
  assert.equal(parent.children[0].textContent, 'a\n\u200b', 'mirror appends ZWSP for trailing newline');

  // Caret in middle, below viewport -> scrolls down and reuses mirror.
  mirrorHeight = 200; // caretBottom = 192 > viewBottom 152 -> scrollTop = 192 - 152 = 40
  ctx.editField(ta, 3, 3, '\n');
  frames.shift()();
  assert.equal(ta.scrollTop, 40, 'below-viewport middle line scrolls down to keep caret in view');
  assert.equal(mirrorCreated, 1, 'reuses single mirror element');
});
