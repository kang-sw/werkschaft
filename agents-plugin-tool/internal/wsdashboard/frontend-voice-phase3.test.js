const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const assetsDir = path.join(__dirname, 'assets');
const voicePttJS = fs.readFileSync(path.join(assetsDir, 'dashboard-voice-ptt.js'), 'utf8');
const voiceJS = fs.readFileSync(path.join(assetsDir, 'dashboard-voice.js'), 'utf8');
const voiceNativeJS = fs.readFileSync(path.join(assetsDir, 'dashboard-voice-native.js'), 'utf8');
const capsulesJS = fs.readFileSync(path.join(assetsDir, 'capsules.js'), 'utf8');
const mdEditorJS = fs.readFileSync(path.join(assetsDir, 'md-editor.js'), 'utf8');

class NodeStub {
  constructor(nodeType) {
    this.nodeType = nodeType;
    this.parentNode = null;
    this.childNodes = [];
  }
  get nextSibling() {
    if (!this.parentNode) return null;
    const i = this.parentNode.childNodes.indexOf(this);
    return i >= 0 && i + 1 < this.parentNode.childNodes.length ? this.parentNode.childNodes[i + 1] : null;
  }
  get previousSibling() {
    if (!this.parentNode) return null;
    const i = this.parentNode.childNodes.indexOf(this);
    return i > 0 ? this.parentNode.childNodes[i - 1] : null;
  }
  remove() {
    if (this.parentNode) this.parentNode.removeChild(this);
  }
}

class TextStub extends NodeStub {
  constructor(val) {
    super(3);
    this.nodeName = '#text';
    this.nodeValue = String(val == null ? '' : val);
  }
  get data() { return this.nodeValue; }
  set data(v) { this.nodeValue = String(v == null ? '' : v); }
  get length() { return this.nodeValue.length; }
  splitText(off) {
    const tail = new TextStub(this.nodeValue.slice(off));
    this.nodeValue = this.nodeValue.slice(0, off);
    if (this.parentNode) this.parentNode.insertBefore(tail, this.nextSibling);
    return tail;
  }
}

class ElementStub extends NodeStub {
  constructor(tag, id, cls) {
    super(1);
    this.tagName = String(tag || 'div').toUpperCase();
    this.nodeName = this.tagName;
    this.id = id || '';
    this.className = cls || '';
    this.attrs = new Map();
    this.listeners = new Map();
    this.style = {};
    this.hidden = false;
    this.disabled = false;
    this.isConnected = true;
    this.value = '';
    this.selectionStart = 0;
    this.selectionEnd = 0;
    this.rect = { left: 100, top: 10, width: 400, height: 60 };
    this._text = '';
  }
  get children() { return this.childNodes.filter(n => n.nodeType === 1); }
  get firstChild() { return this.childNodes[0] || null; }
  get lastChild() { return this.childNodes[this.childNodes.length - 1] || null; }
  get textContent() {
    if (this.childNodes.length) {
      return this.childNodes.map(n => n.nodeType === 3 ? n.nodeValue : n.textContent).join('');
    }
    return this._text;
  }
  set textContent(v) {
    this._text = String(v == null ? '' : v);
    this.childNodes.forEach(c => { c.parentNode = null; });
    this.childNodes = this._text ? [new TextStub(this._text)] : [];
    this.childNodes.forEach(c => { c.parentNode = this; });
  }
  setAttribute(k, v) { this.attrs.set(k, String(v)); }
  getAttribute(k) { return this.attrs.has(k) ? this.attrs.get(k) : null; }
  hasAttribute(k) { return this.attrs.has(k); }
  removeAttribute(k) { this.attrs.delete(k); }
  appendChild(child) {
    if (child.parentNode) child.parentNode.removeChild(child);
    child.parentNode = this;
    this.childNodes.push(child);
    return child;
  }
  insertBefore(child, ref) {
    if (child.parentNode) child.parentNode.removeChild(child);
    child.parentNode = this;
    const idx = ref ? this.childNodes.indexOf(ref) : -1;
    if (idx >= 0) this.childNodes.splice(idx, 0, child);
    else this.childNodes.push(child);
    return child;
  }
  removeChild(child) {
    const idx = this.childNodes.indexOf(child);
    if (idx >= 0) this.childNodes.splice(idx, 1);
    child.parentNode = null;
    return child;
  }
  contains(node) {
    if (node === this) return true;
    for (const c of this.childNodes) {
      if (c === node || (c.nodeType === 1 && c.contains(node))) return true;
    }
    return false;
  }
  addEventListener(t, fn) {
    if (!this.listeners.has(t)) this.listeners.set(t, []);
    this.listeners.get(t).push(fn);
  }
  removeEventListener(t, fn) {
    const arr = this.listeners.get(t);
    if (!arr) return;
    const i = arr.indexOf(fn);
    if (i >= 0) arr.splice(i, 1);
  }
  dispatchEvent(ev) {
    const t = typeof ev === 'string' ? ev : ev.type;
    for (const fn of (this.listeners.get(t) || []).slice()) fn(ev);
    return true;
  }
  click() { this.dispatchEvent({ type: 'click', preventDefault() {} }); }
  focus() { this.focused = true; }
  getBoundingClientRect() { return this.rect; }
  setRangeText(rep, start, end, mode) {
    const a = typeof start === 'number' ? start : this.selectionStart;
    const b = typeof end === 'number' ? end : this.selectionEnd;
    this.value = this.value.slice(0, a) + rep + this.value.slice(b);
    if (mode === 'end') this.selectionStart = this.selectionEnd = a + rep.length;
  }
  setSelectionRange(a, b) { this.selectionStart = a; this.selectionEnd = b; }
  querySelector(sel) {
    const match = (el) => {
      if (sel.startsWith('.')) return el.className.split(/\s+/).includes(sel.slice(1));
      const eq = sel.match(/^\[([^=\]]+)="([^"]*)"\]$/);
      if (eq) return el.getAttribute(eq[1]) === eq[2];
      const pr = sel.match(/^\[([^=\]]+)\]$/);
      if (pr) return el.hasAttribute(pr[1]);
      return false;
    };
    const walk = (n) => {
      for (const c of n.children) {
        if (match(c)) return c;
        const found = walk(c);
        if (found) return found;
      }
      return null;
    };
    return walk(this);
  }
  querySelectorAll(sel) {
    const res = [];
    const tag = sel.toUpperCase();
    const walk = (n) => {
      for (const c of n.children) {
        if (c.tagName === tag || (sel.startsWith('[') && c.hasAttribute(sel.slice(1, -1)))) res.push(c);
        walk(c);
      }
    };
    walk(this);
    return res;
  }
}

class FakeEvent {
  constructor(type, init) {
    this.type = type;
    this.defaultPrevented = false;
    Object.assign(this, init || {});
  }
  preventDefault() { this.defaultPrevented = true; }
}

class FakeCustomEvent extends FakeEvent {
  constructor(type, init) {
    super(type, init);
    this.detail = init && init.detail;
  }
}

function makeFakeTimers() {
  let now = 0;
  let nextId = 1;
  const timers = new Map();
  return {
    setTimeout(fn, ms) {
      const id = nextId++;
      timers.set(id, { fn, due: now + (ms || 0) });
      return id;
    },
    clearTimeout(id) { timers.delete(id); },
    advance(ms) {
      now += ms;
      for (const [id, t] of Array.from(timers.entries())) {
        if (t.due <= now) {
          timers.delete(id);
          t.fn();
        }
      }
    },
    pendingCount() { return timers.size; }
  };
}

