"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const assetsDir = path.join(__dirname, "..", "assets");
const mentionSrc = fs.readFileSync(path.join(assetsDir, "mention.js"), "utf8");
const capsulesSrc = fs.readFileSync(path.join(assetsDir, "capsules.js"), "utf8");
const mdParserSrc = fs.readFileSync(path.join(assetsDir, "md-parser.js"), "utf8");
const mdEditorSrc = fs.readFileSync(path.join(assetsDir, "md-editor.js"), "utf8");
const wsInputSrc = fs.readFileSync(path.join(assetsDir, "ws-input.js"), "utf8");

function makeHarness(extraOpts) {
  extraOpts = extraOpts || {};
  class FakeEvent {
    constructor(type, opts) {
      this.type = type;
      this.bubbles = !!(opts && opts.bubbles);
      this.cancelable = !!(opts && opts.cancelable);
      this.defaultPrevented = false;
      this.detail = opts && opts.detail;
      this.target = null;
      this._stoppedImmediate = false;
    }
    preventDefault() { this.defaultPrevented = true; }
    stopImmediatePropagation() { this._stoppedImmediate = true; }
    stopPropagation() {}
  }

  class FakeRange {
    setStart(node, offset) { this.startContainer = node; this.startOffset = offset; }
    setEnd(node, offset) { this.endContainer = node; this.endOffset = offset; }
    collapse(toStart) {
      if (toStart) { this.endContainer = this.startContainer; this.endOffset = this.startOffset; }
      else { this.startContainer = this.endContainer; this.startOffset = this.endOffset; }
    }
    getBoundingClientRect() { return { left: 10, top: 20, bottom: 36, right: 20, width: 10, height: 16 }; }
  }

  class FakeHighlight {
    constructor() { this.ranges = []; this.priority = 0; }
    add(r) { this.ranges.push(r); }
  }

  class FakeNode {
    constructor(nodeType, nodeName, ownerDocument) {
      this.nodeType = nodeType;
      this.nodeName = nodeName;
      this.ownerDocument = ownerDocument;
      this.parentNode = null;
      this.childNodes = [];
    }
    get previousSibling() {
      if (!this.parentNode) return null;
      const i = this.parentNode.childNodes.indexOf(this);
      return i > 0 ? this.parentNode.childNodes[i - 1] : null;
    }
    get nextSibling() {
      if (!this.parentNode) return null;
      const i = this.parentNode.childNodes.indexOf(this);
      return i >= 0 && i + 1 < this.parentNode.childNodes.length ? this.parentNode.childNodes[i + 1] : null;
    }
    get firstChild() { return this.childNodes[0] || null; }
    get lastChild() { return this.childNodes[this.childNodes.length - 1] || null; }
    get nextElementSibling() {
      let s = this.nextSibling;
      while (s && s.nodeType !== 1) s = s.nextSibling;
      return s;
    }
    get isConnected() {
      let cur = this;
      while (cur) {
        if (cur === this.ownerDocument.body) return true;
        cur = cur.parentNode;
      }
      return false;
    }
    contains(other) {
      let cur = other;
      while (cur) {
        if (cur === this) return true;
        cur = cur.parentNode;
      }
      return false;
    }
  }

  class FakeText extends FakeNode {
    constructor(val, ownerDocument) {
      super(3, "#text", ownerDocument);
      this.nodeValue = String(val);
    }
    get textContent() { return this.nodeValue; }
    set textContent(v) { this.nodeValue = String(v); }
  }

  class FakeFragment extends FakeNode {
    constructor(ownerDocument) {
      super(11, "#document-fragment", ownerDocument);
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
      const i = this.childNodes.indexOf(child);
      if (i >= 0) { this.childNodes.splice(i, 1); child.parentNode = null; }
      return child;
    }
  }

  class FakeElement extends FakeNode {
    constructor(tag, ownerDocument) {
      super(1, tag.toUpperCase(), ownerDocument);
      this.tagName = tag.toUpperCase();
      this.attrs = {};
      this.style = { setProperty() {} };
      this.listeners = {};
      this.hidden = false;
      this.id = "";
      this.title = "";
      this.offsetWidth = 200;
      this.offsetHeight = 100;
      this.scrollTop = 0;
      this.scrollLeft = 0;
      this.scrollHeight = 40;
      this.clientHeight = 40;
      this._classList = new Set();
      const self = this;
      this.classList = {
        add(...cs) { cs.forEach((c) => self._classList.add(c)); },
        remove(...cs) { cs.forEach((c) => self._classList.delete(c)); },
        contains(c) { return self._classList.has(c); },
        toggle(c, f) { if (f === undefined) f = !self._classList.has(c); if (f) self._classList.add(c); else self._classList.delete(c); }
      };
      if (this.tagName === "TEXTAREA") {
        this._value = "";
        this._selStart = 0;
        this._selEnd = 0;
      }
    }
    get selectionStart() { return this._selStart || 0; }
    set selectionStart(v) { this._selStart = v; }
    get selectionEnd() { return this._selEnd || 0; }
    set selectionEnd(v) { this._selEnd = v; }
    get className() { return Array.from(this._classList).join(" "); }
    set className(v) { this._classList = new Set(String(v || "").split(/\s+/).filter(Boolean)); }
    get value() { return this._value || ""; }
    set value(v) {
      this._value = String(v == null ? "" : v);
      this._selStart = this._value.length;
      this._selEnd = this._value.length;
    }
    get textContent() {
      return this.childNodes.map((c) => c.textContent).join("");
    }
    set textContent(v) {
      this.childNodes.forEach((c) => { c.parentNode = null; });
      this.childNodes = [];
      if (v) this.appendChild(new FakeText(v, this.ownerDocument));
    }
    setRangeText(rep, start, end, mode) {
      const v = this.value;
      const a = start == null ? this.selectionStart : start;
      const b = end == null ? this.selectionEnd : end;
      this.value = v.slice(0, a) + rep + v.slice(b);
      if (mode === "select") { this._selStart = a; this._selEnd = a + rep.length; }
      else { this._selStart = a + rep.length; this._selEnd = a + rep.length; }
    }
    setSelectionRange(a, b) { this._selStart = a; this._selEnd = b; }
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
      const i = this.childNodes.indexOf(child);
      if (i >= 0) { this.childNodes.splice(i, 1); child.parentNode = null; }
      return child;
    }
    insertAdjacentElement(pos, el) {
      if (pos === "afterend" && this.parentNode) {
        const i = this.parentNode.childNodes.indexOf(this);
        el.parentNode = this.parentNode;
        this.parentNode.childNodes.splice(i + 1, 0, el);
      }
      return el;
    }
    setAttribute(k, v) {
      this.attrs[k] = String(v);
      if (k === "id") this.id = String(v);
      if (k === "open") this.open = true;
    }
    getAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k) ? this.attrs[k] : null; }
    hasAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k); }
    removeAttribute(k) { delete this.attrs[k]; if (k === "open") this.open = false; }
    matches(sel) {
      if (sel === "dialog[open]") return this.tagName === "DIALOG" && (this.open || this.hasAttribute("open"));
      if (sel === ".wsc-capsule") return this._classList.has("wsc-capsule");
      if (sel === ".wsm-opt.on") return this._classList.has("wsm-opt") && this._classList.has("on");
      if (sel === "[data-i]") return this.hasAttribute("data-i");
      if (sel === "[data-wsc-kind]") return this.hasAttribute("data-wsc-kind");
      if (sel.startsWith(".")) {
        const parts = sel.slice(1).split(".");
        return parts.every((c) => this._classList.has(c));
      }
      return false;
    }
    closest(sel) {
      let cur = this;
      while (cur && cur.nodeType === 1) {
        if (cur.matches(sel)) return cur;
        cur = cur.parentNode;
      }
      return null;
    }
    querySelectorAll(sel) {
      const out = [];
      const walk = (n) => {
        for (const c of n.childNodes) {
          if (c.nodeType === 1) {
            if (c.matches(sel)) out.push(c);
            walk(c);
          }
        }
      };
      walk(this);
      return out;
    }
    querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
    addEventListener(type, fn, capture) {
      (this.listeners[type] = this.listeners[type] || []).push({ fn, capture: !!capture });
    }
    removeEventListener(type, fn, capture) {
      if (!this.listeners[type]) return;
      const cap = !!capture;
      this.listeners[type] = this.listeners[type].filter((l) => !(l.fn === fn && l.capture === cap));
    }
    dispatchEvent(ev) {
      ev.target = ev.target || this;
      const all = (this.listeners[ev.type] || []).slice();
      for (const l of all.filter((x) => x.capture)) {
        l.fn.call(this, ev);
        if (ev._stoppedImmediate) return !ev.defaultPrevented;
      }
      for (const l of all.filter((x) => !x.capture)) {
        l.fn.call(this, ev);
        if (ev._stoppedImmediate) break;
      }
      return !ev.defaultPrevented;
    }
    focus() { this.ownerDocument.activeElement = this.wsc ? this.wsc.editor : this; }
    blur() {
      if (this.ownerDocument.activeElement === this || (this.wsc && this.ownerDocument.activeElement === this.wsc.editor)) {
        this.ownerDocument.activeElement = this.ownerDocument.body;
      }
      this.dispatchEvent(new FakeEvent("blur"));
    }
    getBoundingClientRect() { return { left: 10, top: 20, bottom: 56, right: 210, width: 200, height: 36 }; }
  }

  const highlights = new Map();
  const timers = [];
  const doc = {
    body: null,
    activeElement: null,
    defaultView: null,
    createElement(tag) { return new FakeElement(tag, doc); },
    createTextNode(v) { return new FakeText(v, doc); },
    createDocumentFragment() { return new FakeFragment(doc); },
    createRange() { return new FakeRange(); },
    execCommand() { return false; }
  };
  doc.body = new FakeElement("body", doc);
  doc.activeElement = doc.body;

  const selRanges = [];
  const windowObj = {
    document: doc,
    HTMLTextAreaElement: FakeElement,
    CSS: { highlights, supports: () => false },
    Highlight: FakeHighlight,
    Event: FakeEvent,
    CustomEvent: FakeEvent,
    KeyboardEvent: FakeEvent,
    innerWidth: 1200,
    innerHeight: 800,
    MutationObserver: class { observe() {} disconnect() {} },
    getSelection() {
      return {
        rangeCount: selRanges.length,
        anchorNode: selRanges[0] ? selRanges[0].startContainer : null,
        anchorOffset: selRanges[0] ? selRanges[0].startOffset : 0,
        focusNode: selRanges[0] ? selRanges[0].endContainer : null,
        focusOffset: selRanges[0] ? selRanges[0].endOffset : 0,
        getRangeAt(i) { return selRanges[i]; },
        removeAllRanges() { selRanges.length = 0; },
        addRange(r) { selRanges.push(r); }
      };
    },
    getComputedStyle() { return {}; },
    addEventListener() {},
    removeEventListener() {},
    requestAnimationFrame(fn) {
      const id = timers.length + 1;
      timers.push({ id, fn, cleared: false });
      return id;
    },
    cancelAnimationFrame(id) {
      const t = timers.find((x) => x.id === id);
      if (t) t.cleared = true;
    },
    setTimeout(fn) {
      const id = timers.length + 1;
      timers.push({ id, fn, cleared: false });
      return id;
    },
    clearTimeout(id) {
      const t = timers.find((x) => x.id === id);
      if (t) t.cleared = true;
    },
    wsT(k) { return k; }
  };
  doc.defaultView = windowObj;
  windowObj.window = windowObj;
  if (extraOpts.preWsMdEditor) windowObj.WsMdEditor = extraOpts.preWsMdEditor;

  const ctx = vm.createContext(windowObj);
  vm.runInContext(mentionSrc, ctx, { filename: "mention.js" });
  vm.runInContext(capsulesSrc, ctx, { filename: "capsules.js" });
  if (!extraOpts.preWsMdEditor && !extraOpts.noMdEditor) {
    vm.runInContext(mdParserSrc, ctx, { filename: "md-parser.js" });
    vm.runInContext(mdEditorSrc, ctx, { filename: "md-editor.js" });
  }
  vm.runInContext(wsInputSrc, ctx, { filename: "ws-input.js" });

  function flushTimers() {
    while (timers.length) {
      const batch = timers.splice(0, timers.length);
      for (const t of batch) {
        if (!t.cleared) t.fn();
      }
    }
  }

  return { window: windowObj, doc, highlights, FakeEvent, flushTimers };
}

