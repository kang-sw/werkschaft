"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const assetsDir = path.join(__dirname, "..", "assets");
const mentionSrc = fs.readFileSync(path.join(assetsDir, "mention.js"), "utf8");
const capsulesSrc = fs.readFileSync(path.join(assetsDir, "capsules.js"), "utf8");
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

  class FakeElement extends FakeNode {
    constructor(tag, ownerDocument) {
      super(1, tag.toUpperCase(), ownerDocument);
      this.tagName = tag.toUpperCase();
      this.attrs = {};
      this.style = {};
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
    addEventListener(type, fn) { (this.listeners[type] = this.listeners[type] || []).push(fn); }
    removeEventListener(type, fn) {
      if (!this.listeners[type]) return;
      this.listeners[type] = this.listeners[type].filter((f) => f !== fn);
    }
    dispatchEvent(ev) {
      ev.target = ev.target || this;
      const list = (this.listeners[ev.type] || []).slice();
      for (const fn of list) {
        fn.call(this, ev);
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
    innerWidth: 1200,
    innerHeight: 800,
    MutationObserver: class { observe() {} disconnect() {} },
    getSelection() {
      return {
        rangeCount: selRanges.length,
        anchorNode: selRanges[0] ? selRanges[0].startContainer : null,
        focusNode: selRanges[0] ? selRanges[0].endContainer : null,
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

test("two simultaneous WsInput instances coexist with isolated state and aggregated CSS highlights", () => {
  const { window, doc, highlights, FakeEvent, flushTimers } = makeHarness();
  const aField = doc.createElement("textarea");
  const bField = doc.createElement("textarea");
  doc.body.appendChild(aField);
  doc.body.appendChild(bField);

  const drafts = { a: "", b: "" };
  const aInst = window.WsInput.attach(aField, {
    draftAdapter: { get: () => drafts.a, set: (v) => { drafts.a = v; } },
    features: { bangMode: true },
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

  // Both instances' ws-md-strong ranges must coexist in CSS.highlights!
  const strongHL = highlights.get("ws-md-strong");
  assert.ok(strongHL, "ws-md-strong highlight exists");
  assert.equal(strongHL.ranges.length, 2, "two strong ranges across both editors");
  assert.ok(aInst.editor.contains(strongHL.ranges[0].startContainer) || aInst.editor.contains(strongHL.ranges[1].startContainer));
  assert.ok(bInst.editor.contains(strongHL.ranges[0].startContainer) || bInst.editor.contains(strongHL.ranges[1].startContainer));

  // Clearing instance A (e.g., entering !bang mode) must NOT wipe instance B's highlights!
  aInst.setValue("!ls -la");
  flushTimers();
  const strongAfterBang = highlights.get("ws-md-strong");
  assert.ok(strongAfterBang, "instance B strong highlight survives A entering bang mode");
  assert.equal(strongAfterBang.ranges.length, 1);
  assert.ok(bInst.editor.contains(strongAfterBang.ranges[0].startContainer));

  // Mentions are isolated per instance
  aInst.setValue("@src/a.go hi");
  aInst.setMentions([{ start: 0, end: 9, text: "@src/a.go" }]);
  assert.equal(aInst.sendText(), "src/a.go hi");
  assert.equal(bInst.getMentions().length, 0);

  // Detaching A leaves B's highlights intact; detaching B clears them completely.
  aInst.destroy();
  assert.equal(highlights.get("ws-md-strong").ranges.length, 1);
  bInst.destroy();
  assert.equal(highlights.has("ws-md-strong"), false);
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
});