function makeVoiceEnv(opts = {}) {
  const root = new ElementStub('html');
  const body = new ElementStub('body');
  root.appendChild(body);
  const composer = new ElementStub('div', 'composer', 'composer');
  const field = new ElementStub('div', '', 'cfield');
  const ctext = new ElementStub('textarea', 'ctext');
  const editor = new ElementStub('div', '', 'wsc-editor');
  editor.setAttribute('contenteditable', 'true');
  if (opts.withCapsules !== false) {
    ctext.wsc = {
      editor,
      anchor(pos) { return { x: 120 + pos * 4, top: 14, bottom: 32 }; },
      point(pos) {
        if (editor.childNodes.length && editor.childNodes[0].nodeType === 3) {
          return [editor.childNodes[0], Math.min(pos, editor.childNodes[0].nodeValue.length)];
        }
        return [editor, editor.childNodes.length];
      }
    };
  }
  const cap = new ElementStub('div', 'cvox-cap', 'cvox-cap');
  cap.hidden = true;
  const btn = new ElementStub('button', 'cvox', 'cvox');
  btn.hidden = true;
  const csend = new ElementStub('button', 'csend', 'csend');
  field.appendChild(ctext);
  field.appendChild(cap);
  composer.appendChild(field);
  composer.appendChild(btn);
  composer.appendChild(csend);
  body.appendChild(composer);

  const docListeners = new Map();
  const winListeners = new Map();
  let popupOpen = false;
  const doc = {
    hidden: false,
    documentElement: root,
    body,
    getElementById(id) {
      if (id === 'composer') return composer;
      if (id === 'ctext') return ctext;
      if (id === 'csend') return csend;
      if (id === 'cvox') return btn;
      if (id === 'cvox-cap') return cap;
      return null;
    },
    createElement(tag) { return new ElementStub(tag); },
    createTextNode(t) { return new TextStub(t); },
    querySelector(sel) {
      if (sel.includes('.wsm-pop') && popupOpen) return new ElementStub('div', '', 'wsm-pop');
      return body.querySelector(sel);
    },
    querySelectorAll(sel) { return body.querySelectorAll(sel); },
    addEventListener(t, fn) {
      if (!docListeners.has(t)) docListeners.set(t, []);
      docListeners.get(t).push(fn);
    },
    removeEventListener(t, fn) {
      const a = docListeners.get(t);
      if (a) { const i = a.indexOf(fn); if (i >= 0) a.splice(i, 1); }
    },
    dispatchEvent(ev) {
      const t = typeof ev === 'string' ? ev : ev.type;
      for (const fn of (docListeners.get(t) || []).slice()) fn(ev);
    }
  };

  const observers = [];
  class FakeMutationObserver {
    constructor(cb) { this.cb = cb; this.disconnected = false; observers.push(this); }
    observe() {}
    disconnect() { this.disconnected = true; }
    trigger() { if (!this.disconnected) this.cb([]); }
  }

  const origin = opts.origin || 'http://127.0.0.1:8787';
  const windowObj = {
    document: doc,
    location: { origin },
    navigator: { language: opts.navigatorLanguage || 'ko-KR' },
    wsT: (k) => k,
    MutationObserver: FakeMutationObserver,
    Event: FakeEvent,
    CustomEvent: FakeCustomEvent,
    addEventListener(t, fn) {
      if (!winListeners.has(t)) winListeners.set(t, []);
      winListeners.get(t).push(fn);
    },
    removeEventListener(t, fn) {
      const a = winListeners.get(t);
      if (a) { const i = a.indexOf(fn); if (i >= 0) a.splice(i, 1); }
    },
    dispatchEvent(ev) {
      const t = typeof ev === 'string' ? ev : ev.type;
      for (const fn of (winListeners.get(t) || []).slice()) fn(ev);
    },
    postMessage(data, targetOrigin) {
      if (opts.onPostMessage) opts.onPostMessage(data, targetOrigin);
    }
  };
  windowObj.top = opts.topWindow || windowObj;
  if (opts.werkschaftSpeech) windowObj.werkschaftSpeech = opts.werkschaftSpeech;

  const ctx = {
    window: windowObj,
    document: doc,
    navigator: windowObj.navigator,
    Event: FakeEvent,
    CustomEvent: FakeCustomEvent,
    MutationObserver: FakeMutationObserver,
    Blob,
    TextDecoder,
    TextEncoder,
    AbortController,
    ArrayBuffer,
    DataView,
    Float32Array,
    Uint8Array,
    Promise,
    Error,
    Math,
    JSON,
    Object,
    String,
    Array,
    console,
    setTimeout,
    clearTimeout
  };
  vm.createContext(ctx);
  const origGet = doc.getElementById;
  doc.getElementById = () => null;
  windowObj.__wsVoiceNativeNoAuto = true;
  vm.runInContext(voicePttJS, ctx);
  vm.runInContext(voiceJS, ctx);
  vm.runInContext(voiceNativeJS, ctx);
  doc.getElementById = origGet;

  return {
    ctx, window: windowObj, doc, root, body, composer, field, ctext, editor, cap, btn, csend,
    observers,
    setPopupOpen(v) { popupOpen = !!v; }
  };
}

const flush = () => new Promise(r => setImmediate(r));

test('order walk resolves desktop native first, browser falls through past native, and zero available resolves null while staying eligible', async () => {
  const env = makeVoiceEnv();
  let nativeAvail = true;
  env.window.WsVoiceInput.registerBackend({
    id: 'native', label: 'OS Native', kind: 'native',
    available: () => nativeAvail,
    start: () => Promise.resolve(),
    stop: () => Promise.resolve({ text: 'ok' }),
    cancel: () => {}
  });

  let capsPayload = {
    order: ['native', 'openrouter', 'groq'],
    backends: [
      { id: 'native', kind: 'native', available: false },
      { id: 'openrouter', kind: 'external', available: false, models: [{ id: 'or-stt' }] },
      { id: 'groq', kind: 'external', available: true, models: [{ id: 'whisper-large-v3-turbo' }] }
    ],
    selected: { backend: 'groq', model: 'whisper-large-v3-turbo', language: 'ko' },
    legacy_backend: 'groq',
    legacy_model: 'whisper-large-v3-turbo'
  };
  let status = 200;
  const fakeFetch = async () => ({ ok: status === 200, status, json: async () => capsPayload });

  const handle = env.window.WsVoiceInput.attachComposer({
    document: env.doc, window: env.window, fetch: fakeFetch, autoDiscover: false
  });

  // Desktop with native available -> picks native first.
  await handle.controller.refreshCapabilities();
  assert.equal(handle.controller.isEligible(), true);
  assert.equal(handle.controller.getResolvedBackend().id, 'native');

  // Browser (native unavailable) -> skips openrouter (unavailable), resolves groq.
  nativeAvail = false;
  await handle.controller.refreshCapabilities();
  assert.equal(handle.controller.isEligible(), true);
  assert.equal(handle.controller.getResolvedBackend().id, 'groq');

  // Zero available -> eligible stays true, resolved is null.
  capsPayload = {
    order: ['native', 'openrouter'],
    backends: [
      { id: 'native', kind: 'native', available: false },
      { id: 'openrouter', kind: 'external', available: false }
    ],
    selected: null
  };
  await handle.controller.refreshCapabilities();
  assert.equal(handle.controller.isEligible(), true);
  assert.equal(handle.controller.getResolvedBackend(), null);

  // Non-loopback 404 -> eligible becomes false.
  status = 404;
  await handle.controller.refreshCapabilities();
  assert.equal(handle.controller.isEligible(), false);
  assert.equal(handle.controller.getResolvedBackend(), null);
  handle.destroy();
});

test('resolved backend receives legacy voice_model only when matching legacy_backend, and native receives voice_language or navigator.language', async () => {
  const env = makeVoiceEnv({ navigatorLanguage: 'ko-KR' });
  let lastCtx = null;
  env.window.WsVoiceInput.registerBackend({
    id: 'native', label: 'OS Native', kind: 'native',
    available: () => true,
    start: (ctx) => { lastCtx = ctx; return Promise.resolve(); },
    stop: () => Promise.resolve({ text: 'done' }),
    cancel: () => {}
  });
  env.window.WsVoiceInput.registerBackend({
    id: 'groq', label: 'Groq', kind: 'external',
    available: () => true,
    start: (ctx) => { lastCtx = ctx; return Promise.resolve(); },
    stop: () => Promise.resolve({ text: 'done' }),
    cancel: () => {}
  });

  const ctrl = env.window.WsVoiceInput.createController({ autoDiscover: false });
  ctrl.setCapabilities({
    eligible: true,
    order: ['native', 'groq'],
    legacy_backend: 'groq',
    legacy_model: 'whisper-large-v3-turbo',
    language: 'ja-JP'
  });
  await ctrl.start();
  assert.equal(lastCtx.model, '', 'non-legacy backend must not receive legacy voice_model');
  assert.equal(lastCtx.language, 'ja-JP');
  await ctrl.stop();

  ctrl.setCapabilities({
    eligible: true,
    order: ['groq'],
    legacy_backend: 'groq',
    legacy_model: 'whisper-large-v3-turbo',
    language: ''
  });
  await ctrl.start();
  assert.equal(lastCtx.model, 'whisper-large-v3-turbo', 'legacy backend keeps legacy voice_model');
  await ctrl.stop();

  // Native adapter locale normalization falls back to navigator.language when voice_language is empty,
  // and drops malformed tags so the Rust bridge never rejects a start/status command.
  assert.equal(env.window.WsVoiceNative.normalizeLocale('', { language: 'ko-KR' }), 'ko-KR');
  assert.equal(env.window.WsVoiceNative.normalizeLocale('en-US, ko-KR', { language: 'ko-KR' }), 'en-US');
  assert.equal(env.window.WsVoiceNative.normalizeLocale('en-US@x', { language: 'ko_KR' }), 'ko-KR');
  assert.equal(env.window.WsVoiceNative.normalizeLocale('en-US@x', { language: 'bad;tag' }), '');
  ctrl.destroy();
});