test("two simultaneous WsInput instances coexist with isolated state and structured WsMdEditor DOM", () => {
  const { window, doc, flushTimers } = makeHarness();
  const aField = doc.createElement("textarea");
  const bField = doc.createElement("textarea");
  doc.body.appendChild(aField);
  doc.body.appendChild(bField);

  const drafts = { a: "", b: "" };
  let aReconciled = 0;
  const aInst = window.WsInput.attach(aField, {
    draftAdapter: { get: () => drafts.a, set: (v) => { drafts.a = v; } },
    features: { bangMode: true },
    onMarkdownReconciled: () => { aReconciled++; },
    providers: { files: (q, done) => done([{ path: "src/a.go", dir: false }]) }
  });
  const bInst = window.WsInput.attach(bField, {
    draftAdapter: { get: () => drafts.b, set: (v) => { drafts.b = v; } },
    features: { bangMode: false },
    providers: { files: (q, done) => done([{ path: "src/b.go", dir: false }]) }
  });

  aInst.setValue("**alpha** and `codeA`");
  bInst.setValue("**beta** and *emB*");
  flushTimers();

  assert.equal(drafts.a, "**alpha** and `codeA`");
  assert.equal(drafts.b, "**beta** and *emB*");

  // Both instances render structured live markdown DOM independently
  assert.ok(aInst.editor.querySelector(".wsmd-strong"), "instance A renders .wsmd-strong");
  assert.ok(aInst.editor.querySelector(".wsmd-code"), "instance A renders .wsmd-code");
  assert.ok(bInst.editor.querySelector(".wsmd-strong"), "instance B renders .wsmd-strong");
  assert.ok(bInst.editor.querySelector(".wsmd-em"), "instance B renders .wsmd-em");
  assert.ok(aReconciled >= 1, "onMarkdownReconciled callback fired for instance A");

  // Entering !bang mode on instance A flattens ONLY A; B's structured markdown survives intact
  aInst.setValue("!ls -la");
  flushTimers();
  assert.equal(aInst.editor.querySelector(".wsmd-strong"), null, "instance A flattened in bang mode");
  assert.equal(aInst.editor.querySelector(".wsmd-line"), null, "instance A has no .wsmd-line in bang mode");
  assert.ok(bInst.editor.querySelector(".wsmd-strong"), "instance B structured markdown survives A entering bang mode");

  // Mentions are isolated per instance and render .wsmd-mention spans
  aInst.setValue("@src/a.go hi");
  aInst.setMentions([{ start: 0, end: 9, text: "@src/a.go" }]);
  aInst.reconcileMarkdown();
  assert.ok(aInst.editor.querySelector(".wsmd-mention"), "instance A renders .wsmd-mention");
  assert.equal(aInst.sendText(), "src/a.go hi");
  assert.equal(bInst.getMentions().length, 0);
  assert.equal(bInst.editor.querySelector(".wsmd-mention"), null);

  // Detaching A cleans up A's _wsmdRenderHook without disturbing B
  assert.equal(typeof aInst.editor._wsmdRenderHook, "function");
  assert.equal(typeof bInst.editor._wsmdRenderHook, "function");
  aInst.destroy();
  assert.equal(aInst.editor._wsmdRenderHook, undefined, "instance A WsMdEditor detached");
  assert.equal(typeof bInst.editor._wsmdRenderHook, "function", "instance B WsMdEditor stays attached");
  assert.ok(bInst.editor.querySelector(".wsmd-strong"), "instance B DOM intact after A destroy");
  bInst.destroy();
  assert.equal(bInst.editor._wsmdRenderHook, undefined, "instance B WsMdEditor detached");
});

