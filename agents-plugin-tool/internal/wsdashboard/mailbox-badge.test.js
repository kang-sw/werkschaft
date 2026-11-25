const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { readAssetSource } = require('./testdata/asset-source.cjs');

const assetsDir = path.join(__dirname, 'assets');

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
  }
  get value() { return this._value || ''; }
  set value(v) { this._value = v; }
  get classList() {
    return {
      add: c => { this.className = (this.className ? this.className + ' ' : '') + c; },
      contains: c => this.className.split(/\s+/).includes(c),
      toggle() {}
    };
  }
  get children() { return this.childNodes.filter(n => n.nodeType === 1); }
  get firstChild() { return this.childNodes[0]; }
  get previousSibling() { return this.parentNode?.childNodes[this.parentNode.childNodes.indexOf(this) - 1]; }
  get nextSibling() { return this.parentNode?.childNodes[this.parentNode.childNodes.indexOf(this) + 1]; }
  get nextElementSibling() { let n = this.nextSibling; while (n && n.nodeType !== 1) n = n.nextSibling; return n; }
  appendChild(n) { this.childNodes.push(n); n.parentNode = this; return n; }
  insertAdjacentElement(_, n) {
    const p = this.parentNode;
    p.childNodes.splice(p.childNodes.indexOf(this) + 1, 0, n);
    n.parentNode = p;
  }
  remove() {
    if (!this.parentNode) return;
    this.parentNode.childNodes.splice(this.parentNode.childNodes.indexOf(this), 1);
    this.parentNode = null;
  }
  removeChild(n) { n.remove(); }
  set textContent(v) {
    this.childNodes = [];
    if (this.nodeType === 3) this.nodeValue = v;
    else if (v) this.appendChild(textNode(v));
  }
  get textContent() { return this.nodeType === 3 ? this.nodeValue : this.childNodes.map(n => n.textContent).join(''); }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k] ?? null; }
  hasAttribute(k) { return k in this.attrs; }
  removeAttribute(k) { delete this.attrs[k]; }
  contains(n) { return this === n || this.childNodes.some(c => c.contains(n)); }
  matches(s) {
    return s.split(',').some(sel => {
      sel = sel.trim();
      if (!sel) return false;
      const parts = sel.split(/\s+/);
      const last = parts[parts.length - 1];
      const attrMatch = last.match(/\[([^\]]+)\]/);
      const clsPart = last.replace(/\[[^\]]+\]/g, '');
      const classes = clsPart.split('.').filter(Boolean);
      const okClass = classes.every(c => this.classList.contains(c));
      const okAttr = !attrMatch || this.hasAttribute(attrMatch[1]);
      return okClass && okAttr;
    });
  }
  closest(s) { return this.matches(s) ? this : this.parentNode?.closest(s); }
  querySelector(s) { return this.querySelectorAll(s)[0] || null; }
  querySelectorAll(s) {
    const out = [];
    for (const c of this.children) {
      if (c.matches(s)) out.push(c);
      out.push(...c.querySelectorAll(s));
    }
    return out;
  }
  addEventListener(k, f) { (this.listeners[k] ||= []).push(f); }
  dispatchEvent(e) {
    e.target = this;
    for (const f of this.listeners[e.type] || []) {
      f(e);
      if (e.stopped) break;
    }
    return !e.defaultPrevented;
  }
  focus() { this._doc.activeElement = this; }
  blur() { this._doc.activeElement = null; }
  getBoundingClientRect() { return { left: 10, top: 500, bottom: 520, height: 20 }; }
}

function textNode(s) { const n = new El('#text', 3); n.nodeValue = s; return n; }