test('native adapter top-frame direct, browser absence, framed postMessage relay, busy refusal, hidden broadcast, and iframe detach teardown', async () => {
  const calls = [];
  const bridge = {
    status(loc) { calls.push({ op: 'status', loc }); return true; },
    start(loc) { calls.push({ op: 'start', loc }); return true; },
    stop() { calls.push({ op: 'stop' }); return true; },
    cancel() { calls.push({ op: 'cancel' }); return true; }
  };

  // Browser-only (no werkschaftSpeech): native never registers.
  const browserEnv = makeVoiceEnv();
  const browserNative = browserEnv.window.WsVoiceNative.create({
    window: browserEnv.window, document: browserEnv.doc, voiceInput: browserEnv.window.WsVoiceInput, isTop: true
  });
  assert.equal(browserNative.isDesktop(), false);
  assert.equal(browserEnv.window.WsVoiceInput.getBackend('native'), null);
  browserNative.destroy();

  // Top-frame desktop host + two child iframes communicating over same-origin postMessage.
  const topEnv = makeVoiceEnv({ werkschaftSpeech: bridge, origin: 'http://127.0.0.1:8787' });
  const topNative = topEnv.window.WsVoiceNative.create({
    window: topEnv.window, document: topEnv.doc, voiceInput: topEnv.window.WsVoiceInput, isTop: true
  });

  // Handshake ready makes native available on top.
  topEnv.window.dispatchEvent(new FakeCustomEvent('ws-native-speech', {
    detail: { type: 'ready', available: true, on_device: true, locale: 'ko-KR', reason: '' }
  }));
  assert.equal(topNative.isDesktop(), true);
  assert.equal(topNative.getStatus().available, true);
  assert.ok(topEnv.window.WsVoiceInput.getBackend('native'));

  // Mount two same-origin child iframes.
  function mountChildFrame() {
    const iframe = new ElementStub('iframe');
    const childEnv = makeVoiceEnv({
      origin: 'http://127.0.0.1:8787',
      topWindow: {
        postMessage(data, targetOrigin) {
          topEnv.window.dispatchEvent({
            type: 'message',
            origin: targetOrigin,
            source: childEnv.window,
            data
          });
        }
      }
    });
    childEnv.window.postMessage = (data, targetOrigin) => {
      childEnv.window.dispatchEvent({
        type: 'message',
        origin: targetOrigin,
        source: childEnv.window.top,
        data
      });
    };
    iframe.contentWindow = childEnv.window;
    topEnv.body.appendChild(iframe);
    const native = childEnv.window.WsVoiceNative.create({
      window: childEnv.window, document: childEnv.doc, voiceInput: childEnv.window.WsVoiceInput, isTop: false
    });
    return { iframe, env: childEnv, native };
  }

  const frameA = mountChildFrame();
  const frameB = mountChildFrame();

  assert.equal(frameA.native.getStatus().available, true, 'frame A receives cached ready status via relay');
  assert.ok(frameA.env.window.WsVoiceInput.getBackend('native'));

  // Cross-origin postMessage is strictly ignored.
  const startCountBefore = calls.filter(c => c.op === 'start').length;
  topEnv.window.dispatchEvent({
    type: 'message',
    origin: 'https://evil.example',
    source: frameA.env.window,
    data: { wsVoiceRelay: true, op: 'start', locale: 'en-US' }
  });
  assert.equal(calls.filter(c => c.op === 'start').length, startCountBefore);

  // Frame A starts dictation via relay; partials/final route only to frame A.
  const partialsA = [];
  const errorsB = [];
  frameA.native.backend.onPartial(t => partialsA.push(t));
  frameB.native.backend.onError(e => errorsB.push(e));

  await frameA.native.backend.start({ language: 'ko-KR' });
  assert.equal(calls[calls.length - 1].op, 'start');
  assert.equal(calls[calls.length - 1].loc, 'ko-KR');

  // Second frame B start while frame A owns recognizer -> refused with busy.
  await frameB.native.backend.start({ language: 'en-US' });
  assert.equal(errorsB.length, 1);
  assert.equal(errorsB[0].code, 'busy');

  // Partial arrives -> delivered to A.
  topEnv.window.dispatchEvent(new FakeCustomEvent('ws-native-speech', {
    detail: { type: 'partial', text: '안녕' }
  }));
  assert.deepEqual(partialsA, ['안녕']);

  // Stop from A -> final delivered to A.
  const stopPromise = frameA.native.backend.stop();
  topEnv.window.dispatchEvent(new FakeCustomEvent('ws-native-speech', {
    detail: { type: 'final', text: '안녕하세요' }
  }));
  const finalRes = await stopPromise;
  assert.equal(finalRes.text, '안녕하세요');

  // Start again in A, then broadcast hidden teardown from Rust -> reaches A and cancels lock/span.
  let hiddenReceived = false;
  frameA.native.backend.onHidden(() => { hiddenReceived = true; });
  await frameA.native.backend.start({ language: 'ko-KR' });
  topEnv.window.dispatchEvent(new FakeCustomEvent('ws-native-speech', {
    detail: { type: 'hidden', reason: 'minimized' }
  }));
  assert.equal(hiddenReceived, true, 'hidden teardown broadcast reaches framed composer');

  // Start in A, then detach iframe A mid-dictation -> MutationObserver triggers bridge.cancel() and lets B start cleanly.
  await frameA.native.backend.start({ language: 'ko-KR' });
  const cancelsBefore = calls.filter(c => c.op === 'cancel').length;
  frameA.iframe.isConnected = false;
  frameA.iframe.remove();
  topEnv.observers.forEach(o => o.trigger());
  assert.equal(calls.filter(c => c.op === 'cancel').length, cancelsBefore + 1, 'detaching owning iframe cancels native bridge');

  const startsBeforeB = calls.filter(c => c.op === 'start').length;
  await frameB.native.backend.start({ language: 'en-US' });
  assert.equal(calls.filter(c => c.op === 'start').length, startsBeforeB + 1, 'second frame starts cleanly after first frame detaches');

  frameA.native.destroy();
  frameB.native.destroy();
  topNative.destroy();
});

test('capsules.js read() and md-editor.js readEditorDom() exclude [data-voice-span] and WsMdEditor skips reconcile while live', () => {
  // Verify readEditorDom directly on DOM containing [data-voice-span].
  const ctx = {
    window: {},
    document: {},
    console,
    setTimeout,
    clearTimeout
  };
  ctx.window.document = ctx.document;
  vm.createContext(ctx);
  vm.runInContext(mdEditorJS, ctx);

  const editor = new ElementStub('div', '', 'wsc-editor');
  editor.appendChild(new TextStub('hello '));
  const vspan = new ElementStub('span');
  vspan.setAttribute('data-voice-span', 'listening');
  vspan.appendChild(new TextStub('interim speech'));
  editor.appendChild(vspan);
  editor.appendChild(new TextStub('world'));

  const read = ctx.window.WsMdEditor.readEditorDom(editor);
  assert.equal(read.text, 'hello world', 'readEditorDom must exclude [data-voice-span] text');

  // Verify capsules.js source excludes data-voice-span in both isMeaningful and read().
  assert.match(capsulesJS, /!node\.hasAttribute\("data-voice-span"\)/);
  assert.match(capsulesJS, /n\.hasAttribute\("data-voice-span"\)/);

  // Verify WsMdEditor.attach skips reconcile while [data-voice-span] is live, and reconciles once removed.
  let renderCount = 0;
  const fakeField = new ElementStub('textarea', 'ctext');
  fakeField.value = 'hello **world**';
  const liveEd = new ElementStub('div', '', 'wsc-editor');
  liveEd.appendChild(new TextStub('hello **world**'));
  liveEd.appendChild(vspan);
  const fakeDoc = {
    activeElement: null,
    createElement: (t) => new ElementStub(t),
    createTextNode: (t) => new TextStub(t),
    createDocumentFragment: () => new ElementStub('fragment'),
    addEventListener() {},
    removeEventListener() {}
  };
  const fakeWin = {
    document: fakeDoc,
    getSelection: () => ({ rangeCount: 0, removeAllRanges() {}, addRange() {} }),
    WsMdParser: {
      parse: (text) => {
        renderCount++;
        return { lines: [{ start: 0, end: text.length, tokens: [], hasNewline: false }] };
      }
    }
  };
  const ctx2 = { window: fakeWin, document: fakeDoc, console, setTimeout, clearTimeout };
  vm.createContext(ctx2);
  vm.runInContext(mdEditorJS, ctx2);
  const md = ctx2.window.WsMdEditor.attach(liveEd, { field: fakeField, window: fakeWin, document: fakeDoc });
  // Initial attach skipped reconcile because vspan is live.
  assert.equal(renderCount, 0, 'reconcile must skip while [data-voice-span] is present');
  liveEd._wsmdRenderHook();
  assert.equal(renderCount, 0, 'renderHook must skip while [data-voice-span] is present');
  assert.ok(liveEd.querySelector('[data-voice-span]'), 'voice span remains intact');

  // Remove span -> _wsmdReconcile runs once.
  vspan.remove();
  liveEd._wsmdReconcile();
  assert.ok(renderCount >= 1, '_wsmdReconcile runs once span is removed');
  md.detach();
});

test('postProcess hook transitions listening -> refining -> atomic span replacement and falls back to raw text on rejection', async () => {
  const env = makeVoiceEnv();
  env.ctext.value = 'before  after';
  env.ctext.selectionStart = 7;
  env.ctext.selectionEnd = 7;

  let reconcileCalls = 0;
  env.editor._wsmdReconcile = () => { reconcileCalls++; };

  const states = [];
  env.window.WsVoiceInput.registerBackend({
    id: 'native', label: 'OS Native', kind: 'native',
    available: () => true,
    onPartial() {},
    start: () => Promise.resolve(),
    stop: () => Promise.resolve({ text: 'raw spoken' }),
    cancel: () => {}
  });

  let resolveRefine;
  const refinePromise = new Promise(r => { resolveRefine = r; });

  const handle = env.window.WsVoiceInput.attachComposer({
    document: env.doc,
    window: env.window,
    autoDiscover: false,
    postProcess: async (raw) => {
      assert.equal(raw, 'raw spoken');
      return refinePromise;
    }
  });
  handle.controller.setCapabilities({ eligible: true, order: ['native'] });

  env.btn.click();
  await flush();
  assert.equal(env.editor.querySelector('[data-voice-span]').getAttribute('data-voice-span'), 'listening');

  env.btn.click();
  await flush();
  assert.equal(handle.controller.getState(), 'refining');
  assert.equal(env.editor.querySelector('[data-voice-span]').getAttribute('data-voice-span'), 'refining');
  assert.equal(env.ctext.value, 'before  after', 'composer unchanged while refining');

  resolveRefine('Refined text.');
  await flush();
  await flush();
  assert.equal(env.ctext.value, 'before Refined text. after', 'only the exact span caret is replaced');
  assert.equal(env.editor.querySelector('[data-voice-span]'), null);
  assert.ok(reconcileCalls >= 1, 'reconcile runs once after atomic replacement');

  // Rejecting postProcess falls back to raw text safely.
  env.window.WsVoiceInput.setPostProcess(() => Promise.reject(new Error('llm down')));
  handle.destroy();
  const handle2 = env.window.WsVoiceInput.attachComposer({
    document: env.doc, window: env.window, autoDiscover: false
  });
  handle2.controller.setCapabilities({ eligible: true, order: ['native'] });
  env.ctext.value = 'hi ';
  env.ctext.selectionStart = env.ctext.selectionEnd = 3;
  env.btn.click();
  await flush();
  env.btn.click();
  await flush();
  await flush();
  assert.equal(env.ctext.value, 'hi raw spoken');

  env.window.WsVoiceInput.setPostProcess(null);
  handle2.destroy();
});