test("feature flags gate slash, mentions, topics, bangMode, imagePaste, longPaste, and listContinuation", () => {
  const { window, doc, FakeEvent, flushTimers } = makeHarness();
  const field = doc.createElement("textarea");
  doc.body.appendChild(field);

  let fileAsked = 0, cmdAsked = 0, topicAsked = 0, imgPasted = 0, longPasted = 0, submitted = 0;
  const inst = window.WsInput.attach(field, {
    keymap: "newline-only",
    features: {
      markdown: true,
      capsules: true,
      mentions: false,
      topics: false,
      slash: false,
      imagePaste: false,
      longPaste: false,
      listContinuation: true,
      bangMode: false
    },
    providers: {
      files: (q, done) => { fileAsked++; done([{ path: "a.go" }]); },
      commands: (q, done) => { cmdAsked++; done([{ name: "ws:test" }]); },
      topics: (done) => { topicAsked++; done([{ id: "t1", label: "Topic 1" }]); }
    },
    attachmentHandler: {
      onImagePaste: () => { imgPasted++; },
      onLongPaste: () => { longPasted++; return true; }
    },
    onSubmit: () => { submitted++; }
  });

  field.focus();
  field.value = "@foo #bar /ws";
  field.setSelectionRange(4, 4);
  field.dispatchEvent(new FakeEvent("input", { bubbles: true }));
  flushTimers();
  assert.equal(fileAsked, 0);
  assert.equal(cmdAsked, 0);
  assert.equal(topicAsked, 0);

  // Paste with imagePaste=false and longPaste=false invokes neither handler
  const pasteEv = new FakeEvent("paste", { bubbles: true, cancelable: true });
  pasteEv.clipboardData = {
    items: [{ kind: "file", type: "image/png", getAsFile: () => ({ type: "image/png" }) }],
    getData: () => "x".repeat(5000)
  };
  field.dispatchEvent(pasteEv);
  assert.equal(imgPasted, 0);
  assert.equal(longPasted, 0);

  // listContinuation continues bullets on Enter under newline-only keymap without submitting
  field.value = "- first item";
  field.setSelectionRange(field.value.length, field.value.length);
  const enterEv = new FakeEvent("keydown", { bubbles: true, cancelable: true });
  enterEv.key = "Enter";
  field.dispatchEvent(enterEv);
  assert.equal(submitted, 0);
  assert.equal(field.value, "- first item\n- ");

  // Pressing Enter on an empty bullet exits the list
  const enterEv2 = new FakeEvent("keydown", { bubbles: true, cancelable: true });
  enterEv2.key = "Enter";
  field.dispatchEvent(enterEv2);
  assert.equal(field.value, "- first item\n");

  inst.destroy();
});

