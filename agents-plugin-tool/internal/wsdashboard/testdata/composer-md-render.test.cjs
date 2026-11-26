const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const ASSETS = path.join(__dirname, '..', 'assets');
const capsulesJS = fs.readFileSync(path.join(ASSETS, 'capsules.js'), 'utf8');
const mdParserJS = fs.readFileSync(path.join(ASSETS, 'md-parser.js'), 'utf8');
const mdEditorJS = fs.readFileSync(path.join(ASSETS, 'md-editor.js'), 'utf8');
const mdCSS = fs.readFileSync(path.join(ASSETS, 'dashboard-composer-md.css'), 'utf8');

function createDomEnv({ forbidLayoutReads = false } = {}) {
  const rafQueue = [];
  let rafSeq = 0;

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
    get lastChild() { return this.childNodes[this.childNodes.length - 1] || null; }
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
      return child;
    }
    removeChild(child) {
      const idx = this.childNodes.indexOf(child);
      if (idx >= 0) {
        this.childNodes.splice(idx, 1);
        child.parentNode = null;
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
    constructor(val) {
      super(3, '#text');
      this.nodeValue = String(val);
    }
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
          if (c.nodeType === 1) {
            if (match(c)) out.push(c);
            walk(c);
          }
        }
      }
      walk(this);
      return out;
    }
    querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
    closest(sel) {
      for (let n = this; n; n = n.parentNode) {
        if (n.nodeType === 1 && sel.startsWith('.') && n.classList.contains(sel.slice(1))) return n;
      }
      return null;
    }
    focus() { doc.activeElement = this; this.dispatchEvent(new EventObj('focus')); }
    blur() { if (doc.activeElement === this) doc.activeElement = null; this.dispatchEvent(new EventObj('blur')); }
    remove() { if (this.parentNode) this.parentNode.removeChild(this); }
  }

  if (forbidLayoutReads) {
    for (const prop of ['scrollHeight', 'offsetHeight', 'offsetWidth', 'clientHeight', 'clientWidth']) {
      Object.defineProperty(Element.prototype, prop, {
        get() { throw new Error('Forbidden synchronous layout read: ' + prop); },
      });
    }
    Element.prototype.getBoundingClientRect = () => { throw new Error('Forbidden synchronous layout read: getBoundingClientRect'); };
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

  const win = {
    document: doc,
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

  function flushRaf() {
    const q = rafQueue.splice(0);
    for (const item of q) item.fn();
  }

  return { win, doc, selection, flushRaf, rafCount: () => rafQueue.length };
}

function collectElements(root, out = []) {
  for (const c of root.childNodes) {
    if (c.nodeType === 1) {
      out.push(c);
      collectElements(c, out);
    }
  }
  return out;
}

test('WsMdParser covers full-spec CommonMark/GFM constructs with exact offset round-trip', () => {
  const { win } = createDomEnv();
  const parse = (text, mentions = [], capsules = []) => win.WsMdParser.parse(text, mentions, capsules);

  // 1. ATX Headings H1..H6 + optional closing hashes
  const headings = '# H1\n## H2 ##\n### H3\n#### H4\n##### H5\n###### H6';
  const hp = parse(headings);
  assert.equal(hp.lines.length, 6);
  for (let lvl = 1; lvl <= 6; lvl++) {
    assert.match(hp.lines[lvl - 1].blockClass, new RegExp(`wsmd-h${lvl}`));
    assert.match(hp.lines[lvl - 1].blockClass, /wsmd-head/);
  }
  // Closing hashes marked as wsmd-marker on H2
  const h2Tokens = JSON.parse(JSON.stringify(hp.lines[1].tokens.map(t => [headings.slice(t.start, t.end), t.cls])));
  assert.deepEqual(h2Tokens, [['## ', 'wsmd-marker'], ['H2', ''], [' ##', 'wsmd-marker']]);

  // 2. Setext headings === (H1) and --- (H2) vs Horizontal Rules
  const setext = 'Title One\n===\n\nTitle Two\n---\n\n---\n***\n___\n- - -';
  const sp = parse(setext);
  assert.match(sp.lines[0].blockClass, /wsmd-h1/);
  assert.equal(sp.lines[1].blockClass, 'wsmd-setext-marker');
  assert.match(sp.lines[3].blockClass, /wsmd-h2/);
  assert.equal(sp.lines[4].blockClass, 'wsmd-setext-marker');
  for (const idx of [6, 7, 8, 9]) {
    assert.equal(sp.lines[idx].blockClass, 'wsmd-hr', `line ${idx} should be HR`);
  }

  // 3. Fenced (``` and ~~~) and 4-space / tab indented code blocks suppress markdown & mentions
  const codeDoc = '```js\n# not heading\n@src/a.js\n```\n\n~~~py\n**nope**\n~~~\n\n    indented_code @src/b.js\n\ttab_code';
  const m1 = codeDoc.indexOf('@src/a.js');
  const m2 = codeDoc.indexOf('@src/b.js');
  const cp = parse(codeDoc, [
    { start: m1, end: m1 + 9, text: '@src/a.js' },
    { start: m2, end: m2 + 9, text: '@src/b.js' },
  ]);
  assert.equal(cp.lines[0].blockClass, 'wsmd-fence-open');
  assert.ok(cp.lines[0].tokens.some(t => t.cls.includes('wsmd-code-lang') && codeDoc.slice(t.start, t.end) === 'js'));
  assert.equal(cp.lines[1].blockClass, 'wsmd-fence-body');
  assert.equal(cp.lines[2].blockClass, 'wsmd-fence-body');
  assert.equal(cp.lines[3].blockClass, 'wsmd-fence-close');
  assert.equal(cp.lines[5].blockClass, 'wsmd-fence-open');
  assert.equal(cp.lines[6].blockClass, 'wsmd-fence-body');
  assert.equal(cp.lines[9].blockClass, 'wsmd-code-block');
  assert.equal(cp.lines[10].blockClass, 'wsmd-code-block');
  // Mentions suppressed inside both fenced and indented code
  assert.ok(!cp.lines[2].tokens.some(t => t.cls.includes('wsmd-mention')));
  assert.ok(!cp.lines[9].tokens.some(t => t.cls.includes('wsmd-mention')));

  // 4. Inline code (single & multi-backtick), bold, italic, bold+italic, strike, snake_case, escapes
  const inline = 'Use ``a `b` **no**`` + **bold** + *em* + ***both*** + ~~del~~ + snake_case_var + \\*escaped\\*';
  const ip = parse(inline);
  const toks = ip.lines[0].tokens.map(t => [inline.slice(t.start, t.end), t.cls]);
  assert.ok(toks.some(([s, c]) => s === 'a `b` **no**' && c.includes('wsmd-code') && !c.includes('wsmd-strong')));
  assert.ok(toks.some(([s, c]) => s === 'bold' && c.includes('wsmd-strong')));
  assert.ok(toks.some(([s, c]) => s === 'em' && c.includes('wsmd-em')));
  assert.ok(toks.some(([s, c]) => s === 'both' && c.includes('wsmd-strong') && c.includes('wsmd-em')));
  assert.ok(toks.some(([s, c]) => s === 'del' && c.includes('wsmd-strike')));
  assert.ok(toks.some(([s, c]) => s.includes('snake_case_var') && !c.includes('wsmd-em')));
  assert.ok(toks.some(([s, c]) => s === '\\' && c.includes('wsmd-escape-slash')));
  assert.ok(!toks.some(([s, c]) => s === 'escaped' && c.includes('wsmd-em')));

  // 5. Blockquotes (nested > >), unordered/ordered/task lists + nested indent
  const blocks = '> quote\n> > nested\n- bullet\n  - nested\n1. ordered\n- [ ] todo\n- [x] done';
  const bp = parse(blocks);
  assert.equal(bp.lines[0].qDepth, 1);
  assert.equal(bp.lines[1].qDepth, 2);
  assert.equal(bp.lines[2].blockClass, 'wsmd-list');
  assert.equal(bp.lines[3].indent, 1);
  assert.equal(bp.lines[4].blockClass, 'wsmd-list');
  assert.ok(bp.lines[5].tokens.some(t => t.cls.includes('wsmd-task-open')));
  assert.ok(bp.lines[6].tokens.some(t => t.cls.includes('wsmd-task-done')));

  // 6. Links, autolinks, reference links, images, and [Image #N]/[Text #N] exclusions
  const links = '[site](https://a.test) <https://b.test> <u@b.test> https://c.test [ref][r1] ![alt](img.png) [Image #1](x) [Text #2](y)\n[r1]: https://ref.test "Ref"';
  const lp = parse(links);
  const ltoks = lp.lines[0].tokens.map(t => [links.slice(t.start, t.end), t.cls]);
  assert.ok(ltoks.some(([s, c]) => s === 'site' && c.includes('wsmd-link')));
  assert.ok(ltoks.some(([s, c]) => s === 'https://b.test' && c.includes('wsmd-autolink')));
  assert.ok(ltoks.some(([s, c]) => s === 'u@b.test' && c.includes('wsmd-autolink')));
  assert.ok(ltoks.some(([s, c]) => s === 'https://c.test' && c.includes('wsmd-autolink')));
  assert.ok(ltoks.some(([s, c]) => s === 'ref' && c.includes('wsmd-link')));
  assert.ok(ltoks.some(([s, c]) => s === 'alt' && c.includes('wsmd-img-alt')));
  assert.ok(!ltoks.some(([s, c]) => s.includes('Image #1') && c.includes('wsmd-link')));
  assert.ok(!ltoks.some(([s, c]) => s.includes('Text #2') && c.includes('wsmd-link')));
  assert.equal(lp.lines[1].blockClass, 'wsmd-refdef');

  // 7. GFM tables (styled text, no <table>)
  const tbl = '| Name | Value |\n| --- | ---: |\n| **foo** | `bar` |';
  const tp = parse(tbl);
  assert.equal(tp.lines[0].blockClass, 'wsmd-table-head');
  assert.equal(tp.lines[1].blockClass, 'wsmd-table-delim');
  assert.equal(tp.lines[2].blockClass, 'wsmd-table-row');
  assert.ok(tp.lines[2].tokens.some(t => t.cls.includes('wsmd-strong') && tbl.slice(t.start, t.end) === 'foo'));
  assert.ok(tp.lines[2].tokens.some(t => t.cls.includes('wsmd-code') && tbl.slice(t.start, t.end) === 'bar'));

  // 8. Unclassifiable constructs remain plain text
  const unclosed = '*unclosed **also unclosed `no close [link';
  const up = parse(unclosed);
  assert.deepEqual(JSON.parse(JSON.stringify(up.lines[0].tokens)), [{ start: 0, end: unclosed.length, cls: '' }]);
});

test('WsMdEditor renders strictly <span> elements (zero DIV/P/HR/TABLE, zero HTML injection) and preserves WsCapsules round-trip', () => {
  const { win, doc } = createDomEnv();
  const field = doc.createElement('textarea');
  doc.body.appendChild(field);
  const wsc = win.WsCapsules.attach(field);

  let mentions = [];
  let bang = false;
  const inst = win.WsMdEditor.attach(wsc.editor, {
    getMentions: () => mentions,
    isBang: () => bang,
  });

  const md = '# Heading with **bold** and <script>alert(1)</script>\n---\n| A | B |\n| - | - |\n| 1 | 2 |\nCheck @src/app.js';
  const mAt = md.indexOf('@src/app.js');
  mentions = [{ start: mAt, end: mAt + 11, text: '@src/app.js' }];
  field.value = md;
  inst.reconcile();

  // Verify zero disallowed elements (no DIV, P, HR, TABLE, SCRIPT)
  const els = collectElements(wsc.editor);
  for (const el of els) {
    assert.ok(el.nodeName === 'SPAN' || el.nodeName === 'BR', 'only SPAN or trailing BR allowed, got ' + el.nodeName);
  }
  assert.equal(field.value, md, 'ctext.value byte-identical');
  assert.ok(wsc.editor.querySelector('.wsmd-h1'), 'H1 styled span present');
  assert.ok(wsc.editor.querySelector('.wsmd-hr'), 'HR styled span present');
  assert.ok(wsc.editor.querySelector('.wsmd-table-head'), 'table head span present');
  assert.ok(wsc.editor.querySelector('.wsmd-mention'), '@ mention span present');

  // Insert atomic capsule inside heading and verify exact DOM node + canonical text preservation
  wsc.insertCapsule('ticket', { anchor: '[261126-test]', label: 'Test Ticket', title: 'Ticket' }, { at: 10 });
  const expectedWithCap = field.value;
  const capBefore = wsc.editor.querySelector('[data-wsc-kind="ticket"]');
  assert.ok(capBefore, 'capsule mounted inside styled heading');
  assert.equal(wsc.snapshot().length, 1);

  // Re-trigger input and verify capsule identity & canonical text survive byte-for-byte
  inst.reconcile();
  const capAfter = wsc.editor.querySelector('[data-wsc-kind="ticket"]');
  assert.equal(capAfter, capBefore, 'exact capsule DOM node preserved');
  assert.equal(field.value, expectedWithCap);
  // Trigger native DOM read via input event on editor to confirm capsules.js read() parity
  wsc.editor.dispatchEvent(new win.Event('input', { bubbles: true }));
  assert.equal(field.value, expectedWithCap, 'capsules.js read() matches canonical value exactly');
  assert.equal(wsc.snapshot().length, 1);

  inst.detach();
});

test('WsMdEditor suppresses markdown in bang mode, restores on exit, isolates multiple instances, and performs zero layout reads', () => {
  const { win, doc, flushRaf } = createDomEnv({ forbidLayoutReads: true });

  const f1 = doc.createElement('textarea');
  const f2 = doc.createElement('textarea');
  doc.body.appendChild(f1);
  doc.body.appendChild(f2);
  const w1 = win.WsCapsules.attach(f1);
  const w2 = win.WsCapsules.attach(f2);

  let bang1 = false, reconciled1 = 0;
  const e1 = win.WsMdEditor.attach(w1.editor, {
    isBang: () => bang1,
    onReconciled: () => { reconciled1++; },
  });
  const e2 = win.WsMdEditor.attach(w2.editor, {
    isBang: () => false,
  });

  // No layout reads during value update / input / reconcile
  f1.value = '# Heading **bold**';
  f2.value = '## Second *italic*';
  e1.reconcile();
  e2.reconcile();
  assert.ok(w1.editor.querySelector('.wsmd-h1'));
  assert.ok(w2.editor.querySelector('.wsmd-h2'));

  // Idempotent no-op fast path when nothing changed
  const prevCount = reconciled1;
  e1.reconcile();
  assert.equal(reconciled1, prevCount, 'unchanged reconcile is a zero-work no-op');

  // Toggle bang mode on editor 1 only — editor 2 remains styled
  bang1 = true;
  f1.value = '!# Heading **bold**';
  e1.reconcile();
  assert.equal(w1.editor.querySelector('.wsmd-line'), null, 'bang mode strips markdown wrappers');
  assert.equal(w1.editor.firstChild.nodeType, 3, 'leading Text node available for ws-bang');
  assert.equal(w1.editor.firstChild.nodeValue.charAt(0), '!');
  assert.ok(w2.editor.querySelector('.wsmd-h2'), 'second instance unaffected by first instance bang mode');

  // Exit bang mode — markdown styling restores immediately
  bang1 = false;
  f1.value = '# Heading **bold**';
  e1.reconcile();
  assert.ok(w1.editor.querySelector('.wsmd-h1'), 'markdown styling restored after leaving bang mode');

  // rAF schedule also performs zero layout reads
  f1.dispatchEvent(new win.Event('input', { bubbles: true }));
  flushRaf();

  e1.detach();
  e2.detach();
});

test('dashboard-composer-md.css declares wsmd prefix, scaled heading hierarchy, monospace code, and capsule typography reset', () => {
  assert.match(mdCSS, /class prefix:\s*wsmd/);
  assert.match(mdCSS, /\.wsmd-h1\s*\{[^}]*font-size:/);
  assert.match(mdCSS, /\.wsmd-h6\s*\{[^}]*font-size:/);
  assert.match(mdCSS, /\.wsmd-code\s*\{[^}]*var\(--wb-mono\)/);
  assert.match(mdCSS, /\.wsc-editor\s+\.wsmd-line\s+\.wsc-capsule\s*\{[^}]*font-size:\s*12px/);
});