test('Spacebar 0.5s PTT: owner single-space trim rule, repeat suppression, short tap, preconditions, and release after lock-induced blur', async () => {
  const env = makeVoiceEnv();
  const trim = (v, c) => ({ ...env.window.WsVoicePTT.trimPrecedingSpacesForPTT(v, c) });

  // Direct unit checks of the owner's single-space trim rule:
  assert.deepEqual(trim(' ', 1), { start: 0, end: 1, replacement: '', nextCaret: 0, changed: true });
  assert.deepEqual(trim('   ', 3), { start: 0, end: 3, replacement: '', nextCaret: 0, changed: true });
  assert.deepEqual(trim('line1\n ', 7), { start: 6, end: 7, replacement: '', nextCaret: 6, changed: true });
  assert.deepEqual(trim('안녕 ', 3), { start: 2, end: 3, replacement: ' ', nextCaret: 3, changed: false });
  assert.deepEqual(trim('안녕  ', 4), { start: 2, end: 4, replacement: ' ', nextCaret: 3, changed: true });
  assert.deepEqual(trim('안녕   ', 5), { start: 2, end: 5, replacement: ' ', nextCaret: 3, changed: true });
  assert.deepEqual(trim('hello   world', 8), { start: 5, end: 8, replacement: ' ', nextCaret: 6, changed: true });

  // End-to-end PTT gesture checks with fake timers.
  const timers = makeFakeTimers();
  let startCount = 0;
  let stopCount = 0;
  env.window.WsVoiceInput.registerBackend({
    id: 'native', label: 'OS Native', kind: 'native',
    available: () => true,
    start: () => { startCount++; return Promise.resolve(); },
    stop: () => { stopCount++; return Promise.resolve({ text: '말하기' }); },
    cancel: () => {}
  });

  const handle = env.window.WsVoiceInput.attachComposer({
    document: env.doc,
    window: env.window,
    autoDiscover: false,
    setTimeout: timers.setTimeout,
    clearTimeout: timers.clearTimeout
  });

  // 1. Unconfigured (no resolved backend) -> Space hold does nothing.
  handle.controller.setCapabilities({ eligible: true, order: [] });
  env.ctext.value = ' ';
  env.ctext.selectionStart = env.ctext.selectionEnd = 1;
  env.editor.dispatchEvent(new FakeEvent('keydown', { key: ' ', code: 'Space', target: env.editor }));
  timers.advance(1100);
  assert.equal(startCount, 0, 'unconfigured user typing is untouched');

  // Enable backend.
  handle.controller.setCapabilities({ eligible: true, order: ['native'] });

  // 2. Short tap (< 500ms) -> does not start dictation, keeps normal space.
  env.ctext.value = '안녕 ';
  env.ctext.selectionStart = env.ctext.selectionEnd = 3;
  env.editor.dispatchEvent(new FakeEvent('keydown', { key: ' ', code: 'Space', target: env.editor }));
  timers.advance(300);
  env.doc.dispatchEvent(new FakeEvent('keyup', { key: ' ', code: 'Space' }));
  timers.advance(1100);
  assert.equal(startCount, 0, 'short tap types normal space without starting dictation');
  assert.equal(env.ctext.value, '안녕 ');

  // 3. IME / modifier / popup preconditions block PTT.
  env.editor.dispatchEvent(new FakeEvent('keydown', { key: ' ', code: 'Space', target: env.editor, isComposing: true }));
  env.editor.dispatchEvent(new FakeEvent('keydown', { key: ' ', code: 'Space', target: env.editor, keyCode: 229 }));
  env.editor.dispatchEvent(new FakeEvent('keydown', { key: ' ', code: 'Space', target: env.editor, ctrlKey: true }));
  env.editor.dispatchEvent(new FakeEvent('keydown', { key: ' ', code: 'Space', target: env.editor, shiftKey: true }));
  env.setPopupOpen(true);
  env.editor.dispatchEvent(new FakeEvent('keydown', { key: ' ', code: 'Space', target: env.editor }));
  env.setPopupOpen(false);
  timers.advance(1100);
  assert.equal(startCount, 0, 'IME, modifiers, and open menus must never trigger PTT');

  // 4. Full hold at start of empty composer: initial space trimmed to "", repeat prevented, lock-induced blur ignored, doc keyup stops & commits.
  env.ctext.value = ' ';
  env.ctext.selectionStart = env.ctext.selectionEnd = 1;
  // Simulate lock turning contenteditable="false" triggering an immediate blur on editor.
  const origSetAttr = env.editor.setAttribute.bind(env.editor);
  env.editor.setAttribute = (k, v) => {
    origSetAttr(k, v);
    if (k === 'contenteditable' && v === 'false') {
      env.editor.dispatchEvent(new FakeEvent('blur', { target: env.editor }));
    }
  };
  env.editor.dispatchEvent(new FakeEvent('keydown', { key: ' ', code: 'Space', target: env.editor }));
  const repEv = new FakeEvent('keydown', { key: ' ', code: 'Space', repeat: true, target: env.editor });
  env.editor.dispatchEvent(repEv);
  assert.equal(repEv.defaultPrevented, true, 'key-repeat spaces suppressed while holding');

  timers.advance(499);
  assert.equal(startCount, 0, 'not yet at the 500ms hold threshold');
  timers.advance(1);
  await flush();
  assert.equal(startCount, 1);
  assert.equal(stopCount, 0, 'lock-induced blur must NOT count as PTT release');
  assert.equal(env.ctext.value, '', 'empty composer leading space trimmed to "" before span insert');
  assert.equal(handle.controller.getState(), 'recording');

  // Document-level keyup releases PTT and commits final text.
  const upEv = new FakeEvent('keyup', { key: ' ', code: 'Space' });
  env.doc.dispatchEvent(upEv);
  await flush();
  await flush();
  assert.equal(upEv.defaultPrevented, true);
  assert.equal(stopCount, 1);
  assert.equal(env.ctext.value, '말하기');

  // 5. Hold after "안녕 " (where initial keydown made it "안녕  "): collapses to exactly 1 space "안녕 " before dictated text.
  env.ctext.value = '안녕  ';
  env.ctext.selectionStart = env.ctext.selectionEnd = 4;
  env.editor.dispatchEvent(new FakeEvent('keydown', { key: ' ', code: 'Space', target: env.editor }));
  timers.advance(500);
  await flush();
  assert.equal(env.ctext.value, '안녕 ', 'consecutive spaces after text collapse to exactly 1 space');
  env.doc.dispatchEvent(new FakeEvent('keyup', { key: ' ', code: 'Space' }));
  await flush();
  await flush();
  assert.equal(env.ctext.value, '안녕 말하기', 'dictated text separates cleanly with 1 space');

  handle.destroy();
});

// Models production wiring: WsCapsules attaches first, so its editor listeners
// run before PTT's, stop propagation of the real key event, and re-dispatch a
// synthetic copy on ctext (bubbling up to document). PTT then listens on both
// ctext and the editor, so one physical press reaches it twice.
function makeCapsulesForwardingPTT(opts = {}) {
  const env = makeVoiceEnv();
  const timers = makeFakeTimers();
  const counts = { start: 0, stop: 0 };
  env.window.WsVoiceInput.registerBackend(opts.backend || {
    id: 'native', label: 'OS Native', kind: 'native',
    available: () => true,
    start: () => { counts.start++; return Promise.resolve(); },
    stop: () => { counts.stop++; return Promise.resolve({ text: '말하기' }); },
    cancel: () => {}
  });
  ['keydown', 'keyup'].forEach((type) => {
    env.editor.addEventListener(type, (e) => {
      if (e.defaultPrevented) return;
      const fwd = new FakeEvent(type, {
        key: e.key, code: e.code, keyCode: e.keyCode, repeat: e.repeat,
        isComposing: !!e.isComposing, target: env.ctext
      });
      env.ctext.dispatchEvent(fwd);
      env.doc.dispatchEvent(fwd);
      if (fwd.defaultPrevented) e.preventDefault();
    });
  });
  const handle = env.window.WsVoiceInput.attachComposer({
    document: env.doc,
    window: env.window,
    autoDiscover: false,
    setTimeout: timers.setTimeout,
    clearTimeout: timers.clearTimeout
  });
  (opts.extraBackends || []).forEach((b) => env.window.WsVoiceInput.registerBackend(b));
  handle.controller.setCapabilities({ eligible: true, order: opts.order || ['native'] });
  env.ctext.value = opts.value == null ? '안녕 ' : opts.value;
  env.ctext.selectionStart = env.ctext.selectionEnd = env.ctext.value.length;
  const down = (init) => env.editor.dispatchEvent(new FakeEvent('keydown', { key: ' ', code: 'Space', keyCode: 32, target: env.editor, ...(init || {}) }));
  const up = () => env.editor.dispatchEvent(new FakeEvent('keyup', { key: ' ', code: 'Space', keyCode: 32, target: env.editor }));
  return { env, timers, counts, handle, down, up };
}

test('PTT with capsules key forwarding: short Space taps never trigger via an orphaned timer', async () => {
  for (const gap of [400, 600, 900]) {
    const t = makeCapsulesForwardingPTT();
    t.down();
    assert.equal(t.timers.pendingCount(), 1, 'one press arms exactly one timer despite double delivery');
    t.timers.advance(100);
    t.up();
    assert.equal(t.timers.pendingCount(), 0, 'keyup clears the only timer');
    t.timers.advance(gap - 100);
    t.down();
    t.timers.advance(80);
    t.up();
    t.timers.advance(3000);
    await flush();
    assert.equal(t.counts.start, 0, `two short taps ${gap}ms apart must not start PTT`);
    assert.equal(t.handle.controller.getState(), 'idle');
    t.handle.destroy();
  }

  // Second press held, but still under the threshold when the first press's
  // (orphaned) timer would have fired.
  const t = makeCapsulesForwardingPTT();
  t.down();
  t.timers.advance(100);
  t.up();
  t.timers.advance(320);
  t.down();
  t.timers.advance(400);
  await flush();
  assert.equal(t.counts.start, 0, 'second press at 400ms hold must not start from the first press timer');
  t.up();
  t.timers.advance(3000);
  await flush();
  assert.equal(t.counts.start, 0);
  t.handle.destroy();
});

test('PTT with capsules key forwarding: a 500ms hold starts once and stops cleanly on keyup', async () => {
  const t = makeCapsulesForwardingPTT({ value: '' });
  t.down();
  t.down({ repeat: true });
  t.timers.advance(499);
  await flush();
  assert.equal(t.counts.start, 0);
  t.timers.advance(1);
  await flush();
  assert.equal(t.counts.start, 1, 'PTT starts exactly once at 500ms');
  assert.equal(t.handle.controller.getState(), 'recording');
  assert.equal(t.timers.pendingCount(), 0, 'no second timer left to disarm the active hold');
  assert.equal(t.handle.ptt.isActive(), true, 'active hold survives the sibling listener');

  t.up();
  await flush();
  await flush();
  assert.equal(t.counts.stop, 1, 'keyup stops PTT exactly once');
  assert.equal(t.env.ctext.value, '말하기');
  assert.equal(t.handle.ptt.isArmed(), false);
  t.handle.destroy();
});