test("mention/slash/topic popups mount inside nearest open <dialog> to avoid top-layer clipping", () => {
  const { window, doc, FakeEvent, flushTimers } = makeHarness();
  const dlg = doc.createElement("dialog");
  dlg.setAttribute("open", "");
  const field = doc.createElement("textarea");
  dlg.appendChild(field);
  doc.body.appendChild(dlg);

  const inst = window.WsInput.attach(field, {
    providers: {
      files: (q, done) => done([{ path: "README.md", dir: false }]),
      commands: (q, done) => done([{ name: "help", description: "Help" }])
    }
  });

  field.focus();
  field.value = "@RE";
  field.setSelectionRange(3, 3);
  field.dispatchEvent(new FakeEvent("input", { bubbles: true }));
  flushTimers();

  const pops = inst.getPopups();
  assert.equal(pops.mention.pop.parentNode, dlg, "mention popover mounts inside open dialog");
  assert.equal(pops.slash.pop.parentNode, dlg, "slash popover mounts inside open dialog");
  assert.equal(pops.mention.pop.hidden, false);

  inst.destroy();
  assert.equal(pops.mention.pop.parentNode, null);
});

test("WsInput can mount on a plain host element and honors keymaps (enter-save, mod-send) and pre-existing WsMdEditor", () => {
  let customAttached = 0, customReconciled = 0, customDetached = 0;
  const customEditor = {
    attach() {
      customAttached++;
      return {
        reconcile() { customReconciled++; },
        detach() { customDetached++; }
      };
    }
  };
  const { window, doc, FakeEvent } = makeHarness({ preWsMdEditor: customEditor });
  const host = doc.createElement("div");
  doc.body.appendChild(host);

  let submits = 0, cancels = 0;
  const inst = window.WsInput.attach(host, {
    placeholder: "Inline note",
    keymap: "enter-save",
    onSubmit: () => { submits++; },
    onCancel: () => { cancels++; }
  });

  assert.equal(inst.field.tagName, "TEXTAREA");
  assert.equal(inst.field.parentNode, host);
  assert.equal(customAttached, 1, "uses pre-registered WsMdEditor without overwriting it");

  inst.focus();
  const eEnter = new FakeEvent("keydown", { bubbles: true, cancelable: true });
  eEnter.key = "Enter";
  inst.field.dispatchEvent(eEnter);
  assert.equal(submits, 1);

  const eShiftEnter = new FakeEvent("keydown", { bubbles: true, cancelable: true });
  eShiftEnter.key = "Enter";
  eShiftEnter.shiftKey = true;
  inst.field.dispatchEvent(eShiftEnter);
  assert.equal(submits, 1, "Shift+Enter does not submit under enter-save");

  const eEsc = new FakeEvent("keydown", { bubbles: true, cancelable: true });
  eEsc.key = "Escape";
  inst.field.dispatchEvent(eEsc);
  assert.equal(cancels, 1);

  inst.reconcileMarkdown();
  assert.equal(customReconciled, 1);

  inst.destroy();
  assert.equal(customDetached, 1);
  assert.equal(inst.field.parentNode, null);

  // Graceful degradation when WsMdEditor is absent (e.g. standalone wswhiteboard)
  const noMd = makeHarness({ noMdEditor: true });
  const plainField = noMd.doc.createElement("textarea");
  noMd.doc.body.appendChild(plainField);
  const plainInst = noMd.window.WsInput.attach(plainField, { keymap: "newline-only" });
  plainInst.setValue("**plain**");
  plainInst.syncMarkdown();
  plainInst.reconcileMarkdown();
  plainInst.clearMarkdown();
  noMd.flushTimers();
  assert.equal(plainField.value, "**plain**");
  plainInst.destroy();
});