function makeEnv(viewingOpen) {
  let currentRange = null;
  const selection = {
    get rangeCount() { return currentRange ? 1 : 0; },
    get anchorNode() { return currentRange?.startContainer; },
    get focusNode() { return currentRange?.endContainer; },
    getRangeAt() { return currentRange; },
    removeAllRanges() { currentRange = null; },
    addRange(r) { currentRange = r; }
  };
  const body = new El('body');
  const doc = {
    body,
    activeElement: null,
    createElement(n) { const el = new El(n); el._doc = doc; return el; },
    createTextNode: textNode,
    querySelector(s) { return body.querySelector(s); },
    querySelectorAll(s) { return body.querySelectorAll(s); },
    execCommand() { return false; },
    createRange() {
      return {
        setStart(n, a) { this.startContainer = n; this.startOffset = a; },
        setEnd(n, a) { this.endContainer = n; this.endOffset = a; },
        collapse() {},
        getBoundingClientRect() { return { left: 10, top: 500, bottom: 520, height: 20 }; }
      };
    }
  };
  body._doc = doc;
  if (viewingOpen) {
    const head = doc.createElement('div');
    head.attrs.id = 'r-head';
    const snm = doc.createElement('button');
    snm.className = 'snm';
    snm.setAttribute('data-mailbox-open', viewingOpen);
    head.appendChild(snm);
    body.appendChild(head);
  }
  const ctx = vm.createContext({
    console,
    document: doc,
    Event: Evt,
    KeyboardEvent: Evt,
    MutationObserver: class { observe() {} },
    wsT: (k, v) => (v !== undefined ? `${k}:${v}` : k),
    setTimeout,
    clearTimeout
  });
  ctx.window = ctx;
  ctx.getSelection = () => selection;
  vm.runInContext(fs.readFileSync(path.join(assetsDir, 'capsules.js'), 'utf8'), ctx);
  const field = body.appendChild(doc.createElement('textarea'));
  field.value = '';
  ctx.WsCapsules.attach(field);
  return { ctx, doc, field, C: ctx.WsCapsules };
}

function pasteText(field, text, rich) {
  const data = { 'text/plain': text };
  if (rich !== undefined) data['application/x-wsc-capsules+json'] = rich;
  const e = new Evt('paste', {
    clipboardData: {
      types: Object.keys(data),
      getData: k => data[k] || ''
    }
  });
  field.wsc.editor.dispatchEvent(e);
  return e;
}

test('mailbox parser and dynamic label parity across dir, branch, plain folder, escaping, and malformed inputs', () => {
  const { C } = makeEnv();
  const viewCtx = { dir: '/work/devenv', branch: 'develop' };

  const cases = [
    ['<mailbox id="id:abcdef123456" dir="/work/devenv" branch="develop" harness="claude">Review Worker</mailbox>', 'Review Worker'],
    ['<mailbox id="id:abcdef123456" dir="/work/devenv" branch="develop"/>', 'id:abcdef'],
    ['<mailbox id="id:abcdef123456" dir="/work/devenv" branch="feat/badge">Badge Worker</mailbox>', 'feat/badge · Badge Worker'],
    ['<mailbox id="id:abcdef123456" dir="/work/devenv" branch="feat/badge"/>', 'feat/badge'],
    ['<mailbox id="id:abcdef123456" dir="/work/other-app" root="/work" branch="main" harness="codex">API</mailbox>', 'other-app:main · API'],
    ['<mailbox id="id:abcdef123456" dir="/work/other-app" branch="main"/>', 'other-app:main'],
    ['<mailbox id="id:abcdef123456" dir="/tmp/scratch-notes">Notes</mailbox>', 'scratch-notes · Notes'],
    ['<mailbox id="id:abcdef123456" dir="/tmp/scratch-notes/"/>', 'scratch-notes'],
    ['<mailbox id="id:esc12345" dir="/work/a&amp;b" branch="f&lt;1&gt;" harness="h&quot;x">A &amp; B &lt;3&gt; &amp;lt;lit&amp;gt;</mailbox>', 'a&b:f<1> · A & B <3> &lt;lit&gt;']
  ];
  for (const [raw, wantLabel] of cases) {
    const m = C.parseMailboxTag(raw, 0);
    assert.ok(m, raw);
    assert.equal(m.end, raw.length);
    assert.equal(C.formatMailboxLabel(m.tag, viewCtx), wantLabel);
  }

  // Windows path comparison parity
  const win = C.parseMailboxTag('<mailbox id="id:win123456" dir="C:\\Users\\Dev\\Repo\\" branch="develop">Win</mailbox>', 0);
  assert.equal(C.formatMailboxLabel(win.tag, { dir: 'c:/users/dev/repo', branch: 'develop' }), 'Win');

  // Malformed tags return null
  for (const bad of [
    '<mailbox dir="/work/devenv" branch="develop">NoID</mailbox>',
    '<mailbox id="id:abc" dir="/work/devenv">NoClose',
    '<mailbox id="id:abc" dir="/work/devenv">bad<tag></mailbox>',
    '<mailbox id="id:abc" id="id:def" dir="/work/devenv"/>'
  ]) {
    assert.equal(C.parseMailboxTag(bad, 0), null, bad);
  }
});