test('PTT with capsules key forwarding: a 400ms hold does not trigger PTT', async () => {
  const t = makeCapsulesForwardingPTT();
  t.down();
  t.timers.advance(400);
  t.up();
  t.timers.advance(3000);
  await flush();
  assert.equal(t.counts.start, 0);
  assert.equal(t.counts.stop, 0);
  t.handle.destroy();
});

test('PTT with capsules key forwarding: keyup on the editor alone still releases when forwarding is skipped', async () => {
  const t = makeCapsulesForwardingPTT({ value: '' });
  t.down();
  t.timers.advance(500);
  await flush();
  assert.equal(t.counts.start, 1);
  // capsules skips forwarding when the real keyup is already defaultPrevented.
  t.env.editor.dispatchEvent(new FakeEvent('keyup', { key: ' ', code: 'Space', keyCode: 32, target: t.env.editor, defaultPrevented: true }));
  await flush();
  await flush();
  assert.equal(t.counts.stop, 1, 'editor keyup releases without the forwarded copy');
  t.handle.destroy();
});

test('PTT with capsules key forwarding: composing Space, input, and pointer events disarm a pending arm', async () => {
  const t = makeCapsulesForwardingPTT();
  const disarmed = async (why, act) => {
    t.down();
    assert.equal(t.handle.ptt.isArmed(), true, why + ': armed first');
    act();
    assert.equal(t.handle.ptt.isArmed(), false, why + ': disarmed');
    assert.equal(t.timers.pendingCount(), 0, why + ': no timer left');
    t.timers.advance(3000);
    await flush();
    assert.equal(t.counts.start, 0, why + ': no PTT');
    t.up();
  };
  const fire = (target, type, init) => target.dispatchEvent(new FakeEvent(type, { target, ...(init || {}) }));

  await disarmed('isComposing Space', () => t.down({ isComposing: true }));
  await disarmed('keyCode 229 Space', () => t.down({ keyCode: 229 }));
  await disarmed('compositionstart', () => fire(t.env.editor, 'compositionstart'));
  fire(t.env.editor, 'compositionend');
  await disarmed('beforeinput (editor)', () => fire(t.env.editor, 'beforeinput', { inputType: 'insertText', data: 'a' }));
  await disarmed('input (ctext)', () => fire(t.env.ctext, 'input', { inputType: 'insertText', data: 'a' }));
  await disarmed('pointerdown', () => fire(t.env.editor, 'pointerdown'));
  await disarmed('mousedown', () => fire(t.env.ctext, 'mousedown'));

  // Composing Space never arms in the first place (IME not lost by forwarding).
  t.down({ isComposing: true, keyCode: 229 });
  assert.equal(t.handle.ptt.isArmed(), false);
  assert.equal(t.timers.pendingCount(), 0);
  t.up();

  // The arming Space's own typed character is not a disarm trigger, whether it
  // arrives as a native InputEvent (data " ") or as capsules' data-less input
  // after ctext.value already contains the new space.
  t.env.ctext.value = '안녕';
  t.env.ctext.selectionStart = t.env.ctext.selectionEnd = 2;
  t.down();
  fire(t.env.editor, 'beforeinput', { inputType: 'insertText', data: ' ' });
  t.env.ctext.value = '안녕 ';
  fire(t.env.ctext, 'input');
  fire(t.env.editor, 'input', { inputType: 'insertText', data: ' ' });
  assert.equal(t.handle.ptt.isArmed(), true, 'own Space insertion keeps the hold armed');
  t.timers.advance(500);
  await flush();
  assert.equal(t.counts.start, 1);
  t.up();
  await flush();
  await flush();
  assert.equal(t.counts.stop, 1);
  t.handle.destroy();
});

test('PTT default hold is 500ms: the exported default and an unconfigured composer both start at exactly 500ms', async () => {
  const t = makeCapsulesForwardingPTT({ value: '' });
  assert.equal(t.env.window.WsVoicePTT.DEFAULT_HOLD_MS, 500);
  t.down();
  t.timers.advance(499);
  await flush();
  assert.equal(t.counts.start, 0);
  t.timers.advance(1);
  await flush();
  assert.equal(t.counts.start, 1, 'default holdMs is 500');
  t.up();
  await flush();
  await flush();
  assert.equal(t.counts.stop, 1);
  t.handle.destroy();
});

test('PTT held Space: its own space input plus key-repeat ticks before and after 500ms neither disarm nor stop; keyup stops once', async () => {
  const fire = (target, type, init) => target.dispatchEvent(new FakeEvent(type, { target, ...(init || {}) }));
  // `leaky` models a repeat the browser typed anyway (not suppressed in time).
  for (const leaky of [false, true]) {
    const t = makeCapsulesForwardingPTT({ value: '안녕' });
    const { env } = t;
    env.ctext.selectionStart = env.ctext.selectionEnd = 2;
    const typeSpace = () => {
      fire(env.editor, 'beforeinput', { inputType: 'insertText', data: ' ' });
      env.ctext.value += ' ';
      env.ctext.selectionStart = env.ctext.selectionEnd = env.ctext.value.length;
      fire(env.ctext, 'input'); // capsules' data-less forwarded input
      fire(env.editor, 'input', { inputType: 'insertText', data: ' ' });
    };
    t.down();
    typeSpace();
    assert.equal(t.handle.ptt.isArmed(), true, 'own space input keeps the hold armed');
    // Repeat ticks before holdMs (macOS repeat starts ~225-400ms).
    for (const at of [250, 100]) {
      t.timers.advance(at);
      const rep = new FakeEvent('keydown', { key: ' ', code: 'Space', keyCode: 32, repeat: true, target: env.editor });
      env.editor.dispatchEvent(rep);
      assert.equal(rep.defaultPrevented, true, 'repeat Space is suppressed');
      if (leaky) typeSpace();
      assert.equal(t.handle.ptt.isArmed(), true, 'repeat tick must not disarm the pending hold');
    }
    assert.equal(t.counts.start, 0);
    assert.equal(t.timers.pendingCount(), 1, 'still exactly one timer');
    t.timers.advance(150);
    await flush();
    assert.equal(t.counts.start, 1, 'starts at 500ms despite repeats');
    assert.equal(t.handle.controller.getState(), 'recording');
    // Repeat ticks while recording: composer is locked/blurred, so they land on
    // the editor, ctext or the page.
    for (const target of [env.editor, env.ctext, env.doc]) {
      fire(target, 'keydown', { key: ' ', code: 'Space', keyCode: 32, repeat: true });
      fire(env.editor, 'blur');
      fire(env.ctext, 'input'); // value no longer matters once active
      await flush();
      assert.equal(t.handle.controller.getState(), 'recording', 'repeat tick / lock blur must not stop');
      assert.equal(t.handle.ptt.isActive(), true);
    }
    assert.equal(t.counts.stop, 0);
    assert.equal(t.timers.pendingCount(), 0);
    t.up();
    await flush();
    await flush();
    assert.equal(t.counts.stop, 1, 'keyup stops exactly once');
    assert.equal(t.handle.ptt.isArmed(), false);
    assert.equal(t.handle.ptt.isActive(), false);
    t.handle.destroy();
  }
});

test('PTT held Space under a Korean IME: repeats reported as keyCode 229 / key Process do not disarm, and its keyup stops', async () => {
  const t = makeCapsulesForwardingPTT({ value: '' });
  const imeRepeat = { key: 'Process', code: '', keyCode: 229, repeat: true };
  t.down();
  t.timers.advance(300);
  const rep = new FakeEvent('keydown', { ...imeRepeat, target: t.env.editor });
  t.env.editor.dispatchEvent(rep);
  assert.equal(t.handle.ptt.isArmed(), true, 'IME repeat tick keeps the hold armed');
  assert.equal(rep.defaultPrevented, true);
  t.timers.advance(200);
  await flush();
  assert.equal(t.counts.start, 1);
  t.env.editor.dispatchEvent(new FakeEvent('keydown', { ...imeRepeat, target: t.env.editor }));
  await flush();
  assert.equal(t.handle.controller.getState(), 'recording');
  t.env.editor.dispatchEvent(new FakeEvent('keyup', { key: 'Process', code: '', keyCode: 229, target: t.env.editor }));
  await flush();
  await flush();
  assert.equal(t.counts.stop, 1, 'IME-flavoured Space keyup releases the hold');
  t.handle.destroy();
});

test('PTT still disarms cleanly on non-Space activity after Space repeats, with zero orphaned timers', async () => {
  const fire = (target, type, init) => target.dispatchEvent(new FakeEvent(type, { target, ...(init || {}) }));
  const cases = {
    'other key': (t) => t.env.editor.dispatchEvent(new FakeEvent('keydown', { key: 'a', code: 'KeyA', keyCode: 65, target: t.env.editor })),
    'other key repeat': (t) => t.env.editor.dispatchEvent(new FakeEvent('keydown', { key: 'a', code: 'KeyA', keyCode: 65, repeat: true, target: t.env.editor })),
    'typed non-space input': (t) => fire(t.env.editor, 'input', { inputType: 'insertText', data: 'a' }),
    'data-less paste-like input': (t) => { t.env.ctext.value += 'paste'; fire(t.env.ctext, 'input'); },
    'pointerdown': (t) => fire(t.env.editor, 'pointerdown'),
    'editor blur': (t) => fire(t.env.editor, 'blur'),
    'window blur': (t) => fire(t.env.window, 'blur'),
    'release before 500ms': (t) => t.up()
  };
  for (const [why, act] of Object.entries(cases)) {
    const t = makeCapsulesForwardingPTT({ value: 'hi' });
    t.down();
    t.timers.advance(200);
    t.env.editor.dispatchEvent(new FakeEvent('keydown', { key: ' ', code: 'Space', keyCode: 32, repeat: true, target: t.env.editor }));
    assert.equal(t.handle.ptt.isArmed(), true, why + ': still armed after repeat');
    act(t);
    assert.equal(t.handle.ptt.isArmed(), false, why + ': disarmed');
    assert.equal(t.timers.pendingCount(), 0, why + ': no orphaned timer');
    t.timers.advance(3000);
    await flush();
    assert.equal(t.counts.start, 0, why + ': no PTT');
    t.handle.destroy();
  }
});