test("multi-surface coexistence: bangMode, dynamic keymap, Escape-popup priority, and container sweep", () => {
  const { window, doc, highlights, FakeEvent, flushTimers } = makeHarness();

  const ctext = doc.createElement("textarea");
  const boardNote = doc.createElement("textarea");
  const chipWrap = doc.createElement("div");
  const closeReason = doc.createElement("textarea");
  chipWrap.appendChild(closeReason);
  const supArea = doc.createElement("textarea");
  doc.body.appendChild(ctext);
  doc.body.appendChild(boardNote);
  doc.body.appendChild(chipWrap);
  doc.body.appendChild(supArea);

  let supMode = "enter";
  let supSubmits = 0, supCancels = 0;
  let noteImgPastes = 0, reasonImgPastes = 0;

  const mainInst = window.WsInput.attach(ctext, {
    managedKeydown: true,
    features: { bangMode: true, imagePaste: true, longPaste: true },
    providers: { files: (q, done) => done([{ path: "main.go", dir: false }]) }
  });
  const noteInst = window.WsInput.attach(boardNote, {
    keymap: "newline-only",
    features: { bangMode: false, imagePaste: true, longPaste: false },
    attachmentHandler: { onImagePaste: () => { noteImgPastes++; } },
    providers: { files: (q, done) => done([{ path: "note.go", dir: false }]) }
  });
  const reasonInst = window.WsInput.attach(closeReason, {
    keymap: "newline-only",
    features: { bangMode: false, imagePaste: false, longPaste: false },
    attachmentHandler: { onImagePaste: () => { reasonImgPastes++; } },
    providers: { files: (q, done) => done([{ path: "reason.go", dir: false }]) }
  });
  const supInst = window.WsInput.attach(supArea, {
    keymap: () => supMode === "mod" ? "mod-send" : "enter-send",
    features: { bangMode: false, imagePaste: false, longPaste: false },
    onSubmit: () => { supSubmits++; },
    onCancel: () => { supCancels++; },
    providers: { files: (q, done) => done([{ path: "sup.go", dir: false }]) }
  });

  mainInst.setValue("**main**");
  noteInst.setValue("**note**");
  reasonInst.setValue("**reason**");
  supInst.setValue("**sup**");
  flushTimers();
  assert.ok(mainInst.editor.querySelector(".wsmd-strong"), "main composer renders .wsmd-strong");
  assert.ok(noteInst.editor.querySelector(".wsmd-strong"), "board note renders .wsmd-strong");
  assert.ok(reasonInst.editor.querySelector(".wsmd-strong"), "close-request reason renders .wsmd-strong");
  assert.ok(supInst.editor.querySelector(".wsmd-strong"), "support chat renders .wsmd-strong");

  // Bang mode only applies to main composer; !cmd in boardNote/supArea still renders markdown
  mainInst.setValue("!echo **hi**");
  noteInst.setValue("!echo **still-bold**");
  flushTimers();
  assert.equal(mainInst.editor.querySelector(".wsmd-strong"), null, "bangMode flattens markdown on main composer");
  assert.ok(noteInst.editor.querySelector(".wsmd-strong"), "board note keeps markdown even with leading !");
  assert.ok(reasonInst.editor.querySelector(".wsmd-strong"), "close-request reason keeps markdown");
  assert.ok(supInst.editor.querySelector(".wsmd-strong"), "support chat keeps markdown");

  // Image paste gated per surface
  const mkImgPaste = () => {
    const ev = new FakeEvent("paste", { bubbles: true, cancelable: true });
    ev.clipboardData = { items: [{ kind: "file", type: "image/png", getAsFile: () => ({ type: "image/png" }) }] };
    return ev;
  };
  boardNote.dispatchEvent(mkImgPaste());
  closeReason.dispatchEvent(mkImgPaste());
  assert.equal(noteImgPastes, 1, "board note allows image paste");
  assert.equal(reasonImgPastes, 0, "close-request reason rejects image paste");

  // Dynamic keymap on .supcomp switches between enter-send and mod-send
  supArea.focus();
  let ev = new FakeEvent("keydown", { bubbles: true, cancelable: true });
  ev.key = "Enter";
  supArea.dispatchEvent(ev);
  assert.equal(supSubmits, 1, "enter-send submits on bare Enter");

  supMode = "mod";
  ev = new FakeEvent("keydown", { bubbles: true, cancelable: true });
  ev.key = "Enter";
  supArea.dispatchEvent(ev);
  assert.equal(supSubmits, 1, "mod-send does not submit on bare Enter");

  ev = new FakeEvent("keydown", { bubbles: true, cancelable: true });
  ev.key = "Enter";
  ev.ctrlKey = true;
  supArea.dispatchEvent(ev);
  assert.equal(supSubmits, 2, "mod-send submits on Ctrl+Enter");

  // Escape closes open @ popup first without calling onCancel
  supArea.focus();
  supArea.value = "@su";
  supArea.setSelectionRange(3, 3);
  supArea.dispatchEvent(new FakeEvent("input", { bubbles: true }));
  flushTimers();
  assert.equal(supInst.getPopups().mention.pop.hidden, false);

  ev = new FakeEvent("keydown", { bubbles: true, cancelable: true });
  ev.key = "Escape";
  supArea.dispatchEvent(ev);
  assert.equal(ev.defaultPrevented, true, "popup Escape prevents outer handler");
  assert.equal(supInst.getPopups().mention.pop.hidden, true);
  assert.equal(supCancels, 0, "first Escape only closes popup");

  ev = new FakeEvent("keydown", { bubbles: true, cancelable: true });
  ev.key = "Escape";
  supArea.dispatchEvent(ev);
  assert.equal(supCancels, 1, "second Escape invokes onCancel");

  // Sweeping chipWrap destroys closeReason without touching other instances
  const reasonPop = reasonInst.getPopups().mention.pop;
  window.WsInput.sweep(chipWrap);
  assert.equal(reasonPop.parentNode, null, "swept popup detached");
  assert.equal(reasonInst.editor._wsmdRenderHook, undefined, "swept editor WsMdEditor detached");
  assert.ok(noteInst.editor.querySelector(".wsmd-strong"), "unswept boardNote markdown survives");
  assert.equal(typeof supInst.editor._wsmdRenderHook, "function", "unswept supcomp WsMdEditor survives");

  mainInst.destroy();
  noteInst.destroy();
  supInst.destroy();
  assert.equal(mainInst.editor._wsmdRenderHook, undefined);
  assert.equal(noteInst.editor._wsmdRenderHook, undefined);
  assert.equal(supInst.editor._wsmdRenderHook, undefined);
});