test('composer paste converts mailbox tags to inline badges while keeping exact T0 XML in backing textarea', () => {
  const { field } = makeEnv('<mailbox id="id:self0000" dir="/work/devenv" branch="develop" harness="claude"');
  field.focus();

  const tag1 = '<mailbox id="id:abc123def456" dir="/work/devenv" branch="develop" harness="claude">Lead A</mailbox>';
  const tag2 = '<mailbox id="id:998877665544" dir="/other/app" root="/other" branch="main" harness="codex"/>';
  const malformed = '<mailbox id="id:partial" dir="/work/devenv">unclosed';
  const pasted = `Ask ${tag1} and ${tag2} (${malformed})`;

  pasteText(field, pasted);

  assert.equal(field.value, pasted, 'backing textarea keeps exact verbatim XML');
  const snap = field.wsc.snapshot();
  assert.equal(snap.length, 2, 'only the two valid mailbox tags become capsules');
  assert.equal(snap[0].kind, 'mailbox');
  assert.equal(snap[0].label, 'Lead A');
  assert.equal(snap[0].payload.anchor, tag1);
  assert.equal(snap[1].kind, 'mailbox');
  assert.equal(snap[1].label, 'app:main');
  assert.equal(snap[1].payload.anchor, tag2);

  const chips = field.wsc.editor.querySelectorAll('.wsc-capsule.wsc-mailbox');
  assert.equal(chips.length, 2);
  assert.equal(chips[0].textContent, 'Lead A');
  assert.match(chips[0].getAttribute('title'), /id: id:abc123def456/);
  assert.match(chips[0].getAttribute('title'), /harness: claude/);
  assert.equal(chips[1].textContent, 'app:main');
  assert.match(chips[1].getAttribute('title'), /root: \/other/);

  // Draft save/restore round-trip preserves exact XML and badges
  const savedSnap = JSON.parse(JSON.stringify(snap));
  const savedVal = field.value;
  field.value = '';
  field.value = savedVal;
  field.wsc.restore(savedSnap);
  assert.equal(field.value, savedVal);
  assert.equal(field.wsc.editor.querySelectorAll('.wsc-capsule.wsc-mailbox').length, 2);

  // Copy preserves exact verbatim XML in text/plain
  field.setSelectionRange(0, field.value.length);
  const clip = {};
  field.wsc.editor.dispatchEvent(new Evt('copy', {
    clipboardData: { setData: (k, v) => { clip[k] = v; } }
  }));
  assert.equal(clip['text/plain'], pasted);
  assert.ok(clip['application/x-wsc-capsules+json']);
});