test('PTT recovers after any failed start or error state: next 500ms hold starts and stops cleanly', async () => {
  // Root cause being pinned: a failed start leaves controller state "error",
  // and PTT used to arm only from "idle" (mic click may start from "error").
  const modes = ['reject', 'throw', 'backend-onError', 'start-then-stop-reject'];
  for (const mode of modes) {
    let behavior = mode;
    let errCb = null;
    const counts = { start: 0, stop: 0 };
    const backend = {
      id: 'native', label: 'OS Native', kind: 'native',
      available: () => true,
      onError: (cb) => { errCb = cb; },
      start: () => {
        counts.start++;
        if (behavior === 'reject') return Promise.reject(Object.assign(new Error('denied'), { code: 'mic_denied' }));
        if (behavior === 'throw') throw Object.assign(new Error('boom'), { code: 'mic_unavailable' });
        return Promise.resolve();
      },
      stop: () => {
        counts.stop++;
        if (behavior === 'start-then-stop-reject') return Promise.reject(Object.assign(new Error('x'), { code: 'upstream_error' }));
        return Promise.resolve({ text: '말하기' });
      },
      cancel: () => {}
    };
    const t = makeCapsulesForwardingPTT({ value: '', backend });

    // Attempt 1: hold until PTT fires; the start fails (or recording dies).
    t.down();
    t.timers.advance(500);
    await flush();
    if (mode === 'backend-onError') {
      assert.equal(t.handle.controller.getState(), 'recording');
      errCb({ code: 'mic_unavailable' });
    }
    if (mode === 'start-then-stop-reject') {
      assert.equal(t.handle.controller.getState(), 'recording');
      t.up();
      await flush();
      await flush();
    } else {
      t.up();
      await flush();
    }
    assert.equal(t.handle.controller.getState(), 'error', `${mode}: controller is in error state`);
    assert.equal(t.handle.ptt.isArmed(), false, `${mode}: no armed/active hold left behind`);
    assert.equal(t.handle.ptt.isActive(), false);
    assert.equal(t.env.ctext.disabled, false, `${mode}: composer unlocked after failure`);
    const startsBefore = counts.start;
    const stopsBefore = counts.stop;

    // Attempt 2: backend now healthy; a fresh 500ms hold must work.
    behavior = 'ok';
    t.down();
    assert.equal(t.handle.ptt.isArmed(), true, `${mode}: Space arms from error state`);
    t.timers.advance(499);
    await flush();
    assert.equal(counts.start, startsBefore, `${mode}: not before 500ms`);
    t.timers.advance(1);
    await flush();
    assert.equal(counts.start, startsBefore + 1, `${mode}: PTT starts after recovery`);
    assert.equal(t.handle.controller.getState(), 'recording');
    t.up();
    await flush();
    await flush();
    assert.equal(counts.stop, stopsBefore + 1, `${mode}: keyup stops PTT once`);
    assert.equal(t.handle.controller.getState(), 'idle');
    assert.equal(t.env.ctext.value, '말하기');
    t.handle.destroy();
  }
});

test('PTT drops a stale active hold (lost keyup, failed recording) so the next press can arm', async () => {
  let errCb = null;
  const counts = { start: 0 };
  const backend = {
    id: 'native', label: 'OS Native', kind: 'native', available: () => true,
    onError: (cb) => { errCb = cb; },
    start: () => { counts.start++; return Promise.resolve(); },
    stop: () => Promise.resolve({ text: 'x' }),
    cancel: () => {}
  };
  const t = makeCapsulesForwardingPTT({ value: '', backend });
  t.down();
  t.timers.advance(500);
  await flush();
  assert.equal(counts.start, 1);
  errCb({ code: 'mic_unavailable' }); // recording dies while Space is still held
  assert.equal(t.handle.controller.getState(), 'error');
  // keyup never arrives; the user presses Space again.
  t.down();
  assert.equal(t.handle.ptt.isArmed(), true, 'stale hold replaced by a fresh arm');
  assert.equal(t.handle.ptt.isActive(), false);
  t.timers.advance(500);
  await flush();
  assert.equal(counts.start, 2);
  t.up();
  await flush();
  await flush();
  assert.equal(t.handle.controller.getState(), 'idle');
  t.handle.destroy();
});

// OS Native -> next backend fallback on unavailability.
function makeFallbackEnv(nativeMode, opts = {}) {
  const log = [];
  let nativeErr = null, nativePartial = null;
  const native = {
    id: 'native', label: 'OS Native', kind: 'native', available: () => true,
    onError: (cb) => { nativeErr = cb; },
    onPartial: (cb) => { nativePartial = cb; },
    start: () => {
      log.push('native.start');
      if (nativeMode.reject) return Promise.reject(Object.assign(new Error('n'), { code: nativeMode.reject }));
      if (nativeMode.throw) throw Object.assign(new Error('n'), { code: nativeMode.throw });
      return Promise.resolve();
    },
    stop: () => { log.push('native.stop'); return Promise.resolve({ text: 'native-text' }); },
    cancel: () => { log.push('native.cancel'); }
  };
  const router = {
    id: 'openrouter', label: 'OpenRouter', kind: 'external', available: () => opts.routerAvailable !== false,
    start: () => { log.push('router.start'); return Promise.resolve(); },
    stop: () => { log.push('router.stop'); return Promise.resolve({ text: '말하기' }); },
    cancel: () => { log.push('router.cancel'); }
  };
  const t = makeCapsulesForwardingPTT({ value: '', backend: native, extraBackends: [router], order: opts.order || ['native', 'openrouter'] });
  return Object.assign(t, { log, fireNativeError: (c) => nativeErr({ code: c }), nativePartial: (x) => nativePartial(x) });
}

test('native unavailable on start rejection or throw falls back to the next backend; Space keyup stops the fallback', async () => {
  for (const mode of [{ reject: 'recognizer_unavailable' }, { throw: 'bridge_error' }, { reject: 'unavailable' }, { reject: 'privacy_disabled' }, { reject: 'language_unavailable' }]) {
    const t = makeFallbackEnv(mode);
    t.down();
    t.timers.advance(500);
    await flush();
    assert.deepEqual(t.log, ['native.start', 'native.cancel', 'router.start'], JSON.stringify(mode));
    assert.equal(t.handle.controller.getState(), 'recording', 'the held gesture keeps recording on the fallback');
    t.up();
    await flush(); await flush();
    assert.deepEqual(t.log.slice(3), ['router.stop'], 'keyup stops the fallback backend once');
    assert.equal(t.handle.controller.getState(), 'idle');
    assert.equal(t.env.ctext.value, '말하기');
    assert.equal(t.env.ctext.disabled, false);
    t.handle.destroy();
  }
});

test('an early async native onError (LSR 201 after start resolved) falls back, also from the mic button', async () => {
  const t = makeFallbackEnv({});
  t.env.btn.click();
  await flush();
  assert.equal(t.handle.controller.getState(), 'recording');
  t.fireNativeError('recognizer_unavailable');
  await flush();
  assert.deepEqual(t.log, ['native.start', 'native.cancel', 'router.start']);
  assert.equal(t.handle.controller.getState(), 'recording');
  t.fireNativeError('recognizer_unavailable'); // a late duplicate from the released native attempt is ignored
  assert.equal(t.handle.controller.getState(), 'recording');
  t.env.btn.click();
  await flush(); await flush();
  assert.deepEqual(t.log.slice(3), ['router.stop']);
  assert.equal(t.env.ctext.value, '말하기');
  t.handle.destroy();
});

test('native fallback is refused for mic errors, after a transcript, after release, with no next backend, and never loops', async () => {
  // mic_denied: any recorder would hit the same microphone.
  for (const mode of [{ reject: 'mic_denied' }, { reject: 'mic_unavailable' }, { reject: 'permission_denied' }, { reject: 'cancelled' }]) {
    const t = makeFallbackEnv(mode);
    t.env.btn.click();
    await flush();
    assert.equal(t.log.includes('router.start'), false, JSON.stringify(mode));
    assert.equal(t.handle.controller.getState(), mode.reject === 'cancelled' ? 'idle' : 'error');
    t.handle.destroy();
  }
  // Transcript already received.
  let t = makeFallbackEnv({});
  t.env.btn.click(); await flush();
  t.nativePartial('hel');
  t.fireNativeError('recognizer_unavailable');
  assert.equal(t.handle.controller.getState(), 'error');
  assert.equal(t.log.includes('router.start'), false);
  t.handle.destroy();
  // Released before the error arrived: the gesture is over, no surprise recording.
  t = makeFallbackEnv({});
  t.env.btn.click(); await flush();
  t.env.btn.click(); // stop -> transcribing
  t.fireNativeError('recognizer_unavailable');
  assert.equal(t.log.includes('router.start'), false);
  assert.equal(t.handle.controller.getState(), 'error');
  t.handle.destroy();
  // Fallback backend unavailable or absent from the order: error with the unavailable label.
  for (const o of [{ routerAvailable: false }, { order: ['native'] }]) {
    t = makeFallbackEnv({ reject: 'recognizer_unavailable' }, o);
    t.env.btn.click(); await flush();
    assert.equal(t.handle.controller.getState(), 'error');
    assert.equal(t.log.includes('router.start'), false);
    assert.equal(t.env.cap.querySelector('.cvox-text').textContent, 'js.voice.err.unavailable');
    t.handle.destroy();
  }
  // A failing fallback ends in error and never falls back again.
  t = makeFallbackEnv({ reject: 'recognizer_unavailable' });
  t.env.window.WsVoiceInput.getBackend('openrouter').start = () => { t.log.push('router.start'); return Promise.reject(Object.assign(new Error('r'), { code: 'unavailable' })); };
  t.env.btn.click(); await flush(); await flush();
  assert.equal(t.log.filter((x) => x === 'router.start').length, 1);
  assert.equal(t.log.filter((x) => x === 'native.start').length, 1);
  assert.equal(t.handle.controller.getState(), 'error');
  t.handle.destroy();
});