test("memo.js with real WsInput: cross-frame providers, stale-session drop, list continuation, and sweep", async () => {
  const { window, doc, highlights, FakeEvent, flushTimers } = makeHarness();
  const memoSrc = fs.readFileSync(path.join(assetsDir, "memo.js"), "utf8");

  // Extend FakeElement matching for memo.js selectors
  const origMatches = doc.body.constructor.prototype.matches;
  doc.body.constructor.prototype.matches = function (sel) {
    if (origMatches.call(this, sel)) return true;
    if (sel === ".sframe.on") return this.classList.contains("sframe") && this.classList.contains("on");
    if (sel.startsWith(".")) return this.classList.contains(sel.slice(1));
    if (sel.startsWith("#")) return this.id === sel.slice(1);
    return this.tagName === sel.toUpperCase();
  };

  const ids = {};
  for (const id of ["memo-input", "memo-list", "memo-workspace", "memo-n", "memo-status", "memo-undo", "memo-close"]) {
    const el = doc.createElement(id === "memo-input" ? "textarea" : "div");
    el.id = id;
    el.style.setProperty = () => {};
    doc.body.appendChild(el);
    ids[id] = el;
  }
  ids["memo-undo"].appendChild(doc.createElement("button"));
  ids["memo-list"].clientWidth = 444;

  const rail = doc.createElement("div");
  const ws = doc.createElement("section");
  ws.className = "rws";
  ws.setAttribute("data-ws", "/repo");
  const nm = doc.createElement("span"); nm.className = "nm"; nm.textContent = "repo";
  ws.appendChild(nm);
  const row = doc.createElement("div"); row.className = "rs"; row.setAttribute("data-rid", "session");
  ws.appendChild(row);
  const badge = doc.createElement("button"); badge.className = "memo-count";
  badge.appendChild(doc.createElement("span"));
  ws.appendChild(badge);
  rail.appendChild(ws);
  doc.body.appendChild(rail);

  let activeFrame = null;
  doc.getElementById = (id) => ids[id] || null;
  doc.querySelector = (sel) => sel === ".sframe.on" ? activeFrame : doc.body.querySelector(sel);
  doc.documentElement = { getAttribute: () => "standalone" };

  let remote = [];
  let nextID = 0;
  const storage = new Map();
  window.sessionStorage = {
    setItem: (k, v) => storage.set(k, v),
    getItem: (k) => storage.get(k),
    removeItem: (k) => storage.delete(k)
  };
  window.fetch = async (url, opts) => {
    if (opts && opts.method === "POST") {
      const req = JSON.parse(opts.body);
      if (req.action === "add") remote.unshift({ id: String(++nextID), text: req.text.trim() });
      if (req.action === "edit") remote.find((m) => m.id === req.id).text = req.text.trim();
      if (req.action === "delete") remote = remote.filter((m) => m.id !== req.id);
    }
    return { ok: true, json: async () => ({ memos: remote.map((m) => ({ ...m })), writable: true }) };
  };

  vm.runInContext(memoSrc, vm.createContext(window), { filename: "memo.js" });

  const panel = doc.createElement("div");
  doc.body.appendChild(panel);
  const memo = window.WsMemo.mount(doc, rail, panel, () => {});
  memo.sync("session");
  memo.open(true, false);
  for (let i = 0; i < 10; i++) await Promise.resolve();

  const input = ids["memo-input"];
  assert.ok(input.wsInput, "memo-input attached WsInput");

  // Cross-frame file/command/topic providers work and drop stale answers on session switch
  let delayedFiles = null;
  let fileQueries = 0;
  activeFrame = {
    contentWindow: {
      document: { documentElement: { getAttribute: () => "session" } },
      wsCapsuleTopics(done) { done([{ id: "t1", label: "Topic 1" }]); },
      wsComposerFiles(q, done) { fileQueries++; delayedFiles = done; },
      wsComposerCommands(q, done) { done([{ name: "ws:go", description: "Go" }]); }
    }
  };

  input.focus();
  input.value = "@src";
  input.setSelectionRange(4, 4);
  input.dispatchEvent(new FakeEvent("input", { bubbles: true }));
  flushTimers();
  assert.equal(fileQueries, 1, "cross-frame wsComposerFiles queried");

  // Switching session before delayed response arrives drops the stale candidates and closes popup
  memo.sync("other-session");
  delayedFiles([{ path: "src/stale.go", dir: false }]);
  assert.equal(input.wsInput.getPopups().mention.pop.hidden, true, "stale cross-frame file candidates dropped");

  // Restore matching session and verify Shift+Enter list continuation + Enter save
  memo.sync("session");
  for (let i = 0; i < 10; i++) await Promise.resolve();
  input.focus();
  input.value = "- **memo item**";
  input.setSelectionRange(input.value.length, input.value.length);
  input.dispatchEvent(new FakeEvent("input", { bubbles: true }));
  flushTimers();
  assert.ok(input.wsc.editor.querySelector(".wsmd-strong"), "memo-input renders structured markdown DOM");

  const shiftEnter = new FakeEvent("keydown", { bubbles: true, cancelable: true });
  shiftEnter.key = "Enter";
  shiftEnter.shiftKey = true;
  input.dispatchEvent(shiftEnter);
  assert.equal(remote.length, 0, "Shift+Enter continues list without saving");
  assert.equal(input.value, "- **memo item**\n- ");

  const enter = new FakeEvent("keydown", { bubbles: true, cancelable: true });
  enter.key = "Enter";
  input.dispatchEvent(enter);
  for (let i = 0; i < 15; i++) await Promise.resolve();
  assert.equal(remote.length, 1, "Enter saves memo");
  assert.equal(remote[0].text, "- **memo item**\n-");

  // Inline edit attaches WsInput and sweeps cleanly when closed
  const textEl = ids["memo-list"].querySelector(".memo-text");
  assert.ok(textEl, "rendered memo text element");
  textEl.dispatchEvent(new FakeEvent("click", { bubbles: true }));
  const editArea = ids["memo-list"].querySelector("textarea");
  assert.ok(editArea && editArea.wsInput, "inline memo-note attached WsInput");
  const editPop = editArea.wsInput.getPopups().mention.pop;
  assert.ok(editPop.parentNode, "inline edit popup mounted");

  const esc = new FakeEvent("keydown", { bubbles: true, cancelable: true });
  esc.key = "Escape";
  editArea.dispatchEvent(esc);
  assert.equal(editPop.parentNode, null, "sweeping memo-list detaches inline WsInput popup");
});