test('Ctrl+Shift+V and Cmd+Shift+V share rawPasteBypass accessor and bypass badge conversion once', () => {
  const { field, C } = makeEnv('<mailbox id="id:self0000" dir="/work/devenv" branch="develop"');
  field.focus();
  const tag = '<mailbox id="id:abc123def456" dir="/work/devenv" branch="develop">Worker</mailbox>';

  const observedDuringPaste = [];
  field.addEventListener('paste', () => {
    observedDuringPaste.push(C.rawPasteBypass());
  });

  // Ctrl+Shift+V bypasses conversion and exposes rawPasteBypass() === true during paste
  field.wsc.editor.dispatchEvent(new Evt('keydown', { key: 'V', code: 'KeyV', ctrlKey: true, shiftKey: true }));
  assert.equal(C.rawPasteBypass(), true);
  pasteText(field, tag);
  assert.deepEqual(observedDuringPaste, [true]);
  assert.equal(C.rawPasteBypass(), false, 'flag resets after paste completes');
  assert.equal(field.value, tag);
  assert.equal(field.wsc.snapshot().length, 0, 'Ctrl+Shift+V keeps raw XML as plain text');

  // Next normal Ctrl+V converts again
  field.value = '';
  pasteText(field, tag);
  assert.deepEqual(observedDuringPaste, [true, false]);
  assert.equal(field.wsc.snapshot().length, 1, 'normal paste converts to badge');

  // Cmd+Shift+V also bypasses, and keyup without paste disarms cleanly
  field.value = '';
  field.wsc.editor.dispatchEvent(new Evt('keydown', { key: 'v', code: 'KeyV', metaKey: true, shiftKey: true }));
  assert.equal(C.rawPasteBypass(), true);
  field.wsc.editor.dispatchEvent(new Evt('keyup', { key: 'v', code: 'KeyV', metaKey: true, shiftKey: true }));
  assert.equal(C.rawPasteBypass(), false);
  pasteText(field, tag);
  assert.equal(field.wsc.snapshot().length, 1, 'disarmed keyup does not stick raw bypass');
});

test('textref opacity precedence: mailbox tag inside valid textref is not converted, outside is converted', () => {
  const { field } = makeEnv('<mailbox id="id:self0000" dir="/work/devenv" branch="develop"');
  field.focus();

  const inside = 'prefix <mailbox id="id:in123456" dir="/work/devenv" branch="develop">Hidden</mailbox> 한글';
  const bytes = Buffer.byteLength(inside, 'utf8');
  const outside = '<mailbox id="id:out123456" dir="/work/devenv" branch="develop">Visible</mailbox>';
  const pasted = `<textref id="#1" bytes="${bytes}">${inside}</textref> and ${outside}`;

  pasteText(field, pasted);
  assert.equal(field.value, pasted);
  const snap = field.wsc.snapshot();
  assert.equal(snap.length, 1);
  assert.equal(snap[0].label, 'Visible');
  assert.equal(snap[0].payload.anchor, outside);
});

test('outboxBubble hydrates mailbox tags in #convo with matching labels, tooltips, and textref opacity', () => {
  const { ctx, doc } = makeEnv('<mailbox id="id:self0000" dir="/work/devenv" branch="develop"');
  const src = readAssetSource('wsdashboard', 'dashboard.js');
  const extract = name => {
    const match = src.match(new RegExp('\\n  function ' + name + '\\([^]*?\\n  \\}\\n'));
    assert.ok(match, name);
    return match[0];
  };
  ctx.doc = doc;
  ctx.parseSelections = s => [{ text: s, selection: null }];
  ctx.icon = () => '';
  ctx.now = () => 0;
  ctx.clockText = () => '00:00';
  vm.runInContext(extract('heldAct') + '\n' + extract('outboxBubble'), ctx);

  const inner = '<mailbox id="id:inside123" dir="/work/devenv" branch="develop">Hidden</mailbox>';
  const tag1 = '<mailbox id="id:abc123def456" dir="/work/devenv" branch="develop" harness="claude">Worker</mailbox>';
  const tag2 = '<mailbox id="id:998877665544" dir="/other/repo" branch="main"/>';
  const text = `Ping ${tag1} and ${tag2} <textref id="#1" bytes="${Buffer.byteLength(inner, 'utf8')}">${inner}</textref>`;

  const bubble = ctx.outboxBubble({ id: 'held-1', back: { text }, pics: [] });
  const pills = bubble.querySelectorAll('.icapsule.icapsule-mailbox');
  assert.equal(pills.length, 2);
  assert.equal(pills[0].textContent, 'Worker');
  assert.match(pills[0].getAttribute('title'), /id: id:abc123def456/);
  assert.equal(pills[1].textContent, 'repo:main');
  assert.match(pills[1].getAttribute('title'), /dir: \/other\/repo/);
});