test('dictation commit normalizes the leading space to exactly one (or none at composer/line start) for mic button and Space hold', async () => {
  // [value before the gesture, dictated text, expected composer value]
  const cases = [
    ['안녕', '하세요', '안녕 하세요'],
    ['안녕 ', '하세요', '안녕 하세요'],
    ['안녕   ', '하세요', '안녕 하세요'],
    ['안녕\t ', '하세요', '안녕 하세요'],
    ['', '하세요', '하세요'],
    ['   ', '하세요', '하세요'],
    ['첫줄\n', '둘째', '첫줄\n둘째'],
    ['첫줄\n   ', '둘째', '첫줄\n둘째'],
    ['안녕', '  하세요  ', '안녕 하세요'],
  ];
  for (const via of ['mic', 'space']) {
    for (const [before, said, want] of cases) {
      const t = makeCapsulesForwardingPTT({
        value: before,
        backend: {
          id: 'native', label: 'OS Native', kind: 'native', available: () => true,
          start: () => Promise.resolve(), stop: () => Promise.resolve({ text: said }), cancel: () => {}
        }
      });
      if (via === 'mic') {
        t.env.btn.click();
        await flush();
        t.env.btn.click();
      } else {
        // The arming Space types one more space before the hold timer fires.
        t.env.ctext.value = before + ' ';
        t.env.ctext.selectionStart = t.env.ctext.selectionEnd = t.env.ctext.value.length;
        t.down();
        t.timers.advance(500);
        await flush();
        t.up();
      }
      await flush();
      await flush();
      assert.equal(t.env.ctext.value, want, `${via}: ${JSON.stringify(before)} + ${JSON.stringify(said)}`);
      assert.equal(t.handle.controller.getState(), 'idle');
      t.handle.destroy();
    }
  }
  // A blank dictation leaves the composer exactly as it was.
  const blank = makeCapsulesForwardingPTT({
    value: '안녕   ',
    backend: { id: 'native', label: 'OS Native', kind: 'native', available: () => true, start: () => Promise.resolve(), stop: () => Promise.resolve({ text: '  ' }), cancel: () => {} }
  });
  blank.env.btn.click(); await flush(); blank.env.btn.click(); await flush(); await flush();
  assert.equal(blank.env.ctext.value, '안녕   ');
  blank.handle.destroy();
});

test('macOS LSR 201 (recognizer_unavailable) shows the unavailable label, not the generic failure', async () => {
  const label = makeVoiceEnv().window.WsVoicePTT.errorLabel;
  for (const code of ['recognizer_unavailable', 'unavailable', 'privacy_disabled', 'language_unavailable']) {
    assert.equal(label({ code }), 'js.voice.err.unavailable', code);
  }
  assert.equal(label({ code: 'something_else' }), 'js.voice.err.failed');
  assert.equal(label({ code: 'mic_denied' }), 'js.voice.err.mic');
  const t = makeCapsulesForwardingPTT({
    value: '',
    backend: {
      id: 'native', label: 'OS Native', kind: 'native', available: () => true,
      start: () => Promise.reject(Object.assign(new Error('201'), { code: 'recognizer_unavailable' })),
      stop: () => Promise.resolve({ text: '' }), cancel: () => {}
    }
  });
  t.env.btn.click();
  await flush();
  assert.equal(t.handle.controller.getState(), 'error');
  assert.equal(t.env.cap.querySelector('.cvox-text').textContent, 'js.voice.err.unavailable');
  t.handle.destroy();
});

// #cvox pointer press-and-hold PTT.
function pointerEnv(extra = {}) {
  const log = [];
  const backend = {
    id: 'native', label: 'OS Native', kind: 'native', available: () => true,
    start: () => { log.push('start'); return extra.startReject ? Promise.reject(Object.assign(new Error('x'), { code: extra.startReject })) : Promise.resolve(); },
    stop: () => { log.push('stop'); return Promise.resolve({ text: '말하기' }); },
    cancel: () => { log.push('cancel'); }
  };
  const t = makeCapsulesForwardingPTT({ value: '', backend, extraBackends: extra.extraBackends, order: extra.order });
  const press = (init) => {
    const e = new FakeEvent('pointerdown', { button: 0, pointerId: 1, isPrimary: true, pointerType: 'mouse', ...(init || {}) });
    t.env.btn.dispatchEvent(e);
    return e;
  };
  const release = (type = 'pointerup', target = t.env.window, init) => target.dispatchEvent(new FakeEvent(type, { pointerId: 1, ...(init || {}) }));
  const click = (detail) => t.env.btn.dispatchEvent(new FakeEvent('click', { detail }));
  return Object.assign(t, { log, press, release, click });
}

test('mic button pointer press starts immediately; release anywhere stops once; the trailing click does not restart', async () => {
  for (const where of ['button', 'window']) {
    const t = pointerEnv();
    const e = t.press();
    assert.equal(e.defaultPrevented, true, 'pointerdown must not steal focus from the composer');
    await flush();
    assert.deepEqual(t.log, ['start'], 'no 1s hold delay on the button');
    assert.equal(t.handle.controller.getState(), 'recording');
    t.release('pointerup', where === 'button' ? t.env.btn : t.env.window);
    if (where === 'button') t.env.window.dispatchEvent(new FakeEvent('pointerup', { pointerId: 1 })); // bubbles to window in a browser
    await flush(); await flush();
    assert.deepEqual(t.log, ['start', 'stop']);
    assert.equal(t.env.ctext.value, '말하기');
    t.click(1); // the click the browser synthesizes after a press released on the button
    await flush();
    assert.deepEqual(t.log, ['start', 'stop'], 'the trailing click must not start again');
    assert.equal(t.handle.controller.getState(), 'idle');
    t.timers.advance(0);
    t.click(1); // a later, separate click is an ordinary click again
    await flush();
    assert.deepEqual(t.log, ['start', 'stop', 'start']);
    t.handle.destroy();
  }
});

test('mic button pointercancel and window blur stop cleanly; unrelated pointers are ignored', async () => {
  for (const kind of ['pointercancel', 'blur']) {
    const t = pointerEnv();
    t.press();
    await flush();
    t.release('pointerup', t.env.window, { pointerId: 9 });
    assert.equal(t.handle.controller.getState(), 'recording', 'another pointer cannot end this hold');
    t.release(kind);
    await flush(); await flush();
    assert.deepEqual(t.log, ['start', 'stop'], kind);
    assert.equal(t.handle.controller.getState(), 'idle');
    t.release('pointerup'); // a duplicate release never stops twice
    await flush();
    assert.deepEqual(t.log, ['start', 'stop']);
    t.handle.destroy();
  }
});

test('mic button ignores right/middle presses and non-primary pointers; keyboard click still toggles', async () => {
  const t = pointerEnv();
  assert.equal(t.press({ button: 2 }).defaultPrevented, false);
  t.press({ button: 1 });
  t.press({ isPrimary: false });
  await flush();
  assert.deepEqual(t.log, []);
  assert.equal(t.handle.controller.getState(), 'idle');
  t.click(0); // Enter/Space on the focused button
  await flush();
  assert.deepEqual(t.log, ['start']);
  t.click(0);
  await flush(); await flush();
  assert.deepEqual(t.log, ['start', 'stop']);
  // A pointer press while already recording keeps click-to-stop behaviour.
  t.click(0); await flush();
  const e = t.press();
  assert.equal(e.defaultPrevented, false);
  t.release();
  t.click(1);
  await flush(); await flush();
  assert.deepEqual(t.log, ['start', 'stop', 'start', 'stop']);
  t.handle.destroy();
});

test('mic button press starts cleanly from the error state', async () => {
  const t = pointerEnv({ startReject: 'mic_denied' });
  t.press();
  await flush();
  assert.equal(t.handle.controller.getState(), 'error');
  t.release();
  await flush();
  t.timers.advance(0);
  t.env.window.WsVoiceInput.getBackend('native').start = () => { t.log.push('start2'); return Promise.resolve(); };
  t.press();
  await flush();
  assert.equal(t.handle.controller.getState(), 'recording');
  assert.equal(t.log.filter((x) => x === 'start2').length, 1);
  t.release();
  await flush(); await flush();
  assert.equal(t.handle.controller.getState(), 'idle');
  assert.equal(t.env.ctext.value, '말하기');
  t.handle.destroy();
});

test('mic button hold on a native that falls back: pointerup stops the fallback backend', async () => {
  const log = [];
  const router = {
    id: 'openrouter', label: 'OpenRouter', kind: 'external', available: () => true,
    start: () => { log.push('router.start'); return Promise.resolve(); },
    stop: () => { log.push('router.stop'); return Promise.resolve({ text: '폴백' }); },
    cancel: () => { log.push('router.cancel'); }
  };
  const t = pointerEnv({ startReject: 'recognizer_unavailable', extraBackends: [router], order: ['native', 'openrouter'] });
  t.press();
  await flush(); await flush();
  assert.deepEqual(t.log, ['start', 'cancel']);
  assert.deepEqual(log, ['router.start']);
  assert.equal(t.handle.controller.getState(), 'recording');
  t.release();
  await flush(); await flush();
  assert.deepEqual(log, ['router.start', 'router.stop']);
  assert.equal(t.env.ctext.value, '폴백');
  t.click(1);
  await flush();
  assert.deepEqual(log, ['router.start', 'router.stop'], 'trailing click must not restart the fallback');
  t.handle.destroy();
});