test("draft values preserve raw @mentions across re-renders and stripMentions strips @ only at submit", () => {
  const { window, doc } = makeHarness();

  // Simulate close-request reason chip re-render & caret preservation with a tracked @mention
  const reason1 = doc.createElement("textarea");
  doc.body.appendChild(reason1);
  const inst1 = window.WsInput.attach(reason1, { keymap: "newline-only" });
  inst1.setValue("see @src/a.go please");
  inst1.setMentions([{ start: 4, end: 13, text: "@src/a.go" }]);
  const savedDraft = { reason: reason1.value, mentions: inst1.getMentions() };
  assert.equal(savedDraft.reason, "see @src/a.go please", "draft retains raw @mention text");

  // Re-render chip: sweep old field, create fresh field, re-seed raw text + mentions
  inst1.destroy();
  const reason2 = doc.createElement("textarea");
  reason2.value = savedDraft.reason;
  doc.body.appendChild(reason2);
  const inst2 = window.WsInput.attach(reason2, { keymap: "newline-only" });
  inst2.setMentions(savedDraft.mentions);
  reason2.setSelectionRange(4, 13);
  assert.equal(reason2.value, "see @src/a.go please", "re-rendered chip preserves raw @mention");
  assert.equal(reason2.value.slice(reason2.selectionStart, reason2.selectionEnd), "@src/a.go", "restored caret offsets stay aligned");
  assert.equal(inst2.getMentions().length, 1, "mention tracking survives re-render");

  // Submit strips only tracked @ mentions via stripMentions
  assert.equal(window.WsInput.stripMentions(savedDraft.reason, savedDraft.mentions), "see src/a.go please");
  assert.equal(window.WsInput.stripMentions("plain @not-tracked", []), "plain @not-tracked");

  inst2.destroy();
});

test("voice fields queue until the voice bundle flushes; attachVoice is idempotent and updateConfig dequeues", () => {
  const { window, doc } = makeHarness();
  const mk = () => { const f = doc.createElement("textarea"); doc.body.appendChild(f); return f; };

  // No voice bundle yet: voice fields queue, non-voice fields never do.
  const queuedField = mk(), droppedField = mk(), plainField = mk();
  const queued = window.WsInput.attach(queuedField, { voice: true });
  const dropped = window.WsInput.attach(droppedField, { voice: true });
  window.WsInput.attach(plainField, {});
  assert.equal(queued.voice, null);

  // Disabling voice before the flush splices the instance out of the queue.
  dropped.updateConfig({ voice: false });

  const attached = [];
  window.WsVoiceInput = {
    attachField(el, api) {
      const handle = { destroyed: 0, destroy() { this.destroyed++; } };
      attached.push({ el, api, handle });
      return handle;
    }
  };
  window.WsInput.flushVoice();
  assert.equal(attached.length, 1, "only the still-voice instance attaches");
  assert.equal(attached[0].el, queuedField);
  assert.equal(queued.voice, attached[0].handle);
  assert.equal(dropped.voice, null);

  // A second flush or an unrelated updateConfig must not attach twice.
  window.WsInput.flushVoice();
  queued.updateConfig({ features: { slash: false } });
  assert.equal(attached.length, 1, "attachVoice guards against double attach");

  // Disabling voice destroys the handle once; destroy() afterwards does not double-destroy.
  queued.updateConfig({ voice: false });
  assert.equal(attached[0].handle.destroyed, 1);
  assert.equal(queued.voice, null);
  queued.destroy();
  assert.equal(attached[0].handle.destroyed, 1);
});