test('destroy during a held press stops the dictation and drops the listeners', async () => {
  const t = pointerEnv();
  t.press();
  await flush();
  t.handle.destroy();
  await flush();
  assert.equal(t.log.includes('stop') || t.log.includes('cancel'), true);
  t.press();
  await flush();
  assert.equal(t.log.filter((x) => x === 'start').length, 1, 'destroyed handle no longer reacts to the pointer');
});

test('dismissing the error capsule returns the controller to idle', async () => {
  const t = makeCapsulesForwardingPTT({
    value: '',
    backend: {
      id: 'native', label: 'OS Native', kind: 'native', available: () => true,
      start: () => Promise.reject(Object.assign(new Error('d'), { code: 'mic_denied' })),
      stop: () => Promise.resolve({ text: '' }), cancel: () => {}
    }
  });
  t.env.btn.click();
  await flush();
  assert.equal(t.handle.controller.getState(), 'error');
  const dismiss = t.env.cap.querySelector('[data-voice-action="dismiss"]');
  assert.ok(dismiss, 'error capsule offers a dismiss action');
  dismiss.click();
  assert.equal(t.handle.controller.getState(), 'idle');
  t.handle.destroy();
});

test('PTT detach removes every added listener and cancels pending arm', async () => {
  const t = makeCapsulesForwardingPTT();
  const names = ['keydown', 'keyup', 'blur', 'compositionstart', 'compositionend', 'mousedown', 'pointerdown', 'beforeinput', 'input'];
  const before = (el) => names.map((n) => (el.listeners.get(n) || []).length);
  const baseEditor = before(t.env.editor);
  assert.ok(baseEditor.some((n) => n > 0));
  t.down();
  assert.equal(t.timers.pendingCount(), 1);
  t.handle.destroy();
  assert.equal(t.timers.pendingCount(), 0, 'destroy clears the pending timer');
  for (const el of [t.env.ctext, t.env.editor]) {
    for (const n of names) {
      const left = (el.listeners.get(n) || []).length;
      // Only the capsules-forwarding stub (keydown/keyup on editor) may remain.
      const allowed = el === t.env.editor && (n === 'keydown' || n === 'keyup') ? 1 : 0;
      assert.equal(left, allowed, `${n} listener cleanup on ${el.id || el.className}`);
    }
  }
  assert.equal((t.env.doc.dispatchEvent(new FakeEvent('keyup', { key: ' ', code: 'Space' })), t.counts.stop), 0);
});

test('fallback #cvox-cap capsule activates when WsCapsules is absent', async () => {
  const env = makeVoiceEnv({ withCapsules: false });
  let emitPartial = null;
  env.window.WsVoiceInput.registerBackend({
    id: 'native', label: 'OS Native', kind: 'native',
    available: () => true,
    onPartial: (cb) => { emitPartial = cb; },
    start: () => Promise.resolve(),
    stop: () => Promise.resolve({ text: 'fallback ok' }),
    cancel: () => {}
  });
  const handle = env.window.WsVoiceInput.attachComposer({
    document: env.doc, window: env.window, autoDiscover: false
  });
  handle.controller.setCapabilities({ eligible: true, order: ['native'] });

  env.btn.click();
  await flush();
  assert.equal(env.cap.hidden, false, '#cvox-cap shown when WsCapsules is absent');
  emitPartial('live');
  assert.equal(env.cap.querySelector('.cvox-text').textContent, 'live');
  env.btn.click();
  await flush();
  assert.equal(env.ctext.value, 'fallback ok');
  assert.equal(env.cap.hidden, true);
  handle.destroy();
});

// Real native adapter over a fake bridge, driven through the composer so the
// controller, span, commit and PTT release paths are the production ones.
function makeNativeBridgeComposer(opts = {}) {
  const calls = [];
  const bridge = {
    status() { return true; },
    start() { calls.push('start'); return true; },
    stop() { calls.push('stop'); return opts.stopReturns === undefined ? true : opts.stopReturns; },
    cancel() { calls.push('cancel'); return true; }
  };
  const env = makeVoiceEnv({ werkschaftSpeech: bridge });
  const timers = makeFakeTimers();
  const native = env.window.WsVoiceNative.create({
    window: env.window, document: env.doc, voiceInput: env.window.WsVoiceInput, isTop: true,
    setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout, stopTimeoutMs: 4000
  });
  const speech = (detail) => env.window.dispatchEvent(new FakeCustomEvent('ws-native-speech', { detail }));
  speech({ type: 'ready', available: true, on_device: true, locale: 'ko-KR', reason: '' });
  const handle = env.window.WsVoiceInput.attachComposer({
    document: env.doc, window: env.window, autoDiscover: false,
    setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout
  });
  handle.controller.setCapabilities({ eligible: true, order: ['native'] });
  env.ctext.value = opts.value == null ? '안녕 ' : opts.value;
  env.ctext.selectionStart = env.ctext.selectionEnd = env.ctext.value.length;
  const click = () => env.btn.dispatchEvent(new FakeEvent('click', { detail: 0 }));
  return { env, timers, calls, native, speech, handle, click };
}

test('native unsolicited Final mid-recording commits at once, normalizes the space, and a later stop/release is a silent no-op', async () => {
  const t = makeNativeBridgeComposer({ value: '안녕   ' });
  const errors = [];
  t.handle.controller.getState();
  t.click();
  await flush();
  assert.equal(t.handle.controller.getState(), 'recording');
  t.speech({ type: 'partial', text: '반갑' });
  t.speech({ type: 'final', text: '반갑습니다' });
  await flush();
  await flush();
  assert.equal(t.handle.controller.getState(), 'idle', 'controller returns to idle');
  assert.equal(t.env.ctext.value, '안녕 반갑습니다', 'single leading space');
  assert.equal(t.env.cap.hidden, true);
  assert.equal(t.env.ctext.disabled, false, 'composer unlocked');

  // PTT/pointer release afterwards: stop() on an idle controller changes nothing.
  const res = await t.handle.controller.stop();
  await flush();
  assert.equal(res, null);
  assert.equal(t.handle.controller.getState(), 'idle');
  assert.equal(t.env.ctext.value, '안녕 반갑습니다');
  assert.equal(t.calls.filter((c) => c === 'stop').length, 0, 'bridge.stop never called');
  assert.equal(errors.length, 0);
  t.native.destroy();
  t.handle.destroy();
});

test('native unsolicited Final with empty text commits the last partial', async () => {
  const t = makeNativeBridgeComposer({ value: '' });
  t.click();
  await flush();
  t.speech({ type: 'partial', text: '첫 번째' });
  t.speech({ type: 'partial', text: '첫 번째 문장' });
  t.speech({ type: 'final', text: '   ' });
  await flush();
  await flush();
  assert.equal(t.handle.controller.getState(), 'idle');
  assert.equal(t.env.ctext.value, '첫 번째 문장');
  t.native.destroy();
  t.handle.destroy();
});

test('native Final with empty text while Stop is pending falls back to the last partial', async () => {
  const t = makeNativeBridgeComposer({ value: '' });
  t.click();
  await flush();
  t.speech({ type: 'partial', text: '마지막 부분' });
  t.click(); // Stop
  await flush();
  assert.equal(t.handle.controller.getState(), 'transcribing');
  t.speech({ type: 'final', text: '' });
  await flush();
  await flush();
  assert.equal(t.handle.controller.getState(), 'idle');
  assert.equal(t.env.ctext.value, '마지막 부분');
  assert.equal(t.timers.pendingCount(), 0, 'stop timer cleared on settle');
  // lastPartial does not leak into the next session.
  t.click();
  await flush();
  t.click();
  await flush();
  t.speech({ type: 'final', text: '' });
  await flush();
  await flush();
  assert.equal(t.env.ctext.value, '마지막 부분', 'next empty session commits nothing');
  t.native.destroy();
  t.handle.destroy();
});

test('native Stop whose bridge never replies settles via the JS timeout with the last partial', async () => {
  const t = makeNativeBridgeComposer({ value: '' });
  t.click();
  await flush();
  t.speech({ type: 'partial', text: '들은 만큼' });
  t.click();
  await flush();
  assert.equal(t.handle.controller.getState(), 'transcribing');
  t.timers.advance(3999);
  await flush();
  assert.equal(t.handle.controller.getState(), 'transcribing', 'not before the timeout');
  t.timers.advance(1);
  await flush();
  await flush();
  assert.equal(t.handle.controller.getState(), 'idle', 'no permanent transcribing hang');
  assert.equal(t.env.ctext.value, '들은 만큼');
  assert.equal(t.timers.pendingCount(), 0);
  // A late Final after abandoning is dropped silently.
  t.speech({ type: 'final', text: '늦은 결과' });
  await flush();
  assert.equal(t.env.ctext.value, '들은 만큼');
  t.native.destroy();
  t.handle.destroy();
});

test('native bridge.stop() returning false settles at once with the last partial (or empty), and cancel clears the timer', async () => {
  const t = makeNativeBridgeComposer({ value: '', stopReturns: false });
  t.click();
  await flush();
  t.speech({ type: 'partial', text: '부분' });
  t.click();
  await flush();
  await flush();
  assert.equal(t.handle.controller.getState(), 'idle');
  assert.equal(t.env.ctext.value, '부분');
  assert.equal(t.timers.pendingCount(), 0, 'no timer left after immediate settle');

  // No partial -> empty commit, still not hanging.
  t.click();
  await flush();
  t.click();
  await flush();
  await flush();
  assert.equal(t.handle.controller.getState(), 'idle');
  assert.equal(t.env.ctext.value, '부분');

  // Cancel while a stop is pending clears its timer.
  const t2 = makeNativeBridgeComposer({ value: '' });
  t2.click();
  await flush();
  t2.click();
  await flush();
  assert.equal(t2.timers.pendingCount(), 1, 'stop timer armed');
  t2.handle.controller.cancel({ discardLast: true });
  await flush();
  assert.equal(t2.timers.pendingCount(), 0, 'cancel clears the stop timer');
  t.native.destroy(); t.handle.destroy(); t2.native.destroy(); t2.handle.destroy();
});
